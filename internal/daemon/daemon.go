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
	"path/filepath"
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
	"github.com/lydakis/mcpx/internal/toolschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	codexAppsServerName       = servercatalog.CodexAppsServerName
	runtimeEphemeralMaxServer = 64
	runtimeConfigPollInterval = 250 * time.Millisecond
	runtimeConfigPollMaxRetry = 8
	diagnosticServerTimeout   = 10 * time.Second
)

var (
	signalNotifyFn                 = signal.Notify
	signalStopFn                   = signal.Stop
	responseCache                  = responseCacheGuard{enabled: true}
	credentialTransitionRecoveryMu sync.Mutex
)

type responseCacheGuard struct {
	mu         sync.RWMutex
	generation uint64
	enabled    bool
}

func (g *responseCacheGuard) clear(clear func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = false
	if err := clear(); err != nil {
		return err
	}
	g.generation++
	g.enabled = true
	return nil
}

func (g *responseCacheGuard) snapshot() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.generation
}

func (g *responseCacheGuard) readIfCurrent(generation uint64, read func() ([]byte, int, bool)) ([]byte, int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.enabled || generation != g.generation {
		return nil, 0, false
	}
	return read()
}

func (g *responseCacheGuard) writeIfCurrent(generation uint64, write func() error) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.enabled || generation != g.generation {
		return false, nil
	}
	return true, write()
}

func clearResponseCache() error {
	return responseCache.clear(cache.Clear)
}

func responseCacheGeneration() uint64 {
	return responseCache.snapshot()
}

func readResponseCacheIfCurrent(generation uint64, read func() ([]byte, int, bool)) ([]byte, int, bool) {
	return responseCache.readIfCurrent(generation, read)
}

func writeResponseCacheIfCurrent(generation uint64, write func() error) (bool, error) {
	return responseCache.writeIfCurrent(generation, write)
}

type runtimeDeps struct {
	poolListTools                      func(ctx context.Context, pool *mcppool.Pool, server string) ([]mcppool.ToolInfo, error)
	poolToolInfoByName                 func(ctx context.Context, pool *mcppool.Pool, server, tool string) (*mcppool.ToolInfo, error)
	poolCallToolWithInfo               func(ctx context.Context, pool *mcppool.Pool, server string, info *mcppool.ToolInfo, args json.RawMessage) (*mcp.CallToolResult, error)
	poolCallToolWithInput              func(ctx context.Context, pool *mcppool.Pool, server string, info *mcppool.ToolInfo, args, inputResponses json.RawMessage, requestState string) (*mcp.CallToolResult, error)
	poolDiagnose                       func(ctx context.Context, pool *mcppool.Pool, server string) (mcppool.Diagnostics, error)
	cacheGet                           func(server, tool string, args json.RawMessage) ([]byte, int, bool)
	cacheGetMetadata                   func(server, tool string, args json.RawMessage) (time.Duration, time.Duration, bool)
	cachePut                           func(server, tool string, args json.RawMessage, content []byte, exitCode int, ttl time.Duration) error
	cacheClear                         func() error
	cacheRemoveCredentialTransition    func(token string) error
	cacheOrphanedCredentialTransitions func() ([]string, error)
	cacheCredentialTransitionPending   func() bool
	poolReset                          func(pool *mcppool.Pool, cfg *config.Config)
	poolSetConfig                      func(pool *mcppool.Pool, cfg *config.Config)
	poolClose                          func(pool *mcppool.Pool, server string)
	poolBeginCredentialTransition      func(pool *mcppool.Pool, servers []string, token string) error
	poolEndCredentialTransition        func(pool *mcppool.Pool, token string)
	keepaliveStop                      func(ka *Keepalive)
	loadConfig                         func() (*config.Config, error)
	mergeFallbackForCWD                func(cfg *config.Config, cwd string) error
	validateConfig                     func(cfg *config.Config) error
	currentRuntimeConfigStamp          func(cfg *config.Config, cwd string) runtimeConfigStamp
	now                                func() time.Time
	signalShutdownProcess              func()
}

func runtimeDefaultDeps() runtimeDeps {
	return runtimeDeps{
		poolListTools: func(ctx context.Context, pool *mcppool.Pool, server string) ([]mcppool.ToolInfo, error) {
			return pool.ListTools(ctx, server)
		},
		poolToolInfoByName: func(ctx context.Context, pool *mcppool.Pool, server, tool string) (*mcppool.ToolInfo, error) {
			return pool.ToolInfoByName(ctx, server, tool)
		},
		poolCallToolWithInfo: func(ctx context.Context, pool *mcppool.Pool, server string, info *mcppool.ToolInfo, args json.RawMessage) (*mcp.CallToolResult, error) {
			return pool.CallToolWithInfo(ctx, server, info, args)
		},
		poolCallToolWithInput: func(ctx context.Context, pool *mcppool.Pool, server string, info *mcppool.ToolInfo, args, inputResponses json.RawMessage, requestState string) (*mcp.CallToolResult, error) {
			return pool.CallToolWithInfoAndInput(ctx, server, info, args, inputResponses, requestState)
		},
		poolDiagnose: func(ctx context.Context, pool *mcppool.Pool, server string) (mcppool.Diagnostics, error) {
			return pool.Diagnose(ctx, server)
		},
		cacheGet:                           cache.Get,
		cacheGetMetadata:                   cache.GetMetadata,
		cachePut:                           cache.Put,
		cacheClear:                         clearResponseCache,
		cacheRemoveCredentialTransition:    cache.RemoveCredentialTransition,
		cacheOrphanedCredentialTransitions: cache.OrphanedCredentialTransitions,
		cacheCredentialTransitionPending:   cache.CredentialTransitionPending,
		poolReset: func(pool *mcppool.Pool, cfg *config.Config) {
			if pool != nil {
				pool.Reset(cfg)
			}
		},
		poolSetConfig: func(pool *mcppool.Pool, cfg *config.Config) {
			if pool != nil {
				pool.SetConfig(cfg)
			}
		},
		poolClose: func(pool *mcppool.Pool, server string) {
			if pool != nil {
				pool.Close(server)
			}
		},
		poolBeginCredentialTransition: func(pool *mcppool.Pool, servers []string, token string) error {
			if pool == nil {
				return nil
			}
			return pool.BeginCredentialTransition(servers, token)
		},
		poolEndCredentialTransition: func(pool *mcppool.Pool, token string) {
			if pool != nil {
				pool.EndCredentialTransition(token)
			}
		},
		keepaliveStop: func(ka *Keepalive) {
			if ka != nil {
				ka.Stop()
			}
		},
		loadConfig:                config.Load,
		mergeFallbackForCWD:       config.MergeFallbackServersForCWD,
		validateConfig:            config.Validate,
		currentRuntimeConfigStamp: currentRuntimeConfigStamp,
		now:                       time.Now,
		signalShutdownProcess: func() {
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Signal(syscall.SIGTERM)
		},
	}
}

