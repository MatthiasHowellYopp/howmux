package watcher

import (
	"log"
	"sync"
	"time"

	"github.com/jbrinkman/kiro-krew/internal/config"
	"github.com/jbrinkman/kiro-krew/internal/jira"
)

// jiraLister is the subset of the jira package used by the watcher. It is an
// interface so tests can supply a fake without shelling out to jtk.
type jiraLister interface {
	ListTodoIssues(jql string, max int) ([]jira.Issue, error)
	GetIssue(key string) (*jira.IssueDetails, error)
}

// jiraStore persists retrieved tickets and reports whether one already exists.
// The stored file acts as a durable sentinel for dedup across restarts.
type jiraStore interface {
	Has(key string) bool
	Save(issue jira.Issue, details *jira.IssueDetails) (string, error)
}

// realJiraClient adapts the package-level jira functions to jiraLister.
type realJiraClient struct{}

func (realJiraClient) ListTodoIssues(jql string, max int) ([]jira.Issue, error) {
	return jira.ListTodoIssues(jql, max)
}

func (realJiraClient) GetIssue(key string) (*jira.IssueDetails, error) {
	return jira.GetIssue(key)
}

// JiraWatcher polls a Jira board for work items. Unlike the GitHub Watcher it
// supports two run modes:
//
//   - bounded: run a fixed number of poll iterations, then stop on its own.
//     `jirawatch` / `jirawatch 1` is the n=1 case (a single immediate check).
//   - infinite: poll until Stop() is called (or the app exits), like the
//     GitHub watcher. Started via `jirawatch start`.
//
// Both modes wait config.Jira poll interval between iterations. For now the
// watcher only retrieves ("downloads") each newly-seen ticket and logs it;
// spawning agents and transitioning issues are deferred to a later step.
type JiraWatcher struct {
	config  *config.Config
	client  jiraLister
	store   jiraStore
	stop    chan struct{}
	tracked map[string]bool
	mu      sync.RWMutex
	started bool
}

// NewJiraWatcher creates a JiraWatcher backed by the real jtk-based client and
// the on-disk ticket store.
func NewJiraWatcher(cfg *config.Config) *JiraWatcher {
	return &JiraWatcher{
		config:  cfg,
		client:  realJiraClient{},
		store:   jira.NewStore(""),
		tracked: make(map[string]bool),
	}
}

// pollInterval returns the interval to wait between iterations. It falls back
// to the top-level PollInterval, then to a sane default, so the watcher never
// busy-loops.
func (w *JiraWatcher) pollInterval() time.Duration {
	if w.config.PollInterval > 0 {
		return w.config.PollInterval
	}
	return 5 * time.Minute
}

// Start begins an infinite poll loop until Stop is called. It is a no-op if the
// watcher is already running.
func (w *JiraWatcher) Start() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.stop = make(chan struct{})
	w.started = true
	w.mu.Unlock()

	log.Printf("[jirawatch] started — polling %q every %s", w.config.Jira.BoardURL, w.pollInterval())
	go w.pollLoopInfinite()
}

// RunBounded runs exactly n poll iterations then stops. n < 1 is treated as 1
// (the one-shot default). It runs asynchronously; use Running to observe state.
func (w *JiraWatcher) RunBounded(n int) {
	if n < 1 {
		n = 1
	}

	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.stop = make(chan struct{})
	w.started = true
	w.mu.Unlock()

	log.Printf("[jirawatch] running %d iteration(s), polling %q every %s", n, w.config.Jira.BoardURL, w.pollInterval())
	go w.pollLoopBounded(n)
}

// Stop halts a running loop (bounded or infinite). No-op if not running.
func (w *JiraWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return
	}
	close(w.stop)
	w.started = false
	log.Printf("[jirawatch] stopped")
}

// Running reports whether a poll loop is currently active.
func (w *JiraWatcher) Running() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.started
}

// markStopped flips the started flag off when a loop finishes on its own
// (bounded mode) without an external Stop().
func (w *JiraWatcher) markStopped() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.started = false
}

func (w *JiraWatcher) pollLoopInfinite() {
	// Run immediately, then on interval.
	w.checkIssues()

	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.checkIssues()
		}
	}
}

func (w *JiraWatcher) pollLoopBounded(n int) {
	defer w.markStopped()

	// First iteration runs immediately.
	w.checkIssues()
	if n == 1 {
		log.Printf("[jirawatch] completed 1 iteration")
		return
	}

	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()

	done := 1
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.checkIssues()
			done++
			if done >= n {
				log.Printf("[jirawatch] completed %d iterations", n)
				return
			}
		}
	}
}

// checkIssues performs one poll: search for matching issues and retrieve any
// not seen before.
func (w *JiraWatcher) checkIssues() {
	jql := w.config.Jira.JQL
	log.Printf("[jirawatch] polling with JQL: %s", jql)

	issues, err := w.client.ListTodoIssues(jql, 50)
	if err != nil {
		log.Printf("[jirawatch] error searching issues: %v", err)
		return
	}

	if len(issues) == 0 {
		log.Printf("[jirawatch] no matching issues")
		return
	}

	for _, issue := range issues {
		// In-memory guard: skip issues already handled this run.
		w.mu.RLock()
		seen := w.tracked[issue.Key]
		w.mu.RUnlock()
		if seen {
			continue
		}

		// Durable sentinel: if the ticket is already stored on disk, it was
		// downloaded on a previous run — skip it and mark it seen.
		if w.store.Has(issue.Key) {
			w.mu.Lock()
			w.tracked[issue.Key] = true
			w.mu.Unlock()
			log.Printf("[jirawatch] %s already stored, skipping", issue.Key)
			continue
		}

		w.mu.Lock()
		w.tracked[issue.Key] = true
		w.mu.Unlock()

		log.Printf("[jirawatch] found issue %s (%s): %s", issue.Key, issue.Status, issue.Summary)

		// Retrieve ("download") the full ticket and persist it to disk. The
		// stored file becomes the sentinel for future runs. Downstream handling
		// (agent spawn, transition) is a later step.
		details, err := w.client.GetIssue(issue.Key)
		if err != nil {
			log.Printf("[jirawatch] failed to retrieve %s: %v", issue.Key, err)
			// Un-track so a later poll can retry the retrieval.
			w.mu.Lock()
			delete(w.tracked, issue.Key)
			w.mu.Unlock()
			continue
		}

		path, err := w.store.Save(issue, details)
		if err != nil {
			log.Printf("[jirawatch] failed to save %s: %v", issue.Key, err)
			// Un-track so a later poll can retry the save.
			w.mu.Lock()
			delete(w.tracked, issue.Key)
			w.mu.Unlock()
			continue
		}

		log.Printf("[jirawatch] retrieved and saved %s -> %s (%d bytes)", issue.Key, path, len(details.FullText))
	}
}
