# Design Specification: Issue #21 - Teach Agents to Reconcile PR Completeness Claims Against the Diff

**Issue**: #21  
**Title**: Self-heal: teach agents to reconcile PR completeness claims against the diff  
**Repository**: matthiashowellyopp/howmux  
**Created**: 2026-09-09

Closes #21

---

## Problem Statement

PR #20 ("Rename project from howmux to howmux") merged as a clean, safe rename, but **claimed completeness it hadn't achieved**. The PR body made assertions that the diff did not verify:

1. **Environment variables claimed as updated but weren't**: The PR body stated `KIRO_KREW_WATCHER_PID` was "updated to `HOWMUX_WATCHER_PID`", but both the writer (`internal/agent/manager.go`) and reader (`internal/hotkey/detector.go`) still used the old name. Because writer and reader **agreed** on the old name, detection kept working and all tests passed — the gap was invisible to CI. (`KIRO_KREW_EVAL_TIMEOUT` in `internal/eval/runner.go` missed the same way.)

2. **Stray references claimed absent but present**: The body said "no stray howmux references remain" — there were ~24-38 in `.go` files, including one user-facing (the About overlay title).

3. **Config files not considered part of the rename surface**: Committed runtime artifacts under `.howmux/evals/results/` slipped in because `.gitignore` still pointed at the **old** `.howmux` runtime paths — a config file the "rename the code" pass never examined.

This is the **completeness sibling** of the concurrency lesson in #17 (validator-conventions "Anti-Pattern 5: Green Race Detector Without Concurrent Test"). Both are the same meta-failure: **a passing test suite is necessary but not sufficient, and the PR body asserts a property the diff does not verify.** Issue #17 taught the concurrency instance; this issue teaches the completeness / claim-reconciliation instance.

### Root Cause Analysis

The failure spanned three agent roles:

1. **Architect** — Did not enumerate the full surface of a sweeping mechanical change. The spec did not call out that config files (`.gitignore`, CI workflows) are part of the rename surface, not just `.go` files. It also didn't require verifying writer+reader symbol pairs move together.

2. **Builder** — Renamed the obvious code but not the tails (config files, env vars in non-obvious locations). Did not verify that writer+reader symbol pairs were renamed together (a still-agreeing old-name pair passes tests but is incomplete). The PR body claimed properties ("no stray references", "all env vars updated") that a repo-wide search would contradict.

3. **Validator** — Accepted the PR body's completeness claims without reconciling them against the actual diff/repo state. When a PR claims "renamed X", "no more Y", "all Z updated", the validator must **verify** the claim holds rather than trusting it.

---

## Solution Approach

Teach each agent their role in preventing "PR claims completeness the diff doesn't verify" failures through **three targeted guidance additions**:

### 1. Validator: Reconcile PR Claims Against the Diff

Add a new anti-pattern to `validator-conventions` (sibling to the existing "Anti-Pattern 5: Green Race Detector Without Concurrent Test") that requires the validator to **verify completeness claims** against the actual repository state.

**Key insight**: When a PR body makes completeness assertions ("renamed X", "no stray Y remain", "all Z updated"), those become **testable criteria** the validator must check via repo-wide search, not trust assertions.

**Trap detection**: Writer+reader pairs that still agree on the OLD name (like `KIRO_KREW_WATCHER_PID`) are the exact blind spot — tests pass because the system is internally consistent, but the rename is incomplete.

### 2. Builder: "Done" Definition for Sweeping/Mechanical Changes

Add guidance to `builder-conventions` defining what "done" means for mechanical changes (rename, move, version bump): **repo-wide search across all file types** (not just source: tests, docs, `.gitignore`, CI/build config, templates), and explicit verification that **writer and reader are renamed together** for env-var / symbol renames.

**Key insight**: Config files like `.gitignore` and CI workflows are part of the rename surface. A grep-based completeness check is the builder's responsibility before claiming "all X renamed" or "no stray Y".

### 3. Architect: Enumerate the Full Surface of a Mechanical Change

Add guidance to `architect-prompt.md` requiring that when an issue is a sweeping mechanical change, the spec must **enumerate the complete affected surface** — code, tests, docs, `.gitignore`, CI/build/release config, and template-synced files — as explicit acceptance criteria.

