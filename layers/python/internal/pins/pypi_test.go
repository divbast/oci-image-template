// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// file is one file of a release, as PyPI's JSON API lists it.
type file struct {
	Filename    string            `json:"filename"`
	PackageType string            `json:"packagetype"`
	URL         string            `json:"url"`
	Digests     map[string]string `json:"digests"`
	Yanked      bool              `json:"yanked"`
	Reason      string            `json:"yanked_reason,omitempty"`
}

// sdist and wheel are a release's files, each with a sha256 made from its
// name, so a test can tell which was chosen.
func sdist(name string) file {
	return file{Filename: name, PackageType: "sdist", URL: "https://files.example/" + name, Digests: map[string]string{"sha256": sum(name)}}
}

func wheel(name string) file {
	return file{Filename: name, PackageType: "bdist_wheel", URL: "https://files.example/" + name, Digests: map[string]string{"sha256": sum(name)}}
}

// sum is a sha256-shaped stand-in for name's digest.
func sum(name string) string {
	return strings.Repeat("0", 64-len(name)%64) + strings.Repeat("a", len(name)%64)
}

// fakePyPI serves PyPI's JSON API for releases, keyed "<name>/<version>",
// and counts each request in hits, when not nil.
func fakePyPI(t *testing.T, releases map[string][]file) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/pypi/"), "/json")
		files, ok := releases[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"urls": files}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChooseFiles(t *testing.T) {
	t.Parallel()
	srv := fakePyPI(t, map[string][]file{
		"idna/3.20":  {sdist("idna-3.20.tar.gz"), wheel("idna-3.20-py3-none-any.whl")},
		"six/1.17.0": {sdist("six-1.17.0.tar.gz"), wheel("six-1.17.0-py2.py3-none-any.whl")},
		// A compiled wheel for every platform, and a pure one: the pure one
		"charset-normalizer/3.5.2": {
			wheel("charset_normalizer-3.5.2-cp315-cp315-manylinux_2_17_x86_64.manylinux2014_x86_64.whl"),
			wheel("charset_normalizer-3.5.2-py3-none-any.whl"), sdist("charset_normalizer-3.5.2.tar.gz"),
		},
		"ujson/6.0.0": {
			wheel("ujson-6.0.0-cp315-cp315-manylinux_2_17_aarch64.manylinux2014_aarch64.whl"),
			wheel("ujson-6.0.0-cp315-cp315-manylinux_2_17_x86_64.manylinux2014_x86_64.whl"),
			sdist("ujson-6.0.0.tar.gz"),
		},
		"windows-only/1.0": {wheel("windows_only-1.0-cp315-cp315-win_amd64.whl")},
		"pulled/1.0":       {func() file { f := sdist("pulled-1.0.tar.gz"); f.Yanked, f.Reason = true, "broken metadata"; return f }()},
	})
	c := &PyPI{Base: srv.URL, Client: srv.Client()}
	for _, tt := range []struct {
		name, version, want string
		fromSource          bool
		wantErr             string
		// upstream is a release the requirements pin with ==, and yanked the reason
		// PyPI gives for yanking it, which the lock must keep
		upstream bool
		yanked   string
	}{
		{name: "idna", version: "3.20", want: "idna-3.20-py3-none-any.whl"},
		{name: "six", version: "1.17.0", want: "six-1.17.0-py2.py3-none-any.whl"},
		{name: "charset_normalizer", version: "3.5.2", want: "charset_normalizer-3.5.2-py3-none-any.whl"},
		{name: "ujson", version: "6.0.0", want: "ujson-6.0.0.tar.gz", fromSource: true},
		{name: "windows-only", version: "1.0", wantErr: "neither a pure wheel nor a source release"},
		// A yanked release pins.txt names is refused; one upstream pins with
		// == is kept, as pip keeps it, saying why it was yanked
		{name: "pulled", version: "1.0", wantErr: "yanked"},
		{name: "pulled", version: "1.0", upstream: true, want: "pulled-1.0.tar.gz", fromSource: true, yanked: "broken metadata"},
		{name: "absent", version: "1.0", wantErr: "404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ChooseFiles(t.Context(), c, Pinned{Name: tt.name, Version: tt.version, Upstream: tt.upstream})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), tt.name) {
					t.Errorf("ChooseFiles(%s) = %+v, %v; want an error naming it and %q", tt.name, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got.Files) != 1 || got.Files[0].Name != tt.want || got.Files[0].SHA256 != sum(tt.want) || got.FromSource != tt.fromSource {
				t.Errorf("ChooseFiles(%s) = %+v, %v; want %s alone, from source %t", tt.name, got, err, tt.want, tt.fromSource)
			}
			if got.Yanked != (tt.yanked != "") || got.YankedReason != tt.yanked {
				t.Errorf("ChooseFiles(%s) = yanked %t, %q; want %q", tt.name, got.Yanked, got.YankedReason, tt.yanked)
			}
		})
	}
}

