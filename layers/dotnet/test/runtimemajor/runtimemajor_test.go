// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package runtimemajor tests test/runtime-major.sh, the check the
// Containerfile's backend stage runs over the app it has published: the
// .NET each runtimeconfig names against the one leading the runtime
// image's tag. The script runs against runtimeconfigs written to a
// temporary directory: the shapes dotnet publish writes and the image
// references a pin can take, which it must pass, a .NET major on one side
// and not the other, each way with its own steps to take, and every way
// it can refuse. The Containerfile must run it where it must, and the
// check of that is seen refusing a Containerfile changed each way it could
// be wired wrong.
package runtimemajor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		{
			// The same major, a later minor
			name:    "app-ahead-by-a-minor",
			version: "10.1.0",
			image:   image,
			want:    fmt.Sprintf(appAhead, "10.1", "10.0"),
		},
		{
			name:    "runtime-ahead-by-a-minor",
			version: "10.0.0",
			image:   "dhi.io/aspnetcore:10.1.2-debian13" + digest,
			want:    fmt.Sprintf(runtimeAhead, "10.0", "10.1"),
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

// TestContainerfileRunsIt fails unless the Containerfile runs the check
// where it must: wiringProblems, which TestWiringProblems sees refuse each
// way the wiring could go wrong, finds nothing wrong with it.
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
	for _, problem := range wiringProblems(string(data)) {
		t.Error(problem)
	}
}

// TestWiringProblems checks wiringProblems finds nothing wrong with wired,
// and in wired changed each way the check could be wired wrong, one
// problem, which says what is wrong.
func TestWiringProblems(t *testing.T) {
	t.Parallel()

	if problems := wiringProblems(wired); len(problems) != 0 {
		t.Fatalf("wired has problems of its own, so no change to it shows one: %q", problems)
	}
	const (
		backend = "FROM dhi.io/dotnet:10.0.404-sdk-debian13 AS backend\n"
		arg     = "ARG TEMPLATE_VERSION RUNTIME_IMAGE TARGETARCH\n"
		copied  = "COPY test/runtime-major.sh /tmp/runtime-major.sh\n"
		output  = "--no-restore -o /out/bin\n"
		ran     = `RUN /tmp/runtime-major.sh /out/bin "${RUNTIME_IMAGE}"` + "\n"
		runtime = "FROM ${RUNTIME_IMAGE}\n"
		ships   = "COPY --from=backend /out/bin /app/bin\n"
	)
	tests := []struct {
		name  string
		edits []edit
		want  string
	}{
		{name: "not-copied", edits: []edit{{copied, ""}}, want: "does not COPY test/runtime-major.sh"},
		{name: "no-image-passed", edits: []edit{{ran, "RUN /tmp/runtime-major.sh /out/bin\n"}}, want: "never runs"},
		{
			// A failure it would hide
			name:  "not-alone-on-its-line",
			edits: []edit{{ran, `RUN /tmp/runtime-major.sh /out/bin "${RUNTIME_IMAGE}" || true` + "\n"}},
			want:  "alone on its line",
		},
		{name: "run-before-copied", edits: []edit{{copied, ""}, {ran, ran + copied}}, want: "before it copies the script there"},
		{
			name:  "runtime-not-from-the-arg",
			edits: []edit{{runtime, "FROM dhi.io/aspnetcore:10.0.15-debian13\n"}},
			want:  "no stage is built FROM ${RUNTIME_IMAGE}",
		},
		{
			// Renovate would read the pin as TEMPLATE_VERSION
			name:  "arg-named-first",
			edits: []edit{{arg, "ARG RUNTIME_IMAGE TEMPLATE_VERSION TARGETARCH\n"}},
			want:  "Renovate reads",
		},
		{name: "no-arg-in-the-stage", edits: []edit{{arg, "ARG TEMPLATE_VERSION TARGETARCH\n"}}, want: "declares no ARG RUNTIME_IMAGE"},
		{
			name:  "arg-with-a-default-of-its-own",
			edits: []edit{{arg, "ARG TEMPLATE_VERSION TARGETARCH\nARG RUNTIME_IMAGE=dhi.io/aspnetcore:9.0.11-debian13\n"}},
			want:  "declares no ARG RUNTIME_IMAGE",
		},
		{name: "arg-after-the-check", edits: moved(arg, "\nFROM dhi.io/golang"), want: "declares no ARG RUNTIME_IMAGE"},
		{name: "nothing-published-to-out-bin", edits: []edit{{output, "--no-restore -o /out/app\n"}}, want: "no RUN before the check"},
		{
			name:  "out-bin-written-after-the-check",
			edits: []edit{{ran, ran + "RUN rm /out/bin/Template.pdb\n"}},
			want:  "a RUN after the check names /out/bin",
		},
		{
			// Prowlarr's copy to /out/bin, on continued lines
			name:  "out-bin-copied-after-the-check",
			edits: []edit{{ran, ran + "RUN rid=$(</tmp/rid); \\\n    cp -a \"_output/${rid}/publish\" /out/bin\n"}},
			want:  "a RUN after the check names /out/bin",
		},
		{name: "not-shipped", edits: []edit{{ships, ""}}, want: "ships nothing the check read"},
		{name: "another-folder-shipped", edits: []edit{{ships, "COPY --from=backend /out /app\n"}}, want: "ships nothing the check read"},
		{name: "shipped-from-another-stage", edits: []edit{{ships, "COPY --from=probe /out/bin /app/bin\n"}}, want: "ships nothing the check read"},
		{
			name:  "unnamed-stage",
			edits: []edit{{backend, "FROM dhi.io/dotnet:10.0.404-sdk-debian13\n"}},
			want:  "the check's stage has no name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			containerfile := changed(t, tt.edits)
			if problems := wiringProblems(containerfile); len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Errorf("want one problem saying %q; got %q, of:\n%s", tt.want, problems, containerfile)
			}
		})
	}
}

