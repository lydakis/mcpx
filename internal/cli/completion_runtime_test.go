package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/paths"
)

func setupCompletionRuntimeServer(t *testing.T, handler func(req *ipc.Request) *ipc.Response) {
	t.Helper()

	runtimeRoot, err := os.MkdirTemp("/tmp", "mcpxrt-")
	if err != nil {
		t.Fatalf("MkdirTemp(runtime): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeRoot)
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		t.Fatalf("EnsureDir(runtime): %v", err)
	}

	const nonce = "completion-runtime-nonce"
	if err := os.WriteFile(paths.StatePath(), []byte(nonce+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(state): %v", err)
	}

	srv := ipc.NewServer(paths.SocketPath(), nonce, func(_ context.Context, req *ipc.Request) *ipc.Response {
		if req != nil && req.Type == "ping" {
			return &ipc.Response{ExitCode: ipc.ExitOK}
		}
		return handler(req)
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(srv.Stop)
}

func setupCompletionRuntimePingThenDropServer(t *testing.T) {
	t.Helper()

	runtimeRoot, err := os.MkdirTemp("/tmp", "mcpxrt-")
	if err != nil {
		t.Fatalf("MkdirTemp(runtime): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeRoot)
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		t.Fatalf("EnsureDir(runtime): %v", err)
	}

	const nonce = "completion-runtime-drop-nonce"
	if err := os.WriteFile(paths.StatePath(), []byte(nonce+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(state): %v", err)
	}

	ln, err := net.Listen("unix", paths.SocketPath())
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		pingValidated := false
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			var req ipc.Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				_ = conn.Close()
				// SpawnOrConnect probes socket liveness with a bare connect/close
				// before nonce validation; ignore those.
				continue
			}
			if !pingValidated && req.Type == "ping" {
				_ = json.NewEncoder(conn).Encode(&ipc.Response{ExitCode: ipc.ExitOK})
				_ = conn.Close()
				pingValidated = true
				continue
			}
			_ = conn.Close()
			return
		}
	}()

	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("ping/drop listener did not stop")
		}
	})
}

