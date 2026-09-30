// Package review (this file) implements the single-review decide actions
// (post/revise/rereview/discard) — the Go equivalent of
// ai-resources/workflows/pr_review_finalize.py's do_post/do_revise/
// do_rereview, scoped to exactly one review instead of the whole pending/
// spool. See .howmux/specs/issue-109-collapse-finalize-into-decide.md for
// the full design rationale.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// --- subprocess seams (Task 2) ---------------------------------------------

// kiroOneshotFunc is the subprocess-execution seam every agentic step in
// this file (humanize, revise, rereview's lens fan-out + consolidation)
// calls through. It is package-level so tests can substitute a fake and
// assert on (agentName, prompt) without ever shelling out to a real
// kiro-cli. Mirrors ai-resources/workflows/kiro_oneshot.py's kiro_oneshot()
// contract exactly: same argv shape, same ANSI+leading-"> "-stripping, same
// two error conditions (non-zero exit, empty cleaned output).
var kiroOneshotFunc = defaultKiroOneshot

// ansiRe matches CSI and OSC escape sequences, mirroring
// kiro_oneshot.py's _ANSI_RE.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// leadingAngleRe matches a single leading "> " (kiro's input-echo arrow),
// mirroring kiro_oneshot.py's _clean_stdout regex.
var leadingAngleRe = regexp.MustCompile(`^\s*>\s?`)

// stripAnsi removes ANSI escape sequences and carriage returns from text,
// the Go port of kiro_oneshot.py's _strip_ansi.
func stripAnsi(text string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(text, ""), "\r", "")
}

// cleanKiroStdout turns kiro-cli's raw one-shot stdout into the assistant's
// plain-text answer: strip ANSI, then remove a single leading "> " from the
// very start of the output only (a ">" inside the body is a real
// blockquote and must survive). Direct port of kiro_oneshot.py's
// _clean_stdout.
func cleanKiroStdout(raw string) string {
	text := stripAnsi(raw)
	text = leadingAngleRe.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// defaultKiroOneshot is the real kiroOneshotFunc implementation: it shells
// to `kiro-cli chat --no-interactive --trust-all-tools --agent <agentName>
// "<prompt>"`, captures stdout, and returns the cleaned answer. Returns an
// error if kiro-cli exits non-zero or the cleaned output is empty —
// mirroring KiroOneshotError's two trigger conditions in the Python
// reference.
func defaultKiroOneshot(ctx context.Context, agentName string, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "kiro-cli", "chat",
		"--no-interactive",
		"--trust-all-tools",
		"--agent", agentName,
		prompt,
	)
	// Detach stdin from the controlling tty, mirroring kiro_oneshot.py's
	// stdin=subprocess.DEVNULL: in --no-interactive mode kiro-cli still
	// emits terminal-control queries; without a tty on stdin to answer
	// them, no stray escape-sequence replies bleed back.
	cmd.Stdin = nil

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("kiro-cli exited %d for agent %q: %s: %w",
				exitErr.ExitCode(), agentName, strings.TrimSpace(stripAnsi(stderr.String())), err)
		}
		return "", fmt.Errorf("kiro-cli failed for agent %q: %w", agentName, err)
	}

	answer := cleanKiroStdout(stdout.String())
	if answer == "" {
		return "", fmt.Errorf("kiro-cli produced no answer for agent %q", agentName)
	}
	return answer, nil
}

// postReviewCommandFunc is the subprocess-execution seam PostReview invokes
// through to publish inline PR comments. Package-level so tests substitute
// a fake `gh` and assert on the invocation without hitting the network.
// Mirrors fetchDiffFunc's (runner.go) established seam pattern.
var postReviewCommandFunc = defaultPostReviewCommand

// defaultPostReviewCommand shells to `gh api
// repos/<repo>/pulls/<pr>/reviews --method POST --input <payloadFile>
// --verbose`, following tool-routing.md's documented rule for posting
// arrays/nested JSON bodies via --input rather than --field. Returns gh's
// stdout (the JSON response) on success, or a wrapped error on failure —
// mirroring fetchDiffFunc's exec.ExitError-unwrapping pattern.
//
// --verbose makes gh dump the full HTTP request/response (headers plus
// bodies) to stderr, and both stdout and stderr are captured into their
// own buffers rather than via cmd.Output(). The reason is a 422 from the
// reviews endpoint: GitHub returns a field-level errors[] array in the
// response body explaining exactly which comment failed to anchor (e.g.
// "pull_request_review_thread.line must be part of the diff"), and where
// that body lands depends on gh's mode — the JSON response body on stdout,
// the --verbose HTTP transcript on stderr. Capturing and wrapping BOTH
// guarantees the actionable field-level detail surfaces in the returned
// error instead of a bare "exit 1", which is what the previous
// cmd.Output()/exitErr.Stderr path discarded.
func defaultPostReviewCommand(ctx context.Context, payloadFile string, repo string, pr int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, pr),
		"--method", "POST",
		"--input", payloadFile,
		"--verbose",
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if body := strings.TrimSpace(stdout.String()); body != "" {
			// gh's JSON response body (the field-level errors[] on a 422)
			// lands on stdout; append it so the actionable detail is never
			// lost, even when --verbose's transcript on stderr is empty.
			if detail != "" {
				detail += "\n"
			}
			detail += "response body: " + body
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api pulls/reviews failed (exit %d): %s: %w",
				exitErr.ExitCode(), detail, err)
		}
		return nil, fmt.Errorf("gh api pulls/reviews failed: %s: %w", detail, err)
	}
	return []byte(stdout.String()), nil
}

