# Design Spec: Fix reviewer identity for PR-review re-request detection

Closes #89

## Problem Statement (recap)

`internal/tui/tui.go` constructs the review watcher with `cfg.Repo` (the repo
slug, e.g. `matthiashowellyopp/howmux`) as the `reviewer` argument:

```go
reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)
```

`decideReviewAction`'s Rule 3 calls `pr.IsReviewRequestedFor(reviewer)`, which
compares `reviewer` against each review request's `login`/`slug`/`name`. A
repo slug can never match a reviewer login, so Rule 3 never fires and
re-requested reviews are silently skipped forever. Only Rule 2
(`LastReviewedSHA == ""`) has ever fired in practice, masking the bug for a
PR's first review.

## Solution Approach

Resolve the correct reviewer identity **once, before constructing the
watcher**, using a small precedence helper:

1. If `cfg.Reviewer` (new optional config field) is set → use it verbatim.
2. Else, resolve the current `gh` user's login via a new
   `github.GetCurrentUser()` function → use that.
3. Else (resolution fails and no override) → fail loudly. Do not silently
   fall back to a value (like `cfg.Repo`) that can never match.

`github.GetCurrentUser()` follows the **existing package-level function-var
seam pattern already used in `internal/review/watcher.go`**
(`fetchPRFunc`, `removeRecordFunc`, `dispatchReviewFunc`): a package-level
`var` holding a function that wraps the real `gh` invocation, swappable in
tests. `internal/github` does not currently have this seam for any of its
`exec.Command` call sites (confirmed: `client.go` and `pr.go` call
`exec.Command`/`exec.CommandContext` directly with no injectable var, and
there is no `client_test.go`). This issue introduces the seam for the one
new call site (`GetCurrentUser`) without refactoring the other existing
`github` functions — that refactor is out of scope for this fix.

`decideReviewAction` and its existing test file (`decision_test.go`) are
**not touched**. Only the value fed into `review.NewWatcher(...)` as
`reviewer` changes.

### Concurrency Analysis

**No new concurrency concern.** The reviewer identity is resolved once,
synchronously, on the main goroutine in `newModel()` — before the watcher
goroutine is started via `reviewWatcher.Start()` (called later, in the
`Update` loop per the `case "review"` handling seen at tui.go line ~385-386).
The resolved string is passed by value into `review.NewWatcher(...)` and
stored as `Watcher.reviewer`, which is already only ever read inside the
watcher's own poll-loop goroutine (`pollOnce` → `decideReviewAction`) and
never mutated after construction. No new field is added to a
mutex-guarded struct, no new cross-goroutine read/write pair is introduced,
and `Watcher.reviewer` was already unexported and immutable post-construction
before this change. No new lock or concurrent test is required for this
reason — this is a plain data-flow correctness fix, not a new shared-state
access pattern.

## Relevant Files

| File | Change |
|---|---|
| `internal/github/client.go` | Add `GetCurrentUser()` + injectable exec seam var |
| `internal/github/client_test.go` | **New file.** Table tests for `GetCurrentUser` via the seam |
| `internal/config/config.go` | Add `Reviewer string` field to `Config` struct |
| `internal/config/config_test.go` | Add test(s) confirming `reviewer:` loads/defaults to empty |
| `internal/tui/reviewer.go` | **New file.** `ResolveReviewer(cfg *config.Config) (string, error)` precedence helper |
| `internal/tui/reviewer_test.go` | **New file.** Table tests for precedence logic |
| `internal/tui/tui.go` | Call `ResolveReviewer` before constructing `reviewWatcher`; handle error |
| `internal/github/pr_test.go` | No change required (existing `TestIsReviewRequestedFor` / team-reviewer tests already cover the matching semantics; see Task 4 for what, if anything, to add) |

No changes to `internal/review/decision.go`, `internal/review/watcher.go`,
`internal/review/decision_test.go`, or `internal/review/watcher_test.go`.

## Detailed Design

### 1. `github.GetCurrentUser()` — new function + seam

Add to `internal/github/client.go`:

