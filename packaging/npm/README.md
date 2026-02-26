# mcpx-go (npm)

`mcpx-go` is the npm distribution of [`mcpx`](https://github.com/lydakis/mcpx), a CLI conversion layer for MCP servers.

This package is not a JavaScript SDK. It installs a `mcpx` executable that downloads and runs the official `mcpx` Go binary from GitHub Releases.

## Install

```bash
npm install -g mcpx-go
```

Windows users: use WSL2 and run npm install inside your Linux distro shell.

## Quick Start

```bash
mcpx --version
mcpx
mcpx github
mcpx github search-repositories --query=mcp
```

Command contract:

- `mcpx` lists servers
- `mcpx <server>` lists tools
- `mcpx <server> <tool>` calls a tool

## Notes

- Supports: macOS/Linux, amd64/arm64.
- Windows support is via WSL2 (Linux install path).
- Installs `mcpx.1` to `${XDG_DATA_HOME:-~/.local/share}/man/man1` when available.
- Set `MCPX_GO_SKIP_DOWNLOAD=1` to skip downloading and rely on `mcpx` in `PATH`.
- Full docs and source: https://github.com/lydakis/mcpx
