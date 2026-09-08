package tui

import (
	"testing"
	"time"

	"github.com/jbrinkman/kiro-krew/internal/session"
)

// TestPerformExitCleanupIsBounded verifies the Ctrl+C hard-quit cleanup path
// completes promptly and closes planning tabs in parallel under an overall
// deadline, rather than blocking sequentially on each tab's ACP shutdown.
//
// Note: test planning tabs have no ACP client, so their close returns fast;
// this test therefore guards the structural property (cleanup returns well
// under the sequential worst case and never hangs) rather than exercising a
// real 3s-per-tab ACP shutdown. The parallel-with-deadline structure is what
// keeps the escape hatch fast when real ACP clients are attached.
func TestPerformExitCleanupIsBounded(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)

	// Add several more planning tabs so a serial regression would be visible.
	ct := NewContextTracker()
	for i := 0; i < 5; i++ {
		pt := NewPlanningTabWithSession("bound-test-"+time.Now().Format("150405.000000"), "Bound Test", m.styles, ct, nil, nil)
		m.tabManager.AddTab(pt)
	}

	// Tighten the deadline for the test; restore after.
	orig := exitCleanupTimeout
	exitCleanupTimeout = 2 * time.Second
	t.Cleanup(func() { exitCleanupTimeout = orig })

	start := time.Now()
	done := make(chan struct{})
	go func() {
		_ = m.performExitCleanup()
		close(done)
	}()

	select {
	case <-done:
		// Must finish comfortably within the deadline (fast closes + parallelism).
		if elapsed := time.Since(start); elapsed > exitCleanupTimeout+time.Second {
			t.Errorf("performExitCleanup took %s, expected well under %s", elapsed, exitCleanupTimeout)
		}
	case <-time.After(exitCleanupTimeout + 2*time.Second):
		t.Fatal("performExitCleanup hung past the bounded deadline")
	}
}

// TestPerformExitCleanupDeletesIdleSessionSynchronously verifies that session
// cleanup for an idle tab happens on exit and — critically — is done on the
// calling goroutine, not inside the bounded parallel-close goroutines. Keeping
// all session-directory I/O single-threaded is what removes the filesystem race
// between per-tab cleanup and the CleanupSessionsOnExit sweep on the timeout
// path (which the bounded-close structure alone did not fix).
func TestPerformExitCleanupDeletesIdleSessionSynchronously(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)

	// Back the tab with a real session manager on a temp dir and an idle session.
	dir := t.TempDir()
	sm := session.NewSessionManagerWithDir(dir)
	sessionID, _, err := sm.CreatePlanningSession("sync-clean-tab", "Sync Clean")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Sanity: the session exists before cleanup.
	if _, err := sm.Load(sessionID); err != nil {
		t.Fatalf("session should exist before cleanup: %v", err)
	}

	pt := activePlanningTab(t, m)
	pt.sessionManager = sm
	pt.sessionID = sessionID
	// Idle state is the "not preserved" case that CleanupSession deletes.
	pt.state = session.PlanningStateIdle

	_ = m.performExitCleanup()

	// After exit cleanup the idle session must be gone, and because cleanup is
	// synchronous it is guaranteed complete by the time performExitCleanup
	// returns (no lingering goroutine still touching the sessions dir).
	if _, err := sm.Load(sessionID); err == nil {
		t.Error("idle session should have been deleted synchronously during exit cleanup")
	}
}
