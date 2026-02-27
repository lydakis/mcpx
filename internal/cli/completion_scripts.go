package cli

var completionScripts = map[string]string{
	"bash": bashCompletionScript,
	"zsh":  zshCompletionScript,
	"fish": fishCompletionScript,
}

const bashCompletionScript = `# bash completion for mcpx
_mcpx_has_add_server() {
  local server
  while IFS= read -r server; do
    if [[ "$server" == "add" ]]; then
      return 0
    fi
  done < <(mcpx __complete servers 2>/dev/null)
  return 1
}

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
    if ! _mcpx_has_add_server; then
      words="$words"$'\n'"add"
    fi
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

  if [[ "$first" == "add" ]] && ! _mcpx_has_add_server; then
    COMPREPLY=( $(compgen -W "--name --overwrite --help -h" -- "$cur") )
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
_mcpx_has_add_server() {
  local server
  for server in ${(f)"$(mcpx __complete servers 2>/dev/null)"}; do
    if [[ "$server" == "add" ]]; then
      return 0
    fi
  done
  return 1
}

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
    if ! _mcpx_has_add_server; then
      servers+=(add)
    fi
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

  if [[ "${words[2]}" == "add" ]] && ! _mcpx_has_add_server; then
    flags=(--name --overwrite --help -h)
    _describe 'add flag' flags
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

function __mcpx_has_add_server
    for s in (mcpx __complete servers 2>/dev/null)
        if test "$s" = add
            return 0
        end
    end
    return 1
end

complete -c mcpx -n 'test (count (__mcpx_words)) -eq 1' -a "completion --help -h --version -V --json (mcpx __complete servers 2>/dev/null)"
complete -c mcpx -n 'test (count (__mcpx_words)) -eq 1; and not __mcpx_has_add_server' -a "add"
complete -c mcpx -n 'test (count (__mcpx_words)) -eq 1; and not __mcpx_has_skill_server' -a "skill"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" = completion' -a "bash zsh fish"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -ge 2; and test "$w[2]" = add; and not __mcpx_has_add_server' -a "--name --overwrite --help -h"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" = skill; and not __mcpx_has_skill_server' -a "install"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -ge 3; and test "$w[2]" = skill; and not __mcpx_has_skill_server' -a "--data-agent-dir --claude-dir --no-claude-link --codex-dir --codex-link --kiro-dir --kiro-link --help -h"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -eq 2; and test "$w[2]" != completion; and begin; test "$w[2]" != add; or __mcpx_has_add_server; end; and begin; test "$w[2]" != skill; or __mcpx_has_skill_server; end' -a "(mcpx __complete tools (__mcpx_server) 2>/dev/null)"
complete -c mcpx -n 'set -l w (__mcpx_words); test (count $w) -ge 3; and test "$w[2]" != completion; and begin; test "$w[2]" != add; or __mcpx_has_add_server; end; and begin; test "$w[2]" != skill; or __mcpx_has_skill_server; end' -a "(mcpx __complete flags (__mcpx_server) (__mcpx_tool) 2>/dev/null)"
`
