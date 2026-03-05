package daemon

import (
	"context"
	"reflect"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestForgetRuntimeEphemeralServerRemovesOverlayAndOrderEntries(t *testing.T) {
	t.Parallel()

	servers := map[string]config.ServerConfig{
		"one": {Command: "echo"},
		"two": {Command: "echo"},
	}
	order := []string{"one", " ", "two", "one"}

	changed := forgetRuntimeEphemeralServer(&servers, &order, " one ")
	if !changed {
		t.Fatal("forgetRuntimeEphemeralServer() = false, want true")
	}
	if _, ok := servers["one"]; ok {
		t.Fatalf("servers still contains removed key: %#v", servers)
	}
	if _, ok := servers["two"]; !ok {
		t.Fatalf("servers missing preserved key: %#v", servers)
	}
	wantOrder := []string{"two"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order = %#v, want %#v", order, wantOrder)
	}
}

func TestForgetRuntimeEphemeralServerNoopOnBlankName(t *testing.T) {
	t.Parallel()

	servers := map[string]config.ServerConfig{
		"one": {Command: "echo"},
	}
	order := []string{"one"}

	changed := forgetRuntimeEphemeralServer(&servers, &order, " ")
	if changed {
		t.Fatal("forgetRuntimeEphemeralServer(blank) = true, want false")
	}
}

func TestForgetRuntimeEphemeralServerPrunesBlankOrderEntries(t *testing.T) {
	t.Parallel()

	servers := map[string]config.ServerConfig{
		"two": {Command: "echo"},
	}
	order := []string{"", " two ", " "}

	changed := forgetRuntimeEphemeralServer(&servers, &order, "missing")
	if !changed {
		t.Fatal("forgetRuntimeEphemeralServer() = false, want true when order cleanup occurs")
	}

	wantOrder := []string{"two"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order = %#v, want %#v", order, wantOrder)
	}
	if _, ok := servers["two"]; !ok {
		t.Fatalf("servers unexpectedly changed: %#v", servers)
	}
}

func TestRuntimeEphemeralServerOrderSortsTrimmedNames(t *testing.T) {
	t.Parallel()

	servers := map[string]config.ServerConfig{
		" zed ": {Command: "echo"},
		"alpha": {Command: "echo"},
		"":      {Command: "echo"},
		" beta": {Command: "echo"},
	}

	got := runtimeEphemeralServerOrder(servers)
	want := []string{"alpha", "beta", "zed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimeEphemeralServerOrder() = %#v, want %#v", got, want)
	}
}

func TestMatchesNoCachePatternIgnoresInvalidPatterns(t *testing.T) {
	t.Parallel()

	scfg := config.ServerConfig{
		NoCacheTools: []string{"[", "search-*"},
	}

	if !matchesNoCachePattern(scfg, "search-repositories") {
		t.Fatal("matchesNoCachePattern() = false, want true for valid fallback pattern")
	}
	if matchesNoCachePattern(config.ServerConfig{NoCacheTools: []string{"[", "list-*"}}, "search-repositories") {
		t.Fatal("matchesNoCachePattern() = true, want false when no valid pattern matches")
	}
}

func TestRuntimeDepsWithDefaultsFillsZeroValueAndPreservesCustomHooks(t *testing.T) {
	t.Parallel()

	customCalled := false
	deps := runtimeDeps{
		loadConfig: func() (*config.Config, error) {
			customCalled = true
			return &config.Config{}, nil
		},
	}

	got := deps.withDefaults()
	if got.poolListTools == nil || got.poolToolInfoByName == nil || got.poolCallToolWithInfo == nil {
		t.Fatal("withDefaults() did not populate pool hooks")
	}
	if got.cacheGet == nil || got.cacheGetMetadata == nil || got.cachePut == nil {
		t.Fatal("withDefaults() did not populate cache hooks")
	}
	if got.poolReset == nil || got.poolSetConfig == nil || got.poolClose == nil || got.keepaliveStop == nil {
		t.Fatal("withDefaults() did not populate lifecycle hooks")
	}
	if got.mergeFallbackForCWD == nil || got.validateConfig == nil || got.signalShutdownProcess == nil {
		t.Fatal("withDefaults() did not populate config/process hooks")
	}

	if _, err := got.loadConfig(); err != nil {
		t.Fatalf("custom loadConfig error = %v", err)
	}
	if !customCalled {
		t.Fatal("withDefaults() replaced custom loadConfig hook")
	}
}

func TestRuntimeDefaultDepsWrappersHandleNilAndConcreteValues(t *testing.T) {
	t.Parallel()

	deps := runtimeDefaultDeps()

	// Nil-safe closures should not panic.
	deps.poolReset(nil, nil)
	deps.poolSetConfig(nil, nil)
	deps.poolClose(nil, "missing")
	deps.keepaliveStop(nil)

	pool := mcppool.New(&config.Config{Servers: map[string]config.ServerConfig{}})
	nextCfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo"},
		},
	}

	deps.poolSetConfig(pool, nextCfg)
	deps.poolReset(pool, nextCfg)
	deps.poolClose(pool, "missing")

	ka := NewKeepalive(pool)
	ka.TouchDaemon()
	deps.keepaliveStop(ka)
	ka.mu.Lock()
	timerCount := len(ka.timers)
	ka.mu.Unlock()
	if timerCount != 0 {
		t.Fatalf("keepalive timers after stop = %d, want 0", timerCount)
	}

	if _, err := deps.poolListTools(context.Background(), pool, "missing"); err == nil {
		t.Fatal("poolListTools() error = nil, want unknown server error")
	}
	if _, err := deps.poolToolInfoByName(context.Background(), pool, "missing", "search"); err == nil {
		t.Fatal("poolToolInfoByName() error = nil, want unknown server error")
	}
	if _, err := deps.poolCallToolWithInfo(context.Background(), pool, "missing", &mcppool.ToolInfo{Name: "search"}, nil); err == nil {
		t.Fatal("poolCallToolWithInfo() error = nil, want unknown server error")
	}
}
