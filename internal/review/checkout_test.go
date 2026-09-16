package review

import (
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
		{
			name:  "basic case",
			owner: "owner",
			repo:  "name",
			pr:    123,
			want:  ".worktrees/review-owner-name-123",
		},
		{
			name:  "hyphenated org",
			owner: "my-org",
			repo:  "repo",
			pr:    456,
			want:  ".worktrees/review-my-org-repo-456",
		},
		{
			name:  "underscored repo",
			owner: "owner",
			repo:  "my_repo",
			pr:    789,
			want:  ".worktrees/review-owner-my_repo-789",
		},
		{
			name:  "large PR number",
			owner: "owner",
			repo:  "repo",
			pr:    99999,
			want:  ".worktrees/review-owner-repo-99999",
		},
		{
			name:  "hyphenated org and repo",
			owner: "my-org",
			repo:  "my-repo",
			pr:    1,
			want:  ".worktrees/review-my-org-my-repo-1",
		},
		{
			name:  "org and repo with numbers",
			owner: "org123",
			repo:  "repo456",
			pr:    42,
			want:  ".worktrees/review-org123-repo456-42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewDir(tt.owner, tt.repo, tt.pr)
			if got != tt.want {
				t.Errorf("reviewDir(%q, %q, %d) = %q, want %q",
					tt.owner, tt.repo, tt.pr, got, tt.want)
			}
		})
	}
}

// TestCheckoutCommands tests the pure argv construction function for both branches
func TestCheckoutCommands(t *testing.T) {
	tests := []struct {
		name      string
		repoURL   string
		pr        int
		dir       string
		dirExists bool
		want      [][]string
	}{
		{
			name:      "fresh clone",
			repoURL:   "https://github.com/owner/repo.git",
			pr:        123,
			dir:       ".worktrees/review-owner-repo-123",
			dirExists: false,
			want: [][]string{
				{"git", "clone", "https://github.com/owner/repo.git", ".worktrees/review-owner-repo-123"},
				{"gh", "pr", "checkout", "123"},
			},
		},
		{
			name:      "refresh existing",
			repoURL:   "https://github.com/owner/repo.git",
			pr:        456,
			dir:       ".worktrees/review-owner-repo-456",
			dirExists: true,
			want: [][]string{
				{"git", "fetch"},
				{"gh", "pr", "checkout", "456"},
			},
		},
		{
			name:      "fresh clone with hyphenated org/repo",
			repoURL:   "https://github.com/my-org/my-repo.git",
			pr:        789,
			dir:       ".worktrees/review-my-org-my-repo-789",
			dirExists: false,
			want: [][]string{
				{"git", "clone", "https://github.com/my-org/my-repo.git", ".worktrees/review-my-org-my-repo-789"},
				{"gh", "pr", "checkout", "789"},
			},
		},
		{
			name:      "refresh with large PR number",
			repoURL:   "https://github.com/owner/repo.git",
			pr:        99999,
			dir:       ".worktrees/review-owner-repo-99999",
			dirExists: true,
			want: [][]string{
				{"git", "fetch"},
				{"gh", "pr", "checkout", "99999"},
			},
		},
		{
			name:      "fresh clone with underscored repo",
			repoURL:   "https://github.com/owner/my_repo.git",
			pr:        1,
			dir:       ".worktrees/review-owner-my_repo-1",
			dirExists: false,
			want: [][]string{
				{"git", "clone", "https://github.com/owner/my_repo.git", ".worktrees/review-owner-my_repo-1"},
				{"gh", "pr", "checkout", "1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkoutCommands(tt.repoURL, tt.pr, tt.dir, tt.dirExists)

			// Check length
			if len(got) != len(tt.want) {
				t.Fatalf("checkoutCommands() returned %d commands, want %d\nGot: %v\nWant: %v",
					len(got), len(tt.want), got, tt.want)
			}

			// Check each command
			for i := range got {
				if !reflect.DeepEqual(got[i], tt.want[i]) {
					t.Errorf("checkoutCommands() command %d = %v, want %v",
						i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCheckoutWorkflow demonstrates the full workflow without executing commands
// This serves as integration documentation for orchestrator use
func TestCheckoutWorkflow(t *testing.T) {
	t.Run("fresh clone workflow", func(t *testing.T) {
		// Step 1: Simulate ResolvePRURL from #60
		owner := "owner"
		repo := "repo"
		prNum := 123

		// Step 2: Get directory path
		dir := reviewDir(owner, repo, prNum)
		expectedDir := ".worktrees/review-owner-repo-123"
		if dir != expectedDir {
			t.Errorf("reviewDir() = %q, want %q", dir, expectedDir)
		}

		// Step 3: Get fresh clone commands
		repoURL := "https://github.com/owner/repo.git"
		commands := checkoutCommands(repoURL, prNum, dir, false)

		// Verify fresh clone commands
		wantCommands := [][]string{
			{"git", "clone", "https://github.com/owner/repo.git", ".worktrees/review-owner-repo-123"},
			{"gh", "pr", "checkout", "123"},
		}

		if !reflect.DeepEqual(commands, wantCommands) {
			t.Errorf("checkoutCommands() for fresh clone = %v, want %v", commands, wantCommands)
		}
	})

	t.Run("refresh workflow", func(t *testing.T) {
		// Step 1: Simulate ResolvePRURL from #60
		owner := "owner"
		repo := "repo"
		prNum := 456

		// Step 2: Get directory path
		dir := reviewDir(owner, repo, prNum)
		expectedDir := ".worktrees/review-owner-repo-456"
		if dir != expectedDir {
			t.Errorf("reviewDir() = %q, want %q", dir, expectedDir)
		}

		// Step 3: Get refresh commands (directory exists)
		repoURL := "https://github.com/owner/repo.git"
		commands := checkoutCommands(repoURL, prNum, dir, true)

		// Verify refresh commands
		wantCommands := [][]string{
			{"git", "fetch"},
			{"gh", "pr", "checkout", "456"},
		}

		if !reflect.DeepEqual(commands, wantCommands) {
			t.Errorf("checkoutCommands() for refresh = %v, want %v", commands, wantCommands)
		}
	})

	t.Run("hyphenated org and repo", func(t *testing.T) {
		// Verify path handling with special characters
		owner := "my-org"
		repo := "my-repo"
		prNum := 789

		dir := reviewDir(owner, repo, prNum)
		expectedDir := ".worktrees/review-my-org-my-repo-789"
		if dir != expectedDir {
			t.Errorf("reviewDir() = %q, want %q", dir, expectedDir)
		}

		repoURL := "https://github.com/my-org/my-repo.git"
		commands := checkoutCommands(repoURL, prNum, dir, false)

		// Verify the directory appears correctly in the clone command
		if commands[0][3] != expectedDir {
			t.Errorf("clone command dir argument = %q, want %q", commands[0][3], expectedDir)
		}
	})
}
