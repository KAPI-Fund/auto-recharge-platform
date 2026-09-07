package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
)

var cardProviderConfigKeys = []string{
	"card_pool_default_provider",
	"card_provider_local_text_enabled",
	"card_provider_airwallex_enabled",
	"card_provider_stripe_issuing_enabled",
	"card_provider_photonpay_enabled",
	"card_provider_dogpay_enabled",
	"card_provider_kimoox_enabled",
}

var cardProviderEnabledConfigKeys = map[string]string{
	"LOCAL_TEXT":     "card_provider_local_text_enabled",
	"AIRWALLEX":      "card_provider_airwallex_enabled",
	"STRIPE_ISSUING": "card_provider_stripe_issuing_enabled",
	"PHOTONPAY":      "card_provider_photonpay_enabled",
	"DOGPAY":         "card_provider_dogpay_enabled",
	"KIMOOX":         "card_provider_kimoox_enabled",
}

// cardProviderValidationKeys contains every setting that can change provider
// readiness or routing. Keeping this list centralized makes the legacy and
// React admin endpoints validate the same post-save state.
var cardProviderValidationKeys = []string{
	"card_pool_default_id",
	"card_pool_routing",
	"card_pool_default_provider",
	"card_pool_card_creation_mode",
	"card_pool_cancel_after_payment",
	"card_provider_local_text_enabled",
	"card_provider_airwallex_enabled",
	"card_provider_stripe_issuing_enabled",
	"card_provider_photonpay_enabled",
	"card_provider_dogpay_enabled",
	"card_provider_kimoox_enabled",
	"airwallex_base_url",
	"airwallex_client_id",
	"airwallex_api_key",
	"airwallex_cardholder_id",
	"airwallex_primary_currency",
	"airwallex_card_type",
	"airwallex_card_purpose",
	"airwallex_created_by",
	"airwallex_form_factor",
	"airwallex_webhook_tolerance_seconds",
	"airwallex_activate_on_issue",
	"airwallex_webhook_secret",
	"stripe_issuing_base_url",
	"stripe_issuing_secret_key",
	"stripe_issuing_cardholder_id",
	"stripe_issuing_currency",
	"stripe_issuing_webhook_secret",
	"stripe_issuing_webhook_tolerance_seconds",
	"photonpay_base_url",
	"photonpay_app_id",
	"photonpay_app_secret",
	"photonpay_private_key",
	"photonpay_webhook_public_key",
	"photonpay_card_bin",
	"photonpay_card_type",
	"photonpay_card_form_factor",
	"photonpay_card_scheme",
	"photonpay_cardholder_id",
	"photonpay_account_id",
	"photonpay_member_id",
	"photonpay_matrix_account",
	"photonpay_nickname",
	"photonpay_primary_currency",
	"photonpay_transaction_limit_type",
	"photonpay_webhook_tolerance_seconds",
	"dogpay_base_url",
	"dogpay_appid",
	"dogpay_secret",
	"dogpay_private_key",
	"dogpay_webhook_secret",
	"dogpay_channel_id",
	"dogpay_cardholder_id",
	"dogpay_entity_id",
	"dogpay_card_type",
	"dogpay_budget_id",
	"dogpay_velocity_amount_limit",
	"dogpay_webhook_tolerance_seconds",
	"kimoox_base_url",
	"kimoox_api_key",
	"kimoox_api_secret",
	"kimoox_webhook_secret",
	"kimoox_card_bin_ids",
	"kimoox_card_type",
	"kimoox_prepaid_amount_mode",
	"kimoox_prepaid_recharge_amount",
	"kimoox_cardholder_id",
	"kimoox_holder_id",
	"kimoox_card_group_id",
	"kimoox_budget_id",
	"kimoox_webhook_tolerance_seconds",
	"kimoox_apply_poll_attempts",
	"kimoox_apply_poll_interval_seconds",
}

