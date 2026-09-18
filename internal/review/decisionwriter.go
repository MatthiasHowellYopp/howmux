package review

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// validDecisions is the closed vocabulary a spool file's decision: field may
// be set to. Enforced in Go first so a typo never reaches a subprocess call
// — see set-review-decision.sh, which re-validates independently as the
// outermost defense (the actual mutation boundary must never trust any
// caller).
var validDecisions = map[string]bool{
	"post":     true,
	"revise":   true,
	"rereview": true,
	"discard":  true,
}

// scriptPathFunc resolves the path to set-review-decision.sh. It is a
// package-level var so tests can override it without touching the real
// filesystem. defaultScriptPath resolves the path relative to the current
// working directory, consistent with how config.Load() locates
// ".howmux/config.yaml" relative to CWD (see internal/config/config.go) —
// this package has no existing repo-relative-path convention of its own to
// follow instead.
var scriptPathFunc = defaultScriptPath

func defaultScriptPath() string {
	return ".howmux/scripts/set-review-decision.sh"
}

// execCommandFunc is the subprocess-execution seam DecisionWriter invokes
// through. It is package-level so tests can substitute a fake and assert the
// invocation (argv, call count) without running a real script.
var execCommandFunc = exec.Command

// statFunc is the file-existence seam used to pre-check the decision script
// (and is package-level so tests can stub it). resolveSpoolPath already has
// its own os.Stat calls for the spool file itself.
var statFunc = os.Stat

// DecisionWriter sets the decision: field on a PR-review spool file by
// shelling out to set-review-decision.sh. It deliberately never touches the
// spool file's bytes directly — parsing/rewriting markdown front-matter in Go
// is out of scope per the issue's constraint; this type only invokes the
// script and inspects the exit code / stderr.
type DecisionWriter struct{}

// NewDecisionWriter creates a new DecisionWriter.
func NewDecisionWriter() *DecisionWriter {
	return &DecisionWriter{}
}

// SetDecision sets the decision: field in the spool file at spoolPath to
// decision, which must be one of "post", "revise", "rereview", "discard".
//
// Validation happens in Go before any subprocess is invoked: an invalid
// decision or an empty spoolPath returns an error without shelling out.
//
// spoolPath is the pending/ path from Record.SpoolPath. It is resolved
// through resolveSpoolPath first (the same pending→done fallback used by
// #91/#93), so three distinct states get three distinct, non-cryptic errors:
//   - the file has already been drained to done/ → "already finalized"
//     (setting a decision on a drained review is meaningless);
//   - no spool file exists in either location → "no spool file";
//   - the decision script itself is missing → an explicit path error, rather
//     than bash's "no such file" landing on bash's own stderr and surfacing
//     to the user as an empty message.
func (dw *DecisionWriter) SetDecision(spoolPath, decision string) error {
	if !validDecisions[decision] {
		return fmt.Errorf("invalid decision %q: must be one of post, revise, rereview, discard", decision)
	}
	if spoolPath == "" {
		return fmt.Errorf("no spool path provided")
	}

	resolved, found, inDoneDir := resolveSpoolPath(spoolPath)
	if !found {
		return fmt.Errorf("no spool file for this PR (nothing to decide on): %s", spoolPath)
	}
	if inDoneDir {
		return fmt.Errorf("review already finalized (archived to done/); its decision can no longer be changed: %s", resolved)
	}

	scriptPath := scriptPathFunc()
	// Stat the script up front: if it is missing (e.g. howmux launched from a
	// directory without a .howmux/ tree), bash's own "no such file" error
	// goes to bash's stderr, not the script's, so cmd.Run() would otherwise
	// fail with an empty captured stderr and a cryptic blank message.
	if _, err := statFunc(scriptPath); err != nil {
		return fmt.Errorf("decision script not found at %s (run howmux from your project root, where .howmux/ lives): %w", scriptPath, err)
	}

	cmd := execCommandFunc("bash", scriptPath, resolved, decision)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("set-review-decision failed: %s", msg)
	}

	return nil
}
