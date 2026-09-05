// Package integration drives the whole program end to end: the real command tree
// resolves its settings, opens the registered platform and bump source, and the
// official Azure DevOps SDK talks HTTP to a server that emulates the API.
//
// Only the network is replaced, so these tests cover what the unit tests cannot:
// flag and configuration precedence, the wiring between planner, platform and
// bump source, the URLs and payloads the SDK produces, and the output a pipeline
// consumes.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidW475/taggr/cmd"
)

const (
	// mainCommit is the commit at the tip of the default branch.
	mainCommit = "4f2a91cd0123456789abcdef0123456789abcdef"
	// olderCommit is the commit the previous release was tagged on.
	olderCommit = "1111111111111111111111111111111111111111"
)

// result is the outcome of one taggr run.
type result struct {
	stdout string
	stderr string
	err    error
}

// run executes taggr with the given arguments against the server, the way the
// binary would, and captures both streams.
func run(t *testing.T, server *adoServer, args ...string) result {
	t.Helper()

	// The platform is configured through the environment, which is how a pipeline
	// supplies it, and exercises the nested environment key lookup. The token is
	// always the same one; a server expecting a different token is what an
	// authentication test uses.
	t.Setenv("TAGGR_PLATFORMS_AZUREDEVOPS_ORG_URL", server.OrgURL())
	t.Setenv("TAGGR_PLATFORMS_AZUREDEVOPS_TOKEN", clientToken)
	t.Setenv("TAGGR_PLATFORMS_AZUREDEVOPS_PROJECT", server.Project)

	var stdout, stderr bytes.Buffer
	root := cmd.NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(args, "--repository", server.Repository))

	err := root.ExecuteContext(context.Background())
	return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// withLabelledPullRequest returns a server holding one released version and a
// pull request carrying the given labels.
func withLabelledPullRequest(t *testing.T, labels ...string) *adoServer {
	t.Helper()

	server := newADOServer(t)
	server.Tags["v1.4.2"] = olderCommit
	server.Branches["main"] = mainCommit
	server.PullRequestForCommit[mainCommit] = 1421

	active := make([]ServerLabel, 0, len(labels))
	for _, label := range labels {
		active = append(active, ServerLabel{Name: label, Active: true})
	}
	server.Labels[1421] = active
	return server
}

func TestNextFromPullRequestLabels(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")

	got := run(t, server, "next")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "1.5.0" {
		t.Errorf("stdout = %q, want the bare version 1.5.0", got.stdout)
	}
	// next must not write anything to the platform.
	if created := server.Created(); len(created) != 0 {
		t.Errorf("next created %v, want nothing", created)
	}
}

func TestNextResolvesThePullRequestOfTheMergeCommit(t *testing.T) {
	server := withLabelledPullRequest(t, "breaking-change")

	got := run(t, server, "next", "--tag")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "v2.0.0" {
		t.Errorf("stdout = %q, want v2.0.0", got.stdout)
	}
	// No pull request ID was given, so it had to be looked up from the commit.
	if !server.Called("POST", "/pullRequestQuery") {
		t.Error("the pull request of the merge commit was never looked up")
	}
}

func TestTagCreatesAnAnnotatedTag(t *testing.T) {
	server := withLabelledPullRequest(t, "fix")

	got := run(t, server, "tag")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}

	created := server.Created()
	if len(created) != 1 {
		t.Fatalf("created %v, want exactly one tag", created)
	}
	if created[0].Name != "v1.4.3" {
		t.Errorf("tag name = %q, want v1.4.3", created[0].Name)
	}
	if created[0].Commit != mainCommit {
		t.Errorf("tag commit = %q, want the tip of the default branch", created[0].Commit)
	}
	if created[0].Message != "Release v1.4.3" {
		t.Errorf("tag message = %q, want the default release message", created[0].Message)
	}
	if !strings.Contains(got.stdout, "created tag v1.4.3") {
		t.Errorf("stdout = %q, want it to report the created tag", got.stdout)
	}
}

func TestTagDryRunCreatesNothing(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")

	got := run(t, server, "tag", "--dry-run")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if created := server.Created(); len(created) != 0 {
		t.Errorf("a dry run created %v, want nothing", created)
	}
	if !strings.Contains(got.stdout, "dry run: tag v1.5.0 was not created") {
		t.Errorf("stdout = %q, want the dry run notice", got.stdout)
	}
	// The plan still has to be resolved and shown in full.
	for _, want := range []string{"v1.4.2", "minor", "v1.5.0", "Payments/checkout"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", got.stdout, want)
		}
	}
}

func TestTagCreatesALightweightTag(t *testing.T) {
	server := withLabelledPullRequest(t, "fix")

	got := run(t, server, "tag", "--annotated=false")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if len(server.CreatedAnnotated) != 0 {
		t.Errorf("created annotated tags %v, want none", server.CreatedAnnotated)
	}
	if len(server.CreatedLightweight) != 1 || server.CreatedLightweight[0].Name != "v1.4.3" {
		t.Errorf("lightweight tags = %v, want exactly v1.4.3", server.CreatedLightweight)
	}
}

