package tui

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// TestMain stubs the human-decision audit seam for the whole package, so no
// TUI test writes decision records into the real ~/PR-Review/audit.
func TestMain(m *testing.M) {
	recordHumanDecisionFunc = func(review.Record, string) func(error) { return func(error) {} }
	os.Exit(m.Run())
}

type recordedDecision struct {
	decision string
	err      error
	finished bool
}

// withRecordedDecisions replaces the audit seam with a recorder.
func withRecordedDecisions(t *testing.T) *[]*recordedDecision {
	t.Helper()
	orig := recordHumanDecisionFunc
	var got []*recordedDecision
	recordHumanDecisionFunc = func(_ review.Record, decision string) func(error) {
		rd := &recordedDecision{decision: decision}
		got = append(got, rd)
		return func(err error) { rd.err = err; rd.finished = true }
	}
	t.Cleanup(func() { recordHumanDecisionFunc = orig })
	return &got
}

func TestDecideActions_RecordHumanDecision(t *testing.T) {
	rec := review.Record{Repo: "owner/repo", PR: 7}
	actErr := errors.New("boom")

	origPost, origRevise, origRereview, origDiscard := postReviewFunc, reviseReviewFunc, rereviewReviewFunc, discardReviewFunc
	t.Cleanup(func() {
		postReviewFunc, reviseReviewFunc, rereviewReviewFunc, discardReviewFunc = origPost, origRevise, origRereview, origDiscard
	})
	postReviewFunc = func(context.Context, review.Record) (int, error) { return 0, actErr }
	reviseReviewFunc = func(context.Context, review.Record, string) (string, error) { return "COMMENT", nil }
	rereviewReviewFunc = func(context.Context, review.Record, string) (string, bool, error) { return "COMMENT", false, nil }
	discardReviewFunc = func(review.Record) error { return nil }

	got := withRecordedDecisions(t)
	m := newDecideRoutingTestModel()

	launchDecidePostCmd(rec, "COMMENT")()
	_, cmd := m.launchDecideRevise(rec, "")
	cmd()
	_, cmd = m.launchDecideRereview(rec, "")
	cmd()
	_, cmd = m.launchDecideDiscard(rec)
	cmd()

	want := []string{"post", "revise", "rereview", "discard"}
	if len(*got) != len(want) {
		t.Fatalf("recorded %d decisions, want %d", len(*got), len(want))
	}
	for i, rd := range *got {
		if rd.decision != want[i] {
			t.Errorf("decision[%d] = %q, want %q", i, rd.decision, want[i])
		}
		if !rd.finished {
			t.Errorf("decision[%d] %q was never finished", i, rd.decision)
		}
	}
	if (*got)[0].err != actErr {
		t.Errorf("post decision err = %v, want the action's error passed through", (*got)[0].err)
	}
}
