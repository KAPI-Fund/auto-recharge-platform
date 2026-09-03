package cfboot

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ChatGPT 的 Cloudflare 会拦纯 TLS 伪装。这里只用 Chrome 拿 cf_clearance，
// 不打开支付页、不填表。后续建单和 Stripe 确认仍走 HTTP。
func Cookies(proxy string, timeout time.Duration) (map[string]string, string, error) {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	if chrome := chromePath(); chrome != "" {
		opts = append(opts, chromedp.ExecPath(chrome))
	}
	if strings.TrimSpace(proxy) != "" {
		opts = append(opts, chromedp.ProxyServer(proxy))
	}
	alloc, stop := chromedp.NewExecAllocator(ctx, opts...)
	defer stop()
	chromeCtx, stopChrome := chromedp.NewContext(alloc)
	defer stopChrome()

	var cookies []*network.Cookie
	var ua string
	err := chromedp.Run(chromeCtx,
		chromedp.Navigate("https://chatgpt.com/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			deadline := time.Now().Add(40 * time.Second)
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
			return context.DeadlineExceeded
		}),
		chromedp.Evaluate(`navigator.userAgent`, &ua),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	)
	if err != nil {
		return nil, "", err
	}
	out := map[string]string{}
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		out[cookie.Name] = cookie.Value
	}
	return out, ua, nil
}

func chromePath() string {
	candidates := []string{
		os.Getenv("CHROME_PATH"),
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
	}
	for _, path := range candidates {
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
