package review

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
)

// Test helpers and fakes

type fakeStore struct {
	records []Record
	mu      sync.Mutex
}

// Verify fakeStore implements StoreInterface at compile time
var _ StoreInterface = (*fakeStore)(nil)

func (fs *fakeStore) List() ([]Record, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]Record{}, fs.records...), nil
}

func (fs *fakeStore) Save(rec Record) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// Update or append
	for i, r := range fs.records {
		if r.Repo == rec.Repo && r.PR == rec.PR {
			fs.records[i] = rec
			return nil
		}
	}
	fs.records = append(fs.records, rec)
	return nil
}

func (fs *fakeStore) Get(repo string, pr int) (Record, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, r := range fs.records {
		if r.Repo == repo && r.PR == pr {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

func (fs *fakeStore) Remove(repo string, pr int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for i, r := range fs.records {
		if r.Repo == repo && r.PR == pr {
			fs.records = append(fs.records[:i], fs.records[i+1:]...)
			return nil
		}
	}
	return nil
}

type fetchPRCall struct {
	repo string
	pr   int
}

type dispatchCall struct {
	repo    string
	pr      int
	headSHA string
}

type removeCall struct {
	repo string
	pr   int
}

func TestWatcherPollOrchestration(t *testing.T) {
	// Set up test data
	store := &fakeStore{
		records: []Record{
			{
				Repo:            "owner/repo1",
				PR:              1,
				URL:             "https://github.com/owner/repo1/pull/1",
				Status:          StatusWatching,
				LastReviewedSHA: "",
				EnrolledAt:      time.Now().Format(time.RFC3339),
			},
			{
				Repo:            "owner/repo2",
				PR:              2,
				URL:             "https://github.com/owner/repo2/pull/2",
				Status:          StatusWatching,
				LastReviewedSHA: "old-sha",
				EnrolledAt:      time.Now().Format(time.RFC3339),
			},
		},
	}

	// Track calls
	var fetchCalls []fetchPRCall
	var dispatchCalls []dispatchCall
	var removeCalls []removeCall
	var mu sync.Mutex

	// Override seams
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		mu.Lock()
		fetchCalls = append(fetchCalls, fetchPRCall{repo: repo, pr: pr})
		mu.Unlock()

		// Return open PR with review requested for repo1/1
		if repo == "owner/repo1" && pr == 1 {
			return github.PR{
				State:      "OPEN",
				HeadRefOid: "new-sha-1",
				ReviewRequests: []github.ReviewRequest{
					{Login: "test-reviewer"},
				},
			}, nil
		}
		// Return merged PR for repo2/2
		if repo == "owner/repo2" && pr == 2 {
			now := time.Now()
			return github.PR{
				State:      "MERGED",
				MergedAt:   &now,
				HeadRefOid: "merged-sha",
			}, nil
		}
		return github.PR{}, fmt.Errorf("unknown PR")
	}

	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		mu.Lock()
		dispatchCalls = append(dispatchCalls, dispatchCall{
			repo:    rec.Repo,
			pr:      rec.PR,
			headSHA: headSHA,
		})
		mu.Unlock()
		return nil
	}

	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		mu.Lock()
		removeCalls = append(removeCalls, removeCall{repo: repo, pr: pr})
		mu.Unlock()
		return store.Remove(repo, pr)
	}

	// Create watcher with very short poll interval for testing
	watcher := NewWatcher(store, 50*time.Millisecond, 5, "test-reviewer")

	// Run one poll manually
	watcher.pollOnce()

	// Wait for async dispatches to complete
	time.Sleep(100 * time.Millisecond)

	// Verify fetch was called for all records
	mu.Lock()
	if len(fetchCalls) != 2 {
		t.Errorf("expected 2 fetch calls, got %d", len(fetchCalls))
	}

	// Verify dispatch was called for open+requested PR
	if len(dispatchCalls) != 1 {
		t.Fatalf("expected 1 dispatch call, got %d", len(dispatchCalls))
	}
	if dispatchCalls[0].repo != "owner/repo1" || dispatchCalls[0].pr != 1 {
		t.Errorf("dispatch called for wrong PR: %+v", dispatchCalls[0])
	}
	if dispatchCalls[0].headSHA != "new-sha-1" {
		t.Errorf("dispatch called with wrong SHA: %s", dispatchCalls[0].headSHA)
	}

	// Verify remove was called for merged PR
	if len(removeCalls) != 1 {
		t.Fatalf("expected 1 remove call, got %d", len(removeCalls))
	}
	if removeCalls[0].repo != "owner/repo2" || removeCalls[0].pr != 2 {
		t.Errorf("remove called for wrong PR: %+v", removeCalls[0])
	}
	mu.Unlock()
}

