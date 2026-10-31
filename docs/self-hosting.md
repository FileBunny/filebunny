# Self-hosting

FileBunny runs as two containers: `backend` (Go) and `web` (Caddy with the
frontend). This guide assumes Debian 13 with Docker Engine and the Compose
plugin.

> FileBunny is in early development (phase 1). The encryption UI follows
> in phase 2; until then only the API is usable.

## Requirements

- A server with a public IP address, ports 80 and 443 open (TCP, and UDP
  443 for HTTP/3).
- A domain whose A/AAAA record points to the server. Caddy requests the
  TLS certificate itself.
- Disk space for `FILEBUNNY_MAX_TOTAL_SIZE` plus some headroom.
- Check your hoster's terms: anonymous file sharing must be allowed, and
  abuse reports will reach you.

## Install

Install Docker Engine with the Compose plugin, for example from Docker's
Debian repository (<https://docs.docker.com/engine/install/debian/>). Then:

```sh
git clone https://github.com/FileBunny/filebunny.git
cd filebunny
```

Create a `.env` file next to `compose.yaml`:

```sh
FILEBUNNY_DOMAIN=files.example.org
FILEBUNNY_MAX_TOTAL_SIZE=53687091200   # 50 GiB, leave room on the disk
```

Start:

```sh
docker compose up -d --build
docker compose logs -f
```

Check: `https://files.example.org` shows the page, and

```sh
DOMAIN=files.example.org
curl --data-binary hello "https://$DOMAIN/api/v1/files?expires=5m"
```

returns an ID. Optionally run the end-to-end test on the server, from the
repository directory: `BASE_URL=https://$DOMAIN scripts/smoke-test.sh`.
It stops and starts the backend once to check the error log.

## Configuration

All settings are environment variables; `compose.yaml` passes them to the
backend with defaults.

| Variable | Default | Meaning |
|---|---|---|
| `FILEBUNNY_DOMAIN` | `localhost` | Domain Caddy serves and gets a certificate for |
| `FILEBUNNY_MAX_SIZE` | `104857600` (100 MiB) | Max upload in bytes (ciphertext) |
| `FILEBUNNY_MAX_TOTAL_SIZE` | `21474836480` (20 GiB) | Max combined size of all files; uploads beyond it get `507` |
| `FILEBUNNY_DEFAULT_EXPIRY` | `24h` | Expiry when the client does not ask for one (min `1m`) |
| `FILEBUNNY_MAX_EXPIRY` | `168h` | Longest expiry a client may ask for |
| `FILEBUNNY_MAX_DOWNLOADS` | `100` | Highest download limit a client may ask for; uploads without a limit can be downloaded until they expire |
| `FILEBUNNY_MAX_PENDING_UPLOADS` | `50` | Max chunked uploads in progress, server-wide; more get `503` |
| `FILEBUNNY_WKD_PROXY` | `true` | Offer `POST /api/v1/wkd`, which fetches keys from recipients' domains (Web Key Directory). `false` switches it off and hides the WKD options in the frontend |
| `FILEBUNNY_MAX_INBOXES` | `1000` | Max upload links that are open (not expired) at once, server-wide; more get `503` |
| `FILEBUNNY_INBOX_DEFAULT_EXPIRY` | `168h` | How long a new upload link stays open when it asks for nothing else |
| `FILEBUNNY_INBOX_MAX_EXPIRY` | `720h` | Longest a link may stay open |
| `FILEBUNNY_INBOX_MAX_UPLOADS` | `100` | Highest number of uploads a link may allow (default per link: 10) |
| `FILEBUNNY_INBOX_MAX_SIZE` | `104857600` | Max combined size of the uploads to one link, in bytes (100 MiB) |
| `FILEBUNNY_FILEBIN` | `false` | Offer Filebin (filebin.net) as external storage through this server; see [storage/filebin.md](storage/filebin.md) |
| `FILEBUNNY_PROXY_DAILY_BYTES` | `53687091200` (50 GiB) | Traffic to and from external storage per UTC day, per service; beyond it uploads and downloads there get `503` |
| `FILEBUNNY_SMTP_FILE` | `/run/secrets/smtp` in Compose | JSON file with the sender address and the SMTP relays; see [E-mail](#e-mail). No file or no relays: no e-mail |
| `FILEBUNNY_PUBLIC_URL` | `https://$FILEBUNNY_DOMAIN` in Compose | The site's address for links in mails; required when e-mail is on |
| `FILEBUNNY_MAIL_DAILY` | `500` | Mails per UTC day, server-wide |
| `FILEBUNNY_MAIL_PER_RECIPIENT` | `3` | Mails per address per UTC day |
| `FILEBUNNY_MAIL_PER_FILE` | `3` | Addresses one share link may be mailed to |
| `FILEBUNNY_CLEANUP_INTERVAL` | `1m` | How often expired files are removed |
| `FILEBUNNY_LISTEN` | `:8080` | Backend listen address (inside the container) |
| `FILEBUNNY_DATA_DIR` | `/data` in the container | Database and blobs |

Durations use Go syntax (`30m`, `24h`); `7d` is not valid, use `168h`.

The WKD proxy needs outgoing HTTPS (port 443) from the backend container
to arbitrary domains; the distroless image includes the CA certificates.
It only connects to public addresses (no private, loopback, link-local or
metadata ranges), follows no redirects and logs no looked-up addresses.
If your firewall restricts egress, either allow outgoing 443 for the
backend or set `FILEBUNNY_WKD_PROXY=false`. Keyserver lookups
(keys.openpgp.org) go directly from the browser and need nothing from
your server.

External storage through the server (`FILEBUNNY_FILEBIN=true`) needs
outgoing 443 to `filebin.net` and `storage.filebin.net`. The direct route
to Filebin goes from the browser and needs nothing from your server
except `connect-src` in the CSP, which `deploy/Caddyfile` already allows.
Read the service's terms before you offer it. The adapter docs in
[storage/](storage/) say what each service sees and how it is used.

## E-mail

Senders can have a share link mailed to an address. FileBunny sends a
fixed text through SMTP relays you configure; it never runs its own mail
server. Off until you give it relays.

**The relay passwords never go into the repository, an image, the
environment or CI.** They live in one file outside the repository that
Compose mounts as a secret:

```json
{
  "from": "noreply@files.example.org",
  "relays": [
    {"name": "relay-a", "host": "smtp.relay-a.example", "port": 587,
     "username": "…", "password": "…", "daily_limit": 300},
    {"name": "relay-b", "host": "smtp.relay-b.example", "port": 465,
     "username": "…", "password": "…", "daily_limit": 100}
  ]
}
```

```sh
sudo install -d -m 0755 /etc/filebunny
sudo install -m 0400 -o 65532 smtp.json /etc/filebunny/smtp.json   # 65532: the backend's user
FILEBUNNY_SMTP=/etc/filebunny/smtp.json docker compose up -d
```

- `name`: shown in logs and used for counters (a-z, 0-9, `-`); never the
  account. `daily_limit`: the relay's own free-tier limit; FileBunny sends
  no more than that through it per UTC day.
- Port 465 uses TLS from the start (or set `"implicit_tls": true`); any
  other port requires STARTTLS. Certificates are always verified; plain
  SMTP is never used.
- The relays are used round-robin; a relay that is full for the day or
  fails is skipped. Follow each relay's terms: do not use several relays
  to get around one relay's limits.
- Without `FILEBUNNY_SMTP`, Compose mounts `deploy/smtp-off.json` (no
  relays): mail off.
- Prefer keys with sending rights only, for your sender address only,
  and, where the relay offers it, only from your server's IP address.

**DNS** for the sender's domain, from each relay's instructions: one SPF
record that includes every relay (at most 10 DNS lookups in total), a
DKIM record per relay, and a DMARC record (start with `p=none`, tighten
once the reports look right). Send a test mail and check that the
headers say `spf=pass`, `dkim=pass` and `dmarc=pass`.

The backend keeps a secret key (`data/mail-pepper`) for the address
hashes behind the limits and the block list. Losing it resets both.

The backend needs outgoing access to the relays' SMTP ports.

## Downloadable single HTML file

`make single` builds `frontend/dist-single/filebunny.html`: the whole app
(script and styles inline, with a CSP by hash) in one file that people can
save and open from disk, so they do not depend on the JavaScript your
server sends each time. It talks to the API at `VITE_FILEBUNNY_API`
(default `https://filebunny.eu`, see `frontend/.env.single`). For your own
instance:

```sh
cd frontend && VITE_FILEBUNNY_API=https://files.example.org npm run build:single
```

The backend allows CORS for `Origin: null` only, which is what such a page
sends. Share and delete links made from the file point to that origin.

## Privacy checklist

FileBunny stores no IP addresses. Keep it that way on your server:

- Do not add a `log` directive to the site block in `deploy/Caddyfile`
  (that would enable access logs), and keep the log filter.
- Do not put another proxy or CDN in front that logs addresses, or
  configure it not to.
- Docker's own logs only contain what the containers print; the
  containers print no addresses. Consider `journald`/log rotation limits.
- Firewall or fail2ban logs on the host may contain addresses; decide
  consciously whether you need them.
- With the WKD proxy on, your server sees which e-mail addresses are
  looked up (not by whom). It never logs them; keep it that way, and
  switch it off (`FILEBUNNY_WKD_PROXY=false`) if you prefer not to see
  them at all.
- With e-mail on, your server and the relay see the recipient's address
  for each mail. FileBunny keeps only a keyed hash of it (for the limits
  and the block list) and never logs it; the relay keeps its own logs
  under its own terms.
- External storage through the server shows the service only your
  server's address. The direct route shows it the visitor's address; the
  page says so before anything is sent.

## Updates

```sh
git pull
docker compose up -d --build
```

Uploaded files and certificates live in Docker volumes (`data`,
`caddy_data`) and survive updates. Database migrations run automatically
at startup.

## Backups

Do **not** back up uploads: they are temporary ciphertext and expire
within 7 days. Back up only your `.env`, the SMTP file (in your password
manager, not next to the backups of the server) and any local changes to
the configuration. The `caddy_data` volume (certificates) is recreated
automatically if lost.

## Removing all data

```sh
docker compose down -v   # removes containers and all volumes, including uploads
```

## Without Docker

Build the binary with `make backend` (reproducible, statically linked)
and run it with the variables above behind your own reverse proxy. The
proxy must strip `X-Forwarded-For`, `X-Real-IP` and `Forwarded`, must not
log client addresses, and must serve `frontend/dist` with the headers from
`deploy/Caddyfile`. Do not expose the backend's port directly.
