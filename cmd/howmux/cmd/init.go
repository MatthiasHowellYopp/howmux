package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/matthiashowellyopp/howmux/internal/acp"
	"github.com/matthiashowellyopp/howmux/internal/templates"
	"github.com/spf13/cobra"
)

// pipelineAgents are the agents howmux launches or krew-lead delegates to.
// howmux no longer ships them: they come from ~/.kiro/agents (the shared
// ai-resources copy) or a repo's own .kiro/agents.
var pipelineAgents = []string{"planner", "krew-lead", "architect", "builder", "validator", "documenter"}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Extract project templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := templates.Extract("templates", ".", false); err != nil {
			return err
		}
		warnMissingAgents(cmd.OutOrStdout(), ".")
		return nil
	},
}

// missingPipelineAgents returns the pipeline agents kiro-cli can't resolve
// from cwd, in pipelineAgents order.
func missingPipelineAgents(cwd string) []string {
	var missing []string
	for _, name := range pipelineAgents {
		if acp.ValidateAgentResolvable(cwd, name) != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// warnMissingAgents prints setup guidance when pipeline agents are missing.
// It warns rather than fails: init still did its job, and the agents can be
// installed afterwards.
func warnMissingAgents(w io.Writer, cwd string) {
	if wd, err := os.Getwd(); err == nil && cwd == "." {
		cwd = wd
	}
	missing := missingPipelineAgents(cwd)
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(w, "\n⚠  howmux agents not found: %s\n", strings.Join(missing, ", "))
	fmt.Fprintln(w, "   howmux loads its agents from ~/.kiro/agents (or this repo's .kiro/agents).")
	fmt.Fprintln(w, "   Install them from ai-resources: link ~/.kiro/agents to ai-resources/agents,")
	fmt.Fprintln(w, "   then run scripts/generate-agents.sh and scripts/generate-skills.sh --link there.")
}

func init() {
	rootCmd.AddCommand(initCmd)
}
