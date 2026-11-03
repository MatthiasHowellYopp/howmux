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
