package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestNormalizeRefreshURL(t *testing.T) {
	empty, err := normalizeRefreshURL("  ")
	if err != nil || empty != "" {
		t.Fatalf("empty refresh URL = %q, %v", empty, err)
	}
	ok, err := normalizeRefreshURL(" https://ip.example.com/refresh?token=abc ")
	if err != nil || ok != "https://ip.example.com/refresh?token=abc" {
		t.Fatalf("https refresh URL = %q, %v", ok, err)
	}
	if _, err := normalizeRefreshURL("ftp://ip.example.com/refresh"); err == nil {
		t.Fatal("ftp refresh URL should be rejected")
	}
	if _, err := normalizeRefreshURL("not-a-url"); err == nil {
		t.Fatal("hostless refresh URL should be rejected")
	}
	for _, blocked := range []string{
		"http://127.0.0.1/refresh",
		"http://localhost/refresh",
		"http://10.0.0.8/refresh",
		"http://192.168.1.1/refresh",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/refresh",
	} {
		if _, err := normalizeRefreshURL(blocked); err == nil {
			t.Fatalf("blocked refresh URL accepted: %s", blocked)
		}
	}
}

func TestProxyRefreshTimeoutAndWaitBounds(t *testing.T) {
	if proxyRefreshTimeout("") != 15*time.Second || proxyRefreshTimeout("0") != 15*time.Second {
		t.Fatalf("default timeout = %s", proxyRefreshTimeout(""))
	}
	if proxyRefreshTimeout("90") != 60*time.Second {
		t.Fatalf("max timeout = %s", proxyRefreshTimeout("90"))
	}
	if proxyRefreshWait("") != 0 || proxyRefreshWait("-1") != 0 {
		t.Fatalf("default wait = %s", proxyRefreshWait(""))
	}
	if proxyRefreshWait("40000") != 30*time.Second {
		t.Fatalf("max wait = %s", proxyRefreshWait("40000"))
	}
	if proxyRefreshWait("1500") != 1500*time.Millisecond {
		t.Fatalf("wait = %s", proxyRefreshWait("1500"))
	}
}

func TestRequestProxyRefreshAccepts2xxAndRejectsErrors(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		if request.Method != http.MethodGet {
			t.Errorf("method = %s", request.Method)
		}
		io.WriteString(writer, "ok")
	}))
	defer server.Close()

	if err := requestProxyRefresh(&http.Client{Timeout: 2 * time.Second}, server.URL); err != nil {
		t.Fatalf("refresh 200: %v", err)
	}
	failServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer failServer.Close()
	if err := requestProxyRefresh(&http.Client{Timeout: 2 * time.Second}, failServer.URL); err == nil {
		t.Fatal("refresh 502 should fail")
	}
	if hits != 1 {
		t.Fatalf("success hits = %d", hits)
	}
}

func TestClaimProxyFromListRefreshesBeforeReturning(t *testing.T) {
	calls := []string{}
	refresh := func(refreshURL string) error {
		calls = append(calls, refreshURL)
		return nil
	}
	row, proxyURL, err := claimProxyFromList([]models.ProxyAsset{{
		ID: "proxy_1", ProxyURL: "http://user:pass@gw.example.com:1000", RefreshURL: "https://ip.example.com/refresh",
	}}, refresh, "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "proxy_1" || proxyURL != "http://user:pass@gw.example.com:1000" {
		t.Fatalf("claimed = %+v %s", row, proxyURL)
	}
	if len(calls) != 1 || calls[0] != "https://ip.example.com/refresh" {
		t.Fatalf("refresh calls = %#v", calls)
	}
}

