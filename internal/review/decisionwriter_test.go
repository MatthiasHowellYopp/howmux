package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExecCommand builds a *exec.Cmd whose Run() behavior is controlled by
// exitCode and stderrText, without invoking the real script. It shells out to
// /bin/sh -c so cmd.Stderr assignment and non-zero exit codes behave exactly
// like a real subprocess, while incrementing callCount and recording the
// argv DecisionWriter actually passed (bash, scriptPath, spoolPath,
// decision) for assertions.
func fakeExecCommand(callCount *int, capturedArgv *[]string, exitCode int, stderrText string) func(name string, arg ...string) *exec.Cmd {
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

// shellQuote wraps s in single quotes for safe interpolation into a
// generated /bin/sh -c script within this test file only.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func withFakeExecAndScriptPath(t *testing.T, exitCode int, stderrText string) (*int, *[]string) {
	t.Helper()
	origExec := execCommandFunc
	origScriptPath := scriptPathFunc
	origStat := statFunc
	callCount := 0
	var capturedArgv []string
	execCommandFunc = fakeExecCommand(&callCount, &capturedArgv, exitCode, stderrText)
	scriptPathFunc = func() string { return "/fake/set-review-decision.sh" }
	// The script pre-check statFunc must succeed so tests exercise the exec
	// path; the spool file itself is resolved by resolveSpoolPath's real
	// os.Stat, so exec-path tests pass a real temp spool via newPendingSpool.
	statFunc = func(string) (os.FileInfo, error) { return nil, nil }
	t.Cleanup(func() {
		execCommandFunc = origExec
		scriptPathFunc = origScriptPath
		statFunc = origStat
	})
	return &callCount, &capturedArgv
}

// newPendingSpool creates a real spool file under a temp pending/ dir so
// resolveSpoolPath finds it, and returns its absolute path.
func newPendingSpool(t *testing.T) string {
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

func TestDecisionWriter_InvalidDecision_ExecNeverCalled(t *testing.T) {
	callCount, _ := withFakeExecAndScriptPath(t, 0, "")

	dw := NewDecisionWriter()
	err := dw.SetDecision("/tmp/spool.md", "bogus")

	if err == nil {
		t.Fatal("expected error for invalid decision, got nil")
	}
	if !strings.Contains(err.Error(), "invalid decision") {
		t.Errorf("expected error to mention 'invalid decision', got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected execCommandFunc to never be called, got %d calls", *callCount)
	}
}

func TestDecisionWriter_EmptySpoolPath_ExecNeverCalled(t *testing.T) {
	callCount, _ := withFakeExecAndScriptPath(t, 0, "")

	dw := NewDecisionWriter()
	err := dw.SetDecision("", "post")

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

func TestDecisionWriter_ExecSuccess_ReturnsNilError(t *testing.T) {
	callCount, capturedArgv := withFakeExecAndScriptPath(t, 0, "")
	spool := newPendingSpool(t)

	dw := NewDecisionWriter()
	err := dw.SetDecision(spool, "post")

	if err != nil {
		t.Fatalf("expected nil error on exit 0, got: %v", err)
	}
	if *callCount != 1 {
		t.Errorf("expected execCommandFunc to be called exactly once, got %d", *callCount)
	}
	want := []string{"bash", "/fake/set-review-decision.sh", spool, "post"}
	if len(*capturedArgv) != len(want) {
		t.Fatalf("argv = %v, want %v", *capturedArgv, want)
	}
	for i := range want {
		if (*capturedArgv)[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, (*capturedArgv)[i], want[i])
		}
	}
}

func TestDecisionWriter_ExecFailure_ErrorContainsStderr(t *testing.T) {
	_, _ = withFakeExecAndScriptPath(t, 1, "error: no 'decision:' field found in front-matter")
	spool := newPendingSpool(t)

	dw := NewDecisionWriter()
	err := dw.SetDecision(spool, "discard")

	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "no 'decision:' field found") {
		t.Errorf("expected error to contain captured stderr text, got: %v", err)
	}
	if !strings.Contains(err.Error(), "set-review-decision failed") {
		t.Errorf("expected error to be wrapped with 'set-review-decision failed', got: %v", err)
	}
}

func TestDecisionWriter_AllFourValidDecisions_PassValidation(t *testing.T) {
	decisions := []string{"post", "revise", "rereview", "discard"}

	for _, d := range decisions {
		t.Run(d, func(t *testing.T) {
			callCount, _ := withFakeExecAndScriptPath(t, 0, "")
			spool := newPendingSpool(t)

			dw := NewDecisionWriter()
			err := dw.SetDecision(spool, d)

			if err != nil {
				t.Errorf("decision %q: expected nil error, got: %v", d, err)
			}
			if *callCount != 1 {
				t.Errorf("decision %q: expected exec to be called once, got %d", d, *callCount)
			}
		})
	}
}

// TestDecisionWriter_AlreadyFinalized distinguishes a drained (done/) review
// from a never-reviewed one.
func TestDecisionWriter_AlreadyFinalized(t *testing.T) {
	callCount, _ := withFakeExecAndScriptPath(t, 0, "")

	// Create the file only in done/, with the pending/ path passed in.
	base := t.TempDir()
	doneDir := filepath.Join(base, "done")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := "pr-review-owner-repo-1.md"
	if err := os.WriteFile(filepath.Join(doneDir, filename), []byte("---\ndecision: post\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(base, "pending", filename)

	dw := NewDecisionWriter()
	err := dw.SetDecision(pendingPath, "revise")

	if err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("expected 'already finalized' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec for an already-finalized review, got %d calls", *callCount)
	}
}

// TestDecisionWriter_NoSpoolFile reports the never-reviewed case distinctly.
func TestDecisionWriter_NoSpoolFile(t *testing.T) {
	callCount, _ := withFakeExecAndScriptPath(t, 0, "")
	missing := filepath.Join(t.TempDir(), "pending", "pr-review-x-1.md")

	dw := NewDecisionWriter()
	err := dw.SetDecision(missing, "post")

	if err == nil || !strings.Contains(err.Error(), "no spool file") {
		t.Errorf("expected 'no spool file' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when there is no spool file, got %d calls", *callCount)
	}
}

// TestDecisionWriter_ScriptMissing surfaces an explicit path error instead of
// a cryptic blank one when the decision script cannot be found.
func TestDecisionWriter_ScriptMissing(t *testing.T) {
	callCount, _ := withFakeExecAndScriptPath(t, 0, "")
	spool := newPendingSpool(t)

	// Override statFunc to fail specifically for the script path.
	statFunc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	dw := NewDecisionWriter()
	err := dw.SetDecision(spool, "post")

	if err == nil || !strings.Contains(err.Error(), "decision script not found") {
		t.Errorf("expected 'decision script not found' error, got: %v", err)
	}
	if *callCount != 0 {
		t.Errorf("expected no exec when the script is missing, got %d calls", *callCount)
	}
}