func (d runtimeDeps) withDefaults() runtimeDeps {
	def := runtimeDefaultDeps()
	if d.poolListTools == nil {
		d.poolListTools = def.poolListTools
	}
	if d.poolToolInfoByName == nil {
		d.poolToolInfoByName = def.poolToolInfoByName
	}
	if d.poolCallToolWithInfo == nil {
		d.poolCallToolWithInfo = def.poolCallToolWithInfo
	}
	if d.poolCallToolWithInput == nil {
		d.poolCallToolWithInput = def.poolCallToolWithInput
	}
	if d.poolDiagnose == nil {
		d.poolDiagnose = def.poolDiagnose
	}
	if d.cacheGet == nil {
		d.cacheGet = def.cacheGet
	}
	if d.cacheGetMetadata == nil {
		d.cacheGetMetadata = def.cacheGetMetadata
	}
	if d.cachePut == nil {
		d.cachePut = def.cachePut
	}
	if d.cacheClear == nil {
		d.cacheClear = def.cacheClear
	}
	if d.cacheRemoveCredentialTransition == nil {
		d.cacheRemoveCredentialTransition = def.cacheRemoveCredentialTransition
	}
	if d.cacheOrphanedCredentialTransitions == nil {
		d.cacheOrphanedCredentialTransitions = def.cacheOrphanedCredentialTransitions
	}
	if d.cacheCredentialTransitionPending == nil {
		d.cacheCredentialTransitionPending = def.cacheCredentialTransitionPending
	}
	if d.poolReset == nil {
		d.poolReset = def.poolReset
	}
	if d.poolSetConfig == nil {
		d.poolSetConfig = def.poolSetConfig
	}
	if d.poolClose == nil {
		d.poolClose = def.poolClose
	}
	if d.poolBeginCredentialTransition == nil {
		d.poolBeginCredentialTransition = def.poolBeginCredentialTransition
	}
	if d.poolEndCredentialTransition == nil {
		d.poolEndCredentialTransition = def.poolEndCredentialTransition
	}
	if d.keepaliveStop == nil {
		d.keepaliveStop = def.keepaliveStop
	}
	if d.loadConfig == nil {
		d.loadConfig = def.loadConfig
	}
	if d.mergeFallbackForCWD == nil {
		d.mergeFallbackForCWD = def.mergeFallbackForCWD
	}
	if d.validateConfig == nil {
		d.validateConfig = def.validateConfig
	}
	if d.currentRuntimeConfigStamp == nil {
		d.currentRuntimeConfigStamp = def.currentRuntimeConfigStamp
	}
	if d.now == nil {
		d.now = def.now
	}
	if d.signalShutdownProcess == nil {
		d.signalShutdownProcess = def.signalShutdownProcess
	}
	return d
}

// Run starts the daemon process. Called when argv[1] == "__daemon".
func Run() error {
	deps := runtimeDefaultDeps()

	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return fmt.Errorf("creating runtime dir: %w", err)
	}
	if err := cache.RecoverCredentialTransition(); err != nil {
		return fmt.Errorf("recovering response-cache credential transition: %w", err)
	}

	cfg, _, err := loadValidatedConfigForCWDWithDeps("", deps, nil)
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
	ka.SetOnAllIdle(deps.signalShutdownProcess)
	ka.TouchDaemon()
	defer ka.Stop()

	handler := newRuntimeRequestHandlerWithDeps(cfg, pool, ka, deps)

	srv := ipc.NewServer(paths.SocketPath(), nonce, handler.handle)
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Stop()

	fmt.Fprintf(os.Stderr, "mcpx daemon: listening on %s\n", paths.SocketPath())

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signalNotifyFn(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signalStopFn(sigCh)
	<-sigCh

	fmt.Fprintln(os.Stderr, "mcpx daemon: shutting down")
	return nil
}

type runtimeRequestHandler struct {
	mu                    sync.RWMutex
	stateVersion          uint64
	activeCWD             string
	cfgHash               string
	runtimeConfigStamp    runtimeConfigStamp
	lastPolledConfigStamp runtimeConfigStamp
	nextConfigPollAt      time.Time
	cfg                   *config.Config
	pool                  *mcppool.Pool
	ka                    *Keepalive
	deps                  runtimeDeps
	ephemeralServers      map[string]config.ServerConfig
	ephemeralServerOrder  []string
}

type runtimeConfigStamp struct {
	Digest string
}

type runtimeConfigState struct {
	activeCWD       string
	cfgHash         string
	cfg             *config.Config
	fallbackWarning bool
}

