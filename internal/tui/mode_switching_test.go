package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/session"
	"github.com/matthiashowellyopp/howmux/internal/watcher"
)

func setupTestModel(t *testing.T) model {
	t.Helper()

	// Create test config
	cfg := &config.Config{
		Theme: "default",
		LoadedTheme: &config.Theme{
			Name: "default",
		},
		MaxActivityLines: 100,
	}

	// Create test manager and watcher
	manager := agent.NewManager(cfg)
	w := &watcher.Watcher{}

	// Create temp files for logging
	logFile, err := os.CreateTemp("", "test-log")
	if err != nil {
		t.Fatalf("Failed to create temp log file: %v", err)
	}
	t.Cleanup(func() { logFile.Close(); os.Remove(logFile.Name()) })

	logReader, err := os.Open(logFile.Name())
	if err != nil {
		t.Fatalf("Failed to open log reader: %v", err)
	}
	t.Cleanup(func() { logReader.Close() })

	return newModel(w, manager, cfg, logFile, logReader, "")
}

func TestSwitchToPlanningModePreservesConsoleState(t *testing.T) {
	m := setupTestModel(t)

	// Setup console state with input and activity
	testInput := "test command input"
	testActivity := []string{
		"Activity line 1",
		"Activity line 2",
		"Console output history",
	}

	m.input.SetValue(testInput)
	m.activityLines = make([]string, len(testActivity))
	copy(m.activityLines, testActivity)

	// Verify initial state
	if m.input.Value() != testInput {
		t.Errorf("Initial input value not set correctly: got %q, want %q", m.input.Value(), testInput)
	}
	if len(m.activityLines) != len(testActivity) {
		t.Errorf("Initial activity lines not set correctly: got %d lines, want %d", len(m.activityLines), len(testActivity))
	}

	// Create a mock planning session to avoid actual CLI execution
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager

	// Switch to planning mode
	m, cmd := m.switchToPlanningMode()

	// Verify console state was preserved
	if m.consoleState == nil {
		t.Fatal("Console state should be preserved but is nil")
	}

	if m.consoleState.inputValue != testInput {
		t.Errorf("Input value not preserved: got %q, want %q", m.consoleState.inputValue, testInput)
	}

	if len(m.consoleState.activityLines) != len(testActivity) {
		t.Errorf("Activity lines count not preserved: got %d, want %d", len(m.consoleState.activityLines), len(testActivity))
	}

	for i, expected := range testActivity {
		if i < len(m.consoleState.activityLines) && m.consoleState.activityLines[i] != expected {
			t.Errorf("Activity line %d not preserved: got %q, want %q", i, m.consoleState.activityLines[i], expected)
		}
	}

	// Verify mode switched
	if m.currentMode != session.Planning {
		t.Errorf("Mode not switched correctly: got %v, want %v", m.currentMode, session.Planning)
	}

	// Verify command is returned (for screen clearing)
	if cmd == nil {
		t.Error("Expected command to be returned for screen clearing")
	}

	// Clean up session
	sessionManager.Delete(sessionID)
}

