# Issue #84: PR-review gate — Open selected review in scrollable window

Closes #84

## Overview

`ReviewsTab` currently shows metadata for each tracked PR (repo, status,
verdict, decision state) but not the review body itself. This adds a new,
closable `ReviewContentTab` that opens when the user presses Enter on a
selected Reviews tab row (footer not focused). It reads the markdown body of
the review's spool file (excluding front-matter), displays it in a
`viewport.Model` following `LogTab`'s exact pattern, supports Ctrl+Y copy via
`CopyableContent()`, and closes on ESC, returning focus to the Reviews tab.

This is read-only. No spool file is ever written, moved, or deleted by this
feature.

### Solution approach

1. **Body reading** (`internal/review`): add `ReadSpoolBody(spoolPath,
   homeDir string) (body string, info SpoolInfo)`, which reuses the same
   pending→done resolution `ReadSpoolInfo` already implements (factored into
   a shared internal helper `resolveSpoolPath`, added in this change) so the
   two functions cannot drift on file-resolution logic, and returns both the
   metadata (`SpoolInfo`, unchanged shape) and the raw body text found after
   the closing `---` front-matter fence.

2. **New Tab type**: `TabTypeReviewContent` added to the `TabType` enum in
   `tabs.go`. A new `ReviewContentTab` struct in a new file
   `internal/tui/review_content_tab.go` implements the `Tab` interface,
   built around a `viewport.Model` exactly as `LogTab` is (same
   `viewport.New(viewport.WithWidth/WithHeight)`, `MouseWheelEnabled = true`,
   `Update` forwards `tea.KeyMsg` to the viewport, `Resize` calls
   `SetWidth`/`SetHeight`). Unlike `LogTab` there is no ring buffer and no
   `tea.Tick` polling — content is set once at construction (or on an
   explicit reload) since the spool file is not expected to change while the
   window is open, and re-reading every render/tick would reintroduce the
   same "no cache, no staleness window" tradeoff `ReviewsTab.View()`
   deliberately documents but for a view with no reason to be dynamic.

3. **Opening the window (ReviewsTab → model)**: `ReviewsTab.Update` handles
   `"enter"` by looking up the currently selected `review.Record` (via
   `SelectedKey()` and a fresh `store.List()`), and returns a `tea.Cmd` that
   produces a new message type `openReviewContentMsg{repo string, pr int,
   spoolPath string}`. `ReviewsTab` has no reference to `TabManager` (by
   design, matching every other tab), so it cannot add a tab itself — the
   `tea.Cmd`/custom-`tea.Msg` round trip is the only mechanism available
   through the `Tab` interface's `Update(tea.Msg) (Tab, tea.Cmd)` signature,
   and it matches the idiomatic Bubble Tea pattern already used for
   asynchronous cross-cutting concerns in this codebase (`reviewStartMsg`,
   `reviewCompleteMsg`, `execDoneMsg` — all `tea.Cmd`-emitted messages
   handled in the top-level `model.Update` switch). The top-level
   `model.Update` in `tui.go` gets a new `case openReviewContentMsg:` arm
   that constructs the `ReviewContentTab` (reading the spool body via
   `review.ReadSpoolBody`), calls `m.tabManager.AddTab(...)`, and switches to
   it via the existing `m.switchActiveTab(len(m.tabManager.GetTabs())-1)`
   helper (the same sequence `commands.go`'s `log` command uses for
   `LogTab`).

   Note this is different from the "synchronous" pattern used by the `log`
   command (which runs inside `executeCommand`, itself invoked synchronously
   from the footer's Enter handling and can call `m.tabManager.AddTab`
   directly). Enter-on-a-tab is forwarded through `m.tabManager.Update(msg)`,
   which only returns a `tea.Cmd` — so the "open a tab" side effect here
   must travel through a message, not a direct call. `tea.Cmd` returning a
   message synchronously (no actual async work) is a normal, supported
   Bubble Tea idiom for "signal the parent" from a component that can't
   mutate parent state directly.

