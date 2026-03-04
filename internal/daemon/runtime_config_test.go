package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	var loadCalls int
	var mergeCalls int
	var validateCalls int
	var resetCalls int
	var stopCalls int

	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{Servers: map[string]config.ServerConfig{}}, nil
	}
	deps.mergeFallbackForCWD = func(cfg *config.Config, cwd string) error {
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
	deps.validateConfig = func(*config.Config) error {
		validateCalls++
		return nil
	}
	deps.poolReset = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	deps.keepaliveStop = func(_ *Keepalive) {
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
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-a", &activeCWD, &cfgHash, &cfg, pool, ka, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a) error = %v", err)
	}
	if activeCWD != "/tmp/project-a" {
		t.Fatalf("active cwd = %q, want %q", activeCWD, "/tmp/project-a")
	}
	if _, ok := cfg.Servers["/tmp/project-a"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want project-a fallback entry", cfg.Servers)
	}

	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-a", &activeCWD, &cfgHash, &cfg, pool, ka, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a repeat) error = %v", err)
	}
	if loadCalls != 1 || mergeCalls != 1 || validateCalls != 1 {
		t.Fatalf("reload hooks called load=%d merge=%d validate=%d, want 1/1/1 after same-cwd repeat", loadCalls, mergeCalls, validateCalls)
	}
	if resetCalls != 1 || stopCalls != 1 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 1/1 after same-cwd repeat", resetCalls, stopCalls)
	}

	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-b", &activeCWD, &cfgHash, &cfg, pool, ka, deps); err != nil {
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
	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		return nil, errors.New("boom")
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error {
		t.Fatal("mergeFallbackForCWD should not be called when load fails")
		return nil
	}
	deps.validateConfig = func(*config.Config) error {
		t.Fatal("validateConfig should not be called when load fails")
		return nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}
	activeCWD := ""
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project", &activeCWD, &cfgHash, &cfg, nil, nil, deps); err == nil {
		t.Fatal("syncRuntimeConfigForRequest() error = nil, want non-nil")
	}
}

func TestSyncRuntimeConfigForRequestSkipsResetWhenConfigFingerprintUnchanged(t *testing.T) {
	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"github": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }

	var resetCalls int
	var stopCalls int
	deps.poolReset = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	deps.keepaliveStop = func(_ *Keepalive) {
		stopCalls++
	}

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
		},
	}
	initialCfg := cfg
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}

	activeCWD := ""
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-a", &activeCWD, &cfgHash, &cfg, nil, nil, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-a) error = %v", err)
	}
	if cfg != initialCfg {
		t.Fatal("cfg pointer changed after unchanged-fingerprint sync")
	}
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-b", &activeCWD, &cfgHash, &cfg, nil, nil, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-b) error = %v", err)
	}
	if cfg != initialCfg {
		t.Fatal("cfg pointer changed after second unchanged-fingerprint sync")
	}

	if resetCalls != 0 || stopCalls != 0 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 0/0 for same effective config", resetCalls, stopCalls)
	}
	if activeCWD != "/tmp/project-b" {
		t.Fatalf("active cwd = %q, want %q", activeCWD, "/tmp/project-b")
	}
}

