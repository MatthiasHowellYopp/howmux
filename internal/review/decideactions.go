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
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/matthiashowellyopp/howmux/internal/github"
	"github.com/matthiashowellyopp/howmux/internal/kirocli"
	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// --- subprocess seams (Task 2) ---------------------------------------------

// kiroOneshotFunc is the subprocess-execution seam every agentic step in
// this package (humanize, revise, rereview's lens fan-out + consolidation,
// the auto-post auditor) calls through. It is package-level so tests can
// substitute a fake and assert on (agentName, prompt) without ever shelling
// out to a real kiro-cli. Mirrors ai-resources/workflows/kiro_oneshot.py's
// kiro_oneshot_meta() contract: same argv shape (stream-json transport),
// same two error conditions (non-zero exit, empty answer).
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

// defaultKiroOneshot is the real kiroOneshotFunc implementation: one
// `kiro-cli chat --no-interactive --trust-all-tools --agent <agentName>
// --output-format stream-json "<prompt>"` turn via internal/kirocli, the Go
// port of kiro_oneshot.py's kiro_oneshot_meta. It errors on a non-zero exit
// or an empty answer (KiroOneshotError's two conditions), and returns the
// per-turn metadata (engine, turn duration, context usage, stop reason) for
// the audit trail.
//
// model is passed as --model; "" passes none, so kiro-cli uses its default.
func defaultKiroOneshot(ctx context.Context, agentName, model, prompt string) (kirocli.Result, error) {
	return kirocli.Run(ctx, kirocli.Request{Agent: agentName, Prompt: prompt, Model: model, TrustAllTools: true})
}

// postReviewCommandFunc is the subprocess-execution seam PostReview invokes
// through to publish inline PR comments. Package-level so tests substitute
// a fake `gh` and assert on the invocation without hitting the network.
// Mirrors fetchDiffFunc's (runner.go) established seam pattern.
var postReviewCommandFunc = defaultPostReviewCommand

// postFileCommentFunc is the seam PostReview posts each file-level comment
// through (a finding whose file is in the diff but whose line isn't).
var postFileCommentFunc = defaultPostFileComment

// prHeadSHAFunc resolves a PR's head commit, used as a file-level comment's
// commit_id when the spool recorded no reviewed_sha.
var prHeadSHAFunc = func(repo string, pr int) (string, error) {
	p, err := github.GetPR(repo, pr)
	if err != nil {
		return "", err
	}
	return p.HeadSHA(), nil
}

// defaultPostFileComment shells to `gh api repos/<repo>/pulls/<pr>/comments
// --method POST --input <payloadFile>`.
func defaultPostFileComment(ctx context.Context, payloadFile string, repo string, pr int) error {
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, pr),
		"--method", "POST",
		"--input", payloadFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh api pulls/comments failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

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

// findingLineRe matches a single review finding line, whose canonical form
// is "file:line - severity - issue -> fix" (both hyphen and em-dash
// separators are accepted, per review-poster.md's documented format and
// pr_review_finalize.py's own comment).
//
// The consolidator (ai-resources' review-consolidator) is an LLM, so the
// lines it emits vary in surface form around that core shape. This regex is
// deliberately tolerant of the decorations the consolidator actually
// produces in the wild, so real findings parse into inline comments instead
// of silently falling through to the review body (the block-comment-only
// regression seen across Bit-Quill/flowdra#78/#81/#84/#85/#86/#88). The
// shapes observed in real reviews, all of which must anchor:
//
//   - a leading markdown list marker ("- ", "* ", "+ ", "1. ", "1) ") — stripped;
//   - the file:line wrapped in backticks or bold stars (`path:line`, **path:line**),
//     including a stray unclosed backtick ("`path:line - …" from #84);
//   - a line RANGE after the colon ("path:621-622") — we anchor to the FIRST
//     line of the range (#86), which is always on the diff if the range is;
//   - a bolded and/or compound severity ("**Medium**", "low/medium") with an
//     optional parenthetical qualifier ("low (non-blocking)", #86/#88);
//   - NO "severity -" middle at all, and/or NO "-> fix" arrow — the whole tail
//     becomes the issue text (#78).
//
// Capture groups:
//
//	1 file      2 line (first number of any range)
//	3 severity token (optional — may be bolded/compound/qualified; "" if absent)
//	4 the remaining text (issue, optionally "… -> fix")
//
// The severity "- … -" middle is optional AND constrained to recognized
// severity keywords (critical/high/medium/low/warning/nit/info/minor/major),
// so a hyphenated ordinary word in the issue text ("single-digit idiom", #78)
// is not mis-parsed as a severity. parseFindingLine (below) normalizes group
// 3 to a bare keyword and splits group 4 on the arrow; buildReviewPayload and
// countFindings both go through it rather than indexing the groups directly.
var findingLineRe = regexp.MustCompile(
	`(?mi)^\s*(?:[-*+]\s+|\d+[.)]\s+)?` + // optional list marker
		"[`*]*" + `(\S+?):(\d+)(?:-\d+)?` + "[`*]*" + // file:line[-range], opt ` or * wrappers
		`\s*[-—]\s*` + // first separator
		// optional "severity -" middle, severity constrained to real keywords
		`(?:(\**(?:critical|high|medium|low|warning|warn|nit|info|minor|major)` +
		`(?:/(?:critical|high|medium|low|warning|warn|nit|info|minor|major))*\**` +
		`(?:\s*\([^)]*\))?)\s*[-—]\s*)?` +
		`(.+?)\s*$`) // rest (issue, optionally "… -> fix")

