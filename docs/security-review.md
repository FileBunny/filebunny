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
| 2 | XSS through decrypted content | Done in phase 2, see below |
| 2 | Download counting / one-time download | Done in phase 2, see below |
| 3 | SSRF in the key-lookup proxy (WKD fallback) | Done in phase 3, see below |
| 4 | Upload-link management link | Done in phase 4, see below |
| 5 | SSRF and data leaks through proxy adapters | Done in phase 5, see below |
| 6 | Mail abuse | Fixed templates, rate limits per recipient/sender/global, block link |
| 7 | Abuse of the whole quota by one client | Rate limiting with a daily rotating salt, proof of work |

## Phase 2 (encryption and sharing)

| | |
|---|---|
| Date | 2026-10-27 |
| Scope | Branch `phase-2`: download limits, chunked uploads, CORS for `Origin: null`, browser crypto, send/download/delete pages, single HTML build |
| Reviewer | Claude Code (AI-assisted), during development |

### Requirements from phase 1

- **XSS through decrypted content:** decrypted text is rendered by Svelte
  as text in a `<pre>`; there is no `{@html}`, `innerHTML` or `eval`
  (checked with `grep`). File names only appear as text and in the
  `download` attribute, after `safeFileName` removes directories and
  control characters. Files are offered from a `blob:` URL of type
  `application/octet-stream`, never opened in a tab.
- **Download counting:** one `UPDATE … RETURNING` that only matches rows
  with downloads left, on a single SQLite connection; 20 parallel
  downloads of a one-time file give exactly one success
  (`TestDownloadLimitConcurrent`). `HEAD` does not count. A `GET` counts
  when it starts, also when aborted.

### New attack surface

| Area | Measure | Test |
|---|---|---|
| Chunked uploads (resource exhaustion) | Max 50 sessions server-wide, max upload size per session, max 16 MiB per chunk, `Content-Length` required, quota reserved per chunk, 2 minute idle timeout, 1 minute stall timeout per chunk, one request per session at a time | `uploads_test.go` |
| Upload tokens | 256 bits, compared as SHA-256 in constant time, in memory only, never logged; bound to their session | `TestUploadTokenIsBoundToUpload`, `TestNoClientAddressInLogs` |
| CORS | Only for `Origin: null`; no credentials; other origins get nothing | `TestCORSOnlyForNullOrigin`, smoke test |
| Metadata in the ciphertext | File name inside the encrypted literal packet, date 0, recipient key IDs hidden | `crypto.test.ts`, `gpg.test.ts` |
| Weak crypto choices | SEIPDv1 with MDC (integrity), AES-256, no compression; tampered messages are rejected | `crypto.test.ts` |
| Single HTML file | CSP by hash for its one script and stylesheet; `connect-src` only the API; build fails on extra files | Build, `make repro` |

### Residual risks

- **`Origin: null` is not only `file://`:** a sandboxed iframe on any
  website also has origin `null`, so another site can use the API from its
  visitors' browsers. The API has no cookies or ambient authority, so this
  gives nothing a server-side script could not do. Once rate limiting per
  (hashed) IP exists (phase 7), such a site could use up its visitors'
  limits; revisit then.
- **Aborted downloads count.** A one-time download that breaks off is
  gone. Counting at the end would let parallel downloads all succeed,
  which is worse. The download page warns before the last download.
- **Ciphertext size reveals the approximate plaintext size.** No padding.
- **The delete link is in the browser history** of whoever opens it (in
  the fragment, so never in server or proxy logs).
- **Private keys pasted into the download page** live in the page's memory
  until it is closed; browser extensions with page access can read them.
  The page never stores or uploads them.
- **The JavaScript is trusted on every visit.** The single HTML file
  reduces this for people who save it; signing and a transparency log
  follow in phase 8.

## Phase 3 (keys)

| | |
|---|---|
| Date | 2026-10-29 |
| Scope | Branch `phase-3`: key lookup on keys.openpgp.org from the browser, WKD proxy (`POST /api/v1/wkd`, `GET /api/v1/features`), local key upload, optional WKD check |
| Reviewer | Claude Code (AI-assisted), during development |

