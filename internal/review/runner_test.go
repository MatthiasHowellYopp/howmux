package review

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeTestSpoolFixture writes a minimal, realistic pending/ spool fixture
// to a fresh t.TempDir() and returns its absolute path. RunReview's
// WriteReviewedMetadata call (added in issue #108) reads/patches whatever
// path runReviewToolFunc returns, so any fake in this file that previously
// returned a bare non-existent path string now needs a real file backing
// it — this helper centralizes that fixture so each test doesn't repeat the
// front-matter boilerplate.
func writeTestSpoolFixture(t *testing.T, filename string) string {
	t.Helper()
	spoolDir := filepath.Join(t.TempDir(), "PR-Review", "pending")
	if err := os.MkdirAll(spoolDir, 0755); err != nil {
		t.Fatalf("failed to create spool dir: %v", err)
	}
	spoolPath := filepath.Join(spoolDir, filename)
	fixture := "---\nverdict: APPROVE\ndecision:\ngenerated:\n---\n\nFixture review body.\n"
	if err := os.WriteFile(spoolPath, []byte(fixture), 0644); err != nil {
		t.Fatalf("failed to write spool fixture: %v", err)
	}
	return spoolPath
}

// TestRunReview_DiffFetch_Argv verifies fetchDiffFunc is called with correct arguments
func TestRunReview_DiffFetch_Argv(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	var capturedOwner, capturedRepo, capturedOutputFile string
	var capturedPR int
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		capturedOwner = owner
		capturedRepo = repo
		capturedPR = pr
		capturedOutputFile = outputFile
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	// RunReview now stamps the spool file via WriteReviewedMetadata, which
	// requires the returned path to exist on disk (see issue #108), so the
	// fake must write a minimal real fixture rather than return a bare
	// string.
	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
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

	ctx := context.Background()
	err := RunReview(ctx, rec, "abc123", store, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify arguments
	if capturedOwner != "testowner" {
		t.Errorf("expected owner 'testowner', got %q", capturedOwner)
	}
	if capturedRepo != "testrepo" {
		t.Errorf("expected repo 'testrepo', got %q", capturedRepo)
	}
	if capturedPR != 42 {
		t.Errorf("expected PR 42, got %d", capturedPR)
	}
	if capturedOutputFile == "" {
		t.Error("expected outputFile to be set")
	}
	if !strings.Contains(capturedOutputFile, "pr-42-") || !strings.HasSuffix(capturedOutputFile, ".diff") {
		t.Errorf("expected temp file pattern 'pr-42-*.diff', got %q", capturedOutputFile)
	}
}

// TestRunReview_ReviewTool_Argv verifies runReviewToolFunc is called with correct argv
func TestRunReview_ReviewTool_Argv(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	var capturedArgv []string
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		capturedArgv = append([]string{}, argv...) // deep copy
		return []string{writeTestSpoolFixture(t, "pr-review-owner-repo.md")}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         99,
		URL:        "https://github.com/owner/repo/pull/99",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-99",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "def456", store, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify argv structure
	if len(capturedArgv) < 8 {
		t.Fatalf("expected at least 8 argv elements, got %d: %v", len(capturedArgv), capturedArgv)
	}

	expectedArgv := map[string]string{
		"pr_review.py": capturedArgv[0],
		"--diff-file":  capturedArgv[1],
		"--repo":       capturedArgv[3],
		"owner/repo":   capturedArgv[4],
		"--pr":         capturedArgv[5],
		"99":           capturedArgv[6],
		"--language":   capturedArgv[7],
		"auto":         capturedArgv[8],
	}

	// Check key argv elements
	if capturedArgv[0] != "pr_review.py" {
		t.Errorf("expected argv[0] = 'pr_review.py', got %q", capturedArgv[0])
	}
	if capturedArgv[1] != "--diff-file" {
		t.Errorf("expected argv[1] = '--diff-file', got %q", capturedArgv[1])
	}
	// argv[2] is the diff file path (temp file, varies)
	if capturedArgv[3] != "--repo" {
		t.Errorf("expected argv[3] = '--repo', got %q", capturedArgv[3])
	}
	if capturedArgv[4] != "owner/repo" {
		t.Errorf("expected argv[4] = 'owner/repo', got %q", capturedArgv[4])
	}
	if capturedArgv[5] != "--pr" {
		t.Errorf("expected argv[5] = '--pr', got %q", capturedArgv[5])
	}
	if capturedArgv[6] != "99" {
		t.Errorf("expected argv[6] = '99', got %q", capturedArgv[6])
	}
	if capturedArgv[7] != "--language" {
		t.Errorf("expected argv[7] = '--language', got %q", capturedArgv[7])
	}
	if capturedArgv[8] != "auto" {
		t.Errorf("expected argv[8] = 'auto', got %q", capturedArgv[8])
	}

	_ = expectedArgv // keep linter happy
}

// TestRunReview_StderrRouting verifies stderr from runReviewToolFunc is written to tabWriter
func TestRunReview_StderrRouting(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		// Write fake stderr progress to the provided writer
		io.WriteString(stderrWriter, "→ [1/3] Orienting…\n")
		io.WriteString(stderrWriter, "→ [2/3] Running lenses…\n")
		io.WriteString(stderrWriter, "→ [3/3] Consolidating…\n")
		io.WriteString(stderrWriter, "→ verdict: APPROVE\n")
		return []string{writeTestSpoolFixture(t, "pr-review-owner-repo.md")}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         1,
		URL:        "https://github.com/owner/repo/pull/1",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-1",
	}

	// Capture stderr output via a buffer
	var stderrBuf bytes.Buffer

	ctx := context.Background()
	err := RunReview(ctx, rec, "sha1", store, &stderrBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify stderr content
	stderrOutput := stderrBuf.String()
	expectedLines := []string{
		"→ [1/3] Orienting…",
		"→ [2/3] Running lenses…",
		"→ [3/3] Consolidating…",
		"→ verdict: APPROVE",
	}

	for _, expected := range expectedLines {
		if !strings.Contains(stderrOutput, expected) {
			t.Errorf("stderr missing expected line: %q\nGot:\n%s", expected, stderrOutput)
		}
	}
}

// TestRunReview_StdoutCapture_SpoolPath verifies stdout is captured and becomes SpoolPath
func TestRunReview_StdoutCapture_SpoolPath(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	expectedSpoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo-42.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		// Return spool path as stdout
		return []string{expectedSpoolPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         42,
		URL:        "https://github.com/owner/repo/pull/42",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-42",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "abc123", store, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify SpoolPath was captured
	updated, found, err := store.Get("owner/repo", 42)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if !found {
		t.Fatal("record not found")
	}
	if updated.SpoolPath != expectedSpoolPath {
		t.Errorf("expected SpoolPath %q, got %q", expectedSpoolPath, updated.SpoolPath)
	}
}

// TestRunReview_RecordUpdate_Success verifies record is updated correctly
func TestRunReview_RecordUpdate_Success(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         10,
		URL:        "https://github.com/owner/repo/pull/10",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-10",
	}

	ctx := context.Background()
	headSHA := "commit-sha-123"
	err := RunReview(ctx, rec, headSHA, store, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify record was updated correctly
	updated, found, err := store.Get("owner/repo", 10)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if !found {
		t.Fatal("record not found")
	}

	// Check all updated fields
	if updated.Status != StatusReviewed {
		t.Errorf("expected Status %q, got %q", StatusReviewed, updated.Status)
	}
	if updated.LastReviewedSHA != headSHA {
		t.Errorf("expected LastReviewedSHA %q, got %q", headSHA, updated.LastReviewedSHA)
	}
	if updated.LastReviewedAt != fixedTime.Format(time.RFC3339) {
		t.Errorf("expected LastReviewedAt %q, got %q", fixedTime.Format(time.RFC3339), updated.LastReviewedAt)
	}
	if updated.LastServicedRequest != headSHA {
		t.Errorf("expected LastServicedRequest %q, got %q", headSHA, updated.LastServicedRequest)
	}
	if updated.SpoolPath != spoolPath {
		t.Errorf("expected SpoolPath %q, got %q", spoolPath, updated.SpoolPath)
	}
}

// TestRunReview_Cancellation verifies context cancellation works (concurrent test)
func TestRunReview_Cancellation(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Capture the status persisted at the start of RunReview, before the
	// review-tool call surfaces the cancellation. fetchDiffFunc runs after
	// the initial StatusReviewing Save, so reading the record back here
	// asserts that intermediate write explicitly — rather than inferring it
	// only from the final reverted state, which would silently depend on
	// BOTH the initial Save AND the revert Save landing (and on store.Save
	// being context-unaware). See PR #116 review, finding 1.
	var statusDuringWork Status
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		if r, found, err := store.Get(owner+"/"+repo, pr); err == nil && found {
			statusDuringWork = r.Status
		}
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	// Seam that checks for cancellation
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return []string{"/Users/test/PR-Review/pending/pr-review-owner-repo.md"}, nil
		}
	}

	rec := Record{
		Repo:       "owner/repo",
		PR:         5,
		URL:        "https://github.com/owner/repo/pull/5",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-5",
	}

	// Create cancellable context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before calling RunReview

	err := RunReview(ctx, rec, "sha5", store, io.Discard)

	// Verify error is context.Canceled
	if err == nil {
		t.Fatal("expected error due to cancelled context")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}

	// The intermediate StatusReviewing write is asserted explicitly (via
	// statusDuringWork, captured inside fetchDiffFunc) rather than only
	// inferred from the reverted end-state — so the two guarantees (initial
	// write landed; revert landed) are tested separately.
	if statusDuringWork != StatusReviewing {
		t.Errorf("expected StatusReviewing to be persisted before the review-tool call, got %q", statusDuringWork)
	}

	// Verify record reverted to StatusWatching: the StatusReviewing write at
	// the top of RunReview persists via storeImpl.Save (not context-aware),
	// so it lands even though ctx is already cancelled; the subsequent
	// runReviewToolFunc failure (surfacing the cancellation) then reverts
	// the record to StatusWatching via the normal error-revert path.
	updated, found, err := store.Get("owner/repo", 5)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before the review-tool call observes cancellation)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after cancellation, got %q", updated.Status)
	}
}

