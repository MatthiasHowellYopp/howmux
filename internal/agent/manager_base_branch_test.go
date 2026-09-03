package agent

import (
	"testing"

	"github.com/jbrinkman/kiro-krew/internal/config"
)

func TestExtractBaseBranchOverride(t *testing.T) {
	tests := []struct {
		name        string
		issueBody   string
		expected    string
		description string
	}{
		{
			name: "simple override present",
			issueBody: `This is an issue
Base-Branch: dev
Some more text`,
			expected:    "dev",
			description: "Standard Base-Branch: dev format",
		},
		{
			name:        "override with no extra text",
			issueBody:   `Base-Branch: feature/new-ui`,
			expected:    "feature/new-ui",
			description: "Single line with Base-Branch only",
		},
		{
			name: "override not present",
			issueBody: `This is an issue
with no base branch override
just regular text`,
			expected:    "",
			description: "No Base-Branch line",
		},
		{
			name:        "empty issue body",
			issueBody:   "",
			expected:    "",
			description: "Empty string",
		},
		{
			name: "case insensitive - lowercase",
			issueBody: `This is an issue
base-branch: develop
Some more text`,
			expected:    "develop",
			description: "Lowercase base-branch",
		},
		{
			name: "case insensitive - uppercase",
			issueBody: `This is an issue
BASE-BRANCH: master
Some more text`,
			expected:    "master",
			description: "Uppercase BASE-BRANCH",
		},
		{
			name: "case insensitive - mixed case",
			issueBody: `This is an issue
BaSe-BrAnCh: release/v1.0
Some more text`,
			expected:    "release/v1.0",
			description: "Mixed case BaSe-BrAnCh",
		},
		{
			name:        "whitespace before colon",
			issueBody:   `Base-Branch  : dev`,
			expected:    "",
			description: "Whitespace before colon should not match (invalid format)",
		},
		{
			name:        "whitespace after colon",
			issueBody:   `Base-Branch:   dev`,
			expected:    "dev",
			description: "Whitespace after colon is trimmed",
		},
		{
			name:        "leading whitespace on line",
			issueBody:   `   Base-Branch: dev`,
			expected:    "dev",
			description: "Leading whitespace on line is trimmed",
		},
		{
			name:        "trailing whitespace on line",
			issueBody:   `Base-Branch: dev   `,
			expected:    "dev",
			description: "Trailing whitespace on value is trimmed",
		},
		{
			name:        "whitespace all around",
			issueBody:   `   Base-Branch:   dev   `,
			expected:    "dev",
			description: "All whitespace is trimmed",
		},
		{
			name: "multiple Base-Branch lines - first wins",
			issueBody: `Base-Branch: dev
Some text
Base-Branch: main`,
			expected:    "dev",
			description: "First occurrence is used",
		},
		{
			name:        "Base-Branch with special characters",
			issueBody:   `Base-Branch: feature/ABC-123-special_branch`,
			expected:    "feature/ABC-123-special_branch",
			description: "Branch name with slashes, dashes, underscores",
		},
		{
			name:        "Base-Branch in middle of line",
			issueBody:   `Some text Base-Branch: dev more text`,
			expected:    "",
			description: "Base-Branch not at start of trimmed line (after trim) - should not match",
		},
		{
			name:        "Base-Branch with tabs",
			issueBody:   "Base-Branch:\tdev",
			expected:    "dev",
			description: "Tab after colon is trimmed",
		},
		{
			name: "Base-Branch with newlines in value",
			issueBody: `Base-Branch: dev
next line`,
			expected:    "dev",
			description: "Value is only up to newline",
		},
		{
			name:        "Base-Branch without value",
			issueBody:   `Base-Branch:`,
			expected:    "",
			description: "Missing value after colon returns empty",
		},
		{
			name:        "Base-Branch without colon",
			issueBody:   `Base-Branch dev`,
			expected:    "",
			description: "Missing colon should not match",
		},
		{
			name: "Similar but not Base-Branch",
			issueBody: `Base Branch: dev
BasesBranch: dev
Branch: dev`,
			expected:    "",
			description: "Similar patterns should not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBaseBranchOverride(tt.issueBody)
			if result != tt.expected {
				t.Errorf("extractBaseBranchOverride() = %q, expected %q (test: %s)", result, tt.expected, tt.description)
			}
		})
	}
}

