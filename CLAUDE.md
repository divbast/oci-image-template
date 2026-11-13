<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# oci-image-template

A minimal working divbast app: one PHP page served by FrankenPHP, in a
container image that runs at zero Linux capabilities on a read-only root
filesystem as a non-root user. Every new divbast image repository is made
from it, by its entry in `divbast/infra`, and then changed as the README's
"Making a new app" says, in that order.

It publishes nothing to ghcr.io: its publish job rehearses. The divbast
organisation's settings, rulesets and Renovate wiring live in
`divbast/infra`.

What makes the image this app's lives here: the Containerfile, the Caddy
and PHP configuration, the page, the health probe and endpoint, and the
acceptance tests. The pipeline around it is shared, and reaches this
repository at a pinned commit:
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
| Root filesystem | **read-only** |
| User | uid/gid **65532**, never root |
| Listen port | **8080** for the page — never 80 |
| Health port | **8081** — health only, never serves the page, meant to stay unpublished |
| Other listeners | **none**: Caddy's admin API is **off** (`admin off`) |
| tmpfs required | exactly one: `/tmp`, at **16 MiB** |
| Volume | **none** |
| Outbound network | **none** |
| Shell, package manager | **none** |
| Processes | **one tree**, FrankenPHP as PID 1: `frankenphp run --config /etc/frankenphp/Caddyfile` |
| Healthy | the shipped probe: `/healthz` **200** and loopback `GET /` **exactly** `Hello from frankenphp` |
| Engine healthcheck | Docker runs it automatically. **Podman does not** — pass `--health-cmd` yourself |

