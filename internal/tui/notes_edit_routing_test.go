package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newNotesEditRoutingTestModel builds a model with everything model.Update
// needs for the "n" key / notes-edit-mode interception routing tests: a
// real AutocompleteInput (so footer-focus routing behaves exactly as it
// does in the running app), a real NotesInput/NotesWriter (so
// notesEditActive's interception branch can dereference m.notesInput
// without a nil-pointer panic), a TabManager, styles, and a
// DecisionWriter — mirroring newFullDecideRoutingTestModel's shape
// (decide_routing_test.go) plus the notes-editing fields Task 1 added to
// model.
func newNotesEditRoutingTestModel() model {
	registry := NewCommandRegistry(nil)
	styles := newTestStyles()
	input := NewAutocompleteInput(registry, styles)
	tabManager := NewTabManager()
	return model{
		activityLines:  []string{},
		styles:         styles,
		input:          input,
		tabManager:     tabManager,
		decisionWriter: review.NewDecisionWriter(),
		notesInput:     NewNotesInput(),
		notesWriter:    review.NewNotesWriter(),
		activeOverlay:  overlayNone,
		tabFocusStates: make(map[string]FocusTarget),
		footerManager:  NewFooterManager(styles, nil, nil, input, tabManager),
	}
}

// writeFakeSpoolWithDecision writes a minimal spool file with the given
// decision: front-matter value (and an empty decision_notes: field,
// matching set-review-decision_test.sh's make_valid_spool fixture) to a
// temp directory, returning its absolute path. Used to exercise
// startNotesEditCmd's gating check against a real file, the same way
// spoolInfoForFunc (review.ReadSpoolInfo) resolves it in the running app —
// no fake exec script is needed here since the gating check only reads the
// spool file, it never shells out.
func writeFakeSpoolWithDecision(t *testing.T, decision string) string {
	t.Helper()
	dir := t.TempDir()
	pendingDir := filepath.Join(dir, "PR-Review", "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to create fake pending dir: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo.md")
	content := "---\ndecision: " + decision + "\ndecision_notes: \n---\nbody\n"
	if err := os.WriteFile(spoolPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fake spool: %v", err)
	}
	return spoolPath
}

// TestNKey_GatePasses_EntersNotesEditMode proves that pressing "n" on a row
// whose decision is "revise" or "rereview" (any casing, matching
// ClassifySpoolState's strings.ToLower convention) enters notes-edit mode:
// m.notesEditActive becomes true and the textinput is focused.
func TestNKey_GatePasses_EntersNotesEditMode(t *testing.T) {
	cases := []string{"revise", "rereview", "REVISE", "ReReview"}
	for _, decision := range cases {
		t.Run(decision, func(t *testing.T) {
			spoolPath := writeFakeSpoolWithDecision(t, decision)
			m := newNotesEditRoutingTestModel()
			addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 42, SpoolPath: spoolPath})

			reviewsIdx := m.findReviewsTabIndex()
			if reviewsIdx < 0 {
				t.Fatalf("expected a Reviews tab to be present")
			}
			m, _ = m.switchActiveTab(reviewsIdx)
			if m.input.Focused() {
				t.Fatalf("expected footer to be unfocused after switching to the Reviews tab")
			}

			result, cmd := m.Update(pressKey('n'))
			resultModel, ok := result.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}
			if cmd == nil {
				t.Fatalf("expected a tea.Cmd emitting startNotesEditMsg from ReviewsTab.Update, got nil")
			}

			msg := cmd()
			if _, ok := msg.(startNotesEditMsg); !ok {
				t.Fatalf("expected startNotesEditMsg, got %T (%v)", msg, msg)
			}

			finalResult, _ := resultModel.Update(msg)
			finalModel, ok := finalResult.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}
			if !finalModel.notesEditActive {
				t.Errorf("expected notesEditActive to be true after startNotesEditMsg")
			}
			if !finalModel.notesInput.Focused() {
				t.Errorf("expected notesInput to be focused after startNotesEditMsg")
			}
			if finalModel.notesEditTarget.PR != 42 {
				t.Errorf("expected notesEditTarget.PR == 42, got %d", finalModel.notesEditTarget.PR)
			}
		})
	}
}

