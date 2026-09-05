// Package plugins wires the built-in platforms and bump sources into their
// registries. It is imported for its side effects only.
//
// This is the single place a new implementation has to be added to become
// selectable through --platform or --source; nothing else in the program needs to
// learn about it.
package plugins

import (
	// Platforms.
	_ "github.com/DavidW475/taggr/internal/platform/ado"

	// Bump sources.
	_ "github.com/DavidW475/taggr/internal/source/conventional"
	_ "github.com/DavidW475/taggr/internal/source/prlabels"
)
