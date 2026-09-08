package flow

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/captcha"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/chatgpt"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/chromeapi"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/httpx"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/region"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/session"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/store"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/stripe"
)

type Result struct {
	Status      string
	Message     string
	ErrorCode   string
	CardLast4   string
	SessionID   string
	CheckoutURL string
	Output      []string
}

type Options struct {
	Config config.Config
	Store  *store.Client
	Secret map[string]any
	OnLog  func(string)
	OnProg func(int, string)
}

func (r *Result) log(opts Options, progress int, message string) {
	r.Output = append(r.Output, message)
	if opts.OnLog != nil {
		opts.OnLog(message)
	}
	if opts.OnProg != nil {
		opts.OnProg(progress, message)
	}
}

func ShouldPause(opts Options) bool {
	cdk := store.Str(opts.Secret, "cdkCode")
	mode := strings.ToLower(opts.Config.PaymentTestMode)
	return opts.Config.PauseBeforeSubmit || !opts.Config.SubmitPayment || mode == "pause" || mode == "pause_before_submit" || cdk == "[checkout-debug]"
}

func Run(opts Options) Result {
	result := Result{Status: "failed"}
	rawSession := store.Str(opts.Secret, "session")
	if rawSession == "" {
		rawSession = store.Str(opts.Secret, "token")
	}
	sess, err := session.Parse(rawSession)
	if err != nil {
		return Result{Status: "failed", Message: err.Error(), ErrorCode: "session_invalid"}
	}
	if sess.Expired {
		return Result{Status: "failed", Message: "Session AccessToken 已过期，请重新获取", ErrorCode: "session_invalid"}
	}

	regionCode := strings.ToUpper(firstNonEmpty(store.Str(opts.Secret, "region"), opts.Config.PaymentRegion, "SG"))
	regionCfg := region.Get(regionCode)
	planType := region.NormalizePlanType(firstNonEmpty(store.Str(opts.Secret, "planType"), store.Str(opts.Secret, "planId"), "plus"))
	proxy := strings.TrimSpace(store.Str(opts.Secret, "proxy"))
	if proxy == "" {
		proxy = strings.TrimSpace(opts.Config.Proxy)
	}

	result.log(opts, 6, fmt.Sprintf("正在启动 Chrome 打开 chatgpt.com（代理 %s）。SOCKS 会先转本地中继，超时通常是代理或 DNS，不是页面卡住。", httpx.MaskProxy(proxy)))
	browser, err := chromeapi.Open(proxy, regionCfg.Locale, regionCfg.Timezone)
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "ERR_TIMED_OUT") || strings.Contains(strings.ToLower(message), "timeout") {
			message += "。本机直连 chatgpt.com 不通时，必须让 Chrome 走代理远程 DNS；请确认 SOCKS 代理可用，或换一条住宅代理。"
		}
		return retryOrFail("启动 Chrome 过 Cloudflare 失败: "+message, "blocked")
	}
	defer browser.Close()
	if err := browser.Inject(sess); err != nil {
		result.log(opts, 6, "Session 注入警告: "+err.Error())
	} else if sess.Cookies["__Secure-next-auth.session-token"] == "" && !hasPrefixedCookie(sess.Cookies, "__Secure-next-auth.session-token.") {
		result.log(opts, 6, "Session 只有 accessToken，已按原版 Worker 拦截 /api/auth/session 并补 Authorization")
	}
	if did := browser.Cookie("oai-did"); did != "" {
		sess.DeviceID = did
	}
	result.log(opts, 8, fmt.Sprintf("协议支付启动 plan=%s region=%s/%s locale=%s tz=%s proxy=%s", planType, regionCode, regionCfg.Currency, regionCfg.Locale, regionCfg.Timezone, httpx.MaskProxy(proxy)))
	if strings.Contains(strings.ToLower(proxy), "socks") {
		result.log(opts, 8, "SOCKS 代理已转为本地 HTTP 中继（Chrome 不支持带账号的 socks5://）")
	}
	result.log(opts, 9, "UA 用本机 Chrome 版本（不随机伪造）；ChatGPT 请求走同一 Chrome 会话，代理来自代理池")

	headers := map[string]string{
		"Authorization": "Bearer " + sess.AccessToken,
		"Accept":        "application/json",
		"Origin":        "https://chatgpt.com",
		"Referer":       "https://chatgpt.com/#pricing",
		"oai-language":  firstNonEmpty(regionCfg.Locale, "en-US"),
	}
	if sess.AccountID != "" {
		headers["ChatGPT-Account-ID"] = sess.AccountID
	}
	if sess.DeviceID != "" {
		headers["oai-device-id"] = sess.DeviceID
	}
	if err := browser.WarmPricing(); err != nil {
		result.log(opts, 12, "打开定价页预热失败: "+err.Error())
	} else {
		result.log(opts, 12, "已打开定价页预热会话，降低 unusual activity")
	}

	status, body, err := browser.Fetch("GET", "https://chatgpt.com/backend-anon/checkout_pricing_config/configs/"+regionCode, map[string]string{"Accept": "application/json"}, nil)
	if err != nil {
		result.log(opts, 16, "读取定价失败: "+err.Error())
	} else {
		result.log(opts, 16, fmt.Sprintf("HTTP GET pricing/%s → %d %s", regionCode, status, truncate(strings.ReplaceAll(string(body), "\n", " "), 160)))
		if status == 200 {
			pricing := chatgpt.ParsePricing(body)
			result.log(opts, 18, fmt.Sprintf("定价 %s %s%.2f / 月（税前 %.2f %s）", pricing.Country, pricing.Symbol, pricing.PlusInclusive, pricing.PlusExclusive, pricing.Currency))
		}
	}

	payload := chatgpt.BuildCheckoutPayload(planType, regionCode, regionCfg.Currency, store.Str(opts.Secret, "planName"))
	checkout, err := createCheckout(browser, headers, payload, &result, opts)
	if err != nil || !checkout.OK {
		message := checkout.Error
		if err != nil {
			message = err.Error()
		}
		failed := Result{Status: "failed", Message: "无法创建官方 Checkout 订单: " + message, ErrorCode: chatgpt.ErrorCode(message), Output: result.Output}
		if chatgpt.UnusualActivity(message) {
			retried := retryOrFail(failed.Message, failed.ErrorCode)
			retried.Output = result.Output
			retried.SessionID = checkout.SessionID
			retried.CheckoutURL = checkout.CheckoutURL
			return retried
		}
		return failed
	}
	result.SessionID = checkout.SessionID
	result.CheckoutURL = checkout.CheckoutURL
	result.Output = append(result.Output, "CHECKOUT_URL: "+checkout.CheckoutURL)
	result.log(opts, 36, "Checkout 已创建 "+truncate(checkout.SessionID, 24))

	if checkout.ClientSecret == "" || checkout.PublishableKey == "" {
		result.log(opts, 38, "建单响应缺少 client_secret/pk，打开 Checkout 页从网络里提取")
		if navErr := browser.OpenCheckout(checkout.CheckoutURL); navErr != nil {
			result.log(opts, 39, "打开 Checkout 页失败: "+navErr.Error())
		} else {
			chatgpt.MergeCaptured(browser.Captured(), &checkout)
			result.log(opts, 40, "已从 Checkout 页网络提取密钥字段")
		}
	}
	if checkout.PublishableKey == "" {
		_, bootBody, bootErr := browser.Fetch("GET", "https://chatgpt.com/backend-api/payments/stripe_client_bootstrap?account_id="+sess.AccountID, headers, nil)
		if bootErr == nil {
			chatgpt.MergeCaptured(string(bootBody), &checkout)
		}
	}
	if checkout.PublishableKey == "" {
		return Result{Status: "failed", Message: "未拿到 Stripe publishable key", ErrorCode: "checkout_create_failed", Output: result.Output}
	}
	result.log(opts, 42, fmt.Sprintf("Stripe pk=%s session=%s secret=%v", truncate(checkout.PublishableKey, 12), firstNonEmpty(checkout.StripeSessionID, checkout.SessionID), checkout.ClientSecret != ""))

	address := pickAddress(opts.Store)
	result.log(opts, 48, fmt.Sprintf("免税账单地址: %s, %s, %s", address["line1"], address["city"], address["state"]))

	if ShouldPause(opts) {
		card := reserveCard(opts.Store, fmt.Sprintf("protocol_debug_%d", time.Now().UnixNano()))
		last4 := ""
		if card != nil {
			last4 = last4Of(card)
			releaseCard(opts.Store, card)
			result.log(opts, 90, "调试预览将使用卡 ..."+last4+"（已释放，未提交）")
		}
		result.Status = "manual"
		result.Message = "调试结束：已到达付款前最后一步，未向 Stripe 提交扣款"
		result.ErrorCode = "payment_paused_before_submit"
		result.CardLast4 = last4
		result.log(opts, 92, result.Message)
		return result
	}

	stripeClient := httpx.New(proxy)
	captchaSolver := captchaFromStore(opts)
	ownerKey := fmt.Sprintf("protocol_%d", time.Now().UnixNano())
	var lastError, last4 string
	var declined []string
	for attempt := 1; attempt <= opts.Config.MaxCardAttempts; attempt++ {
		card := reserveCard(opts.Store, ownerKey)
		if card == nil {
			if attempt == 1 {
				return Result{Status: "failed", Message: "卡池资产枯竭", ErrorCode: "card_pool_exhausted", Output: result.Output}
			}
			break
		}
		last4 = last4Of(card)
		handled := false
		result.log(opts, 56+attempt*4, fmt.Sprintf("已预留卡片 #%d: ...%s", attempt, last4))
		if opts.Config.PaymentTestMode == "decline" {
			lastError = "PAYMENT_TEST_MODE=decline"
			markDeclined(opts.Store, card, last4, lastError, checkout.SessionID, planType, store.Str(opts.Secret, "cdkCode"), sess.Email)
			declined = append(declined, last4)
			handled = true
			continue
		}
		result.log(opts, 60, "正在提交卡号/有效期/CVC 到 Stripe payment_methods")
		pm := createPaymentMethod(stripeClient, checkout.PublishableKey, card, address, randomName())
		if pm.err != "" {
			lastError = pm.err
			if pm.declined {
				markDeclined(opts.Store, card, last4, lastError, checkout.SessionID, planType, store.Str(opts.Secret, "cdkCode"), sess.Email)
				declined = append(declined, last4)
				handled = true
				continue
			}
			releaseCard(opts.Store, card)
			return Result{Status: "manual", Message: "需要人工操作：" + lastError, ErrorCode: "manual_intervention", CardLast4: last4, Output: result.Output, SessionID: checkout.SessionID}
		}
		result.log(opts, 70, "PaymentMethod 已创建 "+truncate(pm.id, 12))

		token := createConfirmationToken(stripeClient, checkout.PublishableKey, pm.id)
		if token.id != "" {
			result.log(opts, 72, "ConfirmationToken 已创建 "+truncate(token.id, 12))
		}
		paid := confirmPayment(browser, stripeClient, headers, checkout, pm.id, token.id, captchaSolver, &result, opts)
		if paid.auth {
			releaseCard(opts.Store, card)
			return Result{Status: "manual", Message: "需要 3DS/额外验证: " + paid.err, ErrorCode: "manual_intervention", CardLast4: last4, Output: result.Output, SessionID: checkout.SessionID}
		}
		if !paid.ok {
			lastError = paid.err
			if paid.declined {
				markDeclined(opts.Store, card, last4, lastError, checkout.SessionID, planType, store.Str(opts.Secret, "cdkCode"), sess.Email)
				declined = append(declined, last4)
				handled = true
				continue
			}
			releaseCard(opts.Store, card)
			return Result{Status: "manual", Message: "支付未确认: " + lastError, ErrorCode: "payment_result_unknown", CardLast4: last4, Output: result.Output, SessionID: checkout.SessionID}
		}

		if !verifySubscription(browser, headers, &result, opts) {
			releaseCard(opts.Store, card)
			return Result{Status: "manual", Message: "Stripe 已接受但账号套餐未变为已订阅", ErrorCode: "payment_result_unknown", CardLast4: last4, Output: result.Output, SessionID: checkout.SessionID}
		}
		recordSuccess(opts.Store, card, last4, checkout.SessionID, planType, store.Str(opts.Secret, "cdkCode"), sess.Email)
		handled = true
		_ = handled
		result.Status = "succeeded"
		result.Message = "激活成功"
		result.CardLast4 = last4
		result.Output = append(result.Output, "PAYMENT_SUCCESS")
		result.log(opts, 100, "最终校验：支付成功，账号已是付费套餐")
		return result
	}
	message := lastError
	code := "payment_incomplete"
	if len(declined) > 0 {
		message = fmt.Sprintf("%d 张卡均被拒 (...%s)", len(declined), strings.Join(declined, ", ..."))
		code = "card_declined"
	}
	return Result{Status: "manual", Message: message, ErrorCode: code, CardLast4: last4, Output: result.Output, SessionID: result.SessionID}
}

