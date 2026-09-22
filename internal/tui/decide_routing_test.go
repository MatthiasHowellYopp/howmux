package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newDecideRoutingTestModel builds a model with everything model.Update
// needs for the decideRequestMsg / AC5a routing tests: a real
// AutocompleteInput (so Focused()/Value() behave exactly as they do in the
// running app), a TabManager, styles, and a DecisionWriter. Distinct from
// newDecideTestModel (commands_decide_test.go), which omits m.input since
// handleDecide never touches it — these tests specifically exercise
// model.Update's footer-focus routing, so a real AutocompleteInput is
// required.
func newDecideRoutingTestModel() model {
	registry := NewCommandRegistry(nil)
	styles := newTestStyles()
	return model{
		activityLines:  []string{},
		styles:         styles,
		input:          NewAutocompleteInput(registry, styles),
		tabManager:     NewTabManager(),
		decisionWriter: review.NewDecisionWriter(),
		activeOverlay:  overlayNone,
	}
}

// pressKey builds a tea.KeyPressMsg for a single printable rune, matching
// how the codebase's own case-sensitivity test (reviews_tab_test.go) builds
// fixtures reproducing real-terminal shift behavior: Text carries the exact
// rune (upper or lower), which is what keyMsg.String() reports back.
func pressKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// newFullDecideRoutingTestModel builds a model with everything
// switchActiveTab needs in addition to what newDecideRoutingTestModel
// provides: tabFocusStates and footerManager. newDecideRoutingTestModel
// (above) omits both, so calling the real model.switchActiveTab on it
// panics (nil map write, nil pointer deref on
// footerManager.GetContextTracker()) — exactly the gap the validator
// identified: every pre-existing test proved p/r/R/d "work" via
// ReviewsTab.Update directly or by manually forcing
// m.input.SetFocus(false), never via the real switchActiveTab routing path.
// This helper closes that gap by initializing both fields, matching what
// newModel (tui.go) does in the real app, so tests built on it can drive
// switchActiveTab itself and observe the real, end-to-end routing
// consequences (issue #85 qa-attempt:2 AC2 fix).
func newFullDecideRoutingTestModel() model {
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
		activeOverlay:  overlayNone,
		tabFocusStates: make(map[string]FocusTarget),
		footerManager:  NewFooterManager(styles, nil, nil, input, tabManager),
	}
}

