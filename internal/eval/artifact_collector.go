package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// collectArtifacts collects files produced by agents during evaluation and returns their content.
//
// WHY THIS EXISTS:
// The ACP transport change altered what gets scored by the evaluation framework:
//   - Pre-ACP: actualOutput contained full stdout, including spec content printed inline
//   - ACP: actualOutput contains only narration; artifacts arrive as ToolCall events not captured
//
// This function bridges that gap by reading artifacts from the filesystem after the agent turn
// and appending their content to actualOutput so rubric grading operates on the real deliverable.
//
// Example flow:
//  1. Agent writes spec to .howmux/specs/issue-123-feature.md via ToolCall event
//  2. ACP returns only "I've analyzed the issue and created a spec..." as text
//  3. collectArtifacts finds the spec file and reads its content
//  4. Content gets appended: "I've analyzed... --- PRODUCED ARTIFACT --- # Spec content here"
//
// Per-agent patterns:
//   - architect: .howmux/specs/issue-*.md (design specifications)
//   - documenter: app_docs/feature-*.md (feature documentation)
//   - others: empty string (no artifacts expected)
func collectArtifacts(agent, workspaceDir string) string {
	var patterns []string

	switch agent {
	case "architect":
		patterns = []string{filepath.Join(workspaceDir, ".howmux", "specs", "issue-*.md")}
	case "documenter":
		patterns = []string{filepath.Join(workspaceDir, "app_docs", "feature-*.md")}
	case "builder":
		// Builder produces arbitrary source files, so we use git diff to capture changes
		return collectBuilderDiff(workspaceDir)
	default:
		// No single globbable artifact for other agents:
		//   - validator / krew-lead produce no written deliverable to grade.
		return ""
	}

	var artifacts []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue // Skip on glob error
		}

		for _, match := range matches {
			content, err := os.ReadFile(match)
			if err != nil {
				continue // Skip unreadable files
			}

			relPath, _ := filepath.Rel(workspaceDir, match)
			artifact := fmt.Sprintf("=== %s ===\n%s", relPath, string(content))
			artifacts = append(artifacts, artifact)
		}
	}

	if len(artifacts) == 0 {
		return ""
	}

	return "\n--- PRODUCED ARTIFACT ---\n" + strings.Join(artifacts, "\n---\n")
}

// initWorkspaceGitBaseline initializes workspaceDir as a git repository with a
// committed baseline of its current contents. The builder artifact collector
// captures the agent's work with `git diff HEAD`; that only works if the
// workspace is a repo whose HEAD reflects the pre-agent state. Without this the
// diff command fails, the collector returns nothing, and the builder is scored
// on ACP narration instead of the code it produced.
//
// The .kiro symlink (agent/skill definitions linked in during setup) is
// git-ignored so it never appears in the diff — only the agent's real changes
// do. Uses a local git identity so the commit succeeds without relying on the
// host's global git config.
func initWorkspaceGitBaseline(workspaceDir string) error {
	// Ignore the .kiro symlink so it stays out of the baseline and the diff.
	gitignore := filepath.Join(workspaceDir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte(".kiro\n"), 0644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}

	steps := [][]string{
		{"init"},
		{"config", "user.email", "eval@howmux.local"},
		{"config", "user.name", "howmux-eval"},
		{"add", "-A"},
		{"commit", "--no-gpg-sign", "-m", "eval workspace baseline"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = workspaceDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// collectBuilderDiff captures builder changes via git diff, filtering out harness paths.
//
// WHY THIS APPROACH:
// Builder produces arbitrary source files (not a single globbable path pattern like architect's
// specs or documenter's feature docs). The rubric grades code_correctness / spec_adherence /
// test_coverage across changed code, so we need the actual diff to evaluate what was built.
//
// Strategy:
//   - Run `git diff HEAD --unified=3` to capture all changes since the last commit
//   - Filter out harness paths (.howmux/, .kiro/, .git/) since these aren't builder deliverables
//   - Return formatted diff with section header, or empty string on failure/no changes
//
// Fail-safe: Returns empty string rather than crashing on git errors or missing repo.
func collectBuilderDiff(workspaceDir string) string {
	// Mark new files as intent-to-add so they appear in `git diff HEAD`. Builders
	// commonly create new source/test files; without this, `git diff HEAD` shows
	// only modifications to already-tracked files and brand-new files would be
	// silently omitted from what the scorer sees. (.kiro is git-ignored, so it is
	// not picked up here.)
	addN := exec.Command("git", "add", "-N", ".")
	addN.Dir = workspaceDir
	if _, err := addN.CombinedOutput(); err != nil {
		// Not a repo / no HEAD yet — nothing to diff against. Return empty; the
		// workspace-setup baseline (runner.go) is responsible for making this a
		// repo, and a failure there is surfaced separately.
		return ""
	}

	cmd := exec.Command("git", "diff", "HEAD", "--unified=3")
	cmd.Dir = workspaceDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Git command failed (not a repo, no HEAD, etc.) - return empty gracefully
		return ""
	}

	diff := string(output)
	if diff == "" {
		// No changes detected
		return ""
	}

	// Filter out harness paths
	filtered := filterHarnessPaths(diff)
	if filtered == "" {
		// Only harness files changed, nothing to report
		return ""
	}

	return fmt.Sprintf("\n--- PRODUCED ARTIFACT ---\n=== Git Diff (Builder Changes) ===\n%s", filtered)
}

// filterHarnessPaths removes diff hunks for harness paths (.howmux/, .kiro/, .git/).
//
// A diff hunk starts with "diff --git" and continues until the next "diff --git" or EOF.
// We parse line-by-line, accumulating hunks for non-harness files only.
func filterHarnessPaths(diff string) string {
	lines := strings.Split(diff, "\n")
	var result []string
	var currentHunk []string
	var inHarnessFile bool

	harnessPatterns := []string{".howmux/", ".kiro/", ".git/"}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			// New hunk starting - save previous if not harness
			if len(currentHunk) > 0 && !inHarnessFile {
				result = append(result, strings.Join(currentHunk, "\n"))
			}

			// Reset for new hunk
			currentHunk = []string{line}
			inHarnessFile = false

			// Check if this is a harness path
			for _, pattern := range harnessPatterns {
				if strings.Contains(line, pattern) {
					inHarnessFile = true
					break
				}
			}
		} else {
			// Continuation of current hunk
			currentHunk = append(currentHunk, line)
		}
	}

	// Don't forget the last hunk
	if len(currentHunk) > 0 && !inHarnessFile {
		result = append(result, strings.Join(currentHunk, "\n"))
	}

	if len(result) == 0 {
		return ""
	}

	return strings.Join(result, "\n")
}
