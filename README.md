# mcpx

Turn MCP servers into composable CLIs.

`mcpx` turns MCP tools into shell commands so agents can use standard CLI composition (`|`, redirection, `jq`, `head`) and skill workflows.

`mcpx` keeps the command contract simple:

- `mcpx` lists servers
- `mcpx <server>` lists tools
- `mcpx <server> <tool>` calls a tool

Tool names are used exactly as exposed by each server (no client-side renaming/aliasing).

Utility commands:

- `mcpx add <source> [--name <server>] [--overwrite]` bootstraps a server config from an install link, manifest URL, or local manifest file
- `mcpx completion <bash|zsh|fish>` prints shell completion scripts
- `mcpx skill install` installs the built-in `mcpx` skill to `~/.agents/skills` and links it for Claude Code (optional flags also link for Codex/Kiro)

It is designed for agent workflows and shell composition:

- schema-aware `--help` (inputs + declared outputs)
- native flag surface from MCP `inputSchema`
- standardized exit mapping (`0/1/2/3`)
- optional response caching with TTL and config overrides
- optional Codex Apps compatibility via virtual per-app servers
- stdio + HTTP transports via a local daemon
- generated shell completions and packaged root man page (`man mcpx`)

## Install

### Homebrew

```bash
brew tap lydakis/mcpx
brew install --cask mcpx
```

### npm

```bash
npm install -g mcpx-go
mcpx --version
```

### PyPI

```bash
pip install mcpx-go
mcpx --version
```

### Build from source

```bash
go build ./...
./mcpx --version
```

Windows users: use WSL2 and run install commands inside your Linux distro shell.

## Quick Start

Create `~/.config/mcpx/config.toml`:

```toml
[servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }
default_cache_ttl = "30s"
```

Run:

```bash
mcpx
mcpx --json
mcpx github
mcpx github --json
mcpx github -v
mcpx github search-repositories --help
mcpx github search-repositories --help --json
mcpx github search-repositories --query=mcp
mcpx add "cursor://anysphere.cursor-deeplink/mcp/install?name=postgres&config=..."
mcpx skill install
```

## Codex Apps (Optional)

When Codex Apps are enabled in local Codex config, `mcpx` can expose connected apps as normal MCP servers (for example, `linear` or `zillow`) through the same command contract:

```bash
mcpx linear
mcpx linear <tool> --help
mcpx linear <tool> ...
```

Auth is still managed by Codex. `mcpx` does not run OAuth flows or store third-party app credentials.

`--json` applies only to mcpx-owned output surfaces:

- `mcpx`
- `mcpx <server>`
- `mcpx <server> <tool> --help`

Normal tool-call output (`mcpx <server> <tool> ...`) is not transformed by `--json`.

Use `mcpx -v` (or `mcpx --json -v`) to include per-server `origin` metadata (config/fallback-derived `kind`; JSON also includes optional `path`).

## MCP Smoke Test Commands

Use these to validate a local MCP quickly:

```bash
mcpx <server>
mcpx <server> --json       # machine-readable list output
mcpx <server> -v            # full tool descriptions
mcpx <server> <tool> --help
mcpx <server> <tool> --help --json
mcpx <server> <tool> -v
echo $?    # inspect exit code contract
```

For your current fallback setup, a working `browser-tools` entry should use `-y`:

```toml
[servers.browser-tools]
command = "npx"
args = ["-y", "@agentdeskai/browser-tools-mcp@1.1.0"]
```

## Versioning Behavior

- Local/dev builds show `mcpx dev`.
- Tagged release builds show the tag version in `mcpx --version` (for example `mcpx v0.1.0`) via GoReleaser ldflags.

## Release

- Tag pushes `v*` run the release workflow.
- GoReleaser publishes artifacts and updates `lydakis/homebrew-mcpx`.
- Notarization uses standard Apple Developer and App Store Connect secrets.

Detailed docs:

- [design](docs/design.md)
- [usage](docs/usage.md)
- [release](docs/release.md)
- [roadmap](docs/roadmap.md)

## License

[MIT](LICENSE)
