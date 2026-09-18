package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// addReviewsTabWithTwoRecords adds a *ReviewsTab backed by a
// fakeReviewStore containing two records to m's tabManager, mirroring
// addReviewsTabWithRecord (commands_decide_test.go) but with a second row
// so moveCursor (driven by up/down on an unfocused footer) has somewhere to
// move to — a single-record store always clamps back to the same row,
// which would make TestAutocompleteArrowRouting_ReviewsTab_*'s row-cursor
// assertions vacuous.
func addReviewsTabWithTwoRecords(m model) *ReviewsTab {
	store := &fakeReviewStore{records: []review.Record{
		{Repo: "owner/repo-a", PR: 1},
		{Repo: "owner/repo-b", PR: 2},
	}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed rt.lastOrder + selectedKey to the first row
	m.tabManager.AddTab(rt)
	return rt
}

// TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_SuggestionsVisible_ArrowsMoveSuggestion
// is the core regression test for issue #100: with the Reviews tab active,
// the footer focused, and matched suggestions showing, up/down must move
// the dropdown's CurrentSuggestionIndex() rather than being swallowed by
// ReviewsTab's row-cursor handling. Before the fix, the hoisted guard in
// model.Update only existed inside the TabTypeMain branch, so this exact
// sequence fell through to m.tabManager.Update and moved the row cursor
// instead.
func TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_SuggestionsVisible_ArrowsMoveSuggestion(t *testing.T) {
	m := newDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 1})
	m.tabManager.SetActiveTab(m.findReviewsTabIndex())

	m.input.SetFocus(true)
	m.input.SetValue("dec")
	if !m.input.HasMatchedSuggestions() {
		t.Fatalf("test setup broken: expected matched suggestions for %q", m.input.Value())
	}

	beforeIndex := m.input.textinput.CurrentSuggestionIndex()
	beforeScroll := m.consoleViewport.YOffset()

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if got := resultModel.input.textinput.CurrentSuggestionIndex(); got == beforeIndex {
		t.Errorf("expected CurrentSuggestionIndex() to change on down, stayed at %d", got)
	}
	if got := resultModel.consoleViewport.YOffset(); got != beforeScroll {
		t.Errorf("expected console viewport scroll unchanged, got %d (want %d)", got, beforeScroll)
	}

	// Up moves the selection backward (PrevSuggestion semantics).
	afterDownIndex := resultModel.input.textinput.CurrentSuggestionIndex()
	result2, _ := resultModel.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	resultModel2, ok := result2.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if got := resultModel2.input.textinput.CurrentSuggestionIndex(); got == afterDownIndex {
		t.Errorf("expected CurrentSuggestionIndex() to change on up, stayed at %d", got)
	}
}

// TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_NoSuggestions_ArrowsRetainExistingBehavior
// locks in AC4: when the footer is focused on the Reviews tab but there are
// no matched suggestions, the hoisted guard's HasMatchedSuggestions() check
// must be false, so the key falls through to the pre-existing behavior
// (forwarded to m.tabManager.Update, which moves ReviewsTab's row cursor).
// This is a pre-existing quirk, not new desired behavior — the test exists
// to prove the fix does not change this case.
func TestAutocompleteArrowRouting_ReviewsTab_FooterFocused_NoSuggestions_ArrowsRetainExistingBehavior(t *testing.T) {
	m := newDecideRoutingTestModel()
	rt := addReviewsTabWithTwoRecords(m)
	m.tabManager.SetActiveTab(m.findReviewsTabIndex())

	m.input.SetFocus(true)
	m.input.SetValue("zzzznomatch")
	if m.input.HasMatchedSuggestions() {
		t.Fatalf("test setup broken: expected no matched suggestions for %q", m.input.Value())
	}

	beforeValue := m.input.Value()
	beforeKey := rt.SelectedKey()

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if cmd != nil {
		_ = cmd()
	}

	if got := resultModel.input.Value(); got != beforeValue {
		t.Errorf("expected input value unchanged, got %q (want %q)", got, beforeValue)
	}
	if got := rt.SelectedKey(); got == beforeKey {
		t.Errorf("expected row cursor to move (pre-existing fallback behavior), stayed at %q", got)
	}
}

