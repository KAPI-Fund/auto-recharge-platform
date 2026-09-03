package httpapi

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func taskNotificationEvent(status, errorCode, message string) string {
	if status == models.TaskSucceeded {
		return "success"
	}
	if status != models.TaskFailed && status != models.TaskManual {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(errorCode + " " + message))
	if strings.Contains(normalized, "card_pool_exhausted") || strings.Contains(message, "卡池") || strings.Contains(message, "银行卡池") {
		return "card_pool_empty"
	}
	return "failure"
}

func (s *Server) notifyTelegramEvent(event string, payload map[string]string) {
	config := s.telegramConfig()
	if strings.TrimSpace(config["bot_token"]) == "" {
		return
	}
	if event == "admin_login_success" || event == "admin_login_failed" || event == "admin_2fa_failed" || event == "admin_secondary_success" {
		if config["on_admin_login"] != "1" && !(config["on_admin_login"] == "" && config["notify_admin"] == "1") {
			return
		}
	}
	if event == "success" && config["on_success"] != "1" {
		return
	}
	if event == "failure" && config["on_failure"] != "1" {
		return
	}
	if event == "card_pool_empty" && config["on_card_pool_empty"] != "1" {
		return
	}
	targets := make([]string, 0, 2)
	if config["notify_admin"] == "1" && strings.TrimSpace(config["admin_chat_id"]) != "" {
		targets = append(targets, strings.TrimSpace(config["admin_chat_id"]))
	}
	if config["notify_group"] == "1" && strings.TrimSpace(config["group_chat_id"]) != "" {
		targets = append(targets, strings.TrimSpace(config["group_chat_id"]))
	}
	if len(targets) == 0 {
		return
	}

	title := map[string]string{
		"success":                 "✅ 开通成功",
		"failure":                 "❌ 开通失败",
		"card_pool_empty":         "⚠️ 卡池已耗尽",
		"admin_login_success":     "🔐 后台登录成功",
		"admin_login_failed":      "⛔ 后台登录失败",
		"admin_2fa_failed":        "⛔ 后台二次验证失败",
		"admin_secondary_success": "🔒 敏感模块已解锁",
	}[event]
	lines := []string{title, ""}
	for _, item := range []struct{ label, key string }{
		{"账号", "email"}, {"IP", "ip"}, {"指纹", "fingerprint"}, {"浏览器", "user_agent"}, {"验证方式", "method"},
		{"CDK", "cdk"}, {"任务", "job_key"}, {"详情", "message"},
	} {
		if value := strings.TrimSpace(payload[item.key]); value != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", item.label, html.EscapeString(value)))
		}
	}
	body := map[string]any{
		"chat_id":                  "",
		"text":                     strings.Join(lines, "\n"),
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, chatID := range targets {
		body["chat_id"] = chatID
		raw, err := json.Marshal(body)
		if err != nil {
			continue
		}
		request, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+config["bot_token"]+"/sendMessage", strings.NewReader(string(raw)))
		if err != nil {
			continue
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
		}
	}
}
