package tui

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// fakeReviewStore is a minimal in-package fake implementing
// review.StoreInterface for testing ReviewsTab without touching real disk.
// Only List() is exercised by ReviewsTab; Save/Get/Remove are unused no-ops.
type fakeReviewStore struct {
	records []review.Record
	listErr error
}

var _ review.StoreInterface = (*fakeReviewStore)(nil)

func (f *fakeReviewStore) List() ([]review.Record, error) {
	return f.records, f.listErr
}

func (f *fakeReviewStore) Save(rec review.Record) error { return nil }

func (f *fakeReviewStore) Get(repo string, pr int) (review.Record, bool, error) {
	return review.Record{}, false, nil
}

func (f *fakeReviewStore) Remove(repo string, pr int) error { return nil }

func testReviewsStyles() *Styles {
	return NewStyles(createMinimalTheme())
}

// TestReviewsTabConstruction verifies the basic Tab contract: ID, Type,
// IsClosable, and a non-empty Title.
func TestReviewsTabConstruction(t *testing.T) {
	store := &fakeReviewStore{}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	if rt.ID() != "reviews" {
		t.Errorf("expected ID 'reviews', got %q", rt.ID())
	}
	if rt.Type() != TabTypeReviews {
		t.Errorf("expected TabTypeReviews, got %v", rt.Type())
	}
	if rt.IsClosable() {
		t.Error("expected reviews tab to not be closable")
	}
	if rt.Title() == "" {
		t.Error("expected non-empty Title()")
	}
}

// TestReviewsTabEmptyState verifies both nil and empty-slice List() results
// render the placeholder message, not a broken zero-row table.
func TestReviewsTabEmptyState(t *testing.T) {
	cases := []struct {
		name    string
		records []review.Record
	}{
		{name: "nil records", records: nil},
		{name: "empty slice", records: []review.Record{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeReviewStore{records: tc.records}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())

			view := rt.View()
			if !strings.Contains(view, "No watched PRs") {
				t.Errorf("expected empty-state placeholder, got %q", view)
			}
		})
	}
}

// TestReviewsTabErrorState verifies a List() error renders an error message
// without panicking.
func TestReviewsTabErrorState(t *testing.T) {
	store := &fakeReviewStore{listErr: errors.New("disk fell over")}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	var view string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("View() panicked on store error: %v", r)
			}
		}()
		view = rt.View()
	}()

	if !strings.Contains(view, "Failed to read PR review state") {
		t.Errorf("expected error indicator, got %q", view)
	}
	if !strings.Contains(view, "disk fell over") {
		t.Errorf("expected underlying error text, got %q", view)
	}
}

// TestReviewsTabPopulatedRendering verifies each Status value renders, along
// with both an empty and populated LastReviewedAt.
func TestReviewsTabPopulatedRendering(t *testing.T) {
	records := []review.Record{
		{
			Repo:           "owner/repo-a",
			PR:             1,
			URL:            "https://github.com/owner/repo-a/pull/1",
			Status:         review.StatusWatching,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "",
		},
		{
			Repo:           "owner/repo-b",
			PR:             2,
			URL:            "https://github.com/owner/repo-b/pull/2",
			Status:         review.StatusReviewing,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "2024-03-15T10:30:00Z",
		},
		{
			Repo:           "owner/repo-c",
			PR:             3,
			URL:            "https://github.com/owner/repo-c/pull/3",
			Status:         review.StatusReviewed,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "2024-03-16T08:00:00Z",
		},
		{
			Repo:           "owner/repo-d",
			PR:             4,
			URL:            "https://github.com/owner/repo-d/pull/4",
			Status:         review.StatusDone,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "2024-03-17T09:15:00Z",
		},
	}

	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	view := rt.View()

	for _, rec := range records {
		if !strings.Contains(view, rec.Repo) {
			t.Errorf("expected view to contain repo %q, got %q", rec.Repo, view)
		}
		prStr := "#" + fmt.Sprintf("%d", rec.PR)
		if !strings.Contains(view, prStr) {
			t.Errorf("expected view to contain PR number %q, got %q", prStr, view)
		}
		if !strings.Contains(view, string(rec.Status)) {
			t.Errorf("expected view to contain status %q, got %q", rec.Status, view)
		}
	}

	// The record with an empty LastReviewedAt must render the placeholder.
	if !strings.Contains(view, emptyTimestampPlaceholder) {
		t.Errorf("expected %q placeholder for empty LastReviewedAt, got %q", emptyTimestampPlaceholder, view)
	}
}

