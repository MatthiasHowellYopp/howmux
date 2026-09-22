package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestReviewContentTabImplementsTabInterface is a compile-time assertion
// that *ReviewContentTab satisfies the Tab interface, mirroring the intent
// of tabs_interface_test.go's mockTab.
var _ Tab = (*ReviewContentTab)(nil)

func TestReviewContentTabImplementsTabInterface(t *testing.T) {
	// The var _ Tab assertion above already proves this at compile time;
	// this test just gives the assertion a home in the test suite so `go
	// test -run TestReviewContentTab` reports it explicitly.
}

// TestReviewContentTabTitleReflectsRepoAndPR verifies Title() returns
// exactly the string passed to the constructor — title formatting itself is
// the caller's responsibility (see the tui.go integration test for that).
func TestReviewContentTabTitleReflectsRepoAndPR(t *testing.T) {
	rct := NewReviewContentTab("review-content-owner/repo-123", "Review: owner/repo #123", "some body", true, testReviewsStyles())

	if rct.Title() != "Review: owner/repo #123" {
		t.Errorf("Title() = %q, want %q", rct.Title(), "Review: owner/repo #123")
	}
}

// TestReviewContentTabViewRendersBodyContent verifies View() renders the
// body text through the viewport once sized.
func TestReviewContentTabViewRendersBodyContent(t *testing.T) {
	body := "# Review\n\nThis is the review body.\n"
	rct := NewReviewContentTab("id", "Review: owner/repo #1", body, true, testReviewsStyles())
	rct.Resize(80, 24)

	view := rct.View()
	if !strings.Contains(view, "This is the review body.") {
		t.Errorf("View() = %q, want it to contain the body text", view)
	}
}

// TestReviewContentTabViewRendersErrorWhenNotFound verifies that when
// found=false, View() renders the missing/unreadable message and
// CopyableContent() returns the same text.
func TestReviewContentTabViewRendersErrorWhenNotFound(t *testing.T) {
	rct := NewReviewContentTab("id", "Review: owner/repo #1", "", false, testReviewsStyles())
	rct.Resize(80, 24)

	view := rct.View()
	if !strings.Contains(view, "Could not read review content") {
		t.Errorf("View() = %q, want it to contain the file-error message", view)
	}

	copyable := rct.CopyableContent()
	if !strings.Contains(copyable, "Could not read review content") {
		t.Errorf("CopyableContent() = %q, want it to contain the file-error message", copyable)
	}
}

// TestReviewContentTabCopyableContentReturnsPlainBody verifies
// CopyableContent() equals the exact body passed in, with no ANSI codes —
// the plain-text contract every other tab's CopyableContent() upholds.
func TestReviewContentTabCopyableContentReturnsPlainBody(t *testing.T) {
	body := "exact body text\nwith multiple lines\n"
	rct := NewReviewContentTab("id", "Review: owner/repo #1", body, true, testReviewsStyles())

	got := rct.CopyableContent()
	if got != body {
		t.Errorf("CopyableContent() = %q, want %q", got, body)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("CopyableContent() contains ANSI escape codes: %q", got)
	}
}

// TestReviewContentTabIsClosable verifies review content windows are always
// closable.
func TestReviewContentTabIsClosable(t *testing.T) {
	rct := NewReviewContentTab("id", "title", "body", true, testReviewsStyles())
	if !rct.IsClosable() {
		t.Error("expected IsClosable() to be true")
	}
}

// TestReviewContentTabUpdateForwardsKeysToViewportForScrolling verifies key
// messages are forwarded to the underlying viewport, changing its scroll
// offset, consistent with how log_tab.go itself reads YOffset()/
// TotalLineCount() internally for isNearBottom().
func TestReviewContentTabUpdateForwardsKeysToViewportForScrolling(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line of review content")
	}
	body := strings.Join(lines, "\n")

	rct := NewReviewContentTab("id", "title", body, true, testReviewsStyles())
	rct.Resize(20, 5) // small viewport so 200 lines requires scrolling

	if rct.viewport.TotalLineCount() <= rct.height {
		t.Fatalf("expected content to exceed viewport height so scrolling is possible; total=%d height=%d", rct.viewport.TotalLineCount(), rct.height)
	}

	before := rct.viewport.YOffset()

	tab, _ := rct.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	rct = tab.(*ReviewContentTab)
	tab, _ = rct.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	rct = tab.(*ReviewContentTab)

	after := rct.viewport.YOffset()
	if after <= before {
		t.Errorf("expected YOffset() to increase after down/pgdown keys, before=%d after=%d", before, after)
	}
}

// TestReviewContentTabCaptureAndRestoreFocusStateAlwaysFooter verifies focus
// state mirrors LogTab: always footer, no-op restore.
func TestReviewContentTabCaptureAndRestoreFocusStateAlwaysFooter(t *testing.T) {
	rct := NewReviewContentTab("id", "title", "body", true, testReviewsStyles())

	if rct.CaptureFocusState() != FocusTargetFooter {
		t.Errorf("CaptureFocusState() = %v, want FocusTargetFooter", rct.CaptureFocusState())
	}
	if cmd := rct.RestoreFocusState(FocusTargetFooter); cmd != nil {
		t.Error("expected RestoreFocusState to be a no-op returning nil")
	}
}