### Requirement from phase 1: SSRF in the WKD proxy

The only outgoing requests the backend makes. `internal/wkd`:

- **URLs only from a validated address:** one `@`, dot-atom local part
  (≤ 64), domain of ≥ 2 ASCII letter-digit-hyphen labels (≤ 253) with a
  non-numeric top-level label; no IP literals, ports, trailing dots,
  paths or non-ASCII (punycode only, no IDNA conversion). The local part
  goes into the URL only as a hash and as an escaped query value.
- **Dedicated client:** `Proxy: nil`, `https` only, every connection to
  port 443 regardless of the URL, TLS verification on (system roots from
  the distroless image), no redirects (`CheckRedirect` refuses them, so
  a redirect to an internal host is never requested), no keep-alives,
  no compression.
- **Address checks:** every address DNS returns is checked before
  dialing, and the dialer's `Control` function checks the address the
  socket actually connects to (covers DNS rebinding). Refused: everything
  that is not global unicast (loopback, link-local incl.
  169.254.169.254, multicast, unspecified, broadcast), private (RFC 1918,
  fc00::/7), CGNAT 100.64/10, 0/8, 192.0.0/24, documentation and
  benchmark ranges, 240/4, 6to4 relay, IPv4-mapped and -compatible IPv6,
  NAT64 (64:ff9b::/96, 64:ff9b:1::/48), 6to4 2002::/16, Teredo 2001::/32,
  site-local and discard.
- **Limits:** 5 s dial, 5 s TLS, 5 s to headers, 10 s per lookup in
  total, 1 MiB response, 16 KiB headers, request body 1 KiB, at most 8
  lookups server-wide (`503` beyond).
- **Fallback:** the direct method is only tried when the `openpgpkey.`
  host does not exist (NXDOMAIN), not after a 404 or another error.
- **No logging:** errors are wrapped so their text never contains the
  URL; the handler logs only a fixed category ("timeout", "tls",
  "blocked address", …).

### New attack surface

| Area | Measure | Test |
|---|---|---|
| SSRF via WKD | See above | `wkd_test.go`: `TestAllowed`, `TestLookupBlocksInternalAddresses`, `TestDialerControl`, `TestDialerControlChecksConnectedAddress`, redirect, size, timeout, TLS, fallback tests; `TestParseAddress` |
| Server as anonymous lookup relay | 8 lookups at a time, 10 s each; switch off with `FILEBUNNY_WKD_PROXY=false` | `TestWKDConcurrencyCap`, `TestWKDSwitchedOff` |
| Privacy of looked-up addresses | POST body, not URL; never logged; error responses do not echo input | `TestNoClientAddressInLogs` (address, domain, hash), `TestWKDErrors`, smoke test |
| Untrusted keys from keyservers or domains | Parsed in the browser only; keys need a valid user ID for exactly the address and must be able to encrypt; all armored blocks are read, so a second key cannot hide; conflicts are shown, never resolved automatically; user IDs rendered as text | `keylookup.test.ts`, `crypto.test.ts` |
| Browser requests to a keyserver | Only on click; `connect-src` adds exactly `https://keys.openpgp.org`; no cookies, referrer, cache or redirects; 10 s, 1 MiB read from the stream | `keylookup.test.ts`, smoke test (CSP), build (single HTML CSP) |

keyserver.ubuntu.com was left out: it sends no CORS headers on a 404
(checked 2026-10-29), so a browser cannot distinguish "not found" from a
failure, and proxying it would show the server every looked-up address.

### Residual risks

- **No per-client rate limit on WKD yet** (phase 7). One client can keep
  the 8 slots busy, so other users' WKD lookups get `503`; it cannot make
  the server open more than 8 connections at once or reach internal
  addresses. The server's own IP address is what the queried domains see.
