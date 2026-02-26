# mcpx — Convert MCP Servers into CLIs

## What Is This

`mcpx` is a single binary that converts MCP servers into shell commands. It exists because many agent workflows are shell-first: they can pipe, filter, truncate, and only pull what they need into context.

There are existing tools in this space (`philschmid/mcp-cli`, `apify/mcpc`, `f/mcptools`). `mcpx` is not a feature-rich alternative. It's a sharper knife — it packages a specific set of Unix-native choices together that none of them combine.

## Prior Art

| Tool | What it does well | Where mcpx differs |
|------|------------------|-------------------|
| `mcp-cli` (philschmid) | Discover → inspect → call, connection pooling daemon, skill integration | No output schema in --help, no error code normalization, no native flag translation, no MCP response unwrapping |
| `mcpc` (apify) | Sessions, OAuth 2.1, proxy mode, tool detail display | Feature-rich by design — different goal than Unix-philosophy sharp knife |
| `mcptools` (fka) | Interactive shell, proxy mode, call tools | Similar gaps to mcp-cli for agent-optimized usage |

All of them solve "call MCP from CLI." mcpx packages a specific set of choices — native output passthrough, MCP response unwrapping, explicit error codes, and native Unix flag translation — into a conversion layer that behaves like a real Unix command surface.

## The Four Fundamentals

These are the only things `mcpx` does beyond basic discover/inspect/call. Each one directly reduces context bloat or makes the tool behave like a real Unix command. Existing CLIs address some of these individually — mcpx is opinionated about packaging them together in a specific way.

### Design Principle: It's Just a CLI

Once you pick your server and tool, `mcpx` behaves exactly like any native Unix command. No invented syntax. No invented behavior. If you know `grep --help`, `man curl`, or `ls | head -5`, you already know how to use `mcpx`. MCP tool parameters become CLI flags, and tool descriptions power `--help` output. Everything follows POSIX conventions. Output goes to stdout, errors go to stderr, and you pipe to whatever you want.

### 1. Output Schema Awareness

**Problem:** Agent calls a tool, gets back unknown JSON. Has to dump it all into context to figure out what it got.

**Solution:** Standard `--help` shows inputs AND outputs. No special flags — just the same `--help` every CLI has, but it includes what comes back.

```bash
mcpx github search-repositories --help
# NAME
#     mcpx github search-repositories — Search for GitHub repositories
#
# SYNOPSIS
#     mcpx github search-repositories [--query=STRING] [--page=NUMBER]
#
# OPTIONS
#     --query=STRING    (required) Search query
#     --page=NUMBER     Page number for pagination
#
# OUTPUT (json)
#     items[].name          string    Repository name
#     items[].full_name     string    owner/repo format
#     items[].stargazers_count  number    Star count
#     items[].description   string    Repo description
#     ...
#
# EXAMPLES
#     mcpx github search-repositories --query="mcp"
#     mcpx github search-repositories --query="mcp" | head -5

# Root CLI man page
man mcpx
```

MCP tool parameters are translated to GNU-style long flags. `{"query": "mcp", "page": 2}` becomes `--query=mcp --page=2`. JSON input is still supported for complex args, but simple calls look like any other CLI:

```bash
# These are equivalent:
mcpx github search-repositories --query="mcp" --page=2
mcpx github search-repositories '{"query": "mcp", "page": 2}'
```

Tool names are used exactly as exposed by the MCP server. mcpx does not rewrite tool names.

For output schema discovery: two-tier approach.
1. If the MCP tool declares `outputSchema` → generate from it
2. If not → `--help` shows "OUTPUT: not declared by server"

No inference from previous calls. A tool can return different shapes depending on inputs (results vs. empty vs. error), so cached schemas would be actively misleading.

### 2. Caching

**Problem:** Agent calls `list-repos` three times in a reasoning loop. Each one hits the server and returns the same data.

