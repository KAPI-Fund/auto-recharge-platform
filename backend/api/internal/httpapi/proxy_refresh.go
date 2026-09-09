package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	"gorm.io/gorm/clause"
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

func proxyIsIdle(row models.ProxyAsset) bool {
	return !row.InUse || row.InUseCount <= 0
}

func orderProxiesForClaim(rows []models.ProxyAsset, excludeIDs []string) []models.ProxyAsset {
	excluded := map[string]bool{}
	for _, id := range excludeIDs {
		if id = strings.TrimSpace(id); id != "" {
			excluded[id] = true
		}
	}
	idle := make([]models.ProxyAsset, 0, len(rows))
	busy := make([]models.ProxyAsset, 0, len(rows))
	excludedIdle := make([]models.ProxyAsset, 0, len(rows))
	excludedBusy := make([]models.ProxyAsset, 0, len(rows))
	for _, row := range rows {
		skip := excluded[row.ID]
		free := proxyIsIdle(row)
		switch {
		case !skip && free:
			idle = append(idle, row)
		case !skip:
			busy = append(busy, row)
		case free:
			excludedIdle = append(excludedIdle, row)
		default:
			excludedBusy = append(excludedBusy, row)
		}
	}
	out := make([]models.ProxyAsset, 0, len(rows))
	out = append(out, idle...)
	out = append(out, busy...)
	out = append(out, excludedIdle...)
	out = append(out, excludedBusy...)
	return out
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

func proxyMaxConcurrentValue(raw string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 1 {
		return 2
	}
	if parsed > 20 {
		return 20
	}
	return parsed
}

func proxyRefreshMinIntervalValue(raw string) time.Duration {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 0 {
		parsed = 120
	}
	if parsed > 3600 {
		parsed = 3600
	}
	return time.Duration(parsed) * time.Second
}

func proxyNeedsRefresh(lastAt *time.Time, lastOK *bool, minInterval time.Duration) bool {
	if lastAt == nil || lastAt.IsZero() {
		return true
	}
	if lastOK != nil && !*lastOK {
		return true
	}
	if minInterval <= 0 {
		return true
	}
	return time.Since(*lastAt) >= minInterval
}

func shouldRefreshClaimedProxy(firstSlot bool, refreshURL string, lastAt *time.Time, lastOK *bool, minInterval time.Duration, reusePrevious bool) bool {
	if strings.TrimSpace(refreshURL) == "" {
		return false
	}
	if reusePrevious {
		return true
	}
	if !firstSlot {
		return false
	}
	return proxyNeedsRefresh(lastAt, lastOK, minInterval)
}

func proxyClaimRegion(input map[string]any) string {
	return strings.ToUpper(firstNonEmpty(stringValue(input, "region"), stringValue(input, "paymentRegion"), stringValue(input, "payment_region")))
}

func proxyCountryMismatch(didRefresh bool, region, country string) string {
	if !didRefresh {
		return ""
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	country = strings.ToUpper(strings.TrimSpace(country))
	if region == "" || country == "" {
		return ""
	}
	if country == region {
		return ""
	}
	return fmt.Sprintf("代理出口国家 %s 与目标地区 %s 不符", country, region)
}

var lookupIPCountryCode = lookupIPCountryCodeDefault

func lookupIPCountryCodeDefault(ip string) (string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", nil
	}
	request, err := http.NewRequest(http.MethodGet, "http://ip-api.com/json/"+url.PathEscape(ip)+"?fields=status,countryCode", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	var parsed struct {
		Status      string `json:"status"`
		CountryCode string `json:"countryCode"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if !strings.EqualFold(parsed.Status, "success") {
		return "", nil
	}
	return strings.ToUpper(strings.TrimSpace(parsed.CountryCode)), nil
}

func (s *Server) proxyMaxConcurrent() int {
	if s == nil {
		return 2
	}
	return proxyMaxConcurrentValue(s.configValue("proxy_max_concurrent", "2"))
}

func (s *Server) proxyRefreshMinInterval() time.Duration {
	if s == nil {
		return 120 * time.Second
	}
	return proxyRefreshMinIntervalValue(s.configValue("proxy_refresh_min_interval_seconds", "120"))
}

func (s *Server) tryLockProxyAsset(id, owner string) (row models.ProxyAsset, firstSlot bool, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return models.ProxyAsset{}, false, nil
	}
	max := s.proxyMaxConcurrent()
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND active = ?", id, true).First(&row)
		if query.Error == gorm.ErrRecordNotFound {
			row = models.ProxyAsset{}
			return nil
		}
		if query.Error != nil {
			return query.Error
		}
		now := time.Now()
		cutoff := now.Add(-assetLockStaleAfter)
		count := row.InUseCount
		if count < 0 || !row.InUse || row.LockedAt == nil || row.LockedAt.Before(cutoff) {
			count = 0
		}
		if count >= max {
			row = models.ProxyAsset{}
			return nil
		}
		firstSlot = count == 0
		count++
		if err := tx.Model(&row).Updates(map[string]any{
			"in_use_count": count,
			"in_use":       true,
			"locked_at":    now,
			"locked_by":    proxyLockOwner(owner),
		}).Error; err != nil {
			return err
		}
		row.InUseCount = count
		row.InUse = true
		return nil
	})
	return row, firstSlot, err
}

func (s *Server) unlockProxyAsset(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var row models.ProxyAsset
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row)
		if query.Error == gorm.ErrRecordNotFound {
			return nil
		}
		if query.Error != nil {
			return query.Error
		}
		count := row.InUseCount - 1
		if count < 0 {
			count = 0
		}
		updates := map[string]any{"in_use_count": count, "in_use": count > 0}
		if count == 0 {
			updates["locked_at"] = nil
			updates["locked_by"] = ""
		}
		return tx.Model(&row).Updates(updates).Error
	})
}

func proxyListSummary(rows []models.ProxyAsset, timeoutSec, waitMs, maxConcurrent, minIntervalSec int) gin.H {
	summary := proxySummary(rows)
	summary["refresh_timeout_seconds"] = timeoutSec
	summary["refresh_wait_ms"] = waitMs
	summary["max_concurrent"] = maxConcurrent
	summary["refresh_min_interval_seconds"] = minIntervalSec
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
	id, proxyURL, _, err := s.claimActiveProxyFor(context.Background(), "worker", "", excludeIDs...)
	return id, proxyURL, err
}

func (s *Server) claimActiveProxyFor(ctx context.Context, owner, region string, excludeIDs ...string) (string, string, []string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout, wait, _, _ := s.proxyRefreshSettings()
	minInterval := s.proxyRefreshMinInterval()
	region = strings.ToUpper(strings.TrimSpace(region))
	ctx, cancel := context.WithTimeout(ctx, proxyClaimDeadline(timeout, wait))
	defer cancel()

	var notes []string
	note := func(format string, args ...any) {
		msg := strings.TrimSpace(fmt.Sprintf(format, args...))
		if msg == "" {
			return
		}
		notes = append(notes, msg)
		log.Printf("[proxy] %s", msg)
	}

	var rows []models.ProxyAsset
	if err := s.DB.WithContext(ctx).Where("active = ?", true).Find(&rows).Error; err != nil {
		return "", "", notes, err
	}
	if len(rows) == 0 {
		note("代理池为空，将使用本机出口或系统配置代理")
		return "", "", notes, errNoActiveProxy
	}
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	rows = orderProxiesForClaim(rows, excludeIDs)
	excluded := map[string]bool{}
	for _, id := range excludeIDs {
		if id = strings.TrimSpace(id); id != "" {
			excluded[id] = true
		}
	}
	if region != "" {
		note("开始领取代理，目标地区 %s，候选 %d 条", region, len(rows))
	} else {
		note("开始领取代理，候选 %d 条", len(rows))
	}
	client := newProxyRefreshClient(timeout, s.allowPrivateRefreshURLs())
	sessionID := strings.TrimPrefix(db.NewID("session"), "session_")
	owner = proxyLockOwner(owner)

	var lastErr error
	triedRefresh := false
	skippedBusy := false
	for _, candidate := range rows {
		if err := ctx.Err(); err != nil {
			note("领取超时，停止继续尝试")
			if lastErr != nil {
				return "", "", notes, fmt.Errorf("代理领取超时: %w", lastErr)
			}
			return "", "", notes, fmt.Errorf("代理领取超时: %w", err)
		}
		row, firstSlot, lockErr := s.tryLockProxyAsset(candidate.ID, owner)
		if lockErr != nil {
			return "", "", notes, lockErr
		}
		if row.ID == "" {
			skippedBusy = true
			note("代理 %s 已达同时任务上限，跳过", candidate.ID)
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = s.unlockProxyAsset(row.ID)
			note("领取超时，已释放代理 %s", row.ID)
			return "", "", notes, fmt.Errorf("代理领取超时: %w", err)
		}
		refreshURL := strings.TrimSpace(row.RefreshURL)
		didRefresh := shouldRefreshClaimedProxy(firstSlot, refreshURL, row.LastRefreshAt, row.LastRefreshOK, minInterval, excluded[row.ID])
		if didRefresh {
			triedRefresh = true
			if excluded[row.ID] {
				note("地区失败后再次领到代理 %s，强制刷新 IP", row.ID)
			} else {
				note("代理 %s 空闲，开始刷新 IP", row.ID)
			}
			if err := s.doProxyRefresh(ctx, client, refreshURL); err != nil {
				lastErr = err
				note("代理 %s 刷新 IP 失败: %s，释放并换下一条", row.ID, err.Error())
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
					note("刷新后等待超时，已释放代理 %s", row.ID)
					if lastErr != nil {
						return "", "", notes, fmt.Errorf("代理领取超时: %w", lastErr)
					}
					return "", "", notes, fmt.Errorf("代理领取超时: %w", ctx.Err())
				case <-timer.C:
				}
				timer.Stop()
			}
			if err := s.saveProxyRefreshResult(row.ID, true, ""); err != nil {
				_ = s.unlockProxyAsset(row.ID)
				return "", "", notes, err
			}
			note("代理 %s 刷新 IP 成功", row.ID)
		} else if refreshURL == "" {
			note("代理 %s 未配置刷新 URL，直接使用", row.ID)
		} else if !firstSlot {
			note("代理 %s 已有任务在用，跳过刷新，复用当前 IP", row.ID)
		} else {
			note("代理 %s 在 %d 秒刷新间隔内，跳过刷新，复用当前 IP", row.ID, int(minInterval/time.Second))
		}
		proxyURL := applyProxySession(row.ProxyURL, sessionID)
		if didRefresh && region != "" {
			probe := proxyProbe(proxyURL)
			if !probe.OK {
				lastErr = fmt.Errorf("刷新后探测失败: %s", firstNonEmpty(probe.Error, "未知错误"))
				note("代理 %s 刷新后探测失败: %s，释放并换下一条", row.ID, firstNonEmpty(probe.Error, "未知错误"))
				_ = s.unlockProxyAsset(row.ID)
				continue
			}
			country, _ := lookupIPCountryCode(probe.IP)
			if reason := proxyCountryMismatch(true, region, country); reason != "" {
				lastErr = errors.New(reason)
				note("代理 %s 刷新后出口 %s（IP %s），目标 %s，不匹配，释放并重新领取", row.ID, firstNonEmpty(country, "未知"), firstNonEmpty(probe.IP, "-"), region)
				_ = s.unlockProxyAsset(row.ID)
				continue
			}
			note("代理 %s 刷新后出口 %s（IP %s），与目标地区 %s 一致，继续", row.ID, firstNonEmpty(country, "未知"), firstNonEmpty(probe.IP, "-"), region)
		}
		if err := s.DB.Model(&models.ProxyAsset{}).Where("id = ?", row.ID).UpdateColumn("usage_count", gorm.Expr("usage_count + 1")).Error; err != nil {
			_ = s.unlockProxyAsset(row.ID)
			return "", "", notes, err
		}
		id, proxyURL, err := s.finishProxyClaim(ctx, row.ID, proxyURL)
		if err != nil {
			note("领取完成前请求已取消，已释放代理 %s", row.ID)
			return "", "", notes, err
		}
		note("已领取代理 %s", id)
		return id, proxyURL, notes, nil
	}
	if triedRefresh && lastErr != nil {
		note("所有可刷新代理均失败: %s", lastErr.Error())
		return "", "", notes, fmt.Errorf("代理刷新 IP 失败: %w", lastErr)
	}
	if skippedBusy {
		note("所有代理都在使用中")
		return "", "", notes, errProxyBusy
	}
	note("没有可用代理")
	return "", "", notes, errNoActiveProxy
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
	taskID := firstNonEmpty(stringValue(input, "taskId"), stringValue(input, "task_id"))
	id, proxyURL, notes, err := s.claimActiveProxyFor(ctx, owner, proxyClaimRegion(input), proxyExcludeIDs(input)...)
	if envProxyFallbackAllowed(err) {
		proxyValue := strings.TrimSpace(firstNonEmpty(s.configValue("proxy", ""), s.Cfg.OutboundProxy))
		if proxyValue != "" {
			proxyValue = applyProxySession(proxyValue, strings.TrimPrefix(db.NewID("session"), "session_"))
			notes = append(notes, "代理池为空，使用系统配置代理")
		} else {
			notes = append(notes, "代理池为空，使用本机出口直连")
		}
		s.writeProxyClaimLogs(taskID, notes)
		return gin.H{"proxy": proxyValue, "proxy_url": proxyValue, "logs": notes, "message": strings.Join(notes, "\n")}, nil
	}
	if err != nil {
		s.writeProxyClaimLogs(taskID, notes)
		if len(notes) > 0 {
			return nil, fmt.Errorf("%s；%w", strings.Join(notes, "；"), err)
		}
		return nil, err
	}
	id, proxyURL, err = s.finishProxyClaim(ctx, id, proxyURL)
	if err != nil {
		s.writeProxyClaimLogs(taskID, notes)
		return nil, err
	}
	s.writeProxyClaimLogs(taskID, notes)
	return gin.H{"proxy": proxyURL, "proxy_url": proxyURL, "id": id, "logs": notes, "message": strings.Join(notes, "\n")}, nil
}

func (s *Server) writeProxyClaimLogs(taskID string, notes []string) {
	taskID = strings.TrimSpace(taskID)
	for _, line := range notes {
		if taskID != "" {
			_ = s.appendTaskRuntimeLog(taskID, "", "info", "proxy", line)
		}
	}
}