func TestTagReportsARejectedRefUpdate(t *testing.T) {
	server := withLabelledPullRequest(t, "fix")
	server.RejectTagCreation = "the tag is protected by policy"

	got := run(t, server, "tag", "--annotated=false")
	if got.err == nil {
		t.Fatal("taggr tag succeeded although the ref update was rejected")
	}
	// Azure DevOps answers a rejection with 200 and success=false, so the reason
	// has to be dug out of the response body.
	if !strings.Contains(got.err.Error(), "the tag is protected by policy") {
		t.Errorf("error = %v, want the reason from the API", got.err)
	}
}

func TestNoReleaseDueLeavesTheRepositoryAlone(t *testing.T) {
	server := withLabelledPullRequest(t, "feature", "no-release")

	got := run(t, server, "tag")
	// A suppressed release is a success: a pipeline step must not fail over it.
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if created := server.Created(); len(created) != 0 {
		t.Errorf("created %v although the release was suppressed", created)
	}
	if !strings.Contains(got.stdout, "no release due") {
		t.Errorf("stdout = %q, want the reason to be reported", got.stdout)
	}
}

func TestFirstReleaseStartsAtTheInitialVersion(t *testing.T) {
	server := newADOServer(t)
	server.Branches["main"] = mainCommit
	server.PullRequestForCommit[mainCommit] = 1421
	server.Labels[1421] = []ServerLabel{{Name: "feature", Active: true}}

	got := run(t, server, "tag")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}
	created := server.Created()
	if len(created) != 1 || created[0].Name != "v0.1.0" {
		t.Fatalf("created %v, want the initial version v0.1.0", created)
	}
}

func TestNextFromConventionalCommits(t *testing.T) {
	server := newADOServer(t)
	server.Tags["v1.4.2"] = olderCommit
	server.Branches["main"] = mainCommit
	server.Commits = []ServerCommit{
		{ID: "aaa", Message: "chore: bump dependencies"},
		{ID: "bbb", Message: "feat(api): add pagination"},
		{ID: "ccc", Message: "fix: correct the rounding"},
	}

	got := run(t, server, "next", "--source", "conventional-commits")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	// feat outranks fix and chore.
	if strings.TrimSpace(got.stdout) != "1.5.0" {
		t.Errorf("stdout = %q, want 1.5.0", got.stdout)
	}
}

func TestConventionalCommitsFindABreakingChangeInATruncatedMessage(t *testing.T) {
	server := newADOServer(t)
	server.Tags["v1.4.2"] = olderCommit
	server.Branches["main"] = mainCommit
	server.Commits = []ServerCommit{{
		ID:      "aaa",
		Message: "feat: rework the API\n\nBREAKING CHANGE: the v1 endpoint is gone",
		// Listing commits only returns the subject, hiding the footer.
		TruncatedIn: "feat: rework the API",
	}}

	got := run(t, server, "next", "--source", "conventional-commits")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "2.0.0" {
		t.Errorf("stdout = %q, want 2.0.0: the truncated footer has to be fetched", got.stdout)
	}
	if !server.Called("GET", "/commits/aaa") {
		t.Error("the full message of the truncated commit was never fetched")
	}
}

func TestConventionalCommitsWithoutAReleaseWorthyChange(t *testing.T) {
	server := newADOServer(t)
	server.Tags["v1.4.2"] = olderCommit
	server.Branches["main"] = mainCommit
	server.Commits = []ServerCommit{
		{ID: "aaa", Message: "chore: bump dependencies"},
		{ID: "bbb", Message: "Update the readme"},
	}

	got := run(t, server, "tag", "--source", "conventional-commits")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if created := server.Created(); len(created) != 0 {
		t.Errorf("created %v, want nothing without a release worthy commit", created)
	}
}

func TestTagsArePagedThroughTheContinuationToken(t *testing.T) {
	server := withLabelledPullRequest(t, "fix")
	server.Tags["v1.9.0"] = olderCommit
	server.Tags["v1.10.0"] = olderCommit
	server.Tags["v1.10.1"] = olderCommit
	server.Tags["not-a-version"] = olderCommit
	server.TagPageSize = 2

	got := run(t, server, "next")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	// The highest version only shows up on a later page, and 1.10.1 outranks
	// 1.9.0 numerically rather than alphabetically.
	if strings.TrimSpace(got.stdout) != "1.10.2" {
		t.Errorf("stdout = %q, want 1.10.2", got.stdout)
	}
}