**Key insight**: Completeness must be designed in at the spec stage. If the architect enumerates `.gitignore` and CI workflows as part of the rename surface in the acceptance criteria, the builder knows to check them and the validator can verify they were addressed.

---

## Relevant Files

### Files to Modify

| File | Role | Change |
|------|------|--------|
| `.kiro/skills/validator-conventions/SKILL.md` | Validator guidance | Add Anti-Pattern 6 for unverified completeness claims |
| `.kiro/skills/builder-conventions/SKILL.md` | Builder guidance | Add "done" definition for mechanical changes |
| `.kiro/agents/architect-prompt.md` | Architect guidance | Add full-surface enumeration requirement for mechanical changes |
| `cmd/howmux/templates/kiro/agents/architect-prompt.md` | Template sync | Mirror architect-prompt.md (not builder/validator skills) |

### Files Not Modified

- `*-conventions` skills are **not** synced to templates (see builder-conventions "Exclusion Patterns")
- Only `architect-prompt.md` requires template sync; builder/validator skills stay local-only

---

## Team Orchestration

This is a **three-task parallel implementation** — each task modifies one agent guidance file independently. No dependencies between tasks (all three can run concurrently). After all three tasks complete, template sync verification ensures `architect-prompt.md` is mirrored correctly.

### Task Execution Flow

```
Task 1 (Validator) ─┐
Task 2 (Builder)   ─┼─→ Template Sync Verification → QA (lint/test/sync:check)
Task 3 (Architect) ─┘
```

All tasks can proceed in parallel since they modify different files with no overlapping content.

---

## Step-by-Step Task Breakdown

### Task 1: Validator — Reconcile PR Claims Against the Diff

**Objective**: Add a new anti-pattern to `validator-conventions` (sibling to "Anti-Pattern 5") teaching the validator to verify completeness claims in PR bodies against actual repository state.

**Acceptance Criteria**:
1. `.kiro/skills/validator-conventions/SKILL.md` gains a new anti-pattern section titled "Anti-Pattern 6: Trusting the PR Body's Completeness Claim" (or similar clear phrasing) placed immediately after Anti-Pattern 5
2. The anti-pattern states: When a PR body claims a completeness property — phrases like "renamed X", "no more Y / no stray Y remain", "all Z updated", "migrated to W" — the validator must **verify the claim against the actual diff/repo** and FAIL if it does not hold
3. Provides a **grep-checkable verification procedure** using PR #20 as the worked example:
   - For a rename: repo-wide search for the old token (`grep -rn <old-token>` across `*.go`, test files, docs, `.gitignore`, CI/build config, and templates) must return only justified/intentional matches
   - For env-var or symbol renames: verify the **writer and reader were both updated** (a writer/reader pair that still agrees on the OLD name is the exact trap — system works, tests pass, but rename is incomplete)
