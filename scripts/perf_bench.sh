#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COUNT="${BENCH_COUNT:-8}"
BENCH_RE="${BENCH_RE:-Benchmark(ListTools(Cold|Hot)|ToolInfoByNameHot|CallToolWithInfo|CompileJSONArgs|NonceValidationSurfaces|SpawnOrConnectHotExistingDaemon|Run(ServerToolListHotPath|RootJSONHotPath))}"
PKGS=(./internal/mcppool ./internal/daemon ./internal/cli)

run_bench() {
  go test "${PKGS[@]}" -run '^$' -bench "$BENCH_RE" -benchmem -count "$COUNT"
}

has_benchmarks() {
  grep -q '^Benchmark' "$1"
}

if [[ $# -eq 0 ]]; then
  echo "Running mcpx benchmark suite (count=$COUNT)"
  run_bench
  exit 0
fi

BASE_REF="$1"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mcpx-perf.XXXXXX")"
BASE_DIR="$TMP_DIR/base"

cleanup() {
  git worktree remove --force "$BASE_DIR" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "Preparing baseline worktree for ref: $BASE_REF"
git worktree add --detach "$BASE_DIR" "$BASE_REF" >/dev/null

echo "Running baseline benchmarks ($BASE_REF)"
(
  cd "$BASE_DIR"
  run_bench
) >"$TMP_DIR/base.txt"

echo "Running current benchmarks (working tree)"
run_bench >"$TMP_DIR/current.txt"

if ! has_benchmarks "$TMP_DIR/base.txt" || ! has_benchmarks "$TMP_DIR/current.txt"; then
  echo
  echo "One side does not contain benchmark output for the selected regex:"
  echo "  $BENCH_RE"
  echo "Raw outputs are below."
  echo
  echo "--- baseline: $BASE_REF ---"
  cat "$TMP_DIR/base.txt"
  echo
  echo "--- current: working tree ---"
  cat "$TMP_DIR/current.txt"
  exit 0
fi

if command -v benchstat >/dev/null 2>&1; then
  echo
  benchstat "$TMP_DIR/base.txt" "$TMP_DIR/current.txt"
else
  echo
  echo "benchstat not found; showing raw outputs."
  echo "Install with: go install golang.org/x/perf/cmd/benchstat@latest"
  echo
  echo "--- baseline: $BASE_REF ---"
  cat "$TMP_DIR/base.txt"
  echo
  echo "--- current: working tree ---"
  cat "$TMP_DIR/current.txt"
fi
