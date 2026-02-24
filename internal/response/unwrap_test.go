package response

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestUnwrapPrefersStructuredContent(t *testing.T) {
	result := &mcp.CallToolResult{
		StructuredContent: map[string]any{"count": 3},
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "ignored"},
		},
	}

	out, code := Unwrap(result)
	if code != ipc.ExitOK {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitOK)
	}
	if string(out) != "{\"count\":3}\n" {
		t.Fatalf("Unwrap output = %q, want %q", string(out), "{\"count\":3}\\n")
	}
}

func TestUnwrapMultipleTextBlocksAreNewlineSeparated(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "alpha"},
			mcp.TextContent{Type: "text", Text: "beta"},
		},
	}

	out, _ := Unwrap(result)
	if string(out) != "alpha\nbeta\n" {
		t.Fatalf("Unwrap output = %q, want %q", string(out), "alpha\\nbeta\\n")
	}
}

func TestUnwrapImageContentWritesTempFileAndPrintsPath(t *testing.T) {
	payload := []byte("image-bytes")
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{
				Type:     "image",
				Data:     base64.StdEncoding.EncodeToString(payload),
				MIMEType: "application/octet-stream",
			},
		},
	}

	out, code := Unwrap(result)
	if code != ipc.ExitOK {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitOK)
	}

	path := strings.TrimSpace(string(out))
	if path == "" {
		t.Fatalf("Unwrap output path is empty")
	}
	defer os.Remove(path) //nolint:errcheck

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading emitted file: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("file content = %q, want %q", string(data), string(payload))
	}
}

func TestUnwrapUsesToolErrorExitCode(t *testing.T) {
	result := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "nope"},
		},
	}

	_, code := Unwrap(result)
	if code != ipc.ExitToolErr {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitToolErr)
	}
}
