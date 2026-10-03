# syntax=docker/dockerfile:1

# ---------------------------------------------------------------- build stage
# The binary is cross-compiled on the build host, so a single `docker buildx
# build --platform linux/amd64,linux/arm64` produces both architectures without
# emulation. The project has no third-party dependencies, so there is no module
# download step and the build works offline.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src

COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
# The Markdown docs are embedded into the binary and served at /docs.
COPY docs/ ./docs/

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags netgo,osusergo \
        -ldflags "-s -w \
            -X github.com/wuzhengmao/updatesite/internal/buildinfo.Version=$VERSION \
            -X github.com/wuzhengmao/updatesite/internal/buildinfo.Commit=$COMMIT \
            -X github.com/wuzhengmao/updatesite/internal/buildinfo.Date=$BUILD_DATE" \
        -o /out/updatesite ./cmd/updatesite

# The checksum cache directory ships with the image so that a named volume
# mounted over it inherits the right ownership.
RUN mkdir -p /out/cache

# ---------------------------------------------------------------- final image
# scratch keeps the image around 14 MB and removes any shell or package manager
# from the attack surface.
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# Installed on PATH so the administration commands read the same inside and
# outside the container: docker exec <container> updatesite token <app-id>
COPY --from=build /out/updatesite /usr/local/bin/updatesite
COPY --from=build /out/cache /var/cache/updatesite

# PATH is declared explicitly: scratch images have nothing on it otherwise, and
# docker exec does not search beyond it.
# ADDR is the plain HTTP port; HTTPS on TLS_ADDR starts automatically once
# TLS_CERT and TLS_KEY point at a certificate and its key.
ENV PATH=/usr/local/bin:/usr/bin:/bin \
    DATA_DIR=/data \
    CACHE_DIR=/var/cache/updatesite \
    ADDR=:80 \
    TLS_ADDR=:443 \
    SCAN_INTERVAL=15s

EXPOSE 80 443
VOLUME ["/data"]

# The image has no shell or curl, so the binary probes itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/updatesite", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/updatesite"]
