# Design Spec: Stamp reviewed SHA + timestamp into PR-review spool front-matter

Closes #108

## Assumptions

- The worktree path supplied by krew-lead
  (`/Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-108-96487`) is
  present and used verbatim below; no fallback path was needed.
- The issue body's own investigation (runner.go/spool.go/types.go/bodywriter.go
  reads, plus the pr_review.py ownership finding) is taken as verified ground
  truth. I additionally re-read all four files plus decisionwriter.go and the
  existing spool_test.go/runner_test.go tests directly in this worktree to
  confirm line-level details and match the existing seam/error-message
  conventions before writing this spec.

## Rationale: why RunReview must read-patch-write, not "write while writing"

The issue's proposed change says to stamp the fields "when RunReview writes the
review." Having read `internal/review/runner.go` end to end, **RunReview never
writes the spool file's bytes at all**, in either the current code or in any
plausible reading of the proposed change:

- `RunReview` (runner.go:99–163) fetches the diff, invokes `pr_review.py` as a
  subprocess via the `runReviewToolFunc` seam (runner.go:132), and treats
  `pr_review.py`'s stdout as an opaque path string. It validates that string
  with `validateSpoolPath` (runner.go:71–86) and stores it on the `Record`
  (`rec.SpoolPath`) — it does not open, read, or write that file.