func TestSyncRuntimeConfigForRequestRearmsDaemonIdleTimerAfterReset(t *testing.T) {
	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"github": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }

	var stopCalls int
	deps.keepaliveStop = func(ka *Keepalive) {
		stopCalls++
		if ka != nil {
			ka.Stop()
		}
	}
	deps.poolReset = func(*mcppool.Pool, *config.Config) {}

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"old": {Command: "old-cmd"},
		},
	}
	cfgHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}

	ka := NewKeepalive(nil)
	defer ka.Stop()
	ka.TouchDaemon()

	activeCWD := ""
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project", &activeCWD, &cfgHash, &cfg, nil, ka, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequestWithDeps() error = %v", err)
	}
	if stopCalls != 1 {
		t.Fatalf("keepaliveStop calls = %d, want 1", stopCalls)
	}

	ka.mu.Lock()
	_, hasDaemonTimer := ka.timers[daemonIdleSentinel]
	ka.mu.Unlock()
	if !hasDaemonTimer {
		t.Fatal("daemon idle timer missing after keepalive reset")
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

func TestRuntimeRequestHandlerPingTouchesDaemonKeepalive(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	handler := newRuntimeRequestHandlerWithDeps(
		&config.Config{Servers: map[string]config.ServerConfig{}},
		nil,
		ka,
		runtimeDefaultDeps(),
	)

	resp := handler.handle(context.Background(), &ipc.Request{Type: "ping"})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	ka.mu.Lock()
	_, hasDaemonTimer := ka.timers[daemonIdleSentinel]
	ka.mu.Unlock()
	if !hasDaemonTimer {
		t.Fatal("daemon idle timer missing after ping request")
	}
}

func TestRuntimeRequestHandlerAllowsConcurrentSameCWDRequests(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	var inFlight int32
	var maxInFlight int32

	deps := runtimeDefaultDeps()
	deps.poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
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
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, &mcppool.Pool{}, ka, deps)
	handler.activeCWD = "/tmp/project"

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

func TestRuntimeRequestHandlerInstallsEphemeralServerFromRequest(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	const source = "/tmp/ephemeral.json"

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != source {
			t.Fatalf("poolListTools server = %q, want %q", server, source)
		}
		return []mcppool.ToolInfo{{Name: "ping", Description: "Ping"}}, nil
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project",
		Ephemeral: &ipc.EphemeralServer{
			Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
		},
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}

	if _, ok := handler.cfg.Servers[source]; !ok {
		t.Fatalf("cfg.Servers missing ephemeral source %q", source)
	}
	origin, ok := handler.cfg.ServerOrigins[source]
	if !ok {
		t.Fatalf("cfg.ServerOrigins missing source %q", source)
	}
	if origin.Kind != config.ServerOriginKindRuntimeEphemeral {
		t.Fatalf("origin kind = %q, want %q", origin.Kind, config.ServerOriginKindRuntimeEphemeral)
	}
}

func TestRuntimeRequestHandlerEphemeralInstallKeepsPersistentConfigHash(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	initialHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(initial) error = %v", err)
	}

	ka := NewKeepalive(nil)
	defer ka.Stop()

	const source = "/tmp/ephemeral.json"

	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != source {
			t.Fatalf("poolListTools server = %q, want %q", server, source)
		}
		return []mcppool.ToolInfo{{Name: "ping"}}, nil
	}
	deps.loadConfig = func() (*config.Config, error) {
		return &config.Config{Servers: map[string]config.ServerConfig{}}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project-a"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project-a",
		Ephemeral: &ipc.EphemeralServer{
			Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
		},
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if handler.cfgHash != initialHash {
		t.Fatalf("cfgHash = %q, want %q after ephemeral install", handler.cfgHash, initialHash)
	}
	expectedHash, err := configFingerprint(cfg)
	if err != nil {
		t.Fatalf("configFingerprint(expected) error = %v", err)
	}
	if handler.cfgHash != expectedHash {
		t.Fatalf("cfgHash = %q, want %q", handler.cfgHash, expectedHash)
	}

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project-b",
	})
	if resp == nil {
		t.Fatal("handler returned nil response on cwd change")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code on cwd change = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 after cwd change when persistent config is unchanged", resetCalls)
	}
	if _, ok := handler.cfg.Servers[source]; !ok {
		t.Fatalf("cfg.Servers missing source %q after cwd change", source)
	}
	origin, ok := handler.cfg.ServerOrigins[source]
	if !ok {
		t.Fatalf("cfg.ServerOrigins missing source %q after cwd change", source)
	}
	if origin.Kind != config.ServerOriginKindRuntimeEphemeral {
		t.Fatalf("origin kind = %q, want %q after cwd change", origin.Kind, config.ServerOriginKindRuntimeEphemeral)
	}

	resp = handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project-b",
	})
	if resp == nil {
		t.Fatal("handler returned nil response on list_tools after cwd change")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code on list_tools after cwd change = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 after same-cwd list_tools", resetCalls)
	}
}

