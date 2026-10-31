#!/usr/bin/env bash
# End-to-end check of the Docker Compose stack: headers, upload, download,
# download limits, chunked upload, CORS, delete, the WKD proxy, upload
# links, and that no client address, token or looked-up e-mail address
# reaches any container log.
# Usage: docker compose up -d --build && scripts/smoke-test.sh
set -euo pipefail

base=${BASE_URL:-https://localhost}
fake_ip=203.0.113.7
curl=(curl -sk --max-time 10 -H "X-Forwarded-For: $fake_ip" -H "X-Real-IP: $fake_ip")
fail() { echo "FAIL: $*"; exit 1; }

echo "waiting for $base"
reachable=
for _ in $(seq 30); do
	if "${curl[@]}" --max-time 2 -o /dev/null "$base/"; then
		reachable=1
		break
	fi
	sleep 1
done
[[ -n $reachable ]] || fail "$base not reachable after 30 tries (stack running? a host firewall blocking Docker bridges?)"

echo "frontend headers"
headers=$("${curl[@]}" -D - -o /dev/null "$base/")
grep -qi "^content-security-policy: default-src 'none'; script-src 'self'" <<<"$headers" || fail "frontend CSP missing"
grep -qi "^content-security-policy: .*connect-src 'self' https://keys.openpgp.org;" <<<"$headers" || fail "connect-src not exactly self + keys.openpgp.org"
grep -qi '^strict-transport-security:' <<<"$headers" || fail "HSTS missing"
grep -qi '^server:' <<<"$headers" && fail "Server header present"

echo "upload"
resp=$("${curl[@]}" --data-binary 'smoke-test-ciphertext' "$base/api/v1/files?expires=1h")
id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$resp")
token=$(sed -n 's/.*"delete_token":"\([^"]*\)".*/\1/p' <<<"$resp")
[[ -n $id && -n $token ]] || fail "upload: $resp"

echo "download"
[[ $("${curl[@]}" "$base/api/v1/files/$id") == smoke-test-ciphertext ]] || fail "download mismatch"
api_headers=$("${curl[@]}" -D - -o /dev/null "$base/api/v1/files/$id")
grep -qi "^content-security-policy: .*sandbox" <<<"$api_headers" || fail "API CSP missing sandbox"
grep -qi '^content-disposition: attachment' <<<"$api_headers" || fail "download not an attachment"

echo "delete"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' -X DELETE -H "Authorization: Bearer $token" "$base/api/v1/files/$id")
[[ $code == 204 ]] || fail "delete: $code"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "$base/api/v1/files/$id")
[[ $code == 404 ]] || fail "after delete: $code"

echo "one-time download"
resp=$("${curl[@]}" --data-binary 'once' "$base/api/v1/files?max_downloads=1")
once=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$resp")
[[ -n $once ]] || fail "upload with max_downloads: $resp"
"${curl[@]}" -I "$base/api/v1/files/$once" | grep -qi '^downloads-remaining: 1' || fail "HEAD downloads-remaining"
[[ $("${curl[@]}" "$base/api/v1/files/$once") == once ]] || fail "one-time download"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "$base/api/v1/files/$once")
[[ $code == 404 ]] || fail "second download of one-time file: $code"

