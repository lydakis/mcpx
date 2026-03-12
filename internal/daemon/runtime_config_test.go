package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/paths"
	"github.com/mark3labs/mcp-go/mcp"
)

func fallbackSourceError(path string) error {
	return &config.FallbackSourceError{
		Path: path,
		Err:  fmt.Errorf("parsing fallback config"),
	}
}

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

func TestSyncRuntimeConfigForRequestSkipsResetAndResyncsPoolConfigWhenConfigFingerprintUnchanged(t *testing.T) {
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
	var setConfigCalls int
	deps.poolReset = func(_ *mcppool.Pool, _ *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(_ *mcppool.Pool, _ *config.Config) {
		setConfigCalls++
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
	firstReloadCfg := cfg
	if firstReloadCfg == initialCfg {
		t.Fatal("cfg pointer unchanged after unchanged-fingerprint sync")
	}
	if err := syncRuntimeConfigForRequestWithDeps("/tmp/project-b", &activeCWD, &cfgHash, &cfg, nil, nil, deps); err != nil {
		t.Fatalf("syncRuntimeConfigForRequest(project-b) error = %v", err)
	}
	secondReloadCfg := cfg
	if secondReloadCfg == firstReloadCfg {
		t.Fatal("cfg pointer unchanged after second unchanged-fingerprint sync")
	}

	if resetCalls != 0 || stopCalls != 0 {
		t.Fatalf("lifecycle hooks called reset=%d stop=%d, want 0/0 for same effective config", resetCalls, stopCalls)
	}
	if setConfigCalls != 2 {
		t.Fatalf("poolSetConfig calls = %d, want 2 for unchanged-fingerprint cwd syncs", setConfigCalls)
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
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: "stable"}
	}
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
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "stable"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

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

func TestRuntimeRequestHandlerSkipsConfigFilePollingBeforeNextDeadline(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {Command: "echo"}}}

	now := time.Unix(1_700_000_000, 0)
	var stampCalls int

	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		stampCalls++
		return runtimeConfigStamp{Digest: "stable"}
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"
	handler.nextConfigPollAt = now.Add(time.Minute)

	for i := 0; i < 3; i++ {
		resp := handler.handle(context.Background(), &ipc.Request{
			Type: "list_servers",
			CWD:  "/tmp/project",
		})
		if resp == nil {
			t.Fatal("handler returned nil response")
		}
		if resp.ExitCode != ipc.ExitOK {
			t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
		}
	}

	if stampCalls != 4 {
		t.Fatalf("currentRuntimeConfigStamp calls = %d, want 4 (1 setup + 3 fast-path checks)", stampCalls)
	}
}

