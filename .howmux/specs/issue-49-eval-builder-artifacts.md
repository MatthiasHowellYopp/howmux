# Design Specification: Eval - Score Builder's Produced Code, Not ACP Stream

**Issue**: #49  
**Title**: Eval: score builder's produced code, not the ACP stream  
**Closes**: #49

## Solution Approach

The builder agent currently scores on conversational narration (ACP text output) instead of the actual code artifacts it produces. This is the same issue that was resolved for architect and documenter agents in the ACP transport migration. The solution is to implement a builder-specific artifact collection strategy in `internal/eval/artifact_collector.go` that reads the builder's produced code from the workspace filesystem and appends it to `actualOutput` before rubric grading.

### Strategy Selection: Git Diff Approach

After analyzing the three candidate approaches mentioned in the issue:

1. **Git diff of workspace** (SELECTED)
2. Collect newly-created/modified files  
3. Known output location

**Why git diff is the best approach:**

- **Most accurate**: Captures exactly what the builder changed, nothing more, nothing less
- **Robust filtering**: Excludes `.howmux/`, `.kiro/`, `.git/`, and other harness files automatically via configurable path patterns
- **Consistent with workflow**: Builder operates in git worktrees; git already tracks what matters
- **Comprehensive**: Captures file modifications, additions, and deletions in one unified format
- **Language-agnostic**: Works for Go, TypeScript, Python, or any codebase without language-specific file detection logic

The git diff approach treats the git working tree as the source of truth for "what the builder produced" and reads that diff after the agent completes.

## Relevant Files

### Files to Modify

- `internal/eval/artifact_collector.go` — Add builder case with git diff collection logic
- `internal/eval/artifact_collector_test.go` — Add unit tests for builder artifact collection
- `internal/eval/runner.go` — May need minor adjustments if collectArtifacts signature changes (review during implementation)

### Files Referenced (no changes needed)

- `.howmux/evals/rubrics/builder.yaml` — Builder rubric criteria (code_correctness, spec_adherence, code_quality, test_coverage)
- `.howmux/evals/cases/builder/*.yaml` — Builder test cases

## Team Orchestration

Single-component change with no external dependencies. The builder artifact collector is an isolated extension to the existing `collectArtifacts` function. No coordination with other agents required.

**Concurrency Note**: This change does not introduce cross-goroutine access to shared state. The artifact collector is invoked synchronously after the agent completes its turn, within the same goroutine as the eval runner. No locking required.

## Step-by-Step Task Breakdown

### Task 1: Implement Builder Artifact Collection Strategy

**Acceptance Criteria**:
- Add `case "builder":` branch to `collectArtifacts` function in `internal/eval/artifact_collector.go`
- Call git diff to capture workspace changes: `git diff HEAD --unified=3`
- Filter out harness paths: `.howmux/`, `.kiro/`, `.git/`, and other non-source paths
- Return formatted diff content with section header: `=== Git Diff (Builder Changes) ===`
- Handle empty diff (no changes) gracefully: return empty string, not an error
- Handle git command failures gracefully: log warning and return empty string (fail-safe: score narration only rather than crash)

**Dependencies**: None (can run in parallel with Task 2)

**Implementation Details**:
- Use `exec.Command("git", "diff", "HEAD")` within the workspace directory
- Apply path exclusion patterns to the diff output (filter lines starting with `diff --git` that match `.howmux/`, `.kiro/`, `.git/`)
- Format: `=== Git Diff (Builder Changes) ===\n<filtered-diff-content>`
- Empty diff is valid (builder may produce no file changes in some test cases)

### Task 2: Add Unit Tests for Builder Artifact Collection

**Acceptance Criteria**:
- Create `internal/eval/artifact_collector_test.go` if it doesn't exist
- Test `collectArtifacts("builder", workspaceDir)` with simulated git diff scenarios:
  1. **No changes**: `git diff HEAD` returns empty → `collectArtifacts` returns `""`
  2. **Source file changes**: Modified `.go` files → diff content returned with header
  3. **Mixed changes**: Source files + harness files (`.howmux/`, `.kiro/`) → only source file diffs returned
  4. **Harness-only changes**: Only `.howmux/` or `.kiro/` modified → returns `""`
  5. **Git command failure**: Git not available or repo not initialized → returns `""` without panic
- All tests pass: `go test ./internal/eval -run TestCollectArtifacts`
- Verify coverage: `go test -coverprofile=coverage.out ./internal/eval && go tool cover -func=coverage.out | grep artifact_collector`

**Dependencies**: None (can run in parallel with Task 1)

**Test Strategy**:
- Use `t.TempDir()` to create isolated git repositories for each test case
- Initialize test repos: `git init && git config user.email "test@test" && git config user.name "Test"`
- Commit baseline files, then modify files to create diff scenarios
- Mock git failures via controlled test environments (e.g., non-git directory)

### Task 3: Verify Builder Rubric Integration

**Acceptance Criteria**:
- Run `howmux eval builder --no-sandbox` successfully
- Verify `actualOutput` in `.howmux/evals/results/<timestamp>/builder.json` contains git diff artifacts for at least one test case
- Verify rubric criteria (code_correctness, spec_adherence, code_quality, test_coverage) score on the diff content, not just narration
- All builder test cases execute without artifact collection errors
- No regressions: architect and documenter artifact collection still works

**Dependencies**: Task 1 and Task 2 must be complete

**Verification Steps**:
```bash
# Clean build
go build ./cmd/howmux

# Run builder evaluation
howmux eval builder --no-sandbox

# Inspect results
cat .howmux/evals/results/<latest>/builder.json | jq '.cases[0].actual_output' | grep "Git Diff"

# Verify no errors in output
howmux eval builder --no-sandbox 2>&1 | grep -i "error\|fail"

# Verify architect/documenter not broken
howmux eval architect --no-sandbox
howmux eval documenter --no-sandbox
```

