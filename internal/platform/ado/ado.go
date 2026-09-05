// Package ado implements the Azure DevOps platform on top of Microsoft's official
// Azure DevOps Go SDK.
package ado

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	azuredevops "github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
)

// Name is the name the platform is registered and selected under.
const Name = "azuredevops"

// Compile-time proof of the capabilities this platform offers.
var (
	_ platform.Platform            = (*Platform)(nil)
	_ platform.LabelReader         = (*Platform)(nil)
	_ platform.CommitReader        = (*Platform)(nil)
	_ platform.PullRequestResolver = (*Platform)(nil)
	_ platform.EnvironmentDetector = (*Platform)(nil)
)

func init() {
	platform.Register(Name, New)
}

// gitClient is the part of the SDK's git.Client that taggr uses. Narrowing the
// interface documents which API surface taggr depends on and lets the tests
// supply a fake instead of a live organisation.
type gitClient interface {
	GetRefs(context.Context, git.GetRefsArgs) (*git.GetRefsResponseValue, error)
	GetCommits(context.Context, git.GetCommitsArgs) (*[]git.GitCommitRef, error)
	GetCommit(context.Context, git.GetCommitArgs) (*git.GitCommit, error)
	UpdateRefs(context.Context, git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error)
	CreateAnnotatedTag(context.Context, git.CreateAnnotatedTagArgs) (*git.GitAnnotatedTag, error)
	GetRepository(context.Context, git.GetRepositoryArgs) (*git.GitRepository, error)
	GetPullRequestLabels(context.Context, git.GetPullRequestLabelsArgs) (*[]core.WebApiTagDefinition, error)
	GetPullRequestQuery(context.Context, git.GetPullRequestQueryArgs) (*git.GitPullRequestQuery, error)
}

// Platform reads tags from and creates tags on Azure Repos.
type Platform struct {
	// OrgURL is the organisation URL, e.g. https://dev.azure.com/contoso.
	OrgURL string
	// Project is the team project used for repositories that carry no owner.
	Project string
	// Token is the personal access token, or the pipeline's own access token,
	// used to authenticate. It needs the "Code (read & write)" scope to create
	// tags, and "Code (read)" is enough for a dry run.
	Token string

	// mu guards the lazily created client, so that a Platform stays safe to share.
	mu     sync.Mutex
	client gitClient
}

// New builds the Azure DevOps platform from its section of the configuration:
//
//	platforms:
//	  azuredevops:
//	    org_url: https://dev.azure.com/contoso
//	    project: Payments
//	    token: <personal access token>
//
// The token falls back to the AZURE_DEVOPS_EXT_PAT variable used by the Azure
// CLI, and then to SYSTEM_ACCESSTOKEN, which Azure Pipelines sets for a job when
// the pipeline maps it in.
func New(settings config.Settings) (platform.Platform, error) {
	orgURL, err := settings.Require("org_url")
	if err != nil {
		return nil, err
	}

	token := settings.String("token")
	if token == "" {
		token = tokenFromEnvironment()
	}
	if token == "" {
		return nil, fmt.Errorf("missing access token (set %q in the config file, or one of %s, AZURE_DEVOPS_EXT_PAT, SYSTEM_ACCESSTOKEN)",
			settings.Key("token"), settings.EnvName("token"))
	}

	return &Platform{
		OrgURL:  strings.TrimSuffix(orgURL, "/"),
		Project: settings.String("project"),
		Token:   token,
	}, nil
}

// Name returns the name the platform is selected under.
func (p *Platform) Name() string { return Name }

// tokenFromEnvironment returns the first access token the surrounding tooling
// provides, preferring the Azure CLI's variable over the pipeline's own token.
func tokenFromEnvironment() string {
	for _, key := range []string{"AZURE_DEVOPS_EXT_PAT", "SYSTEM_ACCESSTOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}

// git returns the SDK client, connecting on first use. Connecting resolves the
// organisation's API endpoints, which is a network call and therefore deferred
// out of New into the first operation that has a context to do it with.
func (p *Platform) git(ctx context.Context) (gitClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil {
		return p.client, nil
	}
	client, err := git.NewClient(ctx, azuredevops.NewPatConnection(p.OrgURL, p.Token))
	if err != nil {
		return nil, fmt.Errorf("ado: connect to %s: %w", p.OrgURL, err)
	}
	p.client = client
	return p.client, nil
}

// target returns the client and the team project to address the repository in.
func (p *Platform) target(ctx context.Context, repo platform.Repository) (gitClient, string, error) {
	if strings.TrimSpace(repo.Name) == "" {
		return nil, "", fmt.Errorf("ado: no repository given")
	}
	project := repo.Owner
	if project == "" {
		project = p.Project
	}
	if project == "" {
		return nil, "", fmt.Errorf("ado: no team project for repository %q (set the project in the config file or pass --owner)", repo.Name)
	}
	client, err := p.git(ctx)
	if err != nil {
		return nil, "", err
	}
	return client, project, nil
}

// deref returns the value behind a pointer the SDK may leave unset.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
