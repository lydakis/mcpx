package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/paths"
)

func TestRunReturnsRuntimeDirErrorBeforeStartingDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/dev/null")

	err := Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "creating runtime dir") {
		t.Fatalf("Run() error = %q, want runtime-dir setup failure", err.Error())
	}
}

func TestRunStartsAndStopsOnSignal(t *testing.T) {
	notifyCalled := make(chan chan<- os.Signal, 1)
	signalNotifyPrev := signalNotifyFn
	signalStopPrev := signalStopFn
	signalNotifyFn = func(c chan<- os.Signal, _ ...os.Signal) {
		notifyCalled <- c
	}
	signalStopFn = func(c chan<- os.Signal) {}
	t.Cleanup(func() {
		signalNotifyFn = signalNotifyPrev
		signalStopFn = signalStopPrev
	})

	runtimeBase, err := os.MkdirTemp("/tmp", "mcpxrt-")
	if err != nil {
		t.Fatalf("MkdirTemp(runtime): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeBase) })

	t.Setenv("HOME", runtimeBase)
	t.Setenv("XDG_RUNTIME_DIR", runtimeBase)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(runtimeBase, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(runtimeBase, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(runtimeBase, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(runtimeBase, "data"))

	done := make(chan error, 1)
	go func() {
		done <- Run()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("Run() exited before daemon became ready: %v", err)
		default:
		}

		socketReady := false
		stateReady := false
		if _, err := os.Stat(paths.SocketPath()); err == nil {
			socketReady = true
		}
		if _, err := os.Stat(paths.StatePath()); err == nil {
			stateReady = true
		}
		if socketReady && stateReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for daemon readiness (socket=%v state=%v)", socketReady, stateReady)
		}
		time.Sleep(25 * time.Millisecond)
	}

	var sigCh chan<- os.Signal
	select {
	case sigCh = <-notifyCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not install signal handler")
	}
	sigCh <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error after signal = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after shutdown signal")
	}
}

func TestDispatchWrapperPing(t *testing.T) {
	resp := dispatch(context.Background(), &config.Config{}, nil, nil, &ipc.Request{Type: "ping"})
	if resp == nil {
		t.Fatal("dispatch(ping) response = nil, want non-nil")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("dispatch(ping) exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
}

func TestListServersWrapperReturnsConfiguredServerNames(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/config.toml"),
		},
	}

	resp := listServers(context.Background(), cfg, nil, nil)
	if resp == nil {
		t.Fatal("listServers() response = nil, want non-nil")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	var entries []serverListEntry
	if err := json.Unmarshal(resp.Content, &entries); err != nil {
		t.Fatalf("json.Unmarshal(server list) error = %v", err)
	}
	want := []serverListEntry{
		{Name: "github", Origin: config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/config.toml")},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("listServers() entries = %#v, want %#v", entries, want)
	}
}

func TestWrapperUnknownServerResponses(t *testing.T) {
	cfg := &config.Config{
		Servers:       map[string]config.ServerConfig{},
		ServerOrigins: map[string]config.ServerOrigin{},
	}

	toolsResp := listTools(context.Background(), cfg, nil, nil, "missing", false)
	if toolsResp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("listTools(missing) exit = %d, want %d", toolsResp.ExitCode, ipc.ExitUsageErr)
	}
	if toolsResp.ErrorCode != ipc.ErrorCodeUnknownServer {
		t.Fatalf("listTools(missing) errorCode = %q, want %q", toolsResp.ErrorCode, ipc.ErrorCodeUnknownServer)
	}

	schemaResp := toolSchema(context.Background(), cfg, nil, nil, "missing", "ping")
	if schemaResp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("toolSchema(missing) exit = %d, want %d", schemaResp.ExitCode, ipc.ExitUsageErr)
	}
	if schemaResp.ErrorCode != ipc.ErrorCodeUnknownServer {
		t.Fatalf("toolSchema(missing) errorCode = %q, want %q", schemaResp.ErrorCode, ipc.ErrorCodeUnknownServer)
	}

	callResp := callTool(context.Background(), cfg, nil, nil, "missing", "ping", json.RawMessage(`{}`), nil, false)
	if callResp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("callTool(missing) exit = %d, want %d", callResp.ExitCode, ipc.ExitUsageErr)
	}
	if callResp.ErrorCode != ipc.ErrorCodeUnknownServer {
		t.Fatalf("callTool(missing) errorCode = %q, want %q", callResp.ErrorCode, ipc.ErrorCodeUnknownServer)
	}
}

func TestRuntimeWrapperConstructorsAndNoopSync(t *testing.T) {
	cfg := &config.Config{
		Servers:       map[string]config.ServerConfig{},
		ServerOrigins: map[string]config.ServerOrigin{},
	}

	handler := newRuntimeRequestHandler(cfg, nil, nil)
	if handler == nil {
		t.Fatal("newRuntimeRequestHandler() = nil, want non-nil")
	}
	if handler.deps.loadConfig == nil || handler.deps.poolListTools == nil {
		t.Fatal("newRuntimeRequestHandler() deps have nil default hooks")
	}

	if catalog := newServerCatalog(cfg, nil, nil); catalog == nil {
		t.Fatal("newServerCatalog() = nil, want non-nil")
	}

	pool := mcppool.New(cfg)
	defer pool.CloseAll()
	if _, err := listServerTools(context.Background(), pool, nil, "missing"); err == nil {
		t.Fatal("listServerTools(missing) error = nil, want non-nil")
	}

	activeCWD := "/tmp/project"
	cfgHash := ""
	cfgPtr := cfg
	if err := syncRuntimeConfigForRequest("/tmp/project", &activeCWD, &cfgHash, &cfgPtr, nil, nil); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(noop same cwd) error = %v, want nil", err)
	}
}

func TestLoadValidatedConfigForCWDWrapperReturnsConfigWithEmptyWorkspace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "xdg-state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "xdg-cache"))

	cfg, err := loadValidatedConfigForCWD("")
	if err != nil {
		t.Fatalf("loadValidatedConfigForCWD(\"\") error = %v, want nil", err)
	}
	if cfg == nil {
		t.Fatal("loadValidatedConfigForCWD(\"\") = nil, want non-nil")
	}
}
