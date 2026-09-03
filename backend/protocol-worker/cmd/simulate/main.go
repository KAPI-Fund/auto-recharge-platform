package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/flow"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/store"
)

func main() {
	command := "probe"
	for _, arg := range os.Args[1:] {
		switch arg {
		case "probe", "checkout", "pay", "decline":
			command = arg
		}
	}
	cfg := config.Load()
	proxy := firstNonEmpty(flagValue("proxy"), cfg.Proxy, os.Getenv("PROXY"), "http://127.0.0.1:7897")
	region := strings.ToUpper(firstNonEmpty(flagValue("region"), cfg.PaymentRegion, "SG"))
	cfg.Proxy = proxy
	cfg.PaymentRegion = region

	if command == "probe" {
		result := flow.Probe(proxy, region)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if ok, _ := result["ok"].(bool); !ok {
			os.Exit(1)
		}
		return
	}

	sessionRaw := loadSession()
	if sessionRaw == "" {
		fmt.Fprintln(os.Stderr, "checkout/pay 需要 SESSION_JSON 或 --session-file")
		os.Exit(2)
	}
	cfg.PauseBeforeSubmit = command == "checkout"
	cfg.PaymentTestMode = ""
	if command == "decline" {
		cfg.PaymentTestMode = "decline"
	}
	if command == "pay" || command == "decline" {
		cfg.SubmitPayment = true
		cfg.PauseBeforeSubmit = false
	}
	var api *store.Client
	if cfg.APIBaseURL != "" {
		api = store.New(cfg.APIBaseURL, cfg.WorkerToken)
	}
	result := flow.Run(flow.Options{
		Config: cfg,
		Store:  api,
		Secret: map[string]any{
			"session":  sessionRaw,
			"region":   region,
			"proxy":    proxy,
			"planType": firstNonEmpty(flagValue("plan"), "plus"),
			"cdkCode":  firstNonEmpty(flagValue("cdk"), "[protocol-simulate]"),
		},
		OnLog: func(text string) { fmt.Println("[protocol]", text) },
		OnProg: func(progress int, message string) {
			fmt.Printf("[%d%%] %s\n", progress, message)
		},
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"status":     result.Status,
		"message":    result.Message,
		"errorCode":  result.ErrorCode,
		"cardLast4":  result.CardLast4,
		"sessionId":  truncate(result.SessionID, 18),
		"checkoutUrl": truncate(result.CheckoutURL, 80),
	})
	if result.Status == "succeeded" || result.ErrorCode == "payment_paused_before_submit" {
		return
	}
	os.Exit(1)
}

func loadSession() string {
	if path := flagValue("session-file"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return string(raw)
	}
	return firstNonEmpty(os.Getenv("SESSION_JSON"), os.Getenv("CHATGPT_SESSION_JSON"), os.Getenv("CHATGPT_TOKEN"))
}

func flagValue(name string) string {
	prefix := "--" + name + "="
	args := os.Args[1:]
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
		if arg == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
