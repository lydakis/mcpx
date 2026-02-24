package cli

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

var (
	rootStdout   io.Writer = os.Stdout
	rootStderr   io.Writer = os.Stderr
	buildVersion           = "dev"
)

func init() {
	buildVersion = resolveBuildVersion(buildVersion)
}

func handleRootFlags(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}

	if len(args) != 1 {
		return false, 0
	}

	switch args[0] {
	case "--version", "-V":
		fmt.Fprintf(rootStdout, "mcpx %s\n", buildVersion)
		return true, 0
	case "--help", "-h":
		printRootHelp(rootStdout)
		return true, 0
	default:
		return false, 0
	}
}

func resolveBuildVersion(defaultVersion string) string {
	if defaultVersion != "" && defaultVersion != "dev" {
		return defaultVersion
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return defaultVersion
	}
	if info.Main.Version == "" || info.Main.Version == "(devel)" {
		return defaultVersion
	}
	return info.Main.Version
}

func printRootHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx")
	fmt.Fprintln(out, "  mcpx <server>")
	fmt.Fprintln(out, "  mcpx <server> <tool> [FLAGS]")
	fmt.Fprintln(out, "  mcpx completion <bash|zsh|fish>")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Global flags:")
	fmt.Fprintln(out, "  --help, -h       Show help")
	fmt.Fprintln(out, "  --version, -V    Show version")
}
