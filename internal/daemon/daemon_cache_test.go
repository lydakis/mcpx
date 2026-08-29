package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/cache"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/paths"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResponseCacheGenerationRejectsWritesFromBeforeCredentialInvalidation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	deps := runtimeDefaultDeps()
	args := json.RawMessage(`{"query":"mcp"}`)
	generation := responseCacheGeneration()

	stored, err := writeResponseCacheIfCurrent(generation, func() error {
		return deps.cachePut("remote", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour)
	})
	if err != nil || !stored {
		t.Fatalf("initial cache write stored=%v err=%v, want stored", stored, err)
	}
	if err := clearResponseCache(); err != nil {
		t.Fatalf("clearResponseCache() error = %v", err)
	}

	stored, err = writeResponseCacheIfCurrent(generation, func() error {
		return deps.cachePut("remote", "search", args, []byte("stale-account-a"), ipc.ExitOK, time.Hour)
	})
	if err != nil {
		t.Fatalf("stale cache write error = %v", err)
	}
	if stored {
		t.Fatal("stale cache write stored = true, want false")
	}
	if _, _, ok := deps.cacheGet("remote", "search", args); ok {
		t.Fatal("cache hit after invalidation, want miss")
	}
}

func TestResponseCacheGuardStaysDisabledAfterFailedClear(t *testing.T) {
	guard := responseCacheGuard{enabled: true}
	generation := guard.snapshot()
	clearErr := errors.New("disk is read-only")
	if err := guard.clear(func() error { return clearErr }); !errors.Is(err, clearErr) {
		t.Fatalf("clear() error = %v, want %v", err, clearErr)
	}

	readCalled := false
	if _, _, ok := guard.readIfCurrent(generation, func() ([]byte, int, bool) {
		readCalled = true
		return []byte("stale"), ipc.ExitOK, true
	}); ok || readCalled {
		t.Fatalf("failed-clear read ok=%v called=%v, want disabled", ok, readCalled)
	}
	writeCalled := false
	stored, err := guard.writeIfCurrent(generation, func() error {
		writeCalled = true
		return nil
	})
	if err != nil || stored || writeCalled {
		t.Fatalf("failed-clear write stored=%v called=%v err=%v, want disabled", stored, writeCalled, err)
	}

	if err := guard.clear(func() error { return nil }); err != nil {
		t.Fatalf("successful clear error = %v", err)
	}
	if got := guard.snapshot(); got == generation {
		t.Fatalf("generation after successful retry = %d, want advancement from %d", got, generation)
	}
}