### Task 4: Handle Edge Cases and Error Conditions

**Acceptance Criteria**:
- Builder artifact collection is fail-safe: if git diff fails, return `""` and log a warning (do not crash the eval run)
- Empty diff is a valid scenario: test cases where builder produces no file changes should not fail artifact collection
- Large diffs are handled gracefully: no truncation or buffer overflow for diffs >100KB
- Path exclusion patterns are configurable (defined as a package-level constant or function parameter for future extensibility)

**Dependencies**: Task 1 must be complete

**Implementation Notes**:
- Define path exclusion patterns as a constant slice at package level:
  ```go
  var builderExcludedPaths = []string{".howmux/", ".kiro/", ".git/", "node_modules/", "vendor/"}
  ```
- Filter diff output by checking each `diff --git a/<path> b/<path>` line against exclusion patterns
- Return early with `""` if git command returns non-zero exit code (log the error via fmt.Fprintf to stderr)

## Validation Commands

```bash
# Build the project
go build ./cmd/howmux

# Run go vet (static analysis)
go vet ./internal/eval

# Run unit tests with coverage
go test ./internal/eval -run TestCollectArtifacts -v
go test ./internal/eval -coverprofile=coverage.out
go tool cover -func=coverage.out | grep artifact_collector

# Run full builder evaluation (end-to-end test)
howmux eval builder --no-sandbox

# Verify artifact collection in results
LATEST=$(ls -t .howmux/evals/results/ | head -1)
cat .howmux/evals/results/$LATEST/builder.json | jq '.cases[0].actual_output' | head -50

# Regression check: verify architect and documenter still work
howmux eval architect --no-sandbox
howmux eval documenter --no-sandbox

# Clean up
task clean
```

## Implementation Approach

This is a **single-PR complete implementation** that addresses all acceptance criteria:
- Task 1 and Task 2 can be executed in parallel (no dependencies between them)
- Task 3 integrates and validates Tasks 1 and 2
- Task 4 hardens the implementation with edge case handling

All tasks contribute to full issue resolution in one pull request. No phased delivery or incremental PRs.

## Mechanical Surface Enumeration

This change is NOT a mechanical rename/move, so comprehensive surface enumeration is not required. The scope is limited to:
- Adding a new case branch to `collectArtifacts()` function
- Creating unit tests for that new branch
- No changes to configuration files, CI workflows, or template-synced files

## Technical Implementation Notes

### Git Diff Format

The builder artifact collector will produce output in this format:

```
--- PRODUCED ARTIFACT ---
=== Git Diff (Builder Changes) ===
diff --git a/internal/tui/commands.go b/internal/tui/commands.go
index abc1234..def5678 100644
--- a/internal/tui/commands.go
+++ b/internal/tui/commands.go
@@ -45,6 +45,14 @@ func (m *model) handleHelp() tea.Cmd {
 	return m.setMessage("Available commands: help, status, stop <issue>, exit")
 }
 
+func (m *model) handleStatus() tea.Cmd {
+	agents := m.manager.List()
+	if len(agents) == 0 {
+		return m.setMessage("No agents running")
+	}
+	// ... table formatting ...
+}
+
 func (m *model) executeCommand(cmd string) tea.Cmd {
```

### Path Exclusion Logic

```go
func filterDiffPaths(diffContent string, excludedPaths []string) string {
	lines := strings.Split(diffContent, "\n")
	var filtered []string
	skip := false
	
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			// Check if this diff block should be excluded
			skip = false
			for _, excluded := range excludedPaths {
				if strings.Contains(line, excluded) {
					skip = true
					break
				}
			}
		}
		
		if !skip {
			filtered = append(filtered, line)
		}
	}
	
	return strings.Join(filtered, "\n")
}
```

## Current vs Expected Behavior

**Current Behavior (BROKEN)**:
- Builder evaluation scores only on ACP narration text: `"I've implemented the status command..."`
- Rubric criteria (code_correctness, spec_adherence, code_quality, test_coverage) judge the narration, not the code
- False positives: Builder can claim success without producing correct code, and the eval will pass

**Expected Behavior (FIXED)**:
- Builder evaluation scores on the actual git diff of produced code
- `actualOutput` contains: narration + `--- PRODUCED ARTIFACT ---` + git diff
- Rubric criteria judge the code changes, file structure, test coverage, and implementation correctness
- Accurate scoring: Eval detects when builder produces incorrect/incomplete code

## Test Case Example

Given builder test case `simple-command-implementation.yaml`:

**Input**: "Implement a status command for the TUI..."

**Current actualOutput** (broken):
```
I've implemented the status command by adding a handleStatus() method 
to internal/tui/commands.go and wiring it into the executeCommand switch.
The implementation follows existing patterns and includes proper error handling.
```

**Expected actualOutput** (fixed):
```
I've implemented the status command by adding a handleStatus() method 
to internal/tui/commands.go and wiring it into the executeCommand switch.
The implementation follows existing patterns and includes proper error handling.

--- PRODUCED ARTIFACT ---
=== Git Diff (Builder Changes) ===
diff --git a/internal/tui/commands.go b/internal/tui/commands.go
index abc1234..def5678 100644
--- a/internal/tui/commands.go
+++ b/internal/tui/commands.go
@@ -45,6 +45,18 @@ func (m *model) handleHelp() tea.Cmd {
...
[actual code changes here]
...
```

The rubric now grades the actual code changes, not the narrative description.
