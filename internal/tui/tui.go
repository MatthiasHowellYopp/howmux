package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	clog "github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"

	"golang.org/x/mod/semver"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/github"
	"github.com/matthiashowellyopp/howmux/internal/hotkey"
	"github.com/matthiashowellyopp/howmux/internal/logging"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/session"
	"github.com/matthiashowellyopp/howmux/internal/version"
	"github.com/matthiashowellyopp/howmux/internal/watcher"
)

type logMsg string

type tickMsg struct{}

type planningHotkeyMsg struct{}

// openReviewContentMsg is emitted by ReviewsTab.Update's "enter" case (via a
// tea.Cmd) to ask the top-level model to open a read-only review content
// window for the given PR. ReviewsTab has no reference to TabManager (by
// design, matching every other tab), so it cannot add a tab itself — this
// message/case pair is the mechanism, matching the existing pattern used
// for other cross-tab, asynchronous-shaped concerns (reviewStartMsg,
// reviewCompleteMsg, execDoneMsg). See the "case openReviewContentMsg:" arm
// in model.Update for the handler.
type openReviewContentMsg struct {
	repo      string
	pr        int
	spoolPath string
}

// decideRequestMsg is emitted by ReviewsTab.Update's p/r/R/d key handlers
// (mirroring openReviewContentMsg's pattern for enter) to ask the
// top-level model to set the decision on the currently selected review.
// ReviewsTab has no reference to DecisionWriter or the model (by design,
// matching every other cross-tab action) — see openSelectedReviewCmd's
// doc comment for why this message/tea.Cmd round trip is the mechanism.
type decideRequestMsg struct {
	repo      string
	pr        int
	spoolPath string
	decision  string // one of "post", "revise", "rereview", "discard"
}

// gateFailedMsg is emitted by ReviewsTab.Update's "n" key handler when the
// selected review's decision does not qualify for notes editing (must be
// "revise" or "rereview", case-insensitive — see SpoolInfo.Decision). It
// carries a plain, already-composed reason string rather than structured
// fields, since the only consumer (model.Update's "case gateFailedMsg:"
// arm) does nothing but render it as an activity-line error — matching the
// "silent no-op reserved for nothing-selected, visible error for
// selected-but-disqualified" convention documented on ReviewsTab.Update.
type gateFailedMsg struct {
	reason string
}

// startBodyEditMsg is emitted by ReviewsTab.Update's "e" key handler
// (mirroring decideRequestMsg's/openReviewContentMsg's pattern) to ask the
// top-level model to launch $EDITOR on the currently selected review's full
// markdown body. ReviewsTab has no reference to the model's
// SuspendOutputCapture/tea.ExecProcess machinery (by design, matching every
// other cross-tab action), so this message/tea.Cmd round trip is the
// mechanism — see handleBodyEditLaunch (commands.go) for the handler.
type startBodyEditMsg struct {
	repo      string
	pr        int
	spoolPath string
}

// editorDoneMsg is emitted by the tea.ExecProcess callback started in
// handleBodyEditLaunch (commands.go) once the user's $EDITOR process exits.
// It is intentionally a *distinct* message from the pre-existing
// execDoneMsg (used by handlePlanSubprocess for the planning-mode kiro-cli
// subprocess): execDoneMsg's resume handling always calls
// restoreConsoleState() and reports planning-session-specific activity
// lines, which would be wrong here — body editing has nothing to do with
// console/planning state, only with a single review's spool file. Reusing
// execDoneMsg for both would either corrupt planning-mode's resume
// behavior or require conditionals inside that case arm; a new message
// keeps the two suspend/resume flows fully independent despite sharing the
// same tea.ExecProcess/SuspendOutputCapture/ResumeOutputCapture mechanics.
//
// tempPath is the temp file the editor was pointed at (removed
// unconditionally by the handler, in every branch). rec identifies which
// review's spool file the edited body belongs to, for the
// BodyWriter.SetBody call. err is the *exec.Cmd exit error, nil on a clean
// editor exit.
type editorDoneMsg struct {
	tempPath string
	rec      review.Record
	err      error
}

// startNotesEditMsg is emitted by ReviewsTab.Update's "n" key handler
// (mirroring decideRequestMsg's pattern) to ask the top-level model to
// enter decision_notes edit mode for the given review. currentNotes is the
// decision_notes value already on the spool file, read up front so the
// notes textinput can be pre-populated without the model re-resolving the
// selection or re-parsing the spool file itself.
type startNotesEditMsg struct {
	repo         string
	pr           int
	spoolPath    string
	currentNotes string
}

// saveNotesMsg is emitted on Enter while decision_notes edit mode is active
// (mirroring startNotesEditMsg's pattern) to ask the top-level model to
// persist the edited notes value via NotesWriter.SetNotes.
type saveNotesMsg struct {
	repo      string
	pr        int
	spoolPath string
	notes     string
}

type overlayType int

const (
	overlayNone overlayType = iota
	overlayStatus
	overlayHelp
	overlayAbout
	overlayLogs

	maxOverlayLines = 1000 // Prevent memory growth from very large overlay content

	// tabHeaderHeight is the number of lines the tab header occupies in the view
	tabHeaderHeight = 1
)

// exitCleanupTimeout bounds how long exit cleanup waits for planning tabs to
// close (each can block ~3s on ACP process shutdown). Keeps Ctrl+C a fast,
// reliable escape hatch even with many open planning tabs.
var exitCleanupTimeout = 3 * time.Second

type overlayContent struct {
	title   string
	content []string
}

type consoleState struct {
	inputValue    string
	activityLines []string
	activeTabID   string
}

type model struct {
	watcher          *watcher.Watcher
	reviewWatcher    *review.Watcher
	manager          *agent.Manager
	sessionManager   *session.SessionManager
	config           *config.Config
	styles           *Styles
	input            *AutocompleteInput
	commandRegistry  *CommandRegistry
	consoleViewport  viewport.Model
	activityLines    []string
	maxActivityLines int
	width            int
	height           int
	confirmingExit   bool
	logFile          *os.File
	logReader        *os.File
	lastLogPos       int64
	quitting         bool
	currentMode      session.SessionType
	consoleState     *consoleState
	initialCommand   string // Command to auto-execute on startup

	// Context cancellation for async operations (Finding #2)
	reviewCancel context.CancelFunc // Cancel function for in-flight review

	// decisionWriter sets the decision: field on a PR-review spool file
	// (see internal/review/decisionwriter.go). Shared by handleDecide (REPL
	// dispatch, commands.go) and the future decideRequestMsg handler
	// (key-menu dispatch, Task 7) via the applyDecision helper below, so
	// both entry points shell out through the exact same collaborator.
	decisionWriter *review.DecisionWriter

	// notesInput is the decision_notes textinput, focusable independently
	// of the footer's AutocompleteInput (m.input) — mirrors where m.input
	// already lives on model rather than being threaded through the Tab
	// interface (see issue #86 design spec, Solution Approach: Component
	// ownership split).
	notesInput *NotesInput
	// notesEditActive is true while the decision_notes textinput is the
	// active focus target — checked ahead of both footer-focus and
	// Reviews-tab row-shortcut key routing (wiring lands in a later task;
	// this field is unused by any Update code path yet).
	notesEditActive bool
	// notesEditTarget is the review being edited via notesInput, so
	// save/cancel know which spool file to act on without re-resolving the
	// Reviews tab's current selection.
	notesEditTarget review.Record
	// notesWriter sets the decision_notes: field on a PR-review spool file
	// (see internal/review/noteswriter.go). Stub until a later task wires
	// full save behavior.
	notesWriter *review.NotesWriter

	// bodyWriter replaces the markdown body of a PR-review spool file
	// while preserving its front-matter block byte-for-byte (see
	// internal/review/bodywriter.go). Invoked by the editorDoneMsg handler
	// once the edited temp file has been read back and validated as
	// non-empty.
	bodyWriter *review.BodyWriter

	// Finalize workflow state (issue #87): drives finalize-reviews.sh with
	// a dry-run preview, then an explicit y/N confirmation before posting
	// live. finalizeState is the state-machine position (see finalize.go);
	// finalizeCapture is the shared OutputCapture backing the live preview
	// window, written by the runFinalizeCmd goroutine's CaptureWriters and
	// read by the poll loop / window render path via OutputCapture's own
	// exported, mutex-guarded methods only. finalizeCancel is the
	// context.CancelFunc for whichever phase (dry-run or live) is
	// currently in flight. finalizeLastGen is the last-seen
	// OutputCapture.Generation() value the finalizeTickMsg handler compared
	// against, mirroring LogTab's lastWriteCounter. finalizeWindowTabID is
	// the fixed ID of the open preview window tab ("finalize-preview"),
	// empty when no finalize window is open. All five fields are mutated
	// only inside model.Update/model.View() (the single Bubble Tea
	// event-loop goroutine) — never inside the runFinalizeCmd or
	// pollFinalizeOutputCmd goroutine closures, which only ever return a
	// tea.Msg for Bubble Tea's runtime to deliver back onto this goroutine.
	finalizeState       finalizeState
	finalizeCapture     *agent.OutputCapture
	finalizeCancel      context.CancelFunc
	finalizeLastGen     uint64
	finalizeWindowTabID string

	// finalizePreflightState/finalizePreflightErr (issue #88) cache the
	// last-known result of review.CheckFinalizeAssets — whether
	// finalize-reviews.sh and pr_review_finalize.py currently resolve on
	// $PATH. This is a distinct concern from the five fields above:
	// finalizeState tracks the dry-run/confirm/live-run subprocess
	// lifecycle, while these two track asset availability, checked before
	// that lifecycle is ever allowed to start (see handleFinalize and
	// finalize_preflight.go). finalizePreflightErr holds the full error
	// text from review.CheckFinalizeAssets ("" when OK/unknown). Both
	// fields are mutated only inside model.Update (handleFinalize's
	// synchronous path and the finalizePreflightResultMsg case), never
	// inside the runFinalizePreflightCmd goroutine closure, which only
	// ever returns a tea.Msg for Bubble Tea's runtime to deliver back onto
	// this goroutine.
	finalizePreflightState finalizePreflightState
	finalizePreflightErr   string

	// Overlay system
	activeOverlay  overlayType
	overlayContent overlayContent
	overlayWidth   int
	overlayHeight  int

	// View state management
	tabManager *TabManager
	mainTab    *MainTab

	// Agent lifecycle tracking
	knownAgents         map[string]bool
	statusRunningAgents []*agent.Agent // Snapshot for deterministic number key selection

	// About dialog state
	aboutDialog *AboutDialog

	// Footer system
	footerManager *FooterManager

	// Logging system state
	loggingActive     bool
	activeLogTabID    string
	activeFileHandler *logging.FileHandler

	// Focus state tracking per tab
	tabFocusStates map[string]FocusTarget

	// Transient copy feedback (Ctrl+Y). copyStatus is shown in the footer until
	// copyStatusExpires; a tea.Tick clears it. copyStatusSeq guards against a
	// stale timer clearing a newer message.
	copyStatus    string
	copyStatusSeq int
}

