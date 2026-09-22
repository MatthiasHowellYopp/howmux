package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/github"
	"github.com/matthiashowellyopp/howmux/internal/incidents"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/session"
)

type runningWatcher interface {
	Running() bool
}

func watcherIsRunning(w any) bool {
	rw, ok := w.(runningWatcher)
	return ok && rw.Running()
}

func (m model) handleWatch(action string) (model, tea.Cmd) {
	switch strings.ToLower(action) {
	case "start":
		if watcherIsRunning(any(m.watcher)) {
			m = m.appendActivity(m.styles.Warning.Render("Watcher already running"))
			return m, nil
		}
		// Update log position to current end before starting watcher
		if info, err := m.logReader.Stat(); err == nil {
			m.lastLogPos = info.Size()
		}
		m.watcher.Start()
		m = m.appendActivity(m.styles.Success.Render("Watcher started"))
	case "stop":
		if !watcherIsRunning(any(m.watcher)) {
			m = m.appendActivity(m.styles.Warning.Render("Watcher not running"))
			return m, nil
		}
		m.watcher.Stop()
		m = m.appendActivity(m.styles.Success.Render("Watcher stopped"))
	default:
		m = m.appendActivity(m.styles.Error.Render("Usage: watch start|stop"))
	}
	return m, nil
}

func (m model) handleStatus() (model, tea.Cmd) {
	agents := m.manager.List()
	content := []string{}

	// Add tab information section
	tabs := m.tabManager.GetTabs()
	activeTabIndex := m.tabManager.GetActiveTabIndex()

	content = append(content, m.styles.Prompt.Render("Active Tab"))
	if len(tabs) > 0 {
		activeTab := m.tabManager.GetActiveTab()
		content = append(content, fmt.Sprintf("  Current: %s", activeTab.Title()))
		content = append(content, fmt.Sprintf("  Type: %s", getTabTypeName(activeTab.Type())))
	} else {
		content = append(content, "  No tabs")
	}

	content = append(content, "")
	content = append(content, m.styles.Prompt.Render(fmt.Sprintf("Tabs (%d open)", len(tabs))))

	for i, tab := range tabs {
		indicator := "  "
		if i == activeTabIndex {
			indicator = "* "
		}
		closable := ""
		if tab.IsClosable() {
			closable = " (closable)"
		}
		content = append(content, fmt.Sprintf("%s%s%s", indicator, tab.Title(), closable))
	}

	if len(tabs) > 1 {
		content = append(content, "")
		content = append(content, m.styles.Prompt.Render("Navigation"))
		content = append(content, "  F2 - Toggle between main and first agent tab")
		content = append(content, "  [ - Previous tab")
		content = append(content, "  ] - Next tab")
	}

	// Filter running agents for interactive selection
	runningAgents := []*agent.Agent{}
	stoppedAgents := []*agent.Agent{}
	for _, a := range agents {
		if a.Status == agent.StatusRunning {
			runningAgents = append(runningAgents, a)
		} else {
			stoppedAgents = append(stoppedAgents, a)
		}
	}

	// Sort running agents by issue number for deterministic ordering
	sort.Slice(runningAgents, func(i, j int) bool {
		return runningAgents[i].IssueNumber < runningAgents[j].IssueNumber
	})

	// Store snapshot so number key selection references the same order
	m.statusRunningAgents = runningAgents

	if len(runningAgents) > 0 {
		content = append(content, "", m.styles.Prompt.Render("Running Agents"))
		content = append(content, "Press number to open view:")
		content = append(content, "")

		// Scale title truncation to available overlay width
		titleMax := m.getOverlayContentWidth() - 25 // Reserve space for number, issue#, status, elapsed
		if titleMax < 15 {
			titleMax = 15
		}

		// Display up to 9 running agents with numbers
		for i, a := range runningAgents {
			if i >= 9 { // Only support 1-9 for simplicity
				break
			}
			elapsed := time.Since(a.StartTime).Truncate(time.Second)
			line := fmt.Sprintf("  %d. Issue #%d: %s (%s, %s)",
				i+1, a.IssueNumber, truncate(a.IssueTitle, titleMax), string(a.Status), elapsed)
			content = append(content, line)
		}

		if len(runningAgents) > 9 {
			content = append(content, fmt.Sprintf("  ... and %d more", len(runningAgents)-9))
		}
	}

	if len(stoppedAgents) > 0 {
		content = append(content, "", m.styles.Prompt.Render("Stopped Agents:"))
		titleMax := m.getOverlayContentWidth() - 22 // Reserve space for issue#, status, elapsed
		if titleMax < 15 {
			titleMax = 15
		}
		for _, a := range stoppedAgents {
			elapsed := time.Since(a.StartTime).Truncate(time.Second)
			line := fmt.Sprintf("   Issue #%d: %s (%s, %s)",
				a.IssueNumber, truncate(a.IssueTitle, titleMax), string(a.Status), elapsed)
			content = append(content, line)
		}
	}

	if len(agents) == 0 {
		content = append(content, "", m.styles.Warning.Render("No agents running"))
	}

	m = m.activateOverlay(overlayStatus, "System Status", content)
	return m, nil
}

