// Package version implements the semantic versioning rules taggr uses to
// derive the next release version from the current one.
package version

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Bump is the magnitude of a semantic version increment.
type Bump int

const (
	// BumpNone means no release is due.
	BumpNone Bump = iota
	// BumpPatch increments the patch number, e.g. 1.4.2 -> 1.4.3.
	BumpPatch
	// BumpMinor increments the minor number, e.g. 1.4.2 -> 1.5.0.
	BumpMinor
	// BumpMajor increments the major number, e.g. 1.4.2 -> 2.0.0.
	BumpMajor
)

// bumpNames maps every Bump to the name used in configuration and output.
var bumpNames = [...]string{"none", "patch", "minor", "major"}

// String returns the name of the bump as it appears in configuration and output.
func (b Bump) String() string {
	if b < 0 || int(b) >= len(bumpNames) {
		return fmt.Sprintf("Bump(%d)", int(b))
	}
	return bumpNames[b]
}

// ParseBump maps a bump name to its Bump value. The comparison is case-insensitive.
func ParseBump(s string) (Bump, error) {
	for i, name := range bumpNames {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return Bump(i), nil
		}
	}
	return BumpNone, fmt.Errorf("version: unknown bump %q (want one of %s)", s, strings.Join(bumpNames[:], ", "))
}

// Version is a semantic version as defined by https://semver.org.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
	// Prerelease holds the dot-separated identifiers after the "-", without the "-".
	Prerelease string
	// Build holds the dot-separated identifiers after the "+", without the "+".
	Build string
}

// Parse parses a semantic version. A single leading "v" or "V" is accepted and
// dropped, so both "1.4.2" and "v1.4.2" yield the same version.
func Parse(s string) (Version, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, errors.New("version: empty version string")
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}

	var v Version
	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build = s[i+1:]
		s = s[:i]
		if err := validIdentifiers(v.Build, false); err != nil {
			return Version{}, fmt.Errorf("version: %q has invalid build metadata: %w", raw, err)
		}
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.Prerelease = s[i+1:]
		s = s[:i]
		if err := validIdentifiers(v.Prerelease, true); err != nil {
			return Version{}, fmt.Errorf("version: %q has an invalid prerelease: %w", raw, err)
		}
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version: %q is not of the form MAJOR.MINOR.PATCH", raw)
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, err := parseNumber(part)
		if err != nil {
			return Version{}, fmt.Errorf("version: %q: %w", raw, err)
		}
		numbers[i] = n
	}
	v.Major, v.Minor, v.Patch = numbers[0], numbers[1], numbers[2]
	return v, nil
}

// IsValid reports whether s parses as a semantic version.
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// String renders the version without a prefix, e.g. "1.4.2-rc.1+build.7".
func (v Version) String() string {
	s := strconv.FormatUint(v.Major, 10) + "." + strconv.FormatUint(v.Minor, 10) + "." + strconv.FormatUint(v.Patch, 10)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Bump returns v incremented by b. Prerelease identifiers and build metadata are
// always dropped, because a bump produces a plain release version.
//
// Bumping a prerelease by BumpPatch only drops the prerelease instead of
// incrementing the patch number: 1.4.0-rc.1 becomes 1.4.0, since the prerelease
// already leads up to that patch version.
func (v Version) Bump(b Bump) Version {
	next := Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
	switch b {
	case BumpMajor:
		next.Major, next.Minor, next.Patch = v.Major+1, 0, 0
	case BumpMinor:
		next.Minor, next.Patch = v.Minor+1, 0
	case BumpPatch:
		if v.Prerelease == "" {
			next.Patch = v.Patch + 1
		}
	}
	return next
}

// Compare orders two versions by semantic version precedence and returns -1 when
// a sorts before b, 1 when a sorts after b and 0 when both rank equally. Build
// metadata is ignored, as required by the specification.
func Compare(a, b Version) int {
	if c := compareUint(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareUint(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareUint(a.Patch, b.Patch); c != 0 {
		return c
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

// LessThan reports whether v has lower precedence than other.
func (v Version) LessThan(other Version) bool { return Compare(v, other) < 0 }

// comparePrerelease applies the precedence rules for prerelease identifiers. A
// version without a prerelease outranks the same version with one.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}

	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		if c := compareIdentifier(left[i], right[i]); c != 0 {
			return c
		}
	}
	// A larger set of identifiers wins when all preceding ones are equal.
	return compareUint(uint64(len(left)), uint64(len(right)))
}

// compareIdentifier compares a single prerelease identifier. Numeric identifiers
// are compared numerically and always rank below alphanumeric ones.
func compareIdentifier(a, b string) int {
	an, aNumeric := numericIdentifier(a)
	bn, bNumeric := numericIdentifier(b)
	switch {
	case aNumeric && bNumeric:
		return compareUint(an, bn)
	case aNumeric:
		return -1
	case bNumeric:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// numericIdentifier reports whether s consists only of digits and returns its value.
func numericIdentifier(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// parseNumber parses one of the major, minor or patch numbers, rejecting the
// leading zeroes the specification forbids.
func parseNumber(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("version number is empty")
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("version number %q has a leading zero", s)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("version number %q is not a number", s)
	}
	return n, nil
}

// validIdentifiers checks the dot-separated identifiers of a prerelease or of
// build metadata. Numeric prerelease identifiers must not carry leading zeroes.
func validIdentifiers(s string, prerelease bool) error {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return errors.New("contains an empty identifier")
		}
		numeric := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				numeric = false
			default:
				return fmt.Errorf("identifier %q contains the invalid character %q", id, r)
			}
		}
		if prerelease && numeric && len(id) > 1 && id[0] == '0' {
			return fmt.Errorf("numeric identifier %q has a leading zero", id)
		}
	}
	return nil
}
