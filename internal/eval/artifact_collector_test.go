package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCollectBuilderDiff(t *testing.T) {
	// Create temporary workspace with git repo
	tempDir, err := os.MkdirTemp("", "builder_diff_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize git repo
	setupGitRepo(t, tempDir)

	tests := []struct {
		name     string
		setup    func(string) error
		expected func(string) bool // validation function for flexible matching
	}{
		{
			name: "no changes scenario",
			setup: func(dir string) error {
				// Commit everything, no pending changes
				return commitAll(dir, "initial commit")
			},
			expected: func(result string) bool {
				return result == "" // empty diff
			},
		},
		{
			name: "source file changes",
			setup: func(dir string) error {
				// Commit base state
				if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
					return err
				}
				if err := commitAll(dir, "add main.go"); err != nil {
					return err
				}
				// Make changes
				return os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
			},
			expected: func(result string) bool {
				return strings.Contains(result, "=== Git Diff (Builder Changes) ===") &&
					strings.Contains(result, "main.go") &&
					strings.Contains(result, "+func main()")
			},
		},
		{
			name: "mixed changes - source and harness files",
			setup: func(dir string) error {
				// Commit base state
				if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package code\n"), 0644); err != nil {
					return err
				}
				if err := commitAll(dir, "add code.go"); err != nil {
					return err
				}
				// Change source file
				if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package code\n\n// Updated\n"), 0644); err != nil {
					return err
				}
				// Change harness file
				harnessDir := filepath.Join(dir, ".howmux", "specs")
				if err := os.MkdirAll(harnessDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(harnessDir, "spec.md"), []byte("# Spec\n"), 0644)
			},
			expected: func(result string) bool {
				return strings.Contains(result, "code.go") &&
					strings.Contains(result, "// Updated") &&
					!strings.Contains(result, ".howmux") &&
					!strings.Contains(result, "spec.md")
			},
		},
		{
			name: "harness-only changes",
			setup: func(dir string) error {
				// Commit base state
				if err := commitAll(dir, "base"); err != nil {
					return err
				}
				// Only change harness files
				harnessDir := filepath.Join(dir, ".howmux", "artifacts")
				if err := os.MkdirAll(harnessDir, 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(harnessDir, "builder-1.md"), []byte("# Artifact\n"), 0644); err != nil {
					return err
				}
				kiroDir := filepath.Join(dir, ".kiro", "agents")
				if err := os.MkdirAll(kiroDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(kiroDir, "agent.json"), []byte("{}"), 0644)
			},
			expected: func(result string) bool {
				return result == "" // all harness paths filtered out
			},
		},
		{
			name: "git command failure - not a repo",
			setup: func(dir string) error {
				// Remove .git to simulate non-repo
				return os.RemoveAll(filepath.Join(dir, ".git"))
			},
			expected: func(result string) bool {
				return result == "" // graceful failure
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each test gets a fresh temp dir
			testDir, err := os.MkdirTemp("", "builder_test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(testDir)

			// Initialize git repo for this test
			setupGitRepo(t, testDir)

			// Setup test scenario
			if err := tt.setup(testDir); err != nil {
				t.Fatal(err)
			}

			// Test collection
			result := collectBuilderDiff(testDir)
			if !tt.expected(result) {
				t.Errorf("collectBuilderDiff() validation failed, got:\n%s", result)
			}
		})
	}
}

func TestFilterHarnessPaths(t *testing.T) {
	tests := []struct {
		name     string
		diff     string
		expected string
	}{
		{
			name:     "empty diff",
			diff:     "",
			expected: "",
		},
		{
			name: "single source file",
			diff: `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,1 +1,3 @@
 package main
+
+func main() {}`,
			expected: `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,1 +1,3 @@
 package main
+
+func main() {}`,
		},
		{
			name: "single harness file",
			diff: `diff --git a/.howmux/specs/spec.md b/.howmux/specs/spec.md
new file mode 100644
index 0000000..abc123
--- /dev/null
+++ b/.howmux/specs/spec.md
@@ -0,0 +1,3 @@
+# Spec
+
+Content`,
			expected: "",
		},
		{
			name: "mixed source and harness",
			diff: `diff --git a/internal/eval/collector.go b/internal/eval/collector.go
index abc123..def456 100644
--- a/internal/eval/collector.go
+++ b/internal/eval/collector.go
@@ -10,3 +10,5 @@ func collect() {
 	// code
+	// more code
 }
diff --git a/.howmux/artifacts/builder-1.md b/.howmux/artifacts/builder-1.md
new file mode 100644
index 0000000..xyz789
--- /dev/null
+++ b/.howmux/artifacts/builder-1.md
@@ -0,0 +1,1 @@
+# Artifact
diff --git a/cmd/howmux/main.go b/cmd/howmux/main.go
index 111222..333444 100644
--- a/cmd/howmux/main.go
+++ b/cmd/howmux/main.go
@@ -5,2 +5,3 @@ func main() {
 	fmt.Println("hello")
+	fmt.Println("world")
 }`,
			expected: `diff --git a/internal/eval/collector.go b/internal/eval/collector.go
index abc123..def456 100644
--- a/internal/eval/collector.go
+++ b/internal/eval/collector.go
@@ -10,3 +10,5 @@ func collect() {
 	// code
+	// more code
 }
diff --git a/cmd/howmux/main.go b/cmd/howmux/main.go
index 111222..333444 100644
--- a/cmd/howmux/main.go
+++ b/cmd/howmux/main.go
@@ -5,2 +5,3 @@ func main() {
 	fmt.Println("hello")
+	fmt.Println("world")
 }`,
		},
		{
			name: "multiple harness patterns",
			diff: `diff --git a/.howmux/config.yaml b/.howmux/config.yaml
index abc..def 100644
--- a/.howmux/config.yaml
+++ b/.howmux/config.yaml
@@ -1 +1 @@
-old
+new
diff --git a/.kiro/agents/builder.json b/.kiro/agents/builder.json
index 111..222 100644
--- a/.kiro/agents/builder.json
+++ b/.kiro/agents/builder.json
@@ -1 +1 @@
-{}
+{"name":"builder"}
diff --git a/.git/config b/.git/config
index aaa..bbb 100644
--- a/.git/config
+++ b/.git/config
@@ -1 +1 @@
-[core]
+[core]
+	filemode = true`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterHarnessPaths(tt.diff)
			if result != tt.expected {
				t.Errorf("filterHarnessPaths() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// setupGitRepo initializes a git repo in the given directory
func setupGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test User"},
		{"git", "config", "user.email", "test@example.com"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git setup failed: %v (cmd: %v)", err, args)
		}
	}
}

// commitAll stages and commits all changes in the directory
func commitAll(dir, message string) error {
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	if err := add.Run(); err != nil {
		return err
	}

	commit := exec.Command("git", "commit", "-m", message, "--allow-empty")
	commit.Dir = dir
	return commit.Run()
}
