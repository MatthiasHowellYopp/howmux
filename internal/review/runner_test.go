package review

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
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

	baseDir := t.TempDir()
	store := NewStore(baseDir)

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

	// Verify record was NOT updated (no save on cancellation)
	_, found, err := store.Get("owner/repo", 5)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if found {
		t.Error("expected record to NOT exist (should not be saved on cancellation)")
	}
}

// TestRunReview_DiffFetchFailure verifies error handling for diff fetch failure
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

	// Verify record was NOT saved
	_, found, err := store.Get("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if found {
		t.Error("expected record to NOT exist (should not be saved on failure)")
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

	// Verify record was NOT saved
	_, found, err := store.Get("owner/repo", 2)
	if err != nil {
		t.Fatalf("failed to check record: %v", err)
	}
	if found {
		t.Error("expected record to NOT exist (should not be saved on failure)")
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

	// The record must never have been saved as StatusReviewed.
	_, found, getErr := store.Get("testowner/testrepo", 43)
	if getErr != nil {
		t.Fatalf("failed to check record: %v", getErr)
	}
	if found {
		t.Error("expected record to NOT exist (should not be saved when metadata stamping fails)")
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