func TestSwitchToConsolePreservesAgentOutput(t *testing.T) {
	m := setupTestModel(t)

	// Setup agent output
	testAgent := &agent.Agent{
		ID:          "test-agent-123",
		IssueNumber: 42,
		IssueTitle:  "Test Issue",
		Status:      agent.StatusRunning,
		StartTime:   time.Now(),
	}

	// Add agent to manager
	m.manager.RegisterAgent(testAgent.ID, testAgent.IssueNumber)

	// Add some test output to the capture (via manager's capture method)
	testOutputLines := []string{
		"Agent started processing",
		"Reading project structure",
		"Implementing solution",
	}

	for _, line := range testOutputLines {
		m.manager.CaptureOutputLine(testAgent.IssueNumber, line)
	}

	// Create agent tab to verify output preservation
	m.tabManager.RestoreOrFocusAgentTab(testAgent.ID, m.manager, m.styles)

	// Verify agent tab was created and has output
	tabs := m.tabManager.GetTabs()
	if len(tabs) != 3 { // Main + Reviews + Agent tab
		t.Fatalf("Expected 3 tabs, got %d", len(tabs))
	}

	agentTab := tabs[2]
	if agentTab.Type() != TabTypeAgent {
		t.Error("Third tab should be an agent tab")
	}

	// Switch to planning mode first
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager
	m.currentMode = session.Planning

	// Now switch back to console mode
	m, cmd := m.switchToConsoleMode()

	// Verify agent output is still available
	outputLines := m.manager.GetOutputLines()
	if len(outputLines) == 0 {
		t.Error("Agent output lines should be preserved")
	}

	// Verify specific output content is preserved
	foundOutput := false
	for _, line := range outputLines {
		if strings.Contains(line, "Agent started processing") {
			foundOutput = true
			break
		}
	}
	if !foundOutput {
		t.Error("Specific agent output not preserved")
	}

	// Verify mode switched back
	if m.currentMode != session.Console {
		t.Errorf("Mode not switched correctly: got %v, want %v", m.currentMode, session.Console)
	}

	// Verify command is returned
	if cmd == nil {
		t.Error("Expected command to be returned")
	}

	// Clean up
	sessionManager.Delete(sessionID)
}

func TestTabStatePreservedDuringModeTransitions(t *testing.T) {
	m := setupTestModel(t)

	// Create multiple tabs
	testAgent1 := &agent.Agent{
		ID:          "test-agent-1",
		IssueNumber: 1,
		IssueTitle:  "First Issue",
		Status:      agent.StatusRunning,
		StartTime:   time.Now(),
	}

	testAgent2 := &agent.Agent{
		ID:          "test-agent-2",
		IssueNumber: 2,
		IssueTitle:  "Second Issue",
		Status:      agent.StatusRunning,
		StartTime:   time.Now(),
	}

	m.manager.RegisterAgent(testAgent1.ID, testAgent1.IssueNumber)
	m.manager.RegisterAgent(testAgent2.ID, testAgent2.IssueNumber)

	// Create agent tabs
	m.tabManager.RestoreOrFocusAgentTab(testAgent1.ID, m.manager, m.styles)
	m.tabManager.RestoreOrFocusAgentTab(testAgent2.ID, m.manager, m.styles)

	// Set active tab to agent tab
	m.tabManager.SetActiveTab(3) // Fourth tab (second agent)
	initialActiveIndex := m.tabManager.GetActiveTabIndex()
	initialTabCount := len(m.tabManager.GetTabs())

	// Verify initial state
	if initialTabCount != 4 { // Main + Reviews + 2 agent tabs
		t.Fatalf("Expected 4 tabs initially, got %d", initialTabCount)
	}
	if initialActiveIndex != 3 {
		t.Fatalf("Expected active tab index 3, got %d", initialActiveIndex)
	}

	// Mock planning session
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager

	// Switch to planning mode
	m, _ = m.switchToPlanningMode()

	// Tabs should still exist
	if len(m.tabManager.GetTabs()) != initialTabCount {
		t.Errorf("Tab count changed during planning mode switch: got %d, want %d", len(m.tabManager.GetTabs()), initialTabCount)
	}

	// Switch back to console mode
	m.currentMode = session.Planning // Ensure we're in planning mode
	m, _ = m.switchToConsoleMode()

	// Verify all tabs are still present
	if len(m.tabManager.GetTabs()) != initialTabCount {
		t.Errorf("Tab count changed after console mode switch: got %d, want %d", len(m.tabManager.GetTabs()), initialTabCount)
	}

	// Verify active tab is preserved
	if m.tabManager.GetActiveTabIndex() != initialActiveIndex {
		t.Errorf("Active tab index changed: got %d, want %d", m.tabManager.GetActiveTabIndex(), initialActiveIndex)
	}

	// Verify tab content is still accessible
	activeTab := m.tabManager.GetActiveTab()
	if activeTab == nil {
		t.Error("Active tab should not be nil")
	}
	if activeTab.Type() != TabTypeAgent {
		t.Error("Active tab should still be an agent tab")
	}

	// Clean up
	sessionManager.Delete(sessionID)
}

