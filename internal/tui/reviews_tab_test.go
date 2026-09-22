package tui

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// TestReviewsTabFocusState verifies the tab reports row-navigation mode
// (FocusTargetRows) as its captured focus state — not FocusTargetFooter —
// so switchActiveTab leaves the footer unfocused by default when the
// Reviews tab becomes active (see AC2 fix, issue #85 qa-attempt:2).
// RestoreFocusState remains a no-op for both targets: this tab has no
// internal widget to focus/blur, all focus arbitration lives in the parent
// model (see setReviewsFocus/toggleReviewsFocus in tui.go).
func TestReviewsTabFocusState(t *testing.T) {
	store := &fakeReviewStore{}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())

	if rt.CaptureFocusState() != FocusTargetRows {
		t.Errorf("expected FocusTargetRows, got %v", rt.CaptureFocusState())
	}
	if cmd := rt.RestoreFocusState(FocusTargetRows); cmd != nil {
		t.Error("expected RestoreFocusState to be a no-op returning nil")
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

// TestReviewsTabMoveCursor covers identity-based navigation over the order
// cached at the last render: up from the first row stays put, down past the
// last row clamps, and an empty store clears the selection. moveCursor
// performs no store I/O — it steps within rt.lastOrder, which a prior render
// populates.
func TestReviewsTabMoveCursor(t *testing.T) {
	seed := func(rt *ReviewsTab) { _ = rt.View() } // populate lastOrder

	t.Run("moveCursor(-1) from first row stays on first", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		seed(rt) // selection defaults to first row (owner/repo-a#1)

		rt.moveCursor(-1)
		if rt.SelectedKey() != "owner/repo-a#1" {
			t.Errorf("SelectedKey() = %q, want owner/repo-a#1", rt.SelectedKey())
		}
	})

	t.Run("moveCursor(1) repeatedly clamps at last row", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
			{Repo: "owner/repo-c", PR: 3},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		seed(rt)

		for i := 0; i < 10; i++ {
			rt.moveCursor(1)
		}
		if rt.SelectedKey() != "owner/repo-c#3" {
			t.Errorf("SelectedKey() = %q, want owner/repo-c#3 (last row)", rt.SelectedKey())
		}
	})

	t.Run("moveCursor clears selection when store has zero records", func(t *testing.T) {
		for _, delta := range []int{-1, 1} {
			store := &fakeReviewStore{records: nil}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())
			seed(rt) // empty render leaves lastOrder empty

			rt.moveCursor(delta)
			if rt.SelectedKey() != "" {
				t.Errorf("delta=%d: SelectedKey() = %q, want empty", delta, rt.SelectedKey())
			}
		}
	})
}

// TestReviewsTabSelectionFollowsPRAcrossResort is the core identity-selection
// guarantee (the #83 review's architectural finding): when a new PR enrolls
// and sorts ABOVE the selected row, the selection must still point at the same
// PR — not silently re-target to whatever now occupies the old row index.
func TestReviewsTabSelectionFollowsPRAcrossResort(t *testing.T) {
	store := &fakeReviewStore{records: []review.Record{
		{Repo: "foo/bar", PR: 10, Status: review.StatusWatching},
		{Repo: "foo/baz", PR: 20, Status: review.StatusWatching},
	}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed order: [foo/bar#10, foo/baz#20], selection -> foo/bar#10

	// Select the second row (foo/baz#20).
	rt.moveCursor(1)
	if rt.SelectedKey() != "foo/baz#20" {
		t.Fatalf("SelectedKey() = %q, want foo/baz#20", rt.SelectedKey())
	}

	// A new PR enrolls that sorts to the TOP (aaa/zzz < foo/*).
	store.records = append(store.records, review.Record{Repo: "aaa/zzz", PR: 1, Status: review.StatusWatching})

	// Re-render: order is now [aaa/zzz#1, foo/bar#10, foo/baz#20]. The
	// selection must still be foo/baz#20 (now index 2), not the PR that took
	// its old index.
	view := rt.View()
	if rt.SelectedKey() != "foo/baz#20" {
		t.Errorf("after re-sort SelectedKey() = %q, want foo/baz#20 (selection must follow its PR)", rt.SelectedKey())
	}
	// And the highlight must be on the foo/baz#20 row.
	for _, line := range strings.Split(view, "\n")[1:] { // skip styled header
		if strings.HasPrefix(line, "\x1b[") && !strings.Contains(line, "foo/baz") {
			t.Errorf("highlight landed on the wrong row after re-sort: %q", line)
		}
	}
}

// TestReviewsTabEmptyThenPopulatedSelectsFirst verifies that when the tab goes
// from empty to populated, the first render reconciles the selection to the
// first row (no keypress required) — the empty→populated gap the #83 review
// flagged.
func TestReviewsTabEmptyThenPopulatedSelectsFirst(t *testing.T) {
	store := &fakeReviewStore{records: nil}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // empty
	if rt.SelectedKey() != "" {
		t.Fatalf("expected empty selection on empty store, got %q", rt.SelectedKey())
	}

	store.records = []review.Record{{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching}}
	_ = rt.View() // now populated
	if rt.SelectedKey() != "owner/repo-a#1" {
		t.Errorf("expected first row selected on populate, got %q", rt.SelectedKey())
	}
}

// TestReviewsTabBuildTableRowStyleIdentity verifies passing an identity
// rowStyle (as CopyableContent does) leaves buildTable's output unstyled.
func TestReviewsTabBuildTableRowStyleIdentity(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
	}
	spoolInfo := []review.SpoolInfo{{}}
	plainStatus := func(_ int, _ review.Status, text string) string { return text }
	identityRow := func(_ int, line string) string { return line }

	got := buildTable(records, spoolInfo, identityStyle, plainStatus, identityRow)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("expected no ANSI codes with identity rowStyle, got %q", got)
	}
	if !strings.Contains(got, "owner/repo-a") {
		t.Errorf("expected row content present, got %q", got)
	}
}

