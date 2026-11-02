package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// TestDecideInFlight_RereviewShowsInDecisionState checks that launching a
// rereview flips the row's DECISION STATE from the spool's "pending" to
// "rereviewing..." while it runs, and back once the terminal message lands.
func TestDecideInFlight_RereviewShowsInDecisionState(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 145, SpoolPath: "/tmp/spool-145.md"}
	withFakeSpoolInfoFunc(t, map[string]review.SpoolInfo{
		rec.SpoolPath: {Found: true, Verdict: "REQUEST_CHANGES", DecisionState: "pending"},
	})

	m := newDecideTestModel()
	rt := addReviewsTabWithRecord(m, rec)
	if !strings.Contains(rt.CopyableContent(), "pending") {
		t.Fatalf("precondition: expected pending row, got:\n%s", rt.CopyableContent())
	}

	m, cmd := m.launchDecideRereview(rec, "")
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from launchDecideRereview")
	}
	if got := rt.CopyableContent(); !strings.Contains(got, "rereviewing...") {
		t.Errorf("expected rereviewing... in DECISION STATE while running, got:\n%s", got)
	}

	result, _ := m.Update(decideRereviewCompleteMsg{rec: rec, newVerdict: "COMMENT"})
	m = result.(model)
	if _, ok := rt.InFlightAction(rec); ok {
		t.Error("expected in-flight flag cleared after decideRereviewCompleteMsg")
	}
	if got := rt.CopyableContent(); strings.Contains(got, "rereviewing...") {
		t.Errorf("expected rereviewing... gone after completion, got:\n%s", got)
	}
}

// TestDecideInFlight_BlocksOverlappingDecision reproduces the PR 145 race:
// a discard issued while a rereview is still running must be refused,
// rather than archiving the spool file out from under the rereview.
func TestDecideInFlight_BlocksOverlappingDecision(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 145, SpoolPath: "/tmp/spool-145.md"}

	discardCalled := false
	origDiscard := discardReviewFunc
	discardReviewFunc = func(review.Record) error { discardCalled = true; return nil }
	t.Cleanup(func() { discardReviewFunc = origDiscard })

	m := newDecideTestModel()
	rt := addReviewsTabWithRecord(m, rec)

	m, _ = m.launchDecideRereview(rec, "")

	m, cmd := m.dispatchDecideAction(rec, "discard")
	if cmd != nil {
		t.Fatal("expected no tea.Cmd: discard should be refused while rereview runs")
	}
	if discardCalled {
		t.Error("discard must not run while a rereview is in flight")
	}
	if !anyLineContains(m.activityLines, "rereview already running for owner/repo#145") {
		t.Errorf("expected an already-running warning, got: %v", m.activityLines)
	}
	if action, _ := rt.InFlightAction(rec); action != "rereview" {
		t.Errorf("in-flight action should still be rereview, got %q", action)
	}

	// Once the rereview finishes (even with an error), decisions are allowed again.
	result, _ := m.Update(decideRereviewErrorMsg{rec: rec, err: errors.New("boom")})
	m = result.(model)
	m, cmd = m.dispatchDecideAction(rec, "discard")
	if cmd == nil {
		t.Fatal("expected discard to launch after the rereview finished")
	}
	cmd()
	if !discardCalled {
		t.Error("expected discard to run after the rereview finished")
	}
}

// TestDecideInFlight_ClearedByEveryTerminalMessage guards the invariant that
// no completion/error path leaves a row stuck as "...ing".
func TestDecideInFlight_ClearedByEveryTerminalMessage(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 7, SpoolPath: "/tmp/spool-7.md"}
	errBoom := errors.New("boom")
	cases := []struct {
		action string
		msg    any
	}{
		{"post", decidePostCompleteMsg{rec: rec}},
		{"post", decidePostErrorMsg{rec: rec, err: errBoom}},
		{"revise", decideReviseCompleteMsg{rec: rec}},
		{"revise", decideReviseErrorMsg{rec: rec, err: errBoom}},
		{"rereview", decideRereviewCompleteMsg{rec: rec}},
		{"rereview", decideRereviewErrorMsg{rec: rec, err: errBoom}},
		{"discard", decideDiscardCompleteMsg{rec: rec}},
		{"discard", decideDiscardErrorMsg{rec: rec, err: errBoom}},
	}
	for _, tc := range cases {
		m := newDecideTestModel()
		rt := addReviewsTabWithRecord(m, rec)
		rt.MarkInFlight(rec, tc.action)
		m.Update(tc.msg)
		if _, ok := rt.InFlightAction(rec); ok {
			t.Errorf("%T: in-flight flag not cleared", tc.msg)
		}
	}
}

// TestDecideInFlight_ReviseAndDiscardMark checks the other launchers set the
// flag too (post is marked in the confirm gate's "y" branch in tui.go).
func TestDecideInFlight_ReviseAndDiscardMark(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 8, SpoolPath: "/tmp/spool-8.md"}

	origRevise := reviseReviewFunc
	reviseReviewFunc = func(context.Context, review.Record, string) (string, error) { return "COMMENT", nil }
	t.Cleanup(func() { reviseReviewFunc = origRevise })

	m := newDecideTestModel()
	rt := addReviewsTabWithRecord(m, rec)
	m, _ = m.launchDecideRevise(rec, "")
	if action, _ := rt.InFlightAction(rec); action != "revise" {
		t.Errorf("expected revise in flight, got %q", action)
	}
	rt.ClearInFlight(rec)

	_, _ = m.launchDecideDiscard(rec)
	if action, _ := rt.InFlightAction(rec); action != "discard" {
		t.Errorf("expected discard in flight, got %q", action)
	}
}
