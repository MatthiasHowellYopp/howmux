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
	errs := map[string]error{
		"post":     errors.New("post failed"),
		"revise":   errors.New("revise failed"),
		"rereview": errors.New("rereview failed"),
		"discard":  errors.New("discard failed"),
	}

	got := withRecordedDecisions(t)

	// Each fake action checks that its decision was snapshotted BEFORE it ran
	// (post/discard move the spool; revise/rereview rewrite it) and hasn't
	// been finished yet, then fails with its own sentinel error.
	beforeAction := func(decision string) {
		t.Helper()
		if len(*got) == 0 {
			t.Errorf("%s: action ran before the decision was recorded", decision)
			return
		}
		last := (*got)[len(*got)-1]
		if last.decision != decision {
			t.Errorf("%s: latest recorded decision is %q", decision, last.decision)
		}
		if last.finished {
			t.Errorf("%s: decision finished before the action ran", decision)
		}
	}

	origPost, origRevise, origRereview, origDiscard := postReviewFunc, reviseReviewFunc, rereviewReviewFunc, discardReviewFunc
	t.Cleanup(func() {
		postReviewFunc, reviseReviewFunc, rereviewReviewFunc, discardReviewFunc = origPost, origRevise, origRereview, origDiscard
	})
	postReviewFunc = func(context.Context, review.Record) (int, error) {
		beforeAction("post")
		return 0, errs["post"]
	}
	reviseReviewFunc = func(context.Context, review.Record, string) (string, error) {
		beforeAction("revise")
		return "", errs["revise"]
	}
	rereviewReviewFunc = func(context.Context, review.Record, string) (string, bool, error) {
		beforeAction("rereview")
		return "", false, errs["rereview"]
	}
	discardReviewFunc = func(review.Record) error {
		beforeAction("discard")
		return errs["discard"]
	}

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
		if rd.err != errs[want[i]] {
			t.Errorf("decision[%d] %q err = %v, want %v", i, rd.decision, rd.err, errs[want[i]])
		}
	}
}