func TestRuntimeRequestHandlerReloadsBeforeNextDeadlineWhenStampChanges(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {Command: "echo"}}}

	now := time.Unix(1_700_000_000, 0)
	stamps := []runtimeConfigStamp{
		{Digest: "initial"},
		{Digest: "updated"},
		{Digest: "updated"},
	}
	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		if stampCalls >= len(stamps) {
			t.Fatalf("unexpected currentRuntimeConfigStamp call %d", stampCalls+1)
		}
		stamp := stamps[stampCalls]
		stampCalls++
		return stamp
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"checkdin": {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp
	handler.nextConfigPollAt = now.Add(time.Minute)

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after pre-deadline stamp change", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after pre-deadline stamp change", resetCalls)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "updated" {
		t.Fatalf("runtimeConfigStamp = %#v, want updated digest after pre-deadline reload", got)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded checkdin entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerKeepsServingLastGoodConfigAfterSameCWDReloadError(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
		},
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		if loadCalls == 1 {
			return nil, fmt.Errorf("invalid config: parsing config")
		}
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"checkdin": {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after failed same-cwd reload", loadCalls)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 when same-cwd reload fails", resetCalls)
	}
	if _, ok := handler.cfg.Servers["github"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want last good github entry to remain", handler.cfg.Servers)
	}
	if got := handler.nextConfigPollAt; !got.Equal(now.Add(runtimeConfigPollInterval)) {
		t.Fatalf("nextConfigPollAt = %v, want %v after failed same-cwd reload", got, now.Add(runtimeConfigPollInterval))
	}

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response before retry deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code before retry deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 before retry deadline", loadCalls)
	}

	now = now.Add(runtimeConfigPollInterval)

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response after retry deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code after retry deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after retry deadline", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after successful retry", resetCalls)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want retried checkdin entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerBacksOffFromEndOfSlowSameCWDReloadError(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
		},
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		if loadCalls == 1 {
			now = now.Add(2 * runtimeConfigPollInterval)
			return nil, fmt.Errorf("invalid config: parsing config")
		}
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"checkdin": {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after failed same-cwd reload", loadCalls)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 when same-cwd reload fails", resetCalls)
	}
	wantPollAt := now.Add(runtimeConfigPollInterval)
	if got := handler.nextConfigPollAt; !got.Equal(wantPollAt) {
		t.Fatalf("nextConfigPollAt = %v, want %v after slow same-cwd reload failure", got, wantPollAt)
	}

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response before backoff deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code before backoff deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 before backoff deadline", loadCalls)
	}

	now = wantPollAt

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response after backoff deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code after backoff deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after backoff deadline", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after successful retry", resetCalls)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want retried checkdin entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerKeepsServingLastGoodConfigAfterSameCWDFallbackReloadError(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
		},
	}

	var loadCalls int
	var mergeCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{Servers: map[string]config.ServerConfig{}}, nil
	}
	deps.mergeFallbackForCWD = func(cfg *config.Config, _ string) error {
		mergeCalls++
		if mergeCalls == 1 {
			return fallbackSourceError("/tmp/project/.mcp.json")
		}
		cfg.Servers["checkdin"] = config.ServerConfig{Command: "echo"}
		return nil
	}
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after failed same-cwd fallback reload", loadCalls)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 when same-cwd fallback reload fails", resetCalls)
	}
	if _, ok := handler.cfg.Servers["github"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want last good github entry to remain", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect partial fallback reload to replace live config", handler.cfg.Servers)
	}
	if got := handler.nextConfigPollAt; !got.Equal(now.Add(runtimeConfigPollInterval)) {
		t.Fatalf("nextConfigPollAt = %v, want %v after failed same-cwd fallback reload", got, now.Add(runtimeConfigPollInterval))
	}

	now = now.Add(runtimeConfigPollInterval)
	stampDigest = "retried"

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response after retry deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code after retry deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after retry deadline", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after successful fallback retry", resetCalls)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want retried checkdin entry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["github"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale github entry after successful fallback retry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerAppliesPrimaryConfigChangesDespiteSameCWDFallbackReloadError(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
			"old":    {Command: "old"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
			"old":    config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
		},
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"new": {Command: "new"},
			},
			ServerOrigins: map[string]config.ServerOrigin{
				"new": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error {
		return fallbackSourceError("/tmp/project/.mcp.json")
	}
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after config reload", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after primary config change", resetCalls)
	}
	if _, ok := handler.cfg.Servers["new"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded primary server", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["github"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want preserved fallback server", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["old"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale primary server", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerRetryPreservesFallbackFromCommittedConfig(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"live":        {Command: "echo"},
			"old-primary": {Command: "old"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"live":        config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
			"old-primary": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	stamps := []runtimeConfigStamp{
		{Digest: "initial"},
		{Digest: "updated"},
		{Digest: "changed"},
		{Digest: "changed"},
		{Digest: "changed"},
	}
	var stampCalls int
	var loadCalls int
	var mergeCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		if stampCalls >= len(stamps) {
			t.Fatalf("unexpected currentRuntimeConfigStamp call %d", stampCalls+1)
		}
		stamp := stamps[stampCalls]
		stampCalls++
		return stamp
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		switch loadCalls {
		case 1:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"primary-a": {Command: "echo"},
				},
				ServerOrigins: map[string]config.ServerOrigin{
					"primary-a": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
				},
			}, nil
		case 2:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"primary-b": {Command: "echo"},
				},
				ServerOrigins: map[string]config.ServerOrigin{
					"primary-b": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
				},
			}, nil
		default:
			t.Fatalf("unexpected loadConfig call %d", loadCalls)
			return nil, nil
		}
	}
	deps.mergeFallbackForCWD = func(cfg *config.Config, _ string) error {
		mergeCalls++
		switch mergeCalls {
		case 1:
			cfg.Servers["transient"] = config.ServerConfig{Command: "echo"}
			cfg.ServerOrigins["transient"] = config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json")
			return nil
		case 2:
			return fallbackSourceError("/tmp/project/.mcp.json")
		default:
			t.Fatalf("unexpected mergeFallbackForCWD call %d", mergeCalls)
			return nil
		}
	}
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after retrying unstable reload", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after stable retry commit", resetCalls)
	}
	if _, ok := handler.cfg.Servers["live"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want committed live fallback entry to remain", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["transient"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect transient fallback entry to leak through retry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["primary-b"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want latest primary config after retry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["old-primary"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale primary entry after retry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerPreservesOnlyFailedFallbackSources(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"from-bad":  {Command: "echo"},
			"from-good": {Command: "echo"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"from-bad":  config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/bad.json"),
			"from-good": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/good.json"),
		},
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"primary": {Command: "new"},
			},
			ServerOrigins: map[string]config.ServerOrigin{
				"primary": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error {
		return fallbackSourceError("/tmp/project/bad.json")
	}
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after fallback warning reload", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after fallback warning reload", resetCalls)
	}
	if _, ok := handler.cfg.Servers["from-bad"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want failed-source fallback entry to remain", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["from-good"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect healthy-source fallback entry to be resurrected", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["primary"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want primary config entry after reload", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerAdvancesStampAfterStableFallbackWarning(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stampDigest := "initial"

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
			"old":    {Command: "old"},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github": config.NewServerOrigin(config.ServerOriginKindClaude, "/tmp/project/.mcp.json"),
			"old":    config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
		},
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		return runtimeConfigStamp{Digest: stampDigest}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"new": {Command: "new"},
			},
			ServerOrigins: map[string]config.ServerOrigin{
				"new": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, paths.ConfigFile()),
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error {
		return fallbackSourceError("/tmp/project/.mcp.json")
	}
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)
	handler.activeCWD = "/tmp/project"

	stampDigest = "updated"

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after warned reload", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after warned reload", resetCalls)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "updated" {
		t.Fatalf("runtimeConfigStamp = %#v, want updated digest after warned reload", got)
	}

	now = now.Add(runtimeConfigPollInterval)

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response after backoff deadline")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code after backoff deadline = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after stable warned follow-up", loadCalls)
	}
}

