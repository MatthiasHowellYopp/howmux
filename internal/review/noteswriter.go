package review

import (
	"fmt"
	"strings"
)

// notesScriptPathFunc resolves the path to set-review-notes.sh. It is a
// package-level var so tests can override it without touching the real
// filesystem, mirroring scriptPathFunc's role for DecisionWriter
// (decisionwriter.go) — kept as a distinct var (rather than reusing
// scriptPathFunc) so NotesWriter and DecisionWriter tests can each stub
// their own script path independently without interfering with each other.
var notesScriptPathFunc = defaultNotesScriptPath

func defaultNotesScriptPath() string {
	return ".howmux/scripts/set-review-notes.sh"
}

// NotesWriter sets the decision_notes: field on a PR-review spool file by
// shelling out to set-review-notes.sh, mirroring DecisionWriter's contract
// (internal/review/decisionwriter.go) exactly: it deliberately never
// touches the spool file's bytes directly — parsing/rewriting markdown
// front-matter in Go is out of scope; this type only invokes the script
// and inspects the exit code / stderr. It reuses DecisionWriter's
// execCommandFunc and statFunc seams (defined in decisionwriter.go) rather
// than declaring its own, since both writers shell out via "bash
// <script> <spoolPath> <value>" and tests substituting a fake exec/stat
// apply identically to either writer.
type NotesWriter struct{}

// NewNotesWriter creates a new NotesWriter.
func NewNotesWriter() *NotesWriter {
	return &NotesWriter{}
}

// SetNotes sets the decision_notes: field in the spool file at spoolPath to
// notes.
//
// Validation happens in Go before any subprocess is invoked: an empty
// spoolPath returns an error without shelling out. Free-text validation
// (e.g. rejecting embedded newlines) is left to set-review-notes.sh itself,
// which re-validates independently as the outermost defense — mirroring
// set-review-decision.sh's "the actual mutation boundary must never trust
// any caller" discipline.
//
// spoolPath is the pending/ path from Record.SpoolPath. It is resolved
// through resolveSpoolPath first (the same pending->done fallback
// DecisionWriter.SetDecision uses), so three distinct states get three
// distinct, non-cryptic errors:
//   - the file has already been drained to done/ -> "already finalized"
//     (setting notes on a drained review is meaningless);
//   - no spool file exists in either location -> "no spool file";
//   - the notes script itself is missing -> an explicit path error, rather
//     than bash's "no such file" landing on bash's own stderr and surfacing
//     to the user as an empty message.
func (nw *NotesWriter) SetNotes(spoolPath, notes string) error {
	if spoolPath == "" {
		return fmt.Errorf("no spool path provided")
	}

	resolved, found, inDoneDir := resolveSpoolPath(spoolPath)
	if !found {
		return fmt.Errorf("no spool file for this PR (nothing to add notes to): %s", spoolPath)
	}
	if inDoneDir {
		return fmt.Errorf("review already finalized (archived to done/); its notes can no longer be changed: %s", resolved)
	}

	scriptPath := notesScriptPathFunc()
	// Stat the script up front: if it is missing (e.g. howmux launched from
	// a directory without a .howmux/ tree), bash's own "no such file" error
	// goes to bash's stderr, not the script's, so cmd.Run() would otherwise
	// fail with an empty captured stderr and a cryptic blank message.
	if _, err := statFunc(scriptPath); err != nil {
		return fmt.Errorf("notes script not found at %s (run howmux from your project root, where .howmux/ lives): %w", scriptPath, err)
	}

	cmd := execCommandFunc("bash", scriptPath, resolved, notes)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("set-review-notes failed: %s", msg)
	}

	return nil
}
