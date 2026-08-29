package ipc

import (
	"encoding/json"
	"time"

	"github.com/lydakis/mcpx/internal/config"
)

// Request is sent from the CLI to the daemon over the Unix socket.
type Request struct {
	Nonce  string `json:"nonce"`            // daemon nonce for auth
	Type   string `json:"type"`             // "ping", "list_servers", "list_tools", "call_tool", "tool_schema", "diagnose_server", "prepare_credential_transition", "reload_server", "shutdown"
	CWD    string `json:"cwd,omitempty"`    // caller working directory
	Server string `json:"server,omitempty"` // target server name
	// Transition identifies a fail-closed OAuth credential lifecycle operation.
	Transition string          `json:"transition,omitempty"`
	Tool       string          `json:"tool,omitempty"` // target tool name
	Args       json.RawMessage `json:"args,omitempty"` // tool arguments
	// ArgsFromJSON distinguishes lossless positional/stdin JSON from shell
	// flags, which cannot represent every valid JSON Schema composition.
	ArgsFromJSON bool `json:"args_from_json,omitempty"`
	// InputResponses and RequestState resume a 2026-07-28 multi-round-trip
	// request. RequestState is opaque and must be echoed without interpretation.
	InputResponses json.RawMessage `json:"input_responses,omitempty"`
	RequestState   string          `json:"request_state,omitempty"`
	Cache          *time.Duration  `json:"cache,omitempty"` // cache TTL override
	Verbose        bool            `json:"verbose,omitempty"`
	// IncludeSchemas asks list_tools for a deterministic full catalog rather
	// than the compact discovery view.
	IncludeSchemas bool `json:"include_schemas,omitempty"`
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
	ErrorCodeInputRequired = "input_required"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitToolErr  = 1
	ExitUsageErr = 2
	ExitInternal = 3
)
