---
name: builder-conventions
description: Project-specific conventions and patterns for the builder agent. Maintains synchronization between template and live project files.
---

# Builder Conventions

Project-specific conventions, patterns, and best practices for the builder agent to follow during implementation tasks.

## Mandatory Template Synchronization

**Critical for Self-Hosting**: Kiro Krew uses itself to build itself. Live project files and embedded templates must stay in sync so that `kiro-krew init` and `kiro-krew update` always deploy current configurations.

Sync is **one-way** (live → template). CI enforces this via `task sync:check` in the Validate PR workflow.

### Sync Mappings

| Live Path | Template Path |
|-----------|---------------|
| `.kiro/agents/*.json` | `cmd/howmux/templates/kiro/agents/` |
| `.kiro/agents/*.md` | `cmd/howmux/templates/kiro/agents/` |
| `.howmux/scripts/*.sh` | `cmd/howmux/templates/howmux/scripts/` |
| `.howmux/themes/*.yaml` | `cmd/howmux/templates/howmux/themes/` |
| `.howmux/evals/fixtures/*` | `cmd/howmux/templates/howmux/evals/fixtures/` |
| `.howmux/evals/rubrics/*` | `cmd/howmux/templates/howmux/evals/rubrics/` |
| `.howmux/evals/cases/**/*` | `cmd/howmux/templates/howmux/evals/cases/` |

### Exclusion Patterns

**Never sync `*-conventions` skills** — they are project-specific and must NOT be distributed in templates.

**Never sync local-only agent config into templates** — some entries in the live
`.kiro/agents/*.json` are intentionally local-only and must NOT ship in the
embedded templates that reach end users. In particular the **`creds-agent` MCP
server** and the **`@creds-agent`** tool grant vend developer-machine
credentials and belong only in the live agents. `task sync:check` compares
agent JSON with a JSON-aware tool that ignores these named entries (see
`scripts/compare-templates.go`), so a live-only creds-agent block does not count
as drift — do not "fix" a nonexistent drift by copying it into the templates.

### Sync Commands

Run the appropriate commands after modifying any template-synchronized files.

> **Agent JSON:** do not blindly `cp` live agents over the templates — that
> would carry local-only entries (e.g. the creds-agent MCP block) into the
> shipped artifact. Copy the file, then strip any local-only `mcpServers` and
> `@creds-agent`/local-only tool grants from the template copy, or edit the
> template to mirror only the intended change. `task sync:check` will pass as
> long as the non-local-only content matches.

```bash
# Agent files (JSON configs and prompt files)
cp .kiro/agents/*.json cmd/howmux/templates/kiro/agents/
cp .kiro/agents/*.md cmd/howmux/templates/kiro/agents/

# Scripts
cp .howmux/scripts/*.sh cmd/howmux/templates/howmux/scripts/

# Themes
cp .howmux/themes/*.yaml cmd/howmux/templates/howmux/themes/

# Evals (excluding results directory)
cp .howmux/evals/fixtures/* cmd/howmux/templates/howmux/evals/fixtures/
cp .howmux/evals/rubrics/* cmd/howmux/templates/howmux/evals/rubrics/
mkdir -p cmd/howmux/templates/howmux/evals/cases/
cp -r .howmux/evals/cases/* cmd/howmux/templates/howmux/evals/cases/
```

### Verification

Run `task sync:check` to verify all template-synchronized files match. This is the same check CI runs — if it passes locally, CI will pass.

```bash
task sync:check
```

If verification fails, re-run the sync commands above for the affected file category, then re-run `task sync:check`.

## Workflow Integration

**Critical Requirement**: If your task created or modified files under any synced directory, you MUST copy them to their template mirrors before completion. This applies to **newly created files** (not just edits to existing files).

When completing tasks that modify template-synchronized files, follow this sequence:

1. **Implement** — complete the assigned task
2. **Sync** — run the appropriate sync commands from above for any new or modified files
3. **Verify** — run `task sync:check` and confirm it passes locally
4. **QA** — run all discovered QA commands (`task lint`, `task test`, etc.)
5. **Complete** — create sentinel file documenting results

**Task cannot be marked complete if `sync:check` fails.**

### Synced Surfaces Checklist

Before marking a task complete, if you created or modified files under any of these directories, you MUST mirror them to templates:

