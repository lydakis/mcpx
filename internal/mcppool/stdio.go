package mcppool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lydakis/mcpx/internal/buildinfo"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/processenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectStdio(ctx context.Context, scfg config.ServerConfig) (*connection, error) {
	env := make(map[string]string, len(scfg.Env)+1)
	for k, v := range scfg.Env {
		env[k] = v
	}

	cmd := exec.Command(scfg.Command, scfg.Args...)
	if cwd := strings.TrimSpace(scfg.CWD); cwd != "" {
		cmd.Dir = cwd
		env["PWD"] = cwd
	}
	cmd.Env = processenv.Merge(os.Environ(), env)

	conn := &connection{}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpx", Version: buildinfo.Version()}, &mcp.ClientOptions{
		Capabilities:   &mcp.ClientCapabilities{},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	transport := &capturingTransport{Transport: &mcp.CommandTransport{Command: cmd}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("initializing: %w", err)
	}
	conn.diagnostics = sessionDiagnostics(session, "stdio", "none")

	conn.listTools = func(ctx context.Context) ([]*mcp.Tool, error) {
		tools, cachePolicy, err := listAllToolPages(ctx, session.ListTools)
		if err != nil {
			return nil, err
		}
		setToolCacheDeadline(conn, cachePolicy.deadline, cachePolicy.cacheable, session.InitializeResult().ProtocolVersion >= modernProtocolVersion)
		return tools, nil
	}
	conn.callTool = func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	}
	conn.callToolWithInput = func(ctx context.Context, name string, args map[string]any, inputResponses mcp.InputResponseMap, requestState string) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args, InputResponses: inputResponses, RequestState: requestState})
	}
	conn.close = session.Close
	return conn, nil
}

type capturingTransport struct {
	mcp.Transport
	connection mcp.Connection
}

func (t *capturingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err == nil {
		t.connection = conn
	}
	return conn, err
}

func (t *capturingTransport) Close() error {
	if t.connection == nil {
		return nil
	}
	return t.connection.Close()
}
