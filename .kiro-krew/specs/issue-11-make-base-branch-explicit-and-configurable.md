# Design Specification: Make Base Branch Explicit and Configurable

**Issue:** #11  
**Title:** Make base branch explicit and configurable for worktrees and PRs  
**Status:** Ready for Implementation  
**Closes:** #11

## Problem Statement

When the watcher processes an issue, it creates a git worktree and branch, and the krew-lead agent later opens a PR. Today the **base** those are built on is implicit and inconsistent:

- `worktree-create.sh` runs `git worktree add <path> -b spec/<name>` with **no start point**, so the new branch forks from whatever commit is checked out when the watcher runs (ambient shell state), not from a declared base.
- `krew-lead-prompt.md` instructs `gh pr create` with **no `--base`**, so the PR targets the repo's default branch (`main`).

**Consequence**: If the operator runs the watcher while a non-default branch is checked out (e.g., an integration branch like `jira-work`), every `spec/*` branch silently forks from that branch, but its PR still targets `main` — producing a PR diff polluted with the integration branch's unmerged commits and possible merge conflicts. The base is decided by ambient state instead of configuration, which is surprising and error-prone.

## Solution Approach

Make the base branch explicit and configurable at three levels:

1. **Config-level default** — Add `base_branch` field to `.kiro-krew/config.yaml`, defaulting to `"main"`
2. **Per-issue override** — Parse `Base-Branch: <branch-name>` from issue body
3. **Script enhancement** — Make `worktree-create.sh` accept an optional base ref argument
4. **Manager integration** — Pass resolved base branch to worktree creation at both call sites
5. **PR creation** — Add explicit `--base` flag to `gh pr create` command in krew-lead prompt

**Resolution precedence**: issue override → config → `"main"` default

This enables integration workflows to specify `Base-Branch: jira-work` in issue bodies while normal issues continue targeting `main` by default, all without manual intervention or ambient state surprises.

## Relevant Files

### Files to Modify

1. **internal/config/config.go**
   - Add `BaseBranch string` field to `Config` struct with yaml tag `base_branch`
   - Set default value `"main"` in `Load()` function alongside other defaults
   - No validation needed (any string is valid)

2. **internal/agent/manager.go**
   - Add helper function `extractBaseBranchOverride(issueBody string) string` to parse `Base-Branch: <value>` from issue body
   - Add helper function `resolveBaseBranch(cfg *Config, issueBody string) string` to implement precedence logic
   - Update initial spawn (~line 191): Pass resolved base branch as second argument to `worktree-create.sh`
   - Update retry path (~line 457): Pass resolved base branch as second argument when recreating worktrees
   - Both call sites need access to issue body to resolve per-issue overrides

3. **.kiro-krew/scripts/worktree-create.sh**
   - Accept optional second argument `$2` for base ref
   - When base ref is provided: use `git worktree add "$WORKTREE_PATH" -b "$BRANCH_NAME" "$BASE_REF"`
   - When base ref is omitted: preserve current behavior `git worktree add "$WORKTREE_PATH" -b "$BRANCH_NAME"`
   - Maintain backward compatibility for standalone/manual usage

4. **.kiro/agents/krew-lead-prompt.md** (live version)
   - Update step 8 "Create PR" command to include `--base <base_branch>`
   - Change from: `gh pr create --repo <repo> --head spec/<worktree-name> --title "<issue-title>" --body "<body>"`
   - Change to: `gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body "<body>"`

5. **cmd/kiro-krew/templates/kiro/agents/krew-lead-prompt.md** (template version)
   - Apply identical change as #4 above to maintain template sync

### Files to Create

6. **internal/config/base_branch_test.go**
   - Test default value when `base_branch` is omitted
   - Test parsing when `base_branch` is explicitly set
   - Test backward compatibility with configs lacking the field