echo "chunked upload"
resp=$("${curl[@]}" -X POST "$base/api/v1/uploads")
up=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$resp")
up_token=$(sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p' <<<"$resp")
[[ -n $up && -n $up_token ]] || fail "create upload: $resp"
auth=(-H "Authorization: Bearer $up_token")
"${curl[@]}" "${auth[@]}" -X PUT --data-binary 'chunk-one/' "$base/api/v1/uploads/$up?offset=0" | grep -q '"offset":10' || fail "chunk 1"
"${curl[@]}" "${auth[@]}" -X PUT --data-binary 'chunk-two' "$base/api/v1/uploads/$up?offset=10" | grep -q '"offset":19' || fail "chunk 2"
resp=$("${curl[@]}" "${auth[@]}" -X POST "$base/api/v1/uploads/$up/complete")
chunked=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$resp")
[[ $("${curl[@]}" "$base/api/v1/files/$chunked") == chunk-one/chunk-two ]] || fail "chunked download"

echo "CORS"
cors=$("${curl[@]}" -D - -o /dev/null -X OPTIONS -H 'Origin: null' -H 'Access-Control-Request-Method: PUT' "$base/api/v1/uploads/x")
grep -qi '^access-control-allow-origin: null' <<<"$cors" || fail "no CORS for Origin: null"
cors=$("${curl[@]}" -D - -o /dev/null -H 'Origin: https://evil.example' "$base/api/v1/files/$chunked")
grep -qi '^access-control-allow-origin' <<<"$cors" && fail "CORS for another origin"

echo "WKD proxy"
"${curl[@]}" "$base/api/v1/features" | grep -q '"wkd":true' || fail "features: WKD not on"
wkd_local=smoke-wkd-local
wkd_domain=smoke-wkd-domain.invalid
wkd() { "${curl[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data "{\"email\":\"$1\"}" "$base/api/v1/wkd"; }
[[ $(wkd "$wkd_local@127.0.0.1") == 400 ]] || fail "WKD accepted an IP literal"
[[ $(wkd "$wkd_local@$wkd_domain:8443") == 400 ]] || fail "WKD accepted a port"
# .invalid never resolves: both methods fail, so 502 (or 404 if a
# resolver in between makes something up).
code=$(wkd "$wkd_local@$wkd_domain")
[[ $code == 502 || $code == 404 ]] || fail "WKD lookup of an unresolvable domain: $code"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "$base/api/v1/wkd?email=$wkd_local@$wkd_domain")
[[ $code == 405 ]] || fail "WKD over GET: $code"

echo "upload link"
key=$'-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmDMEsmoketest\n=AAAA\n-----END PGP PUBLIC KEY BLOCK-----\n'
resp=$("${curl[@]}" --data-binary "$key" "$base/api/v1/inboxes?max_uploads=1")
inbox=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$resp")
manage=$(sed -n 's/.*"manage_token":"\([^"]*\)".*/\1/p' <<<"$resp")
[[ -n $inbox && -n $manage ]] || fail "create upload link: $resp"
"${curl[@]}" "$base/api/v1/inboxes/$inbox" | grep -q '"uploads_left":1' || fail "upload link info"
"${curl[@]}" --data-binary 'inbox-ciphertext' "$base/api/v1/inboxes/$inbox/files" | grep -q '"size":16' || fail "upload to link"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' --data-binary 'more' "$base/api/v1/inboxes/$inbox/files")
[[ $code == 409 ]] || fail "upload to a full link: $code"
mauth=(-H "Authorization: Bearer $manage")
list=$("${curl[@]}" "${mauth[@]}" "$base/api/v1/inboxes/$inbox/files")
inbox_file=$(sed -n 's/.*"files":\[{"id":"\([^"]*\)".*/\1/p' <<<"$list")
[[ -n $inbox_file ]] || fail "list upload link: $list"
[[ $("${curl[@]}" "${mauth[@]}" "$base/api/v1/inboxes/$inbox/files/$inbox_file") == inbox-ciphertext ]] || fail "download from link"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "$base/api/v1/files/$inbox_file")
[[ $code == 404 ]] || fail "link file reachable without the management token: $code"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer wrong' "$base/api/v1/inboxes/$inbox/files")
[[ $code == 403 ]] || fail "wrong management token: $code"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "${mauth[@]}" -X DELETE "$base/api/v1/inboxes/$inbox")
[[ $code == 204 ]] || fail "revoke upload link: $code"
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' "$base/api/v1/inboxes/$inbox")
[[ $code == 404 ]] || fail "revoked link still there: $code"

echo "error log with backend down"
docker compose stop backend >/dev/null
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "$base/api/v1/files/$id")
[[ $code == 502 ]] || fail "backend down: $code"
docker compose start backend >/dev/null

echo "logs"
logs=$(docker compose logs --no-color 2>&1)
for secret in "$fake_ip" "$token" "$id" "$up" "$up_token" "$once" "$chunked" "$wkd_local" "$wkd_domain" "$inbox" "$manage" "$inbox_file"; do
	grep -qF -- "$secret" <<<"$logs" && fail "log contains $secret"
done
grep -qE '"(remote|client)_ip"' <<<"$logs" && fail "log contains remote_ip/client_ip"

echo "OK"
