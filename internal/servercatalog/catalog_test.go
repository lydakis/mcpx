package servercatalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestServerNamesHidesCodexAppsAndAddsVirtualApps(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"playwright":        {},
			CodexAppsServerName: {},
			"supermemory":       {},
		},
	}

	catalog := New(cfg, func(_ context.Context, server string) ([]mcppool.ToolInfo, error) {
		if server != CodexAppsServerName {
			t.Fatalf("listTools server = %q, want %q", server, CodexAppsServerName)
		}
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile"},
			{Name: "zillow_get_zestimate"},
			{Name: "google calendar_search"},
		}, nil
	})

	names, err := catalog.ServerNames(context.Background())
	if err != nil {
		t.Fatalf("ServerNames() error = %v", err)
	}

	want := []string{"google_calendar", "linear", "playwright", "supermemory", "zillow"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ServerNames() = %#v, want %#v", names, want)
	}
}

func TestResolveReturnsConfiguredRouteWithoutCodexAppsProbe(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"playwright":        {},
			CodexAppsServerName: {},
		},
	}
	calls := 0
	catalog := New(cfg, func(_ context.Context, _ string) ([]mcppool.ToolInfo, error) {
		calls++
		return nil, nil
	})

	route, tools, found, err := catalog.Resolve(context.Background(), "playwright")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	if route.Backend != "playwright" || route.ConfigServer != "playwright" || route.IsVirtual() {
		t.Fatalf("Resolve() route = %#v, want configured non-virtual route", route)
	}
	if len(tools) != 0 {
		t.Fatalf("Resolve() tools = %#v, want empty", tools)
	}
	if calls != 0 {
		t.Fatalf("codex list-tools calls = %d, want 0", calls)
	}
}

func TestResolveVirtualServerReturnsCodexAppsRouteAndTools(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			CodexAppsServerName: {},
		},
	}
	catalog := New(cfg, func(_ context.Context, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile"},
			{Name: "zillow_get_zestimate"},
		}, nil
	})

	route, tools, found, err := catalog.Resolve(context.Background(), "linear")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	if route.Backend != CodexAppsServerName || route.ConfigServer != CodexAppsServerName || !route.IsVirtual() || route.VirtualPrefix != "linear" {
		t.Fatalf("Resolve() route = %#v, want linear virtual codex_apps route", route)
	}
	if len(tools) != 2 {
		t.Fatalf("Resolve() tools len = %d, want 2", len(tools))
	}
}

func TestResolveForToolFastPathUsesToolPrefix(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			CodexAppsServerName: {},
		},
	}
	calls := 0
	catalog := New(cfg, func(_ context.Context, _ string) ([]mcppool.ToolInfo, error) {
		calls++
		return nil, nil
	})

	route, found, err := catalog.ResolveForTool(context.Background(), "linear", "linear_get_profile")
	if err != nil {
		t.Fatalf("ResolveForTool() error = %v", err)
	}
	if !found {
		t.Fatal("ResolveForTool() found = false, want true")
	}
	if route.VirtualPrefix != "linear" || !route.IsVirtual() {
		t.Fatalf("ResolveForTool() route = %#v, want linear virtual route", route)
	}
	if calls != 0 {
		t.Fatalf("codex list-tools calls = %d, want 0 for prefix fast-path", calls)
	}
}

func TestToolInfoAndToolBelongsToRoute(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			CodexAppsServerName: {},
		},
	}
	catalog := New(cfg, nil)
	route := Route{
		Backend:       CodexAppsServerName,
		ConfigServer:  CodexAppsServerName,
		VirtualName:   "linear",
		VirtualPrefix: "linear",
	}
	tools := []mcppool.ToolInfo{
		{Name: "linear_get_profile"},
		{Name: "zillow_get_zestimate"},
	}

	if !catalog.ToolBelongsToRoute(route, "linear_get_profile") {
		t.Fatal("ToolBelongsToRoute(linear_get_profile) = false, want true")
	}
	if catalog.ToolBelongsToRoute(route, "zillow_get_zestimate") {
		t.Fatal("ToolBelongsToRoute(zillow_get_zestimate) = true, want false")
	}

	info, ok := catalog.ToolInfo(route, tools, "linear_get_profile")
	if !ok || info == nil || info.Name != "linear_get_profile" {
		t.Fatalf("ToolInfo(linear_get_profile) = (%#v, %v), want matching info + true", info, ok)
	}
	if _, ok := catalog.ToolInfo(route, tools, "zillow_get_zestimate"); ok {
		t.Fatal("ToolInfo(zillow_get_zestimate) ok = true, want false for wrong virtual prefix")
	}
}