// TestReviewContentTabResizeUpdatesViewportDimensions verifies Resize
// updates the tab's tracked dimensions and does not panic on 0/negative
// dimensions, matching defensive patterns elsewhere in the package (e.g.
// ReviewsTab.padToHeight's height <= 0 guard).
func TestReviewContentTabResizeUpdatesViewportDimensions(t *testing.T) {
	rct := NewReviewContentTab("id", "title", "some body content", true, testReviewsStyles())

	rct.Resize(100, 30)
	if rct.width != 100 || rct.height != 30 {
		t.Errorf("after Resize(100, 30): width=%d height=%d, want 100/30", rct.width, rct.height)
	}

	dims := [][2]int{{0, 0}, {-1, -1}, {1, 1}, {200, 50}}
	for _, d := range dims {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Resize(%d, %d) panicked: %v", d[0], d[1], r)
				}
			}()
			rct.Resize(d[0], d[1])
		}()
	}
}

// wrappedLineCount returns the number of newline-delimited lines in the
// ANSI-stripped view output, used by the wrap tests below to assert on
// visual line counts without being tripped up by styling escape codes.
func wrappedLineCount(view string) int {
	stripped := ansi.Strip(view)
	return strings.Count(stripped, "\n") + 1
}

// TestReviewContentTabViewWrapsLongLineButCopyableContentStaysUnwrapped
// verifies AC1: a long single line (with and without spaces to break on)
// wraps across multiple visual lines at a known narrow width, while
// CopyableContent() still returns the original unwrapped single-line
// string exactly.
func TestReviewContentTabViewWrapsLongLineButCopyableContentStaysUnwrapped(t *testing.T) {
	longLine := strings.Repeat("a very long finding line with words to wrap ", 6)

	rct := NewReviewContentTab("id", "Review: owner/repo #1", longLine, true, testReviewsStyles())
	rct.Resize(40, 24)

	view := rct.View()
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "\n") {
		t.Errorf("View() = %q, want wrapped output containing multiple lines", stripped)
	}
	if wrappedLineCount(view) <= 1 {
		t.Errorf("wrappedLineCount(view) = %d, want > 1 at width 40 for a long line", wrappedLineCount(view))
	}

	copyable := rct.CopyableContent()
	if copyable != longLine {
		t.Errorf("CopyableContent() = %q, want unwrapped original %q", copyable, longLine)
	}
}

// TestReviewContentTabViewWrapsLongUnbrokenToken verifies that a single
// unbroken token (no spaces) longer than the width — e.g. a long file:line
// prefix — still wraps mid-token via lipgloss.Wrap, matching AC1's
// "onto the next line(s)" requirement even without word-boundary breaks.
func TestReviewContentTabViewWrapsLongUnbrokenToken(t *testing.T) {
	longToken := strings.Repeat("x", 200)

	rct := NewReviewContentTab("id", "Review: owner/repo #1", longToken, true, testReviewsStyles())
	rct.Resize(40, 24)

	view := rct.View()
	if wrappedLineCount(view) <= 1 {
		t.Errorf("wrappedLineCount(view) = %d, want > 1 for a 200-char unbroken token at width 40", wrappedLineCount(view))
	}

	copyable := rct.CopyableContent()
	if copyable != longToken {
		t.Errorf("CopyableContent() = %q, want unwrapped original %q", copyable, longToken)
	}
}

// TestReviewContentTabResizeReflowsWrappedLineCount verifies AC2: Resize
// re-wraps stored content on every call, not just the first. Narrowing
// increases the wrapped line count, widening decreases it, and narrowing
// again increases it back up. Uses viewport.TotalLineCount() rather than
// View() output because View() pads/clips to the viewport's fixed height,
// which would mask the actual wrapped content line count once it exceeds
// the viewport height.
func TestReviewContentTabResizeReflowsWrappedLineCount(t *testing.T) {
	longLine := strings.Repeat("word ", 60)
	rct := NewReviewContentTab("id", "Review: owner/repo #1", longLine, true, testReviewsStyles())

	rct.Resize(20, 24)
	narrowCount := rct.viewport.TotalLineCount()

	rct.Resize(200, 24)
	wideCount := rct.viewport.TotalLineCount()

	if wideCount >= narrowCount {
		t.Errorf("after widening: wideCount=%d, want < narrowCount=%d", wideCount, narrowCount)
	}

	rct.Resize(20, 24)
	narrowAgainCount := rct.viewport.TotalLineCount()

	if narrowAgainCount <= wideCount {
		t.Errorf("after re-narrowing: narrowAgainCount=%d, want > wideCount=%d", narrowAgainCount, wideCount)
	}
}

// TestReviewContentTabViewWrapsErrorMessageButCopyableContentStaysUnwrapped
// verifies AC4: the found=false error path wraps in View() at a narrow
// width when the title is long enough to push the error message past that
// width, while CopyableContent() still returns the exact raw error message.
func TestReviewContentTabViewWrapsErrorMessageButCopyableContentStaysUnwrapped(t *testing.T) {
	longTitle := "Review: a-very-long-organization-name/a-very-long-repository-name #123456"
	rct := NewReviewContentTab("id", longTitle, "", false, testReviewsStyles())
	rct.Resize(20, 24)

	wantErrMsg := "Could not read review content for " + longTitle + " (spool file missing or unreadable)."

	view := rct.View()
	if wrappedLineCount(view) <= 1 {
		t.Errorf("wrappedLineCount(view) = %d, want > 1 for the error message at width 20 with a long title", wrappedLineCount(view))
	}

	copyable := rct.CopyableContent()
	if copyable != wantErrMsg {
		t.Errorf("CopyableContent() = %q, want exact error message %q", copyable, wantErrMsg)
	}
}
