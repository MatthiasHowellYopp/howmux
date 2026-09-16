# Design Specification: Rework Review Runner to Use pr_review.py

**Issue**: #76  
**Closes**: #76, #73  
**Related**: #64 (watch loop), #63 (asset preflight), #59 (state store), #72 (original ACP runner)

## Context

This replaces the ACP-based review runner (#72) with a subprocess invocation of the existing Python tool `pr_review.py` from `ai-resources/workflows/`. Per the #73 decision, there is no ACP-promptable review orchestrator — the working review pipeline is the multi-lens fan-out via `pr_review.py` (one-shot kiro captures, consolidation, spool to `~/PR-Review/pending/` for a human gate). It does **not** auto-post to GitHub.

The ACP path is removed entirely; the new runner fetches the PR diff and invokes `pr_review.py` as a subprocess, streaming stderr to a review tab and capturing stdout for the spool path.

## Solution Approach

### High-Level Strategy

1. **Replace ACP invocation with subprocess**: Instead of `acp.Client.SendMessage(...)`, run `pr_review.py --diff-file <path> --repo <owner/repo> --pr <n> --language auto [--valkey]` via `exec.CommandContext`.

2. **Injectable command seams**: All external commands (diff fetch via `gh pr diff`, `pr_review.py` invocation) go through injectable seams for testing. No real `gh`/`git`/`pr_review.py` in unit tests.

3. **Stream progress to review tab**: Wire the subprocess's **stderr** (phase progress: `→ [1/3] Orienting…`, etc.) to the review tab's output view writer. Capture **stdout** separately for the spool path (the single line `pr_review.py` prints on success).

4. **Context cancellation**: Use `exec.CommandContext(ctx, ...)` so the watcher's `Stop()` method cancels in-flight review subprocesses.

5. **Asset root resolution**: `pr_review.py` resolves agents/skills from `~/.kiro`. The review is diff-based, never run from the PR checkout. The existing `CheckReviewAssets(kiroDir)` preflight from #63 remains unchanged.

6. **Artifact continuity decision**: Choose option **(a)** — drop the prior-artifact-into-prompt path entirely. `pr_review.py` reviews a diff fresh and takes no "prior reviews" input; `pr_review_finalize.py` already avoids duplicating GitHub reviews. Keep `reviews/<sha>.md` only as howmux's own record of what ran (and the spool path). Remove `reviewPromptContext`, `loadPriorReviews`, `loadPriorReviewsFunc` from `runner.go`.

7. **Remove ACP dependencies**: Remove `reviewAgent` constant, `ACPClientFactory` type, `defaultACPClientFactory` var, `validateAgentFunc` seam, `RunReviewWithFactory` ACP code path. No ACP imports or references remain in the review invocation path.

8. **Record update**: On success, set `Status = StatusReviewed`, update `LastReviewedSHA`, `LastReviewedAt`, `LastServicedRequest`, and store the **spool path** in a new `SpoolPath` field on `Record`.

### Why This Approach

- **Reuse existing pipeline**: `pr_review.py` is the working, tested review orchestrator with multi-lens fan-out, consolidation, and nitpick filtering. It's a deliberate scoped exception to "ACP everywhere" — the review pipeline is an external non-ACP orchestrator we reuse wholesale.
- **Human gate preserved**: `pr_review.py` writes to `~/PR-Review/pending/` and never auto-posts. Posting stays the existing human-gated `pr_review_finalize.py` step, outside howmux.
- **Visibility during wait**: Streaming stderr to the tab shows phase progress (orientation, N lenses running, consolidation, verdict) so the review doesn't look hung.
- **Testable without side effects**: Injectable seams allow unit tests to assert the command argv, diff-fetch logic, stdout/stderr routing, and cancellation without running real tools.

## Concurrency Analysis

This change introduces a new **cross-goroutine access** to shared state:

### Goroutine Boundaries Crossed

1. **Watcher poll loop goroutine** (`pollLoop`) → dispatches review in a new goroutine
2. **Dispatched review goroutine** (`dispatch` func) → runs `RunReview`, which will spawn a subprocess
3. **Main/command goroutine** → reads watcher state via `Running()`, triggers `Stop()` which cancels `w.ctx`

### Shared State Access

- **Watcher context (`w.ctx`)**: The watcher-scoped context is created in `NewWatcher` and cancelled in `Stop()`. It's passed to `dispatchReviewFunc` (which becomes `RunReview`), which must pass it to `exec.CommandContext` for subprocess cancellation.
- **Store write after review**: The review goroutine writes the updated record to `StoreInterface`, which already has internal concurrency control (atomic file writes).

### Locking Strategy

- **No new locks required**: The watcher's existing `w.mu` guards `w.activeReviews` and `w.started`. The context cancellation mechanism (`w.ctx`) is already thread-safe (created with `context.WithCancel`, cancelled in `Stop()` under `w.stopOnce`).
- **Subprocess cancellation via context**: The new `RunReview` implementation must use `exec.CommandContext(ctx, ...)` so the subprocess is killed when `ctx` is cancelled (watcher `Stop()`).

### Acceptance Criteria (Concurrency)

1. All subprocess execution uses `exec.CommandContext(ctx, ...)` where `ctx` is the watcher-scoped context passed to `RunReview`.
2. Add a concurrent test (`TestRunReview_Cancellation`) that starts a review, immediately cancels the context, and verifies the subprocess is terminated (via the injectable seam returning `context.Canceled`).
3. Verify via `go test -race internal/review` that no data races are detected.

## Relevant Files

### Files to Modify

| File | Changes |
|------|---------|
| `internal/review/runner.go` | Replace ACP invocation with subprocess runner; remove `reviewPromptContext`, `loadPriorReviews`, `saveReviewArtifact` (artifact write is now `pr_review.py`'s job); add injectable command seams for diff fetch and `pr_review.py` invocation; update `RunReview` signature to accept tab writer |
| `internal/review/runner_test.go` | Replace ACP client factory tests with subprocess seam tests; test diff-fetch argv, `pr_review.py` argv construction, stdout/stderr routing, cancellation via context |
| `internal/review/watcher.go` | Update `dispatchReviewFunc` default to call the new subprocess-based `RunReview`; ensure context is passed through so `Stop()` cancels subprocess |
| `internal/review/types.go` | Add `SpoolPath string` field to `Record` for storing the spool file path returned by `pr_review.py` |
| `internal/review/types_test.go` | Update `Record` validation and JSON serialization tests for the new `SpoolPath` field |

### Files Referenced (Not Modified)

| File | Purpose |
|------|---------|
| `internal/review/assets.go` | Asset preflight check (unchanged; already validates `~/.kiro` agents/skills) |
| `internal/review/store.go` | Record persistence (unchanged; atomic writes already handle concurrency) |
| `internal/review/decision.go` | Review action decision logic (unchanged) |
| `internal/review/checkout.go` | PR checkout logic (unchanged; diff fetch is separate) |
| `ai-resources/workflows/pr_review.py` | External review orchestrator invoked as subprocess (not part of howmux repo) |

## Artifact Continuity Decision

**Decision: (a) — Drop prior-artifact-into-prompt path.**

**Rationale**:
- `pr_review.py` reviews a diff fresh and takes no "prior reviews" input; it has no `--prior-reviews` argument.
- `pr_review_finalize.py` already avoids duplicating a PR's existing GitHub reviews when posting.
- The per-PR artifact-continuity mechanism (feeding prior `reviews/<sha>.md` into the next prompt) was built for ACP-based review agents that might want continuity across re-reviews. `pr_review.py` owns its own prompt and doesn't need this.

**Simplifications**:
- Remove `reviewPromptContext` (assembles prompt from prior reviews; no longer needed).
- Remove `loadPriorReviews` and `loadPriorReviewsFunc` (no longer feeding artifacts into prompt).
- Remove `saveReviewArtifact` and `saveReviewArtifactFunc` (artifact write is now `pr_review.py`'s job; howmux only records the spool path).
- The `reviews/` subdirectory structure under `.howmux/reviews/owner-repo-pr/reviews/` is **removed** — howmux no longer writes its own artifacts there. `pr_review.py` writes to `~/PR-Review/pending/` instead.

**What remains**:
- The top-level `.howmux/reviews/owner-repo-pr/` record directory still exists (it's where the JSON record lives).
- The record gains a `SpoolPath` field to store the path returned by `pr_review.py`.

## Step-by-Step Task Breakdown

### Task 1: Add SpoolPath Field to Record Type

**What**: Add a new `SpoolPath` field to the `Record` struct to store the spool file path returned by `pr_review.py`.

**Files**:
- `internal/review/types.go`
- `internal/review/types_test.go`

**Acceptance Criteria**:
1. `Record` struct in `types.go` has a new `SpoolPath string` JSON field: `json:"spool_path"`
2. `Record.Validate()` does NOT require `SpoolPath` to be non-empty (it's populated only after a successful review; records with `StatusWatching` won't have it yet)
3. `Record.ToJSON()` and `FromJSON()` correctly serialize/deserialize the new field
4. Existing tests in `types_test.go` updated to pass with the new field (add empty/sample values as needed)
5. New test case in `types_test.go`: `TestRecord_SpoolPath_SerializationRoundTrip` verifies a record with a populated `SpoolPath` round-trips through JSON correctly

**Dependencies**: None (can run in parallel with Task 2)

---

### Task 2: Create Injectable Command Seams for Subprocess Execution

**What**: Define injectable function variables (seams) for fetching the PR diff and invoking `pr_review.py`, following the same pattern as `runCommand` in `checkout.go`.

**Files**:
- `internal/review/runner.go`

**Acceptance Criteria**:
1. New package-level var `fetchDiffFunc` with signature `func(ctx context.Context, owner, repo string, pr int, outputFile string) error` — fetches diff via `gh pr diff <pr> --repo <owner/repo>` to `outputFile`, returns error on failure
2. New package-level var `runReviewToolFunc` with signature `func(ctx context.Context, argv []string, stderrWriter io.Writer) (stdoutLines []string, err error)` — runs a command with the given argv, streams stderr to `stderrWriter`, captures stdout lines, returns them and any error
3. Both seams use `exec.CommandContext(ctx, ...)` so they respect context cancellation
4. Both seams are injectable: tests can replace them with fakes that don't run real commands
5. Default implementations reference real `gh` and the `pr_review.py` script (path resolution via `exec.LookPath` or hardcoded to `pr_review.py` assuming it's on `$PATH`)

**Example Default Implementation**:
```go
var fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", strconv.Itoa(pr), "--repo", owner+"/"+repo)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh pr diff failed: %w", err)
	}
	return os.WriteFile(outputFile, output, 0644)
}

var runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = stderrWriter
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("command failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return lines, nil
}
```

**Dependencies**: None (can run in parallel with Task 1)

---

### Task 3: Implement New RunReview with Subprocess Invocation

**What**: Replace the ACP-based `RunReview` implementation with a subprocess-based implementation that fetches the diff, invokes `pr_review.py`, streams stderr to a tab writer, captures stdout for the spool path, and updates the record.

**Files**:
- `internal/review/runner.go`

**Acceptance Criteria**:
1. `RunReview` signature becomes: `func RunReview(ctx context.Context, rec Record, headSHA string, storeImpl StoreInterface, tabWriter io.Writer) error`
   - `ctx`: watcher-scoped context for cancellation
   - `rec`: the review record
   - `headSHA`: the commit SHA to review
   - `storeImpl`: the store for persisting the updated record
   - `tabWriter`: where to stream `pr_review.py` stderr (phase progress)
2. Implementation:
   a. Parse `rec.Repo` into `owner/name`
   b. Create a temp file for the diff: `os.CreateTemp("", "pr-*.diff")`
   c. Call `fetchDiffFunc(ctx, owner, repo, rec.PR, diffFile)` to fetch the diff
   d. Construct `pr_review.py` argv: `["pr_review.py", "--diff-file", diffFile, "--repo", rec.Repo, "--pr", strconv.Itoa(rec.PR), "--language", "auto"]`
      - Add `--valkey` flag if language is detected as `python` or `go` (simple heuristic: if `rec.ReviewDir` contains "valkey" or "redis" in path, or we skip this logic and always let `pr_review.py` auto-detect)
      - For simplicity: always use `--language auto` and let `pr_review.py` detect both language and valkey
   e. Call `runReviewToolFunc(ctx, argv, tabWriter)` to run `pr_review.py`, streaming stderr to `tabWriter`, capturing stdout
   f. Parse stdout: the last non-empty line is the spool path
   g. Clean up temp diff file: `os.Remove(diffFile)`
   h. Update record: `rec.Status = StatusReviewed`, `rec.LastReviewedSHA = headSHA`, `rec.LastReviewedAt = time.Now().Format(time.RFC3339)`, `rec.LastServicedRequest = headSHA`, `rec.SpoolPath = spoolPath`
   i. Call `storeImpl.Save(rec)` to persist atomically
3. Remove `RunReviewWithFactory` (ACP factory-based version) entirely
4. Remove `reviewPromptContext`, `loadPriorReviews`, `loadPriorReviewsFunc`, `saveReviewArtifact`, `saveReviewArtifactFunc` — all dead code after switching to subprocess
5. Remove `reviewAgent` constant, `ACPClientFactory` type, `defaultACPClientFactory` var, `validateAgentFunc` seam
6. Remove all ACP imports: `"github.com/matthiashowellyopp/howmux/internal/acp"` should not be imported in `runner.go` after this task
7. Preserve `timeNow` injectable seam (still used for `LastReviewedAt`)

**Error Handling**:
- If `fetchDiffFunc` fails: return error immediately (no partial state saved)
- If `runReviewToolFunc` fails: clean up temp diff file, return error (no partial state saved)
- If `storeImpl.Save` fails: return error (spool file is written by `pr_review.py`, but record isn't persisted — watcher will retry on next poll)

**Concurrency**:
- All subprocess execution uses `exec.CommandContext(ctx, ...)` so cancellation via `ctx.Done()` kills the subprocess
- If `ctx` is cancelled mid-review: subprocess exits, function returns `context.Canceled` error, no record update

**Dependencies**: Task 1 (SpoolPath field), Task 2 (command seams)

---

### Task 4: Update Watcher to Pass TabWriter to RunReview

**What**: Update the `dispatchReviewFunc` seam in `watcher.go` to pass a tab writer to `RunReview`, and update the default implementation to call the new subprocess-based `RunReview`.

**Files**:
- `internal/review/watcher.go`

**Acceptance Criteria**:
1. `dispatchReviewFunc` signature becomes: `func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error`
2. Default implementation updated: `dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error { return RunReview(ctx, rec, headSHA, store, tabWriter) }`
3. In `watcher.go`, the `dispatch` method passes `w.ctx` (watcher-scoped context) to `dispatchReviewFunc` so `Stop()` cancels in-flight reviews
4. For now, `tabWriter` is passed as `io.Discard` (or a TODO comment) — wiring the actual review tab writer is deferred to when howmux integrates tab management (#64 or later)
5. Update the call site in `dispatch` to pass the new `tabWriter` argument

**Temporary Solution**:
Since the review tab integration is tracked in a separate issue, pass `io.Discard` for `tabWriter` initially:
```go
if err := dispatchReviewFunc(w.ctx, r, sha, w.store, io.Discard); err != nil {
```

**Dependencies**: Task 3 (new RunReview signature)

---

### Task 5: Write Unit Tests for Subprocess-Based Runner

**What**: Replace the ACP factory-based tests in `runner_test.go` with tests for the new subprocess-based runner using injectable command seams.

**Files**:
- `internal/review/runner_test.go`

**Acceptance Criteria**:
1. Remove all tests that reference `ACPClientFactory`, `RunReviewWithFactory`, `mockACPClient` — these test the old ACP path
2. New test: `TestRunReview_DiffFetch_Argv` — asserts `fetchDiffFunc` is called with correct `owner`, `repo`, `pr`, and a temp file path
3. New test: `TestRunReview_ReviewTool_Argv` — asserts `runReviewToolFunc` is called with correct argv: `["pr_review.py", "--diff-file", "<path>", "--repo", "owner/repo", "--pr", "42", "--language", "auto"]`
4. New test: `TestRunReview_StderrRouting` — verifies stderr from `runReviewToolFunc` is written to the provided `tabWriter` (inject a `bytes.Buffer` as tabWriter, check it contains the stderr lines)
5. New test: `TestRunReview_StdoutCapture_SpoolPath` — verifies stdout is captured and the last non-empty line becomes `rec.SpoolPath`
6. New test: `TestRunReview_RecordUpdate_Success` — verifies record is updated with `StatusReviewed`, `LastReviewedSHA`, `LastReviewedAt`, `LastServicedRequest`, `SpoolPath` and saved to store
7. New test: `TestRunReview_Cancellation` — verifies that cancelling the context mid-review causes the subprocess seam to return `context.Canceled` and no record is saved (concurrent test: start review in goroutine, cancel context immediately, assert error is `context.Canceled`)
8. New test: `TestRunReview_DiffFetchFailure` — verifies that if `fetchDiffFunc` returns error, `RunReview` returns error immediately and no record is saved
9. New test: `TestRunReview_ReviewToolFailure` — verifies that if `runReviewToolFunc` returns error, temp diff file is cleaned up and no record is saved
10. Remove tests for `reviewPromptContext`, `loadPriorReviews`, `saveReviewArtifact` — these functions no longer exist

**Injectable Seam Fakes**:
```go
fakeFetchDiff := func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
	// Write a fake diff to outputFile
	return os.WriteFile(outputFile, []byte("fake diff content"), 0644)
}

fakeRunReviewTool := func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
	// Write fake stderr progress
	io.WriteString(stderrWriter, "→ [1/3] Orienting…\n")
	io.WriteString(stderrWriter, "→ [2/3] Running lenses…\n")
	io.WriteString(stderrWriter, "→ [3/3] Consolidating…\n")
	io.WriteString(stderrWriter, "→ verdict: APPROVE\n")
	// Return fake spool path as stdout
	return []string{"/Users/test/PR-Review/pending/pr-review-owner-repo-42.md"}, nil
}
```

**Dependencies**: Task 3 (new RunReview implementation)

---

### Task 6: Update Watcher Tests for New dispatchReviewFunc Signature

**What**: Update tests in `watcher_test.go` that reference `dispatchReviewFunc` to use the new signature (with `tabWriter` argument).

**Files**:
- `internal/review/watcher_test.go`

**Acceptance Criteria**:
1. All tests that set `dispatchReviewFunc` to a fake are updated to match the new signature: `func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error`
2. Fake implementations updated to accept and ignore `tabWriter` (since watcher tests don't assert on stderr content; they only assert that `dispatchReviewFunc` was called)
3. All watcher tests pass after the signature change

**Example Update**:
```go
var called bool
dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
	called = true
	return nil
}
```

**Dependencies**: Task 4 (new dispatchReviewFunc signature)

---

### Task 7: Run Full Test Suite and Fix Any Breakages

**What**: Run the full test suite with race detection and fix any test failures introduced by the refactor.

**Files**:
- All test files under `internal/review/`

**Acceptance Criteria**:
1. `go test ./internal/review/...` passes with zero failures
2. `go test -race ./internal/review/...` passes with zero data races
3. `task test` (project-level test task) passes
4. `task fmt:check lint` passes (no formatting or linting errors)
5. `task sync:check` passes (template files are in sync)
6. `task build` succeeds

**Dependencies**: Task 1 through Task 6 complete

---

## Team Orchestration

### Parallel Work (Tasks 1 and 2)

Tasks 1 (add SpoolPath field) and Task 2 (create injectable seams) can run **in parallel** — they touch different parts of the codebase and have no dependencies on each other:
- **Task 1**: modifies `types.go` and `types_test.go` (Record struct)
- **Task 2**: modifies `runner.go` (adds injectable function vars)

### Sequential Work (Tasks 3–7)

Tasks 3 through 7 must run **sequentially**:
- **Task 3** depends on Task 1 (uses `SpoolPath` field) and Task 2 (uses command seams)
- **Task 4** depends on Task 3 (calls the new `RunReview` signature)
- **Task 5** depends on Task 3 (tests the new `RunReview` implementation)
- **Task 6** depends on Task 4 (tests the new `dispatchReviewFunc` signature)
- **Task 7** depends on all prior tasks (integration verification)

### Suggested Execution Order

1. **Parallel**: Task 1 + Task 2
2. **Sequential**: Task 3 → Task 4 → Task 5 → Task 6 → Task 7

## Validation Commands

### Unit Tests
```bash
# Run review package tests
go test ./internal/review/...

# Run with race detection
go test -race ./internal/review/...

# Run with verbose output
go test -v ./internal/review/...
```

### Full Project Gate
```bash
# Format check
task fmt:check

# Linting
task lint

# Template sync check
task sync:check

# All tests
task test

# Build
task build
```

### Verification Checklist

After all tasks complete, verify:

1. **No ACP dependencies remain in runner.go**:
   ```bash
   grep -n "acp\." internal/review/runner.go
   # Should return zero matches
   ```

2. **No prior-artifact functions remain**:
   ```bash
   grep -n "loadPriorReviews\|saveReviewArtifact\|reviewPromptContext" internal/review/runner.go
   # Should return zero matches
   ```

3. **SpoolPath field is serialized**:
   ```bash
   go test -v -run TestRecord.*SpoolPath internal/review/types_test.go
   ```

4. **Subprocess cancellation works**:
   ```bash
   go test -v -run TestRunReview_Cancellation internal/review/runner_test.go
   ```

5. **Race detector is clean**:
   ```bash
   go test -race ./internal/review/
   ```

## Mechanical Change Surface Enumeration

This is **not** a mechanical rename/move, but it does have a sweeping removal surface (all ACP-related code in the review path). Here's the complete affected surface:

### Source Code (Go files)
- `internal/review/runner.go`: remove ACP client, factory, imports; add subprocess seams and implementation
- `internal/review/types.go`: add `SpoolPath` field
- `internal/review/watcher.go`: update `dispatchReviewFunc` signature

### Test Files
- `internal/review/runner_test.go`: remove all ACP factory tests; add subprocess seam tests
- `internal/review/types_test.go`: add `SpoolPath` serialization tests
- `internal/review/watcher_test.go`: update fake `dispatchReviewFunc` signatures

### Verification Commands in Acceptance Criteria

See **Task 1, Criterion 5**: `TestRecord_SpoolPath_SerializationRoundTrip`  
See **Task 5, Criterion 7**: `TestRunReview_Cancellation`  
See **Task 7, Criterion 2**: `go test -race ./internal/review/...`  
See **Verification Checklist** above for grep-checkable completeness checks

## Implementation Notes

### Diff File Cleanup

The temp diff file created via `os.CreateTemp` must be cleaned up in all code paths (success, error, cancellation). Use `defer os.Remove(diffFile)` immediately after creating the file.

### Stdout Parsing

`pr_review.py` prints the spool path as its **single stdout line** on success. If stderr is noisy (progress messages), stdout will still contain only the spool path. Parse stdout as:
```go
lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
spoolPath := lines[len(lines)-1] // last non-empty line
```

### TabWriter Integration (Deferred)

The `tabWriter` argument to `RunReview` is passed as `io.Discard` for now. When howmux integrates review tab management (likely in #64 or a follow-up), the watcher will pass the actual tab's output writer, and stderr from `pr_review.py` will stream to the tab in real-time.

### Asset Preflight (Unchanged)

The asset preflight check (`CheckReviewAssets` from #63) validates that all required agents/skills exist under `~/.kiro` before the watcher starts. This remains unchanged — `pr_review.py` uses those same agents/skills, so the preflight is still correct.

### Language and Valkey Detection

For simplicity, always pass `--language auto` to `pr_review.py` and let it detect both the language and valkey usage from the diff. This avoids duplicating the detection logic in Go. If explicit control is needed later, the argv construction in Task 3 can be extended.

## Summary

This design replaces the ACP-based review runner with a subprocess-based implementation that invokes the existing `pr_review.py` orchestrator. It removes all ACP dependencies from the review path, streams progress to a review tab, and records the spool path for later human-gated posting. The implementation uses injectable command seams for testability, respects context cancellation for clean shutdown, and drops the now-redundant prior-artifact continuity mechanism.

All acceptance criteria from the issue are addressed:
- ✅ Diff fetch + `pr_review.py` invocation go through injectable seams (Task 2, Task 5)
- ✅ Subprocess uses `exec.CommandContext` for cancellation (Task 3, Task 5 criterion 7)
- ✅ stdout captured, stderr routed to tab writer (Task 3, Task 5 criteria 4-5)
- ✅ Record ends `StatusReviewed` with SHA/time/spool-path (Task 3, Task 5 criterion 6)
- ✅ Artifact-continuity decision documented (option a chosen, dead code removed — Task 3 criterion 4)
- ✅ ACP client/factory/agent removed (Task 3 criterion 6)
- ✅ Full gate green (Task 7)
