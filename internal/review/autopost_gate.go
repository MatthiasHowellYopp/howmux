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
	"fmt"
	"os"
	"strings"
	"sync/atomic"

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
	AuditorModel  string       // kiro-cli --model for the review-auditor; "" = kiro-cli's default
	Language      string       // the review's language lens (spool "language:"); "generic" is forced to a human
	// InjectionGuard is the spool's "injection_guard:" value (see
	// injectionGuardValue); "" when absent. A "flagged…" value is forced to a
	// human by the no_injection guardrail check.
	InjectionGuard string
}

// ActsOn reports whether the trust mode lets the gate take a real action
// (post/discard) on repo without a human: always in ModeAll, only for
// allow-listed owners in ModeOwnRepos, never in dry-run (or an empty Mode).
func (c AutoPostConfig) ActsOn(repo string) bool {
	switch c.Mode {
	case ModeAll:
		return true
	case ModeOwnRepos:
		return ownerAllowed(repo, c.OwnRepoOwners)
	default:
		return false
	}
}

// reviewScopeConfig is the auto-post config RunReview consults to choose the
// prior-feedback issue scope it asks pr_review.py for. It is package-level
// (rather than a RunReview parameter) because RunReview's signature is shared
// by several seams; nil (never set) means "related" scope.
var reviewScopeConfig atomic.Pointer[AutoPostConfig]

// SetReviewScopeConfig records the auto-post config RunReview uses to choose
// the issue scope for prior-feedback fetching: repos the gate ActsOn get
// `--issues all` (no human will catch a re-raised point), every other repo
// gets `--issues related`. Safe for concurrent use.
func SetReviewScopeConfig(cfg AutoPostConfig) {
	reviewScopeConfig.Store(&cfg)
}

