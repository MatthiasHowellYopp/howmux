# Design Specification: PR-Review Workflow Asset Validation

**Issue:** #63  
**Closes:** #63  
**Status:** Ready for Implementation  
**Complexity:** Low

## Solution Approach

Implement a pre-flight asset validation system for the PR-review workflow that verifies all required kiro-level agents and skills exist before initiating a review. The system will fail loud with actionable error messages when assets are missing, eliminating silent failures and guiding users to symlink the correct resources.

The validation will be implemented as two pure functions in a new `internal/review/assets.go` file:

1. `RequiredReviewAssets() []string` — returns the canonical list of agent and skill names the PR-review workflow depends on
2. `CheckReviewAssets(kiroDir string) error` — validates that all required assets exist under the given kiro directory, returning a descriptive error listing missing assets if any are absent

This design follows the existing pattern established by `acp.ValidateAgentResolvable` (which validates a single agent config file) but extends it to cover the complete set of review-workflow dependencies and supports both agents and skills.

**Key architectural decisions:**

- **Pure functions with no side effects** — both functions are deterministic and testable without I/O dependencies
- **Parameterized kiro directory** — accepts `kiroDir` as a parameter rather than hardcoding `~/.kiro`, enabling testing against temporary directories
- **Fail-loud philosophy** — follows the existing pattern from `acp.ValidateAgentResolvable` of detecting problems before a session starts rather than discovering them mid-workflow
- **No concurrency concerns** — these are synchronous validation functions called before spawning any goroutines

## Relevant Files

### New Files

- `internal/review/assets.go` — Core validation logic with `RequiredReviewAssets()` and `CheckReviewAssets()`
- `internal/review/assets_test.go` — Comprehensive test suite covering all validation scenarios

### Modified Files

None initially — this is a standalone preflight utility. Integration with the review command will happen in a follow-on issue (the `review` command itself is not yet implemented).

## Team Orchestration

This is a self-contained feature with no dependencies on other components:

- **Backend implementation** — Both functions implemented together in `internal/review/assets.go`
- **Testing** — Complete test coverage added in `internal/review/assets_test.go`

No integration work required — the functions are designed for future use by the review command handler when it is implemented.

## Step-by-Step Task Breakdown

### Task 1: Implement RequiredReviewAssets() and CheckReviewAssets()

**File:** `internal/review/assets.go`

**Implementation:**

```go
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
// - "skill:" for skill directories (.kiro/skills/<name>/)
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
// kiroDir should typically be os.UserHomeDir()+"/.kiro", but is parameterized
// to enable testing against temporary directories without dependency on the
// real ~/.kiro.
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
			// Skills are directories: .kiro/skills/<name>/
			assetPath = filepath.Join(kiroDir, "skills", assetName)
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
```

**Acceptance Criteria:**

1. `RequiredReviewAssets()` returns a complete list of all agents and skills the PR-review workflow depends on
   - Each entry is prefixed with `agent:` or `skill:` to distinguish the asset type
   - List includes: language reviewers (Python, Go, Node, Java, Astro), Valkey reviewers, review lens agents (security, performance, testing), consolidator, poster, and all corresponding skills
   - List is ordered logically (language reviewers, Valkey reviewers, lens agents, consolidation, skills)
   - **Verification:** `go test ./internal/review -run TestRequiredReviewAssets` passes

2. `CheckReviewAssets(kiroDir string)` validates all required assets exist
   - Accepts kiroDir as a parameter (not hardcoded to `~/.kiro`)
   - For each `agent:X`, checks for `<kiroDir>/agents/X.json`
   - For each `skill:X`, checks for `<kiroDir>/skills/X/`
   - Returns `nil` when all assets are present
   - Returns descriptive error when assets are missing
   - **Verification:** `go test ./internal/review -run TestCheckReviewAssets` passes

3. Error message lists exactly what is missing and is actionable
   - States the count of missing assets (e.g., "Missing 3 of 25 required assets:")
   - Lists each missing asset with its expected path
   - Includes concrete symlink instructions referencing the typical source location
   - **Verification:** Test confirms error message format matches spec

