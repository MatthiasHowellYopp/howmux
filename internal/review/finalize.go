package review

import (
	"errors"
	"path/filepath"
	"sync"
)

// ErrAlreadyFinalized is returned (wrapped) when an action targets a review
// that has already been archived to done/: someone else posted or discarded
// it first. The refusal is correct behavior, not a failure; callers that log
// outcomes use errors.Is to record it as a guard stop rather than an error.
var ErrAlreadyFinalized = errors.New("review already finalized (archived to done/)")

// ErrReviewBusy is returned when another post, discard, revise or rereview is
// already running on the same review. Like ErrAlreadyFinalized, it is a guard
// stop, not a failure.
var ErrReviewBusy = errors.New("another action is already running on this review")

// IsGuardStop reports whether err is a refusal by one of the guards above:
// the review was already finalized, or another action holds it. Audit
// records log these as "fail" (a guard stopped the action, the workflow
// working as designed), not "error" (something broke).
func IsGuardStop(err error) bool {
	return errors.Is(err, ErrAlreadyFinalized) || errors.Is(err, ErrReviewBusy)
}

// finalizeLocks serializes the actions that rewrite or archive a spool file
// (post, discard, revise, rereview), one lock per review. Without it two
// finalizers could both pass PostReview's "not yet in done/" check during the
// seconds a post takes, and both post to GitHub. Keyed by the spool file's
// base name, which is the same in pending/ and done/.
var finalizeLocks = struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}{m: map[string]*sync.Mutex{}}

// lockFinalize takes the review's finalize lock without waiting. It returns
// ErrReviewBusy if another action holds it. The caller must call unlock.
func lockFinalize(spoolPath string) (unlock func(), err error) {
	key := filepath.Base(spoolPath)
	finalizeLocks.mu.Lock()
	l, ok := finalizeLocks.m[key]
	if !ok {
		l = &sync.Mutex{}
		finalizeLocks.m[key] = l
	}
	finalizeLocks.mu.Unlock()

	if !l.TryLock() {
		return nil, ErrReviewBusy
	}
	return l.Unlock, nil
}
