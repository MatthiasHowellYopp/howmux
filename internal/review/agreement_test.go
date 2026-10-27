package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeAuditFile writes one .jsonl audit file under root/audit.
func writeAuditFile(t *testing.T, root, name string, lines ...auditLine) {
	t.Helper()
	dir := filepath.Join(root, "audit")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gateRun builds the audit lines of one gate run. would is "post", "discard",
// "punch" (guardrail fail) or "needs_human" (auditor punch); mode is "dry-run"
// or "acted".
func gateRun(t *testing.T, root, runID, pr, ts, sha, would, mode string) {
	t.Helper()
	mk := func(step, status, detail string) auditLine {
		return auditLine{RunID: runID, Pipeline: auditPipeline, Repo: "o/r", PR: pr, Step: step, Status: status, Detail: detail, Timestamp: ts}
	}
	lines := []auditLine{mk("inputs", "pass", formatKV("verdict", "COMMENT", "reviewed_sha", sha, "head_sha", sha))}
	if would == "punch" {
		lines = append(lines, mk("guardrail", "fail", "guardrail failed: sha_fresh"), mk("outcome", "pass", "punch (guardrail failed)"))
	} else {
		lines = append(lines, mk("guardrail", "pass", "all deterministic checks clean"))
		verdict := map[string]string{"post": "POST_OK", "discard": "DISCARD", "needs_human": "NEEDS_HUMAN"}[would]
		lines = append(lines, mk("auditor", "pass", verdict+": NOTES: x"))
		outcome := map[string]string{"post": "posted", "discard": "discarded", "needs_human": "punch"}[would]
		if mode == "dry-run" {
			outcome = "dry-run; would " + outcome
		}
		lines = append(lines, mk("outcome", "pass", outcome))
	}
	writeAuditFile(t, root, "review_auto_post-o-r-"+pr+"-"+runID+".jsonl", lines...)
}

func humanDecision(t *testing.T, root, runID, pr, ts, decision, sha string) {
	t.Helper()
	writeAuditFile(t, root, filepath.Join(decisionSubdir, "review_decision-o-r-"+pr+"-"+runID+".jsonl"), auditLine{
		RunID: runID, Pipeline: decisionPipeline, Repo: "o/r", PR: pr, Step: decisionStep, Status: "pass",
		Detail: formatKV("decision", decision, "verdict", "COMMENT", "reviewed_sha", sha), Timestamp: ts,
	})
}

func rowByRun(t *testing.T, rep AgreementReport, runID string) AgreementRow {
	t.Helper()
	for _, r := range rep.Rows {
		if r.RunID == runID {
			return r
		}
	}
	t.Fatalf("no row for run %s", runID)
	return AgreementRow{}
}

func TestBuildAgreementReport_Classes(t *testing.T) {
	root := t.TempDir()

	gateRun(t, root, "agree", "1", "2026-10-01T10:00:00Z", "s1", "post", "dry-run")
	humanDecision(t, root, "d1", "1", "2026-10-01T11:00:00Z", "post", "s1")

	gateRun(t, root, "perm", "2", "2026-10-01T10:00:00Z", "s2", "post", "dry-run")
	humanDecision(t, root, "d2", "2", "2026-10-01T11:00:00Z", "revise", "s2")

	gateRun(t, root, "strict", "3", "2026-10-01T10:00:00Z", "s3", "discard", "dry-run")
	humanDecision(t, root, "d3", "3", "2026-10-01T11:00:00Z", "post", "s3")

	gateRun(t, root, "punch", "4", "2026-10-01T10:00:00Z", "s4", "punch", "dry-run")
	humanDecision(t, root, "d4", "4", "2026-10-01T11:00:00Z", "post", "s4")

	gateRun(t, root, "nh", "5", "2026-10-01T10:00:00Z", "s5", "needs_human", "dry-run")

	gateRun(t, root, "undecided", "6", "2026-10-01T10:00:00Z", "s6", "post", "dry-run")

	gateRun(t, root, "auto", "7", "2026-10-01T10:00:00Z", "s7", "post", "acted")

	gateRun(t, root, "agree-discard", "8", "2026-10-01T10:00:00Z", "s8", "discard", "dry-run")
	humanDecision(t, root, "d8", "8", "2026-10-01T10:30:00Z", "discard", "s8")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"agree": AgreementAgree, "perm": AgreementPermissive, "strict": AgreementStrict,
		"punch": AgreementPunched, "nh": AgreementPunched, "undecided": AgreementUndecided,
		"auto": AgreementAuto, "agree-discard": AgreementAgree,
	}
	for run, class := range want {
		if got := rowByRun(t, rep, run).Class; got != class {
			t.Errorf("run %s: class = %q, want %q", run, got, class)
		}
	}
	if rep.Graded != 4 {
		t.Errorf("Graded = %d, want 4", rep.Graded)
	}
	if rep.AgreementRate != 0.5 {
		t.Errorf("AgreementRate = %v, want 0.5", rep.AgreementRate)
	}
	if r := rowByRun(t, rep, "punch"); r.HumanDecision != "post" || r.PunchReason == "" {
		t.Errorf("punched row should keep the human decision and reason: %+v", r)
	}
}

