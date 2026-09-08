package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/paymentregion"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
)

func (s *Server) legacyListProxies(c *gin.Context) {
	var rows []models.ProxyAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, proxyResponse(row))
	}
	_, _, timeoutSec, waitMs := s.proxyRefreshSettings()
	c.JSON(http.StatusOK, gin.H{"success": true, "proxies": items, "summary": proxyListSummary(rows, timeoutSec, waitMs, s.proxyMaxConcurrent(), int(s.proxyRefreshMinInterval()/time.Second))})
}

func proxyResponse(row models.ProxyAsset) gin.H {
	checkLabel, checkTone, ipText, latencyText := proxyCheckView(row)
	refreshURL := strings.TrimSpace(row.RefreshURL)
	maskedRefresh := maskProxy(refreshURL)
	stabilityText, stabilityRate, scored := proxyStabilityView(row.SuccessCount, row.FailureCount)
	return gin.H{
		"id": row.ID, "proxy_url": row.ProxyURL, "proxy_url_masked": maskProxy(row.ProxyURL),
		"refresh_url": maskedRefresh, "refresh_url_masked": maskedRefresh, "has_refresh_url": refreshURL != "",
		"label": row.Label, "protocol": row.Protocol, "host": row.Host, "is_active": row.Active,
		"last_check_at": row.LastCheckAt, "last_check_ok": row.LastCheckOK, "last_check_ip": row.LastCheckIP,
		"last_check_latency_ms": row.LastCheckLatency, "last_check_error": row.LastCheckError,
		"last_refresh_at": row.LastRefreshAt, "last_refresh_ok": row.LastRefreshOK, "last_refresh_error": row.LastRefreshError,
		"usage_count": row.UsageCount, "success_count": row.SuccessCount, "failure_count": row.FailureCount,
		"stability_rate": stabilityRate, "stability_scored": scored, "stability_text": stabilityText,
		"sort_order": row.SortOrder, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		"check_label": checkLabel, "check_tone": checkTone, "ip_text": ipText, "latency_text": latencyText,
	}
}

func proxyCheckView(row models.ProxyAsset) (label, tone, ipText, latencyText string) {
	if row.LastCheckOK == nil {
		return "未检测", "neutral", "—", "—"
	}
	if *row.LastCheckOK {
		ipText = firstNonEmpty(row.LastCheckIP, "-")
		return "活跃", "success", ipText, fmt.Sprintf("%dms", row.LastCheckLatency)
	}
	return "不可用", "warning", firstNonEmpty(row.LastCheckError, "—"), "—"
}

func proxySummary(rows []models.ProxyAsset) gin.H {
	active := 0
	for _, row := range rows {
		if row.Active {
			active++
		}
	}
	return gin.H{"total": len(rows), "active": active, "inactive": len(rows) - active}
}

func (s *Server) legacyAddProxies(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	values := []string{}
	switch raw := input["proxies"].(type) {
	case []any:
		for _, item := range raw {
			values = append(values, fmt.Sprint(item))
		}
	case string:
		values = strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' })
	}
	if len(values) == 0 {
		values = []string{firstNonEmpty(stringValue(input, "proxy_url"), stringValue(input, "proxy"))}
	}
	refreshURL, err := s.normalizeStoredRefreshURL(firstNonEmpty(stringValue(input, "refresh_url"), stringValue(input, "refreshUrl")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	added, skipped := 0, 0
	ids := []string{}
	for _, value := range values {
		proxy, protocol, host, hash, err := normalizeProxy(value)
		if err != nil {
			continue
		}
		var existing models.ProxyAsset
		lookupErr := s.DB.Where("proxy_url_hash = ?", hash).First(&existing).Error
		if lookupErr == nil {
			skipped++
			continue
		}
		if lookupErr != gorm.ErrRecordNotFound {
			fail(c, http.StatusInternalServerError, "检查代理重复状态失败")
			return
		}
		row := models.ProxyAsset{ID: db.NewID("proxy"), ProxyURL: proxy, ProxyURLHash: hash, RefreshURL: refreshURL, Protocol: protocol, Host: host, Label: stringValue(input, "label"), Active: true, SortOrder: time.Now().Nanosecond()}
		if err := s.DB.Create(&row).Error; err != nil {
			continue
		}
		added++
		ids = append(ids, row.ID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "added": added, "skipped": skipped, "ids": ids, "message": fmt.Sprintf("已保存 %d 条代理", added)})
}

func (s *Server) legacyUpdateProxy(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	updates := map[string]any{}
	if _, ok := input["is_active"]; ok {
		updates["active"] = input["is_active"] == true || stringValue(input, "is_active") == "1"
	}
	if _, ok := input["refresh_url"]; ok || input["refreshUrl"] != nil {
		rawRefresh := firstNonEmpty(stringValue(input, "refresh_url"), stringValue(input, "refreshUrl"))
		if strings.Contains(rawRefresh, "******") {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "刷新 URL 不能使用脱敏值覆盖"})
			return
		}
		refreshURL, err := s.normalizeStoredRefreshURL(rawRefresh)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			return
		}
		updates["refresh_url"] = refreshURL
	}
	if _, ok := input["label"]; ok {
		updates["label"] = stringValue(input, "label")
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少可更新字段"})
		return
	}
	result := s.DB.Model(&models.ProxyAsset{}).Where("id = ?", c.Param("id")).Updates(updates)
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "更新代理失败")
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "代理不存在"})
		return
	}
	refreshURL, _ := updates["refresh_url"].(string)
	c.JSON(http.StatusOK, gin.H{"success": true, "id": c.Param("id"), "is_active": updates["active"], "has_refresh_url": strings.TrimSpace(refreshURL) != ""})
}

