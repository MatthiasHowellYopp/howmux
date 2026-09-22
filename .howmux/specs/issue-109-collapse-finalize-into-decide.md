# Design Spec: Collapse finalize into decide — launch action on decision, inline confirm only on post

Closes #109

## Assumptions

- **Worktree path**: provided and used as given
  (`/Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-109-63108`),
  no fallback needed.
- **Open question resolved per the issue's explicit instruction**: `decide post`
  writes `decision: post` to front-matter first, then posts, then archives to
  `done/` — giving crash recovery (a crash mid-post leaves a recoverable
  `decision: post` file in `pending/` that a retry of `decide post` — or, in
  principle, a headless drain — can pick up again).
- **No existing Go implementation of post/revise/rereview/discard action
  logic exists anywhere in this repo.** I verified this by reading
  `internal/review/decisionwriter.go` (writes `decision:` only, via
  `set-review-decision.sh`), `internal/tui/finalize.go` (only ever shells out
  to the *batch* `finalize-reviews.sh`, never a single file), and
  `internal/github/pr.go` (only `gh pr view` — no posting code at all). The
  actual post/revise/rereview logic lives entirely in
  `ai-resources/workflows/pr_review_finalize.py` (`do_post`, `do_revise`,
  `do_rereview`), which is Python, is not part of this repo, and — critically
  — **operates on the whole pending/ spool with no single-file/single-PR
  flag** (`pr_review_finalize.py`'s `main()` always calls
  `spool.scan_pending()` and iterates every decided entry; I checked its
  `argparse` block, lines 165–171, and there is no `--file`/`--pr` option).
  Per the issue's explicit instruction ("the TUI needs its own Go
  implementation of these actions now (equivalent logic, not shelling out to
  the headless script)"), this spec designs a new, single-review-scoped Go
  implementation in `internal/review` that mirrors the Python logic's
  externally-observable contract (same `kiro-cli` one-shot agent-call
  mechanism, same spool front-matter mutations, same `gh api` posting shape)
  without modifying `ai-resources` (out of this repo, out of scope) and
  without shelling out to `finalize-reviews.sh`/`pr_review_finalize.py` at
  all. This is a substantial new subsystem, not a thin dispatch change —
  flagged here up front because it is the single biggest scope driver in this
  spec, and the task breakdown reflects that with dedicated tasks for it.
