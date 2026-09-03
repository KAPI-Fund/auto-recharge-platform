package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const (
	chatGPTSubscriptionCheckURL  = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"
	chatGPTCancelSubscriptionURL = "https://chatgpt.com/backend-api/subscriptions/cancel"
	chatGPTResumeSubscriptionURL = "https://chatgpt.com/backend-api/subscriptions/resume"
	chatGPTBillingPageURL        = "https://chatgpt.com/account/manage"
	chatGPTSubscriptionUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"
)

var subscriptionJWTPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

type subscriptionProfile struct {
	Email     string
	AccountID string
}

type subscriptionResult struct {
	OK         bool
	StatusCode int
	Data       map[string]any
	Error      string
}

type subscriptionHTTPResponse struct {
	Status int
	Data   any
}

func extractSubscriptionAccessToken(raw string) string {
	content := strings.Trim(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")), "\"'")
	if content == "" {
		return ""
	}

	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) == nil {
		for _, key := range []string{"accessToken", "access_token", "token"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	if match := subscriptionJWTPattern.FindString(content); match != "" {
		return match
	}
	return content
}

func subscriptionEmailFromRaw(raw, token string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload) == nil {
		if user := subscriptionMap(payload["user"]); user != nil {
			if email := subscriptionString(user["email"]); email != "" {
				return email
			}
		}
		if email := subscriptionString(payload["email"]); email != "" {
			return email
		}
	}
	profile, _ := subscriptionTokenProfile(token)
	return profile.Email
}

func subscriptionTokenProfile(token string) (subscriptionProfile, string) {
	value := strings.TrimSpace(token)
	if value == "" {
		return subscriptionProfile{}, "缺少 AccessToken"
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return subscriptionProfile{}, "该 Token 不合法：格式错误"
	}

	payload, err := decodeSubscriptionJWTPart(parts[1])
	if err != nil {
		return subscriptionProfile{}, "该 Token 不合法：无法解析"
	}
	if issuer := subscriptionString(payload["iss"]); issuer != "" && issuer != "https://auth.openai.com" {
		return subscriptionProfile{}, "该 Token 不合法：签发方错误"
	}
	if exp := subscriptionNumber(payload["exp"]); exp > 0 && exp <= float64(time.Now().Unix()) {
		return subscriptionProfile{}, "该 Token 已过期，请重新获取 Session"
	}

	authInfo := subscriptionMap(payload["https://api.openai.com/auth"])
	profile := subscriptionMap(payload["https://api.openai.com/profile"])
	return subscriptionProfile{
		Email:     subscriptionString(profile["email"]),
		AccountID: subscriptionString(authInfo["chatgpt_account_id"]),
	}, ""
}

func validateSubscriptionToken(token string) (subscriptionProfile, string) {
	return subscriptionTokenProfile(token)
}

func decodeSubscriptionJWTPart(part string) (map[string]any, error) {
	value := strings.NewReplacer("-", "+", "_", "/").Replace(part)
	value += strings.Repeat("=", (4-len(value)%4)%4)
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func subscriptionMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	result, _ := value.(map[string]any)
	return result
}

func subscriptionString(value any) string {
	if value == nil {
		return ""
	}
	if result, ok := value.(string); ok {
		return strings.TrimSpace(result)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func subscriptionNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result
	default:
		return 0
	}
}

func subscriptionBool(value any) bool {
	if value == nil {
		return false
	}
	if result, ok := value.(bool); ok {
		return result
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "", "0", "false", "null":
		return false
	default:
		return true
	}
}

func normalizeSubscriptionPlan(raw string, hasActive bool) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		if hasActive {
			return "unknown"
		}
		return "free"
	}
	if strings.Contains(value, "team") {
		return "team"
	}
	if strings.Contains(value, "pro") && !strings.Contains(value, "plus") {
		return "pro"
	}
	if strings.Contains(value, "plus") {
		return "plus"
	}
	if strings.Contains(value, "free") {
		return "free"
	}
	if len(value) > 40 {
		return value[:40]
	}
	return value
}

