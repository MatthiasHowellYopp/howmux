package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_IgnoresLegacySandboxBlock verifies that a config file still
// carrying the "sandbox:" block from the removed eval sandbox mode loads
// without error. The values are deliberately ones the old validation rejected
// (cpu_cores 0, memory_mb below 256, empty workspace_dir), proving the block
// is ignored rather than validated.
func TestLoad_IgnoresLegacySandboxBlock(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".howmux")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	content := `repo: test/repo
label: howmux
sandbox:
  workspace_dir: ""
  cpu_cores: 0
  memory_mb: 10
  timeout: 0s
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restoring working directory: %v", err)
		}
	})
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("changing directory: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with legacy sandbox block: got error %v, want nil", err)
	}
	if cfg.Repo != "test/repo" {
		t.Errorf("Repo = %q, want %q", cfg.Repo, "test/repo")
	}
	if cfg.Label != "howmux" {
		t.Errorf("Label = %q, want %q", cfg.Label, "howmux")
	}
}
