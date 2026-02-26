package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lydakis/mcpx/internal/cache"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/paths"
	"github.com/lydakis/mcpx/internal/response"
	"github.com/mark3labs/mcp-go/mcp"
)

var (
	poolListTools = func(ctx context.Context, pool *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
		return pool.ListTools(ctx, server)
	}
	poolToolInfoByName = func(ctx context.Context, pool *mcppool.Pool, server, tool string) (*mcppool.ToolInfo, error) {
		return pool.ToolInfoByName(ctx, server, tool)
	}
	poolCallToolWithInfo = func(ctx context.Context, pool *mcppool.Pool, server string, info *mcppool.ToolInfo, args json.RawMessage) (*mcp.CallToolResult, error) {
		return pool.CallToolWithInfo(ctx, server, info, args)
	}
	cacheGet         = cache.Get
	cacheGetMetadata = cache.GetMetadata
	cachePut         = cache.Put
	poolResetFn      = func(pool *mcppool.Pool, cfg *config.Config) {
		if pool != nil {
			pool.Reset(cfg)
		}
	}
	keepaliveStopFn = func(ka *Keepalive) {
		if ka != nil {
			ka.Stop()
		}
	}
	loadConfigFn     = config.Load
	mergeFallbackFn  = config.MergeFallbackServersForCWD
	validateConfigFn = config.Validate
	signalShutdownFn = func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}
)

const codexAppsServerName = "codex_apps"

// Run starts the daemon process. Called when argv[1] == "__daemon".
func Run() error {
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return fmt.Errorf("creating runtime dir: %w", err)
	}

	cfg, err := loadValidatedConfigForCWD("")
	if err != nil {
		return err
	}

	nonce, err := readOrCreateNonce()
	if err != nil {
		return fmt.Errorf("nonce setup: %w", err)
	}

	pool := mcppool.New(cfg)
	defer pool.CloseAll()

	ka := NewKeepalive(pool)
	ka.SetOnAllIdle(signalShutdownFn)
	defer ka.Stop()

	handler := newRuntimeRequestHandler(cfg, pool, ka)

	srv := ipc.NewServer(paths.SocketPath(), nonce, handler.handle)
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Stop()

	fmt.Fprintf(os.Stderr, "mcpx daemon: listening on %s\n", paths.SocketPath())

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(os.Stderr, "mcpx daemon: shutting down")
	return nil
}

type runtimeRequestHandler struct {
	mu        sync.RWMutex
	activeCWD string
	cfgHash   string
	cfg       *config.Config
	pool      *mcppool.Pool
	ka        *Keepalive
}

func newRuntimeRequestHandler(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *runtimeRequestHandler {
	cfgHash, _ := configFingerprint(cfg)
	return &runtimeRequestHandler{
		cfgHash: cfgHash,
		cfg:     cfg,
		pool:    pool,
		ka:      ka,
	}
}

func (h *runtimeRequestHandler) handle(ctx context.Context, req *ipc.Request) *ipc.Response {
	if req == nil {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "nil request"}
	}

	if !requestNeedsRuntimeConfig(req) {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return dispatch(ctx, h.cfg, h.pool, h.ka, req)
	}

	normalizedCWD := strings.TrimSpace(req.CWD)

	h.mu.RLock()
	if normalizedCWD == strings.TrimSpace(h.activeCWD) {
		// Safe to dispatch concurrently for same-CWD requests.
		// mcppool serializes per-connection RPCs to prevent stdio frame interleaving.
		defer h.mu.RUnlock()
		return dispatch(ctx, h.cfg, h.pool, h.ka, req)
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := syncRuntimeConfigForRequest(normalizedCWD, &h.activeCWD, &h.cfgHash, &h.cfg, h.pool, h.ka); err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: err.Error()}
	}

	return dispatch(ctx, h.cfg, h.pool, h.ka, req)
}

func requestNeedsRuntimeConfig(req *ipc.Request) bool {
	if req == nil {
		return false
	}
	switch req.Type {
	case "list_servers", "list_tools", "tool_schema", "call_tool":
		return true
	default:
		return false
	}
}