func createCheckout(browser *chromeapi.Session, headers map[string]string, payload chatgpt.CheckoutPayload, result *Result, opts Options) (chatgpt.CheckoutResult, error) {
	attempts := []chatgpt.CheckoutPayload{payload, chatgpt.WithoutCustomUI(payload)}
	var last chatgpt.CheckoutResult
	for i, current := range attempts {
		for retry := 1; retry <= 2; retry++ {
			status, body, err := browser.Fetch("POST", "https://chatgpt.com/backend-api/payments/checkout", headers, current)
			if err != nil {
				return chatgpt.CheckoutResult{}, err
			}
			mode := current.CheckoutUIMode
			if mode == "" {
				mode = "hosted"
			}
			last = chatgpt.ParseCheckout(status, body)
			chatgpt.MergeCaptured(browser.Captured(), &last)
			result.log(opts, 28+i+retry, fmt.Sprintf("HTTP POST /backend-api/payments/checkout ui=%s → %d (第 %d 次) %s", mode, status, retry, truncate(last.Error, 160)))
			if last.OK {
				return last, nil
			}
			if chatgpt.UnusualActivity(last.Error) && retry == 1 {
				result.log(opts, 30, "建单被风控 unusual activity，同一 Chrome 会话等待后重试")
				time.Sleep(3 * time.Second)
				_ = browser.WarmPricing()
				continue
			}
			break
		}
		if last.OK {
			return last, nil
		}
	}
	if chatgpt.UnusualActivity(last.Error) {
		result.log(opts, 32, "API 建单仍被风控，回退到定价页点升级（只建单，不填卡）")
		href, err := browser.OpenPricingCheckout(store.Str(opts.Secret, "planType"))
		if err != nil {
			result.log(opts, 33, "定价页回退失败: "+err.Error())
			return last, nil
		}
		fallback := chatgpt.ParseCheckout(200, []byte("{}"))
		chatgpt.MergeCaptured(href+"\n"+browser.Captured(), &fallback)
		if fallback.SessionID != "" {
			fallback.OK = true
			fallback.Error = ""
			fallback.CheckoutURL = href
			result.log(opts, 34, "定价页回退已拿到 Checkout "+truncate(fallback.SessionID, 24))
			return fallback, nil
		}
		result.log(opts, 34, "定价页已打开但未解析到 oaic session: "+truncate(href, 80))
	}
	return last, nil
}

