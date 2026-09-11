# Design Specification: Fix PR Creation to Handle Backticks in Body Text

**Issue**: #25  
**Closes**: #25

## Problem Statement

The krew-lead agent's PR creation workflow (step 8) suffers from a **shell injection vulnerability** when PR body content contains backticks. The current implementation passes the PR body directly as a command-line argument using `--body "<body>"`, which causes the shell to interpret backticks as command substitution.

### Security Impact

- **Critical Risk**: Backticks in PR bodies trigger command execution
- **Real-World Trigger**: Any PR containing code snippets with backticks
- **Attack Surface**: Automated PR creation from untrusted issue descriptions

### Current Vulnerable Implementation

Located in `.kiro/agents/krew-lead-prompt.md` step 8:

```bash
gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body "<body>"
```

When `<body>` contains backticks like `` `date` ``, the shell executes the command inside the backticks before passing the result to `gh`.

## Solution Approach

### Strategy

Switch from inline `--body` to file-based `--body-file` approach, following the proven pattern already used successfully in the planner agent's issue creation workflow.

### Why This Works

1. **File-based approach**: Content written to temp file bypasses shell interpretation
2. **No escaping needed**: File contents are read verbatim by `gh` CLI
3. **Proven pattern**: Planner agent already uses this successfully for issue creation
4. **Clean separation**: Shell command parsing is separated from content handling

### Security Properties

- ✅ Backticks handled as literal text
- ✅ No command substitution possible
- ✅ Quotes, newlines, and special characters preserved exactly
- ✅ No escaping complexity or edge cases

## Relevant Files

### Files to Modify

| File | Purpose | Change Type |
|------|---------|-------------|
| `.kiro/agents/krew-lead-prompt.md` | Krew-lead workflow definition | Modify step 8 only |

### Reference Implementation

| File | Purpose | Relevance |
|------|---------|-----------|
| `.kiro/agents/planner-prompt.md` | Planner agent workflow | Reference for `--body-file` pattern |

## Team Orchestration

**Single-file change**: This is a focused security fix modifying only step 8 in the krew-lead workflow. No coordination between teams or components required.

**Parallel work**: N/A (single atomic change)

**Dependencies**: None

## Step-by-Step Task Breakdown

### Task 1: Update PR Creation Step to Use --body-file

**File**: `.kiro/agents/krew-lead-prompt.md`

**Location**: Step 8 (Create PR) section

**Current Implementation**:
```bash
gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body "<body>"
```

**Required Changes**:

1. **Write PR body to temp file** before calling `gh pr create`:
   ```bash
   cat > /tmp/pr-body-<issue-number>.md << 'EOF'
   <body>
   EOF
   ```

2. **Update gh pr create command** to use `--body-file`:
   ```bash
   gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body-file /tmp/pr-body-<issue-number>.md
   ```

3. **Add cleanup step** after PR creation (success or failure):
   ```bash
   rm -f /tmp/pr-body-<issue-number>.md
   ```

