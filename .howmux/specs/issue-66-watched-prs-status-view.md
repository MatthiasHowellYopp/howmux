# Issue #66: PR-review workflow — TUI watched-PRs status view (optional)

Closes #66

## Summary

Add a read-only TUI tab that lists every PR currently tracked in the review
state store (`.howmux/reviews/`, from #59): repo, PR number, status, and
last-reviewed-at. The tab is purely observational — it renders a snapshot of
`review.Store.List()` on demand and performs no writes, no network calls, and
no external process invocations. It sits alongside the existing `Main`,
`Agent`, `Planning`, and `Log` tabs, following the same `Tab` interface and
`TabManager` wiring already used by those.

This is additive and low-risk: no existing tab, command, or state-mutation
path is touched. The only shared dependency is the already-constructed
`review.StoreInterface` (`m.reviewWatcher`'s store is not exported directly,
so the new tab holds its own `review.NewDefaultStore()` reference — see
Concurrency Analysis below for why this is safe).

## Analysis: Existing State Store Data Model (#59)

`internal/review/types.go` defines `review.Record`:

```go
type Record struct {
    Repo                string // "owner/name"
    PR                  int    // Pull request number
    URL                 string // Full PR URL
    Status              Status // watching | reviewing | reviewed | done
    LastReviewedSHA      string // Commit SHA last reviewed
    LastReviewedAt       string // RFC3339 timestamp or empty
    LastServicedRequest string // Head SHA at last serviced review request
    EnrolledAt          string // RFC3339 timestamp
    ReviewDir           string // Path to worktree
    SpoolPath           string // Path to review spool file
}
```

`Status` is a typed string enum: `StatusWatching`, `StatusReviewing`,
`StatusReviewed`, `StatusDone` (`internal/review/types.go`). All four map
directly to a "watched PR" row: `Repo`, `PR`, `Status`, and `LastReviewedAt`
are exactly the four fields the issue asks for. `LastReviewedAt` is `""` for a
PR that hasn't been reviewed yet — this is the primary per-row empty/placeholder
case, distinct from the *no records at all* empty state.

`internal/review/store.go` (`Store`, satisfies `StoreInterface`):

```go
type StoreInterface interface {
    Save(rec Record) error
    Get(repo string, pr int) (Record, bool, error)
    List() ([]Record, error)
    Remove(repo string, pr int) error
}
```

- `List()` reads every `record.json` under the base dir (`.howmux/reviews/` via
  `NewDefaultStore()`) and returns `[]Record{}` (not nil, not an error) if the
  directory doesn't exist yet or contains nothing parseable. This is the
  contract the new view's empty state is built on — no records is a valid,
  expected steady state, not an error condition.
- `List()` performs synchronous disk I/O (`os.ReadDir` + `os.ReadFile` per
  entry) and returns already-decoded `Record` values — no locking, no
  goroutines, no network. It is safe to call directly from the TUI's
  synchronous `View()`/render path.
- The new view only ever calls `Get`/`List`; it must never call `Save` or
  `Remove` — see the read-only constraint below.

## Analysis: Existing TUI Tab/View Architecture

### The `Tab` interface (`internal/tui/tabs.go`)

```go
type Tab interface {
    ID() string
    Type() TabType
    Title() string
    IsClosable() bool
    View() string
    Update(tea.Msg) (Tab, tea.Cmd)
    Resize(width, height int)
    CopyableContent() string
    CaptureFocusState() FocusTarget
    RestoreFocusState(target FocusTarget) tea.Cmd
}
```

`TabType` is a closed enum (`TabTypeMain`, `TabTypeAgent`, `TabTypePlanning`,
`TabTypeLog`) with a `String()` method used by `getTabTypeName` in
`commands.go`'s `handleStatus`. Adding a new tab type (`TabTypeReviews`)
requires updating this enum and its `String()` switch.

### Reference implementations

- **`MainTab`** (`internal/tui/main_tab.go`) — simplest possible `Tab`: holds a
  pre-rendered `baseView` string set externally via `SetBaseView`. Not a good
  template here because our tab computes its own view from data, not from an
  externally-pushed string.
- **`LogTab`** (`internal/tui/log_tab.go`) — closer template. Owns its own data
  source (`*logging.RingBuffer`), refreshes on a `tea.Tick`-driven `TickMsg`,
  renders into a `viewport.Model`, and exposes `CopyableContent()` as an
  unstyled plain-text rebuild of the same rows (see `formatLogEntry` vs the
  plain-text loop in `CopyableContent`). This dual-rendering pattern (styled
  `View()` + plain `CopyableContent()`) is the convention to follow.
- **`AgentTab`** (`internal/tui/agent_tab.go`) — thin wrapper delegating to
  `OutputView`; not relevant to a tabular status display.

### Wiring into `TabManager` (`internal/tui/tui.go`, `newModel`)

```go
tabManager := NewTabManager()
mainTab := NewMainTab()
tabManager.AddTab(mainTab)
...
reviewStore := review.NewDefaultStore()
reviewPollInterval := 5 * time.Minute
if cfg.PollInterval > 0 { reviewPollInterval = cfg.PollInterval }
reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)
```

Note `reviewStore` here is a **local variable** consumed only by
`review.NewWatcher(...)`; it is not stored on `model` and not otherwise
exposed. `AgentTab`s are added later, dynamically, via
`tabManager.AddTab(agentTab)` at line ~1535 when an agent starts. The new tab
should be added once, at `newModel` construction time (like `mainTab`), since
it has no per-instance lifecycle — there is exactly one reviews view for the
whole session, not one per watched PR.

### Render-test pattern (per acceptance criteria)

`internal/tui/tab_manager_test.go` and `agent_tab_test.go` show the pattern:
plain `*_test.go` files in package `tui`, construct the tab directly via its
constructor (`NewMainTab()`, `NewAgentTab(...)`), call `.View()` / `.Title()` /
`.ID()` / `.Type()` / `.IsClosable()` directly, assert on returned strings —
**no bubbletea program driver, no terminal emulation, no I/O**. `footer_test.go`
additionally asserts on line-count invariants of rendered output
(`strings.Split(rendered, "\n")`), which is the applicable pattern here since
our tab renders multiple rows.

Critically, `review.StoreInterface` already exists and is *designed* to be
faked: `internal/review/watcher_test.go` and `store_test.go` construct
in-memory fakes/stub stores rather than touching real disk in unit tests. The
new tab's tests should construct records directly (in Go, as `[]review.Record`
literals) and inject them via a fake `StoreInterface`, never touching
`.howmux/reviews/` on disk and never invoking `gh`, git, or any subprocess.

