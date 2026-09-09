# Design Specification: Rename Project from howmux to howmux

**Issue**: #19
**Status**: Design Complete
**Closes**: #19

## Solution Approach

This is an atomic mechanical rename across the entire codebase, transforming all references from `howmux` to `howmux`. The rename affects approximately 240+ files across multiple categories:

1. **Go module and package paths**: `github.com/jbrinkman/howmux` → `github.com/matthiashowellyopp/howmux`
2. **Binary and command names**: `howmux` → `howmux`
3. **Directory structure**: `cmd/howmux/` → `cmd/howmux/`, `.howmux/` → `.howmux/`
4. **Configuration paths**: All references to `.howmux/` → `.howmux/`
5. **Environment variables**: `KIRO_KREW_*` → `HOWMUX_*`
6. **Documentation and user-facing strings**: "Kiro Krew" → "Howmux"
7. **Build artifacts and CI/CD**: All job names, artifact names, task names
8. **Template-synchronized files**: Agent configs, scripts, themes, evals

**Critical constraint**: This is a pure naming refactor with NO functionality changes. All tests must continue to pass, and the application behavior remains identical.

### Concurrency Concerns

This rename does NOT introduce new concurrency concerns. It is a mechanical text replacement that:
- Does not add new goroutine boundaries
- Does not modify any types with `sync.Mutex` or `sync.RWMutex`
- Does not change how the TUI render path accesses shared state
- Does not alter command handler or background service interaction patterns

The rename preserves all existing locking patterns and concurrent access guarantees.

## Relevant Files

### 1. Go Module and Package Paths (Core)
- `go.mod` — module path declaration
- All `*.go` files with import statements (~100+ files)
- `internal/*/` packages — import path updates

### 2. Binary and Command Structure
- `cmd/howmux/` directory → `cmd/howmux/`
- `cmd/howmux/main.go`
- `cmd/howmux/cmd/*.go` — all cobra command files
- `cmd/howmux/templates/` directory structure

### 3. Configuration Files (CRITICAL - Often Missed)
- `.gitignore` — runtime artifact paths (`.howmux/` → `.howmux/`)
- `Taskfile.yml` — `BINARY_NAME` variable, task commands, build flags
- `.releaserc.json` — artifact paths and labels
- `package.json` — name, description, repository URL
- All config loading code referencing `.howmux/` paths

### 4. CI/CD Workflows
- `.github/workflows/ci.yml` — job names, commands
- `.github/workflows/release.yml` — artifact names, release asset labels, gh release commands
- Both manual and semantic-release paths

### 5. Documentation
- `README.md` — project title, description, installation commands, examples
- Any docs/ files (if present)
- Code comments mentioning "howmux" or "Kiro Krew"

### 6. Template-Synchronized Files
**Agent Configs** (`.kiro/agents/` and `cmd/howmux/templates/kiro/agents/`):
- `*.json` files — no howmux references expected in config
- `*-prompt.md` files — contains howmux in examples and paths

**Scripts** (`.howmux/scripts/` and `cmd/howmux/templates/howmux/scripts/`):
- `worktree-create.sh`
- `worktree-merge.sh`
- `planning-worktree-create.sh`
- `planning-worktree-cleanup.sh`

**Themes** (`.howmux/themes/` and `cmd/howmux/templates/howmux/themes/`):
- `*.yaml` theme files

**Evals** (`.howmux/evals/` and `cmd/howmux/templates/howmux/evals/`):
- `cases/*/*.yaml` — test case definitions
- `rubrics/*.yaml` — evaluation rubrics

### 7. Internal Package References
- `internal/config/config.go` — default paths (`.howmux/sessions`, `.howmux/logs`)
- `internal/templates/extract.go` — path transformation logic
- `internal/eval/*.go` — results directory paths (`.howmux/evals/`)
- `internal/agent/manager.go` — environment variable writer
- `internal/hotkey/detector.go` — environment variable reader
- All test files (`*_test.go`) — environment variable usage, temp directory names

