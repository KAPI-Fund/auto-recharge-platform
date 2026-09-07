package httpapi

import (
	"fmt"
	"strconv"
	"strings"
)

// normalizeModernConfigValues converts both the React camelCase contract and
// the canonical snake_case contract into the exact values persisted by the
// API. The returned map is safe to pass to upsertConfigBatch.
func normalizeModernConfigValues(input map[string]string) (map[string]string, error) {
	allowed := map[string]bool{
		"mode": true, "upstreamBaseURL": true, "upstreamCreatePath": true, "upstreamStatusPath": true,
		"storeDebugMode": true, "stripeSecretKey": true, "stripeWebhookSecret": true,
		"stripeSuccessURL": true, "stripeCancelURL": true, "publicBaseURL": true,
		"rechargeQueuedTimeoutSeconds": true, "rechargeTaskLeaseTimeoutSeconds": true,
		"emailEnabled": true, "emailNotifyPurchase": true, "emailNotifyRedeem": true, "emailSiteName": true,
		"emailSMTPHost": true, "emailSMTPPort": true, "emailSMTPUsername": true, "emailSMTPPassword": true,
		"emailSMTPFrom": true, "emailSMTPFromName": true, "emailSMTPUseTLS": true, "emailSMTPTimeoutSeconds": true,
		"cardPoolDefaultID": true, "cardPoolRouting": true, "cardPoolDefaultProvider": true, "cardPoolCardCreationMode": true,
		"cardPoolCancelAfterPayment": true,
		"localTextEnabled":           true,
		"airwallexEnabled":           true, "airwallexBaseURL": true, "airwallexClientID": true, "airwallexAPIKey": true,
		"airwallexPrimaryCurrency": true, "airwallexCardPurpose": true, "airwallexCardType": true,
		"airwallexFormFactor": true, "airwallexActivateOnIssue": true, "airwallexCreatedBy": true,
		"airwallexCardholderID": true, "airwallexWebhookSecret": true, "airwallexWebhookToleranceSeconds": true,
		"stripeIssuingEnabled": true, "stripeIssuingBaseURL": true, "stripeIssuingSecretKey": true,
		"stripeIssuingCardholderID": true, "stripeIssuingCurrency": true, "stripeIssuingWebhookSecret": true,
		"stripeIssuingWebhookToleranceSeconds": true,
		"photonpayEnabled":                     true, "photonpayBaseURL": true, "photonpayAppID": true, "photonpayAppSecret": true,
		"photonpayPrivateKey": true, "photonpayWebhookPublicKey": true, "photonpayCardBIN": true,
		"photonpayCardType": true, "photonpayCardFormFactor": true, "photonpayCardScheme": true,
		"photonpayCardholderID": true, "photonpayAccountID": true, "photonpayMemberID": true,
		"photonpayMatrixAccount": true, "photonpayNickname": true, "photonpayPrimaryCurrency": true,
		"photonpayTransactionLimitType": true, "photonpayWebhookToleranceSeconds": true,
		"dogpayEnabled": true, "dogpayBaseURL": true, "dogpayAppID": true, "dogpaySecret": true,
		"dogpayPrivateKey": true, "dogpayWebhookSecret": true, "dogpayChannelID": true, "dogpayCardholderID": true,
		"dogpayEntityID": true, "dogpayCardType": true, "dogpayBudgetID": true, "dogpayVelocityAmountLimit": true, "dogpayWebhookToleranceSeconds": true,
		"kimooxEnabled": true, "kimooxBaseURL": true, "kimooxAPIKey": true, "kimooxAPISecret": true,
		"kimooxWebhookSecret": true, "kimooxCardBINIDs": true, "kimooxCardType": true, "kimooxPrepaidRechargeAmount": true,
		"kimooxCardholderID": true, "kimooxHolderID": true, "kimooxCardGroupID": true, "kimooxBudgetID": true,
		"kimooxWebhookToleranceSeconds": true, "kimooxApplyPollAttempts": true, "kimooxApplyPollIntervalSeconds": true,
	}
	for _, key := range []string{
		"max_concurrent_activations", "max_background_concurrent", "maintenance_mode", "maintenance_mode_drain",
		"recharge_queued_timeout_seconds", "recharge_task_lease_timeout_seconds",
		"email_enabled", "email_notify_purchase", "email_notify_redeem", "email_smtp_host", "email_smtp_port",
		"email_smtp_username", "email_smtp_password", "email_smtp_from", "email_smtp_from_name", "email_smtp_use_tls",
		"email_smtp_timeout_seconds", "email_site_name", "card_pool_default_id", "card_pool_routing", "card_pool_default_provider",
		"card_provider_local_text_enabled", "card_provider_airwallex_enabled", "card_provider_stripe_issuing_enabled",
		"card_provider_photonpay_enabled", "card_provider_dogpay_enabled",
		"card_provider_kimoox_enabled", "card_pool_card_creation_mode", "card_pool_cancel_after_payment",
		"airwallex_base_url", "airwallex_client_id", "airwallex_api_key", "airwallex_primary_currency",
		"airwallex_card_purpose", "airwallex_card_type", "airwallex_form_factor", "airwallex_activate_on_issue",
		"airwallex_created_by", "airwallex_cardholder_id", "airwallex_webhook_secret", "airwallex_webhook_tolerance_seconds",
		"stripe_issuing_base_url", "stripe_issuing_secret_key", "stripe_issuing_cardholder_id", "stripe_issuing_currency",
		"stripe_issuing_webhook_secret", "stripe_issuing_webhook_tolerance_seconds",
		"photonpay_base_url", "photonpay_app_id", "photonpay_app_secret", "photonpay_private_key", "photonpay_webhook_public_key",
		"photonpay_card_bin", "photonpay_card_type", "photonpay_card_form_factor", "photonpay_card_scheme",
		"photonpay_cardholder_id", "photonpay_account_id", "photonpay_member_id", "photonpay_matrix_account", "photonpay_nickname",
		"photonpay_primary_currency", "photonpay_transaction_limit_type", "photonpay_webhook_tolerance_seconds",
		"dogpay_base_url", "dogpay_appid", "dogpay_secret", "dogpay_private_key", "dogpay_webhook_secret", "dogpay_channel_id",
		"dogpay_cardholder_id", "dogpay_entity_id", "dogpay_card_type", "dogpay_budget_id", "dogpay_velocity_amount_limit", "dogpay_webhook_tolerance_seconds",
		"kimoox_base_url", "kimoox_api_key", "kimoox_api_secret", "kimoox_webhook_secret", "kimoox_card_bin_ids", "kimoox_card_type",
		"kimoox_prepaid_recharge_amount",
		"kimoox_cardholder_id", "kimoox_holder_id", "kimoox_card_group_id", "kimoox_budget_id", "kimoox_webhook_tolerance_seconds",
		"kimoox_apply_poll_attempts", "kimoox_apply_poll_interval_seconds",
		"stripe_secret_key", "stripe_webhook_secret", "store_debug_mode", "stripe_success_url", "stripe_cancel_url", "public_base_url",
		"mode", "upstreamBaseURL", "upstreamCreatePath", "upstreamStatusPath",
	} {
		allowed[key] = true
	}

	canonical := map[string]string{
		"storeDebugMode": "store_debug_mode", "stripeSecretKey": "stripe_secret_key", "stripeWebhookSecret": "stripe_webhook_secret",
		"stripeSuccessURL": "stripe_success_url", "stripeCancelURL": "stripe_cancel_url", "publicBaseURL": "public_base_url",
		"rechargeQueuedTimeoutSeconds": "recharge_queued_timeout_seconds", "rechargeTaskLeaseTimeoutSeconds": "recharge_task_lease_timeout_seconds",
		"emailEnabled": "email_enabled", "emailNotifyPurchase": "email_notify_purchase", "emailNotifyRedeem": "email_notify_redeem",
		"emailSiteName": "email_site_name", "emailSMTPHost": "email_smtp_host", "emailSMTPPort": "email_smtp_port",
		"emailSMTPUsername": "email_smtp_username", "emailSMTPPassword": "email_smtp_password", "emailSMTPFrom": "email_smtp_from",
		"emailSMTPFromName": "email_smtp_from_name", "emailSMTPUseTLS": "email_smtp_use_tls", "emailSMTPTimeoutSeconds": "email_smtp_timeout_seconds",
		"cardPoolDefaultID": "card_pool_default_id", "cardPoolRouting": "card_pool_routing", "cardPoolDefaultProvider": "card_pool_default_provider", "cardPoolCardCreationMode": "card_pool_card_creation_mode",
		"cardPoolCancelAfterPayment": "card_pool_cancel_after_payment",
		"localTextEnabled":           "card_provider_local_text_enabled", "airwallexEnabled": "card_provider_airwallex_enabled",
		"airwallexBaseURL": "airwallex_base_url", "airwallexClientID": "airwallex_client_id", "airwallexAPIKey": "airwallex_api_key",
		"airwallexPrimaryCurrency": "airwallex_primary_currency", "airwallexCardPurpose": "airwallex_card_purpose",
		"airwallexCardType": "airwallex_card_type", "airwallexFormFactor": "airwallex_form_factor", "airwallexActivateOnIssue": "airwallex_activate_on_issue",
		"airwallexCreatedBy": "airwallex_created_by", "airwallexCardholderID": "airwallex_cardholder_id", "airwallexWebhookSecret": "airwallex_webhook_secret",
		"airwallexWebhookToleranceSeconds": "airwallex_webhook_tolerance_seconds", "stripeIssuingEnabled": "card_provider_stripe_issuing_enabled",
		"stripeIssuingBaseURL": "stripe_issuing_base_url", "stripeIssuingSecretKey": "stripe_issuing_secret_key",
		"stripeIssuingCardholderID": "stripe_issuing_cardholder_id", "stripeIssuingCurrency": "stripe_issuing_currency",
		"stripeIssuingWebhookSecret": "stripe_issuing_webhook_secret", "stripeIssuingWebhookToleranceSeconds": "stripe_issuing_webhook_tolerance_seconds",
		"photonpayEnabled": "card_provider_photonpay_enabled", "photonpayBaseURL": "photonpay_base_url", "photonpayAppID": "photonpay_app_id",
		"photonpayAppSecret": "photonpay_app_secret", "photonpayPrivateKey": "photonpay_private_key", "photonpayWebhookPublicKey": "photonpay_webhook_public_key",
		"photonpayCardBIN": "photonpay_card_bin", "photonpayCardType": "photonpay_card_type", "photonpayCardFormFactor": "photonpay_card_form_factor",
		"photonpayCardScheme": "photonpay_card_scheme", "photonpayCardholderID": "photonpay_cardholder_id", "photonpayAccountID": "photonpay_account_id",
		"photonpayMemberID": "photonpay_member_id", "photonpayMatrixAccount": "photonpay_matrix_account", "photonpayNickname": "photonpay_nickname",
		"photonpayPrimaryCurrency": "photonpay_primary_currency", "photonpayTransactionLimitType": "photonpay_transaction_limit_type",
		"photonpayWebhookToleranceSeconds": "photonpay_webhook_tolerance_seconds", "dogpayEnabled": "card_provider_dogpay_enabled",
		"dogpayBaseURL": "dogpay_base_url", "dogpayAppID": "dogpay_appid", "dogpaySecret": "dogpay_secret", "dogpayPrivateKey": "dogpay_private_key",
		"dogpayWebhookSecret": "dogpay_webhook_secret", "dogpayChannelID": "dogpay_channel_id", "dogpayCardholderID": "dogpay_cardholder_id",
		"dogpayEntityID": "dogpay_entity_id", "dogpayCardType": "dogpay_card_type", "dogpayBudgetID": "dogpay_budget_id",
		"dogpayVelocityAmountLimit": "dogpay_velocity_amount_limit", "dogpayWebhookToleranceSeconds": "dogpay_webhook_tolerance_seconds",
		"kimooxEnabled": "card_provider_kimoox_enabled", "kimooxBaseURL": "kimoox_base_url", "kimooxAPIKey": "kimoox_api_key",
		"kimooxAPISecret": "kimoox_api_secret", "kimooxWebhookSecret": "kimoox_webhook_secret", "kimooxCardBINIDs": "kimoox_card_bin_ids",
		"kimooxCardType": "kimoox_card_type", "kimooxPrepaidRechargeAmount": "kimoox_prepaid_recharge_amount",
		"kimooxCardholderID": "kimoox_cardholder_id", "kimooxHolderID": "kimoox_holder_id",
		"kimooxCardGroupID": "kimoox_card_group_id", "kimooxBudgetID": "kimoox_budget_id",
		"kimooxWebhookToleranceSeconds": "kimoox_webhook_tolerance_seconds", "kimooxApplyPollAttempts": "kimoox_apply_poll_attempts",
		"kimooxApplyPollIntervalSeconds": "kimoox_apply_poll_interval_seconds",
	}

	values := make(map[string]string, len(input))
	for key, raw := range input {
		if !allowed[key] {
			continue
		}
		storageKey := firstNonEmpty(canonical[key], key)
		value := strings.TrimSpace(raw)
		if isSensitiveConfigKey(storageKey) {
			if value == "" || value == "********" {
				continue
			}
			values[storageKey] = value
			continue
		}
		normalized, err := normalizeModernConfigValue(storageKey, value)
		if err != nil {
			return nil, err
		}
		values[storageKey] = normalized
	}
	return values, nil
}

