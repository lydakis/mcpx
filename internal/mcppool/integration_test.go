package mcppool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

const (
	stdioHelperEnv       = "GO_WANT_MCPX_STDIO_HELPER"
	legacyHelperEnv      = "GO_WANT_MCPX_LEGACY_HELPER"
	unsupportedHelperEnv = "GO_WANT_MCPX_UNSUPPORTED_HELPER"
	stdioExitMarkerEnv   = "GO_WANT_MCPX_STDIO_EXIT_MARKER"
	modernProtocol       = "2026-07-28"
	legacyProtocol       = "2025-11-25"
)

type staticTestOAuthHandler struct {
	token *oauth2.Token
}

func (h *staticTestOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(h.token), nil
}

func (*staticTestOAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return nil
}

func TestPoolStdioIntegrationListToolsAndCallTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"stdio": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestMCPXStdioHelperProcess", "--", "stdio-helper"},
				Env: map[string]string{
					stdioHelperEnv: "1",
				},
			},
		},
	}

	pool := New(cfg)
	defer pool.CloseAll()

	tools, err := pool.ListTools(ctx, "stdio")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if tools[0].Name != "echo_tool" {
		t.Fatalf("tools[0].Name = %q, want %q", tools[0].Name, "echo_tool")
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("tools[0].OutputSchema is empty, want declared schema")
	}
	if !strings.Contains(string(tools[0].OutputSchema), `"echo"`) {
		t.Fatalf("tools[0].OutputSchema = %s, want field echo", tools[0].OutputSchema)
	}

	result, err := pool.CallTool(ctx, "stdio", "echo_tool", json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["echo"] != "hello" {
		t.Fatalf("StructuredContent[echo] = %v, want %q", typed["echo"], "hello")
	}
	if typed["protocol"] != modernProtocol {
		t.Fatalf("StructuredContent[protocol] = %v, want %q", typed["protocol"], modernProtocol)
	}
}

func TestPoolStdioIntegrationFallsBackToLegacyProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"stdio": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestMCPXStdioHelperProcess", "--", "stdio-helper"},
			Env: map[string]string{
				stdioHelperEnv:  "1",
				legacyHelperEnv: "1",
			},
		},
	}}

	pool := New(cfg)
	defer pool.CloseAll()

	result, err := pool.CallTool(ctx, "stdio", "echo_tool", json.RawMessage(`{"query":"legacy"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["protocol"] != legacyProtocol {
		t.Fatalf("StructuredContent[protocol] = %v, want %q", typed["protocol"], legacyProtocol)
	}
}

func TestConnectStdioClosesTransportAfterUnsupportedNegotiation(t *testing.T) {
	marker := t.TempDir() + "/helper-exited"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := connectStdio(ctx, config.ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPXStdioHelperProcess", "--", "stdio-helper"},
		Env: map[string]string{
			stdioHelperEnv:       "1",
			legacyHelperEnv:      "1",
			unsupportedHelperEnv: "1",
			stdioExitMarkerEnv:   marker,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("connectStdio() error = %v, want unsupported protocol version", err)
	}

	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		if _, statErr := os.Stat(marker); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stdio helper did not exit after failed negotiation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnectHTTPClosesTransportAfterUnsupportedNegotiation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-http-helper", Version: "1.0.0"}, nil)
	forceUnsupportedProtocol(mcpServer)
	base := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	var deletes atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		base.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	_, err := connectHTTP(ctx, config.ServerConfig{URL: httpServer.URL})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("connectHTTP() error = %v, want unsupported protocol version", err)
	}
	if got := deletes.Load(); got != 1 {
		t.Fatalf("session cleanup DELETEs = %d, want 1 after failed negotiation", got)
	}
}

func TestConnectHTTPBoundsFailedNegotiationCleanupByCallerDeadline(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-http-helper", Version: "1.0.0"}, nil)
	forceUnsupportedProtocol(mcpServer)
	base := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	deleteStarted := make(chan struct{})
	var deleteOnce sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteOnce.Do(func() { close(deleteStarted) })
			select {
			case <-r.Context().Done():
			case <-time.After(500 * time.Millisecond):
			}
			return
		}
		base.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := connectHTTP(ctx, config.ServerConfig{URL: httpServer.URL})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("connectHTTP() error = %v, want unsupported protocol version", err)
	}
	select {
	case <-deleteStarted:
	default:
		t.Fatal("failed negotiation did not attempt session cleanup")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("failed negotiation cleanup took %s, want caller deadline to bound it", elapsed)
	}
}

func TestPoolHTTPIntegrationListToolsCallToolAndHeaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		headerMu   sync.Mutex
		seenHeader string
	)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-http-helper", Version: "1.0.0"}, nil)
	mcpServer.AddTool(&mcp.Tool{
		Name:        "sum_values",
		Description: "Returns the sum of a and b",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"a":{"type":"number"},"b":{"type":"number"}},
			"required":["a","b"]
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"total":{"type":"number"}},
			"required":["total"]
		}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			A float64 `json:"a"`
			B float64 `json:"b"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"total":    args.A + args.B,
			"protocol": request.ProtocolVersion(),
		}}, nil
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerMu.Lock()
		seenHeader = r.Header.Get("X-MCPX-Test")
		headerMu.Unlock()
		mcpHandler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"http": {
				URL: httpServer.URL,
				Headers: map[string]string{
					"X-MCPX-Test": "integration",
				},
			},
		},
	}

	pool := New(cfg)
	defer pool.CloseAll()

	tools, err := pool.ListTools(ctx, "http")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sum_values" {
		t.Fatalf("ListTools() tools = %#v, want sum_values", tools)
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("tools[0].OutputSchema is empty, want declared schema")
	}

	result, err := pool.CallTool(ctx, "http", "sum_values", json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["total"] != float64(5) {
		t.Fatalf("StructuredContent[total] = %v, want 5", typed["total"])
	}
	if typed["protocol"] != modernProtocol {
		t.Fatalf("StructuredContent[protocol] = %v, want %q", typed["protocol"], modernProtocol)
	}

	headerMu.Lock()
	gotHeader := seenHeader
	headerMu.Unlock()
	if gotHeader != "integration" {
		t.Fatalf("seen header = %q, want %q", gotHeader, "integration")
	}
}

