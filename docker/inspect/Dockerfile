# renovate: datasource=github-releases depName=containernetworking/plugins
ARG CNI_VERSION=v1.9.1
ARG CNI_SHA256_AMD64=b98f74a0f8522f0a83867178729c1aa70f2158f90c45a2ca8fa791db1c76b303
ARG CNI_SHA256_ARM64=56171987d3947707c3563db2f4001bccaf50fd63468611b9f3cbecb1375ee7ec

# renovate: datasource=github-releases depName=coredns/coredns
ARG COREDNS_VERSION=1.14.7
ARG COREDNS_SHA256_AMD64=9a09356438c6d9591cb80011a2a7a52f87fc3a113f6690e41f243f98b98faac9
ARG COREDNS_SHA256_ARM64=bf26782d19b5340e3e6cb50f1ed6df72f58e4a30932c79d167f0f0a8b1e92e16

# Prepare dependencies
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS deps
ARG CNI_VERSION
ARG CNI_SHA256_AMD64
ARG CNI_SHA256_ARM64
ARG COREDNS_VERSION
ARG COREDNS_SHA256_AMD64
ARG COREDNS_SHA256_ARM64
RUN apk add --no-cache curl && \
    ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/') && \
    mkdir -p /opt/cni/bin && \
    CNI_SHA=$([ "$ARCH" = "amd64" ] && echo "$CNI_SHA256_AMD64" || echo "$CNI_SHA256_ARM64") && \
    curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz" \
      -o /tmp/cni.tgz && \
    echo "${CNI_SHA}  /tmp/cni.tgz" | sha256sum -c && \
    tar -C /opt/cni/bin -xz -f /tmp/cni.tgz ./bridge ./host-local ./loopback && \
    COREDNS_SHA=$([ "$ARCH" = "amd64" ] && echo "$COREDNS_SHA256_AMD64" || echo "$COREDNS_SHA256_ARM64") && \
    curl -fsSL "https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${ARCH}.tgz" \
      -o /tmp/coredns.tgz && \
    echo "${COREDNS_SHA}  /tmp/coredns.tgz" | sha256sum -c && \
    tar -C /usr/local/bin -xz -f /tmp/coredns.tgz coredns

# Build the runc wrapper (statically linked). --platform=$BUILDPLATFORM keeps
# the Go toolchain on the build host's own architecture; GOOS/GOARCH then
# cross-compile for the target.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS runc-wrapper-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY docker/inspect/buildcage-runc/ ./
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w" -o /out/buildcage-runc .

# Bundle the Docker-image scripts (rolldown; see rolldown.scripts.config.js):
# the QuickJS config generator and report-action.node.ts. Architecture-
# independent output, so pinned to --platform=$BUILDPLATFORM like the stages
# above (no QEMU).
FROM --platform=$BUILDPLATFORM ghcr.io/pnpm/pnpm:11@sha256:e26f380828856f205feaaf42abd157df9ce41bc8e17b662eeaa3379a9638dee0 AS scripts-build
WORKDIR /build
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY rolldown.scripts.config.js ./
COPY src/core/ ./src/core/
COPY docker/lib/ ./docker/lib/
COPY docker/inspect/scripts/ ./docker/inspect/scripts/
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    pnpm run build:scripts

# Final image
FROM moby/buildkit:v0.32.2@sha256:28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8

LABEL org.opencontainers.image.title="buildcage (inspect proxy engine)" \
      org.opencontainers.image.description="Secure Docker build environment enforcing on method, URL and path" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0 AND GPL-2.0-or-later AND ISC" \
      org.opencontainers.image.source="https://github.com/buildcage/docker" \
      io.github.buildcage.report-source="true"
# report finds this container by this label — must match src/core/lib/docker/report-source.ts.

# haproxy carries what this engine needs in one binary: it tells a TLS
# handshake from a plain request by its first bytes, generates certificates for
# an SNI, resolves names itself, and normalises a path before matching it.
RUN apk add --no-cache \
      s6-overlay \
      haproxy \
      openssl \
      iptables \
      quickjs-ng \
      rsync

ENV S6_KEEP_ENV=1
ENV S6_LOGGING=0

COPY --from=deps /opt/cni/bin /opt/cni/bin
COPY --from=deps /usr/local/bin/coredns /usr/local/bin/coredns
COPY --from=runc-wrapper-build /out/buildcage-runc /opt/buildcage/bin/buildcage-runc

RUN mkdir -p /etc/buildkit /etc/coredns /etc/haproxy /var/log/haproxy \
      /var/log/coredns /var/log/buildcage /run/buildcage
COPY docker/inspect/files/buildkitd.toml /etc/buildkit/buildkitd.toml
COPY docker/inspect/files/cni.conflist   /etc/buildkit/cni.conflist

# Generated at startup from the rule inputs, both by the same run.
COPY --from=scripts-build /build/dist/qjs/docker/inspect/scripts/gen-configs.js /opt/buildcage/scripts/gen-configs.js

# report-action.js — the runner-side report generator `docker cp`'d out by
# the report action itself (never staged elsewhere); see its own header
# comment for details.
COPY --from=scripts-build /build/dist/report-action/docker/inspect/scripts/report-action.js /opt/buildcage/scripts/report-action.js

# License
COPY docker/inspect/files/THIRD_PARTY_LICENSES /opt/buildcage/THIRD_PARTY_LICENSES

COPY docker/inspect/files/s6-rc.d/    /etc/s6-overlay/s6-rc.d/
COPY docker/inspect/files/s6-scripts/ /etc/s6-overlay/scripts/

# Test-only. The integration fixtures serve a self-signed certificate no public
# store contains, so the proxy has to be told to trust it when it verifies the
# origin. The marker exists only in an image built with this arg set, so a
# released proxy has no way to be told the same thing: the check in
# init-inspect-cfg finds nothing to act on. Never set for a real build.
ARG BUILDCAGE_TEST_HOOKS=""
RUN if [ -n "$BUILDCAGE_TEST_HOOKS" ]; then touch /opt/buildcage/.test-hooks; fi

ENTRYPOINT ["/init"]
