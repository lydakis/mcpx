package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestMaybeHandleShimCommandRunsWhenNameUnclaimed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "github", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), `Installed shim "github"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleShimCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"shim": {}}}

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "github"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestMaybeHandleShimCommandInstallRejectsUnknownServer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()
	shimKnownServersFn = func() ([]string, error) {
		return []string{"known-server"}, nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "does-not-exist", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitUsageErr {
		t.Fatalf("code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), `unknown server`) {
		t.Fatalf("stderr = %q, want unknown-server guidance", errOut.String())
	}
	shimPath := filepath.Join(tmp, "does-not-exist")
	if _, err := os.Stat(shimPath); !os.IsNotExist(err) {
		t.Fatalf("shim file %q should not exist, stat err=%v", shimPath, err)
	}
}

func TestMaybeHandleShimCommandInstallAllowsDiscoveredVirtualServer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()
	shimKnownServersFn = func() ([]string, error) {
		return []string{"gmail", "linear"}, nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"codex_apps": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "gmail", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Installed shim "gmail"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleShimCommandInstallAllowsConfiguredUtilityName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"add": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "add", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Installed shim "add"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
}

func TestRunShimInstallIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	var out bytes.Buffer
	var errOut bytes.Buffer

	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("first runShimCommand(install) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("second runShimCommand(install) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `already installed`) {
		t.Fatalf("stdout = %q, want already-installed message", out.String())
	}
}

func TestRunShimListPrintsInstalledEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if code := runShimCommand([]string{"install", "zeta", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install zeta code = %d, want %d", code, ipc.ExitOK)
	}
	if code := runShimCommand([]string{"install", "alpha", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install alpha code = %d, want %d", code, ipc.ExitOK)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := runShimCommand([]string{"list", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimCommand(list) = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "zeta") {
		t.Fatalf("stdout = %q, want both entries", out.String())
	}
}

func TestRunShimRemoveDeletesInstalledShim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install code = %d, want %d", code, ipc.ExitOK)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := runShimCommand([]string{"remove", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimCommand(remove) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Removed shim "github"`) {
		t.Fatalf("stdout = %q, want remove confirmation", out.String())
	}
}

func TestRunShimRemoveMissingReturnsUsageError(t *testing.T) {
	tmp := t.TempDir()
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runShimCommand([]string{"remove", "github", "--dir", tmp}, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runShimCommand(remove missing) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "not installed") {
		t.Fatalf("stderr = %q, want not-installed guidance", errOut.String())
	}
}
