package httpapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/paymentregion"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Server) legacyAdminPaths(c *gin.Context) {
	loginPath, panelPath := s.adminPaths()
	loginURL, panelURL := "/"+loginPath, "/"+panelPath
	c.JSON(http.StatusOK, gin.H{"success": true, "loginPath": loginPath, "panelPath": panelPath, "loginUrl": loginURL, "panelUrl": panelURL, "paths": gin.H{"loginPath": loginPath, "panelPath": panelPath, "loginUrl": loginURL, "panelUrl": panelURL}})
}

func (s *Server) adminPaths() (string, string) {
	loginPath := strings.Trim(normalizePath(s.configValue("admin_login_path", s.Cfg.AdminLoginPath), "/admin-login"), "/")
	panelPath := strings.Trim(normalizePath(s.configValue("admin_panel_path", s.Cfg.AdminPanelPath), "/admin"), "/")
	return loginPath, panelPath
}

func (s *Server) legacyPaymentRegion(c *gin.Context) {
	region := strings.ToUpper(s.configValue("payment_region", "PH"))
	currency, label := regionBilling(region)
	c.JSON(http.StatusOK, gin.H{"success": true, "region": region, "payment_region": region, "currency": currency, "label": label, "options": legacyRegionOptions()})
}

func (s *Server) legacyRuntime(c *gin.Context) {
	var running, total int64
	if err := s.DB.Model(&models.RechargeTask{}).Where("status IN ?", []string{models.TaskQueued, models.TaskRunning}).Count(&running).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取运行中任务数量失败")
		return
	}
	if err := s.DB.Model(&models.RechargeTask{}).Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取任务总数失败")
		return
	}
	maxConcurrent := maxInt(1, parseConfigInt(s.configValue("max_concurrent_activations", "1"), 1))
	c.JSON(http.StatusOK, gin.H{
		"success": true, "runningTasks": running, "totalTasks": total, "activeJobs": running,
		"maintenance": s.configValue("maintenance_mode", "0") == "1",
		"runtime":     gin.H{"active_foreground_jobs": running, "max_foreground_jobs": maxConcurrent},
	})
}

func regionBilling(region string) (string, string) {
	if value, ok := paymentregion.Get(region); ok {
		return value.Currency, value.CountryLabel
	}
	value := paymentregion.Default()
	return value.Currency, value.CountryLabel
}

func legacyRegionOptions() []gin.H {
	options := make([]gin.H, 0, len(paymentregion.All()))
	for _, value := range paymentregion.All() {
		options = append(options, gin.H{
			"value": value.Code, "code": value.Code, "label": value.Label,
			"countryLabel": value.CountryLabel, "currency": value.Currency,
		})
	}
	return options
}

func (s *Server) legacyPlans(c *gin.Context) {
	var plans []models.Plan
	if err := s.DB.Where("active = ?", true).Order("sort_order ASC, code ASC").Find(&plans).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(plans))
	for _, plan := range plans {
		decoratePlanInventory(&plan)
		items = append(items, gin.H{
			"code": plan.Code, "plan_type": plan.Code, "plan_name": db.NormalizeProviderPlanName(plan.Code, plan.ProviderPlanName), "name": plan.Name, "label": plan.Name,
			"country": plan.Country, "currency": models.PlatformStoreCurrency, "price": plan.Price,
			"saleLimit": plan.SaleLimit, "soldCount": plan.SoldCount, "remainingQuantity": plan.RemainingQuantity, "soldOut": plan.SoldOut,
			"purchaseEnabled": plan.PurchaseEnabled, "availabilityLabel": plan.AvailabilityLabel,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "plans": items})
}