func (s *Server) legacyRefreshProxy(c *gin.Context) {
	var row models.ProxyAsset
	if err := s.DB.Where("id = ?", c.Param("id")).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "代理不存在"})
		return
	}
	refreshURL := strings.TrimSpace(row.RefreshURL)
	if refreshURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "未配置刷新 URL"})
		return
	}
	timeout, wait, _, _ := s.proxyRefreshSettings()
	ctx, cancel := context.WithTimeout(c.Request.Context(), proxyClaimDeadline(timeout, wait))
	defer cancel()
	err := s.doProxyRefresh(ctx, newProxyRefreshClient(timeout, s.allowPrivateRefreshURLs()), refreshURL)
	now := time.Now()
	ok := err == nil
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	updates := map[string]any{"last_refresh_at": now, "last_refresh_ok": ok, "last_refresh_error": truncateProxyError(errorText)}
	ip := ""
	probeOK := false
	if ok {
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				ok = false
				errorText = "代理刷新等待超时"
				updates["last_refresh_ok"] = false
				updates["last_refresh_error"] = errorText
			case <-timer.C:
			}
			timer.Stop()
		}
	}
	if ok {
		probe := proxyProbe(row.ProxyURL)
		updates["last_check_at"] = now
		updates["last_check_ok"] = probe.OK
		updates["last_check_ip"] = probe.IP
		updates["last_check_latency"] = probe.LatencyMs
		updates["last_check_error"] = probe.Error
		ip = probe.IP
		probeOK = probe.OK
		if !probe.OK {
			ok = false
			errorText = firstNonEmpty(probe.Error, "刷新后代理不可用")
			updates["last_refresh_ok"] = false
			updates["last_refresh_error"] = truncateProxyError(errorText)
		}
	}
	if saveErr := s.DB.Model(&row).Updates(updates).Error; saveErr != nil {
		fail(c, http.StatusInternalServerError, "保存代理刷新结果失败")
		return
	}
	traceID := requestTraceID(c)
	if !ok {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "id": row.ID, "ok": false, "error": errorText, "ip": ip, "traceId": traceID, "trace_id": traceID})
		return
	}
	if !probeOK {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "id": row.ID, "ok": false, "error": errorText, "ip": ip, "traceId": traceID, "trace_id": traceID})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": row.ID, "ok": true, "ip": ip, "traceId": traceID, "trace_id": traceID, "message": "已刷新 IP"})
}

func (s *Server) legacyToggleProxy(c *gin.Context) {
	var row models.ProxyAsset
	if err := s.DB.Where("id = ?", c.Param("id")).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "error": "代理不存在"})
		return
	}
	active := !row.Active
	if err := s.DB.Model(&row).Update("active", active).Error; err != nil {
		fail(c, http.StatusInternalServerError, "切换代理状态失败")
		return
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{"success": true, "id": row.ID, "is_active": active, "traceId": traceID, "trace_id": traceID})
}

func (s *Server) legacyDeleteProxy(c *gin.Context) {
	result := s.DB.Delete(&models.ProxyAsset{}, "id = ?", c.Param("id"))
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "代理不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (s *Server) legacyTestProxy(c *gin.Context) {
	var row models.ProxyAsset
	if err := s.DB.Where("id = ?", c.Param("id")).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "代理不存在"})
		return
	}
	started := time.Now()
	result := proxyProbe(row.ProxyURL)
	latency := int(time.Since(started).Milliseconds())
	now := time.Now()
	result.LatencyMs = latency
	result.CheckedAt = now
	updates := map[string]any{"last_check_at": now, "last_check_ok": result.OK, "last_check_ip": result.IP, "last_check_latency": latency, "last_check_error": result.Error}
	if err := s.DB.Model(&row).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存代理检测结果失败")
		return
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{"success": true, "id": row.ID, "ok": result.OK, "ip": result.IP, "latencyMs": latency, "error": result.Error, "traceId": traceID, "trace_id": traceID})
}

func (s *Server) legacyTestAllProxies(c *gin.Context) {
	var rows []models.ProxyAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取代理列表失败")
		return
	}
	results := make([]gin.H, 0, len(rows))
	activeCount, successCount := 0, 0
	for index := range rows {
		row := &rows[index]
		if row.Active {
			activeCount++
		}
		started := time.Now()
		probe := proxyProbe(row.ProxyURL)
		latency := int(time.Since(started).Milliseconds())
		now := time.Now()
		probe.LatencyMs = latency
		probe.CheckedAt = now
		if err := s.DB.Model(row).Updates(map[string]any{
			"last_check_at": now, "last_check_ok": probe.OK, "last_check_ip": probe.IP,
			"last_check_latency": latency, "last_check_error": probe.Error,
		}).Error; err != nil {
			fail(c, http.StatusInternalServerError, "保存代理检测结果失败")
			return
		}
		if probe.OK {
			successCount++
		}
		results = append(results, gin.H{
			"id": row.ID, "proxy_url_masked": maskProxy(row.ProxyURL), "ok": probe.OK,
			"ip": probe.IP, "latencyMs": latency, "error": probe.Error,
		})
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{
		"success": true, "traceId": traceID, "trace_id": traceID,
		"total": len(rows), "active": activeCount, "checked": len(results), "successCount": successCount,
		"failedCount": len(results) - successCount, "results": results,
		"message": fmt.Sprintf("已完成 %d 条代理检测，%d 条可用", len(results), successCount),
	})
}

