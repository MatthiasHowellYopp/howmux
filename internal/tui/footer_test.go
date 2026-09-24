package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/watcher"
)

func TestFooterRendersExactly3Lines(t *testing.T) {
	cfg := &config.Config{Theme: "default"}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)
	theme := &config.Theme{}
	styles := NewStyles(theme)
	autocomplete := NewAutocompleteInput(registry, styles)
	tabManager := NewTabManager()
	w := watcher.New(cfg, manager)

	fm := NewFooterManager(styles, cfg, w, autocomplete, tabManager)
	fm.Resize(80, 24)

	tests := []struct {
		name    string
		tabType TabType
	}{
		{"main tab", TabTypeMain},
		{"planning tab", TabTypePlanning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := fm.RenderWithSeparator(tt.tabType)
			lines := strings.Split(rendered, "\n")
			expected := fm.GetFooterHeight()
			if len(lines) != expected {
				t.Errorf("RenderWithSeparator(%v) produced %d lines, expected %d\nContent: %q",
					tt.tabType, len(lines), expected, rendered)
			}
		})
	}
}

func TestFooterDropdownRendersExactly3LinesWithoutDropdown(t *testing.T) {
	cfg := &config.Config{Theme: "default"}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)
	theme := &config.Theme{}
	styles := NewStyles(theme)
	autocomplete := NewAutocompleteInput(registry, styles)
	tabManager := NewTabManager()
	w := watcher.New(cfg, manager)

	fm := NewFooterManager(styles, cfg, w, autocomplete, tabManager)
	fm.Resize(80, 24)

	tests := []struct {
		name    string
		tabType TabType
	}{
		{"main tab", TabTypeMain},
		{"planning tab", TabTypePlanning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := fm.RenderWithSeparator(tt.tabType)
			lines := strings.Split(rendered, "\n")
			expected := fm.GetFooterHeight()
			if len(lines) != expected {
				t.Errorf("RenderWithSeparator(%v) produced %d lines, expected %d\nContent: %q",
					tt.tabType, len(lines), expected, rendered)
			}
		})
	}
}

// TestNotesEditMode_SetsAndClearsFooterTransientMessage proves the issue
// #86 Task 6 footer/status feedback contract: entering decision_notes
// edit mode (via startNotesEditMsg, the same message ReviewsTab.Update's
// "n" key handler emits once the revise/rereview gate passes) sets the
// footer's transient message to the real wording, and exiting edit mode —
// via either the Enter-save path or the Esc-cancel path — clears it back
// to "" so the footer's normal status row reappears. Verified end-to-end
// through model.Update rather than by calling SetTransientMessage
// directly, so a regression in the wiring (not just in FooterManager
// itself) would be caught.
func TestNotesEditMode_SetsAndClearsFooterTransientMessage(t *testing.T) {
	t.Run("entry sets the expected message", func(t *testing.T) {
		spoolPath := writeFakeSpoolWithDecision(t, "revise")
		m := newNotesEditRoutingTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 42, SpoolPath: spoolPath})

		reviewsIdx := m.findReviewsTabIndex()
		if reviewsIdx < 0 {
			t.Fatalf("expected a Reviews tab to be present")
		}
		m, _ = m.switchActiveTab(reviewsIdx)

		result, cmd := m.Update(pressKey('n'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting startNotesEditMsg, got nil")
		}
		msg := cmd()
		startMsg, ok := msg.(startNotesEditMsg)
		if !ok {
			t.Fatalf("expected startNotesEditMsg, got %T (%v)", msg, msg)
		}

		finalResult, _ := resultModel.Update(startMsg)
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		want := "Editing notes for owner/repo #42 (Enter to save, Esc to cancel)"
		got := finalModel.footerManager.transientMessage
		if got != want {
			t.Errorf("expected footer transient message %q, got %q", want, got)
		}
	})

	t.Run("Enter-save clears the message", func(t *testing.T) {
		origWd := chdirToRepoRootForNotesEdit(t)
		defer restoreWd(t, origWd)

		spoolPath := newTempSpoolForNotesEdit(t)
		m := newNotesEditRoutingTestModel()
		result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: spoolPath, currentNotes: ""})
		m, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if m.footerManager.transientMessage == "" {
			t.Fatalf("test setup broken: expected a non-empty transient message after entering notes-edit mode")
		}

		result, _ = m.Update(pressKey('o'))
		m, ok = result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		result, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if got := resultModel.footerManager.transientMessage; got != "" {
			t.Errorf("expected footer transient message to be cleared after Enter-save, got %q", got)
		}
	})

	t.Run("Esc-cancel clears the message", func(t *testing.T) {
		m := newNotesEditRoutingTestModel()
		result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: "/tmp/spool.md", currentNotes: ""})
		m, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if m.footerManager.transientMessage == "" {
			t.Fatalf("test setup broken: expected a non-empty transient message after entering notes-edit mode")
		}

		result, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if got := resultModel.footerManager.transientMessage; got != "" {
			t.Errorf("expected footer transient message to be cleared after Esc-cancel, got %q", got)
		}
	})
}

