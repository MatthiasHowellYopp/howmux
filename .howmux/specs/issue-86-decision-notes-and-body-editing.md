# Design Spec: PR-review gate — edit decision notes and review body

Closes #86

## Problem Statement

The Reviews tab (issue #84's read-only content window, issue #85's
`p`/`r`/`R`/`d` decision keys) lets a user select a row and set a decision,
but there is no way to explain *why* — `decision_notes` stays permanently
`""` — and no way to fix the review body itself before posting. This spec
adds two editing surfaces:

1. An in-TUI `textinput` for `decision_notes` (bound to `n`, gated to when
   the selected review's decision is `revise` or `rereview`).
2. A `$EDITOR`-launch flow for the full review body (bound to `e`), reusing
   the existing `tea.ExecProcess` suspend/resume pattern that
   `handlePlanSubprocess` (`internal/tui/commands.go`) already uses for
   `kiro-cli`.

## Codebase Findings

### The mutation boundary is a shell script, not Go-side markdown parsing

`internal/review/decisionwriter.go` (`DecisionWriter.SetDecision`) is
explicit that it "deliberately never touches the spool file's bytes
directly" — it shells out to `.howmux/scripts/set-review-decision.sh`, which
is the "sole mutation boundary for the spool's `decision:` field." That
script:

- validates argc/decision vocabulary before touching the filesystem,
- requires the first line to be exactly `---` and a closing `---` fence,
- confirms the target key exists *inside* the front-matter block via `awk`
  before writing anything,
- rewrites only that one key's value with `sed`, address-bounded to
  `2,$((CLOSING_LINE-1))` (strictly interior to the fences, so a
  `decision:`-looking string in the body is never touched),
- writes to a `mktemp` sibling file (inherits mode via `cp -p`), then does an
  atomic `mv` over the original — crash-safe, no half-written spool file,
- exits non-zero with a specific stderr message for every failure mode (bad
  args, invalid value, missing file, missing fence, missing key).

This is the pattern to extend, not re-invent. Two new scripts follow it:

- `set-review-notes.sh <spool-file-path> <notes-text>` — same shape, targets
  the `decision_notes:` key.
- `set-review-body.sh <spool-file-path> <body-file-path>` — same
  fence-detection and atomic-write discipline, but replaces everything
  *after* the closing fence with the contents of `<body-file-path>`,
  leaving the front-matter block (including `decision_notes:` and every
  other key) byte-for-byte untouched.

Doing the rewrite in the shell layer (not Go) keeps front-matter integrity
enforcement in exactly one place per field, matches the existing
`DecisionWriter` contract ("only invokes the script and inspects the exit
code / stderr"), and reuses the crash-safety property (atomic rename) for
free instead of re-deriving it in Go.

### `decision_notes` already exists in the front-matter contract

`.howmux/scripts/set-review-decision_test.sh`'s `make_valid_spool` fixture
already includes `decision_notes: ""` in the front-matter block — the key is
part of the external spool format (owned by `ai-resources/workflows/spool.py`,
per `spool.go`'s package comment) and simply has no writer yet on the Go/TUI
side. `review.ParseSpoolFrontMatter` already parses arbitrary keys into a
flat map, so reading the current value back (to pre-populate the textinput,
and to restore on cancel) requires no new parsing code — call
`ParseSpoolFrontMatter` on the resolved spool file and read `fields["decision_notes"]`.

### The suspend/resume `tea.ExecProcess` pattern (commands.go)

`handlePlanSubprocess` (`internal/tui/commands.go:~355-392`) is the exact
pattern to reuse for `$EDITOR`, structurally:

1. `m.manager.SuspendOutputCapture()` before handing over the terminal.
2. Snapshot state that must survive the subprocess into `m.consoleState`
   (input value, activity lines, active tab ID) — for body editing there is
   an equivalent piece of state to snapshot: which review is being edited
   and its pre-edit spool contents (for restore-on-failure), not console
   chrome.
3. Build the `exec.Cmd`.
4. `return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{err: err} })`.
5. `model.Update`'s `case execDoneMsg:` (`tui.go:411`) is the resume half:
   `m.manager.ResumeOutputCapture()`, report success/failure via
   `appendActivity`, `m = m.restoreConsoleState()`, refocus, `tea.ClearScreen`.

**Reuse decision**: rather than overload the single existing `execDoneMsg`
(which currently always means "planning subprocess exited" and always calls
`restoreConsoleState()` — wrong for body editing, which has nothing to do
with console/planning state), introduce a **new** message
`editorDoneMsg{tempPath string, rec review.Record, err error}` with its own
`case` arm in `model.Update`. This keeps `execDoneMsg`'s existing contract
untouched (no risk to planning mode) while reusing the exact same
`tea.ExecProcess` mechanics and the same
`SuspendOutputCapture`/`ResumeOutputCapture` pair around the call.

### The existing `textinput` pattern (autocomplete.go)

`AutocompleteInput` wraps a single `charm.land/bubbles/v2/textinput.Model`,
constructed via `textinput.New()`, configured (`ti.Prompt`, cursor style via
`ti.Styles()`/`ti.SetStyles()`), and exposes `Focus()`/`Blur()`/`SetFocus()`/
`Value()`/`SetValue()`/`Update(tea.Msg) (*T, tea.Cmd)`/`View() string`. The
new `decision_notes` editor follows the same shape as a small, purpose-built
wrapper type (`NotesInput` or inline on `ReviewsTab`) — no autocomplete
suggestions needed, so it is simpler than `AutocompleteInput`, but the
Focus/Blur/Update/View skeleton and the "delegate to the underlying
`textinput.Model.Update` for anything not specially handled" idiom carry
over directly.

### Reviews tab focus model (reviews_tab.go, tui.go, focus_state.go)

`ReviewsTab` reports `FocusTargetRows` from `CaptureFocusState()` — the
tab's own key handling (`up`/`down`/`enter`/`p`/`r`/`R`/`d`) is the default,
and `Tab`/`Shift+Tab` toggle to `FocusTargetFooter` via
`toggleReviewsFocus()`/`setReviewsFocus()` (`tui.go`). The `default:` arm in
`model.Update`'s big key-message switch (around `tui.go:1026`, "AC5a") is
where `p`/`r`/`R`/`d` are forwarded to `ReviewsTab.Update` **only when the
footer input does NOT have focus** — otherwise the same keystrokes type into
the footer normally. `n` and `e` must be gated identically: **new row-mode
keys, not new footer commands**, dispatched through
`ReviewsTab.Update`'s `switch keyMsg.String()` alongside `p`/`r`/`R`/`d`,
under the same "footer not focused" guard already in place.

This means `ReviewsTab` needs **its own internal editing sub-state**
(distinct from row-navigation) for the `decision_notes` textinput — while
that sub-state is active, further keystrokes (except Enter/Esc) must go to
the textinput, not be reinterpreted as `p`/`r`/`R`/`d`/`up`/`down`. This is
a third focus mode *within* the Reviews tab, layered under the existing
row-vs-footer duality: row-navigation → notes-editing → (Enter saves /
Esc cancels) → back to row-navigation. `$EDITOR` launch (`e`) does not need
this in-tab sub-state because it fully suspends the TUI event loop via
`tea.ExecProcess` — there is no intermediate "typing" state to protect
inside `ReviewsTab.Update` itself, only the top-level suspend/resume.

### `openReviewContentMsg` / `decideRequestMsg` message-passing convention

Both existing cross-tab messages follow the same shape: `ReviewsTab.Update`
constructs a small message struct carrying only plain values resolved from
the selected `review.Record` (never a pointer back into `ReviewsTab` or
`TabManager`), returns a `tea.Cmd` that emits it, and `model.Update` has the
corresponding `case` arm that does the actual cross-cutting work (opening a
tab, calling `DecisionWriter`). The new work follows the same shape:

- `startNotesEditMsg{repo, pr, spoolPath, currentNotes string}` — emitted by
  `n`, gated by `decision` state (see below); handled in `model.Update` by
  entering notes-edit sub-state.
- `saveNotesMsg{repo, pr, spoolPath, notes string}` — emitted by `ReviewsTab`
  (or by `model` directly, since the textinput can live at the `model`
  level — see Task 1 for the decision) on Enter; handled by invoking the new
  `NotesWriter.SetNotes`.
- `startBodyEditMsg{repo, pr, spoolPath string}` — emitted by `e`; handled
  in `model.Update` by writing the current body to a temp file and issuing
  `tea.ExecProcess`.
- `editorDoneMsg{tempPath string, rec review.Record, err error}` — emitted
  by the `tea.ExecProcess` callback; handled by reading the temp file back,
  invoking the new `BodyWriter.SetBody`, and restoring TUI state.

### Where "gated to revise/rereview" is checked

`review.SpoolInfo.Decision` (raw front-matter value, from
`ReadSpoolInfo`/`buildSpoolInfo` in `spool.go`) is already read by
`ReviewsTab.resolveSpoolInfo` for every row on every render. The `n` key's
gating check ("only offered/active when decision is revise/rereview") reads
this same field for the *selected* row — no new I/O, just an `SpoolInfo.Decision`
comparison (case-insensitively, matching `ClassifySpoolState`'s existing
`strings.ToLower` convention) against `"revise"`/`"rereview"` before emitting
`startNotesEditMsg`. If the gate fails, emit an activity-line error
(matching `applyDecision`'s existing error-feedback shape) instead of a
silent no-op, so the user understands why `n` did nothing — silent no-ops
elsewhere in this codebase (e.g. `decideSelectedCmd` on empty selection) are
reserved for "nothing is selected at all," a different condition than "a
row is selected but doesn't qualify."

## Solution Approach

### Component ownership split

| Concern | Owner | Rationale |
|---|---|---|
| `decision_notes` textinput widget + Focus/Blur/Update/View | New `NotesInput` type, held on `model` (not `ReviewsTab`) | Mirrors where `m.input` (the footer `AutocompleteInput`) already lives — `model` is the existing home for focusable text-entry widgets, and this avoids threading a `tea.Model`-shaped sub-widget through the `Tab` interface, which no existing tab does |
| Gating check (decision == revise/rereview) + key dispatch (`n`) | `ReviewsTab.Update`, alongside `p`/`r`/`R`/`d` | Same place the other row-shortcuts already live; `ReviewsTab` already reads `SpoolInfo` for the selected row via the same `resolveSpoolInfo`/`SelectedRecord` machinery |
| Notes-edit sub-mode (once active, intercept keys before row-shortcuts) | `model` level, via a new `notesEditActive bool` + `notesEditTarget review.Record` on `model`, checked at the *top* of the key-message switch in `model.Update`, before the Reviews-tab-specific forwarding block | Consistent with how footer-focus already pre-empts row-shortcuts (`m.input.Focused()` check) — notes-edit is a third, even-higher-priority focus state that must intercept before both footer-forwarding and row-forwarding |
| `$EDITOR` launch, temp file write/read, `tea.ExecProcess` | `model` level (`commands.go`), mirroring `handlePlanSubprocess` | Only `model` has `SuspendOutputCapture`/`ResumeOutputCapture` and the pattern to copy |
| Spool mutation (write `decision_notes:` / replace body) | Two new shell scripts + two new thin Go wrappers (`NotesWriter`, `BodyWriter`) in `internal/review/`, mirroring `DecisionWriter`/`set-review-decision.sh` exactly | Keeps the single mutation boundary per field, reuses the atomic-write/fence-scoping discipline instead of re-implementing it in Go |
| Backup/restore | Inside each new shell script (atomic temp-file + rename, i.e. the original is simply never overwritten in place) **and** at the Go level for the body-edit flow specifically (see below) | The shell script's atomicity already protects against a crash mid-write; the *additional* Go-level backup is needed only for body editing, because the failure window there is much larger (the user has an entire `$EDITOR` session to make the file invalid or the process to be killed) |

### Why body editing needs an extra Go-level backup, but notes editing does not

`decision_notes` edits are: read current value → user edits a *bounded,
already-validated-by-construction string in a single-line textinput* → on
Enter, call `NotesWriter.SetNotes` once. The shell script's own atomicity is
sufficient; there is no intermediate on-disk state to protect because the
Go side never writes anything until the final, complete value is ready.

Body edits are: read current body → write it to a **temp file on disk** →
suspend the TUI → hand control to an external process for an **unbounded
duration** → resume → **read the temp file back**, which the user's editor
may have corrupted, emptied, or left in $EDITOR's swap-file state if it
crashed. The risk surface is the *temp file*, not the spool file — the spool
file itself is never touched until `BodyWriter.SetBody` is called with a
validated, non-empty temp-file path at the very end. So "backup before
editing" here concretely means: keep the pre-edit body available in memory
(captured in the `startBodyEditMsg`/`editorDoneMsg` round trip, not written
anywhere new) so that if the temp file comes back invalid, `model.Update`
can report the failure and **skip** calling `BodyWriter.SetBody` entirely —
the spool file is simply never touched, which is a stronger guarantee than
"restore a backup." No separate `.bak` file is needed on top of the shell
script's own atomic-rename safety.

### Front-matter preservation

- **Notes**: `set-review-notes.sh` follows `set-review-decision.sh` exactly
  — fence-detect, confirm `decision_notes:` key exists inside the block,
  `sed`-replace only that line's value (properly shell-escaping the notes
  text so embedded `:`, `&`, or `/` in user-typed notes can't break the
  `sed` substitution — `set-review-decision.sh` doesn't need this care
  because its value vocabulary is a fixed 4-word enum, but free-text notes
  do; see Task 3 for the escaping approach), atomic temp+rename.
- **Body**: `set-review-body.sh` fence-detects the *closing* `---` line,
  keeps lines `1..closing` verbatim (the entire front-matter block,
  untouched), and replaces everything from `closing+1` onward with the
  contents of the temp file, again via temp+rename. This guarantees the
  front-matter block — including whatever `decision_notes:` value is
  currently set — survives a body edit unchanged, addressing AC7 directly.

### Validation

- `set-review-notes.sh` / `set-review-body.sh` both re-validate the fence
  structure independently before writing (never trust the caller), matching
  `set-review-decision.sh`'s existing defense-in-depth comment
  ("the actual mutation boundary must never trust any caller").
- The Go wrappers (`NotesWriter.SetNotes`, `BodyWriter.SetBody`) validate
  obviously-wrong inputs before shelling out at all (empty `spoolPath`,
  already-finalized/`done/` spool file via the existing
  `resolveSpoolPath` helper) — mirroring `DecisionWriter.SetDecision`'s
  existing three-tier error messages (already finalized / no spool file /
  script missing).
- Body edit additionally validates the **temp file is non-empty** after the
  editor exits, before calling `BodyWriter.SetBody` — an empty body is
  almost certainly an accidental full-delete-and-save, and posting an empty
  review body downstream would be a much worse failure than refusing the
  save and reporting "editor produced an empty file; body not changed."

### Error handling

| Failure | Where caught | User-facing behavior |
|---|---|---|
| `$EDITOR` not set | Before `tea.ExecProcess` is even issued, at `startBodyEditMsg` handling time | Activity-line error: `"$EDITOR is not set — cannot edit review body"`; no subprocess launched, no temp file left behind |
| `$EDITOR` exits non-zero | `editorDoneMsg.err != nil` | Activity-line error including the exit error; spool file untouched (matches `execDoneMsg`'s existing error-reporting shape for planning-subprocess failures) |
| Edited file is empty | `editorDoneMsg` handler, after successful exit | Activity-line error: `"Editor produced an empty file — review body not changed"`; `BodyWriter.SetBody` is never called |
| `set-review-notes.sh` / `set-review-body.sh` exits non-zero | `NotesWriter.SetNotes` / `BodyWriter.SetBody` return `error` | Activity-line error via the same `applyDecision`-style rendering (`m.styles.Error.Render(...)`) |
| User presses Esc during notes-edit | `model.Update`'s notes-edit-mode key handling | No write attempted at all; activity-line info message `"Notes edit cancelled"`; textinput discarded, revert to row-navigation |
| Temp file cleanup | `editorDoneMsg` handler, in all branches (success or failure) | `os.Remove` the temp file unconditionally via `defer`-equivalent at the point where the temp path is known, so a crashed/misbehaving editor never leaves stray files in `os.TempDir()` |

### Visual feedback

- **Entering notes-edit mode**: footer transient message (via
  `FooterManager.SetTransientMessage`, the existing mechanism used for
  Ctrl+Y copy feedback) — e.g. `"Editing notes for PR #123 (Enter to save, Esc to cancel)"`.
  This reuses an existing, already-wired status-row mechanism rather than
  inventing a new footer state.
- **Saving notes**: activity-line success message, matching
  `applyDecision`'s existing pattern — `"Saved decision notes for PR #123"`.
- **Launching editor**: activity-line info message before suspending —
  `"Opening $EDITOR for PR #123 review body..."` — so the transition from
  TUI to external process has a visible cause in the scrollback once
  control returns.
- **Editor returns successfully**: activity-line success —
  `"Updated review body for PR #123"`.
- **All error cases**: `m.styles.Error.Render(...)` activity lines, per the
  table above — no new styling primitives needed, `Styles.Error` /
  `Styles.Success` / `Styles.Warning` already exist and are used identically
  throughout `commands.go`.

## Concurrency Analysis

This feature introduces **no new cross-goroutine shared state**. All new
state (`model.notesEditActive`, `model.notesEditTarget`, the `NotesInput`
widget, the temp-file path held across the `tea.ExecProcess` round trip) is
read and written exclusively on the Bubble Tea event-loop goroutine — the
same goroutine that already owns `model` entirely, exactly like
`m.consoleState`, `m.currentMode`, and every other `model` field mutated by
`handlePlanSubprocess`/`execDoneMsg`. `tea.ExecProcess`'s callback
(`editorDoneMsg`) is delivered back onto the event loop by Bubble Tea's own
`tea.Cmd` contract, not invoked directly from a spawned goroutine touching
`model` — this is the same contract `execDoneMsg` already relies on for
`kiro-cli` today, so no new locking, no new accessor methods, and no new
concurrent test are required.

The two new shell scripts run as short-lived subprocesses
(`exec.Command("bash", ...)`) exactly like `set-review-decision.sh` already
does via `execCommandFunc` — synchronous, blocking calls from the event-loop
goroutine, not background work. No new goroutine boundary is created.

## Relevant Files

### New files

| Path | Purpose |
|---|---|
| `.howmux/scripts/set-review-notes.sh` | Mutation boundary for `decision_notes:` front-matter field |
| `.howmux/scripts/set-review-notes_test.sh` | Standalone bash test suite, mirroring `set-review-decision_test.sh` |
| `.howmux/scripts/set-review-body.sh` | Mutation boundary for replacing the markdown body while preserving front-matter |
| `.howmux/scripts/set-review-body_test.sh` | Standalone bash test suite, mirroring `set-review-decision_test.sh` |
| `internal/review/noteswriter.go` | `NotesWriter.SetNotes(spoolPath, notes string) error` — thin Go wrapper, mirrors `decisionwriter.go` |
| `internal/review/noteswriter_test.go` | Unit tests mirroring `decisionwriter_test.go`'s fake-exec-command pattern |
| `internal/review/bodywriter.go` | `BodyWriter.SetBody(spoolPath, bodyFilePath string) error` — thin Go wrapper |
| `internal/review/bodywriter_test.go` | Unit tests mirroring `decisionwriter_test.go` |
| `internal/tui/notes_input.go` | `NotesInput` type wrapping `bubbles/v2/textinput.Model`, mirrors `AutocompleteInput`'s Focus/Blur/Update/View skeleton (no autocomplete needed) |
| `internal/tui/notes_input_test.go` | Unit tests for `NotesInput` in isolation |
| `internal/tui/notes_edit_test.go` | Integration-style tests for the `n` key → textinput → Enter/Esc round trip through `model.Update` |
| `internal/tui/body_edit_test.go` | Integration-style tests for the `e` key → temp file → `editorDoneMsg` round trip through `model.Update`, using an injectable exec-command seam (see Task 4) |

### Modified files

| Path | Change |
|---|---|
| `internal/review/spool.go` | No parsing changes needed — `ParseSpoolFrontMatter`/`fields["decision_notes"]` already covers reading the current value. Confirm via a new small helper `CurrentDecisionNotes(spoolPath, homeDir string) string` for symmetry with `ReadSpoolInfo`, OR call `ParseSpoolFrontMatter` directly from `commands.go` — see Task 1 decision point. |
| `internal/tui/tui.go` | Add `notesEditActive bool`, `notesEditTarget review.Record` (or minimal equivalent) fields to `model`; add `NotesInput` field; add message types `startNotesEditMsg`, `saveNotesMsg`, `startBodyEditMsg`, `editorDoneMsg`; add corresponding `case` arms in `model.Update`; add top-of-switch interception for notes-edit-mode keys (Enter/Esc/all-else-forwarded-to-textinput), positioned before the existing Reviews-tab `p`/`r`/`R`/`d` forwarding block |
| `internal/tui/reviews_tab.go` | Add `n`/`e` cases to `ReviewsTab.Update`'s key switch, alongside `p`/`r`/`R`/`d`; add gating check (decision == revise/rereview, case-insensitive) for `n` using the already-available `SpoolInfo`; add `startNotesEditCmd()`/`startBodyEditCmd()` helper methods mirroring `decideSelectedCmd`'s shape |
| `internal/tui/commands.go` | Add `handleBodyEditLaunch` (or similarly-named) helper implementing the `tea.ExecProcess` suspend/write-temp-file/launch sequence, structurally mirroring `handlePlanSubprocess`; add `handleEditorDone` helper implementing the resume/read-temp-file/validate/`BodyWriter.SetBody`/cleanup sequence; wire `decisionWriter`-style `notesWriter *review.NotesWriter` and `bodyWriter *review.BodyWriter` fields onto `model` (constructed in `NewModel`, mirroring how `decisionWriter` is already constructed) |
| `internal/tui/footer.go` | No structural change — reuse existing `SetTransientMessage` for the "editing notes" indicator. (Only touched if a dedicated helper method is judged clearer than calling `SetTransientMessage` directly from `commands.go`; default to no change.) |
| `internal/tui/focus_state.go` | Comment-only update if a new `FocusTarget` constant is judged necessary for notes-edit mode — default to NOT adding one, since notes-edit is modeled as a `model`-level bool flag intercepted before focus routing, not a new `FocusTarget` value routed through `CaptureFocusState`/`RestoreFocusState` (those are tab-capture concepts; notes-edit is transient overlay-like state, closer to how `m.activeOverlay` already works for help/about/status) |
| `internal/tui/command_registry_test.go` | No new REPL commands are being added (this is a key-binding-only feature, not a `decide`-style REPL command) — confirm no registry changes needed; add a note/assertion only if the review turns up a reason `n`/`e` should also be reachable via typed command (out of scope per the issue's key-binding-first framing) |

### Existing test files needing updates

| Path | Why |
|---|---|
| `internal/tui/reviews_tab_test.go` | Add cases for `n`/`e` key dispatch, including the gating check (decision != revise/rereview → no message emitted, activity-line error) |
| `internal/tui/decide_routing_test.go` | Add routing cases proving `n`/`e` are intercepted identically to `p`/`r`/`R`/`d` when the footer has focus (i.e., typed into the footer, not treated as shortcuts) — mirrors this file's existing AC5a-style tests |
| `internal/tui/commands_decide_test.go` | No direct change expected (that file covers the `decide` REPL command specifically), but review for any shared test-model constructor (`newDecideTestModel`) that now also needs `notesWriter`/`bodyWriter` fields populated so newly-added `model` fields don't panic on zero-value access in unrelated tests |
| `internal/tui/review_content_tab_test.go` / `internal/tui/review_content_window_test.go` | No behavior change expected to `ReviewContentTab` itself — confirm no incidental breakage from new `model` fields; only touch if a test helper needs the new fields populated |
| `internal/tui/mode_switching_test.go` | Add a case (or confirm existing coverage) that entering notes-edit mode and body-edit mode are properly exclusive with planning-mode/console-mode transitions — e.g. body-edit's suspend/resume must not corrupt `m.currentMode` the way `restoreConsoleState()` is scoped specifically to planning mode today |
| `internal/tui/footer_test.go` | Add a case confirming `SetTransientMessage` is invoked with the expected notes-edit-mode string (if `commands.go` calls it directly) |

## Team Orchestration

Tasks 1–2 (notes editing) and Tasks 4–5 (body editing) are independent
feature slices sharing only the `model` struct shape and the `ReviewsTab`
key-switch — they can be built in parallel by separate builder agents.
Task 3 (notes backup/save/cancel) depends on Task 1. Task 5 (body
backup/reintegration) depends on Task 4. Task 6 (footer/status feedback)
depends on both slices existing (Tasks 1–5) since it wires visible strings
into both flows. Task 7 (tests) depends on everything and should run last,
though test *files* can be scaffolded earlier in parallel — the acceptance
criteria below assume Task 7 is the final integration/verification pass.

```
Task 1 (notes state+wiring) ──> Task 2 (n key + gating) ──> Task 3 (notes save/cancel)
Task 4 (editor launch)      ──> Task 5 (body backup/reintegration)
                                                     \
Task 3 + Task 5 ─────────────────────────────────────┴──> Task 6 (feedback) ──> Task 7 (tests)
```

Tasks 1 and 4 have no dependency on each other and can run in parallel.

## Step-by-Step Task Breakdown

### Task 1: Add `decision_notes` textinput state and wiring

**Scope**: `internal/tui/notes_input.go` (new), `internal/tui/tui.go`
(model fields + message types), `internal/review/noteswriter.go` (new,
minimal — just enough to compile; full behavior in Task 3).

**Work**:
- Create `NotesInput` in `internal/tui/notes_input.go`: a struct wrapping
  `textinput.Model`, with `New()`/`Focus()`/`Blur()`/`Focused()`/`Value()`/
  `SetValue(string)`/`Update(tea.Msg) (*NotesInput, tea.Cmd)`/`View() string`,
  mirroring `AutocompleteInput`'s shape exactly but with no suggestions
  logic. Prompt text should identify context, e.g. `ti.Prompt = "notes> "`.
- Add to `model`: `notesInput *NotesInput`, `notesEditActive bool`,
  `notesEditTarget review.Record` (the record being edited, so save/cancel
  know which spool file to act on without re-resolving selection).
- Construct `notesInput` in `NewModel` alongside `input` (the existing
  footer `AutocompleteInput`).
- Define message types in `tui.go`: `startNotesEditMsg{repo string; pr int; spoolPath string; currentNotes string}`
  and `saveNotesMsg{repo string; pr int; spoolPath string; notes string}`
  (structurally next to `openReviewContentMsg`/`decideRequestMsg`, same doc-comment style explaining the round trip).
- Add a package-level stub `NotesWriter` type in `internal/review/noteswriter.go`
  with `SetNotes(spoolPath, notes string) error` returning `nil` — full
  script-invocation behavior lands in Task 3, but the type must exist now so
  `model` can hold a `notesWriter *review.NotesWriter` field and compile.

**Acceptance Criteria**:
- `NotesInput` compiles and has unit tests in `notes_input_test.go` covering
  `Focus`/`Blur`/`Focused`/`SetValue`/`Value`/`View` in isolation (no `model`
  dependency), mirroring the style of `autocomplete_test.go`.
- `model` has the four new fields (`notesInput`, `notesEditActive`,
  `notesEditTarget`, `notesWriter`) and constructs them in `NewModel`
  without panics (add/extend a `TestNewModel`-style smoke test if one
  exists, or confirm via `mode_switching_test.go`'s existing model
  construction helpers).
- `go build ./...` and `go vet ./...` pass.
- No existing test's model-construction helper (e.g. `newDecideTestModel`,
  `newDecideRoutingTestModel`) breaks due to the new fields — zero-value
  `notesInput`/`notesWriter` (nil pointers) must not be dereferenced by any
  code path these tests exercise yet, since wiring lands in later tasks.

**Dependencies**: None.

---

### Task 2: Implement `n` key binding with revise/rereview gating

**Scope**: `internal/tui/reviews_tab.go`, `internal/tui/tui.go` (top-level
key interception scaffold, `startNotesEditMsg` handler entering edit mode).

**Work**:
- In `ReviewsTab.Update`'s key switch, add `case "n":` alongside
  `p`/`r`/`R`/`d`. It must:
  - Resolve the selected record via `rt.SelectedRecord()` (same as
    `decideSelectedCmd`); no-op if nothing selected (same silent-no-op
    convention as `openSelectedReviewCmd`/`decideSelectedCmd`).
  - Resolve `SpoolInfo` for that record (via `spoolInfoForFunc`, same seam
    `resolveSpoolInfo` already uses) to read `Decision`.
  - If `Decision` (lower-cased) is neither `"revise"` nor `"rereview"`,
    return a `tea.Cmd` that emits an activity-line-error-shaped message —
    introduce a minimal `gateFailedMsg{reason string}` or reuse an existing
    generic error-activity message type if one exists; if not, add one
    small enough to not warrant its own file (co-locate in `tui.go` near
    `decideRequestMsg`).
  - Otherwise, read the current `decision_notes` value (via
    `ParseSpoolFrontMatter` on the resolved spool path — reuse
    `resolveSpoolPath`/`os.ReadFile`, following `ReadSpoolInfo`'s exact
    resolution order so notes-editing sees the same file `ReadSpoolInfo`
    would) and return a `tea.Cmd` emitting `startNotesEditMsg` populated
    with `currentNotes`.
- In `model.Update`, add `case startNotesEditMsg:` that sets
  `m.notesEditActive = true`, `m.notesEditTarget = <resolved record>`,
  `m.notesInput.SetValue(msg.currentNotes)`, `m.notesInput.Focus()`, and
  triggers the footer transient message (stub text acceptable here; full
  wording in Task 6).
- Add the top-of-switch interception in `model.Update`'s key-message
  handling: when `m.notesEditActive`, forward all keys to
  `m.notesInput.Update` except `enter`/`esc`, which are handled specially
  (stubbed as no-ops for now — full behavior in Task 3). This interception
  must run **before** the existing Reviews-tab `p`/`r`/`R`/`d`/Tab/Shift+Tab
  forwarding block, so that typing in the notes textinput while
  `notesEditActive` is true never falls through to row-shortcut handling.

**Acceptance Criteria**:
- Pressing `n` on a row whose decision is `revise` or `rereview` (any
  casing) enters notes-edit mode: `m.notesEditActive == true`, textinput is
  focused and pre-populated with the current `decision_notes` value.
- Pressing `n` on a row whose decision is anything else (including empty)
  does NOT enter notes-edit mode and produces a visible activity-line error
  explaining why (e.g. "Set decision to revise or rereview before editing
  notes").
- Pressing `n` with no row selected is a silent no-op (matches
  `decideSelectedCmd`'s existing convention for the same condition).
- While `notesEditActive` is true, arrow keys / `p`/`r`/`R`/`d` do NOT
  trigger row navigation or decision-setting — they are consumed by the
  textinput (even though Enter/Esc handling is still a stub in this task,
  the interception itself must already be in place and tested).
- `decide_routing_test.go`-style tests added confirming the interception
  ordering (notes-edit mode beats Reviews-tab forwarding beats footer
  forwarding) — mirrors that file's existing AC5a pattern.
- `n`/`e` do NOT appear in `CommandRegistry` (confirm via
  `command_registry_test.go` that no new REPL command was accidentally
  registered — this stays a key-binding-only feature).

**Dependencies**: Task 1.

---

### Task 3: Implement notes backup/restore + save-on-Enter/cancel-on-Esc

**Scope**: `internal/review/noteswriter.go` (full implementation),
`.howmux/scripts/set-review-notes.sh` (new), `internal/tui/tui.go` (Enter/Esc
handling), `internal/tui/commands.go` (if a dedicated handler helper is
warranted).

**Work**:
- Write `.howmux/scripts/set-review-notes.sh`, copying
  `set-review-decision.sh`'s structure:
  - Usage: `set-review-notes.sh <spool-file-path> <notes-text>`.
  - Same fence-detection and `decision_notes:`-key-exists-inside-fence
    validation as `set-review-decision.sh` does for `decision:`.
  - **Escaping**: unlike `set-review-decision.sh` (fixed 4-word vocabulary,
    safe to interpolate directly into a `sed` pattern), `<notes-text>` is
    free text from a user. Escape it for safe use as a `sed` replacement
    value (escape `&`, `\`, and the delimiter character; reject or escape
    embedded newlines since the field is a single front-matter line — a
    multi-line value would corrupt the flat `key: value` contract). Prefer
    picking a `sed` delimiter unlikely to collide (e.g. `|`) and escaping
    any literal `|` in the input, rather than trying to escape `/`.
  - Same atomic temp-file + `mv` write-back as `set-review-decision.sh`.
  - Same exit-code contract shape (usage error, missing file, missing
    fence/key) — reuse the same numeric codes where the failure mode
    matches, document any new code if a notes-specific failure mode (e.g.
    "notes contains a bare newline") is added.
- Write `.howmux/scripts/set-review-notes_test.sh`, mirroring
  `set-review-decision_test.sh`'s case list (missing args, nonexistent
  file, no fence, no key, valid update with rest-of-file-unchanged check,
  idempotency, and a body-text-containing-`decision_notes:`-like-string
  case analogous to the existing "body decision:-like string" case) plus
  new cases specific to notes: notes containing `:`, `&`, `/`, and a
  single-quote, all surviving round-trip intact.
- Implement `NotesWriter.SetNotes(spoolPath, notes string) error` in
  `internal/review/noteswriter.go`, structurally identical to
  `DecisionWriter.SetDecision` (resolve spool path via `resolveSpoolPath`,
  the same three-tier error messages for already-finalized / missing spool
  / missing script, invoke via an `execCommandFunc`-style seam so tests can
  fake it exactly like `decisionwriter_test.go` does).
- Wire Enter/Esc in `model.Update`'s notes-edit interception (from Task 2):
  - `esc`: `m.notesEditActive = false`, blur the textinput, clear the
    footer transient message, append an info-level activity line ("Notes
    edit cancelled"). No write attempted.
  - `enter`: read `m.notesInput.Value()`, call
    `m.notesWriter.SetNotes(m.notesEditTarget.SpoolPath, value)`; on error,
    append an error activity line and **stay in edit mode** (so the user
    doesn't lose their typed text on a transient failure — matches the
    principle that a failed write must not silently discard user input);
    on success, `m.notesEditActive = false`, blur, clear transient message,
    append a success activity line.

**Acceptance Criteria**:
- `set-review-notes_test.sh` passes all cases including the new
  escaping-specific ones; run via the existing `Taskfile.yml` test target
  pattern (confirm how `set-review-decision_test.sh` is currently wired
  into `task test` / CI and add `set-review-notes_test.sh` the same way).
- `NotesWriter.SetNotes` unit tests (`noteswriter_test.go`) mirror
  `decisionwriter_test.go`'s fake-exec-command coverage: success, script
  exit non-zero with stderr message, already-finalized spool, missing
  spool, missing script.
- Pressing Enter in notes-edit mode with a value containing `:`, `&`, or a
  single-quote round-trips correctly through the real script (integration
  test using a real temp spool file, not just the faked exec seam) and
  every other front-matter field is byte-for-byte unchanged.
- Pressing Esc discards the typed value; re-opening notes-edit (`n` again)
  on the same row shows the **original** unmodified `decision_notes` value,
  not the discarded edit.
- A `SetNotes` failure (simulated via the fake-exec seam) leaves
  `notesEditActive == true` and the typed value intact in the textinput.

**Dependencies**: Task 1, Task 2.

---

### Task 4: Implement `$EDITOR` launch via `tea.ExecProcess` for full body edit

**Scope**: `internal/tui/reviews_tab.go` (`e` key), `internal/tui/commands.go`
(launch handler), `internal/tui/tui.go` (message types + `editorDoneMsg`
case scaffold).

**Work**:
- In `ReviewsTab.Update`, add `case "e":` alongside `p`/`r`/`R`/`d`/`n`.
  Resolves the selected record (same pattern as `n`); no gating check is
  required for body editing (unlike notes, the issue does not restrict body
  editing to any particular decision state) — no-op if nothing selected.
  Returns a `tea.Cmd` emitting `startBodyEditMsg{repo, pr, spoolPath}`.
- Add `startBodyEditMsg` and `editorDoneMsg{tempPath string; rec review.Record; err error}`
  types in `tui.go`, doc-commented in the same style as
  `openReviewContentMsg`/`decideRequestMsg`, explicitly noting this is a
  **distinct** message from the pre-existing `execDoneMsg` (used by
  planning-subprocess launches) because the resume behavior differs
  (`restoreConsoleState()` must NOT be called here — see Solution Approach).
- Implement a new handler in `commands.go`, e.g. `handleBodyEditLaunch(m model, rec review.Record) (model, tea.Cmd)`,
  structurally mirroring `handlePlanSubprocess`:
  1. Read `$EDITOR` via `os.Getenv("EDITOR")`; if empty, append an error
     activity line and return `m, nil` immediately — no subprocess, no temp
     file, no suspend/resume side effects at all.
  2. Resolve the current body via `review.ReadSpoolBody(rec.SpoolPath, homeDir)`
     (reuse `userHomeDirFunc`, same as every other spool read in this
     package).
  3. Write the body to a fresh temp file (`os.CreateTemp("", "howmux-review-*.md")`
     or similar) — this is the "backup" per the Solution Approach reasoning
     (the pre-edit content lives in this file; nothing is overwritten until
     `BodyWriter.SetBody` runs at the very end).
  4. `m.manager.SuspendOutputCapture()`.
  5. Build `exec.Command(editorBin, tempPath)` — split `$EDITOR` on
     whitespace first in case it contains flags (e.g. `EDITOR="code -w"`),
     matching common `$EDITOR` conventions other CLI tools support.
  6. `return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{tempPath: tempPath, rec: rec, err: err} })`.
  7. Append an info activity line before returning ("Opening $EDITOR for PR
     #N review body...").
- Add a **scaffold-only** `case editorDoneMsg:` in `model.Update` that calls
  `m.manager.ResumeOutputCapture()`, logs success/failure, and unconditionally
  removes the temp file — full read-back/validate/`BodyWriter.SetBody`
  logic lands in Task 5, but the resume half must exist now so the
  suspend/resume round trip is testable end-to-end without leaking real
  `$EDITOR` subprocesses in CI (inject a fake command via a seam, mirroring
  `ensureCheckoutFunc`/`runReviewFunc`'s existing pattern in `commands.go`,
  so tests substitute e.g. `/bin/true` or `/bin/false` instead of spawning
  a real editor).

**Acceptance Criteria**:
- Pressing `e` on a row with a spool file present: `$EDITOR` (faked in
  tests) launches against a temp file containing the current body; the TUI
  output capture is suspended for the duration (`SuspendOutputCapture`
  called exactly once before `tea.ExecProcess`).
- `$EDITOR` unset: pressing `e` produces an activity-line error and does
  **not** call `tea.ExecProcess`, `SuspendOutputCapture`, or create a temp
  file (assert via the exec-command seam's call count staying at 0, and via
  `os.TempDir()` listing/glob in the test, matching how filesystem-effect
  tests elsewhere in this codebase assert non-mutation).
- `e` with no row selected is a silent no-op (same convention as `n`/`p`/`r`/`R`/`d`).
- `body_edit_test.go` covers: editor exits 0, editor exits non-zero,
  `$EDITOR` unset — using the injectable command seam, no real subprocess
  spawned in CI.
- `execDoneMsg`'s existing behavior (planning-mode resume,
  `restoreConsoleState()`) is unaffected — add/confirm a regression test
  that planning-mode suspend/resume still works exactly as before Task 4's
  changes (no shared-state leakage between `execDoneMsg` and the new
  `editorDoneMsg`).

**Dependencies**: None (parallel with Task 1).

---

### Task 5: Implement body backup/restore + front-matter-preserving reintegration

**Scope**: `internal/review/bodywriter.go` (new),
`.howmux/scripts/set-review-body.sh` (new), `internal/tui/tui.go`
(`editorDoneMsg` full handling, replacing Task 4's scaffold).

**Work**:
- Write `.howmux/scripts/set-review-body.sh`:
  - Usage: `set-review-body.sh <spool-file-path> <body-file-path>`.
  - Validates `<spool-file-path>` exists and has a valid fence (first line
    `---`, a closing `---` found) — same checks as
    `set-review-decision.sh`, reusing its exit-code conventions (3 = spool
    not found, 4 = no fence) plus a new code (e.g. 5) for
    "`<body-file-path>` not found / not a regular file" and another (e.g.
    6) for "`<body-file-path>` is empty."
  - On success: writes lines `1..closing` from the original spool file
    verbatim, followed by a single blank line (matching
    `extractSpoolBody`'s existing "trim one leading newline after the
    fence" convention, applied in reverse — i.e. reintroduce exactly the
    separator `ReadSpoolBody` strips, so a subsequent `ReadSpoolBody` call
    round-trips), followed by the full contents of `<body-file-path>`, to a
    temp file, then atomic `mv` over the original — same
    crash-safety property as `set-review-decision.sh`.
  - Never touches bytes 1..closing — this is the literal front-matter
    preservation guarantee.
- Write `.howmux/scripts/set-review-body_test.sh` mirroring
  `set-review-decision_test.sh`'s structure, with cases: missing args,
  missing spool file, missing fence, empty body file, valid update (assert
  front-matter block is byte-for-byte identical before/after, assert new
  body content matches the input file exactly), and a case where the new
  body content itself contains a line that looks like `---` (must survive
  verbatim in the body, must not be misinterpreted as a new fence on a
  subsequent read — verify by round-tripping through
  `ParseSpoolFrontMatter`/`extractSpoolBody`'s Go-side logic conceptually,
  i.e. the front-matter block boundary is only ever determined by *position*
  from the original file's fence, never re-scanned in the new body).
- Implement `BodyWriter.SetBody(spoolPath, bodyFilePath string) error` in
  `internal/review/bodywriter.go`, mirroring `DecisionWriter.SetDecision`'s
  shape (resolve via `resolveSpoolPath`, same three-tier errors, invoke via
  an `execCommandFunc`-style seam).
- Replace Task 4's scaffold `case editorDoneMsg:` with full handling:
  1. `m.manager.ResumeOutputCapture()` (unchanged from scaffold).
  2. `defer`-equivalent: always `os.Remove(msg.tempPath)` before returning,
     on every branch (success, editor error, validation error).
  3. If `msg.err != nil` (editor exited non-zero or failed to launch):
     append error activity line, remove temp file, return — `BodyWriter.SetBody`
     is never called.
  4. Read the temp file back (`os.ReadFile`); if empty (after
     `strings.TrimSpace`), append the "editor produced an empty file" error
     activity line, remove temp file, return — `BodyWriter.SetBody` is never
     called (this is the "restore on invalid content" behavior: restoring
     means *not writing*, since the spool file was never touched).
  5. Otherwise, call `m.bodyWriter.SetBody(msg.rec.SpoolPath, msg.tempPath)`
     **before** removing the temp file (the script needs to read it); on
     error, append error activity line; on success, append success activity
     line.
  6. Remove the temp file as the final step regardless of step 5's outcome.

**Acceptance Criteria**:
- `set-review-body_test.sh` passes all cases, wired into the test runner
  the same way as `set-review-decision_test.sh` / `set-review-notes_test.sh`.
- `BodyWriter.SetBody` unit tests (`bodywriter_test.go`) mirror
  `decisionwriter_test.go`'s coverage shape.
- End-to-end test (`body_edit_test.go`, extending Task 4's tests): faking
  the editor as a script that modifies the temp file's content, confirm
  the real spool file's body is updated and its front-matter block
  (including any `decision_notes:` value already set) is byte-for-byte
  unchanged.
- End-to-end test: faking the editor as a script that empties the temp
  file, confirm the spool file is completely unchanged (front-matter AND
  body) and an activity-line error is shown.
- End-to-end test: faking the editor as a script that exits non-zero,
  confirm the spool file is completely unchanged.
- Temp file is removed from disk in every branch (success, empty-file
  error, non-zero-exit error) — assert via `os.Stat` returning
  `os.IsNotExist` after the flow completes.

**Dependencies**: Task 4.

---

### Task 6: Footer/status visual feedback for both editing modes

**Scope**: `internal/tui/commands.go`, `internal/tui/tui.go`,
`internal/tui/footer_test.go`.

**Work**:
- Notes-edit mode: on entering (Task 2/3's `startNotesEditMsg` handler),
  call `m.footerManager.SetTransientMessage(...)` with a message identifying
  the PR and the save/cancel keys, e.g.
  `fmt.Sprintf("Editing notes for %s #%d (Enter to save, Esc to cancel)", rec.Repo, rec.PR)`.
  On Enter (save) or Esc (cancel), clear it via
  `m.footerManager.SetTransientMessage("")`, restoring the normal status
  row.
- Body-edit mode: since the TUI is fully suspended during `$EDITOR`, there
  is no footer to update *during* the edit — visual feedback is entirely
  activity-line-based (already specified in Tasks 4/5: "Opening $EDITOR...",
  then success/error on return). Confirm no footer transient-message call
  is needed here (the suspend already communicates "something external is
  happening" via the terminal itself being handed over, matching how
  planning-mode's `handlePlanSubprocess` also relies on activity lines, not
  footer state, around its `tea.ExecProcess` call).
- Review all activity-line strings introduced across Tasks 2–5 for
  consistency with existing phrasing conventions (`applyDecision`'s
  `"Set decision on PR #%d to '%s'"` style: PR-number-first, single-quoted
  values where relevant, `Styles.Success`/`Styles.Error` wrapping).

**Acceptance Criteria**:
- `footer_test.go` has a case asserting `SetTransientMessage` is called
  with the expected string shape when notes-edit mode is entered, and
  called with `""` when notes-edit mode is exited (either via save or
  cancel).
- Manual/integration test confirms the footer status row visibly changes
  during notes-edit mode and reverts afterward (can be asserted via
  `FooterManager.RenderFooter`'s returned `StatusRow` in a test, without a
  real terminal).
- All activity-line strings added in Tasks 2–5 are reviewed for consistent
  phrasing (this can be a lightweight pass — no new test required beyond
  what Tasks 2–5 already specify, but flag any inconsistency found).

**Dependencies**: Task 3, Task 5.

---

### Task 7: Tests for all of the above (integration pass)

**Scope**: All test files listed under "Existing test files needing
updates," plus a final consistency pass across everything added in Tasks
1–6.

**Work**:
- Run the full existing test suite (`go test ./...`) and confirm zero
  regressions from the new `model` fields, message types, and key-switch
  additions.
- Add/complete `decide_routing_test.go` cases proving `n`/`e` are gated
  identically to `p`/`r`/`R`/`d` by footer focus (typed into footer when
  focused, treated as shortcuts otherwise).
- Add/complete `mode_switching_test.go` cases proving body-edit's
  suspend/resume does not interfere with planning-mode's existing
  suspend/resume (they must be fully independent despite sharing the
  `tea.ExecProcess` mechanism and `SuspendOutputCapture`/`ResumeOutputCapture` calls).
- Confirm `commands_decide_test.go`'s and any other test file's model-
  construction helpers are updated so zero-value `notesWriter`/`bodyWriter`/
  `notesInput` fields don't cause nil-pointer panics in tests that don't
  exercise the new feature but do construct a full `model`.
- Run `.howmux/scripts/set-review-notes_test.sh` and
  `.howmux/scripts/set-review-body_test.sh` directly (bash) and confirm
  they're wired into whatever CI/task target already runs
  `set-review-decision_test.sh` (check `Taskfile.yml` / `.github/workflows/`
  for the existing wiring and extend it identically).
- Final repo-wide review: `grep -rn "decision_notes" --include="*.go" --include="*.sh" .`
  to confirm every touch point (parse, write, test fixtures) is accounted
  for and no stale/half-finished reference was left behind.

**Acceptance Criteria**:
- `go test ./...` passes with zero regressions.
- `go vet ./...` and existing lint tooling (per `Taskfile.yml`'s `lint`
  target) pass on all new/modified files.
- Both new shell test suites pass and are wired into the same CI job(s) as
  `set-review-decision_test.sh`.
  - **Verification**: `grep -n "set-review-decision_test.sh" Taskfile.yml .github/workflows/*.yml` shows the wiring location(s); confirm `set-review-notes_test.sh` and `set-review-body_test.sh` appear alongside it in the same file(s).
- All acceptance criteria from Tasks 1–6 are independently re-verified
  (not just individually passing in isolation, but passing together in the
  full suite — e.g. notes-edit and body-edit tests run in the same package
  without interfering with each other's fakes/seams).
- `grep -rn "decision_notes" --include="*.go" --include="*.sh" .` output is
  reviewed and every match is either a real, intentional touch point
  (parsing, writing, test fixture) or explicitly justified in the PR body.

**Dependencies**: Task 3, Task 5, Task 6.

## Validation Commands

```bash
# Build and vet
go build ./...
go vet ./...

# Full test suite
go test ./... -v

# Race detector on the TUI package specifically (event-loop-goroutine-only
# state, but run anyway to catch any accidental new goroutine)
go test ./internal/tui/... -race

# New shell script test suites, run directly
bash .howmux/scripts/set-review-notes_test.sh
bash .howmux/scripts/set-review-body_test.sh

# Confirm existing decision-script test suite still passes unmodified
bash .howmux/scripts/set-review-decision_test.sh

# Confirm no accidental new REPL command was registered
go test ./internal/tui/... -run TestCommandRegistry -v

# Confirm decision_notes touch points are all accounted for
grep -rn "decision_notes" --include="*.go" --include="*.sh" .

# Lint (per Taskfile.yml)
task lint
```
