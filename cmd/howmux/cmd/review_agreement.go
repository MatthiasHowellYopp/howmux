package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/matthiashowellyopp/howmux/internal/review"
	"github.com/spf13/cobra"
)

var reviewAgreementJSON bool

var reviewAgreementCmd = &cobra.Command{
	Use:   "agreement",
	Short: "Compare the auto-post gate's calls with human review decisions",
	Long: `Compare the auto-post gate's calls with human review decisions.

Reads the audit trail under $PR_REVIEW_DIR (default ~/PR-Review). Each gate
run is paired with the first human decision (post, revise, rereview, discard)
made on that PR after the run and before the PR's next gate run. Runs from
before decisions were audited fall back to the PR's done/ file.

Classes:
  agree           the gate and the human chose the same action
  too_permissive  the gate would have posted; the human did something else
  too_strict      the gate would have discarded; the human kept the review
  punched         the gate deferred to a human (not graded)
  auto            the gate acted for real (not graded)
  undecided       no human decision found yet

Agreement = agree / (agree + too_permissive + too_strict).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := review.ReviewRootDir()
		if err != nil {
			return err
		}
		rep, err := review.BuildAgreementReport(root)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if reviewAgreementJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return fmt.Errorf("encode report: %w", err)
			}
			return nil
		}
		review.WriteAgreementText(out, rep)
		return nil
	},
}

func init() {
	reviewAgreementCmd.Flags().BoolVar(&reviewAgreementJSON, "json", false, "print the report as JSON")
	reviewCmd.AddCommand(reviewAgreementCmd)
}
