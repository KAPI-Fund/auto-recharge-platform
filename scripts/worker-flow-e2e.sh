#!/usr/bin/env bash
set -euo pipefail

# Cross-process acceptance test: Go API -> PostgreSQL -> Redis -> Worker -> Go API.
# It intentionally leaves one used dry-run CDK/task as an auditable local record.
API_URL="${API_URL:-http://127.0.0.1:28080}"
WORKER_URL="${WORKER_URL:-http://127.0.0.1:8091}"
WORKER_TOKEN="${WORKER_TOKEN:-dev-worker-token}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin123}"

trace_id="worker-e2e-$(date +%s)-${RANDOM}"
json_headers=(-H 'Content-Type: application/json' -H "X-Trace-ID: $trace_id")

health="$(curl -sS "$API_URL/healthz")"
jq -e '.ok == true and .redis == "up"' <<<"$health" >/dev/null
worker_health="$(curl -sS "$WORKER_URL/health" -H "X-Worker-Token: $WORKER_TOKEN")"
jq -e '.success == true and .pool.initialized == true' <<<"$worker_health" >/dev/null

login="$(curl -sS "$API_URL/api/admin/login" "${json_headers[@]}" --data "$(jq -cn --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')")"
admin_token="$(jq -r '.token' <<<"$login")"
[[ -n "$admin_token" && "$admin_token" != "null" ]]

cdk_response="$(curl -sS -X POST "$API_URL/api/v1/admin/cdks" \
  -H "Authorization: Bearer $admin_token" "${json_headers[@]}" \
  --data '{"planCode":"plus","quantity":1,"prefix":"E2E"}')"
cdk_code="$(jq -r '.cdks[0].code' <<<"$cdk_response")"
[[ -n "$cdk_code" && "$cdk_code" != "null" ]]

session="$(node -e 'const b=(value)=>Buffer.from(JSON.stringify(value)).toString("base64url"); console.log(`${b({typ:"JWT",alg:"RS256"})}.${b({iss:"https://auth.openai.com",aud:["https://api.openai.com/v1"],"https://api.openai.com/auth":{chatgpt_account_id:"acct-local-e2e",chatgpt_user_id:"user-local-e2e"},scp:["model.request"],exp:Math.floor(Date.now()/1000)+3600})}.local-e2e`)')"
task_response="$(curl -sS -X POST "$API_URL/api/v1/recharge/tasks" "${json_headers[@]}" \
  --data "$(jq -cn --arg code "$cdk_code" --arg session "$session" '{code:$code,session:$session,mode:"dry_run"}')")"
task_id="$(jq -r '.task.id' <<<"$task_response")"
[[ -n "$task_id" && "$task_id" != "null" ]]

final_task=""
for _ in $(seq 1 60); do
  final_task="$(curl -sS "$API_URL/api/v1/recharge/tasks/$task_id")"
  task_status="$(jq -r '.task.status' <<<"$final_task")"
  case "$task_status" in
    succeeded|failed|manual) break ;;
  esac
  sleep 0.5
done

jq -e \
  '.task.status == "succeeded" and .task.progress == 100 and (.task.message | type) == "string"' \
  <<<"$final_task" >/dev/null

# The buyer endpoint intentionally hides trace IDs, raw output, and CDK
# internals. Verify those audit fields through the authenticated admin API.
admin_tasks="$(curl -sS "$API_URL/api/v1/admin/tasks?limit=200" \
  -H "Authorization: Bearer $admin_token" -H "X-Trace-ID: $trace_id")"
jq -e --arg task_id "$task_id" --arg trace "$trace_id" \
  'any(.tasks[]; .id == $task_id and .status == "succeeded" and .progress == 100 and .traceId == $trace and .cdk.status == "used")' \
  <<<"$admin_tasks" >/dev/null

runtime_logs="$(curl -sS "$API_URL/api/admin/runtime-logs?limit=2000&tail=1" \
  -H "Authorization: Bearer $admin_token" -H "X-Trace-ID: $trace_id")"
jq -e --arg trace "$trace_id" \
  '([.logs[] | select(.traceId == $trace)] | length) >= 5' \
  <<<"$runtime_logs" >/dev/null

printf 'Worker flow E2E passed: task=%s cdk=%s trace=%s runtime_logs=%s\n' \
  "$task_id" "$cdk_code" "$trace_id" "$(jq --arg trace "$trace_id" '[.logs[] | select(.traceId == $trace)] | length' <<<"$runtime_logs")"
