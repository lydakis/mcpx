package daemon

import (
	"errors"
	"strings"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func classifyCallToolError(err error) int {
	if err == nil {
		return ipc.ExitOK
	}
	if isLocalToolNotFoundError(err) {
		return ipc.ExitUsageErr
	}

	if errors.Is(err, mcppool.ErrInvalidParams) {
		return ipc.ExitUsageErr
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && (rpcErr.Code == jsonrpc.CodeInvalidParams || rpcErr.Code == jsonrpc.CodeMethodNotFound) {
		return ipc.ExitUsageErr
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "-32602") || strings.Contains(msg, "-32601") {
		return ipc.ExitUsageErr
	}
	if strings.Contains(msg, "invalid params") || strings.Contains(msg, "method not found") {
		return ipc.ExitUsageErr
	}

	return ipc.ExitInternal
}

func classifyToolLookupError(err error) int {
	if err == nil {
		return ipc.ExitOK
	}
	if isLocalToolNotFoundError(err) {
		return ipc.ExitUsageErr
	}
	return ipc.ExitInternal
}

func isLocalToolNotFoundError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.HasPrefix(msg, "tool ") && strings.Contains(msg, " not found on server ")
}
