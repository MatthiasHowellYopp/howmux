// Package review (this file) is the TOP of the review auto-post gate: it wires
// the deterministic guardrail (autopost_guardrail.go) and the adversarial
// auditor (autopost_auditor.go) into a single decision, honors the configured
// trust mode (dry-run / own-repos / all), routes to the existing
// PostReview/DiscardReview actions or punches to the human spool gate, and
// writes an audit record at each step (autopost_auditlog.go).
//
// Design: docs/design/review-auto-post-gate.md.
//
// FAIL-CLOSED and PUNCH-OUT: the only outcome that posts without a human is a
// clean deterministic guardrail AND an auditor POST_OK AND a trust mode that
// allows this repo. Everything else — any guardrail failure, auditor
// NEEDS_HUMAN, or a repo not permitted by the mode — leaves the review in
// pending/ with no decision, which is exactly the existing human gate. DISCARD
// from the auditor auto-discards. In dry-run, NOTHING is posted or discarded:
// the gate records what it WOULD have done and always punches, so the Phase 0
// agreement rate can be measured with zero risk.
package review

import (
	"context"
	"strings"

	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// AutoPostMode is the trust scope for auto-posting.
type AutoPostMode string

const (
	// ModeDryRun records the gate's would-be decision but always punches to a
	// human. The safe default.
	ModeDryRun AutoPostMode = "dry-run"
	// ModeOwnRepos allows real auto-post/discard only for repos whose owner is
	// in OwnRepoOwners; all other repos punch.
	ModeOwnRepos AutoPostMode = "own-repos"
	// ModeAll allows real auto-post/discard for every repo.
	ModeAll AutoPostMode = "all"
)

// AutoPostConfig controls the gate. The zero value is safe: an empty Mode is
// treated as dry-run, and a zero MaxFindings disables the finding-count cap.
type AutoPostConfig struct {
	Mode          AutoPostMode // dry-run | own-repos | all
	MaxFindings   int          // guardrail finding-count cap (<=0 disables)
	OwnRepoOwners []string     // owners allowed to auto-post in own-repos mode (owner part of owner/repo)
}

// autoPostOutcome is the gate's final decision for one review.
type autoPostOutcome string

const (
	outcomePosted    autoPostOutcome = "posted"    // auto-posted to GitHub
	outcomeDiscarded autoPostOutcome = "discarded" // auto-discarded
	outcomePunted    autoPostOutcome = "punch"     // left in pending/ for the human
	outcomeDryRun    autoPostOutcome = "dry-run"   // would-be decision recorded; punched
)

// GateResult is the gate's reported outcome, for the caller to log/display.
type GateResult struct {
	Outcome        autoPostOutcome // what the gate actually did
	WouldHaveDone  autoPostOutcome // in dry-run, what it WOULD have done; else == Outcome
	Reason         string          // short human explanation
	CommentsPosted int             // inline comments posted (only when Outcome == posted)
}

// --- action seams (so the gate is testable without gh / kiro-cli) ----------

// postReviewFunc and discardReviewFunc are seams over the real PostReview /
// DiscardReview so gate tests can assert routing without shelling out. They
// default to the real implementations.
var postReviewFunc = PostReview
var discardReviewFunc = DiscardReview

// EvaluateAndMaybePost is the gate entry point. It runs the deterministic
// guardrail, then (if clean) the adversarial auditor, then routes to an action
// per the configured mode, writing an audit record at each step.
//
// Inputs the caller supplies (the review watcher has all of these after a
// successful RunReview):
//   - rec:         the review Record (repo, pr, spool path)
//   - body:        the consolidated review body (already read from the spool)
//   - verdict:     the review's verdict
//   - headSHA:     the PR's current head SHA
//   - reviewedSHA: the spool's recorded reviewed_sha
//   - diff:        the PR diff text (for anchoring + the auditor's ground truth)
//   - cfg:         the trust config
//
// It never returns an error for a "punch" — punching is a normal outcome, not a
// failure. It returns an error only if a real post/discard action it attempted
// failed.
func EvaluateAndMaybePost(ctx context.Context, rec Record, body, verdict, headSHA, reviewedSHA, diff string, cfg AutoPostConfig) (GateResult, error) {
	audit := NewAuditLog(newRunID(), rec.Repo, rec.PR)

	// Step 1: deterministic guardrail.
	commentable := parseDiffCommentableLines(diff)
	g := evaluateAutoPostGuardrail(verdict, body, headSHA, reviewedSHA, commentable, cfg.MaxFindings)
	if !g.clean {
		reason := "guardrail failed: " + g.firstFailure()
		recordAudit(audit, auditStep{step: "guardrail", status: "fail", detail: reason})
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: string(outcomePunted) + " (" + reason + ")"})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePunted, Reason: reason}, nil
	}
	recordAudit(audit, auditStep{step: "guardrail", status: "pass", detail: "all deterministic checks clean"})

	// Step 2: adversarial auditor.
	ar := callReviewAuditor(ctx, rec.Repo, rec.PR, verdict, body, diff)
	auditorStatus := "pass"
	if ar.meta.Status == "error" {
		auditorStatus = "error"
	}
	recordAudit(audit, auditStep{
		step:   "auditor",
		status: auditorStatus,
		agent:  reviewAuditorAgent,
		detail: string(ar.verdict) + ": " + ar.reason,
		meta:   &ar.meta,
	})

	// Map the auditor verdict to the action the gate WOULD take (ignoring mode).
	var would autoPostOutcome
	switch ar.verdict {
	case auditorPostOK:
		would = outcomePosted
	case auditorDiscard:
		would = outcomeDiscarded
	default: // NEEDS_HUMAN and any unexpected value → punch
		would = outcomePunted
	}

	// Step 3: apply trust mode. Dry-run never acts. own-repos acts only for
	// permitted owners. A would-be punch is a punch regardless of mode.
	if cfg.Mode == "" || cfg.Mode == ModeDryRun {
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "dry-run; would " + string(would)})
		return GateResult{Outcome: outcomeDryRun, WouldHaveDone: would, Reason: "dry-run; would " + string(would)}, nil
	}
	if would == outcomePunted {
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: string(outcomePunted) + " (auditor NEEDS_HUMAN)"})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePunted, Reason: "auditor: " + ar.reason}, nil
	}
	if cfg.Mode == ModeOwnRepos && !ownerAllowed(rec.Repo, cfg.OwnRepoOwners) {
		reason := "own-repos mode: " + repoOwner(rec.Repo) + " not in allow-list; punching"
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: reason})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: would, Reason: reason}, nil
	}

	// Step 4: perform the real action.
	switch would {
	case outcomePosted:
		n, err := postReviewFunc(ctx, rec)
		if err != nil {
			recordAudit(audit, auditStep{step: "outcome", status: "error", detail: "post failed: " + err.Error()})
			return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePosted, Reason: "post failed: " + err.Error()}, err
		}
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "posted"})
		return GateResult{Outcome: outcomePosted, WouldHaveDone: outcomePosted, Reason: "auto-posted", CommentsPosted: n}, nil
	case outcomeDiscarded:
		if err := discardReviewFunc(rec); err != nil {
			recordAudit(audit, auditStep{step: "outcome", status: "error", detail: "discard failed: " + err.Error()})
			return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomeDiscarded, Reason: "discard failed: " + err.Error()}, err
		}
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "discarded"})
		return GateResult{Outcome: outcomeDiscarded, WouldHaveDone: outcomeDiscarded, Reason: "auto-discarded"}, nil
	}

	// Unreachable (would is one of posted/discarded/punted, punted handled above).
	return GateResult{Outcome: outcomePunted, WouldHaveDone: would, Reason: "no action"}, nil
}

// recordAudit writes one audit step, logging (not returning) any write error —
// the gate's decision must never depend on the audit write succeeding.
func recordAudit(a *AuditLog, s auditStep) {
	if err := a.record(s); err != nil {
		logging.Warn("auto-post gate: audit record failed", "step", s.step, "error", err)
	}
}

// repoOwner returns the owner part of an "owner/repo" string ("" if malformed).
func repoOwner(repo string) string {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// ownerAllowed reports whether repo's owner is in the allow-list
// (case-insensitive, matching GitHub's case-insensitive owner handling).
func ownerAllowed(repo string, owners []string) bool {
	owner := repoOwner(repo)
	if owner == "" {
		return false
	}
	for _, o := range owners {
		if strings.EqualFold(o, owner) {
			return true
		}
	}
	return false
}