// TestReviewsTabMalformedTimestamp verifies a record with an unparseable
// LastReviewedAt falls back to the raw string instead of panicking.
func TestReviewsTabMalformedTimestamp(t *testing.T) {
	records := []review.Record{
		{
			Repo:           "owner/repo",
			PR:             9,
			URL:            "https://github.com/owner/repo/pull/9",
			Status:         review.StatusWatching,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "not-a-timestamp",
		},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	view := rt.View()
	if !strings.Contains(view, "not-a-timestamp") {
		t.Errorf("expected raw fallback for malformed timestamp, got %q", view)
	}
}

// TestReviewsTabCopyableContentParity verifies CopyableContent's independent
// plain-text rebuild carries the same repo/PR/status data as View(), and
// contains no ANSI escape codes.
func TestReviewsTabCopyableContentParity(t *testing.T) {
	records := []review.Record{
		{
			Repo:           "owner/repo-a",
			PR:             1,
			URL:            "https://github.com/owner/repo-a/pull/1",
			Status:         review.StatusWatching,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "",
		},
		{
			Repo:           "owner/repo-b",
			PR:             2,
			URL:            "https://github.com/owner/repo-b/pull/2",
			Status:         review.StatusDone,
			EnrolledAt:     "2024-01-01T00:00:00Z",
			LastReviewedAt: "2024-03-15T10:30:00Z",
		},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	content := rt.CopyableContent()

	if strings.Contains(content, "\x1b[") {
		t.Errorf("CopyableContent contains ANSI escape codes: %q", content)
	}
	for _, rec := range records {
		if !strings.Contains(content, rec.Repo) {
			t.Errorf("CopyableContent missing repo %q: %q", rec.Repo, content)
		}
		if !strings.Contains(content, string(rec.Status)) {
			t.Errorf("CopyableContent missing status %q: %q", rec.Status, content)
		}
	}
	if !strings.Contains(content, emptyTimestampPlaceholder) {
		t.Errorf("CopyableContent missing empty-timestamp placeholder: %q", content)
	}
}

// TestReviewsTabCopyableContentEmptyAndError verify CopyableContent handles
// the empty and error states the same way View() does.
func TestReviewsTabCopyableContentEmptyAndError(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		store := &fakeReviewStore{records: nil}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		content := rt.CopyableContent()
		if !strings.Contains(content, "No watched PRs") {
			t.Errorf("expected empty-state placeholder, got %q", content)
		}
	})

	t.Run("error", func(t *testing.T) {
		store := &fakeReviewStore{listErr: errors.New("disk fell over")}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		content := rt.CopyableContent()
		if !strings.Contains(content, "Failed to read PR review state") {
			t.Errorf("expected error indicator, got %q", content)
		}
	})
}

// TestReviewsTabViewCopyDrift asserts that View() with its ANSI styling
// stripped is byte-identical to CopyableContent(). Both now share the same
// row/header formatters and message strings (buildTable + the reviews*Message
// constants), so this guards against the two ever drifting apart — the
// duplication hazard called out in review: edit one and copy would silently
// show different text than the view.
func TestReviewsTabViewCopyDrift(t *testing.T) {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	strip := func(s string) string { return ansi.ReplaceAllString(s, "") }

	cases := map[string]*fakeReviewStore{
		"empty": {records: nil},
		"populated": {records: []review.Record{
			{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone, LastReviewedAt: "2024-03-15T10:30:00Z"},
			{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching, LastReviewedAt: ""},
		}},
	}

	for name, store := range cases {
		t.Run(name, func(t *testing.T) {
			rt := NewReviewsTab("reviews", store, testReviewsStyles())
			gotView := strip(rt.View())
			gotCopy := rt.CopyableContent()
			if gotView != gotCopy {
				t.Errorf("View() (ANSI-stripped) and CopyableContent() drifted:\nview: %q\ncopy: %q", gotView, gotCopy)
			}
		})
	}
}

