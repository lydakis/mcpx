package mcpimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPrepareCodexContextPinsExecutableAndWorkingDirectory(t *testing.T) {
	binDir := t.TempDir()
	command := filepath.Join(binDir, "codex")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	raw, err := PrepareContext("codex", "/work/project")
	if err != nil {
		t.Fatalf("PrepareContext(codex) error = %v", err)
	}
	got, err := parseCodexContext(raw)
	if err != nil {
		t.Fatalf("parseCodexContext() error = %v", err)
	}
	if got.CWD != "/work/project" || got.Command != command || got.Path != binDir {
		t.Fatalf("Codex source context = %#v", got)
	}
	rawWithDifferentExecution, err := json.Marshal(codexSourceContext{CWD: "/work/project", Command: "/new/codex", Path: "/new/bin"})
	if err != nil {
		t.Fatalf("Marshal(context) error = %v", err)
	}
	if !SameContext("codex", raw, string(rawWithDifferentExecution)) {
		t.Fatal("SameContext(codex) = false for the same workspace")
	}
}

func TestRefreshCodexContextReResolvesExecutableAndPath(t *testing.T) {
	oldBinDir := t.TempDir()
	oldCommand := filepath.Join(oldBinDir, "codex")
	if err := os.WriteFile(oldCommand, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(old codex) error = %v", err)
	}
	t.Setenv("PATH", oldBinDir)
	oldContext, err := PrepareContext("codex", "/work/project")
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
	refreshed, err := RefreshContext("codex", oldContext)
	if err != nil {
		t.Fatalf("RefreshContext(codex) error = %v", err)
	}
	got, err := parseCodexContext(refreshed)
	if err != nil {
		t.Fatalf("parseCodexContext() error = %v", err)
	}
	if got.CWD != "/work/project" || got.Command != newCommand || got.Path != newBinDir {
		t.Fatalf("refreshed Codex source context = %#v", got)
	}
}

func TestRefreshCodexContextKeepsValidSavedExecutionWhenCurrentPathCannotResolveCodex(t *testing.T) {
	binDir := t.TempDir()
	command := filepath.Join(binDir, "codex")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)
	saved, err := PrepareContext("codex", "/work/project")
	if err != nil {
		t.Fatalf("PrepareContext(codex) error = %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	refreshed, err := RefreshContext("codex", saved)
	if err != nil {
		t.Fatalf("RefreshContext(codex) error = %v", err)
	}
	if refreshed != saved {
		t.Fatalf("refreshed context = %q, want saved context %q", refreshed, saved)
	}
}

func TestParseCodexCatalogPreservesResolvedTransportWithoutCredentials(t *testing.T) {
	raw := []byte(`[
		{
			"name":"messages",
			"enabled":true,
			"auth_status":"unsupported",
			"transport":{
				"type":"stdio",
				"command":"/plugins/messages/launcher",
				"args":["--server","messages"],
				"cwd":"/plugins/messages",
				"env":{"STATIC":"value"},
				"env_vars":["MESSAGE_TOKEN","STATIC"]
			}
		},
		{
			"name":"vercel",
			"enabled":true,
			"auth_status":"unknown",
			"transport":{
				"type":"streamable_http",
				"url":"https://mcp.vercel.com",
				"http_headers":{"X-Client":"mcpx"},
				"env_http_headers":{"X-Trace":"TRACE_ID"},
				"bearer_token_env_var":"VERCEL_TOKEN"
			}
		},
		{
			"name":"disabled",
			"enabled":false,
			"disabled_reason":"disabled by plugin",
			"transport":{"type":"stdio","command":"false"}
		},
		{
			"name":"legacy",
			"enabled":true,
			"transport":{"type":"sse","url":"https://example.com/sse"}
		}
	]`)

	candidates, err := ParseCodexCatalog(raw)
	if err != nil {
		t.Fatalf("ParseCodexCatalog() error = %v", err)
	}
	if got, want := candidateNames(candidates), []string{"disabled", "legacy", "messages", "vercel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate names = %#v, want %#v", got, want)
	}

	messages := candidateByName(t, candidates, "messages")
	if !messages.Supported || messages.Transport != "stdio" {
		t.Fatalf("messages candidate = %#v, want supported stdio", messages)
	}
	if messages.Server.Command != "/plugins/messages/launcher" || messages.Server.CWD != "/plugins/messages" {
		t.Fatalf("messages transport = %#v, want resolved command and cwd", messages.Server)
	}
	if _, exists := messages.Server.Env["MESSAGE_TOKEN"]; exists {
		t.Fatalf("MESSAGE_TOKEN unexpectedly persisted from pass-through env_vars: %#v", messages.Server.Env)
	}
	if got := messages.Server.Env["STATIC"]; got != "value" {
		t.Fatalf("STATIC = %q, want explicit value preserved", got)
	}

	vercel := candidateByName(t, candidates, "vercel")
	if !vercel.Supported || vercel.Transport != "streamable_http" {
		t.Fatalf("vercel candidate = %#v, want supported streamable_http", vercel)
	}
	if vercel.Server.OAuth {
		t.Fatal("vercel OAuth = true, want credentials and auth ownership excluded from import")
	}
	if got := vercel.Server.Headers["Authorization"]; got != "Bearer ${VERCEL_TOKEN}" {
		t.Fatalf("Authorization = %q, want bearer env placeholder", got)
	}
	if got := vercel.Server.Headers["X-Trace"]; got != "${TRACE_ID}" {
		t.Fatalf("X-Trace = %q, want env placeholder", got)
	}

	disabled := candidateByName(t, candidates, "disabled")
	if disabled.Enabled || disabled.DisabledReason != "disabled by plugin" {
		t.Fatalf("disabled candidate = %#v", disabled)
	}
	legacy := candidateByName(t, candidates, "legacy")
	if legacy.Supported || !strings.Contains(legacy.UnsupportedReason, "sse") {
		t.Fatalf("legacy candidate = %#v, want unsupported SSE reason", legacy)
	}
}

func TestListCodexCatalogReportsCommandFailure(t *testing.T) {
	original := runCodexMCPList
	t.Cleanup(func() { runCodexMCPList = original })
	runCodexMCPList = func(_ context.Context, rawContext string) ([]byte, []byte, error) {
		sourceContext, err := parseCodexContext(rawContext)
		if err != nil {
			t.Fatalf("parseCodexContext() error = %v", err)
		}
		if sourceContext.CWD != "/work/project" {
			t.Fatalf("source context cwd = %q, want /work/project", sourceContext.CWD)
		}
		return nil, []byte("codex failed safely"), errors.New("exit status 1")
	}

	rawContext, err := json.Marshal(codexSourceContext{CWD: "/work/project", Command: "/usr/bin/codex", Path: "/usr/bin"})
	if err != nil {
		t.Fatalf("Marshal(context) error = %v", err)
	}
	_, err = ListCodex(context.Background(), string(rawContext))
	if err == nil || !strings.Contains(err.Error(), "codex failed safely") {
		t.Fatalf("ListCodex() error = %v, want actionable stderr", err)
	}
}

func candidateNames(candidates []Candidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	return names
}

func candidateByName(t *testing.T, candidates []Candidate, name string) Candidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("candidate %q missing from %#v", name, candidates)
	return Candidate{}
}
