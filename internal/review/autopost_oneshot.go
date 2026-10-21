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
// What we CAN capture per call, all real and transport-independent:
//   - model:      the --model value we pass (empty when we pass none)
//   - durationMs: wall-clock around the subprocess
//   - status:     "pass" on success, "error" on failure
//
// turn_duration_ms / context_usage_pct (from kiro-cli stream-json) are a
// possible future enrichment and are intentionally not captured here yet;
// wall-clock durationMs is the reliable baseline.
package review

import (
	"context"
	"time"
)

// OneshotResult is the metadata captured around a single kiro-cli agent call,
// alongside its text answer. Populated by kiroOneshotMeta.
type OneshotResult struct {
	Answer     string // the cleaned assistant answer (empty on error)
	Model      string // the --model value passed, if any ("" when none)
	DurationMs int64  // wall-clock milliseconds for the subprocess call
	Status     string // "pass" | "error"
}

// kiroOneshotMeta calls the existing kiroOneshotFunc seam and wraps it with
// wall-clock timing and a pass/error status, returning a OneshotResult. It
// does NOT change the seam itself, so every existing text-only caller
// (humanizeReviewBody, ReviseReview, the rereview lens fan-out) and the test
// fake that substitutes kiroOneshotFunc keep working unchanged.
//
// model is the value the caller intends to pass via --model; today the seam
// does not forward it to kiro-cli (the default impl passes no --model), so it
// is recorded for the audit trail but not yet acted on. When the seam learns
// to forward --model, this is where it threads through.
//
// On error the returned OneshotResult has Status "error", an empty Answer, and
// the measured DurationMs; the error is returned too so callers can act on it.
func kiroOneshotMeta(ctx context.Context, agentName, model, prompt string) (OneshotResult, error) {
	start := time.Now()
	answer, err := kiroOneshotFunc(ctx, agentName, prompt)
	dur := time.Since(start).Milliseconds()

	if err != nil {
		return OneshotResult{Model: model, DurationMs: dur, Status: "error"}, err
	}
	return OneshotResult{Answer: answer, Model: model, DurationMs: dur, Status: "pass"}, nil
}
