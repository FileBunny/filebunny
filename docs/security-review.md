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
  non-root user is a possible follow-up. (Done in phase 10.)

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
  *Addressed in phase 7:* per-visitor limits and proof of work; one
  visitor gets at most 4 GiB a day.

### Misconfigurations — fixed (#1, #8, #9)
- Caddy: no access log, error log filtered, HSTS, CSP, `-Server`,
  forwarding headers stripped before the backend. Verified end to end.
- Docker: base images pinned by digest, `npm ci --ignore-scripts`.
- CI: `permissions: contents: read`, actions pinned by SHA, no
  `pull_request_target`, no persisted credentials.
- **Open:** HSTS has no `includeSubDomains`/`preload` yet; decide once all
  subdomains (mail, status) are known. (Phase 10: `includeSubDomains` on, `preload` after the public launch.)

### Business logic — fixed (#5, #6, #7)
Expired files are unreachable even before cleanup (`GET`, `DELETE`).
Orphans are only removed at startup, before uploads are accepted, so
cleanup never races an upload in progress. Downloads that are running when
a file is deleted or expires finish; this is accepted behavior.

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
  limits; revisit then. *Phase 7:* such a site would also have to solve
  the proof of work in its visitors' browsers; it could use up their
  limits, as any script they run could.
- **Aborted downloads count.** A one-time download that breaks off is
  gone. Counting at the end would let parallel downloads all succeed,
  which is worse. The download page warns before the last download.
- **Ciphertext size reveals the approximate plaintext size.** No padding.
- **The delete link is in the browser history** of whoever opens it (in
  the fragment, so never in server or proxy logs). *Phase 9:* the delete page takes the token
  out of the address bar at once; the browser's history list may still
  have the visit.
- **Private keys pasted into the download page** live in the page's memory
  until it is closed; browser extensions with page access can read them.
  The page never stores or uploads them.
- **The JavaScript is trusted on every visit.** The single HTML file
  reduces this for people who save it; signing and a transparency log
  follow in phase 8. *Phase 8:* signed releases in a public log and an
  external watcher; see docs/verify.md.

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
  *Phase 7:* 60 lookups per visitor per hour.
- **The server sees looked-up addresses** when the user turns WKD on or
  runs a WKD check. They are not logged or stored, but a compromised
  server could record them. The UI says so; the option is off by default.
- **keys.openpgp.org sees the visitor's IP and the address** looked up.
  The UI says so before the lookup.
- **No IDNA:** internationalized domains must be entered in punycode. *Phase 9:* the browser converts the domain to
  punycode for WKD and matches user IDs in either form.
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
  `503` until they expire) or fill one link it knows. *Phase 7:* 30 links
  per visitor per day, with proof of work; filling 1000 takes many
  addresses.
- **Anyone with the upload link can fill it** up to its limits; the owner
  can revoke it. There is no notification of new uploads (phase 6 mails
  share links only).
- **The private key is in the page's memory** while unlocked on the
  management page, as on the download page; "Forget it" or closing the
  page removes it.
- **Lost private key or passphrase:** nothing sent to the link can be
  opened. The page requires saving the key and says there is no recovery.

## Phase 5 (external storage)

| | |
|---|---|
| Date | 2026-10-31 |
| Scope | Branch `phase-5`: `internal/storage` (Filebin adapter), `?storage=` on uploads, remote downloads, `remote_trash`, health checks, `GET /api/v1/storage`, `?storage=` on upload links (through FileBunny only); frontend storage choice, direct route to Filebin, `#/x/filebin/…`, `#/status` |
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
- **Dependence on the service's behavior:** if Filebin changes its API or
  verification, the health check takes it out of use; the direct route
  falls back to opening the file in a tab.
- **No per-client limit on proxied traffic** yet (phase 7); one client
  can use up the daily cap for everyone, after which local storage still
  works. *Phase 7:* uploads count against the visitor's 4 GiB a day.

## Phase 6 (e-mail)

| | |
|---|---|
| Date | 2026-10-31 |
| Scope | Branch `phase-6`: `internal/mail` (SMTP client), `POST /api/v1/files/{id}/notify`, `/api/v1/mail/block/{token}`, mail limits and block list in SQLite, SMTP secret in Compose; frontend "E-mail the link" and `#/block/…` |
| Reviewer | Claude Code (AI-assisted), during development |

### Outgoing connections