func TestAnnotatedTagsResolveToTheirPeeledCommit(t *testing.T) {
	server := withLabelledPullRequest(t, "fix")
	// The ref of an annotated tag points at the tag object, not at the commit.
	server.AnnotatedTags["v1.4.2"] = olderCommit

	got := run(t, server, "next", "--output", "json")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	var plan struct {
		CurrentTag string `json:"current_tag"`
		NextTag    string `json:"next_tag"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &plan); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, got.stdout)
	}
	if plan.CurrentTag != "v1.4.2" || plan.NextTag != "v1.4.3" {
		t.Errorf("plan = %+v, want the annotated tag to be read as v1.4.2", plan)
	}
}

func TestJSONOutputCarriesTheWholePlan(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")

	got := run(t, server, "tag", "--output", "json")
	if got.err != nil {
		t.Fatalf("taggr tag failed: %v (stderr: %s)", got.err, got.stderr)
	}

	var plan struct {
		Platform       string `json:"platform"`
		Source         string `json:"source"`
		Repository     string `json:"repository"`
		Commit         string `json:"commit"`
		CurrentVersion string `json:"current_version"`
		Bump           string `json:"bump"`
		Reason         string `json:"reason"`
		NextVersion    string `json:"next_version"`
		NextTag        string `json:"next_tag"`
		ReleaseDue     bool   `json:"release_due"`
		Created        bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &plan); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, got.stdout)
	}

	if plan.Platform != "azuredevops" || plan.Source != "pr-labels" {
		t.Errorf("platform/source = %s/%s, want azuredevops/pr-labels", plan.Platform, plan.Source)
	}
	if plan.Repository != "Payments/checkout" || plan.Commit != mainCommit {
		t.Errorf("repository/commit = %s/%s, want the resolved ones", plan.Repository, plan.Commit)
	}
	if plan.CurrentVersion != "1.4.2" || plan.NextVersion != "1.5.0" || plan.NextTag != "v1.5.0" {
		t.Errorf("versions = %+v, want 1.4.2 -> 1.5.0", plan)
	}
	if plan.Bump != "minor" || !plan.ReleaseDue || !plan.Created {
		t.Errorf("plan = %+v, want a created minor release", plan)
	}
	if !strings.Contains(plan.Reason, "feature") {
		t.Errorf("reason = %q, want it to name the deciding label", plan.Reason)
	}
}

func TestConfigFileSuppliesTheSettings(t *testing.T) {
	server := withLabelledPullRequest(t, "story")

	// The label is unknown by default, so only the config file can make it count.
	configFile := writeConfig(t, `
platform: azuredevops
source: pr-labels
sources:
  pr-labels:
    default_bump: none
    labels:
      minor: [story]
tag:
  prefix: "release-"
`)

	got := run(t, server, "next", "--tag", "--config", configFile)
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	// No release-prefixed tag exists yet, so this is a first release.
	if strings.TrimSpace(got.stdout) != "release-0.1.0" {
		t.Errorf("stdout = %q, want release-0.1.0", got.stdout)
	}
}

func TestFlagsWinOverTheConfigFile(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")
	configFile := writeConfig(t, "tag:\n  prefix: \"release-\"\n")

	got := run(t, server, "next", "--tag", "--config", configFile, "--prefix", "v")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	// With the prefix from the flag the existing v1.4.2 is found and bumped.
	if strings.TrimSpace(got.stdout) != "v1.5.0" {
		t.Errorf("stdout = %q, want the flag to win over the config file", got.stdout)
	}
}

func TestPipelineVariablesSupplyTheContext(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")
	// A pull request build: the ID comes from the environment, so no lookup by
	// merge commit is needed.
	t.Setenv("SYSTEM_PULLREQUEST_PULLREQUESTID", "1421")
	t.Setenv("BUILD_SOURCEVERSION", mainCommit)

	got := run(t, server, "next")
	if got.err != nil {
		t.Fatalf("taggr next failed: %v (stderr: %s)", got.err, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "1.5.0" {
		t.Errorf("stdout = %q, want 1.5.0", got.stdout)
	}
	if server.Called("POST", "/pullRequestQuery") {
		t.Error("the pull request was looked up although the environment named it")
	}
}

func TestBadCredentialsAreReported(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")
	server.Token = "a-different-token"

	got := run(t, server, "next")
	if got.err == nil {
		t.Fatal("taggr next succeeded with a wrong token")
	}
	if !strings.Contains(got.err.Error(), "not authorized") {
		t.Errorf("error = %v, want the authorisation failure to be reported", got.err)
	}
}

func TestUnknownRefIsReported(t *testing.T) {
	server := withLabelledPullRequest(t, "feature")

	got := run(t, server, "next", "--ref", "does-not-exist")
	if got.err == nil {
		t.Fatal("taggr next succeeded for a branch that does not exist")
	}
	if !strings.Contains(got.err.Error(), "does-not-exist") {
		t.Errorf("error = %v, want it to name the missing ref", got.err)
	}
}

// writeConfig writes a config file into the test's temporary directory.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".taggr.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config file failed: %v", err)
	}
	return path
}