var cardProviderValidationAliases = map[string]string{
	"card_pool_default_id":                     "cardPoolDefaultID",
	"card_pool_routing":                        "cardPoolRouting",
	"card_pool_default_provider":               "cardPoolDefaultProvider",
	"card_pool_card_creation_mode":             "cardPoolCardCreationMode",
	"card_pool_cancel_after_payment":           "cardPoolCancelAfterPayment",
	"card_provider_local_text_enabled":         "localTextEnabled",
	"card_provider_airwallex_enabled":          "airwallexEnabled",
	"card_provider_stripe_issuing_enabled":     "stripeIssuingEnabled",
	"card_provider_photonpay_enabled":          "photonpayEnabled",
	"card_provider_dogpay_enabled":             "dogpayEnabled",
	"card_provider_kimoox_enabled":             "kimooxEnabled",
	"airwallex_base_url":                       "airwallexBaseURL",
	"airwallex_client_id":                      "airwallexClientID",
	"airwallex_api_key":                        "airwallexAPIKey",
	"airwallex_cardholder_id":                  "airwallexCardholderID",
	"airwallex_primary_currency":               "airwallexPrimaryCurrency",
	"airwallex_card_type":                      "airwallexCardType",
	"airwallex_card_purpose":                   "airwallexCardPurpose",
	"airwallex_created_by":                     "airwallexCreatedBy",
	"airwallex_form_factor":                    "airwallexFormFactor",
	"airwallex_webhook_tolerance_seconds":      "airwallexWebhookToleranceSeconds",
	"airwallex_activate_on_issue":              "airwallexActivateOnIssue",
	"airwallex_webhook_secret":                 "airwallexWebhookSecret",
	"stripe_issuing_base_url":                  "stripeIssuingBaseURL",
	"stripe_issuing_secret_key":                "stripeIssuingSecretKey",
	"stripe_issuing_cardholder_id":             "stripeIssuingCardholderID",
	"stripe_issuing_currency":                  "stripeIssuingCurrency",
	"stripe_issuing_webhook_secret":            "stripeIssuingWebhookSecret",
	"stripe_issuing_webhook_tolerance_seconds": "stripeIssuingWebhookToleranceSeconds",
	"photonpay_base_url":                       "photonpayBaseURL",
	"photonpay_app_id":                         "photonpayAppID",
	"photonpay_app_secret":                     "photonpayAppSecret",
	"photonpay_private_key":                    "photonpayPrivateKey",
	"photonpay_webhook_public_key":             "photonpayWebhookPublicKey",
	"photonpay_card_bin":                       "photonpayCardBIN",
	"photonpay_card_type":                      "photonpayCardType",
	"photonpay_card_form_factor":               "photonpayCardFormFactor",
	"photonpay_card_scheme":                    "photonpayCardScheme",
	"photonpay_cardholder_id":                  "photonpayCardholderID",
	"photonpay_account_id":                     "photonpayAccountID",
	"photonpay_member_id":                      "photonpayMemberID",
	"photonpay_matrix_account":                 "photonpayMatrixAccount",
	"photonpay_nickname":                       "photonpayNickname",
	"photonpay_primary_currency":               "photonpayPrimaryCurrency",
	"photonpay_transaction_limit_type":         "photonpayTransactionLimitType",
	"photonpay_webhook_tolerance_seconds":      "photonpayWebhookToleranceSeconds",
	"dogpay_base_url":                          "dogpayBaseURL",
	"dogpay_appid":                             "dogpayAppID",
	"dogpay_secret":                            "dogpaySecret",
	"dogpay_private_key":                       "dogpayPrivateKey",
	"dogpay_webhook_secret":                    "dogpayWebhookSecret",
	"dogpay_channel_id":                        "dogpayChannelID",
	"dogpay_cardholder_id":                     "dogpayCardholderID",
	"dogpay_entity_id":                         "dogpayEntityID",
	"dogpay_card_type":                         "dogpayCardType",
	"dogpay_budget_id":                         "dogpayBudgetID",
	"dogpay_velocity_amount_limit":             "dogpayVelocityAmountLimit",
	"dogpay_webhook_tolerance_seconds":         "dogpayWebhookToleranceSeconds",
	"kimoox_base_url":                          "kimooxBaseURL",
	"kimoox_api_key":                           "kimooxAPIKey",
	"kimoox_api_secret":                        "kimooxAPISecret",
	"kimoox_webhook_secret":                    "kimooxWebhookSecret",
	"kimoox_card_bin_ids":                      "kimooxCardBINIDs",
	"kimoox_card_type":                         "kimooxCardType",
	"kimoox_prepaid_amount_mode":               "kimooxPrepaidAmountMode",
	"kimoox_prepaid_recharge_amount":           "kimooxPrepaidRechargeAmount",
	"kimoox_cardholder_id":                     "kimooxCardholderID",
	"kimoox_holder_id":                         "kimooxHolderID",
	"kimoox_card_group_id":                     "kimooxCardGroupID",
	"kimoox_budget_id":                         "kimooxBudgetID",
	"kimoox_webhook_tolerance_seconds":         "kimooxWebhookToleranceSeconds",
	"kimoox_apply_poll_attempts":               "kimooxApplyPollAttempts",
	"kimoox_apply_poll_interval_seconds":       "kimooxApplyPollIntervalSeconds",
}

// cardProviderOverridesFromAny extracts only the relational settings from the
// legacy JSON payload. An unrelated section save must not need a config read.
func cardProviderOverridesFromAny(input map[string]any) map[string]string {
	overrides := make(map[string]string, len(cardProviderValidationKeys))
	for _, key := range cardProviderValidationKeys {
		value, exists := input[key]
		if !exists {
			if alias := cardProviderValidationAliases[key]; alias != "" {
				value, exists = input[alias]
			}
		}
		if !exists {
			continue
		}
		text := strings.TrimSpace(toString(value))
		if isSensitiveConfigKey(key) && (text == "" || text == "********") {
			continue
		}
		overrides[key] = text
	}
	return overrides
}

