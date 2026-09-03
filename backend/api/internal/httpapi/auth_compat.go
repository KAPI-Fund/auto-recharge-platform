package httpapi

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
)

var telegramLoginCodes sync.Map

const (
	loginMaxAttempts = 5
	loginWindow      = 15 * time.Minute
	loginLock        = 30 * time.Minute
)

var loginAttempts = struct {
	sync.Mutex
	items map[string]loginAttempt
}{items: map[string]loginAttempt{}}

type loginAttempt struct {
	Count       int
	FirstAt     time.Time
	LockedUntil time.Time
}

type loginCode struct {
	Code     string
	Expires  time.Time
	Attempts int
}

func (s *Server) ensureAuthDefaults() (map[string]string, error) {
	defaults := map[string]string{
		"admin_email":                      s.Cfg.AdminEmail,
		"admin_password_version":           "1",
		"admin_secondary_password_version": "1",
		"admin_totp_enabled":               "0",
		"admin_2fa_login_mode":             "either",
		"telegram_on_admin_login":          "1",
	}
	for key, value := range defaults {
		if s.configValue(key, "") == "" {
			if err := s.upsertConfig(key, value); err != nil {
				return nil, err
			}
		}
	}
	if s.configValue("admin_password_hash", "") == "" {
		hash, err := security.CreatePasswordHash(s.Cfg.AdminPassword)
		if err != nil {
			return nil, err
		}
		if err := s.upsertConfig("admin_password_hash", hash); err != nil {
			return nil, err
		}
	}
	if s.configValue("admin_secondary_password_hash", "") == "" {
		hash, err := security.CreatePasswordHash(s.Cfg.AdminSecondaryPassword)
		if err != nil {
			return nil, err
		}
		if err := s.upsertConfig("admin_secondary_password_hash", hash); err != nil {
			return nil, err
		}
	}
	result := map[string]string{}
	var rows []models.AppConfig
	if err := s.DB.Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result, nil
}

func (s *Server) adminLogin(c *gin.Context) {
	var input struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Email) == "" || input.Password == "" {
		fail(c, http.StatusBadRequest, "请输入管理员邮箱和密码")
		return
	}
	if allowed, retryAfter := checkLoginRate(c.ClientIP()); !allowed {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		fail(c, http.StatusTooManyRequests, fmt.Sprintf("登录尝试过多，请 %d 秒后再试", retryAfter))
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil {
		fail(c, http.StatusInternalServerError, "初始化管理员认证配置失败")
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email != strings.ToLower(config["admin_email"]) || !security.VerifyPassword(input.Password, config["admin_password_hash"]) {
		recordLoginFailure(c.ClientIP())
		s.logAdminEvent("login_failed", email, c, "邮箱或密码错误")
		go s.notifyTelegramEvent("admin_login_failed", adminSecurityPayload(c, email, "", "邮箱或密码错误"))
		fail(c, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	clearLoginFailures(c.ClientIP())

	methods := s.available2FAMethods(config)
	methods = resolve2FAMethods(methods, config["admin_2fa_login_mode"])
	if len(methods) > 0 {
		token, payload := security.SignToken(map[string]any{"sub": "admin_login_challenge", "email": email, "pv": config["admin_password_version"], "ip": c.ClientIP(), "fp": firstNonEmpty(input.Fingerprint, c.GetHeader("X-Client-Fingerprint"))}, s.Cfg.AdminTokenSecret, 5*time.Minute)
		c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "requires2fa": true, "challengeToken": token, "methods": methods, "defaultMethod": methods[0], "login2faMode": config["admin_2fa_login_mode"], "email": email, "expiresAt": payload["exp"]})
		return
	}
	s.logAdminEvent("login_success", email, c, "密码登录")
	go s.notifyTelegramEvent("admin_login_success", adminSecurityPayload(c, email, "password_only", "后台登录成功（尚未启用 2FA）"))
	s.writeAdminToken(c, email, config["admin_password_version"])
}

func (s *Server) writeAdminToken(c *gin.Context, email, passwordVersion string) {
	token, payload := security.SignToken(map[string]any{"sub": "admin", "permissions": []string{"admin"}, "email": email, "pv": passwordVersion}, s.Cfg.AdminTokenSecret, 24*time.Hour)
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "token": token, "expiresAt": payload["exp"], "issuedAt": payload["iat"], "permissions": []string{"admin"}, "email": email, "requires2fa": false, "setupRequired": true})
}