// is422 reports whether err (a wrapped postReviewCommandFunc failure) came
// from a GitHub 422 Unprocessable Entity. gh surfaces the status both as
// "HTTP 422" in the --verbose transcript and, on the reviews endpoint, as a
// "Validation Failed" message in the JSON response body; matching either
// (case-insensitively) keeps the check robust to which stream carried the
// detail. Used by PostReview to decide whether a body-only retry is worth
// attempting — other failures (network, auth, 5xx) should surface as-is.
func is422(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "422") ||
		strings.Contains(msg, "unprocessable entity") ||
		strings.Contains(msg, "validation failed")
}

// isOwnPRReviewError reports whether err came from GitHub rejecting an
// APPROVE or REQUEST_CHANGES review on the poster's OWN pull request.
// GitHub returns a 422 with the message "Review cannot approve your own pull
// request" (or "... request changes on your own pull request"); the only
// review event a self-authored PR accepts is COMMENT. PostReview uses this
// to auto-downgrade the event to COMMENT and retry, rather than failing the
// whole post. Matching on "your own pull request" keeps it specific to this
// case and off other 422s (off-diff comment lines, bad paths).
func isOwnPRReviewError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "your own pull request")
}

// downgradeEventToComment rewrites a marshaled review payload's "event"
// field to "COMMENT" and prepends a visible note to the body explaining
// that the intended verdict couldn't be posted because GitHub blocks
// APPROVE / REQUEST_CHANGES on the poster's own PR. Returns the re-marshaled
// JSON and true on success. Used by PostReview to retry a review GitHub
// rejected for that reason. The inline comments are preserved byte-for-byte;
// only the disposition changes and the note is added, so it's obvious in the
// posted review that the verdict was intended to be APPROVE/REQUEST_CHANGES.
// Returns (nil, false) if the input isn't the expected payload shape or is
// already a COMMENT (no-op), so the caller can fall through untouched.
func downgradeEventToComment(payloadJSON []byte) ([]byte, bool) {
	var p reviewPayload
	if err := json.Unmarshal(payloadJSON, &p); err != nil {
		return nil, false
	}
	if p.Event == "COMMENT" {
		// Already COMMENT — a downgrade wouldn't change anything, so signal
		// no-op rather than pointlessly re-posting an identical payload.
		return nil, false
	}

	intended := p.Event
	verb := "approve"
	if intended == "REQUEST_CHANGES" {
		verb = "request changes on"
	}
	note := fmt.Sprintf(
		"> **Note:** this review was intended as **%s** but GitHub does not allow you to %s your own pull request, so it was posted as a comment.\n\n",
		intended, verb)

	p.Event = "COMMENT"
	p.Body = note + p.Body
	out, err := json.Marshal(p)
	if err != nil {
		return nil, false
	}
	return out, true
}

// lookPathForDecideActionsFunc resolves a tool on PATH, mirroring
// assets.go's lookPathFunc seam. Kept as its own package-level var (rather
// than reusing lookPathFunc directly) so decide-action tests can fake PATH
// resolution independently of the existing CheckReviewAssets preflight
// tests, without the two seams' fakes colliding across test files.
var lookPathForDecideActionsFunc = exec.LookPath

// --- pure helpers (Task 3) --------------------------------------------------

// findingLineRe matches a single review finding line of the form
// "file:line - severity - issue -> fix" (both hyphen and em-dash
// separators are accepted, per review-poster.md's documented format and
// pr_review_finalize.py's own comment). Capture groups: file, line,
// severity, issue, fix.
var findingLineRe = regexp.MustCompile(`(?m)^\s*(\S+):(\d+)\s*[-—]\s*(\w+)\s*[-—]\s*(.+?)\s*(?:->|→)\s*(.+?)\s*$`)