func subscriptionPlanLabel(planKey, rawPlan string) string {
	switch planKey {
	case "plus":
		return "ChatGPT Plus"
	case "pro":
		return "ChatGPT Pro"
	case "team":
		return "ChatGPT Team"
	case "free":
		return "免费版"
	case "unknown":
		return "未知"
	default:
		if rawPlan != "" {
			return rawPlan
		}
		return "未知"
	}
}

func subscriptionOriginLabel(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	switch raw {
	case "":
		return "—"
	case "chatgpt_not_purchased":
		return "未购买"
	case "chatgpt_web", "web":
		return "Web (Stripe)"
	case "stripe":
		return "Stripe"
	case "ios", "apple":
		return "Apple App Store"
	case "android", "google_play":
		return "Google Play"
	}
	if strings.Contains(raw, "apple") || strings.Contains(raw, "ios") {
		return "Apple App Store"
	}
	if strings.Contains(raw, "android") || strings.Contains(raw, "google") {
		return "Google Play"
	}
	if strings.Contains(raw, "stripe") || strings.Contains(raw, "web") {
		return "Web (Stripe)"
	}
	return value
}

func subscriptionOriginIsAppStore(value string) bool {
	raw := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(raw, "apple") || strings.Contains(raw, "ios") || strings.Contains(raw, "google") || strings.Contains(raw, "android")
}

func subscriptionCurrency(sources ...map[string]any) string {
	for _, source := range sources {
		for _, key := range []string{"billing_currency", "currency", "currency_code", "billing_currency_code"} {
			value := strings.ToUpper(strings.TrimSpace(subscriptionString(source[key])))
			if len(value) == 3 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z' && value[2] >= 'A' && value[2] <= 'Z' {
				return value
			}
		}
	}
	return ""
}

