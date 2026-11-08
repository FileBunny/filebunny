# syntax=docker/dockerfile:1
#
# Targets:
#   backend  the Go API (distroless, non-root)
#   web      Caddy with the built frontend
#
# Base images are pinned by digest. The Go version must match backend/go.mod;
# GOTOOLCHAIN=local makes the build fail instead of downloading another one.

# Built on the build machine's platform and cross-compiled for the target
# (the same binary as a native build), so multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-trixie@sha256:c124b9ad3452f46061b5054607d26ea51730dae5372c5e469920063ba2bc6dcc AS backend-build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Same flags as `make backend`, so the binary is bit-for-bit identical.
ARG VERSION=dev
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X github.com/FileBunny/filebunny/backend/internal/server.Version=${VERSION}" -o /out/filebunny ./cmd/filebunny \
 && mkdir /out/data

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329 AS backend
COPY --from=backend-build /out/filebunny /filebunny
COPY --from=backend-build --chown=nonroot:nonroot /out/data /data
ENV FILEBUNNY_LISTEN=:8080 FILEBUNNY_DATA_DIR=/data
USER nonroot
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/filebunny"]

FROM --platform=$BUILDPLATFORM node:26-trixie-slim@sha256:5adad64f9a1fca2d51972caa65983634c8a774eb300d72b20f7f2a0f60368072 AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json frontend/.npmrc ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
# Contact addresses shown on the contact page (src/lib/contact.ts).
ARG VITE_FILEBUNNY_CONTACT=""
ARG VITE_FILEBUNNY_ABUSE=""
ARG VITE_FILEBUNNY_SECURITY=""
ARG VERSION=dev
ENV VITE_FILEBUNNY_VERSION=${VERSION}
RUN npm run build
# Caddy's data and config directories (/data/caddy, /config/caddy), owned
# by its unprivileged user in the web image; a new volume takes over that
# owner. The base image's are world-writable (1777).
RUN mkdir -p /caddy/data/caddy /caddy/config/caddy

FROM caddy:2.11.7-alpine@sha256:e70b164da81ef698b3ad19af799ea6086ce6d9e48ed4079eb9fe26f6fbf14ee5 AS web
# Fixed modes: COPY keeps the checkout's, which depend on the umask of
# whoever cloned the repository.
COPY --chmod=0644 deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=frontend-build /src/dist /srv
COPY --chmod=u=rwX,go=rX deploy/well-known/ /srv/.well-known/
# Caddy runs as an unprivileged user without capabilities; compose.yaml
# lets the container bind ports from 80 up (net.ipv4.ip_unprivileged_port_start).
# The image's binary carries the file capability cap_net_bind_service,
# which the kernel refuses to exec without it: remove it.
RUN setcap -r /usr/bin/caddy
COPY --from=frontend-build --chown=65532:65532 --chmod=0700 /caddy/data /data
COPY --from=frontend-build --chown=65532:65532 --chmod=0700 /caddy/config /config
USER 65532:65532
