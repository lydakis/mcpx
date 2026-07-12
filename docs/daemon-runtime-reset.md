# Daemon Runtime Reset Plan

## Why

Daemon runtime, source invalidation, and cache changes produced repeated P1
regressions when implemented together in the request path. Preserve the product
goals, but rebuild around narrow subsystem ownership instead of adding more
conditions to `runtimeRequestHandler.handle()` or `callToolWithDeps()`.

## Non-Negotiable Invariants

### Runtime config

- Same-CWD warm requests remain in-memory when sources are unchanged.
- Config, fallback, auth, and OAuth credential changes invalidate the runtime
  view.
- Invalid config fails fast once observed.
- Watchers and fingerprints observe the same files and symlink targets.

### Cache behavior

- Requested tool name controls user-facing cache policy.
- Resolved backend identity controls storage and validation.
- Cold-daemon and keepalive-expired cache hits remain usable when backend
  metadata is temporarily unavailable.
- Cached aliases and virtual tools cannot hide removal or rename indefinitely.

## Target Architecture

### RuntimeConfigManager

Owns the active CWD, config snapshot, hash, source generation, reload decisions,
and fail-fast behavior. It does not own raw watcher or cache logic.

### SourceObserver

Owns file observation, reconciliation, and a monotonic dirty signal. The steady
state remains event-driven and request-path decisions remain in-memory.

### ToolCachePolicy

Owns requested-name enablement and TTL lookup only.

### ToolCacheIdentity

Owns canonical storage identity, alias metadata, and live-validation decisions.

## Execution

1. Preserve behavior tests from the reset branch.
2. Introduce subsystem seams without changing behavior.
3. Reimplement runtime invalidation and cache semantics behind those seams.
4. Validate with `go test ./...`, `go vet ./...`, `go build ./...`, and
   `make perf`.
5. Compare warm daemon listing and cached tool calls with the release baseline.

The preserved information branch is `codex/runtime-source-monitor-reset`. Treat
it as a source of regression tests and clarified invariants, not an
implementation to merge line-for-line.
