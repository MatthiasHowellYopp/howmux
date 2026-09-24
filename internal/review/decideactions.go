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
// repos/<repo>/pulls/<pr>/reviews --method POST --input <payloadFile>`,
// following tool-routing.md's documented rule for posting arrays/nested
// JSON bodies via --input rather than --field. Returns gh's stdout (the
// JSON response) on success, or a wrapped error including gh's stderr on
// failure — mirroring fetchDiffFunc's exec.ExitError-unwrapping pattern.
func defaultPostReviewCommand(ctx context.Context, payloadFile string, repo string, pr int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, pr),
		"--method", "POST",
		"--input", payloadFile,
	)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api pulls/reviews failed (exit %d): %s: %w",
				exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return nil, fmt.Errorf("gh api pulls/reviews failed: %w", err)
	}
	return output, nil
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

// buildReviewPayload parses findings out of body (reusing findingLineRe,
// the same line-matching regex countFindings uses) into one inline
// GitHub review comment per finding, maps verdict to the GitHub review
// "event" per verdictEvent, and marshals the whole thing into the JSON
// shape gh api pulls/reviews expects. Returns the marshaled payload, the
// resolved event, and the number of comments produced.
func buildReviewPayload(repo string, pr int, verdict string, body string) (payloadJSON []byte, event string, commentCount int, err error) {
	event = verdictEvent(verdict)

	matches := findingLineRe.FindAllStringSubmatch(body, -1)
	comments := make([]reviewComment, 0, len(matches))
	for _, m := range matches {
		file := m[1]
		line, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			continue
		}
		severity := strings.ToLower(strings.TrimSpace(m[3]))
		issue := strings.TrimSpace(m[4])
		fix := strings.TrimSpace(m[5])
		comments = append(comments, reviewComment{
			Path: file,
			Line: line,
			Body: fmt.Sprintf("%s: %s → %s", severity, issue, fix),
		})
	}

	payload := reviewPayload{
		Event:    event,
		Body:     body,
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

	payloadJSON, _, commentCount, err := buildReviewPayload(rec.Repo, rec.PR, info.Verdict, humanized)
	if err != nil {
		return 0, fmt.Errorf("failed to build review payload for %s#%d: %w", rec.Repo, rec.PR, err)
	}

	tmp, err := os.CreateTemp("", fmt.Sprintf("pr-review-payload-%d-*.json", rec.PR))
	if err != nil {
		return 0, fmt.Errorf("failed to create temp payload file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(payloadJSON); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("failed to write temp payload file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("failed to close temp payload file: %w", err)
	}

	if _, err := postReviewCommandFunc(ctx, tmpPath, rec.Repo, rec.PR); err != nil {
		// Deliberately do NOT archive: decision: post is already recorded
		// (crash-recovery contract) — the file stays in pending/ so a retry
		// of PostReview can pick it up again.
		return 0, fmt.Errorf("failed to post review for %s#%d: %w", rec.Repo, rec.PR, err)
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