func normalizeModernConfigValue(key, value string) (string, error) {
	switch key {
	case "email_enabled", "email_notify_purchase", "email_notify_redeem", "email_smtp_use_tls", "maintenance_mode", "maintenance_mode_drain",
		"card_provider_local_text_enabled", "card_provider_airwallex_enabled", "card_provider_stripe_issuing_enabled",
		"card_provider_photonpay_enabled", "card_provider_dogpay_enabled", "card_provider_kimoox_enabled", "airwallex_activate_on_issue",
		"card_pool_cancel_after_payment":
		if value != "0" && value != "1" && !strings.EqualFold(value, "true") && !strings.EqualFold(value, "false") {
			return "", fmt.Errorf("%s 必须是布尔值", key)
		}
		return boolConfigValue(value), nil
	case "email_smtp_port":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", fmt.Errorf("email_smtp_port 必须是 1-65535 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "email_smtp_timeout_seconds":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 300 {
			return "", fmt.Errorf("email_smtp_timeout_seconds 必须是 1-300 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "recharge_queued_timeout_seconds":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 86400 {
			return "", fmt.Errorf("recharge_queued_timeout_seconds 必须是 1-86400 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "recharge_task_lease_timeout_seconds":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 5 || parsed > 3600 {
			return "", fmt.Errorf("recharge_task_lease_timeout_seconds 必须是 5-3600 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "airwallex_webhook_tolerance_seconds", "stripe_issuing_webhook_tolerance_seconds", "photonpay_webhook_tolerance_seconds", "dogpay_webhook_tolerance_seconds", "kimoox_webhook_tolerance_seconds":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 86400 {
			return "", fmt.Errorf("%s 必须是 1-86400 的整数", key)
		}
		return strconv.Itoa(parsed), nil
	case "dogpay_velocity_amount_limit":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 {
			return "", fmt.Errorf("dogpay_velocity_amount_limit 必须是大于等于 0 的数字")
		}
		return strconv.FormatFloat(parsed, 'f', -1, 64), nil
	case "kimoox_apply_poll_attempts":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 300 {
			return "", fmt.Errorf("kimoox_apply_poll_attempts 必须是 1-300 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "kimoox_apply_poll_interval_seconds":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 300 {
			return "", fmt.Errorf("kimoox_apply_poll_interval_seconds 必须是 0-300 的整数")
		}
		return strconv.Itoa(parsed), nil
	case "card_pool_routing":
		value = strings.ToUpper(value)
		if value != "FIXED" && value != "PRIORITY" && value != "WEIGHTED" && value != "FAILOVER" {
			return "", fmt.Errorf("card_pool_routing 仅支持 FIXED、PRIORITY、WEIGHTED、FAILOVER")
		}
		return value, nil
	case "card_pool_card_creation_mode":
		value = strings.ToUpper(value)
		if value != "POOL_ONLY" && value != "CREATE_ON_DEMAND" {
			return "", fmt.Errorf("card_pool_card_creation_mode 仅支持 POOL_ONLY、CREATE_ON_DEMAND")
		}
		return value, nil
	case "card_pool_default_provider":
		value = strings.ToUpper(value)
		if _, ok := cardProviderEnabledConfigKeys[value]; !ok {
			return "", fmt.Errorf("card_pool_default_provider 仅支持 LOCAL_TEXT、AIRWALLEX、STRIPE_ISSUING、PHOTONPAY、DOGPAY、KIMOOX")
		}
		return value, nil
	case "kimoox_card_type":
		value = strings.ToUpper(value)
		if value != "PREPAID" && value != "BUDGET" {
			return "", fmt.Errorf("kimoox_card_type 仅支持 PREPAID、BUDGET")
		}
		return value, nil
	case "kimoox_prepaid_recharge_amount":
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || parsed <= 0 || parsed > 100000 {
			return "", fmt.Errorf("kimoox_prepaid_recharge_amount 必须是大于 0 且不超过 100000 的金额")
		}
		return strconv.FormatFloat(parsed, 'f', -1, 64), nil
	case "airwallex_form_factor":
		value = strings.ToUpper(value)
		if value != "VIRTUAL" && value != "PHYSICAL" {
			return "", fmt.Errorf("airwallex_form_factor 仅支持 VIRTUAL、PHYSICAL")
		}
		return value, nil
	case "photonpay_card_form_factor":
		value = strings.ToLower(value)
		if value != "virtual_card" && value != "physical_card" {
			return "", fmt.Errorf("photonpay_card_form_factor 仅支持 virtual_card、physical_card")
		}
		return value, nil
	case "photonpay_transaction_limit_type":
		value = strings.ToLower(value)
		if value != "limited" && value != "unlimited" {
			return "", fmt.Errorf("photonpay_transaction_limit_type 仅支持 limited、unlimited")
		}
		return value, nil
	case "airwallex_primary_currency", "stripe_issuing_currency", "photonpay_primary_currency":
		value = strings.ToUpper(value)
		if len(value) != 3 {
			return "", fmt.Errorf("%s 必须是 3 位币种代码", key)
		}
		return value, nil
	case "mode":
		value = normalizeMode(value, "browser")
		if value == "protocol" {
			value = "browser"
		}
		if value != "browser" && value != "dry_run" && value != "upstream" {
			return "", fmt.Errorf("mode 仅支持 browser、dry_run、upstream")
		}
		return value, nil
	default:
		return value, nil
	}
}
