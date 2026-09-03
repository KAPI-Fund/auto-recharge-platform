# KC Recharge Platform

基于 `KC-PAY-GPT` 业务模型重建的自动充值平台。旧目录中的 Payment Kit、SQLite、单体 Node 服务和旧 Git 历史已移除；当前目录是新的拆分式实现。

本仓库只提交可复现构建所需的源码、静态生成输入/SDK、部署文件、测试脚本和说明文档。真实密钥、运行数据、浏览器 Profile、截图/录像、抓包和依赖目录均不提交。

## 目录结构

```text
auto-recharge-platform/
├── frontend/                  # Next.js + TypeScript 页面和 UI
├── backend/api/               # Go API、数据库模型、业务服务和 HTTP 接口
├── backend/worker/            # Node/Playwright Worker 与原版自动化逻辑
├── backend/protocol-worker/   # 可选的 Go 协议 Worker
├── scripts/                   # 本地 smoke、旁路和 E2E 测试
├── docs/                      # 本地测试和运维说明
├── Dockerfile.web
├── docker-compose*.yml
├── .env.example
└── .env.production.example
```

`frontend` 只负责页面展示、路由和调用 API；订单、CDK、卡池、Provider、任务、账单、配置和 Worker 通信都由 Go API 处理。Worker 是独立进程，通过 Redis 消费任务并回调 Go API；浏览器自动化的内部业务流程保留在 Worker 内。

## 技术栈

| 层 | 实现 |
| --- | --- |
| Web | Next.js 15、TypeScript、Tailwind CSS、Lucide、shadcn/ui 风格组件 |
| API | Go 1.25+、Gin、GORM |
| DB | PostgreSQL 16 |
| Queue | Redis list contract，任务状态由 API 持久化，结构按 Asynq 风格拆分 |
| Worker | Node 20+、Playwright |
| Deploy | Docker Compose |

## 业务映射

| KC-PAY-GPT | 新系统 |
| --- | --- |
| `public/index.html` 兑换 | Next.js `/recharge` |
| `admin.html` | Next.js `/admin/*` |
| CDK、卡池、任务、账单 | Go domain + `/api/v1/admin/*` |
| `product_activator`、`stripe-payment` | Node Worker 的 `browser` provider |
| 第三方代充 API | Node Worker 的 `upstream` provider |
| MySQL | PostgreSQL + GORM AutoMigrate |
| 单进程 Node | `web` / `api` / `worker` 三个服务 |

## 启动

执行 `cp .env.example .env`，然后执行 `docker compose up --build`。

地址：

- 前台：<http://localhost:3000/recharge>
- 后台：<http://localhost:3000/admin>
- API：<http://localhost:8080/healthz>

开发阶段也可以使用非默认端口，避免占用本机已有服务：

```bash
POSTGRES_PORT=15432 REDIS_PORT=16379 API_PORT=28080 WEB_PORT=23000 \
  docker compose up -d --build
```

默认执行模式为 `browser`，由拆分后的 Worker 启动原版 Playwright 自动化；后台 Token 默认是 `dev-admin-token`，部署前必须修改。需要只验证队列链路时可以显式设置 `DEFAULT_RECHARGE_MODE=dry_run`。

系统配置中的“邮件通知”由 Go API 使用 SMTP 发送。填写 SMTP 主机、端口、账号、密码和发件人后，先保存配置，再填写测试收件人发送测试邮件；购买成功和 CDK 兑换成功通知可分别开关。默认使用 587 端口 STARTTLS，SMTP 密码只写入后端，不会回显给前端。

## 执行模式

- `dry_run`：本地演示，不访问外部服务。
- `upstream`：Worker 调用 `UPSTREAM_BASE_URL + UPSTREAM_CREATE_PATH` 创建代充订单，再按 `UPSTREAM_STATUS_PATH` 轮询；默认兼容 `/pay` 和 `/tasks/:id`。
- `browser`：Worker 启动 `backend/worker/src/legacy/index.js`，保留原版 Session、Checkout、Stripe 卡池支付、hCaptcha、代理和截图/录像逻辑。

