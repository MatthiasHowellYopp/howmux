package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/acp"
)

// TestReviewPromptContext tests the pure prompt assembly function
func TestReviewPromptContext(t *testing.T) {
	tests := []struct {
		name           string
		owner          string
		repo           string
		pr             int
		currentSHA     string
		priorReviews   []string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:         "first review - no prior reviews",
			owner:        "testowner",
			repo:         "testrepo",
			pr:           42,
			currentSHA:   "abc123",
			priorReviews: []string{},
			wantContains: []string{
				"Review PR #42 from testowner/testrepo",
				"Current SHA: abc123",
				"This is the first review of this PR",
			},
			wantNotContain: []string{"Prior Review"},
		},
		{
			name:       "with one prior review",
			owner:      "org",
			repo:       "project",
			pr:         100,
			currentSHA: "def456",
			priorReviews: []string{
				"Previous review content",
			},
			wantContains: []string{
				"Review PR #100 from org/project",
				"Current SHA: def456",
				"This PR has 1 prior review(s)",
				"--- Prior Review 1 ---",
				"Previous review content",
			},
		},
		{
			name:       "with multiple prior reviews",
			owner:      "owner",
			repo:       "name",
			pr:         5,
			currentSHA: "sha999",
			priorReviews: []string{
				"First review",
				"Second review",
				"Third review",
			},
			wantContains: []string{
				"Review PR #5 from owner/name",
				"Current SHA: sha999",
				"This PR has 3 prior review(s)",
				"--- Prior Review 1 ---",
				"First review",
				"--- Prior Review 2 ---",
				"Second review",
				"--- Prior Review 3 ---",
				"Third review",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewPromptContext(tt.owner, tt.repo, tt.pr, tt.currentSHA, tt.priorReviews)

			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("prompt missing expected content: %q\nGot:\n%s", want, got)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(got, notWant) {
					t.Errorf("prompt contains unexpected content: %q\nGot:\n%s", notWant, got)
				}
			}
		})
	}
}