func Probe(proxy, regionCode string) map[string]any {
	if regionCode == "" {
		regionCode = "SG"
	}
	out := map[string]any{"proxy": httpx.MaskProxy(proxy), "error": ""}
	regionCfg := region.Get(regionCode)
	browser, err := chromeapi.Open(proxy, regionCfg.Locale, regionCfg.Timezone)
	if err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		return out
	}
	defer browser.Close()
	out["cfBootstrap"] = true
	out["userAgent"] = browser.UA
	status, body, err := browser.Fetch("GET", "https://chatgpt.com/backend-anon/checkout_pricing_config/configs/"+strings.ToUpper(regionCode), map[string]string{"Accept": "application/json"}, nil)
	if err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		return out
	}
	out["homeStatus"] = status
	if status == 200 {
		pricing := chatgpt.ParsePricing(body)
		out["pricing"] = pricing
		out["ok"] = pricing.OK
		out["cloudflare"] = false
	} else {
		out["ok"] = false
		out["cloudflare"] = status == 403
	}
	return out
}

func captchaFromStore(opts Options) *captcha.Solver {
	cfg := captcha.Config{}
	if opts.Store != nil {
		if data, err := opts.Store.Config(); err == nil {
			values := store.Map(data, "config")
			if values == nil {
				values = data
			}
			cfg.APIKey = store.Str(values, "hcaptchaCaptchaPlatformApiKey")
			cfg.APIURL = store.Str(values, "hcaptchaCaptchaPlatformApiUrl")
		}
	}
	return captcha.New(cfg)
}