// countFindings counts finding lines in a review body — lines matching
// findingLineRe — and composes a short severity breakdown like "2
// warnings, 5 nits" by tallying the recognized severity token
// (critical/warning/nit, case-insensitive) captured from each finding
// line. This is a display-only heuristic for the post confirm prompt, not
// a full review-body parser: false positives/negatives on unusually
// formatted lines only affect the displayed count, never what PostReview
// actually posts (see buildReviewPayload, which posts the whole body
// regardless).
//
// If no recognized severities are found among the matched findings,
// breakdown is "" so the caller can fall back to a prompt with no
// parenthetical rather than a misleading "0 warnings, 0 nits".
func countFindings(body string) (total int, breakdown string) {
	matches := findingLineRe.FindAllStringSubmatch(body, -1)
	total = len(matches)

	counts := map[string]int{}
	order := []string{}
	for _, m := range matches {
		sev := strings.ToLower(strings.TrimSpace(m[3]))
		switch sev {
		case "critical", "warning", "nit":
			if _, seen := counts[sev]; !seen {
				order = append(order, sev)
			}
			counts[sev]++
		}
	}

	if len(order) == 0 {
		return total, ""
	}

	parts := make([]string, 0, len(order))
	for _, sev := range order {
		n := counts[sev]
		label := sev
		if n != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, label))
	}
	return total, strings.Join(parts, ", ")
}

// humanizeReviewBody best-effort rewrites body's prose via the humanizer
// agent, preserving every finding line and the VERDICT: line byte-for-byte
// per the standing instruction below. This call can never fail the
// caller: on any error (opt-out env var set, kiro-cli error, empty result)
// it returns the original body unchanged, mirroring
// ai-resources/workflows/humanize.py's documented "cosmetic, never
// load-bearing" contract exactly.
//
// Set HOWMUX_NO_HUMANIZE to any non-empty value to skip the pass entirely
// — the Go-side equivalent of the Python reference's
// WORKFLOWS_NO_HUMANIZE, needed so hermetic tests never shell out to a
// real kiro-cli via this path.
func humanizeReviewBody(ctx context.Context, body string) string {
	if os.Getenv("HOWMUX_NO_HUMANIZE") != "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return body
	}

	prompt := fmt.Sprintf(`Humanize the prose below in EMBEDDED mode: return ONLY the final
rewritten text, nothing else — no preamble, no list of patterns, no commentary.

Rewrite for tone only. Do not change, add, or remove any fact, claim, name,
number, date, or citation. Preserve the Markdown structure (headings, lists,
code fences).

This is a PR review body. Humanize ONLY the summary/overview prose
sentences. Every finding line and the VERDICT: line must pass through
byte-for-byte.

--- BEGIN TEXT ---
%s
--- END TEXT ---`, body)

	out, err := kiroOneshotFunc(ctx, "humanizer", prompt)
	if err != nil {
		logging.Debug("humanizeReviewBody: degraded to original body", "error", err)
		return body
	}
	if strings.TrimSpace(out) == "" {
		logging.Debug("humanizeReviewBody: empty result, degraded to original body")
		return body
	}
	return out
}

// verdictLineRe matches the LAST "VERDICT: <value>" line in a text,
// case-insensitively on the "VERDICT" token, capturing the (non-space)
// value. Used by parseVerdict.
var verdictLineRe = regexp.MustCompile(`(?im)^\s*VERDICT:\s*(\S+)\s*$`)

// parseVerdict returns the value of the LAST line matching
// "^VERDICT:\s*(\S+)" in reviewText, upper-cased. Returns "UNKNOWN" if no
// such line is present. Direct Go port of pr_review.py's parse_verdict.
func parseVerdict(reviewText string) string {
	matches := verdictLineRe.FindAllStringSubmatch(reviewText, -1)
	if len(matches) == 0 {
		return "UNKNOWN"
	}
	last := matches[len(matches)-1]
	return strings.ToUpper(strings.TrimSpace(last[1]))
}

// extractReviewBody trims kiro's one-shot tool-narration preamble to leave
// just the review: the consolidated review starts at the first top-level
// Markdown H1 ("# ..."); everything from that H1 to the end (including the
// trailing VERDICT: line) is kept. If no H1 is present, the entire raw
// text is returned (trimmed) so content is never silently dropped. Direct
// Go port of pr_review.py's extract_review_body.
func extractReviewBody(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "# ") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(raw)
}

// verdictEventMap maps a spool "verdict:" value to the GitHub review
// "event" gh api expects, per review-poster.md's documented mapping and
// pr-review-workflow.md's steering doc.
func verdictEvent(verdict string) string {
	switch strings.ToUpper(strings.TrimSpace(verdict)) {
	case "APPROVE":
		return "APPROVE"
	case "REQUEST_CHANGES":
		return "REQUEST_CHANGES"
	default:
		return "COMMENT"
	}
}

// reviewComment is one entry of the gh api pulls/reviews "comments" array.
type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// reviewPayload is the full JSON body posted to
// repos/<repo>/pulls/<pr>/reviews, matching review-poster.md's documented
// shape: {"event":..., "body":..., "comments":[...]}.
type reviewPayload struct {
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Comments []reviewComment `json:"comments"`
}