func newModel(w *watcher.Watcher, m *agent.Manager, cfg *config.Config, logFile *os.File, logReader *os.File, initialCommand string) model {
	theme := cfg.LoadedTheme
	styles := NewStyles(theme)

	// Create command registry and autocomplete input
	commandRegistry := NewCommandRegistry(m)
	autocompleteInput := NewAutocompleteInput(commandRegistry, styles)

	consoleViewport := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	// Disable built-in key bindings — we handle scrolling explicitly
	consoleViewport.KeyMap = viewport.KeyMap{}

	// Initialize tab system
	tabManager := NewTabManager()
	mainTab := NewMainTab()
	tabManager.AddTab(mainTab)

	// Reviews tab: a permanent, non-closable, read-only view over the PR
	// review state store. It gets its own review.NewDefaultStore() instance —
	// separate from the store used by reviewWatcher below — since it only
	// ever calls List() and shares no mutable state with the watcher's poll
	// loop (see issue #66 design spec, Concurrency Analysis).
	reviewsTab := NewReviewsTab("reviews", review.NewDefaultStore(), styles)
	tabManager.AddTab(reviewsTab)

	// Initialize footer system
	footerManager := NewFooterManager(styles, cfg, w, autocompleteInput, tabManager)

	// Initialize review watcher
	reviewStore := review.NewDefaultStore()
	reviewPollInterval := 5 * time.Minute // Default poll interval
	if cfg.PollInterval > 0 {
		reviewPollInterval = cfg.PollInterval
	}
	reviewer, err := ResolveReviewer(cfg)
	var reviewerNotice string
	if err != nil {
		// No override configured and gh login resolution failed: the watcher
		// would otherwise compare against an identity that can never match a
		// real reviewer (see issue #89). Log clearly and fall back to an
		// empty reviewer so IsReviewRequestedFor never spuriously matches,
		// rather than crashing the whole TUI over a re-review convenience
		// feature. Rule 2 (never-reviewed) still works with an empty
		// reviewer; only re-request detection (Rule 3) is disabled.
		//
		// Also surface it where the operator will actually see it: with
		// console_logging off, a log-only message is invisible and the
		// degraded behavior ("I re-requested a review and nothing happened")
		// is indistinguishable from the very bug #89 fixed. Seed a startup
		// activity line too.
		logging.Error("failed to resolve reviewer identity for PR review watcher; re-request detection disabled", "error", err)
		reviewerNotice = "PR-review: could not resolve your GitHub identity — re-request detection is OFF " +
			"(set 'reviewer:' in .howmux/config.yaml or fix 'gh auth'). New-PR reviews still work."
	}
	reviewWatcher := review.NewWatcher(reviewStore, reviewPollInterval, 2, reviewer)

	initialActivity := make([]string, 0)
	if reviewerNotice != "" {
		initialActivity = append(initialActivity, styles.Warning.Render(reviewerNotice))
	}

	return model{
		watcher:          w,
		reviewWatcher:    reviewWatcher,
		manager:          m,
		sessionManager:   session.NewSessionManager(),
		config:           cfg,
		styles:           styles,
		input:            autocompleteInput,
		commandRegistry:  commandRegistry,
		consoleViewport:  consoleViewport,
		logFile:          logFile,
		logReader:        logReader,
		maxActivityLines: cfg.MaxActivityLines,
		currentMode:      session.Console,
		activityLines:    initialActivity,
		consoleState: &consoleState{
			inputValue:    "",
			activityLines: append([]string(nil), initialActivity...),
		},
		tabManager:     tabManager,
		mainTab:        mainTab,
		knownAgents:    make(map[string]bool),
		aboutDialog:    NewAboutDialog(),
		footerManager:  footerManager,
		tabFocusStates: make(map[string]FocusTarget),
		initialCommand: initialCommand,
		decisionWriter: review.NewDecisionWriter(),
		notesInput:     NewNotesInput(),
		notesWriter:    review.NewNotesWriter(),
		bodyWriter:     review.NewBodyWriter(),
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), m.tickCmd()}

	// Fix #3: Auto-execute initial command if provided
	if m.initialCommand != "" {
		// Parse and execute the initial command
		parts := strings.Fields(m.initialCommand)
		if len(parts) > 0 {
			cmd := parts[0]
			args := parts[1:]

			// Dispatch the initial command (same logic as the Update loop)
			switch cmd {
			case "review":
				_, reviewCmd := m.handleReview(args)
				if reviewCmd != nil {
					cmds = append(cmds, reviewCmd)
				}
				// Add other commands as needed
			}
		}
	}

	return tea.Batch(cmds...)
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// clearCopyStatusMsg clears a transient Ctrl+Y copy status after a delay. seq
// ties the timer to the message it was scheduled for, so a later copy's message
// isn't cleared early by an earlier timer.
type clearCopyStatusMsg struct{ seq int }

// copyStatusDuration is how long the Ctrl+Y feedback stays in the footer.
const copyStatusDuration = 2500 * time.Millisecond

// handleCopy copies the active tab's plain-text content to the clipboard and
// sets transient footer feedback describing the result.
func (m model) handleCopy() (tea.Model, tea.Cmd) {
	activeTab := m.tabManager.GetActiveTab()
	if activeTab == nil {
		return m, nil
	}

	content := activeTab.CopyableContent()
	if content == "" {
		return m.setCopyStatus("Nothing to copy")
	}

	if err := CopyToClipboard(content); err != nil {
		return m.setCopyStatus(fmt.Sprintf("Copy failed: %v", err))
	}

	lines := strings.Count(content, "\n") + 1
	plural := "s"
	if lines == 1 {
		plural = ""
	}
	return m.setCopyStatus(fmt.Sprintf("Copied %d line%s to clipboard", lines, plural))
}

// setCopyStatus records a transient status message and schedules its clearing.
func (m model) setCopyStatus(status string) (tea.Model, tea.Cmd) {
	m.copyStatus = status
	m.copyStatusSeq++
	seq := m.copyStatusSeq
	if m.footerManager != nil {
		m.footerManager.SetTransientMessage(status)
	}
	return m, tea.Tick(copyStatusDuration, func(time.Time) tea.Msg {
		return clearCopyStatusMsg{seq: seq}
	})
}

// appendActivity appends lines to activityLines and trims to maxActivityLines if set.
func (m model) appendActivity(lines ...string) model {
	m.activityLines = append(m.activityLines, lines...)
	if m.maxActivityLines > 0 && len(m.activityLines) > m.maxActivityLines {
		m.activityLines = m.activityLines[len(m.activityLines)-m.maxActivityLines:]
	}
	// Sync viewport content and auto-scroll if user is near the bottom
	content := strings.Join(m.activityLines, "\n")
	m.consoleViewport.SetContent(content)
	if m.consoleViewport.ScrollPercent() >= 0.95 {
		m.consoleViewport.GotoBottom()
	}
	return m
}

