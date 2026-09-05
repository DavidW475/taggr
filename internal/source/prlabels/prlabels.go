// Package prlabels determines the version bump from the labels of the pull
// request a release originates from.
//
// It works with every platform that can read pull request labels, so it is not
// tied to Azure DevOps: the platform is asked for the platform.LabelReader
// capability at run time.
package prlabels

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// Name is the name the bump source is registered and selected under.
const Name = "pr-labels"

var _ source.Source = (*Source)(nil)

func init() {
	source.Register(Name, New)
}

// Source maps the labels of a pull request to a version bump.
type Source struct {
	// Labels holds, per bump, the label names that request that bump. Matching is
	// case-insensitive.
	Labels map[version.Bump][]string
	// Default is the bump used when a pull request carries none of the labels.
	Default version.Bump
}

// DefaultLabels returns the label names recognised out of the box. The labels of
// BumpNone suppress the release entirely.
func DefaultLabels() map[version.Bump][]string {
	return map[version.Bump][]string{
		version.BumpMajor: {"major", "breaking", "breaking-change", "semver:major"},
		version.BumpMinor: {"minor", "feature", "enhancement", "semver:minor"},
		version.BumpPatch: {"patch", "fix", "bugfix", "semver:patch"},
		version.BumpNone:  {"no-release", "skip-release", "semver:none"},
	}
}

// New builds the bump source from its section of the configuration:
//
//	sources:
//	  pr-labels:
//	    default_bump: patch
//	    labels:
//	      major: [major, breaking-change]
//	      minor: [minor, feature]
//	      patch: [patch, fix]
//	      none:  [no-release]
//
// Every list that is configured replaces the built-in list for that bump; the
// lists that are left out keep their defaults.
func New(settings config.Settings) (source.Source, error) {
	s := &Source{Labels: DefaultLabels(), Default: version.BumpPatch}

	for _, bump := range []version.Bump{version.BumpNone, version.BumpPatch, version.BumpMinor, version.BumpMajor} {
		if configured := settings.StringSlice("labels." + bump.String()); len(configured) > 0 {
			s.Labels[bump] = configured
		}
	}

	if raw := settings.String("default_bump"); raw != "" {
		bump, err := version.ParseBump(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", settings.Key("default_bump"), err)
		}
		s.Default = bump
	}
	return s, nil
}

// Name returns the name the bump source is selected under.
func (s *Source) Name() string { return Name }

// Bump reads the labels of the pull request behind the release and returns the
// largest bump they request.
//
// When several bump labels are set the largest one wins, except for a label that
// suppresses the release: that one always wins, so a pull request can be excluded
// from a release explicitly. When no label matches, the configured default bump
// is used.
func (s *Source) Bump(ctx context.Context, req source.Request) (source.Result, error) {
	reader, ok := req.Platform.(platform.LabelReader)
	if !ok {
		return source.Result{}, fmt.Errorf("pr-labels: platform %q cannot read pull request labels", req.Platform.Name())
	}

	pullRequest, err := s.pullRequest(ctx, req)
	if err != nil {
		return source.Result{}, err
	}
	if pullRequest == "" {
		return source.Result{
			Bump:   version.BumpNone,
			Reason: fmt.Sprintf("commit %s belongs to no pull request", shortCommit(req.Commit)),
		}, nil
	}

	labels, err := reader.PullRequestLabels(ctx, req.Repository, pullRequest)
	if err != nil {
		return source.Result{}, err
	}

	bump, label, matched := s.match(labels)
	switch {
	case !matched:
		return source.Result{
			Bump:   s.Default,
			Reason: fmt.Sprintf("pull request %s carries no bump label (%s), using the default %s bump", pullRequest, describeLabels(labels), s.Default),
		}, nil
	case bump == version.BumpNone:
		return source.Result{
			Bump:   version.BumpNone,
			Reason: fmt.Sprintf("label %q on pull request %s suppresses the release", label, pullRequest),
		}, nil
	default:
		return source.Result{
			Bump:   bump,
			Reason: fmt.Sprintf("label %q on pull request %s requests a %s bump", label, pullRequest, bump),
		}, nil
	}
}

// pullRequest returns the pull request the bump is read from. Outside of a pull
// request build the ID is unknown, so a platform that can name the pull request a
// commit was merged by is asked for it — that is the case on the branch build
// that runs right after a merge.
func (s *Source) pullRequest(ctx context.Context, req source.Request) (string, error) {
	if id := strings.TrimSpace(req.PullRequest); id != "" {
		return id, nil
	}
	resolver, ok := req.Platform.(platform.PullRequestResolver)
	if !ok || req.Commit == "" {
		return "", nil
	}
	id, err := resolver.PullRequestForCommit(ctx, req.Repository, req.Commit)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(id), nil
}

// match returns the bump the labels request, the label that decided it, and
// whether any label matched at all. A label that suppresses the release outranks
// every other label; otherwise the largest bump wins.
func (s *Source) match(labels []string) (version.Bump, string, bool) {
	var (
		best      version.Bump
		bestLabel string
		matched   bool
	)
	for _, label := range labels {
		bump, ok := s.lookup(label)
		if !ok {
			continue
		}
		if bump == version.BumpNone {
			return version.BumpNone, label, true
		}
		if !matched || bump > best {
			best, bestLabel, matched = bump, label, true
		}
	}
	return best, bestLabel, matched
}

// lookup returns the bump a single label requests.
func (s *Source) lookup(label string) (version.Bump, bool) {
	label = strings.TrimSpace(label)
	for bump, names := range s.Labels {
		for _, name := range names {
			if strings.EqualFold(label, name) {
				return bump, true
			}
		}
	}
	return version.BumpNone, false
}

// describeLabels renders the labels seen on a pull request for the reason line.
func describeLabels(labels []string) string {
	cleaned := make([]string, 0, len(labels))
	for _, label := range labels {
		if label = strings.TrimSpace(label); label != "" {
			cleaned = append(cleaned, label)
		}
	}
	if len(cleaned) == 0 {
		return "no labels at all"
	}
	sort.Strings(cleaned)
	return "labels: " + strings.Join(cleaned, ", ")
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
