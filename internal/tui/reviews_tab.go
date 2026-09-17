package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// emptyTimestampPlaceholder is rendered in place of an empty LastReviewedAt.
const emptyTimestampPlaceholder = "—"

// Message strings shared by the styled View() and the plain-text
// CopyableContent() so the two can never silently diverge (edit once, both
// use it).
const (
	reviewsEmptyMessage = "No watched PRs. Run 'howmux review <PR-URL>' to enroll one."
	reviewsErrorFormat  = "Failed to read PR review state: %v"
)

// identityStyle is a no-op style function used by the plain-text
// (CopyableContent) rebuild so it can share the row/header formatters with the
// styled View() without applying any ANSI styling.
func identityStyle(s string) string { return s }

// ReviewsTab implements the Tab interface for displaying the current state of
// every PR tracked by the review state store (.howmux/reviews/). It is a
// read-only, on-demand view: View() calls store.List() fresh every time it is
// invoked — no caching, no background polling, no tea.Tick loop. This tab
// never calls Save or Remove on the store.
type ReviewsTab struct {
	id     string
	store  review.StoreInterface
	styles *Styles
	width  int
	height int
}

// NewReviewsTab creates a new reviews tab backed by the given
// review.StoreInterface. The interface (not a concrete *review.Store) is
// accepted so tests can inject a fake store.
func NewReviewsTab(id string, store review.StoreInterface, styles *Styles) *ReviewsTab {
	return &ReviewsTab{
		id:     id,
		store:  store,
		styles: styles,
	}
}

// ID returns the tab identifier
func (rt *ReviewsTab) ID() string {
	return rt.id
}

// Type returns the tab type
func (rt *ReviewsTab) Type() TabType {
	return TabTypeReviews
}

// Title returns the tab title
func (rt *ReviewsTab) Title() string {
	return "Reviews"
}

// IsClosable returns whether this tab can be closed
func (rt *ReviewsTab) IsClosable() bool {
	return false // There is exactly one reviews view per session, nothing to close back to.
}

// View returns the tab's rendered content.
//
// Cost note: View() is invoked by Bubble Tea on every render (keypress,
// resize, and any tick from other tabs re-renders the whole model), and each
// call reads the store fresh — os.ReadDir + os.ReadFile + JSON-unmarshal per
// tracked PR (#67's List), on the event-loop goroutine. This is a deliberate
// "no cache, no staleness window" tradeoff that is fine for a low-volume,
// local-FS tab: with a handful of PRs the per-frame I/O is negligible. It has
// a ceiling, though — with many tracked PRs or a slow/network FS it would add
// disk latency to every frame. If PR counts ever grow, cache the List() result
// and invalidate it on a poll tick (or on copy) to drop the per-frame I/O
// without reintroducing a real staleness window.
func (rt *ReviewsTab) View() string {
	records, err := rt.store.List()

	var content string
	switch {
	case err != nil:
		content = rt.renderError(err)
	case len(records) == 0:
		content = rt.renderEmpty()
	default:
		content = rt.renderTable(records)
	}

	return rt.padToHeight(content)
}

// padToHeight appends blank lines so the rendered content fills the tab's
// content area (rt.height). Non-main tabs are composed as `content + "\n" +
// footer` (see renderTabContentWithFooter): MainTab, LogTab and AgentTab all
// fill the viewport to its height so the footer pins to the bottom of the
// screen, but ReviewsTab builds a plain string, so without this pad its footer
// would float up directly under the last row. Only View() pads;
// CopyableContent() stays unpadded so copied text has no trailing blank lines.
func (rt *ReviewsTab) padToHeight(content string) string {
	if rt.height <= 0 {
		return content
	}
	lines := strings.Count(content, "\n") + 1
	if lines >= rt.height {
		return content
	}
	return content + strings.Repeat("\n", rt.height-lines)
}

// renderEmpty renders the placeholder shown when no PRs are currently
// tracked. This is a distinct, expected steady state — not an error.
func (rt *ReviewsTab) renderEmpty() string {
	if rt.styles != nil {
		return rt.styles.Prompt.Render(reviewsEmptyMessage)
	}
	return reviewsEmptyMessage
}

// renderError renders the message shown when store.List() fails (e.g. a
// transient I/O error reading the reviews directory).
func (rt *ReviewsTab) renderError(err error) string {
	msg := fmt.Sprintf(reviewsErrorFormat, err)
	if rt.styles != nil {
		return rt.styles.Error.Render(msg)
	}
	return msg
}

