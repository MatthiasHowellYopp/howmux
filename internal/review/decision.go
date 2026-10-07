package review

import "github.com/matthiashowellyopp/howmux/internal/github"

// decideReviewAction is a pure function that determines the action for a PR
// given its current GitHub state and stored record.
//
// Rules:
// - PR merged or closed → ActionPrune
// - Previously classified unsupported-language → ActionSkip (unless terminal, which still prunes above)
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

	// Rule 1b: Previously classified as unsupported-language → skip.
	// This is a deliberate, documented exception to this function's
	// general "does not read Status" design: StatusUnsupported is the one
	// Status value decideReviewAction must read, specifically to stop the
	// silent retry loop described in issue #123 — a record stuck here has
	// (and will keep) an empty LastReviewedSHA, so without this check
	// Rule 2 below would re-dispatch ActionReview on every poll forever.
	// Must stay AFTER the terminal/prune check above (a merged/closed PR
	// must still be pruned even if it was unsupported) and BEFORE Rule 2
	// (which would otherwise re-fire on the permanently-empty
	// LastReviewedSHA).
	if rec.Status == StatusUnsupported {
		return ActionSkip
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