// TestReviewsTabHighlightPresence verifies View() wraps exactly the selected
// row (by identity) with the AutocompleteSelected style, and no other row.
func TestReviewsTabHighlightPresence(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
		{Repo: "owner/repo-c", PR: 3, Status: review.StatusReviewed},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View()                     // seed order
	rt.selectedKey = "owner/repo-b#2" // select the middle row by identity

	view := rt.View()
	lines := strings.Split(view, "\n")

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

// TestReviewsTabSelectedRowStatusStaysLegible verifies the regression from
// issue #112: on the selected row, the STATUS cell must not carry its normal
// per-status foreground (Success/Warning/Prompt) — which is unreadable
// against the AutocompleteSelected highlight background — and must instead
// carry the highlight's own foreground (theme.Colors.Surface), matching what
// selectedStatusStyle derives from styles.AutocompleteSelected.
func TestReviewsTabSelectedRowStatusStaysLegible(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusDone},      // Success when unselected
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusReviewing}, // Warning when unselected
	}
	store := &fakeReviewStore{records: records}
	styles := testReviewsStyles()
	rt := NewReviewsTab("reviews", store, styles)
	_ = rt.View()                     // seed lastOrder
	rt.selectedKey = "owner/repo-a#1" // select the StatusDone row

	view := rt.View()

	// The ANSI sequence styleStatus would have used for an UNSELECTED
	// StatusDone row (Success foreground) must not appear anywhere in the
	// selected row's rendered STATUS text.
	unselectedDoneANSI := styles.Success.Render(string(review.StatusDone))
	// lipgloss renders foreground-only styles as an SGR sequence; extract
	// just the color-setting prefix (before the text) for a substring check
	// robust to reset placement — reuse the same helper pattern as
	// TestReviewsTabHighlightPresence's isRowHighlighted (ANSI-prefix check).
	ansiPrefix := func(rendered, text string) string {
		idx := strings.Index(rendered, text)
		if idx == -1 {
			return rendered
		}
		return rendered[:idx]
	}
	successPrefix := ansiPrefix(unselectedDoneANSI, string(review.StatusDone))

	lines := strings.Split(view, "\n")
	var selectedLine string
	for _, line := range lines[1:] { // skip header
		if strings.Contains(line, "owner/repo-a") {
			selectedLine = line
			break
		}
	}
	if selectedLine == "" {
		t.Fatalf("expected to find the selected row (owner/repo-a) in view, got %q", view)
	}

	if successPrefix != "" && strings.Contains(selectedLine, successPrefix) {
		t.Errorf("selected row's STATUS still carries the normal Success color sequence %q — expected it overridden by the highlight foreground; line: %q", successPrefix, selectedLine)
	}

	// Positive assertion: the selected row's STATUS text is rendered with
	// AutocompleteSelected's own foreground color (the highlight foreground),
	// not left uncolored and not colored per-status.
	highlightForeground := styles.AutocompleteSelected.GetForeground()
	expectedSelectedStatusPrefix := ansiPrefix(
		lipgloss.NewStyle().Foreground(highlightForeground).Render(string(review.StatusDone)),
		string(review.StatusDone),
	)
	if expectedSelectedStatusPrefix != "" && !strings.Contains(selectedLine, expectedSelectedStatusPrefix) {
		t.Errorf("expected selected row's STATUS to carry the highlight foreground sequence %q, got line %q", expectedSelectedStatusPrefix, selectedLine)
	}

	// Sanity: the OTHER (unselected) row must still carry its normal
	// per-status color — regression guard for the "unselected rows keep
	// their existing per-status colours" acceptance criterion.
	var unselectedLine string
	for _, line := range lines[1:] {
		if strings.Contains(line, "owner/repo-b") {
			unselectedLine = line
			break
		}
	}
	if unselectedLine == "" {
		t.Fatalf("expected to find the unselected row (owner/repo-b) in view, got %q", view)
	}
	unselectedWarningPrefix := ansiPrefix(styles.Warning.Render(string(review.StatusReviewing)), string(review.StatusReviewing))
	if unselectedWarningPrefix != "" && !strings.Contains(unselectedLine, unselectedWarningPrefix) {
		t.Errorf("expected unselected row's STATUS to keep its normal Warning color sequence %q, got line %q", unselectedWarningPrefix, unselectedLine)
	}
}