// reviewsColumnWidths returns the fixed column widths used by both the styled
// table and the plain-text CopyableContent rebuild, so the two stay aligned.
const (
	reviewsColRepo          = 30
	reviewsColPR            = 8
	reviewsColStatus        = 12
	reviewsColLastReviewed  = 19 // width of "2006-01-02 15:04:05"
	reviewsColSpoolPath     = 40
	reviewsColVerdict       = 16
	reviewsColDecisionState = 20
)

// reviewsHeader returns the plain (unstyled) header row. Shared by the styled
// View() (which styles it) and CopyableContent() (which does not).
func reviewsHeader() string {
	return fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %s",
		reviewsColRepo, "REPO",
		reviewsColPR, "PR",
		reviewsColStatus, "STATUS",
		reviewsColLastReviewed, "LAST REVIEWED",
		reviewsColSpoolPath, "SPOOL PATH",
		reviewsColVerdict, "VERDICT",
		"DECISION STATE")
}

// buildTable is the single source of truth for the table body. It writes the
// header (via headerStyle) and one row per record, styling only the STATUS
// column via statusStyle. records must already be sorted (via sortedRecords)
// by the caller, and spoolInfo[i] must correspond to records[i] — buildTable
// no longer sorts internally, since two independent call sites each calling
// sortedRecords could otherwise drift relative to a separately-computed
// spoolInfo slice. Passing identity functions produces the plain-text form
// used by CopyableContent(); passing the real style functions produces the
// styled View() form. This keeps the row/header layout and loop in one place
// so the styled and plain outputs cannot drift.
func buildTable(records []review.Record, spoolInfo []review.SpoolInfo, headerStyle func(string) string, statusStyle func(review.Status, string) string) string {
	var b strings.Builder
	b.WriteString(headerStyle(reviewsHeader()))

	for i, rec := range records {
		b.WriteString("\n")
		repoCol := fmt.Sprintf("%-*s", reviewsColRepo, rec.Repo)
		prCol := fmt.Sprintf("%-*s", reviewsColPR, fmt.Sprintf("#%d", rec.PR))
		statusCol := statusStyle(rec.Status, fmt.Sprintf("%-*s", reviewsColStatus, string(rec.Status)))
		lastReviewed := fmt.Sprintf("%-*s", reviewsColLastReviewed, formatLastReviewedAt(rec.LastReviewedAt))

		info := spoolInfo[i]

		spoolPathVal := emptyTimestampPlaceholder
		if rec.SpoolPath != "" {
			spoolPathVal = truncate(rec.SpoolPath, reviewsColSpoolPath)
		}
		spoolPathCol := fmt.Sprintf("%-*s", reviewsColSpoolPath, spoolPathVal)

		verdictVal := info.Verdict
		if verdictVal == "" {
			verdictVal = emptyTimestampPlaceholder
		}
		verdictCol := fmt.Sprintf("%-*s", reviewsColVerdict, verdictVal)

		decisionStateCol := info.DecisionState

		b.WriteString(fmt.Sprintf("%s %s %s %s %s %s %s", repoCol, prCol, statusCol, lastReviewed, spoolPathCol, verdictCol, decisionStateCol))
	}

	return b.String()
}

// renderTable renders the styled table: the header uses styles.Prompt and the
// STATUS column is colored by styleStatus. Sorts records once and resolves
// spool info against that sorted slice so index i stays aligned between the
// two slices passed into buildTable.
func (rt *ReviewsTab) renderTable(records []review.Record) string {
	sorted := sortedRecords(records)
	spoolInfo := rt.resolveSpoolInfo(sorted)

	headerStyle := identityStyle
	if rt.styles != nil {
		headerStyle = func(s string) string { return rt.styles.Prompt.Render(s) }
	}
	return buildTable(sorted, spoolInfo, headerStyle, rt.styleStatus)
}