// TestRunReview_DiffFetchFailure verifies error handling for diff fetch
// failure: RunReview persists StatusReviewing before the diff fetch runs,
// so on failure the record exists but is reverted to StatusWatching rather
// than left stuck on StatusReviewing.
func TestRunReview_DiffFetchFailure(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams - fetchDiff fails
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return fmt.Errorf("gh pr diff failed: network error")
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		t.Fatal("runReviewToolFunc should not be called when diff fetch fails")
		return nil, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         1,
		URL:        "https://github.com/owner/repo/pull/1",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-1",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "sha1", store, io.Discard)

	// Verify error
	if err == nil {
		t.Fatal("expected error from diff fetch failure")
	}
	if !strings.Contains(err.Error(), "failed to fetch PR diff") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify record reverted to StatusWatching: RunReview now saves a
	// StatusReviewing record before the diff fetch runs, then reverts it to
	// StatusWatching on failure, so the record exists but is no longer
	// stuck on StatusReviewing.
	updated, found, err := store.Get("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before diff fetch)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after diff-fetch failure, got %q", updated.Status)
	}
}

// TestRunReview_ReviewToolFailure verifies error handling and cleanup for tool failure
func TestRunReview_ReviewToolFailure(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Track temp file creation to verify cleanup
	var createdDiffFile string
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		createdDiffFile = outputFile
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return nil, fmt.Errorf("pr_review.py failed: exit code 1")
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         2,
		URL:        "https://github.com/owner/repo/pull/2",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-2",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "sha2", store, io.Discard)

	// Verify error
	if err == nil {
		t.Fatal("expected error from review tool failure")
	}
	if !strings.Contains(err.Error(), "pr_review.py invocation failed") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify temp file was cleaned up (defer os.Remove should have run)
	if createdDiffFile != "" {
		if _, err := os.Stat(createdDiffFile); err == nil {
			t.Errorf("temp diff file %q should have been cleaned up", createdDiffFile)
		}
	}

	// Verify record reverted to StatusWatching: a StatusReviewing record is
	// saved before the review tool runs, then reverted on failure.
	updated, found, err := store.Get("owner/repo", 2)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before review tool invocation)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after review-tool failure, got %q", updated.Status)
	}
}

