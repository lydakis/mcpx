package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/cache"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestListToolsOutputsNativeNamesAndShortDescriptionsByDefault(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
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

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, "github", false, deps)

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

func TestReloadServerClosesOAuthAliasesSharingCredential(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"primary": {URL: "https://example.com/mcp", OAuth: true},
		"alias":   {URL: "https://example.com/mcp", OAuth: true},
		"header":  {URL: "https://example.com/mcp", OAuth: true, Headers: map[string]string{"authorization": "Bearer explicit"}},
		"other":   {URL: "https://other.example.com/mcp", OAuth: true},
		"stdio":   {Command: "server"},
	}}
	args := json.RawMessage(`{"query":"mcp"}`)
	if err := cache.Put("primary", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	var closed []string
	deps := runtimeDefaultDeps()
	deps.poolClose = func(_ *mcppool.Pool, server string) {
		closed = append(closed, server)
	}

	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:   "reload_server",
		Server: "primary",
	}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("reload_server exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	want := []string{"alias", "primary"}
	if !reflect.DeepEqual(closed, want) {
		t.Fatalf("closed servers = %#v, want %#v", closed, want)
	}
	if _, _, ok := cache.Get("primary", "search", args); ok {
		t.Fatal("response cache hit after OAuth reload, want miss")
	}
}

func TestCredentialTransitionDrainsOAuthAliasesBeforeAcknowledging(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"primary": {URL: "https://example.com/mcp", OAuth: true},
		"alias":   {URL: "https://example.com/mcp", OAuth: true},
		"other":   {URL: "https://other.example.com/mcp", OAuth: true},
	}}
	var gotNames []string
	var gotToken string
	deps := runtimeDefaultDeps()
	deps.poolBeginCredentialTransition = func(_ *mcppool.Pool, names []string, token string) error {
		gotNames = append([]string(nil), names...)
		gotToken = token
		return nil
	}

	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:       "prepare_credential_transition",
		Server:     "primary",
		Transition: "transition-token",
	}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("prepare_credential_transition exit = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if want := []string{"alias", "primary"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("transition servers = %#v, want %#v", gotNames, want)
	}
	if gotToken != "transition-token" {
		t.Fatalf("transition token = %q, want transition-token", gotToken)
	}
}

func TestCredentialTransitionReleasesFenceOnlyAfterCacheClear(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"primary": {URL: "https://example.com/mcp", OAuth: true},
	}}
	deps := runtimeDefaultDeps()
	deps.cacheClear = func() error { return errors.New("cache unavailable") }
	ended := false
	deps.poolEndCredentialTransition = func(_ *mcppool.Pool, token string) {
		ended = true
	}

	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:       "reload_server",
		Server:     "primary",
		Transition: "transition-token",
	}, deps)
	if resp.ExitCode != ipc.ExitInternal {
		t.Fatalf("reload_server exit = %d, want %d", resp.ExitCode, ipc.ExitInternal)
	}
	if ended {
		t.Fatal("reload_server released credential fence after cache clear failed")
	}
}

func TestCredentialTransitionCleanupSurvivesServerRemoval(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	deps := runtimeDefaultDeps()
	cleared := false
	deps.cacheClear = func() error {
		cleared = true
		return nil
	}
	deps.cacheRemoveCredentialTransition = func(token string) error {
		if token != "transition-token" {
			t.Fatalf("completed token = %q", token)
		}
		return nil
	}
	var ended string
	deps.poolEndCredentialTransition = func(_ *mcppool.Pool, token string) {
		ended = token
	}

	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:       "reload_server",
		Server:     "removed-alias",
		Transition: "transition-token",
	}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("reload_server exit = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if !cleared || ended != "transition-token" {
		t.Fatalf("cleanup state: cleared=%v ended=%q", cleared, ended)
	}
}

