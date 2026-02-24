# mcpx Release Checklist

## Versioning

- Set release version/tag.
- Verify `mcpx --version` output.

## Validation

- Run `make check` (or `go test ./...`, `go vet ./...`, `go build ./...`).
- Run `make qa` for smoke + integration matrix checks.
- Optionally run `RUN_DIST=1 make qa` to include artifact generation in one pass.
- Run `goreleaser release --snapshot --clean` to validate release packaging.

## Smoke Tests

- `mcpx` with no config prints setup guidance.
- `mcpx completion bash` returns script text.
- `mcpx <server> <tool> --help` renders options/output and writes a man page.
- One stdio server call and one HTTP server call succeed end-to-end.

## Binary

- Confirm binary size target (< 15MB) for primary build artifact.
- Verify executable permissions and startup behavior on target platforms.

## Docs

- `docs/design.md` reflects implemented behavior.
- `docs/roadmap.md` status is current.
- `docs/usage.md` includes completion + troubleshooting guidance.
- `docs/install.md` includes manual/homebrew install paths.

## Packaging / Distribution

- Ensure `.goreleaser.yml` tap settings match target repos (`mcpx`, `homebrew-mcpx`).
- Verify `GORELEASER_TOKEN` secret is configured in GitHub Actions.
- Verify notarization secrets are configured (same names as IceVault):
  - `APPLE_DEVELOPER_ID_CERTIFICATE_P12_BASE64`
  - `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`
  - `APPLE_DEVELOPER_ID_APPLICATION`
  - `APP_STORE_CONNECT_API_KEY_P8`
  - `APP_STORE_CONNECT_KEY_ID`
  - `APP_STORE_CONNECT_ISSUER_ID`
- Tag release (`v*`) and verify workflow publishes:
  - `mcpx_<version>_<os>_<arch>.tar.gz`
  - `checksums.txt`
  - Homebrew cask update in tap repo.
- Include release notes with:
  - new behavior
  - breaking changes
  - migration steps (if any)
  - install/update steps