func newRuntimeRequestHandler(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *runtimeRequestHandler {
	return newRuntimeRequestHandlerWithDeps(cfg, pool, ka, runtimeDefaultDeps())
}

func newRuntimeRequestHandlerWithDeps(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, deps runtimeDeps) *runtimeRequestHandler {
	deps = deps.withDefaults()
	cfgHash, _ := configFingerprint(cfg)
	initialStamp := deps.currentRuntimeConfigStamp(cfg, "")
	ephemeralServers := runtimeEphemeralServersFromConfig(cfg)
	return &runtimeRequestHandler{
		cfgHash:               cfgHash,
		runtimeConfigStamp:    initialStamp,
		lastPolledConfigStamp: initialStamp,
		nextConfigPollAt:      time.Time{},
		cfg:                   cfg,
		pool:                  pool,
		ka:                    ka,
		deps:                  deps,
		ephemeralServers:      ephemeralServers,
		ephemeralServerOrder:  runtimeEphemeralServerOrder(ephemeralServers),
	}
}

func (h *runtimeRequestHandler) handle(ctx context.Context, req *ipc.Request) *ipc.Response {
	if req == nil {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "nil request"}
	}
	if h.ka != nil && req.Type != "shutdown" {
		h.ka.TouchDaemon()
	}

	if !requestNeedsRuntimeConfig(req) {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return dispatchWithDeps(ctx, h.cfg, h.pool, h.ka, req, h.deps)
	}

	normalizedCWD := strings.TrimSpace(req.CWD)

	for {
		h.mu.RLock()
		sameLiveCWD := normalizedCWD == strings.TrimSpace(h.activeCWD)
		var currentStamp runtimeConfigStamp
		hasCurrentStamp := false
		if sameLiveCWD && req.Ephemeral == nil {
			currentStamp = h.deps.currentRuntimeConfigStamp(h.cfg, normalizedCWD)
			hasCurrentStamp = true
		}
		if sameLiveCWD &&
			req.Ephemeral == nil &&
			currentStamp == h.lastPolledConfigStamp &&
			!h.configPollDueLocked(h.deps.now()) {
			// Safe to dispatch concurrently for same-CWD requests.
			// mcppool serializes per-connection RPCs to prevent stdio frame interleaving.
			resp := dispatchWithDeps(ctx, h.cfg, h.pool, h.ka, req, h.deps)
			h.mu.RUnlock()
			return resp
		}
		h.mu.RUnlock()

		h.mu.Lock()
		if err := h.syncRuntimeConfigLocked(normalizedCWD, currentStamp, hasCurrentStamp); err != nil {
			h.mu.Unlock()
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: err.Error()}
		}
		restoredEphemeral, restoreErr := installRuntimeEphemeralServers(h.cfg, h.ephemeralServers)
		if restoreErr != nil {
			h.mu.Unlock()
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("restoring ephemeral servers: %v", restoreErr)}
		}
		installedEphemeral, resolvedName, resolvedServer, err := installRequestEphemeralServer(h.cfg, req.Server, req.Ephemeral)
		if err != nil {
			h.mu.Unlock()
			return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: err.Error()}
		}
		if restoredEphemeral {
			nextHash, hashErr := configFingerprint(h.cfg)
			if hashErr != nil {
				h.mu.Unlock()
				return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("fingerprinting runtime config: %v", hashErr)}
			}
			h.cfgHash = nextHash
		}

		h.stateVersion++
		dispatchVersion := h.stateVersion
		h.mu.Unlock()

		h.mu.RLock()
		if h.stateVersion != dispatchVersion {
			h.mu.RUnlock()
			continue
		}
		resp := dispatchWithDeps(ctx, h.cfg, h.pool, h.ka, req, h.deps)
		h.mu.RUnlock()

		if !installedEphemeral {
			return resp
		}
		finalResp, finalized := h.finalizeRequestEphemeralInstall(dispatchVersion, resolvedName, resolvedServer, resp)
		if !finalized {
			return resp
		}
		return finalResp
	}
}

func (h *runtimeRequestHandler) syncRuntimeConfigLocked(normalizedCWD string, currentStamp runtimeConfigStamp, hasCurrentStamp bool) error {
	now := h.deps.now()
	sameLiveCWD := normalizedCWD == strings.TrimSpace(h.activeCWD)
	if sameLiveCWD && !hasCurrentStamp {
		currentStamp = h.deps.currentRuntimeConfigStamp(h.cfg, normalizedCWD)
		hasCurrentStamp = true
	}
	if sameLiveCWD &&
		currentStamp == h.lastPolledConfigStamp &&
		!h.configPollDueLocked(now) {
		return nil
	}

	nextState := runtimeConfigState{
		activeCWD: h.activeCWD,
		cfgHash:   h.cfgHash,
		cfg:       h.cfg,
	}
	reloaded := false
	for attempts := 0; ; attempts++ {
		loadStamp := currentStamp
		if attempts > 0 || !sameLiveCWD || !hasCurrentStamp {
			loadStamp = h.deps.currentRuntimeConfigStamp(nextState.cfg, normalizedCWD)
		}
		forceReload := reloaded ||
			!sameLiveCWD ||
			loadStamp != h.runtimeConfigStamp
		if forceReload {
			var err error
			var preserveFallbackFrom *config.Config
			if sameLiveCWD {
				preserveFallbackFrom = h.cfg
			}
			nextState, err = loadRuntimeConfigStateForRequestWithDeps(normalizedCWD, nextState, h.deps, preserveFallbackFrom)
			if err != nil {
				if sameLiveCWD {
					h.lastPolledConfigStamp = loadStamp
					h.nextConfigPollAt = h.deps.now().Add(runtimeConfigPollInterval)
					return nil
				}
				return err
			}
			reloaded = true
		}

		confirmedStamp := h.deps.currentRuntimeConfigStamp(nextState.cfg, normalizedCWD)
		if loadStamp != confirmedStamp {
			// The config changed while we were reloading it, so retry until the
			// loaded config and cached stamp represent the same file contents.
			// This also covers the window where the stamp changes between a
			// same-CWD preflight check and the confirmation read, before we
			// decided to reload on the first pass. Cap the loop so a hot writer
			// does not block the daemon indefinitely.
			if attempts+1 >= runtimeConfigPollMaxRetry {
				if !sameLiveCWD {
					return fmt.Errorf("runtime config did not stabilize after %d reload attempts", runtimeConfigPollMaxRetry)
				}
				h.lastPolledConfigStamp = confirmedStamp
				h.nextConfigPollAt = now
				return nil
			}
			continue
		}
		if err := applyRuntimeConfigStateWithDeps(&h.activeCWD, &h.cfgHash, &h.cfg, h.pool, h.ka, h.deps, nextState); err != nil {
			if sameLiveCWD {
				h.nextConfigPollAt = h.deps.now().Add(runtimeConfigPollInterval)
				return nil
			}
			return err
		}
		h.runtimeConfigStamp = confirmedStamp
		h.lastPolledConfigStamp = confirmedStamp
		h.nextConfigPollAt = h.deps.now().Add(runtimeConfigPollInterval)
		return nil
	}
}

func (h *runtimeRequestHandler) configPollDueLocked(now time.Time) bool {
	return !now.Before(h.nextConfigPollAt)
}

func (h *runtimeRequestHandler) finalizeRequestEphemeralInstall(_ uint64, name string, server config.ServerConfig, resp *ipc.Response) (*ipc.Response, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	changed := false
	if resp != nil && resp.ExitCode == ipc.ExitOK {
		if rememberRuntimeEphemeralServer(h.cfg, h.pool, h.deps, &h.ephemeralServers, &h.ephemeralServerOrder, name, server) {
			changed = true
		}
	} else {
		overlayChanged := forgetRuntimeEphemeralServer(&h.ephemeralServers, &h.ephemeralServerOrder, name)
		removed := removeRuntimeEphemeralServer(h.cfg, name)
		if removed {
			h.deps.poolClose(h.pool, name)
		}
		if overlayChanged || removed {
			changed = true
		}
	}

	if changed {
		nextHash, hashErr := configFingerprint(h.cfg)
		if hashErr != nil {
			return &ipc.Response{
				ExitCode: ipc.ExitInternal,
				Stderr:   fmt.Sprintf("fingerprinting runtime config: %v", hashErr),
			}, true
		}
		h.cfgHash = nextHash
		h.stateVersion++
	}

	return resp, true
}

