# Design Specification: GitHub PR Queries + Parsing (internal/github)

Closes #60

## Context

Part of the PR-review workflow series (foundation issue #59). This issue adds the ability to read a PR's state and reviewer info via `gh`, structured so that **parsing is unit-tested against fixtures** and **argv is asserted** — but `gh` itself is never run in tests.

Independent of #59 (no code dependency); lands alongside it. Together they form the foundation for the PR-review workflow series.

## Solution Approach

### Core Design Principle

Split GitHub CLI interaction into **pure, testable functions** and **thin wiring**:

1. **Pure argv construction** (`prViewArgs`) — builds the exact command-line arguments for `gh pr view` with the required `--json` fields
2. **Pure parsing** (`parsePRView`) — unmarshals `gh pr view` JSON output into a structured `PR` type
3. **Pure methods on PR** — `IsTerminal()`, `IsReviewRequestedFor()`, head SHA accessor — all operate on the already-parsed struct
4. **Pure URL parsing** (`ResolvePRURL`) — extracts owner, repo, PR number from GitHub PR URLs
5. **Thin wiring** (`GetPR`) — executes `gh` with the argv from step 1, passes output to step 2

This pattern makes the actual GitHub interaction untestable (deliberately — it's a single `exec.Command` call with no logic), while making everything that *contains logic* testable against fixtures.

### Why This Pattern Works

- **Argv assertions** catch breaking changes to the `gh pr view` command invocation
- **Fixture-based parsing tests** verify the unmarshaling logic against real `gh` output, covering multiple PR states (open, merged, closed, draft, with reviewers)
- **Pure methods** are tested via table tests on constructed `PR` structs
- **URL parsing** is tested against valid/invalid GitHub URL patterns
- **No test ever calls `gh`** — all external interaction is isolated in the thin `GetPR` wrapper

### Concurrency Analysis

**No concurrency concerns** — this issue introduces pure functions and a thin wrapper that shells out to `gh`. The `PR` struct is returned by value (no shared state), and `GetPR` has no side effects beyond calling an external command. No locks needed.

### GitHub CLI JSON Output Schema

Based on `gh pr view --json state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number`, the JSON structure is:

```json
{
  "state": "OPEN|MERGED|CLOSED",
  "isDraft": true|false,
  "mergedAt": "2024-09-16T12:34:56Z" (or null),
  "closedAt": "2024-09-16T12:34:56Z" (or null),
  "headRefOid": "abc123...",
  "headRefName": "feature/branch-name",
  "reviewRequests": [
    {"login": "username1"},
    {"login": "username2"}
  ],
  "url": "https://github.com/owner/repo/pull/123",
  "number": 123
}
```

### Data Structures

```go
// PR represents a pull request's state fetched from GitHub
type PR struct {
    State          string          `json:"state"`           // "OPEN", "MERGED", "CLOSED"
    IsDraft        bool            `json:"isDraft"`         // true if draft PR
    MergedAt       *time.Time      `json:"mergedAt"`        // nil if not merged
    ClosedAt       *time.Time      `json:"closedAt"`        // nil if not closed
    HeadRefOid     string          `json:"headRefOid"`      // commit SHA
    HeadRefName    string          `json:"headRefName"`     // branch name
    ReviewRequests []ReviewRequest `json:"reviewRequests"`  // pending reviewers
    URL            string          `json:"url"`             // full PR URL
    Number         int             `json:"number"`          // PR number
}

// ReviewRequest represents a pending review request
type ReviewRequest struct {
    Login string `json:"login"` // GitHub username
}

// IsTerminal returns true if the PR is merged or closed
func (pr PR) IsTerminal() bool {
    return pr.State == "MERGED" || pr.State == "CLOSED"
}

// IsReviewRequestedFor returns true if the given login appears in reviewRequests
func (pr PR) IsReviewRequestedFor(login string) bool {
    for _, req := range pr.ReviewRequests {
        if req.Login == login {
            return true
        }
    }
    return false
}

// HeadSHA returns the commit SHA of the PR head
func (pr PR) HeadSHA() string {
    return pr.HeadRefOid
}
```

## Relevant Files

### New Files to Create

- `internal/github/pr.go` — PR struct, derived methods, `GetPR`, `prViewArgs`, `parsePRView`, `ResolvePRURL`
- `internal/github/pr_test.go` — All unit tests
- `internal/github/testdata/pr_open.json` — Fixture: open PR
- `internal/github/testdata/pr_merged.json` — Fixture: merged PR
- `internal/github/testdata/pr_closed.json` — Fixture: closed (unmerged) PR
- `internal/github/testdata/pr_draft.json` — Fixture: draft PR
- `internal/github/testdata/pr_with_reviewers.json` — Fixture: PR with specific login in reviewRequests

### Existing Files to Reference

- `internal/github/client.go` — Follow the existing style (package-level functions, exec.Command for `gh`, error wrapping)

## Team Orchestration

**No dependencies** — this issue is independent and lands alongside #59. Both are foundations for the PR-review workflow series but have no code interdependencies.

## Step-by-Step Task Breakdown

### Task 1: Create PR Type and Pure Methods

**What to build:**
- Define `PR` struct in `internal/github/pr.go`
- Define `ReviewRequest` struct
- Implement `IsTerminal()` method (checks if State is "MERGED" or "CLOSED")
- Implement `IsReviewRequestedFor(login string) bool` method (checks if login appears in ReviewRequests)
- Implement `HeadSHA()` method (returns HeadRefOid)

**Acceptance Criteria:**
1. `PR` struct matches the JSON schema from `gh pr view --json` output
2. `IsTerminal()` returns true only for "MERGED" or "CLOSED" states
3. `IsReviewRequestedFor()` correctly finds a login in the reviewRequests array
4. `HeadSHA()` returns the HeadRefOid value
5. Table tests in `pr_test.go` cover:
   - `IsTerminal()` for OPEN, MERGED, CLOSED states
   - `IsReviewRequestedFor()` with present, absent, and empty reviewRequests
   - `HeadSHA()` accessor

**Dependencies:** None

### Task 2: Implement prViewArgs (Pure Argv Construction)

**What to build:**
- Create `prViewArgs(repo string, pr int) []string` function
- Returns the exact argv slice for `gh pr view <pr> --repo <repo> --json <fields>`
- Fields: `state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number`

**Acceptance Criteria:**
1. Function is pure (deterministic, no side effects)
2. Returns slice in correct order: `["pr", "view", "<pr>", "--repo", "<repo>", "--json", "<field-list>"]`
3. Unit test in `pr_test.go` asserts exact argument slice for a known input (e.g., repo="owner/name", pr=123)
4. Field list is exactly: `state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number`

**Dependencies:** None

### Task 3: Implement parsePRView (Pure JSON Parsing)

**What to build:**
- Create `parsePRView(data []byte) (PR, error)` function
- Unmarshals JSON from `gh pr view` into `PR` struct
- Returns error if JSON is malformed or missing required fields

**Acceptance Criteria:**
1. Function is pure (deterministic, no side effects)
2. Successfully unmarshals valid JSON into `PR` struct
3. Handles null values for `mergedAt`, `closedAt` (uses `*time.Time`)
4. Returns descriptive error for malformed JSON
5. Table tests in `pr_test.go` using fixtures:
   - `testdata/pr_open.json` — OPEN state, no mergedAt/closedAt
   - `testdata/pr_merged.json` — MERGED state, has mergedAt
   - `testdata/pr_closed.json` — CLOSED state, has closedAt, no mergedAt
   - `testdata/pr_draft.json` — OPEN + isDraft=true
   - `testdata/pr_with_reviewers.json` — has reviewRequests with at least one login
6. Each table test verifies the parsed PR struct's fields match expected values

**Dependencies:** Task 1 (PR struct must exist)

### Task 4: Create Testdata Fixtures

**What to build:**
- Capture real `gh pr view --json` output and save as fixtures
- Create 5 fixture files under `internal/github/testdata/`:
  - `pr_open.json` — Open PR, not draft, no reviewers
  - `pr_merged.json` — Merged PR with mergedAt timestamp
  - `pr_closed.json` — Closed but not merged PR with closedAt timestamp
  - `pr_draft.json` — Open draft PR
  - `pr_with_reviewers.json` — Open PR with at least one login in reviewRequests

**Acceptance Criteria:**
1. All fixtures are valid JSON matching the `gh pr view` schema
2. Each fixture represents a distinct PR state
3. Fixtures are committed to the repository
4. `pr_with_reviewers.json` has at least one entry in reviewRequests array with a known login (e.g., "testuser")
5. All timestamp fields (mergedAt, closedAt) are valid RFC3339 strings or null

**How to capture fixtures:**
```bash
# From a real repository with PRs in different states:
gh pr view 123 --repo owner/repo --json state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number > internal/github/testdata/pr_open.json
# Repeat for PRs in merged, closed, draft, and with-reviewers states
```

**Dependencies:** None (can be done in parallel with code tasks)

### Task 5: Implement GetPR (Thin Wiring)

**What to build:**
- Create `GetPR(repo string, pr int) (PR, error)` function
- Calls `exec.Command("gh", prViewArgs(repo, pr)...)`
- Passes stdout to `parsePRView`
- Returns parsed `PR` or error

**Acceptance Criteria:**
1. Function combines `prViewArgs` + `exec.Command` + `parsePRView`
2. Returns error if `gh` command fails (with wrapped error message)
3. Returns error if parsing fails (with wrapped error message)
4. Error messages include repo and PR number for debugging
5. Follow existing `internal/github/client.go` patterns for error wrapping (use `fmt.Errorf`)
6. **This function is NOT unit tested** (it's the thin wiring that calls external `gh`)

**Dependencies:** Task 2 (prViewArgs), Task 3 (parsePRView)

### Task 6: Implement ResolvePRURL (Pure URL Parsing)

**What to build:**
- Create `ResolvePRURL(url string) (owner, repo string, pr int, err error)` function
- Parses GitHub PR URLs in the form:
  - `https://github.com/owner/repo/pull/123`
  - `http://github.com/owner/repo/pull/123`
- Extracts owner, repo name, and PR number
- Returns error for malformed URLs

**Acceptance Criteria:**
1. Function is pure (deterministic, no side effects)
2. Successfully parses valid GitHub PR URLs
3. Returns all three parts: owner, repo, pr number
4. Returns error for malformed URLs (not matching GitHub PR pattern)
5. Table tests in `pr_test.go` cover:
   - Valid HTTPS URL: `https://github.com/owner/repo/pull/123`
   - Valid HTTP URL: `http://github.com/owner/repo/pull/123`
   - Repo name with hyphens/underscores: `https://github.com/my-org/my_repo/pull/456`
   - Invalid URL (no /pull/): `https://github.com/owner/repo/issues/123` → error
   - Invalid URL (malformed): `not-a-url` → error
   - Invalid URL (missing PR number): `https://github.com/owner/repo/pull/` → error
6. Error messages are descriptive (e.g., "invalid GitHub PR URL format")

**Dependencies:** None

### Task 7: Integration Test (No gh Execution)

**What to build:**
- Create `TestPRWorkflow` in `pr_test.go` that demonstrates the full workflow **without calling gh**:
  1. Call `prViewArgs("owner/name", 123)` to get argv
  2. Verify argv is correct
  3. Simulate `gh pr view` output by reading `testdata/pr_open.json`
  4. Call `parsePRView` with the fixture data
  5. Verify parsed PR has expected values
  6. Call derived methods (IsTerminal, IsReviewRequestedFor, HeadSHA) on the parsed PR

**Acceptance Criteria:**
1. Test demonstrates the intended workflow from argv construction to parsing to derived methods
2. Test uses fixtures (no `gh` execution)
3. Test asserts correct argv, parsed struct, and method results
4. Test serves as documentation for how other code should use these functions

**Dependencies:** All previous tasks

## Validation Commands

```bash
# Run all tests in internal/github
go test ./internal/github -v

# Run tests with coverage
go test ./internal/github -cover

# Verify no external gh calls in tests (should complete instantly)
go test ./internal/github -v -timeout 1s

# Verify fixtures exist
ls internal/github/testdata/*.json

# Verify all table tests pass
go test ./internal/github -v -run TestParsePRView
go test ./internal/github -v -run TestPRViewArgs
go test ./internal/github -v -run TestResolvePRURL
go test ./internal/github -v -run TestPRMethods

# Verify the PR type matches gh output schema (manual check):
# gh pr view <some-pr> --json state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number
# Compare output to PR struct fields

# Full project build and test to ensure no integration issues
go build ./...
go test ./...
```

## Test Coverage Requirements

All functions except `GetPR` (the thin wiring) must be unit tested:

- ✅ `prViewArgs` — assert exact argv slice
- ✅ `parsePRView` — table test with 5 fixtures
- ✅ `PR.IsTerminal()` — table test with OPEN, MERGED, CLOSED
- ✅ `PR.IsReviewRequestedFor()` — table test with present, absent, empty
- ✅ `PR.HeadSHA()` — simple assertion test
- ✅ `ResolvePRURL` — table test with valid/invalid URLs
- ❌ `GetPR` — NOT tested (thin wiring, no logic)

## Style Consistency

Follow `internal/github/client.go` patterns:

- Package-level functions (not methods on a client struct)
- Use `exec.Command("gh", ...)` for GitHub CLI calls
- Wrap errors with `fmt.Errorf` including context (repo, PR number)
- Use `json.Unmarshal` for parsing JSON output
- Define structs with JSON tags matching `gh` output

## Example Usage (Post-Implementation)

```go
// Parse a GitHub PR URL
owner, repo, prNum, err := github.ResolvePRURL("https://github.com/owner/name/pull/123")
if err != nil {
    return fmt.Errorf("invalid PR URL: %w", err)
}

// Fetch PR state from GitHub
pr, err := github.GetPR(fmt.Sprintf("%s/%s", owner, repo), prNum)
if err != nil {
    return fmt.Errorf("failed to fetch PR: %w", err)
}

// Check PR state
if pr.IsTerminal() {
    fmt.Println("PR is already merged or closed")
    return
}

// Check if review is requested for a specific user
if pr.IsReviewRequestedFor("myusername") {
    fmt.Println("Review requested for myusername")
}

// Get current commit SHA
fmt.Printf("PR head SHA: %s\n", pr.HeadSHA())
```

## Notes

- This issue introduces **no new concurrency** — all functions are pure or execute synchronously
- The separation between pure functions (testable) and thin wiring (untestable but trivial) is intentional and follows the project's testing philosophy
- Fixtures must be captured from real `gh` output to ensure schema accuracy
- The `GetPR` function is the only part that touches the network/external command, and it contains no logic (just wiring)
