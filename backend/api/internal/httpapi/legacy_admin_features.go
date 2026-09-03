package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
)

func (s *Server) legacySecurityStatus(c *gin.Context) {
	config, err := s.ensureAuthDefaults()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	methods := s.available2FAMethods(config)
	loginPath, panelPath := s.adminPaths()
	c.JSON(http.StatusOK, gin.H{"success": true, "email": config["admin_email"], "totpEnabled": config["admin_totp_enabled"] == "1", "login2faMode": config["admin_2fa_login_mode"], "login2faModeLabel": login2FAModeLabel(config["admin_2fa_login_mode"]), "availableMethods": methods, "methods": methods, "notifyAdminLogin": config["telegram_on_admin_login"] != "0", "loginPath": loginPath, "panelPath": panelPath, "loginUrl": "/" + loginPath, "panelUrl": "/" + panelPath})
}

func login2FAModeLabel(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "totp":
		return "仅 Google Authenticator"
	case "telegram":
		return "仅 Telegram 验证码"
	default:
		return "登录时可切换"
	}
}

func (s *Server) legacySave2FAMode(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	mode := strings.ToLower(stringValue(input, "mode"))
	if mode != "either" && mode != "totp" && mode != "telegram" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的登录验证方式"})
		return
	}
	if err := s.upsertConfig("admin_2fa_login_mode", mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "login2faMode": mode, "message": "登录验证方式已保存"})
}

func (s *Server) legacySaveAdminPaths(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	loginPath, err := normalizeAdminPath(stringValue(input, "loginPath"), "/admin-login")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	panelPath, err := normalizeAdminPath(stringValue(input, "panelPath"), "/admin")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if loginPath == panelPath {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "登录入口与管理入口不能相同"})
		return
	}
	if err := s.upsertConfig("admin_login_path", loginPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_panel_path", panelPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "loginPath": loginPath, "panelPath": panelPath, "loginUrl": "/" + loginPath, "panelUrl": "/" + panelPath, "message": "入口路径已更新，请使用新地址访问并收藏"})
}

func normalizePath(value, fallback string) string {
	path, err := normalizeAdminPath(value, fallback)
	if err != nil {
		path, _ = normalizeAdminPath(fallback, fallback)
	}
	return "/" + path
}

func normalizeAdminPath(value, fallback string) (string, error) {
	normalize := func(raw string) string {
		raw = strings.ToLower(strings.Trim(strings.TrimSpace(raw), "/"))
		var builder strings.Builder
		lastDash := false
		for _, char := range raw {
			valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-'
			if valid {
				builder.WriteRune(char)
				lastDash = char == '-'
			} else if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		return strings.Trim(builder.String(), "-")
	}

	path := normalize(value)
	if path == "" {
		path = normalize(fallback)
	}
	if len(path) < 2 || len(path) > 32 {
		return "", fmt.Errorf("入口路径长度需在 2-32 个字符之间")
	}
	reserved := map[string]bool{"api": true, "public": true, "static": true, "assets": true, "favicon.svg": true, "index.html": true, "subscription.html": true, "admin-login.html": true, "admin.html": true}
	if reserved[path] {
		return "", fmt.Errorf("路径 %q 为系统保留，请换一个", path)
	}
	return path, nil
}

func (s *Server) legacyTOTPSetup(c *gin.Context) {
	config, err := s.ensureAuthDefaults()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	secret := config["admin_totp_secret"]
	if secret == "" || config["admin_totp_enabled"] == "1" {
		secret = randomTOTPSecret()
		if err := s.upsertConfig("admin_totp_secret", secret); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		if err := s.upsertConfig("admin_totp_enabled", "0"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	email := config["admin_email"]
	otpauth := legacyTOTPURI(email, secret)
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "secret": secret, "otpauthUrl": otpauth, "qrCodeUrl": "https://api.qrserver.com/v1/create-qr-code/?size=220x220&data=" + url.QueryEscape(otpauth), "message": "请使用 Google Authenticator 扫码后输入验证码确认启用"})
}

func legacyTOTPURI(email, secret string) string {
	issuer := "PlusPapay"
	account := strings.TrimSpace(email)
	if account == "" {
		account = "admin"
	}
	normalizedSecret := strings.ToUpper(strings.Join(strings.Fields(secret), ""))
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30", url.QueryEscape(issuer+":"+account), normalizedSecret, url.QueryEscape(issuer))
}

func (s *Server) legacyTOTPConfirm(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	code := strings.TrimSpace(stringValue(input, "code"))
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请输入 Authenticator 验证码"})
		return
	}
	// The secret is generated and persisted by /2fa/setup. Do not trust a
	// client-supplied replacement secret when enabling administrator 2FA.
	secret := strings.TrimSpace(s.configValue("admin_totp_secret", ""))
	if secret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请先发起 2FA 绑定"})
		return
	}
	if !security.VerifyTOTP(secret, code) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "验证码错误，请重试"})
		return
	}
	if err := s.upsertConfig("admin_totp_secret", secret); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_totp_enabled", "1"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "Google Authenticator 已启用"})
}

func (s *Server) legacyTOTPDisable(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if !security.VerifyPassword(stringValue(input, "currentPassword"), config["admin_password_hash"]) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "登录密码错误"})
		return
	}
	if config["admin_totp_enabled"] == "1" && !security.VerifyTOTP(config["admin_totp_secret"], stringValue(input, "code")) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Authenticator 验证码错误"})
		return
	}
	if err := s.upsertConfig("admin_totp_enabled", "0"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_totp_secret", ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "Google Authenticator 已关闭"})
}

