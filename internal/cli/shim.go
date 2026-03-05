package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/shim"
	"github.com/lydakis/mcpx/internal/skill"
)

type shimInstallArgs struct {
	server             string
	dir                string
	installSkill       bool
	skillStrict        bool
	dataAgentDir       string
	claudeDir          string
	enableClaudeLink   bool
	kiroDir            string
	enableKiroLink     bool
	openClawDir        string
	enableOpenClawLink bool
	help               bool
}

type shimRemoveArgs struct {
	server string
	dir    string
	help   bool
}

type shimListArgs struct {
	dir  string
	help bool
}

var shimKnownServersFn = listShimKnownServers
var installServerSkillFn = installServerSkill

func maybeHandleShimCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "shim" {
		return false, 0
	}

	if cfg != nil {
		if _, ok := cfg.Servers["shim"]; ok {
			return false, 0
		}
	}

	return true, runShimCommandWithConfig(args[1:], cfg, stdout, stderr)
}

func runShimCommand(args []string, stdout, stderr io.Writer) int {
	return runShimCommandWithConfig(args, nil, stdout, stderr)
}

func runShimCommandWithConfig(args []string, cfg *config.Config, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || isHelpFlag(args[0]) {
		printShimHelp(stdout)
		return ipc.ExitOK
	}

	switch args[0] {
	case "install":
		return runShimInstallCommand(args[1:], cfg, stdout, stderr)
	case "remove":
		return runShimRemoveCommand(args[1:], stdout, stderr)
	case "list":
		return runShimListCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mcpx: unknown shim command: %s\n", args[0])
		printShimHelp(stderr)
		return ipc.ExitUsageErr
	}
}

func runShimInstallCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) int {
	parsed, err := parseShimInstallArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printShimInstallHelp(stdout)
		return ipc.ExitOK
	}
	if cfg != nil {
		ok, err := shimServerKnown(parsed.server, cfg)
		if err == nil && !ok {
			fmt.Fprintf(stderr, "mcpx: shim: unknown server: %q\n", parsed.server)
			return ipc.ExitUsageErr
		}
	}

	result, err := shim.Install(parsed.server, shim.InstallOptions{Dir: parsed.dir})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: shim: %v\n", err)
		return classifyShimErrorExitCode(err)
	}

	if result.AlreadyInstalled {
		fmt.Fprintf(stdout, "Shim %q already installed at %s\n", result.Server, result.Path)
	} else {
		fmt.Fprintf(stdout, "Installed shim %q at %s\n", result.Server, result.Path)
	}
	if !result.DirInPath {
		fmt.Fprintf(stderr, "mcpx: shim: warning: %s is not in PATH; add it to run shims directly\n", result.Dir)
	}

	if parsed.installSkill {
		skillResult, skillErr := installServerSkillFn(parsed.server, &skillInstallArgs{
			dataAgentDir:       parsed.dataAgentDir,
			claudeDir:          parsed.claudeDir,
			enableClaudeLink:   parsed.enableClaudeLink,
			kiroDir:            parsed.kiroDir,
			enableKiroLink:     parsed.enableKiroLink,
			openClawDir:        parsed.openClawDir,
			enableOpenClawLink: parsed.enableOpenClawLink,
		})
		if skillErr != nil {
			fmt.Fprintf(stderr, "mcpx: shim: warning: installed shim %q, but failed to install skill: %v\n", parsed.server, skillErr)
			if parsed.skillStrict {
				if strings.Contains(strings.ToLower(skillErr.Error()), "unknown server") {
					return ipc.ExitUsageErr
				}
				return ipc.ExitInternal
			}
			return ipc.ExitOK
		}
		printInstalledSkillResult(stdout, skillResult)
	}

	return ipc.ExitOK
}