4. No dependency on real `~/.kiro` in tests
   - All tests use `t.TempDir()` or equivalent temporary directories
   - Tests can run in parallel without interference
   - **Verification:** `go test ./internal/review -race` passes with no data races

### Task 2: Comprehensive Test Coverage

**File:** `internal/review/assets_test.go`

**Test Cases:**

```go
package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequiredReviewAssets verifies the canonical asset list
func TestRequiredReviewAssets(t *testing.T) {
	assets := RequiredReviewAssets()

	// Verify list is non-empty
	if len(assets) == 0 {
		t.Fatal("RequiredReviewAssets() returned empty list")
	}

	// Verify all entries are properly prefixed
	for _, asset := range assets {
		if !strings.HasPrefix(asset, "agent:") && !strings.HasPrefix(asset, "skill:") {
			t.Errorf("asset %q missing type prefix (expected 'agent:' or 'skill:')", asset)
		}
	}

	// Verify expected critical assets are present
	criticalAssets := []string{
		"agent:python-reviewer",
		"agent:go-reviewer",
		"agent:review-consolidator",
		"skill:review-protocol",
		"skill:python-reviewer",
	}
	for _, critical := range criticalAssets {
		found := false
		for _, asset := range assets {
			if asset == critical {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("critical asset %q not in RequiredReviewAssets()", critical)
		}
	}
}

// TestCheckReviewAssets_AllPresent verifies nil error when all assets exist
func TestCheckReviewAssets_AllPresent(t *testing.T) {
	kiroDir := t.TempDir()
	agentsDir := filepath.Join(kiroDir, "agents")
	skillsDir := filepath.Join(kiroDir, "skills")

	// Create directory structure
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// Create all required assets
	for _, asset := range RequiredReviewAssets() {
		parts := strings.SplitN(asset, ":", 2)
		assetType := parts[0]
		assetName := parts[1]

		if assetType == "agent" {
			// Create agent JSON config
			agentFile := filepath.Join(agentsDir, assetName+".json")
			if err := os.WriteFile(agentFile, []byte("{}"), 0644); err != nil {
				t.Fatalf("failed to create agent file %s: %v", agentFile, err)
			}
		} else if assetType == "skill" {
			// Create skill directory
			skillDir := filepath.Join(skillsDir, assetName)
			if err := os.Mkdir(skillDir, 0755); err != nil {
				t.Fatalf("failed to create skill dir %s: %v", skillDir, err)
			}
		}
	}

	// Verify CheckReviewAssets returns nil
	err := CheckReviewAssets(kiroDir)
	if err != nil {
		t.Errorf("CheckReviewAssets() with all assets present returned error: %v", err)
	}
}

// TestCheckReviewAssets_MissingAgent verifies error when agent is missing
func TestCheckReviewAssets_MissingAgent(t *testing.T) {
	kiroDir := t.TempDir()
	agentsDir := filepath.Join(kiroDir, "agents")
	skillsDir := filepath.Join(kiroDir, "skills")

	// Create directory structure
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// Create all required assets EXCEPT python-reviewer agent
	for _, asset := range RequiredReviewAssets() {
		if asset == "agent:python-reviewer" {
			continue // Skip this one to trigger error
		}

		parts := strings.SplitN(asset, ":", 2)
		assetType := parts[0]
		assetName := parts[1]

		if assetType == "agent" {
			agentFile := filepath.Join(agentsDir, assetName+".json")
			if err := os.WriteFile(agentFile, []byte("{}"), 0644); err != nil {
				t.Fatalf("failed to create agent file %s: %v", agentFile, err)
			}
		} else if assetType == "skill" {
			skillDir := filepath.Join(skillsDir, assetName)
			if err := os.Mkdir(skillDir, 0755); err != nil {
				t.Fatalf("failed to create skill dir %s: %v", skillDir, err)
			}
		}
	}

	// Verify CheckReviewAssets returns error
	err := CheckReviewAssets(kiroDir)
	if err == nil {
		t.Fatal("CheckReviewAssets() with missing agent returned nil, expected error")
	}

	// Verify error message names the missing asset
	errMsg := err.Error()
	if !strings.Contains(errMsg, "agent:python-reviewer") {
		t.Errorf("error message does not name missing agent:python-reviewer: %v", errMsg)
	}

	// Verify error message includes the expected path
	expectedPath := filepath.Join(kiroDir, "agents", "python-reviewer.json")
	if !strings.Contains(errMsg, expectedPath) {
		t.Errorf("error message does not include expected path %s: %v", expectedPath, errMsg)
	}

	// Verify error message includes symlink instructions
	if !strings.Contains(errMsg, "symlink") {
		t.Errorf("error message does not include symlink instructions: %v", errMsg)
	}
}

// TestCheckReviewAssets_MissingSkill verifies error when skill is missing
func TestCheckReviewAssets_MissingSkill(t *testing.T) {
	kiroDir := t.TempDir()
	agentsDir := filepath.Join(kiroDir, "agents")
	skillsDir := filepath.Join(kiroDir, "skills")

	// Create directory structure
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// Create all required assets EXCEPT review-protocol skill
	for _, asset := range RequiredReviewAssets() {
		if asset == "skill:review-protocol" {
			continue // Skip this one to trigger error
		}

		parts := strings.SplitN(asset, ":", 2)
		assetType := parts[0]
		assetName := parts[1]

		if assetType == "agent" {
			agentFile := filepath.Join(agentsDir, assetName+".json")
			if err := os.WriteFile(agentFile, []byte("{}"), 0644); err != nil {
				t.Fatalf("failed to create agent file %s: %v", agentFile, err)
			}
		} else if assetType == "skill" {
			skillDir := filepath.Join(skillsDir, assetName)
			if err := os.Mkdir(skillDir, 0755); err != nil {
				t.Fatalf("failed to create skill dir %s: %v", skillDir, err)
			}
		}
	}

	// Verify CheckReviewAssets returns error
	err := CheckReviewAssets(kiroDir)
	if err == nil {
		t.Fatal("CheckReviewAssets() with missing skill returned nil, expected error")
	}

	// Verify error message names the missing asset
	errMsg := err.Error()
	if !strings.Contains(errMsg, "skill:review-protocol") {
		t.Errorf("error message does not name missing skill:review-protocol: %v", errMsg)
	}

	// Verify error message includes the expected path
	expectedPath := filepath.Join(kiroDir, "skills", "review-protocol")
	if !strings.Contains(errMsg, expectedPath) {
		t.Errorf("error message does not include expected path %s: %v", expectedPath, errMsg)
	}
}

// TestCheckReviewAssets_MultipleMissing verifies error lists all missing assets
func TestCheckReviewAssets_MultipleMissing(t *testing.T) {
	kiroDir := t.TempDir()
	agentsDir := filepath.Join(kiroDir, "agents")
	skillsDir := filepath.Join(kiroDir, "skills")

	// Create directory structure
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// Create all required assets EXCEPT python-reviewer agent and review-protocol skill
	skippedAssets := []string{"agent:python-reviewer", "skill:review-protocol", "agent:go-reviewer"}
	for _, asset := range RequiredReviewAssets() {
		skip := false
		for _, skipped := range skippedAssets {
			if asset == skipped {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		parts := strings.SplitN(asset, ":", 2)
		assetType := parts[0]
		assetName := parts[1]

		if assetType == "agent" {
			agentFile := filepath.Join(agentsDir, assetName+".json")
			if err := os.WriteFile(agentFile, []byte("{}"), 0644); err != nil {
				t.Fatalf("failed to create agent file %s: %v", agentFile, err)
			}
		} else if assetType == "skill" {
			skillDir := filepath.Join(skillsDir, assetName)
			if err := os.Mkdir(skillDir, 0755); err != nil {
				t.Fatalf("failed to create skill dir %s: %v", skillDir, err)
			}
		}
	}

	// Verify CheckReviewAssets returns error
	err := CheckReviewAssets(kiroDir)
	if err == nil {
		t.Fatal("CheckReviewAssets() with multiple missing assets returned nil, expected error")
	}

	// Verify error message names all missing assets
	errMsg := err.Error()
	for _, skipped := range skippedAssets {
		if !strings.Contains(errMsg, skipped) {
			t.Errorf("error message does not name missing asset %s: %v", skipped, errMsg)
		}
	}

	// Verify error message includes count of missing assets
	if !strings.Contains(errMsg, "Missing 3 of") {
		t.Errorf("error message does not include count of missing assets: %v", errMsg)
	}
}

// TestCheckReviewAssets_EmptyKiroDir verifies error when kiro directory is completely empty
func TestCheckReviewAssets_EmptyKiroDir(t *testing.T) {
	kiroDir := t.TempDir()

	// Verify CheckReviewAssets returns error
	err := CheckReviewAssets(kiroDir)
	if err == nil {
		t.Fatal("CheckReviewAssets() with empty kiro directory returned nil, expected error")
	}

	// Verify error message indicates all assets are missing
	errMsg := err.Error()
	totalAssets := len(RequiredReviewAssets())
	expectedMsg := fmt.Sprintf("Missing %d of %d", totalAssets, totalAssets)
	if !strings.Contains(errMsg, expectedMsg) {
		t.Errorf("error message does not indicate all assets missing: expected %q in %v", expectedMsg, errMsg)
	}
}
```

