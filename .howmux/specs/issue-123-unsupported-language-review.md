# Design Spec: Surface Unsupported-Language Review Failures Instead of Silently Retrying

Closes #123

## Assumptions

- The worktree path supplied by the orchestrator
  (`/Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-123-46443`)
  exists and is fully populated (real git worktree with `.git`, `go.mod`,
  `internal/`, etc.) — confirmed by direct read, no fallback path was needed.
- The issue text's claim that a prior design spec is committed at
  `.howmux/specs/issue-unsupported-language-review.md` is **false** —
  verified: that file does not exist anywhere in the worktree. There is an
  unrelated, pre-existing `.howmux/specs/issue-123-fix-eval-framework-silent-failures.md`
  from a different historical issue #123 (about the eval framework); it has
  zero relevance to this issue and is ignored entirely. This spec is
  written from scratch, to the correct filename
  (`issue-123-unsupported-language-review.md`), per the orchestrator's
  explicit instruction.
- `pr_review.py` itself (ai-resources repo) is out of scope for changes —
  confirmed against the issue's own "Out of scope" section. Its exit-code
  and stderr-message contract is treated as a fixed, external interface
  that `howmux` must classify against, not something this PR can alter.

## Problem Statement (recap)

`RunReview` always invokes `pr_review.py … --language auto`. When the PR's
diff is in a language `pr_review.py` doesn't support (e.g. C/C++), the tool
exits 2 with a stderr message, and `RunReview` treats this exactly like any
other failure: call `revertToWatching`, return a wrapped error. Because
`revertToWatching` resets the record to `StatusWatching` and
`LastReviewedSHA` is never set, `decideReviewAction`'s Rule 2 ("never
reviewed → `ActionReview`") fires again on the very next poll — an infinite,
silent retry loop with no durable marker, no spool file, no worktree, and no
visible error.

The fix must distinguish "diff is in an unsupported/undetectable language"
(a real, durable, user-facing terminal-ish state) from other exit-2 failures
like a missing diff file (a transient/generic bug that should keep retrying
via the existing `revertToWatching` path).

## Codebase Findings (verified against the actual worktree)

### 1. `RunReview`'s current error path and why it causes the silent retry loop

File: `internal/review/runner.go`

- `runReviewToolFunc` (lines 36–59) is the subprocess seam: it builds
  `cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)`, sets
  `cmd.Stderr = stderrWriter` (line 44), and calls `cmd.Output()` (line 46).
  On a non-zero exit it returns
  `fmt.Errorf("command failed (exit %d): %w", exitErr.ExitCode(), err)`
  (line 49) — **the actual stderr text is never captured into this error
  or anywhere else**; it is only streamed live to `stderrWriter` (the tab
  writer), which `Watcher.dispatch` currently passes `io.Discard` for
  (watcher.go line ~241, `// TODO: wire actual review tab writer`). This
  means today, the unsupported-language stderr message
  (`Error: could not determine the language from the diff...`) is
  discarded entirely in the watcher path and never available to `RunReview`
  for classification.
- `RunReview` (lines 128–230) always constructs argv with
  `"--language", "auto"` (lines 164–170) — there is no branch that passes an
  explicit language, confirming the issue's root-cause claim.
- The call site: lines 177–181:
  ```go
  stdoutLines, err := runReviewToolFunc(ctx, argv, tabWriter)
  if err != nil {
      revertToWatching(storeImpl, rec)
      return fmt.Errorf("pr_review.py invocation failed: %w", err)
  }
  ```
  This is the **only** branch reached on a `pr_review.py` exit-2 failure
  (whether unsupported-language, missing-diff, or any other exit-2
  condition) — `revertToWatching` is called unconditionally, with no
  inspection of *why* the command failed.
- `revertToWatching` (lines 104–112):
  ```go
  func revertToWatching(storeImpl StoreInterface, rec Record) {
      rec.Status = StatusWatching
      if err := storeImpl.Save(rec); err != nil {
          logging.Warn(...)
      }
  }
  ```
  sets `rec.Status = StatusWatching` and saves — **no new field, no marker,
  no distinction from the pre-review state**. The persisted record is
  byte-for-byte indistinguishable from a freshly-enrolled, never-reviewed
  PR, except that `rec.LastReviewedSHA` was already empty and stays empty.
- `decideReviewAction` (`internal/review/decision.go`, lines 17–39), Rule 2
  (lines 27–29):
  ```go
  if rec.LastReviewedSHA == "" {
      return ActionReview
  }
  ```
  fires again on the very next poll because nothing in the error path ever
  sets `LastReviewedSHA`. This is the exact mechanism of the "silent retry
  trap": `RunReview` fails → reverts to `StatusWatching` with
  `LastReviewedSHA` still `""` → next `pollOnce()` (watcher.go lines
  170–216) re-decides `ActionReview` → dispatches again → fails again,
  forever. `Watcher.dispatch`'s only observable state during this cycle is
  the brief `StatusReviewing` write inside `RunReview` (line 143) followed
  immediately by the revert — which is exactly the "watching → reviewing →
  watching" flicker the issue describes.
- No spool file and no worktree are created on this path because the
  failure happens entirely inside `runReviewToolFunc`'s `cmd.Output()` call
  — `pr_review.py` itself never gets far enough to print a spool path
  (there is no stdout on a stderr-reported exit-2 failure), and nothing in
  `RunReview` creates a worktree directly (that's `commands.go`'s
  `ensureCheckoutFunc`, a separate, already-completed step by the time
  `RunReview` is dispatched from the watcher path — the watcher path
  doesn't call checkout at all, only `commands.go`'s manual-enroll path
  does, per `internal/tui/commands.go` lines ~1080–1086). So "creates no
  worktree, writes no spool file" is already true today for this failure
  mode and must remain true — nothing in the proposed design needs to touch
  worktree/spool creation.

### 2. The tee pattern — capturing stderr while still streaming it to the tab writer

File: `internal/review/runner.go`

- Current signature of the seam (lines 36–37):
  ```go
  var runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) (stdoutLines []string, err error) {
  ```
  and inside, line 44: `cmd.Stderr = stderrWriter`.
