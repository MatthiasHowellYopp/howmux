# Design Specification: PR Review Checkout under .worktrees/

Closes #61

## Context

Part of the PR-review workflow series. Gets a target PR's code onto disk into an isolated dir so the review orchestrator can run against it.

Note: this is **not** a git *worktree* (git worktrees are same-repo only). It is a **clone** of the third-party repo, refreshed in place on re-review. We keep the `.worktrees/` location for a consistent mental model, but the mechanism is clone/fetch.

Blocked by: #60 (needs owner/repo/pr resolution) — **this issue depends on the `ResolvePRURL` function from #60**.

## Solution Approach

### Core Design Principle

This issue follows the same testing philosophy as #60: **split into pure functions and thin wiring**, so that all logic is unit-testable without running real `git`/`gh` commands.

The design has three layers:

1. **Pure path construction** (`reviewDir`) — builds the worktree directory path `.worktrees/review-<owner>-<repo>-<pr>`
2. **Pure argv construction** (`checkoutCommands`) — returns the ordered command sequences for fresh clone vs refresh
3. **Thin wiring** (`EnsureCheckout`) — checks directory existence and executes the commands

This pattern makes command construction testable via table tests while keeping the actual execution (which touches disk/network) in a thin, untestable wrapper.

### Why .worktrees/ for Third-Party Clones?

The naming choice (`.worktrees/` for clones) maintains consistency with the existing issue-processing workflow, which uses `.worktrees/issue-<number>-<pid>/` for git worktrees. While technically these are clones rather than worktrees, the mental model is the same: isolated working directories for parallel work on different issues/PRs. The consistent location makes scripts, gitignore patterns, and directory cleanup uniform.

### Fresh Clone vs Refresh

