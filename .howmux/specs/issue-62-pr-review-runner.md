# Design Specification: PR Review Runner (ACP one-shot orchestrator + auto-post + artifacts)

**Issue**: #62  
**Title**: PR-review workflow: review runner (ACP one-shot orchestrator + auto-post + artifacts)  
**Closes**: #62

## Context

Part of the PR-review workflow series. Given a checked-out PR directory, run the existing review pipeline (the valkey/cookbook review agents) which self-evaluates to a minimum bar and **auto-posts** to the PR; then record the reviewed SHA + timestamp and persist the review artifacts.

Review **quality / prompt content is out of scope** — that is covered by existing evals. This issue only ensures howmux **invokes the pipeline correctly** and manages the surrounding state/artifacts.

**Dependencies**: Blocked by #59 (state store) and #61 (checkout) — both complete.

## ACP Boundary (Decided)

- **ACP everywhere, one-shot per review.** For each review: connect over ACP, `NewSession` with `cwd` = the PR's checkout dir, send **one** prompt to a single **review-orchestrator agent** (same shape as howmux prompts `krew-lead` today), stream to completion, then `Close()`. No resident process per PR between polls.
- howmux's only ACP hop is howmux ↔ orchestrator. Whatever the orchestrator does internally (fan-out to review sub-agents, and however sub-agents communicate) is **outside howmux and not over ACP** — howmux neither knows nor cares.
- Do **not** use the `kiro-cli chat --no-interactive` path; inter-agent comms go through ACP.

## Context Continuity Across Re-reviews (Decided)

- **Non-goal:** resuming an ACP session across reviews. howmux's ACP client mints a fresh session per connect and has no resume-by-id path; do not try to resume.
- **Instead:** persist each review's products into the PR's state folder (`.howmux/reviews/<owner>-<repo>-<pr>/reviews/<sha>.md`). On a re-review, **load prior review artifacts and include them in the new prompt** ("here is your previous review at SHA X; the author pushed changes and re-requested review; focus on whether prior findings were addressed"). Continuity comes from persisted artifacts fed forward, not from a live session.

## What to Build

A runner that, given a record + checked-out dir, opens a one-shot ACP session and prompts the review orchestrator; streams output (surfaced in the TUI like issue agents).

- `reviewPromptContext(...)` / artifact loading — **pure**; assembles the prompt context from any prior `reviews/*.md` for this PR.
- On completion: write `reviews/<sha>.md`, and update the record's `last_reviewed_sha`, `last_reviewed_at`, `last_serviced_request`, `status`.

## Known Risk Resolution

The issue raises the question: does a single **review-orchestrator agent** exist that is ACP-promptable the way `krew-lead` is? If the review flow currently only exists as external script orchestration (`run-pr-review.sh` / `workflows/`), surfacing/creating an ACP-promptable orchestrator is a design decision.

**Design Decision**: For this implementation, we will use **krew-lead** as the review orchestrator agent. The krew-lead agent config already exists and is ACP-compatible. The prompt will be structured to request a PR review from krew-lead, which can delegate to review sub-agents via its existing `subagent` tool capability.

If a dedicated review-orchestrator agent is needed in the future, that will be a separate issue. This design focuses on the runner infrastructure and state management, using the existing krew-lead agent as the orchestration entry point.

## Solution Approach

Create a new `internal/review/runner.go` file with:

1. **Pure prompt-context assembly function** (`reviewPromptContext`) that loads prior review artifacts from `.howmux/reviews/<owner>-<repo>-<pr>/reviews/` and constructs the prompt text for the orchestrator
2. **ACP session management** (`runReview`) that:
   - Creates an ACP client with `cwd` = the PR's checkout directory
   - Connects, creates a session, sends the prompt via `StreamMessage`
   - Captures streaming output (text chunks)
   - Writes the final review artifact to `reviews/<sha>.md`
   - Updates the record with reviewed SHA, timestamp, and status
