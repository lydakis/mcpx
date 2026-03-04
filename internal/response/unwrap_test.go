package response

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCleanupTempArtifactsInDirRemovesOnlyManagedExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cutoff := now.Add(-tempArtifactRetention)

	oldManaged := filepath.Join(dir, tempArtifactPrefixImage+"old.bin")
	if err := os.WriteFile(oldManaged, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old managed file: %v", err)
	}
	if err := os.Chtimes(oldManaged, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("chtimes old managed file: %v", err)
	}

	recentManaged := filepath.Join(dir, tempArtifactPrefixResource+"recent.bin")
	if err := os.WriteFile(recentManaged, []byte("recent"), 0o600); err != nil {
		t.Fatalf("write recent managed file: %v", err)
	}
	if err := os.Chtimes(recentManaged, now.Add(-1*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatalf("chtimes recent managed file: %v", err)
	}

	oldUnmanaged := filepath.Join(dir, "other-old.bin")
	if err := os.WriteFile(oldUnmanaged, []byte("other"), 0o600); err != nil {
		t.Fatalf("write old unmanaged file: %v", err)
	}
	if err := os.Chtimes(oldUnmanaged, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("chtimes old unmanaged file: %v", err)
	}

	cleanupTempArtifactsInDir(dir, cutoff)

	if _, err := os.Stat(oldManaged); !os.IsNotExist(err) {
		t.Fatalf("old managed file should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(recentManaged); err != nil {
		t.Fatalf("recent managed file should remain, stat err = %v", err)
	}
	if _, err := os.Stat(oldUnmanaged); err != nil {
		t.Fatalf("old unmanaged file should remain, stat err = %v", err)
	}
}

func TestMaybeCleanupTempArtifactsRateLimitsSweeps(t *testing.T) {
	oldNowFn := nowFn
	oldCleanupFn := cleanupTempArtifactsFn
	tempArtifactCleanupMu.Lock()
	oldLast := lastTempArtifactCleanup
	lastTempArtifactCleanup = time.Time{}
	tempArtifactCleanupMu.Unlock()

	defer func() {
		nowFn = oldNowFn
		cleanupTempArtifactsFn = oldCleanupFn
		tempArtifactCleanupMu.Lock()
		lastTempArtifactCleanup = oldLast
		tempArtifactCleanupMu.Unlock()
	}()

	base := time.Now()
	now := base
	nowFn = func() time.Time { return now }

	callCount := 0
	cleanupTempArtifactsFn = func(cutoff time.Time) {
		callCount++
	}

	maybeCleanupTempArtifacts()
	now = base.Add(5 * time.Minute)
	maybeCleanupTempArtifacts()
	now = base.Add(tempArtifactCleanupEvery + time.Second)
	maybeCleanupTempArtifacts()

	if callCount != 2 {
		t.Fatalf("cleanup call count = %d, want 2", callCount)
	}
}

func TestUnwrapEmbeddedTextResourceWritesTempFileAndPrintsPath(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.EmbeddedResource{
				Type: "resource",
				Resource: mcp.TextResourceContents{
					URI:      "file:///tmp/note.txt",
					MIMEType: "text/plain",
					Text:     "resource text",
				},
			},
		},
	}

	out, code := Unwrap(result)
	if code != ipc.ExitOK {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitOK)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		t.Fatal("Unwrap emitted empty path for resource")
	}
	defer os.Remove(path) //nolint:errcheck

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading emitted file: %v", err)
	}
	if string(data) != "resource text" {
		t.Fatalf("file content = %q, want %q", string(data), "resource text")
	}
}

func TestUnwrapEmbeddedBlobResourceUsesMIMEExtension(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.EmbeddedResource{
				Type: "resource",
				Resource: &mcp.BlobResourceContents{
					URI:      "file:///tmp/payload.json",
					MIMEType: "application/json",
					Blob:     base64.StdEncoding.EncodeToString(payload),
				},
			},
		},
	}

	out, code := Unwrap(result)
	if code != ipc.ExitOK {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitOK)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		t.Fatal("Unwrap emitted empty path for blob resource")
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

func TestRenderResourceJSONWritesTextPayload(t *testing.T) {
	raw := json.RawMessage(`{"text":"typed resource text","mimeType":"text/plain"}`)
	path, ok := renderResourceJSON(raw)
	if !ok {
		t.Fatal("renderResourceJSON() ok = false, want true")
	}
	if path == "" {
		t.Fatal("renderResourceJSON() returned empty path")
	}
	defer os.Remove(path) //nolint:errcheck

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading emitted file: %v", err)
	}
	if string(data) != "typed resource text" {
		t.Fatalf("file content = %q, want %q", string(data), "typed resource text")
	}
}