4. **Closing the window (ESC)**: `ReviewContentTab.IsClosable()` returns
   `true`. ESC is handled at the top level, matching the existing ESC
   precedence chain in `tui.go`'s `tea.KeyPressMsg` case (overlay dismissal
   first, then planning-tab-focus-to-footer). A new branch is added
   immediately after the planning-tab ESC branch and before the
   overlay-active input block:
   ```go
   if msg.String() == "esc" {
       activeTab := m.tabManager.GetActiveTab()
       if activeTab != nil && activeTab.Type() == TabTypeReviewContent {
           reviewsIdx := m.findReviewsTabIndex() // new small helper, see below
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
   `findReviewsTabIndex()` is a small new `model` method mirroring
   `TabManager.FindLogTab()`'s shape but returning the Reviews tab's index
   (the Reviews tab's ID is the fixed string `"reviews"`, already used at
   construction in `newModel`, so this can just scan `m.tabManager.GetTabs()`
   for `tab.Type() == TabTypeReviews`). Since `ReviewsTab.IsClosable()` is
   `false`, the Reviews tab is guaranteed to still exist, so the fallback
   `return m, nil` branch above is defensive only (mirrors the nil-check
   style already used throughout `tui.go`, e.g. `checkLogTabClosed`).

## New / modified files

| File | Change |
|---|---|
| `internal/review/spool.go` | Add `ReadSpoolBody`, refactor path resolution into shared `resolveSpoolPath` helper reused by `ReadSpoolInfo` |
| `internal/review/spool_test.go` | Add tests for `ReadSpoolBody` (see Test Plan) |
| `internal/tui/tabs.go` | Add `TabTypeReviewContent` to `TabType` enum + `String()` case |
| `internal/tui/review_content_tab.go` | **New.** `ReviewContentTab` struct + all `Tab` interface methods |
| `internal/tui/review_content_tab_test.go` | **New.** Unit tests for the new tab |
| `internal/tui/reviews_tab.go` | `Update()` gains `"enter"` handling; new `openReviewContentMsg` type (or place it in `tui.go` alongside sibling msg types — see Task 2) |
| `internal/tui/reviews_tab_test.go` | Add tests for Enter-key behavior (selected/empty/error cases) |
| `internal/tui/tui.go` | New `case openReviewContentMsg:` arm in `model.Update`; new ESC branch for `TabTypeReviewContent`; new `findReviewsTabIndex()` helper method |
| `internal/tui/enter_key_test.go` or new `internal/tui/review_content_window_test.go` | Integration-level test: Enter on Reviews tab (footer unfocused) opens the window; ESC closes it and returns to Reviews tab |

No changes to `internal/review/store.go`, `internal/review/types.go`,
`internal/tui/tab_manager.go` (its existing `AddTab`/`RemoveTab`/`CloseTab`
API is sufficient — no new TabManager method is needed), or `internal/tui/log_tab.go`.

## `internal/review` — body-reading function

```go
// resolveSpoolPath resolves spoolPath to the file that actually exists on
// disk, per the pending -> done fallback the external finalize pipeline
// uses. Returns (path, found, inDoneDir). Shared by ReadSpoolInfo and
// ReadSpoolBody so the two functions cannot drift on resolution logic.
//
//   - spoolPath == ""              -> ("", false, false), zero filesystem calls
//   - spoolPath exists              -> (spoolPath, true, false)
//   - derived done/ path exists      -> (donePath, true, true)
//   - neither exists                 -> ("", false, false)
func resolveSpoolPath(spoolPath string) (path string, found bool, inDoneDir bool) {
    if spoolPath == "" {
        return "", false, false
    }
    if _, err := os.Stat(spoolPath); err == nil {
        return spoolPath, true, false
    }
    if donePath := derivePendingToDone(spoolPath); donePath != "" {
        if _, err := os.Stat(donePath); err == nil {
            return donePath, true, true
        }
    }
    return "", false, false
}

// ReadSpoolBody reads and returns the markdown body of a spool file — the
// content after the closing "---" front-matter fence — along with the same
// SpoolInfo metadata ReadSpoolInfo returns, so a single call gives a caller
// (ReviewContentTab) everything needed to render both the window title
// (Verdict/DecisionState available on info) and the content (body).
//
// spoolPath is Record.SpoolPath (may be ""). homeDir is accepted for the
// same reason and with the same current no-op status as in ReadSpoolInfo
// (kept for signature symmetry and future relative-path resolution).
//
// Resolution mirrors ReadSpoolInfo exactly (via the shared resolveSpoolPath
// helper): pending path first, then its done/ counterpart.
//
// Return contract:
//   - spoolPath == "" or neither location exists:
//     body == "", info == SpoolInfo{Found: false, DecisionState: "no spool"}
//   - file exists but is unreadable (permission error, race where it's
//     deleted between resolveSpoolPath's Stat and the ReadFile):
//     body == "", info.Found == false, info.DecisionState == "no spool" —
//     ReviewContentTab surfaces this as a file-error message (AC6), not a
//     panic or an empty-but-"found" window
//   - file exists and is readable: info is populated exactly as
//     buildSpoolInfo already does (Verdict/Decision/DecisionState from
//     front-matter), and body is the substring after the closing "---"
//     line, with a single leading newline (if present, immediately after
//     the fence) trimmed, and otherwise returned verbatim — no further
//     markdown processing. If no front-matter fence is present at all
//     (ParseSpoolFrontMatter's "malformed/legacy" case), body is the
//     entire raw file content, matching ParseSpoolFrontMatter's own
//     degrade-gracefully-to-"treat as unstructured" contract.
func ReadSpoolBody(spoolPath string, homeDir string) (body string, info SpoolInfo) {
    _ = homeDir

    path, found, inDoneDir := resolveSpoolPath(spoolPath)
    if !found {
        return "", SpoolInfo{Found: false, DecisionState: ClassifySpoolState(false, false, "")}
    }

    data, err := os.ReadFile(path)
    if err != nil {
        return "", SpoolInfo{Found: false, DecisionState: ClassifySpoolState(false, false, "")}
    }

    info = buildSpoolInfo(data, inDoneDir)
    body = extractSpoolBody(data)
    return body, info
}

