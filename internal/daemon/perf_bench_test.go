package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func benchmarkCodexTools(connectors, toolsPerConnector int) []mcppool.ToolInfo {
	total := connectors * toolsPerConnector
	out := make([]mcppool.ToolInfo, 0, total)
	for c := 0; c < connectors; c++ {
		prefix := "app" + strconv.Itoa(c)
		for t := 0; t < toolsPerConnector; t++ {
			name := prefix + "_tool_" + strconv.Itoa(t)
			out = append(out, mcppool.ToolInfo{
				Name:         name,
				Description:  "Benchmark tool " + name,
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
			})
		}
	}
	return out
}

func benchmarkRuntimeHandlerForListServers() *runtimeRequestHandler {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
			"github":            {},
			"filesystem":        {},
		},
	}
	ka := NewKeepalive(nil)

	deps := runtimeDefaultDeps()
	tools := benchmarkCodexTools(30, 20)
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server == codexAppsServerName {
			return tools, nil
		}
		return nil, nil
	}

	return newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
}

func BenchmarkNonceValidationSurfaces(b *testing.B) {
	ctx := context.Background()

	b.Run("ping", func(b *testing.B) {
		handler := benchmarkRuntimeHandlerForListServers()
		req := &ipc.Request{Type: "ping"}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp := handler.handle(ctx, req)
			if resp == nil || resp.ExitCode != ipc.ExitOK {
				b.Fatalf("ping response = %#v", resp)
			}
		}
	})

	b.Run("list_servers", func(b *testing.B) {
		handler := benchmarkRuntimeHandlerForListServers()
		req := &ipc.Request{Type: "list_servers"}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp := handler.handle(ctx, req)
			if resp == nil || resp.ExitCode != ipc.ExitOK {
				b.Fatalf("list_servers response = %#v", resp)
			}
		}
	})
}
