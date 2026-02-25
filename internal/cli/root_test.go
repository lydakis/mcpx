package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestHandleRootFlagsVersion(t *testing.T) {
	oldVersion := buildVersion
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		buildVersion = oldVersion
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	buildVersion = "1.2.3"
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	handled, code := handleRootFlags([]string{"--version"})
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out.String() != "mcpx 1.2.3\n" {
		t.Fatalf("output = %q, want %q", out.String(), "mcpx 1.2.3\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestHandleRootFlagsIgnoresNonGlobal(t *testing.T) {
	handled, _ := handleRootFlags([]string{"github"})
	if handled {
		t.Fatal("handled = true, want false")
	}
}

func TestHandleRootFlagsHelp(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	handled, code := handleRootFlags([]string{"--help"})
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if got := out.String(); got == "" {
		t.Fatal("help output is empty")
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx <server> <tool>")) {
		t.Fatalf("help output missing command surface: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx <server>")) {
		t.Fatalf("help output missing server list command: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx completion <bash|zsh|fish>")) {
		t.Fatalf("help output missing completion command: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx skill install [FLAGS]")) {
		t.Fatalf("help output missing skill command: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	rootManPath := filepath.Join(os.Getenv("XDG_DATA_HOME"), "man", "man1", "mcpx.1")
	if _, err := os.Stat(rootManPath); err != nil {
		t.Fatalf("expected root man page at %q: %v", rootManPath, err)
	}
}

func TestHandleRootFlagsDoesNotTreatCompletionAsGlobal(t *testing.T) {
	handled, _ := handleRootFlags([]string{"completion", "zsh"})
	if handled {
		t.Fatal("handled = true, want false")
	}
}

func TestMaybeHandleCompletionCommandRunsWhenNameUnclaimed(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleCompletionCommand([]string{"completion", "zsh"}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("#compdef mcpx")) {
		t.Fatalf("output missing zsh completion marker: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleCompletionCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"completion": {},
		},
	}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleCompletionCommand([]string{"completion", "zsh"}, cfg, &out, &errOut)
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleInternalCompletionDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"__complete": {},
		},
	}
	handled, code := maybeHandleCompletionCommand([]string{"__complete", "servers"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestResolveBuildVersionHonorsInjectedValue(t *testing.T) {
	got := resolveBuildVersion("1.2.3")
	if got != "1.2.3" {
		t.Fatalf("resolveBuildVersion(injected) = %q, want %q", got, "1.2.3")
	}
}

func TestParseToolListArgsVerbose(t *testing.T) {
	parsed, err := parseToolListArgs([]string{"--verbose"})
	if err != nil {
		t.Fatalf("parseToolListArgs() error = %v", err)
	}
	if !parsed.verbose {
		t.Fatal("verbose = false, want true")
	}
	if parsed.help {
		t.Fatal("help = true, want false")
	}
}

func TestParseToolListArgsHelpAndVerbose(t *testing.T) {
	parsed, err := parseToolListArgs([]string{"-h", "-v"})
	if err != nil {
		t.Fatalf("parseToolListArgs() error = %v", err)
	}
	if !parsed.verbose {
		t.Fatal("verbose = false, want true")
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
}

func TestParseToolListArgsSupportsJSON(t *testing.T) {
	parsed, err := parseToolListArgs([]string{"--json"})
	if err != nil {
		t.Fatalf("parseToolListArgs() error = %v", err)
	}
	if !parsed.json {
		t.Fatal("json = false, want true")
	}
}

func TestParseToolListArgsRejectsUnknownFlags(t *testing.T) {
	if _, err := parseToolListArgs([]string{"--cache=10s"}); err == nil {
		t.Fatal("parseToolListArgs() error = nil, want non-nil")
	}
}

func TestParseServerCommandDefaultsToToolList(t *testing.T) {
	cmd, err := parseServerCommand(nil)
	if err != nil {
		t.Fatalf("parseServerCommand() error = %v", err)
	}
	if !cmd.list {
		t.Fatal("list = false, want true")
	}
	if cmd.listOpts.verbose {
		t.Fatal("verbose = true, want false")
	}
	if cmd.listOpts.help {
		t.Fatal("help = true, want false")
	}
}

func TestParseServerCommandParsesToolListFlags(t *testing.T) {
	cmd, err := parseServerCommand([]string{"-v"})
	if err != nil {
		t.Fatalf("parseServerCommand() error = %v", err)
	}
	if !cmd.list {
		t.Fatal("list = false, want true")
	}
	if !cmd.listOpts.verbose {
		t.Fatal("verbose = false, want true")
	}
}

func TestParseServerCommandParsesToolListJSONFlag(t *testing.T) {
	cmd, err := parseServerCommand([]string{"--json"})
	if err != nil {
		t.Fatalf("parseServerCommand() error = %v", err)
	}
	if !cmd.list {
		t.Fatal("list = false, want true")
	}
	if !cmd.listOpts.json {
		t.Fatal("json = false, want true")
	}
}

func TestParseServerCommandTreatsUnknownDashTokenAsToolName(t *testing.T) {
	cmd, err := parseServerCommand([]string{"--status", "--json=true"})
	if err != nil {
		t.Fatalf("parseServerCommand() error = %v", err)
	}
	if cmd.list {
		t.Fatal("list = true, want false")
	}
	if cmd.tool != "--status" {
		t.Fatalf("tool = %q, want %q", cmd.tool, "--status")
	}
	if len(cmd.toolArgs) != 1 || cmd.toolArgs[0] != "--json=true" {
		t.Fatalf("toolArgs = %v, want [--json=true]", cmd.toolArgs)
	}
}

func TestParseServerCommandSeparatorForcesToolMode(t *testing.T) {
	cmd, err := parseServerCommand([]string{"--", "--help"})
	if err != nil {
		t.Fatalf("parseServerCommand() error = %v", err)
	}
	if cmd.list {
		t.Fatal("list = true, want false")
	}
	if cmd.tool != "--help" {
		t.Fatalf("tool = %q, want %q", cmd.tool, "--help")
	}
}

func TestParseServerCommandSeparatorRequiresToolName(t *testing.T) {
	if _, err := parseServerCommand([]string{"--"}); err == nil {
		t.Fatal("parseServerCommand() error = nil, want non-nil")
	}
}

func TestRunServerHelpDoesNotRequireDaemon(t *testing.T) {
	tmp := t.TempDir()
	xdgConfigHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(xdgConfigHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	configToml := []byte(`[servers.github]
command = "echo"
args = ["ok"]
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), configToml, 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("XDG_RUNTIME_DIR", "/dev/null")

	if code := Run([]string{"github", "--help"}); code != 0 {
		t.Fatalf("Run([github --help]) = %d, want 0", code)
	}
}

func TestRunRootJSONListsServers(t *testing.T) {
	tmp := t.TempDir()
	xdgConfigHome := filepath.Join(tmp, "xdg-config")
	configDir := filepath.Join(xdgConfigHome, "mcpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir): %v", err)
	}
	configToml := []byte(`[servers.beta]
command = "echo"
args = ["ok"]

[servers.alpha]
command = "echo"
args = ["ok"]
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), configToml, 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	oldOut := rootStdout
	defer func() { rootStdout = oldOut }()
	var out bytes.Buffer
	rootStdout = &out

	if code := Run([]string{"--json"}); code != ipc.ExitOK {
		t.Fatalf("Run([--json]) = %d, want %d", code, ipc.ExitOK)
	}

	var got []string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(root output): %v", err)
	}
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
}

func TestNormalizeToolListJSONPayloadAcceptsValidJSON(t *testing.T) {
	raw := []byte(`[{"name":"list_issues","description":"List issues"}]`)

	got, err := normalizeToolListJSONPayload(raw)
	if err != nil {
		t.Fatalf("normalizeToolListJSONPayload() error = %v", err)
	}

	var decoded []map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(normalized payload): %v; payload=%q", err, string(got))
	}

	if len(decoded) != 1 || decoded[0]["name"] != "list_issues" {
		t.Fatalf("normalized payload = %#v, want one list_issues entry", decoded)
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("normalized payload must end with newline: %q", string(got))
	}
}

func TestNormalizeToolListJSONPayloadRejectsLegacyTextOutput(t *testing.T) {
	raw := []byte("list_issues\tList issues\nsearch_repositories\tSearch repositories quickly\n")

	if _, err := normalizeToolListJSONPayload(raw); err == nil {
		t.Fatal("normalizeToolListJSONPayload() error = nil, want non-nil")
	}
}

func TestWriteCallResponseUsageErrorPrintsStderrWithoutVerbose(t *testing.T) {
	resp := &ipc.Response{
		ExitCode: ipc.ExitUsageErr,
		Stderr:   "calling tool: invalid params: unknown argument \"bad\"",
	}

	var out bytes.Buffer
	var errOut bytes.Buffer

	writeCallResponse(resp, false, &out, &errOut)

	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !bytes.Contains(errOut.Bytes(), []byte("invalid params")) {
		t.Fatalf("stderr = %q, want invalid params diagnostics", errOut.String())
	}
}
