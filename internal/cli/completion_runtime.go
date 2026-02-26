package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/daemon"
	"github.com/lydakis/mcpx/internal/ipc"
)

func completeServers(stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}

	for _, name := range configuredServerNames(cfg) {
		fmt.Fprintln(stdout, name)
	}
	return ipc.ExitOK
}

func completeTools(server string, stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}
	if _, ok := cfg.Servers[server]; !ok {
		return ipc.ExitUsageErr
	}

	client, code := completionClient(stderr)
	if code != ipc.ExitOK {
		return code
	}

	resp, err := client.Send(&ipc.Request{
		Type:   "list_tools",
		Server: server,
		CWD:    callerWorkingDirectory(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
		return resp.ExitCode
	}

	entries, err := decodeToolListPayload(resp.Content)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	tools := toolListNames(entries)
	for _, tool := range tools {
		fmt.Fprintln(stdout, tool)
	}
	return ipc.ExitOK
}

func completeFlags(server, tool string, stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}
	if _, ok := cfg.Servers[server]; !ok {
		return ipc.ExitUsageErr
	}

	client, code := completionClient(stderr)
	if code != ipc.ExitOK {
		return code
	}

	resp, err := client.Send(&ipc.Request{
		Type:   "tool_schema",
		Server: server,
		Tool:   tool,
		CWD:    callerWorkingDirectory(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
		return resp.ExitCode
	}

	_, _, inputSchema, _ := parseToolHelpPayload(resp.Content)
	for _, flag := range toolFlagCompletions(inputSchema) {
		fmt.Fprintln(stdout, flag)
	}
	return ipc.ExitOK
}

func loadConfigWithFallback(stderr io.Writer) (*config.Config, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return nil, ipc.ExitInternal
	}
	if ferr := config.MergeFallbackServers(cfg); ferr != nil {
		fmt.Fprintf(stderr, "mcpx: warning: failed to load fallback MCP server config: %v\n", ferr)
	}
	return cfg, ipc.ExitOK
}

func completionClient(stderr io.Writer) (*ipc.Client, int) {
	nonce, err := daemon.SpawnOrConnect()
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return nil, ipc.ExitInternal
	}
	return ipc.NewClient(ipc.SocketPath(), nonce), ipc.ExitOK
}

func configuredServerNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
