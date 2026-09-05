package conventional

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

// fakePlatform serves a fixed list of commits.
type fakePlatform struct {
	commits    []platform.Commit
	commitsErr error
	seen       platform.CommitRange
}

func (f *fakePlatform) Name() string { return "fake" }

func (f *fakePlatform) ListTags(context.Context, platform.Repository) ([]platform.Tag, error) {
	return nil, nil
}

func (f *fakePlatform) CreateTag(context.Context, platform.Repository, platform.Tag) error {
	return nil
}

func (f *fakePlatform) ResolveCommit(context.Context, platform.Repository, string) (string, error) {
	return "", nil
}

func (f *fakePlatform) Commits(_ context.Context, _ platform.Repository, r platform.CommitRange) ([]platform.Commit, error) {
	f.seen = r
	return f.commits, f.commitsErr
}

func newSource(t *testing.T) *Source {
	t.Helper()
	s, err := New(config.Settings{})
	if err != nil {
		t.Fatalf("New returned an unexpected error: %v", err)
	}
	return s.(*Source)
}

func commits(subjects ...string) []platform.Commit {
	out := make([]platform.Commit, 0, len(subjects))
	for i, subject := range subjects {
		out = append(out, platform.Commit{ID: string(rune('a'+i)) + "0123456", Message: subject})
	}
	return out
}

func bumpOf(t *testing.T, s *Source, cs []platform.Commit) source.Result {
	t.Helper()
	result, err := s.Bump(context.Background(), source.Request{
		Platform: &fakePlatform{commits: cs},
		Commit:   "4f2a91cd",
	})
	if err != nil {
		t.Fatalf("Bump returned an unexpected error: %v", err)
	}
	return result
}

func TestBumpFromCommitTypes(t *testing.T) {
	tests := []struct {
		name    string
		commits []platform.Commit
		want    version.Bump
	}{
		{name: "feat is a minor bump", commits: commits("feat: add pagination"), want: version.BumpMinor},
		{name: "fix is a patch bump", commits: commits("fix: correct the rounding"), want: version.BumpPatch},
		{name: "perf is a patch bump", commits: commits("perf: cache the lookup"), want: version.BumpPatch},
		{name: "chore requests nothing", commits: commits("chore: bump dependencies"), want: version.BumpNone},
		{name: "a scope is allowed", commits: commits("feat(api): add pagination"), want: version.BumpMinor},
		{name: "the type is case insensitive", commits: commits("Feat: add pagination"), want: version.BumpMinor},
		{name: "the largest bump wins", commits: commits("fix: a typo", "feat: add pagination", "chore: tidy"), want: version.BumpMinor},
		{name: "an unknown type requests nothing", commits: commits("wip: still working"), want: version.BumpNone},
		{name: "a non conventional commit is ignored", commits: commits("Update readme"), want: version.BumpNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bumpOf(t, newSource(t), tc.commits)
			if result.Bump != tc.want {
				t.Errorf("Bump = %s, want %s", result.Bump, tc.want)
			}
			if result.Reason == "" {
				t.Error("Bump returned an empty reason, the decision has to be explainable")
			}
		})
	}
}

func TestBumpDetectsBreakingChanges(t *testing.T) {
	tests := []struct {
		name   string
		commit platform.Commit
	}{
		{name: "marker after the type", commit: platform.Commit{ID: "a", Message: "feat!: drop the v1 endpoint"}},
		{name: "marker after the scope", commit: platform.Commit{ID: "a", Message: "feat(api)!: drop the v1 endpoint"}},
		{name: "footer", commit: platform.Commit{ID: "a", Message: "feat: rework the API\n\nBREAKING CHANGE: the v1 endpoint is gone"}},
		{name: "hyphenated footer", commit: platform.Commit{ID: "a", Message: "fix: rework\n\nBREAKING-CHANGE: gone"}},
		// A patch type with a breaking marker is still a major bump.
		{name: "breaking fix", commit: platform.Commit{ID: "a", Message: "fix!: reject invalid input"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bumpOf(t, newSource(t), []platform.Commit{tc.commit})
			if result.Bump != version.BumpMajor {
				t.Errorf("Bump = %s, want major for %q", result.Bump, tc.commit.Message)
			}
			if !strings.Contains(result.Reason, "breaking") {
				t.Errorf("Reason = %q, want it to mention the breaking change", result.Reason)
			}
		})
	}
}

func TestBumpIgnoresABreakingWordInThePlainBody(t *testing.T) {
	// Only the footer counts, not a mention somewhere in the prose.
	result := bumpOf(t, newSource(t), []platform.Commit{{
		ID:      "a",
		Message: "fix: guard the parser\n\nThis avoids a BREAKING CHANGE: for downstream users.",
	}})
	if result.Bump != version.BumpPatch {
		t.Errorf("Bump = %s, want patch: the keyword has to start a footer line", result.Bump)
	}
}

func TestBumpStripsTheAzureDevOpsMergePrefix(t *testing.T) {
	// This is what a squashed pull request looks like in Azure Repos.
	result := bumpOf(t, newSource(t), commits("Merged PR 1421: feat: add pagination"))
	if result.Bump != version.BumpMinor {
		t.Errorf("Bump = %s, want minor for a squashed pull request", result.Bump)
	}
}

