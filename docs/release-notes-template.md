# mcpx vX.Y.Z Release Notes

Release date: YYYY-MM-DD

## Highlights

- 
- 
- 

## Added

- 

## Changed

- 

## Fixed

- 

## Breaking Changes

- None.

## MCP Compatibility

| Surface | Version / status | Evidence |
| --- | --- | --- |
| Protocol, modern | 2026-07-28 | conformance result or smoke test |
| Protocol, legacy | 2025-11-25 | fallback smoke test |
| Go SDK | vX.Y.Z | module lock |
| Official conformance | pinned revision | scenarios and pass count |
| Tasks extension | supported / gated | SDK and extension version |

## Install / Update

Manual binary:

```bash
# Example for macOS arm64
curl -L -o mcpx https://<release-url>/mcpx-darwin-arm64
chmod +x mcpx
mv mcpx /usr/local/bin/mcpx
```

Verify:

```bash
mcpx --version
```

## Checksums

Use `SHA256SUMS` from the release artifacts:

```bash
shasum -a 256 -c SHA256SUMS
```

## Migration Notes

- 