// TestFooterReviewStatus covers all four review-segment states: unavailable
// (no SetReviewWatcher call), inactive (set but not started), active with 0
// enrolled, and active with N>0 enrolled. Uses review.NewWatcher backed by a
// real review.NewStore(t.TempDir()) — footer_test.go is in package tui, so it
// cannot reach review's unexported fakeStore test double.
func TestFooterReviewStatus(t *testing.T) {
	newFooter := func(t *testing.T) *FooterManager {
		cfg := &config.Config{Theme: "default"}
		manager := agent.NewManager(cfg)
		registry := NewCommandRegistry(manager)
		theme := &config.Theme{}
		styles := NewStyles(theme)
		autocomplete := NewAutocompleteInput(registry, styles)
		tabManager := NewTabManager()
		w := watcher.New(cfg, manager)
		return NewFooterManager(styles, cfg, w, autocomplete, tabManager)
	}

	t.Run("unavailable when SetReviewWatcher never called", func(t *testing.T) {
		fm := newFooter(t)

		got := fm.renderReviewStatus()
		want := "review: unavailable"
		if got != want {
			t.Errorf("renderReviewStatus() = %q, want %q", got, want)
		}
		if base := fm.renderBaseInfo(); !strings.Contains(base, want) {
			t.Errorf("renderBaseInfo() = %q, want it to contain %q", base, want)
		}
	})

	t.Run("inactive when watcher set but not started", func(t *testing.T) {
		fm := newFooter(t)
		store := review.NewStore(t.TempDir())
		rw := review.NewWatcher(store, time.Minute, 2, "test-reviewer")
		fm.SetReviewWatcher(rw)

		got := fm.renderReviewStatus()
		want := "review: inactive"
		if got != want {
			t.Errorf("renderReviewStatus() = %q, want %q", got, want)
		}
	})

	t.Run("active with 0 enrolled", func(t *testing.T) {
		fm := newFooter(t)
		store := review.NewStore(t.TempDir())
		rw := review.NewWatcher(store, 5*time.Minute, 2, "test-reviewer")
		rw.Start()
		defer rw.Stop()
		fm.SetReviewWatcher(rw)

		got := fm.renderReviewStatus()
		want := "review: active (5m0s, 0 enrolled)"
		if got != want {
			t.Errorf("renderReviewStatus() = %q, want %q", got, want)
		}
	})

	t.Run("active with N>0 enrolled, no pluralization", func(t *testing.T) {
		fm := newFooter(t)
		store := review.NewStore(t.TempDir())
		if err := store.Save(review.Record{
			Repo:       "owner/repo",
			PR:         1,
			URL:        "https://github.com/owner/repo/pull/1",
			Status:     review.StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("failed to save record: %v", err)
		}
		if err := store.Save(review.Record{
			Repo:       "owner/repo",
			PR:         2,
			URL:        "https://github.com/owner/repo/pull/2",
			Status:     review.StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("failed to save record: %v", err)
		}
		if err := store.Save(review.Record{
			Repo:       "owner/repo",
			PR:         3,
			URL:        "https://github.com/owner/repo/pull/3",
			Status:     review.StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("failed to save record: %v", err)
		}

		rw := review.NewWatcher(store, 5*time.Minute, 2, "test-reviewer")
		rw.Start()
		defer rw.Stop()
		fm.SetReviewWatcher(rw)

		got := fm.renderReviewStatus()
		want := "review: active (5m0s, 3 enrolled)"
		if got != want {
			t.Errorf("renderReviewStatus() = %q, want %q", got, want)
		}
	})
}