func TestCredentialTransitionKeepsFenceWhenMarkerCompletionFails(t *testing.T) {
	deps := runtimeDefaultDeps()
	deps.cacheClear = func() error { return nil }
	deps.cacheRemoveCredentialTransition = func(string) error {
		return errors.New("marker unavailable")
	}
	ended := false
	deps.poolEndCredentialTransition = func(*mcppool.Pool, string) { ended = true }

	resp := dispatchWithDeps(context.Background(), &config.Config{}, nil, nil, &ipc.Request{
		Type:       "reload_server",
		Transition: "transition-token",
	}, deps)
	if resp.ExitCode != ipc.ExitInternal || !strings.Contains(resp.Stderr, "marker unavailable") {
		t.Fatalf("reload_server response = %#v, want marker-completion failure", resp)
	}
	if ended {
		t.Fatal("credential fence released before transition marker completed")
	}
}

func TestCredentialTransitionDaemonCompletesMarkerBeforeReply(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	transition, err := cache.BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:       "reload_server",
		Server:     "removed-alias",
		Transition: transition,
	}, runtimeDefaultDeps())
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("reload_server exit = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	if cache.CredentialTransitionPending() {
		t.Fatal("daemon replied before completing its credential-transition marker")
	}
}

func TestCredentialTransitionBlocksAffectedServerButAllowsUnrelatedWork(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
		"local":  {Command: "server"},
	}}
	pool := mcppool.New(cfg)
	if err := pool.BeginCredentialTransition([]string{"remote"}, "transition-token"); err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	defer pool.EndCredentialTransition("transition-token")

	deps := runtimeDefaultDeps()
	deps.cacheCredentialTransitionPending = func() bool { return true }
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{{Name: server + "_tool"}}, nil
	}

	blocked := dispatchWithDeps(context.Background(), cfg, pool, nil, &ipc.Request{
		Type:   "list_tools",
		Server: "remote",
	}, deps)
	if blocked.ExitCode != ipc.ExitToolErr || !strings.Contains(blocked.Stderr, "credential transition") {
		t.Fatalf("affected response = %#v, want credential-transition rejection", blocked)
	}

	allowed := dispatchWithDeps(context.Background(), cfg, pool, nil, &ipc.Request{
		Type:   "list_tools",
		Server: "local",
	}, deps)
	if allowed.ExitCode != ipc.ExitOK {
		t.Fatalf("unrelated response exit = %d, want %d (stderr=%q)", allowed.ExitCode, ipc.ExitOK, allowed.Stderr)
	}
}

func TestDiagnoseServerUsesDaemonPoolAndReturnsRedactedMetadata(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp"},
	}}
	deps := runtimeDefaultDeps()
	deps.poolDiagnose = func(_ context.Context, _ *mcppool.Pool, server string) (mcppool.Diagnostics, error) {
		if server != "remote" {
			t.Fatalf("diagnosed server = %q, want remote", server)
		}
		return mcppool.Diagnostics{
			Transport:       "streamable_http",
			ProtocolVersion: "2026-07-28",
			AuthSource:      "oauth_os_store",
			ToolCount:       4,
		}, nil
	}

	resp := dispatchWithDeps(context.Background(), cfg, nil, nil, &ipc.Request{
		Type:   "diagnose_server",
		Server: "remote",
	}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("diagnose_server exit = %d, want %d (stderr=%q)", resp.ExitCode, ipc.ExitOK, resp.Stderr)
	}
	var got mcppool.Diagnostics
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.ProtocolVersion != "2026-07-28" || got.ToolCount != 4 || got.AuthSource != "oauth_os_store" {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestDiagnoseServerDoesNotReturnServerControlledErrorText(t *testing.T) {
	const secret = "opaque-oauth-access-token"
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}
	deps := runtimeDefaultDeps()
	deps.poolDiagnose = func(context.Context, *mcppool.Pool, string) (mcppool.Diagnostics, error) {
		return mcppool.Diagnostics{}, errors.New("server reflected " + secret)
	}

	resp := diagnoseServerWithDeps(context.Background(), cfg, nil, nil, "remote", deps)
	if resp.ExitCode != ipc.ExitToolErr {
		t.Fatalf("diagnose_server exit = %d, want %d", resp.ExitCode, ipc.ExitToolErr)
	}
	if strings.Contains(resp.Stderr, secret) {
		t.Fatalf("diagnose_server leaked server-controlled credential: %q", resp.Stderr)
	}
	if resp.Stderr != "server diagnostics failed" {
		t.Fatalf("diagnose_server stderr = %q, want safe failure category", resp.Stderr)
	}
}