**Acceptance Criteria:**

1. All test cases pass with `go test ./internal/review -v`
   - **Verification:** `go test ./internal/review -v` exit status 0

2. Test coverage is comprehensive:
   - All assets present → nil error
   - Missing single agent → descriptive error naming the asset
   - Missing single skill → descriptive error naming the asset
   - Multiple missing assets → error lists all missing items
   - Empty kiro directory → error indicates all assets missing
   - **Verification:** `go test -cover ./internal/review` shows >90% coverage for assets.go

3. Tests use temporary directories (no dependency on real `~/.kiro`)
   - All tests use `t.TempDir()`
   - Tests can run in parallel safely
   - **Verification:** `go test ./internal/review -race` passes with no data races

4. Error message format matches specification
   - Includes count of missing assets
   - Lists each missing asset with expected path
   - Includes symlink instructions
   - **Verification:** Test cases verify exact error message content

## Validation Commands

```bash
# Run all review package tests
go test ./internal/review -v

# Run specific asset validation tests
go test ./internal/review -run TestRequiredReviewAssets -v
go test ./internal/review -run TestCheckReviewAssets -v

# Check test coverage
go test -cover ./internal/review

# Run with race detector
go test ./internal/review -race

# Verify no dependency on real ~/.kiro
# (all tests should pass even if ~/.kiro doesn't exist or has different contents)
go test ./internal/review -v

# Run entire test suite to ensure no regressions
go test ./... -short
```

## Implementation Notes

### Design Rationale

1. **Type-prefixed asset names** — Using `agent:` and `skill:` prefixes in a flat list is simpler than nested structures and makes the asset list self-documenting

2. **Parameterized kiro directory** — Following the pattern from `acp.ValidateAgentResolvable`, the function accepts `kiroDir` as a parameter rather than hardcoding `~/.kiro`, enabling testing without real filesystem dependencies

3. **Fail-loud error messages** — The error format explicitly lists what's missing and where to find it, following the principle from the issue: "Error message is actionable (tells the user what to symlink/where)"

4. **No runtime state** — Both functions are pure and stateless, making them trivially testable and thread-safe

### Future Integration

When the `review` command is implemented (tracked separately), it will call `CheckReviewAssets()` at the start:

```go
func handleReview(prURL string) error {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("failed to get home directory: %w", err)
    }
    kiroDir := filepath.Join(homeDir, ".kiro")
    
    if err := review.CheckReviewAssets(kiroDir); err != nil {
        return fmt.Errorf("asset validation failed:\n%w", err)
    }
    
    // Proceed with review workflow...
}
```

This design keeps the asset validation self-contained and independently testable before the review command itself exists.