// resolveSpoolInfo resolves review.SpoolInfo for each record in sorted, in
// order, so index i of the returned slice corresponds to sorted[i]. Calls
// userHomeDirFunc() once for the whole render rather than once per record. If
// userHomeDirFunc() errors, "" is used as homeDir for every spoolInfoForFunc
// call in this render — a home-dir lookup failure degrades spool resolution
// rather than failing the whole tab render (Record.SpoolPath is always
// already absolute per validateSpoolPath's contract in runner.go, so an empty
// homeDir does not prevent resolution).
//
// Cost note (extends the View() I/O note): this runs inside View() — invoked
// every render (keypress/resize/tick) — and adds, per record, one spool
// os.ReadFile, plus a second done/ read for any archived PR whose pending/
// path no longer exists (that miss-then-fallback doubles reads for posted
// PRs). Combined with the store.List() scan already in View(), a render is
// ~1 dir scan + N record reads + up to 2N spool reads. This is the deliberate
// no-cache/no-staleness tradeoff, fine for a low-volume local-FS tab; if PR
// counts grow, a per-tick cache (invalidated on the same cadence) would remove
// the per-frame I/O without a real staleness window.
func (rt *ReviewsTab) resolveSpoolInfo(sorted []review.Record) []review.SpoolInfo {
	homeDir, err := userHomeDirFunc()
	if err != nil {
		homeDir = ""
	}

	infos := make([]review.SpoolInfo, len(sorted))
	for i, rec := range sorted {
		infos[i] = spoolInfoForFunc(rec.SpoolPath, homeDir)
	}
	return infos
}

// styleStatus colors the STATUS column: StatusDone -> Success, StatusReviewing
// -> Warning, StatusWatching/StatusReviewed -> neutral (styles.Prompt), reusing
// existing Styles fields rather than inventing new theme colors.
func (rt *ReviewsTab) styleStatus(status review.Status, text string) string {
	if rt.styles == nil {
		return text
	}
	switch status {
	case review.StatusDone:
		return rt.styles.Success.Render(text)
	case review.StatusReviewing:
		return rt.styles.Warning.Render(text)
	case review.StatusWatching, review.StatusReviewed:
		return rt.styles.Prompt.Render(text)
	default:
		return text
	}
}

// formatLastReviewedAt renders the RFC3339 LastReviewedAt as a short,
// human-readable timestamp, "—" when empty, or the raw string if it fails to
// parse (defensive against stale/hand-edited record files).
func formatLastReviewedAt(raw string) string {
	if raw == "" {
		return emptyTimestampPlaceholder
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.Format("2006-01-02 15:04:05")
}

// sortedRecords returns a stable, deterministically-ordered copy of records
// (by Repo, then PR number) so repeated View() calls render rows in the same
// order regardless of the store's underlying (directory-listing-based) order.
func sortedRecords(records []review.Record) []review.Record {
	sorted := make([]review.Record, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Repo != sorted[j].Repo {
			return sorted[i].Repo < sorted[j].Repo
		}
		return sorted[i].PR < sorted[j].PR
	})
	return sorted
}

// Update handles messages for the reviews tab. There is no internal state to
// update in response to tea.Msg — resize is handled via Resize, and every
// View() call reads fresh data directly from the store, so no tea.Tick poll
// loop is needed.
func (rt *ReviewsTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	return rt, nil
}

// Resize updates the tab dimensions
func (rt *ReviewsTab) Resize(width, height int) {
	rt.width = width
	rt.height = height
}

// CopyableContent independently rebuilds the same rows as unstyled plain
// text, mirroring LogTab.CopyableContent()'s pattern of a parallel plain-text
// builder rather than stripping ANSI codes from View(). It shares the row/
// header formatters and message strings with View() (via buildTable and the
// reviews*Message constants) so the copied text can never drift from what is
// displayed; it just passes identity style functions so nothing is colored.
func (rt *ReviewsTab) CopyableContent() string {
	records, err := rt.store.List()
	if err != nil {
		return fmt.Sprintf(reviewsErrorFormat, err)
	}

	if len(records) == 0 {
		return reviewsEmptyMessage
	}

	sorted := sortedRecords(records)
	spoolInfo := rt.resolveSpoolInfo(sorted)

	plainStatus := func(_ review.Status, text string) string { return text }
	return buildTable(sorted, spoolInfo, identityStyle, plainStatus)
}

// CaptureFocusState returns the current focus state for the reviews tab. This
// tab has no internal focusable widget (no text input, no interactive
// selection), so it always uses footer input, matching MainTab.
func (rt *ReviewsTab) CaptureFocusState() FocusTarget {
	return FocusTargetFooter
}

// RestoreFocusState restores the focus state for the reviews tab. The reviews
// tab doesn't manage focus directly — handled by the parent model, matching
// MainTab.
func (rt *ReviewsTab) RestoreFocusState(target FocusTarget) tea.Cmd {
	return nil
}
