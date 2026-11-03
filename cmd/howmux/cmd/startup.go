package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

// checkAuditorModelFunc is a seam over review.CheckAuditorModel for tests.
var checkAuditorModelFunc = review.CheckAuditorModel

// checkReviewModelFunc is a seam over review.CheckReviewModel for tests.
var checkReviewModelFunc = review.CheckReviewModel

// validateAuditorModel checks review.auto_post.auditor_model against
// kiro-cli's model list before the TUI starts. An unknown model is a startup
// error: otherwise every auditor call fails and nothing ever auto-posts.
//
// If the list can't be read it returns a notice instead of an error, since the
// auditor still fails closed at runtime. The caller must show the notice in
// the TUI's activity pane (tui.Run's startup notices): the TUI runs on the
// alt screen, so anything printed to stderr here is hidden for the whole
// session.
func validateAuditorModel(ctx context.Context, cfg *config.Config) (notice string, err error) {
	if ctx == nil { // cmd.Context() is nil when RunE is called outside Execute
		ctx = context.Background()
	}
	err = checkAuditorModelFunc(ctx, cfg.Review.AutoPost.AuditorModel)
	if err == nil {
		return "", nil
	}
	if errors.Is(err, review.ErrModelListUnavailable) {
		return fmt.Sprintf("PR-review: %v — auto-post may punch every review to you until this is fixed", err), nil
	}
	return "", fmt.Errorf("invalid config: %w", err)
}

// validateReviewModel checks review.model the same way validateAuditorModel
// checks the auditor's model: an unknown model is a startup error (every
// review agent call would fail); an unreadable model list is a notice.
func validateReviewModel(ctx context.Context, cfg *config.Config) (notice string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	err = checkReviewModelFunc(ctx, cfg.Review.Model)
	if err == nil {
		return "", nil
	}
	if errors.Is(err, review.ErrModelListUnavailable) {
		return fmt.Sprintf("PR-review: %v — reviews may fail until this is fixed", err), nil
	}
	return "", fmt.Errorf("invalid config: %w", err)
}