func syncRuntimeConfigForRequest(reqCWD string, activeCWD, cfgHash *string, cfg **config.Config, pool *mcppool.Pool, ka *Keepalive) error {
	normalized := strings.TrimSpace(reqCWD)
	if normalized == strings.TrimSpace(*activeCWD) {
		return nil
	}

	nextCfg, err := loadValidatedConfigForCWD(normalized)
	if err != nil {
		return err
	}

	nextHash, err := configFingerprint(nextCfg)
	if err != nil {
		return err
	}

	if nextHash != strings.TrimSpace(*cfgHash) {
		keepaliveStopFn(ka)
		poolResetFn(pool, nextCfg)
	}
	*cfg = nextCfg
	*cfgHash = nextHash
	*activeCWD = normalized
	return nil
}

func configFingerprint(cfg *config.Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("fingerprinting config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func loadValidatedConfigForCWD(cwd string) (*config.Config, error) {
	cfg, err := loadConfigFn()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if ferr := mergeFallbackFn(cfg, cwd); ferr != nil {
		fmt.Fprintf(os.Stderr, "mcpx daemon: warning: failed to load fallback MCP server config: %v\n", ferr)
	}
	if verr := validateConfigFn(cfg); verr != nil {
		return nil, fmt.Errorf("invalid config: %w", verr)
	}
	return cfg, nil
}

func dispatch(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, req *ipc.Request) *ipc.Response {
	switch req.Type {
	case "list_servers":
		return listServers(ctx, cfg, pool, ka)
	case "list_tools":
		return listTools(ctx, cfg, pool, ka, req.Server, req.Verbose)
	case "tool_schema":
		return toolSchema(ctx, cfg, pool, ka, req.Server, req.Tool)
	case "call_tool":
		return callTool(ctx, cfg, pool, ka, req.Server, req.Tool, req.Args, req.Cache, req.Verbose)
	case "shutdown":
		go signalShutdownFn()
		return &ipc.Response{Content: []byte("shutting down\n")}
	default:
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown request type: %s", req.Type)}
	}
}

type serverRoute struct {
	backend       string
	configServer  string
	virtualName   string
	virtualPrefix string
}

func (r serverRoute) isVirtual() bool {
	return strings.TrimSpace(r.virtualPrefix) != ""
}

func listServers(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *ipc.Response {
	names := make(map[string]struct{}, len(cfg.Servers))
	for name := range cfg.Servers {
		if name == codexAppsServerName {
			continue
		}
		names[name] = struct{}{}
	}

	var warn string
	if _, ok := cfg.Servers[codexAppsServerName]; ok {
		tools, err := listCodexAppsTools(ctx, pool, ka)
		if err != nil {
			warn = fmt.Sprintf("mcpx: warning: failed to enumerate codex apps: %v", err)
		} else {
			for name := range codexVirtualServerMap(tools) {
				if strings.TrimSpace(name) == "" {
					continue
				}
				if _, exists := cfg.Servers[name]; exists {
					continue
				}
				names[name] = struct{}{}
			}
		}
	}

	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var out []byte
	for _, name := range sorted {
		out = append(out, []byte(name+"\n")...)
	}
	return &ipc.Response{Content: out, Stderr: warn}
}

type toolListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func listTools(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, verbose bool) *ipc.Response {
	route, routeTools, found, err := resolveServerRoute(ctx, cfg, pool, ka, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	tools := routeTools
	if !route.isVirtual() {
		ka.Begin(route.backend)
		defer ka.End(route.backend)

		tools, err = poolListTools(ctx, pool, route.backend)
		if err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("listing tools: %v", err)}
		}
	} else {
		tools = filterToolsByPrefix(tools, route.virtualPrefix)
	}

	displayNames := make(map[string]string, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if _, exists := displayNames[name]; exists {
			continue
		}
		desc := strings.TrimSpace(t.Description)
		if !verbose {
			desc = summarizeToolDescription(desc)
		}
		displayNames[name] = desc
	}

	names := make([]string, 0, len(displayNames))
	for name := range displayNames {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]toolListEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, toolListEntry{
			Name:        name,
			Description: strings.TrimSpace(displayNames[name]),
		})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("encoding tool list: %v", err)}
	}
	data = append(data, '\n')
	return &ipc.Response{Content: data}
}

