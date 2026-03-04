package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainVersionPathExitsZero(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess", "--", "--version")
	cmd.Env = append(os.Environ(), "GO_WANT_MAIN_HELPER=1")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version helper process error = %v (output=%q)", err, string(out))
	}
	if got := string(out); !strings.Contains(got, "mcpx ") {
		t.Fatalf("version output = %q, want mcpx version string", got)
	}
}

func TestMainDaemonPathExitsOneWhenDaemonRunFails(t *testing.T) {
	runtimeRoot := t.TempDir()
	runtimeFile := filepath.Join(runtimeRoot, "runtime-file")
	if err := os.WriteFile(runtimeFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(runtime file): %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess", "--", "__daemon")
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER=1",
		"XDG_RUNTIME_DIR="+runtimeFile, // Forces daemon runtime dir creation failure.
	)

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("daemon helper process error = %v (output=%q), want non-zero exit", err, string(out))
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("daemon helper exit code = %d, want 1 (output=%q)", exitErr.ExitCode(), string(out))
	}
	if got := string(out); !strings.Contains(got, "mcpx daemon:") {
		t.Fatalf("daemon output = %q, want daemon error prefix", got)
	}
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER") != "1" {
		return
	}

	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"mcpx"}, os.Args[i+1:]...)
			break
		}
	}

	main()
	os.Exit(0)
}