// TestReviewsTabViewPadsToHeight verifies View() fills its content area so the
// footer (composed after it) pins to the bottom, matching MainTab/LogTab. With
// no height set (height 0) it must not pad, so CopyableContent parity holds.
func TestReviewsTabViewPadsToHeight(t *testing.T) {
	store := &fakeReviewStore{records: []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
	}}

	t.Run("pads to height when set", func(t *testing.T) {
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		rt.Resize(80, 20)
		got := rt.View()
		if lines := strings.Count(got, "\n") + 1; lines != 20 {
			t.Errorf("expected View() padded to 20 lines, got %d", lines)
		}
	})

	t.Run("no pad when height unset", func(t *testing.T) {
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		got := rt.View()
		// header + one row = 2 lines, no trailing blanks
		if lines := strings.Count(got, "\n") + 1; lines != 2 {
			t.Errorf("expected 2 unpadded lines, got %d", lines)
		}
	})
}

// TestReviewsTabResizeNoPanic verifies Resize with a variety of dimensions
// doesn't panic and View() still returns a non-empty string afterward.
func TestReviewsTabResizeNoPanic(t *testing.T) {
	store := &fakeReviewStore{records: []review.Record{
		{Repo: "owner/repo", PR: 1, URL: "u", Status: review.StatusWatching, EnrolledAt: "2024-01-01T00:00:00Z"},
	}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	dims := [][2]int{
		{0, 0},
		{1, 1},
		{80, 24},
		{200, 50},
		{20, 5},
	}

	for _, d := range dims {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Resize(%d, %d) panicked: %v", d[0], d[1], r)
				}
			}()
			rt.Resize(d[0], d[1])
		}()

		view := rt.View()
		if view == "" {
			t.Errorf("expected non-empty View() after Resize(%d, %d)", d[0], d[1])
		}
	}
}

// TestReviewsTabFocusState verifies focus state mirrors MainTab: footer
// focus, no-op restore.
func TestReviewsTabFocusState(t *testing.T) {
	store := &fakeReviewStore{}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	if rt.CaptureFocusState() != FocusTargetFooter {
		t.Errorf("expected FocusTargetFooter, got %v", rt.CaptureFocusState())
	}
	if cmd := rt.RestoreFocusState(FocusTargetFooter); cmd != nil {
		t.Error("expected RestoreFocusState to be a no-op returning nil")
	}
}

// TestReviewsTabUpdateNoOp verifies Update returns the tab unchanged with no
// command, since there is no internal state to update and no tea.Tick loop.
func TestReviewsTabUpdateNoOp(t *testing.T) {
	store := &fakeReviewStore{}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	tab, cmd := rt.Update(nil)
	if tab != rt {
		t.Error("expected Update to return the same tab instance")
	}
	if cmd != nil {
		t.Error("expected Update to return a nil command")
	}
}

// withFakeSpoolInfoFunc temporarily swaps spoolInfoForFunc for a fake that
// never touches the real ~/PR-Review directory, restoring the original after
// the test completes. byPath maps a record's SpoolPath to the SpoolInfo the
// fake should return for it; any spoolPath not present in the map returns
// the zero-value SpoolInfo (Found: false, DecisionState: "").
func withFakeSpoolInfoFunc(t *testing.T, byPath map[string]review.SpoolInfo) {
	t.Helper()
	original := spoolInfoForFunc
	spoolInfoForFunc = func(spoolPath, homeDir string) review.SpoolInfo {
		if info, ok := byPath[spoolPath]; ok {
			return info
		}
		return review.SpoolInfo{}
	}
	t.Cleanup(func() { spoolInfoForFunc = original })
}