const shortToolDescriptionMaxLen = 120

func summarizeToolDescription(desc string) string {
	if desc == "" {
		return ""
	}

	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		desc = strings.Join(strings.Fields(line), " ")
		break
	}

	if desc == "" {
		return ""
	}
	if len(desc) <= shortToolDescriptionMaxLen {
		return desc
	}
	return strings.TrimSpace(desc[:shortToolDescriptionMaxLen-3]) + "..."
}

func toolSchema(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string) *ipc.Response {
	route, routeTools, found, err := resolveServerRoute(ctx, cfg, pool, ka, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	var info *mcppool.ToolInfo
	if route.isVirtual() {
		toolInfo, ok := toolInfoByNameAndPrefix(routeTools, tool, route.virtualPrefix)
		if !ok {
			return &ipc.Response{
				ExitCode: ipc.ExitUsageErr,
				Stderr:   fmt.Sprintf("tool %s not found on server %s", tool, server),
			}
		}
		info = toolInfo
	} else {
		ka.Begin(route.backend)
		defer ka.End(route.backend)

		info, err = poolToolInfoByName(ctx, pool, route.backend, tool)
		if err != nil {
			return &ipc.Response{
				ExitCode: classifyToolLookupError(err),
				Stderr:   fmt.Sprintf("getting schema: %v", err),
			}
		}
	}

	payload := map[string]any{
		"name":        info.Name,
		"description": info.Description,
	}

	if len(info.InputSchema) > 0 {
		var in any
		if err := json.Unmarshal(info.InputSchema, &in); err == nil {
			payload["input_schema"] = in
		}
	}
	if len(info.OutputSchema) > 0 {
		var out any
		if err := json.Unmarshal(info.OutputSchema, &out); err == nil {
			payload["output_schema"] = out
		}
	}

	data, _ := json.MarshalIndent(payload, "", "  ")
	data = append(data, '\n')
	return &ipc.Response{Content: data}
}

func callTool(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string, args json.RawMessage, reqCache *time.Duration, verbose bool) *ipc.Response {
	route, found, err := resolveServerRouteForTool(ctx, cfg, pool, ka, server, tool)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}
	scfg, ok := cfg.Servers[route.configServer]
	if !ok {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	ka.Begin(route.backend)
	defer ka.End(route.backend)

	cacheTTL, shouldCache, err := effectiveCacheTTL(scfg, tool, reqCache)
	if err != nil {
		return &ipc.Response{
			ExitCode: ipc.ExitInternal,
			Stderr:   fmt.Sprintf("cache configuration error: %v", err),
		}
	}
	var logs []string
	if shouldCache {
		if out, exitCode, ok := cacheGet(server, tool, args); ok {
			if verbose {
				if age, ttl, ok := cacheGetMetadata(server, tool, args); ok {
					logs = append(logs, fmt.Sprintf("mcpx: cache hit (age=%s ttl=%s)", age, ttl))
				} else {
					logs = append(logs, "mcpx: cache hit")
				}
			}
			return &ipc.Response{Content: out, ExitCode: exitCode, Stderr: joinLogs(logs)}
		}
		if verbose {
			logs = append(logs, "mcpx: cache miss")
		}
	}

	info := &mcppool.ToolInfo{Name: tool}
	if route.isVirtual() && !toolMatchesPrefix(tool, route.virtualPrefix) {
		return &ipc.Response{
			ExitCode: ipc.ExitUsageErr,
			Stderr:   fmt.Sprintf("resolving tool: tool %s not found on server %s", tool, server),
		}
	}
	if pool != nil {
		resolvedInfo, err := poolToolInfoByName(ctx, pool, route.backend, tool)
		if err != nil {
			return &ipc.Response{
				ExitCode: classifyToolLookupError(err),
				Stderr:   fmt.Sprintf("resolving tool: %v", err),
			}
		}
		if route.isVirtual() && (resolvedInfo == nil || !toolMatchesPrefix(resolvedInfo.Name, route.virtualPrefix)) {
			return &ipc.Response{
				ExitCode: ipc.ExitUsageErr,
				Stderr:   fmt.Sprintf("resolving tool: tool %s not found on server %s", tool, server),
			}
		}
		if resolvedInfo != nil {
			info = resolvedInfo
		}
	}
	cacheTool := tool
	if info.Name != "" {
		cacheTool = info.Name
	}

	result, err := poolCallToolWithInfo(ctx, pool, route.backend, info, args)
	if err != nil {
		return &ipc.Response{
			ExitCode: classifyCallToolError(err),
			Stderr:   fmt.Sprintf("calling tool: %v", err),
		}
	}

	out, exitCode := response.Unwrap(result)
	if shouldCache && exitCode == ipc.ExitOK {
		_ = cachePut(server, cacheTool, args, out, exitCode, cacheTTL)
		if verbose {
			logs = append(logs, fmt.Sprintf("mcpx: cache store (ttl=%s)", cacheTTL))
		}
	}
	return &ipc.Response{Content: out, ExitCode: exitCode, Stderr: joinLogs(logs)}
}

func resolveServerRoute(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, requested string) (serverRoute, []mcppool.ToolInfo, bool, error) {
	if cfg != nil && requested != codexAppsServerName {
		if _, ok := cfg.Servers[requested]; ok {
			return serverRoute{
				backend:      requested,
				configServer: requested,
			}, nil, true, nil
		}
	}

	if cfg == nil {
		return serverRoute{}, nil, false, nil
	}
	if _, ok := cfg.Servers[codexAppsServerName]; !ok {
		return serverRoute{}, nil, false, nil
	}

	tools, err := listCodexAppsTools(ctx, pool, ka)
	if err != nil {
		return serverRoute{}, nil, false, err
	}
	prefix, ok := codexVirtualServerMap(tools)[requested]
	if !ok {
		return serverRoute{}, nil, false, nil
	}

	return serverRoute{
		backend:       codexAppsServerName,
		configServer:  codexAppsServerName,
		virtualName:   requested,
		virtualPrefix: prefix,
	}, tools, true, nil
}

func resolveServerRouteForTool(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, requested, tool string) (serverRoute, bool, error) {
	if cfg != nil && requested != codexAppsServerName {
		if _, ok := cfg.Servers[requested]; ok {
			return serverRoute{
				backend:      requested,
				configServer: requested,
			}, true, nil
		}
	}
	if cfg == nil {
		return serverRoute{}, false, nil
	}
	if _, ok := cfg.Servers[codexAppsServerName]; !ok {
		return serverRoute{}, false, nil
	}

	prefix, hasPrefix := connectorPrefixFromToolName(tool)
	if hasPrefix && normalizeCodexVirtualServerName(prefix) == requested {
		return serverRoute{
			backend:       codexAppsServerName,
			configServer:  codexAppsServerName,
			virtualName:   requested,
			virtualPrefix: prefix,
		}, true, nil
	}

	route, _, found, err := resolveServerRoute(ctx, cfg, pool, ka, requested)
	if err != nil {
		return serverRoute{}, false, err
	}
	if !found || !route.isVirtual() {
		return serverRoute{}, false, nil
	}
	return route, true, nil
}

func listCodexAppsTools(ctx context.Context, pool *mcppool.Pool, ka *Keepalive) ([]mcppool.ToolInfo, error) {
	if ka != nil {
		ka.Begin(codexAppsServerName)
		defer ka.End(codexAppsServerName)
	}
	return poolListTools(ctx, pool, codexAppsServerName)
}

func codexVirtualServerMap(tools []mcppool.ToolInfo) map[string]string {
	prefixes := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		prefix, ok := connectorPrefixFromToolName(tool.Name)
		if !ok {
			continue
		}
		prefixes[prefix] = struct{}{}
	}

	sortedPrefixes := make([]string, 0, len(prefixes))
	for prefix := range prefixes {
		sortedPrefixes = append(sortedPrefixes, prefix)
	}
	sort.Strings(sortedPrefixes)

	out := make(map[string]string, len(sortedPrefixes))
	for _, prefix := range sortedPrefixes {
		base := normalizeCodexVirtualServerName(prefix)
		if base == "" {
			continue
		}
		name := base
		if existingPrefix, exists := out[name]; exists && existingPrefix != prefix {
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s_%d", base, i)
				if _, inUse := out[candidate]; inUse {
					continue
				}
				name = candidate
				break
			}
		}
		out[name] = prefix
	}
	return out
}

