# Design Spec: Row Selection Cursor for Reviews Tab

Closes #83

## Problem Statement

`ReviewsTab` (`internal/tui/reviews_tab.go`) renders PR review rows on every
`View()` call with no notion of a "current" row. There is no way for a user
to indicate which row they want to act on (open / edit / set decision), which
blocks the PR-review gate workflow. This spec adds a persistent cursor
(`selectedIndex`) with up/down navigation, bounds checking, and visual
highlighting reusing `Styles.AutocompleteSelected` from `autocomplete.go`.

This spec only adds the cursor and highlight. It does not add any "act on
selected row" command/keybinding — that is out of scope per the acceptance
criteria (issue #83 is scoped to selection, not actions).

## Solution Approach

### Why `up`/`down` reach `ReviewsTab.Update()` today

Global key routing lives in `internal/tui/tui.go`'s `case tea.KeyPressMsg`
handler (around line ~780). The `"up", "down", "pgup", "pgdown", "home",
"end"` case is special-cased for two tab types:

1. `TabTypeMain` — routes to `m.consoleViewport` scrolling, returns early.
2. `TabTypePlanning` with footer focused and matched suggestions — routes to
   `m.input`, returns early.

Every other tab type (including `TabTypeReviews`) falls through to:

```go
navCmd := m.tabManager.Update(msg)
```

`TabManager.Update` (`internal/tui/tab_manager.go:89`) forwards the raw
`tea.Msg` unconditionally to `activeTab.Update(msg)`. This means
**`ReviewsTab.Update()` already receives `up`/`down` key messages today** —
they're simply dropped because the current `Update()` implementation ignores
all messages (`return rt, nil`). No changes to `tui.go` or `tab_manager.go`
are required. All new logic is contained in `reviews_tab.go`.

### Why there is no key-binding conflict

- `up` / `down` bare arrow keys are not bound to any *global* action (no
  `case "up":`/`case "down":` exists outside the tab-type-specific branch
  already described).
- Within that branch, the Main-tab and Planning-tab special cases both
  `return` before reaching `m.tabManager.Update(msg)`, so they never see
  Reviews-tab messages and vice versa — the branches are mutually exclusive
  by `activeTab.Type()`.
- `j`/`k` (vi-style aliases) are used only by `LogTab` (`log_tab.go:165`) as
  additional bindings for its own viewport, handled entirely inside
  `LogTab.Update()`. They are not global and do not reach `ReviewsTab`.
- No existing global hotkey uses bare `up`/`down` (checked `hotkey.HandleKeyMsg`
  call site and the full `switch msg.String()` block in `tui.go`; the other
  cases are `f2`, `[`, `]`, `ctrl+w`, `tab`, `shift+tab`, `ctrl+c`, `ctrl+y`,
  `esc`, digit keys under the status overlay).

Conclusion: implement cursor movement entirely inside
`ReviewsTab.Update(msg tea.Msg) (Tab, tea.Cmd)`, matching `up`/`down` on
`tea.KeyMsg.String()`. No routing changes needed upstream.

### State field

Add one field to `ReviewsTab`:

```go
type ReviewsTab struct {
	id            string
	store         review.StoreInterface
	styles        *Styles
	width         int
	height        int
	selectedIndex int // index into the sorted, rendered row slice; -1 when no rows exist
}
```

- `selectedIndex` is a plain `int`, not a pointer and not reset in
  `NewReviewsTab` beyond Go's zero value (`0`), which already satisfies
  acceptance criterion 6 ("default cursor to first row on load") for the
  normal case. See "Initial state and empty table" below for the `-1`
  sentinel needed for the empty case.
- No mutex is needed. `selectedIndex` is only ever read/written from the
  Bubble Tea event-loop goroutine (`Update()` calls and `View()` reads both
  happen synchronously on that single goroutine, per the existing
  no-background-polling design documented in `ReviewsTab.View()`'s comment).
  This tab has no background goroutine, ticker, or watcher writing to it —
  confirmed by `Update()`'s existing doc comment ("There is no internal state
  to update... every View() call reads fresh data directly from the store").
  **No concurrency concern applies to this change** (see Concurrency Analysis
  below for the explicit call-out required by process).

### Key bindings

Handle exactly two key strings inside `ReviewsTab.Update()`, matching the
issue's acceptance criteria (arrow keys only — do not add `j`/`k` aliases,
since the issue says "up/down arrow keys" and adding extra bindings is not
requested; keep the surface minimal per the "no new UI primitives" constraint):

| Key string | Action |
|---|---|
| `"up"` | `selectedIndex--`, clamp to `0` |
| `"down"` | `selectedIndex++`, clamp to `len(rows)-1` |

Any other `tea.KeyMsg`, and any non-`tea.KeyMsg` message, falls through to
the existing `return rt, nil` behavior (no-op), preserving current behavior
for all other input.

### Row count source for bounds checking

`Update()` does not have direct access to the rendered row count today —
`View()` computes it locally from `rt.store.List()`. To bounds-check cursor
movement, `Update()` must independently call `rt.store.List()` (mirroring the
same pattern `View()` and `CopyableContent()` already use — see the "Cost
note" comment on `View()`, which explicitly accepts a fresh store read per
render/interaction as the deliberate no-cache tradeoff for this tab). This
keeps `Update()` self-contained and consistent with the tab's existing
architecture rather than introducing a cached row-count field that could
drift from what `View()` actually renders.

Add a small helper reused by both `Update()` and `renderTable()`:

```go
// rowCount returns the number of rows that will be rendered for the current
// store state, ignoring List() errors (an error means renderError() is shown
// and there are no selectable rows). Used by Update() to bounds-check cursor
// movement without duplicating the err/empty branching in View().
func (rt *ReviewsTab) rowCount() int {
	records, err := rt.store.List()
	if err != nil {
		return 0
	}
	return len(records)
}
```

### Bounds-checking logic

```go
func (rt *ReviewsTab) moveCursor(delta int) {
	n := rt.rowCount()
	if n == 0 {
		rt.selectedIndex = -1
		return
	}
	next := rt.selectedIndex + delta
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	rt.selectedIndex = next
}
```

Called from `Update()` as `rt.moveCursor(-1)` for `"up"` and
`rt.moveCursor(1)` for `"down"`.

This also self-heals the case where the row count shrinks between renders
(e.g. a PR is un-enrolled and the store now returns fewer records than
`selectedIndex` pointed at) — the next `"up"`/`"down"` keypress will clamp
back into range. `View()` additionally defends against a stale
out-of-range index directly (see below) so a shrink is handled even without
a keypress first.

### Initial state and empty-table handling

- **Initial state**: `selectedIndex` zero-value (`0`) is correct for the
  normal "has rows" case — satisfies acceptance criterion 6 with no
  constructor change needed.
- **Empty table**: when `rt.store.List()` returns zero records,
  `selectedIndex` must be treated as disabled. Rather than special-casing
  "0 but disabled" vs "0 and valid", explicitly set `selectedIndex = -1`
  whenever `rowCount() == 0` is observed, and treat `-1` (or any value
  outside `[0, n-1]`) as "no selection" in `renderTable()`. This is
  belt-and-suspenders with the clamping in `moveCursor`: `moveCursor` already
  sets `-1` when `n == 0`, and `renderTable` independently guards against
  rendering a highlight when `n == 0` or when `selectedIndex` is out of
  `[0, n-1]` range (covers the case where the table transitions from
  non-empty to empty between renders without a `moveCursor` call in
  between — e.g. the last tracked PR is removed from the store on disk while
  the Reviews tab is simply being viewed, not navigated).
- No keypress should panic or behave oddly when rows are empty: `"up"`/`"down"`
  with zero rows call `moveCursor`, which sets `selectedIndex = -1` and
  returns — safe no-op.

### Cursor persistence across focus changes

Per the `CaptureFocusState`/`RestoreFocusState` investigation: these methods
only track **footer vs. message input focus** (`FocusTargetFooter` /
`FocusTargetMessage`), used by `model.switchActiveTab` in `tui.go` to restore
which input box has keyboard focus when switching tabs. `ReviewsTab` already
returns `FocusTargetFooter` unconditionally from `CaptureFocusState()` and
does nothing in `RestoreFocusState()` — this is unrelated to row selection
and requires **no changes**.

Row-cursor persistence is automatic and requires no explicit save/restore
logic: `TabManager` holds the *same* `*ReviewsTab` pointer/value in its
`tabs` slice across tab switches (confirmed in `tab_manager.go` — tabs are
stored once and only swapped via `Update`'s `tm.tabs[tm.activeTab] = updated`
after each message). Switching away from and back to the Reviews tab does
not call `NewReviewsTab` again and does not reset `selectedIndex`. This
satisfies acceptance criterion 5 with no additional code — call this out
explicitly as a **verification-only** task for the builder (write a test,
don't write new persistence logic).

### Visual highlighting — reuse pattern from `autocomplete.go`

`autocomplete.go`'s `RenderSuggestionsMenu()` establishes the pattern to
copy:

```go
selectedStyle := a.styles.AutocompleteSelected.Padding(0, 1)
defaultStyle := lipgloss.NewStyle().Padding(0, 1)
...
if actualIdx == currentIndex {
    style = selectedStyle
} else {
    style = defaultStyle
}
menuItems = append(menuItems, style.Render(suggestions[actualIdx]))
```

`Styles.AutocompleteSelected` (defined in `styles.go`) is:

```go
AutocompleteSelected: lipgloss.NewStyle().
    Background(lipgloss.Color(theme.Colors.Primary)).
    Foreground(lipgloss.Color(theme.Colors.Surface)),
```

Reuse this exact style field — do not add a new theme color or new `Styles`
field (satisfies acceptance criterion 8 and the "no new UI primitives"
constraint). Apply it to the **whole rendered row string** (not just one
column) so the highlight reads as a full-row selection band, matching how a
table row selection typically looks and how `AutocompleteSelected` is already
applied to a full menu-item line in `autocomplete.go`.

Concretely, in `buildTable` — which is shared by both `renderTable()` (styled)
and `CopyableContent()` (plain-text) — the per-row styling decision must be
threaded through **without changing `CopyableContent()`'s output** (plain
text copy should never contain highlight styling; copying selection state to
the clipboard is not part of this issue and would violate `CopyableContent()`'s
contract of returning unstyled text matching the display *structure*, not the
transient interactive state).

Do this by adding a row-style parameter to `buildTable` rather than reaching
into `rt.selectedIndex` from within `buildTable` itself:

```go
// rowStyle returns a per-row wrapping style function: identity for
// CopyableContent() (never highlights), or a highlight-aware closure bound
// to rt.selectedIndex for renderTable(). Signature: func(rowIndex int, line string) string.
func buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(review.Status, string) string, rowStyle func(int, string) string) string {
	...
	for i, rec := range records {
		b.WriteString("\n")
		... // build row string `line` as today
		b.WriteString(rowStyle(i, line))
	}
	...
}
```

- `renderTable()` passes a closure:
  ```go
  rowStyle := func(i int, line string) string {
      if rt.styles == nil || i != rt.selectedIndex {
          return line
      }
      return rt.styles.AutocompleteSelected.Render(line)
  }
  ```
- `CopyableContent()` passes `func(_ int, line string) string { return line }`
  (identity — no behavior change to existing copy output).

This is a **minimal, additive change to `buildTable`'s signature** — both
existing call sites (`renderTable`, `CopyableContent`) must be updated to
pass the new parameter. No other structural change to `buildTable` is
needed; the header line is unaffected (header is never highlighted; only
data rows participate in row styling).

Note on `AutocompleteSelected` and padding: `autocomplete.go` applies
`.Padding(0, 1)` when constructing `selectedStyle`, but that padding is
specific to the dropdown menu's box layout. The Reviews table already
right-pads every column via `fmt.Sprintf("%-*s", width, ...)`, so no
additional `.Padding()` call is needed on the reused style here — applying
`rt.styles.AutocompleteSelected.Render(line)` directly is sufficient and
keeps column alignment intact (adding horizontal padding would shift columns
out of alignment with the unstyled header).

### `View()` and `renderTable()` changes

`renderTable()` needs no change to its signature (still `(rt *ReviewsTab) renderTable(records []review.Record) string`) — it internally builds the `rowStyle` closure described above and passes it to `buildTable`. `View()` itself needs no change; it already calls `rt.renderTable(records)` and `rt.renderEmpty()`/`rt.renderError()` for the other two branches (empty/error branches have no rows, so no highlight is drawn — consistent with the empty-table requirement).

### `Update()` implementation

```go
// Update handles messages for the reviews tab. Arrow-key navigation moves
// the row cursor (selectedIndex); all other messages are no-ops, matching
// the tab's existing "no background state to update" design — resize is
// handled via Resize, and every View() call reads fresh data directly from
// the store.
func (rt *ReviewsTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return rt, nil
	}
	switch keyMsg.String() {
	case "up":
		rt.moveCursor(-1)
	case "down":
		rt.moveCursor(1)
	}
	return rt, nil
}
```

Confirm the concrete key message type: `tui.go`'s routing switch operates on
`tea.KeyPressMsg` (see the `case tea.KeyPressMsg:` block), while
`ReviewsTab.Update`'s existing signature types the parameter as `tea.Msg`
and other tabs (e.g. `LogTab.Update`) type-switch on `tea.KeyMsg`. The
builder must verify which concrete type actually arrives at `Tab.Update()` —
check whether `tea.KeyPressMsg` satisfies/aliases `tea.KeyMsg` in the
`charm.land/bubbletea/v2` version this module vendors (see `go.mod`), and
match whatever `LogTab.Update()` already type-switches on
(`msg.(type) { case tea.KeyMsg: ... }`) so `ReviewsTab` is consistent with
the established pattern in this codebase. Do not introduce a second key
message type convention.

## Relevant Files

| File | Change |
|---|---|
| `internal/tui/reviews_tab.go` | Add `selectedIndex` field, `rowCount()`, `moveCursor()`, real `Update()` body, `rowStyle` closure in `renderTable()`, update `buildTable` signature + both call sites |
| `internal/tui/reviews_tab_test.go` | New tests for cursor init, navigation, bounds clamping, empty-table disable, highlight presence/absence in `View()` output, persistence across simulated tab-switch (construct once, call `Update` then `View` then `Update` again — no re-construction), and `CopyableContent()` remaining unstyled |
| `internal/tui/autocomplete.go` | No change — read-only reference for the reused style pattern |
| `internal/tui/styles.go` | No change — `AutocompleteSelected` reused as-is |
| `internal/tui/tui.go` | No change — confirmed `up`/`down` already reach `ReviewsTab.Update()` via the existing fallthrough path |
| `internal/tui/tab_manager.go` | No change — confirmed `Update` forwarding already works generically |

## Concurrency Analysis

**No cross-goroutine access is introduced by this change.** `selectedIndex`
is read and written exclusively from the Bubble Tea event-loop goroutine:
`Update()` (writes, via `moveCursor`) and `View()` (reads, via the
`rowStyle` closure) are both invoked synchronously from the same
`tea.Program` render/update loop, and `ReviewsTab` has no ticker, no
background goroutine, and no watcher/agent-manager dependency (unlike
`LogTab`'s ring-buffer polling or the watcher's poll loop). This matches the
existing documented design in `ReviewsTab.View()`'s comment block, which
already establishes that all `store.List()` reads happen synchronously on
the event-loop goroutine with no caching or background refresh. No mutex,
lock-guarded accessor, or concurrent test is required for this issue.

## Team Orchestration

Single-package, single-file core change (`reviews_tab.go`) plus its test
file. No frontend/backend split and no independent subsystems — this does
not benefit from multi-builder parallelization. Recommend **one builder**
executing the tasks below sequentially, since each task's tests depend on
the previous task's code existing (state field before navigation, navigation
before highlight-integration tests, etc.). If the orchestrator prefers to
parallelize regardless, Task 1 and Task 2 (state field placement and
highlight-style refactor of `buildTable`) touch disjoint code regions and
could run in parallel, but Task 3 (Update wiring) depends on both — the
sequential path is simpler and low-risk given the small overall diff.