// TestNKey_GateFails_ProducesActivityLineError proves that pressing "n" on
// a row whose decision is anything other than revise/rereview (including
// empty) does NOT enter notes-edit mode, and instead produces a visible
// activity-line error explaining why.
func TestNKey_GateFails_ProducesActivityLineError(t *testing.T) {
	cases := []string{"", "post", "discard", "bogus"}
	for _, decision := range cases {
		name := decision
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			spoolPath := writeFakeSpoolWithDecision(t, decision)
			m := newNotesEditRoutingTestModel()
			addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 7, SpoolPath: spoolPath})

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
				t.Fatalf("expected a tea.Cmd emitting gateFailedMsg, got nil")
			}

			msg := cmd()
			gateMsg, ok := msg.(gateFailedMsg)
			if !ok {
				t.Fatalf("expected gateFailedMsg, got %T (%v)", msg, msg)
			}

			finalResult, finalCmd := resultModel.Update(gateMsg)
			if finalCmd != nil {
				t.Errorf("expected nil cmd from gateFailedMsg handling, got %v", finalCmd)
			}
			finalModel, ok := finalResult.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}

			if finalModel.notesEditActive {
				t.Errorf("expected notesEditActive to remain false when the gate fails")
			}
			if len(finalModel.activityLines) == 0 {
				t.Fatalf("expected a visible activity-line error, got none")
			}
			if !anyLineContains(finalModel.activityLines, "revise or rereview") {
				t.Errorf("expected activity line explaining the gate failure, got: %v", finalModel.activityLines)
			}
		})
	}
}

// TestNKey_NoSelection_SilentNoOp proves pressing "n" with no row selected
// is a silent no-op, matching decideSelectedCmd's existing convention for
// the same condition (distinct from the visible gateFailedMsg error a
// selected-but-disqualified row produces).
func TestNKey_NoSelection_SilentNoOp(t *testing.T) {
	m := newNotesEditRoutingTestModel()
	store := &fakeReviewStore{records: []review.Record{}}
	rt := NewReviewsTab("reviews", store, testReviewsStyles())
	m.tabManager.AddTab(rt)

	reviewsIdx := m.findReviewsTabIndex()
	if reviewsIdx < 0 {
		t.Fatalf("expected a Reviews tab to be present")
	}
	m, _ = m.switchActiveTab(reviewsIdx)

	result, cmd := m.Update(pressKey('n'))
	if cmd != nil {
		t.Errorf("expected nil cmd when nothing is selected, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if resultModel.notesEditActive {
		t.Errorf("expected notesEditActive to remain false")
	}
	if len(resultModel.activityLines) != 0 {
		t.Errorf("expected no activity line for a silent no-op, got: %v", resultModel.activityLines)
	}
}

// TestNotesEditMode_InterceptsBeforeRowShortcuts is the interception-
// ordering regression test the spec calls for (mirroring decide_routing_
// test.go's AC5a pattern): once notesEditActive is true, printable keys
// that would otherwise be row-shortcuts (p/r/R/d) or row-navigation
// (up/down) must be consumed by the notes textinput instead — the
// interception must run before the Reviews-tab forwarding block.
func TestNotesEditMode_InterceptsBeforeRowShortcuts(t *testing.T) {
	spoolPath := writeFakeSpoolWithDecision(t, "revise")
	m := newNotesEditRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 5, SpoolPath: spoolPath})

	reviewsIdx := m.findReviewsTabIndex()
	if reviewsIdx < 0 {
		t.Fatalf("expected a Reviews tab to be present")
	}
	m, _ = m.switchActiveTab(reviewsIdx)

	// Enter notes-edit mode directly via the message (bypassing the "n" key
	// itself, which is covered by TestNKey_GatePasses_EntersNotesEditMode).
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 5, spoolPath: spoolPath, currentNotes: "existing"})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if !m.notesEditActive {
		t.Fatalf("test setup broken: expected notesEditActive to be true")
	}

	for _, key := range []rune{'p', 'r', 'R', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			before := m.notesInput.Value()
			result, cmd := m.Update(pressKey(key))
			resultModel, ok := result.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}

			// The key must reach the textinput, not trigger a decision: no
			// tea.Cmd emitting decideRequestMsg, and the textinput's value
			// must have grown by the pressed key.
			if cmd != nil {
				if _, isDecide := cmd().(decideRequestMsg); isDecide {
					t.Errorf("expected key %q NOT to trigger a decision while notesEditActive, but got decideRequestMsg", string(key))
				}
			}
			want := before + string(key)
			if resultModel.notesInput.Value() != want {
				t.Errorf("expected notesInput value %q, got %q — key %q did not reach the textinput", want, resultModel.notesInput.Value(), string(key))
			}
			if !resultModel.notesEditActive {
				t.Errorf("expected notesEditActive to remain true")
			}
		})
	}

	// up/down must not navigate rows either — they are forwarded to the
	// textinput like any other non-enter/esc key (textinput.Update is a
	// no-op for arrow keys with no matching internal behavior, but the
	// important assertion is that notesEditActive stays true and no row
	// navigation/decision side effect occurs).
	for _, key := range []string{"up", "down"} {
		t.Run(key, func(t *testing.T) {
			result, _ := m.Update(tea.KeyPressMsg{Text: key})
			resultModel, ok := result.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}
			if !resultModel.notesEditActive {
				t.Errorf("expected notesEditActive to remain true for key %q", key)
			}
		})
	}
}

