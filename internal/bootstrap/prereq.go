package bootstrap

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
)

type lookupPathFunc func(file string) (string, error)

func CheckPrerequisites(server config.ServerConfig) error {
	return checkPrerequisitesWithLookup(server, exec.LookPath)
}

func checkPrerequisitesWithLookup(server config.ServerConfig, lookup lookupPathFunc) error {
	if !server.IsStdio() {
		return nil
	}
	if lookup == nil {
		lookup = exec.LookPath
	}

	command := strings.TrimSpace(server.Command)
	if command == "" {
		return nil
	}
	if _, err := lookup(command); err != nil {
		return fmt.Errorf("required runtime %q not found in PATH", command)
	}

	if !isEnvCommand(command) {
		return nil
	}

	wrapped := envWrappedCommand(server.Args)
	if wrapped == "" {
		return nil
	}
	if _, err := lookup(wrapped); err != nil {
		return fmt.Errorf("required runtime %q not found in PATH", wrapped)
	}
	return nil
}

func isEnvCommand(command string) bool {
	return filepath.Base(strings.TrimSpace(command)) == "env"
}

func envWrappedCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "--" {
			return nextNonEmptyCommandToken(args[i+1:])
		}

		if token == "-S" || token == "--split-string" {
			if i+1 >= len(args) {
				return ""
			}
			i++
			if parsed := envWrappedSplitCommand(args[i]); parsed != "" {
				return parsed
			}
			continue
		}

		if strings.HasPrefix(token, "-S=") {
			if parsed := envWrappedSplitCommand(strings.TrimPrefix(token, "-S=")); parsed != "" {
				return parsed
			}
			continue
		}

		if strings.HasPrefix(token, "--split-string=") {
			if parsed := envWrappedSplitCommand(strings.TrimPrefix(token, "--split-string=")); parsed != "" {
				return parsed
			}
			continue
		}
		if envOptionConsumesNextArg(token) {
			if i+1 >= len(args) {
				return ""
			}
			i++
			continue
		}
		if strings.HasPrefix(token, "--unset=") || strings.HasPrefix(token, "--chdir=") {
			continue
		}

		if strings.HasPrefix(token, "-") {
			continue
		}

		// Treat KEY=value as environment assignment.
		if idx := strings.Index(token, "="); idx > 0 {
			continue
		}

		return trimBalancedQuotes(token)
	}
	return ""
}

func envOptionConsumesNextArg(token string) bool {
	switch token {
	case "-u", "--unset", "-C", "--chdir":
		return true
	default:
		return false
	}
}

func nextNonEmptyCommandToken(args []string) string {
	for _, raw := range args {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}

		token = trimBalancedQuotes(token)
		if idx := strings.Index(token, "="); idx > 0 {
			continue
		}
		return token
	}
	return ""
}

func envWrappedSplitCommand(raw string) string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return ""
	}
	return envWrappedCommand(fields)
}

func trimBalancedQuotes(token string) string {
	if len(token) < 2 {
		return token
	}
	start := token[0]
	end := token[len(token)-1]
	if (start == '\'' && end == '\'') || (start == '"' && end == '"') {
		return token[1 : len(token)-1]
	}
	return token
}
