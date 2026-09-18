# Design Spec: Finalize confirmation — co-locate prompt, stop silent-cancel

Closes #102

## Problem Recap

`finalizeState == finalizeAwaitingConfirmation` has two compounding defects
in `internal/tui/tui.go`:

1. The `post live? (y/N)` prompt (and all finalize status lines) are
   appended to the Main TUI activity log via `m.appendActivity`, while the
   dry-run output the user needs to read lives in the separate "Finalize
   Preview" `ReviewContentTab` (`finalizeWindowTabID`, opened by
   `openFinalizePreviewWindow` in `commands.go`). The prompt is invisible
   from the tab it tells the user to review.
2. The confirm-gate switch at `tui.go:1204` treats anything other than
   `y`/`yes` as cancel (the `default:` arm, `tui.go:~1246`). Navigation
   keys (`[`, `]`, `F2`, arrows) used to actually get to the preview tab hit
   this `default:` arm and silently cancel — and the cancel line itself
   only appears on Main TUI, so the user doesn't see it happen.

There is also a **third, related defect** discovered while tracing the
`Esc` handling for this spec (not explicitly called out in the issue body,
but directly blocks acceptance criterion 2 for the `Esc` case): the
generic `TabTypeReviewContent` `Esc`-closes-window handler
(`tui.go:~1133`, "Priority handling for closing a review content window")
runs *before* the finalize confirm-gate block (`tui.go:1204`) in
`model.Update`. Today, while `finalizeState == finalizeAwaitingConfirmation`
and the Finalize Preview tab is the active tab, pressing `Esc` matches the
`TabTypeReviewContent` branch first and closes the window — it never reaches
the confirm-gate's `Esc`-cancels-explicitly logic at all. This must be fixed
as part of this issue: the confirm-gate check must run before the generic
`TabTypeReviewContent` `Esc` handler, or `Esc` will close the preview window
instead of cancelling finalize whenever the preview tab happens to be
focused — silently defeating AC2's `Esc` → cancel requirement in exactly
the situation (preview tab focused) this issue is about co-locating for.

## Solution Approach

### 1. Confirm-gate key handling (tui.go:1204 block)

Replace the binary `y`/`yes` vs. everything-else switch with an explicit
three-way branch:

- `y` / `yes` → post live (existing behavior, unchanged).
- `n` / `N` / `esc` → cancel explicitly (existing "nothing was posted"
  message, unchanged wording).
- Anything else → **return `m, nil` with no state change and no activity
  line.** This is the core fix for the silent-cancel trap.

`msg.String()` on a `tea.KeyPressMsg` already normalizes case for letter
keys the same way it does today (the existing code lowercases via
`strings.ToLower(strings.TrimSpace(msg.String()))`), so `"N"` and `"n"`
both need to reach the cancel branch — verify this by checking the string
against `"n"` post-lowering (matches existing normalization) OR against
`"N"` pre-lowering; simplest is to keep lowering and check `"n"`, since
`strings.ToLower` already collapses `"N"` → `"n"` before the switch runs
(no separate `"N"` case needed, exactly mirroring how `"yes"` and `"y"`
already collapse together today).

`msg.String()` for the Escape key is `"esc"` (confirmed by existing esc
checks elsewhere in this file, e.g. `tui.go:1110`, `:1117`, `:1133`), so
`"esc"` becomes a second literal in the cancel case alongside `"n"`.

### 2. Move the confirm-gate block ahead of the `TabTypeReviewContent` Esc handler

The finalize confirm-gate block (currently at `tui.go:1204`, immediately
after the "Block other input when overlay is active" check) must be
relocated to run **before** the three `esc`-handling blocks around
`tui.go:1108–1143` (overlay dismissal, planning-tab focus return, and
`TabTypeReviewContent` window-close). Concretely: move the entire
`if m.finalizeState == finalizeAwaitingConfirmation { ... }` block (and its
preceding comment) to immediately after the `ctrl+y` copy handler
(`tui.go:~1104`) and before the "Priority handling for overlay dismissal"
comment (`tui.go:~1108`).

