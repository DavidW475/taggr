/*
Copyright © 2026 David Welz <david.welz@outlook.com>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/DavidW475/taggr/internal/release"
)

// Flags and configuration keys specific to the tag command.
const (
	flagMessage   = "message"
	flagAnnotated = "annotated"
	flagDryRun    = "dry-run"

	keyMessage   = "tag.message"
	keyAnnotated = "tag.annotated"
	keyDryRun    = "tag.dry_run"
)

// newTagCommand creates the next version tag.
func (c *cli) newTagCommand() *cobra.Command {
	tagCmd := &cobra.Command{
		Use:   "tag",
		Short: "Create the next version tag on the target platform",
		Long: `Create the next semantic version tag on the target platform.

taggr resolves the commit to tag, reads the highest version tag the repository
already has, asks the bump source how large the next increment should be, and
creates the resulting tag on the platform.

The tag is annotated with "Release <tag>" unless a different message is given or
--annotated=false asks for a lightweight tag. Nothing is created when the bump
source reports that no release is due, or when --dry-run is set. Both cases still
succeed, so a pipeline step does not fail merely because a change did not warrant
a release.

Examples:
  # Inside a pipeline: repository, commit and pull request come from the build
  # environment, the bump from the labels of the pull request.
  taggr tag

  # Release a specific commit using the labels of a known pull request.
  taggr tag --repository checkout --ref 4f2a91c... --pull-request 1421

  # Show what would happen without creating anything.
  taggr tag --dry-run

  # Tag without the leading "v" and with a message of your own.
  taggr tag --prefix "" --message "Checkout service release"`,
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

			dryRun := c.settingBool(cmd, flagDryRun, keyDryRun, false)
			created := false
			if plan.ReleaseDue() && !dryRun {
				if err := res.planner.Apply(ctx, plan, c.tagMessage(cmd, plan)); err != nil {
					return err
				}
				created = true
			}

			if format == outputJSON {
				return writeJSON(cmd, newPlanOutput(res, plan, dryRun, created))
			}
			if err := writePlanTable(cmd, res, plan); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n"+tagOutcome(plan, dryRun, created))
			return nil
		},
	}

	addReleaseFlags(tagCmd)
	tagCmd.Flags().StringP(flagMessage, "m", "",
		`message of the annotated tag (default "Release <tag>")`)
	tagCmd.Flags().Bool(flagAnnotated, true,
		"create an annotated tag; --annotated=false creates a lightweight one")
	tagCmd.Flags().Bool(flagDryRun, false,
		"resolve the version and print the plan without creating the tag")
	return tagCmd
}

// tagMessage returns the message of the tag to create. An empty message means a
// lightweight tag.
func (c *cli) tagMessage(cmd *cobra.Command, plan *release.Plan) string {
	if !c.settingBool(cmd, flagAnnotated, keyAnnotated, true) {
		return ""
	}
	if message := c.setting(cmd, flagMessage, keyMessage); message != "" {
		return message
	}
	return "Release " + plan.NextTag
}

// tagOutcome describes in one line what the run did.
func tagOutcome(plan *release.Plan, dryRun, created bool) string {
	switch {
	case created:
		return fmt.Sprintf("created tag %s on %s", plan.NextTag, plan.Commit)
	case plan.ReleaseDue():
		return fmt.Sprintf("dry run: tag %s was not created", plan.NextTag)
	default:
		return "no release due, nothing was tagged"
	}
}