- `.howmux/scripts/` → `cmd/howmux/templates/howmux/scripts/`
- `.howmux/themes/` → `cmd/howmux/templates/howmux/themes/`
- `.howmux/evals/fixtures/` → `cmd/howmux/templates/howmux/evals/fixtures/`
- `.howmux/evals/rubrics/` → `cmd/howmux/templates/howmux/evals/rubrics/`
- `.howmux/evals/cases/` → `cmd/howmux/templates/howmux/evals/cases/` (recursive)
- `.kiro/agents/*.json` → `cmd/howmux/templates/kiro/agents/` (excluding local-only entries per guidance)
- `.kiro/agents/*.md` → `cmd/howmux/templates/kiro/agents/`

**Excluded from sync**: `*-conventions` skills (`.kiro/skills/*-conventions/`) are project-specific and must NOT be synced.

**This applies to**:
- ✅ New files created
- ✅ Edits to existing files
- ✅ File renames or moves

### Verification Fallback

If `task sync:check` cannot be run (task binary unavailable), verify manually:

```bash
# Agents comparison (JSON-aware, ignores local-only entries)
go run scripts/compare-templates.go --agents-only

# Direct diff for other synced dirs
diff -rq .howmux/scripts/ cmd/howmux/templates/howmux/scripts/
diff -rq .howmux/themes/ cmd/howmux/templates/howmux/themes/
diff -rq --exclude=results --exclude=.DS_Store --exclude=tmp .howmux/evals/ cmd/howmux/templates/howmux/evals/
```

All commands must produce no output (or only "Files ... and ... are identical") for sync to be valid.

### Sentinel File Requirements

Include sync verification status in sentinel files. For tasks that touch synced surfaces, sync verification is MANDATORY.

**Example for tasks that created new files under synced directories**:

```markdown
## Task Complete

**Files Modified**:
- Created: `.howmux/evals/cases/krew-lead/case-001-spawn-builder.yaml` (NEW)
- Created: `.howmux/evals/fixtures/sample-issue.md` (NEW)
- Edited: `.kiro/agents/builder.json`

**Template Sync**: ✅ VERIFIED
- Mirrored new eval case and fixture to templates
- Updated agent JSON in template (local-only creds-agent block excluded)
- `task sync:check` passed locally

**QA Results**:
- Linting: ✅ PASS (`task lint`)
- Tests: ✅ PASS (`task test`)
- Formatting: ✅ PASS (`task fmt:check`)
- Sync Verification: ✅ PASS (`task sync:check`)

**Sync Commands Used**:
```bash
mkdir -p cmd/howmux/templates/howmux/evals/cases/krew-lead/
cp .howmux/evals/cases/krew-lead/case-001-spawn-builder.yaml cmd/howmux/templates/howmux/evals/cases/krew-lead/
cp .howmux/evals/fixtures/sample-issue.md cmd/howmux/templates/howmux/evals/fixtures/
cp .kiro/agents/builder.json cmd/howmux/templates/kiro/agents/
# (Then manually removed local-only creds-agent MCP block from template copy)
task sync:check  # ✅ PASS
```
```

**Example for tasks that didn't touch synced files**:

```markdown
## Task Complete

**Template Sync**: N/A - no template files modified

**QA Results**:
- Linting: ✅ PASS
- Tests: ✅ PASS
```

## Implementation Patterns

### Quality Assurance
- Run ALL discovered QA commands before completion
- Use QA discovery results from `.kiro-krew/artifacts/qa-tools.md`
- Document specific QA command sources (CI vs build tool)

### File Modifications
- Preserve existing formatting and structure
- Maintain JSON validity for configuration files
- Verify template sync before task completion
- Document changes in sentinel files including sync status

### Error Recovery
- Address validator feedback from `.kiro-krew/artifacts/validator-<issue>.md`
- Focus on specific failing commands identified by validator
- Include sync verification in error recovery process
- Document how feedback was incorporated

## Project Standards

### Code Quality
- Follow existing code style and conventions
- Use project's configured linting and formatting tools
- Ensure all tests pass (100% pass rate required)

### Documentation
- Update relevant docs when adding features
- Follow project's documentation format
- Include usage examples for new functionality

## Sweeping and Mechanical Changes

When implementing a **sweeping mechanical change** (rename, move across packages, version bump across dependencies), "done" requires **repo-wide verification across all file types** — not just source code.

### Definition

A **mechanical change** is one where the same transformation must be applied consistently everywhere:
- **Rename**: project name, package name, struct/type name, env var name
- **Move**: relocating a type or package and updating all references
- **Version bump**: updating dependency versions across all lockfiles and imports

