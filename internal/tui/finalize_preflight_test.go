package tui

import (
	"errors"
	"testing"
)

// stubCheckFinalizeAssets replaces checkFinalizeAssetsFunc for the duration
// of a test and restores the original via t.Cleanup, mirroring the
// save/restore pattern used throughout this codebase's other *Func seams
// (e.g. review.lookPathFunc in assets_test.go).
func stubCheckFinalizeAssets(t *testing.T, fn func() error) {
	t.Helper()
	orig := checkFinalizeAssetsFunc
	checkFinalizeAssetsFunc = fn
	t.Cleanup(func() {
		checkFinalizeAssetsFunc = orig
	})
}

func TestRunFinalizePreflightCmd_Success(t *testing.T) {
	stubCheckFinalizeAssets(t, func() error { return nil })

	cmd := runFinalizePreflightCmd()
	if cmd == nil {
		t.Fatal("runFinalizePreflightCmd() returned nil tea.Cmd")
	}

	msg := cmd()
	result, ok := msg.(finalizePreflightResultMsg)
	if !ok {
		t.Fatalf("expected finalizePreflightResultMsg, got %T", msg)
	}
	if result.err != nil {
		t.Errorf("expected nil err, got %v", result.err)
	}
}

func TestRunFinalizePreflightCmd_Failure(t *testing.T) {
	stubErr := errors.New("boom: missing scripts on PATH")
	stubCheckFinalizeAssets(t, func() error { return stubErr })

	cmd := runFinalizePreflightCmd()
	if cmd == nil {
		t.Fatal("runFinalizePreflightCmd() returned nil tea.Cmd")
	}

	msg := cmd()
	result, ok := msg.(finalizePreflightResultMsg)
	if !ok {
		t.Fatalf("expected finalizePreflightResultMsg, got %T", msg)
	}
	if result.err != stubErr {
		t.Errorf("expected the exact stubbed error unchanged (no re-wrapping), got %v", result.err)
	}
}

func TestFinalizePreflightState_DistinctFromFinalizeState(t *testing.T) {
	// finalizePreflightState and finalizeState are deliberately separate
	// enum types (see finalize_preflight.go's doc comment). This test just
	// asserts the three finalizePreflightState constants exist and are
	// distinct values.
	states := map[finalizePreflightState]string{
		finalizePreflightUnknown: "unknown",
		finalizePreflightOK:      "ok",
		finalizePreflightFailed:  "failed",
	}
	if len(states) != 3 {
		t.Errorf("expected 3 distinct finalizePreflightState values, got %d", len(states))
	}
}
