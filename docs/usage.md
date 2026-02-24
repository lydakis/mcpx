# mcpx Usage

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
mcpx <server>                # list tools
mcpx <server> <tool> --help  # show schema-aware help
mcpx <server> <tool> ...     # call tool
```

Examples:

```bash
mcpx github search-repositories --query=mcp
mcpx github search-repositories '{"query":"mcp"}'
echo '{"query":"mcp"}' | mcpx github search-repositories
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

## Man Pages

`mcpx <server> <tool> --help` writes a man page under:

- `$XDG_DATA_HOME/man/man1` (default: `~/.local/share/man/man1`)

Example:

```bash
man mcpx-github-search-repositories
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
  - Check fallback files exist and contain a top-level `mcpServers` object.
