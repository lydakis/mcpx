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

func TestDefaultFallbackSourcePathsIncludeClaudeCodeAndKiro(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	var want []string
	switch runtime.GOOS {
	case "darwin", "linux":
		want = []string{
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".kiro", "settings", "mcp.json"),
		}
	default:
		t.Skip("claude/kiro fallback paths not defined for this platform")
	}

	for _, expected := range want {
		found := false
		for _, p := range paths {
			if p == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fallback paths = %#v, want %q", paths, expected)
		}
	}
}

func TestLoadMCPServersFileReadsClaudeCodeProjectServers(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	projectSubdir := filepath.Join(projectRoot, "subdir")
	if err := os.MkdirAll(projectSubdir, 0700); err != nil {
		t.Fatalf("mkdir project subdir: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(projectSubdir); err != nil {
		t.Fatalf("chdir project subdir: %v", err)
	}

	docPath := filepath.Join(root, ".claude.json")
	raw := []byte(`{
		"mcpServers": {
			"user-server": {"command":"user-command"},
			"shared": {"command":"user-shared"}
		},
		"projects": {
			"` + projectRoot + `": {
				"mcpServers": {
					"local-server": {"command":"local-command"},
					"shared": {"command":"local-shared"}
				}
			},
			"` + filepath.Join(root, "other-project") + `": {
				"mcpServers": {
					"other": {"command":"other-command"}
				}
			}
		}
	}`)
	if err := os.WriteFile(docPath, raw, 0600); err != nil {
		t.Fatalf("write claude doc: %v", err)
	}

	servers, err := loadMCPServersFile(docPath)
	if err != nil {
		t.Fatalf("loadMCPServersFile() error = %v", err)
	}

	if _, ok := servers["local-server"]; !ok {
		t.Fatalf("servers = %#v, want local-server", servers)
	}
	if got := servers["shared"].Command; got != "local-shared" {
		t.Fatalf("shared command = %q, want %q", got, "local-shared")
	}
	if _, ok := servers["user-server"]; !ok {
		t.Fatalf("servers = %#v, want user-server", servers)
	}
	if _, ok := servers["other"]; ok {
		t.Fatalf("servers = %#v, want other project server excluded", servers)
	}
}

func TestNearestUpwardPathFindsNearestParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	grandChild := filepath.Join(child, "grandchild")
	if err := os.MkdirAll(grandChild, 0700); err != nil {
		t.Fatalf("mkdir grandchild: %v", err)
	}

	nearest := filepath.Join(child, ".mcp.json")
	farther := filepath.Join(parent, ".mcp.json")
	for _, path := range []string{nearest, farther} {
		if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(grandChild); err != nil {
		t.Fatalf("chdir grandchild: %v", err)
	}

	if got := nearestUpwardPath(".mcp.json", ""); got != nearest {
		gotResolved, gotErr := filepath.EvalSymlinks(got)
		wantResolved, wantErr := filepath.EvalSymlinks(nearest)
		if gotErr != nil || wantErr != nil || gotResolved != wantResolved {
			t.Fatalf("nearestUpwardPath(.mcp.json) = %q, want %q", got, nearest)
		}
	}
}

func TestMergeFallbackServersForCWDUsesProvidedWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	projectA := filepath.Join(root, "project-a")
	projectB := filepath.Join(root, "project-b")
	projectASubdir := filepath.Join(projectA, "subdir")
	projectBSubdir := filepath.Join(projectB, "subdir")
	for _, dir := range []string{projectASubdir, projectBSubdir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	projectAConfig := filepath.Join(projectA, ".mcp.json")
	projectBConfig := filepath.Join(projectB, ".mcp.json")
	if err := os.WriteFile(projectAConfig, []byte(`{"mcpServers":{"server-a":{"command":"cmd-a"}}}`), 0600); err != nil {
		t.Fatalf("write project-a config: %v", err)
	}
	if err := os.WriteFile(projectBConfig, []byte(`{"mcpServers":{"server-b":{"command":"cmd-b"}}}`), 0600); err != nil {
		t.Fatalf("write project-b config: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(projectASubdir); err != nil {
		t.Fatalf("chdir project-a subdir: %v", err)
	}

	paths := fallbackSourcePathsForCWD(nil, projectBSubdir)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	cfg := &Config{Servers: map[string]ServerConfig{}}
	if err := MergeFallbackServersForCWD(cfg, projectBSubdir); err != nil {
		t.Fatalf("MergeFallbackServersForCWD() error = %v", err)
	}
	if _, ok := cfg.Servers["server-b"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want server-b from provided cwd", cfg.Servers)
	}
	if _, ok := cfg.Servers["server-a"]; ok {
		t.Fatalf("cfg.Servers = %#v, want server-a excluded", cfg.Servers)
	}
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
