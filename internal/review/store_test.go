package review

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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

	// Compare all fields
	if got.Repo != rec.Repo {
		t.Errorf("Repo mismatch: got %q, want %q", got.Repo, rec.Repo)
	}
	if got.PR != rec.PR {
		t.Errorf("PR mismatch: got %d, want %d", got.PR, rec.PR)
	}
	if got.URL != rec.URL {
		t.Errorf("URL mismatch: got %q, want %q", got.URL, rec.URL)
	}
	if got.Status != rec.Status {
		t.Errorf("Status mismatch: got %q, want %q", got.Status, rec.Status)
	}
	if got.EnrolledAt != rec.EnrolledAt {
		t.Errorf("EnrolledAt mismatch: got %q, want %q", got.EnrolledAt, rec.EnrolledAt)
	}
	if got.ReviewDir != rec.ReviewDir {
		t.Errorf("ReviewDir mismatch: got %q, want %q", got.ReviewDir, rec.ReviewDir)
	}
}

func TestStoreList(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Save 3 records for different PRs
	records := []Record{
		{
			Repo:       "owner1/repo1",
			PR:         42,
			URL:        "https://github.com/owner1/repo1/pull/42",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  ".worktrees/review-owner1-repo1-42",
		},
		{
			Repo:       "owner2/repo2",
			PR:         123,
			URL:        "https://github.com/owner2/repo2/pull/123",
			Status:     StatusReviewing,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  ".worktrees/review-owner2-repo2-123",
		},
		{
			Repo:       "owner3/repo3",
			PR:         7,
			URL:        "https://github.com/owner3/repo3/pull/7",
			Status:     StatusDone,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  ".worktrees/review-owner3-repo3-7",
		},
	}

	for _, rec := range records {
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save failed for %s#%d: %v", rec.Repo, rec.PR, err)
		}
	}

	// List all
	got, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(got) != 3 {
		t.Errorf("List returned %d records, want 3", len(got))
	}

	// Verify all records are present
	foundRepos := make(map[string]bool)
	for _, rec := range got {
		key := rec.Repo + "#" + string(rune(rec.PR))
		foundRepos[key] = true
	}

	for _, want := range records {
		found := false
		for _, got := range got {
			if got.Repo == want.Repo && got.PR == want.PR {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("List missing record: %s#%d", want.Repo, want.PR)
		}
	}
}

