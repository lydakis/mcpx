package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCallToolUsesCachedResponseWhenPresent(t *testing.T) {
	restore := saveCallToolHooks()
	defer restore()

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

	poolCallTool = func(_ context.Context, _ *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return nil, errors.New("pool should not be called on cache hit")
	}
	cacheGet = func(server, tool string, args json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	cachePut = func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		cacheWrites++
		return nil
	}

	resp := callTool(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false)
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
	restore := saveCallToolHooks()
	defer restore()

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

	poolCallTool = func(_ context.Context, _ *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return &mcp.CallToolResult{
			StructuredContent: map[string]any{"ok": true},
		}, nil
	}
	cacheGet = func(server, tool string, args json.RawMessage) ([]byte, int, bool) {
		return nil, 0, false
	}
	cachePut = func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		cacheWrites++
		wroteTTL = ttl
		wroteExit = exitCode
		wroteContent = string(content)
		return nil
	}

	resp := callTool(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), nil, false)
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

func saveCallToolHooks() func() {
	oldPoolCallTool := poolCallTool
	oldPoolToolInfoByName := poolToolInfoByName
	oldCacheGet := cacheGet
	oldCacheGetMetadata := cacheGetMetadata
	oldCachePut := cachePut

	return func() {
		poolCallTool = oldPoolCallTool
		poolToolInfoByName = oldPoolToolInfoByName
		cacheGet = oldCacheGet
		cacheGetMetadata = oldCacheGetMetadata
		cachePut = oldCachePut
	}
}

func TestEffectiveCacheTTLExplicitRequestOverridesNoCachePatterns(t *testing.T) {
	scfg := config.ServerConfig{
		DefaultCacheTTL: "30s",
		NoCacheTools:    []string{"search-*"},
	}
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
	scfg := config.ServerConfig{
		Tools: map[string]config.ToolConfig{
			"search": {Cache: &enabled},
		},
	}

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
	restore := saveCallToolHooks()
	defer restore()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	poolCallTool = func(_ context.Context, _ *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, errors.New("pool should not be called on cache hit")
	}
	cacheGet = func(server, tool string, args json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	cachePut = func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		return nil
	}

	resp := callTool(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, true)
	if resp.Stderr != "mcpx: cache hit" {
		t.Fatalf("callTool() stderr = %q, want %q", resp.Stderr, "mcpx: cache hit")
	}
}

func TestCallToolVerboseIncludesCacheAgeAndTTLWhenAvailable(t *testing.T) {
	restore := saveCallToolHooks()
	defer restore()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	poolCallTool = func(_ context.Context, _ *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, errors.New("pool should not be called on cache hit")
	}
	cacheGet = func(server, tool string, args json.RawMessage) ([]byte, int, bool) {
		return []byte("cached\n"), ipc.ExitOK, true
	}
	cacheGetMetadata = func(server, tool string, args json.RawMessage) (time.Duration, time.Duration, bool) {
		return 23 * time.Second, 60 * time.Second, true
	}
	cachePut = func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		return nil
	}

	resp := callTool(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, true)
	if resp.Stderr != "mcpx: cache hit (age=23s ttl=1m0s)" {
		t.Fatalf("callTool() stderr = %q, want %q", resp.Stderr, "mcpx: cache hit (age=23s ttl=1m0s)")
	}
}

func TestCallToolCacheKeyCanonicalizesAliasSpellings(t *testing.T) {
	restore := saveCallToolHooks()
	defer restore()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second

	poolCalls := 0
	cacheStore := map[string][]byte{}

	poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _ string, _ string) (*mcppool.ToolInfo, error) {
		return &mcppool.ToolInfo{Name: "search_repositories"}, nil
	}
	poolCallTool = func(_ context.Context, _ *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		poolCalls++
		return &mcp.CallToolResult{
			StructuredContent: map[string]any{"count": poolCalls},
		}, nil
	}
	cacheGet = func(server, tool string, args json.RawMessage) ([]byte, int, bool) {
		content, ok := cacheStore[tool]
		if !ok {
			return nil, 0, false
		}
		return content, ipc.ExitOK, true
	}
	cachePut = func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
		cacheStore[tool] = content
		return nil
	}

	dummyPool := &mcppool.Pool{}
	first := callTool(context.Background(), cfg, dummyPool, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false)
	second := callTool(context.Background(), cfg, dummyPool, ka, "github", "search_repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false)

	if poolCalls != 1 {
		t.Fatalf("pool calls = %d, want 1", poolCalls)
	}
	if string(second.Content) != string(first.Content) {
		t.Fatalf("second content = %q, want cache hit matching first %q", second.Content, first.Content)
	}
	if _, ok := cacheStore["search_repositories"]; !ok {
		t.Fatalf("cache store missing key %q", "search_repositories")
	}
	if _, ok := cacheStore["search-repositories"]; ok {
		t.Fatalf("cache store unexpectedly has alias key %q", "search-repositories")
	}
}

func TestCallToolReturnsAliasCacheHitWithoutResolvingTool(t *testing.T) {
	restore := saveCallToolHooks()
	defer restore()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	reqCache := 30 * time.Second
	resolveCalls := 0

	poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
		resolveCalls++
		return nil, errors.New("tool lookup should not run on cache hit")
	}
	poolCallTool = func(_ context.Context, _ *mcppool.Pool, _, _ string, _ json.RawMessage) (*mcp.CallToolResult, error) {
		return nil, errors.New("pool should not be called on cache hit")
	}
	cacheGet = func(_ string, tool string, _ json.RawMessage) ([]byte, int, bool) {
		if tool != "search_repositories" {
			return nil, 0, false
		}
		return []byte("cached\n"), ipc.ExitOK, true
	}

	resp := callTool(context.Background(), cfg, nil, ka, "github", "search-repositories", json.RawMessage(`{"query":"mcp"}`), &reqCache, false)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("callTool() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	if string(resp.Content) != "cached\n" {
		t.Fatalf("callTool() content = %q, want %q", resp.Content, "cached\n")
	}
	if resolveCalls != 0 {
		t.Fatalf("resolve calls = %d, want 0", resolveCalls)
	}
}
