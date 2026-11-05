#!/usr/bin/env bash
# Builds the files of a release into <outdir>, from the same Dockerfile
# stages as the images, so the published hashes are exactly what the
# images contain:
#
#   filebunny-<version>-linux-amd64, -linux-arm64   backend binaries
#   filebunny-web-<version>.tar.gz                  what Caddy serves (/srv)
#   frontend-SHA256SUMS                             hash per served file, for the watcher
#   filebunny-<version>.html                        the single HTML file
#   SHA256SUMS                                      all of the above
#
# Usage: scripts/release-files.sh v0.8.0 out
# FILEBUNNY_CONTACT (and _ABUSE, _SECURITY) as for `docker compose build`.
set -euo pipefail

version=${1:?usage: release-files.sh <vX.Y.Z> <outdir>}
out=${2:?usage: release-files.sh <vX.Y.Z> <outdir>}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || { echo "not a release version: $version" >&2; exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"

build() { # build <target> <platform> <dest>
	docker buildx build "$root" --target "$1" --platform "$2" \
		--build-arg VERSION="$version" \
		--build-arg VITE_FILEBUNNY_CONTACT="${FILEBUNNY_CONTACT:-}" \
		--build-arg VITE_FILEBUNNY_ABUSE="${FILEBUNNY_ABUSE:-}" \
		--build-arg VITE_FILEBUNNY_SECURITY="${FILEBUNNY_SECURITY:-}" \
		--output type=local,dest="$3" --quiet >/dev/null
}

for arch in amd64 arm64; do
	build backend "linux/$arch" "$work/backend-$arch"
	cp "$work/backend-$arch/filebunny" "$out/filebunny-$version-linux-$arch"
done

build web linux/amd64 "$work/web"
(cd "$work/web/srv" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sed 's|  \./|  |') >"$out/frontend-SHA256SUMS"
# A tarball that is the same for the same files: fixed order, times and owners.
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
	--pax-option=exthdr.name=%d/PaxHeaders/%f,delete=atime,delete=ctime \
	-C "$work/web" -cf - srv | gzip -n -9 >"$out/filebunny-web-$version.tar.gz"

(cd "$root" && make -s single VERSION="$version" >/dev/null)
cp "$root/frontend/dist-single/filebunny.html" "$out/filebunny-$version.html"

(cd "$out" && find . -maxdepth 1 -type f ! -name SHA256SUMS -printf '%f\n' | sort | xargs sha256sum) >"$out/SHA256SUMS"
cat "$out/SHA256SUMS"
