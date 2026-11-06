package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/github"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

// withRealReviewClaims swaps TestMain's always-succeed claim stub for the
// real process-wide registry.
func withRealReviewClaims(t *testing.T) {
	t.Helper()
	orig := claimReviewFunc
	claimReviewFunc = review.ClaimReview
	t.Cleanup(func() { claimReviewFunc = orig })
}

func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				_ = child()
			}
		}
	}
}

// The manual review claims the PR for the whole review and gate (so the
// watcher won't start a duplicate), refuses a second review of the same PR
// while the first runs, and releases the claim when it's done.
func TestHandleReview_ClaimsPRForReviewAndGate(t *testing.T) {
	if err := review.CheckReviewAssets(filepath.Join(os.Getenv("HOME"), ".kiro")); err != nil {
		t.Skipf("Skipping test - review assets not available: %v", err)
	}
	withRealReviewClaims(t)
	const repo, pr = "owner/claimrepo", 4242

	oldGetPR, oldCheckout, oldRun, oldGate := getPRFunc, ensureCheckoutFunc, runReviewFunc, autoPostGateFunc
	t.Cleanup(func() {
		getPRFunc, ensureCheckoutFunc, runReviewFunc, autoPostGateFunc = oldGetPR, oldCheckout, oldRun, oldGate
		_ = review.NewDefaultStore().Remove(repo, pr)
	})
	getPRFunc = func(string, int) (github.PR, error) { return github.PR{HeadRefOid: "abc123"}, nil }
	ensureCheckoutFunc = func(string, string, string, int, string) error { return nil }

	m := newTestModel()
	defer m.reviewWatcher.Stop()
	url := "https://github.com/owner/claimrepo/pull/4242"

	var heldDuringReview, heldDuringGate bool
	var secondRefused bool
	runReviewFunc = func(ctx context.Context, rec review.Record, sha string, s review.StoreInterface, w io.Writer) error {
		heldDuringReview = review.ReviewInFlight(repo, pr)
		// A second `review <URL>` while this one runs must be refused.
		m2, cmd := m.handleReview([]string{url})
		secondRefused = cmd == nil && strings.Contains(strings.Join(m2.activityLines, "\n"), "already in progress")
		return nil
	}
	autoPostGateFunc = func(ctx context.Context, rec review.Record, sha string, s review.StoreInterface, cfg review.AutoPostConfig) (review.GateResult, error) {
		heldDuringGate = review.ReviewInFlight(repo, pr)
		return review.GateResult{}, nil
	}

	_, cmd := m.handleReview([]string{url})
	if cmd == nil {
		t.Fatal("expected a review command")
	}
	if !review.ReviewInFlight(repo, pr) {
		t.Error("PR not claimed when the review was started")
	}
	runBatch(cmd)

	if !heldDuringReview || !heldDuringGate {
		t.Errorf("claim held during review=%v gate=%v, want both", heldDuringReview, heldDuringGate)
	}
	if !secondRefused {
		t.Error("a second review of the same PR was not refused while the first ran")
	}
	if review.ReviewInFlight(repo, pr) {
		t.Error("claim not released after the review finished")
	}
}

// A human decision is refused while the PR's review or gate is in flight.
func TestDecide_RefusedWhileReviewInFlight(t *testing.T) {
	orig := reviewInFlightFunc
	t.Cleanup(func() { reviewInFlightFunc = orig })
	reviewInFlightFunc = func(repo string, pr int) bool { return repo == "owner/repo" && pr == 7 }

	m := newDecideTestModel()
	m2, refused := m.refuseIfDecideInFlight(review.Record{Repo: "owner/repo", PR: 7})
	if !refused {
		t.Fatal("decision not refused while the review is in flight")
	}
	if !strings.Contains(strings.Join(m2.activityLines, "\n"), "auto-post gate still running") {
		t.Errorf("activity = %v, want the gate-running warning", m2.activityLines)
	}
	if _, refused := m.refuseIfDecideInFlight(review.Record{Repo: "owner/repo", PR: 8}); refused {
		t.Error("decision on a different PR was refused")
	}
}
