# Design Specification: Footer Input Prompt Shows "kiro-krew>" Instead of "howmux>"

Closes #121

## Solution Approach

This is a narrow, user-visible string fix, not a rename sweep. Three call sites
hardcode the literal `"kiro-krew> "` prompt string used by the footer input
line: the actual `textinput.Prompt` assignment, a duplicate literal used only
for click-hit-testing width calculation, and an explanatory code comment. All
three must change to `"howmux> "` together, because the click-hit-testing logic
in `tui.go` computes `promptWidth` from its own local literal rather than
reading it from the `AutocompleteInput`'s configured prompt — if only one
occurrence is changed, the click boundary will silently desync from the
rendered prompt (mouse clicks would target the wrong column), which is exactly
the kind of correctness regression the issue's "no functional behavor changes"
criterion is designed to catch.

Additionally, a repo-wide check (per the issue's third acceptance criterion)
turned up one more genuinely user-visible surface: the About overlay's title
string `"Kiro-Krew Version Information"` in `internal/tui/commands.go:568`.
This is displayed to the user when they run the `about` command / press the
about hotkey, so it falls inside the issue's "user-visible text" scope even
though it wasn't called out in the issue's Context/References section. It is
included here as an in-scope fix. The `--version`/`--about` CLI flag output in
`cmd/howmux/cmd/root.go` was checked and is already clean — it reads
`Use: "howmux"`, `Short: "Multi-agent development tool"`, and a `howmux`-branded
`Long` description already, with no "kiro-krew" leftovers. No window-title API
(`SetWindowTitle` or similar) is used anywhere in the codebase, since this is a
terminal TUI — that item under "window title" in the issue's acceptance
criteria is not applicable here.

All other `kiro-krew` occurrences found by the repo-wide grep are internal
(comments, internal error strings, log paths, test fixture names, template
sync paths, changelog history, historical spec files, agent-prompt prose) and
are explicitly out of scope per the issue's constraint: "do not perform a
broad rename sweep beyond user-facing text." These are enumerated below for
completeness and future-cleanup visibility, but none are touched by this
change.

## Relevant Files

### Files to modify (in scope)

| File | Line(s) | Change |
|------|---------|--------|
| `internal/tui/tui.go` | 511 | Comment: `// The prompt is "kiro-krew> " from the textinput` → `// The prompt is "howmux> " from the textinput` |
| `internal/tui/tui.go` | 512 | `prompt := "kiro-krew> "` → `prompt := "howmux> "` |
| `internal/tui/autocomplete.go` | 35 | `ti.Prompt = "kiro-krew> "` → `ti.Prompt = "howmux> "` |
| `internal/tui/commands.go` | 568 | `"Kiro-Krew Version Information"` → `"Howmux Version Information"` (overlay title string passed to `m.activateOverlay`) |

### Files explicitly NOT modified (out of scope, per issue constraint)

- `internal/tui/tui.go:2534` — `logPath := ".howmux/kiro-krew.log"` — internal log filename, explicitly flagged as out of scope by the issue itself. Do not touch.
- All other `kiro-krew` occurrences listed under "Other Occurrences Found" below.

## Exact String Changes

1. **`internal/tui/tui.go:511`** (comment)
   - Old: `// The prompt is "kiro-krew> " from the textinput`
   - New: `// The prompt is "howmux> " from the textinput`

2. **`internal/tui/tui.go:512`** (click-hit-test width literal)
   - Old: `prompt := "kiro-krew> "`
   - New: `prompt := "howmux> "`

3. **`internal/tui/autocomplete.go:35`** (actual rendered prompt)
   - Old: `ti.Prompt = "kiro-krew> "`
   - New: `ti.Prompt = "howmux> "`

4. **`internal/tui/commands.go:568`** (About overlay title, in-scope user-visible surface)
   - Old: `m = m.activateOverlay(overlayAbout, "Kiro-Krew Version Information", m.aboutDialog.GetFullContent())`
   - New: `m = m.activateOverlay(overlayAbout, "Howmux Version Information", m.aboutDialog.GetFullContent())`

No other characters on these lines change. No struct fields, function signatures, or control flow change.

## Other "kiro-krew" Occurrences Found (repo-wide check)

Per acceptance criterion 3 ("A quick repo-wide check confirms no other
user-visible surface... still shows kiro-krew"), the following is the complete
result of `grep -rn "kiro-krew" -i .` across the worktree, classified by
scope. Nothing in this list beyond the four items above is modified by this
change.

| Location | Type | In scope? | Notes |
|----------|------|-----------|-------|
| `internal/tui/tui.go:2534` | Internal log path `.howmux/kiro-krew.log` | **Explicitly out of scope** (per issue) | Flagged by the issue itself as future cleanup, not user-visible UI text |
| `internal/tui/commands.go:568` | About overlay title | **In scope — fixed by this change** | User-visible; see above |
| `cmd/howmux/cmd/root.go` | CLI `Use`/`Short`/`Long`/`--version`/`--about` output | Already clean, no leftover | Verified: `Use: "howmux"`, howmux-branded `Short`/`Long`; no "kiro-krew" text anywhere in this file |
| Window title | N/A | Not applicable | Terminal TUI — no `SetWindowTitle` or equivalent API used anywhere in the codebase |
| `internal/tui/integration_test.go:484` | Test fixture temp-dir name prefix `"kiro-krew-test-sessions-*"` | Out of scope (not user-visible; test-only string) | Future cleanup candidate if a full rename sweep is ever done |
| `internal/eval/selective_test.go` (×3) | Test fixture temp-dir name prefix `"kiro-krew-test"` | Out of scope (test-only) | Future cleanup candidate |
| `internal/hotkey/detector.go`, `error_handling_test.go`, `integration_test.go`, `INTEGRATION_TESTS.md` | Internal function doc-comment (`IsKiroKrewContext`), internal error string (`"hotkey toggle not available outside kiro-krew context"`), and associated tests/docs | Out of scope (internal error text, not a UI surface a user reads as branding; it's a descriptive runtime-context term, not the product name label) | Future cleanup candidate — could be reworded but does not display as a "kiro-krew>" style leftover brand |
| `internal/templates/extract.go:33-34` | Template path-rewrite logic (`relSlash == "kiro-krew"` → rewritten to `.howmux`) | Out of scope | This is intentional legacy-path-handling logic (migrates old template layouts), not a display string |
| `internal/tui/commands.go` — none else found | — | — | Only line 568 in this file references kiro-krew |
| `package.json:2,17` / `package-lock.json:2,8` | npm package `name` field (`"kiro-krew"`) and repo URL | Out of scope (not UI text; broader rename-sweep territory the issue explicitly excludes) | Future cleanup candidate — would require npm package rename, out of this issue's scope |
| `scripts/compare-templates.go:55-56` | Internal comment + template dir name | Out of scope (internal tooling, not user-facing) | — |
| `CHANGELOG.md` (many lines) | Historical changelog entries referencing the old repo/commit history | Out of scope | Historical record; must not be rewritten |
| `.howmux/specs/*.md` (multiple files) | Historical design specs from prior issues (#16, #39, #151, #197, #104, #9, #49, #33) | Out of scope | Historical artifacts, not live code or UI |
| `.kiro/agents/architect-prompt.md`, `cmd/howmux/templates/kiro/agents/architect-prompt.md` | Agent-prompt prose using "Kiro-Krew" as a proper-noun reference to the orchestration system | Out of scope | Internal agent instructions, not user-facing UI |
| `.kiro/skills/*/SKILL.md` (builder-conventions, validator-conventions, discover-qa-tools, planner-conventions) | Skill documentation referencing old paths/examples | Out of scope | Internal agent guidance docs |
| `docs/eval-framework-analysis.md` | Doc title referencing "Kiro-Krew Evaluation Framework" | Out of scope | Internal dev-facing doc, not shipped product UI |

**Summary**: Beyond the two prompt strings named in the issue, exactly one
additional genuinely user-visible surface was found (`commands.go:568`, the
About overlay title) and is included in this fix. Everything else in the
repo-wide `kiro-krew` grep is either explicitly flagged out of scope by the
issue itself (`tui.go:2534`), or is internal (tests, comments, docs, package
metadata, historical specs/changelog, agent-prompt prose) and stays untouched
per the issue's constraint against a broad rename sweep.

## Team Orchestration

Single builder agent, single task. No parallelization needed — four
one-line/one-string changes in three files with no interdependencies beyond
"do all four in the same commit." No concurrency boundary is crossed: these
are static string literals rendered/read synchronously on the TUI's own
goroutine (Bubble Tea's `Update`/`View` run single-threaded per the Elm
architecture used by this TUI), with no shared mutable state read or written
across goroutines as a result of this change.

## Step-by-Step Task Breakdown

### Task 1: Update the footer prompt strings and About overlay title
**Acceptance Criteria**:
- `internal/tui/autocomplete.go:35` — `ti.Prompt` literal changed from `"kiro-krew> "` to `"howmux> "`
- `internal/tui/tui.go:512` — `prompt` literal in the click-hit-test helper changed from `"kiro-krew> "` to `"howmux> "`
- `internal/tui/tui.go:511` — comment above it updated to say `"howmux> "` instead of `"kiro-krew> "` (keep the comment accurate, since it directly describes line 512)
- `internal/tui/commands.go:568` — About overlay title string changed from `"Kiro-Krew Version Information"` to `"Howmux Version Information"`
- `internal/tui/tui.go:2534` (`.howmux/kiro-krew.log` path) is **NOT** modified — verify this line is unchanged after the edit
- No other lines in any of these three files are modified
- **Verification**: `grep -n 'kiro-krew> ' internal/tui/tui.go internal/tui/autocomplete.go` returns zero matches
- **Verification**: `grep -n 'Kiro-Krew Version Information' internal/tui/commands.go` returns zero matches
- **Verification**: `grep -n 'kiro-krew.log' internal/tui/tui.go` still returns exactly one match at line 2534 (confirms the out-of-scope log path was left untouched)
- **Verification**: `go build ./...` succeeds
- **Verification**: existing tests in `internal/tui/` still pass (e.g. `go test ./internal/tui/...`); if any test asserts the literal string `"kiro-krew> "` or `"Kiro-Krew Version Information"`, update that test's expected string to match the new value — this is a permitted test-string update, not a scope violation, since the test exists to lock in the exact prompt/label text the UI renders

**Dependencies**: None — this is the only task.

## Validation Commands

```bash
# 1. Confirm no rendered "kiro-krew>" prompt remains in TUI source
grep -rn 'kiro-krew> ' internal/tui/

# 2. Confirm the About overlay title no longer says Kiro-Krew
grep -rn 'Kiro-Krew Version Information' internal/tui/

# 3. Confirm the out-of-scope internal log path is untouched
grep -n 'kiro-krew.log' internal/tui/tui.go   # expect exactly 1 match, at line 2534

# 4. Confirm --version / --about CLI output has no leftover (should already be clean)
grep -n -i 'kiro-krew' cmd/howmux/cmd/root.go   # expect 0 matches

# 5. Build and test
go build ./...
go test ./internal/tui/...

# 6. Manual smoke check (optional): run the TUI and confirm the footer reads "howmux> "
go run ./cmd/howmux
```

## Assumptions

- The `<WORKTREE>` path supplied by krew-lead
  (`/Users/matthowt/GITWorkspace/improving/howmux/.worktrees/issue-121-54005`)
  was present, consistent, and used for all file operations in this spec. No
  fallback path was needed — this is noted only for completeness, not because
  any delegation fault occurred.
- The About overlay title fix (`commands.go:568`) was not named in the issue's
  Context/References section but is included as in-scope because it is a
  genuinely user-visible string, matching the issue's own acceptance criterion
  3 language ("no other user-visible surface... still shows kiro-krew"). If
  the reviewer disagrees this should be in scope, it is a single isolated line
  and can be trivially reverted without affecting the rest of the fix.
