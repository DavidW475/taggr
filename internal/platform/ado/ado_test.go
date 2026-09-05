package ado

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
)

// fakeGit is a gitClient whose responses each test sets up for itself. A call
// nobody prepared fails the test instead of returning a zero value.
type fakeGit struct {
	t                    *testing.T
	getRefs              func(git.GetRefsArgs) (*git.GetRefsResponseValue, error)
	updateRefs           func(git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error)
	createAnnotatedTag   func(git.CreateAnnotatedTagArgs) (*git.GitAnnotatedTag, error)
	getRepository        func(git.GetRepositoryArgs) (*git.GitRepository, error)
	getPullRequestLabels func(git.GetPullRequestLabelsArgs) (*[]core.WebApiTagDefinition, error)
	getPullRequestQuery  func(git.GetPullRequestQueryArgs) (*git.GitPullRequestQuery, error)
	getCommits           func(git.GetCommitsArgs) (*[]git.GitCommitRef, error)
	getCommit            func(git.GetCommitArgs) (*git.GitCommit, error)
}

func (f *fakeGit) GetCommits(_ context.Context, args git.GetCommitsArgs) (*[]git.GitCommitRef, error) {
	if f.getCommits == nil {
		f.t.Fatal("unexpected call to GetCommits")
	}
	return f.getCommits(args)
}

func (f *fakeGit) GetCommit(_ context.Context, args git.GetCommitArgs) (*git.GitCommit, error) {
	if f.getCommit == nil {
		f.t.Fatal("unexpected call to GetCommit")
	}
	return f.getCommit(args)
}

func (f *fakeGit) GetRefs(_ context.Context, args git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
	if f.getRefs == nil {
		f.t.Fatal("unexpected call to GetRefs")
	}
	return f.getRefs(args)
}

func (f *fakeGit) UpdateRefs(_ context.Context, args git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error) {
	if f.updateRefs == nil {
		f.t.Fatal("unexpected call to UpdateRefs")
	}
	return f.updateRefs(args)
}

func (f *fakeGit) CreateAnnotatedTag(_ context.Context, args git.CreateAnnotatedTagArgs) (*git.GitAnnotatedTag, error) {
	if f.createAnnotatedTag == nil {
		f.t.Fatal("unexpected call to CreateAnnotatedTag")
	}
	return f.createAnnotatedTag(args)
}

func (f *fakeGit) GetRepository(_ context.Context, args git.GetRepositoryArgs) (*git.GitRepository, error) {
	if f.getRepository == nil {
		f.t.Fatal("unexpected call to GetRepository")
	}
	return f.getRepository(args)
}

func (f *fakeGit) GetPullRequestLabels(_ context.Context, args git.GetPullRequestLabelsArgs) (*[]core.WebApiTagDefinition, error) {
	if f.getPullRequestLabels == nil {
		f.t.Fatal("unexpected call to GetPullRequestLabels")
	}
	return f.getPullRequestLabels(args)
}

func (f *fakeGit) GetPullRequestQuery(_ context.Context, args git.GetPullRequestQueryArgs) (*git.GitPullRequestQuery, error) {
	if f.getPullRequestQuery == nil {
		f.t.Fatal("unexpected call to GetPullRequestQuery")
	}
	return f.getPullRequestQuery(args)
}

func ptr[T any](v T) *T { return &v }

// newTestPlatform returns a platform with the connection already in place, so
// that no test needs a live organisation.
func newTestPlatform(client gitClient) *Platform {
	return &Platform{
		OrgURL:  "https://dev.azure.com/contoso",
		Project: "Payments",
		Token:   "token",
		client:  client,
	}
}

var testRepo = platform.Repository{Owner: "Payments", Name: "checkout"}

func TestListTagsFollowsTheContinuationToken(t *testing.T) {
	pages := 0
	client := &fakeGit{t: t, getRefs: func(args git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
		pages++
		if pages == 1 {
			if args.ContinuationToken != nil {
				t.Error("the first page must be requested without a continuation token")
			}
			return &git.GetRefsResponseValue{
				Value:             []git.GitRef{{Name: ptr("refs/tags/v1.4.2"), ObjectId: ptr("aaa")}},
				ContinuationToken: "page-2",
			}, nil
		}
		if deref(args.ContinuationToken) != "page-2" {
			t.Errorf("second page requested with token %q, want page-2", deref(args.ContinuationToken))
		}
		return &git.GetRefsResponseValue{
			Value: []git.GitRef{{Name: ptr("refs/tags/v1.5.0"), ObjectId: ptr("bbb")}},
		}, nil
	}}

	tags, err := newTestPlatform(client).ListTags(context.Background(), testRepo)
	if err != nil {
		t.Fatalf("ListTags returned an unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want both pages", len(tags))
	}
	if tags[0].Name != "v1.4.2" || tags[1].Name != "v1.5.0" {
		t.Errorf("got %v, want the ref prefix stripped from both names", tags)
	}
}

