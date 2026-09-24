package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"charm.land/lipgloss/v2"
)

// maxVisibleLineWidth returns the widest visible (ANSI-stripped, cell-measured)
// line in view. Used by the wrap tests to assert that no rendered line spills
// past the viewport width.
func maxVisibleLineWidth(view string) int {
	max := 0
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}

// TestPlanningMessagesWrapToWidth is the regression test for the reported bug:
// planner text extended past the right edge of the panel with no way to read
// it. The viewport clips/scrolls horizontally rather than wrapping, so message
// content must be wrapped to the tab width before it reaches the viewport. A
// long assistant line (no early spaces to break on) and a long user line must
// both render with no visible line wider than the tab width.
func TestPlanningMessagesWrapToWidth(t *testing.T) {
	const width = 40

	cases := []struct {
		name string
		role string
		text string
	}{
		{
			name: "assistant long spaced line",
			role: "assistant",
			text: strings.Repeat("this is a long planner sentence that must wrap ", 8),
		},
		{
			name: "user long spaced line",
			role: "user",
			text: strings.Repeat("a long user question that keeps going and going ", 8),
		},
		{
			name: "assistant very long unbroken token",
			role: "assistant",
			text: strings.Repeat("x", 300),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			styles := NewStyles(createMinimalTheme())
			pt := NewPlanningTabWithSession("wrap-test", "Wrap Test", styles, NewContextTracker(), nil, nil)
			pt.Resize(width, 20)

			pt.AddMessage(tc.role, tc.text)

			view := pt.View()
			if got := maxVisibleLineWidth(view); got > width {
				t.Errorf("widest visible line = %d cells, want <= %d (text overflowed the panel)", got, width)
			}
			if wrappedLineCount(view) <= 1 {
				t.Errorf("expected the long line to wrap across multiple visual lines, got %d line(s)", wrappedLineCount(view))
			}
		})
	}
}

// TestPlanningStreamingResponseWrapsToWidth verifies the in-progress streaming
// response (rendered with the "● " indicator prefix) also wraps within the tab
// width — the indicator gutter is reserved so the first wrapped line does not
// push past the right edge.
func TestPlanningStreamingResponseWrapsToWidth(t *testing.T) {
	const width = 40
	styles := NewStyles(createMinimalTheme())
	pt := NewPlanningTabWithSession("wrap-stream-test", "Wrap Stream", styles, NewContextTracker(), nil, nil)
	pt.Resize(width, 20)

	pt.streamingResponse = true
	pt.currentResponse.WriteString(strings.Repeat("streaming planner output that should wrap cleanly ", 8))
	pt.updateViewportContent()

	view := pt.View()
	if got := maxVisibleLineWidth(view); got > width {
		t.Errorf("widest visible streaming line = %d cells, want <= %d", got, width)
	}
}

// TestPlanningResizeReflowsExistingContent verifies that shrinking the terminal
// re-wraps already-rendered messages to the new, narrower width instead of
// leaving them wrapped to the old width (which would overflow after a shrink).
func TestPlanningResizeReflowsExistingContent(t *testing.T) {
	styles := NewStyles(createMinimalTheme())
	pt := NewPlanningTabWithSession("reflow-test", "Reflow", styles, NewContextTracker(), nil, nil)

	// Render wide first, then shrink.
	pt.Resize(120, 20)
	pt.AddMessage("assistant", strings.Repeat("reflow me across a resize boundary ", 8))

	const narrow = 30
	pt.Resize(narrow, 20)

	view := pt.View()
	if got := maxVisibleLineWidth(view); got > narrow {
		t.Errorf("after shrink to %d, widest visible line = %d cells, want <= %d (content did not reflow)", narrow, got, narrow)
	}
}