- **This signature does not need to change.** The tee happens at the
  **call site** in `RunReview`, not inside the seam/`runReviewToolFunc`
  itself — construct a `bytes.Buffer` and an `io.MultiWriter(tabWriter,
  &stderrBuf)`, and pass that `io.MultiWriter` as the `stderrWriter`
  argument instead of passing `tabWriter` directly. This keeps
  `runReviewToolFunc`'s contract (and every existing test that fakes it)
  completely unchanged — the function still just writes everything it's
  given to the single `io.Writer` it receives; `RunReview` is simply
  constructing a different writer value for the already-existing parameter.
- Exact change, in `RunReview`, immediately before the existing call at
  line 177:
  ```go
  var stderrBuf bytes.Buffer
  teeWriter := io.MultiWriter(tabWriter, &stderrBuf)

  stdoutLines, err := runReviewToolFunc(ctx, argv, teeWriter)
  if err != nil {
      if reason, ok := classifyUnsupportedLanguage(stderrBuf.String()); ok {
          rec.Status = StatusUnsupported
          if saveErr := storeImpl.Save(rec); saveErr != nil {
              logging.Warn("failed to persist StatusUnsupported", "repo", rec.Repo, "pr", rec.PR, "error", saveErr)
          }
          notifyUnsupported(rec, reason)
          return fmt.Errorf("%w: %s", errUnsupportedLanguage, reason)
      }
      revertToWatching(storeImpl, rec)
      return fmt.Errorf("pr_review.py invocation failed: %w", err)
  }
  ```
  (`errUnsupportedLanguage`, `classifyUnsupportedLanguage`, and
  `notifyUnsupported` are new; see sections 3 and 6 below.)
- New import needed in `runner.go`: `"bytes"` (not currently imported —
  confirmed against the current import block, lines 3–16: `context`,
  `fmt`, `io`, `os`, `os/exec`, `path/filepath`, `strconv`, `strings`,
  `time`, plus the two internal packages).
- This is a one-sided tee: `tabWriter` keeps receiving the live stream
  exactly as before (so the Reviews/Agent tab still shows real-time
  `pr_review.py` stderr output during the run — unchanged UX for every
  existing non-error and other-error path), while `stderrBuf` accumulates
  the same bytes for post-hoc classification once the subprocess call
  returns. No buffering delay is introduced to the tab writer because
  `io.MultiWriter` writes to both writers synchronously, in order, on every
  `Write` call `runReviewToolFunc`'s underlying `cmd.Stderr` plumbing makes.

### 3. Stderr substring matching to distinguish unsupported-language from other exit-2 failures

File: `internal/review/runner.go` (new code)

- Per the issue's "Key contract detail," the exact stderr substrings to
  match, verbatim as given in the issue:
  - `could not determine the language from the diff`
  - `unsupported language '`
- New pure helper function (testable without a subprocess), placed near
  `validateSpoolPath` (after line 102, before `revertToWatching`):
  ```go
  // unsupportedLanguageMarkers are the exact pr_review.py stderr substrings
  // that indicate an unsupported-or-undetectable-language failure, per the
  // issue #123 contract. pr_review.py uses exit code 2 for several
  // DISTINCT conditions (diff-not-found, language-undetected,
  // explicit-unsupported-language) — exit code alone cannot distinguish
  // them, so classification must match on these stderr substrings. Any
  // exit-2 (or other) failure whose stderr does NOT contain one of these
  // markers is a generic failure and must fall through to the existing
  // revertToWatching path unchanged — in particular, a missing-diff-file
  // failure (also exit 2) must NOT match here.
  var unsupportedLanguageMarkers = []string{
      "could not determine the language from the diff",
      "unsupported language '",
  }

  // errUnsupportedLanguage is a sentinel error RunReview wraps and returns
  // on the unsupported-language path, so callers (and tests) can detect
  // this specific condition via errors.Is without string-matching the
  // returned error's text.
  var errUnsupportedLanguage = errors.New("unsupported language")

  // classifyUnsupportedLanguage reports whether stderrText (the captured
  // stderr from a failed pr_review.py invocation) matches one of
  // unsupportedLanguageMarkers, and if so, returns the trimmed stderr text
  // itself as the human-readable reason (it already IS the message
  // pr_review.py printed, e.g. "Error: could not determine the language
  // from the diff (no dominant supported file type). Re-run with an
  // explicit language: astro, go, java, node, python."). Returns ("",
  // false) for every other failure, including a missing-diff-file error,
  // so that case continues to revertToWatching via the existing generic
  // path.
  func classifyUnsupportedLanguage(stderrText string) (reason string, matched bool) {
      trimmed := strings.TrimSpace(stderrText)
      if trimmed == "" {
          return "", false
      }
      for _, marker := range unsupportedLanguageMarkers {
          if strings.Contains(trimmed, marker) {
              return trimmed, true
          }
      }
      return "", false
  }
  ```
- New import needed: `"errors"` (for `errors.New`; not currently imported
  in `runner.go`).
- **Negative-case contract (acceptance criterion 3 from the issue):** a
  missing-diff-file stderr message (e.g. `Error: diff file not found:
  <path>` or similar — the exact missing-diff wording is pr_review.py's,
  not reproduced verbatim in the issue, so the unit test must use a
  *distinct* stderr string that deliberately does NOT contain either
  marker substring) must return `("", false)` from
  `classifyUnsupportedLanguage`, causing `RunReview` to fall through to the
  existing `revertToWatching(storeImpl, rec)` + generic
  `"pr_review.py invocation failed: %w"` error — i.e., **no behavior
  change at all** for this case. This is the critical regression the issue
  explicitly calls out: "Exit code alone is NOT enough."

### 4. New `StatusUnsupported` value and `Validate()` update

File: `internal/review/types.go`

- Current `Status` block (lines 8–14):
  ```go
  const (
      StatusWatching  Status = "watching"  // Enrolled, waiting for review trigger
      StatusReviewing Status = "reviewing" // Review in progress
      StatusReviewed  Status = "reviewed"  // Reviewed at a SHA, awaiting the next push (non-terminal)
      StatusDone      Status = "done"      // Terminal: PR merged/closed
  )
  ```
