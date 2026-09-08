package kimoox

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	api "github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/kimoox/generated"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

const providerName = "KIMOOX"

var requestNoUnsafeCharacters = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// Provider is the Kimoox VCC adapter. Kimoox's public documentation does not
// publish a downloadable OpenAPI document, so the generated client is kept
// deliberately envelope-oriented. All provider-specific request and response
// handling remains in this package and never leaks into PaymentService.
type Provider struct {
	Config     cardpool.ConfigReader
	HTTPClient *http.Client

	clientMu sync.Mutex
	client   *api.Client
	baseURL  string
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
		// Kimoox does not document a native single-use-card flag. ONE_TIME is
		// enforced by the normalized allocation lifecycle instead.
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
		{p.secret("kimoox_api_key", p.value("kimoox_api_key", "")), "Kimoox API Key"},
		{p.secret("kimoox_api_secret", p.value("kimoox_api_secret", "")), "Kimoox API Secret"},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.value) == "" {
			return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%s 未配置", check.name))
		}
	}
	base := p.base()
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox Base URL 必须是有效的 HTTP/HTTPS 地址"))
	}
	if _, err := p.binIDs(); err != nil {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, err)
	}
	cardType := strings.ToUpper(p.value("kimoox_card_type", "PREPAID"))
	if cardType != "PREPAID" && cardType != "BUDGET" {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox 卡类型仅支持 PREPAID 或 BUDGET"))
	}
	if cardType == "BUDGET" {
		for _, item := range []struct {
			value string
			name  string
		}{
			{p.value("kimoox_card_group_id", ""), "Kimoox Card Group ID"},
			{p.value("kimoox_budget_id", ""), "Kimoox Budget ID"},
		} {
			if strings.TrimSpace(item.value) == "" {
				return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%s 未配置", item.name))
			}
			if _, err := strconv.ParseInt(strings.TrimSpace(item.value), 10, 64); err != nil {
				return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%s 必须是整数", item.name))
			}
		}
	}
	if attempts, err := p.pollAttempts(); err != nil || attempts < 1 || attempts > 300 {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox 开卡轮询次数必须是 1-300"))
	}
	if interval, err := p.pollInterval(); err != nil || interval < 0 || interval > 5*time.Minute {
		return cardpool.NewProviderError(providerName, "validate_config", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox 开卡轮询间隔必须是 0-300 秒"))
	}
	return nil
}

func (p *Provider) enabled() bool {
	return configBool(p.value("card_provider_kimoox_enabled", "0"), false)
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
	return strings.TrimRight(p.value("kimoox_base_url", "https://card.kimoox.com"), "/")
}

func (p *Provider) clientForBase() (*api.Client, error) {
	base := p.base()
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.client != nil && p.baseURL == base {
		return p.client, nil
	}
	client, err := api.NewClient(base, api.WithHTTPClient(p.HTTPClient), api.WithRequestEditorFn(p.signRequest))
	if err != nil {
		return nil, cardpool.NewProviderError(providerName, "client", cardpool.CategoryInvalidRequest, false, false, err)
	}
	p.client, p.baseURL = client, base
	return client, nil
}