// extractSpoolBody returns the text following the closing "---" front-matter
// fence (see ParseSpoolFrontMatter for the fence-detection contract this
// mirrors). If the file has no opening "---" as its first non-empty-trimmed
// line, or no closing "---" is found, the entire raw content is returned
// unchanged (same degrade-gracefully rule ParseSpoolFrontMatter uses for its
// map return). A single leading "\n" immediately after the closing fence is
// trimmed so the body doesn't start with a blank line; nothing else about
// the body content is altered (no trimming trailing whitespace, no markdown
// rendering).
func extractSpoolBody(data []byte) string {
    // implementation splits on "\n", finds the opening "---" at index 0 and
    // the next literal "---" line, and joins everything after it; falls
    // back to string(data) if either fence is missing.
}
```

`ReadSpoolInfo` is left in place unchanged (call sites in `reviews_tab.go`'s
`resolveSpoolInfo` keep using it — that code path needs metadata only, on
every render, and does not need the body). `ReadSpoolInfo` is refactored
internally to call the new `resolveSpoolPath` helper instead of duplicating
the pending/done branching, but its exported signature and behavior are
unchanged (verified by the existing `spool_test.go` suite passing unmodified).

## `internal/tui` — `ReviewContentTab`

```go
// review_content_tab.go

package tui

import (
    "fmt"

    "charm.land/bubbles/v2/viewport"
    tea "charm.land/bubbletea/v2"

    "github.com/matthiashowellyopp/howmux/internal/review"
)

// ReviewContentTab implements the Tab interface for displaying the full,
// read-only markdown body of a single PR review's spool file. It is always
// closable and holds no reference back to ReviewsTab or TabManager — it is
// constructed with everything it needs (title text + body text) and knows
// nothing about how it was opened or how ESC will close it; that
// orchestration lives in model.Update (see openReviewContentMsg handling),
// matching the existing separation where tabs never reach back into the
// manager that holds them.
type ReviewContentTab struct {
    id       string
    title    string
    viewport viewport.Model
    width    int
    height   int

    // plainContent is the exact body text set at construction (or by a
    // future Reload), independent of any styling applied in View(). Mirrors
    // LogTab's separation between the styled viewport content and
    // CopyableContent()'s plain-text return.
    plainContent string

    // errMsg is set instead of plainContent when the spool file could not be
    // read (AC6); View() renders it via styles.Error, CopyableContent()
    // returns it verbatim. Mutually exclusive with a populated plainContent.
    errMsg string
    styles *Styles
}

// NewReviewContentTab creates a review content window tab. id should be
// unique per window (see openReviewContentMsg handling for the id scheme).
// title is the fully-formed display title (e.g. "Review: owner/repo #123"),
// already formatted by the caller so this constructor stays free of
// repo/PR-number formatting concerns.
//
// body is the spool body text (already resolved by the caller via
// review.ReadSpoolBody); found reports whether the spool file was located
// and read successfully. When found is false, the tab renders a
// file-error/missing message instead of body, per AC6 — callers pass found
// rather than an error value because the only failure mode ReadSpoolBody
// surfaces to a caller is "not found / unreadable", already collapsed into
// SpoolInfo.Found by that function.
func NewReviewContentTab(id, title, body string, found bool, styles *Styles) *ReviewContentTab {
    vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
    vp.MouseWheelEnabled = true

    rct := &ReviewContentTab{
        id:       id,
        title:    title,
        viewport: vp,
        styles:   styles,
    }

    if !found {
        rct.errMsg = fmt.Sprintf("Could not read review content for %s (spool file missing or unreadable).", title)
        vp.SetContent(styles.Error.Render(rct.errMsg))
        rct.viewport = vp
        return rct
    }

    rct.plainContent = body
    vp.SetContent(body)
    rct.viewport = vp
    return rct
}

func (rct *ReviewContentTab) ID() string       { return rct.id }
func (rct *ReviewContentTab) Type() TabType    { return TabTypeReviewContent }
func (rct *ReviewContentTab) Title() string    { return rct.title }
func (rct *ReviewContentTab) IsClosable() bool { return true }
func (rct *ReviewContentTab) View() string     { return rct.viewport.View() }

