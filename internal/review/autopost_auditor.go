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
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
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

// auditorNotesLineRe matches the persona's bare "NOTES: ..." line. It uses
// [ \t] rather than \s so a bare "NOTES:" can't swallow the following line.
var auditorNotesLineRe = regexp.MustCompile(`(?im)^[ \t]*NOTES:[ \t]*(.*?)[ \t]*$`)

// auditorRationale extracts the auditor's reasoning for the audit trail:
// the NOTES: text first (it names the specific finding to scrutinize, so it
// survives the 500-char audit cap; multiple non-empty NOTES: lines are joined
// with "; "), then the free-text rationale with the NOTES:/VERDICT: lines
// removed and whitespace collapsed. Returns "" if the answer carries neither.
func auditorRationale(answer string) string {
	var parts []string
	for _, m := range auditorNotesLineRe.FindAllStringSubmatch(answer, -1) {
		if m[1] != "" {
			parts = append(parts, m[1])
		}
	}
	notes := strings.Join(parts, "; ")
	rest := auditorNotesLineRe.ReplaceAllString(answer, "")
	rest = auditorVerdictLineRe.ReplaceAllString(rest, "")
	rest = strings.Join(strings.Fields(rest), " ")

	switch {
	case notes != "" && rest != "":
		return "NOTES: " + notes + " | " + rest
	case notes != "":
		return "NOTES: " + notes
	default:
		return rest
	}
}

// auditorInjectionMarker is the prefix the review-auditor persona puts at the
// start of its NOTES: content when the review body contains an injected
// instruction, followed by the quoted text. It is matched case-sensitively to
// the persona contract, which spells it in upper case: a note that begins
// with lower-case "injection: …" is ordinary prose about a finding (say, a
// SQL injection the review reports), not the auditor's alarm.
const auditorInjectionMarker = "INJECTION:"

// maxInjectionNoteRunes caps the auditor's injection note in the audit detail
// and the gate's Reason, so the quoted text cannot crowd out the rest.
const maxInjectionNoteRunes = 300

// auditorInjectionNote reports whether any NOTES: line in the auditor's answer
// starts (after leading spaces) with auditorInjectionMarker, and returns the
// text after the marker, trimmed. The first marked NOTES: line wins. The
// marker anywhere else — later in a NOTES: line, or outside NOTES: — does not
// count: the contract places it at the start so that quoted review text
// mentioning the word cannot trigger or suppress it.
func auditorInjectionNote(answer string) (string, bool) {
	for _, m := range auditorNotesLineRe.FindAllStringSubmatch(answer, -1) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(m[1]), auditorInjectionMarker); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// truncateRunes cuts s to at most n runes, appending "…" when it cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

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

// maxPriorFeedbackPromptBytes caps the prior-feedback JSON embedded in the
// auditor prompt, so a PR with a long history can't crowd out the review and
// the diff.
const maxPriorFeedbackPromptBytes = 40000

// priorFeedbackTruncatedNote is appended when the prior-feedback JSON is cut
// at maxPriorFeedbackPromptBytes.
const priorFeedbackTruncatedNote = "\n…[truncated]"

// buildAuditorPrompt composes the prompt sent to the review-auditor agent. It
// supplies the inputs the agent contract specifies: repo, pr, the review's own
// verdict, the consolidated review body (including its Reviewer notes, so the
// auditor can check the "Already raised (dropped)" citations), the PR diff
// (ground truth), and — when the spool has one — the prior-feedback JSON
// (earlier reviews, threads, conversation, issues), capped at
// maxPriorFeedbackPromptBytes. The decision rubric and output contract live in
// the agent persona (ai-resources), so this prompt only supplies data and
// restates the output contract tersely as a guard against a drifting persona.
func buildAuditorPrompt(repo string, pr int, verdict, body, diff string, priorJSON []byte) string {
	priorBlock := ""
	if len(bytes.TrimSpace(priorJSON)) > 0 {
		pj := priorJSON
		note := ""
		if len(pj) > maxPriorFeedbackPromptBytes {
			pj = truncateUTF8(pj, maxPriorFeedbackPromptBytes)
			note = priorFeedbackTruncatedNote
		}
		priorBlock = fmt.Sprintf(`

--- BEGIN PRIOR FEEDBACK ---
%s%s
--- END PRIOR FEEDBACK ---`, pj, note)
	}
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
--- END PR DIFF ---%s

Decide per your review-auditor rubric. End with a single final line exactly:
VERDICT: <POST_OK|NEEDS_HUMAN|DISCARD>`, repo, pr, verdict, body, diff, priorBlock)
}

// truncateUTF8 cuts b to at most n bytes without splitting a UTF-8 rune.
func truncateUTF8(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return b[:cut]
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
// (autopost_gate.go). It performs no spool or GitHub I/O. priorJSON is the
// prior-feedback file's raw bytes (nil/empty when the spool has none).
//
// Fail-closed cases, all → NEEDS_HUMAN:
//   - kiro-cli not on PATH (agent not runnable)
//   - kiroOneshotMeta error (non-zero exit / empty output)
//   - the answer has no well-formed VERDICT: line
func callReviewAuditor(ctx context.Context, repo string, pr int, verdict, body, diff, model string, priorJSON []byte) auditorResult {
	// Preflight: the agent runs via kiro-cli. If kiro-cli isn't resolvable,
	// the auditor cannot run — fail closed without attempting the call.
	if _, err := lookPathForDecideActionsFunc("kiro-cli"); err != nil {
		return auditorResult{
			verdict: auditorNeedsHuman,
			meta:    OneshotResult{Status: "error"},
			reason:  "kiro-cli not on PATH; auditor could not run (fail-closed to NEEDS_HUMAN)",
		}
	}

	prompt := buildAuditorPrompt(repo, pr, verdict, body, diff, priorJSON)
	meta, err := kiroOneshotMeta(ctx, reviewAuditorAgent, model, prompt)
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

	reason := auditorRationale(meta.Answer)
	if reason == "" {
		reason = "(auditor gave no rationale)"
	}
	return auditorResult{verdict: v, meta: meta, reason: reason}
}
