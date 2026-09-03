#!/usr/bin/env bash
set -euo pipefail

# End-to-end admin API audit. It intentionally uses the compatibility routes
# consumed by the React admin panel, so a passing result covers the real UI
# contract, persistence, and the Worker control boundary.
API_URL="${API_URL:-http://127.0.0.1:28080}"
WORKER_URL="${WORKER_URL:-http://127.0.0.1:8091}"
WORKER_TOKEN="${WORKER_TOKEN:-dev-worker-token}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin123}"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/auto-recharge-admin-audit.XXXXXX")"
admin_token=""
proxy_id=""
address_id=""
card_id=""
cdk_code=""
cleanup() {
  if [[ -n "$admin_token" ]]; then
    [[ -z "$proxy_id" ]] || curl -sS -o /dev/null -X DELETE "$API_URL/api/admin/proxies/$proxy_id" -H "X-Admin-Token: $admin_token" || true
    [[ -z "$address_id" ]] || curl -sS -o /dev/null -X DELETE "$API_URL/api/admin/addresses/$address_id" -H "X-Admin-Token: $admin_token" || true
    [[ -z "$card_id" ]] || curl -sS -o /dev/null -X DELETE "$API_URL/api/admin/cards/$card_id" -H "X-Admin-Token: $admin_token" || true
    [[ -z "$cdk_code" ]] || curl -sS -o /dev/null -X DELETE "$API_URL/api/admin/cdks/$cdk_code" -H "X-Admin-Token: $admin_token" || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

pass_count=0
fail() {
  printf 'FAIL %s\n' "$1" >&2
  exit 1
}
pass() {
  pass_count=$((pass_count + 1))
  printf 'PASS %s\n' "$1"
}
request() {
  local name="$1" method="$2" url="$3" expected="$4"
  shift 4
  local body="$tmp_dir/${name}.body"
  local headers="$tmp_dir/${name}.headers"
  local status
  status="$(curl -sS -D "$headers" -o "$body" -w '%{http_code}' -X "$method" "$url" "$@")" || fail "$name request"
  [[ "$status" == "$expected" ]] || {
    sed -n '1,80p' "$body" >&2
    fail "$name expected HTTP $expected, got $status"
  }
  [[ -s "$body" ]] || fail "$name empty response"
  printf '%s' "$body"
}
assert_json() {
  local name="$1" body="$2"
  shift 2
  local expression="${!#}"
  local jq_argument_count=$(( $# - 1 ))
  local jq_arguments=()
  if (( jq_argument_count > 0 )); then
    jq_arguments=("${@:1:jq_argument_count}")
  fi
  if (( jq_argument_count > 0 )); then
    jq -e "${jq_arguments[@]}" "$expression" "$body" >/dev/null
  else
    jq -e "$expression" "$body" >/dev/null
  fi || {
    jq . "$body" >&2 || sed -n '1,80p' "$body" >&2
    fail "$name JSON assertion"
  }
  pass "$name"
}

login="$(request admin-login POST "$API_URL/api/admin/login" 200 \
  -H 'Content-Type: application/json' \
  -H 'X-Trace-ID: audit-login' \
  --data "$(jq -cn --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')")"
assert_json admin-login "$login" '.success == true and (.token | type) == "string"'
admin_token="$(jq -r '.token' "$login")"
auth=(-H "X-Admin-Token: $admin_token")
trace=(-H 'X-Trace-ID: audit-admin')
json=(-H 'Content-Type: application/json')

health="$(request worker-health GET "$WORKER_URL/health" 200 -H "X-Worker-Token: $WORKER_TOKEN")"
assert_json worker-health "$health" '.success == true and .pool.initialized == true'

overview="$(request overview GET "$API_URL/api/admin/data" 200 "${auth[@]}" "${trace[@]}")"
assert_json overview "$overview" '.success == true and .stats != null and .runtime.system != null'

proxy_seed="http://audit:bad@127.0.0.1:1/audit-$(date +%s%N)"
proxies="$(request proxy-create POST "$API_URL/api/admin/proxies" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data "$(jq -cn --arg value "$proxy_seed" '{proxies:$value}')")"
assert_json proxy-create "$proxies" '.success == true and .added == 1'
proxy_id="$(jq -r '.ids[0]' "$proxies")"
proxy_list="$(request proxy-list GET "$API_URL/api/admin/proxies" 200 "${auth[@]}" "${trace[@]}")"
assert_json proxy-list "$proxy_list" --arg id "$proxy_id" '.success == true and any(.proxies[]; (.id == $id and (.proxy_url_masked | type) == "string"))'
proxy_update="$(request proxy-disable PUT "$API_URL/api/admin/proxies/$proxy_id" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"is_active":false}')"
assert_json proxy-disable "$proxy_update" '.success == true and .is_active == false'
proxy_test="$(request proxy-test POST "$API_URL/api/admin/proxies/$proxy_id/test" 200 "${auth[@]}" "${trace[@]}")"
assert_json proxy-test "$proxy_test" '.success == true and (.ok == false or .ok == true) and (.latencyMs | type) == "number"'

address="$(request address-create POST "$API_URL/api/admin/addresses" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"region":"US","line1":"1 Audit Lane","city":"Portland","state":"Oregon","postal_code":"97201","country":"US"}')"
assert_json address-create "$address" '.success == true and (.address.id | type) == "string"'
address_id="$(jq -r '.address.id' "$address")"
address_list="$(request address-list GET "$API_URL/api/admin/addresses?region=US" 200 "${auth[@]}" "${trace[@]}")"
assert_json address-list "$address_list" --arg id "$address_id" '.success == true and any(.addresses[]; .id == $id)'
address_update="$(request address-update PUT "$API_URL/api/admin/addresses/$address_id" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"line1":"2 Audit Lane"}')"
assert_json address-update "$address_update" '.success == true'
address_delete="$(request address-delete DELETE "$API_URL/api/admin/addresses/$address_id" 200 "${auth[@]}" "${trace[@]}")"
assert_json address-delete "$address_delete" '.success == true'

card_tag="$(date +%s)"
card="$(request card-create POST "$API_URL/api/admin/cards/import" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data "$(jq -cn --arg number "424242424242${card_tag: -4}" '{cards:[{number:$number,expiry:"12/30",cvc:"123",holder:"Audit User"}]}')")"
assert_json card-create "$card" '.success == true and .created == 1'
card_list="$(request card-list GET "$API_URL/api/admin/cards" 200 "${auth[@]}" "${trace[@]}")"
assert_json card-list "$card_list" --arg last4 "${card_tag: -4}" '.success == true and any(.cards[]; .last4 == $last4)'
card_id="$(jq -r --arg last4 "${card_tag: -4}" '.cards[] | select(.last4 == $last4) | .id' "$card_list" | head -n 1)"
[[ -n "$card_id" ]] || fail "card id lookup"
card_delete="$(request card-delete DELETE "$API_URL/api/admin/cards/$card_id" 200 "${auth[@]}" "${trace[@]}")"
assert_json card-delete "$card_delete" '.success == true'

cdk="$(request cdk-create POST "$API_URL/api/admin/cdks/generate" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"count":1,"plan_type":"plus","prefix":"AUDIT"}')"
assert_json cdk-create "$cdk" '.success == true and (.cdks | length) == 1'
cdk_code="$(jq -r '.cdks[0]' "$cdk")"
cdk_list="$(request cdk-list GET "$API_URL/api/admin/cdks" 200 "${auth[@]}" "${trace[@]}")"
assert_json cdk-list "$cdk_list" --arg code "$cdk_code" '.success == true and (.cdks | type) == "array" and any(.cdks[]; .code == $code)'
cdk_ship="$(request cdk-ship POST "$API_URL/api/admin/cdks/$cdk_code/ship" 200 "${auth[@]}" "${trace[@]}")"
assert_json cdk-ship "$cdk_ship" '.success == true'
cdk_delete="$(request cdk-delete DELETE "$API_URL/api/admin/cdks/$cdk_code" 200 "${auth[@]}" "${trace[@]}")"
assert_json cdk-delete "$cdk_delete" '.success == true'

session_list="$(request session-list GET "$API_URL/api/admin/sessions?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json session-list "$session_list" 'type == "array"'
session_job="$(jq -r '.[0].job_key // empty' "$session_list")"
if [[ -n "$session_job" ]]; then
  session_detail="$(request session-detail GET "$API_URL/api/admin/sessions/$session_job" 200 "${auth[@]}" "${trace[@]}")"
  assert_json session-detail "$session_detail" --arg job "$session_job" '.success == true and .session.job_key == $job and (.session.session_payload | type) == "string"'
fi

billing="$(request billing-list GET "$API_URL/api/admin/billing?page=1&page_size=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json billing-list "$billing" '.success == true and (.records | type) == "array"'
billing_export="$(request billing-export GET "$API_URL/api/admin/billing/export" 200 "${auth[@]}" "${trace[@]}")"
grep -q 'Stripe Session ID' "$billing_export" || fail billing-export-content
pass billing-export-content

task_logs="$(request task-logs GET "$API_URL/api/admin/task-logs?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json task-logs "$task_logs" '.success == true and (.logs | type) == "array"'
assert_json automation-tasks "$task_logs" '.success == true and all(.logs[]; .automation != null)'
runtime_logs="$(request runtime-logs GET "$API_URL/api/admin/runtime-logs?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json runtime-logs "$runtime_logs" '.success == true and (.logs | type) == "array"'
login_logs="$(request login-logs GET "$API_URL/api/admin/login-logs?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json login-logs "$login_logs" '.success == true and (.logs | type) == "array" and any(.logs[]; .traceId == "audit-login")'

screenshots="$(request screenshots-index GET "$API_URL/api/admin/screenshots?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json screenshots-index "$screenshots" '.success == true and (.items | type) == "array"'
videos="$(request videos-index GET "$API_URL/api/admin/video?limit=5" 200 "${auth[@]}" "${trace[@]}")"
assert_json videos-index "$videos" '.success == true and (.items | type) == "array"'
media_path="$(jq -r '.items[0].path // empty' "$screenshots")"
if [[ -n "$media_path" ]]; then
  media_file="$(request screenshot-file GET "$API_URL/api/admin/screenshots?path=$(jq -rn --arg value "$media_path" '$value|@uri')" 200 "${auth[@]}" "${trace[@]}")"
  file -b "$media_file" | grep -Eiq 'PNG image|Web/P image' || fail screenshot-file-content
  pass screenshot-file-content
fi

pool="$(request browser-pool GET "$API_URL/api/admin/browser-pool" 200 "${auth[@]}" "${trace[@]}")"
assert_json browser-pool "$pool" '.success == true and .pool != null and .system != null and (.enabled == .pool.enabled)'
pool_reload="$(request browser-pool-reload POST "$API_URL/api/admin/browser-pool/reload" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"size":2}')"
assert_json browser-pool-reload "$pool_reload" '.success == true and .pool != null'

checkout_plans="$(request checkout-plans GET "$API_URL/api/admin/checkout/plans" 200 "${auth[@]}" "${trace[@]}")"
assert_json checkout-plans "$checkout_plans" '.success == true and .plans.plus != null'
checkout_invalid="$(request checkout-invalid POST "$API_URL/api/admin/checkout/generate" 400 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"session":"not-a-session"}')"
assert_json checkout-invalid "$checkout_invalid" '.success == false and ((.error // .message) | type) == "string"'
renewal_invalid="$(request renewal-invalid POST "$API_URL/api/public/subscription/check" 400 "${trace[@]}" "${json[@]}" --data '{"session":"not-a-session"}')"
assert_json renewal-invalid "$renewal_invalid" '.success == false and ((.error // .message) | type) == "string"'

config_before="$(request config-before GET "$API_URL/api/admin/config" 200 "${auth[@]}" "${trace[@]}")"
assert_json config-before "$config_before" '.config != null'
config="$(request config-save POST "$API_URL/api/admin/config" 200 "${auth[@]}" "${trace[@]}" "${json[@]}" --data '{"max_concurrent_activations":1,"maintenance_mode":false}')"
assert_json config-save "$config" '.success == true'
config_after="$(request config-after GET "$API_URL/api/admin/config" 200 "${auth[@]}" "${trace[@]}")"
assert_json config-after "$config_after" '.config.max_concurrent_activations == "1"'

proxy_delete="$(request proxy-delete DELETE "$API_URL/api/admin/proxies/$proxy_id" 200 "${auth[@]}" "${trace[@]}")"
assert_json proxy-delete "$proxy_delete" '.success == true'

printf '\nAdmin integrity smoke passed: %d assertions.\n' "$pass_count"
