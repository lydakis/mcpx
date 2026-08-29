package oauthclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/lydakis/mcpx/internal/safehttp"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var ErrCredentialWritesQuiesced = errors.New("OAuth credential writes are quiesced")

// CredentialKey returns a stable, non-secret key for a protected resource.
func CredentialKey(serverURL string) string {
	return fmt.Sprintf("oauth:%x", sha256.Sum256([]byte(serverURL)))
}

// HandlerOptions configures an MCP OAuth authorization-code handler.
type HandlerOptions struct {
	ServerURL         string
	RedirectURL       string
	Fetcher           auth.AuthorizationCodeFetcher
	Store             Store
	Scopes            []string
	ClientMetadataURL string
	AllowLogin        bool
}

// NewHandler restores a saved session when present and configures secure
// refresh persistence. Interactive authorization only occurs when AllowLogin
// is true.
func NewHandler(ctx context.Context, opts HandlerOptions) (auth.OAuthHandler, error) {
	if opts.ServerURL == "" {
		return nil, fmt.Errorf("OAuth server URL is required")
	}
	if opts.Store == nil {
		opts.Store = NewKeyringStore()
	}
	if opts.Fetcher == nil {
		opts.Fetcher = func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, fmt.Errorf("interactive authorization required; run mcpx auth login for this server")
		}
	}
	outboundPolicy, err := safehttp.NewPolicy(opts.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("OAuth outbound policy: %w", err)
	}
	httpClient := outboundPolicy.Client(0, safehttp.PublicRedirects)
	fetcher := opts.Fetcher
	opts.Fetcher = func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		if args == nil {
			return nil, fmt.Errorf("OAuth authorization request is missing")
		}
		if err := outboundPolicy.ValidateURL(ctx, args.URL); err != nil {
			return nil, fmt.Errorf("refusing authorization URL: %w", err)
		}
		return fetcher(ctx, args)
	}

	writeGuard := newCredentialWriteGuard()
	key := CredentialKey(opts.ServerURL)
	stored, err := opts.Store.Load(key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if stored != nil && !SessionScopesCompatible(stored, opts.Scopes) {
		return nil, fmt.Errorf("configured OAuth scopes differ from the saved session; run mcpx auth logout, then mcpx auth login")
	}
	if stored != nil && strings.TrimSpace(stored.Config.Issuer) == "" {
		return nil, fmt.Errorf("saved OAuth session has no authorization-server issuer binding; run mcpx auth logout, then mcpx auth login")
	}
	restoreStoredToken := stored != nil && SessionHasUsableToken(stored)
	if stored != nil && !restoreStoredToken && !opts.AllowLogin {
		return nil, fmt.Errorf("saved OAuth grant is expired or incomplete; run mcpx auth login for this server")
	}

	redirectURL := opts.RedirectURL
	var initial oauth2.TokenSource
	var preregistered *oauthex.ClientCredentials
	if stored != nil {
		cfg := stored.oauthConfig()
		if redirectURL == "" {
			redirectURL = cfg.RedirectURL
		}
		if restoreStoredToken {
			tokenContext := context.WithValue(persistentOAuthContext(ctx), oauth2.HTTPClient, httpClient)
			initial = newSavingTokenSource(cfg.TokenSource(tokenContext, &stored.Token), cfg, &stored.Token, key, opts.Store, stored.Config.Issuer, writeGuard)
		}
		preregistered = &oauthex.ClientCredentials{ClientID: cfg.ClientID, Issuer: stored.Config.Issuer}
		if cfg.ClientSecret != "" {
			preregistered.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: cfg.ClientSecret}
		}
	}
	if redirectURL == "" {
		return nil, fmt.Errorf("OAuth redirect URL is required; run mcpx auth login for this server")
	}

	var pendingBinding *pendingOAuthBinding
	newTokenSource := func(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
		issuer := ""
		if stored != nil {
			issuer = stored.Config.Issuer
		}
		if err := writeGuard.Save(opts.Store, key, sessionFromOAuthWithIssuer(cfg, token, issuer)); err != nil {
			return nil, err
		}
		tokenContext := context.WithValue(persistentOAuthContext(ctx), oauth2.HTTPClient, httpClient)
		return newSavingTokenSource(cfg.TokenSource(tokenContext, token), cfg, token, key, opts.Store, issuer, writeGuard), nil
	}
	if stored == nil {
		pendingBinding = &pendingOAuthBinding{store: opts.Store, key: key, writeGuard: writeGuard, client: httpClient}
		fetcher := opts.Fetcher
		opts.Fetcher = func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			result, err := fetcher(ctx, args)
			if err == nil && result != nil {
				pendingBinding.recordIssuer(result.Iss)
			}
			return result, err
		}
		newTokenSource = pendingBinding.newTokenSource
	}

	config := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirectURL,
		AuthorizationCodeFetcher: opts.Fetcher,
		RequestRefreshToken:      true,
		InitialTokenSource:       initial,
		PreregisteredClient:      preregistered,
		NewTokenSource:           newTokenSource,
		Client:                   httpClient,
	}
	if opts.ClientMetadataURL != "" && preregistered == nil {
		config.ClientIDMetadataDocumentConfig = &auth.ClientIDMetadataDocumentConfig{URL: opts.ClientMetadataURL}
	}
	if preregistered == nil {
		if !opts.AllowLogin {
			return nil, fmt.Errorf("no saved OAuth session; run mcpx auth login for this server")
		}
		config.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs:            []string{redirectURL},
				TokenEndpointAuthMethod: "none",
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				ClientName:              "mcpx",
				Scope:                   joinScopes(opts.Scopes),
			},
		}
	}
	sdkHandler, err := auth.NewAuthorizationCodeHandler(config)
	if err != nil {
		return nil, err
	}
	var handler auth.OAuthHandler = sdkHandler
	if pendingBinding != nil {
		handler = &issuerPersistingOAuthHandler{base: handler, binding: pendingBinding}
	}
	if scopes := joinScopes(opts.Scopes); scopes != "" {
		handler = &scopedOAuthHandler{base: handler, scopes: scopes}
	}
	return &credentialGuardedOAuthHandler{base: handler, writeGuard: writeGuard}, nil
}