// finding is one parsed review finding line: the anchor (file + first line),
// a normalized bare severity keyword (lowercased, compound/bold/qualifier
// stripped; "" when the line carried no recognized severity), and the human
// text split into issue and suggested fix on the "->"/"→" arrow (fix is ""
// when the line had no arrow). It is the single shape both buildReviewPayload
// (what posts) and countFindings (the confirm-prompt tally) consume, so the
// two can never drift in how they interpret findingLineRe's groups.
type finding struct {
	file     string
	line     int
	severity string
	issue    string
	fix      string
}

// parseFindingLine applies findingLineRe to one line and normalizes its
// capture groups into a finding. ok is false when the line is not a finding
// (no file:line anchor) or the line number does not parse — callers skip
// those. The whole line's text (minus the anchor and an optional severity
// middle) is preserved: when there is a "-> fix" arrow the text splits into
// issue/fix, otherwise the entire remainder is the issue and fix is "".
func parseFindingLine(line string) (finding, bool) {
	m := findingLineRe.FindStringSubmatch(line)
	if m == nil {
		return finding{}, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return finding{}, false
	}

	// Normalize severity: strip bold stars and any parenthetical/compound
	// qualifier down to the leading keyword, lowercased. Empty when the line
	// carried no recognized severity middle.
	sev := strings.ToLower(strings.Trim(strings.TrimSpace(m[3]), "*"))
	if i := strings.IndexAny(sev, "/ ("); i >= 0 {
		sev = sev[:i]
	}

	// Split the remaining text on the first "->" or "→". No arrow → the whole
	// remainder is the issue and there is no explicit suggested fix.
	rest := strings.TrimSpace(m[4])
	issue, fix := rest, ""
	if idx := strings.Index(rest, "->"); idx >= 0 {
		issue, fix = strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+2:])
	} else if idx := strings.Index(rest, "→"); idx >= 0 {
		issue, fix = strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+len("→"):])
	}

	return finding{file: m[1], line: n, severity: sev, issue: issue, fix: fix}, true
}