- Exact change — add a fifth value:
  ```go
  const (
      StatusWatching     Status = "watching"     // Enrolled, waiting for review trigger
      StatusReviewing    Status = "reviewing"    // Review in progress
      StatusReviewed     Status = "reviewed"     // Reviewed at a SHA, awaiting the next push (non-terminal)
      StatusDone         Status = "done"         // Terminal: PR merged/closed
      StatusUnsupported  Status = "unsupported"  // Diff language unsupported/undetectable by pr_review.py; not re-dispatched until re-enrolled
  )
  ```
- Current `Validate()` allowed-set check (line ~56, inside `Validate()`):
  ```go
  if r.Status != StatusWatching && r.Status != StatusReviewing && r.Status != StatusReviewed && r.Status != StatusDone {
      return fmt.Errorf("invalid status: %s", r.Status)
  }
  ```
- Exact change:
  ```go
  if r.Status != StatusWatching && r.Status != StatusReviewing && r.Status != StatusReviewed && r.Status != StatusDone && r.Status != StatusUnsupported {
      return fmt.Errorf("invalid status: %s", r.Status)
  }
  ```
- This is the only change required in `types.go`. `Record`'s field set
  needs no new field — `StatusUnsupported` is carried entirely in the
  existing `Status` field; the "reason" string is **not** persisted on the
  `Record` (there is no `UnsupportedReason` field to add) — per the issue's
  scope, the reason is surfaced once via the notification (section 6) and
  via the Reviews tab's distinct status rendering (section 7), not stored
  durably. (If a future issue wants the reason to survive a `howmux`
  restart for display, that would be a separate, additive field — out of
  scope here, since the issue's acceptance criteria only require the
  *status* to persist, not the reason text.)

### 5. `decideReviewAction`: where `StatusUnsupported` → `ActionSkip` fits

File: `internal/review/decision.go`

- Current full rule ordering (lines 17–39):
  ```go
  func decideReviewAction(pr github.PR, rec Record, reviewer string) ReviewAction {
      // Rule 1: PR is terminal (merged or closed) → prune from tracking
      if pr.IsTerminal() {
          return ActionPrune
      }

      // Rule 2: Never reviewed (empty LastReviewedSHA) → review
      if rec.LastReviewedSHA == "" {
          return ActionReview
      }

      // Rule 3: Open + review requested for me
      if pr.IsReviewRequestedFor(reviewer) {
          if rec.LastServicedRequest == pr.HeadSHA() {
              return ActionSkip
          }
          return ActionReview
      }

      // Rule 4: Open but not requested → skip
      return ActionSkip
  }
  ```
- **Critical ordering constraint (explicit in the issue and verified
  against the acceptance criteria):** the new `StatusUnsupported` check
  must sit **after** Rule 1 (`pr.IsTerminal()` → `ActionPrune`), so that "a
  merged/closed PR in `StatusUnsupported` is still pruned" (issue
  acceptance criterion) continues to work — pruning must win over the
  unsupported-skip rule. It must also sit **before** Rule 2 (the
  `LastReviewedSHA == ""` check), because a `StatusUnsupported` record's
  `LastReviewedSHA` is (and remains) empty forever on this path — Rule 2
  would otherwise re-fire `ActionReview` for exactly the record this issue
  needs to stop re-dispatching, reproducing the original bug.
- Exact change — insert a new rule between the current Rule 1 and Rule 2:
  ```go
  func decideReviewAction(pr github.PR, rec Record, reviewer string) ReviewAction {
      // Rule 1: PR is terminal (merged or closed) → prune from tracking
      if pr.IsTerminal() {
          return ActionPrune
      }

      // Rule 1b: Previously classified as unsupported-language → skip.
      // This is a deliberate, documented exception to this function's
      // general "does not read Status" design (see the package doc above):
      // StatusUnsupported is the one Status value decideReviewAction must
      // read, specifically to stop the silent retry loop described in
      // issue #123 — a record stuck here has (and will keep) an empty
      // LastReviewedSHA, so without this check Rule 2 below would
      // re-dispatch ActionReview on every poll forever. Must stay AFTER
      // the terminal/prune check above (a merged/closed PR must still be
      // pruned even if it was unsupported) and BEFORE Rule 2 (which would
      // otherwise re-fire on the permanently-empty LastReviewedSHA).
      if rec.Status == StatusUnsupported {
          return ActionSkip
      }

      // Rule 2: Never reviewed (empty LastReviewedSHA) → review
      if rec.LastReviewedSHA == "" {
          return ActionReview
      }

      // Rule 3: Open + review requested for me
      if pr.IsReviewRequestedFor(reviewer) {
          if rec.LastServicedRequest == pr.HeadSHA() {
              return ActionSkip
          }
          return ActionReview
      }

      // Rule 4: Open but not requested → skip
      return ActionSkip
  }
  ```
- The package-level doc comment on `decideReviewAction` (lines 7–15) must
  also be updated to list the new rule in its summary, e.g. add a bullet:
  `- Previously unsupported-language → ActionSkip (unless terminal, which
  still prunes)`.

### 6. `notifyFunc` call site — matching the #117 pattern

Files: `internal/review/runner.go` (reads `.howmux/specs/issue-117-notify-review-complete.md` for precedent)

