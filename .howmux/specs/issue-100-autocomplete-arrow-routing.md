# Design Spec: Autocomplete dropdown arrow-key routing outside the Main tab

Closes #100

## Confirmed Current Behavior

Explored `internal/tui/tui.go` at the worktree path and confirmed the issue's
description matches the code exactly:

- `internal/tui/tui.go:1357` — `case "up", "down", "pgup", "pgdown", "home", "end":`
- Lines 1360–1384: a `TabTypeMain`-only branch that routes `up`/`down` to
  `m.input.Update(msg)` when `m.input.HasMatchedSuggestions()`, otherwise
  scrolls `m.consoleViewport`, then `return`s unconditionally — so nothing
  below this `if` ever runs when `TabTypeMain` is active.
- Lines 1386–1393: a second, separate `TabTypePlanning`-only branch that
  routes `up`/`down` to `m.input.Update(msg)` when `m.input.Focused()`
  (no `HasMatchedSuggestions()` check here — see Note below).
- Lines 1399–1410: falls through to `m.tabManager.Update(msg)` for
  everything else, including `TabTypeReviews` — so on the Reviews tab,
  `up`/`down` reach `ReviewsTab.Update` (`internal/tui/reviews_tab.go:462`),
  which unconditionally does `moveCursor(-1)`/`moveCursor(1)` on `up`/`down`
  regardless of whether the footer is focused or suggestions are showing.
  There is no suggestion-routing branch for `TabTypeReviews` at all, exactly
  as the issue describes.

Confirmed supporting facts:
- `m.input` is `*AutocompleteInput` (`internal/tui/tui.go:179`), which wraps
  `textinput.Model` (`internal/tui/autocomplete.go:26-29`, field name
  `textinput`, unexported but in-package).
- `AutocompleteInput.Focused()` (`autocomplete.go:60-62`) and
  `HasMatchedSuggestions()` (`autocomplete.go:162-164`) are already public
  methods with exactly the semantics the issue's suggested guard needs.
- `charm.land/bubbles/v2@v2.1.0` textinput `DefaultKeyMap()` binds
  `NextSuggestion` to `down`/`ctrl+n` and `PrevSuggestion` to `up`/`ctrl+p`
  (`textinput.go:84-85`), and `Update` dispatches on `m.KeyMap.NextSuggestion`
  / `PrevSuggestion` (`textinput.go:641-643`) — confirming that once an
  `up`/`down` `tea.KeyMsg` reaches `m.input.Update(msg)`, the suggestion
  cursor moves correctly with no further change needed in
  `AutocompleteInput` or `autocomplete.go`.
- `AutocompleteInput.RenderSuggestionsMenu()` (`autocomplete.go:167-198`)
  reads `a.textinput.CurrentSuggestionIndex()` to highlight the selected
  row — this is what "the highlight moves" cashes out to.
- `ReviewsTab.Update` (`reviews_tab.go:462-486`) has no footer-focus check on
  its own `up`/`down` cases — the *caller* (`model.Update`) is solely
  responsible for not forwarding `up`/`down` to `m.tabManager.Update` while
  the footer is focused and showing suggestions. This matches the pattern
  already used for `p`/`r`/`R`/`d` (AC5a guard, `tui.go` "default:" arm,
  `commented at tui.go` around line ~1497) — the tab's own key handler does
  not gate on footer focus; `model.Update`'s routing does.
- Existing regression-test infrastructure for exactly this class of routing
  bug already exists in `internal/tui/decide_routing_test.go`:
  `newDecideRoutingTestModel()` builds a `model` with a real
  `*AutocompleteInput`, a real `TabManager`, and enough scaffolding to drive
  `model.Update` end to end; `addReviewsTabWithRecord` (in
  `commands_decide_test.go`) adds a populated Reviews tab; `pressKey(r)`
  builds a `tea.KeyPressMsg` for a printable rune. The new tests for this
  issue follow the same conventions.

### Note on the pre-existing `TabTypePlanning` branch (do not regress)

