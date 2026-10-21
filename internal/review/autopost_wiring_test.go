package review

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
)

// overrideWatcherSeamsForGate sets the watcher seams needed to drive one
// dispatch to completion with a controllable review outcome, and records
// whether the auto-post gate seam was invoked (and with what config). Restores
// all seams on cleanup.
func overrideWatcherSeamsForGate(t *testing.T, reviewErr error) (gateCalls *int, gotCfg *AutoPostConfig) {
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

	var mu sync.Mutex
	n := 0
	var seen AutoPostConfig
	runAutoPostGateFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, cfg AutoPostConfig) (GateResult, error) {
		mu.Lock()
		n++
		seen = cfg
		mu.Unlock()
		return GateResult{Outcome: outcomeDryRun, WouldHaveDone: outcomePunted}, nil
	}
	return &n, &seen
}

func TestWatcher_RunsGateAfterSuccessfulReview(t *testing.T) {
	store := &fakeStore{records: []Record{
		{Repo: "Bit-Quill/flowdra", PR: 1, URL: "u", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
	}}
	gateCalls, gotCfg := overrideWatcherSeamsForGate(t, nil)

	w := NewWatcher(store, time.Second, 5, "test-reviewer")
	w.SetAutoPostConfig(AutoPostConfig{Mode: ModeOwnRepos, MaxFindings: 7, OwnRepoOwners: []string{"Bit-Quill"}})

	w.pollOnce()
	// dispatch runs the review + gate in a goroutine; wait for it.
	waitFor(t, func() bool { return *gateCalls == 1 }, time.Second)

	if *gateCalls != 1 {
		t.Fatalf("gate called %d times, want 1", *gateCalls)
	}
	// The config set via SetAutoPostConfig must reach the gate unchanged.
	if gotCfg.Mode != ModeOwnRepos || gotCfg.MaxFindings != 7 {
		t.Errorf("gate got cfg %+v, want mode own-repos max 7", *gotCfg)
	}
}

func TestWatcher_SkipsGateWhenReviewFails(t *testing.T) {
	store := &fakeStore{records: []Record{
		{Repo: "owner/repo", PR: 2, URL: "u", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
	}}
	gateCalls, _ := overrideWatcherSeamsForGate(t, context.DeadlineExceeded)

	w := NewWatcher(store, time.Second, 5, "test-reviewer")
	w.pollOnce()
	// Give the dispatch goroutine time to run and (not) call the gate.
	time.Sleep(100 * time.Millisecond)

	if *gateCalls != 0 {
		t.Errorf("gate called %d times after a failed review, want 0", *gateCalls)
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