func parseSubscriptionTime(value any) (time.Time, bool) {
	raw := strings.TrimSpace(subscriptionString(value))
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func subscriptionDateDisplay(value any) string {
	if value == nil || subscriptionString(value) == "" {
		return "—"
	}
	parsed, ok := parseSubscriptionTime(value)
	if !ok {
		return subscriptionString(value)
	}
	return parsed.Local().Format("2006/01/02 15:04:05")
}

func subscriptionRemainingDays(value any, now time.Time) (any, string) {
	parsed, ok := parseSubscriptionTime(value)
	if !ok {
		return nil, "—"
	}
	days := int(math.Ceil(parsed.Sub(now).Hours() / 24))
	if days < 0 {
		return days, fmt.Sprintf("已过期 %d 天", -days)
	}
	if days == 0 {
		return days, "今天到期"
	}
	return days, fmt.Sprintf("%d 天", days)
}

func subscriptionBooleanDisplay(value any) string {
	if result, ok := value.(bool); ok {
		if result {
			return "是"
		}
		return "否"
	}
	return "—"
}

func parseSubscriptionResponse(data map[string]any, profile subscriptionProfile, now time.Time) map[string]any {
	accounts := subscriptionMap(data["accounts"])
	defaultAccount := subscriptionMap(accounts["default"])
	account := subscriptionMap(defaultAccount["account"])
	entitlement := subscriptionMap(defaultAccount["entitlement"])
	lastActive := subscriptionMap(defaultAccount["last_active_subscription"])

	hasActive := subscriptionBool(entitlement["has_active_subscription"])
	rawPlan := subscriptionString(entitlement["subscription_plan"])
	planKey := normalizeSubscriptionPlan(rawPlan, hasActive)
	expiresAt := entitlement["expires_at"]
	if subscriptionString(expiresAt) == "" {
		expiresAt = lastActive["expires_at"]
	}
	remainingDays, remainingDaysDisplay := subscriptionRemainingDays(expiresAt, now)
	currency := subscriptionCurrency(lastActive, entitlement, account)
	if currency == "" {
		currency = "—"
	}
	originRaw := subscriptionString(lastActive["purchase_origin_platform"])
	queriedAt := now.UTC().Format(time.RFC3339Nano)

	return map[string]any{
		"email":                  profile.Email,
		"accountId":              firstSubscriptionValue(subscriptionString(account["account_id"]), profile.AccountID),
		"plan":                   subscriptionPlanLabel(planKey, rawPlan),
		"planKey":                planKey,
		"rawPlan":                rawPlan,
		"hasActiveSubscription":  hasActive,
		"subscriptionChannel":    subscriptionOriginLabel(originRaw),
		"subscriptionChannelRaw": originRaw,
		"currency":               currency,
		"expiresAt":              expiresAt,
		"expiresAtDisplay":       subscriptionDateDisplay(expiresAt),
		"remainingDays":          remainingDays,
		"remainingDaysDisplay":   remainingDaysDisplay,
		"autoRenew":              subscriptionBooleanDisplay(lastActive["will_renew"]),
		"autoRenewRaw":           lastActive["will_renew"],
		"hasPreviouslyPaid":      subscriptionBooleanDisplay(account["has_previously_paid_subscription"]),
		"hasPreviouslyPaidRaw":   subscriptionBool(account["has_previously_paid_subscription"]),
		"queriedAt":              queriedAt,
		"queriedAtDisplay":       now.Local().Format("2006/01/02 15:04:05"),
		"billingPageUrl":         chatGPTBillingPageURL,
	}
}

func firstSubscriptionValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func subscriptionOffsetMinutes(input map[string]any) int {
	if value, ok := input["timezone_offset_min"]; ok {
		if parsed := subscriptionNumber(value); math.IsNaN(parsed) == false && math.IsInf(parsed, 0) == false {
			return int(parsed)
		}
	}
	_, offset := time.Now().Zone()
	return offset / 60
}

func (s *Server) querySubscriptionByToken(token string, timezoneOffset int, email string) subscriptionResult {
	profile, message := validateSubscriptionToken(token)
	if message != "" {
		return subscriptionResult{StatusCode: http.StatusBadRequest, Error: message}
	}
	if strings.TrimSpace(email) != "" {
		profile.Email = strings.TrimSpace(email)
	}

	endpoint := chatGPTSubscriptionCheckURL + "?timezone_offset_min=" + url.QueryEscape(strconv.Itoa(timezoneOffset))
	response, err := s.requestSubscriptionJSON(http.MethodGet, endpoint, token, nil)
	if err != nil {
		return subscriptionResult{StatusCode: http.StatusBadGateway, Error: "无法连接 OpenAI 订阅接口：" + err.Error()}
	}
	if response.Status != http.StatusOK {
		status, mapped := mapSubscriptionCheckError(response.Status, response.Data)
		return subscriptionResult{StatusCode: status, Error: mapped}
	}
	body, ok := response.Data.(map[string]any)
	if !ok || len(body) == 0 {
		return subscriptionResult{StatusCode: http.StatusBadGateway, Error: "OpenAI 返回数据格式异常"}
	}
	return subscriptionResult{OK: true, StatusCode: http.StatusOK, Data: parseSubscriptionResponse(body, profile, time.Now())}
}

func (s *Server) requestSubscriptionJSON(method, endpoint, token string, body any) (subscriptionHTTPResponse, error) {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return subscriptionHTTPResponse{}, err
		}
		requestBody = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, endpoint, requestBody)
	if err != nil {
		return subscriptionHTTPResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")
	request.Header.Set("User-Agent", chatGPTSubscriptionUserAgent)
	request.Header.Set("Referer", "https://chatgpt.com/")
	request.Header.Set("Origin", "https://chatgpt.com")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("sec-ch-ua", `"Chromium";v="136", "Google Chrome";v="136", "Not.A/Brand";v="99"`)
	request.Header.Set("sec-ch-ua-mobile", "?0")
	request.Header.Set("sec-ch-ua-platform", `"Windows"`)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	client, err := subscriptionHTTPClient(s.Cfg.OutboundProxy)
	if err != nil {
		return subscriptionHTTPResponse{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return subscriptionHTTPResponse{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return subscriptionHTTPResponse{}, err
	}
	if len(raw) == 0 {
		return subscriptionHTTPResponse{Status: response.StatusCode}, nil
	}
	var data any
	if json.Unmarshal(raw, &data) != nil {
		data = string(raw)
	}
	return subscriptionHTTPResponse{Status: response.StatusCode, Data: data}, nil
}

func subscriptionHTTPClient(proxyValue string) (*http.Client, error) {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	proxyValue = strings.TrimSpace(proxyValue)
	if proxyValue == "" {
		return &http.Client{Transport: transport, Timeout: 20 * time.Second}, nil
	}
	parsed, err := url.Parse(proxyValue)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("代理 URL 无效")
	}
	if parsed.Scheme == "socks5" || parsed.Scheme == "socks5h" {
		var auth *xproxy.Auth
		if parsed.User != nil {
			auth = &xproxy.Auth{User: parsed.User.Username()}
			if password, ok := parsed.User.Password(); ok {
				auth.Password = password
			}
		}
		dialer, err := xproxy.SOCKS5("tcp", parsed.Host, auth, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			return dialer.Dial(network, address)
		}
		return &http.Client{Transport: transport, Timeout: 20 * time.Second}, nil
	}
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}, nil
}