func (s *Server) legacySaveConfig(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	// The React admin panel uses the camelCase names returned by getConfig,
	// while the legacy HTML contract also accepts snake_case names. Normalize
	// both forms before applying the allow-list so a visible setting cannot be
	// silently ignored.
	input = normalizeLegacyConfigInput(input)
	if err := s.validateCardProviderSettings(cardProviderOverridesFromAny(input)); err != nil {
		writeCardProviderValidationError(c, err)
		return
	}
	values := make(map[string]string, len(input)+8)
	allowed := map[string]bool{
		"mode": true, "upstreamBaseURL": true, "upstreamCreatePath": true, "upstreamStatusPath": true,
		"max_concurrent_activations": true, "max_background_concurrent": true, "maintenance_mode": true, "maintenance_mode_drain": true,
		"random_email_domain": true, "inbox_api_base": true, "inbox_email_domain": true, "inbox_email_domains": true,
		"checkout_mode": true, "browser_pool_enabled": true, "browserCheckoutURL": true, "browserHeadless": true, "runtimeDir": true,
		"pool_email_imap_host": true, "pool_email_imap_port": true, "pool_email_include_junk": true,
		"recharge_queued_timeout_seconds": true, "recharge_task_lease_timeout_seconds": true,
		"worker_log_level": true,
		"store_debug_mode": true, "stripe_success_url": true, "stripe_cancel_url": true, "public_base_url": true,
		"email_enabled": true, "email_notify_purchase": true, "email_notify_redeem": true, "email_site_name": true,
		"email_smtp_host": true, "email_smtp_port": true, "email_smtp_username": true, "email_smtp_from": true,
		"email_smtp_from_name": true, "email_smtp_use_tls": true, "email_smtp_timeout_seconds": true,
		"card_pool_default_id": true, "card_pool_routing": true, "card_pool_default_provider": true, "card_pool_card_creation_mode": true,
		"card_pool_cancel_after_payment":   true,
		"card_provider_local_text_enabled": true, "card_provider_airwallex_enabled": true,
		"card_provider_stripe_issuing_enabled": true,
		"card_provider_photonpay_enabled":      true, "card_provider_dogpay_enabled": true,
		"card_provider_kimoox_enabled": true,
		"airwallex_base_url":           true, "airwallex_primary_currency": true, "airwallex_card_purpose": true,
		"airwallex_card_type": true, "airwallex_form_factor": true, "airwallex_activate_on_issue": true,
		"airwallex_created_by": true, "airwallex_cardholder_id": true,
		"airwallex_webhook_tolerance_seconds": true,
		"stripe_issuing_base_url":             true, "stripe_issuing_currency": true, "stripe_issuing_cardholder_id": true,
		"stripe_issuing_webhook_tolerance_seconds": true,
		"photonpay_base_url":                       true, "photonpay_card_bin": true, "photonpay_card_type": true,
		"photonpay_card_form_factor": true, "photonpay_card_scheme": true, "photonpay_account_id": true,
		"photonpay_member_id": true, "photonpay_matrix_account": true, "photonpay_nickname": true,
		"photonpay_primary_currency": true, "photonpay_transaction_limit_type": true,
		"photonpay_webhook_tolerance_seconds": true,
		"dogpay_base_url":                     true, "dogpay_channel_id": true, "dogpay_entity_id": true,
		"dogpay_card_type": true, "dogpay_budget_id": true,
		"dogpay_webhook_tolerance_seconds": true,
		"kimoox_base_url":                  true, "kimoox_card_bin_ids": true, "kimoox_card_type": true,
		"kimoox_prepaid_amount_mode": true, "kimoox_prepaid_recharge_amount": true,
		"kimoox_cardholder_id": true, "kimoox_holder_id": true, "kimoox_card_group_id": true,
		"kimoox_budget_id": true, "kimoox_webhook_tolerance_seconds": true,
		"kimoox_apply_poll_attempts": true, "kimoox_apply_poll_interval_seconds": true,
	}
	for key := range input {
		if !allowed[key] {
			continue
		}
		value := stringValue(input, key)
		switch key {
		case "max_concurrent_activations", "max_background_concurrent":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-1000 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "checkout_mode":
			mode := strings.ToLower(strings.TrimSpace(value))
			switch mode {
			case "api", "ui", "api_then_ui":
				value = mode
			default:
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "checkout_mode 必须是 api / ui / api_then_ui"})
				return
			}
		case "worker_log_level":
			level := normalizeWorkerLogLevel(value)
			if level != strings.ToLower(strings.TrimSpace(value)) {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "worker_log_level 必须是 off / info / debug"})
				return
			}
			value = level
		case "maintenance_mode", "maintenance_mode_drain", "browser_pool_enabled",
			"card_provider_local_text_enabled", "card_provider_airwallex_enabled",
			"card_provider_stripe_issuing_enabled", "card_provider_photonpay_enabled",
			"card_provider_dogpay_enabled", "card_provider_kimoox_enabled", "airwallex_activate_on_issue",
			"card_pool_cancel_after_payment":
			value = boolConfigValue(input[key])
		case "random_email_domain", "inbox_email_domain":
			value = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "@"))
		case "inbox_email_domains":
			value = strings.Join(configDomainList(input[key]), "\n")
		case "pool_email_include_junk":
			value = boolConfigValue(input[key])
		case "pool_email_imap_port":
			value = strconv.Itoa(parseConfigInt(value, 993))
		case "email_enabled", "email_notify_purchase", "email_notify_redeem", "email_smtp_use_tls":
			value = boolConfigValue(input[key])
		case "email_smtp_port":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 65535 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-65535 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "email_smtp_timeout_seconds":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 300 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-300 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "recharge_queued_timeout_seconds":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 86400 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-86400 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "recharge_task_lease_timeout_seconds":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 5 || parsed > 3600 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 5-3600 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "airwallex_webhook_tolerance_seconds", "stripe_issuing_webhook_tolerance_seconds",
			"photonpay_webhook_tolerance_seconds", "dogpay_webhook_tolerance_seconds",
			"kimoox_webhook_tolerance_seconds":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 86400 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 1-86400 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "card_pool_routing":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "FIXED" && value != "PRIORITY" && value != "WEIGHTED" && value != "FAILOVER" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "card_pool_routing 仅支持 FIXED、PRIORITY、WEIGHTED、FAILOVER"})
				return
			}
		case "card_pool_default_provider":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "LOCAL_TEXT" && value != "AIRWALLEX" && value != "STRIPE_ISSUING" && value != "PHOTONPAY" && value != "DOGPAY" && value != "KIMOOX" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "card_pool_default_provider 仅支持 LOCAL_TEXT、AIRWALLEX、STRIPE_ISSUING、PHOTONPAY、DOGPAY、KIMOOX"})
				return
			}
		case "card_pool_card_creation_mode":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "POOL_ONLY" && value != "CREATE_ON_DEMAND" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "card_pool_card_creation_mode 仅支持 POOL_ONLY、CREATE_ON_DEMAND"})
				return
			}
		case "kimoox_apply_poll_attempts":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 1 || parsed > 300 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kimoox_apply_poll_attempts 必须是 1-300 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "kimoox_apply_poll_interval_seconds":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 0 || parsed > 300 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kimoox_apply_poll_interval_seconds 必须是 0-300 的整数"})
				return
			}
			value = strconv.Itoa(parsed)
		case "kimoox_prepaid_amount_mode":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "PLAN_PLUS_5" && value != "FIXED" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kimoox_prepaid_amount_mode 仅支持 PLAN_PLUS_5、FIXED"})
				return
			}
		case "kimoox_prepaid_recharge_amount":
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed <= 0 || parsed > 100000 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kimoox_prepaid_recharge_amount 必须是大于 0 且不超过 100000 的金额"})
				return
			}
			value = strconv.FormatFloat(parsed, 'f', -1, 64)
		case "kimoox_card_type":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "PREPAID" && value != "BUDGET" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kimoox_card_type 仅支持 PREPAID、BUDGET"})
				return
			}
		case "airwallex_form_factor":
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "VIRTUAL" && value != "PHYSICAL" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "airwallex_form_factor 仅支持 VIRTUAL、PHYSICAL"})
				return
			}
		case "airwallex_primary_currency", "stripe_issuing_currency", "photonpay_primary_currency":
			value = strings.ToUpper(strings.TrimSpace(value))
			if len(value) != 3 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": key + " 必须是 3 位币种代码"})
				return
			}
		case "mode":
			value = normalizeMode(value, "browser")
			if value == "protocol" {
				value = "browser"
			}
			if value != "browser" && value != "dry_run" && value != "upstream" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "mode 仅支持 browser"})
				return
			}
		}
		values[key] = value
	}
	if _, hasEmailSource := input["email_source"]; hasEmailSource {
		source := strings.ToLower(strings.TrimSpace(stringValue(input, "email_source")))
		if source != "random" && source != "pool" && source != "inbox" {
			source = "random"
		}
		values["email_source"] = source
		values["pool_email_enabled"] = map[bool]string{true: "1", false: "0"}[source == "pool"]
	} else if raw, exists := input["pool_email_enabled"]; exists {
		source := "random"
		if boolConfigValue(raw) == "1" {
			source = "pool"
		}
		values["email_source"] = source
		values["pool_email_enabled"] = map[bool]string{true: "1", false: "0"}[source == "pool"]
	}
	for key, value := range legacyStoreStripeConfig(input) {
		values[key] = value
	}
	for key, value := range legacyEmailConfig(input) {
		values[key] = value
	}
	for key, value := range legacyCardProviderSecrets(input) {
		values[key] = value
	}
	if err := s.upsertConfigBatch(values); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "配置保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "配置已保存"})
}

