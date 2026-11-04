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

4. The release workflow (`.github/workflows/release.yml`) then:
   - refuses tags that are not on `main`;
   - builds and pushes `ghcr.io/filebunny/filebunny-backend` and
     `ghcr.io/filebunny/filebunny-web` (amd64 and arm64) and signs them;
   - builds the release files with `scripts/release-files.sh` (binaries,
     the served frontend, `frontend-SHA256SUMS`, the single HTML file,
     `SHA256SUMS`) and signs them;
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

## Deploying a release

Pull the signed images instead of building on the server, and check them
first:

```sh
gh attestation verify oci://ghcr.io/filebunny/filebunny-web:v0.8.0 --repo FileBunny/filebunny
gh attestation verify oci://ghcr.io/filebunny/filebunny-backend:v0.8.0 --repo FileBunny/filebunny
```

(Setting up the server itself follows later.)
