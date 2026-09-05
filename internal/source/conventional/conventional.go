// Package conventional determines the version bump from commit messages that
// follow the Conventional Commits specification (https://www.conventionalcommits.org).
//
// Like every bump source it is platform independent: it asks the active platform
// for the platform.CommitReader capability and reads the commits between the
// previous release and the commit being released.
package conventional

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// Name is the name the bump source is registered and selected under.
const Name = "conventional-commits"

// defaultStripPattern removes the prefix Azure DevOps puts in front of the title
// of a squashed pull request, so that "Merged PR 1421: feat: add pagination" is
// read as the conventional commit it wraps.
const defaultStripPattern = `^Merged PR \d+: `

var _ source.Source = (*Source)(nil)

func init() {
	source.Register(Name, New)
}

// header matches the first line of a conventional commit:
// type, optional scope in parentheses, an optional "!" marking a breaking change,
// then the description.
var header = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)(?:\(([^()]*)\))?(!)?: *(.+)$`)

// breakingFooter matches the footer that marks a breaking change. The
// specification writes it in upper case; taggr accepts any case, because a
// release must not be missed over a typo in capitalisation.
var breakingFooter = regexp.MustCompile(`(?im)^BREAKING[ -]CHANGE[ ]*:`)

// Source maps the commits of a release to a version bump.
type Source struct {
	// Types holds, per bump, the commit types that request that bump. Matching is
	// case-insensitive.
	Types map[version.Bump][]string
	// Default is the bump used when no commit requests one.
	Default version.Bump
	// Strip removes a wrapping prefix from the subject before it is parsed. It is
	// nil when no prefix is stripped.
	Strip *regexp.Regexp
}

// DefaultTypes returns the commit types recognised out of the box. Breaking
// changes are not a type: they are marked with a "!" after the type or with a
// BREAKING CHANGE footer, and always produce a major bump.
func DefaultTypes() map[version.Bump][]string {
	return map[version.Bump][]string{
		version.BumpMajor: {},
		version.BumpMinor: {"feat"},
		version.BumpPatch: {"fix", "perf"},
		version.BumpNone:  {"chore", "docs", "style", "refactor", "test", "build", "ci", "revert"},
	}
}

// New builds the bump source from its section of the configuration:
//
//	sources:
//	  conventional-commits:
//	    default_bump: none
//	    strip_pattern: '^Merged PR \d+: '
//	    types:
//	      minor: [feat]
//	      patch: [fix, perf]
//	      none:  [chore, docs, refactor]
//
// Every list that is configured replaces the built-in list for that bump; the
// lists that are left out keep their defaults. A commit type in none of the lists
// requests no bump.
func New(settings config.Settings) (source.Source, error) {
	s := &Source{Types: DefaultTypes(), Default: version.BumpNone}

	for _, bump := range []version.Bump{version.BumpNone, version.BumpPatch, version.BumpMinor, version.BumpMajor} {
		if configured := settings.StringSlice("types." + bump.String()); len(configured) > 0 {
			s.Types[bump] = configured
		}
	}

	if raw := settings.String("default_bump"); raw != "" {
		bump, err := version.ParseBump(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", settings.Key("default_bump"), err)
		}
		s.Default = bump
	}

	// An explicitly empty pattern switches the stripping off, which is different
	// from leaving the setting out and keeping the default.
	pattern := defaultStripPattern
	if settings.Get("strip_pattern") != nil {
		pattern = settings.String("strip_pattern")
	}
	if pattern != "" {
		strip, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", settings.Key("strip_pattern"), err)
		}
		s.Strip = strip
	}
	return s, nil
}

// Name returns the name the bump source is selected under.
func (s *Source) Name() string { return Name }

// Bump reads the commits since the previous release and returns the largest bump
// they request.
//
// A commit that does not follow the specification is ignored, which makes the
// source usable in a repository that adopted the convention only recently. When
// no commit requests a bump the configured default is used, and when there are no
// commits at all no release is due.
func (s *Source) Bump(ctx context.Context, req source.Request) (source.Result, error) {
	reader, ok := req.Platform.(platform.CommitReader)
	if !ok {
		return source.Result{}, fmt.Errorf("conventional-commits: platform %q cannot read commits", req.Platform.Name())
	}

	commits, err := reader.Commits(ctx, req.Repository, platform.CommitRange{From: req.PreviousTag, To: req.Commit})
	if err != nil {
		return source.Result{}, err
	}
	if len(commits) == 0 {
		return source.Result{
			Bump:   version.BumpNone,
			Reason: fmt.Sprintf("no commits since %s", describeSince(req.PreviousTag)),
		}, nil
	}

	var (
		best       version.Bump
		bestCommit platform.Commit
		bestType   string
		breaking   bool
		conforming int
	)
	for _, commit := range commits {
		bump, commitType, isBreaking, ok := s.classify(commit)
		if !ok {
			continue
		}
		conforming++
		if bump > best {
			best, bestCommit, bestType, breaking = bump, commit, commitType, isBreaking
		}
	}

	if best == version.BumpNone {
		return source.Result{
			Bump:   s.Default,
			Reason: s.explainNoBump(len(commits), conforming),
		}, nil
	}
	return source.Result{
		Bump:   best,
		Reason: explainBump(bestCommit, bestType, best, breaking, len(commits), conforming),
	}, nil
}

// classify returns the bump a single commit requests, the type it declared and
// whether it is a breaking change. ok is false for a commit that does not follow
// the specification.
func (s *Source) classify(commit platform.Commit) (bump version.Bump, commitType string, breaking, ok bool) {
	subject := commit.Subject()
	if s.Strip != nil {
		subject = s.Strip.ReplaceAllString(subject, "")
	}

	match := header.FindStringSubmatch(strings.TrimSpace(subject))
	if match == nil {
		return version.BumpNone, "", false, false
	}
	commitType = match[1]

	// A breaking change outranks whatever the type alone would request.
	if match[3] == "!" || breakingFooter.MatchString(commit.Message) {
		return version.BumpMajor, commitType, true, true
	}
	return s.lookup(commitType), commitType, false, true
}

// lookup returns the bump a commit type requests. A type in none of the lists
// requests no bump.
func (s *Source) lookup(commitType string) version.Bump {
	for bump, types := range s.Types {
		for _, candidate := range types {
			if strings.EqualFold(commitType, candidate) {
				return bump
			}
		}
	}
	return version.BumpNone
}

// explainBump describes the commit that decided the bump.
func explainBump(commit platform.Commit, commitType string, bump version.Bump, breaking bool, total, conforming int) string {
	what := fmt.Sprintf("type %q", commitType)
	if breaking {
		what = fmt.Sprintf("breaking change in type %q", commitType)
	}
	return fmt.Sprintf("commit %s (%s) requests a %s bump: %q [%s]",
		shortCommit(commit.ID), what, bump, ellipsis(commit.Subject(), 60), countConforming(total, conforming))
}

// explainNoBump describes why nothing requested a release.
func (s *Source) explainNoBump(total, conforming int) string {
	if conforming == 0 {
		return fmt.Sprintf("none of the %s follows the conventional commits format, using the default %s bump",
			plural(total, "commit"), s.Default)
	}
	return fmt.Sprintf("no commit requests a release [%s], using the default %s bump",
		countConforming(total, conforming), s.Default)
}

// countConforming renders how much of the range followed the convention.
func countConforming(total, conforming int) string {
	return fmt.Sprintf("%d of %s conventional", conforming, plural(total, "commit"))
}

// describeSince names the lower bound of the commit range.
func describeSince(previousTag string) string {
	if previousTag == "" {
		return "the start of the history"
	}
	return previousTag
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ellipsis shortens a subject line so that a reason stays on one line.
func ellipsis(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// shortCommit abbreviates a commit ID for human readable messages.
func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	if commit == "" {
		return "(unknown)"
	}
	return commit
}