- The `notifyFunc` seam already exists in `runner.go` (from issue #117),
  declared at line 68:
  ```go
  var notifyFunc = notify.Notify
  ```
  and the existing success-path call site (lines 215–221) is:
  ```go
  notifyFn := notifyFunc
  go func(repo string, pr int) {
      msg := fmt.Sprintf("Review ready: %s #%d", repo, pr)
      if err := notifyFn("howmux", msg); err != nil {
          logging.Warn("failed to send review-complete notification", "repo", repo, "pr", pr, "error", err)
      }
  }(rec.Repo, rec.PR)
  ```
  Note the documented rationale (comment above it, lines ~205–214): read the
  `notifyFunc` seam on `RunReview`'s own goroutine and close over the local
  copy, not the package global, so a test restoring the seam in
  `t.Cleanup` cannot race the background goroutine's read of it.
- **New call site, on the unsupported path only**, following the exact
  same local-copy-then-goroutine pattern, added as a small helper
  (`notifyUnsupported`) called from inside the new branch in section 2's
  code block, so the pattern is defined once and the main `RunReview` body
  stays readable:
  ```go
  // notifyUnsupported fires a best-effort, non-blocking notification that a
  // review attempt failed because the diff's language is unsupported or
  // undetectable, mirroring the exact goroutine/local-copy pattern RunReview's
  // success-path notification already uses (see notifyFunc's seam doc
  // comment above): read notifyFunc into a local on the caller's
  // goroutine, close over the local (not the package global) inside the
  // spawned goroutine, and log-and-continue on failure — never propagate.
  // This runs on the unsupported-classification path only; every other
  // error path in RunReview (generic failures via revertToWatching) fires
  // no notification at all, unchanged from before this issue.
  func notifyUnsupported(rec Record, reason string) {
      notifyFn := notifyFunc
      go func(repo string, pr int, reason string) {
          msg := fmt.Sprintf("Review skipped: %s #%d is an unsupported language (%s)", repo, pr, reason)
          if err := notifyFn("howmux", msg); err != nil {
              logging.Warn("failed to send unsupported-language notification", "repo", repo, "pr", pr, "error", err)
          }
      }(rec.Repo, rec.PR, reason)
  }
  ```
- This satisfies the issue's acceptance criterion verbatim: "User gets a
  notification (via `notifyFunc`) naming repo/PR + unsupported reason, on
  the unsupported path only; failure logged, never propagated." The
  `reason` string is exactly the stderr text `classifyUnsupportedLanguage`
  returned (section 3), so the notification body includes
  `pr_review.py`'s own explanation, e.g. `"Error: could not determine the
  language from the diff (no dominant supported file type). Re-run with an
  explicit language: astro, go, java, node, python."`.
- No changes are needed to `internal/notify/notify.go` itself — the
  existing `Notify(title, message string) error` signature is reused
  as-is; this is purely a second call site in `runner.go`, matching #117's
  established contract exactly.

### 7. Reviews tab — rendering `unsupported` distinctly

File: `internal/tui/reviews_tab.go`

- `styleStatus` (lines ~343–360 per the current file) is the single place
  that maps a `review.Status` value to a display color:
  ```go
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
  Today, any status not explicitly matched (including a hypothetical
  unknown value) falls through to `default: return text` — unstyled plain
  text, i.e. **visually identical to `StatusWatching`/`StatusReviewed`**
  under most terminal themes (no color at all vs. the neutral `Prompt`
  color may already look similar) — this is the gap the issue's "Reviews
  tab visibly distinguishes `unsupported` from `watching`" criterion closes.
- Exact change — add an explicit `case review.StatusUnsupported:` arm using
  the existing `styles.Error` style (already used elsewhere in this file,
  e.g. `renderError`, for error-adjacent states — `StatusUnsupported` is
  exactly that: a durable, user-actionable error condition, not a neutral
  "nothing to do" state like `StatusWatching`):
  ```go
  switch status {
  case review.StatusDone:
      return rt.styles.Success.Render(text)
  case review.StatusReviewing:
      return rt.styles.Warning.Render(text)
  case review.StatusUnsupported:
      return rt.styles.Error.Render(text)
  case review.StatusWatching, review.StatusReviewed:
      return rt.styles.Prompt.Render(text)
  default:
      return text
  }
  ```
- No change is needed to `buildTable`/`reviewsHeader`/column widths: the
  STATUS column already renders `string(rec.Status)` verbatim (line ~290,
  `statusStyle(i, rec.Status, fmt.Sprintf("%-*s", reviewsColStatus,
  string(rec.Status)))`), so the literal text `"unsupported"` (11 chars,
  fits within `reviewsColStatus = 12`) will display correctly with no
  truncation once `Status` is set to `StatusUnsupported` — the only gap was
  the missing color case, now closed above.
- `CopyableContent()` reuses the same `buildTable` helper with identity
  style functions (`plainStatus := func(_ int, _ review.Status, text string)
  string { return text }`), so the plain-text copy output already shows
  `"unsupported"` as literal text with no further change needed there — the
  distinction in that view is the word itself, which is sufficient (color
  is a `View()`-only concept, `CopyableContent()` is plain text by design).

### 8. Re-enrollment reset path — `unsupported` → `watching`

File: `internal/tui/commands.go`

- Current re-enrollment branch (the `found` case, lines ~1035–1053 per the
  read above):
  ```go
  var rec review.Record
  if found {
      m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("PR #%d already enrolled (status: %s)", prNum, existing.Status)))
      rec = existing

      // Fix #5: Re-review bypasses dedup - check if head SHA was already serviced
      prData, err := getPRFunc(fullRepo, prNum)
      if err != nil {
          m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to fetch PR metadata: %v", err)))
          return m, nil
      }
      headSHA := prData.HeadSHA()

      if rec.LastServicedRequest == headSHA {
          m = m.appendActivity(m.styles.Activity.Render(fmt.Sprintf("PR #%d already reviewed at %s - nothing new to review", prNum, headSHA[:8])))
          return m, nil
      }
      // Head SHA has advanced past last serviced one, proceed with review
  } else {
      // ... new-record creation with Status: review.StatusWatching ...
  }
  ```
  **This is the exact gap the issue's "Recovery: re-enrolling resets
  `unsupported` → `watching`" requirement targets**: today, `rec =
  existing` carries forward whatever `Status` the existing record has —
  including `StatusUnsupported` — and nothing in this branch ever resets
  it. Since `LastServicedRequest` was never set on the unsupported path
  (`RunReview` returns before reaching the `rec.LastServicedRequest =
  headSHA` assignment at line ~211, which only happens on the full success
  path), `rec.LastServicedRequest == headSHA` is false here (it's `""`),
  so execution falls through past the dedup check — meaning a re-enroll of
  an unsupported PR already reaches the "proceed with review" code below
  this block today. The missing piece is purely that `rec.Status` is never
  explicitly reset to `StatusWatching` before that re-review proceeds — it
  stays `StatusUnsupported` in memory and gets overwritten moments later
  only indirectly, when `RunReview` itself sets `rec.Status =
  StatusReviewing` (runner.go line 143) at the start of the new attempt.
  That incidental overwrite is enough to make the retry *work* mechanically,
  but it means there is a window (between re-enroll and the next
  `RunReview` call) where the on-disk record still reads
  `StatusUnsupported` even though the user just explicitly asked to retry
  it — and if that retry also fails for a non-language reason (e.g.
  transient network), `revertToWatching` reverts it to `StatusWatching`
  correctly, but if `RunReview` is never reached at all (e.g. the function
  returns early at the `getPRFunc` error check above), the record is left
  stuck on `StatusUnsupported` despite the user's explicit re-enroll
  intent. The fix must make the reset explicit and immediate, not rely on
  the incidental `RunReview`-internal overwrite.
- Exact change — immediately after `rec = existing` (before the
  `getPRFunc`/dedup logic), add:
  ```go
  var rec review.Record
  if found {
      m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("PR #%d already enrolled (status: %s)", prNum, existing.Status)))
      rec = existing

      // Re-enrolling is the documented recovery path for a PR stuck in
      // StatusUnsupported (issue #123): explicitly reset it to
      // StatusWatching here, immediately and durably, rather than relying
      // on RunReview's incidental StatusReviewing overwrite a few lines
      // later to mask the stale on-disk value. This guarantees the record
      // never reads "unsupported" again once the user has explicitly
      // asked to retry, even if the retry itself fails before reaching
      // RunReview (e.g. the getPRFunc call below).
      if rec.Status == review.StatusUnsupported {
          rec.Status = review.StatusWatching
          if err := store.Save(rec); err != nil {
              m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to reset PR #%d status: %v", prNum, err)))
              return m, nil
          }
      }

      // Fix #5: Re-review bypasses dedup - check if head SHA was already serviced
      prData, err := getPRFunc(fullRepo, prNum)
      // ... unchanged ...
  ```
- This also implicitly covers the watcher-poll recovery path: once
  `decideReviewAction`'s new Rule 1b (section 5) is skipping a
  `StatusUnsupported` record on every poll, the **only** way such a record
  changes state again is through this explicit manual re-enroll path (there
  is no other write path to a `StatusUnsupported` record once it's set,
  other than `Remove` via `ActionPrune`) — confirming the re-enroll command
  handler is the correct and only place this reset belongs.

## Solution Approach Summary

1. Add `StatusUnsupported` to the `Status` enum and `Validate()`'s allowed
   set (`types.go`) — a pure data-model addition, no behavioral change to
   any existing status.
2. In `RunReview` (`runner.go`), tee `runReviewToolFunc`'s stderr into a
   local `bytes.Buffer` (via `io.MultiWriter`) alongside the existing
   `tabWriter` stream, so the live-streaming UX is unchanged while stderr
   becomes available for post-hoc inspection after the subprocess call
   returns.
3. On a `runReviewToolFunc` failure, classify the captured stderr via a new
   pure helper (`classifyUnsupportedLanguage`) matching the two documented
   substrings; on a match, persist `StatusUnsupported` (not
   `revertToWatching`), fire a `notifyUnsupported` notification mirroring
   the #117 pattern, and return a sentinel-wrapped error. On no match
   (including the missing-diff-file case), fall through to the existing,
   completely unchanged `revertToWatching` + generic-error path.
4. `decideReviewAction` (`decision.go`) gains one new rule — `StatusUnsupported`
   → `ActionSkip` — inserted after the terminal/prune check and before the
   never-reviewed check, so a merged/closed unsupported PR still prunes,
   but an open unsupported PR stops being re-dispatched forever.
5. `ReviewsTab.styleStatus` (`reviews_tab.go`) gains an explicit
   `StatusUnsupported` → `styles.Error` color case, so the status column
   visibly distinguishes it from `watching`/`reviewed`'s neutral color and
   `reviewing`'s warning color.
6. The manual re-enroll handler (`commands.go`) explicitly resets
   `StatusUnsupported` → `StatusWatching` and persists it immediately upon
   re-enrollment, before any further review-dispatch logic runs, closing
   the one write path back out of the terminal-ish `StatusUnsupported`
   state.

## Concurrency Analysis

**No new cross-goroutine shared state is introduced by this design.**　

- The new `notifyUnsupported` call follows `notifyFunc`'s pre-existing,
  already-reviewed-and-landed (#117) pattern exactly: it reads the
  `notifyFunc` package-level seam on `RunReview`'s own calling goroutine
  into a local variable, then closes over that **local** (not the package
  global) inside a spawned `go func(...)`. This is the identical
  happens-before relationship #117 established and already relies on — no
  new lock is introduced because no new mutable shared field is read or
  written across the goroutine boundary; the local capture is by value.
- `classifyUnsupportedLanguage` is a pure function (string in, string+bool
  out) with no shared state at all.
- The `bytes.Buffer` used for the stderr tee is function-local to a single
  `RunReview` call (one goroutine, from `Watcher.dispatch`'s per-review
  goroutine or `commands.go`'s `reviewCmd` closure) — it is never shared
  across goroutines, read or written from only one goroutine for its
  entire lifetime, so no lock is needed.
- `decideReviewAction` remains a pure function (`github.PR`, `Record`,
  `string` in; `ReviewAction` out) — adding a new `if` branch that reads
  `rec.Status` (a value already passed in by value, not a shared pointer)
  introduces no new concurrency surface. `rec` here is the `Record` value
  the watcher's `pollOnce` already read via `w.store.List()` on its own
  goroutine; no other goroutine mutates that local value.
- `ReviewsTab.styleStatus` reads only its own `status` parameter (passed by
  value from `buildTable`'s loop, itself derived from a fresh
  `rt.store.List()` read inside `View()`/`CopyableContent()` on the render
  goroutine) — no new field, no new lock, consistent with every other
  status-coloring branch already in this function.
- `commands.go`'s re-enroll reset writes `rec.Status` and calls
  `store.Save(rec)` synchronously, on the same goroutine already handling
  the `review <PR_URL>` command (confirmed: this code runs before the
  `reviewCmd := func() tea.Msg { ... }` async closure is even constructed,
  i.e. it's on the command-handling path, not inside the already-async
  review-dispatch goroutine) — no new goroutine, no new shared field.

Because no lock-guarded field or new goroutine-shared mutable state is
introduced, no concurrent test (`-race` accessor exercise) is required for
this issue, per the architect convention's own detection rules (which key
on new cross-goroutine access to a `sync.Mutex`/`sync.RWMutex`-guarded
field — none is added here). This is stated explicitly rather than silently
omitted.

## Relevant Files

### Modified files

| File | Change |
|------|--------|
| `internal/review/types.go` | Add `StatusUnsupported Status = "unsupported"` to the `Status` const block; add `&& r.Status != StatusUnsupported` to `Validate()`'s allowed-status check |
| `internal/review/types_test.go` | Add test case(s): `Validate()` accepts `StatusUnsupported`; existing "invalid status" test continues to reject an arbitrary unknown string |
| `internal/review/runner.go` | Add `"bytes"` and `"errors"` imports; add `unsupportedLanguageMarkers`, `errUnsupportedLanguage`, `classifyUnsupportedLanguage`, `notifyUnsupported`; change `RunReview`'s call to `runReviewToolFunc` to pass a teed `io.MultiWriter(tabWriter, &stderrBuf)`; add the new classify-and-branch logic in the review-tool-failure error path (before the fallback to `revertToWatching`) |
| `internal/review/runner_test.go` | Add test(s): (a) unsupported-language stderr (containing either documented marker) sets `StatusUnsupported` and persists it, does NOT call `revertToWatching`'s effective behavior (record is NOT left/reset to `StatusWatching`), returns an error satisfying `errors.Is(err, errUnsupportedLanguage)`; (b) a missing-diff-file-style stderr message (distinct wording, no marker substring) still reverts to `StatusWatching` exactly as before (regression guard per acceptance criterion 3); (c) `notifyFunc` is called exactly once on the unsupported path with a message containing the repo, PR number, and the captured reason text, using the same channel/WaitGroup-based synchronization pattern the existing #117 notify tests use (no `time.Sleep`); (d) the live tab-writer stream still receives the full stderr output even when classification matches (tee doesn't suppress the stream) |
| `internal/review/decision.go` | Insert the new `StatusUnsupported` → `ActionSkip` rule between the existing terminal/prune check and the never-reviewed check; update the function's doc comment to list the new rule |
| `internal/review/decision_test.go` | Add test(s): `StatusUnsupported` + open PR → `ActionSkip`; `StatusUnsupported` + merged/closed PR → `ActionPrune` (prune wins); confirm `StatusUnsupported` + `LastReviewedSHA == ""` does NOT return `ActionReview` (the regression this issue fixes) |
| `internal/tui/reviews_tab.go` | Add `case review.StatusUnsupported: return rt.styles.Error.Render(text)` to `styleStatus`'s switch |
| `internal/tui/reviews_tab_test.go` | Add test(s): a record with `Status: review.StatusUnsupported` renders with the Error style (or, if the test suite asserts on rendered string content rather than style objects, assert the row's status text is `"unsupported"` and distinguishable from a `StatusWatching` row's styling via whatever assertion mechanism existing `styleStatus`/color tests in this file already use — follow the established pattern, do not invent a new one) |
| `internal/tui/commands.go` | In the re-enroll (`found == true`) branch, immediately after `rec = existing`, add the explicit `if rec.Status == review.StatusUnsupported { rec.Status = review.StatusWatching; store.Save(rec) }` reset block (with its own error-reporting `appendActivity` on a `Save` failure, matching the file's existing error-handling style) |
| `internal/tui/commands_review_test.go` | Add test(s): re-enrolling a PR whose stored record has `Status: review.StatusUnsupported` persists `Status: review.StatusWatching` before any further dispatch logic runs; re-enrolling a PR with any other status is unaffected (no spurious extra `Save` call changes behavior for the non-unsupported case — or, if a `Save` call is observably idempotent/harmless, confirm it does not break the existing dedup-check tests in this file) |

No changes are needed to `internal/review/watcher.go`, `internal/review/checkout.go`,
`internal/review/spool.go`, `internal/review/bodywriter.go`,
`internal/review/decisionwriter.go`, `internal/review/noteswriter.go`,
`internal/review/paths.go`, `internal/review/store.go`, or
`internal/review/decideactions.go` (the post/revise/rereview/discard logic
in that file operates on already-spooled reviews and is entirely
unreachable for a record that never produced a spool file, which is exactly
the unsupported-language case) — confirmed by reading each file; none of
them read or write `Status` in a way this issue's scope touches.

## Team Orchestration

This is a small, single-package-plus-one-TUI-file change with no component
boundaries requiring cross-team coordination. All tasks below operate on
`internal/review` (the core state machine) and `internal/tui` (the two
display/command touch points) within one PR. Tasks 1–2 are independent and
parallelizable; Tasks 3–5 each depend on Task 1 (the new `Status` value
must exist before anything can set, check, or render it).

## Task Breakdown

### Task 1: Add `StatusUnsupported` to the type system

**Files touched:** `internal/review/types.go`, `internal/review/types_test.go`

**Acceptance Criteria:**
- `StatusUnsupported Status = "unsupported"` added to the `Status` const
  block in `types.go`, with a doc comment matching the existing style
  (one-line, explains when the record is in this state and that it is not
  re-dispatched until re-enrolled).
- `Record.Validate()`'s allowed-status check includes `StatusUnsupported`
  as a valid value — a `Record` with `Status: StatusUnsupported` and all
  other required fields set must pass `Validate()` with no error.
- A `Record` with an arbitrary unrecognized status string (e.g.
  `"bogus"`) must still fail `Validate()` with an `"invalid status"` error
  — the existing negative test must continue to pass unmodified.
- `go build ./...` passes.
- `go test ./internal/review/... -run TestRecord_Validate -v` passes,
  including the new `StatusUnsupported`-accepted case.

**Dependencies:** None.

---

### Task 2: Reviews tab status coloring for `unsupported`

**Files touched:** `internal/tui/reviews_tab.go`, `internal/tui/reviews_tab_test.go`

**Acceptance Criteria:**
- `styleStatus`'s switch statement has an explicit
  `case review.StatusUnsupported:` arm returning
  `rt.styles.Error.Render(text)`, placed before the
  `case review.StatusWatching, review.StatusReviewed:` arm (ordering among
  switch cases is not semantically significant in Go, but match the file's
  existing visual grouping by placing it adjacent to the other
  non-neutral/non-success states for readability).
- A row with `Status: review.StatusUnsupported` renders visibly differently
  from a row with `Status: review.StatusWatching` under the existing test
  harness's assertion style (follow whatever pattern
  `reviews_tab_test.go` already uses to assert on `styleStatus`'s output —
  do not introduce a new assertion mechanism).
- `CopyableContent()` (plain-text path) is confirmed, by test or inspection,
  to already render the literal string `"unsupported"` with no truncation
  (column width is 12 chars, the word is 11) — no code change needed there,
  but add a regression test asserting this if the existing test file has an
  analogous assertion for other statuses.
- `go build ./...` passes.
- `go test ./internal/tui/... -run TestReviewsTab -v` passes.

**Dependencies:** Task 1 (needs `review.StatusUnsupported` to exist).

---

### Task 3: Stderr tee + classification + `StatusUnsupported` transition in `RunReview`

**Files touched:** `internal/review/runner.go`, `internal/review/runner_test.go`

**Acceptance Criteria:**
- `runner.go` imports `"bytes"` and `"errors"`.
- New `unsupportedLanguageMarkers` var (`[]string`) contains exactly the
  two documented substrings: `"could not determine the language from the
  diff"` and `"unsupported language '"`.
