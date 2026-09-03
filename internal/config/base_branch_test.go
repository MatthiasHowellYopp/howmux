package config

import (
	"os"
	"testing"
)

func TestLoad_BaseBranchDefault(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
	if err := os.Mkdir(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Config without base_branch field should default to "main"
	configContent := `repo: test/repo`
	configFile := configDir + string(os.PathSeparator) + "config.yaml"
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	})
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, expected %q", cfg.BaseBranch, "main")
	}
}

func TestLoad_BaseBranchExplicit(t *testing.T) {
	tests := []struct {
		name               string
		configContent      string
		expectedBaseBranch string
	}{
		{
			name: "explicit main",
			configContent: `repo: test/repo
base_branch: main`,
			expectedBaseBranch: "main",
		},
		{
			name: "explicit dev",
			configContent: `repo: test/repo
base_branch: dev`,
			expectedBaseBranch: "dev",
		},
		{
			name: "explicit develop",
			configContent: `repo: test/repo
base_branch: develop`,
			expectedBaseBranch: "develop",
		},
		{
			name: "explicit master",
			configContent: `repo: test/repo
base_branch: master`,
			expectedBaseBranch: "master",
		},
		{
			name: "explicit feature branch",
			configContent: `repo: test/repo
base_branch: feature/new-ui`,
			expectedBaseBranch: "feature/new-ui",
		},
		{
			name: "explicit release branch",
			configContent: `repo: test/repo
base_branch: release/v1.0`,
			expectedBaseBranch: "release/v1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
			if err := os.Mkdir(configDir, 0755); err != nil {
				t.Fatalf("Failed to create config dir: %v", err)
			}

			configFile := configDir + string(os.PathSeparator) + "config.yaml"
			if err := os.WriteFile(configFile, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldDir); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			})
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if cfg.BaseBranch != tt.expectedBaseBranch {
				t.Errorf("BaseBranch = %q, expected %q", cfg.BaseBranch, tt.expectedBaseBranch)
			}
		})
	}
}

func TestLoad_BaseBranchBackwardCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		description   string
	}{
		{
			name: "minimal config",
			configContent: `repo: test/repo
label: kiro-krew`,
			description: "Config without base_branch should work",
		},
		{
			name: "config with github and jira",
			configContent: `repo: test/repo
jira:
  board_url: https://example.atlassian.net/jira/software/c/projects/PROJ/boards/1`,
			description: "Config with work sources but no base_branch should work",
		},
		{
			name: "config with all fields except base_branch",
			configContent: `repo: test/repo
label: kiro-krew
poll_interval: 5m
max_retries: 3
console_logging: true`,
			description: "Full config without base_branch should work",
		},
		{
			name: "config with sandbox but no base_branch",
			configContent: `repo: test/repo
sandbox:
  cpu_cores: 2.0
  memory_mb: 2048`,
			description: "Config with sandbox section but no base_branch should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
			if err := os.Mkdir(configDir, 0755); err != nil {
				t.Fatalf("Failed to create config dir: %v", err)
			}

			configFile := configDir + string(os.PathSeparator) + "config.yaml"
			if err := os.WriteFile(configFile, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldDir); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			})
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v (test case: %s)", err, tt.description)
			}

			// All backward compatibility tests should default to "main"
			if cfg.BaseBranch != "main" {
				t.Errorf("BaseBranch = %q, expected %q (test case: %s)", cfg.BaseBranch, "main", tt.description)
			}
		})
	}
}

func TestLoad_BaseBranchEmptyString(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
	if err := os.Mkdir(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Explicitly setting empty string overwrites the default
	configContent := `repo: test/repo
base_branch: ""`
	configFile := configDir + string(os.PathSeparator) + "config.yaml"
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	})
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Explicitly setting empty string results in empty string (overwrites default)
	if cfg.BaseBranch != "" {
		t.Errorf("BaseBranch = %q, expected %q (explicit empty string overwrites default)", cfg.BaseBranch, "")
	}
}

func TestLoad_BaseBranchWithWhitespace(t *testing.T) {
	tests := []struct {
		name               string
		configContent      string
		expectedBaseBranch string
	}{
		{
			name: "trailing whitespace",
			configContent: `repo: test/repo
base_branch: "dev   "`,
			expectedBaseBranch: "dev   ",
		},
		{
			name: "leading whitespace",
			configContent: `repo: test/repo
base_branch: "   dev"`,
			expectedBaseBranch: "   dev",
		},
		{
			name: "leading and trailing whitespace",
			configContent: `repo: test/repo
base_branch: "  dev  "`,
			expectedBaseBranch: "  dev  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
			if err := os.Mkdir(configDir, 0755); err != nil {
				t.Fatalf("Failed to create config dir: %v", err)
			}

			configFile := configDir + string(os.PathSeparator) + "config.yaml"
			if err := os.WriteFile(configFile, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldDir); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			})
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			// YAML parsing preserves whitespace in quoted strings
			if cfg.BaseBranch != tt.expectedBaseBranch {
				t.Errorf("BaseBranch = %q, expected %q", cfg.BaseBranch, tt.expectedBaseBranch)
			}
		})
	}
}