func runShimRemoveCommand(args []string, stdout, stderr io.Writer) int {
	parsed, err := parseShimRemoveArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printShimRemoveHelp(stdout)
		return ipc.ExitOK
	}

	result, err := shim.Remove(parsed.server, shim.RemoveOptions{Dir: parsed.dir})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: shim: %v\n", err)
		return classifyShimErrorExitCode(err)
	}
	fmt.Fprintf(stdout, "Removed shim %q from %s\n", result.Server, result.Path)
	return ipc.ExitOK
}

func runShimListCommand(args []string, stdout, stderr io.Writer) int {
	parsed, err := parseShimListArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printShimListHelp(stdout)
		return ipc.ExitOK
	}

	entries, err := shim.List(shim.ListOptions{Dir: parsed.dir})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: shim: %v\n", err)
		return classifyShimErrorExitCode(err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No mcpx shims installed.")
		return ipc.ExitOK
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVER\tPATH")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%s\t%s\n", entry.Server, entry.Path)
	}
	_ = tw.Flush()
	return ipc.ExitOK
}

func classifyShimErrorExitCode(err error) int {
	if errors.Is(err, shim.ErrInvalidServerName) ||
		errors.Is(err, shim.ErrPathOccupied) ||
		errors.Is(err, shim.ErrCommandCollision) ||
		errors.Is(err, shim.ErrNotInstalled) ||
		errors.Is(err, shim.ErrNotManagedShim) {
		return ipc.ExitUsageErr
	}
	return ipc.ExitInternal
}

func parseShimInstallArgs(args []string) (*shimInstallArgs, error) {
	parsed := &shimInstallArgs{}
	skillFlagsSeen := false
	claudeDirSet := false
	kiroDirSet := false
	openClawDirSet := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case arg == "--skill":
			parsed.installSkill = true
		case arg == "--skill-strict":
			parsed.skillStrict = true
		case arg == "--claude-link":
			parsed.enableClaudeLink = true
			skillFlagsSeen = true
		case arg == "--kiro-link":
			parsed.enableKiroLink = true
			skillFlagsSeen = true
		case arg == "--openclaw-link":
			parsed.enableOpenClawLink = true
			skillFlagsSeen = true
		case strings.HasPrefix(arg, "--dir="):
			parsed.dir = strings.TrimSpace(strings.TrimPrefix(arg, "--dir="))
		case arg == "--dir":
			value, err := parseShimPathArg(args, &i, "--dir")
			if err != nil {
				return nil, err
			}
			parsed.dir = value
		case strings.HasPrefix(arg, "--data-agent-dir="):
			parsed.dataAgentDir = strings.TrimSpace(strings.TrimPrefix(arg, "--data-agent-dir="))
			skillFlagsSeen = true
		case arg == "--data-agent-dir":
			value, err := parseShimPathArg(args, &i, "--data-agent-dir")
			if err != nil {
				return nil, err
			}
			parsed.dataAgentDir = value
			skillFlagsSeen = true
		case strings.HasPrefix(arg, "--claude-dir="):
			parsed.claudeDir = strings.TrimSpace(strings.TrimPrefix(arg, "--claude-dir="))
			parsed.enableClaudeLink = true
			claudeDirSet = true
			skillFlagsSeen = true
		case arg == "--claude-dir":
			value, err := parseShimPathArg(args, &i, "--claude-dir")
			if err != nil {
				return nil, err
			}
			parsed.claudeDir = value
			parsed.enableClaudeLink = true
			claudeDirSet = true
			skillFlagsSeen = true
		case strings.HasPrefix(arg, "--kiro-dir="):
			parsed.kiroDir = strings.TrimSpace(strings.TrimPrefix(arg, "--kiro-dir="))
			parsed.enableKiroLink = true
			kiroDirSet = true
			skillFlagsSeen = true
		case strings.HasPrefix(arg, "--openclaw-dir="):
			parsed.openClawDir = strings.TrimSpace(strings.TrimPrefix(arg, "--openclaw-dir="))
			parsed.enableOpenClawLink = true
			openClawDirSet = true
			skillFlagsSeen = true
		case arg == "--kiro-dir":
			value, err := parseShimPathArg(args, &i, "--kiro-dir")
			if err != nil {
				return nil, err
			}
			parsed.kiroDir = value
			parsed.enableKiroLink = true
			kiroDirSet = true
			skillFlagsSeen = true
		case arg == "--openclaw-dir":
			value, err := parseShimPathArg(args, &i, "--openclaw-dir")
			if err != nil {
				return nil, err
			}
			parsed.openClawDir = value
			parsed.enableOpenClawLink = true
			openClawDirSet = true
			skillFlagsSeen = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			if parsed.server != "" {
				return nil, fmt.Errorf("unexpected positional argument: %s", arg)
			}
			parsed.server = strings.TrimSpace(arg)
		}
	}

	if parsed.help {
		return parsed, nil
	}
	if parsed.server == "" {
		return nil, fmt.Errorf("missing server (usage: mcpx shim install <server>)")
	}
	if parsed.skillStrict && !parsed.installSkill {
		return nil, fmt.Errorf("--skill-strict requires --skill")
	}
	if skillFlagsSeen && !parsed.installSkill {
		return nil, fmt.Errorf("skill install flags require --skill")
	}
	if parsed.installSkill {
		if parsed.dataAgentDir == "" {
			parsed.dataAgentDir = skill.DefaultDataAgentDir()
		}
		if parsed.enableClaudeLink && parsed.claudeDir == "" {
			parsed.claudeDir = skill.DefaultClaudeDir()
		}
		if parsed.enableKiroLink && parsed.kiroDir == "" {
			parsed.kiroDir = skill.DefaultKiroDir()
		}
		if parsed.enableOpenClawLink && parsed.openClawDir == "" {
			parsed.openClawDir = skill.DefaultOpenClawDir()
		}
		if parsed.openClawDir == "" {
			parsed.openClawDir = skill.DefaultOpenClawDir()
		}
	}
	if claudeDirSet && parsed.claudeDir == "" {
		parsed.claudeDir = skill.DefaultClaudeDir()
	}
	if kiroDirSet && parsed.kiroDir == "" {
		parsed.kiroDir = skill.DefaultKiroDir()
	}
	if openClawDirSet && parsed.openClawDir == "" {
		parsed.openClawDir = skill.DefaultOpenClawDir()
	}
	return parsed, nil
}