- New `errUnsupportedLanguage` sentinel error exists
  (`errors.New("unsupported language")`), and `RunReview`'s returned error
  on this path wraps it such that `errors.Is(err, errUnsupportedLanguage)`
  is `true`.
- New pure function `classifyUnsupportedLanguage(stderrText string) (reason
  string, matched bool)` returns `(trimmed stderr, true)` when the input
  contains either marker substring, and `("", false)` otherwise (including
  on empty input).
- `RunReview`'s call to `runReviewToolFunc` passes an `io.MultiWriter`
  combining the existing `tabWriter` parameter and a new function-local
  `bytes.Buffer` — the tab writer continues to receive every byte of
  stderr exactly as before (verified by a test asserting the tab writer's
  captured content is unchanged/complete even when classification
  triggers).
- On a `runReviewToolFunc` error, `RunReview` calls
  `classifyUnsupportedLanguage` on the buffered stderr content:
  - **Match:** sets `rec.Status = StatusUnsupported`, calls
    `storeImpl.Save(rec)` (logging, not returning, on a `Save` failure —
    matching `revertToWatching`'s own established tolerance for a failed
    revert-write), calls the new `notifyUnsupported(rec, reason)` helper,
    and returns an error wrapping `errUnsupportedLanguage` with the reason
    text included. **`revertToWatching` is NOT called on this branch.**
  - **No match:** falls through to the existing, byte-for-byte unchanged
    `revertToWatching(storeImpl, rec)` + `fmt.Errorf("pr_review.py
    invocation failed: %w", err)` path.