This preserves the documented mutual-exclusion invariant with
`m.confirmingExit` (that block stays where it is, still after the moved
finalize block, still checked next) and is the *only* reordering needed:
none of the blocks between the old and new position
(`activeOverlay != overlayNone && esc`, planning-focus-esc,
`TabTypeReviewContent`-esc, status-overlay number keys, generic
overlay-block) can currently fire while `finalizeAwaitingConfirmation` is
true in practice — but their being reachable in principle is exactly the
bug this move closes for the `TabTypeReviewContent` case, and moving the
whole gate up is simpler and more robust than special-casing just the one
interaction.

After the move, verify (and adjust the comment) that the block's own
"must be checked before `m.confirmingExit`'s block AND before Reviews-tab
row-shortcut forwarding" note still holds — it does, since `confirmingExit`
remains strictly after this block's new position too.

### 3. Co-locate the prompt with the preview window (footer status-row overlay, not the activity log)

Use the **existing `FooterManager.SetTransientMessage` mechanism** — the
same one `notesEditActive` already uses (`tui.go:787`,
`"Editing notes for %s #%d (Enter to save, Esc to cancel)"`) to show a
persistent, state-scoped hint in the footer's status row regardless of
which tab is active. This is preferred over a per-tab footer/overlay
inside `ReviewContentTab` itself for three reasons:

- `SetTransientMessage` is already the codebase's established pattern for
  "a mode is active; show its prompt in the footer status row until the
  mode ends" (notes-edit uses exactly this shape).
  `renderStatusRow` (`footer.go`) already prioritizes
  `fm.transientMessage` over all other status-row content when set, so it
  is visible on *every* tab, which satisfies (and exceeds) the acceptance
  criterion "visible in/at the Finalize Preview window... without
  switching to Main TUI" — it is visible on literally any tab, including
  the Finalize Preview tab, without requiring the user to first navigate
  there.
- `ReviewContentTab` (`review_content_tab.go`) is explicitly documented as
  holding "no reference back to ReviewsTab or TabManager" and knowing
  "nothing about how it was opened" — adding finalize-specific prompt
  rendering logic into this generically-reused tab type (also used by the
  unrelated #84 static-review-body window) would violate that documented
  separation and couple a generic tab type to one specific caller's
  workflow state.
- It requires no new rendering path, no new field on `ReviewContentTab`,
  and no changes to `Resize`/`View`/`setWrappedContent` — the mechanism,
  styling (`styles.ThemeLabel`), and layout slot (footer status row, always
  visible, `GetFooterHeight()` already accounts for it) all already exist
  and are exercised by existing tests.

Additionally — per the issue's "ideally the finalize status: running /
complete / cancelled / failed" wish — route ALL finalize status lines
that currently go only to the activity log through
`SetTransientMessage` as well, using the *same* co-location rationale,
while **keeping** the existing `appendActivity` calls unchanged (Main TUI
users/tests still see the history there; this is additive, not a
replacement). Concretely:

| Event | File:line | Existing activity line (unchanged) | New transient message |
|---|---|---|---|
| Dry-run complete → awaiting confirmation | `tui.go:613` | "Dry-run complete — review the preview window, then post live? (y/N)" | `"Dry-run complete — post live? (y/N)"` |
| Live posting started | `tui.go:1229` (after move, same content) | "Posting live via finalize-reviews.sh..." | `"Posting live via finalize-reviews.sh..."` |
| Live complete → idle | `tui.go:628` | "Finalize complete — spool drained." | `""` (clear) |
| Cancelled → idle | `tui.go:~1246` (after move) | "Live posting cancelled — nothing was posted." | `""` (clear) |
| Dry-run or live error → idle | `tui.go:651` | "Finalize (%s) failed: %v" | `""` (clear) |

The transient message must be **cleared** (`SetTransientMessage("")`)
whenever `finalizeState` returns to `finalizeIdle` — on live success, on
cancel, and on either error path — so a stale "post live? (y/N)" prompt
never lingers in the footer after the workflow has actually ended. This
mirrors the existing notes-edit pattern's own two clear sites (Enter-save
and Esc-cancel, `tui.go:987`/`:1288`/`:1310`).

Do **not** clear the transient message when transitioning
`finalizeDryRunRunning`/`finalizeLiveRunning` → their respective completion
messages happen in the same case arm that sets the *next* transient
message, so there is no intermediate empty-message frame — set the new
message and let it naturally be the only mutation needed (matches how
`finalizeState` itself is set once per case arm).