func TestListToolsVerboseOutputsFullDescriptions(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	fullDesc := "Search repositories quickly with advanced filters\nSecond line with extra details"
	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{Name: "search_repositories", Description: fullDesc},
		}, nil
	}

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, "github", true, deps)

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

func TestListToolsCatalogIncludesSchemasAndAnnotations(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{{
			Name:         "search",
			Title:        "Search",
			Description:  "Search everything",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array"}}}`),
			Annotations:  json.RawMessage(`{"readOnlyHint":true}`),
		}}, nil
	}

	resp := listToolsResponseWithDeps(context.Background(), cfg, nil, ka, "github", true, true, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("catalog exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	var got []toolListEntry
	if err := json.Unmarshal(resp.Content, &got); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Search" || len(got[0].InputSchema) == 0 || len(got[0].OutputSchema) == 0 || len(got[0].Annotations) == 0 {
		t.Fatalf("catalog = %#v, want full descriptor", got)
	}
}

func TestListToolsJSONVerbosePreservesMultilineDescription(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{
			{
				Name:        "search_repositories",
				Description: "Search repositories quickly\nReturns:\n- id\n- name",
			},
			{Name: "list_issues"},
		}, nil
	}

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, "github", true, deps)
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
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolToolInfoByName = func(_ context.Context, _ *mcppool.Pool, _, _ string) (*mcppool.ToolInfo, error) {
		return &mcppool.ToolInfo{
			Name:         "search_repositories",
			Description:  "Search repos",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array"}}}`),
		}, nil
	}

	resp := toolSchemaWithDeps(context.Background(), cfg, nil, ka, "github", "search_repositories", deps)
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

func TestListServersHidesCodexAppsWithoutDiscovery(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github":            {},
			codexAppsServerName: {},
			"supermemory":       {},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github":      config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
			"supermemory": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		t.Fatal("list_servers must not call poolListTools")
		return nil, nil
	}

	resp := listServersWithDeps(context.Background(), cfg, nil, ka, false, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	got := decodeServerLines(resp.Content)
	want := []string{"github", "supermemory"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server list = %#v, want %#v", got, want)
	}
	entries := decodeServerEntries(resp.Content)
	for _, entry := range entries {
		switch entry.Name {
		case "github", "supermemory":
			if entry.Origin.Kind != config.ServerOriginKindMCPXConfig {
				t.Fatalf("server %q origin kind = %q, want %q", entry.Name, entry.Origin.Kind, config.ServerOriginKindMCPXConfig)
			}
		}
	}
	for _, name := range got {
		if name == codexAppsServerName {
			t.Fatalf("server list = %#v, want %q omitted", got, codexAppsServerName)
		}
	}
}

