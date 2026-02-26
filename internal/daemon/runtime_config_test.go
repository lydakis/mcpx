package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestSyncRuntimeConfigForRequestReloadsOnlyWhenCWDChanges(t *testing.T) {
	oldLoadConfigFn := loadConfigFn
	oldMergeFallbackFn := mergeFallbackFn
	oldValidateConfigFn := validateConfigFn
	oldPoolResetFn := poolResetFn
	oldKeepaliveStopFn := keepaliveStopFn
	defer func() {
		loadConfigFn = oldLoadConfigFn
		mergeFallbackFn = oldMergeFallbackFn
		validateConfigFn = oldValidateConfigFn
		poolResetFn = oldPoolResetFn
		keepaliveStopFn = oldKeepaliveStopFn
	}()

	var loadCalls int
	var mergeCalls int
	var validateCalls int
	var resetCalls int
	var stopCalls int

	loadConfigFn = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{Servers: map[string]config.ServerConfig{}}, nil
	}
	mergeFallbackFn = func(cfg *config.Config, cwd string) error {
		mergeCalls++
		if cfg.Servers == nil {
			cfg.Servers = make(map[string]config.ServerConfig)
		}
		key := cwd
		if key == "" {
			key = "default"
		}
		cfg.Servers[key] = config.ServerConfig{Command: "test-command"}
		return nil
	}
	validateConfigFn = func(*config.Config) error {
		validateCalls++
		return nil
	}
	poolResetFn = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	keepaliveStopFn = func(_ *Keepalive) {
		stopCalls++
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"old": {Command: "old"}}}
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}
	pool := mcppool.New(cfg)
	ka := NewKeepalive(pool)
	defer ka.Stop()

	activeCWD := ""
	if err := syncRuntimeConfigForRequest("/tmp/project-a", &activeCWD, &cfgHash, &cfg, pool, ka); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a) error = %v", err)
	}
	if activeCWD != "/tmp/project-a" {
		t.Fatalf("active cwd = %q, want %q", activeCWD, "/tmp/project-a")
	}
	if _, ok := cfg.Servers["/tmp/project-a"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want project-a fallback entry", cfg.Servers)
	}

	if err := syncRuntimeConfigForRequest("/tmp/project-a", &activeCWD, &cfgHash, &cfg, pool, ka); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a repeat) error = %v", err)
	}
	if loadCalls != 1 || mergeCalls != 1 || validateCalls != 1 {
		t.Fatalf("reload hooks called load=%d merge=%d validate=%d, want 1/1/1 after same-cwd repeat", loadCalls, mergeCalls, validateCalls)
	}
	if resetCalls != 1 || stopCalls != 1 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 1/1 after same-cwd repeat", resetCalls, stopCalls)
	}

	if err := syncRuntimeConfigForRequest("/tmp/project-b", &activeCWD, &cfgHash, &cfg, pool, ka); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-b) error = %v", err)
	}
	if activeCWD != "/tmp/project-b" {
		t.Fatalf("active cwd = %q, want %q", activeCWD, "/tmp/project-b")
	}
	if _, ok := cfg.Servers["/tmp/project-b"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want project-b fallback entry", cfg.Servers)
	}
	if loadCalls != 2 || mergeCalls != 2 || validateCalls != 2 {
		t.Fatalf("reload hooks called load=%d merge=%d validate=%d, want 2/2/2 after cwd change", loadCalls, mergeCalls, validateCalls)
	}
	if resetCalls != 2 || stopCalls != 2 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 2/2 after cwd change", resetCalls, stopCalls)
	}
}

func TestSyncRuntimeConfigForRequestReturnsConfigLoadErrors(t *testing.T) {
	oldLoadConfigFn := loadConfigFn
	oldMergeFallbackFn := mergeFallbackFn
	oldValidateConfigFn := validateConfigFn
	oldPoolResetFn := poolResetFn
	oldKeepaliveStopFn := keepaliveStopFn
	defer func() {
		loadConfigFn = oldLoadConfigFn
		mergeFallbackFn = oldMergeFallbackFn
		validateConfigFn = oldValidateConfigFn
		poolResetFn = oldPoolResetFn
		keepaliveStopFn = oldKeepaliveStopFn
	}()

	loadConfigFn = func() (*config.Config, error) {
		return nil, errors.New("boom")
	}
	mergeFallbackFn = func(*config.Config, string) error {
		t.Fatal("mergeFallbackFn should not be called when load fails")
		return nil
	}
	validateConfigFn = func(*config.Config) error {
		t.Fatal("validateConfigFn should not be called when load fails")
		return nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}
	activeCWD := ""
	if err := syncRuntimeConfigForRequest("/tmp/project", &activeCWD, &cfgHash, &cfg, nil, nil); err == nil {
		t.Fatal("syncRuntimeConfigForRequest() error = nil, want non-nil")
	}
}

