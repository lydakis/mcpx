#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COUNT="${COUNT:-500}"
TARGET_SECONDS="${TARGET_SECONDS:-2.8}"

TMP_DIR="$(mktemp -d "/tmp/mcpx-loop-perf.XXXXXX")"
BASE_DIR="$TMP_DIR/base"
CURRENT_BIN="$TMP_DIR/mcpx-current"
BASE_BIN="$TMP_DIR/mcpx-base"

cleanup() {
  git worktree remove --force "$BASE_DIR" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

measure_loop() {
  local bin="$1"
  local label="$2"
  local run_dir="$TMP_DIR/$label"
  local home="$run_dir/home"
  local xdg_config_home="$run_dir/xdg-config"
  local runtime="$run_dir/runtime"
  mkdir -p "$home" "$xdg_config_home" "$runtime"

  # Warm up daemon outside timing window.
  HOME="$home" XDG_CONFIG_HOME="$xdg_config_home" XDG_RUNTIME_DIR="$runtime" "$bin" --json >/dev/null 2>&1

  local elapsed
  elapsed=$(
    {
      HOME="$home" XDG_CONFIG_HOME="$xdg_config_home" XDG_RUNTIME_DIR="$runtime" \
        /usr/bin/time -p bash -lc 'for i in $(seq 1 '"$COUNT"'); do "$0" --json >/dev/null; done' "$bin"
    } 2>&1 | awk '/^real /{print $2}'
  )

  printf '%s\n' "$elapsed"
}

echo "Building current binary"
go build -o "$CURRENT_BIN" ./cmd/mcpx

if [[ $# -eq 0 ]]; then
  current_time="$(measure_loop "$CURRENT_BIN" current)"
  printf 'warm loop: mcpx --json x%s\n' "$COUNT"
  printf 'current: %ss\n' "$current_time"
  if awk -v t="$current_time" -v target="$TARGET_SECONDS" 'BEGIN{exit !(t <= target)}'; then
    printf 'target: PASS (<= %ss)\n' "$TARGET_SECONDS"
  else
    printf 'target: MISS (<= %ss)\n' "$TARGET_SECONDS"
  fi
  exit 0
fi

BASE_REF="$1"
echo "Preparing baseline worktree for ref: $BASE_REF"
git worktree add --detach "$BASE_DIR" "$BASE_REF" >/dev/null

echo "Building baseline binary ($BASE_REF)"
(cd "$BASE_DIR" && go build -o "$BASE_BIN" ./cmd/mcpx)

echo "Running warm loop: mcpx --json x$COUNT"
baseline_time="$(measure_loop "$BASE_BIN" baseline)"
current_time="$(measure_loop "$CURRENT_BIN" current)"

improvement_pct="$(awk -v b="$baseline_time" -v c="$current_time" 'BEGIN{printf "%.2f", ((b-c)/b)*100}')"
speedup="$(awk -v b="$baseline_time" -v c="$current_time" 'BEGIN{printf "%.3f", b/c}')"

printf 'baseline (%s): %ss\n' "$BASE_REF" "$baseline_time"
printf 'current: %ss\n' "$current_time"
printf 'improvement: %s%% (speedup %sx)\n' "$improvement_pct" "$speedup"
if awk -v t="$current_time" -v target="$TARGET_SECONDS" 'BEGIN{exit !(t <= target)}'; then
  printf 'target: PASS (<= %ss)\n' "$TARGET_SECONDS"
else
  printf 'target: MISS (<= %ss)\n' "$TARGET_SECONDS"
fi
