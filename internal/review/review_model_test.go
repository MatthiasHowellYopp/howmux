package review

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/kirocli"
)

// withReviewModel pins the workflow model for one test.
func withReviewModel(t *testing.T, model string) {
	t.Helper()
	orig := ReviewModel()
	SetReviewModel(model)
	t.Cleanup(func() { SetReviewModel(orig) })
}

func TestSetReviewModelTrims(t *testing.T) {
	withReviewModel(t, "  claude-sonnet-4.6 ")
	if got := ReviewModel(); got != "claude-sonnet-4.6" {
		t.Errorf("ReviewModel = %q, want trimmed", got)
	}
}

func TestWithModelArg(t *testing.T) {
	base := []string{"pr_review.py", "--diff-file", "d"}
	if got := withModelArg(append([]string{}, base...), ""); len(got) != len(base) {
		t.Errorf("no model: argv = %v, want unchanged", got)
	}
	got := withModelArg(append([]string{}, base...), "claude-sonnet-4.6")
	if strings.Join(got[len(base):], " ") != "--model claude-sonnet-4.6" {
		t.Errorf("argv = %v, want --model claude-sonnet-4.6 appended", got)
	}
}

func TestAuditorModelFor(t *testing.T) {
	withReviewModel(t, "claude-sonnet-4.6")
	if got := auditorModelFor(AutoPostConfig{AuditorModel: "claude-opus-5"}); got != "claude-opus-5" {
		t.Errorf("auditor_model set: got %q, want it to win", got)
	}
	if got := auditorModelFor(AutoPostConfig{}); got != "claude-sonnet-4.6" {
		t.Errorf("auditor_model unset: got %q, want the review model", got)
	}
	SetReviewModel("")
	if got := auditorModelFor(AutoPostConfig{}); got != "" {
		t.Errorf("neither set: got %q, want empty (kiro-cli default)", got)
	}
}

// runReviewArgv runs RunReview with fake seams and returns the pr_review.py argv.
func runReviewArgv(t *testing.T) []string {
	t.Helper()
	origFetch, origRun, origNow := fetchDiffFunc, runReviewToolFunc, timeNow
	t.Cleanup(func() { fetchDiffFunc, runReviewToolFunc, timeNow = origFetch, origRun, origNow })
	timeNow = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, outputFile string) error {
		return os.WriteFile(outputFile, []byte("fake diff"), 0o644)
	}
	var argv []string
	runReviewToolFunc = func(ctx context.Context, a []string, _ io.Writer) ([]string, error) {
		argv = append([]string{}, a...)
		return []string{writeTestSpoolFixture(t, "pr-review-owner-repo.md")}, nil
	}
	rec := Record{Repo: "owner/repo", PR: 99, URL: "https://github.com/owner/repo/pull/99",
		Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339), ReviewDir: t.TempDir()}
	if err := RunReview(context.Background(), rec, "def456", NewStore(t.TempDir()), io.Discard); err != nil {
		t.Fatal(err)
	}
	return argv
}

func TestRunReview_PassesReviewModel(t *testing.T) {
	withReviewModel(t, "claude-sonnet-4.6")
	argv := strings.Join(runReviewArgv(t), " ")
	if !strings.Contains(argv, "--model claude-sonnet-4.6") {
		t.Errorf("argv %q lacks --model claude-sonnet-4.6", argv)
	}

	SetReviewModel("")
	if argv := strings.Join(runReviewArgv(t), " "); strings.Contains(argv, "--model") {
		t.Errorf("no review model: argv %q should not pass --model", argv)
	}
}

func TestHumanizerUsesReviewModel(t *testing.T) {
	withReviewModel(t, "claude-sonnet-4.6")
	calls := withFakeKiroOneshot(t, map[string]string{"humanizer": "Rewritten summary."}, nil)
	humanizeReviewBody(context.Background(), "A summary that needs humanizing.\n\nVERDICT: COMMENT\n")
	var models []string
	for _, c := range *calls {
		if c.agentName == "humanizer" {
			models = append(models, c.model)
		}
	}
	if len(models) == 0 {
		t.Fatal("humanizer was not called")
	}
	for _, m := range models {
		if m != "claude-sonnet-4.6" {
			t.Errorf("humanizer called with model %q, want claude-sonnet-4.6", m)
		}
	}
}

