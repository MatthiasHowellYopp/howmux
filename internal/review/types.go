package review

import (
	"encoding/json"
	"fmt"
	"time"
)

// Status represents the current state of a PR review
type Status string

const (
	StatusWatching  Status = "watching"  // Enrolled, waiting for review trigger
	StatusReviewing Status = "reviewing" // Review in progress
	StatusReviewed  Status = "reviewed"  // Reviewed at a SHA, awaiting the next push (non-terminal)
	StatusDone      Status = "done"      // Terminal: PR merged/closed
)

// ReviewAction represents the action to take for a PR during a watch poll
type ReviewAction int

const (
	ActionSkip   ReviewAction = iota // Skip this PR (not requested or already serviced)
	ActionReview                     // Trigger a review
	ActionPrune                      // Remove from tracking (merged/closed)
)

// Record represents the persisted state for a single PR review
type Record struct {
	Repo                string `json:"repo"`                  // "owner/name"
	PR                  int    `json:"pr"`                    // Pull request number
	URL                 string `json:"url"`                   // Full PR URL
	Status              Status `json:"status"`                // Current review status
	LastReviewedSHA     string `json:"last_reviewed_sha"`     // Commit SHA last reviewed
	LastReviewedAt      string `json:"last_reviewed_at"`      // RFC3339 timestamp or empty
	LastServicedRequest string `json:"last_serviced_request"` // Head SHA at the time the last review request was serviced; used to avoid re-reviewing the same commit on every poll
	EnrolledAt          string `json:"enrolled_at"`           // RFC3339 timestamp
	ReviewDir           string `json:"review_dir"`            // Path to worktree
	SpoolPath           string `json:"spool_path"`            // Path to the review spool file (populated after successful review)
}

// Validate checks required fields and valid status
func (r *Record) Validate() error {
	if r.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	if r.PR <= 0 {
		return fmt.Errorf("pr must be positive")
	}
	if r.URL == "" {
		return fmt.Errorf("url is required")
	}
	if r.Status == "" {
		return fmt.Errorf("status is required")
	}
	if r.Status != StatusWatching && r.Status != StatusReviewing && r.Status != StatusReviewed && r.Status != StatusDone {
		return fmt.Errorf("invalid status: %s", r.Status)
	}
	if r.EnrolledAt == "" {
		return fmt.Errorf("enrolled_at is required")
	}
	// Validate RFC3339 timestamps
	if r.EnrolledAt != "" {
		if _, err := time.Parse(time.RFC3339, r.EnrolledAt); err != nil {
			return fmt.Errorf("enrolled_at must be RFC3339: %w", err)
		}
	}
	if r.LastReviewedAt != "" {
		if _, err := time.Parse(time.RFC3339, r.LastReviewedAt); err != nil {
			return fmt.Errorf("last_reviewed_at must be RFC3339: %w", err)
		}
	}
	return nil
}

// ToJSON serializes the record to JSON
func (r *Record) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// FromJSON deserializes JSON data into a record
func FromJSON(data []byte) (*Record, error) {
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
