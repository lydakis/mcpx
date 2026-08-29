package oauthclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/safehttp"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

type capturingOAuthHandler struct {
	challenges []string
}

func (h *capturingOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return nil, nil
}

func (h *capturingOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	h.challenges = append([]string(nil), resp.Header.Values("WWW-Authenticate")...)
	return nil
}

type memoryStore struct {
	session *Session
	saves   int
}

func (s *memoryStore) Load(string) (*Session, error) {
	if s.session == nil {
		return nil, ErrNotFound
	}
	return s.session, nil
}

func (s *memoryStore) Save(_ string, session *Session) error {
	s.session = session
	s.saves++
	return nil
}

func (s *memoryStore) Delete(string) error {
	s.session = nil
	return nil
}

func TestNewHandlerRequiresLoginWhenSessionMissing(t *testing.T) {
	_, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     &memoryStore{},
	})
	if err == nil || !stringsContains(err.Error(), "mcpx auth login") {
		t.Fatalf("NewHandler() error = %v, want login guidance", err)
	}
}

func TestNewHandlerRejectsSavedSessionWithoutIssuerBinding(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{AccessToken: "token"},
	}}
	_, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     store,
	})
	if err == nil || !strings.Contains(err.Error(), "issuer binding") {
		t.Fatalf("NewHandler() error = %v, want issuer-binding guidance", err)
	}
}

func TestNewHandlerBindsRestoredClientCredentialsToSavedIssuer(t *testing.T) {
	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           authServer.URL,
			"authorization_endpoint":           authServer.URL + "/authorize",
			"token_endpoint":                   authServer.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer authServer.Close()

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "http://" + r.Host + "/mcp",
			"authorization_servers": []string{authServer.URL},
		})
	}))
	defer resourceServer.Close()

	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://original-issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{AccessToken: "token"},
	}}
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: resourceServer.URL + "/mcp",
		Store:     store,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, resourceServer.URL+"/mcp", nil)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Www-Authenticate": {`Bearer resource_metadata="` + resourceServer.URL + `/metadata"`},
		},
		Body: io.NopCloser(strings.NewReader("")),
	}
	err = handler.Authorize(context.Background(), req, resp)
	if err == nil || !strings.Contains(err.Error(), "does not match pre-registered credentials issuer") {
		t.Fatalf("Authorize() error = %v, want issuer mismatch", err)
	}
}

func TestSessionHasUsableTokenRequiresValidAccessOrRefreshEndpoint(t *testing.T) {
	valid := sessionFromOAuthWithIssuer(&oauth2.Config{}, &oauth2.Token{
		AccessToken: "valid",
		Expiry:      time.Now().Add(time.Hour),
	}, "https://issuer.example")
	if !SessionHasUsableToken(valid) {
		t.Fatal("SessionHasUsableToken(valid access token) = false, want true")
	}

	unbound := sessionFromOAuth(&oauth2.Config{}, &oauth2.Token{
		AccessToken: "valid",
		Expiry:      time.Now().Add(time.Hour),
	})
	if SessionHasUsableToken(unbound) {
		t.Fatal("SessionHasUsableToken(unbound access token) = true, want false")
	}

	expired := sessionFromOAuthWithIssuer(&oauth2.Config{}, &oauth2.Token{
		AccessToken: "expired",
		Expiry:      time.Now().Add(-time.Hour),
	}, "https://issuer.example")
	if SessionHasUsableToken(expired) {
		t.Fatal("SessionHasUsableToken(expired access token) = true, want false")
	}

	refreshable := sessionFromOAuthWithIssuer(&oauth2.Config{
		Endpoint: oauth2.Endpoint{TokenURL: "https://example.com/token"},
	}, &oauth2.Token{
		AccessToken:  "expired",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}, "https://issuer.example")
	if !SessionHasUsableToken(refreshable) {
		t.Fatal("SessionHasUsableToken(refresh grant) = false, want true")
	}
}

func TestNewHandlerAcceptsClientIDMetadataDocument(t *testing.T) {
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL:         "https://example.com/mcp",
		RedirectURL:       "http://127.0.0.1/callback",
		Store:             &memoryStore{},
		AllowLogin:        true,
		ClientMetadataURL: "https://client.example.com/mcpx.json",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("NewHandler() = nil")
	}
}

func TestNewHandlerRestoresSavedTokenWithoutExposingIt(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{
			AccessToken: "secret-token",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(time.Hour),
		},
	}}
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     store,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}
	token, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token.AccessToken != "secret-token" {
		t.Fatal("restored token mismatch")
	}
}

func TestNewHandlerInteractiveLoginDropsExpiredUnrefreshableTokenSource(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{
			AccessToken: "expired-token",
			Expiry:      time.Now().Add(-time.Hour),
		},
	}}
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL:   "https://example.com/mcp",
		RedirectURL: "http://127.0.0.1/callback",
		Store:       store,
		AllowLogin:  true,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}
	if source != nil {
		t.Fatal("TokenSource() restored an expired unrefreshable grant, want interactive authorization")
	}
}

