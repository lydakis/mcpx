package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromExpandsEnvValuesAfterParsing(t *testing.T) {
	t.Setenv("API_TOKEN", `abc"def`)

	path := filepath.Join(t.TempDir(), "config.toml")
	const raw = `
[servers.github]
url = "https://example.com/mcp"
headers = { Authorization = "Bearer ${API_TOKEN}" }
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	got := cfg.Servers["github"].Headers["Authorization"]
	want := `Bearer abc"def`
	if got != want {
		t.Fatalf("Authorization header = %q, want %q", got, want)
	}
}

func TestLoadFromExpandsFallbackSourcePaths(t *testing.T) {
	t.Setenv("HOME", "/tmp/mcpx-home")

	path := filepath.Join(t.TempDir(), "config.toml")
	const raw = `
fallback_sources = ["${HOME}/custom/mcp.json"]
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if len(cfg.FallbackSources) != 1 {
		t.Fatalf("fallback_sources len = %d, want 1", len(cfg.FallbackSources))
	}
	want := "/tmp/mcpx-home/custom/mcp.json"
	if cfg.FallbackSources[0] != want {
		t.Fatalf("fallback source = %q, want %q", cfg.FallbackSources[0], want)
	}
}
