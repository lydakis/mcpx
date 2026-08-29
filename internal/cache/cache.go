package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lydakis/mcpx/internal/paths"
)

type entry struct {
	Content  []byte    `json:"content"`
	ExitCode int       `json:"exit_code"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
}

const credentialTransitionDir = "response-credential-transitions"

var credentialTransitionLocks = struct {
	sync.Mutex
	files map[string]*os.File
}{files: make(map[string]*os.File)}

// Get looks up a cached response. Returns nil if not found or expired.
func Get(server, tool string, args json.RawMessage) ([]byte, int, bool) {
	e, _, ok := getEntry(server, tool, args)
	if !ok {
		return nil, 0, false
	}
	return e.Content, e.ExitCode, true
}

// GetMetadata returns cache age and ttl when a valid entry exists.
func GetMetadata(server, tool string, args json.RawMessage) (time.Duration, time.Duration, bool) {
	e, path, ok := getEntry(server, tool, args)
	if !ok {
		return 0, 0, false
	}

	created := e.Created
	if created.IsZero() {
		if st, err := os.Stat(path); err == nil {
			created = st.ModTime()
		}
	}
	if created.IsZero() {
		created = e.Expires
	}

	ttl := e.Expires.Sub(created)
	if ttl < 0 {
		ttl = 0
	}

	age := time.Since(created)
	if age < 0 {
		age = 0
	}

	return age, ttl, true
}

// Put stores a response in the cache.
func Put(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error {
	if credentialTransitionPending() {
		return nil
	}
	dir := cacheDir()
	if err := paths.EnsureDir(dir); err != nil {
		return err
	}

	now := time.Now()
	e := entry{
		Content:  content,
		ExitCode: exitCode,
		Created:  now,
		Expires:  now.Add(ttl),
	}

	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	return os.WriteFile(entryPath(server, tool, args), data, 0600)
}

// Clear invalidates every cached tool response. This is intentionally global:
// entries created before credential-aware invalidation cannot be attributed to
// a particular OAuth account safely.
func Clear() error {
	return os.RemoveAll(cacheDir())
}

// BeginCredentialTransition prevents cache reads and writes before OAuth
// credentials can change. The marker remains until a daemon has closed its
// credential-bound connections and cleared all pre-transition responses.
func BeginCredentialTransition() (string, error) {
	dir := credentialTransitionPath()
	if err := paths.EnsureDir(dir); err != nil {
		return "", err
	}
	marker, err := os.CreateTemp(filepath.Dir(dir), ".credential-transition-")
	if err != nil {
		return "", err
	}
	cleanup := func() {
		_ = marker.Close()
		_ = os.Remove(marker.Name())
	}
	if err := syscall.Flock(int(marker.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		cleanup()
		return "", fmt.Errorf("locking credential transition marker: %w", err)
	}
	suffix := strings.TrimPrefix(filepath.Base(marker.Name()), ".credential-transition-")
	token := fmt.Sprintf("transition-%d-%s", os.Getpid(), suffix)
	if err := os.Rename(marker.Name(), filepath.Join(dir, token)); err != nil {
		cleanup()
		return "", fmt.Errorf("publishing credential transition marker: %w", err)
	}
	credentialTransitionLocks.Lock()
	credentialTransitionLocks.files[token] = marker
	credentialTransitionLocks.Unlock()
	return token, Clear()
}

// CompleteCredentialTransition clears any writes that raced with transition
// startup, then re-enables caching by removing the fail-closed marker.
func CompleteCredentialTransition(token string) error {
	if err := Clear(); err != nil {
		return err
	}
	return RemoveCredentialTransition(token)
}

// RemoveCredentialTransition re-enables caching after the caller has
// established the credential boundary and invalidated pre-transition entries.
func RemoveCredentialTransition(token string) error {
	if !validCredentialTransitionToken(token) {
		return fmt.Errorf("invalid credential transition token")
	}
	if err := os.Remove(filepath.Join(credentialTransitionPath(), token)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return releaseCredentialTransitionLock(token)
}

// AbortCredentialTransition re-enables caching when the caller proves that the
// attempted transition did not change credentials. Other concurrent markers
// continue to keep the cache fail-closed.
func AbortCredentialTransition(token string) error {
	return RemoveCredentialTransition(token)
}

// RecoverCredentialTransition lets a freshly started daemon safely complete a
// transition left pending because the previous daemon could not be reached.
func RecoverCredentialTransition() error {
	orphaned, err := OrphanedCredentialTransitions()
	if err != nil {
		return err
	}
	if len(orphaned) == 0 {
		return nil
	}
	if err := Clear(); err != nil {
		return err
	}
	for _, token := range orphaned {
		if err := RemoveCredentialTransition(token); err != nil {
			return err
		}
	}
	return nil
}

// OrphanedCredentialTransitions reports markers whose owning process no
// longer holds the transition lock. It does not mutate cache or marker state;
// a running daemon must coordinate invalidation with its cache guard and pool.
func OrphanedCredentialTransitions() ([]string, error) {
	entries, err := os.ReadDir(credentialTransitionPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	orphaned := make([]string, 0, len(entries))
	for _, entry := range entries {
		alive, err := credentialTransitionOwnerAlive(entry.Name())
		if err != nil {
			return nil, err
		}
		if !alive {
			orphaned = append(orphaned, entry.Name())
		}
	}
	sort.Strings(orphaned)
	return orphaned, nil
}

func getEntry(server, tool string, args json.RawMessage) (entry, string, bool) {
	path := entryPath(server, tool, args)
	if credentialTransitionPending() {
		return entry{}, path, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return entry{}, path, false
	}

	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		_ = os.Remove(path)
		return entry{}, path, false
	}

	if time.Now().After(e.Expires) {
		_ = os.Remove(path)
		return entry{}, path, false
	}

	return e, path, true
}

func entryPath(server, tool string, args json.RawMessage) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", server, tool, string(args))
	key := hex.EncodeToString(h.Sum(nil))[:32]
	return filepath.Join(cacheDir(), key+".json")
}

func cacheDir() string {
	return filepath.Join(paths.CacheDir(), "responses")
}

func credentialTransitionPath() string {
	return filepath.Join(paths.CacheDir(), credentialTransitionDir)
}

func credentialTransitionPending() bool {
	pending, err := credentialTransitionState()
	return err != nil || pending
}

// CredentialTransitionPending reports whether response caching and
// credential-bound daemon work must remain fail-closed.
func CredentialTransitionPending() bool {
	return credentialTransitionPending()
}

func credentialTransitionState() (bool, error) {
	entries, err := os.ReadDir(credentialTransitionPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func credentialTransitionOwnerAlive(token string) (bool, error) {
	if !validCredentialTransitionToken(token) {
		return false, fmt.Errorf("invalid credential transition marker %q", token)
	}
	marker, err := os.OpenFile(filepath.Join(credentialTransitionPath(), token), os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer marker.Close() //nolint:errcheck

	err = syscall.Flock(int(marker.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err := syscall.Flock(int(marker.Fd()), syscall.LOCK_UN); err != nil {
		return false, err
	}
	return false, nil
}

func validCredentialTransitionToken(token string) bool {
	return token != "" && filepath.Base(token) == token && strings.HasPrefix(token, "transition-")
}

func releaseCredentialTransitionLock(token string) error {
	credentialTransitionLocks.Lock()
	marker := credentialTransitionLocks.files[token]
	delete(credentialTransitionLocks.files, token)
	credentialTransitionLocks.Unlock()
	if marker == nil {
		return nil
	}
	return marker.Close()
}
