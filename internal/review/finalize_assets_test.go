package review

import (
	"fmt"
	"strings"
	"testing"
)

// TestRequiredFinalizeAssets verifies the canonical asset list is exactly
// the two tool: entries the finalize gate depends on.
func TestRequiredFinalizeAssets(t *testing.T) {
	assets := RequiredFinalizeAssets()

	expected := []string{"tool:finalize-reviews.sh", "tool:pr_review_finalize.py"}
	if len(assets) != len(expected) {
		t.Fatalf("RequiredFinalizeAssets() = %v, want %v", assets, expected)
	}
	for i, want := range expected {
		if assets[i] != want {
			t.Errorf("RequiredFinalizeAssets()[%d] = %q, want %q", i, assets[i], want)
		}
	}
}

// TestCheckFinalizeAssets_AllPresent verifies nil error when both scripts
// resolve on PATH.
func TestCheckFinalizeAssets_AllPresent(t *testing.T) {
	origLookPath := lookPathFunc
	lookPathFunc = func(string) (string, error) { return "/usr/local/bin/stub", nil }
	t.Cleanup(func() { lookPathFunc = origLookPath })

	if err := CheckFinalizeAssets(); err != nil {
		t.Errorf("CheckFinalizeAssets() with both scripts present returned error: %v", err)
	}
}

// TestCheckFinalizeAssets_MissingFinalizeScript verifies error when only
// finalize-reviews.sh is missing.
func TestCheckFinalizeAssets_MissingFinalizeScript(t *testing.T) {
	origLookPath := lookPathFunc
	lookPathFunc = func(name string) (string, error) {
		if name == "finalize-reviews.sh" {
			return "", fmt.Errorf("not found")
		}
		return "/usr/local/bin/stub", nil
	}
	t.Cleanup(func() { lookPathFunc = origLookPath })

	err := CheckFinalizeAssets()
	if err == nil {
		t.Fatal("CheckFinalizeAssets() with finalize-reviews.sh missing returned nil, expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "tool:finalize-reviews.sh") {
		t.Errorf("error message does not name missing tool:finalize-reviews.sh: %v", errMsg)
	}
	if strings.Contains(errMsg, "tool:pr_review_finalize.py") {
		t.Errorf("error message unexpectedly names tool:pr_review_finalize.py as missing: %v", errMsg)
	}
	if !strings.Contains(errMsg, "Missing 1 of 2") {
		t.Errorf("error message does not include correct missing count: %v", errMsg)
	}
	if !strings.Contains(errMsg, "PATH") {
		t.Errorf("error message does not mention PATH: %v", errMsg)
	}
	if !strings.Contains(errMsg, "custom-scripts.md") {
		t.Errorf("error message does not reference custom-scripts.md in the fix-it block: %v", errMsg)
	}
	if !strings.Contains(errMsg, "~/.local/bin") {
		t.Errorf("error message does not reference ~/.local/bin in the fix-it block: %v", errMsg)
	}
}

// TestCheckFinalizeAssets_MissingFinalizePyScript verifies error when only
// pr_review_finalize.py is missing.
func TestCheckFinalizeAssets_MissingFinalizePyScript(t *testing.T) {
	origLookPath := lookPathFunc
	lookPathFunc = func(name string) (string, error) {
		if name == "pr_review_finalize.py" {
			return "", fmt.Errorf("not found")
		}
		return "/usr/local/bin/stub", nil
	}
	t.Cleanup(func() { lookPathFunc = origLookPath })

	err := CheckFinalizeAssets()
	if err == nil {
		t.Fatal("CheckFinalizeAssets() with pr_review_finalize.py missing returned nil, expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "tool:pr_review_finalize.py") {
		t.Errorf("error message does not name missing tool:pr_review_finalize.py: %v", errMsg)
	}
	if strings.Contains(errMsg, "tool:finalize-reviews.sh") {
		t.Errorf("error message unexpectedly names tool:finalize-reviews.sh as missing: %v", errMsg)
	}
	if !strings.Contains(errMsg, "Missing 1 of 2") {
		t.Errorf("error message does not include correct missing count: %v", errMsg)
	}
}

// TestCheckFinalizeAssets_BothMissing verifies error names both scripts and
// reports a count of "2 of 2" when neither resolves on PATH.
func TestCheckFinalizeAssets_BothMissing(t *testing.T) {
	origLookPath := lookPathFunc
	lookPathFunc = func(string) (string, error) { return "", fmt.Errorf("not found") }
	t.Cleanup(func() { lookPathFunc = origLookPath })

	err := CheckFinalizeAssets()
	if err == nil {
		t.Fatal("CheckFinalizeAssets() with both scripts missing returned nil, expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "tool:finalize-reviews.sh") {
		t.Errorf("error message does not name missing tool:finalize-reviews.sh: %v", errMsg)
	}
	if !strings.Contains(errMsg, "tool:pr_review_finalize.py") {
		t.Errorf("error message does not name missing tool:pr_review_finalize.py: %v", errMsg)
	}
	if !strings.Contains(errMsg, "Missing 2 of 2") {
		t.Errorf("error message does not include correct missing count: %v", errMsg)
	}
}
