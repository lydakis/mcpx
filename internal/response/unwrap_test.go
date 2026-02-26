package response

import (
	"encoding/base64"
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
