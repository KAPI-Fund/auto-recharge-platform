package photonpay

import (
	"context"
	"crypto"
	"crypto/md5" // #nosec G501 -- PhotonPay's documented signing contract is MD5withRSA.
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
	api "github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/photonpay/generated"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

const providerName = "PHOTONPAY"

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
		// PhotonPay's VCC API does not expose a native single-use flag. The
		// normalized service lifecycle enforces ONE_TIME semantics instead.
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
		{p.secret("photonpay_app_id", p.value("photonpay_app_id", "")), "PhotonPay App ID"},
		{p.secret("photonpay_app_secret", p.value("photonpay_app_secret", "")), "PhotonPay App Secret"},
		{p.secret("photonpay_private_key", p.value("photonpay_private_key", "")), "PhotonPay RSA private key"},
		{p.value("photonpay_card_bin", ""), "PhotonPay card BIN"},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.value) == "" {
			return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%s 未配置", check.name))
		}
	}
	if strings.TrimSpace(p.base()) == "" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("PhotonPay Base URL 未配置"))
	}
	formFactor := strings.ToLower(p.value("photonpay_card_form_factor", "virtual_card"))
	if formFactor != "virtual_card" && formFactor != "physical_card" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("PhotonPay card form factor %q 无效", formFactor))
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
	return configBool(p.value("card_provider_photonpay_enabled", "0"), false)
}