func pickAddress(api *store.Client) map[string]string {
	if api != nil {
		if data, err := api.Action("pickableTaxFreeAddresses", map[string]any{"region": "US"}); err == nil {
			if list, ok := data["addresses"].([]any); ok && len(list) > 0 {
				row, _ := list[rand.Intn(len(list))].(map[string]any)
				return map[string]string{
					"line1":       fmt.Sprint(row["line1"]),
					"city":        fmt.Sprint(row["city"]),
					"state":       fmt.Sprint(row["state"]),
					"postal_code": firstNonEmpty(fmt.Sprint(row["postal_code"]), fmt.Sprint(row["postalCode"])),
					"country":     firstNonEmpty(fmt.Sprint(row["country"]), "US"),
					"id":          fmt.Sprint(row["id"]),
				}
			}
		}
	}
	states := []struct{ code, city, zip string }{{"OR", "Portland", "97205"}, {"MT", "Billings", "59101"}, {"NH", "Manchester", "03101"}, {"DE", "Wilmington", "19801"}, {"AK", "Anchorage", "99501"}}
	pick := states[rand.Intn(len(states))]
	return map[string]string{
		"line1":       fmt.Sprintf("%d Main Street", 100+rand.Intn(8900)),
		"city":        pick.city,
		"state":       pick.code,
		"postal_code": pick.zip,
		"country":     "US",
	}
}