## Concurrency Analysis

**No new cross-goroutine shared state is introduced.**

- The new tab's `View()` method calls `StoreInterface.List()` synchronously,
  on the render goroutine, exactly like every other tab's `View()` call today
  (`TabManager.RenderCurrentView()` → `activeTab.View()`, called from the main
  bubbletea `Update`/render cycle — single-threaded from the TUI's
  perspective).
- `review.Store` itself has no mutex and does its own O/S-level atomicity via
  temp-file-then-rename (`store.go` `Save`); reads (`Get`, `List`) are plain
  file reads with no shared in-memory state to race on. The TUI's call to
  `List()` does not share a `Store` struct instance with the `review.Watcher`
  goroutine's poll loop (`pollOnce` calls `w.store.List()` on its own
  goroutine) — **each holds/uses independent `*Store` values constructed from
  the same `baseDir`**, and `Store` carries no mutable fields beyond the
  immutable `baseDir` string set at construction. Two goroutines calling
  `List()`/`Save()` concurrently against the same directory are safe by
  construction (atomic rename-based writes, and reads that tolerate a
  torn/missing file via `os.IsNotExist`/skip-on-parse-error) — no new lock is
  required and none should be added.
- The new tab does **not** read `review.Watcher`'s internal fields
  (`activeReviews`, `started`, etc.) — it only talks to the `Store`, so it
  never touches the `sync.RWMutex`-guarded state inside `Watcher`. This is a
  deliberate design boundary: **do not** wire the new tab through
  `model.reviewWatcher` at all; give it its own `review.NewDefaultStore()` (or
  accept an injected `review.StoreInterface`), matching how `newModel`
  already constructs a separate local `reviewStore` for the watcher.
- **Acceptance criterion**: no field with a mutex is read or written by this
  feature; therefore no accessor-locking acceptance criterion or concurrent
  `-race` test is required for this issue. State the reasoning above in the PR
  description so the validator can confirm the "no new shared state" claim
  rather than re-deriving it.

