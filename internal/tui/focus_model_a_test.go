package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// activePlanningTab returns the active tab as *PlanningTab, failing the test if
// the active tab is not a planning tab.
func activePlanningTab(t *testing.T, m model) *PlanningTab {
	t.Helper()
	active := m.tabManager.GetActiveTab()
	if active == nil {
		t.Fatal("no active tab")
	}
	pt, ok := active.(*PlanningTab)
	if !ok {
		t.Fatalf("active tab is not a planning tab: %T", active)
	}
	return pt
}

// assertFocusInSync verifies the three focus stores agree for the active
// planning tab: m.input.Focused(), pt.focusTarget, and m.tabFocusStates[id].
func assertFocusInSync(t *testing.T, m model, wantFooterFocused bool) {
	t.Helper()
	pt := activePlanningTab(t, m)

	if got := m.input.Focused(); got != wantFooterFocused {
		t.Errorf("footer focus = %v, want %v", got, wantFooterFocused)
	}

	wantTarget := FocusTargetMessage
	if wantFooterFocused {
		wantTarget = FocusTargetFooter
	}
	if pt.focusTarget != wantTarget {
		t.Errorf("pt.focusTarget = %v, want %v", pt.focusTarget, wantTarget)
	}
	if got := m.tabFocusStates[pt.ID()]; got != wantTarget {
		t.Errorf("tabFocusStates[%s] = %v, want %v", pt.ID(), got, wantTarget)
	}
}

// TestPlanningTabStartsFooterFocused verifies Model A: a new planning tab starts
// with the footer command line focused, not the message input.
func TestPlanningTabStartsFooterFocused(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	// A freshly created planning tab must default to footer focus.
	pt := activePlanningTab(t, m)
	if pt.focusTarget != FocusTargetFooter {
		t.Errorf("new planning tab focusTarget = %v, want %v", pt.focusTarget, FocusTargetFooter)
	}
}

// TestTabTogglesFocusOnPlanningTab verifies Tab toggles footer<->message when
// there is no active completion suggestion.
func TestTabTogglesFocusOnPlanningTab(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	m.input.SetFocus(true) // start footer-focused (Model A default)
	m.tabFocusStates[activePlanningTab(t, m).ID()] = FocusTargetFooter

	tabMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})

	// Footer -> message
	updated, _ := m.Update(tabMsg)
	m = updated.(model)
	assertFocusInSync(t, m, false /* footer focused? no, message now */)

	// Message -> footer
	updated, _ = m.Update(tabMsg)
	m = updated.(model)
	assertFocusInSync(t, m, true /* back to footer */)
}

// TestShiftTabTogglesFocusOnPlanningTab verifies shift+tab always toggles,
// regardless of completion state.
func TestShiftTabTogglesFocusOnPlanningTab(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	m.input.SetFocus(true)
	m.tabFocusStates[activePlanningTab(t, m).ID()] = FocusTargetFooter

	shiftTab := tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})

	updated, _ := m.Update(shiftTab)
	m = updated.(model)
	assertFocusInSync(t, m, false)

	updated, _ = m.Update(shiftTab)
	m = updated.(model)
	assertFocusInSync(t, m, true)
}

// TestEscReturnsFocusToFooter verifies Model A: Esc returns focus to the footer
// command line from the message input.
func TestEscReturnsFocusToFooter(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	// Put focus on the message input first.
	m.input.SetFocus(false)
	pt := activePlanningTab(t, m)
	m.tabFocusStates[pt.ID()] = FocusTargetMessage
	pt.focusTarget = FocusTargetMessage

	escMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	updated, _ := m.Update(escMsg)
	m = updated.(model)

	assertFocusInSync(t, m, true /* footer focused after Esc */)
}

// TestFooterFocusedExecutesCommandFromPlanningTab verifies the footer command
// line remains usable from a planning tab (Model A: always available).
func TestFooterFocusedExecutesCommandFromPlanningTab(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	m.input.SetFocus(true)
	m.input.SetValue("status")

	enterMsg := tea.KeyPressMsg(tea.Key{Code: 13})
	updated, _ := m.Update(enterMsg)
	m = updated.(model)

	if m.input.Value() != "" {
		t.Errorf("expected command to execute (input cleared) from planning tab with footer focused, got %q", m.input.Value())
	}
}
