// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestParseRequiresDist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, name, spec, marker string
		wantErr                string
	}{
		// The old form, with the specifier in parentheses
		{in: "importlib-metadata (<=9.0.0,>=3.3.0)", name: "importlib-metadata", spec: "<=9.0.0,>=3.3.0"},
		{in: "pycountry<23.0.0,>=20.7.3", name: "pycountry", spec: "<23.0.0,>=20.7.3"},
		{in: `foo[bar,baz]>=1; python_version < "3.16"`, name: "foo", spec: ">=1", marker: `python_version < "3.16"`},
		{in: "py7zr (<=0.21.0) ; extra == '7z'", name: "py7zr", spec: "<=0.21.0", marker: "extra == '7z'"},
		{in: "attrs >=19.2.0", name: "attrs", spec: ">=19.2.0"},
		{in: "google-api-core!=2.0.*,<3.0.0", name: "google-api-core", spec: "!=2.0.*,<3.0.0"},
		{in: "zope.event", name: "zope.event"},
		{in: "foo @ https://example.com/foo.whl", wantErr: "URL"},
		{in: "(bad)", wantErr: "(bad)"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			name, spec, marker, err := ParseRequiresDist(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ParseRequiresDist(%q) = %q, %q, %q, %v; want an error naming %q", tt.in, name, spec, marker, err, tt.wantErr)
				}
				return
			}
			if err != nil || name != tt.name || spec != tt.spec || marker != tt.marker {
				t.Errorf("ParseRequiresDist(%q) = %q, %q, %q, %v; want %q, %q, %q", tt.in, name, spec, marker, err, tt.name, tt.spec, tt.marker)
			}
		})
	}
}

// boundsFixture is pins.txt's pins, one of each kind.
var boundsFixture = []Pin{
	{Name: "pycountry", Version: "22.3.5", Kind: KindUnpinned},
	{Name: "importlib-metadata", Version: "9.0.0", Kind: KindNeeded},
	{Name: "Cython", Version: "3.2.9", Kind: KindBuild},
	{Name: "idna", Version: "3.10", Kind: KindUnpinned},
}

