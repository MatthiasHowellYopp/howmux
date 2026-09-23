# Design Spec: Notify User When a PR Review Completes

Closes #117

## Assumptions

None — the worktree path supplied by the orchestrator
(`/Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-117-1106`)
exists and is fully populated (a real git worktree with `.git`, `go.mod`,
`internal/`, etc.), and the issue body's file references
(`internal/review/watcher.go`, `internal/review/runner.go`,
`internal/tui/reviews_tab.go`) were confirmed present and read directly. No
placeholder/fixture data was encountered and no path fallback was needed.

## Problem Statement (recap)

A PR review artifact lands in `~/PR-Review/pending/` via two trigger paths —
the watcher's recurring poll loop and a manual `review <PR_URL>` command —
and nothing tells the user it's ready. Both paths must fire a macOS system
notification on success only, and the Reviews tab must show a live count of
reviews awaiting a decision.

## Codebase Findings

- **Both trigger paths funnel through one function.** Confirmed by reading
  the code, not just the issue's claim:
  - `internal/review/watcher.go`'s `dispatch()` goroutine calls
    `dispatchReviewFunc` (package var, production impl wraps `RunReview`).
  - `internal/tui/commands.go`'s `handleReview()` (the `review <PR_URL>`
    command handler) calls `runReviewFunc` (package var, production impl
    wraps `RunReview`) inside its async `reviewCmd` closure.
  - Both `dispatchReviewFunc` and `runReviewFunc` are thin package-level
    var wrappers around the exact same `review.RunReview` in
    `internal/review/runner.go`. **`RunReview` is the single correct hook
    point** — one hook covers both trigger paths, matching the issue's own
    hint.
- **`RunReview`'s success path** (`internal/review/runner.go`): fetches the
  diff, invokes `pr_review.py`, captures and validates the spool path from
  stdout, stamps reviewed metadata into the spool file
  (`WriteReviewedMetadata`), then updates and saves the `Record` (status
  `StatusReviewed`, `SpoolPath`, timestamps) via `storeImpl.Save(rec)`. It
  returns `nil` only after all of this succeeds. Every early return above
  that point is a failure (`return fmt.Errorf(...)`) — so "the last line
  before `return nil`" is exactly and only the success case, which lines
  up with the constraint "does NOT fire ... only on success."
- **Existing error-handling convention**: the package already uses
  `internal/logging` (`logging.Info`, `logging.Debug`, `logging.Error`) for
  best-effort, non-fatal side effects — e.g. `Store.Save`'s directory-fsync
  is "best-effort ... don't fail the Save" and watcher.go logs
  `dispatch`/removal failures without propagating them. **Notification
  delivery must follow this exact pattern**: log-and-continue, never
  propagate an error out of `RunReview`.
