/*
Copyright © 2026 David Welz <david.welz@outlook.com>
*/
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/release"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// Names of the flags shared by the commands that resolve a version.
const (
	flagPlatform       = "platform"
	flagSource         = "source"
	flagOwner          = "owner"
	flagRepository     = "repository"
	flagRef            = "ref"
	flagPullRequest    = "pull-request"
	flagPrefix         = "prefix"
	flagInitialVersion = "initial-version"
	flagOutput         = "output"
)

// Configuration keys the shared flags fall back to.
const (
	keyPlatform       = "platform"
	keySource         = "source"
	keyOwner          = "owner"
	keyRepository     = "repository"
	keyRef            = "ref"
	keyPullRequest    = "pull_request"
	keyPrefix         = "tag.prefix"
	keyInitialVersion = "tag.initial_version"
	keyOutput         = "output"
)

// Defaults of the shared settings.
const (
	defaultPlatform       = "azuredevops"
	defaultSource         = "pr-labels"
	defaultPrefix         = "v"
	defaultInitialVersion = "0.1.0"
)

// Output formats understood by --output.
const (
	outputText = "text"
	outputJSON = "json"
)

// addReleaseFlags registers the flags shared by every command that resolves a
// version: which platform and bump source to use, which repository and commit to
// act on, and how the tag name is built.
func addReleaseFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.String(flagPlatform, defaultPlatform,
		"platform to read tags from and create tags on ("+strings.Join(platform.Names(), ", ")+")")
	flags.String(flagSource, defaultSource,
		"where the size of the version bump is read from ("+strings.Join(source.Names(), ", ")+")")
	flags.String(flagOwner, "",
		"owner of the repository, the team project on Azure DevOps (default: detected from the CI environment)")
	flags.StringP(flagRepository, "r", "",
		"repository to tag (default: detected from the CI environment)")
	flags.String(flagRef, "",
		"branch, tag or full commit ID to tag (default: the tip of the default branch)")
	flags.String(flagPullRequest, "",
		"pull request the bump is read from (default: the current pull request, or the one the commit was merged by)")
	flags.String(flagPrefix, defaultPrefix,
		"prefix put in front of the version to form the tag name")
	flags.String(flagInitialVersion, defaultInitialVersion,
		"version to release when the repository has no version tag yet")
	flags.String(flagOutput, outputText,
		"output format: text or json")
}

// resolved is the fully merged input of a release command: settings, flags and
// the CI environment combined into the objects the planner works with.
type resolved struct {
	planner      *release.Planner
	platformName string
	sourceName   string
	request      release.Request
}

// resolveRelease builds the planner and the request for a release command.
func (c *cli) resolveRelease(cmd *cobra.Command) (*resolved, error) {
	platformName := c.setting(cmd, flagPlatform, keyPlatform)
	plat, err := platform.Open(platformName, config.New("platforms."+platformName, c.config.Get))
	if err != nil {
		return nil, err
	}

	sourceName := c.setting(cmd, flagSource, keySource)
	bumpSource, err := source.Open(sourceName, config.New("sources."+sourceName, c.config.Get))
	if err != nil {
		return nil, err
	}

	// A platform that recognises its CI system fills in whatever was not given
	// explicitly, which is what makes taggr argument-free inside a pipeline.
	var detected platform.Environment
	if detector, ok := plat.(platform.EnvironmentDetector); ok {
		detected = detector.DetectEnvironment()
	}

	repo := platform.Repository{
		Owner: firstNonEmpty(c.setting(cmd, flagOwner, keyOwner), detected.Repository.Owner),
		Name:  firstNonEmpty(c.setting(cmd, flagRepository, keyRepository), detected.Repository.Name),
	}
	if repo.Name == "" {
		return nil, fmt.Errorf("no repository given: pass --%s, set %q in the config file, or run inside a pipeline of the %s platform",
			flagRepository, keyRepository, platformName)
	}

	prefix := c.setting(cmd, flagPrefix, keyPrefix)
	initial, err := version.Parse(c.setting(cmd, flagInitialVersion, keyInitialVersion))
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", flagInitialVersion, err)
	}

	return &resolved{
		planner:      release.NewPlanner(plat, bumpSource, release.Options{Prefix: prefix, InitialVersion: initial}),
		platformName: platformName,
		sourceName:   sourceName,
		request: release.Request{
			Repository:  repo,
			Ref:         firstNonEmpty(c.setting(cmd, flagRef, keyRef), detected.Ref),
			PullRequest: firstNonEmpty(c.setting(cmd, flagPullRequest, keyPullRequest), detected.PullRequest),
		},
	}, nil
}

