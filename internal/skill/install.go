package skill

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// Name is the built-in mcpx skill folder name.
	Name                = "mcpx"
	guidanceStartMarker = "<!-- MCPX MANAGED GUIDANCE START -->"
	guidanceEndMarker   = "<!-- MCPX MANAGED GUIDANCE END -->"
)

var (
	ErrInvalidSkillName = errors.New("invalid skill name")
	skillNameRe         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// InstallOptions controls where the built-in mcpx skill is installed.
type InstallOptions struct {
	DataAgentDir       string
	ClaudeDir          string
	EnableClaudeLink   bool
	KiroDir            string
	EnableKiroLink     bool
	OpenClawDir        string
	EnableOpenClawLink bool
	EnableGuidance     bool
	GuidanceFile       string
	GuidanceText       string
}

// InstallResult describes where the skill was installed.
type InstallResult struct {
	SkillDir           string
	SkillFile          string
	ClaudeLink         string
	ClaudeLinkTarget   string
	KiroLink           string
	KiroLinkTarget     string
	OpenClawLink       string
	OpenClawLinkTarget string
	GuidanceFile       string
}

var (
	//go:embed assets/mcpx/SKILL.md
	embeddedSkillFS embed.FS
)

// DefaultDataAgentDir returns the default data-agent skills directory.
func DefaultDataAgentDir() string {
	return filepath.Join(homeDir(), ".agents", "skills")
}

// DefaultKiroDir returns the default Kiro skills directory.
func DefaultKiroDir() string {
	return filepath.Join(homeDir(), ".kiro", "skills")
}

// DefaultOpenClawDir returns the default OpenClaw skills directory.
func DefaultOpenClawDir() string {
	return filepath.Join(homeDir(), ".openclaw", "skills")
}

// DefaultClaudeDir returns the default Claude Code skills directory.
func DefaultClaudeDir() string {
	return filepath.Join(homeDir(), ".claude", "skills")
}

// DefaultClaudeGuidanceFile returns the default Claude guidance path.
func DefaultClaudeGuidanceFile() string {
	return filepath.Join(homeDir(), ".claude", "CLAUDE.md")
}

// DefaultGuidanceFile returns the default global AGENTS.md path.
func DefaultGuidanceFile() string {
	return filepath.Join(homeDir(), ".agents", "AGENTS.md")
}

// DefaultKiroGuidanceFile returns the default Kiro guidance path.
func DefaultKiroGuidanceFile() string {
	return filepath.Join(homeDir(), ".kiro", "AGENTS.md")
}

// DefaultOpenClawGuidanceFile returns the default OpenClaw guidance path.
func DefaultOpenClawGuidanceFile() string {
	return filepath.Join(homeDir(), ".openclaw", "AGENTS.md")
}

// InstallMCPXSkill installs the built-in mcpx skill.
func InstallMCPXSkill(opts InstallOptions) (*InstallResult, error) {
	content, err := fs.ReadFile(embeddedSkillFS, "assets/mcpx/SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("reading embedded skill: %w", err)
	}
	return InstallSkill(Name, content, opts)
}

// InstallSkill installs a named skill from caller-provided content.
func InstallSkill(name string, content []byte, opts InstallOptions) (*InstallResult, error) {
	name = strings.TrimSpace(name)
	if !skillNameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSkillName, name)
	}

	dataAgentDir := strings.TrimSpace(opts.DataAgentDir)
	if dataAgentDir == "" {
		dataAgentDir = DefaultDataAgentDir()
	}

	claudeDir := strings.TrimSpace(opts.ClaudeDir)
	if opts.EnableClaudeLink && claudeDir == "" {
		claudeDir = DefaultClaudeDir()
	}

	kiroDir := strings.TrimSpace(opts.KiroDir)
	if opts.EnableKiroLink && kiroDir == "" {
		kiroDir = DefaultKiroDir()
	}
	openClawDir := strings.TrimSpace(opts.OpenClawDir)
	if opts.EnableOpenClawLink && openClawDir == "" {
		openClawDir = DefaultOpenClawDir()
	}

	skillDir := filepath.Join(dataAgentDir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating skill directory: %w", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, ensureTrailingNewline(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing skill file: %w", err)
	}

	result := &InstallResult{
		SkillDir:  skillDir,
		SkillFile: skillFile,
	}

	if opts.EnableClaudeLink {
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating claude skills directory: %w", err)
		}

		linkPath := filepath.Join(claudeDir, name)
		linkTarget, err := ensureSymlink(skillDir, linkPath)
		if err != nil {
			return nil, fmt.Errorf("linking claude skill: %w", err)
		}
		result.ClaudeLink = linkPath
		result.ClaudeLinkTarget = linkTarget
	}

	if opts.EnableKiroLink {
		if err := os.MkdirAll(kiroDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating kiro skills directory: %w", err)
		}

		linkPath := filepath.Join(kiroDir, name)
		linkTarget, err := ensureSymlink(skillDir, linkPath)
		if err != nil {
			return nil, fmt.Errorf("linking kiro skill: %w", err)
		}
		result.KiroLink = linkPath
		result.KiroLinkTarget = linkTarget
	}

	if opts.EnableOpenClawLink {
		if err := os.MkdirAll(openClawDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating openclaw skills directory: %w", err)
		}

		linkPath := filepath.Join(openClawDir, name)
		linkTarget, err := ensureSymlink(skillDir, linkPath)
		if err != nil {
			return nil, fmt.Errorf("linking openclaw skill: %w", err)
		}
		result.OpenClawLink = linkPath
		result.OpenClawLinkTarget = linkTarget
	}

	if opts.EnableGuidance {
		guidanceFile := strings.TrimSpace(opts.GuidanceFile)
		if guidanceFile == "" {
			guidanceFile = DefaultGuidanceFile()
		}
		if err := installManagedGuidance(guidanceFile, opts.GuidanceText); err != nil {
			return nil, fmt.Errorf("writing guidance file: %w", err)
		}
		result.GuidanceFile = guidanceFile
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

func installManagedGuidance(path string, customText string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("guidance file path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating guidance directory: %w", err)
	}

	var existing []byte
	raw, err := os.ReadFile(path)
	if err == nil {
		existing = raw
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading guidance file: %w", err)
	}

	block := renderManagedGuidanceBlock(customText)
	next, err := upsertManagedGuidanceBlock(existing, block)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, ensureTrailingNewline(next), 0o644); err != nil {
		return fmt.Errorf("writing guidance file: %w", err)
	}
	return nil
}

