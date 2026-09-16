package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequiredReviewAssets_WellFormed is a hermetic drift guard: it asserts
// the hardcoded list stays structurally valid as entries are added/renamed,
// without depending on the real ~/.kiro. It checks each entry parses as a
// known type:name, that there are no duplicates, and that krew-lead is not
// present (it is intentionally excluded per #73 — howmux runs reviews via
// pr_review.py, not by spawning krew-lead).
func TestRequiredReviewAssets_WellFormed(t *testing.T) {
	assets := RequiredReviewAssets()
	seen := make(map[string]bool, len(assets))

	for _, asset := range assets {
		if seen[asset] {
			t.Errorf("duplicate asset in list: %q", asset)
		}
		seen[asset] = true

		parts := strings.SplitN(asset, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			t.Errorf("malformed asset %q (expected non-empty type:name)", asset)
			continue
		}
		if parts[0] != "agent" && parts[0] != "skill" {
			t.Errorf("asset %q has unknown type %q (expected agent or skill)", asset, parts[0])
		}
	}

	if seen["agent:krew-lead"] {
		t.Error("agent:krew-lead must not be in the review asset list (excluded per #73)")
	}
}

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
			// Create skill directory with a SKILL.md (what actually loads)
			skillDir := filepath.Join(skillsDir, assetName)
			if err := os.Mkdir(skillDir, 0755); err != nil {
				t.Fatalf("failed to create skill dir %s: %v", skillDir, err)
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill"), 0644); err != nil {
				t.Fatalf("failed to create SKILL.md in %s: %v", skillDir, err)
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
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill"), 0644); err != nil {
				t.Fatalf("failed to create SKILL.md in %s: %v", skillDir, err)
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
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill"), 0644); err != nil {
				t.Fatalf("failed to create SKILL.md in %s: %v", skillDir, err)
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
	expectedPath := filepath.Join(kiroDir, "skills", "review-protocol", "SKILL.md")
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
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill"), 0644); err != nil {
				t.Fatalf("failed to create SKILL.md in %s: %v", skillDir, err)
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
