package dogpay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	api "github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/dogpay/generated"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

const providerName = "DOGPAY"

const dogPayGrantType = "client_credential"

// Provider is the DogPay adapter. All DogPay request/response details remain
// in this package; the card-pool service only sees cardpool.PaymentCard.
type Provider struct {
	Config     cardpool.ConfigReader
	HTTPClient *http.Client

	clientMu sync.Mutex
	client   *api.Client
	baseURL  string

	tokenMu        sync.Mutex
	tokenRefreshMu sync.Mutex
	token          string
	tokenExp       time.Time

	keyMu          sync.Mutex
	keyFingerprint string
	privateKey     *rsa.PrivateKey
}

func New(config cardpool.ConfigReader, httpClient *http.Client) *Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Provider{Config: config, HTTPClient: httpClient}
}

func (p *Provider) ProviderName() string { return providerName }

func (p *Provider) Supports(capability cardpool.Capability) bool {
	switch capability {
	case cardpool.CapabilityCreateCard,
		cardpool.CapabilitySensitiveDetails,
		cardpool.CapabilityFreeze,
		cardpool.CapabilityUnfreeze,
		cardpool.CapabilityCancel,
		cardpool.CapabilityMultiUse,
		cardpool.CapabilityRecurringPayment,
		cardpool.CapabilityTransactionLimit,
		cardpool.CapabilityTransactionQuery,
		cardpool.CapabilityWebhook:
		return true
	default:
		// DogPay's card endpoint does not expose a native single-use contract.
		// ONE_TIME is therefore enforced by the normalized allocation lifecycle.
		return false
	}
}

func (p *Provider) ValidateConfiguration() error {
	if !p.enabled() {
		return nil
	}
	checks := []struct {
		value string
		name  string
	}{
		{p.secret("dogpay_appid", p.value("dogpay_appid", "")), "DogPay App ID"},
		{p.secret("dogpay_secret", p.value("dogpay_secret", "")), "DogPay App Secret"},
		{p.value("dogpay_channel_id", ""), "DogPay Channel ID"},
		{p.secret("dogpay_cardholder_id", p.value("dogpay_cardholder_id", "")), "DogPay Cardholder ID"},
		{p.secret("dogpay_private_key", p.value("dogpay_private_key", "")), "DogPay RSA private key"},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.value) == "" {
			return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%s 未配置", check.name))
		}
	}
	if _, err := p.optionalNonNegativeAmount("dogpay_velocity_amount_limit"); err != nil {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, err)
	}
	if strings.TrimSpace(p.base()) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("DogPay Base URL 未配置"))
	}
	return nil
}

func (p *Provider) value(key, fallback string) string {
	if p != nil && p.Config != nil {
		return strings.TrimSpace(p.Config.Value(key, fallback))
	}
	return strings.TrimSpace(fallback)
}

func (p *Provider) secret(key, fallback string) string {
	if p != nil && p.Config != nil {
		return strings.TrimSpace(p.Config.Secret(key, fallback))
	}
	return strings.TrimSpace(fallback)
}

func (p *Provider) enabled() bool {
	return configBool(p.value("card_provider_dogpay_enabled", "0"), false)
}

func (p *Provider) base() string {
	return strings.TrimRight(p.value("dogpay_base_url", "https://sandbox-api-v2.dogpay.com"), "/")
}

func (p *Provider) entityID() string {
	return p.value("dogpay_entity_id", "")
}

func (p *Provider) clientForBase() (*api.Client, error) {
	base := p.base()
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.client != nil && p.baseURL == base {
		return p.client, nil
	}
	client, err := api.NewClient(base, api.WithHTTPClient(p.HTTPClient), api.WithRequestEditorFn(p.authorizeRequest))
	if err != nil {
		return nil, cardpool.NewProviderError(providerName, "client", cardpool.CategoryInvalidRequest, false, false, err)
	}
	p.client, p.baseURL = client, base
	p.invalidateToken()
	return client, nil
}