// issueScopeFor returns the pr_review.py --issues value for repo, based on
// the config set via SetReviewScopeConfig.
func issueScopeFor(repo string) string {
	if cfg := reviewScopeConfig.Load(); cfg != nil && cfg.ActsOn(repo) {
		return "all"
	}
	return "related"
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
//   - prior:       the spool's prior feedback (nil when the spool has no
//     prior_feedback key); feeds the not_already_raised check and the auditor
//
// It never returns an error for a "punch" — punching is a normal outcome, not a
// failure. It returns an error only if a real post/discard action it attempted
// failed.
func EvaluateAndMaybePost(ctx context.Context, rec Record, body, verdict, headSHA, reviewedSHA, diff string, cfg AutoPostConfig, prior *LoadedPriorFeedback) (GateResult, error) {
	audit := NewAuditLog(newRunID(), rec.Repo, rec.PR)
	audit.TraceID = SpoolTraceID(rec.SpoolPath)

	// Step 0: record the inputs, so the agreement report can tie this run to
	// the exact review generation (reviewed_sha) a human later decided on.
	recordAudit(audit, auditStep{step: "inputs", status: "pass", detail: formatKV(
		"verdict", strings.ToUpper(strings.TrimSpace(verdict)),
		"language", strings.TrimSpace(cfg.Language), // "" → "-" via formatKV
		"reviewed_sha", reviewedSHA,
		"head_sha", headSHA,
	)})

	// Step 1: deterministic guardrail.
	commentable := parseDiffCommentableLines(diff)
	g := evaluateAutoPostGuardrail(verdict, body, headSHA, reviewedSHA, commentable, cfg.MaxFindings, cfg.Language, prior, cfg.InjectionGuard)
	if !g.clean {
		reason := "guardrail failed: " + g.firstFailure()
		recordAudit(audit, auditStep{step: "guardrail", status: "fail", detail: reason})
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: string(outcomePunted) + " (" + reason + ")"})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePunted, Reason: reason}, nil
	}
	recordAudit(audit, auditStep{step: "guardrail", status: "pass", detail: "all deterministic checks clean"})

	// Step 2: adversarial auditor.
	// body is the FULL spool body, Reviewer notes included: the auditor needs
	// the "Already raised (dropped)" list to check the reviewers' citations.
	// PostReview strips the notes when posting (planReview), not here.
	var priorJSON []byte
	if prior != nil {
		priorJSON = prior.Raw
	}
	ar := callReviewAuditor(ctx, rec.Repo, rec.PR, verdict, body, diff, auditorModelFor(cfg), priorJSON)
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

	// Late catch: the auditor marks injected instructions it found in the
	// review body with a NOTES: INJECTION: line. That always goes to a human,
	// whatever verdict the auditor gave, since the verdict itself may be what
	// the injected text asked for.
	injectionReason := ""
	if note, ok := auditorInjectionNote(ar.meta.Answer); ok {
		if note == "" {
			note = "(no text quoted)"
		}
		note = truncateRunes(note, maxInjectionNoteRunes)
		recordAudit(audit, auditStep{step: "injection", status: "fail", detail: "caught late by auditor: " + note})
		injectionReason = "auditor flagged prompt injection in the review: " + note
		would = outcomePunted
	}

	// Step 3: apply trust mode. Dry-run never acts. own-repos acts only for
	// permitted owners. A would-be punch is a punch regardless of mode.
	if cfg.Mode == "" || cfg.Mode == ModeDryRun {
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "dry-run; would " + string(would)})
		return GateResult{Outcome: outcomeDryRun, WouldHaveDone: would, Reason: "dry-run; would " + string(would)}, nil
	}
	if injectionReason != "" {
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: string(outcomePunted) + " (auditor caught prompt injection)"})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePunted, Reason: injectionReason}, nil
	}
	if would == outcomePunted {
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: string(outcomePunted) + " (auditor NEEDS_HUMAN)"})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePunted, Reason: "auditor: " + ar.reason}, nil
	}
	if !cfg.ActsOn(rec.Repo) {
		// Dry-run was handled above, so this is own-repos with a repo outside
		// the allow-list — or an unrecognized mode, which fails closed.
		reason := "own-repos mode: " + repoOwner(rec.Repo) + " not in allow-list; punching"
		if cfg.Mode != ModeOwnRepos {
			reason = fmt.Sprintf("unrecognized auto-post mode %q; punching", cfg.Mode)
		}
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: reason})
		return GateResult{Outcome: outcomePunted, WouldHaveDone: would, Reason: reason}, nil
	}

	// Step 4: perform the real action.
	switch would {
	case outcomePosted:
		n, err := postReviewFunc(ctx, rec)
		if err != nil {
			recordAudit(audit, auditStep{step: "outcome", status: outcomeErrStatus(err), detail: "post failed: " + err.Error()})
			return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomePosted, Reason: "post failed: " + err.Error()}, err
		}
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "posted"})
		return GateResult{Outcome: outcomePosted, WouldHaveDone: outcomePosted, Reason: "auto-posted", CommentsPosted: n}, nil
	case outcomeDiscarded:
		if err := discardReviewFunc(rec); err != nil {
			recordAudit(audit, auditStep{step: "outcome", status: outcomeErrStatus(err), detail: "discard failed: " + err.Error()})
			return GateResult{Outcome: outcomePunted, WouldHaveDone: outcomeDiscarded, Reason: "discard failed: " + err.Error()}, err
		}
		recordAudit(audit, auditStep{step: "outcome", status: "pass", detail: "discarded"})
		return GateResult{Outcome: outcomeDiscarded, WouldHaveDone: outcomeDiscarded, Reason: "auto-discarded"}, nil
	}

	// Unreachable (would is one of posted/discarded/punted, punted handled above).
	return GateResult{Outcome: outcomePunted, WouldHaveDone: would, Reason: "no action"}, nil
}

