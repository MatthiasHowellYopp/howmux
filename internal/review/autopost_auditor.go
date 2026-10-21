// Package review (this file) orchestrates the ADVERSARIAL half of the review
// auto-post gate — the Stage 4 "adversarial review agent that challenges a
// prior step's output." It runs ONLY after the deterministic guardrail
// (autopost_guardrail.go) is clean.
//
// Design: docs/design/review-auto-post-gate.md (Adversarial auditor section +
// the `review-auditor` agent contract handed to ai-resources).
//
// Ownership split: the review-auditor PERSONA lives in ai-resources; this file
// is only the Go caller. It builds the prompt, invokes the agent through the
// metadata-capturing kiroOneshotMeta wrapper (so the call is recorded in the
// audit trail), and parses the agent's final VERDICT: line.
//
// FAIL-CLOSED: anything that prevents a confident POST_OK/DISCARD — the agent
// not being runnable (kiro-cli missing), a non-zero exit, empty output, or an
// unparseable verdict — resolves to NEEDS_HUMAN. The gate can only reduce human
// load, never post something unsupervised on a failure it couldn't reason about.
package review

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// auditorVerdict is the adversarial auditor's decision over a review.
type auditorVerdict string

const (
	auditorPostOK     auditorVerdict = "POST_OK"
	auditorNeedsHuman auditorVerdict = "NEEDS_HUMAN"
	auditorDiscard    auditorVerdict = "DISCARD"
)

// reviewAuditorAgent is the kiro agent name invoked for the adversarial step.
// Must match the persona authored in ai-resources.
const reviewAuditorAgent = "review-auditor"

// auditorVerdictLineRe matches the LAST "VERDICT: <value>" line, where value is
// exactly one of the three auditor verdicts (case-insensitive on the keyword,
// value upper-cased by the caller). Line-anchored and strict so decoration
// breaks the parse rather than silently mis-reading — the same discipline as
// verdictLineRe for review verdicts.
var auditorVerdictLineRe = regexp.MustCompile(`(?im)^\s*VERDICT:\s*(POST_OK|NEEDS_HUMAN|DISCARD)\s*$`)

// parseAuditorVerdict returns the value of the LAST matching VERDICT: line in
// the agent's answer, and ok=false when no well-formed verdict line is present
// (which the caller treats as fail-closed → NEEDS_HUMAN).
func parseAuditorVerdict(answer string) (auditorVerdict, bool) {
	matches := auditorVerdictLineRe.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return "", false
	}
	last := matches[len(matches)-1]
	return auditorVerdict(strings.ToUpper(strings.TrimSpace(last[1]))), true
}

// buildAuditorPrompt composes the prompt sent to the review-auditor agent. It
// supplies the inputs the agent contract specifies: repo, pr, the review's own
// verdict, the consolidated review body, and the PR diff (ground truth). The
// decision rubric and output contract live in the agent persona (ai-resources),
// so this prompt only supplies data and restates the output contract tersely as
// a guard against a drifting persona.
func buildAuditorPrompt(repo string, pr int, verdict, body, diff string) string {
	return fmt.Sprintf(`You are auditing an already-generated PR review that may be posted
automatically, without a human. Judge whether it is safe to post unsupervised.

repo: %s
pr: %d
review verdict: %s

--- BEGIN REVIEW BODY ---
%s
--- END REVIEW BODY ---

--- BEGIN PR DIFF (ground truth) ---
%s
--- END PR DIFF ---

Decide per your review-auditor rubric. End with a single final line exactly:
VERDICT: <POST_OK|NEEDS_HUMAN|DISCARD>`, repo, pr, verdict, body, diff)
}

// auditorResult carries the parsed verdict plus the OneshotResult metadata of
// the agent call, so the caller can both route on the verdict and record the
// step (model, duration, status) in the audit trail.
type auditorResult struct {
	verdict auditorVerdict
	meta    OneshotResult
	// reason is a short human note for the audit detail: the agent's own
	// rationale when parsed, or the fail-closed cause.
	reason string
}

// callReviewAuditor runs the adversarial auditor over a review and returns its
// verdict (fail-closed to NEEDS_HUMAN) plus the call metadata. It does NOT
// decide what to do with the verdict — that is the gate's job
// (autopost_gate.go). It performs no spool or GitHub I/O.
//
// Fail-closed cases, all → NEEDS_HUMAN:
//   - kiro-cli not on PATH (agent not runnable)
//   - kiroOneshotMeta error (non-zero exit / empty output)
//   - the answer has no well-formed VERDICT: line
func callReviewAuditor(ctx context.Context, repo string, pr int, verdict, body, diff string) auditorResult {
	// Preflight: the agent runs via kiro-cli. If kiro-cli isn't resolvable,
	// the auditor cannot run — fail closed without attempting the call.
	if _, err := lookPathForDecideActionsFunc("kiro-cli"); err != nil {
		return auditorResult{
			verdict: auditorNeedsHuman,
			meta:    OneshotResult{Status: "error"},
			reason:  "kiro-cli not on PATH; auditor could not run (fail-closed to NEEDS_HUMAN)",
		}
	}

	prompt := buildAuditorPrompt(repo, pr, verdict, body, diff)
	meta, err := kiroOneshotMeta(ctx, reviewAuditorAgent, "", prompt)
	if err != nil {
		return auditorResult{
			verdict: auditorNeedsHuman,
			meta:    meta,
			reason:  fmt.Sprintf("auditor call failed: %v (fail-closed to NEEDS_HUMAN)", err),
		}
	}

	v, ok := parseAuditorVerdict(meta.Answer)
	if !ok {
		return auditorResult{
			verdict: auditorNeedsHuman,
			meta:    meta,
			reason:  "auditor produced no parseable VERDICT: line (fail-closed to NEEDS_HUMAN)",
		}
	}

	return auditorResult{verdict: v, meta: meta, reason: string(v)}
}
