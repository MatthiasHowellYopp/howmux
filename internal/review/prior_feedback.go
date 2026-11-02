// Package review (this file) models the prior-feedback snapshot pr_review.py
// writes before a review runs: earlier PR reviews, inline review threads, the
// PR conversation, and GitHub issues. Reviewers use it to avoid re-raising
// points that were already made; the auto-post gate uses it to check that
// they didn't (the not_already_raised guardrail and the auditor's input).
//
// pr_review.py (ai-resources) writes the file to
// ~/PR-Review/prior/pr-review-<owner>-<name>-<pr>.json and records its
// absolute path in the spool front-matter key prior_feedback. Older spools
// have no such key.
package review

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// priorFeedbackKey is the spool front-matter key holding the absolute path
// of the prior-feedback JSON file.
const priorFeedbackKey = "prior_feedback"

// PriorFeedback is the parsed prior-feedback JSON document.
type PriorFeedback struct {
	Repo         string         `json:"repo"`
	PR           int            `json:"pr"`
	FetchedAt    string         `json:"fetched_at"`
	IssueScope   string         `json:"issue_scope"`
	Errors       []string       `json:"errors"`
	Reviews      []PriorReview  `json:"reviews"`
	Threads      []PriorThread  `json:"threads"`
	Conversation []PriorComment `json:"conversation"`
	Issues       []PriorIssue   `json:"issues"`
}

// PriorReview is one submitted PR review.
type PriorReview struct {
	Author      string `json:"author"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
	Body        string `json:"body"`
}

// PriorThread is one inline review thread. Line is nil for file-level or
// otherwise unanchored threads.
type PriorThread struct {
	Path string `json:"path"`
	Line *int   `json:"line"`
	// StartLine is the first line of a multi-line comment (nil for a
	// single-line one, and in snapshots written before it was recorded).
	// Line is then the last line of the span.
	StartLine  *int                 `json:"start_line"`
	IsResolved bool                 `json:"is_resolved"`
	IsOutdated bool                 `json:"is_outdated"`
	Comments   []PriorThreadComment `json:"comments"`
}

// PriorThreadComment is one comment in an inline review thread.
type PriorThreadComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

// PriorComment is one PR conversation (issue-style) comment.
type PriorComment struct {
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// PriorIssue is one GitHub issue in the fetched scope. Linked is true when
// the PR references or closes it.
type PriorIssue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
	URL    string   `json:"url"`
	Body   string   `json:"body"`
	Linked bool     `json:"linked"`
}

// LoadedPriorFeedback is the gate's view of a spool's prior feedback, loaded
// once by RunAutoPostGate and passed to the (pure) guardrail and the auditor.
// A nil *LoadedPriorFeedback means the spool has no prior_feedback key (an
// older spool), which the guardrail treats as a pass.
type LoadedPriorFeedback struct {
	Path string         // the front-matter path
	Raw  []byte         // the file's bytes as read (nil when unreadable)
	Data *PriorFeedback // parsed document; nil when Err != nil
	Err  error          // non-nil when the key is set but the file is missing or unparseable
}

// loadPriorFeedback loads the prior-feedback file named by the spool
// front-matter fields. It returns nil only when the key is absent (a spool
// written before prior feedback existed). A key that is present but blank
// means pr_review.py ran the newer pipeline but the snapshot was never
// written, so it returns a LoadedPriorFeedback with Err set, as it does when
// the file can't be read or parsed; the guardrail fails closed on all three.
func loadPriorFeedback(fm map[string]string) *LoadedPriorFeedback {
	value, present := fm[priorFeedbackKey]
	if !present {
		return nil
	}
	path := strings.TrimSpace(value)
	if path == "" {
		return &LoadedPriorFeedback{Err: fmt.Errorf("prior_feedback is blank: the prior-feedback snapshot was not written")}
	}
	lp := &LoadedPriorFeedback{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		lp.Err = fmt.Errorf("read prior feedback %s: %w", path, err)
		return lp
	}
	lp.Raw = raw
	var pf PriorFeedback
	if err := json.Unmarshal(raw, &pf); err != nil {
		lp.Err = fmt.Errorf("parse prior feedback %s: %w", path, err)
		return lp
	}
	lp.Data = &pf
	return lp
}
