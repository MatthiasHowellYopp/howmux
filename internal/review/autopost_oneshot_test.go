package review

import (
	"context"
	"errors"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/kirocli"
)

func TestKiroOneshotMeta_Pass(t *testing.T) {
	withFakeKiroOneshot(t, map[string]string{"review-auditor": "the answer"}, nil)

	got, err := kiroOneshotMeta(context.Background(), "review-auditor", "claude-sonnet", "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Answer != "the answer" {
		t.Errorf("Answer = %q, want %q", got.Answer, "the answer")
	}
	if got.Status != "pass" {
		t.Errorf("Status = %q, want pass", got.Status)
	}
	if got.Model != "claude-sonnet" {
		t.Errorf("Model = %q, want claude-sonnet", got.Model)
	}
	if got.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", got.DurationMs)
	}
}

func TestKiroOneshotMeta_Error(t *testing.T) {
	wantErr := errors.New("kiro boom")
	withFakeKiroOneshot(t, nil, map[string]error{"review-auditor": wantErr})

	got, err := kiroOneshotMeta(context.Background(), "review-auditor", "m", "prompt")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got.Status != "error" {
		t.Errorf("Status = %q, want error", got.Status)
	}
	if got.Answer != "" {
		t.Errorf("Answer = %q, want empty on error", got.Answer)
	}
	if got.Model != "m" {
		t.Errorf("Model = %q, want m (recorded even on error)", got.Model)
	}
}

// TestKiroOneshotMeta_CarriesStreamMetadata: the engine, kiro turn duration,
// context usage and stop reason from the one-shot result reach
// OneshotResult (and from there the audit trail).
func TestKiroOneshotMeta_CarriesStreamMetadata(t *testing.T) {
	orig := kiroOneshotFunc
	t.Cleanup(func() { kiroOneshotFunc = orig })
	turn, pct := 1497.0, 2.37
	kiroOneshotFunc = func(context.Context, string, string) (kirocli.Result, error) {
		return kirocli.Result{Text: "VERDICT: POST_OK", Meta: kirocli.Meta{
			TurnDurationMs: &turn, ContextUsagePct: &pct, Engine: "v3", StopReason: "end_turn"}}, nil
	}
	got, err := kiroOneshotMeta(context.Background(), "review-auditor", "", "prompt")
	if err != nil {
		t.Fatalf("kiroOneshotMeta: %v", err)
	}
	if got.Answer != "VERDICT: POST_OK" || got.Engine != "v3" || got.StopReason != "end_turn" ||
		got.TurnDurationMs == nil || *got.TurnDurationMs != turn || got.ContextUsagePct == nil || *got.ContextUsagePct != pct {
		t.Errorf("OneshotResult = %+v", got)
	}
}