func normalizeLegacyConfigInput(input map[string]any) map[string]any {
	aliases := map[string]string{
		"storeDebugMode":                       "store_debug_mode",
		"stripeSecretKey":                      "stripe_secret_key",
		"stripeWebhookSecret":                  "stripe_webhook_secret",
		"stripeSuccessURL":                     "stripe_success_url",
		"stripeCancelURL":                      "stripe_cancel_url",
		"publicBaseURL":                        "public_base_url",
		"proxyRefreshTimeoutSeconds":           "proxy_refresh_timeout_seconds",
		"proxyRefreshWaitMs":                   "proxy_refresh_wait_ms",
		"emailEnabled":                         "email_enabled",
		"emailNotifyPurchase":                  "email_notify_purchase",
		"emailNotifyRedeem":                    "email_notify_redeem",
		"emailSiteName":                        "email_site_name",
		"emailSMTPHost":                        "email_smtp_host",
		"emailSMTPPort":                        "email_smtp_port",
		"emailSMTPUsername":                    "email_smtp_username",
		"emailSMTPPassword":                    "email_smtp_password",
		"emailSMTPFrom":                        "email_smtp_from",
		"emailSMTPFromName":                    "email_smtp_from_name",
		"emailSMTPUseTLS":                      "email_smtp_use_tls",
		"emailSMTPTimeoutSeconds":              "email_smtp_timeout_seconds",
		"rechargeQueuedTimeoutSeconds":         "recharge_queued_timeout_seconds",
		"rechargeTaskLeaseTimeoutSeconds":      "recharge_task_lease_timeout_seconds",
		"workerLogLevel":                       "worker_log_level",
		"cardPoolDefaultID":                    "card_pool_default_id",
		"cardPoolRouting":                      "card_pool_routing",
		"cardPoolDefaultProvider":              "card_pool_default_provider",
		"cardPoolCardCreationMode":             "card_pool_card_creation_mode",
		"cardPoolCancelAfterPayment":           "card_pool_cancel_after_payment",
		"localTextEnabled":                     "card_provider_local_text_enabled",
		"airwallexEnabled":                     "card_provider_airwallex_enabled",
		"photonpayEnabled":                     "card_provider_photonpay_enabled",
		"dogpayEnabled":                        "card_provider_dogpay_enabled",
		"airwallexBaseURL":                     "airwallex_base_url",
		"airwallexPrimaryCurrency":             "airwallex_primary_currency",
		"airwallexCardPurpose":                 "airwallex_card_purpose",
		"airwallexCardType":                    "airwallex_card_type",
		"airwallexFormFactor":                  "airwallex_form_factor",
		"airwallexActivateOnIssue":             "airwallex_activate_on_issue",
		"airwallexCreatedBy":                   "airwallex_created_by",
		"airwallexCardholderID":                "airwallex_cardholder_id",
		"airwallexWebhookToleranceSeconds":     "airwallex_webhook_tolerance_seconds",
		"stripeIssuingEnabled":                 "card_provider_stripe_issuing_enabled",
		"stripeIssuingBaseURL":                 "stripe_issuing_base_url",
		"stripeIssuingCurrency":                "stripe_issuing_currency",
		"stripeIssuingCardholderID":            "stripe_issuing_cardholder_id",
		"stripeIssuingWebhookToleranceSeconds": "stripe_issuing_webhook_tolerance_seconds",
		"airwallexClientID":                    "airwallex_client_id",
		"airwallexAPIKey":                      "airwallex_api_key",
		"airwallexWebhookSecret":               "airwallex_webhook_secret",
		"stripeIssuingSecretKey":               "stripe_issuing_secret_key",
		"stripeIssuingWebhookSecret":           "stripe_issuing_webhook_secret",
		"photonpayBaseURL":                     "photonpay_base_url",
		"photonpayCardBIN":                     "photonpay_card_bin",
		"photonpayCardType":                    "photonpay_card_type",
		"photonpayCardFormFactor":              "photonpay_card_form_factor",
		"photonpayCardScheme":                  "photonpay_card_scheme",
		"photonpayAccountID":                   "photonpay_account_id",
		"photonpayMemberID":                    "photonpay_member_id",
		"photonpayMatrixAccount":               "photonpay_matrix_account",
		"photonpayNickname":                    "photonpay_nickname",
		"photonpayPrimaryCurrency":             "photonpay_primary_currency",
		"photonpayTransactionLimitType":        "photonpay_transaction_limit_type",
		"photonpayWebhookToleranceSeconds":     "photonpay_webhook_tolerance_seconds",
		"dogpayBaseURL":                        "dogpay_base_url",
		"dogpayChannelID":                      "dogpay_channel_id",
		"dogpayEntityID":                       "dogpay_entity_id",
		"dogpayCardType":                       "dogpay_card_type",
		"dogpayBudgetID":                       "dogpay_budget_id",
		"dogpayWebhookToleranceSeconds":        "dogpay_webhook_tolerance_seconds",
		"dogpayAppID":                          "dogpay_appid",
		"dogpaySecret":                         "dogpay_secret",
		"dogpayWebhookSecret":                  "dogpay_webhook_secret",
		"dogpayCardholderID":                   "dogpay_cardholder_id",
		"dogpayPrivateKey":                     "dogpay_private_key",
		"kimooxEnabled":                        "card_provider_kimoox_enabled",
		"kimooxBaseURL":                        "kimoox_base_url",
		"kimooxCardBINIDs":                     "kimoox_card_bin_ids",
		"kimooxCardType":                       "kimoox_card_type",
		"kimooxPrepaidAmountMode":              "kimoox_prepaid_amount_mode",
		"kimooxPrepaidRechargeAmount":          "kimoox_prepaid_recharge_amount",
		"kimooxCardholderID":                   "kimoox_cardholder_id",
		"kimooxHolderID":                       "kimoox_holder_id",
		"kimooxCardGroupID":                    "kimoox_card_group_id",
		"kimooxBudgetID":                       "kimoox_budget_id",
		"kimooxWebhookToleranceSeconds":        "kimoox_webhook_tolerance_seconds",
		"kimooxApplyPollAttempts":              "kimoox_apply_poll_attempts",
		"kimooxApplyPollIntervalSeconds":       "kimoox_apply_poll_interval_seconds",
		"kimooxAPIKey":                         "kimoox_api_key",
		"kimooxAPISecret":                      "kimoox_api_secret",
		"kimooxWebhookSecret":                  "kimoox_webhook_secret",
		"photonpayAppID":                       "photonpay_app_id",
		"photonpayAppSecret":                   "photonpay_app_secret",
		"photonpayCardholderID":                "photonpay_cardholder_id",
		"photonpayPrivateKey":                  "photonpay_private_key",
		"photonpayWebhookPublicKey":            "photonpay_webhook_public_key",
	}
	normalized := make(map[string]any, len(input))
	for key, value := range input {
		if _, isAlias := aliases[key]; !isAlias {
			normalized[key] = value
		}
	}
	// React edits the camelCase field while the GET response also contains the
	// legacy snake_case mirror. The edited field must win over that stale mirror.
	for alias, canonical := range aliases {
		if value, exists := input[alias]; exists {
			normalized[canonical] = value
		}
	}
	return normalized
}

func normalizeWorkerLogLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "info", "debug":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "info"
	}
}

func legacyStoreStripeConfig(input map[string]any) map[string]string {
	values := make(map[string]string, 2)
	for _, key := range []string{"stripe_secret_key", "stripe_webhook_secret"} {
		raw, provided := input[key]
		if !provided {
			continue
		}
		value := strings.TrimSpace(toString(raw))
		if value != "" && value != "********" {
			values[key] = value
		}
	}
	return values
}

func legacyEmailConfig(input map[string]any) map[string]string {
	values := make(map[string]string, 1)
	value, provided := input["email_smtp_password"]
	if !provided {
		return values
	}
	password := strings.TrimSpace(toString(value))
	if password != "" && password != "********" {
		values["email_smtp_password"] = password
	}
	return values
}

func legacyCardProviderSecrets(input map[string]any) map[string]string {
	values := make(map[string]string, 8)
	for _, key := range []string{
		"airwallex_client_id", "airwallex_api_key", "airwallex_webhook_secret",
		"stripe_issuing_secret_key", "stripe_issuing_webhook_secret",
		"photonpay_app_id", "photonpay_app_secret", "photonpay_cardholder_id", "photonpay_private_key", "photonpay_webhook_public_key",
		"dogpay_appid", "dogpay_secret", "dogpay_cardholder_id", "dogpay_private_key", "dogpay_webhook_secret",
		"kimoox_api_key", "kimoox_api_secret", "kimoox_webhook_secret",
	} {
		raw, provided := input[key]
		if !provided {
			continue
		}
		value := strings.TrimSpace(toString(raw))
		if value == "" || value == "********" {
			continue
		}
		values[key] = value
	}
	return values
}

func boolConfigValue(value any) string {
	if value == true || strings.EqualFold(strings.TrimSpace(toString(value)), "true") || strings.TrimSpace(toString(value)) == "1" {
		return "1"
	}
	return "0"
}

func configDomainList(value any) []string {
	if values, ok := value.([]any); ok {
		result := make([]string, 0, len(values))
		for _, item := range values {
			domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(toString(item)), "@"))
			if domain != "" {
				result = append(result, domain)
			}
		}
		return result
	}
	return splitConfigList(toString(value))
}

func (s *Server) legacyRegion(c *gin.Context) { s.legacyPaymentRegion(c) }

func (s *Server) legacySaveRegion(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	region := strings.ToUpper(stringValue(input, "region"))
	if region == "" {
		region = strings.ToUpper(stringValue(input, "payment_region"))
	}
	if !paymentregion.IsSupported(region) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "不支持的地区代码"})
		return
	}
	if err := s.upsertConfig("payment_region", region); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "region": region})
}