// diffHunkHeaderRe matches a unified-diff hunk header, capturing the
// RIGHT-side (new-file) start line and optional line count:
// "@@ -a,b +c,d @@" -> c is group 1, d (may be absent, defaults to 1) is
// group 2. The trailing section heading after the second @@ is ignored.
var diffHunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseDiffCommentableLines walks a unified diff (as produced by
// `gh pr diff`) and returns, per file path, the set of RIGHT-side line
// numbers a GitHub review comment may anchor to. GitHub only accepts an
// inline comment whose line is part of the diff on the new-file side —
// added ('+') lines and unchanged context (' ') lines both qualify;
// removed ('-') lines do not (they have no new-file line number). Anchoring
// to any other line is exactly what triggers the 422
// "line must be part of the diff" the caller is guarding against.
//
// File paths are taken from the "+++ b/<path>" header (the new-file side),
// with the leading "b/" stripped, matching the path shape GitHub expects in
// the comment "path" field. A "+++ /dev/null" (deleted file) contributes no
// commentable lines. Malformed hunk headers are skipped rather than
// aborting the whole parse, so one odd hunk never suppresses anchoring for
// the rest of the diff.
func parseDiffCommentableLines(diff string) map[string]map[int]bool {
	result := map[string]map[int]bool{}
	var curFile string
	var newLine int
	inHunk := false

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			target := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			// Strip a leading "b/" (git's new-file prefix); "/dev/null"
			// means the file was deleted and has no commentable lines.
			target = strings.TrimPrefix(target, "b/")
			if target == "/dev/null" {
				curFile = ""
			} else {
				curFile = target
				if _, ok := result[curFile]; !ok {
					result[curFile] = map[int]bool{}
				}
			}
			inHunk = false

		case strings.HasPrefix(line, "@@"):
			m := diffHunkHeaderRe.FindStringSubmatch(line)
			if m == nil {
				inHunk = false
				continue
			}
			start, err := strconv.Atoi(m[1])
			if err != nil {
				inHunk = false
				continue
			}
			newLine = start
			inHunk = true

		case inHunk && curFile != "":
			// Within a hunk, track new-file line numbers. Added ('+') and
			// context (' ') lines advance the new-file counter and are
			// commentable; removed ('-') lines do not advance it and are
			// not commentable. Anything else (e.g. "\ No newline at end of
			// file") is ignored without advancing.
			switch {
			case strings.HasPrefix(line, "+"):
				result[curFile][newLine] = true
				newLine++
			case strings.HasPrefix(line, " "):
				result[curFile][newLine] = true
				newLine++
			case strings.HasPrefix(line, "-"):
				// removed line: no new-file number, do not advance
			}
		}
	}
	return result
}

// buildReviewPayload parses findings out of body (reusing findingLineRe,
// the same line-matching regex countFindings uses) into one inline
// GitHub review comment per finding, maps verdict to the GitHub review
// "event" per verdictEvent, and marshals the whole thing into the JSON
// shape gh api pulls/reviews expects. Returns the marshaled payload, the
// resolved event, and the number of comments produced.
//
// When commentable is non-nil (a diff was available and parsed), a finding
// is only emitted as an inline comment if its file:line lands on the
// RIGHT side of the diff per commentable — this is the fix for the 422
// "line must be part of the diff" that GitHub rejects the whole review
// with when even one comment can't anchor. Findings that can't be anchored
// are not dropped: they are appended to the review body under an "Additional
// findings" section so the developer still sees them, just not inline.
//
// When commentable is nil (no diff_file, or it couldn't be read/parsed),
// behavior is unchanged: every parsed finding becomes an inline comment
// (best effort — the previous behavior, preserved so a missing diff never
// silently strips inline comments that would have anchored fine).
func buildReviewPayload(repo string, pr int, verdict string, body string, commentable map[string]map[int]bool) (payloadJSON []byte, event string, commentCount int, err error) {
	event = verdictEvent(verdict)

	matches := findingLineRe.FindAllStringSubmatch(body, -1)
	comments := make([]reviewComment, 0, len(matches))
	var unanchored []string
	for _, m := range matches {
		file := m[1]
		line, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			continue
		}
		severity := strings.ToLower(strings.TrimSpace(m[3]))
		issue := strings.TrimSpace(m[4])
		fix := strings.TrimSpace(m[5])
		commentBody := fmt.Sprintf("%s: %s → %s", severity, issue, fix)

		if commentable != nil {
			lines, fileInDiff := commentable[file]
			if !fileInDiff || !lines[line] {
				// Can't anchor to the diff — GitHub would 422 the whole
				// review. Fold it into the body instead of dropping it.
				unanchored = append(unanchored,
					fmt.Sprintf("- `%s:%d` — %s", file, line, commentBody))
				continue
			}
		}

		comments = append(comments, reviewComment{
			Path: file,
			Line: line,
			Body: commentBody,
		})
	}

	finalBody := body
	if len(unanchored) > 0 {
		finalBody = body +
			"\n\n---\n\n### Additional findings (not on the diff)\n\n" +
			"These reference lines outside this PR's diff, so they couldn't be posted inline:\n\n" +
			strings.Join(unanchored, "\n")
	}

	payload := reviewPayload{
		Event:    event,
		Body:     finalBody,
		Comments: comments,
	}

	payloadJSON, err = json.Marshal(payload)
	if err != nil {
		return nil, event, 0, fmt.Errorf("failed to marshal review payload: %w", err)
	}
	return payloadJSON, event, len(comments), nil
}

