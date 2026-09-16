# Design Specification: PR-Review Workflow Watch Loop + Pure Decision Function

**Issue**: #64  
**Closes**: #64

## Solution Approach

This issue builds the centerpiece of the PR-review workflow: a recurring poll loop that watches enrolled PRs, decides per-PR whether to review/skip/prune using a **pure, table-tested decision function**, and dispatches reviews while enforcing a concurrency cap. The decision logic follows the `BackoffTracker` lesson from the issue watcher — isolate the decision rules into a pure function testable with in-memory data, separate from the I/O-heavy poll loop shell.

### Design Principles

1. **Pure decision function**: `decideReviewAction(pr github.PR, rec review.Record) ReviewAction` — no I/O, fully deterministic, exhaustively table-tested across the state matrix.

2. **Thin poll loop**: orchestrates fetch → decide → dispatch/prune on a timer, with injectable seams for `gh`, `git`, and `kiro-cli` so the dispatch/branching/cleanup logic is unit-tested with fakes (per #70).

3. **Re-request deduplication**: track `last_serviced_request` as the head SHA at review time; only re-review when a review is requested AND the head SHA differs from the last serviced SHA, preventing duplicate reviews on the same commit.

4. **Concurrency cap**: enforce `max_concurrent_reviews` (default 2) to prevent resource exhaustion and allow reviews to proceed serially or with controlled parallelism.

### Architectural Context

The watch loop coordinates with existing components:

- **Store** (#59): persists `review.Record` state, reads/writes/removes records
- **GitHub PR queries** (#60): fetches PR state via `github.GetPR`
- **Review runner** (#62): dispatches `review.RunReview` for selected PRs
- **Concurrency**: uses a semaphore pattern to cap concurrent review dispatches

This is **not** a port of the issue watcher — the issue watcher tracks labels and spawns kiro-cli agents; the review watcher tracks enrolled PRs and dispatches ACP review sessions via `review.RunReview`.

### Re-Request Deduplication Rule (Design Decision)

GitHub's formal review re-request is the trigger. The loop must not re-review the *same* request on every poll. Proposed rule (to be implemented and documented):

**When a review runs**, store `last_serviced_request` = head SHA at review time. **Only `Review` again** when review is requested (via `pr.IsReviewRequestedFor(reviewer)`) **and** the head SHA differs from `last_serviced_request`.

This prevents re-reviewing on every poll when the PR is still at the same commit. When new commits are pushed, the head SHA changes, so a standing review request re-fires.

The `last_serviced_request` field already exists in `review.Record` (carried forward from an earlier iteration); this design assigns it a clear semantic.

### Concurrency Analysis

This change introduces **cross-goroutine access to shared state**:

1. **Poll loop goroutine** (spawned by `Start()`) reads/writes the internal map tracking active reviews and writes to the store.
2. **TUI/command handlers** (main goroutine) may call `Stop()` or read watcher state concurrently with the poll loop.
3. **Review dispatch** (via `review.RunReview`) is a blocking call within the poll loop but may mutate shared state (store writes).

**Synchronization requirements**:

- `Watcher.mu` (RWMutex) guards:
  - `started` (bool)
  - `activeReviews` (map tracking in-progress reviews for concurrency cap)
- All reads/writes to these fields must acquire the appropriate lock (`RLock()` for reads, `Lock()` for writes).
- Store operations (`store.Save`, `store.Remove`) are already atomic and crash-safe; no additional locking required at the store level.

## Relevant Files

### New Files

- `internal/review/watcher.go` — watch loop orchestrator with poll loop, decision dispatch, concurrency cap
- `internal/review/decision.go` — pure decision function `decideReviewAction`
- `internal/review/watcher_test.go` — poll loop + concurrency cap tests with injectable seams
- `internal/review/decision_test.go` — exhaustive table tests for decision function

### Modified Files

- `internal/review/types.go` — add `ReviewAction` constants, clarify `last_serviced_request` semantics in doc comment
- `internal/review/store.go` — no changes needed (already supports Save/Get/List/Remove)
- `internal/review/runner.go` — update `RunReview` to set `last_serviced_request` = head SHA after successful review

### Reference Files (no changes)

- `internal/github/pr.go` — PR state queries (`GetPR`, `IsReviewRequestedFor`, `IsTerminal`)
- `internal/watcher/dependencies.go` — reference for `BackoffTracker` pattern (no shared code)

## Team Orchestration

All work can proceed in a single implementation pass. The pure decision function is independent of the poll loop wiring, so they can be written in parallel if desired, but both must be complete before the watcher is functional.

**Dependencies within this issue**:

1. **Decision function** (`decision.go` + `decision_test.go`) — pure logic, no external dependencies
2. **Watcher types and poll loop** (`watcher.go` + `watcher_test.go`) — depends on decision function, store, GitHub queries, and runner

**External dependencies** (must already exist per issue description):

- #59: `review.Store` with `Save`, `Get`, `List`, `Remove`
- #60: `github.GetPR`, `PR.IsReviewRequestedFor`, `PR.IsTerminal`, `PR.HeadSHA`
- #62: `review.RunReview` dispatcher

## Step-by-Step Task Breakdown

### Task 1: Define ReviewAction Type and Decision Function Signature

**Acceptance Criteria**:

1. Add `ReviewAction` type and constants to `internal/review/types.go`:
   ```go
   type ReviewAction int

   const (
       ActionSkip   ReviewAction = iota // Skip this PR (not requested or already serviced)
       ActionReview                     // Trigger a review
       ActionPrune                      // Remove from tracking (merged/closed)
   )
   ```

2. Update doc comment for `Record.LastServicedRequest` to document the re-request deduplication rule: "Head SHA at the time the last review request was serviced; used to avoid re-reviewing the same commit on every poll."

3. No behavioral changes yet — this is type/constant definition only.

**Dependencies**: None

---

### Task 2: Implement Pure Decision Function with Exhaustive Table Tests

**Acceptance Criteria**:

1. Create `internal/review/decision.go` with:
   ```go
   // decideReviewAction is a pure function that determines the action for a PR
   // given its current GitHub state and stored record.
   //
   // Rules:
   // - PR merged or closed → ActionPrune
   // - No stored record / never reviewed → ActionReview
   // - Open + review requested for me + request not already serviced → ActionReview
   // - Open + not requested (or request already serviced) → ActionSkip
   //
   // Re-request deduplication: A review request is "already serviced" when
   // rec.LastServicedRequest == pr.HeadSHA(). Only re-review when a review is
   // requested AND the head SHA differs.
   func decideReviewAction(pr github.PR, rec review.Record, reviewer string) ReviewAction
   ```

2. Implement the decision logic per the rules above.

3. Create `internal/review/decision_test.go` with **exhaustive table tests** covering:
   - PR merged → `ActionPrune`
   - PR closed → `ActionPrune`
   - Never reviewed (empty `LastReviewedSHA`) + open → `ActionReview`
   - Open + review requested + never serviced (empty `LastServicedRequest`) → `ActionReview`
   - Open + review requested + head SHA differs from `LastServicedRequest` → `ActionReview`
   - Open + review requested + head SHA matches `LastServicedRequest` → `ActionSkip` (already serviced)
   - Open + not review requested → `ActionSkip`
   - Open + review requested for different reviewer → `ActionSkip`

4. All tests use in-memory `github.PR` and `review.Record` structs — **no `gh` / `git` / network**.

5. Test coverage: every branch in the decision function is exercised.

**Dependencies**: Task 1 (types)

---

### Task 3: Implement Watcher with Poll Loop and Injectable Seams

**Acceptance Criteria**:

1. Create `internal/review/watcher.go` with:
   - `Watcher` struct fields:
     ```go
     type Watcher struct {
         store             *Store
         pollInterval      time.Duration
         maxConcurrent     int
         reviewer          string // GitHub username or team for IsReviewRequestedFor
         stop              chan struct{}
         started           bool
         activeReviews     map[string]bool // "owner/repo#pr" → in-progress
         mu                sync.RWMutex
     }
     ```
   - Constructor `NewWatcher(store *Store, pollInterval time.Duration, maxConcurrent int, reviewer string) *Watcher`
   - `Start()` — spawns poll loop goroutine, sets `started = true`
   - `Stop()` — closes stop channel, sets `started = false`
   - `Running()` — returns `started` (lock-guarded read)

2. Poll loop implementation:
   - Run immediately on `Start()`, then every `pollInterval`
   - Fetch all records via `store.List()`
   - For each record:
     - Parse `rec.Repo` into owner/name
     - Fetch PR state via injectable `fetchPRFunc(repo, pr)` (seam over `github.GetPR`)
     - Call `decideReviewAction(pr, rec, w.reviewer)`
     - **ActionPrune**: call injectable `removeRecordFunc(owner, repo, pr)` (seam over `store.Remove`)
     - **ActionReview**: if concurrency cap allows, dispatch via injectable `dispatchReviewFunc(ctx, rec, pr.HeadSHA())` (seam over `review.RunReview`), increment active count, decrement on completion
     - **ActionSkip**: no-op, continue to next record
   - Respect `w.stop` channel to exit gracefully

3. Concurrency cap logic:
   - Before dispatching `ActionReview`, check `len(w.activeReviews) < w.maxConcurrent`
   - If cap reached, skip this PR (will retry next poll round)
   - Track active reviews in `w.activeReviews` map (key: `"owner/repo#pr"`)
   - Dispatch reviews in separate goroutines; decrement active count on completion

4. Injectable seams (package-level function vars for testing):
   ```go
   var (
       fetchPRFunc       = func(repo string, pr int) (github.PR, error) { return github.GetPR(repo, pr) }
       removeRecordFunc  = func(store *Store, repo string, pr int) error { return store.Remove(repo, pr) }
       dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string) error {
           return RunReview(ctx, rec, ".howmux/reviews", headSHA, NewStore(".howmux/reviews"))
       }
   )
   ```

5. Logging via `internal/logging` at appropriate points (start, stop, poll round start, action per PR).

6. All access to `w.started` and `w.activeReviews` is lock-guarded (`w.mu.RLock()` for reads, `w.mu.Lock()` for writes).

**Dependencies**: Task 2 (decision function)

**Concurrency note**: This introduces cross-goroutine access to `Watcher` state. The acceptance criteria above require lock-guarded access to `started` and `activeReviews`.

---

### Task 4: Update RunReview to Set LastServicedRequest

**Acceptance Criteria**:

1. In `internal/review/runner.go`, after a successful review (before the final `storeImpl.Save(rec)` call), set:
   ```go
   rec.LastServicedRequest = currentSHA
   ```

2. This ensures the decision function's re-request deduplication rule works: the next poll after a review sees `LastServicedRequest == HeadSHA` and skips until a new commit is pushed.

3. No test changes needed — existing `runner_test.go` verifies record updates.

**Dependencies**: None (modifies existing file)

---

### Task 5: Unit Test Poll Loop with Injectable Seams (No Real I/O)

**Acceptance Criteria**:

1. Create `internal/review/watcher_test.go` with tests for:
   - **Poll loop orchestration**: override `fetchPRFunc`, `removeRecordFunc`, `dispatchReviewFunc` with fakes that record calls; verify correct sequence (fetch → decide → dispatch/prune) per record.
   - **Concurrency cap**: set `maxConcurrent=2`, enroll 5 PRs all needing review, verify only 2 reviews dispatched per round (activeReviews map enforces cap).
   - **Prune removes record**: fake `removeRecordFunc` asserts it's called for merged/closed PRs.
   - **Skip does nothing**: fake dispatch never called for skipped PRs.
   - **Graceful stop**: verify poll loop exits when `Stop()` is called.

2. Fake implementations:
   - `fakeFetchPR` — returns pre-configured `github.PR` structs from a map
   - `fakeRemoveRecord` — appends to a slice for assertion, returns nil
   - `fakeDispatchReview` — records calls with (repo, pr, sha) tuples, simulates success/failure

3. Use `time.AfterFunc` or manual ticker control to avoid real-time waits in tests.

4. All tests run entirely in-memory — **no `gh` / `git` / `kiro-cli` / network / subprocess**.

5. Concurrency test uses `sync.WaitGroup` or channel to ensure goroutine completion before assertions.

6. **All access to shared field `activeReviews` is done under its lock** — add an acceptance criterion stating this, and include a test that exercises concurrent dispatch (multiple reviews running in parallel) to verify the lock-guarded map updates work correctly under `go test -race`.

**Dependencies**: Task 3 (watcher implementation)

---

### Task 6: Integration Smoke Test (Optional, If Time Permits)

**Acceptance Criteria**:

1. Create `internal/review/watcher_integration_test.go` with a single smoke test:
   - Use a real tempdir store
   - Enroll 1 PR (fake PR via override `fetchPRFunc` to return open + requested)
   - Start watcher, wait 1 poll interval, verify `dispatchReviewFunc` was called once
   - Stop watcher

2. Still uses fake `fetchPRFunc` and `dispatchReviewFunc` (no real `gh` / `kiro-cli`), but exercises the real timer and goroutine lifecycle.

3. **This is optional** — the unit tests in Task 5 provide full coverage. This is a sanity check for the poll timer + goroutine wiring.

**Dependencies**: Task 5 (unit tests pass)

---

## Validation Commands

### Unit Tests

```bash
# Run all review package tests
go test ./internal/review/... -v

# Verify no real subprocess/network calls in tests
go test ./internal/review/... -v 2>&1 | grep -i "gh\|git\|kiro-cli" && echo "FAIL: real I/O detected" || echo "PASS: pure tests"

# Race detector on concurrency tests
go test ./internal/review/... -race -v
```

### Decision Function Coverage

```bash
# Verify decision function has 100% branch coverage
go test ./internal/review/decision_test.go -cover -v
# Expected: coverage: 100.0% of statements in decision.go
```

### Concurrency Cap Verification

```bash
# Run concurrency cap test in isolation (verbose logging)
go test ./internal/review/watcher_test.go -run TestWatcherConcurrencyCap -v
# Expected: logs show only maxConcurrent reviews dispatched per round
```

### Linting

```bash
# Standard Go linting
task lint

# Verify no naked returns, no magic numbers in decision logic
go vet ./internal/review/...
```

### Documentation Check

```bash
# Verify decision function and watcher have package-level doc comments
go doc github.com/matthiashowellyopp/howmux/internal/review decideReviewAction
go doc github.com/matthiashowellyopp/howmux/internal/review Watcher
```

---

## Open Design Questions (Resolved in This Spec)

1. **Re-request deduplication rule**: Resolved — use `last_serviced_request` = head SHA at review time; only re-review when head SHA differs.

2. **Concurrency cap enforcement**: Resolved — use `activeReviews` map + len check before dispatch, decrement on completion.

3. **Reviewer identity**: Passed as constructor parameter `reviewer string` (GitHub username or team slug) for `IsReviewRequestedFor` check.

---

## Summary

This design isolates the review decision logic into a pure, exhaustively tested function, wraps it in a thin poll loop with injectable seams for all I/O, and enforces a concurrency cap to prevent resource exhaustion. The re-request deduplication rule (`last_serviced_request` = head SHA) prevents duplicate reviews on the same commit. All logic is unit-testable without real `gh` / `git` / `kiro-cli` invocations, following the #70 convention and the `BackoffTracker` lesson from the issue watcher.
