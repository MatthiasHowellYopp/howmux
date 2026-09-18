package review

import (
	"fmt"
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
func (dw *DecisionWriter) SetDecision(spoolPath, decision string) error {
	if !validDecisions[decision] {
		return fmt.Errorf("invalid decision %q: must be one of post, revise, rereview, discard", decision)
	}
	if spoolPath == "" {
		return fmt.Errorf("no spool path provided")
	}

	scriptPath := scriptPathFunc()
	cmd := execCommandFunc("bash", scriptPath, spoolPath, decision)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("set-review-decision failed: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}
