package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/shim"
	"github.com/lydakis/mcpx/internal/skill"
)

func TestMaybeHandleShimCommandRunsWhenNameUnclaimed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"github": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "github", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), `Installed shim "github"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleShimCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerConfig{"shim": {}}}

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "github"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestMaybeHandleShimCommandInstallRejectsUnknownServer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()
	shimKnownServersFn = func() ([]string, error) {
		return []string{"known-server"}, nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "does-not-exist", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitUsageErr {
		t.Fatalf("code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), `unknown server`) {
		t.Fatalf("stderr = %q, want unknown-server guidance", errOut.String())
	}
	shimPath := filepath.Join(tmp, "does-not-exist")
	if _, err := os.Stat(shimPath); !os.IsNotExist(err) {
		t.Fatalf("shim file %q should not exist, stat err=%v", shimPath, err)
	}
}

func TestMaybeHandleShimCommandInstallAllowsDiscoveredVirtualServer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()
	shimKnownServersFn = func() ([]string, error) {
		return []string{"gmail", "linear"}, nil
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"codex_apps": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "gmail", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Installed shim "gmail"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestMaybeHandleShimCommandInstallAllowsConfiguredUtilityName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	cfg := &config.Config{Servers: map[string]config.ServerConfig{"add": {}}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleShimCommand([]string{"shim", "install", "add", "--dir", tmp}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != ipc.ExitOK {
		t.Fatalf("code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Installed shim "add"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
}

func TestRunShimInstallIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	var out bytes.Buffer
	var errOut bytes.Buffer

	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("first runShimCommand(install) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("second runShimCommand(install) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `already installed`) {
		t.Fatalf("stdout = %q, want already-installed message", out.String())
	}
}

func TestRunShimListPrintsInstalledEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if code := runShimCommand([]string{"install", "zeta", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install zeta code = %d, want %d", code, ipc.ExitOK)
	}
	if code := runShimCommand([]string{"install", "alpha", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install alpha code = %d, want %d", code, ipc.ExitOK)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := runShimCommand([]string{"list", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimCommand(list) = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "zeta") {
		t.Fatalf("stdout = %q, want both entries", out.String())
	}
}

func TestRunShimRemoveDeletesInstalledShim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	if code := runShimCommand([]string{"install", "github", "--dir", tmp}, &bytes.Buffer{}, &bytes.Buffer{}); code != ipc.ExitOK {
		t.Fatalf("install code = %d, want %d", code, ipc.ExitOK)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := runShimCommand([]string{"remove", "github", "--dir", tmp}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimCommand(remove) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Removed shim "github"`) {
		t.Fatalf("stdout = %q, want remove confirmation", out.String())
	}
}

func TestRunShimRemoveMissingReturnsUsageError(t *testing.T) {
	tmp := t.TempDir()
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runShimCommand([]string{"remove", "github", "--dir", tmp}, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runShimCommand(remove missing) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "not installed") {
		t.Fatalf("stderr = %q, want not-installed guidance", errOut.String())
	}
}

