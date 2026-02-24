package cli

import (
	"bytes"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
)

func TestHandleRootFlagsVersion(t *testing.T) {
	oldVersion := buildVersion
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		buildVersion = oldVersion
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	buildVersion = "1.2.3"
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	handled, code := handleRootFlags([]string{"--version"})
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out.String() != "mcpx 1.2.3\n" {
		t.Fatalf("output = %q, want %q", out.String(), "mcpx 1.2.3\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestHandleRootFlagsIgnoresNonGlobal(t *testing.T) {
	handled, _ := handleRootFlags([]string{"github"})
	if handled {
		t.Fatal("handled = true, want false")
	}
}

func TestHandleRootFlagsHelp(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	handled, code := handleRootFlags([]string{"--help"})
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if got := out.String(); got == "" {
		t.Fatal("help output is empty")
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx <server> <tool>")) {
		t.Fatalf("help output missing command surface: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx <server> [--describe]")) {
		t.Fatalf("help output missing describe list mode: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx completion <bash|zsh|fish>")) {
		t.Fatalf("help output missing completion command: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx skill install [FLAGS]")) {
		t.Fatalf("help output missing skill command: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestHandleRootFlagsDoesNotTreatCompletionAsGlobal(t *testing.T) {
	handled, _ := handleRootFlags([]string{"completion", "zsh"})
	if handled {
		t.Fatal("handled = true, want false")
	}
}

func TestMaybeHandleCompletionCommandRunsWhenNameUnclaimed(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleCompletionCommand([]string{"completion", "zsh"}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("#compdef mcpx")) {
		t.Fatalf("output missing zsh completion marker: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleCompletionCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"completion": {},
		},
	}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleCompletionCommand([]string{"completion", "zsh"}, cfg, &out, &errOut)
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleInternalCompletionDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"__complete": {},
		},
	}
	handled, code := maybeHandleCompletionCommand([]string{"__complete", "servers"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestResolveBuildVersionHonorsInjectedValue(t *testing.T) {
	got := resolveBuildVersion("1.2.3")
	if got != "1.2.3" {
		t.Fatalf("resolveBuildVersion(injected) = %q, want %q", got, "1.2.3")
	}
}

func TestParseServerListModeArgs(t *testing.T) {
	t.Run("default list mode", func(t *testing.T) {
		handled, describe, code := parseServerListModeArgs(nil, &bytes.Buffer{})
		if !handled || describe || code != 0 {
			t.Fatalf("parseServerListModeArgs(nil) = handled:%v describe:%v code:%d, want true false 0", handled, describe, code)
		}
	})

	t.Run("describe list mode", func(t *testing.T) {
		handled, describe, code := parseServerListModeArgs([]string{"--describe"}, &bytes.Buffer{})
		if !handled || !describe || code != 0 {
			t.Fatalf("parseServerListModeArgs(--describe) = handled:%v describe:%v code:%d, want true true 0", handled, describe, code)
		}
	})

	t.Run("tool invocation args passthrough", func(t *testing.T) {
		handled, describe, code := parseServerListModeArgs([]string{"search-repositories"}, &bytes.Buffer{})
		if handled || describe || code != 0 {
			t.Fatalf("parseServerListModeArgs(tool) = handled:%v describe:%v code:%d, want false false 0", handled, describe, code)
		}
	})

	t.Run("describe mode rejects extra args", func(t *testing.T) {
		var errOut bytes.Buffer
		handled, describe, code := parseServerListModeArgs([]string{"--describe", "extra"}, &errOut)
		if !handled || describe || code == 0 {
			t.Fatalf("parseServerListModeArgs(--describe extra) = handled:%v describe:%v code:%d, want true false non-zero", handled, describe, code)
		}
		if !bytes.Contains(errOut.Bytes(), []byte("usage: mcpx <server> [--describe]")) {
			t.Fatalf("stderr = %q, want describe mode usage", errOut.String())
		}
	})
}

func TestWriteListedTools(t *testing.T) {
	content := []byte("search-repositories\tSearch repos\nlist-issues\tList issues\n")

	t.Run("names only by default", func(t *testing.T) {
		var out bytes.Buffer
		writeListedTools(content, false, &out)
		if got, want := out.String(), "list-issues\nsearch-repositories\n"; got != want {
			t.Fatalf("writeListedTools(names-only) = %q, want %q", got, want)
		}
	})

	t.Run("include descriptions with describe flag", func(t *testing.T) {
		var out bytes.Buffer
		writeListedTools(content, true, &out)
		if got, want := out.String(), string(content); got != want {
			t.Fatalf("writeListedTools(describe) = %q, want %q", got, want)
		}
	})
}
