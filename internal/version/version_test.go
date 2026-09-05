package version

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Version
	}{
		{name: "plain", in: "1.4.2", want: Version{Major: 1, Minor: 4, Patch: 2}},
		{name: "v prefix", in: "v1.4.2", want: Version{Major: 1, Minor: 4, Patch: 2}},
		{name: "zeroes", in: "0.0.0", want: Version{}},
		{name: "prerelease", in: "1.4.0-rc.1", want: Version{Major: 1, Minor: 4, Prerelease: "rc.1"}},
		{name: "build metadata", in: "1.4.2+build.7", want: Version{Major: 1, Minor: 4, Patch: 2, Build: "build.7"}},
		{name: "prerelease and build", in: "v2.0.0-beta.1+exp.sha.5114f85", want: Version{Major: 2, Prerelease: "beta.1", Build: "exp.sha.5114f85"}},
		{name: "hyphen inside build metadata", in: "1.0.0+21AF26D3--117B344092BD", want: Version{Major: 1, Build: "21AF26D3--117B344092BD"}},
		{name: "surrounding spaces", in: "  1.2.3  ", want: Version{Major: 1, Minor: 2, Patch: 3}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned an unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	for _, in := range []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"01.2.3",
		"1.02.3",
		"a.b.c",
		"1.2.3-",
		"1.2.3-01",
		"1.2.3-rc..1",
		"1.2.3-rc$1",
		"-1.2.3",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", in)
		}
	}
}

func TestStringRoundTrips(t *testing.T) {
	for _, in := range []string{"1.4.2", "0.1.0", "1.4.0-rc.1", "1.4.2+build.7", "2.0.0-beta.1+exp.sha.5114f85"} {
		v, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) returned an unexpected error: %v", in, err)
		}
		if got := v.String(); got != in {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, in)
		}
	}
}

func TestBump(t *testing.T) {
	tests := []struct {
		name string
		in   string
		bump Bump
		want string
	}{
		{name: "patch", in: "1.4.2", bump: BumpPatch, want: "1.4.3"},
		{name: "minor resets patch", in: "1.4.2", bump: BumpMinor, want: "1.5.0"},
		{name: "major resets minor and patch", in: "1.4.2", bump: BumpMajor, want: "2.0.0"},
		{name: "none keeps the version", in: "1.4.2", bump: BumpNone, want: "1.4.2"},
		{name: "patch finalises a prerelease", in: "1.4.0-rc.1", bump: BumpPatch, want: "1.4.0"},
		{name: "minor still increments a prerelease", in: "1.4.0-rc.1", bump: BumpMinor, want: "1.5.0"},
		{name: "build metadata is dropped", in: "1.4.2+build.7", bump: BumpPatch, want: "1.4.3"},
		{name: "zero major stays below one", in: "0.3.1", bump: BumpMinor, want: "0.4.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned an unexpected error: %v", tc.in, err)
			}
			if got := v.Bump(tc.bump).String(); got != tc.want {
				t.Errorf("Parse(%q).Bump(%s) = %s, want %s", tc.in, tc.bump, got, tc.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{a: "1.4.2", b: "1.4.2", want: 0},
		{a: "1.4.2", b: "1.4.3", want: -1},
		{a: "1.4.2", b: "1.5.0", want: -1},
		{a: "1.4.2", b: "2.0.0", want: -1},
		{a: "2.0.0", b: "10.0.0", want: -1},
		// A prerelease ranks below the release it leads up to.
		{a: "1.4.0-rc.1", b: "1.4.0", want: -1},
		{a: "1.4.0-alpha", b: "1.4.0-beta", want: -1},
		// Numeric identifiers are compared numerically, not as text.
		{a: "1.4.0-rc.2", b: "1.4.0-rc.10", want: -1},
		// Numeric identifiers rank below alphanumeric ones.
		{a: "1.4.0-1", b: "1.4.0-alpha", want: -1},
		// A larger set of identifiers wins when the common ones are equal.
		{a: "1.4.0-rc", b: "1.4.0-rc.1", want: -1},
		// Build metadata is ignored.
		{a: "1.4.2+build.1", b: "1.4.2+build.9", want: 0},
	}

	for _, tc := range tests {
		a, err := Parse(tc.a)
		if err != nil {
			t.Fatalf("Parse(%q) returned an unexpected error: %v", tc.a, err)
		}
		b, err := Parse(tc.b)
		if err != nil {
			t.Fatalf("Parse(%q) returned an unexpected error: %v", tc.b, err)
		}
		if got := Compare(a, b); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := Compare(b, a); got != -tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestParseBump(t *testing.T) {
	for in, want := range map[string]Bump{
		"none":    BumpNone,
		"patch":   BumpPatch,
		"Minor":   BumpMinor,
		"MAJOR":   BumpMajor,
		" major ": BumpMajor,
	} {
		got, err := ParseBump(in)
		if err != nil {
			t.Fatalf("ParseBump(%q) returned an unexpected error: %v", in, err)
		}
		if got != want {
			t.Errorf("ParseBump(%q) = %s, want %s", in, got, want)
		}
	}

	if _, err := ParseBump("huge"); err == nil {
		t.Error(`ParseBump("huge") succeeded, want an error`)
	}
}

func TestBumpOrderingMatchesMagnitude(t *testing.T) {
	// The pr-labels source relies on the numeric order to pick the largest bump.
	if !(BumpNone < BumpPatch && BumpPatch < BumpMinor && BumpMinor < BumpMajor) {
		t.Error("bumps must be ordered none < patch < minor < major")
	}
}