func TestRapidModeSwitchingNoDataLoss(t *testing.T) {
	m := setupTestModel(t)

	// Setup initial state with data
	testInput := "rapid switch test"
	testActivity := []string{"Rapid test line 1", "Rapid test line 2"}

	m.input.SetValue(testInput)
	m.activityLines = make([]string, len(testActivity))
	copy(m.activityLines, testActivity)

	// Add test agent for output
	testAgent := &agent.Agent{
		ID:          "rapid-agent",
		IssueNumber: 99,
		IssueTitle:  "Rapid Switch Test",
		Status:      agent.StatusRunning,
		StartTime:   time.Now(),
	}
	m.manager.RegisterAgent(testAgent.ID, testAgent.IssueNumber)

	m.manager.CaptureOutputLine(testAgent.IssueNumber, "Rapid switch test output")

	// Mock planning session
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager

	// Perform rapid switching (multiple cycles)
	for i := 0; i < 5; i++ {
		// Switch to planning
		m, _ = m.switchToPlanningMode()
		if m.currentMode != session.Planning {
			t.Errorf("Cycle %d: Failed to switch to planning mode", i)
		}

		// Switch back to console
		m.currentMode = session.Planning // Ensure we're in planning mode for switch
		m, _ = m.switchToConsoleMode()
		if m.currentMode != session.Console {
			t.Errorf("Cycle %d: Failed to switch to console mode", i)
		}
	}

	// Verify no data loss after rapid switching
	if m.consoleState.inputValue != testInput {
		t.Errorf("Input value lost after rapid switching: got %q, want %q", m.consoleState.inputValue, testInput)
	}

	// Activity lines should contain the original lines in order at the beginning
	for i, expected := range testActivity {
		if i >= len(m.activityLines) {
			t.Errorf("Activity lines truncated: missing line %d %q", i, expected)
			break
		}
		if m.activityLines[i] != expected {
			t.Errorf("Activity line %d out of order or modified: got %q, want %q", i, m.activityLines[i], expected)
		}
	}

	// Verify agent output is still available
	outputLines := m.manager.GetOutputLines()
	foundRapidOutput := false
	for _, line := range outputLines {
		if strings.Contains(line, "Rapid switch test output") {
			foundRapidOutput = true
			break
		}
	}
	if !foundRapidOutput {
		t.Error("Agent output lost after rapid switching")
	}

	// Clean up
	sessionManager.Delete(sessionID)
}

func TestConsoleStateRestorationAfterModeSwitch(t *testing.T) {
	m := setupTestModel(t)

	// Setup console with complex state
	complexInput := "complex command with args --flag=value"
	complexActivity := []string{
		"Started processing complex task",
		"Multiple lines of console output",
		"Error: Something went wrong",
		"Recovery: Fixed the error",
		"Success: Task completed",
	}

	m.input.SetValue(complexInput)
	m.activityLines = make([]string, len(complexActivity))
	copy(m.activityLines, complexActivity)

	// Mock planning session
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager

	// Switch to planning mode
	m, _ = m.switchToPlanningMode()

	// Switch back and verify restoration
	m.currentMode = session.Planning
	m, _ = m.switchToConsoleMode()

	// Verify restoration via restoreConsoleState
	if m.input.Value() != complexInput {
		t.Errorf("Complex input not restored: got %q, want %q", m.input.Value(), complexInput)
	}

	// Activity lines should contain all original lines (mode switch message may be appended)
	if len(m.activityLines) < len(complexActivity) {
		t.Errorf("Activity lines count less than expected: got %d, want at least %d", len(m.activityLines), len(complexActivity))
	}

	// Verify specific activity content is preserved (in original order)
	for i, expected := range complexActivity {
		if i < len(m.activityLines) && m.activityLines[i] != expected {
			t.Errorf("Activity line %d not restored correctly: got %q, want %q", i, m.activityLines[i], expected)
		}
	}

	// Clean up
	sessionManager.Delete(sessionID)
}

