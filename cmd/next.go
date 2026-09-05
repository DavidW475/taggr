/*
Copyright © 2026 David Welz <david.welz@outlook.com>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// flagTag switches the printed value from the bare version to the tag name.
const flagTag = "tag"

// newNextCommand prints the next version without changing anything.
func (c *cli) newNextCommand() *cobra.Command {
	nextCmd := &cobra.Command{
		Use:   "next",
		Short: "Print the next version without creating a tag",
		Long: `Print the version taggr would release next, without changing anything.

next performs exactly the same resolution as "taggr tag" — resolve the commit,
read the current version, ask the bump source for the size of the bump — but it
only reads and prints. That makes it the command to capture in a pipeline
variable before the artefacts are built:

  VERSION=$(taggr next)

When no release is due, the current version is printed instead and the reason is
written to standard error, so a surrounding script always receives a usable
version on standard output.

Examples:
  # 1.5.0
  taggr next

  # v1.5.0, the tag name including the configured prefix
  taggr next --tag

  # The whole plan: current version, bump, reason and resulting tag.
  taggr next --output json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := c.outputFormat(cmd)
			if err != nil {
				return err
			}

			ctx, cancel := commandContext(cmd)
			defer cancel()

			res, err := c.resolveRelease(cmd)
			if err != nil {
				return err
			}
			plan, err := res.planner.Plan(ctx, res.request)
			if err != nil {
				return err
			}

			if format == outputJSON {
				return writeJSON(cmd, newPlanOutput(res, plan, true, false))
			}

			// The reason belongs on standard error so that standard output stays a
			// plain version a script can consume.
			if !plan.ReleaseDue() {
				fmt.Fprintf(cmd.ErrOrStderr(), "no release due (%s), printing the current version\n", plan.Reason)
				if !plan.HasCurrentVersion {
					fmt.Fprintln(cmd.ErrOrStderr(), "the repository has no version tag yet, nothing to print")
					return nil
				}
			}
			if asTag, _ := cmd.Flags().GetBool(flagTag); asTag {
				fmt.Fprintln(cmd.OutOrStdout(), plan.NextTag)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), plan.NextVersion)
			return nil
		},
	}

	addReleaseFlags(nextCmd)
	nextCmd.Flags().Bool(flagTag, false,
		"print the tag name including the prefix instead of the bare version")
	return nextCmd
}