func mapSubscriptionCheckError(status int, data any) (int, string) {
	if status == http.StatusUnauthorized {
		return http.StatusUnauthorized, "Session 无效或已过期，请重新获取 Session"
	}
	if status == http.StatusForbidden {
		body := strings.ToLower(subscriptionBodyText(data))
		if strings.Contains(body, "cloudflare") || strings.Contains(body, "cf-ray") || strings.Contains(body, "attention required") || strings.Contains(body, "just a moment") || strings.Contains(body, "cf_chl") {
			return http.StatusForbidden, "OpenAI 风控拦截（Cloudflare），请稍后重试或在后台配置住宅代理 PROXY"
		}
		return http.StatusForbidden, "OpenAI 拒绝访问（403），请确认 Session 来自 chatgpt.com 且 accessToken 未过期"
	}
	if status == 0 {
		return http.StatusBadGateway, "无法连接 OpenAI 订阅接口：network error"
	}
	detail := subscriptionBodyText(data)
	if len(detail) > 200 {
		detail = detail[:200]
	}
	message := fmt.Sprintf("OpenAI 返回异常 (%d)", status)
	if detail != "" {
		message += "：" + detail
	}
	return status, message
}

func subscriptionBodyText(data any) string {
	if data == nil {
		return ""
	}
	if value, ok := data.(string); ok {
		return value
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprint(data)
	}
	return string(encoded)
}