// TestRunReview_TempFileCleanup verifies temp files are cleaned up in all paths
func TestRunReview_TempFileCleanup(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	tests := []struct {
		name              string
		runReviewToolMock func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error)
		expectError       bool
	}{
		{
			name: "success path",
			runReviewToolMock: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				return []string{writeTestSpoolFixture(t, "pr-review-owner-repo.md")}, nil
			},
			expectError: false,
		},
		{
			name: "failure path",
			runReviewToolMock: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				return nil, fmt.Errorf("tool failed")
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var createdDiffFile string
			fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				createdDiffFile = outputFile
				return os.WriteFile(outputFile, []byte("fake diff"), 0644)
			}

			runReviewToolFunc = tt.runReviewToolMock

			baseDir := t.TempDir()
			store := NewStore(baseDir)

			rec := Record{
				Repo:       "owner/repo",
				PR:         3,
				URL:        "https://github.com/owner/repo/pull/3",
				Status:     StatusWatching,
				EnrolledAt: time.Now().Format(time.RFC3339),
				ReviewDir:  "/tmp/review-3",
			}

			ctx := context.Background()
			err := RunReview(ctx, rec, "sha3", store, io.Discard)

			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify temp file was cleaned up
			if createdDiffFile != "" {
				if _, statErr := os.Stat(createdDiffFile); statErr == nil {
					t.Errorf("temp diff file %q should have been cleaned up", createdDiffFile)
				}
			}
		})
	}
}

