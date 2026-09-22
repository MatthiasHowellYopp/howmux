package review

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		if exitErr, ok := err.(*exec.ExitError); ok {
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
		if exitErr, ok := err.(*exec.ExitError); ok {
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

// validateSpoolPath checks that a captured stdout line looks like the spool
// path pr_review.py is expected to emit (…/PR-Review/pending/<name>.md). This
// guards against a trailing diagnostic/banner line being persisted as the
// SpoolPath — a wrong value would otherwise only surface downstream when
// finalize tries to drain it.
//
// This is a format check, not an existence check, so it stays unit-testable
// without a real pr_review.py run. A tighter contract (a `SPOOL_PATH=<path>`
// sentinel line grepped from stdout) would remove the last-line ordering
// assumption entirely, but that requires a change to pr_review.py in
// ai-resources; tracked as a follow-up.
func validateSpoolPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if !strings.HasSuffix(p, ".md") {
		return fmt.Errorf("expected a .md file")
	}
	if !strings.Contains(filepath.ToSlash(p), "PR-Review/pending/") {
		return fmt.Errorf("expected a PR-Review/pending/ path")
	}
	return nil
}

// revertToWatching reverts rec to StatusWatching and persists it, used on
// every error exit from RunReview so a record can never remain stuck on
// StatusReviewing after a failed/aborted review attempt. A Save failure
// here is logged, not returned — the caller's original error takes
// precedence, and a record left on StatusReviewing is still re-reviewable
// on the next poll (decideReviewAction does not read Status).
func revertToWatching(storeImpl StoreInterface, rec Record) {
	rec.Status = StatusWatching
	if err := storeImpl.Save(rec); err != nil {
		logging.Warn("failed to revert record to StatusWatching after review error", "repo", rec.Repo, "pr", rec.PR, "error", err)
	}
}

// RunReview orchestrates a complete PR review via subprocess invocation of pr_review.py
// Fetches diff → runs pr_review.py → captures spool path → updates record
func RunReview(ctx context.Context, rec Record, headSHA string, storeImpl StoreInterface, tabWriter io.Writer) error {
	// Parse repo into owner/name
	parts := strings.Split(rec.Repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s (expected owner/name)", rec.Repo)
	}
	owner, repo := parts[0], parts[1]

	// Persist StatusReviewing before any long-running work starts, so the
	// Reviews tab can show this PR as "reviewing" for the duration of the
	// review. If this write itself fails, bail out immediately rather than
	// proceeding with an un-persisted status.
	rec.Status = StatusReviewing
	if err := storeImpl.Save(rec); err != nil {
		return fmt.Errorf("failed to persist reviewing status: %w", err)
	}

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
		revertToWatching(storeImpl, rec)
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
		revertToWatching(storeImpl, rec)
		return fmt.Errorf("pr_review.py invocation failed: %w", err)
	}

	// Parse stdout: pr_review.py prints the spool path as its single stdout
	// line. Take the last non-empty line and validate it against the expected
	// contract before persisting — a silently-wrong SpoolPath would only
	// surface downstream when finalize tries to drain it, far from the cause.
	if len(stdoutLines) == 0 {
		revertToWatching(storeImpl, rec)
		return fmt.Errorf("pr_review.py produced no output (expected spool path)")
	}
	spoolPath := stdoutLines[len(stdoutLines)-1]
	if err := validateSpoolPath(spoolPath); err != nil {
		revertToWatching(storeImpl, rec)
		return fmt.Errorf("pr_review.py returned an unexpected spool path %q: %w", spoolPath, err)
	}
	logging.Debug("review completed", "spool_path", spoolPath)

	// Stamp the reviewed SHA and generation timestamp into the spool file's
	// own front-matter, so the review document is self-describing without
	// cross-referencing the tracking Record. pr_review.py (ai-resources, out
	// of howmux's control) does not populate these reliably today — see
	// WriteReviewedMetadata's doc comment for the full rationale. Use the
	// same generatedAt instant for both the spool file and the Record below
	// so the two stay consistent.
	generatedAt := timeNow()
	if err := WriteReviewedMetadata(spoolPath, headSHA, generatedAt); err != nil {
		revertToWatching(storeImpl, rec)
		return fmt.Errorf("failed to stamp reviewed metadata into spool file %s: %w", spoolPath, err)
	}

	// Update record with StatusReviewed, timestamps, and spool path
	rec.Status = StatusReviewed
	rec.LastReviewedSHA = headSHA
	rec.LastReviewedAt = generatedAt.Format(time.RFC3339)
	rec.LastServicedRequest = headSHA
	rec.SpoolPath = spoolPath

	// Save record atomically. No revertToWatching here: this Save call IS
	// the attempted transition out of StatusReviewing. If it fails, the
	// write never landed, so the record honestly remains StatusReviewing on
	// disk — which is still safely re-reviewable on the next poll, since
	// decideReviewAction never reads Status.
	if err := storeImpl.Save(rec); err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}

	logging.Info("PR review completed", "repo", rec.Repo, "pr", rec.PR, "sha", headSHA, "spool", spoolPath)

	return nil
}
