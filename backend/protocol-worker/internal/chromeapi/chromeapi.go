package chromeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/proxyrelay"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/session"
)

type Session struct {
	ctx          context.Context
	cancel       context.CancelFunc
	allocCancel  context.CancelFunc
	proxyCleanup func()
	UA           string
	mu           sync.Mutex
	bodies       []string
	urls         map[network.RequestID]string
}

type FetchResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

func Open(proxy, locale, timezone string) (*Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	path := chromePath()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.UserAgent(desktopChromeUA(chromeMajor(path))),
	)
	if path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}
	proxySettings, err := proxyrelay.ForChrome(proxy)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("代理无法给 Chrome 使用: %w", err)
	}
	proxyCleanup := proxySettings.Cleanup
	if proxySettings.Server != "" {
		opts = append(opts, chromedp.ProxyServer(proxySettings.Server))
	}
	if proxySettings.HostResolverRules != "" {
		opts = append(opts, chromedp.Flag("host-resolver-rules", proxySettings.HostResolverRules))
	}
	if strings.TrimSpace(locale) != "" {
		opts = append(opts, chromedp.Flag("lang", locale), chromedp.Flag("accept-lang", locale+",en;q=0.9"))
	}
	alloc, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	chromeCtx, chromeCancel := chromedp.NewContext(alloc)
	s := &Session{
		ctx: chromeCtx,
		cancel: func() {
			chromeCancel()
			allocCancel()
			cancel()
			if proxyCleanup != nil {
				proxyCleanup()
			}
		},
		allocCancel:  allocCancel,
		proxyCleanup: proxyCleanup,
		urls:         map[network.RequestID]string{},
	}

	chromedp.ListenTarget(chromeCtx, func(ev any) {
		switch typed := ev.(type) {
		case *network.EventRequestWillBeSent:
			if typed.Request != nil {
				s.mu.Lock()
				s.urls[typed.RequestID] = typed.Request.URL
				s.mu.Unlock()
			}
		case *network.EventLoadingFinished:
			go s.captureBody(typed.RequestID)
		}
	})

	boot := []chromedp.Action{network.Enable()}
	if strings.TrimSpace(timezone) != "" {
		boot = append(boot, emulation.SetTimezoneOverride(timezone))
	}
	boot = append(boot,
		chromedp.Navigate("https://chatgpt.com/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(waitCloudflare),
	)
	var ua string
	boot = append(boot, chromedp.Evaluate(`navigator.userAgent`, &ua))
	err = chromedp.Run(chromeCtx, boot...)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.UA = ua
	return s, nil
}

func waitCloudflare(ctx context.Context) error {
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var title, body string
		if err := chromedp.Title(&title).Do(ctx); err != nil {
			return err
		}
		_ = chromedp.Text("body", &body, chromedp.ByQuery).Do(ctx)
		blob := strings.ToLower(title + "\n" + body)
		if title != "" && !strings.Contains(blob, "just a moment") && !strings.Contains(blob, "verify you are human") {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("Cloudflare 未在 45s 内通过")
}

func (s *Session) captureBody(id network.RequestID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	rawURL := s.urls[id]
	s.mu.Unlock()
	body, err := network.GetResponseBody(id).Do(s.ctx)
	if err != nil || len(body) == 0 {
		return
	}
	text := string(body)
	if !interestingBody(text) && !interestingURL(rawURL) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) < 40 {
		s.bodies = append(s.bodies, text)
	}
}

func (s *Session) Inject(sess *session.Session) error {
	if s == nil || sess == nil {
		return nil
	}
	actions := []chromedp.Action{}
	for name, value := range sess.Cookies {
		actions = append(actions, cookieParams(name, value))
	}
	if sess.DeviceID != "" {
		if _, exists := sess.Cookies["oai-did"]; !exists {
			actions = append(actions, cookieParams("oai-did", sess.DeviceID))
		}
	}
	if len(actions) > 0 {
		if err := chromedp.Run(s.ctx, actions...); err != nil {
			return err
		}
	}
	if err := s.bindAuth(sess); err != nil {
		return err
	}
	return s.Reload()
}