## 本地开发

1. 执行 `docker compose up -d postgres redis`。
2. 在 `backend/api` 执行 `go mod tidy && go run ./cmd/server`。
3. 在 `frontend` 执行 `yarn install && yarn dev`。
4. 在 `backend/worker` 执行 `npm install && npm start`。

Provider SDK 由各自维护的 OpenAPI JSON 静态生成，业务代码只依赖统一的 CardProvider 接口：

```bash
cd backend/api
go generate ./internal/cardpool/providers/airwallex/generated ./internal/cardpool/providers/stripeissuing/generated ./internal/cardpool/providers/photonpay/generated ./internal/cardpool/providers/dogpay/generated
go test ./internal/cardpool/...
```

生成入口、配置和输入文档分别位于各 Provider 的 `generated/` 目录。`client.gen.go` 不手工编辑；Provider-specific 请求字段只在对应 Adapter 中组装。

### 银行卡 Provider 配置

卡池配置保存在后台“系统配置”中，由 Go API 写入 PostgreSQL；Provider 适配器通过 `CardProviderRegistry` 读取，不在支付业务中判断供应商。卡池本身可在“银行卡池”管理，默认卡池的路由策略支持 `FIXED`、`PRIORITY`、`WEIGHTED` 和 `FAILOVER`。只有 Provider 技术故障或不可用时才允许 failover，商户拒付、余额不足和合规拒绝不会自动换供应商。

支持的配置字段包括：

- `card_pool_default_id`、`card_pool_routing`、`card_pool_default_provider`
- `card_provider_local_text_enabled`
- `card_provider_airwallex_enabled`、`airwallex_base_url`、`airwallex_primary_currency`、`airwallex_card_purpose`、`airwallex_card_type`、`airwallex_form_factor`、`airwallex_activate_on_issue`、`airwallex_cardholder_id`、`airwallex_webhook_tolerance_seconds`
- `card_provider_stripe_issuing_enabled`、`stripe_issuing_base_url`、`stripe_issuing_currency`、`stripe_issuing_cardholder_id`、`stripe_issuing_webhook_tolerance_seconds`
- `card_provider_photonpay_enabled`、`photonpay_base_url`、`photonpay_app_id`、`photonpay_app_secret`、`photonpay_card_bin`、`photonpay_cardholder_id`、`photonpay_card_type`、`photonpay_card_form_factor`、`photonpay_card_scheme`、`photonpay_primary_currency`、`photonpay_transaction_limit_type`、`photonpay_webhook_tolerance_seconds`
- `card_provider_dogpay_enabled`、`dogpay_base_url`、`dogpay_appid`、`dogpay_secret`、`dogpay_channel_id`、`dogpay_entity_id`、`dogpay_cardholder_id`、`dogpay_card_type`、`dogpay_budget_id`、`dogpay_velocity_amount_limit`、`dogpay_webhook_tolerance_seconds`

`airwallex_client_id`、`airwallex_api_key`、`airwallex_webhook_secret`、`stripe_issuing_secret_key`、`stripe_issuing_webhook_secret`、PhotonPay 的 App ID/App Secret/RSA 密钥、DogPay 的 App ID/App Secret/RSA 密钥和 Webhook Secret 属于敏感配置：数据库启用 `SESSION_ENCRYPTION_KEY` 时加密保存，后台只显示“已配置”状态，不回显原文。Provider Webhook 地址为 `/api/v1/webhooks/cards/airwallex`、`/api/v1/webhooks/cards/stripe`、`/api/v1/webhooks/cards/photonpay` 和 `/api/v1/webhooks/cards/dogpay`，各自使用对应签名校验和幂等事件记录。

PhotonPay 使用其官方 Open API Reference 中的 Issuing/VCC 接口，Sandbox 默认地址为 `https://x-api.sandbox.photontech.cc`，Production 默认地址为 `https://x-api.photonpay.com`。DogPay 使用官方 Card Issuing API，Sandbox 默认地址为 `https://sandbox-api-v2.dogpay.com`，Production 默认地址为 `https://api.dogpay.com`。切换到 Production 前必须先在 Provider 后台配置中替换 Base URL，并使用对应环境的凭证和签名密钥。

