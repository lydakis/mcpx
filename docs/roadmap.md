# mcpx Roadmap

This plan is derived from the current scaffold audit versus `docs/design.md`.

## Current Status (Feb 2026)
- ✅ Phase 0/1/2 are implemented with test coverage.
- ✅ Phase 3 and Phase 4 are implemented.
- ✅ Phase 5 fallback-source support is implemented (including Cline).
- ✅ Core Phase 6 testing slices (transport integration + daemon lifecycle smoke) are implemented.
- ✅ Final config validation pass is implemented with actionable errors.
- ✅ Release checklist and usage docs are added.
- ✅ `Makefile` includes `check` and `dist` targets for release workflows.
- ✅ `scripts/qa_matrix.sh` + `make qa` provide repeatable QA matrix checks.
- ✅ Release notes template is added for first tagged release.
- ✅ GoReleaser + GitHub Actions release automation is configured for Homebrew cask publishing.
- ✅ Host QA matrix pass completed via `make qa`.
- 🔲 Remaining major work: run final release pass with artifacts and cut first tagged release.

## Phase 0: Stabilize Contracts (first)
- ✅ Add tests for:
  - flag parsing and type coercion (`internal/cli`)
  - response unwrapping semantics (`internal/response`)
  - config loading/env expansion/fallback merge behavior (`internal/config`)
  - cache key + TTL behavior (`internal/cache`)
- ✅ Add daemon-spawn regression test coverage for lock behavior and stale socket handling.
- ✅ Define error mapping tests for exit codes 0/1/2/3.

## Phase 1: Correctness Gaps
- ✅ Implement daemon spawn lock (`daemon.lock`) to prevent duplicate daemon races.
- ✅ Enforce socket/auth hardening:
  - owner-only socket permissions
  - nonce validation and stale state/socket recovery
  - peer-UID validation where supported.
- ✅ Fix XDG runtime fallback to state dir (`$XDG_STATE_HOME/mcpx`) instead of cache run dir.
- ✅ Align keepalive default with design (60s sliding window).
- ✅ Implement transport vs usage vs tool-level error normalization.

## Phase 2: CLI Contract Completion
- ✅ Support documented global flags:
  - `--cache`, `--no-cache`, `-v/--verbose`, `-q/--quiet`, `--version`.
- ✅ Implement tool flag collision handling (`--tool-*`) and `--` separator.
- ✅ Support positional JSON args and stdin JSON input when no flags are provided.
- ✅ Support tool-name aliases (snake_case and kebab-case).

## Phase 3: Help and Discoverability
- ✅ Include output schema details in `--help` when `outputSchema` exists.
- ✅ Show explicit fallback message when output schema is absent.
- ✅ Expand help text with required/optional/default semantics and examples.
- ✅ Generate/manage man pages under XDG data path.
- ✅ Add shell completion generation (bash/zsh/fish).

## Phase 4: Caching Engine Integration
- ✅ Wire cache reads/writes into daemon `call_tool` path.
- ✅ Apply precedence rules:
  - CLI flags override tool config
  - tool config overrides server defaults
  - safe default is no cache unless explicitly enabled.
- ✅ Add no-cache denylist matching and per-tool overrides.
- ✅ Add verbose cache diagnostics on stderr.

## Phase 5: Configuration and Fallback Sources
- ✅ Add Cline fallback source (`cline_mcp_settings.json`) as read-only import.
- ✅ Merge fallback sources deterministically and document precedence.
- ✅ Validate configuration errors with actionable stderr messages.

## Phase 6: End-to-End Hardening
- ✅ Add integration tests for stdio and HTTP servers (happy path + failure path).
- ✅ Add smoke tests for daemon lifecycle and idle shutdown.
- ✅ Build release checklist (binary size, docs, examples, install notes).

## Immediate Next Sprint
1. Create `lydakis/homebrew-mcpx` tap repo (if not already created).
2. Set `GORELEASER_TOKEN` in GitHub Actions with access to source + tap repos.
3. Run final release QA pass (`make qa` + `goreleaser release --snapshot --clean`).
4. Push first release tag and verify cask update lands in tap repo.