func (s *Server) legacyTestProxyDirect(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	proxyValue := firstNonEmpty(stringValue(input, "proxy_url"), stringValue(input, "proxy"))
	if proxyValue == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少代理地址"})
		return
	}
	started := time.Now()
	result := proxyProbe(proxyValue)
	result.LatencyMs = int(time.Since(started).Milliseconds())
	c.JSON(http.StatusOK, gin.H{"success": true, "ok": result.OK, "ip": result.IP, "latencyMs": result.LatencyMs, "error": result.Error})
}

type proxyProbeResult struct {
	OK        bool
	IP        string
	Error     string
	LatencyMs int
	CheckedAt time.Time
}

func proxyProbe(proxyValue string) proxyProbeResult {
	parsed, err := url.Parse(proxyValue)
	if err != nil || parsed.Scheme == "" {
		return proxyProbeResult{Error: "代理 URL 无效"}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(parsed)}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "https://api.ipify.org?format=text", nil)
	response, err := client.Do(request)
	if err != nil {
		return proxyProbeResult{Error: err.Error()}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return proxyProbeResult{Error: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	return proxyProbeResult{OK: true, IP: strings.TrimSpace(string(body))}
}

func maskProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword(parsed.User.Username(), "******")
	}
	return parsed.String()
}

func (s *Server) legacyListAddresses(c *gin.Context) {
	region := strings.ToUpper(strings.TrimSpace(c.Query("region")))
	if region == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少 region 参数"})
		return
	}
	var rows []models.TaxFreeAddress
	if err := s.DB.Where("region = ? AND active = ?", region, true).Order("bound_card_id ASC, id DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, addressResponse(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "addresses": items, "summary": addressSummary(rows)})
}

func (s *Server) legacyCreateAddress(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	result, err := s.internalCreateAddress(input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if success, ok := result["success"].(bool); ok && !success {
		c.JSON(http.StatusBadRequest, result)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) legacyGenerateAddresses(c *gin.Context) {
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	count := 10
	if raw := strings.TrimSpace(stringValue(input, "count")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed != 0 && !math.IsNaN(parsed) {
			switch {
			case parsed < 1:
				count = 1
			case parsed > 100:
				count = 100
			default:
				count = int(math.Ceil(parsed))
			}
		}
	}
	locations := []struct {
		city, state string
		postal      []string
	}{
		{"Portland", "Oregon", []string{"97201", "97205", "97209", "97214"}},
		{"Salem", "Oregon", []string{"97301", "97302", "97306"}},
		{"Eugene", "Oregon", []string{"97401", "97402", "97404"}},
		{"Wilmington", "Delaware", []string{"19801", "19802", "19805"}},
		{"Dover", "Delaware", []string{"19901", "19904"}},
		{"Billings", "Montana", []string{"59101", "59102", "59105"}},
		{"Missoula", "Montana", []string{"59801", "59802"}},
		{"Manchester", "New Hampshire", []string{"03101", "03102", "03104"}},
		{"Nashua", "New Hampshire", []string{"03060", "03062"}},
		{"Anchorage", "Alaska", []string{"99501", "99503", "99508"}},
		{"Fairbanks", "Alaska", []string{"99701", "99709"}},
	}
	streetNames := []string{"Main St", "Oak Ave", "Maple Dr", "Cedar Ln", "Park Blvd", "Washington St", "Lake View Rd", "Highland Ave", "Pine St", "Elm St"}
	ids := []string{}
	for i := 0; i < count; i++ {
		location := locations[rand.Intn(len(locations))]
		address := models.TaxFreeAddress{ID: db.NewID("address"), Region: "US", Line1: fmt.Sprintf("%d %s", rand.Intn(8900)+100, streetNames[rand.Intn(len(streetNames))]), City: location.city, State: location.state, PostalCode: location.postal[rand.Intn(len(location.postal))], Country: "US", Active: true}
		if err := s.DB.Create(&address).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		ids = append(ids, address.ID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "count": len(ids), "ids": ids})
}

func (s *Server) legacyClearAddresses(c *gin.Context) {
	region := strings.ToUpper(firstNonEmpty(c.Query("region"), "US"))
	var unboundBefore int64
	if err := s.DB.Model(&models.TaxFreeAddress{}).Where("region = ? AND active = ? AND (bound_card_id = '' OR bound_card_id IS NULL)", region, true).Count(&unboundBefore).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计未绑定地址失败")
		return
	}
	result := s.DB.Model(&models.TaxFreeAddress{}).Where("region = ? AND active = ? AND (bound_card_id = '' OR bound_card_id IS NULL)", region, true).Update("active", false)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	traceID := requestTraceID(c)
	message := fmt.Sprintf("已清空 %d 条未绑定地址", result.RowsAffected)
	if result.RowsAffected == 0 {
		message = "没有可清空的未绑定地址"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "count": result.RowsAffected, "unboundBefore": unboundBefore, "message": message, "traceId": traceID, "trace_id": traceID})
}

