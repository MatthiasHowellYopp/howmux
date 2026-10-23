package kirocli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func lines(t *testing.T, events ...any) string {
	t.Helper()
	var out []string
	for _, e := range events {
		if s, ok := e.(string); ok {
			out = append(out, s)
			continue
		}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return strings.Join(out, "\n")
}

type m = map[string]any

func chunk(sid, text string) m {
	return m{"type": "sessionUpdate", "data": m{"sessionId": sid, "update": m{
		"sessionUpdate": "agent_message_chunk", "content": m{"type": "text", "text": text}}}}
}

func v3Info(sid string, kiro m) m {
	return m{"type": "sessionUpdate", "data": m{"sessionId": sid, "update": m{
		"sessionUpdate": "session_info_update", "_meta": m{"kiro": kiro}}}}
}

func f(v float64) *float64 { return &v }

// TestParseStreamJSON mirrors ai-resources workflows/tests/test_oneshot_parse.py
// case for case (same synthetic streams, same expectations), plus stop
// reason / truncation cases. Keep the two in lockstep.
func TestParseStreamJSON(t *testing.T) {
	v2 := lines(t,
		m{"type": "runStarted", "data": m{"sessionId": "s2", "engine": "v2"}},
		m{"type": "metadata", "data": m{"sessionId": "s2", "contextUsagePercentage": 4.8}},
		chunk("s2", "VERDICT: "),
		chunk("s2", "DISCARD"),
		m{"type": "metadata", "data": m{"sessionId": "s2", "contextUsagePercentage": 5.09, "turnDurationMs": 1410}},
		m{"type": "runFinished", "data": m{"sessionId": "s2", "status": "completed", "stopReason": "end_turn", "finalText": "VERDICT: DISCARD"}},
	)
	v3 := lines(t,
		m{"type": "runStarted", "data": m{"sessionId": "s3", "engine": "v3"}},
		chunk("s3", "VERDICT: DISCARD"),
		v3Info("s3", m{"kind": "context_usage", "usagePercentage": 3.2}),
		v3Info("s3", m{"kind": "turn_completion", "elapsedTime": 4979}),
		m{"type": "runFinished", "data": m{"sessionId": "s3", "status": "completed", "stopReason": "end_turn", "finalText": "VERDICT: DISCARD"}},
	)
	malformed := lines(t,
		"not json at all",
		`{"type": "runStarted", "data": {"engine": "v3"`,
		"[1, 2, 3]",
		m{"type": "runStarted", "data": m{"sessionId": "s4", "engine": "v3"}},
		v3Info("s4", m{"kind": "something_new", "elapsedTime": 999}),
		chunk("s4", "partial answer"),
		"",
		v3Info("s4", m{"kind": "turn_completion", "elapsedTime": 12}),
	)
	truncated := lines(t,
		chunk("s5", "full chunked answer"),
		m{"type": "runFinished", "data": m{"sessionId": "s5", "stopReason": "max_tokens", "finalText": "full chu", "finalTextTruncated": true}},
	)

	tests := []struct {
		name, stream, text string
		meta               Meta
	}{
		{"v2_metadata_event", v2, "VERDICT: DISCARD",
			Meta{TurnDurationMs: f(1410), ContextUsagePct: f(5.09), Engine: "v2", SessionID: "s2", StopReason: "end_turn", Status: "completed"}},
		{"v3_session_info_update", v3, "VERDICT: DISCARD",
			Meta{TurnDurationMs: f(4979), ContextUsagePct: f(3.2), Engine: "v3", SessionID: "s3", StopReason: "end_turn", Status: "completed"}},
		{"malformed_lines_skipped", malformed, "partial answer",
			Meta{TurnDurationMs: f(12), Engine: "v3", SessionID: "s4"}},
		{"empty_stream", "", "", Meta{}},
		{"truncated_final_text_falls_back_to_chunks", truncated, "full chunked answer",
			Meta{SessionID: "s5", StopReason: "max_tokens"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, meta := ParseStreamJSON(tt.stream)
			if text != tt.text {
				t.Errorf("text = %q, want %q", text, tt.text)
			}
			if !reflect.DeepEqual(meta, tt.meta) {
				t.Errorf("meta = %+v, want %+v", fmtMeta(meta), fmtMeta(tt.meta))
			}
		})
	}
}

func fmtMeta(x Meta) map[string]any {
	d := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return map[string]any{"dur": d(x.TurnDurationMs), "ctx": d(x.ContextUsagePct), "stop": x.StopReason,
		"status": x.Status, "session": x.SessionID, "engine": x.Engine}
}

func TestArgs(t *testing.T) {
	got := Args(Request{Agent: "review-auditor", Prompt: "p", TrustAllTools: true})
	want := []string{"chat", "--no-interactive", "--trust-all-tools", "--agent", "review-auditor", "--output-format", "stream-json", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v, want %v", got, want)
	}
	got = Args(Request{Prompt: "p", PromptOnStdin: true, Model: "m1"})
	want = []string{"chat", "--no-interactive", "--output-format", "stream-json", "--model", "m1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args(stdin, no agent) = %v, want %v", got, want)
	}
}