func TestChooseBuildFiles(t *testing.T) {
	t.Parallel()
	srv := fakePyPI(t, map[string][]file{
		"setuptools/82.0.1": {wheel("setuptools-82.0.1-py3-none-any.whl"), sdist("setuptools-82.0.1.tar.gz")},
		// A Rust binary with no pure wheel: each Linux glibc wheel of the
		// two architectures the image builds for
		"maturin/1.14.1": {
			wheel("maturin-1.14.1-py3-none-manylinux_2_12_x86_64.manylinux2010_x86_64.musllinux_1_1_x86_64.whl"),
			wheel("maturin-1.14.1-py3-none-manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64.whl"),
			wheel("maturin-1.14.1-py3-none-musllinux_1_1_armv7l.whl"),
			wheel("maturin-1.14.1-py3-none-win_amd64.whl"),
			wheel("maturin-1.14.1-py3-none-macosx_10_12_x86_64.macosx_11_0_arm64.macosx_10_12_universal2.whl"),
			sdist("maturin-1.14.1.tar.gz"),
		},
		"musl-only/1.0": {wheel("musl_only-1.0-py3-none-musllinux_1_1_x86_64.whl")},
		"pulled/2.0":    {func() file { f := wheel("pulled-2.0-py3-none-any.whl"); f.Yanked = true; return f }()},
	})
	c := &PyPI{Base: srv.URL, Client: srv.Client()}
	got, err := ChooseBuildFiles(t.Context(), c, Pinned{Name: "setuptools", Version: "82.0.1"})
	if err != nil || len(got.Files) != 1 || got.Files[0].Name != "setuptools-82.0.1-py3-none-any.whl" {
		t.Errorf("ChooseBuildFiles(setuptools) = %+v, %v; want its pure wheel alone", got, err)
	}
	got, err = ChooseBuildFiles(t.Context(), c, Pinned{Name: "maturin", Version: "1.14.1"})
	names := make([]string, 0, len(got.Files))
	for _, f := range got.Files {
		names = append(names, f.Name)
	}
	want := []string{
		"maturin-1.14.1-py3-none-manylinux_2_12_x86_64.manylinux2010_x86_64.musllinux_1_1_x86_64.whl",
		"maturin-1.14.1-py3-none-manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64.whl",
	}
	slices.Sort(names)
	if err != nil || !slices.Equal(names, want) {
		t.Errorf("ChooseBuildFiles(maturin) = %q, %v; want %q", names, err, want)
	}
	if _, err = ChooseBuildFiles(t.Context(), c, Pinned{Name: "musl-only", Version: "1.0"}); err == nil || !strings.Contains(err.Error(), "musl-only") {
		t.Errorf("ChooseBuildFiles(musl-only) = %v, want an error naming it", err)
	}
	// A build backend is pins.txt's alone, so a yanked one is refused
	if _, err = ChooseBuildFiles(t.Context(), c, Pinned{Name: "pulled", Version: "2.0"}); err == nil || !strings.Contains(err.Error(), "pulled 2.0 is yanked") {
		t.Errorf("ChooseBuildFiles(yanked) = %v, want an error naming it as yanked", err)
	}
}

// TestGetRetries is a server that fails twice with 503 then answers, and
// one that answers 404: the first is tried again, the second is not.
func TestGetRetries(t *testing.T) {
	t.Parallel()
	var tries, missing atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			missing.Add(1)
			http.NotFound(w, r)
			return
		}
		if tries.Add(1) <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	delays := []time.Duration{0, 0, 0}
	body, err := get(t.Context(), srv.Client(), srv.URL+"/flaky", delays)
	if err != nil || string(body) != "ok" || tries.Load() != 3 {
		t.Errorf("get(flaky) = %q, %v after %d tries; want ok after 3", body, err, tries.Load())
	}
	if _, err = get(t.Context(), srv.Client(), srv.URL+"/missing", delays); err == nil || missing.Load() != 1 {
		t.Errorf("get(missing) = %v after %d tries; want a 404 after 1", err, missing.Load())
	}
}
