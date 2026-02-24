package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/daemon"
	"github.com/lydakis/mcpx/internal/ipc"
)

// Run is the main CLI entry point. Returns an exit code.
func Run(args []string) int {
	if handled, code := handleRootFlags(args); handled {
		return code
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}

	if ferr := config.MergeFallbackServers(cfg); ferr != nil {
		fmt.Fprintf(os.Stderr, "mcpx: warning: failed to load fallback MCP server config: %v\n", ferr)
	}

	if handled, code := maybeHandleCompletionCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if handled, code := maybeHandleSkillCommand(args, cfg, rootStdout, rootStderr); handled {
		return code
	}

	if verr := config.Validate(cfg); verr != nil {
		fmt.Fprintf(os.Stderr, "mcpx: invalid config: %v\n", verr)
		return ipc.ExitUsageErr
	}

	// No args: list servers
	if len(args) == 0 {
		return listServers(cfg)
	}

	server := args[0]
	if _, ok := cfg.Servers[server]; !ok {
		fmt.Fprintf(os.Stderr, "mcpx: unknown server: %s\n", server)
		fmt.Fprintf(os.Stderr, "Available servers:\n")
		for name := range cfg.Servers {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		return ipc.ExitUsageErr
	}

	// Connect to daemon
	nonce, err := daemon.SpawnOrConnect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	client := ipc.NewClient(ipc.SocketPath(), nonce)

	// One arg (server only): list tools
	if len(args) == 1 {
		return listTools(client, server)
	}

	tool := args[1]
	toolArgs := args[2:]

	return callTool(client, server, tool, toolArgs)
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

func listServers(cfg *config.Config) int {
	if len(cfg.Servers) == 0 {
		fmt.Println("No MCP servers configured.")
		fmt.Printf("Create a config file at %s\n", config.ExampleConfigPath())
		return 0
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Println(name)
	}
	return 0
}

func listTools(client *ipc.Client, server string) int {
	resp, err := client.Send(&ipc.Request{
		Type:   "list_tools",
		Server: server,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(os.Stderr, resp.Stderr)
	}
	os.Stdout.Write(resp.Content)
	return resp.ExitCode
}

func showHelp(client *ipc.Client, server, tool string) int {
	resp, err := client.Send(&ipc.Request{
		Type:   "tool_schema",
		Server: server,
		Tool:   tool,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(os.Stderr, resp.Stderr)
		return resp.ExitCode
	}

	toolName, desc, inputSchema, outputSchema := parseToolHelpPayload(resp.Content)
	if inputSchema == nil {
		os.Stdout.Write(resp.Content)
		return 0
	}

	if toolName == "" {
		toolName = toKebabToolName(tool)
	}

	printToolHelp(os.Stdout, server, toolName, desc, inputSchema, outputSchema)
	if _, err := writeManPage(server, toolName, desc, inputSchema, outputSchema); err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: warning: failed to write man page: %v\n", err)
	}
	return 0
}

func callTool(client *ipc.Client, server, tool string, rawArgs []string) int {
	parsed, err := parseToolCallArgs(rawArgs, os.Stdin, stdinIsTTY(os.Stdin))
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		return showHelp(client, server, tool)
	}

	argsJSON, err := json.Marshal(parsed.toolArgs)
	if err != nil {
		if !parsed.quiet {
			fmt.Fprintf(os.Stderr, "mcpx: invalid arguments: %v\n", err)
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
	})
	if err != nil {
		if !parsed.quiet {
			fmt.Fprintf(os.Stderr, "mcpx: %v\n", err)
		}
		return ipc.ExitInternal
	}
	if !parsed.quiet && resp.Stderr != "" {
		fmt.Fprintln(os.Stderr, resp.Stderr)
	}
	writeToolResponse(resp, parsed.quiet, os.Stdout, os.Stderr)
	return resp.ExitCode
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
