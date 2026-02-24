package mcppool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCallToolCoercesArgsByInputSchema(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"page":    map[string]any{"type": "integer"},
							"score":   map[string]any{"type": "number"},
							"enabled": map[string]any{"type": "boolean"},
							"config": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"nested": map[string]any{"type": "string"},
								},
								"required": []string{"nested"},
							},
							"tags": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "integer"},
							},
						},
						Required: []string{"page", "enabled"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	raw := map[string]any{
		"page":    "2",
		"score":   "1.5",
		"enabled": "false",
		"config":  `{"nested":"x"}`,
		"tags":    []any{"1", "2"},
	}
	argsJSON, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	if _, err := p.CallTool(context.Background(), "github", "search", argsJSON); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if _, ok := calledArgs["page"].(int64); !ok {
		t.Fatalf("page type = %T, want int64", calledArgs["page"])
	}
	if calledArgs["page"] != int64(2) {
		t.Fatalf("page = %v, want 2", calledArgs["page"])
	}
	if _, ok := calledArgs["score"].(float64); !ok {
		t.Fatalf("score type = %T, want float64", calledArgs["score"])
	}
	if calledArgs["score"] != 1.5 {
		t.Fatalf("score = %v, want 1.5", calledArgs["score"])
	}
	if _, ok := calledArgs["enabled"].(bool); !ok {
		t.Fatalf("enabled type = %T, want bool", calledArgs["enabled"])
	}
	if calledArgs["enabled"] != false {
		t.Fatalf("enabled = %v, want false", calledArgs["enabled"])
	}

	configArg, ok := calledArgs["config"].(map[string]any)
	if !ok {
		t.Fatalf("config type = %T, want map[string]any", calledArgs["config"])
	}
	if configArg["nested"] != "x" {
		t.Fatalf("config.nested = %v, want x", configArg["nested"])
	}

	tags, ok := calledArgs["tags"].([]any)
	if !ok {
		t.Fatalf("tags type = %T, want []any", calledArgs["tags"])
	}
	if len(tags) != 2 || tags[0] != int64(1) || tags[1] != int64(2) {
		t.Fatalf("tags = %#v, want [1 2]", tags)
	}
}

func TestCallToolRejectsMissingRequiredArgs(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
						Required: []string{"query"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			t.Fatal("callTool should not run when required args are missing")
			return nil, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("CallTool() error = %v, want invalid params", err)
	}
}

func TestCallToolRejectsUnknownFlags(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			t.Fatal("callTool should not run for unknown flags")
			return nil, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"unexpected":"value"}`))
	if err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("CallTool() error = %v, want invalid params", err)
	}
}

func TestCallToolTreatsNoPrefixedAliasAsBooleanNegation(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"dry-run": map[string]any{"type": "boolean"},
						},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"no-dry-run":true}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["dry-run"] != false {
		t.Fatalf("dry-run = %#v, want false", calledArgs["dry-run"])
	}
	if _, ok := calledArgs["no-dry-run"]; ok {
		t.Fatalf("no-dry-run should be rewritten away, args = %#v", calledArgs)
	}
}

func TestCallToolPreservesLiteralNoPrefixedBooleanParams(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"no-color": map[string]any{"type": "boolean"},
						},
						Required: []string{"no-color"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"no-color":true}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["no-color"] != true {
		t.Fatalf("no-color = %#v, want true", calledArgs["no-color"])
	}
}
