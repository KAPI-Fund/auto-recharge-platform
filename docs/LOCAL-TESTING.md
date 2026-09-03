# 本地链路测试

## 先说明

`/recharge` 已包含完整的“购买 CDK → 兑换 CDK → Worker 自动开通”链路。平台购买 debug 只绕过我们自己的 Stripe 收款并直接发放 CDK；CDK 兑换后，Worker 仍会完整执行原版 Node/Playwright 流程，包括目标站 Stripe 支付。

原 `KC-PAY-GPT` 的公开页面本身是 CDK 兑换页，CDK 支持后台生成/导入/出库；新系统的客户购买订单由 Go API 即时生成唯一 CDK，不依赖预先导入的 CDK 库存。原 CDK 管理仍保留用于兼容和人工发码。

真实 Playwright 流程必须使用当前有效的 ChatGPT Session JSON，并且需要外网、可用代理和对应的 Checkout 页面。不要把真实 Session、卡号或 CVC 写入仓库、日志或聊天记录。

## 1. 启动本地服务

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform
POSTGRES_PORT=15432 REDIS_PORT=16379 API_PORT=28080 WEB_PORT=23000 docker compose up -d --build
```

前端源码位于 `frontend`；Go API 和独立 Worker 分别位于 `backend/api`、`backend/worker`。公共 Compose、Dockerfile、脚本和文档仍位于项目根目录。

访问：

- 前台：`http://127.0.0.1:23000/recharge`
- 后台：`http://127.0.0.1:23000/admin`
- API：`http://127.0.0.1:28080/healthz`

### 1.1 Worker 使用 Docker，Go API 本地调试

Worker 与 Go API 是独立进程。需要调试 Go API 时，可以只把 PostgreSQL、Redis、Worker 放进 Docker，API 在宿主机启动：

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform
POSTGRES_PORT=15432 REDIS_PORT=16379 docker compose \
  -f docker-compose.yml \
  -f docker-compose.worker-local-api.yml \
  up -d --build postgres redis worker
```

另开终端启动本地 Go API：

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform/backend/api
HTTP_ADDR=:28080 \
DATABASE_URL='postgres://recharge:recharge@127.0.0.1:15432/recharge?sslmode=disable' \
REDIS_URL='redis://127.0.0.1:16379/0' \
ADMIN_API_TOKEN=dev-admin-token \
WORKER_API_TOKEN=dev-worker-token \
BROWSER_POOL_CONTROL_URL='http://127.0.0.1:8091' \
BROWSER_POOL_CONTROL_TOKEN=dev-worker-token \
PUBLIC_BASE_URL='http://127.0.0.1:23000' \
go run ./cmd/server
```

该模式下 Worker 容器通过 `host.docker.internal:28080` 回调本地 API，API 通过 `127.0.0.1:8091` 管理容器内浏览器池。两边必须使用相同的 `WORKER_API_TOKEN`。

## 1.1 自动化回归检查

Go API 和 Worker 的单元/契约测试：

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform/backend/api
go test ./...

cd /Users/ljq/projects/payment-kit/auto-recharge-platform/backend/worker
npm test
```

服务启动后，可执行项目内 smoke 测试：

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform
./scripts/local-smoke.sh
```

该脚本会在 `STORE_DEBUG_MODE=1` 时创建一笔唯一的调试购买订单，用于验证“发 CDK → 查询订单 → 验证 CDK”；它不会提交充值任务，不会打开目标站支付。若只检查服务和页面，不创建订单：

```bash
RUN_PURCHASE_SMOKE=0 ./scripts/local-smoke.sh
```

后台业务完整旁路测试（使用 React 后台实际调用的兼容接口）:

```bash
./scripts/admin-integrity-smoke.sh

# Go -> Redis -> Worker -> Go 的真实本地 dry_run 链路
./scripts/worker-flow-e2e.sh
```

真实浏览器 E2E（登录 React 后台，逐个打开后台路由，并通过售卡商品页面验证 Go API 写入与上下架持久化）:

