#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GO_CMD="${GO_CMD:-go}"
BINARY="${BINARY:-./mcpx}"
QA_SCOPE="${QA_SCOPE:-all}"
RUN_DIST="${RUN_DIST:-0}"

log() {
  printf '%s\n' "$*"
}

run_step() {
  local label="$1"
  shift
  log
  log "==> $label"
  "$@"
  log "[PASS] $label"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "missing required command: $cmd"
    return 1
  fi
}

ensure_binary() {
  "$GO_CMD" build -o "$BINARY" ./cmd/mcpx
}

smoke_no_config() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' RETURN

  mkdir -p "$tmp/runtime" "$tmp/state" "$tmp/cache" "$tmp/data" "$tmp/config"

  local output status
  set +e
  output="$(HOME="$tmp" \
    XDG_CONFIG_HOME="$tmp/config" \
    XDG_RUNTIME_DIR="$tmp/runtime" \
    XDG_STATE_HOME="$tmp/state" \
    XDG_CACHE_HOME="$tmp/cache" \
    XDG_DATA_HOME="$tmp/data" \
    "$BINARY" 2>&1)"
  status=$?
  set -e

  if [[ "$status" -ne 0 ]]; then
    log "mcpx exited with status $status"
    log "$output"
    return 1
  fi

  [[ "$output" == *"No MCP servers configured."* ]]
  [[ "$output" == *"Create a config file at"* ]]
}

smoke_completion() {
  local out
  out="$("$BINARY" completion bash)"
  [[ "$out" == "# bash completion for mcpx"* ]]
}

smoke_json_no_config() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' RETURN

  mkdir -p "$tmp/runtime" "$tmp/state" "$tmp/cache" "$tmp/data" "$tmp/config"

  local output status normalized
  set +e
  output="$(HOME="$tmp" \
    XDG_CONFIG_HOME="$tmp/config" \
    XDG_RUNTIME_DIR="$tmp/runtime" \
    XDG_STATE_HOME="$tmp/state" \
    XDG_CACHE_HOME="$tmp/cache" \
    XDG_DATA_HOME="$tmp/data" \
    "$BINARY" --json 2>&1)"
  status=$?
  set -e

  if [[ "$status" -ne 0 ]]; then
    log "mcpx --json exited with status $status"
    log "$output"
    return 1
  fi

  normalized="$(printf '%s' "$output" | tr -d '[:space:]')"
  [[ "$normalized" == "[]" ]]
}

smoke_add_help() {
  local out
  out="$("$BINARY" add --help)"
  [[ "$out" == *"Usage:"* ]]
  [[ "$out" == *"install-link URL"* ]]
  [[ "$out" == *"--overwrite"* ]]
}

smoke_shim_help() {
  local out
  out="$("$BINARY" shim --help)"
  [[ "$out" == *"Usage:"* ]]
  [[ "$out" == *"mcpx shim install <server>"* ]]
  [[ "$out" == *"install"* ]]
}

smoke_skill_help() {
  local out
  out="$("$BINARY" skill --help)"
  [[ "$out" == *"Usage:"* ]]
  [[ "$out" == *"mcpx skill install"* ]]
  [[ "$out" == *"mcpx skill install [<server>]"* ]]
}

packaging_pypi_tests() {
  require_cmd python3
  python3 -m unittest discover -s packaging/pypi/tests -p 'test_*.py'
}

packaging_pypi_checksum_manifest_shape() {
  require_cmd python3
  python3 <<'PY'
from pathlib import Path
import json

path = Path("packaging/pypi/src/mcpx_go/checksums.json")
manifest = json.loads(path.read_text())
if not isinstance(manifest.get("version"), str):
    raise SystemExit(1)
if not isinstance(manifest.get("checksums"), dict):
    raise SystemExit(1)
PY
}