func TestRuntimeRequestHandlerReloadsSameCWDWhenConfigFileChanges(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("HOME", tmp)

	configDir := filepath.Dir(paths.ConfigFile())
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte("[servers.github]\ncommand = \"echo\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(initial config): %v", err)
	}

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"checkdin": {URL: "https://stunning-art-production-340c.up.railway.app/mcp"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"

	updated := []byte("[servers.checkdin]\nurl = \"https://stunning-art-production-340c.up.railway.app/mcp\"\n")
	if err := os.WriteFile(paths.ConfigFile(), updated, 0o600); err != nil {
		t.Fatalf("WriteFile(updated config): %v", err)
	}
	nextStamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(paths.ConfigFile(), nextStamp, nextStamp); err != nil {
		t.Fatalf("Chtimes(updated config): %v", err)
	}

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after config file change", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after config file change", resetCalls)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded checkdin entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerReloadsSameCWDWhenFallbackSourceChanges(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("HOME", tmp)

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDir): %v", err)
	}
	configDir := filepath.Dir(paths.ConfigFile())
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	fallbackPath := filepath.Join(projectDir, ".mcp.json")
	writeFallback := func(serverName string) {
		t.Helper()
		content := fmt.Sprintf(`{"mcpServers":{"%s":{"command":"echo"}}}`, serverName)
		if err := os.WriteFile(fallbackPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(fallback): %v", err)
		}
	}
	writeFallback("github")

	deps := runtimeDefaultDeps()
	var resetCalls int
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	cfg, _, err := loadValidatedConfigForCWDWithDeps(projectDir, deps, nil)
	if err != nil {
		t.Fatalf("loadValidatedConfigForCWDWithDeps(initial): %v", err)
	}
	if _, ok := cfg.Servers["github"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want initial github entry", cfg.Servers)
	}

	ka := NewKeepalive(nil)
	defer ka.Stop()
	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = projectDir
	handler.runtimeConfigStamp = deps.currentRuntimeConfigStamp(cfg, projectDir)
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	writeFallback("checkdin")

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  projectDir,
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after fallback change", resetCalls)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded checkdin entry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["github"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale github entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerReloadsSameCWDWhenConfigContentChangesWithoutStampChange(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("HOME", tmp)

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDir): %v", err)
	}
	configDir := filepath.Dir(paths.ConfigFile())
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}

	writeConfig := func(contents string, stamp time.Time) {
		t.Helper()
		if err := os.WriteFile(paths.ConfigFile(), []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(config): %v", err)
		}
		if err := os.Chtimes(paths.ConfigFile(), stamp, stamp); err != nil {
			t.Fatalf("Chtimes(config): %v", err)
		}
	}

	initialConfig := "[servers.aaaaaaa]\ncommand = \"echo\"\n"
	updatedConfig := "[servers.bbbbbbb]\ncommand = \"echo\"\n"
	if len(initialConfig) != len(updatedConfig) {
		t.Fatalf("config lengths differ: %d vs %d", len(initialConfig), len(updatedConfig))
	}
	stamp := time.Unix(1_700_000_000, 0)
	writeConfig(initialConfig, stamp)

	deps := runtimeDefaultDeps()
	var resetCalls int
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	cfg, _, err := loadValidatedConfigForCWDWithDeps(projectDir, deps, nil)
	if err != nil {
		t.Fatalf("loadValidatedConfigForCWDWithDeps(initial): %v", err)
	}
	if _, ok := cfg.Servers["aaaaaaa"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want initial aaaaaaa entry", cfg.Servers)
	}

	ka := NewKeepalive(nil)
	defer ka.Stop()
	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = projectDir
	handler.runtimeConfigStamp = deps.currentRuntimeConfigStamp(cfg, projectDir)
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	writeConfig(updatedConfig, stamp)

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  projectDir,
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after same-size/same-stamp config rewrite", resetCalls)
	}
	if _, ok := handler.cfg.Servers["bbbbbbb"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded bbbbbbb entry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["aaaaaaa"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale aaaaaaa entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerRetriesSameCWDReloadWhenConfigChangesDuringLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("HOME", tmp)

	configDir := filepath.Dir(paths.ConfigFile())
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte("[servers.github]\ncommand = \"echo\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(initial config): %v", err)
	}

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	writeStampedConfig := func(contents string, stamp time.Time) {
		t.Helper()
		if err := os.WriteFile(paths.ConfigFile(), []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(config): %v", err)
		}
		if err := os.Chtimes(paths.ConfigFile(), stamp, stamp); err != nil {
			t.Fatalf("Chtimes(config): %v", err)
		}
	}

	var loadCalls int
	var resetCalls int
	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		switch loadCalls {
		case 1:
			writeStampedConfig("[servers.posthog]\nurl = \"https://example.com/posthog\"\n", time.Now().Add(3*time.Second))
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"checkdin": {URL: "https://example.com/checkdin"},
				},
			}, nil
		case 2:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"posthog": {URL: "https://example.com/posthog"},
				},
			}, nil
		default:
			t.Fatalf("unexpected loadConfig call %d", loadCalls)
			return nil, nil
		}
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"

	writeStampedConfig("[servers.checkdin]\nurl = \"https://example.com/checkdin\"\n", time.Now().Add(2*time.Second))

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after mid-reload config change", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after stable mid-reload commit", resetCalls)
	}
	if _, ok := handler.cfg.Servers["posthog"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want reloaded posthog entry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["checkdin"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale checkdin entry", handler.cfg.Servers)
	}

	resp = handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response on second request")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code on second request = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after stable same-cwd follow-up request", loadCalls)
	}
}

