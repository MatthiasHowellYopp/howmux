package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// stubReviseReviewFunc substitutes the package-level reviseReviewFunc seam
// (decideactions.go) with fn for the duration of a test, returning a
// restore closure. Mirrors the *Func substitution pattern this codebase
// already uses throughout (ensureCheckoutFunc, runReviewFunc, etc.) — used
// here so revise/rereview/post-adjacent decide tests never actually shell
// out to kiro-cli/gh via internal/review's own private seams, which are not
// reachable from this package.
func stubReviseReviewFunc(fn func(ctx context.Context, rec review.Record, inlineNotes string) (string, error)) func() {
	original := reviseReviewFunc
	reviseReviewFunc = fn
	return func() { reviseReviewFunc = original }
}

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

	var gotNotes string
	restore := stubReviseReviewFunc(func(ctx context.Context, rec review.Record, inlineNotes string) (string, error) {
		gotNotes = inlineNotes
		return "APPROVE", nil
	})
	defer restore()

	// revise now opens the multi-line notes composer instead of launching
	// immediately; the launch happens on Ctrl+D. Use the fuller routing
	// model so the composer keypress-interception path in model.Update has
	// the input/footer plumbing it dereferences.
	m := newDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 7, SpoolPath: spoolPath})

	opened, _ := m.handleDecide([]string{"revise"})
	if !opened.reviseComposeActive {
		t.Fatalf("expected handleDecide(revise) to open the notes composer (reviseComposeActive=true)")
	}
	if !opened.notesComposer.Focused() {
		t.Fatalf("expected the composer to be focused after opening")
	}

	// Type multi-line notes into the composer, then submit with Ctrl+D.
	opened.notesComposer.SetValue("recheck the auth flow\nand add a test")
	submitted, cmd := opened.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	submittedModel, ok := submitted.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if submittedModel.reviseComposeActive {
		t.Errorf("expected compose mode to exit after Ctrl+D, still active")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the 'revise' launch after Ctrl+D, got nil")
	}
	if !anyLineContains(submittedModel.activityLines, "Revising review for owner/repo#7...") {
		t.Errorf("expected in-progress activity line, got: %v", submittedModel.activityLines)
	}

	msg := drainBatchForType[decideReviseCompleteMsg](t, cmd)
	if msg.rec.PR != 7 || msg.newVerdict != "APPROVE" {
		t.Errorf("unexpected decideReviseCompleteMsg: %+v", msg)
	}
	if gotNotes != "recheck the auth flow\nand add a test" {
		t.Errorf("expected the typed multi-line notes to reach the seam, got %q", gotNotes)
	}

	updated, finalCmd := submittedModel.Update(msg)
	if finalCmd != nil {
		t.Errorf("expected nil cmd from decideReviseCompleteMsg handling, got %v", finalCmd)
	}
	finalModel, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if !anyLineContains(finalModel.activityLines, "Revised review for owner/repo#7 — new verdict: APPROVE, back in pending/.") {
		t.Errorf("expected success activity line containing PR number and new verdict, got: %v", finalModel.activityLines)
	}
}

func TestHandleDecide_ValidSpoolPath_WriterError(t *testing.T) {
	spoolPath := withFakeDecisionScript(t)

	restore := stubReviseReviewFunc(func(ctx context.Context, rec review.Record, inlineNotes string) (string, error) {
		return "", fmt.Errorf("revise failed for owner/repo#9: kiro-cli exited 3: spool file not found: /tmp/spool.md")
	})
	defer restore()

	m := newDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 9, SpoolPath: spoolPath})

	opened, _ := m.handleDecide([]string{"revise"})
	if !opened.reviseComposeActive {
		t.Fatalf("expected handleDecide(revise) to open the notes composer")
	}

	submitted, cmd := opened.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	submittedModel, ok := submitted.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the 'revise' launch after Ctrl+D, got nil")
	}

	msg := drainBatchForType[decideReviseErrorMsg](t, cmd)
	updated, finalCmd := submittedModel.Update(msg)
	if finalCmd != nil {
		t.Errorf("expected nil cmd from decideReviseErrorMsg handling, got %v", finalCmd)
	}
	finalModel, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	found := false
	for _, line := range finalModel.activityLines {
		if strings.Contains(line, "Failed to revise review for owner/repo#9:") && strings.Contains(line, "spool file not found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error activity line containing wrapped error text, got: %v", finalModel.activityLines)
	}
}

func TestHandleDecide_WorksWhenDifferentTabActive(t *testing.T) {
	spoolPath := withFakeDecisionScript(t)

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
		t.Errorf("expected nil cmd (post opens the confirm gate synchronously, nothing launched yet), got %v", cmd)
	}
	if result.decidePostConfirmState != decidePostConfirmAwaiting {
		t.Fatalf("expected decidePostConfirmAwaiting even with a different tab active, got %v", result.decidePostConfirmState)
	}
	if result.decidePostPending.PR != 13 {
		t.Errorf("expected decidePostPending.PR == 13, got %d", result.decidePostPending.PR)
	}
	if !anyLineContains(result.activityLines, "to owner/repo#13?") {
		t.Errorf("expected confirm-prompt activity line even with a different tab active, got: %v", result.activityLines)
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