func TestBounds(t *testing.T) {
	t.Parallel()
	reqs := []Requirement{
		{Name: "pycountry", Specifier: ">=20.0.0,<27.0.0"},
		{Name: "idna", Specifier: ">=2.5"},
		// Upstream's own pin: not pins.txt's, and Renovate does not move it
		{Name: "comicapi", Specifier: "==3.2.0"},
	}
	locked := []Locked{
		{Name: "comicapi", Version: "3.2.0", Requires: []string{
			"importlib-metadata (<=9.0.0,>=3.3.0)",
			"pycountry (<23.0.0,>=20.7.3)",
			// pip check reads no extra
			"pycountry (<21) ; extra == 'old'",
		}},
		{Name: "anyio", Version: "4.16.0", Requires: []string{
			// A lower bound alone never refuses an update
			"idna>=2.8",
			// It applies on one architecture alone, which is enough
			`importlib-metadata!=9.1.0; platform_machine == "aarch64"`,
			// Not on the image's Python
			`pycountry<20; python_version < "3.10"`,
		}},
		{Name: "pycountry", Version: "22.3.5", Requires: []string{"pycountry<99"}},
	}
	backends := []Locked{
		// A build backend bounds build pins alone
		{Name: "meson-python", Version: "0.22.1", Requires: []string{"pycountry<1", "Cython<3.3"}},
	}
	got, groups, err := Bounds(boundsFixture, reqs, locked, backends, DistEnvs("3.15.2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("Bounds() groups = %+v; want none, as no pin asks another for its own version", groups)
	}
	want := []Bound{
		{Name: "Cython", Allowed: "<3.3", Sources: []string{"meson-python 0.22.1 requires <3.3"}},
		{Name: "importlib-metadata", Allowed: "!=9.1.0,<=9.0.0,>=3.3.0", Sources: []string{
			"anyio 4.16.0 requires !=9.1.0", "comicapi 3.2.0 requires <=9.0.0,>=3.3.0",
		}},
		{Name: "pycountry", Allowed: "<23.0.0,<27.0.0,>=20.0.0,>=20.7.3", Sources: []string{
			"upstream asks for >=20.0.0,<27.0.0", "comicapi 3.2.0 requires <23.0.0,>=20.7.3",
		}},
	}
	if !slices.EqualFunc(got, want, func(a, b Bound) bool {
		return a.Name == b.Name && a.Allowed == b.Allowed && slices.Equal(a.Sources, b.Sources)
	}) {
		t.Errorf("Bounds() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestBoundsUnknownMarker(t *testing.T) {
	t.Parallel()
	locked := []Locked{{Name: "comicapi", Version: "3.2.0", Requires: []string{`pycountry<23; platform_release == "6"`}}}
	_, _, err := Bounds(boundsFixture, nil, locked, nil, DistEnvs("3.15.2"))
	if err == nil || !strings.Contains(err.Error(), "comicapi 3.2.0") || !strings.Contains(err.Error(), "platform_release") {
		t.Errorf("Bounds() with a marker of an unknown variable = %v; want an error naming the package and the variable", err)
	}
}

// TestBoundsPair is a pin asking another for its own version, as
// python-Levenshtein asks Levenshtein: the two move together, in one
// pull request, within the asking pin's bounds, never one alone.
func TestBoundsPair(t *testing.T) {
	t.Parallel()
	pins := []Pin{
		{Name: "python-Levenshtein", Version: "0.27.5", Kind: KindUnpinned},
		{Name: "levenshtein", Version: "0.27.5", Kind: KindNeeded},
		// Asked for at a version not the asker's own: no pair
		{Name: "pydantic", Version: "2.5.0", Kind: KindUnpinned},
		{Name: "pydantic-core", Version: "2.14.1", Kind: KindNeeded},
	}
	reqs := []Requirement{
		{Name: "python-Levenshtein", Specifier: ">=0.12.0,<0.28.0"},
		{Name: "pydantic", Specifier: "<3"},
	}
	locked := []Locked{
		{Name: "python-Levenshtein", Version: "0.27.5", Requires: []string{"Levenshtein==0.27.5"}},
		{Name: "pydantic", Version: "2.5.0", Requires: []string{"pydantic-core==2.14.1"}},
		{Name: "rapidfuzz-user", Version: "1.0", Requires: []string{"levenshtein!=0.27.9"}},
	}
	got, groups, err := Bounds(pins, reqs, locked, nil, DistEnvs("3.15.2"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Bound{
		{Name: "levenshtein", Allowed: "!=0.27.9,<0.28.0,>=0.12.0", Sources: []string{
			"python-Levenshtein 0.27.5 requires ==0.27.5, its own version, so it moves with python-Levenshtein, within python-Levenshtein's bounds",
			"rapidfuzz-user 1.0 requires !=0.27.9",
		}},
		{Name: "pydantic", Allowed: "<3", Sources: []string{"upstream asks for <3"}},
		{Name: "pydantic-core", Allowed: "==2.14.1", Sources: []string{"pydantic 2.5.0 requires ==2.14.1"}},
		{Name: "python-Levenshtein", Allowed: "<0.28.0,>=0.12.0", Sources: []string{"upstream asks for >=0.12.0,<0.28.0"}},
	}
	if !slices.EqualFunc(got, want, func(a, b Bound) bool {
		return a.Name == b.Name && a.Allowed == b.Allowed && slices.Equal(a.Sources, b.Sources)
	}) {
		t.Errorf("Bounds() =\n%+v\nwant\n%+v", got, want)
	}
	wantGroups := []Group{{Asker: "python-Levenshtein", Version: "0.27.5", Asked: "levenshtein"}}
	if !slices.Equal(groups, wantGroups) {
		t.Errorf("Bounds() groups = %+v; want %+v", groups, wantGroups)
	}
}

// TestBoundsBadSpecifier is a specifier Renovate could not read, which
// would stop its every run for the repository: refused, naming who asks.
func TestBoundsBadSpecifier(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"pycountry>=1.0 extra", "pycountry===foo", "pycountry~=2", "pycountry>=1.*", "pycountry<"} {
		locked := []Locked{{Name: "comicapi", Version: "3.2.0", Requires: []string{entry}}}
		_, _, err := Bounds(boundsFixture, nil, locked, nil, DistEnvs("3.15.2"))
		if err == nil || !strings.Contains(err.Error(), "comicapi 3.2.0") || !strings.Contains(err.Error(), "pins.txt") {
			t.Errorf("Bounds() with %q = %v; want an error naming comicapi 3.2.0, and what to change in pins.txt", entry, err)
		}
	}
	// Each form PEP 440 allows is read
	for _, entry := range []string{"pycountry~=2.10", "pycountry==1.*", "pycountry!=2.0.*,<3", "pycountry>=2.5.0.dev0,<3", "pycountry<4.0.0rc1"} {
		locked := []Locked{{Name: "comicapi", Version: "3.2.0", Requires: []string{entry}}}
		if _, _, err := Bounds(boundsFixture, nil, locked, nil, DistEnvs("3.15.2")); err != nil {
			t.Errorf("Bounds() with %q = %v; want it read", entry, err)
		}
	}
}

func TestWriteBounds(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	err := WriteBounds(&b, []Bound{
		{Name: "certifi", Allowed: "<2026.11.20,>=2026.10.18", Sources: []string{"upstream asks for >=2026.10.18,<2026.11.20"}},
		{Name: "docutils", Allowed: "<0.23,>0.18", Sources: []string{"sphinx-rtd-theme 3.1.0 requires <0.23,>0.18"}},
	}, []Group{{Asker: "python-Levenshtein", Version: "0.27.5", Asked: "levenshtein"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "description": "Generated by go run ./cmd/divbast-pins: each pin of python/pins.txt that a package the image installs, or upstream, bounds from above, with every specifier asked of it, so Renovate proposes no version the build refuses. Do not edit it: change the pins, then run go run ./cmd/divbast-pins again.",
  "packageRules": [
    {
      "description": "certifi: upstream asks for >=2026.10.18,<2026.11.20.",
      "matchManagers": [
        "custom.regex"
      ],
      "matchDatasources": [
        "pypi"
      ],
      "matchPackageNames": [
        "certifi"
      ],
      "allowedVersions": "<2026.11.20,>=2026.10.18"
    },
    {
      "description": "docutils: sphinx-rtd-theme 3.1.0 requires <0.23,>0.18.",
      "matchManagers": [
        "custom.regex"
      ],
      "matchDatasources": [
        "pypi"
      ],
      "matchPackageNames": [
        "docutils"
      ],
      "allowedVersions": "<0.23,>0.18"
    },
    {
      "description": "python-Levenshtein 0.27.5 requires levenshtein at its own version, so the two move together, in one pull request.",
      "matchManagers": [
        "custom.regex"
      ],
      "matchDatasources": [
        "pypi"
      ],
      "matchPackageNames": [
        "python-Levenshtein",
        "levenshtein"
      ],
      "groupName": "python-Levenshtein and levenshtein"
    }
  ]
}
`
	if got := b.String(); got != want {
		t.Errorf("WriteBounds() =\n%s\nwant\n%s", got, want)
	}
}
