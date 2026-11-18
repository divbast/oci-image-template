// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package runtimemajor tests test/runtime-major.sh, the check the
// Containerfile's backend stage runs over the app it has published: the
// .NET each runtimeconfig names against the one leading the runtime
// image's tag. The script runs against runtimeconfigs written to a
// temporary directory: the shapes dotnet publish writes and the image
// references a pin can take, which it must pass, a .NET major on one side
// and not the other, each way with its own steps to take, and every way
// it can refuse.
package runtimemajor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// script is the script under test, from this package's directory.
const script = "../runtime-major.sh"

// digest pins the runtime images below, as a DHI pin carries one.
const digest = "@sha256:7d806f4ac510394ceee07c6bb0642791b9fba1ebccb2a678bb23575868e91e90"

// image is the runtime image, pinned by tag and digest, wherever a test
// names none.
const image = "dhi.io/aspnetcore:10.0.15-debian13" + digest

// aspnet is a runtimeconfig as dotnet publish writes it for an ASP.NET
// Core app, naming the one framework it runs on.
const aspnet = `{
  "runtimeOptions": {
    "tfm": "net10.0",
    "framework": {
      "name": "Microsoft.AspNetCore.App",
      "version": "10.0.0"
    },
    "configProperties": {
      "System.GC.Server": false,
      "System.Reflection.Metadata.MetadataUpdater.IsSupported": false
    }
  }
}
`

// both is a runtimeconfig naming each framework the app runs on.
const both = `{
  "runtimeOptions": {
    "tfm": "net10.0",
    "frameworks": [
      {
        "name": "Microsoft.NETCore.App",
        "version": "10.0.0"
      },
      {
        "name": "Microsoft.AspNetCore.App",
        "version": "10.0.0"
      }
    ]
  }
}
`

// selfContained is a runtimeconfig as a self-contained publish writes it,
// with the runtime it carries listed under includedFrameworks.
const selfContained = `{
  "runtimeOptions": {
    "tfm": "net10.0",
    "includedFrameworks": [
      {
        "name": "Microsoft.NETCore.App",
        "version": "10.0.1"
      },
      {
        "name": "Microsoft.AspNetCore.App",
        "version": "10.0.1"
      }
    ]
  }
}
`

// The two ways a .NET major can stand on one side alone, each with the
// steps to take, for the app's major.minor and then the runtime image's.
const (
	appAhead     = "ERROR: the app targets .NET %[1]s; the runtime image is .NET %[2]s. Approve the .NET %[1]s major on the Dependency Dashboard and move both together: CLAUDE.md, 'A .NET major'.\n"
	runtimeAhead = "ERROR: the app targets .NET %[1]s; the runtime image is .NET %[2]s. A .NET major waits until the app retargets, then moves with the app's own update: CLAUDE.md, 'A .NET major'.\n"
)

// result is how a run of the script ended.
type result struct {
	code           int
	stdout, stderr string
}

// TestRuntimeMajorAgrees checks an app and a runtime image on the same
// .NET pass, with one line saying which, whatever shape the runtimeconfigs
// take and wherever the image is pulled from.
func TestRuntimeMajorAgrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		image string
	}{
		{name: "framework", files: map[string]string{"App.runtimeconfig.json": aspnet}},
		{name: "frameworks", files: map[string]string{"App.runtimeconfig.json": both}},
		{
			// A later patch of the same major.minor still agrees
			name: "two-files",
			files: map[string]string{
				"App.runtimeconfig.json":      both,
				"App.Tool.runtimeconfig.json": config("framework", `{"name":"Microsoft.NETCore.App","version":"10.0.2"}`),
			},
		},
		{
			// The tag follows the last colon, not the registry's port
			name:  "registry-with-port",
			files: map[string]string{"App.runtimeconfig.json": aspnet},
			image: "localhost:5000/aspnetcore:10.0.15",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := run(t, published(t, tt.files), tt.image)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("exit %d, want 0 with nothing on stderr; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if want := "app and runtime image on .NET 10.0\n"; r.stdout != want {
				t.Errorf("stdout is %q, want %q", r.stdout, want)
			}
		})
	}
}

// TestRuntimeMismatch checks an app whose .NET major or minor is not the
// runtime image's is refused, each way with its own steps to take, since
// the image would ship an app that cannot start.
func TestRuntimeMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, version, image, want string
	}{
		{
			name:    "app-ahead",
			version: "11.0.0",
			image:   image,
			want:    fmt.Sprintf(appAhead, "11.0", "10.0"),
		},
		{
			name:    "runtime-ahead",
			version: "10.0.0",
			image:   "dhi.io/aspnetcore:11.0.0-debian13" + digest,
			want:    fmt.Sprintf(runtimeAhead, "10.0", "11.0"),
		},
		{
			// Compared as numbers: 10 sorts before 9 as text
			name:    "app-ahead-by-a-digit",
			version: "10.0.0",
			image:   "dhi.io/aspnetcore:9.0.11-debian13" + digest,
			want:    fmt.Sprintf(appAhead, "10.0", "9.0"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"App.runtimeconfig.json": framework(tt.version)}
			r := run(t, published(t, files), tt.image)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if r.stderr != tt.want {
				t.Errorf("stderr is\n%q\nwant\n%q", r.stderr, tt.want)
			}
			if r.stdout != "" {
				t.Errorf("stdout is %q, want nothing", r.stdout)
			}
		})
	}
}

