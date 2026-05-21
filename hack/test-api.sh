#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${OPERATOR_API_URL:-https://operator.infra.deco.cx}"
USER="${OPERATOR_API_USER:-sre@deco.cx}"
PASS="${OPERATOR_API_PASSWORD:-}"
AUTH="-u ${USER}:${PASS}"

DOMAIN="${1:-test-script.com}"
TO="${2:-https://www.${DOMAIN}}"

pass() { echo "✓ $1"; }
fail() { echo "✗ $1"; exit 1; }

echo "=== Operator API Test ==="
echo "URL:    $BASE_URL"
echo "Domain: $DOMAIN → $TO"
echo ""

# 1. Auth check
echo "--- 1. Unauthorized request ---"
code=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/redirects")
[ "$code" = "401" ] && pass "401 on missing auth" || fail "expected 401, got $code"

# 2. List
echo "--- 2. List ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH "$BASE_URL/redirects")
[ "$code" = "200" ] && pass "GET /redirects → 200" || fail "expected 200, got $code"
curl -s $AUTH "$BASE_URL/redirects" | python3 -m json.tool

# 3. Create
echo "--- 3. Create ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH -X POST "$BASE_URL/redirects" \
  -H "Content-Type: application/json" \
  -d "{\"from\":\"$DOMAIN\",\"to\":\"$TO\"}")
[ "$code" = "201" ] && pass "POST /redirects → 201" || fail "expected 201, got $code"

# 4. Create duplicate → 409
echo "--- 4. Duplicate create ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH -X POST "$BASE_URL/redirects" \
  -H "Content-Type: application/json" \
  -d "{\"from\":\"$DOMAIN\",\"to\":\"$TO\"}")
[ "$code" = "409" ] && pass "POST duplicate → 409" || fail "expected 409, got $code"

# 5. Get
echo "--- 5. Get ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH "$BASE_URL/redirects/$DOMAIN")
[ "$code" = "200" ] && pass "GET /redirects/$DOMAIN → 200" || fail "expected 200, got $code"
curl -s $AUTH "$BASE_URL/redirects/$DOMAIN" | python3 -m json.tool

# 6. Get not found
echo "--- 6. Get not found ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH "$BASE_URL/redirects/notfound-xyz.com")
[ "$code" = "404" ] && pass "GET /redirects/notfound-xyz.com → 404" || fail "expected 404, got $code"

# 7. Invalid domain
echo "--- 7. Invalid domain ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH "$BASE_URL/redirects/not_a_domain!")
[ "$code" = "400" ] && pass "GET invalid domain → 400" || fail "expected 400, got $code"

# 8. Delete
echo "--- 8. Delete ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH -X DELETE "$BASE_URL/redirects/$DOMAIN")
[ "$code" = "204" ] && pass "DELETE /redirects/$DOMAIN → 204" || fail "expected 204, got $code"

# 9. Get after delete → 404
echo "--- 9. Get after delete ---"
code=$(curl -s -o /dev/null -w "%{http_code}" $AUTH "$BASE_URL/redirects/$DOMAIN")
[ "$code" = "404" ] && pass "GET after delete → 404" || fail "expected 404, got $code"

echo ""
echo "=== All tests passed ==="
