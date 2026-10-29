package tui

import (
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
)

// TestWithStartupNotices: a pre-TUI warning must land in the activity pane
// (live and the saved console-tab copy), since the alt screen hides stderr.
func TestWithStartupNotices(t *testing.T) {
	cfg := &config.Config{Theme: "dark", MaxRetries: 1, LoadedTheme: &config.Theme{Name: "dark"}}
	m := newModel(nil, agent.NewManager(cfg), cfg, nil, nil, "")
	before := len(m.activityLines)
	beforeConsole := len(m.consoleState.activityLines)

	m = m.withStartupNotices([]string{"", "PR-review: could not list kiro-cli models"})

	if got := len(m.activityLines) - before; got != 1 {
		t.Fatalf("added %d activity lines, want 1 (empty notices are skipped)", got)
	}
	if !strings.Contains(m.activityLines[len(m.activityLines)-1], "could not list kiro-cli models") {
		t.Errorf("last activity line = %q, want the notice", m.activityLines[len(m.activityLines)-1])
	}
	if got := len(m.consoleState.activityLines) - beforeConsole; got != 1 {
		t.Errorf("console-tab copy gained %d lines, want 1", got)
	}
}
