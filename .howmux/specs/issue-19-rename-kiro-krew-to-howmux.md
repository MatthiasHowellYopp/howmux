# Design Specification: Rename howmux to howmux

**Issue**: #19  
**Type**: Mechanical Rename  
**Scope**: Complete atomic rename across 150+ files  
**Closes**: #19

## Problem Statement

This repository is an R&D fork where experimentation and divergence from upstream (flowdra and the original howmux) are expected. The current name "howmux" creates confusion because:

1. It shares the name with the original upstream project
2. This is a personal research and development version that may take different directions
3. A distinct name clarifies this is an independent experimental branch

The repository URL (`matthiashowellyopp/howmux`) already reflects the new name. Only internal references need updating.

## Solution Approach

This is a **complete mechanical rename** from `howmux` to `howmux` across the entire codebase. The rename must be atomic — all references updated together to avoid a broken intermediate state. No functionality changes; purely a naming refactor.

### Affected Surface Areas

1. **Go Module & Imports** — Module path, all internal imports
2. **Binary & Build System** — Binary name, build targets, directory structure
3. **Configuration & Runtime Paths** — `.howmux/` → `.howmux/`, all path references
4. **Documentation** — README, CONTRIBUTING, CHANGELOG, all docs
5. **User-Facing Text** — CLI help, prompts, agent configs
6. **CI/CD & Release** — GitHub Actions, release artifacts
7. **Environment Variables** — `KIRO_KREW_*` → `HOWMUX_*` (writer + reader pairs)
8. **Template Files** — Embedded templates for `init` command
9. **Tests** — Test files, fixtures, assertions

### Concurrency Considerations

This change does not introduce new cross-goroutine access patterns. The rename affects naming only, not concurrency boundaries. All existing lock-guarded access patterns remain unchanged.

## Relevant Files

### Go Source Files (~80 files)
- `go.mod` — Module declaration
- `cmd/howmux/` directory → `cmd/howmux/`
- All `.go` files in `cmd/`, `internal/` — Import paths
- All `*_test.go` files — Import paths, test assertions

### Configuration Files
- `.gitignore` — Binary names, `.howmux/` paths
- `Taskfile.yml` — Binary name variable, build targets, ldflags
- `.releaserc.json` — Release artifact paths and labels
- `package.json` — Name field (if present)

### Documentation Files
- `README.md` — Project name, installation instructions, examples
- `CONTRIBUTING.md` — Project references
- `CHANGELOG.md` — Project name in entries
- `docs/*.md` — All documentation files

### CI/CD Workflows
- `.github/workflows/ci.yml` — Build steps if any references exist
- `.github/workflows/release.yml` — Artifact paths and names

### Template Files (Embedded in Binary)
- `cmd/howmux/templates/howmux/` directory → `cmd/howmux/templates/howmux/`
- `cmd/howmux/templates/kiro/agents/*.json` — References in configs
- `cmd/howmux/templates/kiro/agents/*.md` — Agent prompt references
- All scripts, configs, eval fixtures in templates

### Test Fixtures & Data
- `.howmux/evals/fixtures/` — Example configs
- Any test data files with embedded project names

## Team Orchestration

This is a sequential implementation — all tasks must be completed in order to maintain a working state at each commit. The rename surface is tightly coupled; partial completion breaks the build.

**Implementation Sequence:**
1. **Foundation Layer** — Update module declaration and create new directory structure
2. **Source Code Layer** — Update all Go imports and code references  
3. **Configuration Layer** — Update build configs, CI/CD, and release configs
4. **Documentation Layer** — Update all user-facing documentation
5. **Template Layer** — Update embedded templates and directory paths
6. **Verification Layer** — Run complete test suite and verify no stray references

## Step-by-Step Task Breakdown

### Task 1: Update Go Module Declaration and Directory Structure

**Purpose:** Establish new module path and directory foundation

**Acceptance Criteria:**
1. `go.mod` module declaration updated from `github.com/jbrinkman/howmux` to `github.com/matthiashowellyopp/howmux`
   - **Verification**: `head -1 go.mod` shows new module path
2. `cmd/howmux/` directory renamed to `cmd/howmux/`
   - **Verification**: `ls cmd/` shows `howmux/`, not `howmux/`
3. `cmd/howmux/templates/howmux/` directory renamed to `cmd/howmux/templates/howmux/`
   - **Verification**: `ls cmd/howmux/templates/` shows `howmux/`, not `howmux/`

**Dependencies**: None

---

### Task 2: Update All Go Source File Imports

**Purpose:** Update every import statement to use new module path