func TestDispatchRecoversOrphanedCredentialTransitionThroughDaemonGuards(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := clearResponseCache(); err != nil {
		t.Fatalf("clearResponseCache() error = %v", err)
	}

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := cache.Put("remote", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	generation := responseCacheGeneration()
	token := "transition-1073741824-orphan"
	transitionDir := filepath.Join(paths.CacheDir(), "response-credential-transitions")
	if err := paths.EnsureDir(transitionDir); err != nil {
		t.Fatalf("creating transition directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(transitionDir, token), nil, 0600); err != nil {
		t.Fatalf("creating orphan transition: %v", err)
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}
	pool := mcppool.New(cfg)
	if err := pool.BeginCredentialTransition([]string{"remote"}, token); err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	defer pool.EndCredentialTransition(token)
	deps := runtimeDefaultDeps()
	deps.poolListTools = func(context.Context, *mcppool.Pool, string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{{Name: "search"}}, nil
	}

	resp := dispatchWithDeps(context.Background(), cfg, pool, nil, &ipc.Request{
		Type:   "list_tools",
		Server: "remote",
	}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("list_tools exit = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if cache.CredentialTransitionPending() {
		t.Fatal("orphaned credential-transition marker remained after recovery")
	}
	if pool.CredentialTransitionPending("remote") {
		t.Fatal("orphaned credential-transition pool fence remained after recovery")
	}
	if _, _, ok := cache.Get("remote", "search", args); ok {
		t.Fatal("pre-transition response survived orphan recovery")
	}
	stored, err := writeResponseCacheIfCurrent(generation, func() error {
		return cache.Put("remote", "search", args, []byte("stale-account-a"), ipc.ExitOK, time.Hour)
	})
	if err != nil {
		t.Fatalf("stale cache write error = %v", err)
	}
	if stored {
		t.Fatal("pre-recovery request published into the new credential epoch")
	}
}

func TestCallToolUsesCachedResponseWhenPresent(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second
	poolCalls := 0
	cacheWrites := 0

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return nil, errors.New("pool should not be called on cache hit")
	}
	deps.cacheGet = func(_, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	deps.cachePut = func(_ string, _ string, _ json.RawMessage, _ []byte, _ int, _ time.Duration) error {
		cacheWrites++
		return nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("callTool() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	if string(resp.Content) != "cached\n" {
		t.Fatalf("callTool() content = %q, want %q", resp.Content, "cached\n")
	}
	if poolCalls != 0 {
		t.Fatalf("pool calls = %d, want 0", poolCalls)
	}
	if cacheWrites != 0 {
		t.Fatalf("cache writes = %d, want 0", cacheWrites)
	}
}

func TestCallToolCachesSuccessfulResponseWithDefaultTTL(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {DefaultCacheTTL: "45s"},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolCalls := 0
	cacheWrites := 0
	var wroteTTL time.Duration
	var wroteExit int
	var wroteContent string

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	}
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return nil, 0, false
	}
	deps.cachePut = func(_ string, _ string, _ json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		cacheWrites++
		wroteTTL = ttl
		wroteExit = exitCode
		wroteContent = string(content)
		return nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), nil, false, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("callTool() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	if poolCalls != 1 {
		t.Fatalf("pool calls = %d, want 1", poolCalls)
	}
	if cacheWrites != 1 {
		t.Fatalf("cache writes = %d, want 1", cacheWrites)
	}
	if wroteTTL != 45*time.Second {
		t.Fatalf("cache ttl = %s, want %s", wroteTTL, 45*time.Second)
	}
	if wroteExit != ipc.ExitOK {
		t.Fatalf("cached exit = %d, want %d", wroteExit, ipc.ExitOK)
	}
	if wroteContent != "{\"ok\":true}\n" {
		t.Fatalf("cached content = %q, want %q", wroteContent, "{\"ok\":true}\n")
	}
}

func TestCallToolReturnsMachineReadableInputRequired(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(context.Context, *mcppool.Pool, string, *mcppool.ToolInfo, json.RawMessage) (*mcp.CallToolResult, error) {
		var result mcp.CallToolResult
		if err := json.Unmarshal([]byte(`{"resultType":"input_required","inputRequests":{"confirm":{"method":"elicitation/create","params":{"message":"Deploy?"}}},"requestState":"opaque"}`), &result); err != nil {
			t.Fatalf("unmarshal input-required fixture: %v", err)
		}
		return &result, nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "deploy", json.RawMessage(`{}`), nil, false, deps)
	if resp.ExitCode != ipc.ExitToolErr || resp.ErrorCode != ipc.ErrorCodeInputRequired {
		t.Fatalf("response = %+v", resp)
	}
	if !strings.Contains(string(resp.Content), `"requestState":"opaque"`) || !strings.Contains(string(resp.Content), `"resultType":"input_required"`) {
		t.Fatalf("content = %s", resp.Content)
	}
}

func TestMultiRoundTripRetryBypassesResponseCache(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {DefaultCacheTTL: "1m"}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	cacheReads, cacheWrites, retryCalls := 0, 0, 0
	deps := runtimeDefaultDeps()
	deps.cacheGet = func(string, string, json.RawMessage) ([]byte, int, bool) {
		cacheReads++
		return []byte("stale\n"), ipc.ExitOK, true
	}
	deps.cachePut = func(string, string, json.RawMessage, []byte, int, time.Duration) error {
		cacheWrites++
		return nil
	}
	deps.poolCallToolWithInput = func(context.Context, *mcppool.Pool, string, *mcppool.ToolInfo, json.RawMessage, json.RawMessage, string) (*mcp.CallToolResult, error) {
		retryCalls++
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	}

	resp := callToolRoundTripWithInputModeWithDeps(
		context.Background(), cfg, nil, ka, "github", "deploy", json.RawMessage(`{}`), true,
		json.RawMessage(`{"confirm":{"action":"accept"}}`), "opaque", nil, true, deps,
	)
	if resp.ExitCode != ipc.ExitOK || retryCalls != 1 {
		t.Fatalf("response=%+v retryCalls=%d", resp, retryCalls)
	}
	if cacheReads != 0 || cacheWrites != 0 {
		t.Fatalf("cache reads=%d writes=%d, want 0", cacheReads, cacheWrites)
	}
	if !strings.Contains(resp.Stderr, "cache bypass") {
		t.Fatalf("stderr = %q, want bypass diagnostic", resp.Stderr)
	}
}

func TestEffectiveCacheTTLExplicitRequestOverridesNoCachePatterns(t *testing.T) {
	scfg := config.ServerConfig{DefaultCacheTTL: "30s", NoCacheTools: []string{"search-*"}}
	req := 5 * time.Second

	ttl, ok, err := effectiveCacheTTL(scfg, "search-repositories", &req)
	if err != nil {
		t.Fatalf("effectiveCacheTTL() error = %v", err)
	}
	if !ok {
		t.Fatal("effectiveCacheTTL() enabled = false, want true")
	}
	if ttl != 5*time.Second {
		t.Fatalf("effectiveCacheTTL() ttl = %s, want %s", ttl, 5*time.Second)
	}
}

func TestEffectiveCacheTTLExplicitRequestOverridesPerToolDisable(t *testing.T) {
	disabled := false
	scfg := config.ServerConfig{
		DefaultCacheTTL: "30s",
		Tools: map[string]config.ToolConfig{
			"search_repositories": {Cache: &disabled},
		},
	}
	req := 8 * time.Second

	ttl, ok, err := effectiveCacheTTL(scfg, "search_repositories", &req)
	if err != nil {
		t.Fatalf("effectiveCacheTTL() error = %v", err)
	}
	if !ok {
		t.Fatal("effectiveCacheTTL() enabled = false, want true")
	}
	if ttl != 8*time.Second {
		t.Fatalf("effectiveCacheTTL() ttl = %s, want %s", ttl, 8*time.Second)
	}
}

func TestEffectiveCacheTTLNoCacheRequestDisablesCaching(t *testing.T) {
	scfg := config.ServerConfig{DefaultCacheTTL: "30s"}
	noCache := time.Duration(0)

	ttl, ok, err := effectiveCacheTTL(scfg, "search", &noCache)
	if err != nil {
		t.Fatalf("effectiveCacheTTL() error = %v", err)
	}
	if ok {
		t.Fatal("effectiveCacheTTL() enabled = true, want false")
	}
	if ttl != 0 {
		t.Fatalf("effectiveCacheTTL() ttl = %s, want 0", ttl)
	}
}

func TestEffectiveCacheTTLToolCacheTrueRequiresDefaultTTL(t *testing.T) {
	enabled := true
	scfg := config.ServerConfig{Tools: map[string]config.ToolConfig{"search": {Cache: &enabled}}}

	ttl, ok, err := effectiveCacheTTL(scfg, "search", nil)
	if err != nil {
		t.Fatalf("effectiveCacheTTL() error = %v", err)
	}
	if ok {
		t.Fatal("effectiveCacheTTL() enabled = true, want false")
	}
	if ttl != 0 {
		t.Fatalf("effectiveCacheTTL() ttl = %s, want 0", ttl)
	}
}

func TestCallToolVerboseIncludesCacheHitLog(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{"github": {}},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, errors.New("pool should not be called on cache hit")
	}
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	deps.cachePut = func(_ string, _ string, _ json.RawMessage, _ []byte, _ int, _ time.Duration) error {
		return nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, true, deps)
	if resp.Stderr != "mcpx: cache hit" {
		t.Fatalf("callTool() stderr = %q, want %q", resp.Stderr, "mcpx: cache hit")
	}
}

func TestCallToolVerboseIncludesCacheAgeAndTTLWhenAvailable(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{"github": {}},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, errors.New("pool should not be called on cache hit")
	}
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	deps.cacheGetMetadata = func(_ string, _ string, _ json.RawMessage) (time.Duration, time.Duration, bool) {
		return 23 * time.Second, 60 * time.Second, true
	}
	deps.cachePut = func(_ string, _ string, _ json.RawMessage, _ []byte, _ int, _ time.Duration) error {
		return nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, true, deps)
	if resp.Stderr != "mcpx: cache hit (age=23s ttl=1m0s)" {
		t.Fatalf("callTool() stderr = %q, want %q", resp.Stderr, "mcpx: cache hit (age=23s ttl=1m0s)")
	}
}