func (p *Provider) base() string {
	return strings.TrimRight(p.value("photonpay_base_url", "https://x-api.sandbox.photontech.cc"), "/")
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
	if strings.HasSuffix(request.URL.Path, "/oauth2/token/accessToken") {
		appID := p.secret("photonpay_app_id", p.value("photonpay_app_id", ""))
		appSecret := p.secret("photonpay_app_secret", p.value("photonpay_app_secret", ""))
		if appID == "" || appSecret == "" {
			return cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryInvalidRequest, false, false, errors.New("PhotonPay App ID/App Secret 未配置"))
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(appID + "/" + appSecret))
		request.Header.Set("Authorization", "Basic "+encoded)
		request.Header.Set("Accept", "application/json")
		return nil
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	request.Header.Set("X-PD-TOKEN", token)
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

	client, err := p.clientForBase()
	if err != nil {
		return "", err
	}
	response, requestErr := client.GenerateAccessToken(ctx)
	if requestErr != nil {
		return "", shared.RequestError(providerName, "authenticate", requestErr)
	}
	payload, decodeErr := shared.DecodeJSONObjectResponse(providerName, "authenticate", response)
	if decodeErr != nil {
		return "", decodeErr
	}
	if err := photonBusinessError(payload, "authenticate"); err != nil {
		return "", err
	}
	data := shared.ObjectField(payload, "data")
	token := shared.StringField(data, "token", "access_token")
	if token == "" {
		token = shared.StringField(payload, "token", "access_token")
	}
	if token == "" {
		return "", cardpool.NewProviderError(providerName, "authenticate", cardpool.CategoryTechnicalFailure, true, true, errors.New("PhotonPay authentication response has no token"))
	}
	expiresAt := time.Now().Add(30 * time.Minute)
	if raw := shared.StringField(data, "expiresIn", "expires_in", "expiresAt", "expires_at"); raw != "" {
		if parsed, ok := parsePhotonExpiryValue(raw); ok {
			expiresAt = parsed
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
		return nil, cardpool.NewProviderError(providerName, "request", cardpool.CategoryProviderUnavailable, false, true, errors.New("PhotonPay provider is disabled"))
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
	pageIndex, pageSize := int64(1), int64(1)
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListCards(ctx, &api.ListCardsParams{PageIndex: &pageIndex, PageSize: &pageSize})
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
	if err := photonBusinessError(payload, "health"); err != nil {
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
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("PhotonPay provider is disabled"))
	}
	requestID := strings.TrimSpace(request.IdempotencyKey)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	cardBin := p.value("photonpay_card_bin", "")
	if cardBin == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("PhotonPay card BIN 未配置"))
	}
	currency := strings.ToUpper(firstNonEmpty(request.Currency, p.value("photonpay_primary_currency", "USD")))
	cardType := p.value("photonpay_card_type", "recharge")
	formFactor := strings.ToLower(p.value("photonpay_card_form_factor", "virtual_card"))
	cardScheme := p.value("photonpay_card_scheme", "Discover")
	limitType := strings.ToLower(p.value("photonpay_transaction_limit_type", "unlimited"))
	if limitType != "limited" && limitType != "unlimited" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("PhotonPay transaction limit type %q 无效", limitType))
	}
	body := api.OpenCardRequest{
		RequestId: requestID, CardBin: cardBin, CardCurrency: currency, CardType: cardType,
		CardFormFactor: stringPointer(formFactor), CardScheme: stringPointer(cardScheme),
		TransactionLimitType: stringPointer(limitType),
	}
	if value := p.secret("photonpay_cardholder_id", p.value("photonpay_cardholder_id", "")); value != "" {
		body.CardholderId = &value
	}
	if value := p.value("photonpay_account_id", ""); value != "" {
		body.AccountId = &value
	}
	if value := p.value("photonpay_member_id", ""); value != "" {
		body.MemberId = &value
	}
	if value := p.value("photonpay_matrix_account", ""); value != "" {
		body.MatrixAccount = &value
	}
	if value := p.value("photonpay_nickname", ""); value != "" {
		body.Nickname = &value
	}
	if request.Amount > 0 {
		amount := float32(request.Amount)
		body.RechargeAmount = &amount
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, err)
	}
	signature, err := p.sign(encoded)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.OpenCard(ctx, &api.OpenCardParams{XPDSIGN: signature, XPDTOKEN: token}, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "create_card", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := photonBusinessError(payload, "create_card"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	cardData := shared.ObjectField(shared.ObjectField(payload, "data"), "cardDetail", "card")
	if cardData == nil {
		cardData = shared.ObjectField(payload, "data")
	}
	card := mapCard(cardData, request)
	if card.ProviderCardID == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryTechnicalFailure, true, true, errors.New("PhotonPay create response has no card id"))
	}
	return card, nil
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	detail, err := p.getCardDetail(ctx, providerCardID)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	card := mapCard(detail, cardpool.CreateCardRequest{})
	if card.ProviderCardID == "" {
		card.ProviderCardID = strings.TrimSpace(providerCardID)
	}
	return card, nil
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	detail, err := p.getCardDetail(ctx, providerCardID)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	cardID := firstNonEmpty(shared.StringField(detail, "cardId", "card_id"), strings.TrimSpace(providerCardID))
	cvvData, err := p.getCvv(ctx, cardID)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	cardNumber := shared.StringField(detail, "cardNo", "card_number", "number")
	cvc := shared.StringField(cvvData, "cvv", "cvc")
	expiry := firstNonEmpty(shared.StringField(detail, "expirationDate", "expiry", "expiryDate"), shared.StringField(cvvData, "expirationDate", "expiry", "expiryDate"))
	month, year, ok := parsePhotonMonthYear(expiry)
	if !ok {
		month, year = shared.IntField(detail, "expiryMonth", "expireMonth"), shared.IntField(detail, "expiryYear", "expireYear")
	}
	if cardNumber == "" || strings.Contains(cardNumber, "*") || cvc == "" || month < 1 || month > 12 || year < 1 {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("PhotonPay sensitive response is incomplete"))
	}
	return cardpool.SensitiveCardDetails{
		CardNumber: cardNumber, ExpiryMonth: month, ExpiryYear: year, CVC: cvc,
		CardholderName: firstNonEmpty(shared.StringField(detail, "cardholderNameAbbreviation", "name"), strings.TrimSpace(shared.StringField(detail, "firstName")+" "+shared.StringField(detail, "lastName"))),
	}, nil
}

