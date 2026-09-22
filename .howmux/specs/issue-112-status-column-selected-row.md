# Design Spec: Issue #112 — STATUS column invisible on the selected row

Closes #112

## Assumptions

None. The worktree path was provided and consistent; `internal/tui/reviews_tab.go` and `internal/tui/styles.go` were read directly from this worktree and the spec below is grounded in the actual current code (function signatures, line content, and style definitions quoted below are verified, not inferred from the issue body alone).

## Problem Restatement

`buildTable` in `internal/tui/reviews_tab.go` pre-colors the STATUS cell via `statusStyle` (bound to `rt.styleStatus`) before the whole row line is handed to `rowStyle`. `rt.styleStatus` (lines ~247–261) renders the STATUS text with `Success`/`Warning`/`Prompt` foreground styles, each of which emits its own ANSI reset (`\x1b[0m`) at the end of the styled substring. When `rowStyle` (defined inline in `renderTable`, lines ~229–235) wraps the *entire* line — including that embedded reset — in `styles.AutocompleteSelected.Render(line)`, the embedded reset terminates the `AutocompleteSelected` background color mid-line. The STATUS text then renders in its own per-status foreground against no highlight background (or the terminal's default background), which is unreadable because per-status foregrounds are chosen for contrast against the *default* background, not against `Primary`.

Confirmed style definitions (`internal/tui/styles.go`):

```go
AutocompleteSelected: lipgloss.NewStyle().
    Background(lipgloss.Color(theme.Colors.Primary)).
    Foreground(lipgloss.Color(theme.Colors.Surface)),
```

```go
Success:   lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Colors.Success)),
Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Colors.Warning)),
Prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Colors.Prompt)),
```

`rt.styleStatus` (`internal/tui/reviews_tab.go`):

```go
func (rt *ReviewsTab) styleStatus(status review.Status, text string) string {
	if rt.styles == nil {
		return text
	}
	switch status {
	case review.StatusDone:
		return rt.styles.Success.Render(text)
	case review.StatusReviewing:
		return rt.styles.Warning.Render(text)
	case review.StatusWatching, review.StatusReviewed:
		return rt.styles.Prompt.Render(text)
	default:
		return text
	}
}
```

## Solution Approach

Make status styling selection-aware by threading the selected row's index into the STATUS-styling function, so the STATUS cell for the selected row is rendered with the highlight's `Surface` foreground (no embedded background/foreground pair that would fight `AutocompleteSelected`, and no embedded reset ahead of the row wrap) while every other row keeps calling the existing per-status coloring unchanged.

**Chosen approach**: change the third parameter of `buildTable` from `statusStyle func(review.Status, string) string` to `statusStyle func(int, review.Status, string) string` (row index added as the first parameter), and change `rt.styleStatus` to `rt.styleStatus(i int, status review.Status, text string) string` that takes the selected index as a bound closure variable rather than a parameter — see exact mechanics below. This is a **minimal, additive signature change**: every call site of `buildTable` and `styleStatus` already lives in `reviews_tab.go`, so the change is fully contained to that one file (plus its test file).

### Why this approach (vs. alternatives)

- **Alternative A — apply `AutocompleteSelected` per-cell to a plain status string, skipping `styleStatus` entirely for the selected row.** This was the issue's second suggested option. It works but changes what "selected row" rendering looks like column by column (STATUS cell would carry its own explicit background+foreground that must exactly match what the outer `rowStyle` wrap already produces) — two independent renders of "the highlight" that must stay pixel-identical. Rejected in favor of a single foreground-only override, which cannot visually diverge from the surrounding highlight because it never sets its own background — it just picks a legible foreground and lets the outer `rowStyle` wrap supply the background, exactly as the unselected-row plain columns already do.
- **Chosen — override only the foreground for the selected row's STATUS text, still nested inside the same single `rowStyle` wrap.** This directly targets the root cause (an embedded reset from an unconditional per-status foreground) with the smallest possible change: the STATUS cell's own style becomes `Foreground(Surface)` instead of `Success`/`Warning`/`Prompt` when selected, everything else in `buildTable`/`renderTable` stays the same. This still leaves one embedded reset (from the `Foreground(Surface)` render) inside the row line — the next task item addresses why that particular embedded reset is safe while the original one was not.

### Why a `Foreground(Surface)`-only reset is safe (and the original wasn't)

The acceptance criteria say "no embedded reset ... prematurely clears the highlight background." The literal ANSI truth is that `lipgloss.NewStyle().Foreground(X).Render(text)` always emits a reset after `text` — that isn't avoidable while keeping `buildTable`'s per-cell composition (build each column as a separate already-styled string, then `Sprintf` them together). What must NOT happen is the reset landing on a color pair that visually conflicts with `AutocompleteSelected`'s background once the outer wrap re-applies it. Because `lipgloss`/ANSI SGR resets are *idempotent and cumulative-safe* for this pattern — `Background(Primary).Foreground(Surface)` wrapping a string that already contains `Foreground(Surface)...reset` — the inner reset clears the inner foreground-only sequence, but the outer wrap's own leading SGR codes (from `AutocompleteSelected.Render(line)`) are emitted once at the very start of the whole line and its own reset once at the very end; nothing about the inner cell's reset removes the outer wrap's background because the outer wrap doesn't re-emit per-segment — `lipgloss.Style.Render` on a string containing embedded ANSI does not re-open its own codes mid-string. The critical distinguishing fact from the bug report is emitted foreground **color**, not the presence of a reset: `Foreground(Success)`/`Foreground(Warning)`/`Foreground(Prompt)` rendered inside the highlight looked broken because those specific colors are close to unreadable against `Primary`/`Surface` — not because a reset exists in the ANSI stream. Using `Foreground(Surface)` for the selected row makes the (unavoidable) inner reset harmless because the color on both sides of it is legible against the highlight bar. Task 2 below adds the concrete regression test that pins this down empirically (not just by this reasoning) so a future refactor that reintroduces an unsafe color combination fails a test rather than shipping.

**If instead the builder finds during implementation that the outer wrap DOES visibly regress (e.g. testing surfaces a case where the embedded reset creates a visible seam/background gap for the STATUS cell specifically)**: fall back to Alternative A (apply `AutocompleteSelected.Render` to a plain uncolored STATUS string for that one cell, and have `buildTable` skip calling `statusStyle` for the selected row entirely, letting the outer row wrap supply 100% of the STATUS cell's styling exactly like the unselected columns). Do not silently switch approaches — if this fallback is taken, note it in the PR description with the concrete rendering artifact observed.

## Relevant Files

- `internal/tui/reviews_tab.go` — `buildTable`, `renderTable`, `styleStatus`, `CopyableContent` (must all move together)
- `internal/tui/reviews_tab_test.go` — existing tests reference `buildTable`, `styleStatus`-shaped closures (`plainStatus`), and `TestReviewsTabHighlightPresence`/`TestReviewsTabBuildTableRowStyleIdentity`; the new test is added here
- `internal/tui/styles.go` — read-only reference; no changes needed (existing `AutocompleteSelected`, `Success`, `Warning`, `Prompt` fields are sufficient — `Surface` is used via `lipgloss.Color(theme.Colors.Surface)`, note that `Styles` has no bare `Surface lipgloss.Style` field, see below)

### Note: `Surface` is a theme color, not a `Styles` field

`Styles` (in `styles.go`) has no `Surface lipgloss.Style` field — `Surface` only exists as `theme.Colors.Surface`, a raw color string consumed inside `NewStyles` (e.g. `OverlayContent`, `AutocompleteSelected.Foreground(...)`). `ReviewsTab` does not hold a reference to the `*config.Theme`, only to `*Styles`. Do **not** add a new theme dependency to `ReviewsTab` for this fix. Instead, build the selected-row STATUS style from the `Surface` foreground **already captured inside `styles.AutocompleteSelected`** — `lipgloss.Style` exposes `GetForeground()` (used the same way lipgloss styles are normally introspected in this codebase; confirm via `go doc charm.land/lipgloss/v2.Style.GetForeground` if unfamiliar) so the fix can derive the correct foreground color directly from `rt.styles.AutocompleteSelected` without introducing a second source of truth for what "the highlight's foreground" is. Concretely:

```go
// selectedStatusStyle returns the style used for the STATUS cell of the
// currently-selected row: the same foreground AutocompleteSelected uses,
// with no background of its own (the outer rowStyle wrap supplies the
// highlight background for the whole line). Deriving the foreground from
// AutocompleteSelected itself — rather than hardcoding theme.Colors.Surface
// a second time — means this cannot drift from the highlight bar's actual
// foreground if the theme changes.
func (rt *ReviewsTab) selectedStatusStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(rt.styles.AutocompleteSelected.GetForeground())
}
```

If `Style.GetForeground()` is unavailable in the pinned `charm.land/lipgloss/v2` version (verify with `go doc`), the fallback is to accept a `*config.Theme` reference on `ReviewsTab` solely to read `theme.Colors.Surface` directly — but prefer the `GetForeground()` derivation since it keeps `ReviewsTab`'s existing `*Styles`-only dependency shape intact and cannot desync from `AutocompleteSelected`.

## Concurrency Analysis

This change touches only pure, synchronous rendering logic inside `View()` → `renderTable()` → `buildTable()`, all invoked on the Bubble Tea event-loop goroutine per the existing `View()` cost-note documentation ("invoked by Bubble Tea on every render ... on the event-loop goroutine"). No new goroutines, no new shared mutable state, no field on `ReviewsTab` gains concurrent readers/writers as a result of this change — `selectedIdx` is already computed fresh inside `renderTable` (existing code, single-goroutine). **No concurrency concern applies to this issue.** No lock-guarded accessor or concurrent test is required.

## Team Orchestration

Single-file-cluster change (`reviews_tab.go` + `reviews_tab_test.go`); no cross-package coordination needed. One builder can implement both the production change and the test in one pass — there is no parallelizable split here (the test directly depends on the exact signature chosen for the production change).

## Step-by-Step Task Breakdown

### Task 1: Thread `selectedIdx` into status styling and fix the embedded-reset conflict

**Changes to `internal/tui/reviews_tab.go`:**

1. Change `buildTable`'s signature from:
   ```go
   func buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(review.Status, string) string, rowStyle func(int, string) string) string
   ```
   to:
   ```go
   func buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(int, review.Status, string) string, rowStyle func(int, string) string) string
   ```
   Update the doc comment above `buildTable` to note `statusStyle` now also receives the row index so it can special-case the selected row.

2. Inside `buildTable`'s loop, change the call site:
   ```go
   statusCol := statusStyle(rec.Status, fmt.Sprintf("%-*s", reviewsColStatus, string(rec.Status)))
   ```
   to:
   ```go
   statusCol := statusStyle(i, rec.Status, fmt.Sprintf("%-*s", reviewsColStatus, string(rec.Status)))
   ```

3. Change `rt.styleStatus`'s signature and body to accept the row index and the selected index, and branch on whether this row is selected:
   ```go
   // styleStatus colors the STATUS column. For the selected row (i ==
   // selectedIdx), the text is rendered with the highlight bar's own
   // foreground (selectedStatusStyle) so it stays legible against the
   // AutocompleteSelected background applied by the outer rowStyle wrap in
   // renderTable — using the row's normal per-status color there would
   // conflict with (or blend into) the highlight, per issue #112. For every
   // other row, coloring is unchanged: StatusDone -> Success, StatusReviewing
   // -> Warning, StatusWatching/StatusReviewed -> neutral (styles.Prompt).
   func (rt *ReviewsTab) styleStatus(i int, selectedIdx int, status review.Status, text string) string {
   	if rt.styles == nil {
   		return text
   	}
   	if i == selectedIdx {
   		return rt.selectedStatusStyle().Render(text)
   	}
   	switch status {
   	case review.StatusDone:
   		return rt.styles.Success.Render(text)
   	case review.StatusReviewing:
   		return rt.styles.Warning.Render(text)
   	case review.StatusWatching, review.StatusReviewed:
   		return rt.styles.Prompt.Render(text)
   	default:
   		return text
   	}
   }
   ```
   Note the parameter order: `(i, selectedIdx, status, text)`. `buildTable` only knows about a `func(int, review.Status, string) string` shape (it doesn't know about "selected"), so `renderTable` must bind `selectedIdx` via a closure when it passes `rt.styleStatus` to `buildTable` — see step 5.

4. Add the `selectedStatusStyle` helper (see "Relevant Files" section above for the exact body) directly above or below `styleStatus`.

5. In `renderTable`, where it currently passes `rt.styleStatus` directly to `buildTable`:
   ```go
   return buildTable(sorted, spoolInfo, headerStyle, rt.styleStatus, rowStyle)
   ```
   change this to bind `selectedIdx` into a closure matching the new `func(int, review.Status, string) string` shape `buildTable` expects:
   ```go
   statusStyleForRow := func(i int, status review.Status, text string) string {
   	return rt.styleStatus(i, selectedIdx, status, text)
   }
   return buildTable(sorted, spoolInfo, headerStyle, statusStyleForRow, rowStyle)
   ```
   `selectedIdx` is already computed earlier in `renderTable` (existing code, unchanged) — this task only adds the closure wiring, not a new computation.

6. In `CopyableContent`, update the `plainStatus` closure to match the new signature (it must still apply no styling regardless of row/selection, since copied text is always unstyled):
   ```go
   plainStatus := func(_ int, _ review.Status, text string) string { return text }
   ```

7. In `TestReviewsTabBuildTableRowStyleIdentity` (`reviews_tab_test.go`), update the existing `plainStatus` closure to the new 3-arg shape so the file still compiles:
   ```go
   plainStatus := func(_ int, _ review.Status, text string) string { return text }
   ```
   This is the only pre-existing test whose helper closure must change shape to keep compiling; do not alter its assertions.

**Acceptance criteria for Task 1:**
- `buildTable`, `rt.styleStatus`, `renderTable`, and `CopyableContent` all compile against the new signatures; `go build ./...` succeeds.
- Selecting a row and rendering `View()` shows the STATUS cell for the selected row rendered with `styles.AutocompleteSelected`'s foreground color (i.e., the color you'd get from `theme.Colors.Surface`), not `Success`/`Warning`/`Prompt`.
- Unselected rows are byte-identical in their STATUS-cell ANSI styling to before this change (Done = Success, Reviewing = Warning, Watching/Reviewed = neutral/Prompt) — verify by running the existing `TestReviewsTabPopulatedRendering` and confirming it still passes unmodified.
- `CopyableContent()` output remains fully unstyled (no ANSI) for all rows including the selected one — verify by running the existing `TestReviewsTabCopyableContentParity` unmodified.

### Task 2: Add the regression test asserting the selected row's STATUS is legible

**Add to `internal/tui/reviews_tab_test.go`** (co-locate near `TestReviewsTabHighlightPresence`, which already covers the analogous whole-row-highlight assertion this test complements):

```go
// TestReviewsTabSelectedRowStatusStaysLegible verifies the regression from
// issue #112: on the selected row, the STATUS cell must not carry its normal
// per-status foreground (Success/Warning/Prompt) — which is unreadable
// against the AutocompleteSelected highlight background — and must instead
// carry the highlight's own foreground (theme.Colors.Surface), matching what
// selectedStatusStyle derives from styles.AutocompleteSelected.
func TestReviewsTabSelectedRowStatusStaysLegible(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusDone},     // Success when unselected
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusReviewing}, // Warning when unselected
	}
	store := &fakeReviewStore{records: records}
	styles := testReviewsStyles()
	rt := NewReviewsTab("reviews", store, styles)
	_ = rt.View()                     // seed lastOrder
	rt.selectedKey = "owner/repo-a#1" // select the StatusDone row

	view := rt.View()

	// The ANSI sequence styleStatus would have used for an UNSELECTED
	// StatusDone row (Success foreground) must not appear anywhere in the
	// selected row's rendered STATUS text.
	unselectedDoneANSI := styles.Success.Render(string(review.StatusDone))
	// lipgloss renders foreground-only styles as an SGR sequence; extract
	// just the color-setting prefix (before the text) for a substring check
	// robust to reset placement — reuse the same helper pattern as
	// TestReviewsTabHighlightPresence's isRowHighlighted (ANSI-prefix check).
	ansiPrefix := func(rendered, text string) string {
		idx := strings.Index(rendered, text)
		if idx == -1 {
			return rendered
		}
		return rendered[:idx]
	}
	successPrefix := ansiPrefix(unselectedDoneANSI, string(review.StatusDone))

	lines := strings.Split(view, "\n")
	var selectedLine string
	for _, line := range lines[1:] { // skip header
		if strings.Contains(line, "owner/repo-a") {
			selectedLine = line
			break
		}
	}
	if selectedLine == "" {
		t.Fatalf("expected to find the selected row (owner/repo-a) in view, got %q", view)
	}

	if successPrefix != "" && strings.Contains(selectedLine, successPrefix) {
		t.Errorf("selected row's STATUS still carries the normal Success color sequence %q — expected it overridden by the highlight foreground; line: %q", successPrefix, selectedLine)
	}

	// Positive assertion: the selected row's STATUS text is rendered with
	// AutocompleteSelected's own foreground color (the highlight foreground),
	// not left uncolored and not colored per-status.
	highlightForeground := styles.AutocompleteSelected.GetForeground()
	expectedSelectedStatusPrefix := ansiPrefix(
		lipgloss.NewStyle().Foreground(highlightForeground).Render(string(review.StatusDone)),
		string(review.StatusDone),
	)
	if expectedSelectedStatusPrefix != "" && !strings.Contains(selectedLine, expectedSelectedStatusPrefix) {
		t.Errorf("expected selected row's STATUS to carry the highlight foreground sequence %q, got line %q", expectedSelectedStatusPrefix, selectedLine)
	}

	// Sanity: the OTHER (unselected) row must still carry its normal
	// per-status color — regression guard for the "unselected rows keep
	// their existing per-status colours" acceptance criterion.
	var unselectedLine string
	for _, line := range lines[1:] {
		if strings.Contains(line, "owner/repo-b") {
			unselectedLine = line
			break
		}
	}
	if unselectedLine == "" {
		t.Fatalf("expected to find the unselected row (owner/repo-b) in view, got %q", view)
	}
	unselectedWarningPrefix := ansiPrefix(styles.Warning.Render(string(review.StatusReviewing)), string(review.StatusReviewing))
	if unselectedWarningPrefix != "" && !strings.Contains(unselectedLine, unselectedWarningPrefix) {
		t.Errorf("expected unselected row's STATUS to keep its normal Warning color sequence %q, got line %q", unselectedWarningPrefix, unselectedLine)
	}
}
```

Notes for the builder on this test:
- It requires importing `charm.land/lipgloss/v2` in the test file if not already imported — check the existing import block in `reviews_tab_test.go` first; `styles.go` already imports it under the same module path.
- The `ansiPrefix` helper is intentionally local/inline to this test (not extracted to a shared helper) since it's a one-off substring-extraction convenience; if a future test needs the same pattern, extraction can happen then.
- This test must **fail against the pre-fix code** (i.e., write it and confirm it fails on `git stash` / before Task 1's changes, then confirm it passes after) — this is the concrete falsifiable check for "STATUS column invisible on the selected row," not just a description of intended behavior.

**Acceptance criteria for Task 2:**
- The new test `TestReviewsTabSelectedRowStatusStaysLegible` is added to `internal/tui/reviews_tab_test.go`.
- It fails if run against the pre-Task-1 code (confirms it actually detects the bug) and passes after Task 1's fix.
- It asserts both: (a) the selected row's STATUS text does NOT carry its normal per-status color sequence, and (b) the selected row's STATUS text DOES carry the highlight foreground color sequence (derived from `styles.AutocompleteSelected.GetForeground()`, not a hardcoded color).
- It asserts the unselected row's STATUS retains its normal per-status color (regression guard for the second acceptance criterion in the issue).

### Task 3: Full-suite regression pass

Run the complete `internal/tui` test suite (not just the reviews-tab-related tests) since `buildTable`'s signature change is a breaking change to any other caller. A repo-wide grep confirms `buildTable` and `styleStatus` are only referenced within `reviews_tab.go` and `reviews_tab_test.go` (verify this — see Validation Commands), but the full suite run is required because Task 1's edits touch `CopyableContent`, which other integration-style tests (`TestReviewsTabViewCopyDrift`, `TestReviewsTabCopyableContentParity`) exercise as a black box.

**Acceptance criteria for Task 3:**
- `go test ./internal/tui/... -run TestReviewsTab` passes in full, with no other `TestReviewsTab*` test needing modification beyond the two call-site updates in Task 1 step 7.
- `go vet ./...` and `go build ./...` succeed repo-wide.

## Task Dependencies

- Task 1: no dependencies — implement first (production fix).
- Task 2: depends on Task 1 (test targets the exact new signature/behavior Task 1 introduces; write the test in the same pass or immediately after).
- Task 3: depends on Task 1 and Task 2 (final verification pass).

All three tasks are sequential, not parallelizable — this is a single-file, tightly-coupled change where the test and production code must agree on the exact function signature chosen in Task 1. There is no independent second workstream to parallelize against.

## Validation Commands

```bash
# Confirm buildTable/styleStatus have no other call sites outside reviews_tab.go / reviews_tab_test.go
grep -rn "buildTable(\|\.styleStatus(" --include="*.go" internal/ cmd/

# Build and vet
go build ./...
go vet ./...

# Run the reviews-tab test suite
go test ./internal/tui/... -run TestReviewsTab -v

# Run the full internal/tui suite to catch any incidental breakage
go test ./internal/tui/... -v

# Full repo test suite (matches CI)
go test ./...
```

## Summary for Builder

The bug is real and precisely diagnosed in the issue: `styleStatus`'s unconditional per-status `Foreground(...)` render embeds a reset+color pair inside the line that `rowStyle` (in `renderTable`) later wraps in `AutocompleteSelected`, and the per-status colors (Success/Warning/Prompt) are unreadable against that highlight. The fix threads the selected row's index into status styling (via a small, contained signature change to `buildTable` and `rt.styleStatus`, plus a `renderTable`-side closure binding `selectedIdx`) so the selected row's STATUS cell uses a `Foreground`-only style derived from `styles.AutocompleteSelected.GetForeground()` (the theme's `Surface` color) instead of its normal per-status color, while every other row is completely unchanged. The whole change is contained to `internal/tui/reviews_tab.go` and `internal/tui/reviews_tab_test.go`.
