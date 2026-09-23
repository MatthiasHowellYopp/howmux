package notify

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// fakeExecCommand builds an *exec.Cmd that runs this test binary in a
// special "helper process" mode (the standard Go pattern for faking
// exec.Command without shelling out to a real external program, as used by
// the Go standard library's own os/exec tests). It also records the
// name/args it was invoked with into calls, and lets the caller force the
// process to exit non-zero via the GO_HELPER_FAIL env var.
func fakeExecCommand(calls *[]string, fail bool) func(name string, arg ...string) *exec.Cmd {
	return func(name string, arg ...string) *exec.Cmd {
		*calls = append(*calls, name+" "+strings.Join(arg, " "))

		cs := []string{"-test.run=TestHelperProcess", "--"}
		cs = append(cs, arg...)
		cmd := exec.Command(os.Args[0], cs...)
		env := []string{"GO_WANT_HELPER_PROCESS=1"}
		if fail {
			env = append(env, "GO_HELPER_FAIL=1")
		}
		cmd.Env = env
		return cmd
	}
}

// TestHelperProcess is not a real test. It is invoked as a subprocess by
// fakeExecCommand to stand in for the real osascript binary.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)
	if os.Getenv("GO_HELPER_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

// resetSeams restores the injectable package vars to their production
// defaults after a test that overrides them.
func resetSeams() {
	execCommand = exec.Command
	goos = runtime.GOOS
}

func TestNotify_ConstructsEscapedScript(t *testing.T) {
	var calls []string
	execCommand = fakeExecCommand(&calls, false)
	goos = "darwin"
	defer resetSeams()

	title := `ti"tle\here`
	message := `mes"sage\there`

	if err := Notify(title, message); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 exec call, got %d: %v", len(calls), calls)
	}

	got := calls[0]
	wantMessage := `mes\"sage\\there`
	wantTitle := `ti\"tle\\here`

	if !strings.Contains(got, wantMessage) {
		t.Errorf("script does not contain expected escaped message %q\nscript args: %s", wantMessage, got)
	}
	if !strings.Contains(got, wantTitle) {
		t.Errorf("script does not contain expected escaped title %q\nscript args: %s", wantTitle, got)
	}
	if !strings.Contains(got, "display notification") {
		t.Errorf("script does not contain 'display notification': %s", got)
	}
	if !strings.HasPrefix(got, "osascript -e ") {
		t.Errorf("expected command to start with 'osascript -e ', got: %s", got)
	}
}

func TestNotify_NonDarwinIsNoOp(t *testing.T) {
	var calls []string
	execCommand = fakeExecCommand(&calls, false)
	goos = "linux"
	defer resetSeams()

	if err := Notify("title", "message"); err != nil {
		t.Fatalf("expected nil error on non-darwin no-op, got: %v", err)
	}

	if len(calls) != 0 {
		t.Fatalf("expected exec seam NOT to be called on non-darwin, but it was called %d time(s): %v", len(calls), calls)
	}
}

func TestNotify_ExecErrorPropagates(t *testing.T) {
	var calls []string
	execCommand = fakeExecCommand(&calls, true)
	goos = "darwin"
	defer resetSeams()

	err := Notify("title", "message")
	if err == nil {
		t.Fatal("expected a non-nil error when exec fails, got nil")
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 exec call, got %d: %v", len(calls), calls)
	}
}

func TestNotify_SuccessReturnsNil(t *testing.T) {
	var calls []string
	execCommand = fakeExecCommand(&calls, false)
	goos = "darwin"
	defer resetSeams()

	if err := Notify("hello", "world"); err != nil {
		t.Fatalf("expected nil error on success, got: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 exec call, got %d: %v", len(calls), calls)
	}
}
