package paths

import (
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