```go
// execCurrentUserFunc is the injectable seam for GetCurrentUser, following
// the same package-level function-var pattern used for other testable gh
// invocations in this codebase (see internal/review/watcher.go). Tests
// replace this var; production code leaves it as the real gh invocation.
var execCurrentUserFunc = func() ([]byte, error) {
	cmd := exec.Command("gh", "api", "user", "--jq", ".login")
	return cmd.Output()
}

// GetCurrentUser resolves the currently authenticated GitHub user's login by
// running `gh api user --jq .login`. It is routed through execCurrentUserFunc
// so tests can substitute a fake without invoking the real gh CLI.
func GetCurrentUser() (string, error) {
	output, err := execCurrentUserFunc()
	if err != nil {
		return "", fmt.Errorf("gh api user failed: %w", err)
	}
	login := strings.TrimSpace(string(output))
	if login == "" {
		return "", fmt.Errorf("gh api user returned an empty login")
	}
	return login, nil
}
```

Notes for the builder:
- `exec`, `fmt`, `strings` are already imported in `client.go`; no new imports
  needed.
- Mirror the error-wrapping style already used in `GetIssueDetails`/`ListIssues`
  (`fmt.Errorf("gh ... failed: %w", err)`).
- Do **not** add a rate-limit check here — `gh api user` is not the
  rate-limited pagination path that `isRateLimited` guards; keep this
  function minimal per the issue's scope.

### 2. Config struct change — optional reviewer override

In `internal/config/config.go`, add one field to `Config`:

```go
type Config struct {
	Repo                string        `yaml:"repo"`
	Label               string        `yaml:"label"`
	BaseBranch          string        `yaml:"base_branch"`
	PollInterval        time.Duration `yaml:"poll_interval"`
	MaxRetries          int           `yaml:"max_retries"`
	MaxQARetries        int           `yaml:"max_qa_retries"`
	MaxActivityLines    int           `yaml:"max_activity_lines"`
	ConsoleLogging      bool          `yaml:"console_logging"`
	Theme               string        `yaml:"theme"`
	EnableCopilotReview bool          `yaml:"enable_copilot_review"`
	Reviewer            string        `yaml:"reviewer"` // Optional: overrides the resolved gh login used for IsReviewRequestedFor matching
	Session             SessionConfig `yaml:"session"`
	Sandbox             SandboxConfig `yaml:"sandbox"`
	Logging             LoggingConfig `yaml:"logging"`
	LoadedTheme         *Theme        `yaml:"-"`
}
```

- No default needs to be added to the `cfg := &Config{...}` defaults literal
  in `Load()` — the Go zero value for `string` (`""`) is exactly "unset",
  which is what the precedence helper checks for.
- No new validation is required in `Load()`. An empty `Reviewer` is valid
  (falls through to gh-login resolution); this field has no "must be set"
  constraint. Do not add a `cfg.Reviewer == ""` error check in `Load()` —
  the missing-identity failure belongs in the resolution helper (Task 3),
  which can also account for a failed `gh` lookup, not just an empty config
  field.

### 3. Precedence resolution — new helper in `internal/tui`

Create `internal/tui/reviewer.go`:

```go
package tui

import (
	"fmt"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/github"
)

// getCurrentUserFunc is the injectable seam over github.GetCurrentUser for
// tests. Production code leaves this as the real call.
var getCurrentUserFunc = github.GetCurrentUser

// ResolveReviewer determines the GitHub identity used to match
// pr.IsReviewRequestedFor(reviewer) in the review watcher.
//
// Precedence:
//  1. cfg.Reviewer, if non-empty (explicit override always wins)
//  2. The currently authenticated gh user's login, resolved via
//     github.GetCurrentUser()
//  3. If neither is available, return an error — callers must not fall back
//     to a value (such as the repo slug) that can never match a real
//     reviewer identity.
func ResolveReviewer(cfg *config.Config) (string, error) {
	if cfg.Reviewer != "" {
		return cfg.Reviewer, nil
	}

	login, err := getCurrentUserFunc()
	if err != nil {
		return "", fmt.Errorf("failed to resolve reviewer identity: no reviewer override configured " +
			"(set 'reviewer:' in .howmux/config.yaml) and gh login resolution failed: %w", err)
	}

	return login, nil
}
```

