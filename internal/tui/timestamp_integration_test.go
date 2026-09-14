package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
)

// TestTimestampFormat verifies the timestamp format matches [YYYY-MM-DD HH:MM:SS]
func TestTimestampFormat(t *testing.T) {
	testTime := time.Date(2026, 9, 14, 9, 49, 48, 0, time.UTC)
	expected := "[2026-09-14 09:49:48]"
	actual := formatTimestamp(testTime)

	if actual != expected {
		t.Errorf("timestamp format mismatch: expected %q, got %q", expected, actual)
	}
}

// TestPhaseTransitionDetection verifies all documented phase patterns are detected
func TestPhaseTransitionDetection(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected bool
	}{
		// Lines starting with '>' (agent narrative markers)
		{"narrative marker", "> Processing issue #42", true},
		{"narrative with spaces", "  > Delegating to architect", true},
		{"narrative mixed case", "> Reading SPEC file", true},

		// Workflow keywords
		{"delegate lowercase", "Delegating to architect agent", true},
		{"delegate uppercase", "DELEGATE TO BUILDER", true},
		{"delegated past tense", "Delegated task to validator", true},
		{"reading spec", "Reading specification from .howmux/specs/", true},
		{"read issue", "Read issue #42 from GitHub", true},
		{"checking quality", "Checking code quality", true},
		{"qa loop", "Entering QA loop iteration 2", true},
		{"quality assurance", "Quality assurance phase started", true},
		{"pushing changes", "Pushing changes to remote", true},
		{"push branch", "Push branch spec/issue-42 to origin", true},
		{"create pr", "Create PR for issue #42", true},
		{"creating pr", "Creating PR with gh cli", true},
		{"label done", "Label issue as howmux-done", true},
		{"label failed", "Label issue as howmux-failed", true},

		// Non-phase lines (should NOT trigger)
		{"regular output", "Building project...", false},
		{"test output", "Running tests: 5 passed", false},
		{"error message", "Error: failed to connect", false},
		{"empty line", "", false},
		{"whitespace only", "   ", false},
		{"delegate substring in word", "undelegated work remains", false},
		{"almost narrative", "output > result", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := detectPhaseTransition(tt.line)
			if actual != tt.expected {
				t.Errorf("detectPhaseTransition(%q) = %v, expected %v", tt.line, actual, tt.expected)
			}
		})
	}
}

// TestTimestampCacheStability verifies timestamps remain fixed across redraws
func TestTimestampCacheStability(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)

	// Register agent and add phase transition output
	manager.RegisterAgent("agent-42", 42)
	agentState := manager.GetAgent("agent-42")
	agentState.Status = agent.StatusRunning
	agentState.IssueTitle = "Test issue"

	// Capture output with phase transition via Manager
	manager.CaptureOutputLine(42, "> Starting workflow")
	manager.CaptureOutputLine(42, "Regular output line")
	manager.CaptureOutputLine(42, "Delegating to architect")

	// Create output view
	view := NewOutputViewForAgent("agent-42", manager, styles)
	view.Resize(80, 24)

	// First refresh
	view.refreshContent()
	firstContent := view.viewport.View()

	// Extract timestamps from first render
	firstTimestamps := extractTimestamps(firstContent)
	if len(firstTimestamps) != 2 {
		t.Fatalf("expected 2 timestamps in first render, got %d", len(firstTimestamps))
	}

	// Simulate time passing
	time.Sleep(10 * time.Millisecond)

	// Second refresh (should reuse cached timestamps)
	view.refreshContent()
	secondContent := view.viewport.View()

	// Extract timestamps from second render
	secondTimestamps := extractTimestamps(secondContent)
	if len(secondTimestamps) != 2 {
		t.Fatalf("expected 2 timestamps in second render, got %d", len(secondTimestamps))
	}

	// Verify timestamps are identical (no drift)
	for i := 0; i < len(firstTimestamps); i++ {
		if firstTimestamps[i] != secondTimestamps[i] {
			t.Errorf("timestamp drift detected at index %d: %q != %q",
				i, firstTimestamps[i], secondTimestamps[i])
		}
	}
}

// TestOutputCaptureIsolation verifies timestamps do NOT appear in OutputCapture
func TestOutputCaptureIsolation(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)

	// Register agent and add output with phase transitions
	manager.RegisterAgent("agent-42", 42)
	agentState := manager.GetAgent("agent-42")
	agentState.Status = agent.StatusRunning
	agentState.IssueTitle = "Test issue"

	// Add lines that would trigger timestamp display
	testLines := []string{
		"> Starting workflow",
		"Delegating to architect",
		"Reading specification",
		"Regular output",
	}

	for _, line := range testLines {
		manager.CaptureOutputLine(42, line)
	}

	// Verify OutputCapture contains NO timestamps
	capturedLines := manager.GetOutputLines()
	for i, line := range capturedLines {
		if strings.Contains(line, "[2") && strings.Contains(line, "]") {
			// Check for timestamp pattern [YYYY-MM-DD HH:MM:SS]
			if strings.Contains(line, "-") && len(line) > 20 {
				if matched := strings.Contains(line, "[20"); matched {
					t.Errorf("OutputCapture line %d contains timestamp pattern: %q", i, line)
				}
			}
		}
	}

	// Verify expected lines are present WITHOUT timestamps
	if len(capturedLines) != len(testLines) {
		t.Errorf("expected %d lines in OutputCapture, got %d", len(testLines), len(capturedLines))
	}
}