func (m model) handleStop(issueStr string) (model, tea.Cmd) {
	issueNum, err := strconv.Atoi(issueStr)
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid issue number: %s", issueStr)))
		return m, nil
	}

	agents := m.manager.List()
	for _, a := range agents {
		if a.IssueNumber == issueNum {
			if err := m.manager.Stop(a.ID); err != nil {
				m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Error stopping agent: %v", err)))
			} else {
				m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Stopped agent for issue %d", issueNum)))
			}
			return m, nil
		}
	}
	m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("No agent running for issue %d", issueNum)))
	return m, nil
}

func (m model) handleHelp() (model, tea.Cmd) {
	content := []string{
		m.styles.Prompt.Render("Available commands:"),
		"  watch start    - Start watching for labeled issues",
		"  watch stop     - Stop watching",
		"  status         - List all agents with details",
		"  stop <issue>   - Stop agent for specific issue number",
		"  plan [desc]    - Create new ACP-based Planning tab",
		"  plan classic [desc] - Start legacy subprocess planning session",
		"  log [level] [size] - Open log viewer (level: debug/info/warn/error, size: buffer lines)",
		"  logs           - View incident logs",
		"  decide <value>  - Launch action on selected review (post confirms inline, others run immediately)",
		"  theme          - Show current theme",
		"  theme <name>   - Switch to theme",
		"  about          - Show version information and check for updates",
		"  exit           - Exit (Ctrl+C also works)",
		"  help           - Show this help message",
		"",
		m.styles.Prompt.Render("Hotkeys:"),
		"  F2             - Toggle between console and agent output views",
		"  Ctrl+Alt+P     - Toggle between console and planning modes",
		"  Tab / Shift+Tab- Toggle focus: command line <-> planning message input",
		"  Ctrl+Y         - Copy conversation/output text to clipboard",
		"  Ctrl+V         - Paste into the message input (when focused)",
		"  Ctrl+C         - Quit",
	}

	m = m.activateOverlay(overlayHelp, "Help", content)
	return m, nil
}

func (m model) handlePlan(description string) (model, tea.Cmd) {
	// Check tab limit before creating new planning tab
	if !m.tabManager.CanCreatePlanningTab() {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Maximum %d concurrent planning tabs reached", MaxPlanningTabs)))
		return m, nil
	}

	// Generate tab title based on description or use default
	var tabTitle string
	if description != "" {
		// Use first few words of description as title
		words := strings.Fields(description)
		if len(words) > 3 {
			tabTitle = strings.Join(words[:3], " ") + "..."
		} else {
			tabTitle = description
		}
	} else {
		// Count existing planning tabs for naming
		existingCount := 0
		for _, tab := range m.tabManager.GetTabs() {
			if tab.Type() == TabTypePlanning {
				existingCount++
			}
		}
		tabTitle = fmt.Sprintf("Planning %d", existingCount+1)
	}

	// Create ACP-based planning tab with comprehensive error handling
	planningTab, err := m.tabManager.CreateAndAddPlanningTab(
		m.styles,
		m.footerManager.GetContextTracker(),
		m.sessionManager,
	)
	if err != nil {
		// Provide detailed error message based on error type
		var errorMsg string
		if strings.Contains(err.Error(), "ACP") {
			errorMsg = fmt.Sprintf("ACP connection failed: %v\nNote: Kiro CLI must be installed and accessible for ACP-based planning.", err)
		} else if strings.Contains(err.Error(), "session") {
			errorMsg = fmt.Sprintf("Session management failed: %v\nPlanning tab may not persist across restarts.", err)
		} else {
			errorMsg = fmt.Sprintf("Failed to create planning tab: %v", err)
		}

		m = m.appendActivity(m.styles.Error.Render(errorMsg))

		// Offer fallback to classic planning if ACP is unavailable
		if strings.Contains(err.Error(), "ACP") || strings.Contains(err.Error(), "kiro-cli") {
			m = m.appendActivity(m.styles.Warning.Render("Consider using 'plan classic [description]' for subprocess-based planning"))
		}
		return m, nil
	}

	// Set the tab title if description was provided
	if description != "" {
		planningTab.SetTitle(tabTitle)
	}

	// Start context tracking for the new planning session with error handling
	// NOTE: Tab switch will handle context tracking automatically

	// Switch to the newly created tab (already added by CreateAndAddPlanningTab)
	var focusCmd tea.Cmd
	m, focusCmd = m.switchActiveTab(len(m.tabManager.GetTabs()) - 1)

	// UX: `plan` is the one command that auto-switches focus to the message
	// input. Typing `plan` signals intent to type an idea next, so land the
	// cursor in the planning window instead of the footer. Every other way of
	// creating/opening a planning tab keeps the Model-A footer-focused default;
	// Tab/Esc still toggle back to the footer here. Routed through the single
	// focus helper so all three focus stores stay in sync.
	messageFocusCmd := m.setPlanningFocus(FocusTargetMessage)

	// Add initial message with connection status feedback
	if description != "" {
		planningTab.AddMessage("user", description)
		planningTab.AddMessage("system", "💡 Planning tab ready. ACP connection will be established when you send your first message.")
	} else {
		planningTab.AddMessage("system", "🚀 ACP-based Planning Tab ready. Type your message to start planning.")
		planningTab.AddMessage("system", "📝 Tab/Esc switch focus between the message input and the footer command line.")
	}

	// Update session with initial state
	planningTab.SaveSession()

	m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("✅ Created planning tab: %s", planningTab.Title())))

	return m, tea.Batch(focusCmd, messageFocusCmd)
}

