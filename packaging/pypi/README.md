# mcpx-go (PyPI)

`mcpx-go` is the PyPI distribution of [`mcpx`](https://github.com/lydakis/mcpx), a CLI that turns MCP servers into composable CLIs.

This package is not a Python SDK. It installs a `mcpx` executable that downloads and runs the official `mcpx` Go binary from GitHub Releases.

## Install

```bash
pip install mcpx-go
```

Windows users: use WSL2 and run `pip install` inside your Linux distro shell.

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
- Optional: when Codex Apps are enabled locally, `mcpx` can expose connected apps as virtual MCP servers. Auth remains managed by Codex.
- Installs `mcpx.1` to `${XDG_DATA_HOME:-~/.local/share}/man/man1` when available.
- Set `MCPX_GO_SKIP_DOWNLOAD=1` to skip downloading and rely on `mcpx` in `PATH`.
- Full docs and source: https://github.com/lydakis/mcpx
