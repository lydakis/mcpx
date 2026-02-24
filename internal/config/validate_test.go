package config

import (
	"strings"
	"testing"
)

func TestValidateAcceptsValidStdioAndHTTPServers(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"github": {
				Command:         "npx",
				Args:            []string{"-y", "@modelcontextprotocol/server-github"},
				DefaultCacheTTL: "30s",
				NoCacheTools:    []string{"create-*"},
			},
			"apify": {
				URL: "https://mcp.apify.com",
			},
		},
	}

	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateRejectsMissingAndMixedTransports(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"missing": {},
			"mixed": {
				Command: "npx",
				URL:     "https://example.com/mcp",
			},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "servers.missing: missing transport") {
		t.Fatalf("Validate() error = %q, want missing transport message", msg)
	}
	if !strings.Contains(msg, "servers.mixed: configure either command") {
		t.Fatalf("Validate() error = %q, want mixed transport message", msg)
	}
}

func TestValidateRejectsInvalidURLTTLAndGlob(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"bad": {
				URL:             "://bad-url",
				DefaultCacheTTL: "abc",
				NoCacheTools:    []string{"["},
			},
			"bad_ttl_zero": {
				Command:         "npx",
				DefaultCacheTTL: "0s",
			},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "servers.bad.url: invalid URL") {
		t.Fatalf("Validate() error = %q, want invalid URL message", msg)
	}
	if !strings.Contains(msg, "servers.bad.default_cache_ttl: invalid duration") {
		t.Fatalf("Validate() error = %q, want invalid default_cache_ttl message", msg)
	}
	if !strings.Contains(msg, "servers.bad.no_cache_tools[0]: invalid glob") {
		t.Fatalf("Validate() error = %q, want invalid glob message", msg)
	}
	if !strings.Contains(msg, "servers.bad_ttl_zero.default_cache_ttl: must be > 0") {
		t.Fatalf("Validate() error = %q, want non-positive TTL message", msg)
	}
}
