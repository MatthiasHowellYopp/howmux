# Design Spec: Add review loop status to footer status bar

Closes #119

## Verified Codebase State

All facts supplied by krew-lead were confirmed by direct inspection of the
worktree before writing this spec:

- `internal/tui/footer.go`: `FooterManager` struct fields are exactly
  `styles, config, watcher (*watcher.Watcher), contextTracker, autocompleteInput,
  tabManager, width, height, transientMessage`. `renderWatcherStatus()` follows
  the nil → "unavailable", `!Running()` → "inactive", else → active-with-details
  pattern. `renderBaseInfo()` is exactly:
  ```go
  func (fm *FooterManager) renderBaseInfo() string {
      watcherStatus := fm.renderWatcherStatus()
      return fmt.Sprintf("%s | theme: %s | Ctrl+Y copy · Ctrl+C quit", watcherStatus, fm.config.Theme)
  }
  ```
- `internal/review/watcher.go`: `Watcher` struct has `store StoreInterface`,
  `pollInterval time.Duration`, `mu sync.RWMutex`, plus the fields listed in the
  issue. `Running()` already exists and correctly RLocks `mu`. `store` and
  `pollInterval` are set once in `NewWatcher` and never mutated after — no lock
  is required to read `pollInterval` for `Interval()` (see Concurrency Analysis
  below for the full reasoning, including why `EnrolledCount()` needs no lock
  either).
- `internal/review/store.go`: `StoreInterface` has `Save`, `Get`, `List()
  ([]Record, error)`, `Remove`.
- `internal/tui/tui.go`: `footerManager := NewFooterManager(styles, cfg, w,
  autocompleteInput, tabManager)` is at line 309. `reviewWatcher :=
  review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)` is at line
  337 (confirmed exact line number and argument list by direct read).
- **No import cycle**: `internal/review` package files import only
  `internal/github`, `internal/logging`, and `internal/notify`. None of the
  10 non-test files under `internal/review/` import `internal/tui`. It is safe
  for `internal/tui/footer.go` to import `internal/review`.
- `internal/tui/footer_test.go` already imports `internal/review` (for an
  unrelated notes-edit test) and constructs `FooterManager` via
  `NewFooterManager(styles, cfg, w, autocomplete, tabManager)` — confirms the
  constructor signature stays untouched.
- `internal/review/watcher_test.go` already defines a `fakeStore` type
  (`records []Record`, mutex-guarded) that implements `StoreInterface`,
  including `List() ([]Record, error)`. This fake is directly reusable for the
  new `Interval()`/`EnrolledCount()` tests in `watcher_test.go` — no new test
  double needed.