- **Podman never sees this image's `HEALTHCHECK`.** Its `libimage` reads a
  healthcheck only from a Docker-media-type manifest; divbast images publish
  as an OCI index, so Podman's OCI inspection branch never reaches the
  config's healthcheck field — no version of Podman does. Give Podman users
  `--health-cmd '["/usr/local/bin/frankenphp","php-cli","/usr/local/bin/template-health"]'`
  (a Quadlet's `HealthCmd=` takes the same value) — no leading `"CMD"`,
  because Podman before 5.8.0 re-splits such an array into one token instead
  of running it. Tracked upstream as podman/podman#25454 and #18904. The
  contract suite checks the image's `HEALTHCHECK` is that array in exec
  form, and has each engine judge the container: Docker by the image's
  `HEALTHCHECK`, Podman by the same array passed as `--health-cmd`
- **The shipped probe asserts two halves, and neither implies the other.**
  `/healthz` on 8081 checks `ini` from inside the server, but never goes
  through 8080's routes. Only the page's exact answer proves Caddy routes
  `/` to PHP and FrankenPHP's server runs it: `PHP_SAPI` in it reads
  `frankenphp` only there. Anything checking this image should run the
  probe rather than reimplement either half. It prints nothing when
  healthy, which the contract's `health-probe` check asserts; each 503 line
  is `<check>: <reason>` and the probe relays it: with no shell and no
  access log on 8081, the engine's health log is the only record of why
- **8080 is an allowlist.** It serves `/`, which the Caddyfile rewrites to
  `index.php`, and answers 404 to everything else, `/index.php` and
  `/healthz` included. **No `php_server`**: it hands every path it finds no
  file for to `index.php`, so `/healthz` on 8080 would answer 200, and the
  contract's `health-port-only` check fails on that. Every route is a
  `handle` block, because Caddy sorts a bare directive after the catch-all
  404
- **Nothing listens but 8080 and 8081.** Caddy's admin API would otherwise
  listen on 127.0.0.1:2019 and take a new config, with no credentials, from
  anything sharing the container's network: under host networking, every
  process on the host. The contract suite reads the kernel's socket tables
  and fails on any other TCP port a process in the container listens on,
  and on any UDP socket one holds
- **One process tree, always.** FrankenPHP is a single binary containing PHP
  and Caddy, and it is the ENTRYPOINT, so it is PID 1. Nothing runs before
  it: the app has no state to prepare and no environment to read. An app
  that needs a bootstrap `pcntl_exec`s the same argv from it, as
  LibreSpeed's does. Do not add a supervisor, s6, or a second long-running
  process
- **Zero egress.** `allow_url_fopen` is off, and `/healthz` fails if the
  server runs with it on. The contract suite proves the image serves and is
  healthy on a genuinely `--internal` network, and cannot connect out
- **Nothing may add a shell or a package manager to the final image.** The
  base is distroless; that is the point. What it removes is us running apt,
  not the patching itself - `cc-debian13` still ships libc6, libssl3t64,
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
  merging from the next run. A new major of divbast/ci, and a raise of
  `go.mod`'s `go` line, wait for approval on the Dependency Dashboard
  first. **A merge never publishes.** `build.yml`'s daily run is the
  publishing slot, through divbast/ci's `app-publish.yml`, and a manual
  run of `build` publishes at once. `.github/workflows/stall.yml`, through
  divbast/ci's `stall.yml`, fails each day that updates have stopped
  flowing: a Renovate PR stuck, or Renovate silent
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
- **The FrankenPHP binary is divbast/frankenphp's, by digest.** The runtime
  stage copies it from `ghcr.io/divbast/frankenphp:baikal@sha256:…`, the
  binary built for the Baikal image: the page needs only core PHP, so any
  of divbast/frankenphp's binaries would do. Before `app-check.yml` builds
  anything, `divbast-verify-frankenphp` has gh verify that frankenphp's own
  `build.yml`, on its main, attested that digest. It counts every mention
  of the image in the Containerfile's instructions, wherever it stands, and
  refuses all but exactly one, that `COPY`'s `--from=`: never name the
  image in an `ARG`, a `LABEL` or a `RUN`. A local `just build` does not
  verify; `just ci-tools && .ci/bin/divbast-verify-frankenphp` does.
  Renovate moves the digest when frankenphp publishes a new `:baikal`
- **The binary must never carry a file capability.** The official FrankenPHP build
  carries `cap_net_bind_service=ep`, and a binary with a file capability cannot be
  exec'd at all under `--cap-drop ALL` — it dies with `Operation not permitted`
  and exit 126 before PHP starts. divbast/frankenphp refuses to publish a
  binary that carries one, and the contract suite's `file-capability` check
  fails on one
- **The application tree stays root-owned and unwritten.** Everything that
  changes lives on `/tmp`: Caddy's own state, in `/tmp/caddy`, through
  `XDG_DATA_HOME` and `XDG_CONFIG_HOME`. The Containerfile copies each file
  with no `--chmod`, because `COPY --chmod` gives a directory it creates the
  file's mode, and a directory of mode 0644 cannot be entered; the files
  keep the checkout's modes, and the acceptance suite fails on one uid
  65532 cannot read

## Where the shared parts live

- **divbast/ci.** `just setup` checks it out into `.ci/`, which git and
  the build context both ignore, at the one commit every workflow calls it
  at: a release's full SHA, with its tag as the comment. Its README is the
  reference for all of it:
  - the reusable workflows: `app-check.yml` (lint; the image built for
    amd64 and arm64 once the binary's attestation verifies; the acceptance
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
    this repository's root: `divbast-pin`, `divbast-verify-frankenphp`,
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
- **divbast/frankenphp** builds and attests the binary,
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
  pin, then install the pinned toolchain and PHPStan. One command on a
  fresh clone, and again whenever the pin moves; `just lint` fails, naming
  it, when `.ci` is not at the pinned commit (`ci-fresh`)
- `just build` — build the image locally. It does not verify the binary's
  attestation; `just ci-tools && .ci/bin/divbast-verify-frankenphp` does,
  with gh 2.102.0 or newer
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
  lint`, hadolint, `docker build --check` and PHPStan. `go-test` runs every
  Go test under the race detector; the acceptance suite skips there, since
  `IMAGE` is unset
- `just archive`, `just scan` — write the image as an OCI archive, then run
  `divbast-scan` over it and the published `:latest`. Needs network access
  to ghcr.io
- `just actions-pin`, `just actions-update` — move the workflows' action pins
- `just ci` — every gate, locally. Run this before committing

## Where the toolchain comes from

- **`mise.toml` is where tool versions are written.** Go, golangci-lint,
  hadolint, ShellCheck, shfmt, actionlint, reuse, just, pinact, trivy and
  zizmor are all listed there. Every justfile recipe but `setup`, which
  installs them, runs through `mise exec`, so a recipe resolves those
  binaries and not a same-named one on PATH. The recipes that call GitHub,
  pinact's and zizmor's, take a fine-grained token from `GITHUB_TOKEN` and
  nothing else, as divbast/ci's README says; CI passes its own
- **Go builds with the toolchain mise installs and nothing else.**
  `GOTOOLCHAIN=local` keeps `go.mod`'s line from downloading another. And
  `GOFLAGS=-mod=readonly`, because Composer's `vendor/` sits at the module
  root, where Go would take it for a Go vendor directory once `go.mod` has a
  `require`, and refuse to build; readonly is the default outside vendor
  mode, and still refuses to edit `go.mod`
- **`just lint` needs a running Docker daemon**, for `docker build --check`.
  `--check` is a buildx flag with no podman equivalent, so this one gate does
  not honour `ENGINE` and does not run without dockerd
- **PHP is a deliberate exception.** `php` and
  `composer` come from the host, not from mise: mise's only PHP backend
  compiles PHP from source, which is minutes added to every CI run in order to
  analyse a handful of files. PHPStan is therefore pinned by `composer.lock`,
  and `phpstan.neon` sets `phpVersion` so the *analysis target* is fixed no
  matter which PHP runs the analyser
- **So this repository carries `composer.json`, `composer.lock` and a
  `vendor/`, and that deserves an explanation**, because the image's whole
  claim is that no PHP toolchain reaches it. None of it does. The Containerfile
  copies named paths and never the build context, `vendor/` is gitignored, and
  `.dockerignore` excludes it too so that a `COPY` written broadly at some
  later date cannot quietly change the answer. `composer.json` has no `require`
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
  only holds back whatever else the release fixes. "Nothing published"
  means the registry answers not-found or denied to an anonymous pull of
  `:latest`; then every finding is reported, on the summary page, as a
  warning annotation and on stderr, and none blocks. A registry that cannot
  be reached fails the scan rather than waving it through
- REUSE-compliant SPDX headers on every file; licence is **BSD-2-Clause**.
  The image's licences label names only that, because the image ships
  nothing else of its own; an app that ships an upstream's files adds the
  upstream's licence to the label and the README together
- **The acceptance suite is Go, on divbast/ci's harness.** Every
  container it starts runs under `harness.Contract("16m")`, the *exact*
  confinement the contract claims, `--tmpfs /tmp:size=16m` included, so a
  check can never pass under looser settings than we ship. What CI proves
  is what ships. `TestContract` runs ci's contract suite against the image;
  `TestServes` asks for the page from the host, through a published port,
  on a fixed host port of its own (18480), since the harness cannot read
  back one the engine picked. An app gives each thing it serves a test,
  one topic per file, each on host ports of its own
- **Go is modern and idiomatic, and linted strictly**, under ci's
  `go/golangci.yml`. Bash is for what is short and will stay so: the
  justfile's recipes. A check that runs inside a Containerfile build stage
  is the exception, since a build stage has bash and no Go: it stays bash,
  under ShellCheck `enable=all`, shfmt and `set -euo pipefail`, with a Go
  test that runs it against fixtures and sees each way it can fail fail
- A test suite that has never failed proves nothing. When adding a check, prove
  it can fail before trusting it
