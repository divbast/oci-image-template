// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Normalize is a package's name as PyPI compares names: lower case, with
// each run of '-', '_' and '.' one '-'.
func Normalize(name string) string {
	return strings.ToLower(separators.ReplaceAllString(name, "-"))
}

var separators = regexp.MustCompile(`[-_.]+`)

// pep440 is the part of PEP 440's version scheme the packages use: an
// epoch, a release, and a pre-, post- and dev-release, with no local part.
var pep440 = regexp.MustCompile(`^(?:(\d+)!)?(\d+(?:\.\d+)*)(?:(a|b|rc)(\d+))?(?:\.post(\d+))?(?:\.dev(\d+))?$`)

// version is a parsed PEP 440 version, in the fields it sorts by.
type version struct {
	epoch   int
	release []int
	// phase orders the pre-release: -1 for a dev release of a final, 0, 1
	// and 2 for a, b and rc, and 3 for none
	phase, pre int
	// post is -1 for none, dev is maxInt for none
	post, dev int
}

func parseVersion(s string) (version, error) {
	m := pep440.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return version{}, fmt.Errorf("%q is no version this tool reads", s)
	}
	num := func(t string, none int) int {
		if t == "" {
			return none
		}
		n, _ := strconv.Atoi(t) // the pattern holds digits alone
		return n
	}
	v := version{epoch: num(m[1], 0), phase: 3, post: num(m[5], -1), dev: num(m[6], int(^uint(0)>>1))}
	for p := range strings.SplitSeq(m[2], ".") {
		v.release = append(v.release, num(p, 0))
	}
	// 1.0 and 1.0.0 are one release
	for len(v.release) > 1 && v.release[len(v.release)-1] == 0 {
		v.release = v.release[:len(v.release)-1]
	}
	switch {
	case m[3] != "":
		v.phase, v.pre = slices.Index([]string{"a", "b", "rc"}, m[3]), num(m[4], 0)
	case m[5] == "" && m[6] != "":
		v.phase = -1
	}
	return v, nil
}

// compareVersions orders a and b as PEP 440 does: below 0 when a is the
// lower, 0 when they are one version, above 0 when a is the higher.
func compareVersions(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	return cmp.Or(
		cmp.Compare(va.epoch, vb.epoch),
		slices.Compare(va.release, vb.release),
		cmp.Compare(va.phase, vb.phase),
		cmp.Compare(va.pre, vb.pre),
		cmp.Compare(va.post, vb.post),
		cmp.Compare(va.dev, vb.dev),
	), nil
}

// clause is one comparison of a specifier, such as ">=3.0".
var clause = regexp.MustCompile(`^(==|!=|>=|<=|>|<)\s*([^\s,]+)$`)

// Satisfies reports whether version meets spec, a comma-separated list of
// comparisons, each ==, !=, >=, <=, > or <. Any other operator, or a
// wildcard, is an error, since the tool does not read it.
func Satisfies(version, spec string) (bool, error) {
	for c := range strings.SplitSeq(spec, ",") {
		c = strings.TrimSpace(c)
		m := clause.FindStringSubmatch(c)
		if m == nil || strings.HasPrefix(c, "===") || strings.HasSuffix(c, ".*") {
			return false, fmt.Errorf("%q in %q is no comparison this tool reads (==, !=, >=, <=, > or <, with no wildcard)", c, spec)
		}
		order, err := compareVersions(version, m[2])
		if err != nil {
			return false, err
		}
		ok := map[string]bool{"==": order == 0, "!=": order != 0, ">=": order >= 0, "<=": order <= 0, ">": order > 0, "<": order < 0}[m[1]]
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
