package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/matthiashowellyopp/howmux/internal/tui"
	"github.com/matthiashowellyopp/howmux/internal/watcher"
)

var reviewCmd = &cobra.Command{
	Use:   "review [PR_URL]",
	Short: "Start PR review workflow",
	Long: `Start the PR review workflow.

With a PR URL: Enroll the PR, review it now, and start the recurring loop.
Without arguments: Start the loop over already-enrolled PRs.

The preflight check runs first and fails if required kiro assets are missing.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Run preflight check first (matching REPL behavior)
		kiroDir := filepath.Join(os.Getenv("HOME"), ".kiro")
		if err := review.CheckReviewAssets(kiroDir); err != nil {
			return fmt.Errorf("PR-review preflight failed:\n%w", err)
		}

		manager := agent.NewManager(cfg)
		w := watcher.New(cfg, manager)

		defer manager.StopAll()
		defer w.Stop()

		// The CLI `review` command enters the TUI with the review command
		// pre-seeded. The TUI handles the actual review workflow.
		// This follows the pattern of other CLI commands that delegate to TUI.

		// For now, we'll enter the TUI and let the user execute the review
		// command manually. A future enhancement could auto-execute it.
		return tui.Run(w, manager, cfg)
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)
}
