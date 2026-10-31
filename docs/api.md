# API v1

Base path `/api/v1`. All bodies are opaque ciphertext; the server never
interprets them (the WKD proxy returns key bytes it does not parse either). Errors are JSON: `{"error": "<message>"}`.

Every API response carries `Content-Security-Policy: default-src 'none';
frame-ancestors 'none'; sandbox`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer` and `Cross-Origin-Resource-Policy:
same-origin` and `Vary: Origin`.

**CORS** is only answered for `Origin: null`, which is what the
downloadable single HTML file sends when opened from disk:
`Access-Control-Allow-Origin: null`, methods `GET, HEAD, POST, PUT,
DELETE`, headers `Authorization, Content-Type`, exposed headers
`Downloads-Remaining, Expires-At`. Credentials are never allowed (the API
has no cookies). Requests from any other origin get no CORS headers.

## Upload

```
POST /api/v1/files?expires=<duration>&max_downloads=<n>
Content-Type: application/octet-stream

<ciphertext>
```

Both parameters are optional:

- `expires`: a Go duration such as `30m`, `24h` or `168h`, between
  1 minute and `FILEBUNNY_MAX_EXPIRY` (7 days). Default:
  `FILEBUNNY_DEFAULT_EXPIRY` (24 hours).
- `max_downloads`: number of downloads allowed, 1 to
  `FILEBUNNY_MAX_DOWNLOADS` (100). `1` is a one-time download. Default:
  unlimited until expiry.

**201 Created**

```json
{
  "id": "7v2AKiSorZYORZzGK4KQYg",
  "delete_token": "OQaeFws2C8_yPR0PH6IIJP0vrUBbycKlesYhrODkODM",
  "size": 5,
  "expires_at": "2026-10-28T15:16:54Z",
  "max_downloads": 1
}
```

- `id`: 22 characters, base64url, 128 random bits.
- `delete_token`: 43 characters, 256 random bits. Shown only once; the
  server keeps only its hash.
- `expires_at`: UTC, whole seconds.
- `max_downloads`: omitted when downloads are unlimited.

| Status | When |
|---|---|
| 400 | `expires` or `max_downloads` invalid or out of range; body aborted or stalled for 1 minute |
| 413 | Body larger than `FILEBUNNY_MAX_SIZE` (checked on `Content-Length` and while streaming) |
| 507 | Storage quota (`FILEBUNNY_MAX_TOTAL_SIZE`) reached; try later |

```sh
curl --data-binary @message.gpg 'https://filebunny.eu/api/v1/files?expires=1h'
```

## Chunked upload

For large files the browser encrypts and sends the ciphertext in parts, so
it never holds all of it in memory (most browsers cannot stream a request
body). The result is the same as a single upload. All requests after the
first need `Authorization: Bearer <upload_token>`.

### Start

```
POST /api/v1/uploads?expires=<duration>&max_downloads=<n>
```

Same optional parameters as a single upload; they apply when the upload
completes. **201 Created**:

```json
{
  "id": "bXqU3xr4yS2NwJ0qkJ8W4A",
  "upload_token": "s6p0h1Ihk3Qm3b6f4c9Ew2CwAqk5mBc0pYJ7xvVdK1E",
  "max_chunk_size": 16777216,
  "max_size": 104857600
}
```

| Status | When |
|---|---|
| 400 | `expires` or `max_downloads` invalid |
| 503 | `FILEBUNNY_MAX_PENDING_UPLOADS` (50) uploads are in progress server-wide; retry after `Retry-After` seconds |

### Send a chunk

```
PUT /api/v1/uploads/{id}?offset=<bytes received so far>
Content-Length: <n>

