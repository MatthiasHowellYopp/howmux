package review

import "github.com/matthiashowellyopp/howmux/internal/github"

// decideReviewAction is a pure function that determines the action for a PR
// given its current GitHub state and stored record.
//
// Rules:
// - PR merged or closed → ActionPrune
// - No stored record / never reviewed → ActionReview
// - Open + review requested for me + request not already serviced → ActionReview
// - Open + not requested (or request already serviced) → ActionSkip
//
// Re-request deduplication: A review request is "already serviced" when
// rec.LastServicedRequest == pr.HeadSHA(). Only re-review when a review is
// requested AND the head SHA differs.
func decideReviewAction(pr github.PR, rec Record, reviewer string) ReviewAction {
	// Rule 1: PR is terminal (merged or closed) → prune from tracking
	if pr.IsTerminal() {
		return ActionPrune
	}

	// Rule 2: Never reviewed (empty LastReviewedSHA) → review
	if rec.LastReviewedSHA == "" {
		return ActionReview
	}

	// Rule 3: Open + review requested for me
	if pr.IsReviewRequestedFor(reviewer) {
		// Check if request already serviced at this SHA
		if rec.LastServicedRequest == pr.HeadSHA() {
			// Already serviced this request at this commit
			return ActionSkip
		}
		// New commit or first request → review
		return ActionReview
	}

	// Rule 4: Open but not requested → skip
	return ActionSkip
}
