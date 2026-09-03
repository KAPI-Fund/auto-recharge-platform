package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr                      string
	DatabaseURL                   string
	RedisURL                      string
	QueueName                     string
	AdminAPIToken                 string
	WorkerAPIToken                string
	SessionEncryptionKey          string
	SessionEncryptionKeyFallbacks []string
	CORSOrigins                   []string
	DefaultRechargeMode           string
	UpstreamBaseURL               string
	UpstreamAPIKey                string
	UpstreamCreatePath            string
	UpstreamStatusPath            string
	UpstreamPollIntervalMS        int
	UpstreamPollAttempts          int
	GPTAPIPollIntervalMS          int
	GPTAPIMaxPolls                int
	BrowserCheckoutURL            string
	BrowserHeadless               bool
	OutboundProxy                 string
	AdminTokenSecret              string
	AdminEmail                    string
	AdminPassword                 string
	AdminSecondaryPassword        string
	AdminLoginPath                string
	AdminPanelPath                string
	RuntimeDir                    string
	PublicBaseURL                 string
	StoreDebugMode                bool
	StripeSecretKey               string
	StripeAPIBaseURL              string
	StripeWebhookSecret           string
	StripeSuccessURL              string
	StripeCancelURL               string
	EmailEnabled                  bool
	EmailNotifyPurchase           bool
	EmailNotifyRedeem             bool
	EmailSiteName                 string
	EmailSMTPHost                 string
	EmailSMTPPort                 int
	EmailSMTPUsername             string
	EmailSMTPPassword             string
	EmailSMTPFrom                 string
	EmailSMTPFromName             string
	EmailSMTPUseTLS               bool
	EmailSMTPTimeoutSeconds       int
	BrowserPoolControlURL         string
	BrowserPoolControlToken       string
	TaskQueuedTimeoutSeconds      int
	TaskLeaseTimeoutSeconds       int
	HcaptchaSolverEnabled         string
	HcaptchaVlmAPIKey             string
	HcaptchaVlmBaseURL            string
	HcaptchaVlmModel              string
	HcaptchaVlmTimeout            string
	HcaptchaSolverTimeout         string
	HcaptchaSolverNoVlm           string
	HcaptchaCDPPort               string
	HcaptchaPlatformAPIKey        string
	HcaptchaPlatformAPIURL        string
	HcaptchaPlatformTimeout       string
}

