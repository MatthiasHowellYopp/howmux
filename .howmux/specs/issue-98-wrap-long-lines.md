# Design Spec: Wrap long lines in the review content window

Closes #98

## Problem Recap

`internal/tui/review_content_tab.go` feeds raw, unwrapped text into the
`viewport.Model` at three call sites (`NewReviewContentTab`'s body path,
its `!found` error path, and `AppendFromCapture`). The bubbles viewport does
not soft-wrap; it clips any line wider than the current viewport width at
the right edge. `Resize` updates `vp.SetWidth`/`SetHeight` but never
re-wraps stored content, so a resize doesn't reflow — it just changes where
the (still-unwrapped) content gets clipped.

## Solution Approach

Add a single wrap-to-width helper and route every `SetContent` call in
`review_content_tab.go` through it, wrapping from the raw source-of-truth
fields (`plainContent` / `errMsg`) rather than from whatever is already in
the viewport. Store the last-known content width so `Resize` can re-wrap
without needing a separate "what did I just set" cache.

**Use `lipgloss.Wrap` from `charm.land/lipgloss/v2`** (already a direct
dependency — see `go.mod`, and already imported by `log_tab.go` and
`styles.go` in this package) as the wrap primitive:

```go
func Wrap(s string, width int, breakpoints string) string
```

This is the *existing* wrapping capability nearest in kind to what the issue
asks for, and it matches the issue's own suggested approach ("via
`lipgloss`/`wordwrap`"). It preserves ANSI styling and hyperlinks, which
matters here because the `!found` error path renders through
`styles.Error.Render(rct.errMsg)` *before* the string reaches
`SetContent` — a wrap helper that discards ANSI would strip the error
styling as a side effect.