// TestRuntimeMajorRefuses checks each app or image the script cannot read
// a .NET from is refused with exit 1 and one ERROR line saying why, rather
// than passing on no version at all.
func TestRuntimeMajorRefuses(t *testing.T) {
	t.Parallel()

	const (
		noConfig    = "holds no *.runtimeconfig.json"
		self        = "published self-contained"
		noFramework = "no framework version"
		noTag       = "no .NET version leading its tag"
	)
	tests := []struct {
		name  string
		files map[string]string
		image string
		want  string
	}{
		{
			// What publish writes beside the runtimeconfig, never in its stead
			name:  "no-runtimeconfig",
			files: map[string]string{"App.deps.json": `{"runtimeTarget":{"name":".NETCoreApp,Version=v10.0"}}`},
			want:  noConfig,
		},
		{name: "self-contained", files: map[string]string{"App.runtimeconfig.json": selfContained}, want: self},
		{
			// includedFrameworks anywhere means self-contained, whatever
			// else the file names
			name: "self-contained-beside-framework",
			files: map[string]string{"App.runtimeconfig.json": config(
				"framework", `{"name":"Microsoft.AspNetCore.App","version":"10.0.0"}`,
				"includedFrameworks", `[{"name":"Microsoft.NETCore.App","version":"10.0.1"}]`,
			)},
			want: self,
		},
		{name: "no-framework", files: map[string]string{"App.runtimeconfig.json": config()}, want: noFramework},
		{
			name:  "framework-without-version",
			files: map[string]string{"App.runtimeconfig.json": config("framework", `{"name":"Microsoft.AspNetCore.App"}`)},
			want:  noFramework,
		},
		{
			// One framework of two with a version is not enough
			name: "frameworks-one-without-version",
			files: map[string]string{"App.runtimeconfig.json": config(
				"frameworks", `[{"name":"Microsoft.NETCore.App","version":"10.0.0"},{"name":"Microsoft.AspNetCore.App"}]`,
			)},
			want: noFramework,
		},
		{name: "empty-frameworks", files: map[string]string{"App.runtimeconfig.json": config("frameworks", `[]`)}, want: noFramework},
		{
			// A file with no version beside one that has it
			name: "one-file-without-framework",
			files: map[string]string{
				"App.runtimeconfig.json":      aspnet,
				"App.Tool.runtimeconfig.json": config(),
			},
			want: noFramework,
		},
		{
			name:  "not-a-version",
			files: map[string]string{"App.runtimeconfig.json": framework("latest")},
			want:  "'latest', not a .NET version",
		},
		{
			name: "files-disagree",
			files: map[string]string{
				"App.runtimeconfig.json":      framework("10.0.0"),
				"App.Tool.runtimeconfig.json": framework("11.0.0"),
			},
			want: "frameworks disagree",
		},
		{
			name:  "digest-only",
			files: map[string]string{"App.runtimeconfig.json": aspnet},
			image: "dhi.io/aspnetcore" + digest,
			want:  noTag,
		},
		{name: "latest", files: map[string]string{"App.runtimeconfig.json": aspnet}, image: "dhi.io/aspnetcore:latest", want: noTag},
		{
			// The port is no tag, and 5000 no version
			name:  "registry-with-port-no-tag",
			files: map[string]string{"App.runtimeconfig.json": aspnet},
			image: "localhost:5000/aspnetcore",
			want:  noTag,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := published(t, tt.files)
			r := run(t, dir, tt.image)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1 with an ERROR line saying %q; stdout:\n%s\nstderr:\n%s", r.code, tt.want, r.stdout, r.stderr)
			}
			// Said in the line's own words, not found in a path it names,
			// which carries the test's name
			lines := errorLines(r.stderr)
			if len(lines) != 1 || !strings.Contains(strings.ReplaceAll(lines[0], dir, "DIR"), tt.want) {
				t.Errorf("want one ERROR line saying %q; stderr:\n%s", tt.want, r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout is %q, want nothing", r.stdout)
			}
		})
	}
}

// TestUsage checks the script refuses a command line that is not one
// directory and one image, rather than reporting no runtimeconfig in a
// directory that is not there.
func TestUsage(t *testing.T) {
	t.Parallel()

	dir := published(t, map[string]string{"App.runtimeconfig.json": aspnet})
	file := filepath.Join(dir, "App.runtimeconfig.json")
	tests := []struct {
		name string
		args []string
	}{
		{name: "no-arguments"},
		{name: "one-argument", args: []string{dir}},
		{name: "three-arguments", args: []string{dir, image, image}},
		{name: "a-file", args: []string{file, image}},
		{name: "absent", args: []string{filepath.Join(dir, "absent"), image}},
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
}

// framework is a runtimeconfig naming the ASP.NET Core framework at
// version.
func framework(version string) string {
	return config("framework", `{"name":"Microsoft.AspNetCore.App","version":"`+version+`"}`)
}

// config is a runtimeconfig whose runtimeOptions hold each key and JSON
// value given in pairs, after the tfm.
func config(pairs ...string) string {
	var b strings.Builder
	b.WriteString(`{"runtimeOptions":{"tfm":"net10.0"`)
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(`,"` + pairs[i] + `":` + pairs[i+1])
	}
	b.WriteString("}}\n")
	return b.String()
}

// published writes files, by name, to a new directory standing for
// publish's output, and returns its path.
func published(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// run runs the script over dir against the runtime image ref, or against
// image when ref is empty.
func run(t *testing.T, dir, ref string) result {
	t.Helper()

	if ref == "" {
		ref = image
	}
	return runArgs(t, dir, ref)
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