func renderManagedGuidanceBlock(customText string) string {
	text := strings.TrimSpace(customText)
	if text == "" {
		text = "Prefer using `mcpx` plus the installed `mcpx` skill for MCP tasks that benefit from CLI composition (`|`, redirection, `jq`, scripts, caching, shims). Use direct MCP tool calls when raw structured output is explicitly required."
	}

	var b strings.Builder
	b.WriteString(guidanceStartMarker)
	b.WriteString("\n## mcpx guidance\n\n")
	b.WriteString(text)
	b.WriteString("\n")
	b.WriteString(guidanceEndMarker)
	return b.String()
}

func upsertManagedGuidanceBlock(existing []byte, block string) ([]byte, error) {
	content := string(existing)
	start := strings.Index(content, guidanceStartMarker)
	end := strings.Index(content, guidanceEndMarker)

	if start == -1 && end == -1 {
		trimmed := strings.TrimRight(content, "\n")
		if strings.TrimSpace(trimmed) == "" {
			return []byte(block), nil
		}
		return []byte(trimmed + "\n\n" + block), nil
	}
	if start == -1 || end == -1 || end < start {
		return nil, errors.New("malformed managed guidance block")
	}

	afterEnd := end + len(guidanceEndMarker)
	updated := content[:start] + block + content[afterEnd:]
	return []byte(updated), nil
}