```bash
E2E_ADMIN_EMAIL=admin@example.com \
E2E_ADMIN_PASSWORD=admin123 \
node ./scripts/admin-e2e.mjs

# 前台购买、发 CDK、验证 CDK、查询状态的真实浏览器 E2E
node ./scripts/public-e2e.mjs

# 公开参考页路由、内部跳转、hash/标签筛选、移动菜单和本地视频资源 E2E
node ./scripts/public-reference-pages-e2e.mjs
```

E2E 默认只使用本地测试管理员账号，创建一个唯一的下架商品后再发布、下架；不会调用 Stripe 或提交充值任务。每个 `/legacy-api/*`、`/platform-api/*` 响应都必须带 `X-Trace-ID`，任何 Go 端 5xx、页面错误或 `[object Object]` 都会使测试失败。

并发准入使用 Redis 做带 TTL 的前置槽位，PostgreSQL 事务做最终裁决。任务创建、CDK 状态变更、Worker 租约、心跳、终态回收和 Redis 槽位释放必须一起验证。真实 PostgreSQL/Redis HTTP 闭环测试默认跳过，避免普通单元测试碰业务库；使用独立测试库运行：

```bash
cd /Users/ljq/projects/payment-kit/auto-recharge-platform/backend/api
RECHARGE_TEST_DATABASE_URL='postgres://<user>:<password>@127.0.0.1:<postgres-port>/<test-database>?sslmode=disable' \
RECHARGE_TEST_REDIS_URL='redis://127.0.0.1:<redis-port>/0' \
go test ./internal/httpapi ./internal/queue \
  -run 'TestRechargeConcurrencyExternalHTTPFlow|TestRedisAdmissionLeaseSupportsLimitRefreshReleaseAndExpiry' \
  -count=1 -v
```

该测试通过真实 TCP HTTP 请求模拟两个同时兑换请求，断言一个成功、一个 `429`；然后模拟 Worker claim/heartbeat/完成、排队超时回收、旧租约更新被拒绝，并检查 PostgreSQL 中的任务/CDK 状态和 Redis 槽位一致。测试数据使用随机队列和 CDK，结束后自动清理。

该检查会覆盖管理员登录、概览实时指标、代理池增改删/检测、免税地址增改删、银行卡加密导入/查询/删除、CDK 生成/出库/删除、Session 列表与详情、账单查询与 CSV 导出、任务/自动化任务、运行日志、登录日志、截图/录像索引、浏览器池控制、支付链接参数校验、续费参数校验和系统配置读写。测试使用唯一的临时数据；地址删除遵循原业务语义，只会将地址标记为 inactive，不会物理删除历史记录。

原版接口路由契约测试位于 `backend/api/internal/httpapi/route_contract_test.go`，会对照 KC-PAY-GPT 原版接口表检查公开、登录、后台、二级权限、CDK、卡池、账单和兑换路由是否缺失。

## 2. 模拟购买 CDK（平台 debug）

设置：

```dotenv
STORE_DEBUG_MODE=1
```

打开 `/recharge`，选择套餐并点击“购买并获取 CDK”。订单会直接标记为已支付并发放一次性 CDK，不调用平台 Stripe。随后使用页面中的 CDK 验证并提交完整 Session JSON。

后台手工生成/导入 CDK 的原版管理能力仍然保留，适合兼容性和人工发码测试，不参与公开购买订单的自动发码。

## 3. 只验证队列链路

在 `.env` 设置：

```dotenv
DEFAULT_RECHARGE_MODE=dry_run
PAYMENT_TEST_MODE=
```

重启 API 和 Worker 后，前台提交任务即可验证：

`Next.js -> Go API -> PostgreSQL -> Redis -> Worker -> Go API -> PostgreSQL`

这个模式仍然要求 Session 使用合法 JWT 格式，但不会访问 ChatGPT 或 Stripe。

## 4. Worker 调试：完整执行目标站支付

Worker 调试不跳过支付，也不改变目标站业务。只使用 `LEGACY_HEADFUL` 控制原版 Playwright 浏览器是否有头：

```dotenv
DEFAULT_RECHARGE_MODE=browser
LEGACY_HEADFUL=1
```

