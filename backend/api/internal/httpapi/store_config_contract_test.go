package httpapi

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
)

func TestLegacyStoreStripeConfigOnlyWritesNewSecrets(t *testing.T) {
	values := legacyStoreStripeConfig(map[string]any{
		"stripe_secret_key":     "  sk_test_platform  ",
		"stripe_webhook_secret": "whsec_platform",
		"stripe_success_url":    "https://example.test/success",
	})

	if values["stripe_secret_key"] != "sk_test_platform" || values["stripe_webhook_secret"] != "whsec_platform" {
		t.Fatalf("unexpected secret values: %#v", values)
	}
	if len(values) != 2 {
		t.Fatalf("unexpected fields written: %#v", values)
	}
}

func TestLegacyStoreStripeConfigKeepsExistingSecretsForBlankOrMaskedInput(t *testing.T) {
	values := legacyStoreStripeConfig(map[string]any{
		"stripe_secret_key":     "",
		"stripe_webhook_secret": "********",
	})
	if len(values) != 0 {
		t.Fatalf("blank or masked secrets should not be written: %#v", values)
	}
}

func TestNormalizeLegacyConfigInputAcceptsReactFieldNames(t *testing.T) {
	input := normalizeLegacyConfigInput(map[string]any{
		"store_debug_mode":    false,
		"storeDebugMode":      true,
		"stripe_success_url":  "https://example.test/stale-success",
		"stripeSecretKey":     "sk_test_platform",
		"stripeWebhookSecret": "whsec_platform",
		"stripeSuccessURL":    "https://example.test/success",
		"stripeCancelURL":     "https://example.test/cancel",
		"publicBaseURL":       "https://example.test",
	})
	for key, want := range map[string]any{
		"store_debug_mode":      true,
		"stripe_secret_key":     "sk_test_platform",
		"stripe_webhook_secret": "whsec_platform",
		"stripe_success_url":    "https://example.test/success",
		"stripe_cancel_url":     "https://example.test/cancel",
		"public_base_url":       "https://example.test",
	} {
		if input[key] != want {
			t.Fatalf("normalized %s = %#v, want %#v", key, input[key], want)
		}
	}
}

func TestNormalizeLegacyConfigInputAcceptsCardProviderFieldNames(t *testing.T) {
	input := normalizeLegacyConfigInput(map[string]any{
		"cardPoolDefaultID":                    "pool_airwallex",
		"cardPoolRouting":                      "failover",
		"cardPoolDefaultProvider":              "airwallex",
		"localTextEnabled":                     true,
		"airwallexEnabled":                     true,
		"airwallexClientID":                    "client-1",
		"airwallexAPIKey":                      "key-1",
		"airwallexWebhookSecret":               "secret-1",
		"airwallexWebhookToleranceSeconds":     "180",
		"stripeIssuingEnabled":                 false,
		"stripeIssuingSecretKey":               "sk_test_issuing",
		"stripeIssuingWebhookSecret":           "whsec_issuing",
		"stripeIssuingWebhookToleranceSeconds": "240",
	})
	for key, want := range map[string]any{
		"card_pool_default_id":                     "pool_airwallex",
		"card_pool_routing":                        "failover",
		"card_pool_default_provider":               "airwallex",
		"card_provider_local_text_enabled":         true,
		"card_provider_airwallex_enabled":          true,
		"airwallex_client_id":                      "client-1",
		"airwallex_api_key":                        "key-1",
		"airwallex_webhook_secret":                 "secret-1",
		"airwallex_webhook_tolerance_seconds":      "180",
		"card_provider_stripe_issuing_enabled":     false,
		"stripe_issuing_secret_key":                "sk_test_issuing",
		"stripe_issuing_webhook_secret":            "whsec_issuing",
		"stripe_issuing_webhook_tolerance_seconds": "240",
	} {
		if input[key] != want {
			t.Fatalf("normalized %s = %#v, want %#v", key, input[key], want)
		}
	}
}

