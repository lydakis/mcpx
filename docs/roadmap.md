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
- ✅ Final release pass completed with artifacts and tagged releases shipped.
- 🔲 Next major work after first release: adoption-focused discovery and onboarding improvements that keep the core command contract unchanged.

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
- ✅ Preserve server-native tool names without client-side rewriting.

## Phase 3: Help and Discoverability
- ✅ Include output schema details in `--help` when `outputSchema` exists.
- ✅ Show explicit fallback message when output schema is absent.
- ✅ Expand help text with required/optional/default semantics and examples.
- ✅ Package root CLI man page (`mcpx.1`) for install-time availability.
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

## Immediate Next Sprint (Post-Release)
1. Define and ship `mcpx add` v1 as source-based remote bootstrap (install links + manifest URLs).
2. Add parsing/validation and clear prerequisite errors for missing runtimes (`docker`, `npx`, `uvx`, etc.).
3. Add overwrite confirmation semantics and regression tests for config writes.
4. Update `README.md` and `docs/usage.md` with end-to-end `mcpx add` examples.

### Phase 8 Execution Log (Now)
- Contract lock (`mcpx add` v1):
  - accepted inputs: install-link URL, manifest URL, direct MCP endpoint URL, local manifest file
  - out of scope: slug-only lookup, registry dependency, package/runtime installation.
- Parser + normalization:
  - parse install-link payloads and manifest payloads into one normalized internal shape
  - preserve server-native tool naming and transport semantics
  - reject malformed/ambiguous payloads with actionable errors.
- Validation and safety:
  - require either stdio (`command` + `args`) or URL transport config
  - prerequisite runtime checks (`docker`, `node/npx`, `uvx`, etc.) with clear stderr guidance
  - explicit overwrite confirmation for existing managed entries
  - atomic config writes only.
- Fixtures to add:
  - valid install-link fixture
  - valid manifest fixture (stdio)
  - valid manifest fixture (HTTP/URL)
  - invalid base64/install-link fixture
  - missing required transport fields fixture
  - unsupported transport fixture.
- Tests to add first:
  - parser table tests (success + failure corpus)
  - validator tests (required fields + runtime prerequisite errors)
  - config write tests (new entry, overwrite denied, overwrite confirmed, atomic write behavior)
  - CLI integration tests for exit mapping and user-facing error text.
- Docs updates:
  - quickstart examples for each input source
  - overwrite behavior and safety notes
  - explicit non-goals for v1 (`add` is bootstrap, not installer).

## Post-Release Direction (Adoption-First, Contract-Stable)

After first release, optimize for adoption without breaking the command surface:
- Keep core contract unchanged:
  - `mcpx` lists servers
  - `mcpx <server>` lists tools
  - `mcpx <server> <tool>` calls tool
- Prioritize discoverability and setup speed over feature breadth.
- Avoid turning `mcpx` into a general package manager in the near term.

## Phase 7: Early-User Feedback Loop
- Add a lightweight docs section with copy/paste issue templates:
  - server setup friction
  - confusing help/flags
  - missing examples.
- Add a `mcpx doctor` style checklist command proposal (design + acceptance tests first) to validate local prerequisites and config health.
- Instrument repeatable manual UX checks in `scripts/qa_matrix.sh` for:
  - fresh install
  - first server config
  - first successful tool call.

## Phase 7.5: Source Ownership Model (Before `mcpx add`)
- Formalize source behavior as `read-many, write-one`:
  - read from fallback sources for zero-config onboarding
  - write only to `mcpx` config by default.
- Define server ownership states:
  - `discovered`: imported from fallback sources (read-only from `mcpx` perspective)
  - `managed`: defined in `mcpx` config (including `mcpx add`).
- Define precedence and collisions:
  - `managed` entries always override `discovered` entries with the same name
  - collisions must be explicit in CLI output with source provenance.
- Add pollution controls while keeping auto-import enabled by default:
  - fallback source allowlist/denylist
  - optional server-level allowlist/denylist
  - workspace-local fallbacks preferred over global fallbacks.
- Keep cross-client writes out of Phase 8 scope:
  - no automatic writeback into Cursor/VS Code/Claude configs
  - any future sync/export remains explicit and opt-in.

## Phase 8: Remote Bootstrap (`mcpx add`) from Install Sources
- Define `mcpx add` as source-based bootstrap for servers not yet configured locally.
- Support explicit remote sources (no registry required in v1):
  - install links (Cursor-style deeplink/web install URLs)
  - direct manifest URLs (JSON/TOML payloads with MCP transport config)
  - direct MCP endpoint URLs (`https://.../mcp`) when no manifest is provided.
- Parse/validate remote payload and write normalized server entry to `config.toml`.
- Keep execution model unchanged:
  - `mcpx` still runs configured commands/URLs
  - no package installation or runtime management by default.
- Provide clear prerequisite checks/errors when referenced runtimes are missing (`docker`, `node/npx`, `uvx`, etc.).
- Require explicit confirmation before overwriting existing server entries.
- Ship regression tests for parsing, validation, and overwrite safeguards.

## Phase 9: Optional Registry Discovery Layer (Read-Only)
- Add registry command surface only after Phase 8 is stable:
  - `mcpx registry search <query>`
  - `mcpx registry info <id>`.
- Reuse the same manifest schema from Phase 8, adding stable identifiers (`id`/slug) for discovery.
- Start with curated registry sources (owned JSON/TOML manifests), no arbitrary script execution.
- Cache registry metadata with short TTL and explicit `--no-cache` override.

## Phase 10: Optional Command Shims (Experimental, Opt-In)
- Evaluate optional shim command surface only after Phases 7-9 signal demand:
  - `mcpx shim install <server>`
  - `mcpx shim remove <server>`
  - `mcpx shim list`.
- Shim behavior must be pass-through only (`mcpx <server> "$@"`), with collision-safe install and clear uninstall path.
- Keep shims disabled by default and document them as convenience wrappers, not MCP server installation.

## Deferred / Explicitly Out of Scope (for now)
- Full package-manager behavior (`mcpx install <server>` downloading arbitrary code).
- Automatic OAuth/account linking flows inside `mcpx`.
- Untrusted remote installer scripts.
- A separate `add --adopt` mode for importing already auto-discovered local servers.
