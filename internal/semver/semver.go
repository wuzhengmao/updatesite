// Package semver implements the small subset of semantic-version comparison
// that the update site needs. It deliberately accepts loose versions such as
// "1.2", "2026.10.03" or "v1.2.3-rc.1" so that archive directories do not have
// to be strictly valid SemVer.
package semver

import (
	"strconv"
	"strings"
)

// Version is a parsed, comparable version.
type Version struct {
	Raw   string
	Parts []int
	Pre   []string // pre-release identifiers, empty for a release version
}

// Parse parses a loose version string. It reports false when s does not look
// like a version at all (no leading numeric component).
func Parse(s string) (Version, bool) {
	raw := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")

	// Build metadata is ignored for ordering.
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	// Both "-" and "_" introduce a pre-release suffix, so "1.2.3-rc.1" and
	// "1.2.3_rc1" are both accepted.
	var pre []string
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		pre = splitPre(s[i+1:])
		s = s[:i]
	}
	if s == "" {
		return Version{}, false
	}

	fields := strings.Split(s, ".")
	parts := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			return Version{}, false
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return Version{}, false
		}
		parts = append(parts, n)
	}
	return Version{Raw: raw, Parts: parts, Pre: pre}, true
}

func splitPre(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	}) {
		out = append(out, part)
	}
	return out
}

// IsVersionLike reports whether s can be used as a version directory name.
func IsVersionLike(s string) bool {
	_, ok := Parse(s)
	return ok
}

// Compare returns -1, 0 or 1 comparing a to b. Unparsable versions sort last
// and are compared as plain strings so the ordering stays total.
func Compare(a, b string) int {
	va, oka := Parse(a)
	vb, okb := Parse(b)
	switch {
	case oka && okb:
		return compare(va, vb)
	case oka:
		return 1 // parsable versions come first
	case okb:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

func compare(a, b Version) int {
	n := len(a.Parts)
	if len(b.Parts) > n {
		n = len(b.Parts)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(a.Parts) {
			x = a.Parts[i]
		}
		if i < len(b.Parts) {
			y = b.Parts[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	// Equal numeric part: a release version outranks a pre-release.
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	n = len(a.Pre)
	if len(b.Pre) < n {
		n = len(b.Pre)
	}
	for i := 0; i < n; i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1 // numeric identifiers rank below alphanumeric ones
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(a.Pre) == len(b.Pre):
		return 0
	case len(a.Pre) < len(b.Pre):
		return -1
	default:
		return 1
	}
}

// IsPrerelease reports whether a version string carries a pre-release suffix.
func IsPrerelease(s string) bool {
	v, ok := Parse(s)
	return ok && len(v.Pre) > 0
}
