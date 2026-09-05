package release

import (
	"context"
	"strings"
	"testing"

	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// fakePlatform records what a plan asked for and what it created.
type fakePlatform struct {
	tags    []platform.Tag
	commit  string
	created []platform.Tag
}

func (f *fakePlatform) Name() string { return "fake" }

func (f *fakePlatform) ListTags(context.Context, platform.Repository) ([]platform.Tag, error) {
	return f.tags, nil
}

func (f *fakePlatform) CreateTag(_ context.Context, _ platform.Repository, tag platform.Tag) error {
	f.created = append(f.created, tag)
	return nil
}

func (f *fakePlatform) ResolveCommit(context.Context, platform.Repository, string) (string, error) {
	return f.commit, nil
}

// fakeSource returns a fixed bump.
type fakeSource struct {
	bump   version.Bump
	reason string
	seen   source.Request
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) Bump(_ context.Context, req source.Request) (source.Result, error) {
	f.seen = req
	return source.Result{Bump: f.bump, Reason: f.reason}, nil
}

func tags(names ...string) []platform.Tag {
	out := make([]platform.Tag, 0, len(names))
	for _, name := range names {
		out = append(out, platform.Tag{Name: name, Commit: "0123456789abcdef0123456789abcdef01234567"})
	}
	return out
}

func newPlanner(plat *fakePlatform, src *fakeSource) *Planner {
	return NewPlanner(plat, src, Options{
		Prefix:         "v",
		InitialVersion: version.Version{Minor: 1},
	})
}

func TestPlanBumpsTheHighestVersionTag(t *testing.T) {
	// Deliberately unordered, with tags that are not versions at all.
	plat := &fakePlatform{
		tags:   tags("v1.4.2", "v1.10.0", "v1.9.3", "release-candidate", "v2.0.0-rc.1"),
		commit: "4f2a91cd",
	}
	plan, err := newPlanner(plat, &fakeSource{bump: version.BumpMinor, reason: "a label said so"}).
		Plan(context.Background(), Request{Repository: platform.Repository{Owner: "Payments", Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}

	// v2.0.0-rc.1 outranks v1.10.0, and a prerelease is a legitimate current version.
	if plan.CurrentTag != "v2.0.0-rc.1" {
		t.Errorf("CurrentTag = %q, want v2.0.0-rc.1", plan.CurrentTag)
	}
	if plan.NextTag != "v2.1.0" {
		t.Errorf("NextTag = %q, want v2.1.0", plan.NextTag)
	}
	if plan.Commit != "4f2a91cd" {
		t.Errorf("Commit = %q, want the resolved commit", plan.Commit)
	}
	if !plan.ReleaseDue() {
		t.Error("ReleaseDue = false, want true")
	}
}

func TestPlanOnlyConsidersTagsWithThePrefix(t *testing.T) {
	plat := &fakePlatform{tags: tags("v1.0.0", "9.9.9"), commit: "abc"}
	plan, err := newPlanner(plat, &fakeSource{bump: version.BumpPatch}).
		Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}
	if plan.NextTag != "v1.0.1" {
		t.Errorf("NextTag = %q, want v1.0.1: the unprefixed tag must be ignored", plan.NextTag)
	}
}

func TestPlanStartsAtTheInitialVersion(t *testing.T) {
	plan, err := newPlanner(&fakePlatform{commit: "abc"}, &fakeSource{bump: version.BumpMajor, reason: "a label said so"}).
		Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}
	if plan.HasCurrentVersion {
		t.Error("HasCurrentVersion = true, want false without any tag")
	}
	// The first release starts at the initial version instead of bumping 0.0.0.
	if plan.NextTag != "v0.1.0" {
		t.Errorf("NextTag = %q, want v0.1.0", plan.NextTag)
	}
	if !strings.Contains(plan.Reason, "no version tag exists yet") {
		t.Errorf("Reason = %q, want an explanation of the initial version", plan.Reason)
	}
}

func TestPlanWithoutABumpReleasesNothing(t *testing.T) {
	plat := &fakePlatform{tags: tags("v1.4.2"), commit: "abc"}
	plan, err := newPlanner(plat, &fakeSource{bump: version.BumpNone, reason: "no-release label"}).
		Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}
	if plan.ReleaseDue() {
		t.Error("ReleaseDue = true, want false")
	}
	if plan.NextTag != "v1.4.2" {
		t.Errorf("NextTag = %q, want the unchanged current tag", plan.NextTag)
	}
}

