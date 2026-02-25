package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirsUseHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")

	if got, want := DefaultDataAgentDir(), filepath.Join("/tmp/home", ".agents", "skills"); got != want {
		t.Fatalf("DefaultDataAgentDir() = %q, want %q", got, want)
	}
	if got, want := DefaultClaudeDir(), filepath.Join("/tmp/home", ".claude", "skills"); got != want {
		t.Fatalf("DefaultClaudeDir() = %q, want %q", got, want)
	}
	if got, want := DefaultCodexDir(), filepath.Join("/tmp/home", ".codex", "skills"); got != want {
		t.Fatalf("DefaultCodexDir() = %q, want %q", got, want)
	}
	if got, want := DefaultKiroDir(), filepath.Join("/tmp/home", ".kiro", "skills"); got != want {
		t.Fatalf("DefaultKiroDir() = %q, want %q", got, want)
	}
}

func TestInstallMCPXSkillCreatesSkillAndClaudeLink(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")

	result, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir: dataDir,
		ClaudeDir:    claudeDir,
	})
	if err != nil {
		t.Fatalf("InstallMCPXSkill() error = %v", err)
	}

	if _, err := os.Stat(result.SkillFile); err != nil {
		t.Fatalf("skill file missing at %s: %v", result.SkillFile, err)
	}
	if result.ClaudeLink == "" {
		t.Fatal("ClaudeLink is empty, want symlink path")
	}

	assertSymlinkTarget(t, result.ClaudeLink, filepath.Join(dataDir, Name))
}

func TestInstallMCPXSkillSkipsClaudeLink(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")

	result, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir:   dataDir,
		ClaudeDir:      claudeDir,
		SkipClaudeLink: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPXSkill() error = %v", err)
	}
	if result.ClaudeLink != "" {
		t.Fatalf("ClaudeLink = %q, want empty", result.ClaudeLink)
	}
	if _, err := os.Lstat(filepath.Join(claudeDir, Name)); !os.IsNotExist(err) {
		t.Fatalf("expected no claude symlink, got err=%v", err)
	}
}

func TestInstallMCPXSkillSupportsOptionalCodexLink(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	codexDir := filepath.Join(tmp, "codex", "skills")

	result, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir:    dataDir,
		ClaudeDir:       claudeDir,
		CodexDir:        codexDir,
		EnableCodexLink: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPXSkill() error = %v", err)
	}
	if result.CodexLink == "" {
		t.Fatal("CodexLink is empty, want symlink path")
	}
	assertSymlinkTarget(t, result.CodexLink, filepath.Join(dataDir, Name))
}

func TestInstallMCPXSkillSupportsOptionalKiroLink(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	kiroDir := filepath.Join(tmp, "kiro", "skills")

	result, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir:   dataDir,
		ClaudeDir:      claudeDir,
		KiroDir:        kiroDir,
		EnableKiroLink: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPXSkill() error = %v", err)
	}
	if result.KiroLink == "" {
		t.Fatal("KiroLink is empty, want symlink path")
	}
	assertSymlinkTarget(t, result.KiroLink, filepath.Join(dataDir, Name))
}

func TestInstallMCPXSkillFailsWhenLinkPathExistsAsFile(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	codexDir := filepath.Join(tmp, "codex", "skills")

	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	blockingPath := filepath.Join(codexDir, Name)
	if err := os.WriteFile(blockingPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking path: %v", err)
	}

	_, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir:    dataDir,
		ClaudeDir:       claudeDir,
		CodexDir:        codexDir,
		EnableCodexLink: true,
	})
	if err == nil {
		t.Fatal("InstallMCPXSkill() error = nil, want non-nil")
	}
}

func assertSymlinkTarget(t *testing.T, linkPath, expectedTarget string) {
	t.Helper()

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat %s: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", linkPath)
	}

	linkRaw, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink %s: %v", linkPath, err)
	}
	resolved := linkRaw
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkPath), resolved)
	}

	if filepath.Clean(resolved) != filepath.Clean(expectedTarget) {
		t.Fatalf("symlink target = %q, want %q", resolved, expectedTarget)
	}
}