// signRequest signs the exact raw JSON bytes produced by the generated client.
// Reading and restoring Body is important: the same bytes must be sent after
// the signature is calculated.
func (p *Provider) signRequest(_ context.Context, request *http.Request) error {
	if request == nil {
		return errors.New("nil Kimoox request")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return cardpool.NewProviderError(providerName, "sign_request", cardpool.CategoryTechnicalFailure, true, true, err)
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	canonical := strings.Join([]string{request.Method, request.URL.Path, timestamp, nonce, bodyHashHex}, "\n")
	digest := hmac.New(sha256.New, []byte(p.secret("kimoox_api_secret", p.value("kimoox_api_secret", ""))))
	_, _ = digest.Write([]byte(canonical))

	request.Header.Set("X-VCC-API-KEY", p.secret("kimoox_api_key", p.value("kimoox_api_key", "")))
	request.Header.Set("X-VCC-TIMESTAMP", timestamp)
	request.Header.Set("X-VCC-NONCE", nonce)
	request.Header.Set("X-VCC-SIGNATURE", hex.EncodeToString(digest.Sum(nil)))
	if strings.TrimSpace(request.Header.Get("X-VCC-REQUEST-ID")) == "" {
		request.Header.Set("X-VCC-REQUEST-ID", "kc_"+uuid.NewString())
	}
	return nil
}

func (p *Provider) authenticatedRequest(ctx context.Context, call func(*api.Client) (*http.Response, error)) (*http.Response, error) {
	return p.doAuthenticatedRequest(ctx, true, call)
}

func (p *Provider) setupRequest(ctx context.Context, call func(*api.Client) (*http.Response, error)) (*http.Response, error) {
	return p.doAuthenticatedRequest(ctx, false, call)
}

func (p *Provider) doAuthenticatedRequest(ctx context.Context, requireEnabled bool, call func(*api.Client) (*http.Response, error)) (*http.Response, error) {
	if requireEnabled && !p.enabled() {
		return nil, cardpool.NewProviderError(providerName, "request", cardpool.CategoryProviderUnavailable, false, true, errors.New("Kimoox provider is disabled"))
	}
	if strings.TrimSpace(p.secret("kimoox_api_key", p.value("kimoox_api_key", ""))) == "" || strings.TrimSpace(p.secret("kimoox_api_secret", p.value("kimoox_api_secret", ""))) == "" {
		return nil, cardpool.NewProviderError(providerName, "request", cardpool.CategoryInvalidRequest, false, false, errors.New("请先填写并保存 Kimoox API Key 和 API Secret"))
	}
	client, err := p.clientForBase()
	if err != nil {
		return nil, err
	}
	response, requestErr := call(client)
	if requestErr != nil {
		return nil, shared.RequestError(providerName, "request", requestErr)
	}
	return response, nil
}

func (p *Provider) HealthCheck(ctx context.Context) (cardpool.ProviderHealth, error) {
	health := cardpool.ProviderHealth{Provider: providerName, Status: "disabled"}
	if !p.enabled() {
		return health, nil
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxAccountBalance(ctx, api.KimooxAccountBalanceJSONRequestBody{})
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
	if err := kimooxBusinessError(payload, "health"); err != nil {
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
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("Kimoox provider is disabled"))
	}
	cardType := strings.ToUpper(p.value("kimoox_card_type", "PREPAID"))
	var rechargeAmount string
	var cardGroupID, budgetID int64
	if cardType == "PREPAID" {
		amount, amountErr := p.prepaidRechargeAmount(request.Amount)
		if amountErr != nil {
			return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, amountErr)
		}
		rechargeAmount = formatAmount(amount)
	} else {
		var groupErr, budgetErr error
		cardGroupID, groupErr = parseRequiredInt(p.value("kimoox_card_group_id", ""))
		budgetID, budgetErr = parseRequiredInt(p.value("kimoox_budget_id", ""))
		if groupErr != nil || budgetErr != nil {
			return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox BUDGET 卡需要有效的 Card Group ID 和 Budget ID"))
		}
	}
	binIDs, err := p.resolveCardBINIDs(ctx)
	if err != nil {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryInvalidRequest, false, false, err)
	}
	binID := selectBINID(binIDs, firstNonEmpty(request.IdempotencyKey, request.PaymentTaskID, request.BusinessAccountID))
	requestNo := kimooxRequestNo(request.IdempotencyKey)
	body := api.KimooxCardsApplyJSONRequestBody{
		"requestNo": requestNo,
		"cardType":  cardType,
		"cardBinId": binID,
		"cardCount": 1,
	}
	if holderID := parseOptionalInt(p.value("kimoox_cardholder_id", p.value("kimoox_holder_id", ""))); holderID != nil {
		body["holderId"] = *holderID
	}
	if cardType == "PREPAID" {
		body["rechargeAmount"] = rechargeAmount
	} else {
		body["cardGroupId"], body["budgetId"] = cardGroupID, budgetID
	}
	if request.Metadata != nil {
		if remark := strings.TrimSpace(request.Metadata["remark"]); remark != "" {
			body["remark"] = truncateRunes(remark, 40)
		}
	}

	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsApply(ctx, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "create_card", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := kimooxBusinessError(payload, "create_card"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	data := objectData(payload)
	taskID := firstNonEmpty(shared.StringField(data, "taskId", "task_id"), shared.StringField(payload, "taskId", "task_id"))
	batchNo := firstNonEmpty(shared.StringField(data, "batchNo", "batch_no"), shared.StringField(payload, "batchNo", "batch_no"))
	if taskID == "" && batchNo == "" {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "create_card", cardpool.CategoryTechnicalFailure, true, true, errors.New("Kimoox create response has no task identity"))
	}
	if err := p.waitForApply(ctx, taskID, batchNo, requestNo); err != nil {
		return cardpool.PaymentCard{}, err
	}
	return p.findAppliedCard(ctx, batchNo, request)
}

