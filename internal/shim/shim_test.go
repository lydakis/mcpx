package shim

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDefaultDirPrefersXDGBinHome(t *testing.T) {
	t.Setenv("XDG_BIN_HOME", "/tmp/bin-home")
	t.Setenv("HOME", "/tmp/home")

	if got, want := DefaultDir(), filepath.Join("/tmp/bin-home"); got != want {
		t.Fatalf("DefaultDir() = %q, want %q", got, want)
	}
}

func TestDefaultDirFallsBackToHomeLocalBin(t *testing.T) {
	t.Setenv("XDG_BIN_HOME", "")
	t.Setenv("HOME", "/tmp/home")

	if got, want := DefaultDir(), filepath.Join("/tmp/home", ".local", "bin"); got != want {
		t.Fatalf("DefaultDir() = %q, want %q", got, want)
	}
}

func TestInstallCreatesExecutableShim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	result, err := Install("github", InstallOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.AlreadyInstalled {
		t.Fatal("AlreadyInstalled = true, want false")
	}
	if result.Path == "" {
		t.Fatal("Path = empty, want shim path")
	}

	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", result.Path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("mode = %v, want executable", info.Mode())
	}

	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", result.Path, err)
	}
	want := "#!/bin/sh\n# mcpx-shim:server=github\nexec mcpx 'github' \"$@\"\n"
	if string(content) != want {
		t.Fatalf("shim content = %q, want %q", string(content), want)
	}
}

func TestInstallIsIdempotentForManagedShim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	first, err := Install("github", InstallOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	second, err := Install("github", InstallOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if !second.AlreadyInstalled {
		t.Fatal("AlreadyInstalled = false, want true")
	}
	if second.Path != first.Path {
		t.Fatalf("Path = %q, want %q", second.Path, first.Path)
	}
}

func TestInstallRejectsTargetPathCollision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	target := filepath.Join(tmp, "github")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho not-managed\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}

	_, err := Install("github", InstallOptions{Dir: tmp})
	if err == nil {
		t.Fatal("Install() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrPathOccupied) {
		t.Fatalf("Install() error = %v, want ErrPathOccupied", err)
	}
}

func TestInstallRejectsCommandCollisionInPATH(t *testing.T) {
	tmp := t.TempDir()
	existingDir := filepath.Join(tmp, "existing")
	shimDir := filepath.Join(tmp, "shims")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(existingDir) error = %v", err)
	}

	existingCmd := filepath.Join(existingDir, "github")
	if err := os.WriteFile(existingCmd, []byte("#!/bin/sh\necho existing\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(existing command) error = %v", err)
	}
	t.Setenv("PATH", existingDir)

	_, err := Install("github", InstallOptions{Dir: shimDir})
	if err == nil {
		t.Fatal("Install() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrCommandCollision) {
		t.Fatalf("Install() error = %v, want ErrCommandCollision", err)
	}
}

func TestInstallRejectsInvalidServerName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	_, err := Install("../../oops", InstallOptions{Dir: tmp})
	if err == nil {
		t.Fatal("Install() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrInvalidServerName) {
		t.Fatalf("Install() error = %v, want ErrInvalidServerName", err)
	}
}

func TestRemoveDeletesManagedShim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	result, err := Install("github", InstallOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	removed, err := Remove("github", RemoveOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if removed.Path != result.Path {
		t.Fatalf("removed path = %q, want %q", removed.Path, result.Path)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) err = %v, want not-exist", result.Path, err)
	}
}

func TestRemoveRejectsNonManagedFile(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "github")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho plain\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}

	_, err := Remove("github", RemoveOptions{Dir: tmp})
	if err == nil {
		t.Fatal("Remove() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrNotManagedShim) {
		t.Fatalf("Remove() error = %v, want ErrNotManagedShim", err)
	}
}

func TestRemoveReturnsNotInstalledWhenMissing(t *testing.T) {
	tmp := t.TempDir()

	_, err := Remove("github", RemoveOptions{Dir: tmp})
	if err == nil {
		t.Fatal("Remove() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Remove() error = %v, want ErrNotInstalled", err)
	}
}

func TestListReturnsManagedShimsSorted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if _, err := Install("zeta", InstallOptions{Dir: tmp}); err != nil {
		t.Fatalf("Install(zeta) error = %v", err)
	}
	if _, err := Install("alpha", InstallOptions{Dir: tmp}); err != nil {
		t.Fatalf("Install(alpha) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "not-a-shim"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile(non-shim) error = %v", err)
	}

	entries, err := List(ListOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Server)
	}
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listed servers = %v, want %v", got, want)
	}
}

func TestListSkipsLongLineNonManagedFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if _, err := Install("github", InstallOptions{Dir: tmp}); err != nil {
		t.Fatalf("Install(github) error = %v", err)
	}

	longLine := make([]byte, 70*1024)
	for i := range longLine {
		longLine[i] = 'A'
	}
	if err := os.WriteFile(filepath.Join(tmp, "binary"), longLine, 0o755); err != nil {
		t.Fatalf("WriteFile(long-line non-shim) error = %v", err)
	}

	entries, err := List(ListOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Server)
	}
	want := []string{"github"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listed servers = %v, want %v", got, want)
	}
}

func TestListMissingDirReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing")

	entries, err := List(ListOptions{Dir: missing})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestInstallScriptUsesPOSIXShebangOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim scripts are Unix-focused")
	}
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	result, err := Install("github", InstallOptions{Dir: tmp})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", result.Path, err)
	}
	if got := string(content); len(got) < len("#!/bin/sh\n") || got[:len("#!/bin/sh\n")] != "#!/bin/sh\n" {
		t.Fatalf("script header = %q, want #!/bin/sh", got)
	}
}
