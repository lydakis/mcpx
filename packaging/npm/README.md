# mcpx-go (npm)

`mcpx-go` installs the `mcpx` Go binary from GitHub Releases and exposes it as `mcpx-go`.

## Install

```bash
npm install -g mcpx-go
```

## Usage

```bash
mcpx-go --version
mcpx-go github search-repositories --query=mcp
```

## Notes

- Supports: macOS/Linux, amd64/arm64.
- Set `MCPX_GO_SKIP_DOWNLOAD=1` to skip downloading and rely on `mcpx` in `PATH`.