func randomName() string {
	first := []string{"James", "Mary", "Robert", "Patricia", "John", "Jennifer"}
	last := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia"}
	return first[rand.Intn(len(first))] + " " + last[rand.Intn(len(last))]
}

func reserveCard(api *store.Client, owner string) map[string]any {
	if api == nil {
		return nil
	}
	data, err := api.Action("reserveCard", map[string]any{"ownerKey": owner})
	if err != nil {
		return nil
	}
	return store.Map(data, "card")
}

func releaseCard(api *store.Client, card map[string]any) {
	if api == nil || card == nil {
		return
	}
	_, _ = api.Action("releaseCard", map[string]any{"cardId": fmt.Sprint(card["id"])})
}

func last4Of(card map[string]any) string {
	number := fmt.Sprint(card["card_number"])
	if len(number) >= 4 {
		return number[len(number)-4:]
	}
	return fmt.Sprint(card["last4"])
}

func markDeclined(api *store.Client, card map[string]any, last4, message, sessionID, planType, cdk, email string) {
	if api == nil || card == nil {
		return
	}
	_, _ = api.Action("markCardExhausted", map[string]any{"cardId": fmt.Sprint(card["id"])})
	_, _ = api.Action("createBillingRecord", map[string]any{"data": map[string]any{
		"card_number": card["card_number"], "card_last4": last4, "plan_type": planType, "cdk_code": cdk, "email": email,
		"stripe_session_id": sessionID, "status": "failed", "error_code": "card_declined", "error_message": message,
	}})
}

