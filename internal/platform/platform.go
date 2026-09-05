// Package platform defines the abstraction over the git hosting platforms taggr
// reads version tags from and creates version tags on.
//
// A platform implementation lives in its own sub-package, implements Platform and
// registers itself from an init function, so adding support for a new platform
// never requires a change to the commands or to the release planner. Anything a
// platform can offer beyond the minimum is modelled as a separate, optional
// interface — LabelReader, PullRequestResolver, EnvironmentDetector — that
// callers discover with a type assertion.
package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/DavidW475/taggr/internal/config"
)

// Repository identifies a repository on a platform.
type Repository struct {
	// Owner is the namespace the repository lives in: the team project on Azure
	// DevOps, the organisation or user on GitHub.
	Owner string
	// Name is the name or ID of the repository.
	Name string
}

// String renders the repository as "owner/name", or as just the name when no
// owner is known.
func (r Repository) String() string {
	if r.Owner == "" {
		return r.Name
	}
	return r.Owner + "/" + r.Name
}

// Tag is a git tag on a platform.
type Tag struct {
	// Name is the tag name without the "refs/tags/" prefix, e.g. "v1.4.2".
	Name string
	// Commit is the ID of the commit the tag points at.
	Commit string
	// Message is the message of an annotated tag. An empty message means the tag
	// is, or is to be created as, a lightweight tag.
	Message string
}

// Platform is a git hosting platform taggr can read tags from and create tags on.
// Every implementation must provide these three operations; everything else is an
// optional interface.
type Platform interface {
	// Name returns the name the platform is registered and selected under.
	Name() string

	// ListTags returns every tag of the repository. Tags that do not look like
	// version tags are filtered out by the caller, not by the platform.
	ListTags(ctx context.Context, repo Repository) ([]Tag, error)

	// CreateTag creates the tag on the repository. An annotated tag is created
	// when Tag.Message is set, a lightweight tag otherwise.
	CreateTag(ctx context.Context, repo Repository, tag Tag) error

	// ResolveCommit resolves a branch name, tag name or commit ID to the commit
	// ID a tag would be created on. An empty ref resolves to the tip of the
	// repository's default branch.
	ResolveCommit(ctx context.Context, repo Repository, ref string) (string, error)
}

// LabelReader is implemented by platforms that expose the labels of a pull
// request. It is the capability the "pr-labels" bump source needs.
type LabelReader interface {
	// PullRequestLabels returns the labels currently set on the pull request.
	PullRequestLabels(ctx context.Context, repo Repository, pullRequest string) ([]string, error)
}

// Commit is a single commit of a repository.
type Commit struct {
	// ID is the commit ID.
	ID string
	// Message is the complete commit message: subject line, body and footers.
	Message string
}

// Subject returns the first line of the commit message.
func (c Commit) Subject() string {
	if i := strings.IndexAny(c.Message, "\r\n"); i >= 0 {
		return c.Message[:i]
	}
	return c.Message
}

// CommitRange selects the commits a release would contain.
type CommitRange struct {
	// From is the tag name or commit ID the previous release was cut on and is
	// excluded from the range. An empty From means the whole history up to To.
	From string
	// To is the commit being released, included in the range.
	To string
}

// CommitReader is implemented by platforms that can list the commits between two
// points in the history. It is the capability the "conventional-commits" bump
// source needs.
type CommitReader interface {
	// Commits returns the commits in the range, newest first.
	Commits(ctx context.Context, repo Repository, commits CommitRange) ([]Commit, error)
}

// PullRequestResolver is implemented by platforms that can name the pull request
// a commit was merged by. It lets taggr run on the branch build after a merge,
// where the CI environment no longer carries a pull request ID.
type PullRequestResolver interface {
	// PullRequestForCommit returns the ID of the pull request that produced the
	// commit, or an empty string when the commit came from no pull request.
	PullRequestForCommit(ctx context.Context, repo Repository, commit string) (string, error)
}

// Environment is the build context a platform inferred from the environment it
// runs in. Empty fields mean "not detected" and are filled in from flags or the
// configuration file instead.
type Environment struct {
	Repository  Repository
	Ref         string
	PullRequest string
}

// EnvironmentDetector is implemented by platforms that can infer the repository,
// commit and pull request from the variables their CI system sets, so that taggr
// needs no arguments in a pipeline.
type EnvironmentDetector interface {
	DetectEnvironment() Environment
}

// Factory builds a platform from its section of the configuration.
type Factory func(settings config.Settings) (Platform, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register makes a platform implementation available under name. It is meant to
// be called from an init function and panics when a name is registered twice.
func Register(name string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[name]; exists {
		panic("platform: " + name + " registered twice")
	}
	registry[name] = factory
}

// Open builds the platform registered under name from the given settings.
func Open(name string, settings config.Settings) (Platform, error) {
	mu.RLock()
	factory, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("platform: unknown platform %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	p, err := factory(settings)
	if err != nil {
		return nil, fmt.Errorf("platform %s: %w", name, err)
	}
	return p, nil
}

// Names returns the names of all registered platforms in alphabetical order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
