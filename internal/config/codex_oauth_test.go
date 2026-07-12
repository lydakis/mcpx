package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testCodexMCPURL = "https://mcp.mymind.com/mcp/chatgpt"

func TestCodexMCPStoreKeyMatchesCodex(t *testing.T) {
	got, err := codexMCPStoreKey("mymind", testCodexMCPURL)
	if err != nil || got != "mymind|fea16231ffb6f072" {
		t.Fatalf("codexMCPStoreKey() = %q, %v", got, err)
	}
}

func TestLoadCodexConfigFileAddsOAuthBearerFromKeyring(t *testing.T) {
	originalGet := codexMCPKeyringGetFn
	t.Cleanup(func() { codexMCPKeyringGetFn = originalGet })
	codexMCPKeyringGetFn = func(service, account string) (string, error) {
		if service != codexMCPKeyringService || account != "mymind|fea16231ffb6f072" {
			t.Fatalf("unexpected keyring lookup: %q %q", service, account)
		}
		return `{"token_response":{"access_token":"oauth-token"}}`, nil
	}
	servers, err := loadCodexConfigFile(writeCodexMCPConfig(t, `[mcp_servers.mymind]
url = "https://mcp.mymind.com/mcp/chatgpt"
`))
	if err != nil || servers["mymind"].Headers[authorizationHeader] != "Bearer oauth-token" {
		t.Fatalf("OAuth config = %#v, %v", servers, err)
	}
}

func TestLoadCodexConfigFilePreservesExplicitAuthorizationOverOAuth(t *testing.T) {
	originalGet := codexMCPKeyringGetFn
	t.Cleanup(func() { codexMCPKeyringGetFn = originalGet })
	codexMCPKeyringGetFn = func(_, _ string) (string, error) {
		t.Fatal("must not read keyring when Authorization is explicit")
		return "", errors.New("unexpected keyring read")
	}
	servers, err := loadCodexConfigFile(writeCodexMCPConfig(t, `[mcp_servers.mymind]
url = "https://mcp.mymind.com/mcp/chatgpt"
http_headers = { authorization = "Bearer explicit" }
`))
	if err != nil || servers["mymind"].Headers["authorization"] != "Bearer explicit" {
		t.Fatalf("explicit auth config = %#v, %v", servers, err)
	}
}

func TestLoadCodexConfigFileUsesOAuthFileStore(t *testing.T) {
	originalGet := codexMCPKeyringGetFn
	t.Cleanup(func() { codexMCPKeyringGetFn = originalGet })
	codexMCPKeyringGetFn = func(_, _ string) (string, error) {
		return "", errors.New("file mode must not read keyring")
	}
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	credentials := []byte(`{"mymind|fea16231ffb6f072":{"access_token":"file-token"}}`)
	if err := os.WriteFile(filepath.Join(codexHome, codexMCPCredentialsFileName), credentials, 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := loadCodexConfigFile(writeCodexMCPConfig(t, `mcp_oauth_credentials_store = "file"
[mcp_servers.mymind]
url = "https://mcp.mymind.com/mcp/chatgpt"
`))
	if err != nil || servers["mymind"].Headers[authorizationHeader] != "Bearer file-token" {
		t.Fatalf("file OAuth config = %#v, %v", servers, err)
	}
}

func TestLoadCodexConfigFileAutoFallsBackToOAuthFileStore(t *testing.T) {
	originalGet := codexMCPKeyringGetFn
	t.Cleanup(func() { codexMCPKeyringGetFn = originalGet })
	codexMCPKeyringGetFn = func(_, _ string) (string, error) {
		return "", errors.New("keyring unavailable")
	}
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	credentials := []byte(`{"mymind|fea16231ffb6f072":{"access_token":"fallback-token"}}`)
	if err := os.WriteFile(filepath.Join(codexHome, codexMCPCredentialsFileName), credentials, 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := loadCodexConfigFile(writeCodexMCPConfig(t, `[mcp_servers.mymind]
url = "https://mcp.mymind.com/mcp/chatgpt"
`))
	if err != nil || servers["mymind"].Headers[authorizationHeader] != "Bearer fallback-token" {
		t.Fatalf("fallback OAuth config = %#v, %v", servers, err)
	}
}

func TestRuntimeConfigSourcePathsIncludeCodexMCPCredentials(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	configPath := filepath.Join(codexHome, codexConfigName)
	paths := RuntimeConfigSourcePathsForCWD(&Config{FallbackSources: []string{configPath}}, "")
	want := filepath.Join(codexHome, codexMCPCredentialsFileName)
	for _, path := range paths {
		if path == want {
			return
		}
	}
	t.Fatalf("RuntimeConfigSourcePathsForCWD() = %q, want %q", paths, want)
}

func writeCodexMCPConfig(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), codexConfigName)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
