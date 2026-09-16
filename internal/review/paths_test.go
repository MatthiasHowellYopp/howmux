package review

import (
	"path/filepath"
	"testing"
)

func TestRecordDir(t *testing.T) {
	tests := []struct {
		name     string
		repo     string
		pr       int
		expected string
	}{
		{
			name:     "standard repo with slash",
			repo:     "owner/name",
			pr:       123,
			expected: "owner-name-123",
		},
		{
			name:     "repo without slash",
			repo:     "singlename",
			pr:       456,
			expected: "singlename-456",
		},
		{
			name:     "repo with multiple slashes (takes first two parts)",
			repo:     "owner/name/extra",
			pr:       789,
			expected: "owner-name-789",
		},
		{
			name:     "pr number zero (edge case, but function should still work)",
			repo:     "owner/repo",
			pr:       0,
			expected: "owner-repo-0",
		},
		{
			name:     "pr number negative (edge case, but function should still work)",
			repo:     "owner/repo",
			pr:       -1,
			expected: "owner-repo--1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecordDir(tt.repo, tt.pr)
			if got != tt.expected {
				t.Errorf("RecordDir(%q, %d) = %q, want %q", tt.repo, tt.pr, got, tt.expected)
			}
		})
	}
}

func TestRecordDirInternal(t *testing.T) {
	tests := []struct {
		name     string
		owner    string
		repo     string
		pr       int
		expected string
	}{
		{
			name:     "standard case",
			owner:    "facebook",
			repo:     "react",
			pr:       456,
			expected: "facebook-react-456",
		},
		{
			name:     "empty owner",
			owner:    "",
			repo:     "repo",
			pr:       1,
			expected: "-repo-1",
		},
		{
			name:     "empty repo",
			owner:    "owner",
			repo:     "",
			pr:       2,
			expected: "owner--2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recordDir(tt.owner, tt.repo, tt.pr)
			if got != tt.expected {
				t.Errorf("recordDir(%q, %q, %d) = %q, want %q", tt.owner, tt.repo, tt.pr, got, tt.expected)
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	baseDir := "/tmp/reviews"
	dirName := "owner-repo-123"

	t.Run("recordFilename", func(t *testing.T) {
		got := recordFilename(baseDir, dirName)
		expected := filepath.Join("/tmp/reviews", "owner-repo-123", "record.json")
		if got != expected {
			t.Errorf("recordFilename() = %q, want %q", got, expected)
		}
	})

	t.Run("reviewsDir", func(t *testing.T) {
		got := reviewsDir(baseDir, dirName)
		expected := filepath.Join("/tmp/reviews", "owner-repo-123", "reviews")
		if got != expected {
			t.Errorf("reviewsDir() = %q, want %q", got, expected)
		}
	})
}
