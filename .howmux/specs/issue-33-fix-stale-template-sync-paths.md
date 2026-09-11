# Design Specification: Fix Stale Template-Sync Paths + Gate Builder Completion on Sync:Check

**Issue**: #33  
**Closes**: #33

## Problem Statement

Agents repeatedly create new files under template-synced directories (`.howmux/evals/`, `.howmux/scripts/`, `.howmux/themes/`, `.kiro/agents/`) but fail to copy them to their template mirrors under `cmd/howmux/templates/`, causing `task sync:check` to fail in CI. This has caused failures in multiple PRs:

- **PR #32**: Added `.howmux/evals/cases/krew-lead/*.yaml` and `.howmux/evals/fixtures/jira-sync-issue.md` without mirroring to templates
- **PR #20**: Missed `.gitignore` and config surface during the kiro-krew → howmux rename

### Root Causes

1. **Stale sync commands in builder guidance**: `.kiro/skills/builder-conventions/SKILL.md` still documents the OLD paths from before the kiro-krew → howmux rename:
   - OLD: `.kiro-krew/evals/` → `cmd/kiro-krew/templates/kiro-krew/evals/`
   - NEW: `.howmux/evals/` → `cmd/howmux/templates/howmux/evals/`
   
   An agent following the documented commands copies to paths that no longer exist, so sync silently stays broken. The `sync:check` task itself already uses correct new paths, creating a mismatch between guidance and verification.

2. **Missing completion gate**: The builder workflow lists sync among steps but doesn't make "run `task sync:check` and copy any new/changed files" a hard gate before marking a task complete. Missing new-file copies aren't caught until CI.

## Solution Approach

This is a **documentation-only fix** to `.kiro/skills/builder-conventions/SKILL.md`:

1. **Update stale paths** in the sync mappings table and sync commands to reflect the current post-rename directory structure
2. **Make sync verification a hard completion gate** by adding explicit requirements that:
   - Before completing any task that touches synced surfaces, agent must copy new/changed files to templates
   - Agent must run `task sync:check` and confirm it passes locally (not just rely on CI)
   - Guidance explicitly calls out that **newly created files** (not just edits to existing ones) must be mirrored
3. **Enumerate synced surfaces** so agents know what directories require mirroring
4. **Provide fallback** for running underlying `diff -rq` commands if `task` binary unavailable

### Why This Fixes the Problem

