package review

import (
	"fmt"
	"strings"
)

// RequiredFinalizeAssets returns the canonical list of external scripts the
// PR-review finalize gate (issue #87's finalize-reviews.sh workflow) depends
// on. Every entry is a "tool:" entry — both scripts are resolved on $PATH
// (finalize-reviews.sh via ~/.local/bin per custom-scripts.md;
// pr_review_finalize.py the same way, since finalize-reviews.sh shells out to
// it directly) — there is no ~/.kiro-rooted agent/skill entry in this list,
// unlike RequiredReviewAssets.
func RequiredFinalizeAssets() []string {
	return []string{
		"tool:finalize-reviews.sh",
		"tool:pr_review_finalize.py",
	}
}

// CheckFinalizeAssets validates that every script RequiredFinalizeAssets
// lists resolves on $PATH via lookPathFunc (the same package-level seam
// CheckReviewAssets uses). Returns nil if both resolve, or a descriptive
// error listing exactly what is missing and how to fix it — same message
// shape as CheckReviewAssets ("Missing N of M ...", one bullet per missing
// entry, then a "how to fix" block), so a user who has already seen the
// review preflight error recognizes the pattern immediately.
//
// Two-seam note: finalize-reviews.sh is resolved in two places. This is the
// front-door superset — it preflights the script AND the transitive
// pr_review_finalize.py before the finalize preview window opens, via
// lookPathFunc ($PATH). The run-time last line is the tui package's
// finalizeScriptPathFunc (finalize.go), which re-resolves finalize-reviews.sh
// on $PATH at spawn time as a TOCTOU backstop. Both consult $PATH so they
// cannot disagree; if you change how one resolves the script, update the
// other to match.
func CheckFinalizeAssets() error {
	required := RequiredFinalizeAssets()
	var missing []string

	for _, asset := range required {
		// Every entry here is "tool:<name>"; strip the prefix for lookPathFunc
		// and keep the prefixed form for the error text (matching
		// CheckReviewAssets's "tool:pr_review.py (not found on PATH)" style).
		name := asset[len("tool:"):]
		if _, err := lookPathFunc(name); err != nil {
			missing = append(missing, fmt.Sprintf("  - %s (not found on PATH)", asset))
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("PR-review finalize gate requires scripts that are not present on PATH.\n")
	sb.WriteString(fmt.Sprintf("Missing %d of %d required scripts:\n\n", len(missing), len(required)))
	sb.WriteString(strings.Join(missing, "\n"))
	sb.WriteString("\n\n")
	sb.WriteString("To fix, ensure ai-resources/scripts is symlinked into ~/.local/bin and on $PATH:\n")
	sb.WriteString("  ls -la ~/.local/bin/finalize-reviews.sh ~/.local/bin/pr_review_finalize.py\n")
	sb.WriteString("  # if missing, re-run ai-resources' scripts setup (see custom-scripts.md)\n")
	return fmt.Errorf("%s", sb.String())
}
