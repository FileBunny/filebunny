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

## Server setup with Ansible

`deploy/ansible` sets up a fresh Debian 13 server for FileBunny. It only
uses `ansible.builtin`, so `ansible-core` on your own machine is enough;
nothing is installed on the server for Ansible beyond Python. It is
idempotent: change a variable and run it again.

What it does:

- an admin user with your SSH key(s) and sudo; the provider's initial
  user can be removed (`filebunny_remove_users`), root's SSH keys are
  removed. SSH by key only, no
  root, `AllowUsers` the admin, only ed25519 (host key, logins, also on a
  FIDO2 key) with post-quantum hybrid key exchange, `MaxAuthTries 3`,
  `MaxSessions 2`, `LogLevel VERBOSE` (key fingerprints in the log),
  fail2ban on SSH. The settings are a drop-in that sshd reads first;
  check the result with `sudo sshd -T`;
- a firewall (nftables, table `inet filebunny`) with policy drop on
  input, forward and output, and **egress filtering**: the server may
  only open DNS (to `filebunny_dns` only), NTP, HTTPS and ICMP; the
  containers only HTTPS, DNS to the same provider and the SMTP ports of
  your relays (`filebunny_smtp_ports`, default 465 and 587). Dropped
  outgoing connections are logged (rate limited); dropped incoming and
  forwarded traffic is not, since it carries visitors' addresses.
  Docker's and fail2ban's tables are left alone: the file has no
  `flush ruleset`;
- one DNS provider in a plain `/etc/resolv.conf` (default Quad9, with
  its IPv4 and IPv6 addresses); systemd-resolved, if the image has it, is
  switched off (no fallback servers, no LLMNR/mDNS listeners), and
  resolvconf/openresolv are removed with the `dns-*` lines of the ifupdown
  config, which they would write back into `resolv.conf` at every boot;
- cloud-init switched off after the setup, so it cannot reapply the
  provider's keys, network or DNS at boot (`filebunny_disable_cloud_init`);
- Debian sources over HTTPS only, automatic updates for Debian and
  Docker with a reboot at `filebunny_reboot_time` (local time, in
  `filebunny_timezone`) when one needs it; packages a FileBunny server
  does not need are removed (`filebunny_remove_packages`);
- Docker from Docker's repository (key checked against its published
  fingerprint), with log caps, `no-new-privileges`, `live-restore` and
  without the userland proxy, so visitors reach Caddy with their own
  address over IPv4 and IPv6;
- time sync, a swap file if the server has no swap, a few kernel
  settings, a capped persistent journal;
- `/opt/filebunny/.env` from your variables for `scripts/deploy.sh`, and
  the SMTP file (`filebunny_smtp_file`, kept outside the repository).

### Monitoring

With `filebunny_monitor` (on by default) the playbook installs
`filebunny-monitor`, which a systemd timer runs every hour. It checks the
disk, the containers (also restarts), the version and certificate the
site serves, errors in the container logs, problems of the relay contact
cleanup, failed systemd units, a reboot
that is not happening, time sync and outgoing traffic, and every 6 hours
asks crt.sh (Certificate Transparency) for new certificates for your
domain. It mails when a problem starts and when it ends, and every day at
`filebunny_monitor_summary_hour` (default 6) a summary with trends: when
the disk would be full at the growth of the last 14 days, the traffic
forecast for the month against `filebunny_monitor_traffic_tb`, and the
daily totals of uploads and downloads, failed mail deliveries and what
the contact cleanup deleted and blocked. If the summary does not arrive,
the server cannot mail.

Mail goes to `filebunny_monitor_mail_to` through the relays of
`filebunny_smtp_file` (with `filebunny_share_mail: false` only the
monitoring uses them, and the site does not mail share links), sent by the user `filebunny-mail`: the firewall
lets only that user reach the relay, and only for two minutes after the
monitor has put the relay's addresses in a set; that user can do nothing
else but DNS. Try it with `sudo filebunny-monitor --test-mail`;
`--summary` sends the summary now, `--dry-run` prints instead of
sending. Without an SMTP file it only writes to the journal
(`journalctl -u filebunny-monitor`). The mails count against the relay's
own limits, not FileBunny's.

DHCP is only allowed through the firewall with `filebunny_dhcp: true`;
leave it off when the provider configures the network statically.

Copy `deploy/ansible/inventory.example.yml` **outside the repository**,
fill it in and run:

```sh
ansible-playbook -i ~/filebunny-inventory.yml deploy/ansible/site.yml
```

