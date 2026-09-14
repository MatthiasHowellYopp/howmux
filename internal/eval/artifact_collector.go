package eval

import (
	"fmt"
	"os"
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
	default:
		// No artifacts expected for other agents
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