packaging_pypi_dist_contains_checksum_manifest() {
  require_cmd python3
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' RETURN

  bash "$ROOT_DIR/scripts/build_pypi_dist.sh" packaging/pypi "$tmp" >/dev/null

  python3 - "$tmp" <<'PY'
from pathlib import Path
import tarfile
import zipfile
import sys

dist_dir = Path(sys.argv[1])
wheel = next(dist_dir.glob("mcpx_go-*.whl"))
sdist = next(dist_dir.glob("mcpx_go-*.tar.gz"))

with zipfile.ZipFile(wheel) as archive:
    if "mcpx_go/checksums.json" not in archive.namelist():
        raise SystemExit(1)

with tarfile.open(sdist, "r:gz") as archive:
    names = archive.getnames()
    if not any(name.endswith("/src/mcpx_go/checksums.json") for name in names):
        raise SystemExit(1)
PY
}

packaging_npm_pack_dry_run() {
  require_cmd npm
  (
    cd packaging/npm
    npm pack --dry-run >/dev/null
  )
}

packaging_npm_tests() {
  require_cmd node
  node --test packaging/npm/tests/*.test.js
}

packaging_npm_postinstall_skip_download() {
  require_cmd node
  MCPX_GO_SKIP_DOWNLOAD=1 node ./packaging/npm/lib/postinstall.js >/dev/null
}

packaging_npm_checksum_manifest_shape() {
  require_cmd node
  node <<'NODE'
const manifest = require("./packaging/npm/lib/checksums.json");
if (!manifest || typeof manifest !== "object") process.exit(1);
if (typeof manifest.version !== "string") process.exit(1);
if (!manifest.checksums || typeof manifest.checksums !== "object") process.exit(1);
NODE
}

validate_scope() {
  case "$QA_SCOPE" in
    core|extended|all)
      ;;
    *)
      log "invalid QA_SCOPE: $QA_SCOPE (expected: core|extended|all)"
      return 1
      ;;
  esac
}

run_core() {
  run_step "go test ./..." "$GO_CMD" test ./...
  run_step "go vet ./..." "$GO_CMD" vet ./...
  run_step "go build ./cmd/mcpx" "$GO_CMD" build -o "$BINARY" ./cmd/mcpx

  run_step "smoke: no config guidance" smoke_no_config
  run_step "smoke: completion output" smoke_completion

  run_step "integration: stdio/http pool transports" \
    "$GO_CMD" test ./internal/mcppool -run 'TestPool(Stdio|HTTP)Integration' -count=1
  run_step "smoke: daemon lifecycle paths" \
    "$GO_CMD" test ./internal/daemon -run 'Test(DispatchShutdownReturnsAckAndSignalsProcess|KeepaliveClosesServerAfterIdleTimeout|KeepaliveTouchResetsSlidingWindow|SpawnOrConnectUsesExistingDaemonWhenNonceValid|SpawnOrConnectSpawnsDaemonWhenMissing)' -count=1
  run_step "smoke: cache enabled/disabled paths" \
    "$GO_CMD" test ./internal/daemon -run 'Test(CallToolUsesCachedResponseWhenPresent|CallToolCachesSuccessfulResponseWithDefaultTTL|EffectiveCacheTTLNoCacheRequestDisablesCaching)' -count=1
}

run_extended() {
  ensure_binary

  run_step "contract: root --json with no config" smoke_json_no_config
  run_step "contract: add --help surface" smoke_add_help
  run_step "contract: shim --help surface" smoke_shim_help
  run_step "contract: skill --help surface" smoke_skill_help

  run_step "packaging: pypi wrapper tests" packaging_pypi_tests
  run_step "packaging: pypi checksum manifest shape" packaging_pypi_checksum_manifest_shape
  run_step "packaging: pypi dist ships checksum manifest" packaging_pypi_dist_contains_checksum_manifest
  run_step "packaging: npm wrapper pack dry-run" packaging_npm_pack_dry_run
  run_step "packaging: npm wrapper tests" packaging_npm_tests
  run_step "packaging: npm checksum manifest shape" packaging_npm_checksum_manifest_shape
  run_step "packaging: npm wrapper postinstall skip-download path" packaging_npm_postinstall_skip_download

  if [[ "$RUN_DIST" == "1" ]]; then
    run_step "make dist" make dist
  fi
}

main() {
  validate_scope

  case "$QA_SCOPE" in
    core)
      run_core
      ;;
    extended)
      run_extended
      ;;
    all)
      run_core
      run_extended
      ;;
  esac

  log
  log "QA matrix complete (scope=$QA_SCOPE)."
}

main "$@"
