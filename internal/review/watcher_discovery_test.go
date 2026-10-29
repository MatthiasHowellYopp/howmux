package review

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/github"
)

// discoveryHarness stubs every watcher seam so discovery tests never shell
// out to gh or run the real review / auto-post gate.
type discoveryHarness struct {
	mu         sync.Mutex
	listCalls  []string // repos passed to listReviewRequestedFunc
	listResult map[string][]github.PRRef
	listErr    map[string]error
	dispatched []string // "repo#pr"
	gated      []string // "repo#pr"
}

func newDiscoveryHarness(t *testing.T) *discoveryHarness {
	t.Helper()
	h := &discoveryHarness{
		listResult: map[string][]github.PRRef{},
		listErr:    map[string]error{},
	}

	origList, origFetch, origDispatch, origGate := listReviewRequestedFunc, fetchPRFunc, dispatchReviewFunc, runAutoPostGateFunc
	t.Cleanup(func() {
		listReviewRequestedFunc, fetchPRFunc, dispatchReviewFunc, runAutoPostGateFunc = origList, origFetch, origDispatch, origGate
	})

	listReviewRequestedFunc = func(repo, reviewer string) ([]github.PRRef, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.listCalls = append(h.listCalls, repo)
		return h.listResult[repo], h.listErr[repo]
	}
	fetchPRFunc = func(repo string, pr int) (github.PR, error) {
		return github.PR{State: "OPEN", HeadRefOid: fmt.Sprintf("sha-%d", pr)}, nil
	}
	dispatchReviewFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, tabWriter io.Writer) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.dispatched = append(h.dispatched, fmt.Sprintf("%s#%d", rec.Repo, rec.PR))
		return nil
	}
	runAutoPostGateFunc = func(ctx context.Context, rec Record, headSHA string, store StoreInterface, cfg AutoPostConfig) (GateResult, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.gated = append(h.gated, fmt.Sprintf("%s#%d", rec.Repo, rec.PR))
		return GateResult{}, nil
	}
	return h
}

func reviewedRecord(repo string, pr int) Record {
	return Record{
		Repo:                repo,
		PR:                  pr,
		URL:                 fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr),
		Status:              StatusReviewed,
		LastReviewedSHA:     fmt.Sprintf("sha-%d", pr), // matches fetch stub → Skip
		LastServicedRequest: fmt.Sprintf("sha-%d", pr),
		EnrolledAt:          time.Now().Format(time.RFC3339),
	}
}

func TestDiscovery_DisabledByDefault(t *testing.T) {
	h := newDiscoveryHarness(t)
	h.listResult["o/r"] = []github.PRRef{{Number: 2}}
	store := &fakeStore{records: []Record{reviewedRecord("o/r", 1)}}

	w := NewWatcher(store, time.Minute, 5, "me")
	w.pollOnce()
	w.wg.Wait()

	if len(h.listCalls) != 0 {
		t.Errorf("discovery ran without being enabled: list calls %v", h.listCalls)
	}
	if recs, _ := store.List(); len(recs) != 1 {
		t.Errorf("store has %d records, want 1", len(recs))
	}
}

func TestDiscovery_EnrollsAndReviewsRequestedPRs(t *testing.T) {
	h := newDiscoveryHarness(t)
	h.listResult["o/r"] = []github.PRRef{
		{Number: 1, URL: "https://github.com/o/r/pull/1"}, // already enrolled
		{Number: 2, URL: "https://github.com/o/r/pull/2"}, // new
		{Number: 3, URL: "https://github.com/o/r/pull/3", IsDraft: true},
		{Number: 4}, // no URL in response → built from repo + number
	}
	store := &fakeStore{records: []Record{
		reviewedRecord("o/r", 1),
		reviewedRecord("o/r", 9), // second record in same repo: repo listed once
	}}

	w := NewWatcher(store, time.Minute, 5, "me")
	w.SetDiscoverRequested(true)
	w.pollOnce()
	w.wg.Wait()

	if len(h.listCalls) != 1 || h.listCalls[0] != "o/r" {
		t.Errorf("list calls = %v, want exactly [o/r]", h.listCalls)
	}

	rec2, ok, _ := store.Get("o/r", 2)
	if !ok {
		t.Fatal("PR #2 was not enrolled")
	}
	if rec2.Status != StatusWatching || rec2.URL != "https://github.com/o/r/pull/2" ||
		rec2.ReviewDir != ".worktrees/review-o-r-2" || rec2.LastReviewedSHA != "" {
		t.Errorf("unexpected discovered record: %+v", rec2)
	}
	if err := rec2.Validate(); err != nil {
		t.Errorf("discovered record fails Validate: %v", err)
	}
	if rec4, ok, _ := store.Get("o/r", 4); !ok || rec4.URL != "https://github.com/o/r/pull/4" {
		t.Errorf("PR #4 not enrolled with a built URL: %+v (found=%v)", rec4, ok)
	}
	if _, ok, _ := store.Get("o/r", 3); ok {
		t.Error("draft PR #3 should not be enrolled")
	}

	// Discovered PRs are reviewed in the same round and go through the gate,
	// exactly like manually enrolled ones. #1 and #9 are already serviced.
	sort.Strings(h.dispatched)
	sort.Strings(h.gated)
	want := []string{"o/r#2", "o/r#4"}
	if fmt.Sprint(h.dispatched) != fmt.Sprint(want) {
		t.Errorf("dispatched = %v, want %v", h.dispatched, want)
	}
	if fmt.Sprint(h.gated) != fmt.Sprint(want) {
		t.Errorf("gated = %v, want %v", h.gated, want)
	}
}

