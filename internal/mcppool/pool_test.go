package mcppool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestListToolsErrorInvalidatesConnection(t *testing.T) {
	var closed bool
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed = true
			return nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ListTools(context.Background(), "github"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil")
	}

	p.mu.Lock()
	_, ok := p.conns["github"]
	p.mu.Unlock()
	if ok {
		t.Fatal("connection was not evicted after list error")
	}
	if !closed {
		t.Fatal("connection close was not called after list error")
	}
}

func TestCallToolErrorInvalidatesConnection(t *testing.T) {
	var closed bool
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{{Name: "search"}}, nil
		},
		callTool: func(context.Context, string, map[string]any) (*mcp.CallToolResult, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed = true
			return nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", []byte(`{"q":"mcp"}`)); err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}

	p.mu.Lock()
	_, ok := p.conns["github"]
	p.mu.Unlock()
	if ok {
		t.Fatal("connection was not evicted after call error")
	}
	if !closed {
		t.Fatal("connection close was not called after call error")
	}
}

func TestCanonicalToolNameSupportsKebabSnakeAliases(t *testing.T) {
	tools := []ToolInfo{
		{Name: "search_repositories"},
		{Name: "list-issues"},
	}

	if got, ok := canonicalToolName(tools, "search-repositories"); !ok || got != "search_repositories" {
		t.Fatalf("canonicalToolName(kebab->snake) = (%q, %v), want (%q, true)", got, ok, "search_repositories")
	}

	if got, ok := canonicalToolName(tools, "list_issues"); !ok || got != "list-issues" {
		t.Fatalf("canonicalToolName(snake->kebab) = (%q, %v), want (%q, true)", got, ok, "list-issues")
	}

	if _, ok := canonicalToolName(tools, "missing-tool"); ok {
		t.Fatal("canonicalToolName(missing) = found, want not found")
	}
}

func TestCallToolResolvesKebabAliasBeforeInvocation(t *testing.T) {
	var calledWith string
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{Name: "search_repositories"},
			}, nil
		},
		callTool: func(_ context.Context, name string, _ map[string]any) (*mcp.CallToolResult, error) {
			calledWith = name
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search-repositories", []byte(`{"q":"mcp"}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledWith != "search_repositories" {
		t.Fatalf("CallTool() invoked %q, want %q", calledWith, "search_repositories")
	}
}

func TestListToolsIncludesOutputSchema(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
					},
					OutputSchema: mcp.ToolOutputSchema{
						Type: "object",
						Properties: map[string]any{
							"items": map[string]any{"type": "array"},
						},
					},
				},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	tools, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("OutputSchema is empty, want declared schema")
	}

	var parsed map[string]any
	if err := json.Unmarshal(tools[0].OutputSchema, &parsed); err != nil {
		t.Fatalf("unmarshal output schema: %v", err)
	}
	if parsed["type"] != "object" {
		t.Fatalf("output type = %v, want object", parsed["type"])
	}
}
