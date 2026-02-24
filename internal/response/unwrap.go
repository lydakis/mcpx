package response

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"os"
	"strings"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/mark3labs/mcp-go/mcp"
)

// Unwrap extracts raw output from an MCP CallToolResult.
// Returns the output bytes and an exit code.
func Unwrap(result *mcp.CallToolResult) ([]byte, int) {
	if result == nil {
		return nil, ipc.ExitInternal
	}

	exitCode := ipc.ExitOK
	if result.IsError {
		exitCode = ipc.ExitToolErr
	}

	if result.StructuredContent != nil {
		if data, err := json.Marshal(result.StructuredContent); err == nil {
			return ensureTrailingNewline(data), exitCode
		}
	}

	var parts []string
	for _, content := range result.Content {
		if rendered, ok := renderContent(content); ok {
			parts = append(parts, rendered)
			continue
		}

		raw, err := json.Marshal(content)
		if err == nil {
			parts = append(parts, string(raw))
		}
	}

	if len(parts) == 0 {
		return nil, exitCode
	}

	out := strings.Join(parts, "\n")
	return ensureTrailingNewline([]byte(out)), exitCode
}

func renderContent(content mcp.Content) (string, bool) {
	switch c := content.(type) {
	case mcp.TextContent:
		return c.Text, true
	case *mcp.TextContent:
		return c.Text, true
	case mcp.ImageContent:
		path, err := writeTempBase64("mcpx-image", c.MIMEType, c.Data)
		if err != nil {
			return "", false
		}
		return path, true
	case *mcp.ImageContent:
		path, err := writeTempBase64("mcpx-image", c.MIMEType, c.Data)
		if err != nil {
			return "", false
		}
		return path, true
	case mcp.EmbeddedResource:
		return renderResourceContent(c.Resource)
	case *mcp.EmbeddedResource:
		return renderResourceContent(c.Resource)
	default:
		var typed struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Data     string          `json:"data"`
			MIMEType string          `json:"mimeType"`
			Resource json.RawMessage `json:"resource"`
		}
		raw, err := json.Marshal(content)
		if err != nil || json.Unmarshal(raw, &typed) != nil {
			return "", false
		}
		switch typed.Type {
		case "text":
			return typed.Text, true
		case "image":
			path, err := writeTempBase64("mcpx-image", typed.MIMEType, typed.Data)
			if err != nil {
				return "", false
			}
			return path, true
		case "resource":
			return renderResourceJSON(typed.Resource)
		default:
			return "", false
		}
	}
}

func renderResourceContent(resource mcp.ResourceContents) (string, bool) {
	switch r := resource.(type) {
	case mcp.TextResourceContents:
		path, err := writeTempFile("mcpx-resource", r.MIMEType, []byte(r.Text))
		if err != nil {
			return "", false
		}
		return path, true
	case *mcp.TextResourceContents:
		path, err := writeTempFile("mcpx-resource", r.MIMEType, []byte(r.Text))
		if err != nil {
			return "", false
		}
		return path, true
	case mcp.BlobResourceContents:
		path, err := writeTempBase64("mcpx-resource", r.MIMEType, r.Blob)
		if err != nil {
			return "", false
		}
		return path, true
	case *mcp.BlobResourceContents:
		path, err := writeTempBase64("mcpx-resource", r.MIMEType, r.Blob)
		if err != nil {
			return "", false
		}
		return path, true
	default:
		return "", false
	}
}

func renderResourceJSON(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var textRes struct {
		Text     string `json:"text"`
		MIMEType string `json:"mimeType"`
	}
	if json.Unmarshal(raw, &textRes) == nil && textRes.Text != "" {
		path, err := writeTempFile("mcpx-resource", textRes.MIMEType, []byte(textRes.Text))
		if err != nil {
			return "", false
		}
		return path, true
	}
	var blobRes struct {
		Blob     string `json:"blob"`
		MIMEType string `json:"mimeType"`
	}
	if json.Unmarshal(raw, &blobRes) == nil && blobRes.Blob != "" {
		path, err := writeTempBase64("mcpx-resource", blobRes.MIMEType, blobRes.Blob)
		if err != nil {
			return "", false
		}
		return path, true
	}
	return "", false
}

func writeTempBase64(prefix, mimeType, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return writeTempFile(prefix, mimeType, data)
}

func writeTempFile(prefix, mimeType string, data []byte) (string, error) {
	ext := extForMIMEType(mimeType)
	f, err := os.CreateTemp("", prefix+"-*"+ext)
	if err != nil {
		return "", err
	}

	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func extForMIMEType(mimeType string) string {
	mimeType = strings.TrimSpace(strings.ToLower(mimeType))
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType != "" {
		if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
			return exts[0]
		}
		if strings.HasPrefix(mimeType, "text/") {
			return ".txt"
		}
		if strings.Contains(mimeType, "json") {
			return ".json"
		}
	}
	return ".bin"
}

func ensureTrailingNewline(out []byte) []byte {
	if len(out) == 0 {
		return out
	}
	if out[len(out)-1] != '\n' {
		return append(out, '\n')
	}
	return out
}
