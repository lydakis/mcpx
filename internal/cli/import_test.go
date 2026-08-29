package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcpimport"
)

func TestImportPreviewIsReadOnlyAndRedacted(t *testing.T) {
	configHome := setupImportTest(t)
	stubImportCandidates(t, "cursor", []mcpimport.Candidate{
		{Name: "disabled", Enabled: false, DisabledReason: "disabled by plugin", Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "false"}},
		{Name: "legacy", Enabled: true, Transport: "sse", UnsupportedReason: `unsupported transport "sse"`},
		{Name: "messages", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "/plugin/launcher", CWD: "/plugin", Env: map[string]string{"TOKEN": "secret"}}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"cursor", "--json"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
	var rows []importPreviewRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("preview JSON = %q: %v", stdout.String(), err)
	}
	if len(rows) != 3 || rows[0].Status != "disabled" || rows[1].Status != "unsupported" || rows[2].Status != "available" {
		t.Fatalf("preview rows = %#v", rows)
	}
	if strings.Contains(stdout.String(), "secret") || strings.Contains(stdout.String(), "/plugin/launcher") {
		t.Fatalf("preview exposed transport details: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(configHome, "mcpx", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote config, stat error = %v", err)
	}
}

func TestImportPreviewTreatsAnotherSourceContextAsConflict(t *testing.T) {
	configHome := setupImportTest(t)
	writeImportConfig(t, configHome, `[servers.messages]
command = "managed"
import_source = "cursor"
import_name = "messages"
import_context = "/another/workspace"
`)
	stubImportCandidates(t, "cursor", []mcpimport.Candidate{
		{Name: "messages", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "replacement"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"cursor", "--json"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
	var rows []importPreviewRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("preview JSON = %q: %v", stdout.String(), err)
	}
	if len(rows) != 1 || rows[0].Status != "conflict" {
		t.Fatalf("preview rows = %#v, want cross-context conflict", rows)
	}
}

func TestImportListsRegisteredSourcesWithoutInvokingAdapters(t *testing.T) {
	setupImportTest(t)
	original := listImportCandidatesFn
	t.Cleanup(func() { listImportCandidatesFn = original })
	listImportCandidatesFn = func(context.Context, string, string) ([]mcpimport.Candidate, error) {
		t.Fatal("source adapter should not run while listing sources")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"--json"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
	for _, source := range []string{"claude", "cline", "codex", "cursor", "kiro"} {
		if !strings.Contains(stdout.String(), `"name":"`+source+`"`) {
			t.Fatalf("sources JSON = %q, missing %q", stdout.String(), source)
		}
	}
}

func TestImportRejectsUnknownSourceAsUsageError(t *testing.T) {
	setupImportTest(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"unknown"}, &stdout, &stderr)
	if code != ipc.ExitUsageErr || !strings.Contains(stderr.String(), "unsupported source") {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
}

func TestMaybeHandleImportCommandDefersToManagedServer(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"import": {Command: "server"}}}
	handled, code := maybeHandleImportCommand([]string{"import"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled || code != 0 {
		t.Fatalf("maybeHandleImportCommand() = (%v, %d), want deferred", handled, code)
	}
}

func TestImportSelectedServersPersistsGenericProvenanceAndOAuthOptIn(t *testing.T) {
	configHome := setupImportTest(t)
	stubImportCandidates(t, "cursor", []mcpimport.Candidate{
		{Name: "messages", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "/plugin/launcher", Args: []string{"messages"}, CWD: "/plugin/messages"}},
		{Name: "vercel", Enabled: true, Transport: "streamable_http", Supported: true, OAuthScopes: []string{"read", "write"}, OAuthClientMetadataURL: "https://client.example.com/mcpx.json", Server: config.ServerConfig{URL: "https://mcp.vercel.com", Headers: map[string]string{"Authorization": "Bearer source-owned"}}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"cursor", "messages", "vercel", "--oauth"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}

	cfg, err := config.LoadForEditFrom(filepath.Join(configHome, "mcpx", "config.toml"))
	if err != nil {
		t.Fatalf("LoadForEditFrom() error = %v", err)
	}
	messages := cfg.Servers["messages"]
	if messages.CWD != "/plugin/messages" || messages.ImportSource != "cursor" || messages.ImportName != "messages" || messages.ImportContext != callerWorkingDirectory() {
		t.Fatalf("messages = %#v, want cwd and generic import provenance", messages)
	}
	if messages.OAuth {
		t.Fatal("stdio server OAuth = true, want false")
	}
	vercel := cfg.Servers["vercel"]
	if !vercel.OAuth || vercel.ImportSource != "cursor" || vercel.ImportName != "vercel" {
		t.Fatalf("vercel = %#v, want explicit OAuth and generic provenance", vercel)
	}
	if vercel.HasAuthorizationHeader() {
		t.Fatalf("vercel headers = %#v, want source Authorization removed for mcpx OAuth ownership", vercel.Headers)
	}
	if got, want := vercel.OAuthScopes, []string{"read", "write"}; !reflect.DeepEqual(got, want) || vercel.OAuthClientMetadataURL != "https://client.example.com/mcpx.json" {
		t.Fatalf("vercel OAuth policy = %#v, want imported scopes and client metadata URL", vercel)
	}
}

func TestImportRejectsSourceOwnedOAuthClientConfiguration(t *testing.T) {
	setupImportTest(t)
	stubImportCandidates(t, "kiro", []mcpimport.Candidate{{
		Name:                   "private",
		Enabled:                true,
		Transport:              "streamable_http",
		Supported:              true,
		OAuthUnsupportedReason: "source OAuth settings require manual configuration: clientId, clientSecret",
		Server:                 config.ServerConfig{URL: "https://example.com/mcp"},
	}})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"kiro", "private", "--oauth"}, &stdout, &stderr)
	if code != ipc.ExitUsageErr || !strings.Contains(stderr.String(), "manual configuration") {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
}

func TestRefreshCodexImportsPersistsReResolvedExecutionContext(t *testing.T) {
	oldBinDir := t.TempDir()
	oldCommand := filepath.Join(oldBinDir, "codex")
	if err := os.WriteFile(oldCommand, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(old codex) error = %v", err)
	}
	t.Setenv("PATH", oldBinDir)
	oldContext, err := mcpimport.PrepareContext("codex", "/source/workspace")
	if err != nil {
		t.Fatalf("PrepareContext(codex) error = %v", err)
	}
	if err := os.Remove(oldCommand); err != nil {
		t.Fatalf("Remove(old codex) error = %v", err)
	}

	newBinDir := t.TempDir()
	newCommand := filepath.Join(newBinDir, "codex")
	if err := os.WriteFile(newCommand, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(new codex) error = %v", err)
	}
	t.Setenv("PATH", newBinDir)
	original := listImportCandidatesFn
	t.Cleanup(func() { listImportCandidatesFn = original })
	listImportCandidatesFn = func(_ context.Context, source, sourceContext string) ([]mcpimport.Candidate, error) {
		if source != "codex" || !strings.Contains(sourceContext, newCommand) || strings.Contains(sourceContext, oldCommand) {
			t.Fatalf("refreshed source context = %q", sourceContext)
		}
		return []mcpimport.Candidate{{Name: "messages", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "messages"}}}, nil
	}
	servers := map[string]config.ServerConfig{
		"messages": {Command: "old", ImportSource: "codex", ImportName: "messages", ImportContext: oldContext},
	}

	if _, err := refreshSourceImports(context.Background(), "codex", servers); err != nil {
		t.Fatalf("refreshSourceImports() error = %v", err)
	}
	if got := servers["messages"].ImportContext; !strings.Contains(got, newCommand) || strings.Contains(got, oldCommand) {
		t.Fatalf("saved source context = %q", got)
	}
}

func TestImportAllSkipsDisabledUnsupportedAndConflicts(t *testing.T) {
	configHome := setupImportTest(t)
	writeImportConfig(t, configHome, `[servers.existing]
command = "managed"
`)
	stubImportCandidates(t, "claude", []mcpimport.Candidate{
		{Name: "available", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "available"}},
		{Name: "disabled", Enabled: false, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "disabled"}},
		{Name: "existing", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "replacement"}},
		{Name: "legacy", Enabled: true, Transport: "sse", UnsupportedReason: "unsupported"},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"claude", "--all"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
	cfg, err := config.LoadForEditFrom(filepath.Join(configHome, "mcpx", "config.toml"))
	if err != nil {
		t.Fatalf("LoadForEditFrom() error = %v", err)
	}
	if got := cfg.Servers["existing"].Command; got != "managed" {
		t.Fatalf("existing command = %q, want managed entry preserved", got)
	}
	if got := cfg.Servers["available"].Command; got != "available" {
		t.Fatalf("available command = %q", got)
	}
	if _, ok := cfg.Servers["disabled"]; ok {
		t.Fatal("disabled server was imported")
	}
	if _, ok := cfg.Servers["legacy"]; ok {
		t.Fatal("unsupported server was imported")
	}
	if !strings.Contains(stdout.String(), "Imported 1") || !strings.Contains(stdout.String(), "skipped 3") {
		t.Fatalf("stdout = %q, want import and skip counts", stdout.String())
	}
}

func TestImportSourceRefreshUpdatesTransportAndPreservesManagedOptions(t *testing.T) {
	configHome := setupImportTest(t)
	writeImportConfig(t, configHome, `[servers.vercel]
url = "https://old.example/mcp"
oauth = true
oauth_scopes = ["read"]
default_cache_ttl = "30s"
import_source = "cursor"
import_name = "vercel"
import_context = "/source/workspace"
`)
	stubImportCandidatesInContext(t, "cursor", "/source/workspace", []mcpimport.Candidate{
		{Name: "vercel", Enabled: true, Transport: "streamable_http", Supported: true, Server: config.ServerConfig{URL: "https://new.example/mcp", Headers: map[string]string{"X-New": "yes", "Authorization": "Bearer source-owned"}}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"cursor", "--refresh"}, &stdout, &stderr)
	if code != ipc.ExitOK || stderr.Len() != 0 {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
	cfg, err := config.LoadForEditFrom(filepath.Join(configHome, "mcpx", "config.toml"))
	if err != nil {
		t.Fatalf("LoadForEditFrom() error = %v", err)
	}
	vercel := cfg.Servers["vercel"]
	if vercel.URL != "https://new.example/mcp" || vercel.Headers["X-New"] != "yes" {
		t.Fatalf("refreshed transport = %#v", vercel)
	}
	if !vercel.OAuth || len(vercel.OAuthScopes) != 1 || vercel.OAuthScopes[0] != "read" || vercel.DefaultCacheTTL != "30s" {
		t.Fatalf("managed options not preserved: %#v", vercel)
	}
	if vercel.HasAuthorizationHeader() {
		t.Fatalf("refreshed headers = %#v, want source Authorization excluded while mcpx OAuth is enabled", vercel.Headers)
	}
}

func TestImportSelectedConflictRequiresOverwrite(t *testing.T) {
	configHome := setupImportTest(t)
	writeImportConfig(t, configHome, "[servers.messages]\ncommand = \"managed\"\n")
	stubImportCandidates(t, "kiro", []mcpimport.Candidate{
		{Name: "messages", Enabled: true, Transport: "stdio", Supported: true, Server: config.ServerConfig{Command: "replacement"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runImportCommand([]string{"kiro", "messages"}, &stdout, &stderr)
	if code != ipc.ExitUsageErr || !strings.Contains(stderr.String(), "--overwrite") {
		t.Fatalf("runImportCommand() = %d, stderr=%q", code, stderr.String())
	}
}

func setupImportTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)
	return configHome
}

func writeImportConfig(t *testing.T, configHome, raw string) {
	t.Helper()
	dir := filepath.Join(configHome, "mcpx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func stubImportCandidates(t *testing.T, wantSource string, candidates []mcpimport.Candidate) {
	t.Helper()
	stubImportCandidatesInContext(t, wantSource, "", candidates)
}

func stubImportCandidatesInContext(t *testing.T, wantSource, wantContext string, candidates []mcpimport.Candidate) {
	t.Helper()
	original := listImportCandidatesFn
	t.Cleanup(func() { listImportCandidatesFn = original })
	listImportCandidatesFn = func(_ context.Context, source, sourceContext string) ([]mcpimport.Candidate, error) {
		if source != wantSource {
			t.Fatalf("source = %q, want %q", source, wantSource)
		}
		if wantContext != "" && sourceContext != wantContext {
			t.Fatalf("source context = %q, want %q", sourceContext, wantContext)
		}
		return candidates, nil
	}
}
