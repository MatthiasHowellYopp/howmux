---
inclusion: n/a
---

# Design Spec: PR-review gate — drive finalize script with dry-run preview

Closes #87

## Problem Statement

Users can set a `decision:` on a review (#85) and read/edit its full body
(#84/#86), but nothing in howmux ever actually drains the spool.
`finalize-reviews.sh` — external to this repo, in `ai-resources/scripts/`
— is the only thing that acts on a set decision (posting inline PR
comments, re-running the consolidator for `revise`/`rereview`, or
archiving a `discard`), and today it can only be run by hand from a
terminal outside the TUI. Because `finalize-reviews.sh` is a *batch drain*
that can spawn a `kiro-cli` one-shot per pending file and make network
calls to GitHub, it is unsafe to wire it in the same way `decide` was
wired in #85 (a synchronous, sub-100ms shell call) — see #85's spec,
"Why a new script rather than reusing `finalize-reviews.sh`". It needs its
own async, streaming, dry-run-gated invocation path.

## User Story

As a howmux user finalizing PR reviews, I want to run
`finalize-reviews.sh` with a dry-run preview first, then explicitly
confirm to post live, while seeing the finalize output streamed in real
time, so I can safely post reviews with confidence in what's being sent.

## Solution Approach

Reuse the codebase's two existing async idioms rather than inventing a
third:

1. **The single-shot `tea.Cmd` → completion-`tea.Msg` pattern** already
   used by `handleReview`/`reviewCompleteMsg` (#65) for "run a subprocess
   in a goroutine, report back exactly once when it's done." This issue
   needs a variant of this for the *terminal* events of a finalize run:
   dry-run finished, live run finished, or either failed.

2. **The ring-buffer + `tea.Tick` poll pattern** already used by `LogTab`
   (`internal/logging/ring_buffer.go` + `TickMsg`) for "show output from a
   goroutine as it arrives, without giving the goroutine a `*tea.Program`
   handle." This issue needs this for real-time line-by-line streaming of
   `finalize-reviews.sh`'s stdout/stderr into the review content window —
   there is no existing `tea.Program.Send`-based push mechanism in this
   codebase (`Run()` in `tui.go` constructs `tea.NewProgram` after the
   model, so the model never holds a `*tea.Program` reference to push
   into), and introducing one would be a much larger structural change
   than this issue calls for. Polling a shared ring buffer on a `tea.Tick`
   is the established, already-proven idiom for exactly this shape of
   problem in this codebase.

Combining them: a goroutine runs `exec.CommandContext(ctx, "bash",
"finalize-reviews.sh", ["--dry-run"])`, with both `Stdout` and `Stderr`
wired to a small thread-safe line buffer (reusing
`agent.NewOutputCapture`/`agent.CaptureWriter` — already exists, already
race-tested, no need for a bespoke buffer type). The **review content
window** (#84's `ReviewContentTab`) is extended to optionally poll that
buffer on a `tea.Tick`, exactly like `LogTab` does, appending new lines to
its viewport as they arrive. When the subprocess exits, the goroutine's
`tea.Cmd` returns a terminal message (`finalizeDryRunMsg` or
`finalizeCompleteMsg`, or `finalizeErrorMsg` on a non-zero exit/spawn
failure) carrying the captured output and exit error, at which point
polling stops.

### State machine

```
   idle
     │  user runs "finalize" (REPL) or presses a Reviews-tab key-menu entry
     ▼
 dry-run-running  ──(finalizeErrorMsg)──► idle (error surfaced, spool untouched)
     │
     │ (finalizeDryRunMsg, exit 0)
     ▼
 awaiting-confirmation   (preview streamed into review content window; footer
     │                    prompts "Post live now? (y/N)", mirroring the
     │                    existing m.confirmingExit y/N convention)
     │
     ├─(user presses "n"/anything else/Esc)──► idle (dry-run output stays
     │                                           visible in the window;
     │                                           nothing was posted)
     │
     └─(user presses "y")
              ▼
        live-running  ──(finalizeErrorMsg)──► idle (error surfaced; per
              │                                 finalize-reviews.sh's own
              │                                 idempotent design, files
              │                                 that already made it to
              │                                 done/ stay there, files
              │                                 still in pending/ stay in
              │                                 pending/ — see "Error
              │                                 handling" below)
              │
              │ (finalizeCompleteMsg, exit 0)
              ▼
            idle (Reviews tab decision_state re-render picks up done/
                   moves automatically — see "Decision state update")
```

This is a strict linear state machine, deliberately modeled on the
existing `m.confirmingExit bool` + y/N-on-any-keypress convention in
`tui.go` (see `case msg.String()` handling under `if m.confirmingExit`)
rather than introducing a new overlay type — the "explicit confirmation"
requirement (AC4) is exactly the shape that field already exists for,
just gated on a different trigger and with a different affirmative
action.

### Why not gate this behind the existing `activeOverlay` system

`activeOverlay` (see `overlayType`/`activateOverlay` in `tui.go`) is used
for self-contained, single-screen dialogs (About, Status) that render
*over* the current tab and capture all key input until dismissed. The
dry-run preview is not that: its content **is** the review content
window's viewport (the same widget #84 built, now optionally
live-streaming), and the confirmation prompt is a one-line footer/activity
question layered on top of already-visible, already-interactive tab
content — closer to `confirmingExit` (a boolean gate on the *next*
keypress, not a modal surface) than to `overlayAbout`. Reusing
`activeOverlay` here would force the finalize output into the overlay's
fixed 60%-width dialog box instead of the full-height scrollable viewport
already built for exactly this purpose in #84.

## Relevant Files

### Files to Create

1. **`internal/tui/finalize.go`** (new)
   - `finalizeState` type (`finalizeIdle`, `finalizeDryRunRunning`,
     `finalizeAwaitingConfirmation`, `finalizeLiveRunning`) — an explicit
     enum rather than reusing a bare bool like `confirmingExit`, because
     this state machine has more than two states (dry-run-running and
     live-running are both "busy" but must be distinguishable so a stray
     `finalizeDryRunMsg` arriving late can't be mistaken for a live-run
     completion).
   - `finalizeDryRunMsg`, `finalizeCompleteMsg`, `finalizeErrorMsg` types
     (see "New tea.Msg types" below).
   - `finalizeTickMsg time.Time` — the poll tick for streaming output,
     kept distinct from `tui.go`'s existing bare `tickMsg struct{}` (used
     for the general activity-line/spinner tick) and from `LogTab`'s
     `TickMsg`, matching the codebase's existing convention of one
     concrete tick type per independent poll loop (see `editorDoneMsg`'s
     doc comment for the same "don't overload an existing message type"
     reasoning applied to a tick instead of a completion message).
   - `runFinalizeCmd(ctx context.Context, dryRun bool, capture
     *agent.OutputCapture) tea.Cmd` — builds and runs the
     `exec.CommandContext`, wires `agent.NewCaptureWriter(io.Discard,
     capture, "")` as both `Stdout` and `Stderr` (two independent
     `CaptureWriter`s over the same `OutputCapture`, since
     `CaptureWriter.Write` already assembles line-buffered writes safely
     under its own mutex — no cross-stream interleaving corruption
     because each `CaptureWriter` only touches its own `lineBuf` before
     calling the shared `OutputCapture.AddLine`, which is itself
     mutex-guarded), and returns the terminal message on exit.
   - `pollFinalizeOutputCmd(capture *agent.OutputCapture, lastGen
     *uint64) tea.Cmd` — the `tea.Tick`-driven re-arming poll, structured
     like `LogTab`'s tick handling: compare `capture.Generation()`
     against the last-seen generation, and if changed, return a
     `finalizeTickMsg` that the window-update handler uses to refresh its
     viewport content from `capture.GetLines()`.

2. **`internal/tui/finalize_test.go`** (new)
   - Unit tests for the state machine transitions, the injectable
     `finalizeCommandFunc` seam (see below), and message routing —
     mirroring the structure of `commands_review_test.go`.

### Files to Modify

1. **`internal/tui/commands.go`**
   - Add `handleFinalize(args []string) (model, tea.Cmd)` — the REPL
     command handler for `finalize` (bare form only; this issue does not
     add a `finalize <PR>` per-review form, since `finalize-reviews.sh`
     itself has no such mode — it always drains the whole spool).
   - Add `finalizeCommandFunc = exec.CommandContext` as a package-level
     var (mirroring `bodyEditCommandFunc`'s pattern) so tests can
     substitute a fake command instead of spawning a real
     `finalize-reviews.sh`.
   - Add `finalizeScriptPathFunc func() string` seam (mirroring
     `decisionwriter.go`'s `scriptPathFunc`) resolving to
     `finalize-reviews.sh` — resolved via `exec.LookPath("finalize-reviews.sh")`
     first (it's expected on `$PATH` per `custom-scripts.md`'s
     `~/.local/bin/` symlink), falling back to an explicit error
     ("finalize-reviews.sh not found on PATH — see ai-resources setup") if
     not found, rather than guessing a repo-relative path the way
     `decisionwriter.go` does for `set-review-decision.sh` — unlike that
     script, `finalize-reviews.sh` is intentionally *not* vendored into
     `.howmux/scripts/` (see #85's spec: it lives in `ai-resources/`,
     "external to this repo").

2. **`internal/tui/tui.go`**
   - Add model fields: `finalizeState finalizeState`,
     `finalizeCapture *agent.OutputCapture`, `finalizeCancel
     context.CancelFunc`, `finalizeLastGen uint64`, `finalizeWindowTabID
     string` (empty when no finalize window is open).
   - Add `case finalizeDryRunMsg:`, `case finalizeCompleteMsg:`, `case
     finalizeErrorMsg:`, `case finalizeTickMsg:` arms to `model.Update`,
     following the exact structure of the existing `case
     reviewCompleteMsg:` arm (error → `m.styles.Error` activity line +
     state reset; success → `m.styles.Success` activity line + state
     transition).
   - Extend the top-level key-input switch: while `m.finalizeState ==
     finalizeAwaitingConfirmation`, intercept the next keypress exactly
     like the existing `if m.confirmingExit { ... }` block does — `y`/`yes`
     triggers the live run, anything else cancels back to idle. This must
     be checked in the same position in the switch as `confirmingExit`
     (near the top of the key-handling branch, before tab-specific
     forwarding), and the two must be mutually exclusive by construction
     (finalize's confirmation is not reachable while `confirmingExit` is
     already true, since both gate on being idle-ish top-level states —
     add a comment noting this invariant so a future change doesn't let
     both fire on the same keypress).

3. **`internal/tui/review_content_tab.go`**
   - Add an optional `liveCapture *agent.OutputCapture` field and
     `lastGen uint64` field to `ReviewContentTab`, both nil/zero for the
     existing PR-review-body use case (#84) — a `ReviewContentTab`
     constructed via the existing `NewReviewContentTab` (called from
     `openReviewContentMsg` handling) never sets these, so its behavior
     is completely unchanged for that call site.
   - Add `NewLiveReviewContentTab(id, title string, capture
     *agent.OutputCapture, styles *Styles) *ReviewContentTab` — a second
     constructor (not a variadic option on the existing one, to keep the
     existing constructor's simple two-value contract intact for #84's
     call site) that starts with empty content and a live capture
     reference instead of a fixed `body string`.
   - Add an `AppendFromCapture()` method that reads
     `capture.GetLines()` and calls `viewport.SetContent` — invoked from
     the `finalizeTickMsg` handler in `tui.go`, not from
     `ReviewContentTab.Update` itself, because `Update` only receives
     `tea.Msg` values already routed to the active tab, and
     `finalizeTickMsg` needs to reach this tab whether or not it is
     currently active (streaming should keep buffering even if the user
     switches away and back) — matching how `LogTab`'s own tick handling
     works, except `LogTab.Update` self-arms its next tick because
     `LogTab` owns its ring buffer outright, whereas here the capture is
     shared model-level state so the top-level `Update` is the natural
     owner of the poll loop's lifecycle (start on dry-run launch, stop on
     terminal message).

4. **`internal/tui/command_registry.go`**
   - Register a `finalize` command (`HasArgs: false`, matching `status`'s
     zero-arg registration) so it appears in autocomplete.

5. **`internal/tui/reviews_tab.go`**
   - No code change to row rendering — `decision_state` is derived live
     from `SpoolInfo.DecisionState` on every `ReviewsTab` render pass (see
     `ClassifySpoolState`), which already reads the spool file's current
     directory (`pending/` vs `done/`) and `decision:` value from disk on
     each call. Once `finalize-reviews.sh` (via `pr_review_finalize.py`)
     moves a file to `done/` on a real `post`/`discard`, or clears
     `decision:` and rewrites it back to `pending/` for `revise`/
     `rereview`, the very next time `ReviewsTab` reads the store (its
     existing no-caching, no-background-polling read-on-render contract —
     see the doc comment at `reviews_tab.go:33`) the row's decision_state
     column reflects it automatically. This satisfies AC7 ("update
     decision_state display when finalize completes") with zero new
     Reviews-tab code: the fix is to make sure something re-renders after
     `finalizeCompleteMsg` — which happens for free, since every
     `tea.Msg` handled by `model.Update` triggers a `View()` re-render on
     the next event loop tick, and `ReviewsTab.View()` re-reads the store
     each time it's called.

6. **`README.md`**
   - Add `finalize` to the REPL Commands table, and a short paragraph in
     the PR Review Workflow section describing the dry-run → confirm →
     live flow.

### Files Referenced (Read-Only Dependencies)

- **`internal/agent/output_capture.go`** — `OutputCapture`,
  `CaptureWriter`, `Generation()` reused as-is for the finalize output
  buffer; no changes needed, already race-tested for concurrent
  writer-goroutine / reader-render-loop access (see its existing
  `sync.RWMutex` + `atomic.Uint64` generation counter).
- **`internal/tui/log_tab.go`** — the `TickMsg`/poll-interval idiom this
  design mirrors for `finalizeTickMsg`.
- **`internal/tui/commands.go`** (`reviewStartMsg`/`reviewCompleteMsg`,
  `handleBodyEditLaunch`) — the single-shot completion-message idiom and
  the "injectable command-func var for testability" idiom this design
  mirrors for `finalizeCommandFunc`.
- **`internal/review/decisionwriter.go`** — the "resolve a script path via
  an overridable func, stat it before running, wrap stderr into the
  returned error" idiom partially reused for `finalizeScriptPathFunc`
  (adapted for `$PATH` lookup instead of a repo-relative path, per the
  difference noted above).
- **`ai-resources/scripts/finalize-reviews.sh`** — the target script.
  Confirmed CLI surface: `finalize-reviews.sh` (live drain) and
  `finalize-reviews.sh --dry-run` (preview; `post` decisions print their
  payload instead of sending, and dry-run **never archives files to
  done/** — confirmed via `pr_review_finalize.py`'s own docstring: "the
  file is NOT archived (stays pending for a real run)"). Exits 0 on a
  successful drain (including a drain where every file was skipped for a
  blank decision), non-zero on an unhandled Python exception (the script
  is `set -euo pipefail`, so any non-zero from `python3
  pr_review_finalize.py` propagates as the script's own exit code).
  Prints a JSON drain summary to stdout; human-readable progress banners
  ("→ Draining the review spool…", "→ Anything still in ... was
  skipped.") go to stdout as well, interleaved before/after the JSON.

## New `tea.Msg` Types

```go
// finalizeDryRunMsg reports the terminal outcome of a dry-run
// finalize-reviews.sh invocation (exit 0). lines holds the full captured
// stdout+stderr (interleaved in write order, via the shared
// OutputCapture — see runFinalizeCmd), for a final, complete render of the
// preview window once streaming stops. cancel is the context.CancelFunc for
// the run that just completed, retained so a still-open review window's
// cleanup path has something to call if the user closes the window instead
// of confirming — mirrors reviewCompleteMsg's cancel field.
type finalizeDryRunMsg struct {
	lines  []string
	cancel context.CancelFunc
}

// finalizeCompleteMsg reports the terminal outcome of a LIVE (non-dry-run)
// finalize-reviews.sh invocation (exit 0). Distinct from finalizeDryRunMsg
// (rather than a shared type with a dryRun bool field) because the two
// carry different follow-on obligations in model.Update: finalizeDryRunMsg
// transitions to finalizeAwaitingConfirmation and leaves the window open
// for the user to read; finalizeCompleteMsg transitions to finalizeIdle
// and is what triggers the "finalize complete" success activity line users
// actually care about (AC7). Collapsing both into one type with a bool
// would push that branching into the single case arm instead of letting
// two case arms each do one thing, matching how this codebase already
// prefers reviewCompleteMsg/editorDoneMsg as distinct types over
// discriminated unions.
type finalizeCompleteMsg struct {
	lines []string
}

// finalizeErrorMsg reports a failed finalize-reviews.sh invocation —
// either the script exited non-zero, or it could not be spawned at all
// (e.g. not found on $PATH). dryRun records which phase failed, since the
// error activity line's wording and the state-machine's fallback
// transition both depend on it (a failed dry-run and a failed live run
// both return to finalizeIdle, but with different messages — see "Error
// handling" below). lines holds whatever partial output was captured
// before failure, for inclusion in the error activity line / left visible
// in the window, matching how a real terminal would leave partial output
// visible after a Ctrl-C.
type finalizeErrorMsg struct {
	dryRun bool
	err    error
	lines  []string
}

// finalizeTickMsg drives the poll loop that copies newly captured
// finalize-reviews.sh output lines into the open review content window's
// viewport, mirroring LogTab's TickMsg but kept as its own type per this
// codebase's one-tick-type-per-poll-loop convention (see editorDoneMsg's
// doc comment for the analogous reasoning applied to completion messages).
type finalizeTickMsg time.Time
```

## `tea.Cmd` Functions

### `runFinalizeCmd`

```go
// runFinalizeCmd runs finalize-reviews.sh (with --dry-run if dryRun is
// true) via finalizeCommandFunc, streaming its combined stdout+stderr into
// capture as it's produced (for the poll loop to pick up), and returns
// exactly one terminal tea.Msg once the process exits: finalizeDryRunMsg
// or finalizeCompleteMsg on exit 0 (branching on dryRun), or
// finalizeErrorMsg on any error (path resolution failure, spawn failure,
// or non-zero exit).
//
// Must run on a goroutine, never on the Update goroutine — this function
// itself is fine to call directly since tea.Cmd values ARE goroutine
// entry points by construction (Bubble Tea's runtime invokes each
// returned tea.Cmd on its own goroutine before feeding the resulting
// tea.Msg back into Update), matching every existing tea.Cmd in this
// codebase (reviewCmd in handleReview, checkForUpdateCmd, etc.) — there is
// no separate manual goroutine spawn required or expected here.
func runFinalizeCmd(ctx context.Context, dryRun bool, capture *agent.OutputCapture, cancel context.CancelFunc) tea.Cmd {
	return func() tea.Msg {
		scriptPath, err := finalizeScriptPathFunc()
		if err != nil {
			return finalizeErrorMsg{dryRun: dryRun, err: err}
		}

		args := []string{scriptPath}
		if dryRun {
			args = append(args, "--dry-run")
		}
		cmd := finalizeCommandFunc(ctx, "bash", args...)

		stdout := agent.NewCaptureWriter(io.Discard, capture, "")
		stderr := agent.NewCaptureWriter(io.Discard, capture, "")
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		err = cmd.Run()
		lines := capture.GetLines()

		if err != nil {
			return finalizeErrorMsg{dryRun: dryRun, err: err, lines: lines}
		}
		if dryRun {
			return finalizeDryRunMsg{lines: lines, cancel: cancel}
		}
		return finalizeCompleteMsg{lines: lines}
	}
}
```

Naming rationale: `finalizeCommandFunc` takes `(ctx, name, args...)` as its
signature (i.e. `exec.CommandContext`'s signature) rather than
`exec.Command`'s, unlike `bodyEditCommandFunc`/`decisionwriter.go`'s
`execCommandFunc` — those wrap short-lived, already-synchronous
operations with no cancellation need; `finalize-reviews.sh` is a
long-running, potentially-networked batch operation the user may want to
cancel mid-flight (see "Cancellation" below), so it must be spawned with a
cancellable context from the start, matching `review/runner.go`'s own
`exec.CommandContext` usage for the same reason.

### `pollFinalizeOutputCmd`

```go
// pollFinalizeOutputCmd re-arms itself on a tea.Tick (matching LogTab's
// logPollInterval idiom) as long as a finalize run is in flight. It does
// NOT itself decide whether new data arrived — that check happens in the
// finalizeTickMsg case arm in model.Update, which compares
// capture.Generation() against m.finalizeLastGen and only calls
// ReviewContentTab.AppendFromCapture() when they differ, exactly
// mirroring LogTab.Update's lastWriteCounter comparison. This function's
// only job is producing the next tick.
func pollFinalizeOutputCmd() tea.Cmd {
	return tea.Tick(finalizePollInterval, func(t time.Time) tea.Msg {
		return finalizeTickMsg(t)
	})
}
```

`finalizePollInterval` is a new constant in `finalize.go`, set to the same
`100 * time.Millisecond` as `LogTab`'s `logPollInterval` — no reason to
pick a different cadence for a structurally identical poll.

**Re-arming contract**: the `finalizeTickMsg` case arm in `model.Update`
must return `pollFinalizeOutputCmd()` again as long as
`m.finalizeState` is `finalizeDryRunRunning` or `finalizeLiveRunning`, and
must NOT re-arm once state has moved to `finalizeAwaitingConfirmation` or
`finalizeIdle` — otherwise the tick loop runs forever after the process
exits. This mirrors `LogTab.Update`'s `case TickMsg:` arm, which always
re-arms (that poll loop is meant to run for the tab's entire lifetime);
here the poll loop has a defined start (dry-run or live launch) and end
(terminal message), so the re-arm must be conditional. Concretely: the
initial `runFinalizeCmd` dispatch and the initial
`pollFinalizeOutputCmd` dispatch are batched together
(`tea.Batch(runFinalizeCmd(...), pollFinalizeOutputCmd())`) when a run
starts, and the `finalizeTickMsg` handler is the only thing that keeps
requesting more ticks.

## Confirmation UI / State Machine Wiring

### Triggering a run

`handleFinalize` (REPL `finalize` command, no args) is the only entry
point (this issue does not add a key-menu shortcut on the Reviews tab
itself, since finalize acts on the whole spool, not a selected row —
unlike `decide`, which is inherently row-scoped). It:

1. Guards against re-entrancy: if `m.finalizeState != finalizeIdle`,
   append a warning activity line ("Finalize already in progress") and
   return `m, nil` — mirrors `handleReview`'s bare-form
   already-running guard (`if m.reviewWatcher.Running()`).
2. Creates a cancellable context (`context.WithCancel`), stores
   `cancel` on the model as `m.finalizeCancel`.
3. Resets `m.finalizeCapture = agent.NewOutputCapture(...)` (a fresh
   buffer per run — reusing one across runs would mix dry-run and
   live-run output in the same generation sequence).
4. Opens (or reuses, by a fixed well-known tab ID like
   `"finalize-preview"`) a `NewLiveReviewContentTab` window via the exact
   same `openReviewContentMsg`-style tab-add mechanism `tui.go` already
   uses for #84's windows — reusing that mechanism rather than
   duplicating tab-creation logic.
5. Sets `m.finalizeState = finalizeDryRunRunning`.
6. Returns `tea.Batch(runFinalizeCmd(ctx, true, m.finalizeCapture,
   cancel), pollFinalizeOutputCmd())`.

### On `finalizeDryRunMsg` (dry-run succeeded)

- `m.finalizeState = finalizeAwaitingConfirmation` (poll loop stops
  re-arming as of this transition).
- Append an activity line prompting confirmation:
  `m.styles.Warning.Render("Dry-run complete — review the preview window, then post live? (y/N)")`.
- Do **not** clear `m.finalizeCapture`/`m.finalizeCancel` — the live run
  reuses the same window (appending further output below the dry-run
  preview, clearly demarcated — see "Window content on live run" below)
  and the same cancel func slot is overwritten by the live run's own
  `context.WithCancel` in the next step, not reused, since dry-run and
  live are separate subprocess invocations each needing their own
  context lifetime.

### On confirmation keypress (`y`/`yes` while `finalizeAwaitingConfirmation`)

- Create a **new** `context.WithCancel` for the live run (the dry-run's
  context is already done; do not reuse it).
- Append a separator line to the window content
  (`"─── Posting live ───"` or similar) via a small helper so the dry-run
  preview and the live-run output are visibly distinguished within the
  same window, rather than opening a second window — this keeps the "one
  finalize window per session" mental model simple and matches how the
  issue's AC3 frames it as a single continuous "preview then post" flow.
- Reset `m.finalizeLastGen = 0` so the poll loop's next tick picks up the
  live run's fresh output as "new" (the generation counter is
  monotonically increasing across both invocations since they share the
  same `OutputCapture` instance within one finalize "session" — resetting
  the *comparison* baseline, not the counter itself, is what's needed
  here, exactly like `LogTab` never resets `ringBuffer`, only compares
  against its last-seen counter).
- Set `m.finalizeState = finalizeLiveRunning`.
- Return `tea.Batch(runFinalizeCmd(ctx, false, m.finalizeCapture,
  cancel), pollFinalizeOutputCmd())`.

### On any other keypress while `finalizeAwaitingConfirmation`

- Set `m.finalizeState = finalizeIdle`.
- Call `m.finalizeCancel()` defensively (the dry-run's subprocess has
  already exited by this point since we only reach
  `finalizeAwaitingConfirmation` after `finalizeDryRunMsg`, but calling
  an already-fired `CancelFunc` is always safe/no-op per the `context`
  package's own contract) and clear `m.finalizeCancel = nil`.
- Append `m.styles.Warning.Render("Live posting cancelled — nothing was posted.")`.
- Leave the preview window open and its content untouched, so the user
  can still read the dry-run output after declining — do not close the
  tab automatically; closing tabs in this codebase is always an explicit
  user action (ESC / close-tab key), never done on the app's initiative.

### On `finalizeCompleteMsg` (live run succeeded)

- `m.finalizeState = finalizeIdle`; clear `m.finalizeCancel`.
- Append `m.styles.Success.Render("Finalize complete — spool drained.")`.
- No explicit Reviews-tab refresh call is needed (see "Files to Modify" →
  `reviews_tab.go` above) — the next render reads current spool state
  from disk.
- Append the drain summary's key facts to the activity log if easily
  extractable (best-effort JSON parse of the last line of `lines` for a
  `posted`/`skipped`/`error` count) — but this is a nice-to-have, not a
  hard requirement; if parsing fails, fall back to just the generic
  success line above. Do not fail the whole handler if the JSON parse
  fails.

## Error Handling

### `finalizeErrorMsg` (either phase)

- `m.finalizeState = finalizeIdle`; clear `m.finalizeCancel`.
- Append `m.styles.Error.Render(fmt.Sprintf("Finalize (%s) failed: %v",
  phaseLabel, msg.err))` where `phaseLabel` is `"dry-run"` or `"live"`
  based on `msg.dryRun`.
- Leave the preview window open with whatever partial output was
  captured (`msg.lines`) — do not clear or close it, so the user can
  read exactly what happened before the failure, matching how a real
  terminal leaves scrollback after a command fails.
- **Spool state preservation**: this handler performs no spool
  mutation of any kind — it only touches `m.finalizeState`,
  `m.finalizeCancel`, and the activity log. Every actual spool-file
  write (front-matter rewrite, move to `done/`) happens inside
  `pr_review_finalize.py`'s own per-file processing loop, which is
  documented as idempotent and re-runnable: a file that failed to post
  stays in `pending/` with its `decision:` untouched; a file that
  already succeeded before the failure (e.g. a `ThreadPoolExecutor`
  worker for one file finished and moved it to `done/` while a
  different worker's file caused the fatal exception) simply stays
  moved. There is nothing for the Go side to roll back — AC8's "preserve
  spool state" is satisfied by construction, by never attempting any
  compensating action and trusting the already-idempotent Python drain
  logic, rather than by adding new Go-side spool-protection code that
  would have to duplicate knowledge the drain script already owns.
- A failed **dry-run** in particular can only fail from a path-resolution
  error, a spawn failure, or a Python-side exception unrelated to any
  individual file's decision (dry-run never touches files on disk at
  all, per `pr_review_finalize.py`'s own contract) — so a failed dry-run
  is always safe to just report and return to idle with zero cleanup
  concerns.

### Cancellation (mid-run, e.g. user hits Ctrl-C or closes the window)

Out of explicit scope for this issue's acceptance criteria (none of AC1–9
ask for a cancel button), but the design must not preclude it: because
`runFinalizeCmd` is built on `exec.CommandContext`, wiring a future
"cancel" key to call `m.finalizeCancel()` is a small, additive change
later (the subprocess would receive its termination signal via the
context exactly like `review.RunReview`'s existing cancellation already
works) and requires no restructuring of this design. Note this
possibility in `finalize.go`'s package doc comment so a future issue can
pick it up without re-deriving the plumbing.

## Window Content Model

The finalize preview window (a `ReviewContentTab` in live-capture mode)
shows, in order, top to bottom as the session progresses:

```
[dry-run stdout/stderr lines, streamed as they arrive]
...
─── Posting live ───
[live-run stdout/stderr lines, streamed as they arrive]
```

`AppendFromCapture()` simply calls `viewport.SetContent(strings.Join(capture.GetLines(), "\n"))`
each time it's invoked from the `finalizeTickMsg` handler — matching
`LogTab.refreshContent`'s "rebuild the whole displayed content from the
current buffer snapshot" approach rather than incremental appends, since
`OutputCapture.GetLines()` already returns the full current buffer
cheaply (it's a fixed-size ring buffer, not an unbounded log) and
incremental-append tracking would add complexity with no payoff at this
data volume (a `finalize-reviews.sh` run's total output is at most a few
hundred lines).

The `"─── Posting live ───"` separator is appended to the `OutputCapture`
itself (via a direct `capture.AddLine(...)` call from the confirmation
handler, not by the subprocess), so it becomes part of the same
line-ordered buffer the dry-run and live output already share — this
keeps `AppendFromCapture()`'s "just re-render the whole buffer" logic
uniform with no special-casing for the separator.

## Decision State Update (AC7)

As detailed under "Files to Modify" → `reviews_tab.go`: no push-based
update mechanism is needed. `ReviewsTab.View()` (and the row-selection
`SelectedRecord()`) already read `Store.List()` fresh on every call with
zero caching — this is an explicit, already-documented design decision
in the existing code (`reviews_tab.go:33`'s doc comment: "no caching, no
background polling, no tea.Tick loop"). The Reviews tab's next natural
re-render — which happens automatically after `finalizeCompleteMsg` is
processed by `model.Update`, since Bubble Tea calls `View()` again after
every `Update()` regardless of which case arm fired — will show the
correct, already-current `decision_state` for every row whose spool file
`pr_review_finalize.py` moved or rewrote. This is the same mechanism that
already makes `decide`'s (#85) synchronous writes visible immediately;
the only difference here is that the write happens at the end of an
async subprocess instead of a synchronous one, which doesn't change how
the Reviews tab discovers it.

## Concurrency Analysis

This change **does** introduce cross-goroutine access to shared state,
in the same shape #65's review-workflow already established and #84
already extended:

- **`m.finalizeCapture` (`*agent.OutputCapture`)**: written by the
  `runFinalizeCmd` goroutine's `CaptureWriter`s (via `AddLine`, under
  `OutputCapture`'s own `sync.RWMutex`) while read by the render-path via
  `Generation()` (atomic, lock-free) and `GetLines()` (under the same
  `RWMutex`, read-locked). This is not new locking to design — it's the
  exact `OutputCapture` type `internal/agent/manager.go` already uses for
  streaming live agent output into `OutputView`, reused here unchanged.
  **Acceptance criterion**: all access to `m.finalizeCapture` goes
  through `OutputCapture`'s existing exported methods
  (`AddLine`/`GetLines`/`Generation`) — no new code reads or writes its
  internal `buffer`/`head`/`count` fields directly.
- **`m.finalizeState`, `m.finalizeCancel`, `m.finalizeLastGen`**: these
  are plain model fields mutated only inside `model.Update` (the single
  Bubble Tea event-loop goroutine) and read only inside `model.Update`/
  `model.View()` — the same goroutine. They are never read or written
  from the `runFinalizeCmd` goroutine itself (that goroutine only
  produces a `tea.Msg` return value, which Bubble Tea's runtime is
  responsible for delivering back onto the Update goroutine — this is
  the same "the tea.Cmd goroutine never touches model fields directly"
  discipline `reviewCmd`/`handleBodyEditLaunch` already follow). No new
  lock is needed for these fields because they never cross a goroutine
  boundary directly; they cross it only via the message-passing channel
  Bubble Tea's runtime already provides. **Acceptance criterion**: grep
  confirms no direct field read/write of `finalizeState`/`finalizeCancel`/
  `finalizeLastGen` appears inside any function passed as a `tea.Cmd`
  closure (i.e. inside `runFinalizeCmd`'s returned closure or
  `pollFinalizeOutputCmd`'s returned closure) — only inside
  `model.Update`/`model.View()` bodies.

**Required concurrent test**: add a test in
`internal/tui/finalize_test.go` that runs `agent.OutputCapture`'s
`AddLine` concurrently with `GetLines()`/`Generation()` calls from a
separate goroutine while a fake `finalizeCommandFunc` writes a burst of
lines — structured like the existing race-oriented tests already present
for `OutputCapture` in `internal/agent/manager_acp_test.go` or similar,
run under `go test -race`. This exercises the exact access pattern
`runFinalizeCmd`'s goroutine and the poll loop's `finalizeTickMsg` reads
will use in production, so `-race` can actually observe it (a test that
never runs both sides concurrently would prove nothing about the locking
this design relies on).

## Team Orchestration

This issue is implemented as a single cohesive change; the pieces below
have no cross-dependencies that block parallel work, since they touch
disjoint files:

- **Track A — Backend/plumbing** (`finalize.go`, `finalize_test.go`,
  `commands.go`'s `handleFinalize` + `finalizeCommandFunc` +
  `finalizeScriptPathFunc`): fully self-contained; can be built and
  unit-tested against fakes before Track B lands.
- **Track B — Model wiring** (`tui.go`'s new fields + case arms + the
  confirmation keypress interception): depends on Track A's message
  types existing (needs `finalizeDryRunMsg`/etc. to compile), but not on
  Track A's *implementation* being correct — can be developed against
  Track A's type signatures in parallel once those are stubbed.
- **Track C — Window rendering** (`review_content_tab.go`'s
  `NewLiveReviewContentTab` + `AppendFromCapture`): independent of Tracks
  A and B until final integration — only needs `agent.OutputCapture`
  (already exists) as its interface, not any of the new finalize types.
- **Track D — Command registry + README**: trivial, no dependencies,
  can land whenever.

All four tracks must be integrated into one PR (no phased delivery) —
Tracks A–C converge at `tui.go`'s new case arms, which is the last piece
to write since it is the only file that imports and wires all three
together.

## Step-by-Step Task Breakdown

### Task 1: Implement `OutputCapture`-backed finalize command primitives
**Files**: `internal/tui/finalize.go`, `internal/tui/finalize_test.go`
**Acceptance Criteria**:
- `finalizeState` enum with 4 values defined (`finalizeIdle`,
  `finalizeDryRunRunning`, `finalizeAwaitingConfirmation`,
  `finalizeLiveRunning`).
- `finalizeDryRunMsg`, `finalizeCompleteMsg`, `finalizeErrorMsg`,
  `finalizeTickMsg` types defined exactly as specified above.
- `runFinalizeCmd(ctx, dryRun, capture, cancel) tea.Cmd` implemented,
  using an injectable `finalizeCommandFunc` var (signature matching
  `exec.CommandContext`) and an injectable `finalizeScriptPathFunc` var,
  both package-level vars overridable in tests.
- `pollFinalizeOutputCmd() tea.Cmd` implemented with a
  `finalizePollInterval = 100 * time.Millisecond` constant.
- Unit tests cover: dry-run success → `finalizeDryRunMsg`; live success
  → `finalizeCompleteMsg`; script-not-found → `finalizeErrorMsg` with no
  subprocess spawned; non-zero exit → `finalizeErrorMsg` with captured
  partial output attached.
- **Concurrent test**: a test exercising `OutputCapture.AddLine`
  concurrently with `GetLines()`/`Generation()` from goroutines
  simulating the writer (subprocess capture) and reader (poll tick)
  sides, runnable under `go test -race` with no reported races.
- **Verification**: `go test ./internal/tui/... -run Finalize -race -v`
  passes.
**Dependencies**: None (can run in parallel with Task 2, Task 3).

### Task 2: Extend `ReviewContentTab` for live-capture streaming
**Files**: `internal/tui/review_content_tab.go`,
`internal/tui/review_content_tab_test.go`
**Acceptance Criteria**:
- `NewLiveReviewContentTab(id, title string, capture
  *agent.OutputCapture, styles *Styles) *ReviewContentTab` added as a
  second constructor; existing `NewReviewContentTab` signature and
  behavior are completely unchanged (verify via existing
  `review_content_tab_test.go` still passing unmodified).
- `AppendFromCapture()` method added, rebuilding viewport content from
  `capture.GetLines()` on each call.
- A test confirms `NewReviewContentTab`'s (#84's) existing static-body
  behavior is unaffected — i.e. calling `AppendFromCapture()` on a tab
  constructed via the *static* constructor is either a documented no-op
  (nil `liveCapture` guarded) or not possible by construction (field only
  set by the live constructor) — pick whichever is simpler to implement
  and document the choice in the method's doc comment.
- **Verification**: `go test ./internal/tui/... -run ReviewContent -v`
  passes; existing tests in this file remain green with no modifications
  to their assertions.
**Dependencies**: None (can run in parallel with Task 1, Task 3).

### Task 3: Wire `finalize` REPL command and registry entry
**Files**: `internal/tui/commands.go`, `internal/tui/command_registry.go`
**Acceptance Criteria**:
- `handleFinalize(args []string) (model, tea.Cmd)` added to
  `commands.go`, implementing the "Triggering a run" sequence above
  (re-entrancy guard, context creation, capture reset, window open,
  state transition, batched `tea.Cmd` return).
- `finalize` registered in `command_registry.go` with `HasArgs: false`.
- A test confirms `finalize` while `m.finalizeState != finalizeIdle`
  produces a warning activity line and no new subprocess launch (assert
  via the injectable `finalizeCommandFunc` call count staying at 0 for
  the second call).
- **Verification**: `go test ./internal/tui/... -run Finalize -v` passes;
  `finalize` appears in autocomplete (existing
  `command_registry_test.go` pattern extended or a new assertion added).
**Dependencies**: Task 1 (needs the message/state types to compile
against).

### Task 4: Wire model-level state, case arms, and confirmation keypress
**Files**: `internal/tui/tui.go`, `internal/tui/tui_test.go` (or a new
`internal/tui/finalize_integration_test.go` if a dedicated file reads
more clearly)
**Acceptance Criteria**:
- Model fields added exactly as listed under "Files to Modify" →
  `tui.go`.
- `case finalizeDryRunMsg:`, `case finalizeCompleteMsg:`, `case
  finalizeErrorMsg:`, `case finalizeTickMsg:` arms added to
  `model.Update`, implementing the transitions specified under
  "Confirmation UI / State Machine Wiring" and "Error Handling" above,
  including the re-arm-or-not logic for `finalizeTickMsg`.
- Confirmation-keypress interception added to the top-level key-handling
  switch, gated on `m.finalizeState == finalizeAwaitingConfirmation`,
  positioned so it is checked before Reviews-tab row-shortcut forwarding
  (mirroring where `confirmingExit`'s check already sits) and is provably
  mutually exclusive with `confirmingExit`'s own block (add the
  invariant comment noted above).
- A test simulates the full happy path: `finalize` REPL command →
  `finalizeDryRunMsg` (fake success) → `y` keypress →
  `finalizeCompleteMsg` (fake success) → asserts final `m.finalizeState
  == finalizeIdle` and that both a "dry-run complete" and a "finalize
  complete" activity line appear in that order.
- A test simulates the decline path: `finalize` → `finalizeDryRunMsg` →
  any non-"y" keypress → asserts `m.finalizeState == finalizeIdle`, that
  `finalizeCommandFunc` was called exactly once (never for the live run),
  and that a "cancelled" activity line appears.
- A test simulates the error path for both phases: dry-run error →
  asserts idle + error activity line + no confirmation prompt ever
  appended; live error (after a successful dry-run + confirmation) →
  asserts idle + error activity line + the dry-run's output is still
  present in `m.finalizeCapture.GetLines()` (proving nothing was
  discarded on failure).
- **Verification**: `go test ./internal/tui/... -race -v` passes in
  full (not just the new tests — the full package, to catch any
  regression in `confirmingExit`'s existing behavior from the new
  mutual-exclusion check).
**Dependencies**: Task 1, Task 3 (needs `runFinalizeCmd`/
`pollFinalizeOutputCmd`/message types and `handleFinalize` to exist).

### Task 5: Integrate live-capture window opening on dry-run launch
**Files**: `internal/tui/commands.go` (`handleFinalize`), `internal/tui/tui.go`
**Acceptance Criteria**:
- `handleFinalize` opens (or reuses, by fixed tab ID
  `"finalize-preview"`) a `NewLiveReviewContentTab` window via the
  `TabManager.AddTab`/`FindTabByID`/`switchActiveTab` sequence, matching
  the reuse pattern already established by `openReviewContentMsg`'s
  handler in `tui.go` (find-existing-by-ID before creating a new one).
- The `finalizeTickMsg` case arm in `tui.go` looks up the finalize window
  tab by its fixed ID and calls `AppendFromCapture()` on it, regardless
  of whether it is the currently active tab.
- A test confirms running `finalize` twice in a row (after the first run
  reaches idle) reuses the same tab rather than stacking a second
  `"finalize-preview"` tab.
- A test confirms output continues to accumulate in
  `m.finalizeCapture`/the tab's viewport even when a different tab is
  active during the run (simulating the user switching away mid-stream).
- **Verification**: `go test ./internal/tui/... -run Finalize -v` passes;
  manual smoke test per "Validation Commands" below shows live streaming
  in the window.
**Dependencies**: Task 2, Task 4.

### Task 6: Documentation
**Files**: `README.md`
**Acceptance Criteria**:
- `finalize` added to the REPL Commands table with a one-line
  description ("Preview and post decided PR reviews via
  finalize-reviews.sh (dry-run, then confirm)").
- A short paragraph added to the "PR Review Workflow" section describing
  the dry-run → confirmation → live flow and pointing at `decide` (#85)
  as the prerequisite step that populates decisions for `finalize` to
  act on.
**Dependencies**: None (can land whenever; informational only).

## Validation Commands

```bash
# Unit tests, full package, race-enabled (catches any regression in the
# existing confirmingExit / reviewCompleteMsg paths alongside new tests)
go test ./internal/tui/... -race -v

# Targeted finalize-related tests only, faster iteration
go test ./internal/tui/... -run Finalize -race -v
go test ./internal/tui/... -run ReviewContent -v

# Build check
go build ./...

# Manual smoke test (requires a populated ~/PR-Review/pending/ spool with
# at least one decided review, and finalize-reviews.sh resolvable on
# $PATH per custom-scripts.md's ~/.local/bin/ symlink setup):
#   1. Launch howmux, run `decide post` on a selected review (#85).
#   2. Run `finalize`.
#   3. Confirm the preview window opens and streams dry-run output.
#   4. Confirm the footer/activity prompt appears asking to post live.
#   5. Press "n" — confirm no live run happens, window content persists.
#   6. Run `finalize` again, press "y" this time — confirm live output
#      streams below a "Posting live" separator, and the Reviews tab's
#      decision_state column updates to "posted"/"done" for the affected
#      row on the very next render.
```

## Out of Scope (explicitly, per the issue's constraints)

- Reimplementing any part of `pr_review_finalize.py`'s drain logic in Go
  — this design only ever shells out to the existing script, per AC's
  explicit constraint.
- A key-menu shortcut on the Reviews tab for triggering finalize (only a
  REPL command is added) — finalize acts on the whole spool, not a
  selected row, so it doesn't fit the row-scoped key-menu pattern #85
  established for `decide`.
- Mid-run cancellation UI (see "Cancellation" above — plumbing-compatible
  but not built in this issue, since no acceptance criterion asks for
  it).
- Parsing/surfacing the full structured JSON drain summary in the UI
  beyond a best-effort activity-line count — the raw output is already
  fully visible in the streamed preview window, which satisfies AC3's
  "showing exact GitHub API payloads" requirement without needing a
  second, parsed presentation.