func TestSyncRuntimeConfigForRequestSkipsResetWhenConfigFingerprintUnchanged(t *testing.T) {
	oldLoadConfigFn := loadConfigFn
	oldMergeFallbackFn := mergeFallbackFn
	oldValidateConfigFn := validateConfigFn
	oldPoolResetFn := poolResetFn
	oldKeepaliveStopFn := keepaliveStopFn
	defer func() {
		loadConfigFn = oldLoadConfigFn
		mergeFallbackFn = oldMergeFallbackFn
		validateConfigFn = oldValidateConfigFn
		poolResetFn = oldPoolResetFn
		keepaliveStopFn = oldKeepaliveStopFn
	}()

	loadConfigFn = func() (*config.Config, error) {
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"github": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
			},
		}, nil
	}
	mergeFallbackFn = func(*config.Config, string) error { return nil }
	validateConfigFn = func(*config.Config) error { return nil }

	var resetCalls int
	var stopCalls int
	poolResetFn = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	keepaliveStopFn = func(_ *Keepalive) {
		stopCalls++
	}

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
		},
	}
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}

	activeCWD := ""
	if err := syncRuntimeConfigForRequest("/tmp/project-a", &activeCWD, &cfgHash, &cfg, nil, nil); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a) error = %v", err)
	}
	if err := syncRuntimeConfigForRequest("/tmp/project-b", &activeCWD, &cfgHash, &cfg, nil, nil); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-b) error = %v", err)
	}

	if resetCalls != 0 || stopCalls != 0 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 0/0 for same effective config", resetCalls, stopCalls)
	}
	if activeCWD != "/tmp/project-b" {
		t.Fatalf("active cwd = %q, want %q", activeCWD, "/tmp/project-b")
	}
}

func TestRequestNeedsRuntimeConfig(t *testing.T) {
	cases := []struct {
		req  *ipc.Request
		want bool
	}{
		{req: nil, want: false},
		{req: &ipc.Request{Type: "shutdown"}, want: false},
		{req: &ipc.Request{Type: "list_servers"}, want: true},
		{req: &ipc.Request{Type: "list_tools"}, want: true},
		{req: &ipc.Request{Type: "tool_schema"}, want: true},
		{req: &ipc.Request{Type: "call_tool"}, want: true},
	}

	for _, tc := range cases {
		if got := requestNeedsRuntimeConfig(tc.req); got != tc.want {
			t.Fatalf("requestNeedsRuntimeConfig(%#v) = %v, want %v", tc.req, got, tc.want)
		}
	}
}

func TestRuntimeRequestHandlerAllowsConcurrentSameCWDRequests(t *testing.T) {
	restore := saveCallToolHooks()
	defer restore()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}

	ka := NewKeepalive(nil)
	defer ka.Stop()

	handler := newRuntimeRequestHandler(cfg, &mcppool.Pool{}, ka)
	handler.activeCWD = "/tmp/project"

	var inFlight int32
	var maxInFlight int32

	poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			currentMax := atomic.LoadInt32(&maxInFlight)
			if n <= currentMax {
				break
			}
			if atomic.CompareAndSwapInt32(&maxInFlight, currentMax, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return &mcppool.ToolInfo{Name: "search"}, nil
	}
	poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			StructuredContent: map[string]any{"ok": true},
		}, nil
	}

	const workers = 4
	start := make(chan struct{})
	results := make(chan *ipc.Response, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- handler.handle(context.Background(), &ipc.Request{
				Type:   "call_tool",
				Server: "github",
				Tool:   "search",
				CWD:    "/tmp/project",
				Args:   json.RawMessage(`{"query":"mcp"}`),
			})
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	for resp := range results {
		if resp == nil {
			t.Fatal("handler returned nil response")
		}
		if resp.ExitCode != ipc.ExitOK {
			t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
		}
	}

	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
		t.Fatalf("max concurrent tool resolution = %d, want >= 2 for same-CWD parallel dispatch", got)
	}
}
