// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package globaljson tests test/global-json.sh, the check and rewrite the
// Containerfile's backend stage runs over upstream's global.json. The
// script runs against fixtures written to a temporary file: a global.json
// as an upstream ships it and the shapes a release might give it, which it
// must rewrite, a source with none, which it must leave alone, and every
// way it can refuse, a .NET major on one side and not the other among
// them, each seen refusing with the file left as it was.
package globaljson

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// script is the script under test, from this package's directory.
const script = "../global-json.sh"

// sdk is the SDK the stage's image reports, wherever a test names none.
const sdk = "10.0.404"

// upstream is a global.json as an upstream ships it: indented, with the
// sdk object holding its version alone.
const upstream = "{\n  \"sdk\": {\n    \"version\": \"10.0.404\"\n  }\n}\n"

// rewritten is what the script must make of a file naming 10.0.404.
const rewritten = `{"sdk":{"version":"10.0.404","rollForward":"latestFeature"}}` + "\n"

// The two ways a .NET major can stand on one side alone, each with the
// steps to take, for global.json's major.minor and then the image's.
const (
	upstreamAhead = "ERROR: global.json asks for the .NET %[1]s SDK; this stage's image has .NET %[2]s. Approve the .NET %[1]s major on the Dependency Dashboard and move both together: CLAUDE.md, 'A .NET major'.\n"
	imageAhead    = "ERROR: global.json asks for the .NET %[1]s SDK; this stage's image has .NET %[2]s. A .NET major waits until upstream retargets, then moves with the app's own update: CLAUDE.md, 'A .NET major'.\n"
)

// result is how a run of the script ended.
type result struct {
	code           int
	stdout, stderr string
}

