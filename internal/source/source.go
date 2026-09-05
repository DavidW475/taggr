// Package source defines the abstraction over the places taggr can learn the size
// of the next version bump from: pull request labels, commit messages, an
// explicit flag, and whatever else gets added later.
//
// Like platforms, a bump source lives in its own sub-package and registers itself
// from an init function. A source never talks to a platform's API directly: it
// receives the active platform in the Request and asks it for the capability it
// needs, which is what keeps a source such as "pr-labels" usable on every
// platform that can read pull request labels.
package source

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/DavidW475/taggr/internal/config"
	"github.com/DavidW475/taggr/internal/platform"
	"github.com/DavidW475/taggr/internal/version"
)

// Request is the build context a bump is determined for.
type Request struct {
	// Platform is the platform taggr is working against. Sources use it to read
	// the data they need, after asserting the capability they require.
	Platform platform.Platform
	// Repository is the repository being released.
	Repository platform.Repository
	// Commit is the commit ID the new tag would point at.
	Commit string
	// PullRequest is the ID of the pull request the release originates from. It
	// is empty when taggr does not run for a pull request.
	PullRequest string
	// PreviousTag is the version tag the last release was cut on, empty when the
	// repository has no version tag yet. Sources that inspect a commit range use
	// it as the lower bound.
	PreviousTag string
}

// Result is the bump a source determined.
type Result struct {
	// Bump is the size of the increment. BumpNone means no release is due.
	Bump version.Bump
	// Reason explains in one line how the bump was determined, so that the
	// decision is visible in the pipeline log.
	Reason string
}

// Source determines how large the next version bump should be.
type Source interface {
	// Name returns the name the source is registered and selected under.
	Name() string

	// Bump determines the size of the next version increment for the request.
	Bump(ctx context.Context, req Request) (Result, error)
}

// Factory builds a bump source from its section of the configuration.
type Factory func(settings config.Settings) (Source, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register makes a bump source available under name. It is meant to be called
// from an init function and panics when a name is registered twice.
func Register(name string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[name]; exists {
		panic("source: " + name + " registered twice")
	}
	registry[name] = factory
}

// Open builds the bump source registered under name from the given settings.
func Open(name string, settings config.Settings) (Source, error) {
	mu.RLock()
	factory, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown bump source %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	s, err := factory(settings)
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", name, err)
	}
	return s, nil
}

// Names returns the names of all registered bump sources in alphabetical order.
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
