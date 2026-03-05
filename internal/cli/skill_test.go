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
	"github.com/lydakis/mcpx/internal/skill"
)

func TestMaybeHandleSkillCommandRunsWhenNameUnclaimed(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	kiroDir := filepath.Join(tmp, "kiro", "skills")
	openClawDir := filepath.Join(tmp, "openclaw", "skills")

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleSkillCommand([]string{
		"skill",
		"install",
		"--data-agent-dir", dataDir,
		"--claude-dir", claudeDir,
		"--kiro-link",
		"--kiro-dir", kiroDir,
		"--openclaw-link",
		"--openclaw-dir", openClawDir,
	}, cfg, &out, &errOut)
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	skillFile := filepath.Join(dataDir, "mcpx", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("skill file not created at %s: %v", skillFile, err)
	}

	assertSymlinkTargets(t, filepath.Join(claudeDir, "mcpx"), filepath.Join(dataDir, "mcpx"))
	assertSymlinkTargets(t, filepath.Join(kiroDir, "mcpx"), filepath.Join(dataDir, "mcpx"))
	assertSymlinkTargets(t, filepath.Join(openClawDir, "mcpx"), filepath.Join(dataDir, "mcpx"))

	if !bytes.Contains(out.Bytes(), []byte("Installed skill file:")) {
		t.Fatalf("stdout missing install message: %q", out.String())
	}
}

func TestMaybeHandleSkillCommandDefersToServerName(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.ServerConfig{
			"skill": {},
		},
	}
	handled, code := maybeHandleSkillCommand([]string{"skill", "install"}, cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("handled = true, want false")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestRunSkillInstallCommandCreatesNoLinksByDefault(t *testing.T) {
	tmp := t.TempDir()
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runSkillInstallCommand([]string{
		"--data-agent-dir", filepath.Join(tmp, "agents", "skills"),
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("No symlinks created.")) {
		t.Fatalf("stdout missing no-link message: %q", out.String())
	}
}

func TestParseSkillInstallArgsRejectsUnknownFlag(t *testing.T) {
	_, err := parseSkillInstallArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseSkillInstallArgs() error = nil, want non-nil")
	}
}

func TestParseSkillInstallArgsKiroDirImpliesKiroLink(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--kiro-dir", "/tmp/kiro-skills"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableKiroLink {
		t.Fatal("enableKiroLink = false, want true when --kiro-dir is set")
	}
}

func TestParseSkillInstallArgsOpenClawDirImpliesOpenClawLink(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--openclaw-dir", "/tmp/openclaw-skills"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableOpenClawLink {
		t.Fatal("enableOpenClawLink = false, want true when --openclaw-dir is set")
	}
}

func TestParseSkillInstallArgsClaudeDirImpliesClaudeLink(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--claude-dir", "/tmp/claude-skills"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableClaudeLink {
		t.Fatal("enableClaudeLink = false, want true when --claude-dir is set")
	}
}

func TestParseSkillInstallArgsGuidanceFileImpliesGuidance(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance-file", "/tmp/AGENTS.md"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableGuidance {
		t.Fatal("enableGuidance = false, want true when --guidance-file is set")
	}
	if parsed.guidanceFile != "/tmp/AGENTS.md" {
		t.Fatalf("guidanceFile = %q, want %q", parsed.guidanceFile, "/tmp/AGENTS.md")
	}
}

func TestParseSkillInstallArgsGuidanceFollowsKiroLinkTarget(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance", "--kiro-link"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if parsed.guidanceFile != skill.DefaultKiroGuidanceFile() {
		t.Fatalf("guidanceFile = %q, want %q", parsed.guidanceFile, skill.DefaultKiroGuidanceFile())
	}
}

func TestParseSkillInstallArgsGuidanceFollowsClaudeLinkTarget(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance", "--claude-link"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if parsed.guidanceFile != skill.DefaultClaudeGuidanceFile() {
		t.Fatalf("guidanceFile = %q, want %q", parsed.guidanceFile, skill.DefaultClaudeGuidanceFile())
	}
}

