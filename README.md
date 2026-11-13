<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# oci-image-template

A minimal working divbast app: one PHP page, served by FrankenPHP, in an
image that runs at zero Linux capabilities on a read-only root filesystem
as a non-root user. It goes through divbast/ci's shared pipeline exactly as
a real app does: lint, a build for amd64 and arm64, the confinement
contract and its own acceptance suite under Docker and rootless Podman, a
vulnerability scan, and a daily rehearsed publish.

Every new divbast image repository starts as a copy of this one.
[Making a new app](#making-a-new-app) lists what to change, in order.

Its publish job rehearses: it runs every step of a real publish against a
registry it starts inside the job, so nothing reaches ghcr.io and there is
no image to pull. [Publishing](#publishing) says how.

## Base

The image runs [FrankenPHP](https://github.com/php/frankenphp): PHP 8.5 and
Caddy in one binary, on a Debian Trixie-based distroless base. The binary
is one that [divbast/frankenphp](https://github.com/divbast/frankenphp)
builds, `ghcr.io/divbast/frankenphp:baikal`, built for the Baikal image
from pinned sources. The page needs nothing beyond core PHP, so any of
divbast/frankenphp's binaries would serve. The Containerfile copies it in by
digest, and CI builds nothing until it has checked that divbast/frankenphp's
own build on its `main` attested that digest.

## Container Features

| Property | Value |
|---|---|
| Capabilities | none (`--cap-drop ALL`) |
| Root filesystem | read-only |
| User | uid/gid 65532 (`nonroot`) |
| Port | 8080 (the page), 8081 (health, do not publish), and nothing else listens: Caddy's admin API is off |
| tmpfs required | `/tmp`, 16 MiB (`--tmpfs /tmp:size=16m`) |
| Volume | none |
| Processes | one: FrankenPHP, as PID 1 |
| Shell in image | none |
| Package manager | none |
| Outbound network | none |
| Healthy | the shipped probe: `/healthz` 200 **and** `GET /` from loopback exactly `Hello from frankenphp` |

`/tmp` holds only Caddy's own state, in `/tmp/caddy`, the configuration it
autosaves among it. 16 MiB is what the acceptance suite gives every
container it runs.

Nothing in the image opens a connection out, and PHP's URL streams are off.

## Running

There is no published image, so build one first, with Podman to match the
command below: `just setup`, then `ENGINE=podman just build`, which tags it
`localhost/oci-image-template:dev`.

```bash
podman run -d --name oci-image-template \
  --cap-drop ALL --read-only \
  --user 65532:65532 \
  --tmpfs /tmp:size=16m \
  -p 8080:8080 \
  localhost/oci-image-template:dev
```

What is served on 8080 is an allowlist: `/`, the page, which PHP writes as
`Hello from frankenphp`, the name of the server API it ran under. Everything
else is a 404 with an empty body, `/index.php` included. The acceptance
suite checks the page, `/index.php` and a path nothing names.

The access log goes to stdout and PHP's errors to stderr, both as JSON.

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
podman run -d --health-cmd '["/usr/local/bin/frankenphp","php-cli","/usr/local/bin/template-health"]' localhost/oci-image-template:dev
```

Under a Quadlet, the equivalent is:

```
HealthCmd=["/usr/local/bin/frankenphp","php-cli","/usr/local/bin/template-health"]
```

The array above has no leading `"CMD"` on purpose: Podman before 5.8.0
re-splits an array that starts with `"CMD"` into one useless token, so
omitting it is what makes the check work across versions.

| | |
|---|---|
| `/usr/local/bin/template-health` | The in-container probe `HEALTHCHECK` runs. Asserts `/healthz` is 200 **and** `GET /` on 8080 answers exactly the page. Prints nothing when healthy; otherwise one line for each half that failed, and exits 1 |
| `http://127.0.0.1:8081/healthz` | 200 `ok`, or 503 with one line, `<check>: <reason>`, for each check that failed |

`/healthz` runs one check, `ini`: inside the server, `allow_url_fopen` and
`display_errors` are off and `log_errors` is on. It never goes through
8080's routes, so only the page's exact answer shows Caddy routing `/` to
PHP and PHP running it. The probe asks both. Port 8081 serves nothing else
and is meant to stay unpublished.

There is no shell in this image, so a health command given as a plain
string, which Docker and Podman both run through `/bin/sh -c`, can never
pass.

## The pipeline

Three workflows, each a short caller of divbast/ci's reusable workflow of
the same purpose, at one pinned release of it:

- `build.yml` calls `app-check.yml` on every push, pull request, daily slot
  and button run: lint, the image built for amd64 and arm64 once the
  binary's attestation verifies, the acceptance suite against each under
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

## Dependency updates

Every input the Containerfile names is pinned by digest: the distroless
runtime image, and the FrankenPHP binary, by the digest of
`ghcr.io/divbast/frankenphp:baikal`. The binary's own inputs are pinned in
divbast/frankenphp, apart from the C libraries static-php-cli builds into
it, most of which come from their newest release whenever the binary is
built; sqlite is held at a fixed URL. So a new `:baikal` digest can carry
newer C code with no pin moved, and that is how fixes to those libraries
arrive.

A pin only moves through a pull request, and
[Renovate](https://docs.renovatebot.com/) opens those. It runs every
morning, after every merge to `main`, whenever CI finishes on one of its
branches and when the Dependency Dashboard issue is edited, from
`.github/workflows/renovate.yml`, as the divbast org's `divbast-renovate`
GitHub App. Its settings are divbast/renovate-config's shared presets, and
`renovate.json` adds nothing to them. The pull requests Renovate opens here
are:

- A new `:baikal` binary arrives as a pull request that moves the digest,
  and CI verifies the new digest's attestation before it builds.
- A new distroless image arrives as a pull request that moves its digest.
- A release of divbast/ci arrives as one pull request that moves every
  workflow's pin of it together.
- The toolchain in `mise.toml` and PHPStan in `composer.lock` move by pull
  requests of their own.

Renovate merges its own pull requests once every check on one has passed,
while the repository variable `OCI_IMAGE_TEMPLATE_AUTOMERGE` is `enabled`.
Two kinds first wait for someone to approve them on the Dependency
Dashboard: a new major version of divbast/ci, and a raise of `go.mod`'s
`go` line. Any other value of the variable, or none, stops merging from the
next run, and Renovate goes on opening and rebasing pull requests for a
person to merge:

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

1. **Names.** Pick the app's name, `<app>`: the image will be
   `ghcr.io/divbast/<app>`. Set it in:
   - `go.mod`'s module path, `github.com/divbast/<repository>`;
   - the justfile's `APP`, from which `IMAGE`, `ARCHIVE` and `PUBLISHED`
     follow;
   - the Containerfile: `TEMPLATE_VERSION`, renamed `<APP>_VERSION` and set
     to the app's version, the `LABEL`s, and the paths below;
   - the health probe and its endpoint, `rootfs/usr/local/bin/<app>-health`
     and `rootfs/usr/local/share/<app>-health/`, `php/<app>.ini` and the
     served tree, `/var/www/<app>`, everywhere each is named: the
     Containerfile, the Caddyfile, `phpstan.neon`, the probe's messages
     and `test/acceptance/contract_test.go`;
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
   stage are a worked example. An app that ships the upstream's files adds
   the upstream's licence to the Containerfile's
   `org.opencontainers.image.licenses` `LABEL`, as `CLAUDE.md` requires,
   and says which file carries which licence in this README.
5. **The contract.** Set what the app's confinement contract says, in
   `test/acceptance/contract_test.go` and in the Containerfile alike, then
   in the tables here and in `CLAUDE.md`, the Volume row among them: the
   `/tmp` size, the paths `HealthOnly404` asks the health port to refuse,
   PID 1's argv, the health command and the `HEALTHCHECK` timings. An app
   with a bootstrap keeps `PID1` as the server's argv, which the bootstrap
   execs. The template leaves the contract's optional fields unset, and an
   app may need them: `Mounts` for a volume, so an app with `/data` mounts
   one there; `Env` for the environment the app reads; `Ready` for what
   must be true before the checks run, past both ports answering;
   `ProbeApp` for probe subcommands of the app's own; and `Scenario` to run
   the contract once for each configuration it supports. Each is
   documented on `contract.Config`, `go -C .ci doc ./contract Config`, and
   divbast/ci's README, under "Go", says how the package reaches the suite.
6. **The app.** Replace `public/index.php` and the Caddyfile's `:8080`
   routes with the app, keeping `:8080` an allowlist, and give the
   acceptance suite a test for each thing it serves. Replace the page half
   of the probe, and add the app's own checks to `/healthz`. List every PHP
   file the image ships in `phpstan.neon`'s `paths`.
7. **Its own binary.** Add the app to divbast/frankenphp in one pull
   request there:
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
   `ghcr.io/divbast/<app>` and remove `rehearse: true`. Once that is on
   `main`, publish once by the button: run `build` from `main` by hand.
   Confirm that the package is public. The run's last step pulls both tags
   as an anonymous client, and fails while the package is private; a
   package that starts private is made public in its settings, and the
   button run again. **Then** turn slots on in `stall.yml`: remove its
   `checks` line, whose default runs all four checks, slots among them,
   and add `published: ghcr.io/divbast/<app>:latest` under its `with`.
   LibreSpeed's `stall.yml` is that shape: `published` alone. In that
   order: until the image is public, slots count every slot run since the
   repository was made as a lost slot. Then rewrite what says the app rehearses: this README's
   opening, "Running", where it says there is no published image, and
   "Publishing"; `CLAUDE.md`'s opening, where it says the app publishes
   nothing to ghcr.io, and its bullet on the rehearsal; and the comment
   above `rehearse` in `build.yml`. The one above `checks` in `stall.yml`
   goes with that line.
9. **The organisation's profile.** Add the image's row to the table in
   divbast/.github's profile README.

## Development

- `just setup` fetches divbast/ci into `.ci/` at the commit the workflows
  pin, then installs the pinned toolchain from `mise.toml` and PHPStan from
  `composer.lock`. PHP and Composer come from the host.
- `just build` builds the image locally, without verifying the binary's
  attestation; `just ci-tools && .ci/bin/divbast-verify-frankenphp` does,
  with gh 2.102.0 or newer.
- `just test` builds, then runs the acceptance suite against the image,
  under `ENGINE`, docker unless it says podman.
- `just lint` runs every lint gate, and needs a running Docker daemon, for
  `docker build --check`.
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
