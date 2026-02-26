package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestListToolsOutputsNativeNamesAndShortDescriptionsByDefault(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{
				Name: "search_repositories",
				Description: "Search repositories quickly with advanced filters\n" +
					"Second line with extra details",
			},
			{Name: "search_repositories", Description: "Duplicate exact"},
			{Name: "list_issues", Description: "List issues"},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, "github", false)

	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}
	var got []toolListEntry
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("unmarshal json tool list: %v; payload=%q", err, string(resp.Content))
	}
	want := []toolListEntry{
		{Name: "list_issues", Description: "List issues"},
		{Name: "search_repositories", Description: "Search repositories quickly with advanced filters"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json tool list = %#v, want %#v", got, want)
	}
}

func TestListToolsVerboseOutputsFullDescriptions(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	fullDesc := "Search repositories quickly with advanced filters\nSecond line with extra details"
	poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{Name: "search_repositories", Description: fullDesc},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, "github", true)

	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}
	var got []toolListEntry
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("unmarshal json tool list: %v; payload=%q", err, string(resp.Content))
	}
	want := []toolListEntry{
		{Name: "search_repositories", Description: fullDesc},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json tool list = %#v, want %#v", got, want)
	}
}

func TestListToolsJSONVerbosePreservesMultilineDescription(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{
				Name:        "search_repositories",
				Description: "Search repositories quickly\nReturns:\n- id\n- name",
			},
			{Name: "list_issues"},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, "github", true)
	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}

	var got []toolListEntry
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("unmarshal json tool list: %v; payload=%q", err, string(resp.Content))
	}

	want := []toolListEntry{
		{Name: "list_issues"},
		{Name: "search_repositories", Description: "Search repositories quickly\nReturns:\n- id\n- name"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json tool list = %#v, want %#v", got, want)
	}
}

func TestSummarizeToolDescriptionTruncatesLongFirstLine(t *testing.T) {
	input := "This is a very long line " +
		"that should be truncated at the configured summary limit to keep default output compact for tool discovery."
	got := summarizeToolDescription(input)
	if len(got) > shortToolDescriptionMaxLen {
		t.Fatalf("summary length = %d, want <= %d (%q)", len(got), shortToolDescriptionMaxLen, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("summary = %q, want trailing ellipsis", got)
	}
}

func TestToolSchemaPayloadUsesNativeToolName(t *testing.T) {
	oldPoolToolInfoByName := poolToolInfoByName
	defer func() {
		poolToolInfoByName = oldPoolToolInfoByName
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
		return &mcppool.ToolInfo{
			Name:         "search_repositories",
			Description:  "Search repos",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array"}}}`),
		}, nil
	}

	resp := toolSchema(context.Background(), cfg, nil, ka, "github", "search_repositories")
	if resp.ExitCode != 0 {
		t.Fatalf("toolSchema() exit = %d, want 0", resp.ExitCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Content, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["name"] != "search_repositories" {
		t.Fatalf("payload name = %v, want %q", payload["name"], "search_repositories")
	}
}

func TestListServersHidesCodexAppsAndShowsVirtualServers(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github":            {},
			codexAppsServerName: {},
			"supermemory":       {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != codexAppsServerName {
			t.Fatalf("poolListTools server = %q, want %q", server, codexAppsServerName)
		}
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile"},
			{Name: "zillow_get_zestimate"},
			{Name: "google calendar_search"},
		}, nil
	}

	resp := listServers(context.Background(), cfg, nil, ka)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	got := decodeServerLines(resp.Content)
	want := []string{"github", "google_calendar", "linear", "supermemory", "zillow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server list = %#v, want %#v", got, want)
	}
	for _, name := range got {
		if name == codexAppsServerName {
			t.Fatalf("server list = %#v, want %q omitted", got, codexAppsServerName)
		}
	}
}

func TestListToolsVirtualServerFiltersCodexAppsTools(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != codexAppsServerName {
			t.Fatalf("poolListTools server = %q, want %q", server, codexAppsServerName)
		}
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile", Description: "Linear profile"},
			{Name: "linear_search_issues", Description: "Linear search"},
			{Name: "zillow_get_zestimate", Description: "Zillow estimate"},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, "linear", false)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listTools() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	var got []toolListEntry
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("unmarshal json tool list: %v; payload=%q", err, string(resp.Content))
	}
	want := []toolListEntry{
		{Name: "linear_get_profile", Description: "Linear profile"},
		{Name: "linear_search_issues", Description: "Linear search"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json tool list = %#v, want %#v", got, want)
	}
}

func TestListToolsCodexAppsServerNameIsNotAddressable(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile"},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, codexAppsServerName, false)
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("listTools() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
}

func TestToolSchemaVirtualServerRejectsToolsOutsideConnector(t *testing.T) {
	oldPoolListTools := poolListTools
	defer func() {
		poolListTools = oldPoolListTools
	}()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{
				Name:        "linear_get_profile",
				Description: "Linear profile",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			{
				Name:        "zillow_get_zestimate",
				Description: "Zillow estimate",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		}, nil
	}

	resp := toolSchema(context.Background(), cfg, nil, ka, "linear", "zillow_get_zestimate")
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("toolSchema() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
	if !strings.Contains(resp.Stderr, "tool zillow_get_zestimate not found on server linear") {
		t.Fatalf("toolSchema() stderr = %q, want missing-tool message", resp.Stderr)
	}
}

func decodeServerLines(payload []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}
