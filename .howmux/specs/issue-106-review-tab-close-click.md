# Design Spec: Review content tab × close button does nothing (mouse click)

Closes #106

## Root Cause (confirmed with code evidence)

### The 15-char truncation / byte-vs-rune theory is refuted

I reproduced the click math directly against `TabManager` (bypassing the
model) and through the full `model.Update()` path, sweeping every mouse-x
column across a rendered header containing `Main | Reviews | Review: owner/repo
#123` at every possible title length (1–30 chars, spanning the 15-char
truncation boundary in both directions). In every case, clicking the visual
column where the `×` glyph actually renders (verified with
`lipgloss.Width()` against the ANSI-stripped header string) correctly
matched `HandleTabHeaderClick`'s computed `closeButtonStart`, and the tab
was removed from `TabManager.tabs`. `len(closeBtnText)` (`" ×"`) is 3
*bytes* in Go (the space is 1 byte, `×` U+00D7 is 2 UTF-8 bytes) while its
*rune count* is 2 and its *display width* is 2 (`×` is a narrow/width-1
glyph) — so there is a byte-vs-display-width mismatch in the constant
itself, but because every character in a Review tab's title
(`"Review: owner/repo #123"`) is single-byte ASCII, `len(title)` tracks
`lipgloss.Width(title)` exactly, so the mismatch never manifests as a
missed click for this tab type. **Do not "fix" the truncation math or the
close-button width constant — it is not the defect and touching it would
violate the issue's constraint against special-casing shared math without
proof.**

### Confirmed defect: `CloseTab` never redirects focus to Reviews; only ESC does

`internal/tui/tab_manager.go:164-189` (`TabManager.CloseTab`):

```go
func (tm *TabManager) CloseTab(index int) bool {
	if index < 0 || index >= len(tm.tabs) || !tm.tabs[index].IsClosable() {
		return false
	}
	...
	tm.tabs = append(tm.tabs[:index], tm.tabs[index+1:]...)
	tm.ClearHover()

	// Maintain active tab index
	if tm.activeTab >= len(tm.tabs) && len(tm.tabs) > 0 {
		tm.activeTab = len(tm.tabs) - 1
	} else if tm.activeTab > index {
		tm.activeTab--
	}

	return true
}
```

This is **generic index-clamping only** — "if the active index fell past
the end, clamp to the new last tab; otherwise, if the active index was
after the closed one, shift left by one." It has no concept of "return to
Reviews." That behavior only exists in one place:
`internal/tui/tui.go:1231-1242`, the dedicated `"esc"` key handler:

```go
if msg.String() == "esc" {
	activeTab := m.tabManager.GetActiveTab()
	if activeTab != nil && activeTab.Type() == TabTypeReviewContent {
		reviewsIdx := m.findReviewsTabIndex()
		m.tabManager.CloseTab(m.tabManager.GetActiveTabIndex())
		if reviewsIdx >= 0 {
			var cmd tea.Cmd
			m, cmd = m.switchActiveTab(reviewsIdx)
			return m, cmd
		}
		return m, nil
	}
}
```

ESC explicitly looks up `findReviewsTabIndex()` (`internal/tui/tui.go:2375`)
and calls `switchActiveTab(reviewsIdx)` itself, **after** calling
`CloseTab`, to force the destination. The mouse-click path
(`internal/tui/tui.go:1044-1053`, the `tea.MouseClickMsg` case) does none of
this — it calls `HandleTabHeaderClick(mouse.X)` and returns, relying
entirely on `CloseTab`'s generic clamping to land somewhere reasonable:

```go
case tea.MouseClickMsg:
	if m.activeOverlay == overlayNone {
		mouse := msg.Mouse()
		if mouse.Y < tabHeaderHeight {
			m.tabManager.HandleTabHeaderClick(mouse.X)
			m.checkLogTabClosed()
			return m, nil
		}
		...
```