func (p *Provider) authorizeRequest(ctx context.Context, request *http.Request) error {
	if strings.HasSuffix(request.URL.Path, "/open-api/v1/auth/access_token") {
		request.Header.Set("Accept", "application/json")
		return nil
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	return nil
}

func (p *Provider) accessToken(ctx context.Context) (string, error) {
	p.tokenRefreshMu.Lock()
	defer p.tokenRefreshMu.Unlock()
	p.tokenMu.Lock()
	if p.token != "" && time.Until(p.tokenExp) > time.Minute {
		token := p.token
		p.tokenMu.Unlock()
		return token, nil
	}
	p.tokenMu.Unlock()

	appid := p.secret("dogpay_appid", p.value("dogpay_appid", ""))
	secret := p.secret("dogpay_secret", p.value("dogpay_secret", ""))
	if appid == "" || secret == "" {
		return "", cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryInvalidRequest, false, false, errors.New("DogPay App ID/App Secret 未配置"))
	}
	client, err := p.clientForBase()
	if err != nil {
		return "", err
	}
	response, requestErr := client.GetAccessToken(ctx, api.AuthRequest{GrantType: dogPayGrantType, Appid: appid, Secret: secret})
	if requestErr != nil {
		return "", shared.RequestError(providerName, "authenticate", requestErr)
	}
	payload, decodeErr := shared.DecodeJSONObjectResponse(providerName, "authenticate", response)
	if decodeErr != nil {
		return "", decodeErr
	}
	if err := dogPayBusinessError(payload, "authenticate"); err != nil {
		return "", err
	}
	data := dogPayData(payload)
	token := firstNonEmpty(shared.StringField(data, "access_token", "accessToken", "token"), shared.StringField(payload, "access_token", "accessToken", "token"))
	if token == "" {
		return "", cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryTechnicalFailure, true, true, errors.New("DogPay authentication response has no token"))
	}
	expiresAt := time.Now().Add(2 * time.Hour)
	if raw := firstNonEmpty(shared.StringField(data, "expires_in", "expiresIn"), shared.StringField(payload, "expires_in", "expiresIn")); raw != "" {
		if seconds, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil && seconds > 0 {
			expiresAt = time.Now().Add(time.Duration(seconds) * time.Second)
		}
	}
	p.tokenMu.Lock()
	p.token, p.tokenExp = token, expiresAt
	p.tokenMu.Unlock()
	return token, nil
}

func (p *Provider) invalidateToken() {
	p.tokenMu.Lock()
	p.token, p.tokenExp = "", time.Time{}
	p.tokenMu.Unlock()
}

func (p *Provider) authenticatedRequest(ctx context.Context, call func(*api.Client) (*http.Response, error)) (*http.Response, error) {
	if !p.enabled() {
		return nil, cardpool.NewProviderError(providerName, "request", cardpool.CategoryProviderUnavailable, false, true, errors.New("DogPay provider is disabled"))
	}
	client, err := p.clientForBase()
	if err != nil {
		return nil, err
	}
	response, requestErr := call(client)
	if requestErr != nil {
		return nil, shared.RequestError(providerName, "request", requestErr)
	}
	if response != nil && response.StatusCode == http.StatusUnauthorized {
		_ = response.Body.Close()
		p.invalidateToken()
		response, requestErr = call(client)
		if requestErr != nil {
			return nil, shared.RequestError(providerName, "request", requestErr)
		}
	}
	return response, nil
}

func (p *Provider) HealthCheck(ctx context.Context) (cardpool.ProviderHealth, error) {
	health := cardpool.ProviderHealth{Provider: providerName, Status: "disabled"}
	if !p.enabled() {
		return health, nil
	}
	page, take := 1, 1
	params := &api.ListCardsParams{Page: &page, Take: &take}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListCards(ctx, params)
	})
	if err != nil {
		health.Status = "unavailable"
		return health, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "health", response)
	if err != nil {
		health.Status = "unavailable"
		return health, err
	}
	if err := dogPayBusinessError(payload, "health"); err != nil {
		health.Status = "unavailable"
		return health, err
	}
	now := time.Now()
	health.Available, health.Status, health.LastSuccessAt = true, "healthy", &now
	return health, nil
}

func (p *Provider) AcquireCard(ctx context.Context, request cardpool.AcquireCardRequest) (cardpool.PaymentCard, error) {
	return p.CreateCard(ctx, cardpool.CreateCardRequest{
		PoolID: request.PoolID, BusinessAccountID: request.BusinessAccountID, PaymentTaskID: request.PaymentTaskID,
		UsageType: request.UsageType, Amount: request.Amount, Currency: request.Currency, IdempotencyKey: request.IdempotencyKey,
	})
}

