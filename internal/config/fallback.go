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
	Projects   map[string]projectEntry   `json:"projects"`
}

type projectEntry struct {
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
	return MergeFallbackServersForCWD(cfg, "")
}

// MergeFallbackServersForCWD is like MergeFallbackServers but resolves
// project-scoped fallback files against the provided working directory.
// When cwd is empty, it falls back to the process working directory.
func MergeFallbackServersForCWD(cfg *Config, cwd string) error {
	if cfg == nil || len(cfg.Servers) > 0 {
		return nil
	}

	fallback, err := loadFallbackServersForCWD(fallbackSourcePathsForCWD(cfg, cwd), cwd)
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
	return loadFallbackServersForCWD(fallbackSourcePaths(nil), "")
}

func loadFallbackServersForCWD(paths []string, cwd string) (map[string]ServerConfig, error) {
	servers := make(map[string]ServerConfig)
	var errs []error

	for _, path := range paths {
		found, err := loadMCPServersFileForCWD(path, cwd)
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
	return loadMCPServersFileForCWD(path, "")
}

func loadMCPServersFileForCWD(path, cwd string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc mcpServersDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing mcpServers JSON: %w", err)
	}

	servers := make(map[string]ServerConfig, len(doc.MCPServers))
	mergeServerEntries(servers, matchProjectServers(doc.Projects, cwd))
	mergeServerEntries(servers, doc.MCPServers)
	return servers, nil
}

func mergeServerEntries(dst map[string]ServerConfig, src map[string]mcpServerEntry) {
	for name, srv := range src {
		if _, exists := dst[name]; exists {
			continue
		}
		dst[name] = expandServerEnvVars(ServerConfig{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			URL:     srv.URL,
			Headers: srv.Headers,
		})
	}
}

func matchProjectServers(projects map[string]projectEntry, cwd string) map[string]mcpServerEntry {
	if len(projects) == 0 {
		return nil
	}

	base := resolveWorkingDirectory(cwd)
	if base == "" {
		return nil
	}

	candidates := []string{base}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		resolved = filepath.Clean(resolved)
		if resolved != candidates[0] {
			candidates = append(candidates, resolved)
		}
	}

	bestLen := -1
	var best map[string]mcpServerEntry
	for projectPath, entry := range projects {
		if len(entry.MCPServers) == 0 {
			continue
		}

		projectPaths := []string{filepath.Clean(projectPath)}
		if resolved, err := filepath.EvalSymlinks(projectPath); err == nil {
			resolved = filepath.Clean(resolved)
			if resolved != projectPaths[0] {
				projectPaths = append(projectPaths, resolved)
			}
		}

		for _, cwdPath := range candidates {
			for _, candidateProjectPath := range projectPaths {
				if !isWithinPath(cwdPath, candidateProjectPath) {
					continue
				}
				if len(candidateProjectPath) > bestLen {
					bestLen = len(candidateProjectPath)
					best = entry.MCPServers
				}
				break
			}
		}
	}

	return best
}

func isWithinPath(path, root string) bool {
	if path == root {
		return true
	}
	if root == string(os.PathSeparator) {
		return strings.HasPrefix(path, root)
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func nearestUpwardPath(relPath, cwd string) string {
	base := resolveWorkingDirectory(cwd)
	if base == "" {
		return ""
	}

	dir := base
	for {
		candidate := filepath.Join(dir, relPath)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func fallbackSourcePaths(cfg *Config) []string {
	return fallbackSourcePathsForCWD(cfg, "")
}

func fallbackSourcePathsForCWD(cfg *Config, cwd string) []string {
	if cfg != nil && cfg.FallbackSources != nil {
		return compactPaths(cfg.FallbackSources)
	}
	return compactPaths(defaultFallbackSourcePathsForCWD(cwd))
}

func defaultFallbackSourcePaths() []string {
	return defaultFallbackSourcePathsForCWD("")
}

func defaultFallbackSourcePathsForCWD(cwd string) []string {
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
			filepath.Join(home, ".claude.json"),
			nearestUpwardPath(".mcp.json", cwd),
			filepath.Join(home, ".kiro", "settings", "mcp.json"),
			nearestUpwardPath(filepath.Join(".kiro", "settings", "mcp.json"), cwd),
		}
	case "linux":
		return []string{
			filepath.Join(home, ".cursor", "mcp.json"),
			filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
			filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
			filepath.Join(home, ".claude.json"),
			nearestUpwardPath(".mcp.json", cwd),
			filepath.Join(home, ".kiro", "settings", "mcp.json"),
			nearestUpwardPath(filepath.Join(".kiro", "settings", "mcp.json"), cwd),
		}
	default:
		return nil
	}
}

func resolveWorkingDirectory(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return filepath.Clean(cwd)
	}

	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return ""
	}
	return filepath.Clean(wd)
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