func (s *Server) createQueuedTask(ctx context.Context, code, sessionInput, mode, clientIP, traceID string) (models.RechargeTask, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	code = strings.TrimSpace(code)
	sessionRaw, sessionToken, err := security.NormalizeSessionPayload(sessionInput)
	if code == "" || err != nil {
		return models.RechargeTask{}, fmt.Errorf("缺少 CDK 或有效 Session")
	}
	mode = normalizeMode(mode, s.configValue("mode", s.Cfg.DefaultRechargeMode))
	if status, message := s.checkActivationGuards(code, mode, clientIP); status != 0 {
		return models.RechargeTask{}, fmt.Errorf("%s", message)
	}
	ciphertext, err := security.Encrypt(sessionRaw, s.Cfg.SessionEncryptionKey)
	if err != nil {
		return models.RechargeTask{}, err
	}
	admissionToken, err := s.acquireAdmission(ctx)
	if err != nil {
		return models.RechargeTask{}, err
	}
	var task models.RechargeTask
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.checkActivationCapacityTx(tx); err != nil {
			return err
		}
		var cdk models.CDK
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Plan").Where("code = ?", code).First(&cdk).Error; err != nil {
			return err
		}
		if cdk.Status != models.CDKAvailable || cdk.Type != "" && cdk.Type != models.CDKTypeSelf || cdk.CooldownUntil != nil && cdk.CooldownUntil.After(time.Now()) {
			return gorm.ErrDuplicatedKey
		}
		now := time.Now()
		queueDeadline := now.Add(s.taskQueuedTimeout())
		cdkID := cdk.ID
		task = models.RechargeTask{
			ID: db.NewID("task"), JobKey: db.NewID("job"), TraceID: traceID, CDKID: &cdkID, PlanID: cdk.PlanID, Mode: mode,
			TokenPreview: security.SessionPreview(sessionToken), SessionPreview: security.SessionPreview(sessionToken), SessionCiphertext: ciphertext, AdmissionToken: admissionToken,
			CDKCode: code, ClientIP: clientIP, Status: models.TaskQueued, Progress: 1, Message: "任务已排队", DisplayTime: now.Format("2006-01-02 15:04:05"), QueueDeadlineAt: &queueDeadline,
		}
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		if err := tx.Model(&cdk).Updates(map[string]any{"status": models.CDKProcessing, "used_by_task_id": task.ID}).Error; err != nil {
			return err
		}
		task.CDK = cdk
		task.CDK.Status = models.CDKProcessing
		task.CDK.UsedByTaskID = task.ID
		task.Plan = cdk.Plan
		return nil
	})
	if err != nil {
		s.releaseAdmission(admissionToken)
		return models.RechargeTask{}, err
	}
	if err := s.Q.Enqueue(ctx, queueMessage(task.ID, mode, task.TraceID)); err != nil {
		s.releaseTask(task.ID, "queue_unavailable", "任务入队失败，请稍后重试")
		return models.RechargeTask{}, err
	}
	return task, nil
}

func queueMessage(taskID, mode, traceID string) queue.TaskMessage {
	return queue.TaskMessage{TaskID: taskID, Mode: mode, TraceID: traceID}
}

func (s *Server) legacyRunProcess(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	code := stringValue(input, "cdk")
	session := stringValue(input, "session")
	if session == "" {
		session = stringValue(input, "token")
	}
	if code == "" || session == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 CDK 或 Session"})
		return
	}
	token, err := security.NormalizeSession(session)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Session 无效，请粘贴完整 Session JSON 或 AccessToken"})
		return
	}
	if err := security.ValidateAccessToken(token); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	mode := s.resolveRechargeMode(stringValue(input, "mode"))
	if !s.guardOrAbort(c, code, mode, c.ClientIP(), subscriptionEmailFromRaw(session, token)) {
		return
	}
	task, err := s.createQueuedTask(c.Request.Context(), code, session, mode, c.ClientIP(), requestTraceID(c))
	if err != nil {
		status := http.StatusInternalServerError
		message := "CDK 不可用或任务创建失败"
		if errors.Is(err, errActivationCapacity) {
			status = http.StatusTooManyRequests
			message = "当前任务过多，请稍后再试"
		}
		if err == gorm.ErrRecordNotFound || err == gorm.ErrDuplicatedKey {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "jobKey": task.JobKey, "taskId": task.ID, "message": "任务已启动，正在为您开通中..."})
}

func (s *Server) legacyVerifyCDK(c *gin.Context) {
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	s.legacyVerifyCDKValue(c, stringValue(input, "cdk"))
}

func (s *Server) legacyVerifyCDKValue(c *gin.Context, code string) {
	var cdk models.CDK
	if err := s.DB.Preload("Plan").Where("code = ?", strings.TrimSpace(code)).First(&cdk).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无效 CDK"})
		return
	}
	var task models.RechargeTask
	if err := s.DB.Where("cdk_id = ? AND status IN ?", cdk.ID, []string{models.TaskQueued, models.TaskRunning}).Order("created_at DESC").First(&task).Error; err == nil {
		planType := legacyCDKPlanType(cdk)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"type": cdk.Type, "plan_type": planType, "plan_label": legacyPlanLabel(planType),
			"status": "processing", "taskStatus": task.Status, "taskId": task.ID, "jobKey": task.JobKey,
			"progress": clampProgress(task.Progress), "message": publicTaskMessage(task.Status),
			"task": publicTaskResponse(task),
		}})
		return
	}
	if cdk.Status == models.CDKAvailable {
		if cdk.CooldownUntil != nil && cdk.CooldownUntil.After(time.Now()) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "该卡密连续无资格尝试过多，请稍后再试"})
			return
		}
		planType := legacyCDKPlanType(cdk)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"type": cdk.Type, "plan_type": planType, "plan_label": legacyPlanLabel(planType)}})
		return
	}
	message := "CDK 无效或不可用"
	if cdk.Status == models.CDKUsed {
		message = "该 CDK 已使用"
	}
	c.JSON(http.StatusForbidden, gin.H{"success": false, "message": message})
}

func (s *Server) legacyCDKQuery(c *gin.Context) {
	s.legacyCDKQueryValue(c, c.Query("cdk"))
}

