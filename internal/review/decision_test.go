package review

import (
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
)

func TestDecideReviewAction(t *testing.T) {
	now := time.Now()
	reviewer := "test-reviewer"

	tests := []struct {
		name     string
		pr       github.PR
		rec      Record
		reviewer string
		want     ReviewAction
	}{
		{
			name: "PR merged → prune",
			pr: github.PR{
				State:      "MERGED",
				MergedAt:   &now,
				HeadRefOid: "abc123",
			},
			rec: Record{
				Repo:            "owner/repo",
				PR:              1,
				LastReviewedSHA: "old-sha",
			},
			reviewer: reviewer,
			want:     ActionPrune,
		},
		{
			name: "PR closed → prune",
			pr: github.PR{
				State:      "CLOSED",
				ClosedAt:   &now,
				HeadRefOid: "abc123",
			},
			rec: Record{
				Repo:            "owner/repo",
				PR:              1,
				LastReviewedSHA: "old-sha",
			},
			reviewer: reviewer,
			want:     ActionPrune,
		},
		{
			name: "never reviewed (empty LastReviewedSHA) + open → review",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "new-sha",
			},
			rec: Record{
				Repo:            "owner/repo",
				PR:              1,
				LastReviewedSHA: "", // Never reviewed
			},
			reviewer: reviewer,
			want:     ActionReview,
		},
		{
			name: "open + review requested + never serviced (empty LastServicedRequest) → review",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "current-sha",
				ReviewRequests: []github.ReviewRequest{
					{Login: reviewer},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "", // Never serviced a request
			},
			reviewer: reviewer,
			want:     ActionReview,
		},
		{
			name: "open + review requested + head SHA differs from LastServicedRequest → review",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "new-sha",
				ReviewRequests: []github.ReviewRequest{
					{Login: reviewer},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "different-sha",
			},
			reviewer: reviewer,
			want:     ActionReview,
		},
		{
			name: "open + review requested + head SHA matches LastServicedRequest → skip (already serviced)",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "same-sha",
				ReviewRequests: []github.ReviewRequest{
					{Login: reviewer},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "same-sha", // Already serviced at this SHA
			},
			reviewer: reviewer,
			want:     ActionSkip,
		},
		{
			name: "open + not review requested → skip",
			pr: github.PR{
				State:          "OPEN",
				HeadRefOid:     "current-sha",
				ReviewRequests: []github.ReviewRequest{}, // No review requests
			},
			rec: Record{
				Repo:            "owner/repo",
				PR:              1,
				LastReviewedSHA: "old-sha",
			},
			reviewer: reviewer,
			want:     ActionSkip,
		},
		{
			name: "open + review requested for different reviewer → skip",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "current-sha",
				ReviewRequests: []github.ReviewRequest{
					{Login: "different-reviewer"},
				},
			},
			rec: Record{
				Repo:            "owner/repo",
				PR:              1,
				LastReviewedSHA: "old-sha",
			},
			reviewer: reviewer,
			want:     ActionSkip,
		},
		{
			name: "open + review requested for team (slug match) → review",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "current-sha",
				ReviewRequests: []github.ReviewRequest{
					{Slug: "my-team", Name: "My Team"},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "",
			},
			reviewer: "my-team",
			want:     ActionReview,
		},
		{
			name: "open + review requested for team (name match) → review",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "current-sha",
				ReviewRequests: []github.ReviewRequest{
					{Slug: "team-slug", Name: "Test Team"},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "",
			},
			reviewer: "Test Team",
			want:     ActionReview,
		},
		{
			name: "case insensitive reviewer match",
			pr: github.PR{
				State:      "OPEN",
				HeadRefOid: "current-sha",
				ReviewRequests: []github.ReviewRequest{
					{Login: "Test-Reviewer"},
				},
			},
			rec: Record{
				Repo:                "owner/repo",
				PR:                  1,
				LastReviewedSHA:     "old-sha",
				LastServicedRequest: "",
			},
			reviewer: "test-reviewer", // Lowercase
			want:     ActionReview,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideReviewAction(tt.pr, tt.rec, tt.reviewer)
			if got != tt.want {
				t.Errorf("decideReviewAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecideReviewAction_StaleReviewingRecordIsStillReviewable documents and
// locks in the dedup invariant issue #115 depends on: decideReviewAction
// decides purely from LastReviewedSHA / LastServicedRequest (write-once, at
// the END of RunReview), never from Status. A record left on StatusReviewing
// by a crashed/interrupted review — which by definition never reached the
// completion write — must still be picked up as ActionReview on the next
// poll, exactly as if the review had never started.
func TestDecideReviewAction_StaleReviewingRecordIsStillReviewable(t *testing.T) {
	reviewer := "test-reviewer"

	t.Run("never-reviewed, crashed on first attempt", func(t *testing.T) {
		pr := github.PR{
			State:      "OPEN",
			HeadRefOid: "new-sha",
			ReviewRequests: []github.ReviewRequest{
				{Login: reviewer},
			},
		}
		rec := Record{
			Repo:   "owner/repo",
			PR:     1,
			Status: StatusReviewing, // stuck from a crashed first review
			// LastReviewedSHA / LastServicedRequest both empty: never completed
		}

		got := decideReviewAction(pr, rec, reviewer)
		if got != ActionReview {
			t.Errorf("decideReviewAction() = %v, want %v", got, ActionReview)
		}
	})

	t.Run("previously reviewed, crashed reviewing a NEW commit", func(t *testing.T) {
		pr := github.PR{
			State:      "OPEN",
			HeadRefOid: "new-sha",
			ReviewRequests: []github.ReviewRequest{
				{Login: reviewer},
			},
		}
		rec := Record{
			Repo:                "owner/repo",
			PR:                  2,
			Status:              StatusReviewing, // stuck from a crashed re-review
			LastReviewedSHA:     "old-sha",
			LastServicedRequest: "old-sha", // request for old-sha was serviced; new-sha's review crashed before completion
		}

		got := decideReviewAction(pr, rec, reviewer)
		if got != ActionReview {
			t.Errorf("decideReviewAction() = %v, want %v", got, ActionReview)
		}
	})

	// Contrast case: the two subtests above both reach ActionReview via a SHA
	// mismatch, so on their own they'd still pass even if decideReviewAction
	// started reading Status. This case pins the actual invariant — that the
	// decision is INVARIANT across Status — by running the identical PR +
	// record under every Status value and asserting the result never changes.
	// If a future change made the action depend on Status, one of these rows
	// would diverge and fail. See PR #116 review, finding 3.
	t.Run("action is invariant across Status value", func(t *testing.T) {
		pr := github.PR{
			State:      "OPEN",
			HeadRefOid: "new-sha",
			ReviewRequests: []github.ReviewRequest{
				{Login: reviewer},
			},
		}
		base := Record{
			Repo:                "owner/repo",
			PR:                  3,
			LastReviewedSHA:     "old-sha",
			LastServicedRequest: "old-sha", // request for new-sha is unserviced → ActionReview
		}

		for _, status := range []Status{StatusWatching, StatusReviewing, StatusReviewed, StatusDone} {
			rec := base
			rec.Status = status
			if got := decideReviewAction(pr, rec, reviewer); got != ActionReview {
				t.Errorf("decideReviewAction() with Status=%q = %v, want %v (action must not depend on Status)", status, got, ActionReview)
			}
		}
	})
}