func TestDiscovery_SecondPollDoesNotReEnroll(t *testing.T) {
	h := newDiscoveryHarness(t)
	h.listResult["o/r"] = []github.PRRef{{Number: 2, URL: "https://github.com/o/r/pull/2"}}
	store := &fakeStore{records: []Record{reviewedRecord("o/r", 1)}}

	w := NewWatcher(store, time.Minute, 5, "me")
	w.SetDiscoverRequested(true)
	first := w.discoverRequestedPRs(mustList(t, store))
	second := w.discoverRequestedPRs(mustList(t, store))

	if len(first) != 1 || len(second) != 0 {
		t.Errorf("first pass added %d, second added %d; want 1 then 0", len(first), len(second))
	}
}

func TestDiscovery_EmptyReviewerSkips(t *testing.T) {
	h := newDiscoveryHarness(t)
	store := &fakeStore{records: []Record{reviewedRecord("o/r", 1)}}

	w := NewWatcher(store, time.Minute, 5, "")
	w.SetDiscoverRequested(true)
	w.pollOnce()
	w.wg.Wait()

	if len(h.listCalls) != 0 {
		t.Errorf("discovery searched with an empty reviewer: %v", h.listCalls)
	}
}

func TestDiscovery_ListErrorDoesNotBlockOtherRepos(t *testing.T) {
	h := newDiscoveryHarness(t)
	h.listErr["o/bad"] = fmt.Errorf("gh exploded")
	h.listResult["o/good"] = []github.PRRef{{Number: 5, URL: "https://github.com/o/good/pull/5"}}
	store := &fakeStore{records: []Record{
		reviewedRecord("o/bad", 1),
		reviewedRecord("o/good", 1),
	}}

	w := NewWatcher(store, time.Minute, 5, "me")
	w.SetDiscoverRequested(true)
	added := w.discoverRequestedPRs(mustList(t, store))

	if len(added) != 1 || added[0].Repo != "o/good" || added[0].PR != 5 {
		t.Errorf("added = %+v, want only o/good#5", added)
	}
}

func TestDiscovery_TeamReviewerSkips(t *testing.T) {
	h := newDiscoveryHarness(t)
	store := &fakeStore{records: []Record{reviewedRecord("o/r", 1), reviewedRecord("o/r2", 1)}}

	w := NewWatcher(store, time.Minute, 5, "org/team")
	w.SetDiscoverRequested(true)
	w.pollOnce()
	w.pollOnce()
	w.wg.Wait()

	if len(h.listCalls) != 0 {
		t.Errorf("discovery searched with a team reviewer: %v", h.listCalls)
	}
}

func TestDiscovery_DoesNotOverwriteConcurrentEnrollment(t *testing.T) {
	h := newDiscoveryHarness(t)
	store := &fakeStore{records: []Record{reviewedRecord("o/r", 1)}}

	// Simulate a manual TUI enroll of #2 landing while gh pr list runs: the
	// record appears after discovery took its snapshot.
	manual := Record{
		Repo: "o/r", PR: 2, URL: "https://github.com/o/r/pull/2",
		Status: StatusReviewing, EnrolledAt: time.Now().Format(time.RFC3339),
		ReviewDir: "manual-dir",
	}
	listReviewRequestedFunc = func(repo, reviewer string) ([]github.PRRef, error) {
		h.mu.Lock()
		h.listCalls = append(h.listCalls, repo)
		h.mu.Unlock()
		if err := store.Save(manual); err != nil {
			t.Fatalf("Save: %v", err)
		}
		return []github.PRRef{{Number: 2, URL: manual.URL}}, nil
	}

	w := NewWatcher(store, time.Minute, 5, "me")
	w.SetDiscoverRequested(true)
	added := w.discoverRequestedPRs(mustList(t, store))

	if len(added) != 0 {
		t.Errorf("discovery added %+v; want nothing (PR already enrolled)", added)
	}
	got, ok, _ := store.Get("o/r", 2)
	if !ok || got.Status != StatusReviewing || got.ReviewDir != "manual-dir" {
		t.Errorf("manual record was overwritten: %+v", got)
	}
}

func mustList(t *testing.T, s StoreInterface) []Record {
	t.Helper()
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return recs
}