func (s *Server) legacyCDKQueryValue(c *gin.Context, code string) {
	var cdk models.CDK
	if err := s.DB.Where("code = ?", strings.TrimSpace(code)).First(&cdk).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "未找到该激活码记录"})
		return
	}
	var task models.RechargeTask
	if err := s.DB.Where("cdk_id = ?", cdk.ID).Order("created_at DESC, id DESC").First(&task).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusInternalServerError, "查询 CDK 任务状态失败")
		return
	}
	status := "未使用"
	activeTask := task.ID != "" && (task.Status == models.TaskQueued || task.Status == models.TaskRunning)
	if activeTask {
		status = "开通中"
	} else if cdk.Status == models.CDKUsed {
		status = "已使用"
	}
	data := gin.H{"status": status, "type": cdk.Type, "createdAt": cdk.CreatedAt, "jobKey": task.JobKey, "usedAt": cdk.UsedAt}
	if task.ID != "" {
		data["taskId"] = task.ID
		data["taskStatus"] = task.Status
		data["progress"] = clampProgress(task.Progress)
		data["message"] = publicTaskMessage(task.Status)
		data["task"] = publicTaskResponse(task)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func (s *Server) legacyCDKs(c *gin.Context) {
	var rows []models.CDK
	query := s.DB.Preload("Plan")
	query = query.Where("(type = ? OR type = '' OR type IS NULL)", models.CDKTypeSelf).Where("status <> ?", models.CDKDisabled)
	if err := query.Order("created_at DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	var tasks []models.RechargeTask
	if err := s.DB.Where("cdk_code <> '' AND token_preview <> ''").Order("created_at DESC, id DESC").Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取 CDK 关联 Session 失败")
		return
	}
	sessionByCDK := make(map[string]models.RechargeTask, len(tasks))
	runningCDKs := make(map[string]bool)
	for _, task := range tasks {
		if _, exists := sessionByCDK[task.CDKCode]; !exists {
			sessionByCDK[task.CDKCode] = task
		}
		if task.Status == models.TaskRunning {
			runningCDKs[task.CDKCode] = true
		}
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		planType := legacyCDKPlanType(row)
		status := "unused"
		if runningCDKs[row.Code] {
			status = "processing"
		} else if row.Status == models.CDKUsed {
			status = "used"
		} else if row.Status == models.CDKDisabled {
			status = "disabled"
		}
		task := sessionByCDK[row.Code]
		sessionPreview := task.TokenPreview
		if sessionPreview == "" {
			sessionPreview = task.SessionPreview
		}
		items = append(items, gin.H{
			"code": row.Code, "traceId": task.TraceID, "trace_id": task.TraceID, "status": status, "status_label": legacyCDKStatusLabel(status), "status_tone": legacyCDKStatusTone(status), "type": firstNonEmpty(row.Type, models.CDKTypeSelf), "plan_type": planType, "plan_label": legacyPlanLabel(planType), "plan_class": "cdk-plan-" + planType,
			"shipped": row.ShippedAt != nil, "shipped_at": legacyOptionalTimeString(row.ShippedAt), "shipped_label": map[bool]string{true: "已出库", false: "未出库"}[row.ShippedAt != nil], "shipped_class": map[bool]string{true: "active", false: ""}[row.ShippedAt != nil],
			"used_at": legacyOptionalTimeString(row.UsedAt), "used_at_text": legacyOptionalTimeString(row.UsedAt), "created_at": legacyTimeString(row.CreatedAt),
			"session_preview": nullableLegacyString(sessionPreview), "session_job_key": nullableLegacyString(task.JobKey),
		})
	}
	filtered := filterLegacyCDKs(items, c)
	page, pageSize, offset := paginationParams(c, 12, 100)
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	pageItems := filtered[offset:end]
	selectableCodes := []string{}
	if c.Query("select_unused") == "1" || strings.EqualFold(c.Query("select_unused"), "true") {
		for _, item := range pageItems {
			if stringValue(item, "status") == "unused" {
				if code := strings.TrimSpace(stringValue(item, "code")); code != "" {
					selectableCodes = append(selectableCodes, code)
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "cdks": pageItems, "total": total, "page": page, "pageSize": pageSize, "totalPages": pageCount(total, pageSize),
		"selectableCodes": selectableCodes,
		"filters": gin.H{
			"status":     []gin.H{{"value": "all", "label": "全部状态"}, {"value": "unused", "label": "未使用"}, {"value": "used", "label": "已使用"}, {"value": "unshipped", "label": "未出库"}, {"value": "shipped", "label": "已出库"}},
			"plan":       []gin.H{{"value": "all", "label": "全部套餐"}, {"value": "plus", "label": "Plus"}, {"value": "pro_5x", "label": "Pro 5x"}, {"value": "pro_20x", "label": "Pro 20x"}, {"value": "go", "label": "ChatGPT Go"}},
			"generation": []gin.H{{"value": "plus", "label": "Plus"}, {"value": "pro_5x", "label": "Pro 5x"}, {"value": "pro_20x", "label": "Pro 20x"}, {"value": "go", "label": "ChatGPT Go"}},
		},
	})
}

func filterLegacyCDKs(items []gin.H, c *gin.Context) []gin.H {
	search := strings.ToUpper(strings.TrimSpace(c.Query("search")))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	planType := normalizePlanType(c.Query("plan_type"))
	filterPlan := strings.TrimSpace(c.Query("plan_type")) != "" && strings.ToLower(strings.TrimSpace(c.Query("plan_type"))) != "all"
	shipped := strings.ToLower(strings.TrimSpace(c.Query("shipped")))
	filtered := make([]gin.H, 0, len(items))
	for _, item := range items {
		code := strings.ToUpper(stringValue(item, "code"))
		itemStatus := strings.ToLower(stringValue(item, "status"))
		itemPlan := strings.ToLower(stringValue(item, "plan_type"))
		itemShipped := item["shipped"] == true
		if search != "" && !strings.Contains(code, search) {
			continue
		}
		if status != "" && status != "all" && itemStatus != status {
			if !(status == "unshipped" && !itemShipped) && !(status == "shipped" && itemShipped) {
				continue
			}
		}
		if filterPlan && itemPlan != strings.ToLower(planType) {
			continue
		}
		if shipped == "true" && !itemShipped || shipped == "false" && itemShipped {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func legacyCDKStatusLabel(status string) string {
	switch status {
	case "processing":
		return "开通中"
	case "used":
		return "已使用"
	case "disabled":
		return "已停用"
	default:
		return "未使用"
	}
}

func legacyCDKStatusTone(status string) string {
	switch status {
	case "processing":
		return "info"
	case "used":
		return "success"
	case "disabled":
		return "danger"
	default:
		return "neutral"
	}
}

func legacyPlanLabel(plan string) string {
	plan = normalizePlanType(plan)
	switch plan {
	case "go":
		return "ChatGPT Go"
	case "pro_5x":
		return "Pro 5x"
	case "pro_20x":
		return "Pro 20x"
	default:
		return "Plus"
	}
}

func legacyCDKPlanType(row models.CDK) string {
	return normalizePlanType(firstNonEmpty(row.PlanType, row.Plan.Code, "plus"))
}

func paginationParams(c *gin.Context, defaultSize, maxSize int) (page, pageSize, offset int) {
	page = parseConfigInt(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	pageSize = parseConfigInt(c.Query("page_size"), defaultSize)
	if pageSize < 1 {
		pageSize = 1
	}
	if maxSize > 0 && pageSize > maxSize {
		pageSize = maxSize
	}
	offset = (page - 1) * pageSize
	return page, pageSize, offset
}

func pageCount(total, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}

func nullableLegacyString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Server) legacyGenerateCDKs(c *gin.Context) {
	var input struct {
		Count    int    `json:"count"`
		PlanType string `json:"plan_type"`
		Type     string `json:"type"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	input.Count = max(1, min(input.Count, 100))
	planType := normalizePlanType(input.PlanType)
	cdkType := normalizeCDKType(input.Type)
	var plan models.Plan
	if err := s.DB.Where("code = ?", planType).First(&plan).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "套餐不存在"})
		return
	}
	cdks := make([]models.CDK, 0, input.Count)
	for i := 0; i < input.Count; i++ {
		code, err := newCDKCode("KC")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		cdks = append(cdks, models.CDK{ID: db.NewID("cdk"), Code: code, PlanID: plan.ID, PlanType: planType, Type: cdkType, Status: models.CDKAvailable})
	}
	if err := s.DB.Create(&cdks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	codes := make([]string, 0, len(cdks))
	for _, row := range cdks {
		codes = append(codes, row.Code)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": fmt.Sprintf("成功生成 %d 个%s CDK", len(cdks), cdkType), "cdks": codes, "insertedCount": len(cdks), "plan_type": planType, "type": cdkType})
}

func (s *Server) legacyImportCDKs(c *gin.Context) {
	var input struct {
		CDKs     []string `json:"cdks"`
		Text     string   `json:"text"`
		PlanType string   `json:"plan_type"`
		Type     string   `json:"type"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供要导入的卡密"})
		return
	}
	if strings.TrimSpace(input.Text) != "" {
		input.CDKs = strings.FieldsFunc(input.Text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t' })
	}
	if len(input.CDKs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供要导入的卡密"})
		return
	}
	planType := normalizePlanType(input.PlanType)
	cdkType := normalizeCDKType(input.Type)
	var plan models.Plan
	if err := s.DB.Where("code = ?", planType).First(&plan).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "套餐不存在"})
		return
	}
	// mysql-store normalizes the payload before inserting: blank values are
	// ignored and duplicate values in the same request count once.
	cdks := normalizeLegacyCDKs(input.CDKs)
	inserted, duplicate := 0, 0
	for _, code := range cdks {
		var existing models.CDK
		lookupErr := s.DB.Where("code = ?", code).First(&existing).Error
		if lookupErr == nil {
			duplicate++
			continue
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			fail(c, http.StatusInternalServerError, "检查 CDK 重复状态失败")
			return
		}
		result := s.DB.Create(&models.CDK{ID: db.NewID("cdk"), Code: code, PlanID: plan.ID, PlanType: planType, Type: cdkType, Status: models.CDKAvailable})
		if result.Error == nil {
			inserted++
			continue
		}
		var raced models.CDK
		racedErr := s.DB.Where("code = ?", code).First(&raced).Error
		if racedErr == nil {
			duplicate++
			continue
		}
		fail(c, http.StatusInternalServerError, "导入 CDK 失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": fmt.Sprintf("导入完成，新增 %d 个，重复 %d 个", inserted, duplicate), "insertedCount": inserted, "duplicateCount": duplicate, "totalCount": inserted + duplicate, "type": cdkType})
}

func normalizeLegacyCDKs(rawValues []string) []string {
	cdks := make([]string, 0, len(rawValues))
	seen := make(map[string]struct{}, len(rawValues))
	for _, raw := range rawValues {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		cdks = append(cdks, code)
	}
	return cdks
}

func (s *Server) legacyShipCDK(c *gin.Context) {
	now := time.Now()
	result := s.DB.Model(&models.CDK{}).Where("code = ?", c.Param("cdk")).Updates(map[string]any{"shipped_at": gorm.Expr("COALESCE(shipped_at, ?)", now)})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "标记 CDK 出库失败")
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "CDK 不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "CDK 已标记出库"})
}

func (s *Server) legacyShipCDKs(c *gin.Context) {
	codes, err := legacyBatchCDKCodes(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	result := s.DB.Model(&models.CDK{}).Where("code IN ?", codes).Updates(map[string]any{"shipped_at": gorm.Expr("COALESCE(shipped_at, ?)", now)})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "批量标记 CDK 出库失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "updated": result.RowsAffected, "message": fmt.Sprintf("已标记 %d 个 CDK 出库", result.RowsAffected)})
}

func (s *Server) legacyDeleteCDKs(c *gin.Context) {
	codes, err := legacyBatchCDKCodes(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	result := s.DB.Where("code IN ?", codes).Delete(&models.CDK{})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "批量删除 CDK 失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": result.RowsAffected, "message": fmt.Sprintf("已删除 %d 个 CDK", result.RowsAffected)})
}

func legacyBatchCDKCodes(c *gin.Context) ([]string, error) {
	var input struct {
		Codes []string `json:"codes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		return nil, fmt.Errorf("请求格式不正确")
	}
	codes := normalizeLegacyCDKs(input.Codes)
	if len(codes) == 0 || len(codes) > 500 {
		return nil, fmt.Errorf("请选择 1 到 500 个 CDK")
	}
	return codes, nil
}

func (s *Server) legacyDeleteCDK(c *gin.Context) {
	result := s.DB.Where("code = ?", c.Param("cdk")).Delete(&models.CDK{})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "删除 CDK 失败")
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "CDK 不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "CDK 已删除"})
}

func normalizePlanType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plus", "chatgptplusplan":
		return "plus"
	case "pro_5x", "pro5x", "chatgptprolite":
		return "pro_5x"
	case "pro_20x", "pro20x", "chatgptpro":
		return "pro_20x"
	case "go", "chatgpt_go", "chatgptgoplan":
		return "go"
	default:
		return "plus"
	}
}

func normalizeCDKType(value string) string {
	if strings.TrimSpace(value) == models.CDKTypeProduct || strings.EqualFold(strings.TrimSpace(value), "product") {
		return models.CDKTypeProduct
	}
	return models.CDKTypeSelf
}

func parseConfigInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func (s *Server) legacyCards(c *gin.Context) {
	var rows []models.CardAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	now := time.Now()
	for _, row := range rows {
		isCooldown := cardIsCoolingDown(row, now)
		items = append(items, gin.H{"id": row.ID, "card_number": maskedCardNumber(row.Last4), "card_expiry": maskedCardExpiry, "card_cvc": maskedCardCVC, "last4": row.Last4, "card_holder": row.Holder, "payment_holder_name": row.PaymentHolderName, "payment_address_line1": row.PaymentAddressLine1, "payment_address_city": row.PaymentAddressCity, "payment_address_state": row.PaymentAddressState, "payment_address_postal": row.PaymentAddressPostal, "payment_address_id": row.PaymentAddressID, "is_active": boolToInt(row.Active), "usage_count": row.UsageCount, "daily_usage_count": row.DailyUsageCount, "last_used_at": row.LastUsedAt, "last_used_at_text": legacyOptionalTimeString(row.LastUsedAt), "status": row.Status, "status_label": cardStatusLabel(row.Status, isCooldown, row.InUse), "status_tone": cardStatusTone(row.Status, isCooldown, row.InUse), "is_cooldown": isCooldown, "in_use": row.InUse, "is_available": cardIsAvailable(row, now), "is_exhausted": cardIsExhausted(row), "cooldown_until": row.CooldownUntil})
	}
	stats := cardPoolStats(rows, now)
	if s.CardPools != nil {
		paymentCards, err := s.CardPools.ListCards(c.Query("poolId"))
		if err != nil {
			fail(c, http.StatusInternalServerError, "读取 Provider 卡池失败")
			return
		}
		for _, card := range paymentCards {
			// LOCAL_TEXT is already represented by the legacy CardAsset row. Do
			// not duplicate it in the unified admin table.
			if card.LocalCardAssetID != "" {
				continue
			}
			items = append(items, paymentCardAdminProjection(card, now))
			stats["total"] = stats["total"].(int) + 1
			if card.CooldownUntil != nil && card.CooldownUntil.After(now) {
				stats["cooldown"] = stats["cooldown"].(int) + 1
			} else if card.Status == cardpool.CardActive && !card.InUse {
				stats["active"] = stats["active"].(int) + 1
				stats["available"] = stats["active"]
			} else if card.Status == cardpool.CardUsed || card.Status == cardpool.CardCancelled || card.Status == cardpool.CardFailed {
				stats["exhausted"] = stats["exhausted"].(int) + 1
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "cards": items, "paymentCards": items, "stats": stats})
}

func paymentCardAdminProjection(card cardpool.PaymentCard, now time.Time) gin.H {
	coolingDown := card.CooldownUntil != nil && card.CooldownUntil.After(now)
	status := strings.ToUpper(strings.TrimSpace(string(card.Status)))
	statusLabel, statusTone := "未知", "neutral"
	switch status {
	case "ACTIVE":
		statusLabel, statusTone = "正常", "success"
	case "ASSIGNED", "IN_USE":
		statusLabel, statusTone = "使用中", "info"
	case "CREATING":
		statusLabel, statusTone = "创建中", "warning"
	case "FROZEN":
		statusLabel, statusTone = "已冻结", "warning"
	case "USED":
		statusLabel, statusTone = "已使用", "danger"
	case "CANCELLED":
		statusLabel, statusTone = "已销毁", "danger"
	case "FAILED":
		statusLabel, statusTone = "销毁待重试", "danger"
	default:
		if status != "" {
			statusLabel = status
		}
	}
	if coolingDown {
		statusLabel, statusTone = "冷却中", "warning"
	}
	return gin.H{
		"id": card.InternalCardID, "provider": card.Provider, "provider_card_id": card.ProviderCardID,
		"pool_id": card.PoolID, "last4": card.Last4, "card_number": maskedCardNumber(card.Last4),
		"card_holder": card.CardholderName, "holder": card.CardholderName, "payment_holder_name": card.CardholderName,
		"usage_type": string(card.UsageType), "usage_count": card.UsageCount, "usageCount": card.UsageCount, "daily_usage_count": card.DailyUsageCount,
		"last_used_at": card.LastUsedAt, "last_used_at_text": legacyOptionalTimeString(card.LastUsedAt),
		"status": status, "status_label": statusLabel, "status_tone": statusTone,
		"is_active": boolToInt(status == "ACTIVE"), "active": status == "ACTIVE", "is_available": status == "ACTIVE" && !card.InUse && !coolingDown,
		"is_exhausted": status == "USED" || status == "CANCELLED" || status == "FAILED",
		"is_cooldown":  coolingDown, "cooldown_until": card.CooldownUntil,
		"card_type": card.CardType, "currency": card.Currency, "provider_status": card.ProviderStatus,
		"last_failure_code": card.LastFailureCode, "last_failure_message": card.LastFailureMessage, "last_failure_at": card.LastFailureAt,
		"created_at": card.CreatedAt, "createdAt": card.CreatedAt, "updated_at": card.UpdatedAt, "updatedAt": card.UpdatedAt,
	}
}

type legacyCardImportInput struct {
	Number     string `json:"number"`
	CardNumber string `json:"card_number"`
	Expiry     string `json:"expiry"`
	CardExpiry string `json:"card_expiry"`
	CVC        string `json:"cvc"`
	CardCVC    string `json:"card_cvc"`
	Holder     string `json:"holder"`
	CardHolder string `json:"card_holder"`
}

func (s *Server) legacyImportCards(c *gin.Context) {
	var input struct {
		Cards []legacyCardImportInput `json:"cards"`
		Text  string                  `json:"text"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}
	if strings.TrimSpace(input.Text) != "" {
		input.Cards = parseLegacyCardImport(input.Text)
	}
	if len(input.Cards) == 0 || len(input.Cards) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少 cards 数组或为空"})
		return
	}
	imported, skipped, failed := 0, 0, 0
	failures := []gin.H{}
	var existing []models.CardAsset
	if err := s.DB.Find(&existing).Error; err != nil {
		fail(c, http.StatusInternalServerError, "检查银行卡重复状态失败")
		return
	}
	existingNumbers := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		stored, decryptErr := s.decryptSessionValue(row.CardNumberCiphertext)
		if decryptErr != nil {
			fail(c, http.StatusInternalServerError, "读取已有银行卡失败")
			return
		}
		existingNumbers[stored] = struct{}{}
	}
	seenNumbers := make(map[string]struct{}, len(input.Cards))
	for index, item := range input.Cards {
		number := strings.TrimSpace(firstNonEmpty(item.Number, item.CardNumber))
		expiry := normalizeLegacyCardExpiry(strings.TrimSpace(firstNonEmpty(item.Expiry, item.CardExpiry)))
		cvc := strings.TrimSpace(firstNonEmpty(item.CVC, item.CardCVC))
		holder := strings.TrimSpace(firstNonEmpty(item.Holder, item.CardHolder))
		if errors := legacyCardValidationErrors(number, expiry, cvc); len(errors) > 0 {
			failed++
			failures = append(failures, gin.H{"index": index, "errors": errors})
			continue
		}
		if _, duplicate := existingNumbers[number]; duplicate {
			skipped++
			continue
		}
		if _, duplicate := seenNumbers[number]; duplicate {
			skipped++
			continue
		}
		seenNumbers[number] = struct{}{}
		numberCipher, err1 := security.Encrypt(number, s.Cfg.SessionEncryptionKey)
		expiryCipher, err2 := security.Encrypt(expiry, s.Cfg.SessionEncryptionKey)
		cvcCipher, err3 := security.Encrypt(cvc, s.Cfg.SessionEncryptionKey)
		if err1 != nil || err2 != nil || err3 != nil {
			failed++
			failures = append(failures, gin.H{"index": index, "errors": []string{"敏感字段加密失败"}})
			continue
		}
		row := models.CardAsset{ID: db.NewID("card"), Last4: number[len(number)-4:], CardNumberCiphertext: numberCipher, ExpiryCiphertext: expiryCipher, CVVCiphertext: cvcCipher, Holder: holder, Status: "正常", Active: true}
		if err := s.DB.Create(&row).Error; err != nil {
			failed++
			failures = append(failures, gin.H{"index": index, "errors": []string{err.Error()}})
			continue
		}
		imported++
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "imported": imported, "skipped": skipped, "failed": failed, "created": imported, "failures": failures, "total": len(input.Cards)})
}