func TestWatcherConcurrencyCap(t *testing.T) {
	// Set up test data with 5 PRs all needing review
	store := &fakeStore{
		records: []Record{
			{Repo: "owner/repo", PR: 1, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url1", Status: StatusWatching},
			{Repo: "owner/repo", PR: 2, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url2", Status: StatusWatching},
			{Repo: "owner/repo", PR: 3, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url3", Status: StatusWatching},
			{Repo: "owner/repo", PR: 4, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url4", Status: StatusWatching},
			{Repo: "owner/repo", PR: 5, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url5", Status: StatusWatching},
		},
	}

	// Track dispatch calls
	dispatchedPRs := make(chan int, 10)

	// Override seams
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		// All PRs are open with review requested
		return github.PR{
			State:      "OPEN",
			HeadRefOid: fmt.Sprintf("sha-%d", pr),
			ReviewRequests: []github.ReviewRequest{
				{Login: "test-reviewer"},
			},
		}, nil
	}

	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		dispatchedPRs <- rec.PR

		// Simulate some work time
		time.Sleep(100 * time.Millisecond)

		return nil
	}

	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}

	// Create watcher with max 2 concurrent reviews
	watcher := NewWatcher(store, 1*time.Second, 2, "test-reviewer")

	// Run one poll
	watcher.pollOnce()

	// Wait a bit for dispatches to start
	time.Sleep(50 * time.Millisecond)

	// Check that only 2 reviews are active (concurrency cap)
	watcher.mu.RLock()
	activeCount := len(watcher.activeReviews)
	watcher.mu.RUnlock()

	if activeCount != 2 {
		t.Errorf("expected 2 active reviews (concurrency cap), got %d", activeCount)
	}

	// Collect dispatched PRs (with timeout)
	var dispatched []int
	timeout := time.After(300 * time.Millisecond)
collectLoop:
	for {
		select {
		case pr := <-dispatchedPRs:
			dispatched = append(dispatched, pr)
			if len(dispatched) >= 2 {
				break collectLoop
			}
		case <-timeout:
			break collectLoop
		}
	}

	// Verify only 2 PRs were dispatched in this round
	if len(dispatched) != 2 {
		t.Errorf("expected 2 dispatched PRs per round, got %d: %v", len(dispatched), dispatched)
	}
}

func TestWatcherSkipDoesNothing(t *testing.T) {
	// Set up test data with PR that should be skipped
	store := &fakeStore{
		records: []Record{
			{
				Repo:            "owner/repo",
				PR:              1,
				URL:             "https://github.com/owner/repo/pull/1",
				Status:          StatusWatching,
				LastReviewedSHA: "old-sha",
				EnrolledAt:      time.Now().Format(time.RFC3339),
			},
		},
	}

	// Track calls
	var dispatchCalled bool
	var removeCalled bool

	// Override seams
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		// Return open PR with NO review request
		return github.PR{
			State:          "OPEN",
			HeadRefOid:     "current-sha",
			ReviewRequests: []github.ReviewRequest{}, // No requests
		}, nil
	}

	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		dispatchCalled = true
		return nil
	}

	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		removeCalled = true
		return store.Remove(repo, pr)
	}

	// Create watcher
	watcher := NewWatcher(store, 1*time.Second, 5, "test-reviewer")

	// Run one poll
	watcher.pollOnce()

	// Wait for any potential async operations
	time.Sleep(50 * time.Millisecond)

	// Verify neither dispatch nor remove were called
	if dispatchCalled {
		t.Error("dispatch should not be called for skipped PR")
	}
	if removeCalled {
		t.Error("remove should not be called for skipped PR")
	}
}

func TestWatcherGracefulStop(t *testing.T) {
	store := &fakeStore{
		records: []Record{},
	}

	// Override seams to avoid real I/O
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{State: "OPEN", HeadRefOid: "sha"}, nil
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		return nil
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}

	// Create watcher with short poll interval
	watcher := NewWatcher(store, 50*time.Millisecond, 5, "test-reviewer")

	// Start watcher
	watcher.Start()

	// Verify it's running
	if !watcher.Running() {
		t.Error("watcher should be running after Start()")
	}

	// Let it run for a bit
	time.Sleep(150 * time.Millisecond)

	// Stop watcher
	watcher.Stop()

	// Verify it's stopped
	if watcher.Running() {
		t.Error("watcher should not be running after Stop()")
	}

	// Verify stop is idempotent
	watcher.Stop()
	if watcher.Running() {
		t.Error("watcher should still be stopped after second Stop()")
	}
}

// TestWatcherSamePRNotDoubleDispatched verifies the per-PR guard: while a
// review for a PR is in flight, a subsequent poll round that re-decides
// ActionReview for the same PR must not dispatch a second concurrent review.
func TestWatcherSamePRNotDoubleDispatched(t *testing.T) {
	store := &fakeStore{
		records: []Record{
			{Repo: "owner/repo", PR: 1, URL: "url1", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
		},
	}

	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{
			State:          "OPEN",
			HeadRefOid:     "sha-1",
			ReviewRequests: []github.ReviewRequest{{Login: "test-reviewer"}},
		}, nil
	}

	// A slow review that stays in flight across multiple poll rounds.
	release := make(chan struct{})
	var dispatchCount int32
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		atomic.AddInt32(&dispatchCount, 1)
		<-release // block until the test allows completion
		return nil
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error { return store.Remove(repo, pr) }

	watcher := NewWatcher(store, 1*time.Second, 5, "test-reviewer")

	// Three poll rounds while the first review is still in flight.
	watcher.pollOnce()
	watcher.pollOnce()
	watcher.pollOnce()

	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&dispatchCount); got != 1 {
		t.Errorf("expected exactly 1 dispatch for the same in-flight PR, got %d", got)
	}

	close(release) // let the review finish
	time.Sleep(50 * time.Millisecond)
}