- New `notifyUnsupported(rec Record, reason string)` helper mirrors the
  existing success-path notification's goroutine/local-seam-copy pattern
  exactly (read `notifyFunc` into a local before spawning the goroutine);
  message format: `"Review skipped: %s #%d is an unsupported language
  (%s)"` with repo, PR, and reason substituted. A `notifyFn` error is
  logged via `logging.Warn` and never propagated.
- New tests in `runner_test.go`:
  1. `TestRunReview_UnsupportedLanguage_SetsStatus`: fake
     `runReviewToolFunc` to write
     `"Error: could not determine the language from the diff (no dominant supported file type). Re-run with an explicit language: astro, go, java, node, python."`
     to its `stderrWriter` parameter and return a non-nil error; assert the
     persisted record's `Status == StatusUnsupported`; assert the returned
     error satisfies `errors.Is(err, errUnsupportedLanguage)`; assert no
     spool file / worktree side effects occur (none are created by this
     function regardless, confirmed in Codebase Findings §1 — this test
     documents that invariant explicitly).
  2. `TestRunReview_UnsupportedLanguage_ExplicitMarker`: same as above but
     using the second documented marker, e.g. stderr `"Error: unsupported
     language 'cpp'. Supported: astro, go, java, node, python."`; same
     assertions.
  3. `TestRunReview_MissingDiffFile_NotMisclassified`: fake
     `runReviewToolFunc` to write a distinct, marker-free stderr message
     (e.g. `"Error: diff file not found: /tmp/pr-1-abc.diff"`) and return a
     non-nil error; assert the persisted record's `Status == StatusWatching`
     (the existing `revertToWatching` behavior, unchanged); assert the
     returned error does NOT satisfy `errors.Is(err, errUnsupportedLanguage)`;
     assert the returned error message still contains `"pr_review.py
     invocation failed"` (the pre-existing generic-failure message,
     confirming zero behavior change on this path). This is the explicit
     regression guard for acceptance criterion 3.
  4. `TestRunReview_UnsupportedLanguage_FiresNotification`: fake both
     `runReviewToolFunc` (unsupported-marker stderr) and `notifyFunc`
     (recording calls via a channel, synchronized with a bounded
     `select`/timeout, not `time.Sleep` — mirroring the existing #117
     notify-test synchronization pattern already present in this file for
     the success-path notification test); assert `notifyFunc` is called
     exactly once with a message containing the repo, the PR number, and
     the reason text; assert a `notifyFunc`-returned error does not cause
     `RunReview` to return a different/additional error (still returns the
     `errUnsupportedLanguage`-wrapped error, nothing about the
     notification failure leaks into it).
  5. `TestRunReview_UnsupportedLanguage_StreamsToTabWriter`: using a
     `bytes.Buffer` as the `tabWriter` argument to `RunReview` (not
     `io.Discard`), assert that after an unsupported-language failure, the
     tab writer's buffer contains the full stderr text `runReviewToolFunc`
     wrote — confirming the tee did not suppress or truncate the live
     stream.
