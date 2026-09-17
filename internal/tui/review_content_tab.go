package tui

import (
	"fmt"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// ReviewContentTab implements the Tab interface for displaying the full,
// read-only markdown body of a single PR review's spool file. It is always
// closable and holds no reference back to ReviewsTab or TabManager — it is
// constructed with everything it needs (title text + body text) and knows
// nothing about how it was opened or how ESC will close it; that
// orchestration lives in model.Update (see openReviewContentMsg handling),
// matching the existing separation where tabs never reach back into the
// manager that holds them.
type ReviewContentTab struct {
	id       string
	title    string
	viewport viewport.Model
	width    int
	height   int

	// plainContent is the exact body text set at construction (or by a
	// future Reload), independent of any styling applied in View(). Mirrors
	// LogTab's separation between the styled viewport content and
	// CopyableContent()'s plain-text return.
	plainContent string

	// errMsg is set instead of plainContent when the spool file could not be
	// read (AC6); View() renders it via styles.Error, CopyableContent()
	// returns it verbatim. Mutually exclusive with a populated plainContent.
	errMsg string
	styles *Styles
}

// NewReviewContentTab creates a review content window tab. id should be
// unique per window (see openReviewContentMsg handling for the id scheme).
// title is the fully-formed display title (e.g. "Review: owner/repo #123"),
// already formatted by the caller so this constructor stays free of
// repo/PR-number formatting concerns.
//
// body is the spool body text (already resolved by the caller via
// review.ReadSpoolBody); found reports whether the spool file was located
// and read successfully. When found is false, the tab renders a
// file-error/missing message instead of body, per AC6 — callers pass found
// rather than an error value because the only failure mode ReadSpoolBody
// surfaces to a caller is "not found / unreadable", already collapsed into
// SpoolInfo.Found by that function.
func NewReviewContentTab(id, title, body string, found bool, styles *Styles) *ReviewContentTab {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	vp.MouseWheelEnabled = true

	rct := &ReviewContentTab{
		id:     id,
		title:  title,
		styles: styles,
	}

	if !found {
		rct.errMsg = fmt.Sprintf("Could not read review content for %s (spool file missing or unreadable).", title)
		if styles != nil {
			vp.SetContent(styles.Error.Render(rct.errMsg))
		} else {
			vp.SetContent(rct.errMsg)
		}
		rct.viewport = vp
		return rct
	}

	rct.plainContent = body
	vp.SetContent(body)
	rct.viewport = vp
	return rct
}

// ID returns the tab identifier.
func (rct *ReviewContentTab) ID() string { return rct.id }

// Type returns the tab type.
func (rct *ReviewContentTab) Type() TabType { return TabTypeReviewContent }

// Title returns the tab title.
func (rct *ReviewContentTab) Title() string { return rct.title }

// IsClosable returns whether this tab can be closed. Review content windows
// are always closable — they are opened on demand from the Reviews tab and
// closed via ESC (see the ESC handling in tui.go's model.Update).
func (rct *ReviewContentTab) IsClosable() bool { return true }

// View returns the tab's rendered content.
func (rct *ReviewContentTab) View() string { return rct.viewport.View() }

// CopyableContent returns the raw body text (or the error message if the
// spool could not be read), matching LogTab's contract of returning the true
// underlying buffer rather than the rendered/styled viewport content.
func (rct *ReviewContentTab) CopyableContent() string {
	if rct.errMsg != "" {
		return rct.errMsg
	}
	return rct.plainContent
}

// Update forwards key events to the viewport for scrolling, exactly as
// LogTab.Update does. This tab has no ring buffer, no tea.Tick polling, and
// no state that changes over time, so there is no equivalent of LogTab's
// TickMsg/refreshContent branch. ESC is intentionally NOT handled here —
// closing this tab requires removing it from TabManager and switching the
// active tab back to Reviews, neither of which this tab can do on its own
// (see Tab interface note above), so ESC is handled at the top level in
// model.Update, matching how TabTypeReviewContent's closability is already
// orchestrated externally.
func (rct *ReviewContentTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	var cmd tea.Cmd
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		rct.viewport, cmd = rct.viewport.Update(keyMsg)
	}
	return rct, cmd
}

// Resize updates the tab dimensions, matching LogTab.Resize.
func (rct *ReviewContentTab) Resize(width, height int) {
	rct.width = width
	rct.height = height
	rct.viewport.SetWidth(width)
	rct.viewport.SetHeight(height)
}

// CaptureFocusState / RestoreFocusState: this tab has no internal focusable
// widget distinct from the footer (scrolling is driven by raw arrow/pgup/
// pgdown keys forwarded through Update, same as LogTab) — always footer,
// matching LogTab exactly.
func (rct *ReviewContentTab) CaptureFocusState() FocusTarget { return FocusTargetFooter }

// RestoreFocusState is a no-op, matching LogTab: this tab has no internal
// focus to restore, focus is always on the footer.
func (rct *ReviewContentTab) RestoreFocusState(target FocusTarget) tea.Cmd { return nil }