func recordSuccess(api *store.Client, card map[string]any, last4, sessionID, planType, cdk, email string) {
	if api == nil || card == nil {
		return
	}
	_, _ = api.Action("recordCardUsage", map[string]any{"cardId": fmt.Sprint(card["id"])})
	_, _ = api.Action("releaseCard", map[string]any{"cardId": fmt.Sprint(card["id"])})
	_, _ = api.Action("createBillingRecord", map[string]any{"data": map[string]any{
		"card_number": card["card_number"], "card_last4": last4, "plan_type": planType, "cdk_code": cdk, "email": email,
		"stripe_session_id": sessionID, "status": "success",
	}})
}

type pmResult struct {
	id       string
	err      string
	declined bool
}

func createPaymentMethod(client *httpx.Client, pk string, card map[string]any, address map[string]string, holder string) pmResult {
	fields := stripe.PaymentMethodFields(
		fmt.Sprint(card["card_number"]), fmt.Sprint(card["card_expiry"]), fmt.Sprint(card["card_cvc"]), holder,
		fmt.Sprint(address["line1"]), fmt.Sprint(address["city"]), fmt.Sprint(address["state"]), fmt.Sprint(address["postal_code"]), fmt.Sprint(address["country"]),
	)
	fields["key"] = pk
	resp, err := client.Form("POST", "https://api.stripe.com/v1/payment_methods", map[string]string{"Authorization": "Bearer " + pk, "Origin": "https://chatgpt.com"}, stripe.EncodeForm(fields))
	if err != nil {
		return pmResult{err: err.Error()}
	}
	var data map[string]any
	_ = json.Unmarshal(resp.Body, &data)
	if id, _ := data["id"].(string); resp.Status < 300 && id != "" && data["error"] == nil {
		return pmResult{id: id}
	}
	classified := stripe.Classify(stripeErr(data), stripeErrCode(data), stripeDecline(data), resp.Status)
	return pmResult{err: classified.Message, declined: classified.Declined}
}

func createConfirmationToken(client *httpx.Client, pk, pmID string) pmResult {
	resp, err := client.Form("POST", "https://api.stripe.com/v1/confirmation_tokens", map[string]string{"Authorization": "Bearer " + pk}, stripe.EncodeForm(map[string]any{"key": pk, "payment_method": pmID}))
	if err != nil {
		return pmResult{err: err.Error()}
	}
	var data map[string]any
	_ = json.Unmarshal(resp.Body, &data)
	id, _ := data["id"].(string)
	return pmResult{id: id, err: stripeErr(data)}
}

type confirmResult struct {
	ok, declined, auth bool
	err                string
}

