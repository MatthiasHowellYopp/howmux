> Closes #115

# Design Spec: Reviews tab shows 'reviewing' status while a review is in progress

## Summary

`StatusReviewing` already exists (`internal/review/types.go:14`) and is already
colored Warning by `styleStatus` (`internal/tui/reviews_tab.go:296-322`), but
nothing ever writes it to a record. `RunReview` (`internal/review/runner.go:92`)
only calls `storeImpl.Save(rec)` once, at the very end, on success
(`runner.go:161`). This spec adds a `StatusReviewing` write at the start of
`RunReview` and confirms/tests that the status always transitions out of
`StatusReviewing` — on success (existing success path) and on every error exit
(new) — and that a crash/interruption leaves a record that is still
re-reviewable on the next poll.

No TUI changes are required: `ReviewsTab.View()` calls `store.List()` fresh on
every render (`internal/tui/reviews_tab.go:112`), and `styleStatus` already
renders `StatusReviewing` in Warning (`reviews_tab.go:320-321`, covered by
`internal/tui/reviews_tab_test.go:126`, `:724`, `:806`). This is entirely an
`internal/review` package change — see "Relevant Files" below.

## Root Cause (confirmed by code reading)

- `internal/review/types.go:12-17` — `Status` enum: `StatusWatching`,
  `StatusReviewing`, `StatusReviewed`, `StatusDone`. `StatusReviewing` is a
  valid enum value already accepted by `Record.Validate()` (`types.go:56`).
- `internal/review/runner.go:92-166` (`RunReview`) — the only two things it
  ever writes to the store:
  1. Nothing at the start.
  2. `rec.Status = StatusReviewed` + timestamps + `SpoolPath`, then
     `storeImpl.Save(rec)` at `runner.go:157-163`, reached only after
     `fetchDiffFunc`, `runReviewToolFunc`, and `WriteReviewedMetadata` all
     succeed.
  Every error return (`fetchDiffFunc` failure at `runner.go:120`,
  `runReviewToolFunc` failure at `runner.go:136`, empty-stdout at
  `runner.go:141`, bad spool path at `runner.go:145`,
  `WriteReviewedMetadata` failure at `runner.go:155`) returns *before* any
  `Save` call — confirmed by `TestRunReview_DiffFetchFailure`
  (`runner_test.go:465-518`), which explicitly asserts
  `store.Get("owner/repo", 1)` returns `found == false` after a diff-fetch
  failure, i.e. today nothing is ever persisted until the very end.
- `internal/review/watcher.go:187-232` (`dispatch`) — the watcher's
  `activeReviews map[string]bool` (declared `watcher.go:41`) is an in-memory,
  per-`Watcher`-instance guard against double-dispatch. It is never read by
  the TUI and has no relationship to the on-disk `Record.Status` — it exists
  purely to stop the poll loop from firing a second `RunReview` for a PR
  whose previous review outlives one poll interval (comment at
  `watcher.go:179-183`).
- `internal/tui/reviews_tab.go:296-322` (`styleStatus`) — already switches on
  `review.StatusReviewing` → `rt.styles.Warning.Render(text)`
  (`reviews_tab.go:320-321`). Already covered by
  `internal/tui/reviews_tab_test.go:126` (fixture with `Status:
  review.StatusReviewing`), `:724` and `:806` (unselected-row Warning-color
  assertion). **No TUI code change needed.**
- `internal/review/decideactions.go:17-41` (`decideReviewAction`) — decides
  `ActionReview` vs `ActionSkip` vs `ActionPrune` purely from
  `pr.IsTerminal()`, `rec.LastReviewedSHA`, `pr.IsReviewRequestedFor(reviewer)`,
  and `rec.LastServicedRequest` vs `pr.HeadSHA()`. **`rec.Status` is never
  read by this function.** This confirms the issue's "Design consideration":
  since `LastReviewedSHA`/`LastServicedRequest` are only ever written in the
  `StatusReviewed` write at the end of `RunReview`, a record stuck on
  `StatusReviewing` (crash mid-review) still has its *pre-review*
  `LastReviewedSHA`/`LastServicedRequest` values, so `decideReviewAction` will
  return `ActionReview` again on the next poll exactly as if the review had
  never started. Dedup is unaffected by this change.

## Call sites that invoke `RunReview` (both must show `reviewing`)