- `go build ./...` passes.
- `go test ./internal/review/... -run TestRunReview -v` passes, including
  all five new tests above and every pre-existing `TestRunReview_*` test
  unmodified (in particular `TestRunReview_ReviewToolFailure`, which must
  continue to pass exactly as today since its stderr fixture contains
  neither marker substring).

**Dependencies:** Task 1 (needs `StatusUnsupported` to exist to set it).

---

### Task 4: `decideReviewAction` — `StatusUnsupported` → `ActionSkip` rule

**Files touched:** `internal/review/decision.go`, `internal/review/decision_test.go`

**Acceptance Criteria:**
- A new rule is inserted into `decideReviewAction` strictly after the
  `pr.IsTerminal() → ActionPrune` check and strictly before the
  `rec.LastReviewedSHA == "" → ActionReview` check:
  ```go
  if rec.Status == StatusUnsupported {
      return ActionSkip
  }
  ```
- The function's doc comment (the `// Rules:` block above its signature) is
  updated to list this new rule in the correct position, e.g.:
  ```
  // - PR merged or closed → ActionPrune
  // - Previously classified unsupported-language → ActionSkip (unless terminal, which still prunes above)
  // - No stored record / never reviewed → ActionReview
  // ...
  ```
- New tests in `decision_test.go`:
  1. `TestDecideReviewAction_StatusUnsupported_OpenPR_Skips`: an open
     (non-terminal) `github.PR` + a `Record{Status: StatusUnsupported,
     LastReviewedSHA: ""}` → `decideReviewAction` returns `ActionSkip` (not
     `ActionReview` — this is the exact regression this issue fixes;
     assert explicitly that the old Rule 2 does not fire).
  2. `TestDecideReviewAction_StatusUnsupported_TerminalPR_StillPrunes`: a
     merged-or-closed `github.PR` + a `Record{Status: StatusUnsupported}` →
     `decideReviewAction` returns `ActionPrune` (prune wins over the new
     skip rule — confirms correct rule ordering per the issue's explicit
     constraint).
  3. A table-driven extension of the existing `TestDecideReviewAction` test
     (if that test is table-driven — confirmed structure in
     `decision_test.go` before implementation) adding both cases above as
     additional table rows, to keep all rule-ordering cases visible
     side-by-side for future maintainers.
