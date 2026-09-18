package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newFinalizeTestModel builds a model with everything model.Update needs
// for driving the finalize command + state machine end to end: a
// TabManager, styles, tabFocusStates, and a footerManager, mirroring
// newFullDecideRoutingTestModel (decide_routing_test.go) — the finalize
// window-opening path goes through the same switchActiveTab machinery
// decide's routing tests already exercise.
func newFinalizeTestModel() model {
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
		finalizeState:  finalizeIdle,
	}
}

// activityContains reports whether any line in m.activityLines contains substr.
func activityContains(m model, substr string) bool {
	for _, l := range m.activityLines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// drainMsgs repeatedly runs cmd (and any tea.Cmd it returns via tea.Batch)
// through model.Update until no more commands are produced, or until msgs
// runs out — a small helper to walk a Bubble Tea Update/Cmd chain
// synchronously in tests without a real tea.Program. tea.Batch's returned
// cmd, when invoked, returns a tea.BatchMsg ([]tea.Cmd); this helper
// recursively invokes every sub-command and collects every resulting
// tea.Msg (flattening nested batches, though none of the finalize cmds
// nest batches inside batches in practice).
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, collectMsgs(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// drainBatchForType runs cmd (unwrapping any tea.Batch) and returns the
// first collected tea.Msg matching type T, failing the test if none is
// found. Used to pull the terminal message (finalizeDryRunMsg,
// finalizeCompleteMsg, finalizeErrorMsg) out of handleFinalize's/the
// confirmation handler's batched tea.Cmd without needing a real
// tea.Program — the pollFinalizeOutputCmd half of the batch resolves to a
// finalizeTickMsg (or blocks briefly on the tea.Tick timer), which this
// helper simply ignores in favor of the type being searched for.
func drainBatchForType[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	for _, msg := range collectMsgs(cmd) {
		if typed, ok := msg.(T); ok {
			return typed
		}
	}
	var zero T
	t.Fatalf("expected a message of type %T in the command chain, none found", zero)
	return zero
}

// TestHandleFinalize_HappyPath drives: "finalize" REPL command ->
// finalizeDryRunMsg (fake success) -> "y" keypress -> finalizeCompleteMsg
// (fake success), asserting the final state is idle and both a "dry-run
// complete" and a "finalize complete" activity line appear in that order.
func TestHandleFinalize_HappyPath(t *testing.T) {
	restore := installFakeFinalizeScript(t, "echo dry-run-preview-output", "echo live-post-output")
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, cmd := m.handleFinalize(nil)
	if m.finalizeState != finalizeDryRunRunning {
		t.Fatalf("expected finalizeDryRunRunning after handleFinalize, got %v", m.finalizeState)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from handleFinalize")
	}

	msg := drainBatchForType[finalizeDryRunMsg](t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.finalizeState != finalizeAwaitingConfirmation {
		t.Fatalf("expected finalizeAwaitingConfirmation after finalizeDryRunMsg, got %v", m.finalizeState)
	}
	if !activityContains(m, "Dry-run complete") {
		t.Errorf("expected a 'Dry-run complete' activity line, got %v", m.activityLines)
	}

	dryRunIdx := lastActivityIndexContaining(m, "Dry-run complete")

	updated, cmd = m.Update(pressKey('y'))
	m = updated.(model)
	if m.finalizeState != finalizeLiveRunning {
		t.Fatalf("expected finalizeLiveRunning after 'y' keypress, got %v", m.finalizeState)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd after 'y' keypress")
	}

	msg2 := drainBatchForType[finalizeCompleteMsg](t, cmd)
	updated, _ = m.Update(msg2)
	m = updated.(model)

	if m.finalizeState != finalizeIdle {
		t.Fatalf("expected finalizeIdle after finalizeCompleteMsg, got %v", m.finalizeState)
	}
	if !activityContains(m, "Finalize complete") {
		t.Errorf("expected a 'Finalize complete' activity line, got %v", m.activityLines)
	}
	completeIdx := lastActivityIndexContaining(m, "Finalize complete")

	if !(dryRunIdx < completeIdx) {
		t.Errorf("expected 'Dry-run complete' (idx %d) to appear before 'Finalize complete' (idx %d)", dryRunIdx, completeIdx)
	}
}

// TestHandleFinalize_DeclinePath drives: "finalize" -> finalizeDryRunMsg ->
// any non-"y" keypress, asserting idle, exactly one subprocess launch (the
// live run never fires), and a "cancelled" activity line.
func TestHandleFinalize_DeclinePath(t *testing.T) {
	callCount := 0
	restoreCount := installFakeFinalizeScriptCounting(t, &callCount)
	defer restoreCount()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, cmd := m.handleFinalize(nil)
	msg := drainBatchForType[finalizeDryRunMsg](t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.finalizeState != finalizeAwaitingConfirmation {
		t.Fatalf("expected finalizeAwaitingConfirmation, got %v", m.finalizeState)
	}

	updated, cmd = m.Update(pressKey('n'))
	m = updated.(model)

	if m.finalizeState != finalizeIdle {
		t.Fatalf("expected finalizeIdle after decline, got %v", m.finalizeState)
	}
	if cmd != nil {
		t.Error("expected no tea.Cmd after declining (no live run launched)")
	}
	if !activityContains(m, "cancelled") {
		t.Errorf("expected a 'cancelled' activity line, got %v", m.activityLines)
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 subprocess launch (dry-run only), got %d", callCount)
	}
}

// TestHandleFinalize_DryRunErrorPath drives: "finalize" -> finalizeErrorMsg
// (dry-run failure), asserting idle, an error activity line, and that no
// confirmation prompt was ever appended.
func TestHandleFinalize_DryRunErrorPath(t *testing.T) {
	restore := installFakeFinalizeScript(t, "echo boom >&2; exit 1", "echo unused")
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, cmd := m.handleFinalize(nil)
	msg := drainBatchForType[finalizeErrorMsg](t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.finalizeState != finalizeIdle {
		t.Fatalf("expected finalizeIdle after dry-run error, got %v", m.finalizeState)
	}
	if !activityContains(m, "Finalize (dry-run) failed") {
		t.Errorf("expected a dry-run failure activity line, got %v", m.activityLines)
	}
	if activityContains(m, "post live") {
		t.Errorf("expected no confirmation prompt to have been appended, got %v", m.activityLines)
	}
	// A dry-run posts nothing, so the partial-post recovery hint (which is
	// only correct for live runs) must NOT appear here.
	if activityContains(m, "may already have posted") {
		t.Errorf("did not expect a partial-post hint on a dry-run failure, got %v", m.activityLines)
	}
}

// TestHandleFinalize_LiveErrorPath drives: "finalize" -> finalizeDryRunMsg
// (success) -> "y" -> finalizeErrorMsg (live failure), asserting idle, an
// error activity line, and that the dry-run's output is still present in
// m.finalizeCapture.GetLines() (proving nothing was discarded on failure).
func TestHandleFinalize_LiveErrorPath(t *testing.T) {
	restore := installFakeFinalizeScript(t, "echo dry-run-kept-line", "echo boom >&2; exit 1")
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, cmd := m.handleFinalize(nil)
	msg := drainBatchForType[finalizeDryRunMsg](t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)

	updated, cmd = m.Update(pressKey('y'))
	m = updated.(model)
	if m.finalizeState != finalizeLiveRunning {
		t.Fatalf("expected finalizeLiveRunning, got %v", m.finalizeState)
	}

	msg2 := drainBatchForType[finalizeErrorMsg](t, cmd)
	updated, _ = m.Update(msg2)
	m = updated.(model)

	if m.finalizeState != finalizeIdle {
		t.Fatalf("expected finalizeIdle after live error, got %v", m.finalizeState)
	}
	if !activityContains(m, "Finalize (live) failed") {
		t.Errorf("expected a live failure activity line, got %v", m.activityLines)
	}
	// A live run can post some reviews before erroring mid-drain, so the
	// error handler appends a safe-recovery hint (re-run is idempotent).
	if !activityContains(m, "may already have posted") {
		t.Errorf("expected a partial-post recovery hint on a live failure, got %v", m.activityLines)
	}

	found := false
	for _, l := range m.finalizeCapture.GetLines() {
		if strings.Contains(l, "dry-run-kept-line") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dry-run output to still be present after live failure, got %v", m.finalizeCapture.GetLines())
	}
}

// TestHandleFinalize_ReentrancyGuard verifies that calling "finalize" while
// a run is already in progress produces a warning activity line and does
// not launch a second subprocess.
func TestHandleFinalize_ReentrancyGuard(t *testing.T) {
	callCount := 0
	restore := installFakeFinalizeScriptCounting(t, &callCount)
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, _ = m.handleFinalize(nil)
	if callCount != 0 {
		// handleFinalize itself doesn't invoke the subprocess synchronously
		// — the returned tea.Cmd does, and it hasn't been run yet in this
		// test. This assertion just documents that fact for readers.
		t.Fatalf("did not expect the subprocess to have run yet, got callCount=%d", callCount)
	}
	if m.finalizeState != finalizeDryRunRunning {
		t.Fatalf("expected finalizeDryRunRunning after first call, got %v", m.finalizeState)
	}

	m2, cmd2 := m.handleFinalize(nil)
	if cmd2 != nil {
		t.Error("expected no tea.Cmd from a re-entrant finalize call")
	}
	if !activityContains(m2, "Finalize already in progress") {
		t.Errorf("expected a re-entrancy warning activity line, got %v", m2.activityLines)
	}
}

// TestHandleFinalize_ReusesWindowOnSecondRun verifies that running
// "finalize" twice in a row (after the first run reaches idle) reuses the
// same "finalize-preview" tab rather than stacking a second one.
func TestHandleFinalize_ReusesWindowOnSecondRun(t *testing.T) {
	restore := installFakeFinalizeScript(t, "echo run1", "echo run1-live")
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	m, cmd := m.handleFinalize(nil)
	msg := drainBatchForType[finalizeDryRunMsg](t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)
	updated, cmd = m.Update(pressKey('n'))
	m = updated.(model)
	_ = cmd

	if m.finalizeState != finalizeIdle {
		t.Fatalf("expected finalizeIdle after decline, got %v", m.finalizeState)
	}

	countAfterFirst := countTabsWithID(m, "finalize-preview")
	if countAfterFirst != 1 {
		t.Fatalf("expected exactly 1 finalize-preview tab after first run, got %d", countAfterFirst)
	}

	m, cmd = m.handleFinalize(nil)
	countAfterSecond := countTabsWithID(m, "finalize-preview")
	if countAfterSecond != 1 {
		t.Errorf("expected the second run to reuse the same finalize-preview tab, got %d tabs", countAfterSecond)
	}
	_ = cmd
}

// countTabsWithID counts how many open tabs share the given ID — used to
// prove tab reuse rather than stacking.
func countTabsWithID(m model, id string) int {
	count := 0
	for _, tab := range m.tabManager.GetTabs() {
		if tab.ID() == id {
			count++
		}
	}
	return count
}

// lastActivityIndexContaining returns the index of the last activity line
// containing substr, or -1 if none match.
func lastActivityIndexContaining(m model, substr string) int {
	idx := -1
	for i, l := range m.activityLines {
		if strings.Contains(l, substr) {
			idx = i
		}
	}
	return idx
}

// TestFinalizeTickMsg_ContinuesStreamingWhileDifferentTabActive verifies
// that a finalizeTickMsg still calls AppendFromCapture() on the finalize
// preview window even when a different tab is the currently active one —
// simulating the user switching away mid-stream (issue #87's design spec,
// Task 5 acceptance criteria: "output continues to accumulate ... even
// when a different tab is active during the run").
func TestFinalizeTickMsg_ContinuesStreamingWhileDifferentTabActive(t *testing.T) {
	restore := installFakeFinalizeScript(t, "echo dry-run-line", "echo live-line")
	defer restore()

	m := newFinalizeTestModel()
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}
	m, _ = m.handleFinalize(nil)

	// Switch away to a different tab (index 0, the finalize-preview tab
	// itself would be at whatever index AddTab placed it; add a second tab
	// and switch to that one instead so the finalize window is provably
	// not active).
	otherTab := NewLiveReviewContentTab("other-tab", "Other", nil, m.styles)
	m.tabManager.AddTab(otherTab)
	otherIdx := m.tabManager.FindTabByID("other-tab")
	var switchCmd tea.Cmd
	m, switchCmd = m.switchActiveTab(otherIdx)
	_ = switchCmd

	activeTab := m.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.ID() != "other-tab" {
		t.Fatalf("expected 'other-tab' to be active, got %v", activeTab)
	}

	// Simulate a burst of output arriving, then a finalizeTickMsg — the
	// same access pattern runFinalizeCmd's goroutine + the poll loop use
	// in production, but driven synchronously here.
	m.finalizeCapture.AddLine("streamed-while-away")
	updated, _ := m.Update(finalizeTickMsg{})
	m = updated.(model)

	previewIdx := m.tabManager.FindTabByID("finalize-preview")
	if previewIdx < 0 {
		t.Fatalf("expected a finalize-preview tab to exist")
	}
	rct, ok := m.tabManager.GetTabs()[previewIdx].(*ReviewContentTab)
	if !ok {
		t.Fatalf("expected finalize-preview tab to be a *ReviewContentTab")
	}
	if !strings.Contains(rct.CopyableContent(), "streamed-while-away") {
		t.Errorf("expected the preview window's content to include newly streamed output even while inactive, got %q", rct.CopyableContent())
	}

	// The other tab must still be the active one — streaming must not
	// force-switch tabs.
	if m.tabManager.GetActiveTab().ID() != "other-tab" {
		t.Errorf("expected 'other-tab' to remain active after finalizeTickMsg, got %q", m.tabManager.GetActiveTab().ID())
	}
}

// installFakeFinalizeScript swaps finalizeCommandFunc/finalizeScriptPathFunc
// so the dry-run invocation runs dryRunScript and the live invocation runs
// liveScript (both passed to "sh -c"), restoring both vars afterward. It
// distinguishes dry-run vs live by checking for the "--dry-run" arg the
// real runFinalizeCmd appends, exactly matching production's own argv
// shape, so these tests exercise the real branching logic in
// runFinalizeCmd rather than a simplified stand-in.
func installFakeFinalizeScript(t *testing.T, dryRunScript, liveScript string) func() {
	t.Helper()
	origCmdFunc := finalizeCommandFunc
	origScriptFunc := finalizeScriptPathFunc
	finalizeScriptPathFunc = func() (string, error) { return "/fake/finalize-reviews.sh", nil }
	finalizeCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		isDryRun := false
		for _, a := range args {
			if a == "--dry-run" {
				isDryRun = true
			}
		}
		script := liveScript
		if isDryRun {
			script = dryRunScript
		}
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	return func() {
		finalizeCommandFunc = origCmdFunc
		finalizeScriptPathFunc = origScriptFunc
	}
}

// installFakeFinalizeScriptCounting is like installFakeFinalizeScript but
// increments *callCount on every invocation (dry-run or live) and always
// succeeds with no output — used by tests asserting exactly how many times
// the subprocess seam was invoked (re-entrancy guard, decline path).
func installFakeFinalizeScriptCounting(t *testing.T, callCount *int) func() {
	t.Helper()
	origCmdFunc := finalizeCommandFunc
	origScriptFunc := finalizeScriptPathFunc
	finalizeScriptPathFunc = func() (string, error) { return "/fake/finalize-reviews.sh", nil }
	finalizeCommandFunc = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		*callCount++
		return exec.CommandContext(ctx, "true")
	}
	return func() {
		finalizeCommandFunc = origCmdFunc
		finalizeScriptPathFunc = origScriptFunc
	}
}

// TestHandleFinalize_PreflightFailure_BlocksDryRun drives handleFinalize
// against a real (unstubbed) review.CheckFinalizeAssets() by forcing
// finalize-reviews.sh/pr_review_finalize.py to be unresolvable on PATH for
// the duration of the test — handleFinalize's synchronous preflight calls
// review.CheckFinalizeAssets() directly (see commands.go), not the
// TUI-layer checkFinalizeAssetsFunc seam, and CheckFinalizeAssets resolves
// through review.lookPathFunc, which is private to package review and not
// reachable from this package. Emptying PATH is the only available seam
// from here — see issue #88's design spec, Task 6 and the Test Plan's row
// for "handleFinalize's synchronous preflight call". Asserts the dry run
// never starts: finalizeState stays finalizeIdle, the lower-level
// subprocess seams (finalizeCommandFunc/finalizeScriptPathFunc) are never
// invoked (proven via the call-counting stub, not just by inspecting
// state), the activity log surfaces CheckFinalizeAssets's "not found on
// PATH" message, and the Reviews tab built into the test model reflects
// finalizePreflightFailed afterward.
func TestHandleFinalize_PreflightFailure_BlocksDryRun(t *testing.T) {
	origPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatalf("failed to empty PATH: %v", err)
	}
	defer func() {
		if err := os.Setenv("PATH", origPath); err != nil {
			t.Fatalf("failed to restore PATH: %v", err)
		}
	}()

	callCount := 0
	restoreCount := installFakeFinalizeScriptCounting(t, &callCount)
	defer restoreCount()

	m := newFinalizeTestModel()
	rt := addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 1})

	m, cmd := m.handleFinalize(nil)

	if m.finalizeState != finalizeIdle {
		t.Errorf("expected finalizeState to remain finalizeIdle after a preflight failure, got %v", m.finalizeState)
	}
	if cmd != nil {
		t.Errorf("expected a nil tea.Cmd from handleFinalize on preflight failure (no dry run kicked off), got %v", cmd)
	}
	if callCount != 0 {
		t.Errorf("expected the subprocess seam to never be invoked on preflight failure, got callCount=%d", callCount)
	}
	if !activityContains(m, "not found on PATH") {
		t.Errorf("expected the activity log to contain CheckFinalizeAssets's 'not found on PATH' message, got %v", m.activityLines)
	}
	if rt.preflightState != finalizePreflightFailed {
		t.Errorf("expected the Reviews tab's preflightState to be finalizePreflightFailed, got %v", rt.preflightState)
	}
	if rt.preflightErr == "" {
		t.Error("expected the Reviews tab's preflightErr to be populated on failure")
	}
}