func forgetRuntimeEphemeralServer(servers *map[string]config.ServerConfig, order *[]string, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	changed := false
	if servers != nil && *servers != nil {
		if _, exists := (*servers)[name]; exists {
			delete(*servers, name)
			changed = true
		}
	}

	if order != nil && len(*order) > 0 {
		filtered := make([]string, 0, len(*order))
		removedFromOrder := false
		for _, existing := range *order {
			trimmed := strings.TrimSpace(existing)
			if trimmed == "" || trimmed == name {
				removedFromOrder = true
				continue
			}
			filtered = append(filtered, trimmed)
		}
		if removedFromOrder || len(filtered) != len(*order) {
			*order = filtered
			changed = true
		}
	}

	return changed
}

func installRequestEphemeralServer(cfg *config.Config, server string, ephemeral *ipc.EphemeralServer) (bool, string, config.ServerConfig, error) {
	if cfg == nil || ephemeral == nil {
		return false, "", config.ServerConfig{}, nil
	}

	resolvedName := strings.TrimSpace(server)
	if resolvedName == "" {
		return false, "", config.ServerConfig{}, fmt.Errorf("ephemeral server requires target name")
	}

	resolvedServer := config.ExpandServerForCurrentEnv(ephemeral.Server)
	installed, err := installRuntimeEphemeralServer(cfg, resolvedName, resolvedServer)
	if err != nil {
		return false, "", config.ServerConfig{}, err
	}
	if !installed {
		return false, "", config.ServerConfig{}, nil
	}
	return true, resolvedName, resolvedServer, nil
}

func installRuntimeEphemeralServers(cfg *config.Config, ephemeral map[string]config.ServerConfig) (bool, error) {
	if cfg == nil || len(ephemeral) == 0 {
		return false, nil
	}

	installedAny := false
	for name, server := range ephemeral {
		installed, err := installRuntimeEphemeralServer(cfg, name, server)
		if err != nil {
			return false, err
		}
		if installed {
			installedAny = true
		}
	}
	return installedAny, nil
}

func installRuntimeEphemeralServer(cfg *config.Config, name string, resolved config.ServerConfig) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("ephemeral server requires target name")
	}
	if _, exists := cfg.Servers[name]; exists {
		return false, nil
	}
	if err := config.ValidateServerConfig(name, resolved); err != nil {
		return false, fmt.Errorf("invalid ephemeral server %q: %w", name, err)
	}

	if cfg.Servers == nil {
		cfg.Servers = make(map[string]config.ServerConfig)
	}
	cfg.Servers[name] = resolved

	if cfg.ServerOrigins == nil {
		cfg.ServerOrigins = make(map[string]config.ServerOrigin)
	}
	cfg.ServerOrigins[name] = config.NewServerOrigin(config.ServerOriginKindRuntimeEphemeral, "")
	return true, nil
}

func rememberRuntimeEphemeralServer(cfg *config.Config, pool *mcppool.Pool, deps runtimeDeps, servers *map[string]config.ServerConfig, order *[]string, name string, server config.ServerConfig) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if servers == nil || order == nil {
		return false
	}
	if *servers == nil {
		*servers = make(map[string]config.ServerConfig)
	}

	if _, exists := (*servers)[name]; exists {
		(*servers)[name] = server
		*order = touchRuntimeEphemeralOrder(*order, name)
		return false
	}

	(*servers)[name] = server
	*order = touchRuntimeEphemeralOrder(*order, name)
	changed := true
	for len(*order) > runtimeEphemeralMaxServer {
		evicted := strings.TrimSpace((*order)[0])
		*order = (*order)[1:]
		if evicted == "" {
			continue
		}
		delete(*servers, evicted)
		if removeRuntimeEphemeralServer(cfg, evicted) {
			deps.poolClose(pool, evicted)
		}
	}
	return changed
}

func touchRuntimeEphemeralOrder(order []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return order
	}
	out := make([]string, 0, len(order)+1)
	for _, existing := range order {
		trimmed := strings.TrimSpace(existing)
		if trimmed == "" || trimmed == name {
			continue
		}
		out = append(out, trimmed)
	}
	out = append(out, name)
	return out
}

func removeRuntimeEphemeralServer(cfg *config.Config, name string) bool {
	if cfg == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	origin, ok := cfg.ServerOrigins[name]
	if !ok {
		return false
	}
	if config.NormalizeServerOrigin(origin).Kind != config.ServerOriginKindRuntimeEphemeral {
		return false
	}
	delete(cfg.Servers, name)
	delete(cfg.ServerOrigins, name)
	return true
}

