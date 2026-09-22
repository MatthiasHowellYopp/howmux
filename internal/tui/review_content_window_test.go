package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// This file covers the Enter-to-open / ESC-to-close review content window
// feature (issue #84) at the model.Update integration level, exercising the
// openReviewContentMsg round trip end to end: ReviewsTab.Update emits the
// message via a tea.Cmd, model.Update's "case openReviewContentMsg:" arm
// reads the spool body and adds/switches to the new tab, and the top-level
// ESC handling closes it and restores the Reviews tab.
//
// Kept separate from enter_key_test.go (which is scoped to the pre-existing
// "footer focus priority" behavior) per the design spec's guidance to avoid
// growing that file past ~150 added lines for an unrelated feature.

// writeSpoolFixture writes a minimal spool file (front-matter + body) under
// t.TempDir() and returns its path, for tests that need a real, readable
// spool file for ReadSpoolBody to resolve.
func writeSpoolFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create spool fixture dir: %v", err)
	}
	path := filepath.Join(dir, name)
	content := "---\nverdict: APPROVE\ndecision:\n---\n\n" + body
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write spool fixture: %v", err)
	}
	return path
}

// selectFirstRowAndPressEnter seeds the Reviews tab's render order (so a
// selection exists), then sends the Enter key through the top-level
// model.Update with the footer unfocused (mirroring
// TestEnterKeyCommandExecutionInAllTabs's footer-unfocused row). Enter on
// the Reviews tab returns a tea.Cmd (the openReviewContentMsg round trip
// described in the design spec) rather than mutating the model directly, so
// this helper executes that Cmd and feeds the resulting message back
// through model.Update itself — mirroring what the real Bubble Tea runtime
// loop does — and returns the fully updated model.
func selectFirstRowAndPressEnter(t *testing.T, m model) model {
	t.Helper()
	m.input.SetFocus(false)

	// Render once via the tab manager's active tab to seed ReviewsTab's
	// internal lastOrder/selectedKey (View() is what performs the
	// selection reconciliation), then press Enter.
	_ = m.tabManager.GetActiveTab().View()

	updatedModel, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := updatedModel.(model)
	if cmd == nil {
		return updated
	}

	msg := cmd()
	if msg == nil {
		return updated
	}
	updatedModel, _ = updated.Update(msg)
	return updatedModel.(model)
}

// TestEnterOnReviewsTabRowOpensReviewContentTab verifies that pressing Enter
// on a selected Reviews tab row (footer unfocused) opens a new
// ReviewContentTab and switches to it.
func TestEnterOnReviewsTabRowOpensReviewContentTab(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)

	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent, got %v", activeTab)
	}
	wantTitle := "Review: owner/repo #123"
	if activeTab.Title() != wantTitle {
		t.Errorf("Title() = %q, want %q", activeTab.Title(), wantTitle)
	}
	if !strings.Contains(activeTab.CopyableContent(), "# Review body") {
		t.Errorf("CopyableContent() = %q, want it to contain the spool body", activeTab.CopyableContent())
	}
}

// TestEnterOnReviewsTabWithMissingSpoolFileOpensErrorWindow verifies that
// when the selected record's SpoolPath points at a nonexistent file, the
// opened tab renders the file-error message (AC6) rather than panicking or
// showing an empty window.
func TestEnterOnReviewsTabWithMissingSpoolFileOpensErrorWindow(t *testing.T) {
	tmp := t.TempDir()
	missingPath := filepath.Join(tmp, "pending", "does-not-exist.md")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 456, SpoolPath: missingPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)

	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent, got %v", activeTab)
	}
	if !strings.Contains(activeTab.View(), "Could not read review content") {
		t.Errorf("View() = %q, want it to contain the file-error message", activeTab.View())
	}
	if !strings.Contains(activeTab.CopyableContent(), "Could not read review content") {
		t.Errorf("CopyableContent() = %q, want it to contain the file-error message", activeTab.CopyableContent())
	}
}

// TestEscOnReviewContentTabClosesAndReturnsToReviewsTab verifies ESC closes
// the review content window and returns focus to the Reviews tab, actually
// removing the content tab from the TabManager (not merely deactivating
// it).
func TestEscOnReviewContentTabClosesAndReturnsToReviewsTab(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)
	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent before ESC, got %v", activeTab)
	}
	contentTabID := activeTab.ID()

	updatedModel, _ := updated.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	afterEsc := updatedModel.(model)

	afterActiveTab := afterEsc.tabManager.GetActiveTab()
	if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
		t.Fatalf("expected active tab to be TabTypeReviews after ESC, got %v", afterActiveTab)
	}
	if idx := afterEsc.tabManager.FindTabByID(contentTabID); idx >= 0 {
		t.Errorf("expected review content tab %q to be removed from TabManager after ESC, found at index %d", contentTabID, idx)
	}
}