**Acceptance Criteria:**
1. All Go source files in `cmd/howmux/` updated:
   - Import paths: `github.com/jbrinkman/howmux` → `github.com/matthiashowellyopp/howmux`
   - Package references in code (if any explicit string references)
   - **Verification**: `grep -r "github.com/jbrinkman/howmux" cmd/howmux/ --include="*.go"` returns zero matches

2. All Go source files in `internal/` updated:
   - Import paths updated
   - Any hardcoded `.howmux/` paths → `.howmux/`
   - **Verification**: `grep -r "github.com/jbrinkman/howmux" internal/ --include="*.go"` returns zero matches
   - **Verification**: `grep -r '\.howmux' internal/ --include="*.go"` returns zero matches

3. All Go test files updated:
   - Import paths updated
   - Test fixtures and assertions referencing project name updated
   - **Verification**: `grep -r "github.com/jbrinkman/howmux" --include="*_test.go" .` returns zero matches
   - **Verification**: `grep -r "howmux" internal/config/base_branch_test.go` returns zero matches (test fixtures)

4. User-facing strings in CLI updated:
   - `cmd/howmux/cmd/root.go`: `Use: "howmux"`, `Long: "howmux is a multi-agent..."`
   - **Verification**: `grep "Use:.*howmux" cmd/howmux/cmd/root.go` returns zero matches

**Dependencies**: Task 1 (module and directory structure must exist)

---

### Task 3: Update Environment Variables (Writer + Reader Pairs)

**Purpose:** Rename all `KIRO_KREW_*` environment variables with verification of both writer and reader

**Acceptance Criteria:**
1. `KIRO_KREW_WATCHER_PID` → `HOWMUX_WATCHER_PID`:
   - **Writer**: `internal/agent/manager.go` — both occurrences updated
   - **Reader**: `internal/hotkey/detector.go` — `os.Getenv("HOWMUX_WATCHER_PID")`
   - **Tests**: All test files in `internal/hotkey/*_test.go` updated
   - **Verification**: `grep -rn "KIRO_KREW_WATCHER_PID" internal/` returns zero matches

2. Any other `KIRO_KREW_*` environment variables in codebase:
   - **Search**: `grep -r "KIRO_KREW" --include="*.go" .`
   - **Update**: Writer and reader for each found variable
   - **Verification**: `grep -r "KIRO_KREW" --include="*.go" .` returns zero matches

**Dependencies**: Task 2 (source files must have correct module structure)

---

### Task 4: Update Build Configuration Files

**Purpose:** Update build system, binary names, and compilation targets

**Acceptance Criteria:**
1. `Taskfile.yml` updated:
   - `BINARY_NAME: howmux` (line ~4)
   - All `-ldflags` references: `github.com/matthiashowellyopp/howmux/internal/version`
   - All `./cmd/howmux` references → `./cmd/howmux`
   - **Verification**: `grep "howmux" Taskfile.yml` returns zero matches
   - **Verification**: `task build` succeeds and produces `dist/howmux`

2. `.gitignore` updated:
   - `/howmux` → `/howmux`
   - `/howmux-test` → `/howmux-test`
   - `/howmux-validate` → `/howmux-validate`
   - `/test-howmux` → `/test-howmux`
   - `cmd/howmux/howmux` → `cmd/howmux/howmux`
   - `.howmux/retries/` → `.howmux/retries/`
   - `**/.howmux/evals/tmp/` → `**/.howmux/evals/tmp/`
   - `.howmux/sessions/` → `.howmux/sessions/`
   - `.howmux/artifacts/` → `.howmux/artifacts/`
   - `**/.howmux/sessions/` → `**/.howmux/sessions/`
   - **Verification**: `grep "howmux" .gitignore` returns zero matches

**Dependencies**: Task 1 (directory structure must exist)

---

### Task 5: Update CI/CD and Release Configuration

**Purpose:** Update GitHub Actions workflows and release artifact naming

**Acceptance Criteria:**
1. `.github/workflows/release.yml` updated:
   - All `howmux` artifact paths → `howmux`
   - `dist/release/howmux` → `dist/release/howmux`
   - `howmux-linux-amd64` → `howmux-linux-amd64`
   - `howmux-linux-arm64` → `howmux-linux-arm64`
   - All artifact labels: `"howmux macOS ARM64"` → `"howmux macOS ARM64"`, etc.
   - **Verification**: `grep "howmux" .github/workflows/release.yml` returns zero matches

2. `.github/workflows/ci.yml` checked and updated:
   - Any build steps or artifact references
   - **Verification**: `grep "howmux" .github/workflows/ci.yml` returns zero matches (may be none)