func TestHTTPAuthorizationRemainsBoundToConfiguredOriginAcrossRedirects(t *testing.T) {
	var crossOriginAuthorization, crossOriginAPIKey string
	crossOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crossOriginAuthorization = r.Header.Get("Authorization")
		crossOriginAPIKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer crossOrigin.Close()

	var sameOriginAuthorization, sameOriginAPIKey string
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same-start":
			http.Redirect(w, r, "/same-finish", http.StatusFound)
		case "/same-finish":
			sameOriginAuthorization = r.Header.Get("Authorization")
			sameOriginAPIKey = r.Header.Get("X-API-Key")
			w.WriteHeader(http.StatusNoContent)
		case "/cross-start":
			http.Redirect(w, r, crossOrigin.URL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	transport, err := newStaticHeaderTransport(origin.URL, map[string]string{
		"Authorization": "Bearer endpoint-secret",
		"X-API-Key":     "api-secret",
	})
	if err != nil {
		t.Fatalf("newStaticHeaderTransport() error = %v", err)
	}
	client := newOriginBoundHTTPClient(transport)

	resp, err := client.Get(origin.URL + "/same-start")
	if err != nil {
		t.Fatalf("same-origin redirect error = %v", err)
	}
	resp.Body.Close()
	if sameOriginAuthorization != "Bearer endpoint-secret" || sameOriginAPIKey != "api-secret" {
		t.Fatalf("same-origin headers = (%q, %q), want configured values", sameOriginAuthorization, sameOriginAPIKey)
	}

	req, err := http.NewRequest(http.MethodGet, origin.URL+"/cross-start", nil)
	if err != nil {
		t.Fatal(err)
	}
	// OAuth adds Authorization to the request rather than the static header
	// transport, so cover both credential paths on redirects.
	req.Header.Set("Authorization", "Bearer oauth-secret")
	resp, err = client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("cross-origin redirect succeeded, want origin-bound rejection")
	}
	if crossOriginAuthorization != "" || crossOriginAPIKey != "" {
		t.Fatalf("cross-origin headers leaked: Authorization=%q X-API-Key=%q", crossOriginAuthorization, crossOriginAPIKey)
	}
}

func TestHTTPBlankStaticAuthorizationDoesNotEraseOAuthBearer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var authorization string
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-oauth-helper", Version: "1.0.0"}, nil)
	mcpServer.AddTool(&mcp.Tool{Name: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = r.Header.Get("Authorization")
		mu.Unlock()
		mcpHandler.ServeHTTP(w, r)
	}))
	defer server.Close()

	conn, err := connectHTTPWithOAuth(ctx, config.ServerConfig{
		URL:     server.URL,
		OAuth:   true,
		Headers: map[string]string{"Authorization": ""},
	}, &staticTestOAuthHandler{token: &oauth2.Token{AccessToken: "oauth-token", TokenType: "Bearer"}})
	if err != nil {
		t.Fatalf("connectHTTPWithOAuth() error = %v", err)
	}
	defer closeConnection(conn)
	if _, err := runListTools(conn, ctx); err != nil {
		t.Fatalf("runListTools() error = %v", err)
	}

	mu.Lock()
	got := authorization
	mu.Unlock()
	if got != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q, want OAuth bearer token", got)
	}
}

