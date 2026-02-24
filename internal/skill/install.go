package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Name is the built-in mcpx skill folder name.
	Name = "mcpx"
)

// InstallOptions controls where the built-in mcpx skill is installed.
type InstallOptions struct {
	DataAgentDir    string
	ClaudeDir       string
	SkipClaudeLink  bool
	CodexDir        string
	EnableCodexLink bool
}

// InstallResult describes where the skill was installed.
type InstallResult struct {
	SkillDir         string
	SkillFile        string
	ClaudeLink       string
	ClaudeLinkTarget string
	CodexLink        string
	CodexLinkTarget  string
}

var (
	//go:embed assets/mcpx/SKILL.md
	embeddedSkillFS embed.FS
)

// DefaultDataAgentDir returns the default data-agent skills directory.
func DefaultDataAgentDir() string {
	return filepath.Join(homeDir(), ".agents", "skills")
}

// DefaultCodexDir returns the default Codex skills directory.
func DefaultCodexDir() string {
	return filepath.Join(homeDir(), ".codex", "skills")
}

// DefaultClaudeDir returns the default Claude Code skills directory.
func DefaultClaudeDir() string {
	return filepath.Join(homeDir(), ".claude", "skills")
}

// InstallMCPXSkill installs the built-in mcpx skill.
func InstallMCPXSkill(opts InstallOptions) (*InstallResult, error) {
	dataAgentDir := strings.TrimSpace(opts.DataAgentDir)
	if dataAgentDir == "" {
		dataAgentDir = DefaultDataAgentDir()
	}

	claudeDir := strings.TrimSpace(opts.ClaudeDir)
	if claudeDir == "" {
		claudeDir = DefaultClaudeDir()
	}

	codexDir := strings.TrimSpace(opts.CodexDir)
	if opts.EnableCodexLink && codexDir == "" {
		codexDir = DefaultCodexDir()
	}

	skillDir := filepath.Join(dataAgentDir, Name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating skill directory: %w", err)
	}

	content, err := fs.ReadFile(embeddedSkillFS, "assets/mcpx/SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("reading embedded skill: %w", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, ensureTrailingNewline(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing skill file: %w", err)
	}

	result := &InstallResult{
		SkillDir:  skillDir,
		SkillFile: skillFile,
	}

	if !opts.SkipClaudeLink {
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating claude skills directory: %w", err)
		}

		linkPath := filepath.Join(claudeDir, Name)
		linkTarget, err := ensureSymlink(skillDir, linkPath)
		if err != nil {
			return nil, fmt.Errorf("linking claude skill: %w", err)
		}
		result.ClaudeLink = linkPath
		result.ClaudeLinkTarget = linkTarget
	}

	if opts.EnableCodexLink {
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating codex skills directory: %w", err)
		}

		linkPath := filepath.Join(codexDir, Name)
		linkTarget, err := ensureSymlink(skillDir, linkPath)
		if err != nil {
			return nil, fmt.Errorf("linking codex skill: %w", err)
		}
		result.CodexLink = linkPath
		result.CodexLinkTarget = linkTarget
	}

	return result, nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func ensureTrailingNewline(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data
	}
	return append(data, '\n')
}

func ensureSymlink(target, linkPath string) (string, error) {
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("path exists and is not a symlink: %s", linkPath)
		}

		existingTarget, err := os.Readlink(linkPath)
		if err != nil {
			return "", fmt.Errorf("reading existing symlink: %w", err)
		}

		if samePath(resolveLinkTarget(linkPath, existingTarget), target) {
			return existingTarget, nil
		}

		if err := os.Remove(linkPath); err != nil {
			return "", fmt.Errorf("removing existing symlink: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking existing link: %w", err)
	}

	linkTarget := target
	if relTarget, err := filepath.Rel(filepath.Dir(linkPath), target); err == nil && relTarget != "" {
		linkTarget = relTarget
	}

	if err := os.Symlink(linkTarget, linkPath); err != nil {
		return "", fmt.Errorf("creating symlink: %w", err)
	}
	return linkTarget, nil
}

func resolveLinkTarget(linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}

func samePath(pathA, pathB string) bool {
	cleanA := filepath.Clean(pathA)
	cleanB := filepath.Clean(pathB)
	if cleanA == cleanB {
		return true
	}

	if resolved, err := filepath.EvalSymlinks(cleanA); err == nil {
		cleanA = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(cleanB); err == nil {
		cleanB = filepath.Clean(resolved)
	}
	return cleanA == cleanB
}
