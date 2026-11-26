// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Requirement is one package a requirements file asks for, on the
// platform it was read for.
type Requirement struct {
	// Name is the package's name as the file writes it.
	Name string
	// Specifier is the version the file asks for, with no spaces: "==1.2"
	// for a pin, ">=3.0" for a range, "" for any version.
	Specifier string
	// Line is where the file says so.
	Line int
}

// Pinned returns the version r pins, when it is one ==.
func (r Requirement) Pinned() (string, bool) {
	v, ok := strings.CutPrefix(r.Specifier, "==")
	if !ok || strings.ContainsAny(v, ",*") {
		return "", false
	}
	return v, true
}

// reqName is the name at the start of a requirement.
var reqName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)

// ParseRequirements reads a pip requirements file, as an upstream's
// requirements.txt writes one, for the platform env: each line a package
// with a version or a range, or none, a comment after '#', and an
// environment marker after ';'. A line whose marker is false for env is
// left out. A package named by a line with no version and a line that
// pins it, as some name a package for older Pythons, is the pin. An
// option, an include, a URL, extras, or one package pinned twice is an
// error.
func ParseRequirements(r io.Reader, env MarkerEnv) ([]Requirement, error) {
	var reqs []Requirement
	at := map[string]int{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		spec, marker, hasMarker := strings.Cut(line, ";")
		spec = strings.TrimSpace(spec)
		switch {
		case strings.HasPrefix(line, "-"):
			return nil, fmt.Errorf("line %d: %q is an option, which this tool does not read", n, line)
		case strings.Contains(spec, "@"):
			return nil, fmt.Errorf("line %d: %q names a URL (@), which this tool does not read", n, line)
		}
		if hasMarker {
			ok, err := EvalMarker(strings.TrimSpace(marker), env)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			if !ok {
				continue
			}
		}
		name := reqName.FindString(spec)
		rest := strings.ReplaceAll(strings.TrimSpace(spec[len(name):]), " ", "")
		if name == "" || strings.HasPrefix(rest, "[") {
			return nil, fmt.Errorf("line %d: %q is no package name with a version this tool reads", n, line)
		}
		req := Requirement{Name: name, Specifier: rest, Line: n}
		key := Normalize(name)
		i, seen := at[key]
		if !seen {
			at[key] = len(reqs)
			reqs = append(reqs, req)
			continue
		}
		_, oldPinned := reqs[i].Pinned()
		_, newPinned := req.Pinned()
		switch {
		case reqs[i].Specifier == "" && newPinned:
			reqs[i] = req
		case oldPinned && req.Specifier == "":
		default:
			return nil, fmt.Errorf("line %d: %s is asked for at %q on line %d and at %q here; this tool reads one of each package",
				n, name, reqs[i].Specifier, reqs[i].Line, req.Specifier)
		}
	}
	return reqs, sc.Err()
}

// The kinds of line pins.txt holds, by the comment above each.
const (
	// KindUnpinned is a package upstream asks for with no ==.
	KindUnpinned = "upstream leaves it unpinned: "
	// KindNeeded is a package a pinned package requires that upstream's
	// list leaves out.
	KindNeeded = "needed by "
	// KindBuild is a build backend, or one of its own requirements, which
	// a source build needs and the image never ships.
	KindBuild = "builds "
)

// Pin is one line of pins.txt: a package at one version, and the comment
// above it, which says why the line is there.
type Pin struct {
	Name, Version string
	// Kind is KindUnpinned, KindNeeded or KindBuild, and Note the whole
	// comment.
	Kind, Note string
	Line       int
}