func connectorPrefixFromToolName(toolName string) (string, bool) {
	sep := strings.Index(toolName, "_")
	if sep <= 0 {
		return "", false
	}
	prefix := strings.TrimSpace(toolName[:sep])
	if prefix == "" {
		return "", false
	}
	return prefix, true
}

func normalizeCodexVirtualServerName(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return ""
	}

	var b strings.Builder
	prevUnderscore := false
	for _, r := range prefix {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if b.Len() == 0 || prevUnderscore {
			continue
		}
		b.WriteByte('_')
		prevUnderscore = true
	}
	return strings.Trim(b.String(), "_")
}

func toolMatchesPrefix(name, prefix string) bool {
	return strings.HasPrefix(name, prefix+"_")
}

func filterToolsByPrefix(tools []mcppool.ToolInfo, prefix string) []mcppool.ToolInfo {
	filtered := make([]mcppool.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if toolMatchesPrefix(tool.Name, prefix) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func canonicalToolNameForPrefix(tools []mcppool.ToolInfo, requested, prefix string) (string, bool) {
	for _, tool := range tools {
		if !toolMatchesPrefix(tool.Name, prefix) {
			continue
		}
		if tool.Name == requested {
			return tool.Name, true
		}
	}
	return "", false
}

func toolInfoByNameAndPrefix(tools []mcppool.ToolInfo, requested, prefix string) (*mcppool.ToolInfo, bool) {
	canonical, ok := canonicalToolNameForPrefix(tools, requested, prefix)
	if !ok {
		return nil, false
	}
	for i := range tools {
		if tools[i].Name == canonical {
			toolCopy := tools[i]
			return &toolCopy, true
		}
	}
	return nil, false
}

func effectiveCacheTTL(scfg config.ServerConfig, tool string, reqCache *time.Duration) (time.Duration, bool, error) {
	if reqCache != nil {
		if *reqCache <= 0 {
			return 0, false, nil
		}
		return *reqCache, true, nil
	}

	ttl, hasDefault, err := parseDefaultCacheTTL(scfg)
	if err != nil {
		return 0, false, err
	}
	enabled := hasDefault

	if hasDefault && matchesNoCachePattern(scfg, tool) {
		enabled = false
	}

	if override, ok := lookupToolCacheOverride(scfg, tool); ok {
		if override {
			enabled = hasDefault
		} else {
			enabled = false
		}
	}

	if !enabled {
		return 0, false, nil
	}
	return ttl, true, nil
}

func parseDefaultCacheTTL(scfg config.ServerConfig) (time.Duration, bool, error) {
	if scfg.DefaultCacheTTL == "" {
		return 0, false, nil
	}
	ttl, err := time.ParseDuration(scfg.DefaultCacheTTL)
	if err != nil {
		return 0, false, fmt.Errorf("invalid default_cache_ttl %q: %w", scfg.DefaultCacheTTL, err)
	}
	if ttl <= 0 {
		return 0, false, nil
	}
	return ttl, true, nil
}

func lookupToolCacheOverride(scfg config.ServerConfig, tool string) (bool, bool) {
	if tc, ok := scfg.Tools[tool]; ok && tc.Cache != nil {
		return *tc.Cache, true
	}
	return false, false
}

func matchesNoCachePattern(scfg config.ServerConfig, tool string) bool {
	for _, pattern := range scfg.NoCacheTools {
		matched, err := path.Match(pattern, tool)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func joinLogs(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