### 8. Environment Variables (Writer + Reader Pairs)
**`KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID`**:
- Writer: `internal/agent/manager.go` (lines 267, 561)
- Reader: `internal/hotkey/detector.go` (line 20)
- Tests: `internal/hotkey/*_test.go` (10+ references)

### 9. Test Infrastructure
- All `*_test.go` files
- Test temp directory names (e.g., `howmux-test`)
- `validate_integration.sh`
- `test_integration.sh`

### 10. Build and Development Scripts
- `scripts/template-sync-summary.sh`
- Any other scripts in `scripts/` directory

## Team Orchestration

This is a single-pass mechanical rename that must be executed atomically. The implementation can be parallelized into two independent phases:

**Phase 1: Core Rename** (Backend + Build)
- Task 1.1: Go module, imports, and source code
- Task 1.2: Directory structure and file moves
- Task 1.3: Configuration files (Taskfile.yml, .releaserc.json, package.json, .gitignore)
- Task 1.4: Environment variables (writer + reader pairs)

**Phase 2: Documentation and Templates** (Frontend/Documentation)
- Task 2.1: Documentation (README.md, comments)
- Task 2.2: Template-synchronized files (agents, scripts, themes, evals)
- Task 2.3: CI/CD workflows

**Dependencies**: Phase 2 can only begin after Phase 1 completes (directory structure must exist before templates can be synced).

## Step-by-Step Task Breakdown

### Task 1.1: Go Module and Source Code Rename

**Objective**: Update the Go module path and all import statements.

**Steps**:
1. Update `go.mod`: `github.com/jbrinkman/howmux` → `github.com/matthiashowellyopp/howmux`
2. Find and replace all import paths in `*.go` files:
   ```bash
   find . -name "*.go" -type f -exec sed -i '' 's|github.com/jbrinkman/howmux|github.com/matthiashowellyopp/howmux|g' {} +
   ```
3. Update `Taskfile.yml` build flags (ldflags with version package path)
4. Run `go mod tidy` to verify module integrity

**Acceptance Criteria**:
- `go.mod` module declaration updated
- All import statements in `*.go` files updated
- `Taskfile.yml` ldflags updated with new package path
- `go mod tidy` completes without errors
- **Verification**: `grep -rn "github.com/jbrinkman/howmux" --include="*.go" .` returns zero matches

### Task 1.2: Directory Structure and Binary Names

**Objective**: Rename directories and update all path references.

**Steps**:
1. Rename `cmd/howmux/` to `cmd/howmux/`:
   ```bash
   git mv cmd/howmux cmd/howmux
   ```
2. Update `Taskfile.yml`:
   - `BINARY_NAME: howmux` → `BINARY_NAME: howmux`
   - All task commands referencing `./cmd/howmux` → `./cmd/howmux`
   - Build artifact names in release tasks
3. Update `internal/templates/extract.go`:
   - Path transformation logic: `"howmux"` → `"howmux"` (lines 29, 31)
   - Transform `".howmux"` → `".howmux"`
4. Update `cmd/howmux/cmd/root.go`:
   - `Use: "howmux"` → `Use: "howmux"`
   - Long description: "howmux is a" → "howmux is a"

**Acceptance Criteria**:
- Directory renamed: `cmd/howmux/` → `cmd/howmux/`
- `Taskfile.yml` `BINARY_NAME` variable updated
- All task commands reference correct directory
- `internal/templates/extract.go` path transformation updated
- Root command `Use` field updated
- **Verification**: `find . -type d -name "howmux"` returns zero matches (excluding .git, .worktrees)
- **Verification**: `grep -rn "cmd/howmux" Taskfile.yml` returns zero matches

### Task 1.3: Configuration Files

**Objective**: Update all configuration files with paths, artifact names, and metadata.

**Steps**:
1. Update `.gitignore`:
   - `/howmux` → `/howmux`
   - `/howmux-test` → `/howmux-test`
   - `/howmux-validate` → `/howmux-validate`
   - `/test-howmux` → `/test-howmux`
   - `cmd/howmux/howmux` → `cmd/howmux/howmux`
   - `.howmux/` → `.howmux/` (all 5 occurrences)