// TestRunReview_RecordValidation verifies SpoolPath integration with Record
func TestRunReview_RecordValidation(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	// Install fake seams
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         20,
		URL:        "https://github.com/owner/repo/pull/20",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-20",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "sha20", store, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Retrieve and validate the updated record
	updated, found, err := store.Get("owner/repo", 20)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if !found {
		t.Fatal("record not found")
	}

	// Verify record passes validation with SpoolPath populated
	if err := updated.Validate(); err != nil {
		t.Errorf("record validation failed: %v", err)
	}

	// Verify JSON serialization includes SpoolPath
	jsonData, err := updated.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	if !strings.Contains(string(jsonData), `"spool_path"`) {
		t.Error("JSON serialization missing 'spool_path' field")
	}
	if !strings.Contains(string(jsonData), `"`+spoolPath+`"`) {
		t.Error("JSON serialization missing spool path value")
	}

	// Verify round-trip deserialization
	roundtrip, err := FromJSON(jsonData)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}
	if roundtrip.SpoolPath != spoolPath {
		t.Errorf("round-trip SpoolPath mismatch: expected %q, got %q", spoolPath, roundtrip.SpoolPath)
	}
}

// TestValidateSpoolPath checks the spool-path contract guard: only a
// PR-Review/pending/<name>.md path is accepted, so a trailing banner or
// diagnostic line is rejected rather than silently persisted as SpoolPath.
func TestValidateSpoolPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid pending md", "/Users/x/PR-Review/pending/pr-review-owner-repo-1.md", false},
		{"empty", "", true},
		{"not md", "/Users/x/PR-Review/pending/review", true},
		{"wrong dir", "/tmp/spool.md", true},
		{"banner line", "Done reviewing 3 files", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSpoolPath(tt.path); (err != nil) != tt.wantErr {
				t.Errorf("validateSpoolPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

// TestRunReview_StampsSpoolFrontMatter is an end-to-end test verifying that
// RunReview stamps "reviewed_sha" and "generated" into the spool file's
// front-matter, while preserving every other front-matter field and the
// review body untouched.
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

	if !strings.Contains(string(patched), "Review body unchanged.") {
		t.Errorf("expected review body to be preserved, got: %s", string(patched))
	}

	if fields["repo"] != "testowner/testrepo" {
		t.Errorf("expected repo to be preserved, got %q", fields["repo"])
	}
	if fields["verdict"] != "APPROVE" {
		t.Errorf("expected verdict to be preserved, got %q", fields["verdict"])
	}
}

// TestRunReview_SpoolStampFailure_DoesNotUpdateRecord verifies the
// fail-closed property from Task 3: if WriteReviewedMetadata refuses to
// stamp the spool file (here, because it resolves to an already-finalized
// done/ path), RunReview returns an error and never persists the record as
// StatusReviewed.
//
// runReviewToolFunc still returns a pending/ path so it satisfies
// validateSpoolPath's format check. No file is written at that pending/
// path, so resolveSpoolPath's pending -> done fallback kicks in and finds
// the fixture written directly under done/, which WriteReviewedMetadata
// then refuses to touch (see spool.go's inDoneDir guard).
func TestRunReview_SpoolStampFailure_DoesNotUpdateRecord(t *testing.T) {
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

	prReviewRoot := t.TempDir()
	donedir := filepath.Join(prReviewRoot, "PR-Review", "done")
	if err := os.MkdirAll(donedir, 0755); err != nil {
		t.Fatalf("failed to create done dir: %v", err)
	}
	pendingPath := filepath.Join(prReviewRoot, "PR-Review", "pending", "pr-review-testowner-testrepo-43.md")
	donePath := filepath.Join(donedir, "pr-review-testowner-testrepo-43.md")

	fixture := `---
repo: testowner/testrepo
pr: 43
verdict: APPROVE
decision: post
decision_notes:
diff_file: /tmp/pr-43-123.diff
generated:
---

Already finalized review body.
`
	if err := os.WriteFile(donePath, []byte(fixture), 0644); err != nil {
		t.Fatalf("failed to write done fixture: %v", err)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{pendingPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "testowner/testrepo",
		PR:         43,
		URL:        "https://github.com/testowner/testrepo/pull/43",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-43",
	}

	const knownSHA = "def5678abc1234"

	ctx := context.Background()
	err := RunReview(ctx, rec, knownSHA, store, io.Discard)
	if err == nil {
		t.Fatal("expected error when spool file is already finalized (done/), got nil")
	}
	if !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("expected error to mention the file being already finalized, got: %v", err)
	}

	// The record must never have been saved as StatusReviewed — it reverts
	// to StatusWatching via the WriteReviewedMetadata-failure revert path.
	updated, found, getErr := store.Get("testowner/testrepo", 43)
	if getErr != nil {
		t.Fatalf("failed to check record: %v", getErr)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before metadata stamping)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after metadata-stamping failure, got %q", updated.Status)
	}

	// The done/ fixture itself must be untouched (WriteReviewedMetadata
	// refused to patch it).
	afterDone, err := os.ReadFile(donePath)
	if err != nil {
		t.Fatalf("failed to read done fixture after RunReview: %v", err)
	}
	if string(afterDone) != fixture {
		t.Errorf("expected done/ fixture to be untouched, got: %s", string(afterDone))
	}
}

// TestRunReview_SetsStatusReviewingBeforeWork verifies that RunReview
// persists rec.Status = StatusReviewing before any long-running work
// (diff fetch, subprocess invocation) starts — not just "at some point"
// during the run. The fake fetchDiffFunc reads the record back from the
// store before writing the diff, capturing whatever status was persisted
// at that moment.
func TestRunReview_SetsStatusReviewingBeforeWork(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	var capturedStatus Status
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		rec, found, err := store.Get(owner+"/"+repo, pr)
		if err != nil {
			t.Fatalf("failed to read back record inside fetchDiffFunc: %v", err)
		}
		if !found {
			t.Fatal("expected record to already exist inside fetchDiffFunc")
		}
		capturedStatus = rec.Status
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	rec := Record{
		Repo:       "owner/repo",
		PR:         30,
		URL:        "https://github.com/owner/repo/pull/30",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-30",
	}

	ctx := context.Background()
	if err := RunReview(ctx, rec, "sha30", store, io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedStatus != StatusReviewing {
		t.Errorf("expected status to be StatusReviewing before diff fetch, got %q", capturedStatus)
	}
}

// saveSeqStore is a StoreInterface double whose Save fails on a chosen
// call number (1-indexed), succeeding on all others, while recording the
// status of the most recent Save it accepted. It lets a test drive the
// distinct "first (StatusReviewing) Save succeeds, final (StatusReviewed)
// Save fails" ordering that the FS-backed store cannot produce. Save is
// called sequentially by RunReview, so no locking is needed.
type saveSeqStore struct {
	failOnCall  int // 1-indexed Save call to fail; 0 = never fail
	saveCalls   int
	lastSaved   Record
	savedStatus Status // status of the last successfully-saved record
}

func (s *saveSeqStore) Save(rec Record) error {
	s.saveCalls++
	if s.failOnCall != 0 && s.saveCalls == s.failOnCall {
		return fmt.Errorf("simulated save failure on call %d", s.saveCalls)
	}
	s.lastSaved = rec
	s.savedStatus = rec.Status
	return nil
}
func (s *saveSeqStore) Get(repo string, pr int) (Record, bool, error) {
	if s.lastSaved.Repo == repo && s.lastSaved.PR == pr {
		return s.lastSaved, true, nil
	}
	return Record{}, false, nil
}
func (s *saveSeqStore) List() ([]Record, error)          { return []Record{s.lastSaved}, nil }
func (s *saveSeqStore) Remove(repo string, pr int) error { return nil }

var _ StoreInterface = (*saveSeqStore)(nil)

// TestRunReview_FinalSaveFailure_LeavesReviewing verifies the one error
// branch that deliberately does NOT revert: if the final StatusReviewed
// Save fails, RunReview returns "failed to update record" and the record's
// last-persisted status honestly remains StatusReviewing (the reviewed
// write never landed). This is the load-bearing "do not mask the failure
// with a revert" decision from the spec — guard it so a future erroneous
// revertToWatching on this branch is caught.
func TestRunReview_FinalSaveFailure_LeavesReviewing(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}
	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	// Save call 1 = the initial StatusReviewing write (succeeds); call 2 =
	// the final StatusReviewed write (fails).
	store := &saveSeqStore{failOnCall: 2}
	rec := Record{Repo: "owner/repo", PR: 40, Status: StatusWatching}

	err := RunReview(context.Background(), rec, "sha40", store, io.Discard)
	if err == nil {
		t.Fatal("expected error when the final StatusReviewed save fails")
	}
	if !strings.Contains(err.Error(), "failed to update record") {
		t.Errorf("expected \"failed to update record\" error, got: %v", err)
	}
	// The reviewed write never landed, and this branch must NOT revert, so
	// the last-persisted status is still StatusReviewing.
	if store.savedStatus != StatusReviewing {
		t.Errorf("expected on-disk status to remain StatusReviewing after a failed final save (no revert), got %q", store.savedStatus)
	}
	if store.saveCalls != 2 {
		t.Errorf("expected exactly 2 Save calls (StatusReviewing + failed StatusReviewed), got %d — a 3rd would indicate an erroneous revert on this branch", store.saveCalls)
	}
}

// TestRunReview_EmptyStdout_RevertsToWatching verifies the empty-stdout
// early-return branch: when runReviewToolFunc returns no output lines,
// RunReview returns a "produced no output" error and reverts the record to
// StatusWatching rather than leaving it stuck on StatusReviewing.
func TestRunReview_EmptyStdout_RevertsToWatching(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}
	// Review tool succeeds but emits no spool-path line.
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)
	rec := Record{
		Repo:       "owner/repo",
		PR:         41,
		URL:        "https://github.com/owner/repo/pull/41",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
	}

	err := RunReview(context.Background(), rec, "sha41", store, io.Discard)
	if err == nil {
		t.Fatal("expected error when the review tool produces no output")
	}
	if !strings.Contains(err.Error(), "produced no output") {
		t.Errorf("expected \"produced no output\" error, got: %v", err)
	}
	updated, found, getErr := store.Get("owner/repo", 41)
	if getErr != nil {
		t.Fatalf("failed to check record: %v", getErr)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before the empty-stdout check)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after empty stdout, got %q", updated.Status)
	}
}