type credentialWriteGuard struct {
	mu       sync.RWMutex
	quiesced bool
}

func newCredentialWriteGuard() *credentialWriteGuard {
	return &credentialWriteGuard{}
}

func (g *credentialWriteGuard) Save(store Store, key string, session *Session) error {
	if g == nil {
		return store.Save(key, session)
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.quiesced {
		return ErrCredentialWritesQuiesced
	}
	return store.Save(key, session)
}

func (g *credentialWriteGuard) Quiesce() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.quiesced = true
	g.mu.Unlock()
}

type credentialGuardedOAuthHandler struct {
	base       auth.OAuthHandler
	writeGuard *credentialWriteGuard
}

func (h *credentialGuardedOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.base.TokenSource(ctx)
}

func (h *credentialGuardedOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	return h.base.Authorize(ctx, req, resp)
}

func (h *credentialGuardedOAuthHandler) QuiesceCredentialWrites() {
	h.writeGuard.Quiesce()
}

// QuiesceCredentialWrites prevents an OAuth handler from persisting any token
// after this call returns, including from SDK reconnect goroutines that outlive
// transport Close.
func QuiesceCredentialWrites(handler auth.OAuthHandler) {
	if guarded, ok := handler.(interface{ QuiesceCredentialWrites() }); ok {
		guarded.QuiesceCredentialWrites()
	}
}

type pendingOAuthBinding struct {
	mu            sync.Mutex
	issuer        string
	pendingConfig *oauth2.Config
	pendingToken  *oauth2.Token
	pendingSource *savingTokenSource
	store         Store
	key           string
	writeGuard    *credentialWriteGuard
	client        *http.Client
}

func (b *pendingOAuthBinding) recordIssuer(issuer string) {
	b.mu.Lock()
	b.issuer = strings.TrimSpace(issuer)
	b.mu.Unlock()
}

func (b *pendingOAuthBinding) newTokenSource(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
	source := newSavingTokenSource(cfg.TokenSource(ctx, token), cfg, token, b.key, b.store, "", b.writeGuard)
	b.mu.Lock()
	b.pendingConfig = cfg
	b.pendingToken = token
	b.pendingSource = source
	b.mu.Unlock()
	return source, nil
}