func (s *Server) legacyChangePassword(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil {
		fail(c, http.StatusInternalServerError, "读取管理员认证配置失败")
		return
	}
	if !security.VerifyPassword(stringValue(input, "currentPassword"), config["admin_password_hash"]) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "原密码错误"})
		return
	}
	newPassword := stringValue(input, "newPassword")
	if len(newPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "新密码至少 6 位"})
		return
	}
	hash, err := security.CreatePasswordHash(newPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_password_hash", hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_password_version", strconvIncrement(config["admin_password_version"])); err != nil {
		fail(c, http.StatusInternalServerError, "密码已更新但版本保存失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "密码修改成功，请重新登录"})
}

func (s *Server) legacyChangeSecondaryPassword(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	config, err := s.ensureAuthDefaults()
	if err != nil {
		fail(c, http.StatusInternalServerError, "读取二级认证配置失败")
		return
	}
	if !security.VerifyPassword(stringValue(input, "currentPassword"), config["admin_secondary_password_hash"]) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "原二级密码错误"})
		return
	}
	newPassword := stringValue(input, "newPassword")
	if len(newPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "新密码至少 6 位"})
		return
	}
	hash, err := security.CreatePasswordHash(newPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := s.upsertConfig("admin_secondary_password_hash", hash); err != nil {
		fail(c, http.StatusInternalServerError, "保存二级密码失败")
		return
	}
	if err := s.upsertConfig("admin_secondary_password_version", strconvIncrement(config["admin_secondary_password_version"])); err != nil {
		fail(c, http.StatusInternalServerError, "保存二级密码版本失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "二级密码修改成功"})
}

func strconvIncrement(value string) string {
	current := parseConfigInt(value, 1)
	return fmt.Sprintf("%d", current+1)
}

func (s *Server) legacyLoginLogs(c *gin.Context) {
	limit := parseConfigInt(c.Query("limit"), 100)
	offset := parseConfigInt(c.Query("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	var rows []models.AdminLoginLog
	var total int64
	if err := s.DB.Model(&models.AdminLoginLog{}).Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取后台登录日志失败")
		return
	}
	if err := s.DB.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取后台登录日志失败")
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		eventLabel := adminLoginEventLabel(row.Event)
		fingerprintPreview := row.Fingerprint
		if len(fingerprintPreview) > 16 {
			fingerprintPreview = fingerprintPreview[:16]
		}
		userAgentPreview := row.UserAgent
		if len(userAgentPreview) > 40 {
			userAgentPreview = userAgentPreview[:40]
		}
		items = append(items, gin.H{
			"id": row.ID, "traceId": row.TraceID, "trace_id": row.TraceID, "event": row.Event, "admin_email": row.AdminEmail,
			"ip": row.IP, "user_agent": row.UserAgent, "fingerprint": row.Fingerprint,
			"event_label": eventLabel, "fingerprint_preview": fingerprintPreview, "user_agent_preview": userAgentPreview,
			"detail": row.Detail, "created_at": legacyISOTimeString(row.CreatedAt), "created_at_text": legacyTimeString(row.CreatedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "logs": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) legacyRuntimeLogs(c *gin.Context) {
	limit := parseConfigInt(c.Query("limit"), 200)
	if limit < 1 {
		limit = 1
	}
	if limit > 2000 {
		limit = 2000
	}
	after, _ := strconv.ParseInt(strings.TrimSpace(c.Query("after")), 10, 64)
	var rows []models.RuntimeLog
	query := s.DB
	if after > 0 {
		query = query.Where("created_at > ?", time.Unix(0, after))
	}
	if c.Query("tail") == "1" || strings.EqualFold(c.Query("tail"), "true") {
		if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
			fail(c, http.StatusInternalServerError, "读取运行日志失败")
			return
		}
		for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
			rows[left], rows[right] = rows[right], rows[left]
		}
	} else {
		if err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
			fail(c, http.StatusInternalServerError, "读取运行日志失败")
			return
		}
	}
	nextAfter := after
	if len(rows) > 0 {
		nextAfter = rows[len(rows)-1].CreatedAt.UnixNano()
	}
	entries := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		line := fmt.Sprintf("[%s] %s %s %s", legacyTimeString(row.CreatedAt), firstNonEmpty(row.Level, "info"), firstNonEmpty(row.Source, "api"), row.Text)
		entries = append(entries, gin.H{
			"id": row.ID, "traceId": row.TraceID, "trace_id": row.TraceID, "ts": row.CreatedAt.UnixMilli(), "jobKey": row.JobKey, "job_key": row.JobKey,
			"level": row.Level, "source": row.Source, "text": row.Text, "created_at": legacyTimeString(row.CreatedAt), "line": line,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "entries": entries, "logs": entries, "nextAfter": nextAfter})
}

func (s *Server) legacyClearRuntimeLogs(c *gin.Context) {
	result := s.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.RuntimeLog{})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "清空运行日志失败")
		return
	}
	if err := s.DB.Create(&models.RuntimeLog{ID: db.NewID("runtime"), TraceID: requestTraceID(c), Level: "system", Source: "server", Text: "运行日志已手动清空"}).Error; err != nil {
		fail(c, http.StatusInternalServerError, "清空运行日志后写入审计日志失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "deleted": result.RowsAffected, "message": "运行日志已清空"})
}

func (s *Server) legacyTaskLogs(c *gin.Context) {
	page, pageSize, offset := paginationParams(c, 12, 500)
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		pageSize = parseConfigInt(rawLimit, pageSize)
		if pageSize < 1 {
			pageSize = 1
		}
		if pageSize > 500 {
			pageSize = 500
		}
		offset = 0
		page = 1
	}
	var total int64
	if err := taskLogQuery(s.DB).Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计任务日志数量失败")
		return
	}
	var rows []models.RechargeTask
	if err := taskLogQuery(s.DB).Order("created_at DESC, id DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, taskLogResponse(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "logs": items, "tasks": items, "total": total, "page": page, "pageSize": pageSize, "totalPages": pageCount(int(total), pageSize)})
}

func productGenerationTaskLogResponse(row models.ProductGenerationTask) gin.H {
	status := legacyTaskStatus(row.Status)
	timeValue := legacyTimeString(row.CreatedAt)
	return gin.H{
		"id": row.JobKey, "job_key": row.JobKey, "jobKey": row.JobKey,
		"time": timeValue, "token": "ADMIN_PRODUCT_GEN", "token_preview": "ADMIN_PRODUCT_GEN",
		"cdk":      fmt.Sprintf("ADMIN_PRODUCT_GEN:%d", row.TargetCount),
		"cdk_code": fmt.Sprintf("ADMIN_PRODUCT_GEN:%d", row.TargetCount), "status": status,
		"status_label": legacyTaskStatusLabel(status), "status_tone": legacyTaskStatusTone(status),
		"message": row.Message, "progress": row.Progress, "raw_output": row.RawOutput,
		"created_at": timeValue, "updated_at": legacyTimeString(row.UpdatedAt),
		"completed_count": row.CompletedCount, "success_count": row.SuccessCount,
		"failed_count": row.FailedCount, "target_count": row.TargetCount,
	}
}

func taskLogQuery(query *gorm.DB) *gorm.DB {
	return query.Model(&models.RechargeTask{}).
		Where("(cdk_code IS NULL OR cdk_code NOT LIKE ?)", "ADMIN_PRODUCT_GEN:%").
		Where("(cdk_id IS NULL OR cdk_id = '' OR NOT EXISTS (SELECT 1 FROM cdks AS task_cdks WHERE task_cdks.id = recharge_tasks.cdk_id AND COALESCE(task_cdks.type, '') NOT IN (?, ?)))", "", models.CDKTypeSelf)
}

func taskLogResponse(row models.RechargeTask) gin.H {
	status := legacyTaskStatus(row.Status)
	progress := clampProgress(row.Progress)
	automation, phase := legacyAutomationSummary(row.RawOutput)
	message := strings.TrimSpace(row.Message)
	if message == "" && phase != "等待启动" {
		message = phase
	}
	automation["info_lines"] = taskInfoLines(message, phase, browserAnyBool(automation["checkoutOpened"]))
	timeValue := firstNonEmpty(row.DisplayTime, legacyTimeString(row.CreatedAt))
	return gin.H{
		// The short keys are the original KC-PAY-GPT admin.html contract.
		// Keep the database-shaped keys too for newer API consumers.
		"id":                  row.JobKey,
		"traceId":             row.TraceID,
		"trace_id":            row.TraceID,
		"job_key":             row.JobKey,
		"jobKey":              row.JobKey,
		"job_key_short":       legacyShortJobKey(row.JobKey),
		"time":                timeValue,
		"token":               row.TokenPreview,
		"token_preview":       row.TokenPreview,
		"cdk":                 row.CDKCode,
		"cdk_code":            row.CDKCode,
		"phone":               row.Phone,
		"cardLast4":           row.CardLast4,
		"status":              status,
		"status_label":        legacyTaskStatusLabel(status),
		"status_tone":         legacyTaskStatusTone(status),
		"message":             message,
		"progress":            progress,
		"progress_text":       fmt.Sprintf("%d%%", progress),
		"raw_output":          row.RawOutput,
		"created_at":          legacyTimeString(row.CreatedAt),
		"updated_at":          legacyTimeString(row.UpdatedAt),
		"failure_screenshots": row.FailureScreenshots,
		"screenshots":         mediaPaths(row.RawOutput, row.FailureScreenshots, "screenshot"),
		"videos":              mediaPaths(row.RawOutput, row.FailureScreenshots, "video"),
		"automation":          automation,
		"task_info_lines":     automation["info_lines"],
	}
}

func legacyShortJobKey(jobKey string) string {
	parts := strings.Split(jobKey, "-")
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		return parts[1]
	}
	if len(jobKey) > 8 {
		return jobKey[len(jobKey)-8:]
	}
	if jobKey == "" {
		return "-"
	}
	return jobKey
}

func legacyTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func legacyISOTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func legacyOptionalTimeString(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return legacyTimeString(*value)
}

func legacyTaskStatus(status string) string {
	if status == models.TaskSucceeded || status == models.ProductGenerationSucceeded {
		return "success"
	}
	return status
}

func legacyTaskStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded":
		return "SUCCESS"
	case "failed":
		return "FAILED"
	case "running":
		return "RUNNING"
	case "manual":
		return "需人工"
	case "card_invalid":
		return "CARD_INVALID"
	default:
		return strings.ToUpper(strings.TrimSpace(status))
	}
}

func legacyTaskStatusTone(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded":
		return "success"
	case "running", "queued", "processing":
		return "info"
	case "manual", "card_invalid":
		return "warning"
	case "failed":
		return "danger"
	default:
		return "neutral"
	}
}