3. `.releaserc.json` updated:
   - `prepareCmd` task references (if any project-name-specific)
   - All `assets[].path` values: `dist/release/howmux*` → `dist/release/howmux*`
   - All `assets[].label` values: `"howmux *"` → `"howmux *"`
   - **Verification**: `grep "howmux" .releaserc.json` returns zero matches

**Dependencies**: Task 4 (build config must be updated first)

---

### Task 6: Update All Documentation Files

**Purpose:** Update user-facing documentation with new project name

**Acceptance Criteria:**
1. `README.md` updated:
   - Title: `# Howmux` (or `# Kiro Krew` → `# Howmux`)
   - All occurrences of "howmux" → "howmux"
   - Installation instructions: `go install github.com/matthiashowellyopp/howmux@latest`
   - Binary name references: `howmux` → `howmux` in usage examples
   - Git clone instructions (if any): new repo path
   - **Verification**: `grep -i "howmux" README.md` returns only justified references (e.g., "forked from howmux")

2. `CONTRIBUTING.md` updated:
   - All project name references
   - Repository references
   - **Verification**: `grep "howmux" CONTRIBUTING.md` returns zero matches

3. `CHANGELOG.md` updated:
   - Project name in header/title
   - Keep historical entries unchanged (they reference the old name at that time)
   - **Verification**: Title/header uses "howmux"

4. `docs/*.md` files updated:
   - All markdown files in `docs/` directory
   - CLI command examples: `howmux` → `howmux`
   - Configuration paths: `.howmux/` → `.howmux/`
   - **Verification**: `grep -r "howmux" docs/ --include="*.md"` returns only justified historical references

5. `LICENSE` file checked:
   - Ensure no project-name-specific references need updating
   - **Verification**: `grep "howmux" LICENSE` returns zero matches (likely)

**Dependencies**: None (documentation is parallel to implementation)

---

### Task 7: Update Template Files (Embedded in Binary)

**Purpose:** Update all embedded template files that the `init` and `update` commands install

**Acceptance Criteria:**
1. Template configuration files updated:
   - `cmd/howmux/templates/howmux/config.yaml` — Any project name references
   - All theme YAML files in `cmd/howmux/templates/howmux/themes/*.yaml`
   - **Verification**: `grep -r "howmux" cmd/howmux/templates/howmux/ --include="*.yaml"` returns zero matches

2. Template scripts updated:
   - All `.sh` files in `cmd/howmux/templates/howmux/scripts/`
   - Script comments, echo statements, variable names
   - Path references: `.howmux/` → `.howmux/`
   - **Verification**: `grep -r "howmux" cmd/howmux/templates/howmux/scripts/` returns zero matches

3. Agent configuration templates updated:
   - All `.json` files in `cmd/howmux/templates/kiro/agents/`
   - All `.md` prompt files in `cmd/howmux/templates/kiro/agents/`
   - Module path references in descriptions or examples
   - **Verification**: `grep -r "howmux" cmd/howmux/templates/kiro/agents/` returns zero matches

4. Evaluation framework templates updated:
   - All files in `cmd/howmux/templates/howmux/evals/`
   - Fixtures, rubrics, and example cases
   - **Verification**: `grep -r "howmux" cmd/howmux/templates/howmux/evals/` returns zero matches

5. Template extraction code checked:
   - `internal/templates/extract.go` — Embedded path references if any
   - **Verification**: `grep "howmux" internal/templates/extract.go` returns zero matches

**Dependencies**: Task 1 (template directory must be renamed first)

---

### Task 8: Update Live Project Files (Non-Template)

**Purpose:** Update the live `.howmux/` directory and `.kiro/` agent configs in the project root

**Acceptance Criteria:**
1. `.howmux/` directory renamed to `.howmux/`:
   - `mv .howmux .howmux`
   - **Verification**: `ls -d .howmux` succeeds, `ls -d .howmux` fails

2. All files within `.howmux/` directory updated:
   - `config.yaml`, scripts, themes — any project name references
   - Spec files (if they contain project-specific paths)
   - **Verification**: `grep -r "howmux" .howmux/ --include="*.yaml" --include="*.md" --include="*.sh"` returns zero matches

3. `.kiro/agents/` configuration files updated:
   - All `.json` and `.md` files
   - Any project-specific references (likely none, but check)
   - **Verification**: `grep -r "howmux" .kiro/agents/` returns zero matches (likely already clean)

**Dependencies**: Task 7 (templates must be updated so `update` command can sync correctly)

---

### Task 9: Update Test Files and Fixtures

**Purpose:** Update all test-specific files, fixtures, and test data

