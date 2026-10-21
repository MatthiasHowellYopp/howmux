package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readAuditLines reads a .jsonl file into a slice of generic maps so tests can
// assert on exact field presence and null-ness (not just typed values).
func readAuditLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	var out []map[string]any
	for _, line := range splitNonEmptyLines(string(data)) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal audit line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range splitLines(s) {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestAuditLog_WritesSchema(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)

	a := NewAuditLog("run123", "Bit-Quill/flowdra", 42)

	// deterministic step: no agent, no meta
	if err := a.record(auditStep{step: "guardrail", status: "pass", detail: "all checks clean"}); err != nil {
		t.Fatalf("record guardrail: %v", err)
	}
	// agent step: with meta supplying model + duration
	meta := &OneshotResult{Model: "claude-sonnet", DurationMs: 1234, Status: "pass"}
	if err := a.record(auditStep{step: "auditor", status: "pass", agent: "review-auditor", detail: "POST_OK", meta: meta}); err != nil {
		t.Fatalf("record auditor: %v", err)
	}

	path := filepath.Join(root, "audit", "review_auto_post-Bit-Quill-flowdra-42-run123.jsonl")
	lines := readAuditLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d audit lines, want 2", len(lines))
	}

	g := lines[0]
	if g["run_id"] != "run123" || g["pipeline"] != "review_auto_post" || g["repo"] != "Bit-Quill/flowdra" || g["pr"] != "42" {
		t.Errorf("guardrail header fields wrong: %+v", g)
	}
	if g["step"] != "guardrail" || g["status"] != "pass" {
		t.Errorf("guardrail step/status wrong: %+v", g)
	}
	// deterministic step → agent and model null
	if g["agent"] != nil {
		t.Errorf("guardrail agent = %v, want null", g["agent"])
	}
	if g["model"] != nil {
		t.Errorf("guardrail model = %v, want null", g["model"])
	}
	// tokens/cost ALWAYS null; must be present as keys
	for _, k := range []string{"tokens", "cost", "turn_duration_ms", "context_usage_pct", "iteration"} {
		v, present := g[k]
		if !present {
			t.Errorf("guardrail missing key %q", k)
		}
		if v != nil {
			t.Errorf("guardrail %q = %v, want null", k, v)
		}
	}

	au := lines[1]
	if au["agent"] != "review-auditor" {
		t.Errorf("auditor agent = %v, want review-auditor", au["agent"])
	}
	if au["model"] != "claude-sonnet" {
		t.Errorf("auditor model = %v, want claude-sonnet", au["model"])
	}
	if au["duration_ms"].(float64) != 1234 {
		t.Errorf("auditor duration_ms = %v, want 1234", au["duration_ms"])
	}
	// tokens/cost still null even on the agent step
	if au["tokens"] != nil || au["cost"] != nil {
		t.Errorf("auditor tokens/cost must be null, got tokens=%v cost=%v", au["tokens"], au["cost"])
	}
}

func TestAuditLog_AppendsAndFlushesPerCall(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	a := NewAuditLog("runX", "owner/repo", 1)

	if err := a.record(auditStep{step: "guardrail", status: "fail", detail: "sha_fresh: stale"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	path := filepath.Join(root, "audit", "review_auto_post-owner-repo-1-runX.jsonl")
	// after one record the file must already exist (immediate flush)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audit file not flushed after first record: %v", err)
	}
	if got := len(readAuditLines(t, path)); got != 1 {
		t.Fatalf("want 1 line after first record, got %d", got)
	}

	if err := a.record(auditStep{step: "outcome", status: "pass", detail: "punch"}); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if got := len(readAuditLines(t, path)); got != 2 {
		t.Fatalf("want 2 lines after second record, got %d", got)
	}
}

func TestAuditLog_RejectsBadStatus(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PR_REVIEW_DIR", root)
	a := NewAuditLog("r", "o/r", 1)
	if err := a.record(auditStep{step: "x", status: "bogus"}); err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}

func TestAuditDir_HomeFallback(t *testing.T) {
	os.Unsetenv("PR_REVIEW_DIR")
	orig := auditHomeDirFunc
	t.Cleanup(func() { auditHomeDirFunc = orig })
	auditHomeDirFunc = func() (string, error) { return "/home/tester", nil }

	dir, err := auditDir()
	if err != nil {
		t.Fatalf("auditDir: %v", err)
	}
	want := filepath.Join("/home/tester", "PR-Review", "audit")
	if dir != want {
		t.Errorf("auditDir = %q, want %q", dir, want)
	}
}