func (s *Server) legacyUpdateAddress(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	input["id"] = c.Param("id")
	result, err := s.internalUpdateAddress(input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if success, ok := result["success"].(bool); ok && !success {
		status := http.StatusBadRequest
		if result["error"] == "地址模板不存在" {
			status = http.StatusNotFound
		} else if strings.HasPrefix(stringValue(result, "error"), "已绑定地址不可") {
			status = http.StatusConflict
		}
		c.JSON(status, result)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) legacyDeleteAddress(c *gin.Context) {
	result, err := s.internalDeleteAddress(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if success, ok := result["success"].(bool); ok && !success {
		status := http.StatusNotFound
		if strings.HasPrefix(stringValue(result, "error"), "已绑定地址不可") {
			status = http.StatusConflict
		}
		c.JSON(status, result)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) gptConfig() map[string]string {
	return map[string]string{"enabled": s.configValue("gpt_api_enabled", "0"), "base_url": s.configValue("gpt_api_base_url", "https://kc.vpss.eu.cc/"), "api_key": s.configValue("gpt_api_key", ""), "plan_key": s.configValue("gpt_api_plan_key", "plus"), "country": s.configValue("gpt_api_country", "PH"), "currency": s.configValue("gpt_api_currency", "PHP")}
}

func (s *Server) legacyGPTConfig(c *gin.Context) {
	config := s.gptConfig()
	key := config["api_key"]
	preview := ""
	if len(key) > 12 {
		preview = key[:8] + "..." + key[len(key)-4:]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "config": gin.H{"enabled": config["enabled"] == "1", "base_url": config["base_url"], "api_key_saved": key != "", "api_key_preview": preview, "plan_key": config["plan_key"], "country": config["country"], "currency": config["currency"]}})
}

func (s *Server) legacySaveGPTConfig(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	config := s.gptConfig()
	if key := stringValue(input, "api_key"); key != "" {
		config["api_key"] = key
	}
	for _, key := range []string{"base_url", "plan_key", "country", "currency"} {
		if value := stringValue(input, key); value != "" {
			config[key] = value
		}
	}
	config["enabled"] = map[bool]string{true: "1", false: "0"}[input["enabled"] == true || stringValue(input, "enabled") == "1"]
	for key, value := range config {
		if err := s.upsertConfig("gpt_api_"+key, value); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "第三方代充 API 配置已保存"})
}

func (s *Server) legacyTestGPTConfig(c *gin.Context) {
	config := s.gptConfig()
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	base := strings.TrimRight(firstNonEmpty(stringValue(input, "base_url"), config["base_url"]), "/")
	key := firstNonEmpty(stringValue(input, "api_key"), config["api_key"])
	plans, status, err := externalJSONRequest(base+"/plans", key)
	if err != nil || status < 200 || status >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": gptUpstreamError(plans, status, err)})
		return
	}
	balance, balanceStatus, balanceErr := externalJSONRequest(base+"/balance", key)
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       fmt.Sprintf("API 连接成功（套餐 %d 个）", len(gptPlanList(plans))),
		"plans":         gptPlanList(plans),
		"gptPlans":      gptPlanList(plans),
		"creditPlans":   gptCreditPlanList(plans),
		"balance":       balanceIfOK(balance, balanceStatus, balanceErr),
		"balanceError":  gptOptionalError(balance, balanceStatus, balanceErr),
		"balanceStatus": balanceStatus,
	})
}

func (s *Server) legacyGPTStatus(c *gin.Context) {
	config := s.gptConfig()
	if config["api_key"] == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "尚未配置第三方代充 API Key", "traceId": requestTraceID(c), "trace_id": requestTraceID(c)})
		return
	}
	base := strings.TrimRight(config["base_url"], "/")
	plans, status, err := externalJSONRequest(base+"/plans", config["api_key"])
	if err != nil || status < 200 || status >= 300 {
		traceID := requestTraceID(c)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "套餐查询失败: " + gptUpstreamError(plans, status, err), "status": status, "traceId": traceID, "trace_id": traceID})
		return
	}
	balance, balanceStatus, balanceErr := externalJSONRequest(base+"/balance", config["api_key"])
	var tasks []models.RechargeTask
	if err := s.DB.Where("gpt_api_order_id <> '' OR gpt_api_task_id <> ''").Order("updated_at DESC").Limit(10).Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取第三方代充任务失败")
		return
	}
	orders := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		orders = append(orders, gin.H{
			"job_key":    task.JobKey,
			"cdk_code":   task.CDKCode,
			"status":     task.Status,
			"message":    task.Message,
			"updated_at": task.UpdatedAt,
			"order_id":   task.GPTAPIOrderID,
			"task_id":    task.GPTAPITaskID,
			"topup_code": nullableString(task.GPTAPITopupCode),
		})
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"traceId":       traceID,
		"trace_id":      traceID,
		"gpt_plans":     gptPlanList(plans),
		"credit_plans":  gptCreditPlanList(plans),
		"balance":       balanceIfOK(balance, balanceStatus, balanceErr),
		"balance_error": gptOptionalError(balance, balanceStatus, balanceErr),
		"status":        status,
		"recent_orders": orders,
	})
}

func gptPlanList(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"gpt", "plans", "data"} {
			if items, ok := object[key].([]any); ok {
				return items
			}
		}
	}
	return []any{}
}

func gptCreditPlanList(value any) []any {
	if object, ok := value.(map[string]any); ok {
		if items, ok := object["credit"].([]any); ok {
			return items
		}
	}
	return []any{}
}

func balanceIfOK(value any, status int, err error) any {
	if err != nil || status < 200 || status >= 300 {
		return nil
	}
	return value
}

