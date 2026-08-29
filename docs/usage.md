# mcpx Usage

`mcpx` turns MCP servers into composable CLI commands so you can discover tools, inspect schemas, and call them with standard shell composition.

## Build

```bash
go build ./...
```

Project shortcuts:

```bash
make check   # test + vet + build
make qa-core # Go gates + core smoke/integration matrix
make qa-extended # CLI contract + wrapper packaging checks
make qa      # full QA matrix (core + extended)
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
mcpx <server> --catalog      # deterministic catalog with full schemas
mcpx <server> -v             # list tools (full descriptions)
mcpx <server> <tool> --help  # show schema-aware help
mcpx <server> <tool> --help --json  # raw schema payload JSON
mcpx <server> <tool> ...     # call tool
mcpx <source>                # if <source> is not a known server, resolve and run it ephemerally
mcpx <source> <tool> ...     # call tools from an ephemeral source (daemon-lifetime only)
mcpx add <source>            # add server config from install link/manifest/endpoint URL
mcpx import                  # list external MCP source adapters
mcpx import <source>         # preview importable servers without writing config
mcpx import <source> <name>  # import selected servers into managed config
mcpx auth login <server>     # authorize an OAuth-enabled HTTP server
mcpx auth status <server>    # show redacted credential status
mcpx auth logout <server>    # remove the stored OAuth session
mcpx doctor [server]         # diagnose config, transport, protocol, and auth source
mcpx shim install <server>   # install a passthrough command shim for one server
mcpx shim remove <server>    # remove an installed shim
mcpx shim list               # list installed mcpx-managed shims
mcpx skill install           # install built-in mcpx skill for agents
mcpx skill install <server>  # generate/install a skill for one server
```

Tool names are used exactly as exposed by the server.
Flag conventions can vary by tool and server, so run `mcpx <server> <tool> --help` before first use.

Ephemeral source mode reuses the same source parsing as `mcpx add` (install links, manifests, direct MCP endpoints) but does not write to `config.toml`.

`--json` is only for mcpx-owned outputs (`mcpx`, `mcpx import`, `mcpx <server>`, and `mcpx <server> <tool> --help`). Tool call output is not transformed.

`mcpx` server listing shows names by default. Add `-v` to include per-server origin metadata.

- `mcpx -v`: `name<TAB>kind`
- `mcpx --json`: `["name", ...]`
- `mcpx --json -v`: `[{ "name": "...", "origin": { "kind": "...", "path": "..." } }, ...]`

Examples:

```bash
mcpx github search-repositories --query=mcp
mcpx github search-repositories '{"query":"mcp"}'
echo '{"query":"mcp"}' | mcpx github search-repositories
```

When a schema uses composition, references, or other shapes that do not map
unambiguously to flags, mcpx requires positional or stdin JSON and says so in
`--help`. External `$ref` values are never fetched.

## Multi-Round-Trip Calls

Non-interactive calls never block for server-requested input. They exit 1 and
print the MCP `input_required` result as JSON. An agent can fulfill it with:

```bash
mcpx <server> <tool> '{}' \
  --request-state '<opaque requestState>' \
  --input-responses '{"confirm":{"action":"accept","content":{"approved":true}}}'
```

For a human at a terminal, `--interactive` prompts for elicitation responses.
Sampling and roots requests stay machine-driven because mcpx is not an agent
host.

## Remote OAuth

```bash
mcpx add https://example.com/mcp --name example --oauth
mcpx auth login example
mcpx auth status example
mcpx doctor example --json
mcpx auth logout example
```

If the client has a public HTTPS Client ID Metadata Document, configure it at
add time. This implies `--oauth`; dynamic registration remains the fallback
when the authorization server does not advertise metadata-document support:

```bash
mcpx add https://example.com/mcp --name example \
  --oauth-client-metadata-url https://client.example.com/mcpx.json
```

The browser flow uses PKCE and the OS credential store. Login prints the
authorization URL for you to open; mcpx does not launch remote authorization
URLs automatically. Explicit Authorization headers and credentials imported
from another host retain precedence and are never copied into mcpx's OAuth
store.

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

## Add Servers (`mcpx add`)

Bootstrap server config entries into `~/.config/mcpx/config.toml` from:

- install-link URLs (for example Cursor-style `.../mcp/install?name=...&config=...`)
- manifest URLs (`https://...`)
- direct MCP endpoint URLs (`https://.../mcp`)
- local manifest files (`.json` or `.toml`)

`mcpx add` accepts common MCP config dialects in manifests:

- `transport` as string, object, or array
- `type` as a transport alias
- HTTP headers via `headers` or `requestInit.headers`
- stdio commands as either `command` string + `args` array or `command` array

Examples:

```bash
mcpx add "cursor://anysphere.cursor-deeplink/mcp/install?name=postgres&config=..."
mcpx add https://example.com/mcp-manifest.json
mcpx add https://mcp.deepwiki.com/mcp
mcpx add https://mcp.devin.ai/mcp --name deepwiki --header "Authorization=Bearer ${DEEPWIKI_API_KEY}"
mcpx add ./mcp-manifest.toml
mcpx add ./mcp-manifest.json --name github-enterprise
mcpx add ./mcp-manifest.json --overwrite
```