**Solution:** Transparent response caching, modeled after HTTP caching semantics. **Off by default.** Caching a mutating tool call (like `post-message` or `create-repo`) would silently skip the action on cache hit — that's a data loss bug, not a feature.

```bash
# Explicitly opt in per call
mcpx github search-repositories --query="mcp" --cache=60s

# Force fresh
mcpx github search-repositories --query="mcp" --no-cache

# Verbose cache info (on stderr, like curl -v)
mcpx github search-repositories --query="mcp" --cache=60s -v
# stderr: mcpx: cache hit (age=23s ttl=60s)
```

Cache is keyed on `(server, tool_name, args_hash)` and stored in `$XDG_CACHE_HOME/mcpx/`. Per-server AND per-tool config:

```toml
[servers.github]
default_cache_ttl = "30s"

# Override: never cache mutating tools
[servers.github.tools.create-issue]
cache = false

# Or use a denylist pattern
[servers.github]
no_cache_tools = ["create-*", "delete-*", "post-*", "update-*"]
```

Rule: if `--cache` is not set and no config default exists, mcpx never caches. Safe by default.

### 3. Error Normalization

**Problem:** MCP servers return errors in wildly different formats. Agents can't reliably detect failures.

**Solution:** Standard Unix exit code conventions, mapped explicitly to MCP's error channels. Errors on stderr. Always.

- **Exit 0** = success, stdout has result
- **Exit 1** = tool-level error (MCP response with `isError: true` — the tool ran but reported failure)
- **Exit 2** = usage/invocation error:
  - Client-side: wrong flags, missing required flags, bad flag types
  - Server-side: JSON-RPC `-32602 Invalid Params`, `-32601 Method Not Found` (you called it wrong)
- **Exit 3** = transport error (server not running, timeout, connection refused, JSON-RPC protocol errors like `-32700 Parse Error`)

The distinction matters: exit 1 means "the tool understood you but said no" (e.g. repo not found). Exit 2 means "you called it wrong" (e.g. missing `--query`). Exit 3 means "couldn't reach the tool at all." Agents can branch on this.

```bash
# Standard Unix error handling — just works
mcpx github get-repo --owner=x --repo=doesntexist || echo "failed"

# Errors always go to stderr, never pollute stdout
mcpx github get-repo --owner=x --repo=doesntexist 2>/dev/null
# exit code: 1

# Verbose mode shows details (like curl -v, ssh -v, git --verbose)
mcpx github get-repo --owner=x --repo=doesntexist -v
# stderr: mcpx: error: not_found — Repository x/doesntexist not found
```

No `--json-errors` flag. Stderr is human-readable. If the agent wants structured errors, it uses `2>&1` and parses — same as any other CLI.

### 4. Native Output to Stdout — Pipe to Whatever You Want

**Problem:** Other MCP CLIs try to reinvent output formatting with custom flags. Or they force everything into JSON.

**Solution:** Don't. Output whatever the MCP server returns. Agents and humans already have `jq`, `grep`, `head`, `awk`, `cut`, `wc`. Use them.

```bash
# JSON output (most MCP tools) → use jq
mcpx github search-repositories --query="mcp" | jq '.items[].full_name'

# Plain text output → use grep, awk, whatever
mcpx filesystem read-file --path="./README.md" | grep "install"

# CSV output → use cut, awk, pandas
mcpx analytics export-report --format=csv | cut -d',' -f1,3

# Pretty print JSON → jq with no filter
mcpx github search-repositories --query="mcp" | jq .
```

`--help` tells you the output format. You pipe accordingly. mcpx doesn't add flags for things Unix already does.

## What mcpx Does NOT Do

- **No output formatting.** That's what `jq`, `grep`, `awk` are for.
- **No truncation.** That's what `head`, `tail`, `jq '[:5]'` are for.
- **No built-in LLM parsing.** The agent calling `mcpx` is the LLM.
- **No interactive shell mode.** Agents use single commands.
- **No OAuth/auth management.** Configure credentials in the server config.
- **No agent identity/coordination.** Out of scope for mcpx; it is a tool, not a platform.
- **No invented behavior.** If Unix already does it, mcpx doesn't reinvent it.

