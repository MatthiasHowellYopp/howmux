# Architect Completion: Issue #19

**Status**: Design Complete
**Timestamp**: 2026-09-09T15:36:34-04:00

## Design Specification Created

Location: `.howmux/specs/issue-19-rename-howmux-to-howmux.md`

## Summary

Comprehensive design specification created for atomic rename of project from `howmux` to `howmux`. The specification covers:

### Scope Analysis
- **Files Affected**: 240+ files across entire codebase
- **Categories**: Go module/imports, directories, configuration, environment variables, documentation, templates, CI/CD

### Task Breakdown (2 Phases)
**Phase 1: Core Rename** (can parallelize internally)
- Task 1.1: Go module and source code
- Task 1.2: Directory structure and binary names
- Task 1.3: Configuration files
- Task 1.4: Environment variables (writer + reader pairs)

**Phase 2: Documentation and Templates** (depends on Phase 1)
- Task 2.1: Documentation updates
- Task 2.2: Template-synchronized files
- Task 2.3: CI/CD workflows

**Phase 3: Final Verification**
- Comprehensive grep verification
- Full test suite execution
- Binary smoke testing

### Critical Surfaces Enumerated
1. **Go Module**: `github.com/jbrinkman/howmux` → `github.com/matthiashowellyopp/howmux`
2. **Directory Structure**: `cmd/howmux/` → `cmd/howmux/`, `.howmux/` → `.howmux/`
3. **Binary Name**: `howmux` → `howmux`
4. **Environment Variables**: `KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID` (writer + reader verified)
5. **Configuration Files**: `.gitignore`, `Taskfile.yml`, `.releaserc.json`, `package.json`
6. **CI/CD**: `.github/workflows/*.yml` (artifact names, paths)
7. **Template Sync**: Agent configs, scripts, themes, evals (source + destination)

### Acceptance Criteria Highlights
- All writer/reader env var pairs move together
- Configuration files explicitly enumerated (gitignore, Taskfile, release config)
- Template synchronization verified via `task sync:check`
- Grep-checkable verification commands provided
- Full test suite must pass (no functionality changes)
- Binary smoke test required

### Verification Commands
- Comprehensive grep for stray references
- Module integrity check (`go mod tidy`)
- Template sync check (`task sync:check`)
- Full test suite (`task test`)
- Build and smoke test (`./dist/howmux --version`, `init`)

## Concurrency Analysis

No concurrency concerns introduced. This is a pure mechanical text replacement that:
- Does not add new goroutine boundaries
- Does not modify lock-guarded types
- Does not change TUI render path access patterns
- Preserves all existing concurrent access guarantees

## Ready for Implementation

The specification provides:
- Complete file surface enumeration
- Atomic task breakdown with clear dependencies
- Explicit acceptance criteria for each task
- Verification commands for completeness checking
- Risk mitigation strategies

Builder can proceed with implementation following the task sequence.