## Step-by-Step Task Breakdown

### Task 1: Add cursor state and bounds-checked movement helpers
**Acceptance Criteria**:
- `selectedIndex int` field added to `ReviewsTab` struct
- `rowCount() int` method added, reading `rt.store.List()` and returning `0` on error or nil/empty slice, `len(records)` otherwise
- `moveCursor(delta int)` method added implementing the clamping logic specified above, setting `selectedIndex = -1` when `rowCount() == 0`
- Unit tests: cursor stays at `0` when `moveCursor(-1)` called from initial state with rows present; cursor clamps at `n-1` when `moveCursor(1)` called repeatedly past the last row; cursor set to `-1` when store has zero records and `moveCursor` is called in either direction
**Dependencies**: None

### Task 2: Thread row-highlight styling through `buildTable`
**Acceptance Criteria**:
- `buildTable` signature extended with a `rowStyle func(int, string) string` parameter
- `renderTable()` updated to build and pass a closure that applies `rt.styles.AutocompleteSelected.Render(line)` when `i == rt.selectedIndex` (and `rt.styles != nil`), otherwise returns the line unchanged
- `CopyableContent()` updated to pass an identity `func(int, string) string` (no visible change to its existing output)
- Existing `CopyableContent()` tests continue to pass unmodified (plain text output must be byte-identical to before this change for the same input data)
- New test: `View()` output contains the row-highlight ANSI/style wrapping for the row at `selectedIndex` and not for other rows, when `styles != nil` and records are non-empty
- New test: with `styles == nil` (mirroring existing nil-styles test coverage patterns in this file, if any exist — otherwise construct one), `View()` does not panic and produces unstyled output identical to before this change
**Dependencies**: None (independent of Task 1's fields; only reads `rt.selectedIndex` which exists once Task 1 lands — if run in parallel, coordinate the field's existence first, or stub it during development)