<ciphertext part>
```

A chunk is all or nothing: if it fails, nothing of it is kept and the
client sends the same chunk again at the same offset. **200 OK**:
`{"offset": <bytes received>}`.

| Status | When |
|---|---|
| 400 | `offset` missing or invalid; body aborted or stalled for 1 minute |
| 401 / 403 | No upload token / wrong upload token |
| 404 | Unknown, completed, aborted or expired upload |
| 409 | `offset` is not the number of bytes received (the body has the right `offset`), or another request for this upload is in progress |
| 411 | No `Content-Length` |
| 413 | Chunk larger than `max_chunk_size`, or the upload would exceed `max_size` |
| 507 | Storage quota reached |

### Complete

```
POST /api/v1/uploads/{id}/complete
```

**201 Created** with the same body as a single upload (ID, delete token,
size, expiry, download limit). The expiry starts now. `400` if no bytes
were received; `401`/`403`/`404`/`409` as above.

### Abort

```
DELETE /api/v1/uploads/{id}
```

**204 No Content**. Discards the received chunks.

### Limits

An upload that receives no request for **2 minutes** is discarded, which
frees its slot, its quota and its temporary file. Chunks hold storage
quota until the upload completes or is discarded. Uploads in progress
live in memory: a restart discards them.

## Download

```
GET /api/v1/files/{id}
```

**200 OK**, body is the ciphertext.

| Header | Value |
|---|---|
| `Content-Type` | `application/octet-stream` |
| `Content-Length` | Size in bytes |
| `Content-Disposition` | `attachment` |
| `Cache-Control` | `no-store` |
| `Expires-At` | Expiry, HTTP date format |
| `Downloads-Remaining` | Downloads left after this one; omitted when unlimited |

Every `GET` counts as a download as soon as it starts, also when the client
aborts it. The last allowed download removes the file.

`HEAD` returns the same headers without a body and does **not** count;
there `Downloads-Remaining` is the number left. The download page uses it
to show expiry and downloads left before the user decides to download.

| Status | When |
|---|---|
| 404 | Unknown, malformed, deleted, expired or no downloads left (indistinguishable on purpose) |

```sh
curl -o message.gpg https://filebunny.eu/api/v1/files/7v2AKiSorZYORZzGK4KQYg
gpg --decrypt message.gpg
```

## Delete

```
DELETE /api/v1/files/{id}
Authorization: Bearer <delete_token>
```

**204 No Content** on success.

| Status | When |
|---|---|
| 401 | No bearer token |
| 403 | Token does not belong to this file |
| 404 | Unknown, malformed or expired ID |

## Upload links

An upload link (an "inbox") lets anyone send ciphertext to its owner.
The owner registers their public key and gets a public link plus a
secret management token. The browser of an uploader fetches the key,
checks its fingerprint against the one in the link (`#/u/<id>/<fingerprint>`)
and encrypts to it; the server never sees plaintext. Files of a link are
only served with the management token, never through `/api/v1/files`.

### Create

```
POST /api/v1/inboxes?expires=168h&max_uploads=10
Content-Type: text/plain

-----BEGIN PGP PUBLIC KEY BLOCK-----
...
-----END PGP PUBLIC KEY BLOCK-----
```

| Parameter | Default | Range |
|---|---|---|
| `expires` | `168h` (`FILEBUNNY_INBOX_DEFAULT_EXPIRY`) | `1m` to `720h` (`FILEBUNNY_INBOX_MAX_EXPIRY`) |
| `max_uploads` | 10 | 1 to 100 (`FILEBUNNY_INBOX_MAX_UPLOADS`) |

The body must be an ASCII-armored public key of at most 64 KiB. The
server only checks its shape (armor lines, printable ASCII); it does not
parse it. Every link may receive at most `FILEBUNNY_INBOX_MAX_SIZE` bytes
in total (default 100 MiB).

**201 Created**

```json
{"id": "Qm9vZ2llV29vZ2llQm9vZ2ll", "manage_token": "…43 characters…",
 "expires_at": "2026-11-05T12:00:00Z", "max_uploads": 10, "max_size": 104857600,
 "keep_seconds": 604800}
```

`keep_seconds` is how long each upload is kept after it arrives
(`FILEBUNNY_MAX_EXPIRY`).

Only a SHA-256 of the management token is stored.

| Status | When |
|---|---|
| 400 | Not an armored public key, or a parameter out of range |
| 413 | Key over 64 KiB |
| 503 | Too many open links on this server (`FILEBUNNY_MAX_INBOXES`, default 1000) |