// TestWatcherStopWaitsForInFlight verifies Stop() blocks until in-flight
// review goroutines have drained (not just that Running() flips false).
func TestWatcherStopWaitsForInFlight(t *testing.T) {
	store := &fakeStore{
		records: []Record{
			{Repo: "owner/repo", PR: 1, URL: "url1", Status: StatusWatching, EnrolledAt: time.Now().Format(time.RFC3339)},
		},
	}

	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{
			State:          "OPEN",
			HeadRefOid:     "sha-1",
			ReviewRequests: []github.ReviewRequest{{Login: "test-reviewer"}},
		}, nil
	}

	var finished int32
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		// Simulate a review that respects context cancellation.
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
		atomic.StoreInt32(&finished, 1)
		return nil
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error { return store.Remove(repo, pr) }

	watcher := NewWatcher(store, 1*time.Hour, 5, "test-reviewer")
	watcher.Start()

	// Give the immediate poll time to dispatch the review.
	time.Sleep(50 * time.Millisecond)

	// Stop must block until the in-flight review goroutine has returned.
	watcher.Stop()

	if atomic.LoadInt32(&finished) != 1 {
		t.Error("Stop() returned before the in-flight review finished draining")
	}
	if watcher.Running() {
		t.Error("watcher should not be running after Stop()")
	}
}

// TestWatcherConcurrentStop verifies two concurrent Stop() calls don't panic
// (double-close guarded by sync.Once).
func TestWatcherConcurrentStop(t *testing.T) {
	store := &fakeStore{records: []Record{}}

	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
	}()
	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{State: "OPEN", HeadRefOid: "sha"}, nil
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		return nil
	}

	watcher := NewWatcher(store, 50*time.Millisecond, 5, "test-reviewer")
	watcher.Start()
	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); watcher.Stop() }()
	}
	wg.Wait() // must not panic

	if watcher.Running() {
		t.Error("watcher should be stopped")
	}
}

func TestWatcherStartIdempotent(t *testing.T) {
	store := &fakeStore{records: []Record{}}

	// Override seams
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{State: "OPEN", HeadRefOid: "sha"}, nil
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		return nil
	}
	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}

	watcher := NewWatcher(store, 100*time.Millisecond, 5, "test-reviewer")

	// Start multiple times
	watcher.Start()
	watcher.Start()
	watcher.Start()

	// Should only be running once
	if !watcher.Running() {
		t.Error("watcher should be running")
	}

	// Clean up
	watcher.Stop()
}

func TestWatcherConcurrentDispatchRaceCondition(t *testing.T) {
	// This test exercises concurrent dispatch to verify lock-guarded map updates work under -race
	store := &fakeStore{
		records: []Record{
			{Repo: "owner/repo", PR: 1, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url1", Status: StatusWatching},
			{Repo: "owner/repo", PR: 2, LastReviewedSHA: "", EnrolledAt: time.Now().Format(time.RFC3339), URL: "url2", Status: StatusWatching},
		},
	}

	completedPRs := make(chan int, 2)

	// Override seams
	origFetch := fetchPRFunc
	origDispatch := dispatchReviewFunc
	origRemove := removeRecordFunc
	defer func() {
		fetchPRFunc = origFetch
		dispatchReviewFunc = origDispatch
		removeRecordFunc = origRemove
	}()

	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{
			State:      "OPEN",
			HeadRefOid: fmt.Sprintf("sha-%d", pr),
			ReviewRequests: []github.ReviewRequest{
				{Login: "test-reviewer"},
			},
		}, nil
	}

	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		// Simulate work
		time.Sleep(50 * time.Millisecond)
		completedPRs <- rec.PR
		return nil
	}

	removeRecordFunc = func(store StoreInterface, repo string, pr int) error {
		return store.Remove(repo, pr)
	}

	watcher := NewWatcher(store, 1*time.Second, 5, "test-reviewer")

	// Run one poll - should dispatch both PRs concurrently
	watcher.pollOnce()

	// Wait for both dispatches to complete
	timeout := time.After(300 * time.Millisecond)
	completed := 0
	for completed < 2 {
		select {
		case <-completedPRs:
			completed++
		case <-timeout:
			t.Fatalf("timeout waiting for dispatches to complete, got %d/2", completed)
		}
	}

	// Give the deferred cleanup a moment to execute
	time.Sleep(50 * time.Millisecond)

	// Verify activeReviews map is empty after completion
	watcher.mu.RLock()
	activeCount := len(watcher.activeReviews)
	watcher.mu.RUnlock()

	if activeCount != 0 {
		t.Errorf("expected 0 active reviews after completion, got %d", activeCount)
	}
}
