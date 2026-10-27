// Package review (this file) builds the auto-post gate's AGREEMENT REPORT: for
// each gate run, what the gate would have done versus what a human decided.
// This is the Phase 0 evidence the design asks for before trusting the gate
// with real posts (docs/design/review-auto-post-gate.md).
//
// Inputs:
//   - review_auto_post-*.jsonl: one file per gate run (autopost_gate.go)
//   - decisions/review_decision-*.jsonl: one file per human decision
//     (decision_audit.go)
//   - done/*.md: fallback for gate runs recorded before human decisions were
//     audited (runs with no "inputs" step). done/ keeps only the latest
//     decision per PR, so it is used only for the latest gate run on a PR, and
//     only if that review generation is the one the gate saw.
//
// Matching: a gate run is paired with the first successful human decision on
// the same PR at or after the run and before that PR's next gate run. When
// both sides know the reviewed_sha, they must agree.
package review

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Agreement classes for one gate run.
const (
	AgreementAgree      = "agree"          // gate and human chose the same action
	AgreementPermissive = "too_permissive" // gate would post; human did something else
	AgreementStrict     = "too_strict"     // gate would discard; human kept the review
	AgreementPunched    = "punched"        // gate deferred to a human (no call to grade)
	AgreementAuto       = "auto"           // gate acted for real; no human decision to compare
	AgreementUndecided  = "undecided"      // no human decision found yet
	AgreementIncomplete = "incomplete"     // gate run has no guardrail/auditor result
)

// Gate "would" values as they appear in the report.
const (
	gateWouldPost    = "post"
	gateWouldDiscard = "discard"
	gateWouldPunch   = "punch"
)

// AgreementRow is one gate run paired with the human decision (if any).
type AgreementRow struct {
	RunID          string    `json:"run_id"`
	Repo           string    `json:"repo"`
	PR             string    `json:"pr"`
	Time           time.Time `json:"time"`
	Verdict        string    `json:"verdict,omitempty"`
	ReviewedSHA    string    `json:"reviewed_sha,omitempty"`
	GateWould      string    `json:"gate_would,omitempty"` // post | discard | punch
	GateMode       string    `json:"gate_mode"`            // dry-run | acted | punched | error
	PunchReason    string    `json:"punch_reason,omitempty"`
	HumanDecision  string    `json:"human_decision,omitempty"`
	DecisionSource string    `json:"decision_source,omitempty"` // audit | done
	Class          string    `json:"class"`

	hasInputs bool // run recorded an "inputs" step (written since decisions are audited)
}

// AgreementReport is the full report. AgreementRate is agree / (agree +
// too_permissive + too_strict); it is -1 when there is nothing to grade yet.
type AgreementReport struct {
	Rows          []AgreementRow `json:"rows"`
	Counts        map[string]int `json:"counts"`
	Graded        int            `json:"graded"`
	AgreementRate float64        `json:"agreement_rate"`
}

