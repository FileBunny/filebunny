# Security review — phase 1

| | |
|---|---|
| Date | 2026-10-27 |
| Scope | The phase 1 foundation (`d83a815`, merged in #1): backend, Caddy, Docker Compose, CI, frontend skeleton. Fixes in `1436999`, `67db56b`, `3164b47` |
| Method | Manual code review per category, targeted tests, end-to-end checks against a locally running Caddy + backend, `govulncheck`, `npm audit` |
| Reviewer | Claude Code (AI-assisted). Not a substitute for the independent review planned in phase 10 |

## Summary

No SQL injection, XSS, command injection, SSRF or IDOR was found. The
review found two privacy leaks of client IP addresses (one in the backend's
logs, one in Caddy's error log), a disk-exhaustion risk, a slow-client
denial of service and a few smaller logic issues. All were fixed in this
branch and are covered by tests or by `scripts/smoke-test.sh`.

### Fixed

| # | Severity | Finding | Fix |
|---|---|---|---|
| 1 | High (privacy) | **Caddy's error log contained client IPs.** On a proxy error (backend down, timeout) Caddy logs the full request: `remote_ip`, `client_ip`, all headers (including `Authorization` with the delete token) and the URI (with the file ID). Reproduced locally. | Log filter in `deploy/Caddyfile` removes these fields. Verified with a real Caddy; checked in CI by the smoke test. |
| 2 | High (privacy) | **Backend logs could contain client IPs.** Network errors such as `read tcp 10.0.0.2:8080->203.0.113.7:51234: i/o timeout` were logged as-is. Behind Caddy this is Caddy's address, but when the backend is exposed directly it is the client's. | `internal/logging` redacts IP addresses from every log message and attribute. Client aborts are no longer logged at all. |
| 3 | High (availability) | **Disk exhaustion.** Anyone could upload 100 MiB files without limit until the disk was full. | Storage quota `FILEBUNNY_MAX_TOTAL_SIZE` (default 20 GiB) with reservations for uploads in progress, so parallel uploads cannot overshoot it. Returns `507`. |
| 4 | Medium (availability) | **Slow clients (slowloris).** No read or write timeout on bodies: a client sending one byte per minute held a connection and a temp file forever. | Stall timeout: each read/write must make progress within 1 minute. `MaxHeaderBytes` lowered to 64 KiB. |
| 5 | Low (logic) | `expires=1ns` was accepted and produced a file that expired immediately; the error message claimed a 1 s minimum. | Minimum expiry 1 minute (`config.MinExpiry`), also enforced for `FILEBUNNY_DEFAULT_EXPIRY`. |
| 6 | Low (integrity) | On an ID collision, `Put` renamed the new blob over the existing one before the database insert failed. Probability 2⁻¹²⁸ per upload, but the order was wrong. | Insert the row first (the primary key guarantees a free ID), then rename. |
| 7 | Low (logic) | `DELETE` on an expired file that cleanup had not removed yet returned `204`/`403` while `GET` returned `404`. | Delete treats expired files as not found. |
| 8 | Low (headers) | Caddy's frontend CSP overwrote the backend's stricter API CSP. | CSP scoped to the frontend in Caddy; API CSP now also has `sandbox`. |
| 9 | Low (CI) | `actions/checkout` kept the GitHub token in `.git/config` for later steps. | `persist-credentials: false`. |
| 10 | Low (errors) | A failed or stalled upload body returned `500` (server error) or an empty `200`. | Returns `400 upload aborted`; real server errors stay `500`. |

## Per category

### SQL injection — no findings
All queries use `?` placeholders (`internal/store/store.go`). The only
formatted SQL is `PRAGMA user_version = %d` with an integer from the
migration loop. IDs are validated against `^[A-Za-z0-9_-]{22}$` before any
query. Test: `TestInjectionInIDIsHarmless`.

### Cross-site scripting (XSS) — no findings
- The API only returns JSON (`application/json`) and downloads
  (`application/octet-stream`, `Content-Disposition: attachment`), with
  `nosniff` and `CSP: default-src 'none'; sandbox`. User input is never
  reflected in responses.
- The frontend has no user content yet. Svelte escapes output by default;
  there is no `{@html}`, `innerHTML` or `eval`. The CSP allows only
  same-origin scripts and styles, no inline code.
- **Phase 2 rule:** decrypted content (text, file names) must never be
  rendered as HTML. Never use `{@html}`. Offer decrypted files as downloads,
  not as `blob:` URLs opened in a tab.

### Broken authentication — no findings
There are no accounts. The only credential is the per-file delete token:
256 bits from `crypto/rand`, stored as SHA-256, compared in constant time,
sent as `Authorization: Bearer` (not in the URL, so not in logs or referrers;
Caddy's error log drops headers). Brute force is infeasible. Test:
`TestDelete`, `TestDeleteTokenIsBoundToFile`.

### Insecure direct object references (IDOR) — no findings
Access follows the capability model: whoever has the 128-bit random ID can
download that ciphertext; whoever has the delete token can delete it.
IDs are not enumerable (no listing endpoint, no sequential IDs) and a
token is bound to its own file. The ciphertext is useless without the
password or private key, which the server never sees.

### Server-side request forgery (SSRF) — no findings (not applicable yet)
The backend makes no outgoing requests (`grep` for `http.Client`,
`net.Dial`: none). This changes in later phases; see *Requirements for
later phases*.

### Command injection — no findings
The backend runs no external commands (no `os/exec`). Nothing from the
client becomes a file path: blob names are server-generated IDs.
`scripts/*.sh` take no external input.

### Privilege escalation — no findings, one residual risk
- Backend: distroless image, user `nonroot`, read-only root filesystem,
  `cap_drop: ALL`, `no-new-privileges`, not published on the host
  (only reachable through Caddy). Data directory `0700`, files `0600`.
- Caddy: `cap_drop: ALL` except `NET_BIND_SERVICE`, `no-new-privileges`,
  read-only root filesystem, admin API off.
- **Residual:** the official Caddy image runs as root inside the container.
  With all capabilities dropped this is limited; running Caddy as a
  non-root user is a possible follow-up.

### API security risks — fixed (#3, #4, #5, #10)
- Size limit enforced while streaming (`MaxBytesReader`), not only via
  `Content-Length`. Caddy caps bodies at 110 MiB as a first line.
- Storage quota and stall timeouts (see Fixed).
- Unknown routes `404`, wrong methods `405`, errors as JSON without
  internal details.
- No CORS headers: other origins cannot read API responses. There are no
  cookies, so cross-site request forgery has nothing to abuse.
- **Open:** no rate limiting yet. One client can still use the whole
  quota. Planned for phase 7 (hashed IP with a daily salt, proof of work).

### Misconfigurations — fixed (#1, #8, #9)
- Caddy: no access log, error log filtered, HSTS, CSP, `-Server`,
  forwarding headers stripped before the backend. Verified end to end.
- Docker: base images pinned by digest, `npm ci --ignore-scripts`.
- CI: `permissions: contents: read`, actions pinned by SHA, no
  `pull_request_target`, no persisted credentials.
- **Open:** HSTS has no `includeSubDomains`/`preload` yet; decide once all
  subdomains (mail, status) are known.

### Business logic — fixed (#5, #6, #7)
Expired files are unreachable even before cleanup (`GET`, `DELETE`).
Orphans are only removed at startup, before uploads are accepted, so
cleanup never races an upload in progress. Downloads that are running when
a file is deleted or expires finish; this is accepted behaviour.

### Multi-tenant leakage — no findings
There are no tenants or accounts; every upload is isolated by its random
ID. Nothing is shared between uploads: no listing, no search, no caching
(`Cache-Control: no-store`), no shared state in responses. Logs contain no
IDs, tokens or addresses.

## Requirements for later phases

| Phase | Risk | Requirement |
|---|---|---|
| 2 | XSS through decrypted content | No `{@html}`; render text as text; file names only as text and in `download` attributes |
| 2 | Download counting / one-time download | Decide the counter atomically in the database (no read-then-write); define what a `HEAD` or an aborted download counts as |
| 3 | SSRF in the key-lookup proxy (WKD fallback) | Only build WKD URLs from a validated e-mail domain; resolve DNS and reject private, loopback, link-local and metadata addresses (also after redirects); no redirects to other hosts; short timeouts; response size limit; no logging of looked-up addresses |
| 4 | Upload-link management link | Same model as the delete token: long random secret, stored hashed, never in logs |
| 5 | SSRF and data leaks through proxy adapters | Fixed list of adapter hosts; no URLs from clients; health-check data must not contain user content |
| 6 | Mail abuse | Fixed templates, rate limits per recipient/sender/global, block link |
| 7 | Abuse of the whole quota by one client | Rate limiting with a daily rotating salt, proof of work |

## How to re-run the checks

```sh
make test     # unit and security regression tests (incl. log privacy)
make vuln     # govulncheck
cd frontend && npm audit
docker compose up -d --build && scripts/smoke-test.sh   # end to end
```
