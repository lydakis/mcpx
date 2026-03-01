package mcppool

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func benchmarkTools(count int) []mcp.Tool {
	tools := make([]mcp.Tool, 0, count)
	for i := 0; i < count; i++ {
		name := "tool_" + strconv.Itoa(i)
		tools = append(tools, mcp.Tool{
			Name:        name,
			Description: "Benchmark tool " + name,
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"query": map[string]any{"type": "string"},
					"page":  map[string]any{"type": "integer"},
				},
				Required: []string{"query"},
			},
		})
	}
	return tools
}

func benchmarkPoolWithTools(tools []mcp.Tool) *Pool {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return tools, nil
		},
		callTool: func(context.Context, string, map[string]any) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	}

	return &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"bench": {}}},
		conns: map[string]*connection{"bench": conn},
	}
}

func BenchmarkListToolsCold(b *testing.B) {
	tools := benchmarkTools(250)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := benchmarkPoolWithTools(tools)
		out, err := p.ListTools(ctx, "bench")
		if err != nil {
			b.Fatalf("ListTools() error = %v", err)
		}
		if len(out) != len(tools) {
			b.Fatalf("len(out) = %d, want %d", len(out), len(tools))
		}
	}
}

func BenchmarkListToolsHot(b *testing.B) {
	tools := benchmarkTools(250)
	ctx := context.Background()
	p := benchmarkPoolWithTools(tools)

	_, err := p.ListTools(ctx, "bench")
	if err != nil {
		b.Fatalf("ListTools(warmup) error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := p.ListTools(ctx, "bench")
		if err != nil {
			b.Fatalf("ListTools() error = %v", err)
		}
		if len(out) != len(tools) {
			b.Fatalf("len(out) = %d, want %d", len(out), len(tools))
		}
	}
}

func BenchmarkToolInfoByNameHot(b *testing.B) {
	tools := benchmarkTools(250)
	ctx := context.Background()
	p := benchmarkPoolWithTools(tools)

	_, err := p.ListTools(ctx, "bench")
	if err != nil {
		b.Fatalf("ListTools(warmup) error = %v", err)
	}

	target := "tool_180"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info, err := p.ToolInfoByName(ctx, "bench", target)
		if err != nil {
			b.Fatalf("ToolInfoByName() error = %v", err)
		}
		if info == nil || info.Name != target {
			b.Fatalf("ToolInfoByName() = %#v, want %q", info, target)
		}
	}
}

func BenchmarkCallToolWithInfo(b *testing.B) {
	ctx := context.Background()
	p := benchmarkPoolWithTools(nil)
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string"},
			"page":{"type":"integer"},
			"includeArchived":{"type":"boolean"}
		},
		"required":["query"]
	}`)
	info := &ToolInfo{
		Name:        "search_repositories",
		InputSchema: schema,
		parsedInput: parseInputSchema(schema),
	}
	args := json.RawMessage(`{"query":"mcp","page":"2","includeArchived":"false"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.CallToolWithInfo(ctx, "bench", info, args); err != nil {
			b.Fatalf("CallToolWithInfo() error = %v", err)
		}
	}
}

func BenchmarkCompileJSONArgs(b *testing.B) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string"},
			"page":{"type":"integer"},
			"includeArchived":{"type":"boolean"}
		},
		"required":["query"]
	}`)
	parsed := parseInputSchema(schema)
	args := json.RawMessage(`{"query":"mcp","page":"2","includeArchived":"true"}`)

	b.Run("raw_schema", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, err := compileJSONArgs(args, schema, nil)
			if err != nil {
				b.Fatalf("compileJSONArgs(raw) error = %v", err)
			}
			if out["query"] != "mcp" {
				b.Fatalf("query = %#v, want %q", out["query"], "mcp")
			}
		}
	})

	b.Run("parsed_schema", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, err := compileJSONArgs(args, schema, parsed)
			if err != nil {
				b.Fatalf("compileJSONArgs(parsed) error = %v", err)
			}
			if out["query"] != "mcp" {
				b.Fatalf("query = %#v, want %q", out["query"], "mcp")
			}
		}
	})
}