The first run logs in as the provider's initial user (usually `root`) and
continues as the admin once that exists. After it, only the admin user
can log in: set `ansible_user` to it.
Then deploy a release with `scripts/deploy.sh --host admin@server vX.Y.Z`.

A closed test: `filebunny_web_allow` (addresses and networks, IPv4 and
IPv6) lets only those reach ports 80 and 443; SSH stays open. Let's
Encrypt checks the domain from addresses nobody knows in advance, so for
the first certificate run once with the list empty, wait until Caddy has
it (`docker compose --project-name filebunny logs web`), and run again
with the list. Caddy renews about a month before expiry; repeat that if
the test lasts that long.

What the host logs: SSH logins and failures (with the address that
tried), fail2ban bans and dropped outgoing connections (the server's own
destinations). Never visitors' web traffic: the firewall does not log it
and Caddy has no access log.

`scripts/test-vm.sh up` starts a local VM (libvirt) sized like a small
VPS to try all this first; set `filebunny_tls: internal` and an internal
name such as `filebunny.home.arpa` there.

## Configuration

All settings are environment variables; `compose.yaml` passes them to the
backend (and the two marked "Caddy only" to Caddy) with defaults.

| Variable | Default | Meaning |
|---|---|---|
| `FILEBUNNY_DOMAIN` | `localhost` | Domain Caddy serves and gets a certificate for |
| `FILEBUNNY_TLS` | `auto` | Caddy only. `auto`: certificate from Let's Encrypt (ports 80 and 443 reachable from the internet). `internal`: Caddy's own CA, for a test server on an internal name; trust the root certificate (`docker compose cp web:/data/caddy/pki/authorities/local/root.crt .`) in your browser. `localhost` always uses the local CA |
| `FILEBUNNY_WWW_REDIRECT` | `false` | Caddy only. `true` also serves `www.$FILEBUNNY_DOMAIN` and redirects it to the domain; needs a DNS record for `www` |
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
| `FILEBUNNY_CLIENT_IP_HEADER` | `X-FileBunny-Client` in Compose, empty otherwise | Header the reverse proxy puts the visitor's address in, for rate limiting; the backend only hashes it. Empty: the connection's address. Only set it if the backend is reachable through that proxy alone |
| `FILEBUNNY_RATE_LIMITS` | `true` | Per-visitor limits (see [api.md](api.md#proof-of-work-and-limits)) |
| `FILEBUNNY_POW_BITS` | `18` | Base proof-of-work difficulty in bits, `0` (off) to `24`; each bit doubles the work |
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

- `type`: `smtp` (the default) or `mailjet-api`. Use `mailjet-api` when
  your provider blocks outgoing SMTP (465/587): it sends through
  Mailjet's Send API over HTTPS, so only port 443 is needed. It takes no
  `host`, `port` or `implicit_tls`; `username` and `password` are the API
  key and secret (the same as Mailjet's SMTP credentials):

  ```json
  {"name": "mailjet", "type": "mailjet-api",
   "username": "<API key>", "password": "<secret key>", "daily_limit": 200}
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
- Switch off open and click tracking at the relay: FileBunny sends plain
  text, but a relay that rewrites links would see every click. (An API
  relay switches tracking off in every message itself.)
- A relay may keep every recipient in its own contact list; Mailjet does,
  through SMTP and the API. For an API relay the backend removes them
  once a day (`"delete_contacts": true`, the default; `false` keeps
  them): every contact older than a day, through Mailjet's GDPR delete
  (anonymized at once, gone after 30 days). Addresses that unsubscribed
  through Mailjet's own link or reported spam go on FileBunny's block
  list first (as an HMAC), since Mailjet replaces FileBunny's
  unsubscribe header. **Use a Mailjet account for FileBunny only**: the
  cleanup deletes every contact in it. At most 2000 deletions per relay
  and day; a rate limit (429) ends the run until the next day. For an
  SMTP relay, delete them in the relay's dashboard.
- Some relays (Mailjet, for example) replace FileBunny's
  `List-Unsubscribe` header with their own; addresses that unsubscribe
  that way end up on the relay's suppression list instead of FileBunny's
  block list. The block link in the mail text is not affected.

**DNS** for the sender's domain, from each relay's instructions: one SPF
record that includes every relay (at most 10 DNS lookups in total), a
DKIM record per relay, and a DMARC record (start with `p=none`, tighten
once the reports look right). Send a test mail and check that the
headers say `spf=pass`, `dkim=pass` and `dmarc=pass`.

The backend keeps a secret key (`data/mail-pepper`) for the address
hashes behind the limits and the block list. Losing it resets both.

The backend needs outgoing access to the relays' SMTP ports, or to 443
for an API relay.

## Releases

Instead of building, you can run the signed release images
(`ghcr.io/filebunny/filebunny-backend` and `-web`) with
`compose.release.yaml`. `scripts/deploy.sh` checks their signatures, runs
them by digest and can roll back; see
[release.md](release.md#deploying-a-release). They contain filebunny.eu's
contact address and `security.txt`, so build your own web image for your
own contact details. `FILEBUNNY_VERSION=vX.Y.Z docker compose build` labels a
local build with a version.

## Contact page and security.txt

The contact, terms and privacy pages show addresses you set at build
time; empty ones are left out:

```sh
FILEBUNNY_CONTACT=contact@files.example.org FILEBUNNY_ABUSE=abuse@files.example.org \
FILEBUNNY_SECURITY=security@files.example.org docker compose up -d --build
```

(Without Docker: `VITE_FILEBUNNY_CONTACT`, `VITE_FILEBUNNY_ABUSE`,
`VITE_FILEBUNNY_SECURITY` for `npm run build`.)

`deploy/well-known/security.txt` is filebunny.eu's security contact.
Replace it with your own (RFC 9116, with an `Expires` within a year) or
delete it; put a public key for it next to it if you mention one. The
terms page says Dutch law applies; change `Terms.svelte` if that is not
true for you.

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

## TLS and HSTS

Caddy speaks **TLS 1.3 only** (`protocols tls1.3` in `deploy/Caddyfile`).
Clients without it, such as old Android or iOS versions, Windows 7/8.1 or
some TLS-inspecting proxies, cannot connect; add `tls1.2` to the
`protocols` lines if you must support them.


Caddy sends `Strict-Transport-Security: max-age=31536000;
includeSubDomains`: for a year, browsers that visited the site use only
HTTPS for your domain **and all its subdomains**. If a subdomain of your
FileBunny domain must stay reachable over plain HTTP, remove
`includeSubDomains` in `deploy/Caddyfile`.

## Privacy checklist

FileBunny stores no IP addresses. Keep it that way on your server:

- Do not add a `log` directive to the site block in `deploy/Caddyfile`
  (that would enable access logs), and keep the log filter.
- Do not put another proxy or CDN in front that logs addresses, or
  configure it not to.
- Docker's own logs only contain what the containers print; the
  containers print no addresses. `compose.yaml` caps them at three files
  of 10 MB per container; keep a cap if you change the log driver.
- Firewall or fail2ban logs on the host may contain addresses; decide
  consciously whether you need them. `deploy/ansible` logs SSH (logins
  are not visitors) and dropped outgoing connections, never web traffic.
- With the WKD proxy on, your server sees which e-mail addresses are
  looked up (not by whom). It never logs them; keep it that way, and
  switch it off (`FILEBUNNY_WKD_PROXY=false`) if you prefer not to see
  them at all.
- Caddy passes each visitor's address to the backend in
  `X-FileBunny-Client` for rate limiting. The backend hashes it at once
  with a key that changes daily and exists only in memory, and never
  stores or logs it. Keep the backend unreachable except through Caddy,
  or anyone could fake the header.
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

With the release images: `scripts/deploy.sh vX.Y.Z` (see
[release.md](release.md#deploying-a-release)).

Updating from v0.9.0 or older: the network now has IPv6, so run
`docker compose down` once before `up` (data volumes stay).

Updating from v0.10.0-rc.1 or older: Caddy now runs as UID 65532 without
capabilities, so its existing volumes must belong to that user once
(`scripts/deploy.sh` does this before every start, also for a rollback to
an older release, where root must own them again). After building the new
images and before `up`:

```sh
docker compose run --rm --no-deps --user 0:0 --cap-add CHOWN --cap-add DAC_READ_SEARCH \
  --entrypoint chown web -R 65532:65532 /data/caddy /config/caddy
```

Uploaded files and certificates live in Docker volumes (`data`,
`caddy_data`) and survive updates. Database migrations run automatically
at startup. The backend refuses to start on a database that a newer
release has migrated, so going back across a migration is not possible.

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
proxy must strip `X-Forwarded-For`, `X-Real-IP` and `Forwarded`, set the
visitor's address in the header named by `FILEBUNNY_CLIENT_IP_HEADER`
(replacing anything the client sent), must not log client addresses, and
must serve `frontend/dist` with the headers from `deploy/Caddyfile`. Do
not expose the backend's port directly.
