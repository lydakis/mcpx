package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestListToolsOutputsKebabCaseAndDeduplicatesAliases(t *testing.T) {
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
			{Name: "search_repositories", Description: "Search repos"},
			{Name: "search-repositories", Description: "Duplicate alias"},
			{Name: "list_issues", Description: "List issues"},
		}, nil
	}

	resp := listTools(context.Background(), cfg, nil, ka, "github")
	want := "list-issues\tList issues\nsearch-repositories\tSearch repos\n"

	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}
	if string(resp.Content) != want {
		t.Fatalf("listTools() content = %q, want %q", resp.Content, want)
	}
}

func TestToolSchemaPayloadUsesKebabDisplayName(t *testing.T) {
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

	resp := toolSchema(context.Background(), cfg, nil, ka, "github", "search-repositories")
	if resp.ExitCode != 0 {
		t.Fatalf("toolSchema() exit = %d, want 0", resp.ExitCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Content, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["name"] != "search-repositories" {
		t.Fatalf("payload name = %v, want %q", payload["name"], "search-repositories")
	}
}