4. Expressed in the validator's existing terms (a checkable criterion with PASS/FAIL evidence, consistent with the structured report template already present in the skill)
5. References the concurrency anti-pattern (#5) as the sibling case: both are "passing tests are necessary but not sufficient, and the PR asserts a property the evidence doesn't verify"
6. The verification procedure is **actionable**: provides exact commands the validator can run (grep patterns, file globs) to check the claim

**Dependencies**: None (can run in parallel with Task 2 and Task 3)

**Verification Commands**:
```bash
# Verify anti-pattern was added
grep -niE "anti-pattern.*6|completeness.*claim|reconcile" .kiro/skills/validator-conventions/SKILL.md

# Verify it references writer/reader and stray references
grep -niE "writer.*reader|stray|grep -rn" .kiro/skills/validator-conventions/SKILL.md

# Verify placement after Anti-Pattern 5
awk '/Anti-Pattern 5/,/Anti-Pattern 6/' .kiro/skills/validator-conventions/SKILL.md | head -20
```

---

### Task 2: Builder — "Done" Definition for Sweeping/Mechanical Changes

**Objective**: Add guidance to `builder-conventions` defining what "done" means for mechanical changes: repo-wide search across **all file types** and explicit writer+reader verification for symbol/env-var renames.

**Acceptance Criteria**:
1. `.kiro/skills/builder-conventions/SKILL.md` gains a new subsection (suggest placement: after "Implementation Patterns" or "Project Standards") titled "Sweeping and Mechanical Changes" or similar
2. States that for mechanical changes (rename, move, version bump), **"done" requires a repo-wide search for the old token across all file types** — not just source code:
   - Source files (`*.go`, `*.ts`, etc.)
   - Test files
   - Documentation files (`.md`, `README`)
   - Configuration files (`.gitignore`, `Taskfile.yml`, `package.json`, `.releaserc.json`)
   - CI/build workflows (`.github/workflows/*.yml`, Jenkins, Makefiles)
   - Template-synced files (`cmd/howmux/templates/**`)
3. Requires that for **env-var / symbol renames** the builder verifies **writer and reader are renamed together** — a still-agreeing old-name pair passes tests but is not done (cite the `HOWMUX_WATCHER_PID` / `KIRO_KREW_WATCHER_PID` example from PR #20)
4. Requires the builder to **reconcile the PR/sentinel description to what actually changed** — do not claim a rename or "no stray references" that a `grep -rn` would contradict
5. Includes **concrete commands** the builder can run to verify completeness (e.g., `grep -rn "old-token" --include="*.go" --include="*.md" .`)
6. **Not synced to templates** (see "Exclusion Patterns" — `*-conventions` skills are local-only)

**Dependencies**: None (can run in parallel with Task 1 and Task 3)

**Verification Commands**:
```bash
# Verify the subsection was added
grep -niE "sweeping|mechanical.*change|rename.*move.*bump" .kiro/skills/builder-conventions/SKILL.md

# Verify it covers all file types
grep -niE "gitignore|CI.*workflow|templates|config" .kiro/skills/builder-conventions/SKILL.md

# Verify writer+reader guidance
grep -niE "writer.*reader|env.*var.*rename|symbol.*rename" .kiro/skills/builder-conventions/SKILL.md

# Verify it's NOT synced (this skill should not appear in templates)
! grep -r "builder-conventions" cmd/howmux/templates/
```

---

### Task 3: Architect — Enumerate the Full Surface of a Mechanical Change

**Objective**: Add guidance to `architect-prompt.md` requiring that when an issue is a sweeping mechanical change, the spec must enumerate the **complete affected surface** as explicit acceptance criteria.

**Acceptance Criteria**:
1. `.kiro/agents/architect-prompt.md` gains a new subsection (suggest placement: within "Design Specification Requirements" or as a new top-level section after "Concurrency Analysis") titled "Mechanical Change Surface Enumeration" or similar
2. States: When an issue is a **sweeping mechanical change** (rename, move across packages, version bump across dependencies), the spec must enumerate the **complete affected surface** as explicit acceptance criteria:
   - Source code (`.go`, language-specific files)
   - Test files
   - Documentation (README, docs/, comments)
   - Configuration files (`.gitignore`, `Taskfile.yml`, `package.json`, `.releaserc.json`, etc.)
   - CI/build/release workflows (`.github/workflows/*.yml`, Jenkins, Makefiles)
   - Template-synced files (see builder-conventions: `.kiro/agents/*.json`, `.kiro/agents/*.md`, scripts, themes)
3. Names the **concrete trap from PR #20**: Config files like `.gitignore` and CI workflows are part of the rename surface, not just source files — if the architect doesn't enumerate them, the builder won't check them
4. Provides guidance that acceptance criteria should state: "All references to `<old-token>` in source, tests, docs, `.gitignore`, CI config, and templates are renamed to `<new-token>`" (or equivalent explicit phrasing)
5. **Template-synced**: This prompt file MUST be copied to `cmd/howmux/templates/kiro/agents/architect-prompt.md` and `task sync:check` must pass

**Dependencies**: None (can run in parallel with Task 1 and Task 2)

**Verification Commands**:
```bash
# Verify the guidance was added to live prompt
grep -niE "mechanical.*change|sweep|full surface|gitignore|CI.*config|templates" .kiro/agents/architect-prompt.md

# Verify the template is synced
diff .kiro/agents/architect-prompt.md cmd/howmux/templates/kiro/agents/architect-prompt.md

# Verify template sync passes
task sync:check
```

---

## Validation Commands

All three tasks must pass these checks before the issue is complete:

### Grep-Based Content Verification

```bash
# Task 1: Validator anti-pattern added
grep -niE "anti-pattern.*6|completeness.*claim|reconcile|writer.*reader|stray" .kiro/skills/validator-conventions/SKILL.md

# Task 2: Builder mechanical-change guidance added
grep -niE "mechanical|sweeping|rename.*move.*bump|all file types|gitignore|writer.*reader" .kiro/skills/builder-conventions/SKILL.md

# Task 3: Architect full-surface enumeration added
grep -niE "mechanical.*change|full surface|gitignore|CI.*workflow|template" .kiro/agents/architect-prompt.md
```

### Template Sync Verification

```bash
# Architect prompt must be synced (Task 3)
task sync:check

# Builder/Validator skills must NOT be in templates (exclusion verified)
! grep -r "builder-conventions\|validator-conventions" cmd/howmux/templates/kiro/skills/
```

### Standard QA

```bash
# Formatting
task fmt:check

# Linting
task lint

# Tests (no test changes expected, but verify no regressions)
task test

# Build
task build
```

---

## Expected Outcomes

After this issue is complete:

### Validator Behavior Change

When reviewing a PR that claims "renamed X" or "no stray Y remain", the validator will:
1. Identify the completeness claim in the PR body
2. Run a repo-wide search for the old token (`grep -rn "old-token"`)
3. **FAIL validation** if the old token appears in unexpected locations (source, tests, docs, config, CI, templates) and the PR claimed it was renamed
4. For env-var/symbol renames, verify writer+reader pairs both moved (not just one)

### Builder Behavior Change

When implementing a mechanical change (rename, move), the builder will:
1. Before claiming "done", run a repo-wide search for the old token across **all file types** (not just `.go` files)
2. Verify config files (`.gitignore`, `Taskfile.yml`, CI workflows) are part of the surface
3. For env-var/symbol renames, confirm writer and reader were renamed together
4. Reconcile PR/sentinel description to actual changes (do not claim "no stray references" that `grep` contradicts)

### Architect Behavior Change

When designing a mechanical change (rename, move), the architect will:
1. Enumerate the **complete affected surface** as explicit acceptance criteria
2. List source, tests, docs, `.gitignore`, CI/build config, and templates as separate acceptance criteria
3. Require writer+reader verification for env-var/symbol renames in the acceptance criteria
4. Ensure completeness is designed in (not discovered during validation)

### Concrete Prevention of PR #20's Failure Class

This guidance addition prevents the exact failure in PR #20:
- **Architect** would have enumerated `.gitignore` and CI config as part of the rename surface in acceptance criteria
- **Builder** would have searched `.gitignore` and found the old `.howmux` paths, and verified `KIRO_KREW_WATCHER_PID` writer+reader both moved
- **Validator** would have rejected the PR body's claim "no stray references" by running `grep -rn howmux` and finding 24–38 instances

---

## Out of Scope

- **No code changes**: PR #20's rename is already fixed on main. This issue only teaches the guidance so the failure class does not recur.
- **No restatement of the concurrency lesson**: Issue #17 is already merged. Reference it as the sibling but add the completeness case as a new anti-pattern.
- **No changes to unrelated agent prompts**: Only modify the three specified guidance files.

---

## References

- **Issue #21**: This specification
- **PR #20**: The motivating example (rename that claimed completeness it hadn't achieved)
- **Issue #17**: The sibling lesson (concurrency: "Green Race Detector Without Concurrent Test")
- **Anti-Pattern 5** in `validator-conventions`: The structural template for Anti-Pattern 6
- **Builder-conventions "Exclusion Patterns"**: Why `*-conventions` skills are not synced to templates
- **Builder-conventions "Mandatory Template Synchronization"**: Why `architect-prompt.md` IS synced

---

## Success Criteria Summary

| Criterion | Evidence |
|-----------|----------|
| Validator anti-pattern added | `grep` finds "Anti-Pattern 6" and "completeness claim" in validator-conventions |
| Builder mechanical-change guidance added | `grep` finds "sweeping/mechanical" and "all file types" in builder-conventions |
| Architect full-surface enumeration added | `grep` finds "mechanical change" and "gitignore" in architect-prompt |
| Template sync passes | `task sync:check` exits 0 |
| QA passes | `task fmt:check && task lint && task test && task build` all exit 0 |
| Guidance is grep-checkable | All acceptance criteria verifiable via grep + task commands |
| No template pollution | `*-conventions` skills NOT in `cmd/howmux/templates/` |

All acceptance criteria must be met in one PR. No phased delivery.