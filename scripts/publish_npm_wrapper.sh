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

version="$1"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src_dir="${root_dir}/packaging/npm"
work_dir="$(mktemp -d /tmp/mcpx-go-npm.XXXXXX)"
npmrc="$(mktemp /tmp/mcpx-go.npmrc.XXXXXX)"

cleanup() {
  trash "$work_dir" "$npmrc" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cp -R "${src_dir}/." "${work_dir}/"

pushd "${work_dir}" >/dev/null
npm version "$version" --no-git-tag-version >/dev/null
printf "//registry.npmjs.org/:_authToken=%s\n" "$NPM_TOKEN" > "$npmrc"
npm publish --access public --userconfig "$npmrc"
popd >/dev/null

echo "Published npm package: mcpx-go@$version"
