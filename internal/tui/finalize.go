package tui

// Package-level note on future cancellation support: runFinalizeCmd is
// already built on exec.CommandContext, so wiring a future "cancel" key to
// call m.finalizeCancel() is a small, additive change (the subprocess would
// receive its termination signal via the context exactly like
// review.RunReview's existing cancellation already works) and requires no
// restructuring of this design. Not built in this issue since no
// acceptance criterion asks for it — see issue #87's design spec,
// "Cancellation".

import (
	"context"
	"io"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/agent"
)

// finalizeState models the linear finalize workflow: idle -> dry-run-running
// -> awaiting-confirmation -> live-running -> idle (with an error from
// either running state also returning to idle). An explicit enum is used
// rather than a bare bool like confirmingExit because this state machine has
// more than two states — dry-run-running and live-running are both "busy"
// but must be distinguishable so a stray finalizeDryRunMsg arriving late
// can't be mistaken for a live-run completion.
type finalizeState int

const (
	finalizeIdle finalizeState = iota
	finalizeDryRunRunning
	finalizeAwaitingConfirmation
	finalizeLiveRunning
)

// finalizePollInterval is the poll cadence for streaming finalize-reviews.sh
// output into the preview window, matching LogTab's logPollInterval — no
// reason to pick a different cadence for a structurally identical poll.
const finalizePollInterval = 100 * time.Millisecond

// finalizeCaptureBufferSize is the ring-buffer capacity (in lines) for the
// OutputCapture backing a finalize run's preview window. A single
// finalize-reviews.sh run's total output (dry-run + live, per the design
// spec's "Window Content Model") is at most a few hundred lines, so this is
// sized generously above that to avoid evicting the dry-run's early output
// before the user gets to read it.
const finalizeCaptureBufferSize = 2000

// finalizeDryRunMsg reports the terminal outcome of a dry-run
// finalize-reviews.sh invocation (exit 0). lines holds the full captured
// stdout+stderr (interleaved in write order, via the shared OutputCapture —
// see runFinalizeCmd), for a final, complete render of the preview window
// once streaming stops. cancel is the context.CancelFunc for the run that
// just completed, retained so a still-open review window's cleanup path has
// something to call if the user closes the window instead of confirming —
// mirrors reviewCompleteMsg's cancel field.
type finalizeDryRunMsg struct {
	lines  []string
	cancel context.CancelFunc
}

// finalizeCompleteMsg reports the terminal outcome of a LIVE (non-dry-run)
// finalize-reviews.sh invocation (exit 0). Distinct from finalizeDryRunMsg
// (rather than a shared type with a dryRun bool field) because the two
// carry different follow-on obligations in model.Update: finalizeDryRunMsg
// transitions to finalizeAwaitingConfirmation and leaves the window open for
// the user to read; finalizeCompleteMsg transitions to finalizeIdle and is
// what triggers the "finalize complete" success activity line users
// actually care about (AC7). Collapsing both into one type with a bool
// would push that branching into a single case arm instead of letting two
// case arms each do one thing, matching how this codebase already prefers
// reviewCompleteMsg/editorDoneMsg as distinct types over discriminated
// unions.
type finalizeCompleteMsg struct {
	lines []string
}

// finalizeErrorMsg reports a failed finalize-reviews.sh invocation — either
// the script exited non-zero, or it could not be spawned at all (e.g. not
// found on $PATH). dryRun records which phase failed, since the error
// activity line's wording and the state machine's fallback transition both
// depend on it (a failed dry-run and a failed live run both return to
// finalizeIdle, but with different messages). lines holds whatever partial
// output was captured before failure, for inclusion in the error activity
// line / left visible in the window, matching how a real terminal would
// leave partial output visible after a Ctrl-C.
type finalizeErrorMsg struct {
	dryRun bool
	err    error
	lines  []string
}

// finalizeTickMsg drives the poll loop that copies newly captured
// finalize-reviews.sh output lines into the open review content window's
// viewport, mirroring LogTab's TickMsg but kept as its own type per this
// codebase's one-tick-type-per-poll-loop convention (see editorDoneMsg's
// doc comment in tui.go for the analogous reasoning applied to completion
// messages).
type finalizeTickMsg time.Time

