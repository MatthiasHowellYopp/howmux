package review

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// Injectable seams for testing (package-level function vars)
var (
	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.GetPR(repo, pr)
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface) error {
		// Cast to concrete Store for RunReview which expects *Store
		concreteStore, ok := store.(*Store)
		if !ok {
			return fmt.Errorf("store must be *Store for RunReview")
		}
		return RunReview(ctx, rec, ".howmux/reviews", headSHA, concreteStore)
	}
)

// Watcher polls enrolled PRs and dispatches reviews based on GitHub state
type Watcher struct {
	store         StoreInterface
	pollInterval  time.Duration
	maxConcurrent int
	reviewer      string // GitHub username or team for IsReviewRequestedFor
	stop          chan struct{}
	started       bool
	activeReviews map[string]bool // "owner/repo#pr" → in-progress
	mu            sync.RWMutex
}

// NewWatcher creates a new PR review watcher
func NewWatcher(store StoreInterface, pollInterval time.Duration, maxConcurrent int, reviewer string) *Watcher {
	return &Watcher{
		store:         store,
		pollInterval:  pollInterval,
		maxConcurrent: maxConcurrent,
		reviewer:      reviewer,
		stop:          make(chan struct{}),
		activeReviews: make(map[string]bool),
	}
}

// Start spawns the poll loop goroutine
func (w *Watcher) Start() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	logging.Info("PR review watcher started", "poll_interval", w.pollInterval, "max_concurrent", w.maxConcurrent, "reviewer", w.reviewer)

	go w.pollLoop()
}

// Stop signals the poll loop to exit and waits for it to finish
func (w *Watcher) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	close(w.stop)

	// Wait for poll loop to acknowledge stop
	w.mu.Lock()
	w.started = false
	w.mu.Unlock()

	logging.Info("PR review watcher stopped")
}

// Running returns true if the watcher is currently running
func (w *Watcher) Running() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.started
}

// pollLoop runs immediately, then on every pollInterval
func (w *Watcher) pollLoop() {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Run immediately on start
	w.pollOnce()

	for {
		select {
		case <-w.stop:
			logging.Debug("poll loop received stop signal")
			return
		case <-ticker.C:
			w.pollOnce()
		}
	}
}

// pollOnce executes a single poll round: fetch all records, decide action per PR, dispatch/prune
func (w *Watcher) pollOnce() {
	logging.Debug("poll round starting")

	records, err := w.store.List()
	if err != nil {
		logging.Error("failed to list records", "error", err)
		return
	}

	logging.Debug("poll round fetched records", "count", len(records))

	for _, rec := range records {
		select {
		case <-w.stop:
			logging.Debug("poll round interrupted by stop signal")
			return
		default:
		}

		// Parse repo into owner/name
		parts := strings.Split(rec.Repo, "/")
		if len(parts) != 2 {
			logging.Warn("invalid repo format, skipping", "repo", rec.Repo, "pr", rec.PR)
			continue
		}

		// Fetch PR state
		pr, err := fetchPRFunc(rec.Repo, rec.PR)
		if err != nil {
			logging.Error("failed to fetch PR", "repo", rec.Repo, "pr", rec.PR, "error", err)
			continue
		}

		// Decide action
		action := decideReviewAction(pr, rec, w.reviewer)

		switch action {
		case ActionPrune:
			logging.Info("pruning merged/closed PR", "repo", rec.Repo, "pr", rec.PR)
			if err := removeRecordFunc(w.store, rec.Repo, rec.PR); err != nil {
				logging.Error("failed to remove record", "repo", rec.Repo, "pr", rec.PR, "error", err)
			}

		case ActionReview:
			// Check concurrency cap
			prKey := fmt.Sprintf("%s#%d", rec.Repo, rec.PR)

			w.mu.Lock()
			if len(w.activeReviews) >= w.maxConcurrent {
				logging.Debug("concurrency cap reached, skipping review", "repo", rec.Repo, "pr", rec.PR, "active", len(w.activeReviews), "max", w.maxConcurrent)
				w.mu.Unlock()
				continue
			}

			// Mark as active
			w.activeReviews[prKey] = true
			w.mu.Unlock()

			logging.Info("dispatching review", "repo", rec.Repo, "pr", rec.PR, "sha", pr.HeadSHA())

			// Dispatch review in separate goroutine
			go func(r Record, headSHA string, key string) {
				defer func() {
					w.mu.Lock()
					delete(w.activeReviews, key)
					w.mu.Unlock()
					logging.Debug("review completed, slot freed", "repo", r.Repo, "pr", r.PR)
				}()

				ctx := context.Background()
				if err := dispatchReviewFunc(ctx, r, headSHA, w.store); err != nil {
					logging.Error("review dispatch failed", "repo", r.Repo, "pr", r.PR, "error", err)
				} else {
					logging.Info("review dispatch succeeded", "repo", r.Repo, "pr", r.PR)
				}
			}(rec, pr.HeadSHA(), prKey)

		case ActionSkip:
			logging.Debug("skipping PR", "repo", rec.Repo, "pr", rec.PR)
		}
	}

	logging.Debug("poll round complete")
}