func TestParseSkillInstallArgsGuidanceFollowsOpenClawLinkTarget(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance", "--openclaw-link"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if parsed.guidanceFile != skill.DefaultOpenClawGuidanceFile() {
		t.Fatalf("guidanceFile = %q, want %q", parsed.guidanceFile, skill.DefaultOpenClawGuidanceFile())
	}
}

func TestParseSkillInstallArgsGuidanceWithMultipleLinksRequiresGuidanceFile(t *testing.T) {
	_, err := parseSkillInstallArgs([]string{"--guidance", "--kiro-link", "--openclaw-link"})
	if err == nil {
		t.Fatal("parseSkillInstallArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "requires --guidance-file") {
		t.Fatalf("parseSkillInstallArgs() error = %q, want guidance-file requirement", err.Error())
	}
}

func TestParseSkillInstallArgsGuidanceFileOverridesMultipleLinks(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance", "--kiro-link", "--openclaw-link", "--guidance-file", "/tmp/AGENTS.md"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if parsed.guidanceFile != "/tmp/AGENTS.md" {
		t.Fatalf("guidanceFile = %q, want %q", parsed.guidanceFile, "/tmp/AGENTS.md")
	}
}

func TestParseSkillInstallArgsGuidanceTextImpliesGuidance(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--guidance-text", "Prefer mcpx"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableGuidance {
		t.Fatal("enableGuidance = false, want true when --guidance-text is set")
	}
	if parsed.guidanceText != "Prefer mcpx" {
		t.Fatalf("guidanceText = %q, want %q", parsed.guidanceText, "Prefer mcpx")
	}
}

