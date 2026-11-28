// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// BoundsPath is the Renovate preset Generate writes, which renovate.json
// extends.
const BoundsPath = "python/renovate-bounds.json"

// Bound is how far Renovate may move one pin of pins.txt: Allowed, every
// specifier the image's packages and upstream ask of it, as one PEP 440
// specifier set, and Sources, who asks for what.
type Bound struct {
	Name, Allowed string
	Sources       []string
}

// requiresDist is a Requires-Dist entry: a name, extras in brackets, then
// a specifier, bare or in parentheses, then a marker after ';'.
var requiresDist = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*(?:\(([^)]*)\)|([^;@()]*))\s*(?:;\s*(.*))?$`)

// ParseRequiresDist reads one Requires-Dist entry, as PyPI gives it, into
// the package it names, its specifier with no spaces, or "" for any
// version, and its marker, or "". A URL in place of a version is an error:
// PyPI takes no release that names one.
func ParseRequiresDist(entry string) (name, spec, marker string, err error) {
	before, _, _ := strings.Cut(entry, ";")
	if strings.Contains(before, "@") {
		return "", "", "", fmt.Errorf("the requirement %q names a URL (@), which this tool does not read", entry)
	}
	m := requiresDist.FindStringSubmatch(strings.TrimSpace(entry))
	if m == nil {
		return "", "", "", fmt.Errorf("%q is no requirement this tool reads", entry)
	}
	return m[1], strings.ReplaceAll(m[2]+m[3], " ", ""), strings.TrimSpace(m[4]), nil
}

// Bounds gives each pin of pins every specifier asked of it: upstream's, from reqs, for a runtime pin; and each other
// package's whose Requires-Dist names it, from locked for a runtime pin
// and from backends for a build one, since each lock is one environment.
// A Requires-Dist marker is judged for each of envs, as pip check judges
// it, and a requirement any of them takes counts. A pin gets a Bound only
// when some specifier could refuse a newer version: a lower bound alone
// never does.
//
// A pin that asks another, with ==, for the asker's own version, as
// python-Levenshtein asks Levenshtein, is a Group: the two move together,
// so the asked pin takes the asker's bounds in place of that ==, which
// would hold it where it is for good.
//
// A marker this tool cannot judge, or a specifier that is not PEP 440's,
// which Renovate would refuse for the whole repository, is an error
// naming the package that asks. Bounds come in the order of their
// normalised names, and Groups in the order of their askers'.
func Bounds(pins []Pin, reqs []Requirement, locked, backends []Locked, envs []MarkerEnv) ([]Bound, []Group, error) {
	upstream := map[string]string{}
	for _, r := range reqs {
		upstream[Normalize(r.Name)] = r.Specifier
	}
	runtime, err := asked(locked, envs)
	if err != nil {
		return nil, nil, err
	}
	built, err := asked(backends, envs)
	if err != nil {
		return nil, nil, err
	}
	// Each pin's own sources and specifiers, and the pins that ask
	// another for their own version
	type own struct {
		pin     Pin
		sources []string
		specs   []sourced
		pairs   map[int]ask
	}
	var owns []own
	byKey := map[string]int{}
	for _, p := range pins {
		byKey[Normalize(p.Name)] = len(owns)
		owns = append(owns, own{pin: p, pairs: map[int]ask{}})
	}
	for i := range owns {
		o := &owns[i]
		key := Normalize(o.pin.Name)
		by := runtime
		if o.pin.Kind == KindBuild {
			by = built
		} else if s := upstream[key]; s != "" {
			o.sources = append(o.sources, "upstream asks for "+s)
			o.specs = append(o.specs, sourced{source: o.sources[len(o.sources)-1], spec: s})
		}
		for _, a := range by[key] {
			if j, ok := byKey[Normalize(a.name)]; ok && (owns[j].pin.Kind == KindBuild) == (o.pin.Kind == KindBuild) && ownVersion(a) {
				o.pairs[len(o.sources)] = a
				o.sources = append(o.sources, "")
				continue
			}
			o.sources = append(o.sources, a.who()+" requires "+a.spec)
			o.specs = append(o.specs, sourced{source: o.sources[len(o.sources)-1], spec: a.spec})
		}
	}
	var (
		out    []Bound
		groups []Group
		errs   []error
	)
	for i := range owns {
		o := &owns[i]
		var sources []string
		specs := slices.Clone(o.specs)
		for k, source := range o.sources {
			a, paired := o.pairs[k]
			if !paired {
				sources = append(sources, source)
				continue
			}
			// The asker's own specifiers, never what its own pairs bring
			asker := owns[byKey[Normalize(a.name)]]
			sources = append(sources, fmt.Sprintf("%s requires %s, its own version, so it moves with %s, within %s's bounds",
				a.who(), a.spec, asker.pin.Name, asker.pin.Name))
			specs = append(specs, asker.specs...)
			groups = append(groups, Group{Asker: asker.pin.Name, Version: a.version, Asked: o.pin.Name})
		}
		b, ok, err := bound(o.pin.Name, sources, specs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Bound) int { return cmp.Compare(Normalize(a.Name), Normalize(b.Name)) })
	slices.SortFunc(groups, func(a, b Group) int { return cmp.Compare(Normalize(a.Asker), Normalize(b.Asker)) })
	return out, groups, errors.Join(errs...)
}

// Group is two pins that move together: Asker, at Version, asks for
// Asked at that same version, with ==.
type Group struct{ Asker, Version, Asked string }

// ask is one package's specifier for another.
type ask struct{ name, version, spec string }

// who is the asking package, as "name version".
func (a ask) who() string { return a.name + " " + a.version }

// ownVersion reports whether a asks, with == alone, for the asker's own
// version.
func ownVersion(a ask) bool {
	v, ok := strings.CutPrefix(a.spec, "==")
	if !ok || strings.ContainsAny(v, ",*") {
		return false
	}
	order, err := compareVersions(v, a.version)
	return err == nil && order == 0
}

// asked reads the Requires-Dist of each package of pkgs, and returns, by
// the normalised name of the package each names, what each asks of it,
// in the order of the asking packages' normalised names. A package's
// requirement of itself is not read.
func asked(pkgs []Locked, envs []MarkerEnv) (map[string][]ask, error) {
	pkgs = slices.Clone(pkgs)
	slices.SortFunc(pkgs, func(a, b Locked) int { return cmp.Compare(Normalize(a.Name), Normalize(b.Name)) })
	by := map[string][]ask{}
	var errs []error
	for _, l := range pkgs {
		who := l.Name + " " + l.Version
		for _, entry := range l.Requires {
			name, spec, marker, perr := ParseRequiresDist(entry)
			if perr != nil {
				errs = append(errs, fmt.Errorf("%s: %w: pin another release of it in pins.txt, or teach internal/pins/bounds.go the form", who, perr))
				continue
			}
			key := Normalize(name)
			if key == Normalize(l.Name) || spec == "" {
				continue
			}
			takes, merr := anyEnv(marker, envs)
			if merr != nil {
				errs = append(errs, fmt.Errorf("%s requires %q: %w: pin another release of it in pins.txt, or teach internal/pins/markers.go the marker", who, entry, merr))
				continue
			}
			if takes {
				by[key] = append(by[key], ask{name: l.Name, version: l.Version, spec: spec})
			}
		}
	}
	return by, errors.Join(errs...)
}

// anyEnv reports whether marker, "" for none, holds for any of envs.
func anyEnv(marker string, envs []MarkerEnv) (bool, error) {
	if marker == "" {
		return true, nil
	}
	for i := range envs {
		ok, err := EvalMarker(marker, envs[i])
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// specClause is one comparison of a PEP 440 specifier.
var specClause = regexp.MustCompile(`^(~=|==|!=|<=|>=|<|>)([^\s,=<>!~]+)$`)

// checkClause refuses c, one comparison of a specifier, unless it is one
// PEP 440 allows: ~=, ==, !=, <=, >=, < or > and a version, with .* only
// after == or !=, and at least two parts after ~=.
func checkClause(c string) error {
	m := specClause.FindStringSubmatch(c)
	if m == nil {
		return fmt.Errorf("%q is no PEP 440 comparison this tool reads (~=, ==, !=, <=, >=, < or > and a version)", c)
	}
	op, v := m[1], m[2]
	if w, ok := strings.CutSuffix(v, ".*"); ok {
		if op != "==" && op != "!=" {
			return fmt.Errorf("%q: only == and != take a .* wildcard", c)
		}
		v = w
	}
	if op == "~=" && !strings.Contains(v, ".") {
		return fmt.Errorf("%q: ~= needs a version of two parts at least", c)
	}
	_, err := parseVersion(v)
	return err
}

// sourced is a specifier and the source that asks for it.
type sourced struct{ source, spec string }

// bound is name's Bound from its sources and their specifiers: each
// clause once, sorted, or none when no clause could refuse a newer
// version. A clause that is not PEP 440's is an error naming the source
// that asks for it.
func bound(name string, sources []string, specs []sourced) (Bound, bool, error) {
	var clauses []string
	for _, s := range specs {
		for c := range strings.SplitSeq(s.spec, ",") {
			c = strings.TrimSpace(c)
			if err := checkClause(c); err != nil {
				return Bound{}, false, fmt.Errorf("%s: %s: %w, which Renovate would refuse for the whole repository: "+
					"pin another release of %s, or of what asks for it, in pins.txt", name, s.source, err, name)
			}
			if !slices.Contains(clauses, c) {
				clauses = append(clauses, c)
			}
		}
	}
	if !slices.ContainsFunc(clauses, func(c string) bool { return !strings.HasPrefix(c, ">") }) {
		return Bound{}, false, nil
	}
	slices.Sort(clauses)
	return Bound{Name: name, Allowed: strings.Join(clauses, ","), Sources: sources}, true, nil
}

// boundsDescription opens the file WriteBounds writes.
const boundsDescription = "Generated by go run ./cmd/divbast-pins: each pin of python/pins.txt that a package the image installs, " +
	"or upstream, bounds from above, with every specifier asked of it, so Renovate proposes no version the build refuses. " +
	"Do not edit it: change the pins, then run go run ./cmd/divbast-pins again."

// WriteBounds writes bounds and groups as a Renovate preset: one package
// rule for each bound, matching the pin by its name as pins.txt writes it,
// in the order given, then one for each group, which puts its two pins in
// one pull request; as two-space JSON with a newline at its end. The same
// bounds and groups always give the same bytes.
func WriteBounds(w io.Writer, bounds []Bound, groups []Group) error {
	type rule struct {
		Description       string   `json:"description"`
		MatchManagers     []string `json:"matchManagers"`
		MatchDatasources  []string `json:"matchDatasources"`
		MatchPackageNames []string `json:"matchPackageNames"`
		AllowedVersions   string   `json:"allowedVersions,omitempty"`
		GroupName         string   `json:"groupName,omitempty"`
	}
	preset := struct {
		Schema       string `json:"$schema"`
		Description  string `json:"description"`
		PackageRules []rule `json:"packageRules"`
	}{Schema: "https://docs.renovatebot.com/renovate-schema.json", Description: boundsDescription, PackageRules: []rule{}}
	pypi := func(description string, names ...string) rule {
		return rule{
			Description: description, MatchManagers: []string{"custom.regex"}, MatchDatasources: []string{"pypi"},
			MatchPackageNames: names,
		}
	}
	for _, b := range bounds {
		r := pypi(b.Name+": "+strings.Join(b.Sources, "; ")+".", b.Name)
		r.AllowedVersions = b.Allowed
		preset.PackageRules = append(preset.PackageRules, r)
	}
	for _, g := range groups {
		r := pypi(fmt.Sprintf("%s %s requires %s at its own version, so the two move together, in one pull request.", g.Asker, g.Version, g.Asked),
			g.Asker, g.Asked)
		r.GroupName = g.Asker + " and " + g.Asked
		preset.PackageRules = append(preset.PackageRules, r)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(preset)
}