### Completeness Requirements

**"Done" means**:
1. **Repo-wide search** for the old token across **all file types** returns only intentional/justified matches
2. **Writer and reader pairs** were renamed together (env vars, symbols, struct fields)
3. **Config files** are part of the surface (not just source code)

**Search ALL file types**:
```bash
# Not just source files
grep -rn "old-token" \
  --include="*.go" \
  --include="*.ts" \
  --include="*.py" \
  --include="*.java" \
  .

# Also: tests, docs, config, CI
grep -rn "old-token" \
  --include="*_test.go" \
  --include="*.md" \
  --include="README*" \
  --include=".gitignore" \
  --include="Taskfile.yml" \
  --include="package.json" \
  --include="go.mod" \
  --include="*.yaml" \
  --include="*.yml" \
  .

# CI and build config explicitly
grep -rn "old-token" .github/workflows/ .gitlab-ci.yml Makefile Jenkinsfile

# Template-synced files (see Template Synchronization section)
grep -rn "old-token" cmd/kiro-krew/templates/
```

### Critical: Config Files Are Part of the Surface

Config files like `.gitignore`, `Taskfile.yml`, CI workflows (`.github/workflows/*.yml`), and build config are **part of the rename surface**, not ancillary files.

**Example from PR #20**: `.gitignore` still pointed at old `.kiro-krew/` runtime paths after rename to `howmux`, causing committed artifacts under `.howmux/` to slip through (the "rename the code" pass never examined `.gitignore` as part of the surface).

**Must check**:
- `.gitignore` — runtime path references
- `Taskfile.yml` / `Makefile` — task names, paths
- `.github/workflows/*.yml` — job names, paths, artifact names
- `package.json` / `go.mod` / `pom.xml` — package/module names
- `.releaserc.json` / release scripts — artifact names
- Template-synced files (see Mandatory Template Synchronization section)

### Writer and Reader Must Move Together

For **env-var or symbol renames**, verify the **writer and reader are renamed together**.

**The Trap**: Writer/reader pairs that still agree on the OLD name pass tests (system is internally consistent) but are incomplete.

**Example from PR #20**:
```go
// Writer (internal/agent/manager.go) — OLD NAME
env = append(env, fmt.Sprintf("KIRO_KREW_WATCHER_PID=%d", m.watcherPID))

// Reader (internal/hotkey/detector.go) — OLD NAME
watcherPID := os.Getenv("KIRO_KREW_WATCHER_PID")

// Both agree on OLD name → tests pass → but rename is INCOMPLETE
```

**Verification procedure**:
```bash
# Find writer (where env var is SET)
grep -rn "os.Setenv\|env.*append.*OLD_NAME" --include="*.go" .

# Find reader (where env var is READ)
grep -rn "os.Getenv.*OLD_NAME" --include="*.go" .

# Both results must be EMPTY (or show only NEW name) for rename to be complete
```

### Reconcile PR/Sentinel Description to Actual Changes

**Do not claim**:
- "Renamed X" if a `grep -rn "X"` contradicts it
- "No stray references" if repo-wide search finds them
- "All Z updated" if writer/reader pairs disagree or config files still use old names

**PR body / sentinel file claims become testable criteria** — the validator will check them. If you claim "no stray kiro-krew references remain", the validator will run `grep -rn "kiro-krew"` and fail validation if it finds matches.

### Completeness Verification Commands

Run these commands **before claiming "done"** on a mechanical change:

```bash
# Repo-wide search for old token (adjust file types for language)
grep -rn "old-token" \
  --include="*.go" \
  --include="*_test.go" \
  --include="*.md" \
  --include="*.yaml" \
  --include="*.yml" \
  --include="*.json" \
  --include=".gitignore" \
  --include="Taskfile.yml" \
  --include="Makefile" \
  .

# Config files explicitly
grep -rn "old-token" .gitignore .github/workflows/ Taskfile.yml

# Templates (if this project has embedded templates)
grep -rn "old-token" cmd/*/templates/ templates/

# Count matches (should be zero or only justified exceptions)
grep -rn "old-token" --include="*.go" . | wc -l
```

**Justified exceptions** (document these in PR body):
- Historical docs or migration notes that intentionally reference the old name
- Test fixtures that verify backward compatibility
- Embedded third-party code that shouldn't be modified

