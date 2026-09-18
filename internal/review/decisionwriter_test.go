package review

import (
	"os/exec"
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
	callCount := 0
	var capturedArgv []string
	execCommandFunc = fakeExecCommand(&callCount, &capturedArgv, exitCode, stderrText)
	scriptPathFunc = func() string { return "/fake/set-review-decision.sh" }
	t.Cleanup(func() {
		execCommandFunc = origExec
		scriptPathFunc = origScriptPath
	})
	return &callCount, &capturedArgv
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

	dw := NewDecisionWriter()
	err := dw.SetDecision("/tmp/spool.md", "post")

	if err != nil {
		t.Fatalf("expected nil error on exit 0, got: %v", err)
	}
	if *callCount != 1 {
		t.Errorf("expected execCommandFunc to be called exactly once, got %d", *callCount)
	}
	want := []string{"bash", "/fake/set-review-decision.sh", "/tmp/spool.md", "post"}
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
	_, _ = withFakeExecAndScriptPath(t, 1, "error: spool file not found: /tmp/spool.md")

	dw := NewDecisionWriter()
	err := dw.SetDecision("/tmp/spool.md", "discard")

	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "spool file not found") {
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

			dw := NewDecisionWriter()
			err := dw.SetDecision("/tmp/spool.md", d)

			if err != nil {
				t.Errorf("decision %q: expected nil error, got: %v", d, err)
			}
			if *callCount != 1 {
				t.Errorf("decision %q: expected exec to be called once, got %d", d, *callCount)
			}
		})
	}
}