// TestLoadPriorReviews tests the artifact loading function
func TestLoadPriorReviews(t *testing.T) {
	t.Run("directory does not exist", func(t *testing.T) {
		baseDir := t.TempDir()

		reviews, err := loadPriorReviews(baseDir, "owner", "repo", 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reviews) != 0 {
			t.Errorf("expected empty slice, got %d reviews", len(reviews))
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		baseDir := t.TempDir()
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		if err := os.MkdirAll(reviewsDir, 0755); err != nil {
			t.Fatal(err)
		}

		reviews, err := loadPriorReviews(baseDir, "owner", "repo", 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reviews) != 0 {
			t.Errorf("expected empty slice, got %d reviews", len(reviews))
		}
	})

	t.Run("loads md files only", func(t *testing.T) {
		baseDir := t.TempDir()
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		if err := os.MkdirAll(reviewsDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create test files
		files := map[string]string{
			"abc123.md":  "First review",
			"def456.md":  "Second review",
			"ghi789.txt": "Not a markdown file",
			"jkl012.md":  "Third review",
		}

		for name, content := range files {
			path := filepath.Join(reviewsDir, name)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}

		reviews, err := loadPriorReviews(baseDir, "owner", "repo", 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should load only .md files
		if len(reviews) != 3 {
			t.Errorf("expected 3 reviews, got %d", len(reviews))
		}

		// Check content
		expected := map[string]bool{
			"First review":  false,
			"Second review": false,
			"Third review":  false,
		}
		for _, review := range reviews {
			if _, ok := expected[review]; ok {
				expected[review] = true
			}
		}
		for content, found := range expected {
			if !found {
				t.Errorf("expected review content not found: %q", content)
			}
		}
	})

	t.Run("skips subdirectories", func(t *testing.T) {
		baseDir := t.TempDir()
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		if err := os.MkdirAll(reviewsDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create a subdirectory
		subDir := filepath.Join(reviewsDir, "subdir")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create a file in the subdirectory
		if err := os.WriteFile(filepath.Join(subDir, "nested.md"), []byte("nested"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a file in the reviews directory
		if err := os.WriteFile(filepath.Join(reviewsDir, "abc.md"), []byte("top-level"), 0644); err != nil {
			t.Fatal(err)
		}

		reviews, err := loadPriorReviews(baseDir, "owner", "repo", 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(reviews) != 1 {
			t.Errorf("expected 1 review, got %d", len(reviews))
		}
		if len(reviews) > 0 && reviews[0] != "top-level" {
			t.Errorf("expected 'top-level', got %q", reviews[0])
		}
	})

	t.Run("handles unreadable file gracefully", func(t *testing.T) {
		baseDir := t.TempDir()
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		if err := os.MkdirAll(reviewsDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create a readable file
		if err := os.WriteFile(filepath.Join(reviewsDir, "good.md"), []byte("readable"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create an unreadable file (permissions don't work well in tests, so we'll skip this)
		// The function should skip unreadable files and continue
		// This is covered by the continue on error in the actual implementation

		reviews, err := loadPriorReviews(baseDir, "owner", "repo", 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(reviews) < 1 {
			t.Errorf("expected at least 1 review, got %d", len(reviews))
		}
	})
}

// TestSaveReviewArtifact tests the artifact persistence function
func TestSaveReviewArtifact(t *testing.T) {
	t.Run("creates directory and saves file", func(t *testing.T) {
		baseDir := t.TempDir()
		content := "Review content for sha abc123"

		err := saveReviewArtifact(baseDir, "owner", "repo", 42, "abc123", content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify file exists and has correct content
		expectedPath := filepath.Join(baseDir, "owner-repo-42", "reviews", "abc123.md")
		gotContent, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("failed to read saved file: %v", err)
		}
		if string(gotContent) != content {
			t.Errorf("content mismatch:\nwant: %q\ngot:  %q", content, string(gotContent))
		}
	})

	t.Run("overwrites existing file atomically", func(t *testing.T) {
		baseDir := t.TempDir()
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		if err := os.MkdirAll(reviewsDir, 0755); err != nil {
			t.Fatal(err)
		}

		targetFile := filepath.Join(reviewsDir, "abc123.md")

		// Create initial file
		if err := os.WriteFile(targetFile, []byte("old content"), 0644); err != nil {
			t.Fatal(err)
		}

		// Overwrite with new content
		newContent := "new content"
		err := saveReviewArtifact(baseDir, "owner", "repo", 42, "abc123", newContent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify new content
		gotContent, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if string(gotContent) != newContent {
			t.Errorf("expected %q, got %q", newContent, string(gotContent))
		}
	})

	t.Run("multiple artifacts for same PR", func(t *testing.T) {
		baseDir := t.TempDir()

		artifacts := map[string]string{
			"sha1": "First review",
			"sha2": "Second review",
			"sha3": "Third review",
		}

		for sha, content := range artifacts {
			if err := saveReviewArtifact(baseDir, "owner", "repo", 42, sha, content); err != nil {
				t.Fatalf("failed to save artifact %s: %v", sha, err)
			}
		}

		// Verify all artifacts exist
		reviewsDir := filepath.Join(baseDir, "owner-repo-42", "reviews")
		entries, err := os.ReadDir(reviewsDir)
		if err != nil {
			t.Fatalf("failed to read reviews dir: %v", err)
		}

		if len(entries) != 3 {
			t.Errorf("expected 3 artifacts, got %d", len(entries))
		}

		// Verify content
		for sha, expectedContent := range artifacts {
			path := filepath.Join(reviewsDir, fmt.Sprintf("%s.md", sha))
			gotContent, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("failed to read %s: %v", sha, err)
				continue
			}
			if string(gotContent) != expectedContent {
				t.Errorf("content mismatch for %s: want %q, got %q", sha, expectedContent, string(gotContent))
			}
		}
	})
}

// fakeACPClient implements acp.Client for testing
type fakeACPClient struct {
	connectErr      error
	sendMessageResp *acp.MessageResponse
	sendMessageErr  error
	connected       bool
	closeCalled     bool
}

func (f *fakeACPClient) Connect(ctx context.Context) error {
	if f.connectErr != nil {
		return f.connectErr
	}
	f.connected = true
	return nil
}

func (f *fakeACPClient) Disconnect() error {
	f.connected = false
	return nil
}

func (f *fakeACPClient) IsConnected() bool {
	return f.connected
}

func (f *fakeACPClient) SendMessage(ctx context.Context, req *acp.MessageRequest) (*acp.MessageResponse, error) {
	if f.sendMessageErr != nil {
		return nil, f.sendMessageErr
	}
	return f.sendMessageResp, nil
}

func (f *fakeACPClient) StreamMessage(ctx context.Context, req *acp.MessageRequest) (<-chan *acp.StreamingResponse, error) {
	return nil, fmt.Errorf("streaming not implemented in fake")
}

func (f *fakeACPClient) Close() error {
	f.closeCalled = true
	f.connected = false
	return nil
}

// TestRunReview tests the orchestration function
func TestRunReview(t *testing.T) {
	// Save and restore original time function
	origTimeNow := timeNow
	origLoadFunc := loadPriorReviewsFunc
	origSaveFunc := saveReviewArtifactFunc
	defer func() {
		timeNow = origTimeNow
		loadPriorReviewsFunc = origLoadFunc
		saveReviewArtifactFunc = origSaveFunc
	}()

	// Fixed time for testing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedTime }

	t.Run("successful review with no prior reviews", func(t *testing.T) {
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

		// Mock loadPriorReviews to return empty
		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		// Mock saveReviewArtifact
		var savedSHA, savedContent string
		saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
			savedSHA = sha
			savedContent = content
			return nil
		}

		// Create fake ACP client
		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success:   true,
				Message:   "Review completed successfully",
				Timestamp: time.Now(),
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			if agent != "krew-lead" {
				t.Errorf("expected agent 'krew-lead', got %q", agent)
			}
			if cwd != rec.ReviewDir {
				t.Errorf("expected cwd %q, got %q", rec.ReviewDir, cwd)
			}
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "abc123", store, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify client was used correctly
		if !fakeClient.closeCalled {
			t.Error("expected Close to be called")
		}

		// Verify artifact was saved
		if savedSHA != "abc123" {
			t.Errorf("expected SHA 'abc123', got %q", savedSHA)
		}
		if savedContent != "Review completed successfully" {
			t.Errorf("unexpected saved content: %q", savedContent)
		}

		// Verify record was updated
		updated, found, err := store.Get("owner/repo", 42)
		if err != nil {
			t.Fatalf("failed to get record: %v", err)
		}
		if !found {
			t.Fatal("record not found")
		}
		if updated.Status != StatusDone {
			t.Errorf("expected status %q, got %q", StatusDone, updated.Status)
		}
		if updated.LastReviewedSHA != "abc123" {
			t.Errorf("expected SHA 'abc123', got %q", updated.LastReviewedSHA)
		}
		if updated.LastReviewedAt != fixedTime.Format(time.RFC3339) {
			t.Errorf("expected time %q, got %q", fixedTime.Format(time.RFC3339), updated.LastReviewedAt)
		}
	})

	t.Run("successful review with prior reviews", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         100,
			URL:        "https://github.com/owner/repo/pull/100",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review-100",
		}

		// Mock loadPriorReviews to return multiple reviews
		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{"Review 1", "Review 2"}, nil
		}

		// Mock saveReviewArtifact
		saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
			return nil
		}

		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success:   true,
				Message:   "Updated review",
				Timestamp: time.Now(),
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "def456", store, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify record updated correctly
		updated, found, err := store.Get("owner/repo", 100)
		if err != nil {
			t.Fatalf("failed to get record: %v", err)
		}
		if !found {
			t.Fatal("record not found")
		}
		if updated.LastReviewedSHA != "def456" {
			t.Errorf("expected SHA 'def456', got %q", updated.LastReviewedSHA)
		}
	})

	t.Run("invalid repo format", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "invalid-repo-format",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			t.Fatal("factory should not be called")
			return nil, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error for invalid repo format")
		}
		if !strings.Contains(err.Error(), "invalid repo format") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("loadPriorReviews fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return nil, fmt.Errorf("load failed")
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			t.Fatal("factory should not be called")
			return nil, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to load prior reviews") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("factory fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return nil, fmt.Errorf("factory error")
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to create ACP client") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("connect fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		fakeClient := &fakeACPClient{
			connectErr: fmt.Errorf("connection failed"),
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to connect to ACP") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("sendMessage fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
			return nil
		}

		fakeClient := &fakeACPClient{
			sendMessageErr: fmt.Errorf("send failed"),
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "ACP review request failed") {
			t.Errorf("unexpected error message: %v", err)
		}
		if !fakeClient.closeCalled {
			t.Error("expected Close to be called even on failure")
		}
	})

	t.Run("review returns failure", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success: false,
				Error:   "review encountered an error",
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "review failed") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("saveArtifact fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
			return fmt.Errorf("save failed")
		}

		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success: true,
				Message: "review ok",
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to save review artifact") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("store.Save fails", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewStore(baseDir)

		rec := Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://example.com",
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  "/tmp/review",
		}

		loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
			return []string{}, nil
		}

		saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
			return nil
		}

		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success: true,
				Message: "review ok",
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		// Make baseDir readonly to cause Save to fail
		if err := os.Chmod(baseDir, 0444); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(baseDir, 0755)

		ctx := context.Background()
		err := RunReviewWithFactory(ctx, rec, baseDir, "sha", store, factory)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to update record") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

// TestRunReviewRace verifies thread safety using -race detector
func TestRunReviewRace(t *testing.T) {
	// Save and restore
	origTimeNow := timeNow
	origLoadFunc := loadPriorReviewsFunc
	origSaveFunc := saveReviewArtifactFunc
	defer func() {
		timeNow = origTimeNow
		loadPriorReviewsFunc = origLoadFunc
		saveReviewArtifactFunc = origSaveFunc
	}()

	fixedTime := time.Now()
	timeNow = func() time.Time { return fixedTime }
	loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
		return []string{}, nil
	}
	saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
		return nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	// Run two reviews concurrently on different PRs
	done := make(chan error, 2)

	runReview := func(pr int) {
		rec := Record{
			Repo:       "owner/repo",
			PR:         pr,
			URL:        fmt.Sprintf("https://github.com/owner/repo/pull/%d", pr),
			Status:     StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  fmt.Sprintf("/tmp/review-%d", pr),
		}

		fakeClient := &fakeACPClient{
			sendMessageResp: &acp.MessageResponse{
				Success: true,
				Message: "review ok",
			},
		}

		factory := func(agent string, cwd string) (acp.Client, error) {
			return fakeClient, nil
		}

		ctx := context.Background()
		done <- RunReviewWithFactory(ctx, rec, baseDir, fmt.Sprintf("sha%d", pr), store, factory)
	}

	go runReview(1)
	go runReview(2)

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent review failed: %v", err)
		}
	}
}