// TestRewrite checks the script keeps upstream's sdk.version as the floor
// and gives it the latestFeature policy, whatever else the sdk object
// held.
func TestRewrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, sdk, want string
	}{
		{name: "upstream", in: upstream, want: rewritten},
		{name: "one-line", in: `{"sdk":{"version":"10.0.404"}}`, want: rewritten},
		{
			// A policy of upstream's own is replaced, not kept beside ours
			name: "policy-replaced",
			in:   `{"sdk":{"version":"10.0.404","rollForward":"disable"}}`,
			want: rewritten,
		},
		{
			name: "version-after-other-keys",
			in:   `{"sdk":{"allowPrerelease":false,"version":"10.0.404"}}`,
			want: rewritten,
		},
		{
			// A preview SDK, should upstream ever pin one, built by the
			// image's SDK of the same major
			name: "preview",
			in:   `{"sdk":{"version":"11.0.100-rc.2.25502.107"}}`,
			sdk:  "11.0.100",
			want: `{"sdk":{"version":"11.0.100-rc.2.25502.107","rollForward":"latestFeature"}}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := fixture(t, tt.in)
			r := run(t, file, tt.sdk)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("exit %d, want 0 with nothing on stderr; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if got := read(t, file); got != tt.want {
				t.Errorf("the file holds %q, want %q", got, tt.want)
			}
			if r.stdout != tt.want {
				t.Errorf("stdout is %q, want what the file now holds, %q", r.stdout, tt.want)
			}
		})
	}
}

// TestSDKMatches checks the image's SDK may be a later band of the major
// and minor global.json names, which is what Renovate moves it to, and the
// file is still rewritten.
func TestSDKMatches(t *testing.T) {
	t.Parallel()

	file := fixture(t, upstream)
	r := run(t, file, "10.0.405")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, want 0 with nothing on stderr; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if got := read(t, file); got != rewritten {
		t.Errorf("the file holds %q, want %q", got, rewritten)
	}
	if r.stdout != rewritten {
		t.Errorf("stdout is %q, want what the file now holds, %q", r.stdout, rewritten)
	}
}

// TestNoGlobalJSON checks a source with no global.json is built as it is,
// with a line saying so and no file made, though an SDK that is not one is
// refused all the same.
func TestNoGlobalJSON(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "global.json")
	r := run(t, file, "")
	want := "no global.json at " + file + ": the image's SDK builds the source as it is\n"
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, want 0 with nothing on stderr; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if r.stdout != want {
		t.Errorf("stdout is %q, want %q", r.stdout, want)
	}

	r = run(t, file, "banana")
	if lines := errorLines(r.stderr); r.code != 1 || len(lines) != 1 || !strings.Contains(lines[0], "'banana', not an SDK version") {
		t.Errorf("an SDK of banana: exit %d, want 1 with one ERROR line refusing it; stderr:\n%s", r.code, r.stderr)
	}

	if _, err := os.Lstat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the script made %s, or it cannot be read: %v", file, err)
	}
}

// TestSDKMismatch checks a global.json whose major or minor is not the
// image's SDK's is refused, each way with its own steps to take, and left
// as it was, so the build stops before a dotnet command that would refuse
// it with less to go on.
func TestSDKMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, version, sdk, want string
	}{
		{
			name:    "upstream-ahead",
			version: "11.0.100",
			sdk:     "10.0.404",
			want:    fmt.Sprintf(upstreamAhead, "11.0", "10.0"),
		},
		{
			name:    "upstream-preview-ahead",
			version: "11.0.100-rc.2.25502.107",
			sdk:     "10.0.404",
			want:    fmt.Sprintf(upstreamAhead, "11.0", "10.0"),
		},
		{
			name:    "image-ahead",
			version: "10.0.404",
			sdk:     "11.0.100",
			want:    fmt.Sprintf(imageAhead, "10.0", "11.0"),
		},
		{
			// Compared as numbers: 10 sorts before 9 as text
			name:    "upstream-ahead-by-a-digit",
			version: "10.0.100",
			sdk:     "9.0.100",
			want:    fmt.Sprintf(upstreamAhead, "10.0", "9.0"),
		},
		{
			// The same major, a later minor
			name:    "upstream-ahead-by-a-minor",
			version: "10.1.100",
			sdk:     "10.0.404",
			want:    fmt.Sprintf(upstreamAhead, "10.1", "10.0"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := `{"sdk":{"version":"` + tt.version + `"}}`
			file := fixture(t, in)
			r := run(t, file, tt.sdk)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if r.stderr != tt.want {
				t.Errorf("stderr is\n%q\nwant\n%q", r.stderr, tt.want)
			}
			if r.stdout != "" {
				t.Errorf("stdout is %q, want nothing", r.stdout)
			}
			if got := read(t, file); got != in {
				t.Errorf("a refused file was changed: it holds %q, want %q", got, in)
			}
		})
	}
}

// TestRefusals checks each file the script cannot keep a floor from, or
// check against the image's SDK, is refused with exit 1 and one ERROR line
// saying why, and left untouched, so the build fails rather than running
// on an SDK policy no one chose.
func TestRefusals(t *testing.T) {
	t.Parallel()

	const (
		noVersion  = "has no sdk.version"
		notVersion = "not an SDK version"
	)
	tests := []struct {
		name, in, sdk, want string
	}{
		{name: "empty-file", in: "", want: noVersion},
		{name: "no-sdk", in: `{}`, want: noVersion},
		{name: "no-version", in: `{"sdk":{"rollForward":"latestFeature"}}`, want: noVersion},
		{
			// msbuild-sdks pins MSBuild project SDKs, not the .NET SDK
			name: "msbuild-sdks-only",
			in:   `{"msbuild-sdks":{"Microsoft.Build.Traversal":"4.1.0"}}`,
			want: noVersion,
		},
		{
			name: "version-outside-sdk",
			in:   `{"sdk":{"rollForward":"patch"},"tools":{"version":"10.0.404"}}`,
			want: noVersion,
		},
		{name: "word", in: `{"sdk":{"version":"latest"}}`, want: "is 'latest', " + notVersion},
		{name: "two-numbers", in: `{"sdk":{"version":"10.0"}}`, want: notVersion},
		{name: "empty-version", in: `{"sdk":{"version":""}}`, want: notVersion},
		{name: "leading-v", in: `{"sdk":{"version":"v10.0.404"}}`, want: notVersion},
		{
			// An escaped quote, which would carry JSON of its own into the
			// file the script writes
			name: "escaped-quote",
			in:   `{"sdk":{"version":"10.0.404\",\"rollForward\":\"disable"}}`,
			want: notVersion,
		},
		{
			// What the stage's image reports, should it ever be no version
			name: "sdk-not-version",
			in:   upstream,
			sdk:  "banana",
			want: "SDK is 'banana', " + notVersion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := fixture(t, tt.in)
			r := run(t, file, tt.sdk)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1 with an ERROR line saying %q; stdout:\n%s\nstderr:\n%s", r.code, tt.want, r.stdout, r.stderr)
			}
			lines := errorLines(r.stderr)
			if len(lines) != 1 || !strings.Contains(lines[0], tt.want) || !strings.Contains(lines[0], file) {
				t.Errorf("want one ERROR line naming %s and saying %q; stderr:\n%s", file, tt.want, r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout is %q, want nothing", r.stdout)
			}
			if got := read(t, file); got != tt.in {
				t.Errorf("a refused file was changed: it holds %q, want %q", got, tt.in)
			}
		})
	}
}

// TestUsage checks the script refuses a command line that is not one file
// and one SDK, or names something other than a regular file, rather than
// checking a file that is not there or one it cannot rewrite.
func TestUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := fixture(t, upstream)
	link := filepath.Join(dir, "dangling.json")
	if err := os.Symlink(filepath.Join(dir, "absent.json"), link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "no-file"},
		{name: "no-sdk", args: []string{file}},
		{name: "two-files", args: []string{file, file, sdk}},
		{name: "a-directory", args: []string{dir, sdk}},
		{name: "dangling-link", args: []string{link, sdk}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := runArgs(t, tt.args...)
			if r.code != 2 {
				t.Errorf("exit %d, want 2; stderr:\n%s", r.code, r.stderr)
			}
			if !strings.HasPrefix(r.stderr, "usage: ") || strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("stderr is %q, want one usage line", r.stderr)
			}
		})
	}
	if got := read(t, file); got != upstream {
		t.Errorf("a usage error changed a file it was given: it holds %q", got)
	}
}

// TestContainerfileRunsIt fails unless the Containerfile copies the script
// in and runs it over global.json, against the SDK the stage's image
// reports from /, where no global.json is in the way, before the first
// dotnet command, which is the first thing that reads global.json.
func TestContainerfileRunsIt(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	data, err := root.ReadFile("Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	containerfile := string(data)

	copied := regexp.MustCompile(`(?m)^COPY test/global-json\.sh (\S+)$`).FindStringSubmatchIndex(containerfile)
	if copied == nil {
		t.Fatal("the Containerfile does not COPY test/global-json.sh into a build stage")
	}
	dst := containerfile[copied[2]:copied[3]]
	want := `RUN sdk=$(cd / && dotnet --version); ` + dst + ` global.json "${sdk}"`
	ran := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `$`).FindStringIndex(containerfile)
	if ran == nil {
		t.Fatalf("the Containerfile copies the script to %s but never runs %s", dst, want)
	}
	if ran[0] < copied[0] {
		t.Errorf("the Containerfile runs %s before it copies the script there", dst)
	}
	dotnet := regexp.MustCompile(`(?m)^RUN (--\S+ )*dotnet `).FindStringIndex(containerfile)
	if dotnet == nil {
		t.Fatal("the Containerfile runs no dotnet command, so this test no longer knows what must follow the check")
	}
	if ran[0] > dotnet[0] {
		t.Error("the Containerfile runs dotnet before it checks and rewrites global.json")
	}
}

// fixture writes content to a new file, and returns its path.
func fixture(t *testing.T, content string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "global.json")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func read(t *testing.T, file string) string {
	t.Helper()

	data, err := os.ReadFile(file) //nolint:gosec // G304: a file this test wrote under its own TempDir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// run runs the script over file against the SDK have, or against sdk when
// have is empty.
func run(t *testing.T, file, have string) result {
	t.Helper()

	if have == "" {
		have = sdk
	}
	return runArgs(t, file, have)
}

// runArgs runs the script with args, and nothing of the host's environment
// but its PATH.
func runArgs(t *testing.T, args ...string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), script, args...) //nolint:gosec // G204: the script is this repository's own, run on files the test made
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("%s: %v", script, err)
		}
	}
	return result{cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

// errorLines returns the lines of stderr that report a refusal.
func errorLines(stderr string) []string {
	var lines []string
	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, "ERROR: ") {
			lines = append(lines, line)
		}
	}
	return lines
}