// ListCardBINs returns the BINs visible to the configured Kimoox account. The
// response is mapped to provider-neutral metadata for the admin UI.
func (p *Provider) ListCardBINs(ctx context.Context) ([]cardpool.CardBIN, error) {
	response, err := p.setupRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardBins(ctx, api.KimooxCardBinsJSONRequestBody{})
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "list_card_bins", response)
	if err != nil {
		return nil, err
	}
	if err := kimooxBusinessError(payload, "list_card_bins"); err != nil {
		return nil, err
	}
	data := objectData(payload)
	items := shared.ArrayField(data, "list", "bins", "items", "records")
	result := make([]cardpool.CardBIN, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := shared.StringField(object, "binId", "bin_id", "id")
		binNumber := shared.StringField(object, "bin", "cardBin", "card_bin")
		if id == "" {
			id = binNumber
		}
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, cardpool.CardBIN{
			ID:           id,
			BIN:          binNumber,
			Name:         firstNonEmpty(shared.StringField(object, "name", "binName", "bin_name"), binNumber),
			CardType:     shared.StringField(object, "cardType", "card_type"),
			Status:       shared.StringField(object, "status", "state"),
			MaxCardCount: int64(shared.IntField(object, "maxCardCount", "max_card_count", "maxOpenCardCount")),
			IssuedCount:  int64(shared.IntField(object, "issuedCardCount", "issued_card_count")),
		})
	}
	if len(result) == 0 {
		return nil, cardpool.NewProviderError(providerName, "list_card_bins", cardpool.CategoryTechnicalFailure, false, false, errors.New("Kimoox BIN 查询未返回可用 BIN"))
	}
	return result, nil
}

func (p *Provider) binIDs() ([]int64, error) {
	value := strings.TrimSpace(p.value("kimoox_card_bin_ids", ""))
	ids, err := parseKimooxBINIDs(value)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("Kimoox Card BIN 未配置或无效")
	}
	return ids, nil
}

