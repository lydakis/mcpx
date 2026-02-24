#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi

if [[ -z "${PYPI_API_TOKEN:-}" ]]; then
  echo "PYPI_API_TOKEN is required" >&2
  exit 1
fi

version="$1"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src_dir="${root_dir}/packaging/pypi"
work_dir="$(mktemp -d /tmp/mcpx-go-pypi.XXXXXX)"
dist_dir="$(mktemp -d /tmp/mcpx-go-pypi-dist.XXXXXX)"
venv_dir=""

cleanup() {
  if [[ -n "$venv_dir" ]]; then
    trash "$venv_dir" >/dev/null 2>&1 || true
  fi
  trash "$work_dir" "$dist_dir" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cp -R "${src_dir}/." "${work_dir}/"

python3 - "$work_dir/pyproject.toml" "$work_dir/src/mcpx_go/__init__.py" "$version" <<'PY'
from pathlib import Path
import re
import sys

pyproject = Path(sys.argv[1])
init_file = Path(sys.argv[2])
version = sys.argv[3]

text = pyproject.read_text()
updated = re.sub(r'^version\s*=\s*"[^"]+"', f'version = "{version}"', text, flags=re.MULTILINE)
if text == updated:
    raise SystemExit("failed to update version in pyproject.toml")
pyproject.write_text(updated)

init_text = init_file.read_text()
init_updated = re.sub(r'^__version__\s*=\s*"[^"]+"', f'__version__ = "{version}"', init_text, flags=re.MULTILINE)
if init_text == init_updated:
    raise SystemExit("failed to update version in __init__.py")
init_file.write_text(init_updated)
PY

if command -v uvx >/dev/null 2>&1; then
  pushd "$work_dir" >/dev/null
  uvx --from build pyproject-build --outdir "$dist_dir"
  TWINE_USERNAME="__token__" TWINE_PASSWORD="$PYPI_API_TOKEN" uvx --from twine twine upload --non-interactive "$dist_dir"/*
  popd >/dev/null
else
  venv_dir="$(mktemp -d /tmp/mcpx-go-pypi-venv.XXXXXX)"
  python3 -m venv "$venv_dir"
  "$venv_dir/bin/pip" install --upgrade build twine
  "$venv_dir/bin/python" -m build "$work_dir" --outdir "$dist_dir"
  TWINE_USERNAME="__token__" TWINE_PASSWORD="$PYPI_API_TOKEN" "$venv_dir/bin/python" -m twine upload --non-interactive "$dist_dir"/*
fi

echo "Published PyPI package: mcpx-go==$version"
