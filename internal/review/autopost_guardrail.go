// Package review (this file) implements the DETERMINISTIC half of the review
// auto-post gate — the Stage 4 "Guardrails" hook/sentinel layer, scoped to
// deciding whether a consolidated PR review is safe to post WITHOUT a human.
//
// Design: docs/design/review-auto-post-gate.md.
//
// This layer is pure (no I/O, no network, no LLM): given an already-loaded
// review body, its verdict, the head SHA it was generated against, the spool's
// recorded reviewed_sha, and the diff-anchoring map PostReview already computes,
// it runs a fixed set of deterministic checks and returns a structured result.
// It reuses the existing review helpers (parseVerdict, parseFindingLine,
// buildReviewPayload's anchoring via parseDiffCommentableLines) rather than
// re-implementing them, so the gate can never drift from what actually posts.
//
// Semantics are FAIL-CLOSED: a failed check does not mean the review is bad, it
// means a computer cannot be sure it is safe to post unsupervised, so the gate
// must punch to a human. The adversarial auditor (autopost_auditor.go) only
// runs when this layer is clean.
package review

import (
	"fmt"
	"regexp"
	"strings"
)

// gateCheck is the outcome of a single deterministic guardrail check.
type gateCheck struct {
	name   string // stable identifier, e.g. "verdict_parseable"
	passed bool
	detail string // human-readable reason, populated when !passed
}

// gateResult is the aggregate of all deterministic checks. clean is true iff
// every check passed. The checks slice is retained in run order so the audit
// trail can record exactly which check tripped.
type gateResult struct {
	clean  bool
	checks []gateCheck
}

// firstFailure returns the detail of the first failed check, or "" when clean.
// Used to compose a concise audit-trail reason.
func (r gateResult) firstFailure() string {
	for _, c := range r.checks {
		if !c.passed {
			return fmt.Sprintf("%s: %s", c.name, c.detail)
		}
	}
	return ""
}

// secretPathMarkers are conservative, obvious indicators that a review body
// leaked a machine-specific path or a credential-shaped token. Per the design
// (resolved question #2) this starts deliberately narrow — macOS/Linux home
// paths plus a few common key prefixes — and can be tightened later. A hit
// fails the deterministic gate (punch to human), it does not attempt redaction.
var secretPathMarkers = []*regexp.Regexp{
	regexp.MustCompile(`/Users/[^/\s]+/`),                    // macOS home path
	regexp.MustCompile(`/home/[^/\s]+/`),                     // Linux home path
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                   // AWS access key id
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),         // GitHub token prefixes
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), // PEM private key
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),       // Slack token
}