func (p *Provider) CreateCard(ctx context.Context, request cardpool.CreateCardRequest) (cardpool.PaymentCard, error) {
	if !p.enabled() {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("DogPay provider is disabled"))
	}
	cardholderID := p.secret("dogpay_cardholder_id", p.value("dogpay_cardholder_id", ""))
	if cardholderID == "" {
		cardholderID = strings.TrimSpace(request.BusinessAccountID)
	}
	if cardholderID == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("DogPay cardholder ID 未配置"))
	}
	channelID := p.value("dogpay_channel_id", "")
	if channelID == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("DogPay Channel ID 未配置"))
	}
	callID := firstNonEmpty(request.IdempotencyKey, uuid.NewString())
	// DogPay documents the virtual card type in lower case. Accept legacy
	// uppercase settings from the admin UI but normalize the wire value.
	cardType := strings.ToLower(firstNonEmpty(p.value("dogpay_card_type", "virtual"), "virtual"))
	label := firstNonEmpty(request.PaymentTaskID, request.BusinessAccountID, callID)
	options := api.CardOptions{CallId: callID, CardHolderId: cardholderID, Label: &label}
	if request.Amount > 0 {
		amount := float32(request.Amount)
		options.Amount = &amount
	}
	if value, err := p.optionalNonNegativeAmount("dogpay_velocity_amount_limit"); err != nil {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, err)
	} else if value > 0 {
		velocity := float32(value)
		options.VelocityAmountLimit = &velocity
	}
	body := api.CreateCardRequest{ChannelId: channelID, CardType: cardType, CardOptions: options}
	if budgetID := p.value("dogpay_budget_id", ""); budgetID != "" {
		body.BudgetId = &budgetID
	}
	params := &api.CreateCardParams{}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.CreateCard(ctx, params, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "create_card", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := dogPayBusinessError(payload, "create_card"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	card := mapCard(dogPayCardObject(dogPayData(payload)), request)
	if card.ProviderCardID == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryTechnicalFailure, true, true, errors.New("DogPay create response has no card id"))
	}
	return card, nil
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
	}
	page, take := 1, 1
	params := &api.ListCardsParams{Page: &page, Take: &take, CardId: &providerCardID}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListCards(ctx, params)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_card", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := dogPayBusinessError(payload, "get_card"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	data := dogPayCardObject(dogPayData(payload))
	if items := dogPayArray(payload); len(items) > 0 {
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if id := dogPayCardID(object); id == providerCardID {
				data = dogPayCardObject(object)
				break
			}
		}
	}
	card := mapCard(data, cardpool.CreateCardRequest{})
	if card.ProviderCardID == "" {
		return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
	}
	return card, nil
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.SensitiveCardDetails{}, cardpool.ErrCardNotFound
	}
	params := &api.GetCardPrivateInfoParams{CardId: providerCardID}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetCardPrivateInfo(ctx, params)
	})
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_sensitive_card_details", response)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	if err := dogPayBusinessError(payload, "get_sensitive_card_details"); err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	data := dogPayData(payload)
	if encrypted := shared.StringField(data, "encrypted_info", "encryptedInfo"); encrypted != "" {
		decrypted, decryptErr := p.decryptPrivateInfo(encrypted)
		if decryptErr != nil {
			return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, decryptErr)
		}
		data = decrypted
	}
	data = dogPayCardObject(data)
	result := cardpool.SensitiveCardDetails{
		CardNumber:     firstNonEmpty(shared.StringField(data, "card_number", "cardNumber", "number", "pan", "account_number")),
		CVC:            firstNonEmpty(shared.StringField(data, "cvv", "cvc", "security_code", "securityCode")),
		CardholderName: firstNonEmpty(shared.StringField(data, "cardholder_name", "cardholderName", "name")),
	}
	result.ExpiryMonth = shared.IntField(data, "expiry_month", "expiryMonth", "expire_m", "expireMonth")
	result.ExpiryYear = shared.IntField(data, "expiry_year", "expiryYear", "expire_y", "expireYear")
	if result.ExpiryMonth == 0 || result.ExpiryYear == 0 {
		month, year, ok := parseMonthYear(firstNonEmpty(shared.StringField(data, "expiry", "expiration", "expiration_date", "expirationDate")))
		if ok {
			result.ExpiryMonth, result.ExpiryYear = month, year
		}
	}
	if result.CardNumber == "" || result.CVC == "" || result.ExpiryMonth < 1 || result.ExpiryMonth > 12 || result.ExpiryYear < 1 || strings.Contains(result.CardNumber, "*") {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("DogPay sensitive response is incomplete"))
	}
	return result, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateFreeze(ctx, providerCardID, true)
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateFreeze(ctx, providerCardID, false)
}