- **Prevents stale path copies**: Agents will copy to correct locations that actually exist
- **Catches missing mirrors before CI**: Making `sync:check` a pre-completion gate surfaces issues locally
- **Explicit new-file awareness**: Guidance will specifically call out that new files under synced dirs must be mirrored (the exact failure mode from PR #32)
- **Verification alignment**: Documented paths will match what `Taskfile.yml` compares

## Relevant Files

- `.kiro/skills/builder-conventions/SKILL.md` — **EDIT ONLY** (this is the single file in scope)
- `Taskfile.yml` — reference only (already correct, used to verify alignment)

## Concurrency Analysis

**Not Applicable**: This change is documentation-only. No code changes, no goroutine boundaries crossed, no shared state access.

## Team Orchestration

Single-agent task (builder only). No dependencies or parallel work streams.

## Step-by-Step Task Breakdown

### Task 1: Correct Stale Sync Paths to Post-Rename Layout

**What to do**:
Update all path references in `.kiro/skills/builder-conventions/SKILL.md` from the old kiro-krew layout to the current howmux layout.

**Specific changes required**:

1. **Sync Mappings table** (around line 11-19):
   - Change `.kiro/agents/*.json` template path from `cmd/kiro-krew/templates/kiro/agents/` to `cmd/howmux/templates/kiro/agents/`
   - Change `.kiro/agents/*.md` template path from `cmd/kiro-krew/templates/kiro/agents/` to `cmd/howmux/templates/kiro/agents/`
   - Change `.kiro-krew/scripts/*.sh` live path to `.howmux/scripts/*.sh` and template path from `cmd/kiro-krew/templates/kiro-krew/scripts/` to `cmd/howmux/templates/howmux/scripts/`
   - Change `.kiro-krew/themes/*.yaml` live path to `.howmux/themes/*.yaml` and template path from `cmd/kiro-krew/templates/kiro-krew/themes/` to `cmd/howmux/templates/howmux/themes/`
   - Change `.kiro-krew/evals/fixtures/*` live path to `.howmux/evals/fixtures/*` and template path from `cmd/kiro-krew/templates/kiro-krew/evals/fixtures/` to `cmd/howmux/templates/howmux/evals/fixtures/`
   - Change `.kiro-krew/evals/rubrics/*` live path to `.howmux/evals/rubrics/*` and template path from `cmd/kiro-krew/templates/kiro-krew/evals/rubrics/` to `cmd/howmux/templates/howmux/evals/rubrics/`
   - Change `.kiro-krew/evals/cases/**/*` live path to `.howmux/evals/cases/**/*` and template path from `cmd/kiro-krew/templates/kiro-krew/evals/cases/` to `cmd/howmux/templates/howmux/evals/cases/`

2. **Sync Commands** section (around line 35-51):
   - Agent JSON commands: Change paths from `cmd/kiro-krew/templates/kiro/agents/` to `cmd/howmux/templates/kiro/agents/`
   - Scripts: Change from `.kiro-krew/scripts/` → `cmd/kiro-krew/templates/kiro-krew/scripts/` to `.howmux/scripts/` → `cmd/howmux/templates/howmux/scripts/`
   - Themes: Change from `.kiro-krew/themes/` → `cmd/kiro-krew/templates/kiro-krew/themes/` to `.howmux/themes/` → `cmd/howmux/templates/howmux/themes/`
   - Evals: Change from `.kiro-krew/evals/` → `cmd/kiro-krew/templates/kiro-krew/evals/` to `.howmux/evals/` → `cmd/howmux/templates/howmux/evals/`

**Acceptance Criteria**:
- The Sync Mappings table uses current paths: live `.howmux/{scripts,themes,evals}` → template `cmd/howmux/templates/howmux/{scripts,themes,evals}`
- The Sync Mappings table uses current paths: live `.kiro/agents/*` → template `cmd/howmux/templates/kiro/agents/`
- All sync commands in the Sync Commands section use the correct current paths
- Zero remaining references to old `.kiro-krew/` or `cmd/kiro-krew/templates/` paths in the sync guidance sections
- Documented paths exactly match what `task sync:check` compares in `Taskfile.yml` (verify by reading lines 61-64 of Taskfile.yml)
- Verification command: `grep -n "kiro-krew" .kiro/skills/builder-conventions/SKILL.md` returns zero matches in sync-related sections (Sync Mappings, Sync Commands, Verification)

**Dependencies**: None

---

### Task 2: Make Sync Verification a Hard Completion Gate for New Files

**What to do**:
Update the "Workflow Integration" and "Sentinel File Requirements" sections to make sync verification mandatory before task completion, with explicit awareness of newly created files.

**Specific changes required**:

1. **Workflow Integration section** (around line 53-65):
   - Add explicit statement: "If your task created or modified files under any synced directory, you MUST copy them to their template mirrors before completion"
   - Add explicit call-out: "**Newly created files** under synced directories must be mirrored, not just edits to existing files"
   - Make "run `task sync:check` and confirm it passes" a required step, not optional
   - Add: "Task cannot be marked complete if `sync:check` fails"
   - Enumerate the synced surfaces so agents know what to check: `.howmux/scripts/`, `.howmux/themes/`, `.howmux/evals/{cases,fixtures,rubrics}`, `.kiro/agents/`
   - Explicitly exclude `*-conventions` skills from sync (already documented, reinforce here)
   - Add fallback: "If `task sync:check` cannot be run (task binary unavailable), run the underlying verification commands directly" with the actual diff commands from Taskfile.yml

2. **Sentinel File Requirements section** (around line 67-82):
   - Strengthen the requirement: sync verification status is MANDATORY in sentinel files for any task touching synced surfaces
   - Update example to show what to document when new files were created
   - Example should show: "Created new files: [list] — mirrored to templates and verified with sync:check"

**New content to add** (suggested structure):

```markdown
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
```

**Acceptance Criteria**:
- Workflow Integration section states that before completing tasks touching synced surfaces, agent must copy new/changed files to templates AND run `task sync:check`
- Guidance explicitly calls out that **newly created files** (not only edits) must be mirrored
- Synced surfaces are enumerated in a checklist or list format
- `*-conventions` skills explicitly excluded from sync
- Fallback commands provided for when `task` binary unavailable (direct `diff -rq` and `go run scripts/compare-templates.go`)
- Sentinel File Requirements section updated to mandate sync verification status for tasks touching synced surfaces
- Example sentinel content shows what to document when new files are created and mirrored
- Verification command: `grep -niE "sync:check|newly created|new file|Synced Surfaces" .kiro/skills/builder-conventions/SKILL.md` produces multiple matches showing the new requirements

**Dependencies**: Task 1 (needs correct paths to reference in checklist)

---

### Task 3: Update Sentinel File Requirements Example

**What to do**:
Enhance the sentinel file example in the "Sentinel File Requirements" section to demonstrate what to document when new files are created under synced directories.

**Specific changes required**:

Update the example around line 73-82 to show:
- How to document newly created files under synced dirs
- What sync verification output to include
- How to list which sync commands were used

**New example** (replace or augment existing):

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

**Acceptance Criteria**:
- Sentinel file example shows how to document newly created files under synced directories
- Example shows the specific sync commands used to mirror new files
- Example demonstrates excluding local-only entries when syncing agent JSON
- Example shows what to report when `task sync:check` passes
- Verification command: Example in `.kiro/skills/builder-conventions/SKILL.md` includes "Created: " entries and demonstrates mirroring new files

**Dependencies**: Task 1, Task 2

## Validation Commands

Run these commands to verify the specification is correctly implemented:

```bash
# 1. No stale kiro-krew paths remain in sync guidance
grep -n "kiro-krew" .kiro/skills/builder-conventions/SKILL.md
# Expected: Zero matches in Sync Mappings, Sync Commands, Verification sections
# (May have matches in other sections like examples or historical context — that's fine)

# 2. New completion gate language present
grep -niE "sync:check|newly created|new file|Synced Surfaces|cmd/howmux/templates" .kiro/skills/builder-conventions/SKILL.md
# Expected: Multiple matches showing the new requirements and correct paths

# 3. Verify sync commands use correct paths by comparing to Taskfile.yml
# Manually verify that paths in sync commands match Taskfile.yml lines 61-64:
# - .howmux/scripts/ → cmd/howmux/templates/howmux/scripts/
# - .howmux/themes/ → cmd/howmux/templates/howmux/themes/
# - .howmux/evals/ → cmd/howmux/templates/howmux/evals/
# - .kiro/agents/ compared via go run scripts/compare-templates.go

# 4. The conventions skill itself is not synced, everything still passes
task sync:check
task fmt:check
task lint
task test
task build

# All tasks must pass ✅
```

## Implementation Notes

### This is Documentation-Only

- **No code changes** — only editing `.kiro/skills/builder-conventions/SKILL.md`
- **No template sync needed** — `builder-conventions` is explicitly excluded from sync (it's a `*-conventions` skill)
- **CI must still pass** — `task sync:check` must pass after this change (nothing about the actual templates changes, only the documentation)

### Path Verification Strategy

Before claiming complete, run this verification to ensure documented paths exactly match what `sync:check` compares:

```bash
# Extract the paths sync:check actually uses
grep -A10 "sync:check:" Taskfile.yml | grep -E "diff -rq|go run scripts/compare-templates"

# Expected output (Taskfile.yml lines 61-64):
#   go run scripts/compare-templates.go --agents-only >/dev/null || error=1
#   diff -rq .howmux/scripts/ cmd/howmux/templates/howmux/scripts/ || error=1
#   diff -rq .howmux/themes/ cmd/howmux/templates/howmux/themes/ || error=1
#   diff -rq --exclude=results --exclude=.DS_Store --exclude=tmp .howmux/evals/ cmd/howmux/templates/howmux/evals/ || error=1

# Then verify your documented sync commands use identical paths
```

### Real-World Example: PR #32 (What This Prevents)

**What happened**:
- Agent created new files: `.howmux/evals/cases/krew-lead/*.yaml` and `.howmux/evals/fixtures/jira-sync-issue.md`
- Agent marked task complete without mirroring to `cmd/howmux/templates/howmux/evals/`
- CI ran `task sync:check` → failed with "Only in .howmux/evals/..."
- Had to manually copy files and push again

**What this fix does**:
1. Agent sees explicit requirement: "newly created files under `.howmux/evals/` must be mirrored"
2. Agent sees checklist: `.howmux/evals/cases/` → `cmd/howmux/templates/howmux/evals/cases/`
3. Agent runs sync commands with correct paths
4. Agent runs `task sync:check` locally before claiming complete
5. Issue caught and fixed before push/CI

### Writer and Reader Must Move Together (Not Applicable Here)

This change is documentation-only — no env vars, no symbols, no writer/reader pairs. The "writer and reader" verification rules don't apply.

## Completeness Criteria

The implementation is complete when:

1. ✅ All path references in Sync Mappings table use post-rename layout
2. ✅ All sync commands use correct `.howmux/` and `cmd/howmux/templates/howmux/` paths
3. ✅ Zero `grep -n "kiro-krew"` matches in sync guidance sections
4. ✅ Workflow Integration section makes sync verification a hard gate
5. ✅ Guidance explicitly calls out newly created files must be mirrored
6. ✅ Synced surfaces enumerated in checklist format
7. ✅ Fallback verification commands provided
8. ✅ Sentinel file example updated to show new-file mirroring
9. ✅ `task sync:check` passes (conventions skill not synced, no drift introduced)
10. ✅ All other QA tasks pass: `task fmt:check`, `task lint`, `task test`, `task build`

## Out of Scope

- **No changes to `task sync:check`** — it's already correct
- **No changes to templates** — this only updates documentation
- **No code changes** — purely documentation fix
- **No changes to other skills or agent configs**

## Success Metrics

After this change, agents will:
1. Use correct sync paths that actually exist
2. Catch sync failures locally before CI
3. Explicitly handle newly created files under synced dirs
4. Have a clear checklist of what needs mirroring

This prevents the PR #32 class of failure (new files under synced dir not mirrored → CI sync:check fails).
