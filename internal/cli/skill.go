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
	dataAgentDir       string
	claudeDir          string
	enableClaudeLink   bool
	kiroDir            string
	enableKiroLink     bool
	openClawDir        string
	enableOpenClawLink bool
	enableGuidance     bool
	guidanceFile       string
	guidanceText       string
	help               bool
}

type skillInstallServerArgs struct {
	server string
	skillInstallArgs
}

var installServerSkillCommandFn = installServerSkill

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
	parsed, installForServer, err := parseSkillInstallCommandArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printSkillInstallHelp(stdout)
		return ipc.ExitOK
	}

	var result *skill.InstallResult
	var installErr error
	if installForServer {
		result, installErr = installServerSkillCommandFn(parsed.server, &parsed.skillInstallArgs)
	} else {
		result, installErr = skill.InstallMCPXSkill(skill.InstallOptions{
			DataAgentDir:       parsed.dataAgentDir,
			ClaudeDir:          parsed.claudeDir,
			EnableClaudeLink:   parsed.enableClaudeLink,
			KiroDir:            parsed.kiroDir,
			EnableKiroLink:     parsed.enableKiroLink,
			OpenClawDir:        parsed.openClawDir,
			EnableOpenClawLink: parsed.enableOpenClawLink,
			EnableGuidance:     parsed.enableGuidance,
			GuidanceFile:       parsed.guidanceFile,
			GuidanceText:       parsed.guidanceText,
		})
	}
	if installErr != nil {
		if installForServer {
			fmt.Fprintf(stderr, "mcpx: install server skill: %v\n", installErr)
			if strings.Contains(strings.ToLower(installErr.Error()), "unknown server") {
				return ipc.ExitUsageErr
			}
			return ipc.ExitInternal
		}
		fmt.Fprintf(stderr, "mcpx: install skill: %v\n", installErr)
		return ipc.ExitInternal
	}

	printInstalledSkillResult(stdout, result)

	return ipc.ExitOK
}

func printInstalledSkillResult(stdout io.Writer, result *skill.InstallResult) {
	fmt.Fprintf(stdout, "Installed skill file: %s\n", result.SkillFile)
	if result.ClaudeLink != "" {
		fmt.Fprintf(stdout, "Claude link: %s -> %s\n", result.ClaudeLink, result.ClaudeLinkTarget)
	}
	if result.KiroLink != "" {
		fmt.Fprintf(stdout, "Kiro link: %s -> %s\n", result.KiroLink, result.KiroLinkTarget)
	}
	if result.OpenClawLink != "" {
		fmt.Fprintf(stdout, "OpenClaw link: %s -> %s\n", result.OpenClawLink, result.OpenClawLinkTarget)
	}
	if result.GuidanceFile != "" {
		fmt.Fprintf(stdout, "Guidance file: %s\n", result.GuidanceFile)
	}
	if result.ClaudeLink == "" && result.KiroLink == "" && result.OpenClawLink == "" {
		fmt.Fprintln(stdout, "No symlinks created.")
	}
}

func parseSkillInstallArgs(args []string) (*skillInstallArgs, error) {
	parsed := &skillInstallArgs{}
	claudeDirSet := false
	kiroDirSet := false
	openClawDirSet := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case arg == "--claude-link":
			parsed.enableClaudeLink = true
		case arg == "--kiro-link":
			parsed.enableKiroLink = true
		case arg == "--openclaw-link":
			parsed.enableOpenClawLink = true
		case arg == "--guidance":
			parsed.enableGuidance = true
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
			claudeDirSet = true
		case arg == "--claude-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--claude-dir")
			if err != nil {
				return nil, err
			}
			parsed.claudeDir = value
			claudeDirSet = true
		case strings.HasPrefix(arg, "--kiro-dir="):
			parsed.kiroDir = strings.TrimSpace(strings.TrimPrefix(arg, "--kiro-dir="))
			kiroDirSet = true
		case strings.HasPrefix(arg, "--openclaw-dir="):
			parsed.openClawDir = strings.TrimSpace(strings.TrimPrefix(arg, "--openclaw-dir="))
			openClawDirSet = true
		case arg == "--kiro-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--kiro-dir")
			if err != nil {
				return nil, err
			}
			parsed.kiroDir = value
			kiroDirSet = true
		case arg == "--openclaw-dir":
			value, err := parseSkillInstallPathArg(args, &i, "--openclaw-dir")
			if err != nil {
				return nil, err
			}
			parsed.openClawDir = value
			openClawDirSet = true
		case strings.HasPrefix(arg, "--guidance-file="):
			parsed.guidanceFile = strings.TrimSpace(strings.TrimPrefix(arg, "--guidance-file="))
			parsed.enableGuidance = true
		case arg == "--guidance-file":
			value, err := parseSkillInstallPathArg(args, &i, "--guidance-file")
			if err != nil {
				return nil, err
			}
			parsed.guidanceFile = value
			parsed.enableGuidance = true
		case strings.HasPrefix(arg, "--guidance-text="):
			parsed.guidanceText = strings.TrimSpace(strings.TrimPrefix(arg, "--guidance-text="))
			parsed.enableGuidance = true
		case arg == "--guidance-text":
			value, err := parseSkillInstallPathArg(args, &i, "--guidance-text")
			if err != nil {
				return nil, err
			}
			parsed.guidanceText = value
			parsed.enableGuidance = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			return nil, fmt.Errorf("unexpected positional argument: %s", arg)
		}
	}

	if !parsed.help {
		if claudeDirSet {
			parsed.enableClaudeLink = true
		}
		if kiroDirSet {
			parsed.enableKiroLink = true
		}
		if openClawDirSet {
			parsed.enableOpenClawLink = true
		}
		if parsed.dataAgentDir == "" {
			parsed.dataAgentDir = skill.DefaultDataAgentDir()
		}
		if parsed.enableClaudeLink && parsed.claudeDir == "" {
			parsed.claudeDir = skill.DefaultClaudeDir()
		}
		if parsed.kiroDir == "" {
			parsed.kiroDir = skill.DefaultKiroDir()
		}
		if parsed.openClawDir == "" {
			parsed.openClawDir = skill.DefaultOpenClawDir()
		}
		if parsed.enableGuidance && parsed.guidanceFile == "" {
			inferredPath, err := inferGuidanceTargetFromSkillLinks(parsed)
			if err != nil {
				return nil, err
			}
			parsed.guidanceFile = inferredPath
		}
	}

	return parsed, nil
}

