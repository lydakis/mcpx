package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lydakis/mcpx/internal/paths"
)

type entry struct {
	Content  []byte    `json:"content"`
	ExitCode int       `json:"exit_code"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
}

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

func getEntry(server, tool string, args json.RawMessage) (entry, string, bool) {
	path := entryPath(server, tool, args)
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
