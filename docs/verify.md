# Verifying FileBunny

FileBunny encrypts in your browser, so what you have to trust is the code
your browser runs. You do not have to take our word for it: every release
is built reproducibly, signed, and logged publicly, and you can check that
a site serves exactly that release.

## Which release does a site run?

```sh
curl -s https://filebunny.eu/api/v1/version
```

The answer, e.g. `{"version":"v0.8.0"}`, names a release on
<https://github.com/FileBunny/filebunny/releases>. The footer of every
page shows it too.

## Check the live site

```sh
scripts/verify-site.sh https://filebunny.eu
```

From a clone of this repository. It fetches that release's
`frontend-SHA256SUMS`, checks its signature (with [`gh`](https://cli.github.com)
installed), downloads every file the site serves and compares the hashes,
checks that the page loads nothing else, and checks the security headers
and the certificate. An external watcher on GitHub does the same every
hour; its runs are public:
<https://github.com/FileBunny/filebunny/actions/workflows/watch.yml>.

The site does not show its own status: a compromised server would lie
about that. Check it from the outside, as above.

## Check a download

Every release file and image carries a signed build provenance
attestation: a statement, signed through [Sigstore](https://www.sigstore.dev)
by this repository's release workflow and recorded in the public Rekor
transparency log, of which commit and workflow built it.

```sh
gh attestation verify filebunny-v0.8.0.html --repo FileBunny/filebunny
gh attestation verify filebunny-v0.8.0-linux-amd64 --repo FileBunny/filebunny
gh attestation verify oci://ghcr.io/filebunny/filebunny-web:v0.8.0 --repo FileBunny/filebunny
gh attestation verify oci://ghcr.io/filebunny/filebunny-backend:v0.8.0 --repo FileBunny/filebunny
```

Without `gh`: compare with `SHA256SUMS` from the release
(`sha256sum -c SHA256SUMS`); that file is signed in the same way.

## The single HTML file

`filebunny-<version>.html` is the whole app in one file. Download it from
the release, check it as above, and open it from disk: it does not load
code from the server again, so a later change on the server cannot affect
it. It talks to the API of filebunny.eu.

## Rebuild it yourself

The strongest check needs no trust in GitHub at all: build the release
from source and compare.

```sh
git clone https://github.com/FileBunny/filebunny && cd filebunny
git checkout v0.8.0
FILEBUNNY_CONTACT=filebunny@tutamail.com scripts/release-files.sh v0.8.0 out
diff out/SHA256SUMS <(curl -sL https://github.com/FileBunny/filebunny/releases/download/v0.8.0/SHA256SUMS)
```

This needs Docker (with buildx), Go and Node as pinned in the repository.
The builds do not depend on time, path or machine; CI checks that on every
change (`make repro`, and on x86 and ARM hosts).

## What this does not prove

- That the server runs the published backend: only the files it serves to
  your browser can be checked from the outside. The backend never sees
  plaintext or keys, so the browser code is what matters most.
- That a site served the same files to you as to the watcher. The watcher
  checks once an hour from GitHub; a targeted attack on one visitor would
  not show. Saving the single HTML file avoids depending on the server's
  code at all.