## Architecture

Daemon/client model. The daemon handles both stdio servers (spawned locally) and HTTP servers (remote):

```
  Agent or human runs:
  $ mcpx github search-repositories --query="mcp"
       │
       ▼
┌─────────────┐         ┌──────────────────────────┐
│  mcpx (CLI) │───IPC──▶│     mcpxd (daemon)       │
│             │  unix   │                          │
│ Translates  │  socket │ Manages connections:     │
│ flags→JSON  │◀────────│                          │
│ Formats out │         │  stdio servers:           │
│ Exit codes  │         │  ┌─────────┐ ┌─────────┐ │
└─────────────┘         │  │ github  │ │postgres │ │
                        │  │ (pipe)  │ │ (pipe)  │ │
                        │  └─────────┘ └─────────┘ │
                        │                          │
                        │  http servers:            │
                        │  ┌─────────┐ ┌─────────┐ │
                        │  │ apify   │ │ custom  │ │
                        │  │ (https) │ │ (https) │ │
                        │  └─────────┘ └─────────┘ │
                        └──────────────────────────┘
                                    │
                        $XDG_RUNTIME_DIR/mcpx/mcpx.sock
                        (or ~/.local/state/mcpx/mcpx.sock)
```

- `mcpx` is the thin CLI client. Translates flags to JSON-RPC, sends to daemon, prints result, exits.
- `mcpxd` is auto-spawned on first call. For stdio servers, it holds process connections open and manages keep-alive. For HTTP servers, it maintains connection pools and handles request routing.
- Sliding window keep-alive: each call resets the per-server TTL (default 60s). Daemon dies when everything times out.
- Communication over Unix domain socket. Fast, no network overhead.

Config lives at `~/.config/mcpx/config.toml`. If no servers are configured, mcpx can import `mcpServers` from common MCP client JSON files as read-only fallback sources. You can override or disable fallback paths with `fallback_sources`.

## Config

```toml
# ~/.config/mcpx/config.toml

# Stdio server (local process)
[servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }  # env var expansion
default_cache_ttl = "30s"

# HTTP server (remote, no local process)
[servers.apify]
url = "https://mcp.apify.com"
headers = { Authorization = "Bearer ${APIFY_TOKEN}" }

# Another stdio server
[servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
```

Config supports:
- `command`/`args` for stdio servers (daemon spawns and manages the process)
- `url` for HTTP servers (daemon makes HTTP requests, no process to manage)
- `${ENV_VAR}` expansion for secrets
- Per-server and per-tool cache defaults
- `fallback_sources = ["/abs/path/source1.json", "/abs/path/source2.json"]` to control MCP fallback discovery (`[]` disables defaults)
- That's it

The CLI surface is identical regardless of transport. The agent doesn't know or care whether `mcpx github ...` talks to a local process or a remote URL.

## Command Surface

```bash
mcpx                                    # list configured servers
mcpx <server>                           # list tools
mcpx <server> <tool> --help             # full help: params, output schema, examples
man mcpx                                # root CLI man page

# Calling tools — MCP params become CLI flags
mcpx github search-repositories --query="mcp"
mcpx github search-repositories --query="mcp" --page=2
mcpx filesystem read-file --path="./README.md"

# JSON input still works for complex/nested args
mcpx github search-repositories '{"query": "mcp"}'

# Stdin is consumed as JSON args only when no flags are given
echo '{"path": "./file"}' | mcpx filesystem read-file

# Output is the native format from the MCP server. Pipe to jq, grep, awk, etc.
mcpx github search-repositories --query="mcp" | jq '.items[0].name'
mcpx github search-repositories --query="mcp" | jq '.items[:5]'   # first 5
mcpx filesystem read-file --path="./README.md" | head -20          # first 20 lines

# Caching
--cache=DURATION    cache TTL (30s, 5m, 1h)
--no-cache          bypass cache

# Standard Unix flags
-v, --verbose       verbose output on stderr (cache status, timing, server info)
-q, --quiet         suppress stderr entirely
--version           version
--help              help
```