// TestReviewsTabSpoolColumns covers all 5 AC3 decision-state cases end-to-end
// through View() and CopyableContent(), using the spoolInfoForFunc seam so no
// real filesystem access under ~/PR-Review happens.
func TestReviewsTabSpoolColumns(t *testing.T) {
	cases := []struct {
		name          string
		spoolPath     string
		info          review.SpoolInfo
		wantVerdict   string
		wantDecision  string
		wantSpoolPath string // "" means expect emptyTimestampPlaceholder
	}{
		{
			name:      "pending",
			spoolPath: "/home/user/PR-Review/pending/owner-repo-1.md",
			info: review.SpoolInfo{
				Found:         true,
				InDoneDir:     false,
				Verdict:       "APPROVE",
				Decision:      "",
				DecisionState: review.ClassifySpoolState(true, false, ""),
			},
			wantVerdict:   "APPROVE",
			wantDecision:  "pending",
			wantSpoolPath: "/home/user/PR-Review/pending/owner-repo-1.md",
		},
		{
			name:      "decided",
			spoolPath: "/home/user/PR-Review/pending/owner-repo-2.md",
			info: review.SpoolInfo{
				Found:         true,
				InDoneDir:     false,
				Verdict:       "REQUEST_CHANGES",
				Decision:      "post",
				DecisionState: review.ClassifySpoolState(true, false, "post"),
			},
			wantVerdict:   "REQUEST_CHANGES",
			wantDecision:  "decided: post",
			wantSpoolPath: "/home/user/PR-Review/pending/owner-repo-2.md",
		},
		{
			name:      "posted",
			spoolPath: "/home/user/PR-Review/pending/owner-repo-3.md",
			info: review.SpoolInfo{
				Found:         true,
				InDoneDir:     true,
				Verdict:       "APPROVE",
				Decision:      "post",
				DecisionState: review.ClassifySpoolState(true, true, "post"),
			},
			wantVerdict:   "APPROVE",
			wantDecision:  "posted",
			wantSpoolPath: "/home/user/PR-Review/pending/owner-repo-3.md",
		},
		{
			name:      "no spool - empty SpoolPath",
			spoolPath: "",
			info: review.SpoolInfo{
				Found:         false,
				DecisionState: review.ClassifySpoolState(false, false, ""),
			},
			wantVerdict:   "",
			wantDecision:  "no spool",
			wantSpoolPath: "",
		},
		{
			name:      "no spool - nonexistent file",
			spoolPath: "/home/user/PR-Review/pending/owner-repo-5.md",
			info: review.SpoolInfo{
				Found:         false,
				DecisionState: review.ClassifySpoolState(false, false, ""),
			},
			wantVerdict:   "",
			wantDecision:  "no spool",
			wantSpoolPath: "/home/user/PR-Review/pending/owner-repo-5.md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFakeSpoolInfoFunc(t, map[string]review.SpoolInfo{
				tc.spoolPath: tc.info,
			})

			records := []review.Record{
				{
					Repo:           "owner/repo",
					PR:             1,
					Status:         review.StatusReviewed,
					LastReviewedAt: "2024-03-15T10:30:00Z",
					SpoolPath:      tc.spoolPath,
				},
			}
			store := &fakeReviewStore{records: records}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())

			view := rt.View()
			content := rt.CopyableContent()

			for _, rendered := range []string{view, content} {
				if !strings.Contains(rendered, tc.wantDecision) {
					t.Errorf("expected decision state %q in rendered output, got %q", tc.wantDecision, rendered)
				}

				if tc.wantVerdict == "" {
					if !strings.Contains(rendered, emptyTimestampPlaceholder) {
						t.Errorf("expected empty-verdict placeholder %q in rendered output, got %q", emptyTimestampPlaceholder, rendered)
					}
				} else if !strings.Contains(rendered, tc.wantVerdict) {
					t.Errorf("expected verdict %q in rendered output, got %q", tc.wantVerdict, rendered)
				}

				if tc.wantSpoolPath == "" {
					if !strings.Contains(rendered, emptyTimestampPlaceholder) {
						t.Errorf("expected empty-spoolpath placeholder %q in rendered output, got %q", emptyTimestampPlaceholder, rendered)
					}
				} else if !strings.Contains(rendered, truncate(tc.wantSpoolPath, reviewsColSpoolPath)) {
					t.Errorf("expected spool path %q in rendered output, got %q", truncate(tc.wantSpoolPath, reviewsColSpoolPath), rendered)
				}
			}
		})
	}
}