The existing `TabTypePlanning` branch (`tui.go:1387-1393`) only checks
`m.input.Focused()`, not `HasMatchedSuggestions()`. Per AC5 in the issue, the
fix generalizes the guard to
`m.input.Focused() && m.input.HasMatchedSuggestions()`, hoisted above all
tab-type branches. This changes `TabTypePlanning`'s behavior very slightly:
previously, on the planning tab with the footer focused and **no** matched
suggestions, `up`/`down` were still sent to `m.input.Update(msg)` (which is a
harmless no-op for `up`/`down` on a `textinput.Model` with no suggestions —
`Update` only special-cases `NextSuggestion`/`PrevSuggestion`, which do
nothing when there's nothing to navigate, per `textinput.go:641-660`), then
returned `m, cmd` unconditionally, bypassing `navFocusCmd`/`m.tabManager.Update`
below. After the fix, that same no-suggestion case now falls through to the
`TabTypePlanning`-specific viewport-nav branch beneath the hoisted guard
(lines 1394-1410, currently below the now-removable inner check), which is
strictly more correct: AC4 requires per-tab fallback behavior to be preserved
when the dropdown is not showing, and the planning tab's viewport-nav
handling (`navFocusCmd`/`m.tabManager.Update`) is what should run in that
case — this is a bugfix, not a behavior the issue's tests need to preserve.
No existing test currently exercises this exact edge (planning tab, footer
focused, no suggestions, `pgup`/`pgdown`/`home`/`end`); the task below adds a
regression test for the general non-Main/non-Reviews case using the Main tab
and Reviews tab per the issue's explicit test list, and confirms via manual
trace (not a new test) that the planning branch is unaffected for the
suggestion-showing case per AC1–3.

## Solution Approach

Hoist a single guard to the very top of the
`case "up", "down", "pgup", "pgdown", "home", "end":` arm in
`internal/tui/tui.go`, before the `TabTypeMain` branch:

```go
case "up", "down", "pgup", "pgdown", "home", "end":
    // Dropdown navigation takes precedence over any tab-specific
    // viewport/row-navigation handling whenever the footer input is
    // focused and currently showing matched suggestions — independent
    // of which tab is active. Previously this only worked on
    // TabTypeMain (and, without the HasMatchedSuggestions check, on
    // TabTypePlanning), so arrows appeared to do nothing on the
    // Reviews tab (and any future tab) while the dropdown was open.
    if (msg.String() == "up" || msg.String() == "down") &&
        m.input.Focused() && m.input.HasMatchedSuggestions() {
        var cmd tea.Cmd
        m.input, cmd = m.input.Update(msg)
        return m, cmd
    }
    activeTab := m.tabManager.GetActiveTab()
    if activeTab != nil && activeTab.Type() == TabTypeMain {
        // Handle console scrolling
        switch msg.String() {
        case "up":
            m.consoleViewport.ScrollUp(1)
        case "down":
            m.consoleViewport.ScrollDown(1)
        case "pgup":
            m.consoleViewport.HalfPageUp()
        case "pgdown":
            m.consoleViewport.HalfPageDown()
        case "home":
            m.consoleViewport.GotoTop()
        case "end":
            m.consoleViewport.GotoBottom()
        }
        return m, nil
    }
    // Forward to tab manager for non-main tabs
    // But if footer has focus in planning tab, route up/down to footer for dropdown
    if activeTab != nil && activeTab.Type() == TabTypePlanning && m.input.Focused() {
        if msg.String() == "up" || msg.String() == "down" {
            var cmd tea.Cmd
            m.input, cmd = m.input.Update(msg)
            return m, cmd
        }
    }
    // ...rest unchanged (navFocusCmd / m.tabManager.Update fallthrough)
```

Key points:
- The new guard is placed **before** `activeTab := m.tabManager.GetActiveTab()`
  is even read for branching purposes (the variable itself can still be
  declared once and reused below it — see exact diff in "Relevant Files"),
  so it applies uniformly to every tab type, current and future, per AC5.
- The `TabTypeMain` branch's now-redundant inner suggestion check
  (`autocomplete.go`'s "Route up/down to textinput when suggestions are
  active" block, `tui.go:1361-1366`) is removed, since the hoisted guard
  already covers it — this is the "redundant inner check removed" the issue
  asks for. The rest of the `TabTypeMain` branch (console-scroll switch,
  unconditional `return m, nil`) is unchanged, preserving AC3/AC4.
- The `TabTypePlanning` branch's own `up`/`down`-to-input forwarding
  (`tui.go:1387-1393`) becomes dead code for `up`/`down` specifically once
  the hoisted guard exists, **but only when suggestions are showing** — the
  hoisted guard already returns before this code is reached in that case.
  When the footer is focused but there are no matched suggestions, this
  block used to still forward to `m.input.Update`; per the Note above, this
  is a (desirable) behavior change, so this dead branch is removed entirely
  rather than left in an unreachable state, to keep the arm readable. Its
  `pgup`/`pgdown`/`home`/`end` sibling handling three lines below
  (`navFocusCmd` for `TabTypePlanning`) is untouched — those keys were never
  part of the `up`/`down`-only forwarding block and are not part of AC5's
  arrow-key dropdown scope.
- Nothing in `internal/tui/reviews_tab.go` or `internal/tui/autocomplete.go`
  changes. `ReviewsTab.Update`'s `up`/`down` cases keep moving the row
  cursor unconditionally — it is the caller's job (this hoisted guard) to
  not call `m.tabManager.Update` at all when the footer is focused and
  showing suggestions, exactly mirroring the existing `p`/`r`/`R`/`d`/`n`/`e`
  AC5a guard pattern already in the same function for the `"default:"` key
  arm.

## Concurrency Analysis

This change is entirely single-goroutine: `model.Update` runs on Bubble
Tea's render/update goroutine, and every field touched (`m.input`,
`m.tabManager`, `m.consoleViewport`) is already only ever mutated from that
same goroutine in the existing code. No new field gains cross-goroutine
access, no `sync.Mutex`/`sync.RWMutex`-guarded type is touched, and no
background loop (watcher, agent manager) reads or writes any state this
change modifies. **No concurrency concern applies to this issue** — the
`-race` requirement in the issue's Tests section is about running the
existing/new TUI test suite under `-race` for general safety (per repo
convention, see `Taskfile.yml:30` `go test -v -race ...`), not about a new
shared-state hazard introduced by this fix.

## Relevant Files

| File | Change |
|---|---|
| `internal/tui/tui.go` | Modify the single `case "up", "down", "pgup", "pgdown", "home", "end":` arm (currently starting at line 1357): hoist the generalized `m.input.Focused() && m.input.HasMatchedSuggestions()` guard above the `TabTypeMain` branch; remove the now-redundant inner `HasMatchedSuggestions()` check inside the `TabTypeMain` branch (lines 1361-1366); remove the now-dead `up`/`down`-forwarding block inside the `TabTypePlanning` branch (lines 1387-1393, the `if msg.String() == "up" || msg.String() == "down"` block specifically — leave the surrounding `if activeTab.Type() == TabTypePlanning && m.input.Focused()` structure's later `pgup`/`pgdown`/`home`/`end` handling at lines 1399-1410 untouched). No other arm of `model.Update` changes. |
| `internal/tui/autocomplete_arrow_routing_test.go` (new) | New test file covering all four scenarios from the issue's Tests section, following the conventions in `decide_routing_test.go` (`newDecideRoutingTestModel`/`newFullDecideRoutingTestModel`, `addReviewsTabWithRecord`, `pressKey`). |

No changes to `internal/tui/autocomplete.go`, `internal/tui/reviews_tab.go`,
`internal/tui/command_registry.go`, or any other tab file — confirmed by
the issue's own Scope section and by the exploration above (the arrow-key
handling inside `AutocompleteInput`/`textinput.Model` already works
correctly once a `tea.KeyMsg` reaches it; the bug is purely in the caller's
routing).

## Team Orchestration

Single-file production change plus one new test file — no parallelizable
split needed; this is a single builder task.

## Step-by-Step Task Breakdown

### Task 1: Hoist the generalized dropdown-routing guard in `model.Update`

**File:** `internal/tui/tui.go`

**Change:** In the `case "up", "down", "pgup", "pgdown", "home", "end":` arm:

1. Immediately after the `case` line, insert:
   ```go
   if (msg.String() == "up" || msg.String() == "down") &&
       m.input.Focused() && m.input.HasMatchedSuggestions() {
       var cmd tea.Cmd
       m.input, cmd = m.input.Update(msg)
       return m, cmd
   }
   ```
2. Keep `activeTab := m.tabManager.GetActiveTab()` immediately after (it's
   still needed for every branch below).
3. Inside the `if activeTab != nil && activeTab.Type() == TabTypeMain {`
   block, delete the now-redundant:
   ```go
   // Route up/down to textinput when suggestions are active
   if (msg.String() == "up" || msg.String() == "down") && m.input.HasMatchedSuggestions() {
       var cmd tea.Cmd
       m.input, cmd = m.input.Update(msg)
       return m, cmd
   }
   ```
   leaving the console-scroll `switch` and trailing `return m, nil` as-is.
4. Inside the `if activeTab != nil && activeTab.Type() == TabTypePlanning && m.input.Focused() {`
   block, delete the now-dead:
   ```go
   if msg.String() == "up" || msg.String() == "down" {
       var cmd tea.Cmd
       m.input, cmd = m.input.Update(msg)
       return m, cmd
   }
   ```
   If this leaves the enclosing `if activeTab != nil && activeTab.Type() == TabTypePlanning && m.input.Focused() {}`
   block empty, remove the whole (now-empty) `if` block — do not leave dead
   scaffolding. Everything below it (the `navFocusCmd`/`pgup` etc. handling,
   `m.tabManager.Update(msg)` fallthrough) is untouched.
5. Do not touch any other `case` arm in `model.Update` (e.g. `"tab"`,
   `"shift+tab"`, `"enter"`, `"default:"`) — those already have their own
   correct, unrelated guards per the issue's Scope section.

**Acceptance criteria:**
- `go build ./...` succeeds.
- `gofmt -l internal/tui/tui.go` reports no diff (matches repo formatting).
- The diff touches only the one `case` arm described above — verify with
  `git diff internal/tui/tui.go` showing changes confined to that block.
- No change to `internal/tui/autocomplete.go`, `internal/tui/reviews_tab.go`,
  or `internal/tui/command_registry.go`.

**Dependencies:** None.

### Task 2: Add regression tests for all four scenarios in the issue's Tests section

**File:** `internal/tui/autocomplete_arrow_routing_test.go` (new)

Follow the conventions already established in `decide_routing_test.go`:
use `newDecideRoutingTestModel()` (real `*AutocompleteInput` via
`NewAutocompleteInput(registry, styles)`, real `TabManager`) as the base
model builder, `addReviewsTabWithRecord(m, review.Record{...})` to populate
a Reviews tab, and `pressKey(r)` / `tea.KeyPressMsg{Code: tea.KeyUp}` /
`tea.KeyPressMsg{Code: tea.KeyDown}` to build key messages. Because
`AutocompleteInput.textinput` is an unexported field in the same `tui`
package, tests may call `m.input.textinput.CurrentSuggestionIndex()` and
`m.input.textinput.MatchedSuggestions()` directly (no new exported method
needed) to assert the suggestion cursor actually moved.

To get real matched suggestions, set `m.input.SetValue("dec")` (or `"rev"`)
before driving the key — `NewCommandRegistry(nil)` already registers
`decide` and `review` commands (confirmed at `command_registry.go:97,104`),
so `GetFlattenedMatches("dec")` returns at least the `decide` family.

Required tests (one function per bullet in the issue, table-driven where
natural):

1. **`TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_SuggestionsVisible_ArrowsMoveSuggestion`**
   - Build model via `newDecideRoutingTestModel()`, add a Reviews tab via
     `addReviewsTabWithRecord`, switch active tab to it (or set
     `m.tabManager`'s active index directly — whichever the existing
     `TabManager` API supports, matching how `decide_routing_test.go` does
     it via `switchActiveTab`/`findReviewsTabIndex`), `m.input.SetFocus(true)`,
     `m.input.SetValue("dec")`.
   - Assert `m.input.HasMatchedSuggestions()` is true (test-setup sanity
     check) before driving the key.
   - Capture `before := m.input.textinput.CurrentSuggestionIndex()` and
     `beforeScroll := m.consoleViewport.YOffset` (or equivalent scroll
     position accessor already used elsewhere in the package, e.g. in
     `planning_scroll_test.go`'s `ScrollPercent()`/`AtBottom()` pattern —
     use whatever the `viewport.Model` in this repo's bubbles version
     exposes for position).
   - Drive `tea.KeyPressMsg{Code: tea.KeyDown}` through `m.Update`.
   - Assert the resulting model's `input.textinput.CurrentSuggestionIndex()`
     changed from `before` (moved forward — matches `NextSuggestion`
     semantics).
   - Assert the console viewport's scroll position is unchanged from
     `beforeScroll` (proves the fix didn't also scroll the console — the
     Reviews tab has no console viewport concept, so this doubles as
     "nothing else fired").
   - Repeat (or sub-test) for `tea.KeyUp` asserting the index moves backward
     (`PrevSuggestion`).

2. **`TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_NoSuggestions_ArrowsRetainExistingBehavior`**
   - Same setup, but `m.input.SetValue("")` (or a string matching nothing,
     e.g. `"zzzznomatch"`) so `HasMatchedSuggestions()` is false.
   - Drive `tea.KeyPressMsg{Code: tea.KeyDown}` (footer still focused).
   - Assert `m.input.Value()` unchanged (arrows are not textinput
     characters, so this mostly guards against accidental mutation) and,
     more importantly, that the key did NOT get swallowed by the dropdown
     guard — assert whatever the pre-fix fallback behavior is for this
     exact state (footer focused, Reviews tab, no suggestions): per current
     code this falls through to `m.tabManager.Update(msg)`, i.e.
     `ReviewsTab.Update`'s `up`/`down` case, which calls `moveCursor`. Since
     the footer is focused, this is arguably a pre-existing quirk (not
     something this issue is asked to fix — AC4 only requires "retain
     current per-tab behavior" when no suggestions are showing). Assert the
     row cursor position changes, matching current (pre-fix) behavior
     exactly, to lock in AC4's "no regression" requirement rather than
     inventing new desired behavior.

3. **`TestAutocompleteArrowRouting_ReviewsTab_FooterUnfocused_ArrowsMoveRowCursor`**
   - Regression guard from the issue's Tests list: `m.input.SetFocus(false)`,
     add a Reviews tab with at least 2 records so `moveCursor` has somewhere
     to go, drive `tea.KeyPressMsg{Code: tea.KeyDown}`.
   - Assert the selected row index changed (use whatever accessor
     `reviews_tab_test.go` already uses to read the selected row/cursor —
     reuse that helper rather than reaching into private fields freshly).
   - Assert `m.input.textinput.CurrentSuggestionIndex()` is unaffected
     (still whatever it was — footer never got the key).

4. **`TestAutocompleteArrowRouting_MainTab_SuggestionsVisible_ArrowsMoveSuggestion`**
   - Build a model with `TabTypeMain` active (this is the `TabManager`'s
     default tab — confirm via `NewTabManager()`'s existing default, matching
     how other Main-tab tests in the package construct it, e.g. any test in
     `footer_test.go` or `mode_switching_test.go` that doesn't explicitly
     add/switch tabs).
   - `m.input.SetFocus(true)`, `m.input.SetValue("rev")`.
   - Drive `tea.KeyPressMsg{Code: tea.KeyDown}`; assert
     `CurrentSuggestionIndex()` moves and `m.consoleViewport`'s scroll
     position is unchanged (proves AC3's "Main-tab dropdown nav preserved,
     console not scrolled instead").

5. **`TestAutocompleteArrowRouting_MainTab_NoSuggestions_ArrowsScrollConsole`**
   - Same Main-tab setup, `m.input.SetValue("")` so no suggestions.
   - Record `beforeScroll`, drive `tea.KeyPressMsg{Code: tea.KeyDown}`,
     assert the console viewport's scroll position changed (or, if the
     console has no scrollable content in the minimal test fixture, assert
     `ScrollDown` was invoked in a way observable from the package — e.g.
     seed `m.consoleViewport` with enough lines first, matching the pattern
     `planning_scroll_test.go` uses to seed viewport content before
     asserting scroll behavior). This is AC3's second half ("still scroll
     the console viewport when [suggestions are] not [visible]").

**Acceptance criteria:**
- All five tests above are present and pass: `go test ./internal/tui/... -run TestAutocompleteArrowRouting -race -v`.
- Test 1 fails (dropdown index does not move) if Task 1's fix is reverted
  (manually confirm by temporarily reverting the hoisted guard and
  re-running — this is the actual regression test for the issue).
- Test 3 fails to catch a regression if the hoisted guard's
  `m.input.Focused()` condition is ever dropped (i.e. it must remain red
  against a mutant that removes `m.input.Focused()` from the guard,
  proving the row-nav-mode protection is real, not incidental).
- No existing test in the package regresses:
  `go test ./internal/tui/... -race` passes in full.

**Dependencies:** Task 1 (tests are written against the fixed code; can be
authored in parallel but must run against Task 1's change to be meaningful —
in practice, implement Task 1 and Task 2 together in one pass since they are
one logical fix plus its regression coverage).

## Validation Commands

```bash
# Build
go build ./...

# Formatting
gofmt -l internal/tui/tui.go internal/tui/autocomplete_arrow_routing_test.go

# Full TUI package test suite, with race detector per issue's Scope section
go test ./internal/tui/... -race -v

# Targeted run of the new tests
go test ./internal/tui/... -run TestAutocompleteArrowRouting -race -v

# Confirm the diff is confined to the intended surface
git diff --stat
```

## Acceptance Criteria (mapped to issue)

1. Reviews tab active, footer focused, matched suggestions showing: up/down
   move `CurrentSuggestionIndex()` — covered by Task 1's guard placement and
   verified by Task 2 test 1.
2. Tab/Enter suggestion-accept behavior is unchanged — no code in
   `autocomplete.go` is touched by Task 1, so this is preserved by
   construction; no new test required (out of scope per issue's own Tests
   list, which only lists arrow-key scenarios).
3. Main-tab behavior preserved (dropdown nav when visible, console scroll
   when not) — covered by Task 1 (redundant inner check removed but
   behavior identical since the hoisted guard is a strict superset for
   `TabTypeMain`) and verified by Task 2 tests 4 and 5.
4. When dropdown is not showing, per-tab behavior is retained — covered by
   Task 1 (guard's `HasMatchedSuggestions()` condition) and verified by Task
   2 tests 2 and 5.
5. Routing is generalized via a single hoisted guard keyed on
   `m.input.Focused() && m.input.HasMatchedSuggestions()`, checked before
   all tab-type-specific branches, with the now-redundant per-tab inner
   checks removed — this is exactly Task 1's diff.
6. Row-navigation mode (footer unfocused) on the Reviews tab is unaffected —
   covered by the guard's `m.input.Focused()` condition and verified by Task
   2 test 3.

## Out of Scope (per issue's Scope section)

- `internal/tui/autocomplete.go` — no changes; its key handling already
  works once keys reach it.
- The command registry (`command_registry.go`) — no changes.
- Any tab's own `Update` method (`reviews_tab.go`, planning tab, etc.) — no
  changes; only the caller's routing in `model.Update` changes.