func gptOptionalError(value any, status int, err error) string {
	if err == nil && status >= 200 && status < 300 {
		return ""
	}
	return gptUpstreamError(value, status, err)
}

func gptUpstreamError(value any, status int, err error) string {
	if err != nil {
		return err.Error()
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"detail", "message", "error", "msg"} {
			if message := strings.TrimSpace(fmt.Sprint(object[key])); message != "" && message != "<nil>" {
				return message
			}
		}
	}
	return fmt.Sprintf("HTTP %d", status)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func externalJSONRequest(endpoint, key string) (any, int, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-API-Key", key)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
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

func (s *Server) hcaptchaConfig() map[string]string {
	fallbacks := map[string]string{
		"hcaptcha_solver_enabled":           s.Cfg.HcaptchaSolverEnabled,
		"hcaptcha_vlm_api_key":              s.Cfg.HcaptchaVlmAPIKey,
		"hcaptcha_vlm_base_url":             s.Cfg.HcaptchaVlmBaseURL,
		"hcaptcha_vlm_model":                s.Cfg.HcaptchaVlmModel,
		"hcaptcha_vlm_timeout":              s.Cfg.HcaptchaVlmTimeout,
		"hcaptcha_solver_timeout":           s.Cfg.HcaptchaSolverTimeout,
		"hcaptcha_solver_no_vlm":            s.Cfg.HcaptchaSolverNoVlm,
		"hcaptcha_cdp_port":                 s.Cfg.HcaptchaCDPPort,
		"hcaptcha_captcha_platform_api_key": s.Cfg.HcaptchaPlatformAPIKey,
		"hcaptcha_captcha_platform_api_url": s.Cfg.HcaptchaPlatformAPIURL,
		"hcaptcha_captcha_platform_timeout": s.Cfg.HcaptchaPlatformTimeout,
	}
	result := map[string]string{}
	for key, fallback := range fallbacks {
		result[key] = s.configValue(key, fallback)
	}
	return result
}

func secretPreview(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 14 {
		return value[:minInt(4, len(value))] + "..."
	}
	return value[:minInt(10, len(value))] + "…" + value[len(value)-4:]
}

func secretWasMasked(value, existing string) bool {
	value = strings.TrimSpace(value)
	existing = strings.TrimSpace(existing)
	if value == "" || existing == "" {
		return value == ""
	}
	return value == secretPreview(existing) || value == existing[:minInt(8, len(existing))]+"..."
}

func publicHcaptchaConfig(config map[string]string) gin.H {
	vlmKey := strings.TrimSpace(config["hcaptcha_vlm_api_key"])
	platformKey := strings.TrimSpace(config["hcaptcha_captcha_platform_api_key"])
	return gin.H{
		"enabled":                          config["hcaptcha_solver_enabled"] != "0",
		"vlm_api_key":                      "",
		"vlm_api_key_saved":                vlmKey != "",
		"vlm_api_key_preview":              secretPreview(vlmKey),
		"vlm_base_url":                     config["hcaptcha_vlm_base_url"],
		"vlm_model":                        config["hcaptcha_vlm_model"],
		"vlm_timeout":                      config["hcaptcha_vlm_timeout"],
		"solver_timeout":                   config["hcaptcha_solver_timeout"],
		"no_vlm":                           config["hcaptcha_solver_no_vlm"] == "1",
		"cdp_port":                         config["hcaptcha_cdp_port"],
		"captcha_platform_api_key":         "",
		"captcha_platform_api_key_saved":   platformKey != "",
		"captcha_platform_api_key_preview": secretPreview(platformKey),
		"captcha_platform_api_url":         config["hcaptcha_captcha_platform_api_url"],
		"captcha_platform_timeout":         config["hcaptcha_captcha_platform_timeout"],
	}
}

func (s *Server) legacyHcaptchaConfig(c *gin.Context) {
	config := s.hcaptchaConfig()
	view := publicHcaptchaConfig(config)
	c.JSON(http.StatusOK, gin.H{"success": true, "config": view, "raw": view})
}

func (s *Server) legacySaveHcaptcha(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	for _, key := range []string{"enabled", "vlm_api_key", "vlm_base_url", "vlm_model", "vlm_timeout", "solver_timeout", "no_vlm", "cdp_port", "captcha_platform_api_key", "captcha_platform_api_url", "captcha_platform_timeout"} {
		if _, exists := input[key]; !exists {
			continue
		}
		value := stringValue(input, key)
		if key == "vlm_timeout" || key == "solver_timeout" || key == "cdp_port" || key == "captcha_platform_timeout" {
			parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
			if parseErr != nil || parsed < 1 || parsed > 86400 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-86400 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		}
		if key == "enabled" || key == "no_vlm" {
			if input[key] == true || value == "1" {
				value = "1"
			} else {
				value = "0"
			}
		}
		configKey := "hcaptcha_" + key
		switch key {
		case "enabled":
			configKey = "hcaptcha_solver_enabled"
		case "solver_timeout":
			configKey = "hcaptcha_solver_timeout"
		case "no_vlm":
			configKey = "hcaptcha_solver_no_vlm"
		case "cdp_port":
			configKey = "hcaptcha_cdp_port"
		}
		if key == "vlm_api_key" && secretWasMasked(value, s.configValue(configKey, "")) {
			continue
		}
		if key == "captcha_platform_api_key" && secretWasMasked(value, s.configValue(configKey, "")) {
			continue
		}
		if err := s.upsertConfig(configKey, value); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "hCaptcha 求解器配置已保存"})
}