// isClickInFooterInput checks if mouse click is in the footer input area
func (m model) isClickInFooterInput(mouseX, mouseY int) bool {
	// Footer is at the bottom of the screen
	footerHeight := m.footerManager.GetFooterHeight()
	footerStartY := m.height - footerHeight

	// Click must be within footer area
	if mouseY < footerStartY {
		return false
	}

	// Input line is the middle line of the 3-line footer (separator, input, help)
	inputLineY := footerStartY + 1
	if mouseY != inputLineY {
		return false
	}

	// Click must be after the prompt text
	// The prompt is "kiro-krew> " from the textinput
	prompt := "kiro-krew> "
	promptWidth := lipgloss.Width(prompt)

	return mouseX >= promptWidth
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update footer manager dimensions
		m.footerManager.Resize(msg.Width, msg.Height)

		// Resize console viewport — account for tab header + footer system height
		footerHeight := m.footerManager.GetFooterHeight()
		activityHeight := m.height - footerHeight - tabHeaderHeight
		if activityHeight < 1 {
			activityHeight = 1
		}
		m.consoleViewport.SetWidth(msg.Width)
		m.consoleViewport.SetHeight(activityHeight)

		// Forward to tab manager with footer-aware resizing
		m.tabManager.ResizeForFooter(msg.Width, msg.Height, footerHeight)

		// Recalculate overlay dimensions on resize
		if m.activeOverlay != overlayNone {
			m.overlayWidth = int(float64(m.width) * 0.6)
			m.overlayHeight = int(float64(m.height) * 0.6)
			if m.overlayWidth < 40 {
				m.overlayWidth = 40
			}
			if m.overlayHeight < 10 {
				m.overlayHeight = 10
			}
		}
		return m, nil

	case execDoneMsg:
		// Resume agent output capture when planning mode exits
		m.manager.ResumeOutputCapture()

		if msg.err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Planning session exited with error: %v", msg.err)))

			// Log the error for debugging
			log.Printf("Planning session error: %v", msg.err)
		} else {
			m = m.appendActivity(m.styles.Success.Render("Planning session completed successfully."))
		}

		// Stop context tracking when exiting planning mode
		if m.footerManager != nil && m.footerManager.GetContextTracker() != nil {
			m.footerManager.GetContextTracker().StopPlanningSession()
		}

		m = m.restoreConsoleState()
		m.input.Focus()
		return m, tea.Batch(m.input.Focus(), tea.ClearScreen)

	case reviewStartMsg:
		// Store cancel function for cleanup on exit (Fix #2)
		m.reviewCancel = msg.cancel
		return m, nil

	case reviewCompleteMsg:
		// Handle async review completion
		if msg.err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Review failed: %v", msg.err)))
		} else {
			m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Review complete - spool at %s", msg.spoolPath)))

			// Ensure review watcher is running on success
			if !m.reviewWatcher.Running() {
				m.reviewWatcher.Start()
				m = m.appendActivity(m.styles.Success.Render("Review watcher started"))
			}
		}

		// Clear the cancel function since the review is done
		m.reviewCancel = nil
		return m, nil

	case finalizeDryRunMsg:
		// Dry-run finished successfully (exit 0). Transition to
		// awaiting-confirmation and stop the poll loop from re-arming (no
		// tea.Cmd requesting another tick is returned here) — see issue
		// #87's design spec, "On finalizeDryRunMsg (dry-run succeeded)".
		// Do NOT clear m.finalizeCapture/m.finalizeCancel: the live run
		// reuses the same window/capture, and finalizeCancel is
		// overwritten (not reused) by the live run's own context in the
		// confirmation-keypress handler below.
		if tabIdx := m.tabManager.FindTabByID(m.finalizeWindowTabID); tabIdx >= 0 {
			if rct, ok := m.tabManager.GetTabs()[tabIdx].(*ReviewContentTab); ok {
				rct.AppendFromCapture()
			}
		}
		m.finalizeState = finalizeAwaitingConfirmation
		m.footerManager.SetTransientMessage("Dry-run complete — post live? (y/N)")
		m = m.appendActivity(m.styles.Warning.Render("Dry-run complete — review the preview window, then post live? (y/N)"))
		return m, nil

	case finalizeCompleteMsg:
		// Live run finished successfully (exit 0). Return to idle; the
		// Reviews tab's next render reads current spool state from disk
		// (see issue #87's design spec, "Decision State Update") — no
		// explicit refresh call is needed here.
		if tabIdx := m.tabManager.FindTabByID(m.finalizeWindowTabID); tabIdx >= 0 {
			if rct, ok := m.tabManager.GetTabs()[tabIdx].(*ReviewContentTab); ok {
				rct.AppendFromCapture()
			}
		}
		m.finalizeState = finalizeIdle
		m.finalizeCancel = nil
		m.footerManager.SetTransientMessage("")
		m = m.appendActivity(m.styles.Success.Render("Finalize complete — spool drained."))
		return m, nil

	case finalizeErrorMsg:
		// Either phase failed — script not found, spawn failure, or a
		// non-zero exit. This handler performs no spool mutation of any
		// kind (see issue #87's design spec, "Error handling": every
		// actual spool-file write happens inside pr_review_finalize.py's
		// own idempotent per-file processing loop) — it only touches
		// m.finalizeState, m.finalizeCancel, and the activity log. The
		// preview window is left open with whatever partial output was
		// captured, so the user can read exactly what happened.
		if tabIdx := m.tabManager.FindTabByID(m.finalizeWindowTabID); tabIdx >= 0 {
			if rct, ok := m.tabManager.GetTabs()[tabIdx].(*ReviewContentTab); ok {
				rct.AppendFromCapture()
			}
		}
		phaseLabel := "live"
		if msg.dryRun {
			phaseLabel = "dry-run"
		}
		m.finalizeState = finalizeIdle
		m.finalizeCancel = nil
		m.footerManager.SetTransientMessage("")
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Finalize (%s) failed: %v", phaseLabel, msg.err)))
		// On a *live* run, the script may have already posted some reviews
		// before erroring mid-drain (each file is processed in its own
		// idempotent pass by pr_review_finalize.py). Point the user at the
		// safe recovery without changing any state: re-running finalize
		// skips already-drained files and completes the rest. Dry-run
		// failures post nothing, so the hint would be misleading there.
		if !msg.dryRun {
			m = m.appendActivity(m.styles.Warning.Render("Some reviews may already have posted — re-run finalize to complete the rest (it skips already-drained files)."))
		}
		return m, nil

	case finalizePreflightResultMsg:
		// Async retry path: the Reviews tab's "F" key emitted
		// finalizeRetryPreflightMsg, which the case below turned into
		// runFinalizePreflightCmd(); this is that command's result
		// arriving back on the Update goroutine. Delegates to the same
		// applyFinalizePreflightResult helper handleFinalize's synchronous
		// path uses, so the two paths can never disagree about what
		// "current preflight state" means (see issue #88's design spec,
		// "Where the preflight runs").
		m = m.applyFinalizePreflightResult(msg.err)
		return m, nil

	case finalizeRetryPreflightMsg:
		// Emitted by ReviewsTab.Update's "F" key handler (see issue #88's
		// design spec, "Retry mechanism"). Re-runs the preflight
		// asynchronously — unlike handleFinalize's synchronous call, this
		// is a user-initiated background re-check, not gating an in-flight
		// command dispatch, so it goes through the tea.Cmd round trip
		// instead of blocking Update.
		return m, runFinalizePreflightCmd()

	case finalizeTickMsg:
		// Poll tick for streaming finalize-reviews.sh output into the
		// preview window. Looks up the window by its fixed tab ID and
		// calls AppendFromCapture() regardless of whether it is the
		// currently active tab (streaming keeps buffering even if the
		// user switches away and back) — see issue #87's design spec,
		// "Files to Modify" -> review_content_tab.go. Re-arms the poll
		// only while a finalize run is actually in flight; once state has
		// moved to finalizeAwaitingConfirmation or finalizeIdle, this
		// stops requesting more ticks so the loop doesn't run forever
		// after the process exits.
		if m.finalizeCapture != nil {
			if currentGen := m.finalizeCapture.Generation(); currentGen != m.finalizeLastGen {
				m.finalizeLastGen = currentGen
				if tabIdx := m.tabManager.FindTabByID(m.finalizeWindowTabID); tabIdx >= 0 {
					if rct, ok := m.tabManager.GetTabs()[tabIdx].(*ReviewContentTab); ok {
						rct.AppendFromCapture()
					}
				}
			}
		}
		if m.finalizeState == finalizeDryRunRunning || m.finalizeState == finalizeLiveRunning {
			return m, pollFinalizeOutputCmd()
		}
		return m, nil

	case openReviewContentMsg:
		// Opens (or refocuses) a read-only window showing the full markdown
		// body of a single PR review's spool file. This message is emitted
		// by ReviewsTab.Update's "enter" case — ReviewsTab has no reference
		// to TabManager (by design, matching every other tab), so it cannot
		// add a tab itself; this handler is the other half of that
		// tea.Cmd/custom-tea.Msg round trip (see issue #84 design spec).
		homeDir, err := userHomeDirFunc()
		if err != nil {
			homeDir = ""
		}
		body, info := review.ReadSpoolBody(msg.spoolPath, homeDir)
		title := fmt.Sprintf("Review: %s #%d", msg.repo, msg.pr)
		id := fmt.Sprintf("review-content-%s-%d", msg.repo, msg.pr)

		// Reuse an already-open window for the same PR instead of stacking
		// duplicate tabs if the user presses Enter again on the same row.
		if existingIdx := m.tabManager.FindTabByID(id); existingIdx >= 0 {
			var cmd tea.Cmd
			m, cmd = m.switchActiveTab(existingIdx)
			return m, cmd
		}

		contentTab := NewReviewContentTab(id, title, body, info.Found, m.styles)
		m.tabManager.AddTab(contentTab)
		var cmd tea.Cmd
		m, cmd = m.switchActiveTab(len(m.tabManager.GetTabs()) - 1)
		return m, cmd

	case decideRequestMsg:
		// Emitted by ReviewsTab.Update's p/r/R/d key handlers (via
		// decideSelectedCmd) — the key-menu counterpart to the "decide"
		// REPL command handled by handleDecide (commands.go). Both entry
		// points delegate to the shared applyDecision helper so key-driven
		// and command-driven decisions produce identical activity-line
		// feedback from exactly one code path (issue #85, Task 7).
		//
		// decideSelectedCmd already resolved repo/pr/spoolPath from the
		// selected record before emitting this message, so no tab lookup
		// is needed here — but the same empty-spool-path check handleDecide
		// performs (step 4) still applies uniformly, since msg.spoolPath
		// could in principle be "" for the same reason a selected record's
		// SpoolPath could be empty (never reviewed yet).
		if msg.spoolPath == "" {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("PR #%d has no review yet — nothing to decide", msg.pr)))
			return m, nil
		}
		rec := review.Record{Repo: msg.repo, PR: msg.pr, SpoolPath: msg.spoolPath}
		m = m.applyDecision(rec, msg.decision)
		return m, nil

	case gateFailedMsg:
		// Emitted by ReviewsTab.Update's "n" key handler when the selected
		// review's decision does not qualify for notes editing. Rendered
		// as a visible activity-line error (not a silent no-op) so the
		// user understands why "n" did nothing — see gateFailedMsg's doc
		// comment for why this is a distinct case from the
		// nothing-selected no-op convention.
		m = m.appendActivity(m.styles.Error.Render(msg.reason))
		return m, nil

	case startNotesEditMsg:
		// Emitted by ReviewsTab.Update's "n" key handler (via
		// startNotesEditCmd) once the selected review's decision has
		// passed the revise/rereview gate. Enters decision_notes edit
		// mode: focuses m.notesInput, pre-populates it with the current
		// decision_notes value already resolved by the emitter, and
		// records which review is being edited so Enter/Esc know which
		// spool file to act on. The footer transient message reuses the
		// same mechanism as the Ctrl+Y copy-feedback indicator (issue #86
		// design spec, "Visual feedback") and is cleared via
		// SetTransientMessage("") on both the Enter-save and Esc-cancel
		// paths below, so it never lingers past the edit.
		m.notesEditTarget = review.Record{Repo: msg.repo, PR: msg.pr, SpoolPath: msg.spoolPath}
		m.notesInput.SetValue(msg.currentNotes)
		m.notesEditActive = true
		if m.footerManager != nil {
			m.footerManager.SetTransientMessage(fmt.Sprintf("Editing notes for %s #%d (Enter to save, Esc to cancel)", msg.repo, msg.pr))
		}
		return m, m.notesInput.Focus()

	case startBodyEditMsg:
		// Emitted by ReviewsTab.Update's "e" key handler (via
		// startBodyEditCmd) — launches $EDITOR on the selected review's
		// full markdown body. Delegates to handleBodyEditLaunch
		// (commands.go), which mirrors handlePlanSubprocess's
		// suspend/write-temp-file/tea.ExecProcess sequence.
		rec := review.Record{Repo: msg.repo, PR: msg.pr, SpoolPath: msg.spoolPath}
		return m.handleBodyEditLaunch(rec)

	case editorDoneMsg:
		// Resume half of the startBodyEditMsg round trip, once $EDITOR
		// exits. Mirrors handlePlanSubprocess/execDoneMsg's
		// suspend/resume pairing, but with its own, deliberately
		// distinct contract (see editorDoneMsg's doc comment): no
		// restoreConsoleState() call, since body editing never touched
		// console/planning state.
		//
		// The temp file is removed unconditionally on every branch below
		// (editor error, empty-file validation failure, or after a
		// successful/failed BodyWriter.SetBody call) so a crashed or
		// misbehaving editor never leaves stray files in os.TempDir().
		//
		// "Restore on invalid content" is implemented as *not writing*:
		// the spool file is never touched until BodyWriter.SetBody is
		// called with a validated, non-empty temp-file path — so an
		// editor error or an emptied temp file simply skips that call
		// entirely, leaving the spool file exactly as it was before the
		// edit (a stronger guarantee than restoring from a backup, since
		// nothing was ever written in the first place).
		m.manager.ResumeOutputCapture()

		if msg.err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Editor exited with error for PR #%d: %v", msg.rec.PR, msg.err)))
			if msg.tempPath != "" {
				_ = os.Remove(msg.tempPath)
			}
			return m, tea.ClearScreen
		}

		data, readErr := os.ReadFile(msg.tempPath)
		if readErr != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to read edited body for PR #%d: %v", msg.rec.PR, readErr)))
			if msg.tempPath != "" {
				_ = os.Remove(msg.tempPath)
			}
			return m, tea.ClearScreen
		}

		if strings.TrimSpace(string(data)) == "" {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Editor produced an empty file for PR #%d — review body not changed", msg.rec.PR)))
			if msg.tempPath != "" {
				_ = os.Remove(msg.tempPath)
			}
			return m, tea.ClearScreen
		}

		if err := m.bodyWriter.SetBody(msg.rec.SpoolPath, msg.tempPath); err != nil {
			m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to update review body for PR #%d: %v", msg.rec.PR, err)))
		} else {
			m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Updated review body for PR #%d", msg.rec.PR)))
		}

		if msg.tempPath != "" {
			_ = os.Remove(msg.tempPath)
		}

		return m, tea.ClearScreen

	case focusTransferMsg:
		// Handle focus coordination between planning tab and footer input.
		// Route through the single focus helper so the three focus stores
		// (footer, tab focusTarget, tabFocusStates) cannot drift apart.
		activeTab := m.tabManager.GetActiveTab()
		if activeTab != nil && activeTab.Type() == TabTypePlanning {
			if msg.target == "footer" {
				return m, m.setPlanningFocus(FocusTargetFooter)
			} else if msg.target == "message" {
				return m, m.setPlanningFocus(FocusTargetMessage)
			}
		}
		return m, nil

	case planningStreamStartMsg, planningStreamMsg, planningResponseMsg:
		// Streaming/response messages are addressed to the planning tab that
		// started the stream, carried in the message's tabID. Dispatch to that
		// specific tab rather than the active one — otherwise switching tabs
		// mid-stream would append chunks to the wrong tab (or drop them on a
		// non-planning tab) and the original stream would stop being drained.
		var tabID string
		switch mt := msg.(type) {
		case planningStreamStartMsg:
			tabID = mt.tabID
		case planningStreamMsg:
			tabID = mt.tabID
		case planningResponseMsg:
			tabID = mt.tabID
		}
		if pt := m.tabManager.GetPlanningTabByID(tabID); pt != nil {
			// PlanningTab.Update mutates via pointer receiver and the tab manager
			// holds the same pointer, so no write-back is needed.
			_, cmd := pt.Update(msg)
			if cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		// Fall back to the active tab if the origin tab is gone (e.g. closed
		// mid-stream), so a late chunk can't wedge the update loop.
		if cmd := m.tabManager.Update(msg); cmd != nil {
			return m, cmd
		}
		return m, nil

	case updateCheckMsg:
		updateLines := []string{}
		if msg.err != nil {
			// Check if error is ErrNoReleases - hide update status section entirely
			if errors.Is(msg.err, github.ErrNoReleases) {
				if m.activeOverlay == overlayAbout {
					// Hide update status section by passing empty slice
					m.aboutDialog.UpdateStatusLine([]string{})
					m.overlayContent.content = append(m.aboutDialog.GetFullContent(), "", "Press ESC to close")
				}
				// For console mode, don't add any activity lines
				return m, nil
			}

			// Other errors - show error message as before
			updateLines = append(updateLines,
				m.styles.Warning.Render("Update Status: Unable to check for updates"),
				m.styles.Error.Render(fmt.Sprintf("  Error: %v", msg.err)),
			)
		} else {
			// Check for empty/invalid TagName - treat as no releases
			if msg.release == nil || strings.TrimSpace(msg.release.TagName) == "" {
				if m.activeOverlay == overlayAbout {
					m.aboutDialog.UpdateStatusLine([]string{})
					m.overlayContent.content = append(m.aboutDialog.GetFullContent(), "", "Press ESC to close")
				}
				return m, nil
			}
			current := version.Version
			latest := msg.release.TagName
			if current == "dev" {
				updateLines = append(updateLines, m.styles.Warning.Render("Update Status: Development build"))
			} else {
				// Ensure "v" prefix for semver comparison
				if !strings.HasPrefix(current, "v") {
					current = "v" + current
				}
				if !strings.HasPrefix(latest, "v") {
					latest = "v" + latest
				}
				if !semver.IsValid(current) || !semver.IsValid(latest) {
					updateLines = append(updateLines,
						m.styles.Warning.Render("Update Status: Unable to compare versions (non-semver format)"),
						fmt.Sprintf("  Current: %s, Latest: %s", version.Version, msg.release.TagName),
					)
				} else if semver.Compare(current, latest) < 0 {
					updateLines = append(updateLines,
						m.styles.Warning.Render("Update Status: Update available"),
						fmt.Sprintf("  Latest: %s (%s)", msg.release.TagName, msg.release.Name),
					)
				} else {
					updateLines = append(updateLines, m.styles.Success.Render("Update Status: Up to date"))
				}
			}
		}

		if m.activeOverlay == overlayAbout {
			// Efficient partial update — only replace the status line
			m.aboutDialog.UpdateStatusLine(updateLines)
			m.overlayContent.content = append(m.aboutDialog.GetFullContent(), "", "Press ESC to close")
		} else {
			// Add to activity as before
			m = m.appendActivity(updateLines...)
		}
		return m, nil

	case tickMsg:
		newLines, newPos := m.readNewLogLines()
		if len(newLines) > 0 {
			m.lastLogPos = newPos
			m = m.appendActivity(newLines...)
		}

		// Check for agent lifecycle changes
		m = m.updateAgentTabs()

		return m, m.tickCmd()

	case clearCopyStatusMsg:
		// Only clear if no newer copy status has replaced this one.
		if msg.seq == m.copyStatusSeq {
			m.copyStatus = ""
			if m.footerManager != nil {
				m.footerManager.SetTransientMessage("")
			}
		}
		return m, nil

	case planningHotkeyMsg:
		if m.currentMode == session.Planning {
			return m.switchToConsoleMode()
		}
		return m, nil

	case hotkey.HotkeyTriggeredMsg:
		if m.currentMode == session.Console {
			return m.switchToPlanningMode()
		} else if m.currentMode == session.Planning {
			return m.switchToConsoleMode()
		}
		return m, nil

	case hotkey.HotkeyErrorMsg:
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Hotkey error: %v", msg.Err)))
		return m, nil

	case tea.MouseWheelMsg:
		// Handle mouse wheel scrolling in main tab when no overlay active
		activeTab := m.tabManager.GetActiveTab()
		if m.activeOverlay == overlayNone && activeTab != nil && activeTab.Type() == TabTypeMain {
			mouse := msg.Mouse()
			if mouse.Button == tea.MouseWheelUp {
				m.consoleViewport.ScrollUp(3)
			} else if mouse.Button == tea.MouseWheelDown {
				m.consoleViewport.ScrollDown(3)
			}
			return m, nil
		}
		// Forward to tab manager for agent tabs
		if cmd := m.tabManager.Update(msg); cmd != nil {
			return m, cmd
		}
		return m, nil

	case tea.MouseMotionMsg:
		// Handle mouse motion for tab hover effects when no overlay active
		if m.activeOverlay == overlayNone {
			mouse := msg.Mouse()
			// Only process hover when mouse is within tab header area
			if mouse.Y < tabHeaderHeight {
				m.tabManager.HandleTabHeaderHover(mouse.X)
			} else {
				m.tabManager.ClearHover()
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		// Handle mouse clicks on tab headers when no overlay active
		if m.activeOverlay == overlayNone {
			mouse := msg.Mouse()
			// Check if click is in the tab header area (first line)
			if mouse.Y < tabHeaderHeight {
				// Capture the set of review content tab IDs before the
				// click so we can tell, by diffing against the set after,
				// whether this click closed a review content tab (either
				// the active one or a non-active one clicked directly).
				// See findReviewsTabIndex/reviewContentTabIDs for context;
				// this mirrors the "esc" handler's redirect-to-Reviews
				// behavior for the mouse-click and ctrl+w close paths.
				before := reviewContentTabIDs(m.tabManager)
				m.tabManager.HandleTabHeaderClick(mouse.X)
				// Check if log tab was closed
				m.checkLogTabClosed()

				if len(reviewContentTabIDs(m.tabManager)) < len(before) {
					if reviewsIdx := m.findReviewsTabIndex(); reviewsIdx >= 0 {
						var cmd tea.Cmd
						m, cmd = m.switchActiveTab(reviewsIdx)
						return m, cmd
					}
				}
				return m, nil
			}

			// Check if click is in footer input area
			if m.isClickInFooterInput(mouse.X, mouse.Y) {
				activeTab := m.tabManager.GetActiveTab()
				if activeTab != nil {
					logging.Debug("mouse click focus transfer to footer",
						"tab_id", activeTab.ID(),
						"mouse_x", mouse.X,
						"mouse_y", mouse.Y)

					// On planning tabs, route through the single focus helper so
					// all three focus stores stay in sync. On other tabs the
					// footer is always the focus target anyway.
					if activeTab.Type() == TabTypePlanning {
						return m, m.setPlanningFocus(FocusTargetFooter)
					}

					m.tabFocusStates[activeTab.ID()] = FocusTargetFooter
					tabCmd := activeTab.RestoreFocusState(FocusTargetFooter)
					m.input.SetFocus(true)
					return m, tea.Batch(m.input.Focus(), tabCmd)
				}
			}
		}
		// Forward to active tab
		if cmd := m.tabManager.Update(msg); cmd != nil {
			return m, cmd
		}
		// Check if log tab was closed by tab update
		m.checkLogTabClosed()
		return m, nil

	case tea.KeyPressMsg:
		// Handle hotkey detection first
		if hotkeyCmd := hotkey.HandleKeyMsg(msg); hotkeyCmd != nil {
			return m, hotkeyCmd
		}

		// Ctrl+C is the unconditional hard-quit escape hatch. It runs before
		// overlays, exit confirmation, and focus routing so there is always a
		// reliable way out regardless of UI state. Best-effort cleanup, then quit.
		// (Copy is on Ctrl+Y — terminals can't reliably deliver Cmd+C to the app,
		// and rebinding Ctrl+C away from quit/SIGINT is surprising cross-platform.)
		if msg.String() == "ctrl+c" {
			m = m.performExitCleanup()
			m.quitting = true
			return m, tea.Quit
		}

		// Ctrl+Y: copy the active tab's underlying text to the clipboard and show
		// transient feedback in the footer. Copy is handled here (not in the tab)
		// so success/failure is reported uniformly — a silent copy left users
		// unsure whether anything happened.
		if msg.String() == "ctrl+y" {
			return m.handleCopy()
		}

		// Finalize confirmation-keypress interception (issue #87, relocated
		// and hardened by issue #102): while
		// m.finalizeState == finalizeAwaitingConfirmation, the next
		// keypress is gated exactly like m.confirmingExit's own y/N block
		// further below — "y"/"yes" triggers the live run, "n"/"no"/"esc"
		// cancels back to idle, and anything else (navigation keys like
		// "[", "]", "f2", arrow keys, or any other key) is ignored outright
		// with no state change and no activity line (issue #102: these keys
		// used to fall into the old two-arm switch's default/cancel case and
		// silently cancelled the pending confirmation). This must be
		// checked before m.confirmingExit's block, before Reviews-tab
		// row-shortcut forwarding, AND — as of issue #102 — before all
		// three esc-handling blocks immediately below (overlay dismissal,
		// planning-tab focus return, and TabTypeReviewContent window-close).
		// Without running ahead of the TabTypeReviewContent-closes-on-Esc
		// handler specifically, pressing Esc while the Finalize Preview tab
		// is the active tab would close that window via the generic
		// handler instead of cancelling finalize here, since the generic
		// handler would otherwise match first.
		//
		// Because none of the keys this block explicitly handles (y/yes/
		// n/no/esc) return early in the default arm, and the default arm
		// falls through with no return, navigation keys such as "[", "]",
		// "f2", and the arrow keys still reach their normal handlers in the
		// switch further below in this function — this block only
		// intercepts the decision keys, not every keypress.
		//
		// Invariant: this block and m.confirmingExit's block below are
		// mutually exclusive by construction — both gate on being
		// idle-ish top-level states (finalizeAwaitingConfirmation is only
		// reached via handleFinalize/finalizeDryRunMsg, which never runs
		// while a "stop all and exit?" prompt is pending, and tryExit's
		// own agent/planning-tab checks are independent of finalize
		// state), so the same keypress can never be interpreted by both.
		// A future change to either gate must preserve this invariant
		// rather than letting both fire on one keypress.
		if m.finalizeState == finalizeAwaitingConfirmation {
			input := strings.ToLower(strings.TrimSpace(msg.String()))
			switch input {
			case "y", "yes":
				// Create a NEW context for the live run — the dry-run's
				// context is already done; do not reuse it.
				ctx, cancel := context.WithCancel(context.Background())
				m.finalizeCancel = cancel

				// Visibly demarcate the live-run output from the dry-run
				// preview within the same window (see design spec,
				// "Window Content Model") by appending a separator line
				// directly to the shared OutputCapture, so
				// AppendFromCapture's "just re-render the whole buffer"
				// logic stays uniform with no special-casing.
				if m.finalizeCapture != nil {
					m.finalizeCapture.AddLine("─── Posting live ───")
				}
				// Reset the comparison baseline (not the counter itself)
				// so the poll loop's next tick treats the live run's
				// fresh output as new, mirroring how LogTab never resets
				// its ring buffer, only its last-seen counter.
				m.finalizeLastGen = 0

				m.finalizeState = finalizeLiveRunning
				m.footerManager.SetTransientMessage("Posting live via finalize-reviews.sh...")
				m = m.appendActivity(m.styles.Activity.Render("Posting live via finalize-reviews.sh..."))

				return m, tea.Batch(
					runFinalizeCmd(ctx, false, m.finalizeCapture, cancel),
					pollFinalizeOutputCmd(),
				)
			case "n", "no", "esc":
				m.finalizeState = finalizeIdle
				// Calling an already-fired CancelFunc is always safe/no-op
				// per the context package's own contract — the dry-run's
				// subprocess has already exited by this point since we
				// only reach finalizeAwaitingConfirmation after
				// finalizeDryRunMsg.
				if m.finalizeCancel != nil {
					m.finalizeCancel()
					m.finalizeCancel = nil
				}
				m.footerManager.SetTransientMessage("")
				m = m.appendActivity(m.styles.Warning.Render("Live posting cancelled — nothing was posted."))
				return m, nil
			default:
				// Navigation/other key while awaiting confirmation: ignore
				// entirely. No state change, no activity line — this is
				// the fix for the silent-cancel-on-navigation trap (issue
				// #102). Deliberately no return here: falling through lets
				// this keypress still reach its normal handler further
				// below in Update (tab switching via "[", "]", "f2",
				// viewport scrolling via the arrow keys, etc.), so
				// navigation continues to work exactly as it did before
				// finalizeAwaitingConfirmation existed.
			}
		}

		// Priority handling for overlay dismissal
		if m.activeOverlay != overlayNone && msg.String() == "esc" {
			m = m.clearOverlay()
			return m, nil
		}

		// Priority handling for focus in planning tabs: Esc always returns focus
		// to the footer command line (the always-available default in Model A).
		if msg.String() == "esc" {
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypePlanning {
				if !m.input.Focused() {
					cmd := m.setPlanningFocus(FocusTargetFooter)
					return m, cmd
				}
			}
		}

		// Priority handling for closing a review content window: Esc closes
		// the window and returns focus to the permanent Reviews tab. Since
		// ReviewsTab.IsClosable() is false, the Reviews tab is guaranteed to
		// still exist, so the fallback "return m, nil" branch below is
		// defensive only (mirrors the nil-check style already used
		// throughout this file, e.g. checkLogTabClosed).
		if msg.String() == "esc" {
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypeReviewContent {
				reviewsIdx := m.findReviewsTabIndex()
				m.tabManager.CloseTab(m.tabManager.GetActiveTabIndex())
				if reviewsIdx >= 0 {
					var cmd tea.Cmd
					m, cmd = m.switchActiveTab(reviewsIdx)
					return m, cmd
				}
				return m, nil
			}
		}

		// Handle number key selection in status overlay for agent restoration
		if m.activeOverlay == overlayStatus {
			switch msg.String() {
			case "1", "2", "3", "4", "5", "6", "7", "8", "9":
				agentIndex, _ := strconv.Atoi(msg.String())
				agentIndex-- // Convert to 0-based index

				// Use snapshot stored when overlay was created
				if agentIndex >= 0 && agentIndex < len(m.statusRunningAgents) && agentIndex < 9 {
					selectedAgent := m.statusRunningAgents[agentIndex]
					m = m.clearOverlay()

					// Validate agent is still running before creating/focusing tab
					currentAgents := m.manager.List()
					agentStillRunning := false
					for _, currentAgent := range currentAgents {
						if currentAgent.ID == selectedAgent.ID && currentAgent.Status == agent.StatusRunning {
							agentStillRunning = true
							break
						}
					}

					if agentStillRunning {
						m.tabManager.RestoreOrFocusAgentTab(selectedAgent.ID, m.manager, m.styles)
						m.knownAgents[selectedAgent.ID] = true
					} else {
						m = m.appendActivity(m.styles.Warning.Render("Agent no longer running"))
					}
					return m, nil
				}
				// Invalid selection - just ignore
				return m, nil
			}
		}

		// Block other input when overlay is active
		if m.activeOverlay != overlayNone {
			return m, nil
		}

		if m.confirmingExit {
			input := strings.ToLower(strings.TrimSpace(msg.String()))
			switch input {
			case "y", "yes":
				// Perform comprehensive cleanup
				m = m.performExitCleanup()
				m.quitting = true
				return m, tea.Quit
			default:
				m.confirmingExit = false
				m = m.appendActivity(m.styles.Warning.Render("Exit cancelled."))
				return m, nil
			}
		}

		// Notes-edit-mode interception: while m.notesEditActive is true, the
		// decision_notes textinput (m.notesInput) is a third, even-higher-
		// priority focus state layered above both footer-focus and
		// Reviews-tab row-navigation (see issue #86 design spec, Solution
		// Approach: "Component ownership split"). This must run before the
		// existing Reviews-tab p/r/R/d forwarding block (the "default:" arm
		// of the switch below) so that typing in the notes textinput never
		// falls through to row-shortcut handling — a bare "p" or "r" typed
		// here must reach the textinput, not set a decision or navigate
		// rows.
		if m.notesEditActive {
			switch msg.String() {
			case "esc":
				// Cancel: no write attempted at all. Discard the typed
				// value (the textinput is not persisted anywhere), blur,
				// clear the footer transient message, and drop back to
				// row-navigation. Re-opening notes-edit (pressing "n"
				// again) re-reads decision_notes from the spool file, so
				// the discarded edit never resurfaces.
				m.notesInput.Blur()
				m.notesEditActive = false
				if m.footerManager != nil {
					m.footerManager.SetTransientMessage("")
				}
				m = m.appendActivity(m.styles.Activity.Render("Notes edit cancelled"))
				return m, nil
			case "enter":
				// Save: persist the typed value via NotesWriter.SetNotes.
				// On failure, stay in edit mode with the typed text intact
				// — a failed write must not silently discard user input
				// (see design spec's Task 3 acceptance criteria) — and
				// show an error activity line. On success, exit edit
				// mode, blur, clear the transient message, and show a
				// success activity line, mirroring applyDecision's
				// success/error activity-line shape (commands.go).
				notes := m.notesInput.Value()
				target := m.notesEditTarget
				if err := m.notesWriter.SetNotes(target.SpoolPath, notes); err != nil {
					m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Failed to save decision notes for PR #%d: %v", target.PR, err)))
					return m, nil
				}
				m.notesInput.Blur()
				m.notesEditActive = false
				if m.footerManager != nil {
					m.footerManager.SetTransientMessage("")
				}
				m = m.appendActivity(m.styles.Success.Render(fmt.Sprintf("Saved decision notes for PR #%d", target.PR)))
				return m, nil
			default:
				var cmd tea.Cmd
				m.notesInput, cmd = m.notesInput.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "f2":
			// Toggle between main and agent tabs
			m.tabManager.ToggleView()
			return m, nil
		case "[":
			// Previous tab
			tabCount := len(m.tabManager.GetTabs())
			if tabCount > 1 {
				prev := (m.tabManager.GetActiveTabIndex() - 1 + tabCount) % tabCount
				var cmd tea.Cmd
				m, cmd = m.switchActiveTab(prev)
				return m, cmd
			}
			return m, nil
		case "]":
			// Next tab
			tabCount := len(m.tabManager.GetTabs())
			if tabCount > 1 {
				next := (m.tabManager.GetActiveTabIndex() + 1) % tabCount
				var cmd tea.Cmd
				m, cmd = m.switchActiveTab(next)
				return m, cmd
			}
			return m, nil
		case "ctrl+w":
			// Close current tab (if closable)
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypeLog {
				// Deactivate logging before closing the tab
				if err := m.deactivateLogging(); err != nil {
					m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("Warning during logging deactivation: %v", err)))
				}
			}
			// Same before/after review-content-tab diff and Reviews
			// redirect as the mouse-click close path above; see
			// reviewContentTabIDs.
			before := reviewContentTabIDs(m.tabManager)
			m.tabManager.CloseCurrentTab()
			if len(reviewContentTabIDs(m.tabManager)) < len(before) {
				if reviewsIdx := m.findReviewsTabIndex(); reviewsIdx >= 0 {
					var cmd tea.Cmd
					m, cmd = m.switchActiveTab(reviewsIdx)
					return m, cmd
				}
			}
			return m, nil
		case "up", "down", "pgup", "pgdown", "home", "end":
			// Dropdown navigation takes precedence over any tab-specific
			// viewport/row-navigation handling whenever the footer input is
			// focused and currently showing matched suggestions —
			// independent of which tab is active. Previously this only
			// worked on TabTypeMain (and, without the HasMatchedSuggestions
			// check, on TabTypePlanning), so arrows appeared to do nothing
			// on the Reviews tab (and any future tab) while the dropdown
			// was open.
			if (msg.String() == "up" || msg.String() == "down") &&
				m.input.Focused() && m.input.HasMatchedSuggestions() {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			// Handle console scroll events when in main tab
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypeMain {
				// Handle console scrolling
				switch msg.String() {
				case "up":
					m.consoleViewport.ScrollUp(1)
				case "down":
					m.consoleViewport.ScrollDown(1)
				case "pgup":
					m.consoleViewport.HalfPageUp()
				case "pgdown":
					m.consoleViewport.HalfPageDown()
				case "home":
					m.consoleViewport.GotoTop()
				case "end":
					m.consoleViewport.GotoBottom()
				}
				return m, nil
			}
			// During viewport navigation, ensure focus is consistently on the
			// footer via the single focus helper. Routing through setPlanningFocus
			// (rather than blurring m.input directly) keeps all three focus stores
			// — m.input, the tab's focusTarget, and m.tabFocusStates — in sync, so
			// the next Tab toggles correctly and the command line stays usable.
			var navFocusCmd tea.Cmd
			if activeTab != nil && activeTab.Type() == TabTypePlanning {
				switch msg.String() {
				case "pgup", "pgdown", "home", "end":
					navFocusCmd = m.setPlanningFocus(FocusTargetFooter)
				}
			}
			navCmd := m.tabManager.Update(msg)
			if navFocusCmd != nil || navCmd != nil {
				return m, tea.Batch(navFocusCmd, navCmd)
			}
			return m, nil
		case "tab":
			// Handle tab completion in footer input
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypeMain {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			// On a planning tab, Tab is context-sensitive (Model A):
			//   - footer focused WITH an active completion suggestion -> complete
			//   - otherwise -> toggle focus between footer and message input
			if activeTab != nil && activeTab.Type() == TabTypePlanning {
				if m.input.Focused() && m.input.HasMatchedSuggestions() {
					var cmd tea.Cmd
					m.input, cmd = m.input.Update(msg)
					return m, cmd
				}
				return m, m.togglePlanningFocus()
			}
			// On the Reviews tab, Tab toggles focus between row-navigation
			// mode (the default — up/down/enter/p/r/R/d reach
			// ReviewsTab.Update) and the footer input (so the user can type
			// a `decide <value>` or other command), mirroring
			// togglePlanningFocus's footer<->message pattern but for this
			// tab's row/footer duality. Without this, there would be no
			// discoverable way back to the footer once row-navigation mode
			// is the entered default (see switchActiveTab / CaptureFocusState).
			if activeTab != nil && activeTab.Type() == TabTypeReviews {
				if m.input.Focused() && m.input.HasMatchedSuggestions() {
					var cmd tea.Cmd
					m.input, cmd = m.input.Update(msg)
					return m, cmd
				}
				return m, m.toggleReviewsFocus()
			}
			// Forward to active tab for other handling
			if activeTab != nil {
				if cmd := m.tabManager.Update(msg); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		case "shift+tab":
			// Shift+Tab always toggles footer<->message focus on a planning tab,
			// so the toggle is discoverable regardless of completion state.
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil && activeTab.Type() == TabTypePlanning {
				return m, m.togglePlanningFocus()
			}
			// Same rationale on the Reviews tab: Shift+Tab always toggles
			// row-navigation<->footer focus, regardless of completion state.
			if activeTab != nil && activeTab.Type() == TabTypeReviews {
				return m, m.toggleReviewsFocus()
			}
			return m, nil
		case "enter":
			// Execute commands when footer is focused, forward enter to tabs otherwise.
			// This allows command execution from any tab while preserving tab-specific
			// enter handling (e.g., message sending in planning tabs).
			activeTab := m.tabManager.GetActiveTab()

			// If footer is focused, execute command regardless of tab type
			if m.input.Focused() {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)

				input := strings.TrimSpace(m.input.Value())
				m.input.SetValue("")
				if input == "" {
					return m, cmd
				}

				return m.executeCommand(input)
			}

			// Footer NOT focused: Forward to active tab for tab-specific handling
			if activeTab != nil {
				if cmd := m.tabManager.Update(msg); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		default:
			// Forward key messages to active tab - removed TabTypeAgent restriction
			activeTab := m.tabManager.GetActiveTab()
			if activeTab != nil {
				// AC5a: on the Reviews tab, p/r/R/d are row-decision
				// shortcuts (see ReviewsTab.Update) — but only when the
				// footer input does NOT have focus. When the footer DOES
				// have focus (the user is typing a command), those same
				// keystrokes must type into the footer normally, matching
				// how m.input.Focused() is re-checked live elsewhere in
				// this same routing block (e.g. the "up/down/pgup/…"
				// case) rather than trusting only the captured focus
				// state. Skip forwarding to the tab in that case and fall
				// through to the input-update path below.
				if activeTab.Type() == TabTypeReviews && m.input.Focused() {
					break
				}
				if cmd := m.tabManager.Update(msg); cmd != nil {
					return m, cmd
				}
			}
		}
	}

	// Update input when no overlay is active - removed TabTypeMain restriction
	activeTab := m.tabManager.GetActiveTab()
	if m.activeOverlay == overlayNone && activeTab != nil {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	return m, nil
}

// setPlanningFocus is the single source of truth for focus on a planning tab.
// It synchronizes all three focus stores that previously drifted apart:
//   - m.input (footer AutocompleteInput)
//   - the tab's focusTarget (via RestoreFocusState)
//   - m.tabFocusStates[tabID]
//
// target must be FocusTargetFooter or FocusTargetMessage. It returns the tea.Cmd
// needed to apply focus to the newly focused surface. Callers must route all
// footer<->message transitions through here so the stores cannot disagree.
func (m *model) setPlanningFocus(target FocusTarget) tea.Cmd {
	activeTab := m.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypePlanning {
		return nil
	}

	m.tabFocusStates[activeTab.ID()] = target

	switch target {
	case FocusTargetMessage:
		// Message input takes focus; footer yields it.
		m.input.SetFocus(false)
		return activeTab.RestoreFocusState(FocusTargetMessage)
	default: // FocusTargetFooter
		// Footer takes focus; message input yields it.
		tabCmd := activeTab.RestoreFocusState(FocusTargetFooter)
		m.input.SetFocus(true)
		return tea.Batch(m.input.Focus(), tabCmd)
	}
}

// togglePlanningFocus flips focus between the footer and the message input on the
// active planning tab, returning the cmd to apply it. Used by Tab/shift+tab.
func (m *model) togglePlanningFocus() tea.Cmd {
	if m.input.Focused() {
		return m.setPlanningFocus(FocusTargetMessage)
	}
	return m.setPlanningFocus(FocusTargetFooter)
}

// setReviewsFocus is the Reviews-tab analog of setPlanningFocus: the single
// source of truth for focus on the active Reviews tab, keeping m.input and
// m.tabFocusStates[tabID] in sync. target must be FocusTargetFooter or
// FocusTargetRows. Unlike setPlanningFocus, there is no tab-side widget to
// restore (ReviewsTab.RestoreFocusState is a no-op — see reviews_tab.go), so
// this only ever needs to drive m.input.
func (m *model) setReviewsFocus(target FocusTarget) tea.Cmd {
	activeTab := m.tabManager.GetActiveTab()
	if activeTab == nil || activeTab.Type() != TabTypeReviews {
		return nil
	}

	m.tabFocusStates[activeTab.ID()] = target

	if target == FocusTargetFooter {
		m.input.SetFocus(true)
		return m.input.Focus()
	}
	// FocusTargetRows (or any other non-footer value): footer yields focus.
	m.input.SetFocus(false)
	return nil
}

// toggleReviewsFocus flips focus between the footer and row-navigation mode
// on the active Reviews tab, returning the cmd to apply it. Used by
// Tab/shift+tab — mirrors togglePlanningFocus's footer<->message pattern.
func (m *model) toggleReviewsFocus() tea.Cmd {
	if m.input.Focused() {
		return m.setReviewsFocus(FocusTargetRows)
	}
	return m.setReviewsFocus(FocusTargetFooter)
}

func (m model) activateOverlay(overlay overlayType, title string, content []string) model {
	// Limit content size to prevent memory issues
	if len(content) > maxOverlayLines {
		content = content[len(content)-maxOverlayLines:]
	}

	m.activeOverlay = overlay
	m.overlayContent = overlayContent{
		title:   title,
		content: append(content, "", "Press ESC to close"),
	}
	return m
}

func (m model) clearOverlay() model {
	m.activeOverlay = overlayNone
	m.overlayContent = overlayContent{} // Clear content to prevent memory accumulation
	return m
}

func (m model) renderBaseView() string {
	// Calculate activity height accounting for footer and tab header
	footerHeight := m.footerManager.GetFooterHeight()
	activityHeight := m.height - footerHeight - tabHeaderHeight
	if activityHeight < 1 {
		activityHeight = 1
	}

	// Update viewport height for the available space
	m.consoleViewport.SetHeight(activityHeight)
	activity := m.consoleViewport.View()

	// Use unified rendering system for consistent footer display
	return m.renderTabContentWithFooter(m.styles.Activity.Render(activity), TabTypeMain)
}

// renderTabContentWithFooter creates a unified rendering method that combines tab content with footer
func (m model) renderTabContentWithFooter(tabContent string, tabType TabType) string {
	// Render footer using the footer system
	footer := m.footerManager.RenderWithSeparator(tabType)

	// Normalize tab content by removing trailing newlines
	normalizedContent := strings.TrimRight(tabContent, "\n")

	// When content is empty, return footer directly without a separator to avoid
	// an unnecessary leading blank line.
	if normalizedContent == "" {
		return footer
	}

	// Compose the complete view with consistent newline separation
	return normalizedContent + "\n" + footer
}

func (m model) View() tea.View {
	if m.quitting {
		v := tea.NewView("Goodbye!\n")
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}

	// Wait for window size before rendering full layout
	if m.height == 0 {
		v := tea.NewView(m.input.View())
		v.AltScreen = true
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}

	// Render tab headers at the top
	tabHeaders := m.tabManager.RenderTabHeaders(m.width, m.styles)

	// Render active tab content using unified rendering system
	var content string
	activeTab := m.tabManager.GetActiveTab()
	if activeTab != nil && activeTab.Type() != TabTypeMain {
		// For non-main tabs, get their content and apply unified rendering with footer
		tabContent := activeTab.View()
		content = m.renderTabContentWithFooter(tabContent, activeTab.Type())
	} else {
		// For main tab, use the existing base view (which now uses unified rendering internally)
		content = m.renderBaseView()
	}

	// Combine tab headers with content
	content = tabHeaders + "\n" + content

	// Render autocomplete menu overlay BEFORE other overlays
	if m.input.HasMatchedSuggestions() {
		menuOverlay := m.input.RenderSuggestionsMenu()
		if menuOverlay != "" {
			content = m.layerMenuOverlay(content, menuOverlay)
		}
	}

	// Compose overlay if active (overlays work on any view)
	if m.activeOverlay != overlayNone {
		overlay := m.renderOverlay()
		content = m.layerOverlay(content, overlay)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

func (m model) readNewLogLines() ([]string, int64) {
	info, err := m.logReader.Stat()
	if err != nil {
		return nil, m.lastLogPos
	}

	size := info.Size()
	if size <= m.lastLogPos {
		return nil, m.lastLogPos
	}

	buf := make([]byte, size-m.lastLogPos)
	n, err := m.logReader.ReadAt(buf, m.lastLogPos)
	if err != nil && err != io.EOF {
		return nil, m.lastLogPos
	}
	newPos := m.lastLogPos + int64(n)

	text := strings.TrimRight(string(buf[:n]), "\n")
	if text == "" {
		return nil, newPos
	}
	return strings.Split(text, "\n"), newPos
}

func (m model) renderOverlay() string {
	// Calculate overlay dimensions (60% of screen, centered)
	m.overlayWidth = int(float64(m.width) * 0.6)
	m.overlayHeight = int(float64(m.height) * 0.6)

	// Ensure minimum dimensions
	if m.overlayWidth < 40 {
		m.overlayWidth = 40
	}
	if m.overlayHeight < 10 {
		m.overlayHeight = 10
	}

	// Ensure overlay doesn't exceed screen bounds
	if m.overlayWidth >= m.width {
		m.overlayWidth = m.width - 2
	}
	if m.overlayHeight >= m.height {
		m.overlayHeight = m.height - 2
	}

	// Create overlay content
	title := m.styles.OverlayTitle.Render(m.overlayContent.title)

	contentHeight := m.overlayHeight - 4 // Account for border + title + padding
	if contentHeight < 1 {
		contentHeight = 1
	}

	content := m.overlayContent.content
	if len(content) > contentHeight {
		content = content[len(content)-contentHeight:]
	}

	// Pad content to fill overlay
	for len(content) < contentHeight {
		content = append(content, "")
	}

	// Trim content lines to fit within overlay width
	maxContentWidth := m.overlayWidth - 6 // Account for border + padding
	if maxContentWidth < 1 {
		maxContentWidth = 1
	}
	truncateStyle := lipgloss.NewStyle().Width(maxContentWidth)
	for i, line := range content {
		if lipgloss.Width(line) > maxContentWidth {
			content[i] = truncateStyle.Render(line)
		}
	}

	contentStr := strings.Join(content, "\n")

	// Apply overlay styling with proper content rendering
	overlayContent := lipgloss.JoinVertical(lipgloss.Left, title, "", m.styles.OverlayContent.Render(contentStr))

	return m.styles.OverlayBorder.
		Width(m.overlayWidth - 4). // Account for border
		Height(m.overlayHeight - 2).
		Render(overlayContent)
}

// skipVisualWidth returns the portion of s after the first n visual characters,
// preserving ANSI escape sequences
// skipVisualWidth returns the portion of s after the first n visual characters,
// preserving ANSI CSI escape sequences (format: \x1b[...letter).
//
// This function assumes well-formed CSI sequences and only handles the subset
// of ANSI sequences used by lipgloss styling. It may behave incorrectly with
// malformed sequences or non-CSI escape sequences.
func skipVisualWidth(s string, n int) string {
	if n <= 0 {
		return s
	}

	var (
		visualCount int
		bytePos     int
		inEscape    bool
	)

	for bytePos < len(s) && visualCount < n {
		if s[bytePos] == '\x1b' && bytePos+1 < len(s) && s[bytePos+1] == '[' {
			// Start of ANSI CSI escape sequence
			inEscape = true
			bytePos += 2 // Skip both \x1b and [
			continue
		}

		if inEscape {
			if (s[bytePos] >= 'A' && s[bytePos] <= 'Z') ||
				(s[bytePos] >= 'a' && s[bytePos] <= 'z') {
				// End of ANSI escape sequence
				inEscape = false
			}
			bytePos++
			continue
		}

		// Regular character - count it and advance
		_, size := utf8.DecodeRuneInString(s[bytePos:])
		bytePos += size
		visualCount++
	}

	if bytePos >= len(s) {
		return ""
	}
	return s[bytePos:]
}

func (m model) layerOverlay(base, overlay string) string {
	// Center overlay on base view
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	overlayW := 0
	for _, l := range overlayLines {
		if w := lipgloss.Width(l); w > overlayW {
			overlayW = w
		}
	}

	startRow := (m.height - len(overlayLines)) / 2
	startCol := (m.width - overlayW) / 2
	// Ensure overlay stays within bounds
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}
	if startRow+len(overlayLines) > len(baseLines) {
		startRow = len(baseLines) - len(overlayLines)
		if startRow < 0 {
			startRow = 0
		}
	}

	// Create result with same length as base
	result := make([]string, len(baseLines))
	copy(result, baseLines)

	// Overlay the content
	for i, overlayLine := range overlayLines {
		targetRow := startRow + i
		if targetRow >= 0 && targetRow < len(result) {
			baseLine := result[targetRow]
			overlayWidth := lipgloss.Width(overlayLine)

			// Pad base line if needed
			if len(baseLine) < startCol {
				baseLine += strings.Repeat(" ", startCol-len(baseLine))
			}

			// Calculate portions of base line
			beforeOverlay := ""
			if startCol > 0 && len(baseLine) > 0 {
				baseWidth := lipgloss.Width(baseLine)
				truncWidth := startCol
				if truncWidth > baseWidth {
					truncWidth = baseWidth
				}
				// Use ANSI-aware truncation for beforeOverlay
				beforeOverlay = ansi.Truncate(baseLine, truncWidth, "")
			}

			afterOverlay := ""
			afterStart := startCol + overlayWidth
			// Only extract suffix if overlay doesn't extend beyond the visual width
			if afterStart < lipgloss.Width(baseLine) {
				afterOverlay = skipVisualWidth(baseLine, afterStart)
			}

			result[targetRow] = beforeOverlay + overlayLine + afterOverlay
		}
	}

	return strings.Join(result, "\n")
}

// layerMenuOverlay positions the autocomplete menu at the bottom-left, directly above the 3-line footer
func (m model) layerMenuOverlay(base, overlay string) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Calculate start row: above the 3-line footer
	startRow := len(baseLines) - 3 - len(overlayLines)
	if startRow < 0 {
		startRow = 0
	}

	result := make([]string, len(baseLines))
	copy(result, baseLines)

	for i, overlayLine := range overlayLines {
		targetRow := startRow + i
		if targetRow >= 0 && targetRow < len(result) {
			baseLine := result[targetRow]
			overlayWidth := lipgloss.Width(overlayLine)

			// FIX: Use ANSI-aware skip function to get remainder after overlay
			afterOverlay := skipVisualWidth(baseLine, overlayWidth)

			result[targetRow] = overlayLine + afterOverlay
		}
	}

	return strings.Join(result, "\n")
}

func (m model) tryExit() (model, tea.Cmd) {
	agents := m.manager.List()
	running := 0
	for _, a := range agents {
		if a.Status == agent.StatusRunning {
			running++
		}
	}

	// Count active planning tabs
	activePlanningTabs := 0
	for _, tab := range m.tabManager.GetTabs() {
		if planningTab, ok := tab.(*PlanningTab); ok && planningTab.IsActive() {
			activePlanningTabs++
		}
	}

	if running > 0 || activePlanningTabs > 0 {
		m.confirmingExit = true
		if running > 0 && activePlanningTabs > 0 {
			m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("There are %d agents running and %d active planning sessions. Stop all and exit? (y/N)", running, activePlanningTabs)))
		} else if running > 0 {
			m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("There are %d agents still running. Stop all and exit? (y/N)", running)))
		} else {
			m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("There are %d active planning sessions. Close all and exit? (y/N)", activePlanningTabs)))
		}
		return m, nil
	}

	// Perform cleanup before exit
	m = m.performExitCleanup()
	m.quitting = true
	return m, tea.Quit
}