func TestResolveBaseBranch(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		issueBody   string
		expected    string
		description string
	}{
		{
			name: "issue override takes precedence",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `Base-Branch: dev`,
			expected:    "dev",
			description: "Issue override should ignore config value",
		},
		{
			name: "config value when no override",
			cfg: &config.Config{
				BaseBranch: "develop",
			},
			issueBody:   `Just regular issue text`,
			expected:    "develop",
			description: "Config value used when no override",
		},
		{
			name: "default main when config empty and no override",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `No override here`,
			expected:    "main",
			description: "Config default (main) when no override",
		},
		{
			name: "issue override even with empty config",
			cfg: &config.Config{
				BaseBranch: "",
			},
			issueBody:   `Base-Branch: feature/test`,
			expected:    "feature/test",
			description: "Issue override works even if config is empty",
		},
		{
			name: "empty config and no override returns config value",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   ``,
			expected:    "main",
			description: "Empty issue body returns config value",
		},
		{
			name: "precedence order explicit test",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `Base-Branch: custom`,
			expected:    "custom",
			description: "Precedence: issue override > config",
		},
		{
			name: "config value with special branch name",
			cfg: &config.Config{
				BaseBranch: "release/v2.0-beta",
			},
			issueBody:   `No override`,
			expected:    "release/v2.0-beta",
			description: "Config supports complex branch names",
		},
		{
			name: "issue override with special branch name",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `Base-Branch: hotfix/urgent-123`,
			expected:    "hotfix/urgent-123",
			description: "Issue override supports complex branch names",
		},
		{
			name: "case insensitive override overrides config",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `BASE-BRANCH: staging`,
			expected:    "staging",
			description: "Case insensitive override still takes precedence",
		},
		{
			name: "whitespace trimmed override overrides config",
			cfg: &config.Config{
				BaseBranch: "main",
			},
			issueBody:   `   Base-Branch:   dev   `,
			expected:    "dev",
			description: "Whitespace trimmed before precedence check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveBaseBranch(tt.cfg, tt.issueBody)
			if result != tt.expected {
				t.Errorf("resolveBaseBranch() = %q, expected %q (test: %s)", result, tt.expected, tt.description)
			}
		})
	}
}

func TestResolveBaseBranchPrecedenceOrder(t *testing.T) {
	// Explicit test for precedence order documentation
	tests := []struct {
		name        string
		configValue string
		issueBody   string
		expected    string
	}{
		{
			name:        "Precedence level 1: Issue override present",
			configValue: "main",
			issueBody:   "Base-Branch: dev",
			expected:    "dev",
		},
		{
			name:        "Precedence level 2: Config value (no override)",
			configValue: "develop",
			issueBody:   "Regular issue text",
			expected:    "develop",
		},
		{
			name:        "Precedence level 3: Default main (config default, no override)",
			configValue: "main",
			issueBody:   "",
			expected:    "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{BaseBranch: tt.configValue}
			result := resolveBaseBranch(cfg, tt.issueBody)
			if result != tt.expected {
				t.Errorf("resolveBaseBranch() = %q, expected %q (precedence test)", result, tt.expected)
			}
		})
	}
}

func TestExtractBaseBranchOverrideEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		issueBody string
		expected  string
	}{
		{
			name:      "only newlines",
			issueBody: "\n\n\n",
			expected:  "",
		},
		{
			name:      "only whitespace",
			issueBody: "   \t   \n   ",
			expected:  "",
		},
		{
			name:      "colon but no Base-Branch",
			issueBody: "Some: text\nOther: value",
			expected:  "",
		},
		{
			name:      "Base-Branch in code block",
			issueBody: "```\nBase-Branch: dev\n```",
			expected:  "dev",
		},
		{
			name:      "very long branch name",
			issueBody: "Base-Branch: feature/very-long-branch-name-with-many-segments/subsegment/another-level",
			expected:  "feature/very-long-branch-name-with-many-segments/subsegment/another-level",
		},
		{
			name:      "unicode in branch name",
			issueBody: "Base-Branch: feature/✨-new-feature",
			expected:  "feature/✨-new-feature",
		},
		{
			name:      "URL-like branch name",
			issueBody: "Base-Branch: user/repo@branch",
			expected:  "user/repo@branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBaseBranchOverride(tt.issueBody)
			if result != tt.expected {
				t.Errorf("extractBaseBranchOverride() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