func (s *Server) legacyTestHcaptcha(c *gin.Context) {
	config := s.hcaptchaConfig()
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	if input == nil {
		input = map[string]any{}
	}
	enabled := config["hcaptcha_solver_enabled"] != "0"
	if raw, exists := input["enabled"]; exists {
		enabled = raw == true || stringValue(input, "enabled") == "1" || strings.EqualFold(stringValue(input, "enabled"), "true")
	}
	if !enabled {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "hCaptcha 求解器未启用"})
		return
	}
	noVLM := config["hcaptcha_solver_no_vlm"] == "1"
	if raw, exists := input["no_vlm"]; exists {
		noVLM = boolConfigValue(raw) == "1"
	}
	payload, err := s.browserPoolControl(http.MethodPost, "/hcaptcha/health", gin.H{
		"vlm_api_key":  firstNonEmpty(stringValue(input, "vlm_api_key"), config["hcaptcha_vlm_api_key"]),
		"vlm_base_url": firstNonEmpty(stringValue(input, "vlm_base_url"), config["hcaptcha_vlm_base_url"]),
		"vlm_model":    firstNonEmpty(stringValue(input, "vlm_model"), config["hcaptcha_vlm_model"]),
		"no_vlm":       noVLM,
	}, requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	status, _ := payload.(map[string]any)
	statusValue, _ := status["status"].(map[string]any)
	ready, _ := statusValue["ready"].(bool)
	message := stringFromAny(statusValue["message"])
	if !ready {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": message, "status": statusValue})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message, "status": statusValue})
}

func (s *Server) legacyTestHcaptchaVLM(c *gin.Context) {
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	config := s.hcaptchaConfig()
	key := firstNonEmpty(stringValue(input, "vlm_api_key"), config["hcaptcha_vlm_api_key"])
	payload, err := s.browserPoolControl(http.MethodPost, "/hcaptcha/test-vlm", gin.H{
		"vlm_api_key": key, "vlm_base_url": firstNonEmpty(stringValue(input, "vlm_base_url"), config["hcaptcha_vlm_base_url"]),
		"vlm_model":   firstNonEmpty(stringValue(input, "vlm_model"), config["hcaptcha_vlm_model"]),
		"vlm_timeout": firstNonEmpty(stringValue(input, "vlm_timeout"), config["hcaptcha_vlm_timeout"]),
	}, requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	result := mapValue(payload, "result")
	ok, _ := result["ok"].(bool)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": stringFromAny(result["message"]), "result": result})
		return
	}
	response := gin.H{"success": true}
	for key, value := range result {
		response[key] = value
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) legacyTestHcaptchaPlatform(c *gin.Context) {
	var input map[string]any
	_ = c.ShouldBindJSON(&input)
	config := s.hcaptchaConfig()
	key := firstNonEmpty(stringValue(input, "captcha_platform_api_key"), config["hcaptcha_captcha_platform_api_key"])
	url := firstNonEmpty(stringValue(input, "captcha_platform_api_url"), config["hcaptcha_captcha_platform_api_url"])
	timeout := firstNonEmpty(stringValue(input, "captcha_platform_timeout"), config["hcaptcha_captcha_platform_timeout"])
	payload, err := s.browserPoolControl(http.MethodPost, "/hcaptcha/test-captcha-platform", gin.H{
		"captcha_platform_api_key": key, "captcha_platform_api_url": url, "captcha_platform_timeout": timeout,
	}, requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	result := mapValue(payload, "result")
	ok, _ := result["ok"].(bool)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": stringFromAny(result["message"]), "result": result})
		return
	}
	if resolvedURL := stringFromAny(result["apiUrl"]); resolvedURL != "" && key != "" {
		if err := s.upsertConfig("hcaptcha_captcha_platform_api_key", key); err != nil {
			fail(c, http.StatusInternalServerError, "验证码平台测试成功，但配置保存失败")
			return
		}
		if err := s.upsertConfig("hcaptcha_captcha_platform_api_url", resolvedURL); err != nil {
			fail(c, http.StatusInternalServerError, "验证码平台测试成功，但地址保存失败")
			return
		}
	}
	response := gin.H{"success": true}
	for key, value := range result {
		response[key] = value
	}
	c.JSON(http.StatusOK, response)
}

func mapValue(value any, key string) map[string]any {
	if payload, ok := value.(map[string]any); ok {
		if nested, ok := payload[key].(map[string]any); ok {
			return nested
		}
	}
	return map[string]any{}
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (s *Server) legacyHcaptchaLogs(c *gin.Context) {
	var rows []models.RuntimeLog
	limit := parseConfigInt(c.Query("limit"), 15)
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	if err := s.DB.Where("source = ? OR level = ?", "captcha", "captcha").Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取 hCaptcha 运行日志失败")
		return
	}
	files, outDir := s.hcaptchaLogFiles(limit)
	fileName := filepath.Base(strings.TrimSpace(c.Query("file")))
	if fileName != "" && fileName != "." {
		for _, item := range files {
			if item["name"] != fileName {
				continue
			}
			lines := readTailLines(item["path"].(string), 120)
			c.JSON(http.StatusOK, gin.H{"success": true, "file": fileName, "lines": lines, "out_dir": outDir, "files": files, "runtime": rows})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "日志文件不存在"})
		return
	}
	for _, item := range files {
		delete(item, "path")
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "runtime": rows, "out_dir": outDir, "files": files})
}

