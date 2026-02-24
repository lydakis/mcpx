package ipc

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/lydakis/mcpx/internal/paths"
)

// Client sends requests to the daemon over a Unix socket.
type Client struct {
	socketPath string
	nonce      string
}

// SocketPath returns the daemon socket path (convenience re-export).
func SocketPath() string {
	return paths.SocketPath()
}

// NewClient creates a new IPC client.
func NewClient(socketPath, nonce string) *Client {
	return &Client{socketPath: socketPath, nonce: nonce}
}

// Send sends a request to the daemon and returns the response.
func (c *Client) Send(req *Request) (*Response, error) {
	req.Nonce = c.nonce

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	var resp Response
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return &resp, nil
}
