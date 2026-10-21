package review

import (
	"context"
	"errors"
	"testing"
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
