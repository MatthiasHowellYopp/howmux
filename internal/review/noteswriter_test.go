package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeExecAndNotesScriptPath mirrors withFakeExecAndScriptPath
// (decisionwriter_test.go) but overrides notesScriptPathFunc instead of
// scriptPathFunc, since NotesWriter resolves its script path through its
// own seam while sharing execCommandFunc/statFunc with DecisionWriter.
func withFakeExecAndNotesScriptPath(t *testing.T, exitCode int, stderrText string) (*int, *[]string) {
	t.Helper()
	origExec := execCommandFunc
	origScriptPath := notesScriptPathFunc
	origStat := statFunc
	callCount := 0
	var capturedArgv []string
	execCommandFunc = fakeExecCommand(&callCount, &capturedArgv, exitCode, stderrText)
	notesScriptPathFunc = func() string { return "/fake/set-review-notes.sh" }
	// The script pre-check statFunc must succeed so tests exercise the exec
	// path; the spool file itself is resolved by resolveSpoolPath's real
	// os.Stat, so exec-path tests pass a real temp spool via newPendingSpool.
	statFunc = func(string) (os.FileInfo, error) { return nil, nil }
	t.Cleanup(func() {
		execCommandFunc = origExec
		notesScriptPathFunc = origScriptPath
		statFunc = origStat
	})
	return &callCount, &capturedArgv
}

func TestNotesWriter_EmptySpoolPath_ExecNeverCalled(t *testing.T) {
	callCount, _ := withFakeExecAndNotesScriptPath(t, 0, "")

	nw := NewNotesWriter()
	err := nw.SetNotes("", "some notes")

	if err == nil {
		t.Fatal("expected error for empty spool path, got nil")
	}
	if !strings.Contains(err.Error(), "no spool path provided") {
		t.Errorf("expected error to mention 'no spool path provided', got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected execCommandFunc to never be called, got %d calls", *callCount)
	}
}

func TestNotesWriter_ExecSuccess_ReturnsNilError(t *testing.T) {
	callCount, capturedArgv := withFakeExecAndNotesScriptPath(t, 0, "")
	spool := newPendingSpool(t)

	nw := NewNotesWriter()
	err := nw.SetNotes(spool, "please fix the null check")

	if err != nil {
		t.Fatalf("expected nil error on exit 0, got: %v", err)
	}
	if *callCount != 1 {
		t.Errorf("expected execCommandFunc to be called exactly once, got %d", *callCount)
	}
	want := []string{"bash", "/fake/set-review-notes.sh", spool, "please fix the null check"}
	if len(*capturedArgv) != len(want) {
		t.Fatalf("argv = %v, want %v", *capturedArgv, want)
	}
	for i := range want {
		if (*capturedArgv)[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, (*capturedArgv)[i], want[i])
		}
	}
}

func TestNotesWriter_ExecSuccess_EmptyNotesAllowed(t *testing.T) {
	callCount, capturedArgv := withFakeExecAndNotesScriptPath(t, 0, "")
	spool := newPendingSpool(t)

	nw := NewNotesWriter()
	err := nw.SetNotes(spool, "")

	if err != nil {
		t.Fatalf("expected nil error for empty notes value, got: %v", err)
	}
	if *callCount != 1 {
		t.Errorf("expected execCommandFunc to be called exactly once, got %d", *callCount)
	}
	if len(*capturedArgv) != 4 || (*capturedArgv)[3] != "" {
		t.Errorf("expected argv[3] to be the empty notes string, got argv = %v", *capturedArgv)
	}
}

func TestNotesWriter_ExecFailure_ErrorContainsStderr(t *testing.T) {
	_, _ = withFakeExecAndNotesScriptPath(t, 2, "error: notes text must not contain a newline")
	spool := newPendingSpool(t)

	nw := NewNotesWriter()
	err := nw.SetNotes(spool, "line one\nline two")

	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "must not contain a newline") {
		t.Errorf("expected error to contain captured stderr text, got: %v", err)
	}
	if !strings.Contains(err.Error(), "set-review-notes failed") {
		t.Errorf("expected error to be wrapped with 'set-review-notes failed', got: %v", err)
	}
}

// TestNotesWriter_AlreadyFinalized distinguishes a drained (done/) review
// from a never-reviewed one, mirroring
// TestDecisionWriter_AlreadyFinalized.
func TestNotesWriter_AlreadyFinalized(t *testing.T) {
	callCount, _ := withFakeExecAndNotesScriptPath(t, 0, "")

	// Create the file only in done/, with the pending/ path passed in.
	base := t.TempDir()
	doneDir := filepath.Join(base, "done")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := "pr-review-owner-repo-1.md"
	if err := os.WriteFile(filepath.Join(doneDir, filename), []byte("---\ndecision: post\ndecision_notes: \"\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(base, "pending", filename)

	nw := NewNotesWriter()
	err := nw.SetNotes(pendingPath, "some notes")

	if err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("expected 'already finalized' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec for an already-finalized review, got %d calls", *callCount)
	}
}

// TestNotesWriter_NoSpoolFile reports the never-reviewed case distinctly,
// mirroring TestDecisionWriter_NoSpoolFile.
func TestNotesWriter_NoSpoolFile(t *testing.T) {
	callCount, _ := withFakeExecAndNotesScriptPath(t, 0, "")
	missing := filepath.Join(t.TempDir(), "pending", "pr-review-x-1.md")

	nw := NewNotesWriter()
	err := nw.SetNotes(missing, "some notes")

	if err == nil || !strings.Contains(err.Error(), "no spool file") {
		t.Errorf("expected 'no spool file' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when there is no spool file, got %d calls", *callCount)
	}
}

// TestNotesWriter_ScriptMissing surfaces an explicit path error instead of
// a cryptic blank one when the notes script cannot be found, mirroring
// TestDecisionWriter_ScriptMissing.
func TestNotesWriter_ScriptMissing(t *testing.T) {
	callCount, _ := withFakeExecAndNotesScriptPath(t, 0, "")
	spool := newPendingSpool(t)

	// Override statFunc to fail specifically for the script path.
	statFunc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	nw := NewNotesWriter()
	err := nw.SetNotes(spool, "some notes")

	if err == nil || !strings.Contains(err.Error(), "notes script not found") {
		t.Errorf("expected 'notes script not found' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when the script is missing, got %d calls", *callCount)
	}
}
