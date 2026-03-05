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

func TestValidateServerConfigRequiresServerName(t *testing.T) {
	err := ValidateServerConfig("   ", ServerConfig{Command: "npx"})
	if err == nil {
		t.Fatal("ValidateServerConfig(blank) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "server name is required") {
		t.Fatalf("ValidateServerConfig(blank) error = %q, want required-name message", err.Error())
	}
}

func TestValidateServerConfigValidAndInvalidCases(t *testing.T) {
	if err := ValidateServerConfig("github", ServerConfig{Command: "npx"}); err != nil {
		t.Fatalf("ValidateServerConfig(valid) error = %v, want nil", err)
	}

	err := ValidateServerConfig("github", ServerConfig{
		Command:         "npx",
		URL:             "https://example.com",
		DefaultCacheTTL: "bad",
		NoCacheTools:    []string{"["},
	})
	if err == nil {
		t.Fatal("ValidateServerConfig(invalid) error = nil, want non-nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "configure either command") {
		t.Fatalf("ValidateServerConfig(invalid) error = %q, want mixed-transport message", msg)
	}
	if !strings.Contains(msg, "invalid duration") {
		t.Fatalf("ValidateServerConfig(invalid) error = %q, want invalid duration message", msg)
	}
	if !strings.Contains(msg, "invalid glob") {
		t.Fatalf("ValidateServerConfig(invalid) error = %q, want invalid glob message", msg)
	}
}

func TestServerConfigTransportPredicates(t *testing.T) {
	cases := []struct {
		name  string
		cfg   ServerConfig
		stdio bool
		http  bool
	}{
		{
			name:  "stdio only",
			cfg:   ServerConfig{Command: "npx"},
			stdio: true,
			http:  false,
		},
		{
			name:  "http only",
			cfg:   ServerConfig{URL: "https://example.com/mcp"},
			stdio: false,
			http:  true,
		},
		{
			name:  "both configured",
			cfg:   ServerConfig{Command: "npx", URL: "https://example.com/mcp"},
			stdio: true,
			http:  true,
		},
		{
			name:  "neither configured",
			cfg:   ServerConfig{},
			stdio: false,
			http:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsStdio(); got != tc.stdio {
				t.Fatalf("IsStdio() = %v, want %v", got, tc.stdio)
			}
			if got := tc.cfg.IsHTTP(); got != tc.http {
				t.Fatalf("IsHTTP() = %v, want %v", got, tc.http)
			}
		})
	}
}

func TestCloneToolMapDeepCopiesCachePointers(t *testing.T) {
	if got := cloneToolMap(nil); got != nil {
		t.Fatalf("cloneToolMap(nil) = %#v, want nil", got)
	}

	cacheEnabled := true
	in := map[string]ToolConfig{
		"search": {Cache: &cacheEnabled},
		"list":   {},
	}

	out := cloneToolMap(in)
	if len(out) != len(in) {
		t.Fatalf("len(cloneToolMap(in)) = %d, want %d", len(out), len(in))
	}
	if out["search"].Cache == nil {
		t.Fatal("cloneToolMap(in)[search].Cache = nil, want non-nil")
	}
	if out["search"].Cache == in["search"].Cache {
		t.Fatal("cloneToolMap(in) reused cache pointer, want deep copy")
	}
	if *out["search"].Cache != true {
		t.Fatalf("cloneToolMap(in)[search].Cache = %v, want true", *out["search"].Cache)
	}

	*in["search"].Cache = false
	if *out["search"].Cache != true {
		t.Fatalf("cloneToolMap(in) cache pointer tracks source mutation: got %v, want true", *out["search"].Cache)
	}
}