func (p *Provider) getCardDetail(ctx context.Context, providerCardID string) (map[string]any, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return nil, cardpool.ErrCardNotFound
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetCardDetail(ctx, &api.GetCardDetailParams{CardId: providerCardID, XPDTOKEN: token})
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_card", response)
	if err != nil {
		return nil, err
	}
	if err := photonBusinessError(payload, "get_card"); err != nil {
		return nil, err
	}
	data := shared.ObjectField(payload, "data")
	if data == nil {
		return nil, cardpool.NewProviderError(providerName, "get_card", cardpool.CategoryTechnicalFailure, true, true, errors.New("PhotonPay card detail response is empty"))
	}
	// PhotonPay may wrap the actual VCC object in data.cardDetail (or data.card)
	// depending on the endpoint/version. Normalize that envelope here so the
	// rest of the adapter never has to branch on a provider response shape.
	if nested := shared.ObjectField(data, "cardDetail", "card", "cardInfo", "card_info", "object"); nested != nil {
		return nested, nil
	}
	return data, nil
}

func (p *Provider) getCvv(ctx context.Context, providerCardID string) (map[string]any, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return nil, cardpool.ErrCardNotFound
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.GetCvv(ctx, &api.GetCvvParams{CardId: providerCardID, XPDTOKEN: token})
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_cvv", response)
	if err != nil {
		return nil, err
	}
	if err := photonBusinessError(payload, "get_cvv"); err != nil {
		return nil, err
	}
	data := shared.ObjectField(payload, "data")
	if data == nil {
		return nil, cardpool.NewProviderError(providerName, "get_cvv", cardpool.CategoryTechnicalFailure, true, true, errors.New("PhotonPay CVV response is empty"))
	}
	return data, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateFreezeState(ctx, providerCardID, "freeze")
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateFreezeState(ctx, providerCardID, "unfreeze")
}