func (b *pendingOAuthBinding) persist(ctx context.Context, req *http.Request, resp *http.Response) error {
	b.mu.Lock()
	issuer := b.issuer
	config := b.pendingConfig
	token := b.pendingToken
	source := b.pendingSource
	b.mu.Unlock()
	if config == nil || token == nil || source == nil {
		return nil
	}
	if issuer == "" {
		if b.client == nil {
			return fmt.Errorf("binding OAuth client credentials to issuer: protected HTTP client is missing")
		}
		var err error
		issuer, err = authorizationIssuer(ctx, req, resp, b.client)
		if err != nil {
			return fmt.Errorf("binding OAuth client credentials to issuer: %w", err)
		}
	}
	if strings.TrimSpace(issuer) == "" {
		return fmt.Errorf("binding OAuth client credentials to issuer: authorization server returned an empty issuer")
	}
	source.setIssuer(issuer)
	if err := b.writeGuard.Save(b.store, b.key, sessionFromOAuthWithIssuer(config, token, issuer)); err != nil {
		return err
	}
	b.mu.Lock()
	b.issuer = ""
	b.pendingConfig = nil
	b.pendingToken = nil
	b.pendingSource = nil
	b.mu.Unlock()
	return nil
}

type issuerPersistingOAuthHandler struct {
	base    auth.OAuthHandler
	binding *pendingOAuthBinding
}

func (h *issuerPersistingOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.base.TokenSource(ctx)
}

func (h *issuerPersistingOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	if err := h.base.Authorize(ctx, req, resp); err != nil {
		return err
	}
	return h.binding.persist(ctx, req, resp)
}

func authorizationIssuer(ctx context.Context, req *http.Request, resp *http.Response, client *http.Client) (string, error) {
	if req == nil || req.URL == nil || resp == nil {
		return "", fmt.Errorf("missing OAuth challenge request or response")
	}
	if client == nil {
		return "", fmt.Errorf("protected HTTP client is required")
	}
	challenges, err := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	if err != nil {
		return "", fmt.Errorf("parsing WWW-Authenticate: %w", err)
	}
	resourceURL := req.URL.String()
	for _, candidate := range protectedResourceMetadataCandidates(challenges, resourceURL) {
		metadata, err := oauthex.GetProtectedResourceMetadata(ctx, candidate.url, candidate.resource, client)
		if err != nil || metadata == nil {
			continue
		}
		if len(metadata.AuthorizationServers) == 0 {
			return "", fmt.Errorf("protected resource metadata has no authorization servers")
		}
		return resolvedAuthorizationIssuer(ctx, metadata.AuthorizationServers[0], client)
	}
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return "", fmt.Errorf("parsing MCP server URL: %w", err)
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return resolvedAuthorizationIssuer(ctx, parsed.String(), client)
}

type resourceMetadataCandidate struct {
	url      string
	resource string
}

func protectedResourceMetadataCandidates(challenges []oauthex.Challenge, resourceURL string) []resourceMetadataCandidate {
	var candidates []resourceMetadataCandidate
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" && challenge.Params["resource_metadata"] != "" {
			candidates = append(candidates, resourceMetadataCandidate{url: challenge.Params["resource_metadata"], resource: resourceURL})
			break
		}
	}
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return candidates
	}
	metadataURL := *parsed
	metadataURL.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(parsed.Path, "/")
	candidates = append(candidates, resourceMetadataCandidate{url: metadataURL.String(), resource: resourceURL})
	metadataURL.Path = "/.well-known/oauth-protected-resource"
	parsed.Path = ""
	candidates = append(candidates, resourceMetadataCandidate{url: metadataURL.String(), resource: parsed.String()})
	return candidates
}

func resolvedAuthorizationIssuer(ctx context.Context, issuerURL string, client *http.Client) (string, error) {
	metadata, err := auth.GetAuthServerMetadata(ctx, issuerURL, client)
	if err != nil {
		return "", fmt.Errorf("discovering authorization server metadata: %w", err)
	}
	if metadata == nil {
		return issuerURL, nil
	}
	return metadata.Issuer, nil
}