**Why not a modal/overlay anchored to the preview tab** (the other option
the issue raises for the builder to weigh): the codebase's actual modal
pattern (`m.activeOverlay`, `renderOverlay`/`layerOverlay`) is used for
things that block all other interaction (autocomplete menu, status
overlay) — using it here would mean the confirm-gate's "ignore
navigation, don't block scrolling/tab-switching" requirement (AC1) now has
to fight the overlay system's existing "block other input when overlay is
active" check at `tui.go:~1146`, which runs `return m, nil` unconditionally
whenever `activeOverlay != overlayNone`. Routing through `activeOverlay`
would require carving out a finalize-specific exception in that generic
block, adding coupling for no benefit the footer-status-row approach
doesn't already provide more simply.

### 4. Preview-window content check (AC3, AC5)

The acceptance criteria and tests require the prompt text to be checkable
"in/at the Finalize Preview window" and "in the preview window's
content/footer." Since Section 3 places the prompt in the **global footer
status row** (visible while the Finalize Preview tab is active, among
others), the test-observable claim is: *while the Finalize Preview tab is
the active tab, `m.footerManager`'s rendered status row (or
`m.footerManager.RenderWithSeparator(TabTypeReviewContent)`) contains the
prompt text.* This satisfies AC3/AC5 literally — the text is visible
"at" the preview window (in its own rendered footer, since the footer is
part of what's on screen under that tab) without requiring any new state
on `ReviewContentTab` itself. Tests should assert against
`m.footerManager`'s render output (or the transient-message field
directly, see Task 4 below) rather than `ReviewContentTab.View()` /
`CopyableContent()`, since the prompt is deliberately not part of the
tab's own content buffer.

## Relevant Files

| File | Change |
|---|---|
| `internal/tui/tui.go` | Move + rewrite the confirm-gate block (key handling); add `SetTransientMessage` calls at all five status transitions in the `finalizeDryRunMsg`/`finalizeCompleteMsg`/`finalizeErrorMsg` case arms and the confirm-gate's `y`/cancel arms |
| `internal/tui/finalize_integration_test.go` | Add/extend tests for AC1 (navigation-key no-op), AC2 (`n`/`N`/`Esc` cancel, `y` posts), AC3/AC5 (transient message content at each transition) |
| `internal/tui/finalize_test.go` | No change expected (pure-function tests unaffected by key-handling/prompt-placement changes) |
| `internal/tui/review_content_tab.go` | No change — confirmed not to need modification; documented here so the builder does not attempt to add prompt rendering to this type (see Section 3 rationale) |
| `internal/tui/commands.go` | No change — `openFinalizePreviewWindow`/`handleFinalize` already correctly wire the shared capture/window; this issue only changes what happens after `finalizeDryRunMsg` arrives |

## Concurrency Analysis

**No new concurrency concern.** All changes in this issue touch only
fields and methods already documented as "mutated only inside
model.Update... never inside the runFinalizeCmd or pollFinalizeOutputCmd
goroutine closures" (`tui.go:246`): `m.finalizeState`, `m.finalizeCancel`,
and now also `m.footerManager.transientMessage` via
`SetTransientMessage`. `FooterManager` has no mutex and is never accessed
from `runFinalizeCmd`'s or `pollFinalizeOutputCmd`'s goroutine closures —
both of those only ever return a `tea.Msg` for Bubble Tea's single Update
goroutine to consume, exactly like every other field this block already
touches. `SetTransientMessage` is already called from `model.Update` at
three other sites (`tui.go:472`, `:787`, `:987`) with no locking, so this
issue introduces no new pattern and no new goroutine boundary. No
`sync.Mutex`/`sync.RWMutex` accessor is required, and no concurrent
`-race` test is needed for this specific mutation (distinct from the
already-existing `TestOutputCapture_ConcurrentWriterAndReader`, which
covers the actual cross-goroutine boundary in this feature —
`OutputCapture` — and is unaffected by this change).

## Team Orchestration

All tasks touch `tui.go` sequentially (same file, same function), so they
are **not parallelizable** among themselves — implement in the order
below. Task 4 (tests) depends on Tasks 1–3 being complete. This is a
single builder's worth of sequential work; no multi-builder split is
warranted for a change this contained.