func adminLoginEventLabel(event string) string {
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "login_success":
		return "登录成功"
	case "login_failed":
		return "登录失败"
	case "2fa_failed":
		return "二次验证失败"
	case "password_changed":
		return "修改密码"
	case "logout":
		return "退出登录"
	default:
		return event
	}
}

func legacyAutomationSummary(raw string) (gin.H, string) {
	contains := func(value string) bool { return strings.Contains(raw, value) }
	paid := contains("PAYMENT_SUCCESS") || contains("最终校验：支付成功")
	checkout := contains("Checkout 页面已打开")
	stripeFormFailed := contains("card_number_not_found") || contains("无法定位信用卡号") || contains("无法定位有效期") || contains("无法定位 CVC")
	phase := "等待启动"
	switch {
	case paid:
		phase = "支付成功"
	case contains("manual_intervention") || contains("需要人工操作") || contains("已连续失败 3 次"):
		phase = "需人工介入"
	case contains("card_number_not_found") || contains("无法定位信用卡号"):
		phase = "Stripe 表单定位失败"
	case checkout:
		phase = "Checkout 已打开"
	case contains("订单创建成功"):
		phase = "订单已创建"
	case contains("创建订单"):
		phase = "创建订单中"
	case contains("正在检查代理"):
		phase = "检查代理中"
	}
	stage := func(key, label string, done, failed bool) gin.H {
		state := "pending"
		if failed {
			state = "failed"
		} else if done {
			state = "done"
		}
		return gin.H{"key": key, "label": label, "done": done, "failed": failed, "state": state, "tone": state}
	}
	checkoutLabel, checkoutTone, checkoutIcon, checkoutClass := "未打开", "neutral", "close", "no"
	if checkout {
		checkoutLabel, checkoutTone, checkoutIcon, checkoutClass = "已打开", "success", "check", "ok"
	}
	return gin.H{
		"phase": phase, "phase_label": phase, "phase_tone": legacyAutomationPhaseTone(phase),
		"checkoutOpened": checkout, "checkout_label": checkoutLabel, "checkout_tone": checkoutTone, "checkout_icon": checkoutIcon, "checkout_class": checkoutClass,
		"paymentStarted": contains("信用卡卡池支付流程") || contains("PaymentRetry"),
		"stages": []gin.H{
			stage("order", "订单", contains("订单创建成功"), contains("订单创建失败") || contains("无法获取支付链接")),
			stage("checkout", "Checkout", checkout, false),
			stage("payment", "支付流程", contains("信用卡卡池支付流程") || contains("正在使用 Stripe"), false),
			stage("card", "预留卡片", contains("已预留卡片"), false),
			stage("stripe_form", "Stripe表单", contains("卡号已填写"), stripeFormFailed),
			stage("paid", "支付成功", paid, false),
		},
	}, phase
}

func legacyAutomationPhaseTone(phase string) string {
	switch phase {
	case "支付成功":
		return "success"
	case "需人工介入", "Stripe 表单定位失败":
		return "warning"
	case "Checkout 已打开", "订单已创建":
		return "info"
	default:
		return "neutral"
	}
}

func taskInfoLines(message, phase string, checkoutOpened bool) []gin.H {
	lines := make([]gin.H, 0, 3)
	if strings.TrimSpace(message) != "" {
		lines = append(lines, gin.H{"kind": "message", "text": message, "tone": "neutral", "class_name": ""})
	}
	if strings.TrimSpace(phase) != "" && phase != message {
		lines = append(lines, gin.H{"kind": "phase", "text": "阶段：" + phase, "tone": "neutral", "class_name": "task-info-phase"})
	}
	if checkoutOpened {
		lines = append(lines, gin.H{"kind": "checkout", "text": "Checkout 已打开", "tone": "success", "class_name": "task-info-checkout"})
	}
	return lines
}

func (s *Server) legacyDeleteTaskLog(c *gin.Context) {
	var productTask models.ProductGenerationTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&productTask).Error; err == nil {
		if err := s.DB.Delete(&productTask).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "deleted": 1, "message": "任务记录已删除"})
		return
	}
	var task models.RechargeTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "任务不存在"})
		return
	}
	if err := s.DB.Delete(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	mediaDeleted := s.deleteTaskMedia(task.RawOutput, task.FailureScreenshots)
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"deleted":      1,
		"mediaDeleted": mediaDeleted,
		"message":      taskDeleteMessage(mediaDeleted),
	})
}

func taskDeleteMessage(mediaDeleted int) string {
	if mediaDeleted > 0 {
		return fmt.Sprintf("任务记录已删除，并清理 %d 个截图/录像文件", mediaDeleted)
	}
	return "任务记录已删除"
}

func (s *Server) deleteTaskMedia(raw, listed string) int {
	paths := append(mediaPaths(raw, listed, "screenshot"), mediaPaths(raw, listed, "video")...)
	deleted := 0
	seen := map[string]bool{}
	root, err := filepath.Abs(filepath.Clean(s.Cfg.RuntimeDir))
	if err != nil {
		return 0
	}
	for _, path := range paths {
		for _, candidate := range mediaCandidates(root, path) {
			if seen[candidate] {
				continue
			}
			seen[candidate] = true
			info, statErr := os.Stat(candidate)
			if statErr != nil || info.IsDir() {
				continue
			}
			if os.Remove(candidate) == nil {
				deleted++
				break
			}
		}
	}
	return deleted
}

