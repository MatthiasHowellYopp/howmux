---
inclusion: n/a
---

# Design Spec: PR-review gate — set decision via command and key menu

Closes #85

## Problem Statement

The Reviews tab (built by #82/#83) can display a review's spool state
(`pending`, `decided: <value>`, `posted`, `discarded`, `done`, `no spool`)
and lets the user move a row cursor over the tracked PRs, but there is no
way to actually *set* the `decision:` field. `finalize-reviews.sh` (in
`ai-resources/scripts/`, external to this repo) only drains files that
already carry a decision — it has no facility for setting one. The human
gate the whole spool design depends on is currently a dead end inside
howmux: the only way to set `decision:` today is to hand-edit the
front-matter of a file under `~/PR-Review/pending/*.md` with a text editor
outside the TUI entirely.

## Solution Approach

Three layers, each independently testable:

1. **A new shell script primitive**, `.howmux/scripts/set-review-decision.sh`,
   that does exactly one thing: given a spool file path and one of
   `post|revise|rereview|discard`, rewrite that file's `decision:`
   front-matter line in place (sed on the flat `key: value` block,
   consistent with the format `spool.py` / `ai-resources/workflows/spool.py`
   owns) and exit non-zero with a message on stderr if the file doesn't
   exist, has no front-matter fence, or has no `decision:` key to rewrite.
   This keeps Go out of the business of parsing/rewriting markdown
   front-matter, per the issue's explicit constraint, and gives both the
   REPL command and the key-menu shortcuts a single call-site to invoke.

2. **A thin Go wrapper**, `internal/review/decisionwriter.go` (new file,
   name chosen to avoid colliding with the unrelated `decision.go` that
   already exists — that file implements `decideReviewAction`, the
   watch-poll routing decision, a completely different "decision" from the
   spool's human-set `decision:` field), that shells out to
   `set-review-decision.sh` via `os/exec`, following the same
   `execCommandFunc`-style seam already used elsewhere in this package
   for testability (see `internal/review/runner.go` / `checkout.go` for the
   existing pattern of a package-level `var execCommandFunc = exec.Command`
   that tests override). This is the "spool primitive call" the issue asks
   for — Go never touches the file's bytes directly, it only invokes the
   script and inspects the exit code / stderr.

3. **TUI wiring**: a `decide <post|revise|rereview|discard>` REPL command
   registered in `command_registry.go` and dispatched in `tui.go`'s
   `executeCommand`, plus single-key shortcuts (`p`/`r`/`R`/`d`) handled
   inside `ReviewsTab.Update` (mirroring how `up`/`down`/`enter` are already
   handled there) so they're only live when the Reviews tab is the active
   tab and the footer input doesn't have focus. Both paths resolve "the
   currently selected review" via `ReviewsTab.SelectedKey()` (already
   exposed by #83) and `Record.SpoolPath` (already tracked per #82), then
   call the same `DecisionWriter` used by both entry points so there is
   exactly one code path that shells out.

### Why a new script rather than reusing `finalize-reviews.sh`

`finalize-reviews.sh` invokes `pr_review_finalize.py`, which is a *batch
drain* — it scans the whole `pending/` directory and, for `post`,
`revise`, and `rereview`, launches a `kiro-cli` one-shot agent call per
file (posting inline comments, re-running the consolidator, etc.). That is
categorically different from "set the decision field on the file the user
has selected in the TUI right now" — invoking it from a keypress would
either (a) drain every other already-decided file in the spool as a side
effect of setting one row's decision, or (b) require the kiro-cli
subprocess machinery to run synchronously from a TUI keystroke, which is
far too slow and heavyweight for what AC5 asks for ("update immediately").
`set-review-decision.sh` is deliberately the missing narrow primitive:
pure `sed`/`awk` on one file's front-matter, no subprocess spawning, no
network calls, sub-100ms — cheap enough to call from a keypress and safe
enough that a human calling `decide post` doesn't accidentally trigger
posting; posting only happens later when the user runs
`finalize-reviews.sh` (or howmux's future equivalent) to drain the queue.
This keeps the human gate intact: `decide` only ever writes intent into the
spool file; `finalize-reviews.sh` is still the only thing that acts on it.

### Concurrency Analysis

This change does **not** cross a goroutine boundary. `ReviewsTab.Update`
runs on Bubble Tea's single event-loop goroutine (the same goroutine that
handles all other key messages, per `tui.go`'s `Update`), and
`DecisionWriter.SetDecision` is a synchronous, blocking `os/exec.Command`
call made from that same goroutine — no `go func()`, no channel, no
shared mutable state read by a background loop. The Reviews tab's existing
design is explicitly "no background state, re-read the store fresh every
View()" (see `reviews_tab.go`'s doc comments), and this change does not
alter that: `decide` is a synchronous action taken in response to a single
keypress or Enter, and the very next `View()` call re-reads the spool file
from disk (already the existing behavior via `resolveSpoolInfo` /
`ReadSpoolInfo`), which is how the "decided: `<value>`" feedback shows up
— no new field is cached on the model or the tab that a background
goroutine could race against.

If a future issue adds a background poller that also touches spool files
(there is no such poller today — the existing `review.Watcher` only
touches `Record`/JSON state under `~/.howmux/reviews/`, never spool files
under `~/PR-Review/`), that poller and this synchronous write path would
then need a lock; out of scope here because no such poller exists.

## Relevant Files

| File | Change |
|------|--------|
| `.howmux/scripts/set-review-decision.sh` | **New.** Shell primitive: `set-review-decision.sh <spool-file> <post\|revise\|rereview\|discard>`. Rewrites the `decision:` line in place. |
| `.howmux/scripts/set-review-decision_test.sh` (or a Go-driven test, see Task 1) | **New.** Script-level tests (see Test Plan). |
| `internal/review/decisionwriter.go` | **New.** `DecisionWriter` type + `SetDecision(spoolPath, decision string) error`; closed-vocabulary validation; `execCommandFunc` seam for tests. |
| `internal/review/decisionwriter_test.go` | **New.** Unit tests for validation, script success, script failure (mocked `execCommandFunc`), missing script binary. |
| `internal/tui/command_registry.go` | **Modify.** Register `decide` command with subcommands `post`, `revise`, `rereview`, `discard`. |
| `internal/tui/command_registry_test.go` | **Modify.** Extend `TestCommandRegistry` (or add a new test) asserting `decide` is registered with exactly those four subcommands and that `IsValidCommand("decide post")` etc. hold while `IsValidCommand("decide bogus")` is false. |
| `internal/tui/tui.go` | **Modify.** Add `case "decide":` to `executeCommand`'s switch, calling a new `handleDecide` (in `commands.go`, matching where `handleReview`/`handleStop`/etc. already live). |
| `internal/tui/commands.go` | **Modify.** Add `func (m model) handleDecide(args []string) (model, tea.Cmd)` — validates args, resolves the selected review from the active `ReviewsTab`, calls `DecisionWriter.SetDecision`, appends success/error activity lines. |
| `internal/tui/commands_test.go` or `commands_review_test.go` | **Modify.** Unit tests for `handleDecide`: valid decision + selection → success line; invalid vocabulary → error line, no script call; no tab selection → error line, no script call; script failure → error line surfaced from stderr. |
| `internal/tui/reviews_tab.go` | **Modify.** Add key handling for `p`/`r`/`R`/`d` in `Update`, each invoking the same decision-setting path via a new `tea.Cmd`/`tea.Msg` round trip (mirroring the existing `openReviewContentMsg` pattern, since `ReviewsTab` has no reference to the `DecisionWriter` or the model, by design — see `openSelectedReviewCmd`'s doc comment on why cross-tab actions go through a message). |
| `internal/tui/reviews_tab_test.go` | **Modify.** Add tests for the four key handlers: with selection → emits the decide-request message with the right decision string; without selection (empty store) → nil cmd, no message. |
| `internal/tui/tui.go` | **Modify.** Add a `case decideRequestMsg:` arm in `model.Update` (alongside the existing `case openReviewContentMsg:` arm) that resolves the message into a call to the same `handleDecide` codepath used by the REPL command, so key-driven and command-driven decisions share one implementation and one set of activity-line / error messages. |
| `internal/tui/commands_test.go` | **Modify.** Test that the `decideRequestMsg` handler in `model.Update` produces the same activity-line output as calling `handleDecide` directly with equivalent arguments (covers "visual feedback is identical regardless of entry point"). |

No changes are needed to `internal/review/spool.go`, `types.go`, or
`store.go` — reading updated spool state already works via the existing
`ReadSpoolInfo`/`ClassifySpoolState` machinery from #82; this issue only
adds the write side.

## Team Orchestration

Tasks are grouped so backend (script + Go wrapper) and TUI wiring
(command registry, dispatch, key handling) can proceed in parallel, with
a final integration task that depends on both.

```
Task 1 (script)  ──┐
                    ├─→ Task 3 (Go wrapper: DecisionWriter) ──┐
Task 2 (n/a)        ┘                                          │
                                                                 ├─→ Task 6 (handleDecide + REPL dispatch) ──┐
Task 4 (command_registry: decide command)  ──────────────────┘                                              ├─→ Task 8 (integration tests, docs)
Task 5 (reviews_tab.go: key handlers + decideRequestMsg)  ─────────────────────────────────────────────────────┘
Task 7 (tui.go: decideRequestMsg handler)  — depends on Task 6 and Task 5's message type
```

Concretely:
- **Task 1** (script) has no Go dependencies and can start immediately.
- **Task 3** (Go wrapper) depends on Task 1 existing (it shells out to it)
  but can be written and unit-tested against a *fake* `execCommandFunc`
  before Task 1 is finished, then integration-tested against the real
  script once both land — so in practice these two can be built in
  parallel by the same or different builder passes and reconciled at the
  end.
- **Task 4** (command_registry) has no dependency on Tasks 1–3 — it's pure
  metadata registration and can be built and tested fully in isolation.
- **Task 5** (reviews_tab.go key handlers) also has no dependency on Tasks
  1–3 — it only needs to define and emit a new message type
  (`decideRequestMsg`); it does not call `DecisionWriter` itself.
- **Task 6** (`handleDecide` + REPL dispatch in `tui.go`/`commands.go`)
  depends on Task 3 (needs `DecisionWriter`) and Task 4 (needs the `decide`
  command registered so `executeCommand` routing has something to switch
  on, though the switch case itself is trivial to add regardless).
- **Task 7** (`decideRequestMsg` handler in `tui.go`) depends on Task 5
  (message type must exist) and Task 6 (needs `handleDecide` to delegate
  to).
- **Task 8** (end-to-end integration test + validation) depends on
  everything above.

## Step-by-Step Task Breakdown

### Task 1: Add `.howmux/scripts/set-review-decision.sh`

**Acceptance Criteria:**

- New executable script `.howmux/scripts/set-review-decision.sh` (mode
  `755`, matching `worktree-create.sh` / `planning-worktree-create.sh` in
  the same directory).
- Usage: `set-review-decision.sh <spool-file-path> <decision>`.
- Validates argument count: exactly 2 positional args, else print usage to
  stderr and exit `1`.
- Validates `<decision>` is exactly one of `post`, `revise`, `rereview`,
  `discard` (case-sensitive, matching the spool contract's lower-case
  values documented in `spool.py`); anything else prints
  `error: invalid decision '<value>' — must be one of: post, revise, rereview, discard`
  to stderr and exits `2`.
- Validates `<spool-file-path>` exists and is a regular file; if not,
  prints `error: spool file not found: <path>` to stderr and exits `3`.
- Validates the file has a front-matter fence (first line is exactly
  `---`, and there's a closing `---` before EOF) and a `decision` key
  somewhere inside that block; if either is missing, prints
  `error: no 'decision:' field found in front-matter: <path>` to stderr
  and exits `4`. This mirrors the "malformed/legacy file" case
  `ParseSpoolFrontMatter` already treats as degrade-gracefully on the Go
  read side — the write side must not silently corrupt such a file by
  appending a duplicate `decision:` line; it must refuse.
- On success: rewrites the existing `decision:` line's value to the new
  decision (preserving every other line byte-for-byte, including field
  order, `decision_notes:`, and the body after the closing fence) using
  `sed` with a bounded address range (only operate within the front-matter
  block, i.e. between the first `---` and the first following `---`, so a
  `decision:`-like string appearing in the review body text is never
  touched). Exit `0` and print nothing to stdout on success (silence is
  the contract the Go wrapper relies on — stderr is reserved for errors).
- Idempotent: running it twice with the same decision produces the same
  file content (verify via `diff` in the test — see Test Plan).
- Does not touch `verdict:`, `decision_notes:`, `diff_file:`, `generated:`,
  or the body — this script's blast radius is exactly one line.

**Files:** `.howmux/scripts/set-review-decision.sh` (new)

---

### Task 2: (removed — folded into Task 1; no separate task needed)

This placeholder is intentionally absent from the numbering below; task
numbers 3 onward continue as originally planned so cross-references in
the Team Orchestration diagram above stay accurate without renumbering.

---

### Task 3: Add `internal/review/decisionwriter.go` + tests

**Acceptance Criteria:**

- New type `DecisionWriter` with a constructor `NewDecisionWriter() *DecisionWriter`
  (no fields needed today beyond the script path resolution below, but a
  struct — not a bare function — keeps the door open for injecting a
  custom script path in tests without a package-level global).
- Closed vocabulary enforced **in Go**, not just in the shell script — the
  issue's AC3 requires validation, and validating in Go first means a typo
  never even reaches a subprocess call:
  ```go
  var validDecisions = map[string]bool{
      "post": true, "revise": true, "rereview": true, "discard": true,
  }
  ```
- `func (dw *DecisionWriter) SetDecision(spoolPath, decision string) error`:
  1. Validates `decision` against `validDecisions`; returns
     `fmt.Errorf("invalid decision %q: must be one of post, revise, rereview, discard", decision)`
     without invoking any subprocess if invalid.
  2. Validates `spoolPath != ""`; returns
     `fmt.Errorf("no spool path provided")` without invoking any subprocess
     if empty (covers the case where a selected review has never been
     reviewed yet, so `Record.SpoolPath` is `""` — this is a distinct,
     earlier check from the row-selection check done in `handleDecide`,
     since a row can be selected but have no spool file yet).
  3. Resolves the script path via a package-level var
     `var scriptPathFunc = defaultScriptPath` (test seam), where
     `defaultScriptPath()` returns
     `filepath.Join(<repoRoot-or-executable-relative-path>, ".howmux/scripts/set-review-decision.sh")`
     — mirror whatever path-resolution convention
     `internal/review/checkout.go` or `runner.go` already use for locating
     repo-relative script/asset paths (read that file first; do not invent
     a second convention). If no such convention exists yet in this
     package, resolve relative to the current working directory the
     howmux binary is run from (`.howmux/scripts/...`), consistent with
     how `.howmux/config.yaml` is already located relative to CWD per
     `internal/config/config.go`.
  4. Invokes the script via `execCommandFunc("bash", scriptPath, spoolPath, decision)`
     (package-level `var execCommandFunc = exec.Command`, the same seam
     name/shape already used in `internal/review/checkout.go` — reuse that
     exact pattern rather than inventing a new one, for consistency within
     the package).
  5. On non-zero exit, returns an error wrapping the captured stderr:
     `fmt.Errorf("set-review-decision failed: %s", strings.TrimSpace(stderrOutput))`.
  6. On success (exit 0), returns `nil`.
- Unit tests in `internal/review/decisionwriter_test.go`:
  - Invalid decision string → error returned, `execCommandFunc` never
    invoked (assert via a counter/flag in the fake).
  - Empty spool path → error returned, `execCommandFunc` never invoked.
  - Fake `execCommandFunc` simulating exit 0 → `SetDecision` returns `nil`.
  - Fake `execCommandFunc` simulating non-zero exit with stderr text →
    `SetDecision` returns an error whose message contains that stderr
    text.
  - Table-driven test over all four valid decisions confirming each is
    accepted by validation (does not by itself prove the script arg is
    correct — that's covered by an integration test against the real
    script, see Task 8).

**Files:** `internal/review/decisionwriter.go` (new), `internal/review/decisionwriter_test.go` (new)

**Dependencies:** None to start (can be built against a fake exec seam
immediately); should be reconciled against the real script from Task 1
before Task 8's integration test.

---

### Task 4: Register `decide` command in `command_registry.go`

**Acceptance Criteria:**

- Add to `NewCommandRegistry`:
  ```go
  registry.register(&Command{
      Name:        "decide",
      Description: "Set the decision on the selected review (post|revise|rereview|discard)",
      Subcommands: []string{"post", "revise", "rereview", "discard"},
  })
  ```
  placed alongside the other command registrations (after `review`,
  matching the file's existing "register everything, then build flattened
  list" structure — no other change needed to
  `buildFlattenedCommands`/`FilterCommands`/`GetSubcommands`, since those
  already generically handle any `Command` with `Subcommands` populated,
  per the existing `watch start`/`watch stop` and `plan classic` patterns).
- `HasArgs` is left `false` (default) since, unlike `plan [desc]`, `decide`
  takes only one of the four closed subcommand values — free text after
  `decide <value>` is not part of this issue's scope (AC9).
- `IsValidCommand("decide post")`, `IsValidCommand("decide revise")`,
  `IsValidCommand("decide rereview")`, `IsValidCommand("decide discard")`
  all return `true` (this falls out of the existing `IsValidCommand`
  subcommand-matching logic once `decide` is registered with those four
  subcommands — verify, don't reimplement).
- `IsValidCommand("decide")` (no subcommand) returns `false` per the
  existing logic's `len(parts) > 1` branch — a bare `decide` with no
  target value is not itself invalid at the registry level (the registry
  only checks the len==1 case by returning `true` for "just the command
  name", matching `watch`'s behavior) — **but** `handleDecide` (Task 6)
  must independently reject a missing decision argument with a clear
  usage message, since the registry's job is autocomplete/validation
  metadata, not runtime enforcement.
- `IsValidCommand("decide bogus")` returns `false` (falls out of the
  existing subcommand-mismatch branch once the four valid subcommands are
  registered).
- `GetSubcommands("decide")` returns exactly
  `["post", "revise", "rereview", "discard"]` (order matching the
  registration order, for consistent autocomplete display).

**Files:** `internal/tui/command_registry.go` (modify), `internal/tui/command_registry_test.go` (modify)

**Dependencies:** None.

---

### Task 5: Add key handlers (`p`/`r`/`R`/`d`) to `reviews_tab.go`

**Acceptance Criteria:**

- Define a new message type near `openReviewContentMsg` in `tui.go` (or in
  `reviews_tab.go` itself if that's more consistent with where
  `openReviewContentMsg` is actually declared — check first; the doc
  comment on `openSelectedReviewCmd` says the message/case pair is handled
  in `tui.go`, so keep the type declaration co-located with that existing
  message per current convention):
  ```go
  // decideRequestMsg is emitted by ReviewsTab.Update's p/r/R/d key handlers
  // (mirroring openReviewContentMsg's pattern for enter) to ask the
  // top-level model to set the decision on the currently selected review.
  // ReviewsTab has no reference to DecisionWriter or the model (by design,
  // matching every other cross-tab action) — see openSelectedReviewCmd's
  // doc comment for why this message/tea.Cmd round trip is the mechanism.
  type decideRequestMsg struct {
      repo      string
      pr        int
      spoolPath string
      decision  string // one of "post", "revise", "rereview", "discard"
  }
  ```
- In `ReviewsTab.Update`, extend the existing `switch keyMsg.String()` with:
  ```go
  case "p":
      return rt, rt.decideSelectedCmd("post")
  case "r":
      return rt, rt.decideSelectedCmd("revise")
  case "R":
      return rt, rt.decideSelectedCmd("rereview")
  case "d":
      return rt, rt.decideSelectedCmd("discard")
  ```
- New helper `func (rt *ReviewsTab) decideSelectedCmd(decision string) tea.Cmd`,
  structured identically to the existing `openSelectedReviewCmd`: if
  `rt.selectedKey == ""`, return `nil` (no-op — AC7's row-selection
  requirement enforced at the tab level too, not just in `handleDecide`,
  so a keypress with nothing selected does nothing observable rather than
  emitting a message that then has to be rejected downstream); otherwise
  look up the matching record from `rt.store.List()` by `recordKey`, and
  return a `tea.Cmd` emitting `decideRequestMsg{repo: rec.Repo, pr: rec.PR, spoolPath: rec.SpoolPath, decision: decision}`.
  A `store.List()` failure degrades to `nil`, matching
  `openSelectedReviewCmd`'s existing error-handling contract exactly (do
  not diverge — read that function's doc comment again before writing
  this one).
- **Important — key capitalization**: Bubble Tea's `tea.KeyMsg.String()`
  already reports shifted letters as their uppercase form (this is how
  the existing codebase would distinguish, e.g., any future `Shift+X`
  binding) — confirm this by checking how `keyMsg.String()` is used
  elsewhere for a case-sensitive letter match (there is no existing
  precedent for a bare uppercase single-letter binding in this codebase
  today, so this task must add a unit test — see Test Plan — actually
  simulating a `tea.KeyMsg` for `R` and confirming it routes to
  `rereview` and not `revise`, since this is the one part of the design
  most likely to silently misbehave if `tea.KeyMsg.String()`'s shift
  handling doesn't work the way this spec assumes).
- These four key cases must **not** fire when the footer input has focus.
  Per `tui.go`'s existing top-level key routing (see the `default:` arm of
  the main `switch msg.String()` in `model.Update`), unmatched keys are
  only forwarded to `m.tabManager.Update(msg)` — and thus to
  `ReviewsTab.Update` — at all; footer-focused text entry is intercepted
  earlier in the same `Update` method for keys that ARE matched
  (`tab`, `enter`, arrows, etc.) but single printable letters like `p`,
  `r`, `d` are *not* in that top-level switch, so they always fall to
  `default:` and get forwarded to the active tab regardless of footer
  focus. **This means an important additional check is required**: if the
  Reviews tab is active AND the footer has focus (the normal state when a
  user is about to type a command), a bare `p` keypress must go to the
  text input, not trigger `decideSelectedCmd`, or the user could never
  type the letter "p" into the footer while on the Reviews tab. Verify by
  reading `tui.go`'s key-routing block once more end-to-end (lines ~800-970
  as read during investigation) before implementing — if
  `ReviewsTab.Update` is only invoked via `m.tabManager.Update(msg)` in the
  `default:` arm, and that arm is reached even when
  `m.input.Focused() == true`, then this task must add a focus check.
  **Resolve this by having `model.Update`'s `default:` arm skip forwarding
  single-letter keys to the active tab when `m.input.Focused()` is true**
  — concretely, add a guard in `tui.go`'s `default:` case: if
  `activeTab.Type() == TabTypeReviews && m.input.Focused()`, fall through
  to the normal input-update path (append the character) instead of
  calling `m.tabManager.Update(msg)`. This is the one piece of `tui.go`
  wiring Task 5 depends on but which lives in `tui.go` rather than
  `reviews_tab.go` — call it out explicitly as its own acceptance
  criterion so it isn't missed:
  - **AC5a**: When the Reviews tab is active and the footer input has
    focus, pressing `p`, `r`, `R`, or `d` types that character into the
    footer input and does **not** trigger a decision. Verified by a
    `tui` package test (Task 7, since it needs `model`, not just
    `ReviewsTab`) that focuses the input, sends each key, and asserts
    `m.input.Value()` grew by that character and no activity line was
    appended.
  - **AC5b**: When the Reviews tab is active and the footer does **not**
    have focus (e.g., immediately after switching to the tab, matching
    `ReviewsTab.CaptureFocusState`'s `FocusTargetFooter` default — see
    below, this needs care), pressing `p`/`r`/`R`/`d` triggers the
    decision flow.

  **Note on the FocusTargetFooter wrinkle**: `ReviewsTab.CaptureFocusState`
  currently always returns `FocusTargetFooter` ("this tab has no internal
  focusable widget... always uses footer input, matching MainTab"). Given
  AC5a above, if the Reviews tab *always* reports footer focus, then by
  the same logic AC5b's precondition ("footer does NOT have focus") would
  never actually hold for this tab today — the p/r/R/d shortcuts would
  never fire, which defeats AC2 of the issue. **This is a real design
  tension the architect flags explicitly for the builder to resolve**:
  the cleanest resolution consistent with existing conventions is to give
  `ReviewsTab` its own internal notion of "row-selection mode" vs.
  "footer-typing mode" — i.e., treat arrow-key/enter/p/r/R/d handling as
  active whenever the Reviews tab is the active tab, and only suppress it
  when the user has explicitly focused the footer to type a command (which
  on this tab, unlike a text-input-bearing tab, means the user pressed
  some explicit "start typing" action). Since `up`/`down`/`enter` already
  work today on the Reviews tab as *row navigation* (not footer input)
  per the existing `Update` method and its tests
  (`TestReviewsTabUpdateNavigation`), the precedent already set by #83 is:
  **on the Reviews tab, footer focus is the exception, not the default** —
  a user switches to the Reviews tab to browse/select rows, and only
  explicitly re-focuses the footer to type a command (e.g., by pressing
  `Tab`, mirroring the planning-tab focus-toggle convention already in
  `tui.go`, or simply by the footer already being focused from before the
  tab switch). Concretely:
  - Reuse `m.input.Focused()` as the single source of truth (already used
    throughout `tui.go`) rather than trusting
    `ReviewsTab.CaptureFocusState()`'s return value for this decision —
    `CaptureFocusState`/`RestoreFocusState` govern what happens *across a
    tab switch*, not the moment-to-moment routing of an individual
    keypress, and `tui.go`'s own key-routing block already re-checks
    `m.input.Focused()` live in several branches (e.g. the `up/down/pgup/…`
    case) rather than only trusting the captured state — follow that same
    established pattern here instead of trying to change what
    `CaptureFocusState` reports.
  - This means Task 5's `p`/`r`/`R`/`d` cases inside `ReviewsTab.Update`
    itself do **not** need to know about footer focus at all — that check
    belongs entirely in `tui.go`'s routing (AC5a's guard), matching how
    `ReviewsTab.Update`'s existing `up`/`down`/`enter` cases also don't
    check footer focus themselves; `tui.go` already decides whether to
    forward a key to the tab at all before `ReviewsTab.Update` ever sees
    it (see the `up, down, pgup, ...` case's own internal
    `m.input.Focused()` check as the precedent to follow for consistency).
  - So AC5a's guard is the *only* new tui.go-level change Task 5 requires;
    once that guard is in place, arriving at `ReviewsTab.Update` at all
    already implies "safe to treat p/r/R/d as row actions."

**Files:** `internal/tui/reviews_tab.go` (modify), `internal/tui/tui.go` (modify — the `decideRequestMsg` type declaration site + the `default:` routing guard from AC5a; the `case decideRequestMsg:` handler itself is Task 7), `internal/tui/reviews_tab_test.go` (modify)

**Dependencies:** None on Tasks 1–4; Task 7 depends on this task's message type.

---

### Task 6: Add `handleDecide` + wire `decide` into `executeCommand`

**Acceptance Criteria:**

- Add `case "decide":` to `executeCommand`'s switch in `tui.go`:
  ```go
  case "decide":
      args := []string{}
      if len(parts) > 1 {
          args = parts[1:]
      }
      return m.handleDecide(args)
  ```
  placed alongside the other `args := []string{}` -style cases (`theme`,
  `log`, `review`) for consistency with the existing style in that switch.
- New `func (m model) handleDecide(args []string) (model, tea.Cmd)` in
  `commands.go`, alongside `handleReview`/`handleStop`/etc., implementing:
  1. **Arg-count / vocabulary validation** (AC3, AC9): if
     `len(args) != 1`, append
     `m.styles.Error.Render("Usage: decide post|revise|rereview|discard")`
     and return `(m, nil)`. If `args[0]` is not one of the four exact
     values (validate here too, redundantly with `DecisionWriter` — belt
     and suspenders, since this is also where the clearest
     user-facing message belongs), append
     `m.styles.Error.Render(fmt.Sprintf("Invalid decision: %s (must be post, revise, rereview, or discard)", args[0]))`
     and return `(m, nil)`.
  2. **Row-selection requirement** (AC7): resolve the active Reviews tab.
     If the active tab is not a `*ReviewsTab`, OR it is but
     `SelectedKey() == ""`, append
     `m.styles.Error.Render("No review selected — switch to the Reviews tab and select a row first")`
     and return `(m, nil)`. Use `m.tabManager.GetActiveTab()` and a type
     assertion to `*ReviewsTab`, following the same
     `if activeTab != nil && activeTab.Type() == TabTypeReviews` pattern
     already used elsewhere in `tui.go` for tab-type dispatch (e.g. the
     `ctrl+w` case's `TabTypeLog` check) — but note `decide` must work
     regardless of which tab is currently active, per the issue's user
     story ("As a howmux user processing PR reviews... using both REPL
     commands and keyboard shortcuts") — **re-read AC7 carefully**: it
     says commands operate on the *currently selected review row*,
     implying the Reviews tab must be the tab where selection lives, but
     does not say the REPL command only works while that tab is active.
     Resolve this ambiguity conservatively: since `ReviewsTab` is a
     singleton (`IsClosable() == false`, "There is exactly one reviews
     view per session"), **locate it by scanning all tabs for
     `Type() == TabTypeReviews`** rather than requiring it to be the
     *active* tab — this lets `decide post` work from the footer while the
     user is looking at an agent tab, matching how `review <PR_URL>` and
     `status` already work regardless of active tab. Add a small helper
     `func (m model) findReviewsTab() *ReviewsTab` if `TabManager` doesn't
     already expose a lookup-by-type method (check `tab_manager.go` first
     — reuse an existing accessor if one already does this).
  3. **Resolve the selected record**: given the found `*ReviewsTab`'s
     `SelectedKey()`, look up the matching `review.Record` (via the tab's
     store — either expose the resolved `Repo`/`PR`/`SpoolPath` from
     `ReviewsTab` directly with a small new accessor like
     `SelectedRecord() (review.Record, bool)`, or duplicate the
     `store.List()` + `recordKey` matching `handleDecide` needs; prefer
     adding the accessor to `ReviewsTab` since `decideSelectedCmd` in Task
     5 needs the identical lookup — factor it into one shared helper method
     on `*ReviewsTab` that both Task 5's key handler and this task's
     `handleDecide` call, rather than duplicating the loop twice).
     If the record can't be found (store list error, or the selected key
     no longer matches any record — a race between selection and an
     external prune), append
     `m.styles.Error.Render("Selected review is no longer available")`
     and return `(m, nil)`.
  4. **Empty spool path check**: if the resolved record's `SpoolPath == ""`
     (never reviewed yet — no spool file exists), append
     `m.styles.Error.Render(fmt.Sprintf("PR #%d has no review yet — nothing to decide", rec.PR))`
     and return `(m, nil)`, without calling `DecisionWriter` (this is the
     same check `DecisionWriter.SetDecision` also performs defensively,
     but checking here first gives a much more specific, PR-numbered
     message than the writer's generic "no spool path provided").
  5. **Invoke the writer**: call
     `m.decisionWriter.SetDecision(rec.SpoolPath, args[0])` (add a
     `decisionWriter *review.DecisionWriter` field to `model`, initialized
     once in the model constructor alongside the other long-lived
     collaborators like `m.manager`/`m.reviewWatcher` — find that
     constructor, likely `NewModel` or similar in `tui.go`, and initialize
     `decisionWriter: review.NewDecisionWriter()` there).
  6. **On error** (AC8): append
     `m.styles.Error.Render(fmt.Sprintf("Failed to set decision: %v", err))`
     and return `(m, nil)` — this is the "user-visible error message" AC8
     requires; it uses the same `appendActivity` + `styles.Error` pattern
     every other command error already uses in this file (verified
     against `handleStop`'s `Error stopping agent: %v` pattern above), so
     it is visually consistent with existing error output, not a new
     pattern.
  7. **On success** (AC5): append
     `m.styles.Success.Render(fmt.Sprintf("Set decision on PR #%d to '%s'", rec.PR, args[0]))`
     and return `(m, nil)`. The Reviews tab itself needs no explicit
     refresh call here — per its documented "no cache, re-read the store
     and spool files fresh on every `View()`" design, the very next render
     (which happens automatically after any `Update`/`executeCommand`
     returns, since Bubble Tea re-renders after every message) will call
     `resolveSpoolInfo` → `ReadSpoolInfo`, which re-reads the just-modified
     spool file from disk and will show `decided: post` (or whichever
     value) immediately. **This is the key existing-architecture fact
     that makes AC5 nearly free**: no explicit "refresh the tab" call is
     needed anywhere in this design, because the tab was already built to
     never cache.

**Files:** `internal/tui/commands.go` (modify), `internal/tui/tui.go` (modify — `executeCommand` case + `model` struct field + constructor init), `internal/tui/commands_test.go` or `commands_review_test.go` (modify)

**Dependencies:** Task 3 (`DecisionWriter`), Task 4 (registry, for
consistency of validation messaging, though `handleDecide` validates
independently at runtime regardless of what the registry allows).

---

### Task 7: Wire `decideRequestMsg` handling in `model.Update`

**Acceptance Criteria:**

- Add a `case decideRequestMsg:` arm in `model.Update` (in `tui.go`),
  placed near the existing `case openReviewContentMsg:` arm, that:
  1. Delegates to the exact same validation/writer/activity-line logic as
     `handleDecide` — do not duplicate the error-message strings or the
     `DecisionWriter` call. Concretely, refactor the "resolve record by
     spoolPath, call writer, append activity line" portion of `handleDecide`
     (steps 5-7 above) into a small shared helper, e.g.
     `func (m model) applyDecision(rec review.Record, decision string) model`,
     that both `handleDecide` (after it resolves `rec` from the tab) and
     the new `case decideRequestMsg:` arm (which already has
     `repo`/`pr`/`spoolPath`/`decision` from the message, no tab lookup
     needed since `decideSelectedCmd` in Task 5 already resolved the
     record before emitting the message) can call. This guarantees
     **identical visual feedback regardless of entry point** — the same
     acceptance criterion phrased two ways in the issue (AC1 REPL, AC2
     key menu) collapse to one implementation.
  2. Since `decideRequestMsg` already carries a `spoolPath`, the empty
     spool-path check (Task 6 step 4) still applies here too — the key
     handlers in Task 5 do not skip that check just because they went
     through a different entry point; `applyDecision` (or a thin wrapper
     around it) must perform it uniformly for both callers.
  3. Returns `(m, nil)` after appending the resulting activity line, same
     as `case openReviewContentMsg:` returns after its work.
- Add the `default:` routing guard from Task 5's AC5a to
  `model.Update`'s key-message handling (this is listed here again as an
  explicit acceptance criterion of Task 7, not just Task 5, since it's the
  one line of `tui.go` wiring that makes the whole feature usable — a
  missing guard here means AC5a silently fails and typing "please" in the
  footer while on the Reviews tab drops every `p` and `R`).

**Files:** `internal/tui/tui.go` (modify), `internal/tui/commands.go` (modify — extract `applyDecision` helper), `internal/tui/commands_test.go` (modify), `internal/tui/reviews_tab_test.go` or a new `internal/tui/decide_test.go` (modify/new — integration-style test simulating a full keypress → message → model.Update round trip)

**Dependencies:** Task 5 (message type), Task 6 (`applyDecision` extraction target).

---

### Task 8: Integration test, README/help text, final validation

**Acceptance Criteria:**

- End-to-end test (can live in `internal/tui/integration_test.go` or a new
  `internal/tui/decide_integration_test.go`) that:
  1. Sets up a temp `~/PR-Review/pending/` (via `PR_REVIEW_DIR` env var if
     `DecisionWriter`'s script-path resolution respects it, OR via a
     temp-dir spool path directly — whichever matches how existing spool
     tests in `spool_test.go` isolate the filesystem; reuse that exact
     mechanism, don't invent a new one) with one real spool file.
  2. Drives a fake `review.StoreInterface` returning one `Record` pointing
     at that file.
  3. Simulates `decide post` via `executeCommand` and asserts (a) the
     returned model's activity lines contain the success message, and (b)
     the actual file on disk now has `decision: post` in its front-matter
     (read it back with `review.ParseSpoolFrontMatter`, not a fragile
     string-contains check).
  4. Repeats for a keypress-driven path: simulate `tea.KeyMsg{...}` for
     `d` while the Reviews tab is active and the footer is unfocused,
     drive it through `model.Update`, and assert the file now has
     `decision: discard`.
  5. Asserts the *rendered* `ReviewsTab.View()` output, called again after
     either path, contains `decided: post` (or `discarded`) in the
     DECISION STATE column — this is the concrete, observable proof of
     AC5 ("Update Reviews tab display immediately after decision is set").
  6. Asserts an invalid decision value (`decide bogus`) leaves the file
     unmodified on disk and produces an error activity line.
  7. Asserts no row selected (empty store) produces an error activity line
     and the script is never invoked (assert via the exec fake's
     invocation counter staying at zero, reusing the same fake from Task
     3's tests).
- Add `decide post|revise|rereview|discard` to the `help` command's output
  list in `handleHelp` (`commands.go`), alongside the existing `review`
  entry, with a one-line description consistent with the existing style
  (`  decide <value>  - Set decision on selected review (post|revise|rereview|discard)`).
- Update `README.md`'s REPL Commands table (see the existing `| review [PR_URL] | ... |`
  row) with a new row:
  `| decide <post\|revise\|rereview\|discard> | Set the decision on the currently selected review |`
- Update `README.md`'s Keyboard Shortcuts section (the "Navigation" list
  currently documents `F2`, `[`/`]`, `Ctrl+W`, arrows, `Tab`/`Shift+Tab`)
  with a new subsection or bullet group documenting `p`/`r`/`R`/`d` as
  Reviews-tab-only row actions, matching the existing format (e.g. a new
  "Reviews Tab (row selected)" grouping alongside "Navigation" /
  "Clipboard" / "Application").
- Run the full validation command list below and confirm all pass.

**Files:** `internal/tui/decide_integration_test.go` (new, or extend
`integration_test.go`), `internal/tui/commands.go` (modify —
`handleHelp`), `README.md` (modify)

**Dependencies:** All of Tasks 1–7.

## Input Validation Summary (AC3, AC9)

Validation happens at **three independent layers**, deliberately
redundant so a bug in any one layer doesn't silently let an invalid value
through:

1. **`command_registry.go`**: `IsValidCommand` rejects `decide <anything not in the four subcommands>` for autocomplete purposes only — this layer never blocks execution by itself (the registry is metadata, not an enforcement gate; `executeCommand` doesn't consult `IsValidCommand` before dispatching).
2. **`handleDecide`** (Go, in the TUI layer): rejects wrong arg count and non-vocabulary values before ever touching `DecisionWriter`, with the clearest user-facing message.
3. **`DecisionWriter.SetDecision`** (Go, in the `review` package): re-validates independently, since this type may be called from contexts other than `handleDecide` in the future (e.g. a future headless CLI subcommand) and must not rely on its caller having already validated.
4. **`set-review-decision.sh`** (shell, the outermost defense): re-validates a third time, since this is the actual mutation boundary and must never trust any caller, including a hypothetical future caller that bypasses the Go layers entirely (e.g. someone invoking the script directly from a terminal).

No free-form text entry exists anywhere in this design (AC9) — `decide`'s
only argument is one of four fixed subcommand strings, matched exactly
(case-sensitive) at every layer.

## Row-Selection Requirement Summary (AC7)

Both entry points enforce "a review row is selected" independently,
because they resolve selection differently:

- **REPL command** (`decide post`): resolves the *singleton* Reviews tab
  by type (not by "is it currently active"), then checks
  `SelectedKey() != ""`. Works from any active tab.
- **Key shortcuts** (`p`/`r`/`R`/`d`): only reachable at all when the
  Reviews tab is the active tab (Bubble Tea only forwards key messages to
  the active tab's `Update`), so "the Reviews tab happens to be active" is
  already guaranteed by the time `ReviewsTab.Update` runs; the additional
  check inside `decideSelectedCmd` is purely "is a row within that active
  tab currently selected" (`selectedKey != ""`).

Both paths degrade to a clear, visible error message (never a silent
no-op) per AC7's "clear message if no review is selected" — see Task 6
step 2 and Task 5's `decideSelectedCmd` no-op contract respectively. Note
the one asymmetry: the REPL path always surfaces an error (there is
somewhere to write an activity line, since `handleDecide` runs in the
normal command-execution flow), while the key-shortcut path with **zero
tracked PRs at all** (not just zero selection) is a true silent no-op
(returns `nil` cmd, matching `openSelectedReviewCmd`'s existing contract
for the identical empty-store case) — this asymmetry already exists for
`enter` today (opening review content also silently no-ops on an empty
store) and this design deliberately keeps the two actions consistent with
each other rather than inventing a new error-surfacing path solely for
`decide` that `enter` doesn't have. If this asymmetry is unacceptable to
the user, that is a follow-up issue against the *existing* `enter`
behavior too, not something to special-case here.

## Error Handling (AC8)

All script/writer failures surface through the identical
`m.styles.Error.Render(...)` + `appendActivity` pattern already used by
every other command in `commands.go` (`handleStop`'s
`"Error stopping agent: %v"`, `handleReview`'s several `"Failed to ...: %v"`
lines, etc.) — this issue introduces no new error-surfacing mechanism,
just new call sites of the existing one. Errors always include the
underlying cause (`%v`-wrapped), never a bare "something went wrong."

## Test Plan

### Script-level (Task 1)
- Missing args → exit 1, usage on stderr.
- Invalid decision value → exit 2, specific error on stderr, file
  untouched.
- Nonexistent spool file → exit 3, specific error on stderr.
- File with no front-matter fence → exit 4, specific error on stderr,
  file untouched.
- File with front-matter but no `decision:` key → exit 4, file untouched.
- Valid file + valid decision → exit 0, `decision:` line updated, every
  other line byte-identical (diff the rest of the file against the
  original).
- Idempotency: running twice with the same decision produces identical
  file content on both runs.
- A `decision:` -looking string inside the review body (after the closing
  fence) is never modified — regression test against the "bounded sed
  address range" requirement in Task 1's acceptance criteria.

### `DecisionWriter` (Task 3, Go)
- Invalid decision string (not one of the four) → error, exec never
  invoked.
- Empty spool path → error, exec never invoked.
- Fake exec returning exit 0 → `nil` error.
- Fake exec returning non-zero + stderr → error containing that stderr
  text.
- Table-driven: all four valid decision strings pass Go-side validation.

### `command_registry.go` (Task 4)
- `decide` registered with exactly the four expected subcommands, in
  order.
- `IsValidCommand` true for all four `decide <value>` forms, false for
  `decide bogus`, true for bare `decide`.
- `GetSubcommands("decide")` returns the four values.
- `GetFlattenedMatches("dec")` includes all four `decide <value>` compound
  strings (verifies the existing `buildFlattenedCommands` generic logic
  picks up the new command without any special-casing needed).

### `reviews_tab.go` key handling (Task 5)
- `p`/`r`/`R`/`d` with a row selected → each emits `decideRequestMsg` with
  the correct `decision` string and the correct `repo`/`pr`/`spoolPath`
  from the selected record.
- Same four keys with an empty store (no rows) → `nil` cmd, no message
  emitted (matches `TestReviewsTabEnterWithNoSelectionReturnsNilCmd`'s
  existing pattern for `enter` — write the analogous test the same way).
- Same four keys with a `store.List()` error → `nil` cmd (matches
  `TestReviewsTabEnterWithStoreListErrorReturnsNilCmd`'s existing pattern).
- Explicit case-sensitivity test: simulate `tea.KeyMsg` for lowercase `r`
  and uppercase `R` separately and assert they route to `revise` and
  `rereview` respectively — do not assume, verify (see Task 5's note on
  why this specific test matters).
- Selection-follows-identity is unaffected: selecting a row, then
  re-sorting the underlying store (as `TestReviewsTabSelectionFollowsPRAcrossResort`
  already tests for navigation), then pressing `p` still targets the
  originally-selected PR, not a row index that shifted.

### `handleDecide` / `commands.go` (Task 6)
- Wrong arg count (`decide` alone, or `decide post extra`) → usage error
  line, `DecisionWriter.SetDecision` never called.
- Invalid vocabulary (`decide bogus`) → specific error line,
  `SetDecision` never called.
- No Reviews tab present at all (shouldn't happen given it's a permanent
  tab, but test defensively) → error line, no panic.
- Reviews tab present but no row selected → error line, `SetDecision`
  never called.
- Row selected but `Record.SpoolPath == ""` → specific "no review yet"
  error line, `SetDecision` never called.
- Row selected with a valid spool path, writer returns success → success
  activity line containing the PR number and decision value,
  `SetDecision` called exactly once with the right arguments.
- Row selected, writer returns an error → error activity line containing
  the wrapped error text.
- Command works when a *different* tab (e.g. an agent tab) is active,
  confirming the "locate by type, not by active tab" resolution from Task
  6 step 2.

### `model.Update` / `decideRequestMsg` (Task 7)
- Sending a `decideRequestMsg` through `model.Update` produces the same
  activity-line text as calling `handleDecide` with the equivalent
  resolved record — exact string comparison, proving the two entry points
  share one code path (`applyDecision`) rather than two copies that could
  drift.
- Footer-focused + Reviews tab active + `p` keypress → character `"p"`
  appended to `m.input.Value()`, no activity line added, no
  `decideRequestMsg` emitted (AC5a's core regression test).
- Footer-unfocused + Reviews tab active + `p` keypress → activity line
  added (decision applied), `m.input.Value()` unchanged.

### Integration (Task 8)
- Full round trip against a real temp spool file for both entry points
  (REPL and keypress), asserting the file on disk changed correctly AND
  the next `ReviewsTab.View()` render shows the new `decided: <value>`
  state — this is the test that actually proves AC5, not just that the
  file was written.

## Validation Commands

```bash
# Build
go build ./...

# Unit tests, new and existing packages touched by this change
go test ./internal/review/... -run 'DecisionWriter|SpoolFrontMatter|ClassifySpoolState' -v
go test ./internal/tui/... -run 'Decide|ReviewsTab|CommandRegistry' -v

# Full test suite (regression guard — nothing else should break)
go test ./... -race

# Script-level tests (shape depends on Task 1's chosen test harness —
# either a bats-style .bats file or a plain bash test script; match
# whatever convention scripts/template-sync-summary.sh or other existing
# .howmux/scripts/*.sh use for testing, if any precedent exists, otherwise
# a simple bash script with explicit exit-code assertions is acceptable)
bash .howmux/scripts/set-review-decision_test.sh   # or equivalent runner

# Manual smoke test
#   1. howmux review <some-real-PR-URL>   # enroll + review a real PR
#   2. Switch to Reviews tab, select the row
#   3. decide post   (REPL) — confirm "Set decision on PR #N to 'post'"
#      activity line and DECISION STATE column shows "decided: post"
#   4. Press 'd' on a different (or the same) row — confirm DECISION STATE
#      updates to "decided: discard" immediately, no manual refresh needed
#   5. decide bogus  — confirm clear error, no file change
#   6. Switch to an agent tab, run `decide revise` from there — confirm it
#      still targets the Reviews tab's selected row correctly

# Lint / vet
go vet ./...
gofmt -l internal/review internal/tui .howmux/scripts
```
