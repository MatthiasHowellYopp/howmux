package tui

import (
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// insertAtCursor inserts insert into current at the given rune index cursorPos
// and returns the new string plus the new rune cursor position (just past the
// inserted text). It operates on runes, not bytes: bubbles textinput stores its
// value as []rune and reports the cursor as a rune index, so a byte-index splice
// would corrupt multi-byte (non-ASCII) characters.
//
// cursorPos is clamped to [0, runeLen(current)] so an out-of-range index cannot
// panic.
func insertAtCursor(current, insert string, cursorPos int) (string, int) {
	r := []rune(current)
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(r) {
		cursorPos = len(r)
	}
	newValue := string(r[:cursorPos]) + insert + string(r[cursorPos:])
	newCursor := cursorPos + utf8.RuneCountInString(insert)
	return newValue, newCursor
}

// CopyToClipboard safely copies text to the system clipboard with graceful error handling.
// It handles permission failures and headless environments without panicking.
// Errors are logged at debug level and not surfaced to users.
//
// Returns nil for empty text (no-op). Returns error on clipboard write failure,
// but the error is logged internally and should not be propagated to UI.
func CopyToClipboard(text string) error {
	if text == "" {
		return nil // No-op for empty text
	}

	if err := clipboard.WriteAll(text); err != nil {
		logging.Debug("clipboard copy failed", "error", err)
		return err // Return but don't propagate to user
	}
	return nil
}

// PasteFromClipboard safely reads text from the system clipboard with graceful error handling.
// It handles permission failures and headless environments without panicking.
// Errors are logged at debug level and not surfaced to users.
//
// Returns empty string on failure. The error is logged internally and should not
// be propagated to UI.
func PasteFromClipboard() (string, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		logging.Debug("clipboard paste failed", "error", err)
		return "", err // Return empty string on failure
	}
	return text, nil
}