// pinLine is a pins.txt line: one package at one version.
var pinLine = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)==([A-Za-z0-9.!+_-]+)$`)

// ParsePins reads pins.txt: each line a package==version, under a comment
// that says which kind of pin it is, from KindUnpinned, KindNeeded and
// KindBuild. A pin with no such comment, a comment with no pin under it, a
// range, or a package pinned twice is an error.
func ParsePins(r io.Reader) ([]Pin, error) {
	var pins []Pin
	seen := map[string]int{}
	note, noteLine := "", 0
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			if note != "" {
				return nil, fmt.Errorf("pins.txt line %d: the comment %q has no pin under it", noteLine, note)
			}
		case strings.HasPrefix(line, "#"):
			if note != "" {
				return nil, fmt.Errorf("pins.txt line %d: the comment %q has no pin under it", noteLine, note)
			}
			note, noteLine = strings.TrimSpace(strings.TrimPrefix(line, "#")), n
		default:
			m := pinLine.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("pins.txt line %d: %q is not one package==version", n, line)
			}
			if note == "" {
				return nil, fmt.Errorf("pins.txt line %d: %s needs a comment above it saying why: %q, %q or %q",
					n, line, "# "+KindUnpinned+"<requirement>", "# "+KindNeeded+"<package>", "# "+KindBuild+"<packages> from source")
			}
			kind := ""
			for _, k := range []string{KindUnpinned, KindNeeded, KindBuild} {
				if strings.HasPrefix(note, k) {
					kind = k
				}
			}
			if kind == "" {
				return nil, fmt.Errorf("pins.txt line %d: the comment %q says none of %q, %q or %q", noteLine, note, KindUnpinned, KindNeeded, KindBuild)
			}
			if first, ok := seen[Normalize(m[1])]; ok {
				return nil, fmt.Errorf("pins.txt line %d: %s is pinned twice, here and on line %d", n, m[1], first)
			}
			seen[Normalize(m[1])] = n
			pins = append(pins, Pin{Name: m[1], Version: m[2], Kind: kind, Note: note, Line: n})
			note = ""
		}
	}
	if note != "" {
		return nil, fmt.Errorf("pins.txt line %d: the comment %q has no pin under it", noteLine, note)
	}
	return pins, sc.Err()
}

// Pinned is a package at the one version the image installs, and why
// that version. Upstream is true for a pin upstream's own list makes
// with ==, which nothing here may change.
type Pinned struct {
	Name, Version, Why string
	Upstream           bool
}

// Merge gives each package of reqs, upstream's, its version: upstream's
// own pin, which nothing in pins overrides, or else the KindUnpinned pin
// for it, which must name upstream's requirement as upstream writes it and
// meet it. Each KindNeeded pin is added. Every mismatch between the two is
// an error, all of them joined, each saying what to change.
func Merge(reqs []Requirement, pins []Pin) ([]Pinned, error) {
	byName := map[string]Pin{}
	for _, p := range pins {
		if p.Kind != KindBuild {
			byName[Normalize(p.Name)] = p
		}
	}
	var (
		out  []Pinned
		errs []error
		used = map[string]bool{}
	)
	for _, r := range reqs {
		key := Normalize(r.Name)
		p, pinned := byName[key]
		used[key] = true
		if v, ok := r.Pinned(); ok {
			if pinned {
				errs = append(errs, fmt.Errorf("%s is pinned by upstream at %s, and upstream's pin stands: remove pins.txt line %d", r.Name, v, p.Line))
			}
			out = append(out, Pinned{Name: r.Name, Version: v, Why: "pinned by upstream", Upstream: true})
			continue
		}
		want := r.Name + r.Specifier
		switch {
		case !pinned:
			errs = append(errs, fmt.Errorf("%s, which upstream leaves unpinned, needs a line in pins.txt: %s==<version> under # %s%s", want, r.Name, KindUnpinned, want))
			continue
		case p.Kind != KindUnpinned:
			errs = append(errs, fmt.Errorf("%s is in upstream's list, as %s: pins.txt line %d says %q, and must say %q", r.Name, want, p.Line, p.Note, KindUnpinned+want))
			continue
		case Normalize(strings.ReplaceAll(strings.TrimPrefix(p.Note, KindUnpinned), " ", "")) != Normalize(want):
			errs = append(errs, fmt.Errorf("pins.txt line %d says %q, but upstream now asks for %s: say %q, and check the version still meets it", p.Line, p.Note, want, KindUnpinned+want))
			continue
		}
		if r.Specifier != "" {
			ok, err := Satisfies(p.Version, r.Specifier)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
				continue
			}
			if !ok {
				errs = append(errs, fmt.Errorf("pins.txt line %d pins %s at %s, which does not meet upstream's %s", p.Line, p.Name, p.Version, want))
				continue
			}
		}
		why := "pins.txt: upstream leaves it unpinned (" + r.Specifier + ")"
		if r.Specifier == "" {
			why = "pins.txt: upstream leaves it unpinned"
		}
		out = append(out, Pinned{Name: r.Name, Version: p.Version, Why: why})
	}
	for _, p := range pins {
		key := Normalize(p.Name)
		switch {
		case p.Kind == KindNeeded && used[key]:
			// Reported above, as a package in upstream's list
		case p.Kind == KindNeeded:
			out = append(out, Pinned{Name: p.Name, Version: p.Version, Why: "pins.txt: " + p.Note})
		case p.Kind == KindUnpinned && !used[key]:
			errs = append(errs, fmt.Errorf("pins.txt line %d pins %s, which upstream's list no longer names: remove it", p.Line, p.Name))
		}
	}
	return out, errors.Join(errs...)
}

// Build is pins' build backends: the KindBuild pins, each at its version.
func Build(pins []Pin) []Pinned {
	var out []Pinned
	for _, p := range pins {
		if p.Kind == KindBuild {
			out = append(out, Pinned{Name: p.Name, Version: p.Version, Why: "pins.txt: " + p.Note})
		}
	}
	return out
}
