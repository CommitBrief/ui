// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"errors"
	"strconv"
	"strings"
)

// maxVersionLen bounds a version string taken from outside (a release tag).
const maxVersionLen = 64

// ErrVersionInvalid is returned for a string that is not a semantic version.
var ErrVersionInvalid = errors.New("update: not a semantic version")

// Version is a semantic version (semver.org, precedence rules of section 11).
// Build metadata after "+" is accepted and ignored, as the specification says
// it carries no precedence.
type Version struct {
	Major, Minor, Patch uint64
	// Pre holds the dot-separated pre-release identifiers; empty for a
	// release.
	Pre []string
}

// ParseVersion reads "1.2.3", "v1.2.3", "1.2.3-test.1" or "1.2.3+build". A
// leading "v" is what release tags carry and is dropped.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	if s == "" || len(s) > maxVersionLen {
		return Version{}, ErrVersionInvalid
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdentifiers(s[i+1:], false) {
			return Version{}, ErrVersionInvalid
		}
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, ErrVersionInvalid
	}
	var nums [3]uint64
	for i, p := range parts {
		n, ok := parseNumeric(p)
		if !ok {
			return Version{}, ErrVersionInvalid
		}
		nums[i] = n
	}
	v := Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}
	if hasPre {
		if !validIdentifiers(pre, true) {
			return Version{}, ErrVersionInvalid
		}
		v.Pre = strings.Split(pre, ".")
	}
	return v, nil
}

// parseNumeric reads a numeric identifier: digits only, no leading zero.
func parseNumeric(s string) (uint64, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// validIdentifiers checks dot-separated identifiers: [0-9A-Za-z-]+, and for a
// pre-release a numeric identifier has no leading zero.
func validIdentifiers(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				numeric = false
			default:
				return false
			}
		}
		if pre && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// IsPrerelease reports whether v carries pre-release identifiers.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// String is the canonical form without "v" and without build metadata.
func (v Version) String() string {
	s := strconv.FormatUint(v.Major, 10) + "." + strconv.FormatUint(v.Minor, 10) + "." + strconv.FormatUint(v.Patch, 10)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Compare returns -1, 0 or 1 as v is lower than, equal to or higher than o.
func (v Version) Compare(o Version) int {
	for _, p := range [3][2]uint64{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if c := cmpUint(p[0], p[1]); c != 0 {
			return c
		}
	}
	// A release is higher than any of its pre-releases.
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		if c := comparePre(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	// The longer identifier list is higher when all shared ones are equal.
	return cmpUint(uint64(len(v.Pre)), uint64(len(o.Pre)))
}

// comparePre compares two pre-release identifiers: numeric ones numerically and
// lower than alphanumeric ones, alphanumeric ones in ASCII order.
func comparePre(a, b string) int {
	an, aok := parseNumeric(a)
	bn, bok := parseNumeric(b)
	switch {
	case aok && bok:
		return cmpUint(an, bn)
	case aok:
		return -1
	case bok:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
