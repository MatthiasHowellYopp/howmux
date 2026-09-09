# Design Specification: Issue #16

**Title**: Self-heal: teach architect/builder/validator to catch concurrency (shared-state) races  
**Issue**: #16  
**Repository**: matthiashowellyopp/howmux  
**Closes**: #16

---

## Solution Approach

This issue addresses a recurring pattern where agent-produced PRs shipped data races that passed CI because tests didn't exercise the concurrent access paths. The root cause is a gap in agent guidance across three roles:

1. **Architect** — Did not identify that changes introduced cross-goroutine access to shared state
2. **Builder** — Wrote unlocked accessor reads from different goroutines (e.g., UI render path accessing `Watcher.started`)
3. **Validator** — Treated passing `task test` (including `-race`) as sufficient without verifying that tests actually exercised the concurrent paths

The solution is a **triple-layer fix**:
- Fix the actual race in `Watcher.Running()` (PR #15's bug)
- Add a regression test that exercises the concurrent path
- Update all three agent guidance files to prevent this class of bug from shipping again

This is a **self-healing change** — teaching the pipeline to catch its own mistakes.

---

## Architecture Context

### The Data Race Pattern (from PR #15)

**Scenario**: The footer manager (running on the TUI render goroutine) calls `watcher.Running()` to display watcher status. Meanwhile, the watcher's own goroutine writes `w.started` via `Start()` and `Stop()`.

**The Bug**:
```go
// internal/watcher/watcher.go

type Watcher struct {
    mu      sync.RWMutex
    started bool  // Protected by mu in Start/Stop
    // ...
}

func (w *Watcher) Start() {
    w.mu.Lock()         // ✅ Takes lock
    w.started = true
    w.mu.Unlock()
}

func (w *Watcher) Stop() {
    w.mu.Lock()         // ✅ Takes lock
    w.started = false
    w.mu.Unlock()
}

func (w *Watcher) Running() bool {
    return w.started    // ❌ No lock — RACE with Start/Stop
}
```

**Why `-race` didn't catch it**: No test exercised `Running()` concurrently with `Start()`/`Stop()`, so the race detector never observed the conflicting accesses.

### Cross-Goroutine Access Points in Kiro-Krew

The following are **concurrent boundaries** where shared state access requires locking:
- **TUI render loop** (render goroutine) reading agent/watcher/session state
- **Command handlers** (main goroutine) reading/writing state concurrently with background loops
- **Watcher poll loop** (watcher goroutine) writing tracked issues while commands read them
- **Agent manager** (spawner goroutine) writing agent state while status commands read it

Any accessor method that crosses these boundaries **must** acquire the appropriate lock.

---

## Relevant Files

### Files to Modify

| File | Change | Template Sync Required |
|------|--------|----------------------|
| `internal/watcher/watcher.go` | Add `RLock()`/`RUnlock()` to `Running()` accessor | No |
| `internal/watcher/watcher_test.go` | Add concurrent regression test (file doesn't exist yet — create it) | No |
| `.kiro/agents/architect-prompt.md` | Add concurrency analysis step to workflow | **Yes** |
| `.kiro/skills/builder-conventions/SKILL.md` | Add concurrency rule under code quality section | No |
| `.kiro/skills/validator-conventions/SKILL.md` | Add correctness dimension for concurrency | No |

### Template Sync Notes

**Critical**: Only `architect-prompt.md` is template-synced. The `*-conventions` skills are intentionally **not** synced (see "Exclusion Patterns" in builder-conventions). After modifying architect-prompt.md, run:

```bash
cp .kiro/agents/architect-prompt.md cmd/kiro-krew/templates/kiro/agents/architect-prompt.md
task sync:check
```

---

## Team Orchestration

### Parallelization Plan

**Tasks 1, 2, 3, 4, and 5 have no dependencies on each other** except that Task 2 (the regression test) depends on Task 1 (the lock fix) to actually pass under `-race`.

**Parallel Group 1** (can execute simultaneously):
- Task 1: Fix the race in `Running()` accessor
- Task 3: Update architect prompt with concurrency guidance
- Task 4: Update builder conventions with concurrency rule
- Task 5: Update validator conventions with correctness dimension

**Sequential Group 2** (depends on Task 1):
- Task 2: Add the concurrent regression test (must come after Task 1 so the test passes)

### Builder Agent Coordination

If multiple builders are spawned:
- **Builder A**: Tasks 1, 3, 4, 5 (parallel — independent edits to different files)
- **Builder B**: Task 2 (runs after Builder A completes Task 1)

Or execute sequentially with a single builder (safer for this issue given the guidance-file changes require careful writing).

---

## Step-by-Step Task Breakdown

### Task 1: Fix the PR #15 race in the watcher accessor

**Objective**: Make `Watcher.Running()` thread-safe by guarding access to `w.started` with the read lock.

**Acceptance Criteria**:
- `Watcher.Running()` in `internal/watcher/watcher.go` takes the read lock: acquires `w.mu.RLock()` (deferring `RUnlock()`) before reading `w.started`, matching how `Start()`/`Stop()` guard the same field.
- No other behavior of `Running()` changes (still returns `w.started`, no additional logic).
- The fix is a **one-line change** (adding lock acquisition + defer before the return).

**Implementation Notes**:
```go
// Before (unlocked):
func (w *Watcher) Running() bool {
    return w.started
}

// After (locked):
func (w *Watcher) Running() bool {
    w.mu.RLock()
    defer w.mu.RUnlock()
    return w.started
}
```

**Dependencies**: None (can run in parallel with Tasks 3–5)

---

### Task 2: Add a concurrent regression test for the accessor

**Objective**: Create a test that exercises `Running()` concurrently with state mutations so that `go test -race` would flag the unlocked version.

**Acceptance Criteria**:
- A new test in `internal/watcher/watcher_test.go` exercises `Running()` concurrently with a goroutine that flips the watcher's started state (via `Start`/`Stop` or the exported surface), so that the race detector would flag the unlocked version.
- `go test -race ./internal/watcher/` passes with the Task 1 fix in place.
- The test is written so that, **without the Task 1 lock**, `go test -race` would report a data race on `w.started` (i.e., it genuinely drives the concurrent path).
- The test file `internal/watcher/watcher_test.go` must be created (it doesn't exist yet).

**Implementation Strategy**:
```go
// internal/watcher/watcher_test.go

package watcher

import (
    "testing"
    "time"
    "github.com/jbrinkman/kiro-krew/internal/agent"
    "github.com/jbrinkman/kiro-krew/internal/config"
)

func TestRunningConcurrentAccess(t *testing.T) {
    // Setup: create watcher
    cfg := &config.Config{
        Repo:         "owner/repo",
        Label:        "test",
        PollInterval: time.Minute,
        MaxRetries:   3,
    }
    mgr := &agent.Manager{} // Mock or minimal manager
    w := New(cfg, mgr)

    // Goroutine 1: continuously flip started state
    done := make(chan struct{})
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

    // Goroutine 2 (main test): continuously read Running()
    for i := 0; i < 1000; i++ {
        _ = w.Running() // Would race without RLock
    }

    close(done)
}
```

**Why this catches the race**: The test loop calls `Running()` while another goroutine repeatedly writes `w.started` via `Start()`/`Stop()`. Without the `RLock()` in Task 1, `-race` detects:
```
WARNING: DATA RACE
Read at 0x... by goroutine X:
  github.com/jbrinkman/kiro-krew/internal/watcher.(*Watcher).Running()
      internal/watcher/watcher.go:270

Previous write at 0x... by goroutine Y:
  github.com/jbrinkman/kiro-krew/internal/watcher.(*Watcher).Start()
      internal/watcher/watcher.go:42
```

**Dependencies**: Task 1 (the lock fix must be in place for the test to pass)

---

### Task 3: Add a concurrency / shared-state analysis step to the architect

**Objective**: Teach the architect agent to identify when a change introduces cross-goroutine access to shared state and require lock-guarded access as an acceptance criterion.

**Acceptance Criteria**:
- `.kiro/agents/architect-prompt.md` gains an explicit analysis step (in the Workflow and/or Design Specification Requirements) requiring the architect to identify when a change introduces a **new reader or writer of state that is accessed from more than one goroutine** — e.g., types with a `sync.Mutex`/`sync.RWMutex` (such as `Watcher`), the TUI render loop, or the session manager.
- When such a boundary crossing exists, the spec must call it out and add "all access to the shared field is done under its lock" (or equivalent) as an explicit **acceptance criterion** in the task breakdown.
- The prompt names the concrete example: reading `Watcher` state from the footer/render path must go through a lock-guarded accessor.
- `.kiro/agents/architect-prompt.md` is copied to `cmd/kiro-krew/templates/kiro/agents/architect-prompt.md` and `task sync:check` passes.

**Implementation Approach**:

Add a new subsection under the **Workflow** or **Design Specification Requirements** section:

```markdown
## Concurrency Analysis (Required for All Changes)

When analyzing a GitHub issue and designing the solution, explicitly identify whether the change introduces **cross-goroutine access to shared state**.

### Concurrent Access Boundaries in Kiro-Krew

The following are goroutine boundaries where shared state access requires locking:
- **TUI render loop** (render goroutine) reading agent/watcher/session state
- **Command handlers** (main goroutine) reading/writing state concurrently with background loops
- **Watcher poll loop** (watcher goroutine) writing tracked issues while commands read them
- **Agent manager** (spawner goroutine) writing agent state while status commands read it

### Detection Rules

A change introduces a **concurrency concern** if it:
1. Adds a call from one goroutine to read/write a field in a type with a `sync.Mutex` or `sync.RWMutex`
2. Wires the TUI render path (e.g., `FooterManager`, `View()` methods) to read shared state from a background service (watcher, agent manager, session)
3. Adds a command handler that reads/writes state modified by a background goroutine

**Example**: Reading `Watcher` state from the footer/render path requires calling a lock-guarded accessor method like `Running()`, which must acquire `w.mu.RLock()` before reading `w.started`.

### Design Specification Requirements

When a change crosses a concurrency boundary:
1. **Call it out explicitly** in the Solution Approach section
2. **Add an acceptance criterion** in the task breakdown stating: "All access to shared field `X` is done under its lock" or "Accessor method `Y()` acquires the appropriate lock before reading/writing state"
3. **Require a concurrent test** as an acceptance criterion: "Add a test that exercises the accessor concurrently with state mutations so `go test -race` can observe the access pattern"

This ensures the builder knows the accessor must be lock-guarded and the validator can verify that both the locking and the concurrent test are present.
```

**Template Sync Command** (run after editing):
```bash
cp .kiro/agents/architect-prompt.md cmd/kiro-krew/templates/kiro/agents/architect-prompt.md
task sync:check
```

**Dependencies**: None

---

### Task 4: Add a Go concurrency rule to the builder conventions

**Objective**: Teach the builder agent the implementation pattern for thread-safe accessor methods and require concurrent tests.

**Acceptance Criteria**:
- `.kiro/skills/builder-conventions/SKILL.md` gains a concurrency subsection under a code-quality/implementation section stating: a field guarded by a mutex must only be read or written while holding that lock, **including through accessor methods**; when adding a call to shared state from a different goroutine (render path, command handler, background loop), confirm the accessor is lock-guarded.
- The same subsection requires that when a change adds a concurrent access to shared state, the builder adds a test that exercises the access concurrently so `go test -race` can observe it — a green race detector with no concurrent test is not evidence of safety.
- This section is **NOT** added to the templates (builder-conventions is a `*-conventions` skill and must not be synced); `task sync:check` still passes (it doesn't check skills).

**Implementation Approach**:

Add a new section under **Implementation Patterns** or **Project Standards**:

```markdown
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
```

**No template sync needed** — this is a `*-conventions` skill and is intentionally not distributed in templates.

**Dependencies**: None

---

### Task 5: Add a correctness / concurrency dimension to the validator conventions

**Objective**: Teach the validator agent that spec-compliance and passing tests are necessary but not sufficient — it must verify concurrent tests exist when a change touches shared state.

**Acceptance Criteria**:
- `.kiro/skills/validator-conventions/SKILL.md` gains guidance that spec-compliance and a green `task test` are **necessary but not sufficient**: when a change touches shared state or crosses a goroutine boundary, the validator must confirm a test actually exercises the concurrent path, and must FAIL the validation if shared state is read/written outside its lock or if no concurrent test covers a new cross-goroutine access.
- This is expressed in the validator's terms (a checkable criterion with PASS/FAIL evidence), consistent with the existing report template — e.g., an anti-pattern entry "green race detector with no concurrent test == PASS" alongside the existing "Ignoring QA Failures" anti-pattern.
- This section is **NOT** added to the templates (validator-conventions is a `*-conventions` skill and must not be synced); `task sync:check` still passes.

**Implementation Approach**:

Add a new subsection under **Common Anti-Patterns** and a new dimension under **Pass/Fail Decision Matrix**:

```markdown
### Anti-Pattern 5: Green Race Detector Without Concurrent Test

**❌ WRONG APPROACH**:

**Issue Criterion**:
> "Add `Running()` accessor to allow footer to display watcher status"

**Implementation**:
```go
// Unlocked accessor
func (w *Watcher) Running() bool {
    return w.started  // RACE: w.started written under lock in Start/Stop
}
```

**Validator Response** (INCORRECT):
```markdown
### Criterion: Running() accessor added
- **Status**: ✅ PASS
- **Evidence**: Executed `task test` — all tests pass under `-race`
- **Finding**: Race detector reports no issues

**QA Results**:
- Tests: ✅ PASS (including -race flag)
```

**Why This Is Wrong**: The race detector only flags races it **observes at runtime**. If no test exercises `Running()` concurrently with `Start()`/`Stop()`, the race goes undetected even though it exists in production (the footer render loop calls `Running()` while the watcher goroutine modifies `w.started`).

A green `-race` output with **no concurrent test** is not evidence of correctness — it's evidence that the test suite didn't exercise the race-prone path.

---

**✅ CORRECT APPROACH**:

**Validator checks for concurrent test**:

```bash
# Search for a test that exercises Running() concurrently
grep -A 20 "func Test.*Concurrent" internal/watcher/watcher_test.go
```

**If no concurrent test exists**, validation FAILS:

```markdown
### Criterion: Thread-safe Running() accessor
- **Status**: ❌ FAIL
- **Evidence**: 
  1. Inspected `Running()` implementation in internal/watcher/watcher.go:270
  2. Searched for concurrent test in watcher_test.go
  3. Ran `task test` with `-race`
- **Finding**: 
  - `Running()` reads `w.started` without acquiring lock (line 271)
  - `w.started` is written under `w.mu.Lock()` in Start/Stop (lines 42, 52)
  - No test exercises `Running()` concurrently with Start/Stop
  - Race detector passes only because no test drives the concurrent path
- **Verification Method**: Code inspection + test search + race detector

**Reasoning**: This is a **data race** — `Running()` reads `w.started` from the render goroutine while `Start()`/`Stop()` write it under lock from the watcher goroutine. The race detector didn't catch it because the test suite doesn't exercise `Running()` concurrently with state mutations. This is a **critical concurrency bug** and fails validation.

### Recommendations:
1. Add `w.mu.RLock()` + `defer w.mu.RUnlock()` to `Running()` before reading `w.started`
2. Add a concurrent test that calls `Running()` in a loop while another goroutine flips state via Start/Stop
3. Verify `go test -race ./internal/watcher/` passes after the fix
```

---

### Concurrency Verification Checklist

When a change **adds or modifies cross-goroutine access to shared state**, use this checklist:

- [ ] **Identify the shared state**: Does the change read/write a field in a struct with a `sync.Mutex` or `sync.RWMutex`?
- [ ] **Verify locking**: Does the accessor method acquire the lock before accessing the field?
- [ ] **Check for concurrent test**: Does a test exercise the accessor concurrently with writers?
- [ ] **Run race detector**: Does `go test -race` pass?
- [ ] **Confirm the test drives concurrency**: Would the test **fail** under `-race` if the lock were removed?

**FAIL validation if**:
- Shared state accessed without holding its lock (even if tests pass)
- No concurrent test exists for a new cross-goroutine access (even if `-race` is green)
- The concurrent test doesn't actually drive the race-prone path (e.g., sequential test mislabeled as "concurrent")

**Example criterion wording**:
```markdown
### Criterion: Thread-safe accessor for shared state
- **Type**: Implementation + Correctness
- **Specified Approach**: Lock-guarded accessor + concurrent test
- **Source**: Issue requires adding footer display of watcher status
```
```

**No template sync needed** — this is a `*-conventions` skill.

**Dependencies**: None

---

## Validation Commands

Run these commands to verify the implementation:

```bash
# 1. Race fix + concurrent test
go test -race ./internal/watcher/
# Expected: PASS (with Task 1 fix + Task 2 test)

# 2. Full suite under race (matches how the reviewer verified)
task test
# Expected: PASS

# 3. Formatting, lint, and — critically — template sync (architect prompt is synced)
task fmt:check
# Expected: PASS

task lint
# Expected: PASS

task sync:check
# Expected: PASS (architect-prompt.md synced to templates)

# 4. Build
task build
# Expected: SUCCESS

# 5. Verify the guidance actually landed (grep-checkable):
grep -n "RLock" internal/watcher/watcher.go
# Expected: Line showing w.mu.RLock() in Running() method

grep -niE "goroutine|shared state|mutex|lock" .kiro/agents/architect-prompt.md
# Expected: Multiple matches in new concurrency analysis section

grep -niE "mutex|accessor|-race|concurrent" .kiro/skills/builder-conventions/SKILL.md
# Expected: Multiple matches in new concurrency rules section

grep -niE "concurrent|goroutine|race" .kiro/skills/validator-conventions/SKILL.md
# Expected: Multiple matches in new anti-pattern and checklist sections
```

---

## Acceptance Criteria Summary

### Overall Success Criteria

- [ ] All 5 tasks completed
- [ ] `go test -race ./internal/watcher/` passes
- [ ] `task test` (full suite under race detector) passes
- [ ] `task fmt:check` passes
- [ ] `task lint` passes
- [ ] `task sync:check` passes (architect prompt synced to templates)
- [ ] `task build` succeeds
- [ ] Grep verification confirms guidance landed in all three agent files
- [ ] The `watcher_test.go` file exists with the concurrent regression test
- [ ] The race is fixed in `Running()` accessor
- [ ] All three agent guidance files updated with concurrency rules

### Out of Scope

- No changes to the footer feature's behavior (PR #15's feature is fine; only its accessor locking is wrong).
- No new features; this is a self-heal of the agent guidance plus the one accessor fix.
- No changes to files outside the explicitly listed scope.

---

## Implementation Notes

### Why This Is a Self-Healing Change

This issue teaches the AI pipeline to catch a class of bugs it previously shipped:

1. **Architect** now explicitly analyzes goroutine boundaries and requires lock-guarded access as an acceptance criterion
2. **Builder** now knows accessor methods must hold locks and must add concurrent tests
3. **Validator** now treats "green `-race` with no concurrent test" as insufficient evidence and FAILS validation when shared state is accessed without locks

**Result**: The next time a PR tries to add cross-goroutine access to shared state, the architect will call it out, the builder will implement it with locking + a concurrent test, and the validator will verify both exist before the PR ships.

### Template Sync Critical Path

**Only** `architect-prompt.md` is template-synced. The `*-conventions` skills are intentionally project-specific and must **not** be synced to templates.

After editing `architect-prompt.md`, **immediately run**:
```bash
cp .kiro/agents/architect-prompt.md cmd/kiro-krew/templates/kiro/agents/architect-prompt.md
task sync:check
```

If `task sync:check` fails, the templates are out of sync and the task cannot be marked complete.

### The Concrete Example

The issue names a specific example to ground the guidance: **reading `Watcher` state from the footer/render path**. This example should appear in:
- Architect prompt (concurrency analysis section)
- Builder conventions (cross-goroutine access section)
- Validator conventions (concurrency verification checklist)

Using a concrete, real example makes the abstract rule ("guard shared state with locks") actionable for the agents.

---

## Design Complete

This specification provides a complete, single-PR solution to issue #16 by:
1. Fixing the immediate race in `Watcher.Running()`
2. Adding a regression test that catches the race under `-race`
3. Teaching all three agent roles to prevent this bug class from shipping again

All acceptance criteria are achievable in one implementation cycle, tasks are organized for parallel execution where possible (Tasks 1, 3, 4, 5 can run in parallel; Task 2 depends on Task 1), and the design preserves the template-sync invariants (only architect prompt is synced; conventions skills are not).