func TestParseSkillInstallArgsRejectsFlagLikeValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "data-agent-dir",
			args: []string{"--data-agent-dir", "--claude-link"},
			want: "missing value for --data-agent-dir",
		},
		{
			name: "claude-dir",
			args: []string{"--claude-dir", "--kiro-link"},
			want: "missing value for --claude-dir",
		},
		{
			name: "kiro-dir",
			args: []string{"--kiro-dir", "--claude-link"},
			want: "missing value for --kiro-dir",
		},
		{
			name: "openclaw-dir",
			args: []string{"--openclaw-dir", "--claude-link"},
			want: "missing value for --openclaw-dir",
		},
		{
			name: "guidance-file",
			args: []string{"--guidance-file", "--claude-link"},
			want: "missing value for --guidance-file",
		},
		{
			name: "guidance-text",
			args: []string{"--guidance-text", "--claude-link"},
			want: "missing value for --guidance-text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSkillInstallArgs(tt.args)
			if err == nil {
				t.Fatal("parseSkillInstallArgs() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseSkillInstallArgs() error = %q, want to contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestParseSkillInstallCommandArgsParsesServerAndFlags(t *testing.T) {
	parsed, installForServer, err := parseSkillInstallCommandArgs([]string{"github", "--kiro-link", "--openclaw-dir", "/tmp/openclaw-skills"})
	if err != nil {
		t.Fatalf("parseSkillInstallCommandArgs() error = %v, want nil", err)
	}
	if !installForServer {
		t.Fatal("installForServer = false, want true")
	}
	if parsed.server != "github" {
		t.Fatalf("server = %q, want %q", parsed.server, "github")
	}
	if !parsed.enableKiroLink {
		t.Fatal("enableKiroLink = false, want true when --kiro-link is set")
	}
	if !parsed.enableOpenClawLink {
		t.Fatal("enableOpenClawLink = false, want true when --openclaw-dir is set")
	}
	if parsed.openClawDir != "/tmp/openclaw-skills" {
		t.Fatalf("openClawDir = %q, want %q", parsed.openClawDir, "/tmp/openclaw-skills")
	}
}

func TestRunSkillInstallCommandSupportsServerSyntax(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")

	oldInstallServerSkillCommandFn := installServerSkillCommandFn
	defer func() { installServerSkillCommandFn = oldInstallServerSkillCommandFn }()
	installServerSkillCommandFn = func(server string, _ *skillInstallArgs) (*skill.InstallResult, error) {
		if server != "github" {
			return nil, errors.New("unexpected server")
		}
		return skill.InstallSkill("mcpx-github", []byte("# test\n"), skill.InstallOptions{
			DataAgentDir: dataDir,
			ClaudeDir:    claudeDir,
		})
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runSkillInstallCommand([]string{"github"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runSkillInstallCommand() code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Installed skill file:")) {
		t.Fatalf("stdout missing install message: %q", out.String())
	}
}

func TestPrintSkillHelpIncludesServerInstallUsage(t *testing.T) {
	var out bytes.Buffer
	printSkillHelp(&out)

	help := out.String()
	if !strings.Contains(help, "mcpx skill install [<server>] [FLAGS]") {
		t.Fatalf("help output missing server install usage: %q", help)
	}
	if !strings.Contains(help, "install    Install built-in skill") {
		t.Fatalf("help output missing install command description: %q", help)
	}
}

func TestPrintSkillInstallHelpIncludesLinkFlags(t *testing.T) {
	var out bytes.Buffer
	printSkillInstallHelp(&out)

	help := out.String()
	if !strings.Contains(help, "--claude-link") {
		t.Fatalf("help output missing --claude-link guidance: %q", help)
	}
	if !strings.Contains(help, "--kiro-link") {
		t.Fatalf("help output missing --kiro-link guidance: %q", help)
	}
	if !strings.Contains(help, "--openclaw-link") {
		t.Fatalf("help output missing --openclaw-link guidance: %q", help)
	}
	if !strings.Contains(help, "--guidance") {
		t.Fatalf("help output missing --guidance flag: %q", help)
	}
	if !strings.Contains(help, "--guidance-file") {
		t.Fatalf("help output missing --guidance-file flag: %q", help)
	}
	if !strings.Contains(help, "--guidance-text") {
		t.Fatalf("help output missing --guidance-text flag: %q", help)
	}
}

func TestRunSkillCommandUnknownSubcommandReturnsUsage(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runSkillCommand([]string{"unknown"}, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runSkillCommand() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "unknown skill command") {
		t.Fatalf("stderr = %q, want unknown command error", got)
	}
	if got := errOut.String(); !strings.Contains(got, "mcpx skill install [<server>] [FLAGS]") {
		t.Fatalf("stderr = %q, want skill help usage", got)
	}
}

func TestRunSkillInstallCommandClassifiesUnknownServerAsUsageError(t *testing.T) {
	oldInstallServerSkillCommandFn := installServerSkillCommandFn
	defer func() { installServerSkillCommandFn = oldInstallServerSkillCommandFn }()
	installServerSkillCommandFn = func(server string, _ *skillInstallArgs) (*skill.InstallResult, error) {
		return nil, errors.New("unknown server: " + server)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runSkillInstallCommand([]string{"missing-server"}, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runSkillInstallCommand() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "install server skill") {
		t.Fatalf("stderr = %q, want install-server context", got)
	}
}

func TestRunSkillInstallCommandClassifiesServerInstallFailuresAsInternal(t *testing.T) {
	oldInstallServerSkillCommandFn := installServerSkillCommandFn
	defer func() { installServerSkillCommandFn = oldInstallServerSkillCommandFn }()
	installServerSkillCommandFn = func(string, *skillInstallArgs) (*skill.InstallResult, error) {
		return nil, errors.New("permission denied")
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := runSkillInstallCommand([]string{"github"}, &out, &errOut)
	if code != ipc.ExitInternal {
		t.Fatalf("runSkillInstallCommand() code = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "install server skill") || !strings.Contains(got, "permission denied") {
		t.Fatalf("stderr = %q, want install failure context", got)
	}
}

func assertSymlinkTargets(t *testing.T, linkPath, targetPath string) {
	t.Helper()

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat %s: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", linkPath)
	}

	rawTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink %s: %v", linkPath, err)
	}
	resolved := rawTarget
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkPath), resolved)
	}
	if filepath.Clean(resolved) != filepath.Clean(targetPath) {
		t.Fatalf("symlink target = %q, want %q", resolved, targetPath)
	}
}
