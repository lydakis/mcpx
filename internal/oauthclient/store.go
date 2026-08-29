package oauthclient

import (
	"encoding/json"
	"errors"
	"fmt"

	keyring "github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const keyringService = "mcpx"

// ErrNotFound reports that no mcpx-owned OAuth session exists for a server.
var ErrNotFound = errors.New("oauth session not found")

// Store is the credential boundary used by OAuth clients. Implementations must
// keep serialized sessions out of repository and ordinary config files.
type Store interface {
	Load(key string) (*Session, error)
	Save(key string, session *Session) error
	Delete(key string) error
}

// Session is the minimum state needed to refresh and reuse an OAuth grant.
type Session struct {
	Config storedOAuthConfig `json:"config"`
	Token  oauth2.Token      `json:"token"`
}

type storedOAuthConfig struct {
	Issuer       string          `json:"issuer"`
	ClientID     string          `json:"client_id"`
	ClientSecret string          `json:"client_secret,omitempty"`
	Endpoint     oauth2.Endpoint `json:"endpoint"`
	RedirectURL  string          `json:"redirect_url"`
	Scopes       []string        `json:"scopes,omitempty"`
}

func sessionFromOAuth(config *oauth2.Config, token *oauth2.Token) *Session {
	return sessionFromOAuthWithIssuer(config, token, "")
}

func sessionFromOAuthWithIssuer(config *oauth2.Config, token *oauth2.Token, issuer string) *Session {
	return &Session{
		Config: storedOAuthConfig{
			Issuer:       issuer,
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     config.Endpoint,
			RedirectURL:  config.RedirectURL,
			Scopes:       append([]string(nil), config.Scopes...),
		},
		Token: *token,
	}
}

func (s *Session) oauthConfig() *oauth2.Config {
	if s == nil {
		return nil
	}
	return &oauth2.Config{
		ClientID:     s.Config.ClientID,
		ClientSecret: s.Config.ClientSecret,
		Endpoint:     s.Config.Endpoint,
		RedirectURL:  s.Config.RedirectURL,
		Scopes:       append([]string(nil), s.Config.Scopes...),
	}
}

type keyringStore struct{}

// NewKeyringStore returns the production OS credential-store adapter.
func NewKeyringStore() Store { return keyringStore{} }

func (keyringStore) Load(key string) (*Session, error) {
	raw, err := keyring.Get(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading OS credential store: %w", err)
	}
	var session Session
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return nil, fmt.Errorf("decoding stored OAuth session: %w", err)
	}
	return &session, nil
}

func (keyringStore) Save(key string, session *Session) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encoding OAuth session: %w", err)
	}
	if err := keyring.Set(keyringService, key, string(raw)); err != nil {
		return fmt.Errorf("writing OS credential store: %w", err)
	}
	return nil
}

func (keyringStore) Delete(key string) error {
	err := keyring.Delete(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting OS credential: %w", err)
	}
	return nil
}