1. **Watcher poll-triggered dispatch**: `watcher.go:24`
   (`dispatchReviewFunc` var) → `watcher.go:216`
   (`dispatch`'s goroutine calls `dispatchReviewFunc(w.ctx, r, sha, w.store,
   io.Discard)`). `r` here is the `Record` passed into `w.dispatch(rec,
   pr.HeadSHA())` from `pollOnce` (`watcher.go:172`), which is exactly the
   `Record` most recently read from `w.store.List()` (`watcher.go:127`) — so
   putting the `StatusReviewing` write inside `RunReview` covers this path
   with no watcher.go change needed.
2. **Manual `review <URL>`**: `internal/tui/commands.go:520`
   (`runReviewFunc` var, wraps `review.RunReview`) is called from the
   `reviewCmd` closure at `commands.go:1105-1107`, using a `rec` freshly
   re-read from the store at `commands.go:1097-1100` right before the call.
   Same conclusion: a `RunReview`-internal write covers this path too, with
   no `commands.go` change needed.

Both call sites converge on `RunReview`, so the fix belongs entirely inside
`internal/review/runner.go`. This is the correct architectural placement:
`RunReview` is the single choke point for "a review is happening right now"
regardless of trigger.

## Solution Approach

1. At the very start of `RunReview`, before any of the current work (temp
   file creation, diff fetch, tool invocation), write `rec.Status =
   StatusReviewing` and persist it via `storeImpl.Save(rec)`. Do this after
   parsing `rec.Repo` (so a malformed-repo record still fails fast without a
   spurious status write — matches the existing early-return-before-any-I/O
   shape at `runner.go:97-101`), but before `logging.Info("starting PR
   review", ...)` (`runner.go:103`) and before the temp-file/diff-fetch work.
2. If the `StatusReviewing` `Save` itself fails, return that error
   immediately (mirrors the existing `Save` error handling at
   `runner.go:161-163`: `"failed to update record: %w"`) — do not proceed
   into diff fetch with an un-persisted status, and do not swallow the
   error.
3. Every subsequent error return path in `RunReview` (diff fetch failure,
   review-tool failure, empty-stdout, bad spool path, metadata-stamp
   failure) must, before returning the error, write the record back to a
   terminal-for-this-attempt status so it never stays visually stuck on
   `reviewing`. Use `StatusWatching` as the revert-on-error status — it is
   the existing "enrolled, not currently reviewing, eligible for the next
   poll" state (`types.go:13`) and requires no new enum value. Concretely:
   add a small helper (e.g. `revertToWatching(storeImpl StoreInterface, rec
   Record) `) that sets `rec.Status = StatusWatching` and calls
   `storeImpl.Save(rec)`, called from each error branch. A `Save` failure
   inside this revert-helper should be logged (`logging.Warn`) but must NOT
   mask/replace the original error being returned — the caller needs to see
   the real failure reason, and a` Reviews` tab row stuck on `reviewing`
   after a failed revert-write is still correctly re-reviewable per the
   dedup analysis above (worst case it just doesn't visually revert
   promptly).
4. The success path is unchanged in effect — it already ends by writing
   `StatusReviewed` (`runner.go:157-163`) — but now that transition is *out
   of* `StatusReviewing` rather than out of whatever stale status the record
   had before (`StatusWatching` or a prior `StatusReviewed`).
5. `rec.Status = StatusDone` is set elsewhere (pruning in
   `watcher.go:165-170` via `removeRecordFunc`/`store.Remove`, which deletes
   the record entirely rather than setting `StatusDone` — confirm by reading
   `store.go:190-203`'s `Remove`). Grep confirms `StatusDone` is not written
   anywhere in `internal/review` today; it's read-only in
   `reviews_tab.go:318` (`styleStatus`) and in fixtures/tests. **This spec
   does not add `StatusDone` writing** — that is out of scope for #115 (the
   issue's acceptance criteria say "reviewed (or done if merged/closed)"
   descriptively, but pruning already removes done/closed PRs from the store
   entirely via `ActionPrune`, so there is no live record left to show
   `done` on — confirmed by `decideReviewAction`'s Rule 1,
   `decideactions.go:18-21`, and `pollOnce`'s `ActionPrune` case,
   `watcher.go:165-171`, which calls `store.Remove`, not `store.Save` with
   `StatusDone`). Do not attempt to introduce `StatusDone` writing; it would
   be undocumented, untested scope creep contradicted by the existing
   architecture. If a reviewer wants `StatusDone` surfaced before removal,
   that is a separate issue.

## No Concurrency Concern

This change adds no new cross-goroutine shared-state access. `RunReview` runs
entirely within whichever goroutine called it (the watcher's per-PR
dispatch goroutine at `watcher.go:213-231`, or the `tea.Cmd` closure at
`commands.go:1081-1112`, which Bubble Tea runs off the render goroutine and
delivers back via message). `storeImpl.Save` is already the sole
synchronization boundary (filesystem-atomic rename, `store.go:113-146`) and
already handles concurrent writers safely — this change just calls the
existing `Save` one additional time per review, with the same `Record`
value already owned by that goroutine. No new field, mutex, or shared map is
introduced. `activeReviews` (`watcher.go:41`) is untouched.

## Relevant Files

| File | Change |
|---|---|
| `internal/review/runner.go` | **Modify.** Add `StatusReviewing` write at the start of `RunReview` (after repo-format validation, before temp-file/diff-fetch). Add a `revertToWatching` helper and call it from every error-return branch. |
| `internal/review/runner_test.go` | **Modify.** `TestRunReview_DiffFetchFailure` (`runner_test.go:465-518`) currently asserts `found == false` after a diff-fetch failure — this assertion **must change** to `found == true` + `Status == StatusWatching` (or equivalent), since a `StatusReviewing` record is now saved before the diff fetch runs and then reverted to `StatusWatching` on failure. Add new tests (see Testing section). |
| `internal/review/watcher_test.go` | **Modify (add tests only).** Add a test asserting a `StatusReviewing`-stuck record (simulating a crash mid-review, i.e. `Status: StatusReviewing`, `LastReviewedSHA: ""`/stale) still yields `ActionReview` from `decideReviewAction`, and/or a watcher-level integration test that the record transitions `watching → reviewing → reviewed` across a dispatched review. |
| `internal/review/decideactions_test.go` | **Modify (add tests only, optional but recommended).** Add a focused unit test on `decideReviewAction` directly: a `Record{Status: StatusReviewing, LastReviewedSHA: "", LastServicedRequest: ""}` still returns `ActionReview` (never-reviewed rule) — and a second case where `LastReviewedSHA` is non-empty but stale (simulating "reviewing a new commit after a previous completed review, crashed mid-way") still returns `ActionReview` under Rule 3 because `LastServicedRequest != pr.HeadSHA()`. |
| `internal/tui/reviews_tab.go` | **No change.** `styleStatus` already handles `StatusReviewing` → Warning (`reviews_tab.go:320-321`). |
| `internal/tui/reviews_tab_test.go` | **No change.** Existing coverage (`:126`, `:724`, `:806`) already exercises `StatusReviewing` → Warning rendering; verify these still pass, do not add redundant tests here. |
| `internal/review/types.go` | **No change.** `StatusReviewing` and `Validate()` already support it. |
| `internal/review/watcher.go` | **No change.** `dispatch`/`pollOnce` pass the same `Record` through to `RunReview`; the fix is fully contained in `RunReview`. |
| `internal/tui/commands.go` | **No change.** The manual-review closure (`commands.go:1081-1112`) calls `runReviewFunc` → `review.RunReview`, which now handles the status transition internally. |

## Step-by-Step Task Breakdown

### Task 1: Add `StatusReviewing` write and error-path revert in `RunReview`
**File:** `internal/review/runner.go`
**Dependencies:** None — this is the foundational change.

- After the repo-format parse/validation (`runner.go:97-101`) and before
  `logging.Info("starting PR review", ...)` (`runner.go:103`), set
  `rec.Status = StatusReviewing` and call `storeImpl.Save(rec)`. If this
  `Save` fails, return `fmt.Errorf("failed to persist reviewing status:
  %w", err)` immediately (do not proceed to diff fetch).
- Add a small unexported helper:
  ```go
  // revertToWatching reverts rec to StatusWatching and persists it, used on
  // every error exit from RunReview so a record can never remain stuck on
  // StatusReviewing after a failed/aborted review attempt. A Save failure
  // here is logged, not returned — the caller's original error takes
  // precedence, and a record left on StatusReviewing is still re-reviewable
  // on the next poll (decideReviewAction does not read Status).
  func revertToWatching(storeImpl StoreInterface, rec Record) {
      rec.Status = StatusWatching
      if err := storeImpl.Save(rec); err != nil {
          logging.Warn("failed to revert record to StatusWatching after review error", "repo", rec.Repo, "pr", rec.PR, "error", err)
      }
  }
  ```
  Note: `rec` must be the same value (with `Status = StatusReviewing` already
  set from Task 1's first write) at the point `revertToWatching` is called,
  so pass the *local* `rec` variable, not a stale copy.
- Call `revertToWatching(storeImpl, rec)` immediately before each of the
  following existing `return fmt.Errorf(...)` statements (after the
  `StatusReviewing` write is in place, all of these run after that write and
  before the final `StatusReviewed` write):
  - diff fetch failure (`runner.go:120`, `"failed to fetch PR diff: %w"`)
  - review-tool invocation failure (`runner.go:136`, `"pr_review.py
    invocation failed: %w"`)
  - empty-stdout (`runner.go:140-141`, `"pr_review.py produced no output..."`)
  - bad spool path (`runner.go:144-145`, `"pr_review.py returned an
    unexpected spool path..."`)
  - `WriteReviewedMetadata` failure (`runner.go:154-155`, `"failed to stamp
    reviewed metadata..."`)
- Do **not** add a revert call before the final `storeImpl.Save(rec)` error
  return (`runner.go:161-163`, `"failed to update record: %w"`) — that
  branch's `Save` call IS the attempted transition out of `StatusReviewing`;
  if it fails, the record legitimately remains on `StatusReviewing` on disk
  (the write never landed), which is the correct/only honest state, and it
  remains re-reviewable next poll for the same dedup reason.
- Context-cancellation (`ctx` cancelled via `Watcher.Stop()`) surfaces as a
  `runReviewToolFunc`/`fetchDiffFunc` error through the normal error-return
  paths above (confirmed by `TestRunReview_Cancellation`,
  `runner_test.go:397-464`, which exercises this via context cancellation
  during the fake `runReviewToolFunc`) — no separate cancellation-specific
  handling is needed; it flows through the existing error branches.

**Acceptance criteria:**
- `RunReview` persists `rec.Status = StatusReviewing` via `storeImpl.Save`
  before any diff fetch or subprocess invocation.
- Every error-return branch in `RunReview` (5 branches listed above) calls
  `revertToWatching` before returning, so no error exit leaves the record on
  `StatusReviewing`.
- The final `Save`-error branch (`runner.go:161-163`) is explicitly NOT
  reverted (documented in a code comment explaining why, per above).
- `go build ./...` succeeds.

### Task 2: Update `TestRunReview_DiffFetchFailure` for the new persisted-then-reverted behavior
**File:** `internal/review/runner_test.go`
**Dependencies:** Task 1.

- `TestRunReview_DiffFetchFailure` (`runner_test.go:465-518`) currently
  asserts (lines ~510-518):
  ```go
  _, found, err := store.Get("owner/repo", 1)
  ...
  if found {
      t.Error("expected record to NOT exist (should not be saved on failure)")
  }
  ```
  This assumption is now false: `RunReview` saves a `StatusReviewing` record
  before the diff fetch runs, then reverts it to `StatusWatching` on
  failure. Update the assertion to:
  ```go
  updated, found, err := store.Get("owner/repo", 1)
  if err != nil {
      t.Fatalf("failed to check record: %v", err)
  }
  if !found {
      t.Fatal("expected record to exist (StatusReviewing write happens before diff fetch)")
  }
  if updated.Status != StatusWatching {
      t.Errorf("expected record reverted to StatusWatching after diff-fetch failure, got %q", updated.Status)
  }
  ```
- Update the test's doc comment (the `// Verify record was NOT saved`
  comment) to describe the new expected behavior.

**Acceptance criteria:**
- `TestRunReview_DiffFetchFailure` passes against the Task 1 implementation
  and accurately documents the revert-on-error contract.
- `go test ./internal/review/... -run TestRunReview_DiffFetchFailure -v`
  passes.

### Task 3: Add new tests for the `StatusReviewing` write, completion transition, and error revert
**File:** `internal/review/runner_test.go`
**Dependencies:** Task 1 (can be written in parallel with Task 2, both land in the same file — coordinate to avoid merge conflicts within one PR, e.g. by having one task's author add all runner_test.go changes).

Add the following new test functions:

1. `TestRunReview_SetsStatusReviewingBeforeWork` — install a `fetchDiffFunc`
   fake that, before writing the diff file, reads the record back from the
   store (`store.Get(rec.Repo, rec.PR)`) and captures its `Status` into a
   test-scoped variable; after `RunReview` returns (success case, reuse the
   `writeTestSpoolFixture` pattern from existing tests), assert the captured
   status was `StatusReviewing` — proving the write happens before the
   long-running work starts, not just "at some point."
2. `TestRunReview_ReviewToolFailure_RevertsToWatching` — extend the existing
   `TestRunReview_ReviewToolFailure` (`runner_test.go:524-590`) or add a
   sibling test asserting that after a `runReviewToolFunc` failure, the
   stored record's `Status == StatusWatching` (mirroring Task 2's pattern).
3. `TestRunReview_SpoolPathValidationFailure_RevertsToWatching` — same
   pattern for the bad-spool-path branch (`runner.go:144-145`): fake
   `runReviewToolFunc` returns a line that fails `validateSpoolPath` (e.g.
   `[]string{"not-a-spool-path.txt"}`), assert `RunReview` errors and the
   record reverts to `StatusWatching`.
4. `TestRunReview_SuccessTransitionsOutOfReviewing` — using the existing
   success fixture pattern (`TestRunReview_RecordUpdate_Success`,
   `runner_test.go:325-393`), additionally assert that at no point does the
   final stored record show `StatusReviewing` — i.e. `updated.Status ==
   StatusReviewed` (this may already be implicitly covered by
   `TestRunReview_RecordUpdate_Success`'s existing assertion; add an
   explicit `!= StatusReviewing` check only if it adds clarity, otherwise
   rely on the existing `== StatusReviewed` assertion as sufficient — avoid
   duplicate assertions for the same fact).

**Acceptance criteria:**
- All four new/extended tests are added to `internal/review/runner_test.go`
  following the existing seam-save/seam-restore (`defer func() { ... }()`)
  and `t.TempDir()`-backed store conventions already used throughout this
  file.
- `go test ./internal/review/... -run TestRunReview -v` passes, covering:
  start-of-review write, completion transition out of it, and at least two
  distinct error-revert branches.

### Task 4: Add a test proving a stale `StatusReviewing` record remains re-reviewable
**File:** `internal/review/decideactions_test.go`
**Dependencies:** None (tests `decideReviewAction` directly, independent of Tasks 1-3 — can run in parallel).

`decideReviewAction` (`decideactions.go:17-41`) never reads `rec.Status`, so
this test documents/locks in that invariant explicitly rather than relying on
incidental behavior. Add:

```go
// TestDecideReviewAction_StaleReviewingRecordIsStillReviewable documents and
// locks in the dedup invariant issue #115 depends on: decideReviewAction
// decides purely from LastReviewedSHA / LastServicedRequest (write-once, at
// the END of RunReview), never from Status. A record left on StatusReviewing
// by a crashed/interrupted review — which by definition never reached the
// completion write — must still be picked up as ActionReview on the next
// poll, exactly as if the review had never started. See runner.go's
// StatusReviewing write (issue #115) and RunReview's completion write
// (runner.go ~157-163).
func TestDecideReviewAction_StaleReviewingRecordIsStillReviewable(t *testing.T) {
    tests := []struct {
        name string
        rec  Record
    }{
        {
            name: "never-reviewed, crashed on first attempt",
            rec: Record{
                Repo:   "owner/repo",
                PR:     1,
                Status: StatusReviewing, // stuck from a crashed first review
                // LastReviewedSHA / LastServicedRequest both empty: never completed
            },
        },
        {
            name: "previously reviewed, crashed reviewing a NEW commit",
            rec: Record{
                Repo:                "owner/repo",
                PR:                  2,
                Status:              StatusReviewing, // stuck from a crashed re-review
                LastReviewedSHA:     "old-sha",
                LastServicedRequest: "old-sha", // request for old-sha was serviced; new-sha's review crashed before completion
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            pr := /* construct a github.PR fake/fixture with HeadSHA() == "new-sha",
                      IsTerminal() == false, IsReviewRequestedFor(reviewer) == true —
                      follow the existing github.PR test-double pattern used elsewhere
                      in decideactions_test.go */
            action := decideReviewAction(pr, tt.rec, "reviewer-handle")
            if action != ActionReview {
                t.Errorf("expected ActionReview for stale StatusReviewing record, got %v", action)
            }
        })
    }
}
```

Follow whatever `github.PR` test-double/fixture pattern
`decideactions_test.go` already uses elsewhere in the file for constructing a
PR with a controllable `HeadSHA()`/`IsTerminal()`/`IsReviewRequestedFor()` —
read a couple of existing test cases in that file first and match the
established fixture style exactly rather than inventing a new one.

**Acceptance criteria:**
- New test explicitly proves `decideReviewAction` ignores `Status` and
  returns `ActionReview` for a stale `StatusReviewing` record in both the
  "never completed a review" and "completed one review, crashed on the next"
  shapes.
- `go test ./internal/review/... -run TestDecideReviewAction_StaleReviewingRecordIsStillReviewable -v` passes.

### Task 5: Full verification pass
**Dependencies:** Tasks 1-4 complete.

- Run the full review package test suite and the TUI package test suite
  (the TUI `StatusReviewing` → Warning coverage already exists and must
  continue to pass unmodified — this is a regression check, not new work).
- Confirm no other test in the repo asserted "record does not exist after a
  RunReview failure" (grep to be safe, since Task 2 changes that invariant
  for `internal/review`).