**Unjustified matches** (these are incomplete):
- User-facing strings (About dialog, help text, error messages)
- Config files (`.gitignore`, CI workflows)
- Tests or docs that should reference the new name
- Env var writers/readers

### Real-World Example: PR #20

**Issue**: Rename project from `kiro-krew` to `howmux`

**What "done" should have meant**:
1. Repo-wide search for `kiro-krew` returns only justified/historical matches
2. Config files (`.gitignore`, CI workflows) updated to new name
3. Env vars renamed in both writer AND reader
4. Template-synced files updated (agent configs, scripts)

**What actually happened**:
1. ~24-38 stray `kiro-krew` references in `.go` files (including user-facing "Kiro Krew" in About overlay)
2. `.gitignore` still pointed at `.kiro-krew/` → committed artifacts under `.howmux/` slipped through
3. Env var `KIRO_KREW_WATCHER_PID` unchanged in both writer and reader (tests passed but rename incomplete)

**How to prevent**:
```bash
# Before claiming "done", run:
grep -rn "kiro-krew" --include="*.go" . | wc -l
# Expected: 0 (or only justified matches documented in PR)

# Check config files
grep -rn "kiro-krew" .gitignore .github/workflows/ Taskfile.yml
# Expected: 0

# Check env vars moved together
grep -rn "KIRO_KREW" --include="*.go" .
# Expected: 0 (all should be HOWMUX_*)
```

## I/O Seam + Test the Wiring

**Rule**: When code performs I/O operations — subprocess execution via `exec.Command`, filesystem mutation, or network calls — route the execution through a small **injectable seam**. This can be a package-level function variable or an interface parameter, allowing tests to substitute a fake implementation.

**Rationale**: The wiring logic contains real complexity: which commands to run, in what order, with what arguments, in which working directory; conditional branches (clone vs. refresh, force-push vs. fast-forward); and cleanup/failure paths. This logic **must** be unit-tested against a fake implementation that records the command sequence, arguments, and working directory — without invoking real subprocesses or touching the network.

**"Thin wiring is not an exemption from testing."** If a layer contains branching, ordering decisions, or cleanup logic, it must be tested. The bugs surface in the wiring — not the pure functions.

**Pattern**: Keep the seam minimal and unexported where possible. Production code keeps the real implementation as the default value. Tests swap in a fake for the duration of the test and restore the original in a cleanup hook.

**Go Example** (injectable subprocess runner):

```go
package review

import (
    "fmt"
    "os/exec"
)

// command describes a subprocess invocation: argv and working directory.
type command struct {
    argv []string
    dir  string
}

// runCommand executes a single command in its working directory. It is a
// package var so tests can substitute a fake runner and assert the command
// sequence and cwd choices without invoking real git/gh.
var runCommand = func(c command) error {
    cmd := exec.Command(c.argv[0], c.argv[1:]...)
    cmd.Dir = c.dir
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("command %v (dir=%q) failed: %w\nOutput: %s", 
            c.argv, c.dir, err, string(output))
    }
    return nil
}

// EnsureCheckout ensures the PR code is checked out on disk, using runCommand
// for all subprocess invocations. The command sequence (clone vs refresh, force
// flag) is determined by pure function checkoutCommands, then executed here.
func EnsureCheckout(owner, repo, repoURL string, pr int, dir string) error {
    dirExists := dirIsGitRepo(dir)
    cmds := checkoutCommands(owner, repo, repoURL, pr, dir, dirExists)
    for _, c := range cmds {
        if err := runCommand(c); err != nil {
            return err
        }
    }
    return nil
}
```

**Test Example** (fake runner records command sequence):

