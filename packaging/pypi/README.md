# mcpx-go (PyPI)

`mcpx-go` downloads the `mcpx` Go binary from GitHub Releases into your user cache and runs it via `mcpx-go`.

## Install

```bash
pip install mcpx-go
```

## Usage

```bash
mcpx-go --version
mcpx-go github search-repositories --query=mcp
```

## Notes

- Supports: macOS/Linux, amd64/arm64.
- Set `MCPX_GO_SKIP_DOWNLOAD=1` to skip downloading and rely on `mcpx` in `PATH`.