// TestReviewsTabHighlightStaleKey verifies that when selectedKey names a PR no
// longer present, the render reconciles to the first row rather than
// highlighting nothing or panicking.
func TestReviewsTabHighlightStaleKey(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	rt.selectedKey = "gone/away#99" // not present

	view := rt.View()
	if rt.SelectedKey() != "owner/repo-a#1" {
		t.Errorf("stale key should reconcile to first row, got %q", rt.SelectedKey())
	}
	// First row highlighted, exactly one highlight (skip styled header).
	highlights := 0
	for _, line := range strings.Split(view, "\n")[1:] {
		if strings.HasPrefix(line, "\x1b[") {
			highlights++
		}
	}
	if highlights != 1 {
		t.Errorf("expected exactly one highlighted row, got %d", highlights)
	}
}

// TestReviewsTabHighlightNilStyles verifies that with styles == nil, View()
// does not panic and produces entirely unstyled output.
func TestReviewsTabHighlightNilStyles(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, nil)

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

// TestReviewsTabUpdateNavigation covers arrow-key navigation through Update().
func TestReviewsTabUpdateNavigation(t *testing.T) {
	t.Run("down navigation clamps at last row", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
			{Repo: "owner/repo-c", PR: 3},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // seed; selection -> owner/repo-a#1

		for i := 0; i < 2; i++ {
			tab, cmd := rt.Update(downKeyMsg())
			rt = tab.(*ReviewsTab)
			if cmd != nil {
				t.Errorf("expected nil cmd from Update, got %v", cmd)
			}
			_ = rt.View() // re-render refreshes lastOrder between presses
		}
		if rt.SelectedKey() != "owner/repo-c#3" {
			t.Errorf("SelectedKey() = %q, want owner/repo-c#3", rt.SelectedKey())
		}

		tab, _ := rt.Update(downKeyMsg())
		rt = tab.(*ReviewsTab)
		if rt.SelectedKey() != "owner/repo-c#3" {
			t.Errorf("SelectedKey() = %q after extra down, want clamped at owner/repo-c#3", rt.SelectedKey())
		}
	})

	t.Run("up navigation clamps at first row", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

		tab, _ := rt.Update(upKeyMsg())
		rt = tab.(*ReviewsTab)
		if rt.SelectedKey() != "owner/repo-a#1" {
			t.Errorf("SelectedKey() = %q, want owner/repo-a#1 (floor)", rt.SelectedKey())
		}
	})

	t.Run("empty store does not panic and disables selection", func(t *testing.T) {
		store := &fakeReviewStore{records: nil}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

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
			if rt.SelectedKey() != "" {
				t.Errorf("SelectedKey() = %q, want empty on empty store", rt.SelectedKey())
			}
		}
	})

	t.Run("non-key message and unrecognized key are no-ops", func(t *testing.T) {
		store := &fakeReviewStore{records: []review.Record{
			{Repo: "owner/repo-a", PR: 1},
			{Repo: "owner/repo-b", PR: 2},
		}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // selection -> owner/repo-a#1

		tab, cmd := rt.Update(TickMsg{})
		rt = tab.(*ReviewsTab)
		if cmd != nil {
			t.Error("expected nil cmd for non-key message")
		}
		if rt.SelectedKey() != "owner/repo-a#1" {
			t.Errorf("expected selection unchanged by non-key message, got %q", rt.SelectedKey())
		}

		tab, _ = rt.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		rt = tab.(*ReviewsTab)
		if rt.SelectedKey() != "owner/repo-a#1" {
			t.Errorf("expected selection unchanged by an unrecognized key, got %q", rt.SelectedKey())
		}
	})
}

