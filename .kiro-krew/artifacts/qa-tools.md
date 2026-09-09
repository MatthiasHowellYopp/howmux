# QA Tools Discovery

**Project**: howmux
**Discovered**: 2026-09-09T15:40:33-04:00

## Formatting Checks
- `task fmt:check` — Taskfile.yml (`fmt:check`), .github/workflows/ci.yml (Format check step) — runs `test -z "$(gofmt -l .)"`
- `task fmt` — Taskfile.yml (`fmt`) — auto-format via `go fmt ./...` (fix mode, not check-only)

## Linting Checks
- `task lint` — Taskfile.yml (`lint`), .github/workflows/ci.yml (Lint step) — runs `go vet ./...`

## Template Sync Check
- `task sync:check` — Taskfile.yml (`sync:check`), .github/workflows/ci.yml (Template sync check step) — verifies template-synchronized files match live counterparts

## Tests
- `task test` — Taskfile.yml (`test`), .github/workflows/ci.yml (Test step) — runs `go test -v -race -coverprofile=coverage.out ./...` then generates HTML coverage

## Build
- `task build` — Taskfile.yml (`build`), .github/workflows/ci.yml (Build step) — builds with version metadata

## All QA Commands (execution order)

This mirrors the CI `validate` job order in `.github/workflows/ci.yml`:

1. `task fmt:check`
2. `task sync:check`
3. `task lint`
4. `task test`
5. `task build`

## Notes
- Go version: 1.25.0 (per CI `setup-go`); module target in go.mod
- No dedicated static-analysis linter (e.g., golangci-lint) is configured — linting is `go vet` only via `task lint`
- `task sync:check` is project-specific: it validates that `.howmux/` template files stay in sync with `cmd/howmux/templates/`. It can fail even when code is correct if templates drift — see `.kiro/skills/builder-conventions/SKILL.md` for the fix commands
- Tests run with `-race`, so a race detector build is required (CGO). The coverage HTML step writes `coverage.html`