## Step-by-Step Task Breakdown

### Task 1: Relocate the finalize confirm-gate block ahead of the Esc-closes-window handler

**File:** `internal/tui/tui.go`

Move the entire block starting at the comment `// Finalize
confirmation-keypress interception (issue #87): ...` through the closing
`}` of `if m.finalizeState == finalizeAwaitingConfirmation { ... }`
(currently `tui.go:~1188`–`~1250`) to a new position **immediately after**
the `ctrl+y` copy-handling block (ends `tui.go:~1105`) and **immediately
before** the comment `// Priority handling for overlay dismissal`
(`tui.go:~1108`).

Do not change the block's internal logic yet (that is Task 2) — this task
is a pure relocation so the diff is easy to review as "moved, then
edited" rather than one large tangled change.

Update the block's own comment to reflect the new ordering guarantee: it
now must additionally be documented as running before the
`TabTypeReviewContent`-closes-on-Esc handler, not just before
`m.confirmingExit` and Reviews-tab row-shortcut forwarding. Add one
sentence noting *why*: without this, `Esc` while the Finalize Preview tab
is active and `finalizeAwaitingConfirmation` is true would close the
window via the generic handler instead of cancelling finalize.

**Acceptance criteria:**
- The block appears before `tui.go`'s three `esc`-handling blocks
  (overlay dismissal, planning-tab focus, `TabTypeReviewContent` close)
  and before the `m.confirmingExit` block.
- No behavioral change from this task alone — `go build ./...` succeeds
  and all pre-existing finalize tests in `finalize_integration_test.go`
  still pass unmodified (they only assert on `m.finalizeState` and
  activity lines, not on block ordering).

**Dependencies:** None.

---

### Task 2: Rewrite the confirm-gate key-handling switch

**File:** `internal/tui/tui.go` (the block relocated in Task 1)

Change the `switch input := strings.ToLower(strings.TrimSpace(msg.String()))`
statement's cases from the current two-arm shape (`case "y", "yes":` /
`default:`) to a three-arm shape:

```go
switch input {
case "y", "yes":
    // ... existing live-run-launch logic, unchanged ...
case "n", "no", "esc":
    // ... existing cancel logic, unchanged (finalizeIdle, cancel
    // finalizeCancel, "Live posting cancelled — nothing was posted."
    // activity line) ...
default:
    // Navigation/other key while awaiting confirmation: ignore
    // entirely. No state change, no activity line — this is the fix
    // for the silent-cancel-on-navigation trap (issue #102). Return
    // early so the keypress does NOT fall through to any handler
    // below (tab switching, scrolling, etc. must still work via their
    // own normal paths — this early-return only prevents this
    // specific keypress from being reinterpreted as a decision or
    // cancel; it does not swallow the keypress's normal tab/viewport
    // effect, which happens via the natural fallthrough further down
    // in Update for keys this switch doesn't return early on).
    return m, nil
}
```

Note on `"no"`: the issue's acceptance criteria only require `n`/`N`/`Esc`
to cancel; `"no"` is added for symmetry with the existing `"yes"` alias on
the accept side (a user typing "no" and hitting enter is not a normal
single-keypress flow in this TUI — keys are handled per-keypress, not
per-line — but since `strings.ToLower(strings.TrimSpace(msg.String()))`
already runs, single-character `"n"` is what any real "N" keypress
normalizes to; do not remove the existing `"yes"` symmetry precedent by
leaving `"no"` out). If in doubt, at minimum `"n"` and `"esc"` must be
present per AC2 — `"no"` is optional polish, not a strict requirement.