func (s *Server) verifyLogin2FA(c *gin.Context) {
	var input struct {
		ChallengeToken string `json:"challengeToken"`
		Method         string `json:"method"`
		Code           string `json:"code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Code) == "" {
		fail(c, http.StatusBadRequest, "请输入验证码")
		return
	}
	if allowed, retryAfter := checkLoginRate(c.ClientIP() + ":2fa"); !allowed {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		fail(c, http.StatusTooManyRequests, fmt.Sprintf("验证尝试过多，请 %d 秒后再试", retryAfter))
		return
	}
	payload, ok := security.VerifyToken(input.ChallengeToken, s.Cfg.AdminTokenSecret, "admin_login_challenge")
	if !ok {
		fail(c, http.StatusUnauthorized, "登录会话已过期，请重新登录")
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil {
		fail(c, http.StatusInternalServerError, "初始化管理员认证配置失败")
		return
	}
	methods := resolve2FAMethods(s.available2FAMethods(config), config["admin_2fa_login_mode"])
	method := strings.ToLower(strings.TrimSpace(input.Method))
	if !containsString(methods, method) {
		fail(c, http.StatusBadRequest, "不支持的验证方式")
		return
	}
	verified := false
	switch method {
	case "totp":
		verified = security.VerifyTOTP(config["admin_totp_secret"], input.Code)
	case "telegram":
		id := strings.TrimSpace(input.ChallengeToken)
		if value, exists := telegramLoginCodes.Load(id); exists {
			entry := value.(loginCode)
			entry.Attempts++
			verified = time.Now().Before(entry.Expires) && entry.Attempts <= 5 && entry.Code == strings.TrimSpace(input.Code)
			if !verified {
				telegramLoginCodes.Store(id, entry)
			} else {
				telegramLoginCodes.Delete(id)
			}
		}
	}
	if !verified {
		recordLoginFailure(c.ClientIP() + ":2fa")
		s.logAdminEvent("2fa_failed", fmtString(payload["email"]), c, "二次验证失败")
		go s.notifyTelegramEvent("admin_2fa_failed", adminSecurityPayload(c, fmtString(payload["email"]), method, "二次验证失败"))
		fail(c, http.StatusUnauthorized, "验证码错误或已过期")
		return
	}
	clearLoginFailures(c.ClientIP() + ":2fa")
	s.logAdminEvent("login_success", fmtString(payload["email"]), c, "二次验证登录")
	go s.notifyTelegramEvent("admin_login_success", adminSecurityPayload(c, fmtString(payload["email"]), method, "后台登录成功"))
	s.writeAdminToken(c, fmtString(payload["email"]), config["admin_password_version"])
}

func (s *Server) sendTelegramCode(c *gin.Context) {
	challenge := strings.TrimSpace(jsonString(c, "challengeToken"))
	payload, ok := security.VerifyToken(challenge, s.Cfg.AdminTokenSecret, "admin_login_challenge")
	if !ok {
		fail(c, http.StatusUnauthorized, "登录会话已过期，请重新登录")
		return
	}
	botToken := s.configValue("telegram_bot_token", "")
	chatID := s.configValue("telegram_admin_chat_id", "")
	if botToken == "" || chatID == "" {
		fail(c, http.StatusBadRequest, "尚未配置管理员 Telegram")
		return
	}
	code := randomDigits(6)
	payloadBody, err := json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    "AutoRecharge 管理员登录验证码: " + code + "\n有效期 5 分钟。",
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成 Telegram 请求失败")
		return
	}
	request, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+botToken+"/sendMessage", strings.NewReader(string(payloadBody)))
	if err != nil {
		fail(c, http.StatusBadGateway, "创建 Telegram 请求失败")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		fail(c, http.StatusBadGateway, "Telegram 请求失败")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fail(c, http.StatusBadGateway, "Telegram 验证码发送失败")
		return
	}
	telegramLoginCodes.Store(challenge, loginCode{Code: code, Expires: time.Now().Add(5 * time.Minute)})
	s.logAdminEvent("2fa_code_sent", fmtString(payload["email"]), c, "Telegram 登录验证码已发送")
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "验证码已发送到管理员 Telegram"})
}

func (s *Server) requireAdminSession(c *gin.Context) {
	if s.hasAdminSession(c) {
		c.Next()
		return
	}
	fail(c, http.StatusUnauthorized, "需要管理员登录")
}

func (s *Server) requireSecondarySession(c *gin.Context) {
	if !s.hasAdminSession(c) {
		fail(c, http.StatusUnauthorized, "需要管理员登录")
		return
	}
	// The legacy application keeps the secondary-password endpoints for
	// compatibility but no longer gates these admin modules behind them.
	c.Next()
}

func (s *Server) hasAdminSession(c *gin.Context) bool {
	if token := strings.TrimSpace(c.GetHeader("X-Admin-Token")); token != "" && token == s.Cfg.AdminAPIToken {
		return true
	}
	if authorized(c, s.Cfg.AdminAPIToken) {
		return true
	}
	token := strings.TrimSpace(c.GetHeader("X-Admin-Token"))
	if token == "" {
		token = strings.TrimSpace(c.GetHeader("Authorization"))
		token = strings.TrimPrefix(token, "Bearer ")
	}
	payload, ok := security.VerifyToken(token, s.Cfg.AdminTokenSecret, "admin")
	if !ok {
		return false
	}
	if fmtString(payload["pv"]) != s.configValue("admin_password_version", "1") {
		return false
	}
	c.Set("adminEmail", fmtString(payload["email"]))
	c.Set("adminPayload", payload)
	return true
}

func (s *Server) verifySecondary(c *gin.Context) {
	var input struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Password == "" {
		fail(c, http.StatusBadRequest, "请输入二级密码")
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil || !security.VerifyPassword(input.Password, config["admin_secondary_password_hash"]) {
		fail(c, http.StatusUnauthorized, "二级密码错误")
		return
	}
	token, payload := security.SignToken(map[string]any{"sub": "admin_secondary", "sv": config["admin_secondary_password_version"], "email": c.GetString("adminEmail")}, s.Cfg.AdminTokenSecret, 30*time.Minute)
	go s.notifyTelegramEvent("admin_secondary_success", adminSecurityPayload(c, firstNonEmpty(c.GetString("adminEmail"), config["admin_email"]), "", "银行卡池/CDK/Session 模块已解锁"))
	// KC-PAY-GPT names this field secondaryToken; keep token as an alias for
	// clients that already consumed the intermediate Go compatibility response.
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "secondaryToken": token, "token": token, "expiresAt": payload["exp"]})
}

func adminSecurityPayload(c *gin.Context, email, method, message string) map[string]string {
	return map[string]string{
		"email":       strings.TrimSpace(email),
		"ip":          c.ClientIP(),
		"fingerprint": strings.TrimSpace(c.GetHeader("X-Client-Fingerprint")),
		"user_agent":  strings.TrimSpace(c.GetHeader("User-Agent")),
		"method":      strings.TrimSpace(method),
		"message":     strings.TrimSpace(message),
	}
}

func resolve2FAMethods(methods []string, mode string) []string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "totp" || mode == "telegram" {
		if containsString(methods, mode) {
			return []string{mode}
		}
	}
	return methods
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func checkLoginRate(key string) (bool, int) {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	now := time.Now()
	entry := loginAttempts.items[key]
	if !entry.LockedUntil.IsZero() && now.Before(entry.LockedUntil) {
		return false, maxInt(1, int(time.Until(entry.LockedUntil).Seconds()+0.999))
	}
	if entry.FirstAt.IsZero() || now.Sub(entry.FirstAt) > loginWindow {
		entry = loginAttempt{FirstAt: now}
	}
	if entry.Count >= loginMaxAttempts {
		entry.LockedUntil = now.Add(loginLock)
		loginAttempts.items[key] = entry
		return false, int(loginLock.Seconds())
	}
	loginAttempts.items[key] = entry
	return true, 0
}

func recordLoginFailure(key string) {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	now := time.Now()
	entry := loginAttempts.items[key]
	if entry.FirstAt.IsZero() || now.Sub(entry.FirstAt) > loginWindow {
		entry = loginAttempt{FirstAt: now}
	}
	entry.Count++
	if entry.Count >= loginMaxAttempts {
		entry.LockedUntil = now.Add(loginLock)
	}
	loginAttempts.items[key] = entry
}

func clearLoginFailures(key string) {
	loginAttempts.Lock()
	delete(loginAttempts.items, key)
	loginAttempts.Unlock()
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (s *Server) adminSession(c *gin.Context) {
	config, err := s.ensureAuthDefaults()
	if err != nil {
		fail(c, http.StatusInternalServerError, "读取管理员会话失败")
		return
	}

	// The legacy client probes this endpoint periodically and expects a fresh
	// token after the first hour of a 24-hour session.
	refreshed := false
	var refreshedToken any
	var issuedAt any
	var expiresAt any
	permissions := []string{"admin"}
	email := firstNonEmpty(c.GetString("adminEmail"), config["admin_email"])
	if rawPayload, exists := c.Get("adminPayload"); exists {
		if payload, ok := rawPayload.(map[string]any); ok {
			email = firstNonEmpty(fmtString(payload["email"]), email)
			issuedAt = payload["iat"]
			expiresAt = payload["exp"]
			if issued, ok := payload["iat"].(float64); ok && time.Since(time.UnixMilli(int64(issued))) >= time.Hour {
				var payloadOut map[string]any
				refreshedToken, payloadOut = security.SignToken(map[string]any{
					"sub": "admin", "permissions": []string{"admin"}, "email": email, "pv": config["admin_password_version"],
				}, s.Cfg.AdminTokenSecret, 24*time.Hour)
				refreshed = true
				issuedAt = payloadOut["iat"]
				expiresAt = payloadOut["exp"]
			}
		}
	}
	loginPath, panelPath := s.adminPaths()
	c.JSON(http.StatusOK, gin.H{
		"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "refreshed": refreshed, "token": refreshedToken,
		"expiresAt": expiresAt, "issuedAt": issuedAt, "permissions": permissions,
		"email": email, "paths": gin.H{"loginPath": loginPath, "panelPath": panelPath},
	})
}

func (s *Server) secondarySession(c *gin.Context) {
	token := strings.TrimSpace(c.GetHeader("X-Admin-Secondary-Token"))
	payload, ok := security.VerifyToken(token, s.Cfg.AdminTokenSecret, "admin_secondary")
	if !ok {
		// Keep the exact legacy unauthenticated response shape. The request
		// trace remains available in X-Trace-ID and server logs.
		c.JSON(http.StatusOK, gin.H{"success": true, "verified": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "verified": true, "expiresAt": payload["exp"]})
}

func (s *Server) available2FAMethods(config map[string]string) []string {
	methods := []string{}
	if config["admin_totp_enabled"] == "1" && config["admin_totp_secret"] != "" {
		methods = append(methods, "totp")
	}
	if config["telegram_bot_token"] != "" && config["telegram_admin_chat_id"] != "" {
		methods = append(methods, "telegram")
	}
	return methods
}

func (s *Server) logAdminEvent(event, email string, c *gin.Context, detail string) {
	_ = s.DB.Create(&models.AdminLoginLog{ID: db.NewID("login"), TraceID: requestTraceID(c), Event: event, AdminEmail: email, IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"), Fingerprint: c.GetHeader("X-Client-Fingerprint"), Detail: detail}).Error
}

func randomDigits(length int) string {
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	result := make([]byte, length)
	for i := range buf {
		result[i] = '0' + (buf[i] % 10)
	}
	return string(result)
}

func randomTOTPSecret() string {
	buf := make([]byte, 20)
	_, _ = rand.Read(buf)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "=")
}

func fmtString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(toString(value)), "<nil>"), "<nil>"))
}

func jsonString(c *gin.Context, key string) string {
	var input map[string]any
	if c.ShouldBindJSON(&input) != nil {
		return ""
	}
	return stringValue(input, key)
}
