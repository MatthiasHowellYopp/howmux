# Design Specification: Surface Spool Visibility in Reviews Tab

**Issue**: #82
**Closes**: #82

## Context

The Reviews tab (`internal/tui/reviews_tab.go`) currently renders REPO / PR / STATUS / LAST REVIEWED columns sourced entirely from `review.Record` (`internal/review/types.go`), which is persisted at `.howmux/reviews/<owner>-<repo>-<pr>/record.json` via `review.Store` (`internal/review/store.go`).

`Record` already has a `SpoolPath` field (added in #76's `RunReview`), populated with the absolute path to the spool file `pr_review.py` writes under `~/PR-Review/pending/<file>.md`. That spool file is later drained by the external `pr_review_finalize.py` / `finalize-reviews.sh` pipeline (see `ai-resources/workflows/spool.py`), which:

- reads/writes a flat, non-nested YAML-like front-matter block bounded by `---` lines
- moves the file from `~/PR-Review/pending/` to `~/PR-Review/done/` once a decision (`post` or `discard`) is finalized
- recognizes exactly four `decision` values: `post`, `revise`, `rereview`, `discard`

None of this spool state is visible in the Reviews tab today. A user sees `STATUS: reviewed` and has no way to tell whether the corresponding spool file is still awaiting a human decision, has been decided but not yet drained, or has already been posted/archived. This spec adds read-only visibility into that state, reusing the existing `buildTable`/`padToHeight` machinery.

## Spool File Front-Matter Contract (authoritative reference)

From `ai-resources/workflows/spool.py` (the shared contract with `pr_review.write_spool`):

```
---
repo: owner/repo
pr: 17
verdict: APPROVE|COMMENT|REQUEST_CHANGES
decision:            # human sets: post|revise|rereview|discard
decision_notes:      # free text for revise/rereview
diff_file: /abs/path/to.diff   # or empty
generated: 2026-08-21T11:00Z
---

<review body ...>
```

Key properties howmux's reader must respect (read-only, this repo does not own this format):

- Front-matter is a **flat set of `key: value` scalars**, no nesting, bounded by a `---` line, the fields, then another `---` line.
- A key with no value after the colon (e.g. `decision:` followed by nothing before the newline) means that field is blank/unset.
- `pending/` holds spool files awaiting a decision; `done/` holds archived (drained) ones. Files are moved, not copied — a given spool file exists in exactly one of the two directories at a time.
- Howmux must **never write** to these files or directories. This issue is display-only.

## Solution Approach

### High-Level Strategy

1. **New read-only spool reader package function(s) in `internal/review`**: Add a small, dependency-free front-matter parser (`ParseSpoolFrontMatter`) plus a lookup helper (`ReadSpoolInfo`) that, given a `SpoolPath`, locates the file (it may be in `pending/` or `done/` regardless of what `SpoolPath` says, since the external finalize pipeline moves it), parses the front-matter, and returns a small `SpoolInfo` struct. This lives in `internal/review` (not `internal/tui`) because it is spool-domain logic, parallel to `Store`, and is unit-testable without any TUI dependencies. It has zero coupling to `pr_review.py`/Python — it only reads whatever `SpoolPath` points at (or its moved-to-`done/` counterpart) and parses the same flat front-matter contract described above.

2. **Extend `ReviewsTab` to enrich records with spool info at render time**: `View()` and `CopyableContent()` already call `rt.store.List()` fresh on every render (no caching — see the existing cost-note comment in `reviews_tab.go`). Add a parallel per-record lookup: for each `review.Record`, call `review.ReadSpoolInfo(rec.SpoolPath)` to get `SpoolInfo{Verdict, DecisionState, Found bool}`. This keeps the "no cache, read fresh every frame" tradeoff already documented and accepted for `List()` — do not introduce caching in this issue.

3. **Decision-state classification is a pure function**: `review.ClassifySpoolState(spoolPath string, found bool, dir string, decision string) string` (exact signature below) encodes the acceptance-criterion-3 mapping. Keep it pure and package-level so it is trivially unit-testable in `internal/review` without touching disk.

4. **Extend the table layout**: Add three columns — `SPOOL PATH`, `VERDICT`, `DECISION STATE` — to `reviewsHeader()`, `buildTable()`, and the column-width constants. `buildTable`'s signature must grow to accept the per-record spool info, since it currently only receives `[]review.Record`. Keep the identity-style / real-style dual-use pattern (`identityStyle` for `CopyableContent()`, real styles for `View()`) exactly as-is — this issue must not fork that pattern.

5. **No mutation, ever**: Every new function in this spec that touches `~/PR-Review/**` performs `os.ReadFile`/`os.Stat` only. No `os.WriteFile`, `os.Rename`, or `os.Remove` calls are introduced anywhere in this change. This satisfies AC7 / the "read-only" constraint directly and should be verified via the grep check in Validation Commands.

### Why This Approach

- **Reuses `buildTable` + `padToHeight`** exactly as AC6 requires — no parallel table-rendering code path is introduced.
- **Keeps spool parsing out of `internal/tui`**: parsing a foreign file format (front-matter shared with a Python tool) is domain logic that belongs next to `Store`/`Record` in `internal/review`, matching the existing separation (`internal/tui` renders, `internal/review` persists/interprets review-domain state).
- **No new caching or background polling**: matches the tab's existing "read fresh every render" design note in `reviews_tab.go`'s `View()` doc comment. Adding a stat+read of one small file per tracked PR, per render, is the same order of I/O already accepted for `record.json`.
- **Independent of decision on where the file currently lives**: because `pr_review_finalize.py` *moves* (not copies) the file from `pending/` to `done/` when drained, `Record.SpoolPath` (captured once, at review time, always under `pending/` per `validateSpoolPath`'s contract) can go stale relative to the file's current location. The reader must check both `pending/` and `done/` — see `ReadSpoolInfo` below — rather than trusting `SpoolPath` blindly once a file has been drained.

## Relevant Files

### Files to Create

| File | Purpose |
|------|---------|
| `internal/review/spool.go` | `SpoolInfo` struct, `ParseSpoolFrontMatter`, `ReadSpoolInfo`, `ClassifySpoolState` — read-only spool front-matter parsing and decision-state classification |
| `internal/review/spool_test.go` | Unit tests for the above: front-matter parsing (present/blank/malformed), pending-vs-done resolution, missing-file handling, decision-state classification for every AC3 case |

### Files to Modify

| File | Changes |
|------|---------|
| `internal/tui/reviews_tab.go` | Add spool columns to `reviewsHeader()`, `buildTable()`, and column-width constants; add a per-record spool lookup step in `View()`/`renderTable()`/`CopyableContent()`; add a "no spool" fallback string constant |
| `internal/tui/reviews_tab_test.go` | Add test cases covering: spool columns rendered for a record with a resolvable spool file; "pending" / "decided: X" / "posted" / "no spool" each rendered correctly; existing tests continue to pass unmodified in behavior (repo/PR/status/last-reviewed columns unchanged) |

### Files Referenced (Not Modified)

| File | Purpose |
|------|---------|
| `internal/review/types.go` | `Record.SpoolPath` field (already exists, added in #76) — read-only reference, no changes needed |
| `internal/review/store.go` | `Store.List()` / `StoreInterface` — unchanged, still the sole source of `Record`s |
| `internal/tui/commands.go` | `userHomeDirFunc` — existing injectable `os.UserHomeDir()` seam in the same package; reuse this seam (do not add a second one) to resolve `~/PR-Review` |
| `ai-resources/workflows/spool.py` | External Python module defining the front-matter contract this issue reads (not part of this repo; reference only) |

## Data Structures

### `internal/review/spool.go`

```go
package review

// SpoolInfo is the read-only, display-oriented view of a spool file's
// state, derived by reading front-matter from disk. It is never persisted —
// it is recomputed on every ReadSpoolInfo call, mirroring the "no cache"
// design of Store.List().
type SpoolInfo struct {
	Found         bool   // true if the spool file was located (in pending/ or done/)
	InDoneDir     bool   // true if the file was found in done/ rather than pending/
	Verdict       string // raw "verdict" front-matter value, "" if absent/not found
	Decision      string // raw "decision" front-matter value, "" if blank/absent
	DecisionState string // classified state string, see ClassifySpoolState; always populated even when !Found
}
```

Design notes:
- `SpoolInfo` intentionally omits `decision_notes`, `diff_file`, `generated`, and the review body — AC1/AC2 only ask for `verdict` and `decision`-derived state to be surfaced as columns; nothing else is displayed, so nothing else needs to be parsed into the struct's fields (front-matter parsing can still expose them generically — see `ParseSpoolFrontMatter`'s return type below — but `SpoolInfo` stays minimal per YAGNI).
- No `Repo`/`PR` fields on `SpoolInfo`: those are cross-checked against `Record.Repo`/`Record.PR` only if a future issue needs consistency validation; out of scope here (AC2 only asks to *extract* front-matter fields for display, not to reconcile them against the record).

### Front-matter parser return type

```go
// ParseSpoolFrontMatter parses the flat `key: value` front-matter block at
// the start of a spool file (bounded by "---" lines, per the contract in
// ai-resources/workflows/spool.py). It does not validate keys against an
// allow-list — unknown keys are simply available in the returned map,
// mirroring spool.py's permissiveness. Returns an empty map (not an error)
// if no front-matter block is present, since a malformed/legacy file should
// degrade to "no known fields" rather than blocking the whole tab render.
func ParseSpoolFrontMatter(data []byte) map[string]string
```

Parsing rules (mirrors `spool.py`'s `_FM_RE` / `_parse_frontmatter`):
1. The file must start with a line that is exactly `---` (optional leading/trailing whitespace).
2. Everything up to the next line that is exactly `---` is the front-matter block.
3. Each non-blank line in that block is split on the first `:` into `key` and `value`; both are trimmed of surrounding whitespace.
4. A key with an empty value after trimming (e.g. `decision:`) maps to `""` in the returned map — this is the "blank/missing" case AC3 requires distinguishing from a populated value.
5. If the file does not start with `---`, return an empty map — do not error. This function must never panic or return an error; malformed input degrades to "no fields found", which the caller (`ReadSpoolInfo`) turns into `Found: false`-equivalent field values.

## Function Signatures

All new functions live in `internal/review/spool.go` unless noted.

```go
// ParseSpoolFrontMatter parses flat key: value front-matter bounded by "---"
// lines. See parsing rules above. Never errors; returns an empty map for
// malformed/absent front-matter.
func ParseSpoolFrontMatter(data []byte) map[string]string

// ReadSpoolInfo resolves and reads a spool file for display purposes only.
// spoolPath is the value from Record.SpoolPath (may be "" if the PR has
// never been reviewed, or may point at a pending/ path that has since been
// moved to done/ by the external finalize pipeline).
//
// homeDir is passed in (rather than calling os.UserHomeDir() internally) so
// this function stays a pure-ish, easily-testable unit: callers resolve the
// home directory once via the existing userHomeDirFunc seam in
// internal/tui/commands.go and pass it down.
//
// Resolution order:
//  1. If spoolPath == "", return SpoolInfo{DecisionState: "no spool"} immediately (AC5).
//  2. If the file at spoolPath exists, read and parse it from there (pending/ case).
//  3. Otherwise, derive the done/ counterpart by swapping the "pending"
//     path segment for "done" (same base filename) and try that path. This
//     handles the case where pr_review_finalize.py has already archived the
//     file after this issue's Record.SpoolPath was captured (#76's RunReview
//     only ever writes a pending/ path — see runner.go's validateSpoolPath).
//  4. If neither location exists, return SpoolInfo{Found: false, DecisionState: "no spool"} (AC5).
//  5. On a read/parse success, populate Verdict/Decision from the parsed
//     front-matter and call ClassifySpoolState to fill DecisionState.
func ReadSpoolInfo(spoolPath string, homeDir string) SpoolInfo

// ClassifySpoolState implements the AC3 decision-state mapping as a pure
// function so it is unit-testable independent of any filesystem access.
//   - found=false                                  -> "no spool"
//   - found=true, inDoneDir=true                    -> "posted"
//   - found=true, inDoneDir=false, decision==""      -> "pending"
//   - found=true, inDoneDir=false, decision==<one of
//     post|revise|rereview|discard>                  -> "decided: <decision>"
//   - found=true, inDoneDir=false, decision==<anything
//     else non-empty>                                 -> "decided: <decision>" (pass through verbatim;
//                                                        display-only, do not validate against the enum —
//                                                        an unrecognized value is still informative to show
//                                                        and must not be hidden or turned into an error)
func ClassifySpoolState(found bool, inDoneDir bool, decision string) string
```

```go
// internal/tui/reviews_tab.go additions

// spoolInfoForFunc wraps review.ReadSpoolInfo for testability, following the
// same "var fn = ..." injectable-seam pattern already used for
// userHomeDirFunc/runReviewFunc in commands.go. Tests can replace this to
// avoid touching the real filesystem.
var spoolInfoForFunc = func(spoolPath string, homeDir string) review.SpoolInfo {
	return review.ReadSpoolInfo(spoolPath, homeDir)
}

// buildTable signature changes: it must now receive spool info per record.
// spoolInfo is indexed by the same order as the (already sorted) records
// slice passed in, i.e. spoolInfo[i] corresponds to sorted[i] — the caller
// (renderTable/CopyableContent) is responsible for sorting records via
// sortedRecords BEFORE computing spoolInfo, so index alignment holds.
func buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(review.Status, string) string) string

// reviewsHeader grows three columns: SPOOL PATH, VERDICT, DECISION STATE.
func reviewsHeader() string

// New column width constants, alongside the existing reviewsColRepo/PR/Status:
const (
	reviewsColSpoolPath     = 40 // truncated/elided if longer, see truncate() in commands.go
	reviewsColVerdict       = 16
	reviewsColDecisionState = 20
)

// resolveSpoolInfo is a small ReviewsTab helper that sorts records once and
// computes the aligned []review.SpoolInfo slice, shared by renderTable() and
// CopyableContent() so they cannot compute spool info differently.
func (rt *ReviewsTab) resolveSpoolInfo(sorted []review.Record) []review.SpoolInfo
```

## Decision State Logic — Mapping to Acceptance Criteria

| AC3 requirement | Implementation |
|---|---|
| "pending" if decision blank/missing and file is in pending/ | `ClassifySpoolState(found=true, inDoneDir=false, decision="")` → `"pending"` |
| "decided: `<value>`" if decision contains post\|revise\|rereview\|discard | `ClassifySpoolState(found=true, inDoneDir=false, decision="post")` → `"decided: post"` (and so on for the other three values) |
| "posted" if file is in done/ (archived after posting) | `ClassifySpoolState(found=true, inDoneDir=true, decision=<anything>)` → `"posted"`, regardless of what `decision` says — being in `done/` is definitionally "posted/archived" per the spool contract, so `inDoneDir` takes priority over inspecting `decision` |
| AC5: missing spool file → "no spool" | `spoolPath == ""` OR neither `pending/<name>` nor `done/<name>` exists on disk → `ClassifySpoolState(found=false, ...)` → `"no spool"` |

Precedence order in `ClassifySpoolState`: check `found` first, then `inDoneDir`, then `decision`. This order is what makes "posted" correctly override a stale/unexpected `decision` value once a file has been archived.

## Spool File Front-Matter Parsing Approach

1. Read the whole spool file into memory with `os.ReadFile` — these files are small (a diff-review body, at most a few hundred KB); no need for streaming.
2. Call `ParseSpoolFrontMatter(data)`, which:
   - Converts `data` to a string, splits on `\n`.
   - Requires the first non-... actually: requires line 0 to be exactly `---` (after `strings.TrimSpace`); if not, return `map[string]string{}` immediately.
   - Scans subsequent lines until a line that is exactly `---` is found (the closing fence); collects lines in between.
   - For each collected line, split on the first `:` via `strings.Cut` (or `strings.SplitN(line, ":", 2)`); trim both sides with `strings.TrimSpace`. Skip lines with no `:`.
   - Returns the resulting `map[string]string`.
3. `ReadSpoolInfo` reads `fields["verdict"]` and `fields["decision"]` (both default to `""` via Go's zero-value map lookup if absent) and passes `fields["decision"]` into `ClassifySpoolState`.
4. This parser is intentionally independent of YAML libraries (no new dependency) — matching `spool.py`'s own stdlib-only, regex-based approach and the project's existing minimal-dependency Go style (check `go.mod` — no YAML library is currently imported).

## Handling Missing / Moved Spool Files (AC5)

Three distinct "not found" scenarios, all collapsing to `"no spool"` / `Found: false`:

1. `Record.SpoolPath == ""` — PR has never completed a review (still `StatusWatching` or a review failed before reaching the record-save step in `RunReview`). Short-circuit without any filesystem access.
2. `Record.SpoolPath` is a `pending/<file>.md` path, but the file exists at neither `pending/<file>.md` nor `done/<file>.md` — e.g. discarded and cleaned up manually, corrupted state, or a spool root relocated via `PR_REVIEW_DIR` in a different shell session than howmux's. Treat identically to case 1.
3. `os.Stat`/`os.ReadFile` returns a permission or transient I/O error on an existing path — treat as not-found for display purposes (do not propagate the error up and break the whole tab render); this matches `Store.List()`'s existing "skip invalid/unparseable records" philosophy of degrading gracefully rather than erroring the whole view.

## Table Layout Changes

Current header: `REPO | PR | STATUS | LAST REVIEWED`
New header: `REPO | PR | STATUS | LAST REVIEWED | SPOOL PATH | VERDICT | DECISION STATE`

- `SPOOL PATH` column displays `rec.SpoolPath` truncated via the existing `truncate(s string, max int)` helper already defined in `internal/tui/commands.go` (reuse it — do not write a second truncation helper). Empty `SpoolPath` displays as `emptyTimestampPlaceholder` (`"—"`) for visual consistency with the existing LAST REVIEWED empty-state convention.
- `VERDICT` column displays `spoolInfo[i].Verdict`, or `"—"` when empty.
- `DECISION STATE` column displays `spoolInfo[i].DecisionState` (never empty — `ClassifySpoolState` always returns a non-empty string, including `"no spool"`).
- No new coloring/styling is required by the acceptance criteria; keep `statusStyle` applied only to the existing STATUS column, matching current behavior (AC4 — "preserve existing functionality" — do not reinterpret this as "also colorize the new columns").

## Team Orchestration

### Parallel Work (Task 1 and Task 2)

- **Task 1** (`internal/review/spool.go` + its test file) has no dependency on the TUI layer and can be built and fully unit-tested in isolation.
- **Task 2** is the `reviews_tab.go` table-layout changes that depend on Task 1's types/functions existing (`review.SpoolInfo`, `review.ReadSpoolInfo`). Because Task 2 needs Task 1's exported API, it is sequential, not parallel — see Task Breakdown below for the exact dependency.

Given the dependency, there is limited true parallelism in this issue (it is a small, single-feature change). The task breakdown below still separates concerns into two builder-sized units so a second builder could stub Task 2 against Task 1's signatures once Task 1's API shape is fixed, but Task 1 must land (or at least have its final signatures fixed) before Task 2's implementation is finished.

## Step-by-Step Task Breakdown

### Task 1: Implement Spool Front-Matter Reader and Decision-State Classifier

**What**: Create `internal/review/spool.go` with `SpoolInfo`, `ParseSpoolFrontMatter`, `ReadSpoolInfo`, and `ClassifySpoolState`, plus full unit test coverage in `internal/review/spool_test.go`.

**Files**:
- `internal/review/spool.go` (new)
- `internal/review/spool_test.go` (new)

**Acceptance Criteria**:
1. `SpoolInfo` struct defined exactly as specified in Data Structures above.
2. `ParseSpoolFrontMatter(data []byte) map[string]string` implemented per the parsing rules above:
   - Well-formed front-matter (all seven known keys from `spool.py`, `decision`/`decision_notes` blank) parses into a map with 5 non-empty values and 2 empty-string values (`decision`, `decision_notes`).
   - A file with no `---` fence at all returns an empty map, no panic, no error.
   - A file with an opening `---` but no closing `---` (malformed/truncated) returns an empty map, no panic, no error.
   - A line inside the fence with no `:` is skipped (does not add a spurious key or crash).
   - A value containing a colon (e.g. a URL in a hypothetical field) is preserved correctly because only the *first* `:` splits key from value.
3. `ClassifySpoolState(found, inDoneDir bool, decision string) string` implemented per the precedence table above, covering all cases:
   - `found=false` → `"no spool"` (regardless of the other two args)
   - `found=true, inDoneDir=true` → `"posted"` (regardless of `decision`, including a `decision=""` or garbage value)
   - `found=true, inDoneDir=false, decision=""` → `"pending"`
   - `found=true, inDoneDir=false, decision="post"` → `"decided: post"`
   - `found=true, inDoneDir=false, decision="revise"` → `"decided: revise"`
   - `found=true, inDoneDir=false, decision="rereview"` → `"decided: rereview"`
   - `found=true, inDoneDir=false, decision="discard"` → `"decided: discard"`
   - `found=true, inDoneDir=false, decision="something-unexpected"` → `"decided: something-unexpected"` (pass-through, not an error)
4. `ReadSpoolInfo(spoolPath, homeDir string) SpoolInfo` implemented per the resolution order above:
   - `spoolPath == ""` → `SpoolInfo{Found: false, DecisionState: "no spool"}`, zero filesystem calls (assert via a test that uses a homeDir that would panic/error if touched, e.g. an empty string, to prove no I/O happens)
   - `spoolPath` pointing at a real file under a temp dir's `pending/` subdirectory → reads and parses it, `Found=true`, `InDoneDir=false`
   - `spoolPath` pointing at a `pending/<file>.md` path that does NOT exist, but a same-named file DOES exist under the sibling `done/` directory → resolves to the `done/` file, `Found=true`, `InDoneDir=true` (this is the "already archived by finalize" case)
   - `spoolPath` pointing at a path where neither `pending/` nor `done/` has the file → `Found=false`, `DecisionState: "no spool"`
   - A spool file that is present but fails to parse cleanly (e.g. empty file, zero-length) still returns a valid `SpoolInfo` with `Found=true` and whatever `ClassifySpoolState` computes from empty `decision`/`verdict` strings — must not error or panic.
5. All new code has zero write operations (`os.WriteFile`, `os.Rename`, `os.Remove`, `os.MkdirAll`) anywhere in `spool.go` — this file only ever calls `os.ReadFile`/`os.Stat`/`os.Open` (read variants).
   - **Verification**: `grep -nE "os\.(WriteFile|Rename|Remove|RemoveAll|MkdirAll|Create)\(" internal/review/spool.go` returns zero matches.
6. Package doc comment on `spool.go` references the shared contract source (`ai-resources/workflows/spool.py`) so future readers know this is a read-only mirror of an external format, not the format's owner.
7. `go test ./internal/review/...` passes; `go test -race ./internal/review/...` passes (no shared mutable state is introduced, so this should be immediate, but confirm).

**Dependencies**: None.

---

### Task 2: Extend Reviews Tab Table With Spool Columns

**What**: Wire `review.ReadSpoolInfo` into `ReviewsTab`, extend `buildTable`/`reviewsHeader`/column-width constants with the three new columns, and add the `spoolInfoForFunc` injectable seam, per Data Structures / Function Signatures above.

**Files**:
- `internal/tui/reviews_tab.go`
- `internal/tui/reviews_tab_test.go`

**Acceptance Criteria**:
1. `spoolInfoForFunc` var added to `reviews_tab.go` (or `commands.go`, matching where `userHomeDirFunc` already lives — builder's choice, but must be consistent with the existing seam-placement convention in this package) wrapping `review.ReadSpoolInfo`.
2. `reviewsColSpoolPath`, `reviewsColVerdict`, `reviewsColDecisionState` width constants added alongside the existing `reviewsColRepo`/`reviewsColPR`/`reviewsColStatus`.
3. `reviewsHeader()` updated to emit all seven column headers in order: `REPO PR STATUS LAST REVIEWED SPOOL PATH VERDICT DECISION STATE` (exact spacing per the existing `fmt.Sprintf("%-*s ...")` pattern — match the existing style of left-padding every column except the last).
4. `buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(review.Status, string) string) string` signature updated; row-building loop appends the three new columns per record, reading `spoolInfo[i]` for the row at the same index as `sorted[i]`.
5. `resolveSpoolInfo(sorted []review.Record) []review.SpoolInfo` helper added to `ReviewsTab`; calls `userHomeDirFunc()` once per invocation (not once per record) and `spoolInfoForFunc(rec.SpoolPath, homeDir)` per record; if `userHomeDirFunc()` errors, pass `""` as `homeDir` to every `spoolInfoForFunc` call for that render (do not fail the whole tab render over a home-dir lookup failure — degrade to whatever `ReadSpoolInfo` does with an empty homeDir, which per Task 1 AC4's third bullet still resolves absolute `spoolPath` values correctly since `spoolPath` from `Record.SpoolPath` is always already absolute per `validateSpoolPath`'s contract in `runner.go`).
6. `renderTable(records []review.Record)` updated: computes `sorted := sortedRecords(records)` (already exists via `buildTable`'s internal call — note: `buildTable` currently calls `sortedRecords` itself; either (a) hoist the sort out of `buildTable` so `renderTable`/`CopyableContent` can sort once, compute `resolveSpoolInfo` against the sorted slice, and pass both into `buildTable`, or (b) have `renderTable`/`CopyableContent` call `sortedRecords` themselves before calling `resolveSpoolInfo`, and keep `buildTable` also sorting internally (redundant but harmless since `sortedRecords` is deterministic). **Choose (a)** — hoist sorting out of `buildTable` into the two call sites — to guarantee `spoolInfo` index-alignment can never drift from a second, independent sort call. Update `buildTable`'s doc comment accordingly (it currently says "sorted := sortedRecords(records)" as its first line — that responsibility moves to the caller).
7. `CopyableContent()` updated identically to `renderTable()` — same sort-once, `resolveSpoolInfo`-once, `buildTable` call pattern — so the plain-text export cannot drift from the styled view (preserving the existing anti-drift guarantee documented on `CopyableContent()`).
8. `emptyTimestampPlaceholder` (`"—"`) reused for empty `SpoolPath` and empty `Verdict` display — no new placeholder constant for these two.
9. New constant, e.g. `spoolStateNoSpool = "no spool"`, is NOT redefined here — it is Task 1's `ClassifySpoolState`'s return value; `reviews_tab.go` must not hardcode `"no spool"`/`"posted"`/`"pending"` strings itself, only ever displaying whatever `SpoolInfo.DecisionState` already contains. This keeps the state-string source of truth in one package.
10. `SPOOL PATH` column value is `truncate(rec.SpoolPath, reviewsColSpoolPath)` when non-empty, else `emptyTimestampPlaceholder`; reuses `truncate()` from `commands.go` (same package, no new helper).
11. Existing `TestReviewsTabConstruction`, `TestReviewsTabEmptyState`, `TestReviewsTabErrorState` continue to pass with no behavioral changes (empty/error states are unaffected by this change — they short-circuit before any table is built).
12. `TestReviewsTabPopulatedRendering` (and any other existing populated-rendering tests) updated only to the extent required by the new columns appearing in the output — assertions on REPO/PR/STATUS/LAST REVIEWED column content must remain unchanged in substance (AC4).
13. New test(s) added covering each AC3 case end-to-end through `ReviewsTab.View()`/`CopyableContent()`:
    - a record whose `SpoolPath` resolves to a `pending/` file with blank `decision` renders `"pending"` in the DECISION STATE column
    - a record whose `SpoolPath` resolves to a `pending/` file with `decision: post` renders `"decided: post"`
    - a record whose `SpoolPath` resolves to a `done/` file renders `"posted"`
    - a record with `SpoolPath == ""` renders `"no spool"`
    - a record whose `SpoolPath` points at a nonexistent file (neither pending/ nor done/ has it) renders `"no spool"`
    - these tests use the `spoolInfoForFunc` seam (inject a fake, do not touch a real `~/PR-Review` directory) — matching the existing `fakeReviewStore` pattern of avoiding real disk I/O in `internal/tui` tests
14. `go test ./internal/tui/...` passes; `go test -race ./internal/tui/...` passes.
15. `gofmt -l internal/review/spool.go internal/tui/reviews_tab.go` (or `task fmt:check`) reports no issues.

**Dependencies**: Task 1 (uses `review.SpoolInfo`, `review.ReadSpoolInfo`).

---

### Task 3: Full Gate Verification

**What**: Run the full project gate to confirm the change is complete, formatted, linted, and does not break template sync or the build.

**Files**: None (verification only).

**Acceptance Criteria**:
1. `go build ./...` succeeds.
2. `go test ./...` passes with zero failures.
3. `go test -race ./internal/review/... ./internal/tui/...` passes with zero data races.
4. `task fmt:check` passes.
5. `task lint` passes.
6. `task sync:check` passes (this issue does not touch any template-synced files — `.kiro/agents/*`, `.howmux/scripts/*`, themes — so this should be a no-op pass-through, but must still be run to confirm nothing regressed).
7. `task build` succeeds.
8. **Read-only guarantee check**: `grep -nE "os\.(WriteFile|Rename|Remove|RemoveAll|MkdirAll|Create)\(" internal/review/spool.go` returns zero matches (repeats Task 1 AC5's check as a final gate).
9. **No hardcoded state strings outside the classifier**: `grep -n '"no spool"\|"posted"\|"pending"\|"decided:' internal/tui/reviews_tab.go` returns zero matches (confirms Task 2 AC9 — all state strings originate solely from `review.ClassifySpoolState`).

**Dependencies**: Task 1, Task 2.

## Validation Commands

```bash
# Unit tests, targeted
go test ./internal/review/... -run TestParseSpoolFrontMatter
go test ./internal/review/... -run TestClassifySpoolState
go test ./internal/review/... -run TestReadSpoolInfo
go test ./internal/tui/... -run TestReviewsTab

# Full package tests + race detector
go test ./internal/review/...
go test ./internal/tui/...
go test -race ./internal/review/... ./internal/tui/...

# Project gate
task fmt:check
task lint
task sync:check
task test
task build

# Read-only guarantee (must return no output)
grep -nE "os\.(WriteFile|Rename|Remove|RemoveAll|MkdirAll|Create)\(" internal/review/spool.go

# State-string source-of-truth guarantee (must return no output)
grep -n '"no spool"\|"posted"\|"pending"\|"decided:' internal/tui/reviews_tab.go
```

## Concurrency Analysis

This change does **not** cross any concurrency boundary as defined in the architect conventions:

- `ReviewsTab.View()`/`CopyableContent()` already run exclusively on the Bubble Tea render/event-loop goroutine (per the existing doc comment on `View()` — "on the event-loop goroutine"). The new `resolveSpoolInfo`/`spoolInfoForFunc`/`review.ReadSpoolInfo` calls are made from that same call chain, synchronously, with no new goroutines spawned.
- No shared mutable state is introduced. `review.ReadSpoolInfo` takes its inputs as parameters and returns a fresh `SpoolInfo` value; there is no package-level mutable state in `spool.go` (no `sync.Mutex`-guarded fields, no caching).
- The watcher goroutine (`internal/review/watcher.go`) and the review-execution goroutine (`RunReview` in `runner.go`) are unaffected — this issue reads `Record.SpoolPath` (already safely obtained via `store.List()`, which the render goroutine already calls) and reads the referenced file on disk; it does not read or write any watcher-owned or runner-owned in-memory state.
- **No new locking is required.** No accessor method needs lock-guarding, and no concurrent test is required by this issue's acceptance criteria.

## Mechanical Change Surface Enumeration

This is a small, additive feature change, not a rename/move/version-bump. The affected surface is:

### Source Code
- `internal/review/spool.go` (new)
- `internal/tui/reviews_tab.go` (modified: header, buildTable, column widths, new seam, new helper)

### Test Files
- `internal/review/spool_test.go` (new)
- `internal/tui/reviews_tab_test.go` (modified: new columns in existing assertions, new AC3-mapping test cases)

### Documentation
- No README/docs changes are required by the acceptance criteria. If the builder finds a doc that describes the current Reviews tab column set (grep for "LAST REVIEWED" or "buildTable" across `docs/` and `README.md`) and it enumerates the exact column list, update it to include the three new columns; otherwise no doc changes are needed. (A quick check during spec-writing found no such enumeration in `README.md`/`docs/`, so this is not expected to be required, but the builder should re-check since docs can drift.)

### Configuration / CI / Templates
- None. This change touches no `.gitignore`, `Taskfile.yml`, CI workflow, or template-synced file (`.kiro/agents/*`, `.howmux/scripts/*`, `.howmux/themes/*`). No template-sync surface is affected.

## Summary

Adds a read-only spool-visibility layer to the Reviews tab: a new `internal/review/spool.go` provides a dependency-free front-matter parser and a pending/done-aware file resolver (`ReadSpoolInfo`) plus a pure decision-state classifier (`ClassifySpoolState`) implementing the exact `pending` / `decided: <value>` / `posted` / `no spool` mapping from the issue's AC3. `internal/tui/reviews_tab.go` is extended — not replaced — to compute per-record `SpoolInfo` via a new injectable seam and render three additional columns (`SPOOL PATH`, `VERDICT`, `DECISION STATE`) through the existing `buildTable`/`padToHeight`/`CopyableContent` machinery, preserving every existing column and the styled/plain-text anti-drift guarantee. No spool file is ever written, renamed, or removed by this change; all filesystem access introduced is read-only (`os.ReadFile`/`os.Stat`), verified via explicit grep checks in both the task breakdown and the final gate.