func TestListTagsUsesThePeeledCommitOfAnAnnotatedTag(t *testing.T) {
	client := &fakeGit{t: t, getRefs: func(args git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
		if deref(args.Filter) != "tags/" {
			t.Errorf("tags requested with filter %q, want tags/", deref(args.Filter))
		}
		return &git.GetRefsResponseValue{Value: []git.GitRef{{
			Name:           ptr("refs/tags/v1.4.2"),
			ObjectId:       ptr("tagobject"),
			PeeledObjectId: ptr("commit"),
		}}}, nil
	}}

	tags, err := newTestPlatform(client).ListTags(context.Background(), testRepo)
	if err != nil {
		t.Fatalf("ListTags returned an unexpected error: %v", err)
	}
	// The object ID of an annotated tag is the tag object, not the commit.
	if tags[0].Commit != "commit" {
		t.Errorf("Commit = %q, want the peeled commit", tags[0].Commit)
	}
}

func TestCreateTagCreatesAnAnnotatedTag(t *testing.T) {
	var got git.CreateAnnotatedTagArgs
	client := &fakeGit{t: t, createAnnotatedTag: func(args git.CreateAnnotatedTagArgs) (*git.GitAnnotatedTag, error) {
		got = args
		return &git.GitAnnotatedTag{}, nil
	}}

	err := newTestPlatform(client).CreateTag(context.Background(), testRepo, platform.Tag{
		Name:    "v1.5.0",
		Commit:  "4f2a91cd",
		Message: "Release v1.5.0",
	})
	if err != nil {
		t.Fatalf("CreateTag returned an unexpected error: %v", err)
	}
	if deref(got.Project) != "Payments" || deref(got.RepositoryId) != "checkout" {
		t.Errorf("tag created in %s/%s, want Payments/checkout", deref(got.Project), deref(got.RepositoryId))
	}
	if got.TagObject == nil || deref(got.TagObject.Name) != "v1.5.0" || deref(got.TagObject.Message) != "Release v1.5.0" {
		t.Fatalf("tag object = %+v, want the planned name and message", got.TagObject)
	}
	if got.TagObject.TaggedObject == nil || deref(got.TagObject.TaggedObject.ObjectId) != "4f2a91cd" {
		t.Error("the annotated tag does not point at the planned commit")
	}
}

func TestCreateTagCreatesALightweightTagWithoutAMessage(t *testing.T) {
	var got git.UpdateRefsArgs
	client := &fakeGit{t: t, updateRefs: func(args git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error) {
		got = args
		return &[]git.GitRefUpdateResult{{Success: ptr(true)}}, nil
	}}

	err := newTestPlatform(client).CreateTag(context.Background(), testRepo, platform.Tag{Name: "v1.5.0", Commit: "4f2a91cd"})
	if err != nil {
		t.Fatalf("CreateTag returned an unexpected error: %v", err)
	}
	if got.RefUpdates == nil || len(*got.RefUpdates) != 1 {
		t.Fatalf("ref updates = %+v, want exactly one", got.RefUpdates)
	}
	update := (*got.RefUpdates)[0]
	if deref(update.Name) != "refs/tags/v1.5.0" {
		t.Errorf("ref name = %q, want the fully qualified tag ref", deref(update.Name))
	}
	// The zero object ID is what turns the update into a creation.
	if deref(update.OldObjectId) != zeroObjectID {
		t.Errorf("old object ID = %q, want the zero ID", deref(update.OldObjectId))
	}
	if deref(update.NewObjectId) != "4f2a91cd" {
		t.Errorf("new object ID = %q, want the planned commit", deref(update.NewObjectId))
	}
}

func TestCreateTagReportsARejectedRefUpdate(t *testing.T) {
	client := &fakeGit{t: t, updateRefs: func(git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error) {
		// Azure DevOps reports a rejection in the result, not as an error.
		return &[]git.GitRefUpdateResult{{
			Success:       ptr(false),
			UpdateStatus:  ptr(git.GitRefUpdateStatus("rejectedByPolicy")),
			CustomMessage: ptr("the tag is protected"),
		}}, nil
	}}

	err := newTestPlatform(client).CreateTag(context.Background(), testRepo, platform.Tag{Name: "v1.5.0", Commit: "4f2a91cd"})
	if err == nil {
		t.Fatal("CreateTag succeeded although the update was rejected, want an error")
	}
	if !strings.Contains(err.Error(), "the tag is protected") {
		t.Errorf("error %q does not carry the reason from Azure DevOps", err)
	}
}

