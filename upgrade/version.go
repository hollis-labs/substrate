// Package upgrade moves an app from one Substrate release to another: it reports
// what the BOM changes, reads the changelog between the two, applies registered
// mechanical migrations, bumps the one Substrate requirement, and verifies the
// app still validates and builds, restoring everything if it does not. It fails
// loudly instead of guessing when a breaking change has no migration.
package upgrade

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // pre-release, including Go pseudo-versions
	Raw                 string
}

// ParseVersion parses vMAJOR.MINOR.PATCH[-pre][+build].
func ParseVersion(s string) (Version, error) {
	v := Version{Raw: s}
	t := strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(t, '+'); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '-'); i >= 0 {
		v.Pre, t = t[i+1:], t[:i]
	}
	parts := strings.Split(t, ".")
	if len(parts) != 3 || !strings.HasPrefix(s, "v") {
		return v, fmt.Errorf("%q is not a semantic version", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("%q is not a semantic version", s)
		}
		*[]*int{&v.Major, &v.Minor, &v.Patch}[i] = n
	}
	return v, nil
}

// Compare returns -1, 0 or 1. A pre-release sorts before its release.
func (a Version) Compare(b Version) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	case a.Pre < b.Pre:
		return -1
	}
	return 1
}

// Kind classifies a move between two versions.
type Kind string

const (
	Same       Kind = "same"
	Downgrade  Kind = "downgrade"
	Patch      Kind = "patch"      // safe by the compat policy
	Minor      Kind = "minor"      // may break before 1.0, safe from 1.0
	Major      Kind = "major"      // may break
	Unreleased Kind = "unreleased" // a pre-release or pseudo-version target: nothing is promised
)

// Classify says what kind of move from -> to is.
func Classify(from, to Version) Kind {
	switch c := from.Compare(to); {
	case c == 0:
		return Same
	case c > 0:
		return Downgrade
	}
	switch {
	case to.Pre != "":
		return Unreleased
	case to.Major != from.Major:
		return Major
	case to.Minor != from.Minor:
		return Minor
	}
	return Patch
}

// MayBreak reports whether the compat policy allows this move to break an app:
// any major bump, a minor bump before 1.0, and anything unreleased.
func (k Kind) MayBreak(from Version) bool {
	switch k {
	case Major, Unreleased:
		return true
	case Minor:
		return from.Major == 0
	}
	return false
}