- **`gh` CLI availability**: the new `post` action shells out to `gh api`
  directly (mirroring `review-poster.md`'s exact payload shape), matching how
  `internal/github/pr.go`'s `GetPR` already shells to `gh pr view` and how
  `internal/review/runner.go`'s `fetchDiffFunc` already shells to `gh pr
  diff`. No new external dependency is introduced.
- **`kiro-cli` availability**: `revise`/`rereview`/`post`'s prose step all
  need one-shot agent calls (`review-consolidator` for revise/rereview,
  optionally `humanizer` for post's summary prose per
  `humanize-prose.md`/`ai-resources/workflows/humanize.py`). This mirrors
  `internal/agent/manager.go`'s existing `kiro-cli` spawn pattern (`KiroCLIPath:
  "kiro-cli"`) and `ai-resources/workflows/kiro_oneshot.py`'s documented
  contract: `kiro-cli chat --no-interactive --trust-all-tools --agent <name>
  "<prompt>"`, answer on stdout (ANSI + leading `> ` stripped), chrome on
  stderr, exit 0. No new external dependency is introduced — `kiro-cli` is
  already a hard requirement of this whole project (see main README
  Prerequisites).
- **Humanizing the post summary is treated as optional polish, not a blocking
  requirement of this issue.** The issue's acceptance criteria say nothing
  about prose quality; `do_post` in Python humanizes the review body before
  posting, but that pass is documented as "cosmetic, never load-bearing" (see
  `ai-resources/workflows/humanize.py`'s docstring) and degrades to
  passthrough on any failure. Task 3 below implements the humanize call as a
  best-effort step with the same degrade-to-original-on-failure contract, so
  it is present (parity with the Python reference) but never blocks a post.

## Problem Recap

Today:
- `decide <action>` (`internal/tui/commands.go:966`, `handleDecide`) validates
  the action and delegates to `applyDecision` (`commands.go:952`), which calls
  `DecisionWriter.SetDecision` (`internal/review/decisionwriter.go:70`) — this
  **only ever writes the `decision:` front-matter field**. Nothing is
  launched.
- The Reviews-tab keys `p`/`r`/`R`/`d` (`internal/tui/reviews_tab.go:474-481`)
  emit `decideRequestMsg` (`tui.go:62`), handled at `tui.go:742` by calling the
  same `applyDecision` — same "write-only" behavior.
- A separate `finalize` REPL command (`commands.go:1014`, `handleFinalize`)
  runs the batch drain: preflight (`review.CheckFinalizeAssets`,
  `internal/review/finalize_assets.go:44`) → dry-run `finalize-reviews.sh`
  (streamed into a preview window, `finalize.go`) → `y/N` confirm
  (`tui.go`'s `finalizeAwaitingConfirmation` gate, relocated/fixed by issue
  #102's spec) → live `finalize-reviews.sh` run, which shells to
  `pr_review_finalize.py` and drains **every** decided file in
  `~/PR-Review/pending/` in one pass.

This issue removes the two-step (decide-then-finalize) model for the TUI
entirely. `decide <action>` becomes the launcher: it performs the action for
the **selected review only**, immediately, with a confirm gate only on `post`
(the one irreversible step). `finalize`, its dry-run preview window, and its
preflight check are deleted from the TUI. The headless
`finalize-reviews.sh`/`pr_review_finalize.py` batch path is untouched — it has
its own callers (documented in `custom-scripts.md`,
`pr-apply-fixes-pipeline.md`) outside the TUI and is explicitly out of scope
per the issue body.

## Solution Approach

### 1. New package-level action logic in `internal/review` (the core of this issue)

Add a new file `internal/review/decideactions.go` containing four
single-review action functions, one per decision, that a caller (the TUI)
invokes synchronously-but-off-the-Update-goroutine (via `tea.Cmd`, matching
every existing subprocess call in this codebase — `runFinalizeCmd`,
`RunReview`, etc.). Each function:

1. Resolves the spool file via the existing `resolveSpoolPath` helper
   (`spool.go`) — reject if not found or already in `done/` (same three-way
   error shape `DecisionWriter.SetDecision` already uses).
2. Reads the current front-matter + body via the existing
   `ParseSpoolFrontMatter`/`extractSpoolBody` helpers (`spool.go`) — no new
   parsing code needed, these already exist and are unit-tested
   (`spool_test.go`).
3. Performs the decision-specific work (detailed per-action in section 3/4
   below).
4. Writes the result back using a **new** shared front-matter+body rewrite
   helper (`RewriteSpoolEntry`, new in `decideactions.go`) that generalizes
   `patchFrontMatterMetadata`'s existing "read fences, replace named
   keys/body, write atomically" pattern (`spool.go:224-337`) to also replace
   the body and clear/set `decision`/`decision_notes` — this is the direct
   Go equivalent of `ai-resources/workflows/spool.py`'s `rewrite()` and
   `move_to_done()` functions, which the Python code uses for the exact same
   purpose. Reuses the same atomic-temp-file-then-rename discipline
   `WriteReviewedMetadata` already established (same directory, same `cp -p`
   permission-preserving approach, same trap/cleanup contract).
5. Returns `(newBody string, newVerdict string, err error)` for
   revise/rereview (caller rewrites in place, clears `decision`), or
   `(postResult PostResult, err error)` for post (caller archives to `done/`
   on success), or just `err` for discard (caller archives to `done/`
   directly, no body rewrite).

This design keeps `internal/review` as the single place that knows the spool
file format and the `kiro-cli`/`gh` invocation contracts — exactly the same
layering `RunReview` (`runner.go`) already established for the *initial*
review (subprocess invocation + spool write live in `internal/review`;
`internal/tui` only orchestrates state transitions and rendering).

### 2. `decide` command flow (validation → dispatch → per-action launch)

`handleDecide` (`commands.go:966`) changes from "validate + write decision"
to "validate + dispatch to launcher," matching the Reviews-tab key handlers'
existing message-round-trip shape so both entry points end up on one
dispatch path (see `applyDecision`'s existing role as the single shared
choke point — this issue keeps that shape, replacing what's chosen there).

**Validation** (unchanged in spirit, tightened in effect): argument count and
vocabulary checks happen exactly where they do today
(`commands.go:967-978`), **before any state change** — this already satisfies
"fail loudly on invalid action, listing valid options, before any state
change" per the issue's AC5, and needs no behavioral change, only a
retargeted success path. The existing error message ("Invalid decision: %s
(must be post, revise, rereview, or discard)") already names the valid set;
keep it verbatim.

**Dispatch**: after the existing row-selection and empty-spool-path checks
(`commands.go:979-999`, unchanged), instead of calling `applyDecision`
directly, `handleDecide` branches on `decision`:

```go
switch decision {
case "post":
    return m.startDecidePost(rec)       // new — opens inline confirm, no
                                          // action yet (see section 3)
case "revise":
    return m.launchDecideRevise(rec)     // new — immediate, no confirm
case "rereview":
    return m.launchDecideRereview(rec)   // new — immediate, no confirm
case "discard":
    return m.launchDecideDiscard(rec)    // new — immediate, no confirm
}
```

Each `launchDecideX` function follows the exact shape `handleFinalize`
already uses for launching a subprocess-backed `tea.Cmd` (`commands.go:1014
onward`): append an "in progress" activity line, return `(model, tea.Cmd)`
where the `tea.Cmd` runs the actual work off the Update goroutine and reports
back via a new terminal message type, handled in `model.Update`.

**Where the inline confirm state machine lives (`post` only)**: model as a
**new, minimal state enum** — `decidePostConfirmState` — rather than reusing
`finalizeState` (which is being deleted entirely, see Inventory below) or
generalizing `confirmingExit` (a bare `bool`, documented in `tui.go` as
mutually exclusive with other confirm gates; reusing it for a *different*
irreversible action with different payload data — repo/PR/finding-count —
would require bolting those fields onto the shared exit-confirm state, which
is out of proportion to a boolean whose only job today is "are we
confirming exit"). This mirrors `finalizeState`'s original reasoning
(`finalize.go`'s own doc comment: "An explicit enum is used rather than a
bare bool ... because this state machine has more than two states") scaled
down to the two states `post`'s confirm actually needs:

```go
// decidePostConfirmState models the (idle -> awaiting-confirmation -> idle)
// lifecycle of decide post's inline y/N gate — this issue's direct, smaller
// replacement for finalizeAwaitingConfirmation (finalize.go, deleted by this
// issue). Unlike finalizeState (dry-run-running / live-running distinction),
// there is no separate "running" state to distinguish from "awaiting
// confirmation": decidePostConfirmAwaiting IS the only non-idle state, since
// posting itself is fire-and-forget from the confirm gate's perspective (the
// actual gh/kiro-cli work happens inside the tea.Cmd the "y" branch returns,
// tracked implicitly by whether that tea.Cmd's terminal message has arrived
// yet — no third state is needed because nothing on screen needs to
// distinguish "posting in flight" from "idle" the way finalize's streamed
// preview window did).
type decidePostConfirmState int

const (
    decidePostConfirmIdle decidePostConfirmState = iota
    decidePostConfirmAwaiting
)
```

New model fields (added to the `model` struct in `tui.go`, replacing the
deleted `finalizeState`/`finalizeCancel`/`finalizeCapture`/`finalizeLastGen`/
`finalizeWindowTabID`/`finalizePreflightState`/`finalizePreflightErr`
fields — see Inventory):

```go
decidePostConfirmState decidePostConfirmState
decidePostPending      review.Record // the record awaiting confirmation
decidePostFindingCount int           // parsed finding count, for the prompt text
decidePostVerdict      string        // for the gh review "event" mapping
```

`startDecidePost` (new, `commands.go`) does NOT launch anything — it:
1. Reads the review body via the existing `ReadSpoolBody` (`spool.go`) to get
   `info.Verdict` and to count findings (see section 3 for the exact parsing
   rule).
2. Sets `m.decidePostConfirmState = decidePostConfirmAwaiting`,
   `m.decidePostPending = rec`, `m.decidePostFindingCount = <count>`,
   `m.decidePostVerdict = info.Verdict`.
3. Appends an activity line showing the prompt text: `fmt.Sprintf("Post %d
   findings (%s) to %s#%d? [y/N]", count, findingBreakdown, rec.Repo,
   rec.PR)` — matching the issue's example wording ("Post 7 findings (2
   warnings, 5 nits) to Bit-Quill/flowdra#24? [y/N]").
4. Returns `(m, nil)` — no `tea.Cmd`, since nothing runs until the user
   answers.

The confirm-gate keypress interception lives in `model.Update` (`tui.go`),
**at the same position in the dispatch order the deleted
`finalizeAwaitingConfirmation` block occupied** (per issue #102's spec: after
the `ctrl+y` copy handler, before the three `esc`-handling blocks — overlay
dismissal, planning-tab focus, `TabTypeReviewContent`-closes-on-`Esc` — and
before `m.confirmingExit`'s block). This preserves the exact ordering
invariant #102 fixed (an `Esc` while some *other* window happens to be active
must still hit this gate first) without having to re-derive it:

```go
if m.decidePostConfirmState == decidePostConfirmAwaiting {
    if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
        switch strings.ToLower(strings.TrimSpace(keyMsg.String())) {
        case "y", "yes":
            m.decidePostConfirmState = decidePostConfirmIdle
            rec := m.decidePostPending
            m = m.appendActivity(m.styles.Activity.Render(
                fmt.Sprintf("Posting review for %s#%d...", rec.Repo, rec.PR)))
            return m, launchDecidePostCmd(rec, m.decidePostVerdict)
        case "n", "no", "esc":
            m.decidePostConfirmState = decidePostConfirmIdle
            m = m.appendActivity(m.styles.Warning.Render(
                "Post cancelled — no decision recorded, review left in pending/."))
            return m, nil
        default:
            // Navigation/other key while awaiting confirmation: ignore,
            // fall through to normal handling — mirrors the #102-fixed
            // finalize confirm-gate's non-swallowing default arm exactly
            // (see issue #102's spec, Task 2, for why this must NOT
            // `return m, nil` unconditionally: doing so would re-introduce
            // the silent-cancel-on-navigation trap #102 fixed, this time
            // for decide post instead of finalize).
        }
    }
}
```

This satisfies the issue's requirement precisely: **no `decision:` is written
to front-matter on `N`/`esc`** — the write only happens in the `"y"` branch's
`tea.Cmd` (`launchDecidePostCmd`, see section 3), so cancelling truly leaves
the file untouched in `pending/` with no side effect at all, not even a
front-matter write that then gets ignored.

### 3. Exactly what `post` does end-to-end

**Repo/PR#/finding-count availability**: `review.Record` (`types.go:26`)
already carries `Repo` and `PR` — no new field needed there.
`SpoolInfo.Verdict` (`spool.go:23`) already carries the `verdict:`
front-matter value read via `ReadSpoolInfo`/`ReadSpoolBody`. **Finding count
is not currently parsed anywhere in Go** — findings are lines of the form
`file:line — severity — issue → fix` inside the review body (per
`review-poster.md`'s documented format and `pr_review_finalize.py`'s comment
"findings are `file:line - severity - issue -> fix`"). Add a small pure
function to `decideactions.go`:

```go
// countFindings counts finding lines in a review body — lines that start,
// after trimming, with a "file:line" pattern followed by " - " or " — "
// (both plain-hyphen and em-dash separators are used across the codebase's
// review outputs — review-poster.md's own examples use "—", while some
// generated bodies use "-"). This is a display-only heuristic (the same
// finding count review-poster.md's Python counterpart already treats as "a
// cheap sanity check", per the issue body) — it does not need to be a full
// review-body parser; false positives/negatives on unusually formatted lines
// only affect the confirm prompt's displayed number, not what gets posted
// (post still posts the whole body; see postReview below).
func countFindings(body string) (total int, breakdown string)
```

`breakdown` composes a short summary like `"2 warnings, 5 nits"` by also
matching the severity token (`critical`/`warning`/`nit`, case-insensitive,
matching `review-protocol`'s documented severities) inside each finding
line's second field; if no severities are recognized, `breakdown` is `""`
and the prompt falls back to `fmt.Sprintf("Post %d findings to %s#%d?
[y/N]", ...)` (no parenthetical) — a graceful degrade rather than a
misleading "0 warnings, 0 nits."

**`launchDecidePostCmd(rec review.Record, verdict string) tea.Cmd`** (new,
`decideactions.go`, following `runFinalizeCmd`'s established
`tea.Cmd`-returns-a-closure-that-does-blocking-work shape):

1. Write `decision: post` to front-matter **first**, via the existing
   `DecisionWriter.SetDecision(rec.SpoolPath, "post")` (already implemented,
   `decisionwriter.go:70` — reused as-is, no new code needed here). This is
   the crash-recovery write the issue's open question resolves: if the
   process dies between here and step 4 (archive), the file is left in
   `pending/` with `decision: post` already set, and a subsequent manual
   `decide post` retry (or, in principle, `finalize-reviews.sh` for an
   operator who wants to fall back to the headless path) can still act on
   it.
2. Read the current body via `ReadSpoolBody` (already implemented).
3. Best-effort humanize the body's prose via a one-shot `kiro-cli chat
   --no-interactive --trust-all-tools --agent humanizer "<prompt>"` call
   (new helper `humanizeReviewBody`, `decideactions.go`) — mirrors
   `ai-resources/workflows/humanize.py`'s exact preserve-instruction text
   ("Every `file:line` finding and the VERDICT: line must pass through
   byte-for-byte") and its degrade-to-original-on-any-failure contract
   (timeout, non-zero exit, empty stdout all fall back to the original body
   unchanged — this call must never be able to block or fail the post).
   Respect a `HOWMUX_NO_HUMANIZE` env var (new, checked once at the top of
   `humanizeReviewBody`) as the Go-side opt-out, mirroring
   `WORKFLOWS_NO_HUMANIZE` in the Python reference — needed for hermetic
   tests (see Task 8) so test runs never actually shell out to `kiro-cli`.
4. Parse findings from the (humanized) body into GitHub review comments:
   for each `file:line — severity — issue → fix` line, emit one
   `{"path": file, "line": line, "body": "<severity>: <issue> → <fix>"}`
   entry — same one-finding-per-inline-comment mapping
   `review-poster.md` specifies. Map `verdict` to the GitHub review
   `event`: `REQUEST_CHANGES` → `"REQUEST_CHANGES"`, `APPROVE` →
   `"APPROVE"`, anything else (including empty) → `"COMMENT"` — identical
   to `review-poster.md`'s documented mapping and to
   `pr-review-workflow.md`'s steering doc ("For `REQUEST_CHANGES` verdict,
   use `--field event="REQUEST_CHANGES"`. For `APPROVE` with nits, use
   `--field event="COMMENT"`").
5. Write the payload to a temp JSON file and POST via `gh api
   repos/<repo>/pulls/<pr>/reviews --method POST --input <tmpfile>` —
   following `tool-routing.md`'s documented rule verbatim ("never use
   `--field` for arrays/nested objects ... write the JSON payload to a temp
   file and use `--input`") and matching `review-poster.md`'s exact
   `gh api` invocation shape. Use `exec.CommandContext` via a new
   package-level seam `postReviewCommandFunc` (mirrors every other
   subprocess seam in this codebase — `execCommandFunc`,
   `finalizeCommandFunc`, `fetchDiffFunc`) so tests substitute a fake `gh`
   instead of hitting the network.
6. On `gh` success: archive the spool file to `done/` — a new function
   `archiveToDone(spoolPath string) error` (`decideactions.go`) that is the
   direct Go equivalent of `ai-resources/workflows/spool.py`'s
   `move_to_done()`: `os.Rename` from the `pending/…` path to its `done/…`
   counterpart (reusing the existing `derivePendingToDone`-style path
   swap already implemented for the read side in `spool.go`'s
   `resolveSpoolPath`, generalized into a shared `swapPendingDone(path,
   direction)` helper both the read path and this new write path call, so
   the pending↔done path-derivation logic exists in exactly one place).
   Return a terminal `decidePostCompleteMsg{rec, commentsPosted int}`.
7. On `gh` failure: **do not** archive (the file stays in `pending/` with
   `decision: post` already written from step 1 — this is exactly the
   crash-recovery behavior the issue's open-question resolution asks for,
   now also covering "gh itself failed," not just "process crashed").
   Return `decidePostErrorMsg{rec, err}`.

`model.Update` handles both terminal messages by appending the appropriate
activity line (success: `"Posted N inline comments to <repo>#<pr>, archived
to done/"`; failure: `"Failed to post review for <repo>#<pr>: <err> (decision
recorded; retry with decide post)"`) — no further state to reset since
`decidePostConfirmState` was already returned to idle in step "y" of the
confirm gate.

