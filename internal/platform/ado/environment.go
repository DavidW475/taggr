package ado

import (
	"os"
	"strings"

	"github.com/DavidW475/taggr/internal/platform"
)

// Azure Pipelines variables taggr infers the build context from. The names are
// the environment spelling of System.TeamProject, Build.Repository.Name,
// Build.SourceVersion and System.PullRequest.PullRequestId.
const (
	envProject     = "SYSTEM_TEAMPROJECT"
	envRepository  = "BUILD_REPOSITORY_NAME"
	envCommit      = "BUILD_SOURCEVERSION"
	envPullRequest = "SYSTEM_PULLREQUEST_PULLREQUESTID"
)

// DetectEnvironment reads the repository, commit and pull request from the
// variables Azure Pipelines sets, so that taggr needs no arguments inside a
// pipeline. Values given on the command line or in the config file win over the
// detected ones.
//
// The pull request ID is only set while a pipeline runs for a pull request. On
// the branch build after the merge it is empty, and taggr falls back to
// PullRequestForCommit to find the pull request the commit was merged by.
func (p *Platform) DetectEnvironment() platform.Environment {
	env := platform.Environment{
		Repository: platform.Repository{
			Owner: strings.TrimSpace(os.Getenv(envProject)),
			Name:  strings.TrimSpace(os.Getenv(envRepository)),
		},
		Ref:         strings.TrimSpace(os.Getenv(envCommit)),
		PullRequest: strings.TrimSpace(os.Getenv(envPullRequest)),
	}
	if env.Repository.Owner == "" {
		env.Repository.Owner = p.Project
	}
	return env
}