## Team Orchestration

No parallelization concerns beyond the ordering above: Task 1 is the
prerequisite for Tasks 2 and 3 (same file, same behavior under test). Task 4
is fully independent (different file, tests an existing function's existing
behavior) and can be built in parallel with Task 1. Given the size of this
change (one primary file + two test files, all in `internal/review`), a
single builder handling Tasks 1-3 sequentially plus Task 4 either before or
after is reasonable; splitting further would add coordination overhead
without real parallel-execution benefit.

## Validation Commands

```bash
# Build
go build ./...

# Full review package tests (includes Tasks 1-4's new/modified tests)
go test ./internal/review/... -v

# Confirm existing TUI StatusReviewing-render coverage still passes unmodified
go test ./internal/tui/... -run TestReviewsTab -v

# Race check — RunReview/store.Save concurrent access patterns are unchanged,
# but re-run to confirm this change introduces nothing new
go test ./internal/review/... ./internal/tui/... -race

# Full suite
go test ./...

# Targeted greps used during design/verification (re-run to confirm no drift
# before merging):
grep -n "StatusReviewing" internal/review/*.go internal/tui/*.go
grep -rn "store.Save\|storeImpl.Save" internal/review/runner.go
```

## Acceptance Criteria (from issue, restated against concrete files)

