package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/lydakis/mcpx/internal/ipc"
)

func runCompletionCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "mcpx: usage: mcpx completion <bash|zsh|fish>")
		return ipc.ExitUsageErr
	}

	script, ok := completionScripts[strings.ToLower(args[0])]
	if !ok {
		fmt.Fprintf(stderr, "mcpx: unknown shell for completion: %s\n", args[0])
		return ipc.ExitUsageErr
	}

	_, _ = io.WriteString(stdout, script)
	return ipc.ExitOK
}

func runInternalCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "mcpx: usage: mcpx __complete <servers|tools|flags> ...")
		return ipc.ExitUsageErr
	}

	switch args[0] {
	case "servers":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "mcpx: usage: mcpx __complete servers")
			return ipc.ExitUsageErr
		}
		return completeServers(stdout, stderr)
	case "tools":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "mcpx: usage: mcpx __complete tools <server>")
			return ipc.ExitUsageErr
		}
		return completeTools(args[1], stdout, stderr)
	case "flags":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "mcpx: usage: mcpx __complete flags <server> <tool>")
			return ipc.ExitUsageErr
		}
		return completeFlags(args[1], args[2], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mcpx: unknown completion query: %s\n", args[0])
		return ipc.ExitUsageErr
	}
}