func confirmPayment(browser *chromeapi.Session, stripeClient *httpx.Client, headers map[string]string, checkout chatgpt.CheckoutResult, pmID, confirmToken string, solver *captcha.Solver, result *Result, opts Options) confirmResult {
	if confirmToken != "" {
		body := chatgpt.ConfirmBody(checkout.SessionID, confirmToken, checkout.Processor)
		for _, path := range chatgpt.ConfirmPaths(checkout.SessionID) {
			status, raw, err := browser.Fetch("POST", "https://chatgpt.com"+path, headers, body)
			if err != nil {
				result.log(opts, 76, "OpenAI confirm "+path+" 失败: "+err.Error())
				continue
			}
			result.log(opts, 76, fmt.Sprintf("HTTP POST %s → %d", path, status))
			if status == 404 {
				continue
			}
			parsed := chatgpt.ParseCheckout(status, raw)
			chatgpt.MergeCaptured(string(raw), &parsed)
			var data map[string]any
			_ = json.Unmarshal(raw, &data)
			kind := fmt.Sprint(data["type"])
			if parsed.ClientSecret != "" {
				checkout.ClientSecret = parsed.ClientSecret
			}
			if status < 300 && (kind == "complete" || stripe.Paid(fmt.Sprint(data["status"]), status, data["error"] != nil)) {
				notifyOpenAIPaid(browser, headers, checkout)
				return confirmResult{ok: true}
			}
			classified := stripe.Classify(stripeErr(data)+parsed.Error, stripeErrCode(data), stripeDecline(data), status)
			if classified.Declined {
				return confirmResult{declined: true, err: classified.Message}
			}
			if classified.AuthRequired {
				return confirmResult{auth: true, err: classified.Message}
			}
			if kind == "payment_intent" || kind == "setup_intent" {
				if intent := confirmStripeIntent(stripeClient, checkout, pmID, confirmToken, kind, result, opts); intent.ok || intent.declined || intent.auth {
					if intent.ok {
						notifyOpenAIPaid(browser, headers, checkout)
					}
					return intent
				}
			}
		}
	}

	pageID := firstNonEmpty(checkout.StripeSessionID, stripe.SessionID(checkout.ClientSecret))
	if pageID == "" {
		return confirmResult{err: "没有 Stripe cs_ session，无法 payment_pages/confirm"}
	}
	fields := map[string]any{"key": checkout.PublishableKey, "payment_method": pmID, "return_url": checkout.CheckoutURL}
	if checkout.ClientSecret != "" {
		fields["client_secret"] = checkout.ClientSecret
	}
	if confirmToken != "" {
		fields["confirmation_token"] = confirmToken
	}
	resp, err := stripeClient.Form("POST", "https://api.stripe.com/v1/payment_pages/"+pageID+"/confirm", map[string]string{"Authorization": "Bearer " + checkout.PublishableKey, "Origin": "https://chatgpt.com"}, stripe.EncodeForm(fields))
	if err != nil {
		return confirmResult{err: err.Error()}
	}
	result.log(opts, 80, fmt.Sprintf("HTTP POST /v1/payment_pages/%s/confirm → %d", truncate(pageID, 18), resp.Status))
	var data map[string]any
	_ = json.Unmarshal(resp.Body, &data)
	payStatus := fmt.Sprint(data["status"])
	if pi, _ := data["payment_intent"].(map[string]any); pi != nil {
		payStatus = firstNonEmpty(fmt.Sprint(pi["status"]), payStatus)
	}
	classified := stripe.Classify(stripeErr(data), stripeErrCode(data), stripeDecline(data), resp.Status)
	if classified.AuthRequired || payStatus == "requires_action" {
		return confirmResult{auth: true, err: classified.Message}
	}
	if classified.Declined {
		return confirmResult{declined: true, err: classified.Message}
	}
	if stripe.Paid(payStatus, resp.Status, data["error"] != nil) {
		notifyOpenAIPaid(browser, headers, checkout)
		return confirmResult{ok: true}
	}
	return confirmResult{err: firstNonEmpty(classified.Message, "Stripe 未返回 succeeded")}
}

