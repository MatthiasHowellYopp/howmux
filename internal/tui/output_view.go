package tui

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/agent"
)

// OutputView displays agent output in a scrollable view
type OutputView struct {
	viewport       viewport.Model
	manager        *agent.Manager
	styles         *Styles
	width          int
	height         int
	cachedOutput   []string
	agentID        string               // Filter output by this agent ID, empty string shows all
	lastGen        uint64               // last observed OutputCapture generation
	lineTimestamps map[string]time.Time // Cache of phase-line timestamps, key: "<issue>:<contentHash>"
}

// SetStyles updates the styles used by this view.
func (ov *OutputView) SetStyles(styles *Styles) {
	ov.styles = styles
}

// NewOutputView creates a new output view for all agents
func NewOutputView(manager *agent.Manager, styles *Styles) *OutputView {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	return &OutputView{
		viewport:       vp,
		manager:        manager,
		styles:         styles,
		agentID:        "", // Empty means show all agents
		lineTimestamps: make(map[string]time.Time),
	}
}

// NewOutputViewForAgent creates a new output view filtered to a specific agent
func NewOutputViewForAgent(agentID string, manager *agent.Manager, styles *Styles) *OutputView {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	return &OutputView{
		viewport:       vp,
		manager:        manager,
		styles:         styles,
		agentID:        agentID,
		lineTimestamps: make(map[string]time.Time),
	}
}

// Init initializes the output view
func (ov *OutputView) Init() tea.Cmd {
	return nil
}

// Update handles messages for the output view
func (ov *OutputView) Update(msg tea.Msg) (*OutputView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		ov.width = msg.Width
		ov.height = msg.Height
		ov.viewport = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(msg.Height))
		ov.refreshContent()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up":
			ov.viewport.ScrollUp(1)
		case "down":
			ov.viewport.ScrollDown(1)
		case "pgup":
			ov.viewport.HalfPageUp()
		case "pgdown":
			ov.viewport.HalfPageDown()
		case "home":
			ov.viewport.GotoTop()
		case "end":
			ov.viewport.GotoBottom()
		case "ctrl+y":
			// Copy the underlying output text (not the rendered viewport, which
			// is padded/styled/visible-only) so the clipboard gets the real,
			// complete output.
			content := strings.Join(ov.cachedOutput, "\n")
			if content != "" {
				CopyToClipboard(content)
			}
		}
	}

	var cmd tea.Cmd
	ov.viewport, cmd = ov.viewport.Update(msg)
	return ov, cmd
}

// View renders the output view
func (ov *OutputView) View() string {
	if ov.width == 0 || ov.height == 0 {
		return ""
	}

	gen := ov.manager.GetOutputGeneration()
	if gen != ov.lastGen {
		ov.lastGen = gen
		ov.refreshContent()
	}
	return ov.viewport.View()
}

// Resize updates the output view dimensions
func (ov *OutputView) Resize(width, height int) {
	ov.width = width
	ov.height = height
	ov.viewport = viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	ov.lastGen = 0 // Force refresh on next View()
	ov.refreshContent()
}