func TestRenderResourceJSONWritesBlobPayload(t *testing.T) {
	payload := []byte("blob-bytes")
	raw := json.RawMessage(`{"blob":"` + base64.StdEncoding.EncodeToString(payload) + `","mimeType":"application/octet-stream"}`)
	path, ok := renderResourceJSON(raw)
	if !ok {
		t.Fatal("renderResourceJSON() ok = false, want true")
	}
	if path == "" {
		t.Fatal("renderResourceJSON() returned empty path")
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

func TestUnwrapFallsBackToJSONForUnsupportedContentType(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.AudioContent{
				Type:     "audio",
				Data:     "invalid-base64",
				MIMEType: "audio/wav",
			},
		},
	}

	out, code := Unwrap(result)
	if code != ipc.ExitOK {
		t.Fatalf("Unwrap code = %d, want %d", code, ipc.ExitOK)
	}
	if got := string(out); !strings.Contains(got, `"type":"audio"`) {
		t.Fatalf("Unwrap output = %q, want fallback JSON audio payload", got)
	}
}

func TestRenderResourceJSONRejectsUnknownPayload(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///tmp/unknown.bin"}`)
	path, ok := renderResourceJSON(raw)
	if ok {
		t.Fatalf("renderResourceJSON() ok = true, want false (path=%q)", path)
	}
	if path != "" {
		t.Fatalf("renderResourceJSON() path = %q, want empty", path)
	}
}

func TestEnsureTrailingNewlineBehavior(t *testing.T) {
	if got := ensureTrailingNewline(nil); got != nil {
		t.Fatalf("ensureTrailingNewline(nil) = %#v, want nil", got)
	}
	if got := string(ensureTrailingNewline([]byte("line"))); got != "line\n" {
		t.Fatalf("ensureTrailingNewline(no newline) = %q, want %q", got, "line\\n")
	}
	if got := string(ensureTrailingNewline([]byte("line\n"))); got != "line\n" {
		t.Fatalf("ensureTrailingNewline(existing newline) = %q, want %q", got, "line\\n")
	}
}

func TestExtForMIMETypeFallbacks(t *testing.T) {
	if got := extForMIMEType("text/x-mcpx-unknown"); got != ".txt" {
		t.Fatalf("extForMIMEType(text fallback) = %q, want %q", got, ".txt")
	}
	if got := extForMIMEType("application/x-mcpx+json"); got != ".json" {
		t.Fatalf("extForMIMEType(json fallback) = %q, want %q", got, ".json")
	}
	if got := extForMIMEType("application/x-mcpx-unknown"); got != ".bin" {
		t.Fatalf("extForMIMEType(default fallback) = %q, want %q", got, ".bin")
	}
}

func TestRenderResourceContentSupportsPointerAndValueTypes(t *testing.T) {
	textPath, ok := renderResourceContent(&mcp.TextResourceContents{
		URI:      "file:///tmp/text.txt",
		MIMEType: "text/plain",
		Text:     "hello",
	})
	if !ok {
		t.Fatal("renderResourceContent(text pointer) ok = false, want true")
	}
	defer os.Remove(textPath) //nolint:errcheck
	if data, err := os.ReadFile(textPath); err != nil || string(data) != "hello" {
		t.Fatalf("text resource data = %q err=%v, want %q and nil error", string(data), err, "hello")
	}

	blobData := []byte("blob")
	blobPath, ok := renderResourceContent(mcp.BlobResourceContents{
		URI:      "file:///tmp/blob.bin",
		MIMEType: "application/octet-stream",
		Blob:     base64.StdEncoding.EncodeToString(blobData),
	})
	if !ok {
		t.Fatal("renderResourceContent(blob value) ok = false, want true")
	}
	defer os.Remove(blobPath) //nolint:errcheck
	if data, err := os.ReadFile(blobPath); err != nil || string(data) != string(blobData) {
		t.Fatalf("blob resource data = %q err=%v, want %q and nil error", string(data), err, string(blobData))
	}
}

func TestRenderContentHandlesUnsupportedAndInvalidImage(t *testing.T) {
	if path, ok := renderContent(mcp.ResourceLink{Type: "resource_link", URI: "file:///tmp"}); ok || path != "" {
		t.Fatalf("renderContent(resource_link) = (%q, %v), want (\"\", false)", path, ok)
	}
	if path, ok := renderContent(mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "not-base64"}); ok || path != "" {
		t.Fatalf("renderContent(invalid image) = (%q, %v), want (\"\", false)", path, ok)
	}
}