func (s *Session) bindAuth(sess *session.Session) error {
	payload := map[string]any{
		"accessToken":  sess.AccessToken,
		"expires":      time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339),
		"authProvider": "google-oauth2",
	}
	if sess.Email != "" || sess.AccountID != "" {
		payload["user"] = map[string]any{"email": sess.Email, "id": sess.AccountID}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`(function(){
	  const session = %s;
	  const token = session.accessToken || "";
	  try {
	    window.__CHATGPT_BOOTSTRAP_SESSION__ = session;
	    localStorage.setItem("oai/apps/chat/bootstrap-session", JSON.stringify(session));
	  } catch (e) {}
	  const orig = window.fetch.bind(window);
	  window.fetch = async function(input, init) {
	    const url = typeof input === "string" ? input : (input && input.url) || "";
	    if (url.includes("/api/auth/session")) {
	      return new Response(JSON.stringify(session), {status:200, headers:{"Content-Type":"application/json"}});
	    }
	    init = Object.assign({}, init || {});
	    const headers = new Headers(init.headers || {});
	    if (token && (url.includes("chatgpt.com") || url.includes("openai.com")) && !headers.has("Authorization")) {
	      headers.set("Authorization", "Bearer " + token);
	      init.headers = headers;
	    }
	    return orig(input, init);
	  };
	})();`, string(raw))
	return chromedp.Run(s.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
		return err
	}))
}

func (s *Session) Reload() error {
	return chromedp.Run(s.ctx,
		chromedp.Navigate("https://chatgpt.com/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(waitCloudflare),
	)
}

func (s *Session) Cookie(name string) string {
	if s == nil {
		return ""
	}
	var cookies []*network.Cookie
	if err := chromedp.Run(s.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs([]string{chatgptURL}).Do(ctx)
		return err
	})); err != nil {
		return ""
	}
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (s *Session) Fetch(method, rawURL string, headers map[string]string, body any) (int, []byte, error) {
	payload := map[string]any{"method": method, "url": rawURL, "headers": headers}
	if body != nil {
		payload["body"] = body
	}
	raw, _ := json.Marshal(payload)
	expr := fmt.Sprintf(`(async () => {
	  const req = %s;
	  const headers = Object.assign({"Accept":"application/json"}, req.headers || {});
	  const init = { method: req.method, headers, credentials: "include" };
	  if (req.body !== undefined && req.body !== null) {
	    init.body = typeof req.body === "string" ? req.body : JSON.stringify(req.body);
	    if (!headers["Content-Type"] && !headers["content-type"]) headers["Content-Type"] = "application/json";
	  }
	  try {
	    const res = await fetch(req.url, init);
	    return { status: res.status, body: await res.text(), error: "" };
	  } catch (err) {
	    return { status: 0, body: "", error: String(err && err.message ? err.message : err) };
	  }
	})()`, string(raw))
	var out FetchResult
	if err := chromedp.Run(s.ctx, chromedp.Evaluate(expr, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})); err != nil {
		return 0, nil, err
	}
	if out.Error != "" && out.Status == 0 {
		return 0, nil, fmt.Errorf("%s", out.Error)
	}
	if interestingBody(out.Body) {
		s.mu.Lock()
		if len(s.bodies) < 40 {
			s.bodies = append(s.bodies, out.Body)
		}
		s.mu.Unlock()
	}
	return out.Status, []byte(out.Body), nil
}

func (s *Session) OpenCheckout(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("缺少 Checkout URL")
	}
	return chromedp.Run(s.ctx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(4*time.Second),
	)
}

func (s *Session) runTimeout(d time.Duration, actions ...chromedp.Action) error {
	if s == nil {
		return fmt.Errorf("chrome session 已关闭")
	}
	ctx, cancel := context.WithTimeout(s.ctx, d)
	defer cancel()
	return chromedp.Run(ctx, actions...)
}

type PageInfo struct {
	URL   string
	Title string
	Text  string
}

func (s *Session) Snapshot() PageInfo {
	info := PageInfo{}
	_ = s.runTimeout(8*time.Second,
		chromedp.Location(&info.URL),
		chromedp.Title(&info.Title),
		chromedp.Text("body", &info.Text, chromedp.ByQuery),
	)
	info.Text = compactText(info.Text, 240)
	return info
}