- **Fresh clone** (directory doesn't exist):
  1. `git clone <repoURL> <dir>` — clone the target repo
  2. `gh pr checkout <pr>` (in `<dir>`) — checkout the PR branch

- **Refresh** (directory already exists):
  1. `git fetch` (in `<dir>`) — fetch latest commits
  2. `gh pr checkout <pr>` (in `<dir>`) — checkout updated PR branch

Both sequences end with the PR branch checked out at the latest commit. The `gh pr checkout` command is idempotent — it handles branch tracking, fetching the PR ref, and checking out the head commit.

### Concurrency Analysis

**No concurrency concerns** — this issue introduces pure functions and a thin wrapper that shells out to `git`/`gh`. All functions are deterministic and side-effect-free except `EnsureCheckout`, which is the intended side-effect boundary (disk/network I/O). No shared state, no goroutines, no locks needed.

### Repository URL Construction

The `repoURL` parameter to `checkoutCommands` should be the HTTPS clone URL: `https://github.com/<owner>/<repo>.git`

The caller (orchestrator) constructs this from the owner/repo parsed by `ResolvePRURL` from issue #60.

### Data Flow

```
PR URL → ResolvePRURL (from #60) → owner, repo, pr
       ↓
owner, repo, pr → reviewDir → ".worktrees/review-owner-repo-pr"
       ↓
repoURL, pr, dir, dirExists → checkoutCommands → [][]string (argv sequences)
       ↓
EnsureCheckout → execute commands → PR code on disk
```

## Relevant Files

### New Files to Create

- `internal/review/checkout.go` — `reviewDir`, `checkoutCommands`, `EnsureCheckout`
- `internal/review/checkout_test.go` — Unit tests for pure functions

### Existing Files to Reference

- `internal/review/paths.go` — Follow existing `recordDir` pattern (pure path construction)
- `internal/github/pr.go` (from #60) — Use `ResolvePRURL` to get owner/repo/pr from URL

## Team Orchestration

**Sequential dependency**: This issue **depends on #60** completing first. The `ResolvePRURL` function from #60 must exist before this issue can be implemented, as it provides the owner/repo/pr inputs to `reviewDir`.

However, this issue can be **architected and tested** before #60 lands by hardcoding test inputs (owner, repo, pr) in unit tests. The actual integration with `ResolvePRURL` is wiring-level (orchestrator code, not tested here).

## Step-by-Step Task Breakdown

### Task 1: Implement reviewDir (Pure Path Construction)

**What to build:**
- Create `internal/review/checkout.go`
- Implement `reviewDir(owner, repo string, pr int) string`
- Returns `.worktrees/review-<owner>-<repo>-<pr>`
- Pure function (deterministic, no side effects)

**Acceptance Criteria:**
1. Function returns path in exact format: `.worktrees/review-<owner>-<repo>-<pr>`
2. Handles owner/repo names with hyphens, underscores, numbers
3. PR number is formatted as integer (no leading zeros)
4. Table test in `checkout_test.go` covers:
   - Basic case: `reviewDir("owner", "name", 123)` → `.worktrees/review-owner-name-123`
   - Hyphenated org: `reviewDir("my-org", "repo", 456)` → `.worktrees/review-my-org-repo-456`
   - Underscored repo: `reviewDir("owner", "my_repo", 789)` → `.worktrees/review-owner-my_repo-789`
   - Large PR number: `reviewDir("owner", "repo", 99999)` → `.worktrees/review-owner-repo-99999`
5. Verification: `grep -rn ".worktrees/review" checkout_test.go` shows exact path format in test assertions

**Dependencies:** None

### Task 2: Implement checkoutCommands (Pure Argv Construction)

**What to build:**
- Implement `checkoutCommands(repoURL string, pr int, dir string, dirExists bool) [][]string`
- Returns ordered argv sequences for clone+checkout or fetch+checkout
- Pure function (deterministic, no side effects)

**Acceptance Criteria:**
1. Function is pure (no disk I/O, no execution)
2. Returns `[][]string` where each inner slice is one command's argv
3. **Fresh clone branch** (`!dirExists`):
   - Command 1: `["git", "clone", "<repoURL>", "<dir>"]`
   - Command 2: `["gh", "pr", "checkout", "<pr>"]` (caller must run in `dir`)
4. **Refresh branch** (`dirExists`):
   - Command 1: `["git", "fetch"]` (caller must run in `dir`)
   - Command 2: `["gh", "pr", "checkout", "<pr>"]` (caller must run in `dir`)
5. PR number is formatted as string in argv (e.g., "123")
6. Table test in `checkout_test.go` covers both branches:

**Test case: fresh clone**
```go
{
    name: "fresh clone",
    repoURL: "https://github.com/owner/repo.git",
    pr: 123,
    dir: ".worktrees/review-owner-repo-123",
    dirExists: false,
    want: [][]string{
        {"git", "clone", "https://github.com/owner/repo.git", ".worktrees/review-owner-repo-123"},
        {"gh", "pr", "checkout", "123"},
    },
}
```

**Test case: refresh**
```go
{
    name: "refresh existing",
    repoURL: "https://github.com/owner/repo.git",
    pr: 456,
    dir: ".worktrees/review-owner-repo-456",
    dirExists: true,
    want: [][]string{
        {"git", "fetch"},
        {"gh", "pr", "checkout", "456"},
    },
}
```

7. Test asserts **exact argv sequences** (length, order, each element)
8. Test includes a case with hyphenated org/repo to verify path handling
9. Verification: `go test ./internal/review -v -run TestCheckoutCommands` passes

**Dependencies:** Task 1 (reviewDir must exist for test setup, though not called by checkoutCommands)

### Task 3: Implement EnsureCheckout (Thin Wiring)

**What to build:**
- Implement `EnsureCheckout(repoURL string, pr int, dir string) error`
- Checks if `dir` exists using `os.Stat`
- Calls `checkoutCommands(repoURL, pr, dir, dirExists)` to get argv sequences
- Executes each command in order:
  - `git clone` and `git fetch` run with no working directory
  - `gh pr checkout` runs with `dir` as working directory
- Returns error if any command fails (wrapped with context)

**Acceptance Criteria:**
1. Function checks directory existence via `os.Stat` (non-nil error means !exists)
2. Uses `checkoutCommands` to get the command sequence
3. Executes commands using `exec.Command`
4. For clone/fetch: `cmd := exec.Command(argv[0], argv[1:]...)`
5. For `gh pr checkout`: `cmd := exec.Command(argv[0], argv[1:]...); cmd.Dir = dir`
6. Returns wrapped error on command failure (include which command failed)
7. Error messages include repo URL, PR number, and directory for debugging
8. Follow existing `internal/github/client.go` patterns for error wrapping (use `fmt.Errorf`)
9. **This function is NOT unit tested** (it's the thin wiring that touches disk/network)
10. Function signature exported (capitalized) for use by orchestrator

**Implementation notes:**
- Use `os.Stat(dir)` to check existence: `_, err := os.Stat(dir); dirExists := err == nil`
- For fresh clone, the first command is `git clone`, run with no Dir set
- For refresh, the first command is `git fetch`, run with `cmd.Dir = dir`
- The second command (`gh pr checkout`) always runs with `cmd.Dir = dir`

**Dependencies:** Task 2 (checkoutCommands must exist)

### Task 4: Integration Documentation Test

**What to build:**
- Create `TestCheckoutWorkflow` in `checkout_test.go`
- Demonstrates the full workflow **without executing commands**:
  1. Parse owner/repo/pr (simulate `ResolvePRURL` from #60)
  2. Call `reviewDir(owner, repo, pr)` to get directory path
  3. Call `checkoutCommands(repoURL, pr, dir, false)` for fresh clone
  4. Verify the returned argv sequences are correct
  5. Call `checkoutCommands(repoURL, pr, dir, true)` for refresh
  6. Verify the refresh argv sequences are correct

**Acceptance Criteria:**
1. Test demonstrates intended usage without calling `git`/`gh`
2. Test covers both fresh clone and refresh paths
3. Test asserts correct path construction and argv sequences
4. Test serves as documentation for orchestrator integration
5. Test completes instantly (no external commands)
6. Verification: `go test ./internal/review -v -run TestCheckoutWorkflow -timeout 1s` passes

**Dependencies:** Tasks 1, 2

## Validation Commands

```bash
# Run all tests in internal/review
go test ./internal/review -v

# Run tests with coverage
go test ./internal/review -cover

# Verify no external git/gh calls in tests (should complete instantly)
go test ./internal/review -v -timeout 1s

# Verify reviewDir path construction
go test ./internal/review -v -run TestReviewDir

# Verify checkoutCommands argv construction for both branches
go test ./internal/review -v -run TestCheckoutCommands

# Verify the workflow documentation test
go test ./internal/review -v -run TestCheckoutWorkflow

# Full project build and test to ensure no integration issues
go build ./...
go test ./...

# Verify the worktree path pattern is consistent
grep -rn ".worktrees/review-" internal/review/
```

## Test Coverage Requirements

All functions except `EnsureCheckout` (the thin wiring) must be unit tested:

- ✅ `reviewDir` — table test with various owner/repo/pr combinations
- ✅ `checkoutCommands` — table test for both branches (fresh clone, refresh)
- ❌ `EnsureCheckout` — NOT tested (thin wiring, no logic)

The test coverage for the pure functions (`reviewDir` and `checkoutCommands`) should be 100%, as they contain all the logic. The thin wiring (`EnsureCheckout`) is trivial command execution and intentionally untested.

## Style Consistency

Follow existing patterns:

- `internal/review/paths.go` — Pure path construction functions
- `internal/github/client.go` — exec.Command for external commands, error wrapping
- Use `fmt.Sprintf` for path/string construction
- Use `exec.Command("cmd", args...)` for command execution
- Wrap errors with `fmt.Errorf` including context

## Example Usage (Post-Implementation)

```go
// In the orchestrator (after #60 lands):

// Step 1: Parse PR URL to get owner, repo, pr
owner, repo, prNum, err := github.ResolvePRURL(prURL)
if err != nil {
    return fmt.Errorf("invalid PR URL: %w", err)
}

// Step 2: Construct directory path
dir := review.reviewDir(owner, repo, prNum)

// Step 3: Ensure checkout (clone or refresh)
repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
if err := review.EnsureCheckout(repoURL, prNum, dir); err != nil {
    return fmt.Errorf("failed to checkout PR: %w", err)
}

// Now the PR code is on disk at `dir`, ready for review
```

## Notes

- This issue introduces **no new concurrency** — all functions are pure or execute synchronously
- The separation between pure functions (testable) and thin wiring (untestable but trivial) follows the project's testing philosophy from #60
- The `.worktrees/` location maintains consistency with the issue-processing workflow
- `EnsureCheckout` is idempotent on success (running it twice with the same inputs refreshes to latest)
- Error messages from failed commands should include stdout/stderr for debugging (follow `exec.Command` error handling patterns)
- The `reviewDir` function is exported for testing and potential external use, but `checkoutCommands` is package-private (lowercase) as it's an internal helper

## Dependency on #60

This issue **depends on #60** for the `ResolvePRURL` function, which provides the owner/repo/pr inputs. However:

- **Architecting and testing** this issue does NOT require #60 to land — tests can hardcode owner/repo/pr inputs
- **Integration** (using this from the orchestrator) requires #60 to land first
- The design spec and implementation can proceed in parallel with #60, as long as #60 lands before the orchestrator wiring is built

## Working Directory Handling

The `checkoutCommands` function returns argv sequences, but the **caller** (EnsureCheckout) must handle working directory correctly:

- **git clone**: run with no Dir set (clone creates the directory)
- **git fetch**: run with Dir set to the existing directory
- **gh pr checkout**: run with Dir set to the directory (works for both fresh and refresh)

This pattern keeps the pure function (`checkoutCommands`) simple (just returns argv) while the thin wiring (`EnsureCheckout`) handles execution context.