2. Update `.releaserc.json`:
   - All asset paths: `dist/release/howmux*` → `dist/release/howmux*`
   - All asset labels: `"howmux ..."` → `"howmux ..."`
3. Update `package.json`:
   - `"name": "howmux"` → `"name": "howmux"`
   - `"description"` updated to mention "Howmux"
   - `"url"` → `"https://github.com/matthiashowellyopp/howmux.git"`
4. Update `internal/config/config.go`:
   - Default label: `"howmux"` → `"howmux"` (line 57)
   - `SessionsDir: ".howmux/sessions"` → `".howmux/sessions"` (line 66)
   - `LogDir: ".howmux/logs"` → `".howmux/logs"` (line 72)
   - Config file path: `".howmux/config.yaml"` → `".howmux/config.yaml"` (line 77)
5. Update all `internal/eval/*.go` files:
   - `.howmux/evals/` → `.howmux/evals/` (results, cases, rubrics, tmp dirs)

**Acceptance Criteria**:
- `.gitignore` updated: all artifact and directory paths
- `.releaserc.json` updated: all asset paths and labels
- `package.json` updated: name, description, repository URL
- `internal/config/config.go` updated: default paths
- `internal/eval/*.go` files updated: all eval directory paths
- **Verification**: `grep -rn "\.howmux" --include="*.go" --include=".gitignore" . | grep -v "\.worktrees" | wc -l` returns zero
- **Verification**: `grep -rn "howmux" .gitignore .releaserc.json package.json` returns zero matches

### Task 1.4: Environment Variables (Writer + Reader Pairs)

**Objective**: Rename all environment variables and verify writer/reader pairs move together.

**Steps**:
1. Update environment variable writer in `internal/agent/manager.go`:
   - Line 267: `KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID`
   - Line 561: `KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID`
2. Update environment variable reader in `internal/hotkey/detector.go`:
   - Line 20: `KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID`
3. Update all test files in `internal/hotkey/*_test.go`:
   - `detector_test.go` (lines 10, 12, 16, 18, 22)
   - `error_handling_test.go` (lines 10, 15, 19)
   - `integration_test.go` (lines 65, 66, 128, 156, 335, 337, 355, 367, 368)
4. Update any other env var references in `internal/eval/selective_test.go`:
   - Line 13: temp dir name `howmux-test` → `howmux-test`

**Acceptance Criteria**:
- Environment variable `KIRO_KREW_WATCHER_PID` renamed to `HOWMUX_WATCHER_PID` in:
  - Writer: `internal/agent/manager.go` (both occurrences)
  - Reader: `internal/hotkey/detector.go`
  - All test files using the variable
- Temp directory names in tests updated
- **Verification**: `grep -rn "KIRO_KREW" --include="*.go" .` returns zero matches
- **Verification**: Writer and reader are both updated (verify by checking both files explicitly)

### Task 2.1: Documentation Updates

**Objective**: Update all user-facing documentation and code comments.

**Steps**:
1. Update `README.md`:
   - Title: `# Kiro Krew` → `# Howmux`
   - All occurrences of "Kiro Krew" → "Howmux"
   - All occurrences of "howmux" → "howmux"
   - Installation URLs: `github.com/jbrinkman/howmux` → `github.com/matthiashowellyopp/howmux`
   - Command examples: `howmux init` → `howmux init`
   - Configuration examples: `label: howmux` → `label: howmux`
2. Update code comments in all `*.go` files:
   - Find comments mentioning "howmux" or "Kiro Krew" and update
3. Update any other markdown files in docs/ (if present)

**Acceptance Criteria**:
- `README.md` fully updated: title, descriptions, commands, URLs
- All code comments updated
- **Verification**: `grep -rn "howmux" --include="*.md" . | grep -v ".worktrees" | grep -v "architect-prompt.md"` returns zero matches (architect-prompt.md contains the rename as an EXAMPLE, which should be preserved)
- **Verification**: Manual spot-check of README.md to ensure natural reading flow