// auditLine is the subset of an audit record the report reads.
type auditLine struct {
	RunID     string `json:"run_id"`
	Pipeline  string `json:"pipeline"`
	Repo      string `json:"repo"`
	PR        string `json:"pr"`
	Step      string `json:"step"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Timestamp string `json:"timestamp"`
}

type decisionEvent struct {
	time        time.Time
	decision    string
	reviewedSHA string
}

// ReviewRootDir returns the PR-Review root: $PR_REVIEW_DIR if set, else
// ~/PR-Review. The audit and done directories live under it.
func ReviewRootDir() (string, error) {
	dir, err := auditDir()
	if err != nil {
		return "", err
	}
	return filepath.Dir(dir), nil
}

// BuildAgreementReport reads the audit trail under root/audit (and done/ for
// legacy runs) and pairs every gate run with a human decision.
func BuildAgreementReport(root string) (AgreementReport, error) {
	auditPath := filepath.Join(root, "audit")
	gateLines, err := readAuditPipeline(auditPath, auditPipeline)
	if err != nil {
		return AgreementReport{}, err
	}
	decisionLines, err := readAuditPipeline(filepath.Join(auditPath, decisionSubdir), decisionPipeline)
	if err != nil {
		return AgreementReport{}, err
	}

	rows := buildGateRows(gateLines)
	decisions := buildDecisionEvents(decisionLines)

	// Group row indexes by PR, in time order, to compute each run's window.
	byPR := map[string][]int{}
	for i, r := range rows {
		k := prKey(r.Repo, r.PR)
		byPR[k] = append(byPR[k], i)
	}
	for k, idxs := range byPR {
		for n, i := range idxs {
			var next time.Time // zero = no later run
			if n+1 < len(idxs) {
				next = rows[idxs[n+1]].Time
			}
			r := &rows[i]
			if d, ok := matchDecision(decisions[k], r.Time, next, r.ReviewedSHA); ok {
				r.HumanDecision = d.decision
				r.DecisionSource = "audit"
			} else if n+1 == len(idxs) && !r.hasInputs {
				// done/ fallback only for runs recorded before human decisions
				// were audited (no inputs step). A newer run with no audited
				// decision is simply undecided; a done/ file from an earlier
				// generation must not be graded against it.
				var prev time.Time
				if n > 0 {
					prev = rows[idxs[n-1]].Time
				}
				if dec, ok := legacyDoneDecision(root, r.Repo, r.PR, prev, r.Time, r.ReviewedSHA); ok {
					r.HumanDecision = dec
					r.DecisionSource = "done"
				}
			}
			r.Class = classifyAgreement(*r)
		}
	}

	rep := AgreementReport{Rows: rows, Counts: map[string]int{}, AgreementRate: -1}
	for _, r := range rows {
		rep.Counts[r.Class]++
	}
	rep.Graded = rep.Counts[AgreementAgree] + rep.Counts[AgreementPermissive] + rep.Counts[AgreementStrict]
	if rep.Graded > 0 {
		rep.AgreementRate = float64(rep.Counts[AgreementAgree]) / float64(rep.Graded)
	}
	return rep, nil
}

// readAuditPipeline reads every record from <dir>/<pipeline>-*.jsonl. A
// missing directory is not an error (no runs yet); malformed lines are skipped.
func readAuditPipeline(dir, pipeline string) ([]auditLine, error) {
	files, err := filepath.Glob(filepath.Join(dir, pipeline+"-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("agreement: glob %s: %w", dir, err)
	}
	var out []auditLine
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, fmt.Errorf("agreement: open %s: %w", f, err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var l auditLine
			if json.Unmarshal(sc.Bytes(), &l) != nil || l.Pipeline != pipeline || l.RunID == "" {
				continue
			}
			out = append(out, l)
		}
		err = sc.Err()
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("agreement: read %s: %w", f, err)
		}
	}
	return out, nil
}

// buildGateRows folds gate audit lines into one row per run, sorted by time.
func buildGateRows(lines []auditLine) []AgreementRow {
	byRun := map[string]*AgreementRow{}
	var order []string
	for _, l := range lines {
		r, ok := byRun[l.RunID]
		if !ok {
			r = &AgreementRow{RunID: l.RunID, Repo: l.Repo, PR: l.PR}
			byRun[l.RunID] = r
			order = append(order, l.RunID)
		}
		if t, err := time.Parse(time.RFC3339, l.Timestamp); err == nil && (r.Time.IsZero() || t.Before(r.Time)) {
			r.Time = t
		}
		switch l.Step {
		case "inputs":
			r.hasInputs = true
			kv := parseKV(l.Detail)
			r.Verdict = kv["verdict"]
			r.ReviewedSHA = kv["reviewed_sha"]
		case "guardrail":
			if l.Status != "pass" {
				r.GateWould = gateWouldPunch
				r.PunchReason = l.Detail
			}
		case "auditor":
			switch {
			case strings.HasPrefix(l.Detail, string(auditorPostOK)):
				r.GateWould = gateWouldPost
			case strings.HasPrefix(l.Detail, string(auditorDiscard)):
				r.GateWould = gateWouldDiscard
			default:
				r.GateWould = gateWouldPunch
				r.PunchReason = l.Detail
			}
		case "outcome":
			switch {
			case l.Status == "error":
				r.GateMode = "error"
			case strings.HasPrefix(l.Detail, "dry-run"):
				r.GateMode = "dry-run"
			case l.Detail == string(outcomePosted) || l.Detail == string(outcomeDiscarded):
				r.GateMode = "acted"
			default:
				r.GateMode = "punched"
			}
		}
	}
	rows := make([]AgreementRow, 0, len(order))
	for _, id := range order {
		rows = append(rows, *byRun[id])
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Time.Before(rows[j].Time) })
	return rows
}

// buildDecisionEvents groups human decisions by PR, sorted by time.
func buildDecisionEvents(lines []auditLine) map[string][]decisionEvent {
	out := map[string][]decisionEvent{}
	for _, l := range lines {
		// Only completed actions count. A failed attempt (status "error") is
		// usually followed by a retry or a different decision, and that is the
		// one that reflects what the human did.
		if l.Step != decisionStep || l.Status != "pass" {
			continue
		}
		t, err := time.Parse(time.RFC3339, l.Timestamp)
		if err != nil {
			continue
		}
		kv := parseKV(l.Detail)
		if kv["decision"] == "" {
			continue
		}
		k := prKey(l.Repo, l.PR)
		out[k] = append(out[k], decisionEvent{time: t, decision: kv["decision"], reviewedSHA: kv["reviewed_sha"]})
	}
	for k := range out {
		ev := out[k]
		sort.SliceStable(ev, func(i, j int) bool { return ev[i].time.Before(ev[j].time) })
	}
	return out
}

// matchDecision returns the first decision at or after start and before next
// (zero next = open-ended) whose reviewed_sha is compatible with sha.
func matchDecision(events []decisionEvent, start, next time.Time, sha string) (decisionEvent, bool) {
	for _, e := range events {
		if e.time.Before(start) {
			continue
		}
		if !next.IsZero() && !e.time.Before(next) {
			break
		}
		if sha != "" && e.reviewedSHA != "" && sha != e.reviewedSHA {
			continue
		}
		return e, true
	}
	return decisionEvent{}, false
}

// legacyDoneDecision reads the PR's done/ file and returns its decision when
// that file is the review generation the gate run at runTime saw: generated
// after the previous gate run (prev, zero = none) and no later than runTime
// (plus a second of timestamp-rounding slack), with a matching reviewed_sha
// when the run recorded one.
func legacyDoneDecision(root, repo, pr string, prev, runTime time.Time, sha string) (string, bool) {
	name := fmt.Sprintf("pr-review-%s-%s.md", strings.ReplaceAll(repo, "/", "-"), pr)
	data, err := os.ReadFile(filepath.Join(root, "done", name))
	if err != nil {
		return "", false
	}
	fm := ParseSpoolFrontMatter(data)
	dec := strings.ToLower(strings.TrimSpace(fm["decision"]))
	if dec == "" {
		return "", false
	}
	if sha != "" && fm["reviewed_sha"] != "" && fm["reviewed_sha"] != sha {
		return "", false
	}
	gen, err := time.Parse(time.RFC3339, strings.TrimSpace(fm["generated"]))
	if err != nil {
		return "", false
	}
	if gen.After(runTime.Add(time.Second)) || (!prev.IsZero() && gen.Before(prev)) {
		return "", false
	}
	return dec, true
}

// classifyAgreement grades one row.
func classifyAgreement(r AgreementRow) string {
	switch {
	case r.GateWould == "":
		return AgreementIncomplete
	case r.GateMode == "acted":
		return AgreementAuto
	case r.GateWould == gateWouldPunch:
		return AgreementPunched
	case r.HumanDecision == "":
		return AgreementUndecided
	case r.GateWould == r.HumanDecision:
		return AgreementAgree
	case r.GateWould == gateWouldPost:
		return AgreementPermissive
	default: // would discard; human posted, revised or re-reviewed
		return AgreementStrict
	}
}

func prKey(repo, pr string) string { return strings.ToLower(repo) + "#" + pr }

// WriteAgreementText renders the report as a plain-text table plus a summary.
func WriteAgreementText(w io.Writer, rep AgreementReport) {
	if len(rep.Rows) == 0 {
		fmt.Fprintln(w, "No auto-post gate runs found.")
		return
	}
	fmt.Fprintf(w, "%-20s  %-26s  %-15s  %-7s  %-8s  %-14s  %s\n",
		"TIME (UTC)", "PR", "VERDICT", "GATE", "HUMAN", "CLASS", "NOTE")
	for _, r := range rep.Rows {
		human := r.HumanDecision
		if human == "" {
			human = "-"
		} else if r.DecisionSource == "done" {
			human += "*"
		}
		verdict := r.Verdict
		if verdict == "" {
			verdict = "-"
		}
		gate := r.GateWould
		if gate == "" {
			gate = "-"
		}
		note := r.PunchReason
		if len([]rune(note)) > 60 {
			note = string([]rune(note)[:57]) + "..."
		}
		fmt.Fprintf(w, "%-20s  %-26s  %-15s  %-7s  %-8s  %-14s  %s\n",
			r.Time.UTC().Format("2006-01-02 15:04:05"), r.Repo+"#"+r.PR, verdict, gate, human, r.Class, note)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Runs: %d", len(rep.Rows))
	for _, c := range []string{AgreementAgree, AgreementPermissive, AgreementStrict, AgreementPunched, AgreementAuto, AgreementUndecided, AgreementIncomplete} {
		if n := rep.Counts[c]; n > 0 {
			fmt.Fprintf(w, "  %s=%d", c, n)
		}
	}
	fmt.Fprintln(w)
	if rep.AgreementRate < 0 {
		fmt.Fprintln(w, "Agreement: n/a (no graded runs yet)")
	} else {
		fmt.Fprintf(w, "Agreement: %.0f%% (%d of %d graded runs)\n",
			rep.AgreementRate*100, rep.Counts[AgreementAgree], rep.Graded)
	}
	if rep.Counts[AgreementPermissive] > 0 {
		fmt.Fprintln(w, "Warning: too_permissive runs are ones the gate would have posted but a human did not.")
	}
	for _, r := range rep.Rows {
		if r.DecisionSource == "done" {
			fmt.Fprintln(w, "* decision taken from done/ (recorded before decisions were audited)")
			break
		}
	}
}