// CopyableContent returns the raw body text (or the error message if the
// spool could not be read), matching LogTab's contract of returning the true
// underlying buffer rather than the rendered/styled viewport content.
func (rct *ReviewContentTab) CopyableContent() string {
    if rct.errMsg != "" {
        return rct.errMsg
    }
    return rct.plainContent
}

// Update forwards key events to the viewport for scrolling, exactly as
// LogTab.Update does. This tab has no ring buffer, no tea.Tick polling, and
// no state that changes over time, so there is no equivalent of LogTab's
// TickMsg/refreshContent branch. ESC is intentionally NOT handled here —
// closing this tab requires removing it from TabManager and switching the
// active tab back to Reviews, neither of which this tab can do on its own
// (see Tab interface note above), so ESC is handled at the top level in
// model.Update, matching how TabTypeReviewContent's closability is already
// orchestrated externally.
func (rct *ReviewContentTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
    var cmd tea.Cmd
    if keyMsg, ok := msg.(tea.KeyMsg); ok {
        rct.viewport, cmd = rct.viewport.Update(keyMsg)
    }
    return rct, cmd
}

// Resize updates the tab dimensions, matching LogTab.Resize.
func (rct *ReviewContentTab) Resize(width, height int) {
    rct.width = width
    rct.height = height
    rct.viewport.SetWidth(width)
    rct.viewport.SetHeight(height)
}

// CaptureFocusState / RestoreFocusState: this tab has no internal focusable
// widget distinct from the footer (scrolling is driven by raw arrow/pgup/
// pgdown keys forwarded through Update, same as LogTab) — always footer,
// matching LogTab exactly.
func (rct *ReviewContentTab) CaptureFocusState() FocusTarget { return FocusTargetFooter }
func (rct *ReviewContentTab) RestoreFocusState(target FocusTarget) tea.Cmd { return nil }
```

### `tabs.go` change

```go
const (
    TabTypeMain TabType = iota
    TabTypeAgent
    TabTypePlanning
    TabTypeLog
    TabTypeReviews
    TabTypeReviewContent // new
)
```
Add the matching `case TabTypeReviewContent: return "ReviewContent"` in
`TabType.String()`.

## Message-passing mechanism

### Opening: `ReviewsTab.Update` → `openReviewContentMsg` → `model.Update`

```go
// In reviews_tab.go (or tui.go, next to reviewStartMsg/reviewCompleteMsg —
// place it in tui.go since it is consumed there and every existing sibling
// cross-tab message lives there too; ReviewsTab only needs to construct and
// return it, it does not need to own the type):
type openReviewContentMsg struct {
    repo      string
    pr        int
    spoolPath string
}
```

`ReviewsTab.Update` gains an `"enter"` case:

```go
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
    case "enter":
        return rt, rt.openSelectedReviewCmd()
    }
    return rt, nil
}

// openSelectedReviewCmd returns a tea.Cmd that emits openReviewContentMsg
// for the currently selected PR, or nil if nothing is selected or the
// store read fails (AC6 partially handled here: a store-level failure
// degrades to "no-op" rather than opening a broken window; a spool-level
// failure — file missing/unreadable — is handled downstream in model.Update
// via ReadSpoolBody's found==false path, since only the spool path is known
// here, not whether that file is actually readable).
func (rt *ReviewsTab) openSelectedReviewCmd() tea.Cmd {
    if rt.selectedKey == "" {
        return nil
    }
    records, err := rt.store.List()
    if err != nil {
        return nil
    }
    for _, rec := range records {
        if recordKey(rec) == rt.selectedKey {
            msg := openReviewContentMsg{repo: rec.Repo, pr: rec.PR, spoolPath: rec.SpoolPath}
            return func() tea.Msg { return msg }
        }
    }
    return nil
}
```

`model.Update` in `tui.go` gains a new case (placed alongside the other
review-related cases, e.g. right after `reviewCompleteMsg`):

```go
case openReviewContentMsg:
    homeDir, err := userHomeDirFunc()
    if err != nil {
        homeDir = ""
    }
    body, info := review.ReadSpoolBody(msg.spoolPath, homeDir)
    title := fmt.Sprintf("Review: %s #%d", msg.repo, msg.pr)
    id := fmt.Sprintf("review-content-%s-%d", msg.repo, msg.pr)

    // Reuse an already-open window for the same PR instead of stacking
    // duplicate tabs if the user presses Enter again on the same row.
    if existingIdx := m.findTabByID(id); existingIdx >= 0 {
        var cmd tea.Cmd
        m, cmd = m.switchActiveTab(existingIdx)
        return m, cmd
    }

    contentTab := NewReviewContentTab(id, title, body, info.Found, m.styles)
    m.tabManager.AddTab(contentTab)
    var cmd tea.Cmd
    m, cmd = m.switchActiveTab(len(m.tabManager.GetTabs()) - 1)
    return m, cmd