func Load() Config {
	publicBaseURL := env("PUBLIC_BASE_URL", "http://localhost:3000")
	return Config{
		HTTPAddr:                      env("HTTP_ADDR", ":8080"),
		DatabaseURL:                   env("DATABASE_URL", "postgres://recharge:recharge@127.0.0.1:5432/recharge?sslmode=disable"),
		RedisURL:                      env("REDIS_URL", "redis://127.0.0.1:6379/0"),
		QueueName:                     env("QUEUE_NAME", "recharge:tasks"),
		AdminAPIToken:                 env("ADMIN_API_TOKEN", "dev-admin-token"),
		WorkerAPIToken:                env("WORKER_API_TOKEN", "dev-worker-token"),
		SessionEncryptionKey:          env("SESSION_ENCRYPTION_KEY", "dev-only-change-this-session-key"),
		SessionEncryptionKeyFallbacks: split(env("SESSION_ENCRYPTION_KEY_FALLBACKS", "")),
		CORSOrigins:                   split(env("CORS_ORIGINS", "http://localhost:3000")),
		DefaultRechargeMode:           normalizeMode(env("DEFAULT_RECHARGE_MODE", "browser")),
		UpstreamBaseURL:               strings.TrimRight(env("UPSTREAM_BASE_URL", ""), "/"),
		UpstreamAPIKey:                env("UPSTREAM_API_KEY", ""),
		UpstreamCreatePath:            env("UPSTREAM_CREATE_PATH", "/pay"),
		UpstreamStatusPath:            env("UPSTREAM_STATUS_PATH", "/tasks/:id"),
		UpstreamPollIntervalMS:        envInt("UPSTREAM_POLL_INTERVAL_MS", 3000),
		UpstreamPollAttempts:          envInt("UPSTREAM_POLL_ATTEMPTS", 40),
		GPTAPIPollIntervalMS:          envInt("GPT_API_POLL_INTERVAL_MS", 5000),
		GPTAPIMaxPolls:                envInt("GPT_API_MAX_POLLS", 120),
		BrowserCheckoutURL:            env("BROWSER_CHECKOUT_URL", ""),
		BrowserHeadless:               envBool("BROWSER_HEADLESS", true),
		OutboundProxy:                 env("PROXY", ""),
		AdminTokenSecret:              env("ADMIN_TOKEN_SECRET", "dev-admin-token-secret"),
		AdminEmail:                    env("ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:                 env("ADMIN_PASSWORD", "admin123"),
		AdminSecondaryPassword:        env("ADMIN_SECONDARY_PASSWORD", "admin123"),
		AdminLoginPath:                env("ADMIN_LOGIN_PATH", "/admin-login"),
		AdminPanelPath:                env("ADMIN_PANEL_PATH", "/admin"),
		RuntimeDir:                    resolveRuntimeDir(os.Getenv("RUNTIME_DIR")),
		PublicBaseURL:                 publicBaseURL,
		StoreDebugMode:                envBool("STORE_DEBUG_MODE", true),
		StripeSecretKey:               env("STRIPE_SECRET_KEY", ""),
		StripeAPIBaseURL:              strings.TrimRight(env("STRIPE_API_BASE_URL", "https://api.stripe.com"), "/"),
		StripeWebhookSecret:           env("STRIPE_WEBHOOK_SECRET", ""),
		StripeSuccessURL:              env("STRIPE_SUCCESS_URL", strings.TrimRight(publicBaseURL, "/")+"/recharge"),
		StripeCancelURL:               env("STRIPE_CANCEL_URL", strings.TrimRight(publicBaseURL, "/")+"/recharge"),
		EmailEnabled:                  envBool("EMAIL_ENABLED", false),
		EmailNotifyPurchase:           envBool("EMAIL_NOTIFY_PURCHASE", true),
		EmailNotifyRedeem:             envBool("EMAIL_NOTIFY_REDEEM", true),
		EmailSiteName:                 env("EMAIL_SITE_NAME", "KC GPT自动充值系统"),
		EmailSMTPHost:                 env("EMAIL_SMTP_HOST", ""),
		EmailSMTPPort:                 envInt("EMAIL_SMTP_PORT", 587),
		EmailSMTPUsername:             env("EMAIL_SMTP_USERNAME", ""),
		EmailSMTPPassword:             env("EMAIL_SMTP_PASSWORD", ""),
		EmailSMTPFrom:                 env("EMAIL_SMTP_FROM", ""),
		EmailSMTPFromName:             env("EMAIL_SMTP_FROM_NAME", ""),
		EmailSMTPUseTLS:               envBool("EMAIL_SMTP_USE_TLS", true),
		EmailSMTPTimeoutSeconds:       envInt("EMAIL_SMTP_TIMEOUT_SECONDS", 10),
		BrowserPoolControlURL:         strings.TrimRight(env("BROWSER_POOL_CONTROL_URL", "http://127.0.0.1:8091"), "/"),
		BrowserPoolControlToken:       env("BROWSER_POOL_CONTROL_TOKEN", env("WORKER_API_TOKEN", "dev-worker-token")),
		TaskQueuedTimeoutSeconds:      envInt("RECHARGE_QUEUED_TIMEOUT_SECONDS", 600),
		TaskLeaseTimeoutSeconds:       envInt("RECHARGE_TASK_LEASE_TIMEOUT_SECONDS", 60),
		HcaptchaSolverEnabled:         env("HCAPTCHA_SOLVER_ENABLED", "1"),
		HcaptchaVlmAPIKey:             env("HCAPTCHA_VLM_API_KEY", ""),
		HcaptchaVlmBaseURL:            env("HCAPTCHA_VLM_BASE_URL", "https://api.openai.com/v1"),
		HcaptchaVlmModel:              env("HCAPTCHA_VLM_MODEL", "gpt-5.5"),
		HcaptchaVlmTimeout:            env("HCAPTCHA_VLM_TIMEOUT", "45"),
		HcaptchaSolverTimeout:         env("HCAPTCHA_SOLVER_TIMEOUT", "240"),
		HcaptchaSolverNoVlm:           env("HCAPTCHA_SOLVER_NO_VLM", "0"),
		HcaptchaCDPPort:               env("HCAPTCHA_CDP_PORT", "9222"),
		HcaptchaPlatformAPIKey:        env("HCAPTCHA_CAPTCHA_PLATFORM_API_KEY", env("CAPTCHA_PLATFORM_API_KEY", "")),
		HcaptchaPlatformAPIURL:        env("HCAPTCHA_CAPTCHA_PLATFORM_API_URL", env("CAPTCHA_PLATFORM_API_URL", "https://api.capsolver.com")),
		HcaptchaPlatformTimeout:       env("HCAPTCHA_CAPTCHA_PLATFORM_TIMEOUT", env("CAPTCHA_PLATFORM_TIMEOUT", "180")),
	}
}

// SessionEncryptionKeys returns the primary key followed by explicitly
// configured legacy keys, with duplicates removed while preserving order.
func (c Config) SessionEncryptionKeys() []string {
	keys := make([]string, 0, 1+len(c.SessionEncryptionKeyFallbacks))
	seen := make(map[string]struct{}, cap(keys))
	for _, key := range append([]string{c.SessionEncryptionKey}, c.SessionEncryptionKeyFallbacks...) {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func resolveRuntimeDir(value string) string {
	raw := strings.TrimSpace(value)
	if raw != "" && raw != "./runtime" && raw != "runtime" {
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw)
		}
		workingDir, err := os.Getwd()
		if err == nil {
			return filepath.Clean(filepath.Join(workingDir, raw))
		}
		return filepath.Clean(raw)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "./runtime"
	}
	for current := filepath.Clean(workingDir); ; current = filepath.Dir(current) {
		if info, statErr := os.Stat(filepath.Join(current, "docker-compose.yml")); statErr == nil && !info.IsDir() {
			return filepath.Join(current, "runtime")
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return filepath.Join(workingDir, "runtime")
}

func normalizeMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "upstream" || mode == "browser" || mode == "dry_run" || mode == "protocol" {
		return mode
	}
	return "browser"
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func split(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}
