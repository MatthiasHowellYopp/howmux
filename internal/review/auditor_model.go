// Package review (this file) validates the configured review-auditor model
// against the models kiro-cli actually offers, at startup.
//
// Why: config.Load only checks that auditor_model is shaped like a model id.
// A typo (e.g. "calude-sonnet-5.5") passes that, and every auditor call then
// fails at runtime. The gate treats a failed auditor as NEEDS_HUMAN, so the
// only symptom is that nothing ever auto-posts. Checking the real model list
// at startup turns that silent punch into a loud error.
package review

import (
	"context"
	"errors"

	"github.com/matthiashowellyopp/howmux/internal/kirocli"
)

// ErrModelListUnavailable means kiro-cli's model list could not be read, so
// the configured model could not be checked either way. Callers should warn
// and carry on: the auditor call itself still fails closed (punch to human).
var ErrModelListUnavailable = errors.New("could not list kiro-cli models")

// listModelsFunc is a seam over kirocli.ListModels for tests.
var listModelsFunc = kirocli.ListModels

// CheckAuditorModel returns an error if model is set but kiro-cli does not
// offer it. An empty model (kiro-cli's default) is always valid. If the model
// list can't be read, the error wraps ErrModelListUnavailable.
func CheckAuditorModel(ctx context.Context, model string) error {
	return checkModel(ctx, "review.auto_post.auditor_model", model,
		"every auditor call would fail and no review would auto-post")
}
