package review

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoPostConfig_ActsOn(t *testing.T) {
	tests := []struct {
		name string
		cfg  AutoPostConfig
		repo string
		want bool
	}{
		{"zero value", AutoPostConfig{}, "owner/repo", false},
		{"dry-run", AutoPostConfig{Mode: ModeDryRun, OwnRepoOwners: []string{"owner"}}, "owner/repo", false},
		{"all", AutoPostConfig{Mode: ModeAll}, "anyone/repo", true},
		{"own-repos allowed", AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"Owner"}}, "owner/repo", true},
		{"own-repos not allowed", AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"other"}}, "owner/repo", false},
		{"own-repos empty list", AutoPostConfig{Mode: ModeOwnRepos}, "owner/repo", false},
		{"own-repos malformed repo", AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"owner"}}, "owner", false},
		{"unknown mode", AutoPostConfig{Mode: "bogus"}, "owner/repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ActsOn(tt.repo); got != tt.want {
				t.Errorf("ActsOn(%q) = %v, want %v", tt.repo, got, tt.want)
			}
		})
	}
}

func TestRunReview_IssueScopeFromConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *AutoPostConfig
		want string
	}{
		{"unset → related", nil, "related"},
		{"dry-run → related", &AutoPostConfig{Mode: ModeDryRun}, "related"},
		{"own-repos allowed → all", &AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"owner"}}, "all"},
		{"own-repos other owner → related", &AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"else"}}, "related"},
		{"all → all", &AutoPostConfig{Mode: ModeAll}, "all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withScopeConfig(t, tt.cfg)
			origFetch, origTool, origNow, origNotify := fetchDiffFunc, runReviewToolFunc, timeNow, notifyFunc
			t.Cleanup(func() {
				fetchDiffFunc, runReviewToolFunc, timeNow, notifyFunc = origFetch, origTool, origNow, origNotify
			})
			timeNow = func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
			notifyFunc = func(title, msg string) error { return nil }
			fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, out string) error {
				return os.WriteFile(out, []byte("diff"), 0o644)
			}
			var argv []string
			runReviewToolFunc = func(ctx context.Context, a []string, _ io.Writer) ([]string, error) {
				argv = append([]string{}, a...)
				return []string{writeTestSpoolFixture(t, "pr-review-owner-repo-7.md")}, nil
			}

			rec := Record{Repo: "owner/repo", PR: 7, URL: "https://github.com/owner/repo/pull/7",
				Status: StatusWatching, EnrolledAt: "2025-01-01T00:00:00Z"}
			if err := RunReview(context.Background(), rec, "sha", NewStore(t.TempDir()), io.Discard); err != nil {
				t.Fatalf("RunReview: %v", err)
			}
			if got := argValue(argv, "--issues"); got != tt.want {
				t.Errorf("--issues = %q, want %q (argv %v)", got, tt.want, argv)
			}
		})
	}
}

func TestSetReviewScopeConfig_StoresCopy(t *testing.T) {
	withScopeConfig(t, nil)
	cfg := AutoPostConfig{Mode: ModeOwnRepos, OwnRepoOwners: []string{"owner"}}
	SetReviewScopeConfig(cfg)
	cfg.Mode = ModeDryRun // mutating the caller's value must not change the stored config
	if got := issueScopeFor("owner/repo"); got != "all" {
		t.Errorf("issueScopeFor = %q, want all", got)
	}
}

