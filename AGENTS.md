# AGENTS.md

Owner: George Lydakis (`@lydakis`)

## Scope
- This file applies to this repository.
- Keep implementation aligned with [docs/design.md](docs/design.md).

## Working Style
- Keep diffs small and reviewable.
- Favor root-cause fixes over patches.
- For behavior changes and bug fixes, add/adjust tests first when practical.
- Preserve the command contract unless intentionally changed:
  - `mcpx` lists servers
  - `mcpx <server>` lists tools
  - `mcpx <server> <tool>` calls tool

## Quality Gates
- Run `go test ./...` for changed scope.
- Run `go vet ./...`.
- Run `go build ./...`.
- If any gate cannot run, document why in handoff.

## Architecture Boundaries
- `internal/cli`: CLI argument parsing and UX surface only.
- `internal/daemon` + `internal/ipc`: lifecycle, socket auth, dispatch.
- `internal/mcppool`: MCP transport/session concerns only.
- `internal/response`: MCP result unwrapping/mapping only.
- `internal/cache`: cache keying and TTL storage only.

## Notes
- Roadmap is tracked in [docs/roadmap.md](docs/roadmap.md).
- Avoid feature creep beyond the design fundamentals unless approved.
