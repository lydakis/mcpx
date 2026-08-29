package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/paths"
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

func TestClearInvalidatesAllResponseEntries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := Put("primary", "search", args, []byte("account-a"), 0, time.Hour); err != nil {
		t.Fatalf("Put(primary) error = %v", err)
	}
	if err := Put("other", "search", args, []byte("unrelated"), 0, time.Hour); err != nil {
		t.Fatalf("Put(other) error = %v", err)
	}

	if err := Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	for _, server := range []string{"primary", "other"} {
		if _, _, ok := Get(server, "search", args); ok {
			t.Fatalf("Get(%s) cache hit after Clear(), want miss", server)
		}
	}
}

func TestCredentialTransitionBlocksReadsAndWritesUntilCompleted(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	if err := Put("remote", "search", args, []byte("account-a"), 0, time.Hour); err != nil {
		t.Fatalf("Put(account-a) error = %v", err)
	}
	transition, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); ok {
		t.Fatal("Get() cache hit during credential transition, want miss")
	}
	if err := Put("remote", "search", args, []byte("stale-write"), 0, time.Hour); err != nil {
		t.Fatalf("Put() during credential transition error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); ok {
		t.Fatal("Get() returned a write made during credential transition")
	}

	if err := CompleteCredentialTransition(transition); err != nil {
		t.Fatalf("CompleteCredentialTransition() error = %v", err)
	}
	if err := Put("remote", "search", args, []byte("account-b"), 0, time.Hour); err != nil {
		t.Fatalf("Put(account-b) error = %v", err)
	}
	content, _, ok := Get("remote", "search", args)
	if !ok || string(content) != "account-b" {
		t.Fatalf("Get() after completed transition = %q, %v; want account-b hit", content, ok)
	}
}