7. **internal/agent/manager_base_branch_test.go**
   - Test `extractBaseBranchOverride()` function with various issue body formats
   - Test `resolveBaseBranch()` precedence logic (override → config → default)
   - Test edge cases (empty strings, whitespace, case sensitivity)

## Team Orchestration

This is a single-developer task implementing a cohesive feature across multiple layers. All tasks build toward complete issue resolution in one PR.

**Task dependencies:**
- Task 1 (Config) has no dependencies
- Task 2 (Manager helpers) depends on Task 1 (needs Config.BaseBranch field)
- Task 3 (Script) has no dependencies (can run parallel to Task 1-2)
- Task 4 (Prompt updates) has no dependencies (can run parallel to all others)
- Task 5 (Tests) depends on Tasks 1-2 (tests the code they validate)

**Parallelization opportunity**: Tasks 1-2 form one work stream, Task 3 is independent, Task 4 is independent. Task 5 must follow Tasks 1-2.

## Step-by-Step Task Breakdown

### Task 1: Add Config Field and Default

**Objective**: Add `base_branch` configuration field with default value.

**Implementation Steps**:
1. Open `internal/config/config.go`
2. Add `BaseBranch string` field to `Config` struct with yaml tag `base_branch` (position it near `GithubRepo`, `Label`, etc.)
3. In `Load()` function, set default value before YAML unmarshal: `cfg.BaseBranch = "main"`
4. Verify existing configs without the field continue working (default applies)

**Acceptance Criteria**:
- `Config` struct has `BaseBranch string` field with `yaml:"base_branch"` tag
- `Load()` sets default value `"main"` before unmarshaling
- Backward compatible: configs without the field get `"main"` default

**Dependencies**: None

---

### Task 2: Implement Base Branch Resolution in Manager

**Objective**: Add helper functions and update worktree creation call sites to use resolved base branch.

**Implementation Steps**:
1. Open `internal/agent/manager.go`
2. Add function `extractBaseBranchOverride(issueBody string) string`:
   - Parse issue body for line matching `Base-Branch: <value>` (case-insensitive)
   - Extract and return trimmed value
   - Return empty string if not found
   - Keep function simple and reusable (single responsibility)
3. Add function `resolveBaseBranch(cfg *Config, issueBody string) string`:
   - Check for per-issue override via `extractBaseBranchOverride(issueBody)`
   - If override exists, return it
   - Otherwise return `cfg.BaseBranch`
   - Apply precedence: override → config → (config already has "main" default)
4. Update `Spawn()` method (~line 191):
   - Fetch issue body using `gh issue view <number> --repo <repo> --json body`
   - Call `resolveBaseBranch(m.config, issueBody)`
   - Pass resolved base branch as second argument to `worktree-create.sh`: `exec.Command("bash", createScript, worktreeName, baseBranch)`
5. Update `retryAgent()` method (~line 457):
   - Fetch issue body (store in agent struct or re-fetch)
   - Call `resolveBaseBranch(m.config, issueBody)`
   - Pass resolved base branch as second argument when recreating worktree

**Acceptance Criteria**:
- `extractBaseBranchOverride()` correctly parses `Base-Branch: <value>` from issue body
- `resolveBaseBranch()` implements correct precedence: issue override → config → default
- Both worktree creation call sites pass resolved base branch as second argument
- Issue body is fetched before resolution at both call sites

**Dependencies**: Task 1 (needs `Config.BaseBranch` field)

---

### Task 3: Update Worktree Creation Script

**Objective**: Make `worktree-create.sh` accept optional base ref argument.

**Implementation Steps**:
1. Open `.kiro-krew/scripts/worktree-create.sh`
2. After `SPEC_NAME=$1`, add: `BASE_REF=${2:-}`
3. Replace the `git worktree add` command with conditional:
   ```bash
   if [ -n "$BASE_REF" ]; then
       OUTPUT=$(git worktree add "$WORKTREE_PATH" -b "$BRANCH_NAME" "$BASE_REF" 2>&1)
   else
       OUTPUT=$(git worktree add "$WORKTREE_PATH" -b "$BRANCH_NAME" 2>&1)
   fi
   ```
