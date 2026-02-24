# Release & Packaging

## Local Snapshot (No Publish)

```bash
goreleaser release --snapshot --clean
```

Output artifacts are written under `dist/`:
- `mcpx_<version>_<os>_<arch>.tar.gz`
- `checksums.txt`

## GitHub Release + Homebrew Cask

Tag pushes that match `v*` trigger `.github/workflows/release.yml`.

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow:
1. Builds/publishes release artifacts with GoReleaser
2. Updates the Homebrew cask in `lydakis/homebrew-mcpx`
3. Publishes `mcpx-go` to npm from `packaging/npm`
4. Publishes `mcpx-go` to PyPI from `packaging/pypi`

## Required GitHub Secrets

- `GORELEASER_TOKEN`: token with repo write access to:
  - `lydakis/mcpx`
  - `lydakis/homebrew-mcpx`
- `NPM_TOKEN`: npm publish token with 2FA bypass enabled
- `PYPI_API_TOKEN`: PyPI token for the `mcpx-go` project
- `APPLE_DEVELOPER_ID_CERTIFICATE_P12_BASE64`
- `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`
- `APPLE_DEVELOPER_ID_APPLICATION`
- `APP_STORE_CONNECT_API_KEY_P8`
- `APP_STORE_CONNECT_KEY_ID`
- `APP_STORE_CONNECT_ISSUER_ID`

The notarization secret names intentionally match IceVault so the same values can be reused.

GoReleaser uses:
- `GITHUB_TOKEN` (auto-provided by Actions) for release assets on the source repo
- `HOMEBREW_TAP_GITHUB_TOKEN` (mapped from `GORELEASER_TOKEN`) for tap updates
- native `notarize.macos` signing/notarization before archiving darwin binaries.

Wrapper package publishing uses:
- `scripts/publish_npm_wrapper.sh <version>`
- `scripts/publish_pypi_wrapper.sh <version>`

## Install After Release

```bash
brew tap lydakis/mcpx
brew install --cask mcpx
```
