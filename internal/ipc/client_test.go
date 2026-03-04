package ipc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/paths"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "mcpxipc-")
	if err != nil {
		t.Fatalf("MkdirTemp(socket root): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return filepath.Join(root, "daemon.sock")
}

func TestSocketPathReexportsPathsSocketPath(t *testing.T) {
	runtimeRoot, err := os.MkdirTemp("/tmp", "mcpxrt-")
	if err != nil {
		t.Fatalf("MkdirTemp(runtime): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeRoot)
	if got, want := SocketPath(), paths.SocketPath(); got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
}

func TestClientSendRoundTrip(t *testing.T) {
	socketPath := shortSocketPath(t)
	reqCh := make(chan *Request, 1)

	srv := NewServer(socketPath, "secret", func(_ context.Context, req *Request) *Response {
		reqCh <- req
		return &Response{
			Content:  []byte("pong\n"),
			ExitCode: ExitOK,
			Stderr:   "warning: cached",
		}
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer srv.Stop()

	client := NewClient(socketPath, "secret")
	req := &Request{Type: "ping"}
	resp, err := client.Send(req)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if req.Nonce != "secret" {
		t.Fatalf("request nonce = %q, want %q", req.Nonce, "secret")
	}
	if string(resp.Content) != "pong\n" {
		t.Fatalf("response content = %q, want %q", string(resp.Content), "pong\\n")
	}
	if resp.ExitCode != ExitOK {
		t.Fatalf("response exit code = %d, want %d", resp.ExitCode, ExitOK)
	}
	if resp.Stderr != "warning: cached" {
		t.Fatalf("response stderr = %q, want %q", resp.Stderr, "warning: cached")
	}

	select {
	case seen := <-reqCh:
		if seen == nil {
			t.Fatal("handler request = nil")
		}
		if seen.Type != "ping" {
			t.Fatalf("handler request type = %q, want %q", seen.Type, "ping")
		}
		if seen.Nonce != "secret" {
			t.Fatalf("handler request nonce = %q, want %q", seen.Nonce, "secret")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not receive request")
	}
}

func TestClientSendReturnsDialError(t *testing.T) {
	client := NewClient(shortSocketPath(t)+"-missing", "secret")
	_, err := client.Send(&Request{Type: "ping"})
	if err == nil {
		t.Fatal("Send() error = nil, want dial error")
	}
	if got := err.Error(); !strings.Contains(got, "connecting to daemon") {
		t.Fatalf("Send() error = %q, want dial context", got)
	}
}

func TestClientSendReturnsEncodeError(t *testing.T) {
	socketPath := shortSocketPath(t)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()

	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		close(accepted)
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	client := NewClient(socketPath, "secret")
	_, err = client.Send(&Request{
		Type: "call_tool",
		Args: json.RawMessage("{"),
	})
	if err == nil {
		t.Fatal("Send() error = nil, want encode error")
	}
	if got := err.Error(); !strings.Contains(got, "sending request") {
		t.Fatalf("Send() error = %q, want encode context", got)
	}

	select {
	case <-accepted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("listener did not accept client connection")
	}
}

func TestClientSendReturnsDecodeError(t *testing.T) {
	socketPath := shortSocketPath(t)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		var req Request
		_ = json.NewDecoder(conn).Decode(&req)
		_, _ = conn.Write([]byte("not-json\n"))
	}()

	client := NewClient(socketPath, "secret")
	_, err = client.Send(&Request{Type: "ping"})
	if err == nil {
		t.Fatal("Send() error = nil, want decode error")
	}
	if got := err.Error(); !strings.Contains(got, "reading response") {
		t.Fatalf("Send() error = %q, want decode context", got)
	}
}