func (s *Server) hcaptchaLogFiles(limit int) ([]gin.H, string) {
	root := filepath.Clean(filepath.Join(s.Cfg.RuntimeDir, "hcaptcha"))
	if _, err := os.Stat(root); err != nil {
		legacyRoot := filepath.Clean(filepath.Join(s.Cfg.RuntimeDir, "legacy", "hcaptcha"))
		if _, legacyErr := os.Stat(legacyRoot); legacyErr == nil {
			root = legacyRoot
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return []gin.H{}, root
	}
	files := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isHcaptchaLogFile(entry.Name()) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		files = append(files, gin.H{"name": entry.Name(), "path": filepath.Join(root, entry.Name()), "size": info.Size(), "mtime": info.ModTime().UnixMilli()})
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left]["mtime"].(int64) > files[right]["mtime"].(int64)
	})
	if limit < len(files) {
		files = files[:limit]
	}
	return files, root
}

func isHcaptchaLogFile(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(lower, "solver") && strings.HasSuffix(lower, ".log") {
		return true
	}
	if !strings.HasPrefix(lower, "round_") || !strings.HasSuffix(lower, ".json") {
		return false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(lower, "round_"), ".json")
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func readTailLines(path string, limit int) []string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return []string{"读取失败: " + err.Error()}
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

func (s *Server) telegramConfig() map[string]string {
	result := map[string]string{}
	for _, key := range []string{"bot_token", "admin_chat_id", "group_chat_id", "notify_admin", "notify_group", "on_success", "on_failure", "on_card_pool_empty", "on_admin_login"} {
		result[key] = s.configValue("telegram_"+key, "")
	}
	return result
}

func publicTelegramConfig(config map[string]string) gin.H {
	token := strings.TrimSpace(config["bot_token"])
	return gin.H{
		"bot_token":          "",
		"bot_token_saved":    token != "",
		"bot_token_preview":  secretPreview(token),
		"admin_chat_id":      config["admin_chat_id"],
		"group_chat_id":      config["group_chat_id"],
		"notify_admin":       config["notify_admin"] == "1",
		"notify_group":       config["notify_group"] == "1",
		"on_success":         config["on_success"] == "1",
		"on_failure":         config["on_failure"] == "1",
		"on_card_pool_empty": config["on_card_pool_empty"] == "1",
		"on_admin_login":     config["on_admin_login"] != "0",
	}
}

func (s *Server) legacyTelegramConfig(c *gin.Context) {
	config := s.telegramConfig()
	c.JSON(http.StatusOK, gin.H{"success": true, "config": publicTelegramConfig(config)})
}

func (s *Server) legacySaveTelegram(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	for _, key := range []string{"bot_token", "admin_chat_id", "group_chat_id", "notify_admin", "notify_group", "on_success", "on_failure", "on_card_pool_empty", "on_admin_login"} {
		if _, exists := input[key]; !exists {
			continue
		}
		value := stringValue(input, key)
		if key == "bot_token" && secretWasMasked(value, s.configValue("telegram_bot_token", "")) {
			continue
		}
		if strings.HasPrefix(key, "notify_") || strings.HasPrefix(key, "on_") {
			if input[key] == true || value == "1" {
				value = "1"
			} else {
				value = "0"
			}
		}
		if err := s.upsertConfig("telegram_"+key, value); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Telegram 配置已保存"})
}

func (s *Server) legacyTestTelegram(c *gin.Context) {
	config := s.telegramConfig()
	if config["bot_token"] == "" || config["admin_chat_id"] == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "尚未配置 Telegram Bot"})
		return
	}
	endpoint := "https://api.telegram.org/bot" + config["bot_token"] + "/sendMessage"
	body := strings.NewReader(fmt.Sprintf(`{"chat_id":%q,"text":%q}`, config["admin_chat_id"], "AutoRecharge Telegram 测试消息"))
	request, _ := http.NewRequest(http.MethodPost, endpoint, body)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fail(c, http.StatusBadGateway, fmt.Sprintf("Telegram 接口返回 HTTP %d", response.StatusCode))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "status": response.StatusCode})
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *Server) legacyCheckoutPlans(c *gin.Context) {
	region := strings.ToUpper(s.configValue("payment_region", "PH"))
	if !paymentregion.IsSupported(region) {
		region = paymentregion.Default().Code
	}
	_, label := regionBilling(region)
	plans := gin.H{"plus": "chatgptplusplan", "pro_5x": "chatgptprolite", "pro_20x": "chatgptpro", "go": "chatgptgoplan"}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "plans": plans, "resolved": plans, "planOptions": legacyCheckoutPlanOptions(),
		"region": region, "currency": regionCurrency(region), "label": label, "regionOptions": legacyRegionOptions(),
		"workerMode": s.workerExecutionMode(),
	})
}

func legacyCheckoutPlanOptions() []gin.H {
	return []gin.H{
		{"value": "plus", "code": "plus", "label": "Plus", "planName": "chatgptplusplan"},
		{"value": "pro_5x", "code": "pro_5x", "label": "Pro 5x", "planName": "chatgptprolite"},
		{"value": "pro_20x", "code": "pro_20x", "label": "Pro 20x", "planName": "chatgptpro"},
		{"value": "go", "code": "go", "label": "ChatGPT Go", "planName": "chatgptgoplan"},
	}
}