func parseSkillInstallCommandArgs(args []string) (*skillInstallServerArgs, bool, error) {
	parsed := &skillInstallServerArgs{}
	if len(args) == 0 || isHelpFlag(args[0]) || strings.HasPrefix(args[0], "-") {
		installParsed, err := parseSkillInstallArgs(args)
		if err != nil {
			return nil, false, err
		}
		parsed.skillInstallArgs = *installParsed
		return parsed, false, nil
	}

	first := strings.TrimSpace(args[0])
	if first == "" {
		return nil, false, fmt.Errorf("missing server (usage: mcpx skill install <server>)")
	}
	parsed.server = first

	installParsed, err := parseSkillInstallArgs(args[1:])
	if err != nil {
		return nil, false, err
	}
	parsed.skillInstallArgs = *installParsed
	return parsed, true, nil
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
	fmt.Fprintln(out, "  mcpx skill install [<server>] [FLAGS]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  install    Install built-in skill, or a server-specific skill when <server> is provided.")
	fmt.Fprintln(out, "")
	printSkillInstallHelp(out)
}

func printSkillInstallHelp(out io.Writer) {
	fmt.Fprintln(out, "Install flags:")
	fmt.Fprintf(out, "  --data-agent-dir <path>  Skill root (default: %s)\n", skill.DefaultDataAgentDir())
	fmt.Fprintf(out, "  --claude-dir <path>      Claude skill link root (default: %s, implies --claude-link)\n", skill.DefaultClaudeDir())
	fmt.Fprintf(out, "  --kiro-dir <path>        Kiro skill link root (default: %s, implies --kiro-link)\n", skill.DefaultKiroDir())
	fmt.Fprintf(out, "  --openclaw-dir <path>    OpenClaw skill link root (default: %s, implies --openclaw-link)\n", skill.DefaultOpenClawDir())
	fmt.Fprintln(out, "  --claude-link            Also create ~/.claude/skills/mcpx symlink.")
	fmt.Fprintln(out, "  --kiro-link              Also create ~/.kiro/skills/mcpx symlink.")
	fmt.Fprintln(out, "  --openclaw-link          Also create ~/.openclaw/skills/mcpx symlink.")
	fmt.Fprintln(out, "  --guidance               Write/update managed guidance (default ~/.agents/AGENTS.md; follows a single claude/kiro/openclaw link target).")
	fmt.Fprintf(out, "  --guidance-file <path>   Guidance target file (default: %s, implies --guidance)\n", skill.DefaultGuidanceFile())
	fmt.Fprintln(out, "  --guidance-text <text>   Custom guidance text for the managed block (implies --guidance).")
	fmt.Fprintln(out, "  --help, -h               Show install help.")
}

func inferGuidanceTargetFromSkillLinks(parsed *skillInstallArgs) (string, error) {
	if parsed == nil {
		return skill.DefaultGuidanceFile(), nil
	}

	targets := make([]string, 0, 3)
	if parsed.enableClaudeLink {
		targets = append(targets, skill.DefaultClaudeGuidanceFile())
	}
	if parsed.enableKiroLink {
		targets = append(targets, skill.DefaultKiroGuidanceFile())
	}
	if parsed.enableOpenClawLink {
		targets = append(targets, skill.DefaultOpenClawGuidanceFile())
	}

	switch len(targets) {
	case 0:
		return skill.DefaultGuidanceFile(), nil
	case 1:
		return targets[0], nil
	default:
		return "", fmt.Errorf("--guidance with multiple link targets requires --guidance-file")
	}
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h"
}
