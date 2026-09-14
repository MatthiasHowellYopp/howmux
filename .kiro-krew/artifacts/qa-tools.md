# QA Tools Discovery

**Project**: howmux (module `github.com/matthiashowellyopp/howmux`)
**Discovered**: 2026-09-14T09:56:01-04:00

## Formatting Checks
- `task fmt:check` — `.github/workflows/ci.yml` (step "Format check") → `Taskfile.yml` (`test -z "$(gofmt -l .)"`)
- `task fmt` — `Taskfile.yml` (auto-fix: `go fmt ./...`)

## Linting Checks
- `task lint` — `.github/workflows/ci.yml` (step "Lint") → `Taskfile.yml` (`go vet ./...`)

## Template Sync Checks
- `task sync:check` — `.github/workflows/ci.yml` (step "Template sync check") → `Taskfile.yml` (compares `.howmux/` scripts/themes/evals and agent JSON against `cmd/howmux/templates/`)

## Tests
- `task test` — `.github/workflows/ci.yml` (step "Test") → `Taskfile.yml` (`go test -v -race -coverprofile=coverage.out ./...`)

## Build
- `task build` — `.github/workflows/ci.yml` (step "Build") → `Taskfile.yml` (`go build ... ./cmd/howmux`)

## Notes
- CI is Go-based via Task. `package.json` contains only semantic-release devDependencies — no JS/TS QA scripts.
- No linter beyond `go vet` is configured (no `.golangci.yml` present).

## All QA Commands (execution order)
1. `task fmt:check`
2. `task sync:check`
3. `task lint`
4. `task test`
5. `task build`