func TestLoadPriorFeedback(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"repo":"o/n","pr":145,"issue_scope":"all",
		"threads":[{"path":"a.go","line":12,"is_resolved":false,"is_outdated":false,"comments":[{"author":"x","body":"y"}]},
		           {"path":"b.go","line":null,"is_resolved":true,"is_outdated":false,"comments":[]}],
		"issues":[{"number":1,"title":"t","state":"OPEN","labels":["bug"],"url":"u","body":"b","linked":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := loadPriorFeedback(map[string]string{}); got != nil {
		t.Errorf("absent key: got %+v, want nil", got)
	}
	if got := loadPriorFeedback(map[string]string{"prior_feedback": "  "}); got != nil {
		t.Errorf("blank key: got %+v, want nil", got)
	}

	lp := loadPriorFeedback(map[string]string{"prior_feedback": good})
	if lp == nil || lp.Err != nil || lp.Data == nil {
		t.Fatalf("good file: %+v", lp)
	}
	if lp.Data.PR != 145 || len(lp.Data.Threads) != 2 || lp.Data.Threads[0].Line == nil || *lp.Data.Threads[0].Line != 12 ||
		lp.Data.Threads[1].Line != nil || !lp.Data.Issues[0].Linked {
		t.Errorf("parsed = %+v", lp.Data)
	}
	if len(lp.Raw) == 0 {
		t.Error("Raw not retained")
	}

	for _, p := range []string{bad, filepath.Join(dir, "missing.json")} {
		lp := loadPriorFeedback(map[string]string{"prior_feedback": p})
		if lp == nil || lp.Err == nil || lp.Data != nil {
			t.Errorf("%s: got %+v, want Err set", p, lp)
		}
	}
}

func TestBuildAuditorPrompt_PriorFeedback(t *testing.T) {
	without := buildAuditorPrompt("o/r", 1, "COMMENT", "body", "diff", nil)
	if strings.Contains(without, "PRIOR FEEDBACK") {
		t.Error("prompt has a prior-feedback block with no prior feedback")
	}
	if blank := buildAuditorPrompt("o/r", 1, "COMMENT", "body", "diff", []byte("  \n")); strings.Contains(blank, "PRIOR FEEDBACK") {
		t.Error("prompt has a prior-feedback block for blank prior feedback")
	}

	with := buildAuditorPrompt("o/r", 1, "COMMENT", "body", "diff", []byte(`{"threads":[]}`))
	if !strings.Contains(with, "--- BEGIN PRIOR FEEDBACK ---\n{\"threads\":[]}\n--- END PRIOR FEEDBACK ---") {
		t.Errorf("prior block missing or malformed:\n%s", with)
	}
	if strings.Contains(with, "…[truncated]") {
		t.Error("small prior feedback marked truncated")
	}
	if !strings.HasSuffix(with, "VERDICT: <POST_OK|NEEDS_HUMAN|DISCARD>") {
		t.Error("output contract no longer ends the prompt")
	}

	// Multi-byte runes straddling the cap must not be split.
	big := []byte(strings.Repeat("é", maxPriorFeedbackPromptBytes)) // 2 bytes each
	trunc := buildAuditorPrompt("o/r", 1, "COMMENT", "body", "diff", big)
	start := strings.Index(trunc, "--- BEGIN PRIOR FEEDBACK ---\n") + len("--- BEGIN PRIOR FEEDBACK ---\n")
	end := strings.Index(trunc, "\n…[truncated]\n--- END PRIOR FEEDBACK ---")
	if end < 0 {
		t.Fatal("truncated prior feedback lacks the …[truncated] note before the end marker")
	}
	payload := trunc[start:end]
	if len(payload) > maxPriorFeedbackPromptBytes {
		t.Errorf("payload %d bytes, cap %d", len(payload), maxPriorFeedbackPromptBytes)
	}
	if strings.ContainsRune(payload, '\uFFFD') || len(payload)%2 != 0 {
		t.Error("truncation split a multi-byte rune")
	}
}

// gateSpoolFixture writes a pending spool (with optional prior_feedback key)
// whose body passes every other guardrail check, stores a record pointing at
// it, and fakes the diff fetch. Returns the record and store.
func gateSpoolFixture(t *testing.T, priorKey string) (Record, StoreInterface) {
	t.Helper()
	base := t.TempDir()
	fm := map[string]string{"repo": "Bit-Quill/flowdra", "pr": "42", "verdict": "COMMENT", "reviewed_sha": "abc123", "language": "go"}
	if priorKey != "" {
		fm["prior_feedback"] = priorKey
	}
	body := "# Review\n\n## Summary\nTidies foo.\n\n## Findings\n- internal/foo.go:10 - low - unused var -> remove it\n\n" +
		"## Reviewer notes\nAlready raised (dropped): internal/foo.go:11 - see alice's thread\n\nVERDICT: COMMENT\n"
	spool := writeFakeSpoolFile(t, base, fm, body)

	store := NewStore(t.TempDir())
	rec := Record{Repo: "Bit-Quill/flowdra", PR: 42, URL: "https://github.com/Bit-Quill/flowdra/pull/42",
		Status: StatusReviewed, EnrolledAt: "2025-01-01T00:00:00Z", SpoolPath: spool, LastReviewedSHA: "abc123"}
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}

	orig := fetchDiffFunc
	t.Cleanup(func() { fetchDiffFunc = orig })
	fetchDiffFunc = func(ctx context.Context, owner, repo string, pr int, out string) error {
		_, _, _, _, _, diff := gateFixture()
		return os.WriteFile(out, []byte(diff), 0o644)
	}
	return rec, store
}

func TestRunAutoPostGate_AuditorSeesNotesAndPriorFeedback(t *testing.T) {
	t.Setenv("PR_REVIEW_DIR", t.TempDir())
	withFakeActions(t)
	withKiroCLIPresent(t)
	calls := withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)

	prior := filepath.Join(t.TempDir(), "prior.json")
	if err := os.WriteFile(prior, []byte(`{"repo":"Bit-Quill/flowdra","pr":42,"threads":[{"path":"internal/foo.go","line":11,"is_resolved":false,"is_outdated":false,"comments":[{"author":"alice","body":"x"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, store := gateSpoolFixture(t, prior)

	res, err := RunAutoPostGate(context.Background(), rec, "abc123", store, AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25})
	if err != nil {
		t.Fatalf("RunAutoPostGate: %v", err)
	}
	if res.WouldHaveDone != outcomePosted {
		t.Fatalf("WouldHaveDone = %q (%s), want posted", res.WouldHaveDone, res.Reason)
	}
	if len(*calls) != 1 {
		t.Fatalf("auditor calls = %d, want 1", len(*calls))
	}
	p := (*calls)[0].prompt
	if !strings.Contains(p, "## Reviewer notes\nAlready raised (dropped)") {
		t.Error("auditor prompt is missing the spool's Reviewer notes section")
	}
	if !strings.Contains(p, "--- BEGIN PRIOR FEEDBACK ---") || !strings.Contains(p, `"author":"alice"`) {
		t.Error("auditor prompt is missing the prior-feedback JSON")
	}
}

func TestRunAutoPostGate_PriorFeedback(t *testing.T) {
	dir := t.TempDir()
	openThread := filepath.Join(dir, "open.json")
	if err := os.WriteFile(openThread, []byte(`{"threads":[{"path":"internal/foo.go","line":10,"is_resolved":false,"is_outdated":false,"comments":[]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		priorKey  string
		wantPunch bool
		wantIn    string
	}{
		{"absent key: no prior block, guardrail clean", "", false, ""},
		{"missing file fails closed", filepath.Join(dir, "missing.json"), true, "not_already_raised"},
		{"open thread at finding punches", openThread, true, "internal/foo.go:10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PR_REVIEW_DIR", t.TempDir())
			withFakeActions(t)
			withKiroCLIPresent(t)
			calls := withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
			rec, store := gateSpoolFixture(t, tt.priorKey)

			res, err := RunAutoPostGate(context.Background(), rec, "abc123", store, AutoPostConfig{Mode: ModeAll, MaxFindings: 25})
			if err != nil {
				t.Fatalf("RunAutoPostGate: %v", err)
			}
			if tt.wantPunch {
				if res.Outcome != outcomePunted || !strings.Contains(res.Reason, tt.wantIn) {
					t.Errorf("result = %+v, want punch mentioning %q", res, tt.wantIn)
				}
				if len(*calls) != 0 {
					t.Error("auditor ran despite a guardrail failure")
				}
				return
			}
			if res.Outcome != outcomePosted {
				t.Errorf("result = %+v, want posted", res)
			}
			if len(*calls) != 1 || strings.Contains((*calls)[0].prompt, "PRIOR FEEDBACK") {
				t.Error("expected one auditor call with no prior-feedback block")
			}
		})
	}
}
