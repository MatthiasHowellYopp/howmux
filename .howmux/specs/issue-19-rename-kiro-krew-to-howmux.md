# Issue #19: Rename kiro-krew to howmux

**Closes #19**

## Problem Statement

This repository is an R&D fork where experimentation and divergence from upstream projects are expected. The current name "kiro-krew" creates confusion because it shares the name with the original upstream project. A distinct name clarifies that this is an independent experimental branch.

## Solution Approach

This is a complete atomic rename operation affecting all references to "kiro-krew" throughout the codebase. The rename must be executed atomically to avoid any broken intermediate states where some files reference the old name and others the new name.

### Rename Scope

**Affected Components:**
1. **Go module path**: `github.com/jbrinkman/kiro-krew` → `github.com/matthiashowellyopp/howmux`
2. **Binary name**: `kiro-krew` → `howmux`
3. **Command directory**: `cmd/kiro-krew/` → `cmd/howmux/`
4. **Config directory**: `.kiro-krew/` → `.howmux/`
5. **Internal string references**: All CLI text, error messages, help text
6. **Documentation**: README, docs/, CONTRIBUTING, CHANGELOG
7. **Build configuration**: Taskfile.yml, GitHub Actions workflows
8. **Package metadata**: package.json, go.mod
9. **Agent configurations**: References in `.kiro/agents/`
10. **Scripts**: Shell scripts in `scripts/` and `.kiro-krew/scripts/` (moving to `.howmux/scripts/`)

**Statistics:**
- Approximately 242 files contain "kiro-krew" references
- 1,291 total occurrences across the codebase
- File types affected: `.go`, `.md`, `.yaml`, `.yml`, `.json`, `.sh`

### Atomic Execution Strategy

The rename will be executed in a single coordinated pass using automated find-and-replace combined with directory/file moves to ensure atomicity. No intermediate commits - all changes in one commit.

### Concurrency Considerations

This change does not introduce new cross-goroutine state access. It is purely a naming refactor that preserves all existing concurrency boundaries and locking patterns. No new accessor methods or lock-guarded operations are required.

## Relevant Files

### Files to Rename/Move

**Directories:**
- `cmd/kiro-krew/` → `cmd/howmux/`
- `.kiro-krew/` → `.howmux/` (entire directory tree)

**Files:**
- `Taskfile.yml` - Update binary name variable and build commands
- `package.json` - Update name field
- `go.mod` - Update module declaration
- `.releaserc.json` - Update any project name references

### Files with Content Updates (partial list - systematic search required)

**Documentation (high priority):**
- `README.md` - All references to project name, installation instructions, URLs
- `CONTRIBUTING.md` - Project name references
- `CHANGELOG.md` - Update header and references
- `docs/*.md` - All documentation files

**Go source files (~150+ files):**
- All Go files in `cmd/`, `internal/` with import paths
- All test files with import paths and string references

**Configuration:**
- `.kiro-krew/config.yaml` → `.howmux/config.yaml` (path and label references)
- `.kiro/agents/*.json` - Update description fields if they contain project name
- `.kiro/agents/*.md` - Update any prose references

**CI/CD:**
- `.github/workflows/ci.yml` - Binary name, paths
- `.github/workflows/release.yml` - Binary names, artifact names, release asset names

**Scripts:**
- All shell scripts in `scripts/` and `.kiro-krew/scripts/` that reference the project name
- Update shebangs or paths that reference config directory

## Team Orchestration

This is a single-agent task - the **builder** will execute all rename operations atomically. The operations are highly parallelizable at the file level but must be committed together.

**Execution approach:**
1. Builder performs systematic search-and-replace across all affected files
2. Builder renames directories and moves files
3. Builder verifies build still works
4. All changes committed in a single atomic commit

**No dependencies between subtasks** - the systematic replacement can process files in any order, but all must complete before verification.

## Step-by-Step Task Breakdown

### Task 1: Rename Directory Structure
**Description:** Move directories to new names  
**Acceptance Criteria:**
- `cmd/kiro-krew/` renamed to `cmd/howmux/`
- `.kiro-krew/` renamed to `.howmux/`
- Verify no broken symlinks or references
- Git tracks the rename (use `git mv` for proper rename tracking)

**Dependencies:** None

---

### Task 2: Update Go Module and Package Imports
**Description:** Update the Go module declaration and all import statements  
**Acceptance Criteria:**
- `go.mod` module declaration updated to `github.com/matthiashowellyopp/howmux`
- All `import` statements updated from `github.com/jbrinkman/kiro-krew` to `github.com/matthiashowellyopp/howmux`
- All internal package imports reflect new module path
- `go.sum` regenerated with `go mod tidy`
- `go build ./...` succeeds without errors

