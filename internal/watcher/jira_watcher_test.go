package watcher

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jbrinkman/kiro-krew/internal/config"
	"github.com/jbrinkman/kiro-krew/internal/jira"
)

// fakeJira is a test double for jiraLister.
type fakeJira struct {
	mu        sync.Mutex
	issues    []jira.Issue
	listCalls int
	gotCalls  int
	getKeys   []string
	getErr    map[string]error // per-key errors from GetIssue
}

func (f *fakeJira) ListTodoIssues(jql string, max int) ([]jira.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	return f.issues, nil
}

func (f *fakeJira) GetIssue(key string) (*jira.IssueDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotCalls++
	f.getKeys = append(f.getKeys, key)
	if f.getErr != nil {
		if err, ok := f.getErr[key]; ok {
			return nil, err
		}
	}
	return &jira.IssueDetails{Key: key, FullText: "body of " + key}, nil
}

func (f *fakeJira) counts() (list, get int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, f.gotCalls
}

// fakeStore is an in-memory jiraStore for tests.
type fakeStore struct {
	mu        sync.Mutex
	saved     map[string]bool
	saveCalls int
	preSeeded map[string]bool // keys considered already-stored (sentinel hit)
}

func newFakeStore() *fakeStore {
	return &fakeStore{saved: make(map[string]bool), preSeeded: make(map[string]bool)}
}

func (s *fakeStore) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved[key] || s.preSeeded[key]
}

func (s *fakeStore) Save(issue jira.Issue, details *jira.IssueDetails) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	s.saved[issue.Key] = true
	return "/tmp/" + issue.Key + ".md", nil
}

func (s *fakeStore) counts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCalls
}

func newTestJiraWatcher(f *fakeJira) *JiraWatcher {
	return newTestJiraWatcherWithStore(f, newFakeStore())
}

func newTestJiraWatcherWithStore(f *fakeJira, s jiraStore) *JiraWatcher {
	cfg := &config.Config{PollInterval: 10 * time.Millisecond}
	cfg.Jira = config.JiraConfig{BoardURL: "https://example.atlassian.net/b/1", JQL: "assignee = currentUser()"}
	return &JiraWatcher{
		config:  cfg,
		client:  f,
		store:   s,
		tracked: make(map[string]bool),
	}
}

// waitUntilStopped polls Running() until false or the deadline.
func waitUntilStopped(t *testing.T, w *JiraWatcher, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !w.Running() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("watcher did not stop within deadline")
}

func TestJiraWatcher_BoundedSingleShot(t *testing.T) {
	f := &fakeJira{issues: []jira.Issue{
		{Key: "AEA-1", Status: "To Do", Summary: "one"},
		{Key: "AEA-2", Status: "To Do", Summary: "two"},
	}}
	s := newFakeStore()
	w := newTestJiraWatcherWithStore(f, s)

	w.RunBounded(1)
	waitUntilStopped(t, w, time.Second)

	list, get := f.counts()
	if list != 1 {
		t.Errorf("list calls = %d, want 1", list)
	}
	if get != 2 {
		t.Errorf("get calls = %d, want 2 (one per issue)", get)
	}
	if s.counts() != 2 {
		t.Errorf("save calls = %d, want 2 (both tickets persisted)", s.counts())
	}
}

func TestJiraWatcher_SkipsAlreadyStored(t *testing.T) {
	f := &fakeJira{issues: []jira.Issue{
		{Key: "AEA-1", Status: "To Do", Summary: "already have"},
		{Key: "AEA-2", Status: "To Do", Summary: "new"},
	}}
	s := newFakeStore()
	s.preSeeded["AEA-1"] = true // sentinel: pretend AEA-1 was saved on a prior run
	w := newTestJiraWatcherWithStore(f, s)

	w.RunBounded(1)
	waitUntilStopped(t, w, time.Second)

	_, get := f.counts()
	if get != 1 {
		t.Errorf("get calls = %d, want 1 (AEA-1 skipped via sentinel)", get)
	}
	if s.counts() != 1 {
		t.Errorf("save calls = %d, want 1 (only AEA-2 saved)", s.counts())
	}
	if len(f.getKeys) != 1 || f.getKeys[0] != "AEA-2" {
		t.Errorf("retrieved keys = %v, want [AEA-2]", f.getKeys)
	}
}

func TestJiraWatcher_DedupAcrossIterations(t *testing.T) {
	f := &fakeJira{issues: []jira.Issue{
		{Key: "AEA-1", Status: "To Do", Summary: "one"},
	}}
	w := newTestJiraWatcher(f)

	// Two iterations, same issue each time -> retrieved only once.
	w.RunBounded(2)
	waitUntilStopped(t, w, 2*time.Second)

	list, get := f.counts()
	if list != 2 {
		t.Errorf("list calls = %d, want 2", list)
	}
	if get != 1 {
		t.Errorf("get calls = %d, want 1 (deduped)", get)
	}
}

func TestJiraWatcher_RetrievalFailureAllowsRetry(t *testing.T) {
	f := &fakeJira{
		issues:  []jira.Issue{{Key: "AEA-1", Status: "To Do", Summary: "one"}},
		getErr:  map[string]error{"AEA-1": fmt.Errorf("transient")},
	}
	w := newTestJiraWatcher(f)

	// First iteration: GetIssue fails, so AEA-1 is un-tracked.
	w.RunBounded(1)
	waitUntilStopped(t, w, time.Second)
	_, get1 := f.counts()
	if get1 != 1 {
		t.Fatalf("get calls after first run = %d, want 1", get1)
	}

	// Clear the error and run again: it should retry (not be deduped away).
	f.mu.Lock()
	f.getErr = nil
	f.mu.Unlock()

	w.RunBounded(1)
	waitUntilStopped(t, w, time.Second)
	_, get2 := f.counts()
	if get2 != 2 {
		t.Errorf("get calls after retry = %d, want 2 (retry happened)", get2)
	}
}

func TestJiraWatcher_StartStop(t *testing.T) {
	f := &fakeJira{issues: nil}
	w := newTestJiraWatcher(f)

	if w.Running() {
		t.Fatal("watcher should not be running before Start")
	}
	w.Start()
	if !w.Running() {
		t.Fatal("watcher should be running after Start")
	}
	w.Stop()
	if w.Running() {
		t.Error("watcher should not be running after Stop")
	}
}
