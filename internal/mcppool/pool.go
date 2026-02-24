package mcppool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	p.mu.Lock()
	if current, ok := p.conns[server]; ok && current == conn {
		delete(p.conns, server)
	}
	p.mu.Unlock()

	if conn != nil && conn.close != nil {
		conn.close() //nolint: errcheck
	}
}

// ListTools returns the tools available on a server.
func (p *Pool) ListTools(ctx context.Context, server string) ([]ToolInfo, error) {
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	tools, err := conn.listTools(ctx)
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

// CallTool invokes a tool on a server.
func (p *Pool) CallTool(ctx context.Context, server, tool string, argsJSON json.RawMessage) (*mcp.CallToolResult, error) {
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	tools, err := p.ListTools(ctx, server)
	if err != nil {
		return nil, err
	}
	canonical, ok := canonicalToolName(tools, tool)
	if !ok {
		return nil, fmt.Errorf("tool %s not found on server %s", tool, server)
	}

	var toolSchema json.RawMessage
	for _, t := range tools {
		if t.Name != canonical {
			continue
		}
		toolSchema = t.InputSchema
		break
	}

	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	} else {
		args = map[string]any{}
	}

	args, err = compileToolArgs(args, toolSchema)
	if err != nil {
		return nil, err
	}

	result, err := conn.callTool(ctx, canonical, args)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}
	return result, nil
}

func canonicalToolName(tools []ToolInfo, requested string) (string, bool) {
	for _, t := range tools {
		if t.Name == requested {
			return t.Name, true
		}
	}

	alias := normalizeToolAlias(requested)
	if alias == requested {
		return "", false
	}
	for _, t := range tools {
		if t.Name == alias {
			return t.Name, true
		}
	}
	return "", false
}

func normalizeToolAlias(name string) string {
	if strings.Contains(name, "-") {
		return strings.ReplaceAll(name, "-", "_")
	}
	if strings.Contains(name, "_") {
		return strings.ReplaceAll(name, "_", "-")
	}
	return name
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

	if ok && conn.close != nil {
		conn.close()
	}
}

// CloseAll disconnects all servers.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*connection)
	p.mu.Unlock()

	for _, conn := range conns {
		if conn.close != nil {
			conn.close()
		}
	}
}