// TestReviewsTabCursorPersistsAcrossViewCalls verifies the selection is not
// implicitly reset by View(), Resize(), CaptureFocusState(), or
// RestoreFocusState() — the "tab regains focus" scenario.
func TestReviewsTabCursorPersistsAcrossViewCalls(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, Status: review.StatusWatching},
		{Repo: "owner/repo-b", PR: 2, Status: review.StatusDone},
		{Repo: "owner/repo-c", PR: 3, Status: review.StatusReviewed},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View()

	tab, _ := rt.Update(downKeyMsg())
	rt = tab.(*ReviewsTab)
	_ = rt.View()
	tab, _ = rt.Update(downKeyMsg())
	rt = tab.(*ReviewsTab)
	if rt.SelectedKey() != "owner/repo-c#3" {
		t.Fatalf("SelectedKey() = %q, want owner/repo-c#3 before simulating focus change", rt.SelectedKey())
	}

	_ = rt.CaptureFocusState()
	_ = rt.RestoreFocusState(FocusTargetFooter)
	rt.Resize(80, 24)
	_ = rt.View()

	if rt.SelectedKey() != "owner/repo-c#3" {
		t.Errorf("selection was reset to %q, want owner/repo-c#3 across View()/Resize()/focus calls", rt.SelectedKey())
	}

	view := rt.View()
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "owner/repo-c") && strings.HasPrefix(line, "\x1b[") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected owner/repo-c to remain highlighted on second View() call, got:\n%s", view)
	}
}

// enterKeyMsg builds the tea.KeyMsg value ReviewsTab.Update() matches on via
// keyMsg.String() == "enter", matching the existing
// TestKeyPressMsg{Code: tea.KeyEnter} convention already used in this file's
// "non-key message and unrecognized key are no-ops" subtest.
func enterKeyMsg() tea.KeyMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// TestReviewsTabEnterOnSelectedRowReturnsOpenReviewContentCmd verifies that
// pressing Enter with a row selected returns a non-nil tea.Cmd which, when
// invoked, produces an openReviewContentMsg carrying the selected record's
// repo/PR/spoolPath.
func TestReviewsTabEnterOnSelectedRowReturnsOpenReviewContentCmd(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, SpoolPath: "/tmp/spool-a.md"},
		{Repo: "owner/repo-b", PR: 2, SpoolPath: "/tmp/spool-b.md"},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed order; selection -> owner/repo-a#1

	tab, _ := rt.Update(downKeyMsg())
	rt = tab.(*ReviewsTab)
	_ = rt.View() // reconcile selection to owner/repo-b#2
	if rt.SelectedKey() != "owner/repo-b#2" {
		t.Fatalf("SelectedKey() = %q, want owner/repo-b#2 before pressing enter", rt.SelectedKey())
	}

	tab, cmd := rt.Update(enterKeyMsg())
	rt = tab.(*ReviewsTab)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from Update on enter with a selection")
	}

	msg := cmd()
	openMsg, ok := msg.(openReviewContentMsg)
	if !ok {
		t.Fatalf("expected openReviewContentMsg, got %T (%v)", msg, msg)
	}
	if openMsg.repo != "owner/repo-b" || openMsg.pr != 2 || openMsg.spoolPath != "/tmp/spool-b.md" {
		t.Errorf("openReviewContentMsg = %+v, want repo=owner/repo-b pr=2 spoolPath=/tmp/spool-b.md", openMsg)
	}
}

