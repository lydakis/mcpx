package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
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
	poolCallTool = func(ctx context.Context, pool *mcppool.Pool, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
		return pool.CallTool(ctx, server, tool, args)
	}
	cacheGet         = cache.Get
	cacheGetMetadata = cache.GetMetadata
	cachePut         = cache.Put
	signalShutdownFn = func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}
)

// Run starts the daemon process. Called when argv[1] == "__daemon".
func Run() error {
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return fmt.Errorf("creating runtime dir: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if ferr := config.MergeFallbackServers(cfg); ferr != nil {
		fmt.Fprintf(os.Stderr, "mcpx daemon: warning: failed to load fallback MCP server config: %v\n", ferr)
	}
	if verr := config.Validate(cfg); verr != nil {
		return fmt.Errorf("invalid config: %w", verr)
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

	handler := func(ctx context.Context, req *ipc.Request) *ipc.Response {
		return dispatch(ctx, cfg, pool, ka, req)
	}

	srv := ipc.NewServer(paths.SocketPath(), nonce, handler)
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

func dispatch(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, req *ipc.Request) *ipc.Response {
	switch req.Type {
	case "list_servers":
		return listServers(cfg)
	case "list_tools":
		return listTools(ctx, cfg, pool, ka, req.Server)
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

func listServers(cfg *config.Config) *ipc.Response {
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []byte
	for _, name := range names {
		out = append(out, []byte(name+"\n")...)
	}
	return &ipc.Response{Content: out}
}

func listTools(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string) *ipc.Response {
	if _, ok := cfg.Servers[server]; !ok {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	ka.Begin(server)
	defer ka.End(server)

	tools, err := poolListTools(ctx, pool, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("listing tools: %v", err)}
	}

	displayNames := make(map[string]string, len(tools))
	for _, t := range tools {
		name := toKebabToolName(t.Name)
		if _, exists := displayNames[name]; exists {
			continue
		}
		displayNames[name] = t.Description
	}

	names := make([]string, 0, len(displayNames))
	for name := range displayNames {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []byte
	for _, name := range names {
		line := name
		if desc := strings.TrimSpace(displayNames[name]); desc != "" {
			line += "\t" + desc
		}
		out = append(out, []byte(line+"\n")...)
	}
	return &ipc.Response{Content: out}
}

func toolSchema(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string) *ipc.Response {
	if _, ok := cfg.Servers[server]; !ok {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	ka.Begin(server)
	defer ka.End(server)

	info, err := poolToolInfoByName(ctx, pool, server, tool)
	if err != nil {
		return &ipc.Response{
			ExitCode: classifyToolLookupError(err),
			Stderr:   fmt.Sprintf("getting schema: %v", err),
		}
	}

	payload := map[string]any{
		"name":        toKebabToolName(info.Name),
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
	scfg, ok := cfg.Servers[server]
	if !ok {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown server: %s", server)}
	}

	ka.Begin(server)
	defer ka.End(server)

	cacheTTL, shouldCache, err := effectiveCacheTTL(scfg, tool, reqCache)
	if err != nil {
		return &ipc.Response{
			ExitCode: ipc.ExitInternal,
			Stderr:   fmt.Sprintf("cache configuration error: %v", err),
		}
	}
	var logs []string
	if shouldCache {
		for _, cacheTool := range toolAliases(tool) {
			if out, exitCode, ok := cacheGet(server, cacheTool, args); ok {
				if verbose {
					if age, ttl, ok := cacheGetMetadata(server, cacheTool, args); ok {
						logs = append(logs, fmt.Sprintf("mcpx: cache hit (age=%s ttl=%s)", age, ttl))
					} else {
						logs = append(logs, "mcpx: cache hit")
					}
				}
				return &ipc.Response{Content: out, ExitCode: exitCode, Stderr: joinLogs(logs)}
			}
		}
		if verbose {
			logs = append(logs, "mcpx: cache miss")
		}
	}

	canonicalTool, err := resolveCanonicalToolName(ctx, pool, server, tool)
	if err != nil {
		return &ipc.Response{
			ExitCode: classifyToolLookupError(err),
			Stderr:   fmt.Sprintf("resolving tool: %v", err),
		}
	}
	cacheTool := canonicalTool

	result, err := poolCallTool(ctx, pool, server, canonicalTool, args)
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
	for _, name := range toolAliases(tool) {
		if tc, ok := scfg.Tools[name]; ok && tc.Cache != nil {
			return *tc.Cache, true
		}
	}
	return false, false
}

func matchesNoCachePattern(scfg config.ServerConfig, tool string) bool {
	aliases := toolAliases(tool)
	for _, pattern := range scfg.NoCacheTools {
		for _, name := range aliases {
			matched, err := path.Match(pattern, name)
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func toolAliases(tool string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 3)
	for _, name := range []string{
		tool,
		strings.ReplaceAll(tool, "-", "_"),
		strings.ReplaceAll(tool, "_", "-"),
	} {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func toKebabToolName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func resolveCanonicalToolName(ctx context.Context, pool *mcppool.Pool, server, requested string) (string, error) {
	if pool == nil {
		return requested, nil
	}

	info, err := poolToolInfoByName(ctx, pool, server, requested)
	if err != nil {
		return "", err
	}
	if info == nil || info.Name == "" {
		return requested, nil
	}
	return info.Name, nil
}

func joinLogs(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