func runtimeEphemeralServersFromConfig(cfg *config.Config) map[string]config.ServerConfig {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}

	out := make(map[string]config.ServerConfig)
	for name, server := range cfg.Servers {
		origin, ok := cfg.ServerOrigins[name]
		if !ok {
			continue
		}
		if config.NormalizeServerOrigin(origin).Kind != config.ServerOriginKindRuntimeEphemeral {
			continue
		}
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		out[trimmedName] = server
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runtimeEphemeralServerOrder(servers map[string]config.ServerConfig) []string {
	if len(servers) == 0 {
		return nil
	}
	order := make([]string, 0, len(servers))
	for name := range servers {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		order = append(order, trimmed)
	}
	sort.Strings(order)
	return order
}

func requestNeedsRuntimeConfig(req *ipc.Request) bool {
	if req == nil {
		return false
	}
	switch req.Type {
	case "list_servers", "list_tools", "tool_schema", "call_tool", "diagnose_server", "prepare_credential_transition", "reload_server":
		return true
	default:
		return false
	}
}

func syncRuntimeConfigForRequest(reqCWD string, activeCWD, cfgHash *string, cfg **config.Config, pool *mcppool.Pool, ka *Keepalive) error {
	return syncRuntimeConfigForRequestWithDeps(reqCWD, activeCWD, cfgHash, cfg, pool, ka, runtimeDefaultDeps())
}

func syncRuntimeConfigForRequestWithDeps(reqCWD string, activeCWD, cfgHash *string, cfg **config.Config, pool *mcppool.Pool, ka *Keepalive, deps runtimeDeps) error {
	return syncRuntimeConfigForRequestForceWithDeps(reqCWD, activeCWD, cfgHash, cfg, pool, ka, deps, false)
}

func loadRuntimeConfigStateForRequestWithDeps(reqCWD string, current runtimeConfigState, deps runtimeDeps, preserveFallbackFrom *config.Config) (runtimeConfigState, error) {
	deps = deps.withDefaults()
	normalized := strings.TrimSpace(reqCWD)
	nextCfg, fallbackWarning, err := loadValidatedConfigForCWDWithDeps(normalized, deps, preserveFallbackFrom)
	if err != nil {
		return runtimeConfigState{}, err
	}

	nextHash, err := configFingerprint(nextCfg)
	if err != nil {
		return runtimeConfigState{}, err
	}

	return runtimeConfigState{
		activeCWD:       normalized,
		cfgHash:         nextHash,
		cfg:             nextCfg,
		fallbackWarning: fallbackWarning,
	}, nil
}

func applyRuntimeConfigStateWithDeps(activeCWD, cfgHash *string, cfg **config.Config, pool *mcppool.Pool, ka *Keepalive, deps runtimeDeps, next runtimeConfigState) error {
	deps = deps.withDefaults()
	if next.cfg == nil {
		return fmt.Errorf("runtime config is nil")
	}
	if next.activeCWD == *activeCWD && next.cfgHash == *cfgHash && next.cfg == *cfg {
		return nil
	}

	if next.cfgHash != strings.TrimSpace(*cfgHash) {
		deps.keepaliveStop(ka)
		deps.poolReset(pool, next.cfg)
		if ka != nil {
			ka.TouchDaemon()
		}
	} else {
		// Keep active pooled connections, but still move both daemon and pool to
		// the freshly loaded config object so future runtime updates stay in sync.
		deps.poolSetConfig(pool, next.cfg)
	}

	*cfg = next.cfg
	*cfgHash = next.cfgHash
	*activeCWD = next.activeCWD
	return nil
}

func syncRuntimeConfigForRequestForceWithDeps(reqCWD string, activeCWD, cfgHash *string, cfg **config.Config, pool *mcppool.Pool, ka *Keepalive, deps runtimeDeps, force bool) error {
	deps = deps.withDefaults()
	normalized := strings.TrimSpace(reqCWD)
	sameCWD := normalized == strings.TrimSpace(*activeCWD)
	if sameCWD && !force {
		return nil
	}

	var preserveFallbackFrom *config.Config
	if sameCWD && force {
		preserveFallbackFrom = *cfg
	}

	nextState, err := loadRuntimeConfigStateForRequestWithDeps(normalized, runtimeConfigState{
		activeCWD: *activeCWD,
		cfgHash:   *cfgHash,
		cfg:       *cfg,
	}, deps, preserveFallbackFrom)
	if err != nil {
		return err
	}

	return applyRuntimeConfigStateWithDeps(activeCWD, cfgHash, cfg, pool, ka, deps, nextState)
}

func currentRuntimeConfigStamp(cfg *config.Config, cwd string) runtimeConfigStamp {
	hasher := sha256.New()
	for _, sourcePath := range config.RuntimeConfigSourcePathsForCWD(cfg, cwd) {
		_, _ = hasher.Write([]byte(sourcePath))
		_, _ = hasher.Write([]byte{0})

		data, err := os.ReadFile(sourcePath)
		switch {
		case err == nil:
			_, _ = hasher.Write([]byte("file"))
			_, _ = hasher.Write([]byte{0})
			sum := sha256.Sum256(data)
			_, _ = hasher.Write(sum[:])
		case os.IsNotExist(err):
			_, _ = hasher.Write([]byte("missing"))
		default:
			_, _ = hasher.Write([]byte("error"))
			_, _ = hasher.Write([]byte{0})
			_, _ = hasher.Write([]byte(err.Error()))
		}
		_, _ = hasher.Write([]byte{0})
	}
	return runtimeConfigStamp{Digest: hex.EncodeToString(hasher.Sum(nil))}
}

func configFingerprint(cfg *config.Config) (string, error) {
	data, err := json.Marshal(configForRuntimeFingerprint(cfg))
	if err != nil {
		return "", fmt.Errorf("fingerprinting config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func configForRuntimeFingerprint(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}

	filtered := &config.Config{
		FallbackSources: append([]string(nil), cfg.FallbackSources...),
		Servers:         make(map[string]config.ServerConfig, len(cfg.Servers)),
		ServerOrigins:   make(map[string]config.ServerOrigin, len(cfg.ServerOrigins)),
	}

	for name, server := range cfg.Servers {
		origin, ok := cfg.ServerOrigins[name]
		if ok && config.NormalizeServerOrigin(origin).Kind == config.ServerOriginKindRuntimeEphemeral {
			continue
		}
		filtered.Servers[name] = server
	}

	for name, origin := range cfg.ServerOrigins {
		if config.NormalizeServerOrigin(origin).Kind == config.ServerOriginKindRuntimeEphemeral {
			continue
		}
		filtered.ServerOrigins[name] = origin
	}

	return filtered
}

func loadValidatedConfigForCWD(cwd string) (*config.Config, error) {
	cfg, _, err := loadValidatedConfigForCWDWithDeps(cwd, runtimeDefaultDeps(), nil)
	return cfg, err
}

func loadValidatedConfigForCWDWithDeps(cwd string, deps runtimeDeps, preserveFallbackFrom *config.Config) (*config.Config, bool, error) {
	deps = deps.withDefaults()
	cfg, err := deps.loadConfig()
	if err != nil {
		return nil, false, fmt.Errorf("loading config: %w", err)
	}
	fallbackWarning := false
	if ferr := deps.mergeFallbackForCWD(cfg, cwd); ferr != nil {
		fallbackWarning = true
		if preserveFallbackFrom != nil {
			preserveFallbackBackedServers(cfg, preserveFallbackFrom, config.FailedFallbackSourcePaths(ferr))
		}
		fmt.Fprintf(os.Stderr, "mcpx daemon: warning: failed to load fallback MCP server config: %v\n", ferr)
	}
	if verr := deps.validateConfig(cfg); verr != nil {
		return nil, false, fmt.Errorf("invalid config: %w", verr)
	}
	return cfg, fallbackWarning, nil
}

func preserveFallbackBackedServers(dst, prev *config.Config, failedPaths []string) {
	if dst == nil || prev == nil {
		return
	}
	if len(prev.Servers) == 0 || len(prev.ServerOrigins) == 0 || len(failedPaths) == 0 {
		return
	}
	if dst.Servers == nil {
		dst.Servers = make(map[string]config.ServerConfig)
	}
	if dst.ServerOrigins == nil {
		dst.ServerOrigins = make(map[string]config.ServerOrigin)
	}

	failedPathSet := make(map[string]struct{}, len(failedPaths))
	for _, sourcePath := range failedPaths {
		sourcePath = strings.TrimSpace(sourcePath)
		if sourcePath == "" {
			continue
		}
		failedPathSet[filepath.Clean(sourcePath)] = struct{}{}
	}
	if len(failedPathSet) == 0 {
		return
	}

	for name, server := range prev.Servers {
		if _, exists := dst.Servers[name]; exists {
			continue
		}
		origin, ok := prev.ServerOrigins[name]
		if !ok || !preserveFallbackBackedOrigin(origin, failedPathSet) {
			continue
		}
		dst.Servers[name] = cloneRuntimeServerConfig(server)
		dst.ServerOrigins[name] = config.NormalizeServerOrigin(origin)
	}
}

func preserveFallbackBackedOrigin(origin config.ServerOrigin, failedPaths map[string]struct{}) bool {
	origin = config.NormalizeServerOrigin(origin)
	switch origin.Kind {
	case config.ServerOriginKindMCPXConfig, config.ServerOriginKindRuntimeEphemeral:
		return false
	default:
		sourcePath := strings.TrimSpace(origin.Path)
		if sourcePath == "" {
			return false
		}
		_, ok := failedPaths[filepath.Clean(sourcePath)]
		return ok
	}
}

func cloneRuntimeServerConfig(server config.ServerConfig) config.ServerConfig {
	return config.ServerConfig{
		Command:                server.Command,
		Args:                   append([]string(nil), server.Args...),
		Env:                    cloneRuntimeStringMap(server.Env),
		URL:                    server.URL,
		Headers:                cloneRuntimeStringMap(server.Headers),
		OAuth:                  server.OAuth,
		OAuthScopes:            append([]string(nil), server.OAuthScopes...),
		OAuthClientMetadataURL: server.OAuthClientMetadataURL,
		DefaultCacheTTL:        server.DefaultCacheTTL,
		NoCacheTools:           append([]string(nil), server.NoCacheTools...),
		Tools:                  cloneRuntimeToolConfigMap(server.Tools),
	}
}

func cloneRuntimeStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneRuntimeToolConfigMap(src map[string]config.ToolConfig) map[string]config.ToolConfig {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]config.ToolConfig, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func dispatch(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, req *ipc.Request) *ipc.Response {
	return dispatchWithDeps(ctx, cfg, pool, ka, req, runtimeDefaultDeps())
}

func dispatchWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, req *ipc.Request, deps runtimeDeps) *ipc.Response {
	deps = deps.withDefaults()
	transitionPending := deps.cacheCredentialTransitionPending()
	if transitionPending {
		if err := recoverOrphanedCredentialTransitions(pool, deps); err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("recovering OAuth credential transition: %v", err)}
		}
		transitionPending = deps.cacheCredentialTransitionPending()
	}
	if transitionPending && credentialTransitionBlocksRequest(pool, req) {
		return &ipc.Response{ExitCode: ipc.ExitToolErr, Stderr: "OAuth credential transition in progress"}
	}
	switch req.Type {
	case "ping":
		return &ipc.Response{ExitCode: ipc.ExitOK}
	case "list_servers":
		return listServersWithDeps(ctx, cfg, pool, ka, req.IncludeHidden, deps)
	case "list_tools":
		return listToolsResponseWithDeps(ctx, cfg, pool, ka, req.Server, req.Verbose, req.IncludeSchemas, deps)
	case "tool_schema":
		return toolSchemaWithDeps(ctx, cfg, pool, ka, req.Server, req.Tool, deps)
	case "call_tool":
		return callToolRoundTripWithInputModeWithDeps(ctx, cfg, pool, ka, req.Server, req.Tool, req.Args, req.ArgsFromJSON, req.InputResponses, req.RequestState, req.Cache, req.Verbose, deps)
	case "diagnose_server":
		return diagnoseServerWithDeps(ctx, cfg, pool, ka, req.Server, deps)
	case "shutdown":
		go deps.signalShutdownProcess()
		return &ipc.Response{Content: []byte("shutting down\n")}
	case "reload_server":
		return reloadServerTransitionWithDeps(cfg, pool, req.Server, req.Transition, deps)
	case "prepare_credential_transition":
		return prepareCredentialTransitionWithDeps(cfg, pool, req.Server, req.Transition, deps)
	default:
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: fmt.Sprintf("unknown request type: %s", req.Type)}
	}
}

