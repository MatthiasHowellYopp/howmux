package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// finalizePreflightState models whether the finalize gate's required
// scripts are currently known to be resolvable on $PATH. Distinct from
// finalizeState (finalize.go), which tracks the dry-run/confirm/live-run
// subprocess lifecycle — this tracks asset availability, checked before that
// lifecycle is ever allowed to start.
type finalizePreflightState int

const (
	// finalizePreflightUnknown is the initial state: no check has run yet
	// this session. Rendered identically to finalizePreflightOK (no status
	// line) so a fresh session doesn't show a spurious warning before the
	// user has ever tried to finalize — matching handleFinalize's existing
	// "check happens on demand" behavior, just adding a cached,
	// re-checkable result for the Reviews tab's status line.
	finalizePreflightUnknown finalizePreflightState = iota
	finalizePreflightOK
	finalizePreflightFailed
)

// checkFinalizeAssetsFunc is the seam runFinalizePreflightCmd calls through.
// Package-level so tests can stub review.CheckFinalizeAssets's result
// without touching the real $PATH — mirrors every other *Func seam in this
// package (finalizeScriptPathFunc, finalizeCommandFunc, userHomeDirFunc).
var checkFinalizeAssetsFunc = review.CheckFinalizeAssets

// finalizePreflightResultMsg reports the outcome of a (re-)run of the
// finalize asset preflight. err is nil on success.
type finalizePreflightResultMsg struct {
	err error
}

// finalizeRetryPreflightMsg is emitted by the Reviews tab's "F" key and
// consumed by model.Update, which responds by returning
// runFinalizePreflightCmd() — the same round-trip pattern every other
// ReviewsTab action already uses (ReviewsTab has no direct reference to run
// a tea.Cmd against checkFinalizeAssetsFunc itself by design).
type finalizeRetryPreflightMsg struct{}

// runFinalizePreflightCmd returns a tea.Cmd that runs checkFinalizeAssetsFunc
// and reports the result as finalizePreflightResultMsg. Run as a tea.Cmd
// (not called inline) even though a $PATH lookup is normally fast, because
// handleFinalize and the Reviews tab's retry key both need this to be
// non-blocking on the Update goroutine — consistent with this codebase's
// existing convention that any external-process/filesystem check funnels
// through a tea.Cmd rather than running directly inside Update (see
// runFinalizeCmd, checkForUpdateCmd).
func runFinalizePreflightCmd() tea.Cmd {
	return func() tea.Msg {
		return finalizePreflightResultMsg{err: checkFinalizeAssetsFunc()}
	}
}

// applyFinalizePreflightResult stores the outcome of a finalize asset
// preflight check (err is nil on success) onto m.finalizePreflightState /
// m.finalizePreflightErr, and propagates the same result to the Reviews
// tab (via SetFinalizePreflightState) so its status line and
// CopyableContent() output stay in sync — see issue #88's design spec,
// "CopyableContent Flow". Both handleFinalize's synchronous preflight call
// and model.Update's finalizePreflightResultMsg case (the async retry
// path) call this same helper so the two paths can never drift on what
// "current preflight state" means. Returns the updated model, matching
// this codebase's existing model-returning-not-mutating convention (model
// is a value type; every Update case and command handler reassigns m
// rather than taking a pointer receiver).
//
// The Reviews tab is looked up via findReviewsTab; if it is nil (should not
// happen in practice — it's a permanent tab added once in newModel, see
// findReviewsTab's doc comment) the propagation is simply skipped, since
// there is nothing to update.
func (m model) applyFinalizePreflightResult(err error) model {
	if err != nil {
		m.finalizePreflightState = finalizePreflightFailed
		m.finalizePreflightErr = err.Error()
	} else {
		m.finalizePreflightState = finalizePreflightOK
		m.finalizePreflightErr = ""
	}
	if rt := m.findReviewsTab(); rt != nil {
		rt.SetFinalizePreflightState(m.finalizePreflightState, m.finalizePreflightErr)
	}
	return m
}