Tool names are passed through exactly as exposed by the server.

MCP tool parameters map to GNU-style `--long-flags`. Required params are required flags. Optional params have defaults shown in `--help`. Booleans support `--flag` (true) and `--no-flag` (false). Nested objects fall back to JSON: `--config='{"nested": "value"}'`.

**Flag name collisions:** Sooner or later an MCP tool will have a parameter named `cache`, `verbose`, or `help`. Rule: mcpx's own flags take precedence. Tool flags that collide are prefixed with `--tool-` (e.g. `--tool-cache`). The `--` separator also works: everything after `--` is treated as tool flags only. `--help` always shows both namespaces clearly.

## Implementation Notes

**Language:** Go. Single static binary — mcpx itself has no runtime dependencies. It runs whatever server command you configure (node, python, etc.), so the MCP server's runtime is your responsibility. mcpx doesn't try to bundle or manage those runtimes.

**Daemon lifecycle:** `mcpx` checks for the daemon socket on every call (see Design Decisions for path resolution). If absent, it acquires a file lock (`mcpx.lock` alongside the socket path), spawns `mcpxd` as a background process, waits for the socket, then releases the lock. This prevents two simultaneous `mcpx` calls from spawning duplicate daemons. The daemon holds stdio server connections open and resets a per-server keep-alive timer on each call (sliding window, 60s default). For HTTP servers, the daemon maintains connection pools but has no process to manage. Daemon dies when all servers have timed out and no HTTP connections are active.

**Daemon security:** The socket is created with mode `0600` (owner-only). On every connection, the daemon verifies the peer's UID matches its own — refuses to talk otherwise. On startup, the daemon writes a random nonce to a state file (`mcpx.state` alongside the socket); the CLI reads this nonce and includes it in the handshake. If the nonce doesn't match, the CLI assumes a stale or hijacked socket, removes it, and spawns a fresh daemon. This prevents a rogue process from impersonating the daemon on a shared system.

**Flag translation:** MCP `inputSchema` properties become GNU-style `--long-flags`. Types map naturally: `string` → takes a value, `boolean` → `--flag` sends `true`, `--no-flag` sends `false` (GNU convention), `number` → takes a value with validation, `array` → repeatable flag `--tag=a --tag=b`, `object` → accepts JSON string. Required properties become required flags.

**Stdin handling:** Stdin is only consumed as JSON args when passed as a positional argument (`echo '{}' | mcpx server tool`). If the tool has flags provided, stdin is not consumed. This avoids ambiguity with tools that legitimately expect stdin data (e.g. file uploads). When in doubt, use a positional JSON arg instead: `mcpx server tool '{}'`.

**Streaming:** For v1, mcpx buffers the complete MCP response then emits to stdout. Streaming to stdout is more Unix-native but conflicts with caching and with guaranteeing valid output. May add `--stream` in a future version if there's demand.

**No tool subcommands.** `mcpx` (bare) lists servers, and the first positional argument is the server namespace. The only reserved utility commands are `completion` and `__complete` for shell integration; they explicitly defer to same-named servers when configured.

**Man pages:** Ship a static root man page (`mcpx.1`) in release artifacts and install it as part of package installation (`man mcpx`). Tool-level docs are served through `mcpx <server> <tool> --help` rather than generated tool man pages.

**Tab completion:** Generates completions for bash/zsh/fish. Server names, tool names, and flag names all completable. Install via `mcpx completion bash > /etc/bash_completion.d/mcpx`.

**Output schema discovery:** Two tiers only:
1. If the MCP tool declares `outputSchema` → use it in `--help`
2. If not → `--help` shows "OUTPUT: not declared by server"
No inference, no caching of observed shapes. A tool can return different structures depending on inputs, so guessing the schema from a previous call would be actively misleading.