func TestCallToolUsageErrorIncludesStderrByDefault(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, mcppool.ErrInvalidParams
	}
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return nil, 0, false
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "github", "search", json.RawMessage(`{}`), nil, false, deps)
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("callTool() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
	if resp.Stderr == "" {
		t.Fatal("callTool() stderr is empty, want usage diagnostics")
	}
	if !strings.Contains(resp.Stderr, "calling tool:") {
		t.Fatalf("callTool() stderr = %q, want calling tool prefix", resp.Stderr)
	}
	if !strings.Contains(strings.ToLower(resp.Stderr), "invalid params") {
		t.Fatalf("callTool() stderr = %q, want invalid params details", resp.Stderr)
	}
}

func TestCallToolCacheKeyUsesRequestedToolName(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	poolCalls := 0
	cacheStore := map[string][]byte{}

	deps := runtimeDefaultDeps()
	deps.poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _ string, tool string) (*mcppool.ToolInfo, error) {
		return &mcppool.ToolInfo{Name: tool}, nil
	}
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return &mcp.CallToolResult{StructuredContent: map[string]any{"count": poolCalls}}, nil
	}
	deps.cacheGet = func(_ string, tool string, _ json.RawMessage) ([]byte, int, bool) {
		content, ok := cacheStore[tool]
		if !ok {
			return nil, 0, false
		}
		return content, ipc.ExitOK, true
	}
	deps.cachePut = func(_ string, tool string, _ json.RawMessage, content []byte, _ int, _ time.Duration) error {
		cacheStore[tool] = content
		return nil
	}

	dummyPool := &mcppool.Pool{}
	first := callToolWithDeps(context.Background(), cfg, dummyPool, ka, "github", "search_repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false, deps)
	second := callToolWithDeps(context.Background(), cfg, dummyPool, ka, "github", "search_repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false, deps)

	if poolCalls != 1 {
		t.Fatalf("pool calls = %d, want 1", poolCalls)
	}
	if string(second.Content) != string(first.Content) {
		t.Fatalf("second content = %q, want cache hit matching first %q", second.Content, first.Content)
	}
	if _, ok := cacheStore["search_repositories"]; !ok {
		t.Fatalf("cache store missing key %q", "search_repositories")
	}
}

func TestCallToolRequiresJSONForComposedInputSchema(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	called := false
	cacheReads := 0
	deps := runtimeDefaultDeps()
	deps.poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _ string, tool string) (*mcppool.ToolInfo, error) {
		return &mcppool.ToolInfo{
			Name: tool,
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"target":{"oneOf":[{"type":"string"},{"type":"integer"}]}}
			}`),
		}, nil
	}
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, _ string, _ *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	}
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		cacheReads++
		return []byte("cached\n"), ipc.ExitOK, true
	}

	dummyPool := &mcppool.Pool{}
	reqCache := 30 * time.Second
	flagResp := callToolWithInputModeWithDeps(context.Background(), cfg, dummyPool, ka, "github", "search", json.RawMessage(`{"target":"x"}`), false, &reqCache, false, deps)
	if flagResp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("flag call exit = %d, want %d", flagResp.ExitCode, ipc.ExitUsageErr)
	}
	if called {
		t.Fatal("tool was called for an unsafe flag representation")
	}
	if cacheReads != 0 {
		t.Fatalf("flag call cache reads = %d, want 0 before unsafe schema rejection", cacheReads)
	}

	jsonResp := callToolWithInputModeWithDeps(context.Background(), cfg, dummyPool, ka, "github", "search", json.RawMessage(`{"target":"x"}`), true, &reqCache, false, deps)
	if jsonResp.ExitCode != ipc.ExitOK {
		t.Fatalf("JSON call exit = %d, want %d (stderr=%q)", jsonResp.ExitCode, ipc.ExitOK, jsonResp.Stderr)
	}
	if called {
		t.Fatal("JSON cache hit unexpectedly called the tool")
	}
	if cacheReads != 1 {
		t.Fatalf("JSON call cache reads = %d, want 1", cacheReads)
	}
}

func TestCallToolVirtualServerRoutesThroughCodexApps(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.cacheGet = func(_ string, _ string, _ json.RawMessage) ([]byte, int, bool) {
		return nil, 0, false
	}
	deps.cachePut = func(_ string, _ string, _ json.RawMessage, _ []byte, _ int, _ time.Duration) error {
		return nil
	}
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		t.Fatal("poolListTools should not be called for direct virtual-server tool routing")
		return nil, nil
	}
	deps.poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
		t.Fatal("poolToolInfoByName should not be called for virtual-server tool routing")
		return nil, nil
	}
	deps.poolCallToolWithInfo = func(_ context.Context, _ *mcppool.Pool, server string, info *mcppool.ToolInfo, _ json.RawMessage) (*mcp.CallToolResult, error) {
		if server != codexAppsServerName {
			t.Fatalf("poolCallToolWithInfo server = %q, want %q", server, codexAppsServerName)
		}
		if info == nil || info.Name != "linear_get_profile" {
			t.Fatalf("poolCallToolWithInfo info name = %v, want %q", info, "linear_get_profile")
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	}

	resp := callToolWithDeps(context.Background(), cfg, nil, ka, "linear", "linear_get_profile", json.RawMessage(`{}`), nil, false, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("callTool() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	if string(resp.Content) != "{\"ok\":true}\n" {
		t.Fatalf("callTool() content = %q, want %q", string(resp.Content), "{\"ok\":true}\n")
	}
}