`HandleTabHeaderClick` (`internal/tui/tab_manager.go:601-631`) does call
`tm.CloseTab(i)` when the click lands in the close-button region — the
close **does happen**, the tab **is** removed from `tm.tabs`. But
`CloseTab`'s clamping only guarantees "land on the tab that is now at the
end of the list, or shift left by one" — not "land on Reviews."

### Where this actually breaks visibly

I confirmed with a reproduction test
(`TestReproCloseWithTabsAfterReviewContent`, run against the real
`model.Update()` path — tab order `[Main, Reviews, ReviewContent, LogTab]`,
active tab = ReviewContent) that clicking the review content tab's `×` at
its correct rendered visual column:

- **does** remove the `ReviewContentTab` from `tm.tabs` (confirmed: tab
  count drops from 4 to 3, no `TabTypeReviewContent` tab remains), but
- the active tab afterward is **`TabTypeLog`** (`log-1`), not Reviews —
  because `LogTab` was sitting at the next index and `CloseTab`'s
  `tm.activeTab >= len(tm.tabs)` clamp doesn't apply (the index is still
  in-bounds after removal — it now just refers to whatever slid into that
  slot).

This is exactly the "clicking × does nothing" symptom as experienced by a
user: the review tab silently disappears from the strip and the view jumps
to some *other* already-open tab (a log tab, a planning tab, or an agent
tab opened after the review window) instead of Reviews. If no tab happens
to exist after the review content tab, `CloseTab`'s
`tm.activeTab >= len(tm.tabs)` branch clamps to the new last tab, which
often *is* Reviews by coincidence of tab ordering (Main and Reviews are
always tabs 0 and 1, added once in `newModel` and never reordered) — this
coincidence is almost certainly why triage/manual spot-checks in a
fresh/minimal session could plausibly look like "it closes but nothing
happens," or in some session states appear to work by luck, masking the
real defect: **the click-close path has no equivalent of ESC's explicit
`findReviewsTabIndex()` redirect.**

### Ctrl+W has the identical defect (confirmed, not just "untested")

`internal/tui/tui.go:1379-1388`:

```go
case "ctrl+w":
	// Close current tab (if closable)
	activeTab := m.tabManager.GetActiveTab()
	if activeTab != nil && activeTab.Type() == TabTypeLog {
		if err := m.deactivateLogging(); err != nil {
			...
		}
	}
	m.tabManager.CloseCurrentTab()
	return m, nil
```

