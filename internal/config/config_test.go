package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lydakis/mcpx/internal/paths"
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

func TestLoadWrapperReadsFromDefaultPath(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())

	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile()), 0o700); err != nil {
		t.Fatalf("MkdirAll(config dir): %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte("[servers.github]\nurl = \"https://example.com\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := cfg.Servers["github"]; !ok {
		t.Fatalf("Load() missing expected server, got %#v", cfg.Servers)
	}
}

func TestLoadForEditPreservesEnvPlaceholders(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_TOKEN", "expanded-value")

	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile()), 0o700); err != nil {
		t.Fatalf("MkdirAll(config dir): %v", err)
	}
	raw := `[servers.github]
headers = { Authorization = "Bearer ${API_TOKEN}" }
`
	if err := os.WriteFile(paths.ConfigFile(), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	cfg, err := LoadForEdit()
	if err != nil {
		t.Fatalf("LoadForEdit() error = %v", err)
	}
	if got := cfg.Servers["github"].Headers["Authorization"]; got != "Bearer ${API_TOKEN}" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer ${API_TOKEN}")
	}
}

func TestExpandServerForCurrentEnvExpandsFields(t *testing.T) {
	t.Setenv("TOKEN", "abc123")
	t.Setenv("HOST", "example.com")

	in := ServerConfig{
		Command:       "${HOST}",
		Args:          []string{"--token", "${TOKEN}"},
		CWD:           "/plugins/${HOST}",
		Env:           map[string]string{"AUTH": "${TOKEN}"},
		Headers:       map[string]string{"Authorization": "Bearer ${TOKEN}"},
		URL:           "https://${HOST}/mcp",
		ImportSource:  "codex",
		ImportName:    "remote-host",
		ImportContext: "/work/project",
	}

	out := ExpandServerForCurrentEnv(in)
	if out.Command != "example.com" {
		t.Fatalf("expanded command = %q, want %q", out.Command, "example.com")
	}
	if out.URL != "https://example.com/mcp" {
		t.Fatalf("expanded url = %q, want %q", out.URL, "https://example.com/mcp")
	}
	if out.Args[1] != "abc123" {
		t.Fatalf("expanded args = %#v, want token expanded", out.Args)
	}
	if out.CWD != "/plugins/example.com" {
		t.Fatalf("expanded cwd = %q, want environment expanded", out.CWD)
	}
	if out.ImportSource != "codex" || out.ImportName != "remote-host" || out.ImportContext != "/work/project" {
		t.Fatalf("expanded import metadata = (%q, %q, %q)", out.ImportSource, out.ImportName, out.ImportContext)
	}
	if out.Env["AUTH"] != "abc123" {
		t.Fatalf("expanded env = %#v, want AUTH expanded", out.Env)
	}
	if out.Headers["Authorization"] != "Bearer abc123" {
		t.Fatalf("expanded headers = %#v, want Authorization expanded", out.Headers)
	}
}

func TestExpandServerForCurrentEnvSupportsDefaultValueExpressions(t *testing.T) {
	t.Setenv("MCPX_DEFAULT_SET", "configured")
	t.Setenv("MCPX_DEFAULT_EMPTY", "")
	previousUnset, hadPreviousUnset := os.LookupEnv("MCPX_DEFAULT_UNSET")
	if err := os.Unsetenv("MCPX_DEFAULT_UNSET"); err != nil {
		t.Fatalf("Unsetenv() error = %v", err)
	}
	t.Cleanup(func() {
		if hadPreviousUnset {
			_ = os.Setenv("MCPX_DEFAULT_UNSET", previousUnset)
			return
		}
		_ = os.Unsetenv("MCPX_DEFAULT_UNSET")
	})

	out := ExpandServerForCurrentEnv(ServerConfig{
		Command: "${MCPX_DEFAULT_SET:-fallback}",
		Args: []string{
			"${MCPX_DEFAULT_UNSET:-https://example.com/mcp}",
			"${MCPX_DEFAULT_EMPTY:-fallback}",
			"${MCPX_DEFAULT_UNSET}",
		},
	})
	if out.Command != "configured" {
		t.Fatalf("command = %q, want configured", out.Command)
	}
	want := []string{"https://example.com/mcp", "fallback", "${MCPX_DEFAULT_UNSET}"}
	if !reflect.DeepEqual(out.Args, want) {
		t.Fatalf("args = %#v, want %#v", out.Args, want)
	}
}

func TestExampleConfigPathMatchesPathsConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if got, want := ExampleConfigPath(), paths.ConfigFile(); got != want {
		t.Fatalf("ExampleConfigPath() = %q, want %q", got, want)
	}
}
