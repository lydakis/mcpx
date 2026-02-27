package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInstallLinkSuccess(t *testing.T) {
	raw := `{"command":"npx","args":["-y","@modelcontextprotocol/server-postgres","postgresql://localhost/mydb"]}`
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	source := "cursor://anysphere.cursor-deeplink/mcp/install?name=postgres&config=" + encoded

	resolved, err := Resolve(context.Background(), source, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "postgres" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "postgres")
	}
	if resolved.Server.Command != "npx" {
		t.Fatalf("resolved.Server.Command = %q, want %q", resolved.Server.Command, "npx")
	}
	if len(resolved.Server.Args) != 3 {
		t.Fatalf("resolved.Server.Args len = %d, want 3", len(resolved.Server.Args))
	}
}

func TestResolveManifestFileStdioSuccess(t *testing.T) {
	source := testdataPath(t, "manifest_stdio.json")
	resolved, err := Resolve(context.Background(), source, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "github" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "github")
	}
	if resolved.Server.Command != "npx" {
		t.Fatalf("resolved.Server.Command = %q, want %q", resolved.Server.Command, "npx")
	}
	if resolved.Server.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Fatalf("resolved.Server.Env[GITHUB_TOKEN] = %q, want %q", resolved.Server.Env["GITHUB_TOKEN"], "${GITHUB_TOKEN}")
	}
}

func TestResolveManifestFileTOMLMCPServersSuccess(t *testing.T) {
	source := testdataPath(t, "manifest_stdio_mcpservers.toml")
	resolved, err := Resolve(context.Background(), source, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "github" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "github")
	}
	if resolved.Server.Command != "npx" {
		t.Fatalf("resolved.Server.Command = %q, want %q", resolved.Server.Command, "npx")
	}
	if resolved.Server.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Fatalf("resolved.Server.Env[GITHUB_TOKEN] = %q, want %q", resolved.Server.Env["GITHUB_TOKEN"], "${GITHUB_TOKEN}")
	}
}

func TestResolveManifestFileHTTPSuccess(t *testing.T) {
	source := testdataPath(t, "manifest_http.json")
	resolved, err := Resolve(context.Background(), source, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "linear" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "linear")
	}
	if resolved.Server.URL != "https://example.com/mcp" {
		t.Fatalf("resolved.Server.URL = %q, want %q", resolved.Server.URL, "https://example.com/mcp")
	}
	if resolved.Server.Headers["Authorization"] != "Bearer ${LINEAR_API_KEY}" {
		t.Fatalf("resolved.Server.Headers[Authorization] = %q, want %q", resolved.Server.Headers["Authorization"], "Bearer ${LINEAR_API_KEY}")
	}
}

func TestResolveManifestAcceptsHyphenatedStreamableHTTPTransport(t *testing.T) {
	manifest := []byte(`{"mcpServers":{"linear":{"transport":"streamable-http","url":"https://example.com/mcp"}}}`)
	resolved, err := Resolve(context.Background(), "manifest.json", ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return manifest, nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "linear" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "linear")
	}
	if resolved.Server.URL != "https://example.com/mcp" {
		t.Fatalf("resolved.Server.URL = %q, want %q", resolved.Server.URL, "https://example.com/mcp")
	}
}

func TestResolveManifestAllowsEnvBackedURL(t *testing.T) {
	t.Setenv("MCP_SERVER_URL", "https://example.com/mcp")

	manifest := []byte(`{"mcpServers":{"linear":{"transport":"http","url":"${MCP_SERVER_URL}"}}}`)
	resolved, err := Resolve(context.Background(), "manifest.json", ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return manifest, nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Name != "linear" {
		t.Fatalf("resolved.Name = %q, want %q", resolved.Name, "linear")
	}
	if resolved.Server.URL != "${MCP_SERVER_URL}" {
		t.Fatalf("resolved.Server.URL = %q, want placeholder preserved", resolved.Server.URL)
	}
}

func TestResolveMarksReadErrorsAsSourceAccess(t *testing.T) {
	readErr := errors.New("read failed")
	_, err := Resolve(context.Background(), "manifest.json", ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return nil, readErr
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !IsSourceAccessError(err) {
		t.Fatalf("IsSourceAccessError(%v) = false, want true", err)
	}
}

func TestResolveMarksFetchErrorsAsSourceAccess(t *testing.T) {
	fetchErr := errors.New("dial failed")
	_, err := Resolve(context.Background(), "https://example.com/manifest.json", ResolveOptions{
		FetchURL: func(context.Context, string) ([]byte, error) {
			return nil, fetchErr
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !IsSourceAccessError(err) {
		t.Fatalf("IsSourceAccessError(%v) = false, want true", err)
	}
}

func TestResolveDoesNotMarkParseErrorsAsSourceAccess(t *testing.T) {
	_, err := Resolve(context.Background(), "manifest.json", ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"mcpServers":{"broken":{"args":["-y"]}}}`), nil
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if IsSourceAccessError(err) {
		t.Fatalf("IsSourceAccessError(%v) = true, want false", err)
	}
}

func TestResolveInstallLinkRejectsInvalidBase64(t *testing.T) {
	raw, err := os.ReadFile(testdataPath(t, "install_link_invalid_base64.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	source := strings.TrimSpace(string(raw))

	_, err = Resolve(context.Background(), source, ResolveOptions{})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "invalid install-link config payload") {
		t.Fatalf("Resolve() error = %q, want to contain %q", err.Error(), "invalid install-link config payload")
	}
}

func TestResolveManifestRejectsMissingTransport(t *testing.T) {
	source := testdataPath(t, "manifest_missing_transport.json")
	_, err := Resolve(context.Background(), source, ResolveOptions{})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing transport") {
		t.Fatalf("Resolve() error = %q, want to contain %q", err.Error(), "missing transport")
	}
}

func TestResolveManifestRejectsUnsupportedTransport(t *testing.T) {
	source := testdataPath(t, "manifest_unsupported_transport.json")
	_, err := Resolve(context.Background(), source, ResolveOptions{})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("Resolve() error = %q, want to contain %q", err.Error(), "unsupported transport")
	}
}

func TestResolveManifestRejectsUnsupportedNestedInstallTransport(t *testing.T) {
	source := testdataPath(t, "manifest_install_transport_unsupported.json")
	_, err := Resolve(context.Background(), source, ResolveOptions{})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("Resolve() error = %q, want to contain %q", err.Error(), "unsupported transport")
	}
}

func TestResolveManifestRejectsEmptyTransportArray(t *testing.T) {
	source := testdataPath(t, "manifest_transport_empty_array.json")
	_, err := Resolve(context.Background(), source, ResolveOptions{})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "transport array cannot be empty") {
		t.Fatalf("Resolve() error = %q, want to contain %q", err.Error(), "transport array cannot be empty")
	}
}

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}
