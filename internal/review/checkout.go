package review

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// reviewDir returns the directory path for a PR checkout under .worktrees/
// Example: reviewDir("owner", "name", 123) → ".worktrees/review-owner-name-123"
// This is a pure function (deterministic, no side effects)
func reviewDir(owner, repo string, pr int) string {
	return fmt.Sprintf(".worktrees/review-%s-%s-%d", owner, repo, pr)
}

// checkoutCommands returns the ordered command sequences to checkout or refresh a PR
// This is a pure function that constructs argv sequences without executing them
//
// For fresh clone (dirExists=false):
//   - git clone <repoURL> <dir>
//   - gh pr checkout <pr> (caller must run in dir)
//
// For refresh (dirExists=true):
//   - git fetch (caller must run in dir)
//   - gh pr checkout <pr> (caller must run in dir)
func checkoutCommands(repoURL string, pr int, dir string, dirExists bool) [][]string {
	prStr := strconv.Itoa(pr)

	if !dirExists {
		// Fresh clone
		return [][]string{
			{"git", "clone", repoURL, dir},
			{"gh", "pr", "checkout", prStr},
		}
	}

	// Refresh existing clone
	return [][]string{
		{"git", "fetch"},
		{"gh", "pr", "checkout", prStr},
	}
}

// EnsureCheckout ensures the PR code is checked out on disk at the given directory
// It clones the repo if the directory doesn't exist, or refreshes it if it does
// This is the thin wiring layer that executes the commands returned by checkoutCommands
func EnsureCheckout(repoURL string, pr int, dir string) error {
	// Check if directory exists
	_, err := os.Stat(dir)
	dirExists := err == nil

	// Get the command sequence
	commands := checkoutCommands(repoURL, pr, dir, dirExists)

	// Execute commands
	for i, argv := range commands {
		if len(argv) == 0 {
			continue
		}

		cmd := exec.Command(argv[0], argv[1:]...)

		// Set working directory based on command type
		if dirExists || i > 0 {
			// For refresh: all commands run in dir
			// For fresh clone: second command (gh pr checkout) runs in dir
			if argv[0] != "git" || argv[1] != "clone" {
				cmd.Dir = dir
			}
		}

		// Execute command
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command %v failed (repo=%s, pr=%d, dir=%s): %w\nOutput: %s",
				argv, repoURL, pr, dir, err, string(output))
		}
	}

	return nil
}