**Do not reuse `OutputView.wrapText`** (`internal/tui/output_view.go`).
It is a private, package-level helper on a different type (`OutputView`,
not `ReviewContentTab`), used for a different purpose (indenting/wrapping
live agent output lines with per-line prefixing), and it has a gap this
issue's content can't tolerate: it wraps on `strings.Fields` word
boundaries only, so a single unbroken token longer than `width` (exactly
what a `file:line — description` finding line looks like when the
`file:line` prefix itself is long, e.g. a deeply nested path with no
spaces) is emitted as-is, wider than the target width, defeating the
wrap. It also has no ANSI awareness. Introducing a second, independent
wrap implementation in the same package — one word-boundary-only, one
lipgloss-based — would be the "duplicate wrap helper" the issue explicitly
asks to avoid; `lipgloss.Wrap` is the correct shared primitive going
forward, and `OutputView.wrapText` is out of scope for this issue (it has
its own call site and behavior; changing it risks regressing agent-output
rendering, which is not part of this issue's acceptance criteria).

### The helper

Add a small private method on `*ReviewContentTab` (keeps the change
localized to this file, matches how `OutputView.wrapText` is itself a
private method on its own type rather than a free function):

```go
// wrapWidth returns the width to wrap content to: the viewport's frame-
// relative width, treated as at least 1 to avoid lipgloss.Wrap degenerate
// behavior at width <= 0 (mirrors the existing Resize test's expectation
// that 0/negative dimensions must not panic).
func (rct *ReviewContentTab) wrapWidth() int {
    w := rct.viewport.Width()
    if w <= 0 {
        return 1
    }
    return w
}

// setWrappedContent wraps raw to the tab's current width and sets it as
// the viewport content. All three SetContent call sites route through
// this so wrapping stays in one place and Resize can re-invoke it.
func (rct *ReviewContentTab) setWrappedContent(raw string) {
    rct.viewport.SetContent(lipgloss.Wrap(raw, rct.wrapWidth(), ""))
}
```

Notes on the design:

- `breakpoints` is passed as `""` (lipgloss's documented default breakpoint
  set of spaces/hyphens) — sufficient for prose and for `file:line —`
  finding lines, since `lipgloss.Wrap` breaks mid-token when a single token
  still exceeds the width after normal breakpoint wrapping (this is exactly
  the behavior the issue needs for long unbroken paths that
  `OutputView.wrapText` cannot handle).
- `wrapWidth()` reads `rct.viewport.Width()` rather than `rct.width`
  (the tab's own tracked field) so the wrap always matches what the
  viewport itself will render at — the two are set together in `Resize`
  today, but reading from the viewport keeps this helper correct even if
  that ever changes, and avoids a second potential source of truth for
  "current width."
- No extra frame/border subtraction is needed: `ReviewContentTab`'s
  viewport is created bare (`viewport.New(viewport.WithWidth(80),
  viewport.WithHeight(24))`, no `.Style()` border/padding applied anywhere
  in this file), so `viewport.Width()` already is the content width. If a
  future change adds a border/padding style to this viewport, `wrapWidth`
  is the single place that would need to subtract the frame size — call
  this out in a comment so it isn't missed later.

### Applying the helper at all three call sites

1. **`NewReviewContentTab`, found path**: after `rct.plainContent = body`,
   call `rct.setWrappedContent(body)` instead of `vp.SetContent(body)`.
   `vp` must be assigned to `rct.viewport` *before* this call (or the
   method restructured to set `rct.viewport = vp` first, then call
   `rct.setWrappedContent`), since `setWrappedContent` reads
   `rct.viewport.Width()`.

2. **`NewReviewContentTab`, `!found` path**: replace the two branches
   (`styles != nil` / else) that call `vp.SetContent(...)` directly with a
   single `rct.setWrappedContent(...)` call, applied to whichever string
   was chosen (`styles.Error.Render(rct.errMsg)` or the raw `rct.errMsg`).
   Same viewport-assignment-order constraint as above.

3. **`AppendFromCapture`**: after `rct.plainContent = content`, call
   `rct.setWrappedContent(content)` instead of
   `rct.viewport.SetContent(content)`.

### Resize reflow

`Resize` currently only calls `SetWidth`/`SetHeight`. It must re-wrap the
stored raw content afterward, using whichever raw field is populated:

```go
func (rct *ReviewContentTab) Resize(width, height int) {
    rct.width = width
    rct.height = height
    rct.viewport.SetWidth(width)
    rct.viewport.SetHeight(height)

    switch {
    case rct.errMsg != "":
        if rct.styles != nil {
            rct.setWrappedContent(rct.styles.Error.Render(rct.errMsg))
        } else {
            rct.setWrappedContent(rct.errMsg)
        }
    case rct.plainContent != "":
        rct.setWrappedContent(rct.plainContent)
    }
    // else: a freshly-constructed NewLiveReviewContentTab tab before its
    // first AppendFromCapture — nothing to (re)wrap yet, matches the
    // existing "starts empty" contract (TestNewLiveReviewContentTab_StartsEmpty).
}
```

This duplicates the `!found`-styling branch logic that already exists in
`NewReviewContentTab`. Rather than duplicate that `if rct.styles != nil {
... } else { ... }` styling decision in two places (constructor and
`Resize`), factor it into a small helper used by both:

```go
// renderedErrMsg returns rct.errMsg run through styles.Error if styles is
// set, otherwise the raw message — the single place that decides how the
// error message is styled before wrapping, used by both the constructor's
// !found path and Resize's re-wrap.
func (rct *ReviewContentTab) renderedErrMsg() string {
    if rct.styles != nil {
        return rct.styles.Error.Render(rct.errMsg)
    }
    return rct.errMsg
}
```

Then both the constructor's `!found` branch and `Resize`'s `errMsg != ""`
branch call `rct.setWrappedContent(rct.renderedErrMsg())`. This removes
duplication and ensures the constructor and `Resize` can never drift on how
the error message is styled.

**Empty-content guard**: `lipgloss.Wrap("", w, "")` returns `""`, and
`setWrappedContent` on an empty `plainContent`/`errMsg` is harmless — but
the `switch` above already skips both branches when neither field is
populated (the live-tab-before-first-append case), so no extra guard is
needed for that case. `rct.plainContent != ""` as the branch condition is
intentionally consistent with how `errMsg != ""`/`plainContent` are already
treated as mutually-exclusive, "is this populated" signals elsewhere in
this file (see the type's own field docs: "Mutually exclusive with a
populated plainContent").

### `CopyableContent()` — unchanged

No change needed. It already reads `rct.errMsg` / `rct.plainContent`
directly (the raw fields), never `rct.viewport`'s content — the existing
implementation already satisfies AC4/AC5 once the raw fields are populated
before wrapping (which they already are, and remain, at each call site).
This is called out explicitly so the builder does not touch
`CopyableContent()` unnecessarily.

## Relevant Files

| File | Change |
|------|--------|
| `internal/tui/review_content_tab.go` | Add `lipgloss` import; add `wrapWidth`, `setWrappedContent`, `renderedErrMsg` helpers; route all `SetContent` calls through `setWrappedContent`; update `Resize` to re-wrap stored raw content |
| `internal/tui/review_content_tab_test.go` | Add wrap-specific tests (see Task 2 below); existing tests must continue to pass unmodified in behavior (they assert `strings.Contains`, which remains true post-wrap since wrapping only inserts newlines, never drops/reorders text) |

No other files change. This confirms the issue's own stated scope: view-layer
only, no spool reading, Reviews tab, or finalize flow changes.

## Concurrency Analysis

**No concurrency concern.** `ReviewContentTab` has no `sync.Mutex`, no
goroutine boundary crossing, and this change adds no new field accessed from
more than one goroutine. `AppendFromCapture` is already called only from the
`finalizeTickMsg` handler in `tui.go`'s single-threaded Bubble Tea update
loop (per the existing doc comment on `AppendFromCapture`), and this change
does not alter that call site or its threading. `Resize` is likewise only
called from the main update loop in response to `tea.WindowSizeMsg`. No new
shared state is introduced.

## Team Orchestration

This is a single-file, single-concern change with no parallelizable
sub-components — one builder task covers the full implementation, and
tests are part of the same task (small, tightly-coupled change; splitting
implementation and tests into separate parallel tasks would create more
coordination overhead than it saves).

## Task Breakdown

### Task 1: Implement wrap-on-width in `review_content_tab.go`

**Acceptance Criteria:**

1. Add `"charm.land/lipgloss/v2"` to the import block in
   `internal/tui/review_content_tab.go`.
2. Add `wrapWidth() int`, `setWrappedContent(raw string)`, and
   `renderedErrMsg() string` methods on `*ReviewContentTab` as specified
   above (or functionally equivalent — the specific method names are a
   suggestion, not a hard requirement, but the *behavior* — wrap via
   `lipgloss.Wrap` at `viewport.Width()`, guard against width <= 0 — is
   required).
3. `NewReviewContentTab`'s found path calls `rct.setWrappedContent(body)`
   after setting `rct.plainContent = body` and after `rct.viewport = vp`,
   instead of the current direct `vp.SetContent(body)`.
4. `NewReviewContentTab`'s `!found` path calls
   `rct.setWrappedContent(rct.renderedErrMsg())` after `rct.viewport = vp`,
   replacing the current `styles != nil` / else branch that calls
   `vp.SetContent` directly. `rct.errMsg` is still set to the raw,
   unstyled message text (unchanged from today) before this call.
5. `AppendFromCapture` calls `rct.setWrappedContent(content)` after setting
   `rct.plainContent = content`, instead of the current
   `rct.viewport.SetContent(content)`.
6. `Resize` re-wraps after calling `SetWidth`/`SetHeight`: if `rct.errMsg`
   is non-empty, re-wrap via `rct.setWrappedContent(rct.renderedErrMsg())`;
   else if `rct.plainContent` is non-empty, re-wrap via
   `rct.setWrappedContent(rct.plainContent)`; else (live tab, no content
   yet) do nothing.
7. `CopyableContent()` is unchanged — still reads `rct.errMsg` /
   `rct.plainContent` directly, never touches the viewport.
8. `wrapWidth()` returns `1` (not `0` or a negative number) when
   `rct.viewport.Width() <= 0`, so `Resize(0, 0)` / `Resize(-1, -1)`
   continue to not panic (existing
   `TestReviewContentTabResizeUpdatesViewportDimensions` coverage).
9. `go build ./...` succeeds; `go vet ./internal/tui/...` reports no new
   issues.

**Dependencies:** None.

### Task 2: Add wrap-specific tests to `review_content_tab_test.go`

**Acceptance Criteria:**

1. **Long single line wraps, `CopyableContent()` unaffected**: construct a
   `ReviewContentTab` via `NewReviewContentTab` with a single line body
   longer than a known narrow width (e.g. 200 chars at width 40, no spaces
   or with spaces — cover the case with spaces via normal wrap and
   optionally a no-space long token to confirm `lipgloss.Wrap` breaks mid-
   token, matching AC1's "onto the next line(s)" requirement even for
   unbroken tokens like a long `file:line` prefix). `Resize` to the known
   width. Assert (via `github.com/charmbracelet/x/ansi`'s `ansi.Strip` on
   `rct.View()`, already an available import in this module — see
   `tui.go`'s existing `ansi.Truncate` usage) that the ANSI-stripped view
   output contains more than 1 line (`strings.Count(stripped, "\n") >= 1`
   or split-and-count > 1). Assert `rct.CopyableContent()` still equals the
   original unwrapped single-line string exactly.
2. **Resize reflow, narrower then wider**: construct with a long body,
   `Resize` to a narrow width (e.g. 20), capture the ANSI-stripped,
   wrapped line count; `Resize` to a much wider width (e.g. 200) that fits
   the body on fewer visual lines, and assert the wrapped line count
   decreases (proving re-wrap happened rather than staying pinned to the
   first width). `Resize` back to a width narrower than the first,
   confirming line count increases again.
3. **`AppendFromCapture` wraps**: construct via `NewLiveReviewContentTab`,
   `Resize` to a narrow width, call `capture.AddLine(...)` with a single
   long line, call `AppendFromCapture()`, and assert (ANSI-stripped) the
   view shows the content wrapped across multiple lines, while
   `CopyableContent()` returns the unwrapped original line.
4. **`!found` error path wraps**: construct with `found=false`, `Resize`
   to a narrow width chosen so the existing error message text (`"Could
   not read review content for %s (spool file missing or unreadable)."`
   with a sufficiently long title) exceeds it, and assert the
   ANSI-stripped view shows more than 1 line while `CopyableContent()`
   still returns the exact single-line error message (matching the
   existing `TestReviewContentTabViewRendersErrorWhenNotFound`'s exact-
   match style assertion, extended with the wrap-line-count check).
5. All new tests use the existing `testReviewsStyles()` helper already
   used throughout this test file, for consistency.
6. All existing tests in `review_content_tab_test.go` continue to pass
   unmodified — no existing test's assertions should need to change,
   since wrapping only inserts newlines into already-passing
   `strings.Contains` checks (verify this by running the full existing
   suite, not just the new tests).
7. `go test ./internal/tui/... -run TestReviewContentTab -v` and
   `go test ./internal/tui/... -run TestLiveReviewContentTab -v` and
   `go test ./internal/tui/... -run TestNewLiveReviewContentTab` all pass.

**Dependencies:** Task 1 (tests exercise the implementation from Task 1;
in practice both are written together as one PR-sized change, but Task 2's
acceptance criteria are stated separately so coverage of each acceptance
criterion in the issue is independently checkable).

## Validation Commands

```bash
# Build
go build ./...

# Vet
go vet ./internal/tui/...

# Targeted tests for this change
go test ./internal/tui/... -run TestReviewContentTab -v
go test ./internal/tui/... -run TestLiveReviewContentTab -v
go test ./internal/tui/... -run TestNewLiveReviewContentTab -v

# Full package test to catch any regression in related tabs (LogTab, etc.)
go test ./internal/tui/... -v

# Repo-wide test suite
go test ./...
```

## Out of Scope (explicitly, per issue)

- No change to spool reading (`review.ReadSpoolBody` or its callers).
- No change to the Reviews tab (`reviews_tab.go`) or how it opens this tab.
- No change to the finalize flow (`finalize.go`, `finalize_preflight.go`).
- No change to `OutputView.wrapText` in `output_view.go` — it is a
  different type with a different call site and its own word-boundary-only
  wrap behavior; changing it is not required by any acceptance criterion
  in this issue and risks an unrelated regression in agent-output
  rendering.
- No template or script changes.