**Important — do not swallow navigation keys globally.** The `default:`
arm's `return m, nil` only fires for *this* keypress, inside *this*
`if m.finalizeState == finalizeAwaitingConfirmation` block. It does not
prevent the next keypress (e.g. the user presses `]` again to actually
switch tabs) from being handled normally on its own turn, because this
block re-evaluates `m.finalizeState` fresh on every keypress and only
intercepts while still awaiting confirmation — once the user explicitly
decides (`y`/`n`/`esc`), `finalizeState` changes and this block no longer
matches, so subsequent keypresses flow through to the normal tab-switch/
scroll handlers below in `Update` exactly as before. **This means
navigation keys pressed while awaiting confirmation currently rely on
falling through past this block to reach the tab-switching handlers
further down in `Update` — verify whether `[`, `]`, `F2`, and arrow-key
handling live below this block in `Update`'s switch/if-chain (they do,
since they are unrelated to finalize and were unaffected before this
issue).** Confirm this by locating the `"["`/`"]"`/`"f2"` handling in
`tui.go` and checking it is positioned after the (relocated) confirm-gate
block; if so, the `default: return m, nil` is wrong and must instead be
`default:` with **no return** (fall through) so navigation keys still
reach their real handlers below. Read the actual surrounding code before
choosing between `return m, nil` and fallthrough — the correct choice is
whichever preserves existing tab-switch/scroll behavior for AC1's
navigation keys while still not treating them as a decision.

**Acceptance criteria (AC1, AC2):**
- AC1: with `m.finalizeState == finalizeAwaitingConfirmation`, pressing
  `[`, `]`, `f2`, an arrow key, or an unrelated letter leaves
  `m.finalizeState == finalizeAwaitingConfirmation` unchanged, produces no
  new "cancelled" line in `m.activityLines`, AND (verify per the note
  above) still performs its normal navigation effect (tab switch/scroll)
  if that behavior existed prior to this change.
- AC2: `y` or `yes` transitions to `finalizeLiveRunning` (unchanged
  existing behavior). `n`, `N` (lowercased to `n`), or `esc` transitions to
  `finalizeIdle`, calls `m.finalizeCancel()` if non-nil, and appends the
  existing "Live posting cancelled — nothing was posted." activity line.

**Dependencies:** Task 1 (block must be in its final position before
editing, so the fallthrough-vs-return analysis is done against the real
surrounding code, not the pre-move position).

---

### Task 3: Route finalize status transitions through `SetTransientMessage`

**File:** `internal/tui/tui.go`

At each of the following five case arms, add a `m.footerManager.SetTransientMessage(...)`
call alongside the existing (unchanged) `m.appendActivity(...)` call:

1. **`finalizeDryRunMsg` handler** (`tui.go:~605–614`, sets
   `m.finalizeState = finalizeAwaitingConfirmation`): after setting
   `finalizeState`, add
   `m.footerManager.SetTransientMessage("Dry-run complete — post live? (y/N)")`.

2. **Confirm-gate `y`/`yes` arm** (Task 2's rewritten switch, the branch
   that sets `m.finalizeState = finalizeLiveRunning`): add
   `m.footerManager.SetTransientMessage("Posting live via finalize-reviews.sh...")`
   immediately before or after the existing
   `m.appendActivity(m.styles.Activity.Render("Posting live via finalize-reviews.sh..."))`
   line.