func (m model) handlePlanClassic(description string) (model, tea.Cmd) {
	// Use the legacy subprocess-based planning functionality
	return m.handlePlanSubprocess(description)
}

func (m model) handlePlanSubprocess(description string) (model, tea.Cmd) {
	// Suspend agent output capture before entering planning mode
	m.manager.SuspendOutputCapture()

	// Preserve current console state
	m.consoleState.inputValue = m.input.Value()
	m.consoleState.activityLines = make([]string, len(m.activityLines))
	copy(m.consoleState.activityLines, m.activityLines)
	if activeTab := m.tabManager.GetActiveTab(); activeTab != nil {
		m.consoleState.activeTabID = activeTab.ID()
	}

	// Check for existing planning sessions with error recovery
	sessions, err := m.sessionManager.List()
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to check sessions: %v", err)))
		return m, nil
	}

	// Look for existing planning session with corruption handling
	var planningSessionID string
	for _, sessionID := range sessions {
		state, err := m.sessionManager.Load(sessionID)
		if err != nil {
			if strings.Contains(err.Error(), "corruption") {
				m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Removing corrupted session %s", sessionID[:8])))
				_ = m.sessionManager.Delete(sessionID)
			}
			continue
		}
		if state.Type == session.Planning {
			planningSessionID = sessionID
			break
		}
	}

	var sessionMsg string
	if planningSessionID == "" {
		sessionMsg = "Starting new classic planning session"
	} else {
		sessionMsg = fmt.Sprintf("Resuming planning session %s...", planningSessionID[:8])
	}

	// Start context tracking for planning mode with default model
	m.footerManager.GetContextTracker().StartPlanningSession("claude-sonnet-4")

	m = m.appendActivity(m.styles.Success.Render(sessionMsg))
	m.currentMode = session.Planning
	m.input.Blur()

	// Execute the kiro-cli subprocess for classic planning with shell wrapper
	args := []string{"chat", "--classic", "--agent", "planner"}
	if description != "" {
		args = append(args, description)
	}

	// Wrap in shell with clear and centered ASCII art banner
	banner := `cols=$(tput cols 2>/dev/null || echo 80)
art1="  _  ___              _  __                   "
art2=" | |/ (_)_ __ ___    | |/ /_ __ _____      __"
art3=" | ' /| | '__/ _ \   | ' /| '__/ _ \ \ /\ / /"
art4=" | . \| | | | (_) |  | . \| | |  __/\ V  V / "
art5=" |_|\_\_|_|  \___/   |_|\_\_|  \___| \_/\_/  "
pad() { w=${#1}; p=$(( (cols - w) / 2 )); [ "$p" -lt 0 ] && p=0; printf "%*s%s\n" "$p" "" "$1"; }
echo ""
pad "$art1"
pad "$art2"
pad "$art3"
pad "$art4"
pad "$art5"
echo ""`
	script := "clear && " + banner + " && exec kiro-cli \"$@\""
	cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...)

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execDoneMsg{err: err}
	})
}

type execDoneMsg struct {
	err error
}

// bodyEditCommandFunc is the subprocess-execution seam handleBodyEditLaunch
// invokes through to build the $EDITOR exec.Cmd, mirroring
// execCommandFunc's role in internal/review/decisionwriter.go. It is
// package-level so tests can substitute a fake command (e.g. "/bin/true" or
// "/bin/false") instead of spawning a real editor, and assert the argv
// (editor binary + args, temp file path) that was actually built.
var bodyEditCommandFunc = exec.Command

