package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/cache"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/oauthclient"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

type cliOAuthStore struct {
	session   *oauthclient.Session
	deleteErr error
	onDelete  func()
}

func issuerBoundCLISession(t *testing.T, token oauth2.Token) *oauthclient.Session {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"config": map[string]any{"issuer": "https://issuer.example"},
		"token":  token,
	})
	if err != nil {
		t.Fatal(err)
	}
	var session oauthclient.Session
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	return &session
}

func isolateAuthCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func successfulDaemonTransition(t *testing.T, req *ipc.Request) (*ipc.Response, error) {
	t.Helper()
	if req.Type == "reload_server" && req.Transition != "" {
		if err := cache.CompleteCredentialTransition(req.Transition); err != nil {
			t.Fatalf("daemon cache transition completion error = %v", err)
		}
	}
	return &ipc.Response{ExitCode: ipc.ExitOK}, nil
}

func (s *cliOAuthStore) Load(string) (*oauthclient.Session, error) {
	if s.session == nil {
		return nil, oauthclient.ErrNotFound
	}
	return s.session, nil
}

func (s *cliOAuthStore) Save(string, *oauthclient.Session) error { return nil }
func (s *cliOAuthStore) Delete(string) error {
	if s.onDelete != nil {
		s.onDelete()
	}
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.session = nil
	return nil
}

