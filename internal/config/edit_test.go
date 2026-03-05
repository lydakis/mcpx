package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/paths"
)

func TestLoadForEditFromPreservesEnvPlaceholders(t *testing.T) {
	t.Setenv("API_TOKEN", "secret-value")

	path := filepath.Join(t.TempDir(), "config.toml")
	const raw = `
[servers.github]
url = "https://example.com/mcp"
headers = { Authorization = "Bearer ${API_TOKEN}" }
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadForEditFrom(path)
	if err != nil {
		t.Fatalf("LoadForEditFrom() error = %v", err)
	}

	got := cfg.Servers["github"].Headers["Authorization"]
	want := "Bearer ${API_TOKEN}"
	if got != want {
		t.Fatalf("Authorization header = %q, want %q", got, want)
	}
	origin, ok := cfg.ServerOrigins["github"]
	if !ok {
		t.Fatalf("ServerOrigins[github] missing")
	}
	if origin.Kind != ServerOriginKindMCPXConfig {
		t.Fatalf("ServerOrigins[github].Kind = %q, want %q", origin.Kind, ServerOriginKindMCPXConfig)
	}
	if origin.Path != path {
		t.Fatalf("ServerOrigins[github].Path = %q, want %q", origin.Path, path)
	}
}

func TestSaveToWritesConfigAndCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"github": {
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	}

	if err := SaveTo(path, cfg); err != nil {
		t.Fatalf("SaveTo() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "[servers.github]") {
		t.Fatalf("saved config missing server section: %q", text)
	}
	if !strings.Contains(text, `command = "npx"`) {
		t.Fatalf("saved config missing command: %q", text)
	}
}

func TestValidateForCurrentEnvExpandsWithoutMutatingSource(t *testing.T) {
	t.Setenv("MCP_URL", "https://example.com/mcp")

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"existing": {
				URL: "${MCP_URL}",
			},
		},
	}

	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() error = nil, want non-nil for raw placeholder URL")
	}
	if err := ValidateForCurrentEnv(cfg); err != nil {
		t.Fatalf("ValidateForCurrentEnv() error = %v, want nil", err)
	}
	if cfg.Servers["existing"].URL != "${MCP_URL}" {
		t.Fatalf("source config URL mutated to %q, want placeholder preserved", cfg.Servers["existing"].URL)
	}
}

func TestSaveWritesToDefaultPath(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"github": {URL: "https://example.com/mcp"},
		},
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	savedPath := paths.ConfigFile()
	if _, err := os.Stat(savedPath); err != nil {
		t.Fatalf("Stat(saved config) error = %v", err)
	}

	loaded, err := LoadForEditFrom(savedPath)
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved path) error = %v", err)
	}
	if got := loaded.Servers["github"].URL; got != "https://example.com/mcp" {
		t.Fatalf("saved github URL = %q, want %q", got, "https://example.com/mcp")
	}
}

func TestSaveHandlesNilConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())

	if err := Save(nil); err != nil {
		t.Fatalf("Save(nil) error = %v", err)
	}

	if _, err := os.Stat(paths.ConfigFile()); err != nil {
		t.Fatalf("Stat(default config path) error = %v", err)
	}

	loaded, err := LoadForEdit()
	if err != nil {
		t.Fatalf("LoadForEdit() error after Save(nil) = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadForEdit() = nil after Save(nil), want non-nil")
	}
}
