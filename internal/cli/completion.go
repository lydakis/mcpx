package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/daemon"
	"github.com/lydakis/mcpx/internal/ipc"
)

var (
	completionScripts = map[string]string{
		"bash": bashCompletionScript,
		"zsh":  zshCompletionScript,
		"fish": fishCompletionScript,
	}
	globalCallFlags = []string{
		"--cache",
		"--no-cache",
		"--verbose",
		"-v",
		"--quiet",
		"-q",
		"--json",
		"--help",
		"-h",
	}
	reservedToolFlagNames = map[string]struct{}{
		"cache":    {},
		"no-cache": {},
		"verbose":  {},
		"quiet":    {},
		"json":     {},
		"help":     {},
		"version":  {},
	}
)

const bashCompletionScript = `# bash completion for mcpx
_mcpx_has_skill_server() {
  local server
  while IFS= read -r server; do
    if [[ "$server" == "skill" ]]; then
      return 0
    fi
  done < <(mcpx __complete servers 2>/dev/null)
  return 1
}

_mcpx_completion() {
  local cur first tool
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"

  if [[ ${COMP_CWORD} -eq 1 ]]; then
    local words
    words="$(mcpx __complete servers 2>/dev/null)"
    words="$words"$'\n'"completion"$'\n'"--help"$'\n'"-h"$'\n'"--version"$'\n'"-V"$'\n'"--json"
    if ! _mcpx_has_skill_server; then
      words="$words"$'\n'"skill"
    fi
    COMPREPLY=( $(compgen -W "$words" -- "$cur") )
    return 0
  fi

  first="${COMP_WORDS[1]}"
  if [[ "$first" == "completion" ]]; then
    COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
    return 0
  fi

  if [[ "$first" == "skill" ]] && ! _mcpx_has_skill_server; then
    if [[ ${COMP_CWORD} -eq 2 ]]; then
      COMPREPLY=( $(compgen -W "install" -- "$cur") )
      return 0
    fi
    COMPREPLY=( $(compgen -W "--data-agent-dir --claude-dir --no-claude-link --codex-dir --codex-link --kiro-dir --kiro-link --help -h" -- "$cur") )
    return 0
  fi

  if [[ ${COMP_CWORD} -eq 2 ]]; then
    local tools
    tools="$(mcpx __complete tools "$first" 2>/dev/null)"
    COMPREPLY=( $(compgen -W "$tools" -- "$cur") )
    return 0
  fi

  tool="${COMP_WORDS[2]}"
  local flags
  flags="$(mcpx __complete flags "$first" "$tool" 2>/dev/null)"
  COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
}
complete -F _mcpx_completion mcpx
`

const zshCompletionScript = `#compdef mcpx
_mcpx_has_skill_server() {
  local server
  for server in ${(f)"$(mcpx __complete servers 2>/dev/null)"}; do
    if [[ "$server" == "skill" ]]; then
      return 0
    fi
  done
  return 1
}

_mcpx_completion() {
  local -a servers tools flags

  if (( CURRENT == 2 )); then
    servers=(${(f)"$(mcpx __complete servers 2>/dev/null)"})
    servers+=(completion --help -h --version -V --json)
    if ! _mcpx_has_skill_server; then
      servers+=(skill)
    fi
    _describe 'mcpx entry' servers
    return
  fi

  if [[ "${words[2]}" == "completion" ]]; then
    _values 'shell' bash zsh fish
    return
  fi

  if [[ "${words[2]}" == "skill" ]] && ! _mcpx_has_skill_server; then
    if (( CURRENT == 3 )); then
      _values 'skill command' install
      return
    fi
    flags=(--data-agent-dir --claude-dir --no-claude-link --codex-dir --codex-link --kiro-dir --kiro-link --help -h)
    _describe 'skill flag' flags
    return
  fi

  if (( CURRENT == 3 )); then
    tools=(${(f)"$(mcpx __complete tools ${words[2]} 2>/dev/null)"})
    _describe 'tool' tools
    return
  fi

  flags=(${(f)"$(mcpx __complete flags ${words[2]} ${words[3]} 2>/dev/null)"})
  _describe 'flag' flags
}
compdef _mcpx_completion mcpx
`

