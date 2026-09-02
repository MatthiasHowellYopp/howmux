package tui

import (
	"strings"
	"testing"
)

// makeStreamingPlanningTab builds a planning tab with a small viewport and a
// long accumulated streaming response, so content overflows the visible area.
func makeStreamingPlanningTab(t *testing.T, lines int) *PlanningTab {
	t.Helper()
	styles := NewStyles(createMinimalTheme())
	pt := NewPlanningTabWithSession("scroll-test", "Scroll Test", styles, NewContextTracker(), nil, nil)

	// Small viewport so any multi-line content overflows.
	pt.Resize(80, 6)

	// Simulate an in-progress streamed response taller than the viewport.
	pt.streamingResponse = true
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString("streaming line ")
		b.WriteString(strings.Repeat("x", 10))
		b.WriteString("\n")
	}
	pt.currentResponse.WriteString(b.String())
	return pt
}

// TestAutoScrollPinsToBottomWhileStreaming is the regression test for the bug
// where planner output became invisible once the response grew past one screen:
// while streaming, the viewport must stay pinned to the bottom.
func TestAutoScrollPinsToBottomWhileStreaming(t *testing.T) {
	pt := makeStreamingPlanningTab(t, 50)

	// Force the viewport away from the bottom to simulate falling behind.
	pt.viewport.GotoTop()

	// New chunk arrives -> content rebuild should re-pin to bottom.
	pt.updateViewportContent()

	if !pt.viewport.AtBottom() {
		t.Errorf("expected viewport pinned to bottom while streaming, ScrollPercent=%.2f", pt.viewport.ScrollPercent())
	}
}

// TestNoAutoScrollWhenNotStreamingAndScrolledUp verifies that when NOT streaming
// and the user has scrolled up to read history, a content rebuild does not yank
// them back to the bottom.
func TestNoAutoScrollWhenNotStreamingAndScrolledUp(t *testing.T) {
	pt := makeStreamingPlanningTab(t, 50)
	// Finish streaming: move the buffered response into a message.
	pt.streamingResponse = false
	pt.AddMessage("assistant", pt.currentResponse.String())
	pt.currentResponse.Reset()
	// Add more turns so this is a real multi-message history (the len==1
	// first-render special case does not apply here).
	pt.AddMessage("user", "another question")
	pt.AddMessage("assistant", strings.Repeat("more history line\n", 50))

	// User scrolls up to read earlier content.
	pt.viewport.GotoTop()

	// A content rebuild (e.g. a resize/redraw) must not force us to the bottom.
	pt.updateViewportContent()

	if pt.viewport.AtBottom() {
		t.Error("expected viewport to stay scrolled up when not streaming, but it jumped to bottom")
	}
}