Design notes:
- This lives in `internal/tui` (not `internal/review` or `internal/config`)
  because it is a one-time orchestration step that combines two other
  packages' concerns (`config` + `github`) and is only ever needed at
  `newModel()` construction time — it is not part of the watcher's own
  domain logic (`decideReviewAction` stays pure and untouched, per
  constraint).
- `getCurrentUserFunc` is a package-level var assigned directly to
  `github.GetCurrentUser` (not wrapped in a closure) so it can be swapped to
  a fake in `reviewer_test.go` without touching `internal/github` at all.
  This keeps `internal/github`'s own seam (`execCurrentUserFunc`) fully
  hermetic and lets `internal/tui` tests avoid needing `internal/github`'s
  internals.
- Returning an error (rather than logging a warning and continuing) is the
  correct choice per acceptance criterion 4 ("fail or warn clearly"): the
  caller (`newModel`, or more likely the code that calls `newModel`/starts
  the TUI) decides whether that error is fatal at startup or merely logged
  and the watcher is skipped. See Task 4 for exactly how `tui.go` handles it.

### 4. Call site changes in `internal/tui/tui.go`

Current code (lines ~163-168 today):

```go
	// Initialize review watcher
	reviewStore := review.NewDefaultStore()
	reviewPollInterval := 5 * time.Minute // Default poll interval
	if cfg.PollInterval > 0 {
		reviewPollInterval = cfg.PollInterval
	}
	reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)
```

Replace with:

```go
	// Initialize review watcher
	reviewStore := review.NewDefaultStore()
	reviewPollInterval := 5 * time.Minute // Default poll interval
	if cfg.PollInterval > 0 {
		reviewPollInterval = cfg.PollInterval
	}
	reviewer, err := ResolveReviewer(cfg)
	if err != nil {
		// No override configured and gh login resolution failed: the watcher
		// would otherwise compare against an identity that can never match a
		// real reviewer (see issue #89). Log clearly and fall back to an
		// empty reviewer so IsReviewRequestedFor never spuriously matches,
		// rather than crashing the whole TUI over a re-review convenience
		// feature. Rule 2 (never-reviewed) still works with an empty
		// reviewer; only re-request detection (Rule 3) is disabled.
		logging.Error("failed to resolve reviewer identity for PR review watcher; re-request detection disabled", "error", err)
	}
	reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)
```

Rationale for "log and continue with empty string" over "panic/os.Exit" at
this call site:
- `newModel()` has no error return in its signature (`func newModel(...) model`)
  and is called during TUI startup; introducing a fatal error here would
  require a signature change that cascades to `newModel`'s caller(s) and is
  out of proportion to a correctness fix for an opt-in re-review feature.
- An empty `reviewer` string is safe: `IsReviewRequestedFor("")` will not
  match any real login/slug/name (GitHub logins/slugs are never empty
  strings), so the watcher degrades to "Rule 2 only" behavior — exactly
  today's de facto (broken) behavior — rather than a crash. This satisfies
  acceptance criterion 4's "warn clearly" branch; it does not silently
  compare against a value that could accidentally match (like `cfg.Repo`
  might in some pathological org-name-equals-username scenario) — empty
  string is guaranteed never to match.
