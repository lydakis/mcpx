package config

import (
	"fmt"
	"os"
	"regexp"

	"github.com/BurntSushi/toml"
	"github.com/lydakis/mcpx/internal/paths"
)

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads the config file and returns the parsed Config.
// If the config file does not exist, it returns an empty Config (no error).
func Load() (*Config, error) {
	return LoadFrom(paths.ConfigFile())
}

// LoadForEdit reads the config file for in-place edits.
// Unlike Load, it preserves raw ${ENV_VAR} placeholders.
func LoadForEdit() (*Config, error) {
	return LoadForEditFrom(paths.ConfigFile())
}

// LoadFrom reads and parses a config file at the given path.
func LoadFrom(path string) (*Config, error) {
	return loadFrom(path, true)
}

// LoadForEditFrom reads and parses a config file at the given path for edits.
// It intentionally skips env expansion so writes do not bake secrets.
func LoadForEditFrom(path string) (*Config, error) {
	return loadFrom(path, false)
}

// ExpandServerForCurrentEnv returns a copy of server with ${ENV_VAR}
// placeholders expanded from the current process environment.
func ExpandServerForCurrentEnv(server ServerConfig) ServerConfig {
	return expandServerEnvVars(cloneServerConfig(server))
}

func loadFrom(path string, expand bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Servers: make(map[string]ServerConfig)}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]ServerConfig)
	}
	if expand {
		expandConfigEnvVars(&cfg)
	}
	return &cfg, nil
}

// ExampleConfigPath returns the default config file path (for help messages).
func ExampleConfigPath() string {
	return paths.ConfigFile()
}

func expandConfigEnvVars(cfg *Config) {
	if cfg == nil {
		return
	}

	for i := range cfg.FallbackSources {
		cfg.FallbackSources[i] = expandEnvVars(cfg.FallbackSources[i])
	}

	for name, srv := range cfg.Servers {
		cfg.Servers[name] = expandServerEnvVars(srv)
	}
}

func expandServerEnvVars(srv ServerConfig) ServerConfig {
	srv.Command = expandEnvVars(srv.Command)
	srv.URL = expandEnvVars(srv.URL)
	srv.DefaultCacheTTL = expandEnvVars(srv.DefaultCacheTTL)

	for i := range srv.Args {
		srv.Args[i] = expandEnvVars(srv.Args[i])
	}
	for i := range srv.NoCacheTools {
		srv.NoCacheTools[i] = expandEnvVars(srv.NoCacheTools[i])
	}
	for k, v := range srv.Env {
		srv.Env[k] = expandEnvVars(v)
	}
	for k, v := range srv.Headers {
		srv.Headers[k] = expandEnvVars(v)
	}

	return srv
}

// expandEnvVars replaces ${VAR_NAME} with the value of the environment variable.
func expandEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := envVarRe.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		return match // leave unresolved vars as-is
	})
}
