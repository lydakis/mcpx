package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Handler processes an IPC request and returns a response.
type Handler func(ctx context.Context, req *Request) *Response

var peerUIDMatchesCurrentUserFn = peerUIDMatchesCurrentUser

// Server listens for IPC connections on a Unix socket.
type Server struct {
	socketPath string
	nonce      string
	handler    Handler
	listener   net.Listener
	wg         sync.WaitGroup
}

// NewServer creates a new IPC server.
func NewServer(socketPath, nonce string, handler Handler) *Server {
	return &Server{
		socketPath: socketPath,
		nonce:      nonce,
		handler:    handler,
	}
}

// Start begins listening for connections. It removes any stale socket file first.
func (s *Server) Start() error {
	// Remove stale socket
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		ln.Close()
		os.Remove(s.socketPath)
		return fmt.Errorf("setting socket permissions: %w", err)
	}
	s.listener = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop()
	}()
	return nil
}

// Stop closes the listener and waits for in-flight connections.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	os.Remove(s.socketPath)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handleConn(conn)
		}()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	ok, err := peerUIDMatchesCurrentUserFn(conn)
	if err != nil {
		writeResponse(conn, &Response{ExitCode: ExitInternal, Stderr: "peer uid check failed"})
		return
	}
	if !ok {
		writeResponse(conn, &Response{ExitCode: ExitInternal, Stderr: "peer uid mismatch"})
		return
	}

	var req Request
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		writeResponse(conn, &Response{ExitCode: ExitInternal, Stderr: "invalid request"})
		return
	}

	if req.Nonce != s.nonce {
		writeResponse(conn, &Response{ExitCode: ExitInternal, Stderr: "nonce mismatch"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [1]byte
		if _, err := conn.Read(buf[:]); err != nil {
			cancel()
			return
		}
		cancel()
	}()

	resp := s.handler(ctx, &req)
	_ = conn.SetReadDeadline(time.Now())
	<-done
	_ = conn.SetReadDeadline(time.Time{})
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp *Response) {
	enc := json.NewEncoder(conn)
	enc.Encode(resp) //nolint: errcheck
}