func confirmStripeIntent(client *httpx.Client, checkout chatgpt.CheckoutResult, pmID, confirmToken, kind string, result *Result, opts Options) confirmResult {
	secret := checkout.ClientSecret
	id := stripe.SessionID(secret)
	if strings.HasPrefix(secret, "pi_") {
		id = strings.Split(secret, "_secret_")[0]
	}
	if strings.HasPrefix(secret, "seti_") {
		id = strings.Split(secret, "_secret_")[0]
	}
	if id == "" {
		return confirmResult{}
	}
	endpoint := "https://api.stripe.com/v1/payment_intents/" + id + "/confirm"
	if kind == "setup_intent" || strings.HasPrefix(secret, "seti_") {
		endpoint = "https://api.stripe.com/v1/setup_intents/" + id + "/confirm"
	}
	fields := map[string]any{"key": checkout.PublishableKey, "payment_method": pmID}
	if secret != "" {
		fields["client_secret"] = secret
	}
	if confirmToken != "" {
		fields["confirmation_token"] = confirmToken
	}
	resp, err := client.Form("POST", endpoint, map[string]string{"Authorization": "Bearer " + checkout.PublishableKey, "Origin": "https://chatgpt.com"}, stripe.EncodeForm(fields))
	if err != nil {
		return confirmResult{err: err.Error()}
	}
	result.log(opts, 78, fmt.Sprintf("HTTP POST %s → %d", truncate(endpoint, 48), resp.Status))
	var data map[string]any
	_ = json.Unmarshal(resp.Body, &data)
	payStatus := fmt.Sprint(data["status"])
	classified := stripe.Classify(stripeErr(data), stripeErrCode(data), stripeDecline(data), resp.Status)
	if classified.AuthRequired || payStatus == "requires_action" {
		return confirmResult{auth: true, err: classified.Message}
	}
	if classified.Declined {
		return confirmResult{declined: true, err: classified.Message}
	}
	if stripe.Paid(payStatus, resp.Status, data["error"] != nil) {
		return confirmResult{ok: true}
	}
	return confirmResult{err: firstNonEmpty(classified.Message, "Stripe intent 未返回 succeeded")}
}

func notifyOpenAIPaid(browser *chromeapi.Session, headers map[string]string, checkout chatgpt.CheckoutResult) {
	_, _, _ = browser.Fetch("POST", "https://chatgpt.com/backend-api/payments/checkout_session", headers, map[string]any{
		"stripe_checkout_session_id": firstNonEmpty(checkout.StripeSessionID, checkout.SessionID),
		"processor_entity":           firstNonEmpty(checkout.Processor, "openai_llc"),
	})
}

func verifySubscription(browser *chromeapi.Session, headers map[string]string, result *Result, opts Options) bool {
	url := "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27?timezone_offset_min=480"
	for i := 0; i < 5; i++ {
		status, body, err := browser.Fetch("GET", url, headers, nil)
		if err != nil {
			result.log(opts, 90, "查询订阅失败: "+err.Error())
			time.Sleep(2 * time.Second)
			continue
		}
		account := chatgpt.ParseAccount(body)
		result.log(opts, 90, fmt.Sprintf("HTTP GET accounts/check → %d active=%v plan=%s", status, account.HasActive, account.Plan))
		if account.HasActive {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func retryOrFail(message, code string) Result {
	blob := strings.ToLower(message)
	status := "failed"
	if strings.Contains(blob, "proxy") || strings.Contains(blob, "timeout") || strings.Contains(blob, "cloudflare") || strings.Contains(blob, "unusual activity") {
		status = "retry"
		if code == "" {
			code = "blocked"
		}
	}
	return Result{Status: status, Message: message, ErrorCode: code}
}

func stripeErr(data map[string]any) string {
	if err, _ := data["error"].(map[string]any); err != nil {
		if text, _ := err["message"].(string); text != "" {
			return text
		}
	}
	if text, _ := data["message"].(string); text != "" {
		return text
	}
	if text, _ := data["detail"].(string); text != "" {
		return text
	}
	return ""
}

func stripeErrCode(data map[string]any) string {
	if err, _ := data["error"].(map[string]any); err != nil {
		if text, _ := err["code"].(string); text != "" {
			return text
		}
	}
	return ""
}

func stripeDecline(data map[string]any) string {
	if err, _ := data["error"].(map[string]any); err != nil {
		if text, _ := err["decline_code"].(string); text != "" {
			return text
		}
	}
	return ""
}

func hasPrefixedCookie(cookies map[string]string, prefix string) bool {
	for name := range cookies {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "<nil>" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