```

`findTabByID` is a small new one-line helper on `model`/`TabManager` (does
not currently exist — `TabManager` has `FindTabByAgentID` and `FindLogTab`
but no generic by-ID lookup). Add it to `tab_manager.go`:

```go
// FindTabByID returns the index of the tab with the given ID, or -1 if not found.
func (tm *TabManager) FindTabByID(id string) int {
    for i, tab := range tm.tabs {
        if tab.ID() == id {
            return i
        }
    }
    return -1
}
```
and call it as `m.tabManager.FindTabByID(id)` in the `openReviewContentMsg`
case above (the spec text above used `m.findTabByID` informally; the actual
call site is `m.tabManager.FindTabByID(id)`).

### Closing: ESC → `model.Update` → `TabManager.CloseTab` + `switchActiveTab`

Add a new `model` helper in `tui.go` (near `checkLogTabClosed`):

```go
// findReviewsTabIndex returns the index of the permanent Reviews tab. The
// Reviews tab is always present (IsClosable() == false, added once in
// newModel), so -1 is only a defensive fallback that should not occur in
// practice.
func (m model) findReviewsTabIndex() int {
    for i, tab := range m.tabManager.GetTabs() {
        if tab.Type() == TabTypeReviews {
            return i
        }
    }
    return -1
}
```

And the new ESC branch in the `tea.KeyPressMsg` case, inserted immediately
after the existing planning-tab ESC branch and before the
`m.activeOverlay == overlayStatus` number-key block (so it takes priority
over the generic "block other input when overlay is active" fallthrough,
consistent with how the planning-tab ESC branch is already positioned ahead
of that same block):

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

No new message type is needed for closing — ESC is a direct key handled
entirely in the top-level `model.Update`, exactly like the existing
`ctrl+w` → `CloseCurrentTab()` handling, which is why this path does not
need a `tea.Cmd` round trip: `model.Update` already has direct access to
`m.tabManager`.

## Concurrency analysis

No new cross-goroutine shared state. `ReadSpoolBody` performs a single
synchronous, blocking file read on the main event-loop goroutine — exactly
the same execution context `resolveSpoolInfo`/`ReadSpoolInfo` already run in in
`ReviewsTab.View()`. `ReviewContentTab` holds no reference to the review
store, no background poller, and no shared mutable state with the watcher
goroutine or agent manager. No lock-guarded accessor is introduced. This
change does not cross any of the documented concurrency boundaries
(TUI render loop, command handlers, watcher poll loop, agent manager).

## Test plan

### `internal/review/spool_test.go` (extend existing file, same style as
existing `TestReadSpoolInfo*` tests — table/subtest per scenario, `t.TempDir()`
+ `os.MkdirAll`/`os.WriteFile` fixtures, no mocking of the filesystem)

- `TestReadSpoolBodyPendingFileReturnsBodyAfterFrontMatter` — writes a spool
  file with front-matter + body in `pending/`, asserts returned `body` equals
  the text after the closing fence and `info.Found == true`.
- `TestReadSpoolBodyFallsBackToDoneDir` — pending path missing, done/
  counterpart present; asserts `body` comes from the done file and
  `info.InDoneDir == true` (mirrors `TestReadSpoolInfo...DoneDir` if such a
  test exists — verify by name during implementation and mirror its exact
  fixture setup).
- `TestReadSpoolBodyMissingFileReturnsNotFound` — neither path exists;
  asserts `body == ""`, `info == SpoolInfo{Found: false, DecisionState: "no
  spool"}` (mirrors `TestReadSpoolInfoMissingFile...`).
- `TestReadSpoolBodyEmptySpoolPathReturnsNotFoundWithoutFilesystemAccess` —
  `spoolPath == ""`; assert same not-found result as above, and (if
  feasible) that no file I/O occurred (can piggyback on the existing pattern
  used for `ReadSpoolInfo("")` if one exists).
- `TestReadSpoolBodyNoFrontMatterFenceReturnsWholeFileAsBody` — file exists
  but does not start with `---`; asserts `body == string(rawContent)`
  (the ParseSpoolFrontMatter degrade-gracefully case) and `info.Found ==
  true` with all metadata fields empty except `DecisionState == "pending"`
  (per `ClassifySpoolState(true, false, "")`).
- `TestReadSpoolBodyTrimsSingleLeadingNewlineAfterFence` — front-matter
  closing fence immediately followed by `\n# Review\n...`; asserts body
  starts with `# Review`, not `\n# Review`.
- `TestReadSpoolBodyUnreadableFileDegradesToNotFound` — simulate via a
  path that Stat succeeds on then becomes unreadable, OR (simpler,
  deterministic) a directory placed at the expected path so `os.ReadFile`
  fails with an error distinct from not-exist; asserts graceful `Found:
  false` rather than a panic or propagated error (matches
  `TestReadSpoolInfoUnparsablePendingFileDoesNotErrorOrPanic`'s spirit).
- Existing `TestReadSpoolInfo*` tests must continue to pass unmodified after
  the `resolveSpoolPath` refactor — run the full existing suite as a
  regression check, not just the new tests.

### `internal/tui/review_content_tab.go` — new `internal/tui/review_content_tab_test.go`

Follow `log_tab.go`'s test conventions where a comparable test file exists in
this package (check for one during implementation; if none exists, follow
`reviews_tab_test.go`'s style: table-driven where natural, `testReviewsStyles()`-
equivalent `NewStyles(createMinimalTheme())` helper, no real terminal).

- `TestReviewContentTabImplementsTabInterface` — `var _ Tab =
  (*ReviewContentTab)(nil)` compile-time assertion, mirroring the intent of
  `tabs_interface_test.go`'s `mockTab`.
- `TestReviewContentTabTitleReflectsRepoAndPR` — construct with a title
  string, assert `Title()` returns it verbatim (title formatting itself is
  the caller's responsibility per the constructor's documented contract —
  test the caller-side formatting in the `tui.go` integration test instead).
- `TestReviewContentTabViewRendersBodyContent` — construct with
  `found=true` and a known body string, assert `View()` contains the body
  text (via viewport; may need to `Resize` first so the viewport has
  non-zero dimensions before `View()` is meaningful — mirror how
  `log_tab.go`'s own tests, if any exist, size the viewport before
  asserting on `View()`).
- `TestReviewContentTabViewRendersErrorWhenNotFound` — construct with
  `found=false`, assert `View()` contains the missing/unreadable message
  and `CopyableContent()` returns the same error text.
- `TestReviewContentTabCopyableContentReturnsPlainBody` — construct with
  `found=true`, assert `CopyableContent()` equals the exact body passed in
  (no ANSI codes — this is the plain-text contract every other tab's
  `CopyableContent()` upholds).
- `TestReviewContentTabIsClosable` — assert `IsClosable() == true`.
- `TestReviewContentTabUpdateForwardsKeysToViewportForScrolling` —
  construct with body long enough to require scrolling once resized small,
  send `down`/`pgdown` key messages, assert the viewport's `YOffset()`
  changes (mirrors how `LogTab`'s scrolling could be tested, adapted since
  there is no existing `log_tab_test.go` cited in the directory listing —
  confirm during implementation whether one exists elsewhere; if not, this
  is the first direct viewport-scroll test in the package and should assert
  via `viewport.Model`'s exported `YOffset()`/`TotalLineCount()`, consistent
  with how `log_tab.go` itself reads `YOffset()`/`TotalLineCount()`
  internally for `isNearBottom()`).
- `TestReviewContentTabCaptureAndRestoreFocusStateAlwaysFooter` — assert
  `CaptureFocusState() == FocusTargetFooter` and `RestoreFocusState(...)  ==
  nil`, mirroring `LogTab`'s equivalent behavior/tests if present.
- `TestReviewContentTabResizeUpdatesViewportDimensions` — call `Resize`,
  assert subsequent `View()` output reflects the new width/height (or that
  no panic occurs with 0/negative dimensions, matching defensive patterns
  elsewhere in the package, e.g. `ReviewsTab.padToHeight`'s `height <= 0`
  guard).

### `internal/tui/reviews_tab_test.go` (extend existing file)

- `TestReviewsTabEnterOnSelectedRowReturnsOpenReviewContentCmd` — build a
  `fakeReviewStore` with records, select a row (via `down`), send
  `tea.KeyPressMsg{Code: tea.KeyEnter}` (matching the existing
  `TestKeyPressMsg{Code: tea.KeyEnter}` convention already used in this
  file's "non-key message and unrecognized key are no-ops" subtest), assert
  the returned `tea.Cmd` is non-nil, invoke it, and assert the resulting
  `tea.Msg` is an `openReviewContentMsg` with the expected `repo`/`pr`/
  `spoolPath` matching the selected record.
- `TestReviewsTabEnterWithNoSelectionReturnsNilCmd` — empty store (mirrors
  the existing "empty store does not panic and disables selection" fixture),
  send Enter, assert `cmd == nil`.
- `TestReviewsTabEnterWithStoreListErrorReturnsNilCmd` — `fakeReviewStore{
  listErr: errors.New(...)}` (the fake already supports `listErr` per the
  existing struct), send Enter, assert `cmd == nil` (no panic, no message
  emitted for a PR that can't be re-resolved).
- Update `TestReviewsTabCursorPersistsAcrossViewCalls`-style existing tests
  only if adding the `"enter"` case changes behavior for the keys they
  already exercise — expected to require no changes, since `"enter"` is a
  new `case`, not a modification of `"up"`/`"down"`.

### `internal/tui/tui.go` — integration-level tests

Add to `internal/tui/enter_key_test.go` (natural home given its existing
`TestEnterKeyCommandExecutionInAllTabs`/`TestEnterKeyFooterFocusPriority`
scope) or a new `internal/tui/review_content_window_test.go` if the file
would grow unwieldy — decide based on final line count during
implementation; prefer the new file if it exceeds ~150 added lines to keep
`enter_key_test.go` focused on its existing "footer focus priority" scope.

- `TestEnterOnReviewsTabRowOpensReviewContentTab` — build a `model` via
  `createTestModelWithTab(t, TabTypeReviews)` (verify this helper supports
  seeding a fake/real store with at least one record with a valid
  `SpoolPath` pointing at a `t.TempDir()` fixture file — extend the helper
  if it currently only supports empty stores; check its signature during
  implementation), send `down` (to establish a selection, mirroring the
  existing `ReviewsTab` cursor tests) then `tea.KeyPressMsg{Code:
  tea.KeyEnter}` through `model.Update` with the footer unfocused (mirroring
  `TestEnterKeyCommandExecutionInAllTabs`'s footer-unfocused row), and assert
  the active tab afterward has `Type() == TabTypeReviewContent` and its
  `Title()` matches `"Review: <repo> #<pr>"`.
- `TestEnterOnReviewsTabWithMissingSpoolFileOpensErrorWindow` — same as
  above but with a record whose `SpoolPath` points at a nonexistent file;
  assert the opened tab's `View()`/`CopyableContent()` contains the
  file-error message (AC6), not a panic or empty window.
- `TestEscOnReviewContentTabClosesAndReturnsToReviewsTab` — from the
  state produced by the first test above, send `tea.KeyPressMsg{Code:
  tea.KeyEscape}` (check the exact zero-value/constant used elsewhere for
  ESC in this file's existing tests, e.g. how the planning-tab ESC test
  constructs its key message, and mirror it), assert the active tab is now
  the Reviews tab (`Type() == TabTypeReviews`) and
  `m.tabManager.FindTabByID(<content-tab-id>)` (or equivalent) confirms the
  content tab was actually removed, not merely deactivated.
- `TestEnterOnReviewsTabTwiceForSamePRReusesExistingWindow` — send Enter
  twice for the same selected row without closing in between; assert only
  one `ReviewContentTab` exists in `m.tabManager.GetTabs()` afterward (tests
  the `FindTabByID` reuse branch in the `openReviewContentMsg` handler).
- Extend `TestEnterKeyCommandExecutionInAllTabs`'s table with a
  `{"reviews tab footer focused", TabTypeReviews, true, "help", true}` case
  only if `createTestModelWithTab(t, TabTypeReviews)` doesn't already behave
  correctly for that generic case — verify first; likely no change needed
  since footer-focused Enter is unaffected by this feature (it still routes
  to `executeCommand`).

## Validation commands

```bash
# Build
go build ./...

# Full test suite (must stay green — this is an additive change, no existing
# behavior should regress)
go test ./...

# Targeted packages for this change
go test ./internal/review/... -run TestReadSpoolBody -v
go test ./internal/tui/... -run 'TestReviewContentTab|TestReviewsTabEnter|TestEnterOnReviewsTab|TestEscOnReviewContentTab' -v

# Race detector — no new goroutines/shared state introduced, but run
# per the project's existing convention for any TUI change
go test -race ./internal/tui/... ./internal/review/...

# Vet / lint (per whatever discover-qa-tools finds configured for this repo)
go vet ./...
```

## Task breakdown (for parallel builder sub-tasks)

All tasks contribute to one PR. Tasks 1 and 2 have no dependency on each
other and can run in parallel; Task 3 depends on both; Task 4 (tests) can be
written in parallel with Tasks 1–3 against the signatures specified above,
then wired up once the real implementations land.

### Task 1: `internal/review.ReadSpoolBody` + `resolveSpoolPath` refactor
**Acceptance criteria:**
- `resolveSpoolPath(spoolPath string) (path string, found bool, inDoneDir bool)` added to `spool.go`, implementing the pending→done resolution order currently inlined in `ReadSpoolInfo`.
- `ReadSpoolInfo` refactored to call `resolveSpoolPath` instead of its own inline `os.ReadFile`-based branching; its exported signature, return values, and documented behavior are unchanged.
- `ReadSpoolBody(spoolPath, homeDir string) (body string, info SpoolInfo)` added, using `resolveSpoolPath` + `buildSpoolInfo` + a new `extractSpoolBody(data []byte) string` helper.
- `extractSpoolBody` handles: normal front-matter-then-body, no-front-matter-fence (returns whole file), and closing-fence-with-no-body (returns "").
- All existing `TestReadSpoolInfo*` tests in `spool_test.go` pass unmodified.
- New tests listed under "Test plan" above are added and pass.
- **Dependencies:** None.

### Task 2: `ReviewContentTab` + `TabTypeReviewContent`
**Acceptance criteria:**
- `TabTypeReviewContent` added to the `TabType` enum in `tabs.go`, with a `String()` case.
- `internal/tui/review_content_tab.go` created with `ReviewContentTab` implementing every method of the `Tab` interface, per the struct/method signatures specified above.
- `var _ Tab = (*ReviewContentTab)(nil)` compiles.
- Viewport construction, `MouseWheelEnabled`, `Update`/`Resize` forwarding follow `LogTab`'s pattern exactly (no ring buffer, no `tea.Tick`).
- `CopyableContent()` returns the plain body (or error message) independent of any styling in `View()`.
- Constructor takes a pre-resolved `body string, found bool` (does not call into `internal/review` itself — keeps this tab a pure presentation type, consistent with every other `Tab` implementation not owning its own data source directly... except `ReviewsTab`, which does own its store reference; this tab intentionally does NOT follow that precedent, since its content is a one-shot snapshot rather than a live, repeatedly-`View()`-refreshed read).
- New tests listed under "Test plan" above are added and pass.
- **Dependencies:** None (can be developed against the `ReadSpoolBody` signature as a contract before Task 1 lands; only needs `review.SpoolInfo`, which already exists).

### Task 3: Wire Enter (open) and ESC (close) through `ReviewsTab` and `model.Update`
**Acceptance criteria:**
- `ReviewsTab.Update` gains a `case "enter"` that returns a `tea.Cmd` emitting `openReviewContentMsg{repo, pr, spoolPath}` for the selected record, or `nil` if nothing is selected or `store.List()` errors.
- `openReviewContentMsg` type defined in `tui.go`.
- `model.Update` gains a `case openReviewContentMsg:` that calls `review.ReadSpoolBody`, builds the title `"Review: <repo> #<pr>"`, checks `m.tabManager.FindTabByID(id)` for an existing window before creating a new one, otherwise constructs `NewReviewContentTab(...)`, adds it via `m.tabManager.AddTab`, and switches to it via `m.switchActiveTab`.
- `TabManager.FindTabByID(id string) int` added to `tab_manager.go`.
- A new ESC branch in the `tea.KeyPressMsg` case closes the active tab when its `Type() == TabTypeReviewContent` via `m.tabManager.CloseTab(m.tabManager.GetActiveTabIndex())`, then switches back to the Reviews tab via a new `m.findReviewsTabIndex()` helper + `m.switchActiveTab`.
- The new ESC branch is positioned so it takes priority in the same way the existing planning-tab ESC branch does (before the overlay-active input block), and does not change behavior for any other tab type or overlay state.
- New/extended tests listed under "Test plan" (`reviews_tab_test.go` Enter cases, `tui.go` integration tests) pass.
- **Dependencies:** Task 1 (needs `ReadSpoolBody`'s real signature) and Task 2 (needs `NewReviewContentTab`'s real constructor) — this task is the integration glue and should be implemented last, once both are available (or against the frozen signatures in this spec, then adjusted if either changes during review).

### Task 4: Test coverage pass and full-suite regression check
**Acceptance criteria:**
- Every test enumerated in the "Test plan" section above exists and passes.
- `go test ./... ` passes with no regressions in currently-passing tests.
- `go test -race ./internal/tui/... ./internal/review/...` passes.
- `go vet ./...` passes.
- **Dependencies:** Tasks 1–3 (writes/extends tests against their real implementations; may start earlier by writing tests against the specified signatures/stubs, but final pass requires the real code).

## Concurrency note (per architect guidelines)

This issue does not introduce a concurrency concern under the detection
rules in the architect's operating instructions: no new goroutine reads or
writes a lock-guarded field, no background service (watcher, agent manager)
state is newly wired into the render path, and no command handler here reads
or writes state mutated by a background goroutine. `ReadSpoolBody` is a
synchronous, single-goroutine file read invoked directly from
`model.Update` on the main event-loop goroutine, identical in execution
context to the existing `resolveSpoolInfo` calls in `ReviewsTab.View()`. No
concurrent test is required for this change.
