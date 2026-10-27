# API v1

Base path `/api/v1`. All bodies are opaque ciphertext; the server never
interprets them. Errors are JSON: `{"error": "<message>"}`.

Every API response carries `Content-Security-Policy: default-src 'none';
frame-ancestors 'none'; sandbox`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer` and `Cross-Origin-Resource-Policy:
same-origin`. There are no CORS headers.

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

## Health

`GET /healthz` returns `200 ok`. Only reachable inside the Compose network;
Caddy does not expose it.

## Changes

The API is versioned in the path. Phase 2 adds chunked uploads and
download limits; breaking changes get `/api/v2`.
