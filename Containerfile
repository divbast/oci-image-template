# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# The app's own version, which tags the image: app-publish.yml reads it from
# this line, by the name build.yml gives as version-arg
ARG TEMPLATE_VERSION=0.2.0
# The runtime: static-debian13, since the binary is static, with nothing in
# it but CA certificates, time zones and /etc/passwd. CI verifies its
# signature by the distroless project before anything is built.
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5

# The newest stable Go, by digest. Only the binary leaves this stage.
FROM golang:1.27.4-trixie@sha256:430985542100b871e108ccc00e8dcc5eef08ca14ee5825e98ebf563d8ffec4ee AS build
ARG TEMPLATE_VERSION
WORKDIR /src
# go.mod's replace names ./.ci, which the build context leaves out: the app
# imports nothing from divbast/ci, so Go never looks for it
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
    go build -trimpath -ldflags="-s -w -X main.version=${TEMPLATE_VERSION}" -o /out/template ./cmd/template

FROM ${RUNTIME_IMAGE}
ARG TEMPLATE_VERSION

# Root-owned, and never written
COPY --from=build /out/template /usr/local/bin/template

LABEL org.opencontainers.image.source="https://github.com/divbast/oci-image-template" \
    org.opencontainers.image.url="https://github.com/divbast/oci-image-template" \
    org.opencontainers.image.title="oci-image-template" \
    org.opencontainers.image.version="${TEMPLATE_VERSION}" \
    org.opencontainers.image.licenses="BSD-2-Clause"

EXPOSE 8080 8081
# Exec form: the shell form would need /bin/sh, which the image lacks.
# --timeout must clear template health's own budget: it allows each of its
# two exchanges 4 s and runs them one after the other, so a listener that
# accepts and never answers costs 8 s. A shorter timeout kills the probe
# mid-flight and records a bare engine timeout instead of the probe's own
# line, which is the only diagnostic there is. Podman reads no HEALTHCHECK
# from an image published as an OCI index: its users pass the same array
# as --health-cmd.
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/template", "health"]
USER 65532:65532
# The server itself, as PID 1: the image has no state to prepare and no
# environment to read, so nothing runs before it
ENTRYPOINT ["/usr/local/bin/template", "serve"]