func TestRuntimeRequestHandlerBoundsRuntimeEphemeralServers(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{{Name: "ping"}}, nil
	}
	var closed []string
	deps.poolClose = func(_ *mcppool.Pool, server string) {
		closed = append(closed, server)
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"

	total := runtimeEphemeralMaxServer + 8
	for i := 0; i < total; i++ {
		source := fmt.Sprintf("/tmp/ephemeral-%03d.json", i)
		resp := handler.handle(context.Background(), &ipc.Request{
			Type:   "list_tools",
			Server: source,
			CWD:    "/tmp/project",
			Ephemeral: &ipc.EphemeralServer{
				Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
			},
		})
		if resp == nil {
			t.Fatal("handler returned nil response")
		}
		if resp.ExitCode != ipc.ExitOK {
			t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
		}
	}

	if got := len(handler.ephemeralServers); got != runtimeEphemeralMaxServer {
		t.Fatalf("ephemeralServers size = %d, want %d", got, runtimeEphemeralMaxServer)
	}
	if got := len(handler.ephemeralServerOrder); got != runtimeEphemeralMaxServer {
		t.Fatalf("ephemeralServerOrder size = %d, want %d", got, runtimeEphemeralMaxServer)
	}

	oldest := "/tmp/ephemeral-000.json"
	if _, ok := handler.ephemeralServers[oldest]; ok {
		t.Fatalf("ephemeralServers still contains evicted %q", oldest)
	}
	if _, ok := handler.cfg.Servers[oldest]; ok {
		t.Fatalf("cfg.Servers still contains evicted %q", oldest)
	}
	if _, ok := handler.cfg.ServerOrigins[oldest]; ok {
		t.Fatalf("cfg.ServerOrigins still contains evicted %q", oldest)
	}

	newest := fmt.Sprintf("/tmp/ephemeral-%03d.json", total-1)
	if _, ok := handler.ephemeralServers[newest]; !ok {
		t.Fatalf("ephemeralServers missing newest %q", newest)
	}
	if _, ok := handler.cfg.Servers[newest]; !ok {
		t.Fatalf("cfg.Servers missing newest %q", newest)
	}
	if got := handler.ephemeralServerOrder[len(handler.ephemeralServerOrder)-1]; got != newest {
		t.Fatalf("last ephemeralServerOrder entry = %q, want %q", got, newest)
	}
	wantCloseCalls := total - runtimeEphemeralMaxServer
	if got := len(closed); got != wantCloseCalls {
		t.Fatalf("poolClose calls = %d, want %d", got, wantCloseCalls)
	}
	for i := 0; i < wantCloseCalls; i++ {
		want := fmt.Sprintf("/tmp/ephemeral-%03d.json", i)
		if got := closed[i]; got != want {
			t.Fatalf("poolClose[%d] = %q, want %q", i, got, want)
		}
	}
}

func TestRememberRuntimeEphemeralServerDoesNotClosePersistentNameCollision(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"shared": {Command: "echo", Args: []string{"configured"}},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"shared": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
		},
	}

	servers := map[string]config.ServerConfig{
		"shared": {Command: "echo", Args: []string{"runtime-ephemeral"}},
	}
	order := []string{"shared"}
	for i := 0; i < runtimeEphemeralMaxServer-1; i++ {
		name := fmt.Sprintf("ephemeral-%03d", i)
		servers[name] = config.ServerConfig{Command: "echo", Args: []string{"ok"}}
		order = append(order, name)
	}

	var closed []string
	deps := runtimeDefaultDeps()
	deps.poolClose = func(_ *mcppool.Pool, server string) {
		closed = append(closed, server)
	}

	changed := rememberRuntimeEphemeralServer(
		cfg,
		nil,
		deps,
		&servers,
		&order,
		"new-ephemeral",
		config.ServerConfig{Command: "echo", Args: []string{"ok"}},
	)
	if !changed {
		t.Fatal("rememberRuntimeEphemeralServer() changed = false, want true")
	}
	if _, ok := cfg.Servers["shared"]; !ok {
		t.Fatal("cfg.Servers missing persistent collision entry")
	}
	if got := config.NormalizeServerOrigin(cfg.ServerOrigins["shared"]).Kind; got != config.ServerOriginKindMCPXConfig {
		t.Fatalf("cfg.ServerOrigins[shared] kind = %q, want %q", got, config.ServerOriginKindMCPXConfig)
	}
	if _, ok := servers["shared"]; ok {
		t.Fatal("runtime-ephemeral overlay still contains stale collision entry")
	}
	if len(closed) != 0 {
		t.Fatalf("poolClose called for persistent collision entry: %#v", closed)
	}
}