- **The server sees looked-up addresses** when the user turns WKD on or
  runs a WKD check. They are not logged or stored, but a compromised
  server could record them. The UI says so; the option is off by default.
- **keys.openpgp.org sees the visitor's IP and the address** looked up.
  The UI says so before the lookup.
- **No IDNA:** internationalized domains must be entered in punycode.
- **A matching WKD key is not proof** of the recipient's identity, only
  that the domain publishes it. The UI asks users to compare fingerprints
  through another channel.
- **DNS answers come from the host's resolver**; a poisoned resolver can
  point a lookup at any public address (but not an internal one).

## Phase 4 (upload links, key pairs)

| | |
|---|---|
| Date | 2026-10-29 |
| Scope | Branch `phase-4`: upload links (`/api/v1/inboxes…`, chunked uploads with `?inbox=`), key pair generation, publishing on keys.openpgp.org, Receive / upload / management pages |
| Reviewer | Claude Code (AI-assisted), during development |

### Requirement from phase 1: the management link

The management token is 256 random bits, returned once, stored only as
SHA-256 and compared in constant time; it travels in the URL fragment
and in `Authorization` headers, never in a path or query, and is never
logged (`TestNoClientAddressInLogs` covers link IDs, tokens and file IDs;
so does the smoke test).

### New attack surface

| Area | Measure | Test |
|---|---|---|
| Key substitution by the server or database | The upload link carries the fingerprint; the upload page refuses a key that does not match it exactly (and a key that cannot encrypt) | Chromium walk-through (tampered fingerprint) |
| Reading someone else's received files | Files of a link are excluded from the public download, info and delete routes; listing, fetching and deleting need the management token of that link | `TestInboxFilesAreNotPublic`, `TestInboxManageAuth`, `TestInboxUploadAndManage`, smoke test |
| Exceeding a link's limits with parallel uploads | Upload and byte counters are updated in the same transaction as the file row, with the limits in the `WHERE` clause | `TestInboxUploadLimitConcurrent`, `TestInboxLimits` |
| Disk and quota exhaustion | Per link 100 MiB and ≤ 100 uploads; the global quota and the upload reservations still apply; chunked uploads to a link are capped at what it has left | `TestInboxLimits`, `TestInboxChunkedUpload` |
| Free storage through "public keys" | Key body ≤ 64 KiB, printable ASCII between public key armor lines, served only as JSON; at most 1000 open links server-wide (one atomic `INSERT … WHERE count <`) | `TestCreateInbox`, `TestInboxCap` |
| Content of keys and received files | Never rendered as HTML: user IDs and decrypted text as text, files as `blob:` downloads of type `application/octet-stream` | `grep` for `{@html}`/`innerHTML`, Chromium walk-through |
| Weak or unusable generated keys | v4 Curve25519 legacy (gpg 2.4 compatible), passphrase of at least 8 characters required, private key must be saved before use | `crypto.test.ts`, `gpg.test.ts` |
| Publishing on keys.openpgp.org | Directly from the browser (CORS allowed), only on click, only with an address in the key; keys.openpgp.org verifies the address by mail | `keylookup.test.ts` |

### Residual risks

- **The server knows the owner of an upload link** when the key has a
  name or e-mail address in its user IDs. The page says so and suggests
  a generated key without them.
- **A changed upload link** (other ID and fingerprint) points senders to
  another key; the fingerprint check cannot catch that. Links must reach
  senders through a trusted channel; comparing the fingerprint with the
  owner remains the real check.
- **No per-client rate limit** on creating links or uploading to them
  (phase 7). One client can use up the 1000 open links (others get
  `503` until they expire) or fill one link it knows.
- **Anyone with the upload link can fill it** up to its limits; the owner
  can revoke it. Notifications follow in phase 6.
- **The private key is in the page's memory** while unlocked on the
  management page, as on the download page; "Forget it" or closing the
  page removes it.
- **Lost private key or passphrase:** nothing sent to the link can be
  opened. The page requires saving the key and says there is no recovery.

## Phase 5 (external storage)

