# FileBunny

Encrypt in the browser with OpenPGP, share anonymously. Public service on
filebunny.eu, AGPL-3.0, self-hostable. The server only ever handles
ciphertext.

## Non-negotiables

- **The server never sees plaintext, keys or passwords.** All crypto happens
  in the browser (OpenPGP.js). Never add server-side decryption, key handling
  or content inspection.
- **No client IP addresses anywhere**: not in app logs, not in the database,
  not in Caddy logs. Never log `r.RemoteAddr` or forwarding headers. Caddy
  strips `X-Forwarded-For`/`X-Real-IP`/`Forwarded` and has no access log;
  don't add one. `TestNoClientAddressInLogs` must keep passing; extend it for
  new endpoints. Rate limiting (phase 7) will use a hashed IP with a salt
  that rotates daily, never the raw address.
- **Nothing from the client becomes a path.** Blobs are named by a
  server-generated random ID (128 bits, base64url). Validate IDs with
  `idPattern` before any lookup.
- **Standard OpenPGP only**, decryptable with `gpg`. No custom formats.
- **No external scripts, CDNs, fonts or trackers.** The frontend must work
  under the strict CSP in `deploy/Caddyfile` (no inline scripts or styles).
- **Reproducible builds.** Don't add anything that makes output depend on
  the build path, time or host (timestamps, `-buildvcs`, non-deterministic
  file names). `make repro` must pass.
- **Minimal dependencies.** Backend: standard library plus
  `modernc.org/sqlite` (pure Go, no cgo). Ask before adding any dependency,
  in either part.

## Stack

| Part | Choice |
|---|---|
| Backend | Go (version pinned in `backend/go.mod`), `net/http` only, `log/slog` |
| Storage | SQLite (metadata) + ciphertext blobs on disk in `FILEBUNNY_DATA_DIR` |
| Frontend | TypeScript + Vite + Svelte 5 (no SvelteKit), exact versions, lockfile committed |
| Deploy | Docker Compose: `backend` (distroless, non-root) + `web` (Caddy with the built frontend) |
| CI | GitHub Actions, actions pinned by commit SHA |

## Layout

```
backend/cmd/filebunny     main: config, startup cleanup, server, shutdown
backend/internal/config   FILEBUNNY_* environment variables
backend/internal/store    blobs + SQLite; migrations as a list, tracked by PRAGMA user_version
backend/internal/server   HTTP handlers, security headers, IP redaction
backend/internal/cleanup  periodic removal of expired files
frontend/                 Vite + Svelte app
deploy/Caddyfile          TLS, CSP, /api proxy
scripts/repro-check.sh    builds twice, compares hashes
```

## Build and test

```sh
make test       # gofmt, go vet, go test -race, svelte-check
make vuln       # govulncheck
make repro      # reproducibility check (backend + frontend)
make build      # bin/filebunny + frontend/dist
make run        # backend on :8080, data in ./data
make dev        # Vite dev server, proxies /api to :8080
docker compose up --build    # full stack on https://localhost
```

The Go toolchain is downloaded automatically from the version in `go.mod`
(`GOTOOLCHAIN=auto`). CI uses `GOTOOLCHAIN=local` with exactly that version.
When upgrading Go, update `backend/go.mod` and the `golang` image (tag and
digest) in `Dockerfile` together.

Backend build flags (keep `Makefile`, `Dockerfile` and
`scripts/repro-check.sh` in sync):
`CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -buildid="`

## API (v1)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/v1/files?expires=24h` | Raw ciphertext body. Returns `id`, `delete_token`, `size`, `expires_at`. 413 above `FILEBUNNY_MAX_SIZE` |
| `GET` | `/api/v1/files/{id}` | Streams the blob. 404 when unknown or expired |
| `DELETE` | `/api/v1/files/{id}` | `Authorization: Bearer <delete_token>` |
| `GET` | `/healthz` | Not exposed through Caddy |

Only the SHA-256 of the delete token is stored. The size limit applies to
ciphertext: 100 MiB leaves room for the OpenPGP overhead on a 100 MB file.

## Configuration

`FILEBUNNY_LISTEN` (`:8080`), `FILEBUNNY_DATA_DIR` (`./data`),
`FILEBUNNY_MAX_SIZE` (bytes, `104857600`), `FILEBUNNY_DEFAULT_EXPIRY` (`24h`),
`FILEBUNNY_MAX_EXPIRY` (`168h`), `FILEBUNNY_CLEANUP_INTERVAL` (`1m`).
Compose: `FILEBUNNY_DOMAIN` (`localhost`).

## Conventions

- Code, comments, commit messages and identifiers in English; the UI is
  Dutch and English (phase 9).
- Go: `gofmt`, standard library first, errors wrapped with context, no
  panics in handlers. Tests use `httptest` and `t.TempDir()`; inject the
  clock (`Server.now`) instead of sleeping, or use `testing/synctest`.
- Handlers return JSON errors as `{"error": "..."}` and never echo internal
  error details to the client.
- Schema changes: append to `migrations` in `store.go`; never edit an
  existing migration.
- Small commits, one logical change each.
- Secrets (signing keys, SMTP passwords) never go into the repo.

## Roadmap

Phases come from the planning notes (Obsidian vault, `filebunny/Planning`).
Phase 1 (foundation) is this code. Next: phase 2, browser encryption,
share links with download limits, chunked upload for large files.
