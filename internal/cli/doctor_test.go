package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
)

func TestParseDoctorArgs(t *testing.T) {
	server, jsonOutput, help, err := parseDoctorArgs([]string{"github", "--json"})
	if err != nil {
		t.Fatalf("parseDoctorArgs() error = %v", err)
	}
	if server != "github" || !jsonOutput || help {
		t.Fatalf("parseDoctorArgs() = (%q,%v,%v), want github,true,false", server, jsonOutput, help)
	}
}

func TestConfiguredAuthSourceDoesNotExposeHeaderValue(t *testing.T) {
	scfg := config.ServerConfig{Headers: map[string]string{"Authorization": "Bearer secret-value"}}
	if got := configuredAuthSource(scfg, config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/config.toml")); got != "explicit_header" {
		t.Fatalf("configuredAuthSource() = %q, want explicit_header", got)
	}
	if got := configuredAuthSource(scfg, config.NewServerOrigin(config.ServerOriginKindCodex, "/tmp/codex.toml")); got != "imported_host" {
		t.Fatalf("configuredAuthSource(imported) = %q, want imported_host", got)
	}
}

func TestRedactDiagnosticErrorRemovesBearerValue(t *testing.T) {
	scfg := config.ServerConfig{Headers: map[string]string{"X-Custom-Secret": "custom-secret-value"}}
	for _, input := range []string{
		"server reflected Authorization: Bearer super-secret-value",
		"server reflected Authorization: Basic super-secret-value",
		"server reflected Cookie: super-secret-value",
		"server reflected X-Custom-Secret: custom-secret-value",
		`response={"access_token":"super-secret-value"}`,
		"request failed: https://user:super-secret-value@example.com/mcp",
		"request failed: https://example.com/mcp?api_key=super-secret-value&ok=1",
		"callback failed: https://localhost/callback?code=super-secret-value&state=ok",
	} {
		got := redactDiagnosticError(input, scfg)
		if strings.Contains(got, "super-secret-value") || strings.Contains(got, "custom-secret-value") || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("redactDiagnosticError(%q) = %q", input, got)
		}
	}
}

func TestDoctorUsesDaemonRuntimeDiagnostics(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type != "diagnose_server" || req.Server != "remote" {
				t.Fatalf("diagnostic request = %#v", req)
			}
			payload, err := json.Marshal(mcppool.Diagnostics{
				Transport:       "streamable_http",
				ProtocolVersion: "2026-07-28",
				ToolCount:       3,
			})
			if err != nil {
				t.Fatal(err)
			}
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: payload}, nil
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp"},
	}}

	var stdout, stderr bytes.Buffer
	code := runDoctorCommand([]string{"remote", "--json"}, cfg, &stdout, &stderr)
	if code != ipc.ExitOK {
		t.Fatalf("runDoctorCommand() = %d, want %d (stderr=%q)", code, ipc.ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"protocol_version":"2026-07-28"`) {
		t.Fatalf("stdout = %q, want daemon diagnostics", stdout.String())
	}
}

func TestDoctorDropsServerControlledDiagnosticMetadata(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{sendFn: func(*ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`{
				"transport":"streamable_http",
				"protocol_version":"2026-07-28",
				"server_name":"secret-value",
				"server_version":"secret-value",
				"capabilities":{"experimental":{"echo":"secret-value"}},
				"tool_count":1
			}`)}, nil
		}}
	}
	cfg := &config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", Headers: map[string]string{"X-Secret": "secret-value"}},
	}}

	var stdout, stderr bytes.Buffer
	code := runDoctorCommand([]string{"remote", "--json"}, cfg, &stdout, &stderr)
	if code != ipc.ExitOK {
		t.Fatalf("runDoctorCommand() = %d, want %d (stderr=%q)", code, ipc.ExitOK, stderr.String())
	}
	if strings.Contains(stdout.String(), "secret-value") || strings.Contains(stdout.String(), "capabilities") || strings.Contains(stdout.String(), "server_name") {
		t.Fatalf("stdout retained server-controlled metadata: %q", stdout.String())
	}
}