// handleBodyEditLaunch implements the "e" key's $EDITOR-launch flow for a
// selected review's full body, structurally mirroring handlePlanSubprocess:
// suspend output capture, write state that must survive the subprocess
// (here: the body, into a temp file — there is no console-chrome state to
// snapshot for body editing), build the exec.Cmd, and return a
// tea.ExecProcess whose callback reports back via editorDoneMsg (a distinct
// message from execDoneMsg — see editorDoneMsg's doc comment in tui.go).
//
// $EDITOR is read fresh from the environment on every invocation (not
// cached), matching how other CLI tools resolve it. If $EDITOR is unset, an
// activity-line error is appended and nil is returned immediately — no
// subprocess is launched, no temp file is created, and
// SuspendOutputCapture is never called, so the TUI's state is completely
// unaffected by a no-op "e" press when $EDITOR isn't configured.
//
// $EDITOR is split on whitespace before building the command, so values
// like "code -w" (editor + flags) work the same way most CLI tools that
// shell out to $EDITOR already support — the first field is the binary,
// the rest are prepended arguments, with the temp file path appended last.
//
// Limitation (by design): this whitespace split does NOT handle a $EDITOR
// whose binary path or an argument contains spaces (e.g.
// "/Applications/My Editor.app/Contents/MacOS/editor") or shell-quoted
// arguments — the first whitespace-delimited fragment is taken as the
// binary, so such a value fails with "executable file not found" on that
// fragment. This matches how many CLI tools treat $EDITOR; users with a
// spaced editor path should point $EDITOR at a space-free wrapper/symlink.
// Full shell-word parsing is intentionally out of scope here.
//
// The temp file written here doubles as the "backup" of the pre-edit body:
// nothing is written to the spool file itself until BodyWriter.SetBody is
// called with a validated, non-empty temp-file path (see the editorDoneMsg
// case arm in tui.go), so a crashed or misbehaving editor can never
// corrupt the real spool file, only the disposable temp file.
func (m model) handleBodyEditLaunch(rec review.Record) (model, tea.Cmd) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		m = m.appendActivity(m.styles.Error.Render("$EDITOR is not set — cannot edit review body"))
		return m, nil
	}

	homeDir, err := userHomeDirFunc()
	if err != nil {
		homeDir = ""
	}
	body, _ := review.ReadSpoolBody(rec.SpoolPath, homeDir)

	tempFile, err := os.CreateTemp("", "howmux-review-*.md")
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to create temp file for editing: %v", err)))
		return m, nil
	}
	tempPath := tempFile.Name()

	if _, err := tempFile.WriteString(body); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to write review body to temp file: %v", err)))
		return m, nil
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to write review body to temp file: %v", err)))
		return m, nil
	}

	m.manager.SuspendOutputCapture()

	editorFields := strings.Fields(editor)
	editorBin := editorFields[0]
	args := append(append([]string{}, editorFields[1:]...), tempPath)
	cmd := bodyEditCommandFunc(editorBin, args...)

	m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Opening $EDITOR for PR #%d review body...", rec.PR)))

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{tempPath: tempPath, rec: rec, err: err}
	})
}

type reviewStartMsg struct {
	cancel context.CancelFunc
}

type reviewCompleteMsg struct {
	repo      string
	pr        int
	spoolPath string
	err       error
	cancel    context.CancelFunc // Cancel function for cleanup
}

type updateCheckMsg struct {
	release *github.Release
	err     error
}

// Injectable seams for testing the async review workflow
// These mirror the pattern from internal/review/runner.go

// ensureCheckoutFunc wraps review.EnsureCheckout for testability
var ensureCheckoutFunc = func(owner, repo, repoURL string, pr int, reviewDir string) error {
	return review.EnsureCheckout(owner, repo, repoURL, pr, reviewDir)
}

// getPRFunc wraps github.GetPR for testability
var getPRFunc = func(repo string, pr int) (github.PR, error) {
	return github.GetPR(repo, pr)
}

// runReviewFunc wraps review.RunReview for testability
var runReviewFunc = func(ctx context.Context, rec review.Record, headSHA string, storeImpl review.StoreInterface, tabWriter io.Writer) error {
	return review.RunReview(ctx, rec, headSHA, storeImpl, tabWriter)
}

// userHomeDirFunc wraps os.UserHomeDir for testability
var userHomeDirFunc = func() (string, error) {
	return os.UserHomeDir()
}

