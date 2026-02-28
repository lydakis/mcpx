package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLoadFallbackServersReadsMCPServersSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}

	doc := map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
				"env": map[string]string{
					"GITHUB_TOKEN": "${GITHUB_TOKEN}",
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	servers, err := LoadFallbackServers()
	if err != nil {
		t.Fatalf("LoadFallbackServers() error = %v", err)
	}

	srv, ok := servers["github"]
	if !ok {
		t.Fatalf("fallback servers = %#v, want github", servers)
	}
	if srv.Command != "npx" {
		t.Fatalf("command = %q, want %q", srv.Command, "npx")
	}
}

func TestMergeFallbackServersUsesFallbackWhenConfigEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}

	raw := []byte(`{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","."]}}}`)
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{Servers: map[string]ServerConfig{}}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if _, ok := cfg.Servers["filesystem"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want filesystem fallback", cfg.Servers)
	}
}

func TestMergeFallbackServersKeepsManagedAndAddsDiscovered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}

	raw := []byte(`{"mcpServers":{
		"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]},
		"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","."]}
	}}`)
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"github": {Command: "echo", Args: []string{"managed"}},
		},
		ServerOrigins: map[string]ServerOrigin{
			"github": NewServerOrigin(ServerOriginKindMCPXConfig, "/tmp/config.toml"),
		},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	managed := cfg.Servers["github"]
	if managed.Command != "echo" || len(managed.Args) != 1 || managed.Args[0] != "managed" {
		t.Fatalf("managed server overwritten: %#v", managed)
	}
	if origin := cfg.ServerOrigins["github"]; origin.Kind != ServerOriginKindMCPXConfig {
		t.Fatalf("managed origin kind = %q, want %q", origin.Kind, ServerOriginKindMCPXConfig)
	}

	if _, ok := cfg.Servers["filesystem"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want discovered filesystem server", cfg.Servers)
	}
	if origin := cfg.ServerOrigins["filesystem"]; origin.Kind == ServerOriginKindMCPXConfig {
		t.Fatalf("filesystem origin kind = %q, want discovered source kind", origin.Kind)
	}
}

