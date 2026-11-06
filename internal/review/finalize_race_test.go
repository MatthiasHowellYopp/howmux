package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two finalizers on one review (the auto-post gate of a duplicate review run,
// or the gate and a human) must not both post it. A real post takes seconds
// between the "not yet in done/" check and the archive; the slow fake gh
// reproduces that window.
func TestPostReview_ConcurrentCallsPostOnce(t *testing.T) {
	withFakeDecisionScript(t)
	os.Setenv("HOWMUX_NO_HUMANIZE", "1")
	defer os.Unsetenv("HOWMUX_NO_HUMANIZE")
	withFakeFileComments(t)

	spoolPath := writeFakeSpoolFile(t, t.TempDir(), map[string]string{
		"repo": "owner/repo", "pr": "147", "verdict": "COMMENT", "decision": "",
	}, "# Review\n\n## Summary\n\nLooks fine.\n\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 147, spoolPath)

	orig := postReviewCommandFunc
	t.Cleanup(func() { postReviewCommandFunc = orig })
	var posts int32
	postReviewCommandFunc = func(ctx context.Context, payloadFile, repo string, pr int) ([]byte, error) {
		atomic.AddInt32(&posts, 1)
		time.Sleep(200 * time.Millisecond)
		return []byte("{}"), nil
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = PostReview(context.Background(), rec)
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&posts); got != 1 {
		t.Fatalf("review posted %d times, want exactly 1 (errs: %v)", got, errs)
	}
}

// The loser of the race is refused with a guard error, not a failure.
func TestPostReview_ConcurrentLoserIsGuardStop(t *testing.T) {
	withFakeDecisionScript(t)
	os.Setenv("HOWMUX_NO_HUMANIZE", "1")
	defer os.Unsetenv("HOWMUX_NO_HUMANIZE")
	withFakeFileComments(t)
	spoolPath := writeFakeSpoolFile(t, t.TempDir(), map[string]string{
		"repo": "owner/repo", "pr": "148", "verdict": "COMMENT", "decision": "",
	}, "# Review\n\n## Summary\n\nLooks fine.\n\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 148, spoolPath)

	orig := postReviewCommandFunc
	t.Cleanup(func() { postReviewCommandFunc = orig })
	postReviewCommandFunc = func(ctx context.Context, payloadFile, repo string, pr int) ([]byte, error) {
		time.Sleep(200 * time.Millisecond)
		return []byte("{}"), nil
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = PostReview(context.Background(), rec) }(i)
	}
	wg.Wait()

	var ok, guarded int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrReviewBusy) || errors.Is(err, ErrAlreadyFinalized):
			guarded++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || guarded != 1 {
		t.Errorf("got %d successes and %d guard stops, want 1 and 1 (errs: %v)", ok, guarded, errs)
	}

	// After the winner archived the review, a late post is ErrAlreadyFinalized.
	if _, err := PostReview(context.Background(), rec); !errors.Is(err, ErrAlreadyFinalized) {
		t.Errorf("late post: err = %v, want ErrAlreadyFinalized", err)
	}
}

// A post while a revise holds the review is refused rather than racing it.
func TestPostReview_RefusedWhileReviseRuns(t *testing.T) {
	withFakeDecisionScript(t)
	spoolPath := writeFakeSpoolFile(t, t.TempDir(), map[string]string{
		"repo": "owner/repo", "pr": "149", "verdict": "COMMENT", "decision": "",
	}, "# Review\n\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 149, spoolPath)
	unlock, err := lockFinalize(spoolPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := PostReview(context.Background(), rec); !errors.Is(err, ErrReviewBusy) {
		t.Errorf("post during another action: err = %v, want ErrReviewBusy", err)
	}
	if err := DiscardReview(rec); !errors.Is(err, ErrReviewBusy) {
		t.Errorf("discard during another action: err = %v, want ErrReviewBusy", err)
	}
	if _, err := ReviseReview(context.Background(), rec, ""); !errors.Is(err, ErrReviewBusy) {
		t.Errorf("revise during another action: err = %v, want ErrReviewBusy", err)
	}
	if _, _, err := RereviewReview(context.Background(), rec, ""); !errors.Is(err, ErrReviewBusy) {
		t.Errorf("rereview during another action: err = %v, want ErrReviewBusy", err)
	}
}

// A refused action (already finalized, or busy) is recorded as "fail", a guard
// stop; a real failure stays "error". e2e_report counts only "error" as a
// broken run.
func TestHumanDecision_GuardStopIsFail(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"already finalized", fmt.Errorf("%w; its decision can no longer be changed: x", ErrAlreadyFinalized), "fail"},
		{"busy", fmt.Errorf("post o/r#1: %w", ErrReviewBusy), "fail"},
		{"real failure", errors.New("gh exploded"), "error"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PR_REVIEW_DIR", root)
			pr := 20 + i
			BeginHumanDecision(Record{Repo: "o/r", PR: pr}, "post").Finish(tc.err)
			files, _ := filepath.Glob(filepath.Join(root, "audit", decisionSubdir, fmt.Sprintf("%s-o-r-%d-*.jsonl", decisionPipeline, pr)))
			if len(files) != 1 {
				t.Fatalf("decision files = %v, want 1", files)
			}
			if got := readAuditLines(t, files[0])[0]["status"]; got != tc.want {
				t.Errorf("status = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestOutcomeErrStatus(t *testing.T) {
	if got := outcomeErrStatus(fmt.Errorf("x: %w", ErrAlreadyFinalized)); got != "fail" {
		t.Errorf("already finalized: %q, want fail", got)
	}
	if got := outcomeErrStatus(errors.New("gh 500")); got != "error" {
		t.Errorf("real failure: %q, want error", got)
	}
}

// The "already finalized" texts are unchanged (callers and the TUI match
// on them); only the sentinel was added.
func TestAlreadyFinalizedMessagesUnchanged(t *testing.T) {
	base := t.TempDir()
	spool := writeFakeSpoolFile(t, base, map[string]string{"repo": "o/r", "pr": "30", "verdict": "COMMENT"}, "# R\n\nVERDICT: COMMENT\n")
	done := filepath.Join(base, "done", filepath.Base(spool))
	if err := os.MkdirAll(filepath.Dir(done), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(spool, done); err != nil {
		t.Fatal(err)
	}
	err := NewDecisionWriter().SetDecision(spool, "post")
	if !errors.Is(err, ErrAlreadyFinalized) ||
		!strings.HasPrefix(err.Error(), "review already finalized (archived to done/); its decision can no longer be changed: ") {
		t.Errorf("SetDecision on done/ review: %v", err)
	}

	// Revise and rereview refusals must wrap the sentinel too, or they are
	// logged as "error" again.
	rec := recordForSpool("o/r", 30, spool)
	_, err = ReviseReview(context.Background(), rec, "")
	if !errors.Is(err, ErrAlreadyFinalized) ||
		!strings.HasPrefix(err.Error(), "review already finalized (archived to done/); cannot revise: ") {
		t.Errorf("ReviseReview on done/ review: %v", err)
	}
	_, _, err = RereviewReview(context.Background(), rec, "")
	if !errors.Is(err, ErrAlreadyFinalized) ||
		!strings.HasPrefix(err.Error(), "review already finalized (archived to done/); cannot rereview: ") {
		t.Errorf("RereviewReview on done/ review: %v", err)
	}
}

// When the gate's post is refused because the review was already finalized
// (howmux#147, flowdra#124), the outcome step is a guard stop, not an error.
func TestGate_PostRefusedAsFinalizedIsFail(t *testing.T) {
	withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	postReviewFunc = func(ctx context.Context, rec Record) (int, error) {
		return 0, fmt.Errorf("%w; its decision can no longer be changed: x", ErrAlreadyFinalized)
	}

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 25, OwnRepoOwners: []string{"Bit-Quill"}}
	_, _ = EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg, nil)

	files, _ := filepath.Glob(filepath.Join(root, "audit", "review_auto_post-*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("gate audit files = %v, want 1", files)
	}
	var outcome map[string]any
	for _, l := range readAuditLines(t, files[0]) {
		if l["step"] == "outcome" {
			outcome = l
		}
	}
	if outcome == nil || outcome["status"] != "fail" {
		t.Errorf("outcome record = %v, want status fail", outcome)
	}
}
