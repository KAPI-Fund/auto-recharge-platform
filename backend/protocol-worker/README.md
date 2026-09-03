# Protocol Worker（Go）

后台「系统配置 → 开通 Worker」可在浏览器 Worker 和协议 Worker 之间切换；「支付链接调试」页也可以按单次任务选择。两边 Worker 只处理自己的 mode，不会互相抢错任务。

协议 Worker 的每一步 HTTP（建单、定价、提交卡号、Stripe 确认）都会写入运行日志，调试页可以实时看。调试页选「协议」时任务 CDK 为 `[checkout-debug]`，会在拿到 Checkout、公钥、免税地址和预览卡号后停止，**不会向 Stripe 提交扣款**。

Cloudflare 会拦纯 TLS 伪装，所以 ChatGPT 请求始终走同一条无头 Chrome 会话（Cookie + TLS 指纹一致）。`checkout_ui_mode=custom` 若返回 unusual activity，会先重试，再去掉 custom，再回退到定价页点升级只为建单。支付过程中的 hCaptcha 走后台已配置的打码平台 API（Capsolver / 2Captcha / Anti-Captcha），不再用 Python CDP 去点图。

浏览器 Worker 仍保留给人机验证很重、需要截图的场景。协议 Worker 抢同一条 Redis 队列，所以同一时间只跑其中一个。

## 支付流程

1. 用 Chrome 打开 chatgpt.com（过 Cloudflare），注入 Session Cookie 后刷新
2. `POST /backend-api/payments/checkout` 建单（SG/SGD 等地区来自后台配置）；失败则定价页回退拿 `oaics_`
3. 从卡池拿卡、从免税地址池拿美国地址
4. 调试任务在这里停止。正式任务：`POST api.stripe.com/v1/payment_methods` 提交卡号 / 有效期 / CVC
5. OpenAI `confirmCheckout`（confirmation_token）→ 必要时 Stripe `payment_pages/{cs_id}/confirm`
6. `GET /backend-api/accounts/check` 确认套餐已生效；拒付则报废该卡并换卡

## 人机验证（全部 API，不点页面）

原版页面 Worker 有三套：

- 打码平台：Capsolver / 2Captcha / Anti-Captcha（`createTask` + `getTaskResult`）
- 大模型 VLM：给 Python solver 看图片
- Python `hcaptcha/solver.py`：通过 CDP 在页面上点选

协议版对应关系：

| 场景 | 做法 |
| --- | --- |
| Cloudflare 403 | Chrome 伪装 TLS；仍失败则打码平台 `AntiCloudflareTask` |
| Stripe/ChatGPT hCaptcha | 打码平台 `HCaptchaTaskProxyless`，token 放进后续请求 |
| 图片点选 / 拖拽 | 协议请求通常碰不到；若响应里带 sitekey/rqdata 仍走打码平台 |
| 3DS | 无法静默完成，任务进人工 |

后台「人机验证」里配的打码平台 Key 会被 Worker 通过 `/internal/config` 读到。

## 调试

```bash
cd backend/protocol-worker
go test ./...

# 代理 + SG 定价，不需要 Session，不扣款
PROXY=http://127.0.0.1:7897 go run ./cmd/simulate probe

# 建单后停下
SESSION_JSON='{"accessToken":"..."}' go run ./cmd/simulate checkout

# 真实提交（空卡会走拒付）
go run ./cmd/simulate pay --session-file ./session.json
PAYMENT_TEST_MODE=decline go run ./cmd/simulate decline --session-file ./session.json
```

队列（先停掉 Node Worker，避免抢任务）：

```bash
docker compose -f docker-compose.yml -f docker-compose.worker-local-api.yml -f docker-compose.protocol.yml up -d --build protocol-worker
```

或本机：

```bash
API_BASE_URL=http://127.0.0.1:28080 REDIS_URL=redis://127.0.0.1:16379/0 go run ./cmd/worker
```

把后台「执行模式」或 `DEFAULT_RECHARGE_MODE` 设成 `protocol` 后，前台点支付就会入协议队列。协议 Worker 也会消费原来的 `browser` 任务，所以不改前端也能用。
