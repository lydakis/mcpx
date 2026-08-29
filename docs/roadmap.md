# mcpx Roadmap

Updated: August 2026

mcpx turns MCP tool surfaces into composable Unix commands. The project stays
focused on tools, shell-native invocation, truthful schemas, native output, and
low warm-call overhead. It is not a general MCP host, package manager, agent
runtime, or application UI.

## Current Baseline

Released through `v1.0.2`:

- Stable command contract:
  - `mcpx` lists servers
  - `mcpx <server>` lists tools
  - `mcpx <server> <tool>` calls a tool
- Schema-aware flags, help, completion, and native response output.
- Explicit response caching with safe opt-in defaults.
- Stdio and Streamable HTTP transports behind a local daemon.
- Hardened daemon IPC, lifecycle, runtime-source invalidation, and cache
  identity behavior.
- Read-only discovery from common MCP client configurations.
- Ephemeral URL and manifest sources plus managed `mcpx add` configuration.
- Optional command shims and agent skill generation.
- Reuse of available Codex-hosted connector and OAuth credentials without
  taking ownership of those login flows.
- Release automation for GitHub, Homebrew, npm, and PyPI wrappers.

Main contains additional correctness and dependency work after `v1.0.2`. The
next maintenance release should publish that work before or alongside the
modern-protocol release.

## Compatibility Matrix

| Capability | Current | Target |
| --- | --- | --- |
| MCP `2025-11-25` | Supported | Preserve through negotiated fallback |
| MCP `2026-07-28` | Implemented on main | Expand conformance coverage |
| Stdio | Supported, stateful handshake | Modern and legacy negotiation |
| Streamable HTTP | Supported, stateful handshake | Stateless modern requests plus legacy fallback |
| Tool input schema | Truthful flag-or-JSON boundary | Expand schema corpus |
| Tool output | Structured and content-block output | Preserve arbitrary structured JSON and content blocks |
| Tool catalog freshness | Connection-local cache | Protocol TTL hints and change invalidation |
| Remote auth | Explicit, imported, and OS-stored OAuth | Expand conformance coverage |
| Multi Round-Trip Requests | Machine retry plus explicit TTY elicitation | Broaden interactive request types only if scope remains narrow |
| Tasks extension | Unsupported by the selected Go SDK | Adopt after official SDK support lands |
| MCP conformance suite | Pinned core tools gate | Expand through frozen 2026-07-28 requirements |

## Phase 11: Modern Protocol Foundation

Goal: speak MCP `2026-07-28` without regressing existing servers or the warm CLI
path.

- Select and isolate the Go SDK behind the existing `internal/mcppool` boundary.
- Negotiate `2026-07-28` first and fall back to `2025-11-25` and earlier peers.
- Support stateless `server/discover`, per-request metadata, standardized HTTP
  headers, and request cancellation.
- Report the actual mcpx build version as client identity.
- Preserve stdio process reuse and HTTP connection reuse where they still add
  value; do not invent protocol sessions on modern HTTP.
- Add the official MCP client conformance suite for modern and legacy versions.
- Keep `go test ./...`, `go vet ./...`, `go build ./...`, `make perf`, and the QA
  matrix green.

Acceptance:

- A modern-only HTTP server and a legacy-only HTTP server both work.
- Modern and legacy stdio servers both work.
- Protocol negotiation and fallback are covered by integration tests.
- No material warm-list or warm-call regression versus the release baseline.

## Phase 12: Truthful Schemas and Catalogs

Goal: never expose a simpler CLI contract than the server actually declared.

- Support JSON Schema 2020-12 composition used by tool inputs and outputs,
  including local references, unions, intersections, and conditionals.
- Bound schema depth and validation work; never fetch external `$ref` targets.
- Generate flags only for schemas that map unambiguously to flags.
- Fall back to positional or stdin JSON for valid schemas that cannot be
  represented truthfully as flags.
