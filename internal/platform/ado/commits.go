package ado

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/platform"
)

const (
	// commitPageSize is the number of commits requested per call.
	commitPageSize = 100
	// maxCommits bounds the walk through the history. Only the first release of a
	// repository has no lower bound, and no bump decision needs more than this.
	maxCommits = 2000
)

// Commits returns the commits between the previous release and the commit being
// released, newest first.
func (p *Platform) Commits(ctx context.Context, repo platform.Repository, commits platform.CommitRange) ([]platform.Commit, error) {
	if commits.To == "" {
		return nil, fmt.Errorf("ado: cannot list commits without a target commit")
	}
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return nil, err
	}

	// Azure DevOps walks the history backwards from compareVersion and stops at
	// itemVersion, so the previous release is the lower and the released commit
	// the upper bound. Without a previous release the whole history of the commit
	// is walked, bounded by maxCommits.
	criteria := &git.GitQueryCommitsCriteria{}
	if commits.From != "" {
		criteria.ItemVersion = versionDescriptor(commits.From)
		criteria.CompareVersion = versionDescriptor(commits.To)
	} else {
		criteria.ItemVersion = versionDescriptor(commits.To)
	}

	var out []platform.Commit
	for skip := 0; skip < maxCommits; skip += commitPageSize {
		top := commitPageSize
		page, err := client.GetCommits(ctx, git.GetCommitsArgs{
			RepositoryId:   &repo.Name,
			Project:        &project,
			SearchCriteria: criteria,
			Top:            &top,
			Skip:           &skip,
		})
		if err != nil {
			return nil, fmt.Errorf("ado: list the commits of %s: %w", repo, err)
		}
		if page == nil || len(*page) == 0 {
			break
		}
		for _, commit := range *page {
			message, err := p.fullMessage(ctx, client, project, repo, commit)
			if err != nil {
				return nil, err
			}
			out = append(out, platform.Commit{ID: deref(commit.CommitId), Message: message})
		}
		if len(*page) < top {
			break
		}
	}
	return out, nil
}

// fullMessage returns the complete commit message. Listing commits truncates long
// messages, which would hide a breaking change footer, so a truncated message is
// fetched again in full.
func (p *Platform) fullMessage(ctx context.Context, client gitClient, project string, repo platform.Repository, commit git.GitCommitRef) (string, error) {
	if commit.CommentTruncated == nil || !*commit.CommentTruncated {
		return deref(commit.Comment), nil
	}

	id := deref(commit.CommitId)
	full, err := client.GetCommit(ctx, git.GetCommitArgs{
		CommitId:     &id,
		RepositoryId: &repo.Name,
		Project:      &project,
	})
	if err != nil {
		return "", fmt.Errorf("ado: read the full message of commit %s in %s: %w", shortCommit(id), repo, err)
	}
	if full == nil {
		return deref(commit.Comment), nil
	}
	return deref(full.Comment), nil
}

// versionDescriptor describes a bound of a commit range. A full commit ID is used
// as such, anything else is treated as a tag name.
func versionDescriptor(ref string) *git.GitVersionDescriptor {
	versionType := git.GitVersionTypeValues.Tag
	if isCommitID(ref) {
		versionType = git.GitVersionTypeValues.Commit
	}
	return &git.GitVersionDescriptor{Version: &ref, VersionType: &versionType}
}
