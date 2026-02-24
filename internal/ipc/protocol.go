package ipc

import (
	"encoding/json"
	"time"
)

// Request is sent from the CLI to the daemon over the Unix socket.
type Request struct {
	Nonce   string          `json:"nonce"`            // daemon nonce for auth
	Type    string          `json:"type"`             // "list_servers", "list_tools", "call_tool", "tool_schema", "shutdown"
	Server  string          `json:"server,omitempty"` // target server name
	Tool    string          `json:"tool,omitempty"`   // target tool name
	Args    json.RawMessage `json:"args,omitempty"`   // tool arguments
	Cache   *time.Duration  `json:"cache,omitempty"`  // cache TTL override
	Verbose bool            `json:"verbose,omitempty"`
}

// Response is sent from the daemon back to the CLI.
type Response struct {
	Content  []byte `json:"content"`          // raw output for stdout
	ExitCode int    `json:"exit_code"`        // 0=ok, 1=tool error, 2=usage error, 3=internal error
	Stderr   string `json:"stderr,omitempty"` // error message for stderr
}

// Exit codes.
const (
	ExitOK       = 0
	ExitToolErr  = 1
	ExitUsageErr = 2
	ExitInternal = 3
)