func TestBuildAgreementReport_WindowAndSHA(t *testing.T) {
	root := t.TempDir()

	// Two gate runs on PR 1. The only decision comes after the second run,
	// so it belongs to the second run, not the first.
	gateRun(t, root, "first", "1", "2026-10-01T10:00:00Z", "a", "post", "dry-run")
	gateRun(t, root, "second", "1", "2026-10-02T10:00:00Z", "b", "post", "dry-run")
	humanDecision(t, root, "d1", "1", "2026-10-02T11:00:00Z", "post", "b")

	// PR 2: the first decision in the window is for a different review
	// generation and must be skipped in favor of the matching one.
	gateRun(t, root, "sha", "2", "2026-10-01T10:00:00Z", "good", "post", "dry-run")
	humanDecision(t, root, "d2a", "2", "2026-10-01T10:10:00Z", "discard", "other")
	humanDecision(t, root, "d2b", "2", "2026-10-01T10:20:00Z", "post", "good")

	// PR 3: a decision made before the gate run doesn't count.
	gateRun(t, root, "before", "3", "2026-10-01T10:00:00Z", "c", "post", "dry-run")
	humanDecision(t, root, "d3", "3", "2026-10-01T09:00:00Z", "post", "c")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowByRun(t, rep, "first").Class; got != AgreementUndecided {
		t.Errorf("first run class = %q, want undecided", got)
	}
	if got := rowByRun(t, rep, "second").Class; got != AgreementAgree {
		t.Errorf("second run class = %q, want agree", got)
	}
	if r := rowByRun(t, rep, "sha"); r.HumanDecision != "post" || r.Class != AgreementAgree {
		t.Errorf("sha run = %+v, want the matching-sha post decision", r)
	}
	if got := rowByRun(t, rep, "before").Class; got != AgreementUndecided {
		t.Errorf("before run class = %q, want undecided", got)
	}
}

