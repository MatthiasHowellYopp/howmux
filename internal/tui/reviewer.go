package tui

import (
	"fmt"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/github"
)

// getCurrentUserFunc is the injectable seam over github.GetCurrentUser for
// tests. Production code leaves this as the real call.
var getCurrentUserFunc = github.GetCurrentUser

// ResolveReviewer determines the GitHub identity used to match
// pr.IsReviewRequestedFor(reviewer) in the review watcher.
//
// Precedence:
//  1. cfg.Reviewer, if non-empty (explicit override always wins)
//  2. The currently authenticated gh user's login, resolved via
//     github.GetCurrentUser()
//  3. If neither is available, return an error — callers must not fall back
//     to a value (such as the repo slug) that can never match a real
//     reviewer identity.
func ResolveReviewer(cfg *config.Config) (string, error) {
	if cfg.Reviewer != "" {
		return cfg.Reviewer, nil
	}

	login, err := getCurrentUserFunc()
	if err != nil {
		return "", fmt.Errorf("failed to resolve reviewer identity: no reviewer override configured "+
			"(set 'reviewer:' in .howmux/config.yaml) and gh login resolution failed: %w", err)
	}

	return login, nil
}
