package airwallex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	api "github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/airwallex/generated"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

const providerName = "AIRWALLEX"

type Provider struct {
	Config     cardpool.ConfigReader
	HTTPClient *http.Client

	clientMu       sync.Mutex
	client         *api.Client
	baseURL        string
	tokenMu        sync.Mutex
	tokenRefreshMu sync.Mutex
	token          string
	tokenExp       time.Time
}

func New(config cardpool.ConfigReader, httpClient *http.Client) *Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Provider{Config: config, HTTPClient: httpClient}
}

func (p *Provider) ProviderName() string { return providerName }

func (p *Provider) ValidateConfiguration() error {
	if !p.enabled() {
		return nil
	}
	if p.secret("airwallex_client_id", p.value("airwallex_client_id", "")) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Airwallex Client ID 未配置"))
	}
	if p.secret("airwallex_api_key", p.value("airwallex_api_key", "")) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Airwallex API Key 未配置"))
	}
	if strings.TrimSpace(p.base()) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Airwallex Base URL 未配置"))
	}
	return nil
}

func (p *Provider) Supports(capability cardpool.Capability) bool {
	switch capability {
	case cardpool.CapabilityCreateCard, cardpool.CapabilitySensitiveDetails,
		cardpool.CapabilityFreeze, cardpool.CapabilityUnfreeze, cardpool.CapabilityCancel,
		cardpool.CapabilitySingleUse, cardpool.CapabilityMultiUse,
		cardpool.CapabilityRecurringPayment, cardpool.CapabilityTransactionQuery,
		cardpool.CapabilityWebhook:
		return true
	default:
		return false
	}
}

func (p *Provider) enabled() bool {
	return configBool(p.value("card_provider_airwallex_enabled", "0"), false)
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

func (p *Provider) base() string {
	return strings.TrimRight(p.value("airwallex_base_url", "https://api.sandbox.airwallex.com"), "/")
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
	p.tokenMu.Lock()
	p.token, p.tokenExp = "", time.Time{}
	p.tokenMu.Unlock()
	return client, nil
}

func (p *Provider) authorizeRequest(ctx context.Context, request *http.Request) error {
	if strings.HasSuffix(request.URL.Path, "/api/v1/authentication/login") {
		return nil
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
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

	client, err := p.clientForBase()
	if err != nil {
		return "", err
	}
	clientID := p.secret("airwallex_client_id", p.value("airwallex_client_id", ""))
	apiKey := p.secret("airwallex_api_key", p.value("airwallex_api_key", ""))
	if clientID == "" || apiKey == "" {
		return "", cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryInvalidRequest, false, false, errors.New("Airwallex Client ID/API Key 未配置"))
	}
	response, requestErr := client.AuthenticationLogin(ctx, api.LoginRequest{ClientId: clientID, ApiKey: apiKey})
	if requestErr != nil {
		return "", shared.RequestError(providerName, "authenticate", requestErr)
	}
	var login api.LoginResponse
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "authenticate", response, &login); decodeErr != nil {
		return "", decodeErr
	}
	if login.Token == nil || strings.TrimSpace(*login.Token) == "" {
		return "", cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryTechnicalFailure, true, true, errors.New("Airwallex authentication response has no token"))
	}
	expiresAt := time.Now().Add(30 * time.Minute)
	if login.ExpiresAt != nil {
		if parsed, parseErr := parseTime(*login.ExpiresAt); parseErr == nil {
			expiresAt = parsed
		}
	}
	p.tokenMu.Lock()
	p.token, p.tokenExp = strings.TrimSpace(*login.Token), expiresAt
	p.tokenMu.Unlock()
	return strings.TrimSpace(*login.Token), nil
}

func (p *Provider) invalidateToken() {
	p.tokenMu.Lock()
	p.token, p.tokenExp = "", time.Time{}
	p.tokenMu.Unlock()
}

