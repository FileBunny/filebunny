# FileBunny

Encrypt in the browser with OpenPGP, share anonymously. Public service on
filebunny.eu, AGPL-3.0, self-hostable. The server only ever handles
ciphertext.

## Non-negotiables

- **The server never sees plaintext, keys or passwords.** All crypto happens
  in the browser (OpenPGP.js). Never add server-side decryption, key handling
  or content inspection.
- **No client IP addresses anywhere**: not in app logs, not in the database,
  not in Caddy logs. Never log `r.RemoteAddr`, forwarding headers, file IDs
  or tokens. Always log through `internal/logging` (redacts addresses as a
  safety net, not as permission). Caddy strips
  `X-Forwarded-For`/`X-Real-IP`/`Forwarded`, has no access log (don't add
  one) and filters request details from its error log.
  `TestNoClientAddressInLogs` and `scripts/smoke-test.sh` must keep passing;
  extend them for new endpoints. Rate limiting (phase 7) will use a hashed IP with a salt
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
- **Never render user or decrypted content as HTML.** No `{@html}`,
  `innerHTML` or `eval` in the frontend.
- **Every new resource limit needs a cap.** Anonymous clients must not be
  able to exhaust disk, memory, connections or time (see the quota,
  `MaxBytesReader` and stall timeouts in `server.go`).
- **Outgoing requests (later phases) need SSRF protection**; see
  `docs/security-review.md` before adding any.
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
backend/internal/server   HTTP handlers, quota, stall timeouts, security headers
backend/internal/logging  slog logger that redacts IP addresses
backend/internal/cleanup  periodic removal of expired files
frontend/                 Vite + Svelte app
deploy/Caddyfile          TLS, CSP, /api proxy, log filter
scripts/repro-check.sh    builds twice, compares hashes
scripts/smoke-test.sh     end-to-end test of the Compose stack, incl. log privacy
docs/                     architecture, API, self-hosting, security review
```

## Build and test

```sh
make test       # gofmt, go vet, go test -race, svelte-check
make vuln       # govulncheck
make repro      # reproducibility check (backend + frontend)
make build      # bin/filebunny + frontend/dist
docker compose up -d --build && scripts/smoke-test.sh   # end to end
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

## Documentation

Keep these up to date in the same commit as the change:

| File | Contents |
|---|---|
| `docs/api.md` | Every endpoint, parameter and status code |
| `docs/architecture.md` | Components, request flow, what is stored, threat model |
| `docs/self-hosting.md` | Install, all `FILEBUNNY_*` variables, privacy checklist |
| `docs/security-review.md` | Findings, residual risks, requirements for later phases |
| `SECURITY.md` | How to report vulnerabilities |

The size limit applies to ciphertext: 100 MiB leaves room for the OpenPGP
overhead on a 100 MB file. Only the SHA-256 of the delete token is stored.

## Conventions

- **Everything public is in English**: code, comments, identifiers, docs,
  commit messages, branch names (e.g. `phase-2`), PR and issue text, and
  the UI. Dutch comes later as a UI translation only (phase 9).
- Go: `gofmt`, standard library first, errors wrapped with context, no
  panics in handlers. Tests use `httptest` and `t.TempDir()`; inject the
  clock (`Server.now`) instead of sleeping, or use `testing/synctest`.
- Security fixes get a regression test (see `server/security_test.go`).
- Handlers return JSON errors as `{"error": "..."}` and never echo internal
  error details to the client.
- Schema changes: append to `migrations` in `store.go`; never edit an
  existing migration.
- Small commits, one logical change each.
- Secrets (signing keys, SMTP passwords) never go into the repo.

## Roadmap

Phase 1 (foundation) is this code. Next: phase 2, browser encryption,
share links with download limits, chunked upload for large files.
