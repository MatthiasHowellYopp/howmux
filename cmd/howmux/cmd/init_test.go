package cmd

import (
	"bytes"
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

func TestMissingPipelineAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // os.UserHomeDir reads $HOME, so this isolates ~/.kiro/agents
	cwd := t.TempDir()

	if got := missingPipelineAgents(cwd); !reflect.DeepEqual(got, pipelineAgents) {
		t.Fatalf("with no agents anywhere, want all missing %v, got %v", pipelineAgents, got)
	}

	// Global agents (the ai-resources copy) and repo-local agents both count.
	for _, name := range []string{"planner", "krew-lead", "architect", "builder", "validator"} {
		writeAgentFile(t, filepath.Join(home, ".kiro", "agents"), name)
	}
	writeAgentFile(t, filepath.Join(cwd, ".kiro", "agents"), "documenter")

	if got := missingPipelineAgents(cwd); len(got) != 0 {
		t.Fatalf("want none missing, got %v", got)
	}
}

func TestWarnMissingAgents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()

	var buf bytes.Buffer
	warnMissingAgents(&buf, cwd)
	out := buf.String()
	for _, want := range []string{"howmux agents not found", "planner", "documenter", "ai-resources"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning %q missing %q", out, want)
		}
	}

	for _, name := range pipelineAgents {
		writeAgentFile(t, filepath.Join(cwd, ".kiro", "agents"), name)
	}
	buf.Reset()
	warnMissingAgents(&buf, cwd)
	if buf.Len() != 0 {
		t.Errorf("want no output when all agents resolve, got %q", buf.String())
	}
}