// TestSwitchActiveTab_ReviewsTab_KeypressReachesDecideSelectedCmd is the
// missing regression test the validator identified: it drives the REAL
// model.switchActiveTab into the Reviews tab (not a manual
// m.input.SetFocus(false) call, which every pre-existing test used instead)
// and then a real "p"/"d" keypress through model.Update, asserting the
// decision was actually applied. Before the AC2 fix, switchActiveTab always
// left the footer focused on the Reviews tab (CaptureFocusState reported
// FocusTargetFooter unconditionally), so this exact sequence would have
// typed into the footer instead of triggering ReviewsTab.decideSelectedCmd —
// this test fails without the fix and passes with it.
func TestSwitchActiveTab_ReviewsTab_KeypressReachesDecideSelectedCmd(t *testing.T) {
	t.Run("p", func(t *testing.T) {
		spoolPath := withRoutingFakeDecisionScript(t)
		m := newFullDecideRoutingTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 42, SpoolPath: spoolPath})

		reviewsIdx := m.findReviewsTabIndex()
		if reviewsIdx < 0 {
			t.Fatalf("expected a Reviews tab to be present")
		}

		m, switchCmd := m.switchActiveTab(reviewsIdx)
		if switchCmd != nil {
			_ = switchCmd()
		}
		if m.input.Focused() {
			t.Fatalf("expected footer to be unfocused after switching to the Reviews tab (AC2 fix), but it was focused")
		}

		// Real keypress through the real model.Update routing path.
		result, cmd := m.Update(pressKey('p'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting decideRequestMsg from ReviewsTab.Update, got nil — keypress did not reach the tab")
		}

		// The cmd emits decideRequestMsg; drive it through Update once more
		// (mirroring the real Bubble Tea event loop). "post" opens the
		// inline confirm gate rather than writing/launching anything yet.
		msg := cmd()
		finalResult, finalCmd := resultModel.Update(msg)
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideRequestMsg handling for 'post' (opens confirm gate synchronously), got %v", finalCmd)
		}
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if finalModel.decidePostConfirmState != decidePostConfirmAwaiting {
			t.Fatalf("expected decidePostConfirmAwaiting after routing 'p' through the tab, got %v", finalModel.decidePostConfirmState)
		}
		if !anyLineContains(finalModel.activityLines, "to owner/repo#42?") {
			t.Errorf("expected confirm-prompt activity line, got: %v", finalModel.activityLines)
		}
	})

	t.Run("d", func(t *testing.T) {
		spoolPath := withRoutingFakeDecisionScript(t)
		m := newFullDecideRoutingTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 42, SpoolPath: spoolPath})

		reviewsIdx := m.findReviewsTabIndex()
		if reviewsIdx < 0 {
			t.Fatalf("expected a Reviews tab to be present")
		}

		m, switchCmd := m.switchActiveTab(reviewsIdx)
		if switchCmd != nil {
			_ = switchCmd()
		}
		if m.input.Focused() {
			t.Fatalf("expected footer to be unfocused after switching to the Reviews tab (AC2 fix), but it was focused")
		}

		result, cmd := m.Update(pressKey('d'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting decideRequestMsg from ReviewsTab.Update, got nil — keypress did not reach the tab")
		}

		// decideRequestMsg -> dispatchDecideAction("discard") -> a tea.Cmd
		// that (once invoked) reports decideDiscardCompleteMsg.
		msg := cmd()
		finalResult, finalCmd := resultModel.Update(msg)
		discardCmd := finalCmd
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if discardCmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the discard launch, got nil")
		}

		completeMsg := drainBatchForType[decideDiscardCompleteMsg](t, discardCmd)
		afterDiscard, afterCmd := finalModel.Update(completeMsg)
		if afterCmd != nil {
			t.Errorf("expected nil cmd from decideDiscardCompleteMsg handling, got %v", afterCmd)
		}
		afterModel, ok := afterDiscard.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if !anyLineContains(afterModel.activityLines, "Discarded review for owner/repo#42 — archived to done/.") {
			t.Errorf("expected success activity line for discard, got: %v", afterModel.activityLines)
		}
	})
}

// TestSwitchActiveTab_ReviewsTab_EnterReachesOpenSelectedReviewCmd proves the
// pre-existing enter-to-open-review-content behavior (from issues #83/#84),
// which shares the exact same root cause as the AC2 bug, is also fixed by
// the same change: after a real switchActiveTab into the Reviews tab, a
// real "enter" keypress through model.Update must reach
// ReviewsTab.openSelectedReviewCmd (observable via the openReviewContentMsg
// it emits) rather than being swallowed by the "footer is focused, execute
// as a command" branch of the "enter" case in model.Update.
func TestSwitchActiveTab_ReviewsTab_EnterReachesOpenSelectedReviewCmd(t *testing.T) {
	m := newFullDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 77, SpoolPath: "/tmp/spool.md"})

	reviewsIdx := m.findReviewsTabIndex()
	if reviewsIdx < 0 {
		t.Fatalf("expected a Reviews tab to be present")
	}

	m, _ = m.switchActiveTab(reviewsIdx)
	if m.input.Focused() {
		t.Fatalf("expected footer to be unfocused after switching to the Reviews tab (AC2 fix), but it was focused")
	}

	result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, ok := result.(model); !ok {
		t.Fatalf("Update did not return a model")
	}
	if cmd == nil {
		t.Fatalf("expected a tea.Cmd emitting openReviewContentMsg from ReviewsTab.Update's enter case, got nil — enter did not reach the tab")
	}

	msg := cmd()
	opened, ok := msg.(openReviewContentMsg)
	if !ok {
		t.Fatalf("expected openReviewContentMsg, got %T (%v)", msg, msg)
	}
	if opened.repo != "owner/repo" || opened.pr != 77 {
		t.Errorf("expected openReviewContentMsg for owner/repo#77, got %+v", opened)
	}
}