func (p *Provider) updateFreeze(ctx context.Context, providerCardID string, freeze bool) error {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.ErrCardNotFound
	}
	body := api.CardIDRequest{CardId: providerCardID}
	if freeze {
		params := &api.FreezeCardParams{}
		p.applyEntity(params)
		response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
			return client.FreezeCard(ctx, params, body)
		})
		if err != nil {
			return err
		}
		payload, err := shared.DecodeJSONObjectResponse(providerName, "freeze_card", response)
		if err != nil {
			return err
		}
		return dogPayBusinessError(payload, "freeze_card")
	}
	params := &api.UnfreezeCardParams{}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.UnfreezeCard(ctx, params, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "unfreeze_card", response)
	if err != nil {
		return err
	}
	return dogPayBusinessError(payload, "unfreeze_card")
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.ErrCardNotFound
	}
	params := &api.DeleteCardParams{}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.DeleteCard(ctx, params, api.CardIDRequest{CardId: providerCardID})
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "cancel_card", response)
	if err != nil {
		return err
	}
	return dogPayBusinessError(payload, "cancel_card")
}

func (p *Provider) UpdateLimits(ctx context.Context, providerCardID string, limits cardpool.CardLimits) error {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.ErrCardNotFound
	}
	body := api.SpendingLimitRequest{CardId: providerCardID}
	if limits.PerTransaction > 0 {
		value := float32(limits.PerTransaction)
		body.SingleAmountLimit = &value
	}
	if limits.VelocityAmount > 0 {
		value := float32(limits.VelocityAmount)
		body.VelocityAmountLimit = &value
	}
	if limits.Weekly > 0 {
		value := float32(limits.Weekly)
		body.WeekAmountLimit = &value
	}
	if limits.Daily > 0 {
		value := float32(limits.Daily)
		body.DayAmountLimit = &value
	}
	if limits.Monthly > 0 {
		value := float32(limits.Monthly)
		body.MonthAmountLimit = &value
	}
	params := &api.UpdateSpendingLimitParams{}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.UpdateSpendingLimit(ctx, params, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "update_limits", response)
	if err != nil {
		return err
	}
	return dogPayBusinessError(payload, "update_limits")
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	cardID := strings.TrimSpace(providerCardID)
	if cardID == "" {
		return nil, cardpool.ErrCardNotFound
	}
	page, take := 1, 100
	params := &api.ListTransactionsParams{Page: &page, Take: &take, CardId: &cardID}
	p.applyEntity(params)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListTransactions(ctx, params)
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "list_transactions", response)
	if err != nil {
		return nil, err
	}
	if err := dogPayBusinessError(payload, "list_transactions"); err != nil {
		return nil, err
	}
	items := dogPayArray(payload)
	result := make([]cardpool.CardTransaction, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		transaction := cardpool.CardTransaction{
			ProviderTransactionID: firstNonEmpty(shared.StringField(object, "transactionId", "transaction_id", "id", "orderId", "order_id")),
			ProviderCardID:        firstNonEmpty(shared.StringField(object, "cardId", "card_id"), cardID),
			Amount:                shared.FloatField(object, "amount", "transactionAmount", "transaction_amount"),
			Currency:              strings.ToUpper(shared.StringField(object, "currency", "transactionCurrency", "transaction_currency")),
			Status:                strings.ToUpper(shared.StringField(object, "status", "transactionStatus", "transaction_status")),
			Type:                  firstNonEmpty(shared.StringField(object, "type", "transactionType", "transaction_type"), "card_payment"),
			MerchantName:          firstNonEmpty(shared.StringField(object, "merchantName", "merchant_name", "merchant"), shared.StringField(object, "detail")),
			FailureCode:           dogPayFailureCode(object),
		}
		if merchant := shared.ObjectField(object, "merchant", "merchantData", "merchant_data", "merchantInfo", "merchant_info"); merchant != nil {
			transaction.MerchantName = firstNonEmpty(transaction.MerchantName, shared.StringField(merchant, "name", "merchantName", "merchant_name", "displayName", "display_name"))
			transaction.FailureCode = firstNonEmpty(transaction.FailureCode, dogPayFailureCode(merchant))
		}
		if occurred, ok := parseTime(firstNonEmpty(shared.StringField(object, "createdAt", "createAt", "created_at", "create_at", "completeAt", "complete_at", "transactionAt", "transaction_at"))); ok {
			transaction.OccurredAt = &occurred
		}
		if transaction.ProviderTransactionID != "" {
			result = append(result, transaction)
		}
	}
	return result, nil
}

