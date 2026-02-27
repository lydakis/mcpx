package bootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/lydakis/mcpx/internal/config"
)

type ResolveOptions struct {
	Name     string
	FetchURL func(ctx context.Context, source string) ([]byte, error)
	ReadFile func(path string) ([]byte, error)
}

type ResolvedServer struct {
	Name   string
	Server config.ServerConfig
}

type parsedServerSet struct {
	named   map[string]config.ServerConfig
	unnamed *config.ServerConfig
}

type tomlServerManifest struct {
	MCPServers map[string]config.ServerConfig `toml:"mcpServers"`
	Servers    map[string]config.ServerConfig `toml:"servers"`
}

type resolveErrorKind uint8

const (
	resolveErrorSourceAccess resolveErrorKind = iota + 1
)

type resolveError struct {
	kind resolveErrorKind
	err  error
}

func (e *resolveError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *resolveError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func wrapResolveSourceAccessError(err error) error {
	if err == nil {
		return nil
	}
	return &resolveError{kind: resolveErrorSourceAccess, err: err}
}

// IsSourceAccessError reports whether err was caused by a fetch/read failure
// while resolving a source manifest or install payload.
func IsSourceAccessError(err error) bool {
	var typed *resolveError
	if !errors.As(err, &typed) {
		return false
	}
	return typed.kind == resolveErrorSourceAccess
}

func Resolve(ctx context.Context, source string, opts ResolveOptions) (ResolvedServer, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return ResolvedServer{}, fmt.Errorf("missing source")
	}

	if isInstallLinkSource(source) {
		resolved, err := resolveInstallLink(source, opts.Name)
		if err != nil {
			return ResolvedServer{}, err
		}
		if err := validateResolvedServer(resolved.Name, resolved.Server); err != nil {
			return ResolvedServer{}, err
		}
		return resolved, nil
	}

	var payload []byte
	var err error
	if isHTTPURL(source) {
		fetch := opts.FetchURL
		if fetch == nil {
			fetch = fetchSourceURL
		}
		payload, err = fetch(ctx, source)
		if err != nil {
			return ResolvedServer{}, wrapResolveSourceAccessError(fmt.Errorf("fetching %q: %w", source, err))
		}
	} else {
		readFile := opts.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		payload, err = readFile(source)
		if err != nil {
			return ResolvedServer{}, wrapResolveSourceAccessError(fmt.Errorf("reading %q: %w", source, err))
		}
	}

	set, err := parseServerPayload(payload)
	if err != nil {
		return ResolvedServer{}, fmt.Errorf("parsing %q: %w", source, err)
	}

	name, server, err := selectResolvedServer(set, opts.Name)
	if err != nil {
		return ResolvedServer{}, err
	}
	if err := validateResolvedServer(name, server); err != nil {
		return ResolvedServer{}, err
	}

	return ResolvedServer{Name: name, Server: server}, nil
}

func resolveInstallLink(source, overrideName string) (ResolvedServer, error) {
	u, err := url.Parse(source)
	if err != nil {
		return ResolvedServer{}, fmt.Errorf("invalid install link: %w", err)
	}

	query := u.Query()
	rawConfig := strings.TrimSpace(query.Get("config"))
	if rawConfig == "" {
		return ResolvedServer{}, fmt.Errorf("install link is missing config payload")
	}

	name := strings.TrimSpace(overrideName)
	if name == "" {
		name = strings.TrimSpace(query.Get("name"))
	}

	configPayload, err := decodeBase64ConfigPayload(rawConfig)
	if err != nil {
		return ResolvedServer{}, fmt.Errorf("invalid install-link config payload: %w", err)
	}

	set, err := parseServerPayload(configPayload)
	if err != nil {
		return ResolvedServer{}, fmt.Errorf("parsing install-link payload: %w", err)
	}

	selectedName, server, err := selectResolvedServer(set, name)
	if err != nil {
		return ResolvedServer{}, err
	}
	return ResolvedServer{Name: selectedName, Server: server}, nil
}

func isInstallLinkSource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if strings.TrimSpace(u.Query().Get("config")) == "" {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(u.Path))
	if strings.Contains(path, "/mcp/install") {
		return true
	}
	if strings.EqualFold(u.Scheme, "cursor") {
		return true
	}
	return false
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