func (p *Provider) resolveCardBINIDs(ctx context.Context) ([]int64, error) {
	configured, err := p.binIDs()
	if err != nil {
		return nil, err
	}
	bins, listErr := p.ListCardBINs(ctx)
	if listErr != nil {
		return nil, fmt.Errorf("解析 Kimoox Card BIN 失败: %w", listErr)
	}
	byID := make(map[int64]int64, len(bins))
	byBIN := make(map[int64]int64, len(bins))
	for _, bin := range bins {
		id, idErr := strconv.ParseInt(strings.TrimSpace(bin.ID), 10, 64)
		if idErr != nil || id <= 0 {
			continue
		}
		byID[id] = id
		if number, numberErr := strconv.ParseInt(strings.TrimSpace(bin.BIN), 10, 64); numberErr == nil && number > 0 {
			byBIN[number] = id
		}
	}
	resolved := make([]int64, 0, len(configured))
	seen := make(map[int64]struct{}, len(configured))
	for _, value := range configured {
		id, ok := byBIN[value]
		if !ok {
			id, ok = byID[value]
		}
		if !ok {
			return nil, fmt.Errorf("Kimoox Card BIN %d 不在可用列表中，请填写卡 BIN 号（例如 40024200）或读取后勾选", value)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		resolved = append(resolved, id)
	}
	if len(resolved) == 0 {
		return nil, errors.New("Kimoox Card BIN 未配置或无效")
	}
	return resolved, nil
}

func parseKimooxBINIDs(value string) ([]int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("Kimoox Card BIN 未配置或无效")
	}
	if strings.HasPrefix(value, "[") {
		var raw []any
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return nil, errors.New("Kimoox Card BIN 必须是整数列表")
		}
		parts := make([]string, 0, len(raw))
		for _, item := range raw {
			parts = append(parts, shared.StringField(map[string]any{"value": item}, "value"))
		}
		value = strings.Join(parts, ",")
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' || r == ' ' || r == '\t' })
	if len(parts) == 0 {
		return nil, errors.New("Kimoox Card BIN 未配置或无效")
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("Kimoox Card BIN 必须是大于 0 的整数列表")
		}
		if _, exists := seen[id]; exists {
			return nil, errors.New("Kimoox Card BIN 不能重复")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func selectBINID(ids []int64, stableKey string) int64 {
	if len(ids) == 1 {
		return ids[0]
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(stableKey)))
	var value uint64
	for _, item := range digest[:8] {
		value = value<<8 | uint64(item)
	}
	return ids[value%uint64(len(ids))]
}

func (p *Provider) waitForApply(ctx context.Context, taskID, batchNo, requestNo string) error {
	attempts, _ := p.pollAttempts()
	interval, _ := p.pollInterval()
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		body := api.KimooxCardsApplyStatusJSONRequestBody{}
		if value := strings.TrimSpace(taskID); value != "" {
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				body["taskId"] = parsed
			}
		}
		if strings.TrimSpace(batchNo) != "" {
			body["batchNo"] = batchNo
		}
		if strings.TrimSpace(requestNo) != "" {
			body["requestNo"] = requestNo
		}
		response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
			return client.KimooxCardsApplyStatus(ctx, body)
		})
		if err != nil {
			return err
		}
		payload, err := shared.DecodeJSONObjectResponse(providerName, "apply_status", response)
		if err != nil {
			return err
		}
		if err := kimooxBusinessError(payload, "apply_status"); err != nil {
			return err
		}
		data := objectData(payload)
		applyStatus := strings.ToUpper(firstNonEmpty(shared.StringField(data, "applyStatus", "apply_status"), shared.StringField(data, "taskStatus", "task_status")))
		taskStatus := strings.ToUpper(shared.StringField(data, "taskStatus", "task_status"))
		if applyStatus == "SUCCESS" || taskStatus == "SUCCESS" {
			return nil
		}
		if isKimooxFailureStatus(applyStatus) || isKimooxFailureStatus(taskStatus) {
			return cardpool.NewProviderError(providerName, "apply_status", cardpool.CategoryBusinessDecline, false, false, errors.New("Kimoox 开卡失败"))
		}
		if latest := firstNonEmpty(shared.StringField(data, "batchNo", "batch_no"), batchNo); latest != "" {
			batchNo = latest
		}
		if latest := firstNonEmpty(shared.StringField(data, "taskId", "task_id"), taskID); latest != "" {
			taskID = latest
		}
	}
	return cardpool.NewProviderError(providerName, "apply_status", cardpool.CategoryProviderUnavailable, true, true, errors.New("Kimoox 开卡状态轮询超时"))
}