4. Test standalone usage with one argument (preserves current behavior)
5. Test with two arguments (forks from specified base)

**Acceptance Criteria**:
- Script accepts optional second argument for base ref
- When base ref provided: `git worktree add <path> -b <branch> <base_ref>`
- When base ref omitted: `git worktree add <path> -b <branch>` (current behavior)
- Backward compatible: standalone usage with one argument works unchanged
- Error handling preserved (exit codes, output capture)

**Dependencies**: None

---

### Task 4: Update Krew-Lead Prompt Files

**Objective**: Add explicit `--base` flag to PR creation command in both live and template versions.

**Implementation Steps**:
1. Open `.kiro/agents/krew-lead-prompt.md`
2. Find step 8 "Create PR"
3. Update the `gh pr create` command to include `--base <base_branch>`:
   - From: `gh pr create --repo <repo> --head spec/<worktree-name> --title "<issue-title>" --body "<body>"`
   - To: `gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body "<body>"`
4. Add note that `<base_branch>` should match the branch the worktree was created from
5. Open `cmd/kiro-krew/templates/kiro/agents/krew-lead-prompt.md`
6. Apply identical change to maintain template sync

**Acceptance Criteria**:
- Live prompt (`.kiro/agents/krew-lead-prompt.md`) includes `--base <base_branch>` in PR creation command
- Template prompt (`cmd/kiro-krew/templates/kiro/agents/krew-lead-prompt.md`) includes identical change
- Both files are in sync
- Instruction clear that `<base_branch>` references the resolved base branch

**Dependencies**: None

---

### Task 5: Add Tests for Base Branch Resolution

**Objective**: Comprehensive test coverage for new functionality.

**Implementation Steps**:
1. Create `internal/config/base_branch_test.go`:
   - Test default value when `base_branch` omitted from config
   - Test explicit `base_branch` value is parsed correctly
   - Test backward compatibility (old configs without field work)
   - Follow patterns from `sandbox_test.go`
2. Create `internal/agent/manager_base_branch_test.go`:
   - Test `extractBaseBranchOverride()`:
     - Returns correct value when `Base-Branch: dev` present
     - Returns empty string when not present
     - Handles case-insensitive matching (`base-branch:`, `BASE-BRANCH:`)
     - Handles whitespace variations
     - Returns first occurrence if multiple lines match
   - Test `resolveBaseBranch()`:
     - Returns issue override when present (ignores config)
     - Returns config value when no override
     - Returns "main" when config is empty and no override
     - Tests precedence order explicitly
3. Run all tests: `go test ./internal/config -v` and `go test ./internal/agent -v`

**Acceptance Criteria**:
- All new functions have unit test coverage
- Tests validate default behavior, explicit configuration, and precedence logic
- Tests follow existing project patterns (temp dir setup, config file creation)
- Tests pass: `go test ./internal/...`
- Edge cases covered (empty strings, whitespace, case sensitivity)

**Dependencies**: Tasks 1 and 2 (tests validate those implementations)

---

## Validation Commands

Run these commands to verify the implementation:

### 1. Build and Verify Compilation
```bash
go build ./cmd/kiro-krew
```

### 2. Run All Tests
```bash
go test ./internal/config -v
go test ./internal/agent -v
```

### 3. Test Config Default (no base_branch field)
```bash
# Create minimal config without base_branch
cat > .kiro-krew/config.yaml << 'EOF'
githubrepo: test/repo
label: kiro-krew
EOF

# Verify default is applied (requires running watcher or unit test)
go test -run TestLoad_BaseBranchDefault ./internal/config -v
```