// TestReviewsTabManagerIntegration verifies the tab can be added to a
// TabManager and behaves as a permanent, non-closable tab.
func TestReviewsTabManagerIntegration(t *testing.T) {
	tm := NewTabManager()
	store := &fakeReviewStore{}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	tm.AddTab(rt)

	found := false
	for _, tab := range tm.GetTabs() {
		if tab.ID() == "reviews" {
			found = true
		}
	}
	if !found {
		t.Error("expected GetTabs() to include the reviews tab")
	}

	if removed := tm.RemoveTab("reviews"); removed {
		t.Error("expected RemoveTab to return false for non-closable reviews tab")
	}
}

// upKeyMsg and downKeyMsg build the tea.KeyMsg values ReviewsTab.Update()
// matches on via keyMsg.String() == "up"/"down".
func upKeyMsg() tea.KeyMsg   { return tea.KeyPressMsg{Code: tea.KeyUp} }
func downKeyMsg() tea.KeyMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }

// TestReviewsTabRowCount verifies rowCount() reads through to the store,
// returning 0 on error or empty/nil records and len(records) otherwise.
func TestReviewsTabRowCount(t *testing.T) {
	cases := []struct {
		name    string
		store   *fakeReviewStore
		wantLen int
	}{
		{name: "nil records", store: &fakeReviewStore{records: nil}, wantLen: 0},
		{name: "empty slice", store: &fakeReviewStore{records: []review.Record{}}, wantLen: 0},
		{name: "list error", store: &fakeReviewStore{listErr: errors.New("boom")}, wantLen: 0},
		{
			name: "populated",
			store: &fakeReviewStore{records: []review.Record{
				{Repo: "owner/repo-a", PR: 1},
				{Repo: "owner/repo-b", PR: 2},
				{Repo: "owner/repo-c", PR: 3},
			}},
			wantLen: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := NewReviewsTab("reviews", tc.store, testReviewsStyles())
			if got := rt.rowCount(); got != tc.wantLen {
				t.Errorf("rowCount() = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

// TestReviewsTabMoveCursor covers the clamping logic directly: staying at 0
// when moving up from the initial position, clamping at n-1 when moving down
// past the last row, and disabling selection (-1) when the store has zero
// records regardless of direction.
func TestReviewsTabMoveCursor(t *testing.T) {
	t.Run("moveCursor(-1) from initial state clamps to 0", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		rt.moveCursor(-1)
		if rt.selectedIndex != 0 {
			t.Errorf("selectedIndex = %d, want 0", rt.selectedIndex)
		}
	})

	t.Run("moveCursor(1) repeatedly clamps at n-1", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
			{Repo: "owner/repo-c", PR: 3},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		for i := 0; i < 10; i++ {
			rt.moveCursor(1)
		}
		if rt.selectedIndex != 2 {
			t.Errorf("selectedIndex = %d, want 2 (n-1)", rt.selectedIndex)
		}
	})

	t.Run("moveCursor sets -1 when store has zero records", func(t *testing.T) {
		for _, delta := range []int{-1, 1} {
			store := &fakeReviewStore{records: nil}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())

			rt.moveCursor(delta)
			if rt.selectedIndex != -1 {
				t.Errorf("delta=%d: selectedIndex = %d, want -1", delta, rt.selectedIndex)
			}
		}
	})
}

// TestReviewsTabBuildTableRowStyleIdentity verifies passing an identity
// rowStyle (as CopyableContent does) leaves buildTable's output unchanged
// relative to the pre-Task-2 behavior — a direct regression guard on
// buildTable's new parameter independent of ReviewsTab plumbing.
func TestReviewsTabBuildTableRowStyleIdentity(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
	}
	spoolInfo := []review.SpoolInfo{{}}
	plainStatus := func(_ review.Status, text string) string { return text }
	identityRow := func(_ int, line string) string { return line }

	got := buildTable(records, spoolInfo, identityStyle, plainStatus, identityRow)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("expected no ANSI codes with identity rowStyle, got %q", got)
	}
	if !strings.Contains(got, "owner/repo-a") {
		t.Errorf("expected row content present, got %q", got)
	}
}

