package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type mcpServersDocument struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// MergeFallbackServers fills cfg.Servers from external MCP fallback sources
// when the primary config has no servers.
func MergeFallbackServers(cfg *Config) error {
	if cfg == nil || len(cfg.Servers) > 0 {
		return nil
	}

	fallback, err := loadFallbackServers(fallbackSourcePaths(cfg))
	if len(fallback) > 0 {
		if cfg.Servers == nil {
			cfg.Servers = make(map[string]ServerConfig)
		}
		for name, srv := range fallback {
			cfg.Servers[name] = srv
		}
	}
	return err
}

// LoadFallbackServers imports server configs from standard mcpServers JSON
// documents used by existing MCP clients.
func LoadFallbackServers() (map[string]ServerConfig, error) {
	return loadFallbackServers(fallbackSourcePaths(nil))
}

func loadFallbackServers(paths []string) (map[string]ServerConfig, error) {
	servers := make(map[string]ServerConfig)
	var errs []error

	for _, path := range paths {
		found, err := loadMCPServersFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}

		for name, srv := range found {
			if _, exists := servers[name]; exists {
				continue
			}
			servers[name] = srv
		}
	}

	if len(errs) > 0 {
		return servers, errors.Join(errs...)
	}
	return servers, nil
}

func loadMCPServersFile(path string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc mcpServersDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing mcpServers JSON: %w", err)
	}

	servers := make(map[string]ServerConfig, len(doc.MCPServers))
	for name, srv := range doc.MCPServers {
		servers[name] = expandServerEnvVars(ServerConfig{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			URL:     srv.URL,
			Headers: srv.Headers,
		})
	}
	return servers, nil
}

func fallbackSourcePaths(cfg *Config) []string {
	if cfg != nil && cfg.FallbackSources != nil {
		return compactPaths(cfg.FallbackSources)
	}
	return compactPaths(defaultFallbackSourcePaths())
}

func defaultFallbackSourcePaths() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, ".cursor", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
		}
	case "linux":
		return []string{
			filepath.Join(home, ".cursor", "mcp.json"),
			filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
			filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
		}
	default:
		return nil
	}
}

func compactPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