func parseLegacyCardImport(raw string) []legacyCardImportInput {
	items := make([]legacyCardImportInput, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		for index := range parts {
			parts[index] = strings.TrimSpace(parts[index])
		}
		item := legacyCardImportInput{}
		if len(parts) > 0 {
			item.Number = parts[0]
		}
		if len(parts) > 1 {
			item.Expiry = parts[1]
		}
		if len(parts) > 2 {
			item.CVC = parts[2]
		}
		if len(parts) > 3 {
			item.Holder = strings.Join(parts[3:], "|")
		}
		items = append(items, item)
	}
	return items
}

func normalizeLegacyCardExpiry(value string) string {
	if matched, _ := regexp.MatchString(`^\d{4}$`, value); matched {
		return value[:2] + "/" + value[2:]
	}
	return value
}

func cardIsCoolingDown(row models.CardAsset, now time.Time) bool {
	return strings.EqualFold(strings.TrimSpace(row.Status), "cooldown") || row.CooldownUntil != nil && row.CooldownUntil.After(now)
}

const (
	maskedCardExpiry = "**/**"
	maskedCardCVC    = "***"
)

func maskedCardNumber(last4 string) string {
	last4 = strings.TrimSpace(last4)
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	if last4 == "" {
		return "****"
	}
	return "**** **** **** " + last4
}

