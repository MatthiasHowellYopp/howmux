package review

import (
	"fmt"
	"os/exec"
	"strings"
)

// bodyScriptPathFunc resolves the path to set-review-body.sh. It is a
// package-level var so tests can override it without touching the real
// filesystem, mirroring scriptPathFunc's role for
// set-review-decision.sh — kept as a distinct var (rather than reusing
// scriptPathFunc) so BodyWriter and DecisionWriter tests can fake their
// respective script paths independently without interfering with each
// other.
var bodyScriptPathFunc = defaultBodyScriptPath

func defaultBodyScriptPath() string {
	return ".howmux/scripts/set-review-body.sh"
}

// bodyExecCommandFunc is the subprocess-execution seam BodyWriter invokes
// through, mirroring execCommandFunc's role for DecisionWriter. It is a
// distinct package-level var (not shared with execCommandFunc) so
// BodyWriter and DecisionWriter tests can fake their own invocations
// independently.
var bodyExecCommandFunc = exec.Command

// BodyWriter sets the markdown body of a PR-review spool file by shelling
// out to set-review-body.sh. It deliberately never touches the spool
// file's bytes directly — mirrors DecisionWriter's contract exactly: this
// type only invokes the script and inspects the exit code / stderr.
type BodyWriter struct{}

// NewBodyWriter creates a new BodyWriter.
func NewBodyWriter() *BodyWriter {
	return &BodyWriter{}
}

// SetBody replaces the markdown body of the spool file at spoolPath with
// the contents of the file at bodyFilePath, preserving the front-matter
// block byte-for-byte (enforced by set-review-body.sh, not here — see that
// script's doc comment).
//
// Validation happens in Go before any subprocess is invoked, mirroring
// DecisionWriter.SetDecision's three-tier error messages:
//   - spoolPath == "" -> "no spool path provided"
//   - bodyFilePath == "" -> "no body file path provided"
//   - spoolPath resolves to done/ (already finalized) -> "already finalized"
//   - spoolPath resolves to nothing -> "no spool file"
//   - the script itself is missing -> an explicit path error, rather than
//     bash's own "no such file" landing on bash's stderr and surfacing as
//     a cryptic blank message.
//
// bodyFilePath's existence/non-emptiness is also re-validated by
// set-review-body.sh independently (the actual mutation boundary must
// never trust any caller) — the Go-side check here is a fast-fail before
// shelling out, not a substitute for the script's own check.
func (bw *BodyWriter) SetBody(spoolPath, bodyFilePath string) error {
	if spoolPath == "" {
		return fmt.Errorf("no spool path provided")
	}
	if bodyFilePath == "" {
		return fmt.Errorf("no body file path provided")
	}

	resolved, found, inDoneDir := resolveSpoolPath(spoolPath)
	if !found {
		return fmt.Errorf("no spool file for this PR (nothing to update): %s", spoolPath)
	}
	if inDoneDir {
		return fmt.Errorf("review already finalized (archived to done/); its body can no longer be changed: %s", resolved)
	}

	scriptPath := bodyScriptPathFunc()
	if _, err := statFunc(scriptPath); err != nil {
		return fmt.Errorf("body script not found at %s (run howmux from your project root, where .howmux/ lives): %w", scriptPath, err)
	}

	cmd := bodyExecCommandFunc("bash", scriptPath, resolved, bodyFilePath)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("set-review-body failed: %s", msg)
	}

	return nil
}
