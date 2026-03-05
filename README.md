# mcpx

Turn MCP servers into composable CLIs.

```bash
mcpx                        # list servers
mcpx <server>               # list tools
mcpx <server> <tool> ...    # call a tool
```

Tool names match exactly what each server exposes. Tool-call output passes through unchanged (text or JSON), so you can pipe, redirect, or parse with `jq`.

## Quick Start

```bash
brew tap lydakis/mcpx
brew install --cask mcpx
```

Install the general `mcpx` skill for your agent (recommended on day one):

```bash
mcpx skill install
```

Add extra links as needed:

```bash
mcpx skill install --codex-link
mcpx skill install --openclaw-link
```

If you already use MCP in Cursor, Claude Code, Cline, Codex, or Kiro, `mcpx` auto-discovers those server configs.

```bash
mcpx github search-repositories --query=mcp | jq -r '.items[:3][].full_name'
```

No existing configs? Point `mcpx` at any MCP endpoint and start calling tools immediately:

```bash
mcpx https://docs.mcp.cloudflare.com/mcp
mcpx https://docs.mcp.cloudflare.com/mcp search_cloudflare_documentation --query="durable objects alarms"
```

Every tool gets schema-aware `--help` for free:

```bash
mcpx https://docs.mcp.cloudflare.com/mcp search_cloudflare_documentation --help
```

## Going Deeper

### Adding Servers

`mcpx add` bootstraps config from install links, manifest URLs, direct MCP endpoints, or local manifest files:

```bash
mcpx add https://mcp.deepwiki.com/mcp
mcpx deepwiki read_wiki_structure --repoName=modelcontextprotocol/specification
```

Added servers persist in `~/.config/mcpx/config.toml`. You can also write entries by hand:

```toml
[servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }
default_cache_ttl = "30s"
```

### Ephemeral Sources

Any source you pass directly (without `mcpx add`) runs ephemerally for the daemon's lifetime: no config written, nothing to clean up.

```bash
mcpx <source>
mcpx <source> <tool> --help
mcpx <source> <tool> ...
```

### Caching

Cache tool responses with `--cache=<duration>`, or force fresh calls with `--no-cache`:

```bash
mcpx deepwiki read_wiki_structure --repoName=modelcontextprotocol/specification --cache=5m
mcpx deepwiki read_wiki_structure --repoName=modelcontextprotocol/specification --no-cache
```

Set per-server defaults with `default_cache_ttl` in config.

### Command Shims

Install a local passthrough so `<server>` works as a standalone command:

```bash
mcpx shim install github
github search-repositories --query=mcp | jq -r '.items[:3][].full_name'
```

Shims land in `$XDG_BIN_HOME` or `~/.local/bin`. Install is collision-safe: it fails if that name already resolves elsewhere in `PATH`.

```bash
mcpx shim install github --skill   # also generate a server skill
mcpx shim list
mcpx shim remove github
```

### Server-Specific Skills (Optional)

When you want tighter, server-specific instructions, generate a skill file for one server (written to `~/.agents/skills/mcpx-<server>` by default):

```bash
mcpx skill install github
mcpx skill install github --codex-link
mcpx skill install github --openclaw-link
```

### Codex Apps

When Codex Apps are enabled and authenticated locally, `mcpx` exposes connected apps as regular servers:

```bash
mcpx linear
mcpx linear <tool> --help
mcpx linear <tool> ...
```

Auth stays with Codex. `mcpx` does not run OAuth flows or store third-party credentials.

## Reference

### Other Install Methods

**npm:**

```bash
npm install -g mcpx-go
```

**PyPI:**

```bash
pip install mcpx-go
```

**Source:**

```bash
go build ./...
./mcpx --version
```

Windows: use WSL2 and run install commands inside your Linux distro shell.

### Commands

| Command | Purpose |
|---------|---------|
| `mcpx add <source>` | Bootstrap a server config from a source |
| `mcpx shim install <server>` | Install a local passthrough shim |
| `mcpx shim remove <server>` | Remove a shim |
| `mcpx shim list` | List installed shims |
| `mcpx completion <shell>` | Print shell completions (bash/zsh/fish) |
| `mcpx skill install [<server>]` | Install built-in or server-specific skill |

`mcpx add` accepts `--name`, `--header KEY=VALUE`, and `--overwrite`. `mcpx shim install` accepts `--skill` and `--skill-strict`.

### Output Modes

`--json` applies to mcpx-owned surfaces only (`mcpx`, `mcpx <server>`, `mcpx <server> <tool> --help`). Tool-call output passes through unmodified.

Use `-v` to include per-server origin metadata. Combine with `--json` for machine-readable output including config paths.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Tool error (MCP `isError`) |
| 2 | Usage error |
| 3 | Internal error |

### MCP Smoke Tests

Validate any server quickly:

```bash
mcpx <server>                      # list tools
mcpx <server> --json               # machine-readable
mcpx <server> -v                   # full descriptions
mcpx <server> <tool> --help        # inspect schema
mcpx <server> <tool> --help --json
echo $?                            # check exit code
```

### More Examples

```bash
mcpx --json
mcpx github --json
mcpx github -v
mcpx github search-repositories --help --json
mcpx add "cursor://anysphere.cursor-deeplink/mcp/install?name=postgres&config=..."
mcpx add https://mcp.deepwiki.com/mcp
mcpx https://mcp.deepwiki.com/mcp
mcpx https://mcp.deepwiki.com/mcp read_wiki_structure --repoName=modelcontextprotocol/specification
mcpx add https://mcp.devin.ai/mcp --name deepwiki --header "Authorization=Bearer \${DEEPWIKI_API_KEY}"
mcpx skill install
```

### Manual Config

If auto-discovery finds nothing, create `~/.config/mcpx/config.toml` directly. For fallback setups, include `-y` for npx:

```toml
[servers.browser-tools]
command = "npx"
args = ["-y", "@agentdeskai/browser-tools-mcp@1.1.0"]
```

## Development

### QA

```bash
make check        # test + vet + build
make qa-core      # Go gates + core smoke/integration matrix
make qa-extended  # CLI contract + wrapper packaging checks
make qa           # full QA matrix (core + extended)
```

### Benchmarks

Benchmarks are manual (not part of CI):

```bash
make perf
./scripts/perf_bench.sh <git-ref>          # compare against baseline
make perf-loop                              # warm CLI throughput (500 calls)
./scripts/perf_cli_loop.sh <git-ref>
```

For summarized comparisons: `go install golang.org/x/perf/cmd/benchstat@latest`

### Versioning

Local builds show `mcpx dev`. Tagged releases show the tag (for example `mcpx v0.1.0`) via GoReleaser ldflags.

### Release

Tag pushes matching `v*` trigger the release workflow. GoReleaser publishes artifacts and updates `lydakis/homebrew-mcpx`. Notarization uses standard Apple Developer and App Store Connect secrets.

## Docs

- [design](docs/design.md)
- [usage](docs/usage.md)
- [release](docs/release.md)
- [roadmap](docs/roadmap.md)

## License

[MIT](LICENSE)