| | |
|---|---|
| Date | 2026-10-31 |
| Scope | Branch `phase-5`: `internal/storage` (Filebin adapter), `?storage=` on uploads, remote downloads, `remote_trash`, health checks, `GET /api/v1/storage`; frontend storage choice, direct route to Filebin, `#/x/filebin/…`, `#/status` |
| Reviewer | Claude Code (AI-assisted), during development |

### Requirement from phase 1: SSRF in outgoing requests

The Filebin client has its own `http.Client`: only `filebin.net` and
`storage.filebin.net` (fixed, not from the client or the service), HTTPS
on port 443, no proxy from the environment, every resolved address and
the connected address checked with `wkd.Allowed`, at most one redirect and
only to those hosts, time limits on dial, TLS and headers, limited header
size. Blob references are generated by the server; nothing from the
client goes into a URL. The verification cookie is kept for
`filebin.net` only.

### New attack surface

| Area | Measure | Test |
|---|---|---|
| SSRF through the service's redirects or DNS | Fixed hosts, address check at dial time, one redirect to a fixed host | `TestFilebinRedirectToOtherHostRefused`, `TestFilebinBlockedAddress` |
| A service that returns other or truncated data | Size checked against `Content-Length` and while streaming (`exactReader`); a web page instead of data counts as unavailable; OpenPGP's MDC catches changed content in the browser | `TestFilebinWrongSize`, `TestExactReader`, `TestFilebinVerificationPage` |
| Verification page | Followed exactly as the service's API documentation describes: one repeat with the cookie, the page itself never parsed; cookie never sent to the storage host | `TestFilebinVerificationCookie` |
| References or IDs in logs | Errors carry no URL (`unavailable` strips it); handlers log only the storage name | `TestFilebinErrorsHideTheReference`, `TestNoClientAddressInLogs` (with remote storage), smoke test |
| Exhausting the server through proxied traffic | 8 transfers per service, 15 minutes each, daily traffic cap; claimed before sending on and before a download is counted. An unavailable service is refused before any data is accepted | `TestRemoteDailyCap`, `TestRemoteRefusedBeforeData` |
| Blobs left at the service | Removal moves the reference to `remote_trash` in the same transaction; background deletion retries until the service confirms | `TestRemoteDeleteAndExpiry`, `TestRemoteDeleteAndExpiryAreDueAtOnce`, `TestRemoteLastDownloadWaitsForGrace` |
| Local quota bypass | Remote files do not count against the local quota; each upload still passes through a temp file under the same size limit and stall timeouts, removed after it was sent on | `TestTotalSizeCountsOnlyLocal`, `TestRemoteUploadDownload` |
| Direct route from the browser | Only on submit or click; `connect-src` adds exactly the two Filebin hosts; no cookies, no referrer; the bin name only in the fragment and checked against a strict pattern; a download larger than 100 MiB is refused | `filebin.test.ts`, `route.test.ts`, smoke test (CSP), build (single HTML CSP) |

### Residual risks

- **A download through FileBunny that fails at the service is counted**
  (`502`): with a one-time link the file is then gone. Unavoidable without
  fetching before counting, which would let concurrent downloads exceed
  the limit. Repeated failures take the service out of use (`503` before
  counting).
- **The service can read metadata and delete files:** size, times and
  this server's address through FileBunny; with the direct route also the
  visitors' addresses. Anyone who knows a bin name can delete it.
- **The direct route has no download limit, expiry or delete link**; the
  page says so before upload.
- **Dependence on the service's behaviour:** if Filebin changes its API or
  verification, the health check takes it out of use; the direct route
  falls back to opening the file in a tab.
- **No per-client limit on proxied traffic** yet (phase 7); one client
  can use up the daily cap for everyone, after which local storage still
  works.

## How to re-run the checks

```sh
make test     # unit and security regression tests (incl. log privacy)
make vuln     # govulncheck
cd frontend && npm audit
docker compose up -d --build && scripts/smoke-test.sh   # end to end
```
