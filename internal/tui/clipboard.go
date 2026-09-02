package tui

import (
	"github.com/atotto/clipboard"
	"github.com/jbrinkman/kiro-krew/internal/logging"
)

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