func (p *Provider) findAppliedCard(ctx context.Context, batchNo string, request cardpool.CreateCardRequest) (cardpool.PaymentCard, error) {
	body := api.KimooxCardsQueryOpenJSONRequestBody{"pageNum": 1, "pageSize": 100}
	if strings.TrimSpace(batchNo) != "" {
		body["batchNo"] = batchNo
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsQueryOpen(ctx, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "query_cards", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := kimooxBusinessError(payload, "query_cards"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	data := objectData(payload)
	items := shared.ArrayField(data, "list", "cards", "items", "records")
	if len(items) == 0 {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "query_cards", cardpool.CategoryTechnicalFailure, true, true, errors.New("Kimoox 开卡成功但未查询到卡"))
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		card := mapCard(object, request)
		if card.ProviderCardID != "" {
			return card, nil
		}
	}
	return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "query_cards", cardpool.CategoryTechnicalFailure, true, true, errors.New("Kimoox 卡列表缺少 cardId"))
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
	}
	body := api.KimooxCardsQueryOpenJSONRequestBody{"pageNum": 1, "pageSize": 20, "cardId": providerCardID}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsQueryOpen(ctx, body)
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_card", response)
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	if err := kimooxBusinessError(payload, "get_card"); err != nil {
		return cardpool.PaymentCard{}, err
	}
	data := objectData(payload)
	items := shared.ArrayField(data, "list", "cards", "items", "records")
	for _, item := range items {
		object, ok := item.(map[string]any)
		if ok && strings.EqualFold(shared.StringField(object, "cardId", "card_id", "id"), providerCardID) {
			return mapCard(object, cardpool.CreateCardRequest{}), nil
		}
	}
	if cardID := shared.StringField(data, "cardId", "card_id", "id"); strings.EqualFold(cardID, providerCardID) {
		return mapCard(data, cardpool.CreateCardRequest{}), nil
	}
	return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.SensitiveCardDetails{}, cardpool.ErrCardNotFound
	}
	secret := p.secret("kimoox_webhook_secret", p.value("kimoox_webhook_secret", ""))
	if secret == "" {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox Webhook Secret 未配置，无法解密卡私密信息"))
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsPrivateInfoQuery(ctx, api.KimooxCardsPrivateInfoQueryJSONRequestBody{"cardId": providerCardID})
	})
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "get_sensitive_card_details", response)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	if err := kimooxBusinessError(payload, "get_sensitive_card_details"); err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	data := objectData(payload)
	key := kimooxSensitiveKey(secret)
	cardNumber, err := decryptKimooxField(key, shared.StringField(data, "cardNumberCiphertext", "card_number_ciphertext"))
	if err != nil {
		return cardpool.SensitiveCardDetails{}, sensitiveDecryptError(err)
	}
	expiry, err := decryptKimooxField(key, shared.StringField(data, "expiryDateCiphertext", "expiry_date_ciphertext"))
	if err != nil {
		return cardpool.SensitiveCardDetails{}, sensitiveDecryptError(err)
	}
	cvv, err := decryptKimooxField(key, shared.StringField(data, "cvvCiphertext", "cvv_ciphertext"))
	if err != nil {
		return cardpool.SensitiveCardDetails{}, sensitiveDecryptError(err)
	}
	month, year, ok := parseExpiry(expiry)
	result := cardpool.SensitiveCardDetails{CardNumber: strings.TrimSpace(cardNumber), CVC: strings.TrimSpace(cvv), ExpiryMonth: month, ExpiryYear: year}
	if !ok || result.CardNumber == "" || strings.Contains(result.CardNumber, "*") || result.CVC == "" {
		return cardpool.SensitiveCardDetails{}, cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("Kimoox sensitive response is incomplete"))
	}
	if metadata, metadataErr := p.GetCard(ctx, providerCardID); metadataErr == nil {
		result.CardholderName = metadata.CardholderName
	}
	return result, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.operateStatus(ctx, providerCardID, "FREEZE")
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.operateStatus(ctx, providerCardID, "UNFREEZE")
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	return p.operateStatus(ctx, providerCardID, "CANCEL")
}