func (p *Provider) authenticatedRequest(ctx context.Context, call func(*api.Client) (*http.Response, error)) (*http.Response, error) {
	if !p.enabled() {
		return nil, cardpool.NewProviderError(providerName, "request", cardpool.CategoryProviderUnavailable, false, true, errors.New("Airwallex provider is disabled"))
	}
	client, err := p.clientForBase()
	if err != nil {
		return nil, err
	}
	response, requestErr := call(client)
	if requestErr != nil {
		var providerErr *cardpool.ProviderError
		if errors.As(requestErr, &providerErr) {
			return nil, requestErr
		}
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
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetIssuingConfig(ctx)
	})
	if err != nil {
		health.Status = "unavailable"
		return health, err
	}
	if _, err := shared.DecodeJSONResponse(providerName, "health", response, &api.GenericObject{}); err != nil {
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
		UsageType: request.UsageType, Amount: request.Amount, Currency: request.Currency,
		IdempotencyKey: request.IdempotencyKey,
	})
}

func (p *Provider) CreateCard(ctx context.Context, request cardpool.CreateCardRequest) (cardpool.PaymentCard, error) {
	if !p.enabled() {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("Airwallex provider is disabled"))
	}
	cardholderID := p.secret("airwallex_cardholder_id", p.value("airwallex_cardholder_id", ""))
	if cardholderID == "" {
		cardholderID = strings.TrimSpace(request.BusinessAccountID)
	}
	if cardholderID == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("Airwallex cardholder ID 未配置"))
	}
	requestID := strings.TrimSpace(request.IdempotencyKey)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	currency := strings.ToUpper(firstNonEmpty(request.Currency, p.value("airwallex_primary_currency", "USD")))
	allowedCount := api.MULTIPLE
	if request.UsageType == cardpool.UsageOneTime {
		allowedCount = api.SINGLE
	}
	formFactor := strings.ToUpper(p.value("airwallex_form_factor", "VIRTUAL"))
	if formFactor != string(api.VIRTUAL) && formFactor != string(api.PHYSICAL) {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("unsupported Airwallex form factor %q", formFactor))
	}
	limits := []map[string]any{}
	if request.Amount > 0 {
		limits = append(limits, map[string]any{"amount": request.Amount, "interval": "PER_TRANSACTION"})
	}
	metadata := copyMetadata(request.Metadata)
	body := api.CreateCardRequest{
		RequestId: requestID, CardholderId: cardholderID,
		CreatedBy:  firstNonEmpty(p.value("airwallex_created_by", ""), "auto-recharge-platform"),
		FormFactor: api.CreateCardRequestFormFactor(formFactor), IsPersonalized: false,
		ActivateOnIssue:       boolPointer(configBool(p.value("airwallex_activate_on_issue", "1"), true)),
		Purpose:               stringPointer(p.value("airwallex_card_purpose", "COMMERCIAL")),
		Program:               api.CardProgram{Type: stringPointer(p.value("airwallex_card_type", "DEBIT")), Purpose: stringPointer(p.value("airwallex_card_purpose", "COMMERCIAL"))},
		AuthorizationControls: api.AuthorizationControls{AllowedTransactionCount: allowedCount, TransactionLimits: map[string]interface{}{"currency": currency, "limits": limits}},
		Metadata:              &metadata,
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.CreateIssuingCard(ctx, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	var remote api.AirwallexCard
	if _, err := shared.DecodeJSONResponse(providerName, "create_card", response, &remote); err != nil {
		return cardpool.PaymentCard{}, err
	}
	return mapCard(remote, request), nil
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetIssuingCard(ctx, providerCardID)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	var remote api.AirwallexCard
	if _, err := shared.DecodeJSONResponse(providerName, "get_card", response, &remote); err != nil {
		return cardpool.PaymentCard{}, err
	}
	card := mapCard(remote, cardpool.CreateCardRequest{})
	if card.ProviderCardID == "" {
		card.ProviderCardID = providerCardID
	}
	return card, nil
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetIssuingCardDetails(ctx, strings.TrimSpace(providerCardID))
	})
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	var remote api.SensitiveCardDetails
	if _, err := shared.DecodeJSONResponse(providerName, "get_sensitive_card_details", response, &remote); err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	result := cardpool.SensitiveCardDetails{}
	if remote.CardNumber != nil {
		result.CardNumber = strings.TrimSpace(*remote.CardNumber)
	}
	if remote.Cvv != nil {
		result.CVC = strings.TrimSpace(*remote.Cvv)
	}
	if remote.ExpiryMonth != nil {
		result.ExpiryMonth = *remote.ExpiryMonth
	}
	if remote.ExpiryYear != nil {
		result.ExpiryYear = *remote.ExpiryYear
	}
	if remote.NameOnCard != nil {
		result.CardholderName = strings.TrimSpace(*remote.NameOnCard)
	}
	if result.CardNumber == "" || result.CVC == "" || result.ExpiryMonth < 1 || result.ExpiryMonth > 12 || result.ExpiryYear < 1 || strings.Contains(result.CardNumber, "*") {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("Airwallex sensitive response is incomplete"))
	}
	return result, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.INACTIVE)
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.ACTIVE)
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.CLOSED)
}