- Pluralization precedent exists elsewhere (`footer.go`'s `renderPlanningStatusInfo`
  for "msg"/"msgs", `tui.go`'s copy-status for "line"/"lines"), but nothing
  currently pluralizes "enrolled". Per the issue's explicit instruction, this
  design does **not** add conditional pluralization for "enrolled" — it is
  always the literal string `"enrolled"` regardless of count, matching AC2's
  one given example exactly and avoiding scope creep beyond the letter of the
  acceptance criteria.

## Solution Approach

Mirror the existing watcher-status pattern end to end:

1. Add two read-only accessor methods to `review.Watcher` — `Interval()` and
   `EnrolledCount()` — so the footer never reaches into `review.Watcher`'s
   internals (`pollInterval`, `store`) directly. This keeps the store
   dependency confined to the `review` package per the issue's constraint.
2. Add a `reviewWatcher *review.Watcher` field and a `SetReviewWatcher`
   setter to `FooterManager`. The setter is used instead of a constructor
   parameter so the existing `NewFooterManager` signature and its call site at
   tui.go:309 are untouched — `review.Watcher` is constructed later (tui.go:337)
   than `FooterManager` (tui.go:309), so a setter is the only way to wire it
   without reordering construction (which the issue explicitly prohibits).
3. Add `renderReviewStatus()` to `footer.go`, structurally identical to
   `renderWatcherStatus()` (nil check → inactive check → active format).
4. Update `renderBaseInfo()` to insert the review segment between `theme` and
   the hotkey hint, per AC1's literal ordering.
5. Wire `footerManager.SetReviewWatcher(reviewWatcher)` immediately after the
   `reviewWatcher := review.NewWatcher(...)` line in tui.go, leaving every
   other line in that block (including the `NewFooterManager` call) unchanged.

### Concurrency Analysis

This change reads state from `review.Watcher` (a background-goroutine-owned
type) from the TUI render path (`FooterManager.renderReviewStatus()`, called
synchronously from `renderBaseInfo()` during `RenderFooter()`). This crosses
the "TUI render loop reads watcher state" boundary called out in the
concurrency rules, so it needs explicit treatment — same boundary the existing
`renderWatcherStatus()` → `watcher.Running()` call already crosses.

Per-field locking analysis for the two new accessors:

- **`Running()`** (already exists, unchanged): reads `w.started` under
  `w.mu.RLock()`. `renderReviewStatus()` must continue to call this guarded
  accessor — never read `w.started` directly.
- **`Interval()`** (new): reads `w.pollInterval`. `pollInterval` is set exactly
  once, in `NewWatcher`, before the `Watcher` is ever handed to another
  goroutine (`Start()` is called after construction and only then spawns
  `pollLoop`), and is never written again anywhere in `watcher.go`. It is
  therefore safe to read without holding `mu` — there is no concurrent writer
  to race against. This mirrors `reviewer` and `maxConcurrent`, which are also
  read unguarded elsewhere (e.g. in `dispatch`'s `w.maxConcurrent` comparison
  is inside the `mu` critical section only incidentally, alongside
  `activeReviews`, not because `maxConcurrent` itself is mutable).
  `Interval()` does not need a lock, but for defensive consistency with the
  existing `Running()` accessor pattern (every public accessor on `Watcher`
  takes the lock), **this design still takes `w.mu.RLock()`** in `Interval()`
  — cheap, uncontended, and removes any future doubt for a reader who doesn't
  re-derive the "set-once" argument above.
- **`EnrolledCount()`** (new): calls `w.store.List()`. `store` itself is
  set once in `NewWatcher` and never reassigned, so reading the `w.store`
  field itself needs no lock by the same set-once argument. `List()` is a
  method on `StoreInterface`/`*Store`, which does its own file-based I/O
  (see `store.go`) and is not guarded by `Watcher.mu` — `Watcher.mu` protects
  `started` and `activeReviews`, not the store. `EnrolledCount()` therefore
  does not need to hold `w.mu` to call `w.store.List()`; the store's own
  concurrency safety (file-based reads, already used concurrently by
  `pollOnce` and the read-only Reviews tab per issue #66) applies unchanged.

**Required acceptance criteria arising from this analysis** (folded into the
task breakdown below):
- `Interval()` acquires `w.mu.RLock()` before reading `w.pollInterval`, for
  consistency with `Running()`, even though `pollInterval` is set-once.
- `EnrolledCount()` does not acquire `w.mu` (no shared mutable field it
  protects is touched) but must never access `w.store` from outside the
  `review` package — the footer/TUI code only ever calls
  `reviewWatcher.EnrolledCount()`.
- A concurrent test (`TestWatcher_Interval_EnrolledCount_ConcurrentWithPoll` or
  similar) exercises `Interval()` and `EnrolledCount()` concurrently with
  `Start()`/poll activity so `go test -race` can observe the access pattern,
  matching the existing style of concurrency tests already present in
  `watcher_test.go`.

## Exact String Formats

| State | Format | Example |
|---|---|---|
| No review watcher set (`reviewWatcher == nil`) | `review: unavailable` | `review: unavailable` |
| Review watcher set, not running | `review: inactive` | `review: inactive` |
| Review watcher set, running | `review: active (<interval>, <N> enrolled)` | `review: active (5m0s, 5 enrolled)` |

Notes on the active format:
- `<interval>` is `fm.reviewWatcher.Interval().String()` — Go's
  `time.Duration.String()` output (e.g. `5m0s` for 5 minutes, matching how
  `renderWatcherStatus()` already formats `fm.config.PollInterval.String()`
  for the existing `watcher: active (...)` segment). The issue's example
  `review: active (5m, 5 enrolled)` uses the shorthand `5m`, but
  `time.Duration.String()` actually renders `5m0s` for an exact 5-minute
  duration — this design intentionally uses the real `.String()` output
  (`5m0s`) rather than hand-rolling truncation logic, for consistency with the
  existing `watcher:` segment's own interval formatting (which has the same
  characteristic and is not questioned by the issue). This is a deliberate,
  noted deviation from the issue's literal example string, made to avoid
  introducing bespoke duration-formatting code that nothing else in the
  codebase has. If exact `5m` (no `0s`) formatting is desired later, that is a
  separate concern that would also need to be applied to `renderWatcherStatus()`
  for consistency — out of scope here.
- `<N>` is `fm.reviewWatcher.EnrolledCount()`, an `int`, formatted with `%d`.
- The literal word `enrolled` is never pluralized/singularized — always
  `"enrolled"` regardless of `N` (see Verified Codebase State above for why).

## renderBaseInfo() New Format

Before:
```go
return fmt.Sprintf("%s | theme: %s | Ctrl+Y copy · Ctrl+C quit", watcherStatus, fm.config.Theme)
```

After:
```go
return fmt.Sprintf("%s | theme: %s | %s | Ctrl+Y copy · Ctrl+C quit", watcherStatus, fm.config.Theme, reviewStatus)
```

Full example rendered strings:
- `watcher: active (owner/repo, 5m0s) | theme: default | review: active (5m0s, 5 enrolled) | Ctrl+Y copy · Ctrl+C quit`
- `watcher: inactive | theme: default | review: unavailable | Ctrl+Y copy · Ctrl+C quit`

## Exact Function Signatures

### `internal/review/watcher.go`

```go
// Interval returns the configured poll interval.
func (w *Watcher) Interval() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.pollInterval
}

// EnrolledCount returns the number of enrolled PR records currently tracked
// by the watcher's store. Returns 0 if the store cannot be listed — this is a
// display-only accessor, so it degrades to 0 rather than propagating an error
// or panicking (mirrors the tolerant error handling already used elsewhere in
// this file, e.g. pollOnce's logging.Error + continue pattern, minus the log
// call since Interval/EnrolledCount are not given a logger context by the
// issue and this is a lightweight, frequently-called render-path accessor).
func (w *Watcher) EnrolledCount() int {
	records, err := w.store.List()
	if err != nil {
		return 0
	}
	return len(records)
}
```

Placement: add both methods immediately after the existing `Running()` method
in `internal/review/watcher.go`, before `pollLoop()`. No changes to any
existing method in this file.

### `internal/tui/footer.go`

```go
// FooterManager manages the two-row footer display system
type FooterManager struct {
	styles            *Styles
	config            *config.Config
	watcher           *watcher.Watcher
	reviewWatcher     *review.Watcher // NEW
	contextTracker    *ContextTracker
	autocompleteInput *AutocompleteInput
	tabManager        *TabManager
	width             int
	height            int

	transientMessage string
}
```

```go
// SetReviewWatcher sets the review loop watcher reference used to render the
// "review: ..." footer segment. Call this after constructing the
// review.Watcher (NewFooterManager's signature and call site are unchanged;
// this setter exists because review.Watcher is constructed after
// FooterManager in tui.go).
func (fm *FooterManager) SetReviewWatcher(rw *review.Watcher) {
	fm.reviewWatcher = rw
}
```

```go
// renderReviewStatus formats the PR review loop status for display, mirroring
// renderWatcherStatus's nil → inactive → active progression.
func (fm *FooterManager) renderReviewStatus() string {
	if fm.reviewWatcher == nil {
		return "review: unavailable"
	}

	if !fm.reviewWatcher.Running() {
		return "review: inactive"
	}

	interval := fm.reviewWatcher.Interval().String()
	count := fm.reviewWatcher.EnrolledCount()

	return fmt.Sprintf("review: active (%s, %d enrolled)", interval, count)
}
```

```go
// renderBaseInfo renders the base information shown on all tabs
func (fm *FooterManager) renderBaseInfo() string {
	watcherStatus := fm.renderWatcherStatus()
	reviewStatus := fm.renderReviewStatus()
	return fmt.Sprintf("%s | theme: %s | %s | Ctrl+Y copy · Ctrl+C quit", watcherStatus, fm.config.Theme, reviewStatus)
}
```

New import required in `internal/tui/footer.go`:
```go
"github.com/matthiashowellyopp/howmux/internal/review"
```

`NewFooterManager`'s signature, body, and every existing method other than
`renderBaseInfo` are unchanged. `renderWatcherStatus()` is unchanged.

### `internal/tui/tui.go`

No signature changes. Insert exactly one line immediately after the existing
`reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)`
line (line 337), before the blank line / `initialActivity` block that follows:

```go
reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)
footerManager.SetReviewWatcher(reviewWatcher) // NEW — wires footer to the review watcher built above
```

The `NewFooterManager` call at line 309 and every line between 309 and 337 are
unchanged.

## File-by-File Change List

| File | Change |
|---|---|
| `internal/review/watcher.go` | Add `Interval() time.Duration` and `EnrolledCount() int` methods after `Running()`. No other changes. |
| `internal/review/watcher_test.go` | Add tests for `Interval()` and `EnrolledCount()` (success, empty store, `List()` error → 0), plus one concurrent test exercising both accessors alongside `Start()`/poll activity. Reuse the existing `fakeStore` type. |
| `internal/tui/footer.go` | Add `internal/review` import. Add `reviewWatcher *review.Watcher` field to `FooterManager`. Add `SetReviewWatcher` setter. Add `renderReviewStatus()` method. Update `renderBaseInfo()` to include the review segment per AC1's ordering. No changes to `NewFooterManager`, `renderWatcherStatus`, or any other method. |
| `internal/tui/footer_test.go` | Add tests covering all four review-segment states (unavailable/nil, inactive, active with N=0, active with N>0) via `renderBaseInfo()` or a direct call to `renderReviewStatus()`, following this file's existing conventions (table-driven where the file already uses that style, direct construction of `FooterManager` + a `review.Watcher` backed by a fake/real store otherwise). Existing tests (`TestFooterRendersExactly3Lines`, etc.) must continue to pass unmodified — they construct `FooterManager` without calling `SetReviewWatcher`, exercising the "unavailable" path implicitly, which is correct default behavior. |
| `internal/tui/tui.go` | Insert `footerManager.SetReviewWatcher(reviewWatcher)` immediately after the `reviewWatcher := review.NewWatcher(...)` line (line 337). No other changes in this file. |

No other files require changes. No config, CI, docs, or template-synced files
are affected by this change — it is a pure code addition inside `internal/`,
not a mechanical rename/move, so the mechanical-change-surface enumeration
requirement does not apply here.

## Team Orchestration

This is a small, well-scoped change confined to two packages (`internal/review`
and `internal/tui`) with a clear dependency order. Two tasks can run in
parallel; a third depends on both.

## Task Breakdown

### Task 1: Add `Interval()` and `EnrolledCount()` to `review.Watcher`
**Files**: `internal/review/watcher.go`, `internal/review/watcher_test.go`
**Dependencies**: None (can run in parallel with Task 2)

**Acceptance Criteria**:
1. `Interval() time.Duration` added to `internal/review/watcher.go`, placed
   after `Running()`. Acquires `w.mu.RLock()` before reading `w.pollInterval`
   and releases it via `defer w.mu.RUnlock()` — all access to the shared
   `Watcher` type continues to go through lock-guarded accessors for
   consistency with `Running()`, even though `pollInterval` is set once at
   construction and never mutated (documented rationale in this spec's
   Concurrency Analysis section; no test asserts the lock is unnecessary —
   the test only asserts correct behavior).
2. `EnrolledCount() int` added to `internal/review/watcher.go`, placed after
   `Interval()`. Calls `w.store.List()`; returns `len(records)` on success,
   `0` if `List()` returns an error. Does not panic. Does not access `w.mu`
   (no shared mutable field is read here — see Concurrency Analysis).
3. New unit tests in `watcher_test.go` (reusing the existing `fakeStore` test
   double, which already implements `StoreInterface`):
   - `Interval()` returns the exact `pollInterval` passed to `NewWatcher`.
   - `EnrolledCount()` returns `0` for an empty store.
   - `EnrolledCount()` returns the correct count for a store with N records.
   - `EnrolledCount()` returns `0` when the store's `List()` returns an error
     (add a small error-returning store fake, or extend `fakeStore` with an
     injectable error, following this test file's existing conventions for
     injecting failures — e.g. the pattern used for `dispatchReviewFunc`/
     `removeRecordFunc` package-level function vars, or a dedicated small
     fake struct like `saveSeqStore`/`failingSaveStore` in `runner_test.go`).
   - A concurrent test that calls `Interval()` and `EnrolledCount()`
     repeatedly from one or more goroutines while `Start()`/`pollLoop` is
     running (or at minimum while the store is being concurrently read/written
     by another goroutine), runnable under `go test -race` without failures.
4. All existing tests in `internal/review/` continue to pass:
   `go test ./internal/review/...`
5. `go test -race ./internal/review/...` passes, including the new concurrent
   test.

**Validation Commands**:
```bash
go test ./internal/review/... -run 'TestWatcher' -v
go test -race ./internal/review/...
```

---

### Task 2: Verify no import cycle; confirm package boundary
**Files**: none (verification-only; informs Tasks 3–4)
**Dependencies**: None (can run in parallel with Task 1)

**Acceptance Criteria**:
1. Confirm (already verified in this spec) that no file under
   `internal/review/*.go` imports `internal/tui`:
   `grep -rn "internal/tui" internal/review/*.go` returns no matches.
2. This confirms `internal/tui` may safely import `internal/review` in Task 3
   without introducing a cycle. No code change results from this task; it is
   a documented pre-flight check the builder re-runs before starting Task 3
   to catch any drift since this spec was written.

**Validation Commands**:
```bash
grep -rn "internal/tui" internal/review/*.go || echo "no cycle risk confirmed"
```

---

### Task 3: Wire `FooterManager` to the review watcher and render the segment
**Files**: `internal/tui/footer.go`, `internal/tui/footer_test.go`
**Dependencies**: Task 1 (needs `Interval()`/`EnrolledCount()` to exist),
Task 2 (import-cycle confirmation)

**Acceptance Criteria**:
1. `internal/tui/footer.go` imports `github.com/matthiashowellyopp/howmux/internal/review`.
2. `FooterManager` struct gains a `reviewWatcher *review.Watcher` field. No
   other struct fields change. `NewFooterManager`'s signature, parameters, and
   body are unchanged — the field is left as its zero value (`nil`) by the
   constructor.
3. `SetReviewWatcher(rw *review.Watcher)` method added to `FooterManager`,
   setting `fm.reviewWatcher = rw`.
4. `renderReviewStatus() string` method added, mirroring
   `renderWatcherStatus()`'s structure exactly:
   - `fm.reviewWatcher == nil` → returns `"review: unavailable"`
   - `!fm.reviewWatcher.Running()` → returns `"review: inactive"`
   - otherwise → returns `fmt.Sprintf("review: active (%s, %d enrolled)", fm.reviewWatcher.Interval().String(), fm.reviewWatcher.EnrolledCount())`
   - No pluralization logic on "enrolled".
5. `renderBaseInfo()` updated to:
   `fmt.Sprintf("%s | theme: %s | %s | Ctrl+Y copy · Ctrl+C quit", watcherStatus, fm.config.Theme, reviewStatus)`
   where `reviewStatus := fm.renderReviewStatus()`. Segment order is exactly
   `watcher: ... | theme: ... | review: ... | Ctrl+Y copy · Ctrl+C quit` per AC1.
6. `renderWatcherStatus()` itself is byte-for-byte unchanged — its format
   (`watcher: active (%s, %s)` / `watcher: inactive` / `watcher: unavailable`)
   is not touched.
7. New tests in `footer_test.go` cover all four states:
   - No `SetReviewWatcher` call (field stays nil, default from
     `NewFooterManager`) → `renderBaseInfo()` / `renderReviewStatus()` contains
     `"review: unavailable"`.
   - `SetReviewWatcher` called with a `review.Watcher` that has not had
     `Start()` called → contains `"review: inactive"`.
   - `SetReviewWatcher` called with a started `review.Watcher` backed by a
     store with 0 records → contains `"review: active (<interval>, 0 enrolled)"`.
   - `SetReviewWatcher` called with a started `review.Watcher` backed by a
     store with N>0 records → contains `"review: active (<interval>, N enrolled)"`
     with the correct N and no pluralization of "enrolled".
   - Tests follow this file's existing conventions (direct `FooterManager`
     construction via `NewFooterManager`, then act/assert), and use
     `review.NewWatcher(...)` with an in-package-visible store — since
     `footer_test.go` is in package `tui`, it needs a `review.StoreInterface`
     implementation; use `review.NewStore(t.TempDir())` (already exported,
     zero external dependencies per its doc comment) rather than reaching for
     `review`'s test-only `fakeStore` (which is unexported to package
     `review` and unavailable from package `tui`). Call `store.Save(...)` for
     N-record cases.
   - Started watchers created in tests must be stopped (`defer rw.Stop()` or
     equivalent) to avoid leaking the poll-loop goroutine across tests.