// TestReviewsTabHighlightPresence verifies View() wraps exactly the row at
// selectedIndex with the AutocompleteSelected style, and no other row, when
// styles is non-nil and records are non-empty. Note: the STATUS column always
// carries its own independent ANSI styling (via styleStatus), so this checks
// specifically for the row-highlight wrapper — which, applied via
// AutocompleteSelected.Render(line), starts at the very beginning of the
// line — rather than "any ANSI code present anywhere in the line".
func TestReviewsTabHighlightPresence(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
		{Repo: "owner/repo-c", PR: 3, Status: review.StatusReviewed},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	rt.selectedIndex = 1 // owner/repo-b, sorted order matches insertion order here

	view := rt.View()
	lines := strings.Split(view, "\n")

	// A row wrapped by AutocompleteSelected.Render(line) starts with an ANSI
	// escape sequence immediately at index 0 of the line, since the whole
	// line (including its own already-styled STATUS column) is re-wrapped.
	isRowHighlighted := func(line string) bool { return strings.HasPrefix(line, "\x1b[") }

	for _, line := range lines[1:] { // skip header
		switch {
		case strings.Contains(line, "owner/repo-b"):
			if !isRowHighlighted(line) {
				t.Errorf("expected selected row (owner/repo-b) to carry the row-highlight wrapper, got %q", line)
			}
		case strings.Contains(line, "owner/repo-a"), strings.Contains(line, "owner/repo-c"):
			if isRowHighlighted(line) {
				t.Errorf("expected non-selected row to lack the row-highlight wrapper, got %q", line)
			}
		}
	}
}

// TestReviewsTabHighlightAbsence verifies that when selectedIndex is out of
// range, no row carries the row-highlight wrapper (individual columns, like
// STATUS, may still carry their own independent styling).
func TestReviewsTabHighlightAbsence(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	rt.selectedIndex = 99 // out of range: no row should be highlighted

	view := rt.View()
	lines := strings.Split(view, "\n")

	for _, line := range lines[1:] { // skip header
		if strings.HasPrefix(line, "\x1b[") {
			t.Errorf("expected no row-highlight wrapper with out-of-range selectedIndex, got %q", line)
		}
	}
}

// TestReviewsTabHighlightNilStyles verifies that with styles == nil, View()
// does not panic and produces entirely unstyled output, matching pre-Task-2
// nil-styles behavior.
func TestReviewsTabHighlightNilStyles(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, nil)
	rt.selectedIndex = 0

	var view string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("View() panicked with nil styles: %v", r)
			}
		}()
		view = rt.View()
	}()

	if strings.Contains(view, "\x1b[") {
		t.Errorf("expected no ANSI codes with nil styles, got %q", view)
	}
	if !strings.Contains(view, "owner/repo-a") {
		t.Errorf("expected row content present, got %q", view)
	}
}