func TestPlanRerunAfterAReleaseMovesOn(t *testing.T) {
	// Running twice for the same pull request must not produce the tag again: the
	// second run starts from the version the first one created.
	plat := &fakePlatform{tags: tags("v1.4.2"), commit: "abc"}
	planner := newPlanner(plat, &fakeSource{bump: version.BumpMinor})

	first, err := planner.Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}
	if err := planner.Apply(context.Background(), first, "Release "+first.NextTag); err != nil {
		t.Fatalf("Apply returned an unexpected error: %v", err)
	}
	plat.tags = append(plat.tags, plat.created...)

	second, err := planner.Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}
	if second.CurrentTag != first.NextTag {
		t.Errorf("CurrentTag = %q, want the tag the first run created (%s)", second.CurrentTag, first.NextTag)
	}
	if second.NextTag != "v1.6.0" {
		t.Errorf("NextTag = %q, want v1.6.0", second.NextTag)
	}
}

func TestPlanPassesTheContextToTheSource(t *testing.T) {
	plat := &fakePlatform{tags: tags("v1.4.2"), commit: "4f2a91cd"}
	src := &fakeSource{bump: version.BumpPatch}
	repo := platform.Repository{Owner: "Payments", Name: "checkout"}

	if _, err := newPlanner(plat, src).Plan(context.Background(), Request{Repository: repo, PullRequest: "1421"}); err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}

	if src.seen.Commit != "4f2a91cd" {
		t.Errorf("source saw commit %q, want the resolved one", src.seen.Commit)
	}
	if src.seen.PullRequest != "1421" {
		t.Errorf("source saw pull request %q, want 1421", src.seen.PullRequest)
	}
	if src.seen.PreviousTag != "v1.4.2" {
		t.Errorf("source saw previous tag %q, want v1.4.2", src.seen.PreviousTag)
	}
	if src.seen.Platform == nil {
		t.Error("source received no platform, it cannot ask for capabilities")
	}
}

func TestApplyCreatesTheTag(t *testing.T) {
	plat := &fakePlatform{tags: tags("v1.4.2"), commit: "4f2a91cd"}
	planner := newPlanner(plat, &fakeSource{bump: version.BumpMinor})
	plan, err := planner.Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}

	if err := planner.Apply(context.Background(), plan, "Release v1.5.0"); err != nil {
		t.Fatalf("Apply returned an unexpected error: %v", err)
	}
	if len(plat.created) != 1 {
		t.Fatalf("created %d tags, want 1", len(plat.created))
	}
	if got := plat.created[0]; got.Name != "v1.5.0" || got.Commit != "4f2a91cd" || got.Message != "Release v1.5.0" {
		t.Errorf("created %+v, want the planned annotated tag", got)
	}
}

func TestApplyRefusesWhenNoReleaseIsDue(t *testing.T) {
	plat := &fakePlatform{tags: tags("v1.4.2"), commit: "abc"}
	planner := newPlanner(plat, &fakeSource{bump: version.BumpNone})
	plan, err := planner.Plan(context.Background(), Request{Repository: platform.Repository{Name: "checkout"}})
	if err != nil {
		t.Fatalf("Plan returned an unexpected error: %v", err)
	}

	if err := planner.Apply(context.Background(), plan, "irrelevant"); err == nil {
		t.Fatal("Apply succeeded without a due release, want an error")
	}
	if len(plat.created) != 0 {
		t.Errorf("created %d tags, want none", len(plat.created))
	}
}