- `logging` is already imported in `tui.go` (confirmed: `internal/logging`
  import present, and `logging.Info`/`logging.Debug` already used in this
  file's surrounding code per `internal/review/watcher.go` patterns) — no
  new import needed. Use `logging.Error` (not `Warn`) because this
  represents a configuration/environment problem the user should fix, not a
  routine runtime condition.
- **Builder must add `err` handling carefully**: `newModel`'s enclosing scope
  at this point does not otherwise declare `err` — verify no variable shadow
  conflicts by checking for an existing `err` in scope in that function
  before this insertion point (grep confirmed none exists between the
  function start and this line as of this spec's writing, but re-verify at
  implementation time since other issues may land first).

No other call sites construct a `review.Watcher` — confirmed via
`review.NewWatcher(` appearing exactly once in `internal/tui/tui.go` and
nowhere else in production code (test files construct it directly with
literal strings, which is unaffected by this change).

### 5. Table test cases required

#### 5a. `internal/github/client_test.go` (new file) — `GetCurrentUser`

Package-level seam swap pattern (mirrors `internal/review/watcher_test.go`
style: save original, defer restore).

```go
func TestGetCurrentUser(t *testing.T) {
	tests := []struct {
		name      string
		fakeExec  func() ([]byte, error)
		wantLogin string
		wantErr   bool
	}{
		{
			name:      "successful login resolution",
			fakeExec:  func() ([]byte, error) { return []byte("octocat\n"), nil },
			wantLogin: "octocat",
			wantErr:   false,
		},
		{
			name:      "trims surrounding whitespace/newline",
			fakeExec:  func() ([]byte, error) { return []byte("  octocat\n\n"), nil },
			wantLogin: "octocat",
			wantErr:   false,
		},
		{
			name:      "gh command failure surfaces as error",
			fakeExec:  func() ([]byte, error) { return nil, fmt.Errorf("gh: not authenticated") },
			wantLogin: "",
			wantErr:   true,
		},
		{
			name:      "empty output is an error, not a valid empty login",
			fakeExec:  func() ([]byte, error) { return []byte(""), nil },
			wantLogin: "",
			wantErr:   true,
		},
		{
			name:      "whitespace-only output is an error",
			fakeExec:  func() ([]byte, error) { return []byte("   \n"), nil },
			wantLogin: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := execCurrentUserFunc
			execCurrentUserFunc = tt.fakeExec
			defer func() { execCurrentUserFunc = orig }()

			got, err := GetCurrentUser()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetCurrentUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantLogin {
				t.Errorf("GetCurrentUser() = %q, want %q", got, tt.wantLogin)
			}
		})
	}
}
```

Add `"fmt"` to the test file's imports for the failure-case fake.

#### 5b. `internal/tui/reviewer_test.go` (new file) — precedence logic

```go
package tui

import (
	"fmt"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/config"
)

func TestResolveReviewer(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.Config
		fakeGetUser  func() (string, error)
		wantReviewer string
		wantErr      bool
	}{
		{
			name:         "explicit override wins over gh login",
			cfg:          &config.Config{Reviewer: "override-user"},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "override-user",
			wantErr:      false,
		},
		{
			name:         "empty override falls back to resolved gh login",
			cfg:          &config.Config{Reviewer: ""},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "gh-login-user",
			wantErr:      false,
		},
		{
			name:         "empty override and gh resolution failure returns error",
			cfg:          &config.Config{Reviewer: ""},
			fakeGetUser:  func() (string, error) { return "", fmt.Errorf("gh: not authenticated") },
			wantReviewer: "",
			wantErr:      true,
		},
		{
			name:         "override is used even when gh resolution would also fail",
			cfg:          &config.Config{Reviewer: "override-user"},
			fakeGetUser:  func() (string, error) { return "", fmt.Errorf("gh: not authenticated") },
			wantReviewer: "override-user",
			wantErr:      false,
		},
		{
			name:         "override with different case is preserved verbatim (no normalization)",
			cfg:          &config.Config{Reviewer: "Some-User"},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "Some-User",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := getCurrentUserFunc
			getCurrentUserFunc = tt.fakeGetUser
			defer func() { getCurrentUserFunc = orig }()

			got, err := ResolveReviewer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveReviewer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantReviewer {
				t.Errorf("ResolveReviewer() = %q, want %q", got, tt.wantReviewer)
			}
		})
	}
}
```

#### 5c. `internal/github/pr_test.go` — matching semantics (verify, extend only if needed)

`TestIsReviewRequestedFor` and `TestParsePRViewTeamReviewer` already exercise
login/slug/name matching case-insensitively (confirmed by reading the file).
These are sufficient for acceptance criterion 6's "`IsReviewRequestedFor`
matching the resolved login" requirement — **no changes required** to this
file. The builder should not duplicate these cases; instead, the new
`reviewer_test.go` (5b) and `client_test.go` (5a) cover the *resolution*
side, and the existing `pr_test.go` already covers the *matching* side. If
during implementation a genuinely new matching scenario surfaces (e.g. a
resolved login that happens to contain characters not covered by existing
fixtures), add it to `pr_test.go` following the existing table shape —
otherwise leave this file untouched.

#### 5d. `internal/config/config_test.go` — reviewer field load

Add one test (or one subtest to an existing table if a generic "field
loads correctly" table exists — none currently does, so add a standalone
test):

```go
func TestLoad_ReviewerField(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		wantReviewer  string
	}{
		{
			name: "reviewer not specified defaults to empty string",
			configContent: `repo: test/repo
label: test-label`,
			wantReviewer: "",
		},
		{
			name: "reviewer explicitly set",
			configContent: `repo: test/repo
label: test-label
reviewer: some-github-login`,
			wantReviewer: "some-github-login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configDir := tmpDir + string(os.PathSeparator) + ".howmux"
			if err := os.Mkdir(configDir, 0755); err != nil {
				t.Fatalf("Failed to create config dir: %v", err)
			}
			configFile := configDir + string(os.PathSeparator) + "config.yaml"
			if err := os.WriteFile(configFile, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldDir); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			})
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Reviewer != tt.wantReviewer {
				t.Errorf("Reviewer = %q, want %q", cfg.Reviewer, tt.wantReviewer)
			}
		})
	}
}
```

Follow the exact `os.Getwd`/`t.Cleanup`/`os.Chdir` scaffolding already used
in `TestLoad_EnableCopilotReviewDefaults` in the same file (read in full
before writing this test to match import list and helper usage exactly —
only `os`, `testing`, `time` are currently imported; `time` may be unused by
this new test alone but is already used elsewhere in the file, so no import
changes needed).

## Team Orchestration / Task Breakdown

All tasks contribute to one PR. Tasks 1 and 2 have no dependency on each
other and can run in parallel. Task 3 depends on Task 1 (needs
`github.GetCurrentUser` to exist) and Task 2 (needs `cfg.Reviewer` to exist).
Task 4 depends on Task 3. Task 5 (tests) can be written alongside each
corresponding implementation task by the same builder, or as a final pass —
no separate parallelization benefit either way since each test file is
tightly coupled to its implementation file.

### Task 1: Add `github.GetCurrentUser()` with injectable exec seam
**Files**: `internal/github/client.go`, `internal/github/client_test.go` (new)
**Acceptance Criteria**:
- `execCurrentUserFunc` package-level var added, wrapping
  `exec.Command("gh", "api", "user", "--jq", ".login")`
- `GetCurrentUser() (string, error)` added, calling `execCurrentUserFunc`,
  trimming whitespace, returning an error on exec failure or empty/blank
  result
- `client_test.go` created with the 5 table cases from section 5a, restoring
  `execCurrentUserFunc` via `defer` in every subtest
- No real `gh` invocation occurs in tests (verify by running tests with
  `gh` temporarily removed from `PATH`, or by inspection: no test path
  reaches the real `exec.Command` closure)
- `go build ./internal/github/...` succeeds; `go vet ./internal/github/...` clean
**Dependencies**: None (parallel with Task 2)

### Task 2: Add `Reviewer` field to `Config`
**Files**: `internal/config/config.go`, `internal/config/config_test.go`
**Acceptance Criteria**:
- `Reviewer string \`yaml:"reviewer"\`` field added to the `Config` struct
  (placement: after `EnableCopilotReview`, before `Session`, per the layout
  shown in section 2)
- No default value added in `Load()`'s defaults literal (zero value `""` is
  correct/intentional)