// TestNotesEditMode_Esc_CancelsWithoutWriting proves pressing Esc while
// notesEditActive discards the typed value, attempts no write, blurs the
// textinput, clears the footer transient message, and appends an info
// activity line ("Notes edit cancelled") — matching the design spec's
// Task 3 acceptance criteria exactly. The spool file on disk is asserted
// unchanged, which is the strongest possible proof that no write was
// attempted (no fake exec seam is available across the tui/review package
// boundary, so "no write attempted" is verified by outcome instead of by
// call count).
func TestNotesEditMode_Esc_CancelsWithoutWriting(t *testing.T) {
	origWd := chdirToRepoRootForNotesEdit(t)
	defer restoreWd(t, origWd)

	spoolPath := newTempSpoolForNotesEdit(t)
	before, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to read spool file: %v", err)
	}

	m := newNotesEditRoutingTestModel()
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: spoolPath, currentNotes: "hello"})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if m.footerManager != nil {
		m.footerManager.SetTransientMessage("Editing notes for PR #9")
	}

	// Type an edit that must never be persisted.
	result, _ = m.Update(pressKey('!'))
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if m.notesInput.Value() != "hello!" {
		t.Fatalf("test setup broken: expected typed value 'hello!', got %q", m.notesInput.Value())
	}

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		t.Errorf("expected nil cmd on Esc, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.notesEditActive {
		t.Errorf("expected notesEditActive to be false after Esc")
	}
	if resultModel.notesInput.Focused() {
		t.Errorf("expected notesInput to be blurred after Esc")
	}
	after, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to re-read spool file: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("expected spool file to be completely untouched by Esc, before:\n%s\nafter:\n%s", before, after)
	}
	if !anyLineContains(resultModel.activityLines, "Notes edit cancelled") {
		t.Errorf("expected an activity line announcing the cancellation, got: %v", resultModel.activityLines)
	}
}

// TestNotesEditMode_EscThenReopen_ShowsOriginalValue proves that
// re-entering notes-edit mode after an Esc-cancelled edit shows the
// original, unmodified decision_notes value — not the discarded edit —
// since Esc never writes anything and re-opening re-reads from the spool
// file's actual current value (simulated here via a fresh
// startNotesEditMsg, mirroring how ReviewsTab.Update re-resolves
// decision_notes on every "n" press).
func TestNotesEditMode_EscThenReopen_ShowsOriginalValue(t *testing.T) {
	m := newNotesEditRoutingTestModel()
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: "/tmp/spool.md", currentNotes: "original value"})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	result, _ = m.Update(pressKey('X'))
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if m.notesInput.Value() == "original value" {
		t.Fatalf("test setup broken: expected the typed edit to change the value")
	}

	result, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	// Re-open notes-edit for the same PR — the emitter re-reads
	// decision_notes from disk, so it is unaffected by the discarded edit.
	result, _ = m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: "/tmp/spool.md", currentNotes: "original value"})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.notesInput.Value() != "original value" {
		t.Errorf("expected re-opened notesInput to show the original unmodified value, got %q", resultModel.notesInput.Value())
	}
}