func cardIsExhausted(row models.CardAsset) bool {
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "exhausted", "disabled", "封禁", "报废", "已报废":
		return true
	default:
		return !row.Active
	}
}

func cardIsAvailable(row models.CardAsset, now time.Time) bool {
	if !row.Active || cardIsCoolingDown(row, now) || cardIsExhausted(row) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "", "active", "available", "ready", "正常":
		return !row.InUse
	default:
		return false
	}
}

func cardStatusLabel(status string, coolingDown bool, inUse bool) string {
	if coolingDown {
		return "冷却中"
	}
	if inUse {
		return "占用中"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "available", "ready", "正常":
		return "正常"
	case "exhausted", "disabled", "封禁", "报废", "已报废":
		return "已报废"
	default:
		return status
	}
}

func cardStatusTone(status string, coolingDown bool, inUse bool) string {
	if coolingDown || inUse {
		return "warning"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "正常", "normal", "active", "可用":
		return "success"
	case "报废", "已报废", "disabled", "封禁":
		return "danger"
	default:
		return "neutral"
	}
}

func cardPoolStats(rows []models.CardAsset, now time.Time) gin.H {
	available, cooldown, exhausted := 0, 0, 0
	for _, row := range rows {
		switch {
		case cardIsCoolingDown(row, now):
			cooldown++
		case cardIsExhausted(row):
			exhausted++
		case cardIsAvailable(row, now):
			available++
		}
	}
	return gin.H{"total": len(rows), "active": available, "available": available, "cooldown": cooldown, "exhausted": exhausted}
}