// --- discard (Task 3) -------------------------------------------------------

// DiscardReview implements the "discard" decide action for a single
// review: it writes decision: discard to the spool file's front-matter
// (via the existing DecisionWriter.SetDecision, reused as-is) and then
// archives the file straight to done/ via archiveToDone. No subprocess
// calls are made — discard never invokes kiro-cli or gh.
//
// Errors from DecisionWriter.SetDecision are returned verbatim (it already
// produces the three-tier "invalid decision" / "no spool file" / "already
// finalized" error shapes callers depend on); an archiveToDone failure
// after a successful decision write is also returned, wrapped, since the
// caller needs to know the file may now be inconsistent (decision written
// but not yet archived).
func DiscardReview(rec Record) error {
	dw := NewDecisionWriter()
	if err := dw.SetDecision(rec.SpoolPath, "discard"); err != nil {
		return err
	}

	resolved, found, _ := resolveSpoolPath(rec.SpoolPath)
	if !found {
		return fmt.Errorf("no spool file to archive for %s#%d (spool path resolution changed mid-operation): %s", rec.Repo, rec.PR, rec.SpoolPath)
	}

	if err := archiveToDone(resolved); err != nil {
		return fmt.Errorf("discard: decision recorded but archive failed: %w", err)
	}

	return nil
}

// --- post (Task 3) ----------------------------------------------------------

