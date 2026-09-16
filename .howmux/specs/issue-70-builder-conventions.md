# Design Specification: Builder Convention for I/O Seam Testing

**Issue**: #70  
**Title**: Builder convention: route I/O through an injectable seam and test the wiring layer  
**Closes**: #70

## Context

Across three consecutive PRs (#67, #68, #69) in the PR-review workflow series, the same review finding recurred: the builder produces clean, well-tested pure functions, then places the real branching logic inside a "thin wiring" layer that shells out (`exec.Command`) or touches the filesystem — and leaves that layer untested. The bugs then surfaced in review, in exactly that layer:

- **PR #67** (`store.Save`): Fixed temp-name clobbering + missing fsync — the write wiring
- **PR #69** (`EnsureCheckout`): Force-push refresh, partial-failure cleanup, wrong-repo target, and fragile `argv[1]` cwd logic — all in the untested command-runner wiring, which the PR itself described as "thin wiring, intentionally not unit tested"

The pure/impure split is good practice; the gap is that the impure side gets treated as too trivial to test when it actually holds the nontrivial logic.

## Solution Approach

This issue adds **conventions and issue hygiene** only — no product code changes are required. The solution has two components:

### 1. Add I/O Seam Testing Convention to builder-conventions SKILL

Add a new section to `.kiro/skills/builder-conventions/SKILL.md` following the existing "rule + why + example" style, documenting:

- **The Rule**: When code performs I/O (subprocess via `exec.Command`, filesystem mutation, network), route the execution through a small injectable seam — a package-level function variable or an interface parameter — so tests can substitute a fake
- **The Rationale**: The wiring logic (which commands, in what order, in which working dir; clone-vs-refresh and similar branching; failure/cleanup paths) must be unit-tested against the fake, asserting the command sequence, arguments, and cwd — with no real subprocess or network. "Thin wiring" is not an exemption from testing. If a layer contains any branching, ordering, or cleanup decision, it must be tested.
- **Implementation Pattern**: Keep the seam minimal and unexported where possible; production keeps the real implementation as the default value
- **Go Example**: Include a concrete example mirroring the `runCommand` seam pattern now used in `internal/review/checkout.go`

The convention explicitly rejects "thin wiring is exempt from tests."

### 2. Update Issues #62 and #64 with Seam/Testability Requirements

Tighten the acceptance criteria on issues #62 (review runner) and #64 (watch loop) to require: any subprocess / `gh` / `git` / `kiro-cli` invocation goes through an injectable seam, and the dispatch/branching/cleanup logic is unit-tested via a fake runner (no real external process) — consistent with the parsing + argv + decision-function test scope already stated there.

Add a new "I/O seam + wiring tests" section to both issues (it has already been added to the fetched versions above, but the design spec must ensure the builder knows this is part of the task).

## Relevant Files

### Files to Modify

| File | Purpose | Changes Required |
|------|---------|------------------|
| `.kiro/skills/builder-conventions/SKILL.md` | Builder agent conventions | Add new "I/O Seam + Test the Wiring" section |
| Issue #62 body (via `gh issue edit`) | Review runner acceptance criteria | Already updated with seam requirement |
| Issue #64 body (via `gh issue edit`) | Watch loop acceptance criteria | Already updated with seam requirement |

### Reference Files (Read-Only)

| File | Purpose |
|------|---------|
| `internal/review/checkout.go` | Example of injectable seam pattern (`runCommand` var) |
| `internal/review/checkout_test.go` | Example of testing wiring logic via fake runner |
| `internal/review/store.go` | Example from PR #67 (atomic write with fsync) |

### Template Synchronization

**Critical**: The `.kiro/skills/builder-conventions/SKILL.md` file is explicitly **excluded** from template synchronization per the "Exclusion Patterns" rule in the existing builder-conventions:

> **Never sync `*-conventions` skills** — they are project-specific and must NOT be distributed in templates.

Therefore, modifying this file does **NOT** require mirroring to templates, and `task sync:check` will not flag it as drift.

## Team Orchestration

This is a **single-task issue** with no parallelization opportunities:

1. **Documentation Task**: Add the I/O seam convention to builder-conventions SKILL
2. **Issue Update Task**: Update issues #62 and #64 (these were already updated in the fetched bodies, but verification is required)

The builder must complete both components in sequence within a single PR.

## Step-by-Step Task Breakdown

### Task 1: Add I/O Seam Testing Convention to builder-conventions SKILL

**Objective**: Document the injectable seam pattern for I/O operations in the builder conventions.

**Steps**:

1. Read `.kiro/skills/builder-conventions/SKILL.md` to understand the existing structure and style
2. Read `internal/review/checkout.go` (lines 60-68) to extract the `runCommand` seam pattern as the Go example
3. Read `internal/review/checkout_test.go` (lines 74-82, and test functions) to understand the test pattern
4. Add a new section titled "## I/O Seam + Test the Wiring" after the existing sections but before any project-specific notes
5. Follow the existing "rule + why + example" structure used in other conventions
6. Include:
   - **Rule**: When to use injectable seams (subprocess, filesystem, network I/O)
   - **What Must Be Tested**: Command sequence, arguments, working directory, branching/cleanup logic
   - **Pattern**: Package-level function variable (or interface parameter), keep minimal/unexported, production default
   - **Explicit Rejection**: "Thin wiring is not an exemption from testing"
   - **Go Example**: Based on `runCommand` pattern from `checkout.go`:
     ```go
     var runCommand = func(c command) error {
         cmd := exec.Command(c.argv[0], c.argv[1:]...)
         cmd.Dir = c.dir
         output, err := cmd.CombinedOutput()
         if err != nil {
             return fmt.Errorf("command %v failed: %w\nOutput: %s", c.argv, err, output)
         }
         return nil
     }
     ```
   - **Test Example**: Show how tests swap in a fake runner:
     ```go
     func withFakeRunner(t *testing.T) *[]command {
         orig := runCommand
         var recorded []command
         runCommand = func(c command) error {
             recorded = append(recorded, c)
             return nil
         }
         t.Cleanup(func() { runCommand = orig })
         return &recorded
     }
     ```

**Acceptance Criteria**:
- New "I/O Seam + Test the Wiring" section added to `.kiro/skills/builder-conventions/SKILL.md`
- Section follows existing style (rule, rationale, pattern, example)
- Explicitly states "thin wiring is not an exemption from testing"
- Includes concrete Go example based on `runCommand` pattern
- Includes test example showing fake substitution and command assertion
- **Verification**: `grep -n "I/O Seam" .kiro/skills/builder-conventions/SKILL.md` returns the new section header
- **Verification**: `grep -n "thin wiring is not an exemption" .kiro/skills/builder-conventions/SKILL.md` returns a match (case-insensitive acceptable)

**Dependencies**: None

---

### Task 2: Verify Issues #62 and #64 Have Seam Requirements

**Objective**: Confirm that issues #62 and #64 include the I/O seam + wiring test requirement in their acceptance criteria.

**Steps**:

1. Fetch current issue #62 body: `gh issue view 62 --repo matthiashowellyopp/howmux --json body`
2. Verify it contains a section titled "## I/O seam + wiring tests (added per #70)"
3. Verify the section includes the requirement: "Any subprocess / `gh` / `git` / `kiro-cli` invocation in this issue must go through an **injectable seam**"
4. Fetch current issue #64 body: `gh issue view 64 --repo matthiashowellyopp/howmux --json body`
5. Verify it contains a section titled "## I/O seam + wiring tests (added per #70)"
6. Verify the section includes the requirement: "Any subprocess / `gh` / `git` / `kiro-cli` invocation in this issue must go through an **injectable seam**"

**Acceptance Criteria**:
- Issue #62 body contains "## I/O seam + wiring tests (added per #70)" section
- Issue #62 body requires injectable seam for subprocess/gh/git/kiro-cli invocations
- Issue #64 body contains "## I/O seam + wiring tests (added per #70)" section
- Issue #64 body requires injectable seam for subprocess/gh/git/kiro-cli invocations
- **Verification**: `gh issue view 62 --json body -q .body | grep -c "I/O seam"` returns 1 or more
- **Verification**: `gh issue view 64 --json body -q .body | grep -c "I/O seam"` returns 1 or more

**Note**: Based on the fetched issue bodies earlier in this session, both issues already contain the required sections. This task is verification only — if the sections are already present, document this in the sentinel file and proceed. If they are missing (unexpected), update them via `gh issue edit`.

**Dependencies**: Task 1 (conceptual precedence, but can run in parallel if needed)

---

## Validation Commands

After completing both tasks, run the following validation commands:

### 1. Verify Convention Added to SKILL

```bash
# Check that the new section exists
grep -n "I/O Seam" .kiro/skills/builder-conventions/SKILL.md

# Check for the explicit rejection of thin-wiring exemption
grep -in "thin wiring.*not.*exempt" .kiro/skills/builder-conventions/SKILL.md

# Check for the runCommand example
grep -n "var runCommand" .kiro/skills/builder-conventions/SKILL.md
```

**Expected**: All three greps return matches with line numbers.

### 2. Verify Issues Updated

```bash
# Check issue #62 has seam requirement
gh issue view 62 --repo matthiashowellyopp/howmux --json body -q .body | grep "I/O seam"

# Check issue #64 has seam requirement
gh issue view 64 --repo matthiashowellyopp/howmux --json body -q .body | grep "I/O seam"
```

**Expected**: Both greps return the section header or requirement text.

### 3. Verify Template Sync Not Required

```bash
# Confirm builder-conventions is not in template sync surface
task sync:check
```

**Expected**: `task sync:check` passes (or reports only unrelated drift). The `*-conventions` exclusion rule means changes to `.kiro/skills/builder-conventions/SKILL.md` do NOT trigger sync drift.

### 4. Standard QA

```bash
# Linting (if applicable to markdown in skills)
task lint

# Format check
task fmt:check

# Tests (no behavior change, so existing tests should still pass)
task test
```

**Expected**: All QA commands pass.

## Implementation Notes

### No Behavioral Changes

This issue modifies **documentation and issue acceptance criteria only**. No product code changes are required. The following are explicitly **out of scope**:

- Refactoring existing code to add seams (that work happens in #62 and #64)
- Creating new tests for existing untested wiring layers
- Modifying CI or build configuration
- Adding new packages or modules

### SKILL Structure Guidance

The builder should follow the existing pattern in `builder-conventions/SKILL.md`:

- Each convention is a `##` section with a descriptive title
- Conventions follow a consistent structure: rule statement, rationale, pattern/example
- Code examples are in fenced code blocks with language tags (`go`, `bash`, etc.)
- The tone is prescriptive ("must", "should") rather than suggestive

### Example Section Structure

```markdown
## I/O Seam + Test the Wiring

**Rule**: [Clear statement of when and how to use injectable seams]

**Rationale**: [Why this matters, what problems it solves, what "thin wiring" misconception it corrects]

**Pattern**: [How to implement the seam — package var, interface param, keep minimal]

**Go Example**:
```go
// [Concrete example based on runCommand]
```

**Test Example**:
```go
// [Concrete example based on withFakeRunner]
```

**What Must Be Tested**: [Command sequence, arguments, cwd, branching, cleanup]
```

### Concurrency Considerations

**No concurrency concerns** for this issue. The changes are to:
- A static markdown file (`.kiro/skills/builder-conventions/SKILL.md`)
- Issue bodies via `gh issue edit` (one-shot operations)

No goroutine boundaries, no shared state, no mutex-guarded fields involved.

## Success Criteria

The implementation is complete when:

1. ✅ `.kiro/skills/builder-conventions/SKILL.md` has a new "I/O Seam + Test the Wiring" section
2. ✅ The section includes rule, rationale, pattern, and Go examples
3. ✅ The section explicitly rejects "thin wiring is exempt from tests"
4. ✅ Issues #62 and #64 include the I/O seam requirement in acceptance criteria
5. ✅ `task sync:check` passes (no template drift introduced)
6. ✅ All standard QA commands pass
7. ✅ PR created with title "Add builder convention for I/O seam testing"

## Quality Assurance

### Pre-Commit Checks

```bash
# Run all discovered QA tools
task lint
task fmt:check
task test
task sync:check
```

### Verification After Merge

The convention will be applied in issues #62 and #64, where the builder will:
- Route subprocess invocations through injectable seams
- Write unit tests asserting command sequences via fakes
- Avoid the "thin wiring exemption" anti-pattern

This design spec ensures future PRs in the series will test the wiring layer.

## References

- **Issue #70**: https://github.com/matthiashowellyopp/howmux/issues/70
- **Issue #62**: https://github.com/matthiashowellyopp/howmux/issues/62
- **Issue #64**: https://github.com/matthiashowellyopp/howmux/issues/64
- **PR #67**: store.Save atomic write (untested fsync wiring)
- **PR #68**: (intermediate PR in series)
- **PR #69**: EnsureCheckout command wiring (untested branching/cleanup)
- **Reference Implementation**: `internal/review/checkout.go` (runCommand seam)
- **Reference Tests**: `internal/review/checkout_test.go` (fake runner pattern)