当前仓库内的 `openapi.json` 是适配器需要的供应商 API 子集输入；更新官方 API 字段时只修改对应 JSON，再运行上面的 `go generate`，禁止手工编辑生成的 `client.gen.go`。

## 临时生产调试部署

生产调试使用独立的 Compose 覆盖文件，不复用本地开发默认值：

1. 将 `.env.production.example` 复制为被 Git 忽略的 `.env.production`，填写公网 HTTPS 域名、Worker 依赖和 Stripe 参数。
2. 先检查配置：`docker compose --env-file .env.production -f docker-compose.yml -f docker-compose.production.yml config --quiet`。
3. 构建并启动：`docker compose --env-file .env.production -f docker-compose.yml -f docker-compose.production.yml up -d --build`。
4. 检查服务：`docker compose --env-file .env.production -f docker-compose.yml -f docker-compose.production.yml ps`，然后访问 Web 的健康页面和后台登录页。

此部署模板保留 `STORE_DEBUG_MODE=1`，只用于站内售卡测试时直接发放 CDK；Worker 仍使用原版浏览器自动化并以无头模式运行。默认管理员邮箱为 `admin@example.com`，密码由本机的 `.env.production` 保存；管理员账号由 `ADMIN_EMAIL` 和 `ADMIN_PASSWORD` 决定，首次使用空数据库初始化时生效；已有数据库不会因为修改环境变量自动覆盖已有密码。

## 安全边界

- Session 进入 API 后只以加密密文保存，公共接口不返回原文；Redis 任务只携带任务 ID。
- 卡号、有效期和 CVC 以密文保存，后台列表仅显示后四位。
- 管理接口使用 `ADMIN_API_TOKEN`，Worker 内部接口使用独立的 `WORKER_API_TOKEN`。
- 生产环境必须替换默认 Token、Session 加密密钥、数据库密码，并通过 HTTPS 和受限网络暴露管理接口。

## Git 提交边界

### 应上传

- `frontend/src`、`frontend/public` 中实际使用的页面、样式、图标和静态资源。
- `backend/api`、`backend/worker`、`backend/protocol-worker` 中的源码、测试和必要配置。
- `go.mod`、`go.sum`、`package.json`、`package-lock.json`、`yarn.lock`。
- Provider 的 OpenAPI 输入、`config.yaml`、`generate.go` 和已生成的静态 SDK；生成的 `client.gen.go` 不手工编辑。
- 根目录和各服务的 Dockerfile、Compose 文件、部署脚本、测试脚本以及 `docs/`、`README.md`。
- `.env.example`、`.env.production.example`，只保留变量名和脱敏示例值。

### 不应上传

- `.env`、`.env.production` 及任何包含真实密码、API Key、Webhook Secret、Session、卡号或 CVC 的文件。
- `node_modules/`、`.next/`、`dist/`、`build/`、`coverage/`、`*.tsbuildinfo` 等可重新生成的依赖和构建产物。
- `runtime/`、浏览器 Profile、运行日志、截图、录像、`captures/` 抓包、HAR 和临时导出文件。
- 本机 IDE 配置、系统文件、临时备份和本地数据库文件。

根目录 `.gitignore` 已覆盖以上规则。上传前可检查：

```bash
git status --short
git diff --cached --check
git check-ignore -v .env.production runtime/api-dev.log frontend/node_modules backend/protocol-worker/captures
```

如果误把敏感文件加入暂存区，应先从暂存区移除并轮换已经暴露的凭证；仅删除工作区文件不能撤销已经进入 Git 历史的 Secret。

## 验证命令

```bash
# Frontend
cd frontend
yarn lint
yarn build

# Go API
cd ../backend/api
go test ./...

# Node Worker
cd ../worker
npm test

# Optional protocol Worker
cd ../protocol-worker
go test ./...
```

完整本地链路和浏览器 E2E 见 [`docs/LOCAL-TESTING.md`](docs/LOCAL-TESTING.md)。