func (p *Provider) updateFreezeState(ctx context.Context, providerCardID, status string) error {
	requestID := uuid.NewString()
	body := api.FreezeCardRequest{CardId: strings.TrimSpace(providerCardID), RequestId: requestID, Status: status}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	signature, err := p.sign(encoded)
	if err != nil {
		return err
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.FreezeCard(ctx, &api.FreezeCardParams{XPDSIGN: signature, XPDTOKEN: token}, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "update_card_status", response)
	if err != nil {
		return err
	}
	return photonBusinessError(payload, "update_card_status")
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	body := api.CancelCardRequest{CardId: strings.TrimSpace(providerCardID)}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	signature, err := p.sign(encoded)
	if err != nil {
		return err
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.CancelCard(ctx, &api.CancelCardParams{XPDSIGN: signature, XPDTOKEN: token}, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "cancel_card", response)
	if err != nil {
		return err
	}
	return photonBusinessError(payload, "cancel_card")
}

func (p *Provider) UpdateLimits(ctx context.Context, providerCardID string, limits cardpool.CardLimits) error {
	body := api.UpdateCardRequest{CardId: strings.TrimSpace(providerCardID), RequestId: uuid.NewString()}
	if limits.PerTransaction > 0 {
		value := float32(limits.PerTransaction)
		body.MaxOnPercent = &value
	}
	if limits.Daily > 0 {
		value := float32(limits.Daily)
		body.MaxOnDaily = &value
	}
	if limits.Monthly > 0 {
		value := float32(limits.Monthly)
		body.MaxOnMonthly = &value
	}
	if limits.AllTime > 0 {
		value := float32(limits.AllTime)
		body.TransactionLimit = &value
		changeType := "increase"
		body.TransactionLimitChangeType = &changeType
	}
	transactionLimitType := "unlimited"
	if limits.PerTransaction > 0 || limits.Daily > 0 || limits.Monthly > 0 || limits.AllTime > 0 {
		transactionLimitType = "limited"
	}
	body.TransactionLimitType = &transactionLimitType
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	signature, err := p.sign(encoded)
	if err != nil {
		return err
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.UpdateCard(ctx, &api.UpdateCardParams{XPDSIGN: signature, XPDTOKEN: token}, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "update_limits", response)
	if err != nil {
		return err
	}
	return photonBusinessError(payload, "update_limits")
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	cardID := strings.TrimSpace(providerCardID)
	if cardID == "" {
		return nil, cardpool.ErrCardNotFound
	}
	pageIndex, pageSize := int64(1), int64(100)
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.ListTradeOrders(ctx, &api.ListTradeOrdersParams{PageIndex: &pageIndex, PageSize: &pageSize, CardId: &cardID, XPDTOKEN: token})
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "list_transactions", response)
	if err != nil {
		return nil, err
	}
	if err := photonBusinessError(payload, "list_transactions"); err != nil {
		return nil, err
	}
	items := shared.ArrayField(payload, "data")
	if data := shared.ObjectField(payload, "data"); data != nil {
		items = shared.ArrayField(data, "items", "records", "list")
	}
	result := make([]cardpool.CardTransaction, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		transaction := cardpool.CardTransaction{
			ProviderTransactionID: shared.StringField(object, "transactionId", "transaction_id", "tradeOrderId", "trade_order_id", "orderId", "order_id", "id"),
			ProviderCardID:        firstNonEmpty(shared.StringField(object, "cardId", "card_id"), cardID),
			Amount:                shared.FloatField(object, "transactionAmount", "transaction_amount", "amount", "tradeAmount", "trade_amount"),
			Currency:              strings.ToUpper(shared.StringField(object, "transactionCurrency", "transaction_currency", "currency")),
			Status:                strings.ToUpper(shared.StringField(object, "status", "transactionStatus", "transaction_status")),
			Type:                  firstNonEmpty(shared.StringField(object, "transactionType", "transaction_type", "type"), "card_payment"),
			MerchantName:          shared.StringField(object, "merchantNameLocation", "merchantName", "merchant_name"),
			FailureCode:           shared.StringField(object, "code", "failureCode", "failure_code", "reasonCode", "reason_code"),
		}
		if merchant := shared.ObjectField(object, "merchant", "merchantData", "merchant_data", "merchantInfo", "merchant_info"); merchant != nil {
			transaction.MerchantName = firstNonEmpty(transaction.MerchantName, shared.StringField(merchant, "name", "merchantName", "merchant_name", "displayName", "display_name"))
			transaction.FailureCode = firstNonEmpty(transaction.FailureCode, shared.StringField(merchant, "reasonCode", "reason_code", "failureCode", "failure_code"))
		}
		if occurred, ok := parsePhotonTime(firstNonEmpty(shared.StringField(object, "txnDate", "txn_date", "transactionAt", "transaction_at"), shared.StringField(object, "createdAt", "createAt", "created_at", "create_at"))); ok {
			transaction.OccurredAt = &occurred
		}
		if transaction.ProviderTransactionID != "" {
			result = append(result, transaction)
		}
	}
	return result, nil
}

func mapCard(data map[string]any, request cardpool.CreateCardRequest) cardpool.PaymentCard {
	providerCardID := shared.StringField(data, "cardId", "card_id", "id")
	providerStatus := shared.StringField(data, "cardStatus", "card_status", "status")
	masked := shared.StringField(data, "maskCardNo", "maskedCardNo", "cardNo", "card_number")
	last4 := lastFour(masked)
	if last4 == "" {
		last4 = lastFour(shared.StringField(data, "last4", "lastFour", "last_four"))
	}
	name := shared.StringField(data, "cardholderNameAbbreviation", "name", "cardholderName")
	if name == "" {
		name = strings.TrimSpace(shared.StringField(data, "firstName") + " " + shared.StringField(data, "lastName"))
	}
	currency := strings.ToUpper(firstNonEmpty(request.Currency, shared.StringField(data, "cardCurrency", "currency")))
	cardType := firstNonEmpty(shared.StringField(data, "cardFormFactor"), "virtual_card")
	return cardpool.PaymentCard{
		Provider: providerName, ProviderCardID: providerCardID, Last4: last4, CardholderName: name,
		CardType: cardType, UsageType: request.UsageType, Currency: currency,
		Status: mapStatus(providerStatus), ProviderStatus: providerStatus,
	}
}

func mapStatus(status string) cardpool.InternalCardStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "normal", "active":
		return cardpool.CardActive
	case "freezing", "frozen", "risk_frozen", "system_frozen", "lost", "stolen", "pin_lost":
		return cardpool.CardFrozen
	case "pending", "pending_recharge", "unactivated", "unfreezing", "canceling", "renewing", "replacing", "processing":
		return cardpool.CardCreating
	case "expired", "cancelled", "canceled":
		return cardpool.CardCancelled
	default:
		return cardpool.CardFailed
	}
}