// TestEnterOnReviewsTabTwiceForSamePRReusesExistingWindow verifies that
// pressing Enter twice for the same selected row (without closing in
// between) reuses the already-open window instead of stacking a duplicate
// ReviewContentTab.
func TestEnterOnReviewsTabTwiceForSamePRReusesExistingWindow(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)

	// Switch back to the Reviews tab (as ESC would, but without removing the
	// content tab) and press Enter again on the same row.
	reviewsIdx := updated.findReviewsTabIndex()
	if reviewsIdx < 0 {
		t.Fatal("expected to find the Reviews tab")
	}
	var switchCmd tea.Cmd
	updated, switchCmd = updated.switchActiveTab(reviewsIdx)
	_ = switchCmd

	updated = selectFirstRowAndPressEnter(t, updated)

	count := 0
	for _, tab := range updated.tabManager.GetTabs() {
		if tab.Type() == TabTypeReviewContent {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 ReviewContentTab after opening the same PR twice, got %d", count)
	}

	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Errorf("expected active tab to be the reused TabTypeReviewContent, got %v", activeTab)
	}
}

// TestMouseClickCloseButtonOnReviewContentTabClosesAndReturnsToReviewsTab
// verifies that clicking the × close button on a Review content tab's
// header (the mouse-click path, tea.MouseClickMsg) closes the tab and
// returns focus to the Reviews tab, mirroring
// TestEscOnReviewContentTabClosesAndReturnsToReviewsTab. Before the fix for
// issue #106, the mouse-click path relied entirely on TabManager.CloseTab's
// generic index-clamping and had no equivalent of ESC's explicit
// findReviewsTabIndex() + switchActiveTab() redirect.
func TestMouseClickCloseButtonOnReviewContentTabClosesAndReturnsToReviewsTab(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)
	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent before click, got %v", activeTab)
	}
	contentTabID := activeTab.ID()

	tabs := updated.tabManager.GetTabs()
	contentIdx := updated.tabManager.FindTabByID(contentTabID)
	if contentIdx < 0 {
		t.Fatalf("expected to find review content tab in TabManager")
	}
	clickPos := tabClickPos(tabs, contentIdx, true)

	updatedModel, _ := updated.Update(tea.MouseClickMsg{X: clickPos, Y: 0})
	afterClick := updatedModel.(model)

	afterActiveTab := afterClick.tabManager.GetActiveTab()
	if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
		t.Fatalf("expected active tab to be TabTypeReviews after clicking close, got %v", afterActiveTab)
	}
	if idx := afterClick.tabManager.FindTabByID(contentTabID); idx >= 0 {
		t.Errorf("expected review content tab %q to be removed from TabManager after click-close, found at index %d", contentTabID, idx)
	}
}

// TestMouseClickCloseButtonOnReviewContentTabWithLaterTabReturnsToReviewsTab
// is the regression case that actually exposes the issue #106 bug: with a
// second closable tab (a LogTab) open AFTER the review content tab,
// click-closing the review content tab must still land on Reviews, not on
// the later tab that slides into its old slot. Before the fix,
// TabManager.CloseTab's generic clamping ("shift left by one if the active
// index was after the closed one") left the active tab on whatever tab
// took the closed tab's place — the LogTab in this case — instead of
// Reviews.
func TestMouseClickCloseButtonOnReviewContentTabWithLaterTabReturnsToReviewsTab(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	updated := selectFirstRowAndPressEnter(t, m)
	activeTab := updated.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent before click, got %v", activeTab)
	}
	contentTabID := activeTab.ID()

	// Add a second closable tab AFTER the review content tab.
	logTab := NewLogTab("log-1", "info", 100, updated.styles)
	updated.tabManager.AddTab(logTab)

	// Switch back to the review content tab so it's the one we click-close.
	contentIdx := updated.tabManager.FindTabByID(contentTabID)
	if contentIdx < 0 {
		t.Fatalf("expected to find review content tab in TabManager")
	}
	var switchCmd tea.Cmd
	updated, switchCmd = updated.switchActiveTab(contentIdx)
	_ = switchCmd

	tabs := updated.tabManager.GetTabs()
	clickPos := tabClickPos(tabs, contentIdx, true)

	updatedModel, _ := updated.Update(tea.MouseClickMsg{X: clickPos, Y: 0})
	afterClick := updatedModel.(model)

	afterActiveTab := afterClick.tabManager.GetActiveTab()
	if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
		t.Fatalf("expected active tab to be TabTypeReviews after click-closing with a later tab present, got %v", afterActiveTab)
	}
	if idx := afterClick.tabManager.FindTabByID(contentTabID); idx >= 0 {
		t.Errorf("expected review content tab %q to be removed from TabManager after click-close, found at index %d", contentTabID, idx)
	}
}