// cardProviderOverridesFromStrings accepts the camelCase payload used by the
// v1 admin endpoint and also accepts canonical snake_case names for callers
// that already normalized the request.
func cardProviderOverridesFromStrings(input map[string]string) map[string]string {
	overrides := make(map[string]string, len(cardProviderValidationKeys))
	for _, key := range cardProviderValidationKeys {
		if value, exists := input[key]; exists {
			value = strings.TrimSpace(value)
			if isSensitiveConfigKey(key) && (value == "" || value == "********") {
				continue
			}
			overrides[key] = value
			continue
		}
		if alias := cardProviderValidationAliases[key]; alias != "" {
			if value, exists := input[alias]; exists {
				value = strings.TrimSpace(value)
				if isSensitiveConfigKey(key) && (value == "" || value == "********") {
					continue
				}
				overrides[key] = value
			}
		}
	}
	return overrides
}

// validateCardProviderSettings validates the effective state after applying
// the request over the persisted values. This keeps the default Provider from
// becoming disabled when the browser or another client calls the API directly.
func (s *Server) validateCardProviderSettings(overrides map[string]string) error {
	if len(overrides) == 0 {
		return nil
	}

	baseValues := map[string]string{
		"card_pool_default_provider":           "LOCAL_TEXT",
		"card_provider_local_text_enabled":     "1",
		"card_provider_airwallex_enabled":      "0",
		"card_provider_stripe_issuing_enabled": "0",
		"card_provider_photonpay_enabled":      "0",
		"card_provider_dogpay_enabled":         "0",
		"card_provider_kimoox_enabled":         "0",
	}
	if s != nil && s.DB != nil {
		var values []models.AppConfig
		if err := s.DB.Find(&values).Error; err != nil {
			return fmt.Errorf("读取银行卡 Provider 配置失败: %w", err)
		}
		for _, value := range values {
			stored := strings.TrimSpace(value.Value)
			if value.IsSecret {
				if key := strings.TrimSpace(s.Cfg.SessionEncryptionKey); key != "" {
					if decrypted, err := security.DecryptWithFallbacks(stored, s.Cfg.SessionEncryptionKeys()...); err == nil {
						stored = strings.TrimSpace(decrypted)
					} else {
						stored = ""
					}
				}
			}
			baseValues[value.Key] = stored
		}
	}

	reader := cardpool.NewOverlayConfigReader(cardpool.NewMapConfigReader(baseValues), overrides)
	provider := strings.ToUpper(strings.TrimSpace(reader.Value("card_pool_default_provider", "LOCAL_TEXT")))
	if _, supported := cardProviderEnabledConfigKeys[provider]; !supported {
		return errInvalid("card_pool_default_provider 仅支持 LOCAL_TEXT、AIRWALLEX、STRIPE_ISSUING、PHOTONPAY、DOGPAY、KIMOOX")
	}
	if normalizeCardProviderEnabled(reader.Value(cardProviderEnabledConfigKeys[provider], "0")) != "1" {
		return errInvalid("默认 Provider 必须处于启用状态")
	}
	creationMode := strings.ToUpper(strings.TrimSpace(reader.Value("card_pool_card_creation_mode", string(cardpool.CardCreationOnDemand))))
	if creationMode != string(cardpool.CardCreationPoolOnly) && creationMode != string(cardpool.CardCreationOnDemand) {
		return errInvalid("card_pool_card_creation_mode 仅支持 POOL_ONLY、CREATE_ON_DEMAND")
	}
	if service := newCardPoolServiceWithReader(s.DB, s.Cfg, reader); service != nil {
		if err := service.ValidateConfiguration(); err != nil {
			return err
		}
	}
	return nil
}

func normalizeCardProviderEnabled(value string) string {
	value = strings.TrimSpace(value)
	if value == "1" || strings.EqualFold(value, "true") {
		return "1"
	}
	return "0"
}

func cardProviderValidationStatus(err error) int {
	var invalid *storeError
	if errors.As(err, &invalid) {
		return http.StatusBadRequest
	}
	var providerErr *cardpool.ProviderError
	if errors.As(err, &providerErr) && providerErr != nil && providerErr.Category == cardpool.CategoryInvalidRequest {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func writeCardProviderValidationError(c *gin.Context, err error) {
	if cardProviderValidationStatus(err) == http.StatusBadRequest {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	fail(c, http.StatusInternalServerError, "读取银行卡 Provider 配置失败")
}