// The recorded model is what kiro-cli reports; with no report, the model we
// passed. The credit rate is carried through to the audit record.
func TestKiroOneshotMeta_RecordsReportedModelAndRate(t *testing.T) {
	orig := kiroOneshotFunc
	t.Cleanup(func() { kiroOneshotFunc = orig })
	rate := 1.3
	kiroOneshotFunc = func(ctx context.Context, agentName, model, prompt string) (kirocli.Result, error) {
		return kirocli.Result{Text: "OK", Meta: kirocli.Meta{Model: "claude-sonnet-4.6", CreditRate: &rate}}, nil
	}
	res, err := kiroOneshotMeta(context.Background(), "review-auditor", "", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "claude-sonnet-4.6" || res.CreditRate == nil || *res.CreditRate != 1.3 {
		t.Errorf("got model %q rate %v, want claude-sonnet-4.6 / 1.3", res.Model, res.CreditRate)
	}

	kiroOneshotFunc = func(ctx context.Context, agentName, model, prompt string) (kirocli.Result, error) {
		return kirocli.Result{Text: "OK"}, nil
	}
	if res, _ := kiroOneshotMeta(context.Background(), "review-auditor", "claude-opus-5", "p"); res.Model != "claude-opus-5" {
		t.Errorf("no reported model: got %q, want the passed model", res.Model)
	}

	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	a := NewAuditLog("run1", "o/r", 1)
	meta := &OneshotResult{Model: "claude-sonnet-4.6", CreditRate: &rate}
	if err := a.record(auditStep{step: "auditor", status: "pass", agent: "review-auditor", meta: meta}); err != nil {
		t.Fatal(err)
	}
	if err := a.record(auditStep{step: "guardrail", status: "pass"}); err != nil {
		t.Fatal(err)
	}
	lines := readAuditLines(t, filepath.Join(root, "audit", "review_auto_post-o-r-1-run1.jsonl"))
	if lines[0]["credit_rate"] != 1.3 || lines[0]["model"] != "claude-sonnet-4.6" {
		t.Errorf("auditor record: credit_rate %v model %v", lines[0]["credit_rate"], lines[0]["model"])
	}
	if v, present := lines[1]["credit_rate"]; !present || v != nil {
		t.Errorf("deterministic step: credit_rate = %v (present %v), want explicit null", v, present)
	}
}

func TestCheckReviewModel(t *testing.T) {
	orig := listModelsFunc
	t.Cleanup(func() { listModelsFunc = orig })
	listModelsFunc = func(context.Context) ([]string, error) { return []string{"auto", "claude-sonnet-4.6"}, nil }

	if err := CheckReviewModel(context.Background(), ""); err != nil {
		t.Errorf("empty model: %v, want nil", err)
	}
	if err := CheckReviewModel(context.Background(), "claude-sonnet-4.6"); err != nil {
		t.Errorf("offered model: %v, want nil", err)
	}
	err := CheckReviewModel(context.Background(), "calude-sonnet-4.6")
	if err == nil || !strings.Contains(err.Error(), "review.model") {
		t.Errorf("unknown model: err %v, want a review.model error", err)
	}

	listModelsFunc = func(context.Context) ([]string, error) { return nil, errors.New("no kiro-cli") }
	if err := CheckReviewModel(context.Background(), "claude-sonnet-4.6"); !errors.Is(err, ErrModelListUnavailable) {
		t.Errorf("list unavailable: err %v, want ErrModelListUnavailable", err)
	}
}

// With no auditor_model, the gate runs the auditor on the workflow's model.
func TestGate_AuditorFallsBackToReviewModel(t *testing.T) {
	withFakeActions(t)
	withKiroCLIPresent(t)
	withReviewModel(t, "claude-sonnet-4.6")
	calls := withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	t.Setenv("PR_REVIEW_DIR", t.TempDir())

	rec, body, verdict, head, reviewed, diff := gateFixture()
	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25}
	if _, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg, nil); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range *calls {
		if c.agentName == reviewAuditorAgent {
			got = append(got, c.model)
		}
	}
	if len(got) != 1 || got[0] != "claude-sonnet-4.6" {
		t.Errorf("auditor models = %v, want [claude-sonnet-4.6]", got)
	}
}

// Revise runs the consolidator one-shot; it must carry the pinned model.
func TestReviseUsesReviewModel(t *testing.T) {
	withReviewModel(t, "claude-sonnet-4.6")
	spoolPath := writeFakeSpoolFile(t, t.TempDir(), map[string]string{
		"repo": "owner/repo", "pr": "11", "verdict": "COMMENT", "decision": "revise",
	}, "# Old Review\n\nold findings\nVERDICT: COMMENT\n")
	calls := withFakeKiroOneshot(t, map[string]string{
		"review-consolidator": "# Revised Review\n\nVERDICT: COMMENT",
	}, nil)
	if _, err := ReviseReview(context.Background(), recordForSpool("owner/repo", 11, spoolPath), "recheck"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range *calls {
		if c.agentName == "review-consolidator" {
			got = append(got, c.model)
		}
	}
	if len(got) != 1 || got[0] != "claude-sonnet-4.6" {
		t.Errorf("revise consolidator models = %v, want [claude-sonnet-4.6]", got)
	}
}

// Rereview re-runs pr_review.py; its argv must carry --model like RunReview's.
func TestRereviewPassesReviewModel(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"claude-sonnet-4.6", "claude-sonnet-4.6"},
		{"", ""},
	} {
		withReviewModel(t, tc.model)
		spoolPath := writeFakeSpoolFile(t, t.TempDir(), map[string]string{
			"repo": "owner/repo", "pr": "23", "verdict": "COMMENT", "decision": "rereview",
		}, "# Review\n\nVERDICT: COMMENT\n")
		run := withFakeRereviewSeams(t, "head123", "COMMENT", "", nil, nil)
		if _, _, err := RereviewReview(context.Background(), recordForSpool("owner/repo", 23, spoolPath), ""); err != nil {
			t.Fatal(err)
		}
		if got := argValue(run.argv, "--model"); got != tc.want {
			t.Errorf("review model %q: --model = %q, want %q (argv %v)", tc.model, got, tc.want, run.argv)
		}
		if tc.want == "" && hasArg(run.argv, "--model") {
			t.Errorf("no review model: argv %v should not pass --model", run.argv)
		}
	}
}