**XDG compliance:** Config in `$XDG_CONFIG_HOME/mcpx/`, cache in `$XDG_CACHE_HOME/mcpx/`, daemon socket in `$XDG_RUNTIME_DIR/mcpx/` (fallback: `$XDG_STATE_HOME/mcpx/`).

**Config fallback:** On startup, if no servers are defined in `config.toml`, mcpx reads `mcpServers` from configured fallback JSON sources. By default it checks common MCP client locations; `fallback_sources` can override this list or disable fallback entirely.

**Binary size target:** Under 15MB. Go's net/http and crypto/tls are in the standard library so HTTP transport support doesn't require external deps, but keep an eye on binary bloat from TLS.

## MCP Response Mapping

**Rule: respect the MCP server's output. Don't transform it.**

MCP tool responses come wrapped in "content blocks" (a protocol transport detail). mcpx strips that wrapper and outputs the content directly:

1. If `structuredContent` exists → emit it as JSON
2. Else if there's one text block → emit the text as-is (could be JSON, plain text, CSV, markdown — whatever the tool returns)
3. Else if there are multiple text blocks → concatenate them (newline-separated)
4. Image/resource blocks → write binary to temp file, emit the file path on stdout

That's it. No wrapping. No transforming. If the MCP server returns JSON, you get JSON — pipe to `jq`. If it returns plain text, you get plain text — pipe to `grep` or `awk`. If it returns CSV, pipe to `cut` or `pandas`.

The agent knows what to expect because `--help` shows the output format. mcpx doesn't second-guess the server — it just strips the protocol envelope and gets out of the way.

## What This Enables

An agent with shell access can now do:

```bash
# Get the 3 most starred repos matching "mcp", just names
mcpx github search-repositories --query="mcp" | jq -r '.items[:3][].full_name'

# Check if a file exists without dumping its contents
mcpx filesystem list-directory --path="./src" | jq -r '.[]' | grep -q "main.rs" && echo "found"

# Chain across servers
REPO=$(mcpx github search-repositories --query="mcpx" | jq -r '.items[0].full_name')
mcpx slack post-message --channel="#dev" --text="Top result: $REPO"

# Standard Unix composition
mcpx github list-repos --org="anthropic" | jq -r '.[].name' | while read repo; do
  mcpx github get-repo --repo="$repo" | jq '.stargazers_count'
done | sort -rn | head -5
```

Each of those puts minimal tokens into context. The agent reads `--help`, knows the output format, pipes accordingly. All standard Unix — no mcpx-specific syntax to learn.

## Design Decisions (Resolved)

1. **Daemon socket location:** Hybrid fallback. Check `$XDG_RUNTIME_DIR` first (use `$XDG_RUNTIME_DIR/mcpx/mcpx.sock`). If unset (macOS default), fall back to `$XDG_STATE_HOME/mcpx/mcpx.sock` (resolves to `~/.local/state/mcpx/mcpx.sock`). Avoid `/tmp` — on macOS it's a symlink to `/private/tmp` that gets wiped, and shared `/tmp` introduces cross-user socket permission issues.

2. **Keep-alive:** Sliding window. Every request resets the server's expiration to `now + 60s`. If idle for 60 contiguous seconds, the server spins down. No accumulation, no capping. An agent looping 50 times over 10 minutes keeps the server alive; it dies exactly 60s after the last call.

3. **Nested params:** JSON only. No dot-notation. `--config='{"nested": "value"}'` is trivial for LLMs to generate (they output JSON natively). Dot-notation creates edge cases (keys with periods, array indices) for zero practical benefit.

4. **jq:** Not bundled. Most MCP tools return JSON, but not all — mcpx passes through the native format. Filtering is the ecosystem's job. `jq` is a recommended peer dependency for JSON tools, `grep`/`awk` for text. Agents know the format from `--help` and pipe accordingly.
