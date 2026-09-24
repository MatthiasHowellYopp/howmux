package tui

// decideactions.go implements the TUI-layer dispatch/launch machinery for
// the decide command's four actions (post/revise/rereview/discard) — the
// direct, per-action replacement for the deleted finalize.go's batch
// dry-run/live-run model (issue #109). Each launchDecideX function is a
// thin tea.Cmd-returning wrapper around the corresponding internal/review
// action function (decideactions.go in that package), mirroring how
// handleReview is a thin wrapper around review.RunReview. Only "post" has
// an inline confirm gate (decidePostConfirmState, wired in tui.go); the
// other three launch immediately, with no confirm step at all.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// Decide-action subprocess timeouts. Each launch wrapper derives a
// context.WithTimeout from these rather than a bare context.Background(),
// so a hung `gh api` (post) or a stalled `kiro-cli` agent call
// (revise/rereview) cannot wedge the tea.Cmd goroutine and its child
// process forever — restoring the cancellability the deleted finalize.go
// had via finalizeCancel. Package-level vars (not consts) so tests can
// shrink them if they ever exercise the timeout path. The rereview budget
// is the largest because RereviewReview fans out up to five kiro-cli
// agent invocations plus a consolidation pass under one context.
var (
	decidePostTimeout     = 2 * time.Minute
	decideReviseTimeout   = 5 * time.Minute
	decideRereviewTimeout = 10 * time.Minute
)

// decidePostConfirmState models the (idle -> awaiting-confirmation -> idle)
// lifecycle of decide post's inline y/N gate — see tui.go's model struct
// field doc comment and the confirm-gate keypress-interception block in
// model.Update for the full contract. Unlike finalize.go's old state enum
// (which distinguished dry-run-running from live-running), there is no
// separate "running" state to distinguish from "awaiting confirmation":
// decidePostConfirmAwaiting IS the only non-idle state, since posting
// itself is fire-and-forget from the confirm gate's perspective — the
// actual gh/kiro-cli work happens inside the tea.Cmd the "y" branch
// returns, tracked implicitly by whether that tea.Cmd's terminal message
// (decidePostCompleteMsg/decidePostErrorMsg) has arrived yet.
type decidePostConfirmState int

const (
	decidePostConfirmIdle decidePostConfirmState = iota
	decidePostConfirmAwaiting
)

// --- terminal messages ------------------------------------------------------
//
// One complete/error pair per action, each a thin wrapper around the
// corresponding internal/review function's return values. discard has no
// subprocess call (see DiscardReview), but still reports through a message
// pair for structural uniformity with the other three — dispatchDecideAction
// always returns (model, tea.Cmd), and every launchDecideX (including
// discard's) returns a tea.Cmd, even if that Cmd's closure runs
// synchronously and returns its message immediately.

type decidePostCompleteMsg struct {
	rec            review.Record
	commentsPosted int
}

type decidePostErrorMsg struct {
	rec review.Record
	err error
}

type decideReviseCompleteMsg struct {
	rec        review.Record
	newVerdict string
}

type decideReviseErrorMsg struct {
	rec review.Record
	err error
}

type decideRereviewCompleteMsg struct {
	rec              review.Record
	newVerdict       string
	degradedToRevise bool
}

type decideRereviewErrorMsg struct {
	rec review.Record
	err error
}

type decideDiscardCompleteMsg struct {
	rec review.Record
}

type decideDiscardErrorMsg struct {
	rec review.Record
	err error
}

// --- dispatch ----------------------------------------------------------------

// dispatchDecideAction is the single shared branch both handleDecide (REPL,
// commands.go) and model.Update's decideRequestMsg case (Reviews-tab
// p/r/R/d keys, tui.go) call, so key-driven and command-driven decisions
// launch identically — the same role applyDecision played before issue
// #109, now dispatching to a launch instead of a write. decision is
// assumed already validated by the caller (both callers validate against
// the exact "post"/"revise"/"rereview"/"discard" vocabulary before
// reaching here).
func (m model) dispatchDecideAction(rec review.Record, decision string) (model, tea.Cmd) {
	switch decision {
	case "post":
		return m.startDecidePost(rec)
	case "revise":
		return m.openReviseCompose(rec, "revise")
	case "rereview":
		return m.openReviseCompose(rec, "rereview")
	case "discard":
		return m.launchDecideDiscard(rec)
	default:
		// Unreachable given both callers' pre-validation, but degrade to a
		// visible error rather than silently doing nothing if a future
		// caller ever skips validation.
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid decision: %s (must be post, revise, rereview, or discard)", decision)))
		return m, nil
	}
}