// TestRunReview_SpoolPathValidationFailure_RevertsToWatching verifies that
// when runReviewToolFunc returns a line that fails validateSpoolPath's
// format check, RunReview returns an error and the record reverts to
// StatusWatching rather than remaining stuck on StatusReviewing.
func TestRunReview_SpoolPathValidationFailure_RevertsToWatching(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{"not-a-spool-path.txt"}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         31,
		URL:        "https://github.com/owner/repo/pull/31",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-31",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "sha31", store, io.Discard)
	if err == nil {
		t.Fatal("expected error from invalid spool path")
	}
	if !strings.Contains(err.Error(), "unexpected spool path") {
		t.Errorf("unexpected error message: %v", err)
	}

	updated, found, getErr := store.Get("owner/repo", 31)
	if getErr != nil {
		t.Fatalf("failed to check record: %v", getErr)
	}
	if !found {
		t.Fatal("expected record to exist (StatusReviewing write happens before spool path validation)")
	}
	if updated.Status != StatusWatching {
		t.Errorf("expected record reverted to StatusWatching after spool-path validation failure, got %q", updated.Status)
	}
}

// TestRunReview_SuccessTransitionsOutOfReviewing verifies that on
// successful completion, the final stored record shows StatusReviewed —
// i.e., RunReview transitions the record OUT of StatusReviewing rather
// than leaving it there.
func TestRunReview_SuccessTransitionsOutOfReviewing(t *testing.T) {
	// Save and restore original seams
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         32,
		URL:        "https://github.com/owner/repo/pull/32",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-32",
	}

	ctx := context.Background()
	if err := RunReview(ctx, rec, "sha32", store, io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, found, err := store.Get("owner/repo", 32)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if !found {
		t.Fatal("record not found")
	}
	if updated.Status != StatusReviewed {
		t.Errorf("expected final status StatusReviewed (transitioned out of StatusReviewing), got %q", updated.Status)
	}
}

