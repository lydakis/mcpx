package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
)

func TestMaybeHandleSkillCommandRunsWhenNameUnclaimed(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	codexDir := filepath.Join(tmp, "codex", "skills")

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
