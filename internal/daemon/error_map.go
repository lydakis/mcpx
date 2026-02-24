package daemon

import (
	"errors"
	"strings"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/mark3labs/mcp-go/mcp"
)

func classifyCallToolError(err error) int {
	if err == nil {
		return ipc.ExitOK
	}
	if isLocalToolNotFoundError(err) {
		return ipc.ExitUsageErr
	}

	if errors.Is(err, mcp.ErrInvalidParams) || errors.Is(err, mcp.ErrMethodNotFound) {
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
