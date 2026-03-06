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

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

version="$1"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src_dir="${root_dir}/packaging/pypi"
work_dir="$(mktemp -d /tmp/mcpx-go-pypi.XXXXXX)"
dist_dir="$(mktemp -d /tmp/mcpx-go-pypi-dist.XXXXXX)"
checksums_file="$(mktemp /tmp/mcpx-go-pypi-checksums.XXXXXX)"
venv_dir=""
pip_cache_dir=""

release_base_url="${MCPX_GO_RELEASE_BASE_URL:-https://github.com/lydakis/mcpx/releases/download}"
release_base_url="${release_base_url%/}"
release_tag_prefix="${MCPX_GO_RELEASE_TAG_PREFIX:-v}"
release_tag="${release_tag_prefix}${version}"
checksums_url="${release_base_url}/${release_tag}/checksums.txt"

cleanup() {
  if [[ -n "$venv_dir" ]]; then
    trash "$venv_dir" >/dev/null 2>&1 || true
  fi
  if [[ -n "$pip_cache_dir" ]]; then
    trash "$pip_cache_dir" >/dev/null 2>&1 || true
  fi
  trash "$work_dir" "$dist_dir" "$checksums_file" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cp -R "${src_dir}/." "${work_dir}/"
curl -fsSL "$checksums_url" -o "$checksums_file"

python3 - "$work_dir" "$checksums_file" "$version" <<'PY'
from pathlib import Path
import re
import sys

work_dir = Path(sys.argv[1])
checksums_txt = Path(sys.argv[2])
version = sys.argv[3]
pyproject = work_dir / "pyproject.toml"
init_file = work_dir / "src" / "mcpx_go" / "__init__.py"
checksums_manifest = work_dir / "src" / "mcpx_go" / "checksums.json"

sys.path.insert(0, str(work_dir / "src"))

from mcpx_go.checksum_manifest import build_bundled_checksum_manifest
from mcpx_go.checksum_manifest import serialize_bundled_checksum_manifest

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

manifest = build_bundled_checksum_manifest(version, checksums_txt.read_text())
checksums_manifest.write_text(serialize_bundled_checksum_manifest(manifest))
PY

pushd "$work_dir" >/dev/null
bash "${root_dir}/scripts/build_pypi_dist.sh" "$work_dir" "$dist_dir"
popd >/dev/null

if ! python3 -m twine --version >/dev/null 2>&1; then
  venv_dir="$(mktemp -d /tmp/mcpx-go-pypi-venv.XXXXXX)"
  pip_cache_dir="$(mktemp -d /tmp/mcpx-go-pypi-pip-cache.XXXXXX)"
  python3 -m venv "$venv_dir"
  PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_CACHE_DIR="$pip_cache_dir" \
    "$venv_dir/bin/pip" install --upgrade twine >/dev/null
  TWINE_USERNAME="__token__" TWINE_PASSWORD="$PYPI_API_TOKEN" "$venv_dir/bin/python" -m twine upload --non-interactive "$dist_dir"/*
else
  TWINE_USERNAME="__token__" TWINE_PASSWORD="$PYPI_API_TOKEN" python3 -m twine upload --non-interactive "$dist_dir"/*
fi

echo "Published PyPI package: mcpx-go==$version"