### 4. Test Config Explicit Value
```bash
# Create config with explicit base_branch
cat > .kiro-krew/config.yaml << 'EOF'
githubrepo: test/repo
label: kiro-krew
base_branch: develop
EOF

# Verify value is parsed correctly
go test -run TestLoad_BaseBranchExplicit ./internal/config -v
```

### 5. Test Per-Issue Override
```bash
# Create test issue body file
cat > /tmp/issue-body.txt << 'EOF'
This is a test issue.

Base-Branch: feature-branch

Some more description.
EOF

# Test extraction function (unit test)
go test -run TestExtractBaseBranchOverride ./internal/agent -v
```

### 6. Test Worktree Script (standalone)
```bash
# Test with one argument (current behavior)
.kiro-krew/scripts/worktree-create.sh test-worktree-1

# Test with two arguments (new behavior)
.kiro-krew/scripts/worktree-create.sh test-worktree-2 develop

# Verify branches created from correct base
git log --oneline -1 spec/test-worktree-1
git log --oneline -1 spec/test-worktree-2

# Clean up
git worktree remove .worktrees/test-worktree-1
git worktree remove .worktrees/test-worktree-2
git branch -D spec/test-worktree-1 spec/test-worktree-2
```

### 7. Integration Test (manual)
```bash
# 1. Set config with base_branch
echo "base_branch: develop" >> .kiro-krew/config.yaml

# 2. Create test issue with override
# (manually on GitHub with "Base-Branch: jira-work" in body)

# 3. Run watcher and observe:
#    - Worktree created from jira-work (not develop)
#    - PR targets jira-work (matches worktree base)

# 4. Verify PR diff contains only issue changes, not integration branch commits
```

### 8. Lint and Format
```bash
go fmt ./...
go vet ./...
```

### 9. Check Template Sync
```bash
# Verify both krew-lead-prompt.md files have identical PR creation command
diff <(grep "gh pr create" .kiro/agents/krew-lead-prompt.md) \
     <(grep "gh pr create" cmd/kiro-krew/templates/kiro/agents/krew-lead-prompt.md)
# Should produce no output (files match)
```

## Backward Compatibility Guarantees

1. **Existing configs without `base_branch`**: Continue to work with `"main"` as default
2. **Standalone script usage**: Calling `worktree-create.sh` with one argument preserves current behavior
3. **No API breaking changes**: Config struct only adds a field, doesn't remove or change existing fields
4. **Template sync**: Both versions of krew-lead prompt updated identically
5. **Default behavior**: Without any configuration, system behaves exactly as today (base = "main")

## Edge Cases Handled

1. **Empty base branch override**: If `Base-Branch:` has no value, treat as no override
2. **Whitespace handling**: Trim whitespace from parsed override values
3. **Case sensitivity**: Match `Base-Branch`, `base-branch`, `BASE-BRANCH` (case-insensitive)
4. **Multiple occurrences**: If multiple `Base-Branch:` lines exist, use the first one
5. **Missing issue body**: If issue has no body, resolution falls back to config → default
6. **Script with empty BASE_REF**: If second argument is empty string `""`, treat as omitted

## Success Criteria

- [ ] Config field `base_branch` added with default `"main"`
- [ ] `worktree-create.sh` accepts optional second argument for base ref
- [ ] Manager passes resolved base branch to worktree creation (both call sites)
- [ ] Base resolution extracts `Base-Branch: <value>` from issue body
- [ ] Base resolution precedence: issue override → config → `"main"`
- [ ] Krew-lead prompts include `--base <base_branch>` in PR creation
- [ ] Template sync maintained between live and template prompts
- [ ] All tests pass
- [ ] Backward compatibility verified (old configs work)
- [ ] Standalone script usage works unchanged
- [ ] Integration test: Issue with `Base-Branch: dev` creates worktree from `dev` and PR targets `dev`
- [ ] Integration test: Issue without override uses config `base_branch`
- [ ] Integration test: Default config (no `base_branch`) uses `"main"`
