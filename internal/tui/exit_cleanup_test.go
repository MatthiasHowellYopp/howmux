package tui

import (
	"testing"
	"time"
)

// TestPerformExitCleanupIsBounded verifies the Ctrl+C hard-quit cleanup path
// completes promptly and closes planning tabs in parallel under an overall
// deadline, rather than blocking sequentially on each tab's ACP shutdown.
//
// Note: test planning tabs have no ACP client, so their Close() returns fast;
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