func (p *Provider) operateStatus(ctx context.Context, providerCardID, operation string) error {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.ErrCardNotFound
	}
	body := api.KimooxCardsStatusOperateJSONRequestBody{
		"requestNo":     kimooxRequestNo(uuid.NewString()),
		"operationType": operation,
		"cardId":        providerCardID,
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsStatusOperate(ctx, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "status_operate", response)
	if err != nil {
		return err
	}
	return kimooxBusinessError(payload, "status_operate")
}

func (p *Provider) UpdateLimits(ctx context.Context, providerCardID string, limits cardpool.CardLimits) error {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return cardpool.ErrCardNotFound
	}
	body := api.KimooxCardsLimitAdjustJSONRequestBody{
		"requestNo": kimooxRequestNo(uuid.NewString()),
		"cardId":    providerCardID,
	}
	if limits.PerTransaction > 0 {
		body["perTransLimit"] = formatAmount(limits.PerTransaction)
	}
	cycleCount := 0
	if limits.Daily > 0 {
		body["dayLimit"] = formatAmount(limits.Daily)
		cycleCount++
	}
	if limits.Monthly > 0 {
		body["monthLimit"] = formatAmount(limits.Monthly)
		cycleCount++
	}
	if limits.AllTime > 0 {
		body["cardLifeCycleMode"] = "fixed_amount"
		body["cardLifeCycleLimit"] = formatAmount(limits.AllTime)
		cycleCount++
	} else {
		body["cardLifeCycleMode"] = "unlimited"
	}
	if cycleCount > 1 {
		return cardpool.NewProviderError(providerName, "update_limits", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox 日/月/生命周期限额同次只能设置一种"))
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardsLimitAdjust(ctx, body)
	})
	if err != nil {
		return err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "update_limits", response)
	if err != nil {
		return err
	}
	return kimooxBusinessError(payload, "update_limits")
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return nil, cardpool.ErrCardNotFound
	}
	card, err := p.GetCard(ctx, providerCardID)
	if err != nil {
		return nil, err
	}
	body := api.KimooxCardTransactionsJSONRequestBody{"pageNum": 1, "pageSize": 100}
	if card.Last4 != "" {
		body["cardNo"] = card.Last4
	}
	response, err := p.authenticatedRequest(ctx, func(client *api.Client) (*http.Response, error) {
		return client.KimooxCardTransactions(ctx, body)
	})
	if err != nil {
		return nil, err
	}
	payload, err := shared.DecodeJSONObjectResponse(providerName, "list_transactions", response)
	if err != nil {
		return nil, err
	}
	if err := kimooxBusinessError(payload, "list_transactions"); err != nil {
		return nil, err
	}
	data := objectData(payload)
	items := shared.ArrayField(data, "list", "transactions", "items", "records")
	result := make([]cardpool.CardTransaction, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok || !strings.EqualFold(firstNonEmpty(shared.StringField(object, "cardId", "card_id"), providerCardID), providerCardID) {
			continue
		}
		transaction := mapTransaction(object, providerCardID)
		if transaction.ProviderTransactionID != "" {
			result = append(result, transaction)
		}
	}
	return result, nil
}

func mapCard(data map[string]any, request cardpool.CreateCardRequest) cardpool.PaymentCard {
	providerStatus := shared.StringField(data, "cardStatus", "card_status", "status", "state")
	return cardpool.PaymentCard{
		Provider: providerName, ProviderCardID: shared.StringField(data, "cardId", "card_id", "id"),
		Last4:          lastFour(firstNonEmpty(shared.StringField(data, "last4", "lastFour", "last_four"), shared.StringField(data, "cardNoMask", "card_no_mask", "maskedCardNumber"))),
		CardholderName: firstNonEmpty(shared.StringField(data, "holderName", "holder_name", "cardholderName", "cardholder_name", "name")),
		CardType:       firstNonEmpty(shared.StringField(data, "cardType", "card_type"), "virtual"), UsageType: request.UsageType,
		Currency: strings.ToUpper(firstNonEmpty(request.Currency, shared.StringField(data, "currency", "cardCurrency", "card_currency"), "USD")),
		Status:   mapStatus(providerStatus), ProviderStatus: providerStatus,
	}
}