// countFindings counts finding lines in a review body — lines matching
// findingLineRe — and composes a short severity breakdown like "2
// warnings, 5 nits" by tallying the recognized severity token
// (critical/warning/nit, case-insensitive) captured from each finding
// line. This is a display-only heuristic for the post confirm prompt, not
// a full review-body parser. It counts the same findings planReview posts
// (finding lines under Reviewer notes are excluded).
//
// If no recognized severities are found among the matched findings,
// breakdown is "" so the caller can fall back to a prompt with no
// parenthetical rather than a misleading "0 warnings, 0 nits".
func countFindings(body string) (total int, breakdown string) {
	counts := map[string]int{}
	order := []string{}
	for _, f := range splitReview(body).findings {
		total++
		switch f.severity {
		case "critical", "warning", "nit":
			if _, seen := counts[f.severity]; !seen {
				order = append(order, f.severity)
			}
			counts[f.severity]++
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

// humanizeReviewBody best-effort rewrites the review summary (the only prose
// PostReview posts) via the humanizer agent, keeping it as short or shorter
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

This is the summary of a PR review. Keep it as short as it is now or
shorter; do not add findings, rationale or caveats.

--- BEGIN TEXT ---
%s
--- END TEXT ---`, body)

	res, err := kiroOneshotFunc(ctx, "humanizer", "", prompt)
	if err != nil {
		logging.Debug("humanizeReviewBody: degraded to original body", "error", err)
		return body
	}
	out := res.Text
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

// fileComment is the body of a file-level PR comment (POST
// repos/<repo>/pulls/<pr>/comments with subject_type=file). Used for a
// finding whose file is in the diff but whose line isn't: GitHub rejects the
// whole review if any inline comment misses the diff.
type fileComment struct {
	Path        string `json:"path"`
	Body        string `json:"body"`
	CommitID    string `json:"commit_id"`
	SubjectType string `json:"subject_type"`
}

// reviewPlan is everything PostReview needs to publish one review, built in
// code from the consolidated review so what reaches GitHub doesn't depend on
// a model's judgment.
type reviewPlan struct {
	// review is the create-review payload: the summary as the body and one
	// inline comment per on-diff finding.
	review reviewPayload
	// fileComments are off-diff findings in files the diff touches. CommitID
	// is left blank; PostReview fills it in.
	fileComments []fileComment
	// unposted are finding lines that can't be anchored anywhere (file not
	// in the diff, or an unparseable item under Findings). Never posted; the
	// full review stays in the archived spool file.
	unposted []string
	// notes is the non-posted part of the review: verdict rationale,
	// verified non-issues, dropped findings. Never posted.
	notes string
}

// findingCommentBody renders a parsed finding as comment text. A missing
// severity (the line carried no recognized keyword, e.g. #78) drops the
// "severity:" prefix; a missing fix (no "->" arrow) drops the "→ fix" suffix.
func findingCommentBody(f finding) string {
	switch {
	case f.severity != "" && f.fix != "":
		return fmt.Sprintf("%s: %s → %s", f.severity, f.issue, f.fix)
	case f.severity != "":
		return fmt.Sprintf("%s: %s", f.severity, f.issue)
	case f.fix != "":
		return fmt.Sprintf("%s → %s", f.issue, f.fix)
	default:
		return f.issue
	}
}

// planReview builds the review to post from a consolidated review body:
//
//   - Review body: the ## Summary section only (or summaryOverride, the
//     humanized summary, when non-blank), capped by capSummary. No verdict
//     rationale, no list of what was cleared or dropped, no VERDICT: line,
//     and no findings.
//   - Inline comments: one per finding whose file:line is on the RIGHT side
//     of the diff per commentable.
//   - File-level comments: findings whose file is in the diff but whose line
//     isn't.
//   - Unposted: findings whose file isn't in the diff at all.
//
// When commentable is nil (no diff_file, or it couldn't be read/parsed),
// every parsed finding becomes an inline comment (best effort, so a missing
// diff never silently strips comments that would have anchored fine).
func planReview(verdict, body, summaryOverride string, commentable map[string]map[int]bool) reviewPlan {
	parts := splitReview(body)
	summary := parts.summary
	if strings.TrimSpace(summaryOverride) != "" {
		summary = summaryOverride
	}
	summary = capSummary(summary, parts.findings)

	plan := reviewPlan{
		unposted: append([]string{}, parts.unparsed...),
		notes:    parts.notes,
	}
	comments := make([]reviewComment, 0, len(parts.findings))
	for i, f := range parts.findings {
		text := findingCommentBody(f)
		if commentable == nil || commentable[f.file][f.line] {
			comments = append(comments, reviewComment{Path: f.file, Line: f.line, Body: text})
			continue
		}
		if _, fileInDiff := commentable[f.file]; fileInDiff {
			plan.fileComments = append(plan.fileComments, fileComment{
				Path:        f.file,
				Body:        fmt.Sprintf("Line %d: %s", f.line, text),
				SubjectType: "file",
			})
			continue
		}
		plan.unposted = append(plan.unposted, parts.raw[i])
	}

	// GitHub rejects a COMMENT / REQUEST_CHANGES review with an empty body
	// (and a 422 here would leave the spool failing on every retry). Fall
	// back to a neutral line rather than inventing an assessment.
	event := verdictEvent(verdict)
	if summary == "" {
		switch {
		case len(comments) > 0 || len(plan.fileComments) > 0:
			summary = "See the inline comments."
		case len(plan.unposted) > 0 && event != "APPROVE":
			summary = "The findings from this review refer to code outside this PR's diff, so none were posted as comments."
		case event != "APPROVE":
			summary = "No comments."
		}
	}

	plan.review = reviewPayload{Event: event, Body: summary, Comments: comments}
	return plan
}

// buildReviewPayload marshals the create-review payload planReview builds for
// body (no summary override). Returns the marshaled payload, the resolved
// event, and the number of inline comments.
func buildReviewPayload(repo string, pr int, verdict string, body string, commentable map[string]map[int]bool) (payloadJSON []byte, event string, commentCount int, err error) {
	plan := planReview(verdict, body, "", commentable)
	payloadJSON, err = json.Marshal(plan.review)
	if err != nil {
		return nil, plan.review.Event, 0, fmt.Errorf("failed to marshal review payload: %w", err)
	}
	return payloadJSON, plan.review.Event, len(plan.review.Comments), nil
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
// again), best-effort humanizes the review summary, builds the review via
// planReview, POSTs it via postReviewCommandFunc, posts any file-level
// comments, and — only on success — archives the file to done/.
//
// What is posted: the ## Summary section as the review body, and the
// findings as inline (or file-level) comments. The verdict rationale,
// reviewer notes, dropped findings and any finding that can't be anchored
// are never posted; they stay in the spool file, archived whole to done/. On gh failure, the file is
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

	// Load the spool front-matter once: diff_file anchors the comments and
	// reviewed_sha is the commit file-level comments attach to.
	var fm map[string]string
	if data, readErr := os.ReadFile(resolved); readErr == nil {
		fm = ParseSpoolFrontMatter(data)
	}

	// Load the saved PR diff (if the spool recorded one) and parse the set
	// of RIGHT-side lines each file exposes, so inline comments only anchor
	// to lines GitHub accepts. A missing/unreadable diff_file yields a nil
	// map, which planReview treats as "no anchoring info" and posts every
	// finding inline; the file-comment fallback below covers a resulting 422.
	var commentable map[string]map[int]bool
	if diffFile := fm["diff_file"]; diffFile != "" {
		if diffData, diffErr := os.ReadFile(diffFile); diffErr == nil {
			commentable = parseDiffCommentableLines(string(diffData))
		}
	}

	// Only the summary is posted as prose, so only the summary is humanized.
	summary := humanizeReviewBody(ctx, splitReview(body).summary)
	plan := planReview(info.Verdict, body, summary, commentable)
	payloadJSON, err := json.Marshal(plan.review)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal review payload for %s#%d: %w", rec.Repo, rec.PR, err)
	}
	commentCount := len(plan.review.Comments)

	// postPayload writes a marshaled payload to a throwaway temp file and
	// POSTs it via the seam. Factored out so the fallback below can reuse
	// the exact same write+post path with a second payload.
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
		// the event to COMMENT and retry once. The summary and inline
		// comments are preserved; only the review's blocking disposition
		// changes, which GitHub was rejecting outright anyway.
		if downgraded, ok := downgradeEventToComment(payloadJSON); ok {
			if dgErr := postPayload(downgraded); dgErr == nil {
				postErr = nil
			} else {
				// Keep going to the fallback below using the downgraded
				// error (e.g. own-PR AND an off-diff comment).
				postErr = dgErr
				payloadJSON = downgraded
			}
		}
	}

	fileComments := plan.fileComments
	if postErr != nil && commentCount > 0 && is422(postErr) {
		// GitHub rejected the review with a 422 despite our best effort to
		// anchor comments (a line the diff parser accepted may still be
		// unmappable to a diff position). Rather than lose the review, retry
		// once without inline comments and post those findings as
		// file-level comments instead. They never fold into the summary.
		var fallback reviewPayload
		if err := json.Unmarshal(payloadJSON, &fallback); err == nil {
			moved := fallback.Comments
			fallback.Comments = []reviewComment{}
			if fbJSON, err := json.Marshal(fallback); err == nil {
				if fbErr := postPayload(fbJSON); fbErr == nil {
					postErr = nil
					commentCount = 0
					for _, c := range moved {
						fileComments = append(fileComments, fileComment{
							Path:        c.Path,
							Body:        fmt.Sprintf("Line %d: %s", c.Line, c.Body),
							SubjectType: "file",
						})
					}
				}
			}
		}
	}

	if postErr == nil {
		// The review is posted; file-level comments are best effort and
		// never fail the post. A finding that couldn't be posted stays in
		// the archived spool file with the rest of the review.
		postFileComments(ctx, rec, fm["reviewed_sha"], fileComments)
		if len(plan.unposted) > 0 {
			logging.Info("review findings not on the diff were not posted; kept in the archived spool",
				"repo", rec.Repo, "pr", rec.PR, "findings", strings.Join(plan.unposted, " | "))
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

// postFileComments posts each file-level comment against commitID (the
// spool's reviewed_sha, falling back to the record's last reviewed SHA and
// then the PR's current head). Failures are logged, never returned: the
// review itself is already posted, and the finding stays in the archived
// spool file.
func postFileComments(ctx context.Context, rec Record, commitID string, comments []fileComment) {
	if len(comments) == 0 {
		return
	}
	if commitID == "" {
		commitID = rec.LastReviewedSHA
	}
	if commitID == "" {
		sha, err := prHeadSHAFunc(rec.Repo, rec.PR)
		if err != nil || sha == "" {
			logging.Warn("file-level review comments not posted: no commit to attach them to",
				"repo", rec.Repo, "pr", rec.PR, "count", len(comments), "error", err)
			return
		}
		commitID = sha
	}
	for _, c := range comments {
		c.CommitID = commitID
		if err := postOneFileComment(ctx, rec, c); err != nil {
			logging.Warn("file-level review comment not posted", "repo", rec.Repo, "pr", rec.PR,
				"path", c.Path, "error", err)
		}
	}
}

func postOneFileComment(ctx context.Context, rec Record, c fileComment) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", fmt.Sprintf("pr-file-comment-%d-*.json", rec.PR))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return postFileCommentFunc(ctx, tmpPath, rec.Repo, rec.PR)
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
	"infra":  "infra-reviewer",
	"docs":   "docs-reviewer",
	// generic is pr_review.py's fallback for a language with no dedicated
	// reviewer. resolveLanguageReviewer never selects it from an unknown
	// language (that falls back to python, matching pr_review_finalize.py),
	// but a spool explicitly recorded as language: generic resolves here.
	"generic": "generic-reviewer",
}

// languagesWithoutCrossCutting mirrors pr_review.py's _LANG_CROSS_CUTTING for
// the languages that run no cross-cutting lenses: a docs-only diff has no code
// for the security/performance/testing lenses to review.
var languagesWithoutCrossCutting = map[string]bool{"docs": true}

// rereviewLenses returns the lens names (sorted, "language" first by
// alphabetical order as in pr_review.py) and their profiles for a rereview of
// a review recorded with the given language.
func rereviewLenses(language, languageReviewer string) ([]string, map[string]string) {
	profiles := map[string]string{"language": languageReviewer}
	if !languagesWithoutCrossCutting[strings.ToLower(strings.TrimSpace(language))] {
		for name, profile := range crossCuttingLenses {
			profiles[name] = profile
		}
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, profiles
}

// valkeyReviewers mirrors pr_review.py's _VALKEY_REVIEWER table: languages
// with a Valkey-specific reviewer variant.
var valkeyReviewers = map[string]string{
	"python": "valkey-python-reviewer",
	"go":     "valkey-go-reviewer",
}

// resolveLanguageReviewer mirrors pr_review.py's resolve_language_reviewer.
// language and valkey come from the spool front-matter, which pr_review.py
// writes with the lens the original review used. An unknown or missing
// language (older spools have none) falls back to python-reviewer, matching
// pr_review_finalize.py's --language default.
func resolveLanguageReviewer(language string, valkey bool) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if valkey {
		if reviewer, ok := valkeyReviewers[language]; ok {
			return reviewer
		}
	}
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

Produce the updated single markdown review document in your output format
(`+"`## Summary`, `## Findings` most-severe first, optional `## Reviewer notes`"+`).
Print the whole review as your reply. End with a final line exactly:
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
	res, err := kiroOneshotFunc(ctx, "review-consolidator", "", prompt)
	if err != nil {
		return "", fmt.Errorf("revise failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	newBody := extractReviewBody(res.Text)
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

	// Re-run the same language lens the original review used.
	valkey := strings.EqualFold(strings.TrimSpace(fields["valkey"]), "true")
	language := resolveLanguageReviewer(fields["language"], valkey)
	lensNames, lensProfiles := rereviewLenses(fields["language"], language)

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
			res, callErr := kiroOneshotFunc(fanCtx, profile, "", prompt)
			results[i] = lensResult{lensName: lensName, profile: profile, output: res.Text, err: callErr}
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
	res, err := kiroOneshotFunc(ctx, "review-consolidator", "", consolidatePrompt)
	if err != nil {
		return "", false, fmt.Errorf("rereview consolidation failed for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	newBody := extractReviewBody(res.Text)
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

Produce a markdown review document as the deliverable, in this shape:

# Consolidated Review
## Summary
<1-3 sentences: what the change does and the overall assessment. This is the
only prose posted to the PR, so no verdict rationale and nothing about what
you cleared, dropped or chose not to flag.>
## Findings
<surviving findings most-severe first, one per line as
`+"`file:line - severity - issue -> fix`"+`; omit the section if none>
## Reviewer notes
<optional, never posted: verdict rationale, verified non-issues, dropped
findings and why. Do not write finding-shaped lines here.>

End with a final line exactly:
`+"`VERDICT: <APPROVE|COMMENT|REQUEST_CHANGES>`", diffFile, digest.String())
}