// notifyCall records a single invocation of the notifyFunc seam.
type notifyCall struct {
	title   string
	message string
}

// fakeNotifyRecorder installs a fake notifyFunc that appends every call to
// calls (guarded by mu, since the real call happens on a background
// goroutine) and signals done exactly once per call so tests can
// synchronize without sleeping.
func installFakeNotifyRecorder(t *testing.T) (calls func() []notifyCall, done chan notifyCall) {
	t.Helper()
	origNotify := notifyFunc
	t.Cleanup(func() { notifyFunc = origNotify })

	var mu sync.Mutex
	var recorded []notifyCall
	done = make(chan notifyCall, 8)

	notifyFunc = func(title, message string) error {
		mu.Lock()
		recorded = append(recorded, notifyCall{title: title, message: message})
		mu.Unlock()
		done <- notifyCall{title: title, message: message}
		return nil
	}

	calls = func() []notifyCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]notifyCall, len(recorded))
		copy(out, recorded)
		return out
	}
	return calls, done
}

// waitForNotify blocks until either a call arrives on done or the timeout
// elapses, returning (call, true) or (zero, false). Bounded select instead
// of time.Sleep so this cannot flake by racing the background goroutine.
func waitForNotify(t *testing.T, done chan notifyCall, timeout time.Duration) (notifyCall, bool) {
	t.Helper()
	select {
	case c := <-done:
		return c, true
	case <-time.After(timeout):
		return notifyCall{}, false
	}
}

