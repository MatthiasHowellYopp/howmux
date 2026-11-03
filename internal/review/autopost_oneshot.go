// Package review (this file) adds a metadata-capturing wrapper over the
// existing kiroOneshotFunc seam, used by the review auto-post gate so each
// agentic step can be recorded in the Stage 4 audit trail.
//
// Design: docs/design/review-auto-post-gate.md (Audit trail section).
//
// IMPORTANT — kiro-cli cost/token limitation (not a howmux choice):
// kiro-cli emits NO token counts and NO cost figures in any output mode
// (confirmed by ai-resources' workflows/audit_log.py, which checked --help,
// --help-all, and a live stream-json run). So OneshotResult deliberately does
// NOT carry tokens or cost — the audit log records those as explicit null.
// What we CAN capture per call:
//   - model:      the model kiro-cli reports (pinned id, or "auto" when unpinned)
//   - creditRate: that model's credit rate multiplier (a rate, not usage)
//   - durationMs: wall-clock around the subprocess (always available)
//   - status:     "pass" on success, "error" on failure
//   - from kiro-cli stream-json, on both agent engines (see internal/kirocli):
//     the engine that ran the turn, kiro's own turn duration, context-window
//     usage, and the stop reason. Each is nil/"" if the stream lacked it.
package review

import (
	"context"
	"time"
)

// OneshotResult is the metadata captured around a single kiro-cli agent call,
// alongside its text answer. Populated by kiroOneshotMeta.
type OneshotResult struct {
	Answer     string // the cleaned assistant answer (empty on error)
	Model      string // the model kiro-cli reported (pinned id or "auto"); else the --model passed
	DurationMs int64  // wall-clock milliseconds for the subprocess call
	Status     string // "pass" | "error"

	// Stream-json metadata (nil / "" when not reported).
	TurnDurationMs  *float64
	ContextUsagePct *float64
	Engine          string // "v2" | "v3"
	StopReason      string
	CreditRate      *float64 // the model's credit rate multiplier; nil if not reported
}

// kiroOneshotMeta calls the existing kiroOneshotFunc seam and wraps it with
// wall-clock timing and a pass/error status, returning a OneshotResult. It
// does NOT change the seam itself, so every existing text-only caller
// (humanizeReviewBody, ReviseReview) and the test
// fake that substitutes kiroOneshotFunc keep working unchanged.
//
// model is passed to kiro-cli as --model (through kiroOneshotFunc). The
// recorded model is the one kiro-cli reports in stream-json: the pinned id,
// or "auto" when model is empty (the router's pick is never named). If the
// stream doesn't report one, the model we passed is recorded instead.
//
// On error the returned OneshotResult has Status "error", an empty Answer, and
// the measured DurationMs; the error is returned too so callers can act on it.
func kiroOneshotMeta(ctx context.Context, agentName, model, prompt string) (OneshotResult, error) {
	start := time.Now()
	res, err := kiroOneshotFunc(ctx, agentName, model, prompt)
	dur := time.Since(start).Milliseconds()

	recorded := res.Model
	if recorded == "" {
		recorded = model
	}
	out := OneshotResult{
		Model:           recorded,
		CreditRate:      res.CreditRate,
		DurationMs:      dur,
		TurnDurationMs:  res.TurnDurationMs,
		ContextUsagePct: res.ContextUsagePct,
		Engine:          res.Engine,
		StopReason:      res.StopReason,
	}
	if err != nil {
		out.Status = "error"
		return out, err
	}
	out.Answer = res.Text
	out.Status = "pass"
	return out, nil
}
