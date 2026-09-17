package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
