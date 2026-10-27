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
| `FILEBUNNY_CLEANUP_INTERVAL` | `1m` | How often expired files are removed |
| `FILEBUNNY_LISTEN` | `:8080` | Backend listen address (inside the container) |
| `FILEBUNNY_DATA_DIR` | `/data` in the container | Database and blobs |

Durations use Go syntax (`30m`, `24h`); `7d` is not valid, use `168h`.

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
within 7 days. Back up only your `.env` and any local changes to the
configuration. The `caddy_data` volume (certificates) is recreated
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
