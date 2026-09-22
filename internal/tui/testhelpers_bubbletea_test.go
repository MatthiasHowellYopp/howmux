package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// activityContains reports whether any line in m.activityLines contains
// substr. Originally defined in finalize_integration_test.go (deleted by
// issue #109); relocated here — a shared, finalize-agnostic test-helpers
// file — since decide_integration_test.go and other decide-related test
// files also depend on it.
func activityContains(m model, substr string) bool {
	for _, l := range m.activityLines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// collectMsgs repeatedly runs cmd (and any tea.Cmd it returns via
// tea.Batch) through model.Update until no more commands are produced, or
// until msgs runs out — a small helper to walk a Bubble Tea Update/Cmd
// chain synchronously in tests without a real tea.Program. tea.Batch's
// returned cmd, when invoked, returns a tea.BatchMsg ([]tea.Cmd); this
// helper recursively invokes every sub-command and collects every
// resulting tea.Msg (flattening nested batches). Originally defined in
// finalize_integration_test.go (deleted by issue #109); relocated here so
// decide-action tests (which also dispatch batched tea.Cmd chains) can use
// it.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, collectMsgs(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// drainBatchForType runs cmd (unwrapping any tea.Batch) and returns the
// first collected tea.Msg matching type T, failing the test if none is
// found. Originally defined in finalize_integration_test.go (deleted by
// issue #109); relocated here so decide-action integration tests can pull
// a specific terminal message (e.g. decidePostCompleteMsg) out of a
// dispatchDecideAction-produced batched tea.Cmd without needing a real
// tea.Program.
func drainBatchForType[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	for _, msg := range collectMsgs(cmd) {
		if typed, ok := msg.(T); ok {
			return typed
		}
	}
	var zero T
	t.Fatalf("expected a message of type %T in the command chain, none found", zero)
	return zero
}

// findRepoRootForTest walks up from the current working directory (the
// package directory, per Go's test-execution convention) to find the repo
// root — identified by the presence of .howmux/scripts/set-review-decision.sh
// — so a test can t.Chdir there and exercise a real repo-relative script
// rather than a fake. Fails the test if no such ancestor is found, rather
// than silently running against the wrong script. Originally defined in
// decide_integration_test.go; relocated here since body_edit_test.go also
// depends on it.
func findRepoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, ".howmux", "scripts", "set-review-decision.sh")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate .howmux/scripts/set-review-decision.sh in any ancestor of %s", dir)
		}
		dir = parent
	}
}