### Task 2.2: Template-Synchronized Files

**Objective**: Update all template files and ensure template/live synchronization.

**Template directories**:
- Source: `cmd/howmux/templates/`
- Destination: `.kiro/agents/`, `.howmux/scripts/`, `.howmux/themes/`, `.howmux/evals/`

**Steps**:
1. Update agent prompt templates in `cmd/howmux/templates/kiro/agents/*-prompt.md`:
   - Update `.howmux/specs/` → `.howmux/specs/`
   - Update `.howmux/artifacts/` → `.howmux/artifacts/`
   - Update references to "howmux" in paths and examples
   - **PRESERVE** the PR #20 example in `architect-prompt.md` (it documents the rename itself)
2. Rename template directory: `cmd/howmux/templates/howmux/` → `cmd/howmux/templates/howmux/`
3. Update all eval case files in `cmd/howmux/templates/howmux/evals/cases/*/*.yaml`:
   - Update any references to `.howmux/` paths
4. Sync templates to live directories:
   - Copy agent prompts to `.kiro/agents/`
   - Copy scripts to `.howmux/scripts/`
   - Copy themes to `.howmux/themes/`
   - Copy evals to `.howmux/evals/`

**Acceptance Criteria**:
- Agent prompt templates updated: all `.howmux/` → `.howmux/`
- Template directory renamed: `cmd/howmux/templates/howmux/` → `cmd/howmux/templates/howmux/`
- Eval case files updated
- All templates synchronized to live directories
- **Verification**: `diff -rq .kiro/agents/ cmd/howmux/templates/kiro/agents/ --exclude="*.json"` shows no differences in markdown files
- **Verification**: `diff -rq .howmux/scripts/ cmd/howmux/templates/howmux/scripts/` shows no differences
- **Verification**: `diff -rq .howmux/themes/ cmd/howmux/templates/howmux/themes/` shows no differences
- **Verification**: `diff -rq .howmux/evals/ cmd/howmux/templates/howmux/evals/ --exclude=results --exclude=tmp` shows no differences
- **Verification**: `task sync:check` passes

### Task 2.3: CI/CD Workflow Updates

**Objective**: Update GitHub Actions workflows with new artifact names and commands.

**Steps**:
1. Update `.github/workflows/release.yml`:
   - Line 48: `ls dist/release/howmux ...` → `ls dist/release/howmux ...`
   - Lines 50-54: All `./dist/release/howmux*` → `./dist/release/howmux*`
   - Lines 51-53: All artifact labels `"howmux ..."` → `"howmux ..."`
2. Review `.github/workflows/ci.yml` (likely no changes needed, uses Taskfile)
3. Update release artifact build tasks in `Taskfile.yml` (should be done in Task 1.2)

**Acceptance Criteria**:
- `.github/workflows/release.yml` updated: all artifact paths and labels
- `.github/workflows/ci.yml` verified (no changes needed)
- **Verification**: `grep -rn "howmux" .github/workflows/` returns zero matches
- **Verification**: Manual review of release workflow to ensure artifact names match Taskfile outputs

### Task 3: Final Verification and Testing

**Objective**: Verify completeness of the rename and ensure all tests pass.

**Steps**:
1. Run comprehensive grep to find any remaining references:
   ```bash
   grep -rn "howmux" \
     --include="*.go" \
     --include="*.md" \
     --include="*.yaml" \
     --include="*.yml" \
     --include="*.json" \
     --include=".gitignore" \
     --include="*.sh" \
     . | grep -v ".worktrees" | grep -v "node_modules"
   ```
2. Verify justified exceptions (document in PR body):
   - `architect-prompt.md` contains PR #20 rename example (KEEP)
   - Any other intentional references
3. Run `go mod tidy` and verify clean module state
4. Run `task fmt:check` to verify code formatting
5. Run `task sync:check` to verify template synchronization
6. Run `task lint` to verify linting passes
7. Run `task test` to verify all tests pass
8. Run `task build` to verify the binary builds successfully
9. Test the binary: `./dist/howmux --version`
10. Test init command: `./dist/howmux init` in a temp directory

