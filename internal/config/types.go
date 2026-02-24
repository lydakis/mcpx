package config

// Config is the top-level mcpx configuration.
type Config struct {
	Servers         map[string]ServerConfig `toml:"servers"`
	FallbackSources []string                `toml:"fallback_sources"`
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
