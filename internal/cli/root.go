package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/daemon"
	"github.com/lydakis/mcpx/internal/ipc"
)

type daemonRequester interface {
	Send(req *ipc.Request) (*ipc.Response, error)
}

var (
	spawnOrConnectFn = daemon.SpawnOrConnect
	newDaemonClient  = func(socketPath, nonce string) daemonRequester {
		return ipc.NewClient(socketPath, nonce)
	}
)

// Run is the main CLI entry point. Returns an exit code.
func Run(args []string) int {
	if handled, code := handleRootFlags(args); handled {
		return code
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}

	if ferr := config.MergeFallbackServers(cfg); ferr != nil {
		fmt.Fprintf(rootStderr, "mcpx: warning: failed to load fallback MCP server config: %v\n", ferr)
	}

	if handled, code := maybeHandleCompletionCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if handled, code := maybeHandleSkillCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if handled, code := maybeHandleAddCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if handled, code := maybeHandleShimCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if verr := config.Validate(cfg); verr != nil {
		fmt.Fprintf(rootStderr, "mcpx: invalid config: %v\n", verr)
		return ipc.ExitUsageErr
	}

	inv, err := parseInvocation(args, cfg)
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}

	if inv.kind == invocationKindRootList {
		nonce, err := spawnOrConnectFn()
		if err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		client := newDaemonClient(ipc.SocketPath(), nonce)
		return listServersFromDaemon(client, callerWorkingDirectory(), inv.rootList.output, inv.rootList.verbose)
	}

	server := inv.server
	cmd := inv.serverCmd

	if cmd.list && cmd.listOpts.help {
		nonce, err := spawnOrConnectFn()
		if err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		client := newDaemonClient(ipc.SocketPath(), nonce)
		resp, err := client.Send(&ipc.Request{
			Type: "list_servers",
			CWD:  callerWorkingDirectory(),
		})
		if err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		if resp.Stderr != "" {
			fmt.Fprintln(rootStderr, resp.Stderr)
		}
		if resp.ExitCode != ipc.ExitOK {
			return resp.ExitCode
		}

		knownServers := decodeServerListPayload(resp.Content)
		if !containsServerName(knownServers, server) {
			printUnknownServer(server, knownServers)
			return ipc.ExitUsageErr
		}
		printToolListHelp(rootStdout, server)
		return ipc.ExitOK
	}

	// Connect to daemon
	nonce, err := spawnOrConnectFn()
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	client := newDaemonClient(ipc.SocketPath(), nonce)
	cwd := callerWorkingDirectory()

	if cmd.list {
		return listTools(client, server, cwd, cmd.listOpts.verbose, cmd.listOpts.output)
	}

	return callTool(client, server, cmd.tool, cmd.toolArgs, cwd)
}

func maybeHandleCompletionCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}

	switch args[0] {
	case "completion":
		if cfg != nil {
			if _, ok := cfg.Servers["completion"]; ok {
				return false, 0
			}
		}
		return true, runCompletionCommand(args[1:], stdout, stderr)
	case "__complete":
		if cfg != nil {
			if _, ok := cfg.Servers["__complete"]; ok {
				return false, 0
			}
		}
		return true, runInternalCompletion(args[1:], stdout, stderr)
	default:
		return false, 0
	}
}

type rootServerListArgs struct {
	output  outputMode
	verbose bool
}

type invocationKind int

const (
	invocationKindRootList invocationKind = iota
	invocationKindServer
)

type invocation struct {
	kind      invocationKind
	rootList  rootServerListArgs
	server    string
	serverCmd serverCommand
}

func parseInvocation(args []string, cfg *config.Config) (invocation, error) {
	if serverCmd, ok := parseConfiguredServerInvocation(args, cfg); ok {
		return invocation{
			kind:      invocationKindServer,
			server:    args[0],
			serverCmd: serverCmd,
		}, nil
	}

	rootList, isRootList, err := parseRootServerListArgs(args)
	if err != nil {
		return invocation{}, err
	}
	if isRootList {
		return invocation{
			kind:     invocationKindRootList,
			rootList: rootList,
		}, nil
	}

	if len(args) == 0 {
		return invocation{
			kind:     invocationKindRootList,
			rootList: rootServerListArgs{output: outputModeText},
		}, nil
	}

	serverCmd, err := parseServerCommand(args[1:])
	if err != nil {
		return invocation{}, err
	}
	return invocation{
		kind:      invocationKindServer,
		server:    args[0],
		serverCmd: serverCmd,
	}, nil
}

func parseConfiguredServerInvocation(args []string, cfg *config.Config) (serverCommand, bool) {
	if len(args) == 0 || cfg == nil || len(cfg.Servers) == 0 {
		return serverCommand{}, false
	}
	if _, exists := cfg.Servers[args[0]]; !exists {
		return serverCommand{}, false
	}
	serverCmd, err := parseServerCommand(args[1:])
	if err != nil {
		return serverCommand{}, false
	}
	return serverCmd, true
}

