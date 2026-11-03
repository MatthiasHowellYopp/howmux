package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeAgentFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolatedHome points ~/.kiro/agents at a temp dir (os.UserHomeDir reads
// $HOME, or %USERPROFILE% on Windows) and returns that agents dir.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return filepath.Join(home, ".kiro", "agents")
}

func TestCheckPipelineAgents_MissingAndResolved(t *testing.T) {
	global := isolatedHome(t)
	cwd := t.TempDir()

	st := checkPipelineAgents(cwd)
	if !reflect.DeepEqual(st.missing, pipelineAgents) {
		t.Fatalf("with no agents anywhere, want all missing %v, got %v", pipelineAgents, st.missing)
	}
	if len(st.failed) != 0 || len(st.overrides) != 0 {
		t.Fatalf("want no failures or overrides, got %+v", st)
	}

	for _, name := range pipelineAgents {
		writeAgentFile(t, global, name)
	}
	st = checkPipelineAgents(cwd)
	if len(st.missing) != 0 || len(st.failed) != 0 || len(st.overrides) != 0 {
		t.Fatalf("all-global agents: want a clean status, got %+v", st)
	}
}

func TestCheckPipelineAgents_WorkspaceOverride(t *testing.T) {
	global := isolatedHome(t)
	cwd := t.TempDir()
	for _, name := range pipelineAgents {
		writeAgentFile(t, global, name)
	}
	local := filepath.Join(cwd, ".kiro", "agents")
	writeAgentFile(t, local, "planner") // shadows the shared planner
	writeAgentFile(t, local, "builder")

	st := checkPipelineAgents(cwd)
	want := map[string]string{
		"planner": filepath.Join(local, "planner.json"),
		"builder": filepath.Join(local, "builder.json"),
	}
	if !reflect.DeepEqual(st.overrides, want) {
		t.Fatalf("overrides: want %v, got %v", want, st.overrides)
	}

	var buf bytes.Buffer
	reportAgentSetup(&buf, cwd)
	out := buf.String()
	for _, s := range []string{"override the shared ones", "planner (" + want["planner"] + ")", "builder ("} {
		if !strings.Contains(out, s) {
			t.Errorf("report %q missing %q", out, s)
		}
	}
	if strings.Contains(out, "not found") {
		t.Errorf("no agent is missing, but report says so: %q", out)
	}
}

func TestCheckPipelineAgents_StatErrorIsNotReportedAsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	isolatedHome(t) // empty global dir
	cwd := t.TempDir()
	local := filepath.Join(cwd, ".kiro", "agents")
	for _, name := range pipelineAgents {
		writeAgentFile(t, local, name)
	}
	if err := os.Chmod(local, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(local, 0o755) })

	st := checkPipelineAgents(cwd)
	if len(st.missing) != 0 {
		t.Errorf("unreadable agents must not be reported as missing, got %v", st.missing)
	}
	if len(st.failed) != len(pipelineAgents) {
		t.Fatalf("want every agent in failed, got %v", st.failed)
	}
	if !errors.Is(st.failed["planner"], os.ErrPermission) {
		t.Errorf("want the permission error kept, got %v", st.failed["planner"])
	}

	var buf bytes.Buffer
	reportAgentSetup(&buf, cwd)
	out := buf.String()
	if !strings.Contains(out, "Couldn't check") || !strings.Contains(out, "permission denied") {
		t.Errorf("report should show the stat error, got %q", out)
	}
	if strings.Contains(out, "Install them from ai-resources") {
		t.Errorf("report must not tell the user to reinstall agents that exist: %q", out)
	}
}

func TestReportAgentSetup_MissingAndClean(t *testing.T) {
	global := isolatedHome(t)
	cwd := t.TempDir()

	var buf bytes.Buffer
	reportAgentSetup(&buf, cwd)
	out := buf.String()
	for _, want := range []string{"howmux agents not found", "planner", "documenter", "ai-resources"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning %q missing %q", out, want)
		}
	}

	for _, name := range pipelineAgents {
		writeAgentFile(t, global, name)
	}
	buf.Reset()
	reportAgentSetup(&buf, cwd)
	if buf.Len() != 0 {
		t.Errorf("want no output when all agents resolve globally, got %q", buf.String())
	}
}