func photonBusinessError(payload map[string]any, operation string) error {
	if success, ok := payload["success"].(bool); ok && !success {
		return shared.BusinessResponseError(providerName, operation, shared.StringField(payload, "code", "errorCode", "error_code"), shared.StringField(payload, "msg", "message", "error"))
	}
	code := shared.StringField(payload, "code", "errorCode", "error_code")
	if code == "" || code == "0000" || code == "0" || strings.EqualFold(code, "success") {
		return nil
	}
	return shared.BusinessResponseError(providerName, operation, code, shared.StringField(payload, "msg", "message", "error"))
}

func (p *Provider) sign(payload []byte) (string, error) {
	key, err := p.loadPrivateKey()
	if err != nil {
		return "", err
	}
	digest := md5.Sum(payload) // #nosec G401 -- required by PhotonPay MD5withRSA.
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.MD5, digest[:])
	if err != nil {
		return "", cardpool.NewProviderError(providerName, "sign_request", cardpool.CategoryInvalidRequest, false, false, err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func (p *Provider) loadPrivateKey() (*rsa.PrivateKey, error) {
	value := p.secret("photonpay_private_key", p.value("photonpay_private_key", ""))
	if value == "" {
		return nil, cardpool.NewProviderError(providerName, "sign_request", cardpool.CategoryInvalidRequest, false, false, errors.New("PhotonPay RSA private key 未配置"))
	}
	p.keyMu.Lock()
	defer p.keyMu.Unlock()
	if p.privateKey != nil && p.keyFingerprint == value {
		return p.privateKey, nil
	}
	key, err := parsePrivateKey(value)
	if err != nil {
		return nil, cardpool.NewProviderError(providerName, "sign_request", cardpool.CategoryInvalidRequest, false, false, err)
	}
	p.keyFingerprint, p.privateKey = value, key
	return key, nil
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	value = strings.ReplaceAll(value, `\n`, "\n")
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
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value)); err == nil {
		if key, parseErr := x509.ParsePKCS8PrivateKey(decoded); parseErr == nil {
			if rsaKey, ok := key.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
		}
		if key, parseErr := x509.ParsePKCS1PrivateKey(decoded); parseErr == nil {
			return key, nil
		}
	}
	return nil, errors.New("PhotonPay RSA private key 格式无效，需 PKCS#8 或 PKCS#1 PEM")
}

func parsePhotonExpiryValue(value string) (time.Time, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	if parsed > 1_000_000_000_000 {
		return time.UnixMilli(parsed), true
	}
	if parsed > 1_000_000_000 {
		return time.Unix(parsed, 0), true
	}
	return time.Now().Add(time.Duration(parsed) * time.Second), true
}

func parsePhotonMonthYear(value string) (int, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
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

func parsePhotonTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix > 0 {
		if unix > 1_000_000_000_000 {
			return time.UnixMilli(unix), true
		}
		if unix > 1_000_000_000 {
			return time.Unix(unix, 0), true
		}
	}
	return time.Time{}, false
}

func lastFour(value string) string {
	digits := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		if value[index] >= '0' && value[index] <= '9' {
			digits = append(digits, value[index])
		}
	}
	if len(digits) < 4 {
		return ""
	}
	return string(digits[len(digits)-4:])
}

func stringPointer(value string) *string { return &value }

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
