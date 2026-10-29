package review

import (
	"context"
	"fmt"
	"io"
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
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		return RunReview(ctx, rec, headSHA, store, tabWriter)
	}
	// runAutoPostGateFunc runs the review auto-post gate after a successful
	// review. It is a package-level seam so tests can substitute a fake. The
	// default implementation (RunAutoPostGate) reads the freshly-updated
	// record + spool body, fetches the diff, and calls EvaluateAndMaybePost.
	runAutoPostGateFunc = RunAutoPostGate
	// listReviewRequestedFunc lists open PRs in a repo that request a review
	// from reviewer. Seam for the discovery pass.
	listReviewRequestedFunc = func(repo, reviewer string) ([]github.PRRef, error) {
		return github.ListReviewRequested(repo, reviewer)
	}
)

// Watcher polls enrolled PRs and dispatches reviews based on GitHub state
type Watcher struct {
	store         StoreInterface
	pollInterval  time.Duration
	maxConcurrent int
	reviewer      string // GitHub username or team for IsReviewRequestedFor

	autoPost AutoPostConfig // review auto-post gate config; zero value = dry-run (safe)

	// discoverRequested enables the per-poll scan of enrolled repos for other
	// PRs requesting the reviewer's review. Off by default.
	discoverRequested bool
	discoveryWarnOnce sync.Once // logs "discovery disabled" once, not every poll

	stop     chan struct{}
	stopOnce sync.Once
	started  bool
	wg       sync.WaitGroup // tracks the poll loop and all in-flight reviews
	cancel   context.CancelFunc
	ctx      context.Context // watcher-scoped; cancelled on Stop

	activeReviews map[string]bool // "owner/repo#pr" → in-progress
	mu            sync.RWMutex
}

// NewWatcher creates a new PR review watcher
func NewWatcher(store StoreInterface, pollInterval time.Duration, maxConcurrent int, reviewer string) *Watcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Watcher{
		store:         store,
		pollInterval:  pollInterval,
		maxConcurrent: maxConcurrent,
		reviewer:      reviewer,
		stop:          make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
		activeReviews: make(map[string]bool),
	}
}

// SetAutoPostConfig sets the review auto-post gate configuration. The zero
// value (never set) leaves the gate in its safe default (dry-run), so this is
// opt-in: production wires the config from .howmux/config.yaml, tests that
// don't care leave it unset. Must be called before Start.
func (w *Watcher) SetAutoPostConfig(cfg AutoPostConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.autoPost = cfg
}

// SetDiscoverRequested turns on the discovery pass: each poll, every repo
// that has an enrolled PR is scanned for other open, non-draft PRs that
// request the watcher's reviewer, and those PRs are enrolled. Must be called
// before Start.
func (w *Watcher) SetDiscoverRequested(enabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.discoverRequested = enabled
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

	w.wg.Add(1)
	go w.pollLoop()
}

// Stop signals the poll loop to exit, cancels in-flight reviews, and waits for
// the poll loop and all dispatched review goroutines to finish before
// returning. Safe to call multiple times and from concurrent goroutines.
func (w *Watcher) Stop() {
	w.mu.RLock()
	started := w.started
	w.mu.RUnlock()
	if !started {
		return
	}

	// sync.Once guards against a double-close panic if Stop races with itself.
	w.stopOnce.Do(func() {
		close(w.stop)
		w.cancel() // cancel in-flight review contexts
	})

	// Wait for the poll loop and all in-flight reviews to drain.
	w.wg.Wait()

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

// Interval returns the configured poll interval.
func (w *Watcher) Interval() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.pollInterval
}

// EnrolledCount returns the number of enrolled PR records currently tracked
// by the watcher's store. Returns 0 if the store cannot be listed — this is a
// display-only accessor, so it degrades to 0 rather than propagating an error
// or panicking (mirrors the tolerant error handling already used elsewhere in
// this file, e.g. pollOnce's logging.Error + continue pattern, minus the log
// call since Interval/EnrolledCount are not given a logger context by the
// issue and this is a lightweight, frequently-called render-path accessor).
func (w *Watcher) EnrolledCount() int {
	records, err := w.store.List()
	if err != nil {
		return 0
	}
	return len(records)
}

// pollLoop runs immediately, then on every pollInterval
func (w *Watcher) pollLoop() {
	defer w.wg.Done()

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

	w.mu.RLock()
	discover := w.discoverRequested
	w.mu.RUnlock()
	if discover {
		// Discovered records join this round, so they're reviewed right away
		// (Rule 2: never reviewed) instead of waiting a full poll interval.
		records = append(records, w.discoverRequestedPRs(records)...)
	}

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
			w.dispatch(rec, pr.HeadSHA())

		case ActionSkip:
			logging.Debug("skipping PR", "repo", rec.Repo, "pr", rec.PR)
		}
	}

	logging.Debug("poll round complete")
}