Relay hosts come only from the operator's secret file, never from a
client, so the SSRF rules for client-chosen targets do not apply. Every
connection uses TLS with certificate verification (implicit TLS on 465,
otherwise STARTTLS must be offered); credentials are never sent in the
clear. Each delivery is bounded by a 30 s deadline.

### New attack surface

| Area | Measure | Test |
|---|---|---|
| Header or SMTP command injection through the address | Strict address check: plain ASCII, no spaces, line breaks, quotes, brackets, display names or IP literals, length limits; only used as `RCPT TO`/`To` | `TestValidAddress`, `TestNotifyRefusals` |
| Spam or phishing through the mail | Fixed text built by the server; the only link is the share link built from the file ID; no sender name, no free text, no password | `TestNotify` |
| Mailing someone else's link | Needs the delete token, which only the sender has | `TestNotifyRefusals` |
| Mail bombing an address | 3 addresses per link, 3 mails per address per day, 500 per day, per relay limits, 4 deliveries at a time; claimed in one transaction | `TestClaimMail`, `TestNotifyRefusals`, `TestNotifyRelays` |
| Addresses in storage or logs | Only an HMAC with a secret key is stored; errors carry the SMTP code only (relays echo addresses); relay accounts are never logged | `TestNoClientAddressInLogs` (with mail), `TestSendErrorsHideReplyText`, smoke test |
| Leaking relay passwords | Compose secret file outside the repository; not in images, environment or CI; config errors never quote the file | `TestLoadConfig` |
| Forged block requests | The token is the HMAC; without the key nobody can make one for an address. Blocking happens only on a click or a one-click POST | `TestMailBlock` |
| Untrusted relay certificate | Refused before any credentials are sent | `TestSendRefusesUntrustedCertificate` |

### Residual risks

- **The relay sees every recipient address and the link** and keeps its
  own logs. Choose relays whose terms and location you accept.
- **Mailed links can look like phishing**; recipients have no way to
  check the sender beyond what the sender tells them.
- **No per-sender limit** until phase 7 (hashed IP, proof of work): one
  client with many links can use up the daily limit for everyone; each
  address still gets at most 3 mails a day. *Phase 7:* 30 mails per
  visitor per day, each with proof of work.
- **A failed delivery still uses up its claim** against the limits.
- **A relay can replace the unsubscribe header** with its own (Mailjet
  does). An address that unsubscribes that way lands on the relay's
  suppression list, not on FileBunny's: the relay then accepts and drops
  further mail to it, and the sender still sees "Sent". The block link in
  the mail text keeps working.
- **New domains land in spam** at first (seen at Outlook with SPF, DKIM
  and DMARC all passing); the page asks senders to tell recipients to
  check their spam folder.
- **Losing `mail-pepper`** resets the counters and the block list.

## Phase 7 (abuse limits, pages)

| | |
|---|---|
| Date | 2026-11-02 |
| Scope | Branch `phase-7`: `internal/ratelimit`, `internal/pow`, `GET /api/v1/challenge`, proof of work and limits on uploads, upload links, mail, WKD and downloads; Caddy's `X-FileBunny-Client`; frontend SHA-256 and solver; about, terms, privacy and contact pages; `security.txt` |
| Reviewer | Claude Code (AI-assisted), during development |

### The address now reaches the backend

Until now the backend never saw a client address. To count per visitor,
Caddy passes it in one header, replacing any value the client sends. The
backend deletes the header before routing and keeps only an HMAC with a
random key that lives in memory and changes every UTC day; counters are
in memory only. Nothing about addresses is written to disk or logs.

### New attack surface

| Area | Measure | Test |
|---|---|---|
| Faking another visitor's address | Caddy replaces the header; the backend trusts it only when configured, and is reachable through Caddy only | `TestClientHeaderOnlyWhenConfigured`, smoke test (fake header sent) |
| Addresses in logs | The header is removed before handlers run; only hashes are kept | `TestNoClientAddressInLogs`, `TestRateLimits`, smoke test |
| Linking visitors over time | Hash key random per day and never stored; IPv6 by /64 | `TestClientHashing` |
| Memory exhaustion through the counters or used proofs | At most 200,000 visitors and 200,000 used proofs; idle visitors and expired proofs are dropped first, then `503` | `TestMaxClients`, `TestUsedIsCapped` |
| Replaying or forging proofs | HMAC over nonce, expiry, difficulty, action and visitor; single use; 10 minutes | `TestSolveAndVerify`, `TestVerifyRefuses`, `TestProofOfWork` |
| Lowering the difficulty | The difficulty is inside the signed token | `TestVerifyRefuses` |
| Rendering the contact addresses | Build-time values, checked as plain addresses, rendered as text | `npm run check` |

