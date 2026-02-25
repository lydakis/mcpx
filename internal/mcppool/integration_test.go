package mcppool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const stdioHelperEnv = "GO_WANT_MCPX_STDIO_HELPER"

func TestPoolStdioIntegrationListToolsAndCallTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"stdio": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestMCPXStdioHelperProcess", "--", "stdio-helper"},
				Env: map[string]string{
					stdioHelperEnv: "1",
				},
			},
		},
	}

	pool := New(cfg)
	defer pool.CloseAll()

	tools, err := pool.ListTools(ctx, "stdio")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if tools[0].Name != "echo_tool" {
		t.Fatalf("tools[0].Name = %q, want %q", tools[0].Name, "echo_tool")
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("tools[0].OutputSchema is empty, want declared schema")
	}
	if !strings.Contains(string(tools[0].OutputSchema), `"echo"`) {
		t.Fatalf("tools[0].OutputSchema = %s, want field echo", tools[0].OutputSchema)
	}

	result, err := pool.CallTool(ctx, "stdio", "echo_tool", json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["echo"] != "hello" {
		t.Fatalf("StructuredContent[echo] = %v, want %q", typed["echo"], "hello")
	}
}

func TestPoolHTTPIntegrationListToolsCallToolAndHeaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		headerMu   sync.Mutex
		seenHeader string
	)

	mcpServer := server.NewMCPServer("mcpx-http-helper", "1.0.0")
	mcpServer.AddTool(mcp.Tool{
		Name:        "sum_values",
		Description: "Returns the sum of a and b",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			Required: []string{"a", "b"},
		},
		OutputSchema: mcp.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"total": map[string]any{"type": "number"},
			},
			Required: []string{"total"},
		},
	}, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		headerMu.Lock()
		seenHeader = request.Header.Get("X-MCPX-Test")
		headerMu.Unlock()

		total := request.GetFloat("a", 0) + request.GetFloat("b", 0)
		return mcp.NewToolResultStructuredOnly(map[string]any{"total": total}), nil
	})

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)
	defer httpServer.Close()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"http": {
				URL: httpServer.URL,
				Headers: map[string]string{
					"X-MCPX-Test": "integration",
				},
			},
		},
	}

	pool := New(cfg)
	defer pool.CloseAll()

	tools, err := pool.ListTools(ctx, "http")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sum_values" {
		t.Fatalf("ListTools() tools = %#v, want sum_values", tools)
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("tools[0].OutputSchema is empty, want declared schema")
	}

	result, err := pool.CallTool(ctx, "http", "sum_values", json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["total"] != float64(5) {
		t.Fatalf("StructuredContent[total] = %v, want 5", typed["total"])
	}

	headerMu.Lock()
	gotHeader := seenHeader
	headerMu.Unlock()
	if gotHeader != "integration" {
		t.Fatalf("seen header = %q, want %q", gotHeader, "integration")
	}
}

func TestPoolStdioIntegrationInvalidCommandFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"broken": {
				Command: "mcpx-this-command-does-not-exist",
			},
		},
	}
	pool := New(cfg)
	defer pool.CloseAll()

	if _, err := pool.ListTools(ctx, "broken"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil for invalid command")
	}
}

func TestPoolHTTPIntegrationUnavailableServerFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mcpServer := server.NewMCPServer("mcpx-http-helper", "1.0.0")
	httpServer := server.NewTestStreamableHTTPServer(mcpServer)
	url := httpServer.URL
	httpServer.Close()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"http": {URL: url},
		},
	}
	pool := New(cfg)
	defer pool.CloseAll()

	if _, err := pool.ListTools(ctx, "http"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil for unavailable server")
	}
}

func TestMCPXStdioHelperProcess(t *testing.T) {
	if os.Getenv(stdioHelperEnv) != "1" {
		return
	}

	s := server.NewMCPServer("mcpx-stdio-helper", "1.0.0")
	s.AddTool(mcp.Tool{
		Name:        "echo_tool",
		Description: "Echoes query",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string"},
			},
			Required: []string{"query"},
		},
		OutputSchema: mcp.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"echo": map[string]any{"type": "string"},
			},
			Required: []string{"echo"},
		},
	}, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := request.RequireString("query")
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultStructuredOnly(map[string]any{"echo": query}), nil
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "serve stdio helper: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