func (s *Session) WarmPricing() error {
	return s.runTimeout(45*time.Second,
		chromedp.Navigate("https://chatgpt.com/#pricing"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(4*time.Second),
		chromedp.ActionFunc(waitCloudflare),
	)
}

func (s *Session) OpenPricingCheckout(planType string) (string, error) {
	if err := s.WarmPricing(); err != nil {
		return "", err
	}
	info := s.Snapshot()
	blob := strings.ToLower(info.Title + " " + info.Text)
	if strings.Contains(blob, "please log in") || strings.Contains(blob, "sign in with google") || strings.Contains(info.URL, "auth.openai.com") {
		return info.URL, fmt.Errorf("定价页未登录 url=%s title=%s body=%s", info.URL, info.Title, info.Text)
	}
	kind := "plus"
	if strings.Contains(strings.ToLower(planType), "pro") {
		kind = "pro"
	}
	opened := false
	_ = s.runTimeout(8*time.Second, chromedp.Evaluate(openPricingJS(), &opened))
	_ = s.runTimeout(4*time.Second, chromedp.Sleep(2*time.Second))
	clicked := false
	if err := s.runTimeout(12*time.Second, chromedp.Evaluate(upgradeClickJS(kind), &clicked)); err != nil {
		return info.URL, fmt.Errorf("点击升级失败: %w url=%s title=%s body=%s", err, info.URL, info.Title, info.Text)
	}
	if !clicked {
		info = s.Snapshot()
		return info.URL, fmt.Errorf("定价页没有升级按钮 url=%s title=%s body=%s", info.URL, info.Title, info.Text)
	}
	var href string
	err := s.runTimeout(25*time.Second, chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if err := chromedp.Location(&href).Do(ctx); err != nil {
				return err
			}
			if strings.Contains(href, "/checkout/") {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("等待 Checkout 页超时")
	}))
	if err != nil {
		info = s.Snapshot()
		return info.URL, fmt.Errorf("%w url=%s title=%s body=%s", err, info.URL, info.Title, info.Text)
	}
	_ = s.runTimeout(6*time.Second, chromedp.Sleep(4*time.Second))
	return href, nil
}

func openPricingJS() string {
	return `(() => {
	  const clickMatching = (re) => {
	    const nodes = [...document.querySelectorAll('button, a, [role="tab"], [role="button"]')];
	    const el = nodes.find((n) => re.test((n.innerText || n.textContent || "").replace(/\s+/g, " ").trim()));
	    if (!el) return false;
	    el.click();
	    return true;
	  };
	  return clickMatching(/Upgrade your plan|升级你的套餐|升级套餐|View plans|See plans|Claim offer/i);
	})()`
}

func upgradeClickJS(planType string) string {
	title := `/ChatGPT Plus|^Plus$/i`
	re := `/Upgrade to Plus|升级至\s*Plus|Get Plus|Subscribe to Plus/i`
	if planType == "pro" {
		title = `/ChatGPT Pro|^Pro$/i`
		re = `/Upgrade to Pro|升级至\s*Pro|Get Pro/i`
	}
	return fmt.Sprintf(`(() => {
	  const textOf = (n) => (n.innerText || n.textContent || "").replace(/\s+/g, " ").trim();
	  const clickMatching = (re) => {
	    const nodes = [...document.querySelectorAll('button, a, [role="tab"], [role="button"]')];
	    const el = nodes.find((n) => re.test(textOf(n)));
	    if (!el) return false;
	    el.click();
	    return true;
	  };
	  clickMatching(/^(Personal|个人)$/i);
	  if (clickMatching(%s)) return true;
	  const cards = [...document.querySelectorAll("div,section,article")];
	  for (const card of cards) {
	    if (!%s.test(textOf(card).split("\n")[0] || "") && !%s.test(textOf(card))) continue;
	    const btn = [...card.querySelectorAll("button")].find((b) => /Upgrade|升级|Subscribe|Get/i.test(textOf(b)));
	    if (btn) { btn.click(); return true; }
	  }
	  return clickMatching(/^Upgrade$|^升级$/i);
	})()`, re, title, title)
}

func compactText(value string, n int) string {
	text := strings.Join(strings.Fields(value), " ")
	if len(text) <= n {
		return text
	}
	return text[:n]
}

func (s *Session) Captured() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.bodies, "\n")
}

func (s *Session) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

func chromePath() string {
	for _, path := range []string{
		os.Getenv("CHROME_PATH"),
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
	} {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
