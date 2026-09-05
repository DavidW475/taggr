package prlabels

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// fakePlatform is a platform that only does what the pr-labels source needs.
type fakePlatform struct {
	labels          []string
	labelsErr       error
	pullRequestFor  map[string]string
	requestedPullID string
}

func (f *fakePlatform) Name() string { return "fake" }

func (f *fakePlatform) ListTags(context.Context, platform.Repository) ([]platform.Tag, error) {
	return nil, nil
}

func (f *fakePlatform) CreateTag(context.Context, platform.Repository, platform.Tag) error {
	return nil
}

func (f *fakePlatform) ResolveCommit(_ context.Context, _ platform.Repository, ref string) (string, error) {
	return ref, nil
}

func (f *fakePlatform) PullRequestLabels(_ context.Context, _ platform.Repository, pullRequest string) ([]string, error) {
	f.requestedPullID = pullRequest
	return f.labels, f.labelsErr
}

func (f *fakePlatform) PullRequestForCommit(_ context.Context, _ platform.Repository, commit string) (string, error) {
	return f.pullRequestFor[commit], nil
}

func newSource(t *testing.T) *Source {
	t.Helper()
	s, err := New(config.Settings{})
	if err != nil {
		t.Fatalf("New returned an unexpected error: %v", err)
	}
	return s.(*Source)
}

func TestBumpFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   version.Bump
	}{
		{name: "patch label", labels: []string{"fix"}, want: version.BumpPatch},
		{name: "minor label", labels: []string{"feature"}, want: version.BumpMinor},
		{name: "major label", labels: []string{"breaking-change"}, want: version.BumpMajor},
		{name: "matching is case insensitive", labels: []string{"Breaking-Change"}, want: version.BumpMajor},
		{name: "unrelated labels fall back to the default", labels: []string{"documentation"}, want: version.BumpPatch},
		{name: "no labels fall back to the default", labels: nil, want: version.BumpPatch},
		{name: "the largest bump wins", labels: []string{"fix", "breaking", "feature"}, want: version.BumpMajor},
		{name: "a suppressing label beats every bump", labels: []string{"feature", "no-release"}, want: version.BumpNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plat := &fakePlatform{labels: tc.labels}
			result, err := newSource(t).Bump(context.Background(), source.Request{
				Platform:    plat,
				PullRequest: "1421",
				Commit:      "4f2a91cd",
			})
			if err != nil {
				t.Fatalf("Bump returned an unexpected error: %v", err)
			}
			if result.Bump != tc.want {
				t.Errorf("Bump = %s, want %s", result.Bump, tc.want)
			}
			if result.Reason == "" {
				t.Error("Bump returned an empty reason, the decision has to be explainable")
			}
		})
	}
}

func TestBumpFindsThePullRequestOfAMergedCommit(t *testing.T) {
	plat := &fakePlatform{
		labels:         []string{"feature"},
		pullRequestFor: map[string]string{"4f2a91cd": "1421"},
	}

	// No pull request ID is known, as on the branch build after a merge.
	result, err := newSource(t).Bump(context.Background(), source.Request{
		Platform: plat,
		Commit:   "4f2a91cd",
	})
	if err != nil {
		t.Fatalf("Bump returned an unexpected error: %v", err)
	}
	if result.Bump != version.BumpMinor {
		t.Errorf("Bump = %s, want minor", result.Bump)
	}
	if plat.requestedPullID != "1421" {
		t.Errorf("labels were read for pull request %q, want 1421", plat.requestedPullID)
	}
}

func TestBumpWithoutAPullRequestReleasesNothing(t *testing.T) {
	result, err := newSource(t).Bump(context.Background(), source.Request{
		Platform: &fakePlatform{pullRequestFor: map[string]string{}},
		Commit:   "4f2a91cd",
	})
	if err != nil {
		t.Fatalf("Bump returned an unexpected error: %v", err)
	}
	if result.Bump != version.BumpNone {
		t.Errorf("Bump = %s, want none for a commit without a pull request", result.Bump)
	}
}

func TestBumpFailsOnAPlatformThatCannotReadLabels(t *testing.T) {
	// A platform value that does not implement platform.LabelReader at all.
	var plat platform.Platform = &labelLessPlatform{}
	_, err := newSource(t).Bump(context.Background(), source.Request{Platform: plat, PullRequest: "7"})
	if err == nil {
		t.Fatal("Bump succeeded on a platform without label support, want an error")
	}
	if !strings.Contains(err.Error(), "cannot read pull request labels") {
		t.Errorf("error %q does not explain the missing capability", err)
	}
}

// labelLessPlatform implements the mandatory platform interface and nothing else.
type labelLessPlatform struct{}

func (labelLessPlatform) Name() string { return "labelless" }
func (labelLessPlatform) ListTags(context.Context, platform.Repository) ([]platform.Tag, error) {
	return nil, nil
}
func (labelLessPlatform) CreateTag(context.Context, platform.Repository, platform.Tag) error {
	return nil
}
func (labelLessPlatform) ResolveCommit(context.Context, platform.Repository, string) (string, error) {
	return "", nil
}

func TestBumpPropagatesPlatformErrors(t *testing.T) {
	want := errors.New("the pull request does not exist")
	_, err := newSource(t).Bump(context.Background(), source.Request{
		Platform:    &fakePlatform{labelsErr: want},
		PullRequest: "1421",
	})
	if !errors.Is(err, want) {
		t.Errorf("Bump returned %v, want the platform error to be passed through", err)
	}
}

func TestNewReadsConfiguration(t *testing.T) {
	values := map[string]any{
		"sources.pr-labels.default_bump":  "none",
		"sources.pr-labels.labels.major":  []string{"api-break"},
		"sources.pr-labels.labels.minor":  "story, feature",
		"sources.pr-labels.default_extra": "ignored",
	}
	settings := config.New("sources.pr-labels", func(key string) any { return values[key] })

	built, err := New(settings)
	if err != nil {
		t.Fatalf("New returned an unexpected error: %v", err)
	}
	s := built.(*Source)

	if s.Default != version.BumpNone {
		t.Errorf("Default = %s, want none", s.Default)
	}
	if got, want := s.Labels[version.BumpMajor], []string{"api-break"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("major labels = %v, want %v", got, want)
	}
	// A comma separated string is what an environment variable can carry.
	if got := s.Labels[version.BumpMinor]; len(got) != 2 || got[0] != "story" || got[1] != "feature" {
		t.Errorf("minor labels = %v, want [story feature]", got)
	}
	// Lists that were not configured keep their defaults.
	if got := s.Labels[version.BumpPatch]; len(got) == 0 {
		t.Error("patch labels lost their defaults")
	}
}

func TestNewRejectsAnUnknownDefaultBump(t *testing.T) {
	settings := config.New("sources.pr-labels", func(key string) any {
		if key == "sources.pr-labels.default_bump" {
			return "gigantic"
		}
		return nil
	})
	if _, err := New(settings); err == nil {
		t.Fatal("New accepted an unknown default bump, want an error")
	}
}
