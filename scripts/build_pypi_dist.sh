#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <source-dir> <out-dir>" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

source_dir="$1"
out_dir="$2"
venv_dir=""
pip_cache_dir=""

cleanup() {
  if [[ -n "$venv_dir" ]]; then
    trash "$venv_dir" >/dev/null 2>&1 || true
  fi
  if [[ -n "$pip_cache_dir" ]]; then
    trash "$pip_cache_dir" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

mkdir -p "$out_dir"

if python3 -m build --version >/dev/null 2>&1; then
  python3 -m build "$source_dir" --outdir "$out_dir"
  exit 0
fi

venv_dir="$(mktemp -d /tmp/mcpx-go-pypi-build-venv.XXXXXX)"
pip_cache_dir="$(mktemp -d /tmp/mcpx-go-pypi-pip-cache.XXXXXX)"

python3 -m venv "$venv_dir"
PIP_DISABLE_PIP_VERSION_CHECK=1 \
  PIP_CACHE_DIR="$pip_cache_dir" \
  "$venv_dir/bin/pip" install --upgrade build >/dev/null
"$venv_dir/bin/python" -m build "$source_dir" --outdir "$out_dir"