### 4. Exactly what `revise` / `rereview` / `discard` do end-to-end (no confirm)

**`discard`** (`launchDecideDiscard`, simplest — no subprocess at all):
1. Write `decision: discard` via the existing `DecisionWriter.SetDecision`
   (reused as-is — matches the crash-recovery-by-write-first pattern for
   symmetry with post, though discard has no real crash window since the
   very next step is synchronous).
2. Call the same new `archiveToDone` helper `post` uses (step 6 above).
3. This can run synchronously inside `handleDecide` itself (no `tea.Cmd`
   needed — no subprocess, no network, matching how `applyDecision` already
   runs synchronously today for the write-only case). Append activity line:
   `"Discarded review for <repo>#<pr> — archived to done/"`.

**`revise`** (`launchDecideRevise`, one `kiro-cli` call):
1. Read current body + `decision_notes` via `ReadSpoolBody` +
   `CurrentDecisionNotes` (both already implemented, `spool.go`).
2. Build the exact prompt `do_revise` uses in the Python reference
   (`pr_review_finalize.py:120-135`): "Revise a consolidated PR review.
   Existing consolidated review to revise: <body> Human revision notes
   (apply these): <notes, or literal '(no notes provided)' if empty> ...
   End with a final line exactly: `VERDICT: <APPROVE|COMMENT|REQUEST_CHANGES>`".
