package ipc

import (
	"encoding/json"
	"time"

	"github.com/lydakis/mcpx/internal/config"
)

// Request is sent from the CLI to the daemon over the Unix socket.
type Request struct {
	Nonce   string          `json:"nonce"`            // daemon nonce for auth
	Type    string          `json:"type"`             // "ping", "list_servers", "list_tools", "call_tool", "tool_schema", "shutdown"
	CWD     string          `json:"cwd,omitempty"`    // caller working directory
	Server  string          `json:"server,omitempty"` // target server name
	Tool    string          `json:"tool,omitempty"`   // target tool name
	Args    json.RawMessage `json:"args,omitempty"`   // tool arguments
	Cache   *time.Duration  `json:"cache,omitempty"`  // cache TTL override
	Verbose bool            `json:"verbose,omitempty"`
	// IncludeHidden asks daemon responses (currently list_servers) to include
	// otherwise hidden runtime-only servers.
	IncludeHidden bool             `json:"include_hidden,omitempty"`
	Ephemeral     *EphemeralServer `json:"ephemeral,omitempty"`
}

// EphemeralServer carries a transient server definition to be registered by
// the daemon for the current runtime lifetime only.
type EphemeralServer struct {
	Server config.ServerConfig `json:"server"`
}

// Response is sent from the daemon back to the CLI.
type Response struct {
	Content   []byte `json:"content"`              // raw output for stdout
	ExitCode  int    `json:"exit_code"`            // 0=ok, 1=tool error, 2=usage error, 3=internal error
	Stderr    string `json:"stderr,omitempty"`     // error message for stderr
	ErrorCode string `json:"error_code,omitempty"` // stable machine-readable error classification
}

const (
	ErrorCodeUnknownServer = "unknown_server"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitToolErr  = 1
	ExitUsageErr = 2
	ExitInternal = 3
)