// spoolInfoForFunc wraps review.ReadSpoolInfo for testability
var spoolInfoForFunc = func(spoolPath, homeDir string) review.SpoolInfo {
	return review.ReadSpoolInfo(spoolPath, homeDir)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func (m model) getOverlayContentWidth() int {
	overlayWidth := int(float64(m.width) * 0.6)
	if overlayWidth < 40 {
		overlayWidth = 40
	}

	// Ensure overlay doesn't exceed screen bounds (mirrors renderOverlay()).
	if overlayWidth >= m.width {
		overlayWidth = m.width - 2
	}

	contentWidth := overlayWidth - 6 // Account for border and padding
	if contentWidth < 1 {
		contentWidth = 1
	}
	return contentWidth
}

func (m model) handleAbout() (model, tea.Cmd) {
	m.aboutDialog.BuildContent()
	m.aboutDialog.UpdateStatusLine([]string{"Checking for updates..."})

	m = m.activateOverlay(overlayAbout, "Kiro-Krew Version Information", m.aboutDialog.GetFullContent())
	return m, checkForUpdateCmd()
}

func (m model) handleTheme(args []string) (model, tea.Cmd) {
	if len(args) == 0 {
		// No longer show overlay for current theme - persistent display handles this
		return m, nil
	}

	if len(args) > 1 {
		m = m.appendActivity(m.styles.Error.Render("Usage: theme [name]"))
		return m, nil
	}

	themeName := args[0]

	// Try to load the theme (this handles validation)
	theme, err := config.LoadTheme(themeName)
	if err != nil {
		available := config.GetAvailableThemes()
		m = m.appendActivity(
			m.styles.Error.Render(fmt.Sprintf("Failed to load theme '%s': %v", themeName, err)),
			m.styles.Warning.Render(fmt.Sprintf("Available themes: %s", strings.Join(available, ", "))),
		)
		return m, nil
	}

	previousTheme := m.config.Theme
	previousLoadedTheme := m.config.LoadedTheme

	// Update config
	m.config.Theme = themeName
	m.config.LoadedTheme = theme

	// Save config
	if err := m.config.Save(); err != nil {
		m.config.Theme = previousTheme
		m.config.LoadedTheme = previousLoadedTheme
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to save config: %v", err)))
		return m, nil
	}

	// Update styles with new theme
	m.styles = NewStyles(theme)

	m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Theme changed to: %s", themeName)))
	return m, tea.ClearScreen
}

func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		release, err := github.GetLatestRelease("matthiashowellyopp/howmux")
		return updateCheckMsg{release: release, err: err}
	}
}

// getTabTypeName converts TabType enum to readable string
func getTabTypeName(tabType TabType) string {
	switch tabType {
	case TabTypeMain:
		return "Main Console"
	case TabTypeAgent:
		return "Agent Output"
	case TabTypePlanning:
		return "Planning"
	case TabTypeLog:
		return "Log Viewer"
	case TabTypeReviews:
		return "Reviews"
	default:
		return "Unknown"
	}
}

// switchToPlanningMode switches from console to planning mode while preserving console state
func (m model) switchToPlanningMode() (model, tea.Cmd) {
	// Suspend agent output capture before entering planning mode
	m.manager.SuspendOutputCapture()

	// Preserve current console state
	m.consoleState.inputValue = m.input.Value()
	m.consoleState.activityLines = make([]string, len(m.activityLines))
	copy(m.consoleState.activityLines, m.activityLines)
	if activeTab := m.tabManager.GetActiveTab(); activeTab != nil {
		m.consoleState.activeTabID = activeTab.ID()
	}

	// Check if there are any active planning tabs first
	activePlanningTabs := 0
	var lastPlanningTab *PlanningTab
	for _, tab := range m.tabManager.GetTabs() {
		if tab.Type() == TabTypePlanning {
			activePlanningTabs++
			if planningTab, ok := tab.(*PlanningTab); ok {
				lastPlanningTab = planningTab
			}
		}
	}

	// If there are active planning tabs, switch to the most recent one
	if activePlanningTabs > 0 && lastPlanningTab != nil {
		// Find the index of the last planning tab and switch to it
		for i, tab := range m.tabManager.GetTabs() {
			if tab == lastPlanningTab {
				m.tabManager.SetActiveTab(i)
				break
			}
		}

		// Start context tracking if not already active
		if !m.footerManager.GetContextTracker().IsActive() {
			if err := m.footerManager.GetContextTracker().StartPlanningSessionWithValidation("claude-sonnet-4"); err != nil {
				m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Context tracking warning: %v", err)))
			}
		}

		m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Switched to active planning tab (found %d planning tabs)", activePlanningTabs)))
		m.currentMode = session.Planning
		// Model A: the footer command line stays focused/available on planning
		// tabs. Route the switch through the single focus helper so all three
		// focus stores (m.input, the tab's focusTarget, m.tabFocusStates) agree on
		// footer focus — otherwise a tab that was message-focused before switching
		// to console returns with both the message input and footer focused.
		focusCmd := m.setPlanningFocus(FocusTargetFooter)
		return m, tea.Batch(focusCmd, tea.ClearScreen)
	}

	// Check for existing planning sessions with error recovery
	sessions, err := m.sessionManager.List()
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to check sessions: %v", err)))
		// Resume output capture on failure
		m.manager.ResumeOutputCapture()
		return m, nil
	}

	// Look for existing planning session with corruption handling
	var planningSessionID string
	for _, sessionID := range sessions {
		state, err := m.sessionManager.Load(sessionID)
		if err != nil {
			if strings.Contains(err.Error(), "corruption") {
				m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Removing corrupted session %s", sessionID[:8])))
				_ = m.sessionManager.Delete(sessionID)
			}
			continue
		}
		if state.Type == session.Planning {
			planningSessionID = sessionID
			break
		}
	}

	var sessionMsg string
	if planningSessionID == "" {
		m = m.appendActivity(m.styles.Warning.Render("No active planning session or tabs"))
		m = m.appendActivity(m.styles.Warning.Render("Use 'plan [description]' to create a new planning tab"))
		// Resume output capture on failure
		m.manager.ResumeOutputCapture()
		return m, nil
	}
	sessionMsg = fmt.Sprintf("Resuming planning session %s...", planningSessionID[:8])

	// Start context tracking for planning mode with default model
	if err := m.footerManager.GetContextTracker().StartPlanningSessionWithValidation("claude-sonnet-4"); err != nil {
		m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Context tracking warning: %v", err)))
	}

	m = m.appendActivity(m.styles.Success.Render(sessionMsg))
	m.currentMode = session.Planning
	// Model A: the footer command line stays focused/available on planning tabs.
	// Do not blur it here; Tab explicitly moves focus to the message input.

	return m, tea.ClearScreen
}