func TestClaimProxyFromListSkipsFailedRefreshAndUsesNext(t *testing.T) {
	calls := 0
	refresh := func(refreshURL string) error {
		calls++
		if strings.Contains(refreshURL, "bad") {
			return errors.New("refresh failed")
		}
		return nil
	}
	row, _, err := claimProxyFromList([]models.ProxyAsset{
		{ID: "bad", ProxyURL: "http://bad.example:1", RefreshURL: "https://ip.example.com/bad"},
		{ID: "good", ProxyURL: "http://good.example:2", RefreshURL: "https://ip.example.com/ok"},
	}, refresh, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "good" || calls != 2 {
		t.Fatalf("row = %s calls = %d", row.ID, calls)
	}
}

func TestClaimProxyFromListDoesNotRefreshWhenURLMissing(t *testing.T) {
	calls := 0
	refresh := func(string) error {
		calls++
		return errors.New("should not refresh")
	}
	row, proxyURL, err := claimProxyFromList([]models.ProxyAsset{{
		ID: "plain", ProxyURL: "socks5://user-{session}:pass@host:2000",
	}}, refresh, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "plain" || proxyURL != "socks5://user-abc:pass@host:2000" || calls != 0 {
		t.Fatalf("plain claim = %+v %s calls=%d", row, proxyURL, calls)
	}
}

func TestClaimProxyFromListFailsWhenEveryRefreshFails(t *testing.T) {
	_, _, err := claimProxyFromList([]models.ProxyAsset{{
		ID: "only", ProxyURL: "http://gw.example:1", RefreshURL: "https://ip.example.com/refresh",
	}}, func(string) error { return errors.New("down") }, "sess")
	if err == nil || !strings.Contains(err.Error(), "代理刷新 IP 失败") {
		t.Fatalf("error = %v", err)
	}
}

func TestOrderProxiesForClaimSpreadsAcrossIdleProxies(t *testing.T) {
	ordered := orderProxiesForClaim([]models.ProxyAsset{
		{ID: "busy", InUse: true, InUseCount: 1},
		{ID: "idle-b", InUse: false},
		{ID: "idle-a", InUse: false},
	}, nil)
	if len(ordered) != 3 || ordered[0].ID == "busy" || ordered[2].ID != "busy" {
		t.Fatalf("idle proxies should come before busy ones, got %#v", idsOf(ordered))
	}
	retry := orderProxiesForClaim([]models.ProxyAsset{
		{ID: "busy", InUse: true, InUseCount: 1},
		{ID: "prev", InUse: false},
		{ID: "idle", InUse: false},
	}, []string{"prev"})
	if retry[0].ID != "idle" || retry[1].ID != "busy" || retry[2].ID != "prev" {
		t.Fatalf("retry should still prefer a different idle proxy, got %#v", idsOf(retry))
	}
}

func idsOf(rows []models.ProxyAsset) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestClaimProxyFromListPrefersUnusedProxyThenFallsBack(t *testing.T) {
	refresh := func(string) error { return nil }
	row, _, err := claimProxyFromList([]models.ProxyAsset{
		{ID: "used", ProxyURL: "http://used.example:1", RefreshURL: "https://ip.example.com/used"},
		{ID: "fresh", ProxyURL: "http://fresh.example:2", RefreshURL: "https://ip.example.com/fresh"},
	}, refresh, "sess", []string{"used"})
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "fresh" {
		t.Fatalf("preferred unused proxy, got %s", row.ID)
	}
	fallback, _, err := claimProxyFromList([]models.ProxyAsset{
		{ID: "used", ProxyURL: "http://used.example:1", RefreshURL: "https://ip.example.com/used"},
	}, refresh, "sess", []string{"used"})
	if err != nil {
		t.Fatal(err)
	}
	if fallback.ID != "used" {
		t.Fatalf("should fall back to previously used proxy, got %s", fallback.ID)
	}
}

func TestProxyExcludeIDsReadsWorkerPayload(t *testing.T) {
	ids := proxyExcludeIDs(map[string]any{"excludeIds": []any{"proxy_1", " proxy_2 "}})
	if len(ids) != 2 || ids[0] != "proxy_1" || ids[1] != "proxy_2" {
		t.Fatalf("exclude ids = %#v", ids)
	}
}

func TestClaimProxyFromListEmptyIsNoActiveProxy(t *testing.T) {
	_, _, err := claimProxyFromList(nil, nil, "sess")
	if !errors.Is(err, errNoActiveProxy) {
		t.Fatalf("error = %v", err)
	}
}

func TestProxyStabilityViewAndAttemptColumns(t *testing.T) {
	text, rate, scored := proxyStabilityView(0, 0)
	if text != "—" || rate != 0 || scored != 0 {
		t.Fatalf("empty stability = %q %d %d", text, rate, scored)
	}
	text, rate, scored = proxyStabilityView(4, 1)
	if text != "80%（失败 1/5）" || rate != 80 || scored != 5 {
		t.Fatalf("stability = %q %d %d", text, rate, scored)
	}
	column, ok := proxyAttemptColumn("failure")
	if !ok || column != "failure_count" {
		t.Fatalf("failure column = %s %v", column, ok)
	}
	if _, ok := proxyAttemptColumn("noop"); ok {
		t.Fatal("unknown outcome should be ignored")
	}
}

func TestProxyListSummaryIncludesRefreshSettings(t *testing.T) {
	summary := proxyListSummary([]models.ProxyAsset{{Active: true}, {Active: false}}, 20, 800, 2, 120)
	if summary["total"] != 2 || summary["active"] != 1 {
		t.Fatalf("counts = %#v", summary)
	}
	if summary["refresh_timeout_seconds"] != 20 || summary["refresh_wait_ms"] != 800 || summary["max_concurrent"] != 2 || summary["refresh_min_interval_seconds"] != 120 {
		t.Fatalf("refresh settings = %#v", summary)
	}
}

func TestLegacyAddProxiesRejectsInvalidRefreshURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/proxies", bytes.NewBufferString(`{"proxies":"http://127.0.0.1:8080","refresh_url":"ftp://ip.example.com/refresh"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&Server{}).legacyAddProxies(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "刷新 URL") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestApplyProxySessionReplacesPlaceholder(t *testing.T) {
	got := applyProxySession("http://USER-session-{session}:PASS@proxy.example.com:1000", "zz9")
	if got != "http://USER-session-zz9:PASS@proxy.example.com:1000" {
		t.Fatalf("session proxy = %s", got)
	}
}

func TestProxyNeedsRefreshUsesInterval(t *testing.T) {
	if !proxyNeedsRefresh(nil, nil, 2*time.Minute) {
		t.Fatal("never refreshed should refresh")
	}
	ok := true
	recent := time.Now().Add(-10 * time.Second)
	if proxyNeedsRefresh(&recent, &ok, 2*time.Minute) {
		t.Fatal("recent successful refresh should be reused")
	}
	old := time.Now().Add(-5 * time.Minute)
	if !proxyNeedsRefresh(&old, &ok, 2*time.Minute) {
		t.Fatal("stale refresh should run again")
	}
	failed := false
	if !proxyNeedsRefresh(&recent, &failed, 2*time.Minute) {
		t.Fatal("failed refresh should retry even inside the interval")
	}
	if !proxyNeedsRefresh(&recent, &ok, 0) {
		t.Fatal("zero interval should always refresh")
	}
}

func TestShouldRefreshClaimedProxyIsSelfConsistent(t *testing.T) {
	ok := true
	failed := false
	recent := time.Now().Add(-10 * time.Second)
	stale := time.Now().Add(-5 * time.Minute)
	interval := 2 * time.Minute
	url := "https://ip.example.com/refresh"
	cases := []struct {
		name          string
		firstSlot     bool
		url           string
		lastAt        *time.Time
		lastOK        *bool
		interval      time.Duration
		reusePrevious bool
		want          bool
	}{
		{name: "idle never refreshed", firstSlot: true, url: url, interval: interval, want: true},
		{name: "idle after cooldown", firstSlot: true, url: url, lastAt: &stale, lastOK: &ok, interval: interval, want: true},
		{name: "idle but just refreshed", firstSlot: true, url: url, lastAt: &recent, lastOK: &ok, interval: interval, want: false},
		{name: "idle last refresh failed", firstSlot: true, url: url, lastAt: &recent, lastOK: &failed, interval: interval, want: true},
		{name: "share while another task is running even if cooldown elapsed", firstSlot: false, url: url, lastAt: &stale, lastOK: &ok, interval: interval, want: false},
		{name: "share while another task is running inside cooldown", firstSlot: false, url: url, lastAt: &recent, lastOK: &ok, interval: interval, want: false},
		{name: "no refresh URL", firstSlot: true, url: "", lastAt: nil, interval: interval, want: false},
		{name: "zero interval on idle proxy", firstSlot: true, url: url, lastAt: &recent, lastOK: &ok, interval: 0, want: true},
		{name: "zero interval still does not refresh a shared proxy", firstSlot: false, url: url, lastAt: &stale, lastOK: &ok, interval: 0, want: false},
		{name: "region retry reuses the only proxy and must refresh", firstSlot: true, url: url, lastAt: &recent, lastOK: &ok, interval: interval, reusePrevious: true, want: true},
		{name: "region retry still refreshes when the only proxy is shared", firstSlot: false, url: url, lastAt: &recent, lastOK: &ok, interval: interval, reusePrevious: true, want: true},
		{name: "region retry without refresh URL cannot rotate IP", firstSlot: true, url: "", lastAt: &recent, lastOK: &ok, interval: interval, reusePrevious: true, want: false},
	}
	for _, tc := range cases {
		got := shouldRefreshClaimedProxy(tc.firstSlot, tc.url, tc.lastAt, tc.lastOK, tc.interval, tc.reusePrevious)
		if got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestProxyCountryMismatchAfterRefresh(t *testing.T) {
	if reason := proxyCountryMismatch(false, "PH", "US"); reason != "" {
		t.Fatalf("skip check when not refreshed, got %q", reason)
	}
	if reason := proxyCountryMismatch(true, "", "US"); reason != "" {
		t.Fatalf("skip check without target region, got %q", reason)
	}
	if reason := proxyCountryMismatch(true, "PH", ""); reason != "" {
		t.Fatalf("skip check when country lookup is empty, got %q", reason)
	}
	if reason := proxyCountryMismatch(true, "PH", "PH"); reason != "" {
		t.Fatalf("matching country should continue, got %q", reason)
	}
	reason := proxyCountryMismatch(true, "PH", "US")
	if reason == "" || !strings.Contains(reason, "US") || !strings.Contains(reason, "PH") {
		t.Fatalf("mismatch reason = %q", reason)
	}
}

func TestProxyClaimRegionReadsWorkerPayload(t *testing.T) {
	if got := proxyClaimRegion(map[string]any{"region": "ph"}); got != "PH" {
		t.Fatalf("region = %q", got)
	}
	if got := proxyClaimRegion(map[string]any{"payment_region": "US"}); got != "US" {
		t.Fatalf("payment_region = %q", got)
	}
}

func TestProxyRefreshMinIntervalDefaultIsTwoMinutes(t *testing.T) {
	if proxyRefreshMinIntervalValue("") != 120*time.Second || proxyRefreshMinIntervalValue("-1") != 120*time.Second {
		t.Fatalf("default interval = %s", proxyRefreshMinIntervalValue(""))
	}
	if proxyRefreshMinIntervalValue("30") != 30*time.Second {
		t.Fatalf("custom interval = %s", proxyRefreshMinIntervalValue("30"))
	}
	if proxyRefreshMinIntervalValue("99999") != 3600*time.Second {
		t.Fatalf("capped interval = %s", proxyRefreshMinIntervalValue("99999"))
	}
}

func TestProxyMaxConcurrentBounds(t *testing.T) {
	if proxyMaxConcurrentValue("") != 0 || proxyMaxConcurrentValue("0") != 0 {
		t.Fatalf("unlimited max concurrent = %d", proxyMaxConcurrentValue(""))
	}
	if proxyMaxConcurrentValue("1") != 1 {
		t.Fatalf("exclusive max concurrent = %d", proxyMaxConcurrentValue("1"))
	}
	if proxyMaxConcurrentValue("8") != 8 {
		t.Fatalf("custom max concurrent = %d", proxyMaxConcurrentValue("8"))
	}
	if proxyMaxConcurrentValue("99") != 50 {
		t.Fatalf("capped max concurrent = %d", proxyMaxConcurrentValue("99"))
	}
}

func TestProxyClaimDeadlineIsCapped(t *testing.T) {
	if proxyClaimDeadline(15*time.Second, 0) != 30*time.Second {
		t.Fatalf("default deadline = %s", proxyClaimDeadline(15*time.Second, 0))
	}
	if proxyClaimDeadline(60*time.Second, 30*time.Second) != proxyClaimDeadlineMax {
		t.Fatalf("max deadline = %s", proxyClaimDeadline(60*time.Second, 30*time.Second))
	}
	if proxyClaimDeadline(time.Second, 0) != proxyClaimDeadlineMin {
		t.Fatalf("min deadline = %s", proxyClaimDeadline(time.Second, 0))
	}
}

func TestBlockedRefreshHostsAndIPs(t *testing.T) {
	if !isBlockedRefreshHost("localhost") || !isBlockedRefreshHost("metadata.google.internal") {
		t.Fatal("localhost/metadata hosts should be blocked")
	}
	if !isBlockedRefreshIP(net.ParseIP("127.0.0.1")) || !isBlockedRefreshIP(net.ParseIP("10.1.2.3")) || !isBlockedRefreshIP(net.ParseIP("169.254.169.254")) {
		t.Fatal("loopback/private/link-local IPs should be blocked")
	}
	if !isBlockedRefreshIP(net.ParseIP("100.64.1.1")) {
		t.Fatal("CGNAT IPs should be blocked")
	}
	if isBlockedRefreshIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP should be allowed")
	}
}

func TestNewProxyRefreshClientRejectsPrivateRedirect(t *testing.T) {
	client := newProxyRefreshClient(2 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("refresh client must inspect redirects")
	}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, []*http.Request{req}); err == nil {
		t.Fatal("redirect to loopback should be rejected")
	}
}

func TestTruncateProxyError(t *testing.T) {
	if truncateProxyError("  boom  ") != "boom" {
		t.Fatalf("trim = %q", truncateProxyError("  boom  "))
	}
	long := strings.Repeat("e", proxyRefreshErrorMax+20)
	if got := truncateProxyError(long); len(got) != proxyRefreshErrorMax {
		t.Fatalf("truncated len = %d", len(got))
	}
}

func TestEnvProxyFallbackOnlyWhenPoolEmpty(t *testing.T) {
	if !envProxyFallbackAllowed(errNoActiveProxy) {
		t.Fatal("empty pool should allow local IP or env proxy")
	}
	if envProxyFallbackAllowed(errProxyBusy) {
		t.Fatal("busy pool must not fall back to local IP")
	}
	if envProxyFallbackAllowed(fmt.Errorf("代理刷新 IP 失败: %w", errors.New("down"))) {
		t.Fatal("refresh failure must not fall back to local IP")
	}
}

func TestFinishProxyClaimUnlocksWhenCallerGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, proxyURL, err := (&Server{}).finishProxyClaim(ctx, "", "http://proxy.example:1")
	if err == nil || id != "" || proxyURL != "" {
		t.Fatalf("canceled claim = %q %q %v", id, proxyURL, err)
	}
	if !strings.Contains(err.Error(), "代理领取超时") {
		t.Fatalf("error = %v", err)
	}
}
