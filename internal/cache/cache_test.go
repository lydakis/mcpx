package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutGetRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := Put("github", "search_repositories", args, []byte("cached\n"), 0, 30*time.Second); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	content, exitCode, ok := Get("github", "search_repositories", args)
	if !ok {
		t.Fatal("Get() cache miss, want hit")
	}
	if string(content) != "cached\n" {
		t.Fatalf("Get() content = %q, want %q", content, "cached\n")
	}
	if exitCode != 0 {
		t.Fatalf("Get() exit code = %d, want 0", exitCode)
	}

	path := entryPath("github", "search_repositories", args)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("cache file mode = %o, want 600", got)
	}
}

func TestGetExpiredEntryRemovesFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := Put("github", "search_repositories", args, []byte("stale"), 0, -1*time.Second); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	path := entryPath("github", "search_repositories", args)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected cache file before read, stat error: %v", err)
	}

	_, _, ok := Get("github", "search_repositories", args)
	if ok {
		t.Fatal("Get() hit = true, want false for expired entry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected expired cache file to be removed, stat error = %v", err)
	}
}

func TestGetCorruptEntryRemovesFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	path := entryPath("github", "search_repositories", args)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("write corrupt cache file: %v", err)
	}

	_, _, ok := Get("github", "search_repositories", args)
	if ok {
		t.Fatal("Get() hit = true, want false for corrupt entry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt cache file to be removed, stat error = %v", err)
	}
}

func TestEntryPathStableAndScoped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	a := entryPath("github", "search_repositories", args)
	b := entryPath("github", "search_repositories", args)
	c := entryPath("github", "get_repo", args)

	if a != b {
		t.Fatalf("entryPath() not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("entryPath() should differ per tool, got %q", a)
	}
}

func TestGetMetadataReturnsAgeAndTTLForHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := Put("github", "search_repositories", args, []byte("cached\n"), 0, 2*time.Second); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	age, ttl, ok := GetMetadata("github", "search_repositories", args)
	if !ok {
		t.Fatal("GetMetadata() cache miss, want hit")
	}
	if age < 0 {
		t.Fatalf("GetMetadata() age = %s, want >= 0", age)
	}
	if ttl <= 0 {
		t.Fatalf("GetMetadata() ttl = %s, want > 0", ttl)
	}
	if ttl > 2*time.Second {
		t.Fatalf("GetMetadata() ttl = %s, want <= 2s", ttl)
	}
}

func TestGetMetadataMiss(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	age, ttl, ok := GetMetadata("github", "search_repositories", json.RawMessage(`{"query":"mcp"}`))
	if ok {
		t.Fatalf("GetMetadata() ok = %v, want false", ok)
	}
	if age != 0 || ttl != 0 {
		t.Fatalf("GetMetadata() age/ttl = %s/%s, want 0/0", age, ttl)
	}
}
