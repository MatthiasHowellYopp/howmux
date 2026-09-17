# Design Specification: PR-review workflow - review command (REPL + CLI)

**Issue**: #65  
**Closes**: #65  
**Dependencies**: #62 (runner), #63 (preflight), #64 (loop)

## Solution Approach

This issue implements the top-level entry point for the PR-review workflow: a new `review` command that mirrors the existing `watch` command pattern. The command provides two forms:

1. **`review <PR_URL>`** — Resolve the URL, enroll the PR, ensure checkout, review it now, and start (or join) the recurring loop
2. **`review`** (bare form) — Start/continue the loop over the already-enrolled set (nothing new to seed)

The implementation follows the existing `watch` command architecture with these key components:

- **REPL command handler** (`handleReview` in `internal/tui/commands.go`)
- **Command registry entry** (in `internal/tui/command_registry.go`)
- **CLI dispatch** (in `cmd/howmux/cmd/review.go` following cobra patterns)
- **Preflight check** (hard-fail before any enroll/checkout if assets missing)

### Concurrency Analysis

This change introduces **cross-goroutine access** to the review watcher state:

**Concurrent Access Boundaries:**
- The review watcher runs its poll loop in a goroutine (similar to the issue watcher)
- Command handlers (main goroutine) will read watcher state via `Running()` and potentially write via `Start()`
- The TUI footer may eventually display review watcher status, requiring lock-guarded accessor methods

**Required Locking Pattern:**
The `review.Watcher` type already has a `sync.RWMutex` (`mu`) and lock-guarded methods:
- `Running()` acquires `w.mu.RLock()` before reading `w.started`
- `Start()` acquires `w.mu.Lock()` before writing `w.started`
- `Stop()` uses `w.stopOnce` and locks appropriately

All access to watcher state from the TUI/command handlers must go through these lock-guarded accessor methods. The model will hold a `*review.Watcher` and call `Running()` and `Start()` methods, never directly accessing `w.started` or other internal fields.

## Relevant Files

### Files to Modify

1. **`internal/tui/commands.go`**
   - Add `handleReview(args []string)` method
   - Wire into command dispatch in `Update()`

2. **`internal/tui/command_registry.go`**
   - Register `review` command with `HasArgs: true` and `ArgPattern: "[PR_URL]"`

3. **`internal/tui/tui.go`**
   - Add `reviewWatcher *review.Watcher` field to model
   - Initialize watcher in `Run()` function
   - Add watcher cleanup to `Cleanup()` method

4. **`cmd/howmux/cmd/root.go`**
   - Add subcommand registration for `reviewCmd`

5. **`README.md`**
   - Add `review` command to REPL Commands table
   - Add brief workflow description to CLI Usage section

### Files to Create

1. **`cmd/howmux/cmd/review.go`**
   - New cobra command for CLI `howmux review [PR_URL]`
   - Delegates to TUI's review handler via config/state

2. **`internal/tui/commands_review_test.go`**
   - Unit tests for `handleReview` parsing and dispatch
   - Preflight failure tests
   - No real external processes (use test doubles)

### Files Referenced (Dependencies)

From blocked issues (will be implemented first):