// TestHandleFinalize_PreflightSuccess_StartsDryRun_NoRegression is the
// happy-path counterpart to TestHandleFinalize_PreflightFailure_BlocksDryRun.
// It relies on the real review.CheckFinalizeAssets() succeeding against the
// actual test-environment $PATH (mirroring the pre-existing happy-path
// tests above, which all skip via t.Skipf when the real scripts are not
// installed) — handleFinalize must still transition finalizeState to
// finalizeDryRunRunning exactly as it did before this issue's preflight
// gate was added, proving the new gate introduces no regression to the
// existing happy path already covered by TestHandleFinalize_HappyPath.
func TestHandleFinalize_PreflightSuccess_StartsDryRun_NoRegression(t *testing.T) {
	if err := review.CheckFinalizeAssets(); err != nil {
		t.Skipf("Skipping test - finalize assets not available: %v", err)
	}

	restore := installFakeFinalizeScript(t, "echo dry-run-preview-output", "echo live-post-output")
	defer restore()

	m := newFinalizeTestModel()
	rt := addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 2})

	m, cmd := m.handleFinalize(nil)

	if m.finalizeState != finalizeDryRunRunning {
		t.Fatalf("expected finalizeDryRunRunning after handleFinalize with a passing preflight, got %v", m.finalizeState)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from handleFinalize on preflight success")
	}
	if rt.preflightState != finalizePreflightOK {
		t.Errorf("expected the Reviews tab's preflightState to be finalizePreflightOK, got %v", rt.preflightState)
	}
	if rt.preflightErr != "" {
		t.Errorf("expected the Reviews tab's preflightErr to be cleared on success, got %q", rt.preflightErr)
	}
}

