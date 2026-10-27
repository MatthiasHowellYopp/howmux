// Package review (this file) records HUMAN review decisions into the audit
// trail, so the auto-post gate's dry-run calls can be compared against what a
// person actually did (see agreement.go).
//
// The spool's done/ directory keeps one file per PR and a re-review overwrites
// it, so it can't serve as decision history. These records are append-only:
// one .jsonl file per decision, with pipeline "review_decision" and the same
// schema as audit_log.py. They go in the audit directory's decisions/
// subdirectory rather than its top level: e2e_report.py treats every top-level
// .jsonl as a pipeline run, and a human decision isn't one.
package review

import (
	"os"
	"strings"
	"time"
)

// decisionPipeline is the pipeline name stamped on human-decision records.
const decisionPipeline = "review_decision"

// decisionSubdir is the audit subdirectory human-decision records live in.
const decisionSubdir = "decisions"

// decisionStep is the step name of the single record each decision writes.
const decisionStep = "human_decision"

// HumanDecision captures the state of a review at the moment a person decided
// on it. Create it with BeginHumanDecision BEFORE running the action (post and
// discard move the spool to done/; revise and rereview rewrite it), then call
// Finish with the action's result.
type HumanDecision struct {
	rec         Record
	decision    string
	verdict     string
	reviewedSHA string
	start       time.Time // when the human decided; the record is stamped with it
}

// BeginHumanDecision snapshots the review's verdict and reviewed_sha for a
// human decision ("post", "revise", "rereview", "discard"). Missing spool data
// is not an error: the fields are just left blank in the record.
func BeginHumanDecision(rec Record, decision string) *HumanDecision {
	hd := &HumanDecision{rec: rec, decision: decision, start: auditTimeNow()}
	path, found, _ := resolveSpoolPath(rec.SpoolPath)
	if !found {
		hd.reviewedSHA = rec.LastReviewedSHA
		return hd
	}
	data, err := os.ReadFile(path)
	if err != nil {
		hd.reviewedSHA = rec.LastReviewedSHA
		return hd
	}
	fm := ParseSpoolFrontMatter(data)
	hd.verdict = strings.ToUpper(strings.TrimSpace(fm["verdict"]))
	hd.reviewedSHA = fm["reviewed_sha"]
	if hd.reviewedSHA == "" {
		hd.reviewedSHA = rec.LastReviewedSHA
	}
	return hd
}

// Finish writes the decision record. actErr is the action's error, if any: the
// record still captures what the human chose, with status "error". Like the
// gate's audit writes, this is best-effort and never affects the action.
func (hd *HumanDecision) Finish(actErr error) {
	if hd == nil {
		return
	}
	audit := NewAuditLog(newRunID(), hd.rec.Repo, hd.rec.PR)
	audit.pipeline = decisionPipeline
	audit.subdir = decisionSubdir

	status := "pass"
	detail := formatKV("decision", hd.decision, "verdict", hd.verdict, "reviewed_sha", hd.reviewedSHA)
	if actErr != nil {
		status = "error"
		detail += decisionErrorSep + actErr.Error()
	}
	// Stamp the record with the decision time, not the finish time: revise and
	// rereview can run for minutes, and a gate run started meanwhile (after a
	// push) must not capture a decision made on the previous review.
	meta := &OneshotResult{DurationMs: auditTimeNow().Sub(hd.start).Milliseconds()}
	recordAudit(audit, auditStep{step: decisionStep, status: status, detail: detail, meta: meta, at: hd.start})
}

// decisionErrorSep separates the key=value fields from a free-text error.
const decisionErrorSep = " error: "

// formatKV renders key/value pairs as "k1=v1 k2=v2". Empty values are written
// as "-" so every key stays present and parseKV stays positional-free. Values
// must not contain spaces (verdicts, SHAs and decision names never do).
func formatKV(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		v := pairs[i+1]
		if v == "" {
			v = "-"
		}
		b.WriteString(pairs[i])
		b.WriteByte('=')
		b.WriteString(v)
	}
	return b.String()
}

// parseKV is formatKV's inverse. Anything after decisionErrorSep is ignored,
// tokens without "=" are skipped, and "-" values come back as "".
func parseKV(s string) map[string]string {
	if i := strings.Index(s, decisionErrorSep); i >= 0 {
		s = s[:i]
	}
	out := map[string]string{}
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k == "" {
			continue
		}
		if v == "-" {
			v = ""
		}
		out[k] = v
	}
	return out
}
