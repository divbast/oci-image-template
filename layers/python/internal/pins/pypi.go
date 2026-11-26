// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultDelays are the waits between the tries of a request that may
// pass on a second try: three retries, 10 s, 30 s and 90 s apart, as the
// Containerfiles retry their fetches, so one dropped connection does not
// fail a run.
var DefaultDelays = []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second}

// maxBody bounds what any request reads.
const maxBody = 64 << 20

// tryTimeout bounds one try of a request.
const tryTimeout = 2 * time.Minute

// get fetches u with client, retrying after each of delays, DefaultDelays
// when nil, a try that failed to connect or that the server answered with
// a 5xx or a 429. Any other answer but 200 fails at once, naming it.
func get(ctx context.Context, client *http.Client, u string, delays []time.Duration) ([]byte, error) {
	if delays == nil {
		delays = DefaultDelays
	}
	for try := 0; ; try++ {
		body, retry, err := getOnce(ctx, client, u)
		if err == nil || !retry || try == len(delays) {
			return body, err
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(err, ctx.Err())
		case <-time.After(delays[try]):
		}
	}
}

func getOnce(ctx context.Context, client *http.Client, u string) (body []byte, retry bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, tryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("fetch %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only, so nothing to lose
	if resp.StatusCode != http.StatusOK {
		retry = resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
		return nil, retry, fmt.Errorf("fetch %s: %s", u, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err == nil && len(body) > maxBody {
		err = fmt.Errorf("fetch %s: more than %d bytes", u, maxBody)
	}
	if err != nil {
		return nil, true, err
	}
	return body, false, nil
}

// PyPI reads releases from PyPI's JSON API.
type PyPI struct {
	// Base is the API's root, https://pypi.org.
	Base   string
	Client *http.Client
	// Delays are the waits between tries: DefaultDelays when nil.
	Delays []time.Duration
}

// File is one file of a release. Yanked is PyPI's mark on a release
// withdrawn under PEP 592, with the reason PyPI gives, on one line.
type File struct {
	Name, URL, SHA256 string
	Wheel             bool
	Yanked            bool
	YankedReason      string
}

// files reads the files of name's release version.
func (c *PyPI) files(ctx context.Context, name, version string) ([]File, error) {
	body, err := get(ctx, c.Client, c.Base+"/pypi/"+url.PathEscape(Normalize(name))+"/"+url.PathEscape(version)+"/json", c.Delays)
	if err != nil {
		return nil, err
	}
	var rel struct {
		URLs []struct {
			Filename    string `json:"filename"`
			PackageType string `json:"packagetype"`
			URL         string `json:"url"`
			Digests     struct {
				SHA256 string `json:"sha256"`
			} `json:"digests"`
			Yanked       bool    `json:"yanked"`
			YankedReason *string `json:"yanked_reason"`
		} `json:"urls"`
	}
	if err = json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("%s %s: PyPI's answer is no release: %w", name, version, err)
	}
	var out []File
	for _, f := range rel.URLs {
		if !sha256Hex.MatchString(f.Digests.SHA256) {
			return nil, fmt.Errorf("%s %s: PyPI gives %s no sha256", name, version, f.Filename)
		}
		reason := ""
		if f.YankedReason != nil {
			reason = strings.Join(strings.Fields(*f.YankedReason), " ")
		}
		out = append(out, File{
			Name: f.Filename, URL: f.URL, SHA256: f.Digests.SHA256, Wheel: f.PackageType == "bdist_wheel",
			Yanked: f.Yanked, YankedReason: reason,
		})
	}
	slices.SortFunc(out, func(a, b File) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// wheelTags reads the Python, ABI and platform tags of a wheel's file
// name, each a set of the dot-separated tags it holds.
func wheelTags(name string) (py, abi, plat []string, ok bool) {
	parts := strings.Split(strings.TrimSuffix(name, ".whl"), "-")
	if len(parts) < 5 || !strings.HasSuffix(name, ".whl") {
		return nil, nil, nil, false
	}
	n := len(parts)
	return strings.Split(parts[n-3], "."), strings.Split(parts[n-2], "."), strings.Split(parts[n-1], "."), true
}

// pure reports whether f is a wheel any Python 3 installs on any
// platform: a py3 tag, no ABI, and the any platform.
func pure(f File) bool {
	py, abi, plat, ok := wheelTags(f.Name)
	return ok && f.Wheel && slices.ContainsFunc(py, func(t string) bool { return strings.HasPrefix(t, "py3") }) &&
		slices.Contains(abi, "none") && slices.Contains(plat, "any")
}

// Locked is a package and the files the lock lets pip fetch for it.
type Locked struct {
	Name, Version, Why string
	Files              []File
	// FromSource is true for a source release, which the build turns into
	// a wheel.
	FromSource bool
	// Yanked is true for a release PyPI has yanked that upstream pins with
	// ==, which pip installs all the same, and YankedReason PyPI's reason.
	Yanked       bool
	YankedReason string
}

// yanked reports whether PyPI has yanked the release files are of, and
// the reason it gives.
func yanked(files []File) (yes bool, reason string) {
	for _, f := range files {
		if f.Yanked {
			return true, f.YankedReason
		}
	}
	return false, ""
}

// refuseYanked is the error for a yanked release that pins.txt names.
func refuseYanked(p Pinned, reason string) error {
	if reason != "" {
		reason = " (" + reason + ")"
	}
	return fmt.Errorf("%s %s is yanked on PyPI%s: pin another release in pins.txt", p.Name, p.Version, reason)
}

// ChooseFiles picks the one file of p's release the image installs: its
// pure wheel when it has one, and else its source release, which the build
// compiles against the image's own libraries. A release with neither is an
// error, and so is a yanked one, unless upstream pins it with ==: pip
// installs a yanked release pinned so, and nothing here may move that pin.
func ChooseFiles(ctx context.Context, c *PyPI, p Pinned) (Locked, error) {
	files, err := c.files(ctx, p.Name, p.Version)
	if err != nil {
		return Locked{}, fmt.Errorf("%s %s: %w", p.Name, p.Version, err)
	}
	l := Locked{Name: p.Name, Version: p.Version, Why: p.Why}
	if y, reason := yanked(files); y {
		if !p.Upstream {
			return Locked{}, refuseYanked(p, reason)
		}
		l.Yanked, l.YankedReason = true, reason
	}
	if i := slices.IndexFunc(files, pure); i >= 0 {
		l.Files = []File{files[i]}
		return l, nil
	}
	// A .tar.gz before a .zip, should a release have both
	slices.SortStableFunc(files, func(a, b File) int {
		return cmp.Compare(btoi(!strings.HasSuffix(a.Name, ".tar.gz")), btoi(!strings.HasSuffix(b.Name, ".tar.gz")))
	})
	if i := slices.IndexFunc(files, func(f File) bool { return !f.Wheel }); i >= 0 {
		l.Files, l.FromSource = []File{files[i]}, true
		return l, nil
	}
	return Locked{}, fmt.Errorf("%s %s has neither a pure wheel nor a source release on PyPI", p.Name, p.Version)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The architectures the image builds for, as wheels name them, and the
// newest glibc a manylinux wheel may need: Debian 13's, 2.41.
var (
	arches = []string{"x86_64", "aarch64"}
	glibc  = 41
)

var manylinux = regexp.MustCompile(`^manylinux(?:_2_(\d+)|(1|2010|2014))_(x86_64|aarch64)$`)

// glibcWheel reports the architecture of f, a Linux glibc wheel any
// CPython 3.15 on the image's glibc installs, or "" for any other file.
func glibcWheel(f File) string {
	py, abi, plat, ok := wheelTags(f.Name)
	if !ok || !f.Wheel {
		return ""
	}
	if !slices.ContainsFunc(py, func(t string) bool {
		return t == "py3" || t == "cp315" || (strings.HasPrefix(t, "cp3") && slices.Contains(abi, "abi3"))
	}) {
		return ""
	}
	for _, t := range plat {
		m := manylinux.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		need := map[string]int{"1": 5, "2010": 12, "2014": 17}[m[2]]
		if m[1] != "" {
			need, _ = strconv.Atoi(m[1]) // the pattern holds digits alone
		}
		if need <= glibc {
			return m[3]
		}
	}
	return ""
}

// ChooseBuildFiles picks the files of p's release a build stage installs:
// its pure wheel when it has one, and else every Linux glibc wheel for
// each architecture the image builds for, so the lock lets pip take
// whichever it prefers there. A build backend never ships, so a binary
// wheel of one, such as maturin's, which has no pure wheel, is a build
// tool, as the compilers are.
func ChooseBuildFiles(ctx context.Context, c *PyPI, p Pinned) (Locked, error) {
	files, err := c.files(ctx, p.Name, p.Version)
	if err != nil {
		return Locked{}, fmt.Errorf("%s %s: %w", p.Name, p.Version, err)
	}
	// Every build backend is pins.txt's
	if y, reason := yanked(files); y {
		return Locked{}, refuseYanked(p, reason)
	}
	l := Locked{Name: p.Name, Version: p.Version, Why: p.Why}
	if i := slices.IndexFunc(files, pure); i >= 0 {
		l.Files = []File{files[i]}
		return l, nil
	}
	for _, arch := range arches {
		var found []File
		for _, f := range files {
			if glibcWheel(f) == arch {
				found = append(found, f)
			}
		}
		if len(found) == 0 {
			return Locked{}, fmt.Errorf("%s %s has no pure wheel, and no Linux glibc wheel for %s, on PyPI", p.Name, p.Version, arch)
		}
		l.Files = append(l.Files, found...)
	}
	return l, nil
}
