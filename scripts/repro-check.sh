#!/usr/bin/env bash
# Builds the backend and frontend twice from separate copies of the source,
# in different directories, and checks that the outputs are identical.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

export CGO_ENABLED=0
status=0

for n in 1 2; do
	dir="$work/build-$n-$RANDOM"
	mkdir -p "$dir"
	git -C "$root" ls-files -z backend frontend | (cd "$root" && xargs -0 cp --parents -t "$dir")

	(cd "$dir/backend" && go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
		-o "$work/filebunny-$n" ./cmd/filebunny)

	(cd "$dir/frontend" && npm ci --ignore-scripts --silent && npm run build --silent >/dev/null &&
		npm run build:single --silent >/dev/null)
	(cd "$dir/frontend" && find dist dist-single -type f -print0 | sort -z | xargs -0 sha256sum) >"$work/frontend-$n.sha256"
done

b1=$(sha256sum <"$work/filebunny-1" | cut -d' ' -f1)
b2=$(sha256sum <"$work/filebunny-2" | cut -d' ' -f1)
if [[ $b1 == "$b2" ]]; then
	echo "backend  OK  $b1"
else
	echo "backend  DIFFERENT  $b1 != $b2"
	status=1
fi

if diff -u "$work/frontend-1.sha256" "$work/frontend-2.sha256"; then
	echo "frontend OK  $(sha256sum <"$work/frontend-1.sha256" | cut -d' ' -f1) (hash of file list)"
	cat "$work/frontend-1.sha256"
else
	echo "frontend DIFFERENT"
	status=1
fi

exit $status