// legacyCardValidationErrors mirrors KC-PAY-GPT/card-validator.js so the
// imported-card result and persisted pool match the original admin workflow.
func legacyCardValidationErrors(number, expiry, cvc string) []string {
	errors := make([]string, 0, 3)
	if number == "" {
		errors = append(errors, "卡号不能为空")
	} else if !regexp.MustCompile(`^\d+$`).MatchString(number) {
		errors = append(errors, "卡号必须为纯数字")
	} else if len(number) < 13 || len(number) > 19 {
		errors = append(errors, "卡号长度必须为 13-19 位")
	}
	if expiry == "" {
		errors = append(errors, "有效期不能为空")
	} else if !regexp.MustCompile(`^\d{2}/\d{2}$`).MatchString(expiry) {
		errors = append(errors, "有效期格式必须为 MM/YY")
	} else {
		month, _ := strconv.Atoi(expiry[:2])
		if month < 1 || month > 12 {
			errors = append(errors, "有效期月份必须在 01-12 之间")
		}
	}
	if cvc == "" {
		errors = append(errors, "CVC 不能为空")
	} else if !regexp.MustCompile(`^\d{3,4}$`).MatchString(cvc) {
		errors = append(errors, "CVC 必须为 3-4 位数字")
	}
	return errors
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Server) legacyDeleteCard(c *gin.Context) {
	cardID := strings.TrimSpace(c.Param("id"))
	cancelProvider := boolConfigValue(c.Query("cancel_provider")) == "1"
	result := s.DB.Delete(&models.CardAsset{}, "id = ?", cardID)
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "删除银行卡失败")
		return
	}
	if result.RowsAffected > 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "localOnly": true})
		return
	}
	// Provider-issued cards are retained as audit rows. Deleting one from the
	// admin list never physically deletes the normalized card record.
	if s.CardPools != nil {
		var card models.PaymentCard
		if lookup := s.DB.Where("id = ?", cardID).First(&card); lookup.Error == nil {
			if card.InUse {
				c.JSON(http.StatusConflict, gin.H{"success": false, "message": "卡片正在被任务使用，暂不能删除"})
				return
			}
			if cancelProvider {
				if err := s.CardPools.MarkCardExhausted(c.Request.Context(), card.ID); err != nil {
					fail(c, http.StatusBadRequest, err.Error())
					return
				}
				c.JSON(http.StatusOK, gin.H{"success": true, "status": string(cardpool.CardCancelled), "providerCancelled": true})
				return
			}
			if err := s.CardPools.RetireCardLocally(c.Request.Context(), card.ID); err != nil {
				fail(c, http.StatusBadRequest, err.Error())
				return
			}
			c.JSON(http.StatusOK, gin.H{"success": true, "status": string(cardpool.CardCancelled), "providerCancelled": false})
			return
		} else if lookup.Error != nil && !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
			fail(c, http.StatusInternalServerError, "读取银行卡失败")
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "卡片不存在"})
}

func (s *Server) legacyExternalCardPush(c *gin.Context) {
	expected := s.configValue("external_card_api_key", "")
	provided := strings.TrimSpace(c.GetHeader("X-API-Key"))
	if expected == "" || provided == "" || provided != expected {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "API Key 无效或缺失"})
		return
	}
	s.legacyImportCards(c)
}

func (s *Server) billingQuery(c *gin.Context) *gorm.DB {
	query := s.DB.Model(&models.BillingRecord{})
	if value := strings.TrimSpace(c.Query("start_date")); value != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
			query = query.Where("payment_time >= ?", parsed)
		} else {
			query = query.Where("payment_time >= ?", value)
		}
	}
	if value := strings.TrimSpace(c.Query("end_date")); value != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
			query = query.Where("payment_time < ?", parsed.AddDate(0, 0, 1))
		} else {
			query = query.Where("payment_time <= ?", value+" 23:59:59.999999")
		}
	}
	if value := strings.TrimSpace(c.Query("card_last4")); value != "" {
		query = query.Where("card_last4 = ?", value)
	}
	if value := strings.TrimSpace(c.Query("plan_type")); value != "" {
		query = query.Where("plan_type = ?", value)
	}
	if value := strings.TrimSpace(c.Query("status")); value != "" {
		query = query.Where("status = ?", value)
	}
	return query
}

func (s *Server) billingResponse(row models.BillingRecord) (gin.H, error) {
	statusLabel, statusTone := billingStatusView(row.Status)
	planType := normalizePlanType(row.PlanType)
	return gin.H{"id": row.ID, "payment_time": row.PaymentTime, "payment_time_text": legacyTimeString(row.PaymentTime), "card_number": maskedCardNumber(row.CardLast4), "card_last4": row.CardLast4, "amount": row.Amount, "amount_text": fmt.Sprintf("%.2f", row.Amount), "currency": row.Currency, "plan_type": planType, "plan_label": legacyPlanLabel(planType), "stripe_session_id": row.StripeSessionID, "cdk_code": row.CDKCode, "email": row.Email, "status": row.Status, "status_label": statusLabel, "status_tone": statusTone, "error_code": row.ErrorCode, "error_message": row.ErrorMessage, "upstream_order_id": row.UpstreamOrderID, "created_at": row.CreatedAt}, nil
}

func billingStatusView(status string) (string, string) {
	if strings.EqualFold(strings.TrimSpace(status), "success") {
		return "成功", "success"
	}
	return "失败", "danger"
}

func (s *Server) legacyBilling(c *gin.Context) {
	page := parseConfigInt(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	pageSize := parseConfigInt(c.Query("page_size"), 20)
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	query := s.billingQuery(c)
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计账单数量失败")
		return
	}
	var rows []models.BillingRecord
	if err := query.Order("payment_time DESC, created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		item, err := s.billingResponse(row)
		if err != nil {
			fail(c, http.StatusInternalServerError, "读取账单敏感字段失败")
			return
		}
		item["traceId"] = row.TraceID
		item["trace_id"] = row.TraceID
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "records": items, "billing": items, "total": total, "page": page,
		"pageSize": pageSize, "totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		"filters": legacyBillingFilterOptions(),
	})
}

func legacyBillingFilterOptions() gin.H {
	return gin.H{
		"plan_type": []gin.H{
			{"value": "", "label": "全部"},
			{"value": "plus", "label": "Plus"},
			{"value": "pro_5x", "label": "Pro 5x"},
			{"value": "pro_20x", "label": "Pro 20x"},
			{"value": "go", "label": "ChatGPT Go"},
		},
		"status": []gin.H{
			{"value": "", "label": "全部"},
			{"value": "success", "label": "成功"},
			{"value": "failed", "label": "失败"},
		},
	}
}

func (s *Server) legacyBillingExport(c *gin.Context) {
	var rows []models.BillingRecord
	if err := s.billingQuery(c).Order("payment_time DESC").Limit(10000).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	var output bytes.Buffer
	output.WriteString("\xef\xbb\xbf")
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"支付时间", "卡号", "卡片后四位", "金额", "币种", "套餐类型", "Stripe Session ID", "CDK码", "邮箱", "状态", "错误码", "错误信息"}); err != nil {
		fail(c, http.StatusInternalServerError, "生成账单导出失败")
		return
	}
	for _, row := range rows {
		if err := writer.Write([]string{row.PaymentTime.Format("2006-01-02 15:04:05"), maskedCardNumber(row.CardLast4), row.CardLast4, strconv.FormatFloat(row.Amount, 'f', 2, 64), row.Currency, row.PlanType, row.StripeSessionID, row.CDKCode, row.Email, row.Status, row.ErrorCode, row.ErrorMessage}); err != nil {
			fail(c, http.StatusInternalServerError, "生成账单导出失败")
			return
		}
	}
	writer.Flush()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="billing_export.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", output.Bytes())
}

func (s *Server) legacyBillingSummary(c *gin.Context) {
	var result struct {
		Amount  float64
		Success int64
		Failed  int64
	}
	if err := s.DB.Model(&models.BillingRecord{}).Select("COALESCE(SUM(CASE WHEN status = 'success' THEN amount ELSE 0 END), 0) AS amount, COUNT(CASE WHEN status = 'success' THEN 1 END) AS success, COUNT(CASE WHEN status = 'failed' THEN 1 END) AS failed").Where("card_last4 = ?", c.Param("cardLast4")).Scan(&result).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计银行卡账单失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "cumulative_amount": result.Amount, "cumulative_amount_text": fmt.Sprintf("%.2f", result.Amount),
		"success_count": result.Success, "failed_count": result.Failed,
	})
}

func (s *Server) legacyDeleteFailedBilling(c *gin.Context) {
	result := s.DB.Where("status = ?", "failed").Delete(&models.BillingRecord{})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "删除失败账单失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": result.RowsAffected})
}

func (s *Server) legacyDeleteBilling(c *gin.Context) {
	result := s.DB.Delete(&models.BillingRecord{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "删除账单失败")
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "账单记录不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
