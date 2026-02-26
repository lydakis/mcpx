package mcppool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolInfo is a simplified tool descriptor returned by ListTools.
type ToolInfo struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
}

// connection wraps an MCP client with its transport.
type connection struct {
	listTools func(ctx context.Context) ([]mcp.Tool, error)
	callTool  func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)
	close     func() error
	reqMu     sync.Mutex
}

// Pool manages MCP server connections, creating them on demand.
type Pool struct {
	cfg   *config.Config
	mu    sync.Mutex
	conns map[string]*connection
}

// New creates a new connection pool.
func New(cfg *config.Config) *Pool {
	return &Pool{
		cfg:   cfg,
		conns: make(map[string]*connection),
	}
}

func (p *Pool) getOrCreate(ctx context.Context, server string) (*connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, ok := p.conns[server]; ok {
		return conn, nil
	}

	scfg, ok := p.cfg.Servers[server]
	if !ok {
		return nil, fmt.Errorf("unknown server: %s", server)
	}

	var conn *connection
	var err error

	if scfg.IsStdio() {
		conn, err = connectStdio(ctx, scfg)
	} else if scfg.IsHTTP() {
		conn, err = connectHTTP(ctx, scfg)
	} else {
		return nil, fmt.Errorf("server %s: no command or url configured", server)
	}

	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", server, err)
	}

	p.conns[server] = conn
	return conn, nil
}

func (p *Pool) invalidate(server string, conn *connection) {
	shouldClose := false
	p.mu.Lock()
	if current, ok := p.conns[server]; ok && current == conn {
		delete(p.conns, server)
		shouldClose = true
	}
	p.mu.Unlock()

	if shouldClose {
		closeConnection(conn)
	}
}

// ListTools returns the tools available on a server.
func (p *Pool) ListTools(ctx context.Context, server string) ([]ToolInfo, error) {
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	tools, err := runListTools(conn, ctx)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}

	infos := make([]ToolInfo, len(tools))
	for i, t := range tools {
		inputSchema, _ := marshalInputSchema(t)
		outputSchema, _ := marshalOutputSchema(t)
		infos[i] = ToolInfo{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		}
	}
	return infos, nil
}

// ToolSchema returns the input schema for a specific tool.
func (p *Pool) ToolSchema(ctx context.Context, server, tool string) (json.RawMessage, error) {
	info, err := p.ToolInfoByName(ctx, server, tool)
	if err != nil {
		return nil, err
	}
	return info.InputSchema, nil
}

// ToolInfoByName returns metadata and schemas for a specific tool.
func (p *Pool) ToolInfoByName(ctx context.Context, server, tool string) (*ToolInfo, error) {
	tools, err := p.ListTools(ctx, server)
	if err != nil {
		return nil, err
	}
	canonical, ok := canonicalToolName(tools, tool)
	if !ok {
		return nil, fmt.Errorf("tool %s not found on server %s", tool, server)
	}
	for _, t := range tools {
		if t.Name == canonical {
			toolCopy := t
			return &toolCopy, nil
		}
	}
	return nil, fmt.Errorf("tool %s not found on server %s", tool, server)
}

// CallToolWithInfo invokes a resolved tool on a server.
func (p *Pool) CallToolWithInfo(ctx context.Context, server string, info *ToolInfo, argsJSON json.RawMessage) (*mcp.CallToolResult, error) {
	if info == nil || info.Name == "" {
		return nil, fmt.Errorf("tool info is required")
	}

	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	args, err := compileJSONArgs(argsJSON, info.InputSchema)
	if err != nil {
		return nil, err
	}

	result, err := runCallTool(conn, ctx, info.Name, args)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}
	return result, nil
}

// CallTool invokes a tool on a server.
func (p *Pool) CallTool(ctx context.Context, server, tool string, argsJSON json.RawMessage) (*mcp.CallToolResult, error) {
	info, err := p.ToolInfoByName(ctx, server, tool)
	if err != nil {
		return nil, err
	}
	return p.CallToolWithInfo(ctx, server, info, argsJSON)
}

func compileJSONArgs(argsJSON json.RawMessage, toolSchema json.RawMessage) (map[string]any, error) {
	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	} else {
		args = map[string]any{}
	}

	return compileToolArgs(args, toolSchema)
}

func runListTools(conn *connection, ctx context.Context) ([]mcp.Tool, error) {
	conn.reqMu.Lock()
	defer conn.reqMu.Unlock()
	return conn.listTools(ctx)
}

func runCallTool(conn *connection, ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	conn.reqMu.Lock()
	defer conn.reqMu.Unlock()
	return conn.callTool(ctx, name, args)
}

func closeConnection(conn *connection) {
	if conn == nil || conn.close == nil {
		return
	}

	// Avoid blocking reset/close paths behind a long in-flight request.
	if conn.reqMu.TryLock() {
		defer conn.reqMu.Unlock()
		conn.close() //nolint: errcheck
		return
	}

	go func(c *connection) {
		c.reqMu.Lock()
		defer c.reqMu.Unlock()
		c.close() //nolint: errcheck
	}(conn)
}

func canonicalToolName(tools []ToolInfo, requested string) (string, bool) {
	for _, t := range tools {
		if t.Name == requested {
			return t.Name, true
		}
	}
	return "", false
}

func marshalInputSchema(t mcp.Tool) (json.RawMessage, error) {
	if len(t.RawInputSchema) > 0 {
		return t.RawInputSchema, nil
	}
	b, err := json.Marshal(t.InputSchema)
	return b, err
}

func marshalOutputSchema(t mcp.Tool) (json.RawMessage, error) {
	if len(t.RawOutputSchema) > 0 {
		return t.RawOutputSchema, nil
	}
	if t.OutputSchema.Type == "" {
		return nil, nil
	}
	b, err := json.Marshal(t.OutputSchema)
	return b, err
}

// Close disconnects a specific server.
func (p *Pool) Close(server string) {
	p.mu.Lock()
	conn, ok := p.conns[server]
	if ok {
		delete(p.conns, server)
	}
	p.mu.Unlock()

	if ok {
		closeConnection(conn)
	}
}

// CloseAll disconnects all servers.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*connection)
	p.mu.Unlock()

	for _, conn := range conns {
		closeConnection(conn)
	}
}

// Reset swaps the underlying config and drops all active connections.
func (p *Pool) Reset(cfg *config.Config) {
	if p == nil {
		return
	}

	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*connection)
	p.cfg = cfg
	p.mu.Unlock()

	for _, conn := range conns {
		closeConnection(conn)
	}
}