### Task 3: Wire arrow-key navigation into `Update()`
**Acceptance Criteria**:
- `Update(msg tea.Msg) (Tab, tea.Cmd)` implementation replaced: type-switches (or type-asserts, matching the concrete key-message type actually used by `LogTab.Update()` in this codebase/bubbletea version) on incoming key messages, calling `rt.moveCursor(-1)` for `"up"` and `rt.moveCursor(1)` for `"down"`, and is a no-op for every other message (preserving current behavior for all other input, including resize which continues to go through `Resize()`, unaffected by this change)
- New test: given a `fakeReviewStore` with N > 1 records, sending N-1 `"down"` key messages through `Update()` then reading `rt.selectedIndex` shows the cursor at `N-1`, and one more `"down"` leaves it at `N-1` (clamped, not `N`)
- New test: sending `"up"` when `selectedIndex` is already `0` leaves it at `0` (no negative index)
- New test: sending `"up"`/`"down"` when the store returns zero records does not panic and leaves/sets `selectedIndex` at `-1`
- New test: after `Update()` moves the cursor, calling `View()` reflects the new `selectedIndex` in the highlighted row (integration between Task 2 and Task 3)
**Dependencies**: Task 1, Task 2

### Task 4: Persistence verification test (no new code expected)
**Acceptance Criteria**:
- New test demonstrating that constructing one `*ReviewsTab`, calling `Update()` to move the cursor to a non-zero index, then calling `View()` a second time (simulating "tab regains focus" — no `RestoreFocusState` call needed since row-cursor state is orthogonal to footer/message focus, as documented in this spec's "Cursor persistence" section) still shows the cursor at the moved position — i.e. `selectedIndex` is not implicitly reset by any code path in `View()`, `Resize()`, `CaptureFocusState()`, or `RestoreFocusState()`
- If this test fails, it indicates an unintended reset was introduced in Tasks 1–3 and must be fixed before proceeding — do not add new persistence/save-restore machinery; the fix should be removing whatever inadvertently zeroes `selectedIndex`
**Dependencies**: Task 3

### Task 5: Full-suite regression pass
**Acceptance Criteria**:
- `go test ./internal/tui/...` passes with no regressions in existing `reviews_tab_test.go`, `autocomplete_test.go`, `tab_manager_test.go`, and integration tests (`integration_test.go`, `tabs_interface_test.go`) that exercise the `Tab` interface generically against `ReviewsTab`
- `go vet ./...` and existing lint/format tooling (per `discover-qa-tools` skill conventions for this repo) pass with no new warnings
- Manual verification note in PR body: describe running the TUI locally (or via existing test harness) confirming visible highlight movement — this is a documentation acceptance criterion, not a new automated test, since no test harness for full terminal rendering exists in this repo today
**Dependencies**: Task 1, Task 2, Task 3, Task 4

## Validation Commands

```bash
# Run the full TUI package test suite
go test ./internal/tui/... -run TestReviewsTab -v

# Run the complete TUI package suite to catch regressions in Tab-interface
# generic tests and buildTable-dependent CopyableContent tests
go test ./internal/tui/... -v

# Vet
go vet ./...

# Confirm no stray references to the old buildTable 4-arg signature remain
grep -rn "buildTable(" --include="*.go" internal/tui/

# Confirm no new global up/down key bindings were introduced accidentally in tui.go
git diff --stat internal/tui/tui.go internal/tui/tab_manager.go
# (expected: empty diff for both files per this spec)
```