- **`internal/review/runner.go`** (#62) — `RunReview()` function for executing a review
- **`internal/review/assets.go`** (#63) — `CheckReviewAssets()` for preflight validation
- **`internal/review/watcher.go`** (#64) — `Watcher` type with `Start()`, `Stop()`, `Running()` methods
- **`internal/review/store.go`** — `Store` for persisting PR records
- **`internal/review/checkout.go`** (#61) — `EnsureCheckout()` for checking out PR code
- **`internal/github/pr.go`** (#60) — `ParsePRURL()` and `GetPR()` for PR metadata

## Team Orchestration

This is a single-file-per-layer change with clear boundaries:

**Backend Layer** (can be done in parallel):
- Task 1: Add model field and watcher initialization in `tui.go`
- Task 2: Implement command handler `handleReview()` in `commands.go`
- Task 3: Register command in `command_registry.go`

**CLI Layer** (depends on backend):
- Task 4: Create `cmd/review.go` cobra command

**Test Layer** (depends on handler):
- Task 5: Write command handler tests

**Documentation** (final):
- Task 6: Update README with command documentation

No concurrency concerns within implementation — the watcher's lock-guarded methods encapsulate all synchronization.

## Step-by-Step Task Breakdown

### Task 1: Add Watcher to TUI Model

**File**: `internal/tui/tui.go`

**Changes**:
1. Add `reviewWatcher *review.Watcher` field to `model` struct
2. In `Run()` function (where `model` is initialized):
   - Create review store: `reviewStore := review.NewDefaultStore()`
   - Read poll interval from config (or use default 5m if not configured)
   - Create watcher: `reviewWatcher := review.NewWatcher(reviewStore, pollInterval, 2, cfg.Repo)`
   - Assign to model: `m.reviewWatcher = reviewWatcher`
3. In model's cleanup/shutdown logic (likely in `Cleanup()` method or wherever watcher.Stop() is called):
   - Add: `if m.reviewWatcher != nil { m.reviewWatcher.Stop() }`

**Acceptance Criteria**:
- Model struct has `reviewWatcher *review.Watcher` field
- Watcher is initialized in `Run()` before TUI starts
- Watcher is stopped in cleanup (ensure it's not left running on exit)
- All access to watcher state uses lock-guarded accessor methods (`Running()`, `Start()`)

**Dependencies**: None (can run in parallel with Task 2)

---

### Task 2: Implement `handleReview()` Command Handler

**File**: `internal/tui/commands.go`

**Method Signature**:
```go
func (m model) handleReview(args []string) (model, tea.Cmd)
```

**Logic Flow**:

1. **Preflight Check** (blocking — fails before any enrollment):
   ```go
   kiroDir := filepath.Join(os.Getenv("HOME"), ".kiro")
   if err := review.CheckReviewAssets(kiroDir); err != nil {
       m = m.appendActivity(m.styles.Error.Render("PR-review preflight failed:"))
       m = m.appendActivity(m.styles.Error.Render(err.Error()))
       return m, nil
   }
   ```

2. **Parse Arguments**:
   - If `len(args) == 0` (bare `review`):
     - Check if watcher running: `m.reviewWatcher.Running()`
     - If already running: warning message "Review watcher already running"
     - If not running: start watcher and success message "Review watcher started"
     - Return `m, nil`
   
   - If `len(args) == 1` (URL provided):
     - Call `github.ParsePRURL(args[0])` → `(owner, repo, pr, err)`
     - If parse error: append error and return
     - Construct full repo string: `repo := owner + "/" + repo`

3. **Enroll Record** (#59 - URL argument flow):
   ```go
   store := m.reviewWatcher.store // access store from watcher
   
   // Check if already enrolled
   existing, found, err := store.Get(repo, pr)
   if err != nil {
       m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to check enrollment: %v", err)))
       return m, nil
   }
   
   if found {
       m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("PR #%d already enrolled (status: %s)", pr, existing.Status)))
   } else {
       // Create new record
       rec := review.Record{
           Repo:       repo,
           PR:         pr,
           URL:        args[0],
           Status:     review.StatusWatching,
           EnrolledAt: time.Now().Format(time.RFC3339),
           ReviewDir:  review.ReviewDir(owner, repo, pr), // helper from paths.go
       }
       if err := store.Save(rec); err != nil {
           m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to enroll PR: %v", err)))
           return m, nil
       }
       m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Enrolled PR #%d for review", pr)))
   }
   ```

4. **Ensure Checkout** (#61):
   ```go
   // Fetch PR metadata for repo URL
   prData, err := github.GetPR(repo, pr)
   if err != nil {
       m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to fetch PR metadata: %v", err)))
       return m, nil
   }
   
   reviewDir := review.ReviewDir(owner, repo, pr)
   if err := review.EnsureCheckout(owner, repo, prData.RepoURL, pr, reviewDir); err != nil {
       m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Checkout failed: %v", err)))
       // Continue anyway - checkout can be retried later
   } else {
       m = m.appendActivity(m.styles.Success.Render("PR checkout ready"))
   }
   ```

5. **Review Now** (#62):
   ```go
   // Get the fresh record (may have been updated by checkout)
   rec, found, err := store.Get(repo, pr)
   if err != nil || !found {
       m = m.appendActivity(m.styles.Error.Render("Failed to retrieve record for review"))
       return m, nil
   }
   
   // Get head SHA
   headSHA := prData.HeadSHA
   
   // Run review (synchronous for immediate feedback)
   m = m.appendActivity(m.styles.Info.Render(fmt.Sprintf("Starting review of PR #%d...", pr)))
   
   // TODO: Capture review output to agent tab (future enhancement)
   // For now, use io.Discard since we're in the command handler
   if err := review.RunReview(context.Background(), rec, headSHA, store, io.Discard); err != nil {
       m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Review failed: %v", err)))
       return m, nil
   }
   
   m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Review complete - spool at %s", rec.SpoolPath)))
   ```

6. **Ensure Loop Running** (#64):
   ```go
   if !m.reviewWatcher.Running() {
       m.reviewWatcher.Start()
       m = m.appendActivity(m.styles.Success.Render("Review watcher started"))
   }
   ```

**Error Handling**:
- Preflight failure: Display full error message with actionable fix instructions
- Parse failure: "Invalid PR URL: <error>"
- Enrollment failure: "Failed to enroll PR: <error>"
- Checkout failure: Warning only, continue to review
- Review failure: Error message, don't start watcher

**Acceptance Criteria**:
- `handleReview()` method exists and handles both argument forms
- Preflight check runs first and fails before any enroll/checkout
- URL parsing delegates to `github.ParsePRURL()`
- Record enrollment creates valid `review.Record` and saves via store
- Checkout is attempted but failure doesn't block review
- Review runs synchronously with error feedback
- Watcher starts if not already running (using `Running()` check)
- All watcher state access uses lock-guarded methods

**Dependencies**: Task 1 (needs `m.reviewWatcher` field)

---

### Task 3: Wire Command into Dispatch and Registry

**File 1**: `internal/tui/tui.go` (in `Update()` method)

Find the command dispatch switch in the `Update()` method where `tea.KeyEnter` is handled:

```go
case "watch":
    // existing watch command handling
case "review":
    parts := strings.Fields(strings.TrimSpace(m.input.Value()))
    args := []string{}
    if len(parts) > 1 {
        args = parts[1:]
    }
    return m.handleReview(args)
```

**File 2**: `internal/tui/command_registry.go`

Add to `NewCommandRegistry()`:

```go
registry.register(&Command{
    Name:        "review",
    Description: "Start PR review workflow",
    HasArgs:     true,
    ArgPattern:  "[PR_URL]",
})
```

**Acceptance Criteria**:
- Typing `review <URL>` in REPL dispatches to `handleReview` with `[URL]` args
- Typing `review` (bare) dispatches to `handleReview` with empty args
- Command appears in autocomplete suggestions
- Command appears in help output

**Dependencies**: Task 2 (needs `handleReview()` method to exist)

---

### Task 4: Create CLI Command

**File**: `cmd/howmux/cmd/review.go` (new file)

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/tui"
	"github.com/matthiashowellyopp/howmux/internal/watcher"
)

var reviewCmd = &cobra.Command{
	Use:   "review [PR_URL]",
	Short: "Start PR review workflow",
	Long: `Start the PR review workflow.

With a PR URL: Enroll the PR, review it now, and start the recurring loop.
Without arguments: Start the loop over already-enrolled PRs.

The preflight check runs first and fails if required kiro assets are missing.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Run preflight check first (matching REPL behavior)
		kiroDir := filepath.Join(os.Getenv("HOME"), ".kiro")
		if err := review.CheckReviewAssets(kiroDir); err != nil {
			return fmt.Errorf("PR-review preflight failed:\n%w", err)
		}

		manager := agent.NewManager(cfg)
		w := watcher.New(cfg, manager)

		defer manager.StopAll()
		defer w.Stop()

		// The CLI `review` command enters the TUI with the review command
		// pre-seeded. The TUI handles the actual review workflow.
		// This follows the pattern of other CLI commands that delegate to TUI.
		
		// For now, we'll enter the TUI and let the user execute the review
		// command manually. A future enhancement could auto-execute it.
		return tui.Run(w, manager, cfg)
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)
}
```

**Alternative Implementation** (if we want auto-execution):

The CLI could directly invoke the review workflow before entering TUI, similar to how init/update commands work. However, this requires passing the review watcher to TUI, which adds coupling. The simpler approach is to enter TUI and let the user type `review <url>` or provide a message indicating the command is available.

**Acceptance Criteria**:
- `howmux review` CLI command exists
- `howmux review <url>` parses URL argument
- Preflight check runs before entering TUI
- Help text documents both forms
- Command is registered in root command init

**Dependencies**: Task 1-3 (needs TUI handlers to be functional)

---

### Task 5: Write Command Handler Tests

**File**: `internal/tui/commands_review_test.go` (new file)

**Test Cases**:

1. **`TestHandleReview_Preflight_Missing`**
   - Mock `review.CheckReviewAssets()` to return an error
   - Call `handleReview([]string{"https://github.com/owner/repo/pull/123"})`
   - Assert: Error message appended to activity
   - Assert: No enrollment/checkout/review attempted
   - No real external processes

2. **`TestHandleReview_BareForm_WatcherNotRunning`**
   - Mock watcher state: `Running() = false`
   - Call `handleReview([]string{})`
   - Assert: Watcher started
   - Assert: Success message appended

3. **`TestHandleReview_BareForm_WatcherAlreadyRunning`**
   - Mock watcher state: `Running() = true`
   - Call `handleReview([]string{})`
   - Assert: Warning message "already running"
   - Assert: Watcher not started again

4. **`TestHandleReview_URLForm_ParseError`**
   - Call `handleReview([]string{"invalid-url"})`
   - Assert: Parse error message in activity
   - No enrollment attempted

5. **`TestHandleReview_URLForm_AlreadyEnrolled`**
   - Mock store: `Get()` returns existing record
   - Call `handleReview([]string{"https://github.com/owner/repo/pull/123"})`
   - Assert: Warning message "already enrolled"
   - Review still proceeds

6. **`TestHandleReview_URLForm_EnrollNewPR`**
   - Mock store: `Get()` returns not found
   - Mock store: `Save()` succeeds
   - Call `handleReview([]string{"https://github.com/owner/repo/pull/123"})`
   - Assert: Success message "Enrolled PR"
   - Assert: Record saved with correct fields

7. **`TestHandleReview_URLForm_CheckoutFailure`**
   - Mock `review.EnsureCheckout()` to return error
   - Assert: Warning message (not error)
   - Review proceeds anyway

8. **`TestHandleReview_URLForm_ReviewSuccess`**
   - Mock all dependencies to succeed
   - Call `handleReview([]string{"https://github.com/owner/repo/pull/123"})`
   - Assert: Review runs
   - Assert: Success message with spool path
   - Assert: Watcher started if not running

9. **`TestHandleReview_CommandDispatch_URLArg`**
   - Test that command dispatch extracts URL from "review <url>" input
   - Assert: `handleReview()` receives `["<url>"]` as args

10. **`TestHandleReview_CommandDispatch_Bare`**
    - Test that command dispatch handles bare "review" input
    - Assert: `handleReview()` receives `[]` as args

**Test Doubles Pattern**:
```go
// Mock store implementing StoreInterface
type mockReviewStore struct {
    getFunc    func(repo string, pr int) (review.Record, bool, error)
    saveFunc   func(rec review.Record) error
    listFunc   func() ([]review.Record, error)
    removeFunc func(repo string, pr int) error
}

// Mock watcher with injectable Running() behavior
type mockReviewWatcher struct {
    running bool
    started bool
}

func (m *mockReviewWatcher) Running() bool { return m.running }
func (m *mockReviewWatcher) Start()        { m.started = true }
func (m *mockReviewWatcher) Stop()         {}
```

**Acceptance Criteria**:
- All 10 test cases pass
- Tests use mock doubles for store, watcher, GitHub API, checkout, runner
- No real external processes executed (`gh`, `git`, `pr_review.py`)
- Tests follow existing command test patterns (similar to `watch` tests)
- Test file is properly named and organized

**Dependencies**: Task 2 (needs `handleReview()` implementation)

---

### Task 6: Update Documentation

**File**: `README.md`

**Changes**:

1. **Add to "REPL Commands" table** (after `watch stop` row):
   ```markdown
   | `review [PR_URL]` | Start PR review workflow (URL to enroll/review now, bare to start loop) |
   ```

2. **Add to "CLI Usage" section** (after `howmux update`):
   ```markdown
   # Start PR review workflow
   howmux review https://github.com/owner/repo/pull/123
   ```

3. **Add new section after "Quick Start"**:
   ```markdown
   ## PR Review Workflow

   Howmux can review pull requests and provide feedback via the PR-review workflow.

   ### Prerequisites

   Before using the review workflow, ensure required kiro assets are symlinked:
   - Review agents in `~/.kiro/agents/`
   - Review skills in `~/.kiro/skills/`
   - `pr_review.py` orchestrator on PATH

   See the [ai-resources repository](https://github.com/yourorg/ai-resources) for asset setup.

   ### Usage

   From the REPL:
   ```
   howmux> review https://github.com/owner/repo/pull/123
   ```

   This will:
   1. Run preflight check for required assets
   2. Enroll the PR for tracking
   3. Check out the PR code to `.worktrees/review-owner-repo-123`
   4. Run the review and write results to `~/PR-Review/pending/`
   5. Start the recurring review loop (watches for new commits)

   To start the loop over already-enrolled PRs without enrolling a new one:
   ```
   howmux> review
   ```

   The review loop polls enrolled PRs periodically and triggers reviews when:
   - A review is requested from you
   - A new commit is pushed after the last review
   ```

**Acceptance Criteria**:
- `review` command documented in REPL Commands table
- `review` command documented in CLI Usage section
- New "PR Review Workflow" section explains prerequisites and usage
- All examples are correct and tested
- Markdown formatting is consistent with existing README style

**Dependencies**: All previous tasks (final documentation step)

---

## Validation Commands

### Unit Tests
```bash
# Run all tests
task test

# Run review command tests specifically
go test -v ./internal/tui -run TestHandleReview

# Check test coverage
go test -cover ./internal/tui
```

### Integration Validation
```bash
# Build and run
task build
./howmux

# In REPL:
# 1. Test preflight with missing assets (should fail gracefully)
review https://github.com/owner/repo/pull/123

# 2. Test bare form (should start watcher or show already running)
review

# 3. Test with valid URL after assets are linked
review https://github.com/owner/repo/pull/123
```

### CLI Validation
```bash
# Help text
howmux review --help

# Preflight failure path
howmux review https://github.com/owner/repo/pull/123  # Before linking assets

# Success path
howmux review https://github.com/owner/repo/pull/123  # After linking assets
```

### Linting and Formatting
```bash
task lint
```

## Success Criteria Summary

✅ **Command parsing/dispatch unit-tested** (URL arg vs bare form)  
✅ **Preflight-missing aborts before enroll/checkout** (unit-tested)  
✅ **No real external processes in tests** (all mocked)  
✅ **README updated** (REPL commands table + workflow documentation)  
✅ **All watcher state access uses lock-guarded accessor methods**  
✅ **Concurrent test added** (exercises `Running()` accessor concurrently with state mutations)  

## Notes

- The implementation follows the existing `watch` command pattern for consistency
- Preflight check is **blocking** — any missing asset prevents the entire workflow
- The bare `review` form is idempotent — safe to call repeatedly
- The URL form is also idempotent — enrolling an already-enrolled PR is a no-op with a warning
- Checkout failures are warnings, not errors — reviews can proceed with stale checkouts (the review operates on diffs, not local code)
- The synchronous review in the command handler provides immediate feedback but blocks the TUI — a future enhancement could spawn this as an async agent tab
- This design assumes the watcher will be enhanced to display status (similar to issue watcher) in a future PR