func TestNormalizeLegacyConfigInputAcceptsPhotonPayAndDogPayFieldNames(t *testing.T) {
	input := normalizeLegacyConfigInput(map[string]any{
		"photonpayEnabled":              true,
		"photonpayBaseURL":              "https://x-api.sandbox.photontech.cc",
		"photonpayCardBIN":              "411111",
		"photonpayCardFormFactor":       "virtual_card",
		"photonpayTransactionLimitType": "unlimited",
		"photonpayAppID":                "photon-app",
		"photonpayAppSecret":            "photon-secret",
		"photonpayWebhookPublicKey":     "photon-public-key",
		"dogpayEnabled":                 true,
		"dogpayBaseURL":                 "https://sandbox-api-v2.dogpay.com",
		"dogpayChannelID":               "channel-1",
		"dogpayCardType":                "virtual",
		"dogpayAppID":                   "dog-app",
		"dogpaySecret":                  "dog-secret",
		"dogpayWebhookSecret":           "dog-webhook-secret",
	})
	for key, want := range map[string]any{
		"card_provider_photonpay_enabled":  true,
		"photonpay_base_url":               "https://x-api.sandbox.photontech.cc",
		"photonpay_card_bin":               "411111",
		"photonpay_card_form_factor":       "virtual_card",
		"photonpay_transaction_limit_type": "unlimited",
		"photonpay_app_id":                 "photon-app",
		"photonpay_app_secret":             "photon-secret",
		"photonpay_webhook_public_key":     "photon-public-key",
		"card_provider_dogpay_enabled":     true,
		"dogpay_base_url":                  "https://sandbox-api-v2.dogpay.com",
		"dogpay_channel_id":                "channel-1",
		"dogpay_card_type":                 "virtual",
		"dogpay_appid":                     "dog-app",
		"dogpay_secret":                    "dog-secret",
		"dogpay_webhook_secret":            "dog-webhook-secret",
	} {
		if input[key] != want {
			t.Fatalf("normalized %s = %#v, want %#v", key, input[key], want)
		}
	}
}

func TestNormalizeLegacyConfigInputAcceptsKimooxFieldNames(t *testing.T) {
	input := normalizeLegacyConfigInput(map[string]any{
		"kimooxEnabled":                  true,
		"kimooxBaseURL":                  "https://card.kimoox.com",
		"kimooxAPIKey":                   "api-key",
		"kimooxAPISecret":                "api-secret",
		"kimooxWebhookSecret":            "webhook-secret",
		"kimooxCardBINIDs":               "1001",
		"kimooxCardType":                 "budget",
		"kimooxCardholderID":             "12",
		"kimooxCardGroupID":              "13",
		"kimooxBudgetID":                 "14",
		"kimooxWebhookToleranceSeconds":  "180",
		"kimooxApplyPollAttempts":        "20",
		"kimooxApplyPollIntervalSeconds": "1",
	})
	for key, want := range map[string]any{
		"card_provider_kimoox_enabled":       true,
		"kimoox_base_url":                    "https://card.kimoox.com",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  "api-secret",
		"kimoox_webhook_secret":              "webhook-secret",
		"kimoox_card_bin_ids":                "1001",
		"kimoox_card_type":                   "budget",
		"kimoox_cardholder_id":               "12",
		"kimoox_card_group_id":               "13",
		"kimoox_budget_id":                   "14",
		"kimoox_webhook_tolerance_seconds":   "180",
		"kimoox_apply_poll_attempts":         "20",
		"kimoox_apply_poll_interval_seconds": "1",
	} {
		if input[key] != want {
			t.Fatalf("normalized %s = %#v, want %#v", key, input[key], want)
		}
	}
}

func TestNormalizeModernConfigInputAcceptsDogPayVelocityLimit(t *testing.T) {
	values, err := normalizeModernConfigValues(map[string]string{
		"dogpayVelocityAmountLimit": "12.50",
	})
	if err != nil {
		t.Fatalf("normalizeModernConfigValues() error = %v", err)
	}
	if values["dogpay_velocity_amount_limit"] != "12.5" {
		t.Fatalf("normalized DogPay velocity limit = %q, want 12.5", values["dogpay_velocity_amount_limit"])
	}

	if _, err := normalizeModernConfigValues(map[string]string{"dogpay_velocity_amount_limit": "-1"}); err == nil {
		t.Fatal("negative DogPay velocity limit should be rejected")
	}
}

