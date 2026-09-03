package httpx

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/imroc/req/v3"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/publicsuffix"
)

type Client struct {
	inner *req.Client
	UA    string
	Proxy string
}

func New(proxy string) *Client {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	inner := req.C().
		ImpersonateChrome().
		SetTLSFingerprint(utls.HelloChrome_133).
		SetCookieJar(jar).
		SetTimeout(45 * time.Second).
		SetCommonRetryCount(0).
		SetUserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	if strings.TrimSpace(proxy) != "" {
		inner.SetProxyURL(proxy)
	}
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	if inner.Headers != nil {
		if value := inner.Headers.Get("User-Agent"); value != "" {
			ua = value
		}
	}
	return &Client{inner: inner, UA: ua, Proxy: proxy}
}

type Response struct {
	Status  int
	Body    []byte
	Text    string
	Headers http.Header
}

func (c *Client) Do(method, rawURL string, headers map[string]string, body any, contentType string) (*Response, error) {
	r := c.inner.R().SetHeader("Accept-Language", "en-SG,en;q=0.9")
	for key, value := range headers {
		if value != "" {
			r.SetHeader(key, value)
		}
	}
	if contentType != "" {
		r.SetHeader("Content-Type", contentType)
	}
	switch typed := body.(type) {
	case nil:
	case string:
		r.SetBody(typed)
	case []byte:
		r.SetBody(typed)
	default:
		r.SetBody(typed)
	}
	resp, err := r.Send(method, rawURL)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Body: resp.Bytes(), Text: resp.String(), Headers: resp.Header}, nil
}

func (c *Client) JSON(method, rawURL string, headers map[string]string, payload any) (*Response, error) {
	r := c.inner.R().SetHeader("Accept-Language", "en-SG,en;q=0.9").SetHeader("Content-Type", "application/json")
	for key, value := range headers {
		if value != "" {
			r.SetHeader(key, value)
		}
	}
	if payload != nil {
		r.SetBodyJsonMarshal(payload)
	}
	resp, err := r.Send(method, rawURL)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Body: resp.Bytes(), Text: resp.String(), Headers: resp.Header}, nil
}

func (c *Client) Form(method, rawURL string, headers map[string]string, encoded string) (*Response, error) {
	return c.Do(method, rawURL, headers, encoded, "application/x-www-form-urlencoded")
}

func (c *Client) SetCookies(rawURL string, cookies map[string]string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	var list []*http.Cookie
	for name, value := range cookies {
		list = append(list, &http.Cookie{Name: name, Value: value, Path: "/", Domain: u.Host})
	}
	c.inner.GetClient().Jar.SetCookies(u, list)
}

func MaskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		user := u.User.Username()
		u.User = url.UserPassword(user, "***")
	}
	return u.String()
}

func Truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}

func IsCloudflare(status int, body string, headers http.Header) bool {
	text := strings.ToLower(body)
	if headers != nil && strings.EqualFold(headers.Get("cf-mitigated"), "challenge") {
		return true
	}
	return status == 403 && (strings.Contains(text, "just a moment") || strings.Contains(text, "cf-mitigated") || strings.Contains(text, "cloudflare"))
}
