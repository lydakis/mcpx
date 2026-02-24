package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLoadFallbackServersReadsMCPServersSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}

	doc := map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
				"env": map[string]string{
					"GITHUB_TOKEN": "${GITHUB_TOKEN}",
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	servers, err := LoadFallbackServers()
	if err != nil {
		t.Fatalf("LoadFallbackServers() error = %v", err)
	}

	srv, ok := servers["github"]
	if !ok {
		t.Fatalf("fallback servers = %#v, want github", servers)
	}
	if srv.Command != "npx" {
		t.Fatalf("command = %q, want %q", srv.Command, "npx")
	}
}

func TestMergeFallbackServersUsesFallbackWhenConfigEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}

	raw := []byte(`{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","."]}}}`)
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{Servers: map[string]ServerConfig{}}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if _, ok := cfg.Servers["filesystem"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want filesystem fallback", cfg.Servers)
	}
}

func TestMergeFallbackServersUsesConfiguredSources(t *testing.T) {
	customPath := filepath.Join(t.TempDir(), "custom-mcp.json")
	raw := []byte(`{"mcpServers":{"custom":{"command":"uvx","args":["mcp-custom"]}}}`)
	if err := os.WriteFile(customPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{
		Servers:         map[string]ServerConfig{},
		FallbackSources: []string{customPath},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if _, ok := cfg.Servers["custom"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want custom fallback", cfg.Servers)
	}
}

func TestMergeFallbackServersExplicitEmptySourcesDisablesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}
	raw := []byte(`{"mcpServers":{"default":{"command":"npx","args":["server-default"]}}}`)
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{
		Servers:         map[string]ServerConfig{},
		FallbackSources: []string{},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if len(cfg.Servers) != 0 {
		t.Fatalf("cfg.Servers = %#v, want empty with fallback disabled", cfg.Servers)
	}
}

func TestDefaultFallbackSourcePathsIncludeCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	var want string
	switch runtime.GOOS {
	case "darwin", "linux":
		want = filepath.Join(home, ".cursor", "mcp.json")
	default:
		t.Skip("cursor fallback path not defined for this platform")
	}

	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Fatalf("fallback paths = %#v, want %q", paths, want)
}

func TestFallbackSourcePathsPreserveDeclaredDefaultOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := compactPaths(defaultFallbackSourcePaths())
	if len(want) == 0 {
		t.Skip("no default fallback source paths for this platform")
	}

	got := fallbackSourcePaths(nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback source order = %#v, want %#v", got, want)
	}
}