// TestThemeCompatibility verifies timestamp styling works with all themes
func TestThemeCompatibility(t *testing.T) {
	themes := []struct {
		name           string
		timestampColor string
		fallbackColor  string
	}{
		{"default", "#888888", "#888888"},
		{"with explicit timestamp", "#AAAAAA", "#888888"},
		{"empty timestamp (fallback)", "", "#888888"}, // Falls back to TextMuted
	}

	for _, tt := range themes {
		t.Run(tt.name, func(t *testing.T) {
			theme := &config.Theme{
				Name:        tt.name,
				Description: "Test theme",
			}
			theme.Colors.Primary = "#00AAFF"
			theme.Colors.Secondary = "#888888"
			theme.Colors.Success = "#00AA00"
			theme.Colors.Warning = "#FFAA00"
			theme.Colors.Error = "#FF0000"
			theme.Colors.TextPrimary = "#FFFFFF"
			theme.Colors.TextSecondary = "#CCCCCC"
			theme.Colors.TextMuted = tt.fallbackColor
			theme.Colors.Prompt = "#00AAFF"
			theme.Colors.Separator = "#00AAFF"
			theme.Colors.Activity = "#FFFFFF"
			theme.Colors.Background = "#000000"
			theme.Colors.Surface = "#111111"
			theme.Colors.AgentSuccess = "#00AA00"
			theme.Colors.AgentFail = "#FF0000"
			theme.Colors.Timestamp = tt.timestampColor

			styles := NewStyles(theme)

			// Verify Timestamp style is initialized
			if styles.Timestamp.GetForeground() == nil {
				t.Error("Timestamp style foreground is nil")
			}

			// Verify no panics when rendering
			testText := "[2026-09-14 09:49:48]"
			rendered := styles.Timestamp.Render(testText)
			if rendered == "" {
				t.Error("Timestamp rendering produced empty string")
			}
		})
	}
}

// TestEdgeCaseNoPhaseTransitions verifies behavior with pure output stream
func TestEdgeCaseNoPhaseTransitions(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)

	manager.RegisterAgent("agent-42", 42)
	agentState := manager.GetAgent("agent-42")
	agentState.Status = agent.StatusRunning
	agentState.IssueTitle = "Test issue"

	// Add output with NO phase transitions
	manager.CaptureOutputLine(42, "Building project...")
	manager.CaptureOutputLine(42, "Running tests...")
	manager.CaptureOutputLine(42, "All tests passed")

	view := NewOutputViewForAgent("agent-42", manager, styles)
	view.Resize(80, 24)
	view.refreshContent()

	content := view.viewport.View()

	// Verify NO timestamps appear
	timestamps := extractTimestamps(content)
	if len(timestamps) > 0 {
		t.Errorf("expected no timestamps for pure output stream, got %d: %v", len(timestamps), timestamps)
	}

	// Verify content is still rendered
	if !strings.Contains(content, "Building project") {
		t.Error("content missing expected output lines")
	}
}

// TestEdgeCaseMultipleAgents verifies timestamp isolation per agent
func TestEdgeCaseMultipleAgents(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)

	// Register two agents with phase transitions
	manager.RegisterAgent("agent-42", 42)
	agent42 := manager.GetAgent("agent-42")
	agent42.Status = agent.StatusRunning
	agent42.IssueTitle = "Issue 42"
	manager.CaptureOutputLine(42, "> Starting workflow")

	manager.RegisterAgent("agent-43", 43)
	agent43 := manager.GetAgent("agent-43")
	agent43.Status = agent.StatusRunning
	agent43.IssueTitle = "Issue 43"
	manager.CaptureOutputLine(43, "> Starting workflow")

	// Create separate views for each agent
	view42 := NewOutputViewForAgent("agent-42", manager, styles)
	view42.Resize(80, 24)
	view42.refreshContent()

	view43 := NewOutputViewForAgent("agent-43", manager, styles)
	view43.Resize(80, 24)
	view43.refreshContent()

	// Verify each view has its own timestamps
	content42 := view42.viewport.View()
	content43 := view43.viewport.View()

	timestamps42 := extractTimestamps(content42)
	timestamps43 := extractTimestamps(content43)

	if len(timestamps42) != 1 {
		t.Errorf("agent-42 expected 1 timestamp, got %d", len(timestamps42))
	}
	if len(timestamps43) != 1 {
		t.Errorf("agent-43 expected 1 timestamp, got %d", len(timestamps43))
	}

	// Verify timestamps are independent (different times possible)
	// Both should be valid timestamp format
	for _, ts := range timestamps42 {
		if !isValidTimestampFormat(ts) {
			t.Errorf("agent-42 invalid timestamp format: %q", ts)
		}
	}
	for _, ts := range timestamps43 {
		if !isValidTimestampFormat(ts) {
			t.Errorf("agent-43 invalid timestamp format: %q", ts)
		}
	}
}

