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
	"github.com/lydakis/mcpx/internal/httpheaders"
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

type httpStatusError struct {
	statusCode int
	body       string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("unexpected HTTP status %d", e.statusCode)
}

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
			if shouldTreatSourceAsDirectMCPURL(source, err) {
				resolved, directErr := resolveDirectMCPURLSource(source, opts.Name)
				if directErr != nil {
					return ResolvedServer{}, directErr
				}
				return resolved, nil
			}
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
		if isHTTPURL(source) && shouldTreatParsedPayloadAsDirectMCPURL(source, payload, err) {
			resolved, directErr := resolveDirectMCPURLSource(source, opts.Name)
			if directErr != nil {
				return ResolvedServer{}, directErr
			}
			return resolved, nil
		}
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

	configPayload, err := decodeInstallLinkConfigPayload(rawConfig)
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

func decodeInstallLinkConfigPayload(raw string) ([]byte, error) {
	decodedJSON := decodeInstallLinkRawJSONPayload(raw)
	if len(decodedJSON) > 0 {
		return decodedJSON, nil
	}

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

func decodeInstallLinkRawJSONPayload(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	if payload := maybeJSONPayload(trimmed); payload != nil {
		return payload
	}

	unescaped, err := url.QueryUnescape(trimmed)
	if err != nil {
		return nil
	}
	return maybeJSONPayload(unescaped)
}

func maybeJSONPayload(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if !(strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) {
		return nil
	}
	return []byte(trimmed)
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &httpStatusError{
			statusCode: resp.StatusCode,
			body:       strings.TrimSpace(string(body)),
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func shouldTreatSourceAsDirectMCPURL(source string, err error) bool {
	if !looksLikeDirectMCPURL(source) {
		return false
	}
	if looksLikeSSEURL(source) && isLikelyStreamingFetchError(err) {
		return true
	}

	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}

	switch statusErr.statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusUnsupportedMediaType:
		return true
	case http.StatusBadRequest:
		trimmed := strings.TrimSpace(statusErr.body)
		if trimmed == "" {
			return true
		}
		var raw map[string]any
		if unmarshalErr := json.Unmarshal([]byte(trimmed), &raw); unmarshalErr == nil {
			if _, ok := raw["jsonrpc"]; ok {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func shouldTreatParsedPayloadAsDirectMCPURL(source string, payload []byte, parseErr error) bool {
	if !looksLikeDirectMCPURL(source) {
		return false
	}
	if parseErr == nil {
		return false
	}
	if payloadLooksLikeJSONRPC(payload) {
		return true
	}
	return looksLikeManifestDetectionError(parseErr)
}

func looksLikeManifestDetectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "payload is not a supported JSON or TOML server manifest") ||
		strings.Contains(msg, "manifest does not contain server definitions")
}

func looksLikeDirectMCPURL(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	urlPath := normalizeCandidateEndpointPath(u.Path)
	if urlPath == "" {
		return false
	}
	if looksLikeManifestPath(urlPath) {
		return false
	}
	return strings.HasSuffix(urlPath, "/mcp") || strings.HasSuffix(urlPath, "/sse")
}

func looksLikeSSEURL(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	urlPath := normalizeCandidateEndpointPath(u.Path)
	return strings.HasSuffix(urlPath, "/sse")
}

func normalizeCandidateEndpointPath(raw string) string {
	path := strings.ToLower(strings.TrimSpace(raw))
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	return path
}

func isLikelyStreamingFetchError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return true
	}
	type timeoutError interface {
		error
		Timeout() bool
	}
	var timeoutErr timeoutError
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

func payloadLooksLikeJSONRPC(payload []byte) bool {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return false
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return false
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return false
	}
	_, ok = root["jsonrpc"]
	return ok
}

func looksLikeManifestPath(urlPath string) bool {
	trimmed := strings.Trim(strings.ToLower(strings.TrimSpace(urlPath)), "/")
	if trimmed == "" {
		return false
	}

	last := trimmed
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	if last == "" {
		return false
	}

	if strings.Contains(last, "manifest") {
		return true
	}
	for _, suffix := range []string{".json", ".toml", ".yaml", ".yml"} {
		if strings.HasSuffix(last, suffix) {
			return true
		}
	}
	return false
}

func resolveDirectMCPURLSource(source, overrideName string) (ResolvedServer, error) {
	name := strings.TrimSpace(overrideName)
	if name == "" {
		name = defaultServerNameFromURL(source)
	}
	if name == "" {
		return ResolvedServer{}, fmt.Errorf("unable to infer server name from URL %q; pass --name", source)
	}

	resolved := ResolvedServer{
		Name: name,
		Server: config.ServerConfig{
			URL: strings.TrimSpace(source),
		},
	}
	if err := validateResolvedServer(resolved.Name, resolved.Server); err != nil {
		return ResolvedServer{}, err
	}
	return resolved, nil
}

func defaultServerNameFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return ""
	}

	trimmedHost := strings.TrimPrefix(host, "mcp.")
	if name := sanitizeServerNameCandidate(firstHostLabel(trimmedHost)); name != "" {
		return name
	}
	if name := sanitizeServerNameCandidate(firstHostLabel(host)); name != "" {
		return name
	}
	return sanitizeServerNameCandidate(host)
}

func firstHostLabel(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if idx := strings.Index(host, "."); idx > 0 {
		return host[:idx]
	}
	return host
}