// PostReview implements the "post" decide action for a single review: it
// writes decision: post first (crash-recovery write — if the process dies
// or gh fails after this point, the file is left in pending/ with
// decision: post already set, so a retry of PostReview can pick it up
// again), best-effort humanizes the review body's prose, builds the
// GitHub review payload, POSTs it via postReviewCommandFunc, and — only on
// success — archives the file to done/. On gh failure, the file is
// deliberately left untouched in pending/ (decision already written);
// PostReview does not attempt any cleanup of that state, since leaving it
// as-is IS the crash-recovery contract this issue's open question
// resolves.
//
// Returns the number of inline comments posted on success.
func PostReview(ctx context.Context, rec Record) (commentsPosted int, err error) {
	dw := NewDecisionWriter()
	if err := dw.SetDecision(rec.SpoolPath, "post"); err != nil {
		return 0, err
	}

	resolved, found, inDoneDir := resolveSpoolPath(rec.SpoolPath)
	if !found {
		return 0, fmt.Errorf("no spool file for %s#%d (spool path resolution changed mid-operation): %s", rec.Repo, rec.PR, rec.SpoolPath)
	}
	if inDoneDir {
		return 0, fmt.Errorf("review already finalized (archived to done/); cannot post: %s", resolved)
	}

	body, info := ReadSpoolBody(resolved, "")
	if !info.Found {
		return 0, fmt.Errorf("failed to read spool body for %s#%d: %s", rec.Repo, rec.PR, resolved)
	}

	humanized := humanizeReviewBody(ctx, body)

	// Load the saved PR diff (if the spool recorded one) and parse the set
	// of RIGHT-side lines each file exposes. buildReviewPayload uses this to
	// only anchor inline comments to lines GitHub will accept; findings off
	// the diff fold into the review body instead. A missing/unreadable
	// diff_file yields a nil map, which buildReviewPayload treats as "no
	// anchoring info" and posts every finding inline (prior best-effort
	// behavior) — the body-only fallback below still covers a resulting 422.
	var commentable map[string]map[int]bool
	if data, readErr := os.ReadFile(resolved); readErr == nil {
		if diffFile := ParseSpoolFrontMatter(data)["diff_file"]; diffFile != "" {
			if diffData, diffErr := os.ReadFile(diffFile); diffErr == nil {
				commentable = parseDiffCommentableLines(string(diffData))
			}
		}
	}

	payloadJSON, _, commentCount, err := buildReviewPayload(rec.Repo, rec.PR, info.Verdict, humanized, commentable)
	if err != nil {
		return 0, fmt.Errorf("failed to build review payload for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	// postPayload writes a marshaled payload to a throwaway temp file and
	// POSTs it via the seam. Factored out so the body-only fallback below
	// can reuse the exact same write+post path with a second payload.
	postPayload := func(pj []byte) error {
		tmp, err := os.CreateTemp("", fmt.Sprintf("pr-review-payload-%d-*.json", rec.PR))
		if err != nil {
			return fmt.Errorf("failed to create temp payload file: %w", err)
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err := tmp.Write(pj); err != nil {
			tmp.Close()
			return fmt.Errorf("failed to write temp payload file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return fmt.Errorf("failed to close temp payload file: %w", err)
		}
		_, err = postReviewCommandFunc(ctx, tmpPath, rec.Repo, rec.PR)
		return err
	}

	postErr := postPayload(payloadJSON)

	if postErr != nil && isOwnPRReviewError(postErr) {
		// GitHub forbids APPROVE / REQUEST_CHANGES on your OWN pull request
		// ("Review cannot approve/request changes on your own pull request")
		// — the only event self-authored PRs accept is COMMENT. Downgrade
		// the event to COMMENT and retry once. The findings, verdict prose,
		// and inline comments are all preserved; only the review's blocking
		// disposition changes, which GitHub was rejecting outright anyway.
		if downgraded, ok := downgradeEventToComment(payloadJSON); ok {
			if dgErr := postPayload(downgraded); dgErr == nil {
				postErr = nil
			} else {
				// Keep going to the body-only fallback below using the
				// downgraded error (e.g. own-PR AND an off-diff comment).
				postErr = dgErr
				payloadJSON = downgraded
			}
		}
	}

	if postErr != nil && commentCount > 0 && is422(postErr) {
		// GitHub rejected the review with a 422 despite our best effort to
		// anchor comments (a comment line the diff parser accepted may still
		// be unmappable to a diff position — e.g. a line inside an
		// expanded/collapsed hunk GitHub treats differently). Rather than
		// lose the entire review, retry once with a comments-less payload so
		// the summary + verdict still land. The individual findings remain
		// in the body prose, so nothing is silently dropped.
		// An empty (non-nil) commentable map anchors nothing, so every
		// finding folds into the body prose and the payload carries zero
		// inline comments — exactly the body-only review we want to retry.
		fallbackJSON, _, _, buildErr := buildReviewPayload(rec.Repo, rec.PR, info.Verdict, humanized, map[string]map[int]bool{})
		if buildErr == nil {
			// If we already downgraded the event above, keep it downgraded
			// on the body-only retry too, otherwise GitHub would reject it
			// for the same own-PR reason.
			if isOwnPRReviewError(postErr) {
				if dg, ok := downgradeEventToComment(fallbackJSON); ok {
					fallbackJSON = dg
				}
			}
			if fbErr := postPayload(fallbackJSON); fbErr == nil {
				commentCount = 0
				postErr = nil
			}
		}
	}

	if postErr != nil {
		// Deliberately do NOT archive: decision: post is already recorded
		// (crash-recovery contract) — the file stays in pending/ so a retry
		// of PostReview can pick it up again.
		return 0, fmt.Errorf("failed to post review for %s#%d: %w", rec.Repo, rec.PR, postErr)
	}

	if err := archiveToDone(resolved); err != nil {
		return commentCount, fmt.Errorf("post succeeded but archive failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	return commentCount, nil
}

// --- revise / rereview (Task 4) --------------------------------------------

// crossCuttingLenses mirrors pr_review.py's CROSS_CUTTING_LENSES: the
// lens-name -> agent-name pairs run against every rereview regardless of
// language.
var crossCuttingLenses = map[string]string{
	"security":    "review-security-agent",
	"performance": "review-performance-agent",
	"testing":     "review-testing-agent",
}

// languageReviewers mirrors pr_review.py's _LANG_REVIEWER table.
var languageReviewers = map[string]string{
	"python": "python-reviewer",
	"go":     "go-reviewer",
	"node":   "node-reviewer",
	"java":   "java-reviewer",
	"astro":  "astro-reviewer",
}

// resolveLanguageReviewer mirrors pr_review.py's resolve_language_reviewer
// for the non-valkey case (rereview's fan-out does not carry a valkey
// flag through the spool front-matter, matching the Python reference's
// own do_rereview, which never passes valkey=True either).
func resolveLanguageReviewer(language string) string {
	if reviewer, ok := languageReviewers[language]; ok {
		return reviewer
	}
	return languageReviewers["python"]
}

// buildRevisePrompt constructs the exact prompt do_revise uses in the
// Python reference (pr_review_finalize.py), substituting the literal
// "(no notes provided)" placeholder when notes is blank.
func buildRevisePrompt(body string, notes string) string {
	if strings.TrimSpace(notes) == "" {
		notes = "(no notes provided)"
	}
	return fmt.Sprintf(`Revise a consolidated PR review.

Existing consolidated review to revise:
%s

Human revision notes (apply these):
%s

Produce the updated single markdown review document, most-severe first. Print
the whole review as your reply. End with a final line exactly:
`+"`VERDICT: <APPROVE|COMMENT|REQUEST_CHANGES>`", body, notes)
}

// ReviseReview implements the "revise" decide action for a single review:
// a consolidator-only re-run using the human's revision notes. It reads
// the current body, resolves the notes to feed the consolidator, calls the
// review-consolidator agent via kiroOneshotFunc, extracts the new
// body/verdict, and rewrites the spool file in place via RewriteSpoolEntry
// with the decision cleared (so the file returns to pending/ for another
// human look). The spool file is left completely untouched if the kiro-cli
// call fails — no partial rewrite is ever written.
//
// Notes resolution: inlineNotes (typed into the in-app multi-line composer
// at decide time) take precedence when non-blank — they are passed straight
// into the consolidator prompt and are NOT persisted to the spool
// front-matter. When inlineNotes is blank, ReviseReview falls back to the
// persisted decision_notes: front-matter field (the manual-flow notes set
// via the single-line notes editor). inlineNotes may contain newlines;
// buildRevisePrompt interpolates them verbatim into a multi-line prompt, so
// multi-line notes are accepted as-is.
func ReviseReview(ctx context.Context, rec Record, inlineNotes string) (newVerdict string, err error) {
	resolved, found, inDoneDir := resolveSpoolPath(rec.SpoolPath)
	if !found {
		return "", fmt.Errorf("no spool file for %s#%d: %s", rec.Repo, rec.PR, rec.SpoolPath)
	}
	if inDoneDir {
		return "", fmt.Errorf("review already finalized (archived to done/); cannot revise: %s", resolved)
	}

	body, info := ReadSpoolBody(resolved, "")
	if !info.Found {
		return "", fmt.Errorf("failed to read spool body for %s#%d: %s", rec.Repo, rec.PR, resolved)
	}
	notes := inlineNotes
	if strings.TrimSpace(notes) == "" {
		notes = CurrentDecisionNotes(resolved, "")
	}

	prompt := buildRevisePrompt(body, notes)
	raw, err := kiroOneshotFunc(ctx, "review-consolidator", prompt)
	if err != nil {
		return "", fmt.Errorf("revise failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	newBody := extractReviewBody(raw)
	newVerdict = parseVerdict(newBody)

	if err := RewriteSpoolEntry(resolved, newBody, newVerdict, true); err != nil {
		return "", fmt.Errorf("revise: kiro-cli succeeded but rewrite failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	return newVerdict, nil
}

// lensResult holds one lens's raw kiro-cli output (or its error, encoded
// as an "ERROR: ..." string, mirroring pr_review.py's run_lens error
// encoding) alongside the lens name and agent profile used, for
// consolidation.
type lensResult struct {
	lensName string
	profile  string
	output   string
	err      error
}

// RereviewReview implements the "rereview" decide action for a single
// review: a full multi-lens fan-out against the original diff, followed
// by consolidation. If the spool file's diff_file front-matter field is
// empty or no longer exists on disk, this degrades to the exact same
// behavior as ReviseReview (same prompt template, same rewrite, same
// "back to pending/, decision cleared" outcome) — mirroring the Python
// reference's documented degrade path — and reports
// degradedToRevise=true.
//
// Notes resolution mirrors ReviseReview: inlineNotes (typed into the
// in-app multi-line composer at decide time) take precedence when
// non-blank and are threaded through to both the lens-fan-out orientation
// context and — on the degrade path — ReviseReview; they are never
// persisted to the spool front-matter. When inlineNotes is blank, the
// persisted decision_notes: front-matter field is used instead.
//
// Known inherited limitation (not new to this issue): the spool
// front-matter does not carry a "language" key, so the language lens is
// always resolved as "python", mirroring pr_review_finalize.py's own
// --language default. A future issue could plumb the original review's
// detected language through the spool schema; this function does not
// attempt to infer it from the diff.
//
// Concurrency: the four cross-cutting lenses plus the language lens are
// run concurrently via goroutines, the direct equivalent of the Python
// reference's ThreadPoolExecutor(max_workers=4).map(...). Each goroutine
// writes ONLY to its own pre-allocated, disjoint index of a results
// slice (results[i]) — there is no shared map and no mutex, and the
// slice is never read until after wg.Wait() returns, by construction
// making concurrent writes impossible. The one shared handle the
// goroutines touch is the context.CancelFunc used to short-circuit
// siblings on first failure, which is safe for concurrent use per the
// context package's contract. This must remain race-free under
// `go test -race`; see TestRereviewReview_LensFanOut_NoRaceCondition.
func RereviewReview(ctx context.Context, rec Record, inlineNotes string) (newVerdict string, degradedToRevise bool, err error) {
	resolved, found, inDoneDir := resolveSpoolPath(rec.SpoolPath)
	if !found {
		return "", false, fmt.Errorf("no spool file for %s#%d: %s", rec.Repo, rec.PR, rec.SpoolPath)
	}
	if inDoneDir {
		return "", false, fmt.Errorf("review already finalized (archived to done/); cannot rereview: %s", resolved)
	}

	data, readErr := os.ReadFile(resolved)
	if readErr != nil {
		return "", false, fmt.Errorf("failed to read spool file for %s#%d: %w", rec.Repo, rec.PR, readErr)
	}
	fields := ParseSpoolFrontMatter(data)
	diffFile := fields["diff_file"]

	diffExists := false
	if diffFile != "" {
		if _, statErr := os.Stat(diffFile); statErr == nil {
			diffExists = true
		}
	}

	if !diffExists {
		verdict, reviseErr := ReviseReview(ctx, rec, inlineNotes)
		return verdict, true, reviseErr
	}

	notes := inlineNotes
	if strings.TrimSpace(notes) == "" {
		notes = CurrentDecisionNotes(resolved, "")
	}
	if strings.TrimSpace(notes) == "" {
		notes = "(none)"
	}
	context_ := fmt.Sprintf("Re-review requested. Human revision notes to weigh: %s", notes)

	language := resolveLanguageReviewer("python")
	lensNames := []string{"language", "performance", "security", "testing"}
	lensProfiles := map[string]string{
		"language":    language,
		"security":    crossCuttingLenses["security"],
		"performance": crossCuttingLenses["performance"],
		"testing":     crossCuttingLenses["testing"],
	}

	// Disjoint pre-allocated slice: goroutine i writes ONLY results[i].
	// No shared map, no mutex, no read of results until after wg.Wait().
	//
	// fanCtx is a cancellable child of ctx: the first lens to fail cancels
	// it, so the sibling kiro-cli subprocesses (spawned via
	// exec.CommandContext inside kiroOneshotFunc) are signalled to stop
	// rather than each running its full agent invocation to completion only
	// to have the aggregate error check below discard them all. cancel() is
	// also invoked on the success path via defer, releasing the context's
	// resources.
	fanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]lensResult, len(lensNames))
	var wg sync.WaitGroup
	for i, lensName := range lensNames {
		i, lensName := i, lensName
		profile := lensProfiles[lensName]
		wg.Add(1)
		go func() {
			defer wg.Done()
			prompt := buildLensPrompt(lensName, diffFile, context_)
			out, callErr := kiroOneshotFunc(fanCtx, profile, prompt)
			results[i] = lensResult{lensName: lensName, profile: profile, output: out, err: callErr}
			if callErr != nil {
				// Signal siblings to stop; the aggregate check below still
				// reports the first error in lens order.
				cancel()
			}
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return "", false, fmt.Errorf("rereview lens %q (%s) failed for %s#%d: %w", r.lensName, r.profile, rec.Repo, rec.PR, r.err)
		}
	}

	consolidatePrompt := buildConsolidatePrompt(diffFile, results)
	raw, err := kiroOneshotFunc(ctx, "review-consolidator", consolidatePrompt)
	if err != nil {
		return "", false, fmt.Errorf("rereview consolidation failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	newBody := extractReviewBody(raw)
	newVerdict = parseVerdict(newBody)

	if err := RewriteSpoolEntry(resolved, newBody, newVerdict, true); err != nil {
		return "", false, fmt.Errorf("rereview: kiro-cli succeeded but rewrite failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	return newVerdict, false, nil
}

// buildLensPrompt constructs the per-lens review prompt, mirroring
// pr_review.py's run_lens prompt shape (diff path + lens name +
// orientation/context).
func buildLensPrompt(lensName string, diffFile string, context string) string {
	return fmt.Sprintf(`Review the unified diff at %s through the %q lens.

Orientation from the context pass:
%s

Rules:
- Review ONLY the changed lines shown in the diff.
- Every finding must cite file and line, and describe a REAL problem
  (not a style preference). Verify each line number against the diff.
- If you find nothing substantive for your lens, say so explicitly.
- This is READ-ONLY: return your findings inline. Do NOT write any files.

Return findings as a markdown list. For each: `+"`file:line - severity - issue -> suggested fix`"+`.`, diffFile, lensName, context)
}

// buildConsolidatePrompt constructs the consolidation prompt from the
// collected lens results, mirroring pr_review.py's run_consolidate prompt
// shape (a digest of every lens's raw output, sorted by lens name for
// determinism).
func buildConsolidatePrompt(diffFile string, results []lensResult) string {
	sorted := make([]lensResult, len(results))
	copy(sorted, results)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].lensName < sorted[i].lensName {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var digest strings.Builder
	for i, r := range sorted {
		if i > 0 {
			digest.WriteString("\n\n")
		}
		digest.WriteString(fmt.Sprintf("### Lens: %s (profile: %s)\n%s", r.lensName, r.profile, r.output))
	}

	return fmt.Sprintf(`You are consolidating a multi-lens code review of the diff at %s.

Below are the raw findings from each review lens. Your job:
1. Merge duplicate findings across lenses (same file:line, same issue).
2. Drop nitpicks: keep a finding only if it has functional, security, or
   correctness impact (Medium+ severity, or any security finding).
3. For each surviving finding, confirm it against the diff - discard any that
   don't match the actual changed lines.
4. Produce a single ordered list (most severe first).

Then give an overall verdict: APPROVE, COMMENT, or REQUEST_CHANGES.

Raw lens findings:
%s

Produce a markdown review document as the deliverable, most-severe first.
End with a final line exactly:
`+"`VERDICT: <APPROVE|COMMENT|REQUEST_CHANGES>`", diffFile, digest.String())
}