func TestCompleteServersPrintsSortedServerNames(t *testing.T) {
	reqCh := make(chan *ipc.Request, 1)
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		reqCh <- req
		return &ipc.Response{
			ExitCode: ipc.ExitOK,
			Content:  []byte(`[{"name":"beta"},{"name":"alpha"},{"name":"alpha"}]`),
		}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeServers(&out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("completeServers() code = %d, want %d", code, ipc.ExitOK)
	}
	if got := out.String(); got != "alpha\nbeta\n" {
		t.Fatalf("stdout = %q, want %q", got, "alpha\\nbeta\\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	select {
	case req := <-reqCh:
		if req == nil {
			t.Fatal("request = nil")
		}
		if req.Type != "list_servers" {
			t.Fatalf("request type = %q, want %q", req.Type, "list_servers")
		}
		if req.CWD != callerWorkingDirectory() {
			t.Fatalf("request cwd = %q, want %q", req.CWD, callerWorkingDirectory())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server did not receive list_servers request")
	}
}

func TestCompleteServersReturnsDaemonExitCode(t *testing.T) {
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "bad server list request"}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeServers(&out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("completeServers() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); got != "bad server list request\n" {
		t.Fatalf("stderr = %q, want %q", got, "bad server list request\\n")
	}
}

func TestCompleteServersHandlesSendError(t *testing.T) {
	setupCompletionRuntimePingThenDropServer(t)

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeServers(&out, &errOut)
	if code != ipc.ExitInternal {
		t.Fatalf("completeServers() code = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "mcpx:") {
		t.Fatalf("stderr = %q, want client error output", got)
	}
}

func TestCompleteToolsPrintsSortedToolNames(t *testing.T) {
	reqCh := make(chan *ipc.Request, 1)
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		reqCh <- req
		return &ipc.Response{
			ExitCode: ipc.ExitOK,
			Content:  []byte(`[{"name":"sum"},{"name":"echo"},{"name":"sum"},{"name":" "}]`),
		}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeTools("math", &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("completeTools() code = %d, want %d", code, ipc.ExitOK)
	}
	if got := out.String(); got != "echo\nsum\n" {
		t.Fatalf("stdout = %q, want %q", got, "echo\\nsum\\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	select {
	case req := <-reqCh:
		if req == nil {
			t.Fatal("request = nil")
		}
		if req.Type != "list_tools" {
			t.Fatalf("request type = %q, want %q", req.Type, "list_tools")
		}
		if req.Server != "math" {
			t.Fatalf("request server = %q, want %q", req.Server, "math")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server did not receive list_tools request")
	}
}

func TestCompleteToolsRejectsInvalidPayload(t *testing.T) {
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`{"name":"sum"}`)}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeTools("math", &out, &errOut)
	if code != ipc.ExitInternal {
		t.Fatalf("completeTools() code = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "invalid daemon response for tool list") {
		t.Fatalf("stderr = %q, want invalid payload error", got)
	}
}

func TestCompleteToolsReturnsDaemonExitCodeWhenStderrPresent(t *testing.T) {
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "tool list denied"}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeTools("math", &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("completeTools() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); got != "tool list denied\n" {
		t.Fatalf("stderr = %q, want %q", got, "tool list denied\\n")
	}
}

func TestCompleteToolsHandlesSendError(t *testing.T) {
	setupCompletionRuntimePingThenDropServer(t)

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeTools("math", &out, &errOut)
	if code != ipc.ExitInternal {
		t.Fatalf("completeTools() code = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "mcpx:") {
		t.Fatalf("stderr = %q, want client error output", got)
	}
}

func TestCompleteFlagsPrintsToolFlagCompletions(t *testing.T) {
	reqCh := make(chan *ipc.Request, 1)
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		reqCh <- req
		return &ipc.Response{
			ExitCode: ipc.ExitOK,
			Content: []byte(`{
				"name": "search",
				"input_schema": {
					"type": "object",
					"properties": {
						"dry_run": {"type": "boolean"},
						"query": {"type": "string"}
					}
				}
			}`),
		}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeFlags("math", "search", &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("completeFlags() code = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	joined := "\n" + strings.Join(lines, "\n") + "\n"
	for _, want := range []string{"--dry_run", "--no-dry_run", "--query", "--cache"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Fatalf("completion output missing %q in %q", want, out.String())
		}
	}

	select {
	case req := <-reqCh:
		if req == nil {
			t.Fatal("request = nil")
		}
		if req.Type != "tool_schema" {
			t.Fatalf("request type = %q, want %q", req.Type, "tool_schema")
		}
		if req.Server != "math" || req.Tool != "search" {
			t.Fatalf("request target = (%q, %q), want (%q, %q)", req.Server, req.Tool, "math", "search")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server did not receive tool_schema request")
	}
}

func TestCompleteFlagsReturnsDaemonExitCodeWhenStderrPresent(t *testing.T) {
	setupCompletionRuntimeServer(t, func(req *ipc.Request) *ipc.Response {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "schema denied"}
	})

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeFlags("math", "search", &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("completeFlags() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); got != "schema denied\n" {
		t.Fatalf("stderr = %q, want %q", got, "schema denied\\n")
	}
}

func TestCompleteFlagsHandlesSendError(t *testing.T) {
	setupCompletionRuntimePingThenDropServer(t)

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := completeFlags("math", "search", &out, &errOut)
	if code != ipc.ExitInternal {
		t.Fatalf("completeFlags() code = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "mcpx:") {
		t.Fatalf("stderr = %q, want client error output", got)
	}
}

func TestCompletionClientReturnsInternalErrorWhenRuntimeDirInvalid(t *testing.T) {
	runtimeRoot := t.TempDir()
	runtimeFile := filepath.Join(runtimeRoot, "runtime-file")
	if err := os.WriteFile(runtimeFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatalf("WriteFile(runtime file): %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeFile)

	var errOut bytes.Buffer
	client, code := completionClient(&errOut)
	if client != nil {
		t.Fatal("completionClient() client != nil, want nil")
	}
	if code != ipc.ExitInternal {
		t.Fatalf("completionClient() code = %d, want %d", code, ipc.ExitInternal)
	}
	if got := errOut.String(); !strings.Contains(got, "creating runtime dir") {
		t.Fatalf("stderr = %q, want runtime dir error", got)
	}
}
