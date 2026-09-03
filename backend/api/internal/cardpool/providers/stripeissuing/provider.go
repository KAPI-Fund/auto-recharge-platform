package stripeissuing

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
	api "github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/stripeissuing/generated"
)

const providerName = "STRIPE_ISSUING"

type Provider struct {
	Config     cardpool.ConfigReader
	HTTPClient *http.Client
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
	key := ""
	if p.Config != nil {
		key = strings.TrimSpace(p.Config.Secret("stripe_issuing_secret_key", p.Config.Value("stripe_issuing_secret_key", "")))
		if key == "" {
			key = strings.TrimSpace(p.Config.Secret("stripe_secret_key", p.Config.Value("stripe_secret_key", "")))
		}
	}
	if key == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Stripe Issuing secret key 未配置"))
	}
	if strings.TrimSpace(p.value("stripe_issuing_base_url", "https://api.stripe.com")) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Stripe Issuing Base URL 未配置"))
	}
	return nil
}

func (p *Provider) Supports(capability cardpool.Capability) bool {
	switch capability {
	case cardpool.CapabilityCreateCard, cardpool.CapabilitySensitiveDetails,
		cardpool.CapabilityFreeze, cardpool.CapabilityUnfreeze, cardpool.CapabilityCancel,
		cardpool.CapabilityMultiUse, cardpool.CapabilityRecurringPayment,
		cardpool.CapabilityTransactionQuery, cardpool.CapabilityWebhook:
		return true
	default:
		// Stripe Issuing does not expose a native single-use card flag through
		// this API surface. ONE_TIME is enforced by CardPoolService lifecycle.
		return false
	}
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
	return configBool(p.value("card_provider_stripe_issuing_enabled", "0"), false)
}

func (p *Provider) client(config cardpool.ConfigReader) (*api.Client, error) {
	base := "https://api.stripe.com"
	if config != nil {
		base = strings.TrimRight(config.Value("stripe_issuing_base_url", base), "/")
	}
	return api.NewClient(base, api.WithHTTPClient(p.HTTPClient), api.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		key := ""
		if config != nil {
			key = strings.TrimSpace(config.Secret("stripe_issuing_secret_key", config.Value("stripe_issuing_secret_key", "")))
			if key == "" {
				// The platform Stripe Payments account may be reused for Issuing,
				// but Issuing remains a separate adapter and API surface.
				key = strings.TrimSpace(config.Secret("stripe_secret_key", config.Value("stripe_secret_key", "")))
			}
		}
		if key == "" {
			return cardpool.NewProviderError(providerName, "authorize", cardpool.CategoryInvalidRequest, false, false, errors.New("Stripe Issuing secret key 未配置"))
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("User-Agent", "auto-recharge-platform/1")
		return nil
	}))
}

func (p *Provider) HealthCheck(ctx context.Context) (cardpool.ProviderHealth, error) {
	health := cardpool.ProviderHealth{Provider: providerName, Status: "disabled"}
	if !p.enabled() {
		return health, nil
	}
	client, err := p.client(p.Config)
	if err != nil {
		return cardpool.ProviderHealth{Provider: providerName, Status: "unavailable"}, err
	}
	limit := 1
	response, requestErr := client.ListIssuingCards(ctx, &api.ListIssuingCardsParams{Limit: &limit})
	if requestErr != nil {
		return cardpool.ProviderHealth{Provider: providerName, Status: "unavailable"}, shared.RequestError(providerName, "health", requestErr)
	}
	var remote api.IssuingCardList
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "health", response, &remote); decodeErr != nil {
		return cardpool.ProviderHealth{Provider: providerName, Status: "unavailable"}, decodeErr
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
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("Stripe Issuing provider is disabled"))
	}
	cardholder := p.secret("stripe_issuing_cardholder_id", p.value("stripe_issuing_cardholder_id", ""))
	if cardholder == "" {
		cardholder = strings.TrimSpace(request.BusinessAccountID)
	}
	if cardholder == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("Stripe Issuing cardholder ID 未配置"))
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		key = uuid.NewString()
	}
	currency := strings.ToLower(firstNonEmpty(request.Currency, p.value("stripe_issuing_currency", "USD")))
	typeValue := api.Virtual
	status := api.CreateCardRequestStatusActive
	metadata := copyMetadata(request.Metadata)
	body := api.CreateCardRequest{Cardholder: cardholder, Currency: currency, Type: typeValue, Status: &status, Metadata: &metadata}
	client, err := p.client(p.Config)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	response, requestErr := client.CreateIssuingCardWithFormdataBody(ctx, body, func(_ context.Context, request *http.Request) error {
		request.Header.Set("Idempotency-Key", key)
		return nil
	})
	if requestErr != nil {
		return cardpool.PaymentCard{}, shared.RequestError(providerName, "create_card", requestErr)
	}
	var remote api.IssuingCard
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "create_card", response, &remote); decodeErr != nil {
		return cardpool.PaymentCard{}, decodeErr
	}
	return mapCard(remote, request), nil
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
	}
	client, err := p.client(p.Config)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	response, requestErr := client.GetIssuingCard(ctx, providerCardID)
	if requestErr != nil {
		return cardpool.PaymentCard{}, shared.RequestError(providerName, "get_card", requestErr)
	}
	var remote api.IssuingCard
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "get_card", response, &remote); decodeErr != nil {
		return cardpool.PaymentCard{}, decodeErr
	}
	card := mapCard(remote, cardpool.CreateCardRequest{})
	if card.ProviderCardID == "" {
		card.ProviderCardID = providerCardID
	}
	return card, nil
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	client, err := p.client(p.Config)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	response, requestErr := client.GetIssuingCard(ctx, strings.TrimSpace(providerCardID), func(_ context.Context, request *http.Request) error {
		query := request.URL.Query()
		query.Add("expand[]", "number")
		query.Add("expand[]", "cvc")
		request.URL.RawQuery = query.Encode()
		return nil
	})
	if requestErr != nil {
		return cardpool.SensitiveCardDetails{}, shared.RequestError(providerName, "get_sensitive_card_details", requestErr)
	}
	var remote api.IssuingCard
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "get_sensitive_card_details", response, &remote); decodeErr != nil {
		return cardpool.SensitiveCardDetails{}, decodeErr
	}
	result := cardpool.SensitiveCardDetails{CardholderName: pointerString(remote.Cardholder, ""), CardNumber: pointerString(remote.Number, ""), CVC: pointerString(remote.Cvc, "")}
	result.ExpiryMonth = pointerInt(remote.ExpMonth)
	if result.ExpiryMonth == 0 {
		result.ExpiryMonth = additionalInt(remote.AdditionalProperties, "expiry_month")
	}
	result.ExpiryYear = pointerInt(remote.ExpYear)
	if result.ExpiryYear == 0 {
		result.ExpiryYear = additionalInt(remote.AdditionalProperties, "expiry_year")
	}
	if result.CardNumber == "" || result.CVC == "" || result.ExpiryMonth == 0 || result.ExpiryYear == 0 || strings.Contains(result.CardNumber, "*") {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("Stripe Issuing sensitive response is incomplete"))
	}
	return result, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.UpdateCardRequestStatusInactive)
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.UpdateCardRequestStatusActive)
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, api.UpdateCardRequestStatusCanceled)
}

