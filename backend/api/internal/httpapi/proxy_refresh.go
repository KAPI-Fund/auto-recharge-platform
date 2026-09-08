package httpapi

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

var errNoActiveProxy = errors.New("没有可用代理")

func normalizeRefreshURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", errInvalid("刷新 URL 无效")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errInvalid("刷新 URL 仅支持 http 或 https")
	}
	return parsed.String(), nil
}

func proxyRefreshTimeout(raw string) time.Duration {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 1 {
		parsed = 15
	}
	if parsed > 60 {
		parsed = 60
	}
	return time.Duration(parsed) * time.Second
}

func proxyRefreshWait(raw string) time.Duration {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 0 {
		return 0
	}
	if parsed > 30000 {
		parsed = 30000
	}
	return time.Duration(parsed) * time.Millisecond
}

func applyProxySession(proxyURL, sessionID string) string {
	return strings.ReplaceAll(proxyURL, "{session}", sessionID)
}

func requestProxyRefresh(client *http.Client, refreshURL string) error {
	refreshURL = strings.TrimSpace(refreshURL)
	if refreshURL == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequest(http.MethodGet, refreshURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "auto-recharge-platform/proxy-refresh")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("刷新 IP 失败: HTTP %d", response.StatusCode)
	}
	return nil
}

func proxyExcludeIDs(input map[string]any) []string {
	if input == nil {
		return nil
	}
	raw := input["excludeIds"]
	if raw == nil {
		raw = input["exclude_ids"]
	}
	out := []string{}
	switch typed := raw.(type) {
	case []string:
		for _, id := range typed {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, id)
			}
		}
	case []any:
		for _, item := range typed {
			id := strings.TrimSpace(fmt.Sprint(item))
			if id != "" && id != "<nil>" {
				out = append(out, id)
			}
		}
	}
	return out
}

func orderProxiesForClaim(rows []models.ProxyAsset, excludeIDs []string) []models.ProxyAsset {
	excluded := map[string]bool{}
	for _, id := range excludeIDs {
		if id = strings.TrimSpace(id); id != "" {
			excluded[id] = true
		}
	}
	preferred := make([]models.ProxyAsset, 0, len(rows))
	rest := make([]models.ProxyAsset, 0, len(rows))
	for _, row := range rows {
		if excluded[row.ID] {
			rest = append(rest, row)
			continue
		}
		preferred = append(preferred, row)
	}
	return append(preferred, rest...)
}

func claimProxyFromList(rows []models.ProxyAsset, refresh func(string) error, sessionID string, excludeIDs ...[]string) (models.ProxyAsset, string, error) {
	var skip []string
	if len(excludeIDs) > 0 {
		skip = excludeIDs[0]
	}
	rows = orderProxiesForClaim(rows, skip)
	if len(rows) == 0 {
		return models.ProxyAsset{}, "", errNoActiveProxy
	}
	var lastErr error
	triedRefresh := false
	for _, row := range rows {
		refreshURL := strings.TrimSpace(row.RefreshURL)
		if refreshURL != "" {
			triedRefresh = true
			if refresh == nil {
				lastErr = errors.New("未提供刷新实现")
				continue
			}
			if err := refresh(refreshURL); err != nil {
				lastErr = err
				continue
			}
		}
		return row, applyProxySession(row.ProxyURL, sessionID), nil
	}
	if triedRefresh && lastErr != nil {
		return models.ProxyAsset{}, "", fmt.Errorf("代理刷新 IP 失败: %w", lastErr)
	}
	return models.ProxyAsset{}, "", errNoActiveProxy
}

func proxyAttemptColumn(outcome string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success":
		return "success_count", true
	case "failure", "fail", "failed":
		return "failure_count", true
	default:
		return "", false
	}
}

func proxyStabilityView(success, failure int) (text string, rate int, scored int) {
	if success < 0 {
		success = 0
	}
	if failure < 0 {
		failure = 0
	}
	scored = success + failure
	if scored <= 0 {
		return "—", 0, 0
	}
	rate = int(math.Round(float64(success) / float64(scored) * 100))
	return fmt.Sprintf("%d%%（失败 %d/%d）", rate, failure, scored), rate, scored
}

func (s *Server) recordProxyAttempt(id, outcome string) error {
	id = strings.TrimSpace(id)
	column, ok := proxyAttemptColumn(outcome)
	if id == "" || !ok {
		return nil
	}
	result := s.DB.Model(&models.ProxyAsset{}).Where("id = ?", id).UpdateColumn(column, gorm.Expr(column+" + 1"))
	return result.Error
}

func proxyListSummary(rows []models.ProxyAsset, timeoutSec, waitMs int) gin.H {
	summary := proxySummary(rows)
	summary["refresh_timeout_seconds"] = timeoutSec
	summary["refresh_wait_ms"] = waitMs
	return summary
}

func (s *Server) proxyRefreshSettings() (timeout time.Duration, wait time.Duration, timeoutSec int, waitMs int) {
	timeout = proxyRefreshTimeout(s.configValue("proxy_refresh_timeout_seconds", "15"))
	wait = proxyRefreshWait(s.configValue("proxy_refresh_wait_ms", "0"))
	timeoutSec = int(timeout / time.Second)
	waitMs = int(wait / time.Millisecond)
	return timeout, wait, timeoutSec, waitMs
}

func (s *Server) claimActiveProxy(excludeIDs ...string) (string, string, error) {
	var rows []models.ProxyAsset
	if err := s.DB.Where("active = ?", true).Find(&rows).Error; err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		return "", "", errNoActiveProxy
	}
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	timeout, wait, _, _ := s.proxyRefreshSettings()
	client := &http.Client{Timeout: timeout}
	refresh := func(refreshURL string) error {
		if err := requestProxyRefresh(client, refreshURL); err != nil {
			return err
		}
		if wait > 0 {
			time.Sleep(wait)
		}
		return nil
	}
	sessionID := strings.TrimPrefix(db.NewID("session"), "session_")
	row, proxyURL, err := claimProxyFromList(rows, refresh, sessionID, excludeIDs)
	if err != nil {
		return "", "", err
	}
	updates := map[string]any{"usage_count": gorm.Expr("usage_count + 1")}
	if strings.TrimSpace(row.RefreshURL) != "" {
		now := time.Now()
		ok := true
		updates["last_refresh_at"] = now
		updates["last_refresh_ok"] = ok
		updates["last_refresh_error"] = ""
	}
	if err := s.DB.Model(&models.ProxyAsset{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		return "", "", err
	}
	return row.ID, proxyURL, nil
}

func (s *Server) claimActiveProxyPayload(input map[string]any) (gin.H, error) {
	id, proxyURL, err := s.claimActiveProxy(proxyExcludeIDs(input)...)
	if err != nil {
		if errors.Is(err, errNoActiveProxy) {
			proxyValue := firstNonEmpty(s.configValue("proxy", ""), s.Cfg.OutboundProxy)
			proxyValue = applyProxySession(proxyValue, strings.TrimPrefix(db.NewID("session"), "session_"))
			return gin.H{"proxy": proxyValue, "proxy_url": proxyValue}, nil
		}
		return nil, err
	}
	return gin.H{"proxy": proxyURL, "proxy_url": proxyURL, "id": id}, nil
}