// performExitCleanup performs comprehensive cleanup when exiting
func (m model) performExitCleanup() model {
	cleanupErrors := []string{}

	// Cancel in-flight review operations (Fix #2)
	if m.reviewCancel != nil {
		m.reviewCancel()
		m.reviewCancel = nil
	}

	// Stop all agents
	if m.manager != nil {
		m.manager.StopAll()
	}

	// Stop watcher
	if m.watcher != nil {
		m.watcher.Stop()
	}

	// Stop review watcher
	if m.reviewWatcher != nil {
		m.reviewWatcher.Stop()
	}

	// Deactivate logging if active
	if m.loggingActive {
		if err := m.deactivateLogging(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("Logging deactivation warning: %v", err))
		}
	}

	// Cleanup all planning tabs. Save sessions synchronously (fast, local I/O),
	// then close tabs concurrently under a single overall deadline. Each tab's
	// Close() can block up to ~3s waiting for its ACP process to exit gracefully;
	// doing them sequentially made Ctrl+C hang for roughly 3s × tab-count (~30s
	// at the 10-tab max), defeating the "reliable escape hatch" guarantee. Run
	// them in parallel and cap the total wait.
	var closeWG sync.WaitGroup
	for _, tab := range m.tabManager.GetTabs() {
		if planningTab, ok := tab.(*PlanningTab); ok {
			// Session I/O runs synchronously on this goroutine (it's fast local
			// I/O, and SessionManager has no locking): save state, then apply the
			// tab's own session cleanup. Keeping all session-directory writes
			// single-threaded here means the parallel closes below — and the
			// CleanupSessionsOnExit sweep — never race on session files, even
			// when a close overruns the deadline.
			planningTab.SaveSession()
			planningTab.CleanupSession()

			// Only the runtime-resource teardown (ACP client shutdown, which can
			// block ~3s) runs in parallel under the overall deadline.
			closeWG.Add(1)
			go func(pt *PlanningTab) {
				defer closeWG.Done()
				pt.closeResources()
			}(planningTab)
		}
	}

	// Wait for all closes, but never longer than the overall deadline.
	closeDone := make(chan struct{})
	go func() {
		closeWG.Wait()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(exitCleanupTimeout):
		cleanupErrors = append(cleanupErrors, fmt.Sprintf("planning-tab cleanup exceeded %s; exiting anyway", exitCleanupTimeout))
	}

	// Cleanup planning sessions (remove orphaned/completed)
	if err := m.tabManager.CleanupSessionsOnExit(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Sprintf("Planning session cleanup warning: %v", err))
	}

	// Stop context tracking
	if m.footerManager != nil && m.footerManager.GetContextTracker() != nil {
		m.footerManager.GetContextTracker().StopPlanningSession()
	}

	// Session manager cleanup
	if err := m.sessionManager.CleanupOnExit(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Sprintf("Session cleanup warning: %v", err))
	}

	// Report cleanup issues (non-blocking)
	if len(cleanupErrors) > 0 {
		m = m.appendActivity(m.styles.Warning.Render("Cleanup warnings:"))
		for _, err := range cleanupErrors {
			m = m.appendActivity(m.styles.Warning.Render(fmt.Sprintf("  %s", err)))
		}
	}

	return m
}