func parseRootServerListArgs(args []string) (rootServerListArgs, bool, error) {
	parsed := rootServerListArgs{output: outputModeText}
	if len(args) == 0 {
		return parsed, true, nil
	}

	for _, arg := range args {
		if !isRootServerListFlag(arg) {
			// Preserve command contract: if any token is not a root-list flag,
			// treat argv[0] as a server name instead of claiming root-list mode.
			return rootServerListArgs{}, false, nil
		}
		switch arg {
		case "--json":
			parsed.output = outputModeJSON
		case "-v", "--verbose":
			parsed.verbose = true
		}
	}

	return parsed, true, nil
}

func isRootServerListFlag(arg string) bool {
	switch arg {
	case "-v", "--verbose", "--json":
		return true
	default:
		return false
	}
}

func listServersFromDaemon(client daemonRequester, cwd string, output outputMode, verbose bool) int {
	resp, err := client.Send(&ipc.Request{
		Type: "list_servers",
		CWD:  cwd,
	})
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(rootStderr, resp.Stderr)
	}
	if resp.ExitCode != ipc.ExitOK {
		return resp.ExitCode
	}

	entries := decodeServerListEntries(resp.Content)

	if output.isJSON() {
		if !verbose {
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
			if err := writeJSONLine(rootStdout, names); err != nil {
				fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
				return ipc.ExitInternal
			}
			return ipc.ExitOK
		}

		if err := writeJSONLine(rootStdout, entries); err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		return ipc.ExitOK
	}

	if len(entries) == 0 {
		fmt.Fprintln(rootStdout, "No MCP servers configured.")
		fmt.Fprintf(rootStdout, "Create a config file at %s\n", config.ExampleConfigPath())
		return ipc.ExitOK
	}
	for _, entry := range entries {
		if !verbose {
			fmt.Fprintln(rootStdout, entry.Name)
			continue
		}
	}
	if !verbose {
		return resp.ExitCode
	}

	tw := tabwriter.NewWriter(rootStdout, 0, 0, 2, ' ', 0)
	for _, entry := range entries {
		origin := config.NormalizeServerOrigin(entry.Origin)
		source := strings.TrimSpace(string(origin.Kind))
		if source == "" {
			source = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", entry.Name, source); err != nil {
			fmt.Fprintf(rootStderr, "mcpx: writing server list output: %v\n", err)
			return ipc.ExitInternal
		}
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(rootStderr, "mcpx: writing server list output: %v\n", err)
		return ipc.ExitInternal
	}
	return resp.ExitCode
}

type toolListArgs struct {
	verbose bool
	help    bool
	output  outputMode
}

type serverCommand struct {
	list     bool
	listOpts toolListArgs
	tool     string
	toolArgs []string
}

func parseServerCommand(args []string) (serverCommand, error) {
	if len(args) == 0 {
		return serverCommand{list: true}, nil
	}

	// Force tool mode for dash-prefixed tool names:
	// mcpx <server> -- --help
	if args[0] == "--" {
		if len(args) == 1 {
			return serverCommand{}, fmt.Errorf("missing tool name after --")
		}
		return serverCommand{
			tool:     args[1],
			toolArgs: args[2:],
		}, nil
	}

	if strings.HasPrefix(args[0], "-") {
		opts, err := parseToolListArgs(args)
		if err == nil {
			return serverCommand{
				list:     true,
				listOpts: opts,
			}, nil
		}
		if isToolListFlag(args[0]) {
			return serverCommand{}, err
		}
	}

	return serverCommand{
		tool:     args[0],
		toolArgs: args[1:],
	}, nil
}

func parseToolListArgs(args []string) (toolListArgs, error) {
	parsed := toolListArgs{
		output: outputModeText,
	}
	for _, arg := range args {
		switch arg {
		case "-v", "--verbose":
			parsed.verbose = true
		case "-h", "--help":
			parsed.help = true
		case "--json":
			parsed.output = outputModeJSON
		default:
			return toolListArgs{}, fmt.Errorf("unsupported flag for tool listing: %s", arg)
		}
	}
	return parsed, nil
}

func isToolListFlag(arg string) bool {
	switch arg {
	case "-v", "--verbose", "-h", "--help", "--json":
		return true
	default:
		return false
	}
}

func printToolListHelp(out io.Writer, server string) {
	fmt.Fprintf(out, "Usage: mcpx %s [FLAGS]\n", server)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "List tools exposed by the server.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  --verbose, -v    Show full tool descriptions")
	fmt.Fprintln(out, "  --json           Emit mcpx list output as JSON")
	fmt.Fprintln(out, "  --help, -h       Show this help output")
}

func listTools(client daemonRequester, server, cwd string, verbose bool, output outputMode) int {
	resp, err := client.Send(&ipc.Request{
		Type:    "list_tools",
		Server:  server,
		Verbose: verbose,
		CWD:     cwd,
	})
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(rootStderr, resp.Stderr)
	}
	if resp.ExitCode != ipc.ExitOK {
		return resp.ExitCode
	}

	entries, err := decodeToolListPayload(resp.Content)
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}

	if output.isJSON() {
		if err := writeJSONLine(rootStdout, entries); err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		return resp.ExitCode
	}

	if err := writeToolListText(rootStdout, entries); err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	return resp.ExitCode
}

