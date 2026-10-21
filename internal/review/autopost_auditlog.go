// Package review (this file) writes the Stage 4 AUDIT TRAIL for the review
// auto-post gate: one JSON object per workflow step, appended as a line to a
// per-run .jsonl file under the PR-Review audit directory.
//
// Design: docs/design/review-auto-post-gate.md (Audit trail section).
//
// The record schema matches ai-resources/workflows/audit_log.py exactly, so
// the existing e2e_report.py success-rate tool can read howmux's runs without
// modification and the two pipelines' numbers stay comparable. In particular
// `tokens` and `cost` are ALWAYS null — kiro-cli reports neither in any output
// mode (see autopost_oneshot.go) — recorded as explicit null, never estimated.
//
// Each record() appends and flushes immediately (the file is opened, written,
// and closed per call), so a crash mid-run still leaves every completed step on
// disk — the exact scenario an audit trail exists for.
package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// auditPipeline is the pipeline name stamped on every record from this gate.
const auditPipeline = "review_auto_post"

// auditHomeDirFunc is the home-directory seam for audit-path resolution, kept
// local to this file (the review package has no shared home-dir seam of its
// own; the one referenced by spool.go lives in internal/tui). Overridable in
// tests.
var auditHomeDirFunc = os.UserHomeDir

// auditTimeNow is the time seam for record timestamps, overridable in tests.
var auditTimeNow = time.Now

// auditDir returns the directory the audit .jsonl files are written to:
// $PR_REVIEW_DIR/audit when PR_REVIEW_DIR is set (same env override
// audit_log.py honors), else ~/PR-Review/audit. Returns an error only if the
// home directory cannot be resolved and no override is set.
func auditDir() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PR_REVIEW_DIR")); root != "" {
		return filepath.Join(root, "audit"), nil
	}
	home, err := auditHomeDirFunc()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory for audit dir: %w", err)
	}
	return filepath.Join(home, "PR-Review", "audit"), nil
}

// auditRecord is one line of the audit trail, serialized to JSON. Field names
// and null-ness match audit_log.py's schema exactly. Pointer/`any` fields that
// are nil marshal to JSON null, which is the intended "not collected" signal
// for turn_duration_ms / context_usage_pct / tokens / cost.
type auditRecord struct {
	RunID           string  `json:"run_id"`
	Pipeline        string  `json:"pipeline"`
	Repo            string  `json:"repo"`
	PR              string  `json:"pr"`
	Step            string  `json:"step"`
	Iteration       *int    `json:"iteration"`         // always null for this gate (no loop)
	Agent           *string `json:"agent"`             // null for deterministic steps
	Model           *string `json:"model"`             // null when no --model passed
	Status          string  `json:"status"`            // "pass" | "fail" | "error"
	TurnDurationMs  *int64  `json:"turn_duration_ms"`  // null — not parsed from stream-json yet
	ContextUsagePct *int64  `json:"context_usage_pct"` // null — not parsed from stream-json yet
	DurationMs      int64   `json:"duration_ms"`       // wall-clock, always captured
	Tokens          *int64  `json:"tokens"`            // ALWAYS null — kiro-cli reports none
	Cost            *int64  `json:"cost"`              // ALWAYS null — kiro-cli reports none
	Detail          string  `json:"detail"`
	Timestamp       string  `json:"timestamp"` // ISO-8601 UTC
}

// AuditLog is one gate invocation's audit trail. All steps of one run share a
// RunID and land in the same .jsonl file. Construct with NewAuditLog.
type AuditLog struct {
	RunID string
	Repo  string
	PR    int
	path  string // resolved on first record(); "" until then
}

// NewAuditLog creates an audit log for one gate run. runID groups every step
// of this invocation; callers should pass a short unique id (see newRunID).
func NewAuditLog(runID, repo string, pr int) *AuditLog {
	return &AuditLog{RunID: runID, Repo: repo, PR: pr}
}

// auditStep is the typed set of arguments for one record() call, keeping the
// call sites readable (Go has no keyword args).
type auditStep struct {
	step   string
	status string // "pass" | "fail" | "error"
	agent  string // "" → null
	detail string
	meta   *OneshotResult // optional; supplies model + duration_ms when present
}

// record appends one step record to this run's .jsonl file and flushes it
// immediately. A write error is returned so callers can log it, but recording
// is best-effort: the gate's decision must not depend on the audit write
// succeeding (an unwritable audit dir should never change whether a review is
// posted). Callers therefore typically log-and-continue on error.
func (a *AuditLog) record(s auditStep) error {
	status := s.status
	if status != "pass" && status != "fail" && status != "error" {
		return fmt.Errorf("audit: status must be pass|fail|error, got %q", status)
	}

	dir, err := auditDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("audit: cannot create audit dir %s: %w", dir, err)
	}

	if a.path == "" {
		slug := strings.ReplaceAll(a.Repo, "/", "-")
		if slug == "" {
			slug = "unknown"
		}
		a.path = filepath.Join(dir, fmt.Sprintf("%s-%s-%d-%s.jsonl", auditPipeline, slug, a.PR, a.RunID))
	}

	var agentPtr *string
	if s.agent != "" {
		agentPtr = &s.agent
	}
	var modelPtr *string
	var durationMs int64
	if s.meta != nil {
		durationMs = s.meta.DurationMs
		if s.meta.Model != "" {
			m := s.meta.Model
			modelPtr = &m
		}
	}

	rec := auditRecord{
		RunID:           a.RunID,
		Pipeline:        auditPipeline,
		Repo:            a.Repo,
		PR:              fmt.Sprintf("%d", a.PR),
		Step:            s.step,
		Iteration:       nil,
		Agent:           agentPtr,
		Model:           modelPtr,
		Status:          status,
		TurnDurationMs:  nil,
		ContextUsagePct: nil,
		DurationMs:      durationMs,
		Tokens:          nil,
		Cost:            nil,
		Detail:          truncateDetail(s.detail),
		Timestamp:       auditTimeNow().UTC().Format("2006-01-02T15:04:05Z"),
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("audit: marshal failed: %w", err)
	}

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("audit: cannot open %s: %w", a.path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit: write failed: %w", err)
	}
	return nil
}

// truncateDetail caps the detail field so a runaway agent message can't bloat
// the audit file, matching audit_log.py's 500-char cap.
func truncateDetail(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// newRunID returns a short run identifier (hex, time-seeded) for grouping one
// gate invocation's steps. Not cryptographic — just unique enough per run.
func newRunID() string {
	return fmt.Sprintf("%012x", auditTimeNow().UnixNano()&0xffffffffffff)
}