// TestReviewsTabEnterWithNoSelectionReturnsNilCmd verifies that Enter on an
// empty store (no selection) is a no-op, mirroring the existing
// "empty store does not panic and disables selection" fixture.
func TestReviewsTabEnterWithNoSelectionReturnsNilCmd(t *testing.T) {
	store := &fakeReviewStore{records: nil}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed; selection stays empty

	_, cmd := rt.Update(enterKeyMsg())
	if cmd != nil {
		t.Error("expected nil cmd from Update on enter with no selection")
	}
}

// TestReviewsTabEnterWithStoreListErrorReturnsNilCmd verifies that a
// store.List() error at the time Enter is pressed degrades to a no-op —
// no panic, no message emitted for a PR that can't be re-resolved.
func TestReviewsTabEnterWithStoreListErrorReturnsNilCmd(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, SpoolPath: "/tmp/spool-a.md"},
	}
	store := &fakeReviewStore{records: records}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed selection while List() still succeeds

	// Now make List() fail, simulating a transient I/O error between render
	// and the enter keypress.
	store.records = nil
	store.listErr = errors.New("disk fell over")

	_, cmd := rt.Update(enterKeyMsg())
	if cmd != nil {
		t.Error("expected nil cmd from Update on enter when store.List() errors")
	}
}

// decideKeyMsg builds a tea.KeyMsg for the given single character, mirroring
// upKeyMsg/downKeyMsg/enterKeyMsg's pattern above but for printable-rune
// keys, which ReviewsTab.Update matches via keyMsg.String() returning that
// exact rune. Bubble Tea reports the shifted form of a letter as its
// uppercase rune, which is what makes "R" distinguishable from "r" — this
// helper exercises that by taking the exact rune to send.
func decideKeyMsg(r rune) tea.KeyMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// TestReviewsTabDecideKeysWithSelection verifies p/r/R/d each emit a
// decideRequestMsg carrying the correct decision string and the selected
// record's repo/pr/spoolPath.
func TestReviewsTabDecideKeysWithSelection(t *testing.T) {
	cases := []struct {
		key          rune
		wantDecision string
	}{
		{key: 'p', wantDecision: "post"},
		{key: 'r', wantDecision: "revise"},
		{key: 'R', wantDecision: "rereview"},
		{key: 'd', wantDecision: "discard"},
	}

	for _, tc := range cases {
		t.Run(string(tc.key), func(t *testing.T) {
			records := []review.Record{
				{Repo: "owner/repo-a", PR: 1, SpoolPath: "/tmp/spool-a.md"},
				{Repo: "owner/repo-b", PR: 2, SpoolPath: "/tmp/spool-b.md"},
			}
			store := &fakeReviewStore{records: records}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())
			_ = rt.View() // seed order; selection -> owner/repo-a#1

			tab, cmd := rt.Update(decideKeyMsg(tc.key))
			rt = tab.(*ReviewsTab)
			if cmd == nil {
				t.Fatalf("key %q: expected non-nil tea.Cmd with a selection", tc.key)
			}

			msg := cmd()
			decideMsg, ok := msg.(decideRequestMsg)
			if !ok {
				t.Fatalf("key %q: expected decideRequestMsg, got %T (%v)", tc.key, msg, msg)
			}
			if decideMsg.repo != "owner/repo-a" || decideMsg.pr != 1 || decideMsg.spoolPath != "/tmp/spool-a.md" {
				t.Errorf("key %q: decideRequestMsg = %+v, want repo=owner/repo-a pr=1 spoolPath=/tmp/spool-a.md", tc.key, decideMsg)
			}
			if decideMsg.decision != tc.wantDecision {
				t.Errorf("key %q: decision = %q, want %q", tc.key, decideMsg.decision, tc.wantDecision)
			}
		})
	}
}

// TestReviewsTabDecideKeysWithNoSelectionReturnsNilCmd verifies p/r/R/d on an
// empty store (no selection) are no-ops, mirroring
// TestReviewsTabEnterWithNoSelectionReturnsNilCmd's pattern for enter.
func TestReviewsTabDecideKeysWithNoSelectionReturnsNilCmd(t *testing.T) {
	for _, key := range []rune{'p', 'r', 'R', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			store := &fakeReviewStore{records: nil}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())
			_ = rt.View() // seed; selection stays empty

			_, cmd := rt.Update(decideKeyMsg(key))
			if cmd != nil {
				t.Errorf("key %q: expected nil cmd with no selection", key)
			}
		})
	}
}

