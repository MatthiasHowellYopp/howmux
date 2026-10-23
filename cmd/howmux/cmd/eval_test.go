package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// executeEval runs the root command with the given eval args, capturing
// output, and resets the eval flag globals afterwards so tests don't leak
// state into each other (pflag keeps parsed values on the package vars).
func executeEval(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetEvalFlags := func() {
		evalList, evalResume, evalCase, evalPerf = false, false, "", false
		evalSandbox, evalNoSandbox, evalDebug, evalCleanup = false, false, false, false
		evalResourceLimit = nil
		for _, name := range []string{"list", "resume", "case", "perf", "sandbox", "no-sandbox", "resource-limit", "debug", "cleanup"} {
			if f := evalCmd.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
	}
	resetEvalFlags()
	t.Cleanup(func() {
		resetEvalFlags()
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"eval"}, args...))
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestEvalSandboxFlagReturnsRemovedError(t *testing.T) {
	_, err := executeEval(t, "--sandbox", "architect")
	if !errors.Is(err, errSandboxRemoved) {
		t.Fatalf("got error %v, want %v", err, errSandboxRemoved)
	}
	want := "the eval sandbox mode has been removed (the container could not authenticate kiro-cli); run evals without --sandbox"
	if err.Error() != want {
		t.Errorf("got message %q, want %q", err.Error(), want)
	}
}

func TestEvalSandboxFlagsAreHidden(t *testing.T) {
	sandbox := evalCmd.Flags().Lookup("sandbox")
	if sandbox == nil {
		t.Fatal("--sandbox flag not registered")
	}
	if !sandbox.Hidden {
		t.Error("--sandbox should be hidden")
	}
	if sandbox.Deprecated != "" {
		t.Errorf("--sandbox should error, not be deprecated; got Deprecated=%q", sandbox.Deprecated)
	}

	for _, name := range []string{"no-sandbox", "resource-limit", "debug", "cleanup"} {
		f := evalCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s flag not registered", name)
			continue
		}
		if !f.Hidden {
			t.Errorf("--%s should be hidden", name)
		}
		if !strings.Contains(f.Deprecated, "removed eval sandbox mode") {
			t.Errorf("--%s Deprecated = %q, want mention of the removed sandbox mode", name, f.Deprecated)
		}
	}

	if f := evalCmd.Flags().ShorthandLookup("d"); f == nil || f.Name != "debug" {
		t.Error("-d shorthand should still map to --debug")
	}
}

// TestEvalDeprecatedFlagsAreNoOps passes every deprecated flag alongside
// --list with no agent. If any flag still short-circuited (e.g. --cleanup
// running container cleanup), the list path's own validation error would not
// be reached.
func TestEvalDeprecatedFlagsAreNoOps(t *testing.T) {
	out, err := executeEval(t, "--list", "--no-sandbox", "--debug", "--cleanup", "--resource-limit", "cpu=2")
	if err == nil {
		t.Fatal("got nil error, want the --list agent-required error")
	}
	if !strings.Contains(err.Error(), "agent name required for --list") {
		t.Errorf("got error %q, want it to come from the --list path", err.Error())
	}
	for _, name := range []string{"no-sandbox", "debug", "cleanup", "resource-limit"} {
		if !strings.Contains(out, "--"+name+" has been deprecated") {
			t.Errorf("output missing deprecation notice for --%s; got:\n%s", name, out)
		}
	}
}