// wiringProblems returns each way containerfile fails to run the check
// where it must, one apiece, or none. The script must be copied into a
// build stage and run there over /out/bin, against the RUNTIME_IMAGE its
// stage declares, after every RUN in that stage that names /out/bin, as
// any RUN that writes there must: the check has to read what the runtime
// stage copies. So the stage built FROM ${RUNTIME_IMAGE} must ship
// /out/bin from the check's stage, by its name. An ARG with no default of
// its own takes the one the runtime stage is built FROM, which a stage
// must be. Renovate's dockerfile manager reads ARG NAME VALUE as
// NAME=VALUE, so no ARG line may give RUNTIME_IMAGE anything after it: a
// name there would become the runtime pin's value, and Renovate would stop
// moving the pin.
func wiringProblems(containerfile string) []string {
	copied := regexp.MustCompile(`(?m)^COPY test/runtime-major\.sh (\S+)$`).FindStringSubmatchIndex(containerfile)
	if copied == nil {
		return []string{"the Containerfile does not COPY test/runtime-major.sh into a build stage"}
	}
	dst := containerfile[copied[2]:copied[3]]
	want := `RUN ` + dst + ` /out/bin "${RUNTIME_IMAGE}"`
	ran := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `$`).FindStringIndex(containerfile)
	if ran == nil {
		return []string{"the Containerfile copies the script to " + dst + " but never runs " + want + ", alone on its line"}
	}
	var problems []string
	if ran[0] < copied[0] {
		problems = append(problems, "the Containerfile runs "+dst+" before it copies the script there")
	}
	runtimes := regexp.MustCompile(`(?m)^FROM \$\{RUNTIME_IMAGE\}$`).FindAllStringIndex(containerfile, -1)
	if len(runtimes) == 0 {
		problems = append(problems, "no stage is built FROM ${RUNTIME_IMAGE}, so the check compares the app with an image nothing runs it on")
	}
	for _, line := range regexp.MustCompile(`(?m)^ARG RUNTIME_IMAGE[ \t].*$`).FindAllString(containerfile, -1) {
		problems = append(problems, fmt.Sprintf("Renovate reads %q as RUNTIME_IMAGE set to what follows it, and would stop moving the runtime pin: name RUNTIME_IMAGE alone, or after another name", line))
	}

	stage, at, ok := stageAt(containerfile, ran[0])
	if !ok {
		return append(problems, "the Containerfile runs the check before any FROM")
	}
	if arg := regexp.MustCompile(`(?m)^ARG (\S+ )*RUNTIME_IMAGE( \S+)*$`).FindStringIndex(stage); len(arg) == 0 || arg[0] > at {
		problems = append(problems, "the check's stage declares no ARG RUNTIME_IMAGE, without a default of its own, before the check, which would then read another image or none")
	}
	wrote := false
	for _, r := range runs.FindAllStringIndex(stage, -1) {
		text := stage[r[0]:r[1]]
		switch {
		case r[0] == at || !strings.Contains(text, "/out/bin"):
		case r[0] > at:
			problems = append(problems, "a RUN after the check names /out/bin, so what ships may not be what it checked:\n"+text)
		default:
			wrote = true
		}
	}
	if !wrote {
		problems = append(problems, "no RUN before the check in its stage names /out/bin, so nothing it checks was published there")
	}

	// What the check read must be what ships
	from, _, _ := strings.Cut(stage, "\n")
	name := regexp.MustCompile(`^FROM (?:--\S+ )*\S+ (?i:AS) (\S+)$`).FindStringSubmatch(from)
	if name == nil {
		return append(problems, "the check's stage has no name, as FROM <image> AS <name> gives it, so no stage can COPY --from it what the check read")
	}
	ships := regexp.MustCompile(`(?m)^COPY (?:--\S+ )*--from=` + regexp.QuoteMeta(name[1]) + ` (?:--\S+ )*/out/bin/? \S+$`)
	shipped := false
	for _, r := range runtimes {
		runtime, _, _ := stageAt(containerfile, r[0])
		shipped = shipped || ships.MatchString(runtime)
	}
	if len(runtimes) > 0 && !shipped {
		problems = append(problems, "the stage built FROM ${RUNTIME_IMAGE} ships nothing the check read: it has no COPY --from="+name[1]+" /out/bin")
	}
	return problems
}