## Design: `ReviewsTab`

### What it reads

A `review.StoreInterface` (not the concrete `*review.Store`), so tests can
inject a fake. Call `store.List()` fresh every time `View()` is invoked — no
caching, no background polling, no `tea.Tick`. This keeps the feature strictly
read-only and trivially correct: the displayed data is always the current
on-disk state at render time, and there is no staleness window to reason
about. (Contrast with `LogTab`, which *does* need a `TickMsg` poll loop
because its source is an in-memory ring buffer being written by a different
goroutine with no on-demand pull API — `review.Store.List()` has no such
constraint, it's a pull-based synchronous API, so a tick loop would be
unjustified complexity.)

### How it renders

Follow the `LogTab` dual-rendering convention:

- `View()` — styled, human-readable table-like layout using
  `m.styles`-equivalent fields available to the tab (pass `*Styles` into the
  constructor, same as `NewLogTab(id, level, bufferSize, styles)` and
  `NewAgentTab(agentID, manager, styles)`). Columns: `REPO`, `PR`, `STATUS`,
  `LAST REVIEWED`. Right-pad/align columns with fixed-width formatting (simple
  `fmt.Sprintf("%-*s", w, s)`-style columns is sufficient — no need for a
  general-purpose table widget; none exists in this codebase, see
  `handleStatus()` in `commands.go` for the established convention of hand-built
  aligned lines via `fmt.Sprintf` rather than a table library).
  - Color the `STATUS` column using existing `Styles` fields:
    `StatusDone` → `styles.Success`, `StatusReviewing` → `styles.Warning`,
    `StatusWatching`/`StatusReviewed` → default/no style (or reuse
    `styles.Prompt` for a neutral highlight — implementer's choice, document
    the mapping in a comment). Do not invent new theme colors; reuse the
    existing `Styles` struct fields already defined in `styles.go`.
  - Format `LastReviewedAt`: parse the RFC3339 string with `time.Parse` and
    render as `"—"` (em dash) when empty, otherwise a short human format (e.g.
    `time.RFC3339` truncated, or `15:04:05` for same-day / date for older —
    keep it simple: rendering the raw RFC3339 string is acceptable for a v1,
    but prefer a short relative/absolute format consistent with
    `timestamp_integration_test.go` / `Timestamp` style usage elsewhere in the
    TUI if time permits). Do not fail or panic on a malformed timestamp — fall
    back to displaying the raw string if `time.Parse` errors (records are
    already `Validate()`-checked before `Save`, but defend against reading
    stale/hand-edited files).
- `CopyableContent()` — same rows, unstyled plain text, one row per line,
  columns separated by whitespace or a simple delimiter — mirroring
  `LogTab.CopyableContent()`'s independent plain-text rebuild.

### Where it's wired in

Add a **new tab type**, not a section grafted onto an existing tab:

1. `internal/tui/tabs.go`: add `TabTypeReviews` to the `TabType` enum and its
   `String()` switch (returns `"Reviews"` or similar — must stay short since
   `RenderTabHeaders` truncates titles at 15 chars).
2. New file `internal/tui/reviews_tab.go`: `ReviewsTab` struct implementing
   `Tab`, constructed via `NewReviewsTab(id string, store review.StoreInterface, styles *Styles) *ReviewsTab`.
3. `internal/tui/tui.go` `newModel`: after `tabManager.AddTab(mainTab)`, add:
   ```go
   reviewsTab := NewReviewsTab("reviews", review.NewDefaultStore(), styles)
   tabManager.AddTab(reviewsTab)
   ```
   Add this as a permanent, non-closable tab (`IsClosable() → false`, same as
   `MainTab`) since — like `Main` — there is exactly one per session and
   nothing to "close" back to. Placing it right after `mainTab` and before any
   dynamically-added agent tabs keeps ordering predictable
   (`Main | Reviews | <agents...> | <planning...> | Logs`).
4. `internal/tui/commands.go` `getTabTypeName` (used by `handleStatus`): add a
   case for `TabTypeReviews` alongside the existing tab-type names, so the
   existing `status` command's tab listing renders the new tab's type
   correctly instead of falling through to `"Unknown"`.