平台购买仍由 `STORE_DEBUG_MODE=1` 控制，和 Worker 浏览器模式是两个独立开关。Docker 容器通常没有桌面窗口，即使有头也应通过截图、录像和运行日志检查页面。

## 5. 关闭平台购买 debug，测试平台 Stripe

设置：

```dotenv
STORE_DEBUG_MODE=0
STRIPE_SECRET_KEY=sk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
PUBLIC_BASE_URL=http://localhost:23000
STRIPE_SUCCESS_URL=http://localhost:23000/recharge?payment=success\&order_id={ORDER_ID}
```

Stripe Dashboard 的 Webhook 指向：

```text
POST http://localhost:28080/api/v1/store/webhooks/stripe
```

至少监听 `checkout.session.completed`。购买页会跳转 Stripe，支付完成后返回 `/recharge?order_id=...`，页面轮询 Go API，Webhook 验证成功后发放 CDK。没有 Stripe 测试密钥时，平台正式购买会明确返回“平台 Stripe 尚未配置”。

## 6. 测试 Worker 目标站拒付

确认平台购买已获得 CDK 后，再设置：

```bash
POSTGRES_PORT=15432 REDIS_PORT=16379 API_PORT=28080 WEB_PORT=23000 docker compose build worker
POSTGRES_PORT=15432 REDIS_PORT=16379 API_PORT=28080 WEB_PORT=23000 docker compose up -d worker
```

Worker 默认无头执行真实目标站 Stripe Checkout。导入一张专门的无效卡或没有余额的测试卡，让目标站 Stripe 自然返回拒付。原版拒付业务语义会执行：

- 写入失败账单。
- 将当前卡片标记为报废。
- 任务进入人工处理或失败分支。

因此只能使用专门导入的测试卡，不能使用生产卡池。测试结束后在后台删除这些测试卡，或执行：

```sql
SELECT id, last4, status, active FROM card_assets ORDER BY created_at DESC;
```

为了不依赖 Stripe 的具体拒付响应，也可以临时设置 `PAYMENT_TEST_MODE=decline`。这个开关不会提交真实支付，但会直接走同一套拒付、失败账单和卡片报废逻辑；它只适合自动化回归，不代表真实 Stripe 已拒付。

## 7. 按 Trace ID 排查

任务创建响应和前台任务卡片会显示 `trace_id`。同一个 ID 会贯穿：

- API 请求响应头 `X-Trace-ID`
- PostgreSQL `recharge_tasks.trace_id`
- Redis 任务消息 `traceId`
- Worker 到 API 的 `X-Trace-ID`
- `runtime_logs.trace_id`
- `billing_records.trace_id`
- Playwright 子进程的 `TRACE_ID`

查询示例：

```sql
SELECT trace_id, id, job_key, status, progress, message, created_at, updated_at
FROM recharge_tasks
WHERE trace_id = 'trace_xxx'
ORDER BY created_at;

SELECT trace_id, job_key, level, source, text, created_at
FROM runtime_logs
WHERE trace_id = 'trace_xxx'
ORDER BY created_at;

SELECT trace_id, task_id, status, error_code, error_message, created_at
FROM billing_records
WHERE trace_id = 'trace_xxx'
ORDER BY created_at;
```

结束测试后恢复：

```dotenv
PAYMENT_TEST_MODE=
DEFAULT_RECHARGE_MODE=browser
STORE_DEBUG_MODE=1
```

## 8. hCaptcha 求解链路

Worker 保留 `KC-PAY-GPT` 原版 hCaptcha 求解器：Node 负责桥接，Python Solver 通过 CDP 连接 Worker 浏览器，使用 CLIP、OpenCV 和 VLM 完成图片题识别。第三方打码平台是原版支持的兼容能力，用于被动 hCaptcha 等场景；后台可单独测试平台连通性。

镜像会安装 Python、CPU 版 PyTorch、Transformers、OpenCV、numpy、Pillow、requests 和 Python Playwright。健康检查只代表这些运行依赖已就绪；未配置 VLM、打码平台 Key 或真实目标站验证页面时，不能据此声称真实 challenge 已成功识别。