func TestParseShimInstallArgsSkillFlagsRequireSkill(t *testing.T) {
	_, err := parseShimInstallArgs([]string{"github", "--codex-link"})
	if err == nil {
		t.Fatal("parseShimInstallArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "require --skill") {
		t.Fatalf("parseShimInstallArgs() error = %q, want require --skill message", err.Error())
	}
}

func TestParseShimInstallArgsOpenClawDirImpliesOpenClawLink(t *testing.T) {
	parsed, err := parseShimInstallArgs([]string{"github", "--openclaw-dir", "/tmp/openclaw-skills", "--skill"})
	if err != nil {
		t.Fatalf("parseShimInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableOpenClawLink {
		t.Fatalf("enableOpenClawLink = false, want true when --openclaw-dir is set")
	}
	if parsed.openClawDir != "/tmp/openclaw-skills" {
		t.Fatalf("openClawDir = %q, want %q", parsed.openClawDir, "/tmp/openclaw-skills")
	}
}

func TestParseShimInstallArgsRejectsOpenClawDirWithoutValue(t *testing.T) {
	_, err := parseShimInstallArgs([]string{"github", "--openclaw-dir", "--no-claude-link", "--skill"})
	if err == nil {
		t.Fatal("parseShimInstallArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing value for --openclaw-dir") {
		t.Fatalf("parseShimInstallArgs() error = %q, want missing value message", err.Error())
	}
}

func TestRunShimInstallWithSkillFailureKeepsShimInstallSuccessful(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldInstallServerSkillFn := installServerSkillFn
	defer func() { installServerSkillFn = oldInstallServerSkillFn }()
	installServerSkillFn = func(string, *skillInstallArgs) (*skill.InstallResult, error) {
		return nil, errors.New("server skill generation failed")
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runShimCommand([]string{"install", "github", "--dir", tmp, "--skill"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runShimCommand(install --skill) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `Installed shim "github"`) {
		t.Fatalf("stdout = %q, want shim install confirmation", out.String())
	}
	if !strings.Contains(errOut.String(), "failed to install skill") {
		t.Fatalf("stderr = %q, want skill warning", errOut.String())
	}
}

func TestRunShimInstallWithSkillStrictFailsOnSkillError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldInstallServerSkillFn := installServerSkillFn
	defer func() { installServerSkillFn = oldInstallServerSkillFn }()
	installServerSkillFn = func(string, *skillInstallArgs) (*skill.InstallResult, error) {
		return nil, errors.New("server skill generation failed")
	}

	code := runShimCommand([]string{"install", "github", "--dir", tmp, "--skill", "--skill-strict"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != ipc.ExitInternal {
		t.Fatalf("runShimCommand(install --skill --skill-strict) = %d, want %d", code, ipc.ExitInternal)
	}
}

func TestPrintShimHelpIncludesUsageAndSubcommandSections(t *testing.T) {
	var out bytes.Buffer
	printShimHelp(&out)

	got := out.String()
	for _, want := range []string{
		"mcpx shim install <server>",
		"mcpx shim remove <server>",
		"mcpx shim list",
		"Install flags:",
		"Remove flags:",
		"List flags:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("printShimHelp() output missing %q: %q", want, got)
		}
	}
}

func TestPrintShimInstallHelpIncludesSkillFlags(t *testing.T) {
	var out bytes.Buffer
	printShimInstallHelp(&out)

	got := out.String()
	for _, want := range []string{
		"--skill",
		"--skill-strict",
		"--data-agent-dir",
		"--openclaw-link",
		"--help, -h",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("printShimInstallHelp() output missing %q: %q", want, got)
		}
	}
}

func TestPrintShimRemoveAndListHelpIncludeHelpFlag(t *testing.T) {
	var removeOut bytes.Buffer
	printShimRemoveHelp(&removeOut)
	if got := removeOut.String(); !strings.Contains(got, "--help, -h") {
		t.Fatalf("printShimRemoveHelp() output missing help flag: %q", got)
	}

	var listOut bytes.Buffer
	printShimListHelp(&listOut)
	if got := listOut.String(); !strings.Contains(got, "--help, -h") {
		t.Fatalf("printShimListHelp() output missing help flag: %q", got)
	}
}

func TestShimServerKnownHandlesConfiguredDiscoveredAndDiscoveryError(t *testing.T) {
	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()

	known, err := shimServerKnown("github", nil)
	if err != nil {
		t.Fatalf("shimServerKnown(nil cfg) error = %v", err)
	}
	if !known {
		t.Fatal("shimServerKnown(nil cfg) = false, want true")
	}

	known, err = shimServerKnown("github", &config.Config{Servers: map[string]config.ServerConfig{"github": {}}})
	if err != nil {
		t.Fatalf("shimServerKnown(configured) error = %v", err)
	}
	if !known {
		t.Fatal("shimServerKnown(configured) = false, want true")
	}

	shimKnownServersFn = func() ([]string, error) {
		return nil, errors.New("discovery failed")
	}
	known, err = shimServerKnown("ghost", &config.Config{Servers: map[string]config.ServerConfig{}})
	if err != nil {
		t.Fatalf("shimServerKnown(discovery error) error = %v", err)
	}
	if !known {
		t.Fatal("shimServerKnown(discovery error) = false, want true (graceful degrade)")
	}

	shimKnownServersFn = func() ([]string, error) {
		return []string{"alpha", "beta"}, nil
	}
	known, err = shimServerKnown("beta", &config.Config{Servers: map[string]config.ServerConfig{}})
	if err != nil {
		t.Fatalf("shimServerKnown(discovered hit) error = %v", err)
	}
	if !known {
		t.Fatal("shimServerKnown(discovered hit) = false, want true")
	}

	known, err = shimServerKnown("gamma", &config.Config{Servers: map[string]config.ServerConfig{}})
	if err != nil {
		t.Fatalf("shimServerKnown(discovered miss) error = %v", err)
	}
	if known {
		t.Fatal("shimServerKnown(discovered miss) = true, want false")
	}
}

func TestListShimKnownServersReturnsDecodedServerNames(t *testing.T) {
	oldSpawnOrConnect := spawnOrConnectFn
	oldNewDaemonClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawnOrConnect
		newDaemonClient = oldNewDaemonClient
	}()

	spawnOrConnectFn = func() (string, error) {
		return "nonce", nil
	}

	newDaemonClient = func(socketPath, nonce string) daemonRequester {
		if socketPath != ipc.SocketPath() {
			t.Fatalf("newDaemonClient socketPath = %q, want %q", socketPath, ipc.SocketPath())
		}
		if nonce != "nonce" {
			t.Fatalf("newDaemonClient nonce = %q, want %q", nonce, "nonce")
		}
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				if req == nil {
					t.Fatal("listShimKnownServers() sent nil request")
				}
				if req.Type != "list_servers" {
					t.Fatalf("request type = %q, want %q", req.Type, "list_servers")
				}
				return &ipc.Response{
					ExitCode: ipc.ExitOK,
					Content:  []byte(`[{"name":"zeta"},{"name":"alpha"},{"name":"alpha"}]`),
				}, nil
			},
		}
	}

	servers, err := listShimKnownServers()
	if err != nil {
		t.Fatalf("listShimKnownServers() error = %v", err)
	}
	if got, want := strings.Join(servers, ","), "alpha,zeta"; got != want {
		t.Fatalf("listShimKnownServers() = %q, want %q", got, want)
	}
}

func TestListShimKnownServersPropagatesConnectionAndDaemonErrors(t *testing.T) {
	oldSpawnOrConnect := spawnOrConnectFn
	oldNewDaemonClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawnOrConnect
		newDaemonClient = oldNewDaemonClient
	}()

	spawnOrConnectFn = func() (string, error) {
		return "", errors.New("spawn failed")
	}
	if _, err := listShimKnownServers(); err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("listShimKnownServers() error = %v, want spawn error", err)
	}

	spawnOrConnectFn = func() (string, error) {
		return "nonce", nil
	}
	newDaemonClient = func(string, string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return nil, errors.New("send failed")
			},
		}
	}
	if _, err := listShimKnownServers(); err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("listShimKnownServers() error = %v, want send error", err)
	}

	newDaemonClient = func(string, string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "daemon said no"}, nil
			},
		}
	}
	if _, err := listShimKnownServers(); err == nil || !strings.Contains(err.Error(), "daemon said no") {
		t.Fatalf("listShimKnownServers() error = %v, want daemon stderr error", err)
	}

	newDaemonClient = func(string, string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return &ipc.Response{ExitCode: ipc.ExitInternal}, nil
			},
		}
	}
	if _, err := listShimKnownServers(); err == nil || !strings.Contains(err.Error(), "listing servers failed") {
		t.Fatalf("listShimKnownServers() error = %v, want generic daemon exit error", err)
	}
}

