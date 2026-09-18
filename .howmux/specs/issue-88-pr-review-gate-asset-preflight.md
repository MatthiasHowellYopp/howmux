# Design Spec: PR-Review Gate — Asset Preflight and Error Handling

Closes #88

## Summary

The finalize workflow (issue #87) already resolves `finalize-reviews.sh` on
`$PATH` — but it does so **lazily, inside `runFinalizeCmd`**, at the moment the
subprocess is about to be spawned. If the script is missing, the user
discovers this only after typing `finalize`, watching the preview window
open, and getting a `finalizeErrorMsg`. `pr_review_finalize.py` isn't checked
at all in Go — it's an internal implementation detail of `finalize-reviews.sh`
(the shell script invokes the Python orchestrator itself), so today a missing
`pr_review_finalize.py` fails even later, inside the shell script, with
whatever stderr that script happens to produce.

This issue moves both checks **in front of** the command, mirroring the
existing `review.CheckReviewAssets` preflight pattern from #74/#77:

- `handleReview` already calls `review.CheckReviewAssets(kiroDir)` before
  doing anything else, and refuses to proceed (with a rendered error) if it
  fails.
- This issue adds the equivalent for the finalize gate:
  `review.CheckFinalizeAssets()`, called at the top of `handleFinalize` (and
  from a new **retryable preflight state** surfaced in the Reviews tab), that
  verifies `finalize-reviews.sh` and `pr_review_finalize.py` both resolve on
  `$PATH` — using the same injectable `lookPathFunc` seam, the same
  `type:name`-descriptive error shape, and the same "missing N of M, here's
  how to fix it" message format `CheckReviewAssets` already produces.

Unlike `CheckReviewAssets` (which checks files on disk under `~/.kiro`), every
entry here is a `tool:` entry resolved via `lookPathFunc` — there is no
"agent"/"skill" filesystem check in this gate, since `finalize-reviews.sh` and
`pr_review_finalize.py` are both `$PATH`-resolved executables per
`custom-scripts.md`, not `.kiro`-rooted assets.

### How this mirrors #74/#77 exactly

| #74/#77 (`review.CheckReviewAssets`) | #88 (`review.CheckFinalizeAssets`) |
|---|---|
| `lookPathFunc = exec.LookPath` (package var in `internal/review`) | Reuses the **same** `lookPathFunc` package var — no second seam |
| `RequiredReviewAssets() []string` — canonical list, `type:name` | `RequiredFinalizeAssets() []string` — canonical list, `tool:name` only |
| `CheckReviewAssets(kiroDir string) error` | `CheckFinalizeAssets() error` — no directory argument; every entry is PATH-resolved |
| Error: "Missing N of M required assets", listed, plus a "how to fix" block | Same shape: "Missing N of M required scripts", listed, plus a "how to fix" block (symlink into `~/.local/bin/`, per `custom-scripts.md`) |
| Called from `handleReview` before enrollment/checkout | Called from `handleFinalize` before the dry run starts, **and** from a new Reviews-tab-visible preflight status the user can (re)trigger |
| `assets_test.go`: stub `lookPathFunc`, assert exact error text/paths | `finalize_assets_test.go`: stub the same `lookPathFunc`, assert exact error text |

This issue does **not** touch `CheckReviewAssets`, `RequiredReviewAssets`, or
the `review` command's preflight — those stay exactly as #74/#77 left them.
It adds a **second, sibling** preflight for the finalize gate specifically,
because the two commands depend on disjoint asset sets (`review` needs
`.kiro`-rooted agents/skills + `pr_review.py`; `finalize` needs
`finalize-reviews.sh` + `pr_review_finalize.py`, neither of which `review`
touches).

## Relevant Files

### New files

| File | Responsibility |
|---|---|
| `internal/review/finalize_assets.go` | `RequiredFinalizeAssets() []string`, `CheckFinalizeAssets() error`. Reuses `lookPathFunc` already declared in `internal/review/assets.go` — same package, no new seam. |
| `internal/review/finalize_assets_test.go` | Unit tests for `CheckFinalizeAssets`/`RequiredFinalizeAssets`, mirroring `assets_test.go`'s structure (stub `lookPathFunc`, assert error text/paths/counts). |
| `internal/tui/finalize_preflight.go` | TUI-layer glue: `finalizePreflightState` enum (`preflightUnknown`/`preflightOK`/`preflightFailed`), `model.finalizePreflightState` + `model.finalizePreflightErr` fields' owning logic, `runFinalizePreflightCmd() tea.Cmd` (wraps `review.CheckFinalizeAssets` as a `tea.Cmd` so it can be re-run without blocking the event loop), `finalizePreflightResultMsg` message type, and the retry command handler. |
| `internal/tui/finalize_preflight_test.go` | Tests for the state machine transitions and the retry command, using a fake/stubbed `checkFinalizeAssetsFunc` (see seam design below) — no real `exec.LookPath` calls. |

### Modified files

| File | Change |
|---|---|
| `internal/tui/finalize.go` | `handleFinalize` (in `commands.go`, see below) will call the new preflight before doing anything else — no change needed inside `finalize.go` itself; `runFinalizeCmd`'s existing lazy `finalizeScriptPathFunc()` call stays as-is (defense in depth: if the environment changes between preflight and dry-run, the lazy check still catches it). Doc comment added noting the new front-door preflight in `handleFinalize`/`ReviewsTab` supersedes this as the *primary* gate; this call remains a safety net. |
| `internal/tui/commands.go` | `handleFinalize` gains a preflight call at the very top (mirroring `handleReview`'s existing `review.CheckReviewAssets` call at lines ~1055-1067), rendering the error via `m.appendActivity` + storing it for Ctrl+Y copy (see CopyableContent design below) and returning without starting the dry run. |
| `internal/tui/tui.go` | Add `finalizePreflightState finalizePreflightState` and `finalizePreflightErr string` fields to `model` (next to the existing `finalize*` fields, same doc-comment block). Add a `case finalizePreflightResultMsg:` arm to `model.Update` that stores the result and (on failure) calls `m.appendActivity` with the copyable error text. Wire the `Reviews` tab's `Update` return values for the new retry key through the same message round-trip other `ReviewsTab` actions already use (`decideRequestMsg`-style: `ReviewsTab` emits a message, `model.Update` handles it). |
| `internal/tui/reviews_tab.go` | Add a `preflightState`/`preflightErr` pair of fields (set via a new `SetFinalizePreflightState(state finalizePreflightState, errText string)` method called from `model.Update`'s `finalizePreflightResultMsg` arm), a status line rendered at the top of `View()` when the state is not `preflightOK`, a `CopyableContent()` addition so the plain-text rebuild includes that same status line, and a new key binding (`"F"` — see rationale below) in `Update()` that emits a `finalizeRetryPreflightMsg` the model handles by re-running the preflight command. |
| `internal/tui/command_registry.go` | No structural change to `Command` (it has no dynamic-enable field, and none of this repo's existing commands have one — see "Action gating" design below for why gating happens at dispatch time instead). `finalize`'s `Description` string is updated to mention the preflight, e.g. `"...(fails fast if finalize-reviews.sh/pr_review_finalize.py aren't on PATH)"`. |
| `internal/tui/footer.go` | No change required to ship the feature (status lives in the Reviews tab per AC7's "Reviews tab **or** status area" wording — Reviews tab is the better fit since that's where finalize-relevant PRs are already listed). Left out of scope; see "Alternatives considered." |

## The Injectable `lookPathFunc` Seam

**No new seam is introduced.** `internal/review/assets.go` already declares:

```go
// lookPathFunc resolves a tool on PATH. Package var so tests can fake PATH
// resolution hermetically.
var lookPathFunc = exec.LookPath
```

`finalize_assets.go` (new file, same `review` package) calls this exact same
variable. This is deliberate, not an oversight — both preflights check tools
on `$PATH`, and having two independent `lookPathFunc`-alikes in the same
package would let them drift (e.g. a test stubbing one but not the other).
Tests that need `CheckFinalizeAssets` to see a "found" or "not found" result
save/restore `review.lookPathFunc` exactly like `assets_test.go` already does:

```go
origLookPath := lookPathFunc
lookPathFunc = func(string) (string, error) { return "/usr/local/bin/stub", nil }
t.Cleanup(func() { lookPathFunc = origLookPath })
```

Because `finalize_assets_test.go` lives in the same package (`review`), it
has direct access to the unexported `lookPathFunc` var — no export needed,
matching `assets_test.go`'s own access pattern.

### `finalize_assets.go` design

```go
package review

import "fmt"

// RequiredFinalizeAssets returns the canonical list of external scripts the
// PR-review finalize gate (issue #87's finalize-reviews.sh workflow) depends
// on. Every entry is a "tool:" entry — both scripts are resolved on $PATH
// (finalize-reviews.sh via ~/.local/bin per custom-scripts.md;
// pr_review_finalize.py the same way, since finalize-reviews.sh shells out to
// it directly) — there is no ~/.kiro-rooted agent/skill entry in this list,
// unlike RequiredReviewAssets.
func RequiredFinalizeAssets() []string {
	return []string{
		"tool:finalize-reviews.sh",
		"tool:pr_review_finalize.py",
	}
}

// CheckFinalizeAssets validates that every script RequiredFinalizeAssets
// lists resolves on $PATH via lookPathFunc (the same package-level seam
// CheckReviewAssets uses). Returns nil if both resolve, or a descriptive
// error listing exactly what is missing and how to fix it — same message
// shape as CheckReviewAssets ("Missing N of M ...", one bullet per missing
// entry, then a "how to fix" block), so a user who has already seen the
// review preflight error recognizes the pattern immediately.
func CheckFinalizeAssets() error {
	required := RequiredFinalizeAssets()
	var missing []string

	for _, asset := range required {
		// Every entry here is "tool:<name>"; strip the prefix for lookPathFunc
		// and keep the prefixed form for the error text (matching
		// CheckReviewAssets's "tool:pr_review.py (not found on PATH)" style).
		name := asset[len("tool:"):]
		if _, err := lookPathFunc(name); err != nil {
			missing = append(missing, fmt.Sprintf("  - %s (not found on PATH)", asset))
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("PR-review finalize gate requires scripts that are not present on PATH.\n")
	sb.WriteString(fmt.Sprintf("Missing %d of %d required scripts:\n\n", len(missing), len(required)))
	sb.WriteString(strings.Join(missing, "\n"))
	sb.WriteString("\n\n")
	sb.WriteString("To fix, ensure ai-resources/scripts is symlinked into ~/.local/bin and on $PATH:\n")
	sb.WriteString("  ls -la ~/.local/bin/finalize-reviews.sh ~/.local/bin/pr_review_finalize.py\n")
	sb.WriteString("  # if missing, re-run ai-resources' scripts setup (see custom-scripts.md)\n")
	return fmt.Errorf("%s", sb.String())
}
```

This keeps `CheckFinalizeAssets()` taking **no arguments**, unlike
`CheckReviewAssets(kiroDir string)` — there is no directory to parameterize
since every entry is `$PATH`-resolved, not filesystem-rooted. Do not add an
unused `kiroDir`-style parameter "for symmetry"; only add parameters a real
caller needs (YAGNI).

## Preflight Result Flow: TUI State Machine

A dedicated `finalizePreflightState` type in `internal/tui/finalize_preflight.go`,
distinct from `finalizeState` (the existing dry-run/live-run state machine in
`finalize.go`) — these are two different concerns (asset availability vs.
subprocess lifecycle) and conflating them into one enum would force invalid
combinations (e.g. "dry-run-running" while assets are known-missing).

```go
package tui

// finalizePreflightState models whether the finalize gate's required
// scripts are currently known to be resolvable on $PATH. Distinct from
// finalizeState (finalize.go), which tracks the dry-run/confirm/live-run
// subprocess lifecycle — this tracks asset availability, checked before that
// lifecycle is ever allowed to start.
type finalizePreflightState int

const (
	// finalizePreflightUnknown is the initial state: no check has run yet
	// this session. Rendered identically to finalizePreflightOK (no status
	// line) so a fresh session doesn't show a spurious warning before the
	// user has ever tried to finalize — matching handleFinalize's existing
	// "check happens on demand" behavior, just adding a cached,
	// re-checkable result for the Reviews tab's status line.
	finalizePreflightUnknown finalizePreflightState = iota
	finalizePreflightOK
	finalizePreflightFailed
)

// checkFinalizeAssetsFunc is the seam runFinalizePreflightCmd calls through.
// Package-level so tests can stub review.CheckFinalizeAssets's result
// without touching the real $PATH — mirrors every other *Func seam in this
// package (finalizeScriptPathFunc, finalizeCommandFunc, userHomeDirFunc).
var checkFinalizeAssetsFunc = review.CheckFinalizeAssets

// finalizePreflightResultMsg reports the outcome of a (re-)run of the
// finalize asset preflight. err is nil on success.
type finalizePreflightResultMsg struct {
	err error
}

// runFinalizePreflightCmd returns a tea.Cmd that runs checkFinalizeAssetsFunc
// and reports the result as finalizePreflightResultMsg. Run as a tea.Cmd
// (not called inline) even though a $PATH lookup is normally fast, because
// handleFinalize and the Reviews tab's retry key both need this to be
// non-blocking on the Update goroutine — consistent with this codebase's
// existing convention that any external-process/filesystem check funnels
// through a tea.Cmd rather than running directly inside Update (see
// runFinalizeCmd, checkForUpdateCmd).
func runFinalizePreflightCmd() tea.Cmd {
	return func() tea.Msg {
		return finalizePreflightResultMsg{err: checkFinalizeAssetsFunc()}
	}
}
```

### Where the preflight runs

1. **On `handleFinalize`** (`commands.go`): before opening the preview window
   or transitioning `finalizeState`, call `review.CheckFinalizeAssets()`
   synchronously (matching `handleReview`'s existing synchronous
   `review.CheckReviewAssets(kiroDir)` call — both are fast local `$PATH`
   lookups, not worth a `tea.Cmd` round trip when blocking the command
   dispatch by a few microseconds is what every other preflight in this
   codebase already does). On failure: render the error via `appendActivity`
   (see CopyableContent flow below), update
   `m.finalizePreflightState`/`m.finalizePreflightErr` so the Reviews tab's
   status line reflects it immediately without waiting for a separate retry,
   and return — never starting `finalizeState = finalizeDryRunRunning`.

2. **On Reviews-tab retry key** (`"F"`, see UI gating below): emits
   `runFinalizePreflightCmd()` as a `tea.Cmd` (async this time, since it's a
   user-initiated background re-check, not gating an in-flight command
   dispatch), and `model.Update`'s `finalizePreflightResultMsg` case stores
   the result the same way.

Both paths converge on the same `model` fields and the same
`ReviewsTab.SetFinalizePreflightState` call, so `handleFinalize`'s synchronous
check and the async retry never disagree about what "current preflight
state" means.

## CopyableContent Flow (Ctrl+Y)

**The gap this closes**: `MainTab.CopyableContent()` returns `""`
unconditionally (by design — the main console has no single plain-text
buffer). Errors rendered via `m.appendActivity` while the Main tab is active
are therefore **not** currently reachable via Ctrl+Y, regardless of this
issue. `handleReview`'s existing `CheckReviewAssets` error has the exact same
gap today — AC4 asks this issue to close it for the finalize preflight
specifically, which means routing the error through a tab that **does**
implement `CopyableContent()` meaningfully: the Reviews tab.

Design:

1. `ReviewsTab` gains two new fields:
   ```go
   preflightState finalizePreflightState
   preflightErr    string // full error text from review.CheckFinalizeAssets; "" when OK/unknown
   ```
2. `SetFinalizePreflightState(state finalizePreflightState, errText string)`
   sets both, called from `model.Update`'s `finalizePreflightResultMsg` arm
   (and directly from `handleFinalize` on the synchronous path) — the exact
   same "model mutates a tab field via an exported setter" pattern
   `LiveReviewContentTab`'s capture-append methods already use for the
   finalize preview window.
3. `View()` prepends a status block (styled `rt.styles.Error`) above the
   table/empty-message when `preflightState == finalizePreflightFailed`:
   ```
   ⚠ Finalize gate unavailable — press F to retry after fixing:
   <preflightErr, verbatim>
   ```
4. `CopyableContent()` prepends the **same** plain-text block (no styling),
   so Ctrl+Y while the Reviews tab is active copies the exact same text the
   user is looking at — following `CopyableContent()`'s existing doc comment
   ("mirroring LogTab.CopyableContent()'s pattern... so the copied text can
   never drift from what is displayed").
5. `handleFinalize`'s synchronous failure path (item 1 under "where the
   preflight runs" above) calls `m.reviewsTab.SetFinalizePreflightState(...)`
   in addition to `m.appendActivity(...)` — so even though the user typed
   `finalize` from the Main tab, switching to the Reviews tab immediately
   shows (and makes copyable) the same error that was printed to the
   activity log. This is the mechanism that satisfies AC4 without changing
   `MainTab.CopyableContent()`'s documented `""` contract, which is
   out of scope for this issue and used by other tabs' tests as a known
   invariant.

`m.reviewsTab` must be reachable from `commands.go`'s `handleFinalize` —
confirm the existing `model` struct already holds a `*ReviewsTab` reference
(it does, for the `TabManager`-registered Reviews tab used by `decide`/`n`/`e`
key routing); if the field is named differently than `reviewsTab`, use
whatever the existing field name is rather than introducing a duplicate
reference.

## UI Gating Design (Reviews Tab)

### Status indicator

`ReviewsTab.View()`'s status block (described above) **is** the status
indicator required by AC7. It renders in three states:

- `finalizePreflightUnknown`: no block shown (steady state before any check).
- `finalizePreflightOK`: no block shown (steady state after a passing check)
  — a persistent "✓ OK" banner would add visual noise for the common case;
  absence of a warning already communicates "fine," matching
  `renderWatcherStatus()`'s existing convention of only calling out the
  *abnormal* state (`"watcher: inactive"`) rather than a green banner for the
  normal one.
- `finalizePreflightFailed`: the `⚠` block described above, always visible at
  the top of the tab until the user retries successfully.

### Action gating

Per the codebase's existing pattern (`command_registry.go`'s `Command` type
has no per-command dynamic-enable flag, and no existing command implements
one), gating happens at **dispatch time**, not by mutating the registry or
hiding autocomplete entries:

- `finalize` (the REPL command): `handleFinalize`'s preflight call already
  blocks progression on failure (see "Where the preflight runs," item 1) —
  this **is** the gate. The command remains visible in autocomplete (so a
  user can still discover and attempt it, seeing the actionable error), but
  produces no `finalizeState` transition and no subprocess spawn.
- Reviews tab's `p`/`r`/`R`/`d` (decision-setting keys): **out of scope** —
  these call `set-review-decision.sh` via `DecisionWriter`
  (`internal/review/decisionwriter.go`), a completely different script with
  its own `scriptPathFunc`/`statFunc` seam pair, not `finalize-reviews.sh` or
  `pr_review_finalize.py`. The issue's AC5 says "finalize-related commands,"
  which is `finalize` alone — do not extend this preflight to gate
  decision-setting, which works independently of whether the finalize gate's
  scripts are present.

This keeps gating minimal and precise: exactly one command (`finalize`) is
gated, exactly one preflight (`CheckFinalizeAssets`) gates it, and the
Reviews tab reflects that preflight's cached result without owning any new
gating logic of its own beyond rendering.

### Retry mechanism

- New Reviews-tab key: **`"F"`** (capital, distinguishing it from `f`, which
  is unbound; chosen over lowercase `f` so it doesn't collide with any future
  filter/find binding, and to visually pair with the existing capital-letter
  binding `"R"` for rereview — both are "less common, deliberate" actions
  next to the frequent lowercase `p`/`r`/`d`).
- `ReviewsTab.Update()` gains a `case "F":` arm returning a `tea.Cmd` that
  emits a new `finalizeRetryPreflightMsg{}` (following the exact
  `decideRequestMsg`/`gateFailedMsg` round-trip pattern already used by every
  other `ReviewsTab` action, since `ReviewsTab` has no direct reference to
  run a `tea.Cmd` against `checkFinalizeAssetsFunc` itself by design).
- `model.Update`'s new `case finalizeRetryPreflightMsg:` arm returns
  `runFinalizePreflightCmd()` — the async path described above — so the
  retry never blocks the Update goroutine even though in practice a `$PATH`
  lookup is sub-millisecond.
- No debounce/cooldown is added: repeated `"F"` presses each just re-run a
  cheap `exec.LookPath` pair; there is no rate-limit concern here unlike the
  finalize subprocess itself (which the existing `finalizeState` machine
  already prevents from double-running via the `finalizeState != finalizeIdle`
  check in `handleFinalize`).

## Concurrency Analysis

`runFinalizePreflightCmd()`'s returned `tea.Cmd` closure runs on its own
goroutine (standard Bubble Tea `tea.Cmd` contract, same as every existing
`tea.Cmd` in this codebase) and calls `checkFinalizeAssetsFunc()` —
`review.CheckFinalizeAssets`, which only reads `lookPathFunc` (a package
var) and does no shared mutable state access itself. `lookPathFunc` is
written only in tests (`t.Cleanup`-restored, single-goroutine test bodies),
never concurrently with a production read, so no lock is needed for that
var — this matches `assets.go`'s existing `lookPathFunc`, which has never
needed one for the same reason.

The result (`finalizePreflightResultMsg`) crosses back to the Update
goroutine through Bubble Tea's message channel — the standard, already-locked
(by the runtime) crossing every `tea.Cmd`/`tea.Msg` pair in this codebase
uses (e.g. `runFinalizeCmd`'s `finalizeDryRunMsg`/`finalizeCompleteMsg`/
`finalizeErrorMsg`). `model.finalizePreflightState`/`model.finalizePreflightErr`
and `ReviewsTab.preflightState`/`preflightErr` are only ever mutated from
inside `model.Update` (the event-loop goroutine) — exactly like every other
`model`/`*Tab` field in this codebase (there is no `sync.Mutex` anywhere in
`internal/tui`, because Bubble Tea's single-threaded Update contract is the
concurrency boundary itself, not a lock). **No new lock is required**: this
change does not introduce cross-goroutine shared-state access beyond the
message-passing Bubble Tea already provides for every tea.Cmd result, and it
does not touch `watcher.Watcher`, `agent.Manager`, or any other
lock-guarded type.

## Task Breakdown

### Task 1: `review.CheckFinalizeAssets` + `RequiredFinalizeAssets`
**Files**: `internal/review/finalize_assets.go` (new),
`internal/review/finalize_assets_test.go` (new)
**Acceptance Criteria**:
- `RequiredFinalizeAssets()` returns exactly `["tool:finalize-reviews.sh",
  "tool:pr_review_finalize.py"]`.
- `CheckFinalizeAssets()` returns `nil` when `lookPathFunc` resolves both.
- `CheckFinalizeAssets()` returns a non-nil error naming each missing script
  (by its `tool:name` form), with a "Missing N of M" count and a
  fix-it block, when one or both are missing — reusing `lookPathFunc` from
  `assets.go` (no new package var).
- Test stubs `lookPathFunc` (save/restore via `t.Cleanup`, matching
  `assets_test.go`'s existing pattern) — no real `exec.LookPath` call in
  tests.
- Test covers: both present (nil), only `finalize-reviews.sh` missing, only
  `pr_review_finalize.py` missing, both missing (error names both, count
  says "2 of 2").
**Dependencies**: None.

### Task 2: TUI preflight state machine + seam
**Files**: `internal/tui/finalize_preflight.go` (new),
`internal/tui/finalize_preflight_test.go` (new)
**Acceptance Criteria**:
- `finalizePreflightState` enum (`finalizePreflightUnknown`/`OK`/`Failed`)
  defined, distinct from the existing `finalizeState`.
- `checkFinalizeAssetsFunc` package var defaults to
  `review.CheckFinalizeAssets`; test stubs it directly (no `lookPathFunc`
  reach-through needed at this layer) to return controlled nil/error results.
- `finalizePreflightResultMsg{err error}` message type defined.
- `runFinalizePreflightCmd() tea.Cmd` returns a `tea.Cmd` that invokes
  `checkFinalizeAssetsFunc()` and wraps the result in
  `finalizePreflightResultMsg`.
- `finalizeRetryPreflightMsg{}` message type defined (emitted by the Reviews
  tab's new `"F"` key, consumed by `model.Update`).
- Test asserts: stubbed success produces `finalizePreflightResultMsg{err:
  nil}`; stubbed failure produces `finalizePreflightResultMsg{err: <the
  stubbed error>}` unchanged (no re-wrapping/message mangling).
**Dependencies**: Task 1 (references `review.CheckFinalizeAssets` as the
default, though the test stubs around it and doesn't require Task 1's
internals to be correct — can be developed in parallel with a temporary
stub default and wired to the real default at integration time).

### Task 3: Wire preflight into `handleFinalize` + `model` fields
**Files**: `internal/tui/commands.go` (modify `handleFinalize`),
`internal/tui/tui.go` (modify `model` struct + `Update`)
**Acceptance Criteria**:
- `model` gains `finalizePreflightState finalizePreflightState` and
  `finalizePreflightErr string` fields, documented in the same comment block
  as the existing `finalize*` fields (state-machine ownership note: only
  mutated from `Update`).
- `handleFinalize` calls `review.CheckFinalizeAssets()` (via the package,
  not the TUI-layer `checkFinalizeAssetsFunc` var — the synchronous path
  calls the real check directly, matching `handleReview`'s existing
  synchronous `review.CheckReviewAssets` call) as its first action, before
  the existing `m.finalizeState != finalizeIdle` guard.
- On failure: renders the error via `m.appendActivity` (styled `Error`,
  two lines: a header line + the error text, matching `handleReview`'s
  existing two-line rendering of `CheckReviewAssets`'s error), sets
  `m.finalizePreflightState = finalizePreflightFailed`,
  `m.finalizePreflightErr = err.Error()`, propagates both to the Reviews tab
  (Task 4's setter), and returns without starting the dry run.
- On success: sets `m.finalizePreflightState = finalizePreflightOK`,
  `m.finalizePreflightErr = ""`, propagates to the Reviews tab, and proceeds
  with the existing dry-run-start logic unchanged.
- `model.Update` gains a `case finalizePreflightResultMsg:` arm (for the
  async retry path, Task 5) that performs the same state-setting +
  Reviews-tab-propagation as `handleFinalize`'s inline failure/success
  branches above, without duplicating the rendering logic — extract a shared
  private helper (e.g. `applyFinalizePreflightResult(err error) model`) that
  both `handleFinalize` and the `finalizePreflightResultMsg` case call, so
  the two paths cannot drift.
- `model.Update` gains a `case finalizeRetryPreflightMsg:` arm returning
  `runFinalizePreflightCmd()`.
**Dependencies**: Task 1, Task 2.

### Task 4: Reviews tab status line + CopyableContent + retry key
**Files**: `internal/tui/reviews_tab.go` (modify),
`internal/tui/reviews_tab_test.go` (modify/extend)
**Acceptance Criteria**:
- `ReviewsTab` gains `preflightState finalizePreflightState` and
  `preflightErr string` fields.
- `SetFinalizePreflightState(state finalizePreflightState, errText string)`
  exported method sets both fields.
- `View()` prepends the `⚠ Finalize gate unavailable — press F to retry
  after fixing:` + `preflightErr` block (styled `rt.styles.Error`) when
  `preflightState == finalizePreflightFailed`; renders nothing extra for
  `Unknown`/`OK`.
- `CopyableContent()` prepends the identical, unstyled block under the same
  condition, so Ctrl+Y while the Reviews tab is active reproduces exactly
  what `View()` showed (verified by a test that sets a failed state and
  asserts `CopyableContent()` contains the exact `preflightErr` string).
- `Update()` gains `case "F":` returning a `tea.Cmd` emitting
  `finalizeRetryPreflightMsg{}`.
- Test: `SetFinalizePreflightState(finalizePreflightFailed, "boom")` then
  `View()` contains "boom" and `CopyableContent()` contains "boom";
  `SetFinalizePreflightState(finalizePreflightOK, "")` then neither contains
  the old error text (tests the block is fully replaced/cleared, not
  appended).
**Dependencies**: Task 2 (needs `finalizePreflightState` type and
`finalizeRetryPreflightMsg` type to exist).

### Task 5: Command registry description update
**Files**: `internal/tui/command_registry.go` (modify)
**Acceptance Criteria**:
- `finalize` command's `Description` string mentions the preflight (e.g.
  append `" — checks finalize-reviews.sh/pr_review_finalize.py are on PATH
  first"`), matching this file's existing style of packing behavior notes
  into `Description`.
- No structural change to the `Command` type.
**Dependencies**: None — can be done in parallel with any other task; purely
textual.

### Task 6: Integration test — end-to-end preflight-blocks-finalize
**Files**: `internal/tui/finalize_integration_test.go` (extend existing file)
**Acceptance Criteria**:
- New test in the style of the file's existing `newFinalizeTestModel()`
  helper: stub `checkFinalizeAssetsFunc` (or, for the synchronous
  `handleFinalize` path, stub `review`-package `lookPathFunc` to force
  `review.CheckFinalizeAssets` to fail) to return an error, drive
  `model.handleFinalize(nil)`, and assert:
  - `m.finalizeState` remains `finalizeIdle` (dry run never started).
  - `finalizeCommandFunc`/`finalizeScriptPathFunc` (the existing
    lower-level seams) are never invoked — add a call-counting stub to
    prove the subprocess path was never reached, not just that the message
    said so.
  - `activityContains(m, "not found on PATH")` (or equivalent substring from
    `CheckFinalizeAssets`'s message) is true.
  - The Reviews tab (constructed in the test model) reflects
    `preflightState == finalizePreflightFailed` after the call.
- A second test drives the success path (`lookPathFunc`/
  `checkFinalizeAssetsFunc` stubbed to succeed) and asserts `finalizeState`
  transitions to `finalizeDryRunRunning` exactly as it does today (i.e. this
  issue introduces no regression to the existing happy path already covered
  by `finalize_test.go`/`finalize_integration_test.go`).
- A third test exercises the retry key end to end: build a `ReviewsTab`,
  send `"F"` via `Update`, run the returned `tea.Cmd`, feed the resulting
  `finalizePreflightResultMsg` back into `model.Update`, and assert the
  Reviews tab's `preflightState` updates accordingly (stub
  `checkFinalizeAssetsFunc` to flip from failing to succeeding between two
  runs, proving the retry actually re-checks rather than caching the first
  result forever).
**Dependencies**: Task 3, Task 4 (needs both wired together to test the
integration).

### Parallelization

- Task 1 and Task 5 have no dependencies and can start immediately, in
  parallel with each other.
- Task 2 depends only on Task 1's public signature (`review.CheckFinalizeAssets
  () error`), not its internal correctness — can start in parallel with Task 1
  using a temporary local stub, then re-point at the real function once
  Task 1 lands.
- Task 3 depends on Tasks 1 and 2 landing (needs the real check + the
  message/state types).
- Task 4 depends on Task 2 (needs the shared types) but not on Task 3 — can
  proceed in parallel with Task 3 once Task 2 is in.
- Task 6 is last: it depends on Tasks 3 and 4 both being in place to test the
  full wiring.

## Test Plan

| Seam | Test file | What's stubbed | What's asserted |
|---|---|---|---|
| `review.lookPathFunc` | `finalize_assets_test.go` | Fake resolver returning found/not-found per script name | `CheckFinalizeAssets()` nil/error shape, message content, counts |
| `checkFinalizeAssetsFunc` | `finalize_preflight_test.go` | Fake func returning canned nil/error | `runFinalizePreflightCmd()`'s returned `tea.Cmd`, when invoked, yields `finalizePreflightResultMsg` carrying that exact error unchanged |
| `handleFinalize`'s synchronous preflight call | `finalize_integration_test.go` | `review.lookPathFunc` (package-level, cross-package override since `commands.go` is in `tui`, calling into `review`) | `finalizeState` stays `finalizeIdle`; `finalizeCommandFunc` never called (call-count stub); activity log contains the error text |
| `ReviewsTab.SetFinalizePreflightState` / `View()` / `CopyableContent()` | `reviews_tab_test.go` | N/A (pure state-setter + string assertions, no external seam) | Status block appears/disappears correctly; `View()` and `CopyableContent()` never disagree on the error text |
| Retry key round trip | `finalize_integration_test.go` | `checkFinalizeAssetsFunc` (flip failing→succeeding across two calls) | `"F"` → `tea.Cmd` → `finalizePreflightResultMsg` → `model.Update` → `ReviewsTab.preflightState` updates on each round, proving it's a live re-check not a cached value |

All tests are `go test ./internal/review/... ./internal/tui/...`-runnable
with **no real subprocess execution and no real `$PATH` dependency** —
every test stubs at the seam boundary (`lookPathFunc` or
`checkFinalizeAssetsFunc`), consistent with #70's convention that preflight
logic must be testable through injectable function vars rather than requiring
the test environment to actually have (or lack) `finalize-reviews.sh`
installed.

## Alternatives Considered

- **Footer status line instead of Reviews tab.** AC7 allows either. The
  Reviews tab was chosen because it is where the user is already looking
  when deciding whether to finalize (the `p`/`r`/`R`/`d` decision keys live
  there), and because `FooterManager.renderBaseInfo()` is shown on every tab
  regardless of relevance — a persistent "finalize gate: failed" footer
  segment would be visible even while, say, editing a decision-notes buffer
  where it has no bearing on the current action. A `Reviews`-tab-scoped
  status keeps the signal next to the actions it gates.
- **Extending `CheckReviewAssets` to also check the two finalize scripts.**
  Rejected: `review`/`CheckReviewAssets` and `finalize`/`CheckFinalizeAssets`
  gate two different commands with two different asset sets and two
  different callers (`handleReview` vs. `handleFinalize`); merging them would
  make `handleReview` also depend on (and fail on) `finalize-reviews.sh`
  being present even though `review` never invokes it, which is a strictly
  worse failure mode than today.
- **Hiding the `finalize` command from autocomplete entirely on preflight
  failure.** Rejected: no existing command supports dynamic
  visibility, `CommandRegistry` has no per-session mutable state today, and
  hiding the command would remove the user's ability to *discover* the
  actionable error message (typing `finalize` and seeing exactly what's
  missing is more helpful than the command silently not autocompleting,
  leaving the user to wonder why).
