package review

import (
	"context"
	"strings"
	"testing"
)

// withFakeActions substitutes the post/discard action seams with recorders so
// gate tests can assert routing without shelling out to gh. Returns pointers to
// call-count ints.
func withFakeActions(t *testing.T) (posts, discards *int) {
	t.Helper()
	origPost := postReviewFunc
	origDiscard := discardReviewFunc
	p, d := 0, 0
	postReviewFunc = func(ctx context.Context, rec Record) (int, error) { p++; return 1, nil }
	discardReviewFunc = func(rec Record) error { d++; return nil }
	t.Cleanup(func() {
		postReviewFunc = origPost
		discardReviewFunc = origDiscard
	})
	return &p, &d
}

// gateFixture returns the inputs for a review that PASSES the deterministic
// guardrail: a COMMENT verdict, one anchorable finding, matching SHA, and a
// diff whose right side includes internal/foo.go:10.
func gateFixture() (rec Record, body, verdict, headSHA, reviewedSHA, diff string) {
	rec = Record{Repo: "Bit-Quill/flowdra", PR: 42, SpoolPath: "/tmp/pending/pr-review-x.md"}
	body = "# Review\n\n- internal/foo.go:10 - low - unused var -> remove it\n\nVERDICT: COMMENT\n"
	verdict = "COMMENT"
	headSHA = "abc123"
	reviewedSHA = "abc123"
	// Minimal unified diff with internal/foo.go new-side lines ~10.
	diff = "--- a/internal/foo.go\n+++ b/internal/foo.go\n@@ -8,3 +8,4 @@\n ctx\n ctx\n+var x int\n ctx\n"
	return
}

func TestGate_DryRun_AlwaysPunches(t *testing.T) {
	posts, discards := withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25, OwnRepoOwners: []string{"Bit-Quill"}}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomeDryRun {
		t.Errorf("Outcome = %q, want dry-run", res.Outcome)
	}
	if res.WouldHaveDone != outcomePosted {
		t.Errorf("WouldHaveDone = %q, want posted", res.WouldHaveDone)
	}
	if *posts != 0 || *discards != 0 {
		t.Errorf("dry-run must not act: posts=%d discards=%d", *posts, *discards)
	}
}

func TestGate_OwnRepos_PostsWhenAllowed(t *testing.T) {
	posts, _ := withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 25, OwnRepoOwners: []string{"Bit-Quill"}}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomePosted {
		t.Errorf("Outcome = %q, want posted", res.Outcome)
	}
	if *posts != 1 {
		t.Errorf("want 1 post, got %d", *posts)
	}
}

// TestGate_OwnRepos_PostsNonCommentVerdicts checks end to end that a clean
// APPROVE or REQUEST_CHANGES review clears the guardrail, reaches the auditor,
// and is posted (it used to be punched by event_legal before the auditor ran).
func TestGate_OwnRepos_PostsNonCommentVerdicts(t *testing.T) {
	for _, v := range []string{"APPROVE", "REQUEST_CHANGES"} {
		t.Run(v, func(t *testing.T) {
			posts, _ := withFakeActions(t)
			withKiroCLIPresent(t)
			withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "NOTES: sound\nVERDICT: POST_OK"}, nil)
			t.Setenv("PR_REVIEW_DIR", t.TempDir())

			rec, body, _, head, reviewed, diff := gateFixture()
			body = strings.Replace(body, "VERDICT: COMMENT", "VERDICT: "+v, 1)
			cfg := AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 25, OwnRepoOwners: []string{"Bit-Quill"}}

			res, err := EvaluateAndMaybePost(context.Background(), rec, body, v, head, reviewed, diff, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Outcome != outcomePosted {
				t.Errorf("Outcome = %q, want posted", res.Outcome)
			}
			if *posts != 1 {
				t.Errorf("want 1 post, got %d", *posts)
			}
		})
	}
}

func TestGate_OwnRepos_PunchesWhenNotAllowed(t *testing.T) {
	posts, _ := withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	// owner Bit-Quill NOT in the allow-list
	cfg := AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 25, OwnRepoOwners: []string{"matthiashowellyopp"}}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomePunted {
		t.Errorf("Outcome = %q, want punch", res.Outcome)
	}
	if res.WouldHaveDone != outcomePosted {
		t.Errorf("WouldHaveDone = %q, want posted", res.WouldHaveDone)
	}
	if *posts != 0 {
		t.Errorf("must not post for a non-allowed owner: posts=%d", *posts)
	}
}

func TestGate_All_DiscardsOnAuditorDiscard(t *testing.T) {
	_, discards := withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: DISCARD"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeAll, MaxFindings: 25}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomeDiscarded {
		t.Errorf("Outcome = %q, want discarded", res.Outcome)
	}
	if *discards != 1 {
		t.Errorf("want 1 discard, got %d", *discards)
	}
}

func TestGate_AuditorNeedsHuman_Punches(t *testing.T) {
	posts, discards := withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: NEEDS_HUMAN"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeAll, MaxFindings: 25}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomePunted {
		t.Errorf("Outcome = %q, want punch", res.Outcome)
	}
	if *posts != 0 || *discards != 0 {
		t.Errorf("NEEDS_HUMAN must not act: posts=%d discards=%d", *posts, *discards)
	}
}

// TestGate_PunchOutBypass is the Stage 4 punch-out bypass test: a review that
// FAILS a deterministic check must be blocked from auto-posting even in the
// most permissive mode (ModeAll) and even if the auditor would say POST_OK.
// The guardrail short-circuits before the auditor and the gate punches. This is
// the evidence that the human-evacuation point cannot be bypassed.
func TestGate_PunchOutBypass(t *testing.T) {
	posts, discards := withFakeActions(t)
	withKiroCLIPresent(t)
	// Auditor would approve — but it must never be consulted, because the
	// deterministic guardrail fails first.
	calls := withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, _, diff := gateFixture()
	// Force a guardrail failure: stale SHA (reviewedSHA != head).
	staleReviewed := "OLDSHA"
	cfg := AutoPostConfig{Mode: ModeAll, MaxFindings: 25, OwnRepoOwners: []string{"Bit-Quill"}}

	res, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, staleReviewed, diff, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != outcomePunted {
		t.Fatalf("bypass attempt succeeded: Outcome = %q, want punch", res.Outcome)
	}
	if *posts != 0 || *discards != 0 {
		t.Errorf("a review failing a deterministic check must not act: posts=%d discards=%d", *posts, *discards)
	}
	// The auditor must not even have been consulted once the guardrail failed.
	if len(*calls) != 0 {
		t.Errorf("auditor was called %d time(s) despite a guardrail failure; it must short-circuit", len(*calls))
	}
}
