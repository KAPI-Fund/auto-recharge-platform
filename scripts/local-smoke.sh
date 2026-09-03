#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:28080}"
WEB_URL="${WEB_URL:-http://127.0.0.1:23000}"
WORKER_URL="${WORKER_URL:-http://127.0.0.1:8091}"
WORKER_TOKEN="${WORKER_TOKEN:-dev-worker-token}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin123}"
RUN_PURCHASE_SMOKE="${RUN_PURCHASE_SMOKE:-1}"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/auto-recharge-smoke.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

request() {
  local name="$1"
  local method="$2"
  local url="$3"
  local expected="$4"
  local body_file="$tmp_dir/${name}.body"
  local header_file="$tmp_dir/${name}.headers"
  shift 4

  local status
  status="$(curl -sS -D "$header_file" -o "$body_file" -w '%{http_code}' -X "$method" "$url" "$@")"
  if [[ "$status" != "$expected" ]]; then
    printf 'FAIL %-28s expected HTTP %s, got %s\n' "$name" "$expected" "$status" >&2
    sed -n '1,5p' "$body_file" >&2
    exit 1
  fi
  printf 'PASS %-28s HTTP %s\n' "$name" "$status" >&2
  printf '%s' "$body_file"
}

assert_json() {
  local name="$1"
  local file="$2"
  shift 2
  local expression="${!#}"
  local jq_argument_count=$(( $# - 1 ))
  local jq_arguments=()
  if (( jq_argument_count > 0 )); then
    jq_arguments=("${@:1:jq_argument_count}")
  fi
  if (( jq_argument_count > 0 )); then
    jq_command=(jq -e "${jq_arguments[@]}" "$expression" "$file")
  else
    jq_command=(jq -e "$expression" "$file")
  fi
  if ! "${jq_command[@]}" >/dev/null; then
    printf 'FAIL %-28s JSON assertion: %s\n' "$name" "$expression" >&2
    jq . "$file" >&2 || sed -n '1,80p' "$file" >&2
    exit 1
  fi
  printf 'PASS %-28s JSON assertion\n' "$name"
}

assert_header() {
  local name="$1"
  local file="$2"
  local wanted="$3"
  local expected="$4"
  local actual
  actual="$(awk -v wanted="$wanted" 'index($0, ":") { key=$0; sub(/:.*/, "", key); value=$0; sub(/^[^:]*:[[:space:]]*/, "", value); sub(/[[:space:]]*\r$/, "", value); if (tolower(key) == tolower(wanted)) print value }' "$file" | tail -n 1)"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL %-28s header %s: expected %s, got %s\n' "$name" "$wanted" "$expected" "$actual" >&2
    sed -n '1,40p' "$file" >&2
    exit 1
  fi
  printf 'PASS %-28s header %s\n' "$name" "$wanted"
}

health="$(request health GET "$API_URL/healthz" 200)"
assert_json health "$health" '.ok == true and .redis == "up"'

paths="$(request public-admin-paths GET "$API_URL/api/public/admin-paths" 200)"
assert_json public-admin-paths "$paths" '.success == true and .loginPath == "admin-login" and .panelPath == "admin"'

plans="$(request plans GET "$API_URL/api/v1/plans" 200)"
assert_json plans "$plans" '(.plans | length) > 0 and any(.plans[]; .code == "plus")'

unauth="$(request admin-unauthorized GET "$API_URL/api/v1/admin/overview" 401)"
assert_json admin-unauthorized "$unauth" '.message == "需要管理员授权"'

login="$(request admin-login POST "$API_URL/api/admin/login" 200 \
  -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')")"
assert_json admin-login "$login" '.success == true and .requires2fa == false and (.token | type) == "string"'
admin_token="$(jq -r '.token' "$login")"

admin_data="$(request admin-data GET "$API_URL/api/admin/data" 200 -H "X-Admin-Token: $admin_token")"
assert_json admin-data "$admin_data" '.success == true'

worker_health="$(request worker-health GET "$WORKER_URL/health" 200 -H "X-Worker-Token: $WORKER_TOKEN")"
assert_json worker-health "$worker_health" '.success == true and .pool != null'

for route in / /recharge /subscription /admin-login /admin; do
  route_name="web-${route#/}"
  route_name="${route_name//\//-}"
  page="$(request "$route_name" GET "$WEB_URL$route" 200)"
  [[ -s "$page" ]] || { printf 'FAIL %s empty response\n' "$route" >&2; exit 1; }
done

for route in /index.html /subscription.html /admin.html /admin-login.html /recharge.html; do
  route_name="html-${route#/}"
  route_name="${route_name//\//-}"
  request "$route_name" GET "$WEB_URL$route" 404 >/dev/null
done

if [[ "$RUN_PURCHASE_SMOKE" == "1" ]]; then
  suffix="$(date +%s)"
  email="smoke-${suffix}@example.com"
  phone="138${suffix: -8}"
  trace_id="trace_smoke_${suffix}"
  purchase="$(request store-order-debug POST "$API_URL/api/v1/store/orders" 201 \
    -H 'Content-Type: application/json' \
    -H "X-Trace-ID: $trace_id" \
    --data "$(jq -cn --arg email "$email" --arg phone "$phone" '{planCode:"plus",email:$email,phoneCountryCode:"+86",phoneNumber:$phone}')")"
  assert_json store-order-debug "$purchase" '.debug == true and .order.status == "paid" and (.order.cdkCode | length) > 0 and ((.order | has("traceId")) | not)'
  assert_header store-order-debug "$tmp_dir/store-order-debug.headers" "X-Trace-ID" "$trace_id"

  order_id="$(jq -r '.order.id' "$purchase")"
  order_no="$(jq -r '.order.orderNo' "$purchase")"
  cdk="$(jq -r '.order.cdkCode' "$purchase")"
  order="$(request store-order-get GET "$API_URL/api/v1/store/orders/$order_id" 200)"
  assert_json store-order-get "$order" '.order.id == $order_id and .order.status == "paid"' --arg order_id "$order_id"

  query_order="$(request store-order-query-number POST "$API_URL/api/v1/store/orders/query" 200 \
    -H 'Content-Type: application/json' --data "$(jq -cn --arg value "$order_no" '{orderNo:$value}')")"
  assert_json store-order-query-number "$query_order" '.orders | length == 1 and .[0].cdkCode == $cdk' --arg cdk "$cdk"

  query_email="$(request store-order-query-email POST "$API_URL/api/v1/store/orders/query" 200 \
    -H 'Content-Type: application/json' --data "$(jq -cn --arg value "$email" '{email:$value}')")"
  assert_json store-order-query-email "$query_email" '.orders | any(.[]; .id == $order_id)' --arg order_id "$order_id"

  query_phone="$(request store-order-query-phone POST "$API_URL/api/v1/store/orders/query" 200 \
    -H 'Content-Type: application/json' --data "$(jq -cn --arg value "$phone" '{phoneCountryCode:"+86",phoneNumber:$value}')")"
  assert_json store-order-query-phone "$query_phone" '.orders | any(.[]; .id == $order_id)' --arg order_id "$order_id"

  verify="$(request cdk-verify POST "$API_URL/api/v1/recharge/verify" 200 \
    -H 'Content-Type: application/json' --data "$(jq -cn --arg code "$cdk" '{code:$code}')")"
  assert_json cdk-verify "$verify" '.valid == true and .cdk.code != null'

  legacy_verify="$(request legacy-cdk-verify POST "$API_URL/api/verify-cdk" 200 \
    -H 'Content-Type: application/json' --data "$(jq -cn --arg code "$cdk" '{cdk:$code}')")"
  assert_json legacy-cdk-verify "$legacy_verify" '.success == true and .data.plan_type == "plus"'
fi

printf '\nLocal smoke test passed. No recharge task was submitted.\n'
