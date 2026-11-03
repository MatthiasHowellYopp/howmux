package review

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// reviewModel is the model pinned for every agent step of the PR-review
// workflow (review.model in .howmux/config.yaml): pr_review.py's lenses and
// consolidator (via --model), howmux's humanizer and revise consolidator, and
// the auditor when review.auto_post.auditor_model is unset. "" leaves the
// choice to kiro-cli ("auto"), whose pick is never named in the audit trail —
// pinning is what makes the per-step model recordable, and keeps production
// on the model the Stage 3 evals measured.
var (
	reviewModelMu sync.RWMutex
	reviewModel   string
)

// SetReviewModel sets the workflow's pinned model. Call once at startup.
func SetReviewModel(model string) {
	reviewModelMu.Lock()
	defer reviewModelMu.Unlock()
	reviewModel = strings.TrimSpace(model)
}

// ReviewModel returns the workflow's pinned model, or "" for kiro-cli's default.
func ReviewModel() string {
	reviewModelMu.RLock()
	defer reviewModelMu.RUnlock()
	return reviewModel
}

// withModelArg appends --model <m> to a pr_review.py argv when a model is set.
func withModelArg(argv []string, model string) []string {
	if model == "" {
		return argv
	}
	return append(argv, "--model", model)
}

// auditorModelFor resolves the auditor's model: its own setting, else the
// workflow's pinned model.
func auditorModelFor(cfg AutoPostConfig) string {
	if m := strings.TrimSpace(cfg.AuditorModel); m != "" {
		return m
	}
	return ReviewModel()
}

// CheckReviewModel returns an error if review.model is set but kiro-cli does
// not offer it. Same rules as CheckAuditorModel.
func CheckReviewModel(ctx context.Context, model string) error {
	return checkModel(ctx, "review.model", model,
		"every review agent call would fail")
}

// checkModel is the shared check behind CheckAuditorModel and
// CheckReviewModel. An empty model (kiro-cli's default) is always valid. If the
// model list can't be read, the error wraps ErrModelListUnavailable.
func checkModel(ctx context.Context, key, model, consequence string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()
	available, err := listModelsFunc(ctx)
	if err != nil {
		return fmt.Errorf("%w to check %s %q: %v", ErrModelListUnavailable, key, model, err)
	}

	for _, id := range available {
		if id == model {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not a model kiro-cli offers; %s.\nAvailable models: %s",
		key, model, consequence, strings.Join(available, ", "))
}

// modelListTimeout bounds the kiro-cli --list-models call (kirocli adds a
// short WaitDelay for lingering child processes), so a hung CLI can't stall
// startup.
const modelListTimeout = 20 * time.Second
