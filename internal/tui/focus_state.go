package tui

// FocusTarget represents which input field should have focus
type FocusTarget string

const (
	// FocusTargetFooter indicates the footer input should have focus
	FocusTargetFooter FocusTarget = "footer"

	// FocusTargetMessage indicates the planning tab message input should have focus
	FocusTargetMessage FocusTarget = "message"

	// FocusTargetRows indicates a tab's own row-selection/content UI should
	// have focus rather than the footer input — used by ReviewsTab so that
	// row navigation and decision shortcuts (up/down/enter/p/r/R/d) are the
	// default, reachable state when the tab becomes active, with the footer
	// input as the explicit exception (toggled via Tab) rather than the
	// default. See ReviewsTab.CaptureFocusState and switchActiveTab in
	// tui.go.
	FocusTargetRows FocusTarget = "rows"
)

// String returns the string representation of the focus target
func (ft FocusTarget) String() string {
	return string(ft)
}

// IsValid checks if the focus target is a valid value
func (ft FocusTarget) IsValid() bool {
	switch ft {
	case FocusTargetFooter, FocusTargetMessage, FocusTargetRows:
		return true
	default:
		return false
	}
}
