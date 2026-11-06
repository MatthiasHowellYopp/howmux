package review

import (
	"fmt"
	"sync"
)

// inflight records which PRs have a review (and its auto-post gate) running,
// across every path that starts one: the watcher's dispatch and the manual
// `review <URL>` command. The watcher's own activeReviews map only covered
// its dispatches, so a manual review left the PR looking never-reviewed to
// the next poll, and the watcher started a second review of the same PR at
// the same SHA. Both then wrote the same spool file and ran the gate.
var inflight = struct {
	mu sync.Mutex
	m  map[string]bool
}{m: map[string]bool{}}

func inflightKey(repo string, pr int) string { return fmt.Sprintf("%s#%d", repo, pr) }

// ClaimReview marks repo#pr as having a review in flight. ok is false if one
// already is; the caller must not start another. release clears the claim
// and is safe to call more than once. Hold the claim through the auto-post
// gate, so a human can't decide on a review the gate is still acting on.
func ClaimReview(repo string, pr int) (release func(), ok bool) {
	key := inflightKey(repo, pr)
	inflight.mu.Lock()
	defer inflight.mu.Unlock()
	if inflight.m[key] {
		return nil, false
	}
	inflight.m[key] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			inflight.mu.Lock()
			delete(inflight.m, key)
			inflight.mu.Unlock()
		})
	}, true
}

// ReviewInFlight reports whether repo#pr has a review or auto-post gate
// running.
func ReviewInFlight(repo string, pr int) bool {
	inflight.mu.Lock()
	defer inflight.mu.Unlock()
	return inflight.m[inflightKey(repo, pr)]
}