// TestEdgeCaseEmptyOutput verifies behavior with no output
func TestEdgeCaseEmptyOutput(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)

	manager.RegisterAgent("agent-42", 42)
	agentState := manager.GetAgent("agent-42")
	agentState.Status = agent.StatusRunning
	agentState.IssueTitle = "Test issue"

	// No output added to OutputCapture

	view := NewOutputViewForAgent("agent-42", manager, styles)
	view.Resize(80, 24)

	// Should not panic
	view.refreshContent()
	content := view.viewport.View()

	// Should show "No captured output yet" message
	if !strings.Contains(content, "No captured output yet") {
		t.Error("expected 'No captured output yet' message for empty output")
	}

	// Should have no timestamps
	timestamps := extractTimestamps(content)
	if len(timestamps) > 0 {
		t.Errorf("expected no timestamps for empty output, got %d", len(timestamps))
	}
}

// TestRapidOutputPerformance verifies no lag with high-volume output
func TestRapidOutputPerformance(t *testing.T) {
	cfg := &config.Config{
		MaxRetries:  1,
		LoadedTheme: getTestTheme(),
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)

	manager.RegisterAgent("agent-42", 42)
	agentState := manager.GetAgent("agent-42")
	agentState.Status = agent.StatusRunning
	agentState.IssueTitle = "Performance test"

	// Add 200 lines with phase transitions scattered throughout
	for i := 0; i < 200; i++ {
		if i%20 == 0 {
			manager.CaptureOutputLine(42, fmt.Sprintf("> Phase transition %d", i))
		} else {
			manager.CaptureOutputLine(42, fmt.Sprintf("Regular output line %d", i))
		}
	}

	view := NewOutputViewForAgent("agent-42", manager, styles)
	view.Resize(80, 24)

	// Measure refresh time
	start := time.Now()
	view.refreshContent()
	duration := time.Since(start)

	// Should complete in under 100ms
	if duration > 100*time.Millisecond {
		t.Errorf("refreshContent took %v, expected < 100ms (potential performance issue)", duration)
	}

	// Verify timestamps were created (should have ~10 phase transitions)
	content := view.viewport.View()
	timestamps := extractTimestamps(content)
	if len(timestamps) < 1 {
		t.Error("expected timestamps for phase transitions in rapid output")
	}
}

// Helper functions

func getTestTheme() *config.Theme {
	theme := &config.Theme{
		Name:        "Test",
		Description: "Test theme",
	}
	theme.Colors.Primary = "#00AAFF"
	theme.Colors.Secondary = "#888888"
	theme.Colors.Success = "#00AA00"
	theme.Colors.Warning = "#FFAA00"
	theme.Colors.Error = "#FF0000"
	theme.Colors.TextPrimary = "#FFFFFF"
	theme.Colors.TextSecondary = "#CCCCCC"
	theme.Colors.TextMuted = "#888888"
	theme.Colors.Prompt = "#00AAFF"
	theme.Colors.Separator = "#00AAFF"
	theme.Colors.Activity = "#FFFFFF"
	theme.Colors.Background = "#000000"
	theme.Colors.Surface = "#111111"
	theme.Colors.AgentSuccess = "#00AA00"
	theme.Colors.AgentFail = "#FF0000"
	theme.Colors.Timestamp = "#888888"
	return theme
}

// extractTimestamps extracts all timestamp patterns from rendered content
func extractTimestamps(content string) []string {
	var timestamps []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// Look for [YYYY-MM-DD HH:MM:SS] pattern
		start := strings.Index(line, "[20")
		if start >= 0 {
			end := strings.Index(line[start:], "]")
			if end > 0 && end <= 20 { // Reasonable length for timestamp
				timestamp := line[start : start+end+1]
				if isValidTimestampFormat(timestamp) {
					timestamps = append(timestamps, timestamp)
				}
			}
		}
	}
	return timestamps
}

// isValidTimestampFormat checks if a string matches [YYYY-MM-DD HH:MM:SS] format
func isValidTimestampFormat(s string) bool {
	if len(s) != 21 {
		return false
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return false
	}
	// Basic pattern check (not exhaustive, but sufficient for test)
	return strings.Contains(s, "-") && strings.Contains(s, ":") && strings.Count(s, "-") == 2 && strings.Count(s, ":") == 2
}
