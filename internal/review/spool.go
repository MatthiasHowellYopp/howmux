// Package review (this file) provides a read-only reader for the external
// PR-review "spool" file format defined and owned by
// ai-resources/workflows/spool.py. Howmux does not own this format — it only
// reads whatever pr_review.py / pr_review_finalize.py have written, mirroring
// the same flat, non-nested "key: value" front-matter contract described
// there. This file never writes, renames, or removes anything under
// ~/PR-Review/**; it only ever reads.
package review

import (
	"os"
	"strings"
)

// SpoolInfo is the read-only, display-oriented view of a spool file's
// state, derived by reading front-matter from disk. It is never persisted —
// it is recomputed on every ReadSpoolInfo call, mirroring the "no cache"
// design of Store.List().
type SpoolInfo struct {
	Found         bool   // true if the spool file was located (in pending/ or done/)
	InDoneDir     bool   // true if the file was found in done/ rather than pending/
	Verdict       string // raw "verdict" front-matter value, "" if absent/not found
	Decision      string // raw "decision" front-matter value, "" if blank/absent
	DecisionState string // classified state string, see ClassifySpoolState; always populated even when !Found
}

// ParseSpoolFrontMatter parses the flat `key: value` front-matter block at
// the start of a spool file (bounded by "---" lines, per the contract in
// ai-resources/workflows/spool.py). It does not validate keys against an
// allow-list — unknown keys are simply available in the returned map,
// mirroring spool.py's permissiveness. Returns an empty map (not an error)
// if no front-matter block is present, since a malformed/legacy file should
// degrade to "no known fields" rather than blocking the whole tab render.
//
// Parsing rules:
//  1. The file must start with a line that is exactly "---" (after trimming
//     surrounding whitespace). If not, return an empty map.
//  2. Everything up to the next line that is exactly "---" is the
//     front-matter block. If no closing fence is found, return an empty map.
//  3. Each non-blank line in that block is split on the first ":" into key
//     and value; both are trimmed of surrounding whitespace. Lines with no
//     ":" are skipped.
//  4. A key with an empty value after trimming (e.g. "decision:") maps to ""
//     in the returned map.
func ParseSpoolFrontMatter(data []byte) map[string]string {
	fields := make(map[string]string)

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return fields
	}

	if strings.TrimSpace(lines[0]) != "---" {
		return fields
	}

	closed := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	if !closed {
		return make(map[string]string)
	}

	return fields
}

// ReadSpoolInfo resolves and reads a spool file for display purposes only.
// spoolPath is the value from Record.SpoolPath (may be "" if the PR has
// never been reviewed, or may point at a pending/ path that has since been
// moved to done/ by the external finalize pipeline).
//
// homeDir is passed in (rather than calling os.UserHomeDir() internally) so
// this function stays a pure-ish, easily-testable unit: callers resolve the
// home directory once via the existing userHomeDirFunc seam in
// internal/tui/commands.go and pass it down. homeDir is currently unused by
// the resolution logic below (spoolPath is always already absolute per
// validateSpoolPath's contract in runner.go) but is kept as a parameter to
// match the documented signature and leave room for future relative-path
// resolution without an API change.
//
// Resolution order:
//  1. If spoolPath == "", return SpoolInfo{Found: false, DecisionState: "no spool"} immediately, with zero filesystem calls.
//  2. If the file at spoolPath exists, read and parse it from there (pending/ case).
//  3. Otherwise, derive the done/ counterpart by swapping the "pending" path
//     segment for "done" (same base filename) and try that path.
//  4. If neither location exists, return SpoolInfo{Found: false, DecisionState: "no spool"}.
//  5. On a read/parse success, populate Verdict/Decision from the parsed
//     front-matter and call ClassifySpoolState to fill DecisionState.
func ReadSpoolInfo(spoolPath string, homeDir string) SpoolInfo {
	_ = homeDir // reserved for future relative-path resolution; spoolPath is always absolute today

	if spoolPath == "" {
		return SpoolInfo{Found: false, DecisionState: ClassifySpoolState(false, false, "")}
	}

	if data, err := os.ReadFile(spoolPath); err == nil {
		return buildSpoolInfo(data, false)
	}

	donePath := derivePendingToDone(spoolPath)
	if donePath != "" {
		if data, err := os.ReadFile(donePath); err == nil {
			return buildSpoolInfo(data, true)
		}
	}

	return SpoolInfo{Found: false, DecisionState: ClassifySpoolState(false, false, "")}
}

// buildSpoolInfo parses the given spool file contents and assembles a
// SpoolInfo for a file that was successfully located, either in pending/
// (inDoneDir=false) or done/ (inDoneDir=true).
func buildSpoolInfo(data []byte, inDoneDir bool) SpoolInfo {
	fields := ParseSpoolFrontMatter(data)
	decision := fields["decision"]

	return SpoolInfo{
		Found:         true,
		InDoneDir:     inDoneDir,
		Verdict:       fields["verdict"],
		Decision:      decision,
		DecisionState: ClassifySpoolState(true, inDoneDir, decision),
	}
}

// derivePendingToDone swaps the last "pending" path segment for "done" in
// spoolPath, returning "" if no "pending" segment is present to swap. This
// mirrors the external finalize pipeline's move from ~/PR-Review/pending/
// to ~/PR-Review/done/ (same base filename, sibling directory).
func derivePendingToDone(spoolPath string) string {
	const from = "pending"
	const to = "done"

	segments := strings.Split(spoolPath, string(os.PathSeparator))
	swapped := false
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] == from {
			segments[i] = to
			swapped = true
			break
		}
	}

	if !swapped {
		return ""
	}

	return strings.Join(segments, string(os.PathSeparator))
}

// ClassifySpoolState implements the AC3 decision-state mapping as a pure
// function so it is unit-testable independent of any filesystem access.
//
// Precedence order: found, then inDoneDir, then decision. The decision is
// matched case-insensitively (real archived files carry e.g. "POST").
//   - found=false                                    -> "no spool"
//   - found=true, inDoneDir=false, decision==""       -> "pending"
//   - found=true, inDoneDir=false, decision!=""       -> "decided: <decision>" (verbatim)
//   - found=true, inDoneDir=true: the file was drained to done/ — surface WHY,
//     not a blanket "posted". The finalize pipeline archives discard/revise/
//     rereview to done/ as well as post, so collapsing all of them to
//     "posted" would mislabel a discarded review as posted (see #82 review):
//   - decision post     -> "posted"
//   - decision discard  -> "discarded"
//   - other non-empty   -> "done: <decision>" (e.g. revise/rereview/unknown)
//   - empty             -> "done"
func ClassifySpoolState(found bool, inDoneDir bool, decision string) string {
	if !found {
		return "no spool"
	}

	if inDoneDir {
		switch strings.ToLower(strings.TrimSpace(decision)) {
		case "post":
			return "posted"
		case "discard":
			return "discarded"
		case "":
			return "done"
		default:
			return "done: " + decision
		}
	}

	if decision == "" {
		return "pending"
	}

	return "decided: " + decision
}