func writeJSONLine(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding json output: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing json output: %w", err)
	}
	if _, err := w.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("writing json output: %w", err)
	}
	return nil
}

func writePayload(w io.Writer, label string, payload []byte) error {
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("writing %s: %w", label, err)
	}
	return nil
}

func decodeServerListPayload(payload []byte) []string {
	entries := decodeServerListEntries(payload)
	if len(entries) == 0 {
		return nil
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

type serverListEntry struct {
	Name   string              `json:"name"`
	Origin config.ServerOrigin `json:"origin"`
}

func decodeServerListEntries(payload []byte) []serverListEntry {
	if len(payload) == 0 {
		return nil
	}

	var entries []serverListEntry
	if err := json.Unmarshal(payload, &entries); err == nil {
		seen := make(map[string]struct{}, len(entries))
		out := make([]serverListEntry, 0, len(entries))
		for _, entry := range entries {
			name := strings.TrimSpace(entry.Name)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, serverListEntry{
				Name:   name,
				Origin: config.NormalizeServerOrigin(entry.Origin),
			})
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].Name < out[j].Name
		})
		return out
	}

	lines := strings.Split(string(payload), "\n")
	seen := make(map[string]struct{}, len(lines))
	out := make([]serverListEntry, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, serverListEntry{
			Name:   name,
			Origin: config.NormalizeServerOrigin(config.ServerOrigin{}),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func containsServerName(names []string, server string) bool {
	for _, name := range names {
		if name == server {
			return true
		}
	}
	return false
}

func printUnknownServer(server string, names []string) {
	fmt.Fprintf(rootStderr, "mcpx: unknown server: %s\n", server)
	if len(names) == 0 {
		return
	}
	fmt.Fprintln(rootStderr, "Available servers:")
	for _, name := range names {
		fmt.Fprintf(rootStderr, "  %s\n", name)
	}
}

func resolvedToolHelpName(requested, payloadName string) string {
	if payloadName != "" {
		return payloadName
	}
	return requested
}

func showHelp(client daemonRequester, server, tool, cwd string, output outputMode) int {
	resp, err := client.Send(&ipc.Request{
		Type:   "tool_schema",
		Server: server,
		Tool:   tool,
		CWD:    cwd,
	})
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(rootStderr, resp.Stderr)
		return resp.ExitCode
	}

	if output.isJSON() {
		if err := writePayload(rootStdout, "help output", resp.Content); err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		return resp.ExitCode
	}

	toolName, desc, inputSchema, outputSchema := parseToolHelpPayload(resp.Content)
	if inputSchema == nil {
		if err := writePayload(rootStdout, "help output", resp.Content); err != nil {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
			return ipc.ExitInternal
		}
		return resp.ExitCode
	}
	toolName = resolvedToolHelpName(tool, toolName)

	printToolHelp(rootStdout, server, toolName, desc, inputSchema, outputSchema)
	return resp.ExitCode
}

func callTool(client daemonRequester, server, tool string, rawArgs []string, cwd string) int {
	parsed, err := parseToolCallArgs(rawArgs, os.Stdin, stdinIsTTY(os.Stdin))
	if err != nil {
		fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		return showHelp(client, server, tool, cwd, parsed.output)
	}

	argsJSON, err := json.Marshal(parsed.toolArgs)
	if err != nil {
		if !parsed.quiet {
			fmt.Fprintf(rootStderr, "mcpx: invalid arguments: %v\n", err)
		}
		return ipc.ExitUsageErr
	}

	resp, err := client.Send(&ipc.Request{
		Type:    "call_tool",
		Server:  server,
		Tool:    tool,
		Args:    argsJSON,
		Cache:   parsed.cacheTTL,
		Verbose: parsed.verbose,
		CWD:     cwd,
	})
	if err != nil {
		if !parsed.quiet {
			fmt.Fprintf(rootStderr, "mcpx: %v\n", err)
		}
		return ipc.ExitInternal
	}
	writeCallResponse(resp, parsed.quiet, rootStdout, rootStderr)
	return resp.ExitCode
}

func writeCallResponse(resp *ipc.Response, quiet bool, stdout, stderr io.Writer) {
	if resp == nil {
		return
	}
	if quiet {
		writeToolResponse(resp, true, stdout, stderr)
		return
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
	}
	writeToolResponse(resp, false, stdout, stderr)
}

func writeToolResponse(resp *ipc.Response, quiet bool, stdout, stderr io.Writer) {
	if resp == nil {
		return
	}

	if resp.ExitCode == ipc.ExitOK {
		stdout.Write(resp.Content) //nolint:errcheck
		return
	}

	if quiet {
		return
	}

	if len(resp.Content) > 0 {
		stderr.Write(resp.Content) //nolint:errcheck
	}
}

func stdinIsTTY(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return true
	}
	return info.Mode()&fs.ModeCharDevice != 0
}

func callerWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