8. Existing tests `TestFooterRendersExactly3Lines`,
   `TestFooterDropdownRendersExactly3LinesWithoutDropdown`, and
   `TestNotesEditMode_SetsAndClearsFooterTransientMessage` continue to pass
   unmodified — none of them call `SetReviewWatcher`, so they exercise the
   `nil` → `"review: unavailable"` path, which does not change footer line
   count (still 3 lines) since it's appended within the existing status row.
9. `go vet ./internal/tui/...` and `go build ./...` succeed.

**Validation Commands**:
```bash
go test ./internal/tui/... -run 'TestFooter' -v
go build ./...
go vet ./internal/tui/...
```

---

### Task 4: Wire `tui.go` construction to call the new setter
**Files**: `internal/tui/tui.go`
**Dependencies**: Task 3 (needs `SetReviewWatcher` to exist)

**Acceptance Criteria**:
1. Exactly one line added: `footerManager.SetReviewWatcher(reviewWatcher)`,
   placed immediately after the existing
   `reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)`
   line.
2. The `footerManager := NewFooterManager(styles, cfg, w, autocompleteInput, tabManager)`
   call and every line between it and the `reviewWatcher := review.NewWatcher(...)`
   line are byte-for-byte unchanged — no reordering.
3. No other line in `tui.go` changes.
4. Full build succeeds and the TUI starts without panicking (manual smoke
   check acceptable if no existing integration test drives `NewModel`/`Init`
   end-to-end; if one exists, it must continue to pass).
5. `go build ./...` succeeds.

**Validation Commands**:
```bash
go build ./...
go test ./internal/tui/... -v
```

---

## Validation Commands (Full Suite)

Run after all four tasks are complete, in this order:

```bash
go build ./...
go vet ./...
go test ./internal/review/... -v
go test -race ./internal/review/...
go test ./internal/tui/... -v
go test ./...
```

All commands must succeed with no failures and no new `go vet` warnings. The
full `go test ./...` run confirms no other package (e.g. `cmd/howmux`) breaks
from the `FooterManager` struct/import changes.

## Constraints Recap (do not violate)

- Do not change `renderWatcherStatus()`'s output format.
- Do not change `NewFooterManager`'s signature or its call site's argument
  list/position (tui.go line 309).
- `EnrolledCount()` is the only path by which the footer/TUI layer learns the
  enrolled-PR count — no direct `store.List()` call is added anywhere in
  `internal/tui`.
- No pluralization logic on the word "enrolled".