### Residual risks

- **Proof of work does not stop an attacker with many machines**; it
  makes cheap mass use expensive. The per-visitor limits stop one
  address; many addresses can still fill the server-wide limits.
- **Shared addresses** (companies, mobile carriers, Tor) share one
  visitor's limits.
- **A restart resets all counters** and invalidates open challenges.
- **No report button or takedown.** The operator cannot judge encrypted
  content, and a report that removes a file would let anyone break a
  one-time link. Reports reach the abuse address on the contact page;
  files expire within 7 days. Whether this meets the DSA's
  notice-and-action duty for hosting services, and what the hosting
  provider expects, is for the operator to check.
- **The pages are not reviewed by a lawyer**, and the operator is not
  named.

## Phase 8 (integrity)

| | |
|---|---|
| Date | 2026-11-04 |
| Scope | Branch `phase-8`: release workflow (images on GHCR, release files, build provenance attestations), `scripts/release-files.sh`, `scripts/verify-site.sh`, the hourly watcher, `GET /api/v1/version`, version in the page; docs/verify.md, docs/release.md |
| Reviewer | Claude Code (AI-assisted), during development |

### What a user can now check

- **Which code a site serves:** `verify-site.sh` compares every served
  frontend file with the release's `frontend-SHA256SUMS`, and checks that
  the page loads nothing else. The watcher does this every hour from
  GitHub and opens an issue on a mismatch.
- **Who built a release:** every file and image has a build provenance
  attestation, signed keyless through Sigstore by the release workflow
  (OIDC identity of this repository and workflow) and recorded in the
  public Rekor log. Forging one needs control of the repository's
  workflow, and leaves a public trace.
- **That the build is honest:** anyone can rebuild a release from its tag
  and get the same `SHA256SUMS` (`release-files.sh` builds from the same
  Dockerfile stages as the images; tested twice in a row, and the Docker
  binary equals `make backend`).

### New attack surface

| Area | Measure | Check |
|---|---|---|
| Release workflow credentials | Only this workflow gets `contents`, `packages`, `id-token` and `attestations` write; no stored secrets; actions pinned by SHA and limited to GitHub's and `docker/*` | Workflow file, repository Actions policy |
| A tag not reviewed through `main` | The workflow refuses tags whose commit is not on `main` | `Tag must be on main` step |
| A watcher that cries wolf or stays quiet | One open issue at a time; the run itself fails, so the Actions page shows every hour's result | `watch.yml` |
| Version string in the build | Taken from the tag, a build input; no time or host | `make repro`, `release-files.sh` run twice |

### Residual risks

- **The backend cannot be checked from outside.** Only what the server
  sends to browsers can be compared; the backend never sees plaintext or
  keys, so the browser code matters most.
- **Targeted attacks:** a server can serve the published files to the
  watcher and altered ones to one visitor. Saving the single HTML file
  avoids depending on the server's code.
- **Trust in GitHub** for the attestations and the watcher; rebuilding
  from source does not need it.
- **Rebuilding `v0.8.0-rc.1` locally** found that the checkout's file
  modes (umask) leaked into the web image and tarball. Fixed with fixed
  modes; `scripts/release-repro.sh` now builds the release files twice
  from copies with different modes and language, in CI on every change
  and in the release workflow before publishing. It cannot cover every
  machine; reports of differing rebuilds are asked for in docs/verify.md.
- **Image digests are not reproducible** (layer metadata); the promise
  covers the files in `SHA256SUMS`.
- **Scheduled workflows pause** after 60 days without repository
  activity.

## Phase 9 (website and UX)

| | |
|---|---|
| Date | 2026-11-07 |
| Scope | Branch `phase-9`: send page in steps, short-password warnings, reasons for unusable keys, algorithm overview, internationalized domains for WKD, the delete token out of the address bar, fingerprints as QR codes, accessibility fixes, the new look (light and dark) and clearer wording |
| Reviewer | Claude Code (AI-assisted), during development |

Frontend only: no backend, API, CSP or deploy changes.

### New attack surface