func recoverOrphanedCredentialTransitions(pool *mcppool.Pool, deps runtimeDeps) error {
	credentialTransitionRecoveryMu.Lock()
	defer credentialTransitionRecoveryMu.Unlock()

	orphaned, err := deps.cacheOrphanedCredentialTransitions()
	if err != nil {
		return err
	}
	if len(orphaned) == 0 {
		return nil
	}
	if err := deps.cacheClear(); err != nil {
		return err
	}
	for _, token := range orphaned {
		if err := deps.cacheRemoveCredentialTransition(token); err != nil {
			return err
		}
		deps.poolEndCredentialTransition(pool, token)
	}
	return nil
}

func credentialTransitionBlocksRequest(pool *mcppool.Pool, req *ipc.Request) bool {
	if req == nil {
		return true
	}
	switch req.Type {
	case "ping", "list_servers", "prepare_credential_transition", "reload_server", "shutdown":
		return false
	}
	if pool == nil || !pool.HasCredentialTransitions() {
		// A live marker without an in-memory owner means this daemon started in
		// the middle of a transition. Keep credential-bound work fail-closed.
		return true
	}
	return pool.CredentialTransitionPending(req.Server)
}

func diagnoseServerWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, deps runtimeDeps) *ipc.Response {
	if _, ok := cfg.Servers[server]; !ok {
		return unknownServerResponse(server)
	}
	if ka != nil {
		ka.Begin(server)
		defer ka.End(server)
	}
	diagnosticCtx, cancel := context.WithTimeout(ctx, diagnosticServerTimeout)
	defer cancel()
	diagnostics, err := deps.poolDiagnose(diagnosticCtx, pool, server)
	if err != nil {
		// Pool errors may contain server-controlled JSON-RPC text. Do not move
		// arbitrary remote strings across the credential-safe doctor boundary.
		return &ipc.Response{ExitCode: ipc.ExitToolErr, Stderr: "server diagnostics failed"}
	}
	content, err := json.Marshal(diagnostics)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("encoding diagnostics: %v", err)}
	}
	return &ipc.Response{ExitCode: ipc.ExitOK, Content: content}
}