func TestBumpWithoutCommitsReleasesNothing(t *testing.T) {
	s := newSource(t)
	s.Default = version.BumpPatch // even a default must not release an empty range

	result, err := s.Bump(context.Background(), source.Request{
		Platform:    &fakePlatform{},
		PreviousTag: "v1.4.2",
	})
	if err != nil {
		t.Fatalf("Bump returned an unexpected error: %v", err)
	}
	if result.Bump != version.BumpNone {
		t.Errorf("Bump = %s, want none for an empty range", result.Bump)
	}
	if !strings.Contains(result.Reason, "v1.4.2") {
		t.Errorf("Reason = %q, want it to name the previous release", result.Reason)
	}
}

func TestBumpUsesTheDefaultWhenNothingRequestsARelease(t *testing.T) {
	s := newSource(t)
	s.Default = version.BumpPatch

	result := bumpOf(t, s, commits("chore: tidy up", "docs: fix a typo"))
	if result.Bump != version.BumpPatch {
		t.Errorf("Bump = %s, want the configured default", result.Bump)
	}
}

func TestBumpAsksForTheRangeSinceThePreviousRelease(t *testing.T) {
	plat := &fakePlatform{commits: commits("feat: add pagination")}
	_, err := newSource(t).Bump(context.Background(), source.Request{
		Platform:    plat,
		Commit:      "4f2a91cd",
		PreviousTag: "v1.4.2",
	})
	if err != nil {
		t.Fatalf("Bump returned an unexpected error: %v", err)
	}
	if plat.seen.From != "v1.4.2" || plat.seen.To != "4f2a91cd" {
		t.Errorf("range = %+v, want it bounded by the previous tag and the released commit", plat.seen)
	}
}

func TestBumpFailsOnAPlatformThatCannotReadCommits(t *testing.T) {
	var plat platform.Platform = &noCommitPlatform{}
	_, err := newSource(t).Bump(context.Background(), source.Request{Platform: plat})
	if err == nil {
		t.Fatal("Bump succeeded on a platform without commit support, want an error")
	}
	if !strings.Contains(err.Error(), "cannot read commits") {
		t.Errorf("error %q does not explain the missing capability", err)
	}
}

// noCommitPlatform implements the mandatory platform interface and nothing else.
type noCommitPlatform struct{}

func (noCommitPlatform) Name() string { return "nocommits" }
func (noCommitPlatform) ListTags(context.Context, platform.Repository) ([]platform.Tag, error) {
	return nil, nil
}
func (noCommitPlatform) CreateTag(context.Context, platform.Repository, platform.Tag) error {
	return nil
}
func (noCommitPlatform) ResolveCommit(context.Context, platform.Repository, string) (string, error) {
	return "", nil
}

func TestBumpPropagatesPlatformErrors(t *testing.T) {
	want := errors.New("the branch is gone")
	_, err := newSource(t).Bump(context.Background(), source.Request{Platform: &fakePlatform{commitsErr: want}})
	if !errors.Is(err, want) {
		t.Errorf("Bump returned %v, want the platform error to be passed through", err)
	}
}

func TestNewReadsConfiguration(t *testing.T) {
	values := map[string]any{
		"sources.conventional-commits.default_bump":  "patch",
		"sources.conventional-commits.types.minor":   []string{"story"},
		"sources.conventional-commits.types.patch":   "bug, hotfix",
		"sources.conventional-commits.strip_pattern": "",
	}
	settings := config.New("sources.conventional-commits", func(key string) any { return values[key] })

	built, err := New(settings)
	if err != nil {
		t.Fatalf("New returned an unexpected error: %v", err)
	}
	s := built.(*Source)

	if s.Default != version.BumpPatch {
		t.Errorf("Default = %s, want patch", s.Default)
	}
	if got := s.Types[version.BumpMinor]; len(got) != 1 || got[0] != "story" {
		t.Errorf("minor types = %v, want [story]", got)
	}
	if got := s.Types[version.BumpPatch]; len(got) != 2 || got[0] != "bug" || got[1] != "hotfix" {
		t.Errorf("patch types = %v, want [bug hotfix]", got)
	}
	// An empty pattern switches the prefix stripping off.
	if s.Strip != nil {
		t.Error("Strip was set although the pattern is empty")
	}
	// Lists that were not configured keep their defaults.
	if len(s.Types[version.BumpNone]) == 0 {
		t.Error("the none types lost their defaults")
	}
}

func TestNewRejectsABrokenStripPattern(t *testing.T) {
	settings := config.New("sources.conventional-commits", func(key string) any {
		if key == "sources.conventional-commits.strip_pattern" {
			return "([unclosed"
		}
		return nil
	})
	if _, err := New(settings); err == nil {
		t.Fatal("New accepted an invalid regular expression, want an error")
	}
}

func TestSubjectStopsAtTheFirstLine(t *testing.T) {
	c := platform.Commit{Message: "feat: add pagination\n\nA longer explanation follows."}
	if got := c.Subject(); got != "feat: add pagination" {
		t.Errorf("Subject() = %q, want only the first line", got)
	}
}