func TestListServersDoesNotExposeCodexBackendWhenIncludingHidden(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github":            {},
			codexAppsServerName: {},
			"supermemory":       {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		t.Fatal("list_servers must not call poolListTools")
		return nil, nil
	}

	resp := listServersWithDeps(context.Background(), cfg, nil, ka, true, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	got := decodeServerLines(resp.Content)
	want := []string{"github", "supermemory"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server list = %#v, want %#v", got, want)
	}
	entries := decodeServerEntries(resp.Content)
	for _, entry := range entries {
		if entry.Origin.Kind != config.ServerOriginKindMCPXConfig {
			t.Fatalf("server %q origin kind = %q, want %q", entry.Name, entry.Origin.Kind, config.ServerOriginKindMCPXConfig)
		}
	}
	if resp.Stderr != "" {
		t.Fatalf("listServers() stderr = %q, want empty", resp.Stderr)
	}
}

func TestListServersSkipsRuntimeEphemeralServers(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github":     {},
			"/tmp/mcp":   {URL: "https://example.com/mcp"},
			"filesystem": {},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github":     config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
			"/tmp/mcp":   config.NewServerOrigin(config.ServerOriginKindRuntimeEphemeral, ""),
			"filesystem": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	resp := listServersWithDeps(context.Background(), cfg, nil, ka, false, runtimeDefaultDeps())
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	got := decodeServerLines(resp.Content)
	want := []string{"filesystem", "github"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server list = %#v, want %#v", got, want)
	}
}

func TestListServersIncludesRuntimeEphemeralServersWhenRequested(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github":     {},
			"/tmp/mcp":   {URL: "https://example.com/mcp"},
			"filesystem": {},
		},
		ServerOrigins: map[string]config.ServerOrigin{
			"github":     config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
			"/tmp/mcp":   config.NewServerOrigin(config.ServerOriginKindRuntimeEphemeral, ""),
			"filesystem": config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx/config.toml"),
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	resp := listServersWithDeps(context.Background(), cfg, nil, ka, true, runtimeDefaultDeps())
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("listServers() exit = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}

	got := decodeServerLines(resp.Content)
	want := []string{"/tmp/mcp", "filesystem", "github"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server list = %#v, want %#v", got, want)
	}
}

func TestListToolsVirtualServerFiltersCodexAppsTools(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		if server != codexAppsServerName {
			t.Fatalf("poolListTools server = %q, want %q", server, codexAppsServerName)
		}
		return []mcppool.ToolInfo{
			{Name: "linear_get_profile", Description: "Linear profile"},
			{Name: "linear_search_issues", Description: "Linear search"},
			{Name: "zillow_get_zestimate", Description: "Zillow estimate"},
		}, nil
	}

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, "linear", false, deps)
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
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
		return []mcppool.ToolInfo{{Name: "linear_get_profile"}}, nil
	}

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, codexAppsServerName, false, deps)
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("listTools() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
}

func TestListToolsUnknownServerSetsStructuredErrorCode(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	resp := listToolsWithDeps(context.Background(), cfg, nil, ka, "missing", false, runtimeDefaultDeps())
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("listTools() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
	if resp.ErrorCode != ipc.ErrorCodeUnknownServer {
		t.Fatalf("listTools() error code = %q, want %q", resp.ErrorCode, ipc.ErrorCodeUnknownServer)
	}
	if !strings.Contains(resp.Stderr, "unknown server: missing") {
		t.Fatalf("listTools() stderr = %q, want unknown-server message", resp.Stderr)
	}
}

func TestToolSchemaVirtualServerRejectsToolsOutsideConnector(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			codexAppsServerName: {},
		},
	}
	ka := NewKeepalive(nil)
	defer ka.Stop()

	deps := runtimeDefaultDeps()
	deps.poolListTools = func(_ context.Context, _ *mcppool.Pool, _ string) ([]mcppool.ToolInfo, error) {
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

	resp := toolSchemaWithDeps(context.Background(), cfg, nil, ka, "linear", "zillow_get_zestimate", deps)
	if resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("toolSchema() exit = %d, want %d", resp.ExitCode, ipc.ExitUsageErr)
	}
	if !strings.Contains(resp.Stderr, "tool zillow_get_zestimate not found on server linear") {
		t.Fatalf("toolSchema() stderr = %q, want missing-tool message", resp.Stderr)
	}
}

func decodeServerEntries(payload []byte) []serverListEntry {
	var entries []serverListEntry
	if err := json.Unmarshal(payload, &entries); err != nil {
		return nil
	}
	return entries
}

func decodeServerLines(payload []byte) []string {
	entries := decodeServerEntries(payload)
	if len(entries) == 0 {
		return nil
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}
