package config

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// Validate checks configuration invariants and returns actionable errors.
func Validate(cfg *Config) error {
	if cfg == nil {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		srv := cfg.Servers[name]
		errs = append(errs, validateServer(name, srv)...)
	}

	return errors.Join(errs...)
}

func validateServer(name string, srv ServerConfig) []error {
	var errs []error

	hasCommand := strings.TrimSpace(srv.Command) != ""
	hasURL := strings.TrimSpace(srv.URL) != ""

	switch {
	case hasCommand && hasURL:
		errs = append(errs, fmt.Errorf("servers.%s: configure either command (stdio) or url (http), not both", name))
	case !hasCommand && !hasURL:
		errs = append(errs, fmt.Errorf("servers.%s: missing transport, set command (stdio) or url (http)", name))
	}

	if hasURL {
		if _, err := url.ParseRequestURI(srv.URL); err != nil {
			errs = append(errs, fmt.Errorf("servers.%s.url: invalid URL %q: %w", name, srv.URL, err))
		}
	}

	if srv.DefaultCacheTTL != "" {
		ttl, err := time.ParseDuration(srv.DefaultCacheTTL)
		if err != nil {
			errs = append(errs, fmt.Errorf("servers.%s.default_cache_ttl: invalid duration %q: %w", name, srv.DefaultCacheTTL, err))
		} else if ttl <= 0 {
			errs = append(errs, fmt.Errorf("servers.%s.default_cache_ttl: must be > 0, got %q", name, srv.DefaultCacheTTL))
		}
	}

	for i, pattern := range srv.NoCacheTools {
		if _, err := path.Match(pattern, "probe"); err != nil {
			errs = append(errs, fmt.Errorf("servers.%s.no_cache_tools[%d]: invalid glob %q: %w", name, i, pattern, err))
		}
	}

	return errs
}
