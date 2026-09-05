/*
Copyright © 2026 David Welz <david.welz@outlook.com>
*/
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	// Registers the built-in platforms and bump sources.
	_ "github.com/DavidW475/taggr/internal/plugins"
	"github.com/DavidW475/taggr/internal/source"
)

// Build information, overridden at build time with
// -ldflags "-X github.com/DavidW475/taggr/cmd.buildVersion=1.4.2".
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

// configName is the base name of the config file taggr looks for.
const configName = ".taggr"

// cli holds the state of a single run. Configuration lives in its own instance
// rather than in a package level singleton, so that two runs in one process — a
// test suite, an embedding program — cannot influence each other.
type cli struct {
	config  *viper.Viper
	cfgFile string
}

// NewRootCommand builds the command tree. Every call returns an independent tree
// with its own configuration and freshly parsed flags.
func NewRootCommand() *cobra.Command {
	c := &cli{config: viper.New()}

	root := &cobra.Command{
		Use:     "taggr",
		Short:   "Create semantic version tags on git hosting platforms",
		Long:    rootDescription(),
		Version: fmt.Sprintf("%s (%s)", buildVersion, buildCommit),
		// Usage on an error would bury the message that explains what went wrong.
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return c.initConfig(cmd)
		},
	}

	root.PersistentFlags().StringVar(&c.cfgFile, "config", "",
		fmt.Sprintf("config file (default: %s.yaml in the current or home directory)", configName))
	root.PersistentFlags().Duration("timeout", time.Minute,
		"maximum time for all platform requests of one run")

	root.AddCommand(c.newTagCommand(), c.newNextCommand())
	return root
}

// rootDescription builds the help text of the root command. The available
// platforms and bump sources are read from their registries, so the help stays
// correct as implementations are added.
func rootDescription() string {
	return fmt.Sprintf(`taggr creates semantic version tags on git hosting platforms.

For every release taggr reads the highest version tag a repository already has,
asks a bump source how large the next increment should be, and creates the
resulting tag on the target commit. Platforms and bump sources are independent
of each other, so the same versioning rules apply wherever a repository is
hosted.

  Platforms      %s
  Bump sources   %s

Settings come from flags, from environment variables prefixed with %s_, and
from a config file (%s.yaml in the current or home directory). Flags win
over environment variables, which win over the config file.

Inside a pipeline taggr needs no arguments at all: repository, commit and pull
request are read from the variables the CI system sets.

  taggr next               print the version that would be released next
  taggr tag --dry-run      show the full plan without changing anything
  taggr tag                create the tag`,
		strings.Join(platform.Names(), ", "),
		strings.Join(source.Names(), ", "),
		config.EnvPrefix,
		configName)
}

// Execute runs the root command. It is called by main.main exactly once.
func Execute() {
	// A cancelled context lets a running API call return instead of leaving the
	// process to be killed mid-request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := NewRootCommand().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

// initConfig reads the config file and connects the configuration to the
// environment. It runs before every command.
func (c *cli) initConfig(cmd *cobra.Command) error {
	if c.cfgFile != "" {
		c.config.SetConfigFile(c.cfgFile)
	} else {
		// The repository's own configuration takes precedence over a personal one.
		c.config.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			c.config.AddConfigPath(home)
		}
		c.config.SetConfigType("yaml")
		c.config.SetConfigName(configName)
	}

	c.config.SetEnvPrefix(config.EnvPrefix)
	c.config.SetEnvKeyReplacer(config.EnvKeyReplacer())
	c.config.AutomaticEnv()

	err := c.config.ReadInConfig()
	if err == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Using config file:", c.config.ConfigFileUsed())
		return nil
	}
	// A config file that was asked for by name has to exist; a missing default
	// one is normal, since every setting can also come from flags or the
	// environment.
	if c.cfgFile != "" {
		return err
	}
	var notFound viper.ConfigFileNotFoundError
	if !errors.As(err, &notFound) {
		return err
	}
	return nil
}