func cloneSubscriptionData(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+4)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (s *Server) changeSubscriptionRenewal(token, action string, timezoneOffset int, email string) subscriptionResult {
	profile, message := validateSubscriptionToken(token)
	if message != "" {
		return subscriptionResult{StatusCode: http.StatusBadRequest, Error: message}
	}
	if strings.TrimSpace(email) != "" {
		profile.Email = strings.TrimSpace(email)
	}
	current := s.querySubscriptionByToken(token, timezoneOffset, profile.Email)
	if !current.OK {
		return current
	}
	subscription := current.Data
	accountID := firstSubscriptionValue(subscriptionString(subscription["accountId"]), profile.AccountID)
	if accountID == "" {
		return subscriptionResult{StatusCode: http.StatusBadRequest, Error: "无法解析 account_id，请确认 Session 完整有效"}
	}
	channelRaw := subscriptionString(subscription["subscriptionChannelRaw"])
	if subscriptionOriginIsAppStore(channelRaw) {
		verb := "取消"
		if action == "resume" {
			verb = "开启"
		}
		return subscriptionResult{StatusCode: http.StatusBadRequest, Error: fmt.Sprintf("该订阅来自 %s，无法通过 API %s，请在对应平台操作", subscription["subscriptionChannel"], verb)}
	}
	if !subscriptionBool(subscription["hasActiveSubscription"]) {
		if action == "resume" {
			return subscriptionResult{StatusCode: http.StatusBadRequest, Error: "账号当前无有效订阅，无法开启自动续费"}
		}
		return subscriptionResult{StatusCode: http.StatusBadRequest, Error: "账号当前无有效订阅，无需取消自动续费"}
	}
	autoRenew, hasAutoRenew := subscription["autoRenewRaw"]
	if action == "cancel" && hasAutoRenew && autoRenew == false {
		result := cloneSubscriptionData(subscription)
		result["alreadyCancelled"] = true
		result["message"] = "自动续费已关闭，无需重复操作"
		return subscriptionResult{OK: true, StatusCode: http.StatusOK, Data: result}
	}
	if action == "resume" && hasAutoRenew && autoRenew == true {
		result := cloneSubscriptionData(subscription)
		result["alreadyEnabled"] = true
		result["message"] = "自动续费已开启，无需重复操作"
		return subscriptionResult{OK: true, StatusCode: http.StatusOK, Data: result}
	}

	endpoint := chatGPTCancelSubscriptionURL
	if action == "resume" {
		endpoint = chatGPTResumeSubscriptionURL
	}
	response, err := s.requestSubscriptionJSON(http.MethodPost, endpoint, token, map[string]any{"account_id": accountID})
	if err != nil {
		verb := "取消"
		if action == "resume" {
			verb = "开启"
		}
		return subscriptionResult{StatusCode: http.StatusBadGateway, Error: fmt.Sprintf("%s自动续费请求失败：%s", verb, err.Error())}
	}
	if response.Status == http.StatusUnauthorized {
		return subscriptionResult{StatusCode: http.StatusUnauthorized, Error: "Session 无效或已过期，请重新获取 Session"}
	}
	if response.Status == http.StatusForbidden {
		status, mapped := mapSubscriptionCheckError(response.Status, response.Data)
		return subscriptionResult{StatusCode: status, Error: mapped}
	}
	if response.Status != http.StatusOK && response.Status != http.StatusNoContent {
		detail := subscriptionBodyText(response.Data)
		if len(detail) > 200 {
			detail = detail[:200]
		}
		verb := "取消"
		if action == "resume" {
			verb = "开启"
		}
		failure := fmt.Sprintf("%s自动续费失败 (%d)", verb, response.Status)
		if detail != "" {
			failure += "：" + detail
		}
		return subscriptionResult{StatusCode: response.Status, Error: failure}
	}

	verified := s.querySubscriptionByToken(token, timezoneOffset, profile.Email)
	result := cloneSubscriptionData(subscription)
	if verified.OK {
		result = cloneSubscriptionData(verified.Data)
	}
	if action == "resume" {
		result["alreadyEnabled"] = false
		result["resumed"] = true
		if verified.OK && verified.Data["autoRenewRaw"] == true {
			result["message"] = "已成功开启自动续费"
		} else {
			result["message"] = "已提交开启自动续费请求，请稍后刷新确认状态"
		}
	} else {
		result["alreadyCancelled"] = false
		result["cancelled"] = true
		if verified.OK && verified.Data["autoRenewRaw"] == false {
			result["message"] = "已成功关闭自动续费，当前周期仍可继续使用"
		} else {
			result["message"] = "已提交取消自动续费请求，请稍后刷新确认状态"
		}
	}
	return subscriptionResult{OK: true, StatusCode: http.StatusOK, Data: result}
}
