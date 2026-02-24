package paths

import (
	"os"
	"path/filepath"
)

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func xdgDir(envVar, fallbackSuffix string) string {
	if v := os.Getenv(envVar); v != "" {
		return filepath.Join(v, "mcpx")
	}
	return filepath.Join(homeDir(), fallbackSuffix, "mcpx")
}

func xdgBaseDir(envVar string, fallbackParts ...string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	parts := append([]string{homeDir()}, fallbackParts...)
	return filepath.Join(parts...)
}

// ConfigDir returns the mcpx config directory ($XDG_CONFIG_HOME/mcpx).
func ConfigDir() string {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// CacheDir returns the mcpx cache directory ($XDG_CACHE_HOME/mcpx).
func CacheDir() string {
	return xdgDir("XDG_CACHE_HOME", ".cache")
}

// StateDir returns the mcpx state directory ($XDG_STATE_HOME/mcpx).
func StateDir() string {
	return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// RuntimeDir returns the mcpx runtime directory for sockets and state.
// Falls back to $XDG_STATE_HOME/mcpx if XDG_RUNTIME_DIR is unset.
func RuntimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "mcpx")
	}
	return StateDir()
}

// ManDir returns the man page target directory ($XDG_DATA_HOME/man/man1).
func ManDir() string {
	dataHome := xdgBaseDir("XDG_DATA_HOME", ".local", "share")
	return filepath.Join(dataHome, "man", "man1")
}

// ConfigFile returns the path to config.toml.
func ConfigFile() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

// SocketPath returns the path to the daemon Unix socket.
func SocketPath() string {
	return filepath.Join(RuntimeDir(), "daemon.sock")
}

// StatePath returns the path to the daemon state file (contains nonce).
func StatePath() string {
	return filepath.Join(RuntimeDir(), "daemon.state")
}

// LockPath returns the path to the daemon file lock.
func LockPath() string {
	return filepath.Join(RuntimeDir(), "daemon.lock")
}

// EnsureDir creates a directory and parents if needed.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0700)
}
