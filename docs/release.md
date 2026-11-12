# Releasing

1. Make sure `main` has everything and CI on `main` is green.
2. Pick the version: SemVer, `vMAJOR.MINOR.PATCH`. Before 1.0 the minor
   number goes up for new features (`v0.8.0`, `v0.9.0`), the patch number
   for fixes.
3. Tag the merge commit on `main` and push the tag:

   ```sh
   git checkout main && git pull
   git tag -a v0.8.0 -m "FileBunny v0.8.0"
   git push origin v0.8.0
   ```

   To try the whole chain first, tag a release candidate such as
   `v0.8.0-rc.1`: it goes through the same workflow and is published as a
   pre-release. If something fails, delete that release and tag
   (`gh release delete v0.8.0-rc.1 --cleanup-tag`), fix, and tag `rc.2`.

4. The release workflow (`.github/workflows/release.yml`) then:
   - refuses tags that are not on `main`;
   - builds and pushes `ghcr.io/filebunny/filebunny-backend` and
     `ghcr.io/filebunny/filebunny-web` (amd64 and arm64) and signs them;
   - builds the release files twice with `scripts/release-repro.sh`
     (binaries, the served frontend, `frontend-SHA256SUMS`, the single
     HTML file, `SHA256SUMS`), stops if the two builds differ, and signs
     them;
   - publishes the GitHub release with the image digests and how to verify.

   No secrets are involved: signing is keyless (Sigstore, through the
   workflow's OIDC identity).
5. Check it from the outside: `gh attestation verify` on a file and an
   image (see [verify.md](verify.md)), and after deploying,
   `scripts/verify-site.sh https://filebunny.eu`.

The first time, make the two GHCR packages public (GitHub → the package
→ Package settings → Change visibility); new packages start private.

## The watcher

`.github/workflows/watch.yml` runs `scripts/verify-site.sh` every hour
against the repository variable `WATCH_URL`, and opens an issue labeled
`watcher` when the site does not match its release. It does nothing
until `WATCH_URL` is set (Settings → Secrets and variables → Actions →
Variables), so set it once the site runs a release. GitHub pauses
scheduled workflows in repositories without activity for 60 days; a push
or a manual run starts them again.

## Keeping up with updates

| What | How updates arrive |
|---|---|
| Go modules, npm packages, GitHub Actions, base image digests | Dependabot opens a pull request every week |
| Caddy (new release tag) | Dependabot, once the tag is out |
| Go itself (`backend/go.mod` with the `golang` image) and Node majors | By hand (Dependabot ignores them on purpose) |
| Debian and Docker on the server | unattended-upgrades, with a reboot at `filebunny_reboot_time` |
| Everything in the images | Only through a new release and `scripts/deploy.sh` |

`.github/workflows/scan.yml` runs every Monday (and by hand): govulncheck
on the source of `main`, and Trivy plus `govulncheck -mode=binary` on the
images of the newest release, pre-releases included, so on what a server
runs. It fails on HIGH, CRITICAL and not yet rated findings that have a
fix; GitHub mails the owner of the repository about a failed scheduled
run. That is the cue to update and release. A finding that is accepted for
now goes in `.trivyignore` with its reason and an expiry date (`exp:`);
after that date it fails the scan again.

## Deploying a release

`scripts/deploy.sh` deploys a release by digest; the server never builds.
Run it from a checkout with the release tag, with `gh` (>= 2.49, logged
in) and Docker:

```sh
git fetch --tags
scripts/deploy.sh --host deploy@filebunny.eu v0.10.0
scripts/verify-site.sh https://filebunny.eu
```

It looks up both images' digests and checks each with
`gh attestation verify`: signed by `release.yml` on GitHub's runners, from
`refs/tags/<tag>` and from the commit the tag points to in your checkout,
so a moved tag or an older signed image is refused. It then copies the
compose files of that commit and a `release.env` with the images by
digest to `/opt/filebunny/releases/<tag>/` (`--dir` for another place),
points `current` at it, gives Caddy's files to the user the web image
runs as, starts it (containers always recreated) and waits until the site
reports the version. The server needs Docker and curl, and `/opt/filebunny/.env` with
the operator's settings; it holds no GitHub token.

To go back to the release before:

```sh
scripts/deploy.sh --host deploy@filebunny.eu --rollback
```

This swaps `current` and `previous`. The backend refuses to start on a
database a newer release migrated; then deploy a newer release instead.

Who deploys: whoever can log in to the server as a user in the `docker`
group, which is root on that server in practice. Keep that to the
operator's own SSH key.
