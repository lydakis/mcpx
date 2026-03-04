package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeDirUsesXDGStateHomeFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/state-home")
	t.Setenv("XDG_CACHE_HOME", "/tmp/cache-home")
	t.Setenv("HOME", "/tmp/home")

	got := RuntimeDir()
	want := filepath.Join("/tmp/state-home", "mcpx")
	if got != want {
		t.Fatalf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDirFallsBackToHomeLocalState(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/tmp/home")

	got := RuntimeDir()
	want := filepath.Join("/tmp/home", ".local", "state", "mcpx")
	if got != want {
		t.Fatalf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDirPrefersXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/xdg-runtime")
	t.Setenv("XDG_STATE_HOME", "/tmp/state-home")

	got := RuntimeDir()
	want := filepath.Join("/tmp/xdg-runtime", "mcpx")
	if got != want {
		t.Fatalf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestManDirUsesXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/data-home")
	t.Setenv("HOME", "/tmp/home")

	got := ManDir()
	want := filepath.Join("/tmp/data-home", "man", "man1")
	if got != want {
		t.Fatalf("ManDir() = %q, want %q", got, want)
	}
}

func TestManDirFallsBackToHomeLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/tmp/home")

	got := ManDir()
	want := filepath.Join("/tmp/home", ".local", "share", "man", "man1")
	if got != want {
		t.Fatalf("ManDir() = %q, want %q", got, want)
	}
}

func TestConfigAndCacheDirUseXDGEnvVars(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/config-home")
	t.Setenv("XDG_CACHE_HOME", "/tmp/cache-home")
	t.Setenv("HOME", "/tmp/home")

	if got, want := ConfigDir(), filepath.Join("/tmp/config-home", "mcpx"); got != want {
		t.Fatalf("ConfigDir() = %q, want %q", got, want)
	}
	if got, want := CacheDir(), filepath.Join("/tmp/cache-home", "mcpx"); got != want {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestConfigDirFallsBackToHomeDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/home")

	if got, want := ConfigDir(), filepath.Join("/tmp/home", ".config", "mcpx"); got != want {
		t.Fatalf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigAndRuntimeDerivedPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/config-home")
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/runtime-home")

	if got, want := ConfigFile(), filepath.Join("/tmp/config-home", "mcpx", "config.toml"); got != want {
		t.Fatalf("ConfigFile() = %q, want %q", got, want)
	}
	if got, want := SocketPath(), filepath.Join("/tmp/runtime-home", "mcpx", "daemon.sock"); got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
	if got, want := StatePath(), filepath.Join("/tmp/runtime-home", "mcpx", "daemon.state"); got != want {
		t.Fatalf("StatePath() = %q, want %q", got, want)
	}
	if got, want := LockPath(), filepath.Join("/tmp/runtime-home", "mcpx", "daemon.lock"); got != want {
		t.Fatalf("LockPath() = %q, want %q", got, want)
	}
}

func TestEnsureDirCreatesNestedDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "mcpx", "runtime")
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("EnsureDir() path %q is not a directory", dir)
	}
}

func TestEnsureDirReturnsErrorWhenParentIsFile(t *testing.T) {
	root := t.TempDir()
	parentFile := filepath.Join(root, "parent-file")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(parent file): %v", err)
	}

	err := EnsureDir(filepath.Join(parentFile, "child"))
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want non-nil when parent is a file")
	}
}