1. When a review starts (either watcher-dispatched via `watcher.go:216`'s
   call into `RunReview`, or manual `review <URL>` via `commands.go:1105-1107`'s
   call into the same `RunReview`), the PR's record is persisted as
   `StatusReviewing` — implemented once, inside `RunReview`
   (`runner.go`), covering both call sites. **(Task 1)**
2. The Reviews tab shows the in-progress PR with `reviewing` status,
   Warning-colored — already implemented and tested
   (`reviews_tab.go:320-321`, `reviews_tab_test.go:126,724,806`); no change
   required, verified as a regression check. **(Task 5)**
3. On successful completion the status transitions to `reviewed` — existing
   behavior (`runner.go:157-163`), now transitioning out of `StatusReviewing`
   instead of out of a stale prior status. **(Task 1, verified by Task 3)**
4. On error it does not remain stuck on `reviewing` — reverts to
   `StatusWatching` on every error-return branch except the final `Save`
   failure (which cannot revert because the write itself failed, and is
   still safely re-reviewable). **(Task 1, verified by Tasks 2 and 3)**
5. A stale `reviewing` record is still picked up as re-reviewable on the next
   poll — proven by `decideReviewAction` never reading `Status`
   (`decideactions.go:17-41`), locked in by a new explicit test.
   **(Task 4)**
6. Tests cover: the start-of-review write (Task 3.1), the completion
   transition (Task 3.4, largely pre-existing), and the interrupted-review
   re-review path (Task 4) — plus the error-revert paths (Tasks 2, 3.2, 3.3)
   which the issue's suggested approach called out as necessary even though
   not separately enumerated in the acceptance criteria list.

## Assumptions

- The issue's phrase "or `done` if merged/closed" is descriptive of the
  overall status lifecycle, not a request to add new `StatusDone`-writing
  logic to `RunReview` — `StatusDone` records are never written today
  (pruning deletes the record via `store.Remove` rather than marking it
  done), and introducing that would be unrelated scope creep. Flagged
  explicitly in "Solution Approach" point 5 above; revisit as a separate
  issue if the user wants a visible terminal `done` row before removal.
- `StatusWatching` (not a new enum value) is the correct revert-on-error
  target, since it is the existing "eligible for next poll, not currently
  reviewing" state and requires no schema/enum change.
