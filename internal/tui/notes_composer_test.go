package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNotesComposer_FocusBlurFocused(t *testing.T) {
	nc := NewNotesComposer()

	if nc.Focused() {
		t.Error("expected NotesComposer to start unfocused")
	}

	nc.Focus()
	if !nc.Focused() {
		t.Error("expected Focused() to be true after Focus()")
	}

	nc.Blur()
	if nc.Focused() {
		t.Error("expected Focused() to be false after Blur()")
	}
}

func TestNotesComposer_SetValueValue(t *testing.T) {
	nc := NewNotesComposer()

	if nc.Value() != "" {
		t.Errorf("expected empty initial value, got %q", nc.Value())
	}

	// Multi-line value must round-trip through SetValue/Value — the whole
	// point of the composer over the single-line NotesInput.
	multiline := "First, tighten the error handling.\nSecond, add a test."
	nc.SetValue(multiline)
	if got := nc.Value(); got != multiline {
		t.Errorf("expected SetValue to round-trip multi-line content, got %q", got)
	}

	// SetValue again to confirm it overwrites rather than appends.
	nc.SetValue("second value")
	if got := nc.Value(); got != "second value" {
		t.Errorf("expected second SetValue to overwrite, got %q", got)
	}
}

func TestNotesComposer_View(t *testing.T) {
	nc := NewNotesComposer()
	nc.Focus()
	nc.SetSize(40, 5)
	nc.SetValue("some\nnotes")

	view := nc.View()
	if strings.TrimSpace(view) == "" {
		t.Error("expected non-empty View() output")
	}
}

func TestNotesComposer_Update_MultilineNewline(t *testing.T) {
	nc := NewNotesComposer()
	nc.Focus()

	// Typing a character then Enter (textarea's InsertNewline binding) then
	// another character must produce a genuine two-line value, proving the
	// composer is multi-line (Enter inserts a newline rather than
	// submitting).
	updated, _ := nc.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if updated != nc {
		t.Error("expected Update to return the same *NotesComposer receiver")
	}
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})

	if got := updated.Value(); got != "a\nb" {
		t.Errorf("expected Enter to insert a newline (multi-line), got %q", got)
	}
}
