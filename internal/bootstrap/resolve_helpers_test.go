package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
)

func TestHTTPStatusErrorErrorStringHandlesNilReceiver(t *testing.T) {
	var nilErr *httpStatusError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("(*httpStatusError)(nil).Error() = %q, want empty", got)
	}

	err := &httpStatusError{statusCode: http.StatusForbidden}
	if got := err.Error(); got != "unexpected HTTP status 403" {
		t.Fatalf("httpStatusError.Error() = %q, want %q", got, "unexpected HTTP status 403")
	}
}

func TestResolveErrorMethodsAndSourceAccessWrapper(t *testing.T) {
	var nilErr *resolveError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("(*resolveError)(nil).Error() = %q, want empty", got)
	}
	if unwrapped := nilErr.Unwrap(); unwrapped != nil {
		t.Fatalf("(*resolveError)(nil).Unwrap() = %v, want nil", unwrapped)
	}

	inner := errors.New("boom")
	typed := &resolveError{kind: resolveErrorSourceAccess, err: inner}
	if got := typed.Error(); got != "boom" {
		t.Fatalf("resolveError.Error() = %q, want %q", got, "boom")
	}
	if unwrapped := typed.Unwrap(); !errors.Is(unwrapped, inner) {
		t.Fatalf("resolveError.Unwrap() = %v, want wrapped boom error", unwrapped)
	}

	if got := wrapResolveSourceAccessError(nil); got != nil {
		t.Fatalf("wrapResolveSourceAccessError(nil) = %v, want nil", got)
	}
	if got := wrapResolveSourceAccessError(inner); !IsSourceAccessError(got) {
		t.Fatalf("wrapResolveSourceAccessError(inner) is source access = false, want true")
	}
}

func TestFetchSourceURLSuccessAndHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = w.Write([]byte(`{"mcpServers":{"demo":{"command":"echo"}}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("  bad request body  "))
	}))
	defer srv.Close()

	okBody, err := fetchSourceURL(context.Background(), srv.URL+"/ok")
	if err != nil {
		t.Fatalf("fetchSourceURL(ok) error = %v, want nil", err)
	}
	if got := string(okBody); !strings.Contains(got, `"mcpServers"`) {
		t.Fatalf("fetchSourceURL(ok) body = %q, want manifest content", got)
	}

	_, err = fetchSourceURL(context.Background(), srv.URL+"/bad")
	if err == nil {
		t.Fatal("fetchSourceURL(status>=400) error = nil, want non-nil")
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("fetchSourceURL(status>=400) error = %v, want *httpStatusError", err)
	}
	if statusErr.statusCode != http.StatusBadRequest {
		t.Fatalf("statusCode = %d, want %d", statusErr.statusCode, http.StatusBadRequest)
	}
	if statusErr.body != "bad request body" {
		t.Fatalf("status body = %q, want %q", statusErr.body, "bad request body")
	}
}

func TestSortedNamesReturnsSortedOrder(t *testing.T) {
	got := sortedNames(map[string]config.ServerConfig{
		"zeta":  {},
		"alpha": {},
		"beta":  {},
	})
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedNames() = %#v, want %#v", got, want)
	}
}

type timeoutErrStub struct{}

func (timeoutErrStub) Error() string { return "timeout" }
func (timeoutErrStub) Timeout() bool { return true }

func TestIsLikelyStreamingFetchError(t *testing.T) {
	if isLikelyStreamingFetchError(nil) {
		t.Fatal("isLikelyStreamingFetchError(nil) = true, want false")
	}
	if !isLikelyStreamingFetchError(context.DeadlineExceeded) {
		t.Fatal("isLikelyStreamingFetchError(context.DeadlineExceeded) = false, want true")
	}
	if !isLikelyStreamingFetchError(timeoutErrStub{}) {
		t.Fatal("isLikelyStreamingFetchError(timeoutErrStub) = false, want true")
	}
	if isLikelyStreamingFetchError(errors.New("boom")) {
		t.Fatal("isLikelyStreamingFetchError(generic) = true, want false")
	}
}

func TestShouldTreatSourceAsDirectMCPURL(t *testing.T) {
	if shouldTreatSourceAsDirectMCPURL("https://example.com/index.html", &httpStatusError{statusCode: http.StatusNotAcceptable}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(non-endpoint) = true, want false")
	}

	if !shouldTreatSourceAsDirectMCPURL("https://example.com/sse", context.DeadlineExceeded) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(sse timeout) = false, want true")
	}

	if !shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", &httpStatusError{statusCode: http.StatusUnauthorized}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(401) = false, want true")
	}

	if !shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", &httpStatusError{statusCode: http.StatusBadRequest}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(400 empty body) = false, want true")
	}

	if !shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", &httpStatusError{statusCode: http.StatusBadRequest, body: `{"jsonrpc":"2.0"}`}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(400 jsonrpc body) = false, want true")
	}

	if shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", &httpStatusError{statusCode: http.StatusBadRequest, body: `{"message":"bad request"}`}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(400 non-jsonrpc body) = true, want false")
	}

	if !shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", &httpStatusError{statusCode: http.StatusBadRequest, body: "Accept must contain 'text/event-stream' for GET requests"}) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(400 text/event-stream body) = false, want true")
	}

	if shouldTreatSourceAsDirectMCPURL("https://example.com/mcp", errors.New("generic fetch failure")) {
		t.Fatal("shouldTreatSourceAsDirectMCPURL(generic error) = true, want false")
	}
}

func TestDecodeTransportValueCoversArrayObjectAndErrorShapes(t *testing.T) {
	srv := &config.ServerConfig{}

	got, err := decodeTransportValue("streamable-http", srv)
	if err != nil {
		t.Fatalf("decodeTransportValue(string) error = %v", err)
	}
	if got != "streamablehttp" {
		t.Fatalf("decodeTransportValue(string) = %q, want %q", got, "streamablehttp")
	}

	got, err = decodeTransportValue([]any{"http"}, srv)
	if err != nil {
		t.Fatalf("decodeTransportValue(array string) error = %v", err)
	}
	if got != "http" {
		t.Fatalf("decodeTransportValue(array string) = %q, want %q", got, "http")
	}

	got, err = decodeTransportValue([]any{
		map[string]any{
			"type":    "stdio",
			"command": "npx",
			"args":    []any{"-y", "@modelcontextprotocol/server-memory"},
		},
	}, srv)
	if err != nil {
		t.Fatalf("decodeTransportValue(array object) error = %v", err)
	}
	if got != "stdio" {
		t.Fatalf("decodeTransportValue(array object) = %q, want %q", got, "stdio")
	}
	if srv.Command != "npx" {
		t.Fatalf("decodeTransportValue(array object) command = %q, want %q", srv.Command, "npx")
	}
	if len(srv.Args) != 2 || srv.Args[1] != "@modelcontextprotocol/server-memory" {
		t.Fatalf("decodeTransportValue(array object) args = %#v, want populated args", srv.Args)
	}

	if _, err := decodeTransportValue([]any{}, &config.ServerConfig{}); err == nil || !strings.Contains(err.Error(), "transport array cannot be empty") {
		t.Fatalf("decodeTransportValue(empty array) error = %v, want empty-array error", err)
	}

	if _, err := decodeTransportValue([]any{123}, &config.ServerConfig{}); err == nil || !strings.Contains(err.Error(), "transport array must contain strings or objects") {
		t.Fatalf("decodeTransportValue(bad array item) error = %v, want array-item-type error", err)
	}

	if _, err := decodeTransportValue(true, &config.ServerConfig{}); err == nil || !strings.Contains(err.Error(), "transport must be a string, object, or array") {
		t.Fatalf("decodeTransportValue(bad type) error = %v, want type error", err)
	}
}

func TestDefaultServerNameFromURLAndFirstHostLabel(t *testing.T) {
	if got := defaultServerNameFromURL("https://mcp.deepwiki.com/mcp"); got != "deepwiki" {
		t.Fatalf("defaultServerNameFromURL(mcp host) = %q, want %q", got, "deepwiki")
	}
	if got := defaultServerNameFromURL("https://chat.openai.com/backend-api/wham/apps"); got != "chat" {
		t.Fatalf("defaultServerNameFromURL(chat host) = %q, want %q", got, "chat")
	}
	if got := defaultServerNameFromURL("not a url"); got != "" {
		t.Fatalf("defaultServerNameFromURL(invalid) = %q, want empty", got)
	}
	if got := defaultServerNameFromURL("/relative/path"); got != "" {
		t.Fatalf("defaultServerNameFromURL(relative) = %q, want empty", got)
	}
	if got := defaultServerNameFromURL("https://mcp.-.-/mcp"); got != "mcp" {
		t.Fatalf("defaultServerNameFromURL(second fallback) = %q, want %q", got, "mcp")
	}
	if got := defaultServerNameFromURL("https://-.-.example/mcp"); got != "example" {
		t.Fatalf("defaultServerNameFromURL(final fallback) = %q, want %q", got, "example")
	}

	if got := firstHostLabel("api.openai.com"); got != "api" {
		t.Fatalf("firstHostLabel(api.openai.com) = %q, want %q", got, "api")
	}
	if got := firstHostLabel("localhost"); got != "localhost" {
		t.Fatalf("firstHostLabel(localhost) = %q, want %q", got, "localhost")
	}
	if got := firstHostLabel("  "); got != "" {
		t.Fatalf("firstHostLabel(blank) = %q, want empty", got)
	}
}

func TestOnlyNamedEntryHandlesEmptyMap(t *testing.T) {
	name, srv := onlyNamedEntry(map[string]config.ServerConfig{})
	if name != "" {
		t.Fatalf("onlyNamedEntry(empty) name = %q, want empty", name)
	}
	if srv.Command != "" || srv.URL != "" || len(srv.Args) != 0 || len(srv.Env) != 0 || len(srv.Headers) != 0 {
		t.Fatalf("onlyNamedEntry(empty) server = %#v, want zero-value ServerConfig", srv)
	}
}

func TestSelectResolvedServerCoversNamedUnnamedAndErrorCases(t *testing.T) {
	named := parsedServerSet{
		named: map[string]config.ServerConfig{
			"github": {Command: "npx"},
			"linear": {URL: "https://example.com/mcp"},
		},
	}

	name, srv, err := selectResolvedServer(named, "github")
	if err != nil {
		t.Fatalf("selectResolvedServer(named exact) error = %v", err)
	}
	if name != "github" || srv.Command != "npx" {
		t.Fatalf("selectResolvedServer(named exact) = (%q, %#v), want github stdio server", name, srv)
	}

	_, _, err = selectResolvedServer(named, "missing")
	if err == nil || !strings.Contains(err.Error(), `server "missing" not found`) {
		t.Fatalf("selectResolvedServer(named missing) error = %v, want not-found error", err)
	}

	_, _, err = selectResolvedServer(named, "")
	if err == nil || !strings.Contains(err.Error(), "manifest includes multiple servers") {
		t.Fatalf("selectResolvedServer(named multi no name) error = %v, want multi-server guidance", err)
	}

	onlyNamed := parsedServerSet{
		named: map[string]config.ServerConfig{
			"only": {Command: "echo"},
		},
	}
	name, srv, err = selectResolvedServer(onlyNamed, "")
	if err != nil {
		t.Fatalf("selectResolvedServer(only named) error = %v", err)
	}
	if name != "only" || srv.Command != "echo" {
		t.Fatalf("selectResolvedServer(only named) = (%q, %#v), want only/echo", name, srv)
	}

	name, srv, err = selectResolvedServer(onlyNamed, "override")
	if err != nil {
		t.Fatalf("selectResolvedServer(only named with override) error = %v", err)
	}
	if name != "override" || srv.Command != "echo" {
		t.Fatalf("selectResolvedServer(only named with override) = (%q, %#v), want override + only server", name, srv)
	}

	unnamedSrv := config.ServerConfig{URL: "https://example.com/mcp"}
	unnamed := parsedServerSet{unnamed: &unnamedSrv}
	_, _, err = selectResolvedServer(unnamed, "")
	if err == nil || !strings.Contains(err.Error(), "manifest defines an unnamed server; pass --name") {
		t.Fatalf("selectResolvedServer(unnamed no name) error = %v, want unnamed guidance", err)
	}

	name, srv, err = selectResolvedServer(unnamed, "inferred")
	if err != nil {
		t.Fatalf("selectResolvedServer(unnamed with name) error = %v", err)
	}
	if name != "inferred" || srv.URL != "https://example.com/mcp" {
		t.Fatalf("selectResolvedServer(unnamed with name) = (%q, %#v), want inferred/http server", name, srv)
	}

	_, _, err = selectResolvedServer(parsedServerSet{}, "")
	if err == nil || !strings.Contains(err.Error(), "manifest does not include any servers") {
		t.Fatalf("selectResolvedServer(empty) error = %v, want empty-manifest error", err)
	}
}