**Acceptance Criteria**:
- Repo-wide search returns ONLY justified matches (documented in PR body)
- `go mod tidy` completes successfully
- `task fmt:check` passes
- `task sync:check` passes
- `task lint` passes
- `task test` passes (all tests green)
- `task build` completes and produces `dist/howmux` binary
- Binary runs: `./dist/howmux --version` shows version info
- Init command works: creates `.howmux/` directory structure
- **Verification**: Zero stray references remain (excluding documented exceptions)
- **Verification**: `git status` shows no unexpected changes (e.g., committed runtime artifacts)

## Validation Commands

### Pre-Merge Validation
```bash
# 1. Find any remaining howmux references (should be minimal/justified)
grep -rn "howmux" --include="*.go" --include="*.md" --include="*.yaml" \
  --include="*.yml" --include="*.json" --include=".gitignore" --include="*.sh" . \
  | grep -v ".worktrees" | grep -v "node_modules"

# 2. Find any remaining KIRO_KREW env vars (should be zero)
grep -rn "KIRO_KREW" --include="*.go" .

# 3. Find any remaining .howmux paths (should be zero, excluding examples)
grep -rn "\.howmux" --include="*.go" --include=".gitignore" . | grep -v ".worktrees"

# 4. Verify module integrity
go mod tidy
git diff go.mod go.sum  # Should show no changes if already tidy

# 5. Run full test suite
task test

# 6. Verify template sync
task sync:check

# 7. Build and smoke test
task build
./dist/howmux --version
./dist/howmux --about

# 8. Test init in temp directory
mkdir -p /tmp/howmux-test-init
cd /tmp/howmux-test-init
/path/to/dist/howmux init
ls -la .howmux/  # Should show config.yaml, scripts/, themes/, evals/
ls -la .kiro/    # Should show agents/
cd -
rm -rf /tmp/howmux-test-init
```

### Post-Merge Validation
```bash
# 1. Verify CI passes on main
gh run list --branch main --limit 1

# 2. Test release process (manual or semantic-release dry-run)
# Manual: git tag v0.1.0-test && git push origin v0.1.0-test
# Semantic: npx semantic-release --dry-run

# 3. Verify released artifacts have correct names
gh release view <version> --json assets
```

## Critical Success Factors

1. **Atomicity**: All changes must be committed together in a single PR
2. **Writer/Reader Pairs**: Environment variables must be renamed in both writer and reader simultaneously
3. **Template Synchronization**: Live files (`.kiro/`, `.howmux/`) must match template files (`cmd/howmux/templates/`)
4. **Configuration Files**: `.gitignore`, `Taskfile.yml`, `.releaserc.json` are CRITICAL and often overlooked
5. **Test Coverage**: All existing tests must pass without modification (pure rename)
6. **No Functional Changes**: This is a naming refactor only; behavior must remain identical

## Risk Mitigation

1. **Stray References**: Use comprehensive grep verification (Task 3) to catch missed references
2. **Path Inconsistencies**: Verify `.gitignore` paths match actual runtime artifact paths
3. **CI/CD Breakage**: Test release workflow in a branch before merging
4. **Template Drift**: Run `task sync:check` to verify live files match templates
5. **Import Errors**: Run `go mod tidy` and full test suite to catch import path issues

## Definition of Done

- [ ] All 240+ files updated with new names
- [ ] `go mod tidy` completes without errors
- [ ] `task fmt:check` passes
- [ ] `task sync:check` passes
- [ ] `task lint` passes
- [ ] `task test` passes (100% existing tests green)
- [ ] `task build` produces working `dist/howmux` binary
- [ ] Binary smoke test passes (`--version`, `--about`, `init`)
- [ ] Repo-wide search returns only justified exceptions (documented in PR)
- [ ] PR body documents all intentional remaining references
- [ ] CI workflow passes on PR branch
