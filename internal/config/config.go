package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/logging"
	"gopkg.in/yaml.v3"
)

type SessionConfig struct {
	MaxHistoryMessages int           `yaml:"max_history_messages"`
	MaxAge             time.Duration `yaml:"max_age"`
	SessionsDir        string        `yaml:"sessions_dir"`
}

type LoggingConfig struct {
	DefaultLevel   string `yaml:"default_level"`
	MaxBufferLines int    `yaml:"max_buffer_lines"`
	MaxFileSizeMB  int    `yaml:"max_file_size_mb"`
	LogDir         string `yaml:"log_dir"`
}

// modelIDRe is the shape of a kiro-cli model id (e.g. claude-sonnet-5.5). It
// rejects any whitespace and a leading "-", so a bad auditor_model fails at
// load time instead of failing every auditor call at runtime (which would
// quietly send every review to a human).
var modelIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)

// AutoPostConfig controls the review auto-post gate's trust scope. See
// docs/design/review-auto-post-gate.md. The safe default is dry-run (the gate
// records its would-be decision but always punches to a human).
type AutoPostConfig struct {
	Mode          string   `yaml:"mode"`            // dry-run | own-repos | all
	MaxFindings   int      `yaml:"max_findings"`    // guardrail finding-count cap (<=0 disables)
	OwnRepoOwners []string `yaml:"own_repo_owners"` // owners allowed to auto-post in own-repos mode
	AuditorModel  string   `yaml:"auditor_model"`   // kiro-cli --model for the review-auditor; "" = kiro-cli's default
}

// ReviewConfig groups review-workflow configuration.
type ReviewConfig struct {
	AutoPost AutoPostConfig `yaml:"auto_post"`
}

type Config struct {
	Repo                string        `yaml:"repo"`
	Label               string        `yaml:"label"`
	BaseBranch          string        `yaml:"base_branch"`
	PollInterval        time.Duration `yaml:"poll_interval"`
	MaxRetries          int           `yaml:"max_retries"`
	MaxQARetries        int           `yaml:"max_qa_retries"`
	MaxActivityLines    int           `yaml:"max_activity_lines"`
	ConsoleLogging      bool          `yaml:"console_logging"`
	Theme               string        `yaml:"theme"`
	EnableCopilotReview bool          `yaml:"enable_copilot_review"`
	Reviewer            string        `yaml:"reviewer"` // Optional: overrides the resolved gh login used for IsReviewRequestedFor matching
	Session             SessionConfig `yaml:"session"`
	Logging             LoggingConfig `yaml:"logging"`
	Review              ReviewConfig  `yaml:"review"`
	LoadedTheme         *Theme        `yaml:"-"`
}

func extractLeadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func Load() (*Config, error) {
	cfg := &Config{
		Label:               "howmux",
		BaseBranch:          "main",
		PollInterval:        5 * time.Minute,
		MaxRetries:          3,
		MaxQARetries:        3,
		MaxActivityLines:    1000,
		ConsoleLogging:      false,
		Theme:               "default",
		EnableCopilotReview: true,
		Session: SessionConfig{
			MaxHistoryMessages: 100,
			MaxAge:             24 * time.Hour,
			SessionsDir:        ".howmux/sessions",
		},
		Logging: LoggingConfig{
			DefaultLevel:   "info",
			MaxBufferLines: 10000,
			MaxFileSizeMB:  100,
			LogDir:         ".howmux/logs",
		},
		Review: ReviewConfig{
			AutoPost: AutoPostConfig{
				Mode:          "dry-run", // safe default: never auto-posts
				MaxFindings:   25,
				OwnRepoOwners: nil,
			},
		},
	}

	data, err := os.ReadFile(".howmux/config.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Non-strict decode on purpose: unknown keys are ignored, so config files
	// that still carry blocks for removed features (e.g. the old eval
	// "sandbox:" block) keep loading.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.Repo == "" {
		return nil, fmt.Errorf("repo field is required")
	}

	// Validate session config
	if cfg.Session.MaxHistoryMessages <= 0 {
		return nil, fmt.Errorf("session.max_history_messages must be greater than 0")
	}
	if cfg.Session.MaxAge <= 0 {
		return nil, fmt.Errorf("session.max_age must be greater than 0")
	}
	if cfg.Session.SessionsDir == "" {
		return nil, fmt.Errorf("session.sessions_dir cannot be empty")
	}

	// Validate logging config
	validLevels := map[string]bool{
		logging.LevelDebug: true,
		logging.LevelInfo:  true,
		logging.LevelWarn:  true,
		logging.LevelError: true,
	}
	if !validLevels[cfg.Logging.DefaultLevel] {
		return nil, fmt.Errorf("logging.default_level must be one of: %s, %s, %s, %s",
			logging.LevelDebug, logging.LevelInfo, logging.LevelWarn, logging.LevelError)
	}
	if cfg.Logging.MaxBufferLines <= 0 {
		return nil, fmt.Errorf("logging.max_buffer_lines must be greater than 0")
	}
	if cfg.Logging.MaxFileSizeMB <= 0 {
		return nil, fmt.Errorf("logging.max_file_size_mb must be greater than 0")
	}
	if cfg.Logging.LogDir == "" {
		return nil, fmt.Errorf("logging.log_dir cannot be empty")
	}

	// Validate review auto-post config. An empty mode is treated as the safe
	// default (dry-run) rather than rejected, so existing config files without
	// a review: block keep working.
	switch cfg.Review.AutoPost.Mode {
	case "", "dry-run":
		cfg.Review.AutoPost.Mode = "dry-run"
	case "own-repos", "all":
		// valid
	default:
		return nil, fmt.Errorf("review.auto_post.mode must be one of: dry-run, own-repos, all (got %q)", cfg.Review.AutoPost.Mode)
	}
	cfg.Review.AutoPost.AuditorModel = strings.TrimSpace(cfg.Review.AutoPost.AuditorModel)
	if m := cfg.Review.AutoPost.AuditorModel; m != "" && !modelIDRe.MatchString(m) {
		return nil, fmt.Errorf("review.auto_post.auditor_model must be a single model id, e.g. claude-sonnet-5.5 (got %q)", cfg.Review.AutoPost.AuditorModel)
	}
	if cfg.Review.AutoPost.MaxFindings < 0 {
		return nil, fmt.Errorf("review.auto_post.max_findings must be >= 0 (0 disables the cap)")
	}

	// Load the specified theme
	theme, err := LoadTheme(cfg.Theme)
	if err != nil {
		fmt.Printf("Warning: Failed to load theme '%s': %v, using built-in default theme\n", cfg.Theme, err)
		theme = getDefaultTheme()
	}
	cfg.LoadedTheme = theme

	return cfg, nil
}

// Save writes the current configuration back to the config file,
// preserving existing YAML structure and comments
func (c *Config) Save() error {
	configPath := ".howmux/config.yaml"

	// Read existing file to preserve structure and comments
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Find and update theme line, or add it if not present
	themeUpdated := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		leadingWhitespace := extractLeadingWhitespace(line)
		if strings.HasPrefix(trimmed, "theme:") {
			lines[i] = fmt.Sprintf("%stheme: %q", leadingWhitespace, c.Theme)
			themeUpdated = true
			break
		}
		// Check for commented theme line
		if strings.HasPrefix(trimmed, "# theme:") {
			lines[i] = fmt.Sprintf("%stheme: %q", leadingWhitespace, c.Theme)
			themeUpdated = true
			break
		}
	}

	// If theme wasn't found, add it at the end
	if !themeUpdated {
		lines = append(lines, fmt.Sprintf("theme: %q", c.Theme))
	}

	// Write back to file
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