// TestToggleReviewsFocus_UserCanStillFocusFooterAndRunDecideCommand proves
// AC1 (typing `decide post` via the footer/REPL) still works after the AC2
// fix: the user can explicitly move focus from row-navigation mode to the
// footer on the Reviews tab (via the Tab key, mirroring
// togglePlanningFocus's discoverable toggle for planning tabs) and then
// type and execute a `decide post` command exactly as before.
func TestToggleReviewsFocus_UserCanStillFocusFooterAndRunDecideCommand(t *testing.T) {
	spoolPath := withRoutingFakeDecisionScript(t)
	m := newFullDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 9, SpoolPath: spoolPath})

	reviewsIdx := m.findReviewsTabIndex()
	if reviewsIdx < 0 {
		t.Fatalf("expected a Reviews tab to be present")
	}

	m, _ = m.switchActiveTab(reviewsIdx)
	if m.input.Focused() {
		t.Fatalf("expected footer to be unfocused by default on the Reviews tab, but it was focused")
	}

	// Tab toggles focus to the footer (mirrors togglePlanningFocus's pattern
	// for TabTypePlanning, applied here to TabTypeReviews).
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if !resultModel.input.Focused() {
		t.Fatalf("expected Tab to focus the footer on the Reviews tab, but it did not")
	}

	// Type "decide post" into the now-focused footer.
	m2 := resultModel
	for _, r := range "decide post" {
		res, _ := m2.Update(pressKey(r))
		m2, ok = res.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
	}
	if got := m2.input.Value(); got != "decide post" {
		t.Fatalf("expected footer value %q, got %q", "decide post", got)
	}

	// Enter executes it as a command, since the footer is focused. "decide
	// post" opens the inline confirm gate rather than writing/launching
	// anything yet (issue #109 — post is no longer write-only).
	finalResult, _ := m2.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	finalModel, ok := finalResult.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if finalModel.decidePostConfirmState != decidePostConfirmAwaiting {
		t.Fatalf("expected decidePostConfirmAwaiting after AC1's decide-post command, got %v", finalModel.decidePostConfirmState)
	}
	if !anyLineContains(finalModel.activityLines, "to owner/repo#9?") {
		t.Errorf("expected confirm-prompt activity line for AC1's decide-post command, got: %v", finalModel.activityLines)
	}
}

func TestModelUpdate_DecideRequestMsg_MatchesHandleDecideActivityLine(t *testing.T) {
	// Entry point 1: decideRequestMsg through model.Update.
	mViaMsg := newDecideRoutingTestModel()
	msgResult, cmd := mViaMsg.Update(decideRequestMsg{
		repo:      "owner/repo",
		pr:        21,
		spoolPath: "/tmp/spool.md",
		decision:  "post",
	})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	viaMsgModel, ok := msgResult.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	// Entry point 2: handleDecide with an equivalent resolved record (a
	// Reviews tab with the same record selected).
	mViaHandle := newDecideTestModel()
	addReviewsTabWithRecord(mViaHandle, review.Record{Repo: "owner/repo", PR: 21, SpoolPath: "/tmp/spool.md"})
	viaHandleModel, handleCmd := mViaHandle.handleDecide([]string{"post"})
	if handleCmd != nil {
		t.Errorf("expected nil cmd, got %v", handleCmd)
	}

	if len(viaMsgModel.activityLines) == 0 || len(viaHandleModel.activityLines) == 0 {
		t.Fatalf("expected both entry points to append an activity line; msg=%v handle=%v",
			viaMsgModel.activityLines, viaHandleModel.activityLines)
	}
	msgLine := viaMsgModel.activityLines[len(viaMsgModel.activityLines)-1]
	handleLine := viaHandleModel.activityLines[len(viaHandleModel.activityLines)-1]
	if msgLine != handleLine {
		t.Errorf("expected identical activity-line text from both entry points, got msg=%q handle=%q", msgLine, handleLine)
	}
}

