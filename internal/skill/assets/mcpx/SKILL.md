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
mcpx <server>                           # list tools on a server

# 2. Inspect before calling (always do this for unfamiliar tools)
mcpx <server> <tool> --help             # shows params, types, output schema

# 3. Call with native flags (preferred) or JSON
mcpx <server> <tool> --param=value
mcpx <server> <tool> '{"param": "value"}'

# 4. Pipe output through standard Unix tools
mcpx <server> <tool> --param=value | jq '.items[:5]'
mcpx <server> <tool> --param=value | grep "pattern"
mcpx <server> <tool> --param=value | head -20
```

## Exit Codes

| Code | Meaning | Agent action |
|------|---------|-------------|
| `0` | Success | Parse stdout |
| `1` | Tool error (`isError: true`) | Tool understood the call but returned an error - check stderr |
| `2` | Usage error (bad flags, missing required params) | Fix the invocation - re-read `--help` |
| `3` | Transport error (server down, timeout) | Retry or report - not a tool logic issue |

Use exit codes to branch:

```bash
mcpx github get-repo --owner=x --repo=y || echo "failed"
if mcpx filesystem read-file --path="./config.json" > /tmp/config.json 2>/dev/null; then
  # process file
fi
```

## Caching

Use `--cache` for read-only tools called in loops or repeated reasoning steps:

```bash
mcpx github search-repositories --query="mcp" --cache=60s
```

Never use `--cache` with mutating tools (`create`, `delete`, `update`, `post`).

## Output

mcpx outputs the MCP server's native format - usually JSON, sometimes plain text. Check `--help` for the output type, then pipe accordingly:

- JSON: `jq`
- Plain text: `grep`, `awk`, `head`
- CSV: `cut`, `awk`

## Rules

1. Always inspect first. Run `--help` before the first call to any unfamiliar tool. It shows params, types, required/optional, and output schema.
2. Prefer flags over JSON. `--query="mcp"` is clearer than `'{"query": "mcp"}'`. Use JSON for nested objects and complex payloads.
3. Booleans from schema. `--flag` sends true, `--no-flag` sends false when the tool parameter is boolean in the declared schema.
4. Stdin. Only consumed as JSON args when no flags are provided. If flags are present, stdin is not consumed.
5. Flag collisions. If a tool has a param named `cache`, `verbose`, `help`, etc., use `--tool-cache` or pass everything after `--`: `mcpx server tool -- --cache=value`.
6. Keep it composable. Pipe, filter, chain. Don't build custom parsers when `jq` or `grep` will do.
7. No interactive use. Every mcpx call is a single command that exits. No shell mode, no prompts.