func (s *Server) legacyAdminData(c *gin.Context) {
	var cdkTotal, cdkUsed, taskTotal, taskRunning, taskSuccess, taskFailed, cardTotal, cardReady int64
	if err := s.DB.Model(&models.CDK{}).Where("status <> ?", models.CDKDisabled).Count(&cdkTotal).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取 CDK 总数失败")
		return
	}
	if err := s.DB.Model(&models.CDK{}).Where("status = ?", models.CDKUsed).Count(&cdkUsed).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取已使用 CDK 数量失败")
		return
	}
	// The reference dashboard counts every user task except the synthetic
	// ADMIN_PRODUCT_GEN rows. The task-log list applies the additional CDK type
	// filter below, so keep these queries separate.
	taskFilter := func() *gorm.DB {
		return s.DB.Model(&models.RechargeTask{}).Where("(cdk_code IS NULL OR cdk_code NOT LIKE ?)", "ADMIN_PRODUCT_GEN:%")
	}
	if err := taskFilter().Count(&taskTotal).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取任务总数失败")
		return
	}
	if err := taskFilter().Where("status IN ?", []string{models.TaskQueued, models.TaskRunning}).Count(&taskRunning).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取运行中任务数量失败")
		return
	}
	if err := taskFilter().Where("status IN ?", []string{models.TaskSucceeded, "success"}).Count(&taskSuccess).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取成功任务数量失败")
		return
	}
	if err := taskFilter().Where("status IN ?", []string{models.TaskFailed, models.TaskManual, "retry", "card_invalid", "maintenance"}).Count(&taskFailed).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取失败任务数量失败")
		return
	}
	if err := s.DB.Model(&models.CardAsset{}).Count(&cardTotal).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取银行卡总数失败")
		return
	}
	if err := s.DB.Model(&models.CardAsset{}).Where("active = ? AND in_use = ? AND status NOT IN ?", true, false, []string{"报废", "已报废", "disabled"}).Count(&cardReady).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取可用银行卡数量失败")
		return
	}

	type billingCurrencyStat struct {
		Currency  string  `json:"currency"`
		Revenue   float64 `json:"revenue"`
		PaidCount int64   `json:"paid_count"`
	}
	var billingByCurrency []billingCurrencyStat
	if err := s.DB.Model(&models.BillingRecord{}).
		Select("currency, COALESCE(SUM(CASE WHEN status = 'success' THEN amount ELSE 0 END), 0) AS revenue, COUNT(CASE WHEN status = 'success' THEN 1 END) AS paid_count").
		Group("currency").Order("paid_count DESC").Scan(&billingByCurrency).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取账单概览失败")
		return
	}
	primaryCurrency := s.configValue("payment_currency", "USD")
	var billingRevenue float64
	var billingSuccess int64
	if len(billingByCurrency) > 0 {
		primaryCurrency = firstNonEmpty(billingByCurrency[0].Currency, primaryCurrency)
		billingRevenue = billingByCurrency[0].Revenue
		billingSuccess = billingByCurrency[0].PaidCount
	}
	var phones []models.PhoneAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&phones).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取手机号池失败")
		return
	}
	phonePool := make([]gin.H, 0, len(phones))
	for _, phone := range phones {
		view := phoneResponse(phone)
		// Preserve the legacy integer flag for compatibility while reusing the
		// same server-owned status/label/tone view as the dedicated page.
		view["is_active"] = boolToInt(phone.Active)
		phonePool = append(phonePool, view)
	}
	var cards []models.CardAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&cards).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取银行卡池失败")
		return
	}
	cardPool := make([]gin.H, 0, len(cards))
	for _, card := range cards {
		status := card.Status
		if card.Active {
			status = firstNonEmpty(status, "normal")
		}
		cardPool = append(cardPool, gin.H{"id": card.ID, "number": maskedCardNumber(card.Last4), "expiry": maskedCardExpiry, "cvc": maskedCardCVC, "last4": card.Last4, "usage_count": card.UsageCount, "is_active": boolToInt(card.Active), "status": status, "cooldown_until": card.CooldownUntil})
	}
	var products []models.ProductAsset
	if err := s.DB.Order("created_at DESC").Find(&products).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取成品池失败")
		return
	}
	productPool := make([]gin.H, 0, len(products))
	for _, product := range products {
		productPool = append(productPool, productResponse(product))
	}
	var poolEmails []models.PoolEmail
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&poolEmails).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取邮箱池失败")
		return
	}
	poolEmailItems := make([]gin.H, 0, len(poolEmails))
	for _, item := range poolEmails {
		poolEmailItems = append(poolEmailItems, poolEmailResponse(item))
	}
	logs := make([]gin.H, 0)
	var logRows []models.RechargeTask
	if err := taskLogQuery(s.DB).Order("created_at DESC, id DESC").Limit(200).Find(&logRows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取任务日志失败")
		return
	}
	logs = make([]gin.H, 0, len(logRows))
	for _, row := range logRows {
		logs = append(logs, taskLogResponse(row))
	}
	systemMetrics := collectSystemMetrics()
	config := map[string]any{
		"proxy":                               s.configValue("proxy", s.Cfg.OutboundProxy),
		"payment_region":                      s.configValue("payment_region", "PH"),
		"store_debug_mode":                    s.storeDebugMode(),
		"stripe_secret_key_saved":             s.storeStripeSecretKey() != "",
		"stripe_webhook_secret_saved":         s.storeStripeWebhookSecret() != "",
		"stripe_success_url":                  s.storeSuccessURL(),
		"stripe_cancel_url":                   s.storeCancelURL(),
		"public_base_url":                     s.storePublicBaseURL(),
		"max_concurrent_activations":          parseConfigInt(s.configValue("max_concurrent_activations", "1"), 1),
		"max_background_concurrent":           parseConfigInt(s.configValue("max_background_concurrent", "1"), 1),
		"recharge_queued_timeout_seconds":     parseConfigInt(s.configValue("recharge_queued_timeout_seconds", strconv.Itoa(s.Cfg.TaskQueuedTimeoutSeconds)), maxInt(s.Cfg.TaskQueuedTimeoutSeconds, 600)),
		"recharge_task_lease_timeout_seconds": parseConfigInt(s.configValue("recharge_task_lease_timeout_seconds", strconv.Itoa(s.Cfg.TaskLeaseTimeoutSeconds)), maxInt(s.Cfg.TaskLeaseTimeoutSeconds, 60)),
		"maintenance_mode":                    s.configValue("maintenance_mode", "0") == "1",
		"maintenance_mode_drain":              s.configValue("maintenance_mode_drain", "0") == "1",
		"email_source":                        firstNonEmpty(s.configValue("email_source", ""), map[bool]string{true: "pool", false: "random"}[s.configValue("pool_email_enabled", "0") == "1"]),
		"pool_email_enabled":                  s.configValue("pool_email_enabled", "0") == "1",
		"pool_email_imap_host":                firstNonEmpty(s.configValue("pool_email_imap_host", ""), "outlook.office365.com"),
		"pool_email_imap_port":                parseConfigInt(s.configValue("pool_email_imap_port", "993"), 993),
		"pool_email_include_junk":             s.configValue("pool_email_include_junk", "1") == "1",
		"random_email_domain":                 strings.TrimPrefix(firstNonEmpty(s.configValue("random_email_domain", ""), "chiyiyi.cloud"), "@"),
		"inbox_api_base":                      strings.TrimRight(firstNonEmpty(s.configValue("inbox_api_base", ""), "https://temp-email-api.jzqkwl.com"), "/"),
		"inbox_email_domain":                  strings.TrimPrefix(s.configValue("inbox_email_domain", ""), "@"),
		"inbox_email_domains":                 splitConfigList(s.configValue("inbox_email_domains", "")),
		"browser_pool_enabled":                s.configValue("browser_pool_enabled", "0") == "1",
		"card_pool":                           cardPool, "phone_pool": phonePool, "product_pool": productPool, "pool_emails": poolEmailItems,
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c),
		"stats": gin.H{
			"total": taskTotal, "success": taskSuccess, "failed": taskFailed, "running": taskRunning,
			"cdk_total": cdkTotal, "cdk_used": cdkUsed, "cdk_unused": cdkTotal - cdkUsed,
			"task_total": taskTotal, "task_success": taskSuccess, "task_failed": taskFailed,
			"card_total": cardTotal, "card_ready": cardReady,
			"billing_success": billingSuccess, "billing_revenue": billingRevenue, "billing_paid_count": billingSuccess,
			"billing_currency": primaryCurrency, "billing_by_currency": billingByCurrency,
		},
		"config": config, "telegram": publicTelegramConfig(s.telegramConfig()), "hcaptcha": publicHcaptchaConfig(s.hcaptchaConfig()), "gpt": s.gptConfig(),
		"logs":    logs,
		"metrics": adminOverviewMetrics(statsForAdminData(taskTotal, taskSuccess, taskFailed, cdkTotal, cdkUsed, cardTotal, billingRevenue, billingSuccess, primaryCurrency), systemMetrics, config, taskRunning),
		"runtime": gin.H{
			"active_foreground_jobs": taskRunning,
			"active_activation_jobs": taskRunning,
			"browser_pool":           s.browserPoolSnapshot(requestTraceID(c)),
			"system":                 systemMetrics,
		},
	})
}

type adminDataStats struct {
	TaskTotal       int64
	TaskSuccess     int64
	TaskFailed      int64
	CDKTotal        int64
	CDKUsed         int64
	CardTotal       int64
	BillingRevenue  float64
	BillingPaid     int64
	BillingCurrency string
}

func statsForAdminData(taskTotal, taskSuccess, taskFailed, cdkTotal, cdkUsed, cardTotal int64, billingRevenue float64, billingPaid int64, billingCurrency string) adminDataStats {
	return adminDataStats{
		TaskTotal: taskTotal, TaskSuccess: taskSuccess, TaskFailed: taskFailed,
		CDKTotal: cdkTotal, CDKUsed: cdkUsed, CardTotal: cardTotal,
		BillingRevenue: billingRevenue, BillingPaid: billingPaid, BillingCurrency: billingCurrency,
	}
}

