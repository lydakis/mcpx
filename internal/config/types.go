package config

// Config is the top-level mcpx configuration.
type Config struct {
	Servers         map[string]ServerConfig `toml:"servers"`
	FallbackSources []string                `toml:"fallback_sources"`
	// ServerOrigins records where each server entry came from at runtime.
	// It is runtime metadata only and is not persisted to config.toml.
	ServerOrigins map[string]ServerOrigin `toml:"-" json:"-"`
}

type ServerOriginKind string

const (
	ServerOriginKindCodexApps      ServerOriginKind = "codex_apps"
	ServerOriginKindMCPXConfig     ServerOriginKind = "mcpx_config"
	ServerOriginKindCursor         ServerOriginKind = "cursor"
	ServerOriginKindCodex          ServerOriginKind = "codex"
	ServerOriginKindClaude         ServerOriginKind = "claude"
	ServerOriginKindKiro           ServerOriginKind = "kiro"
	ServerOriginKindFallbackCustom ServerOriginKind = "fallback_custom"
)

// ServerOrigin describes the source of a resolved server entry.
type ServerOrigin struct {
	Kind ServerOriginKind `json:"kind"`
	Path string           `json:"path,omitempty"`
}

func NewServerOrigin(kind ServerOriginKind, path string) ServerOrigin {
	normalizedKind := kind
	if normalizedKind == "" {
		normalizedKind = ServerOriginKindFallbackCustom
	}
	return ServerOrigin{
		Kind: normalizedKind,
		Path: path,
	}
}

func NormalizeServerOrigin(origin ServerOrigin) ServerOrigin {
	kind := origin.Kind
	if kind == "" {
		kind = ServerOriginKindFallbackCustom
	}
	return ServerOrigin{
		Kind: kind,
		Path: origin.Path,
	}
}

// ServerConfig describes how to connect to a single MCP server.
type ServerConfig struct {
	// Stdio transport
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`

	// HTTP transport
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`

	// Caching
	DefaultCacheTTL string                `toml:"default_cache_ttl"`
	NoCacheTools    []string              `toml:"no_cache_tools"`
	Tools           map[string]ToolConfig `toml:"tools"`
}

// ToolConfig holds per-tool overrides.
type ToolConfig struct {
	Cache *bool `toml:"cache"`
}

// IsStdio returns true if the server uses stdio transport.
func (s ServerConfig) IsStdio() bool {
	return s.Command != ""
}

// IsHTTP returns true if the server uses HTTP transport.
func (s ServerConfig) IsHTTP() bool {
	return s.URL != ""
}