- The only in-repo code that mutates an existing spool file's bytes is
  `BodyWriter.SetBody` (bodywriter.go) and `DecisionWriter.SetDecision`
  (decisionwriter.go), and both do so by shelling out to
  `set-review-body.sh` / `set-review-decision.sh` — external scripts that are
  documented to preserve the front-matter block byte-for-byte and only touch
  the body or the `decision`/`decision_notes` fields respectively. Neither
  script is a vehicle for adding new front-matter keys; using either to smuggle
  in `reviewed_sha`/`generated` would abuse their documented, narrow contracts
  (bodywriter.go's doc comment: "deliberately never touch the spool file's
  bytes directly ... mirrors DecisionWriter's contract exactly").
- The spool `.md` file's front-matter is originally produced by
  `ai-resources/workflows/pr_review.py`, a script in a separate repo that
  howmux does not own or vendor. `spool.go`'s own package doc comment states
  this explicitly: "Howmux does not own this format — it only reads whatever
  pr_review.py / pr_review_finalize.py have written... This file never writes,
  renames, or removes anything under `~/PR-Review/**`; it only ever reads."

Given that constraint, there is no way to satisfy the acceptance criteria
("RunReview writes a non-empty `generated:`... RunReview writes a
`reviewed_sha:`...") by changing `pr_review.py`'s behavior — that repo is out
of scope for this PR, and even if it were in scope, howmux would still need a
way to guarantee the fields land in the file without depending on every
caller/version of `pr_review.py` cooperating.

**Resolution: approach (a).** `RunReview` gains a small, in-repo, read-patch-write
step that runs immediately after `pr_review.py`'s stdout has been validated as
a real spool path (i.e., right after the `validateSpoolPath` check succeeds at
runner.go:145, using the same `spoolPath` and `headSHA` that feed the `Record`
update a few lines later). This step:

1. Reads the spool file's raw bytes.
2. Locates the front-matter fence exactly the way `ParseSpoolFrontMatter`
   does (bounded by two `---` lines).
3. Rewrites that block in place — overwriting a `generated:` line if present
   (even if blank), adding one if absent — and adding a `reviewed_sha:` line
   if absent, or overwriting it if already present (e.g., on a re-review of
   a file `pr_review.py` itself now populates in some future version).
4. Leaves everything after the closing fence (the review body) byte-for-byte
   untouched.
5. Writes the result back to the same path.

This keeps the guarantee entirely inside the howmux repo: **RunReview**, not
`pr_review.py`, becomes the component responsible for these two fields always
being present and correct in the file on disk, regardless of what
`pr_review.py` did or didn't emit. This is a strictly more robust reading of
the issue's intent ("stamp ... when RunReview writes the review") than trying
to patch an external, unversioned-from-howmux's-perspective Python script —
and it satisfies every acceptance criterion without touching `ai-resources`.

Approach (b) (modify `pr_review.py`) is rejected as the primary fix because it
lives outside this repo, cannot be verified/tested from here, and would leave
the guarantee dependent on every deployment having the updated script — but
nothing in this design *prevents* `ai-resources` from later making
`pr_review.py` emit `reviewed_sha`/non-blank `generated` itself; the patch step
here is explicitly written to be idempotent and tolerant of that (see Task 1),
so the two approaches don't conflict if (b) is done later as a follow-up.

## Backward compatibility (confirmed, not re-derived)

`ParseSpoolFrontMatter` (spool.go) is structurally permissive: it builds a
`map[string]string` from whatever `key: value` lines appear between the two
`---` fences, skips lines without a colon, and maps a key with an empty value
(e.g. `decision:`) to `""`. It does not validate against a fixed key set and
has no notion of "required" fields. Consequently:

- A spool file with **no** `reviewed_sha:` line at all → `fields["reviewed_sha"]`
  is simply absent from the map (Go's zero value for a missing map key is
  `""` when read with `fields["reviewed_sha"]`), which every existing caller
  already handles correctly since they only look up known keys individually
  (`fields["verdict"]`, `fields["decision"]`, `fields["decision_notes"]`) and
  never enumerate/require the full key set.
- A spool file with a **blank** `generated:` line parses to `fields["generated"]
  == ""`, exactly like today's `decision:` blank-value case, which is already
  exercised by the existing "well-formed front-matter with blank
  decision/decision_notes" test table entry in spool_test.go.

No changes to `ParseSpoolFrontMatter` are required for backward compatibility —
this spec confirms that claim rather than re-deriving it, per the issue's
explicit instruction. `ParseSpoolFrontMatter` itself is NOT modified by this
work; the new patch function operates on raw bytes and produces output that
`ParseSpoolFrontMatter` already parses correctly with zero changes.

## Solution Approach

1. Add a new front-matter patch function, `WriteReviewedMetadata`, in
   `internal/review/spool.go`. It performs a validated read → in-memory patch
   of the fenced front-matter block → write, following the same
   validation/safety conventions `BodyWriter.SetBody` and
   `DecisionWriter.SetDecision` already use (empty-path checks,
   `resolveSpoolPath` for pending/done resolution, refusing to touch files
   already archived to `done/`).
2. Call `WriteReviewedMetadata` from `RunReview` in `internal/review/runner.go`,
   immediately after `validateSpoolPath(spoolPath)` succeeds and before the
   `Record` mutation block, passing the same `spoolPath`, `headSHA`, and
   `timeNow()` value used for the `Record` update a few lines later — so the
   spool file and the tracking `Record` are always stamped with the identical
   SHA and (functionally) the identical instant.
3. No changes to `ParseSpoolFrontMatter`, `SpoolInfo`, `ReadSpoolInfo`,
   `ReadSpoolBody`, `ClassifySpoolState`, or any TUI-facing type — these
   already tolerate the new fields structurally (see previous section), and
   the issue explicitly puts TUI rendering out of scope.
4. `WriteReviewedMetadata`'s failure mode: if it errors (file went missing
   between validation and the patch call, permission error, front-matter fence
   malformed/absent), `RunReview` returns that error and does **not** proceed
   to update the `Record` — a review whose file wasn't successfully stamped
   should not be marked reviewed, since the reviewer-facing guarantee this
   issue exists to provide would be broken silently otherwise. This mirrors
   the existing pattern in `RunReview` where every prior step
   (`fetchDiffFunc`, `runReviewToolFunc`, `validateSpoolPath`) already returns
   early on error before the `Record` is touched.

## Concurrency Analysis

This change does not cross any of the documented concurrency boundaries
(TUI render loop, watcher poll loop, agent manager). `RunReview` already runs
synchronously within a single review invocation with no shared mutable state
beyond the `Record` passed by value and the `Store`, and `WriteReviewedMetadata`
only touches the filesystem via a single read + single write on a path that is
unique to this review run (no new goroutines, no new fields on any
mutex-guarded type). No locking or concurrent test is required for this issue.

## Mechanical Change Surface

This is not a rename/move/version-bump; it's a scoped feature addition to two
files plus their tests. No `.gitignore`, CI workflow, Taskfile, template-synced
file, or doc surface is affected. `README.md`/`docs/` mention `generated`
nowhere, so no doc updates are required. (Confirmed via inspection of the
`internal/review` package; no repo-wide rename surface exists for this change.)

## Relevant Files

| File | Change |
|---|---|
| `internal/review/spool.go` | Add `WriteReviewedMetadata` (exported) and a private front-matter-block rewrite helper it uses |
| `internal/review/runner.go` | Call `WriteReviewedMetadata` inside `RunReview`, right after `validateSpoolPath` succeeds |
| `internal/review/spool_test.go` | Add table-driven unit tests for `WriteReviewedMetadata` |
| `internal/review/runner_test.go` | Add a test asserting `RunReview`'s spool file ends up with both fields populated for a known `headSHA` |

No other files are touched. `internal/review/types.go`, `bodywriter.go`,
`decisionwriter.go`, `decision.go`, `store.go`, and all TUI code are
unmodified — this matches the issue's explicit "out of scope" note on TUI
consumers.

## Team Orchestration

This is a single, small, sequentially-dependent change confined to one
package (`internal/review`). There is no meaningful parallelization
opportunity: the test for the patch function (Task 2) depends on the function
existing (Task 1), and the call-site wiring (Task 3) and its test (Task 4)
depend on the patch function's exact signature and error contract (Task 1).
One builder agent executing Tasks 1→4 in order is the correct shape; do not
split across parallel builders for this issue.

## Step-by-Step Task Breakdown

### Task 1: Add `WriteReviewedMetadata` to `internal/review/spool.go`

**File:** `internal/review/spool.go`

Add the following, placed near the other spool-mutation-adjacent helpers
(after `resolveSpoolPath`, before `ReadSpoolInfo`, to keep read helpers and
the one write helper visually grouped but not interleaved):

```go
// WriteReviewedMetadata patches the "generated" and "reviewed_sha"
// front-matter fields into the spool file at spoolPath, in place, preserving
// the review body and every other front-matter key byte-for-byte.
//
// This is the one exception to this file's "read-only" package doc comment:
// pr_review.py (ai-resources, out of howmux's control) writes the spool
// file's front-matter with a blank "generated:" and no "reviewed_sha:" key.
// RunReview is the only thing in howmux that both knows the head SHA a
// review actually covered and controls when the spool file is considered
// finished — so it is the correct place to guarantee these two fields are
// populated, rather than depending on pr_review.py's own output. See
// runner.go's call site and issue #108 for the full rationale.
//
// Behavior:
//   - spoolPath == "" -> "no spool path provided"
//   - headSHA == "" -> "no head SHA provided"
//   - spoolPath resolves to nothing (neither pending/ nor done/) -> "no spool file"
//   - spoolPath resolves to done/ (already finalized/archived) -> refuses,
//     mirroring BodyWriter.SetBody / DecisionWriter.SetDecision: a review
//     that has already been drained should not have its front-matter
//     rewritten out from under a decision that already ran against it.
//   - no opening "---" fence, or no closing "---" fence -> returns an error
//     (this function requires a well-formed fence to patch; unlike
//     ParseSpoolFrontMatter it does not silently no-op on malformed input,
//     because silently failing to stamp the file would defeat the point of
//     this function existing)
//   - on success: the front-matter block between the fences has its
//     "generated:" line's value replaced with generatedAt formatted as
//     RFC3339 (added if absent), and its "reviewed_sha:" line's value
//     replaced with headSHA (added if absent). Every other front-matter
//     line, and everything after the closing fence (the review body), is
//     unchanged, including line order of untouched keys.
//
// New keys ("generated", "reviewed_sha" when absent) are appended
// immediately before the closing fence, so a human scanning the file finds
// them at a predictable place (bottom of the front-matter block) whether
// pr_review.py already emitted a blank "generated:" or emitted neither key.
func WriteReviewedMetadata(spoolPath, headSHA string, generatedAt time.Time) error {
	if spoolPath == "" {
		return fmt.Errorf("no spool path provided")
	}
	if headSHA == "" {
		return fmt.Errorf("no head SHA provided")
	}

	resolved, found, inDoneDir := resolveSpoolPath(spoolPath)
	if !found {
		return fmt.Errorf("no spool file to stamp: %s", spoolPath)
	}
	if inDoneDir {
		return fmt.Errorf("review already finalized (archived to done/); its metadata can no longer be changed: %s", resolved)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("failed to read spool file %s: %w", resolved, err)
	}

	patched, err := patchFrontMatterMetadata(data, headSHA, generatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to patch front-matter in %s: %w", resolved, err)
	}

	if err := os.WriteFile(resolved, patched, 0644); err != nil {
		return fmt.Errorf("failed to write spool file %s: %w", resolved, err)
	}

	return nil
}

// patchFrontMatterMetadata rewrites the "generated" and "reviewed_sha" lines
// within the fenced front-matter block of data, leaving every other line
// (front-matter or body) unchanged. It requires a well-formed opening and
// closing "---" fence (unlike ParseSpoolFrontMatter's silent-degrade
// contract) because a caller asking to patch metadata into a file needs to
// know if that file has no fence to patch into, rather than silently
// producing a byte-identical no-op.
//
// This is a pure function over []byte so it is directly unit-testable
// without touching the filesystem; WriteReviewedMetadata is the only
// filesystem-facing caller.
func patchFrontMatterMetadata(data []byte, headSHA, generatedRFC3339 string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("no opening front-matter fence")
	}

	closingIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closingIdx = i
			break
		}
	}
	if closingIdx == -1 {
		return nil, fmt.Errorf("no closing front-matter fence")
	}

	sawGenerated := false
	sawReviewedSHA := false

	frontMatter := lines[1:closingIdx]
	for i, line := range frontMatter {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, _, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "generated":
			frontMatter[i] = "generated: " + generatedRFC3339
			sawGenerated = true
		case "reviewed_sha":
			frontMatter[i] = "reviewed_sha: " + headSHA
			sawReviewedSHA = true
		}
	}

	if !sawReviewedSHA {
		frontMatter = append(frontMatter, "reviewed_sha: "+headSHA)
	}
	if !sawGenerated {
		frontMatter = append(frontMatter, "generated: "+generatedRFC3339)
	}

	newLines := make([]string, 0, len(lines)+2)
	newLines = append(newLines, lines[0])
	newLines = append(newLines, frontMatter...)
	newLines = append(newLines, lines[closingIdx:]...)

	return []byte(strings.Join(newLines, "\n")), nil
}
```

Add `"time"` to the existing `import` block in `spool.go` (currently `"os"`
and `"strings"` only).

**Acceptance criteria:**
- `WriteReviewedMetadata` and `patchFrontMatterMetadata` compile as part of
  package `review`.
- `go build ./...` succeeds.
- `go vet ./internal/review/...` reports no new issues.

**Dependencies:** None.

---

### Task 2: Unit tests for the patch function in `internal/review/spool_test.go`

**File:** `internal/review/spool_test.go`

Add a new test function `TestPatchFrontMatterMetadata` as a table-driven test
in the same style as `TestParseSpoolFrontMatter` (string literals for `data`,
assert on the resulting map via `ParseSpoolFrontMatter` on the *output* of
`patchFrontMatterMetadata`, so the test verifies round-trip correctness
through the real parser rather than asserting on exact byte layout):

Required cases (mirror the table style already used in this file):
1. **"blank generated, no reviewed_sha (pr_review.py's current output)"** —
   input front-matter has `generated:` blank and no `reviewed_sha` key at
   all; after patching with a known SHA and time, `ParseSpoolFrontMatter` on
   the result must return `generated` == the RFC3339 string and
   `reviewed_sha` == the SHA; all other original keys (`repo`, `pr`,
   `verdict`, `decision`, `decision_notes`, `diff_file`) must be unchanged;
   the body after the closing fence must be byte-identical to the input body.
2. **"both keys already present and non-blank (re-review / already-patched
   file)"** — input has a real `generated:` timestamp and a real
   `reviewed_sha:` value already; after patching with a *different* SHA and
   time, both values must be overwritten to the new ones, not appended a
   second time (assert the output has exactly one `generated:` line and
   exactly one `reviewed_sha:` line, and that `ParseSpoolFrontMatter`'s
   resulting map still has exactly the original key count — no duplicate
   keys were introduced).
