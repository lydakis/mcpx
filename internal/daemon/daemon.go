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
	"github.com/lydakis/mcpx/internal/servercatalog"
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

const codexAppsServerName = servercatalog.CodexAppsServerName

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

func listServers(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *ipc.Response {
	catalog := newServerCatalog(cfg, pool, ka)
	names, err := catalog.ServerNames(ctx)
	var warn string
	if err != nil {
		warn = fmt.Sprintf("mcpx: warning: failed to enumerate codex apps: %v", err)
		names = configuredServerNames(cfg)
	}

	var out []byte
	for _, name := range names {
		out = append(out, []byte(name+"\n")...)
	}
	return &ipc.Response{Content: out, Stderr: warn}
}

func configuredServerNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		if strings.TrimSpace(name) == "" || name == codexAppsServerName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func newServerCatalog(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *servercatalog.Catalog {
	return servercatalog.New(cfg, func(ctx context.Context, server string) ([]mcppool.ToolInfo, error) {
		return listServerTools(ctx, pool, ka, server)
	})
}

func listServerTools(ctx context.Context, pool *mcppool.Pool, ka *Keepalive, server string) ([]mcppool.ToolInfo, error) {
	if ka != nil {
		ka.Begin(server)
		defer ka.End(server)
	}
	return poolListTools(ctx, pool, server)
}

type toolListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func listTools(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, verbose bool) *ipc.Response {
	catalog := newServerCatalog(cfg, pool, ka)
	route, routeTools, found, err := catalog.Resolve(ctx, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	tools := routeTools
	if !route.IsVirtual() {
		tools, err = listServerTools(ctx, pool, ka, route.Backend)
		if err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("listing tools: %v", err)}
		}
	} else {
		tools = catalog.FilterTools(route, routeTools)
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
	catalog := newServerCatalog(cfg, pool, ka)
	route, routeTools, found, err := catalog.Resolve(ctx, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	var info *mcppool.ToolInfo
	if route.IsVirtual() {
		toolInfo, ok := catalog.ToolInfo(route, routeTools, tool)
		if !ok {
			return &ipc.Response{
				ExitCode: ipc.ExitUsageErr,
				Stderr:   fmt.Sprintf("tool %s not found on server %s", tool, server),
			}
		}
		info = toolInfo
	} else {
		ka.Begin(route.Backend)
		defer ka.End(route.Backend)

		info, err = poolToolInfoByName(ctx, pool, route.Backend, tool)
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
	catalog := newServerCatalog(cfg, pool, ka)
	route, found, err := catalog.ResolveForTool(ctx, server, tool)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}
	scfg, ok := cfg.Servers[route.ConfigServer]
	if !ok {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	ka.Begin(route.Backend)
	defer ka.End(route.Backend)

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
	if !catalog.ToolBelongsToRoute(route, tool) {
		return &ipc.Response{
			ExitCode: ipc.ExitUsageErr,
			Stderr:   fmt.Sprintf("resolving tool: tool %s not found on server %s", tool, server),
		}
	}
	if pool != nil {
		resolvedInfo, err := poolToolInfoByName(ctx, pool, route.Backend, tool)
		if err != nil {
			return &ipc.Response{
				ExitCode: classifyToolLookupError(err),
				Stderr:   fmt.Sprintf("resolving tool: %v", err),
			}
		}
		if resolvedInfo != nil && !catalog.ToolBelongsToRoute(route, resolvedInfo.Name) {
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

	result, err := poolCallToolWithInfo(ctx, pool, route.Backend, info, args)
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
