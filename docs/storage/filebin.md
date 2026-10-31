# Storage adapter: Filebin

[Filebin](https://filebin.net) is a free file sharing service without
accounts. FileBunny can keep share links there instead of on its own disk.
It only ever stores ciphertext: encryption happens in the browser, as for
local storage. Code: `backend/internal/storage/filebin.go`,
`frontend/src/lib/filebin.ts`.

Upload links (inboxes) always stay on this server.

## Two routes

The sender chooses in step 3 of Send.

| | Through FileBunny | Directly from the browser |
|---|---|---|
| Who sees the sender's IP address | only FileBunny | Filebin |
| Upload | browser → FileBunny → Filebin | browser → filebin.net |
| Download | Filebin → FileBunny → browser | browser ← Filebin |
| Download limit, expiry, delete link | yes, enforced by FileBunny | no: FileBunny does not know about the file |
| Kept for | as chosen, at most 6 days | about 7 days (Filebin's own limit) |
| Link | `#/d/<id>` | `#/x/filebin/<bin>` (the bin name only in the fragment) |
| Traffic on this server | twice the file size | none |
| Needs | `FILEBUNNY_FILEBIN=true` and a passing health check | nothing on the server |

**Through FileBunny**, the server receives the ciphertext as for local
storage, sends it on to Filebin and deletes its local copy. Filebin only
ever sees this server. FileBunny keeps the metadata (expiry, downloads left,
hash of the delete token) and deletes the bin after the last download, when
the file expires or when the sender deletes it.

**Directly**, the browser uploads to Filebin itself and FileBunny's server
is not involved at all. The link carries the bin name in the fragment, which
is never sent to FileBunny. Anyone with the link can download the file, and
Filebin also lets them delete it. The recipient's browser first tries to
fetch the file; if Filebin answers with its verification page, the page
offers to open the file at Filebin in a new tab and decrypt the downloaded
file in FileBunny.

## API used

From the API documentation at `https://filebin.net/api` (`/api.yaml`):

| Request | Used for | Expected |
|---|---|---|
| `POST /{bin}/data.bin` with `Content-SHA256` | upload through FileBunny | `201` |
| `POST /{bin}/data.bin`, no extra headers (a CORS simple request) | direct upload | `201` |
| `GET /{bin}/data.bin` | download | `302` to `storage.filebin.net` (a presigned URL), or `200` with a verification page, see below |
| `DELETE /{bin}` | deleting the bin | `200`; `404` counts as already deleted |

- One bin per file, with one file in it, always named `data.bin`, so the
  original file name never reaches Filebin.
- Bin names are 128 random bits as 32 lowercase hex characters, made by the
  server (through FileBunny) or the browser (direct). Nothing the client
  sends ends up in a URL on the server.

### Verification page

A download may first answer `200` with an HTML verification page that sets a
cookie. As the API documentation says, the request is then repeated with
the cookie. The backend does this once, keeps the cookie for `filebin.net`
only and never sends it to `storage.filebin.net`. Another page after the
repeat counts as "unavailable"; the page itself is never parsed or run.

In a browser the cookie is `HttpOnly` and cross-origin, so a `fetch` from
FileBunny's page cannot complete this step. The direct route then falls back
to opening the file in a tab, where Filebin's page works as intended.

Filebin shows the page more readily to some clients than others; a
browser-like User-Agent triggers it. The backend sends Go's default
User-Agent.

## Limits and health

| What | Value | Where |
|---|---|---|
| Longest expiry through FileBunny | 6 days (Filebin keeps bins about 7) | `Filebin.MaxExpiry` |
| Transfers to or from Filebin at a time | 8, server-wide | `maxRemoteTransfers` |
| One transfer | at most 15 minutes | `remoteTransferTimeout` |
| Traffic per UTC day | 50 GiB by default | `FILEBUNNY_PROXY_DAILY_BYTES` |
| Health check | every 12 hours: 1 KiB of random bytes uploaded, downloaded, compared and deleted | `healthInterval` |
| Taken out of use | after the health check fails, or after 3 failed transfers in a row; back after the next successful check or transfer | `maxFailures` |

While Filebin is out of use, an upload that asks for it is refused with
`503` before any data is sent, and the page offers this server's storage
instead. `GET /api/v1/storage` and the status page (`#/status`) show the
state.

## Outgoing requests

- Only `https://filebin.net` and `https://storage.filebin.net`, port 443.
- Every resolved address is checked as for WKD (`wkd.Allowed`): no private,
  loopback, link-local or other special ranges.
- At most one redirect, only over https and only to those two hosts.
- No proxy from the environment, no compression, limited header size, time
  limits on dial, TLS and response headers.
- Downloads must be exactly the recorded size; a longer or shorter blob
  fails.
- Errors never contain the bin name, because they may be logged.

## What Filebin can see

- **Through FileBunny:** this server's IP address, the ciphertext, its size
  and its SHA-256, and when it is uploaded and downloaded. Not who sent or
  downloaded it.
- **Direct:** the sender's and the recipients' IP addresses and browsers,
  plus all of the above.

Filebin cannot decrypt anything. Its operators, and anyone who learns a bin
name, can delete the file at any time.

## Self-hosting

Off by default. Set `FILEBUNNY_FILEBIN=true` to offer the route through
your server, and allow `https://filebin.net https://storage.filebin.net` in
`connect-src` (already in `deploy/Caddyfile`). The direct route works without
the server setting. Read Filebin's [terms](https://filebin.net/terms) before
you offer it.

## If Filebin changes

Things to check when it stops working:

- `GET /api/v1/storage` shows `available: false`; the log says why
  (`external storage check failed`).
- The status codes above, and the storage host of the redirect.
- Whether the verification step still works the way the API documentation
  describes. Never work around it in any other way.