| Area | Measure | Check |
|---|---|---|
| New dependency `qrcode-generator` 2.0.4 | MIT, no dependencies of its own, pinned with its lockfile hash; it only gets a validated fingerprint (40 or 64 hex) and returns modules, drawn as one SVG path, so no HTML strings | `qr.ts`, `qr.test.ts`, `npm audit` |
| Internationalized domains (WKD) | The browser's URL parser converts the domain to punycode; the backend still validates the address strictly and keeps its SSRF protection | `keylookup.test.ts`, backend `wkd` tests |
| Hidden steps keep their fields | Only hidden with `hidden`, never sent elsewhere; nothing leaves the browser before "Encrypt and upload" | `Send.svelte` |
| New look | CSS and an inline SVG logo only; no fonts, images or other assets, no inline styles, so the strict CSP and `make single` are unchanged | `grep style=`, `make repro` |

### Residual risks

- **Password-protected files can be guessed offline** by anyone with the
  link; the iterated S2K slows guessing but does not stop it. Short
  passwords get a warning, not a block.
- **No AEAD, no Argon2:** kept for gpg 2.4 compatibility (see
  architecture.md, Algorithms).
- **Not quantum-resistant** in public-key mode: a large quantum computer
  could decrypt stored copies ("harvest now, decrypt later"). Password
  mode relies on AES-256 and the password. The About page says so.
- **The QR code shows the fingerprint, not the key.** A scan proves which
  fingerprint the page shows; whether it is the recipient's still needs
  comparing with them.
- **`npm audit` reports `deepmerge`** (GHSA-gcqx-w48m-w2h4, low, no fix
  yet) through `@sveltejs/vite-plugin-svelte`. Build time only: it merges
  this repository's own Vite config, never input from visitors, and is not
  in the built page (`npm audit --omit=dev`: 0). Update when a fix lands.

## Phase 10 (test and launch, in progress)

| | |
|---|---|
| Date | 2026-11-07 |
| Scope | Branch `phase-10`: `scripts/deploy.sh`, `compose.release.yaml`, `FILEBUNNY_TLS`, `FILEBUNNY_WWW_REDIRECT`, log caps, refusing a newer database schema |
| Reviewer | Claude Code (AI-assisted), during development |

### New attack surface