// TestReviewsTabUpdateNavigation covers arrow-key navigation through
// Update(): clamped down-navigation past the last row, clamped up-navigation
// at row 0, and safe no-op behavior against an empty store.
func TestReviewsTabUpdateNavigation(t *testing.T) {
	t.Run("down navigation clamps at n-1", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
			{Repo: "owner/repo-c", PR: 3},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		// N-1 "down" presses reach the last row (n=3, so 2 presses from index 0).
		for i := 0; i < 2; i++ {
			tab, cmd := rt.Update(downKeyMsg())
			rt = tab.(*ReviewsTab)
			if cmd != nil {
				t.Errorf("expected nil cmd from Update, got %v", cmd)
			}
		}
		if rt.selectedIndex != 2 {
			t.Errorf("selectedIndex = %d, want 2", rt.selectedIndex)
		}

		// One more "down" must not move past the last row.
		tab, _ := rt.Update(downKeyMsg())
		rt = tab.(*ReviewsTab)
		if rt.selectedIndex != 2 {
			t.Errorf("selectedIndex = %d after extra down, want clamped at 2", rt.selectedIndex)
		}
	})

	t.Run("up navigation clamps at 0", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		tab, _ := rt.Update(upKeyMsg())
		rt = tab.(*ReviewsTab)
		if rt.selectedIndex != 0 {
			t.Errorf("selectedIndex = %d, want 0 (already at floor)", rt.selectedIndex)
		}
	})

	t.Run("empty store does not panic and disables selection", func(t *testing.T) {
		store := &fakeReviewStore{records: nil}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		for _, keyMsg := range []tea.KeyMsg{upKeyMsg(), downKeyMsg()} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Update() panicked on empty store: %v", r)
					}
				}()
				tab, _ := rt.Update(keyMsg)
				rt = tab.(*ReviewsTab)
			}()
			if rt.selectedIndex != -1 {
				t.Errorf("selectedIndex = %d, want -1 on empty store", rt.selectedIndex)
			}
		}
	})

	t.Run("Update and View integration reflects moved cursor", func(t *testing.T) {
		records := []review.Record{
			{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
			{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
		}
		store := &fakeReviewStore{records: records}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())

		tab, _ := rt.Update(downKeyMsg())
		rt = tab.(*ReviewsTab)
		if rt.selectedIndex != 1 {
			t.Fatalf("selectedIndex = %d, want 1 before checking View()", rt.selectedIndex)
		}

		view := rt.View()
		lines := strings.Split(view, "\n")
		found := false
		for _, line := range lines {
			if strings.Contains(line, "owner/repo-b") && strings.HasPrefix(line, "\x1b[") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected owner/repo-b row (selectedIndex=1) to carry the row-highlight wrapper in View(), got:\n%s", view)
		}
	})

	t.Run("non-key message and unrecognized key are no-ops", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		rt.selectedIndex = 0

		tab, cmd := rt.Update(TickMsg{})
		rt = tab.(*ReviewsTab)
		if cmd != nil {
			t.Error("expected nil cmd for non-key message")
		}
		if rt.selectedIndex != 0 {
			t.Errorf("expected selectedIndex unchanged by non-key message, got %d", rt.selectedIndex)
		}

		tab, _ = rt.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		rt = tab.(*ReviewsTab)
		if rt.selectedIndex != 0 {
			t.Errorf("expected selectedIndex unchanged by an unrecognized key, got %d", rt.selectedIndex)
		}
	})
}

// TestReviewsTabCursorPersistsAcrossViewCalls verifies selectedIndex is not
// implicitly reset by any code path in View(), Resize(), CaptureFocusState(),
// or RestoreFocusState() — the "tab regains focus" scenario. Per the design
// spec, this is a verification-only test: TabManager holds the same
// *ReviewsTab across tab switches, so persistence requires no new save/
// restore machinery, only the absence of an accidental reset.
func TestReviewsTabCursorPersistsAcrossViewCalls(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
		{Repo: "owner/repo-c", PR: 3, Status: review.StatusReviewed},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	// Move the cursor to a non-zero index.
	tab, _ := rt.Update(downKeyMsg())
	rt = tab.(*ReviewsTab)
	tab, _ = rt.Update(downKeyMsg())
	rt = tab.(*ReviewsTab)
	if rt.selectedIndex != 2 {
		t.Fatalf("selectedIndex = %d, want 2 before simulating focus change", rt.selectedIndex)
	}

	// Simulate the surrounding operations a tab-switch/redraw cycle performs,
	// none of which should touch selectedIndex.
	_ = rt.CaptureFocusState()
	_ = rt.RestoreFocusState(FocusTargetFooter)
	rt.Resize(80, 24)
	_ = rt.View()

	if rt.selectedIndex != 2 {
		t.Errorf("selectedIndex was reset to %d, want it to remain 2 across View()/Resize()/focus calls", rt.selectedIndex)
	}

	// A second View() call (simulating "tab regains focus" redraw) must still
	// reflect the same selection.
	view := rt.View()
	lines := strings.Split(view, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "owner/repo-c") && strings.HasPrefix(line, "\x1b[") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected owner/repo-c (selectedIndex=2) to remain highlighted on second View() call, got:\n%s", view)
	}
}