func TestResolveCommitMatchesTheExactRef(t *testing.T) {
	client := &fakeGit{t: t, getRefs: func(args git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
		if deref(args.Filter) != "heads/main" {
			t.Errorf("refs filtered by %q, want heads/main", deref(args.Filter))
		}
		// The filter matches by prefix, so similar branches come back too.
		return &git.GetRefsResponseValue{Value: []git.GitRef{
			{Name: ptr("refs/heads/main-fix"), ObjectId: ptr("wrong")},
			{Name: ptr("refs/heads/main"), ObjectId: ptr("right")},
		}}, nil
	}}

	commit, err := newTestPlatform(client).ResolveCommit(context.Background(), testRepo, "main")
	if err != nil {
		t.Fatalf("ResolveCommit returned an unexpected error: %v", err)
	}
	if commit != "right" {
		t.Errorf("commit = %q, want the exact branch match", commit)
	}
}

func TestResolveCommitFallsBackToTheDefaultBranch(t *testing.T) {
	client := &fakeGit{
		t: t,
		getRepository: func(git.GetRepositoryArgs) (*git.GitRepository, error) {
			return &git.GitRepository{DefaultBranch: ptr("refs/heads/trunk")}, nil
		},
		getRefs: func(args git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
			if deref(args.Filter) != "heads/trunk" {
				t.Errorf("refs filtered by %q, want heads/trunk", deref(args.Filter))
			}
			return &git.GetRefsResponseValue{Value: []git.GitRef{{Name: ptr("refs/heads/trunk"), ObjectId: ptr("tip")}}}, nil
		},
	}

	commit, err := newTestPlatform(client).ResolveCommit(context.Background(), testRepo, "")
	if err != nil {
		t.Fatalf("ResolveCommit returned an unexpected error: %v", err)
	}
	if commit != "tip" {
		t.Errorf("commit = %q, want the tip of the default branch", commit)
	}
}

func TestResolveCommitAcceptsACommitIDWithoutAskingTheAPI(t *testing.T) {
	// Every call on this client fails the test, so a lookup would be visible.
	const id = "4F2A91CD0123456789ABCDEF0123456789ABCDEF"

	commit, err := newTestPlatform(&fakeGit{t: t}).ResolveCommit(context.Background(), testRepo, id)
	if err != nil {
		t.Fatalf("ResolveCommit returned an unexpected error: %v", err)
	}
	if commit != strings.ToLower(id) {
		t.Errorf("commit = %q, want the normalised commit ID", commit)
	}
}

func TestResolveCommitFailsOnAnUnknownRef(t *testing.T) {
	client := &fakeGit{t: t, getRefs: func(git.GetRefsArgs) (*git.GetRefsResponseValue, error) {
		return &git.GetRefsResponseValue{}, nil
	}}

	if _, err := newTestPlatform(client).ResolveCommit(context.Background(), testRepo, "gone"); err == nil {
		t.Fatal("ResolveCommit succeeded for a ref that does not exist, want an error")
	}
}

func TestPullRequestLabelsSkipsInactiveOnes(t *testing.T) {
	client := &fakeGit{t: t, getPullRequestLabels: func(args git.GetPullRequestLabelsArgs) (*[]core.WebApiTagDefinition, error) {
		if args.PullRequestId == nil || *args.PullRequestId != 1421 {
			t.Errorf("labels read for pull request %v, want 1421", args.PullRequestId)
		}
		return &[]core.WebApiTagDefinition{
			{Name: ptr("feature"), Active: ptr(true)},
			{Name: ptr("breaking"), Active: ptr(false)},
			{Name: ptr("documentation")},
		}, nil
	}}

	labels, err := newTestPlatform(client).PullRequestLabels(context.Background(), testRepo, "1421")
	if err != nil {
		t.Fatalf("PullRequestLabels returned an unexpected error: %v", err)
	}
	// A label that was removed again stays on the pull request as inactive.
	if len(labels) != 2 || labels[0] != "feature" || labels[1] != "documentation" {
		t.Errorf("labels = %v, want the active ones only", labels)
	}
}

func TestPullRequestLabelsRejectsANonNumericID(t *testing.T) {
	if _, err := newTestPlatform(&fakeGit{t: t}).PullRequestLabels(context.Background(), testRepo, "PR-7"); err == nil {
		t.Fatal("PullRequestLabels accepted a non-numeric ID, want an error")
	}
}