// TestCtrlWOnReviewContentTabClosesAndReturnsToReviewsTab mirrors the two
// mouse-click shapes above (lone review content tab, and with a later
// closable tab present) but exercises the "ctrl+w" key path instead of a
// mouse click.
func TestCtrlWOnReviewContentTabClosesAndReturnsToReviewsTab(t *testing.T) {
	t.Run("lone review content tab", func(t *testing.T) {
		tmp := t.TempDir()
		spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

		m := createTestModelWithReviewsTab(t, []review.Record{
			{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
		})

		updated := selectFirstRowAndPressEnter(t, m)
		activeTab := updated.tabManager.GetActiveTab()
		if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
			t.Fatalf("expected active tab to be TabTypeReviewContent before ctrl+w, got %v", activeTab)
		}
		contentTabID := activeTab.ID()

		updatedModel, _ := updated.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModCtrl}))
		afterCtrlW := updatedModel.(model)

		afterActiveTab := afterCtrlW.tabManager.GetActiveTab()
		if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
			t.Fatalf("expected active tab to be TabTypeReviews after ctrl+w, got %v", afterActiveTab)
		}
		if idx := afterCtrlW.tabManager.FindTabByID(contentTabID); idx >= 0 {
			t.Errorf("expected review content tab %q to be removed from TabManager after ctrl+w, found at index %d", contentTabID, idx)
		}
	})

	t.Run("with later closable tab present", func(t *testing.T) {
		tmp := t.TempDir()
		spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

		m := createTestModelWithReviewsTab(t, []review.Record{
			{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
		})

		updated := selectFirstRowAndPressEnter(t, m)
		activeTab := updated.tabManager.GetActiveTab()
		if activeTab == nil || activeTab.Type() != TabTypeReviewContent {
			t.Fatalf("expected active tab to be TabTypeReviewContent before ctrl+w, got %v", activeTab)
		}
		contentTabID := activeTab.ID()

		logTab := NewLogTab("log-1", "info", 100, updated.styles)
		updated.tabManager.AddTab(logTab)

		contentIdx := updated.tabManager.FindTabByID(contentTabID)
		if contentIdx < 0 {
			t.Fatalf("expected to find review content tab in TabManager")
		}
		var switchCmd tea.Cmd
		updated, switchCmd = updated.switchActiveTab(contentIdx)
		_ = switchCmd

		updatedModel, _ := updated.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModCtrl}))
		afterCtrlW := updatedModel.(model)

		afterActiveTab := afterCtrlW.tabManager.GetActiveTab()
		if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
			t.Fatalf("expected active tab to be TabTypeReviews after ctrl+w with a later tab present, got %v", afterActiveTab)
		}
		if idx := afterCtrlW.tabManager.FindTabByID(contentTabID); idx >= 0 {
			t.Errorf("expected review content tab %q to be removed from TabManager after ctrl+w, found at index %d", contentTabID, idx)
		}
	})
}