func TestRecoverCredentialTransitionClearsTombstoneForFreshDaemon(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if err := paths.EnsureDir(credentialTransitionPath()); err != nil {
		t.Fatalf("creating transition dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(credentialTransitionPath(), "transition-1073741824-orphan"), nil, 0600); err != nil {
		t.Fatalf("creating orphan transition: %v", err)
	}
	if err := RecoverCredentialTransition(); err != nil {
		t.Fatalf("RecoverCredentialTransition() error = %v", err)
	}
	args := json.RawMessage(`{}`)
	if err := Put("remote", "search", args, []byte("fresh"), 0, time.Hour); err != nil {
		t.Fatalf("Put() after recovery error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); !ok {
		t.Fatal("Get() after recovery = miss, want cache re-enabled")
	}
}

func TestRecoverCredentialTransitionDoesNotTrustAReusedPID(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if err := paths.EnsureDir(credentialTransitionPath()); err != nil {
		t.Fatalf("creating transition dir: %v", err)
	}
	marker := filepath.Join(credentialTransitionPath(), fmt.Sprintf("transition-%d-orphan", os.Getpid()))
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatalf("creating reused-PID transition: %v", err)
	}
	if err := RecoverCredentialTransition(); err != nil {
		t.Fatalf("RecoverCredentialTransition() error = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("reused-PID marker still exists: %v", err)
	}
}

func TestRecoverCredentialTransitionPreservesLiveOwner(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	transition, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	if err := RecoverCredentialTransition(); err != nil {
		t.Fatalf("RecoverCredentialTransition() error = %v", err)
	}
	args := json.RawMessage(`{}`)
	if err := Put("remote", "search", args, []byte("racing"), 0, time.Hour); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); ok {
		t.Fatal("cache re-enabled while the credential-transition owner was alive")
	}
	if err := AbortCredentialTransition(transition); err != nil {
		t.Fatalf("AbortCredentialTransition() error = %v", err)
	}
	if err := Put("remote", "search", args, []byte("unchanged-account"), 0, time.Hour); err != nil {
		t.Fatalf("Put() after abort error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); !ok {
		t.Fatal("cache remained disabled after unchanged transition was aborted")
	}
}

func TestOrphanedCredentialTransitionsDoesNotMutateMarker(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	transition, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	if err := RecoverCredentialTransition(); err != nil {
		t.Fatalf("initial RecoverCredentialTransition() error = %v", err)
	}
	if err := releaseCredentialTransitionLock(transition); err != nil {
		t.Fatalf("simulating owner exit: %v", err)
	}
	orphaned, err := OrphanedCredentialTransitions()
	if err != nil {
		t.Fatalf("OrphanedCredentialTransitions() error = %v", err)
	}
	if len(orphaned) != 1 || orphaned[0] != transition {
		t.Fatalf("orphaned transitions = %#v, want [%q]", orphaned, transition)
	}
	if !CredentialTransitionPending() {
		t.Fatal("orphan inspection removed the pending transition")
	}
	if _, err := os.Stat(filepath.Join(credentialTransitionPath(), transition)); err != nil {
		t.Fatalf("orphaned transition marker was mutated: %v", err)
	}
	if err := RemoveCredentialTransition(transition); err != nil {
		t.Fatalf("RemoveCredentialTransition() error = %v", err)
	}
}

func TestRecoverCredentialTransitionRemovesDeadMarkersAlongsideLiveOwner(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	live, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	dead := filepath.Join(credentialTransitionPath(), "transition-1073741824-orphan")
	if err := os.WriteFile(dead, nil, 0600); err != nil {
		t.Fatalf("creating orphan transition: %v", err)
	}

	if err := RecoverCredentialTransition(); err != nil {
		t.Fatalf("RecoverCredentialTransition() error = %v", err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead marker still exists: %v", err)
	}
	args := json.RawMessage(`{}`)
	if err := Put("remote", "search", args, []byte("blocked"), 0, time.Hour); err != nil {
		t.Fatalf("Put() with live owner error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); ok {
		t.Fatal("cache re-enabled while live transition remained")
	}
	if err := AbortCredentialTransition(live); err != nil {
		t.Fatalf("AbortCredentialTransition() error = %v", err)
	}
	if err := Put("remote", "search", args, []byte("enabled"), 0, time.Hour); err != nil {
		t.Fatalf("Put() after live owner completed error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); !ok {
		t.Fatal("dead marker kept cache disabled after live owner completed")
	}
}

func TestCredentialTransitionsRemainBlockedUntilEveryConcurrentTransitionCompletes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	first, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition(first) error = %v", err)
	}
	second, err := BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition(second) error = %v", err)
	}
	if err := CompleteCredentialTransition(first); err != nil {
		t.Fatalf("CompleteCredentialTransition(first) error = %v", err)
	}
	args := json.RawMessage(`{}`)
	if err := Put("remote", "search", args, []byte("racing"), 0, time.Hour); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); ok {
		t.Fatal("cache re-enabled while a concurrent credential transition remained")
	}
	if err := CompleteCredentialTransition(second); err != nil {
		t.Fatalf("CompleteCredentialTransition(second) error = %v", err)
	}
	if err := Put("remote", "search", args, []byte("settled"), 0, time.Hour); err != nil {
		t.Fatalf("Put() after transitions error = %v", err)
	}
	if _, _, ok := Get("remote", "search", args); !ok {
		t.Fatal("cache remained disabled after every credential transition completed")
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

func TestGetMetadataHandlesFutureCreatedAndNegativeTTL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	path := entryPath("github", "search_repositories", args)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	now := time.Now()
	raw, err := json.Marshal(entry{
		Content:  []byte("cached"),
		ExitCode: 0,
		Created:  now.Add(20 * time.Second),
		Expires:  now.Add(10 * time.Second), // Expires before Created -> ttl should clamp to zero.
	})
	if err != nil {
		t.Fatalf("json.Marshal(entry) error = %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	age, ttl, ok := GetMetadata("github", "search_repositories", args)
	if !ok {
		t.Fatal("GetMetadata() cache miss, want hit")
	}
	if age != 0 {
		t.Fatalf("GetMetadata() age = %s, want 0 (clamped from future created)", age)
	}
	if ttl != 0 {
		t.Fatalf("GetMetadata() ttl = %s, want 0 (clamped from negative ttl)", ttl)
	}
}

func TestGetMetadataUsesFileModTimeWhenCreatedMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	args := json.RawMessage(`{"query":"mcp"}`)
	path := entryPath("github", "search_repositories", args)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	now := time.Now()
	modTime := now.Add(-3 * time.Second)
	raw, err := json.Marshal(entry{
		Content:  []byte("cached"),
		ExitCode: 0,
		Created:  time.Time{},               // Force stat(path).ModTime() fallback
		Expires:  now.Add(30 * time.Second), // Keep entry valid
	})
	if err != nil {
		t.Fatalf("json.Marshal(entry) error = %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes cache file: %v", err)
	}

	age, ttl, ok := GetMetadata("github", "search_repositories", args)
	if !ok {
		t.Fatal("GetMetadata() cache miss, want hit")
	}
	if age < 2*time.Second {
		t.Fatalf("GetMetadata() age = %s, want >= 2s from file modtime", age)
	}
	if ttl <= 25*time.Second {
		t.Fatalf("GetMetadata() ttl = %s, want > 25s based on expires-modtime", ttl)
	}
}
