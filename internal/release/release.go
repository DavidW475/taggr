// Package release turns the state of a repository and the bump a source
// determined into the plan for the next release, and applies that plan.
//
// The planner is the only place that knows the order of the steps — resolve the
// commit, read the latest version tag, ask the bump source, compute the next
// version — which is what lets the commands stay thin and lets platforms and
// bump sources be swapped freely.
package release

import (
	"context"
	"fmt"
	"strings"

	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/source"
	"github.com/DavidW475/taggr/internal/version"
)

// Options controls how tag names are built and where versioning starts.
type Options struct {
	// Prefix is put in front of the version to form the tag name, e.g. "v" for
	// the tag v1.4.2. Only tags carrying this prefix are considered when the
	// current version is determined.
	Prefix string
	// InitialVersion is released when the repository has no version tag yet.
	InitialVersion version.Version
}

// Request describes what a plan is computed for.
type Request struct {
	// Repository is the repository to release.
	Repository platform.Repository
	// Ref is the branch, tag or commit to put the new tag on. An empty ref means
	// the tip of the repository's default branch.
	Ref string
	// PullRequest is the pull request the release originates from, if known.
	PullRequest string
}

// Plan is the outcome of the version resolution: what is released, from which
// version, and why.
type Plan struct {
	// Repository is the repository the plan applies to.
	Repository platform.Repository
	// Commit is the commit the new tag would point at.
	Commit string
	// CurrentTag is the tag holding the current version, empty on a first release.
	CurrentTag string
	// CurrentVersion is the highest version currently tagged.
	CurrentVersion version.Version
	// HasCurrentVersion reports whether the repository has a version tag at all.
	HasCurrentVersion bool
	// Bump is the increment the source determined.
	Bump version.Bump
	// Reason is the source's one-line explanation of the bump.
	Reason string
	// NextVersion is the version to release. It equals CurrentVersion when no
	// release is due.
	NextVersion version.Version
	// NextTag is the tag name to create for NextVersion.
	NextTag string
}

// ReleaseDue reports whether the plan actually releases something.
func (p *Plan) ReleaseDue() bool { return p.Bump != version.BumpNone }

// Planner computes and applies release plans for one platform and bump source.
type Planner struct {
	platform platform.Platform
	source   source.Source
	options  Options
}

// NewPlanner combines a platform and a bump source into a planner.
func NewPlanner(p platform.Platform, s source.Source, options Options) *Planner {
	return &Planner{platform: p, source: s, options: options}
}

// Platform returns the platform the planner works against.
func (p *Planner) Platform() platform.Platform { return p.platform }

// Source returns the bump source the planner asks.
func (p *Planner) Source() source.Source { return p.source }

// Plan resolves the commit to tag, reads the current version from the platform's
// tags and asks the bump source how large the next increment should be. It only
// reads: nothing is created until Apply is called.
func (p *Planner) Plan(ctx context.Context, req Request) (*Plan, error) {
	commit, err := p.platform.ResolveCommit(ctx, req.Repository, req.Ref)
	if err != nil {
		return nil, err
	}

	tags, err := p.platform.ListTags(ctx, req.Repository)
	if err != nil {
		return nil, err
	}
	currentVersion, currentTag, hasCurrent := latest(tags, p.options.Prefix)

	result, err := p.source.Bump(ctx, source.Request{
		Platform:    p.platform,
		Repository:  req.Repository,
		Commit:      commit,
		PullRequest: req.PullRequest,
		PreviousTag: currentTag,
	})
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Repository:        req.Repository,
		Commit:            commit,
		CurrentTag:        currentTag,
		CurrentVersion:    currentVersion,
		HasCurrentVersion: hasCurrent,
		Bump:              result.Bump,
		Reason:            result.Reason,
		NextVersion:       currentVersion,
		NextTag:           currentTag,
	}
	if !plan.ReleaseDue() {
		return plan, nil
	}

	// The first release of a repository starts at the configured initial version
	// instead of bumping the implicit 0.0.0.
	if hasCurrent {
		plan.NextVersion = currentVersion.Bump(result.Bump)
	} else {
		plan.NextVersion = p.options.InitialVersion
		plan.Reason = fmt.Sprintf("%s; no version tag exists yet, starting at %s", result.Reason, plan.NextVersion)
	}
	plan.NextTag = p.options.Prefix + plan.NextVersion.String()

	// Bumping the highest version cannot collide today, but a bump source that
	// names an absolute version could, and failing here is clearer than letting
	// the platform reject the tag.
	if existing, ok := find(tags, plan.NextTag); ok {
		return nil, fmt.Errorf("release: tag %s already exists on commit %s", existing.Name, existing.Commit)
	}
	return plan, nil
}

// Apply creates the planned tag. An empty message creates a lightweight tag, any
// other message an annotated one.
func (p *Planner) Apply(ctx context.Context, plan *Plan, message string) error {
	if plan == nil {
		return fmt.Errorf("release: no plan to apply")
	}
	if !plan.ReleaseDue() {
		return fmt.Errorf("release: no release is due, there is nothing to tag")
	}
	return p.platform.CreateTag(ctx, plan.Repository, platform.Tag{
		Name:    plan.NextTag,
		Commit:  plan.Commit,
		Message: message,
	})
}

// latest returns the highest version among the tags carrying the prefix, together
// with the tag holding it. Tags that do not parse as a version — release names,
// build markers, anything a repository accumulates — are ignored.
func latest(tags []platform.Tag, prefix string) (version.Version, string, bool) {
	var (
		best    version.Version
		bestTag string
		found   bool
	)
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Name, prefix) {
			continue
		}
		candidate, err := version.Parse(strings.TrimPrefix(tag.Name, prefix))
		if err != nil {
			continue
		}
		if !found || best.LessThan(candidate) {
			best, bestTag, found = candidate, tag.Name, true
		}
	}
	return best, bestTag, found
}

// find returns the tag with the given name.
func find(tags []platform.Tag, name string) (platform.Tag, bool) {
	for _, tag := range tags {
		if tag.Name == name {
			return tag, true
		}
	}
	return platform.Tag{}, false
}