`CloseCurrentTab()` (`internal/tui/tab_manager.go:236-238`) is
`tm.CloseTab(tm.activeTab)` — the same generic clamping, same missing
Reviews redirect. Ctrl+W on a review content tab will close it (AC "verify
Ctrl+W behavior" is satisfied: it does close) but, like the mouse-click
path, does not reliably switch focus back to Reviews when another closable
tab exists after it in the list.

## Minimal Fix (respects the issue's constraints)

**Do not** touch `tab_manager.go`'s `RenderTabHeaders` / `HandleTabHeaderClick`
click-position math — it is correct and shared across all tab types with no
type-specific branching, and this investigation found no divergence there
to justify introducing one. **Do not** change the existing `"esc"` handler
in `tui.go` — it already does the right thing and is the reference
behavior to match, not to alter.

The fix is to give the mouse-click path (and, per AC3, the Ctrl+W path) the
same "if the tab I just closed was a review content tab, redirect to
Reviews" behavior that the `"esc"` handler already has — implemented once,
shared by both call sites, rather than duplicating the ESC handler's
`findReviewsTabIndex()` + `switchActiveTab()` sequence inline in two more
places.

### Approach

Add a small model-level helper, `closeTabAndReturnToReviewsIfNeeded`, that:

1. Captures whether the tab about to be closed (at a given index) is a
   `TabTypeReviewContent` tab, *before* calling `CloseTab` (the tab
   reference is gone after removal).
2. Calls `tm.CloseTab(index)`.
3. If the closed tab was `TabTypeReviewContent` and the close succeeded,
   looks up `findReviewsTabIndex()` and calls `switchActiveTab(reviewsIdx)`,
   mirroring the ESC handler's own sequence exactly.
4. Returns the resulting `(model, tea.Cmd)`.

Then:

- The `tea.MouseClickMsg` handler (`tui.go:1044-1053`) replaces its direct
  `m.tabManager.HandleTabHeaderClick(mouse.X)` call with logic that
  determines *which* tab (if any) `HandleTabHeaderClick` is about to close
  and routes through the same helper, OR — simpler and lower-risk — the
  helper is invoked from inside the click handler by checking, immediately
  after `HandleTabHeaderClick` returns, whether the previously-active
  review-content tab is now gone. See "Implementation detail" below for the
  exact recommended shape; both achieve the same outcome without touching
  `HandleTabHeaderClick` itself.
- The `"ctrl+w"` handler (`tui.go:1379-1388`) replaces its direct
  `m.tabManager.CloseCurrentTab()` call with the same helper.
- The existing `"esc"` handler is left completely unchanged (constraint).

### Implementation detail — recommended shape for the mouse-click case

`HandleTabHeaderClick` returns `bool` (handled or not) and internally
decides whether the click was a close vs. a select vs. neither. Rather than
threading a new return value through that shared function (which the issue
explicitly asks us to avoid unless proven necessary — and it is not
necessary here), detect the close **from the outside**, using information
already available before and after the call:

```go
case tea.MouseClickMsg:
	if m.activeOverlay == overlayNone {
		mouse := msg.Mouse()
		if mouse.Y < tabHeaderHeight {
			// Capture identity of the tab that WAS active and its type
			// before the click, so we can tell afterward whether a
			// review content tab was the one just closed (as opposed to
			// merely losing focus because a different tab was clicked).
			var closedReviewContentTab bool
			if activeBefore := m.tabManager.GetActiveTab(); activeBefore != nil &&
				activeBefore.Type() == TabTypeReviewContent {
				beforeID := activeBefore.ID()
				m.tabManager.HandleTabHeaderClick(mouse.X)
				closedReviewContentTab = m.tabManager.FindTabByID(beforeID) < 0
			} else {
				m.tabManager.HandleTabHeaderClick(mouse.X)
			}

			m.checkLogTabClosed()

			if closedReviewContentTab {
				if reviewsIdx := m.findReviewsTabIndex(); reviewsIdx >= 0 {
					var cmd tea.Cmd
					m, cmd = m.switchActiveTab(reviewsIdx)
					return m, cmd
				}
			}
			return m, nil
		}
		...
```

This only special-cases behavior in `tui.go` (already the file that hosts
the ESC special-case), reuses `findReviewsTabIndex()` and
`switchActiveTab()` verbatim from the ESC handler, and leaves
`tab_manager.go` untouched. It correctly does nothing extra when the click:
- selects a different tab (not a close) — `closedReviewContentTab` stays
  `false` because the active tab before the click wasn't
  `TabTypeReviewContent`, or it was but is still present after the click
  (meaning the click selected/hovered rather than closed it).
- closes some *other* closable tab (Log/Planning/Agent) while a
  ReviewContent tab is active but not the one clicked — the "before" check
  only fires when the review content tab is the currently *active* one,
  which is what determines its ID for the presence check; a click closing
  an unrelated tab leaves the active review content tab present, so
  `closedReviewContentTab` is correctly `false`.

Note: this only handles the case where the *active* tab is the review
content tab being closed. Clicking the × of a review content tab that is
**not currently active** is already possible today (any closable tab can
be closed via its header without being active first — see
`HandleTabHeaderClick`'s existing behavior of closing tab `i` directly by
index regardless of which tab is active). For that case, capture the
target tab's identity by index before the call instead of relying on
`GetActiveTab()`:

```go
			// Determine, by index, whether the click's target position
			// would land on a still-existing ReviewContentTab's close
			// region — capture IDs of all ReviewContentTabs before the
			// click, then diff after.
```

Builder should implement the general (not just "active tab") case: capture
the full list of `TabTypeReviewContent` tab IDs present before calling
`HandleTabHeaderClick`, call it, then diff against the list of
`TabTypeReviewContent` tab IDs present after. If the "before" set had
exactly one more entry than the "after" set (i.e., exactly one
`ReviewContentTab` disappeared), and that ID is not present in "after",
treat it as a review-content-tab close and redirect to Reviews — this
covers both the active-tab-closed-itself case and the
click-closed-a-non-active-review-tab case uniformly, without needing
`HandleTabHeaderClick` to report anything new.

```go
case tea.MouseClickMsg:
	if m.activeOverlay == overlayNone {
		mouse := msg.Mouse()
		if mouse.Y < tabHeaderHeight {
			before := reviewContentTabIDs(m.tabManager)
			m.tabManager.HandleTabHeaderClick(mouse.X)
			m.checkLogTabClosed()

			if len(before) > len(reviewContentTabIDs(m.tabManager)) {
				if reviewsIdx := m.findReviewsTabIndex(); reviewsIdx >= 0 {
					var cmd tea.Cmd
					m, cmd = m.switchActiveTab(reviewsIdx)
					return m, cmd
				}
			}
			return m, nil
		}
		...

// reviewContentTabIDs returns the IDs of all TabTypeReviewContent tabs
// currently in tm, in order. Used to detect (by set difference) whether a
// click removed a review content tab, without HandleTabHeaderClick needing
// to report which tab type it closed.
func reviewContentTabIDs(tm *TabManager) []string {
	var ids []string
	for _, tab := range tm.GetTabs() {
		if tab.Type() == TabTypeReviewContent {
			ids = append(ids, tab.ID())
		}
	}
	return ids
}
```

This `reviewContentTabIDs` helper (or equivalent) is also exactly what the
Ctrl+W fix needs, reused verbatim:

```go
case "ctrl+w":
	activeTab := m.tabManager.GetActiveTab()
	if activeTab != nil && activeTab.Type() == TabTypeLog {
		if err := m.deactivateLogging(); err != nil {
			m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Warning during logging deactivation: %v", err)))
		}
	}
	before := reviewContentTabIDs(m.tabManager)
	m.tabManager.CloseCurrentTab()
	if len(before) > len(reviewContentTabIDs(m.tabManager)) {
		if reviewsIdx := m.findReviewsTabIndex(); reviewsIdx >= 0 {
			var cmd tea.Cmd
			m, cmd = m.switchActiveTab(reviewsIdx)
			return m, cmd
		}
	}
	return m, nil
```

The builder may choose either the "active-tab-only" detection shown first
or the general ID-diff `reviewContentTabIDs` approach shown second — the
ID-diff approach is recommended because it is a single helper shared by
both the mouse-click and Ctrl+W call sites (no duplicated capture/compare
logic) and correctly covers closing a non-active review content tab via
click, which the active-tab-only approach does not.

## Relevant Files

| File | Change |
|------|--------|
| `internal/tui/tui.go` | Add `reviewContentTabIDs(tm *TabManager) []string` helper (package-level function, near `findReviewsTabIndex` for locality). Modify the `tea.MouseClickMsg` case (~line 1044) to capture before/after review-content-tab IDs around the `HandleTabHeaderClick` call and redirect to Reviews on a detected close. Modify the `"ctrl+w"` case (~line 1379) identically. Do NOT modify the `"esc"` handler (~line 1231). |
| `internal/tui/tab_manager.go` | **No changes.** `CloseTab`, `HandleTabHeaderClick`, `RenderTabHeaders` are confirmed correct and shared; the issue's constraint against special-casing here is upheld because investigation found no divergence in this file to justify it. |
| `internal/tui/review_content_tab.go` | **No changes.** Confirmed by reading `Update`'s doc comment and implementation that this tab correctly delegates closing orchestration externally already; nothing here needs to change for either ESC (already works) or mouse-click/Ctrl+W (fixed in `tui.go`). |
| `internal/tui/tab_manager_test.go` or a new/existing review-tab test file | Add regression tests (see below). |

## Team Orchestration

Single-file-class change, no parallelizable split needed — this is a
small, tightly-scoped fix confined to `tui.go`'s two message handlers plus
one new shared helper function, with regression tests in the existing test
files. One builder handles the full task.

## Concurrency Analysis

**No concurrency concern.** This change touches only the main-goroutine
message-handling path (`model.Update`'s `tea.MouseClickMsg` and
`tea.KeyPressMsg("ctrl+w")` cases) and `TabManager` state, which is already
exclusively mutated from the main/render goroutine in the existing
architecture (no background goroutine writes to `tm.tabs` or `tm.activeTab`
today, and this change does not introduce any). No new shared-state
accessor, no new lock-guarded field, no cross-goroutine access introduced.

## Step-by-Step Task Breakdown

### Task 1: Add `reviewContentTabIDs` helper and fix the mouse-click path

**Acceptance Criteria:**
- Add `reviewContentTabIDs(tm *TabManager) []string` in `internal/tui/tui.go`
  (near `findReviewsTabIndex`, `internal/tui/tui.go:2375`), returning the
  IDs of all tabs with `Type() == TabTypeReviewContent`, in `tm.GetTabs()`
  order.
- Modify the `tea.MouseClickMsg` case (`internal/tui/tui.go:1044-1053`):
  capture `reviewContentTabIDs(m.tabManager)` before calling
  `HandleTabHeaderClick(mouse.X)`, call it, call the existing
  `m.checkLogTabClosed()` unchanged, then compare the before/after ID
  lists. If the after-count is lower than the before-count (a review
  content tab was closed by this click), look up `m.findReviewsTabIndex()`
  and, if found, call `m.switchActiveTab(reviewsIdx)` and return that
  model/cmd. Otherwise fall through to the existing `return m, nil`.
- Clicking the × on a Review content tab now switches the active tab to
  Reviews, matching ESC's behavior.
- Clicking anywhere else on any tab header (title area of any tab, ×  of a
  Log/Planning/Agent tab, empty header space) is completely unaffected —
  verify by running the existing `TestTabManager_HandleTabHeaderClick`
  (`internal/tui/tab_manager_test.go:284`) and confirming it still passes
  unmodified, plus manually tracing that non-review-tab-closing clicks
  produce `before == after` for `reviewContentTabIDs`, so the new redirect
  branch is never taken for them.

**Dependencies:** None.

### Task 2: Fix the Ctrl+W path

**Acceptance Criteria:**
- Modify the `"ctrl+w"` case (`internal/tui/tui.go:1379-1388`): capture
  `reviewContentTabIDs(m.tabManager)` before calling
  `m.tabManager.CloseCurrentTab()`, call it (keep the existing
  `TabTypeLog` → `deactivateLogging()` branch immediately above it
  unchanged), then apply the same before/after diff and
  `findReviewsTabIndex()` + `switchActiveTab()` redirect as Task 1, using
  the shared `reviewContentTabIDs` helper (no duplicated logic — the
  helper itself is defined once in Task 1).
- Pressing Ctrl+W while a Review content tab is active closes it and
  switches focus to Reviews.
- Ctrl+W on Planning tabs, Log tabs, and Agent tabs remains unchanged
  (verify via existing tests that exercise Ctrl+W on those tab types, if
  any exist — grep for `"ctrl+w"` in `*_test.go` before assuming none do).

**Dependencies:** Task 1 (reuses the `reviewContentTabIDs` helper Task 1
introduces; if run in parallel, both tasks must agree the helper lives in
`tui.go` and is added exactly once — recommend sequencing Task 2 after
Task 1 to avoid a merge conflict on the same new function, even though the
two call sites they modify don't otherwise overlap).

### Task 3: Regression tests

**Acceptance Criteria:**
- Add `TestMouseClickCloseButtonOnReviewContentTabClosesAndReturnsToReviewsTab`
  in `internal/tui/review_content_window_test.go` (co-located with the
  existing `TestEscOnReviewContentTabClosesAndReturnsToReviewsTab`,
  `internal/tui/review_content_window_test.go:129`, which it directly
  mirrors) — build the model via the existing
  `createTestModelWithReviewsTab` + `selectFirstRowAndPressEnter` helpers
  (`internal/tui/enter_key_test.go:345`,
  `internal/tui/review_content_window_test.go:50`) to reach an active
  `ReviewContentTab` exactly as the ESC test does, then:
  - Render the header via `m.tabManager.RenderTabHeaders(m.width, m.styles)`
    to get the actual rendered string.
  - Locate the visual column of the review content tab's `×` by
    ANSI-stripping the rendered header and finding the `×` rune's index
    (mirror the `stripANSI`/rune-index approach used during this
    investigation — add a small `stripANSI` test helper via `regexp` if
    one doesn't already exist in the test package; check first with
    `grep -rn "func stripANSI" internal/tui/*_test.go` since none existed
    at investigation time but may have been added by a parallel task).
  - Dispatch `tea.MouseClickMsg{X: <that column>, Y: 0}` through
    `m.Update(...)`.
  - Assert: the resulting active tab is `TabTypeReviews` (same assertion
    shape as the ESC test), AND `FindTabByID(contentTabID)` returns `-1`
    (tab actually removed, not just deactivated) — copy both assertions
    directly from `TestEscOnReviewContentTabClosesAndReturnsToReviewsTab`.
- Add a second case to the same test (or a sibling test) that opens a
  **second** closable tab (a `LogTab`, via `NewLogTab`) *after* the review
  content tab, switches back to the review content tab, then performs the
  same click-close, and asserts the active tab is still `TabTypeReviews`
  (not the Log tab) — this is the specific regression this investigation
  found: without the fix, closing the review tab while a later tab exists
  lands on that later tab instead of Reviews. This is the test that would
  have failed before the fix and is the one that pins the actual root
  cause down.
- Add `TestCtrlWOnReviewContentTabClosesAndReturnsToReviewsTab` mirroring
  the same two shapes (active-tab-only, and with-a-later-tab-present) but
  dispatching `tea.KeyPressMsg{...}` for `"ctrl+w"` instead of a mouse
  click (check existing Ctrl+W tests, e.g. via
  `grep -rn "ctrl+w" internal/tui/*_test.go`, for the exact `tea.KeyPressMsg`
  construction idiom already in use in this codebase — do not guess the
  struct shape).
- Confirm `TestTabManager_HandleTabHeaderClick`
  (`internal/tui/tab_manager_test.go:284`) still passes unmodified — this
  pins that Main/Agent tab click-to-select and click-to-close behavior is
  untouched (AC "No change to click behavior on Planning, Log, or Main
  tabs").

**Dependencies:** Task 1, Task 2 (tests exercise the fixed code paths).

## Validation Commands

```bash
# Run the full TUI package test suite
go test ./internal/tui/... -v

# Run just the new/modified tests
go test ./internal/tui/ -run 'ReviewContentTab|TabManager_HandleTabHeaderClick|CtrlWOnReviewContentTab' -v

# Confirm no regressions in existing tab-close/click coverage
go test ./internal/tui/ -run 'TestTabManager|TestEsc|TestEnterOnReviewsTab' -v

# Full build + vet
go build ./... && go vet ./...

# Race detector (defensive — this change touches no new shared state, but
# the package as a whole exercises goroutines elsewhere; keep race-clean)
go test ./internal/tui/... -race
```

## Notes for the Validator

- The core assertion to check manually against the diff: the `"esc"`
  handler block (`internal/tui/tui.go`, originally at line 1231) must be
  byte-for-byte unchanged.
- `internal/tui/tab_manager.go` must have zero diff. If the builder touched
  it, that is a constraint violation — the investigation in this spec
  found no justification for changes there.
- Verify the new `reviewContentTabIDs` helper is defined exactly once and
  called from both the mouse-click and Ctrl+W sites (not duplicated
  inline in each).
- Verify the new regression test that opens a tab *after* the review
  content tab and confirms the close-then-redirect still lands on Reviews
  — this is the test that actually pins the bug found during investigation
  (a naive "close a lone review tab" test would pass even on the old,
  buggy code by coincidence of `CloseTab`'s clamp-to-last-tab behavior when
  no later tab exists).