### Info (public)

```
GET /api/v1/inboxes/{id}
```

**200 OK**: `{"public_key": "-----BEGIN PGP…", "expires_at": "…",
"uploads_left": 9, "bytes_left": 104857586, "max_file_size": 104857586}`.
A full link is still returned (with nothing left). **404** when unknown,
expired or revoked.

### Upload (public)

```
POST /api/v1/inboxes/{id}/files
```

Body: the ciphertext, at most `max_file_size`. Larger files use the
chunked upload with `POST /api/v1/uploads?inbox={id}` (no `expires` or
`max_downloads`; `max_size` is what the link has left); `complete` then
answers like this endpoint. Files of a link are kept for 7 days
(`FILEBUNNY_MAX_EXPIRY`) after the upload, independent of the link.

**201 Created**: `{"size": 14}`. The uploader gets no ID and no delete
token. The upload and byte counters are updated atomically with storing
the file, so parallel uploads cannot exceed the limits.

| Status | When |
|---|---|
| 400 | Empty body |
| 404 | Unknown, expired or revoked link |
| 409 | No uploads or bytes left |
| 413 | Larger than what the link has left |
| 507 | Server storage full |

### Manage (management token)

All with `Authorization: Bearer <manage_token>`; **401** without a token,
**403** with a wrong one, **404** for an unknown link. They keep working
after the link expired, as long as it still holds files.

| Request | Answer |
|---|---|
| `GET /api/v1/inboxes/{id}/files` | `{"expires_at", "open", "uploads", "max_uploads", "bytes", "max_bytes", "files": [{"id", "size", "received_at", "expires_at"}]}` |
| `GET /api/v1/inboxes/{id}/files/{file}` | The ciphertext, with the download headers above; not counted, can be fetched again |
| `DELETE /api/v1/inboxes/{id}/files/{file}` | 204; the upload it used stays used |
| `DELETE /api/v1/inboxes/{id}` | 204; revokes the link and deletes everything it received |

## Features

```
GET /api/v1/features
```

**200 OK**, `{"wkd": true}`: which optional endpoints this server offers.
The frontend hides the WKD options when `wkd` is `false`.

## WKD lookup

```
POST /api/v1/wkd
Content-Type: application/json

{"email": "alice@example.org"}
```

Fetches the public key for the address from the recipient's own domain
(Web Key Directory, draft-koch-openpgp-webkey-service), because a browser
cannot reach arbitrary domains under the frontend's CSP. POST keeps the
address out of URLs. The address, its domain and hash are never logged or
stored.

**200 OK**, body is the key exactly as the domain published it
(`Content-Type: application/octet-stream`, `Cache-Control: no-store`).
The server does not parse or judge it: the browser checks the user ID and
fingerprint.

The server tries the advanced method
(`https://openpgpkey.<domain>/.well-known/openpgpkey/<domain>/hu/<hash>?l=<local>`)
and only if that host does not exist (NXDOMAIN) the direct method
(`https://<domain>/.well-known/openpgpkey/hu/<hash>?l=<local>`). The hash
is the z-base32 SHA-1 of the lowercased local part.

| Status | When |
|---|---|
| 400 | Not JSON, body over 1 KiB, or an address that is not accepted (see below) |
| 404 | The domain publishes no key for the address, or WKD is switched off on this server (`FILEBUNNY_WKD_PROXY=false`) |
| 502 | Lookup failed: DNS, TLS, timeout (10 s), redirect, a non-public address, an unexpected status, or a response over 1 MiB |
| 503 | Too many lookups in progress (8 server-wide); try again |

Accepted addresses: exactly one `@`; a dot-atom local part of at most 64
ASCII characters; a domain of at least two ASCII letter-digit-hyphen
labels (at most 253 characters) with a non-numeric top-level label. No IP
literals, ports or trailing dot. Internationalized domains must be given
in punycode (`xn--…`); the server does not convert them.

## Health

`GET /healthz` returns `200 ok`. Only reachable inside the Compose network;
Caddy does not expose it.

## Changes

The API is versioned in the path; breaking changes get `/api/v2`.
