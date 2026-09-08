package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

var (
	errNoActiveProxy = errors.New("没有可用代理")
	errProxyBusy     = errors.New("代理正在使用")
)

const (
	proxyClaimDeadlineMin = 20 * time.Second
	proxyClaimDeadlineMax = 45 * time.Second
	proxyRefreshErrorMax  = 512
)

func normalizeRefreshURL(value string) (string, error) {
	return normalizeRefreshURLWithPolicy(value, privateRefreshURLsAllowed())
}

func (s *Server) normalizeStoredRefreshURL(value string) (string, error) {
	return normalizeRefreshURLWithPolicy(value, s.allowPrivateRefreshURLs())
}

func normalizeRefreshURLWithPolicy(value string, allowPrivate bool) (string, error) {
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
	if err := denyRefreshURLHostWithPolicy(parsed, allowPrivate); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func privateRefreshURLsAllowed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("PROXY_REFRESH_ALLOW_PRIVATE")), "1")
}

func (s *Server) allowPrivateRefreshURLs() bool {
	if privateRefreshURLsAllowed() {
		return true
	}
	if s == nil {
		return false
	}
	return s.configValue("proxy_refresh_allow_private", "0") == "1"
}

func denyRefreshURLHost(parsed *url.URL) error {
	return denyRefreshURLHostWithPolicy(parsed, privateRefreshURLsAllowed())
}

func denyRefreshURLHostWithPolicy(parsed *url.URL, allowPrivate bool) error {
	if parsed == nil || strings.TrimSpace(parsed.Host) == "" {
		return errInvalid("刷新 URL 无效")
	}
	if allowPrivate {
		return nil
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return errInvalid("刷新 URL 无效")
	}
	if isBlockedRefreshHost(host) {
		return errInvalid("刷新 URL 不允许指向内网或本机地址")
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedRefreshIP(ip) {
		return errInvalid("刷新 URL 不允许指向内网或本机地址")
	}
	return nil
}

func isBlockedRefreshHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	switch host {
	case "localhost", "localhost.localdomain", "metadata", "metadata.google.internal", "metadata.gke.internal":
		return true
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	return false
}

func isBlockedRefreshIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}

func denyRefreshURLResolved(ctx context.Context, parsed *url.URL) error {
	return denyRefreshURLResolvedWithPolicy(ctx, parsed, privateRefreshURLsAllowed())
}

func denyRefreshURLResolvedWithPolicy(ctx context.Context, parsed *url.URL, allowPrivate bool) error {
	if err := denyRefreshURLHostWithPolicy(parsed, allowPrivate); err != nil {
		return err
	}
	if allowPrivate {
		return nil
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("刷新 URL 主机无法解析: %w", err)
	}
	if len(ips) == 0 {
		return errInvalid("刷新 URL 主机无法解析")
	}
	for _, addr := range ips {
		if isBlockedRefreshIP(addr.IP) {
			return errInvalid("刷新 URL 不允许指向内网或本机地址")
		}
	}
	return nil
}

func newProxyRefreshClient(timeout time.Duration, allowPrivate ...bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	allow := len(allowPrivate) > 0 && allowPrivate[0]
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("刷新 URL 重定向次数过多")
			}
			if req == nil || req.URL == nil {
				return errInvalid("刷新 URL 无效")
			}
			if err := denyRefreshURLHostWithPolicy(req.URL, allow); err != nil {
				return err
			}
			return denyRefreshURLResolvedWithPolicy(req.Context(), req.URL, allow)
		},
	}
}

func proxyClaimDeadline(timeout, wait time.Duration) time.Duration {
	per := timeout + wait
	if per < time.Second {
		per = time.Second
	}
	total := per * 2
	if total < proxyClaimDeadlineMin {
		total = proxyClaimDeadlineMin
	}
	if total > proxyClaimDeadlineMax {
		total = proxyClaimDeadlineMax
	}
	return total
}

func truncateProxyError(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= proxyRefreshErrorMax {
		return text
	}
	return text[:proxyRefreshErrorMax]
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
	return requestProxyRefreshContext(context.Background(), client, refreshURL)
}

func requestProxyRefreshContext(ctx context.Context, client *http.Client, refreshURL string) error {
	refreshURL = strings.TrimSpace(refreshURL)
	if refreshURL == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, refreshURL, nil)
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

func (s *Server) doProxyRefresh(ctx context.Context, client *http.Client, refreshURL string) error {
	refreshURL = strings.TrimSpace(refreshURL)
	if refreshURL == "" {
		return nil
	}
	parsed, err := url.Parse(refreshURL)
	if err != nil {
		return errInvalid("刷新 URL 无效")
	}
	allowPrivate := s.allowPrivateRefreshURLs()
	if err := denyRefreshURLHostWithPolicy(parsed, allowPrivate); err != nil {
		return err
	}
	if err := denyRefreshURLResolvedWithPolicy(ctx, parsed, allowPrivate); err != nil {
		return err
	}
	return requestProxyRefreshContext(ctx, client, refreshURL)
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
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errInvalid("代理不存在")
	}
	return nil
}

func (s *Server) saveProxyRefreshResult(id string, ok bool, errText string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	now := time.Now()
	return s.DB.Model(&models.ProxyAsset{}).Where("id = ?", id).Updates(map[string]any{
		"last_refresh_at":    now,
		"last_refresh_ok":    ok,
		"last_refresh_error": truncateProxyError(errText),
	}).Error
}

func proxyLockOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "worker"
	}
	if len(owner) > 96 {
		return owner[:96]
	}
	return owner
}

func (s *Server) tryLockProxyAsset(id, owner string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	now := time.Now()
	cutoff := now.Add(-assetLockStaleAfter)
	result := s.DB.Model(&models.ProxyAsset{}).
		Where("id = ? AND active = ?", id, true).
		Where("in_use = ? OR locked_at IS NULL OR locked_at < ?", false, cutoff).
		Updates(map[string]any{"in_use": true, "locked_at": now, "locked_by": proxyLockOwner(owner)})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *Server) unlockProxyAsset(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return s.DB.Model(&models.ProxyAsset{}).Where("id = ?", id).Updates(map[string]any{
		"in_use": false, "locked_at": nil, "locked_by": "",
	}).Error
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
	return s.claimActiveProxyFor(context.Background(), "worker", excludeIDs...)
}

func (s *Server) claimActiveProxyFor(ctx context.Context, owner string, excludeIDs ...string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout, wait, _, _ := s.proxyRefreshSettings()
	ctx, cancel := context.WithTimeout(ctx, proxyClaimDeadline(timeout, wait))
	defer cancel()

	var rows []models.ProxyAsset
	if err := s.DB.WithContext(ctx).Where("active = ?", true).Find(&rows).Error; err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		return "", "", errNoActiveProxy
	}
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	rows = orderProxiesForClaim(rows, excludeIDs)
	client := newProxyRefreshClient(timeout, s.allowPrivateRefreshURLs())
	sessionID := strings.TrimPrefix(db.NewID("session"), "session_")
	owner = proxyLockOwner(owner)

	var lastErr error
	triedRefresh := false
	skippedBusy := false
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return "", "", fmt.Errorf("代理领取超时: %w", lastErr)
			}
			return "", "", fmt.Errorf("代理领取超时: %w", err)
		}
		locked, lockErr := s.tryLockProxyAsset(row.ID, owner)
		if lockErr != nil {
			return "", "", lockErr
		}
		if !locked {
			skippedBusy = true
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = s.unlockProxyAsset(row.ID)
			return "", "", fmt.Errorf("代理领取超时: %w", err)
		}
		refreshURL := strings.TrimSpace(row.RefreshURL)
		if refreshURL != "" {
			triedRefresh = true
			if err := s.doProxyRefresh(ctx, client, refreshURL); err != nil {
				lastErr = err
				_ = s.saveProxyRefreshResult(row.ID, false, err.Error())
				_ = s.unlockProxyAsset(row.ID)
				continue
			}
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					_ = s.unlockProxyAsset(row.ID)
					if lastErr != nil {
						return "", "", fmt.Errorf("代理领取超时: %w", lastErr)
					}
					return "", "", fmt.Errorf("代理领取超时: %w", ctx.Err())
				case <-timer.C:
				}
				timer.Stop()
			}
			if err := s.saveProxyRefreshResult(row.ID, true, ""); err != nil {
				_ = s.unlockProxyAsset(row.ID)
				return "", "", err
			}
		}
		if err := s.DB.Model(&models.ProxyAsset{}).Where("id = ?", row.ID).UpdateColumn("usage_count", gorm.Expr("usage_count + 1")).Error; err != nil {
			_ = s.unlockProxyAsset(row.ID)
			return "", "", err
		}
		id, proxyURL, err := s.finishProxyClaim(ctx, row.ID, applyProxySession(row.ProxyURL, sessionID))
		if err != nil {
			return "", "", err
		}
		return id, proxyURL, nil
	}
	if triedRefresh && lastErr != nil {
		return "", "", fmt.Errorf("代理刷新 IP 失败: %w", lastErr)
	}
	if skippedBusy {
		return "", "", errProxyBusy
	}
	return "", "", errNoActiveProxy
}

func (s *Server) finishProxyClaim(ctx context.Context, id, proxyURL string) (string, string, error) {
	if err := context.Cause(ctx); err != nil {
		_ = s.unlockProxyAsset(id)
		return "", "", fmt.Errorf("代理领取超时: %w", err)
	}
	return id, proxyURL, nil
}

func envProxyFallbackAllowed(err error) bool {
	return errors.Is(err, errNoActiveProxy)
}

func (s *Server) claimActiveProxyPayload(ctx context.Context, input map[string]any) (gin.H, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	owner := firstNonEmpty(stringValue(input, "ownerKey"), stringValue(input, "taskId"), "worker")
	id, proxyURL, err := s.claimActiveProxyFor(ctx, owner, proxyExcludeIDs(input)...)
	if err != nil {
		if envProxyFallbackAllowed(err) {
			proxyValue := strings.TrimSpace(firstNonEmpty(s.configValue("proxy", ""), s.Cfg.OutboundProxy))
			if proxyValue == "" {
				return nil, errNoActiveProxy
			}
			proxyValue = applyProxySession(proxyValue, strings.TrimPrefix(db.NewID("session"), "session_"))
			return gin.H{"proxy": proxyValue, "proxy_url": proxyValue}, nil
		}
		return nil, err
	}
	id, proxyURL, err = s.finishProxyClaim(ctx, id, proxyURL)
	if err != nil {
		return nil, err
	}
	return gin.H{"proxy": proxyURL, "proxy_url": proxyURL, "id": id}, nil
}