func (p *Provider) updateStatus(ctx context.Context, providerCardID string, status api.UpdateCardRequestStatus) error {
	client, err := p.client(p.Config)
	if err != nil {
		return err
	}
	response, requestErr := client.UpdateIssuingCardWithFormdataBody(ctx, strings.TrimSpace(providerCardID), api.UpdateCardRequest{Status: &status})
	if requestErr != nil {
		return shared.RequestError(providerName, "update_card", requestErr)
	}
	_, err = shared.DecodeJSONResponse(providerName, "update_card", response, &api.IssuingCard{})
	return err
}

func (p *Provider) UpdateLimits(context.Context, string, cardpool.CardLimits) error {
	return cardpool.UnsupportedCapability(providerName, cardpool.CapabilityTransactionLimit)
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	client, err := p.client(p.Config)
	if err != nil {
		return nil, err
	}
	cardID := strings.TrimSpace(providerCardID)
	limit := 100
	response, requestErr := client.ListIssuingTransactions(ctx, &api.ListIssuingTransactionsParams{Card: &cardID, Limit: &limit})
	if requestErr != nil {
		return nil, shared.RequestError(providerName, "list_transactions", requestErr)
	}
	var remote api.IssuingTransactionList
	if _, decodeErr := shared.DecodeJSONResponse(providerName, "list_transactions", response, &remote); decodeErr != nil {
		return nil, decodeErr
	}
	result := make([]cardpool.CardTransaction, 0)
	if remote.Data == nil {
		return result, nil
	}
	for _, item := range *remote.Data {
		transaction := cardpool.CardTransaction{ProviderCardID: cardID, Amount: float64(pointerInt(item.Amount)) / 100, Currency: pointerString(item.Currency, ""), Status: pointerString(item.Status, ""), Type: pointerString(item.Type, "card_payment"), FailureCode: pointerString(item.FailureCode, "")}
		transaction.ProviderTransactionID = pointerString(item.Id, "")
		transaction.MerchantName = merchantName(item.MerchantData)
		if item.Created != nil {
			occurred := time.Unix(int64(*item.Created), 0).UTC()
			transaction.OccurredAt = &occurred
		}
		result = append(result, transaction)
	}
	return result, nil
}

func mapCard(remote api.IssuingCard, request cardpool.CreateCardRequest) cardpool.PaymentCard {
	providerStatus := pointerString(remote.Status, "")
	cardType := pointerString(remote.Type, "virtual")
	return cardpool.PaymentCard{Provider: providerName, ProviderCardID: pointerString(remote.Id, ""), Last4: pointerString(remote.Last4, ""),
		CardholderName: pointerString(remote.Cardholder, ""), CardType: cardType, UsageType: request.UsageType,
		Currency: firstNonEmpty(request.Currency, pointerString(remote.Currency, "")), Status: mapStatus(providerStatus), ProviderStatus: providerStatus}
}

func mapStatus(status string) cardpool.InternalCardStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return cardpool.CardActive
	case "inactive":
		return cardpool.CardFrozen
	case "canceled", "cancelled":
		return cardpool.CardCancelled
	case "pending", "creating":
		return cardpool.CardCreating
	default:
		return cardpool.CardFailed
	}
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

func pointerString(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func pointerInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func additionalInt(values map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch item := value.(type) {
		case float64:
			return int(item)
		case int:
			return item
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(item)); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func merchantName(values *map[string]interface{}) string {
	if values == nil {
		return ""
	}
	for _, key := range []string{"name", "merchant_name", "merchantName"} {
		if value, ok := (*values)[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
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

var _ cardpool.CardProvider = (*Provider)(nil)