func TestModelUpdate_DecideRequestMsg_EmptySpoolPath(t *testing.T) {
	m := newDecideRoutingTestModel()

	result, cmd := m.Update(decideRequestMsg{
		repo:      "owner/repo",
		pr:        55,
		spoolPath: "",
		decision:  "discard",
	})
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if !anyLineContains(resultModel.activityLines, "PR #55 has no review yet — nothing to decide") {
		t.Errorf("expected 'no review yet' error line, got: %v", resultModel.activityLines)
	}
	// Confirm nothing else was appended (i.e. applyDecision/SetDecision was
	// never invoked) — the empty-spool-path branch must return before ever
	// reaching the writer, so exactly one activity line should exist.
	if len(resultModel.activityLines) != 1 {
		t.Errorf("expected exactly one activity line (no script invocation side effects), got: %v", resultModel.activityLines)
	}
}

// withRoutingFakeDecisionScript is the routing-test analog of
// withFakeDecisionScript (commands_decide_test.go) — same rationale
// (execCommandFunc/scriptPathFunc are private to internal/review, so the
// real filesystem path is the only available seam from this package).
func withRoutingFakeDecisionScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, ".howmux", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("failed to create fake scripts dir: %v", err)
	}
	scriptPath := filepath.Join(scriptsDir, "set-review-decision.sh")
	script := "#!/bin/bash\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake script: %v", err)
	}
	// SetDecision resolves+stats the spool file before shelling out; provide
	// a real pending/ spool and return its path for use as a record SpoolPath.
	pendingDir := filepath.Join(dir, "PR-Review", "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to create fake pending dir: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo.md")
	if err := os.WriteFile(spoolPath, []byte("---\ndecision:\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("failed to write fake spool: %v", err)
	}
	t.Chdir(dir)
	return spoolPath
}

// TestAC5a_FooterFocused_ReviewsTabActive_PrintableKeyTypesIntoInput is the
// core AC5a regression test: with the footer focused and the Reviews tab
// active, a bare "p" keypress must type into the footer input, not trigger
// a decision.
func TestAC5a_FooterFocused_ReviewsTabActive_PrintableKeyTypesIntoInput(t *testing.T) {
	for _, key := range []rune{'p', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			withRoutingFakeDecisionScript(t)
			m := newDecideRoutingTestModel()
			addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 3, SpoolPath: "/tmp/spool.md"})
			m.input.SetFocus(true)
			if !m.input.Focused() {
				t.Fatalf("test setup broken: input should be focused")
			}
			beforeValue := m.input.Value()

			result, _ := m.Update(pressKey(key))
			resultModel, ok := result.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}

			wantValue := beforeValue + string(key)
			if resultModel.input.Value() != wantValue {
				t.Errorf("expected input value to grow by %q, got %q (want %q)", string(key), resultModel.input.Value(), wantValue)
			}
			if len(resultModel.activityLines) != 0 {
				t.Errorf("expected no activity line to be appended when footer is focused, got: %v", resultModel.activityLines)
			}
		})
	}
}

// TestAC5a_FooterUnfocused_ReviewsTabActive_KeyTriggersDecision is the
// counterpart: with the footer unfocused and the Reviews tab active (and a
// row selected), the same keypress must reach ReviewsTab.Update's p/r/R/d
// cases and trigger a decision — proving the guard only suppresses
// forwarding when the footer actually has focus, not unconditionally.
func TestAC5a_FooterUnfocused_ReviewsTabActive_KeyTriggersDecision(t *testing.T) {
	t.Run("p", func(t *testing.T) {
		spoolPath := withRoutingFakeDecisionScript(t)
		m := newDecideRoutingTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 8, SpoolPath: spoolPath})
		m.input.SetFocus(false)
		if m.input.Focused() {
			t.Fatalf("test setup broken: input should not be focused")
		}
		beforeValue := m.input.Value()

		result, cmd := m.Update(pressKey('p'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting decideRequestMsg, got nil")
		}
		msg := cmd()
		finalResult, finalCmd := resultModel.Update(msg)
		// "post" opens the confirm gate synchronously (no launch cmd yet).
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideRequestMsg handling for 'post', got %v", finalCmd)
		}
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if finalModel.decidePostConfirmState != decidePostConfirmAwaiting {
			t.Fatalf("expected decidePostConfirmAwaiting, got %v", finalModel.decidePostConfirmState)
		}
		if !anyLineContains(finalModel.activityLines, "to owner/repo#8?") {
			t.Errorf("expected confirm-prompt activity line, got: %v", finalModel.activityLines)
		}
		if finalModel.input.Value() != beforeValue {
			t.Errorf("expected input value unchanged, got %q (want %q)", finalModel.input.Value(), beforeValue)
		}
	})

	t.Run("d", func(t *testing.T) {
		spoolPath := withRoutingFakeDecisionScript(t)
		m := newDecideRoutingTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 8, SpoolPath: spoolPath})
		m.input.SetFocus(false)
		if m.input.Focused() {
			t.Fatalf("test setup broken: input should not be focused")
		}
		beforeValue := m.input.Value()

		result, cmd := m.Update(pressKey('d'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting decideRequestMsg, got nil")
		}
		msg := cmd()
		finalResult, finalCmd := resultModel.Update(msg)
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if finalCmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the discard launch, got nil")
		}

		completeMsg := drainBatchForType[decideDiscardCompleteMsg](t, finalCmd)
		afterResult, afterCmd := finalModel.Update(completeMsg)
		if afterCmd != nil {
			t.Errorf("expected nil cmd from decideDiscardCompleteMsg handling, got %v", afterCmd)
		}
		afterModel, ok := afterResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if !anyLineContains(afterModel.activityLines, "Discarded review for owner/repo#8 — archived to done/.") {
			t.Errorf("expected success activity line for discard, got: %v", afterModel.activityLines)
		}
		if afterModel.input.Value() != beforeValue {
			t.Errorf("expected input value unchanged, got %q (want %q)", afterModel.input.Value(), beforeValue)
		}
	})
}

