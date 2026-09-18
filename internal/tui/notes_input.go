package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// NotesInput wraps textinput.Model for editing a PR review's decision_notes
// front-matter field. It mirrors AutocompleteInput's Focus/Blur/Update/View
// skeleton (autocomplete.go) but has no suggestions logic — decision_notes
// is free text, not a command to match against a registry.
type NotesInput struct {
	textinput textinput.Model
}

// NewNotesInput creates a new NotesInput component.
func NewNotesInput() *NotesInput {
	ti := textinput.New()
	ti.Prompt = "notes> "

	// Configure solid cursor (non-blinking), matching AutocompleteInput's
	// existing convention (autocomplete.go's NewAutocompleteInput).
	currentStyles := ti.Styles()
	currentStyles.Cursor.Blink = false
	ti.SetStyles(currentStyles)

	return &NotesInput{
		textinput: ti,
	}
}

// Focused returns whether the input currently has focus.
func (n *NotesInput) Focused() bool {
	return n.textinput.Focused()
}

// Focus gives focus to the underlying textinput.
func (n *NotesInput) Focus() tea.Cmd {
	return n.textinput.Focus()
}

// Blur removes focus from the underlying textinput.
func (n *NotesInput) Blur() {
	n.textinput.Blur()
}

// Value returns the current input value.
func (n *NotesInput) Value() string {
	return n.textinput.Value()
}

// SetValue sets the input value.
func (n *NotesInput) SetValue(value string) {
	n.textinput.SetValue(value)
}

// Update handles messages for the notes input, delegating to the underlying
// textinput.Model.Update for anything not specially handled — mirroring
// AutocompleteInput.Update's idiom, minus the autocomplete-specific
// suggestion/template handling (not applicable to free-text notes).
func (n *NotesInput) Update(msg tea.Msg) (*NotesInput, tea.Cmd) {
	var cmd tea.Cmd
	n.textinput, cmd = n.textinput.Update(msg)
	return n, cmd
}

// View renders the notes input using the underlying textinput's rendering.
func (n *NotesInput) View() string {
	return n.textinput.View()
}
