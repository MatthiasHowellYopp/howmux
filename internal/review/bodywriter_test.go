package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBodyExecCommand mirrors fakeExecCommand (decisionwriter_test.go) but
// targets bodyExecCommandFunc's distinct seam, so BodyWriter tests can fake
// their own invocations independently of DecisionWriter's.
func fakeBodyExecCommand(callCount *int, capturedArgv *[]string, exitCode int, stderrText string) func(name string, arg ...string) *exec.Cmd {
	return func(name string, arg ...string) *exec.Cmd {
		*callCount++
		full := append([]string{name}, arg...)
		*capturedArgv = full

		shArgs := []string{"-c"}
		script := ""
		if stderrText != "" {
			script += "echo " + shellQuote(stderrText) + " 1>&2; "
		}
		script += "exit " + itoa(exitCode)
		shArgs = append(shArgs, script)
		return exec.Command("/bin/sh", shArgs...)
	}
}

func withFakeBodyExecAndScriptPath(t *testing.T, exitCode int, stderrText string) (*int, *[]string) {
	t.Helper()
	origExec := bodyExecCommandFunc
	origScriptPath := bodyScriptPathFunc
	origStat := statFunc
	callCount := 0
	var capturedArgv []string
	bodyExecCommandFunc = fakeBodyExecCommand(&callCount, &capturedArgv, exitCode, stderrText)
	bodyScriptPathFunc = func() string { return "/fake/set-review-body.sh" }
	// The script pre-check statFunc must succeed so tests exercise the exec
	// path; the spool file itself is resolved by resolveSpoolPath's real
	// os.Stat, so exec-path tests pass a real temp spool via
	// newPendingSpoolForBody.
	statFunc = func(string) (os.FileInfo, error) { return nil, nil }
	t.Cleanup(func() {
		bodyExecCommandFunc = origExec
		bodyScriptPathFunc = origScriptPath
		statFunc = origStat
	})
	return &callCount, &capturedArgv
}

// newPendingSpoolForBody creates a real spool file under a temp pending/
// dir so resolveSpoolPath finds it, and returns its absolute path.
func newPendingSpoolForBody(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	path := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(path, []byte("---\ndecision:\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write spool: %v", err)
	}
	return path
}

// newBodyFile creates a temp file with the given content and returns its
// absolute path, standing in for the temp file an $EDITOR session would
// have produced.
func newBodyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	return path
}

func TestBodyWriter_EmptySpoolPath_ExecNeverCalled(t *testing.T) {
	callCount, _ := withFakeBodyExecAndScriptPath(t, 0, "")

	bw := NewBodyWriter()
	err := bw.SetBody("", "/tmp/body.md")

	if err == nil {
		t.Fatal("expected error for empty spool path, got nil")
	}
	if !strings.Contains(err.Error(), "no spool path provided") {
		t.Errorf("expected error to mention 'no spool path provided', got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected bodyExecCommandFunc to never be called, got %d calls", *callCount)
	}
}

func TestBodyWriter_EmptyBodyFilePath_ExecNeverCalled(t *testing.T) {
	callCount, _ := withFakeBodyExecAndScriptPath(t, 0, "")
	spool := newPendingSpoolForBody(t)

	bw := NewBodyWriter()
	err := bw.SetBody(spool, "")

	if err == nil {
		t.Fatal("expected error for empty body file path, got nil")
	}
	if !strings.Contains(err.Error(), "no body file path provided") {
		t.Errorf("expected error to mention 'no body file path provided', got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected bodyExecCommandFunc to never be called, got %d calls", *callCount)
	}
}

func TestBodyWriter_ExecSuccess_ReturnsNilError(t *testing.T) {
	callCount, capturedArgv := withFakeBodyExecAndScriptPath(t, 0, "")
	spool := newPendingSpoolForBody(t)
	bodyFile := newBodyFile(t, "new body content\n")

	bw := NewBodyWriter()
	err := bw.SetBody(spool, bodyFile)

	if err != nil {
		t.Fatalf("expected nil error on exit 0, got: %v", err)
	}
	if *callCount != 1 {
		t.Errorf("expected bodyExecCommandFunc to be called exactly once, got %d", *callCount)
	}
	want := []string{"bash", "/fake/set-review-body.sh", spool, bodyFile}
	if len(*capturedArgv) != len(want) {
		t.Fatalf("argv = %v, want %v", *capturedArgv, want)
	}
	for i := range want {
		if (*capturedArgv)[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, (*capturedArgv)[i], want[i])
		}
	}
}

func TestBodyWriter_ExecFailure_ErrorContainsStderr(t *testing.T) {
	_, _ = withFakeBodyExecAndScriptPath(t, 6, "error: body file is empty")
	spool := newPendingSpoolForBody(t)
	bodyFile := newBodyFile(t, "irrelevant, exec is faked\n")

	bw := NewBodyWriter()
	err := bw.SetBody(spool, bodyFile)

	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "body file is empty") {
		t.Errorf("expected error to contain captured stderr text, got: %v", err)
	}
	if !strings.Contains(err.Error(), "set-review-body failed") {
		t.Errorf("expected error to be wrapped with 'set-review-body failed', got: %v", err)
	}
}

// TestBodyWriter_AlreadyFinalized distinguishes a drained (done/) review
// from a never-reviewed one, mirroring
// TestDecisionWriter_AlreadyFinalized.
func TestBodyWriter_AlreadyFinalized(t *testing.T) {
	callCount, _ := withFakeBodyExecAndScriptPath(t, 0, "")

	base := t.TempDir()
	doneDir := filepath.Join(base, "done")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := "pr-review-owner-repo-1.md"
	if err := os.WriteFile(filepath.Join(doneDir, filename), []byte("---\ndecision: post\n---\nold body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(base, "pending", filename)
	bodyFile := newBodyFile(t, "new body\n")

	bw := NewBodyWriter()
	err := bw.SetBody(pendingPath, bodyFile)

	if err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("expected 'already finalized' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec for an already-finalized review, got %d calls", *callCount)
	}
}

// TestBodyWriter_NoSpoolFile reports the never-reviewed case distinctly,
// mirroring TestDecisionWriter_NoSpoolFile.
func TestBodyWriter_NoSpoolFile(t *testing.T) {
	callCount, _ := withFakeBodyExecAndScriptPath(t, 0, "")
	missing := filepath.Join(t.TempDir(), "pending", "pr-review-x-1.md")
	bodyFile := newBodyFile(t, "new body\n")

	bw := NewBodyWriter()
	err := bw.SetBody(missing, bodyFile)

	if err == nil || !strings.Contains(err.Error(), "no spool file") {
		t.Errorf("expected 'no spool file' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when there is no spool file, got %d calls", *callCount)
	}
}

// TestBodyWriter_ScriptMissing surfaces an explicit path error instead of a
// cryptic blank one when the body script cannot be found, mirroring
// TestDecisionWriter_ScriptMissing.
func TestBodyWriter_ScriptMissing(t *testing.T) {
	callCount, _ := withFakeBodyExecAndScriptPath(t, 0, "")
	spool := newPendingSpoolForBody(t)
	bodyFile := newBodyFile(t, "new body\n")

	statFunc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	bw := NewBodyWriter()
	err := bw.SetBody(spool, bodyFile)

	if err == nil || !strings.Contains(err.Error(), "body script not found") {
		t.Errorf("expected 'body script not found' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when the script is missing, got %d calls", *callCount)
	}
}