func TestMergeFallbackServersUsesConfiguredSources(t *testing.T) {
	customPath := filepath.Join(t.TempDir(), "custom-mcp.json")
	raw := []byte(`{"mcpServers":{"custom":{"command":"uvx","args":["mcp-custom"]}}}`)
	if err := os.WriteFile(customPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{
		Servers:         map[string]ServerConfig{},
		FallbackSources: []string{customPath},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if _, ok := cfg.Servers["custom"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want custom fallback", cfg.Servers)
	}
	origin, ok := cfg.ServerOrigins["custom"]
	if !ok {
		t.Fatalf("ServerOrigins[custom] missing")
	}
	if origin.Kind != ServerOriginKindFallbackCustom {
		t.Fatalf("ServerOrigins[custom].Kind = %q, want %q", origin.Kind, ServerOriginKindFallbackCustom)
	}
	if origin.Path != customPath {
		t.Fatalf("ServerOrigins[custom].Path = %q, want %q", origin.Path, customPath)
	}
}

func TestMergeFallbackServersUsesConfiguredSourceOrderForCollisions(t *testing.T) {
	tmp := t.TempDir()
	first := filepath.Join(tmp, "first.json")
	second := filepath.Join(tmp, "second.json")

	firstRaw := []byte(`{"mcpServers":{
		"shared":{"command":"first-command"},
		"first-only":{"command":"first-only-command"}
	}}`)
	secondRaw := []byte(`{"mcpServers":{
		"shared":{"command":"second-command"},
		"second-only":{"command":"second-only-command"}
	}}`)
	if err := os.WriteFile(first, firstRaw, 0600); err != nil {
		t.Fatalf("write first fallback file: %v", err)
	}
	if err := os.WriteFile(second, secondRaw, 0600); err != nil {
		t.Fatalf("write second fallback file: %v", err)
	}

	cfg := &Config{
		Servers:         map[string]ServerConfig{},
		FallbackSources: []string{first, second},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if got := cfg.Servers["shared"].Command; got != "first-command" {
		t.Fatalf("shared command = %q, want %q", got, "first-command")
	}
	if got := cfg.Servers["first-only"].Command; got != "first-only-command" {
		t.Fatalf("first-only command = %q, want %q", got, "first-only-command")
	}
	if got := cfg.Servers["second-only"].Command; got != "second-only-command" {
		t.Fatalf("second-only command = %q, want %q", got, "second-only-command")
	}
}

func TestClassifyFallbackOriginKnownPaths(t *testing.T) {
	cursorPath := filepath.Join(t.TempDir(), ".cursor", "mcp.json")
	cursorOrigin := classifyFallbackOrigin(cursorPath)
	if cursorOrigin.Kind != ServerOriginKindCursor {
		t.Fatalf("cursor origin kind = %q, want %q", cursorOrigin.Kind, ServerOriginKindCursor)
	}
	if cursorOrigin.Path != cursorPath {
		t.Fatalf("cursor origin path = %q, want %q", cursorOrigin.Path, cursorPath)
	}

	codexPath := filepath.Join(t.TempDir(), ".codex", "config.toml")
	codexOrigin := classifyFallbackOrigin(codexPath)
	if codexOrigin.Kind != ServerOriginKindCodex {
		t.Fatalf("codex origin kind = %q, want %q", codexOrigin.Kind, ServerOriginKindCodex)
	}
	if codexOrigin.Path != codexPath {
		t.Fatalf("codex origin path = %q, want %q", codexOrigin.Path, codexPath)
	}

	claudeProjectPath := filepath.Join(t.TempDir(), ".mcp.json")
	claudeProjectOrigin := classifyFallbackOrigin(claudeProjectPath)
	if claudeProjectOrigin.Kind != ServerOriginKindClaude {
		t.Fatalf("project .mcp.json origin kind = %q, want %q", claudeProjectOrigin.Kind, ServerOriginKindClaude)
	}
	if claudeProjectOrigin.Path != claudeProjectPath {
		t.Fatalf("project .mcp.json origin path = %q, want %q", claudeProjectOrigin.Path, claudeProjectPath)
	}
}

func TestMergeFallbackServersExplicitEmptySourcesDisablesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	fallbackPath := paths[0]
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0700); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}
	raw := []byte(`{"mcpServers":{"default":{"command":"npx","args":["server-default"]}}}`)
	if err := os.WriteFile(fallbackPath, raw, 0600); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}

	cfg := &Config{
		Servers:         map[string]ServerConfig{},
		FallbackSources: []string{},
	}
	if err := MergeFallbackServers(cfg); err != nil {
		t.Fatalf("MergeFallbackServers() error = %v", err)
	}

	if len(cfg.Servers) != 0 {
		t.Fatalf("cfg.Servers = %#v, want empty with fallback disabled", cfg.Servers)
	}
}

func TestDefaultFallbackSourcePathsIncludeCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	var want string
	switch runtime.GOOS {
	case "darwin", "linux":
		want = filepath.Join(home, ".cursor", "mcp.json")
	default:
		t.Skip("cursor fallback path not defined for this platform")
	}

	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Fatalf("fallback paths = %#v, want %q", paths, want)
}

func TestDefaultFallbackSourcePathsIncludeClaudeCodeAndKiro(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := fallbackSourcePaths(nil)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	var want []string
	switch runtime.GOOS {
	case "darwin", "linux":
		want = []string{
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".codex", "config.toml"),
			filepath.Join(home, ".kiro", "settings", "mcp.json"),
		}
	default:
		t.Skip("claude/kiro fallback paths not defined for this platform")
	}

	for _, expected := range want {
		found := false
		for _, p := range paths {
			if p == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fallback paths = %#v, want %q", paths, expected)
		}
	}
}