// discoverRequestedPRs scans each distinct repo in records for open PRs that
// request w.reviewer's review and enrolls any that aren't tracked yet. Drafts
// are skipped; a draft that's later marked ready is picked up on a later poll
// while the request is still pending. It returns the newly saved records.
// Failures are logged per repo and never abort the poll round.
func (w *Watcher) discoverRequestedPRs(records []Record) []Record {
	if err := github.ValidateReviewerLogin(w.reviewer); err != nil {
		// No usable user login: gh login resolution failed (the TUI already
		// warns at startup) or reviewer: is set to a team. Check once here
		// instead of letting every repo's list call fail on every poll.
		w.discoveryWarnOnce.Do(func() {
			logging.Warn("review-request discovery disabled", "reviewer", w.reviewer, "reason", err)
		})
		return nil
	}

	enrolled := make(map[string]bool, len(records)) // "owner/repo#pr"
	seenRepo := make(map[string]bool)
	var repos []string
	for _, rec := range records {
		enrolled[fmt.Sprintf("%s#%d", rec.Repo, rec.PR)] = true
		if !seenRepo[rec.Repo] {
			seenRepo[rec.Repo] = true
			repos = append(repos, rec.Repo)
		}
	}

	var added []Record
	for _, repo := range repos {
		select {
		case <-w.stop:
			return added
		default:
		}

		parts := strings.Split(repo, "/")
		if len(parts) != 2 {
			continue // pollOnce logs invalid repos
		}

		refs, err := listReviewRequestedFunc(repo, w.reviewer)
		if err != nil {
			logging.Error("failed to list review-requested PRs", "repo", repo, "error", err)
			continue
		}

		for _, ref := range refs {
			if ref.IsDraft || ref.Number <= 0 {
				continue
			}
			key := fmt.Sprintf("%s#%d", repo, ref.Number)
			if enrolled[key] {
				continue
			}
			url := ref.URL
			if url == "" {
				url = fmt.Sprintf("https://github.com/%s/pull/%d", repo, ref.Number)
			}
			rec := Record{
				Repo:       repo,
				PR:         ref.Number,
				URL:        url,
				Status:     StatusWatching,
				EnrolledAt: time.Now().Format(time.RFC3339),
				ReviewDir:  fmt.Sprintf(".worktrees/review-%s-%s-%d", parts[0], parts[1], ref.Number),
			}
			// Re-check right before saving: the snapshot above was taken
			// before the gh calls, and Save overwrites unconditionally, so a
			// PR enrolled from the TUI in the meantime (possibly mid-review)
			// must not be replaced with a fresh Watching record.
			if _, exists, err := w.store.Get(repo, ref.Number); err != nil {
				logging.Error("failed to check discovered PR enrollment", "repo", repo, "pr", ref.Number, "error", err)
				continue
			} else if exists {
				enrolled[key] = true
				continue
			}
			if err := w.store.Save(rec); err != nil {
				logging.Error("failed to enroll discovered PR", "repo", repo, "pr", ref.Number, "error", err)
				continue
			}
			enrolled[key] = true
			added = append(added, rec)
			logging.Info("enrolled review-requested PR", "repo", repo, "pr", ref.Number)
		}
	}
	return added
}

// dispatch admits a review for the given record if the same PR is not already
// in flight and the global concurrency cap has room, then runs it in a
// tracked goroutine. The admission decision (per-PR guard + cap + mark) happens
// atomically under one lock.
func (w *Watcher) dispatch(rec Record, headSHA string) {
	prKey := fmt.Sprintf("%s#%d", rec.Repo, rec.PR)

	w.mu.Lock()
	// Per-PR guard: a review that outlasts a poll interval leaves the store
	// record stale (LastReviewedSHA is only persisted on completion), so the
	// next poll would re-decide ActionReview. Without this guard that would
	// dispatch a SECOND concurrent review of the same PR — duplicate work and
	// two goroutines racing the same artifact/record.
	if w.activeReviews[prKey] {
		w.mu.Unlock()
		logging.Debug("review already in flight for PR, skipping", "repo", rec.Repo, "pr", rec.PR)
		return
	}
	if len(w.activeReviews) >= w.maxConcurrent {
		w.mu.Unlock()
		logging.Debug("concurrency cap reached, skipping review", "repo", rec.Repo, "pr", rec.PR, "active", len(w.activeReviews), "max", w.maxConcurrent)
		return
	}
	w.activeReviews[prKey] = true
	w.mu.Unlock()

	logging.Info("dispatching review", "repo", rec.Repo, "pr", rec.PR, "sha", headSHA)

	w.wg.Add(1)
	go func(r Record, sha, key string) {
		defer w.wg.Done()
		defer func() {
			w.mu.Lock()
			delete(w.activeReviews, key)
			w.mu.Unlock()
			logging.Debug("review completed, slot freed", "repo", r.Repo, "pr", r.PR)
		}()

		// Use the watcher-scoped context so Stop() cancels in-flight reviews
		// rather than letting them run their full ACP timeout.
		// TODO: wire actual review tab writer when tab management is integrated
		if err := dispatchReviewFunc(w.ctx, r, sha, w.store, io.Discard); err != nil {
			logging.Error("review dispatch failed", "repo", r.Repo, "pr", r.PR, "error", err)
			return
		}
		logging.Info("review dispatch succeeded", "repo", r.Repo, "pr", r.PR)

		// Run the auto-post gate on the completed review. Read the config
		// under the lock (SetAutoPostConfig may run concurrently). The gate
		// is best-effort: a gate error never fails the review that already
		// succeeded — it logs and leaves the review in pending/ for a human.
		w.mu.RLock()
		cfg := w.autoPost
		w.mu.RUnlock()
		if res, err := runAutoPostGateFunc(w.ctx, r, sha, w.store, cfg); err != nil {
			logging.Error("auto-post gate failed", "repo", r.Repo, "pr", r.PR, "error", err)
		} else {
			logging.Info("auto-post gate complete", "repo", r.Repo, "pr", r.PR, "outcome", string(res.Outcome), "would", string(res.WouldHaveDone))
		}
	}(rec, headSHA, prKey)
}