// TestNotesEditMode_Enter_SavesAndExitsEditMode proves pressing Enter
// reads the typed value, calls NotesWriter.SetNotes with it (via the real
// set-review-notes.sh script against a real temp spool file), and on
// success exits edit mode, blurs, clears the transient message, and shows
// a success activity line.
func TestNotesEditMode_Enter_SavesAndExitsEditMode(t *testing.T) {
	origWd := chdirToRepoRootForNotesEdit(t)
	defer restoreWd(t, origWd)

	spoolPath := newTempSpoolForNotesEdit(t)

	m := newNotesEditRoutingTestModel()
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: spoolPath, currentNotes: ""})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if m.footerManager != nil {
		m.footerManager.SetTransientMessage("Editing notes for PR #9")
	}

	result, _ = m.Update(pressKey('o'))
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	result, _ = m.Update(pressKey('k'))
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected nil cmd on Enter, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.notesEditActive {
		t.Errorf("expected notesEditActive to be false after a successful save; activity lines: %v", resultModel.activityLines)
	}
	if resultModel.notesInput.Focused() {
		t.Errorf("expected notesInput to be blurred after a successful save")
	}
	updated, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to read updated spool file: %v", err)
	}
	if !strings.Contains(string(updated), "decision_notes: ok") {
		t.Errorf("expected the spool file's decision_notes to be updated to 'ok', got:\n%s", updated)
	}
	if !anyLineContains(resultModel.activityLines, "Saved decision notes for PR #9") {
		t.Errorf("expected a success activity line, got: %v", resultModel.activityLines)
	}
}

// TestNotesEditMode_Enter_FailureKeepsEditModeAndTypedValue proves that a
// NotesWriter.SetNotes failure leaves notesEditActive true and the typed
// value intact in the textinput — a failed write must not silently
// discard user input. The failure is induced with a real, already-
// finalized (done/) spool file, exercising NotesWriter's own "already
// finalized" error path rather than a fake seam.
func TestNotesEditMode_Enter_FailureKeepsEditModeAndTypedValue(t *testing.T) {
	origWd := chdirToRepoRootForNotesEdit(t)
	defer restoreWd(t, origWd)

	base := t.TempDir()
	doneDir := filepath.Join(base, "done")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := "pr-review-owner-repo-9.md"
	if err := os.WriteFile(filepath.Join(doneDir, filename), []byte("---\ndecision: post\ndecision_notes: \"\"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(base, "pending", filename)

	m := newNotesEditRoutingTestModel()
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: pendingPath, currentNotes: ""})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	result, _ = m.Update(pressKey('x'))
	m, ok = result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected nil cmd on a failed Enter save, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if !resultModel.notesEditActive {
		t.Errorf("expected notesEditActive to remain true after a failed save")
	}
	if resultModel.notesInput.Value() != "x" {
		t.Errorf("expected the typed value to remain intact after a failed save, got %q", resultModel.notesInput.Value())
	}
	if !anyLineContains(resultModel.activityLines, "Failed to save decision notes") {
		t.Errorf("expected an error activity line, got: %v", resultModel.activityLines)
	}
}