// switchActiveTab switches to a tab by index and updates context tracking
func (m model) switchActiveTab(index int) (model, tea.Cmd) {
	var cmds []tea.Cmd

	// Capture focus state from current active tab before switching
	if currentTab := m.tabManager.GetActiveTab(); currentTab != nil {
		currentFocus := currentTab.CaptureFocusState()
		m.tabFocusStates[currentTab.ID()] = currentFocus
		logging.Debug("captured focus state",
			"tab_id", currentTab.ID(),
			"focus_target", currentFocus)
	}

	// Switch to new tab
	m.tabManager.SetActiveTab(index)

	// Restore focus state for newly active tab
	if activeTab := m.tabManager.GetActiveTab(); activeTab != nil {
		// Handle planning context tracking
		if activeTab.Type() == TabTypePlanning {
			if !m.footerManager.GetContextTracker().IsActive() {
				m.footerManager.GetContextTracker().StartPlanningSession("claude-sonnet-4")
			}
		} else {
			if m.footerManager.GetContextTracker().IsActive() {
				m.footerManager.GetContextTracker().StopPlanningSession()
			}
		}

		// Retrieve previous focus state or default based on tab type. Most
		// tabs (Main, Planning, Agent, Log) default to footer focus on first
		// visit, matching historical behavior. The Reviews tab is the
		// exception: it has its own row-navigation/decision-key surface
		// (up/down/enter/p/r/R/d, see ReviewsTab.Update) that must be
		// reachable on first visit too, not just on later re-visits once
		// CaptureFocusState has had a chance to report FocusTargetRows —
		// otherwise a user's very first F2/[/] into the Reviews tab would
		// still land with the footer focused, reintroducing the AC2
		// unreachability bug for that one visit.
		previousFocus, exists := m.tabFocusStates[activeTab.ID()]
		if !exists {
			if activeTab.Type() == TabTypeReviews {
				previousFocus = FocusTargetRows
			} else {
				previousFocus = FocusTargetFooter
			}
		}

		logging.Debug("restoring focus state",
			"tab_id", activeTab.ID(),
			"focus_target", previousFocus)

		// Apply focus state to the tab
		if cmd := activeTab.RestoreFocusState(previousFocus); cmd != nil {
			cmds = append(cmds, cmd)
		}

		// Apply footer input focus based on target
		if previousFocus == FocusTargetFooter {
			m.input.SetFocus(true)
			cmds = append(cmds, m.input.Focus())
		} else {
			m.input.SetFocus(false)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) executeCommand(input string) (model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch strings.ToLower(cmd) {
	case "watch":
		if len(parts) < 2 {
			m = m.appendActivity(m.styles.Error.Render("Usage: watch start|stop"))
			return m, nil
		}
		return m.handleWatch(parts[1])
	case "status":
		return m.handleStatus()
	case "stop":
		if len(parts) < 2 {
			m = m.appendActivity(m.styles.Error.Render("Usage: stop <issue-number>"))
			return m, nil
		}
		return m.handleStop(parts[1])
	case "plan":
		// Check if this is "plan classic" command
		if len(parts) >= 2 && strings.ToLower(parts[1]) == "classic" {
			// Extract description after "classic"
			description := ""
			if len(parts) > 2 {
				description = strings.Join(parts[2:], " ")
			}
			return m.handlePlanClassic(description)
		}

		// Regular plan command - uses ACP-based planning tabs
		description := ""
		if len(parts) > 1 {
			description = strings.Join(parts[1:], " ")
		}
		return m.handlePlan(description)
	case "exit":
		return m.tryExit()
	case "about":
		return m.handleAbout()
	case "help":
		return m.handleHelp()
	case "theme":
		args := []string{}
		if len(parts) > 1 {
			args = parts[1:]
		}
		return m.handleTheme(args)
	case "logs":
		return m.handleLogs()
	case "log":
		args := []string{}
		if len(parts) > 1 {
			args = parts[1:]
		}
		return m.handleLog(args)
	case "review":
		args := []string{}
		if len(parts) > 1 {
			args = parts[1:]
		}
		return m.handleReview(args)
	case "decide":
		args := []string{}
		if len(parts) > 1 {
			args = parts[1:]
		}
		return m.handleDecide(args)
	case "finalize":
		return m.handleFinalize(nil)
	default:
		m = m.appendActivity(m.styles.Error.Render(fmt.Sprintf("Unknown command: %s", cmd)))
		return m, nil
	}
}

// Run starts the TUI, redirecting log output to a file.
func (m model) updateAgentTabs() model {
	agents := m.manager.List()

	// Track current agent IDs
	currentAgents := make(map[string]bool)
	for _, ag := range agents {
		currentAgents[ag.ID] = true

		// Create tab for new agents when they start (no auto-switch)
		if !m.knownAgents[ag.ID] {
			agentTab := NewAgentTab(ag.ID, m.manager, m.styles)
			m.tabManager.AddTab(agentTab)
			m.knownAgents[ag.ID] = true
		}
	}

	// Prune knownAgents entries for agents no longer in the manager
	for id := range m.knownAgents {
		if !currentAgents[id] {
			delete(m.knownAgents, id)
		}
	}

	return m
}

// activateLogging activates the logging subsystem when a log viewer tab opens
func (m *model) activateLogging(logTab *LogTab) error {
	if m.loggingActive {
		return fmt.Errorf("logging already active")
	}

	// Get the ring buffer from the log tab
	ringBuffer := logTab.GetRingBuffer()
	if ringBuffer == nil {
		return fmt.Errorf("log tab has no ring buffer")
	}

	// Create file handler with configuration
	fileConfig := logging.FileOutputConfig{
		LogDir:        m.config.Logging.LogDir,
		MaxFileSizeMB: m.config.Logging.MaxFileSizeMB,
	}
	fileHandler, err := logging.NewFileHandler(fileConfig)
	if err != nil {
		return fmt.Errorf("failed to create file handler: %w", err)
	}

	// Create a custom writer that writes to both ring buffer and file handler
	multiWriter := &loggingMultiWriter{
		ringBuffer:  ringBuffer,
		fileHandler: fileHandler,
	}

	// Activate logging with the multi-writer
	logging.Activate(multiWriter)

	// Set the configured log level
	if err := logging.SetLevel(logTab.level); err != nil {
		// Roll back activation on failure
		logging.Deactivate()
		fileHandler.Close()
		return fmt.Errorf("failed to set log level: %w", err)
	}

	// Store state
	m.loggingActive = true
	m.activeLogTabID = logTab.ID()
	m.activeFileHandler = fileHandler

	log.Printf("Logging activated: level=%s, buffer_size=%d", logTab.level, logTab.bufferSize)
	return nil
}

// deactivateLogging deactivates the logging subsystem when the log viewer tab closes
func (m *model) deactivateLogging() error {
	if !m.loggingActive {
		return nil // Already inactive
	}

	// Deactivate the logger (removes handlers)
	logging.Deactivate()

	// Close the file handler
	if m.activeFileHandler != nil {
		if err := m.activeFileHandler.Close(); err != nil {
			log.Printf("Error closing file handler: %v", err)
		}
		m.activeFileHandler = nil
	}

	// Clear state
	m.loggingActive = false
	m.activeLogTabID = ""

	log.Printf("Logging deactivated")
	return nil
}

// checkLogTabClosed checks if the log tab was closed and deactivates logging if necessary.
// This should be called after any operation that might close tabs (e.g., tabManager.Update).
func (m *model) checkLogTabClosed() {
	if !m.loggingActive {
		return
	}

	// Check if the log tab with the active ID still exists
	logTab := m.tabManager.GetLogTab()
	if logTab == nil || logTab.ID() != m.activeLogTabID {
		// Log tab was closed, deactivate logging
		if err := m.deactivateLogging(); err != nil {
			log.Printf("Error deactivating logging after tab close: %v", err)
		}
	}
}

// reviewContentTabIDs returns the IDs of all TabTypeReviewContent tabs
// currently in tm, in tm.GetTabs() order. Callers that close a tab via a
// path other than the dedicated "esc" handler (mouse-click on a header's
// close button, "ctrl+w") capture this before a close and compare the
// count against the count after — a drop means a review content tab was
// the one just closed, without needing CloseTab/HandleTabHeaderClick to
// report which tab type they removed. A count comparison is sufficient
// because those close paths remove at most one tab per invocation, so a
// length drop unambiguously identifies a single review-content close. The
// IDs (rather than a bare count) are returned so a caller that needs to
// know *which* tab closed can diff the lists, but the current callers only
// need the length. See the "esc" handler above for the reference behavior
// this is mirroring.
func reviewContentTabIDs(tm *TabManager) []string {
	var ids []string
	for _, tab := range tm.GetTabs() {
		if tab.Type() == TabTypeReviewContent {
			ids = append(ids, tab.ID())
		}
	}
	return ids
}

// findReviewsTabIndex returns the index of the permanent Reviews tab. The
// Reviews tab is always present (IsClosable() == false, added once in
// newModel), so -1 is only a defensive fallback that should not occur in
// practice. Used by the ESC handling that closes a review content window
// and returns focus to the Reviews tab it was opened from.
func (m model) findReviewsTabIndex() int {
	for i, tab := range m.tabManager.GetTabs() {
		if tab.Type() == TabTypeReviews {
			return i
		}
	}
	return -1
}

// loggingMultiWriter writes to both ring buffer and file handler
// It implements io.Writer to work with charmbracelet/log
type loggingMultiWriter struct {
	ringBuffer  *logging.RingBuffer
	fileHandler *logging.FileHandler
}

func (lmw *loggingMultiWriter) Write(p []byte) (n int, err error) {
	// Write to file handler first
	n, err = lmw.fileHandler.Write(p)
	if err != nil {
		return n, err
	}

	// Parse JSON structured log entry
	// Unmarshal into a map to capture all fields
	var rawEntry map[string]interface{}
	if err := json.Unmarshal(p, &rawEntry); err != nil {
		// JSON parse failed, but file write succeeded - don't fail the write
		// This could happen during transition or with malformed input
		return n, nil
	}

	// Extract level and message
	var levelStr, message string
	if l, ok := rawEntry["level"].(string); ok {
		levelStr = l
	}
	if m, ok := rawEntry["msg"].(string); ok {
		message = m
	}

	// Map string level to log.Level constant
	level := mapStringToLogLevel(levelStr)

	// Convert remaining fields to key-value pairs (exclude time, level, message)
	// Normalize float64 values that are actually integers
	var keyvals []interface{}
	for k, v := range rawEntry {
		if k != "time" && k != "level" && k != "msg" {
			// JSON unmarshals all numbers as float64; normalize integers
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				keyvals = append(keyvals, k, int64(f))
			} else {
				keyvals = append(keyvals, k, v)
			}
		}
	}

	// Add structured entry to ring buffer
	lmw.ringBuffer.Add(level, message, keyvals...)

	return n, nil
}

// mapStringToLogLevel converts a string log level to charmbracelet/log.Level constant
func mapStringToLogLevel(levelStr string) clog.Level {
	switch strings.ToLower(levelStr) {
	case "debug", "debu", "dbug":
		return clog.DebugLevel
	case "info":
		return clog.InfoLevel
	case "warn", "warning":
		return clog.WarnLevel
	case "error", "erro", "err":
		return clog.ErrorLevel
	default:
		return clog.InfoLevel // Default to info for unknown levels
	}
}

func Run(w *watcher.Watcher, m *agent.Manager, cfg *config.Config, initialCommand string) error {
	logPath := ".howmux/kiro-krew.log"
	if err := os.MkdirAll(".howmux", 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)

	logReader, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log reader: %w", err)
	}
	defer logReader.Close()

	// Seek to end so we only show new entries
	info, err := logReader.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat log file: %w", err)
	}
	startPos := info.Size()

	mdl := newModel(w, m, cfg, logFile, logReader, initialCommand)
	mdl.lastLogPos = startPos

	// Setup cleanup on exit
	defer func() {
		if err := mdl.sessionManager.CleanupOnExit(); err != nil {
			log.Printf("Session cleanup error: %v", err)
		}
	}()

	p := tea.NewProgram(mdl)
	_, err = p.Run()
	return err
}
