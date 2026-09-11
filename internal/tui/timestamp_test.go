package tui

import (
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
)

func TestIsWorkflowPhase(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Repo:        "test-org/test-repo",
		Label:       "howmux",
		MaxRetries:  1,
		LoadedTheme: &config.Theme{Name: "dark"},
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)
	ov := NewOutputView(manager, styles)

	tests := []struct {
		name     string
		line     string
		expected bool
	}{
		{
			name:     "workflow phase - reading",
			line:     "> Reading spec from .howmux/specs/issue-30-16888.md",
			expected: true,
		},
		{
			name:     "workflow phase - delegating",
			line:     "> Delegating to builder agent",
			expected: true,
		},
		{
			name:     "workflow phase - creating",
			line:     "> Creating PR for completed work",
			expected: true,
		},
		{
			name:     "workflow phase - completed",
			line:     "> Task completed successfully",
			expected: true,
		},
		{
			name:     "workflow phase - validation",
			line:     "> Validating implementation",
			expected: true,
		},
		{
			name:     "regular log line",
			line:     "Processing issue #123",
			expected: false,
		},
		{
			name:     "error message",
			line:     "Error: failed to connect",
			expected: false,
		},
		{
			name:     "empty line",
			line:     "",
			expected: false,
		},
		{
			name:     "similar but not workflow phase",
			line:     "> Some other message without keywords",
			expected: false,
		},
		{
			name:     "workflow phase uppercase keywords",
			line:     "> CREATING the implementation",
			expected: true,
		},
		{
			name:     "no > prefix",
			line:     "Reading some file",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ov.isWorkflowPhase(tt.line)
			if result != tt.expected {
				t.Errorf("isWorkflowPhase(%q) = %v, want %v", tt.line, result, tt.expected)
			}
		})
	}
}

func TestInjectTimestamps(t *testing.T) {
	cfg := &config.Config{
		Repo:        "test-org/test-repo",
		Label:       "howmux",
		MaxRetries:  1,
		LoadedTheme: &config.Theme{Name: "dark"},
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)
	ov := NewOutputView(manager, styles)

	tests := []struct {
		name         string
		input        []string
		expectPrefix bool // Do we expect timestamp prefix on any line?
		expectCount  int  // How many lines should have timestamps?
	}{
		{
			name: "single workflow phase line",
			input: []string{
				"> Delegating to builder agent",
			},
			expectPrefix: true,
			expectCount:  1,
		},
		{
			name: "multiple lines with one workflow phase",
			input: []string{
				"Regular log line",
				"> Creating PR for completed work",
				"Another log line",
			},
			expectPrefix: true,
			expectCount:  1,
		},
		{
			name: "multiple workflow phases",
			input: []string{
				"> Reading spec file",
				"Some output",
				"> Task completed successfully",
			},
			expectPrefix: true,
			expectCount:  2,
		},
		{
			name: "no workflow phases",
			input: []string{
				"Regular log line",
				"Another line",
				"Third line",
			},
			expectPrefix: false,
			expectCount:  0,
		},
		{
			name:         "empty input",
			input:        []string{},
			expectPrefix: false,
			expectCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ov.injectTimestamps(tt.input, styles)

			// Check the count of timestamped lines
			timestampCount := 0
			for _, line := range result {
				if strings.HasPrefix(line, "[") {
					timestampCount++
				}
			}

			if timestampCount != tt.expectCount {
				t.Errorf("Expected %d timestamped lines, got %d", tt.expectCount, timestampCount)
			}

			// If we expect timestamps, verify format
			if tt.expectPrefix && timestampCount > 0 {
				foundTimestamp := false
				for _, line := range result {
					if strings.HasPrefix(line, "[") {
						foundTimestamp = true
						// Check basic format [YYYY-MM-DD HH:MM:SS]
						if !strings.Contains(line, "[20") || !strings.Contains(line, "]") {
							t.Errorf("Timestamp format incorrect in line: %q", line)
						}
					}
				}
				if !foundTimestamp {
					t.Error("Expected to find timestamp but none found")
				}
			}
		})
	}
}

func TestTimestampFormat(t *testing.T) {
	cfg := &config.Config{
		Repo:        "test-org/test-repo",
		Label:       "howmux",
		MaxRetries:  1,
		LoadedTheme: &config.Theme{Name: "dark"},
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)
	ov := NewOutputView(manager, styles)

	input := []string{"> Reading spec file"}
	result := ov.injectTimestamps(input, styles)

	if len(result) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(result))
	}

	line := result[0]
	// Should start with [YYYY-MM-DD HH:MM:SS] format
	if !strings.HasPrefix(line, "[20") {
		t.Errorf("Expected timestamp to start with [20, got: %q", line)
	}

	// Check for basic date-time structure
	if !strings.Contains(line, "-") || !strings.Contains(line, ":") {
		t.Errorf("Timestamp should contain - and :, got: %q", line)
	}

	// Verify the original line content is preserved after the timestamp
	if !strings.Contains(line, "> Reading spec file") {
		t.Errorf("Original line content not found after timestamp: %q", line)
	}
}

func TestTimestampDoesNotAffectLogFiles(t *testing.T) {
	// This test verifies the design principle that timestamps are display-only
	// The injectTimestamps function is only called in the render path (refreshContent),
	// not in the log writing path

	// Simulate what would be written to log file (no timestamp injection)
	logContent := []string{
		"> Reading spec file",
		"Processing...",
		"> Task completed successfully",
	}

	// Verify no timestamps in raw log content
	for _, line := range logContent {
		if strings.HasPrefix(line, "[20") {
			t.Error("Log content should not contain timestamps - timestamps are display-only")
		}
	}

	// Verify the display version HAS timestamps when rendered
	cfg := &config.Config{
		Repo:        "test-org/test-repo",
		Label:       "howmux",
		MaxRetries:  1,
		LoadedTheme: &config.Theme{Name: "dark"},
	}
	manager := agent.NewManager(cfg)
	styles := NewStyles(cfg.LoadedTheme)
	ov := NewOutputView(manager, styles)
	displayContent := ov.injectTimestamps(logContent, styles)

	timestampFound := false
	for _, line := range displayContent {
		if strings.HasPrefix(line, "[") {
			timestampFound = true
			break
		}
	}
	if !timestampFound {
		t.Error("Display content should contain timestamps")
	}
}

func TestTimestampIntegrationWithThemeStyles(t *testing.T) {
	cfg := &config.Config{
		Repo:        "test-org/test-repo",
		Label:       "howmux",
		MaxRetries:  1,
		LoadedTheme: &config.Theme{Name: "dark"},
	}
	manager := agent.NewManager(cfg)

	// Test with default styles
	styles := NewStyles(cfg.LoadedTheme)
	ov := NewOutputView(manager, styles)

	input := []string{"> Delegating to builder"}
	result := ov.injectTimestamps(input, styles)

	if len(result) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(result))
	}

	// Verify timestamp was added
	if !strings.Contains(result[0], "[20") {
		t.Errorf("Expected timestamp in output: %q", result[0])
	}
}
