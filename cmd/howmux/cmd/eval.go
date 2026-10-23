package cmd

import (
	"errors"

	"github.com/matthiashowellyopp/howmux/internal/eval"
	"github.com/spf13/cobra"
)

var (
	evalList   bool
	evalResume bool
	evalCase   string
	evalPerf   bool

	// evalSandbox backs the hidden --sandbox flag. The sandbox mode was
	// removed; the flag is kept only so old invocations fail with a clear
	// error instead of "unknown flag".
	evalSandbox bool

	// Deprecated no-op flags that only affected the removed sandbox mode.
	// They are still parsed so existing scripts don't break, but their
	// values are never read.
	evalNoSandbox     bool
	evalResourceLimit []string
	evalDebug         bool
	evalCleanup       bool
)

// errSandboxRemoved is returned when --sandbox is passed to eval.
var errSandboxRemoved = errors.New("the eval sandbox mode has been removed (the container could not authenticate kiro-cli); run evals without --sandbox")

// deprecatedSandboxFlagMsg is the deprecation notice for flags that only had
// effect in the removed sandbox mode.
const deprecatedSandboxFlagMsg = "it only had effect in the removed eval sandbox mode and is now ignored"

var evalCmd = &cobra.Command{
	Use:   "eval [agent] [testcase]",
	Short: "Run evaluations or show diff between runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		if evalSandbox {
			return errSandboxRemoved
		}

		var agent, testcase string
		if len(args) > 0 {
			agent = args[0]
		}
		if len(args) > 1 {
			testcase = args[1]
		}

		// Use --case flag if provided
		if evalCase != "" {
			testcase = evalCase
		}

		// Handle performance investigation
		if evalPerf {
			return eval.RunPerformanceInvestigation(agent)
		}

		return eval.RunWithOptions(agent, testcase, eval.RunOptions{
			List:   evalList,
			Resume: evalResume,
		})
	},
}

var diffCmd = &cobra.Command{
	Use:   "diff <runA> <runB>",
	Short: "Compare two evaluation runs",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return eval.Diff(args[0], args[1])
	},
}

var baselineCompareOnly bool

var baselineCmd = &cobra.Command{
	Use:   "baseline [agent]",
	Short: "Run evals and compare against the committed baseline (.howmux/evals/baseline.json)",
	Long: `Run evaluations and compare the result against the committed baseline,
printing per-agent and per-case deltas. Exits non-zero if any baseline agent
regressed beyond tolerance — use it to gate a prompt-change workflow.

With --compare-only, skips running and compares the latest existing run instead.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var agent string
		if len(args) > 0 {
			agent = args[0]
		}
		if !baselineCompareOnly {
			if err := eval.RunWithOptions(agent, "", eval.RunOptions{}); err != nil {
				return err
			}
		}
		return eval.CompareToBaselineLatest()
	},
}

func init() {
	baselineCmd.Flags().BoolVar(&baselineCompareOnly, "compare-only", false, "Skip running; compare the latest existing run against the baseline")
}

func init() {
	evalCmd.Flags().BoolVar(&evalList, "list", false, "List available test cases for the agent")
	evalCmd.Flags().BoolVar(&evalResume, "resume", false, "Resume interrupted evaluation from last completed test")
	evalCmd.Flags().StringVar(&evalCase, "case", "", "Run specific test case")
	evalCmd.Flags().BoolVar(&evalPerf, "perf", false, "Run performance investigation and profiling")

	// Removed sandbox mode: hidden, errors if used.
	evalCmd.Flags().BoolVar(&evalSandbox, "sandbox", false, "Removed; returns an error if set")
	_ = evalCmd.Flags().MarkHidden("sandbox")

	// Sandbox-only flags: hidden, deprecated, ignored.
	evalCmd.Flags().BoolVar(&evalNoSandbox, "no-sandbox", false, "Deprecated; ignored")
	evalCmd.Flags().StringSliceVar(&evalResourceLimit, "resource-limit", nil, "Deprecated; ignored")
	evalCmd.Flags().BoolVarP(&evalDebug, "debug", "d", false, "Deprecated; ignored")
	evalCmd.Flags().BoolVar(&evalCleanup, "cleanup", false, "Deprecated; ignored")
	for _, name := range []string{"no-sandbox", "resource-limit", "debug", "cleanup"} {
		_ = evalCmd.Flags().MarkDeprecated(name, deprecatedSandboxFlagMsg)
		_ = evalCmd.Flags().MarkHidden(name)
	}

	evalCmd.AddCommand(diffCmd)
	evalCmd.AddCommand(baselineCmd)
	rootCmd.AddCommand(evalCmd)
}