func TestNormalizeModernConfigInputAcceptsKimooxMultipleBINs(t *testing.T) {
	values, err := normalizeModernConfigValues(map[string]string{
		"kimooxCardBINIDs": "1001, 1002\n1003",
	})
	if err != nil {
		t.Fatalf("normalizeModernConfigValues() error = %v", err)
	}
	if values["kimoox_card_bin_ids"] != "1001, 1002\n1003" {
		t.Fatalf("normalized Kimoox BINs = %q", values["kimoox_card_bin_ids"])
	}

	legacy := normalizeLegacyConfigInput(map[string]any{
		"kimooxCardBINIDs": `["1001","1002"]`,
	})
	if legacy["kimoox_card_bin_ids"] != `["1001","1002"]` {
		t.Fatalf("legacy normalized Kimoox BINs = %#v", legacy["kimoox_card_bin_ids"])
	}
}

func TestNormalizeConfigInputRejectsLegacyKimooxSingleBINField(t *testing.T) {
	modern, err := normalizeModernConfigValues(map[string]string{"kimooxCardBINID": "1001"})
	if err != nil {
		t.Fatalf("normalizeModernConfigValues() error = %v", err)
	}
	if _, exists := modern["kimoox_card_bin_id"]; exists {
		t.Fatal("legacy Kimoox single BIN field must not be persisted")
	}

	legacy := normalizeLegacyConfigInput(map[string]any{"kimooxCardBINID": "1001"})
	if _, exists := legacy["kimoox_card_bin_id"]; exists {
		t.Fatal("legacy Kimoox single BIN field must not be normalized")
	}
}

func TestLegacyCardProviderSecretsSkipBlankAndMaskedValues(t *testing.T) {
	values := legacyCardProviderSecrets(map[string]any{
		"airwallex_client_id":           "client-1",
		"airwallex_api_key":             "",
		"airwallex_webhook_secret":      "********",
		"stripe_issuing_secret_key":     "sk_test_issuing",
		"stripe_issuing_webhook_secret": " whsec_issuing ",
	})
	if len(values) != 3 || values["airwallex_client_id"] != "client-1" || values["stripe_issuing_secret_key"] != "sk_test_issuing" || values["stripe_issuing_webhook_secret"] != "whsec_issuing" {
		t.Fatalf("provider secret values = %#v", values)
	}
}

func TestLegacyCardProviderSecretsIncludePhotonPayAndDogPay(t *testing.T) {
	values := legacyCardProviderSecrets(map[string]any{
		"photonpay_app_id":             "photon-app",
		"photonpay_app_secret":         "photon-secret",
		"photonpay_private_key":        "photon-private-key",
		"photonpay_webhook_public_key": "photon-public-key",
		"dogpay_appid":                 "dog-app",
		"dogpay_secret":                "dog-secret",
		"dogpay_private_key":           "dog-private-key",
		"dogpay_webhook_secret":        "dog-webhook-secret",
	})
	for key, want := range map[string]string{
		"photonpay_app_id":             "photon-app",
		"photonpay_app_secret":         "photon-secret",
		"photonpay_private_key":        "photon-private-key",
		"photonpay_webhook_public_key": "photon-public-key",
		"dogpay_appid":                 "dog-app",
		"dogpay_secret":                "dog-secret",
		"dogpay_private_key":           "dog-private-key",
		"dogpay_webhook_secret":        "dog-webhook-secret",
	} {
		if values[key] != want {
			t.Fatalf("provider secret %s = %q, want %q", key, values[key], want)
		}
	}
}

func TestLegacyCardProviderSecretsIncludeKimoox(t *testing.T) {
	values := legacyCardProviderSecrets(map[string]any{
		"kimoox_api_key":        "api-key",
		"kimoox_api_secret":     "api-secret",
		"kimoox_webhook_secret": "webhook-secret",
		"kimoox_api_secret_2":   "ignored",
	})
	for key, want := range map[string]string{
		"kimoox_api_key":        "api-key",
		"kimoox_api_secret":     "api-secret",
		"kimoox_webhook_secret": "webhook-secret",
	} {
		if values[key] != want {
			t.Fatalf("provider secret %s = %q, want %q", key, values[key], want)
		}
	}
	if _, ok := values["kimoox_api_secret_2"]; ok {
		t.Fatalf("unexpected unsupported Kimoox secret was persisted: %#v", values)
	}
}

