// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// upstream is testdata/requirements.txt, an upstream's requirements of
// each kind, parsed for the image.
func upstream(t *testing.T) []Requirement {
	t.Helper()
	f, err := os.Open("testdata/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	reqs, err := ParseRequirements(f, linux315)
	if err != nil {
		t.Fatalf("ParseRequirements() = %v", err)
	}
	return reqs
}

// byName finds the requirement for name, by its normalised name.
func byName(reqs []Requirement, name string) (Requirement, bool) {
	i := slices.IndexFunc(reqs, func(r Requirement) bool { return Normalize(r.Name) == Normalize(name) })
	if i < 0 {
		return Requirement{}, false
	}
	return reqs[i], true
}

func TestParseRequirements(t *testing.T) {
	t.Parallel()
	reqs := upstream(t)
	for name, spec := range map[string]string{
		"sabctools": "==10.0.0", "httptools": "==0.9.0", "CT3": "==3.4.0.post5",
		"pycparser": "==3.12", "more-itertools": "==11.2.1", "notify2": "==0.3.1",
		"cryptography": ">=3.0",
	} {
		r, ok := byName(reqs, name)
		if !ok || r.Specifier != spec {
			t.Errorf("%s: %+v, %t; want it with %q", name, r, ok, spec)
		}
	}
	for _, name := range []string{"pywin32", "winrt-runtime", "pyobjc-core", "pygobject", "dbus-python"} {
		if r, ok := byName(reqs, name); ok {
			t.Errorf("holds %+v, which is not for Linux and Python 3.15", r)
		}
	}
	// A version-less line beside a pin for the same package yields the pin
	// alone: pycparser and more-itertools
	if n := len(reqs); n != 9 {
		t.Errorf("%d requirements, want 9, one per package", n)
	}
}

func TestParseRequirementsRefuses(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, text, want string }{
		{"include", "-r other.txt\n", "-r other.txt"},
		{"url", "sabctools @ https://example.com/s.tar.gz\n", "@"},
		{"two-pins", "six==1.17.0\nsix==1.16.0\n", "six"},
		{"bad-marker", "six==1.17.0; os_name == 'posix'\n", "os_name"},
		{"no-name", "==1.0\n", "==1.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRequirements(strings.NewReader(tt.text), linux315)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseRequirements(%q) = %v, want an error naming %q", tt.text, err, tt.want)
			}
		})
	}
}

// pinsText is a pins.txt of each kind.
const pinsText = `# upstream leaves it unpinned: cryptography>=3.0
cryptography==50.0.2

# needed by python-dateutil, which upstream's list leaves out
tzdata==2026.2

# builds cffi from source
setuptools==82.0.1
`

func TestMergePins(t *testing.T) {
	t.Parallel()
	reqs := upstream(t)
	pins, err := ParsePins(strings.NewReader(pinsText))
	if err != nil {
		t.Fatalf("ParsePins() = %v", err)
	}
	got, err := Merge(reqs, pins)
	if err != nil {
		t.Fatalf("Merge() = %v", err)
	}
	if n := len(got); n != 10 {
		t.Errorf("Merge() gave %d packages, want upstream's 9 and tzdata", n)
	}
	for name, version := range map[string]string{"cryptography": "50.0.2", "tzdata": "2026.2", "sabctools": "10.0.0"} {
		i := slices.IndexFunc(got, func(p Pinned) bool { return p.Name == name })
		if i < 0 || got[i].Version != version {
			t.Errorf("Merge() has %s at %v, want %s", name, got, version)
		}
	}
	if slices.ContainsFunc(got, func(p Pinned) bool { return p.Name == "setuptools" }) {
		t.Error("Merge() put a build backend among the image's packages")
	}
	// Upstream's == pins, and those alone, are upstream's
	for name, upstream := range map[string]bool{"sabctools": true, "cryptography": false, "tzdata": false} {
		if i := slices.IndexFunc(got, func(p Pinned) bool { return p.Name == name }); i < 0 || got[i].Upstream != upstream {
			t.Errorf("Merge() has %s as %+v, want Upstream %t", name, got, upstream)
		}
	}
	if b := Build(pins); len(b) != 1 || b[0].Name != "setuptools" || b[0].Version != "82.0.1" {
		t.Errorf("Build() = %+v, want setuptools 82.0.1 alone", b)
	}

	for _, tt := range []struct{ name, pins, want string }{
		// Upstream leaves cryptography unpinned, and nothing pins it
		{"unpinned-missing", "", "cryptography>=3.0, which upstream leaves unpinned, needs a line in pins.txt: " +
			"cryptography==<version> under # upstream leaves it unpinned: cryptography>=3.0"},
		// Upstream's own pin wins
		{"overrides-upstream", pinsText + "\n# upstream leaves it unpinned: six\nsix==1.16.0\n", "six"},
		{"outside-range", "# upstream leaves it unpinned: cryptography>=3.0\ncryptography==2.9\n", "2.9"},
		{"needed-but-listed", pinsText + "\n# needed by apprise\nhttptools==2.0.0\n", "httptools"},
		{"comment-mismatch", "# upstream leaves it unpinned: cryptography>=4.0\ncryptography==50.0.2\n", ">=4.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pins, err := ParsePins(strings.NewReader(tt.pins))
			if err == nil {
				_, err = Merge(reqs, pins)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Merge() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestParsePinsRefuses(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, text, want string }{
		{"no-comment", "cryptography==50.0.2\n", "line 1"},
		{"unknown-kind", "# because\ncryptography==50.0.2\n", "because"},
		{"range", "# builds cffi from source\nsetuptools>=82\n", "setuptools>=82"},
		{"twice", "# builds cffi from source\nsetuptools==82.0.1\n# builds PyYAML from source\nsetuptools==82.0.0\n", "twice"},
		{"dangling", "# builds cffi from source\n", "line 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParsePins(strings.NewReader(tt.text))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParsePins(%q) = %v, want an error naming %q", tt.text, err, tt.want)
			}
		})
	}
}

func TestVersionOrder(t *testing.T) {
	t.Parallel()
	// Each is lower than the next, as PEP 440 orders them
	order := []string{"1.0.dev1", "1.0a1", "1.0b2", "1.0rc1", "1.0", "1.0.post1", "1.0.1", "1.1", "3.0", "3.12", "50.0.2", "1!0.1"}
	for i := 0; i+1 < len(order); i++ {
		c, err := compareVersions(order[i], order[i+1])
		if err != nil || c >= 0 {
			t.Errorf("compareVersions(%s, %s) = %d, %v; want below 0", order[i], order[i+1], c, err)
		}
	}
	if c, err := compareVersions("3.0", "3.0.0"); err != nil || c != 0 {
		t.Errorf("compareVersions(3.0, 3.0.0) = %d, %v; want 0", c, err)
	}
	for spec, want := range map[string]bool{">=3.0": true, "<50": false, ">=80.9.0,<=82.0.1": false, "!=50.0.2": false, "==50.0.2": true} {
		got, err := Satisfies("50.0.2", spec)
		if err != nil || got != want {
			t.Errorf("Satisfies(50.0.2, %q) = %t, %v; want %t", spec, got, err, want)
		}
	}
	if _, err := Satisfies("50.0.2", "~=50.0"); err == nil {
		t.Error("Satisfies with ~=: nil error, want one naming the operator")
	}
}