// newDecideRoutingTestModelWithNotesAndBody extends newDecideRoutingTestModel
// with the notesInput/notesWriter/bodyWriter fields Task 1/4 added to model,
// so tests can drive the "n"/"e" keys' full round trip (gating check through
// to startNotesEditMsg/gateFailedMsg/startBodyEditMsg handling) the same way
// newFullDecideRoutingTestModel extends it with tabFocusStates/footerManager
// for switchActiveTab. The routing decision itself (footer-focus vs.
// tab-forwarding, in model.Update's "default:" arm) never dereferences these
// three fields — only the messages "n"/"e" emit, once handled, do — but
// tests exercising the full round trip (not just the routing gate) need
// them populated to avoid a nil-pointer panic.
func newDecideRoutingTestModelWithNotesAndBody() model {
	m := newDecideRoutingTestModel()
	m.notesInput = NewNotesInput()
	m.notesWriter = review.NewNotesWriter()
	m.bodyWriter = review.NewBodyWriter()
	return m
}

// TestAC5a_FooterFocused_ReviewsTabActive_NKeyTypesIntoInput proves "n" is
// gated identically to "p"/"r"/"R"/"d" by footer focus: with the footer
// focused and the Reviews tab active, a bare "n" keypress must type into
// the footer input, not enter notes-edit mode or emit any Reviews-tab
// message at all.
func TestAC5a_FooterFocused_ReviewsTabActive_NKeyTypesIntoInput(t *testing.T) {
	m := newDecideRoutingTestModelWithNotesAndBody()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 3, SpoolPath: "/tmp/spool.md"})
	m.input.SetFocus(true)
	if !m.input.Focused() {
		t.Fatalf("test setup broken: input should be focused")
	}
	beforeValue := m.input.Value()

	result, cmd := m.Update(pressKey('n'))
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	wantValue := beforeValue + "n"
	if resultModel.input.Value() != wantValue {
		t.Errorf("expected input value to grow by %q, got %q (want %q)", "n", resultModel.input.Value(), wantValue)
	}
	if resultModel.notesEditActive {
		t.Errorf("expected notesEditActive to remain false when the footer is focused")
	}
	if cmd != nil {
		if _, isStart := cmd().(startNotesEditMsg); isStart {
			t.Errorf("expected no startNotesEditMsg when the footer is focused")
		}
	}
	if len(resultModel.activityLines) != 0 {
		t.Errorf("expected no activity line to be appended when footer is focused, got: %v", resultModel.activityLines)
	}
}