// scopedOAuthHandler prepends the configured scope set to the server's
// challenge. The SDK otherwise derives authorization scopes only from the
// challenge or protected-resource metadata, leaving mcpx's explicit scope
// configuration unused for authorization requests.
type scopedOAuthHandler struct {
	base   auth.OAuthHandler
	scopes string
}

func (h *scopedOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.base.TokenSource(ctx)
}

func (h *scopedOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	if resp == nil || strings.TrimSpace(h.scopes) == "" {
		return h.base.Authorize(ctx, req, resp)
	}
	cloned := new(http.Response)
	*cloned = *resp
	cloned.Header = resp.Header.Clone()
	existing := cloned.Header.Values("WWW-Authenticate")
	cloned.Header.Del("WWW-Authenticate")
	cloned.Header.Add("WWW-Authenticate", fmt.Sprintf("Bearer scope=%q", h.scopes))
	for _, value := range existing {
		cloned.Header.Add("WWW-Authenticate", value)
	}
	return h.base.Authorize(ctx, req, cloned)
}

func persistentOAuthContext(ctx context.Context) context.Context {
	persistent := context.Background()
	if ctx == nil {
		return persistent
	}
	if client, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && client != nil {
		return context.WithValue(persistent, oauth2.HTTPClient, client)
	}
	return persistent
}

func joinScopes(scopes []string) string {
	tokens := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		tokens = append(tokens, strings.Fields(scope)...)
	}
	return strings.Join(tokens, " ")
}

func normalizedScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	for _, configured := range scopes {
		for _, scope := range strings.Fields(configured) {
			if scope != "offline_access" {
				seen[scope] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func sameScopes(left, right []string) bool {
	a := normalizedScopes(left)
	b := normalizedScopes(right)
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// SessionScopesCompatible reports whether a saved OAuth grant can satisfy the
// explicitly configured scopes. An empty configured set accepts the grant's
// discovered scopes, matching NewHandler behavior.
func SessionScopesCompatible(session *Session, configured []string) bool {
	if session == nil {
		return false
	}
	return len(normalizedScopes(configured)) == 0 || sameScopes(configured, session.Config.Scopes)
}

// SessionHasIssuerBinding reports whether a saved OAuth grant is bound to the
// authorization server that issued its client credentials.
func SessionHasIssuerBinding(session *Session) bool {
	return session != nil && strings.TrimSpace(session.Config.Issuer) != ""
}

// SessionHasUsableToken reports whether a saved session can authenticate now
// or refresh without another interactive login.
func SessionHasUsableToken(session *Session) bool {
	if !SessionHasIssuerBinding(session) {
		return false
	}
	if session.Token.Valid() {
		return true
	}
	return strings.TrimSpace(session.Token.RefreshToken) != "" &&
		strings.TrimSpace(session.Config.Endpoint.TokenURL) != ""
}

type savingTokenSource struct {
	mu           sync.Mutex
	source       oauth2.TokenSource
	config       *oauth2.Config
	store        Store
	key          string
	accessToken  string
	refreshToken string
	issuer       string
	writeGuard   *credentialWriteGuard
}

func newSavingTokenSource(source oauth2.TokenSource, config *oauth2.Config, initial *oauth2.Token, key string, store Store, issuer string, writeGuard *credentialWriteGuard) *savingTokenSource {
	s := &savingTokenSource{source: source, config: config, key: key, store: store, issuer: issuer, writeGuard: writeGuard}
	if initial != nil {
		s.accessToken = initial.AccessToken
		s.refreshToken = initial.RefreshToken
	}
	return s
}

func (s *savingTokenSource) setIssuer(issuer string) {
	s.mu.Lock()
	s.issuer = issuer
	s.mu.Unlock()
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.AccessToken != s.accessToken || token.RefreshToken != s.refreshToken {
		if err := s.writeGuard.Save(s.store, s.key, sessionFromOAuthWithIssuer(s.config, token, s.issuer)); err != nil {
			return nil, err
		}
		s.accessToken = token.AccessToken
		s.refreshToken = token.RefreshToken
	}
	return token, nil
}
