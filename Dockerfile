# syntax=docker/dockerfile:1
#
# Targets:
#   backend  the Go API (distroless, non-root)
#   web      Caddy with the built frontend
#
# Base images are pinned by digest. The Go version must match backend/go.mod;
# GOTOOLCHAIN=local makes the build fail instead of downloading another one.

FROM golang:1.27.3-trixie@sha256:c124b9ad3452f46061b5054607d26ea51730dae5372c5e469920063ba2bc6dcc AS backend-build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Same flags as `make backend`, so the binary is bit-for-bit identical.
RUN go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o /out/filebunny ./cmd/filebunny \
 && mkdir /out/data

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS backend
COPY --from=backend-build /out/filebunny /filebunny
COPY --from=backend-build --chown=nonroot:nonroot /out/data /data
ENV FILEBUNNY_LISTEN=:8080 FILEBUNNY_DATA_DIR=/data
USER nonroot
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/filebunny"]

FROM node:24-trixie-slim@sha256:43b9ba1142b8dcf33fa8f922ca1be85714adc444e0586c7cf7726daaeabc1bea AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json frontend/.npmrc ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

FROM caddy:2.11.7-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f AS web
COPY deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=frontend-build /src/dist /srv
