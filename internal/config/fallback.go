package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
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

type codexConfigDocument struct {
	MCPServers     map[string]codexMCPServerEntry `toml:"mcp_servers"`
	Features       codexFeaturesDocument          `toml:"features"`
	ChatGPTBaseURL string                         `toml:"chatgpt_base_url"`
}

type codexFeaturesDocument struct {
	Apps           *bool `toml:"apps"`
	AppsMCPGateway *bool `toml:"apps_mcp_gateway"`
}

type codexMCPServerEntry struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	EnvVars []string          `toml:"env_vars"`

	URL               string            `toml:"url"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
	HTTPHeaders       map[string]string `toml:"http_headers"`
	EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`

	Enabled *bool `toml:"enabled"`
}

type codexAuthDocument struct {
	Tokens *codexAuthTokens `json:"tokens"`
}

type codexAuthTokens struct {
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
}

const (
	codexAppsServerName                  = "codex_apps"
	codexConnectorsTokenEnvVar           = "CODEX_CONNECTORS_TOKEN"
	defaultCodexChatGPTBaseURL           = "https://chatgpt.com/backend-api"
	openAIConnectorsMCPBaseURL           = "https://api.openai.com"
	openAIConnectorsMCPPath              = "/v1/connectors/gateways/flat/mcp"
	codexConfigName                      = "config.toml"
	codexConfigDirName                   = ".codex"
	codexAuthFileName                    = "auth.json"
	chatGPTAccountIDHeader               = "ChatGPT-Account-ID"
	authorizationHeader                  = "Authorization"
	bearerAuthPrefix                     = "Bearer "
)

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
		found, err := loadFallbackSourceForCWD(path, cwd)
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

func loadFallbackSourceForCWD(path, cwd string) (map[string]ServerConfig, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		return loadCodexConfigFile(path)
	default:
		return loadMCPServersFileForCWD(path, cwd)
	}
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

func loadCodexConfigFile(path string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc codexConfigDocument
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing codex config TOML: %w", err)
	}

	servers := make(map[string]ServerConfig, len(doc.MCPServers))
	for name, entry := range doc.MCPServers {
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}

		env := copyStringMap(entry.Env)
		for _, key := range entry.EnvVars {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if _, exists := env[key]; exists {
				continue
			}
			if val, ok := os.LookupEnv(key); ok {
				if env == nil {
					env = make(map[string]string)
				}
				env[key] = val
			}
		}

		headers := copyStringMap(entry.HTTPHeaders)
		for header, envVar := range entry.EnvHTTPHeaders {
			header = strings.TrimSpace(header)
			envVar = strings.TrimSpace(envVar)
			if header == "" || envVar == "" {
				continue
			}
			if hasHeaderKey(headers, header) {
				continue
			}
			if headers == nil {
				headers = make(map[string]string)
			}
			headers[header] = "${" + envVar + "}"
		}
		if tokenEnv := strings.TrimSpace(entry.BearerTokenEnvVar); tokenEnv != "" {
			if headers == nil {
				headers = make(map[string]string)
			}
			if !hasHeaderKey(headers, "Authorization") {
				headers["Authorization"] = "Bearer ${" + tokenEnv + "}"
			}
		}

		servers[name] = expandServerEnvVars(ServerConfig{
			Command: entry.Command,
			Args:    entry.Args,
			Env:     env,
			URL:     entry.URL,
			Headers: headers,
		})
	}

	if _, exists := servers[codexAppsServerName]; !exists {
		if appServer, ok := codexAppsServerFromConfig(path, doc); ok {
			servers[codexAppsServerName] = appServer
		}
	}

	return servers, nil
}

func codexAppsServerFromConfig(configPath string, doc codexConfigDocument) (ServerConfig, bool) {
	if doc.Features.Apps == nil || !*doc.Features.Apps {
		return ServerConfig{}, false
	}

	token := strings.TrimSpace(os.Getenv(codexConnectorsTokenEnvVar))
	accountID := ""
	if token == "" {
		var ok bool
		token, accountID, ok = readCodexAuthTokens(codexAuthFilePath(configPath))
		if !ok {
			return ServerConfig{}, false
		}
	} else {
		_, accountID, _ = readCodexAuthTokens(codexAuthFilePath(configPath))
	}

	headers := map[string]string{
		authorizationHeader: bearerAuthPrefix + token,
	}
	if accountID != "" {
		headers[chatGPTAccountIDHeader] = accountID
	}

	return ServerConfig{
		URL:     codexAppsURL(doc),
		Headers: headers,
	}, true
}

func codexAppsURL(doc codexConfigDocument) string {
	if doc.Features.AppsMCPGateway != nil && *doc.Features.AppsMCPGateway {
		return openAIConnectorsMCPBaseURL + openAIConnectorsMCPPath
	}

	baseURL := strings.TrimSpace(doc.ChatGPTBaseURL)
	if baseURL == "" {
		baseURL = defaultCodexChatGPTBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if (strings.HasPrefix(baseURL, "https://chatgpt.com") ||
		strings.HasPrefix(baseURL, "https://chat.openai.com")) &&
		!strings.Contains(baseURL, "/backend-api") {
		baseURL += "/backend-api"
	}
	if strings.Contains(baseURL, "/backend-api") {
		return baseURL + "/wham/apps"
	}
	if strings.Contains(baseURL, "/api/codex") {
		return baseURL + "/apps"
	}
	return baseURL + "/api/codex/apps"
}

func codexAuthFilePath(configPath string) string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, codexAuthFileName)
	}

	configDir := filepath.Dir(configPath)
	if filepath.Base(strings.TrimSpace(configPath)) == codexConfigName &&
		filepath.Base(configDir) == codexConfigDirName {
		return filepath.Join(configDir, codexAuthFileName)
	}

	home, _ := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, codexConfigDirName, codexAuthFileName)
}

func readCodexAuthTokens(path string) (token string, accountID string, ok bool) {
	if strings.TrimSpace(path) == "" {
		return "", "", false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}

	var doc codexAuthDocument
	if err := json.Unmarshal(data, &doc); err != nil || doc.Tokens == nil {
		return "", "", false
	}

	token = strings.TrimSpace(doc.Tokens.AccessToken)
	accountID = strings.TrimSpace(doc.Tokens.AccountID)
	if token == "" {
		return "", "", false
	}
	return token, accountID, true
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func hasHeaderKey(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
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
			filepath.Join(home, ".codex", "config.toml"),
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
			filepath.Join(home, ".codex", "config.toml"),
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