No footer, autocomplete, or command-registry changes are required — the tab is
reachable purely through existing tab navigation (`[`/`]`, tab-header click)
exactly like `Log`/`Main`. Do not add a new slash-command to open it; that
would exceed the issue's read-only-surface scope.

### Focus state

`CaptureFocusState()` / `RestoreFocusState()` should mirror `MainTab`/`LogTab`:
this tab has no internal focusable widget (no text input, no interactive
selection), so return `FocusTargetFooter` and a no-op `RestoreFocusState`,
identical to `MainTab.CaptureFocusState`/`RestoreFocusState`.

### `Update()` behavior

No state to update in response to `tea.Msg` — return `(rt, nil)` unconditionally,
except optionally handling `tea.WindowSizeMsg`-driven resize bookkeeping the
same way `LogTab`/`MainTab` do (store width/height for use by `View()`, if the
render needs to truncate/wrap to terminal width). Do **not** add a `tea.Tick`
poll loop (see "How it renders" above) — the whole point of doing a fresh
`store.List()` call inside `View()` is that no periodic refresh plumbing is
needed; the tab naturally shows current data whenever bubbletea re-renders it
(e.g. after any key press, resize, or other tab's tick), which is sufficient
for a low-frequency observability surface.

## Empty-State Design

Two distinct empty-ish states must be handled:

1. **No watched PRs at all** (`store.List()` returns `[]Record{}` or `nil`):
   render a single centered/left-aligned placeholder line, e.g.
   `"No watched PRs. Run 'howmux review <PR-URL>' to enroll one."` — reuse
   `styles.Prompt` or similar for the message so it's visually consistent with
   other "nothing here yet" messaging patterns in the codebase (check
   `handleStatus()`'s `"  No tabs"` fallback for the established terse-message
   convention). Do not render an empty table with zero rows and no
   explanation — that reads as broken, not empty.
2. **`store.List()` returns an error** (e.g. transient I/O failure — `List()`
   only errors on a `ReadDir` failure other than not-exists): render an error
   line, e.g. `"Failed to read PR review state: <err>"`, rather than panicking
   or silently showing an empty table. This keeps the read-only surface
   resilient without pretending success.
3. **Per-row `LastReviewedAt == ""`**: not a table-level empty state, just a
   `"—"` placeholder in that cell, as covered above.

## Testing Approach

All new tests live in `internal/tui/reviews_tab_test.go`, package `tui`,
following the exact convention in `tab_manager_test.go` /
`agent_tab_test.go` / `footer_test.go`:

1. **Fake store, no disk I/O.** Define a small in-package (or test-file-local)
   fake implementing `review.StoreInterface`:
   ```go
   type fakeReviewStore struct {
       records []review.Record
       listErr error
   }
   func (f *fakeReviewStore) List() ([]review.Record, error) { return f.records, f.listErr }
   func (f *fakeReviewStore) Save(rec review.Record) error { return nil } // unused by ReviewsTab
   func (f *fakeReviewStore) Get(repo string, pr int) (review.Record, bool, error) { return review.Record{}, false, nil }
   func (f *fakeReviewStore) Remove(repo string, pr int) error { return nil }
   ```
   This mirrors the existing `StoreInterface` fakes already used in
   `internal/review/watcher_test.go` — do not introduce a second, differently-
   shaped fake convention; check that file first and match its style/naming if
   it already exports a reusable fake.
2. **Construction + basic contract**: `NewReviewsTab("reviews", fakeStore, styles)`
   then assert `ID() == "reviews"`, `Type() == TabTypeReviews`,
   `IsClosable() == false`, non-empty `Title()`.
3. **Empty state**: fake store with `records: nil` (and separately
   `records: []review.Record{}`) → `View()` contains the placeholder text
   (substring match), does not contain column headers with zero data rows in
   a way that reads as broken (implementer's call on exact wording; test the
   presence of the placeholder string, not exact full-string equality, to
   avoid brittle tests against exact spacing).
4. **Error state**: fake store with `listErr: errors.New("disk fell over")` →
   `View()` contains an error indicator and does not panic.
5. **Populated rendering**: fake store with 2–3 `review.Record` literals
   covering each `Status` value and both an empty and populated
   `LastReviewedAt`, → assert `View()` contains each `Repo`, formatted `PR`
   number, and `Status` string; assert the `"—"` placeholder appears for the
   empty-timestamp record. Use substring assertions
   (`strings.Contains`) rather than full-string golden-file comparisons, matching
   the style of `agent_tab_test.go`.
6. **`CopyableContent()` parity**: assert the plain-text copy output also
   contains the same repo/PR/status data (mirrors the intent of
   `LogTab`'s `CopyableContent` test coverage — check
   `internal/tui/clipboard_test.go` / `copy_feedback_test.go` for the exact
   assertion style used for other tabs' `CopyableContent()` and match it).
7. **Line-count / no-crash on resize**: call `Resize(w, h)` with a few
   width/height combinations and assert `View()` doesn't panic and returns a
   non-empty string — matching the defensive style of `footer_test.go`'s
   line-count assertions (exact line-count assertions are optional here since
   row count is data-dependent, unlike the footer's fixed height).
8. **`TabManager` integration** (optional but recommended): extend
   `tab_manager_test.go`-style coverage with one test that does
   `tm.AddTab(NewReviewsTab(...))` then asserts `tm.GetTabs()` includes it and
   `RemoveTab("reviews")` returns `false` (non-closable), mirroring the
   existing `TestTabManager` main-tab assertions.

**Explicitly forbidden in these tests**: shelling out to `gh`, `git`, or any
subprocess; writing to or reading from `.howmux/reviews/` on real disk;
network calls; sleeping/polling loops; asserting against wall-clock `time.Now()`
without injecting a fixed time (use fixed RFC3339 literal strings in test
`Record`s, not `time.Now().Format(...)`, so tests are deterministic).

## Relevant Files

| File | Change |
|---|---|
| `internal/tui/tabs.go` | Add `TabTypeReviews` to enum + `String()` |
| `internal/tui/reviews_tab.go` | **New.** `ReviewsTab` struct + `Tab` impl + `NewReviewsTab` constructor |
| `internal/tui/reviews_tab_test.go` | **New.** Unit tests per Testing Approach above |
| `internal/tui/tui.go` | In `newModel`, construct and `AddTab` the reviews tab |
| `internal/tui/commands.go` | Add `TabTypeReviews` case to `getTabTypeName` |

No changes to `internal/review/*` (state store, types, watcher) — this issue
is purely a consumer of the existing, already-merged store API. No changes to
`.howmux/config.yaml`, CI workflows, `Taskfile.yml`, or any template-synced
files — this is not a mechanical/rename change and touches no config/CI
surface.

## Team Orchestration / Task Breakdown

Single builder, sequential tasks (no parallelization benefit — all tasks touch
overlapping/dependent code in a small file set, and the whole feature is
small enough that splitting it across builders would add coordination
overhead without shortening wall-clock time).

### Task 1: Add `TabTypeReviews` enum value
**Files**: `internal/tui/tabs.go`
**Acceptance Criteria**:
- `TabTypeReviews` added to the `TabType` const block
- `String()` switch returns a short label (e.g. `"Reviews"`) for it
- Existing tests in the package still compile (enum addition is additive,
  should not break any existing `switch` that doesn't have a `default`)
**Dependencies**: None

### Task 2: Implement `ReviewsTab`
**Files**: `internal/tui/reviews_tab.go` (new)
**Acceptance Criteria**:
- `ReviewsTab` implements the full `Tab` interface (`ID`, `Type`, `Title`,
  `IsClosable` → `false`, `View`, `Update`, `Resize`, `CopyableContent`,
  `CaptureFocusState`, `RestoreFocusState`)
- `NewReviewsTab(id string, store review.StoreInterface, styles *Styles) *ReviewsTab`
  stores the injected `StoreInterface` (not a concrete `*review.Store`) so
  tests can fake it
- `View()` calls `store.List()` fresh on every call — no cached field, no
  background goroutine, no `tea.Tick`
- `View()` handles: (a) `List()` returns empty/nil → placeholder message;
  (b) `List()` returns an error → error message, no panic; (c) `List()`
  returns records → one row per record with `Repo`, `PR`, `Status`,
  formatted `LastReviewedAt` (or `"—"` if empty)
- `CopyableContent()` independently rebuilds the same data as unstyled plain
  text (mirrors `LogTab.CopyableContent()`'s pattern of a parallel plain-text
  builder, not a strip-ANSI-from-View() approach)
- `CaptureFocusState()` returns `FocusTargetFooter`; `RestoreFocusState()` is
  a no-op returning `nil` — matching `MainTab`
- No call to `store.Save()` or `store.Remove()` anywhere in this file —
  **verification**: `grep -n "\.Save(\|\.Remove(" internal/tui/reviews_tab.go`
  returns zero matches
**Dependencies**: Task 1 (needs `TabTypeReviews` to exist)

### Task 3: Wire `ReviewsTab` into `TabManager` at startup
**Files**: `internal/tui/tui.go`
**Acceptance Criteria**:
- In `newModel`, immediately after `tabManager.AddTab(mainTab)`, construct
  `review.NewDefaultStore()` and `NewReviewsTab(...)`, then `tabManager.AddTab(reviewsTab)`
- The reviews tab is added exactly once per session (at construction), not
  re-added on every render or agent-add
- No new field is added to `model` for the reviews tab unless genuinely
  needed elsewhere (unlike `mainTab`, which `model` keeps a reference to for
  `SetBaseView` — `ReviewsTab` has no equivalent external-push need, so a
  local variable passed to `AddTab` is sufficient; do not add
  `model.reviewsTab` unless a concrete later use is identified)
- Existing `reviewWatcher`/`reviewStore` construction for `review.NewWatcher`
  is untouched — the new tab's store is a separate `review.NewDefaultStore()`
  call, not a shared reference (see Concurrency Analysis: this is intentional
  and safe)
**Dependencies**: Task 2

### Task 4: Update `getTabTypeName` for the new tab type
**Files**: `internal/tui/commands.go`
**Acceptance Criteria**:
- `getTabTypeName(TabTypeReviews)` returns a non-empty, non-`"Unknown"` label
  consistent with the tab's `Title()`
- **Verification**: `grep -n "getTabTypeName" internal/tui/commands.go` shows
  the new case added to the existing switch, not a parallel/duplicate function
**Dependencies**: Task 1

### Task 5: Unit tests for `ReviewsTab`
**Files**: `internal/tui/reviews_tab_test.go` (new)
**Acceptance Criteria**:
- All scenarios from the "Testing Approach" section above are covered:
  fake-store construction/contract, empty state (nil and `[]Record{}`),
  error state, populated rendering (covering all four `Status` values and
  both empty/populated `LastReviewedAt`), `CopyableContent()` parity, resize
  no-panic
- Fake `StoreInterface` implementation added in the test file (or reused from
  an existing test-only fake in `internal/review` if one is already exported
  for this purpose — check `internal/review/watcher_test.go` first)
- **Verification — no external processes**: `grep -rn "exec\.\|os/exec\|\"gh \|http\.\|net/http" internal/tui/reviews_tab_test.go`
  returns zero matches
- **Verification — no real disk I/O against the reviews store**:
  `grep -n "\.howmux/reviews\|NewDefaultStore" internal/tui/reviews_tab_test.go`
  returns zero matches (tests only construct the fake, never
  `review.NewDefaultStore()`)
- All tests deterministic: no `time.Now()`-based assertions without a fixed
  reference; no goroutines/sleeps
**Dependencies**: Task 2, Task 3 (test file should compile against final
`ReviewsTab` API; Task 3's wiring doesn't change the tab's public API but
finishing Task 2 first is a hard prerequisite)

### Task 6: Full build + test verification
**Acceptance Criteria**:
- `go build ./...` succeeds
- `go test ./internal/tui/... ./internal/review/...` passes
- `go vet ./...` clean
**Dependencies**: Tasks 1–5

## Validation Commands

```bash
cd /Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-66-15599

# Build
go build ./...

# Targeted tests
go test ./internal/tui/... -run TestReviewsTab -v
go test ./internal/tui/... -v
go test ./internal/review/... -v

# Full test suite
go test ./...

# Vet
go vet ./...

# Confirm no mutation calls in the new tab
grep -n "\.Save(\|\.Remove(" internal/tui/reviews_tab.go   # expect: no output

# Confirm no subprocess/network in the new tests
grep -rn "exec\.\|os/exec\|http\.\|net/http" internal/tui/reviews_tab_test.go   # expect: no output

# Confirm no real store construction in tests
grep -n "NewDefaultStore" internal/tui/reviews_tab_test.go   # expect: no output
```