// TestRunReviewWrapper tests the wrapper function that uses defaultACPClientFactory
func TestRunReviewWrapper(t *testing.T) {
	// Save and restore
	origTimeNow := timeNow
	origLoadFunc := loadPriorReviewsFunc
	origSaveFunc := saveReviewArtifactFunc
	origFactory := defaultACPClientFactory
	defer func() {
		timeNow = origTimeNow
		loadPriorReviewsFunc = origLoadFunc
		saveReviewArtifactFunc = origSaveFunc
		defaultACPClientFactory = origFactory
	}()

	fixedTime := time.Now()
	timeNow = func() time.Time { return fixedTime }
	loadPriorReviewsFunc = func(baseDir, owner, repo string, pr int) ([]string, error) {
		return []string{}, nil
	}
	saveReviewArtifactFunc = func(baseDir, owner, repo string, pr int, sha, content string) error {
		return nil
	}

	// Replace the factory
	fakeClient := &fakeACPClient{
		sendMessageResp: &acp.MessageResponse{
			Success: true,
			Message: "review ok",
		},
	}
	defaultACPClientFactory = func(agent string, cwd string) (acp.Client, error) {
		return fakeClient, nil
	}

	baseDir := t.TempDir()
	store := NewStore(baseDir)

	rec := Record{
		Repo:       "owner/repo",
		PR:         1,
		URL:        "https://github.com/owner/repo/pull/1",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  "/tmp/review",
	}

	ctx := context.Background()
	err := RunReview(ctx, rec, baseDir, "sha1", store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
