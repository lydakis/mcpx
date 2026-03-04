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
	if got, want := DefaultOpenClawDir(), filepath.Join("/tmp/home", ".openclaw", "skills"); got != want {
		t.Fatalf("DefaultOpenClawDir() = %q, want %q", got, want)
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

func TestInstallMCPXSkillSupportsOptionalOpenClawLink(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "agents", "skills")
	claudeDir := filepath.Join(tmp, "claude", "skills")
	openClawDir := filepath.Join(tmp, "openclaw", "skills")

	result, err := InstallMCPXSkill(InstallOptions{
		DataAgentDir:       dataDir,
		ClaudeDir:          claudeDir,
		OpenClawDir:        openClawDir,
		EnableOpenClawLink: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPXSkill() error = %v", err)
	}
	if result.OpenClawLink == "" {
		t.Fatal("OpenClawLink is empty, want symlink path")
	}
	assertSymlinkTarget(t, result.OpenClawLink, filepath.Join(dataDir, Name))
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

func TestResolveLinkTargetHandlesAbsoluteAndRelativeTargets(t *testing.T) {
	linkPath := "/tmp/skills/mcpx"

	if got, want := resolveLinkTarget(linkPath, "/opt/mcpx"), filepath.Clean("/opt/mcpx"); got != want {
		t.Fatalf("resolveLinkTarget(abs) = %q, want %q", got, want)
	}
	if got, want := resolveLinkTarget(linkPath, "../targets/mcpx"), filepath.Clean("/tmp/targets/mcpx"); got != want {
		t.Fatalf("resolveLinkTarget(rel) = %q, want %q", got, want)
	}
}

func TestSamePathRecognizesEquivalentPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll(target): %v", err)
	}

	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("Symlink(alias): %v", err)
	}

	if !samePath(target, target) {
		t.Fatal("samePath(target, target) = false, want true")
	}
	if !samePath(alias, target) {
		t.Fatal("samePath(alias, target) = false, want true")
	}
	if samePath(filepath.Join(root, "missing-a"), filepath.Join(root, "missing-b")) {
		t.Fatal("samePath(missing-a, missing-b) = true, want false")
	}
}

func TestEnsureTrailingNewlineAddsOnlyWhenNeeded(t *testing.T) {
	if got := ensureTrailingNewline(nil); got != nil {
		t.Fatalf("ensureTrailingNewline(nil) = %#v, want nil", got)
	}
	if got := string(ensureTrailingNewline([]byte("line"))); got != "line\n" {
		t.Fatalf("ensureTrailingNewline(no newline) = %q, want %q", got, "line\\n")
	}
	if got := string(ensureTrailingNewline([]byte("line\n"))); got != "line\n" {
		t.Fatalf("ensureTrailingNewline(existing newline) = %q, want %q", got, "line\\n")
	}
}

func TestHomeDirFallsBackToUserHomeWhenHOMEUnset(t *testing.T) {
	t.Setenv("HOME", "")
	want, _ := os.UserHomeDir()
	if got := homeDir(); got != want {
		t.Fatalf("homeDir() = %q, want %q", got, want)
	}
}

func TestEnsureSymlinkHandlesExistingLinks(t *testing.T) {
	root := t.TempDir()
	targetA := filepath.Join(root, "target-a")
	targetB := filepath.Join(root, "target-b")
	if err := os.MkdirAll(targetA, 0o755); err != nil {
		t.Fatalf("MkdirAll(targetA): %v", err)
	}
	if err := os.MkdirAll(targetB, 0o755); err != nil {
		t.Fatalf("MkdirAll(targetB): %v", err)
	}

	linkPath := filepath.Join(root, "link")
	initialTarget, err := ensureSymlink(targetA, linkPath)
	if err != nil {
		t.Fatalf("ensureSymlink(initial) error = %v", err)
	}
	if initialTarget == "" {
		t.Fatal("ensureSymlink(initial) target = empty")
	}

	sameTarget, err := ensureSymlink(targetA, linkPath)
	if err != nil {
		t.Fatalf("ensureSymlink(same target) error = %v", err)
	}
	if sameTarget != initialTarget {
		t.Fatalf("ensureSymlink(same target) = %q, want %q", sameTarget, initialTarget)
	}

	replacedTarget, err := ensureSymlink(targetB, linkPath)
	if err != nil {
		t.Fatalf("ensureSymlink(replace target) error = %v", err)
	}
	if replacedTarget == "" {
		t.Fatal("ensureSymlink(replace target) = empty target")
	}
	assertSymlinkTarget(t, linkPath, targetB)
}
