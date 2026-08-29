package mcpimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/processenv"
)

type codexCatalogEntry struct {
	Name           string                `json:"name"`
	Enabled        bool                  `json:"enabled"`
	DisabledReason string                `json:"disabled_reason"`
	AuthStatus     string                `json:"auth_status"`
	Transport      codexCatalogTransport `json:"transport"`
}

type codexCatalogTransport struct {
	Type              string            `json:"type"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	CWD               string            `json:"cwd"`
	Env               map[string]string `json:"env"`
	URL               string            `json:"url"`
	HTTPHeaders       map[string]string `json:"http_headers"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var"`
}

type codexSourceContext struct {
	CWD     string `json:"cwd"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

func prepareCodexContext(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("resolving Codex source context: working directory must be absolute")
	}
	pathEnv := strings.TrimSpace(os.Getenv("PATH"))
	if pathEnv == "" {
		return "", fmt.Errorf("resolving Codex source context: PATH is empty")
	}
	command, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("locating codex executable: %w", err)
	}
	if !filepath.IsAbs(command) {
		command, err = filepath.Abs(command)
		if err != nil {
			return "", fmt.Errorf("resolving codex executable: %w", err)
		}
	}
	payload, err := json.Marshal(codexSourceContext{CWD: cwd, Command: command, Path: pathEnv})
	if err != nil {
		return "", fmt.Errorf("encoding codex source context: %w", err)
	}
	return string(payload), nil
}

func parseCodexContext(raw string) (codexSourceContext, error) {
	var sourceContext codexSourceContext
	if err := json.Unmarshal([]byte(raw), &sourceContext); err != nil {
		return codexSourceContext{}, fmt.Errorf("invalid saved Codex source context: %w", err)
	}
	sourceContext.CWD = strings.TrimSpace(sourceContext.CWD)
	sourceContext.Command = strings.TrimSpace(sourceContext.Command)
	sourceContext.Path = strings.TrimSpace(sourceContext.Path)
	if !filepath.IsAbs(sourceContext.CWD) || !filepath.IsAbs(sourceContext.Command) || sourceContext.Path == "" {
		return codexSourceContext{}, fmt.Errorf("invalid saved Codex source context")
	}
	return sourceContext, nil
}

func refreshCodexContext(raw string) (string, error) {
	saved, err := parseCodexContext(raw)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath(saved.Command); err == nil {
		return raw, nil
	}
	return prepareCodexContext(saved.CWD)
}

func sameCodexContext(left, right string) bool {
	leftContext, leftErr := parseCodexContext(left)
	rightContext, rightErr := parseCodexContext(right)
	return leftErr == nil && rightErr == nil && leftContext.CWD == rightContext.CWD
}

var runCodexMCPList = func(ctx context.Context, sourceContext string) ([]byte, []byte, error) {
	resolved, err := parseCodexContext(sourceContext)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, resolved.Command, "mcp", "list", "--json")
	cmd.Dir = resolved.CWD
	cmd.Env = processenv.Merge(os.Environ(), map[string]string{"PATH": resolved.Path, "PWD": resolved.CWD})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// ListCodex returns Codex's resolved MCP catalog, including plugin-provided
// servers. It invokes Codex only for this explicit import operation.
func ListCodex(ctx context.Context, sourceContext string) ([]Candidate, error) {
	stdout, stderr, err := runCodexMCPList(ctx, sourceContext)
	if err != nil {
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("running codex mcp list --json: %s", detail)
	}
	return ParseCodexCatalog(stdout)
}

// ParseCodexCatalog converts Codex's resolved catalog into transport-only mcpx
// candidates. OAuth credentials remain owned by Codex and are never exported.
func ParseCodexCatalog(raw []byte) ([]Candidate, error) {
	var entries []codexCatalogEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parsing codex MCP catalog: %w", err)
	}

	seen := make(map[string]struct{}, len(entries))
	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return nil, fmt.Errorf("parsing codex MCP catalog: server name is empty")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("parsing codex MCP catalog: duplicate server %q", name)
		}
		seen[name] = struct{}{}

		candidate := Candidate{
			Name:           name,
			Enabled:        entry.Enabled,
			DisabledReason: strings.TrimSpace(entry.DisabledReason),
			Transport:      strings.TrimSpace(entry.Transport.Type),
			AuthStatus:     strings.TrimSpace(entry.AuthStatus),
		}
		candidate.Server, candidate.Supported, candidate.UnsupportedReason = importTransport(entry.Transport)
		candidates = append(candidates, candidate)
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates, nil
}

func importTransport(transport codexCatalogTransport) (config.ServerConfig, bool, string) {
	switch strings.TrimSpace(transport.Type) {
	case "stdio":
		command := strings.TrimSpace(transport.Command)
		if command == "" {
			return config.ServerConfig{}, false, "stdio transport has no command"
		}
		env := copyMap(transport.Env)
		return config.ServerConfig{
			Command: command,
			Args:    append([]string(nil), transport.Args...),
			CWD:     strings.TrimSpace(transport.CWD),
			Env:     env,
		}, true, ""
	case "streamable_http":
		url := strings.TrimSpace(transport.URL)
		if url == "" {
			return config.ServerConfig{}, false, "streamable_http transport has no URL"
		}
		headers := copyMap(transport.HTTPHeaders)
		for header, envVar := range transport.EnvHTTPHeaders {
			header = strings.TrimSpace(header)
			envVar = strings.TrimSpace(envVar)
			if header == "" || envVar == "" || hasHeader(headers, header) {
				continue
			}
			if headers == nil {
				headers = make(map[string]string)
			}
			headers[header] = "${" + envVar + "}"
		}
		if tokenEnv := strings.TrimSpace(transport.BearerTokenEnvVar); tokenEnv != "" && !hasHeader(headers, "Authorization") {
			if headers == nil {
				headers = make(map[string]string)
			}
			headers["Authorization"] = "Bearer ${" + tokenEnv + "}"
		}
		return config.ServerConfig{URL: url, Headers: headers}, true, ""
	default:
		kind := strings.TrimSpace(transport.Type)
		if kind == "" {
			kind = "unknown"
		}
		return config.ServerConfig{}, false, fmt.Sprintf("unsupported transport %q", kind)
	}
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func hasHeader(headers map[string]string, name string) bool {
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}
