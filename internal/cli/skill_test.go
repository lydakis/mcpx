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
	codexDir := filepath.Join(tmp, "codex", "skills")
	kiroDir := filepath.Join(tmp, "kiro", "skills")

	cfg := &config.Config{Servers: map[string]config.ServerConfig{}}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, code := maybeHandleSkillCommand([]string{
		"skill",
		"install",
		"--data-agent-dir", dataDir,
		"--claude-dir", claudeDir,
		"--codex-link",
		"--codex-dir", codexDir,
		"--kiro-link",
		"--kiro-dir", kiroDir,
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
	assertSymlinkTargets(t, filepath.Join(codexDir, "mcpx"), filepath.Join(dataDir, "mcpx"))
	assertSymlinkTargets(t, filepath.Join(kiroDir, "mcpx"), filepath.Join(dataDir, "mcpx"))

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

func TestRunSkillInstallCommandSupportsNoClaudeLink(t *testing.T) {
	tmp := t.TempDir()
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runSkillInstallCommand([]string{
		"--data-agent-dir", filepath.Join(tmp, "agents", "skills"),
		"--no-claude-link",
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

func TestParseSkillInstallArgsCodexDirImpliesCodexLink(t *testing.T) {
	parsed, err := parseSkillInstallArgs([]string{"--codex-dir", "/tmp/codex-skills"})
	if err != nil {
		t.Fatalf("parseSkillInstallArgs() error = %v, want nil", err)
	}
	if !parsed.enableCodexLink {
		t.Fatal("enableCodexLink = false, want true when --codex-dir is set")
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

func TestParseSkillInstallArgsRejectsFlagLikeValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "data-agent-dir",
			args: []string{"--data-agent-dir", "--no-claude-link"},
			want: "missing value for --data-agent-dir",
		},
		{
			name: "claude-dir",
			args: []string{"--claude-dir", "--codex-link"},
			want: "missing value for --claude-dir",
		},
		{
			name: "codex-dir",
			args: []string{"--codex-dir", "--no-claude-link"},
			want: "missing value for --codex-dir",
		},
		{
			name: "kiro-dir",
			args: []string{"--kiro-dir", "--no-claude-link"},
			want: "missing value for --kiro-dir",
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

func TestParseSkillInstallServerArgsRequiresServer(t *testing.T) {
	_, err := parseSkillInstallServerArgs(nil)
	if err == nil {
		t.Fatal("parseSkillInstallServerArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing server") {
		t.Fatalf("parseSkillInstallServerArgs() error = %q, want missing-server message", err.Error())
	}
}

func TestParseSkillInstallServerArgsRejectsEmptyServer(t *testing.T) {
	_, err := parseSkillInstallServerArgs([]string{""})
	if err == nil {
		t.Fatal("parseSkillInstallServerArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing server") {
		t.Fatalf("parseSkillInstallServerArgs() error = %q, want missing-server message", err.Error())
	}
}

func TestParseSkillInstallServerArgsParsesServerAndFlags(t *testing.T) {
	parsed, err := parseSkillInstallServerArgs([]string{"github", "--codex-dir", "/tmp/codex"})
	if err != nil {
		t.Fatalf("parseSkillInstallServerArgs() error = %v, want nil", err)
	}
	if parsed.server != "github" {
		t.Fatalf("server = %q, want %q", parsed.server, "github")
	}
	if !parsed.enableCodexLink {
		t.Fatal("enableCodexLink = false, want true when --codex-dir is set")
	}
}

func TestRunSkillInstallServerCommand(t *testing.T) {
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
	code := runSkillInstallServerCommand([]string{"github"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runSkillInstallServerCommand() code = %d, want %d (stderr=%q)", code, ipc.ExitOK, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Installed skill file:")) {
		t.Fatalf("stdout missing install message: %q", out.String())
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
