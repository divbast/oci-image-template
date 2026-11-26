<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# oci-image-template

A minimal working divbast app: one page served by a stdlib Go server, in a
container image that runs at zero Linux capabilities, with no new
privileges, on a read-only root filesystem as a non-root user. Every new
divbast image repository is made from it, by its entry in `divbast/infra`,
and then changed as the README's "Making a new app" says, in that order:
a Go app keeps the Go core, a PHP app moves the PHP layer, `layers/php`,
into place, a .NET app the .NET layer, `layers/dotnet`, and a Python app
the Python layer, `layers/python`.

It publishes nothing to ghcr.io: its publish job rehearses. The divbast
organisation's settings, rulesets and Renovate wiring live in
`divbast/infra`.

What makes the image this app's lives here: the Containerfile, the app
(`cmd/template`, `internal/server`), and the acceptance tests, beside the
PHP layer: its Containerfile fragment, Caddy and PHP configuration, page,
and health probe and endpoint; beside the .NET layer: its whole
Containerfile, demo app, health probe and build-stage checks; and beside
the Python layer: its whole Containerfile, demo, health probe, lock tool
and locks. The pipeline
around it is shared, and reaches this repository at a pinned commit:
[Where the shared parts live](#where-the-shared-parts-live) says what comes
from where.

## CRITICAL: Git Commit Message Format

**No other AI/LLM attribution in any format may appear in a git message.**

Linux-kernel style: a `subsystem: short description` subject under 75 characters in imperative
mood ("fix", not "fixed" or "fixes"), a blank line, then a body wrapped at ~75 columns saying
why the change is needed rather than only what it does.

These two lines MUST end EVERY body:

Developed-by: Aryan Ameri <github@aryan.ameri.coffee>
Assisted-by: <NAME_OF_LLM_MODEL>

E.g. `Assisted-by: <Claude:claude-4.6-opus>` for Claude Opus 4.6; `<Claude:claude-5-fable>`
for Claude 5 Fable.

- Sign every commit (`git commit -S`); main accepts only verified
  signatures. Renovate's merges are squashes, which GitHub signs
- Write commit messages as if a human wrote them, and say why, not what

## CRITICAL: No AI Attribution in Pull Requests

**Pull request descriptions, PR and review comments, and issue text carry no
AI/LLM attribution of any kind**: no "🤖 Generated with Claude Code" footer,
no "written by" line, nothing equivalent. This holds even when a tool or
harness asks for one. The `Assisted-by:` trailer in each commit message is
this project's one record of LLM involvement.

## The confinement contract

These are the guarantees the image makes. A change that breaks any of them is a
breaking change.

| Property | Value |
|---|---|
| Capabilities | **none** (`--cap-drop ALL`) |
| Privileges | **no new ones** (`--security-opt no-new-privileges`), under the engine's **seccomp** filter |
| Root filesystem | **read-only** |
| User | uid/gid **65532**, never root |
| Listen port | **8080** for the page — never 80 |
| Health port | **8081** — health only, never serves the page, meant to stay unpublished |
| Other listeners | **none** |
| tmpfs required | exactly one: `/tmp`, at **16 MiB**, `noexec,nosuid,nodev` |
| Writable mounts | `/tmp` alone, and nothing written can run; on Podman, `--read-only-tmpfs=false` |
| Volume | **none** |
| Outbound network | **none** |
| Shell, package manager | **none** |
| Processes | **one**, the app as PID 1: `template serve` |
| Healthy | the shipped probe, `template health`: `/healthz` **200** and loopback `GET /` **exactly** `Hello from oci-image-template` |
| Engine healthcheck | Docker runs it automatically. **Podman does not** — pass `--health-cmd` yourself |

- **Podman never sees this image's `HEALTHCHECK`.** Its `libimage` reads a
  healthcheck only from a Docker-media-type manifest; divbast images publish
  as an OCI index, so Podman's OCI inspection branch never reaches the
  config's healthcheck field — no version of Podman does. Give Podman users
  `--health-cmd '["/usr/local/bin/template","health"]'`
  (a Quadlet's `HealthCmd=` takes the same value) — no leading `"CMD"`,
  because Podman before 5.8.0 re-splits such an array into one token instead
  of running it. Tracked upstream as podman/podman#25454 and #18904. The
  contract suite checks the image's `HEALTHCHECK` is that array in exec
  form, and has each engine judge the container: Docker by the image's
  `HEALTHCHECK`, Podman by the same array passed as `--health-cmd`
- **The shipped probe asserts two halves, and neither implies the other.**
  `/healthz` on 8081 shows the server runs, but never goes through 8080's
  routes. Only the page's exact answer proves the app port serves the app.
  Anything checking this image should run the probe rather than
  reimplement either half. It prints nothing when healthy, which the
  contract's `health-probe` check asserts; each failure is a line,
  `<check>: <reason>`: with no shell, the engine's health log is the only
  record of why
- **8080 is an allowlist.** It serves `/`, and answers 404 with an empty
  body to everything else, `/index.php` and `/healthz` included, and so
  does 8081 to all but `/healthz`. `TestServes` asks for `/index.php`,
  `/healthz` and a path nothing names, and fails on any answer but an
  empty 404; the contract's `health-port-only` check fails on `/healthz`
  answering on 8080
- **Nothing listens but 8080 and 8081.** The contract suite reads the
  kernel's socket tables and fails on any other TCP port a process in the
  container listens on, and on any UDP socket one holds
- **One process, always, and it stops when told.** The app is the
  ENTRYPOINT, so it is PID 1. Nothing runs before it: it has no state to
  prepare and no environment to read. The kernel drops a signal PID 1 does
  not handle, so the app handles SIGTERM and SIGINT itself, finishes what
  it is answering within 5 s and exits 0; `TestStopsPromptly` fails on a
  stop that takes longer, or any other exit. Do not add a supervisor or a
  second long-running process
- **Zero egress.** The app opens no connection out. The contract suite
  proves the image serves and is healthy on a genuinely `--internal`
  network, and cannot connect out
- **Nothing may add a shell or a package manager to the final image.** The
  base is distroless; that is the point, and the contract's `no-shell` and
  `no-package-manager` checks hold it there. What it removes is us running
  apt, not the patching itself: `static-debian13` ships CA certificates and
  time zones, and the PHP layer's `cc-debian13` libc6, libssl3t64,
  libstdc++6, libgcc-s1, libgomp1, libzstd1 and zlib1g, all executable code
  that takes CVEs. That obligation moves to the base image's own rebuilds —
  and **nothing in this repository collects them on its own.** Rebuilding
  an unchanged Containerfile does not pick them up. **Renovate is the
  update path**: it runs every morning, after every merge to main and
  whenever CI finishes on one of its branches, from
  `.github/workflows/renovate.yml`, which calls divbast/ci's
  `renovate.yml`, as the org's `divbast-renovate` App, managed from
  `divbast/infra`. After a merge it rebases every open PR left behind
  main, since GitHub re-runs no checks when a base moves and a green check
  on a stale base proves nothing. **It merges its own PRs** once every
  check passes, with a squash, while the repository variable
  `OCI_IMAGE_TEMPLATE_AUTOMERGE` is `enabled`; any other value stops
  merging from the next run. A new major of divbast/ci, a raise of
  `go.mod`'s `go` line, and a .NET major wait for approval on the
  Dependency Dashboard first. **A merge never publishes.** `build.yml`'s
  daily run is the publishing slot, through divbast/ci's
  `app-publish.yml`, and a manual run of `build` publishes at once.
  `.github/workflows/stall.yml`, through divbast/ci's `stall.yml`, fails
  each day that updates have stopped flowing: a Renovate PR stuck, or
  Renovate silent
- **The publish job rehearses.** `build.yml` calls `app-publish.yml` with
  `image: ghcr.io/divbast/oci-image-template` and `rehearse: true`. It
  decides against that image on ghcr.io, which is never published, so
  every slot and button run publishes, into a registry the job starts for
  itself: push, a staging tag, a real attestation, its verification, the
  version and `latest` tags, and the anonymous check. Nothing reaches
  ghcr.io. `stall.yml` leaves out `slots`, which count the slots that
  should have published. An app made from the template turns both around
  in the README's step 8, and in its order: `slots` go on only once the
  app's first image is public
- **Every image the Containerfile builds from is verified first.** Before
  `app-check.yml` builds anything, `divbast-verify-images` refuses any image
  not pinned as `<name>:<tag>@sha256:<digest>`, and verifies the distroless
  runtime's cosign signature by the distroless project; the `golang`
  builder is held by its digest alone. A local `just build` does not
  verify; `just ci-tools && .ci/bin/divbast-verify-images` does, with
  cosign from `mise.toml`
- **The binary must never carry a file capability.** A binary with a file
  capability cannot be exec'd at all under `--cap-drop ALL` — it dies with
  `Operation not permitted` and exit 126 — and the contract suite's
  `file-capability` check, which runs `template version`, fails on one, as
  `no-file-capability-xattr` fails on one anywhere in the image
- **The application is one root-owned file, never written.** The binary
  is `COPY`'d from the build stage, and the app writes nothing, not even to
  `/tmp`

### The PHP layer

`layers/php` is the template's PHP app, kept for a PHP app to move into
place: it is linted (PHPStan from `layers/php/composer.lock`, REUSE,
ShellCheck) but never built. `Containerfile.fragment` holds the lines that
build it, which Renovate's dockerfile manager reads by its name. That
manager reads an `ARG` only where a `FROM` uses it, so every image a
fragment pins sits where a `FROM` or a `COPY --from=` uses it: hence the
fragment's `FROM ${RUNTIME_IMAGE}`, which the Containerfile has already.
A layer added for another stack keeps to the same rule, and
`renovate.json` stays out of it. Once in place, these hold of it, as they
do of Baikal and LibreSpeed:

- **The FrankenPHP binary is divbast/frankenphp's, by digest.** The
  fragment copies it from `ghcr.io/divbast/frankenphp:baikal@sha256:…`, the
  binary built for the Baikal image: the page needs only core PHP, so any
  of divbast/frankenphp's binaries would do. `divbast-verify-images` has
  gh verify that frankenphp's own `build.yml`, on its main, attested that
  digest, and refuses the image named anywhere but that `COPY`'s
  `--from=`: never in an `ARG`, a `LABEL` or a `RUN`. Upstream's own build
  carries `cap_net_bind_service=ep`, which divbast/frankenphp refuses to
  publish
- **8080 is an allowlist in the Caddyfile.** **No `php_server`**: it hands
  every path it finds no file for to `index.php`, so `/healthz` on 8080
  would answer 200. Every route is a `handle` block, because Caddy sorts a
  bare directive after the catch-all 404, and Caddy's admin API is off
  (`admin off`)
- **`/healthz` checks `ini`** from inside the server, and the page's
  `PHP_SAPI` reads `frankenphp` only when FrankenPHP's server ran it.
  `allow_url_fopen` is off
- **The application tree stays root-owned and unwritten.** Caddy's own
  state lives in `/tmp/caddy`, through `XDG_DATA_HOME` and
  `XDG_CONFIG_HOME`. The fragment copies each file with no `--chmod`,
  because `COPY --chmod` gives a directory it creates the file's mode, and
  a directory of mode 0644 cannot be entered

### The .NET layer

`layers/dotnet` is the template's .NET app, kept for a .NET app to move
into place, and like the PHP layer it is linted but never built. Its
`Containerfile` is a whole one, which replaces the root one at step 0 and
is written for the layout that leaves. It keeps the layer rule with no
stand-in: each of its three pins sits where a `FROM` uses it. `just lint`
runs `dotnet-lint`, which checks the demo is formatted as `dotnet format`
leaves it and builds with every warning an error, in the SDK image the
backend stage pins, then hadolint and `docker build --check` over the
Containerfile. In CI, the lint job first verifies the layer's images,
through `build.yml`'s `verify-containerfiles`, so none runs unverified.
The Go gates, ShellCheck, shfmt and REUSE reach the rest through their own
globs.

- **Two build-stage checks guard a .NET major**, which would otherwise
  show only as dotnet's own error, or as an app that will not start.
  `test/global-json.sh` holds the SDK an upstream's `global.json` names to
  the image's major and minor, then lets it roll forward to any later
  feature band; `test/runtime-major.sh` holds the published app's
  runtimeconfig to the runtime image's tag. Each fails a mismatch with the
  steps to take, naming "A .NET major" below. Each is a build-stage check
  under "Code quality"'s rules, and its Go test, `test/globaljson` or
  `test/runtimemajor`, also checks the Containerfile runs it where it
  must. The two scripts and their tests are prowlarr-oci-image's too,
  byte for byte but for the SPDX line, so a change to one goes to both
- **The demo has no `global.json`**, and its target framework is whichever
  its SDK bundles. Renovate's NuGet manager would move a `global.json` on
  its own, apart from the SDK image: a second SDK pin, free to disagree
  with the first. An upstream's arrives with its source at build time,
  where Renovate never sees it

Once in place, these hold of it, and of Prowlarr too, except where a
bullet says otherwise:

- **Every stage is a Docker Hardened Image, by digest**, its signature by
  Docker verified before anything is built: the SDK, the probe's Go, and
  the runtime, `dhi.io/aspnetcore`, which has no shell or package manager.
  Renovate moves the SDK and runtime in one pull request, since an app
  needs the runtime patch its SDK knows
- **The app is framework-dependent**, so a rebuild of the runtime image
  patches the .NET it runs on. It is published offline, after a restore,
  to `/app/bin`, with no target framework in any path; `/app/bin` is
  root-owned and never written. Here it is also the runtime stage's
  `WORKDIR`, and so ASP.NET Core's content root; Prowlarr's runtime stage
  sets no `WORKDIR`
- **One port, 8080, with health beside the page.** The apphost is PID 1
  and the one listener, with .NET's diagnostics socket off. An upstream
  .NET app cannot host a probe subcommand, so the probe, `divbast-template`,
  is a static Go binary of its own, on the standard library, which the
  `HEALTHCHECK` runs. The contract has no health port: `HealthPort` 0, and
  `HealthPath` the app's own health endpoint, here `/healthz`, and
  Prowlarr's `/ping`

### A .NET major

A .NET major moves by hand, about once per LTS, because neither of its two
pull requests can go green alone: upstream's retargeted release needs the
new SDK, and the SDK and runtime the Dashboard holds back cannot build or
run the app upstream had before. The build-stage checks above fail each
with a message naming this section; only an upstream with no
`global.json` that retargets first fails on dotnet's own error instead.
Each step needs the user's go-ahead:

1. The app's Renovate PR is red with one of the checks' messages, where
   `W` is the new .NET:
   `ERROR: global.json asks for the .NET W SDK; … Approve the .NET W major …`
   or `ERROR: the app targets .NET W; … Approve …`.
2. Approve the major, the "dhi .NET SDK and runtime" group, on the
   Dependency Dashboard. Renovate opens its PR, red on its own: a check
   fails it with `ERROR: … A .NET major waits until … retargets …`, which
   here is expected, and no reason to stop.
3. On a branch from main, take both Renovate commits with
   `git cherry-pick -S`, open a PR, and merge it when green.
4. Close the two Renovate PRs.

For the template alone, approving the major is the whole step: the demo's
target framework follows its SDK, and no upstream waits on it.

### The Python layer

`layers/python` is the template's Python app, kept for a Python app to
move into place, and like the others it is linted but never built. Its
`Containerfile` is a whole one, lifted from sabnzbd-oci-image's, which
replaces the root one at step 0 and is written for the layout that
leaves. Each of its images sits where a `FROM` uses it; the toolchain
stage's DHI packages are pinned too, which only the Renovate manager an
app copies from SABnzbd moves. `just lint` runs `python-lint`: ruff
format and ruff check, under every rule but the four `ruff.toml` lifts,
each with its reason, and ty, under `ty.toml`, over the demo; then
`go run ./layers/python/cmd/divbast-pins -C layers/python -check`, which
reads PyPI. Then hadolint and `docker build --check` over the
Containerfile. In CI, the lint job first verifies the layer's images,
through `build.yml`'s `verify-containerfiles`. The Go gates reach the
layer's probe and lock tool.

- **The demo is the standard library alone.** `app/server.py` serves the
  page on `/` and `ok` on `/healthz`, to GET or HEAD alone, whatever the
  query, and an empty 404 to everything else, on 8080 on every address,
  or every IPv4 one where the kernel has no IPv6. As PID 1 it handles
  SIGTERM itself, finishes what it is answering, and exits 0
- **`divbast-pins` locks the Python packages**, lifted from SABnzbd's and
  generalised. `python/requirements.in` holds the app's own requirements,
  or one `-r` line naming its upstream's `requirements.txt`: an https URL
  at a full commit, written with the Containerfile's ARGs as `${NAME}`.
  `python/pins.txt` pins what those leave unpinned or out, and the build
  backends, each under a comment of its kind. It writes
  `requirements.lock`, `build.lock` and `files.txt`, and `-check` holds
  them to the pins. Nothing resolves dependencies: every package is
  named, and the build's `pip check` fails on one that is not. Given
  SABnzbd's inputs, it writes SABnzbd's locks, byte for byte but the
  header; SABnzbd's own copy also writes unrar's checksum, a native tool's
  pin being the app's to add. The demo needs no package, so its locks
  hold their headers alone

Once in place, these hold of it, and of SABnzbd:

- **Every stage is a Docker Hardened Image, by digest**, its signature
  verified first, and every apt package a stage installs is DHI's: the
  toolchain stage deletes Debian's sources first
- **No package's code runs with the network on.** Each file is fetched
  by its sha256 with curl and checked, then pip builds and installs
  offline: source releases with `--no-build-isolation
  --check-build-dependencies`, from a build venv that never ships, into
  `/app/venv`, which holds no pip, passes `pip check`, and is compiled for
  `-OO` with bytecode that needs no timestamps
- **One port, 8080, with health beside the page.** The app is PID 1 on
  the runtime's Python. It cannot host a probe subcommand, so the probe,
  `divbast-template`, is a static Go binary of its own, which the
  `HEALTHCHECK` runs: `HealthPort` 0 and `HealthPath` `/healthz`
- **What a Bazarr- or Calibre-shaped app adds** goes where the
  Containerfile's comments say: a frontend stage on DHI's node image, as
  Lidarr's; a stage for each native tool, as SABnzbd's unrar and 7zz,
  with its `check-elf`; and the declared SBOM, named in the
  `io.github.divbast.scan.sboms` label, which divbast/ci's scan reads

## Where the shared parts live

- **divbast/ci.** `just setup` checks it out into `.ci/`, which git and
  the build context both ignore, at the one commit every workflow calls it
  at: a release's full SHA, with its tag as the comment. Its README is the
  reference for all of it:
  - the reusable workflows: `app-check.yml` (lint; the image built for
    amd64 and arm64 once every image it builds from verifies; the acceptance
    suite on each under Docker and rootless Podman; the scan),
    `app-publish.yml` (the daily slot and the button only: push, index,
    attest, verify, then tag and check as an anonymous client pulls it;
    its `rehearse` input sends all of that to a registry inside the job),
    `renovate.yml` and `stall.yml`. `build.yml`, `renovate.yml` and
    `stall.yml` here call them, and keep their own triggers and the
    `required` job;
  - the recipes the justfile imports, `.ci/just/common.just` and
    `.ci/just/app.just`. Recipes here call them from a recipe's body, never
    as a dependency, so the justfile still parses before `.ci` exists;
  - the Go tools, which `just ci-tools` builds into `.ci/bin/`, run from
    this repository's root: `divbast-pin`, `divbast-verify-images`,
    `divbast-scan`, `divbast-testcount`, `divbast-published-revision`,
    `divbast-publish-decision`, `divbast-stall` and the rest;
  - the acceptance `harness` and the `contract` suite, which `go.mod`
    requires through `replace github.com/divbast/ci => ./.ci`;
  - `go/golangci.yml`, the one golangci-lint configuration, and
    `.github/actionlint.yaml`.

  A change to ci reaches this repository only through the Renovate PR that
  moves every pin of it at once. `just setup`, and the pin check
  (`divbast-pin`) in each ci workflow that runs this repository's code,
  refuse workflows that call ci at more than one commit
- **divbast/frankenphp** builds and attests the PHP layer's binary,
  `ghcr.io/divbast/frankenphp:baikal`, with its pins and the in-build
  checks on them
- **divbast/renovate-config** holds the presets `renovate.json` extends.
  `renovate.json` adds nothing of its own: an app adds its upstream's
  manager and rule there
- **divbast/infra** holds the org's settings, this repository's rulesets
  and its template flag, the `divbast-renovate` App, the `renovate`
  environment and `OCI_IMAGE_TEMPLATE_AUTOMERGE`

## Key commands

- `just setup` — fetch divbast/ci into `.ci/` at the commit the workflows
  pin, then install the pinned toolchain, and PHPStan for the PHP layer. One command on a
  fresh clone, and again whenever the pin moves; `just lint` fails, naming
  it, when `.ci` is not at the pinned commit (`ci-fresh`)
- `just build` — build the image locally. It does not verify the images it
  builds from; `just ci-tools && .ci/bin/divbast-verify-images` does, with
  cosign from `mise.toml`
- `just acceptance` — `go test ./test/acceptance/...` against the image
  `just build` wrote, under `ENGINE` (docker unless it says podman),
  through `divbast-testcount`, which fails the run unless a test passed and
  none failed or skipped. `just IMAGE=… acceptance` tests another image.
  `ENGINE=docker IMAGE=localhost/oci-image-template:dev mise exec -- go test
  -run TestServes ./test/acceptance/` runs one test
- `just test` — build, then the acceptance suite
- `just lint` — pinact, zizmor, actionlint, ShellCheck and shfmt, the Go
  gates (the compiler, `go vet`, `go fix -diff`, `go mod tidy -diff`,
  `golangci-lint fmt --diff` and `golangci-lint run`), `go-test`, `reuse
  lint`, hadolint, `docker build --check`, PHPStan over the PHP layer,
  `dotnet-lint`, hadolint and `docker build --check` over the .NET layer,
  and `python-lint` (ruff, ty and the locks check), hadolint and
  `docker build --check` over the Python layer.
  `go-test` runs every Go test under the race detector; the acceptance
  suite skips there, since `IMAGE` is unset
- `just archive`, `just scan` — write the image as an OCI archive, then run
  `divbast-scan` over it and the published `:latest`. Needs network access
  to ghcr.io
- `just actions-pin`, `just actions-update` — move the workflows' action pins
- `just ci` — every gate, locally. Run this before committing

## Where the toolchain comes from

- **`mise.toml` is where tool versions are written.** Go, golangci-lint,
  hadolint, ShellCheck, shfmt, ruff, ty, actionlint, reuse, just, pinact,
  trivy, grype, zizmor and cosign are all listed there. Every justfile recipe but `setup`, which
  installs them, runs through `mise exec`, so a recipe resolves those
  binaries and not a same-named one on PATH. The recipes that call GitHub,
  pinact's and zizmor's, take a fine-grained token from `GITHUB_TOKEN` and
  nothing else, as divbast/ci's README says; CI passes its own
- **Go builds with the toolchain mise installs and nothing else.**
  `GOTOOLCHAIN=local` keeps `go.mod`'s line from downloading another. And
  `GOFLAGS=-mod=readonly`, because a PHP app made from the template keeps
  Composer's `vendor/` at the module root, where Go would take it for a Go
  vendor directory once `go.mod` has a `require`, and refuse to build;
  readonly is the default outside vendor mode, and still refuses to edit
  `go.mod`
- **`just lint` needs a running Docker daemon**, for `docker build --check`,
  logged in to dhi.io for the .NET and Python layers' images, which
  `--check` reads and `dotnet-lint` runs; and the network, for the Python
  layer's locks check, which reads PyPI. `--check` is a buildx flag with no podman
  equivalent, so that gate does not honour `ENGINE` and does not run
  without dockerd
- **The .NET SDK is not in mise.** The demo is linted in the SDK image the
  .NET layer's backend stage pins, which CI verifies first: one SDK pin,
  and no second one in `mise.toml` to disagree with it. `dotnet-lint` runs
  it under Docker whatever `ENGINE` says, since the dhi.io login is Docker's
- **`just scan` needs `skopeo` on `PATH`.** It reads the published image with
  [skopeo](https://github.com/containers/skopeo), which no `mise.toml` entry
  provides, so `just ci` needs it too. CI runners already have it
- **PHP is a deliberate exception.** `php` and
  `composer` come from the host, not from mise: mise's only PHP backend
  compiles PHP from source, which is minutes added to every CI run in order to
  analyse a handful of files. PHPStan is therefore pinned by the PHP layer's
  `composer.lock`, and its `phpstan.neon` sets `phpVersion` so the
  *analysis target* is fixed no matter which PHP runs the analyser
- **So the PHP layer carries `composer.json`, `composer.lock` and a
  `vendor/`, and that deserves an explanation**, because a PHP image's whole
  claim is that no PHP toolchain reaches it. None of it does. The Containerfile
  copies named paths and never the build context, `vendor/` is gitignored, and
  `.dockerignore` excludes it, and the whole of `layers/`, so that a `COPY`
  written broadly at some later date cannot quietly change the answer. `composer.json` has no `require`
  section, no `autoload` and no package name on purpose: Composer is asked to
  install a linter here, not to make this a PHP project
- Licensing for `composer.json`, `composer.lock` and `renovate.json` lives in
  `.license` sidecars. JSON has no comment syntax, and an SPDX tag written as
  a string value carries the closing quote into the expression, which `reuse`
  rejects

## Code quality

- All linter gates are enforced as errors — fix them, don't suppress them. Any
  suppression, anywhere, needs an explicit, comment-level justification beside
  it to be accepted
- **The scan blocks only what a change would add, and is advisory while
  nothing is published.** `divbast-scan` scans the candidate and the
  published `:latest` for the same arch from one Trivy database, and fails
  only on a HIGH or CRITICAL finding, the same vulnerability in the same
  package, that the published image lacks. No release can make an
  advisory against something already shipped any worse, so blocking on one
  only holds back whatever else the release fixes. Whether anything is
  published is the registry's to say, never Trivy's: the scan reads
  `:latest` as an anonymous client, and when ghcr.io refuses, asks GitHub
  why, with the check job's token, which is what its `packages: read` is
  for. Not published yet, or a package still private: every finding is
  reported, on the summary page, as a warning annotation and on stderr,
  and none blocks. Once the image is public, only new findings block. A
  public image the registry refuses, a registry that cannot be reached, or
  GitHub not answering fails the scan rather than waving it through. A
  local `just scan` never asks GitHub, so a refusal there is advisory,
  with a warning that only CI can tell what it means
- REUSE-compliant SPDX headers on every file; licence is **BSD-2-Clause**.
  The image's licences label names only that, because the image ships
  nothing else of its own; an app that ships an upstream's files adds the
  upstream's licence to the label, and leaves which file carries which
  licence to REUSE and the files' SPDX headers
- **The acceptance suite is Go, on divbast/ci's harness.** Every
  container it starts runs under `harness.Contract("16m")`, the *exact*
  confinement the contract claims, `--security-opt no-new-privileges` and
  `--tmpfs /tmp:size=16m,noexec,nosuid,nodev` included, so a
  check can never pass under looser settings than we ship. What CI proves
  is what ships. `TestContract` runs ci's contract suite against the image;
  `TestServes` and `TestStopsPromptly` are the template's own. Each serves through
  the harness's `Serve` (or `StartServing`, for a container that must be
  started apart from its first request), which publishes on the host's
  loopback at ports the engine picks, so no two tests, or two suites on one
  host, ask for the same one. The assertions (`StatusIs`, `Answers`,
  `Refuses` and the rest) are the harness's too, shared with every divbast
  image. An app gives each thing it serves a test, one topic per file
- **Go is modern and idiomatic, and linted strictly**, under ci's
  `go/golangci.yml`. Bash is for what is short and will stay so: the
  justfile's recipes. A check that runs inside a Containerfile build stage
  is the exception, since a build stage has bash and no Go: it stays bash,
  under ShellCheck `enable=all`, shfmt and `set -euo pipefail`, with a Go
  test that runs it against fixtures and sees each way it can fail fail
- A test suite that has never failed proves nothing. When adding a check, prove
  it can fail before trusting it