- Preserve arbitrary JSON values in `structuredContent`.
- Respect `ttlMs` and `cacheScope` on catalog responses.
- Bound the outer catalog cache by modern `ttlMs` and a short legacy TTL. The
  selected SDK's eager subscription behavior is incompatible with conforming
  servers that omit `subscriptions/listen`; revisit event-driven outer-cache
  invalidation when the SDK can subscribe conditionally.
- Expose titles and annotations for display and diagnostics only. Never treat
  untrusted annotations as authorization or cache policy.

Acceptance:

- Composition, local-reference, recursive-depth, and external-reference cases
  have regression coverage.
- Help explicitly explains when JSON input is required.
- Catalog changes cannot remain hidden indefinitely on a warm daemon.

## Phase 13: Remote Authorization and Diagnostics

Goal: let a CLI user connect to a standards-compliant protected remote server
without placing bearer tokens in configuration files or shell history.

- Add a credential-provider boundary that preserves explicit headers and
  imported host credentials.
- Implement a narrow OAuth client flow using the system browser, PKCE, issuer
  validation, resource binding, Client ID Metadata Documents where supported,
  and secure OS credential storage.
- Keep tokens out of stdout, stderr, repository files, process arguments, and
  generated diagnostics.
- Add `mcpx doctor` for config origin, protocol/capability negotiation,
  prerequisites, transport health, and redacted auth-source diagnostics.
- Keep enterprise-managed authorization and future workload identity behind the
  same provider boundary rather than embedding an identity platform in mcpx.

Acceptance:

- Public, explicit-header, imported-host, and first-class OAuth servers work.
- Issuer mix-up, redirect, refresh, cancellation, and credential-change cases
  have regression coverage.
- `mcpx doctor` never prints credential values.

## Phase 14: Long-Running and Interactive Calls

Goal: map modern MCP interaction primitives onto predictable shell behavior.

- Multi Round-Trip Requests:
  - default non-interactive calls fail closed with an actionable input-required
    result
  - explicit interactive mode may prompt on a TTY
  - machine-readable input responses support agent-controlled retries.
- Tasks extension is deliberately gated on official Go SDK support. Do not
  duplicate the SDK's private transport or ship an unversioned custom protocol
  path. Once supported, add wait, detach, status, result, update, and cancel as
  one reviewed slice.
- Preserve stdout as the final native tool result and retain existing exit-code
  semantics.

Acceptance:

- Confirmation, missing-input, cancellation, resume, success, and failure paths
  have end-to-end tests.
- Non-interactive automation never blocks waiting for a terminal prompt.
- Tasks acceptance is tracked separately after the SDK gate clears.

## Phase 15: Adoption and Release Discipline

- Publish the maintenance backlog, then ship modern protocol support as the next
  minor release.
- Maintain a dated protocol/SDK compatibility matrix in release notes.
- Expand the pinned official conformance gate from `tools_call` through the
  frozen 2026-07-28 client requirements. Keep missing scenarios visible rather
  than baselining them as successes.
- Add copy/paste issue templates for setup, schema, auth, and server-compatibility
  reports.
- Track first-call success and setup friction through opt-in user reports, not
  telemetry in the binary.
- Revisit official Server Card discovery when its conventions stabilize.

## Watch, Do Not Build Yet

- Progressive tool discovery: follow the MCP specification work and consume the
  standard once stable.
- Server Cards and `.well-known` discovery: prefer the ecosystem convention over
  a proprietary registry.
- Workload identity, DPoP, and delegation: keep the auth boundary ready without
  preempting the standards.
- Tasks: watch official Go SDK support and the optional extension conformance
  scenarios before adding CLI surface.

## Explicit Non-Goals

- A general MCP resources, prompts, or Apps client.
- Roots, Sampling, or Logging implementation for new protocol versions; these
  features are deprecated.
- Legacy HTTP+SSE expansion.
- Full package-manager behavior or execution of remote installer scripts.
- Cross-client config writeback.
- Agent identity, orchestration, or an embedded LLM.
