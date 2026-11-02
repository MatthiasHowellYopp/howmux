package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	// A second record stays marked throughout, so a clear-all bug (rather
	// than clearing only msg.rec) fails here.
	other := review.Record{Repo: "owner/repo", PR: 99, SpoolPath: "/tmp/spool-99.md"}
	for _, tc := range cases {
		m := newDecideTestModel()
		rt := addReviewsTabWithRecord(m, rec)
		rt.MarkInFlight(rec, tc.action)
		rt.MarkInFlight(other, "rereview")
		m.Update(tc.msg)
		if _, ok := rt.InFlightAction(rec); ok {
			t.Errorf("%T: in-flight flag not cleared", tc.msg)
		}
		if action, ok := rt.InFlightAction(other); !ok || action != "rereview" {
			t.Errorf("%T: cleared another record's in-flight flag (got %q, %v)", tc.msg, action, ok)
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

// ctrlD is the composer's submit key.
var ctrlD = tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl})

// TestDecideInFlight_PostConfirmMarksInFlight drives the real confirm gate:
// y must mark post as in flight, so a discard right after is refused.
func TestDecideInFlight_PostConfirmMarksInFlight(t *testing.T) {
	spoolPath := newPostSpoolFixture(t, t.TempDir(), "owner-repo-55.md")
	rec := review.Record{Repo: "owner/repo", PR: 55, SpoolPath: spoolPath}
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	updated, cmd := m.Update(pressKey('y'))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("expected y to launch the post")
	}
	rt := m.findReviewsTab()
	if action, _ := rt.InFlightAction(rec); action != "post" {
		t.Fatalf("expected post in flight after y, got %q", action)
	}
	if _, cmd := m.dispatchDecideAction(rec, "discard"); cmd != nil {
		t.Error("expected discard to be refused while the post runs")
	}
}

// TestDecideInFlight_PostConfirmRefusedAfterRereviewLaunched covers the
// gap where the post gate lets other keys through: with the gate waiting,
// open a rereview composer and submit it, then press y. The post must be
// refused rather than archiving the spool under the running rereview.
func TestDecideInFlight_PostConfirmRefusedAfterRereviewLaunched(t *testing.T) {
	spoolPath := newPostSpoolFixture(t, t.TempDir(), "owner-repo-55.md")
	rec := review.Record{Repo: "owner/repo", PR: 55, SpoolPath: spoolPath}
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	m, _ = m.handleDecide([]string{"rereview"})
	if !m.reviseComposeActive {
		t.Fatal("expected the rereview composer to open under the post gate")
	}
	updated, cmd := m.Update(ctrlD)
	m = updated.(model)
	if cmd == nil {
		t.Fatal("expected Ctrl+D to launch the rereview")
	}
	if action, _ := m.findReviewsTab().InFlightAction(rec); action != "rereview" {
		t.Fatalf("expected rereview in flight, got %q", action)
	}

	updated, cmd = m.Update(pressKey('y'))
	m = updated.(model)
	if cmd != nil {
		t.Error("expected y to be refused while the rereview runs")
	}
	if !anyLineContains(m.activityLines, "rereview already running for owner/repo#55") {
		t.Errorf("expected an already-running warning, got: %v", m.activityLines)
	}
	if action, _ := m.findReviewsTab().InFlightAction(rec); action != "rereview" {
		t.Errorf("refused post must not overwrite the rereview mark, got %q", action)
	}
}

// TestDecideInFlight_ComposerSubmitRefusedWhileRunning checks Ctrl+D
// re-checks the guard: an action that started after the composer opened
// blocks the launch.
func TestDecideInFlight_ComposerSubmitRefusedWhileRunning(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 7, SpoolPath: "/tmp/spool-7.md"}
	m := newDecideRoutingTestModel()
	rt := addReviewsTabWithRecord(m, rec)

	m, _ = m.handleDecide([]string{"rereview"})
	if !m.reviseComposeActive {
		t.Fatal("expected the rereview composer to open")
	}
	rt.MarkInFlight(rec, "post")

	updated, cmd := m.Update(ctrlD)
	m = updated.(model)
	if cmd != nil {
		t.Error("expected Ctrl+D to be refused while a post runs")
	}
	if m.reviseComposeActive {
		t.Error("expected the composer to close on a refused submit")
	}
	if !anyLineContains(m.activityLines, "post already running for owner/repo#7") {
		t.Errorf("expected an already-running warning, got: %v", m.activityLines)
	}
}

// TestDecideInFlight_BodyEditRefusedWhileRunning checks e is refused during
// a revise/rereview, so a stale edit can't overwrite the new body.
func TestDecideInFlight_BodyEditRefusedWhileRunning(t *testing.T) {
	t.Setenv("EDITOR", "vi")
	capturedArgv := withFakeBodyEditCommand(t, 0)

	m := newBodyEditTestModel(t)
	rec := review.Record{Repo: "owner/repo", PR: 7, SpoolPath: filepath.Join(t.TempDir(), "spool.md")}
	m.findReviewsTab().MarkInFlight(rec, "rereview")

	m, cmd := m.handleBodyEditLaunch(rec)
	if cmd != nil {
		t.Error("expected body edit to be refused while a rereview runs")
	}
	if len(*capturedArgv) != 0 {
		t.Errorf("editor must not be launched, got argv %v", *capturedArgv)
	}
	if !anyLineContains(m.activityLines, "rereview already running for owner/repo#7") {
		t.Errorf("expected an already-running warning, got: %v", m.activityLines)
	}
}