// evaluateAutoPostGuardrail runs the deterministic checks over an already-loaded
// review and returns the aggregate result. It performs NO I/O: the caller
// supplies everything.
//
//   - verdict:     the review's own verdict value (APPROVE/COMMENT/REQUEST_CHANGES/…)
//   - body:        the consolidated review markdown body
//   - headSHA:     the PR's current head SHA
//   - reviewedSHA: the spool's recorded reviewed_sha front-matter value
//   - commentable: diff-anchoring map from parseDiffCommentableLines; nil means
//     no diff was available, in which case the anchor check is SKIPPED-as-fail
//     (fail-closed: we cannot prove findings anchor without a diff)
//   - maxFindings: the finding-count cap (check #6)
//
// The checks, in order (all deterministic):
//  1. verdict_parseable  — parseVerdict(body) != "UNKNOWN"
//  2. findings_anchor     — every finding's file:line is on the diff's right side
//  3. sha_fresh           — reviewedSHA == headSHA (review not stale)
//  4. event_legal          — verdict's event is one we can post unsupervised
//  5. no_secrets           — body contains no machine paths / key-shaped tokens
//  6. finding_count         — finding count within [0, maxFindings]
func evaluateAutoPostGuardrail(verdict, body, headSHA, reviewedSHA string, commentable map[string]map[int]bool, maxFindings int) gateResult {
	var checks []gateCheck

	// 1. Verdict must be parseable. We accept the passed verdict value, but
	// also confirm the body itself yields a non-UNKNOWN verdict, since the two
	// should agree and a body with no VERDICT: line is not auto-postable.
	bodyVerdict := parseVerdict(body)
	verdictOK := bodyVerdict != "UNKNOWN" && strings.TrimSpace(verdict) != ""
	checks = append(checks, gateCheck{
		name:   "verdict_parseable",
		passed: verdictOK,
		detail: fmt.Sprintf("body verdict %q, record verdict %q", bodyVerdict, verdict),
	})

	// 2. Every finding must anchor to the diff. Reuse the exact anchoring logic
	// buildReviewPayload uses: parse findings, and for each, confirm its
	// file:line is a commentable right-side line. If commentable is nil we have
	// no diff to prove anchoring against — fail closed.
	anchorOK := true
	anchorDetail := ""
	if commentable == nil {
		anchorOK = false
		anchorDetail = "no diff available to verify finding anchoring"
	} else {
		var unanchored []string
		for _, line := range strings.Split(body, "\n") {
			f, ok := parseFindingLine(line)
			if !ok {
				continue
			}
			diffLines, fileInDiff := commentable[f.file]
			if !fileInDiff || !diffLines[f.line] {
				unanchored = append(unanchored, fmt.Sprintf("%s:%d", f.file, f.line))
			}
		}
		if len(unanchored) > 0 {
			anchorOK = false
			anchorDetail = "findings not on the diff: " + strings.Join(unanchored, ", ")
		}
	}
	checks = append(checks, gateCheck{name: "findings_anchor", passed: anchorOK, detail: anchorDetail})

	// 3. SHA freshness: the review must have been generated against the current
	// head. A stale review could comment on lines that have since changed.
	shaOK := reviewedSHA != "" && reviewedSHA == headSHA
	checks = append(checks, gateCheck{
		name:   "sha_fresh",
		passed: shaOK,
		detail: fmt.Sprintf("reviewed_sha %q vs head %q", reviewedSHA, headSHA),
	})

	// 4. Event legality. The verdict maps to a GitHub review event via
	// verdictEvent. COMMENT is always safe to auto-post. APPROVE and
	// REQUEST_CHANGES are rejected by GitHub on the poster's OWN PR (handled
	// reactively elsewhere by downgradeEventToComment), and carry more weight;
	// for an UNSUPERVISED post we require COMMENT so the gate never
	// auto-approves or auto-requests-changes. A stronger verdict punches to a
	// human, who can post the APPROVE/REQUEST_CHANGES themselves.
	event := verdictEvent(verdict)
	eventOK := event == "COMMENT"
	checks = append(checks, gateCheck{
		name:   "event_legal",
		passed: eventOK,
		detail: fmt.Sprintf("verdict %q → event %q (only COMMENT is auto-postable)", verdict, event),
	})

	// 5. Secret / machine-path scan over the whole body.
	secretsOK := true
	secretDetail := ""
	for _, re := range secretPathMarkers {
		if loc := re.FindString(body); loc != "" {
			secretsOK = false
			secretDetail = "body contains a machine path or key-shaped token"
			break
		}
	}
	checks = append(checks, gateCheck{name: "no_secrets", passed: secretsOK, detail: secretDetail})

	// 6. Finding-count sanity. Zero findings is fine (a clean APPROVE/COMMENT).
	// An unbounded count suggests a runaway or low-signal review that a human
	// should triage. maxFindings <= 0 disables the cap.
	count := 0
	for _, line := range strings.Split(body, "\n") {
		if _, ok := parseFindingLine(line); ok {
			count++
		}
	}
	countOK := maxFindings <= 0 || count <= maxFindings
	checks = append(checks, gateCheck{
		name:   "finding_count",
		passed: countOK,
		detail: fmt.Sprintf("%d findings (cap %d)", count, maxFindings),
	})

	clean := true
	for _, c := range checks {
		if !c.passed {
			clean = false
			break
		}
	}
	return gateResult{clean: clean, checks: checks}
}