// refreshContent updates the viewport content with latest agent output
func (ov *OutputView) refreshContent() {
	agents := ov.manager.List()
	capturedLines := ov.manager.GetOutputLines()

	// If filtering by agent ID, only show that agent
	if ov.agentID != "" {
		var filteredAgents []*agent.Agent
		for _, agentItem := range agents {
			if agentItem.ID == ov.agentID {
				filteredAgents = []*agent.Agent{agentItem}
				break
			}
		}
		agents = filteredAgents
	}

	if len(agents) == 0 {
		if len(capturedLines) == 0 {
			content := ov.styles.Warning.Render("No agents running. Use 'watch start' to begin monitoring issues.")
			ov.viewport.SetContent(content)
			return
		}

		// If filtering by agent ID but agent not found, show appropriate message
		if ov.agentID != "" {
			content := ov.styles.Warning.Render(fmt.Sprintf("Agent %s not found or no longer running.", ov.agentID))
			ov.viewport.SetContent(content)
			return
		}

		content := strings.Join(capturedLines, "\n")
		ov.viewport.SetContent(content)
		return
	}

	var output []string

	for _, agentItem := range agents {
		// Agent header with status indicator
		statusIndicator := "●"
		statusStyle := ov.styles.Success
		switch agentItem.Status {
		case agent.StatusRunning:
			statusStyle = ov.styles.Success
		case agent.StatusCompleted:
			statusStyle = ov.styles.Activity
		case agent.StatusFailed:
			statusStyle = ov.styles.Error
		}

		header := fmt.Sprintf("%s Agent %s - Issue #%d: %s",
			statusStyle.Render(statusIndicator),
			agentItem.ID,
			agentItem.IssueNumber,
			agentItem.IssueTitle)

		output = append(output, header)

		agentPrefix := fmt.Sprintf("[agent issue-%d] ", agentItem.IssueNumber)
		agentOutput := make([]string, 0)
		for _, line := range capturedLines {
			if strings.HasPrefix(line, agentPrefix) {
				agentOutput = append(agentOutput, strings.TrimPrefix(line, agentPrefix))
			}
		}
		if len(agentOutput) == 0 {
			agentOutput = []string{
				"No captured output yet.",
			}
		}

		// Wrap and indent agent output, injecting timestamps at phase transitions
		// Phase detection now uses structured events instead of text heuristics
		for _, line := range agentOutput {
			// Check for phase transition based on structured events
			isPhase, eventType, eventTimestamp := ov.detectPhaseTransitionFromEvents(agentItem.ID, line)

			if isPhase {
				// Key the cache by the line's IDENTITY (issue + event type + content hash),
				// not its slice position. The output comes from a fixed-size ring
				// buffer, so a line's index shifts as older lines scroll off;
				// keying by index would pin a cached timestamp to a position that
				// later holds a different line, showing a stale timestamp. Hashing
				// the content keeps a phase line's first-seen timestamp stable
				// across redraws and buffer wraps, and distinct phase lines get
				// distinct timestamps. Event type is included to prevent collision
				// between different event types on the same line content.
				cacheKey := fmt.Sprintf("%d:%s:%08x", agentItem.IssueNumber, eventType, hashLine(line))

				// If no cached timestamp exists for this line, store event timestamp
				if _, exists := ov.lineTimestamps[cacheKey]; !exists {
					ov.lineTimestamps[cacheKey] = eventTimestamp
				}

				// Prepend timestamp to the line with event type indicator
				timestamp := ov.lineTimestamps[cacheKey]
				timestampStr := formatTimestampWithEventType(timestamp, eventType)
				styledTimestamp := ov.styles.Timestamp.Render(timestampStr)
				line = styledTimestamp + " " + line
			}

			wrapped := ov.wrapText(line, ov.width-4)
			for _, wrappedLine := range wrapped {
				output = append(output, "  "+wrappedLine)
			}
		}

		// Add separator between agents (only when showing multiple agents)
		if len(agents) > 1 {
			output = append(output, "")
			output = append(output, ov.styles.Separator.Render(strings.Repeat("─", ov.width)))
			output = append(output, "")
		}
	}

	content := strings.Join(output, "\n")
	ov.viewport.SetContent(content)

	// Intentionally do not force-scroll here; preserve the user's scroll position.
}

// detectPhaseTransitionFromEvents checks if a line corresponds to a structured
// tool_call or plan event from the ACP stream. This replaces the previous
// text-based heuristic with event-driven phase detection.
//
// Returns (isPhase, eventType, timestamp) where:
//   - isPhase: true if an event matches this line
//   - eventType: the event type ("tool_call" or "plan")
//   - timestamp: the event's timestamp (for display)
//
// Since output lines don't have timestamps, we use a pragmatic heuristic:
// 1. Check if the line looks like a phase transition (contains phase markers)
// 2. If yes, check if there are any tool_call/plan events for this agent
// 3. If events exist, return true with the most recent matching event's timestamp
// 4. If no events exist, return false (no timestamp will be shown)
func (ov *OutputView) detectPhaseTransitionFromEvents(agentID string, line string) (bool, string, time.Time) {
	// First, check if this line looks like a phase transition using lightweight heuristics
	// This prevents us from querying events for every single output line
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, ">") {
		// Not a phase marker line at all
		return false, "", time.Time{}
	}

	// Line looks like it could be a phase transition - check for matching events
	// Get all events for this agent (lock-guarded via Manager.GetAgentEvents)
	events := ov.manager.GetAgentEvents(agentID)
	if len(events) == 0 {
		// No events captured yet - no timestamp to show
		return false, "", time.Time{}
	}

	// Find the most recent tool_call or plan event
	// We iterate backwards to find the most recent matching event
	var mostRecentEvent *time.Time
	var mostRecentType string

	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type == "tool_call" || event.Type == "plan" {
			mostRecentEvent = &event.Timestamp
			mostRecentType = event.Type
			break
		}
	}

	if mostRecentEvent == nil {
		// No tool_call or plan events found
		return false, "", time.Time{}
	}

	// We have a phase-marker line and a matching event
	return true, mostRecentType, *mostRecentEvent
}

// formatTimestamp formats a timestamp in the fixed format [YYYY-MM-DD HH:MM:SS]
func formatTimestamp(t time.Time) string {
	return t.Format("[2006-01-02 15:04:05]")
}

// formatTimestampWithEventType formats a timestamp with an event type indicator
// - [YYYY-MM-DD HH:MM:SS] 🔧 for tool_call
// - [YYYY-MM-DD HH:MM:SS] 📋 for plan
func formatTimestampWithEventType(t time.Time, eventType string) string {
	timestamp := t.Format("[2006-01-02 15:04:05]")
	switch eventType {
	case "tool_call":
		return timestamp + " 🔧"
	case "plan":
		return timestamp + " 📋"
	default:
		return timestamp
	}
}

// hashLine returns a stable hash of a line's content, used to key the timestamp
// cache by line identity rather than by its (unstable) position in the output
// ring buffer.
func hashLine(line string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(line))
	return h.Sum32()
}

// wrapText wraps text to fit within the specified width
func (ov *OutputView) wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	if len(text) <= width {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	currentLine := words[0]

	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}