3. **Confirm-gate `n`/`esc` arm** (Task 2's rewritten switch, the branch
   that sets `m.finalizeState = finalizeIdle`): add
   `m.footerManager.SetTransientMessage("")` to clear the prompt, placed
   after `m.finalizeCancel = nil` and before/after the existing
   `appendActivity` "cancelled" line (ordering relative to `appendActivity`
   doesn't matter; both must execute).

4. **`finalizeCompleteMsg` handler** (`tui.go:~620–630`, sets
   `m.finalizeState = finalizeIdle`): add
   `m.footerManager.SetTransientMessage("")` to clear the prompt.

5. **`finalizeErrorMsg` handler** (`tui.go:~633–654`, sets
   `m.finalizeState = finalizeIdle` for both dry-run and live failures):
   add `m.footerManager.SetTransientMessage("")` to clear the prompt.
   (Applies to both the dry-run-error and live-error paths, since both set
   `finalizeState = finalizeIdle` in the same case arm — one call site
   covers both.)

Do **not** modify `renderStatusRow`/`RenderWithSeparator`/
`GetFooterHeight` in `footer.go` — the existing `transientMessage`
priority-check in `renderStatusRow` (`if fm.transientMessage != "" { return
fm.transientMessage }`) already makes any non-empty transient message
visible on every tab's status row without further changes, exactly as it
does today for the notes-edit prompt.

**Acceptance criteria (AC3):**
- After `finalizeDryRunMsg` is processed, `m.footerManager.RenderFooter(TabTypeReviewContent).StatusRow`
  (or an equivalent public read of the transient message) contains the
  substring `"post live? (y/N)"`.
- After a `y`/`n`/`esc` decision or a terminal completion/error message,
  the transient message is empty (`""`), so no stale prompt persists once
  `finalizeState` returns to `finalizeIdle` or moves to
  `finalizeLiveRunning`.
- This holds regardless of which tab is currently active (Main TUI,
  Finalize Preview, or any other tab) — `renderStatusRow`'s existing
  priority check does not gate on tab type.

**Dependencies:** Task 2 (the confirm-gate arms being edited here are the
same arms Task 2 rewrote; do this as a continuation of the same edit pass
to avoid a second read-modify-write of the same lines).

---

### Task 4: Tests

**File:** `internal/tui/finalize_integration_test.go` (extend existing
file; reuse `newFinalizeTestModel`, `pressKey`, `activityContains`,
`drainBatchForType`, `installFakeFinalizeScript` helpers already defined
there and in `finalize_test.go`/`decide_routing_test.go`)

Add the following test functions. Drive each through
`m.Update(msg)` exactly as `TestHandleFinalize_HappyPath`/
`TestHandleFinalize_DeclinePath` already do (get to
`finalizeAwaitingConfirmation` via `handleFinalize` + a fake
`finalizeDryRunMsg`, then assert on the keypress under test).

1. **`TestFinalizeConfirm_NavigationKeyDoesNotCancel`** (AC1 regression
   guard — the core silent-cancel-on-navigation trap this issue fixes).
   Drive to `finalizeAwaitingConfirmation`. For each of a representative
   set of navigation/other keys — `"["`, `"]"`, `"f2"`, an arrow key
   (`tea.KeyPressMsg{Code: tea.KeyUp}` or the equivalent constructor used
   elsewhere in this test file for arrow keys — check
   `mode_switching_test.go`/`enter_key_test.go` for the existing arrow-key
   `tea.KeyPressMsg` construction idiom and match it), and an unrelated
   letter like `pressKey('z')` — call `m.Update(msg)` and assert:
   - `m.finalizeState` is still `finalizeAwaitingConfirmation` after each
     keypress.
   - `activityContains(m, "cancelled")` is `false` after each keypress.
   - (Optional but recommended) the returned `tea.Cmd` is nil or, if
     non-nil because the key has a legitimate navigation side effect
     (e.g. tab switch), that no finalize-related message is produced by
     it.
   Run this as either one test with a `for`-loop table of keys or
   multiple subtests via `t.Run` per key — prefer `t.Run` subtests so a
   failure on one key doesn't mask a failure on another.

2. **`TestFinalizeConfirm_LowercaseNCancels`** and
   **`TestFinalizeConfirm_UppercaseNCancels`** (AC2). Drive to
   `finalizeAwaitingConfirmation`, press `pressKey('n')` (respectively
   `pressKey('N')`), and assert:
   - `m.finalizeState == finalizeIdle`.
   - `activityContains(m, "cancelled")` is `true`.
   - The transient message is cleared (see Task 3's public-read note —
     assert via `m.footerManager.RenderFooter(TabTypeMain).StatusRow`
     does NOT contain `"post live?"`).
   - No live-run `tea.Cmd` was launched (mirror
     `TestHandleFinalize_DeclinePath`'s `callCount` assertion pattern).

3. **`TestFinalizeConfirm_EscCancels`** (AC2, and the Task 1 ordering fix
   — this is the test that would have failed before Task 1's reordering
   if the Finalize Preview tab were active, so construct it that way).
   Drive to `finalizeAwaitingConfirmation`, then **switch the active tab
   to the Finalize Preview tab** (`m.tabManager.FindTabByID(m.finalizeWindowTabID)`
   then `m.switchActiveTab(idx)` — mirror how `openFinalizePreviewWindow`
   itself switches tabs) before sending the `Esc` keypress
   (`tea.KeyPressMsg{Code: tea.KeyEscape}` or this file's existing esc-key
   construction idiom — check `body_edit_test.go`/`notes_edit_routing_test.go`
   for the established pattern). Assert:
   - `m.finalizeState == finalizeIdle` (proves the confirm-gate handled
     `Esc`, not the generic `TabTypeReviewContent`-closes-on-Esc handler).
   - The Finalize Preview tab is **still open**
     (`m.tabManager.FindTabByID(m.finalizeWindowTabID) >= 0`) — proves the
     window was NOT closed as a side effect, which is what would happen
     if the old bug (Esc closing the window instead of cancelling) were
     still present.
   - `activityContains(m, "cancelled")` is `true`.

4. **`TestFinalizeConfirm_YKeyStillPosts`** (AC2 regression guard — a
   plain assertion that Task 2's rewritten switch didn't accidentally
   break the existing happy path;
   `TestHandleFinalize_HappyPath` already covers this end-to-end, so this
   can be a smaller, narrower duplicate focused only on the immediate
   post-keypress state if the existing happy-path test doesn't already
   give sufficient isolated coverage of just this transition — skip
   adding this if `TestHandleFinalize_HappyPath` already asserts
   `finalizeState == finalizeLiveRunning` immediately after the `y`
   keypress, which it does; in that case this task item is already
   satisfied by the existing test and needs no new code).

5. **`TestFinalizeConfirm_PromptVisibleInFooterDuringAwaitingConfirmation`**
   (AC3, AC5 — prompt text is present in the preview window's
   content/footer). Drive to `finalizeAwaitingConfirmation` via a fake
   `finalizeDryRunMsg` (as in `TestHandleFinalize_HappyPath`). Assert:
   - `m.footerManager.RenderFooter(TabTypeReviewContent).StatusRow`
     contains `"post live? (y/N)"` — proves the prompt is visible
     specifically when rendering as-if the Finalize Preview tab (whose
     `Type()` is `TabTypeReviewContent`) is active, satisfying "visible
     in/at the Finalize Preview window."
   - The same call with `TabTypeMain` also contains the prompt (proving
     it is not accidentally gated to one tab type only —
     `renderStatusRow`'s transient-message check has no tab-type
     condition, so this should already pass once Task 3 is done; treat a
     failure here as a signal Task 3 was implemented incorrectly, e.g. by
     gating the `SetTransientMessage` call on the active tab type when it
     should not be).
   - After the subsequent `y` keypress, the status row's content changes
     to reflect `"Posting live"` (not the stale `"post live?"` prompt).
   - After a subsequent terminal message (`finalizeCompleteMsg` or a
     cancel keypress), the status row's transient content is empty (falls
     back to `renderBaseInfo()`'s normal watcher-status text, i.e. no
     longer contains `"post live?"` or `"Posting live"`).

**Run with `-race`** per AC5's explicit instruction:

```bash
go test ./internal/tui/... -run TestFinalizeConfirm -race -v
```

Also run the full existing finalize suite to confirm no regressions from
the relocation/rewrite:

```bash
go test ./internal/tui/... -run TestHandleFinalize -race -v
go test ./internal/tui/... -run TestFinalize -race -v
```

**Dependencies:** Tasks 1, 2, 3 (tests exercise the final combined
behavior).

## Validation Commands

Run from the worktree root:

```bash
cd /Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-102-94660

# Build check
go build ./...

# Full TUI package test suite, race-enabled (per AC5)
go test ./internal/tui/... -race -v

# Targeted new/changed tests
go test ./internal/tui/... -race -run 'TestFinalizeConfirm|TestHandleFinalize' -v

# Vet
go vet ./...
```

All of the above must pass with no new failures and no `-race` warnings.
The pre-existing full suite (`go test ./internal/tui/...`) must remain
green — this issue does not intend to change any behavior of
`finalize.go`'s pure functions, `commands.go`'s window-opening, or the
subprocess flow itself (per the issue's explicit "Scope" section), only
the confirm-gate's key handling, its position in `Update`'s dispatch
order, and where the prompt/status text is surfaced.

## Non-Goals (explicit, per issue Scope)

- No change to `finalize.go` (the state enum, message types, or
  `runFinalizeCmd`/`pollFinalizeOutputCmd`).
- No change to `finalize-reviews.sh`, `pr_review_finalize.py`, or the
  spool file format/schema.
- No change to `openFinalizePreviewWindow`'s window-reuse-by-ID logic in
  `commands.go`.
- No change to `ReviewContentTab`'s constructors, `AppendFromCapture`, or
  its `Type()`/`IsClosable()` contracts — it remains a generic,
  caller-agnostic tab type per its existing doc comments.
