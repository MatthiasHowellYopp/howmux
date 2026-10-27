package review

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// withKiroCLIPresent / withKiroCLIMissing fake the PATH preflight for kiro-cli.
func withKiroCLIPresent(t *testing.T) {
	t.Helper()
	orig := lookPathForDecideActionsFunc
	lookPathForDecideActionsFunc = func(file string) (string, error) {
		if file == "kiro-cli" {
			return "/usr/local/bin/kiro-cli", nil
		}
		return orig(file)
	}
	t.Cleanup(func() { lookPathForDecideActionsFunc = orig })
}

func withKiroCLIMissing(t *testing.T) {
	t.Helper()
	orig := lookPathForDecideActionsFunc
	lookPathForDecideActionsFunc = func(file string) (string, error) {
		if file == "kiro-cli" {
			return "", exec.ErrNotFound
		}
		return orig(file)
	}
	t.Cleanup(func() { lookPathForDecideActionsFunc = orig })
}

func TestParseAuditorVerdict(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   auditorVerdict
		wantOK bool
	}{
		{"post_ok", "rationale...\nNOTES: fine\nVERDICT: POST_OK", auditorPostOK, true},
		{"needs_human", "VERDICT: NEEDS_HUMAN", auditorNeedsHuman, true},
		{"discard", "stuff\nVERDICT: DISCARD\n", auditorDiscard, true},
		{"lowercase keyword ok", "verdict: post_ok", auditorPostOK, true},
		{"last verdict wins", "VERDICT: DISCARD\nmore\nVERDICT: POST_OK", auditorPostOK, true},
		{"no verdict line", "I think this is fine, post it.", "", false},
		{"decorated breaks parse", "**VERDICT: POST_OK**", "", false},
		{"unknown value rejected", "VERDICT: MAYBE", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAuditorVerdict(tt.answer)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("parseAuditorVerdict() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestCallReviewAuditor_Verdicts(t *testing.T) {
	cases := []struct {
		name     string
		response string
		want     auditorVerdict
	}{
		{"post_ok", "NOTES: sound\nVERDICT: POST_OK", auditorPostOK},
		{"needs_human", "NOTES: finding 2 is shaky\nVERDICT: NEEDS_HUMAN", auditorNeedsHuman},
		{"discard", "NOTES: all noise\nVERDICT: DISCARD", auditorDiscard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withKiroCLIPresent(t)
			withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: tc.response}, nil)

			got := callReviewAuditor(context.Background(), "owner/repo", 1, "COMMENT", "body", "diff")
			if got.verdict != tc.want {
				t.Errorf("verdict = %q, want %q", got.verdict, tc.want)
			}
			if got.meta.Status != "pass" {
				t.Errorf("meta.Status = %q, want pass", got.meta.Status)
			}
			if !strings.Contains(got.reason, "NOTES:") {
				t.Errorf("reason = %q, want the auditor's NOTES carried through", got.reason)
			}
		})
	}
}

func TestAuditorRationale(t *testing.T) {
	tests := []struct {
		name, answer, want string
	}{
		{"notes and rationale", "Finding 1 is real.\nSeverity fits.\nNOTES: watch finding 2\nVERDICT: POST_OK",
			"NOTES: watch finding 2 | Finding 1 is real. Severity fits."},
		{"notes only", "NOTES: all noise\nVERDICT: DISCARD", "NOTES: all noise"},
		{"rationale only", "Looks sound.\nVERDICT: POST_OK", "Looks sound."},
		{"verdict only", "VERDICT: POST_OK", ""},
		{"bare notes does not swallow next line", "NOTES:\nFinding 1 is wrong.\nVERDICT: NEEDS_HUMAN",
			"Finding 1 is wrong."},
		{"multiple notes are all kept", "NOTES: first\nmid\nNOTES: second\nVERDICT: POST_OK",
			"NOTES: first; second | mid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auditorRationale(tt.answer); got != tt.want {
				t.Errorf("auditorRationale() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCallReviewAuditor_FailClosed_MissingAgent(t *testing.T) {
	withKiroCLIMissing(t)
	// kiroOneshotFunc should never be called; set a fake that would fail the
	// test if invoked by returning a POST_OK we must NOT see.
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "VERDICT: POST_OK"}, nil)

	got := callReviewAuditor(context.Background(), "owner/repo", 1, "COMMENT", "body", "diff")
	if got.verdict != auditorNeedsHuman {
		t.Errorf("verdict = %q, want NEEDS_HUMAN (fail-closed on missing kiro-cli)", got.verdict)
	}
}

func TestCallReviewAuditor_FailClosed_CallError(t *testing.T) {
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, nil, map[string]error{reviewAuditorAgent: errors.New("kiro exited 1")})

	got := callReviewAuditor(context.Background(), "owner/repo", 1, "COMMENT", "body", "diff")
	if got.verdict != auditorNeedsHuman {
		t.Errorf("verdict = %q, want NEEDS_HUMAN (fail-closed on call error)", got.verdict)
	}
	if got.meta.Status != "error" {
		t.Errorf("meta.Status = %q, want error", got.meta.Status)
	}
}

func TestCallReviewAuditor_FailClosed_Unparseable(t *testing.T) {
	withKiroCLIPresent(t)
	withFakeKiroOneshot(t, map[string]string{reviewAuditorAgent: "I think it's fine but I won't say the magic word."}, nil)

	got := callReviewAuditor(context.Background(), "owner/repo", 1, "COMMENT", "body", "diff")
	if got.verdict != auditorNeedsHuman {
		t.Errorf("verdict = %q, want NEEDS_HUMAN (fail-closed on unparseable verdict)", got.verdict)
	}
}