// switchToConsoleMode switches from planning to console mode while preserving planning state
func (m model) switchToConsoleMode() (model, tea.Cmd) {
	// Save any active planning tab states
	activePlanningTabs := 0
	for _, tab := range m.tabManager.GetTabs() {
		if planningTab, ok := tab.(*PlanningTab); ok && planningTab.Type() == TabTypePlanning {
			activePlanningTabs++
			// Force save session state when switching away
			planningTab.SaveSession()
		}
	}

	// Stop context tracking when exiting planning mode
	m.footerManager.GetContextTracker().StopPlanningSession()

	// Resume agent output capture when returning to console mode
	m.manager.ResumeOutputCapture()

	// Switch to console mode and restore console state
	m.currentMode = session.Console
	m = m.restoreConsoleState()

	// Restore previously active tab by ID
	restored := false
	if m.consoleState.activeTabID != "" {
		for i, tab := range m.tabManager.GetTabs() {
			if tab.ID() == m.consoleState.activeTabID {
				m, _ = m.switchActiveTab(i)
				restored = true
				break
			}
		}
	}
	if !restored {
		// Fallback to main tab
		for i, tab := range m.tabManager.GetTabs() {
			if tab.Type() == TabTypeMain {
				m, _ = m.switchActiveTab(i)
				break
			}
		}
	}

	m.input.Focus()

	// Provide user feedback about mode switch
	if activePlanningTabs > 0 {
		m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Switched to console mode (%d planning tabs preserved)", activePlanningTabs)))
	} else {
		m = m.appendActivity(m.styles.Success.Render("Switched to console mode"))
	}

	return m, tea.Batch(m.input.Focus(), tea.ClearScreen)
}

// restoreConsoleState restores the console state after returning from planning mode
func (m model) restoreConsoleState() model {
	if m.consoleState != nil {
		m.input.SetValue(m.consoleState.inputValue)
		m.activityLines = make([]string, len(m.consoleState.activityLines))
		copy(m.activityLines, m.consoleState.activityLines)
	}
	m.currentMode = session.Console
	return m
}

func (m model) handleLogs() (model, tea.Cmd) {
	logger, err := incidents.NewIncidentLogger()
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to initialize logger: %v", err)))
		return m, nil
	}

	incidents, err := logger.ListIncidents()
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to list incidents: %v", err)))
		return m, nil
	}

	content := []string{}
	if len(incidents) == 0 {
		content = append(content, m.styles.Warning.Render("No incident logs found"))
	} else {
		content = append(content, m.styles.Prompt.Render(fmt.Sprintf("Found %d incident logs:", len(incidents))))
		content = append(content, "")

		for _, incident := range incidents {
			timestamp := incident.Timestamp.Format("Jan 02 15:04:05")
			line := fmt.Sprintf("Issue #%d (attempt %d) - %s", incident.IssueNumber, incident.Attempt, timestamp)
			content = append(content, line)
		}

		content = append(content, "")
		content = append(content, m.styles.Prompt.Render("Log files location:"))
		content = append(content, fmt.Sprintf("~/.howmux/logs/%s/incidents/", logger.RepoName()))
	}

	m = m.activateOverlay(overlayLogs, "Incident Logs", content)
	return m, nil
}