func TestNewHandlerNoninteractiveRejectsExpiredUnrefreshableGrant(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{
			AccessToken: "expired-token",
			Expiry:      time.Now().Add(-time.Hour),
		},
	}}
	_, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     store,
	})
	if err == nil || !strings.Contains(err.Error(), "mcpx auth login") {
		t.Fatalf("NewHandler() error = %v, want login guidance", err)
	}
}

func TestNewHandlerRejectsStoredSessionWithDifferentConfiguredScopes(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
			Scopes:      []string{"admin"},
		},
		Token: oauth2.Token{AccessToken: "admin-token"},
	}}
	_, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL:   "https://example.com/mcp",
		RedirectURL: "http://127.0.0.1/callback",
		Store:       store,
		Scopes:      []string{"read"},
		AllowLogin:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "scopes differ") {
		t.Fatalf("NewHandler() error = %v, want scope-mismatch guidance", err)
	}
}

func TestNewHandlerAllowsProtocolAddedOfflineAccessScope(t *testing.T) {
	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			RedirectURL: "http://127.0.0.1/callback",
			Scopes:      []string{"read", "offline_access"},
		},
		Token: oauth2.Token{AccessToken: "read-token"},
	}}
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     store,
		Scopes:    []string{"read"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("NewHandler() = nil")
	}
}

func TestSessionScopesCompatibleSplitsConfiguredScopeEntries(t *testing.T) {
	session := &Session{Config: storedOAuthConfig{Scopes: []string{"read", "write"}}}
	if !SessionScopesCompatible(session, []string{" read  write "}) {
		t.Fatal("SessionScopesCompatible() = false, want space-delimited configured scopes to match stored tokens")
	}
	if got := joinScopes([]string{" read  write ", "profile"}); got != "read write profile" {
		t.Fatalf("joinScopes() = %q, want normalized scope tokens", got)
	}
}