func TestModeSwitchingWithNoActivePlanningSessions(t *testing.T) {
	m := setupTestModel(t)

	// Use an isolated session directory with no existing sessions
	tmpDir := t.TempDir()
	m.sessionManager = session.NewSessionManagerWithDir(tmpDir)

	// Try to switch to planning mode with no active sessions
	m, _ = m.switchToPlanningMode()

	// Should remain in console mode when no planning session exists
	if m.currentMode != session.Console {
		t.Errorf("Should remain in console mode when no planning session exists: got %v", m.currentMode)
	}

	// Verify activity line was added about no active session
	foundWarning := false
	for _, line := range m.activityLines {
		if strings.Contains(line, "No active planning session") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Error("Expected warning about no active planning session")
	}
}

// TestBodyEditDoesNotAffectPlanningModeState proves that a body-edit round
// trip (startBodyEditMsg → editorDoneMsg) leaves m.currentMode and
// m.consoleState completely untouched, even though both body-edit and
// planning-mode share the exact same tea.ExecProcess mechanism and the
// same Manager.SuspendOutputCapture/ResumeOutputCapture pair. This is the
// design spec's Task 7 regression requirement: editorDoneMsg's handler
// intentionally never calls restoreConsoleState() or touches
// m.currentMode (see editorDoneMsg's doc comment in tui.go) — this test
// fails if a future change accidentally routes body-edit through
// execDoneMsg's console-restoring code path instead of its own.
func TestBodyEditDoesNotAffectPlanningModeState(t *testing.T) {
	m := setupTestModel(t)

	// Seed state that would be disturbed if editorDoneMsg accidentally
	// shared execDoneMsg's restoreConsoleState() behavior.
	m.currentMode = session.Planning
	m.consoleState = &consoleState{
		inputValue:    "in-progress planning input",
		activityLines: []string{"pre-existing planning activity"},
	}
	m.activityLines = []string{"pre-existing planning activity"}
	m.input.SetValue("in-progress planning input")

	dir := t.TempDir()
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-55.md")
	if err := os.WriteFile(spoolPath, []byte("---\ndecision:\ndecision_notes: \"\"\n---\noriginal body\n"), 0o644); err != nil {
		t.Fatalf("failed to write spool file: %v", err)
	}

	tempFile, err := os.CreateTemp("", "howmux-review-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := tempFile.WriteString("edited body\n"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tempFile.Close()

	m.manager.SuspendOutputCapture()
	rec := review.Record{Repo: "owner/repo", PR: 55, SpoolPath: spoolPath}
	newM, _ := m.Update(editorDoneMsg{tempPath: tempFile.Name(), rec: rec, err: nil})
	resultModel, ok := newM.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if resultModel.currentMode != session.Planning {
		t.Errorf("expected currentMode to remain session.Planning (untouched by body edit), got %v", resultModel.currentMode)
	}
	if resultModel.consoleState == nil || resultModel.consoleState.inputValue != "in-progress planning input" {
		t.Errorf("expected consoleState.inputValue to remain untouched by body edit, got %+v", resultModel.consoleState)
	}
	if resultModel.input.Value() != "in-progress planning input" {
		t.Errorf("expected footer input value to remain untouched by body edit, got %q", resultModel.input.Value())
	}
}

// TestPlanningModeSwitchDoesNotAffectBodyEditState is the converse of
// TestBodyEditDoesNotAffectPlanningModeState: a full planning-mode
// switchToPlanningMode/switchToConsoleMode round trip must not disturb any
// body-edit-related model state. Body editing has no persistent model
// fields of its own (no notesEditActive-style flag — the temp file and
// review.Record travel through the message round trip, not through model
// fields), so this test's job is to confirm that remains true: after a
// full planning-mode cycle, model construction invariants for body editing
// (a non-nil bodyWriter, ready to accept the next "e" press) still hold.
func TestPlanningModeSwitchDoesNotAffectBodyEditState(t *testing.T) {
	m := setupTestModel(t)
	if m.bodyWriter == nil {
		t.Fatalf("test setup broken: expected model construction to populate bodyWriter")
	}

	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager

	m, _ = m.switchToPlanningMode()
	m.currentMode = session.Planning
	m, _ = m.switchToConsoleMode()

	if m.bodyWriter == nil {
		t.Errorf("expected bodyWriter to remain non-nil after a planning-mode round trip")
	}
	if m.currentMode != session.Console {
		t.Errorf("expected currentMode to be session.Console after the round trip, got %v", m.currentMode)
	}

	sessionManager.Delete(sessionID)
}

// TestBodyEditAndPlanningModeSuspendResumeAreIndependentRoundTrips proves
// that Manager.SuspendOutputCapture/ResumeOutputCapture — called by both
// handleBodyEditLaunch/editorDoneMsg (body editing) and
// handlePlanSubprocess/execDoneMsg (planning mode) on the exact same
// Manager — can each run a full suspend→resume cycle back to back on the
// same model without one flow's cycle leaving state that breaks the
// other's. Manager.terminalOutputPaused has no exported accessor from this
// package (confirmed unobservable directly, matching body_edit_test.go's
// own note on this), so this test's assertion is behavioral: both
// editorDoneMsg and execDoneMsg must complete their handler without
// panicking or returning an error state, and a second suspend/resume
// cycle (of either kind) must succeed identically after the first — the
// strongest test available without adding a new exported accessor to
// internal/agent purely for this test, which would over-grow that
// package's surface for a single assertion.
func TestBodyEditAndPlanningModeSuspendResumeAreIndependentRoundTrips(t *testing.T) {
	m := setupTestModel(t)

	// Cycle 1: body-edit suspend/resume.
	dir := t.TempDir()
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte("---\ndecision:\ndecision_notes: \"\"\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("failed to write spool file: %v", err)
	}
	tempFile, err := os.CreateTemp("", "howmux-review-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := tempFile.WriteString("edited\n"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tempFile.Close()

	m.manager.SuspendOutputCapture()
	rec := review.Record{Repo: "owner/repo", PR: 1, SpoolPath: spoolPath}
	newM, _ := m.Update(editorDoneMsg{tempPath: tempFile.Name(), rec: rec, err: nil})
	m, ok := newM.(model)
	if !ok {
		t.Fatalf("Update did not return a model after editorDoneMsg")
	}

	// Cycle 2: planning-mode suspend/resume, on the same model/Manager,
	// immediately after cycle 1 — proves the shared Suspend/Resume pair
	// on Manager tolerates back-to-back use by both flows.
	sessionManager := session.NewSessionManager()
	sessionID, err := sessionManager.Create(session.Planning)
	if err != nil {
		t.Fatalf("Failed to create mock planning session: %v", err)
	}
	m.sessionManager = sessionManager
	m.consoleState = &consoleState{inputValue: "before planning", activityLines: []string{"before"}}
	m.manager.SuspendOutputCapture()
	newM2, cmd := m.Update(execDoneMsg{err: nil})
	m, ok = newM2.(model)
	if !ok {
		t.Fatalf("Update did not return a model after execDoneMsg")
	}
	if cmd == nil {
		t.Errorf("expected a non-nil tea.Cmd from execDoneMsg handling")
	}
	if m.input.Value() != "before planning" {
		t.Errorf("expected restoreConsoleState() to run normally on execDoneMsg after a prior body-edit cycle, got input %q", m.input.Value())
	}

	sessionManager.Delete(sessionID)
}