func (p *Provider) updateStatus(ctx context.Context, providerCardID string, status api.UpdateCardRequestCardStatus) error {
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.UpdateIssuingCard(ctx, strings.TrimSpace(providerCardID), api.UpdateCardRequest{CardStatus: &status})
	})
	if err != nil {
		return err
	}
	_, err = shared.DecodeJSONResponse(providerName, "update_card", response, &api.AirwallexCard{})
	return err
}

func (p *Provider) UpdateLimits(context.Context, string, cardpool.CardLimits) error {
	return cardpool.UnsupportedCapability(providerName, cardpool.CapabilityTransactionLimit)
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	cardID := strings.TrimSpace(providerCardID)
	params := &api.ListIssuingCardTransactionsParams{CardId: &cardID}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListIssuingCardTransactions(ctx, params)
	})
	if err != nil {
		return nil, err
	}
	var remote api.CardTransactionList
	if _, err := shared.DecodeJSONResponse(providerName, "list_transactions", response, &remote); err != nil {
		return nil, err
	}
	result := make([]cardpool.CardTransaction, 0)
	if remote.Items == nil {
		return result, nil
	}
	for _, item := range *remote.Items {
		transaction := cardpool.CardTransaction{ProviderCardID: cardID, Amount: float64(pointerFloat32(item.Amount)), Currency: pointerString(item.BillingCurrency, ""), Status: pointerString(item.Status, ""), Type: "card_payment", FailureCode: pointerString(item.FailureReason, "")}
		if item.Id != nil {
			transaction.ProviderTransactionID = strings.TrimSpace(*item.Id)
		}
		if transaction.Currency == "" {
			transaction.Currency = additionalString(item.AdditionalProperties, "currency", "billing_currency")
		}
		if item.CreatedAt != nil {
			if parsed, parseErr := parseTime(*item.CreatedAt); parseErr == nil {
				transaction.OccurredAt = &parsed
			}
		}
		result = append(result, transaction)
	}
	return result, nil
}

func mapCard(remote api.AirwallexCard, request cardpool.CreateCardRequest) cardpool.PaymentCard {
	providerCardID := pointerString(remote.CardId, "")
	providerStatus := pointerString(remote.CardStatus, "")
	last4 := firstNonEmpty(pointerString(remote.Last4, ""), additionalString(remote.AdditionalProperties, "last_four", "card_last4"))
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	currency := firstNonEmpty(request.Currency, pointerString(remote.Currency, ""), additionalString(remote.AdditionalProperties, "currency"))
	cardholderName := firstNonEmpty(pointerString(remote.NameOnCard, ""), additionalString(remote.AdditionalProperties, "cardholder_name"))
	return cardpool.PaymentCard{Provider: providerName, ProviderCardID: providerCardID, Last4: last4,
		CardholderName: cardholderName, CardType: "virtual",
		UsageType: request.UsageType, Currency: currency,
		Status: mapStatus(providerStatus), ProviderStatus: providerStatus}
}

func mapStatus(status string) cardpool.InternalCardStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE":
		return cardpool.CardActive
	case "INACTIVE", "SUSPENDED", "FROZEN":
		return cardpool.CardFrozen
	case "CLOSED", "CANCELLED", "CANCELED", "TERMINATED":
		return cardpool.CardCancelled
	case "PENDING", "CREATING", "PROCESSING":
		return cardpool.CardCreating
	default:
		return cardpool.CardFailed
	}
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid provider timestamp")
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

func copyMetadata(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

func pointerString(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func pointerFloat32(value *float32) float32 {
	if value == nil {
		return 0
	}
	return *value
}

func additionalString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
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
