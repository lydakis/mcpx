#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi

if [[ -z "${NPM_TOKEN:-}" ]]; then
  echo "NPM_TOKEN is required" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  echo "node is required" >&2
  exit 1
fi

version="$1"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src_dir="${root_dir}/packaging/npm"
work_dir="$(mktemp -d /tmp/mcpx-go-npm.XXXXXX)"
npmrc="$(mktemp /tmp/mcpx-go.npmrc.XXXXXX)"
checksums_file="$(mktemp /tmp/mcpx-go.checksums.XXXXXX)"

release_base_url="${MCPX_GO_RELEASE_BASE_URL:-https://github.com/lydakis/mcpx/releases/download}"
release_base_url="${release_base_url%/}"
release_tag_prefix="${MCPX_GO_RELEASE_TAG_PREFIX:-v}"
release_tag="${release_tag_prefix}${version}"
checksums_url="${release_base_url}/${release_tag}/checksums.txt"

cleanup() {
  trash "$work_dir" "$npmrc" "$checksums_file" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cp -R "${src_dir}/." "${work_dir}/"

curl -fsSL "$checksums_url" -o "$checksums_file"

pushd "${work_dir}" >/dev/null
npm version "$version" --no-git-tag-version >/dev/null
node - "$checksums_file" "./lib/checksums.json" "$version" <<'NODE'
const fs = require("fs");
const path = require("path");

const [checksumsPath, outPath, version] = process.argv.slice(2);
const lines = fs.readFileSync(checksumsPath, "utf8").split(/\r?\n/);
const prefix = `mcpx_${version}_`;
const checksums = {};
const supportedGOOS = ["darwin", "linux"];
const supportedGOARCH = ["amd64", "arm64"];
const expectedAssets = [];

for (const goos of supportedGOOS) {
  for (const goarch of supportedGOARCH) {
    expectedAssets.push(`mcpx_${version}_${goos}_${goarch}.tar.gz`);
  }
}

for (const line of lines) {
  const trimmed = line.trim();
  if (!trimmed) continue;
  const match = trimmed.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
  if (!match) continue;
  const digest = match[1].toLowerCase();
  const name = path.basename(match[2].trim());
  if (!name.startsWith(prefix) || !name.endsWith(".tar.gz")) continue;
  checksums[name] = digest;
}

const missingAssets = expectedAssets.filter((asset) => !Object.prototype.hasOwnProperty.call(checksums, asset));
if (missingAssets.length > 0) {
  throw new Error(`missing release archive checksums for version ${version}: ${missingAssets.join(", ")}`);
}

const manifestChecksums = {};
for (const asset of expectedAssets) {
  manifestChecksums[asset] = checksums[asset];
}

fs.writeFileSync(outPath, `${JSON.stringify({ version, checksums: manifestChecksums }, null, 2)}\n`);
NODE
printf "//registry.npmjs.org/:_authToken=%s\n" "$NPM_TOKEN" > "$npmrc"
npm publish --access public --userconfig "$npmrc"
popd >/dev/null

echo "Published npm package: mcpx-go@$version"
