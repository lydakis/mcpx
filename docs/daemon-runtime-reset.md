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

## Protocol And Credential Correction Slice (2026-08-28)

This slice fixes the current review findings without adding more mixed policy
to the request path:

1. OAuth login and logout close every connection that shares the credential
   identity and invalidate the response cache. Credential transitions are rare,
   so a global cache invalidation is preferable to retaining legacy entries that
   cannot be safely attributed to an account.
2. MCP tool discovery follows every pagination cursor through one shared helper,
   rejects cursor cycles, and applies the shortest page TTL to the assembled
   catalog.
3. JSON-mode calls retain cold-daemon cache hits. Flag-mode calls validate the
   live input schema before reading the response cache, so cached output cannot
   bypass an unsafe-schema rejection.
4. Direct HTTP probing recognizes the first complete SSE response event without
   waiting for stream EOF and always closes the response before terminating the
   probe session.
5. Auth status reports a stored session as authenticated only when it has a
   currently valid access token or a refresh token with a usable token endpoint.

Add focused regressions first, then run the full test, vet, build, performance,
and core conformance gates. If another P1 appears in these same paths, stop this
slice and revisit the cache/session ownership boundaries before patching again.

## Reset After Repeated P1 (2026-08-29)

The response-cache generation alone is not an invalidation boundary. Cache
ownership moves to a fail-closed guard with these rules:

- A clear attempt disables reads and writes before touching disk.
- Only a successful clear advances the generation and re-enables the cache.
- A failed clear leaves the cache disabled until a later clear succeeds.
- Requests retain their starting generation, so work started before a successful
  clear cannot read or write entries in the new credential epoch.

The protocol fixes remain bounded at their owning seams: direct URL probing
performs `server/discover` before legacy `initialize` and carries the negotiated
legacy version into session cleanup; tool pagination enforces page and tool
budgets; schema analysis rejects untyped values that flag coercion cannot
represent; malformed multi-round-trip input is classified as invalid user
input. Add one regression per invariant before implementation, then rerun all
repository gates and compare the daemon hot-path allocation baseline.

### Orphan credential-transition recovery correction

Runtime recovery belongs to the daemon, not the disk-cache package. The cache
package may identify unlocked transition markers, but it must not clear entries
or remove markers while requests are active. The daemon recovers an orphan in
this order: perform a guarded cache clear, remove the marker, then release the
matching pool fence. Any failure leaves the remaining marker or fence in place
so credential-bound reads and writes stay blocked. A regression must cover a
CLI process dying after daemon preparation and prove that pre-recovery work
cannot publish into the new cache generation.

## OAuth Issuer And Catalog Semantics Reset (2026-08-29)

OAuth client registration is an issuer-bound credential. New sessions persist
the issuer validated during authorization, restored sessions pass that issuer
back to the SDK, and legacy sessions without a binding fail closed with
logout/login guidance. Token refreshes preserve the same issuer binding.

Paginated tool TTLs are absolute receipt-time deadlines, not durations applied
after catalog assembly. The assembled catalog uses the earliest page deadline.
Schema analysis and shell completion share one flag-safety boundary: only
flag-representable JSON types produce property flags, while JSON-only schemas
offer global invocation flags only.

Add one focused regression for each boundary before implementation, then rerun
the full test, vet, build, and performance gates.

## Credential Writer Quiescence Reset (2026-08-29)

Closing an MCP session is not proof that the SDK has joined every background
HTTP reconnect. Credential safety therefore lives at the persistent token
writer, not only at the transport:

- every daemon OAuth handler owns a revocable write guard;
- prepare revokes the guard and waits for any active store write before it
  acknowledges credential mutation;
- the pool tracks detached connections with their immutable credential
  identity until their writers and close paths finish; credential transitions
  wait only for matching retirees, including matching aliases removed by config
  synchronization;
- active transition fences follow the credential identity across config renames
  and request-ephemeral aliases, while unrelated servers remain available with
  response caching disabled;
- current credential aliases remain fenced until response-cache invalidation
  succeeds;
- failed commands release an acknowledged transition by reloading the current
  credential state, while pre-ack rejection removes only that caller's marker.

Ordinary config reset stays nonblocking. Add regressions for late background
writes, reset-before-prepare, removed aliases, and failed-transition cleanup
before implementation.