func TestBuildAgreementReport_LegacyDoneFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDone := func(pr, generated, decision string) {
		body := "---\nrepo: o/r\npr: " + pr + "\nverdict: COMMENT\ndecision: " + decision +
			"\ngenerated: " + generated + "\nreviewed_sha:\n---\n\n# Review\n"
		if err := os.WriteFile(filepath.Join(root, "done", "pr-review-o-r-"+pr+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Legacy runs have no inputs step (no SHA): write them without one.
	legacy := func(runID, pr, ts string) {
		writeAuditFile(t, root, "review_auto_post-o-r-"+pr+"-"+runID+".jsonl",
			auditLine{RunID: runID, Pipeline: auditPipeline, Repo: "o/r", PR: pr, Step: "guardrail", Status: "pass", Timestamp: ts},
			auditLine{RunID: runID, Pipeline: auditPipeline, Repo: "o/r", PR: pr, Step: "auditor", Status: "pass", Detail: "POST_OK: POST_OK", Timestamp: ts},
			auditLine{RunID: runID, Pipeline: auditPipeline, Repo: "o/r", PR: pr, Step: "outcome", Status: "pass", Detail: "dry-run; would posted", Timestamp: ts},
		)
	}

	// PR 1: two legacy runs; done/ (generated just before run 2, with a
	// timezone offset) applies to run 2 only.
	legacy("old", "1", "2026-10-01T10:00:00Z")
	legacy("new", "1", "2026-10-02T10:00:01Z")
	writeDone("1", "2026-10-02T06:00:00-04:00", "post")

	// PR 2: the review was regenerated after the gate ran, so done/ is a
	// different generation and must not be matched.
	legacy("regen", "2", "2026-10-01T10:00:00Z")
	writeDone("2", "2026-10-01T12:00:00Z", "post")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByRun(t, rep, "new"); r.Class != AgreementAgree || r.DecisionSource != "done" {
		t.Errorf("new run = %+v, want agree from done/", r)
	}
	if got := rowByRun(t, rep, "old").Class; got != AgreementUndecided {
		t.Errorf("old run class = %q, want undecided", got)
	}
	if got := rowByRun(t, rep, "regen").Class; got != AgreementUndecided {
		t.Errorf("regen run class = %q, want undecided", got)
	}
}

func TestBuildAgreementReport_Empty(t *testing.T) {
	rep, err := BuildAgreementReport(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 0 || rep.AgreementRate != -1 {
		t.Errorf("empty report = %+v", rep)
	}
	var buf bytes.Buffer
	WriteAgreementText(&buf, rep)
	if !strings.Contains(buf.String(), "No auto-post gate runs") {
		t.Errorf("empty text = %q", buf.String())
	}
}

func TestWriteAgreementText(t *testing.T) {
	root := t.TempDir()
	gateRun(t, root, "agree", "1", "2026-10-01T10:00:00Z", "s1", "post", "dry-run")
	humanDecision(t, root, "d1", "1", "2026-10-01T11:00:00Z", "post", "s1")
	gateRun(t, root, "perm", "2", "2026-10-01T10:00:00Z", "s2", "post", "dry-run")
	humanDecision(t, root, "d2", "2", "2026-10-01T11:00:00Z", "discard", "s2")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	WriteAgreementText(&buf, rep)
	out := buf.String()
	for _, want := range []string{"o/r#1", "o/r#2", "Agreement: 50% (1 of 2 graded runs)", "too_permissive"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

// TestAgreement_EndToEnd runs a real dry-run gate pass and a real human
// decision record, then checks the report pairs them.
func TestAgreement_EndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	withFakeActions(t)
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "NOTES: fine\nVERDICT: POST_OK"}, nil)

	rec, body, verdict, head, reviewed, diff := gateFixture()
	spool := filepath.Join(root, "pending", "pr-review-Bit-Quill-flowdra-42.md")
	if err := os.MkdirAll(filepath.Dir(spool), 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nrepo: Bit-Quill/flowdra\npr: 42\nverdict: COMMENT\ndecision:\nreviewed_sha: " + reviewed + "\n---\n\n"
	if err := os.WriteFile(spool, []byte(front+body), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.SpoolPath = spool

	cfg := AutoPostConfig{Mode: ModeDryRun, MaxFindings: 25}
	if _, err := EvaluateAndMaybePost(context.Background(), rec, body, verdict, head, reviewed, diff, cfg); err != nil {
		t.Fatal(err)
	}
	BeginHumanDecision(rec, "post").Finish(nil)

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	r := rep.Rows[0]
	if r.Class != AgreementAgree || r.DecisionSource != "audit" || r.ReviewedSHA != reviewed || r.Verdict != "COMMENT" {
		t.Errorf("row = %+v, want agree from audit with sha %q", r, reviewed)
	}
}

func TestHumanDecision_RecordsSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	spool := filepath.Join(root, "pending", "pr-review-o-r-9.md")
	if err := os.MkdirAll(filepath.Dir(spool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spool, []byte("---\nverdict: approve\nreviewed_sha: abc\n---\n\n# R\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := Record{Repo: "o/r", PR: 9, SpoolPath: spool}

	hd := BeginHumanDecision(rec, "discard")
	// The action moves the file; the snapshot must already be taken.
	if err := os.Remove(spool); err != nil {
		t.Fatal(err)
	}
	hd.Finish(errors.New("gh exploded"))

	files, _ := filepath.Glob(filepath.Join(root, "audit", decisionSubdir, decisionPipeline+"-o-r-9-*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("decision files = %v, want 1", files)
	}
	lines := readAuditLines(t, files[0])
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	l := lines[0]
	if l["pipeline"] != decisionPipeline || l["step"] != decisionStep || l["status"] != "error" {
		t.Errorf("record = %v", l)
	}
	detail, _ := l["detail"].(string)
	kv := parseKV(detail)
	if kv["decision"] != "discard" || kv["verdict"] != "APPROVE" || kv["reviewed_sha"] != "abc" {
		t.Errorf("detail kv = %v (detail %q)", kv, detail)
	}
	if !strings.Contains(detail, "gh exploded") {
		t.Errorf("detail %q should carry the action error", detail)
	}
}

func TestHumanDecision_NilSafeAndMissingSpool(t *testing.T) {
	var hd *HumanDecision
	hd.Finish(nil) // must not panic

	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	rec := Record{Repo: "o/r", PR: 3, SpoolPath: filepath.Join(root, "pending", "missing.md"), LastReviewedSHA: "fallback"}
	BeginHumanDecision(rec, "post").Finish(nil)
	files, _ := filepath.Glob(filepath.Join(root, "audit", decisionSubdir, decisionPipeline+"-*.jsonl"))
	if top, _ := filepath.Glob(filepath.Join(root, "audit", "*.jsonl")); len(top) != 0 {
		t.Errorf("decision records must stay out of the top-level audit dir (e2e_report.py reads it): %v", top)
	}
	if len(files) != 1 {
		t.Fatalf("decision files = %v, want 1", files)
	}
	detail, _ := readAuditLines(t, files[0])[0]["detail"].(string)
	if kv := parseKV(detail); kv["reviewed_sha"] != "fallback" || kv["verdict"] != "" {
		t.Errorf("kv = %v, want reviewed_sha from the record and empty verdict", kv)
	}
}

func TestFormatParseKV(t *testing.T) {
	s := formatKV("a", "1", "b", "", "c", "x")
	if s != "a=1 b=- c=x" {
		t.Errorf("formatKV = %q", s)
	}
	kv := parseKV(s + decisionErrorSep + "oops c=y")
	if kv["a"] != "1" || kv["b"] != "" || kv["c"] != "x" {
		t.Errorf("parseKV = %v", kv)
	}
}

func TestBuildAgreementReport_IgnoresFailedDecisions(t *testing.T) {
	root := t.TempDir()
	// Gate would post; a rereview attempt fails; the human then posts.
	gateRun(t, root, "run", "1", "2026-10-01T10:00:00Z", "s", "post", "dry-run")
	writeAuditFile(t, root, filepath.Join(decisionSubdir, "review_decision-o-r-1-fail.jsonl"), auditLine{
		RunID: "fail", Pipeline: decisionPipeline, Repo: "o/r", PR: "1", Step: decisionStep, Status: "error",
		Detail:    formatKV("decision", "rereview", "verdict", "COMMENT", "reviewed_sha", "s") + decisionErrorSep + "kiro timed out",
		Timestamp: "2026-10-01T10:05:00Z",
	})
	humanDecision(t, root, "ok", "1", "2026-10-01T10:10:00Z", "post", "s")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByRun(t, rep, "run"); r.HumanDecision != "post" || r.Class != AgreementAgree {
		t.Errorf("row = %+v, want the successful post (agree), not the failed rereview", r)
	}
}

func TestBuildAgreementReport_NoDoneFallbackForNewRuns(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A done/ file from an earlier generation, with no reviewed_sha.
	stale := "---\nrepo: o/r\npr: 1\nverdict: COMMENT\ndecision: discard\ngenerated: 2026-09-01T10:00:00Z\nreviewed_sha:\n---\n"
	if err := os.WriteFile(filepath.Join(root, "done", "pr-review-o-r-1.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	// A new-format run (has an inputs step) with no audited decision yet.
	gateRun(t, root, "new", "1", "2026-10-01T10:00:00Z", "s", "post", "dry-run")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByRun(t, rep, "new"); r.Class != AgreementUndecided || r.DecisionSource != "" {
		t.Errorf("row = %+v, want undecided with no done/ fallback", r)
	}
}

func TestBuildAgreementReport_WindowEdges(t *testing.T) {
	root := t.TempDir()
	// Decision in the same second as the run belongs to that run.
	gateRun(t, root, "same", "1", "2026-10-01T10:00:00Z", "a", "post", "dry-run")
	humanDecision(t, root, "d1", "1", "2026-10-01T10:00:00Z", "post", "")

	// Decision exactly at the next run's start belongs to the next run.
	gateRun(t, root, "e1", "2", "2026-10-01T10:00:00Z", "", "post", "dry-run")
	gateRun(t, root, "e2", "2", "2026-10-01T11:00:00Z", "", "discard", "dry-run")
	humanDecision(t, root, "d2", "2", "2026-10-01T11:00:00Z", "discard", "")

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowByRun(t, rep, "same").Class; got != AgreementAgree {
		t.Errorf("same-second run class = %q, want agree", got)
	}
	if got := rowByRun(t, rep, "e1").Class; got != AgreementUndecided {
		t.Errorf("e1 class = %q, want undecided (decision is at e2's start)", got)
	}
	if got := rowByRun(t, rep, "e2").Class; got != AgreementAgree {
		t.Errorf("e2 class = %q, want agree", got)
	}
}

func TestBuildAgreementReport_SamePRNumberDifferentRepos(t *testing.T) {
	root := t.TempDir()
	gateRun(t, root, "or", "5", "2026-10-01T10:00:00Z", "", "post", "dry-run")
	// A decision on x/y#5 must not pair with o/r#5.
	writeAuditFile(t, root, filepath.Join(decisionSubdir, "review_decision-x-y-5-d.jsonl"), auditLine{
		RunID: "d", Pipeline: decisionPipeline, Repo: "x/y", PR: "5", Step: decisionStep, Status: "pass",
		Detail: formatKV("decision", "post"), Timestamp: "2026-10-01T11:00:00Z",
	})

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowByRun(t, rep, "or").Class; got != AgreementUndecided {
		t.Errorf("o/r#5 class = %q, want undecided", got)
	}
}

func TestBuildAgreementReport_MalformedAndIncomplete(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	good, _ := json.Marshal(auditLine{RunID: "inc", Pipeline: auditPipeline, Repo: "o/r", PR: "1", Step: "inputs", Status: "pass", Timestamp: "2026-10-01T10:00:00Z"})
	data := "not json\n{\"run_id\":\n" + string(good) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "review_auto_post-o-r-1-inc.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := BuildAgreementReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (malformed lines skipped)", len(rep.Rows))
	}
	if got := rep.Rows[0].Class; got != AgreementIncomplete {
		t.Errorf("class = %q, want incomplete (no guardrail/auditor result)", got)
	}
}

func TestHumanDecision_StampedAtBegin(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	orig := auditTimeNow
	t.Cleanup(func() { auditTimeNow = orig })

	begin := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	auditTimeNow = func() time.Time { return begin }
	hd := BeginHumanDecision(Record{Repo: "o/r", PR: 1}, "rereview")
	auditTimeNow = func() time.Time { return begin.Add(4 * time.Minute) }
	hd.Finish(nil)

	files, _ := filepath.Glob(filepath.Join(root, "audit", decisionSubdir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("files = %v, want 1", files)
	}
	l := readAuditLines(t, files[0])[0]
	if l["timestamp"] != "2026-10-01T10:00:00Z" {
		t.Errorf("timestamp = %v, want the Begin time", l["timestamp"])
	}
	if l["duration_ms"] != float64(240000) {
		t.Errorf("duration_ms = %v, want 240000", l["duration_ms"])
	}
}
