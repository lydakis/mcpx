//go:build linux || darwin

package ipc

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

func TestPeerUIDMatchesCurrentUserForSelfConnection(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/mcpx-peer-%d.sock", time.Now().UnixNano())
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath) //nolint:errcheck

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	results := make(chan struct {
		ok  bool
		err error
	}, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			results <- struct {
				ok  bool
				err error
			}{ok: false, err: err}
			return
		}
		defer conn.Close()

		ok, err := peerUIDMatchesCurrentUser(conn)
		results <- struct {
			ok  bool
			err error
		}{ok: ok, err: err}
	}()

	client, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	_ = client.Close()

	res := <-results
	if res.err != nil {
		t.Fatalf("peerUIDMatchesCurrentUser() error = %v", res.err)
	}
	if !res.ok {
		t.Fatal("peerUIDMatchesCurrentUser() = false, want true")
	}
}