func TestAuthStatusIsRedacted(t *testing.T) {
	oldStore := oauthStoreFn
	defer func() { oauthStoreFn = oldStore }()
	store := &cliOAuthStore{session: issuerBoundCLISession(t, oauth2.Token{
		AccessToken: "secret-access-token",
		Expiry:      time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	})}
	oauthStoreFn = func() oauthclient.Store { return store }
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"status", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitOK {
		t.Fatalf("runAuthCommand() = %d, want %d (stderr=%q)", code, ipc.ExitOK, stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("secret-access-token")) {
		t.Fatalf("stdout leaked token: %q", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("2030-01-02T03:04:05Z")) {
		t.Fatalf("stdout = %q, want redacted expiry status", stdout.String())
	}
}

func TestAuthStatusRejectsSessionWithoutIssuerBinding(t *testing.T) {
	oldStore := oauthStoreFn
	defer func() { oauthStoreFn = oldStore }()
	oauthStoreFn = func() oauthclient.Store {
		return &cliOAuthStore{session: &oauthclient.Session{Token: oauth2.Token{
			AccessToken: "unbound-token",
			Expiry:      time.Now().Add(time.Hour),
		}}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"status", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitToolErr {
		t.Fatalf("runAuthCommand() = %d, want %d", code, ipc.ExitToolErr)
	}
	if !strings.Contains(stdout.String(), "issuer binding") ||
		!strings.Contains(stdout.String(), "mcpx auth logout remote") ||
		!strings.Contains(stdout.String(), "mcpx auth login remote") {
		t.Fatalf("stdout = %q, want issuer-binding reauthentication guidance", stdout.String())
	}
	if strings.Contains(stdout.String(), "unbound-token") {
		t.Fatalf("stdout leaked token: %q", stdout.String())
	}
}

func TestAuthStatusRejectsStoredSessionWithDifferentConfiguredScopes(t *testing.T) {
	oldStore := oauthStoreFn
	defer func() { oauthStoreFn = oldStore }()
	oauthStoreFn = func() oauthclient.Store {
		return &cliOAuthStore{session: &oauthclient.Session{Token: oauth2.Token{AccessToken: "old-scope-token"}}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {
			URL:         "https://example.com/mcp",
			OAuth:       true,
			OAuthScopes: []string{"read"},
		},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"status", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitToolErr {
		t.Fatalf("runAuthCommand() = %d, want %d", code, ipc.ExitToolErr)
	}
	if !strings.Contains(stdout.String(), "configured OAuth scopes differ") {
		t.Fatalf("stdout = %q, want reauthentication guidance", stdout.String())
	}
	if strings.Contains(stdout.String(), "old-scope-token") {
		t.Fatalf("stdout leaked token: %q", stdout.String())
	}
}

func TestAuthStatusRejectsExpiredSessionWithoutRefreshGrant(t *testing.T) {
	oldStore := oauthStoreFn
	defer func() { oauthStoreFn = oldStore }()
	oauthStoreFn = func() oauthclient.Store {
		return &cliOAuthStore{session: issuerBoundCLISession(t, oauth2.Token{
			AccessToken: "expired-token",
			Expiry:      time.Now().Add(-time.Hour),
		})}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"status", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitToolErr {
		t.Fatalf("runAuthCommand() = %d, want %d", code, ipc.ExitToolErr)
	}
	if !strings.Contains(stdout.String(), "expired or incomplete") || !strings.Contains(stdout.String(), "mcpx auth login remote") {
		t.Fatalf("stdout = %q, want login guidance", stdout.String())
	}
	if strings.Contains(stdout.String(), "expired-token") {
		t.Fatalf("stdout leaked token: %q", stdout.String())
	}
}

func TestAuthCommandsRejectExplicitAuthorizationPrecedence(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {
			URL:     "https://example.com/mcp",
			OAuth:   true,
			Headers: map[string]string{"authorization": "Bearer explicit"},
		},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"login", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runAuthCommand() = %d, want %d", code, ipc.ExitUsageErr)
	}
	if !strings.Contains(stderr.String(), "explicit Authorization header") {
		t.Fatalf("stderr = %q, want header-precedence guidance", stderr.String())
	}
}

func TestAuthLoginRequiresStoredSessionBeforeReportingSuccess(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{}
	oauthStoreFn = func() oauthclient.Store { return store }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error { return nil }
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return successfulDaemonTransition(t, req)
		}}
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: true,
	}, &stdout, &stderr)
	if code != ipc.ExitToolErr {
		t.Fatalf("runAuthLogin() = %d, want %d (stdout=%q stderr=%q)", code, ipc.ExitToolErr, stdout.String(), stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("Authenticated")) {
		t.Fatalf("stdout reported false success: %q", stdout.String())
	}
}

func TestAuthLogoutReportsDaemonInvalidationFailure(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{session: &oauthclient.Session{Token: oauth2.Token{AccessToken: "secret"}}}
	args := json.RawMessage(`{"account":"current"}`)
	if err := cache.Put("remote", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	oauthStoreFn = func() oauthclient.Store { return store }
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type == "prepare_credential_transition" {
				return &ipc.Response{ExitCode: ipc.ExitOK}, nil
			}
			return nil, errors.New("daemon unavailable")
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"logout", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitInternal {
		t.Fatalf("runAuthCommand(logout) = %d, want %d", code, ipc.ExitInternal)
	}
	if store.session != nil {
		t.Fatal("logout left credential in store after daemon quiescence")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("daemon unavailable")) {
		t.Fatalf("stderr = %q, want daemon invalidation error", stderr.String())
	}
	if _, _, ok := cache.Get("remote", "search", args); ok {
		t.Fatal("cache.Get() hit after failed logout invalidation, want fail-closed miss")
	}
	if err := cache.Put("remote", "search", args, []byte("stale-write"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() after failed invalidation error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); ok {
		t.Fatal("cache accepted a stale write after failed logout invalidation")
	}
}

func TestAuthLoginKeepsResponseCacheFailClosedWhenDaemonInvalidationFails(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{}
	oauthStoreFn = func() oauthclient.Store { return store }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		store.session = issuerBoundCLISession(t, oauth2.Token{AccessToken: "account-b-token"})
		return nil
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type == "prepare_credential_transition" {
				return &ipc.Response{ExitCode: ipc.ExitOK}, nil
			}
			return nil, errors.New("daemon unavailable")
		}}
	}
	args := json.RawMessage(`{"account":"current"}`)
	if err := cache.Put("remote", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: true,
	}, &stdout, &stderr)
	if code != ipc.ExitInternal {
		t.Fatalf("runAuthLogin() = %d, want %d (stderr=%q)", code, ipc.ExitInternal, stderr.String())
	}
	if store.session == nil || store.session.Token.AccessToken != "account-b-token" {
		t.Fatal("login did not store the replacement OAuth session")
	}
	if _, _, ok := cache.Get("remote", "search", args); ok {
		t.Fatal("cache.Get() hit after failed login invalidation, want fail-closed miss")
	}
}

func TestAuthLoginFailureReenablesCacheWhenCredentialsAreUnchanged(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{}
	oauthStoreFn = func() oauthclient.Store { return store }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		return errors.New("browser authorization canceled")
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return successfulDaemonTransition(t, req)
		}}
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: true,
	}, &stdout, &stderr)
	if code != ipc.ExitToolErr {
		t.Fatalf("runAuthLogin() = %d, want %d", code, ipc.ExitToolErr)
	}
	args := json.RawMessage(`{}`)
	if err := cache.Put("remote", "search", args, []byte("unchanged-account"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); !ok {
		t.Fatal("cache remained disabled after login failed without changing credentials")
	}
}