```go
package review

import (
    "fmt"
    "reflect"
    "testing"
)

// withFakeRunner swaps runCommand for a recorder for the duration of the test.
// Returns a pointer to the recorded command slice, which tests can assert against.
func withFakeRunner(t *testing.T) *[]command {
    t.Helper()
    orig := runCommand
    var recorded []command
    runCommand = func(c command) error {
        recorded = append(recorded, c)
        return nil
    }
    t.Cleanup(func() { runCommand = orig })
    return &recorded
}

func TestEnsureCheckout_FreshClone(t *testing.T) {
    recorded := withFakeRunner(t)

    // Ensure checkout for a fresh clone (directory doesn't exist)
    err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 123, ".worktrees/review-owner-repo-123")
    if err != nil {
        t.Fatalf("EnsureCheckout failed: %v", err)
    }

    want := []command{
        {argv: []string{"git", "clone", "https://github.com/owner/repo.git", ".worktrees/review-owner-repo-123"}, dir: ""},
        {argv: []string{"gh", "pr", "checkout", "123", "--repo", "owner/repo"}, dir: ".worktrees/review-owner-repo-123"},
    }

    if !reflect.DeepEqual(*recorded, want) {
        t.Errorf("command sequence mismatch:\ngot:  %v\nwant: %v", *recorded, want)
    }
}

func TestEnsureCheckout_RefreshExisting(t *testing.T) {
    recorded := withFakeRunner(t)

    // Simulate existing directory (refresh path with --force)
    // In real test, you'd mock dirIsGitRepo to return true
    err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 456, ".worktrees/review-owner-repo-456")
    if err != nil {
        t.Fatalf("EnsureCheckout failed: %v", err)
    }

    // Assert that refresh uses git fetch + gh pr checkout --force
    want := []command{
        {argv: []string{"git", "fetch"}, dir: ".worktrees/review-owner-repo-456"},
        {argv: []string{"gh", "pr", "checkout", "456", "--repo", "owner/repo", "--force"}, dir: ".worktrees/review-owner-repo-456"},
    }

    if !reflect.DeepEqual(*recorded, want) {
        t.Errorf("command sequence mismatch:\ngot:  %v\nwant: %v", *recorded, want)
    }
}
```

**What Must Be Tested**:
- Command sequence (clone vs refresh, order of operations)
- Command arguments (repo URL, PR number, flags like `--force`)
- Working directory for each command (`cmd.Dir`)
- Branching logic (fresh clone vs existing repo, error handling)
- Cleanup paths (partial failure scenarios)

Tests must assert **all of the above** via the fake recorder — not by running real subprocesses.

## Go Concurrency Rules

### Mutex-Guarded Fields

A field protected by a `sync.Mutex` or `sync.RWMutex` must **only** be accessed while holding the appropriate lock. This applies to:
- Direct field access within the struct's methods
- **Accessor methods** (getters/setters) called from other goroutines

**Pattern**: If a struct has a mutex and fields guarded by it, every method that reads or writes those fields must acquire the lock first.

**Example** (Watcher):
```go
type Watcher struct {
    mu      sync.RWMutex
    started bool         // Guarded by mu
    // ...
}

// ✅ Correct: accessor acquires lock
func (w *Watcher) Running() bool {
    w.mu.RLock()
    defer w.mu.RUnlock()
    return w.started
}

// ❌ Wrong: accessor reads without lock
func (w *Watcher) Running() bool {
    return w.started  // RACE with Start/Stop
}
```

### Cross-Goroutine Access

When adding a feature that reads shared state from a **different goroutine** than the writers, verify the accessor method is lock-guarded:
- **TUI render path** reading agent/watcher/session state → accessor must hold lock
- **Command handler** reading state written by background loop → accessor must hold lock
- **Status display** reading dynamically updated fields → accessor must hold lock

**If no lock-guarded accessor exists**, either:
1. Add locking to the existing accessor (if it's unlocked), or
2. Create a new lock-guarded accessor method

Do **not** directly access struct fields from outside the struct, even if they are exported — use accessor methods so locking stays encapsulated.

### Concurrent Test Requirement

When a change adds a **new concurrent access path** (e.g., wiring the footer to read watcher state):
1. Add a test that exercises the accessor **concurrently** with the writers (e.g., `Start()`/`Stop()`)
2. The test must run the accessor in a loop while another goroutine modifies the state
3. Verify the test passes with `-race`: `go test -race ./path/to/package`

**Why**: A passing race detector with **no concurrent test** proves nothing — the detector only flags races it observes at runtime. If your test doesn't exercise the concurrent path, `-race` stays silent even when a race exists.

**Example**:
```go
func TestRunningConcurrentAccess(t *testing.T) {
    w := New(cfg, mgr)
    done := make(chan struct{})

    // Goroutine: flip state
    go func() {
        for {
            select {
            case <-done:
                return
            default:
                w.Start()
                time.Sleep(1 * time.Millisecond)
                w.Stop()
                time.Sleep(1 * time.Millisecond)
            }
        }
    }()

    // Main goroutine: read concurrently
    for i := 0; i < 1000; i++ {
        _ = w.Running()  // Would race without RLock
    }

    close(done)
}
```

This test **would fail** under `-race` if `Running()` didn't hold the lock, proving the test exercises the race-prone path.