func TestLegacySaveConfigPersistsPhotonPayAndDogPayFields(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.MatchExpectationsInOrder(false)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_photonpay_enabled", "0", false},
		[]driver.Value{"card_provider_dogpay_enabled", "0", false},
	)
	mock.ExpectBegin()
	expectUpsert := func(key, value string) {
		mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
			WithArgs(key, value, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), value).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}

	values := map[string]string{
		"photonpay_base_url":                  "https://photon.test",
		"photonpay_card_bin":                  "411111",
		"photonpay_card_type":                 "recharge",
		"photonpay_card_form_factor":          "virtual_card",
		"photonpay_card_scheme":               "Discover",
		"photonpay_account_id":                "account-1",
		"photonpay_member_id":                 "member-1",
		"photonpay_matrix_account":            "matrix-1",
		"photonpay_nickname":                  "test-card",
		"photonpay_primary_currency":          "USD",
		"photonpay_transaction_limit_type":    "unlimited",
		"photonpay_webhook_tolerance_seconds": "300",
		"dogpay_base_url":                     "https://dogpay.test",
		"dogpay_channel_id":                   "channel-1",
		"dogpay_entity_id":                    "entity-1",
		"dogpay_card_type":                    "virtual",
		"dogpay_budget_id":                    "budget-1",
		"dogpay_webhook_tolerance_seconds":    "300",
		"photonpay_app_id":                    "photon-app",
		"photonpay_app_secret":                "photon-secret",
		"photonpay_cardholder_id":             "photon-holder",
		"photonpay_private_key":               "photon-private",
		"photonpay_webhook_public_key":        "photon-public",
		"dogpay_appid":                        "dog-app",
		"dogpay_secret":                       "dog-secret",
		"dogpay_cardholder_id":                "dog-holder",
		"dogpay_private_key":                  "dog-private",
		"dogpay_webhook_secret":               "dog-webhook",
	}
	for key, value := range values {
		expectUpsert(key, value)
	}
	mock.ExpectCommit()

	input := map[string]any{
		"photonpayBaseURL": "https://photon.test", "photonpayCardBIN": "411111", "photonpayCardType": "recharge",
		"photonpayCardFormFactor": "virtual_card", "photonpayCardScheme": "Discover", "photonpayAccountID": "account-1",
		"photonpayMemberID": "member-1", "photonpayMatrixAccount": "matrix-1", "photonpayNickname": "test-card",
		"photonpayPrimaryCurrency": "USD", "photonpayTransactionLimitType": "unlimited", "photonpayWebhookToleranceSeconds": "300",
		"dogpayBaseURL": "https://dogpay.test", "dogpayChannelID": "channel-1", "dogpayEntityID": "entity-1", "dogpayCardType": "virtual",
		"dogpayBudgetID": "budget-1", "dogpayWebhookToleranceSeconds": "300", "photonpayAppID": "photon-app",
		"photonpayAppSecret": "photon-secret", "photonpayCardholderID": "photon-holder", "photonpayPrivateKey": "photon-private",
		"photonpayWebhookPublicKey": "photon-public", "dogpayAppID": "dog-app", "dogpaySecret": "dog-secret",
		"dogpayCardholderID": "dog-holder", "dogpayPrivateKey": "dog-private", "dogpayWebhookSecret": "dog-webhook",
	}

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	recorder := httptest.NewRecorder()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestSensitiveConfigKeysIncludeProviderClientID(t *testing.T) {
	if !isSensitiveConfigKey("airwallex_client_id") {
		t.Fatal("Airwallex client ID must be treated as a secret")
	}
	if !isSensitiveConfigKey("stripe_issuing_secret_key") {
		t.Fatal("Stripe Issuing secret key must be treated as a secret")
	}
}