func TestLoadCodexConfigFileReadsMCPServers(t *testing.T) {
	t.Setenv("REMOTE_TOKEN", "tok-123")
	t.Setenv("TRACE_ID", "trace-abc")
	t.Setenv("PASSTHROUGH_ENV", "pass-through")

	path := filepath.Join(t.TempDir(), "config.toml")
	raw := []byte(`
[mcp_servers.playwright]
command = "npx"
args = ["@playwright/mcp@latest"]
env = { STATIC_VALUE = "one" }
env_vars = ["PASSTHROUGH_ENV"]

[mcp_servers.remote]
url = "https://example.com/mcp"
http_headers = { "X-Static" = "static" }
env_http_headers = { "X-Trace-ID" = "TRACE_ID" }
bearer_token_env_var = "REMOTE_TOKEN"

[mcp_servers.disabled]
command = "npx"
args = ["disabled-server"]
enabled = false
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(path)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	playwright, ok := servers["playwright"]
	if !ok {
		t.Fatalf("servers = %#v, want playwright", servers)
	}
	if playwright.Command != "npx" {
		t.Fatalf("playwright command = %q, want npx", playwright.Command)
	}
	if got := playwright.Env["PASSTHROUGH_ENV"]; got != "pass-through" {
		t.Fatalf("playwright env PASSTHROUGH_ENV = %q, want pass-through", got)
	}
	if got := playwright.Env["STATIC_VALUE"]; got != "one" {
		t.Fatalf("playwright env STATIC_VALUE = %q, want one", got)
	}

	remote, ok := servers["remote"]
	if !ok {
		t.Fatalf("servers = %#v, want remote", servers)
	}
	if remote.URL != "https://example.com/mcp" {
		t.Fatalf("remote url = %q, want https://example.com/mcp", remote.URL)
	}
	if got := remote.Headers["Authorization"]; got != "Bearer tok-123" {
		t.Fatalf("remote Authorization = %q, want %q", got, "Bearer tok-123")
	}
	if got := remote.Headers["X-Trace-ID"]; got != "trace-abc" {
		t.Fatalf("remote X-Trace-ID = %q, want trace-abc", got)
	}
	if got := remote.Headers["X-Static"]; got != "static" {
		t.Fatalf("remote X-Static = %q, want static", got)
	}

	if _, ok := servers["disabled"]; ok {
		t.Fatalf("servers = %#v, want disabled server omitted", servers)
	}
}

func TestLoadCodexConfigFileHeaderPrecedence(t *testing.T) {
	t.Setenv("REMOTE_TOKEN", "tok-123")
	t.Setenv("ALT_AUTH", "Bearer alt")

	path := filepath.Join(t.TempDir(), "config.toml")
	raw := []byte(`
[mcp_servers.remote]
url = "https://example.com/mcp"
http_headers = { Authorization = "Bearer explicit" }
env_http_headers = { Authorization = "ALT_AUTH" }
bearer_token_env_var = "REMOTE_TOKEN"
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(path)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	if got := servers["remote"].Headers["Authorization"]; got != "Bearer explicit" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer explicit")
	}
}

