package config

import "strings"

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
	ServerOriginKindCodexApps        ServerOriginKind = "codex_apps"
	ServerOriginKindMCPXConfig       ServerOriginKind = "mcpx_config"
	ServerOriginKindCursor           ServerOriginKind = "cursor"
	ServerOriginKindCodex            ServerOriginKind = "codex"
	ServerOriginKindClaude           ServerOriginKind = "claude"
	ServerOriginKindKiro             ServerOriginKind = "kiro"
	ServerOriginKindFallbackCustom   ServerOriginKind = "fallback_custom"
	ServerOriginKindRuntimeEphemeral ServerOriginKind = "runtime_ephemeral"
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
	OAuth   bool              `toml:"oauth,omitempty"`
	// OAuthScopes optionally narrows scopes requested during first-class login.
	OAuthScopes []string `toml:"oauth_scopes,omitempty"`
	// OAuthClientMetadataURL enables Client ID Metadata Document registration.
	// DCR remains the fallback when the authorization server supports it.
	OAuthClientMetadataURL string `toml:"oauth_client_metadata_url,omitempty"`

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

// HasAuthorizationHeader reports whether an explicit Authorization value wins
// over first-class OAuth for this server.
func (s ServerConfig) HasAuthorizationHeader() bool {
	for name, value := range s.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