- No new validation added to `Load()` for this field (empty is valid)
- `TestLoad_ReviewerField` added per section 5d covering both the
  unset-defaults-to-empty and explicitly-set cases
- `go build ./internal/config/...` succeeds
**Dependencies**: None (parallel with Task 1)

### Task 3: Add `tui.ResolveReviewer` precedence helper
**Files**: `internal/tui/reviewer.go` (new), `internal/tui/reviewer_test.go` (new)
**Acceptance Criteria**:
- `getCurrentUserFunc` package-level var added, assigned to
  `github.GetCurrentUser`
- `ResolveReviewer(cfg *config.Config) (string, error)` added implementing
  exactly the precedence in section 3: non-empty `cfg.Reviewer` wins
  unconditionally (even if gh resolution would also succeed/fail); otherwise
  call `getCurrentUserFunc()` and return its result or a wrapped error
- Error message text includes guidance to set `reviewer:` in
  `.howmux/config.yaml` (matches section 3's exact string, or equivalent
  guidance — exact wording not load-bearing for tests, but must mention the
  config field name and that gh resolution failed)
- `reviewer_test.go` created with the 5 table cases from section 5b,
  restoring `getCurrentUserFunc` via `defer` in every subtest
- No real `gh` invocation occurs in these tests
- `go build ./internal/tui/...` succeeds
**Dependencies**: Task 1 (needs `github.GetCurrentUser` to exist to assign
as the default), Task 2 (needs `config.Config.Reviewer` to exist)