Notes:

- `mcpx add` writes only to mcpx config; it does not install runtimes/packages.
- Existing entries require explicit `--overwrite`.
- `--header KEY=VALUE` can be repeated and is applied only to URL-based servers.
- `--oauth-client-metadata-url URL` enables OAuth and requires a non-root HTTPS URL.

## Import Servers (`mcpx import`)

Import adapters promote servers from other MCP clients into managed mcpx
config. Supported sources are `claude`, `cline`, `codex`, `cursor`, and `kiro`.

```bash
mcpx import                         # list source adapters
mcpx import claude                  # redacted preview
mcpx import claude filesystem       # import selected enabled servers
mcpx import cursor --all            # import all supported enabled servers
mcpx import cursor --refresh        # refresh prior Cursor imports
mcpx import codex                   # adapter preview includes enabled plugins
```

- Preview is read-only. `--json` emits names, transport kinds, statuses, and
  safe details without command, environment, or header values.
- Existing managed names are skipped by `--all` and rejected for explicit
  selections unless `--overwrite` is supplied.
- Import provenance is generic. `--refresh` dispatches through the original
  source adapter and source context, preserving project-scoped resolution plus
  mcpx cache and OAuth policy while updating source-owned transport fields.
- Relative stdio working directories are normalized against their manifest.
- File-backed adapters prefer the nearest project manifest before user-level
  configuration; the import context makes that precedence stable on refresh.
- Codex imports use `codex mcp list --json`, so enabled plugin MCPs participate
  without putting Codex on the daemon request path.
- Codex keyring OAuth tokens are not exported. Add `--oauth` when importing an
  HTTP server that mcpx should authorize, then run `mcpx auth login <server>`.

## Command Shims (`mcpx shim`)

Create optional convenience wrappers that forward directly to `mcpx <server> ...`.

Examples:

```bash
mcpx shim install github
mcpx shim install github --skill
mcpx shim install github --skill --skill-strict
mcpx shim install linear --dir ~/.local/bin
mcpx shim list
mcpx shim remove github
```

Behavior:

- Shims are pass-through wrappers only; they do not install MCP servers or runtimes.
- Default install directory is `$XDG_BIN_HOME` (if set) or `~/.local/bin`.
- Installs are collision-safe: `mcpx shim install <server>` fails if that command already resolves elsewhere in `PATH`.
- `mcpx shim remove <server>` removes only mcpx-managed shim files.
- `mcpx shim install <server> --skill` also installs a generated server skill after shim install succeeds.
- Add `--skill-strict` to fail if the generated skill cannot be installed.

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

By default this writes `SKILL.md` under `~/.agents/skills/mcpx`.

Optional flags:

```bash
mcpx skill install --claude-link
mcpx skill install --kiro-link
mcpx skill install --openclaw-link
mcpx skill install --guidance
mcpx skill install --guidance --claude-link
mcpx skill install --guidance --kiro-link
mcpx skill install --guidance --openclaw-link
mcpx skill install --guidance --guidance-text "Prefer mcpx when MCP work benefits from CLI composition."
mcpx skill install --guidance-file /custom/AGENTS.md
mcpx skill install --data-agent-dir /custom/agents/skills --claude-dir /custom/.claude/skills
mcpx skill install --kiro-dir /custom/.kiro/skills --openclaw-dir /custom/.openclaw/skills
```

Generate and install a skill for one configured server:

```bash
mcpx skill install github
mcpx skill install github --openclaw-link
mcpx skill install github --data-agent-dir /custom/agents/skills --claude-dir /custom/.claude/skills
```

Generated server skills are installed under `~/.agents/skills/mcpx-<server>` by default.

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
- No fallback or imported servers discovered:
  - Bootstrap fallbacks are read only when mcpx has no managed servers. Use
    `mcpx import` once you want an explicit mixed managed catalog.
  - By default, mcpx checks:
    - `~/.cursor/mcp.json`
    - Claude Desktop config
    - Cline MCP settings
    - Claude Code user/local config (`~/.claude.json`)
    - Codex config (`~/.codex/config.toml`, `mcp_servers.*`; when `[features].apps = true`, explicitly named virtual servers like `linear`/`zillow` resolve through Codex connector auth from `CODEX_CONNECTORS_TOKEN` or `~/.codex/auth.json`)
    - Kiro user config (`~/.kiro/settings/mcp.json`)
  - Project-local `.mcp.json` and `.kiro/settings/mcp.json` are not auto-discovered from the working directory.
  - Bare listing and shell completion omit Codex virtual servers because discovering them would connect to the Codex apps backend. Named calls such as `mcpx linear` still resolve them lazily.
  - Check fallback files exist and expose either `mcpServers` (JSON sources) or `mcp_servers` (Codex TOML). Claude Code local scope uses `projects[<path>].mcpServers`.
