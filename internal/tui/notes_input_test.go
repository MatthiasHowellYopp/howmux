package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNotesInput_FocusBlurFocused(t *testing.T) {
	ni := NewNotesInput()

	if ni.Focused() {
		t.Error("expected NotesInput to start unfocused")
	}

	ni.Focus()
	if !ni.Focused() {
		t.Error("expected Focused() to be true after Focus()")
	}

	ni.Blur()
	if ni.Focused() {
		t.Error("expected Focused() to be false after Blur()")
	}
}

func TestNotesInput_SetValueValue(t *testing.T) {
	ni := NewNotesInput()

	if ni.Value() != "" {
		t.Errorf("expected empty initial value, got %q", ni.Value())
	}

	ni.SetValue("looks good, just fix the typo on line 12")
	if got := ni.Value(); got != "looks good, just fix the typo on line 12" {
		t.Errorf("expected SetValue to round-trip through Value(), got %q", got)
	}

	// SetValue again to confirm it overwrites rather than appends.
	ni.SetValue("second value")
	if got := ni.Value(); got != "second value" {
		t.Errorf("expected second SetValue to overwrite, got %q", got)
	}
}

func TestNotesInput_View(t *testing.T) {
	ni := NewNotesInput()
	ni.Focus()
	ni.SetValue("some notes")

	view := ni.View()
	if view == "" {
		t.Error("expected non-empty View() output")
	}
}

func TestNotesInput_Update(t *testing.T) {
	ni := NewNotesInput()
	ni.Focus()

	// Typing a character through Update should be reflected in Value(),
	// exercising the delegate-to-textinput.Model.Update path.
	updated, _ := ni.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if updated != ni {
		t.Error("expected Update to return the same *NotesInput receiver")
	}
	if got := updated.Value(); got != "x" {
		t.Errorf("expected typed character to appear in Value(), got %q", got)
	}
}
