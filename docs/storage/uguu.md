# Storage adapter: Uguu

[Uguu](https://uguu.se) is a free file host without accounts, run by
Pomf AB in Sweden (source: [nokonoko/uguu](https://github.com/nokonoko/uguu)).
FileBunny can keep share links there instead of on its own disk. It only
ever stores ciphertext: encryption happens in the browser, as for local
storage. Code: `backend/internal/storage/uguu.go`.

## Only through FileBunny, only for share links

| | Uguu | Compare: Filebin |
|---|---|---|
| Route | through FileBunny only: Uguu sends no CORS headers, so a browser cannot fetch from it | through FileBunny or directly |
| Upload links | no: Uguu keeps files for 3 hours, too short for an owner who looks later | yes |
| Longest expiry through FileBunny | 2 hours | 6 days |
| Delete | **not possible**: the file stays at Uguu for 3 hours after the upload | after the last download, expiry or delete link |
| Integrity at upload | none: Uguu takes no checksum; the reply must report the right size | `Content-SHA256` |

**Through FileBunny**, the server receives the ciphertext as for local
storage, sends it on to Uguu and deletes its local copy. Uguu only sees
this server. FileBunny keeps the metadata (expiry, downloads left, hash of
the delete token) and stops handing the file out after the last download,
when it expires or when the sender deletes it. The ciphertext itself stays
at Uguu until Uguu removes it, 3 hours after the upload. Only this server
knows its name.

## API used

From [uguu.se/api](https://uguu.se/api):

| Request | Used for | Expected |
|---|---|---|
| `POST https://uguu.se/upload`, multipart field `files[]`, file name `data.bin` | upload | `200`, JSON `{"success": true, "files": [{"url": "https://<letter>.uguu.se/<name>", "size": <n>, ...}]}` |
| `GET https://<letter>.uguu.se/<name>` | download | `200`, `application/octet-stream`; `404` when it is gone |

Uguu spreads files over one-letter subdomains (seen on 2026-11-24: `d`,
`h`, `n`), so the reference stored is `<letter>/<name>`.

- One file per upload, always named `data.bin`, so the original file name
  never reaches Uguu.
- Uguu chooses the file name. The reply is checked strictly: exactly one
  file, of the uploaded size, at `https://<letter>.uguu.se/<name>` (one
  lowercase letter, no port, user, query or fragment), and a name of 6 to
  32 letters and digits plus `.bin`.
  Only such a name is stored, and it is checked again before every
  download. Nothing the client sends ends up in a URL.
- The upload has a fixed `Content-Length` (no chunked encoding).

## Limits and health

| What | Value | Where |
|---|---|---|
| Longest expiry through FileBunny | 2 hours (Uguu keeps files 3) | `Uguu.MaxExpiry` |
| Size | 100 MiB, FileBunny's limit (Uguu allows 128 MiB) | `FILEBUNNY_MAX_SIZE` |
| Transfers to or from Uguu at a time | 8, server-wide | `maxRemoteTransfers` |
| One transfer | at most 15 minutes | `remoteTransferTimeout` |
| Traffic per UTC day | 50 GiB by default, shared with the other adapters | `FILEBUNNY_PROXY_DAILY_BYTES` |
| Health check | every 12 hours: 1 KiB of random bytes uploaded, downloaded and compared; it stays at Uguu for 3 hours | `healthInterval` |
| Taken out of use | after the health check fails, or after 3 failed transfers in a row | `maxFailures` |

While Uguu is out of use, an upload that asks for it is refused with `503`
before any data is sent. `GET /api/v1/storage` and the status page
(`#/status`) show the state; there `upload_links` is `false` for Uguu, and
creating an upload link with `storage=uguu` answers `400`.

## Outgoing requests

- Only `https://uguu.se` and its one-letter subdomains (`a.uguu.se` to
  `z.uguu.se`), port 443.
- Every resolved address is checked as for WKD (`wkd.Allowed`).
- At most one redirect, only over https and only to those hosts.
- No proxy from the environment, no compression, limited header and reply
  size, time limits on dial, TLS and response headers.
- Downloads must be exactly the recorded size; a web page instead of the
  file counts as unavailable.
- Errors never contain the file name or URL, because they may be logged.

## What Uguu can see

This server's IP address, the ciphertext, its size and when it is uploaded
and downloaded. Uguu keeps a list of active files with name, hash and the
uploader's IP address (its FAQ): that is this server's address, not the
sender's. Uguu cannot decrypt anything.

Because a file cannot be deleted, anyone who learns its name could fetch
the ciphertext until Uguu removes it. The name is random and only this
server knows it.

## Self-hosting

Off by default. Set `FILEBUNNY_UGUU=true` (with `deploy/ansible`:
`filebunny_uguu: true`). No change to `connect-src`: browsers never talk
to Uguu. Read Uguu's [FAQ](https://uguu.se/faq) before you offer it.

## If Uguu changes

- `GET /api/v1/storage` shows `available: false`; the log says why
  (`external storage check failed`).
- Check the upload reply (`curl -F 'files[]=@file' https://uguu.se/upload`):
  field names, the file hosts and the form of the names.