// adminOverviewMetrics is the server-side view model for the dashboard cards.
// The browser should not derive operational values from raw database rows or
// host metrics; it only selects the icon/style for each stable metric key.
func adminOverviewMetrics(stats adminDataStats, system map[string]any, config map[string]any, running int64) []gin.H {
	cpu := adminNestedMap(system, "cpu")
	memory := adminNestedMap(system, "memory")
	disk := adminNestedMap(system, "disk")
	uptime := adminNestedMap(system, "uptime")
	maxForeground := intValue(config["max_concurrent_activations"], 1)
	return []gin.H{
		{"key": "cpu", "label": "CPU 占用率", "value": fmt.Sprintf("%v%%", cpu["percent"]), "meta": stringValue(cpu, "text")},
		{"key": "memory", "label": "内存占用率", "value": fmt.Sprintf("%v%%", memory["percent"]), "meta": stringValue(memory, "text")},
		{"key": "disk", "label": "硬盘占用率", "value": fmt.Sprintf("%v%%", disk["percent"]), "meta": fmt.Sprintf("%v/%v (%v)", disk["usedText"], disk["totalText"], disk["drive"])},
		{"key": "uptime", "label": "运行时间", "value": formatUptimeText(intValue64(uptime["seconds"])), "meta": "服务持续运行时间"},
		{"key": "task_total", "label": "总任务量", "value": stats.TaskTotal},
		{"key": "task_success", "label": "成功任务", "value": stats.TaskSuccess},
		{"key": "task_failed", "label": "失败任务", "value": stats.TaskFailed},
		{"key": "cdk_total", "label": "卡密总数", "value": stats.CDKTotal},
		{"key": "cdk_used", "label": "已使用卡密", "value": stats.CDKUsed},
		{"key": "cdk_unused", "label": "未使用卡密", "value": stats.CDKTotal - stats.CDKUsed},
		{"key": "card_total", "label": "银行卡数量", "value": stats.CardTotal},
		{"key": "billing_revenue", "label": "成功收款", "value": fmt.Sprintf("%.2f", stats.BillingRevenue), "meta": fmt.Sprintf("成功账单累计 (%s)", firstNonEmpty(stats.BillingCurrency, "USD"))},
		{"key": "billing_paid_count", "label": "成功支付笔数", "value": stats.BillingPaid},
		{"key": "foreground_slots", "label": "前台并发槽位信息", "value": fmt.Sprintf("%d/%d", running, maxForeground), "meta": "前台占用/最大前台并发"},
	}
}

func adminNestedMap(value map[string]any, key string) map[string]any {
	if nested, ok := value[key].(map[string]any); ok {
		return nested
	}
	return map[string]any{}
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

func intValue64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case uint64:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func formatUptimeText(totalSeconds int64) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if days > 0 {
		return fmt.Sprintf("%d天 %d时 %d分", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d时 %d分 %d秒", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d分 %d秒", minutes, seconds)
	}
	return fmt.Sprintf("%d秒", seconds)
}

func splitConfigList(value string) []string {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t' })
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimPrefix(strings.TrimSpace(item), "@")
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func (s *Server) activeForegroundJobCount() int64 {
	var count int64
	s.DB.Model(&models.RechargeTask{}).
		Where("status IN ?", []string{models.TaskQueued, models.TaskRunning}).
		Count(&count)
	return count
}

func (s *Server) legacyBrowserPool(c *gin.Context) {
	enabled := s.configValue("browser_pool_enabled", "1") == "1"
	activeForegroundJobs := s.activeForegroundJobCount()
	pool, err := s.browserPoolControl(http.MethodGet, "/stats", nil, requestTraceID(c))
	if err != nil {
		view := browserPoolViewModel(map[string]any{}, enabled, collectSystemMetrics(), activeForegroundJobs)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false, "enabled": enabled, "initialized": false, "active": enabled,
			"controlAvailable": false, "mode": gin.H{"enabled": enabled},
			"system":     collectSystemMetrics(),
			"foreground": gin.H{"activeForegroundJobs": activeForegroundJobs},
			"view":       view["view"],
			"message":    err.Error(),
		})
		return
	}
	snapshot := browserPoolViewModel(pool, enabled, collectSystemMetrics(), activeForegroundJobs)
	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"enabled":          enabled,
		"mode":             gin.H{"enabled": enabled},
		"controlAvailable": true,
		"pool":             pool,
		"system":           collectSystemMetrics(),
		"foreground":       gin.H{"activeForegroundJobs": activeForegroundJobs},
		"view":             snapshot["view"],
	})
}

func (s *Server) legacyBrowserPoolMode(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	enabled := input["enabled"] == true || stringValue(input, "mode") == "enabled"
	pool, err := s.browserPoolControl(http.MethodPost, "/mode", gin.H{"enabled": enabled}, requestTraceID(c))
	if err != nil {
		fail(c, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := s.upsertConfig("browser_pool_enabled", map[bool]string{true: "1", false: "0"}[enabled]); err != nil {
		fail(c, http.StatusInternalServerError, "浏览器池状态已切换，但配置持久化失败")
		return
	}
	snapshot := browserPoolViewModel(pool, enabled, collectSystemMetrics(), s.activeForegroundJobCount())
	c.JSON(http.StatusOK, gin.H{
		"success": true, "enabled": enabled, "mode": gin.H{"enabled": enabled},
		"controlAvailable": true, "pool": pool,
		"system":     collectSystemMetrics(),
		"foreground": gin.H{"activeForegroundJobs": s.activeForegroundJobCount()},
		"view":       snapshot["view"],
	})
}

func (s *Server) legacyBrowserPoolReload(c *gin.Context) {
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	body := gin.H{}
	if size := stringValue(input, "size"); size != "" {
		body["size"] = parseConfigInt(size, 1)
	}
	pool, err := s.browserPoolControl(http.MethodPost, "/reload", body, requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "controlAvailable": false, "message": err.Error()})
		return
	}
	enabled := s.configValue("browser_pool_enabled", "1") == "1"
	snapshot := browserPoolViewModel(pool, enabled, collectSystemMetrics(), s.activeForegroundJobCount())
	c.JSON(http.StatusOK, gin.H{
		"success": true, "enabled": enabled, "mode": gin.H{"enabled": enabled},
		"controlAvailable": true, "pool": pool,
		"system":     collectSystemMetrics(),
		"foreground": gin.H{"activeForegroundJobs": s.activeForegroundJobCount()},
		"view":       snapshot["view"],
	})
}

func (s *Server) browserPoolSnapshot(traceID string) gin.H {
	payload, err := s.browserPoolControl(http.MethodGet, "/stats", nil, traceID)
	if err != nil {
		enabled := s.configValue("browser_pool_enabled", "1") == "1"
		view := browserPoolViewModel(map[string]any{}, enabled, collectSystemMetrics(), s.activeForegroundJobCount())
		view["controlAvailable"] = false
		view["error"] = err.Error()
		return view
	}
	enabled := s.configValue("browser_pool_enabled", "1") == "1"
	return browserPoolViewModel(payload, enabled, collectSystemMetrics(), s.activeForegroundJobCount())
}