func parseShimRemoveArgs(args []string) (*shimRemoveArgs, error) {
	parsed := &shimRemoveArgs{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case strings.HasPrefix(arg, "--dir="):
			parsed.dir = strings.TrimSpace(strings.TrimPrefix(arg, "--dir="))
		case arg == "--dir":
			value, err := parseShimPathArg(args, &i, "--dir")
			if err != nil {
				return nil, err
			}
			parsed.dir = value
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			if parsed.server != "" {
				return nil, fmt.Errorf("unexpected positional argument: %s", arg)
			}
			parsed.server = strings.TrimSpace(arg)
		}
	}

	if parsed.help {
		return parsed, nil
	}
	if parsed.server == "" {
		return nil, fmt.Errorf("missing server (usage: mcpx shim remove <server>)")
	}
	return parsed, nil
}

func parseShimListArgs(args []string) (*shimListArgs, error) {
	parsed := &shimListArgs{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case strings.HasPrefix(arg, "--dir="):
			parsed.dir = strings.TrimSpace(strings.TrimPrefix(arg, "--dir="))
		case arg == "--dir":
			value, err := parseShimPathArg(args, &i, "--dir")
			if err != nil {
				return nil, err
			}
			parsed.dir = value
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			return nil, fmt.Errorf("unexpected positional argument: %s", arg)
		}
	}

	return parsed, nil
}

