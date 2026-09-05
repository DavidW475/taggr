package ado

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/platform"
)

const (
	// tagRefPrefix is the ref namespace tags live in.
	tagRefPrefix = "refs/tags/"
	// refPageSize is the number of refs requested per call. Azure DevOps caps it
	// at 1000 and pages the rest behind a continuation token.
	refPageSize = 500
	// zeroObjectID is the old object ID that marks a ref update as "create".
	zeroObjectID = "0000000000000000000000000000000000000000"
)

// ListTags returns every tag of the repository, following the continuation token
// until Azure DevOps has handed out all of them.
func (p *Platform) ListTags(ctx context.Context, repo platform.Repository) ([]platform.Tag, error) {
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return nil, err
	}

	filter, peelTags, top := "tags/", true, refPageSize
	var (
		tags              []platform.Tag
		continuationToken string
	)
	for {
		args := git.GetRefsArgs{
			RepositoryId: &repo.Name,
			Project:      &project,
			Filter:       &filter,
			PeelTags:     &peelTags,
			Top:          &top,
		}
		if continuationToken != "" {
			args.ContinuationToken = &continuationToken
		}

		page, err := client.GetRefs(ctx, args)
		if err != nil {
			return nil, fmt.Errorf("ado: list tags of %s: %w", repo, err)
		}
		if page == nil || len(page.Value) == 0 {
			break
		}
		for _, ref := range page.Value {
			tags = append(tags, platform.Tag{
				Name:   strings.TrimPrefix(deref(ref.Name), tagRefPrefix),
				Commit: refCommit(ref),
			})
		}
		if page.ContinuationToken == "" {
			break
		}
		continuationToken = page.ContinuationToken
	}
	return tags, nil
}

// CreateTag creates an annotated tag when the tag carries a message and a
// lightweight tag otherwise.
func (p *Platform) CreateTag(ctx context.Context, repo platform.Repository, tag platform.Tag) error {
	if strings.TrimSpace(tag.Name) == "" {
		return fmt.Errorf("ado: cannot create a tag without a name")
	}
	if strings.TrimSpace(tag.Commit) == "" {
		return fmt.Errorf("ado: cannot create tag %s without a commit", tag.Name)
	}
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return err
	}

	if tag.Message != "" {
		_, err := client.CreateAnnotatedTag(ctx, git.CreateAnnotatedTagArgs{
			Project:      &project,
			RepositoryId: &repo.Name,
			TagObject: &git.GitAnnotatedTag{
				Name:         &tag.Name,
				Message:      &tag.Message,
				TaggedObject: &git.GitObject{ObjectId: &tag.Commit},
			},
		})
		if err != nil {
			return fmt.Errorf("ado: create annotated tag %s on %s: %w", tag.Name, repo, err)
		}
		return nil
	}

	name := tagRefPrefix + tag.Name
	oldObjectID := zeroObjectID
	updates := []git.GitRefUpdate{{
		Name:        &name,
		OldObjectId: &oldObjectID,
		NewObjectId: &tag.Commit,
	}}
	results, err := client.UpdateRefs(ctx, git.UpdateRefsArgs{
		RefUpdates:   &updates,
		RepositoryId: &repo.Name,
		Project:      &project,
	})
	if err != nil {
		return fmt.Errorf("ado: create tag %s on %s: %w", tag.Name, repo, err)
	}
	// A rejected ref update is reported inside the result, not as a transport
	// error, so the outcome has to be inspected explicitly.
	if results == nil || len(*results) == 0 {
		return fmt.Errorf("ado: create tag %s on %s: Azure DevOps returned no result", tag.Name, repo)
	}
	if result := (*results)[0]; result.Success == nil || !*result.Success {
		return fmt.Errorf("ado: create tag %s on %s: %s", tag.Name, repo, refUpdateFailure(result))
	}
	return nil
}

// ResolveCommit resolves a branch name, a tag name or a commit ID to the commit a
// tag would be created on. An empty ref resolves to the tip of the repository's
// default branch.
func (p *Platform) ResolveCommit(ctx context.Context, repo platform.Repository, ref string) (string, error) {
	client, project, err := p.target(ctx, repo)
	if err != nil {
		return "", err
	}

	ref = strings.TrimSpace(ref)
	if isCommitID(ref) {
		return strings.ToLower(ref), nil
	}
	if ref == "" {
		repository, err := client.GetRepository(ctx, git.GetRepositoryArgs{RepositoryId: &repo.Name, Project: &project})
		if err != nil {
			return "", fmt.Errorf("ado: read repository %s: %w", repo, err)
		}
		if repository == nil || deref(repository.DefaultBranch) == "" {
			return "", fmt.Errorf("ado: repository %s has no default branch, pass an explicit --ref", repo)
		}
		ref = deref(repository.DefaultBranch)
	}

	// Refs are filtered by prefix below "refs/", so a bare name is looked up as a
	// branch and a fully qualified ref is used as given.
	filter := strings.TrimPrefix(ref, "refs/")
	if !strings.HasPrefix(filter, "heads/") && !strings.HasPrefix(filter, "tags/") {
		filter = "heads/" + filter
	}

	peelTags := true
	page, err := client.GetRefs(ctx, git.GetRefsArgs{
		RepositoryId: &repo.Name,
		Project:      &project,
		Filter:       &filter,
		PeelTags:     &peelTags,
	})
	if err != nil {
		return "", fmt.Errorf("ado: resolve %q in %s: %w", ref, repo, err)
	}
	// The filter matches by prefix, so looking up "heads/main" also returns
	// "heads/main-fix": only the exact ref name counts.
	wanted := "refs/" + filter
	if page != nil {
		for _, candidate := range page.Value {
			if deref(candidate.Name) != wanted {
				continue
			}
			if commit := refCommit(candidate); commit != "" {
				return commit, nil
			}
		}
	}
	return "", fmt.Errorf("ado: %q does not exist in %s", ref, repo)
}

// refCommit returns the commit a ref points at. An annotated tag points at a tag
// object, whose peeled ID is the commit; every other ref points at the commit
// directly.
func refCommit(ref git.GitRef) string {
	if peeled := deref(ref.PeeledObjectId); peeled != "" {
		return peeled
	}
	return deref(ref.ObjectId)
}

// refUpdateFailure explains why Azure DevOps rejected a ref update.
func refUpdateFailure(result git.GitRefUpdateResult) string {
	if message := strings.TrimSpace(deref(result.CustomMessage)); message != "" {
		return message
	}
	if result.UpdateStatus != nil {
		if rejectedBy := deref(result.RejectedBy); rejectedBy != "" {
			return fmt.Sprintf("%s (rejected by %s)", *result.UpdateStatus, rejectedBy)
		}
		return string(*result.UpdateStatus)
	}
	return "the ref update was rejected"
}

// isCommitID reports whether s is a full 40 character commit ID, which can be
// used as-is instead of being looked up.
func isCommitID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
