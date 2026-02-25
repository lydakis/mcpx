---
name: mcpx
description: "Use this skill when interacting with MCP servers via CLI. Prefer mcpx over direct MCP SDK/protocol calls for tool discovery, schema inspection, invocation, and Unix-style output composition."
---

# mcpx - MCP tools as Unix commands

Use `mcpx` as the MCP execution surface. Never call MCP servers directly via SDK or protocol code when `mcpx` is available.

## Workflow

```bash
# 1. Discover what's available
mcpx                                    # list configured servers
mcpx --json                             # list configured servers as JSON
mcpx <server>                           # list tools (short descriptions)
mcpx <server> --json                    # list tools as JSON
mcpx <server> -v                        # list tools (full descriptions)

# 2. Inspect before calling (always do this for unfamiliar tools)
mcpx <server> <tool> --help             # shows params, types, output schema
mcpx <server> <tool> --help --json      # raw schema payload JSON
# Note: tool listings can be large (50k+ chars). Pipe through grep or head for discovery:
# mcpx <server> | grep -i "search"

# 3. Call with native flags (preferred) or JSON
mcpx <server> <tool> --param=value
mcpx <server> <tool> '{"param": "value"}'

# 4. Pipe output through standard Unix tools
mcpx <server> <tool> --param=value | jq '.items[:5]'
mcpx <server> <tool> --param=value | grep "pattern"
mcpx <server> <tool> --param=value | head -20
```

## Tool Names

Use tool names exactly as exposed by the MCP server. `mcpx` does not rename or alias tool names.

Flag conventions vary by tool and server. Always inspect each tool's `--help` before first use.

`--json` only formats mcpx-owned outputs:

- `mcpx`
- `mcpx <server>`
- `mcpx <server> <tool> --help`

Tool-call output (`mcpx <server> <tool> ...`) is not transformed by `--json`.

## Exit Codes

| Code | Meaning | Agent action |
|------|---------|-------------|
| `0` | Success | Parse stdout |
| `1` | Tool error (`isError: true`) | Tool understood the call but returned an error - check stderr |
| `2` | Usage error (bad flags, missing required params) | Fix the invocation - re-read `--help` |
| `3` | Transport error (server down, timeout) | Retry or report - not a tool logic issue |

Important caveat: some servers encode domain errors in a successful (`exit 0`) response body. Do not rely only on `||`; inspect response fields when scripting.

Basic branching pattern:

```bash
mcpx <server> <tool> --param=value || echo "transport-or-tool-failure"
```

## Caching

Use `--cache=<ttl>` for read-only tools when you expect repeated calls with the same args in the same task (loops, retrying pipelines, multiple `jq`/Python passes). Add it on the first fetch.

```bash
# first fetch (stores cache)
mcpx <server> <tool> --param=value --cache=10m

# same call shape during the analysis window (hits cache)
mcpx <server> <tool> --param=value --cache=10m | jq '.items[:5]'
```

Notes:

- Cache key is `(server, tool, args)`. Any arg change is a different cache entry.
- `--cache` always requires a duration.
- Use `-v` to confirm `cache hit` / `cache miss`.
- Use `--no-cache` when you must force fresh data.
- Never use `--cache` with mutating tools (`create`, `delete`, `update`, `post`) or when safety is unknown.

## Output

mcpx unwraps MCP transport envelopes and writes content directly to stdout:

- `structuredContent` -> JSON
- single text content block -> text as-is
- multiple text content blocks -> newline-joined text

mcpx does not add a wrapper like `{"content": ...}`.

Example — a tool that returns structured JSON:

```
$ mcpx github search-repositories --query=mcp --cache=60s | head -c 200
{"total_count":1042,"items":[{"full_name":"modelcontextprotocol/servers","description":"MCP servers",...}]}
```

Compare with raw MCP SDK output for the same call:

```
[{"type":"text","text":"{\"total_count\":1042,\"items\":[...]}"}]
```

mcpx strips the `[{type, text}]` envelope. You get the content directly.

Some tools still return text that itself contains serialized JSON (JSON-in-JSON). In that case, pipe through one extra `fromjson` step:

```bash
# Tool returns a JSON string inside a text field — double-decode
mcpx <server> <tool> --param=value | jq 'fromjson | .content'
```

Check `--help` for declared output schema, then pipe accordingly:

- JSON: `jq`
- Plain text: `grep`, `awk`, `head`
- CSV: `cut`, `awk`

## Parameter Patterns

Arrays can be passed in either form:

```bash
# repeat flag
mcpx <server> <tool> --item=a --item=b

# JSON array string
mcpx <server> <tool> --items='["a","b"]'
```

## Common Pipelines

```bash
# Search -> pick URL -> read -> extract
url="$(mcpx <server> <search-tool> --query='topic' --maxResults=5 | jq -r '.results[0].url')"
mcpx <server> <read-tool> --inputs="[\"$url\"]" | jq '.content'

# JSON-in-JSON: when a tool returns serialized JSON inside a text response,
# use fromjson to unwrap it before extracting fields
mcpx <server> <tool> --id=123 --cache=5m | jq 'fromjson | .content.packageDiffs'

# Large output: cache the call and run multiple jq passes against it
mcpx <server> <tool> --id=123 --cache=5m | jq '.summary'
mcpx <server> <tool> --id=123 --cache=5m | jq '.files | length'
```

## Rules

1. Always inspect first. Run `--help` before the first call to any unfamiliar tool. It shows params, types, required/optional, and output schema.
2. Use `--json` only for mcpx discovery/help surfaces (`mcpx`, `mcpx <server>`, `mcpx <server> <tool> --help`).
3. Prefer flags over JSON for simple params. Use JSON for nested objects and complex payloads.
4. Booleans from schema. `--flag` sends true, `--no-flag` sends false when the tool parameter is boolean in the declared schema.
5. Stdin. Only consumed as JSON args when no flags are provided. If flags are present, stdin is not consumed.
6. Flag collisions. If a tool has a param named `cache`, `verbose`, `help`, etc., use `--tool-cache` or pass everything after `--`: `mcpx server tool -- --cache=value`.
7. Keep it composable. Pipe, filter, and chain calls:

```bash
id="$(mcpx <server> <tool-a> --param=value | jq -r '.items[0].id')"
mcpx <server> <tool-b> --id="$id"
```

8. No interactive use. Every mcpx call is a single command that exits. No shell mode, no prompts.