func TestNewHandlerRestoredTokenRefreshOutlivesConstructionContext(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-token" {
			t.Fatalf("refresh_token = %q, want refresh-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      tokenServer.URL,
			ClientID:    "client-id",
			Endpoint:    oauth2.Endpoint{TokenURL: tokenServer.URL},
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{
			AccessToken:  "expired-token",
			RefreshToken: "refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(-time.Hour),
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	handler, err := NewHandler(ctx, HandlerOptions{
		ServerURL: tokenServer.URL + "/mcp",
		Store:     store,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	cancel()

	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}
	token, err := source.Token()
	if err != nil {
		t.Fatalf("Token() after construction context cancellation error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Fatalf("refreshed access token = %q, want refreshed-token", token.AccessToken)
	}
}

func TestNewHandlerRestoredTokenRejectsPrivateRefreshForRemoteServer(t *testing.T) {
	var tokenHits atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		tokenHits.Add(1)
	}))
	defer tokenServer.Close()

	store := &memoryStore{session: &Session{
		Config: storedOAuthConfig{
			Issuer:      "https://issuer.example",
			ClientID:    "client-id",
			Endpoint:    oauth2.Endpoint{TokenURL: tokenServer.URL},
			RedirectURL: "http://127.0.0.1/callback",
		},
		Token: oauth2.Token{
			AccessToken:  "expired-token",
			RefreshToken: "refresh-token",
			Expiry:       time.Now().Add(-time.Hour),
		},
	}}
	handler, err := NewHandler(context.Background(), HandlerOptions{
		ServerURL: "https://example.com/mcp",
		Store:     store,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}
	if _, err := source.Token(); err == nil || !strings.Contains(err.Error(), "restricted address") {
		t.Fatalf("Token() error = %v, want restricted-address rejection", err)
	}
	if tokenHits.Load() != 0 {
		t.Fatalf("private token endpoint hits = %d, want 0", tokenHits.Load())
	}
}

func TestScopedOAuthHandlerOverridesRequestedScopesAndPreservesChallengeMetadata(t *testing.T) {
	base := &capturingOAuthHandler{}
	handler := &scopedOAuthHandler{base: base, scopes: "read profile"}
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Www-Authenticate": {`Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource", scope="all"`},
		},
		Body: io.NopCloser(strings.NewReader("")),
	}
	if err := handler.Authorize(context.Background(), httptest.NewRequest(http.MethodPost, "https://example.com/mcp", nil), resp); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if len(base.challenges) != 2 {
		t.Fatalf("challenges = %#v, want configured and server challenges", base.challenges)
	}
	if base.challenges[0] != `Bearer scope="read profile"` {
		t.Fatalf("first challenge = %q, want configured scopes", base.challenges[0])
	}
	if !strings.Contains(base.challenges[1], "resource_metadata=") {
		t.Fatalf("server challenge metadata was not preserved: %#v", base.challenges)
	}
}

func TestSavingTokenSourcePersistsRefresh(t *testing.T) {
	store := &memoryStore{}
	initial := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh"}
	refreshed := &oauth2.Token{AccessToken: "new", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}
	source := newSavingTokenSource(oauth2.StaticTokenSource(refreshed), &oauth2.Config{ClientID: "client"}, initial, "key", store, "https://issuer.example", newCredentialWriteGuard())
	if _, err := source.Token(); err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if store.saves != 1 || store.session.Token.AccessToken != "new" {
		t.Fatalf("store = %+v saves=%d, want refreshed session", store.session, store.saves)
	}
	if store.session.Config.Issuer != "https://issuer.example" {
		t.Fatalf("stored issuer = %q, want refresh to retain binding", store.session.Config.Issuer)
	}
}

func TestSavingTokenSourceCannotPersistAfterCredentialWritesAreQuiesced(t *testing.T) {
	store := &memoryStore{}
	guard := newCredentialWriteGuard()
	source := newSavingTokenSource(
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "new"}),
		&oauth2.Config{ClientID: "client"},
		&oauth2.Token{AccessToken: "old"},
		"key", store, "https://issuer.example", guard,
	)
	guard.Quiesce()

	if _, err := source.Token(); !errors.Is(err, ErrCredentialWritesQuiesced) {
		t.Fatalf("Token() error = %v, want ErrCredentialWritesQuiesced", err)
	}
	if store.saves != 0 {
		t.Fatalf("store saves = %d, want 0 after quiescence", store.saves)
	}
}

func TestPendingOAuthBindingPersistsValidatedIssuer(t *testing.T) {
	store := &memoryStore{}
	binding := &pendingOAuthBinding{store: store, key: "key"}
	binding.recordIssuer("https://issuer.example")
	config := &oauth2.Config{ClientID: "client-id", RedirectURL: "http://127.0.0.1/callback"}
	token := &oauth2.Token{AccessToken: "access-token"}
	if _, err := binding.newTokenSource(context.Background(), config, token); err != nil {
		t.Fatalf("newTokenSource() error = %v", err)
	}
	if err := binding.persist(context.Background(), nil, nil); err != nil {
		t.Fatalf("persist() error = %v", err)
	}
	if store.session == nil || store.session.Config.Issuer != "https://issuer.example" {
		t.Fatalf("stored session = %+v, want validated issuer binding", store.session)
	}
}

func TestPendingOAuthBindingDiscoversIssuerWhenAuthorizationResponseOmitsIt(t *testing.T) {
	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           authServer.URL,
			"authorization_endpoint":           authServer.URL + "/authorize",
			"token_endpoint":                   authServer.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer authServer.Close()

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "http://" + r.Host + "/mcp",
			"authorization_servers": []string{authServer.URL},
		})
	}))
	defer resourceServer.Close()

	store := &memoryStore{}
	policy, err := safehttp.NewPolicy(resourceServer.URL)
	if err != nil {
		t.Fatalf("safehttp.NewPolicy() error = %v", err)
	}
	binding := &pendingOAuthBinding{
		store:  store,
		key:    "key",
		client: policy.Client(0, safehttp.PublicRedirects),
	}
	config := &oauth2.Config{ClientID: "client-id", RedirectURL: "http://127.0.0.1/callback"}
	token := &oauth2.Token{AccessToken: "access-token"}
	if _, err := binding.newTokenSource(context.Background(), config, token); err != nil {
		t.Fatalf("newTokenSource() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, resourceServer.URL+"/mcp", nil)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Www-Authenticate": {`Bearer resource_metadata="` + resourceServer.URL + `/metadata"`},
		},
		Body: io.NopCloser(strings.NewReader("")),
	}
	if err := binding.persist(context.Background(), req, resp); err != nil {
		t.Fatalf("persist() error = %v", err)
	}
	if store.session == nil || store.session.Config.Issuer != authServer.URL {
		t.Fatalf("stored session = %+v, want discovered issuer %q", store.session, authServer.URL)
	}
}

func TestLoopbackCallbackCapturesIssuerAndState(t *testing.T) {
	receiver := &LoopbackReceiver{
		result: make(chan *auth.AuthorizationResult, 1),
		errs:   make(chan error, 1),
	}
	request := httptest.NewRequest("GET", "/callback?code=abc&state=state-1&iss=https%3A%2F%2Fissuer.example", nil)
	recorder := httptest.NewRecorder()
	receiver.handleCallback(recorder, request)
	select {
	case result := <-receiver.result:
		if result.Code != "abc" || result.State != "state-1" || result.Iss != "https://issuer.example" {
			t.Fatalf("result = %+v", result)
		}
	default:
		t.Fatal("callback result was not delivered")
	}
}

func TestLoopbackFetcherRequiresManualBrowserNavigation(t *testing.T) {
	var output bytes.Buffer
	receiver := &LoopbackReceiver{
		result: make(chan *auth.AuthorizationResult, 1),
		errs:   make(chan error, 1),
		output: &output,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := receiver.Fetch(ctx, &auth.AuthorizationArgs{URL: "https://authorization.example/login?state=opaque"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context canceled", err)
	}
	if !strings.Contains(output.String(), "https://authorization.example/login?state=opaque") {
		t.Fatalf("Fetch() output = %q, want authorization URL", output.String())
	}
}

func stringsContains(value, part string) bool {
	return len(value) >= len(part) && strings.Contains(value, part)
}
