#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GO_CMD="${GO_CMD:-go}"
BINARY="${BINARY:-./mcpx}"
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

main() {
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

  if [[ "$RUN_DIST" == "1" ]]; then
    run_step "make dist" make dist
  fi

  log
  log "QA matrix complete."
}

main "$@"