func (p *Provider) applyEntity(params any) {
	entity := p.entityID()
	if entity == "" {
		return
	}
	switch value := params.(type) {
	case *api.DeleteCardParams:
		value.DgpEntityId = &entity
	case *api.ListCardsParams:
		value.DgpEntityId = &entity
	case *api.CreateCardParams:
		value.DgpEntityId = &entity
	case *api.FreezeCardParams:
		value.DgpEntityId = &entity
	case *api.GetCardPrivateInfoParams:
		value.DgpEntityId = &entity
	case *api.GetSpendingLimitParams:
		value.DgpEntityId = &entity
	case *api.UpdateSpendingLimitParams:
		value.DgpEntityId = &entity
	case *api.ListTransactionsParams:
		value.DgpEntityId = &entity
	case *api.UnfreezeCardParams:
		value.DgpEntityId = &entity
	}
}

func (p *Provider) optionalNonNegativeAmount(key string) (float64, error) {
	raw := strings.TrimSpace(p.value(key, "0"))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s 必须是大于等于 0 的数字", key)
	}
	return value, nil
}

func mapCard(data map[string]any, request cardpool.CreateCardRequest) cardpool.PaymentCard {
	providerStatus := shared.StringField(data, "status", "cardStatus", "card_status", "state")
	cardType := firstNonEmpty(shared.StringField(data, "cardType", "card_type", "type"), "virtual")
	currency := strings.ToUpper(firstNonEmpty(request.Currency, shared.StringField(data, "currency", "cardCurrency", "card_currency")))
	return cardpool.PaymentCard{
		Provider: providerName, ProviderCardID: dogPayCardID(data), Last4: lastFour(shared.StringField(data, "last4", "lastFour", "last_four", "maskedPan", "masked_pan", "maskedCardNumber", "masked_card_number")),
		CardholderName: firstNonEmpty(shared.StringField(data, "cardholderName", "cardholder_name", "cardHolderName", "name")),
		CardType:       cardType, UsageType: request.UsageType, Currency: currency,
		Status: mapStatus(providerStatus), ProviderStatus: providerStatus,
	}
}

func mapStatus(status string) cardpool.InternalCardStatus {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.NewReplacer("-", "_", " ", "_").Replace(status)
	switch status {
	case "active", "normal", "enabled", "open":
		return cardpool.CardActive
	case "frozen", "freeze", "inactive", "suspended", "blocked":
		return cardpool.CardFrozen
	case "pending", "creating", "opening", "processing", "issued":
		return cardpool.CardCreating
	case "cancelled", "canceled", "closed", "terminated", "expired", "pre_delete", "deleted", "delete":
		return cardpool.CardCancelled
	default:
		return cardpool.CardFailed
	}
}

func dogPayBusinessError(payload map[string]any, operation string) error {
	if value, ok := payload["success"].(bool); ok && !value {
		return dogPayMappedBusinessError(operation, shared.StringField(payload, "code", "errorCode", "error_code"), shared.StringField(payload, "message", "msg", "error"))
	}
	code := shared.StringField(payload, "code", "errorCode", "error_code", "statusCode", "status_code")
	if code == "" || code == "0" || code == "200" || strings.EqualFold(code, "success") || strings.EqualFold(code, "ok") {
		return nil
	}
	message := shared.StringField(payload, "message", "msg", "error", "errorMessage", "error_message")
	return dogPayMappedBusinessError(operation, code, message)
}

func dogPayMappedBusinessError(operation, code, message string) error {
	category := cardpool.CategoryInvalidRequest
	retryable, failover := false, false
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "100001":
		category = cardpool.CategoryInsufficientFunds
	case "100003", "100004", "100005", "100006", "100019":
		category = cardpool.CategoryComplianceBlock
	case "100007", "100018":
		category, retryable, failover = cardpool.CategoryProviderUnavailable, true, true
	case "100002", "100008", "100012", "100016", "100017", "200801", "200802", "200808", "200810", "200811":
		category = cardpool.CategoryBusinessDecline
	default:
		return shared.BusinessResponseError(providerName, operation, code, message)
	}
	stable := "provider returned an application error"
	if strings.TrimSpace(code) != "" {
		stable = fmt.Sprintf("provider returned application error code %s", strings.TrimSpace(code))
	}
	return cardpool.NewProviderError(providerName, operation, category, retryable, failover, errors.New(stable))
}