// browserPoolViewModel owns all derived pool state shown by the admin page.
// The browser only renders this contract; it does not infer capacity or
// operational status from raw Worker fields.
func browserPoolViewModel(payload any, enabled bool, system map[string]any, activeForegroundJobs int64) gin.H {
	pool := map[string]any{}
	if value, ok := payload.(map[string]any); ok {
		for key, item := range value {
			pool[key] = item
		}
	}
	poolEnabled := browserAnyBool(pool["enabled"])
	initialized := browserAnyBool(pool["initialized"])
	ready := poolEnabled && initialized
	statusLabel := "独立模式"
	if enabled {
		statusLabel = "已禁用"
		if poolEnabled && ready {
			statusLabel = "运行中"
		} else if poolEnabled {
			statusLabel = "未就绪"
		}
	}
	configuredSize := browserAnyInt(pool["configuredSize"], browserAnyInt(pool["size"], 0))
	maxPoolSize := browserAnyInt(pool["maxPoolSize"], 24)
	profileBytes := int64(0)
	if slots, ok := pool["slots"].([]any); ok {
		for _, raw := range slots {
			if slot, ok := raw.(map[string]any); ok {
				profileBytes += browserAnyInt64(slot["profileSizeBytes"])
			}
		}
	}
	memory := map[string]any{}
	if value, ok := pool["memory"].(map[string]any); ok {
		for key, item := range value {
			memory[key] = item
		}
	}
	hostTotal := browserAnyFloat(memory["hostTotalGb"])
	hostFree := browserAnyFloat(memory["hostFreeGb"])
	hostUsed := hostTotal - hostFree
	if hostUsed < 0 {
		hostUsed = 0
	}
	suggestedSize := 1
	if hostFree > 0 {
		suggestedSize = int(hostFree / 0.55)
		if suggestedSize < 1 {
			suggestedSize = 1
		}
	}
	profileSize := ""
	if totals, ok := pool["totals"].(map[string]any); ok {
		profileSize = browserAnyString(totals["profileSizeText"])
	}
	if profileSize == "" {
		profileSize = formatBrowserBytes(profileBytes)
	}
	estimatedProcess := fmt.Sprintf("~%d MB", browserAnyInt(pool["size"], 0)*420)
	if totals, ok := pool["totals"].(map[string]any); ok && browserAnyString(totals["estimatedProcessText"]) != "" {
		estimatedProcess = browserAnyString(totals["estimatedProcessText"])
	}
	queue := pool["queue"]
	queueCount := browserAnyInt(pool["waiting"], 0)
	if queueItems, ok := queue.([]any); ok {
		queueCount = len(queueItems)
	}
	normalizeBrowserPoolSlots(pool)
	statusTone := "neutral"
	if enabled && poolEnabled && ready {
		statusTone = "success"
	} else if enabled && poolEnabled {
		statusTone = "warning"
	}
	pool["status_label"] = statusLabel
	pool["status_tone"] = statusTone
	pool["ready"] = ready
	pool["queue_count"] = queueCount
	pool["profile_size_text"] = profileSize
	pool["estimated_process_text"] = estimatedProcess
	pool["host_used_gb"] = hostUsed
	pool["suggested_size"] = suggestedSize
	pool["max_pool_size"] = maxPoolSize
	view := gin.H{
		"statusLabel": statusLabel, "statusTone": statusTone, "statusClass": map[bool]string{true: "is-ready", false: "is-not-ready"}[statusTone == "success"], "ready": ready,
		"modeLabel":      map[bool]string{true: "浏览器池 · 子进程 BROWSER_RUNTIME_MODE=pool", false: "独立启动 · 子进程 BROWSER_RUNTIME_MODE=standalone"}[enabled],
		"configuredSize": configuredSize, "maxPoolSize": maxPoolSize,
		"hostTotalGb": hostTotal, "hostFreeGb": hostFree, "hostUsedGb": hostUsed,
		"profileSizeText": profileSize, "estimatedProcessText": estimatedProcess,
		"suggestedSize": suggestedSize, "sizingHint": browserAnyString(memory["sizingHint"]),
		"queueCount": queueCount, "queue": queue, "activeForegroundJobs": activeForegroundJobs,
	}
	if view["sizingHint"] == "" {
		view["sizingHint"] = fmt.Sprintf("按当前可用资源，建议池大小 ≤ %d（上限 %d）", suggestedSize, maxPoolSize)
	}
	return gin.H{"enabled": enabled, "initialized": initialized, "controlAvailable": true, "pool": pool, "view": view, "system": system, "foreground": gin.H{"activeForegroundJobs": activeForegroundJobs}}
}

func browserAnyBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "1" || strings.EqualFold(typed, "true")
	case float64:
		return typed != 0
	case int:
		return typed != 0
	default:
		return false
	}
}

func browserAnyFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

func browserAnyInt(value any, fallback int) int {
	parsed := int(browserAnyFloat(value))
	if parsed == 0 && fallback != 0 {
		return fallback
	}
	return parsed
}

func browserAnyInt64(value any) int64 { return int64(browserAnyFloat(value)) }

func browserAnyString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizeBrowserPoolSlots(pool map[string]any) {
	slots, ok := pool["slots"].([]any)
	if !ok {
		return
	}
	for _, raw := range slots {
		slot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inUse := browserAnyBool(slot["inUse"])
		pageCount := browserAnyInt(slot["pageCount"], 0)
		jobKey := browserAnyString(slot["jobKey"])
		if jobKey == "" {
			jobKey = "-"
		}
		slot["statusLabel"] = map[bool]string{true: "忙碌", false: "空闲"}[inUse]
		slot["statusTone"] = map[bool]string{true: "warning", false: "success"}[inUse]
		slot["statusClass"] = map[bool]string{true: "is-busy", false: "is-idle"}[inUse]
		slot["pageCountText"] = fmt.Sprintf("%d", pageCount)
		slot["jobKeyText"] = jobKey
		slot["pageSummary"] = fmt.Sprintf("页面数: %d%s", pageCount, map[bool]string{true: " · 任务 " + jobKey, false: ""}[inUse])
		slot["openUrls"] = browserAnyStrings(slot["openUrls"])
	}
}

func browserAnyStrings(value any) []string {
	result := make([]string, 0)
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text := browserAnyString(item); text != "" {
				result = append(result, text)
			}
		}
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func formatBrowserBytes(value int64) string {
	if value >= 1024*1024*1024 {
		return fmt.Sprintf("%.2f GB", float64(value)/(1024*1024*1024))
	}
	if value >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(value)/(1024*1024))
	}
	if value >= 1024 {
		return fmt.Sprintf("%.1f KB", float64(value)/1024)
	}
	return fmt.Sprintf("%d B", value)
}

func (s *Server) browserPoolControl(method, route string, body any, traceIDs ...string) (any, error) {
	base := strings.TrimRight(s.Cfg.BrowserPoolControlURL, "/")
	if base == "" {
		return nil, fmt.Errorf("未配置浏览器池控制地址")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(raw))
	}
	request, err := http.NewRequest(method, base+route, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Worker-Token", s.Cfg.BrowserPoolControlToken)
	if len(traceIDs) > 0 && strings.TrimSpace(traceIDs[0]) != "" {
		request.Header.Set("X-Trace-ID", strings.TrimSpace(traceIDs[0]))
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("浏览器池 Worker 不可用: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil, fmt.Errorf("浏览器池控制端返回了无效响应")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || payload["success"] == false {
		return nil, fmt.Errorf("浏览器池控制失败: %s", string(raw))
	}
	if pool, ok := payload["pool"]; ok {
		return pool, nil
	}
	return payload, nil
}

func (s *Server) legacySessions(c *gin.Context) {
	limit := parseConfigInt(c.Query("limit"), 200)
	if limit > 500 {
		limit = 500
	}
	var rows []models.RechargeTask
	if err := s.DB.
		Where("token_preview <> ''").
		Where("(cdk_code IS NULL OR cdk_code = '' OR cdk_code NOT LIKE ?)", "ADMIN_PRODUCT_GEN:%").
		Where("(cdk_code IS NULL OR cdk_code = '' OR NOT EXISTS (SELECT 1 FROM cdks AS session_cdks WHERE session_cdks.code = recharge_tasks.cdk_code AND session_cdks.type = ?))", models.CDKTypeProduct).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取 Session 列表失败")
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		renewalEligible := (row.Status == models.TaskSucceeded || row.Status == "success") && row.SessionCiphertext != ""
		items = append(items, gin.H{
			"job_key": row.JobKey, "traceId": row.TraceID, "trace_id": row.TraceID, "time": firstNonEmpty(row.DisplayTime, legacyTimeString(row.CreatedAt)),
			"token_preview": row.TokenPreview, "has_session": row.SessionCiphertext != "", "cdk_code": row.CDKCode,
			"card_last4": row.CardLast4, "status": legacyTaskStatus(row.Status), "status_label": legacyTaskStatusLabel(row.Status), "status_tone": legacyTaskStatusTone(row.Status), "message": row.Message,
			"progress": row.Progress, "created_at": legacyTimeString(row.CreatedAt), "updated_at": legacyTimeString(row.UpdatedAt),
			"renewal_eligible": renewalEligible,
			"renewal_visible":  renewalEligible, "renewal_status": map[bool]string{true: "pending", false: "none"}[renewalEligible],
			"renewal_status_label": map[bool]string{true: "查询中…", false: "—"}[renewalEligible], "renewal_status_tone": "neutral",
		})
	}
	// KC-PAY-GPT's admin.html consumes the response as a bare array.
	c.JSON(http.StatusOK, items)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Server) legacySession(c *gin.Context) {
	var row models.RechargeTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Session 记录不存在"})
		return
	}
	secret, err := s.decryptSessionValue(row.SessionCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		fail(c, http.StatusInternalServerError, "Session 数据解密失败")
		return
	}
	status := legacyTaskStatus(row.Status)
	c.JSON(http.StatusOK, gin.H{"success": true, "session": gin.H{
		"job_key": row.JobKey, "traceId": row.TraceID, "trace_id": row.TraceID,
		"token_preview": row.TokenPreview, "session_payload": secret, "cdk_code": row.CDKCode,
		"status": status, "status_label": legacyTaskStatusLabel(status), "status_tone": legacyTaskStatusTone(status),
		"renewal_status_tone": "neutral",
		"message":             row.Message, "progress": row.Progress, "time": firstNonEmpty(row.DisplayTime, legacyTimeString(row.CreatedAt)),
		"created_at": legacyTimeString(row.CreatedAt), "created_at_text": legacyTimeString(row.CreatedAt), "updated_at": legacyTimeString(row.UpdatedAt),
	}})
}