// outcomeErrStatus is the audit status for a failed gate action: "fail" when a
// guard refused it (someone else finalized the review first), else "error".
func outcomeErrStatus(err error) string {
	if IsGuardStop(err) {
		return "fail"
	}
	return "error"
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

// readSpoolFileFunc reads the spool for its front-matter; a seam for tests.
var readSpoolFileFunc = os.ReadFile

// spoolGateInputs are the gate inputs taken from the spool front-matter.
type spoolGateInputs struct {
	reviewedSHA    string
	prior          *LoadedPriorFeedback
	language       string // the review's language lens; "generic" is forced to a human
	injectionGuard string // pr_review.py's prompt-injection scan result; "" for older spools
}

// readSpoolGateInputs re-reads the spool front-matter. reviewed_sha falls back
// to fallbackSHA (the record's LastReviewedSHA) when the front-matter lacks
// it. If the spool can't be re-read, the keys' presence is unknown, so both
// checks that depend on them fail closed: prior feedback is an unreadable
// file (not an older spool without one), and injection_guard is
// injectionGuardUnreadable.
func readSpoolGateInputs(path, fallbackSHA string) spoolGateInputs {
	in := spoolGateInputs{reviewedSHA: fallbackSHA}
	data, err := readSpoolFileFunc(path)
	if err != nil {
		in.prior = &LoadedPriorFeedback{Err: fmt.Errorf("re-read spool front-matter: %w", err)}
		in.injectionGuard = injectionGuardUnreadable
		return in
	}
	fm := ParseSpoolFrontMatter(data)
	in.prior = loadPriorFeedback(fm)
	if fm["reviewed_sha"] != "" {
		in.reviewedSHA = fm["reviewed_sha"]
	}
	in.language = fm["language"]
	in.injectionGuard = injectionGuardValue(fm)
	return in
}

// RunAutoPostGate is the default runAutoPostGateFunc seam implementation the
// watcher calls after a successful review. It gathers the inputs
// EvaluateAndMaybePost needs — the freshly-updated record (for SpoolPath and
// LastReviewedSHA), the spool body + verdict, the spool's reviewed_sha, and the
// PR diff — then runs the gate. It is exported so the manual
// `howmux review <URL>` path (internal/tui) can run the same gate as the
// watcher's automatic path.
//
// It is intentionally tolerant: a review already succeeded and landed in the
// spool, so a problem gathering gate inputs (record vanished, spool unreadable,
// diff fetch failed) must NOT undo that — it returns an error the caller logs,
// and the review simply stays in pending/ for a human (the safe default).
func RunAutoPostGate(ctx context.Context, rec Record, headSHA string, store StoreInterface, cfg AutoPostConfig) (GateResult, error) {
	// Re-load the record RunReview just updated, so SpoolPath / LastReviewedSHA
	// reflect the completed review.
	updated, found, err := store.Get(rec.Repo, rec.PR)
	if err != nil {
		return GateResult{}, fmt.Errorf("auto-post gate: reload record: %w", err)
	}
	if !found {
		return GateResult{}, fmt.Errorf("auto-post gate: record for %s#%d vanished after review", rec.Repo, rec.PR)
	}
	if updated.SpoolPath == "" {
		return GateResult{}, fmt.Errorf("auto-post gate: no spool path on record for %s#%d", rec.Repo, rec.PR)
	}

	body, info := ReadSpoolBody(updated.SpoolPath, "")
	if !info.Found {
		return GateResult{}, fmt.Errorf("auto-post gate: cannot read spool body for %s#%d: %s", rec.Repo, rec.PR, updated.SpoolPath)
	}

	// Front-matter inputs (reviewed_sha, prior feedback, language,
	// injection_guard) are loaded here so the guardrail stays pure; see
	// readSpoolGateInputs for the fail-closed rules.
	in := readSpoolGateInputs(updated.SpoolPath, updated.LastReviewedSHA)
	reviewedSHA, prior := in.reviewedSHA, in.prior
	cfg.Language = in.language
	cfg.InjectionGuard = in.injectionGuard

	// Fetch the PR diff to a temp file, then read it back. The guardrail's
	// anchoring check and the auditor both need it.
	parts := strings.SplitN(rec.Repo, "/", 2)
	if len(parts) != 2 {
		return GateResult{}, fmt.Errorf("auto-post gate: invalid repo %q", rec.Repo)
	}
	diffFile, err := os.CreateTemp("", fmt.Sprintf("autopost-diff-%d-*.diff", rec.PR))
	if err != nil {
		return GateResult{}, fmt.Errorf("auto-post gate: temp diff: %w", err)
	}
	diffPath := diffFile.Name()
	diffFile.Close()
	defer os.Remove(diffPath)

	if err := fetchDiffFunc(ctx, parts[0], parts[1], rec.PR, diffPath); err != nil {
		return GateResult{}, fmt.Errorf("auto-post gate: fetch diff: %w", err)
	}
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		return GateResult{}, fmt.Errorf("auto-post gate: read diff: %w", err)
	}

	return EvaluateAndMaybePost(ctx, updated, body, info.Verdict, headSHA, reviewedSHA, string(diffData), cfg, prior)
}