**Implementation Notes:**
- Use systematic find-and-replace: `github.com/jbrinkman/kiro-krew` → `github.com/matthiashowellyopp/howmux`
- Update in all `.go` files across `cmd/`, `internal/` directories

**Dependencies:** Task 1 (directory rename must happen first)

---

### Task 3: Update Build Configuration
**Description:** Update Taskfile and build-related configuration  
**Acceptance Criteria:**
- `Taskfile.yml`: `BINARY_NAME` variable changed from `kiro-krew` to `howmux`
- Build command targets updated to `./cmd/howmux`
- ldflags in build tasks updated to new module path
- `task build` produces `dist/howmux` binary
- `task dev` produces `howmux` binary in project root
- All task commands execute successfully

**Files to modify:**
- `Taskfile.yml`

**Dependencies:** Task 1, Task 2

---

### Task 4: Update Package Metadata
**Description:** Update package.json and release configuration  
**Acceptance Criteria:**
- `package.json`: `name` field changed to `howmux`
- `package.json`: `description` updated to reference "howmux"
- `.releaserc.json`: Any project-specific references updated

**Dependencies:** None (can run in parallel with other tasks)

---

### Task 5: Update GitHub Actions Workflows
**Description:** Update CI/CD workflows for new binary names and paths  
**Acceptance Criteria:**
- `.github/workflows/ci.yml`: All references to `kiro-krew` binary updated to `howmux`
- `.github/workflows/release.yml`: Binary names updated in build, verify, and release steps
- Release artifact names updated: `howmux`, `howmux-linux-amd64`, `howmux-linux-arm64`
- Workflow file references to config directory updated from `.kiro-krew` to `.howmux`
- All workflow syntax remains valid

**Dependencies:** Task 1

---

### Task 6: Update Configuration Files
**Description:** Update YAML configuration files  
**Acceptance Criteria:**
- `.howmux/config.yaml`: `label` field changed from `kiro-krew` to `howmux` (or remains configurable)
- `.howmux/config.yaml`: `sessions_dir` updated to `.howmux/sessions`
- `.howmux/config.yaml`: `log_dir` updated to `.howmux/logs`
- All theme files in `.howmux/themes/` remain functional
- Configuration loads correctly at runtime

**Dependencies:** Task 1 (directory must be renamed first)

---

### Task 7: Update Shell Scripts
**Description:** Update all shell scripts in scripts/ and .howmux/scripts/  
**Acceptance Criteria:**
- All references to `.kiro-krew` directory updated to `.howmux`
- All references to `kiro-krew` binary updated to `howmux`
- Scripts in `.howmux/scripts/`: `worktree-create.sh`, `worktree-merge.sh`, `planning-worktree-*`
- Scripts remain executable and functional
- No broken path references

**Files to update:**
- `scripts/*.sh`
- `.howmux/scripts/*.sh`
- `test_integration.sh`
- `validate_integration.sh`

**Dependencies:** Task 1

---

### Task 8: Update Documentation
**Description:** Comprehensive documentation update  
**Acceptance Criteria:**
- `README.md`: All occurrences of "Kiro Krew" changed to "Howmux"
- `README.md`: All occurrences of "kiro-krew" changed to "howmux"
- `README.md`: Installation URLs updated to reference `howmux` binary
- `README.md`: Example commands updated to use `howmux` instead of `kiro-krew`
- `CONTRIBUTING.md`: Project name references updated
- `CHANGELOG.md`: Header updated, historical entries preserved
- All docs/ files updated:
  - `docs/evaluation.md`
  - `docs/agent-conventions.md`
  - `docs/hotkey-toggle.md`
  - `docs/unified-container-flow.md`
  - `docs/eval-framework-analysis.md`
  - `docs/performance-investigation.md`
- Agent prompt files in `.kiro/agents/*.md`: Update prose that references project name
- Specification files in `.howmux/specs/*.md`: Update references in templates/examples (existing specs remain as historical record)

**Dependencies:** Task 1

---

### Task 9: Update Agent Configurations
**Description:** Update agent JSON configs and prompts  
**Acceptance Criteria:**
- `.kiro/agents/*.json`: Update `description` fields if they contain "kiro-krew"
- `.kiro/agents/*-prompt.md`: Update any references to config directory or binary name
- Agent configurations remain valid JSON
- Agents can still load and execute

**Dependencies:** Task 1

---

### Task 10: Update User-Facing Strings
**Description:** Update CLI help text, prompts, and error messages in Go source  
**Acceptance Criteria:**
- All user-facing strings in `cmd/howmux/cmd/*.go` updated
- Help text references updated from "kiro-krew" to "howmux"
- Error messages and log output updated
- Version output and about information updated
- No remaining user-visible references to old name