func decodeBase64ConfigPayload(raw string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}

	trimmed := strings.TrimSpace(raw)
	candidates := []string{trimmed}
	if strings.Contains(trimmed, " ") {
		candidates = append(candidates, strings.ReplaceAll(trimmed, " ", "+"))
	}

	var lastErr error
	for _, candidate := range candidates {
		for _, enc := range encodings {
			decoded, err := enc.DecodeString(candidate)
			if err == nil {
				return decoded, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("decode failed")
	}
	return nil, lastErr
}

func fetchSourceURL(ctx context.Context, source string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseServerPayload(payload []byte) (parsedServerSet, error) {
	if set, ok, err := parseJSONServerPayload(payload); ok {
		return set, err
	}

	if set, ok, err := parseTOMLServerPayload(payload); ok {
		return set, err
	}

	return parsedServerSet{}, fmt.Errorf("payload is not a supported JSON or TOML server manifest")
}

func parseTOMLServerPayload(payload []byte) (parsedServerSet, bool, error) {
	var decoded tomlServerManifest
	if err := toml.Unmarshal(payload, &decoded); err != nil {
		return parsedServerSet{}, false, nil
	}

	if len(decoded.MCPServers) > 0 {
		return parsedServerSet{named: decoded.MCPServers}, true, nil
	}
	if len(decoded.Servers) > 0 {
		return parsedServerSet{named: decoded.Servers}, true, nil
	}

	return parsedServerSet{}, true, fmt.Errorf("manifest does not contain server definitions")
}

func parseJSONServerPayload(payload []byte) (parsedServerSet, bool, error) {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return parsedServerSet{}, false, nil
	}

	root, ok := decoded.(map[string]any)
	if !ok {
		return parsedServerSet{}, true, fmt.Errorf("manifest root must be a JSON object")
	}

	if raw, ok := root["mcpServers"]; ok {
		named, err := decodeNamedServerMap(raw)
		if err != nil {
			return parsedServerSet{}, true, err
		}
		return parsedServerSet{named: named}, true, nil
	}

	if raw, ok := root["servers"]; ok {
		named, err := decodeNamedServerMap(raw)
		if err != nil {
			return parsedServerSet{}, true, err
		}
		return parsedServerSet{named: named}, true, nil
	}

	if looksLikeServerConfigMap(root) {
		server, err := decodeServerConfig(root)
		if err != nil {
			return parsedServerSet{}, true, err
		}
		return parsedServerSet{unnamed: &server}, true, nil
	}

	named, err := decodeNamedServerMap(root)
	if err == nil && len(named) > 0 {
		return parsedServerSet{named: named}, true, nil
	}

	return parsedServerSet{}, true, fmt.Errorf("manifest does not contain server definitions")
}

func decodeNamedServerMap(raw any) (map[string]config.ServerConfig, error) {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("server map must be an object")
	}

	servers := make(map[string]config.ServerConfig, len(root))
	for name, value := range root {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("server name cannot be empty")
		}

		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("server %q definition must be an object", name)
		}

		server, err := decodeServerConfig(obj)
		if err != nil {
			return nil, fmt.Errorf("server %q: %w", name, err)
		}
		servers[name] = server
	}

	return servers, nil
}

