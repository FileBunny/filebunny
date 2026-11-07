#!/usr/bin/env bash
# Deploys a signed FileBunny release by digest, or rolls back.
#
#   scripts/deploy.sh [--host user@server] [--dir /opt/filebunny] vX.Y.Z
#   scripts/deploy.sh [--host user@server] [--dir /opt/filebunny] --rollback
#
# Run it on the operator's machine, in this repository, with git, docker
# (buildx) and gh >= 2.49 logged in (GH=/path/to/gh for another gh). For
# the release it:
#   1. looks up the digests of the backend and web images on GHCR;
#   2. checks each with `gh attestation verify`: built by release.yml on
#      GitHub's own runners, from that tag and from the commit the tag
#      points to in this checkout;
#   3. copies compose.yaml, compose.release.yaml and deploy/smtp-off.json
#      from that commit, plus release.env (the images by digest), to
#      <dir>/releases/<tag>/ on the server (over SSH with --host,
#      otherwise on this machine);
#   4. points <dir>/current at it (the release it replaces becomes
#      <dir>/previous), pulls the images, starts them and waits until the
#      site reports the new version.
# The server only needs Docker and curl: it builds nothing and holds no
# GitHub token. The operator's settings stay in <dir>/.env
# (FILEBUNNY_DOMAIN, FILEBUNNY_SMTP, ...), shared by all releases.
# --rollback swaps current and previous and starts that release. The
# backend refuses a rollback across a database migration.
set -euo pipefail

repo=FileBunny/filebunny
gh=${GH:-gh}
host="" dir=/opt/filebunny mode=deploy tag=""
while [[ $# -gt 0 ]]; do
	case $1 in
	--host) host=$2; shift 2 ;;
	--dir) dir=$2; shift 2 ;;
	--rollback) mode=rollback; shift ;;
	-*) echo "unknown option $1" >&2; exit 2 ;;
	*) tag=$1; shift ;;
	esac
done
die() { echo "deploy: $*" >&2; exit 1; }
[[ $dir == /* ]] || die "--dir must be an absolute path"
if [[ $mode == deploy ]]; then
	[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || die "usage: deploy.sh [--host user@server] [--dir path] vX.Y.Z | --rollback"
else
	[[ -z $tag ]] || die "--rollback takes no version"
fi

# Runs a command string on the target.
on_target() {
	if [[ -n $host ]]; then ssh -- "$host" "$1"; else bash -c "$1"; fi
}

# Switches the release on the target and starts it; arguments: dir, mode, tag.
activate='
set -euo pipefail
dir=$1 mode=$2 tag=${3:-}
cd "$dir"
[[ -f .env ]] || { echo "deploy: $dir/.env is missing (FILEBUNNY_DOMAIN and other settings)" >&2; exit 1; }
if [[ $mode == deploy ]]; then
	old=$(readlink current || true)
	if [[ -n $old && $old != "releases/$tag" ]]; then ln -sfn "$old" previous; fi
	ln -sfn "releases/$tag" current
else
	prev=$(readlink previous) || { echo "deploy: no previous release to roll back to" >&2; exit 1; }
	cur=$(readlink current)
	ln -sfn "$prev" current
	ln -sfn "$cur" previous
	tag=${prev#releases/}
fi
c=(docker compose --project-name filebunny --project-directory "$dir/current"
	-f "$dir/current/compose.yaml" -f "$dir/current/compose.release.yaml"
	--env-file "$dir/.env" --env-file "$dir/current/release.env")
"${c[@]}" pull --quiet
"${c[@]}" up -d --remove-orphans
domain=$(grep -E "^FILEBUNNY_DOMAIN=" .env | tail -n 1 | cut -d= -f2- | tr -d "\"\047[:space:]")
domain=${domain:-localhost}
got=""
for _ in $(seq 30); do
	got=$(curl -fsk --max-time 5 --resolve "$domain:443:127.0.0.1" "https://$domain/api/v1/version" 2>/dev/null |
		sed -n "s/.*\"version\":\"\([^\"]*\)\".*/\1/p") || true
	if [[ $got == "$tag" ]]; then echo "deploy: $domain runs $tag"; exit 0; fi
	sleep 2
done
echo "deploy: $domain does not report $tag (got ${got:-nothing}); see: docker compose --project-name filebunny logs" >&2
echo "deploy: to go back: scripts/deploy.sh --rollback" >&2
exit 1
'

if [[ $mode == rollback ]]; then
	on_target "bash -s -- $(printf '%q ' "$dir" rollback) <<'FILEBUNNY_ACTIVATE'
$activate
FILEBUNNY_ACTIVATE"
	exit
fi

"$gh" attestation verify --help 2>/dev/null | grep -q -- --source-digest ||
	die "this gh cannot check attestations by tag and commit; install gh >= 2.49 or set GH=/path/to/gh"
commit=$(git rev-parse --verify --quiet "refs/tags/$tag^{commit}") ||
	die "no tag $tag in this checkout (git fetch --tags)"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/deploy"
for f in compose.yaml compose.release.yaml deploy/smtp-off.json; do
	git show "$commit:$f" >"$stage/$f" 2>/dev/null ||
		die "$tag has no $f (releases before deploy.sh cannot be deployed with it)"
done

for image in backend web; do
	ref=ghcr.io/filebunny/filebunny-$image
	digest=$(docker buildx imagetools inspect "$ref:$tag" --format '{{.Manifest.Digest}}') ||
		die "cannot look up $ref:$tag"
	[[ $digest =~ ^sha256:[0-9a-f]{64}$ ]] || die "unexpected digest for $ref:$tag: $digest"
	"$gh" attestation verify "oci://$ref@$digest" --repo "$repo" \
		--signer-workflow "$repo/.github/workflows/release.yml" \
		--source-ref "refs/tags/$tag" --source-digest "$commit" \
		--deny-self-hosted-runners >/dev/null ||
		die "$ref@$digest is not signed as release $tag ($commit); not deploying"
	echo "deploy: verified $ref@$digest"
	var=FILEBUNNY_$(tr a-z A-Z <<<"$image")_IMAGE
	echo "$var=$ref@$digest" >>"$stage/release.env"
done

rel=$(printf '%q' "$dir/releases/$tag")
tar -C "$stage" -cf - . | on_target "mkdir -p $rel && tar -xf - -C $rel"
on_target "bash -s -- $(printf '%q ' "$dir" deploy "$tag") <<'FILEBUNNY_ACTIVATE'
$activate
FILEBUNNY_ACTIVATE"