3. **"no opening fence returns an error"** — input `"repo: owner/repo\npr: 17\n"`
   (no `---` at all) → `patchFrontMatterMetadata` must return a non-nil
   error, and the caller must not attempt to write anything (this case only
   needs to assert the error return, not filesystem behavior — that belongs
   to Task 2's `WriteReviewedMetadata` test below).
4. **"opening fence with no closing fence returns an error"** — input
   `"---\nrepo: owner/repo\npr: 17\n"` → non-nil error.
5. **"body content and body-only blank lines are preserved exactly"** — input
   with a multi-line markdown body containing blank lines and a line that
   itself contains a `---`-like string not at the start of a line (e.g. a
   markdown horizontal rule inside the body, `\n---\n` — must NOT be treated
   as a second front-matter fence since the body is everything after the
   first closing fence encountered) → assert the body substring (join of
   `newLines[closingIdx+1:]` equivalent, or just check the output string
   contains the original body text unchanged after the new closing fence).

Add a second test function `TestWriteReviewedMetadata` (filesystem-facing,
mirrors the `t.TempDir()` + real-file style already used elsewhere in this
package, e.g. `checkout_test.go` / `store_test.go` patterns) covering:

6. **"happy path: pending file gets both fields written to disk"** — write a
   fixture spool file with `generated:` blank and no `reviewed_sha` to a
   `t.TempDir()` path containing a `pending` segment (e.g.
   `filepath.Join(t.TempDir(), "pending", "pr-review-owner-repo-1.md")`, so
   `resolveSpoolPath` treats it as a pending file), call
   `WriteReviewedMetadata(path, "abc1234", knownTime)`, then re-read the file
   from disk and assert via `ParseSpoolFrontMatter` that `reviewed_sha ==
   "abc1234"` and `generated == knownTime.Format(time.RFC3339)`.
7. **"empty spoolPath returns an error without touching the filesystem"** —
   `WriteReviewedMetadata("", "abc1234", knownTime)` returns a non-nil error.
8. **"empty headSHA returns an error"** — `WriteReviewedMetadata(path, "",
   knownTime)` returns a non-nil error.
9. **"nonexistent spool file returns an error"** — path pointing at a file
   that was never created → non-nil error, message mentions "no spool file".
10. **"file already in done/ is refused"** — write the fixture at a path
    containing a `done` segment instead of `pending`, call
    `WriteReviewedMetadata`, assert a non-nil error mentioning "already
    finalized", AND re-read the file afterward to assert its bytes are
    completely unchanged from the original fixture (proves the refusal
    happens before any write, not after a partial one).

**Acceptance criteria:**
- All 10 cases above exist as either table entries or discrete test
  functions.
- `go test ./internal/review/... -run 'TestPatchFrontMatterMetadata|TestWriteReviewedMetadata' -v` passes.

**Dependencies:** Task 1.

---

### Task 3: Wire `WriteReviewedMetadata` into `RunReview` in `internal/review/runner.go`

**File:** `internal/review/runner.go`

In `RunReview`, insert the call immediately after the existing
`validateSpoolPath` check and its `logging.Debug` line, and before the
`// Update record with StatusReviewed...` comment block. Current code at that
point (runner.go, inside `RunReview`):

```go
	spoolPath := stdoutLines[len(stdoutLines)-1]
	if err := validateSpoolPath(spoolPath); err != nil {
		return fmt.Errorf("pr_review.py returned an unexpected spool path %q: %w", spoolPath, err)
	}
	logging.Debug("review completed", "spool_path", spoolPath)

	// Update record with StatusReviewed, timestamps, and spool path
	rec.Status = StatusReviewed
	rec.LastReviewedSHA = headSHA
	rec.LastReviewedAt = timeNow().Format(time.RFC3339)
	rec.LastServicedRequest = headSHA
	rec.SpoolPath = spoolPath
```

Change to:

```go
	spoolPath := stdoutLines[len(stdoutLines)-1]
	if err := validateSpoolPath(spoolPath); err != nil {
		return fmt.Errorf("pr_review.py returned an unexpected spool path %q: %w", spoolPath, err)
	}
	logging.Debug("review completed", "spool_path", spoolPath)

	// Stamp the reviewed SHA and generation timestamp into the spool file's
	// own front-matter, so the review document is self-describing without
	// cross-referencing the tracking Record. pr_review.py (ai-resources, out
	// of howmux's control) does not populate these reliably today — see
	// WriteReviewedMetadata's doc comment for the full rationale. Use the
	// same generatedAt instant for both the spool file and the Record below
	// so the two stay consistent.
	generatedAt := timeNow()
	if err := WriteReviewedMetadata(spoolPath, headSHA, generatedAt); err != nil {
		return fmt.Errorf("failed to stamp reviewed metadata into spool file %s: %w", spoolPath, err)
	}

	// Update record with StatusReviewed, timestamps, and spool path
	rec.Status = StatusReviewed
	rec.LastReviewedSHA = headSHA
	rec.LastReviewedAt = generatedAt.Format(time.RFC3339)
	rec.LastServicedRequest = headSHA
	rec.SpoolPath = spoolPath
```

Note the `rec.LastReviewedAt` assignment changes from calling `timeNow()`
inline to reusing the `generatedAt` local — this guarantees the Record's
`LastReviewedAt` and the spool file's `generated:` field are byte-identical
RFC3339 strings for the same review run (previously they'd almost always
match since `timeNow()` is fast, but relying on two separate calls to a
monotonic-ish clock returning the exact same value was never guaranteed).

**Acceptance criteria:**
- `RunReview` calls `WriteReviewedMetadata` with `spoolPath`, `headSHA`, and a
  single `generatedAt := timeNow()` value, before mutating `rec`.
- `rec.LastReviewedAt` is set from that same `generatedAt` value, not a fresh
  `timeNow()` call.
- If `WriteReviewedMetadata` returns an error, `RunReview` returns
  immediately with a wrapped error and does **not** call `storeImpl.Save`
  (i.e., a review is never marked `StatusReviewed` in the Record if its
  spool file wasn't successfully stamped).
- `go build ./...` succeeds.

**Dependencies:** Task 1.

---

### Task 4: Test `RunReview`'s end-to-end spool stamping in `internal/review/runner_test.go`

**File:** `internal/review/runner_test.go`

Add a new test function `TestRunReview_StampsSpoolFrontMatter`, following the
existing style in this file (save/restore `fetchDiffFunc`, `runReviewToolFunc`,
`timeNow` package vars via `defer`; fixed `timeNow`; a fake `runReviewToolFunc`
that returns a spool path).

Key difference from the existing fakes (`TestRunReview_DiffFetch_Argv`,
`TestRunReview_ReviewTool_Argv`): those tests return a spool path string that
does not need to correspond to a real file, because they only assert on
captured argv. This new test needs `runReviewToolFunc`'s fake implementation
to **actually create a real spool file on disk** at the path it returns (using
`t.TempDir()`), containing realistic front-matter fixture content
(`generated:` blank, no `reviewed_sha`, matching the issue's own example),
because `WriteReviewedMetadata` inside `RunReview` will attempt to read/patch
that exact path.

```go
func TestRunReview_StampsSpoolFrontMatter(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	// Spool path must contain a "pending" segment so resolveSpoolPath (used
	// internally by WriteReviewedMetadata) treats it as a pending file, and
	// must exist on disk with realistic pre-patch front-matter, mirroring
	// pr_review.py's actual output shape (blank generated, no reviewed_sha).
	spoolDir := filepath.Join(t.TempDir(), "PR-Review", "pending")
	if err := os.MkdirAll(spoolDir, 0755); err != nil {
		t.Fatalf("failed to create spool dir: %v", err)
	}
	spoolPath := filepath.Join(spoolDir, "pr-review-testowner-testrepo-42.md")
	fixture := `---
repo: testowner/testrepo
pr: 42
verdict: APPROVE
decision:
decision_notes:
diff_file: /tmp/pr-42-123.diff
generated:
---

Review body unchanged.
`
	if err := os.WriteFile(spoolPath, []byte(fixture), 0644); err != nil {
		t.Fatalf("failed to write spool fixture: %v", err)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "testowner/testrepo",
		PR:         42,
		URL:        "https://github.com/testowner/testrepo/pull/42",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-42",
	}

	const knownSHA = "abc1234def5678"

	ctx := context.Background()
	if err := RunReview(ctx, rec, knownSHA, store, io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	patched, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to read patched spool file: %v", err)
	}

	fields := ParseSpoolFrontMatter(patched)
	if fields["reviewed_sha"] != knownSHA {
		t.Errorf("expected reviewed_sha %q, got %q", knownSHA, fields["reviewed_sha"])
	}
	wantGenerated := fixedTime.Format(time.RFC3339)
	if fields["generated"] != wantGenerated {
		t.Errorf("expected generated %q, got %q", wantGenerated, fields["generated"])
	}

	// Body must be untouched.
	if !strings.Contains(string(patched), "Review body unchanged.") {
		t.Errorf("expected review body to be preserved, got: %s", string(patched))
	}

	// Other pre-existing front-matter keys must survive unchanged.
	if fields["repo"] != "testowner/testrepo" {
		t.Errorf("expected repo to be preserved, got %q", fields["repo"])
	}
	if fields["verdict"] != "APPROVE" {
		t.Errorf("expected verdict to be preserved, got %q", fields["verdict"])
	}
}
```

Add a second test, `TestRunReview_SpoolStampFailure_DoesNotUpdateRecord`,
that points `spoolPath` at a location under a `done/` segment (so
`WriteReviewedMetadata` refuses it as already-finalized) and asserts:
- `RunReview` returns a non-nil error.
- `store.Load(rec.Repo, rec.PR)` (or the equivalent existing `Store` read
  method used elsewhere in this test file) shows the record was **not**
  saved / not transitioned to `StatusReviewed` — i.e., `storeImpl.Save` was
  never reached. If the existing `Store` doesn't expose a clean "was this
  ever saved" check, structure this as: pre-populate the store directory with
  no record for this PR, run `RunReview`, then assert loading it back returns
  "not found" rather than a `Record` with `Status == StatusReviewed`.

Add `"path/filepath"` to the import block in `runner_test.go` if not already
present (check current imports first; the existing block at the top of the
file already has `bytes`, `context`, `fmt`, `io`, `os`, `strings`, `testing`,
`time` — `path/filepath` needs to be added).

**Acceptance criteria:**
- `TestRunReview_StampsSpoolFrontMatter` exists and passes, directly
  satisfying the issue's explicit acceptance criterion: "A test asserts both
  fields are populated in the written spool for a review run with a known
  head SHA."
- `TestRunReview_SpoolStampFailure_DoesNotUpdateRecord` exists and passes,
  proving the fail-closed behavior from Task 3.
- `go test ./internal/review/... -run TestRunReview -v` passes, including all
  pre-existing `TestRunReview_*` tests (no regressions).

**Dependencies:** Tasks 1, 3.

---

## Validation Commands

Run from the repo root inside the worktree:

```bash
# Build the whole module
go build ./...

# Vet the changed package
go vet ./internal/review/...

# Run the full review package test suite (existing + new tests)
go test ./internal/review/... -v

# Targeted re-run of just the new/changed tests
go test ./internal/review/... -run 'TestPatchFrontMatterMetadata|TestWriteReviewedMetadata|TestRunReview' -v

# Race detector — no new concurrency surface was introduced, but this
# confirms the change doesn't regress existing race-checked behavior in the
# package
go test ./internal/review/... -race
```

All commands must exit 0 with no test failures for the issue to be considered
resolved.

## Out of Scope (confirmed, unchanged from issue)

- `SpoolInfo` struct: no new fields added.
- `ReadSpoolInfo`, `ReadSpoolBody`, `ClassifySpoolState`: unchanged.
- TUI `ReviewContentTab` or any rendering of `reviewed_sha`/`generated`:
  unchanged. This is explicitly deferred to a follow-up issue per the
  original issue body.
- `ai-resources/pr_review.py`: not modified. This spec's approach makes that
  modification unnecessary for satisfying the acceptance criteria, though it
  remains a valid independent follow-up (see Rationale section) that would
  not conflict with the patch step added here, since the patch is idempotent
  and overwrites/no-ops correctly either way.
