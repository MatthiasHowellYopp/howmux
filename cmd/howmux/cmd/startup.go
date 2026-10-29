package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

// checkAuditorModelFunc is a seam over review.CheckAuditorModel for tests.
var checkAuditorModelFunc = review.CheckAuditorModel

// validateAuditorModel checks review.auto_post.auditor_model against
// kiro-cli's model list before the TUI starts. An unknown model is a startup
// error: otherwise every auditor call fails and nothing ever auto-posts. If
// the list can't be read, it only warns on warnOut, since the auditor still
// fails closed at runtime.
func validateAuditorModel(ctx context.Context, cfg *config.Config, warnOut io.Writer) error {
	if ctx == nil { // cmd.Context() is nil when RunE is called outside Execute
		ctx = context.Background()
	}
	err := checkAuditorModelFunc(ctx, cfg.Review.AutoPost.AuditorModel)
	if err == nil {
		return nil
	}
	if errors.Is(err, review.ErrModelListUnavailable) {
		fmt.Fprintf(warnOut, "Warning: %v\n", err)
		return nil
	}
	return fmt.Errorf("invalid config: %w", err)
}