// TestRunReview_Notify_SuccessPath verifies that on a fully successful
// RunReview, notifyFunc is invoked exactly once, in the background, with the
// expected "Review ready: <repo> #<pr>" message and "howmux" title.
func TestRunReview_Notify_SuccessPath(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-notify-owner-repo.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	calls, done := installFakeNotifyRecorder(t)

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "notifyowner/notifyrepo",
		PR:         77,
		URL:        "https://github.com/notifyowner/notifyrepo/pull/77",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-77",
	}

	ctx := context.Background()
	if err := RunReview(ctx, rec, "sha77", store, io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, ok := waitForNotify(t, done, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for notifyFunc to be called")
	}

	if call.title != "howmux" {
		t.Errorf("expected notification title %q, got %q", "howmux", call.title)
	}
	wantMsg := "Review ready: notifyowner/notifyrepo #77"
	if call.message != wantMsg {
		t.Errorf("expected notification message %q, got %q", wantMsg, call.message)
	}

	// Give any accidental duplicate/async second call a chance to land
	// before asserting exactly one call was recorded.
	select {
	case extra := <-done:
		t.Fatalf("expected exactly one notifyFunc call, got an extra one: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	if got := calls(); len(got) != 1 {
		t.Fatalf("expected exactly 1 recorded notifyFunc call, got %d: %+v", len(got), got)
	}
}

// TestRunReview_Notify_NotCalledOnFailure verifies notifyFunc is never
// invoked when RunReview fails at any of its existing failure points.
func TestRunReview_Notify_NotCalledOnFailure(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	type setup struct {
		name          string
		fetchDiff     func(ctx context.Context, owner, repo string, pr int, outputFile string) error
		runReviewTool func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error)
	}

	// Fixture for the "invalid spool path" case: runReviewToolFunc returns
	// a stdout line that fails validateSpoolPath's format check.
	tests := []setup{
		{
			name: "diff fetch failure",
			fetchDiff: func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				return fmt.Errorf("gh pr diff failed: network error")
			},
			runReviewTool: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				t.Fatal("runReviewToolFunc should not be called when diff fetch fails")
				return nil, nil
			},
		},
		{
			name: "tool invocation failure",
			fetchDiff: func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				return os.WriteFile(outputFile, []byte("fake diff"), 0644)
			},
			runReviewTool: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				return nil, fmt.Errorf("pr_review.py failed: exit code 1")
			},
		},
		{
			name: "empty stdout",
			fetchDiff: func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				return os.WriteFile(outputFile, []byte("fake diff"), 0644)
			},
			runReviewTool: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				return nil, nil
			},
		},
		{
			name: "invalid spool path",
			fetchDiff: func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				return os.WriteFile(outputFile, []byte("fake diff"), 0644)
			},
			runReviewTool: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				return []string{"not-a-valid-spool-path"}, nil
			},
		},
		{
			name: "WriteReviewedMetadata failure (already finalized done/)",
			fetchDiff: func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
				return os.WriteFile(outputFile, []byte("fake diff"), 0644)
			},
			runReviewTool: func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
				prReviewRoot := t.TempDir()
				donedir := filepath.Join(prReviewRoot, "PR-Review", "done")
				if err := os.MkdirAll(donedir, 0755); err != nil {
					t.Fatalf("failed to create done dir: %v", err)
				}
				pendingPath := filepath.Join(prReviewRoot, "PR-Review", "pending", "pr-review-notifyowner-notifyrepo-90.md")
				donePath := filepath.Join(donedir, "pr-review-notifyowner-notifyrepo-90.md")
				fixture := `---
repo: notifyowner/notifyrepo
pr: 90
verdict: APPROVE
decision: post
decision_notes:
diff_file: /tmp/pr-90-123.diff
generated:
---

Already finalized review body.
`
				if err := os.WriteFile(donePath, []byte(fixture), 0644); err != nil {
					t.Fatalf("failed to write done fixture: %v", err)
				}
				return []string{pendingPath}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetchDiffFunc = tt.fetchDiff
			runReviewToolFunc = tt.runReviewTool

			calls, done := installFakeNotifyRecorder(t)

			baseDir := t.TempDir()
			store := NewStore(baseDir)

			rec := Record{
				Repo:       "notifyowner/notifyrepo",
				PR:         90,
				URL:        "https://github.com/notifyowner/notifyrepo/pull/90",
				Status:     StatusWatching,
				EnrolledAt: time.Now().Format(time.RFC3339),
				ReviewDir:  "/tmp/review-90",
			}

			ctx := context.Background()
			err := RunReview(ctx, rec, "shaFail", store, io.Discard)
			if err == nil {
				t.Fatalf("expected RunReview to fail for case %q, got nil error", tt.name)
			}

			// Give a wrongly-fired background goroutine a bounded window
			// to show up before asserting it never did.
			select {
			case c := <-done:
				t.Fatalf("expected notifyFunc to NOT be called on failure path %q, but got: %+v", tt.name, c)
			case <-time.After(200 * time.Millisecond):
			}

			if got := calls(); len(got) != 0 {
				t.Fatalf("expected 0 notifyFunc calls on failure path %q, got %d: %+v", tt.name, len(got), got)
			}
		})
	}
}

