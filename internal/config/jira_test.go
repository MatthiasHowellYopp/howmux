package config

import (
	"os"
	"strings"
	"testing"
)

// writeConfigAndLoad writes the given YAML into a temp .kiro-krew/config.yaml,
// changes into that directory for the duration of the test, and loads it.
func writeConfigAndLoad(t *testing.T, content string) (*Config, error) {
	t.Helper()

	tmpDir := t.TempDir()
	configDir := tmpDir + string(os.PathSeparator) + ".kiro-krew"
	if err := os.Mkdir(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	configFile := configDir + string(os.PathSeparator) + "config.yaml"
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
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

	return Load()
}

func TestLoad_RequiresAtLeastOneSource(t *testing.T) {
	// Neither githubrepo nor jira.board_url set -> error.
	_, err := writeConfigAndLoad(t, `label: test-label`)
	if err == nil {
		t.Fatal("expected error when no work source is configured, got nil")
	}
	if !strings.Contains(err.Error(), "at least one work source") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_JiraOnlyIsValid(t *testing.T) {
	// Jira board configured, no githubrepo -> valid.
	cfg, err := writeConfigAndLoad(t, `jira:
  board_url: "https://example.atlassian.net/board/1"`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, expected empty", cfg.GithubRepo)
	}
	if !cfg.Jira.IsConfigured() {
		t.Error("expected Jira to be configured")
	}
}

func TestLoad_GithubOnlyIsValid(t *testing.T) {
	cfg, err := writeConfigAndLoad(t, `githubrepo: owner/name`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GithubRepo != "owner/name" {
		t.Errorf("GithubRepo = %q, expected owner/name", cfg.GithubRepo)
	}
	if cfg.Jira.IsConfigured() {
		t.Error("expected Jira to be unconfigured")
	}
}

func TestLoad_JiraDefaultJQLFromCurrentUser(t *testing.T) {
	cfg, err := writeConfigAndLoad(t, `jira:
  board_url: "https://example.atlassian.net/board/1"
  assignee: "currentUser()"`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := `assignee = currentUser() AND status = "To Do"`
	if cfg.Jira.JQL != want {
		t.Errorf("JQL = %q, expected %q", cfg.Jira.JQL, want)
	}
}

func TestLoad_JiraDefaultJQLFromEmailIsQuoted(t *testing.T) {
	cfg, err := writeConfigAndLoad(t, `jira:
  board_url: "https://example.atlassian.net/board/1"
  assignee: "user@example.com"`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := `assignee = "user@example.com" AND status = "To Do"`
	if cfg.Jira.JQL != want {
		t.Errorf("JQL = %q, expected %q", cfg.Jira.JQL, want)
	}
}

func TestLoad_JiraDefaultJQLWhenNoAssignee(t *testing.T) {
	// Board configured but no assignee -> falls back to currentUser().
	cfg, err := writeConfigAndLoad(t, `jira:
  board_url: "https://example.atlassian.net/board/1"`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := `assignee = currentUser() AND status = "To Do"`
	if cfg.Jira.JQL != want {
		t.Errorf("JQL = %q, expected %q", cfg.Jira.JQL, want)
	}
}

func TestLoad_JiraExplicitJQLPreserved(t *testing.T) {
	custom := `project = ABC AND status = "In Review"`
	cfg, err := writeConfigAndLoad(t, `jira:
  board_url: "https://example.atlassian.net/board/1"
  jql: '`+custom+`'`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Jira.JQL != custom {
		t.Errorf("JQL = %q, expected %q (should not be overwritten)", cfg.Jira.JQL, custom)
	}
}
