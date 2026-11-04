#!/usr/bin/env bash
# Checks that a FileBunny site serves exactly a published release:
#   1. asks the site which release it runs (GET /api/v1/version);
#   2. fetches that release's frontend-SHA256SUMS from GitHub (with
#      `gh` installed, also checks its signature: gh attestation verify);
#   3. fetches every file in it from the site and compares the hashes,
#      and checks that the page loads no script or style outside it;
#   4. checks the security headers and that the certificate is valid for
#      at least 14 more days.
# The external watcher (.github/workflows/watch.yml) runs this every hour;
# anyone can run it. Exit status 0 only if everything matches.
#
# Usage: scripts/verify-site.sh https://filebunny.eu
#        scripts/verify-site.sh --manifest <frontend-SHA256SUMS> --insecure https://localhost   (testing)
set -uo pipefail

repo=FileBunny/filebunny
manifest="" insecure=()
while [[ $# -gt 1 ]]; do
	case $1 in
	--manifest) manifest=$2; shift 2 ;;
	--insecure) insecure=(-k); shift ;;
	*) break ;;
	esac
done
site=${1:?usage: verify-site.sh [--manifest file] [--insecure] https://host}
site=${site%/}
host=${site#https://}
[[ $site == https://* && $host != */* ]] || { echo "want https://host, got $site" >&2; exit 2; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0
fail() { echo "FAIL: $*"; failures=$((failures + 1)); }
ok() { echo "ok:   $*"; }
fetch() { curl -fsS --max-time 30 "${insecure[@]}" "$@"; }

version=$(fetch "$site/api/v1/version" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "FAIL: the site does not report a release version (got '${version:-nothing}')"
	exit 1
fi
ok "site runs $version"

if [[ -z $manifest ]]; then
	manifest=$work/frontend-SHA256SUMS
	curl -fsSL --max-time 30 -o "$manifest" "https://github.com/$repo/releases/download/$version/frontend-SHA256SUMS" ||
		{ echo "FAIL: no frontend-SHA256SUMS published for $version"; exit 1; }
	if command -v gh >/dev/null; then
		if gh attestation verify "$manifest" --repo "$repo" >/dev/null 2>&1; then
			ok "frontend-SHA256SUMS is signed by the release workflow of $repo"
		else
			fail "frontend-SHA256SUMS has no valid signature from $repo"
		fi
	else
		echo "note: install gh to also check the signature of frontend-SHA256SUMS"
	fi
fi

# Every published file, as the site serves it (no compression: curl does
# not ask for it, so the bytes are the file's).
while read -r want path; do
	[[ $path =~ ^[A-Za-z0-9._/-]+$ && $path != *..* ]] || { fail "odd path in manifest: $path"; continue; }
	got=$(fetch "$site/$path" | sha256sum | cut -d' ' -f1)
	[[ $got == "$want" ]] && ok "$path" || fail "$path differs from the release"
done <"$manifest"

# The page at / is index.html, and loads nothing that is not published.
index=$(grep '  index.html$' "$manifest" | cut -d' ' -f1)
fetch "$site/" >"$work/root.html"
[[ $(sha256sum <"$work/root.html" | cut -d' ' -f1) == "$index" ]] && ok "/ is the published index.html" || fail "/ differs from index.html"
for ref in $(grep -oE '(src|href)="[^"]*"' "$work/root.html" | sed -E 's/^(src|href)="\/?//; s/"$//'); do
	case $ref in
	http*://* | //*) fail "page loads from elsewhere: $ref" ;;
	*) grep -q "  ${ref}$" "$manifest" || fail "page loads a file that is not published: $ref" ;;
	esac
done

headers=$(fetch -o /dev/null -D - "$site/" | tr -d '\r')
csp=$(grep -i '^content-security-policy:' <<<"$headers" | cut -d' ' -f2-)
[[ $csp == *"default-src 'none'"* && $csp == *"script-src 'self'"* ]] && ok "CSP: default-src 'none', script-src 'self'" || fail "CSP missing or too loose: $csp"
[[ $csp == *unsafe-* ]] && fail "CSP allows unsafe-*: $csp"
grep -qi '^strict-transport-security:' <<<"$headers" && ok "HSTS" || fail "no HSTS header"

if command -v openssl >/dev/null; then
	cert=$(openssl s_client -connect "$host:443" -servername "$host" </dev/null 2>/dev/null)
	if openssl x509 -noout -checkend $((14 * 86400)) <<<"$cert" >/dev/null 2>&1; then
		ok "certificate valid until $(openssl x509 -noout -enddate <<<"$cert" | cut -d= -f2)"
	else
		fail "certificate expires within 14 days, or could not be read"
	fi
fi

if ((failures > 0)); then
	echo "$failures check(s) failed: $site does not match release $version"
	exit 1
fi
echo "$site serves release $version exactly"