// The instructions wiringProblems reads, each from its keyword to the end
// of its last continued line.
var (
	froms = regexp.MustCompile(`(?m)^FROM `)
	runs  = regexp.MustCompile(`(?m)^RUN (?:.*\\\n)*.*$`)
)

// stageAt returns the build stage holding offset at in containerfile, from
// the last FROM before it up to the next FROM or the end, and at's offset
// within it; ok is false when no FROM comes before at.
func stageAt(containerfile string, at int) (stage string, offset int, ok bool) {
	start, end := -1, len(containerfile)
	for _, from := range froms.FindAllStringIndex(containerfile, -1) {
		if from[0] > at {
			end = from[0]
			break
		}
		start = from[0]
	}
	if start < 0 {
		return "", 0, false
	}
	return containerfile[start:end], at - start, true
}

// wired is a Containerfile that runs both checks where they must, for
// TestWiringProblems to change one way at a time.
const wired = `ARG RUNTIME_IMAGE=dhi.io/aspnetcore:10.0.15-debian13

FROM dhi.io/dotnet:10.0.404-sdk-debian13 AS backend
ARG TEMPLATE_VERSION RUNTIME_IMAGE TARGETARCH
WORKDIR /src
COPY src/ ./
COPY test/global-json.sh /tmp/global-json.sh
COPY test/runtime-major.sh /tmp/runtime-major.sh
RUN sdk=$(cd / && dotnet --version); /tmp/global-json.sh global.json "${sdk}"
RUN dotnet restore Template/Template.csproj -r linux-x64
RUN --network=none dotnet publish Template/Template.csproj -r linux-x64 \
    --self-contained false --no-restore -o /out/bin
RUN /tmp/runtime-major.sh /out/bin "${RUNTIME_IMAGE}"

FROM dhi.io/golang:1.27.4-debian13-dev AS probe
RUN go build -o /out/divbast-template ./cmd/divbast-template

FROM ${RUNTIME_IMAGE}
COPY --from=backend /out/bin /app/bin
COPY --from=probe /out/divbast-template /usr/local/bin/divbast-template
ENTRYPOINT ["/app/bin/Template"]
`

// edit replaces from, which must occur once, with to.
type edit struct{ from, to string }

// moved is the edits that move line, a whole one, to just before before.
func moved(line, before string) []edit {
	return []edit{{line, ""}, {before, line + before}}
}

// changed is wired with each edit made in turn.
func changed(t *testing.T, edits []edit) string {
	t.Helper()

	containerfile := wired
	for _, e := range edits {
		if n := strings.Count(containerfile, e.from); n != 1 {
			t.Fatalf("%q occurs %d times, want once, in:\n%s", e.from, n, containerfile)
		}
		containerfile = strings.Replace(containerfile, e.from, e.to, 1)
	}
	return containerfile
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
