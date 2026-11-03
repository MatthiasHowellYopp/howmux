package acp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAgent creates <dir>/<name>.json so ValidateAgentResolvable can find it.
func writeAgent(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubGlobalAgentsDir points the global agent directory at dir for one test.
func stubGlobalAgentsDir(t *testing.T, dir string, err error) {
	t.Helper()
	orig := globalAgentsDirFunc
	globalAgentsDirFunc = func() (string, error) { return dir, err }
	t.Cleanup(func() { globalAgentsDirFunc = orig })
}

func TestValidateAgentResolvable(t *testing.T) {
	cwd := t.TempDir()
	global := t.TempDir()
	writeAgent(t, filepath.Join(cwd, ".kiro", "agents"), "local-only")
	writeAgent(t, global, "global-only")
	writeAgent(t, filepath.Join(cwd, ".kiro", "agents"), "both")
	writeAgent(t, global, "both")
	stubGlobalAgentsDir(t, global, nil)

	tests := []struct {
		name    string
		agent   string
		wantErr bool
	}{
		{"workspace agent", "local-only", false},
		{"global agent", "global-only", false},
		{"agent in both locations", "both", false},
		{"unknown agent (would silently fall back)", "nonexistent", true},
		{"empty agent", "", true},
		{"parent traversal", "../local-only", true},
		{"nested path", "sub/both", true},
		{"backslash path", `sub\both`, true},
		{"dot", ".", true},
		{"dot-dot", "..", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgentResolvable(cwd, tt.agent)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAgentResolvable(%q) err=%v, wantErr=%v", tt.agent, err, tt.wantErr)
			}
		})
	}
}

// The error for an unknown agent names both locations checked, so the user
// can see where to put the config.
func TestValidateAgentResolvable_ErrorListsBothLocations(t *testing.T) {
	cwd := t.TempDir()
	global := t.TempDir()
	stubGlobalAgentsDir(t, global, nil)

	err := ValidateAgentResolvable(cwd, "missing")
	if err == nil {
		t.Fatal("expected an error for a missing agent")
	}
	for _, want := range []string{
		filepath.Join(cwd, ".kiro", "agents", "missing.json"),
		filepath.Join(global, "missing.json"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// If the home directory can't be resolved, the check still works against the
// workspace alone rather than failing outright.
func TestValidateAgentResolvable_NoHomeDirFallsBackToWorkspace(t *testing.T) {
	cwd := t.TempDir()
	writeAgent(t, filepath.Join(cwd, ".kiro", "agents"), "local-only")
	stubGlobalAgentsDir(t, "", errors.New("no home"))

	if err := ValidateAgentResolvable(cwd, "local-only"); err != nil {
		t.Errorf("workspace agent should resolve without a home dir: %v", err)
	}
	if err := ValidateAgentResolvable(cwd, "nonexistent"); err == nil {
		t.Error("unknown agent should still fail without a home dir")
	}
}

// A traversal name must not resolve even when it points at a real .json file
// outside both agent directories.
func TestValidateAgentResolvable_TraversalDoesNotReachOtherFiles(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "repo")
	writeAgent(t, root, "outside") // root/outside.json
	stubGlobalAgentsDir(t, filepath.Join(root, "global"), nil)

	// From <cwd>/.kiro/agents, ../../../outside lands on root/outside.json.
	if err := ValidateAgentResolvable(cwd, "../../../outside"); err == nil {
		t.Error("traversal name resolved to a file outside the agent dirs")
	}
}

// An unreadable workspace agents dir must not hide an agent that exists in the
// global dir; and when nothing resolves, the stat error is the one reported.
func TestValidateAgentResolvable_WorkspaceStatErrorFallsThroughToGlobal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	cwd := t.TempDir()
	global := t.TempDir()
	wsAgents := filepath.Join(cwd, ".kiro", "agents")
	writeAgent(t, wsAgents, "blocked")
	writeAgent(t, global, "global-only")
	if err := os.Chmod(wsAgents, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(wsAgents, 0o755) })
	stubGlobalAgentsDir(t, global, nil)

	if err := ValidateAgentResolvable(cwd, "global-only"); err != nil {
		t.Errorf("global agent should resolve despite an unreadable workspace dir: %v", err)
	}

	err := ValidateAgentResolvable(cwd, "blocked")
	if err == nil {
		t.Fatal("agent only in the unreadable dir should not resolve")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("want the permission error wrapped, got %v", err)
	}
}

// Exercises the real globalAgentsDirFunc (no stub) so the ~/.kiro/agents
// subpath kiro-cli uses is actually pinned by a test.
func TestValidateAgentResolvable_RealHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	writeAgent(t, filepath.Join(home, ".kiro", "agents"), "from-home")

	if err := ValidateAgentResolvable(t.TempDir(), "from-home"); err != nil {
		t.Errorf("agent in $HOME/.kiro/agents should resolve: %v", err)
	}
}
