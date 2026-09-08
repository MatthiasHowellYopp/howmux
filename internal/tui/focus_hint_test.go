package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestFocusHintFitsWidth is a regression test for the bug where the focus hint
// was appended to the single input line unconditionally, so on narrow terminals
// it wrapped and pushed the planning view into the footer. The rendered input
// line must never exceed pt.width.
func TestFocusHintFitsWidth(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	pt := activePlanningTab(t, m)

	// Message-focused is the longest-hint case.
	pt.focusTarget = FocusTargetMessage

	for _, width := range []int{20, 30, 40, 60, 80, 120} {
		pt.Resize(width, 24)
		line := pt.renderInputArea()
		if got := lipgloss.Width(line); got > width {
			t.Errorf("width=%d: rendered input line width=%d exceeds terminal width (would wrap)", width, got)
		}
	}

	// At a comfortable width the full hint (with the copy/paste keys) should show.
	pt.Resize(120, 24)
	wide := pt.renderInputArea()
	if !strings.Contains(wide, "Ctrl+Y copy") || !strings.Contains(wide, "Ctrl+V paste") {
		t.Errorf("wide render should include the full hint, got: %q", wide)
	}
}