| Area | Measure | Check |
|---|---|---|
| Deploying images | By digest only; each digest checked with `gh attestation verify` against `release.yml`, the tag ref, the tag's commit and GitHub-hosted runners, so a moved tag, an older signed image or a CI build is refused | tested against v0.8.0/v0.9.0: wrong workflow and wrong tag fail |
| Deploy access | The server needs no GitHub token; whoever deploys is in the `docker` group (root in practice) | release.md |
| Rollback | The backend refuses a database schema newer than it knows, instead of writing rows without the newer columns | `TestNewerSchemaRefused` |
| Container logs | Capped at 3 × 10 MB per container, so they cannot fill the disk | `compose.yaml` |
| API relay (`mailjet-api`) | For providers that block outgoing SMTP. One fixed URL (`https://api.mailjet.com/v3.1/send`), no client input in the request but the checked recipient; no redirects, no proxy from the environment, TLS 1.2+ with a verified certificate, the response capped and only its status kept (it may quote the recipient); tracking off in every message; an API error never counts as a refused recipient. The monitor sends the same way, and the firewall lets its mail user reach 443 only for the relay's addresses | `TestMailjetAPIRequest`, `TestMailjetAPIErrors`, `TestMailjetAPINoRedirect`, `TestLoadConfigRelayTypes`; a fake key against the real API gives 401 |
| Relay contact cleanup | Once a day for API relays (`delete_contacts`, default on): lists the contacts (paged, response capped) and deletes those older than a day through `DELETE /v4/contacts/{id}`, at most 2000 per relay and day, a pause between requests, stop at 429 or after 5 failures; addresses that unsubscribed or complained at the relay go on the block list (HMAC) first. Same fixed host and client settings as the Send API; only counts are logged | `TestCleanupContacts*`, `TestMailjetContacts`, `TestDeleteContactsOnlyForAPIRelays` |
| Monitoring (`filebunny-monitor`) | Runs as root, reads only the server's own state and logs (which hold no visitor addresses), the daily totals (read-only) and crt.sh; mails through the relay as `filebunny-mail`, which the firewall lets reach only the relay's addresses for two minutes (an nftables set with timeout, `meta skuid`) and nothing else but DNS; TLS with certificate checks, never plain SMTP; the relay password goes to the sender on stdin, never on a command line | test VM: login reached the relay; other hosts, other users and HTTPS for that user blocked; alert, recovery and summary mails (dry run) |
| Go 1.27.4 | **Finding:** GO-2026-6859, a crash of the HTTP/1 server through a header race in `MaxBytesReader` (used on every upload), in Go 1.27.3, so also in v0.10.0-rc.1. Fixed by Go 1.27.4 (`go.mod` and the `golang` image together) | `make vuln` |
| Daily totals (`daily_counts`) | Counts per UTC day only (uploads, upload-link uploads, downloads, owner fetches); no file ID, visitor, hash or time of day, so they reveal nothing about one transfer. One row per day; after 400 days added up per month (`monthly_counts`, twelve rows a year). No API: only the operator's monitoring reads them. Counting is best effort and never fails a transfer; the column names are constants, never input | `TestDailyCounts` |
| Caddy without root | Caddy runs as UID 65532 with no capabilities at all (also not `NET_BIND_SERVICE`): the container's own `net.ipv4.ip_unprivileged_port_start=80` lets it bind 80 and 443, and the binary's file capability is removed (the kernel refuses to exec it without that capability), in a stage on the build platform so the arm64 image needs no emulation. Its directories in the image belong to that user with mode 0700. `deploy.sh` sets the owner of Caddy's files to the web image's user before every start: root without capabilities cannot read files of another user, so converting the volumes before the new release runs (or keeping them on a rollback) would stop the old Caddy | smoke test: no container runs as root or has capabilities; test VM: deploy and rollback across the change |
| TLS (testssl 3.2.1 against filebunny.eu, 2026-11-08) | Grade A+; TLS 1.2 and 1.3, forward secrecy, AEAD ciphers only (AES-GCM, ChaCha20), ECDSA P-256 certificate. No OCSP stapling: Let's Encrypt no longer runs OCSP. Two low findings: no CAA record yet (requested from the DNS provider), and a session ticket lifetime hint of 7 days. Afterwards **TLS 1.3 only** (Mozilla's modern profile): no downgrade to 1.2, and resumed sessions keep forward secrecy, which settles the ticket finding. Clients without TLS 1.3 (old Android, iOS before 12.2, Windows 7/8.1, some inspecting proxies) cannot connect | `testssl https://filebunny.eu` |
| HSTS | `max-age=31536000; includeSubDomains`: no subdomain of filebunny.eu serves HTTP (the mail records are DNS only). `preload` waits until some weeks after the public launch, since leaving the preload list takes months | `curl -I` |
| Certificate source | `FILEBUNNY_TLS` picks a Caddyfile snippet; an unknown value stops Caddy instead of falling back | `caddy validate` |

| IPv6 visitors | **Finding:** on an IPv4-only Compose network Docker passes IPv6 visitors through its userland proxy, so Caddy saw the proxy's address and all IPv6 visitors shared one rate limit (v0.9.0 and older). The network now has IPv6 and Docker forwards them with their own address; with the proxy off (deploy/ansible) they would otherwise not arrive at all | tested on the test VM: IPv6 to the site 200 |
| Server setup (`deploy/ansible`) | SSH key only, ed25519 only, no root, `AllowUsers`, fail2ban; firewall with policy drop on input, forward and output and egress filtering (host: DNS to one provider, NTP, HTTPS, ICMP; containers: HTTPS, DNS to that provider, SMTP relay ports), no `flush ruleset`, only outgoing drops logged; one DNS provider, systemd-resolved and cloud-init off; connection tracking starts before the firewall loads, so the first run keeps its SSH session; firewall and SSH settings re-applied on every run; Docker key checked by fingerprint; apt over HTTPS | fresh test VM, one complete first run: root, the initial user, passwords and non-ed25519 host keys refused; outgoing 80, 22 and DNS to another provider dropped, 443 and 587 (containers) open; allow list closes and opens the site; nothing listens but SSH; survives a reboot; a second run changes nothing |

### Automated checks (2026-11-12, against `v0.10.0-rc.3`)

