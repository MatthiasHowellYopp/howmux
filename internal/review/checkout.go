package review

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// reviewDir returns the directory path for a PR checkout under .worktrees/
// Example: reviewDir("owner", "name", 123) → ".worktrees/review-owner-name-123"
// This is a pure function (deterministic, no side effects).
func reviewDir(owner, repo string, pr int) string {
	return fmt.Sprintf(".worktrees/review-%s-%s-%d", owner, repo, pr)
}

// command is a single step in a checkout sequence: an argv plus the working
// directory it must run in. Carrying the cwd on the command itself removes the
// need for the caller to guess which step runs where (which previously relied
// on a fragile argv index).
type command struct {
	argv []string
	dir  string // working directory; "" means the process's current directory
}

// checkoutCommands returns the ordered command sequence to check out or refresh
// a PR into dir. Pure: it constructs commands without executing them.
//
// Fresh clone (dirExists=false):
//   - git clone <repoURL> <dir>            (run in cwd)
//   - gh pr checkout <pr> --repo <o/r>     (run in dir)
//
// Refresh (dirExists=true) — a PR branch may have been force-pushed, so the
// refresh must reset the local branch to the PR's latest state rather than
// fail on a diverged branch:
//   - git fetch                                    (run in dir)
//   - gh pr checkout <pr> --repo <o/r> --force     (run in dir)
//
// --repo is passed explicitly so the checkout targets the intended repository
// regardless of the clone's origin remote.
func checkoutCommands(owner, repo, repoURL string, pr int, dir string, dirExists bool) []command {
	prStr := strconv.Itoa(pr)
	ownerRepo := owner + "/" + repo

	if !dirExists {
		return []command{
			{argv: []string{"git", "clone", repoURL, dir}, dir: ""},
			{argv: []string{"gh", "pr", "checkout", prStr, "--repo", ownerRepo}, dir: dir},
		}
	}

	return []command{
		{argv: []string{"git", "fetch"}, dir: dir},
		{argv: []string{"gh", "pr", "checkout", prStr, "--repo", ownerRepo, "--force"}, dir: dir},
	}
}

// runCommand executes a single command in its working directory. It is a
// package var so tests can substitute a fake runner and assert the command
// sequence and cwd choices without invoking real git/gh.
var runCommand = func(c command) error {
	cmd := exec.Command(c.argv[0], c.argv[1:]...)
	cmd.Dir = c.dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command %v (dir=%q) failed: %w\nOutput: %s", c.argv, c.dir, err, string(output))
	}
	return nil
}

// isGitRepo reports whether dir looks like a real git clone (has a .git entry),
// as opposed to merely existing. A directory that exists but is not a git repo
// is treated as a half-initialized clone and re-cloned.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// EnsureCheckout ensures the PR code is checked out on disk at reviewDir.
// It clones the repo if the directory is not already a healthy clone, or
// refreshes it (fetch + force checkout) if it is.
//
// Failure handling: if a *fresh* clone (clone or the following checkout) fails,
// the directory is removed so the deterministic slot is not left wedged in a
// half-initialized state for the next call. Refresh failures leave the existing
// clone in place (it was healthy before) and surface the error.
func EnsureCheckout(owner, repo, repoURL string, pr int, dir string) (err error) {
	// A directory that exists but is not a git repo is a half-initialized
	// clone from a prior failure: remove it and treat as a fresh clone.
	if _, statErr := os.Stat(dir); statErr == nil && !isGitRepo(dir) {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return fmt.Errorf("failed to clear stale checkout dir %q: %w", dir, rmErr)
		}
	}

	dirExists := isGitRepo(dir)
	commands := checkoutCommands(owner, repo, repoURL, pr, dir, dirExists)

	// On the fresh-clone path, clean up a partial checkout on failure so the
	// slot is reusable.
	if !dirExists {
		defer func() {
			if err != nil {
				os.RemoveAll(dir)
			}
		}()
	}

	for _, c := range commands {
		if len(c.argv) == 0 {
			continue
		}
		if runErr := runCommand(c); runErr != nil {
			return fmt.Errorf("checkout failed (repo=%s/%s, pr=%d, dir=%s): %w", owner, repo, pr, dir, runErr)
		}
	}

	return nil
}
