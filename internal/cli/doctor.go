package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/bootstrap"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
)

type doctorResult struct {
	Server     string               `json:"server"`
	Healthy    bool                 `json:"healthy"`
	Origin     config.ServerOrigin  `json:"origin"`
	AuthSource string               `json:"auth_source"`
	Connection *mcppool.Diagnostics `json:"connection,omitempty"`
	Error      string               `json:"error,omitempty"`
}

func maybeHandleDoctorCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "doctor" {
		return false, 0
	}
	if utilityCommandDeferredToServer(cfg, "doctor") {
		return false, 0
	}
	return true, runDoctorCommand(args[1:], cfg, stdout, stderr)
}

func runDoctorCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) int {
	server, jsonOutput, help, err := parseDoctorArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: doctor: %v\n", err)
		printDoctorHelp(stderr)
		return ipc.ExitUsageErr
	}
	if help {
		printDoctorHelp(stdout)
		return ipc.ExitOK
	}

	names := make([]string, 0, len(cfg.Servers))
	if server != "" {
		if _, ok := cfg.Servers[server]; !ok {
			fmt.Fprintf(stderr, "mcpx: doctor: unknown server: %s\n", server)
			return ipc.ExitUsageErr
		}
		names = append(names, server)
	} else {
		for name := range cfg.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	results := make([]doctorResult, 0, len(names))
	healthy := true
	var client daemonRequester
	var daemonErr error
	for _, name := range names {
		result, ready := prepareDoctorResult(cfg, name)
		if ready {
			if client == nil && daemonErr == nil {
				var nonce string
				nonce, daemonErr = spawnOrConnectFn()
				if daemonErr == nil {
					client = newDaemonClient(ipc.SocketPath(), nonce)
				}
			}
			if daemonErr != nil {
				result.Error = redactDiagnosticError("daemon: "+daemonErr.Error(), cfg.Servers[name])
			} else {
				result = diagnoseServer(client, callerWorkingDirectory(), cfg.Servers[name], result)
			}
		}
		results = append(results, result)
		healthy = healthy && result.Healthy
	}

	if jsonOutput {
		if err := writeJSONLine(stdout, results); err != nil {
			fmt.Fprintf(stderr, "mcpx: doctor: %v\n", err)
			return ipc.ExitInternal
		}
	} else {
		for _, result := range results {
			status := "ok"
			if !result.Healthy {
				status = "failed"
			}
			fmt.Fprintf(stdout, "%s: %s", result.Server, status)
			if result.Connection != nil {
				fmt.Fprintf(stdout, " (transport=%s protocol=%s tools=%d auth=%s)", result.Connection.Transport, result.Connection.ProtocolVersion, result.Connection.ToolCount, result.AuthSource)
			}
			fmt.Fprintln(stdout)
			if result.Error != "" {
				fmt.Fprintf(stdout, "  %s\n", result.Error)
			}
		}
	}
	if !healthy {
		return ipc.ExitToolErr
	}
	return ipc.ExitOK
}

func prepareDoctorResult(cfg *config.Config, server string) (doctorResult, bool) {
	scfg := cfg.Servers[server]
	origin := config.NormalizeServerOrigin(cfg.ServerOrigins[server])
	result := doctorResult{
		Server:     server,
		Origin:     origin,
		AuthSource: configuredAuthSource(scfg, origin),
	}
	if err := config.ValidateServerConfig(server, scfg); err != nil {
		result.Error = redactDiagnosticError("config: "+err.Error(), scfg)
		return result, false
	}
	if err := bootstrap.CheckPrerequisites(scfg); err != nil {
		result.Error = redactDiagnosticError("prerequisite: "+err.Error(), scfg)
		return result, false
	}
	return result, true
}

func diagnoseServer(client daemonRequester, cwd string, scfg config.ServerConfig, result doctorResult) doctorResult {
	resp, err := client.Send(&ipc.Request{Type: "diagnose_server", Server: result.Server, CWD: cwd})
	if err != nil {
		result.Error = redactDiagnosticError(err.Error(), scfg)
		return result
	}
	if resp == nil {
		result.Error = "empty daemon response"
		return result
	}
	if resp.ExitCode != ipc.ExitOK {
		message := strings.TrimSpace(resp.Stderr)
		if message == "" {
			message = fmt.Sprintf("daemon diagnosis failed with exit code %d", resp.ExitCode)
		}
		result.Error = redactDiagnosticError(message, scfg)
		return result
	}
	var diagnostics mcppool.Diagnostics
	if err := json.Unmarshal(resp.Content, &diagnostics); err != nil {
		result.Error = redactDiagnosticError("invalid daemon diagnostics: "+err.Error(), scfg)
		return result
	}
	diagnostics.AuthSource = result.AuthSource
	result.Connection = &diagnostics
	result.Healthy = true
	return result
}

func configuredAuthSource(scfg config.ServerConfig, origin config.ServerOrigin) string {
	if scfg.HasAuthorizationHeader() {
		if origin.Kind != config.ServerOriginKindMCPXConfig && origin.Kind != config.ServerOriginKindFallbackCustom {
			return "imported_host"
		}
		return "explicit_header"
	}
	if scfg.OAuth {
		return "oauth_os_store"
	}
	return "none"
}

func redactDiagnosticError(message string, scfg config.ServerConfig) string {
	redacted := message
	for _, value := range scfg.Headers {
		value = strings.TrimSpace(value)
		if value != "" {
			redacted = strings.ReplaceAll(redacted, value, "[REDACTED]")
		}
	}

	// Errors are ultimately unstructured strings. These final guards cover
	// common reflected credentials even when the value did not originate in a
	// configured header.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)((?:authorization|proxy-authorization)\s*[:=]\s*(?:basic|bearer)\s+)[^\s,;"']+`),
		regexp.MustCompile(`(?i)((?:basic|bearer)\s+)[^\s,;"']+`),
		regexp.MustCompile(`(?i)((?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|api[-_]?key|access[-_]?token|refresh[-_]?token|client[-_]?secret|password|passwd|token|secret|credential|code)["']?\s*[:=]\s*["']?)[^&\s,"'}]+`),
		regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|client_secret|api[-_]?key|token|password|secret|code)=)[^&#\s"']+`),
		regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
	}
	for index, pattern := range patterns {
		replacement := `${1}[REDACTED]`
		if index == len(patterns)-1 {
			replacement += "@"
		}
		redacted = pattern.ReplaceAllString(redacted, replacement)
	}
	return redacted
}

func parseDoctorArgs(args []string) (server string, jsonOutput, help bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--help", "-h":
			help = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, false, fmt.Errorf("unknown flag: %s", arg)
			}
			if server != "" {
				return "", false, false, fmt.Errorf("unexpected positional argument: %s", arg)
			}
			server = arg
		}
	}
	return server, jsonOutput, help, nil
}

func printDoctorHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage: mcpx doctor [server] [--json]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Checks config, prerequisites, transport health, protocol negotiation, and redacted auth source.")
}
