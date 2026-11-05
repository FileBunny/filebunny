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

FROM --platform=$BUILDPLATFORM node:24-trixie-slim@sha256:43b9ba1142b8dcf33fa8f922ca1be85714adc444e0586c7cf7726daaeabc1bea AS frontend-build
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

FROM caddy:2.11.7-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f AS web
# Fixed modes: COPY keeps the checkout's, which depend on the umask of
# whoever cloned the repository.
COPY --chmod=0644 deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=frontend-build /src/dist /srv
COPY --chmod=u=rwX,go=rX deploy/well-known/ /srv/.well-known/
