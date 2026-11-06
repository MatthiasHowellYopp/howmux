package tui

import (
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newModel must hand review.model to the review package; without it every
// agent step falls back to kiro-cli's "auto" (the bug PR #149 re-lands the
// fix for).
func TestNewModel_WiresReviewModel(t *testing.T) {
	orig := review.ReviewModel()
	t.Cleanup(func() { review.SetReviewModel(orig) })
	review.SetReviewModel("")

	cfg := &config.Config{LoadedTheme: createMinimalTheme()}
	cfg.Review.Model = "claude-sonnet-4.6"
	_ = newModel(nil, agent.NewManager(cfg), cfg, nil, nil, "")

	if got := review.ReviewModel(); got != "claude-sonnet-4.6" {
		t.Errorf("review.ReviewModel() = %q after newModel, want claude-sonnet-4.6", got)
	}
}
