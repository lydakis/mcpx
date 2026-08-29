package mcppool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	modernProtocolVersion = "2026-07-28"
	legacyToolCatalogTTL  = time.Minute
	maxToolCatalogPages   = 256
	maxToolCatalogItems   = 10_000
)

// ToolInfo is a simplified tool descriptor returned by ListTools.
type ToolInfo struct {
	Name         string
	Title        string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Annotations  json.RawMessage
	parsedInput  map[string]any
}

// Diagnostics is a redacted snapshot of an established MCP connection.
type Diagnostics struct {
	Transport       string `json:"transport"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
	AuthSource      string `json:"auth_source"`
	ToolCount       int    `json:"tool_count"`
}

// connection wraps an MCP client with its transport.
type connection struct {
	listTools         func(ctx context.Context) ([]*mcp.Tool, error)
	callTool          func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)
	callToolWithInput func(ctx context.Context, name string, args map[string]any, inputResponses mcp.InputResponseMap, requestState string) (*mcp.CallToolResult, error)
	close             func() error
	quiesce           func()
	reqMu             sync.Mutex
	toolMu            sync.RWMutex
	toolIndex         map[string]ToolInfo
	toolList          []ToolInfo
	indexed           bool
	// toolCacheUntil bounds mcpx's connection-local catalog cache. The SDK has
	// its own protocol TTL cache; this outer cache must never outlive it.
	toolCacheUntil     time.Time
	toolCacheDisabled  bool
	indexMu            sync.Mutex
	diagnostics        Diagnostics
	credentialIdentity string
}

type retiredConnection struct {
	done               chan struct{}
	credentialIdentity string
}

type listToolsPageFunc func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)

type toolCatalogCachePolicy struct {
	deadline  time.Time
	cacheable bool
}

func listAllToolPages(ctx context.Context, listPage listToolsPageFunc) ([]*mcp.Tool, toolCatalogCachePolicy, error) {
	return listAllToolPagesWithClock(ctx, listPage, time.Now)
}

func listAllToolPagesWithClock(ctx context.Context, listPage listToolsPageFunc, now func() time.Time) ([]*mcp.Tool, toolCatalogCachePolicy, error) {
	var (
		allTools       []*mcp.Tool
		params         *mcp.ListToolsParams
		earliestExpiry time.Time
		allCacheable   = true
	)
	seenCursors := make(map[string]struct{})
	for page := 0; ; page++ {
		if page >= maxToolCatalogPages {
			return nil, toolCatalogCachePolicy{}, fmt.Errorf("tools/list exceeded page limit of %d", maxToolCatalogPages)
		}
		result, err := listPage(ctx, params)
		if err != nil {
			return nil, toolCatalogCachePolicy{}, err
		}
		if result == nil {
			return nil, toolCatalogCachePolicy{}, fmt.Errorf("tools/list returned an empty result")
		}
		if len(result.Tools) > maxToolCatalogItems-len(allTools) {
			return nil, toolCatalogCachePolicy{}, fmt.Errorf("tools/list exceeded tool limit of %d", maxToolCatalogItems)
		}
		allTools = append(allTools, result.Tools...)
		allCacheable = allCacheable && result.TTLMs > 0
		pageExpiry := now().Add(time.Duration(result.TTLMs) * time.Millisecond)
		if earliestExpiry.IsZero() || pageExpiry.Before(earliestExpiry) {
			earliestExpiry = pageExpiry
		}

		if result.NextCursor == "" {
			return allTools, toolCatalogCachePolicy{deadline: earliestExpiry, cacheable: allCacheable}, nil
		}
		if _, exists := seenCursors[result.NextCursor]; exists {
			return nil, toolCatalogCachePolicy{}, fmt.Errorf("tools/list returned repeated cursor %q", result.NextCursor)
		}
		seenCursors[result.NextCursor] = struct{}{}
		params = &mcp.ListToolsParams{Cursor: result.NextCursor}
	}
}

// Diagnose connects to a server, verifies tool discovery, and returns only
// redacted protocol and transport metadata.
func (p *Pool) Diagnose(ctx context.Context, server string) (Diagnostics, error) {
	tools, err := p.ListTools(ctx, server)
	if err != nil {
		return Diagnostics{}, err
	}
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return Diagnostics{}, err
	}
	diagnostics := conn.diagnostics
	diagnostics.ToolCount = len(tools)
	return diagnostics, nil
}

func sessionDiagnostics(session *mcp.ClientSession, transport, authSource string) Diagnostics {
	diagnostics := Diagnostics{Transport: transport, AuthSource: authSource}
	if session == nil || session.InitializeResult() == nil {
		return diagnostics
	}
	diagnostics.ProtocolVersion = session.InitializeResult().ProtocolVersion
	return diagnostics
}

// Pool manages MCP server connections, creating them on demand.
type Pool struct {
	cfg                   *config.Config
	mu                    sync.Mutex
	conns                 map[string]*connection
	transitions           map[string]string
	credentialTransitions map[string]string
	retiring              map[string][]*retiredConnection
}

// New creates a new connection pool.
func New(cfg *config.Config) *Pool {
	return &Pool{
		cfg:                   cfg,
		conns:                 make(map[string]*connection),
		transitions:           make(map[string]string),
		credentialTransitions: make(map[string]string),
		retiring:              make(map[string][]*retiredConnection),
	}
}

func (p *Pool) getOrCreate(ctx context.Context, server string) (*connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, ok := p.conns[server]; ok {
		identity := conn.credentialIdentity
		if identity == "" && p.cfg != nil {
			identity = serverCredentialIdentity(p.cfg.Servers[server])
		}
		if p.transitions[server] != "" || (identity != "" && p.credentialTransitions[identity] != "") {
			return nil, fmt.Errorf("server %s: credential transition in progress", server)
		}
		return conn, nil
	}

	scfg, ok := p.cfg.Servers[server]
	if !ok {
		return nil, fmt.Errorf("unknown server: %s", server)
	}
	identity := serverCredentialIdentity(scfg)
	if p.transitions[server] != "" || (identity != "" && p.credentialTransitions[identity] != "") {
		return nil, fmt.Errorf("server %s: credential transition in progress", server)
	}

	var conn *connection
	var err error

	if scfg.IsStdio() {
		conn, err = connectStdio(ctx, scfg)
	} else if scfg.IsHTTP() {
		conn, err = connectHTTP(ctx, scfg)
	} else {
		return nil, fmt.Errorf("server %s: no command or url configured", server)
	}

	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", server, err)
	}
	conn.credentialIdentity = serverCredentialIdentity(scfg)

	p.conns[server] = conn
	return conn, nil
}

func (p *Pool) invalidate(server string, conn *connection) {
	p.mu.Lock()
	if current, ok := p.conns[server]; ok && current == conn {
		delete(p.conns, server)
		p.retireConnectionLocked(server, conn)
	}
	p.mu.Unlock()
}

// ListTools returns the tools available on a server.
func (p *Pool) ListTools(ctx context.Context, server string) ([]ToolInfo, error) {
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	if infos, found := cachedToolInfos(conn); found {
		return infos, nil
	}

	conn.indexMu.Lock()
	defer conn.indexMu.Unlock()

	if infos, found := cachedToolInfos(conn); found {
		return infos, nil
	}

	tools, err := runListTools(conn, ctx)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}

	infos := buildToolInfos(tools)
	cacheToolInfos(conn, infos)
	if cached, found := cachedToolInfos(conn); found {
		return cached, nil
	}
	return infos, nil
}

// ToolSchema returns the input schema for a specific tool.
func (p *Pool) ToolSchema(ctx context.Context, server, tool string) (json.RawMessage, error) {
	info, err := p.ToolInfoByName(ctx, server, tool)
	if err != nil {
		return nil, err
	}
	return info.InputSchema, nil
}

// ToolInfoByName returns metadata and schemas for a specific tool.
func (p *Pool) ToolInfoByName(ctx context.Context, server, tool string) (*ToolInfo, error) {
	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	if info, found, _ := cachedToolInfo(conn, tool); found {
		return info, nil
	}

	conn.indexMu.Lock()
	defer conn.indexMu.Unlock()

	if info, found, _ := cachedToolInfo(conn, tool); found {
		return info, nil
	}

	tools, err := runListTools(conn, ctx)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}
	infos := buildToolInfos(tools)
	cacheToolInfos(conn, infos)

	if info, found, _ := cachedToolInfo(conn, tool); found {
		return info, nil
	}
	for _, info := range infos {
		if info.Name == tool {
			toolCopy := cloneToolInfo(info)
			return &toolCopy, nil
		}
	}
	return nil, fmt.Errorf("tool %s not found on server %s", tool, server)
}

func buildToolInfos(tools []*mcp.Tool) []ToolInfo {
	infos := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		inputSchema, _ := marshalInputSchema(t)
		outputSchema, _ := marshalOutputSchema(t)
		annotations, _ := json.Marshal(t.Annotations)
		if t.Annotations == nil {
			annotations = nil
		}
		infos = append(infos, ToolInfo{
			Name:         t.Name,
			Title:        t.Title,
			Description:  t.Description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
			Annotations:  annotations,
			parsedInput:  parseInputSchema(inputSchema),
		})
	}
	return infos
}

func parseInputSchema(schema json.RawMessage) map[string]any {
	if len(schema) == 0 {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return nil
	}
	return parsed
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func cloneToolInfo(info ToolInfo) ToolInfo {
	cloned := info
	cloned.InputSchema = cloneRawMessage(info.InputSchema)
	cloned.OutputSchema = cloneRawMessage(info.OutputSchema)
	cloned.Annotations = cloneRawMessage(info.Annotations)
	return cloned
}

func cachedToolInfo(conn *connection, tool string) (*ToolInfo, bool, bool) {
	if conn == nil {
		return nil, false, false
	}

	conn.toolMu.RLock()
	defer conn.toolMu.RUnlock()

	if !toolCacheValidLocked(conn, time.Now()) {
		return nil, false, false
	}

	info, ok := conn.toolIndex[tool]
	if !ok {
		return nil, false, true
	}

	toolCopy := cloneToolInfo(info)
	return &toolCopy, true, true
}

func cachedToolInfos(conn *connection) ([]ToolInfo, bool) {
	if conn == nil {
		return nil, false
	}

	conn.toolMu.RLock()
	defer conn.toolMu.RUnlock()

	if !toolCacheValidLocked(conn, time.Now()) {
		return nil, false
	}

	out := make([]ToolInfo, len(conn.toolList))
	for i := range conn.toolList {
		out[i] = cloneToolInfo(conn.toolList[i])
	}
	return out, true
}

func cacheToolInfos(conn *connection, infos []ToolInfo) {
	if conn == nil {
		return
	}

	index := make(map[string]ToolInfo, len(infos))
	list := make([]ToolInfo, 0, len(infos))
	for _, info := range infos {
		if info.Name == "" {
			continue
		}
		cloned := cloneToolInfo(info)
		index[cloned.Name] = cloned
		list = append(list, cloned)
	}

	conn.toolMu.Lock()
	if conn.toolCacheUntil.IsZero() && !conn.toolCacheDisabled {
		conn.toolCacheUntil = time.Now().Add(legacyToolCatalogTTL)
	}
	conn.toolIndex = index
	conn.toolList = list
	conn.indexed = true
	conn.toolMu.Unlock()
}

func setToolCachePolicy(conn *connection, ttl time.Duration, modern bool) {
	setToolCacheDeadline(conn, time.Now().Add(ttl), ttl > 0, modern)
}

func setToolCacheDeadline(conn *connection, deadline time.Time, cacheable bool, modern bool) {
	if conn == nil {
		return
	}

	conn.toolMu.Lock()
	defer conn.toolMu.Unlock()
	conn.toolCacheDisabled = modern && !cacheable
	if cacheable {
		conn.toolCacheUntil = deadline
	} else if modern {
		conn.toolCacheUntil = time.Time{}
	} else {
		conn.toolCacheUntil = time.Now().Add(legacyToolCatalogTTL)
	}
}

func toolCacheValidLocked(conn *connection, now time.Time) bool {
	if conn == nil || !conn.indexed || conn.toolCacheDisabled {
		return false
	}
	return conn.toolCacheUntil.IsZero() || now.Before(conn.toolCacheUntil)
}

// CallToolWithInfo invokes a resolved tool on a server.
func (p *Pool) CallToolWithInfo(ctx context.Context, server string, info *ToolInfo, argsJSON json.RawMessage) (*mcp.CallToolResult, error) {
	return p.CallToolWithInfoAndInput(ctx, server, info, argsJSON, nil, "")
}

// CallToolWithInfoAndInput invokes a tool retry with MCP multi-round-trip
// input responses and the opaque request state returned by the server.
func (p *Pool) CallToolWithInfoAndInput(ctx context.Context, server string, info *ToolInfo, argsJSON, inputResponsesJSON json.RawMessage, requestState string) (*mcp.CallToolResult, error) {
	if info == nil || info.Name == "" {
		return nil, fmt.Errorf("tool info is required")
	}

	args, err := compileJSONArgs(argsJSON, info.InputSchema, info.parsedInput)
	if err != nil {
		return nil, err
	}

	var inputResponses mcp.InputResponseMap
	if len(inputResponsesJSON) > 0 {
		if err := json.Unmarshal(inputResponsesJSON, &inputResponses); err != nil {
			return nil, fmt.Errorf("%w: invalid input responses: %v", ErrInvalidParams, err)
		}
	}

	conn, err := p.getOrCreate(ctx, server)
	if err != nil {
		return nil, err
	}

	result, err := runCallToolWithInput(conn, ctx, info.Name, args, inputResponses, requestState)
	if err != nil {
		p.invalidate(server, conn)
		return nil, err
	}
	return result, nil
}

// CallTool invokes a tool on a server.
func (p *Pool) CallTool(ctx context.Context, server, tool string, argsJSON json.RawMessage) (*mcp.CallToolResult, error) {
	info, err := p.ToolInfoByName(ctx, server, tool)
	if err != nil {
		return nil, err
	}
	return p.CallToolWithInfo(ctx, server, info, argsJSON)
}

func compileJSONArgs(argsJSON json.RawMessage, toolSchema json.RawMessage, parsedSchema map[string]any) (map[string]any, error) {
	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	} else {
		args = map[string]any{}
	}

	if len(parsedSchema) > 0 {
		return compileToolArgsAgainstSchema(args, parsedSchema)
	}
	return compileToolArgs(args, toolSchema)
}

func runListTools(conn *connection, ctx context.Context) ([]*mcp.Tool, error) {
	conn.reqMu.Lock()
	defer conn.reqMu.Unlock()
	return conn.listTools(ctx)
}

func runCallTool(conn *connection, ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return runCallToolWithInput(conn, ctx, name, args, nil, "")
}

func runCallToolWithInput(conn *connection, ctx context.Context, name string, args map[string]any, inputResponses mcp.InputResponseMap, requestState string) (*mcp.CallToolResult, error) {
	conn.reqMu.Lock()
	defer conn.reqMu.Unlock()
	if conn.callToolWithInput != nil {
		return conn.callToolWithInput(ctx, name, args, inputResponses, requestState)
	}
	if len(inputResponses) > 0 || requestState != "" {
		return nil, fmt.Errorf("server connection does not support multi-round-trip retries")
	}
	return conn.callTool(ctx, name, args)
}

func closeConnection(conn *connection) {
	if conn == nil {
		return
	}
	if conn.close == nil {
		if conn.quiesce != nil {
			conn.quiesce()
		}
		return
	}

	// Avoid blocking reset/close paths behind a long in-flight request.
	if conn.reqMu.TryLock() {
		defer conn.reqMu.Unlock()
		conn.close() //nolint: errcheck
		if conn.quiesce != nil {
			conn.quiesce()
		}
		return
	}

	go func(c *connection) {
		c.reqMu.Lock()
		defer c.reqMu.Unlock()
		c.close() //nolint: errcheck
		if c.quiesce != nil {
			c.quiesce()
		}
	}(conn)
}

func closeConnectionAndWait(conn *connection) error {
	if conn == nil {
		return nil
	}
	if conn.close == nil {
		if conn.quiesce != nil {
			conn.quiesce()
		}
		return nil
	}
	conn.reqMu.Lock()
	defer conn.reqMu.Unlock()
	err := conn.close()
	if conn.quiesce != nil {
		conn.quiesce()
	}
	return err
}

func serverCredentialIdentity(scfg config.ServerConfig) string {
	if scfg.IsHTTP() && scfg.OAuth && !scfg.HasAuthorizationHeader() {
		return scfg.URL
	}
	return ""
}

func (p *Pool) retireConnectionLocked(server string, conn *connection) chan struct{} {
	if p.retiring == nil {
		p.retiring = make(map[string][]*retiredConnection)
	}
	done := make(chan struct{})
	identity := conn.credentialIdentity
	if identity == "" && p.cfg != nil {
		identity = serverCredentialIdentity(p.cfg.Servers[server])
	}
	retired := &retiredConnection{done: done, credentialIdentity: identity}
	p.retiring[server] = append(p.retiring[server], retired)
	go func() {
		_ = closeConnectionAndWait(conn)
		close(done)
		p.mu.Lock()
		pending := p.retiring[server]
		for i, candidate := range pending {
			if candidate == retired {
				pending = append(pending[:i], pending[i+1:]...)
				break
			}
		}
		if len(pending) == 0 {
			delete(p.retiring, server)
		} else {
			p.retiring[server] = pending
		}
		p.mu.Unlock()
	}()
	return done
}

// BeginCredentialTransition prevents new connections for the named servers,
// then waits for every existing request and connection to finish. Returning
// successfully is the daemon's proof that no old OAuth token writer remains.
func (p *Pool) BeginCredentialTransition(servers []string, token string) error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("credential transition token is required")
	}

	p.mu.Lock()
	if p.transitions == nil {
		p.transitions = make(map[string]string)
	}
	if p.credentialTransitions == nil {
		p.credentialTransitions = make(map[string]string)
	}
	affectedIdentities := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		if owner := p.transitions[server]; owner != "" && owner != token {
			p.mu.Unlock()
			return fmt.Errorf("server %s: credential transition already in progress", server)
		}
		if p.cfg != nil {
			if identity := serverCredentialIdentity(p.cfg.Servers[server]); identity != "" {
				affectedIdentities[identity] = struct{}{}
				if owner := p.credentialTransitions[identity]; owner != "" && owner != token {
					p.mu.Unlock()
					return fmt.Errorf("server %s: credential transition already in progress", server)
				}
			}
		}
	}
	affectedServers := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		affectedServers[server] = struct{}{}
		p.transitions[server] = token
		if conn, ok := p.conns[server]; ok {
			delete(p.conns, server)
			p.retireConnectionLocked(server, conn)
		}
	}
	for identity := range affectedIdentities {
		p.credentialTransitions[identity] = token
	}
	waits := make([]chan struct{}, 0, len(servers))
	seen := make(map[chan struct{}]struct{})
	for server, pending := range p.retiring {
		_, serverAffected := affectedServers[server]
		for _, retired := range pending {
			_, identityAffected := affectedIdentities[retired.credentialIdentity]
			if !serverAffected && (retired.credentialIdentity == "" || !identityAffected) {
				continue
			}
			if _, exists := seen[retired.done]; exists {
				continue
			}
			seen[retired.done] = struct{}{}
			waits = append(waits, retired.done)
		}
	}
	p.mu.Unlock()

	for _, done := range waits {
		<-done
	}
	return nil
}

// HasCredentialTransitions reports whether any credential mutation currently
// owns a server fence in this daemon.
func (p *Pool) HasCredentialTransitions() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.transitions) > 0 || len(p.credentialTransitions) > 0
}

// CredentialTransitionPending reports whether server is fenced by a current
// credential mutation.
func (p *Pool) CredentialTransitionPending(server string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.transitions[server] != "" {
		return true
	}
	if p.cfg == nil {
		return false
	}
	identity := serverCredentialIdentity(p.cfg.Servers[server])
	return identity != "" && p.credentialTransitions[identity] != ""
}

// EndCredentialTransition releases every server fence owned by token.
func (p *Pool) EndCredentialTransition(token string) {
	if p == nil || token == "" {
		return
	}
	p.mu.Lock()
	for server, owner := range p.transitions {
		if owner == token {
			delete(p.transitions, server)
		}
	}
	for identity, owner := range p.credentialTransitions {
		if owner == token {
			delete(p.credentialTransitions, identity)
		}
	}
	p.mu.Unlock()
}

// SetConfig swaps the underlying config without dropping active connections.
func (p *Pool) SetConfig(cfg *config.Config) {
	if p == nil {
		return
	}

	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

func marshalInputSchema(t *mcp.Tool) (json.RawMessage, error) {
	if t == nil || t.InputSchema == nil {
		return nil, nil
	}
	return json.Marshal(t.InputSchema)
}

func marshalOutputSchema(t *mcp.Tool) (json.RawMessage, error) {
	if t == nil || t.OutputSchema == nil {
		return nil, nil
	}
	return json.Marshal(t.OutputSchema)
}

func invalidateToolInfos(conn *connection) {
	if conn == nil {
		return
	}
	conn.toolMu.Lock()
	conn.toolIndex = nil
	conn.toolList = nil
	conn.indexed = false
	conn.toolCacheUntil = time.Time{}
	conn.toolCacheDisabled = false
	conn.toolMu.Unlock()
}

// Close disconnects a specific server.
func (p *Pool) Close(server string) {
	p.mu.Lock()
	conn, ok := p.conns[server]
	if ok {
		delete(p.conns, server)
		p.retireConnectionLocked(server, conn)
	}
	p.mu.Unlock()
}

// CloseAll disconnects all servers and waits for every pending retirement.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*connection)
	for server, conn := range conns {
		p.retireConnectionLocked(server, conn)
	}
	waits := make([]chan struct{}, 0, len(p.retiring))
	for _, pending := range p.retiring {
		for _, retired := range pending {
			waits = append(waits, retired.done)
		}
	}
	p.mu.Unlock()

	for _, done := range waits {
		<-done
	}
}

// Reset swaps the underlying config and drops all active connections.
func (p *Pool) Reset(cfg *config.Config) {
	if p == nil {
		return
	}

	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*connection)
	for server, conn := range conns {
		p.retireConnectionLocked(server, conn)
	}
	p.cfg = cfg
	p.mu.Unlock()
}