const fishCompletionScript = `function __mcpx_words
    commandline -opc
end

function __mcpx_server
    set -l w (__mcpx_words)
    if test (count $w) -ge 2
        echo $w[2]
    end
end

function __mcpx_tool
    set -l w (__mcpx_words)
    if test (count $w) -ge 3
        echo $w[3]
    end
end

function __mcpx_has_skill_server
    for s in (mcpx __complete servers 2>/dev/null)
        if test "$s" = skill
            return 0
        end
    end
    return 1
end

complete -c mcpx -n 'test (count (__mcpx_words)) -eq 1' -a "completion --help -h --version -V --json (mcpx __complete servers 2>/dev/null)"
complete -c mcpx -n 'test (count (__mcpx_words)) -eq 1; and not __mcpx_has_skill_server' -a "skill"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" = completion' -a "bash zsh fish"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" = skill; and not __mcpx_has_skill_server' -a "install"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -ge 3; and test "$w[2]" = skill; and not __mcpx_has_skill_server' -a "--data-agent-dir --claude-dir --no-claude-link --codex-dir --codex-link --kiro-dir --kiro-link --help -h"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" != completion; and begin; test "$w[2]" != skill; or __mcpx_has_skill_server; end' -a "(mcpx __complete tools (__mcpx_server) 2>/dev/null)"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -ge 3; and test "$w[2]" != completion; and begin; test "$w[2]" != skill; or __mcpx_has_skill_server; end' -a "(mcpx __complete flags (__mcpx_server) (__mcpx_tool) 2>/dev/null)"
`

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

func completeServers(stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}

	for _, name := range configuredServerNames(cfg) {
		fmt.Fprintln(stdout, name)
	}
	return ipc.ExitOK
}

func completeTools(server string, stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}
	if _, ok := cfg.Servers[server]; !ok {
		return ipc.ExitUsageErr
	}

	client, code := completionClient(stderr)
	if code != ipc.ExitOK {
		return code
	}

	resp, err := client.Send(&ipc.Request{
		Type:   "list_tools",
		Server: server,
		CWD:    callerWorkingDirectory(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
		return resp.ExitCode
	}

	entries, err := decodeToolListPayload(resp.Content)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	tools := toolListNames(entries)
	for _, tool := range tools {
		fmt.Fprintln(stdout, tool)
	}
	return ipc.ExitOK
}

func completeFlags(server, tool string, stdout, stderr io.Writer) int {
	cfg, code := loadConfigWithFallback(stderr)
	if code != ipc.ExitOK {
		return code
	}
	if _, ok := cfg.Servers[server]; !ok {
		return ipc.ExitUsageErr
	}

	client, code := completionClient(stderr)
	if code != ipc.ExitOK {
		return code
	}

	resp, err := client.Send(&ipc.Request{
		Type:   "tool_schema",
		Server: server,
		Tool:   tool,
		CWD:    callerWorkingDirectory(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return ipc.ExitInternal
	}
	if resp.Stderr != "" {
		fmt.Fprintln(stderr, resp.Stderr)
		return resp.ExitCode
	}

	_, _, inputSchema, _ := parseToolHelpPayload(resp.Content)
	for _, flag := range toolFlagCompletions(inputSchema) {
		fmt.Fprintln(stdout, flag)
	}
	return ipc.ExitOK
}

func loadConfigWithFallback(stderr io.Writer) (*config.Config, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return nil, ipc.ExitInternal
	}
	if ferr := config.MergeFallbackServers(cfg); ferr != nil {
		fmt.Fprintf(stderr, "mcpx: warning: failed to load fallback MCP server config: %v\n", ferr)
	}
	return cfg, ipc.ExitOK
}

func completionClient(stderr io.Writer) (*ipc.Client, int) {
	nonce, err := daemon.SpawnOrConnect()
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: %v\n", err)
		return nil, ipc.ExitInternal
	}
	return ipc.NewClient(ipc.SocketPath(), nonce), ipc.ExitOK
}

func configuredServerNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func toolFlagCompletions(inputSchema map[string]any) []string {
	flags := append([]string{}, globalCallFlags...)

	props, _ := inputSchema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		typ, _ := prop["type"].(string)
		base, neg := toolFlagNames(name, typ)

		flags = append(flags, base)
		if neg != "" {
			flags = append(flags, neg)
		}
	}

	return uniqueSorted(flags)
}

func isReservedToolFlagName(name string) bool {
	_, ok := reservedToolFlagNames[name]
	return ok
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
