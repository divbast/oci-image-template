// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/divbast/oci-image-template/layers/python/internal/pins"
)

const commit = "ffc619947b5ba4e6520e3a0bc70680024e184b2f"

// containerfile is the start of an app's Containerfile, as the tool reads
// it: the commit of the upstream it builds, and DHI's Python.
const containerfile = `ARG APP_VERSION=1.2.0
ARG APP_COMMIT=` + commit + `
ARG RUNTIME_IMAGE=dhi.io/python:3.15.2-debian13@sha256:c6c9414d78bd285be1a4dacfb94ae61732c77019605b1678792823e319b720f6
FROM dhi.io/python:3.15.2-debian13-dev@sha256:c73e2cb9e8d56cfab7f01b99f67b2c830e84e7397dfe666d8bfaf5a69ad0ce85 AS deps
FROM ${RUNTIME_IMAGE}
`

const pinsTxt = `# upstream leaves it unpinned: cryptography>=3.0
cryptography==50.0.2

# builds cryptography and ujson from source
setuptools==82.0.1
`

// requirements is a requirements list of the kinds an app's holds: pins,
// a range, and a package for another platform.
const requirements = `six==1.17.0
ujson==6.0.0 # a C extension
cryptography>=3.0
pywin32==312; sys_platform == 'win32'
`

// upstreamPath is where the test's GitHub serves the upstream's own
// requirements.txt, at the Containerfile's commit.
const upstreamPath = "/owner/app/" + commit + "/requirements.txt"

