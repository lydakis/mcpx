package cli

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func BenchmarkRunServerToolListHotPath(b *testing.B) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	oldOut := rootStdout
	oldErr := rootStderr
	b.Cleanup(func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
		rootStdout = oldOut
		rootStderr = oldErr
	})

	rootStdout = io.Discard
	rootStderr = io.Discard

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				if req.Type != "list_tools" || req.Server != "github" {
					b.Fatalf("unexpected request: %#v", req)
				}
				return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`[]`)}, nil
			},
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if code := Run([]string{"github"}); code != ipc.ExitOK {
			b.Fatalf("Run([github]) = %d, want %d", code, ipc.ExitOK)
		}
	}
}

func BenchmarkRunRootJSONHotPath(b *testing.B) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	oldOut := rootStdout
	oldErr := rootStderr
	b.Cleanup(func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
		rootStdout = oldOut
		rootStderr = oldErr
	})

	rootStdout = io.Discard
	rootStderr = io.Discard

	payload, err := json.Marshal([]serverListEntry{
		{Name: "beta", Origin: config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/config.toml")},
		{Name: "alpha", Origin: config.NewServerOrigin(config.ServerOriginKindCodexApps, "")},
	})
	if err != nil {
		b.Fatalf("json.Marshal(server list payload): %v", err)
	}

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				if req.Type != "list_servers" {
					b.Fatalf("unexpected request type: %s", req.Type)
				}
				return &ipc.Response{ExitCode: ipc.ExitOK, Content: payload}, nil
			},
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if code := Run([]string{"--json"}); code != ipc.ExitOK {
			b.Fatalf("Run([--json]) = %d, want %d", code, ipc.ExitOK)
		}
	}
}