func (s *Server) legacySessionExport(c *gin.Context) {
	var row models.RechargeTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": "Session 记录不存在"})
		return
	}
	secret, err := s.decryptSessionValue(row.SessionCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		fail(c, http.StatusNotFound, "该记录没有完整 Session")
		return
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, row.JobKey)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="session_%s.json"`, name))
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(secret))
}

func (s *Server) legacySessionRenewalAction(c *gin.Context) {
	action := strings.ToLower(strings.TrimSpace(c.Param("action")))
	if action == "enable" {
		action = "resume"
	}
	if action != "cancel" && action != "resume" {
		fail(c, http.StatusBadRequest, "无效的续费操作")
		return
	}
	var row models.RechargeTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&row).Error; err != nil {
		fail(c, http.StatusNotFound, "Session 记录不存在")
		return
	}
	secret, err := s.decryptSessionValue(row.SessionCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		fail(c, http.StatusBadRequest, "该记录没有完整 Session")
		return
	}
	token := extractSubscriptionAccessToken(secret)
	if _, message := validateSubscriptionToken(token); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	result := s.changeSubscriptionRenewal(token, action, subscriptionOffsetMinutes(map[string]any{}), subscriptionEmailFromRaw(secret, token))
	if !result.OK {
		traceID := requestTraceID(c)
		c.JSON(result.StatusCode, gin.H{"success": false, "message": result.Error, "traceId": traceID, "trace_id": traceID})
		return
	}
	data := cloneSubscriptionData(result.Data)
	renewal := renewalView(data)
	data["renewalStatus"] = renewal["status"]
	data["renewalStatusLabel"] = renewal["label"]
	data["renewalStatusTone"] = renewal["tone"]
	data["canCancel"] = renewal["canCancel"]
	data["canEnable"] = renewal["canEnable"]
	traceID := requestTraceID(c)
	message := map[string]string{"cancel": "自动续费已关闭", "resume": "自动续费已开启"}[action]
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data, "result_view": renewalResultView(data, message), "message": message, "traceId": traceID, "trace_id": traceID})
}

func (s *Server) legacySubscriptionCheck(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	rawSession := strings.TrimSpace(firstNonEmpty(stringValue(input, "session"), stringValue(input, "token")))
	token := extractSubscriptionAccessToken(rawSession)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请粘贴 Session JSON 或 AccessToken"})
		return
	}
	profile, message := validateSubscriptionToken(token)
	if message != "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": message})
		return
	}
	result := s.querySubscriptionByToken(token, subscriptionOffsetMinutes(input), firstNonEmpty(subscriptionEmailFromRaw(rawSession, token), profile.Email))
	if !result.OK {
		c.JSON(result.StatusCode, gin.H{"success": false, "message": result.Error})
		return
	}
	data := cloneSubscriptionData(result.Data)
	renewal := renewalView(data)
	data["renewalStatus"] = renewal["status"]
	data["renewalStatusLabel"] = renewal["label"]
	data["renewalStatusTone"] = renewal["tone"]
	data["canCancel"] = renewal["canCancel"]
	data["canEnable"] = renewal["canEnable"]
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data, "result_view": renewalResultView(data, "订阅状态查询完成")})
}

func (s *Server) legacySubscriptionAction(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	rawSession := strings.TrimSpace(firstNonEmpty(stringValue(input, "session"), stringValue(input, "token")))
	token := extractSubscriptionAccessToken(rawSession)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请粘贴 Session JSON 或 AccessToken"})
		return
	}
	action := "cancel"
	if strings.Contains(c.Request.URL.Path, "enable-auto-renew") {
		action = "resume"
	}
	result := s.changeSubscriptionRenewal(token, action, subscriptionOffsetMinutes(input), subscriptionEmailFromRaw(rawSession, token))
	if !result.OK {
		c.JSON(result.StatusCode, gin.H{"success": false, "message": result.Error})
		return
	}
	data := cloneSubscriptionData(result.Data)
	renewal := renewalView(data)
	data["renewalStatus"] = renewal["status"]
	data["renewalStatusLabel"] = renewal["label"]
	data["renewalStatusTone"] = renewal["tone"]
	data["canCancel"] = renewal["canCancel"]
	data["canEnable"] = renewal["canEnable"]
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data, "result_view": renewalResultView(data, stringValue(data, "message"))})
}

func (s *Server) legacyBatchRenewalStatus(c *gin.Context) {
	var input struct {
		JobKeys           []string `json:"job_keys"`
		TimezoneOffsetMin *float64 `json:"timezone_offset_min"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	jobKeys := make([]string, 0, len(input.JobKeys))
	for _, value := range input.JobKeys {
		if jobKey := strings.TrimSpace(value); jobKey != "" {
			jobKeys = append(jobKeys, jobKey)
		}
	}
	if len(jobKeys) > 50 {
		jobKeys = jobKeys[:50]
	}
	if len(jobKeys) == 0 {
		candidates, err := s.renewalCandidateJobKeys(50)
		if err != nil {
			fail(c, http.StatusInternalServerError, "读取可续费 Session 失败")
			return
		}
		jobKeys = candidates
	}
	offset := subscriptionOffsetMinutes(map[string]any{})
	if input.TimezoneOffsetMin != nil {
		offset = int(*input.TimezoneOffsetMin)
	}
	results := map[string]any{}
	var mutex sync.Mutex
	var wait sync.WaitGroup
	cursor := make(chan string)
	workerCount := len(jobKeys)
	if workerCount > 3 {
		workerCount = 3
	}
	queryOne := func(jobKey string) any {
		var row models.RechargeTask
		if err := s.DB.Where("job_key = ?", jobKey).First(&row).Error; err != nil {
			return renewalFailureView("Session 不存在")
		}
		secret, err := s.decryptSessionValue(row.SessionCiphertext)
		if err != nil || strings.TrimSpace(secret) == "" {
			return renewalFailureView("无完整 Session")
		}
		token := extractSubscriptionAccessToken(secret)
		if _, message := validateSubscriptionToken(token); message != "" {
			return renewalFailureView(message)
		}
		result := s.querySubscriptionByToken(token, offset, subscriptionEmailFromRaw(secret, token))
		if !result.OK {
			return renewalFailureView(result.Error)
		}
		data := result.Data
		renewal := renewalView(data)
		return gin.H{
			"ok":                    true,
			"email":                 data["email"],
			"autoRenew":             data["autoRenew"],
			"autoRenewRaw":          data["autoRenewRaw"],
			"hasActiveSubscription": data["hasActiveSubscription"],
			"subscriptionChannel":   data["subscriptionChannel"],
			"renewalStatus":         renewal["status"],
			"renewalStatusLabel":    renewal["label"],
			"renewalStatusTone":     renewal["tone"],
			"renewalDisplayLabel":   renewal["label"],
			"renewalDisplayTone":    renewal["tone"],
			"renewalDisplayTitle":   "",
			"canCancel":             renewal["canCancel"],
			"canEnable":             renewal["canEnable"],
		}
	}
	for i := 0; i < workerCount; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for jobKey := range cursor {
				value := queryOne(jobKey)
				mutex.Lock()
				results[jobKey] = value
				mutex.Unlock()
			}
		}()
	}
	for _, jobKey := range jobKeys {
		cursor <- jobKey
	}
	close(cursor)
	wait.Wait()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": results})
}

