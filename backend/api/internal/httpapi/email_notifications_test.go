package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestEmailConfigViewDoesNotExposeSMTPPassword(t *testing.T) {
	server := &Server{Cfg: config.Config{
		EmailEnabled: true, EmailNotifyPurchase: true, EmailNotifyRedeem: false,
		EmailSiteName: "KC GPT", EmailSMTPHost: "smtp.example.test", EmailSMTPPort: 2525,
		EmailSMTPUsername: "mailer@example.test", EmailSMTPPassword: "smtp-secret",
		EmailSMTPFrom: "noreply@example.test", EmailSMTPFromName: "KC GPT", EmailSMTPUseTLS: true,
		EmailSMTPTimeoutSeconds: 12,
	}}
	values := server.emailConfigValues()
	if values["emailSMTPHost"] != "smtp.example.test" || values["emailSMTPPort"] != "2525" {
		t.Fatalf("smtp settings = %#v", values)
	}
	if values["emailSMTPPasswordSavedValue"] != "true" {
		t.Fatalf("password saved marker = %#v", values["emailSMTPPasswordSavedValue"])
	}
	if _, exposed := values["emailSMTPPassword"]; exposed {
		t.Fatal("smtp password was returned to the admin view")
	}
	if values["emailSMTPUsername"] != "mailer@example.test" {
		t.Fatalf("smtp username = %q", values["emailSMTPUsername"])
	}
}

func TestAdminConfigViewNormalizesEmailFlags(t *testing.T) {
	view := adminConfigView(map[string]string{
		"emailEnabled":                "true",
		"emailNotifyPurchase":         "1",
		"emailNotifyRedeem":           "0",
		"emailSMTPUseTLS":             "false",
		"emailSMTPPasswordSavedValue": "true",
	}, false, false, false)
	checks := map[string]bool{
		"emailEnabled":                true,
		"emailNotifyPurchase":         true,
		"emailNotifyRedeem":           false,
		"emailSMTPUseTLS":             false,
		"emailSMTPPasswordSavedValue": true,
	}
	for key, want := range checks {
		got, ok := view[key].(bool)
		if !ok || got != want {
			t.Errorf("%s = %#v, want %v", key, view[key], want)
		}
	}
}

func TestLegacyEmailConfigIgnoresBlankAndMaskedPassword(t *testing.T) {
	if values := legacyEmailConfig(map[string]any{"email_smtp_password": "", "emailSMTPPassword": ""}); len(values) != 0 {
		t.Fatalf("blank password values = %#v", values)
	}
	if values := legacyEmailConfig(map[string]any{"email_smtp_password": "********"}); len(values) != 0 {
		t.Fatalf("masked password values = %#v", values)
	}
	values := legacyEmailConfig(map[string]any{"email_smtp_password": "  smtp-secret  "})
	if values["email_smtp_password"] != "smtp-secret" {
		t.Fatalf("password value = %#v", values)
	}
}

func TestPurchaseEmailContainsCDKOrderAndRedeemURL(t *testing.T) {
	settings := emailNotificationConfig{SiteName: "KC GPT", PublicBaseURL: "https://pay.example.test"}
	order := models.StoreOrder{
		OrderNo: "ORD-20260827", Email: "buyer@example.test", Status: storeOrderPaid,
		Amount: 20, Currency: "CNY", CDKCode: "KC-SECRET-1234",
		Plan: models.Plan{Code: "plus", Name: "ChatGPT Plus"},
	}
	to, subject, plain, htmlBody, err := buildPurchaseEmail(order, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{to, subject, plain, htmlBody} {
		if strings.TrimSpace(value) == "" {
			t.Fatal("purchase email contains an empty part")
		}
	}
	for _, expected := range []string{"KC-SECRET-1234", "ORD-20260827", "ChatGPT Plus", "https://pay.example.test/recharge?tab=redeem"} {
		if !strings.Contains(plain, expected) || !strings.Contains(htmlBody, expected) {
			t.Fatalf("purchase email does not contain %q: plain=%q html=%q", expected, plain, htmlBody)
		}
	}
	if strings.Contains(plain, "trace") || strings.Contains(htmlBody, "trace") {
		t.Fatal("internal trace id leaked into customer purchase mail")
	}
}

func TestRedeemEmailContainsCompletionTimeAndEscapesHTML(t *testing.T) {
	finished := time.Date(2026, time.August, 27, 12, 34, 56, 0, time.UTC)
	settings := emailNotificationConfig{SiteName: "KC <GPT>", PublicBaseURL: "https://pay.example.test"}
	order := models.StoreOrder{
		OrderNo: "ORD-1", Email: "buyer@example.test", Status: storeOrderPaid, CDKCode: "KC-1",
		Plan: models.Plan{Name: "Plus & Pro"},
	}
	task := models.RechargeTask{Status: models.TaskSucceeded, FinishedAt: &finished, Plan: models.Plan{Name: "Plus & Pro"}}
	_, subject, plain, htmlBody, err := buildRedeemEmail(order, task, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subject, "兑换成功") || !strings.Contains(plain, "2026-08-27 12:34:56") {
		t.Fatalf("redeem email = %q / %q", subject, plain)
	}
	if !strings.Contains(htmlBody, "Plus &amp; Pro") || !strings.Contains(htmlBody, "KC &lt;GPT&gt;") {
		t.Fatalf("html content was not escaped: %q", htmlBody)
	}
}
