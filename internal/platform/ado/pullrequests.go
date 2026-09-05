package ado

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/platform"
)

// PullRequestLabels returns the labels currently set on the pull request.
func (p *Platform) PullRequestLabels(ctx context.Context, repo platform.Repository, pullRequest string) ([]string, error) {
	id, err := parsePullRequestID(pullRequest)
	if err != nil {
		return nil, err
	}
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return nil, err
	}

	labels, err := client.GetPullRequestLabels(ctx, git.GetPullRequestLabelsArgs{
		RepositoryId:  &repo.Name,
		PullRequestId: &id,
		Project:       &project,
	})
	if err != nil {
		return nil, fmt.Errorf("ado: read the labels of pull request %d in %s: %w", id, repo, err)
	}
	if labels == nil {
		return nil, nil
	}

	names := make([]string, 0, len(*labels))
	for _, label := range *labels {
		// A label that was removed again stays on the pull request as inactive.
		if label.Active != nil && !*label.Active {
			continue
		}
		if name := strings.TrimSpace(deref(label.Name)); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// PullRequestForCommit returns the ID of the pull request the commit was merged
// by, or an empty string when the commit came from no pull request. It is what
// lets taggr find the labels of a merged pull request on the branch build that
// follows the merge, where Azure Pipelines no longer sets a pull request ID.
func (p *Platform) PullRequestForCommit(ctx context.Context, repo platform.Repository, commit string) (string, error) {
	if strings.TrimSpace(commit) == "" {
		return "", nil
	}
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return "", err
	}

	queryType := git.GitPullRequestQueryTypeValues.LastMergeCommit
	items := []string{commit}
	query := git.GitPullRequestQuery{
		Queries: &[]git.GitPullRequestQueryInput{{Items: &items, Type: &queryType}},
	}
	result, err := client.GetPullRequestQuery(ctx, git.GetPullRequestQueryArgs{
		Queries:      &query,
		RepositoryId: &repo.Name,
		Project:      &project,
	})
	if err != nil {
		return "", fmt.Errorf("ado: look up the pull request of commit %s in %s: %w", shortCommit(commit), repo, err)
	}
	if result == nil || result.Results == nil {
		return "", nil
	}

	// Each result is a map from the queried commit to the pull requests that
	// produced it; the commit IDs come back in whatever case the API used.
	for _, pullRequestsByCommit := range *result.Results {
		for candidate, pullRequests := range pullRequestsByCommit {
			if !strings.EqualFold(candidate, commit) {
				continue
			}
			for _, pullRequest := range pullRequests {
				if pullRequest.PullRequestId != nil {
					return strconv.Itoa(*pullRequest.PullRequestId), nil
				}
			}
		}
	}
	return "", nil
}

// parsePullRequestID accepts the numeric pull request ID Azure DevOps uses.
func parsePullRequestID(pullRequest string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(pullRequest))
	if err != nil {
		return 0, fmt.Errorf("ado: %q is not a numeric pull request ID", pullRequest)
	}
	if id <= 0 {
		return 0, fmt.Errorf("ado: pull request ID %d is not positive", id)
	}
	return id, nil
}

// shortCommit abbreviates a commit ID for human readable messages.
func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