// TestReviewsTabDecideKeysWithStoreListErrorReturnsNilCmd verifies p/r/R/d
// degrade to a no-op when store.List() errors at keypress time, mirroring
// TestReviewsTabEnterWithStoreListErrorReturnsNilCmd's pattern for enter.
func TestReviewsTabDecideKeysWithStoreListErrorReturnsNilCmd(t *testing.T) {
	for _, key := range []rune{'p', 'r', 'R', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			records := []review.Record{
				{Repo: "owner/repo-a", PR: 1, SpoolPath: "/tmp/spool-a.md"},
			}
			store := &fakeReviewStore{records: records}
			rt := NewReviewsTab("reviews", store, testReviewsStyles())
			_ = rt.View() // seed selection while List() still succeeds

			store.records = nil
			store.listErr = errors.New("disk fell over")

			_, cmd := rt.Update(decideKeyMsg(key))
			if cmd != nil {
				t.Errorf("key %q: expected nil cmd when store.List() errors", key)
			}
		})
	}
}

// TestReviewsTabDecideKeyCaseSensitivity is an explicit regression test for
// the one part of this design most likely to silently misbehave: confirming
// lowercase "r" routes to "revise" and uppercase "R" routes to "rereview",
// rather than assuming tea.KeyMsg.String()'s shift handling works as
// expected.
func TestReviewsTabDecideKeyCaseSensitivity(t *testing.T) {
	records := []review.Record{
		{Repo: "owner/repo-a", PR: 1, SpoolPath: "/tmp/spool-a.md"},
	}

	t.Run("lowercase r routes to revise", func(t *testing.T) {
		store := &fakeReviewStore{records: records}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

		_, cmd := rt.Update(decideKeyMsg('r'))
		if cmd == nil {
			t.Fatal("expected non-nil cmd")
		}
		msg := cmd().(decideRequestMsg)
		if msg.decision != "revise" {
			t.Errorf("lowercase 'r' decision = %q, want %q", msg.decision, "revise")
		}
	})

	t.Run("uppercase R routes to rereview", func(t *testing.T) {
		store := &fakeReviewStore{records: records}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

		_, cmd := rt.Update(decideKeyMsg('R'))
		if cmd == nil {
			t.Fatal("expected non-nil cmd")
		}
		msg := cmd().(decideRequestMsg)
		if msg.decision != "rereview" {
			t.Errorf("uppercase 'R' decision = %q, want %q", msg.decision, "rereview")
		}
	})
}

// TestReviewsTabDecideSelectionFollowsPRAcrossResort mirrors
// TestReviewsTabSelectionFollowsPRAcrossResort: after selecting a row and
// then re-sorting the underlying store, pressing a decide key must still
// target the originally-selected PR, not whatever now occupies its old row
// index.
func TestReviewsTabDecideSelectionFollowsPRAcrossResort(t *testing.T) {
	store := &fakeReviewStore{records: []review.Record{
		{Repo: "foo/bar", PR: 10, Status: review.StatusWatching, SpoolPath: "/tmp/bar.md"},
		{Repo: "foo/baz", PR: 20, Status: review.StatusWatching, SpoolPath: "/tmp/baz.md"},
	}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	_ = rt.View() // seed order: [foo/bar#10, foo/baz#20], selection -> foo/bar#10

	// Select the second row (foo/baz#20).
	rt.moveCursor(1)
	if rt.SelectedKey() != "foo/baz#20" {
		t.Fatalf("SelectedKey() = %q, want foo/baz#20", rt.SelectedKey())
	}

	// A new PR enrolls that sorts to the TOP (aaa/zzz < foo/*).
	store.records = append(store.records, review.Record{Repo: "aaa/zzz", PR: 1, Status: review.StatusWatching, SpoolPath: "/tmp/zzz.md"})
	_ = rt.View() // re-render, re-sorts, selection must still follow foo/baz#20

	_, cmd := rt.Update(decideKeyMsg('p'))
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd().(decideRequestMsg)
	if msg.repo != "foo/baz" || msg.pr != 20 || msg.spoolPath != "/tmp/baz.md" {
		t.Errorf("decideRequestMsg = %+v, want repo=foo/baz pr=20 spoolPath=/tmp/baz.md (selection must follow its PR across resort)", msg)
	}
}
