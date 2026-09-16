package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RequiredReviewAssets returns the canonical list of agents and skills
// the PR-review workflow depends on. Each entry is prefixed with its type:
// - "agent:" for agent configs (.kiro/agents/<name>.json)
// - "skill:" for skill directories (.kiro/skills/<name>/SKILL.md)
//
// These are the agents/skills the pr_review.py orchestrator uses (reviewer
// lenses, consolidation, posting). howmux does NOT list its own issue
// orchestrator (krew-lead) here: per the #73 decision it runs reviews by
// invoking pr_review.py, not by spawning krew-lead, so krew-lead is
// intentionally out of scope for the review asset check.
func RequiredReviewAssets() []string {
	return []string{
		// Language-specific reviewer agents
		"agent:python-reviewer",
		"agent:go-reviewer",
		"agent:node-reviewer",
		"agent:java-reviewer",
		"agent:astro-reviewer",

		// Valkey-specific reviewer agents
		"agent:valkey-python-reviewer",
		"agent:valkey-go-reviewer",

		// Review lens agents
		"agent:review-security-agent",
		"agent:review-performance-agent",
		"agent:review-testing-agent",

		// Consolidation and posting
		"agent:review-consolidator",
		"agent:review-poster",

		// Review skills
		"skill:review-protocol",
		"skill:review-security",
		"skill:review-performance",
		"skill:review-testing",
		"skill:review-architecture",
		"skill:python-reviewer",
		"skill:go-reviewer",
		"skill:node-reviewer",
		"skill:java-reviewer",
		"skill:astro-reviewer",
		"skill:valkey-python-reviewer",
		"skill:valkey-go-reviewer",
		"skill:infra-reviewer",
	}
}

// CheckReviewAssets validates that all required review assets exist under
// the given kiro directory. Returns nil if all assets are present, or a
// descriptive error listing exactly what is missing and where to symlink it.
//
// Root — why ~/.kiro (see #73): the review is run by the existing pr_review.py
// orchestrator, which resolves agents/skills from ~/.kiro. The review is
// diff-based, not run from the PR checkout dir, so agent resolution never
// happens against the third-party checkout. That makes ~/.kiro the correct
// (and only) root to validate here. kiroDir is parameterized so tests can use
// a temp directory instead of the real ~/.kiro.
func CheckReviewAssets(kiroDir string) error {
	required := RequiredReviewAssets()
	var missing []string

	for _, asset := range required {
		parts := strings.SplitN(asset, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("internal error: malformed asset name %q (expected type:name)", asset)
		}

		assetType := parts[0]
		assetName := parts[1]
		var assetPath string

		switch assetType {
		case "agent":
			// Agents are JSON config files: .kiro/agents/<name>.json
			assetPath = filepath.Join(kiroDir, "agents", assetName+".json")
		case "skill":
			// Skills load from .kiro/skills/<name>/SKILL.md. Check the file,
			// not just the directory: a present-but-empty (or half-generated)
			// skill dir with no SKILL.md would otherwise pass while loading
			// nothing at runtime.
			assetPath = filepath.Join(kiroDir, "skills", assetName, "SKILL.md")
		default:
			return fmt.Errorf("internal error: unknown asset type %q in %q", assetType, asset)
		}

		if _, err := os.Stat(assetPath); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, fmt.Sprintf("  - %s (%s)", asset, assetPath))
			} else {
				return fmt.Errorf("failed to check %s: %w", asset, err)
			}
		}
	}

	if len(missing) > 0 {
		var sb strings.Builder
		sb.WriteString("PR-review workflow requires kiro-level assets that are not present.\n")
		sb.WriteString(fmt.Sprintf("Missing %d of %d required assets:\n\n", len(missing), len(required)))
		sb.WriteString(strings.Join(missing, "\n"))
		sb.WriteString("\n\n")
		sb.WriteString("To fix, symlink the missing resources into ~/.kiro:\n")
		sb.WriteString("  cd ~/.kiro\n")
		sb.WriteString("  ln -s /path/to/ai-resources/agents/<name>.json agents/\n")
		sb.WriteString("  ln -s /path/to/ai-resources/skills/<name> skills/\n")
		return fmt.Errorf("%s", sb.String())
	}

	return nil
}
