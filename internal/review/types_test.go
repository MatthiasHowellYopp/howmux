package review

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecordValidation(t *testing.T) {
	validRecord := Record{
		Repo:       "owner/name",
		PR:         123,
		URL:        "https://github.com/owner/name/pull/123",
		Status:     StatusWatching,
		EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir:  ".worktrees/review-owner-name-123",
	}

	t.Run("valid record passes", func(t *testing.T) {
		if err := validRecord.Validate(); err != nil {
			t.Errorf("valid record failed validation: %v", err)
		}
	})

	t.Run("missing repo", func(t *testing.T) {
		rec := validRecord
		rec.Repo = ""
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "repo is required") {
			t.Errorf("expected 'repo is required' error, got: %v", err)
		}
	})

	t.Run("zero PR number", func(t *testing.T) {
		rec := validRecord
		rec.PR = 0
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "pr must be positive") {
			t.Errorf("expected 'pr must be positive' error, got: %v", err)
		}
	})

	t.Run("negative PR number", func(t *testing.T) {
		rec := validRecord
		rec.PR = -5
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "pr must be positive") {
			t.Errorf("expected 'pr must be positive' error, got: %v", err)
		}
	})

	t.Run("missing URL", func(t *testing.T) {
		rec := validRecord
		rec.URL = ""
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "url is required") {
			t.Errorf("expected 'url is required' error, got: %v", err)
		}
	})

	t.Run("missing status", func(t *testing.T) {
		rec := validRecord
		rec.Status = ""
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "status is required") {
			t.Errorf("expected 'status is required' error, got: %v", err)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		rec := validRecord
		rec.Status = "invalid"
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "invalid status") {
			t.Errorf("expected 'invalid status' error, got: %v", err)
		}
	})

	t.Run("missing enrolled_at", func(t *testing.T) {
		rec := validRecord
		rec.EnrolledAt = ""
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "enrolled_at is required") {
			t.Errorf("expected 'enrolled_at is required' error, got: %v", err)
		}
	})

	t.Run("invalid enrolled_at format", func(t *testing.T) {
		rec := validRecord
		rec.EnrolledAt = "2026-09-16 10:00:00"
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "enrolled_at must be RFC3339") {
			t.Errorf("expected RFC3339 error for enrolled_at, got: %v", err)
		}
	})

	t.Run("invalid last_reviewed_at format", func(t *testing.T) {
		rec := validRecord
		rec.LastReviewedAt = "invalid-timestamp"
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "last_reviewed_at must be RFC3339") {
			t.Errorf("expected RFC3339 error for last_reviewed_at, got: %v", err)
		}
	})

	t.Run("valid last_reviewed_at", func(t *testing.T) {
		rec := validRecord
		rec.LastReviewedAt = time.Now().Format(time.RFC3339)
		if err := rec.Validate(); err != nil {
			t.Errorf("valid last_reviewed_at failed: %v", err)
		}
	})

	t.Run("empty last_reviewed_at is allowed", func(t *testing.T) {
		rec := validRecord
		rec.LastReviewedAt = ""
		if err := rec.Validate(); err != nil {
			t.Errorf("empty last_reviewed_at should be allowed: %v", err)
		}
	})

	t.Run("all status constants are valid", func(t *testing.T) {
		statuses := []Status{StatusWatching, StatusReviewing, StatusDone}
		for _, status := range statuses {
			rec := validRecord
			rec.Status = status
			if err := rec.Validate(); err != nil {
				t.Errorf("status %q failed validation: %v", status, err)
			}
		}
	})
}

func TestRecordJSON(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	original := Record{
		Repo:                "owner/name",
		PR:                  123,
		URL:                 "https://github.com/owner/name/pull/123",
		Status:              StatusWatching,
		LastReviewedSHA:     "abc123def456",
		LastReviewedAt:      now,
		LastServicedRequest: "request-id-123",
		EnrolledAt:          now,
		ReviewDir:           ".worktrees/review-owner-name-123",
	}

	t.Run("round-trip marshal/unmarshal", func(t *testing.T) {
		// Marshal
		data, err := original.ToJSON()
		if err != nil {
			t.Fatalf("ToJSON failed: %v", err)
		}

		// Unmarshal
		decoded, err := FromJSON(data)
		if err != nil {
			t.Fatalf("FromJSON failed: %v", err)
		}

		// Compare all fields
		if decoded.Repo != original.Repo {
			t.Errorf("Repo mismatch: got %q, want %q", decoded.Repo, original.Repo)
		}
		if decoded.PR != original.PR {
			t.Errorf("PR mismatch: got %d, want %d", decoded.PR, original.PR)
		}
		if decoded.URL != original.URL {
			t.Errorf("URL mismatch: got %q, want %q", decoded.URL, original.URL)
		}
		if decoded.Status != original.Status {
			t.Errorf("Status mismatch: got %q, want %q", decoded.Status, original.Status)
		}
		if decoded.LastReviewedSHA != original.LastReviewedSHA {
			t.Errorf("LastReviewedSHA mismatch: got %q, want %q", decoded.LastReviewedSHA, original.LastReviewedSHA)
		}
		if decoded.LastReviewedAt != original.LastReviewedAt {
			t.Errorf("LastReviewedAt mismatch: got %q, want %q", decoded.LastReviewedAt, original.LastReviewedAt)
		}
		if decoded.LastServicedRequest != original.LastServicedRequest {
			t.Errorf("LastServicedRequest mismatch: got %q, want %q", decoded.LastServicedRequest, original.LastServicedRequest)
		}
		if decoded.EnrolledAt != original.EnrolledAt {
			t.Errorf("EnrolledAt mismatch: got %q, want %q", decoded.EnrolledAt, original.EnrolledAt)
		}
		if decoded.ReviewDir != original.ReviewDir {
			t.Errorf("ReviewDir mismatch: got %q, want %q", decoded.ReviewDir, original.ReviewDir)
		}
	})

	t.Run("JSON field names match schema", func(t *testing.T) {
		data, err := original.ToJSON()
		if err != nil {
			t.Fatalf("ToJSON failed: %v", err)
		}

		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("Unmarshal to map failed: %v", err)
		}

		// Verify JSON field names
		expectedFields := []string{
			"repo", "pr", "url", "status",
			"last_reviewed_sha", "last_reviewed_at",
			"last_serviced_request", "enrolled_at", "review_dir",
		}

		for _, field := range expectedFields {
			if _, ok := raw[field]; !ok {
				t.Errorf("missing JSON field: %q", field)
			}
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := FromJSON([]byte("{invalid json"))
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("empty JSON returns error", func(t *testing.T) {
		_, err := FromJSON([]byte(""))
		if err == nil {
			t.Error("expected error for empty JSON, got nil")
		}
	})
}