func TestAuthLoginRollsBackTransitionAfterAmbiguousPrepareFailure(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	oauthStoreFn = func() oauthclient.Store { return &cliOAuthStore{} }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		t.Fatal("browser authorization started after daemon prepare failed")
		return nil
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	var requests []string
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			requests = append(requests, req.Type)
			if req.Type == "prepare_credential_transition" {
				return nil, errors.New("lost prepare response")
			}
			if len(requests) == 2 {
				return nil, errors.New("lost rollback response")
			}
			return successfulDaemonTransition(t, req)
		}}
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: true,
	}, &stdout, &stderr)
	if code != ipc.ExitInternal {
		t.Fatalf("runAuthLogin() = %d, want %d", code, ipc.ExitInternal)
	}
	if got, want := strings.Join(requests, ","), "prepare_credential_transition,reload_server,reload_server"; got != want {
		t.Fatalf("daemon requests = %q, want %q", got, want)
	}
	args := json.RawMessage(`{}`)
	if err := cache.Put("remote", "search", args, []byte("safe"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() after rollback error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); !ok {
		t.Fatal("ambiguous prepare failure left the response cache disabled")
	}
}

func TestAuthLoginFailureReleasesTransitionWhenCredentialsChanged(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{}
	oauthStoreFn = func() oauthclient.Store { return store }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		store.session = issuerBoundCLISession(t, oauth2.Token{AccessToken: "partially-stored-token"})
		return errors.New("authorization validation failed")
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return successfulDaemonTransition(t, req)
		}}
	}

	var stdout, stderr bytes.Buffer
	if code := runAuthLogin("remote", config.ServerConfig{URL: "https://example.com/mcp", OAuth: true}, &stdout, &stderr); code != ipc.ExitToolErr {
		t.Fatalf("runAuthLogin() = %d, want %d", code, ipc.ExitToolErr)
	}
	args := json.RawMessage(`{}`)
	if err := cache.Put("remote", "search", args, []byte("current-state"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); !ok {
		t.Fatal("cache remained disabled after failed login reconciled changed credentials")
	}
}

func TestAuthLogoutPrepareRejectionAbortsOnlyItsCacheMarker(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{session: issuerBoundCLISession(t, oauth2.Token{AccessToken: "old-token"})}
	oauthStoreFn = func() oauthclient.Store { return store }
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(*ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitInternal, Stderr: "another transition is active"}, nil
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	if code := runAuthCommand([]string{"logout", "remote"}, cfg, &stdout, &stderr); code != ipc.ExitInternal {
		t.Fatalf("runAuthCommand(logout) = %d, want %d", code, ipc.ExitInternal)
	}
	if store.session == nil {
		t.Fatal("logout deleted credentials after prepare rejection")
	}
	args := json.RawMessage(`{}`)
	if err := cache.Put("remote", "search", args, []byte("unchanged-account"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); !ok {
		t.Fatal("prepare rejection left its cache marker active")
	}
}

func TestAuthLogoutFailureReenablesCacheWhenCredentialsAreUnchanged(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{
		session:   issuerBoundCLISession(t, oauth2.Token{AccessToken: "account-a-token"}),
		deleteErr: errors.New("credential store unavailable"),
	}
	oauthStoreFn = func() oauthclient.Store { return store }
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return successfulDaemonTransition(t, req)
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	code := runAuthCommand([]string{"logout", "remote"}, cfg, &stdout, &stderr)
	if code != ipc.ExitInternal {
		t.Fatalf("runAuthCommand(logout) = %d, want %d", code, ipc.ExitInternal)
	}
	args := json.RawMessage(`{}`)
	if err := cache.Put("remote", "search", args, []byte("account-a"), ipc.ExitOK, time.Hour); err != nil {
		t.Fatalf("cache.Put() error = %v", err)
	}
	if _, _, ok := cache.Get("remote", "search", args); !ok {
		t.Fatal("cache remained disabled after logout failed without changing credentials")
	}
}

func TestAuthLoginReportsSuccessAfterSessionProofAndDaemonInvalidation(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	store := &cliOAuthStore{}
	oauthStoreFn = func() oauthclient.Store { return store }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		store.session = issuerBoundCLISession(t, oauth2.Token{AccessToken: "stored-token"})
		return nil
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	var requests []*ipc.Request
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			requests = append(requests, req)
			if req.Server != "remote" || req.Transition == "" {
				t.Fatalf("credential transition request = %#v", req)
			}
			return successfulDaemonTransition(t, req)
		}}
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: true,
	}, &stdout, &stderr)
	if code != ipc.ExitOK {
		t.Fatalf("runAuthLogin() = %d, want %d (stderr=%q)", code, ipc.ExitOK, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Authenticated")) {
		t.Fatalf("stdout = %q, want authenticated confirmation", stdout.String())
	}
	if len(requests) != 2 || requests[0].Type != "prepare_credential_transition" || requests[1].Type != "reload_server" {
		t.Fatalf("daemon requests = %#v, want prepare then reload", requests)
	}
	if requests[0].Transition != requests[1].Transition {
		t.Fatalf("transition tokens differ: %q and %q", requests[0].Transition, requests[1].Transition)
	}
}