**Acceptance Criteria:**
1. Test fixture files updated:
   - `.howmux/evals/fixtures/*.md` — Any embedded project names
   - Test data in `internal/*/testdata/` if any
   - **Verification**: `grep -r "howmux" .howmux/evals/fixtures/` returns zero matches

2. Test assertion strings updated:
   - All `*_test.go` files checked for string assertions
   - Error message assertions containing "howmux"
   - Config example strings in tests
   - **Verification**: `grep -r '"howmux"' --include="*_test.go" .` returns zero matches
   - **Verification**: `grep -r "'howmux'" --include="*_test.go" .` returns zero matches

**Dependencies**: Task 2 (test imports must be updated), Task 8 (live files renamed)

---

### Task 10: Verification and Completeness Check

**Purpose:** Verify all references updated and no broken state remains

**Acceptance Criteria:**
1. **Full codebase search for stray references**:
   ```bash
   grep -r "howmux" \
     --include="*.go" \
     --include="*.md" \
     --include="*.yaml" \
     --include="*.yml" \
     --include="*.sh" \
     --include="*.json" \
     --include=".gitignore" \
     . | tee /tmp/stray-refs.txt
   ```
   - Output should show ONLY justified references (e.g., historical changelog entries, "forked from howmux" attribution)
   - Document any remaining matches in the PR body as justified exceptions
   - **Acceptance**: Zero unjustified matches

2. **Environment variable verification**:
   - `grep -rn "KIRO_KREW" --include="*.go" .` returns zero matches
   - **Acceptance**: Zero matches

3. **Build succeeds**:
   - `task clean && task build` completes without errors
   - Binary produced: `dist/howmux` (not `dist/howmux`)
   - **Acceptance**: Build produces `dist/howmux`, all imports resolve

4. **Tests pass**:
   - `go test ./...` passes all tests
   - `task test` passes
   - **Acceptance**: All tests pass, zero failures

5. **Module validation**:
   - `go mod tidy` runs without errors
   - `go list -m all` shows `github.com/matthiashowellyopp/howmux` as the main module
   - **Acceptance**: Module system clean, no broken dependencies

6. **Git status clean**:
   - All renamed directories tracked
   - No untracked template files or broken references
   - **Acceptance**: `git status` shows expected renames and modifications only

**Dependencies**: All previous tasks (final verification)

---

## Validation Commands

```bash
# 1. Verify module path updated
head -1 go.mod

# 2. Verify directory structure
ls cmd/ | grep howmux
ls cmd/howmux/templates/ | grep howmux

# 3. Verify no stray Go import references
grep -r "github.com/jbrinkman/howmux" --include="*.go" .

# 4. Verify no stray path references in Go files
grep -r '\.howmux' --include="*.go" .

# 5. Verify no stray env var references
grep -rn "KIRO_KREW" --include="*.go" .

# 6. Verify config files clean
grep "howmux" Taskfile.yml .gitignore .releaserc.json

# 7. Verify CI workflows clean
grep "howmux" .github/workflows/*.yml

# 8. Verify templates clean
grep -r "howmux" cmd/howmux/templates/

# 9. Verify documentation clean (expect justified historical refs only)
grep -r "howmux" --include="*.md" .

# 10. Build and test
task clean
task build
test -f dist/howmux
go test ./...
go mod tidy
```

## Success Criteria

- [ ] Module path: `github.com/matthiashowellyopp/howmux`
- [ ] Binary name: `howmux` in all build outputs
- [ ] Directory: `cmd/howmux/` exists (not `cmd/howmux/`)
- [ ] Template directory: `cmd/howmux/templates/howmux/` exists
- [ ] Live config directory: `.howmux/` exists (not `.howmux/`)
- [ ] Environment variables: `HOWMUX_*` (not `KIRO_KREW_*`)
- [ ] All imports use new module path
- [ ] All `.gitignore` entries use new names
- [ ] CI/CD workflows reference new artifact names
- [ ] Documentation uses "howmux" consistently
- [ ] All tests pass: `go test ./...`
- [ ] Build succeeds: `task build` produces `dist/howmux`
- [ ] Full search for `howmux` returns ONLY justified historical references
- [ ] Full search for `KIRO_KREW` returns zero matches
- [ ] `go mod tidy` succeeds with no errors

## Notes

- This is a **mechanical rename** with no functionality changes
- The rename must be **atomic** to avoid broken intermediate states
- Template synchronization with live files is critical (see `builder-conventions` skill)
- Tasks are **sequential** and tightly coupled — partial completion breaks the build
- Git history and existing issues/PRs remain unchanged
- Repository URL already reflects new name; internal references catch up to it
