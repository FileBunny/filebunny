#!/usr/bin/env bash
# Builds the release files twice, from two copies of the source that
# differ in what a checkout can differ in (file modes from the umask, the
# directory, the language setting), and fails unless SHA256SUMS is the
# same. The first build ends up in <outdir>.
#
# CI runs this on every change; the release workflow runs it before
# anything is published.
#
# Usage: scripts/release-repro.sh v0.8.0 out
set -euo pipefail

version=${1:?usage: release-repro.sh <version> <outdir>}
out=${2:?usage: release-repro.sh <version> <outdir>}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Two copies of the tracked files: group-writable as with umask 002, and
# not as with umask 022.
copy() {
	mkdir -p "$2"
	git -C "$root" ls-files -z | (cd "$root" && xargs -0 cp --parents -t "$2")
	chmod -R "$1" "$2"
}
copy g+w,o-w "$work/a-$RANDOM"
copy g-w,o-w "$work/b-$RANDOM"
a=$(ls -d "$work"/a-*)
b=$(ls -d "$work"/b-*)

(cd "$a" && LANG=en_US.UTF-8 scripts/release-files.sh "$version" "$out" >/dev/null)
(cd "$b" && LANG=nl_NL.UTF-8 scripts/release-files.sh "$version" "$work/second" >/dev/null)

if ! diff -u "$out/SHA256SUMS" "$work/second/SHA256SUMS"; then
	echo "release files differ between two builds of the same source" >&2
	exit 1
fi
echo "release files are reproducible:"
cat "$out/SHA256SUMS"