- `go build ./...` passes.
- `go test ./internal/review/... -run TestDecideReviewAction -v` passes,
  including every pre-existing case unmodified.

**Dependencies:** Task 1 (needs `StatusUnsupported` to exist to compare
against).

---

### Task 5: Re-enrollment reset path (`unsupported` → `watching`)

**Files touched:** `internal/tui/commands.go`, `internal/tui/commands_review_test.go`

**Acceptance Criteria:**
- In the `found == true` branch of the `review <PR_URL>` command handler
  (the branch starting with `m = m.appendActivity(... "already enrolled"
  ...); rec = existing`), immediately after `rec = existing`, add:
  ```go
  if rec.Status == review.StatusUnsupported {
      rec.Status = review.StatusWatching
      if err := store.Save(rec); err != nil {
          m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to reset PR #%d status: %v", prNum, err)))
          return m, nil
      }
  }
  ```
  placed strictly before the subsequent `getPRFunc`/`LastServicedRequest`
  dedup-check logic in the same branch, so the reset is durable on disk
  before any further decision is made about whether to proceed with a new
  review.
- New tests in `commands_review_test.go`:
  1. `TestHandleReview_ReenrollUnsupported_ResetsToWatching`: seed the fake
     store with a record for the target repo/PR with `Status:
     review.StatusUnsupported`; invoke the `review <PR_URL>` command
     handler; assert the record now persisted in the store has `Status:
     review.StatusWatching` (read it back via the store, not just inspect
     the in-memory `rec` the handler held).
  2. `TestHandleReview_ReenrollNonUnsupported_StatusUnaffected`: seed the
     fake store with a record with `Status: review.StatusReviewed` (or
     whatever non-unsupported status the file's existing dedup tests
     already use); invoke the command handler; assert the existing
     dedup-check test behavior is unchanged (no spurious `Save` call
     side-effects alter the pre-existing assertions in this file — run the
     full existing test suite in this file to confirm no regression).
- `go build ./...` passes.
- `go test ./internal/tui/... -run TestHandleReview -v` passes, including
  every pre-existing case unmodified.

**Dependencies:** Task 1 (needs `review.StatusUnsupported` to exist to
compare against and reset from).

## Validation Commands

```bash
# Build the whole module
go build ./...

# Vet
go vet ./...

# Targeted tests, per touched package
go test ./internal/review/... -run TestRecord_Validate -v
go test ./internal/review/... -run TestRunReview -v
go test ./internal/review/... -run TestDecideReviewAction -v
go test ./internal/tui/... -run TestReviewsTab -v
go test ./internal/tui/... -run TestHandleReview -v

# Full package-scoped suite named in the issue's own acceptance criteria
go test ./internal/review/...

# Full repo build + test, to catch any unexpected cross-package fallout
go build ./...
go test ./...
```

## Out of Scope (per the issue's own "Out of scope" section, reaffirmed)

- Adding a C/C++ (or any new) reviewer lens to `pr_review.py` — this issue
  only makes `howmux` *report* the unsupported case cleanly; the set of
  languages `pr_review.py` supports is unchanged.
- Changing `pr_review.py`'s exit codes or stderr message wording — that
  tool lives in the `ai-resources` repo, out of this repo's control and
  explicitly excluded by the issue.
- Persisting the unsupported "reason" string durably on the `Record` (e.g.
  a new `UnsupportedReason` field surviving a `howmux` restart) — the
  issue's acceptance criteria only require the notification and the Reviews
  tab color/word to surface it at the time of failure; no acceptance
  criterion requires the reason to be recoverable after that point.
- Non-macOS notification delivery — unchanged from `internal/notify`'s
  existing, already-scoped-elsewhere macOS-only behavior.
