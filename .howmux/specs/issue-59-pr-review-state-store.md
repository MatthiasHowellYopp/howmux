# Design Specification: PR-review State Store

**Issue:** Closes #59  
**Created:** 2026-09-16  
**Author:** architect agent

## Context

This is the first piece of a new PR-review workflow for howmux (an 8-issue series). This specification covers **only the persistence layer** — a pure, unit-testable data layer that stores PR review state on the filesystem. No GitHub calls, no network operations, no git operations.

The broader workflow (for context, not this issue's scope): a `review <PR_URL>` command reviews a PR now and enrolls it in a recurring loop that re-reviews when a GitHub review re-request arrives, auto-posts the review, and prunes the PR when it merges/closes. This persistence layer is the foundation.

## Solution Approach

### High-Level Strategy

Create a new package `internal/review` that stores PR review state as **one folder per watched PR** under `.howmux/reviews/`. The folder-per-PR approach (vs. a single shared file) ensures:
- No file contention when concurrent reviews run
- Each PR has a home for accumulated review artifacts
- Clean isolation and easy per-PR cleanup

The package will follow patterns established in the codebase:
- Atomic writes (temp file + rename) like `internal/session/manager.go`
- JSON marshaling patterns like `internal/session/types.go`
- Pure filesystem operations testable with `t.TempDir()` like `internal/session/manager_test.go`
- Directory-based storage like `.howmux/sessions/` and `.howmux/retries/`

### Architecture Decisions

1. **Folder-per-PR layout**: Each PR gets `<owner>-<repo>-<pr>/` subdirectory
2. **Atomic writes**: Use temp file + rename to prevent corruption on crashes
3. **Pure functions**: Filename/dirname derivation is stateless and unit-testable
4. **No external dependencies**: Zero network, GitHub CLI, or git operations
5. **Extensible structure**: `reviews/` subdirectory ready for future artifacts

## Package Structure and Organization

```
internal/review/
├── store.go          # Store type and core CRUD operations
├── store_test.go     # Round-trip and CRUD tests
├── types.go          # Record type and JSON marshaling
├── types_test.go     # Record validation and JSON tests
└── paths.go          # Pure filename/dirname helpers
    └── paths_test.go # Pure function path derivation tests
```

### File Responsibilities

**store.go**
- `Store` type with base directory field
- `NewStore(baseDir string) *Store` constructor
- CRUD methods: `Save`, `Get`, `List`, `Remove`
- Atomic write implementation
- Directory creation and cleanup

**types.go**
- `Record` struct with JSON tags
- `Status` type and constants
- JSON marshaling/unmarshaling
- Record validation helpers

**paths.go**
- `recordDir(owner, repo string, pr int) string` — pure directory name derivation
- `recordFilename(dir string) string` — path to record.json
- `reviewsDir(dir string) string` — path to reviews/ subdirectory
- No state, no I/O — pure string transformations

## Data Models and JSON Schema

### Record Type

```go
package review

import "time"

// Status represents the current state of a PR review
type Status string

const (
    StatusWatching  Status = "watching"  // Enrolled, waiting for review trigger
    StatusReviewing Status = "reviewing" // Review in progress
    StatusDone      Status = "done"      // Review complete, PR merged/closed
)

// Record represents the persisted state for a single PR review
type Record struct {
    Repo                  string    `json:"repo"`                     // "owner/name"
    PR                    int       `json:"pr"`                       // Pull request number
    URL                   string    `json:"url"`                      // Full PR URL
    Status                Status    `json:"status"`                   // Current review status
    LastReviewedSHA       string    `json:"last_reviewed_sha"`        // Commit SHA last reviewed
    LastReviewedAt        string    `json:"last_reviewed_at"`         // RFC3339 timestamp or empty
    LastServicedRequest   string    `json:"last_serviced_request"`    // Dedup tracking (unused now, carried forward)
    EnrolledAt            string    `json:"enrolled_at"`              // RFC3339 timestamp
    ReviewDir             string    `json:"review_dir"`               // Path to worktree
}

// Validate checks required fields and valid status
func (r *Record) Validate() error {
    if r.Repo == "" {
        return fmt.Errorf("repo is required")
    }
    if r.PR <= 0 {
        return fmt.Errorf("pr must be positive")
    }
    if r.URL == "" {
        return fmt.Errorf("url is required")
    }
    if r.Status == "" {
        return fmt.Errorf("status is required")
    }
    if r.Status != StatusWatching && r.Status != StatusReviewing && r.Status != StatusDone {
        return fmt.Errorf("invalid status: %s", r.Status)
    }
    if r.EnrolledAt == "" {
        return fmt.Errorf("enrolled_at is required")
    }
    // Validate RFC3339 timestamps
    if r.EnrolledAt != "" {
        if _, err := time.Parse(time.RFC3339, r.EnrolledAt); err != nil {
            return fmt.Errorf("enrolled_at must be RFC3339: %w", err)
        }
    }
    if r.LastReviewedAt != "" {
        if _, err := time.Parse(time.RFC3339, r.LastReviewedAt); err != nil {
            return fmt.Errorf("last_reviewed_at must be RFC3339: %w", err)
        }
    }
    return nil
}

// ToJSON serializes the record to JSON
func (r *Record) ToJSON() ([]byte, error) {
    return json.Marshal(r)
}

// FromJSON deserializes JSON data into a record
func FromJSON(data []byte) (*Record, error) {
    var rec Record
    if err := json.Unmarshal(data, &rec); err != nil {
        return nil, err
    }
    return &rec, nil
}
```

### JSON Schema Example

```json
{
  "repo": "owner/name",
  "pr": 123,
  "url": "https://github.com/owner/name/pull/123",
  "status": "watching",
  "last_reviewed_sha": "abc123def456",
  "last_reviewed_at": "2026-09-16T14:30:00Z",
  "last_serviced_request": "",
  "enrolled_at": "2026-09-16T10:00:00Z",
  "review_dir": ".worktrees/review-owner-name-123"
}
```

## File System Layout and Naming Conventions

### Directory Structure

```
.howmux/reviews/
├── owner1-repo1-42/
│   ├── record.json              # State record for PR #42
│   └── reviews/
│       ├── sha1.md              # Review artifact (future: issue #4)
│       └── sha2.md
├── owner2-repo2-123/
│   ├── record.json
│   └── reviews/
└── owner3-repo3-7/
    ├── record.json
    └── reviews/
```

### Naming Rules

**PR Directory**: `<owner>-<repo>-<pr>`
- `owner` and `repo` extracted from `Repo` field ("owner/name")
- `pr` is the numeric PR number
- Example: `"facebook/react"` PR 456 → `facebook-react-456/`

**Record File**: Always `record.json` within the PR directory

**Reviews Subdirectory**: Always `reviews/` within the PR directory (created on first save, artifacts written by future workflow components)

### Path Derivation (Pure Functions)

```go
// recordDir returns the directory name for a PR record
// Example: recordDir("owner", "name", 123) → "owner-name-123"
func recordDir(owner, repo string, pr int) string {
    return fmt.Sprintf("%s-%s-%d", owner, repo, pr)
}

// recordFilename returns the full path to record.json
func recordFilename(baseDir, dirName string) string {
    return filepath.Join(baseDir, dirName, "record.json")
}

// reviewsDir returns the path to the reviews/ subdirectory
func reviewsDir(baseDir, dirName string) string {
    return filepath.Join(baseDir, dirName, "reviews")
}
```

## API Interface Design

### Store Type

```go
package review

// Store manages PR review records on the filesystem
type Store struct {
    baseDir string // Base directory (e.g., ".howmux/reviews")
}

// NewStore creates a new Store with the given base directory
func NewStore(baseDir string) *Store {
    return &Store{baseDir: baseDir}
}

// NewDefaultStore creates a Store using the default base directory
func NewDefaultStore() *Store {
    return NewStore(".howmux/reviews")
}
```

### CRUD Operations

```go
// Save persists a record to disk atomically
// Creates the PR directory and reviews/ subdirectory if they don't exist
// Returns error if validation fails or write fails
func (s *Store) Save(rec Record) error

// Get retrieves a record by repo and PR number
// Returns (record, true, nil) if found
// Returns (empty, false, nil) if not found
// Returns (empty, false, error) on I/O error
func (s *Store) Get(repo string, pr int) (Record, bool, error)

// List returns all stored records
// Returns empty slice if no records exist
// Returns error only on directory read failure
func (s *Store) List() ([]Record, error)

// Remove deletes a PR's entire folder (record.json + reviews/)
// Returns nil if folder doesn't exist (idempotent)
// Returns error on deletion failure
func (s *Store) Remove(repo string, pr int) error
```

### Helper Functions

```go
// RecordDir returns the directory name for a given repo and PR
// This is a pure function exported for testing and external use
func RecordDir(repo string, pr int) string {
    parts := strings.SplitN(repo, "/", 2)
    if len(parts) != 2 {
        return fmt.Sprintf("%s-%d", repo, pr)
    }
    return recordDir(parts[0], parts[1], pr)
}
```

## Atomic Write Implementation Strategy

Following the pattern from `internal/session/manager.go`:

1. **Ensure parent directory exists**: `os.MkdirAll(prDir, 0755)`
2. **Ensure reviews subdirectory exists**: `os.MkdirAll(reviewsDir, 0755)`
3. **Serialize record to JSON**: `rec.ToJSON()`
4. **Write to temporary file**: `<prDir>/record.json.tmp`
5. **Atomic rename**: `os.Rename(tmpFile, finalFile)`
6. **Cleanup on failure**: `os.Remove(tmpFile)` if rename fails

### Implementation Pseudo-code

```go
func (s *Store) Save(rec Record) error {
    // Validate first
    if err := rec.Validate(); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }

    // Derive paths
    dirName := RecordDir(rec.Repo, rec.PR)
    prDir := filepath.Join(s.baseDir, dirName)
    reviewsSubdir := filepath.Join(prDir, "reviews")
    recordFile := filepath.Join(prDir, "record.json")
    tempFile := recordFile + ".tmp"

    // Create directories
    if err := os.MkdirAll(reviewsSubdir, 0755); err != nil {
        return fmt.Errorf("failed to create directories: %w", err)
    }

    // Serialize
    data, err := rec.ToJSON()
    if err != nil {
        return fmt.Errorf("failed to serialize: %w", err)
    }

    // Atomic write
    if err := os.WriteFile(tempFile, data, 0644); err != nil {
        return fmt.Errorf("failed to write temp file: %w", err)
    }

    if err := os.Rename(tempFile, recordFile); err != nil {
        os.Remove(tempFile)
        return fmt.Errorf("failed to finalize write: %w", err)
    }

    return nil
}
```

## Error Handling Approach

### Error Categories

1. **Validation Errors**: Returned before any I/O (required fields, invalid status)
2. **I/O Errors**: Wrapped with context (directory creation, file write, file read)
3. **Serialization Errors**: JSON marshal/unmarshal failures
4. **Not Found**: Represented by `(empty, false, nil)` return from `Get()`

### Error Wrapping

Use `fmt.Errorf` with `%w` for error chains:
```go
return fmt.Errorf("failed to write record for %s#%d: %w", rec.Repo, rec.PR, err)
```

### Idempotency

- `Remove()` returns `nil` if the folder doesn't exist (idempotent delete)
- `Save()` overwrites existing records (upsert behavior)
- `Get()` distinguishes not-found from error via `(rec, bool, error)` return

## Testing Strategy

### Test File Organization

**types_test.go**
- `TestRecordValidation`: Required fields, status values, RFC3339 timestamps
- `TestRecordJSON`: Round-trip marshal/unmarshal
- `TestRecordValidateEdgeCases`: Empty strings, negative PR, invalid timestamps

**paths_test.go**
- `TestRecordDir`: Standard cases, repo with slash, edge cases
- `TestRecordDirWithSlashInRepo`: Ensure "owner/name" → "owner-name-pr"
- `TestPathHelpers`: Verify filename and reviewsDir construction

**store_test.go**
- `TestStoreRoundTrip`: Save then Get returns equal record
- `TestStoreList`: Save multiple, List returns all
- `TestStoreRemove`: Remove deletes folder, other records unaffected
- `TestStoreGetNotFound`: Get non-existent returns `(empty, false, nil)`
- `TestStoreAtomicWrite`: Verify temp file + rename (check no `.tmp` left behind)
- `TestStoreConcurrentSaves`: Multiple goroutines saving different PRs (no race detector warnings)
- `TestStoreReviewsDir`: Verify `reviews/` subdirectory created on Save

### Test Patterns

All tests use `t.TempDir()` for isolation:

```go
func TestStoreRoundTrip(t *testing.T) {
    baseDir := t.TempDir()
    store := NewStore(baseDir)

    rec := Record{
        Repo:       "owner/name",
        PR:         123,
        URL:        "https://github.com/owner/name/pull/123",
        Status:     StatusWatching,
        EnrolledAt: time.Now().Format(time.RFC3339),
        ReviewDir:  ".worktrees/review-owner-name-123",
    }

    // Save
    if err := store.Save(rec); err != nil {
        t.Fatalf("Save failed: %v", err)
    }

    // Get
    got, found, err := store.Get("owner/name", 123)
    if err != nil {
        t.Fatalf("Get failed: %v", err)
    }
    if !found {
        t.Fatal("Get returned not found")
    }

    // Compare (ignoring timestamp precision)
    if got.Repo != rec.Repo || got.PR != rec.PR || got.Status != rec.Status {
        t.Errorf("Round-trip mismatch: got %+v, want %+v", got, rec)
    }
}
```

### Coverage Requirements

- **No network calls**: All tests run offline
- **No gh/git invocations**: Pure filesystem operations only
- **Race detector clean**: `go test -race` passes
- **Edge cases**: Repo with slash, missing fields, concurrent access

## Implementation Tasks Breakdown

### Task 1: Create Package Structure and Path Helpers
**Dependencies**: None  
**Acceptance Criteria**:
- Create `internal/review/` directory
- Implement `paths.go` with `recordDir`, `recordFilename`, `reviewsDir` pure functions
- Implement `RecordDir` exported helper that handles "owner/name" → "owner-name-pr"
- Create `paths_test.go` with tests for:
  - Standard repo path derivation
  - Repo containing slash ("owner/name" → "owner-name-pr")
  - Edge cases (empty strings, zero PR)
- All `paths_test.go` tests pass
- Pure functions have no side effects (no I/O, no state)

### Task 2: Define Record Type and JSON Marshaling
**Dependencies**: None (can run in parallel with Task 1)  
**Acceptance Criteria**:
- Create `types.go` with `Record` struct and JSON tags matching schema
- Implement `Status` type with constants: `StatusWatching`, `StatusReviewing`, `StatusDone`
- Implement `Validate()` method checking:
  - Required fields: `Repo`, `PR`, `URL`, `Status`, `EnrolledAt`
  - Valid status values
  - RFC3339 timestamp format for `EnrolledAt` and `LastReviewedAt`
  - PR number is positive
- Implement `ToJSON()` and `FromJSON()` methods
- Create `types_test.go` with tests for:
  - Valid record passes validation
  - Missing required fields fail validation
  - Invalid status value fails validation
  - Invalid RFC3339 timestamp fails validation
  - Negative/zero PR number fails validation
  - Round-trip JSON marshal/unmarshal preserves all fields
- All `types_test.go` tests pass

### Task 3: Implement Store and CRUD Operations
**Dependencies**: Task 1 (paths), Task 2 (types)  
**Acceptance Criteria**:
- Create `store.go` with `Store` type and `NewStore`, `NewDefaultStore` constructors
- Implement `Save(rec Record) error`:
  - Calls `rec.Validate()` before any I/O
  - Creates PR directory and `reviews/` subdirectory via `os.MkdirAll`
  - Atomic write: temp file + rename pattern
  - Wraps errors with context
- Implement `Get(repo string, pr int) (Record, bool, error)`:
  - Returns `(record, true, nil)` if found
  - Returns `(empty, false, nil)` if not found (not an error)
  - Returns `(empty, false, error)` on I/O or deserialization error
- Implement `List() ([]Record, error)`:
  - Reads all subdirectories under `baseDir`
  - Parses each `record.json`
  - Returns empty slice if no records exist (not an error)
  - Skips invalid/unparseable records with a log warning
- Implement `Remove(repo string, pr int) error`:
  - Deletes entire PR directory (record.json + reviews/)
  - Returns `nil` if directory doesn't exist (idempotent)
- All methods follow error wrapping pattern: `fmt.Errorf("context: %w", err)`

### Task 4: Write Store Tests
**Dependencies**: Task 3  
**Acceptance Criteria**:
- Create `store_test.go` with the following tests:
  - `TestStoreRoundTrip`: Save a record, Get it back, verify all fields match
  - `TestStoreList`: Save 3 records for different PRs, List returns all 3
  - `TestStoreRemove`: Save 2 records, Remove one, verify:
    - Removed record's Get returns `(empty, false, nil)`
    - Other record still exists via Get
  - `TestStoreGetNotFound`: Get non-existent record returns `(empty, false, nil)`
  - `TestStoreAtomicWrite`: After Save completes, verify no `.tmp` file remains
  - `TestStoreValidationError`: Save invalid record returns error before any I/O
  - `TestStoreReviewsDir`: After Save, verify `reviews/` subdirectory exists
  - `TestStoreConcurrentSaves`: 10 goroutines save different PRs concurrently, no races
- All tests use `t.TempDir()` for isolation
- All tests pass
- `go test -race ./internal/review` passes with zero race warnings

### Task 5: Integration Test and Documentation
**Dependencies**: Task 4  
**Acceptance Criteria**:
- Add integration test `TestStoreEndToEnd` in `store_test.go`:
  - Save a record with `StatusWatching`
  - Get it back, update to `StatusReviewing`, Save again
  - Get it again, verify status updated
  - List shows 1 record
  - Remove it, List shows 0 records
- Add package documentation comment in `store.go`:
  ```go
  // Package review provides a filesystem-based persistence layer for PR review state.
  // Each PR is stored in its own directory under .howmux/reviews/, with a record.json
  // file containing metadata and a reviews/ subdirectory for accumulated artifacts.
  //
  // All operations are atomic and crash-safe. No network or git operations are performed.
  ```
- Add usage example in doc comment for `Store` type
- Verify all exported symbols have doc comments
- Run `go test ./internal/review -v` and verify all tests pass
- Run `go test ./internal/review -race` and verify zero race warnings
- Verify `go vet ./internal/review` passes

### Task 6: Verify No External Dependencies
**Dependencies**: Task 5  
**Acceptance Criteria**:
- Run `grep -r "github.com/matthiashowellyopp/howmux/internal/github" internal/review/` returns zero matches
- Run `grep -r "exec.Command.*gh" internal/review/` returns zero matches
- Run `grep -r "exec.Command.*git" internal/review/` returns zero matches
- Run `grep -r "http\." internal/review/` returns zero matches
- Verify imports in all `internal/review/*.go` files contain only:
  - Standard library (`encoding/json`, `fmt`, `os`, `path/filepath`, `strings`, `time`, etc.)
  - `testing` (test files only)
- Add a comment in `store.go` stating: `// This package has zero external dependencies and performs no network or git operations.`

## Validation Commands

From the project root:

```bash
# Run all review package tests
go test ./internal/review -v

# Run with race detector
go test ./internal/review -race

# Run specific test
go test ./internal/review -run TestStoreRoundTrip -v

# Verify no temp files left behind (after running tests)
find .howmux/reviews -name "*.tmp" | wc -l  # Should output: 0

# Check test coverage
go test ./internal/review -cover

# Verify no network/github/git dependencies
grep -rn "internal/github" internal/review/    # Should be empty
grep -rn "exec.Command" internal/review/       # Should be empty
grep -rn "http\." internal/review/             # Should be empty

# Lint
go vet ./internal/review
```

## Non-Goals (Deferred to Future Issues)

- **Review runner**: Writing review artifacts to `reviews/<sha>.md` (issue #4)
- **GitHub integration**: Fetching PR metadata, posting reviews (issue #5)
- **Loop logic**: Re-review triggers, auto-posting (issue #6)
- **Cleanup/pruning**: Removing records for merged/closed PRs (issue #7)
- **CLI commands**: `review <PR_URL>` command implementation (issue #8)

This package is purely the data layer. Future issues build on top of it.

## Concurrency Considerations

**No cross-goroutine shared state**: This package is stateless — `Store` only holds a `baseDir` string. No mutexes needed.

**Concurrent access pattern**: Multiple goroutines can call methods on the same `Store` instance safely because:
- Each PR gets its own directory (no file contention between different PRs)
- Atomic writes (temp + rename) ensure no partial reads
- OS-level filesystem locking handles concurrent writes to the same PR (last write wins)

**Race detector verification**: Task 4 includes `TestStoreConcurrentSaves` exercised with `go test -race` to confirm no data races.

## Open Questions

None — specification is complete and implementation is straightforward.

## References

- Issue #59: https://github.com/matthiashowellyopp/howmux/issues/59
- Existing patterns:
  - `internal/session/manager.go` — atomic write pattern
  - `internal/session/types.go` — JSON marshaling pattern
  - `internal/watcher/watcher.go` — directory-based state (`.howmux/retries/`)
