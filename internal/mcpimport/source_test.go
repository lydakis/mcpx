package mcpimport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSourcesExposeGeneralClientAdapters(t *testing.T) {
	var names []string
	for _, source := range Sources() {
		names = append(names, source.Name)
		if source.DisplayName == "" {
			t.Fatalf("source %q has no display name", source.Name)
		}
	}
	want := []string{"claude", "cline", "codex", "cursor", "kiro"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Sources() names = %#v, want %#v", names, want)
	}
}

func TestDisplayNameUsesSourceRegistry(t *testing.T) {
	if got := DisplayName("CoDeX"); got != "Codex" {
		t.Fatalf("DisplayName(codex) = %q, want Codex", got)
	}
}

func TestListCursorImportsSharedManifestContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifestDir := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	raw := `{"mcpServers":{
		"bad":{"url":"://credential-like-value"},
		"legacy":{"type":"sse","url":"https://example.com/sse"},
		"local":{"command":"launcher","args":["serve"],"cwd":"./plugin","env_vars":["API_TOKEN"]},
		"remote":{"type":"http","url":"https://example.com/mcp","env_http_headers":{"X-Token":"API_TOKEN"}}
	}}`
	if err := os.WriteFile(filepath.Join(manifestDir, "mcp.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	candidates, err := List(context.Background(), "cursor", "/workspace")
	if err != nil {
		t.Fatalf("List(cursor) error = %v", err)
	}
	local := candidateByName(t, candidates, "local")
	if !local.Supported || local.Server.CWD != filepath.Join(manifestDir, "plugin") {
		t.Fatalf("local candidate = %#v", local)
	}
	if _, exists := local.Server.Env["API_TOKEN"]; exists {
		t.Fatalf("API_TOKEN unexpectedly persisted from pass-through env_vars: %#v", local.Server.Env)
	}
	remote := candidateByName(t, candidates, "remote")
	if !remote.Supported || remote.Server.Headers["X-Token"] != "${API_TOKEN}" {
		t.Fatalf("remote candidate = %#v", remote)
	}
	legacy := candidateByName(t, candidates, "legacy")
	if legacy.Supported || legacy.Transport != "sse" || !strings.Contains(legacy.UnsupportedReason, "sse") {
		t.Fatalf("legacy candidate = %#v, want unsupported SSE transport", legacy)
	}
	bad := candidateByName(t, candidates, "bad")
	if bad.Supported || strings.Contains(bad.UnsupportedReason, "credential-like-value") {
		t.Fatalf("bad candidate leaked source values: %#v", bad)
	}
}

func TestListCursorPrefersNearestProjectManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".cursor")
	projectDir := filepath.Join(home, "projects", "app")
	workDir := filepath.Join(projectDir, "nested")
	for _, dir := range []string{userDir, filepath.Join(projectDir, ".cursor"), workDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(userDir, "mcp.json"), []byte(`{"mcpServers":{"shared":{"command":"user"},"user-only":{"command":"user-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(user) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"shared":{"command":"project"},"project-only":{"command":"project-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}

	candidates, err := List(context.Background(), "cursor", workDir)
	if err != nil {
		t.Fatalf("List(cursor) error = %v", err)
	}
	if got := candidateByName(t, candidates, "shared").Server.Command; got != "project" {
		t.Fatalf("shared command = %q, want nearest project definition", got)
	}
	for _, name := range []string{"project-only", "user-only"} {
		if !candidateByName(t, candidates, name).Supported {
			t.Fatalf("candidate %q is not supported", name)
		}
	}
}

func TestListClaudeHonorsLocalProjectUserPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "projects", "app")
	workDir := filepath.Join(projectDir, "nested")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	projectRaw := `{"mcpServers":{
		"shared":{"command":"project"},
		"project-over-user":{"command":"project"},
		"project-only":{"command":"project-only"}
	}}`
	if err := os.WriteFile(filepath.Join(projectDir, ".mcp.json"), []byte(projectRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}
	userRaw := `{
		"mcpServers":{
			"shared":{"command":"user"},
			"project-over-user":{"command":"user"},
			"user-only":{"command":"user-only"}
		},
		"projects":{"` + projectDir + `":{"mcpServers":{
			"shared":{"command":"local"},
			"local-only":{"command":"local-only"}
		}}}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(userRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(user) error = %v", err)
	}

	candidates, err := List(context.Background(), "claude", workDir)
	if err != nil {
		t.Fatalf("List(claude) error = %v", err)
	}
	if got := candidateByName(t, candidates, "shared").Server.Command; got != "local" {
		t.Fatalf("shared command = %q, want local override", got)
	}
	if got := candidateByName(t, candidates, "project-over-user").Server.Command; got != "project" {
		t.Fatalf("project-over-user command = %q, want project override", got)
	}
	for _, name := range []string{"local-only", "project-only", "user-only"} {
		if !candidateByName(t, candidates, name).Supported {
			t.Fatalf("candidate %q is not supported", name)
		}
	}
}

func TestListClineDiscoversCurrentProjectAndDataDirectoryScopes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, "custom-cline-data")
	t.Setenv("CLINE_DATA_DIR", dataDir)
	projectDir := filepath.Join(home, "projects", "app")
	workDir := filepath.Join(projectDir, "nested")
	var legacyDir string
	switch runtime.GOOS {
	case "darwin":
		legacyDir = filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings")
	case "linux":
		legacyDir = filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings")
	default:
		t.Skip("Cline legacy path is not defined for this platform")
	}
	for _, dir := range []string{filepath.Join(dataDir, "settings"), filepath.Join(projectDir, ".cline"), workDir, legacyDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dataDir, "settings", "cline_mcp_settings.json"), []byte(`{"mcpServers":{"shared":{"command":"global"},"global-only":{"command":"global-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(global) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".cline", "mcp.json"), []byte(`{"mcpServers":{"shared":{"command":"project"},"project-only":{"command":"project-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "cline_mcp_settings.json"), []byte(`{"mcpServers":{"legacy-only":{"command":"legacy-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	candidates, err := List(context.Background(), "cline", workDir)
	if err != nil {
		t.Fatalf("List(cline) error = %v", err)
	}
	if got := candidateByName(t, candidates, "shared").Server.Command; got != "project" {
		t.Fatalf("shared command = %q, want project", got)
	}
	for _, name := range []string{"global-only", "legacy-only", "project-only"} {
		if !candidateByName(t, candidates, name).Supported {
			t.Fatalf("candidate %q is not supported", name)
		}
	}
}

func TestListClineDiscoversDefaultGlobalScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLINE_DATA_DIR", "")
	globalDir := filepath.Join(home, ".cline", "data", "settings")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", globalDir, err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "cline_mcp_settings.json"), []byte(`{"mcpServers":{"global":{"command":"global"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(global) error = %v", err)
	}

	candidates, err := List(context.Background(), "cline", filepath.Join(home, "workspace"))
	if err != nil {
		t.Fatalf("List(cline) error = %v", err)
	}
	if global := candidateByName(t, candidates, "global"); !global.Enabled || !global.Supported {
		t.Fatalf("global candidate = %#v, want enabled default global entry", global)
	}
}

func TestListKiroPreservesDisabledProjectOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "projects", "app")
	workDir := filepath.Join(projectDir, "nested")
	userDir := filepath.Join(home, ".kiro", "settings")
	projectConfigDir := filepath.Join(projectDir, ".kiro", "settings")
	for _, dir := range []string{userDir, projectConfigDir, workDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(userDir, "mcp.json"), []byte(`{"mcpServers":{"shared":{"command":"global"},"global-only":{"command":"global-only"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(global) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "mcp.json"), []byte(`{"mcpServers":{"shared":{"command":"project","disabled":true},"disabled-only":{"disabled":true}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}

	candidates, err := List(context.Background(), "kiro", workDir)
	if err != nil {
		t.Fatalf("List(kiro) error = %v", err)
	}
	shared := candidateByName(t, candidates, "shared")
	if shared.Enabled || shared.Server.Command != "project" {
		t.Fatalf("shared candidate = %#v, want disabled project override", shared)
	}
	if disabled := candidateByName(t, candidates, "disabled-only"); disabled.Enabled {
		t.Fatalf("disabled-only candidate = %#v, want disabled", disabled)
	}
	if global := candidateByName(t, candidates, "global-only"); !global.Enabled || !global.Supported {
		t.Fatalf("global-only candidate = %#v, want enabled global entry", global)
	}
}

func TestListKiroPreservesPortableOAuthPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".kiro", "settings")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	raw := `{"mcpServers":{
		"portable":{"url":"https://example.com/mcp","oauthScopes":["fallback"],"oauth":{"oauthScopes":["read","write"],"clientMetadataUrl":"https://client.example.com/mcpx.json"}},
		"source-client":{"url":"https://example.com/client","oauth":{"clientId":"kiro-client","clientSecret":"secret","redirectUri":"http://127.0.0.1:8080/callback"}}
	}}`
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	candidates, err := List(context.Background(), "kiro", filepath.Join(home, "workspace"))
	if err != nil {
		t.Fatalf("List(kiro) error = %v", err)
	}
	portable := candidateByName(t, candidates, "portable")
	if got, want := portable.OAuthScopes, []string{"read", "write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("portable OAuth scopes = %#v, want %#v", got, want)
	}
	if portable.OAuthClientMetadataURL != "https://client.example.com/mcpx.json" || portable.OAuthUnsupportedReason != "" {
		t.Fatalf("portable OAuth metadata = %#v", portable)
	}
	sourceClient := candidateByName(t, candidates, "source-client")
	for _, field := range []string{"clientId", "clientSecret", "redirectUri"} {
		if !strings.Contains(sourceClient.OAuthUnsupportedReason, field) {
			t.Fatalf("source-client unsupported reason = %q, missing %q", sourceClient.OAuthUnsupportedReason, field)
		}
	}
}

func TestListRejectsUnknownSourceWithRegisteredNames(t *testing.T) {
	_, err := List(context.Background(), "unknown", "")
	if err == nil || !strings.Contains(err.Error(), "claude, cline, codex, cursor, kiro") {
		t.Fatalf("List(unknown) error = %v", err)
	}
}