// TestFinalizeRetryPreflight_RoundTrip_IsLiveNotCached drives the full
// retry-key round trip described in issue #88's design spec, Task 6 and
// the Test Plan's "Retry key round trip" row: a ReviewsTab's "F" keypress
// emits finalizeRetryPreflightMsg; model.Update turns that into
// runFinalizePreflightCmd(); running that tea.Cmd invokes
// checkFinalizeAssetsFunc and yields finalizePreflightResultMsg; feeding
// that back into model.Update propagates the result onto the Reviews tab
// via applyFinalizePreflightResult/SetFinalizePreflightState.
//
// checkFinalizeAssetsFunc (not review.lookPathFunc) is the seam stubbed
// here, matching the Test Plan's row for this exact scenario — retry
// round trips through runFinalizePreflightCmd, which calls through
// checkFinalizeAssetsFunc, not the synchronous handleFinalize path (which
// calls review.CheckFinalizeAssets directly). The stub flips from failing
// to succeeding between the two invocations to prove each "F" press
// performs a live re-check rather than reusing a cached first result.
func TestFinalizeRetryPreflight_RoundTrip_IsLiveNotCached(t *testing.T) {
	callCount := 0
	origCheck := checkFinalizeAssetsFunc
	checkFinalizeAssetsFunc = func() error {
		callCount++
		if callCount == 1 {
			return errors.New("boom: scripts not found on PATH")
		}
		return nil
	}
	defer func() { checkFinalizeAssetsFunc = origCheck }()

	m := newFinalizeTestModel()
	rt := addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 3})

	// Round 1: "F" -> finalizeRetryPreflightMsg -> runFinalizePreflightCmd()
	// -> finalizePreflightResultMsg (failing).
	tab, keyCmd := rt.Update(pressKey('F'))
	if _, ok := tab.(*ReviewsTab); !ok {
		t.Fatalf("expected ReviewsTab.Update to return a *ReviewsTab, got %T", tab)
	}
	if keyCmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from the 'F' keypress")
	}
	retryMsg := keyCmd()
	if _, ok := retryMsg.(finalizeRetryPreflightMsg); !ok {
		t.Fatalf("expected finalizeRetryPreflightMsg from the 'F' keypress, got %T (%v)", retryMsg, retryMsg)
	}

	updated, preflightCmd := m.Update(retryMsg)
	m, ok := updated.(model)
	if !ok {
		t.Fatalf("model.Update did not return a model")
	}
	if preflightCmd == nil {
		t.Fatal("expected model.Update to return runFinalizePreflightCmd() for finalizeRetryPreflightMsg")
	}

	resultMsg := preflightCmd()
	preflightResult, ok := resultMsg.(finalizePreflightResultMsg)
	if !ok {
		t.Fatalf("expected finalizePreflightResultMsg, got %T (%v)", resultMsg, resultMsg)
	}
	if preflightResult.err == nil {
		t.Fatal("expected the first round's result to carry an error")
	}

	updated, _ = m.Update(preflightResult)
	m, ok = updated.(model)
	if !ok {
		t.Fatalf("model.Update did not return a model")
	}

	if rt.preflightState != finalizePreflightFailed {
		t.Errorf("round 1: expected the Reviews tab's preflightState to be finalizePreflightFailed, got %v", rt.preflightState)
	}
	if callCount != 1 {
		t.Errorf("round 1: expected checkFinalizeAssetsFunc to have been called exactly once, got %d", callCount)
	}

	// Round 2: press "F" again; the stub now succeeds, proving the retry
	// performed a fresh check rather than replaying the cached first result.
	tab, keyCmd = rt.Update(pressKey('F'))
	if _, ok := tab.(*ReviewsTab); !ok {
		t.Fatalf("expected ReviewsTab.Update to return a *ReviewsTab, got %T", tab)
	}
	retryMsg = keyCmd()

	updated, preflightCmd = m.Update(retryMsg)
	m, ok = updated.(model)
	if !ok {
		t.Fatalf("model.Update did not return a model")
	}
	if preflightCmd == nil {
		t.Fatal("expected model.Update to return runFinalizePreflightCmd() for finalizeRetryPreflightMsg")
	}

	resultMsg = preflightCmd()
	preflightResult, ok = resultMsg.(finalizePreflightResultMsg)
	if !ok {
		t.Fatalf("expected finalizePreflightResultMsg, got %T (%v)", resultMsg, resultMsg)
	}
	if preflightResult.err != nil {
		t.Errorf("round 2: expected the stubbed check to succeed, got %v", preflightResult.err)
	}

	updated, _ = m.Update(preflightResult)
	if _, ok := updated.(model); !ok {
		t.Fatalf("model.Update did not return a model")
	}

	if rt.preflightState != finalizePreflightOK {
		t.Errorf("round 2: expected the Reviews tab's preflightState to be finalizePreflightOK, got %v", rt.preflightState)
	}
	if rt.preflightErr != "" {
		t.Errorf("round 2: expected the Reviews tab's preflightErr to be cleared, got %q", rt.preflightErr)
	}
	if callCount != 2 {
		t.Errorf("expected checkFinalizeAssetsFunc to have been called exactly twice (live re-check, not cached), got %d", callCount)
	}
}
