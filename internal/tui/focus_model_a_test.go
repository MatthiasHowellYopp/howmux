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

// TestPlanCommandAutoFocusesMessageInput verifies the UX refinement: the `plan`
// command auto-switches focus to the message input on the newly created tab so
// the user can keep typing their idea, while every other planning tab keeps the
// Model-A footer-focused default. Tab/Esc still toggle back to the footer.
func TestPlanCommandAutoFocusesMessageInput(t *testing.T) {
	m := createTestModelWithTab(t, TabTypeMain)
	// Footer starts focused (console default).
	m.input.SetFocus(true)

	updated, _ := m.handlePlan("build a thing")
	m = updated

	active := m.tabManager.GetActiveTab()
	if active == nil || active.Type() != TabTypePlanning {
		t.Fatalf("plan did not create/activate a planning tab (got %v)", active)
	}
	// After `plan`, focus must be on the message input, in sync across stores.
	assertFocusInSync(t, m, false /* footer focused? no — message is */)
}

// TestViewportNavKeepsFocusInSync is a regression test for the bug where
// pgup/pgdown/home/end in a planning tab blurred the footer directly, leaving
// the three focus stores out of sync so the next Tab misfired and the command
// line was unavailable. Navigation must route focus through setPlanningFocus.
func TestViewportNavKeepsFocusInSync(t *testing.T) {
	for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd} {
		m := createTestModelWithTab(t, TabTypePlanning)
		// Start message-focused (the drift-prone starting state).
		if cmd := m.setPlanningFocus(FocusTargetMessage); cmd != nil {
			_ = cmd
		}
		assertFocusInSync(t, m, false /* message focused */)

		// Navigate the viewport.
		navMsg := tea.KeyPressMsg(tea.Key{Code: key})
		updated, _ := m.Update(navMsg)
		m = updated.(model)

		// After navigation, all three stores must agree on footer focus.
		assertFocusInSync(t, m, true /* footer focused */)

		// And Tab must still toggle cleanly footer -> message.
		tabMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
		updated, _ = m.Update(tabMsg)
		m = updated.(model)
		assertFocusInSync(t, m, false /* message focused again */)
	}
}
