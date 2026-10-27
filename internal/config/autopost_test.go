package config

import (
	"os"
	"testing"
)

// loadConfigFrom writes configContent to a temp .howmux/config.yaml, chdirs
// there, and calls Load(). Mirrors the harness in base_branch_test.go.
func loadConfigFrom(t *testing.T, configContent string) (*Config, error) {
	t.Helper()
	tmpDir := t.TempDir()
	configDir := tmpDir + string(os.PathSeparator) + ".howmux"
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configFile := configDir + string(os.PathSeparator) + "config.yaml"
	if err := os.WriteFile(configFile, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore wd: %v", err)
		}
	})
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return Load()
}

func TestLoad_AutoPostDefault(t *testing.T) {
	cfg, err := loadConfigFrom(t, "repo: test/repo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Review.AutoPost.Mode != "dry-run" {
		t.Errorf("default Mode = %q, want dry-run", cfg.Review.AutoPost.Mode)
	}
	if cfg.Review.AutoPost.MaxFindings != 25 {
		t.Errorf("default MaxFindings = %d, want 25", cfg.Review.AutoPost.MaxFindings)
	}
}

func TestLoad_AutoPostExplicit(t *testing.T) {
	content := `repo: test/repo
review:
  auto_post:
    mode: own-repos
    max_findings: 10
    own_repo_owners:
      - matthiashowellyopp
      - Bit-Quill`
	cfg, err := loadConfigFrom(t, content)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ap := cfg.Review.AutoPost
	if ap.Mode != "own-repos" {
		t.Errorf("Mode = %q, want own-repos", ap.Mode)
	}
	if ap.MaxFindings != 10 {
		t.Errorf("MaxFindings = %d, want 10", ap.MaxFindings)
	}
	if len(ap.OwnRepoOwners) != 2 || ap.OwnRepoOwners[1] != "Bit-Quill" {
		t.Errorf("OwnRepoOwners = %v, want [matthiashowellyopp Bit-Quill]", ap.OwnRepoOwners)
	}
}

func TestLoad_AutoPostInvalidModeRejected(t *testing.T) {
	content := `repo: test/repo
review:
  auto_post:
    mode: bogus`
	_, err := loadConfigFrom(t, content)
	if err == nil {
		t.Fatal("expected error for invalid auto_post.mode, got nil")
	}
}

func TestLoad_AutoPostNegativeMaxFindingsRejected(t *testing.T) {
	content := `repo: test/repo
review:
  auto_post:
    max_findings: -1`
	_, err := loadConfigFrom(t, content)
	if err == nil {
		t.Fatal("expected error for negative max_findings, got nil")
	}
}

func TestLoad_AutoPostAuditorModel(t *testing.T) {
	cfg, err := loadConfigFrom(t, "repo: test/repo\nreview:\n  auto_post:\n    auditor_model: \"  claude-sonnet-5.5 \"")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Review.AutoPost.AuditorModel; got != "claude-sonnet-5.5" {
		t.Errorf("AuditorModel = %q, want trimmed claude-sonnet-5.5", got)
	}

	cfg, err = loadConfigFrom(t, "repo: test/repo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Review.AutoPost.AuditorModel; got != "" {
		t.Errorf("default AuditorModel = %q, want empty (kiro-cli default)", got)
	}
}

func TestLoad_AutoPostAuditorModelShapeRejected(t *testing.T) {
	for _, v := range []string{
		`claude sonnet`,    // space
		`"claude\tsonnet"`, // tab
		`"claude\nsonnet"`, // newline
		`"claude\u00a0x"`,  // non-breaking space
		`--help`,           // looks like a flag
		`"-model"`,         // leading dash
		`"claude;rm"`,      // not a model-id character
	} {
		t.Run(v, func(t *testing.T) {
			_, err := loadConfigFrom(t, "repo: test/repo\nreview:\n  auto_post:\n    auditor_model: "+v)
			if err == nil {
				t.Fatalf("expected error for auditor_model %s, got nil", v)
			}
		})
	}
}

func TestLoad_AutoPostAuditorModelShapeAccepted(t *testing.T) {
	for _, v := range []string{"claude-sonnet-5.5", "gpt-5.6-luna", "auto", "qwen3-coder-next"} {
		t.Run(v, func(t *testing.T) {
			cfg, err := loadConfigFrom(t, "repo: test/repo\nreview:\n  auto_post:\n    auditor_model: "+v)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Review.AutoPost.AuditorModel != v {
				t.Errorf("AuditorModel = %q, want %q", cfg.Review.AutoPost.AuditorModel, v)
			}
		})
	}
}
