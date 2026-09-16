package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/acp"
	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// reviewPromptContext assembles the review prompt from prior artifacts
// Pure function that builds the prompt text for the ACP agent
func reviewPromptContext(owner, repo string, pr int, currentSHA string, priorReviews []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Review PR #%d from %s/%s\n", pr, owner, repo))
	sb.WriteString(fmt.Sprintf("Current SHA: %s\n\n", currentSHA))

	if len(priorReviews) == 0 {
		sb.WriteString("This is the first review of this PR.\n")
	} else {
		sb.WriteString(fmt.Sprintf("This PR has %d prior review(s):\n\n", len(priorReviews)))
		for i, review := range priorReviews {
			sb.WriteString(fmt.Sprintf("--- Prior Review %d ---\n", i+1))
			sb.WriteString(review)
			sb.WriteString("\n\n")
		}
	}

	sb.WriteString("Please conduct a thorough review of the current state.\n")

	return sb.String()
}

// loadPriorReviews reads all *.md files from the reviews subdirectory
// Returns empty slice if directory doesn't exist
func loadPriorReviews(baseDir, owner, repo string, pr int) ([]string, error) {
	fullRepo := owner + "/" + repo
	dirName := RecordDir(fullRepo, pr)
	reviewsDir := filepath.Join(baseDir, dirName, "reviews")

	// Return empty slice if directory doesn't exist
	if _, err := os.Stat(reviewsDir); os.IsNotExist(err) {
		return []string{}, nil
	}

	entries, err := os.ReadDir(reviewsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read reviews directory: %w", err)
	}

	var reviews []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(reviewsDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			logging.Warn("failed to read review file", "file", filePath, "error", err)
			continue
		}

		reviews = append(reviews, string(content))
	}

	return reviews, nil
}

// saveReviewArtifact writes review content to the reviews subdirectory
// Uses atomic write (temp + rename) for crash safety
func saveReviewArtifact(baseDir, owner, repo string, pr int, sha string, content string) error {
	fullRepo := owner + "/" + repo
	dirName := RecordDir(fullRepo, pr)
	reviewsDir := filepath.Join(baseDir, dirName, "reviews")

	// Create reviews directory if it doesn't exist
	if err := os.MkdirAll(reviewsDir, 0755); err != nil {
		return fmt.Errorf("failed to create reviews directory: %w", err)
	}

	targetFile := filepath.Join(reviewsDir, fmt.Sprintf("%s.md", sha))

	// Atomic write: create temp file, write, sync, rename
	tmp, err := os.CreateTemp(reviewsDir, "review-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempFile := tmp.Name()

	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tempFile)
		}
	}()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tempFile, targetFile); err != nil {
		return fmt.Errorf("failed to finalize write: %w", err)
	}
	renamed = true

	// Fsync parent directory (best effort)
	if dir, err := os.Open(reviewsDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	return nil
}

// Injectable seams for testing
var (
	loadPriorReviewsFunc   = loadPriorReviews
	saveReviewArtifactFunc = saveReviewArtifact
	timeNow                = time.Now
)

// ACPClientFactory creates an ACP client for a given agent
type ACPClientFactory func(agent string, cwd string) (acp.Client, error)

// defaultACPClientFactory creates a real ACP client
var defaultACPClientFactory ACPClientFactory = func(agent string, cwd string) (acp.Client, error) {
	config := &acp.ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             agent,
		Cwd:               cwd,
		MaxRetries:        3,
		RetryDelay:        1 * time.Second,
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    10 * time.Minute, // 10 min timeout for review
	}

	if err := acp.ValidateConnectionConfig(config); err != nil {
		return nil, fmt.Errorf("invalid ACP config: %w", err)
	}

	client := acp.NewClient(config)
	return client, nil
}

// RunReview orchestrates a complete PR review via ACP
// Loads prior reviews → assembles prompt → runs ACP session → writes artifact → updates record
func RunReview(ctx context.Context, rec Record, baseDir, currentSHA string, storeImpl *Store) error {
	return RunReviewWithFactory(ctx, rec, baseDir, currentSHA, storeImpl, defaultACPClientFactory)
}

// RunReviewWithFactory is the injectable version for testing
func RunReviewWithFactory(ctx context.Context, rec Record, baseDir, currentSHA string, storeImpl *Store, factory ACPClientFactory) error {
	// Parse repo into owner/name
	parts := strings.Split(rec.Repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s (expected owner/name)", rec.Repo)
	}
	owner, repo := parts[0], parts[1]

	logging.Info("starting PR review", "repo", rec.Repo, "pr", rec.PR, "sha", currentSHA)

	// Load prior reviews
	priorReviews, err := loadPriorReviewsFunc(baseDir, owner, repo, rec.PR)
	if err != nil {
		return fmt.Errorf("failed to load prior reviews: %w", err)
	}
	logging.Debug("loaded prior reviews", "count", len(priorReviews))

	// Assemble prompt
	prompt := reviewPromptContext(owner, repo, rec.PR, currentSHA, priorReviews)
	logging.Debug("assembled review prompt", "length", len(prompt))

	// Create ACP client with krew-lead agent
	client, err := factory("krew-lead", rec.ReviewDir)
	if err != nil {
		return fmt.Errorf("failed to create ACP client: %w", err)
	}

	// Connect to ACP
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to ACP: %w", err)
	}
	defer client.Close()

	logging.Info("ACP connection established", "agent", "krew-lead")

	// Send review request with 10min timeout
	reviewCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	req := &acp.MessageRequest{
		Agent:          "krew-lead",
		Message:        prompt,
		Streaming:      false,
		ResponseFormat: "text",
		Timeout:        10 * time.Minute,
	}

	resp, err := client.SendMessage(reviewCtx, req)
	if err != nil {
		return fmt.Errorf("ACP review request failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("review failed: %s", resp.Error)
	}

	logging.Info("review completed successfully", "response_length", len(resp.Message))

	// Save review artifact
	if err := saveReviewArtifactFunc(baseDir, owner, repo, rec.PR, currentSHA, resp.Message); err != nil {
		return fmt.Errorf("failed to save review artifact: %w", err)
	}
	logging.Debug("review artifact saved", "sha", currentSHA)

	// Update record
	rec.Status = StatusDone
	rec.LastReviewedSHA = currentSHA
	rec.LastReviewedAt = timeNow().Format(time.RFC3339)

	if err := storeImpl.Save(rec); err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}

	logging.Info("PR review completed", "repo", rec.Repo, "pr", rec.PR, "sha", currentSHA)

	return nil
}