**Files to scan:**
- `cmd/howmux/cmd/*.go` - All command files
- `internal/tui/*.go` - TUI display strings
- `internal/watcher/*.go` - Watcher messages
- `internal/agent/*.go` - Agent-related messages

**Dependencies:** Task 1, Task 2

---

### Task 11: Update Test Files
**Description:** Update test fixtures and test assertions  
**Acceptance Criteria:**
- All test files import paths updated (covered by Task 2)
- Test fixtures in `.howmux/evals/fixtures/*` - path references updated
- Test assertions checking binary names or paths updated
- All tests pass: `go test ./...`

**Dependencies:** Task 2, Task 6

---

### Task 12: Verify and Validate
**Description:** Comprehensive verification that all changes work  
**Acceptance Criteria:**
- `go mod tidy` completes successfully
- `go build ./...` succeeds
- `task build` produces working `howmux` binary
- `./dist/howmux --version` executes successfully
- `./dist/howmux --help` shows correct binary name
- `go test ./...` passes all tests
- `task test` passes with coverage report
- Grep verification: `grep -r "kiro-krew" . --exclude-dir=.git --exclude-dir=.howmux/specs --exclude="*.md"` returns only acceptable historical references (CHANGELOG, old spec files)
- Documentation spot-check: README installation instructions work

**Validation Commands:**
```bash
# Module validation
go mod tidy
go build ./...

# Build validation
task build
./dist/howmux --version
./dist/howmux --help
./dist/howmux init  # Should create .howmux/ directory

# Test validation
go test ./...
task test

# Reference validation (should find minimal results)
grep -r "kiro-krew" . \
  --exclude-dir=.git \
  --exclude-dir=.howmux/specs \
  --exclude="CHANGELOG.md" \
  --include="*.go" \
  --include="*.yaml" \
  --include="*.yml" \
  --include="*.json" \
  --include="*.sh"

# Config directory validation
test -d .howmux && echo ".howmux exists" || echo "ERROR: .howmux missing"
test ! -d .kiro-krew && echo ".kiro-krew removed" || echo "WARNING: .kiro-krew still exists"
```

**Dependencies:** All previous tasks (1-11)

---

## Implementation Notes

### Systematic Replacement Strategy

Use the following find-and-replace operations across the appropriate file types:

1. **Module path**: `github.com/jbrinkman/kiro-krew` → `github.com/matthiashowellyopp/howmux` (in `.go` files)
2. **Directory path**: `.kiro-krew` → `.howmux` (in all file types)
3. **Binary name**: `kiro-krew` → `howmux` (context-sensitive: avoid replacing in URLs until repo is also renamed)
4. **Prose name**: `Kiro Krew` → `Howmux` (in documentation)

### Files to Exclude from Replacement

- `.howmux/specs/*.md` - Historical specs should preserve original context
- `CHANGELOG.md` - Historical entries should remain accurate
- `.git/` directory
- Binary files

### Git Commit Strategy

One atomic commit with clear message:
```
Rename project from kiro-krew to howmux

- Rename cmd/kiro-krew -> cmd/howmux
- Rename .kiro-krew -> .howmux  
- Update Go module path: github.com/jbrinkman/kiro-krew -> github.com/matthiashowellyopp/howmux
- Update all import statements
- Update binary name in Taskfile.yml and CI workflows
- Update documentation and user-facing strings
- Update configuration files and scripts

Closes #19
```

### Post-Merge Considerations

After merge, the following external updates will be needed (outside PR scope):
- Update repository description on GitHub
- Consider updating GitHub repository name if desired
- Update any external documentation or wikis
- Notify users of the rename

## Validation Commands

See Task 12 acceptance criteria for comprehensive validation commands.

Quick validation checklist:
```bash
# 1. Module compiles
go build ./...

# 2. Binary works
task build && ./dist/howmux --version

# 3. Tests pass
go test ./...

# 4. Config directory correct
test -d .howmux

# 5. No stray references (should be minimal)
grep -r "kiro-krew" . --exclude-dir=.git --exclude-dir=.howmux/specs --exclude="CHANGELOG.md"
```

## Risks and Mitigations

**Risk:** Missed references causing runtime failures  
**Mitigation:** Comprehensive grep-based verification in Task 12, all tests must pass

**Risk:** Git history confusion  
**Mitigation:** Use `git mv` for directory renames to preserve history, clear commit message

**Risk:** Breaking existing clones/forks  
**Mitigation:** This is unavoidable for a module path change. Document in commit message and CHANGELOG.

**Risk:** Broken paths in configuration  
**Mitigation:** Test `init` command to verify `.howmux/` directory creation works

**Risk:** CI/CD pipeline breaks  
**Mitigation:** Validate workflow YAML syntax, ensure artifact names are consistent