### Task 4: Wire `ResolveReviewer` into `tui.go`'s watcher construction
**Files**: `internal/tui/tui.go`
**Acceptance Criteria**:
- The line `reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)`
  is replaced exactly as shown in section 4: call `ResolveReviewer(cfg)`
  first, log via `logging.Error` on failure, pass the resolved (possibly
  empty-on-failure) `reviewer` string into `review.NewWatcher(...)`
  — **verification**: `grep -n "review.NewWatcher(" internal/tui/tui.go`
  shows the 4th argument is a variable named `reviewer` (or equivalent),
  never `cfg.Repo`
- **Verification**: `grep -rn "review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)" internal/` returns zero matches after the change
- No new variable name collides with an existing `err` in the same scope in
  `newModel()` — if a collision exists at implementation time, use a
  distinct name (e.g. `reviewerErr`) instead of `err`
- `decideReviewAction`, `internal/review/decision.go`, and
  `internal/review/decision_test.go` are **not modified**
  — **verification**: `git diff --stat` (or equivalent) shows no changes
  under `internal/review/decision*.go`
- `go build ./...` succeeds; the TUI still starts (manual smoke: `go run ./cmd/howmux` or existing build/run task, confirming no panic at startup when `.howmux/config.yaml` has no `reviewer:` field and `gh` is authenticated)
**Dependencies**: Task 3

### Task 5: Full verification pass
**Files**: none (verification only)
**Acceptance Criteria**:
- `go build ./...` succeeds repo-wide
- `go vet ./...` clean
- `go test ./internal/github/... ./internal/config/... ./internal/tui/... ./internal/review/... -race` all pass
- `go test ./internal/review/... -race` specifically confirms
  `decideReviewAction`/watcher tests are unaffected (no regressions from the
  identity-source change, since `decideReviewAction` itself is untouched)
- Repo-wide check that no other call site was missed:
  `grep -rn "review.NewWatcher(" --include="*.go" .` shows exactly one
  production call site (in `tui.go`, now using the resolved `reviewer`
  variable) plus any pre-existing test call sites (unaffected, since they
  already pass literal strings)
**Dependencies**: Tasks 1–4 complete

## Validation Commands

```bash
# Build
go build ./...
go vet ./...

# Targeted tests
go test ./internal/github/... -run TestGetCurrentUser -v
go test ./internal/tui/... -run TestResolveReviewer -v
go test ./internal/config/... -run TestLoad_ReviewerField -v

# Full suite with race detector (constraint: -race for watcher-touching tests)
go test ./... -race

# Confirm the buggy call site is gone
grep -rn "review.NewWatcher(reviewStore, reviewPollInterval, 2, cfg.Repo)" internal/ || echo "OK: old call site removed"

# Confirm decideReviewAction / decision.go untouched (run before/after in a
# clean git worktree diff, or rely on the task-level git diff check)
git diff --stat -- internal/review/decision.go internal/review/decision_test.go
```

## Acceptance Criteria Traceability

| Issue AC | Where addressed |
|---|---|
| 1. Watcher constructed with reviewer identity, not `cfg.Repo` | Task 4 |
| 2. `github.GetCurrentUser()` via gh-exec seam, testable/hermetic | Task 1 |
| 3. Explicit override via config, override wins | Task 2 (field) + Task 3 (precedence) |
| 4. Fail/warn clearly when neither available | Task 3 (`ResolveReviewer` returns error) + Task 4 (`logging.Error` at call site) |
| 5. `decideReviewAction` unchanged | Task 4 verification step; no edits proposed anywhere in this spec to `decision.go` |
| 6. Table tests for precedence + `IsReviewRequestedFor` matching, injectable seam, no real gh/network | Tasks 1, 2, 3 (sections 5a, 5b, 5d); existing `pr_test.go` already covers matching (section 5c) |
