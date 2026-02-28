package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/bootstrap"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestMaybeHandleAddCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"add": {},
		},
	}

	handled, code := maybeHandleAddCommand([]string{"add", "manifest.json"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestRunAddAddsServerFromManifestFile(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", manifestPath})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add manifest]) = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`Added server "github"`)) {
		t.Fatalf("stdout = %q, want add confirmation", out.String())
	}

	cfgPath := filepath.Join(configHome, "mcpx", "config.toml")
	edited, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	if edited.Servers["github"].Command != "npx" {
		t.Fatalf("saved command = %q, want %q", edited.Servers["github"].Command, "npx")
	}
}

func TestRunAddAllowsExistingEnvPlaceholderServers(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(configHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`[servers.existing]
url = "${EXISTING_MCP_URL}"
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("EXISTING_MCP_URL", "https://example.com/mcp")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", manifestPath})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add manifest]) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}

	edited, err := config.LoadForEditFrom(filepath.Join(configDir, "config.toml"))
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	if edited.Servers["existing"].URL != "${EXISTING_MCP_URL}" {
		t.Fatalf("existing URL = %q, want placeholder preserved", edited.Servers["existing"].URL)
	}
	if edited.Servers["github"].Command != "npx" {
		t.Fatalf("saved command = %q, want %q", edited.Servers["github"].Command, "npx")
	}
}

func TestRunAddChecksPrerequisitesAfterEnvExpansion(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"go-server":{"command":"${MCPX_TEST_RUNTIME}","args":["version"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("MCPX_TEST_RUNTIME", "go")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", manifestPath})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add manifest]) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`Added server "go-server"`)) {
		t.Fatalf("stdout = %q, want add confirmation", out.String())
	}

	cfgPath := filepath.Join(configHome, "mcpx", "config.toml")
	edited, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	if edited.Servers["go-server"].Command != "${MCPX_TEST_RUNTIME}" {
		t.Fatalf("saved command = %q, want placeholder preserved", edited.Servers["go-server"].Command)
	}
}

func TestRunAddRejectsOverwriteWithoutFlag(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(configHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`[servers.github]
command = "echo"
args = ["old"]
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", manifestPath})
	if code != ipc.ExitUsageErr {
		t.Fatalf("Run([add manifest]) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "already exists") || !strings.Contains(errOut.String(), "--overwrite") {
		t.Fatalf("stderr = %q, want overwrite guidance", errOut.String())
	}
}

func TestRunAddRejectsOverwriteBeforeCheckingPrerequisites(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(configHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`[servers.github]
command = "echo"
args = ["old"]
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"github":{"command":"__mcpx_definitely_missing_runtime__","args":["serve"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", manifestPath})
	if code != ipc.ExitUsageErr {
		t.Fatalf("Run([add manifest]) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "already exists") || !strings.Contains(errOut.String(), "--overwrite") {
		t.Fatalf("stderr = %q, want overwrite guidance", errOut.String())
	}
	if strings.Contains(errOut.String(), "required runtime") {
		t.Fatalf("stderr = %q, want overwrite error before prerequisite check", errOut.String())
	}
}

func TestRunAddAllowsOverwriteWithFlag(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(configHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`[servers.github]
command = "echo"
args = ["old"]
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	rootStdout = &bytes.Buffer{}
	rootStderr = &bytes.Buffer{}

	code := Run([]string{"add", manifestPath, "--overwrite"})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add manifest --overwrite]) = %d, want %d", code, ipc.ExitOK)
	}

	edited, err := config.LoadForEditFrom(filepath.Join(configDir, "config.toml"))
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	if edited.Servers["github"].Command != "npx" {
		t.Fatalf("saved command = %q, want %q", edited.Servers["github"].Command, "npx")
	}
}

func TestRunAddAppliesHeaderOverrides(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	raw := `{"url":"https://mcp.devin.ai/mcp"}`
	source := "cursor://anysphere.cursor-deeplink/mcp/install?name=deepwiki&config=" + base64.StdEncoding.EncodeToString([]byte(raw))

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", source, "--header", "Authorization=Bearer ${DEEPWIKI_API_KEY}"})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add install-link --header]) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}

	cfgPath := filepath.Join(configHome, "mcpx", "config.toml")
	edited, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	if edited.Servers["deepwiki"].Headers["Authorization"] != "Bearer ${DEEPWIKI_API_KEY}" {
		t.Fatalf("saved headers = %#v, want Authorization header", edited.Servers["deepwiki"].Headers)
	}
}

func TestRunAddAppliesHeaderOverridesCaseInsensitively(t *testing.T) {
	tmp := t.TempDir()
	configHome := filepath.Join(tmp, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", tmp)

	raw := `{"url":"https://mcp.devin.ai/mcp","headers":{"authorization":"Bearer old-token"}}`
	source := "cursor://anysphere.cursor-deeplink/mcp/install?name=deepwiki&config=" + base64.StdEncoding.EncodeToString([]byte(raw))

	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := Run([]string{"add", source, "--header", "Authorization=Bearer ${DEEPWIKI_API_KEY}"})
	if code != ipc.ExitOK {
		t.Fatalf("Run([add install-link --header]) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}

	cfgPath := filepath.Join(configHome, "mcpx", "config.toml")
	edited, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadForEditFrom(saved config) error = %v", err)
	}
	headers := edited.Servers["deepwiki"].Headers
	if len(headers) != 1 {
		t.Fatalf("saved headers len = %d, want 1 (headers=%#v)", len(headers), headers)
	}
	if headers["Authorization"] != "Bearer ${DEEPWIKI_API_KEY}" {
		t.Fatalf("saved headers = %#v, want Authorization override", headers)
	}
	if _, ok := headers["authorization"]; ok {
		t.Fatalf("saved headers = %#v, want lowercase authorization removed", headers)
	}
}

func TestClassifyResolveErrorExitCodeReturnsInternalForSourceAccessErrors(t *testing.T) {
	_, err := bootstrap.Resolve(context.Background(), "manifest.json", bootstrap.ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return nil, errors.New("read failed")
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}

	got := classifyResolveErrorExitCode(err)
	if got != ipc.ExitInternal {
		t.Fatalf("classifyResolveErrorExitCode(%v) = %d, want %d", err, got, ipc.ExitInternal)
	}
}

func TestClassifyResolveErrorExitCodeReturnsUsageForParseErrors(t *testing.T) {
	_, err := bootstrap.Resolve(context.Background(), "manifest.json", bootstrap.ResolveOptions{
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"mcpServers":{"broken":{"args":["-y"]}}}`), nil
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}

	got := classifyResolveErrorExitCode(err)
	if got != ipc.ExitUsageErr {
		t.Fatalf("classifyResolveErrorExitCode(%v) = %d, want %d", err, got, ipc.ExitUsageErr)
	}
}

func TestParseAddArgsRejectsMissingNameValue(t *testing.T) {
	tests := [][]string{
		{"source.json", "--name"},
		{"source.json", "--name="},
		{"source.json", "--name", "--overwrite"},
	}

	for _, args := range tests {
		_, err := parseAddArgs(args)
		if err == nil {
			t.Fatalf("parseAddArgs(%v) error = nil, want non-nil", args)
		}
		if !strings.Contains(err.Error(), "missing value for --name") {
			t.Fatalf("parseAddArgs(%v) error = %q, want missing --name value message", args, err.Error())
		}
	}
}

func TestParseAddArgsParsesHeaderFlags(t *testing.T) {
	parsed, err := parseAddArgs([]string{
		"https://mcp.deepwiki.com/mcp",
		"--header", "Authorization=Bearer token",
		"--header=X-Trace-ID=abc123",
	})
	if err != nil {
		t.Fatalf("parseAddArgs() error = %v", err)
	}
	if parsed.source != "https://mcp.deepwiki.com/mcp" {
		t.Fatalf("parsed.source = %q, want source URL", parsed.source)
	}
	if len(parsed.headers) != 2 {
		t.Fatalf("len(parsed.headers) = %d, want 2", len(parsed.headers))
	}
	if parsed.headers[0].name != "Authorization" || parsed.headers[0].value != "Bearer token" {
		t.Fatalf("parsed.headers[0] = %#v, want Authorization header", parsed.headers[0])
	}
	if parsed.headers[1].name != "X-Trace-ID" || parsed.headers[1].value != "abc123" {
		t.Fatalf("parsed.headers[1] = %#v, want X-Trace-ID header", parsed.headers[1])
	}
}

func TestParseAddArgsRejectsInvalidHeaderFlag(t *testing.T) {
	tests := [][]string{
		{"https://mcp.deepwiki.com/mcp", "--header"},
		{"https://mcp.deepwiki.com/mcp", "--header", ""},
		{"https://mcp.deepwiki.com/mcp", "--header", "Authorization"},
		{"https://mcp.deepwiki.com/mcp", "--header", "=Bearer token"},
		{"https://mcp.deepwiki.com/mcp", "--header="},
	}

	for _, args := range tests {
		_, err := parseAddArgs(args)
		if err == nil {
			t.Fatalf("parseAddArgs(%v) error = nil, want non-nil", args)
		}
		if !strings.Contains(err.Error(), "invalid --header") {
			t.Fatalf("parseAddArgs(%v) error = %q, want invalid --header message", args, err.Error())
		}
	}
}
