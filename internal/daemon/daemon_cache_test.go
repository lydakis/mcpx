package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/mark3labs/mcp-go/mcp"
)

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
		return nil, mcp.ErrInvalidParams
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