func sanitizeServerNameCandidate(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	lastUnderscore := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	return out
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

	if looksLikeNamedServerConfigMap(root) {
		named, err := decodeNamedServerMap(root)
		if err == nil && len(named) > 0 {
			return parsedServerSet{named: named}, true, nil
		}
		if err != nil {
			return parsedServerSet{}, true, err
		}
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

	srv.Command, srv.Args, err = decodeCommandAndArgs(target)
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

	srv.Headers, err = decodeHeaders(target)
	if err != nil {
		return config.ServerConfig{}, err
	}

	transport, hasTransport, err := decodeTransport(target, &srv)
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

func decodeCommandAndArgs(raw map[string]any) (string, []string, error) {
	args, err := decodeOptionalStringSlice(raw, "args")
	if err != nil {
		return "", nil, err
	}

	value, ok := raw["command"]
	if !ok {
		return "", args, nil
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), args, nil
	case []any:
		if len(typed) == 0 {
			return "", nil, fmt.Errorf("command array cannot be empty")
		}
		parts := make([]string, 0, len(typed))
		for i, item := range typed {
			text, ok := item.(string)
			if !ok {
				return "", nil, fmt.Errorf("command[%d] must be a string", i)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return "", nil, fmt.Errorf("command[%d] cannot be empty", i)
			}
			parts = append(parts, text)
		}

		command := parts[0]
		commandArgs := parts[1:]
		if len(commandArgs) == 0 {
			return command, args, nil
		}
		combinedArgs := make([]string, 0, len(commandArgs)+len(args))
		combinedArgs = append(combinedArgs, commandArgs...)
		combinedArgs = append(combinedArgs, args...)
		return command, combinedArgs, nil
	default:
		return "", nil, fmt.Errorf("command must be a string")
	}
}

func decodeHeaders(raw map[string]any) (map[string]string, error) {
	headers, err := decodeOptionalStringMap(raw, "headers")
	if err != nil {
		return nil, err
	}

	for _, key := range []string{"httpHeaders", "http_headers"} {
		alias, aliasErr := decodeOptionalStringMap(raw, key)
		if aliasErr != nil {
			return nil, aliasErr
		}
		headers = httpheaders.Merge(headers, alias, false)
	}

	requestInitHeaders, err := decodeRequestInitHeaders(raw)
	if err != nil {
		return nil, err
	}
	headers = httpheaders.Merge(headers, requestInitHeaders, false)
	return headers, nil
}

func decodeRequestInitHeaders(raw map[string]any) (map[string]string, error) {
	for _, key := range []string{"requestInit", "request_init"} {
		requestInit, err := decodeOptionalObject(raw, key)
		if err != nil {
			return nil, err
		}
		if requestInit == nil {
			continue
		}
		headers, err := decodeOptionalStringMap(requestInit, "headers")
		if err != nil {
			return nil, err
		}
		return headers, nil
	}
	return nil, nil
}

func decodeTransport(raw map[string]any, srv *config.ServerConfig) (string, bool, error) {
	value, ok := raw["transport"]
	if ok {
		transport, err := decodeTransportValue(value, srv)
		if err != nil {
			return "", false, err
		}
		return transport, true, nil
	}

	value, ok = raw["type"]
	if !ok {
		return "", false, nil
	}
	transport, err := decodeTransportValue(value, srv)
	if err != nil {
		return "", false, fmt.Errorf("type: %w", err)
	}
	return transport, true, nil
}

func decodeTransportValue(value any, srv *config.ServerConfig) (string, error) {
	switch typed := value.(type) {
	case string:
		return normalizeTransport(typed), nil
	case map[string]any:
		return decodeTransportObject(typed, srv)
	case []any:
		if len(typed) == 0 {
			return "", fmt.Errorf("transport array cannot be empty")
		}
		first := typed[0]
		switch firstTyped := first.(type) {
		case string:
			return normalizeTransport(firstTyped), nil
		case map[string]any:
			return decodeTransportObject(firstTyped, srv)
		default:
			return "", fmt.Errorf("transport array must contain strings or objects")
		}
	default:
		return "", fmt.Errorf("transport must be a string, object, or array")
	}
}

func decodeTransportObject(raw map[string]any, srv *config.ServerConfig) (string, error) {
	transportType, err := decodeOptionalString(raw, "type")
	if err != nil {
		return "", err
	}
	if transportType == "" {
		transportType, err = decodeOptionalString(raw, "transport")
		if err != nil {
			return "", err
		}
	}

	if srv != nil {
		command, args, err := decodeCommandAndArgs(raw)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(srv.Command) == "" && strings.TrimSpace(command) != "" {
			srv.Command = command
			if len(srv.Args) == 0 {
				srv.Args = args
			}
		}

		urlValue, err := decodeOptionalString(raw, "url")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(srv.URL) == "" {
			srv.URL = urlValue
		}

		headers, err := decodeHeaders(raw)
		if err != nil {
			return "", err
		}
		srv.Headers = httpheaders.Merge(srv.Headers, headers, false)
	}

	if transportType != "" {
		return normalizeTransport(transportType), nil
	}
	if srv != nil {
		if strings.TrimSpace(srv.Command) != "" && strings.TrimSpace(srv.URL) == "" {
			return "stdio", nil
		}
		if strings.TrimSpace(srv.URL) != "" {
			return "http", nil
		}
	}
	return "", fmt.Errorf("transport object must include type")
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

func decodeOptionalObject(raw map[string]any, key string) (map[string]any, error) {
	value, ok := raw[key]
	if !ok {
		return nil, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return obj, nil
}

func looksLikeServerConfigMap(raw map[string]any) bool {
	serverKeys := []string{
		"command",
		"args",
		"env",
		"url",
		"headers",
		"httpHeaders",
		"http_headers",
		"requestInit",
		"request_init",
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

func looksLikeNamedServerConfigMap(raw map[string]any) bool {
	if len(raw) == 0 {
		return false
	}
	for _, value := range raw {
		obj, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if looksLikeServerConfigMap(obj) {
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