// TestMouseClickCloseButtonOnNonActiveReviewContentTabReturnsToReviewsTab
// covers the case the reviewContentTabIDs set-difference fix explicitly
// handles but the other tests do not: click-closing a review content tab
// that is NOT the currently active tab. A second review content tab is
// added and left active; clicking the × of the first (non-active) review
// content tab must still remove it and redirect to Reviews, because the
// count-drop detection keys on the review-content-tab count falling, not on
// which tab was active.
func TestMouseClickCloseButtonOnNonActiveReviewContentTabReturnsToReviewsTab(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	// First review content tab (via the real Enter round trip).
	updated := selectFirstRowAndPressEnter(t, m)
	firstTab := updated.tabManager.GetActiveTab()
	if firstTab == nil || firstTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent, got %v", firstTab)
	}
	firstTabID := firstTab.ID()

	// Add a SECOND review content tab and make it the active one, so the
	// first review content tab is present but not active when we close it.
	secondTab := NewReviewContentTab("review-content-2", "Review: owner/repo #456", "# Body 2\n", true, updated.styles)
	updated.tabManager.AddTab(secondTab)
	secondIdx := updated.tabManager.FindTabByID(secondTab.ID())
	if secondIdx < 0 {
		t.Fatalf("expected to find the second review content tab")
	}
	var switchCmd tea.Cmd
	updated, switchCmd = updated.switchActiveTab(secondIdx)
	_ = switchCmd

	// Click the × of the FIRST (non-active) review content tab.
	tabs := updated.tabManager.GetTabs()
	firstIdx := updated.tabManager.FindTabByID(firstTabID)
	if firstIdx < 0 {
		t.Fatalf("expected to find the first review content tab")
	}
	clickPos := tabClickPos(tabs, firstIdx, true)

	updatedModel, _ := updated.Update(tea.MouseClickMsg{X: clickPos, Y: 0})
	afterClick := updatedModel.(model)

	if idx := afterClick.tabManager.FindTabByID(firstTabID); idx >= 0 {
		t.Errorf("expected non-active review content tab %q to be removed after click-close, found at index %d", firstTabID, idx)
	}
	afterActiveTab := afterClick.tabManager.GetActiveTab()
	if afterActiveTab == nil || afterActiveTab.Type() != TabTypeReviews {
		t.Fatalf("expected active tab to be TabTypeReviews after click-closing a non-active review content tab, got %v", afterActiveTab)
	}
}

// TestMouseClickCloseButtonOnLogTabDoesNotRedirectToReviews is the negative
// guard: the redirect-to-Reviews behavior must fire ONLY when a review
// content tab is closed. Closing a non-review closable tab (a Log tab) while
// a review content tab exists elsewhere must NOT force focus to Reviews.
// This pins the count-drop guard against over-firing.
func TestMouseClickCloseButtonOnLogTabDoesNotRedirectToReviews(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := writeSpoolFixture(t, filepath.Join(tmp, "pending"), "owner-repo-123.md", "# Review body\n")

	m := createTestModelWithReviewsTab(t, []review.Record{
		{Repo: "owner/repo", PR: 123, SpoolPath: spoolPath},
	})

	// Open a review content tab (so a review content tab exists in the strip),
	// then add a Log tab AFTER it and make the Log tab active.
	updated := selectFirstRowAndPressEnter(t, m)
	reviewTab := updated.tabManager.GetActiveTab()
	if reviewTab == nil || reviewTab.Type() != TabTypeReviewContent {
		t.Fatalf("expected active tab to be TabTypeReviewContent, got %v", reviewTab)
	}
	reviewTabID := reviewTab.ID()

	logTab := NewLogTab("log-1", "info", 100, updated.styles)
	updated.tabManager.AddTab(logTab)
	logIdx := updated.tabManager.FindTabByID(logTab.ID())
	if logIdx < 0 {
		t.Fatalf("expected to find the log tab")
	}
	var switchCmd tea.Cmd
	updated, switchCmd = updated.switchActiveTab(logIdx)
	_ = switchCmd

	// Click the × of the Log tab.
	tabs := updated.tabManager.GetTabs()
	clickPos := tabClickPos(tabs, logIdx, true)

	updatedModel, _ := updated.Update(tea.MouseClickMsg{X: clickPos, Y: 0})
	afterClick := updatedModel.(model)

	// The Log tab is gone, the review content tab remains, and focus was NOT
	// forced to Reviews (the redirect must not over-fire on a non-review close).
	if idx := afterClick.tabManager.FindTabByID(logTab.ID()); idx >= 0 {
		t.Errorf("expected log tab to be removed after click-close, found at index %d", idx)
	}
	if idx := afterClick.tabManager.FindTabByID(reviewTabID); idx < 0 {
		t.Errorf("expected review content tab %q to remain after closing an unrelated log tab", reviewTabID)
	}
	afterActiveTab := afterClick.tabManager.GetActiveTab()
	if afterActiveTab != nil && afterActiveTab.Type() == TabTypeReviews {
		t.Errorf("closing a non-review Log tab must not redirect to Reviews, but active tab is Reviews")
	}
}
