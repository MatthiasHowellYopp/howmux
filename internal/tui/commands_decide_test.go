package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newDecideTestModel builds a model with the collaborators handleDecide
// needs: styles, a real *review.DecisionWriter, and a TabManager. Unlike
// newTestModel() (commands_test.go), this helper always populates
// m.tabManager so tests can add a *ReviewsTab (or, for the "no Reviews tab"
// case, deliberately omit one) and exercise findReviewsTab()'s scan.
func newDecideTestModel() model {
	return model{
		activityLines:  []string{},
		styles:         newTestStyles(),
		tabManager:     NewTabManager(),
		decisionWriter: review.NewDecisionWriter(),
	}
}

// addReviewsTabWithRecord adds a *ReviewsTab backed by a fakeReviewStore
// containing exactly one record to m's tabManager, selects that record (via
// a View() render, which seeds selection to the first row per
// renderTable's reconciliation), and returns the tab so callers can also
// add other tabs before/after or assert on it directly.
func addReviewsTabWithRecord(m model, rec review.Record) *ReviewsTab {
	store := &fakeReviewStore{records: []review.Record{rec}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed rt.lastOrder + selectedKey to the first (only) row
	m.tabManager.AddTab(rt)
	return rt
}

// withFakeDecisionScript points the *review* package's script-path
// resolution at a fake bash script under a temp CWD (scriptPathFunc
// resolves ".howmux/scripts/set-review-decision.sh" relative to CWD, and
// execCommandFunc is the real exec.Command — both are package-private to
// internal/review, so from this package the only available seam is the
// real filesystem path DecisionWriter.SetDecision shells out to). The fake
// script's behavior is controlled per-invocation via the
// FAKE_DECISION_EXIT / FAKE_DECISION_STDERR env vars so a single script
// file serves both the success and failure test cases below. t.Chdir
// restores the original working directory automatically on test cleanup.
func withFakeDecisionScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, ".howmux", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("failed to create fake scripts dir: %v", err)
	}
	scriptPath := filepath.Join(scriptsDir, "set-review-decision.sh")
	script := `#!/bin/bash
if [ -n "$FAKE_DECISION_STDERR" ]; then
  echo "$FAKE_DECISION_STDERR" 1>&2
fi
exit "${FAKE_DECISION_EXIT:-0}"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake script: %v", err)
	}
	// SetDecision now resolves+stats the spool file (pending→done) before
	// shelling out, so provide a real pending/ spool for the writer to find.
	// Returned so tests use this path as the record's SpoolPath.
	pendingDir := filepath.Join(dir, "PR-Review", "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to create fake pending dir: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo.md")
	if err := os.WriteFile(spoolPath, []byte("---\ndecision:\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("failed to write fake spool: %v", err)
	}
	t.Chdir(dir)
	return spoolPath
}

func TestHandleDecide_WrongArgCount(t *testing.T) {
	cases := [][]string{
		{},
		{"post", "extra"},
	}
	for _, args := range cases {
		t.Run(fmt.Sprintf("%v", args), func(t *testing.T) {
			m := newDecideTestModel()
			addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 1, SpoolPath: "/tmp/spool.md"})

			result, cmd := m.handleDecide(args)
			if cmd != nil {
				t.Errorf("expected nil cmd, got %v", cmd)
			}
			if !anyLineContains(result.activityLines, "Usage: decide post|revise|rereview|discard") {
				t.Errorf("expected usage error line, got: %v", result.activityLines)
			}
		})
	}
}

func TestHandleDecide_InvalidVocabulary(t *testing.T) {
	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 1, SpoolPath: "/tmp/spool.md"})

	result, cmd := m.handleDecide([]string{"bogus"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "Invalid decision: bogus (must be post, revise, rereview, or discard)") {
		t.Errorf("expected invalid-vocabulary error line, got: %v", result.activityLines)
	}
}

func TestHandleDecide_NoReviewsTabPresent(t *testing.T) {
	m := newDecideTestModel()
	// Deliberately do not add a Reviews tab — defensive case, should not occur
	// in practice since ReviewsTab is permanent, but must not panic.

	var result model
	var cmd tea.Cmd
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("handleDecide panicked with no Reviews tab present: %v", r)
			}
		}()
		result, cmd = m.handleDecide([]string{"post"})
	}()

	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "No review selected — switch to the Reviews tab and select a row first") {
		t.Errorf("expected no-selection error line, got: %v", result.activityLines)
	}
}

func TestHandleDecide_ReviewsTabPresentNoRowSelected(t *testing.T) {
	m := newDecideTestModel()
	// Empty store: no rows exist, so nothing can be selected.
	store := &fakeReviewStore{records: nil}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View()
	m.tabManager.AddTab(rt)

	result, cmd := m.handleDecide([]string{"post"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "No review selected — switch to the Reviews tab and select a row first") {
		t.Errorf("expected no-selection error line, got: %v", result.activityLines)
	}
}

func TestHandleDecide_RowSelectedNoSpoolPath(t *testing.T) {
	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 42, SpoolPath: ""})

	result, cmd := m.handleDecide([]string{"post"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "PR #42 has no review yet — nothing to decide") {
		t.Errorf("expected no-review-yet error line, got: %v", result.activityLines)
	}
}

func TestHandleDecide_ValidSpoolPath_WriterSuccess(t *testing.T) {
	spoolPath := withFakeDecisionScript(t)
	os.Setenv("FAKE_DECISION_EXIT", "0")
	os.Unsetenv("FAKE_DECISION_STDERR")
	defer os.Unsetenv("FAKE_DECISION_EXIT")

	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 7, SpoolPath: spoolPath})

	result, cmd := m.handleDecide([]string{"revise"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "Set decision on PR #7 to 'revise'") {
		t.Errorf("expected success activity line containing PR number and decision, got: %v", result.activityLines)
	}
}

func TestHandleDecide_ValidSpoolPath_WriterError(t *testing.T) {
	spoolPath := withFakeDecisionScript(t)
	os.Setenv("FAKE_DECISION_EXIT", "3")
	os.Setenv("FAKE_DECISION_STDERR", "error: spool file not found: /tmp/spool.md")
	defer os.Unsetenv("FAKE_DECISION_EXIT")
	defer os.Unsetenv("FAKE_DECISION_STDERR")

	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 9, SpoolPath: spoolPath})

	result, cmd := m.handleDecide([]string{"discard"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	found := false
	for _, line := range result.activityLines {
		if strings.Contains(line, "Failed to set decision:") && strings.Contains(line, "spool file not found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error activity line containing wrapped error text, got: %v", result.activityLines)
	}
}

func TestHandleDecide_WorksWhenDifferentTabActive(t *testing.T) {
	spoolPath := withFakeDecisionScript(t)
	os.Setenv("FAKE_DECISION_EXIT", "0")
	os.Unsetenv("FAKE_DECISION_STDERR")
	defer os.Unsetenv("FAKE_DECISION_EXIT")

	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 13, SpoolPath: spoolPath})

	// Add a second tab and make it the active one — decide must still find
	// and act on the Reviews tab's selection, since findReviewsTab locates
	// by Type(), not by "is it currently active" (matches how `review
	// <PR_URL>` and `status` already work regardless of active tab).
	m.tabManager.AddTab(NewMainTab())
	m.tabManager.SetActiveTab(1)
	if activeTab := m.tabManager.GetActiveTab(); activeTab == nil || activeTab.Type() == TabTypeReviews {
		t.Fatalf("test setup broken: active tab should be the non-Reviews tab")
	}

	result, cmd := m.handleDecide([]string{"post"})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "Set decision on PR #13 to 'post'") {
		t.Errorf("expected success activity line even with a different tab active, got: %v", result.activityLines)
	}
}

// anyLineContains reports whether any line in lines contains substr.
func anyLineContains(lines []string, substr string) bool {
	for _, line := range lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}
