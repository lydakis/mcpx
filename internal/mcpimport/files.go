package mcpimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
)

func listCursor(_ context.Context, cwd string) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return listManifestPaths(withProjectManifest(cwd, []string{".cursor", "mcp.json"}, filepath.Join(home, ".cursor", "mcp.json")), cwd)
}

func listClaude(_ context.Context, cwd string) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	paths := withProjectManifest(cwd, []string{".mcp.json"})
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"))
	case "linux":
		paths = append(paths, filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"))
	}

	servers := make(map[string]config.FallbackImportEntry)
	var errs []error
	claudePath := filepath.Join(home, ".claude.json")
	layers, layerErr := config.LoadFallbackSourceLayersForImport(claudePath, cwd)
	if layerErr != nil && !os.IsNotExist(layerErr) {
		errs = append(errs, fmt.Errorf("%s: %w", claudePath, layerErr))
	}
	mergeImportEntries(servers, layers.MatchedProject)
	middle, middleErr := loadManifestPaths(paths, cwd)
	if middleErr != nil {
		errs = append(errs, middleErr)
	}
	mergeImportEntries(servers, middle)
	mergeImportEntries(servers, layers.TopLevel)
	return candidatesFromEntries(servers), errors.Join(errs...)
}

func listCline(_ context.Context, cwd string) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dataDir := strings.TrimSpace(os.Getenv("CLINE_DATA_DIR"))
	if dataDir == "" {
		dataDir = filepath.Join(home, ".cline", "data")
	}
	paths := withProjectManifest(cwd, []string{".cline", "mcp.json"}, filepath.Join(dataDir, "settings", "cline_mcp_settings.json"))
	var legacyPath string
	switch runtime.GOOS {
	case "darwin":
		legacyPath = filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
	case "linux":
		legacyPath = filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
	}
	if legacyPath != "" && !containsPath(paths, legacyPath) {
		paths = append(paths, legacyPath)
	}
	return listManifestPaths(paths, cwd)
}

func listKiro(_ context.Context, cwd string) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return listManifestPaths(withProjectManifest(cwd, []string{".kiro", "settings", "mcp.json"}, filepath.Join(home, ".kiro", "settings", "mcp.json")), cwd)
}

func withProjectManifest(cwd string, relative []string, additional ...string) []string {
	paths := make([]string, 0, len(additional)+1)
	if projectPath := nearestManifest(cwd, relative...); projectPath != "" {
		paths = append(paths, projectPath)
	}
	for _, path := range additional {
		if path == "" || containsPath(paths, path) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func nearestManifest(cwd string, relative ...string) string {
	dir := filepath.Clean(cwd)
	if dir == "" || dir == "." {
		return ""
	}
	if !filepath.IsAbs(dir) {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return ""
		}
		dir = absolute
	}
	for {
		candidate := filepath.Join(append([]string{dir}, relative...)...)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func containsPath(paths []string, candidate string) bool {
	candidate = filepath.Clean(candidate)
	for _, path := range paths {
		if filepath.Clean(path) == candidate {
			return true
		}
	}
	return false
}

func listManifestPaths(paths []string, cwd string) ([]Candidate, error) {
	servers, err := loadManifestPaths(paths, cwd)
	return candidatesFromEntries(servers), err
}

func loadManifestPaths(paths []string, cwd string) (map[string]config.FallbackImportEntry, error) {
	servers := make(map[string]config.FallbackImportEntry)
	var errs []error
	for _, path := range paths {
		found, err := config.LoadFallbackSourceForImport(path, cwd)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		mergeImportEntries(servers, found)
	}
	return servers, errors.Join(errs...)
}

func mergeImportEntries(dst, src map[string]config.FallbackImportEntry) {
	for name, server := range src {
		if _, exists := dst[name]; !exists {
			dst[name] = server
		}
	}
}

func candidatesFromEntries(servers map[string]config.FallbackImportEntry) []Candidate {
	candidates := make([]Candidate, 0, len(servers))
	for name, entry := range servers {
		server := entry.Server
		candidate := Candidate{
			Name:                   name,
			Enabled:                !entry.Disabled,
			Server:                 server,
			OAuthScopes:            append([]string(nil), entry.OAuthScopes...),
			OAuthClientMetadataURL: entry.OAuthClientMetadataURL,
			OAuthUnsupportedReason: entry.OAuthUnsupportedReason,
		}
		if entry.Disabled {
			candidate.DisabledReason = "disabled in source configuration"
		}
		candidate.Transport, candidate.UnsupportedReason = classifyFileTransport(entry.TransportType, server)
		if candidate.UnsupportedReason == "" {
			if err := config.ValidateServerConfig(name, server); err != nil {
				candidate.UnsupportedReason = "invalid " + candidate.Transport + " server configuration"
			} else {
				candidate.Supported = true
			}
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates
}

func classifyFileTransport(declared string, server config.ServerConfig) (string, string) {
	kind := strings.ToLower(strings.TrimSpace(declared))
	switch kind {
	case "":
		switch {
		case server.IsStdio() && !server.IsHTTP():
			return "stdio", ""
		case server.IsHTTP() && !server.IsStdio():
			return "streamable_http", ""
		default:
			return "unknown", ""
		}
	case "stdio":
		if !server.IsStdio() || server.IsHTTP() {
			return "stdio", "declared stdio transport requires a command without a URL"
		}
		return "stdio", ""
	case "http", "streamable-http", "streamable_http":
		if !server.IsHTTP() || server.IsStdio() {
			return "streamable_http", "declared HTTP transport requires a URL without a command"
		}
		return "streamable_http", ""
	case "sse":
		return "sse", "unsupported transport \"sse\""
	default:
		return kind, fmt.Sprintf("unsupported transport %q", kind)
	}
}
