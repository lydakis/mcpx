package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
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
	want := "list_issues\tList issues\nsearch_repositories\tSearch repositories quickly with advanced filters\n"

	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}
	if string(resp.Content) != want {
		t.Fatalf("listTools() content = %q, want %q", resp.Content, want)
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
	want := "search_repositories\t" + fullDesc + "\n"

	if resp.ExitCode != 0 {
		t.Fatalf("listTools() exit = %d, want 0", resp.ExitCode)
	}
	if string(resp.Content) != want {
		t.Fatalf("listTools() content = %q, want %q", resp.Content, want)
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