func decodeServerConfig(raw map[string]any) (config.ServerConfig, error) {
	target := raw
	if installRaw, ok := raw["install"]; ok {
		installMap, ok := installRaw.(map[string]any)
		if !ok {
			return config.ServerConfig{}, fmt.Errorf("install must be an object")
		}
		target = installMap
	}

	srv := config.ServerConfig{}
	var err error

	srv.Command, err = decodeOptionalString(target, "command")
	if err != nil {
		return config.ServerConfig{}, err
	}

	srv.Args, err = decodeOptionalStringSlice(target, "args")
	if err != nil {
		return config.ServerConfig{}, err
	}

	srv.Env, err = decodeOptionalStringMap(target, "env")
	if err != nil {
		return config.ServerConfig{}, err
	}

	srv.URL, err = decodeOptionalString(target, "url")
	if err != nil {
		return config.ServerConfig{}, err
	}

	srv.Headers, err = decodeOptionalStringMap(target, "headers")
	if err != nil {
		return config.ServerConfig{}, err
	}

	transport, hasTransport, err := decodeTransport(target)
	if err != nil {
		return config.ServerConfig{}, err
	}
	if hasTransport {
		switch transport {
		case "stdio":
			if strings.TrimSpace(srv.Command) == "" {
				return config.ServerConfig{}, fmt.Errorf("transport %q requires command", transport)
			}
		case "http", "https", "sse", "streamablehttp":
			if strings.TrimSpace(srv.URL) == "" {
				return config.ServerConfig{}, fmt.Errorf("transport %q requires url", transport)
			}
		default:
			return config.ServerConfig{}, fmt.Errorf("unsupported transport %q", transport)
		}
	}

	return srv, nil
}

func decodeTransport(raw map[string]any) (string, bool, error) {
	value, ok := raw["transport"]
	if !ok {
		return "", false, nil
	}

	switch typed := value.(type) {
	case string:
		return normalizeTransport(typed), true, nil
	case []any:
		if len(typed) == 0 {
			return "", false, fmt.Errorf("transport array cannot be empty")
		}
		first, ok := typed[0].(string)
		if !ok {
			return "", false, fmt.Errorf("transport array must contain strings")
		}
		return normalizeTransport(first), true, nil
	default:
		return "", false, fmt.Errorf("transport must be a string or string array")
	}
}

func normalizeTransport(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

func decodeOptionalString(raw map[string]any, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(text), nil
}

func decodeOptionalStringSlice(raw map[string]any, key string) ([]string, error) {
	value, ok := raw[key]
	if !ok {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}

	out := make([]string, 0, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", key, i)
		}
		out = append(out, text)
	}
	return out, nil
}

func decodeOptionalStringMap(raw map[string]any, key string) (map[string]string, error) {
	value, ok := raw[key]
	if !ok {
		return nil, nil
	}
	items, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object<string,string>", key)
	}

	out := make(map[string]string, len(items))
	for k, v := range items {
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", key, k)
		}
		out[k] = text
	}
	return out, nil
}

func looksLikeServerConfigMap(raw map[string]any) bool {
	serverKeys := []string{
		"command",
		"args",
		"env",
		"url",
		"headers",
		"transport",
		"install",
	}
	for _, key := range serverKeys {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

func selectResolvedServer(set parsedServerSet, requestedName string) (string, config.ServerConfig, error) {
	requestedName = strings.TrimSpace(requestedName)

	if len(set.named) > 0 {
		if requestedName != "" {
			if srv, ok := set.named[requestedName]; ok {
				return requestedName, srv, nil
			}
			if len(set.named) == 1 {
				_, only := onlyNamedEntry(set.named)
				return requestedName, only, nil
			}
			return "", config.ServerConfig{}, fmt.Errorf("server %q not found in manifest (available: %s)", requestedName, strings.Join(sortedNames(set.named), ", "))
		}
		if len(set.named) == 1 {
			name, srv := onlyNamedEntry(set.named)
			return name, srv, nil
		}
		return "", config.ServerConfig{}, fmt.Errorf("manifest includes multiple servers (%s); pass --name to select one", strings.Join(sortedNames(set.named), ", "))
	}

	if set.unnamed != nil {
		if requestedName == "" {
			return "", config.ServerConfig{}, fmt.Errorf("manifest defines an unnamed server; pass --name")
		}
		return requestedName, *set.unnamed, nil
	}

	return "", config.ServerConfig{}, fmt.Errorf("manifest does not include any servers")
}

func onlyNamedEntry(servers map[string]config.ServerConfig) (string, config.ServerConfig) {
	for name, srv := range servers {
		return name, srv
	}
	return "", config.ServerConfig{}
}

func sortedNames(servers map[string]config.ServerConfig) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateResolvedServer(name string, server config.ServerConfig) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("server name cannot be empty")
	}

	err := config.ValidateForCurrentEnv(&config.Config{
		Servers: map[string]config.ServerConfig{
			name: server,
		},
	})
	if err != nil {
		return err
	}
	return nil
}