func mapTransaction(data map[string]any, cardID string) cardpool.CardTransaction {
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: shared.StringField(data, "transactionId", "transaction_id", "id"),
		ProviderCardID:        firstNonEmpty(shared.StringField(data, "cardId", "card_id"), cardID),
		Amount:                shared.FloatField(data, "actualAmount", "actual_amount", "usdAmount", "usd_amount", "originalAmount", "original_amount"),
		Currency:              strings.ToUpper(firstNonEmpty(shared.StringField(data, "actualCurrency", "actual_currency", "usdCurrency", "usd_currency"), shared.StringField(data, "originalCurrency", "original_currency"))),
		Status:                strings.ToUpper(shared.StringField(data, "transactionStatus", "transaction_status", "eventStatus", "event_status", "status")),
		Type:                  firstNonEmpty(shared.StringField(data, "transactionType", "transaction_type", "type"), "card_payment"),
		MerchantName:          shared.StringField(data, "merchantName", "merchant_name", "transactionInfo", "transaction_info"),
		FailureCode:           shared.StringField(data, "displayFailReason", "display_fail_reason", "failReasonCode", "fail_reason_code"),
	}
	if parsed, ok := parseKimooxTime(firstNonEmpty(shared.StringField(data, "transactionTime", "transaction_time", "txnTime", "txn_time", "eventTime", "event_time"))); ok {
		transaction.OccurredAt = &parsed
	}
	return transaction
}

func mapStatus(status string) cardpool.InternalCardStatus {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "1", "ACTIVE", "OPEN", "NORMAL", "ENABLED":
		return cardpool.CardActive
	case "FROZEN", "FREEZE", "INACTIVE", "BLOCKED", "SUSPENDED":
		return cardpool.CardFrozen
	case "OPENING", "PENDING", "PROCESSING", "SUBMITTED", "CREATING", "ISSUING", "CANCEL_REQUESTED":
		if status == "CANCEL_REQUESTED" {
			return cardpool.CardCreating
		}
		return cardpool.CardCreating
	case "CANCELLED", "CANCELED", "CLOSED", "EXPIRED", "DELETED":
		return cardpool.CardCancelled
	case "FAILED", "REJECTED", "DECLINED":
		return cardpool.CardFailed
	default:
		return cardpool.CardFailed
	}
}

func kimooxBusinessError(payload map[string]any, operation string) error {
	code := shared.StringField(payload, "code", "errorCode", "error_code", "status")
	message := shared.StringField(payload, "msg", "message", "error", "errorMessage", "error_message")
	if code == "" || code == "0" || code == "200" || strings.EqualFold(code, "success") || strings.EqualFold(code, "ok") {
		return nil
	}
	lower := strings.ToLower(message + " " + code)
	category, retryable, failover := cardpool.CategoryInvalidRequest, false, false
	switch {
	case strings.Contains(lower, "rate"), strings.Contains(lower, "too many"), code == "429":
		category, retryable = cardpool.CategoryRateLimit, true
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "temporar"), strings.Contains(lower, "unavailable"), strings.Contains(lower, "busy"), code == "500", code == "502", code == "503", code == "504":
		category, retryable, failover = cardpool.CategoryProviderUnavailable, true, true
	case strings.Contains(lower, "insufficient"), strings.Contains(lower, "balance"), strings.Contains(lower, "fund"):
		category = cardpool.CategoryInsufficientFunds
	case strings.Contains(lower, "compliance"), strings.Contains(lower, "risk"), strings.Contains(lower, "kyc"):
		category = cardpool.CategoryComplianceBlock
	case strings.Contains(lower, "declin"), strings.Contains(lower, "reject"), strings.Contains(lower, "failed"):
		category = cardpool.CategoryBusinessDecline
	case code == "401" || code == "403":
		category = cardpool.CategoryInvalidRequest
	}
	return cardpool.NewProviderError(providerName, operation, category, retryable, failover, errors.New("Kimoox provider returned an application error"))
}

