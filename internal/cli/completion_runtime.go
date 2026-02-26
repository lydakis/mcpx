package cli

import (
	"fmt"
	"io"

	"github.com/lydakis/mcpx/internal/daemon"
	"github.com/lydakis/mcpx/internal/ipc"
)

func completeServers(stdout, stderr io.Writer) int {
	client, code := completionClient(stderr)
	if code != ipc.ExitOK {
		return code
	}

	resp, err := client.Send(&ipc.Request{
		Type: "list_servers",
		CWD:  callerWorkingDirectory(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
	}
	if resp.ExitCode != ipc.ExitOK {
		return resp.ExitCode
	}

	for _, name := range decodeServerListPayload(resp.Content) {
		fmt.Fprintln(stdout, name)
	}
	return ipc.ExitOK
}

func completeTools(server string, stdout, stderr io.Writer) int {
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

func completionClient(stderr io.Writer) (*ipc.Client, int) {
	nonce, err := daemon.SpawnOrConnect()
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return nil, ipc.ExitInternal
	}
	return ipc.NewClient(ipc.SocketPath(), nonce), ipc.ExitOK
}