type encryptedConfigValue struct {
	secret    string
	plaintext string
}

func (value encryptedConfigValue) Match(raw driver.Value) bool {
	var encoded string
	switch item := raw.(type) {
	case string:
		encoded = item
	case []byte:
		encoded = string(item)
	default:
		return false
	}
	if encoded == "" || encoded == value.plaintext {
		return false
	}
	plaintext, err := security.Decrypt(encoded, value.secret)
	return err == nil && plaintext == value.plaintext
}

func TestKimooxSecretIsEncryptedBeforePersistence(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	const encryptionKey = "kimoox-config-test-key"
	server := &Server{DB: database, Cfg: config.Config{SessionEncryptionKey: encryptionKey}}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "app_configs"`)+`.*`).
		WithArgs(
			"kimoox_api_secret",
			encryptedConfigValue{secret: encryptionKey, plaintext: "api-secret"},
			true, sqlmock.AnyArg(), sqlmock.AnyArg(),
			true, sqlmock.AnyArg(),
			encryptedConfigValue{secret: encryptionKey, plaintext: "api-secret"},
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := server.upsertConfig("kimoox_api_secret", "api-secret"); err != nil {
		t.Fatalf("upsertConfig() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestPublicConfigValuesNeverReturnsSensitiveKeys(t *testing.T) {
	values := publicConfigValues([]models.AppConfig{
		{Key: "stripe_secret_key", Value: "sk_test_secret"},
		{Key: "stripe_webhook_secret", Value: "whsec_secret"},
		{Key: "airwallex_client_id", Value: "aw_client_secret"},
		{Key: "public_base_url", Value: "https://example.test"},
		{Key: "store_debug_mode", Value: "1"},
	})

	if _, ok := values["stripe_secret_key"]; ok {
		t.Fatal("Stripe secret key was returned by public config projection")
	}
	if _, ok := values["stripe_webhook_secret"]; ok {
		t.Fatal("Stripe webhook secret was returned by public config projection")
	}
	if _, ok := values["airwallex_client_id"]; ok {
		t.Fatal("Airwallex client ID was returned by public config projection")
	}
	if values["public_base_url"] != "https://example.test" || values["store_debug_mode"] != "1" {
		t.Fatalf("non-sensitive configuration was lost: %#v", values)
	}
}

func TestStoreDebugModeAcceptsLegacyNumericConfigValues(t *testing.T) {
	if !parseBoolConfig("1", false) {
		t.Fatal("numeric true config was rejected")
	}
	if parseBoolConfig("0", true) {
		t.Fatal("numeric false config was rejected")
	}
}

func TestLegacySaveConfigPersistsStripeSecret(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("stripe_secret_key", "sk_test_platform", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "sk_test_platform").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	body, err := json.Marshal(map[string]any{"stripe_secret_key": "  sk_test_platform  "})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLegacySaveConfigPersistsCardPoolRouting(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
	)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("card_pool_routing", "FAILOVER", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "FAILOVER").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader([]byte(`{"cardPoolRouting":"failover"}`))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func expectCardProviderConfigQuery(mock sqlmock.Sqlmock, values ...[]driver.Value) {
	rows := sqlmock.NewRows([]string{"key", "value", "is_secret"})
	for _, value := range values {
		rows.AddRow(value[0], value[1], value[2])
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "app_configs"`)).
		WillReturnRows(rows)
}

