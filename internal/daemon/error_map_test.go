package daemon

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestClassifyCallToolErrorUsageInvalidParams(t *testing.T) {
	if got := classifyCallToolError(mcp.ErrInvalidParams); got != ipc.ExitUsageErr {
		t.Fatalf("classifyCallToolError(invalid params) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestClassifyCallToolErrorUsageMethodNotFound(t *testing.T) {
	err := fmt.Errorf("rpc failed: %w", mcp.ErrMethodNotFound)
	if got := classifyCallToolError(err); got != ipc.ExitUsageErr {
		t.Fatalf("classifyCallToolError(method not found) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestClassifyCallToolErrorUsageFromErrorCodeText(t *testing.T) {
	err := errors.New("json-rpc error -32602: invalid params")
	if got := classifyCallToolError(err); got != ipc.ExitUsageErr {
		t.Fatalf("classifyCallToolError(-32602 text) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestClassifyCallToolErrorUsageLocalToolNotFound(t *testing.T) {
	err := errors.New("tool search not found on server github")
	if got := classifyCallToolError(err); got != ipc.ExitUsageErr {
		t.Fatalf("classifyCallToolError(local tool not found) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestClassifyCallToolErrorTransportDefault(t *testing.T) {
	err := errors.New("dial unix /tmp/mcpx.sock: connect: no such file or directory")
	if got := classifyCallToolError(err); got != ipc.ExitInternal {
		t.Fatalf("classifyCallToolError(transport) = %d, want %d", got, ipc.ExitInternal)
	}
}

func TestClassifyCallToolErrorParseErrorRemainsInternal(t *testing.T) {
	if got := classifyCallToolError(mcp.ErrParseError); got != ipc.ExitInternal {
		t.Fatalf("classifyCallToolError(parse error) = %d, want %d", got, ipc.ExitInternal)
	}
}

func TestClassifyCallToolErrorInvalidRequestCodeRemainsInternal(t *testing.T) {
	err := errors.New("json-rpc error -32600: invalid request")
	if got := classifyCallToolError(err); got != ipc.ExitInternal {
		t.Fatalf("classifyCallToolError(-32600) = %d, want %d", got, ipc.ExitInternal)
	}
}

func TestClassifyToolLookupErrorUsageLocalToolNotFound(t *testing.T) {
	err := errors.New("tool read_file not found on server filesystem")
	if got := classifyToolLookupError(err); got != ipc.ExitUsageErr {
		t.Fatalf("classifyToolLookupError(local tool not found) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestClassifyToolLookupErrorInternalDefault(t *testing.T) {
	err := errors.New("listing tools: timeout")
	if got := classifyToolLookupError(err); got != ipc.ExitInternal {
		t.Fatalf("classifyToolLookupError(default) = %d, want %d", got, ipc.ExitInternal)
	}
}
