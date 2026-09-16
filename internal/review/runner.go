package review

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// Injectable command seams for subprocess execution
// These use exec.CommandContext for cancellation support and are replaceable in tests

// fetchDiffFunc fetches a PR diff via gh CLI and writes it to outputFile
var fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", strconv.Itoa(pr), "--repo", owner+"/"+repo)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors, ok := err.(*exec.ExitError); ok {
			exitErr = errors
			return fmt.Errorf("gh pr diff failed (exit %d): %s: %w", exitErr.ExitCode(), string(exitErr.Stderr), err)
		}
		return fmt.Errorf("gh pr diff failed: %w", err)
	}
	return os.WriteFile(outputFile, output, 0644)
}

// runReviewToolFunc runs pr_review.py (or any command) with the given argv,
// streams stderr to stderrWriter for progress visibility, and captures stdout lines
var runReviewToolFunc = func(ctx context.Context, argv []string, stderrWriter io.Writer) (stdoutLines []string, err error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv is empty")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = stderrWriter

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors, ok := err.(*exec.ExitError); ok {
			exitErr = errors
			return nil, fmt.Errorf("command failed (exit %d): %w", exitErr.ExitCode(), err)
		}
		return nil, fmt.Errorf("command failed: %w", err)
	}

	// Parse stdout into lines, filtering out empty lines
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var nonEmptyLines []string
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			nonEmptyLines = append(nonEmptyLines, trimmed)
		}
	}

	return nonEmptyLines, nil
}

// Injectable seam for time (used for timestamps)
var timeNow = time.Now

// RunReview orchestrates a complete PR review via subprocess invocation of pr_review.py
// Fetches diff → runs pr_review.py → captures spool path → updates record
func RunReview(ctx context.Context, rec Record, headSHA string, storeImpl StoreInterface, tabWriter io.Writer) error {
	// Parse repo into owner/name
	parts := strings.Split(rec.Repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s (expected owner/name)", rec.Repo)
	}
	owner, repo := parts[0], parts[1]

	logging.Info("starting PR review", "repo", rec.Repo, "pr", rec.PR, "sha", headSHA)

	// Create temp file for diff
	diffFile, err := os.CreateTemp("", fmt.Sprintf("pr-%d-*.diff", rec.PR))
	if err != nil {
		return fmt.Errorf("failed to create temp diff file: %w", err)
	}
	diffPath := diffFile.Name()
	diffFile.Close()
	defer os.Remove(diffPath)

	// Fetch PR diff via gh CLI
	if err := fetchDiffFunc(ctx, owner, repo, rec.PR, diffPath); err != nil {
		return fmt.Errorf("failed to fetch PR diff: %w", err)
	}
	logging.Debug("fetched PR diff", "file", diffPath)

	// Construct pr_review.py argv
	// Always use --language auto to let pr_review.py detect both language and valkey
	argv := []string{
		"pr_review.py",
		"--diff-file", diffPath,
		"--repo", rec.Repo,
		"--pr", strconv.Itoa(rec.PR),
		"--language", "auto",
	}

	logging.Debug("invoking pr_review.py", "argv", strings.Join(argv, " "))

	// Run pr_review.py with stderr streaming to tabWriter and stdout capture
	stdoutLines, err := runReviewToolFunc(ctx, argv, tabWriter)
	if err != nil {
		return fmt.Errorf("pr_review.py invocation failed: %w", err)
	}

	// Parse stdout: last non-empty line is the spool path
	if len(stdoutLines) == 0 {
		return fmt.Errorf("pr_review.py produced no output (expected spool path)")
	}
	spoolPath := stdoutLines[len(stdoutLines)-1]
	logging.Debug("review completed", "spool_path", spoolPath)

	// Update record with StatusReviewed, timestamps, and spool path
	rec.Status = StatusReviewed
	rec.LastReviewedSHA = headSHA
	rec.LastReviewedAt = timeNow().Format(time.RFC3339)
	rec.LastServicedRequest = headSHA
	rec.SpoolPath = spoolPath

	// Save record atomically
	if err := storeImpl.Save(rec); err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}

	logging.Info("PR review completed", "repo", rec.Repo, "pr", rec.PR, "sha", headSHA, "spool", spoolPath)

	return nil
}