// startDecidePost does NOT launch anything — it reads the review body to
// count findings, sets the confirm-gate state fields, and appends the
// confirm-prompt activity line. Nothing runs until the user's next
// keypress reaches the confirm gate in model.Update (tui.go), which either
// launches launchDecidePostCmd ("y") or cancels with no side effect at all
// ("n"/"esc") — no decision: front-matter write happens on this path,
// matching the issue's explicit requirement that declining leaves the file
// completely untouched in pending/.
func (m model) startDecidePost(rec review.Record) (model, tea.Cmd) {
	homeDir, err := userHomeDirFunc()
	if err != nil {
		homeDir = ""
	}
	body, info := review.ReadSpoolBody(rec.SpoolPath, homeDir)

	count, breakdown := 0, ""
	if info.Found {
		count, breakdown = countFindingsFunc(body)
	}

	m.decidePostConfirmState = decidePostConfirmAwaiting
	m.decidePostPending = rec
	m.decidePostFindingCount = count
	m.decidePostVerdict = info.Verdict

	var prompt string
	switch {
	case count == 0:
		// A review with no parsed finding lines is still postable (e.g. an
		// APPROVE with only summary prose + verdict, no inline comments).
		// Say "Post review" rather than the misleading "Post 0 findings".
		prompt = fmt.Sprintf("Post review to %s#%d? [y/N]", rec.Repo, rec.PR)
	case breakdown != "":
		prompt = fmt.Sprintf("Post %d findings (%s) to %s#%d? [y/N]", count, breakdown, rec.Repo, rec.PR)
	default:
		prompt = fmt.Sprintf("Post %d findings to %s#%d? [y/N]", count, rec.Repo, rec.PR)
	}
	m = m.appendActivity(m.styles.Activity.Render(prompt))

	return m, nil
}

// openReviseCompose does NOT launch anything — it opens the in-app
// multi-line notes composer for a "revise" or "rereview" decision,
// pre-populated (best-effort) with any persisted decision_notes so the
// manual-flow notes carry into the composer as a starting point. Nothing
// runs until the user submits the composer (Ctrl+D) — handled by the
// reviseComposeActive keypress-interception block in model.Update (tui.go),
// which captures the typed multi-line notes and dispatches to
// launchDecideRevise/launchDecideRereview with them threaded in-memory
// (never written to the front-matter). Esc cancels with no action taken.
// This is the revise/rereview analog of startDecidePost's confirm gate:
// both defer the actual launch to a subsequent keypress in model.Update.
func (m model) openReviseCompose(rec review.Record, action string) (model, tea.Cmd) {
	homeDir, err := userHomeDirFunc()
	if err != nil {
		homeDir = ""
	}
	currentNotes := review.CurrentDecisionNotes(rec.SpoolPath, homeDir)

	// Defensive lazy-init: the production model always constructs the
	// composer (see newModel), but a directly-struct-literal model (some
	// tests) may not — create one on demand rather than nil-panic.
	if m.notesComposer == nil {
		m.notesComposer = NewNotesComposer()
	}

	m.reviseComposeActive = true
	m.reviseComposeTarget = rec
	m.reviseComposeAction = action
	m.notesComposer.SetValue(currentNotes)
	m.sizeNotesComposer()

	verb := "Revise"
	if action == "rereview" {
		verb = "Rereview"
	}
	if m.footerManager != nil {
		m.footerManager.SetTransientMessage(fmt.Sprintf("%s notes for %s #%d (Ctrl+D run · Esc cancel)", verb, rec.Repo, rec.PR))
	}

	return m, m.notesComposer.Focus()
}

// countFindingsFunc wraps review's package-private countFindings via the
// only exported surface available: PostReview builds the payload
// internally, so the TUI layer cannot call the private function directly.
// Since countFindings is unexported in internal/review, this seam re-parses
// findings with the same line shape locally for the confirm prompt's
// display-only count — a duplicate of the display heuristic only, never
// used for anything load-bearing (PostReview posts the whole body
// regardless of what this counts). Package-level var so tests can
// substitute a fake without needing a real spool body shaped like a review.
var countFindingsFunc = countFindingsForPrompt

// --- launch wrappers (thin tea.Cmd-returning wrappers around internal/review) ---

// launchDecidePostCmd builds the tea.Cmd the confirm gate's "y" branch
// returns: it calls review.PostReview off the Update goroutine and reports
// back via decidePostCompleteMsg/decidePostErrorMsg. verdict is currently
// unused by PostReview's own signature (it re-reads the verdict itself from
// the spool file) but is threaded through for symmetry with the design
// spec's documented model fields; kept as a parameter so a future caller
// that already has the verdict in hand need not re-read it.
func launchDecidePostCmd(rec review.Record, _ string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), decidePostTimeout)
		defer cancel()
		commentsPosted, err := postReviewFunc(ctx, rec)
		if err != nil {
			return decidePostErrorMsg{rec: rec, err: err}
		}
		return decidePostCompleteMsg{rec: rec, commentsPosted: commentsPosted}
	}
}