// TestRunReview_Notify_StoreSaveFailure_NotCalled verifies notifyFunc is
// never invoked when storeImpl.Save fails, using a fake StoreInterface
// implementation rather than the real Store (Save failure on the real
// filesystem-backed Store is hard to force deterministically).
func TestRunReview_Notify_StoreSaveFailure_NotCalled(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-notify-save-failure.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	calls, done := installFakeNotifyRecorder(t)

	failingStore := &failingSaveStore{saveErr: fmt.Errorf("simulated disk full")}

	rec := Record{
		Repo:       "notifyowner/notifyrepo",
		PR:         91,
		URL:        "https://github.com/notifyowner/notifyrepo/pull/91",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-91",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "shaSaveFail", failingStore, io.Discard)
	if err == nil {
		t.Fatal("expected error from storeImpl.Save failure")
	}
	// failingSaveStore fails on every Save, so RunReview bails at the first
	// one — the StatusReviewing persist that guards the review (added with
	// the reviewing-status feature). Whichever Save fails, the invariant this
	// test guards is the same: no notification fires on a Save failure.
	if !strings.Contains(err.Error(), "failed to persist reviewing status") {
		t.Errorf("unexpected error message: %v", err)
	}

	select {
	case c := <-done:
		t.Fatalf("expected notifyFunc to NOT be called when storeImpl.Save fails, but got: %+v", c)
	case <-time.After(200 * time.Millisecond):
	}

	if got := calls(); len(got) != 0 {
		t.Fatalf("expected 0 notifyFunc calls when storeImpl.Save fails, got %d: %+v", len(got), got)
	}
}

// failingSaveStore is a minimal StoreInterface fake whose Save always
// fails, used to exercise RunReview's storeImpl.Save error path
// deterministically (the real filesystem-backed Store has no easy seam to
// force a Save failure).
type failingSaveStore struct {
	saveErr error
}

func (f *failingSaveStore) Save(rec Record) error {
	return f.saveErr
}

func (f *failingSaveStore) Get(repo string, pr int) (Record, bool, error) {
	return Record{}, false, nil
}

func (f *failingSaveStore) List() ([]Record, error) {
	return nil, nil
}

func (f *failingSaveStore) Remove(repo string, pr int) error {
	return nil
}

// Verify failingSaveStore implements StoreInterface at compile time
var _ StoreInterface = (*failingSaveStore)(nil)

// TestRunReview_Notify_ErrorDoesNotFailRunReview verifies that when
// notifyFunc itself returns an error, RunReview still returns nil overall —
// a notification failure is best-effort and must never surface as a
// RunReview error.
func TestRunReview_Notify_ErrorDoesNotFailRunReview(t *testing.T) {
	origFetchDiff := fetchDiffFunc
	origRunReviewTool := runReviewToolFunc
	origTimeNow := timeNow
	origNotify := notifyFunc
	defer func() {
		fetchDiffFunc = origFetchDiff
		runReviewToolFunc = origRunReviewTool
		timeNow = origTimeNow
		notifyFunc = origNotify
	}()

	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0644)
	}

	spoolPath := writeTestSpoolFixture(t, "pr-review-notify-error.md")
	runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) ([]string, error) {
		return []string{spoolPath}, nil
	}

	notifyCalled := make(chan struct{}, 1)
	notifyFunc = func(title, message string) error {
		notifyCalled <- struct{}{}
		return fmt.Errorf("simulated osascript failure")
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "notifyowner/notifyrepo",
		PR:         92,
		URL:        "https://github.com/notifyowner/notifyrepo/pull/92",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review-92",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, "shaNotifyErr", store, io.Discard)
	if err != nil {
		t.Fatalf("expected RunReview to return nil despite notifyFunc error, got: %v", err)
	}

	select {
	case <-notifyCalled:
		// notifyFunc was invoked as expected; its error was swallowed.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notifyFunc to be called")
	}
}