func TestLoadCodexConfigFileHeaderPrecedenceCaseInsensitive(t *testing.T) {
	t.Setenv("REMOTE_TOKEN", "tok-123")
	t.Setenv("ALT_AUTH", "Bearer alt")

	path := filepath.Join(t.TempDir(), "config.toml")
	raw := []byte(`
[mcp_servers.remote]
url = "https://example.com/mcp"
http_headers = { authorization = "Bearer explicit-lower" }
env_http_headers = { Authorization = "ALT_AUTH" }
bearer_token_env_var = "REMOTE_TOKEN"
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(path)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	if got := servers["remote"].Headers["authorization"]; got != "Bearer explicit-lower" {
		t.Fatalf("authorization = %q, want %q", got, "Bearer explicit-lower")
	}
	if _, ok := servers["remote"].Headers["Authorization"]; ok {
		t.Fatalf("headers = %#v, want no extra Authorization key", servers["remote"].Headers)
	}
}

func TestLoadCodexConfigFileAddsCodexAppsServerFromAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
[features]
apps = true
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	authRaw := []byte(`{"tokens":{"access_token":"access-123","account_id":"acct-456"}}`)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), authRaw, 0600); err != nil {
		t.Fatalf("write codex auth: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	apps, ok := servers[codexAppsServerName]
	if !ok {
		t.Fatalf("servers = %#v, want %q", servers, codexAppsServerName)
	}
	if apps.URL != "https://chatgpt.com/backend-api/wham/apps" {
		t.Fatalf("codex_apps url = %q, want %q", apps.URL, "https://chatgpt.com/backend-api/wham/apps")
	}
	if got := apps.Headers["Authorization"]; got != "Bearer access-123" {
		t.Fatalf("codex_apps Authorization = %q, want %q", got, "Bearer access-123")
	}
	if got := apps.Headers["ChatGPT-Account-ID"]; got != "acct-456" {
		t.Fatalf("codex_apps ChatGPT-Account-ID = %q, want %q", got, "acct-456")
	}
}

func TestLoadCodexConfigFileCodexAppsUsesConnectorsTokenEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(codexConnectorsTokenEnvVar, "connectors-999")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
chatgpt_base_url = "https://chatgpt.com"
[features]
apps = true
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	authRaw := []byte(`{"tokens":{"access_token":"ignored","account_id":"acct-789"}}`)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), authRaw, 0600); err != nil {
		t.Fatalf("write codex auth: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	apps, ok := servers[codexAppsServerName]
	if !ok {
		t.Fatalf("servers = %#v, want %q", servers, codexAppsServerName)
	}
	if apps.URL != "https://chatgpt.com/backend-api/wham/apps" {
		t.Fatalf("codex_apps url = %q, want %q", apps.URL, "https://chatgpt.com/backend-api/wham/apps")
	}
	if got := apps.Headers["Authorization"]; got != "Bearer connectors-999" {
		t.Fatalf("codex_apps Authorization = %q, want %q", got, "Bearer connectors-999")
	}
	if got := apps.Headers["ChatGPT-Account-ID"]; got != "acct-789" {
		t.Fatalf("codex_apps ChatGPT-Account-ID = %q, want %q", got, "acct-789")
	}
}

func TestLoadCodexConfigFileSkipsCodexAppsWithoutToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
[features]
apps = true
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	if _, ok := servers[codexAppsServerName]; ok {
		t.Fatalf("servers = %#v, want %q omitted when auth token is unavailable", servers, codexAppsServerName)
	}
}

func TestLoadCodexConfigFileRespectsDisabledCodexAppsEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
[features]
apps = true

[mcp_servers.codex_apps]
enabled = false
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	authRaw := []byte(`{"tokens":{"access_token":"access-123","account_id":"acct-456"}}`)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), authRaw, 0600); err != nil {
		t.Fatalf("write codex auth: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	if _, ok := servers[codexAppsServerName]; ok {
		t.Fatalf("servers = %#v, want %q omitted when explicitly disabled", servers, codexAppsServerName)
	}
}

func TestLoadCodexConfigFileCodexAppsUsesMCPGatewayURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(codexConnectorsTokenEnvVar, "connectors-123")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
[features]
apps = true
apps_mcp_gateway = true
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	apps, ok := servers[codexAppsServerName]
	if !ok {
		t.Fatalf("servers = %#v, want %q", servers, codexAppsServerName)
	}
	if apps.URL != "https://api.openai.com/v1/connectors/gateways/flat/mcp" {
		t.Fatalf("codex_apps url = %q, want %q", apps.URL, "https://api.openai.com/v1/connectors/gateways/flat/mcp")
	}
}

func TestLoadCodexConfigFileCodexAppsPreservesExplicitCodexPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(codexConnectorsTokenEnvVar, "connectors-123")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	configRaw := []byte(`
chatgpt_base_url = "https://chatgpt.com/api/codex"
[features]
apps = true
`)
	if err := os.WriteFile(configPath, configRaw, 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	servers, err := loadCodexConfigFile(configPath)
	if err != nil {
		t.Fatalf("loadCodexConfigFile() error = %v", err)
	}

	apps, ok := servers[codexAppsServerName]
	if !ok {
		t.Fatalf("servers = %#v, want %q", servers, codexAppsServerName)
	}
	if apps.URL != "https://chatgpt.com/api/codex/apps" {
		t.Fatalf("codex_apps url = %q, want %q", apps.URL, "https://chatgpt.com/api/codex/apps")
	}
}

func TestLoadMCPServersFileReadsClaudeCodeProjectServers(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	projectSubdir := filepath.Join(projectRoot, "subdir")
	if err := os.MkdirAll(projectSubdir, 0700); err != nil {
		t.Fatalf("mkdir project subdir: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(projectSubdir); err != nil {
		t.Fatalf("chdir project subdir: %v", err)
	}

	docPath := filepath.Join(root, ".claude.json")
	raw := []byte(`{
		"mcpServers": {
			"user-server": {"command":"user-command"},
			"shared": {"command":"user-shared"}
		},
		"projects": {
			"` + projectRoot + `": {
				"mcpServers": {
					"local-server": {"command":"local-command"},
					"shared": {"command":"local-shared"}
				}
			},
			"` + filepath.Join(root, "other-project") + `": {
				"mcpServers": {
					"other": {"command":"other-command"}
				}
			}
		}
	}`)
	if err := os.WriteFile(docPath, raw, 0600); err != nil {
		t.Fatalf("write claude doc: %v", err)
	}

	servers, err := loadMCPServersFile(docPath)
	if err != nil {
		t.Fatalf("loadMCPServersFile() error = %v", err)
	}

	if _, ok := servers["local-server"]; !ok {
		t.Fatalf("servers = %#v, want local-server", servers)
	}
	if got := servers["shared"].Command; got != "local-shared" {
		t.Fatalf("shared command = %q, want %q", got, "local-shared")
	}
	if _, ok := servers["user-server"]; !ok {
		t.Fatalf("servers = %#v, want user-server", servers)
	}
	if _, ok := servers["other"]; ok {
		t.Fatalf("servers = %#v, want other project server excluded", servers)
	}
}

func TestNearestUpwardPathFindsNearestParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	grandChild := filepath.Join(child, "grandchild")
	if err := os.MkdirAll(grandChild, 0700); err != nil {
		t.Fatalf("mkdir grandchild: %v", err)
	}

	nearest := filepath.Join(child, ".mcp.json")
	farther := filepath.Join(parent, ".mcp.json")
	for _, path := range []string{nearest, farther} {
		if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(grandChild); err != nil {
		t.Fatalf("chdir grandchild: %v", err)
	}

	if got := nearestUpwardPath(".mcp.json", ""); got != nearest {
		gotResolved, gotErr := filepath.EvalSymlinks(got)
		wantResolved, wantErr := filepath.EvalSymlinks(nearest)
		if gotErr != nil || wantErr != nil || gotResolved != wantResolved {
			t.Fatalf("nearestUpwardPath(.mcp.json) = %q, want %q", got, nearest)
		}
	}
}

func TestMergeFallbackServersForCWDUsesProvidedWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	projectA := filepath.Join(root, "project-a")
	projectB := filepath.Join(root, "project-b")
	projectASubdir := filepath.Join(projectA, "subdir")
	projectBSubdir := filepath.Join(projectB, "subdir")
	for _, dir := range []string{projectASubdir, projectBSubdir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	projectAConfig := filepath.Join(projectA, ".mcp.json")
	projectBConfig := filepath.Join(projectB, ".mcp.json")
	if err := os.WriteFile(projectAConfig, []byte(`{"mcpServers":{"server-a":{"command":"cmd-a"}}}`), 0600); err != nil {
		t.Fatalf("write project-a config: %v", err)
	}
	if err := os.WriteFile(projectBConfig, []byte(`{"mcpServers":{"server-b":{"command":"cmd-b"}}}`), 0600); err != nil {
		t.Fatalf("write project-b config: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(projectASubdir); err != nil {
		t.Fatalf("chdir project-a subdir: %v", err)
	}

	paths := fallbackSourcePathsForCWD(nil, projectBSubdir)
	if len(paths) == 0 {
		t.Skip("no fallback source paths for this platform")
	}

	cfg := &Config{Servers: map[string]ServerConfig{}}
	if err := MergeFallbackServersForCWD(cfg, projectBSubdir); err != nil {
		t.Fatalf("MergeFallbackServersForCWD() error = %v", err)
	}
	if _, ok := cfg.Servers["server-b"]; !ok {
		t.Fatalf("cfg.Servers = %#v, want server-b from provided cwd", cfg.Servers)
	}
	if _, ok := cfg.Servers["server-a"]; ok {
		t.Fatalf("cfg.Servers = %#v, want server-a excluded", cfg.Servers)
	}
}

func TestFallbackSourcePathsPreserveDeclaredDefaultOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := compactPaths(defaultFallbackSourcePaths())
	if len(want) == 0 {
		t.Skip("no default fallback source paths for this platform")
	}

	got := fallbackSourcePaths(nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback source order = %#v, want %#v", got, want)
	}
}