// TestAutocompleteArrowRouting_ReviewsTab_FooterUnfocused_ArrowsMoveRowCursor
// is the row-navigation-mode regression guard from AC6: with the footer
// unfocused, up/down must reach ReviewsTab.Update's row-cursor handling
// regardless of the hoisted guard, since the guard's m.input.Focused()
// condition is false.
func TestAutocompleteArrowRouting_ReviewsTab_FooterUnfocused_ArrowsMoveRowCursor(t *testing.T) {
	m := newDecideRoutingTestModel()
	rt := addReviewsTabWithTwoRecords(m)
	m.tabManager.SetActiveTab(m.findReviewsTabIndex())

	m.input.SetFocus(false)
	if m.input.Focused() {
		t.Fatalf("test setup broken: input should not be focused")
	}

	beforeKey := rt.SelectedKey()
	beforeSuggestionIndex := m.input.textinput.CurrentSuggestionIndex()

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if got := rt.SelectedKey(); got == beforeKey {
		t.Errorf("expected the selected row to change, stayed at %q", got)
	}
	if got := resultModel.input.textinput.CurrentSuggestionIndex(); got != beforeSuggestionIndex {
		t.Errorf("expected suggestion index unaffected since the footer never received the key, got %d (want %d)", got, beforeSuggestionIndex)
	}
}

// TestAutocompleteArrowRouting_MainTab_SuggestionsVisible_ArrowsMoveSuggestion
// covers AC3: on the Main tab, with the footer focused and matched
// suggestions showing, up/down must still move the dropdown selection
// rather than scrolling the console — behavior that must survive the
// removal of the TabTypeMain branch's now-redundant inner suggestion check.
func TestAutocompleteArrowRouting_MainTab_SuggestionsVisible_ArrowsMoveSuggestion(t *testing.T) {
	m := newDecideRoutingTestModel()
	m.tabManager.AddTab(NewMainTab())
	m.tabManager.SetActiveTab(0)

	m.input.SetFocus(true)
	m.input.SetValue("dec")
	if !m.input.HasMatchedSuggestions() {
		t.Fatalf("test setup broken: expected matched suggestions for %q", m.input.Value())
	}

	beforeIndex := m.input.textinput.CurrentSuggestionIndex()
	beforeScroll := m.consoleViewport.YOffset()

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if got := resultModel.input.textinput.CurrentSuggestionIndex(); got == beforeIndex {
		t.Errorf("expected CurrentSuggestionIndex() to change on down, stayed at %d", got)
	}
	if got := resultModel.consoleViewport.YOffset(); got != beforeScroll {
		t.Errorf("expected console viewport scroll unchanged when suggestions are visible, got %d (want %d)", got, beforeScroll)
	}
}

// TestAutocompleteArrowRouting_MainTab_NoSuggestions_ArrowsScrollConsole
// covers AC3's second half: on the Main tab, with no matched suggestions,
// up/down must fall through to the console-scroll switch as before.
func TestAutocompleteArrowRouting_MainTab_NoSuggestions_ArrowsScrollConsole(t *testing.T) {
	m := newDecideRoutingTestModel()
	m.tabManager.AddTab(NewMainTab())
	m.tabManager.SetActiveTab(0)

	// Seed enough content so ScrollDown has somewhere to move, then rewind
	// to the top so the assertion below observes a real change.
	lines := ""
	for i := 0; i < 200; i++ {
		lines += "activity line\n"
	}
	m.consoleViewport.SetContent(lines)
	m.consoleViewport.GotoTop()

	m.input.SetFocus(true)
	m.input.SetValue("")
	if m.input.HasMatchedSuggestions() {
		t.Fatalf("test setup broken: expected no matched suggestions for empty input")
	}

	beforeScroll := m.consoleViewport.YOffset()

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if got := resultModel.consoleViewport.YOffset(); got == beforeScroll {
		t.Errorf("expected console viewport to scroll on down when no suggestions are showing, stayed at %d", got)
	}
}
