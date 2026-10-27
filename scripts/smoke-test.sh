#!/usr/bin/env bash
# End-to-end check of the Docker Compose stack: headers, upload, download,
# delete, and that no client address or token reaches any container log.
# Usage: docker compose up -d --build && scripts/smoke-test.sh
set -euo pipefail

base=${BASE_URL:-https://localhost}
fake_ip=203.0.113.7
curl=(curl -sk --max-time 10 -H "X-Forwarded-For: $fake_ip" -H "X-Real-IP: $fake_ip")
fail() { echo "FAIL: $*"; exit 1; }

for _ in $(seq 30); do
	"${curl[@]}" -o /dev/null "$base/" && break
	sleep 1
done

echo "frontend headers"
headers=$("${curl[@]}" -D - -o /dev/null "$base/")
grep -qi "^content-security-policy: default-src 'none'; script-src 'self'" <<<"$headers" || fail "frontend CSP missing"
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

echo "error log with backend down"
docker compose stop backend >/dev/null
code=$("${curl[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "$base/api/v1/files/$id")
[[ $code == 502 ]] || fail "backend down: $code"
docker compose start backend >/dev/null

echo "logs"
logs=$(docker compose logs --no-color 2>&1)
for secret in "$fake_ip" "$token" "$id"; do
	grep -qF -- "$secret" <<<"$logs" && fail "log contains $secret"
done
grep -qE '"(remote|client)_ip"' <<<"$logs" && fail "log contains remote_ip/client_ip"

echo "OK"
