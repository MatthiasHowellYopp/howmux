package tui

import tea "charm.land/bubbletea/v2"

// TabType represents the different types of tabs
type TabType int

const (
	TabTypeMain TabType = iota
	TabTypeAgent
	TabTypePlanning
	TabTypeLog
	TabTypeReviews
	TabTypeReviewContent
)

// String returns the string representation of the TabType
func (t TabType) String() string {
	switch t {
	case TabTypeMain:
		return "Main"
	case TabTypeAgent:
		return "Agent"
	case TabTypePlanning:
		return "Planning"
	case TabTypeLog:
		return "Log"
	case TabTypeReviews:
		return "Reviews"
	case TabTypeReviewContent:
		return "ReviewContent"
	default:
		return "Unknown"
	}
}

// Tab interface defines the contract for all tab implementations
type Tab interface {
	ID() string
	Type() TabType
	Title() string
	IsClosable() bool
	View() string
	Update(tea.Msg) (Tab, tea.Cmd)
	Resize(width, height int)

	// CopyableContent returns the tab's underlying plain, unstyled text for
	// clipboard copy (Ctrl+Y), or "" if the tab has nothing to copy. This is the
	// real buffer content, not the rendered/visible viewport.
	CopyableContent() string

	// Focus state management
	CaptureFocusState() FocusTarget
	RestoreFocusState(target FocusTarget) tea.Cmd
}