// fakeCLI points cliPath at a shell script for the test's duration.
func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "kiro-cli")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := cliPath
	cliPath = p
	t.Cleanup(func() { cliPath = orig })
	return dir
}

func TestRun_ParsesStream(t *testing.T) {
	fakeCLI(t, `cat <<'EOF'
{"type":"runStarted","data":{"engine":"v3"}}
{"type":"sessionUpdate","data":{"sessionId":"s","update":{"sessionUpdate":"session_info_update","_meta":{"kiro":{"kind":"turn_completion","elapsedTime":42}}}}}
{"type":"runFinished","data":{"sessionId":"s","status":"success","stopReason":"end_turn","finalText":"PONG"}}
EOF
`)
	res, err := Run(context.Background(), Request{Agent: "a", Prompt: "p", TrustAllTools: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text != "PONG" || res.Engine != "v3" || res.StopReason != "end_turn" || res.TurnDurationMs == nil || *res.TurnDurationMs != 42 {
		t.Errorf("Run = %+v", res)
	}
}

func TestRun_PromptOnStdin(t *testing.T) {
	dir := fakeCLI(t, `cat > "$(dirname "$0")/stdin.txt"; printf '%s\n' "$*" > "$(dirname "$0")/argv.txt"
echo '{"type":"runFinished","data":{"finalText":"ok"}}'
`)
	if _, err := Run(context.Background(), Request{Prompt: "judge this", PromptOnStdin: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin.txt"))
	argv, _ := os.ReadFile(filepath.Join(dir, "argv.txt"))
	if string(stdin) != "judge this" {
		t.Errorf("stdin = %q, want the prompt", stdin)
	}
	if strings.Contains(string(argv), "judge this") || strings.Contains(string(argv), "--trust-all-tools") {
		t.Errorf("argv = %q: prompt must be on stdin only, and tools not trusted unless asked", argv)
	}
}

func TestRun_NonZeroExitKeepsStderrTail(t *testing.T) {
	fakeCLI(t, `i=0; while [ $i -lt 200 ]; do echo "noisy v3 log line $i" >&2; i=$((i+1)); done
echo 'agent "x" not found, using "default"' >&2; exit 4
`)
	_, err := Run(context.Background(), Request{Agent: "x", Prompt: "p"})
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exited 4") || !strings.Contains(msg, `agent "x" not found`) {
		t.Errorf("error should keep exit code and the final stderr line: %q", msg)
	}
	if strings.Contains(msg, "noisy v3 log line 0\n") || len(msg) > tailLen+200 {
		t.Errorf("error should carry only a stderr tail, got %d bytes", len(msg))
	}
}

func TestRun_EmptyAnswer(t *testing.T) {
	fakeCLI(t, `echo '{"type":"runFinished","data":{"stopReason":"refusal","finalText":""}}'
`)
	res, err := Run(context.Background(), Request{Agent: "a", Prompt: "p"})
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
	if res.StopReason != "refusal" {
		t.Errorf("meta should survive an empty answer, got %+v", res.Meta)
	}
}

func TestTailIsRuneSafe(t *testing.T) {
	got := tail(strings.Repeat("é", tailLen)) // 2 bytes each, so the cut lands mid-rune
	if !strings.HasPrefix(got, "…") || !utf8.ValidString(got) {
		t.Errorf("tail produced invalid UTF-8: %q", got[:10])
	}
}

// TestRun_ReportsRunError: in stream-json mode kiro-cli reports failures such
// as an unknown agent as a runError event on stdout (v2: empty stderr; v3:
// stderr is engine logging). The error must carry that message.
func TestRun_ReportsRunError(t *testing.T) {
	fakeCLI(t, `echo '{"type":"runStarted","data":{"engine":"v3"}}'
echo '{"type":"runError","data":{"sessionId":"s","stage":"init","message":"agent \\"x\\" not found, using \\"default\\""}}'
echo '[INFO] fs.workflow.recipes.readDir.ok {"system":"fs"}' >&2
exit 4
`)
	res, err := Run(context.Background(), Request{Agent: "x", Prompt: "p"})
	if err == nil {
		t.Fatal("want error")
	}
	if want := `init: agent "x" not found, using "default"`; !strings.Contains(err.Error(), want) || res.RunError != want {
		t.Errorf("err = %q, RunError = %q; want both to carry %q", err, res.RunError, want)
	}
	if strings.Contains(err.Error(), "[INFO]") {
		t.Errorf("engine log noise should not replace the runError message: %q", err)
	}
}