func TestRunShimCommandWithConfigHelpAndUnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runShimCommandWithConfig([]string{"--help"}, nil, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runShimCommandWithConfig([--help]) = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), "mcpx shim install <server>") {
		t.Fatalf("stdout = %q, want shim usage", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = runShimCommandWithConfig([]string{"unknown"}, nil, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runShimCommandWithConfig([unknown]) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "unknown shim command") {
		t.Fatalf("stderr = %q, want unknown-command error", got)
	}
}

func TestRunShimSubcommandHelpPaths(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	if code := runShimInstallCommand([]string{"--help"}, nil, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimInstallCommand([--help]) = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), "Install flags:") {
		t.Fatalf("install help stdout = %q, want install flags", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("install help stderr = %q, want empty", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runShimRemoveCommand([]string{"--help"}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimRemoveCommand([--help]) = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), "Remove flags:") {
		t.Fatalf("remove help stdout = %q, want remove flags", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("remove help stderr = %q, want empty", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runShimListCommand([]string{"--help"}, &out, &errOut); code != ipc.ExitOK {
		t.Fatalf("runShimListCommand([--help]) = %d, want %d", code, ipc.ExitOK)
	}
	if !strings.Contains(out.String(), "List flags:") {
		t.Fatalf("list help stdout = %q, want list flags", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("list help stderr = %q, want empty", errOut.String())
	}
}

func TestParseShimInstallArgsValidationAndDefaults(t *testing.T) {
	if _, err := parseShimInstallArgs(nil); err == nil {
		t.Fatal("parseShimInstallArgs(nil) error = nil, want non-nil")
	}
	if _, err := parseShimInstallArgs([]string{"github", "--skill-strict"}); err == nil {
		t.Fatal("parseShimInstallArgs([github --skill-strict]) error = nil, want non-nil")
	}
	if _, err := parseShimInstallArgs([]string{"github", "--unknown"}); err == nil {
		t.Fatal("parseShimInstallArgs([github --unknown]) error = nil, want non-nil")
	}
	if _, err := parseShimInstallArgs([]string{"github", "extra"}); err == nil {
		t.Fatal("parseShimInstallArgs([github extra]) error = nil, want non-nil")
	}

	parsedHelp, err := parseShimInstallArgs([]string{"--help"})
	if err != nil {
		t.Fatalf("parseShimInstallArgs([--help]) error = %v, want nil", err)
	}
	if !parsedHelp.help {
		t.Fatal("parsedHelp.help = false, want true")
	}

	parsed, err := parseShimInstallArgs([]string{"github", "--skill", "--codex-link", "--kiro-link", "--openclaw-link"})
	if err != nil {
		t.Fatalf("parseShimInstallArgs(skill defaults) error = %v, want nil", err)
	}
	if parsed.dataAgentDir != skill.DefaultDataAgentDir() {
		t.Fatalf("dataAgentDir = %q, want %q", parsed.dataAgentDir, skill.DefaultDataAgentDir())
	}
	if parsed.claudeDir != skill.DefaultClaudeDir() {
		t.Fatalf("claudeDir = %q, want %q", parsed.claudeDir, skill.DefaultClaudeDir())
	}
	if parsed.codexDir != skill.DefaultCodexDir() {
		t.Fatalf("codexDir = %q, want %q", parsed.codexDir, skill.DefaultCodexDir())
	}
	if parsed.kiroDir != skill.DefaultKiroDir() {
		t.Fatalf("kiroDir = %q, want %q", parsed.kiroDir, skill.DefaultKiroDir())
	}
	if parsed.openClawDir != skill.DefaultOpenClawDir() {
		t.Fatalf("openClawDir = %q, want %q", parsed.openClawDir, skill.DefaultOpenClawDir())
	}
}

func TestParseShimRemoveAndListArgsValidation(t *testing.T) {
	parsedRemoveHelp, err := parseShimRemoveArgs([]string{"--help"})
	if err != nil {
		t.Fatalf("parseShimRemoveArgs([--help]) error = %v, want nil", err)
	}
	if !parsedRemoveHelp.help {
		t.Fatal("parsedRemoveHelp.help = false, want true")
	}
	if _, err := parseShimRemoveArgs([]string{"--unknown"}); err == nil {
		t.Fatal("parseShimRemoveArgs([--unknown]) error = nil, want non-nil")
	}
	if _, err := parseShimRemoveArgs([]string{"github", "extra"}); err == nil {
		t.Fatal("parseShimRemoveArgs([github extra]) error = nil, want non-nil")
	}

	if _, err := parseShimListArgs([]string{"extra"}); err == nil {
		t.Fatal("parseShimListArgs([extra]) error = nil, want non-nil")
	}
	if _, err := parseShimListArgs([]string{"--unknown"}); err == nil {
		t.Fatal("parseShimListArgs([--unknown]) error = nil, want non-nil")
	}
}

func TestClassifyShimErrorExitCodeDefaultsToInternal(t *testing.T) {
	if got := classifyShimErrorExitCode(errors.New("boom")); got != ipc.ExitInternal {
		t.Fatalf("classifyShimErrorExitCode(generic) = %d, want %d", got, ipc.ExitInternal)
	}
	if got := classifyShimErrorExitCode(shim.ErrNotInstalled); got != ipc.ExitUsageErr {
		t.Fatalf("classifyShimErrorExitCode(ErrNotInstalled) = %d, want %d", got, ipc.ExitUsageErr)
	}
}

func TestRunShimInstallSkillStrictClassifiesUnknownServerAsUsage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldInstallServerSkillFn := installServerSkillFn
	defer func() { installServerSkillFn = oldInstallServerSkillFn }()
	installServerSkillFn = func(string, *skillInstallArgs) (*skill.InstallResult, error) {
		return nil, errors.New("unknown server: github")
	}

	code := runShimCommand([]string{"install", "github", "--dir", tmp, "--skill", "--skill-strict"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != ipc.ExitUsageErr {
		t.Fatalf("runShimCommand(skill strict unknown server) = %d, want %d", code, ipc.ExitUsageErr)
	}
}

func TestRunShimInstallIgnoresDiscoveryErrorAndStillInstalls(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	oldKnownServersFn := shimKnownServersFn
	defer func() { shimKnownServersFn = oldKnownServersFn }()
	shimKnownServersFn = func() ([]string, error) {
		return nil, errors.New("discovery unavailable")
	}

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runShimInstallCommand([]string{"github", "--dir", tmp}, cfg, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runShimInstallCommand(discovery error) = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), `shim "github"`) {
		t.Fatalf("stdout = %q, want install confirmation", out.String())
	}
}

func TestRunShimListCommandReportsNoEntries(t *testing.T) {
	tmp := t.TempDir()

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runShimListCommand([]string{"--dir", tmp}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runShimListCommand(empty) = %d, want %d", code, ipc.ExitOK)
	}
	if got := out.String(); !strings.Contains(got, "No mcpx shims installed.") {
		t.Fatalf("stdout = %q, want empty-list message", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}