| Check | Result | Done |
|---|---|---|
| Trivy and `govulncheck -mode=binary` on the release images | Backend: nothing. **Finding:** the web image had Caddy 2.11.7, built with Go 1.26.8: 18 Go advisories in the binary: four in the HTTP/2 server (a crash, double flow control refunds, CPU, memory through trailers), HTTP/1 desynchronization after CONNECT, unlimited `Range` headers, a MIME header memory limit bypass, ECH, html/template, the distrust of compromised intermediates and the `MaxBytesReader` race (GO-2026-6859); in Alpine a critical brotli flaw and curl. Caddy 2.11.9 fixes 13 of the 18 and the Alpine ones. Left in 2.11.9: GO-2026-6859 (Go 1.27.3), a panic in klauspost/compress, two x/net/html flaws (only Caddy's templates and markdown parse HTML; the Caddyfile uses neither) | Caddy 2.11.9 (Dependabot); the rest accepted until 2026-12-15 in `.trivyignore`, so the weekly scan fails again if no Caddy release fixes them |
| Weekly scan | CI only ran govulncheck on a push, so a new advisory for a released image went unnoticed until the next change | `.github/workflows/scan.yml` (release.md) |
| Trivy on the repository (secrets, misconfiguration) | No secrets. Only "no `HEALTHCHECK`" (distroless has no shell; Compose restarts). **Finding:** a compiled `.pyc` of the monitor was committed | removed, `__pycache__/` ignored |
| OWASP ZAP baseline (passive) on the release images, served locally | 0 failures, 7 warnings, all informational or low: no `Cross-Origin-Embedder-Policy`/`-Resource-Policy` (the page loads nothing cross-origin and the static files are public, so they would add nothing), "timestamps" and "Base64" in OpenPGP.js, `Sec-Fetch-*` (sent by browsers, not ZAP), cacheable static files | none needed |
| ssh-audit | ed25519 only, AEAD ciphers, ETM MACs, strict key exchange (Terrapin). `curve25519-sha256` was still allowed as a non-post-quantum fallback. The host key is offered twice on the VPS: the provider's image uncommented `HostKey` in `sshd_config`, which the drop-in repeats (harmless). OpenSSH 10's `PerSourcePenalties` briefly refused the scanner after its unauthenticated connections | only post-quantum hybrid key exchange (client OpenSSH 9.0+) |
| Lynis 3.1.4 on the VPS (installed for the run, then purged with its logs) | Hardening index 73, no warnings, 38 suggestions. Most are a generic checklist without a threat here (GRUB password, password ageing, separate partitions, file integrity and malware scanners, a login banner, another SSH port, remote logging, which would tie the server to other infrastructure). Not taken: `rp_filter` and `forwarding` (Docker needs them), `log_martians` (would log visitors' addresses), `modules_disabled` (breaks Docker and nftables), needrestart (restarts services by itself and had a privilege escalation of its own, CVE-2024-48990) | `kernel.yama.ptrace_scope=1`, `dev.tty.ldisc_autoload=0`, `fs.protected_fifos=2`: only of use after a first break-in as an ordinary user |
| Open ports | nmap from the operator's laptop measured nothing: the VPN on the way accepts every TCP connection itself (even to 9.9.9.9:31337), so every port looks open; only SSH answered with its own banner. Checked on the server instead (configuration, not a measurement from outside): only sshd listens; Docker publishes only 80 and 443 (TCP and UDP for HTTP/3), not the backend's 8080 or Caddy's admin port 2019; nftables accepts new connections on input only for 22, and forwards DNATed traffic only to 80/443 and only from the allow list, for IPv4 and IPv6 | none needed; scan from outside without such a VPN once the site is public |

### Residual risks

- **The compose files come from the operator's checkout** (at the tag's
  commit, which the attestation binds to); a compromised laptop can
  deploy anything regardless.
- **Mailjet keeps every recipient as a contact**, through SMTP and the
  API alike (seen in its dashboard). For API relays the server deletes
  them after a day (below); Mailjet keeps them anonymized for 30 more
  days. For SMTP relays they stay.
- **The contact cleanup deletes every contact in the relay account**, so
  that account must serve FileBunny only (self-hosting.md says so).
  Whether a GDPR delete also clears Mailjet's suppression of an address
  is not documented; FileBunny blocks unsubscribed and complaining
  addresses itself before deleting them.
- **The monitoring reports from the server itself**: a compromised
  server can stop or fake it. Only a missing summary shows that; the
  external watcher covers the site once it is public.
- **crt.sh** sees the server's address asking about its own domain (CT
  logs are public anyway) and is not always reachable; the summary says
  so.