func (s *Server) legacyCheckoutGenerate(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	session := stringValue(input, "session")
	if session == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请提供 Session JSON"})
		return
	}
	_, token, err := security.NormalizeSessionPayload(session)
	if err != nil || token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Session 无效，无法提取 accessToken"})
		return
	}
	if err := security.ValidateAccessToken(token); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	planType := normalizePlanType(stringValue(input, "plan_type"))
	// Normalize legacy aliases at the API boundary. Older clients may submit
	// the UI plan code as plan_name, while the provider accepts only its
	// canonical enum value.
	planNameOverride := db.NormalizeProviderPlanName(planType, stringValue(input, "plan_name"))
	region := strings.ToUpper(firstNonEmpty(stringValue(input, "country"), stringValue(input, "region"), s.configValue("payment_region", "PH")))
	if !paymentregion.IsSupported(region) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "不支持的地区"})
		return
	}
	var plan models.Plan
	if err := s.DB.Where("code = ? AND active = ?", planType, true).First(&plan).Error; err != nil {
		fail(c, http.StatusBadRequest, "套餐不存在或已下架")
		return
	}
	sessionRaw, sessionToken, normalizeErr := security.NormalizeSessionPayload(session)
	if normalizeErr != nil || strings.TrimSpace(sessionRaw) == "" || strings.TrimSpace(sessionToken) == "" {
		fail(c, http.StatusBadRequest, "Session 无效，无法提取 accessToken")
		return
	}
	ciphertext, err := security.Encrypt(sessionRaw, s.Cfg.SessionEncryptionKey)
	if err != nil {
		fail(c, http.StatusInternalServerError, "Session 加密失败")
		return
	}
	admissionToken, admissionErr := s.acquireAdmission(c.Request.Context())
	if admissionErr != nil {
		if errors.Is(admissionErr, errActivationCapacity) {
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "当前任务过多，请稍后再试"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "当前任务暂不可用，请稍后再试"})
		return
	}
	email := subscriptionEmailFromRaw(sessionRaw, sessionToken)
	now := time.Now()
	queueDeadline := now.Add(s.taskQueuedTimeout())
	mode := checkoutDebugMode(stringValue(input, "mode"), s.workerExecutionMode())
	queuedMessage := "浏览器调试任务已排队"
	if mode == "protocol" {
		queuedMessage = "协议调试任务已排队"
	}
	task := models.RechargeTask{ID: db.NewID("task"), JobKey: db.NewID("job"), TraceID: requestTraceID(c), PlanID: plan.ID, PaymentRegion: region, Mode: mode, TokenPreview: security.SessionPreview(sessionToken), SessionPreview: security.SessionPreview(sessionToken), SessionCiphertext: ciphertext, AdmissionToken: admissionToken, PlanNameOverride: planNameOverride, CDKCode: "[checkout-debug]", Status: models.TaskQueued, Progress: 5, Message: queuedMessage, DisplayTime: now.Format("2006-01-02 15:04:05"), QueueDeadlineAt: &queueDeadline}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.checkActivationCapacityTx(tx); err != nil {
			return err
		}
		return tx.Create(&task).Error
	}); err != nil {
		s.releaseAdmission(admissionToken)
		if errors.Is(err, errActivationCapacity) {
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "当前任务过多，请稍后再试"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	if err := s.Q.Enqueue(c.Request.Context(), queue.TaskMessage{TaskID: task.ID, Mode: mode, TraceID: task.TraceID}); err != nil {
		s.releaseTask(task.ID, "queue_unavailable", "支付链接调试任务入队失败，请稍后重试")
		fail(c, http.StatusServiceUnavailable, "支付链接调试任务队列暂不可用")
		return
	}
	var emailValue any
	if email != "" {
		emailValue = email
	}
	started := "浏览器调试任务已启动，请查看下方运行日志"
	if mode == "protocol" {
		started = "协议调试任务已启动，请查看下方运行日志"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": task.TraceID, "trace_id": task.TraceID, "jobKey": task.JobKey, "mode": mode, "email": emailValue, "message": started})
}

func (s *Server) legacyCheckoutStatus(c *gin.Context) {
	var task models.RechargeTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "任务不存在"})
		return
	}
	status := task.Status
	if status == models.TaskSucceeded {
		status = "success"
	}
	checkoutURL := extractCheckoutURL(task.RawOutput)
	var checkoutValue any
	if checkoutURL != "" {
		checkoutValue = checkoutURL
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "traceId": task.TraceID, "trace_id": task.TraceID, "jobKey": task.JobKey, "mode": task.Mode, "status": status, "message": task.Message, "progress": task.Progress, "errorCode": task.ErrorCode, "error_code": task.ErrorCode, "checkout_url": checkoutValue, "screenshots": mediaPaths(task.RawOutput, task.FailureScreenshots, "screenshot"), "videos": mediaPaths(task.RawOutput, task.FailureScreenshots, "video")})
}

func extractCheckoutURL(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if strings.Contains(line, "CHECKOUT_URL:") {
			return strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	return ""
}

func regionCurrency(region string) string {
	if value, ok := paymentregion.Get(region); ok {
		return value.Currency
	}
	return paymentregion.Default().Currency
}

func normalizeProxyValue(value string) (string, string, string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", "", "", "", fmt.Errorf("代理 URL 无效")
	}
	digest := sha256.Sum256([]byte(value))
	return value, strings.ToLower(parsed.Scheme), parsed.Host, hex.EncodeToString(digest[:]), nil
}
