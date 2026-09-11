package cmd

import (
	"github.com/matthiashowellyopp/howmux/internal/templates"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Extract project templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		return templates.Extract("templates", ".", false)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
