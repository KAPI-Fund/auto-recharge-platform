package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	APIBaseURL       string
	WorkerToken      string
	RedisURL         string
	QueueName        string
	WorkerID         string
	Proxy            string
	PaymentRegion    string
	PaymentTestMode  string
	PauseBeforeSubmit bool
	SubmitPayment    bool
	MaxCardAttempts  int
	MaxAttempts      int
	Debug            bool
	RuntimeDir       string
	ChromeProfile    string
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func Load() Config {
	testMode := strings.ToLower(env("PAYMENT_TEST_MODE", ""))
	return Config{
		APIBaseURL:        strings.TrimRight(env("API_BASE_URL", "http://127.0.0.1:28080"), "/"),
		WorkerToken:       env("WORKER_API_TOKEN", "dev-worker-token"),
		RedisURL:          env("REDIS_URL", "redis://127.0.0.1:16379/0"),
		QueueName:         env("QUEUE_NAME", "recharge:tasks"),
		WorkerID:          env("WORKER_ID", "protocol-worker"),
		Proxy:             env("PROXY", ""),
		PaymentRegion:     strings.ToUpper(env("PAYMENT_REGION_OVERRIDE", env("PAYMENT_REGION", ""))),
		PaymentTestMode:   testMode,
		PauseBeforeSubmit: testMode == "pause" || testMode == "pause_before_submit" || envBool("PROTOCOL_PAUSE_BEFORE_SUBMIT", false),
		SubmitPayment:     envBool("PROTOCOL_SUBMIT_PAYMENT", true) && testMode != "pause" && testMode != "pause_before_submit",
		MaxCardAttempts:   envInt("PAYMENT_MAX_CARD_ATTEMPTS", 3),
		MaxAttempts:       envInt("LEGACY_MAX_ATTEMPTS", 3),
		Debug:             envBool("PROTOCOL_DEBUG", false),
		RuntimeDir:        env("RUNTIME_DIR", "./runtime"),
		ChromeProfile:     env("PROTOCOL_CHROME_PROFILE", "chrome"),
	}
}
