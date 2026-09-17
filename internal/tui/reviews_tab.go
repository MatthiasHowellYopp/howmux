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

// View returns the tab's rendered content
func (rt *ReviewsTab) View() string {
	records, err := rt.store.List()
	if err != nil {
		return rt.renderError(err)
	}

	if len(records) == 0 {
		return rt.renderEmpty()
	}

	return rt.renderTable(records)
}

// renderEmpty renders the placeholder shown when no PRs are currently
// tracked. This is a distinct, expected steady state — not an error.
func (rt *ReviewsTab) renderEmpty() string {
	msg := "No watched PRs. Run 'howmux review <PR-URL>' to enroll one."
	if rt.styles != nil {
		return rt.styles.Prompt.Render(msg)
	}
	return msg
}

// renderError renders the message shown when store.List() fails (e.g. a
// transient I/O error reading the reviews directory).
func (rt *ReviewsTab) renderError(err error) string {
	msg := fmt.Sprintf("Failed to read PR review state: %v", err)
	if rt.styles != nil {
		return rt.styles.Error.Render(msg)
	}
	return msg
}

// reviewsColumnWidths returns the fixed column widths used by both the styled
// table and the plain-text CopyableContent rebuild, so the two stay aligned.
const (
	reviewsColRepo   = 30
	reviewsColPR     = 8
	reviewsColStatus = 12
)

// renderTable renders one row per record, sorted by repo then PR number for
// stable, deterministic output across renders.
func (rt *ReviewsTab) renderTable(records []review.Record) string {
	sorted := sortedRecords(records)

	var b strings.Builder
	header := fmt.Sprintf("%-*s %-*s %-*s %s",
		reviewsColRepo, "REPO",
		reviewsColPR, "PR",
		reviewsColStatus, "STATUS",
		"LAST REVIEWED")
	if rt.styles != nil {
		b.WriteString(rt.styles.Prompt.Render(header))
	} else {
		b.WriteString(header)
	}

	for _, rec := range sorted {
		b.WriteString("\n")
		b.WriteString(rt.renderRow(rec))
	}

	return b.String()
}

// renderRow renders a single styled row.
func (rt *ReviewsTab) renderRow(rec review.Record) string {
	prCol := fmt.Sprintf("%-*s", reviewsColPR, fmt.Sprintf("#%d", rec.PR))
	statusText := fmt.Sprintf("%-*s", reviewsColStatus, string(rec.Status))
	statusCol := rt.styleStatus(rec.Status, statusText)
	lastReviewed := formatLastReviewedAt(rec.LastReviewedAt)

	repoCol := fmt.Sprintf("%-*s", reviewsColRepo, rec.Repo)
	return fmt.Sprintf("%s %s %s %s", repoCol, prCol, statusCol, lastReviewed)
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
// builder rather than stripping ANSI codes from View().
func (rt *ReviewsTab) CopyableContent() string {
	records, err := rt.store.List()
	if err != nil {
		return fmt.Sprintf("Failed to read PR review state: %v", err)
	}

	if len(records) == 0 {
		return "No watched PRs. Run 'howmux review <PR-URL>' to enroll one."
	}

	sorted := sortedRecords(records)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-*s %-*s %-*s %s",
		reviewsColRepo, "REPO",
		reviewsColPR, "PR",
		reviewsColStatus, "STATUS",
		"LAST REVIEWED"))

	for _, rec := range sorted {
		b.WriteString("\n")
		prCol := fmt.Sprintf("%-*s", reviewsColPR, fmt.Sprintf("#%d", rec.PR))
		statusCol := fmt.Sprintf("%-*s", reviewsColStatus, string(rec.Status))
		repoCol := fmt.Sprintf("%-*s", reviewsColRepo, rec.Repo)
		lastReviewed := formatLastReviewedAt(rec.LastReviewedAt)
		b.WriteString(fmt.Sprintf("%s %s %s %s", repoCol, prCol, statusCol, lastReviewed))
	}

	return b.String()
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