func reloadServerWithDeps(cfg *config.Config, pool *mcppool.Pool, server string, deps runtimeDeps) *ipc.Response {
	return reloadServerTransitionWithDeps(cfg, pool, server, "", deps)
}

func credentialBoundServerNames(cfg *config.Config, server string) ([]string, bool) {
	target, ok := cfg.Servers[server]
	if !ok {
		return nil, false
	}

	names := []string{server}
	if target.IsHTTP() && target.OAuth && !target.HasAuthorizationHeader() {
		names = names[:0]
		for name, candidate := range cfg.Servers {
			if candidate.IsHTTP() && candidate.OAuth && !candidate.HasAuthorizationHeader() && candidate.URL == target.URL {
				names = append(names, name)
			}
		}
		sort.Strings(names)
	}
	return names, true
}

func prepareCredentialTransitionWithDeps(cfg *config.Config, pool *mcppool.Pool, server, transition string, deps runtimeDeps) *ipc.Response {
	names, ok := credentialBoundServerNames(cfg, server)
	if !ok {
		return unknownServerResponse(server)
	}
	if strings.TrimSpace(transition) == "" {
		return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "credential transition token is required"}
	}
	if err := deps.poolBeginCredentialTransition(pool, names, transition); err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("preparing credential transition: %v", err)}
	}
	return &ipc.Response{ExitCode: ipc.ExitOK}
}

func reloadServerTransitionWithDeps(cfg *config.Config, pool *mcppool.Pool, server, transition string, deps runtimeDeps) *ipc.Response {
	if transition != "" {
		if err := deps.cacheClear(); err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("clearing response cache: %v", err)}
		}
		if err := deps.cacheRemoveCredentialTransition(transition); err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("completing credential transition: %v", err)}
		}
		deps.poolEndCredentialTransition(pool, transition)
		return &ipc.Response{ExitCode: ipc.ExitOK}
	}
	names, ok := credentialBoundServerNames(cfg, server)
	if !ok {
		return unknownServerResponse(server)
	}
	for _, name := range names {
		deps.poolClose(pool, name)
	}
	if err := deps.cacheClear(); err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("clearing response cache: %v", err)}
	}
	return &ipc.Response{ExitCode: ipc.ExitOK}
}

func listServers(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *ipc.Response {
	return listServersWithDeps(ctx, cfg, pool, ka, false, runtimeDefaultDeps())
}

func listServersWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, includeHidden bool, deps runtimeDeps) *ipc.Response {
	catalog := newServerCatalogWithDeps(cfg, pool, ka, deps)
	names, err := catalog.ServerNames(ctx)
	var warn string
	if err != nil {
		warn = fmt.Sprintf("mcpx: warning: failed to enumerate codex apps: %v", err)
		names = configuredServerNames(cfg, includeHidden)
	}
	if !includeHidden {
		names = visibleServerNames(cfg, names)
	}

	entries := make([]serverListEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, serverListEntry{
			Name:   name,
			Origin: resolveServerOrigin(cfg, name),
		})
	}

	raw, marshalErr := json.Marshal(entries)
	if marshalErr != nil {
		return &ipc.Response{
			ExitCode: ipc.ExitInternal,
			Stderr:   fmt.Sprintf("encoding server list: %v", marshalErr),
		}
	}
	return &ipc.Response{Content: raw, Stderr: warn}
}

func visibleServerNames(cfg *config.Config, names []string) []string {
	if len(names) == 0 || cfg == nil {
		return names
	}

	filtered := make([]string, 0, len(names))
	for _, name := range names {
		origin, ok := cfg.ServerOrigins[name]
		if ok && config.NormalizeServerOrigin(origin).Kind == config.ServerOriginKindRuntimeEphemeral {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

type serverListEntry struct {
	Name   string              `json:"name"`
	Origin config.ServerOrigin `json:"origin"`
}

func resolveServerOrigin(cfg *config.Config, name string) config.ServerOrigin {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return config.NormalizeServerOrigin(config.ServerOrigin{})
	}
	if cfg == nil {
		return config.NewServerOrigin(config.ServerOriginKindCodexApps, "")
	}
	if origin, ok := cfg.ServerOrigins[trimmedName]; ok {
		return config.NormalizeServerOrigin(origin)
	}
	if _, ok := cfg.Servers[trimmedName]; ok {
		return config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "")
	}
	return config.NewServerOrigin(config.ServerOriginKindCodexApps, "")
}

func configuredServerNames(cfg *config.Config, includeHidden bool) []string {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		if strings.TrimSpace(name) == "" || name == codexAppsServerName {
			continue
		}
		if !includeHidden {
			if origin, ok := cfg.ServerOrigins[name]; ok {
				if config.NormalizeServerOrigin(origin).Kind == config.ServerOriginKindRuntimeEphemeral {
					continue
				}
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func unknownServerResponse(server string) *ipc.Response {
	return &ipc.Response{
		ExitCode:  ipc.ExitUsageErr,
		Stderr:    fmt.Sprintf("unknown server: %s", server),
		ErrorCode: ipc.ErrorCodeUnknownServer,
	}
}

func newServerCatalog(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive) *servercatalog.Catalog {
	return newServerCatalogWithDeps(cfg, pool, ka, runtimeDefaultDeps())
}

func newServerCatalogWithDeps(cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, deps runtimeDeps) *servercatalog.Catalog {
	return servercatalog.New(cfg, func(ctx context.Context, server string) ([]mcppool.ToolInfo, error) {
		return listServerToolsWithDeps(ctx, pool, ka, server, deps)
	})
}

func listServerTools(ctx context.Context, pool *mcppool.Pool, ka *Keepalive, server string) ([]mcppool.ToolInfo, error) {
	return listServerToolsWithDeps(ctx, pool, ka, server, runtimeDefaultDeps())
}

func listServerToolsWithDeps(ctx context.Context, pool *mcppool.Pool, ka *Keepalive, server string, deps runtimeDeps) ([]mcppool.ToolInfo, error) {
	deps = deps.withDefaults()
	if ka != nil {
		ka.Begin(server)
		defer ka.End(server)
	}
	return deps.poolListTools(ctx, pool, server)
}

type toolListEntry struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
}

func listTools(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, verbose bool) *ipc.Response {
	return listToolsWithDeps(ctx, cfg, pool, ka, server, verbose, runtimeDefaultDeps())
}

func listToolsWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, verbose bool, deps runtimeDeps) *ipc.Response {
	return listToolsResponseWithDeps(ctx, cfg, pool, ka, server, verbose, false, deps)
}

func listToolsResponseWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server string, verbose, includeSchemas bool, deps runtimeDeps) *ipc.Response {
	catalog := newServerCatalogWithDeps(cfg, pool, ka, deps)
	route, routeTools, found, err := catalog.Resolve(ctx, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return unknownServerResponse(server)
	}

	tools := routeTools
	if !route.IsVirtual() {
		tools, err = listServerToolsWithDeps(ctx, pool, ka, route.Backend, deps)
		if err != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("listing tools: %v", err)}
		}
	} else {
		tools = catalog.FilterTools(route, routeTools)
	}

	toolByName := make(map[string]mcppool.ToolInfo, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if _, exists := toolByName[name]; exists {
			continue
		}
		desc := strings.TrimSpace(t.Description)
		if !verbose {
			desc = summarizeToolDescription(desc)
		}
		t.Description = desc
		toolByName[name] = t
	}

	names := make([]string, 0, len(toolByName))
	for name := range toolByName {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]toolListEntry, 0, len(names))
	for _, name := range names {
		tool := toolByName[name]
		entry := toolListEntry{
			Name:        name,
			Title:       strings.TrimSpace(tool.Title),
			Description: strings.TrimSpace(tool.Description),
		}
		if includeSchemas {
			entry.InputSchema = tool.InputSchema
			entry.OutputSchema = tool.OutputSchema
			entry.Annotations = tool.Annotations
		}
		entries = append(entries, entry)
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
	return toolSchemaWithDeps(ctx, cfg, pool, ka, server, tool, runtimeDefaultDeps())
}

func toolSchemaWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string, deps runtimeDeps) *ipc.Response {
	deps = deps.withDefaults()
	catalog := newServerCatalogWithDeps(cfg, pool, ka, deps)
	route, routeTools, found, err := catalog.Resolve(ctx, server)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return unknownServerResponse(server)
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

		info, err = deps.poolToolInfoByName(ctx, pool, route.Backend, tool)
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
	if info.Title != "" {
		payload["title"] = info.Title
	}
	if len(info.Annotations) > 0 {
		var annotations any
		if err := json.Unmarshal(info.Annotations, &annotations); err == nil {
			payload["annotations"] = annotations
		}
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
	return callToolWithDeps(ctx, cfg, pool, ka, server, tool, args, reqCache, verbose, runtimeDefaultDeps())
}

func callToolWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string, args json.RawMessage, reqCache *time.Duration, verbose bool, deps runtimeDeps) *ipc.Response {
	return callToolWithInputModeWithDeps(ctx, cfg, pool, ka, server, tool, args, true, reqCache, verbose, deps)
}

func callToolWithInputModeWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string, args json.RawMessage, argsFromJSON bool, reqCache *time.Duration, verbose bool, deps runtimeDeps) *ipc.Response {
	return callToolRoundTripWithInputModeWithDeps(ctx, cfg, pool, ka, server, tool, args, argsFromJSON, nil, "", reqCache, verbose, deps)
}

func callToolRoundTripWithInputModeWithDeps(ctx context.Context, cfg *config.Config, pool *mcppool.Pool, ka *Keepalive, server, tool string, args json.RawMessage, argsFromJSON bool, inputResponses json.RawMessage, requestState string, reqCache *time.Duration, verbose bool, deps runtimeDeps) *ipc.Response {
	deps = deps.withDefaults()
	cacheEpoch := responseCacheGeneration()
	catalog := newServerCatalogWithDeps(cfg, pool, ka, deps)
	route, found, err := catalog.ResolveForTool(ctx, server, tool)
	if err != nil {
		return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("resolving server: %v", err)}
	}
	if !found {
		return unknownServerResponse(server)
	}
	scfg, ok := cfg.Servers[route.ConfigServer]
	if !ok {
		return unknownServerResponse(server)
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
	if len(inputResponses) > 0 || requestState != "" {
		shouldCache = false
		if verbose {
			logs = append(logs, "mcpx: cache bypass (multi-round-trip retry)")
		}
	}
	readCache := func() *ipc.Response {
		out, exitCode, ok := readResponseCacheIfCurrent(cacheEpoch, func() ([]byte, int, bool) {
			return deps.cacheGet(server, tool, args)
		})
		if ok {
			if verbose {
				if age, ttl, ok := deps.cacheGetMetadata(server, tool, args); ok {
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
		return nil
	}
	if shouldCache && argsFromJSON {
		if cached := readCache(); cached != nil {
			return cached
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
		resolvedInfo, err := deps.poolToolInfoByName(ctx, pool, route.Backend, tool)
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
	if analysis := toolschema.AnalyzeRaw(info.InputSchema); !analysis.FlagSafe && !argsFromJSON {
		return &ipc.Response{
			ExitCode: ipc.ExitUsageErr,
			Stderr: fmt.Sprintf(
				"calling tool: input schema cannot be represented safely as flags (%s); pass an object as positional or stdin JSON",
				analysis.Reason,
			),
		}
	}
	if shouldCache && !argsFromJSON {
		if cached := readCache(); cached != nil {
			return cached
		}
	}

	var result *mcp.CallToolResult
	if len(inputResponses) > 0 || requestState != "" {
		result, err = deps.poolCallToolWithInput(ctx, pool, route.Backend, info, args, inputResponses, requestState)
	} else {
		result, err = deps.poolCallToolWithInfo(ctx, pool, route.Backend, info, args)
	}
	if err != nil {
		return &ipc.Response{
			ExitCode: classifyCallToolError(err),
			Stderr:   fmt.Sprintf("calling tool: %v", err),
		}
	}
	if result != nil && result.NeedsInput() {
		out, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: fmt.Sprintf("encoding input-required result: %v", marshalErr)}
		}
		out = append(out, '\n')
		return &ipc.Response{
			Content:   out,
			ExitCode:  ipc.ExitToolErr,
			ErrorCode: ipc.ErrorCodeInputRequired,
			Stderr:    joinLogs(logs),
		}
	}

	out, exitCode := response.Unwrap(result)
	if shouldCache && exitCode == ipc.ExitOK {
		stored, _ := writeResponseCacheIfCurrent(cacheEpoch, func() error {
			return deps.cachePut(server, cacheTool, args, out, exitCode, cacheTTL)
		})
		if verbose && stored {
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