func dogPayData(payload map[string]any) map[string]any {
	for _, key := range []string{"data", "result", "card", "cardInfo", "card_info"} {
		if object := shared.ObjectField(payload, key); object != nil {
			return object
		}
	}
	return payload
}

func dogPayCardObject(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	for _, key := range []string{"card", "cardInfo", "card_info", "item", "object"} {
		if object := shared.ObjectField(data, key); object != nil {
			return object
		}
	}
	return data
}

func dogPayArray(payload map[string]any) []any {
	if items := shared.ArrayField(payload, "data", "items", "records", "list", "cards", "transactions"); len(items) > 0 {
		return items
	}
	for _, key := range []string{"data", "result"} {
		if object := shared.ObjectField(payload, key); object != nil {
			if items := shared.ArrayField(object, "items", "records", "list", "cards", "transactions"); len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func dogPayCardID(data map[string]any) string {
	return firstNonEmpty(shared.StringField(data, "cardId", "card_id", "id"))
}

func (p *Provider) decryptPrivateInfo(value string) (map[string]any, error) {
	key, err := p.loadPrivateKey()
	if err != nil {
		return nil, err
	}
	encoded := strings.TrimSpace(value)
	decoded, err := decodeBase64(encoded)
	if err != nil {
		return nil, errors.New("DogPay encrypted card details are not base64")
	}
	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, key, decoded)
	if err != nil {
		return nil, fmt.Errorf("DogPay encrypted card details could not be decrypted")
	}
	var object map[string]any
	if err := json.Unmarshal(plaintext, &object); err != nil || object == nil {
		return nil, errors.New("DogPay decrypted card details are not JSON")
	}
	return dogPayData(object), nil
}

func (p *Provider) loadPrivateKey() (*rsa.PrivateKey, error) {
	value := p.secret("dogpay_private_key", p.value("dogpay_private_key", ""))
	if value == "" {
		return nil, cardpool.NewProviderError(providerName, "decrypt_private_info", cardpool.CategoryInvalidRequest, false, false, errors.New("DogPay RSA private key 未配置"))
	}
	value = strings.ReplaceAll(value, `\n`, "\n")
	p.keyMu.Lock()
	defer p.keyMu.Unlock()
	if p.privateKey != nil && p.keyFingerprint == value {
		return p.privateKey, nil
	}
	key, err := parsePrivateKey(value)
	if err != nil {
		return nil, cardpool.NewProviderError(providerName, "decrypt_private_info", cardpool.CategoryInvalidRequest, false, false, err)
	}
	p.keyFingerprint, p.privateKey = value, key
	return key, nil
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	data := []byte(strings.TrimSpace(value))
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	if key, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(data); err == nil {
		return key, nil
	}
	if decoded, err := decodeBase64(strings.TrimSpace(value)); err == nil {
		if key, parseErr := x509.ParsePKCS8PrivateKey(decoded); parseErr == nil {
			if rsaKey, ok := key.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
		}
		if key, parseErr := x509.ParsePKCS1PrivateKey(decoded); parseErr == nil {
			return key, nil
		}
	}
	return nil, errors.New("DogPay RSA private key 格式无效，需 PKCS#8 或 PKCS#1 PEM")
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func parseMonthYear(value string) (int, int, bool) {
	value = strings.TrimSpace(value)
	for _, separator := range []string{"/", "-", "_"} {
		parts := strings.Split(value, separator)
		if len(parts) != 2 {
			continue
		}
		first, firstErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		second, secondErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if firstErr != nil || secondErr != nil {
			continue
		}
		if first > 12 && second <= 12 {
			first, second = second, first
		}
		if first >= 1 && first <= 12 {
			if second < 100 {
				second += 2000
			}
			return first, second, true
		}
	}
	return 0, 0, false
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix > 0 {
		if unix > 1_000_000_000_000 {
			return time.UnixMilli(unix), true
		}
		return time.Unix(unix, 0), true
	}
	return time.Time{}, false
}

func lastFour(value string) string {
	digits := make([]byte, 0, len(value))
	for index := range value {
		if value[index] >= '0' && value[index] <= '9' {
			digits = append(digits, value[index])
		}
	}
	if len(digits) < 4 {
		return ""
	}
	return string(digits[len(digits)-4:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func configBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

var _ cardpool.CardProvider = (*Provider)(nil)