func TestStoreRemove(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Save 2 records
	rec1 := Record{
		Repo:       "owner1/repo1",
		PR:         42,
		URL:        "https://github.com/owner1/repo1/pull/42",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  ".worktrees/review-owner1-repo1-42",
	}
	rec2 := Record{
		Repo:       "owner2/repo2",
		PR:         123,
		URL:        "https://github.com/owner2/repo2/pull/123",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  ".worktrees/review-owner2-repo2-123",
	}

	if err := store.Save(rec1); err != nil {
		t.Fatalf("Save rec1 failed: %v", err)
	}
	if err := store.Save(rec2); err != nil {
		t.Fatalf("Save rec2 failed: %v", err)
	}

	// Remove rec1
	if err := store.Remove("owner1/repo1", 42); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify rec1 is gone
	got, found, err := store.Get("owner1/repo1", 42)
	if err != nil {
		t.Fatalf("Get after Remove failed: %v", err)
	}
	if found {
		t.Errorf("Get after Remove found deleted record: %+v", got)
	}

	// Verify rec2 still exists
	got2, found2, err := store.Get("owner2/repo2", 123)
	if err != nil {
		t.Fatalf("Get rec2 failed: %v", err)
	}
	if !found2 {
		t.Error("Get rec2 returned not found after removing rec1")
	}
	if got2.PR != 123 {
		t.Errorf("rec2 corrupted: got PR %d, want 123", got2.PR)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Get non-existent record
	got, found, err := store.Get("nonexistent/repo", 999)
	if err != nil {
		t.Fatalf("Get returned error for non-existent record: %v", err)
	}
	if found {
		t.Errorf("Get returned found=true for non-existent record: %+v", got)
	}
	if got.Repo != "" || got.PR != 0 {
		t.Errorf("Get returned non-zero record for non-existent: %+v", got)
	}
}

func TestStoreAtomicWrite(t *testing.T) {
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

	if err := store.Save(rec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify no .tmp file remains
	dirName := RecordDir(rec.Repo, rec.PR)
	prDir := filepath.Join(baseDir, dirName)
	tmpFile := filepath.Join(prDir, "record.json.tmp")

	if _, err := os.Stat(tmpFile); err == nil {
		t.Error("temp file still exists after successful Save")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error checking for temp file: %v", err)
	}

	// Verify record.json exists
	recordFile := filepath.Join(prDir, "record.json")
	if _, err := os.Stat(recordFile); err != nil {
		t.Errorf("record.json does not exist: %v", err)
	}
}

func TestStoreValidationError(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Invalid record (missing required fields)
	rec := Record{
		Repo:   "", // Missing
		PR:     123,
		URL:    "https://github.com/owner/name/pull/123",
		Status: StatusWatching,
	}

	err := store.Save(rec)
	if err == nil {
		t.Fatal("Save should fail for invalid record")
	}

	// Verify no directory was created (validation failed before I/O)
	entries, _ := os.ReadDir(baseDir)
	if len(entries) > 0 {
		t.Errorf("Save created directories despite validation failure: %v", entries)
	}
}

func TestStoreReviewsDir(t *testing.T) {
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

	if err := store.Save(rec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify reviews/ subdirectory exists
	dirName := RecordDir(rec.Repo, rec.PR)
	reviewsSubdir := filepath.Join(baseDir, dirName, "reviews")

	stat, err := os.Stat(reviewsSubdir)
	if err != nil {
		t.Fatalf("reviews/ subdirectory does not exist: %v", err)
	}
	if !stat.IsDir() {
		t.Error("reviews/ is not a directory")
	}
}

func TestStoreConcurrentSaves(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	var wg sync.WaitGroup
	numGoroutines := 10

	// 10 goroutines save different PRs concurrently
	for i := 1; i <= numGoroutines; i++ {
		wg.Add(1)
		go func(prNum int) {
			defer wg.Done()

			rec := Record{
				Repo:       "owner/repo",
				PR:         prNum,
				URL:        "https://github.com/owner/repo/pull/" + string(rune(prNum)),
				Status:     StatusWatching,
				EnrolledAt: time.Now().Format(time.RFC3339),
				ReviewDir:  ".worktrees/review-owner-repo-" + string(rune(prNum)),
			}

			if err := store.Save(rec); err != nil {
				t.Errorf("Save failed for PR %d: %v", prNum, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all records were saved
	records, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != numGoroutines {
		t.Errorf("List returned %d records, want %d", len(records), numGoroutines)
	}
}

func TestStoreEndToEnd(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Save a record with StatusWatching
	rec := Record{
		Repo:       "owner/name",
		PR:         123,
		URL:        "https://github.com/owner/name/pull/123",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  ".worktrees/review-owner-name-123",
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Get it back
	got, found, err := store.Get("owner/name", 123)
	if err != nil || !found {
		t.Fatalf("Get failed: found=%v, err=%v", found, err)
	}
	if got.Status != StatusWatching {
		t.Errorf("initial status: got %q, want %q", got.Status, StatusWatching)
	}

	// Update to StatusReviewing
	got.Status = StatusReviewing
	got.LastReviewedAt = time.Now().Format(time.RFC3339)
	got.LastReviewedSHA = "abc123"

	if err := store.Save(got); err != nil {
		t.Fatalf("update Save failed: %v", err)
	}

	// Get it again
	got2, found2, err := store.Get("owner/name", 123)
	if err != nil || !found2 {
		t.Fatalf("Get after update failed: found=%v, err=%v", found2, err)
	}
	if got2.Status != StatusReviewing {
		t.Errorf("updated status: got %q, want %q", got2.Status, StatusReviewing)
	}
	if got2.LastReviewedSHA != "abc123" {
		t.Errorf("LastReviewedSHA: got %q, want %q", got2.LastReviewedSHA, "abc123")
	}

	// List shows 1 record
	list, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List returned %d records, want 1", len(list))
	}

	// Remove it
	if err := store.Remove("owner/name", 123); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// List shows 0 records
	list2, err := store.List()
	if err != nil {
		t.Fatalf("List after Remove failed: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("List after Remove returned %d records, want 0", len(list2))
	}
}

func TestStoreListEmpty(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// List on empty directory should return empty slice, not error
	list, err := store.List()
	if err != nil {
		t.Fatalf("List on empty dir failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List on empty dir returned %d records, want 0", len(list))
	}
}

func TestStoreRemoveIdempotent(t *testing.T) {
	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Remove non-existent record should not error
	err := store.Remove("nonexistent/repo", 999)
	if err != nil {
		t.Errorf("Remove non-existent record returned error: %v", err)
	}

	// Remove twice should not error
	rec := Record{
		Repo:       "owner/name",
		PR:         123,
		URL:        "https://github.com/owner/name/pull/123",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  ".worktrees/review-owner-name-123",
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// First remove
	if err := store.Remove("owner/name", 123); err != nil {
		t.Fatalf("first Remove failed: %v", err)
	}

	// Second remove (should be idempotent)
	if err := store.Remove("owner/name", 123); err != nil {
		t.Errorf("second Remove failed: %v", err)
	}
}

func TestNewDefaultStore(t *testing.T) {
	store := NewDefaultStore()
	if store == nil {
		t.Fatal("NewDefaultStore returned nil")
	}
	if store.baseDir != ".howmux/reviews" {
		t.Errorf("NewDefaultStore baseDir: got %q, want %q", store.baseDir, ".howmux/reviews")
	}
}
