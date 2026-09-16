package review

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestReviewDir tests the pure path construction function
func TestReviewDir(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		repo  string
		pr    int
		want  string
	}{
		{"basic case", "owner", "name", 123, ".worktrees/review-owner-name-123"},
		{"hyphenated org", "my-org", "repo", 456, ".worktrees/review-my-org-repo-456"},
		{"underscored repo", "owner", "my_repo", 789, ".worktrees/review-owner-my_repo-789"},
		{"large PR number", "owner", "repo", 99999, ".worktrees/review-owner-repo-99999"},
		{"hyphenated org and repo", "my-org", "my-repo", 1, ".worktrees/review-my-org-my-repo-1"},
		{"org and repo with numbers", "org123", "repo456", 42, ".worktrees/review-org123-repo456-42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewDir(tt.owner, tt.repo, tt.pr)
			if got != tt.want {
				t.Errorf("reviewDir(%q, %q, %d) = %q, want %q", tt.owner, tt.repo, tt.pr, got, tt.want)
			}
		})
	}
}

// TestCheckoutCommands tests the pure command construction for both branches.
// Fresh clone runs `git clone` in cwd then `gh pr checkout --repo` in dir.
// Refresh runs `git fetch` then `gh pr checkout --repo --force` in dir (the
// --force handles a force-pushed PR branch).
func TestCheckoutCommands(t *testing.T) {
	tests := []struct {
		name      string
		owner     string
		repo      string
		repoURL   string
		pr        int
		dir       string
		dirExists bool
		want      []command
	}{
		{
			name:      "fresh clone",
			owner:     "owner",
			repo:      "repo",
			repoURL:   "https://github.com/owner/repo.git",
			pr:        123,
			dir:       ".worktrees/review-owner-repo-123",
			dirExists: false,
			want: []command{
				{argv: []string{"git", "clone", "https://github.com/owner/repo.git", ".worktrees/review-owner-repo-123"}, dir: ""},
				{argv: []string{"gh", "pr", "checkout", "123", "--repo", "owner/repo"}, dir: ".worktrees/review-owner-repo-123"},
			},
		},
		{
			name:      "refresh existing uses --force",
			owner:     "owner",
			repo:      "repo",
			repoURL:   "https://github.com/owner/repo.git",
			pr:        456,
			dir:       ".worktrees/review-owner-repo-456",
			dirExists: true,
			want: []command{
				{argv: []string{"git", "fetch"}, dir: ".worktrees/review-owner-repo-456"},
				{argv: []string{"gh", "pr", "checkout", "456", "--repo", "owner/repo", "--force"}, dir: ".worktrees/review-owner-repo-456"},
			},
		},
		{
			name:      "fresh clone with hyphenated org/repo",
			owner:     "my-org",
			repo:      "my-repo",
			repoURL:   "https://github.com/my-org/my-repo.git",
			pr:        789,
			dir:       ".worktrees/review-my-org-my-repo-789",
			dirExists: false,
			want: []command{
				{argv: []string{"git", "clone", "https://github.com/my-org/my-repo.git", ".worktrees/review-my-org-my-repo-789"}, dir: ""},
				{argv: []string{"gh", "pr", "checkout", "789", "--repo", "my-org/my-repo"}, dir: ".worktrees/review-my-org-my-repo-789"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkoutCommands(tt.owner, tt.repo, tt.repoURL, tt.pr, tt.dir, tt.dirExists)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkoutCommands() = %v, want %v", got, tt.want)
			}
		})
	}
}

// withFakeRunner swaps runCommand for a recorder for the duration of the test.
func withFakeRunner(t *testing.T, fail map[int]bool) *[]command {
	t.Helper()
	orig := runCommand
	var recorded []command
	call := 0
	runCommand = func(c command) error {
		recorded = append(recorded, c)
		i := call
		call++
		if fail[i] {
			return fmt.Errorf("simulated failure on command %d", i)
		}
		return nil
	}
	t.Cleanup(func() { runCommand = orig })
	return &recorded
}

// TestEnsureCheckoutFreshClone verifies the fresh-clone path issues clone (cwd)
// then gh pr checkout --repo (in dir), when the dir does not exist.
func TestEnsureCheckoutFreshClone(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "review-owner-repo-1")

	recorded := withFakeRunner(t, nil)

	if err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 1, dir); err != nil {
		t.Fatalf("EnsureCheckout() error = %v", err)
	}

	want := []command{
		{argv: []string{"git", "clone", "https://github.com/owner/repo.git", dir}, dir: ""},
		{argv: []string{"gh", "pr", "checkout", "1", "--repo", "owner/repo"}, dir: dir},
	}
	if !reflect.DeepEqual(*recorded, want) {
		t.Errorf("commands = %v, want %v", *recorded, want)
	}
}

// TestEnsureCheckoutRefresh verifies that an existing healthy clone (.git
// present) takes the refresh path with --force.
func TestEnsureCheckoutRefresh(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "review-owner-repo-2")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	recorded := withFakeRunner(t, nil)

	if err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 2, dir); err != nil {
		t.Fatalf("EnsureCheckout() error = %v", err)
	}

	want := []command{
		{argv: []string{"git", "fetch"}, dir: dir},
		{argv: []string{"gh", "pr", "checkout", "2", "--repo", "owner/repo", "--force"}, dir: dir},
	}
	if !reflect.DeepEqual(*recorded, want) {
		t.Errorf("commands = %v, want %v", *recorded, want)
	}
}

// TestEnsureCheckoutReclonesHalfInitializedDir verifies that a directory which
// exists but is not a git repo (a partial clone from a prior failure) is
// removed and re-cloned instead of being treated as refreshable.
func TestEnsureCheckoutReclonesHalfInitializedDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "review-owner-repo-3")
	// Dir exists but has no .git — half-initialized.
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	recorded := withFakeRunner(t, nil)

	if err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 3, dir); err != nil {
		t.Fatalf("EnsureCheckout() error = %v", err)
	}

	// Should have taken the fresh-clone path, not refresh.
	if len(*recorded) == 0 || (*recorded)[0].argv[1] != "clone" {
		t.Fatalf("expected re-clone, got %v", *recorded)
	}
}

// TestEnsureCheckoutCleansUpOnFreshFailure verifies that when a fresh clone
// fails, the partial directory is removed so the slot is not left wedged.
func TestEnsureCheckoutCleansUpOnFreshFailure(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "review-owner-repo-4")

	// Fail the second command (gh pr checkout). Simulate the clone having
	// created the dir by creating it inside the fake runner.
	orig := runCommand
	call := 0
	runCommand = func(c command) error {
		if call == 0 {
			// Simulate git clone creating the directory.
			os.MkdirAll(dir, 0755)
		}
		call++
		if call == 2 {
			return fmt.Errorf("simulated checkout failure")
		}
		return nil
	}
	t.Cleanup(func() { runCommand = orig })

	err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 4, dir)
	if err == nil {
		t.Fatal("expected error from failed checkout")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("expected partial dir %q to be cleaned up, but it still exists", dir)
	}
}

// TestEnsureCheckoutRefreshFailureKeepsDir verifies that a refresh failure
// leaves the existing (previously healthy) clone in place.
func TestEnsureCheckoutRefreshFailureKeepsDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "review-owner-repo-5")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Fail the first refresh command (git fetch).
	withFakeRunner(t, map[int]bool{0: true})

	err := EnsureCheckout("owner", "repo", "https://github.com/owner/repo.git", 5, dir)
	if err == nil {
		t.Fatal("expected error from failed fetch")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("expected existing clone dir %q to be preserved on refresh failure: %v", dir, statErr)
	}
}