// servers stands in for GitHub and PyPI, on one server that speaks TLS,
// with each release in yanked yanked on PyPI, for broken metadata. It
// returns the sources and the server's URL.
func servers(t *testing.T, yanked ...string) (src pins.Sources, base string) {
	t.Helper()
	release := func(files ...string) []map[string]any {
		out := make([]map[string]any, 0, len(files))
		for _, f := range files {
			kind := "bdist_wheel"
			if strings.HasSuffix(f, ".tar.gz") {
				kind = "sdist"
			}
			h := sha256.Sum256([]byte(f))
			pulled := slices.ContainsFunc(yanked, func(r string) bool { return strings.HasPrefix(f, r+"-") })
			out = append(out, map[string]any{
				"filename": f, "packagetype": kind, "url": "https://files.example/" + f,
				"digests": map[string]string{"sha256": hex.EncodeToString(h[:])},
				"yanked":  pulled, "yanked_reason": map[bool]any{true: "broken metadata", false: nil}[pulled],
			})
		}
		return out
	}
	pypi := map[string][]map[string]any{
		"/pypi/six/1.17.0/json":          release("six-1.17.0-py2.py3-none-any.whl", "six-1.17.0.tar.gz"),
		"/pypi/ujson/6.0.0/json":         release("ujson-6.0.0-cp315-cp315-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", "ujson-6.0.0.tar.gz"),
		"/pypi/cryptography/50.0.2/json": release("cryptography-50.0.2-cp311-abi3-manylinux_2_34_x86_64.whl", "cryptography-50.0.2.tar.gz"),
		"/pypi/setuptools/82.0.1/json":   release("setuptools-82.0.1-py3-none-any.whl", "setuptools-82.0.1.tar.gz"),
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == upstreamPath:
			_, _ = w.Write([]byte(requirements))
		case pypi[r.URL.Path] != nil:
			_ = json.NewEncoder(w).Encode(map[string]any{"urls": pypi[r.URL.Path]})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return pins.Sources{PyPI: &pins.PyPI{Base: srv.URL, Client: srv.Client()}, Client: srv.Client()}, srv.URL
}

// repo writes a repository holding the Containerfile, pins.txt, and
// requirements.in as in, or none when in is empty.
func repo(t *testing.T, in string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "python"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"Containerfile": containerfile, "python/pins.txt": pinsTxt}
	if in != "" {
		files["python/requirements.in"] = in
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) //nolint:gosec // G304: the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// runIn runs the command in dir, and returns its exit code, stdout and
// stderr.
func runIn(t *testing.T, src pins.Sources, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = run(t.Context(), append([]string{"-C", dir}, args...), src, &out, &errs)
	return code, out.String(), errs.String()
}

// checkLocks holds the three files generate wrote in dir to the
// requirements above, merged with pinsTxt.
func checkLocks(t *testing.T, dir string) {
	t.Helper()
	lock := read(t, dir, "python/requirements.lock")
	for _, want := range []string{"--no-binary cryptography,ujson\n", "six==1.17.0 \\\n", "cryptography==50.0.2 \\\n", "ujson==6.0.0 \\\n"} {
		if !strings.Contains(lock, want) {
			t.Errorf("requirements.lock =\n%s\nwant it to hold %q", lock, want)
		}
	}
	if strings.Contains(lock, "pywin32") || strings.Contains(lock, "setuptools") {
		t.Errorf("requirements.lock =\n%s\nwant no Windows package and no build backend", lock)
	}
	if b := read(t, dir, "python/build.lock"); !strings.Contains(b, "setuptools==82.0.1 \\\n") {
		t.Errorf("build.lock =\n%s\nwant setuptools 82.0.1", b)
	}
	// Every file either lock lets pip take, by its sha256 and URL, and no other
	files := read(t, dir, "python/files.txt")
	for _, f := range []string{"six-1.17.0-py2.py3-none-any.whl", "ujson-6.0.0.tar.gz", "cryptography-50.0.2.tar.gz", "setuptools-82.0.1-py3-none-any.whl"} {
		h := sha256.Sum256([]byte(f))
		if want := hex.EncodeToString(h[:]) + " https://files.example/" + f + "\n"; !strings.Contains(files, want) {
			t.Errorf("files.txt =\n%s\nwant it to hold %q", files, want)
		}
	}
	if n := strings.Count(files, " https://"); n != 4 {
		t.Errorf("files.txt names %d files, want the 4 the locks take", n)
	}
	if got := read(t, dir, "Containerfile"); got != containerfile {
		t.Error("the Containerfile changed, which the tool only reads")
	}
}

// TestCheckMode is requirements.in holding the app's own requirements.
func TestCheckMode(t *testing.T) {
	t.Parallel()
	src, _ := servers(t)
	dir := repo(t, requirements)

	// Nothing generated yet: stale
	if code, out, _ := runIn(t, src, dir, "-check"); code != 1 || !strings.Contains(out, "+++ python/requirements.lock") {
		t.Errorf("-check before any lock = %d, %q; want 1 and a diff creating the lock", code, out)
	}
	if code, out, errs := runIn(t, src, dir); code != 0 || strings.Contains(out, "Containerfile") {
		t.Fatalf("generate = %d, %q; stderr: %s; want 0, and the Containerfile not written", code, out, errs)
	}
	checkLocks(t, dir)
	if lock := read(t, dir, "python/requirements.lock"); !strings.Contains(lock, "from python/requirements.in\n# and python/pins.txt") {
		t.Errorf("requirements.lock =\n%s\nwant its header to name requirements.in", lock)
	}

	// Current: nothing to say
	if code, out, errs := runIn(t, src, dir, "-check"); code != 0 || out != "" {
		t.Errorf("-check when current = %d, %q (stderr %q); want 0 and nothing", code, out, errs)
	}

	// A lock edited by hand is stale, and -check writes nothing
	lock := read(t, dir, "python/requirements.lock")
	stale := strings.Replace(lock, "six==1.17.0", "six==1.16.0", 1)
	if err := os.WriteFile(filepath.Join(dir, "python", "requirements.lock"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runIn(t, src, dir, "-check")
	if code != 1 || !strings.Contains(out, "--- python/requirements.lock") || !strings.Contains(out, "-six==1.16.0 \\") || !strings.Contains(out, "+six==1.17.0 \\") {
		t.Errorf("-check of a stale lock = %d,\n%s\nwant 1 and a unified diff of the change", code, out)
	}
	if got := read(t, dir, "python/requirements.lock"); got != stale {
		t.Error("-check rewrote the lock")
	}
}

// TestIncludedRequirements is requirements.in naming the upstream's own
// requirements.txt, at the commit the Containerfile pins, by an ARG.
func TestIncludedRequirements(t *testing.T) {
	t.Parallel()
	src, base := servers(t)
	dir := repo(t, "# The upstream's own list, at the commit it is built from\n-r "+base+"/owner/app/${APP_COMMIT}/requirements.txt\n")
	if code, _, errs := runIn(t, src, dir); code != 0 {
		t.Fatalf("generate = %d; stderr: %s", code, errs)
	}
	checkLocks(t, dir)
	if lock := read(t, dir, "python/requirements.lock"); !strings.Contains(lock, base+upstreamPath) {
		t.Errorf("requirements.lock =\n%s\nwant its header to name %s", lock, base+upstreamPath)
	}
	if code, out, errs := runIn(t, src, dir, "-check"); code != 0 || out != "" {
		t.Errorf("-check when current = %d, %q (stderr %q); want 0 and nothing", code, out, errs)
	}
}

// TestRequirementsRefused is each requirements.in the tool will not read:
// it fails, naming why, and writes nothing.
func TestRequirementsRefused(t *testing.T) {
	t.Parallel()
	src, base := servers(t)
	include := "-r " + base + "/owner/app/${APP_COMMIT}/requirements.txt\n"
	for _, tt := range []struct{ name, in, want string }{
		{"missing", "", "python/requirements.in"},
		{"http", "-r http://example.com/" + commit + "/requirements.txt\n", "https"},
		{"no-commit", "-r " + base + "/owner/app/main/requirements.txt\n", "commit"},
		{"undefined-arg", "-r " + base + "/owner/app/${APP_REF}/requirements.txt\n", "APP_REF"},
		{"mixed", include + "six==1.17.0\n", "one -r line"},
		{"two-includes", include + include, "one -r line"},
		{"not-served", "-r " + base + "/owner/app/${APP_COMMIT}/other.txt\n", "404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := repo(t, tt.in)
			if code, _, errs := runIn(t, src, dir); code != 1 || !strings.Contains(errs, tt.want) {
				t.Errorf("generate = %d, stderr %q; want 1, naming %q", code, errs, tt.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "python", "requirements.lock")); err == nil {
				t.Error("a lock was written")
			}
		})
	}
}

// TestYankedUpstreamPin is a release the requirements pin with == that
// PyPI has since yanked: pip still installs it, so the tool keeps it, says
// so in the lock and in a note, and exits 0. One pins.txt names is refused.
func TestYankedUpstreamPin(t *testing.T) {
	t.Parallel()
	src, _ := servers(t, "six")
	dir := repo(t, requirements)
	code, _, errs := runIn(t, src, dir)
	if code != 0 {
		t.Fatalf("run() = %d, stderr %q; want 0", code, errs)
	}
	if !strings.Contains(errs, "note: six 1.17.0") || !strings.Contains(errs, "yanked") || !strings.Contains(errs, "broken metadata") {
		t.Errorf("stderr = %q; want a note naming six 1.17.0, yanked, and PyPI's reason", errs)
	}
	if lock := read(t, dir, "python/requirements.lock"); !strings.Contains(lock, "# pinned by upstream; yanked on PyPI: broken metadata\nsix==1.17.0 \\\n") {
		t.Errorf("requirements.lock =\n%s\nwant six's comment to say it is yanked, and why", lock)
	}

	src, _ = servers(t, "cryptography")
	if code, _, errs = runIn(t, src, repo(t, requirements)); code != 1 || !strings.Contains(errs, "cryptography 50.0.2 is yanked") {
		t.Errorf("run() with pins.txt's cryptography yanked = %d, stderr %q; want 1, naming it", code, errs)
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"extra"}, pins.Sources{}, &stdout, &stderr); code != 2 {
		t.Errorf("run(extra) = %d, want 2", code)
	}
}