func TestPoolHTTPIntegrationFallsBackToLegacyProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-legacy-helper", Version: "1.0.0"}, nil)
	forceLegacyProtocol(mcpServer)
	mcpServer.AddTool(&mcp.Tool{
		Name:        "protocol_version",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"protocol":{"type":"string"}},
			"required":["protocol"]
		}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"protocol": request.ProtocolVersion(),
		}}, nil
	})

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil))
	defer httpServer.Close()

	pool := New(&config.Config{Servers: map[string]config.ServerConfig{
		"http": {URL: httpServer.URL},
	}})
	defer pool.CloseAll()

	result, err := pool.CallTool(ctx, "http", "protocol_version", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	typed, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	if typed["protocol"] != legacyProtocol {
		t.Fatalf("StructuredContent[protocol] = %v, want %q", typed["protocol"], legacyProtocol)
	}
}

func TestPoolHTTPIntegrationMultiRoundTripRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-mrtr-helper", Version: "1.0.0"}, nil)
	mcpServer.AddTool(&mcp.Tool{Name: "deploy", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if len(request.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{Message: "Deploy?"}},
				RequestState:  "opaque-deploy-state",
			}, nil
		}
		response, ok := request.Params.InputResponses["confirm"].(*mcp.ElicitResult)
		if !ok || response.Action != "accept" || request.Params.RequestState != "opaque-deploy-state" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "invalid retry"}}}, nil
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"deployed": true}}, nil
	})

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer httpServer.Close()

	pool := New(&config.Config{Servers: map[string]config.ServerConfig{"http": {URL: httpServer.URL}}})
	defer pool.CloseAll()
	info, err := pool.ToolInfoByName(ctx, "http", "deploy")
	if err != nil {
		t.Fatalf("ToolInfoByName() error = %v", err)
	}
	first, err := pool.CallToolWithInfo(ctx, "http", info, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	if !first.NeedsInput() || first.RequestState != "opaque-deploy-state" {
		t.Fatalf("first result = %+v, want input required", first)
	}

	responses := json.RawMessage(`{"confirm":{"action":"accept"}}`)
	final, err := pool.CallToolWithInfoAndInput(ctx, "http", info, json.RawMessage(`{}`), responses, first.RequestState)
	if err != nil {
		t.Fatalf("retry call error = %v", err)
	}
	if final.NeedsInput() || final.IsError {
		t.Fatalf("final result = %+v", final)
	}
	if got := final.StructuredContent.(map[string]any)["deployed"]; got != true {
		t.Fatalf("deployed = %v, want true", got)
	}
}

func TestPoolStdioIntegrationInvalidCommandFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"broken": {
				Command: "mcpx-this-command-does-not-exist",
			},
		},
	}
	pool := New(cfg)
	defer pool.CloseAll()

	if _, err := pool.ListTools(ctx, "broken"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil for invalid command")
	}
}

func TestPoolHTTPIntegrationUnavailableServerFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcpx-http-helper", Version: "1.0.0"}, nil)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil))
	url := httpServer.URL
	httpServer.Close()

	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"http": {URL: url},
		},
	}
	pool := New(cfg)
	defer pool.CloseAll()

	if _, err := pool.ListTools(ctx, "http"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil for unavailable server")
	}
}

func TestMCPXStdioHelperProcess(t *testing.T) {
	if os.Getenv(stdioHelperEnv) != "1" {
		return
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "mcpx-stdio-helper", Version: "1.0.0"}, nil)
	if os.Getenv(legacyHelperEnv) == "1" {
		forceLegacyProtocol(s)
	}
	if os.Getenv(unsupportedHelperEnv) == "1" {
		forceUnsupportedProtocol(s)
	}
	s.AddTool(&mcp.Tool{
		Name:        "echo_tool",
		Description: "Echoes query",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"query":{"type":"string"}},
			"required":["query"]
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"echo":{"type":"string"},"protocol":{"type":"string"}},
			"required":["echo","protocol"]
		}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"echo":     args.Query,
			"protocol": request.ProtocolVersion(),
		}}, nil
	})

	ctx := context.Background()
	if os.Getenv(unsupportedHelperEnv) == "1" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "serve stdio helper: %v\n", err)
		os.Exit(1)
	}
	if marker := os.Getenv(stdioExitMarkerEnv); marker != "" {
		_ = os.WriteFile(marker, []byte("exited"), 0600)
	}
	os.Exit(0)
}

func forceLegacyProtocol(server *mcp.Server) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "server/discover" {
				return nil, &jsonrpc.Error{
					Code:    jsonrpc.CodeMethodNotFound,
					Message: "method not found",
				}
			}
			return next(ctx, method, request)
		}
	})
}

func forceUnsupportedProtocol(server *mcp.Server) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			if err == nil && method == "initialize" {
				result.(*mcp.InitializeResult).ProtocolVersion = "1900-01-01"
			}
			return result, err
		}
	})
}
