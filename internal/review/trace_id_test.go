package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTraceSpool writes a pending spool with the given front-matter lines and
// returns its path. PR_REVIEW_DIR must already point at root.
func writeTraceSpool(t *testing.T, root, name, extraFront string) string {
	t.Helper()
	spool := filepath.Join(root, "pending", name)
	if err := os.MkdirAll(filepath.Dir(spool), 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nrepo: Bit-Quill/flowdra\npr: 42\nverdict: COMMENT\ndecision:\nreviewed_sha: abc123\n" + extraFront + "---\n\n# Review\n"
	if err := os.WriteFile(spool, []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	return spool
}

// allAuditRecords reads every .jsonl under <root>/audit (recursively).
func allAuditRecords(t *testing.T, root string) []map[string]any {
	t.Helper()
	var out []map[string]any
	err := filepath.WalkDir(filepath.Join(root, "audit"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(p) == ".jsonl" {
			out = append(out, readAuditLines(t, p)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSpoolTraceID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)

	withTrace := writeTraceSpool(t, root, "a.md", "trace_id:  3f2a9c1d0b7e \n")
	legacy := writeTraceSpool(t, root, "b.md", "")

	if got := SpoolTraceID(withTrace); got != "3f2a9c1d0b7e" {
		t.Errorf("SpoolTraceID = %q, want trimmed 3f2a9c1d0b7e", got)
	}
	if got := SpoolTraceID(legacy); got != "" {
		t.Errorf("legacy spool: SpoolTraceID = %q, want empty", got)
	}
	if got := SpoolTraceID(filepath.Join(root, "pending", "missing.md")); got != "" {
		t.Errorf("missing spool: SpoolTraceID = %q, want empty", got)
	}
}

// The gate and the human decision both stamp every record with the spool's
// trace_id, so the cycle joins to the pr_review run that produced the review.
func TestTraceID_GateAndDecisionJoinTheCycle(t *testing.T) {
	withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)

	rec, body, verdict, head, reviewed, diff := gateFixture()
	rec.SpoolPath = writeTraceSpool(t, root, "pr-review-Bit-Quill-flowdra-42.md", "trace_id: 3f2a9c1d0b7e\n")

	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25}
	if _, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg, nil); err != nil {
		t.Fatal(err)
	}
	BeginHumanDecision(rec, "discard").Finish(nil)

	recs := allAuditRecords(t, root)
	pipelines := map[string]bool{}
	for _, r := range recs {
		pipelines[r["pipeline"].(string)] = true
		if r["trace_id"] != "3f2a9c1d0b7e" {
			t.Errorf("%s/%s: trace_id = %v, want 3f2a9c1d0b7e", r["pipeline"], r["step"], r["trace_id"])
		}
	}
	if !pipelines[auditPipeline] || !pipelines[decisionPipeline] {
		t.Fatalf("want records from both %s and %s, got %v", auditPipeline, decisionPipeline, pipelines)
	}
}

// A spool from before trace_id existed leaves trace_id as an explicit null,
// present as a key, rather than inventing a value.
func TestTraceID_LegacySpoolIsNull(t *testing.T) {
	withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)

	rec, body, verdict, head, reviewed, diff := gateFixture()
	rec.SpoolPath = writeTraceSpool(t, root, "pr-review-Bit-Quill-flowdra-42.md", "")

	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25}
	if _, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg, nil); err != nil {
		t.Fatal(err)
	}
	BeginHumanDecision(rec, "post").Finish(nil)

	recs := allAuditRecords(t, root)
	if len(recs) == 0 {
		t.Fatal("no audit records written")
	}
	for _, r := range recs {
		v, present := r["trace_id"]
		if !present {
			t.Errorf("%s/%s: trace_id key missing", r["pipeline"], r["step"])
		} else if v != nil {
			t.Errorf("%s/%s: trace_id = %v, want null", r["pipeline"], r["step"], v)
		}
	}
}

// writeDoneSpool writes the spool under <root>/done/ (where finalize moves
// it) and returns the pending/ path a Record would still carry.
func writeDoneSpool(t *testing.T, root, name, extraFront string) string {
	t.Helper()
	done := filepath.Join(root, "done", name)
	if err := os.MkdirAll(filepath.Dir(done), 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nrepo: Bit-Quill/flowdra\npr: 42\nverdict: COMMENT\ndecision:\nreviewed_sha: abc123\n" + extraFront + "---\n\n# Review\n"
	if err := os.WriteFile(done, []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "pending", name)
}

// Once finalize has moved the spool to done/, the trace_id is still found
// from the pending/ path, by SpoolTraceID and by a human decision.
func TestTraceID_FoundInDoneDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	pending := writeDoneSpool(t, root, "pr-review-Bit-Quill-flowdra-42.md", "trace_id: 3f2a9c1d0b7e\n")
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("pending/ copy must not exist for this test (stat err %v)", err)
	}

	if got := SpoolTraceID(pending); got != "3f2a9c1d0b7e" {
		t.Errorf("SpoolTraceID(pending path, spool in done/) = %q, want 3f2a9c1d0b7e", got)
	}

	BeginHumanDecision(Record{Repo: "Bit-Quill/flowdra", PR: 42, SpoolPath: pending}, "post").Finish(nil)
	recs := allAuditRecords(t, root)
	if len(recs) != 1 {
		t.Fatalf("want 1 decision record, got %d", len(recs))
	}
	if recs[0]["trace_id"] != "3f2a9c1d0b7e" {
		t.Errorf("decision trace_id = %v, want 3f2a9c1d0b7e", recs[0]["trace_id"])
	}
}

func TestNormalizeTraceID(t *testing.T) {
	for in, want := range map[string]string{
		"3f2a9c1d0b7e":          "3f2a9c1d0b7e",
		"  3f2a9c1d0b7e \t":     "3f2a9c1d0b7e",
		"run_ID-2":              "run_ID-2",
		"":                      "",
		"has space":             "",
		"3f2a;rm -rf":           "",
		"../../etc":             "",
		"tráce":                 "",
		strings.Repeat("a", 64): strings.Repeat("a", 64),
		strings.Repeat("a", 65): "",
	} {
		if got := normalizeTraceID(in); got != want {
			t.Errorf("normalizeTraceID(%q) = %q, want %q", in, got, want)
		}
	}
}

// A malformed trace_id in the spool is recorded as null, not stored as-is,
// by both the gate and the human decision.
func TestTraceID_MalformedValueIsNull(t *testing.T) {
	withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)

	rec, body, verdict, head, reviewed, diff := gateFixture()
	rec.SpoolPath = writeTraceSpool(t, root, "pr-review-Bit-Quill-flowdra-42.md", "trace_id: not a trace id; DROP\n")

	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25}
	if _, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg, nil); err != nil {
		t.Fatal(err)
	}
	BeginHumanDecision(rec, "discard").Finish(nil)

	recs := allAuditRecords(t, root)
	if len(recs) == 0 {
		t.Fatal("no audit records written")
	}
	for _, r := range recs {
		if v, present := r["trace_id"]; !present || v != nil {
			t.Errorf("%s/%s: trace_id = %v (present %v), want explicit null", r["pipeline"], r["step"], v, present)
		}
	}
}