func TestPullRequestForCommit(t *testing.T) {
	const commit = "4f2a91cd0123456789abcdef0123456789abcdef"
	client := &fakeGit{t: t, getPullRequestQuery: func(args git.GetPullRequestQueryArgs) (*git.GitPullRequestQuery, error) {
		queries := *args.Queries.Queries
		if got := *queries[0].Type; got != git.GitPullRequestQueryTypeValues.LastMergeCommit {
			t.Errorf("query type = %q, want lastMergeCommit", got)
		}
		return &git.GitPullRequestQuery{Results: &[]map[string][]git.GitPullRequest{
			// Azure DevOps echoes the commit in its own casing.
			{strings.ToUpper(commit): {{PullRequestId: ptr(1421)}}},
		}}, nil
	}}

	id, err := newTestPlatform(client).PullRequestForCommit(context.Background(), testRepo, commit)
	if err != nil {
		t.Fatalf("PullRequestForCommit returned an unexpected error: %v", err)
	}
	if id != "1421" {
		t.Errorf("pull request = %q, want 1421", id)
	}
}

func TestPullRequestForCommitWithoutAMatch(t *testing.T) {
	client := &fakeGit{t: t, getPullRequestQuery: func(git.GetPullRequestQueryArgs) (*git.GitPullRequestQuery, error) {
		return &git.GitPullRequestQuery{Results: &[]map[string][]git.GitPullRequest{{}}}, nil
	}}

	id, err := newTestPlatform(client).PullRequestForCommit(context.Background(), testRepo, "4f2a91cd")
	if err != nil {
		t.Fatalf("PullRequestForCommit returned an unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("pull request = %q, want an empty ID for a commit without one", id)
	}
}

func TestNewRequiresTheOrganisationURL(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "token")

	_, err := New(config.New("platforms.azuredevops", func(string) any { return nil }))
	if err == nil {
		t.Fatal("New succeeded without an organisation URL, want an error")
	}
	// The message has to name both ways of supplying the value.
	if !strings.Contains(err.Error(), "platforms.azuredevops.org_url") ||
		!strings.Contains(err.Error(), "TAGGR_PLATFORMS_AZUREDEVOPS_ORG_URL") {
		t.Errorf("error %q does not explain how to set the organisation URL", err)
	}
}

func TestNewTakesTheTokenFromThePipeline(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("SYSTEM_ACCESSTOKEN", "pipeline-token")

	values := map[string]any{"platforms.azuredevops.org_url": "https://dev.azure.com/contoso/"}
	built, err := New(config.New("platforms.azuredevops", func(key string) any { return values[key] }))
	if err != nil {
		t.Fatalf("New returned an unexpected error: %v", err)
	}

	p := built.(*Platform)
	if p.Token != "pipeline-token" {
		t.Errorf("Token = %q, want the pipeline access token", p.Token)
	}
	if p.OrgURL != "https://dev.azure.com/contoso" {
		t.Errorf("OrgURL = %q, want the trailing slash removed", p.OrgURL)
	}
}

func TestNewFailsWithoutTheToken(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("SYSTEM_ACCESSTOKEN", "")

	values := map[string]any{"platforms.azuredevops.org_url": "https://dev.azure.com/contoso"}
	if _, err := New(config.New("platforms.azuredevops", func(key string) any { return values[key] })); err == nil {
		t.Fatal("New succeeded without a token, want an error")
	}
}

func TestDetectEnvironmentReadsThePipelineVariables(t *testing.T) {
	t.Setenv(envProject, "Payments")
	t.Setenv(envRepository, "checkout")
	t.Setenv(envCommit, "4f2a91cd")
	t.Setenv(envPullRequest, "1421")

	env := newTestPlatform(&fakeGit{t: t}).DetectEnvironment()
	if env.Repository.Owner != "Payments" || env.Repository.Name != "checkout" {
		t.Errorf("repository = %s, want Payments/checkout", env.Repository)
	}
	if env.Ref != "4f2a91cd" {
		t.Errorf("Ref = %q, want the build commit", env.Ref)
	}
	if env.PullRequest != "1421" {
		t.Errorf("PullRequest = %q, want 1421", env.PullRequest)
	}
}

func TestDetectEnvironmentFallsBackToTheConfiguredProject(t *testing.T) {
	t.Setenv(envProject, "")
	t.Setenv(envRepository, "checkout")
	t.Setenv(envCommit, "")
	t.Setenv(envPullRequest, "")

	env := newTestPlatform(&fakeGit{t: t}).DetectEnvironment()
	if env.Repository.Owner != "Payments" {
		t.Errorf("Owner = %q, want the project from the configuration", env.Repository.Owner)
	}
}

func TestTargetRequiresARepositoryAndProject(t *testing.T) {
	p := newTestPlatform(&fakeGit{t: t})
	if _, _, err := p.target(context.Background(), platform.Repository{}); err == nil {
		t.Error("target succeeded without a repository name, want an error")
	}

	p.Project = ""
	if _, _, err := p.target(context.Background(), platform.Repository{Name: "checkout"}); err == nil {
		t.Error("target succeeded without a project, want an error")
	}
}
