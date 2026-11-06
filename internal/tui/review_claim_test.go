package tui

import (
	"context"
	"io"
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
	withRealReviewClaims(t)
	const repo, pr = "owner/claimrepo", 4242

	oldAssets, oldGetPR, oldCheckout, oldRun, oldGate := checkReviewAssetsFunc, getPRFunc, ensureCheckoutFunc, runReviewFunc, autoPostGateFunc
	t.Cleanup(func() {
		checkReviewAssetsFunc, getPRFunc, ensureCheckoutFunc, runReviewFunc, autoPostGateFunc = oldAssets, oldGetPR, oldCheckout, oldRun, oldGate
		_ = review.NewDefaultStore().Remove(repo, pr)
	})
	// Stub the preflight so this test runs in CI, where ~/.kiro has no
	// review assets; it guards the root-cause fix for duplicate reviews.
	checkReviewAssetsFunc = func(string) error { return nil }
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

// A human decision claims the PR while it runs, so the watcher and a manual
// `review <URL>` don't start a review that rewrites the spool under it, and
// releases the claim when it finishes. A decision is refused if a review
// claimed the PR first.
func TestDecideLaunches_ClaimPRWhileRunning(t *testing.T) {
	withRealReviewClaims(t)
	origRevise, origRereview, origDiscard := reviseReviewFunc, rereviewReviewFunc, discardReviewFunc
	t.Cleanup(func() {
		reviseReviewFunc, rereviewReviewFunc, discardReviewFunc = origRevise, origRereview, origDiscard
	})
	rec := review.Record{Repo: "owner/decideclaim", PR: 77, SpoolPath: "/tmp/x.md"}
	var held bool
	reviseReviewFunc = func(context.Context, review.Record, string) (string, error) {
		held = review.ReviewInFlight(rec.Repo, rec.PR)
		return "COMMENT", nil
	}
	rereviewReviewFunc = func(context.Context, review.Record, string) (string, string, error) {
		held = review.ReviewInFlight(rec.Repo, rec.PR)
		return "COMMENT", "", nil
	}
	discardReviewFunc = func(review.Record) error {
		held = review.ReviewInFlight(rec.Repo, rec.PR)
		return nil
	}

	launches := map[string]func(model) (model, tea.Cmd){
		"revise":   func(m model) (model, tea.Cmd) { return m.launchDecideRevise(rec, "") },
		"rereview": func(m model) (model, tea.Cmd) { return m.launchDecideRereview(rec, "") },
		"discard":  func(m model) (model, tea.Cmd) { return m.launchDecideDiscard(rec) },
	}
	for name, launch := range launches {
		t.Run(name, func(t *testing.T) {
			held = false
			_, cmd := launch(newDecideTestModel())
			if cmd == nil {
				t.Fatal("expected a decide command")
			}
			if !review.ReviewInFlight(rec.Repo, rec.PR) {
				t.Error("PR not claimed when the decision launched")
			}
			cmd()
			if !held {
				t.Error("claim not held while the decision ran")
			}
			if review.ReviewInFlight(rec.Repo, rec.PR) {
				t.Error("claim not released after the decision finished")
			}

			// With a review holding the PR, the decision is refused.
			release, ok := review.ClaimReview(rec.Repo, rec.PR)
			if !ok {
				t.Fatal("could not claim the PR for the review")
			}
			defer release()
			m2, cmd := launch(newDecideTestModel())
			if cmd != nil {
				t.Error("decision launched while a review held the PR")
			}
			if !strings.Contains(strings.Join(m2.activityLines, "\n"), "auto-post gate still running") {
				t.Errorf("activity = %v, want the review-running warning", m2.activityLines)
			}
		})
	}
}

// Confirming a post at the gate claims the PR for the post and releases it
// when the post finishes; with a review holding the PR, "y" is refused.
func TestDecidePostConfirm_ClaimsPRWhileRunning(t *testing.T) {
	withRealReviewClaims(t)
	const repo, pr = "owner/repo", 55
	var held bool
	restore := stubPostReviewFunc(func(context.Context, review.Record) (int, error) {
		held = review.ReviewInFlight(repo, pr)
		return 0, nil
	})
	defer restore()

	spoolPath := newPostSpoolFixture(t, t.TempDir(), "owner-repo-55.md")
	_, cmd := driveToDecidePostAwaitingConfirmation(t, spoolPath).Update(pressKey('y'))
	if cmd == nil {
		t.Fatal("expected a post command")
	}
	if !review.ReviewInFlight(repo, pr) {
		t.Error("PR not claimed when the post launched")
	}
	_ = drainBatchForType[decidePostCompleteMsg](t, cmd)
	if !held {
		t.Error("claim not held while the post ran")
	}
	if review.ReviewInFlight(repo, pr) {
		t.Error("claim not released after the post finished")
	}

	// A review that claims the PR after the gate opened blocks the post.
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)
	release, ok := review.ClaimReview(repo, pr)
	if !ok {
		t.Fatal("could not claim the PR for the review")
	}
	defer release()
	updated, cmd := m.Update(pressKey('y'))
	if cmd != nil {
		t.Error("post launched while a review held the PR")
	}
	if m2 := updated.(model); !strings.Contains(strings.Join(m2.activityLines, "\n"), "auto-post gate still running") {
		t.Errorf("activity = %v, want the review-running warning", m2.activityLines)
	}
}
