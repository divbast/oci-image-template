<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# oci-image-template

A minimal working divbast app: one page, served by a stdlib Go server, in
an image that runs at zero Linux capabilities, with no new privileges, on a
read-only root filesystem as a non-root user. It goes through divbast/ci's shared pipeline exactly as
a real app does: lint, a build for amd64 and arm64, the confinement
contract and its own acceptance suite under Docker and rootless Podman, a
vulnerability scan, and a daily rehearsed publish.

Every new divbast image repository starts as a copy of this one, whatever
its app is written in. A Go app keeps the Go core. A PHP app served by
FrankenPHP moves the PHP layer, `layers/php`, into place: the same page as
a FrankenPHP app, linted here but not built. A .NET app moves the .NET
layer, `layers/dotnet`, into place: the same page again, from ASP.NET Core,
also linted here but not built.
[Making a new app](#making-a-new-app) lists what to change, in order.

Its publish job rehearses: it runs every step of a real publish against a
registry it starts inside the job, so nothing reaches ghcr.io and there is
no image to pull. [Publishing](#publishing) says how.

## Base

The image is one static Go binary, `/usr/local/bin/template`, on
[distroless](https://github.com/GoogleContainerTools/distroless)'s
`static-debian13`, which holds nothing else but CA certificates, time zones
and the `nonroot` user. The binary is built in a `golang` stage, at the
newest stable Go, with the standard library alone. Both images are pinned by
digest, and CI builds nothing until it has checked that the distroless
project signed the runtime's digest.

The PHP layer runs [FrankenPHP](https://github.com/php/frankenphp) instead:
PHP 8.5 and Caddy in one binary, built by
[divbast/frankenphp](https://github.com/divbast/frankenphp) and copied in
by digest, on `cc-debian13`, as divbast's PHP apps do.

The .NET layer runs a framework-dependent ASP.NET Core app on
[Docker Hardened Images](https://docs.docker.com/dhi/)' ASP.NET Core
runtime, which holds the .NET runtime, ICU and OpenSSL, and no shell or
package manager. The app is built in their .NET SDK image, and its health
probe, a static Go binary on the standard library, in their Go image. All
three are pinned by digest, and CI checks Docker's signature on each before
the lint runs any of them.

## Container Features

| Property | Value |
|---|---|
| Capabilities | none (`--cap-drop ALL`) |
| Privileges | no new ones (`--security-opt no-new-privileges`), under the engine's seccomp filter |
| Root filesystem | read-only |
| User | uid/gid 65532 (`nonroot`) |
| Port | 8080 (the page), 8081 (health, do not publish), and nothing else listens |
| tmpfs required | `/tmp`, 16 MiB, which nothing can run from (`--tmpfs /tmp:size=16m,noexec,nosuid,nodev`) |
| Volume | none |
| Processes | one: the app, `template serve`, as PID 1 |
| Shell in image | none |
| Package manager | none |
| Outbound network | none |
| Healthy | the shipped probe: `/healthz` 200 **and** `GET /` from loopback exactly `Hello from oci-image-template` |

The app writes nothing, so `/tmp` stays empty: the contract grants one
tmpfs, and 16 MiB is what the acceptance suite gives every container it
runs.

Nothing in the image opens a connection out.

## Running

There is no published image, so build one first, with Podman to match the
command below: `just setup`, then `ENGINE=podman just build`, which tags it
`localhost/oci-image-template:dev`.

```bash
podman run -d --name oci-image-template \
  --cap-drop ALL --read-only --read-only-tmpfs=false \
  --security-opt no-new-privileges \
  --user 65532:65532 \
  --tmpfs /tmp:size=16m,noexec,nosuid,nodev \
  -p 8080:8080 \
  localhost/oci-image-template:dev
```

`--read-only-tmpfs=false` is Podman's alone: without it, Podman mounts
writable tmpfs of its own at `/run`, `/var/tmp` and elsewhere beside the
read-only root. Docker takes the same command without it.

What is served on 8080 is an allowlist: `/`, the page,
`Hello from oci-image-template`. Everything else is a 404 with an empty
body. The acceptance suite checks the page, `/index.php`, `/healthz` and a
path nothing names.

Each request is logged to stdout as JSON. A stop ends the server at once:
it takes SIGTERM, finishes what it is answering within 5 seconds, and exits
0.

## Health

A server that answers can still be broken, so the image ships its own probe
and declares a `HEALTHCHECK`. Docker runs it with no configuration.

Podman does not. Podman's `libimage` reads a healthcheck only out of a
Docker-media-type manifest, and divbast images are published as an OCI
index, so Podman's inspection path never looks at the config's healthcheck
field — [podman/podman#25454](https://github.com/containers/podman/issues/25454)
and [#18904](https://github.com/containers/podman/issues/18904) track it. A
Podman user passes it explicitly:

```bash
podman run -d --health-cmd '["/usr/local/bin/template","health"]' localhost/oci-image-template:dev
```

Under a Quadlet, the equivalent is:

```
HealthCmd=["/usr/local/bin/template","health"]
```

The array above has no leading `"CMD"` on purpose: Podman before 5.8.0
re-splits an array that starts with `"CMD"` into one useless token, so
omitting it is what makes the check work across versions.

| | |
|---|---|
| `/usr/local/bin/template health` | The in-container probe `HEALTHCHECK` runs. Asserts `/healthz` is 200 **and** `GET /` on 8080 answers exactly the page. Prints nothing when healthy; otherwise one line, `<check>: <reason>`, for each half that failed, and exits 1 |
| `http://127.0.0.1:8081/healthz` | 200 `ok` while the server runs |

`/healthz` never goes through 8080's routes, so only the page's exact
answer shows the app port serves the app. The probe asks both. Port 8081
serves nothing else and is meant to stay unpublished.

There is no shell in this image, so a health command given as a plain
string, which Docker and Podman both run through `/bin/sh -c`, can never
pass.

## The pipeline

Three workflows, each a short caller of divbast/ci's reusable workflow of
the same purpose, at one pinned release of it:

- `build.yml` calls `app-check.yml` on every push, pull request, daily slot
  and button run: lint, the image built for amd64 and arm64 once every
  image the Containerfile builds from is verified, the acceptance suite against each under
  Docker and rootless Podman, and the scan. Its `required` job is the one
  check `main`'s ruleset requires. From the daily slot and the button, on
  `main`, it then calls `app-publish.yml`.
- `renovate.yml` runs Renovate, as below.
- `stall.yml` fails, and GitHub emails you, when a Renovate pull request
  has been open more than 14 days or red or pending for more than a day, or
  when Renovate has not succeeded in a day.

### Publishing

A merge never publishes. The daily slot publishes `main` when `main` moved
since the commit the published `:latest` was built from, and the button, a
manual run of `build` from `main`, publishes at once.

Here, the publish job calls `app-publish.yml` with `rehearse: true`. It
decides against `ghcr.io/divbast/oci-image-template`, which has never been
published, so every slot and button run publishes. It then starts a
registry inside the job and publishes there instead: it pushes each
architecture, merges them into one index under a staging tag, attests the
index and verifies that attestation, then tags it with the version and
`latest`, and checks both tags as an anonymous client. The attestation is
real, signed for this repository with Sigstore. The run says
`rehearsal: nothing was published to ghcr.io` in a warning and on its
summary.

Only this repository may rehearse: since divbast/ci v2, `rehearse: true`
fails the publish job anywhere else.

## Dependency updates

Every input the Containerfile names is pinned by digest: the `golang`
builder and the distroless runtime. The app is the standard library alone,
so `go.mod` requires nothing but divbast/ci's harness for its tests. The
PHP layer's fragment pins the FrankenPHP binary by the digest of
`ghcr.io/divbast/frankenphp:baikal`, and the .NET layer's Containerfile
its three Docker Hardened Images by tag and digest.

A pin only moves through a pull request, and
[Renovate](https://docs.renovatebot.com/) opens those. It runs every
morning, after every merge to `main`, whenever CI finishes on one of its
branches and when the Dependency Dashboard issue is edited, from
`.github/workflows/renovate.yml`, as the divbast org's `divbast-renovate`
GitHub App. Its settings are divbast/renovate-config's shared presets, and
`renovate.json` adds nothing to them. The pull requests Renovate opens here
are:

- A new Go release, as a `golang` image, arrives as a pull request that
  moves the builder's tag and digest. A new distroless image arrives as one
  that moves its digest, and CI verifies its signature before it builds.
- A new `:baikal` binary arrives as a pull request that moves the PHP
  layer's digest, and a new `cc-debian13` image as one that moves the PHP
  layer's runtime digest.
- A new .NET SDK or ASP.NET Core image arrives as one pull request that
  moves both of the .NET layer's pins, since an app needs the runtime
  patch its SDK knows, and a new Go image for its probe as one of its own.
  dhi.io answers nothing without a login, so `renovate.yml` passes Renovate
  the DHI login divbast/infra keeps here. The demo has no `global.json`,
  and its target framework follows its SDK, so a .NET major moves nothing
  else in the template.
- A release of divbast/ci arrives as one pull request that moves every
  workflow's pin of it together.
- The toolchain in `mise.toml` and PHPStan in `layers/php/composer.lock`
  move by pull requests of their own.

Renovate merges its own pull requests once every check on one has passed,
while the repository variable `OCI_IMAGE_TEMPLATE_AUTOMERGE` is `enabled`.
Three kinds first wait for someone to approve them on the Dependency
Dashboard: a new major version of divbast/ci, a raise of `go.mod`'s `go`
line, and a .NET major. Any other value of the variable, or none, stops
merging from the next run, and Renovate goes on opening and rebasing pull
requests for a person to merge:

    gh variable set OCI_IMAGE_TEMPLATE_AUTOMERGE --body disabled

After a merge it rebases every open update the merge left behind, so each
is tested again against the new `main` before it merges. It merges with a
squash, which GitHub signs, because `main` accepts only signed commits.

The App, the `renovate` environment holding its key,
`OCI_IMAGE_TEMPLATE_AUTOMERGE` and this repository's rulesets are all
managed from [divbast/infra](https://github.com/divbast/infra).

## Making a new app

The repository is made from this template by its entry in divbast/infra,
which names this repository as its `template` and also gives it its
rulesets, its `renovate` environment and its kill switch. divbast/infra's
README says how. The copy arrives as one commit, and nothing in it is
renamed for you. Then, in this order:

0. **Go, PHP or .NET.** Choose once, first, since every step after names
   the files of one of them. Each of the three deletes `layers/`, so
   `.dockerignore`'s `layers/` line goes too, with its comment. A Go or
   PHP app also drops the .NET layer's lint and verification: the
   justfile's `dotnet-lint` recipe; the three `lint` lines after
   PHPStan's, which run it, hadolint and `docker build --check` on the
   layer; what `lint`'s comment says of the layer and dhi.io; and
   `verify-containerfiles` on `build.yml`'s `check` job, with its comment.
   It drops the lines for a local dotnet build's output as well,
   `src/**/bin` and `src/**/obj` in `.dockerignore` and `**/src/**/bin/`
   and `**/src/**/obj/` in `.gitignore`, each pair with its comment: kept,
   they would hide any `bin/` or `obj/` under a `src/` of the app's own
   from git and the build.
   - **A Go app** keeps the Go core: `cmd/template`, `internal/server` and
     the Containerfile as they are. Delete `layers/`, the justfile's
     `composer install` and PHPStan lines, and `php: true` on `build.yml`'s
     `check` job.
   - **A PHP app** served by FrankenPHP moves `layers/php` into place:
     `git mv layers/php/* .` (its `caddy/`, `php/`, `public/`, `rootfs/`,
     `phpstan.neon` and Composer's files), then deletes `cmd/`,
     `internal/` and `layers/`. In the Containerfile, the fragment's lines
     replace the Go app's, each where its comment says: the
     `RUNTIME_IMAGE` `ARG`, the build stage and the `COPY` from it, the
     `HEALTHCHECK` and the `ENTRYPOINT`. Its `FROM ${RUNTIME_IMAGE}` is
     the Containerfile's own, already there, which stands in the fragment
     for Renovate alone. The justfile's `setup` and
     `lint` go back to `composer install` and
     `vendor/bin/phpstan analyse --no-progress` at the root.
     `test/acceptance/contract_test.go`'s `PID1`, `HealthCmd` and `Exec`
     become FrankenPHP's, as the fragment's `ENTRYPOINT` and
     `HEALTHCHECK` give them, with `Exec` its `version`.
     [Baikal](https://github.com/divbast/baikal-oci-image) and
     [LibreSpeed](https://github.com/divbast/librespeed-oci-image) are
     the fully tested PHP apps to follow.
   - **A .NET app** moves `layers/dotnet` into place, over the Go core;
     [Prowlarr](https://github.com/divbast/prowlarr-oci-image) is the fully
     tested .NET app to follow. The layer's `Containerfile` is a whole one,
     written for the layout this leaves. From the repository's root:
     1. `git rm -r cmd internal layers/php Containerfile`.
     2. `git mv layers/dotnet/test/* test/`, beside `test/acceptance`.
     3. `git mv layers/dotnet/Containerfile layers/dotnet/cmd layers/dotnet/internal layers/dotnet/src .`
        Then delete the Containerfile's opening comment, on the layer and
        this move, which no longer holds.
     4. `rm -rf layers`: nothing tracked is left in it, but PHPStan's
        `vendor/` may be.
     5. In `cmd/divbast-template/main.go` and
        `internal/probe/probe_test.go`, drop `/layers/dotnet` from the
        probe's import path, which leaves
        `github.com/divbast/oci-image-template/internal/probe` for step 1
        to rename with the module.
     6. In the justfile, delete `composer install` from `setup` and
        PHPStan from its comment, every `lint` line but the first, and
        the `dotnet-lint` recipe. The first line's `app-lint` lints the
        root Containerfile, now the .NET one, so `lint`'s comment says the
        Docker daemon must be logged in to dhi.io for its images.
     7. On `build.yml`'s `check` job, delete `php: true` and
        `verify-containerfiles`, each with its comment. `dhi-username` and
        `DHI_TOKEN` stay, since the build reads dhi.io now.
     8. In `test/acceptance/contract_test.go`, health moves to the app
        port, beside the page: delete the `healthPort` constant and
        `HealthOnly404`, and set `HealthPort: 0` and
        `HealthPath: "/healthz"`. `binary` becomes the probe,
        `/usr/local/bin/divbast-template`, which `Exec` and `healthCmd`
        run, and `serverArgv`, PID 1's argv, the apphost alone:
        `[]string{"/app/bin/Template"}`. Their comments follow.
     9. In `test/acceptance/serves_test.go`, delete `"/healthz"` from
        `refused`, and check with `harness.StatusIs` that GET `/healthz`
        answers 200. The comments follow here too.
     10. Give the app's entry in divbast/infra `dhi = true`, which writes
         the DHI login the workflows pass.
1. **Names.** Pick the app's name, `<app>`: the image will be
   `ghcr.io/divbast/<app>`. Set it in:
   - `go.mod`'s module path, `github.com/divbast/<repository>`, and every
     import of it, in `cmd/` and `internal/` and their tests, which a PHP
     app no longer has;
   - the justfile's `APP`, from which `IMAGE`, `ARCHIVE` and `PUBLISHED`
     follow;
   - the Containerfile: `TEMPLATE_VERSION`, renamed `<APP>_VERSION` and set
     to the app's version, the `LABEL`s, and the paths below;
   - a Go app's binary, `cmd/<app>`, and `/usr/local/bin/<app>` wherever
     the Containerfile and `test/acceptance/contract_test.go` name it;
   - a PHP app's health probe and its endpoint,
     `rootfs/usr/local/bin/<app>-health` and
     `rootfs/usr/local/share/<app>-health/`, `php/<app>.ini` and the
     served tree, `/var/www/<app>`, everywhere each is named: the
     Containerfile, the Caddyfile, `phpstan.neon`, the probe's messages
     and `test/acceptance/contract_test.go`;
   - a .NET app's probe, `cmd/divbast-template` and
     `/usr/local/bin/divbast-template`, as `divbast-<app>`, everywhere it
     is named: the Containerfile, the probe's messages and
     `test/acceptance/contract_test.go`. The demo keeps its names,
     `src/Template`, `Template.csproj` and `/app/bin/Template`, until step
     6 replaces it with the app, whose project file's name is its
     apphost's, and so PID 1's path;
   - `build.yml`'s `version-arg` and `description`;
   - this README and `CLAUDE.md`.
2. **The crons.** Give `build.yml`'s daily slot and `stall.yml`'s check
   times of their own, and update the comments that give their times and
   their neighbours', so that neither starts with another repository's:
   the organisation runs at most 20 jobs at once. `renovate.yml`'s cron can
   stay as it is, since divbast's repositories share it.
3. **The kill switch.** In `renovate.yml`, replace
   `OCI_IMAGE_TEMPLATE_AUTOMERGE`, in the job and in the comment above it,
   with the variable the app's entry in divbast/infra names, such as
   `LIBRESPEED_AUTOMERGE`. Do the same in this README and `CLAUDE.md`.
4. **The upstream pin.** Pin what the app is built from in the
   Containerfile, by version and by digest or commit, and give it a
   Renovate manager in `renovate.json` in the same change: a `customManagers`
   entry for an `ARG` no `FROM` reads, and a `packageRules` entry naming its
   labels and commit prefix. LibreSpeed's `renovate.json` and its fetch
   stage are a worked example. A .NET app's fetch stage replaces the
   backend stage's `COPY src/ ./`, as Prowlarr's does. An app that ships
   the upstream's files adds the upstream's licence to the Containerfile's
   `org.opencontainers.image.licenses` `LABEL`, as `CLAUDE.md` requires.
   Which licence each file in the repository carries is REUSE's to say, by
   its SPDX header, not this README's.
5. **The contract.** Set what the app's confinement contract says, in
   `test/acceptance/contract_test.go` and in the Containerfile alike, then
   in the tables here and in `CLAUDE.md`, the Volume row among them: the
   `/tmp` size, the paths `HealthOnly404` asks the health port to refuse,
   PID 1's argv, the health command, `Exec`, a binary of the app's own
   that runs to exit 0, and the `HEALTHCHECK` timings. An app
   with a bootstrap keeps `PID1` as the server's argv, which the bootstrap
   execs. The template leaves the contract's optional fields unset, and an
   app may need them: `Mounts` for a volume, so an app with `/data` mounts
   one there; `Env` for the environment the app reads; `ExtraTCPPorts` for
   a listener past the two ports; `Ready` for what must be true before the
   checks run, past both ports answering; `AppProbe`, a Go main package,
   or `PHP`, an `app.php` run under the app's PHP, for probe subcommands
   of the app's own; and `Scenario` to run the contract once for each
   configuration it supports. Each is
   documented on `contract.Config`, `go -C .ci doc ./contract Config`, and
   divbast/ci's README, under "Go", says how the package reaches the suite.
6. **The app.** Replace the page with the app, keeping `:8080` an
   allowlist, and give the acceptance suite a test for each thing it
   serves. Replace the page half of the probe, and add the app's own checks
   to `/healthz`. A Go app replaces `internal/server`; a PHP app replaces
   `public/index.php` and the Caddyfile's `:8080` routes, and lists every
   PHP file the image ships in `phpstan.neon`'s `paths`; a .NET app
   deletes the demo, `src/`, which its fetch stage replaced at step 4, and
   names the app's project where the Containerfile names
   `Template/Template.csproj`, and its apphost wherever `/app/bin/Template`
   is named.
7. **A PHP app's own binary.** A Go or .NET app skips this. Add the app to
   divbast/frankenphp in one pull request there:
   - `apps/<app>.env`, holding each of `PHP_EXTENSIONS`,
     `PHP_EXTENSION_LIBS`, `EXPECTED_EXTENSIONS` and
     `REQUIRED_GD_FEATURES` exactly once. The first three must not be
     empty; `REQUIRED_GD_FEATURES` is empty for an app without gd.
     `EXPECTED_EXTENSIONS` is every module the binary loads, in lower case;
   - `<app>` in both `app:` lists in `.github/workflows/build.yml`, the
     `build` job's matrix (`app: [baikal, librespeed]`) and the `publish`
     job's;
   - a `slots-<app>` job, named `slots (<app>)`, in
     `.github/workflows/stall.yml`, as `slots-baikal` is, with
     `published: ghcr.io/divbast/frankenphp:<app>`;
   - `<app>` in the list that `TestRepositoryApps`, in
     `internal/appenv/appenv_test.go`, expects:
     `[]string{"baikal", "librespeed"}`;
   - frankenphp's README: a row for `:<app>` in its table of tags, and the
     app's entry in its list of the C libraries each app builds that spc
     takes from their newest release.

   Once that merges and `ghcr.io/divbast/frankenphp:<app>` is published,
   point the Containerfile's one `COPY --from=` at it by that digest. Until
   then the app can keep building on `:baikal`: CI verifies the
   attestation of any divbast/frankenphp binary.
8. **The publish switch.** In `build.yml`'s `publish` job, set `image` to
   `ghcr.io/divbast/<app>` and remove `rehearse: true`. Do this once the app
   is ready to publish, not before: it is what lets the first image reach
   `ghcr.io/divbast/<app>` and creates the package. Until then each daily
   slot's publish job fails by design, because divbast/ci rehearses only
   in `divbast/oci-image-template`, so a repository made from the template
   that keeps `rehearse: true` goes red at its publish job. Once that is on
   `main`, publish once by the button: run `build` from `main` by hand.
   Confirm that the package is public. The run's last step pulls both tags
   as an anonymous client, and fails while the package is private; a
   package that starts private is made public in its settings, and the
   button run again. **Then** turn slots on in `stall.yml`: remove its
   `checks` line, whose default runs all four checks, slots among them,
   and add `published: ghcr.io/divbast/<app>:latest` under its `with`.
   LibreSpeed's `stall.yml` is that shape: `published` alone. In that
   order: until the image is public, slots count every slot run since the
   repository was made as a lost slot. Then rewrite what says the app
   rehearses: this README's opening, "Running", where it says there is no
   published image, and "Publishing"; `CLAUDE.md`'s opening, where it says
   the app publishes nothing to ghcr.io, and its bullet on the rehearsal;
   and the comment above `rehearse` in `build.yml`. The one above `checks`
   in `stall.yml` goes with that line.
9. **The organisation's profile.** Add the image's row to the table in
   divbast/.github's profile README.

## Development

- `just setup` fetches divbast/ci into `.ci/` at the commit the workflows
  pin, then installs the pinned toolchain from `mise.toml` and PHPStan,
  for the PHP layer, from `layers/php/composer.lock`. PHP and Composer
  come from the host.
- `just scan` reads the published image with
  [skopeo](https://github.com/containers/skopeo), which must be on `PATH`;
  so must it for `just ci`. CI runners already have it.
- `just build` builds the image locally, without verifying the images it
  builds from; `just ci-tools && .ci/bin/divbast-verify-images` does, with
  cosign from `mise.toml`, and gh 2.102.0 or newer for a FrankenPHP
  binary. With `-containerfile layers/dotnet/Containerfile`, and
  `DHI_USERNAME` and `DHI_TOKEN` set, it verifies the .NET layer's.
- `just test` builds, then runs the acceptance suite against the image,
  under `ENGINE`, docker unless it says podman.
- `just lint` runs every lint gate, and needs a running Docker daemon, for
  `docker build --check`, logged in to dhi.io for the .NET layer's images,
  which it reads and runs.
- `just ci` runs every gate, the scan included. Run it before committing.

The recipes that call GitHub, pinact's and zizmor's, read a fine-grained
personal access token from `GITHUB_TOKEN`: divbast/ci's README says which,
and how to pass it.

## AI/LLM Disclosure

This project was developed with significant LLM involvement. Each git commit contains an `Assisted-by:` tag detailing the particular model/tool used.

## Licence

Copyright 2026 Aryan Ameri.

[BSD-2-Clause](LICENSES/BSD-2-Clause.txt)

This project is [REUSE](https://reuse.software/) compliant.
