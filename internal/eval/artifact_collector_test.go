package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectArtifacts(t *testing.T) {
	// Create temporary workspace
	tempDir, err := os.MkdirTemp("", "artifact_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name     string
		agent    string
		setup    func(string) error
		expected string
	}{
		{
			name:  "architect with spec file",
			agent: "architect",
			setup: func(dir string) error {
				specDir := filepath.Join(dir, ".howmux", "specs")
				if err := os.MkdirAll(specDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(specDir, "issue-42-test.md"), []byte("# Test Spec\n\nContent here"), 0644)
			},
			expected: "\n--- PRODUCED ARTIFACT ---\n=== .howmux/specs/issue-42-test.md ===\n# Test Spec\n\nContent here",
		},
		{
			name:  "documenter with feature doc",
			agent: "documenter",
			setup: func(dir string) error {
				docDir := filepath.Join(dir, "app_docs")
				if err := os.MkdirAll(docDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(docDir, "feature-auth.md"), []byte("# Auth Feature\n\nDocumentation"), 0644)
			},
			expected: "\n--- PRODUCED ARTIFACT ---\n=== app_docs/feature-auth.md ===\n# Auth Feature\n\nDocumentation",
		},
		{
			name:     "unknown agent",
			agent:    "unknown",
			setup:    func(dir string) error { return nil },
			expected: "",
		},
		{
			name:     "architect with no specs",
			agent:    "architect",
			setup:    func(dir string) error { return nil },
			expected: "",
		},
		{
			name:  "architect with non-issue spec",
			agent: "architect",
			setup: func(dir string) error {
				specDir := filepath.Join(dir, ".howmux", "specs")
				if err := os.MkdirAll(specDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(specDir, "other-spec.md"), []byte("# Other"), 0644)
			},
			expected: "",
		},
		{
			name:  "architect with multiple specs",
			agent: "architect",
			setup: func(dir string) error {
				specDir := filepath.Join(dir, ".howmux", "specs")
				if err := os.MkdirAll(specDir, 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(specDir, "issue-1-first.md"), []byte("# First"), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(specDir, "issue-2-second.md"), []byte("# Second"), 0644)
			},
			expected: "\n--- PRODUCED ARTIFACT ---\n=== .howmux/specs/issue-1-first.md ===\n# First\n---\n=== .howmux/specs/issue-2-second.md ===\n# Second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test files
			if err := tt.setup(tempDir); err != nil {
				t.Fatal(err)
			}

			// Test collection
			result := collectArtifacts(tt.agent, tempDir)
			if result != tt.expected {
				t.Errorf("collectArtifacts() = %q, want %q", result, tt.expected)
			}

			// Cleanup for next test
			os.RemoveAll(filepath.Join(tempDir, ".howmux"))
			os.RemoveAll(filepath.Join(tempDir, "app_docs"))
		})
	}
}
