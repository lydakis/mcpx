package ipc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleConnCancelsContextWhenClientDisconnects(t *testing.T) {
	restorePeer := peerUIDMatchesCurrentUserFn
	peerUIDMatchesCurrentUserFn = func(conn net.Conn) (bool, error) { return true, nil }
	defer func() {
		peerUIDMatchesCurrentUserFn = restorePeer
	}()

	started := make(chan struct{})
	canceled := make(chan struct{})

	s := &Server{
		nonce: "secret",
		handler: func(ctx context.Context, req *Request) *Response {
			close(started)
			<-ctx.Done()
			close(canceled)
			return &Response{ExitCode: ExitOK}
		},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	go s.handleConn(serverConn)

	if err := json.NewEncoder(clientConn).Encode(&Request{
		Nonce: "secret",
		Type:  "call_tool",
	}); err != nil {
		t.Fatalf("encoding request: %v", err)
	}

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not start")
	}

	if err := clientConn.Close(); err != nil {
		t.Fatalf("closing client conn: %v", err)
	}

	select {
	case <-canceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler context was not canceled after client disconnect")
	}
}

func TestStartSetsSocketMode0600(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "mcpx.sock")
	s := NewServer(socketPath, "secret", func(ctx context.Context, req *Request) *Response {
		return &Response{ExitCode: ExitOK}
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want %o", got, 0o600)
	}
}

func TestHandleConnRejectsPeerUIDMismatch(t *testing.T) {
	restorePeer := peerUIDMatchesCurrentUserFn
	peerUIDMatchesCurrentUserFn = func(conn net.Conn) (bool, error) { return false, nil }
	defer func() {
		peerUIDMatchesCurrentUserFn = restorePeer
	}()

	s := &Server{
		nonce: "secret",
		handler: func(ctx context.Context, req *Request) *Response {
			t.Fatal("handler should not be called on peer uid mismatch")
			return &Response{ExitCode: ExitOK}
		},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleConn(serverConn)
	}()

	var resp Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.ExitCode != ExitInternal {
		t.Fatalf("exit code = %d, want %d", resp.ExitCode, ExitInternal)
	}
	if resp.Stderr != "peer uid mismatch" {
		t.Fatalf("stderr = %q, want %q", resp.Stderr, "peer uid mismatch")
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handleConn did not return")
	}
}