3. **Injectable seams for external operations** (per #70):
   - ACP client creation/connection
   - File I/O (reading prior artifacts, writing new ones)
   - Time/timestamp generation
   - Record update (via store interface)

All subprocess/network/filesystem operations route through injectable seams so unit tests can verify dispatch logic, prompt assembly, and artifact persistence without real external processes.

## Relevant Files

### Files to Create

| File | Purpose |
|------|---------|
| `internal/review/runner.go` | Review runner implementation with ACP orchestration |
| `internal/review/runner_test.go` | Unit tests for prompt context assembly, artifact persistence, injectable seams |

### Files to Reference (Read-Only)

| File | Purpose |
|------|---------|
| `internal/acp/client.go` | ACP client interface and connection management |
| `internal/acp/types.go` | ACP types (ConnectionConfig, MessageRequest, StreamingResponse) |
| `internal/review/store.go` | PR state store (Save, Get methods) |
| `internal/review/types.go` | Record type with status, SHA, timestamps |
| `internal/review/checkout.go` | Example of injectable seam pattern (runCommand var) |
| `internal/review/checkout_test.go` | Example of testing wiring logic via fake runner |
| `internal/agent/manager.go` | Reference for ACP session creation pattern |

## Team Orchestration

This is a **single-task implementation** with no parallelization opportunities:

1. Implement the runner with pure functions + injectable seams
2. Write comprehensive unit tests (table tests for prompt context assembly, fake seams for wiring)

All work contributes to complete issue resolution in one PR.

## Step-by-Step Task Breakdown

### Task 1: Implement Pure Prompt Context Assembly

**Objective**: Create the pure function that assembles the review prompt from prior artifacts.

**Steps**:

1. Create `internal/review/runner.go`
2. Implement `reviewPromptContext(owner, repo string, pr int, currentSHA string, priorReviews []string) string`:
   - Takes the PR identifier, current SHA, and a slice of prior review markdown content
   - Returns the assembled prompt text for the orchestrator
   - Pure function: no I/O, deterministic output from inputs
3. Prompt structure:
   - Base: "Review PR #{pr} from {owner}/{repo} at commit {currentSHA}. This is a PR review task."
   - If no prior reviews: "This is the first review of this PR."
   - If prior reviews exist: "Prior reviews exist. Here are the previous findings to consider:\n\n{prior review content}\n\nThe author has pushed new changes. Focus on whether prior findings were addressed."
4. Add package documentation explaining the review runner's role

**Acceptance Criteria**:
- `reviewPromptContext` is a pure function (no I/O, no state mutation)
- Prompt assembly handles zero prior reviews correctly
- Prompt assembly handles one or more prior reviews correctly
- Prompt includes PR identifier (owner/repo/pr), current SHA, and prior content when present
- **Verification**: Unit test in `runner_test.go` with table-driven test cases covering:
  - Zero prior reviews → base prompt only
  - One prior review → base prompt + prior review content
  - Multiple prior reviews → base prompt + all prior reviews
  - All inputs (owner, repo, pr, SHA, prior content) appear in output

**Dependencies**: None

---

### Task 2: Implement Artifact Loading Function

**Objective**: Create the function that loads prior review artifacts from disk.

**Steps**:

1. In `internal/review/runner.go`, implement `loadPriorReviews(baseDir, owner, repo string, pr int) ([]string, error)`:
   - Constructs the path to the PR's reviews directory: `{baseDir}/{owner}-{repo}-{pr}/reviews/`
   - Reads all `*.md` files in that directory
   - Returns slice of file contents (one string per file)
   - Returns empty slice (not error) if directory doesn't exist
   - Returns error only on I/O failure reading existing files
2. Make this function injectable via a package-level var for testing:
   ```go
   var loadPriorReviewsFunc = loadPriorReviews
   ```

**Acceptance Criteria**:
- `loadPriorReviews` reads all `*.md` files from the reviews directory
- Returns empty slice if directory doesn't exist (not an error)
- Returns error on I/O failure
- Function is injectable via package var for testing
- **Verification**: Unit test with `t.TempDir()`:
  - Empty directory → empty slice, no error
  - Directory with 2 review files → slice with 2 content strings
  - Directory doesn't exist → empty slice, no error
  - Unreadable file → error returned

**Dependencies**: Task 1 (conceptual, can run in parallel)

---

### Task 3: Implement Review Artifact Persistence

**Objective**: Write the reviewed artifact to disk after review completion.

**Steps**:

1. In `internal/review/runner.go`, implement `saveReviewArtifact(baseDir, owner, repo string, pr int, sha string, content string) error`:
   - Constructs the path: `{baseDir}/{owner}-{repo}-{pr}/reviews/{sha}.md`
   - Ensures the reviews directory exists (MkdirAll)
   - Writes content to `{sha}.md` atomically (temp file + rename, same pattern as store.go)
   - Returns error on write failure
2. Make this function injectable via package-level var:
   ```go
   var saveReviewArtifactFunc = saveReviewArtifact
   ```

**Acceptance Criteria**:
- `saveReviewArtifact` creates the reviews directory if missing
- Writes artifact atomically (temp + rename)
- File is named `{sha}.md`
- Returns error on write failure
- Function is injectable via package var for testing
- **Verification**: Unit test with `t.TempDir()`:
  - Writing to non-existent directory → creates directory + file
  - Writing artifact → file exists with correct content
  - Reading artifact back → content matches input
  - Concurrent writes to different SHAs → both succeed

**Dependencies**: Task 1 (conceptual, can run in parallel)

---

### Task 4: Implement ACP Session Management for Review

**Objective**: Create the main review runner that orchestrates the ACP session, prompt, and artifact persistence.

**Steps**:

1. Define injectable interfaces/function vars for external operations:
   ```go
   // ACPClientFactory creates an ACP client
   type ACPClientFactory func(config *acp.ConnectionConfig) acp.Client
   var defaultACPClientFactory ACPClientFactory = func(config *acp.ConnectionConfig) acp.Client {
       return acp.NewClient(config)
   }

   // TimeNow returns current time (injectable for testing)
   var timeNow = time.Now
   ```

2. Implement `RunReview(ctx context.Context, rec review.Record, baseDir, currentSHA string, storeImpl *review.Store) error`:
   - Validates inputs (record, baseDir, SHA)
   - Loads prior reviews via `loadPriorReviewsFunc`
   - Assembles prompt via `reviewPromptContext`
   - Creates ACP client with config:
     - KiroCLIPath: "kiro-cli"
     - Agent: "krew-lead"
     - Cwd: rec.ReviewDir (the PR's checkout directory)
     - Timeout: 10 minutes
   - Connects to ACP via `client.Connect(ctx)`
   - Sends prompt via `client.StreamMessage(ctx, &acp.MessageRequest{...})`
   - Collects streaming response chunks into a buffer
   - On completion (receives "done" event):
     - Writes artifact via `saveReviewArtifactFunc`
     - Updates record:
       - last_reviewed_sha = currentSHA
       - last_reviewed_at = timeNow().Format(time.RFC3339)
       - last_serviced_request = currentSHA
       - status = StatusWatching (ready for next poll)
     - Saves updated record via `storeImpl.Save(rec)`
   - Disconnects ACP client
   - Returns accumulated error if any step fails

3. Add parameter for injectable client factory so tests can substitute a fake ACP client

**Acceptance Criteria**:
- `RunReview` validates all inputs before starting
- Loads prior reviews via injectable function
- Creates ACP client with correct config (agent="krew-lead", cwd=reviewDir)
- Sends correct prompt via StreamMessage
- Collects all streaming response chunks
- Writes artifact with correct SHA filename
- Updates record with reviewed SHA, timestamp, and status
- Saves updated record to store
- All external operations (ACP client, file I/O, time, store) use injectable seams
- Returns error if any step fails (connection, prompt, artifact write, store update)
- **Verification**: Unit tests with fake seams:
  - Successful review → artifact written, record updated, no error
  - ACP connection failure → error returned, no artifact written
  - Prompt send failure → error returned, no artifact written
  - Artifact write failure → error returned, record not updated
  - Store update failure → error returned (artifact written but state inconsistent)

**Dependencies**: Tasks 1, 2, 3 (all prior functions must exist)

---

### Task 5: Write Comprehensive Unit Tests

**Objective**: Verify all logic paths via unit tests with no real external processes.

**Steps**:

1. Create `internal/review/runner_test.go`
2. Implement table-driven tests for `reviewPromptContext`:
   - Test cases:
     - Zero prior reviews
     - One prior review
     - Multiple prior reviews
     - All inputs appear in output
   - Assert exact prompt structure
3. Implement tests for `loadPriorReviews` with `t.TempDir()`:
   - Empty directory
   - Directory with review files
   - Non-existent directory
   - Unreadable file (simulate I/O error)
4. Implement tests for `saveReviewArtifact` with `t.TempDir()`:
   - Fresh directory (creates dirs + file)
   - Existing directory (writes file)
   - Content round-trip (write then read)
   - Concurrent writes to different SHAs
5. Implement integration tests for `RunReview` with fake seams:
   - Fake ACP client that captures prompt and returns fake streaming responses
   - Fake file I/O that records writes
   - Fake time that returns fixed timestamp
   - Fake store that records Save calls
   - Test cases:
     - Successful review end-to-end
     - ACP connection failure
     - Prompt send failure
     - Artifact write failure
     - Store update failure
   - Assert: correct prompt assembled, artifact written to correct path with correct content, record updated with correct fields

**Acceptance Criteria**:
- All pure functions (reviewPromptContext) have table-driven tests
- All I/O functions (loadPriorReviews, saveReviewArtifact) have tempdir-based tests
- Main `RunReview` has integration tests with all seams faked
- Tests assert:
  - Prompt content correctness
  - ACP client config (agent, cwd, timeout)
  - Artifact file path and content
  - Record field updates (SHA, timestamp, status)
  - Store.Save call with updated record
- **No real `kiro-cli`, `gh`, `git`, or network in tests**
- **Verification**: `go test ./internal/review/... -v` passes with 100% coverage of runner.go logic paths

**Dependencies**: Task 4 (must have implementation to test against)

---

## Validation Commands

After completing all tasks, run the following validation commands:

### 1. Run Unit Tests

```bash
go test ./internal/review/... -v -race
```

**Expected**: All tests pass, no race conditions detected.

### 2. Check Test Coverage

```bash
go test ./internal/review/... -coverprofile=coverage.out
go tool cover -func=coverage.out | grep runner.go
```

**Expected**: `runner.go` has >90% coverage (all logic paths tested).

### 3. Verify No External Dependencies in Tests

```bash
# Check that tests don't invoke external processes
grep -r "exec.Command\|os.Exec" internal/review/runner_test.go

# Should return no matches (tests use fakes, not real processes)
```

**Expected**: No matches (or only fake/mock implementations).

### 4. Standard QA

```bash
task test     # Run all project tests
task lint     # Run linters
task fmt      # Format code
```

**Expected**: All QA commands pass.

### 5. Validate Against #70 Convention

```bash
# Verify injectable seams exist
grep -n "var.*Func\|type.*Factory" internal/review/runner.go
```

**Expected**: Injectable seams declared for:
- `loadPriorReviewsFunc`
- `saveReviewArtifactFunc`
- `timeNow`
- `defaultACPClientFactory`

## Concurrency Considerations

**No cross-goroutine shared state** is introduced by this issue. The runner operates in a request-response pattern:

1. Caller invokes `RunReview` with a record
2. Runner performs I/O (load artifacts, ACP session, write artifact, update store)
3. Runner returns control to caller

The runner does **not**:
- Spawn background goroutines that outlive the function call
- Share state across multiple RunReview invocations
- Mutate shared state from the ACP streaming callback

The ACP client's streaming channel is consumed synchronously within the RunReview call, and the accumulated text is local to that call.

**Future watch loop consideration**: When issue #64 integrates this runner, the watch loop will need to ensure that concurrent reviews of the *same* PR don't occur (e.g., via a per-PR mutex or a single-threaded dispatch queue). That is out of scope for this issue.

## Implementation Notes

### Injectable Seam Pattern (Per #70)

This implementation follows the injectable seam pattern documented in `.kiro/skills/builder-conventions/SKILL.md`:

- All external operations (ACP client creation, file I/O, time, store) route through package-level function variables or interface parameters
- Production code uses real implementations as defaults
- Tests substitute fakes/mocks that record calls and return controlled responses
- The wiring logic (branching, error handling, call sequence) is fully unit-tested with no real external process

Example seam declaration:
```go
var loadPriorReviewsFunc = loadPriorReviews
var saveReviewArtifactFunc = saveReviewArtifact
var timeNow = time.Now
var defaultACPClientFactory ACPClientFactory = func(config *acp.ConnectionConfig) acp.Client {
    return acp.NewClient(config)
}
```

Example test seam substitution:
```go
func TestRunReview_Success(t *testing.T) {
    // Save originals
    origLoad := loadPriorReviewsFunc
    origSave := saveReviewArtifactFunc
    origTime := timeNow
    t.Cleanup(func() {
        loadPriorReviewsFunc = origLoad
        saveReviewArtifactFunc = origSave
        timeNow = origTime
    })

    // Substitute fakes
    loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
        return []string{"prior review content"}, nil
    }
    var savedArtifact string
    saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
        savedArtifact = content
        return nil
    }
    timeNow = func() time.Time {
        return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
    }

    // Test RunReview and assert savedArtifact contains expected content
}
```

### ACP Client Configuration

The runner creates an ACP client with:
- `Agent`: "krew-lead" (existing orchestrator agent)
- `Cwd`: The PR's checkout directory from `rec.ReviewDir`
- `RequestTimeout`: 10 minutes (reviews can be lengthy)
- `ConnectionTimeout`: 30 seconds
- `MaxRetries`: 3

The krew-lead agent will receive a prompt structured as a review request and can delegate to review sub-agents via its `subagent` tool.

### Artifact File Naming

Review artifacts are named `{sha}.md` where `sha` is the commit SHA at review time. This allows:
- Multiple reviews of the same PR (different SHAs) to coexist
- Prior review lookup by SHA if needed
- Natural deduplication (same SHA = same review, don't re-review)

### Error Handling Strategy

The runner returns an error if any step fails, but attempts to leave the system in a consistent state:
- If artifact write fails, the record is NOT updated (no orphaned record pointing to a missing artifact)
- If record update fails, the artifact IS written (safe: artifact exists but record is stale; next review will overwrite)
- If ACP connection fails, no artifact or record update occurs (no partial state)

## Success Criteria

The implementation is complete when:

1. ✅ `reviewPromptContext` is a pure, tested function
2. ✅ `loadPriorReviews` loads artifacts from disk with tempdir tests
3. ✅ `saveReviewArtifact` writes artifacts atomically with tempdir tests
4. ✅ `RunReview` orchestrates ACP session, artifact persistence, and record update
5. ✅ All external operations use injectable seams (per #70)
6. ✅ Unit tests cover all logic paths with no real external processes
7. ✅ Test coverage >90% for runner.go
8. ✅ All tests pass with `-race` flag (no race conditions)
9. ✅ PR created with title "Add PR review runner (ACP one-shot + artifacts)"

## Quality Assurance

### Pre-Commit Checks

```bash
go test ./internal/review/... -v -race -coverprofile=coverage.out
go tool cover -func=coverage.out
task lint
task fmt
```

### Integration Readiness

After this issue, the runner is ready for integration with issue #64 (watch loop). The watch loop will:
1. Decide which PRs need review (via pure decision function)
2. Ensure checkout (via #61)
3. Invoke `RunReview` (this issue)
4. Prune completed PRs

This runner provides the missing orchestration layer between PR state and the review orchestrator agent.

## References

- **Issue #62**: https://github.com/matthiashowellyopp/howmux/issues/62
- **Issue #59** (state store): https://github.com/matthiashowellyopp/howmux/issues/59
- **Issue #61** (checkout): https://github.com/matthiashowellyopp/howmux/issues/61
- **Issue #64** (watch loop): https://github.com/matthiashowellyopp/howmux/issues/64
- **Issue #70** (I/O seam convention): https://github.com/matthiashowellyopp/howmux/issues/70
- **Reference**: `internal/review/checkout.go` (injectable seam pattern)
- **Reference**: `internal/acp/client.go` (ACP client interface)
- **Reference**: `internal/agent/manager.go` (ACP session creation pattern)