3. Call `kiro-cli chat --no-interactive --trust-all-tools --agent
   review-consolidator "<prompt>"` via the new one-shot helper (see Task 2),
   capture stdout, strip ANSI + leading `"> "` (mirroring
   `kiro_oneshot.py`'s `_clean_stdout`), extract the review body and the
   trailing `VERDICT:` line (two small pure functions,
   `extractReviewBody`/`parseVerdict`, direct Go ports of
   `pr_review.py`'s `extract_review_body`/`parse_verdict`).
4. Rewrite the spool file in place via the new `RewriteSpoolEntry` helper:
   new body, new verdict, **`decision` cleared back to `""`** — matching
   the issue's table ("Re-run the consolidator with notes, back to
   `pending/`") and the Python reference's `spool.rewrite(..., clear_decision=True)`.
   File stays in `pending/` (never archived).
5. Runs as a `tea.Cmd` (subprocess call, must not block Update) — reports
   back via `decideReviseCompleteMsg{rec, newVerdict}` /
   `decideReviseErrorMsg{rec, err}`.

**`rereview`** (`launchDecideRereview`, full lens fan-out — the most
substantial of the four):
1. Read `diff_file` from front-matter (`ParseSpoolFrontMatter`'s
   `fields["diff_file"]`, already parseable — no new parsing needed).
2. If `diff_file` is empty or the file no longer exists on disk (`os.Stat`
   check): **degrade to the exact `revise` behavior above** (same prompt
   template, same rewrite, same "back to pending/, decision cleared"
   outcome) — matching the Python reference's documented degrade path
   ("rereview → revise (diff gone)", `pr_review_finalize.py:200-206`) and
   the issue's own table note. This means `launchDecideRereview` should
   literally delegate to the same helper `launchDecideRevise` builds its
   `tea.Cmd` from, parameterized by an `acted` label ("revise" vs
   "rereview") only used in the resulting activity line's wording — do not
   duplicate the prompt-building/parsing logic between the two paths.
3. If the diff file exists: run the multi-lens fan-out. This is a direct
   port of `pr_review.py`'s `run_lens`/`run_consolidate` plus
   `pr_review_finalize.py`'s `do_rereview` orchestration:
   - Resolve the language-specific reviewer agent name. The Python
     reference's `resolve_language_reviewer(language, valkey=False)` needs a
     `language` argument the spool front-matter does not carry (confirmed —
     `_FM_KEYS` in `spool.py` has no `language` key). Mirror
     `pr_review_finalize.py`'s own default (`--language` defaults to
     `"python"`) for a first correct implementation; **this is a known,
     inherited limitation, not a new one this issue introduces** — flag it
     verbatim in a comment on the new Go function, and do not attempt to
     infer language from the diff in this issue (out of scope; the Python
     reference doesn't do this either).
   - Fan out to the cross-cutting lenses (`review-security`,
     `review-performance`, `review-testing`, `review-architecture` —
     mirroring `CROSS_CUTTING_LENSES` in `pr_review.py`) plus the language
     lens, each as one `kiro-cli` one-shot call, run **concurrently** via a
     `sync.WaitGroup`/goroutines (the direct Go equivalent of
     `ThreadPoolExecutor(max_workers=4).map(...)` in the Python reference) —
     each lens call passes the diff file path and a context string noting
     `decision_notes` if present (mirroring `do_rereview`'s `context`
     string).
   - Consolidate: one more `kiro-cli` call to `review-consolidator` with all
     lens outputs, producing the final body + verdict — same
     extract/parse helpers `revise` uses.
   - Same rewrite-in-place-with-decision-cleared outcome as `revise`.
4. Runs as a `tea.Cmd` — reports back via `decideRereviewCompleteMsg{rec,
   newVerdict}` / `decideRereviewErrorMsg{rec, err}` (or the revise
   messages, if degraded).

For **both** revise and rereview, `model.Update`'s handler appends
`"Revised review for <repo>#<pr> — new verdict: <verdict>, back in
pending/"` (or the rereview-specific wording) on success, or `"Failed to
<revise|rereview> review for <repo>#<pr>: <err> (decision NOT recorded —
retry with decide <action>)"` on failure — note revise/rereview never write
a `decision:` value at all before running (unlike post/discard), so a
failure here leaves the file exactly as it was pre-decide, which is already
the correct "nothing recorded" behavior with no extra code needed.

### 5. Keybinding changes for the Reviews tab (`p`/`r`/`R`/`d`)

`ReviewsTab.Update`'s `p`/`r`/`R`/`d` cases (`reviews_tab.go:474-481`)
currently call `decideSelectedCmd(decision)`, which emits `decideRequestMsg`
(`tui.go:62`), handled at `tui.go:742` by calling `applyDecision` — the same
write-only path `handleDecide` used. **No change to `ReviewsTab.Update`
itself or to `decideSelectedCmd`/`decideRequestMsg`'s shape is needed** — the
message already carries everything the new launcher needs
(`repo`/`pr`/`spoolPath`/`decision`). The only change is in the
**`decideRequestMsg` handler** in `model.Update` (`tui.go:742-758`): instead
of calling `m.applyDecision(rec, msg.decision)` unconditionally, it must
branch exactly like the rewritten `handleDecide` does in section 2 —
`"post"` → `startDecidePost`, others → their respective `launchDecideX`. To
avoid duplicating that branch in two places, extract it into one shared
helper both call:

```go
// dispatchDecideAction is the single shared branch both handleDecide (REPL)
// and model.Update's decideRequestMsg case (Reviews-tab p/r/R/d keys) call,
// so key-driven and command-driven decisions launch identically — the same
// role applyDecision played before this issue, now dispatching to a launch
// instead of a write. See section 2/5 of this spec.
func (m model) dispatchDecideAction(rec review.Record, decision string) (model, tea.Cmd)
```

`applyDecision` itself is deleted (see Inventory) — its only two callers
(`handleDecide`, the `decideRequestMsg` case) both move to
`dispatchDecideAction`.

This means the issue's AC7 ("The Reviews-tab keys `p`/`r`/`R`/`d` follow the
same launch-on-decide semantics as the REPL command") is satisfied by
construction — both entry points funnel through the one new dispatch
function, exactly mirroring how they already funneled through one
write-only function before.

**Confirm-gate interaction with keypresses vs. the REPL command line**: the
inline `y/N` confirm for `post` is a **keypress-level** gate in
`model.Update` (section 2), so it fires identically whether `post` was
launched via the REPL (`decide post` + Enter) or via the Reviews-tab `p` key
— once `decidePostConfirmState == decidePostConfirmAwaiting`, the very next
keypress (whether the user is focused on the REPL input or the Reviews tab)
is intercepted by the same gate before reaching either the input box or
`ReviewsTab.Update`. This matches the existing ordering guarantee issue #102
established for `finalizeAwaitingConfirmation` and requires no
tab-focus-awareness in the gate itself.

### 6. Documentation updates

**`README.md`**:
- Delete the "Finalizing Reviews" subsection (`README.md:154-156`) — replace
  with a short paragraph describing the new immediate-launch behavior:

  > Setting a decision on a review with `decide` (`post`, `revise`,
  > `rereview`, or `discard`) launches that action immediately for the
  > selected review. `revise` and `rereview` re-run locally and land back in
  > `pending/` with the decision cleared; `discard` archives to `done/`
  > right away. `post` is the one irreversible step — it shows an inline
  > `y/N` confirm (repo, PR number, and finding count) before publishing
  > inline PR comments and archiving to `done/`; declining leaves the
  > review untouched in `pending/` with no decision recorded.

- Remove the `finalize` row from the REPL Commands table (`README.md:188`).
- Reword the `decide` row (`README.md:187`) to reflect immediate action:
  `| \`decide <post\|revise\|rereview\|discard>\` | Launch the action
  immediately on the selected review (post shows an inline y/N confirm) |`

**In-app help text** (`handleHelp`, `commands.go:190-215`): the existing
`decide` line (`commands.go:199`, `"decide <value>  - Set decision on
selected review (post|revise|rereview|discard)"`) is reworded to `"decide
<value>  - Launch action on selected review (post confirms inline, others
run immediately)"`. No `finalize` line exists in `handleHelp` today (it was
never added there — confirmed by reading the full function body, lines
190-215), so there is nothing to remove from this specific list, only the
`decide` line to reword.

**No `docs/*.md` file mentions `finalize` or `decide`** (confirmed via
`grep -rl` across `docs/`) — no changes needed there.

## Inventory: Files to Delete

| File | Why |
|---|---|
| `internal/tui/finalize.go` | `finalizeState`, `runFinalizeCmd`, `pollFinalizeOutputCmd`, and all four finalize message types (`finalizeDryRunMsg`, `finalizeCompleteMsg`, `finalizeErrorMsg`, `finalizeTickMsg`) — the entire batch dry-run/live-run subprocess model this issue replaces with per-action launches. |
| `internal/tui/finalize_preflight.go` | `finalizePreflightState`, `checkFinalizeAssetsFunc`, `runFinalizePreflightCmd`, `applyFinalizePreflightResult`, `finalizePreflightResultMsg`, `finalizeRetryPreflightMsg` — the finalize-specific asset preflight. Superseded (see below) rather than dropped outright: the new action functions must still check that `kiro-cli`/`gh` are resolvable, but that check moves to a lighter-weight, per-action-invocation check inside `internal/review` (see Task 2), not a separate TUI-level preflight-before-window-opens step, since there is no longer a window to gate. |
| `internal/tui/finalize_test.go` | Tests `finalizeState`/`runFinalizeCmd`'s pure-function behavior — no longer applicable, the types are deleted. |
| `internal/tui/finalize_preflight_test.go` | Tests `CheckFinalizeAssets`/`finalizePreflightState` wiring — no longer applicable. |
| `internal/tui/finalize_integration_test.go` | Tests the full dry-run→confirm→live-run keypress flow — superseded by new tests for `decidePostConfirmState` (Task 8/9 below) covering the same *shape* of scenarios (navigation-doesn't-cancel, `y`/`n`/`esc` handling, ordering vs. other `Esc` handlers) against the new, smaller state machine. |
| `internal/review/finalize_assets.go` | `RequiredFinalizeAssets`, `CheckFinalizeAssets` — the TUI-side preflight for `finalize-reviews.sh`/`pr_review_finalize.py` on PATH. These two scripts are no longer invoked by the TUI at all after this issue (the new Go actions call `kiro-cli`/`gh` directly, never `finalize-reviews.sh`), so checking for them is checking the wrong thing. |
| `internal/review/finalize_assets_test.go` | Tests the above — no longer applicable. |

**Not deleted** (still used by the headless CLI path, explicitly out of
scope per the issue body): `ai-resources/scripts/finalize-reviews.sh`,
`ai-resources/workflows/pr_review_finalize.py`, and everything under
`ai-resources/` generally — none of that lives in this repo and none of it
is touched.

## Inventory: Files to Modify

| File | Functions/Types touched |
|---|---|
| `internal/tui/commands.go` | Delete `handleFinalize` (`:1014`) and `openFinalizePreviewWindow` (`:1063`). Rewrite `handleDecide` (`:966`) to dispatch via the new shared `dispatchDecideAction` instead of calling `applyDecision`. Delete `applyDecision` (`:952`) itself (folded into `dispatchDecideAction`, which now lives in the new `decideactions.go` or stays in `commands.go` — see Task 5, either placement is fine since it's TUI-orchestration code, not spool-format code). Add `startDecidePost` (new). |
| `internal/tui/command_registry.go` | Delete the `"finalize"` `Command` registration (`:110-113`). Reword the `"decide"` `Command`'s `Description` field (`:105-108`) to reflect immediate-launch semantics. |
| `internal/tui/tui.go` | Delete the `model` struct fields `finalizeState`, `finalizeCancel`, `finalizeCapture`, `finalizeLastGen`, `finalizeWindowTabID`, `finalizePreflightState`, `finalizePreflightErr` (wherever declared in the struct — grep confirms these are model fields referenced throughout `tui.go`/`commands.go`/`finalize.go`/`finalize_preflight.go`; find the exact struct block and remove each). Add new fields `decidePostConfirmState`, `decidePostPending`, `decidePostFindingCount`, `decidePostVerdict`. Delete the `finalizeAwaitingConfirmation` keypress-interception block (relocated/fixed by issue #102's spec — find its final position, described in #102's spec as "immediately after the `ctrl+y` copy-handling block ... before ... `overlay dismissal`") and replace it with the new `decidePostConfirmAwaiting` block at the **same position** (see section 2 above). Delete the `decideRequestMsg` case's body (`:742-758`) and replace with a call to the new shared `dispatchDecideAction`. Delete the four finalize message-type `case` arms in `model.Update`'s big switch (`finalizeDryRunMsg`, `finalizeCompleteMsg`, `finalizeErrorMsg`, `finalizeTickMsg` — these were handled somewhere in `tui.go`'s `Update`; locate via `grep -n "case finalize"` and remove each arm). Add new `case` arms for `decidePostCompleteMsg`, `decidePostErrorMsg`, `decideReviseCompleteMsg`, `decideReviseErrorMsg`, `decideRereviewCompleteMsg`, `decideRereviewErrorMsg` (each appends its activity line per sections 3/4 above). |
| `internal/tui/reviews_tab.go` | No change to `Update`'s `p`/`r`/`R`/`d` cases or `decideSelectedCmd`/`decideRequestMsg` shape (see section 5 — the message already carries what's needed). Delete `SetFinalizePreflightState`, `preflightState`/`preflightErr` fields, `finalizePreflightBlock`, `finalizePreflightWarningHeader` constant, and the `"F"` key case (finalize-gate retry — no longer meaningful, there is no separate finalize gate to retry; see Task 4 for whether `F` should instead retry *nothing* i.e. be removed, or be repurposed — this spec recommends removal, since the new per-action invocation has no equivalent "gate that can be pre-checked independently of an action" concept). Update `View()`/`CopyableContent()` to no longer render the preflight warning block (`finalizePreflightBlock` call site). |
| `internal/review/decisionwriter.go` | No change — `DecisionWriter.SetDecision` is reused as-is by the new post/discard action functions (it already does exactly "write decision: X to front-matter," which is step 1 of both). |
| `internal/review/spool.go` | Add the new shared `swapPendingDone(path string, toDone bool) string` helper (generalizing the existing `derivePendingToDone`, used by both the existing read path and the new `archiveToDone` write path). Add `RewriteSpoolEntry` (new — the Go equivalent of `spool.py`'s `rewrite()`). No change to any existing exported function's signature or behavior. |
| `README.md` | See section 6. |

## New Files

| File | Purpose |
|---|---|
| `internal/review/decideactions.go` | The core new logic: `PostReview`/`ReviseReview`/`RereviewReview`/`DiscardReview` (or similarly named exported entry points — exact naming left to the builder, but each must be a single exported function per action taking a `review.Record` and returning enough for the TUI to report success/failure), plus their private helpers (`countFindings`, `humanizeReviewBody`, `archiveToDone`, `extractReviewBody`, `parseVerdict`, the lens-fan-out orchestration, the `gh api` posting call) and package-level `*Func` seams for every subprocess call (`kiroOneshotFunc`, `postReviewCommandFunc`, etc.) so tests can substitute fakes. |
| `internal/review/decideactions_test.go` | Table-driven tests for the pure-function pieces (`countFindings`, `extractReviewBody`, `parseVerdict`, verdict→event mapping) plus subprocess-seam-substitution tests for each action function's success/failure paths (mirroring `decisionwriter_test.go`'s existing fake-script-via-env-var pattern for the `gh`/`kiro-cli` seams). |
| `internal/tui/decideactions.go` (or fold into `commands.go` — builder's choice) | `dispatchDecideAction`, `startDecidePost`, `launchDecideRevise`, `launchDecideRereview`, `launchDecideDiscard`, and the new message types (`decidePostCompleteMsg`, etc.), each a thin `tea.Cmd`-returning wrapper around the corresponding `internal/review` function — mirrors how `commands.go`'s `handleReview` is a thin wrapper around `review.RunReview`. |

## Team Orchestration / Parallelization

This is naturally two layers with a hard dependency between them:

- **Layer 1 (`internal/review` package)**: the new action logic. Self-contained,
  no dependency on `internal/tui`. Can be built and unit-tested in isolation.
- **Layer 2 (`internal/tui` package)**: dispatch, confirm-gate state machine,
  message plumbing, deletions. Depends on Layer 1's function signatures
  existing (even as stubs) to compile against.

Within Layer 1, the four action functions (post/revise/rereview/discard) are
independent of each other (each reads/writes a distinct spool file in a
test) and can be built in parallel by separate builders/tasks. Within Layer
2, the deletions (finalize.go, finalize_preflight.go, their tests,
command_registry.go's finalize entry) have no dependency on the new confirm
gate and can proceed in parallel with it; the confirm-gate/dispatch rewire
must land in the same PR as the deletions since `commands.go`/`tui.go` would
not compile with both the old and new paths present simultaneously (the
model struct can't have both `finalizeState` and expect `finalize.go`
deleted).

## Step-by-Step Task Breakdown

### Task 1: `internal/review` — spool rewrite/archive primitives

**Files:** `internal/review/spool.go` (add to, don't restructure)

- Add `swapPendingDone(path string, toDone bool) string`: when `toDone` is
  true, behaves exactly like the existing `derivePendingToDone`; when false,
  swaps the other direction (`done` → `pending`). Refactor
  `derivePendingToDone`'s current body to delegate to this new function with
  `toDone=true`, so there is exactly one path-swap implementation.
- Add `RewriteSpoolEntry(spoolPath string, newBody string, newVerdict
  string, clearDecision bool) error`: reads the file, replaces the `verdict:`
  front-matter line (adds if absent, mirroring `patchFrontMatterMetadata`'s
  existing add-if-absent pattern for `generated`/`reviewed_sha`), replaces
  everything after the closing fence with `newBody`, and if `clearDecision`
  is true, blanks the `decision:` line's value (not `decision_notes:` —
  notes are left as-is, matching the Python reference's `rewrite()`, which
  only touches the keys explicitly passed). Writes atomically via the same
  temp-file-same-directory-then-rename pattern `WriteReviewedMetadata`
  already uses (copy that discipline, do not invent a new one).
- Add `archiveToDone(spoolPath string) error`: computes the `done/`
  destination via `swapPendingDone(spoolPath, true)`, ensures the
  destination directory exists (`os.MkdirAll`, matching how `pending/`'s
  existence is assumed elsewhere — but `done/` may not have been created yet
  on a fresh install), and `os.Rename`s the file. Returns a descriptive
  error (not a bare `os.Rename` error) if the source doesn't exist or the
  rename fails.

**Acceptance criteria:**
- `TestSwapPendingDone` (table-driven): both directions, including the
  no-`pending`/no-`done` segment case (returns `""`, matching
  `derivePendingToDone`'s existing contract).
- `TestRewriteSpoolEntry_ReplacesBodyAndVerdict`,
  `TestRewriteSpoolEntry_ClearsDecision`,
  `TestRewriteSpoolEntry_PreservesOtherFrontMatterKeys`,
  `TestRewriteSpoolEntry_NoOpeningFence_ReturnsError`,
  `TestRewriteSpoolEntry_NoClosingFence_ReturnsError` (mirror
  `patchFrontMatterMetadata`'s existing test shapes in
  `finalize_assets_test.go`... actually `spool_test.go` — locate the
  existing `TestPatchFrontMatterMetadata*` tests and mirror their structure
  exactly for the new function).
- `TestArchiveToDone_MovesFile`,
  `TestArchiveToDone_CreatesDoneDirIfMissing`,
  `TestArchiveToDone_SourceMissing_ReturnsError`.

**Dependencies:** None.

---

### Task 2: `internal/review` — `kiro-cli` one-shot seam + `gh` posting seam

**File:** `internal/review/decideactions.go` (new)

- Add `kiroOneshotFunc` (package-level var, mirrors `execCommandFunc`'s
  seam pattern): signature `func(ctx context.Context, agentName string,
  prompt string) (string, error)`. Default implementation
  (`defaultKiroOneshot`) shells to `kiro-cli chat --no-interactive
  --trust-all-tools --agent <agentName> "<prompt>"` via
  `exec.CommandContext`, captures stdout, strips ANSI codes and a single
  leading `"> "` (port `kiro_oneshot.py`'s `_strip_ansi`/`_clean_stdout`
  regex logic to Go — same two regexes, `regexp.MustCompile`), and returns
  an error if the exit code is non-zero or the cleaned output is empty
  (mirroring `KiroOneshotError`'s two trigger conditions in the Python
  reference).
- Add `postReviewCommandFunc` (package-level var, mirrors `fetchDiffFunc`'s
  seam pattern): signature `func(ctx context.Context, payloadFile string,
  repo string, pr int) ([]byte, error)`. Default implementation shells to
  `gh api repos/<repo>/pulls/<pr>/reviews --method POST --input
  <payloadFile>` via `exec.CommandContext`, returns stdout (the JSON
  response, used to confirm success) or a wrapped error including `gh`'s
  stderr (mirroring `fetchDiffFunc`'s existing `exec.ExitError`-unwrapping
  pattern).
- Add `lookPathForDecideActionsFunc` (package-level var, same role as
  `lookPathFunc` in `assets.go`) so the new action functions can check
  `kiro-cli`/`gh` resolve on PATH before attempting to shell out, returning
  a clear "not found on PATH" error rather than a cryptic subprocess spawn
  failure — this is the lightweight, per-invocation replacement for the
  deleted `CheckFinalizeAssets` preflight (see Inventory: Files to Delete).

**Acceptance criteria:**
- `TestDefaultKiroOneshot_StripsAnsiAndPrompt` (pure function test against
  known ANSI-wrapped input, no subprocess).
- `TestDefaultKiroOneshot_EmptyOutput_ReturnsError`,
  `TestDefaultKiroOneshot_NonZeroExit_ReturnsError` (via a fake `kiro-cli`
  shell script substituted through `kiroOneshotFunc`, mirroring
  `commands_decide_test.go`'s `withFakeDecisionScript` env-var-controlled
  fake-script pattern).
- `TestPostReviewCommandFunc_Success`,
  `TestPostReviewCommandFunc_GhError_WrapsStderr` (same fake-script
  pattern, substituting a fake `gh`).

**Dependencies:** None (can run in parallel with Task 1).

---

### Task 3: `internal/review` — `discard` and `post` action functions

**File:** `internal/review/decideactions.go`

- `DiscardReview(rec review.Record) error`: resolve spool path (reject if
  not found/already done, same three-tier error messages
  `DecisionWriter.SetDecision` uses), call `DecisionWriter.SetDecision(...,
  "discard")`, call `archiveToDone(...)` (Task 1). No subprocess calls.
- `countFindings(body string) (total int, breakdown string)`: pure function,
  see section 3's exact contract above. Regex-match lines matching
  `^\s*\S+:\d+\s*[-—]` (file:line followed by a hyphen/em-dash separator);
  for `breakdown`, additionally look for `critical`/`warning`/`nit`
  (case-insensitive) as the next token and tally each.
- `humanizeReviewBody(ctx context.Context, body string) string` (returns
  `body` unchanged, never an error, per the "cosmetic, never load-bearing"
  contract): checks `os.Getenv("HOWMUX_NO_HUMANIZE") != ""` first (return
  original immediately); otherwise calls `kiroOneshotFunc(ctx, "humanizer",
  <prompt with the exact preserve-instruction text from section 3>)`; on
  any error or empty result, log via `logging.Debug` (matching this
  package's existing `logging` usage in `runner.go`) and return the
  original `body` unchanged.
- `extractReviewBody(raw string) string` / `parseVerdict(reviewText
  string) string`: direct ports of `pr_review.py`'s same-named functions —
  read that Python source before porting (`extract_review_body`,
  `parse_verdict`, around `pr_review.py:127-166`) to match the exact
  contract (verdict is the last line matching `^VERDICT:\s*(\S+)`, body is
  everything, trimmed).
- `buildReviewPayload(repo string, pr int, verdict string, body string)
  (payloadJSON []byte, event string, commentCount int, err error)`: parses
  findings from `body` (reuse `countFindings`'s line-matching regex to also
  extract `file`/`line`/`severity`/`issue`/`fix` capture groups — extend the
  regex rather than writing a second one), maps `verdict` to `event` per
  section 3's mapping table, and marshals the exact JSON shape
  `review-poster.md` specifies (`{"event":..., "body":..., "comments":[...]}`)
  via `encoding/json`.
- `PostReview(ctx context.Context, rec review.Record) (commentsPosted int,
  err error)`: orchestrates steps 1–7 from section 3, using
  `DecisionWriter.SetDecision`, `ReadSpoolBody`, `humanizeReviewBody`,
  `buildReviewPayload`, a temp file + `postReviewCommandFunc`, and
  `archiveToDone` on success (leaving the file in place on failure, per
  section 3 step 7).

**Acceptance criteria:**
- `TestDiscardReview_Success`, `TestDiscardReview_SpoolNotFound`,
  `TestDiscardReview_AlreadyInDone` (mirror
  `decisionwriter_test.go`'s existing error-case test shapes).
- `TestCountFindings` (table-driven: 0 findings, mixed severities, em-dash
  vs. hyphen separators, malformed lines ignored not crashed on).
- `TestHumanizeReviewBody_OptOutEnvVar_ReturnsOriginal`,
  `TestHumanizeReviewBody_KiroError_ReturnsOriginal`,
  `TestHumanizeReviewBody_Success_ReturnsHumanized` (fake `kiroOneshotFunc`).
- `TestExtractReviewBody`, `TestParseVerdict` (table-driven, mirror
  `pr_review.py`'s own test fixtures if any exist in `ai-resources` for
  parity — otherwise construct fixtures from `review-poster.md`'s examples).
- `TestBuildReviewPayload_MapsVerdictToEvent` (table-driven: `APPROVE` →
  `"APPROVE"`, `REQUEST_CHANGES` → `"REQUEST_CHANGES"`, `COMMENT`/`""`/
  unrecognized → `"COMMENT"`).
- `TestPostReview_Success_ArchivesToDone`,
  `TestPostReview_GhFails_LeavesInPending_DecisionAlreadyWritten` (the
  crash-recovery contract — assert the file is still in `pending/` AND its
  `decision:` field already reads `post` after a simulated `gh` failure).
- `TestPostReview_HumanizeFailureDoesNotBlockPost` (fake `kiroOneshotFunc`
  returns an error; assert `PostReview` still succeeds using the original
  body).

**Dependencies:** Task 1 (`archiveToDone`, `RewriteSpoolEntry` not directly
needed by post/discard but the package must compile), Task 2 (the seams).

---

### Task 4: `internal/review` — `revise` and `rereview` action functions

**File:** `internal/review/decideactions.go`

- `ReviseReview(ctx context.Context, rec review.Record) (newVerdict string,
  err error)`: build the exact prompt from section 4, call `kiroOneshotFunc(ctx,
  "review-consolidator", prompt)`, extract body/verdict, call
  `RewriteSpoolEntry(rec.SpoolPath, body, verdict, clearDecision=true)`
  (Task 1).
- `RereviewReview(ctx context.Context, rec review.Record) (newVerdict
  string, degradedToRevise bool, err error)`: check `diff_file` from
  front-matter + `os.Stat`; if missing/gone, call `ReviseReview` directly
  and return `degradedToRevise=true`. Otherwise fan out to the language
  lens (hardcoded `"python"` per the noted inherited limitation) plus the
  four cross-cutting lenses concurrently (`sync.WaitGroup`, one
  `kiroOneshotFunc` call each), consolidate via one more `kiroOneshotFunc`
  call to `review-consolidator`, then the same `RewriteSpoolEntry` call as
  `ReviseReview`.
- Both functions must respect `ctx` cancellation (pass it through to every
  `kiroOneshotFunc` call — the signature from Task 2 already takes a
  `context.Context` for exactly this).

**Acceptance criteria:**
- `TestReviseReview_Success_ClearsDecisionAndRewritesBody`.
- `TestReviseReview_NoNotesProvided_UsesPlaceholderText` (asserts the
  literal `"(no notes provided)"` string appears in the prompt sent to the
  fake `kiroOneshotFunc` when `decision_notes` is blank).
- `TestReviseReview_KiroCallFails_ReturnsError_SpoolUnchanged` (assert the
  spool file's body/decision are untouched — no partial rewrite on
  failure).
- `TestRereviewReview_DiffFileMissing_DegradesToRevise` (assert
  `degradedToRevise == true` and the fake `kiroOneshotFunc` was called with
  the `review-consolidator` agent, not any lens agent).
- `TestRereviewReview_DiffFileExists_FansOutToAllLenses` (assert the fake
  `kiroOneshotFunc` was called once per lens agent name plus once for
  consolidation — use a call-recording fake, mirroring
  `decisionwriter_test.go`'s call-count assertion style).
- `TestRereviewReview_LensCallFails_PropagatesError` (one lens's fake call
  returns an error; assert the whole operation fails rather than silently
  proceeding with partial lens results).

**Dependencies:** Task 1, Task 2. Independent of Task 3 (can run in
parallel).

---

### Task 5: `internal/tui` — delete `finalize.go`/`finalize_preflight.go` and their tests

**Files deleted:** `internal/tui/finalize.go`, `finalize_preflight.go`,
`finalize_test.go`, `finalize_preflight_test.go`,
`finalize_integration_test.go`.

**Files modified:**
- `internal/tui/tui.go`: remove the model struct fields listed in the
  Inventory table, remove the `finalizeAwaitingConfirmation` block, remove
  the four finalize-message `case` arms in `Update`.
- `internal/tui/commands.go`: remove `handleFinalize`,
  `openFinalizePreviewWindow`.
- `internal/tui/command_registry.go`: remove the `"finalize"` registration.
- `internal/tui/reviews_tab.go`: remove `SetFinalizePreflightState`,
  `preflightState`/`preflightErr` fields, `finalizePreflightBlock`,
  `finalizePreflightWarningHeader`, the `"F"` key case in `Update`, and the
  call site(s) in `View()`/`CopyableContent()` that render
  `finalizePreflightBlock`'s output.
- `internal/review/finalize_assets.go`, `finalize_assets_test.go`: delete.

**Acceptance criteria:**
- `go build ./...` succeeds with zero references to `finalizeState`,
  `finalizeAwaitingConfirmation`, `runFinalizeCmd`, `CheckFinalizeAssets`,
  `RequiredFinalizeAssets`, `finalizePreflightState`, or
  `SetFinalizePreflightState` anywhere in the repo.
  **Verification**: `grep -rn "finalizeState\|finalizeAwaitingConfirmation\|runFinalizeCmd\|CheckFinalizeAssets\|RequiredFinalizeAssets\|finalizePreflightState\|SetFinalizePreflightState" --include="*.go" .` returns zero matches.
- `go vet ./...` succeeds.

**Dependencies:** None — pure deletion, can happen in parallel with Tasks
1–4 and 6, but must land in the same PR as Task 7 (the model struct can't
have both old and new confirm-state fields coexist if `commands.go`
references both `handleFinalize` and the new dispatch in an inconsistent
state — in practice this task and Task 7 are done as one continuous edit
pass over the same files for exactly that reason).

---

### Task 6: `internal/tui` — new `decidePostConfirmState` machine + message types

**File:** `internal/tui/decideactions.go` (new) or added to `tui.go`/`commands.go`

- Add the `decidePostConfirmState` enum and model fields (section 2).
- Add message types: `decidePostCompleteMsg{rec review.Record,
  commentsPosted int}`, `decidePostErrorMsg{rec review.Record, err error}`,
  `decideReviseCompleteMsg{rec review.Record, newVerdict string}`,
  `decideReviseErrorMsg{rec review.Record, err error}`,
  `decideRereviewCompleteMsg{rec review.Record, newVerdict string,
  degradedToRevise bool}`, `decideRereviewErrorMsg{rec review.Record, err
  error}`, `decideDiscardCompleteMsg{rec review.Record}`,
  `decideDiscardErrorMsg{rec review.Record, err error}` (discard is
  synchronous per section 4, but still reports through a message for
  consistency with the others and so `dispatchDecideAction`'s return shape
  is uniform — `(model, tea.Cmd)` where the `tea.Cmd` can be a
  zero-latency `func() tea.Msg { return decideDiscardCompleteMsg{...} }`
  rather than special-cased as a direct `(model, nil)` return, keeping the
  five decision types structurally uniform for testability).
- Add the confirm-gate keypress-interception block in `model.Update`
  (section 2), positioned exactly where `finalizeAwaitingConfirmation`'s
  block was (per Task 5's deletion — insert this new block at that same
  position).
- Add `case` arms in `model.Update` for each new terminal message type,
  each appending the activity line specified in sections 3/4.
- Add `launchDecidePostCmd`, `launchDecideRevise`, `launchDecideRereview`,
  `launchDecideDiscard` (thin wrappers around the `internal/review`
  functions from Tasks 3/4, each building a `tea.Cmd` closure that calls
  the review-package function and maps its return to the corresponding
  message type).
- Add `dispatchDecideAction` (section 5) and delete `applyDecision`.

**Acceptance criteria:**
- `go build ./...` succeeds.
- `dispatchDecideAction` is the only caller-facing entry point both
  `handleDecide` and the `decideRequestMsg` case in `Update` invoke — no
  other code path calls `launchDecideX`/`startDecidePost` directly except
  through it. **Verification**: `grep -rn "launchDecideRevise\|launchDecideRereview\|launchDecideDiscard\|startDecidePost" --include="*.go" internal/tui/` shows exactly one call site for each, inside `dispatchDecideAction`.

**Dependencies:** Task 3, Task 4 (needs their function signatures to wrap),
Task 5 (needs the old fields/blocks gone to add the new ones cleanly —
though in practice done as one edit pass, see Task 5's note).

---

### Task 7: `internal/tui` — rewrite `handleDecide` and the `decideRequestMsg` case

**File:** `internal/tui/commands.go`, `internal/tui/tui.go`

- Rewrite `handleDecide` (section 2): keep the existing validation
  (arg count, vocabulary, row-selection, empty-spool-path checks —
  `commands.go:967-999`, unchanged), replace the final `m =
  m.applyDecision(rec, decision)` line with `return
  m.dispatchDecideAction(rec, decision)`.
- Rewrite the `decideRequestMsg` case in `model.Update` (`tui.go:742-758`):
  replace the body with the same empty-spool-path check
  `handleDecide` already performs (per the existing comment at that call
  site, "the same empty-spool-path check handleDecide performs ... still
  applies uniformly") followed by `return m.dispatchDecideAction(rec,
  msg.decision)`.

**Acceptance criteria (maps directly to issue ACs):**
- AC1 (`decide post` prompts inline `y/N`, `y` posts + archives, `N`/`esc`
  cancels with nothing recorded, file stays in `pending/`) — verified by
  Task 9's tests.
- AC2 (`decide revise` immediate consolidator re-run, no confirm) —
  verified by Task 9's tests.
- AC3 (`decide rereview` immediate lens fan-out, no confirm) — verified by
  Task 9's tests.
- AC4 (`decide discard` immediate archive, no confirm) — verified by Task
  9's tests.
- AC5 (invalid decision rejected at decide time, names valid options,
  records nothing) — **already satisfied**, unchanged from today's
  `handleDecide` validation block; add a regression test confirming this
  still holds post-rewrite (Task 9).
- AC7 (Reviews-tab keys match REPL semantics) — satisfied by construction
  via the shared `dispatchDecideAction` (Task 6).

**Dependencies:** Task 6.

---

### Task 8: `internal/review` — hermetic test harness for the new subprocess seams

**File:** `internal/review/decideactions_test.go`

Before writing the bulk of Task 3/4's tests, establish the shared test
helpers this file needs, mirroring `decisionwriter_test.go`'s /
`commands_decide_test.go`'s existing conventions:

- `withFakeKiroOneshot(t *testing.T) (calls *[]fakeKiroCall)` — substitutes
  `kiroOneshotFunc` with a fake that records `(agentName, prompt)` per call
  and returns a scripted response (configurable per-agent-name so a
  rereview test can script different responses for each lens vs. the
  consolidator), restoring the real func on test cleanup (`t.Cleanup`).
- `withFakeGhPost(t *testing.T) (calls *[]fakeGhCall)` — same pattern for
  `postReviewCommandFunc`.
- A fixture spool file builder (`writeFakeSpoolFile(t, dir, frontMatter
  map[string]string, body string) string`) so each test doesn't hand-roll
  front-matter strings — reduces duplication across the ~20 new test
  functions in Tasks 3/4.

**Acceptance criteria:**
- Every test in Tasks 3/4 uses these helpers rather than touching real
  `kiro-cli`/`gh`/network — **no test in this package may shell out to a
  real subprocess named `kiro-cli` or `gh`**. **Verification**: `grep -rn
  "exec.Command(\"kiro-cli\"\|exec.Command(\"gh\"" internal/review/*_test.go`
  returns zero matches (all subprocess execution in tests must go through
  the fake-substituted `*Func` vars).

**Dependencies:** None — should be built first, before Tasks 3/4's test
bodies, so this is really a sub-step of those tasks' ordering rather than a
separately schedulable task. Listed separately here only so the builder
sees it called out explicitly rather than reverse-engineering it while
writing Task 3's tests.

---

### Task 9: `internal/tui` — confirm-gate and dispatch integration tests

**File:** `internal/tui/decide_integration_test.go` (extend the existing
file — do not create a new one, since `decide_integration_test.go` already
exists and covers the pre-this-issue write-only `decide` flow; extend it to
cover the new launch semantics, removing/updating any assertions that
depended on the old write-only `applyDecision` behavior)

New/updated tests, following `finalize_integration_test.go`'s deleted
helpers' *shape* (`drainBatchForType`, `collectMsgs`, `activityContains` —
these are generic Bubble Tea test helpers, not finalize-specific; confirm
whether they're defined in `finalize_integration_test.go` itself (in which
case copy them into `decide_integration_test.go` before deleting the
original per Task 5) or in a shared test-helpers file (in which case no
copy needed) — **read the actual file locations before deleting**, since
Task 5 deletes `finalize_integration_test.go` and these helpers must not be
lost if it's their only definition site):

1. **`TestDecidePost_NavigationKeyDoesNotCancel`** — direct analog of the
   deleted `TestFinalizeConfirm_NavigationKeyDoesNotCancel`, retargeted at
   `decidePostConfirmAwaiting` instead of `finalizeAwaitingConfirmation`.
   Drive `handleDecide(["post"])` to reach the confirm state, then assert
   `[`, `]`, `f2`, arrow keys, and an unrelated letter leave
   `decidePostConfirmState` unchanged and produce no "cancelled" activity
   line.
2. **`TestDecidePost_LowercaseNCancels`** / **`TestDecidePost_UppercaseNCancels`**
   / **`TestDecidePost_EscCancels`** — direct analogs of the deleted
   finalize confirm tests. Assert: state returns to
   `decidePostConfirmIdle`, activity line contains "cancelled" and "no
   decision recorded", `postReviewCommandFunc`/`kiroOneshotFunc` fakes
   record **zero calls** (nothing was posted), and — critically, this is a
   new assertion the old finalize tests didn't need — **the spool file's
   `decision:` front-matter is still blank** (read the fixture file back
   from disk and assert `ParseSpoolFrontMatter` returns `decision == ""`),
   proving AC1's "no decision recorded" literally, not just via activity-line
   text.
3. **`TestDecidePost_YKeyPostsAndArchives`** — drive to confirm state,
   press `y`, drain the returned `tea.Cmd` for `decidePostCompleteMsg`, then
   assert: the fake `postReviewCommandFunc` was called once with the
   expected repo/pr, the fixture spool file no longer exists at its
   `pending/` path, and exists at the derived `done/` path.
4. **`TestDecidePost_GhFailure_LeavesDecisionWrittenInPending`** — drive to
   confirm state, press `y` with the fake `gh` seam returning an error,
   drain for `decidePostErrorMsg`, assert: the file is still in `pending/`,
   its `decision:` field already reads `post` (the crash-recovery
   contract), and the activity line names the retry path
   ("retry with decide post").
5. **`TestDecideRevise_ImmediateNoConfirm`** — drive `handleDecide(["revise"])`,
   assert the returned `tea.Cmd` is non-nil and, once drained, produces
   `decideReviseCompleteMsg` **without any intervening confirm keypress** —
   i.e. no `decidePostConfirmState`-equivalent gate exists for revise at
   all (there is no such state to check, which is itself the assertion:
   the model has no revise-confirm field to even inspect).
6. **`TestDecideRereview_ImmediateNoConfirm`** — same shape, for rereview,
   including a sub-case where the fixture's `diff_file` doesn't exist on
   disk and asserting the resulting message reports
   `degradedToRevise: true`.
7. **`TestDecideDiscard_ImmediateNoConfirm_ArchivesRightAway`** — drive
   `handleDecide(["discard"])`, assert immediate archive (file moved to
   `done/`) with no confirm keypress needed.
8. **`TestHandleDecide_InvalidVocabulary_StillRejectsBeforeDispatch`** —
   regression test: confirm the existing invalid-vocabulary rejection
   (already covered by `commands_decide_test.go`'s
   `TestHandleDecide_InvalidVocabulary`, which does NOT need to change
   since validation is untouched) still passes after the rewrite — this
   can be "no new test needed, existing test in `commands_decide_test.go`
   already covers AC5 and requires no modification," which the builder
   should verify by running that existing test post-rewrite rather than
   assuming.
9. **`TestReviewsTabKeys_MatchReplDecideSemantics`** — drive the Reviews-tab
   `p` key (via `ReviewsTab.Update` + the resulting `decideRequestMsg`
   round-tripped through `model.Update`, mirroring how existing
   `decide_routing_test.go` tests already drive key-to-message-to-model
   round trips) and assert it reaches the exact same
   `decidePostConfirmAwaiting` state `decide post` (REPL) reaches — proving
   AC7 by direct behavioral comparison, not just "they call the same
   function" (which Task 6/7 already guarantees structurally, but a
   behavioral test catches a future regression that structural sharing
   alone wouldn't).

**Existing tests requiring deletion/rewrite** (beyond the whole-file
deletions in Task 5):
- `internal/tui/commands_decide_test.go`'s
  `TestHandleDecide_ValidSpoolPath_WriterSuccess` and
  `TestHandleDecide_ValidSpoolPath_WriterError` currently assert on
  `"Set decision on PR #%d to '%s'"` — this exact activity-line text no
  longer appears anywhere (the new code produces action-specific lines
  like "Discarded review for..." / "Posting review for..."). These two
  tests must be **rewritten**, not just left alone — they will fail to
  compile-and-pass as-is once `applyDecision` is deleted (Task 6), since
  `withFakeDecisionScript`'s fake `set-review-decision.sh` still works for
  the discard/post decision-write step, but the assertion text and the
  synchronous-no-`tea.Cmd` expectation (`if cmd != nil { t.Errorf(...) }`)
  are both wrong for `revise`/`rereview`/`post` post-rewrite (those now
  return a non-nil `tea.Cmd`). Rewrite each to match its decision's new
  contract: `discard` stays synchronous (`cmd` may still be non-nil per
  Task 6's uniformity note — verify against the final Task 6
  implementation and adjust the nil-check accordingly), `post` reaches the
  confirm gate rather than writing anything.
- `internal/tui/commands_decide_test.go`'s `TestHandleDecide_WorksWhenDifferentTabActive`
  similarly asserts the old success-line text with `decision: "post"` —
  rewrite to assert reaching `decidePostConfirmAwaiting` instead (posting
  itself requires a second keypress, which this test did not previously
  need to simulate since the old path was one-shot).
- `internal/tui/reviews_tab_test.go`: any test asserting on
  `SetFinalizePreflightState`/`preflightState`/the `"F"` key/
  `finalizePreflightBlock`'s rendered output must be deleted (the fields/
  function no longer exist per Task 5).

**Dependencies:** Task 7 (needs the rewritten dispatch to test against),
Task 8 (test harness conventions, though this is the `internal/tui`
package's own harness, likely simpler — mostly reusing
`commands_decide_test.go`'s existing `withFakeDecisionScript`/
`addReviewsTabWithRecord` helpers plus new fakes for the `internal/review`
seams the TUI layer's `tea.Cmd` wrappers ultimately call through to).

---

### Task 10: Documentation

**Files:** `README.md`, `internal/tui/commands.go` (`handleHelp`)

- Apply the README changes from section 6 (delete "Finalizing Reviews"
  subsection + replacement paragraph, remove `finalize` REPL table row,
  reword `decide` REPL table row).
- Apply the `handleHelp` reword from section 6.

**Acceptance criteria:**
- **Verification**: `grep -n "finalize" README.md` returns zero matches
  (case-sensitive; `Finalizing`/`finalize-reviews.sh` etc. are all covered
  by this pattern) — confirms complete removal from the TUI-facing docs
  (the CLI-scripts docs like `custom-scripts.md` in `ai-resources`, which
  document the headless `finalize-reviews.sh`, are a different repo and
  untouched).
- **Verification**: `grep -n "finalize" internal/tui/commands.go` returns
  zero matches.

**Dependencies:** None — can run in parallel with everything else, though
logically sequenced last here since the wording should reflect the final
implemented behavior.

## Concurrency Analysis

**New concurrency concern introduced by `rereview`'s lens fan-out (Task
4).** `RereviewReview` runs multiple `kiroOneshotFunc` calls concurrently via
goroutines (the Go equivalent of the Python reference's
`ThreadPoolExecutor.map`). This is a **new** cross-goroutine boundary that
does not exist anywhere else in this codebase today (every existing
subprocess call in `internal/review`/`internal/tui` — `RunReview`,
`runFinalizeCmd`, `fetchDiffFunc` — runs exactly one subprocess at a time,
synchronously within a single `tea.Cmd`'s goroutine).

- **Shared state accessed across the fan-out goroutines**: a
  `map[string]lensResult` (or slice) collecting each lens's output. This
  must be written by each goroutine and read only after `sync.WaitGroup.Wait()`
  returns — the simplest safe pattern is each goroutine writing to its own
  pre-allocated slice index (no shared map, no mutex needed at all), which
  is the pattern to use here: allocate `results := make([]lensResult,
  len(lensNames))` before spawning, have goroutine `i` write only to
  `results[i]`, and never read `results` until after `wg.Wait()`. This
  avoids needing a `sync.Mutex` entirely by construction (each goroutine
  owns a disjoint slice index), which is preferable to adding a lock for a
  first implementation — but **only if the builder follows this exact
  disjoint-index pattern**; a map keyed by lens name written concurrently
  from multiple goroutines WOULD need a `sync.Mutex` and must not be used
  without one.
- **Acceptance criterion**: "The lens fan-out in `RereviewReview` writes
  each goroutine's result to a disjoint slice index (no shared map, no
  concurrent writes to the same memory location) — verified by a test run
  under `go test -race`."
- **Required concurrent test**: `TestRereviewReview_LensFanOut_NoRaceCondition`
  — run with `go test ./internal/review/... -race -run
  TestRereviewReview_LensFanOut` — exercises the concurrent fan-out with
  the fake `kiroOneshotFunc` (from Task 8) deliberately introducing a small
  random `time.Sleep` per call to maximize the chance of exposing any
  accidental shared-state write if the disjoint-index pattern is not
  followed correctly.

No other part of this issue introduces a concurrency concern:
`decidePostConfirmState` and the other new model fields are mutated only
inside `model.Update` (the single Bubble Tea Update goroutine), exactly
like every other model field this codebase already documents as safe
without locking (`finalizeState`'s own doc comment made the identical
claim, which this issue's deletion of that type does not change the
validity of for the new, analogous field). The `tea.Cmd` closures
(`launchDecidePostCmd` etc.) run on their own goroutine per Bubble Tea's
model, but report back via exactly one terminal `tea.Msg` each — the same
single-writer-then-message-handoff pattern `runFinalizeCmd`/`RunReview`
already use safely.

## Validation Commands

Run from the worktree root:

```bash
cd /Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-109-63108

# Build
go build ./...
go vet ./...

# internal/review — new action logic (Tasks 1-4, 8)
go test ./internal/review/... -race -v

# internal/tui — dispatch, confirm-gate, deletions (Tasks 5-7, 9)
go test ./internal/tui/... -race -v

# Targeted new-behavior tests
go test ./internal/review/... -race -run 'TestDiscardReview|TestPostReview|TestReviseReview|TestRereviewReview|TestCountFindings|TestBuildReviewPayload' -v
go test ./internal/tui/... -race -run 'TestDecidePost|TestDecideRevise|TestDecideRereview|TestDecideDiscard|TestHandleDecide' -v

# Confirm complete removal of the deleted finalize surface
grep -rn "finalizeState\|finalizeAwaitingConfirmation\|runFinalizeCmd\|CheckFinalizeAssets\|RequiredFinalizeAssets\|finalizePreflightState\|SetFinalizePreflightState" --include="*.go" . && echo "FAIL: finalize surface still referenced" || echo "OK: finalize surface fully removed"

# Confirm no test shells to a real kiro-cli/gh
grep -rn 'exec.Command("kiro-cli"\|exec.Command("gh"' internal/review/*_test.go && echo "FAIL: test hits real subprocess" || echo "OK: all subprocess calls are seamed"

# Docs
grep -n "finalize" README.md internal/tui/commands.go && echo "FAIL: stale finalize doc reference" || echo "OK: docs updated"

# Full suite, race-enabled
go test ./... -race
```

All of the above must pass with no new failures and no `-race` warnings.