- **No existing OS-notification or subprocess-toast code** anywhere in the
  repo (`grep -rn "osascript|terminal-notifier|beeep"` returns nothing outside
  ACP's unrelated "SessionUpdate ... notifications" comments). This is a
  new capability, not a refactor of an existing one.
- **`Record` fields available at the hook point**: `rec.Repo` (`"owner/name"`)
  and `rec.PR` (int) are both already in scope inside `RunReview` — exactly
  what the notification body needs (`"Review ready: owner/repo #123"`), no
  new plumbing required to obtain them.
- **Reviews tab title today** is a hardcoded literal: `Title() string {
  return "Reviews" }` in `internal/tui/reviews_tab.go`. `Title()` has no
  access to `err`/store-read failures today (it can't fail) and is called
  by `TabManager.RenderTabHeaders` on every render.
- **Pending-review count is a re-scan, not a new counter.** The tab already
  has zero caching by design: `View()`, `CopyableContent()`, and (per its own
  doc comments) every render calls `rt.store.List()` fresh from disk — "no
  cache, no staleness window ... fine for a low-volume, local-FS tab." The
  vocabulary for "awaiting decision" already exists and is unit-tested:
  `review.SpoolInfo.DecisionState`, produced by the pure function
  `ClassifySpoolState(found, inDoneDir, decision)` in `internal/review/spool.go`:
  - `found=true, inDoneDir=false, decision==""` → `"pending"` — this is
    precisely "awaiting decision" (in `~/PR-Review/pending/`, not yet
    archived to `done/`, no decision set yet).
  - Any other combination (`"no spool"`, `"decided: X"`, `"posted"`,
    `"discarded"`, `"done"`, `"done: X"`) is not "awaiting decision."
  - `ReviewsTab.resolveSpoolInfo(sorted)` already computes a `[]SpoolInfo`
    (via `spoolInfoForFunc`/`review.ReadSpoolInfo`) for every record on every
    render — the same slice `renderTable` and `CopyableContent` already
    build. Counting `DecisionState == "pending"` in that existing slice
    requires no new I/O, no new store method, and no new field on `Record`.
  - This directly satisfies the acceptance criteria for free: the count
    "increments as new reviews land in `pending/`" (a fresh `RunReview`
    success sets `SpoolPath` and the spool file has no `decision:` yet →
    counted) and "clears/decrements once the user views/selects... in the
    Reviews tab" — see below for exactly what "clears" means here.
- **What "clears... once the user views/selects" means precisely.** The
  acceptance criterion says the badge should clear/decrement "once the user
  views/selects the corresponding review(s) in the Reviews tab" — but
  selecting a row (`moveCursor`/`selectedKey`) does not itself resolve a
  review; only setting a decision (`p`/`r`/`R`/`d` → `decideSelectedCmd` →
  `DecisionWriter.SetDecision`, or opening it and posting) changes its
  `DecisionState` away from `"pending"`. Re-reading this literally ("merely
  highlighting a row clears the badge") would make the badge lie the instant
  the cursor moves off a still-undecided review. The correct, acceptance-
  criteria-honoring interpretation — consistent with how "awaiting decision"
  is used everywhere else in this codebase (`DecisionState`, not row
  selection) — is: **the badge counts reviews whose `DecisionState ==
  "pending"`, and it decrements automatically the moment a review's decision
  is set (via the same p/r/R/d flow already wired to `decideSelectedCmd` and
  `handleDecide`) or the review is otherwise archived to `done/`.** Because
  the count is a live re-scan on every render, this "clears" behavior is
  already correct with zero extra code once the count is computed from
  `DecisionState`: the *next* render after a decision is set/finalized will
  naturally show the review as no longer pending. This is called out
  explicitly here so the builder does not add row-viewed/dismissal tracking
  state that isn't needed and isn't what the store models.
- **Where the badge renders**: `TabManager.RenderTabHeaders` calls
  `tab.Title()` per tab (in `internal/tui/tab_manager.go`) and truncates any
  title over 15 chars to `title[:12] + "..."`. A count suffix like `" (2)"`
  must be appended inside `ReviewsTab.Title()` itself (the one place that
  knows the count), not computed in `TabManager`, matching the existing
  ownership boundary (`TabManager` never reaches into a tab's domain data;
  it only calls the `Tab` interface). `"Reviews (2)"` is 11 chars — safely
  under the 15-char truncation threshold for any realistic pending count
  (double digits: `"Reviews (12)"` = 12 chars, still fine; the existing
  truncation is an unrelated pre-existing behavior, not something this
  issue needs to change).
- **Go module has no notification/toast dependency.** `go.mod` (Go 1.25.0)
  has no `beeep`, no wrapper around `osascript`/`terminal-notifier`. Given
  the constraints — macOS only, informational only, no click action, must
  not block or slow the review path, best-effort/log-and-continue on
  failure — the idiomatic choice for a Go CLI needing a single "fire and
  forget" macOS banner with zero interactivity is **`osascript -e 'display
  notification "<body>" with title "<title>"'`** via `os/exec`, run
  asynchronously (its own goroutine, detached from `RunReview`'s critical
  path) with a bounded timeout. This needs zero new dependencies (`osascript`
  ships with every macOS install) and trivially satisfies "no click action"
  since `display notification` has no click-through/action-button support at
  all (unlike `terminal-notifier`, which supports `-execute`/`-open` and
  would be pulling in a third-party binary dependency the constraints don't
  need). No new Go dependency is added to `go.mod`.

## Solution Approach

1. **New package `internal/notify`** encapsulates the macOS notification
   mechanism behind a small, injectable-for-tests function var, following
   the exact pattern already used throughout this codebase for OS/subprocess
   seams (`fetchDiffFunc`, `runReviewToolFunc` in `runner.go`;
   `ensureCheckoutFunc`, `getPRFunc`, `runReviewFunc` in `commands.go`).
   - `notify.Notify(title, message string) error` shells out to
     `osascript -e display notification ...`, escaping the title/message
     against AppleScript string-literal injection (both values are
     user/repo-controlled — `rec.Repo` echoes a GitHub owner/repo string,
     which is a trust boundary: a malicious/crafted repo name must not be
     able to break out of the AppleScript string or inject additional
     `osascript` commands).
   - `notify.Notify` returns an error (so it's independently unit-testable
     via the injectable `execCommand` var) but its *caller* in `runner.go`
     only logs the error — never propagates it, never blocks the return of
     `RunReview`.
   - macOS-only guard: check `runtime.GOOS == "darwin"` inside `Notify` (or
     at the call site) and no-op elsewhere, matching the constraint "macOS
     is the only target platform ... no need to support other OSes."
2. **Hook point: `RunReview` in `internal/review/runner.go`**, immediately
   after `storeImpl.Save(rec)` succeeds and right before the existing
   `logging.Info("PR review completed", ...)` call, fire the notification in
   its own goroutine so a slow/hung `osascript` process cannot add latency to
   `RunReview`'s return (satisfying "must not block or slow down the review
   dispatch path"). Content is `fmt.Sprintf("Review ready: %s #%d", rec.Repo,
   rec.PR)` for the message body, title `"howmux"` (a fixed, brand-identifying
   title — the issue only constrains the *body* content to repo+PR, not the
   title).
3. **Reviews tab badge: `ReviewsTab.Title()`** re-scans `rt.store.List()` and
   counts `DecisionState == "pending"` (via the same `resolveSpoolInfo`
   helper `View()`/`CopyableContent()` already call), appending `" (N)"` when
   N > 0 and nothing when N == 0 (so an empty/steady-state tab still just
   reads `"Reviews"`, unchanged from today). A `store.List()` error inside
   `Title()` degrades to the bare `"Reviews"` title (never propagates,
   mirroring `View()`'s existing `renderError` graceful-degradation pattern)
   — a transient I/O error must not crash tab-header rendering.
4. **No new persisted state, no new background goroutine/watch, no new
   shared counter.** The count is derived, read-only, computed fresh on
   every call, exactly matching this tab's pre-existing architecture and its
   own documented cost tradeoff. This keeps the change minimal and avoids
   introducing a second source of truth that could drift from the spool
   files on disk.

### Why not a filesystem watch (fsnotify) or a shared atomic counter?

- A cross-goroutine shared counter would require a new lock-guarded field
  updated from `RunReview` (a goroutine outside the TUI render loop) and read
  from `Title()` (the render goroutine) — a real concurrency boundary (see
  Concurrency Analysis below) for a value that's trivially and cheaply
  re-derivable from disk on every render, exactly like every other value this
  tab already shows. Introducing one would violate the existing "no cache"
  design for no benefit and add a lock for a value that's already correct
  without one.
- A filesystem watch (`fsnotify` on `~/PR-Review/pending/`) would add a new
  dependency and a new background goroutine + lifecycle (start/stop, wired
  into the same `Watcher.Stop()`/model shutdown path) for a tab that already
  reads fresh on every keypress/tick/resize with no observed staleness
  problem at today's PR volumes. This is unjustified complexity for the
  problem as scoped.

## Concurrency Analysis

**No new cross-goroutine shared state is introduced by this design.**

- The notification fire in `RunReview` is a fire-and-forget goroutine that
  reads only its own function-local copy of `title`/`message` strings
  (captured by value into the goroutine closure) and writes nothing back
  into any struct shared with the render loop or another goroutine. There is
  no field to lock.
- The pending-count badge reads only `rt.store.List()` (already a read-only,
  fresh-per-call filesystem read with no in-memory shared state — the same
  call `View()` and `CopyableContent()` already make from the render
  goroutine today) and the pure function `ClassifySpoolState`. No mutex is
  introduced or required because no mutable shared field is introduced.
- `ReviewsTab` itself has no `sync.Mutex`/`sync.RWMutex` today and this
  design adds none — `Title()` remains a pure, allocation-only computation
  over data already fetched fresh inside the same call.

Because no lock-guarded field or new goroutine-shared mutable state is added,
no concurrent test (`-race` accessor exercise) is required for this issue —
the "Detection Rules" in the architect's concurrency-analysis convention
(field with a mutex read/written across goroutines) simply do not apply
here. This is stated explicitly, rather than silently omitted, per the
convention's intent.

## Relevant Files

### New files

| File | Purpose |
|------|---------|
| `internal/notify/notify.go` | `Notify(title, message string) error` — macOS `osascript` "display notification" wrapper, injectable exec seam, AppleScript-string escaping, `runtime.GOOS` guard |
| `internal/notify/notify_test.go` | Unit tests: argument construction, escaping of quotes/backslashes in title/message, non-darwin no-op, injectable exec-failure propagation |

### Modified files

| File | Change |
|------|--------|
| `internal/review/runner.go` | After `storeImpl.Save(rec)` succeeds (success path only), spawn a goroutine calling `notify.Notify("howmux", fmt.Sprintf("Review ready: %s #%d", rec.Repo, rec.PR))`; log (`logging.Warn` or `logging.Error`) if it returns an error, never propagate |
| `internal/review/runner_test.go` | Add test(s) asserting: (a) notification is triggered on the success path (inject a fake `notify.Notify` via a package var seam, mirroring `fetchDiffFunc`/`runReviewToolFunc`), (b) notification is NOT triggered when `RunReview` returns an error at any earlier step, (c) a `Notify` error does not cause `RunReview` to return an error |
| `internal/tui/reviews_tab.go` | `Title()` computes pending count from `rt.store.List()` + `resolveSpoolInfo` + `ClassifySpoolState`-driven `DecisionState == "pending"`, appends `" (N)"` when N > 0 |
| `internal/tui/reviews_tab_test.go` | Add test(s): `Title()` returns `"Reviews"` with zero pending, `"Reviews (N)"` with N pending records, unaffected by non-pending records (`done`, `decided: post`, etc.), degrades to `"Reviews"` on a `store.List()` error |

No changes are needed to `internal/review/watcher.go`, `internal/tui/commands.go`,
or any spool/decision-writing code — the single hook in `RunReview` already
covers both trigger paths, and the badge is computed from already-existing
`DecisionState` data with no new store method.

## Task Breakdown

Tasks 1 and 2 have no dependency on each other and can be built/reviewed in
parallel; Task 3 depends on Task 1 (it wires `notify.Notify` into `RunReview`).

### Task 1: Implement `internal/notify` package (macOS notification)

**Acceptance Criteria:**
- New file `internal/notify/notify.go` with:
  - `func Notify(title, message string) error` — the public entry point.
  - An injectable exec seam (package-level var, e.g. `var execCommand =
    exec.Command`, mirroring `runReviewToolFunc`'s pattern of a var holding
    the subprocess-invoking closure) so tests can substitute a fake without
    shelling out for real.
  - Runs `osascript -e <script>` where `<script>` is `display notification
    "<escaped message>" with title "<escaped title>"`.
  - Escaping function that neutralizes AppleScript string-literal
    injection: at minimum, escape `"` → `\"` and `\` → `\\` in both title and
    message before interpolating into the `-e` script string. Add a doc
    comment explaining why this is required (repo names / PR-derived text
    are not a fully trusted input — a crafted or unusual repo name is
    attacker-adjacent input flowing into a shell-invoked string).
  - `runtime.GOOS != "darwin"` → return `nil` immediately (a documented,
    intentional no-op — not an error — consistent with "macOS is the only
    target platform for now").
  - Non-blocking to its *caller*'s intent: `Notify` itself may run
    synchronously (it's a fast, one-shot subprocess call) — the
    "don't block RunReview" requirement is satisfied by the *caller*
    invoking it inside a `go func() { ... }()`, not by `Notify` internally
    spawning a goroutine. Document this division of responsibility in
    `Notify`'s doc comment so Task 3 wires it correctly.
  - Never panics; a failed `exec.Command` run returns a wrapped error, does
    not crash.
- New file `internal/notify/notify_test.go` covering:
  - The constructed `osascript` argv/script contains the expected escaped
    title and message for at least one case with embedded `"` and `\` in
    each of title and message.
  - On a non-darwin `runtime.GOOS` (skip this case if the test can't
    control `GOOS`; alternatively refactor the darwin-check into a small
    injectable `goos string` var defaulting to `runtime.GOOS`, mirroring
    other seams in this codebase, so the test can force both branches
    without `//go:build` tricks).
  - Injectable exec seam returning an error propagates as a wrapped, non-nil
    error from `Notify` (and does not panic).
  - A successful (faked) exec call returns `nil`.
- `go build ./...` and `go vet ./...` pass with the new package.
- `go test ./internal/notify/...` passes.

**Dependencies:** None.

---

### Task 2: Reviews tab badge — pending count in `Title()`

**Acceptance Criteria:**
- `internal/tui/reviews_tab.go`: `Title()` is changed from the current
  hardcoded `return "Reviews"` to:
  1. Call `rt.store.List()`.
  2. On error, return `"Reviews"` unchanged (graceful degradation, no
     panic, no error propagation — `Title()`'s signature is `() string`,
     it has no way to surface an error and must not need one).
  3. On success, resolve `SpoolInfo` per record (reuse
     `rt.resolveSpoolInfo(sorted)` — no need to re-sort by repo/PR for a
     pure count, but reusing the existing helper avoids a second,
     divergent way of calling `spoolInfoForFunc`/`review.ReadSpoolInfo`;
     sorting order is irrelevant to a count).
  4. Count records where `info.DecisionState == "pending"`.
  5. If count == 0, return `"Reviews"`.
  6. If count > 0, return `fmt.Sprintf("Reviews (%d)", count)`.
- Add a short doc comment on `Title()` explaining: the count reflects
  reviews with `DecisionState == "pending"` (landed in `pending/`, not yet
  archived, no decision set) — i.e., "awaiting decision" — and that it is
  recomputed fresh on every call (same no-cache tradeoff as `View()`), so it
  automatically decrements the next time the tab renders after a decision is
  set or a review is archived, with no separate "mark as viewed" step.
- `internal/tui/reviews_tab_test.go`: add test(s) verifying:
  - Zero records → `Title() == "Reviews"`.
  - One or more records, none with `DecisionState == "pending"` (e.g. all
    `StatusDone` with archived/`done`-classified spool state) → `Title() ==
    "Reviews"`.
  - N records with `DecisionState == "pending"` → `Title() == "Reviews
    (N)"`. Use the existing `fakeReviewStore` plus a way to make
    `spoolInfoForFunc` (or an equivalent seam) return `DecisionState:
    "pending"` for the fixture records — follow whatever seam
    `reviews_tab_test.go` already uses to control `SpoolInfo` resolution in
    existing tests (check for an existing pattern before introducing a new
    one; `spoolInfoForFunc` in `commands.go` is the production var this
    likely needs to be overridden through, consistent with how the rest of
    the tab's tests already fake spool resolution).
  - A mix of pending and non-pending (`done`, `posted`, `discarded`,
    `decided: revise`) records → count reflects only the pending ones.
  - `store.List()` returning an error → `Title() == "Reviews"` (no panic,
    no error string leaking into the tab header).
- Confirm `RenderTabHeaders` in `tab_manager.go` needs **no changes**: it
  already calls `tab.Title()` generically and truncates titles over 15
  chars — verify (by running the existing `tab_manager_test.go` suite, and
  optionally adding one assertion) that `"Reviews (N)"` for realistic N
  (1–2 digits) renders correctly without unwanted truncation regressions.

**Dependencies:** None (independent of Task 1; can be built in parallel).

---

### Task 3: Wire notification into `RunReview`'s success path

**Acceptance Criteria:**
- `internal/review/runner.go` imports `internal/notify`.
- Add an injectable package-level seam, e.g.:
  ```go
  var notifyFunc = notify.Notify
  ```
  mirroring the existing `fetchDiffFunc`/`runReviewToolFunc`/`timeNow` seam
  pattern in the same file, so tests can substitute a fake without touching
  `internal/notify` at all.
- In `RunReview`, immediately after the `storeImpl.Save(rec)` success check
  (i.e., after the `if err := storeImpl.Save(rec); err != nil { return ... }`
  block, before the final `logging.Info("PR review completed", ...)` and
  `return nil`), add:
  ```go
  go func(repo string, pr int) {
      msg := fmt.Sprintf("Review ready: %s #%d", repo, pr)
      if err := notifyFunc("howmux", msg); err != nil {
          logging.Warn("failed to send review-complete notification", "repo", repo, "pr", pr, "error", err)
      }
  }(rec.Repo, rec.PR)
  ```
  — capturing `rec.Repo`/`rec.PR` by value into the goroutine's parameters
  (not by closing over `rec` itself) so a future mutation of `rec` in this
  function cannot race with the goroutine reading it (defensive, since `rec`
  is a local value type here today and this makes that safety explicit and
  future-proof rather than relying on today's absence of post-notification
  mutation).
- The notification call must be **unreachable from any error-return path**
  in `RunReview` — verify by inspection that every `return fmt.Errorf(...)`
  in the function occurs strictly before this new block, and the new block
  sits strictly before the final `return nil`.
- `internal/review/runner_test.go`: add test(s) using the `notifyFunc` seam:
  - Fake `notifyFunc` to record calls (repo/pr/message) in a slice; run
    `RunReview` through its full success path (reusing existing fakes for
    `fetchDiffFunc`/`runReviewToolFunc`/store); assert `notifyFunc` was
    called exactly once with the expected `"Review ready: <repo> #<pr>"`
    message. Because the call is in a background goroutine, the test must
    synchronize (e.g. a buffered channel/`sync.WaitGroup` signaled inside the
    fake `notifyFunc`, with a bounded `select`/timeout) rather than sleeping,
    to avoid flakiness.
  - Force `RunReview` to fail at each of its existing failure points (diff
    fetch failure, tool invocation failure, empty stdout, invalid spool
    path, `WriteReviewedMetadata` failure, `storeImpl.Save` failure — reuse
    the existing failure-path tests already in `runner_test.go` where
    present) and assert `notifyFunc` is **never** called in any of these
    cases.
  - Fake `notifyFunc` to return an error and assert `RunReview` still
    returns `nil` (the overall call succeeds) — i.e., a notification failure
    never surfaces as a `RunReview` error, per the "best-effort" constraint.
- `go build ./...`, `go vet ./...`, and `go test ./internal/review/...` pass.

**Dependencies:** Task 1 (needs `internal/notify.Notify` to exist and its
signature to be final before wiring the seam).

---

### Task 4: Integration verification and README consistency

**Acceptance Criteria:**
- Confirm (by code inspection, and optionally a short manual/local run) that
  both trigger paths described in the issue actually produce a notification
  once Tasks 1–3 land:
  - `Watcher.dispatch()` → `dispatchReviewFunc` → `RunReview` (the poll-loop
    path in `internal/review/watcher.go` — unchanged by this issue, but
    verify the call graph still reaches the new code after the other tasks'
    edits).
  - `handleReview([prURL])` → `runReviewFunc` → `RunReview` (the manual
    `review <PR_URL>` path in `internal/tui/commands.go` — likewise
    unchanged, verify it still reaches `RunReview`).
- No changes are needed to `README.md`'s existing "PR Review Workflow"
  section for this issue — the notification and badge are additive side
  effects on the already-documented flows, not a change to the documented
  commands or their semantics. Confirm no doc claims contradict the new
  behavior (e.g. nothing currently claims "no notifications are sent," so no
  correction is required); if a future documenter pass wants to mention the
  new notification/badge behavior, that is optional polish, not required by
  this issue's acceptance criteria.
- Full test suite passes: `go test ./...`.
- `go vet ./...` and existing lint/format tooling (per `Taskfile.yml`'s
  `lint` task) pass with no new findings introduced by this change.

**Dependencies:** Tasks 1, 2, 3 (verification-only task; runs last).

## Validation Commands

```bash
# Build
go build ./...

# Vet
go vet ./...

# Unit tests, targeted
go test ./internal/notify/... -v
go test ./internal/review/... -run TestRunReview -v
go test ./internal/tui/... -run TestReviewsTab -v

# Full suite
go test ./...

# Lint (per Taskfile.yml)
task lint
```

## Out of Scope (explicitly, per the issue's own Constraints)

- Non-macOS notification support (Linux/Windows) — issue explicitly scopes
  to macOS only.
- Click-to-focus or any interactive notification behavior — issue explicitly
  excludes this ("no click action... informational only").
- Verdict or finding-count content in the notification body — issue
  explicitly restricts content to repo + PR number.
- Any change to watcher dispatch semantics, spool file format, or decision
  writing — issue explicitly requires "existing review dispatch behavior...
  is unchanged."
