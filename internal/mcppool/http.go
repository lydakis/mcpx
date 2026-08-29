package mcppool

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lydakis/mcpx/internal/buildinfo"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/oauthclient"
	"github.com/lydakis/mcpx/internal/safehttp"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const httpSessionCleanupTimeout = 10 * time.Second

func connectHTTP(ctx context.Context, scfg config.ServerConfig) (*connection, error) {
	var oauthHandler auth.OAuthHandler
	if scfg.OAuth && !scfg.HasAuthorizationHeader() {
		var err error
		oauthHandler, err = oauthclient.NewHandler(ctx, oauthclient.HandlerOptions{
			ServerURL:         scfg.URL,
			Store:             oauthclient.NewKeyringStore(),
			Scopes:            scfg.OAuthScopes,
			ClientMetadataURL: scfg.OAuthClientMetadataURL,
		})
		if err != nil {
			return nil, fmt.Errorf("OAuth: %w", err)
		}
	}
	return connectHTTPWithOAuth(ctx, scfg, oauthHandler)
}

func connectHTTPWithOAuth(ctx context.Context, scfg config.ServerConfig, oauthHandler auth.OAuthHandler) (*connection, error) {
	authSource := "none"
	if scfg.HasAuthorizationHeader() {
		authSource = "explicit_header"
	} else if oauthHandler != nil {
		authSource = "oauth_os_store"
	}
	configuredHeaders := scfg.Headers
	if oauthHandler != nil {
		configuredHeaders = withoutBlankAuthorizationHeader(configuredHeaders)
	}
	staticHeaders, err := newStaticHeaderTransport(scfg.URL, configuredHeaders)
	if err != nil {
		return nil, fmt.Errorf("HTTP transport: %w", err)
	}
	httpClient := newOriginBoundHTTPClient(staticHeaders)
	conn := &connection{}
	if oauthHandler != nil {
		conn.quiesce = func() { oauthclient.QuiesceCredentialWrites(oauthHandler) }
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpx", Version: buildinfo.Version()}, &mcp.ClientOptions{
		Capabilities:   &mcp.ClientCapabilities{},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	transport := &capturingTransport{Transport: &mcp.StreamableClientTransport{
		Endpoint:     scfg.URL,
		HTTPClient:   httpClient,
		OAuthHandler: oauthHandler,
	}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		staticHeaders.bindCleanupContext(ctx)
		_ = transport.Close()
		if conn.quiesce != nil {
			conn.quiesce()
		}
		return nil, fmt.Errorf("initializing: %w", err)
	}
	conn.diagnostics = sessionDiagnostics(session, "streamable_http", authSource)

	conn.listTools = func(ctx context.Context) ([]*mcp.Tool, error) {
		tools, cachePolicy, err := listAllToolPages(ctx, session.ListTools)
		if err != nil {
			return nil, err
		}
		setToolCacheDeadline(conn, cachePolicy.deadline, cachePolicy.cacheable, session.InitializeResult().ProtocolVersion >= modernProtocolVersion)
		return tools, nil
	}
	conn.callTool = func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	}
	conn.callToolWithInput = func(ctx context.Context, name string, args map[string]any, inputResponses mcp.InputResponseMap, requestState string) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args, InputResponses: inputResponses, RequestState: requestState})
	}
	conn.close = session.Close
	return conn, nil
}

// VerifyHTTPAuthorization exercises discovery and tool listing with an
// explicitly constructed OAuth handler, then closes the temporary connection.
func VerifyHTTPAuthorization(ctx context.Context, scfg config.ServerConfig, oauthHandler auth.OAuthHandler) error {
	conn, err := connectHTTPWithOAuth(ctx, scfg, oauthHandler)
	if err != nil {
		return err
	}
	defer closeConnection(conn)
	_, err = runListTools(conn, ctx)
	return err
}

type staticHeaderTransport struct {
	base           http.RoundTripper
	headers        map[string]string
	origin         *url.URL
	cleanupMu      sync.RWMutex
	cleanupContext context.Context
}

func newOriginBoundHTTPClient(transport *staticHeaderTransport) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if transport == nil || !sameHTTPOrigin(transport.origin, req.URL) {
				return fmt.Errorf("refusing cross-origin HTTP redirect")
			}
			return nil
		},
	}
}

func (t *staticHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	requestContext := req.Context()
	var cancel context.CancelFunc
	if req.Method == http.MethodDelete {
		t.cleanupMu.RLock()
		boundContext := t.cleanupContext
		t.cleanupMu.RUnlock()
		if boundContext != nil {
			requestContext = boundContext
		}
		requestContext, cancel = context.WithTimeout(requestContext, httpSessionCleanupTimeout)
		defer cancel()
	}
	cloned := req.Clone(requestContext)
	cloned.Header = req.Header.Clone()
	if sameHTTPOrigin(t.origin, req.URL) {
		for name, value := range t.headers {
			cloned.Header.Set(name, value)
		}
	}
	return t.base.RoundTrip(cloned)
}

func (t *staticHeaderTransport) bindCleanupContext(ctx context.Context) {
	t.cleanupMu.Lock()
	t.cleanupContext = ctx
	t.cleanupMu.Unlock()
}

func newStaticHeaderTransport(endpoint string, headers map[string]string) (*staticHeaderTransport, error) {
	origin, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if origin.Scheme == "" || origin.Host == "" {
		return nil, fmt.Errorf("invalid endpoint URL %q", endpoint)
	}
	policy, err := safehttp.NewPolicy(endpoint)
	if err != nil {
		return nil, fmt.Errorf("outbound destination policy: %w", err)
	}
	return &staticHeaderTransport{
		base:    policy.Transport(),
		headers: cloneHeaders(headers),
		origin:  origin,
	}, nil
}

func sameHTTPOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	if !strings.EqualFold(left.Scheme, right.Scheme) || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	return effectiveHTTPPort(left) == effectiveHTTPPort(right)
}

func effectiveHTTPPort(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		return "80"
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return "443"
	}
	return ""
}

func cloneHeaders(headers map[string]string) map[string]string {
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		cloned[name] = value
	}
	return cloned
}

func withoutBlankAuthorizationHeader(headers map[string]string) map[string]string {
	filtered := cloneHeaders(headers)
	for name, value := range filtered {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(value) == "" {
			delete(filtered, name)
		}
	}
	return filtered
}