// commandContext derives the context of one run from the command, bounded by the
// --timeout flag so that a hanging API call cannot stall a pipeline forever.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	timeout, err := cmd.Root().PersistentFlags().GetDuration("timeout")
	if err != nil || timeout <= 0 {
		return context.WithCancel(cmd.Context())
	}
	return context.WithTimeout(cmd.Context(), timeout)
}

// setting returns the value of a flag when it was given on the command line, and
// falls back to the configuration value of key, then to the flag's default.
func (c *cli) setting(cmd *cobra.Command, flagName, key string) string {
	flag := cmd.Flags().Lookup(flagName)
	if flag != nil && flag.Changed {
		return strings.TrimSpace(flag.Value.String())
	}
	if value := strings.TrimSpace(c.config.GetString(key)); value != "" {
		return value
	}
	if flag != nil {
		return flag.DefValue
	}
	return ""
}

// settingBool is setting for a boolean flag.
func (c *cli) settingBool(cmd *cobra.Command, flagName, key string, def bool) bool {
	if flag := cmd.Flags().Lookup(flagName); flag != nil && flag.Changed {
		value, err := cmd.Flags().GetBool(flagName)
		if err == nil {
			return value
		}
	}
	if c.config.IsSet(key) {
		return c.config.GetBool(key)
	}
	return def
}

// firstNonEmpty returns the first value that is not empty.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// outputFormat returns the validated value of --output.
func (c *cli) outputFormat(cmd *cobra.Command) (string, error) {
	format := strings.ToLower(c.setting(cmd, flagOutput, keyOutput))
	switch format {
	case outputText, outputJSON:
		return format, nil
	default:
		return "", fmt.Errorf("--%s: unknown format %q (want %s or %s)", flagOutput, format, outputText, outputJSON)
	}
}

// planOutput is the machine readable form of a plan, written by --output json.
type planOutput struct {
	Platform       string `json:"platform"`
	Source         string `json:"source"`
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	CurrentVersion string `json:"current_version,omitempty"`
	CurrentTag     string `json:"current_tag,omitempty"`
	Bump           string `json:"bump"`
	Reason         string `json:"reason"`
	NextVersion    string `json:"next_version,omitempty"`
	NextTag        string `json:"next_tag,omitempty"`
	ReleaseDue     bool   `json:"release_due"`
	DryRun         bool   `json:"dry_run"`
	Created        bool   `json:"created"`
}

// newPlanOutput assembles the machine readable form of a finished run.
func newPlanOutput(res *resolved, plan *release.Plan, dryRun, created bool) planOutput {
	out := planOutput{
		Platform:   res.platformName,
		Source:     res.sourceName,
		Repository: plan.Repository.String(),
		Commit:     plan.Commit,
		CurrentTag: plan.CurrentTag,
		Bump:       plan.Bump.String(),
		Reason:     plan.Reason,
		ReleaseDue: plan.ReleaseDue(),
		DryRun:     dryRun,
		Created:    created,
	}
	if plan.HasCurrentVersion {
		out.CurrentVersion = plan.CurrentVersion.String()
	}
	if plan.ReleaseDue() {
		out.NextVersion = plan.NextVersion.String()
		out.NextTag = plan.NextTag
	}
	return out
}

// writeJSON writes v as indented JSON to the command's output.
func writeJSON(cmd *cobra.Command, v any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

// writePlanTable writes the plan as an aligned key/value table.
func writePlanTable(cmd *cobra.Command, res *resolved, plan *release.Plan) error {
	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	row := func(label, value string) {
		fmt.Fprintf(table, "%s\t%s\n", label, value)
	}

	row("platform", fmt.Sprintf("%s (bump from %s)", res.platformName, res.sourceName))
	row("repository", plan.Repository.String())
	row("commit", plan.Commit)
	if plan.HasCurrentVersion {
		row("current", plan.CurrentTag)
	} else {
		row("current", "none, this is the first release")
	}
	row("bump", fmt.Sprintf("%s — %s", plan.Bump, plan.Reason))
	if plan.ReleaseDue() {
		row("next", plan.NextTag)
	}
	return table.Flush()
}