func objectData(payload map[string]any) map[string]any {
	if data := shared.ObjectField(payload, "data", "result", "object"); data != nil {
		return data
	}
	return payload
}

func kimooxRequestNo(value string) string {
	value = requestNoUnsafeCharacters.ReplaceAllString(strings.TrimSpace(value), "-")
	value = strings.Trim(value, "-_")
	if len(value) < 4 {
		value = uuid.NewString()
	}
	// Kimoox requires a leading letter and at least one letter plus digit.
	return "KX" + shortHash(value)
}

func shortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:24]
}

func kimooxSensitiveKey(secret string) []byte {
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte("vcc-webhook-sensitive-v1"))
	return digest.Sum(nil)
}

func decryptKimooxField(key []byte, value string) (string, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return "", errors.New("empty encrypted field")
	}
	decoded, err := decodeBase64URL(encoded)
	if err != nil || len(decoded) < 12+16 {
		return "", errors.New("invalid Kimoox encrypted field")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, decoded[:12], decoded[12:], nil)
	if err != nil {
		return "", errors.New("Kimoox encrypted field authentication failed")
	}
	return decodedText(plain), nil
}

func decodedText(value []byte) string {
	text := strings.TrimSpace(string(value))
	var quoted string
	if json.Unmarshal(value, &quoted) == nil {
		return strings.TrimSpace(quoted)
	}
	return text
}

func sensitiveDecryptError(err error) error {
	return cardpool.NewProviderError(providerName, "get_sensitive_card_details", cardpool.CategoryTechnicalFailure, true, true, errors.New("Kimoox sensitive card details could not be decrypted"))
}

func parseExpiry(value string) (int, int, bool) {
	value = strings.TrimSpace(value)
	if len(value) == 4 {
		if month, err1 := strconv.Atoi(value[:2]); err1 == nil {
			if year, err2 := strconv.Atoi(value[2:]); err2 == nil && month >= 1 && month <= 12 {
				return month, normalizeYear(year), true
			}
		}
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '-' || r == '_' || r == '.' || r == ' ' })
	if len(parts) < 2 {
		return 0, 0, false
	}
	first, firstErr := strconv.Atoi(parts[0])
	second, secondErr := strconv.Atoi(parts[1])
	if firstErr != nil || secondErr != nil {
		return 0, 0, false
	}
	if first > 12 && second <= 12 {
		first, second = second, first
	}
	if first < 1 || first > 12 {
		return 0, 0, false
	}
	return first, normalizeYear(second), true
}

func normalizeYear(value int) int {
	if value < 100 {
		return 2000 + value
	}
	return value
}

func parseKimooxTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339} {
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

func (p *Provider) prepaidRechargeAmount(requested float64) (float64, error) {
	if requested > 0 {
		return requested, nil
	}
	return 0, errors.New("Kimoox PREPAID 开卡需要大于 0 的首充金额")
}

func parseOptionalInt(value string) *int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func parseRequiredInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid integer")
	}
	return parsed, nil
}

func (p *Provider) pollAttempts() (int, error) {
	value := strings.TrimSpace(p.value("kimoox_apply_poll_attempts", "30"))
	return strconv.Atoi(value)
}

func (p *Provider) pollInterval() (time.Duration, error) {
	value := strings.TrimSpace(p.value("kimoox_apply_poll_interval_seconds", "2"))
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
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

func formatAmount(value float64) string { return strconv.FormatFloat(value, 'f', 2, 64) }

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func lastFour(value string) string { return lastFourDigits(value) }

func lastFourDigits(value string) string {
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

func decodeBase64URL(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func isKimooxFailureStatus(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "FAILED", "FAIL", "REJECTED", "CANCELLED", "CANCELED", "ERROR":
		return true
	default:
		return false
	}
}

var _ cardpool.CardProvider = (*Provider)(nil)
var _ cardpool.ConfigValidator = (*Provider)(nil)