// TestNotesEditMode_Enter_RealScriptRoundTrip is an integration test using
// the real set-review-notes.sh script against a real temp spool file,
// confirming special characters (colon, ampersand, slash, single-quote)
// round-trip correctly through NotesWriter.SetNotes end-to-end and every
// other front-matter field is byte-for-byte unchanged.
func TestNotesEditMode_Enter_RealScriptRoundTrip(t *testing.T) {
	origWd := chdirToRepoRootForNotesEdit(t)
	defer restoreWd(t, origWd)

	dir := t.TempDir()
	pendingDir := filepath.Join(dir, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to create pending dir: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-9.md")
	original := "---\nrepo: owner/repo\npr: 9\ndecision: revise\ndecision_notes: \"\"\nverdict: APPROVE\n---\n\n# Review body\n"
	if err := os.WriteFile(spoolPath, []byte(original), 0o644); err != nil {
		t.Fatalf("failed to write spool file: %v", err)
	}

	notesValue := "see docs at https://example.com/a & fix it's issue: now"

	m := newNotesEditRoutingTestModel()
	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 9, spoolPath: spoolPath, currentNotes: ""})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	m.notesInput.SetValue(notesValue)

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected nil cmd on Enter, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.notesEditActive {
		t.Fatalf("expected notesEditActive to be false after a successful real-script save; activity lines: %v", resultModel.activityLines)
	}

	updated, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to read updated spool file: %v", err)
	}
	updatedStr := string(updated)
	if !strings.Contains(updatedStr, "decision_notes: "+notesValue) {
		t.Errorf("expected decision_notes to contain the round-tripped value verbatim, got:\n%s", updatedStr)
	}
	for _, field := range []string{"repo: owner/repo", "pr: 9", "decision: revise", "verdict: APPROVE", "# Review body"} {
		if !strings.Contains(updatedStr, field) {
			t.Errorf("expected front-matter field %q to survive unchanged, got:\n%s", field, updatedStr)
		}
	}
}

// newTempSpoolForNotesEdit creates a real spool file under a temp
// pending/ dir so resolveSpoolPath (internal/review) finds it, returning
// its absolute path.
func newTempSpoolForNotesEdit(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create pending dir: %v", err)
	}
	path := filepath.Join(dir, "pr-review-owner-repo-9.md")
	if err := os.WriteFile(path, []byte("---\ndecision: revise\ndecision_notes: \"\"\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("failed to write spool file: %v", err)
	}
	return path
}

// chdirToRepoRootForNotesEdit locates the repository root (the directory
// containing .howmux/scripts/set-review-notes.sh) by walking up from the
// current working directory and chdirs into it, so NotesWriter.SetNotes's
// CWD-relative script resolution (mirroring DecisionWriter's) finds the
// real script during these integration tests. Returns the original
// working directory for restoreWd to chdir back to.
func chdirToRepoRootForNotesEdit(t *testing.T) string {
	t.Helper()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	dir := origWd
	for {
		candidate := filepath.Join(dir, ".howmux", "scripts", "set-review-notes.sh")
		if _, statErr := os.Stat(candidate); statErr == nil {
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("failed to chdir to repo root %s: %v", dir, err)
			}
			return origWd
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root containing .howmux/scripts/set-review-notes.sh above %s", origWd)
		}
		dir = parent
	}
}

// restoreWd chdirs back to origWd, for use with chdirToRepoRootForNotesEdit
// via defer.
func restoreWd(t *testing.T, origWd string) {
	t.Helper()
	if err := os.Chdir(origWd); err != nil {
		t.Fatalf("failed to restore working directory to %s: %v", origWd, err)
	}
}

// TestNotesEditMode_TakesPriorityOverFooterFocus proves the third-focus-
// state priority ordering from the design spec: notes-edit mode intercepts
// keys even when the footer (m.input) is focused — a higher-priority state
// than the existing footer-vs-row-shortcut duality AC5a covers.
func TestNotesEditMode_TakesPriorityOverFooterFocus(t *testing.T) {
	m := newNotesEditRoutingTestModel()
	m.input.SetFocus(true)

	result, _ := m.Update(startNotesEditMsg{repo: "owner/repo", pr: 11, spoolPath: "/tmp/spool.md", currentNotes: ""})
	m, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	footerValueBefore := m.input.Value()
	result, _ = m.Update(pressKey('p'))
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.input.Value() != footerValueBefore {
		t.Errorf("expected footer value unchanged (key routed to notesInput instead), got %q (want %q)", resultModel.input.Value(), footerValueBefore)
	}
	if resultModel.notesInput.Value() != "p" {
		t.Errorf("expected notesInput to receive the key, got value %q", resultModel.notesInput.Value())
	}
}