// handleLog opens the log viewer tab with optional level and size parameters
func (m model) handleLog(args []string) (model, tea.Cmd) {
	// Parse optional parameters: log [level] [size]
	var level string
	var bufferSize int
	var err error

	// Use config defaults
	level = m.config.Logging.DefaultLevel
	bufferSize = m.config.Logging.MaxBufferLines

	// Parse level parameter if provided
	if len(args) > 0 {
		providedLevel := strings.ToLower(args[0])
		validLevels := map[string]bool{
			"debug": true,
			"info":  true,
			"warn":  true,
			"error": true,
		}
		if !validLevels[providedLevel] {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid log level '%s'. Valid levels: debug, info, warn, error", args[0])))
			return m, nil
		}
		level = providedLevel
	}

	// Parse size parameter if provided
	if len(args) > 1 {
		bufferSize, err = strconv.Atoi(args[1])
		if err != nil || bufferSize <= 0 {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid buffer size '%s'. Must be a positive integer", args[1])))
			return m, nil
		}
	}

	// Check if a log viewer tab already exists (enforce single tab constraint)
	if m.tabManager.HasLogTab() {
		existingIndex := m.tabManager.FindLogTab()
		if len(args) > 0 {
			// User provided parameters - prompt for action
			m = m.appendActivity(
				m.styles.Warning.Render("Log viewer tab already exists"),
				m.styles.Warning.Render("Close the existing tab (Ctrl+W) first to create a new one with different parameters"),
			)
		} else {
			// No parameters provided - just navigate to existing tab
			m = m.appendActivity(m.styles.Success.Render("Switched to existing log viewer tab"))
		}
		// Switch to the existing log tab
		var cmd tea.Cmd
		m, cmd = m.switchActiveTab(existingIndex)
		return m, cmd
	}

	// Create new log viewer tab
	logTab := NewLogTab("log-viewer", level, bufferSize, m.styles)

	// Activate the logging subsystem with this log tab
	if err := m.activateLogging(logTab); err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to activate logging: %v", err)))
		return m, nil
	}

	// Add the log tab to the tab manager
	m.tabManager.AddTab(logTab)

	// Switch to the newly created log tab
	var switchCmd tea.Cmd
	m, switchCmd = m.switchActiveTab(len(m.tabManager.GetTabs()) - 1)

	// Start polling for log entries
	pollCmd := logTab.StartPolling()

	m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Log viewer opened: level=%s, buffer_size=%d", level, bufferSize)))

	return m, tea.Batch(switchCmd, pollCmd)
}

// findReviewsTab locates the singleton Reviews tab by TYPE, not by "is it
// currently active" — decide must work regardless of which tab is active
// (matching how handleReview's bare form and handleStatus already operate
// independent of the active tab), since ReviewsTab is a permanent,
// non-closable tab (IsClosable() == false, added once in newModel) and
// there is exactly one per session. Returns nil if no Reviews tab is found
// (defensive only; should not occur in practice — mirrors the nil-check
// style already used throughout tui.go, e.g. findReviewsTabIndex).
func (m model) findReviewsTab() *ReviewsTab {
	for _, tab := range m.tabManager.GetTabs() {
		if tab.Type() == TabTypeReviews {
			if rt, ok := tab.(*ReviewsTab); ok {
				return rt
			}
		}
	}
	return nil
}

// handleDecide implements the `decide post|revise|rereview|discard` REPL
// command. It validates the argument, resolves the currently selected review
// row from the singleton Reviews tab (regardless of which tab is currently
// active — see findReviewsTab), and dispatches to the shared
// dispatchDecideAction, which launches the corresponding action
// immediately (revise/rereview/discard) or opens the inline y/N confirm
// gate (post) — see decideactions.go and the design spec's "Input
// Validation Summary" and "Row-Selection Requirement Summary" sections
// (issue #85) for why these checks are layered and why selection is
// resolved by tab type rather than active-tab status.
func (m model) handleDecide(args []string) (model, tea.Cmd) {
	if len(args) != 1 {
		m = m.appendActivity(m.styles.Error.Render("Usage: decide post|revise|rereview|discard"))
		return m, nil
	}

	decision := args[0]
	switch decision {
	case "post", "revise", "rereview", "discard":
		// valid
	default:
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid decision: %s (must be post, revise, rereview, or discard)", decision)))
		return m, nil
	}

	rt := m.findReviewsTab()
	if rt == nil {
		m = m.appendActivity(m.styles.Error.Render("No review selected — switch to the Reviews tab and select a row first"))
		return m, nil
	}

	rec, ok := rt.SelectedRecord()
	if !ok {
		m = m.appendActivity(m.styles.Error.Render("No review selected — switch to the Reviews tab and select a row first"))
		return m, nil
	}

	if rec.SpoolPath == "" {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("PR #%d has no review yet — nothing to decide", rec.PR)))
		return m, nil
	}

	return m.dispatchDecideAction(rec, decision)
}