// launchDecideRevise builds the tea.Cmd for the "revise" action: an
// immediate consolidator re-run via review.ReviseReview. inlineNotes are
// the multi-line instructions the user typed into the in-app composer
// (empty if they submitted the composer without typing anything, in which
// case ReviseReview falls back to the persisted decision_notes). They are
// passed straight to the seam and never persisted to the spool.
func (m model) launchDecideRevise(rec review.Record, inlineNotes string) (model, tea.Cmd) {
	m = m.appendActivity(m.styles.Activity.Render(fmt.Sprintf("Revising review for %s#%d...", rec.Repo, rec.PR)))
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), decideReviseTimeout)
		defer cancel()
		newVerdict, err := reviseReviewFunc(ctx, rec, inlineNotes)
		if err != nil {
			return decideReviseErrorMsg{rec: rec, err: err}
		}
		return decideReviseCompleteMsg{rec: rec, newVerdict: newVerdict}
	}
}

// launchDecideRereview builds the tea.Cmd for the "rereview" action: an
// immediate multi-lens fan-out (or a degrade to revise behavior, if the
// diff file is unavailable) via review.RereviewReview. inlineNotes are the
// multi-line instructions the user typed into the in-app composer (empty if
// none), threaded to the seam identically to launchDecideRevise.
func (m model) launchDecideRereview(rec review.Record, inlineNotes string) (model, tea.Cmd) {
	m = m.appendActivity(m.styles.Activity.Render(fmt.Sprintf("Re-reviewing %s#%d...", rec.Repo, rec.PR)))
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), decideRereviewTimeout)
		defer cancel()
		newVerdict, degraded, err := rereviewReviewFunc(ctx, rec, inlineNotes)
		if err != nil {
			return decideRereviewErrorMsg{rec: rec, err: err}
		}
		return decideRereviewCompleteMsg{rec: rec, newVerdict: newVerdict, degradedToRevise: degraded}
	}
}

// launchDecideDiscard builds the tea.Cmd for the "discard" action: an
// immediate, no-confirm archive to done/ via review.DiscardReview. No
// subprocess call is made (see DiscardReview's doc comment), but this
// still returns a tea.Cmd (rather than a direct (model, nil)) for
// structural uniformity with the other three decide actions.
func (m model) launchDecideDiscard(rec review.Record) (model, tea.Cmd) {
	return m, func() tea.Msg {
		if err := discardReviewFunc(rec); err != nil {
			return decideDiscardErrorMsg{rec: rec, err: err}
		}
		return decideDiscardCompleteMsg{rec: rec}
	}
}

// --- internal/review call seams (package-level vars so tests substitute fakes) ---

// postReviewFunc wraps review.PostReview for testability, mirroring
// runReviewFunc's/ensureCheckoutFunc's existing seam pattern in commands.go.
var postReviewFunc = review.PostReview

// reviseReviewFunc wraps review.ReviseReview for testability. Its
// inlineNotes parameter carries the multi-line notes typed into the in-app
// composer (empty when the user submitted without typing anything).
var reviseReviewFunc = review.ReviseReview

// rereviewReviewFunc wraps review.RereviewReview for testability. Its
// inlineNotes parameter carries the multi-line notes typed into the in-app
// composer (empty when the user submitted without typing anything).
var rereviewReviewFunc = review.RereviewReview

// discardReviewFunc wraps review.DiscardReview for testability.
var discardReviewFunc = review.DiscardReview

// findingLinePromptRe mirrors internal/review's private findingLineRe
// exactly (same "file:line - severity - issue -> fix" shape, hyphen or
// em-dash separators) — duplicated here only because that regex is
// unexported in internal/review and this is a display-only heuristic for
// the confirm prompt's finding count/breakdown text, never load-bearing
// for what PostReview actually posts.
var findingLinePromptRe = regexp.MustCompile(`(?m)^\s*(\S+):(\d+)\s*[-—]\s*(\w+)\s*[-—]\s*(.+?)\s*(?:->|→)\s*(.+?)\s*$`)

// countFindingsForPrompt is the default implementation behind
// countFindingsFunc: a local, display-only port of internal/review's
// private countFindings heuristic (same line shape: "file:line - severity -
// issue -> fix", hyphen or em-dash separators), used only to size the
// confirm prompt's "Post N findings (...)" text. Never used to decide what
// PostReview actually posts — PostReview posts (and internally re-parses)
// the whole body itself, independent of this function's result.
func countFindingsForPrompt(body string) (total int, breakdown string) {
	matches := findingLinePromptRe.FindAllStringSubmatch(body, -1)
	total = len(matches)

	counts := map[string]int{}
	order := []string{}
	for _, match := range matches {
		sev := strings.ToLower(strings.TrimSpace(match[3]))
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