func TestAuthLogoutPreparesDaemonBeforeDeletingCredential(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	prepared := false
	store := &cliOAuthStore{session: issuerBoundCLISession(t, oauth2.Token{AccessToken: "old-token"})}
	store.onDelete = func() {
		if !prepared {
			t.Fatal("credential deleted before daemon acknowledged quiescence")
		}
	}
	oauthStoreFn = func() oauthclient.Store { return store }
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			switch req.Type {
			case "prepare_credential_transition":
				prepared = true
			case "reload_server":
				if !prepared {
					t.Fatal("reload sent before prepare")
				}
			default:
				t.Fatalf("unexpected daemon request: %#v", req)
			}
			return successfulDaemonTransition(t, req)
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}}

	var stdout, stderr bytes.Buffer
	if code := runAuthCommand([]string{"logout", "remote"}, cfg, &stdout, &stderr); code != ipc.ExitOK {
		t.Fatalf("runAuthCommand(logout) = %d, want %d (stderr=%q)", code, ipc.ExitOK, stderr.String())
	}
}

func TestReloadDaemonServerDoesNotRepeatDaemonCacheCompletion(t *testing.T) {
	isolateAuthCache(t)
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	transition, err := cache.BeginCredentialTransition()
	if err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	args := json.RawMessage(`{}`)
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if err := cache.CompleteCredentialTransition(req.Transition); err != nil {
				t.Fatalf("daemon completion error = %v", err)
			}
			if err := cache.Put("remote", "search", args, []byte("fresh"), ipc.ExitOK, time.Hour); err != nil {
				t.Fatalf("fresh daemon cache write error = %v", err)
			}
			return &ipc.Response{ExitCode: ipc.ExitOK}, nil
		}}
	}

	if err := reloadDaemonServer("remote", transition); err != nil {
		t.Fatalf("reloadDaemonServer() error = %v", err)
	}
	content, _, ok := cache.Get("remote", "search", args)
	if !ok || string(content) != "fresh" {
		t.Fatalf("cache after daemon completion = %q, %v; want fresh hit", content, ok)
	}
}

func TestAuthLoginReportsTransitionCleanupFailure(t *testing.T) {
	isolateAuthCache(t)
	oldStore := oauthStoreFn
	oldVerify := verifyHTTPAuthorizationFn
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		oauthStoreFn = oldStore
		verifyHTTPAuthorizationFn = oldVerify
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	oauthStoreFn = func() oauthclient.Store { return &cliOAuthStore{} }
	verifyHTTPAuthorizationFn = func(context.Context, config.ServerConfig, auth.OAuthHandler) error {
		return errors.New("browser authorization canceled")
	}
	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type == "prepare_credential_transition" {
				return &ipc.Response{ExitCode: ipc.ExitOK}, nil
			}
			return nil, errors.New("daemon unavailable during cleanup")
		}}
	}

	var stdout, stderr bytes.Buffer
	code := runAuthLogin("remote", config.ServerConfig{URL: "https://example.com/mcp", OAuth: true}, &stdout, &stderr)
	if code != ipc.ExitInternal {
		t.Fatalf("runAuthLogin() = %d, want %d", code, ipc.ExitInternal)
	}
	for _, message := range []string{"browser authorization canceled", "releasing credential transition", "daemon unavailable during cleanup"} {
		if !strings.Contains(stderr.String(), message) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), message)
		}
	}
}
