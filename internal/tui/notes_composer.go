package tui

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// notesComposerDefaultWidth and notesComposerDefaultHeight are the initial
// dimensions the textarea is built with before the first SetSize() call
// (which the model issues on open and on resize). They are deliberately
// modest so the composer is usable even in the brief window before the
// model has sized it against the real terminal width.
const (
	notesComposerDefaultWidth  = 60
	notesComposerDefaultHeight = 6
)

// NotesComposer wraps textarea.Model for composing the multi-line revision
// notes fed to a "revise"/"rereview" decide action in-memory (never
// persisted to the spool front-matter — that is NotesInput's job, the
// single-line manual-flow editor). It mirrors NotesInput's Focus/Blur/
// Value/Update/View skeleton (notes_input.go) but is multi-line: Enter
// inserts a newline (textarea's default InsertNewline binding), so
// submission is bound to Ctrl+D by the model's keypress interception rather
// than Enter.
type NotesComposer struct {
	textarea textarea.Model
}

// NewNotesComposer creates a new NotesComposer component with a
// non-blinking cursor, matching NotesInput/AutocompleteInput's existing
// convention, and a placeholder describing its purpose.
func NewNotesComposer() *NotesComposer {
	ta := textarea.New()
	ta.Placeholder = "Instructions for the reviser (optional)"
	ta.ShowLineNumbers = false
	ta.SetWidth(notesComposerDefaultWidth)
	ta.SetHeight(notesComposerDefaultHeight)

	// Configure a solid (non-blinking) cursor, matching NotesInput's
	// existing convention (notes_input.go's NewNotesInput).
	styles := ta.Styles()
	styles.Cursor.Blink = false
	ta.SetStyles(styles)

	return &NotesComposer{
		textarea: ta,
	}
}

// Focused returns whether the composer currently has focus.
func (n *NotesComposer) Focused() bool {
	return n.textarea.Focused()
}

// Focus gives focus to the underlying textarea.
func (n *NotesComposer) Focus() tea.Cmd {
	return n.textarea.Focus()
}

// Blur removes focus from the underlying textarea.
func (n *NotesComposer) Blur() {
	n.textarea.Blur()
}

// Value returns the current multi-line composer value.
func (n *NotesComposer) Value() string {
	return n.textarea.Value()
}

// SetValue sets the composer value, replacing any existing content.
func (n *NotesComposer) SetValue(value string) {
	n.textarea.SetValue(value)
}

// SetSize sets the composer's visible width and height. Called by the model
// when the composer is opened and on every terminal resize so the textarea
// tracks the available content area. Non-positive dimensions are ignored so
// a resize event that arrives before the layout is known cannot collapse
// the textarea to zero.
func (n *NotesComposer) SetSize(width, height int) {
	if width > 0 {
		n.textarea.SetWidth(width)
	}
	if height > 0 {
		n.textarea.SetHeight(height)
	}
}

// Update handles messages for the composer, delegating to the underlying
// textarea.Model.Update — mirroring NotesInput.Update's idiom.
func (n *NotesComposer) Update(msg tea.Msg) (*NotesComposer, tea.Cmd) {
	var cmd tea.Cmd
	n.textarea, cmd = n.textarea.Update(msg)
	return n, cmd
}

// View renders the composer using the underlying textarea's rendering.
func (n *NotesComposer) View() string {
	return n.textarea.View()
}