// renewalCandidateJobKeys is the server-side selection used when the admin
// page asks for renewal status without an explicit list. Keeping this query
// here makes the eligibility rules testable independently from the worker
// calls and prevents the browser from deciding which sessions are eligible.
func (s *Server) renewalCandidateJobKeys(limit int) ([]string, error) {
	if limit < 1 {
		limit = 50
	}
	var eligible []models.RechargeTask
	if err := s.DB.
		Where("status IN ? AND session_ciphertext <> ''", []string{models.TaskSucceeded, "success"}).
		Where("cdk_code IS NULL OR cdk_code NOT LIKE ?", "ADMIN_PRODUCT_GEN:%").
		Order("updated_at DESC, id DESC").
		Limit(limit).
		Find(&eligible).Error; err != nil {
		return nil, err
	}
	jobKeys := make([]string, 0, len(eligible))
	for _, row := range eligible {
		if jobKey := strings.TrimSpace(row.JobKey); jobKey != "" {
			jobKeys = append(jobKeys, jobKey)
		}
	}
	return jobKeys, nil
}

func renewalView(data map[string]any) gin.H {
	if !subscriptionBool(data["hasActiveSubscription"]) {
		return gin.H{"status": "none", "label": "无订阅", "tone": "neutral", "canCancel": false, "canEnable": false}
	}
	if subscriptionBool(data["autoRenewRaw"]) {
		return gin.H{"status": "enabled", "label": "已开启", "tone": "success", "canCancel": true, "canEnable": false}
	}
	if raw, ok := data["autoRenewRaw"]; ok && raw != nil {
		return gin.H{"status": "disabled", "label": "已关闭", "tone": "neutral", "canCancel": false, "canEnable": true}
	}
	return gin.H{"status": "unknown", "label": "—", "tone": "neutral", "canCancel": false, "canEnable": false}
}

func renewalFailureView(message string) gin.H {
	return gin.H{
		"ok": false, "error": message, "renewalDisplayLabel": "失败", "renewalDisplayTone": "danger",
		"renewalDisplayTitle": message, "canCancel": false, "canEnable": false,
	}
}

func renewalResultView(data map[string]any, message string) gin.H {
	return gin.H{
		"message":            firstNonEmpty(message, stringValue(data, "message"), "操作完成"),
		"email":              stringValue(data, "email"),
		"subscription_label": stringValue(data, "subscriptionChannel"),
		"status":             stringValue(data, "renewalStatus"),
		"status_label":       stringValue(data, "renewalStatusLabel"),
		"status_tone":        stringValue(data, "renewalStatusTone"),
		"expires_at":         stringValue(data, "expiresAtDisplay"),
		"remaining_days":     data["remainingDaysDisplay"],
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func extractAccessToken(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "\"'")
	var payload map[string]any
	if json.Unmarshal([]byte(value), &payload) == nil {
		for _, key := range []string{"accessToken", "access_token", "token"} {
			if token, ok := payload[key].(string); ok {
				return strings.TrimSpace(token)
			}
		}
	}
	return value
}

func openAIRequest(token, method, endpoint string, body any) (any, int, error) {
	requestBody := io.Reader(nil)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		requestBody = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, endpoint, requestBody)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Mozilla/5.0 AutoRecharge")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	var data any
	if json.Unmarshal(raw, &data) != nil {
		data = string(raw)
	}
	return data, response.StatusCode, nil
}

func (s *Server) legacyMediaIndex(c *gin.Context, kind string) {
	var rows []models.RechargeTask
	limit := parseConfigInt(c.Query("limit"), 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	if err := taskLogQuery(s.DB).Order("updated_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取媒体任务索引失败")
		return
	}
	items := []gin.H{}
	for _, row := range rows {
		for _, path := range mediaPaths(row.RawOutput, row.FailureScreenshots, kind) {
			items = append(items, gin.H{"jobKey": row.JobKey, "job_key": row.JobKey, "path": path, "status": row.Status, "updated_at": row.UpdatedAt})
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "items": items, "files": items})
}

func mediaPaths(raw, listed, kind string) []string {
	values := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = normalizeLegacyMediaPath(value)
		if value == "" || seen[value] {
			return
		}
		if kind == "screenshot" && !strings.HasSuffix(strings.ToLower(value), ".png") {
			return
		}
		if kind == "video" && !strings.HasSuffix(strings.ToLower(value), ".webm") {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	for _, source := range []string{raw, listed} {
		var stored []string
		if strings.HasPrefix(strings.TrimSpace(source), "[") {
			_ = json.Unmarshal([]byte(source), &stored)
		}
		for _, value := range stored {
			add(value)
		}
		for _, line := range strings.FieldsFunc(source, func(r rune) bool { return r == '\n' || r == '\r' }) {
			line = strings.TrimSpace(line)
			for _, marker := range []string{"FAILURE_SCREENSHOT:", "SUCCESS_SCREENSHOT:", "LIVE_SCREENSHOT:", "截图已保存:", "SCREENSHOT:", "VIDEO_FILE:"} {
				if index := strings.Index(line, marker); index >= 0 {
					fields := strings.Fields(strings.TrimSpace(line[index+len(marker):]))
					if len(fields) > 0 {
						add(fields[0])
					}
					line = ""
					break
				}
			}
			if line != "" {
				add(line)
			}
		}
	}
	return values
}

// Worker output may contain an absolute path while the persisted media list
// contains the same file relative to runtime/. Keep the legacy API contract
// stable by exposing one canonical path so the admin page does not duplicate
// the same screenshot or recording.
func normalizeLegacyMediaPath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if marker := "debug_screenshots/"; strings.Contains(value, marker) {
		value = value[strings.Index(value, marker)+len(marker):]
	}
	if marker := "/runtime/"; strings.Contains(value, marker) {
		value = value[strings.Index(value, marker)+len(marker):]
	}
	return strings.TrimPrefix(value, "./")
}

func (s *Server) legacyScreenshots(c *gin.Context) {
	if _, exists := c.GetQuery("path"); exists {
		s.serveMedia(c, "screenshot", c.Query("path"))
		return
	}
	s.legacyMediaIndex(c, "screenshot")
}

func (s *Server) legacyVideo(c *gin.Context) {
	if _, exists := c.GetQuery("path"); exists {
		s.serveMedia(c, "video", c.Query("path"))
		return
	}
	s.legacyMediaIndex(c, "video")
}

func (s *Server) legacyMedia(c *gin.Context) {
	subdir := filepath.Base(strings.TrimSpace(c.Param("subdir")))
	name := filepath.Base(strings.TrimSpace(c.Param("filename")))
	if subdir == "." || subdir == ".." || name == "." || name == ".." || strings.Contains(subdir, "..") || strings.Contains(name, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的媒体路径"})
		return
	}
	kind := "screenshot"
	if strings.HasSuffix(strings.ToLower(name), ".webm") {
		kind = "video"
	}
	s.serveMedia(c, kind, filepath.ToSlash(filepath.Join(subdir, name)))
}

func (s *Server) serveMedia(c *gin.Context, kind, value string) {
	value = strings.TrimSpace(value)
	ext := strings.ToLower(value)
	if value == "" || strings.Contains(strings.ReplaceAll(value, "\\", "/"), "..") ||
		(kind == "screenshot" && !strings.HasSuffix(ext, ".png")) ||
		(kind == "video" && !strings.HasSuffix(ext, ".webm")) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的媒体路径"})
		return
	}
	root, err := filepath.Abs(filepath.Clean(s.Cfg.RuntimeDir))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	for _, candidate := range mediaCandidates(root, value) {
		info, statErr := os.Stat(candidate)
		if statErr != nil || info.IsDir() {
			continue
		}
		c.Header("Cache-Control", "private, max-age=3600")
		if kind == "video" {
			c.Header("Content-Type", "video/webm")
		}
		c.File(candidate)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"success": false, "message": map[string]string{"screenshot": "截图不存在", "video": "录像不存在"}[kind]})
}

func mediaCandidates(root, value string) []string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if marker := "debug_screenshots/"; strings.Contains(value, marker) {
		value = value[strings.Index(value, marker)+len(marker):]
	}
	if strings.HasPrefix(value, "激活/") {
		value = "activation/" + strings.TrimPrefix(value, "激活/")
	}
	var candidates []string
	if filepath.IsAbs(value) {
		candidates = append(candidates, filepath.Clean(value))
	} else {
		candidates = []string{
			filepath.Join(root, value),
			filepath.Join(root, "legacy", value),
			filepath.Join(root, "legacy", "debug_screenshots", value),
			filepath.Join(root, "debug_screenshots", value),
			filepath.Join(root, "screenshots", value),
			filepath.Join(root, "legacy", "videos", value),
		}
	}
	result := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result
}
