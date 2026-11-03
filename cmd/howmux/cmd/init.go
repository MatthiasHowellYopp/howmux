package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		reportAgentSetup(cmd.OutOrStdout(), cwd)
		return nil
	},
}

// agentStatus is what init found for the pipeline agents in one repo.
type agentStatus struct {
	missing   []string          // no config in any location
	failed    map[string]error  // a location couldn't be checked (e.g. permission denied)
	overrides map[string]string // name -> workspace config that shadows ~/.kiro/agents
}

// checkPipelineAgents resolves every pipeline agent from cwd, in
// pipelineAgents order. A not-found result is kept apart from other errors so
// the user isn't told to reinstall agents that exist but couldn't be read.
func checkPipelineAgents(cwd string) agentStatus {
	st := agentStatus{failed: map[string]error{}, overrides: map[string]string{}}
	for _, name := range pipelineAgents {
		if err := acp.ValidateAgentResolvable(cwd, name); err != nil {
			if errors.Is(err, acp.ErrAgentNotFound) {
				st.missing = append(st.missing, name)
			} else {
				st.failed[name] = err
			}
			continue
		}
		// kiro-cli prefers a workspace agent over the global one, so a
		// leftover config from an older `howmux init` (or a deliberate
		// repo override) silently replaces the shared agent.
		local := filepath.Join(cwd, ".kiro", "agents", name+".json")
		if _, err := os.Stat(local); err == nil {
			st.overrides[name] = local
		}
	}
	return st
}

// reportAgentSetup prints notices about the pipeline agents. It warns rather
// than fails: init still did its job, and agents can be fixed afterwards.
func reportAgentSetup(w io.Writer, cwd string) {
	st := checkPipelineAgents(cwd)

	if len(st.overrides) > 0 {
		fmt.Fprintln(w, "\nℹ  These repo-local agents override the shared ones in ~/.kiro/agents:")
		for _, name := range pipelineAgents {
			if path, ok := st.overrides[name]; ok {
				fmt.Fprintf(w, "   %s (%s)\n", name, path)
			}
		}
		fmt.Fprintln(w, "   Delete them to use the shared agents, unless the override is intended.")
	}

	if len(st.failed) > 0 {
		fmt.Fprintln(w, "\n⚠  Couldn't check some howmux agents:")
		for _, name := range pipelineAgents {
			if err, ok := st.failed[name]; ok {
				fmt.Fprintf(w, "   %s: %v\n", name, err)
			}
		}
	}

	if len(st.missing) > 0 {
		fmt.Fprintf(w, "\n⚠  howmux agents not found: %s\n", strings.Join(st.missing, ", "))
		fmt.Fprintln(w, "   howmux loads its agents from ~/.kiro/agents (or this repo's .kiro/agents).")
		fmt.Fprintln(w, "   Install them from ai-resources: link ~/.kiro/agents to ai-resources/agents,")
		fmt.Fprintln(w, "   then run scripts/generate-agents.sh and scripts/generate-skills.sh --link there.")
	}
}

func init() {
	rootCmd.AddCommand(initCmd)
}
