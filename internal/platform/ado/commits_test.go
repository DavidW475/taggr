package ado

import (
	"context"
	"fmt"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/DavidW475/taggr/internal/platform"
)

func TestCommitsBoundsTheRangeByThePreviousRelease(t *testing.T) {
	var got git.GitQueryCommitsCriteria
	client := &fakeGit{t: t, getCommits: func(args git.GetCommitsArgs) (*[]git.GitCommitRef, error) {
		got = *args.SearchCriteria
		return &[]git.GitCommitRef{{CommitId: ptr("aaa"), Comment: ptr("feat: add pagination")}}, nil
	}}

	commits, err := newTestPlatform(client).Commits(context.Background(), testRepo, platform.CommitRange{
		From: "v1.4.2",
		To:   "4f2a91cd0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("Commits returned an unexpected error: %v", err)
	}
	if len(commits) != 1 || commits[0].Message != "feat: add pagination" {
		t.Fatalf("commits = %+v, want the single commit with its message", commits)
	}

	// Azure DevOps walks backwards from compareVersion and stops at itemVersion.
	if got.ItemVersion == nil || deref(got.ItemVersion.Version) != "v1.4.2" {
		t.Errorf("itemVersion = %+v, want the previous tag as lower bound", got.ItemVersion)
	}
	if got.ItemVersion != nil && *got.ItemVersion.VersionType != git.GitVersionTypeValues.Tag {
		t.Errorf("itemVersion type = %v, want tag", *got.ItemVersion.VersionType)
	}
	if got.CompareVersion == nil || deref(got.CompareVersion.Version) != "4f2a91cd0123456789abcdef0123456789abcdef" {
		t.Errorf("compareVersion = %+v, want the released commit as upper bound", got.CompareVersion)
	}
	if got.CompareVersion != nil && *got.CompareVersion.VersionType != git.GitVersionTypeValues.Commit {
		t.Errorf("compareVersion type = %v, want commit", *got.CompareVersion.VersionType)
	}
}

func TestCommitsWithoutAPreviousReleaseWalksTheWholeHistory(t *testing.T) {
	var got git.GitQueryCommitsCriteria
	client := &fakeGit{t: t, getCommits: func(args git.GetCommitsArgs) (*[]git.GitCommitRef, error) {
		got = *args.SearchCriteria
		return &[]git.GitCommitRef{{CommitId: ptr("aaa"), Comment: ptr("feat: first")}}, nil
	}}

	_, err := newTestPlatform(client).Commits(context.Background(), testRepo, platform.CommitRange{To: "abc"})
	if err != nil {
		t.Fatalf("Commits returned an unexpected error: %v", err)
	}
	if got.CompareVersion != nil {
		t.Error("compareVersion was set although there is no previous release")
	}
	if got.ItemVersion == nil || deref(got.ItemVersion.Version) != "abc" {
		t.Errorf("itemVersion = %+v, want the released commit", got.ItemVersion)
	}
}

func TestCommitsPagesUntilThePagesRunOut(t *testing.T) {
	calls := 0
	client := &fakeGit{t: t, getCommits: func(args git.GetCommitsArgs) (*[]git.GitCommitRef, error) {
		calls++
		if calls > 1 {
			if *args.Skip != commitPageSize {
				t.Errorf("second page requested with skip %d, want %d", *args.Skip, commitPageSize)
			}
			return &[]git.GitCommitRef{{CommitId: ptr("last"), Comment: ptr("fix: last one")}}, nil
		}
		// A full page means there may be more.
		full := make([]git.GitCommitRef, commitPageSize)
		for i := range full {
			full[i] = git.GitCommitRef{CommitId: ptr(fmt.Sprintf("c%d", i)), Comment: ptr("chore: noise")}
		}
		return &full, nil
	}}

	commits, err := newTestPlatform(client).Commits(context.Background(), testRepo, platform.CommitRange{To: "abc"})
	if err != nil {
		t.Fatalf("Commits returned an unexpected error: %v", err)
	}
	if len(commits) != commitPageSize+1 {
		t.Errorf("got %d commits, want both pages", len(commits))
	}
	if calls != 2 {
		t.Errorf("made %d calls, want exactly two pages", calls)
	}
}

func TestCommitsFetchesTruncatedMessagesInFull(t *testing.T) {
	const full = "feat: add pagination\n\nBREAKING CHANGE: the v1 endpoint is gone"
	fetched := 0
	client := &fakeGit{
		t: t,
		getCommits: func(git.GetCommitsArgs) (*[]git.GitCommitRef, error) {
			return &[]git.GitCommitRef{
				{CommitId: ptr("aaa"), Comment: ptr("feat: add pagination"), CommentTruncated: ptr(true)},
				{CommitId: ptr("bbb"), Comment: ptr("fix: a typo")},
			}, nil
		},
		getCommit: func(args git.GetCommitArgs) (*git.GitCommit, error) {
			fetched++
			if deref(args.CommitId) != "aaa" {
				t.Errorf("fetched commit %q, want the truncated one", deref(args.CommitId))
			}
			return &git.GitCommit{Comment: ptr(full)}, nil
		},
	}

	commits, err := newTestPlatform(client).Commits(context.Background(), testRepo, platform.CommitRange{To: "abc"})
	if err != nil {
		t.Fatalf("Commits returned an unexpected error: %v", err)
	}
	// A truncated message would hide the breaking change footer.
	if commits[0].Message != full {
		t.Errorf("message = %q, want the full message", commits[0].Message)
	}
	if fetched != 1 {
		t.Errorf("fetched %d full messages, want only the truncated one", fetched)
	}
}

func TestCommitsRequiresATargetCommit(t *testing.T) {
	if _, err := newTestPlatform(&fakeGit{t: t}).Commits(context.Background(), testRepo, platform.CommitRange{}); err == nil {
		t.Fatal("Commits succeeded without a target commit, want an error")
	}
}