func (m model) handleReview(args []string) (model, tea.Cmd) {
	// Preflight check - blocks before any enrollment/checkout/review
	// Fix #4: Use os.UserHomeDir() instead of os.Getenv("HOME") for Windows compatibility
	homeDir, err := userHomeDirFunc()
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to get home directory: %v", err)))
		return m, nil
	}
	kiroDir := filepath.Join(homeDir, ".kiro")
	if err := review.CheckReviewAssets(kiroDir); err != nil {
		m = m.appendActivity(m.styles.Error.Render("PR-review preflight failed:"))
		m = m.appendActivity(m.styles.Error.Render(err.Error()))
		return m, nil
	}

	// Bare form: start/continue the loop over enrolled PRs
	if len(args) == 0 {
		if m.reviewWatcher.Running() {
			m = m.appendActivity(m.styles.Warning.Render("Review watcher already running"))
			return m, nil
		}
		m.reviewWatcher.Start()
		m = m.appendActivity(m.styles.Success.Render("Review watcher started"))
		return m, nil
	}

	// URL form: parse, enroll, checkout, review, and ensure loop running
	prURL := args[0]

	// Parse PR URL
	owner, repo, prNum, err := github.ResolvePRURL(prURL)
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Invalid PR URL: %v", err)))
		return m, nil
	}
	fullRepo := owner + "/" + repo

	// Create store instance - intentional separate instance from watcher's store
	// The FS store is stateless so this is harmless; both read/write the same files
	store := review.NewDefaultStore()

	// Check if already enrolled
	existing, found, err := store.Get(fullRepo, prNum)
	if err != nil {
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to check enrollment: %v", err)))
		return m, nil
	}

	var rec review.Record
	if found {
		m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("PR #%d already enrolled (status: %s)", prNum, existing.Status)))
		rec = existing

		// Fix #5: Re-review bypasses dedup - check if head SHA was already serviced
		// Fetch PR metadata for head SHA first
		prData, err := getPRFunc(fullRepo, prNum)
		if err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to fetch PR metadata: %v", err)))
			return m, nil
		}
		headSHA := prData.HeadSHA()

		// Mirror watcher.go's deduplication logic: if LastServicedRequest == current head SHA, skip
		if rec.LastServicedRequest == headSHA {
			m = m.appendActivity(m.styles.Activity.Render(fmt.Sprintf("PR #%d already reviewed at %s - nothing new to review", prNum, headSHA[:8])))
			return m, nil
		}
		// Head SHA has advanced past last serviced one, proceed with review
	} else {
		// Create new record for enrollment
		reviewDirPath := fmt.Sprintf(".worktrees/review-%s-%s-%d", owner, repo, prNum)
		rec = review.Record{
			Repo:       fullRepo,
			PR:         prNum,
			URL:        prURL,
			Status:     review.StatusWatching,
			EnrolledAt: time.Now().Format(time.RFC3339),
			ReviewDir:  reviewDirPath,
		}
		if err := store.Save(rec); err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to enroll PR: %v", err)))
			return m, nil
		}
		m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Enrolled PR #%d for review", prNum)))
	}

	// Immediate feedback before async dispatch
	m = m.appendActivity(m.styles.Activity.Render(fmt.Sprintf("Starting review of PR #%d...", prNum)))

	// Fix #1: Run the slow operations (checkout, GetPR, RunReview) asynchronously
	// Return a tea.Cmd that runs the review work in a goroutine
	ctx, cancel := context.WithCancel(context.Background())

	// Store cancel function in a closure for cleanup
	reviewCmd := func() tea.Msg {
		// Fetch PR metadata for head SHA
		prData, err := getPRFunc(fullRepo, prNum)
		if err != nil {
			return reviewCompleteMsg{repo: fullRepo, pr: prNum, err: fmt.Errorf("failed to fetch PR metadata: %w", err), cancel: cancel}
		}
		headSHA := prData.HeadSHA()

		// Ensure checkout
		repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
		if err := ensureCheckoutFunc(owner, repo, repoURL, prNum, rec.ReviewDir); err != nil {
			// Warning, not fatal - checkout can be retried later
			// Continue with review anyway
		}

		// Get fresh record (may have been updated by checkout)
		rec, found, err := store.Get(fullRepo, prNum)
		if err != nil || !found {
			return reviewCompleteMsg{repo: fullRepo, pr: prNum, err: fmt.Errorf("failed to retrieve record for review"), cancel: cancel}
		}

		// Run review with cancellable context and io.Discard for progress
		// Future enhancement: capture output to agent tab
		if err := runReviewFunc(ctx, rec, headSHA, store, io.Discard); err != nil {
			return reviewCompleteMsg{repo: fullRepo, pr: prNum, err: fmt.Errorf("review failed: %w", err), cancel: cancel}
		}

		// Get updated record to access SpoolPath
		rec, _, _ = store.Get(fullRepo, prNum)
		return reviewCompleteMsg{repo: fullRepo, pr: prNum, spoolPath: rec.SpoolPath, err: nil, cancel: cancel}
	}

	// Return both the start message (to store cancel func) and the review command
	return m, tea.Batch(
		func() tea.Msg { return reviewStartMsg{cancel: cancel} },
		reviewCmd,
	)
}
