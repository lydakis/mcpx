# mcpx Usage

`mcpx` converts MCP servers into CLI commands so you can discover tools, inspect schemas, and call them with standard shell composition.

## Build

```bash
go build ./...
```

Project shortcuts:

```bash
make check   # test + vet + build
make qa      # deterministic QA smoke + integration matrix
make dist    # cross-platform artifacts + SHA256SUMS
RUN_DIST=1 make qa  # QA matrix + distribution artifact build
```

## Configure

Create `~/.config/mcpx/config.toml`:

```toml
[servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }
default_cache_ttl = "30s"

[servers.github.tools.create_issue]
cache = false
```

For HTTP servers:

```toml
[servers.apify]
url = "https://mcp.apify.com"
headers = { Authorization = "Bearer ${APIFY_TOKEN}" }
```

## Core Commands

```bash
mcpx                         # list servers
mcpx --json                  # list servers as JSON
mcpx <server>                # list tools (short descriptions)
mcpx <server> --json         # list tools as JSON
mcpx <server> -v             # list tools (full descriptions)
mcpx <server> <tool> --help  # show schema-aware help
mcpx <server> <tool> --help --json  # raw schema payload JSON
mcpx <server> <tool> ...     # call tool
mcpx skill install           # install built-in mcpx skill for agents
```

Tool names are used exactly as exposed by the server.
Flag conventions can vary by tool and server, so run `mcpx <server> <tool> --help` before first use.

`--json` is only for mcpx-owned outputs (`mcpx`, `mcpx <server>`, and `mcpx <server> <tool> --help`). Tool call output is not transformed.

Examples:

```bash
mcpx github search-repositories --query=mcp
mcpx github search-repositories '{"query":"mcp"}'
echo '{"query":"mcp"}' | mcpx github search-repositories
```

Generic pipeline:

```bash
url="$(mcpx <server> <search-tool> --query='topic' --maxResults=5 | jq -r '.results[0].url')"
mcpx <server> <read-tool> --inputs="[\"$url\"]" | jq '.content'
```

## Caching

```bash
mcpx github search-repositories --query=mcp --cache=60s
mcpx github search-repositories --query=mcp --no-cache
mcpx github search-repositories --query=mcp --cache=60s -v
```

## Shell Completions

Generate and install:

```bash
mcpx completion bash > ~/.local/share/bash-completion/completions/mcpx
mcpx completion zsh > "${fpath[1]}/_mcpx"
mcpx completion fish > ~/.config/fish/completions/mcpx.fish
```

If your shell does not pick up completions immediately, restart the shell.

## Skill Install

Install the built-in `mcpx` skill:

```bash
mcpx skill install
```

By default this writes `SKILL.md` under `~/.agents/skills/mcpx`, then links into Claude Code at `~/.claude/skills/mcpx`.

Optional flags:

```bash
mcpx skill install --no-claude-link
mcpx skill install --codex-link
mcpx skill install --kiro-link
mcpx skill install --data-agent-dir /custom/agents/skills --claude-dir /custom/.claude/skills
mcpx skill install --codex-dir /custom/.codex/skills --kiro-dir /custom/.kiro/skills
```

## Man Pages

`mcpx` ships a root CLI man page (`mcpx.1`). Package installs place it in your manpath.
For manual installs from release archives, copy `man/man1/mcpx.1` into your local or system man directory, for example:

- `$XDG_DATA_HOME/man/man1` (default: `~/.local/share/man/man1`)

Example:

```bash
mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/man/man1"
cp man/man1/mcpx.1 "${XDG_DATA_HOME:-$HOME/.local/share}/man/man1/"
man mcpx
```

## Troubleshooting

- `mcpx: unknown server ...`
  - Verify `config.toml` server names and run `mcpx` to list known servers.
- `mcpx: invalid config ...`
  - Fix transport settings (`command` xor `url`), URL format, cache TTL, or glob patterns.
- `calling tool: ...`
  - Use `-v` to get cache diagnostics and confirm server-side credentials/env vars.
- No fallback servers discovered:
  - By default, mcpx checks:
    - `~/.cursor/mcp.json`
    - Claude Desktop config
    - Cline MCP settings
    - Claude Code user/local config (`~/.claude.json`)
    - Codex config (`~/.codex/config.toml`, `mcp_servers.*`)
    - Claude Code project config (`.mcp.json`, nearest parent)
    - Kiro user config (`~/.kiro/settings/mcp.json`)
    - Kiro project config (`.kiro/settings/mcp.json`, nearest parent)
  - Check fallback files exist and expose either `mcpServers` (JSON sources) or `mcp_servers` (Codex TOML). Claude Code local scope uses `projects[<path>].mcpServers`.
