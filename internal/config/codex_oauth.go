package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	codexMCPKeyringService      = "Codex MCP Credentials"
	codexMCPCredentialsFileName = ".credentials.json"
)

var codexMCPKeyringGetFn = keyring.Get

type codexMCPStoredOAuthTokens struct {
	TokenResponse struct {
		AccessToken string `json:"access_token"`
	} `json:"token_response"`
}

type codexMCPFallbackTokenEntry struct {
	AccessToken string `json:"access_token"`
}

func codexMCPAccessToken(configPath, storeMode, serverName, serverURL string) (string, bool) {
	serverName = strings.TrimSpace(serverName)
	serverURL = strings.TrimSpace(serverURL)
	if serverName == "" || serverURL == "" {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(storeMode)) {
	case "file":
		return codexMCPAccessTokenFromFile(configPath, serverName, serverURL)
	case "keyring":
		return codexMCPAccessTokenFromKeyring(serverName, serverURL)
	default:
		if token, ok := codexMCPAccessTokenFromKeyring(serverName, serverURL); ok {
			return token, true
		}
		return codexMCPAccessTokenFromFile(configPath, serverName, serverURL)
	}
}

func codexMCPAccessTokenFromKeyring(serverName, serverURL string) (string, bool) {
	key, err := codexMCPStoreKey(serverName, serverURL)
	if err != nil {
		return "", false
	}
	raw, err := codexMCPKeyringGetFn(codexMCPKeyringService, key)
	if err != nil {
		return "", false
	}
	var stored codexMCPStoredOAuthTokens
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return "", false
	}
	token := strings.TrimSpace(stored.TokenResponse.AccessToken)
	return token, token != ""
}

func codexMCPAccessTokenFromFile(configPath, serverName, serverURL string) (string, bool) {
	data, err := os.ReadFile(codexMCPCredentialsFilePath(configPath))
	if err != nil {
		return "", false
	}
	var entries map[string]codexMCPFallbackTokenEntry
	if json.Unmarshal(data, &entries) != nil {
		return "", false
	}
	key, err := codexMCPStoreKey(serverName, serverURL)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(entries[key].AccessToken)
	return token, token != ""
}

func codexMCPStoreKey(serverName, serverURL string) (string, error) {
	payload := struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}{Type: "http", URL: serverURL, Headers: map[string]string{}}
	serialized, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(serialized)
	return serverName + "|" + hex.EncodeToString(digest[:])[:16], nil
}