func parseShimPathArg(args []string, idx *int, flag string) (string, error) {
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

func printShimHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx shim install <server> [--dir <path>]")
	fmt.Fprintln(out, "  mcpx shim remove <server> [--dir <path>]")
	fmt.Fprintln(out, "  mcpx shim list [--dir <path>]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  install    Create a command shim that forwards to `mcpx <server> ...`.")
	fmt.Fprintln(out, "  remove     Delete an installed shim.")
	fmt.Fprintln(out, "  list       List installed mcpx-managed shims.")
	fmt.Fprintln(out, "")
	printShimInstallHelp(out)
	fmt.Fprintln(out, "")
	printShimRemoveHelp(out)
	fmt.Fprintln(out, "")
	printShimListHelp(out)
}

func printShimInstallHelp(out io.Writer) {
	fmt.Fprintln(out, "Install flags:")
	fmt.Fprintf(out, "  --dir <path>  Install directory (default: %s)\n", shim.DefaultDir())
	fmt.Fprintln(out, "  --skill                 Also install a generated server skill after shim install succeeds.")
	fmt.Fprintln(out, "  --skill-strict          Fail if server skill installation fails (requires --skill).")
	fmt.Fprintf(out, "  --data-agent-dir <path> Skill root (default: %s, requires --skill)\n", skill.DefaultDataAgentDir())
	fmt.Fprintf(out, "  --claude-dir <path>     Claude skill link root (default: %s, requires --skill; implies --claude-link)\n", skill.DefaultClaudeDir())
	fmt.Fprintf(out, "  --kiro-dir <path>       Kiro skill link root (default: %s, requires --skill; implies --kiro-link)\n", skill.DefaultKiroDir())
	fmt.Fprintf(out, "  --openclaw-dir <path>   OpenClaw skill link root (default: %s, requires --skill; implies --openclaw-link)\n", skill.DefaultOpenClawDir())
	fmt.Fprintln(out, "  --claude-link           Also create ~/.claude/skills/<generated-name> symlink (requires --skill).")
	fmt.Fprintln(out, "  --kiro-link             Also create ~/.kiro/skills/<generated-name> symlink (requires --skill).")
	fmt.Fprintln(out, "  --openclaw-link         Also create ~/.openclaw/skills/<generated-name> symlink (requires --skill).")
	fmt.Fprintln(out, "  --help, -h    Show install help.")
}

func printShimRemoveHelp(out io.Writer) {
	fmt.Fprintln(out, "Remove flags:")
	fmt.Fprintf(out, "  --dir <path>  Install directory (default: %s)\n", shim.DefaultDir())
	fmt.Fprintln(out, "  --help, -h    Show remove help.")
}

func printShimListHelp(out io.Writer) {
	fmt.Fprintln(out, "List flags:")
	fmt.Fprintf(out, "  --dir <path>  Install directory (default: %s)\n", shim.DefaultDir())
	fmt.Fprintln(out, "  --help, -h    Show list help.")
}

func shimServerKnown(server string, cfg *config.Config) (bool, error) {
	if cfg == nil {
		return true, nil
	}
	if _, ok := cfg.Servers[server]; ok {
		return true, nil
	}

	known, err := shimKnownServersFn()
	if err != nil {
		// Degrade gracefully if server discovery is unavailable; install still works
		// as a pure pass-through wrapper.
		return true, nil
	}
	return containsServerName(known, server), nil
}

func listShimKnownServers() ([]string, error) {
	nonce, err := spawnOrConnectFn()
	if err != nil {
		return nil, err
	}
	client := newDaemonClient(ipc.SocketPath(), nonce)
	resp, err := client.Send(&ipc.Request{
		Type: "list_servers",
		CWD:  callerWorkingDirectory(),
	})
	if err != nil {
		return nil, err
	}
	if resp.ExitCode != ipc.ExitOK {
		if resp.Stderr != "" {
			return nil, errors.New(resp.Stderr)
		}
		return nil, fmt.Errorf("listing servers failed (exit %d)", resp.ExitCode)
	}
	return decodeServerListPayload(resp.Content), nil
}