**Implementation Pattern** (following planner agent's approach):

The planner agent demonstrates the correct pattern in its issue creation workflow:

```bash
# Write body to temp file
cat > /tmp/issue-body.md << 'EOF'
<body content>
EOF

# Use --body-file flag
gh issue create --repo <REPO> --title "<title>" --body-file /tmp/issue-body.md

# Cleanup
rm -f /tmp/issue-body.md
```

Apply this same pattern to step 8's PR creation.

**Acceptance Criteria**:

1. Step 8 writes PR body to `/tmp/pr-body-<issue-number>.md` using a here-document with single quotes (`<< 'EOF'`) to prevent shell interpretation
2. The `gh pr create` command uses `--body-file /tmp/pr-body-<issue-number>.md` instead of `--body "<body>"`
3. Cleanup command `rm -f /tmp/pr-body-<issue-number>.md` is added after the `gh pr create` command
4. Cleanup occurs regardless of PR creation success or failure (use trap or explicit cleanup in both paths)
5. The temp file name includes `<issue-number>` for uniqueness and debuggability
6. No other changes to step 8's PR body content generation, metadata, or workflow logic
7. Step 8's descriptive text is updated to document the `--body-file` approach

**Verification**:

```bash
# Test with backticks in PR body
echo 'Test body with `command` and `another` backticks' > /tmp/test-body.md
gh pr create --repo test/repo --title "Test" --body-file /tmp/test-body.md --dry-run

# Verify temp file cleanup
test ! -f /tmp/pr-body-<issue-number>.md
```

### Task 2: Ensure Error Handling and Cleanup

**File**: `.kiro/agents/krew-lead-prompt.md`

**Purpose**: Guarantee temp file cleanup even when PR creation fails

**Implementation Options**:

**Option A** (Explicit cleanup in both paths):
```bash
# Write temp file
cat > /tmp/pr-body-<issue-number>.md << 'EOF'
<body>
EOF

# Create PR
if gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body-file /tmp/pr-body-<issue-number>.md; then
  rm -f /tmp/pr-body-<issue-number>.md
else
  rm -f /tmp/pr-body-<issue-number>.md
  # Handle PR creation failure per existing step 11 workflow
fi
```

**Option B** (Using trap for guaranteed cleanup - RECOMMENDED):
```bash
# Set up cleanup trap
trap 'rm -f /tmp/pr-body-<issue-number>.md' EXIT

# Write temp file
cat > /tmp/pr-body-<issue-number>.md << 'EOF'
<body>
EOF

# Create PR (cleanup happens automatically on exit)
gh pr create --repo <repo> --head spec/<worktree-name> --base <base_branch> --title "<issue-title>" --body-file /tmp/pr-body-<issue-number>.md
```

**Recommendation**: Use Option B (trap-based cleanup) for robustness and simplicity.

**Acceptance Criteria**:

1. Temp file is cleaned up when PR creation succeeds
2. Temp file is cleaned up when PR creation fails
3. Temp file is cleaned up if krew-lead agent is interrupted or crashes during PR creation
4. Cleanup implementation uses either explicit paths (Option A) or trap handler (Option B)
5. Existing step 11 failure handling workflow remains unchanged

**Verification**:

```bash
# Simulate PR creation failure
gh pr create --repo nonexistent/repo --title "Test" --body-file /tmp/test-body.md 2>&1 || true

# Verify cleanup occurred despite failure
test ! -f /tmp/test-body.md && echo "PASS: Cleanup succeeded"
```

## Validation Commands

### Unit-Level Verification

```bash
# 1. Verify krew-lead-prompt.md uses --body-file
grep -n "body-file" .kiro/agents/krew-lead-prompt.md

# 2. Verify no remaining --body inline usage in step 8
grep -n 'gh pr create.*--body "' .kiro/agents/krew-lead-prompt.md || echo "PASS: No inline --body found"

# 3. Verify temp file cleanup is present
grep -n "rm -f /tmp/pr-body" .kiro/agents/krew-lead-prompt.md

# 4. Verify here-document uses single quotes (prevents expansion)
grep -n "<< 'EOF'" .kiro/agents/krew-lead-prompt.md
```

### Integration Testing

```bash
# Create a test worktree and simulate PR creation with problematic content
# (This should be done in the validator phase)

# 1. Create test PR body with backticks
cat > /tmp/test-pr-body-999.md << 'EOF'
## Summary
Test PR with `command substitution` and `date` commands.

## Code Example
```bash
echo `whoami`
```
EOF

# 2. Verify gh accepts the file (dry-run)
gh pr create --repo matthiashowellyopp/howmux --title "Test PR" --body-file /tmp/test-pr-body-999.md --dry-run

# 3. Verify cleanup
rm -f /tmp/test-pr-body-999.md
test ! -f /tmp/test-pr-body-999.md && echo "PASS: Cleanup works"
```

### Security Verification

```bash
# Test that backticks are NOT executed
cat > /tmp/security-test.md << 'EOF'
This body contains `date` and `whoami` and should not execute them.
EOF

# Create PR (dry-run) and verify no command execution
gh pr create --repo matthiashowellyopp/howmux --title "Security Test" --body-file /tmp/security-test.md --dry-run 2>&1 | grep -v "date\|whoami" || echo "FAIL: Command was executed"

# Cleanup
rm -f /tmp/security-test.md
```

## Implementation Notes

### Key Design Decisions

1. **Temp file location**: Use `/tmp/pr-body-<issue-number>.md` for:
   - Standard Linux/macOS temp directory
   - Issue number provides uniqueness and traceability
   - `.md` extension for editor syntax highlighting during debugging

2. **Here-document with single quotes**: `<< 'EOF'` prevents shell expansion:
   - Without quotes: `<< EOF` - shell expands variables and backticks
   - With quotes: `<< 'EOF'` - content is literal (required for security)

3. **Cleanup strategy**: Trap-based cleanup (Option B) is preferred:
   - Simpler code (single cleanup point)
   - Handles interrupts and crashes
   - Mirrors best practices in shell scripting

4. **No behavior changes**: PR body content, formatting, and metadata remain identical:
   - Same title, summary, file list, "Closes #N" footer
   - Only the transport mechanism changes (inline → file)

### Why Not Escape Instead?

Escaping backticks and quotes is **not recommended** because:

1. **Complexity**: Requires escaping `, ", $, \, and newlines correctly
2. **Fragility**: Easy to miss edge cases (nested quotes, escaped escapes)
3. **Maintenance burden**: Every special character is a potential bug
4. **Proven alternative exists**: `--body-file` is purpose-built for this

The file-based approach is **always correct** regardless of content.

## Backward Compatibility

### No Breaking Changes

- PR bodies are generated identically (same content)
- PR metadata (title, base branch, head branch) unchanged
- Existing workflow steps (1-7, 9-11) untouched
- Only step 8's command execution changes (internal implementation detail)

### Agent Behavior

- Krew-lead agent sees no API changes
- PR creation success/failure handling unchanged
- Error messages and logging remain the same

## Concurrency Analysis

### Cross-Goroutine Access

**None**. This change is purely within the krew-lead agent's sequential workflow execution. No shared state is accessed or modified across goroutines.

### Temp File Uniqueness

Using `<issue-number>` in the temp filename ensures:
- No collision between concurrent krew-lead processes (each works on different issues)
- Debuggability (can identify which issue's PR creation left a temp file)

## Testing Strategy

### Validator Verification (Task-Level)

The validator agent will verify:

1. **Step 8 modification**: 
   - Grep confirms `--body-file` is present
   - Grep confirms no remaining inline `--body` usage
   - Here-document uses single quotes

2. **Cleanup presence**:
   - Grep confirms `rm -f /tmp/pr-body-<issue-number>.md`
   - OR grep confirms trap handler for cleanup

3. **Security test** (dry-run):
   - Create test body with backticks
   - Run `gh pr create --body-file --dry-run`
   - Verify no command execution occurred

### QA Loop Validation

If QA tooling detects issues:
- Shellcheck should pass on the modified step 8 commands
- No new linter warnings introduced
- Existing tests continue to pass (PR creation tests)

## Reference Implementation

The planner agent's issue creation in `.kiro/agents/planner-prompt.md` uses this exact pattern successfully:

```bash
# Write issue body to temp file
cat > /tmp/issue-body.md << 'EOF'
<body content with any characters>
EOF

# Create issue with --body-file
gh issue create --repo <REPO> --title "<title>" --body-file /tmp/issue-body.md --label "<LABEL>"

# Cleanup
rm -f /tmp/issue-body.md
```

**Validation evidence**: The planner agent has been used extensively in production and correctly handles:
- Backticks in issue descriptions
- Multi-line content with code blocks
- Special characters and quotes

This PR creation fix applies the **same proven pattern** to step 8.

## Summary

This is a focused **security fix** that eliminates shell injection vulnerability in PR creation by:

1. Writing PR body to temp file (`/tmp/pr-body-<issue-number>.md`)
2. Using `--body-file` instead of inline `--body`
3. Cleaning up temp file after creation
4. Following the proven pattern from planner agent's issue creation

**Impact**: Security vulnerability eliminated with zero behavior changes to PR creation workflow.

**Scope**: Single-file change (`.kiro/agents/krew-lead-prompt.md` step 8 only).

**Risk**: Minimal - applying existing proven pattern to similar use case.