- **TLS 1.3 only** shuts out clients without it (old Android and iOS,
  Windows 7/8.1, some TLS-inspecting proxies). Chosen: every current
  browser has it, and it removes the downgrade to 1.2 and the TLS 1.2
  session ticket window.
- **Egress filtering does not stop exfiltration over HTTPS**: port 443
  must stay open to any address (WKD domains, Filebin, certificates,
  updates). It blocks other ports and makes unexpected connections
  visible in the log.
- **Caddy 2.11.9 still has known flaws** (above): a visitor may crash
  it over HTTP/1 until a Caddy release is built with Go 1.27.4; Docker
  restarts it. Accepted until 2026-12-15 in `.trivyignore`.
- **deepmerge 4.3.1** (GHSA-gcqx-w48m-w2h4, low, no fix yet): prototype
  poisoning, pulled in by `@sveltejs/vite-plugin-svelte`. Only a build
  dependency that merges the plugin's own options; it is not in the site.
- **The admin user is root in practice** (sudo without a password, and
  the `docker` group). Protect its SSH key.
- **Debian's cloud image sums** (used only by `scripts/test-vm.sh`) come
  over HTTPS without a signature.

## Photos from the camera (2026-11-12)

| | |
|---|---|
| Date | 2026-11-12 |
| Scope | Branch `photo-capture`: taking a photo in the browser on the send page and on upload links |
| Reviewer | Claude Code (AI-assisted), during development |

### New attack surface

| Area | Measure | Test |
|---|---|---|
| Camera permission | `Permissions-Policy: camera=(self)` (was `camera=()`): only this site's own pages, no frames (`frame-ancestors 'none'`); microphone and location stay off. The camera starts only on a click, never on page load | `camera.test.ts` (video only, no audio); headless Chromium with a fake camera |
| Metadata in the photo | Encoded from a canvas: no EXIF, so no location, device or time; neutral name `photo.jpg`; literal date 0 as for every file | Decrypted with `gpg`: JFIF without an Exif segment |
| Plaintext in the browser | No object URL, no `localStorage`/IndexedDB/Cache API; the camera stream is never fetched, so the HTTP cache is not involved. Camera off and canvas emptied (`width = 0`) and the Blob dropped after sending, on Retake, on another choice, on leaving the page and on `pagehide` | Headless Chromium: canvas 0 after `pagehide`, no stream after taking the photo |
| CSP | Unchanged: the still is a canvas, so no `img-src blob:` | Build |

### Residual risks

- **When the browser frees the memory** is up to the browser: the page
  only drops every reference. The operating system can still write it to
  swap or a crash dump, and a browser may move very large Blobs to disk
  (a photo of a few MB normally stays in memory).
- **Code from the server sees the camera image.** As with typed text: a
  compromised server could serve code that sends the photo elsewhere. The
  same mitigations apply (signed releases, the watcher, the single HTML
  file).
- **What is in the picture** (faces, screens, documents, a window with a
  view) is up to the person taking it; removing EXIF does not remove that.

## How to re-run the checks

```sh
make test     # unit and security regression tests (incl. log privacy)
make vuln     # govulncheck
cd frontend && npm audit
docker compose up -d --build && scripts/smoke-test.sh   # end to end
```

Against a release (also weekly in `.github/workflows/scan.yml`):

```sh
img=ghcr.io/filebunny/filebunny-web:v0.10.0
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$PWD/.trivyignore:/.trivyignore:ro" aquasec/trivy:0.76.2 image \
  --ignorefile /.trivyignore --severity UNKNOWN,HIGH,CRITICAL --ignore-unfixed "$img"
id=$(docker create "$img") && docker cp "$id:/usr/bin/caddy" /tmp/caddy && docker rm "$id"
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /tmp/caddy)
```

ZAP needs a host name with a dot (Java sends no SNI for `localhost`), so
serve the release images as `filebunny.test` with `FILEBUNNY_TLS=internal`
(`compose.release.yaml`) and run
`docker run --rm --net=host --add-host filebunny.test:127.0.0.1 ghcr.io/zaproxy/zaproxy:stable zap-baseline.py -t https://filebunny.test/`.
Port scans (nmap) only from a network that does not answer for the
target (check that a closed port of another host is refused). On the
server: `ssh-audit <host>` from outside, and Lynis only
temporarily (`apt install lynis`, `lynis audit system`, then
`apt purge lynis` and remove `/var/log/lynis*`).
