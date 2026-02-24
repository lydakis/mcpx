package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/skill"
)

type skillInstallArgs struct {
	dataAgentDir    string
	claudeDir       string
	skipClaudeLink  bool
	codexDir        string
	enableCodexLink bool
	help            bool
}

func maybeHandleSkillCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "skill" {
		return false, 0
	}

	if cfg != nil {
		if _, ok := cfg.Servers["skill"]; ok {
			return false, 0
		}
	}

	return true, runSkillCommand(args[1:], stdout, stderr)
}

func runSkillCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || isHelpFlag(args[0]) || args[0] == "help" {
		printSkillHelp(stdout)
		return ipc.ExitOK
	}

	switch args[0] {
	case "install":
		return runSkillInstallCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mcpx: unknown skill command: %s\n", args[0])
		printSkillHelp(stderr)
		return ipc.ExitUsageErr
	}
}

func runSkillInstallCommand(args []string, stdout, stderr io.Writer) int {
	parsed, err := parseSkillInstallArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printSkillInstallHelp(stdout)
		return ipc.ExitOK
	}

	result, err := skill.InstallMCPXSkill(skill.InstallOptions{
		DataAgentDir:    parsed.dataAgentDir,
		ClaudeDir:       parsed.claudeDir,
		SkipClaudeLink:  parsed.skipClaudeLink,
		CodexDir:        parsed.codexDir,
		EnableCodexLink: parsed.enableCodexLink,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: install skill: %v\n", err)
		return ipc.ExitInternal
	}

	fmt.Fprintf(stdout, "Installed skill file: %s\n", result.SkillFile)
	if result.ClaudeLink != "" {
		fmt.Fprintf(stdout, "Claude link: %s -> %s\n", result.ClaudeLink, result.ClaudeLinkTarget)
	}
	if result.CodexLink != "" {
		fmt.Fprintf(stdout, "Codex link: %s -> %s\n", result.CodexLink, result.CodexLinkTarget)
	}
	if result.ClaudeLink == "" && result.CodexLink == "" {
		fmt.Fprintln(stdout, "No symlinks created.")
	}

	return ipc.ExitOK
}

func parseSkillInstallArgs(args []string) (*skillInstallArgs, error) {
	parsed := &skillInstallArgs{}
	codexDirSet := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case arg == "--no-claude-link":
			parsed.skipClaudeLink = true
		case arg == "--codex-link":
			parsed.enableCodexLink = true
		case strings.HasPrefix(arg, "--data-agent-dir="):
			parsed.dataAgentDir = strings.TrimSpace(strings.TrimPrefix(arg, "--data-agent-dir="))
		case arg == "--data-agent-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--data-agent-dir")
			if err != nil {
				return nil, err
			}
			parsed.dataAgentDir = value
		case strings.HasPrefix(arg, "--claude-dir="):
			parsed.claudeDir = strings.TrimSpace(strings.TrimPrefix(arg, "--claude-dir="))
		case arg == "--claude-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--claude-dir")
			if err != nil {
				return nil, err
			}
			parsed.claudeDir = value
		case strings.HasPrefix(arg, "--codex-dir="):
			parsed.codexDir = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-dir="))
			codexDirSet = true
		case arg == "--codex-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--codex-dir")
			if err != nil {
				return nil, err
			}
			parsed.codexDir = value
			codexDirSet = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			return nil, fmt.Errorf("unexpected positional argument: %s", arg)
		}
	}

	if !parsed.help {
		if codexDirSet {
			parsed.enableCodexLink = true
		}
		if parsed.dataAgentDir == "" {
			parsed.dataAgentDir = skill.DefaultDataAgentDir()
		}
		if parsed.claudeDir == "" {
			parsed.claudeDir = skill.DefaultClaudeDir()
		}
		if parsed.codexDir == "" {
			parsed.codexDir = skill.DefaultCodexDir()
		}
	}

	return parsed, nil
}

func parseSkillInstallPathArg(args []string, idx *int, flag string) (string, error) {
	if *idx+1 >= len(args) {
		return "", fmt.Errorf("missing value for %s", flag)
	}
	next := strings.TrimSpace(args[*idx+1])
	if next == "" || strings.HasPrefix(next, "-") {
		return "", fmt.Errorf("missing value for %s", flag)
	}
	*idx = *idx + 1
	return next, nil
}

func printSkillHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx skill install [FLAGS]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  install    Install the built-in mcpx skill.")
	fmt.Fprintln(out, "")
	printSkillInstallHelp(out)
}

func printSkillInstallHelp(out io.Writer) {
	fmt.Fprintln(out, "Install flags:")
	fmt.Fprintf(out, "  --data-agent-dir <path>  Skill root (default: %s)\n", skill.DefaultDataAgentDir())
	fmt.Fprintf(out, "  --claude-dir <path>      Claude skill link root (default: %s)\n", skill.DefaultClaudeDir())
	fmt.Fprintf(out, "  --codex-dir <path>       Legacy Codex link root (default: %s, implies --codex-link)\n", skill.DefaultCodexDir())
	fmt.Fprintln(out, "  --no-claude-link         Skip creating ~/.claude/skills/mcpx symlink.")
	fmt.Fprintln(out, "  --codex-link             Also create ~/.codex/skills/mcpx symlink.")
	fmt.Fprintln(out, "  --help, -h               Show install help.")
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h"
}