// TestAC5a_FooterFocused_ReviewsTabActive_EKeyTypesIntoInput is "n"'s
// counterpart for "e": with the footer focused, "e" must type into the
// footer input rather than launch the $EDITOR flow.
func TestAC5a_FooterFocused_ReviewsTabActive_EKeyTypesIntoInput(t *testing.T) {
	m := newDecideRoutingTestModelWithNotesAndBody()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 3, SpoolPath: "/tmp/spool.md"})
	m.input.SetFocus(true)
	if !m.input.Focused() {
		t.Fatalf("test setup broken: input should be focused")
	}
	beforeValue := m.input.Value()

	result, cmd := m.Update(pressKey('e'))
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	wantValue := beforeValue + "e"
	if resultModel.input.Value() != wantValue {
		t.Errorf("expected input value to grow by %q, got %q (want %q)", "e", resultModel.input.Value(), wantValue)
	}
	if cmd != nil {
		if _, isStart := cmd().(startBodyEditMsg); isStart {
			t.Errorf("expected no startBodyEditMsg when the footer is focused")
		}
	}
	if len(resultModel.activityLines) != 0 {
		t.Errorf("expected no activity line to be appended when footer is focused, got: %v", resultModel.activityLines)
	}
}

// TestAC5a_FooterUnfocused_ReviewsTabActive_NKeyTriggersGatedNotesEdit is
// the counterpart proving "n" reaches ReviewsTab.Update and its
// revise/rereview gating check when the footer does NOT have focus — same
// AC5a guard as p/r/R/d, exercised end to end through both possible
// outcomes (gate passes → startNotesEditMsg; gate fails → gateFailedMsg).
func TestAC5a_FooterUnfocused_ReviewsTabActive_NKeyTriggersGatedNotesEdit(t *testing.T) {
	t.Run("gate passes", func(t *testing.T) {
		spoolPath := writeFakeSpoolWithDecision(t, "revise")
		m := newDecideRoutingTestModelWithNotesAndBody()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 8, SpoolPath: spoolPath})
		m.input.SetFocus(false)

		result, cmd := m.Update(pressKey('n'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting startNotesEditMsg, got nil")
		}
		msg := cmd()
		if _, isStart := msg.(startNotesEditMsg); !isStart {
			t.Fatalf("expected startNotesEditMsg, got %T (%v)", msg, msg)
		}
		finalResult, _ := resultModel.Update(msg)
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if !finalModel.notesEditActive {
			t.Errorf("expected notesEditActive to be true after the gate passes")
		}
	})

	t.Run("gate fails", func(t *testing.T) {
		spoolPath := writeFakeSpoolWithDecision(t, "post")
		m := newDecideRoutingTestModelWithNotesAndBody()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 8, SpoolPath: spoolPath})
		m.input.SetFocus(false)

		result, cmd := m.Update(pressKey('n'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting gateFailedMsg, got nil")
		}
		msg := cmd()
		if _, isGate := msg.(gateFailedMsg); !isGate {
			t.Fatalf("expected gateFailedMsg, got %T (%v)", msg, msg)
		}
		finalResult, _ := resultModel.Update(msg)
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
	})
}

// TestAC5a_FooterUnfocused_ReviewsTabActive_EKeyTriggersBodyEdit is "n"'s
// counterpart for "e": with the footer unfocused, "e" reaches
// ReviewsTab.Update's "e" case and emits startBodyEditMsg for the selected
// row (no gating check applies to body editing, per the design spec).
func TestAC5a_FooterUnfocused_ReviewsTabActive_EKeyTriggersBodyEdit(t *testing.T) {
	spoolPath := writeFakeSpoolWithDecision(t, "post")
	m := newDecideRoutingTestModelWithNotesAndBody()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 12, SpoolPath: spoolPath})
	m.input.SetFocus(false)

	result, cmd := m.Update(pressKey('e'))
	resultModel, ok := result.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if cmd == nil {
		t.Fatalf("expected a tea.Cmd emitting startBodyEditMsg, got nil")
	}
	msg := cmd()
	started, isStart := msg.(startBodyEditMsg)
	if !isStart {
		t.Fatalf("expected startBodyEditMsg, got %T (%v)", msg, msg)
	}
	if started.pr != 12 || started.repo != "owner/repo" {
		t.Errorf("expected startBodyEditMsg for owner/repo#12, got %+v", started)
	}
	if resultModel.input.Focused() {
		t.Errorf("expected footer to remain unfocused")
	}
}
