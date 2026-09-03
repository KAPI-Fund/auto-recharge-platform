package captcha

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const stripeSiteKey = "c7faac4c-1cd7-4b1b-b2d4-42ba98d09c7a"

type Config struct {
	APIKey  string
	APIURL  string
	Timeout time.Duration
}

type Solver struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Solver {
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.capsolver.com"
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	return &Solver{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Solver) Enabled() bool {
	return s != nil && strings.TrimSpace(s.cfg.APIKey) != ""
}

type Result struct {
	OK     bool
	Token  string
	EKey   string
	TaskID any
	Error  string
}

func (s *Solver) SolveHCaptcha(siteKey, websiteURL, rqdata, userAgent string) Result {
	if !s.Enabled() {
		return Result{Error: "未配置打码平台 API Key"}
	}
	if strings.TrimSpace(siteKey) == "" {
		siteKey = stripeSiteKey
	}
	if strings.TrimSpace(websiteURL) == "" {
		websiteURL = "https://js.stripe.com/"
	}
	variants := hcaptchaTasks(siteKey, websiteURL, rqdata, userAgent)
	var last string
	for _, task := range variants {
		taskID, err := s.createTask(task)
		if err != nil {
			last = err.Error()
			continue
		}
		token, ekey, err := s.poll(taskID)
		if err != nil {
			last = err.Error()
			continue
		}
		return Result{OK: true, Token: token, EKey: ekey, TaskID: taskID}
	}
	return Result{Error: last}
}

func (s *Solver) SolveCloudflare(websiteURL, html, proxy string) Result {
	if !s.Enabled() {
		return Result{Error: "未配置打码平台 API Key"}
	}
	task := map[string]any{
		"type":       "AntiCloudflareTask",
		"websiteURL": websiteURL,
		"html":       html,
	}
	if proxy != "" {
		task["proxy"] = proxy
		task["type"] = "AntiCloudflareTask"
	}
	taskID, err := s.createTask(task)
	if err != nil {
		return Result{Error: err.Error()}
	}
	token, _, err := s.poll(taskID)
	if err != nil {
		return Result{Error: err.Error()}
	}
	return Result{OK: true, Token: token, TaskID: taskID}
}

func (s *Solver) createTask(task map[string]any) (any, error) {
	data, err := s.post("/createTask", map[string]any{"clientKey": s.cfg.APIKey, "task": task})
	if err != nil {
		return nil, err
	}
	if num(data["errorId"]) != 0 {
		return nil, fmt.Errorf("%s", first(data, "errorDescription", "errorCode", "message"))
	}
	if data["taskId"] == nil {
		return nil, fmt.Errorf("createTask 未返回 taskId")
	}
	return data["taskId"], nil
}

func (s *Solver) poll(taskID any) (string, string, error) {
	deadline := time.Now().Add(s.cfg.Timeout)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		data, err := s.post("/getTaskResult", map[string]any{"clientKey": s.cfg.APIKey, "taskId": taskID})
		if err != nil {
			continue
		}
		if num(data["errorId"]) != 0 {
			return "", "", fmt.Errorf("%s", first(data, "errorDescription", "errorCode", "message"))
		}
		if fmt.Sprint(data["status"]) != "ready" {
			continue
		}
		solution, _ := data["solution"].(map[string]any)
		token := first(solution, "gRecaptchaResponse", "hcaptchaToken", "token", "cf_clearance")
		if token == "" {
			token = first(data, "gRecaptchaResponse", "token")
		}
		if token == "" {
			return "", "", fmt.Errorf("打码平台返回 ready 但无 token")
		}
		return token, first(solution, "eKey", "respKey", "ekey"), nil
	}
	return "", "", fmt.Errorf("打码平台解题超时")
}

func (s *Solver) post(path string, payload map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(payload)
	resp, err := s.client.Post(s.cfg.APIURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("非 JSON 响应 (%d): %s", resp.StatusCode, truncate(string(body), 120))
	}
	return data, nil
}

func hcaptchaTasks(siteKey, websiteURL, rqdata, userAgent string) []map[string]any {
	var tasks []map[string]any
	for _, enterprise := range []bool{true, false} {
		task := map[string]any{
			"type":         "HCaptchaTaskProxyless",
			"websiteURL":   websiteURL,
			"websiteKey":   siteKey,
			"isEnterprise": enterprise,
			"isInvisible":  true,
		}
		if rqdata != "" {
			task["rqdata"] = rqdata
		}
		if userAgent != "" {
			task["userAgent"] = userAgent
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func ExtractSiteKey(html string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`data-sitekey="([^"]+)"`),
		regexp.MustCompile(`sitekey["']?\s*[:=]\s*["']([^"']+)`),
		regexp.MustCompile(`site-key["']?\s*[:=]\s*["']([^"']+)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(html); len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

func ExtractRqdata(html string) string {
	pattern := regexp.MustCompile(`rqdata["']?\s*[:=]\s*["']([^"']+)`)
	if match := pattern.FindStringSubmatch(html); len(match) == 2 {
		return match[1]
	}
	return ""
}

func LooksLikeChallenge(status int, body string, header http.Header) bool {
	if header != nil && strings.EqualFold(header.Get("cf-mitigated"), "challenge") {
		return true
	}
	text := strings.ToLower(body)
	return status == 403 && (strings.Contains(text, "just a moment") || strings.Contains(text, "cf-mitigated") || strings.Contains(text, "hcaptcha") || strings.Contains(text, "captcha") || strings.Contains(text, "cloudflare"))
}

func LooksLikeHCaptcha(body string) bool {
	text := strings.ToLower(body)
	return strings.Contains(text, "hcaptcha") || strings.Contains(text, "h-captcha")
}

func first(data map[string]any, keys ...string) string {
	if data == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := data[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func num(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}

func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
