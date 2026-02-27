package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/lydakis/mcpx/internal/paths"
)

// Save writes the config to the default config path atomically.
func Save(cfg *Config) error {
	return SaveTo(paths.ConfigFile(), cfg)
}

// SaveTo writes cfg to path atomically.
func SaveTo(path string, cfg *Config) error {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]ServerConfig)
	}

	var payload bytes.Buffer
	if err := toml.NewEncoder(&payload).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".config.toml.tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp config file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("setting temp config permissions: %w", err)
	}
	if _, err := tmpFile.Write(payload.Bytes()); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing temp config file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("syncing temp config file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp config file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replacing config file: %w", err)
	}
	cleanup = false
	return nil
}