func TestRuntimeRequestHandlerReloadsWhenStampChangesDuringNoOpPoll(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"initial": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	stamps := []runtimeConfigStamp{
		{Digest: "initial"}, // handler init
		{Digest: "initial"}, // preflight stamp, looks unchanged
		{Digest: "updated"}, // confirm stamp, changed mid-poll
		{Digest: "updated"}, // retry preflight
		{Digest: "updated"}, // retry confirm, stable
	}
	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		if stampCalls >= len(stamps) {
			t.Fatalf("unexpected currentRuntimeConfigStamp call %d", stampCalls+1)
		}
		stamp := stamps[stampCalls]
		stampCalls++
		return stamp
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"updated": {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 1 {
		t.Fatalf("loadConfig calls = %d, want 1 after no-op poll stamp change", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after no-op poll stamp change", resetCalls)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "updated" {
		t.Fatalf("runtimeConfigStamp = %#v, want updated digest after reload", got)
	}
	if _, ok := handler.cfg.Servers["updated"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want updated entry after reload", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["initial"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect stale initial entry", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerForcesReloadAfterUnstableStampRetry(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"initial": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	stamps := []runtimeConfigStamp{
		{Digest: "initial"}, // handler init
		{Digest: "updated"}, // before first reload
		{Digest: "initial"}, // after first reload, source flipped back
		{Digest: "initial"}, // before second forced reload
		{Digest: "initial"}, // after second reload, stable
	}
	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		if stampCalls >= len(stamps) {
			t.Fatalf("unexpected currentRuntimeConfigStamp call %d", stampCalls+1)
		}
		stamp := stamps[stampCalls]
		stampCalls++
		return stamp
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		switch loadCalls {
		case 1:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"transient": {Command: "echo"},
				},
			}, nil
		case 2:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"stable": {Command: "echo"},
				},
			}, nil
		default:
			t.Fatalf("unexpected loadConfig call %d", loadCalls)
			return nil, nil
		}
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after unstable stamp retry", loadCalls)
	}
	if resetCalls != 1 {
		t.Fatalf("poolReset calls = %d, want 1 after stable retry commit", resetCalls)
	}
	if _, ok := handler.cfg.Servers["stable"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want stable reloaded entry", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["transient"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect transient entry to remain", handler.cfg.Servers)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "initial" {
		t.Fatalf("runtimeConfigStamp = %#v, want initial digest after stable retry", got)
	}
}

