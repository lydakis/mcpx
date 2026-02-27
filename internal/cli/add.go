package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lydakis/mcpx/internal/bootstrap"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/paths"
)

type addArgs struct {
	source    string
	name      string
	overwrite bool
	help      bool
}

func maybeHandleAddCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "add" {
		return false, 0
	}

	if cfg != nil {
		if _, ok := cfg.Servers["add"]; ok {
			return false, 0
		}
	}

	return true, runAddCommand(args[1:], stdout, stderr)
}

func runAddCommand(args []string, stdout, stderr io.Writer) int {
	parsed, err := parseAddArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		printAddHelp(stderr)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printAddHelp(stdout)
		return ipc.ExitOK
	}

	resolved, err := bootstrap.Resolve(context.Background(), parsed.source, bootstrap.ResolveOptions{
		Name: parsed.name,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: add: %v\n", err)
		return classifyResolveErrorExitCode(err)
	}

	cfgPath := paths.ConfigFile()
	cfg, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: add: loading config: %v\n", err)
		return ipc.ExitInternal
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]config.ServerConfig)
	}

	_, exists := cfg.Servers[resolved.Name]
	if exists && !parsed.overwrite {
		fmt.Fprintf(stderr, "mcpx: add: server %q already exists; rerun with --overwrite to replace it\n", resolved.Name)
		return ipc.ExitUsageErr
	}
	if err := bootstrap.CheckPrerequisites(config.ExpandServerForCurrentEnv(resolved.Server)); err != nil {
		fmt.Fprintf(stderr, "mcpx: add: %v\n", err)
		return ipc.ExitUsageErr
	}

	cfg.Servers[resolved.Name] = resolved.Server
	if err := config.ValidateForCurrentEnv(cfg); err != nil {
		fmt.Fprintf(stderr, "mcpx: add: invalid resulting config: %v\n", err)
		return ipc.ExitUsageErr
	}

	if err := config.SaveTo(cfgPath, cfg); err != nil {
		fmt.Fprintf(stderr, "mcpx: add: writing config: %v\n", err)
		return ipc.ExitInternal
	}

	verb := "Added"
	if exists {
		verb = "Updated"
	}
	fmt.Fprintf(stdout, "%s server %q in %s\n", verb, resolved.Name, cfgPath)
	return ipc.ExitOK
}

func classifyResolveErrorExitCode(err error) int {
	if bootstrap.IsSourceAccessError(err) {
		return ipc.ExitInternal
	}
	return ipc.ExitUsageErr
}

func parseAddArgs(args []string) (*addArgs, error) {
	parsed := &addArgs{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case arg == "--overwrite":
			parsed.overwrite = true
		case strings.HasPrefix(arg, "--name="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--name="))
			if value == "" {
				return nil, fmt.Errorf("missing value for --name")
			}
			parsed.name = value
		case arg == "--name":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for --name")
			}
			i++
			value := strings.TrimSpace(args[i])
			if value == "" || strings.HasPrefix(value, "-") {
				return nil, fmt.Errorf("missing value for --name")
			}
			parsed.name = value
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			if parsed.source != "" {
				return nil, fmt.Errorf("unexpected positional argument: %s", arg)
			}
			parsed.source = strings.TrimSpace(arg)
		}
	}

	if parsed.help {
		return parsed, nil
	}
	if parsed.source == "" {
		return nil, fmt.Errorf("missing source (usage: mcpx add <source>)")
	}

	return parsed, nil
}

func printAddHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx add <source> [--name <server>] [--overwrite]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Sources:")
	fmt.Fprintln(out, "  - install-link URL (for example cursor://.../mcp/install?... )")
	fmt.Fprintln(out, "  - manifest URL (http/https)")
	fmt.Fprintln(out, "  - local manifest file path (JSON or TOML)")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  --name <server>   Select or rename the server entry to add.")
	fmt.Fprintln(out, "  --overwrite       Replace existing server entry in mcpx config.")
	fmt.Fprintln(out, "  --help, -h        Show this help output.")
}