// finalizeCommandFunc is the subprocess-execution seam runFinalizeCmd
// invokes through to build the finalize-reviews.sh exec.Cmd, mirroring
// bodyEditCommandFunc's/execCommandFunc's role elsewhere in this codebase.
// It takes (ctx, name, args...) — exec.CommandContext's signature — rather
// than exec.Command's, because finalize-reviews.sh is a long-running,
// potentially-networked batch operation the user may want to cancel
// mid-flight, so it must be spawned with a cancellable context from the
// start. Package-level so tests can substitute a fake command instead of
// spawning a real finalize-reviews.sh.
var finalizeCommandFunc = exec.CommandContext

// finalizeScriptPathFunc resolves the path to finalize-reviews.sh. Unlike
// decisionwriter.go's scriptPathFunc (which resolves a repo-relative path
// for set-review-decision.sh), finalize-reviews.sh is intentionally not
// vendored into .howmux/scripts/ — it lives in ai-resources/, external to
// this repo (see issue #85's spec) — so this resolves via $PATH instead,
// matching custom-scripts.md's documented ~/.local/bin/ symlink setup.
// Package-level so tests can override it without touching the real
// filesystem/PATH.
var finalizeScriptPathFunc = defaultFinalizeScriptPath

func defaultFinalizeScriptPath() (string, error) {
	path, err := exec.LookPath("finalize-reviews.sh")
	if err != nil {
		return "", &finalizeScriptNotFoundError{}
	}
	return path, nil
}

// finalizeScriptNotFoundError is a distinct error type (rather than a bare
// fmt.Errorf) so callers/tests can identify this specific failure mode
// without string-matching, while still satisfying the error interface with
// a clear, actionable message.
type finalizeScriptNotFoundError struct{}

func (e *finalizeScriptNotFoundError) Error() string {
	return "finalize-reviews.sh not found on PATH — see ai-resources setup"
}

// runFinalizeCmd runs finalize-reviews.sh (with --dry-run if dryRun is
// true) via finalizeCommandFunc, streaming its combined stdout+stderr into
// capture as it's produced (for the poll loop to pick up), and returns
// exactly one terminal tea.Msg once the process exits: finalizeDryRunMsg or
// finalizeCompleteMsg on exit 0 (branching on dryRun), or finalizeErrorMsg
// on any error (path resolution failure, spawn failure, or non-zero exit).
//
// Must run on a goroutine, never on the Update goroutine — this function
// itself is fine to call directly since tea.Cmd values ARE goroutine entry
// points by construction (Bubble Tea's runtime invokes each returned
// tea.Cmd on its own goroutine before feeding the resulting tea.Msg back
// into Update), matching every existing tea.Cmd in this codebase (reviewCmd
// in handleReview, checkForUpdateCmd, etc.) — there is no separate manual
// goroutine spawn required or expected here.
func runFinalizeCmd(ctx context.Context, dryRun bool, capture *agent.OutputCapture, cancel context.CancelFunc) tea.Cmd {
	return func() tea.Msg {
		scriptPath, err := finalizeScriptPathFunc()
		if err != nil {
			return finalizeErrorMsg{dryRun: dryRun, err: err}
		}

		args := []string{scriptPath}
		if dryRun {
			args = append(args, "--dry-run")
		}
		cmd := finalizeCommandFunc(ctx, "bash", args...)

		stdout := agent.NewCaptureWriter(io.Discard, capture, "")
		stderr := agent.NewCaptureWriter(io.Discard, capture, "")
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		err = cmd.Run()
		lines := capture.GetLines()

		if err != nil {
			return finalizeErrorMsg{dryRun: dryRun, err: err, lines: lines}
		}
		if dryRun {
			return finalizeDryRunMsg{lines: lines, cancel: cancel}
		}
		return finalizeCompleteMsg{lines: lines}
	}
}

// pollFinalizeOutputCmd re-arms itself on a tea.Tick (matching LogTab's
// logPollInterval idiom) as long as a finalize run is in flight. It does
// NOT itself decide whether new data arrived — that check happens in the
// finalizeTickMsg case arm in model.Update, which compares
// capture.Generation() against m.finalizeLastGen and only calls
// ReviewContentTab.AppendFromCapture() when they differ, exactly mirroring
// LogTab.Update's lastWriteCounter comparison. This function's only job is
// producing the next tick.
func pollFinalizeOutputCmd() tea.Cmd {
	return tea.Tick(finalizePollInterval, func(t time.Time) tea.Msg {
		return finalizeTickMsg(t)
	})
}