func TestRuntimeRequestHandlerStopsRetryingAfterRepeatedUnstableStamps(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"initial": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	now := time.Unix(1_700_000_000, 0)
	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.now = func() time.Time { return now }
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		stampCalls++
		if stampCalls == 1 {
			return runtimeConfigStamp{Digest: "initial"}
		}
		if stampCalls%2 == 0 {
			return runtimeConfigStamp{Digest: fmt.Sprintf("load-%02d", stampCalls)}
		}
		return runtimeConfigStamp{Digest: fmt.Sprintf("confirm-%02d", stampCalls)}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				fmt.Sprintf("server-%d", loadCalls): {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if loadCalls != runtimeConfigPollMaxRetry {
		t.Fatalf("loadConfig calls = %d, want %d after capped unstable retries", loadCalls, runtimeConfigPollMaxRetry)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 while unstable reload stays uncommitted", resetCalls)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "initial" {
		t.Fatalf("runtimeConfigStamp = %#v, want unchanged initial digest after capped retries", got)
	}
	if !handler.nextConfigPollAt.Equal(now) {
		t.Fatalf("nextConfigPollAt = %v, want immediate retry at %v", handler.nextConfigPollAt, now)
	}
	if _, ok := handler.cfg.Servers["initial"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want last confirmed config after capped retries", handler.cfg.Servers)
	}
	if len(handler.cfg.Servers) != 1 {
		t.Fatalf("cfg.Servers = %#v, want only the last confirmed initial config", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerReturnsCWDSwitchErrorAfterCappedUnstableRetry(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"initial": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		stampCalls++
		if stampCalls == 1 {
			return runtimeConfigStamp{Digest: "initial"}
		}
		if stampCalls%2 == 0 {
			return runtimeConfigStamp{Digest: fmt.Sprintf("load-%02d", stampCalls)}
		}
		return runtimeConfigStamp{Digest: fmt.Sprintf("confirm-%02d", stampCalls)}
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				fmt.Sprintf("server-%d", loadCalls): {Command: "echo"},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/original"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitInternal {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitInternal, resp.Stderr)
	}
	if !strings.Contains(resp.Stderr, "runtime config did not stabilize") {
		t.Fatalf("stderr = %q, want unstable reload error", resp.Stderr)
	}
	if loadCalls != runtimeConfigPollMaxRetry {
		t.Fatalf("loadConfig calls = %d, want %d after capped unstable cwd switch retries", loadCalls, runtimeConfigPollMaxRetry)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 while unstable cwd switch stays uncommitted", resetCalls)
	}
	if handler.activeCWD != "/tmp/original" {
		t.Fatalf("activeCWD = %q, want original cwd to remain live", handler.activeCWD)
	}
	if got := handler.runtimeConfigStamp; got.Digest != "initial" {
		t.Fatalf("runtimeConfigStamp = %#v, want unchanged initial digest after capped cwd switch retries", got)
	}
	if _, ok := handler.cfg.Servers["initial"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want initial live config after capped cwd switch retries", handler.cfg.Servers)
	}
	if len(handler.cfg.Servers) != 1 {
		t.Fatalf("cfg.Servers = %#v, want only the original live config after capped cwd switch retries", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerReturnsCWDSwitchReloadErrorAfterUnstableRetry(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"initial": {Command: "echo"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	stamps := []runtimeConfigStamp{
		{Digest: "initial"}, // handler init
		{Digest: "updated"}, // before first reload
		{Digest: "changed"}, // after first reload, source moved again
		{Digest: "changed"}, // before second reload attempt
	}
	var stampCalls int
	var loadCalls int
	var resetCalls int

	deps := runtimeDefaultDeps()
	deps.currentRuntimeConfigStamp = func(*config.Config, string) runtimeConfigStamp {
		if stampCalls >= len(stamps) {
			t.Fatalf("unexpected currentRuntimeConfigStamp call %d", stampCalls+1)
		}
		stamp := stamps[stampCalls]
		stampCalls++
		return stamp
	}
	deps.loadConfig = func() (*config.Config, error) {
		loadCalls++
		switch loadCalls {
		case 1:
			return &config.Config{
				Servers: map[string]config.ServerConfig{
					"transient": {Command: "echo"},
				},
			}, nil
		case 2:
			return nil, fmt.Errorf("invalid config: parsing config")
		default:
			t.Fatalf("unexpected loadConfig call %d", loadCalls)
			return nil, nil
		}
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolReset = func(*mcppool.Pool, *config.Config) {
		resetCalls++
	}
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.keepaliveStop = func(*Keepalive) {}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/original"
	handler.runtimeConfigStamp = runtimeConfigStamp{Digest: "initial"}
	handler.lastPolledConfigStamp = handler.runtimeConfigStamp

	resp := handler.handle(context.Background(), &ipc.Request{
		Type: "list_servers",
		CWD:  "/tmp/project",
	})
	if resp == nil {
		t.Fatal("handler returned nil response")
	}
	if resp.ExitCode != ipc.ExitInternal {
		t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitInternal, resp.Stderr)
	}
	if !strings.Contains(resp.Stderr, "invalid config: parsing config") {
		t.Fatalf("stderr = %q, want propagated reload error", resp.Stderr)
	}
	if loadCalls != 2 {
		t.Fatalf("loadConfig calls = %d, want 2 after unstable cwd switch retry", loadCalls)
	}
	if resetCalls != 0 {
		t.Fatalf("poolReset calls = %d, want 0 before a stable cwd switch commit", resetCalls)
	}
	if handler.activeCWD != "/tmp/original" {
		t.Fatalf("activeCWD = %q, want original cwd to remain live", handler.activeCWD)
	}
	if _, ok := handler.cfg.Servers["initial"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want initial live config to remain", handler.cfg.Servers)
	}
	if _, ok := handler.cfg.Servers["transient"]; ok {
		t.Fatalf("cfg.Servers = %#v, did not expect transient cwd-switch config to leak live", handler.cfg.Servers)
	}
}

func TestRuntimeRequestHandlerAllowsConcurrentDispatchAfterCWDSync(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo", Args: []string{"old"}},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	var inFlight int32
	var maxInFlight int32
	dispatchStarted := make(chan struct{}, 2)
	releaseDispatch := make(chan struct{})

	deps := runtimeDefaultDeps()
	deps.loadConfig = func() (*config.Config, error) {
		return &config.Config{
			Servers: map[string]config.ServerConfig{
				"github": {Command: "echo", Args: []string{"old"}},
			},
		}, nil
	}
	deps.mergeFallbackForCWD = func(*config.Config, string) error { return nil }
	deps.validateConfig = func(*config.Config) error { return nil }
	deps.poolSetConfig = func(*mcppool.Pool, *config.Config) {}
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != "github" {
			t.Fatalf("poolListTools server = %q, want github", server)
		}
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
		dispatchStarted <- struct{}{}
		<-releaseDispatch
		atomic.AddInt32(&inFlight, -1)
		return []mcppool.ToolInfo{{Name: "ping", Description: "Ping"}}, nil
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/old"

	results := make(chan *ipc.Response, 2)
	go func() {
		results <- handler.handle(context.Background(), &ipc.Request{
			Type:   "list_tools",
			Server: "github",
			CWD:    "/tmp/new",
		})
	}()

	select {
	case <-dispatchStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first request did not reach dispatch")
	}

	go func() {
		results <- handler.handle(context.Background(), &ipc.Request{
			Type:   "list_tools",
			Server: "github",
			CWD:    "/tmp/new",
		})
	}()

	select {
	case <-dispatchStarted:
		// second request reached dispatch while first request was still in-flight
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second request did not dispatch concurrently after CWD sync")
	}

	close(releaseDispatch)

	for i := 0; i < 2; i++ {
		resp := <-results
		if resp == nil {
			t.Fatal("handler returned nil response")
		}
		if resp.ExitCode != ipc.ExitOK {
			t.Fatalf("handler exit code = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
		}
	}

	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
		t.Fatalf("max concurrent dispatch after CWD sync = %d, want >= 2", got)
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

func TestRuntimeRequestHandlerDoesNotRememberEphemeralServerOnFailedFirstUse(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	const source = "/tmp/ephemeral.json"

	var poolClosed []string
	var listCalls int
	var handler *runtimeRequestHandler

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != source {
			t.Fatalf("poolListTools server = %q, want %q", server, source)
		}
		listCalls++
		if _, ok := handler.cfg.Servers[source]; !ok {
			return nil, fmt.Errorf("unknown server: %s", source)
		}
		if listCalls == 1 {
			return nil, errors.New("bootstrap failed")
		}
		return []mcppool.ToolInfo{{Name: "ping", Description: "Ping"}}, nil
	}
	deps.poolClose = func(_ *mcppool.Pool, server string) {
		poolClosed = append(poolClosed, server)
	}

	handler = newRuntimeRequestHandlerWithDeps(cfg, nil, ka, deps)
	handler.activeCWD = "/tmp/project"

	first := handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project",
		Ephemeral: &ipc.EphemeralServer{
			Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
		},
	})
	if first == nil {
		t.Fatal("first handler response is nil")
	}
	if first.ExitCode == ipc.ExitOK {
		t.Fatalf("first handler exit code = %d, want non-OK for failed bootstrap", first.ExitCode)
	}
	if _, ok := handler.cfg.Servers[source]; ok {
		t.Fatalf("cfg.Servers still contains failed first-use source %q", source)
	}
	if _, ok := handler.cfg.ServerOrigins[source]; ok {
		t.Fatalf("cfg.ServerOrigins still contains failed first-use source %q", source)
	}
	if _, ok := handler.ephemeralServers[source]; ok {
		t.Fatalf("ephemeralServers still contains failed first-use source %q", source)
	}
	for _, name := range handler.ephemeralServerOrder {
		if name == source {
			t.Fatalf("ephemeralServerOrder still contains failed first-use source %q", source)
		}
	}
	if len(poolClosed) != 1 || poolClosed[0] != source {
		t.Fatalf("poolClose calls = %#v, want one close for %q", poolClosed, source)
	}

	second := handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project",
	})
	if second == nil {
		t.Fatal("second handler response is nil")
	}
	if second.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("second handler exit code = %d, want %d for unknown server after rollback", second.ExitCode, ipc.ExitUsageErr)
	}

	third := handler.handle(context.Background(), &ipc.Request{
		Type:   "list_tools",
		Server: source,
		CWD:    "/tmp/project",
		Ephemeral: &ipc.EphemeralServer{
			Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
		},
	})
	if third == nil {
		t.Fatal("third handler response is nil")
	}
	if third.ExitCode != ipc.ExitOK {
		t.Fatalf("third handler exit code = %d, want %d (stderr=%q)", third.ExitCode, ipc.ExitOK, third.Stderr)
	}
	if _, ok := handler.ephemeralServers[source]; !ok {
		t.Fatalf("ephemeralServers missing source %q after successful retry", source)
	}
}

func TestFinalizeRequestEphemeralInstallRemembersOnStateVersionAdvance(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, runtimeDefaultDeps())

	const source = "/tmp/ephemeral-version-race.json"
	installed, resolvedName, resolvedServer, err := installRequestEphemeralServer(handler.cfg, source, &ipc.EphemeralServer{
		Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
	})
	if err != nil {
		t.Fatalf("installRequestEphemeralServer() error = %v", err)
	}
	if !installed {
		t.Fatal("installRequestEphemeralServer() installed = false, want true")
	}

	const dispatchVersion = uint64(10)
	handler.stateVersion = dispatchVersion + 1

	resp := &ipc.Response{ExitCode: ipc.ExitOK}
	finalResp, finalized := handler.finalizeRequestEphemeralInstall(dispatchVersion, resolvedName, resolvedServer, resp)
	if !finalized {
		t.Fatal("finalizeRequestEphemeralInstall() finalized = false, want true")
	}
	if finalResp != resp {
		t.Fatal("finalizeRequestEphemeralInstall() returned unexpected response pointer")
	}
	if _, ok := handler.ephemeralServers[source]; !ok {
		t.Fatalf("ephemeralServers missing source %q after finalize", source)
	}
	if _, ok := handler.cfg.Servers[source]; !ok {
		t.Fatalf("cfg.Servers missing source %q after finalize", source)
	}
	if got := handler.stateVersion; got != dispatchVersion+2 {
		t.Fatalf("stateVersion = %d, want %d", got, dispatchVersion+2)
	}
}

func TestFinalizeRequestEphemeralInstallRollsBackOnStateVersionAdvance(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}

	var poolClosed []string
	deps := runtimeDefaultDeps()
	deps.poolClose = func(_ *mcppool.Pool, server string) {
		poolClosed = append(poolClosed, server)
	}

	handler := newRuntimeRequestHandlerWithDeps(cfg, nil, nil, deps)

	const source = "/tmp/ephemeral-version-race-fail.json"
	installed, resolvedName, resolvedServer, err := installRequestEphemeralServer(handler.cfg, source, &ipc.EphemeralServer{
		Server: config.ServerConfig{Command: "echo", Args: []string{"ok"}},
	})
	if err != nil {
		t.Fatalf("installRequestEphemeralServer() error = %v", err)
	}
	if !installed {
		t.Fatal("installRequestEphemeralServer() installed = false, want true")
	}

	const dispatchVersion = uint64(20)
	handler.stateVersion = dispatchVersion + 1

	resp := &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: "bootstrap failed"}
	finalResp, finalized := handler.finalizeRequestEphemeralInstall(dispatchVersion, resolvedName, resolvedServer, resp)
	if !finalized {
		t.Fatal("finalizeRequestEphemeralInstall() finalized = false, want true")
	}
	if finalResp != resp {
		t.Fatal("finalizeRequestEphemeralInstall() returned unexpected response pointer")
	}
	if _, ok := handler.cfg.Servers[source]; ok {
		t.Fatalf("cfg.Servers still contains failed source %q", source)
	}
	if _, ok := handler.cfg.ServerOrigins[source]; ok {
		t.Fatalf("cfg.ServerOrigins still contains failed source %q", source)
	}
	if _, ok := handler.ephemeralServers[source]; ok {
		t.Fatalf("ephemeralServers still contains failed source %q", source)
	}
	if len(poolClosed) != 1 || poolClosed[0] != source {
		t.Fatalf("poolClose calls = %#v, want one close for %q", poolClosed, source)
	}
	if got := handler.stateVersion; got != dispatchVersion+2 {
		t.Fatalf("stateVersion = %d, want %d", got, dispatchVersion+2)
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
