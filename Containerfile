# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# The app's own version, which tags the image: app-publish.yml reads it from
# this line, by the name build.yml gives as version-arg
ARG TEMPLATE_VERSION=0.1.0
ARG RUNTIME_IMAGE=gcr.io/distroless/cc-debian13:nonroot@sha256:cb87acc574abe719b6e5c547384defa2f7b1f24af9605d3f618cf852495d8be2

FROM ${RUNTIME_IMAGE}
ARG TEMPLATE_VERSION

# The binary divbast/frankenphp builds, by digest. CI refuses it unless that
# repository's own build on main attested it.
COPY --from=ghcr.io/divbast/frankenphp:baikal@sha256:23c042d9259caa052bc9a0131f58f75a15f73602fd182a2546b6759dc1e0d1db --chmod=0755 /frankenphp /usr/local/bin/frankenphp

# Root-owned, and never written: everything that changes lives on /tmp. No
# --chmod on these, because COPY gives a directory it creates the file's
# mode, and then no one could enter it; they keep the checkout's modes.
# The page, the one script :8080 runs
COPY --chown=root:root public/index.php /var/www/template/index.php
# /healthz, which the Caddyfile serves on :8081 alone
COPY --chown=root:root rootfs/usr/local/share/template-health/index.php /usr/local/share/template-health/index.php
COPY --chown=root:root caddy/Caddyfile /etc/frankenphp/Caddyfile
# The binary scans /etc/frankenphp/php.d, so this applies to the server and to
# every php-cli run alike
COPY --chown=root:root php/template.ini /etc/frankenphp/php.d/template.ini
COPY --chown=root:root --chmod=0755 rootfs/usr/local/bin/template-health /usr/local/bin/template-health

# Caddy keeps its state and the configuration it autosaves under these
ENV XDG_DATA_HOME=/tmp \
    XDG_CONFIG_HOME=/tmp

LABEL org.opencontainers.image.source="https://github.com/divbast/oci-image-template" \
    org.opencontainers.image.url="https://github.com/divbast/oci-image-template" \
    org.opencontainers.image.title="oci-image-template" \
    org.opencontainers.image.version="${TEMPLATE_VERSION}" \
    org.opencontainers.image.licenses="BSD-2-Clause"

EXPOSE 8080 8081
# Exec form: the shell form would need /bin/sh, which the image lacks.
# --timeout must clear template-health's own budget: it allows each of its
# two exchanges 4 s and runs them one after the other, so a listener that
# accepts and never answers costs 8 s. A shorter timeout kills the probe
# mid-flight and records a bare engine timeout instead of the probe's own
# line, and with no shell and no access log on :8081 that line is the only
# diagnostic there is. Podman reads no HEALTHCHECK from an image published
# as an OCI index: its users pass the same array as --health-cmd.
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/frankenphp", "php-cli", "/usr/local/bin/template-health"]
USER 65532:65532
# The server itself, as PID 1: the image has no state to prepare and no
# environment to read, so nothing runs before it
ENTRYPOINT ["/usr/local/bin/frankenphp", "run", "--config", "/etc/frankenphp/Caddyfile"]