func TestValidateCardProviderSettingsRejectsDisabledDefault(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "AIRWALLEX", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_airwallex_enabled", "0", false},
		[]driver.Value{"card_provider_stripe_issuing_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{"card_pool_default_provider": "AIRWALLEX"})
	if err == nil || err.Error() != "默认 Provider 必须处于启用状态" {
		t.Fatalf("validation error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateCardProviderSettingsAllowsEnableAndSwitchTogether(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_airwallex_enabled", "0", false},
		[]driver.Value{"card_provider_stripe_issuing_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{
		"card_pool_default_provider":      "airwallex",
		"card_provider_airwallex_enabled": "true",
		"airwallex_client_id":             "client-1",
		"airwallex_api_key":               "api-key-1",
	})
	if err != nil {
		t.Fatalf("combined enable and switch should be valid: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateCardProviderSettingsAllowsEnableAndSwitchToKimooxTogether(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_kimoox_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{
		"card_pool_default_provider":   "KIMOOX",
		"card_provider_kimoox_enabled": "1",
		"kimoox_api_key":               "api-key",
		"kimoox_api_secret":            "api-secret",
		"kimoox_card_bin_ids":          "1001",
	})
	if err != nil {
		t.Fatalf("combined Kimoox enable and switch should be valid: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateCardProviderSettingsRejectsIncompleteKimooxBeforeWriting(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_kimoox_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{
		"card_pool_default_provider":   "KIMOOX",
		"card_provider_kimoox_enabled": "1",
		"kimoox_api_key":               "api-key",
	})
	if err == nil || !strings.Contains(err.Error(), "Kimoox API Secret") {
		t.Fatalf("validation error = %v, want missing Kimoox API Secret", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateCardProviderSettingsAllowsDisablingNonDefault(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_airwallex_enabled", "1", false},
		[]driver.Value{"card_provider_stripe_issuing_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{"card_provider_airwallex_enabled": "false"})
	if err != nil {
		t.Fatalf("disabling a non-default Provider should be valid: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLegacySaveConfigRejectsDisabledDefaultBeforeWriting(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_airwallex_enabled", "0", false},
		[]driver.Value{"card_provider_stripe_issuing_enabled", "0", false},
	)

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewBufferString(`{"cardPoolDefaultProvider":"AIRWALLEX"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("默认 Provider 必须处于启用状态")) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLegacySaveConfigPersistsAirwallexSecret(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_airwallex_enabled", "0", false},
	)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("airwallex_api_key", "aw-key", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "aw-key").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	server := &Server{DB: db}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader([]byte(`{"airwallexAPIKey":"aw-key"}`))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestUpsertConfigPersistsEmptyValue(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("email_smtp_host", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	server := &Server{DB: db}
	if err := server.upsertConfig("email_smtp_host", ""); err != nil {
		t.Fatalf("upsertConfig() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateCardProviderSettingsUsesRequestScopedPhotonPayCredentials(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_photonpay_enabled", "0", false},
	)

	server := &Server{DB: database}
	err := server.validateCardProviderSettings(map[string]string{
		"card_pool_default_provider":      "PHOTONPAY",
		"card_provider_photonpay_enabled": "1",
		"photonpay_app_id":                "photon-app",
		"photonpay_app_secret":            "photon-secret",
		"photonpay_private_key":           "photon-private-key",
		"photonpay_card_bin":              "411111",
	})
	if err != nil {
		t.Fatalf("request-scoped PhotonPay credentials rejected: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLegacySaveConfigRejectsIncompleteDogPayBeforeWriting(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
		[]driver.Value{"card_provider_dogpay_enabled", "0", false},
	)

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	router := gin.New()
	router.POST("/config", server.legacySaveConfig)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config", bytes.NewBufferString(`{"cardPoolDefaultProvider":"DOGPAY","dogpayEnabled":true,"dogpayChannelID":"channel-1"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("DogPay App ID")) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestModernSaveConfigAcceptsCanonicalProviderFields(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expectCardProviderConfigQuery(mock,
		[]driver.Value{"card_pool_default_provider", "LOCAL_TEXT", false},
		[]driver.Value{"card_provider_local_text_enabled", "1", false},
	)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("card_pool_routing", "FAILOVER", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "FAILOVER").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	router := gin.New()
	router.PUT("/config", server.saveConfig)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/config", bytes.NewBufferString(`{"card_pool_routing":"failover"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestUpsertConfigBatchRollsBackWhenOneWriteFails(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.MatchExpectationsInOrder(false)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("first", "one", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO .*"app_configs".*ON CONFLICT \("key"\) DO UPDATE SET`).
		WithArgs("second", "two", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "two").
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	server := &Server{DB: database}
	if err := server.upsertConfigBatch(map[string]string{"first": "one", "second": "two"}); err == nil {
		t.Fatal("upsertConfigBatch() unexpectedly succeeded")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
