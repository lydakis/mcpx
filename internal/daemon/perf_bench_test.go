package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/paths"
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

func BenchmarkSpawnOrConnectHotExistingDaemon(b *testing.B) {
	runtimeDir, err := os.MkdirTemp("/tmp", "mcpxrt-")
	if err != nil {
		b.Fatalf("MkdirTemp(runtime): %v", err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	b.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		b.Fatalf("EnsureDir(runtime): %v", err)
	}

	const nonce = "bench-nonce"
	if err := os.WriteFile(paths.StatePath(), []byte(nonce+"\n"), 0600); err != nil {
		b.Fatalf("WriteFile(state): %v", err)
	}

	srv := ipc.NewServer(paths.SocketPath(), nonce, func(_ context.Context, req *ipc.Request) *ipc.Response {
		if req != nil && req.Type == "ping" {
			return &ipc.Response{ExitCode: ipc.ExitOK}
		}
		return &ipc.Response{ExitCode: ipc.ExitInternal}
	})
	if err := srv.Start(); err != nil {
		b.Fatalf("server start: %v", err)
	}
	b.Cleanup(func() { srv.Stop() })

	nonceOut, err := SpawnOrConnect()
	if err != nil {
		b.Fatalf("SpawnOrConnect(warmup) error = %v", err)
	}
	if nonceOut != nonce {
		b.Fatalf("SpawnOrConnect(warmup) nonce = %q, want %q", nonceOut, nonce)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nonceOut, err := SpawnOrConnect()
		if err != nil {
			b.Fatalf("SpawnOrConnect() error = %v", err)
		}
		if nonceOut != nonce {
			b.Fatalf("SpawnOrConnect() nonce = %q, want %q", nonceOut, nonce)
		}
	}
}
