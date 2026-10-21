package review

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
)

// gateProbe is the thread-safe recorder for the faked auto-post gate seam.
// The gate runs on the watcher's dispatch goroutine while the test asserts on
// the main goroutine, so every access to the recorded state must go through
// the mutex — otherwise `go test -race` (CI) flags it.
type gateProbe struct {
	mu   sync.Mutex
	n    int
	seen AutoPostConfig
}

func (p *gateProbe) record(cfg AutoPostConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	p.seen = cfg
}

func (p *gateProbe) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *gateProbe) config() AutoPostConfig {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

// overrideWatcherSeamsForGate sets the watcher seams needed to drive one
// dispatch to completion with a controllable review outcome, and returns a
// thread-safe probe recording whether the auto-post gate seam was invoked (and
// with what config). Restores all seams on cleanup.
func overrideWatcherSeamsForGate(t *testing.T, reviewErr error) *gateProbe {
	t.Helper()
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	origGate := runAutoPostGateFunc
	t.Cleanup(func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
		runAutoPostGateFunc = origGate
	})

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{State: "OPEN", HeadRefOid: "sha"}, nil
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		return reviewErr
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}

	probe := &gateProbe{}
	runAutoPostGateFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, cfg AutoPostConfig) (GateResult, error) {
		probe.record(cfg)
		return GateResult{Outcome: outcomeDryRun, WouldHaveDone: outcomePunted}, nil
	}
	return probe
}

func TestWatcher_RunsGateAfterSuccessfulReview(t *testing.T) {
	store := &fakeStore{records: []Record{
		{Repo: "Bit-Quill/flowdra", PR: 1, URL: "u", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
	}}
	probe := overrideWatcherSeamsForGate(t, nil)

	w := NewWatcher(store, time.Second, 5, "test-reviewer")
	w.SetAutoPostConfig(AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 7, OwnRepoOwners: []string{"Bit-Quill"}})

	w.Start()
	// dispatch runs the review + gate in a goroutine; wait for it.
	waitFor(t, func() bool { return probe.calls() == 1 }, 2*time.Second)
	// Stop drains all in-flight dispatch goroutines (wg.Wait) BEFORE the test
	// returns, so t.Cleanup's seam restore cannot race a goroutine's seam read.
	w.Stop()

	if got := probe.calls(); got != 1 {
		t.Fatalf("gate called %d times, want 1", got)
	}
	// The config set via SetAutoPostConfig must reach the gate unchanged.
	if cfg := probe.config(); cfg.Mode != ModeOwnRepos || cfg.MaxFindings != 7 {
		t.Errorf("gate got cfg %+v, want mode own-repos max 7", cfg)
	}
}

func TestWatcher_SkipsGateWhenReviewFails(t *testing.T) {
	store := &fakeStore{records: []Record{
		{Repo: "owner/repo", PR: 2, URL: "u", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
	}}
	probe := overrideWatcherSeamsForGate(t, context.DeadlineExceeded)

	w := NewWatcher(store, time.Second, 5, "test-reviewer")
	w.Start()
	// Let the first poll's dispatch goroutine run, then Stop() drains it
	// (wg.Wait) before the test returns — so cleanup's seam restore cannot
	// race the goroutine, and the review-failed path has definitely run.
	time.Sleep(50 * time.Millisecond)
	w.Stop()

	if got := probe.calls(); got != 0 {
		t.Errorf("gate called %d times after a failed review, want 0", got)
	}
}

// waitFor polls cond until true or the timeout elapses, failing the test on
// timeout. Keeps the async-dispatch tests from flaking on a fixed sleep.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}
