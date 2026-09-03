package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	storeOrderPending = "pending"
	storeOrderPaid    = "paid"
	storeOrderFailed  = "failed"

	storePaymentProvider       = "stripe"
	storePaymentEventReceived  = "received"
	storePaymentEventIgnored   = "ignored"
	storePaymentEventFailed    = "failed"
	storePaymentEventProcessed = "processed"
	maxStripeWebhookBodySize   = 1 << 20
	stripeSignatureTolerance   = 5 * time.Minute
)

var errStoreProductSoldOut = errors.New("商品已售罄，补货中")

var stripeHTTPClient = &http.Client{Timeout: 30 * time.Second}

type stripeWebhookNonRetryableError struct {
	err error
}

func (e *stripeWebhookNonRetryableError) Error() string {
	if e == nil || e.err == nil {
		return "Stripe Webhook 业务校验失败"
	}
	return e.err.Error()
}

func (e *stripeWebhookNonRetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type stripePaymentValidationError struct {
	err error
}

func (e *stripePaymentValidationError) Error() string {
	if e == nil || e.err == nil {
		return "Stripe 支付业务校验失败"
	}
	return e.err.Error()
}

func (e *stripePaymentValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (s *Server) createStoreOrder(c *gin.Context) {
	var input struct {
		PlanCode         string `json:"planCode"`
		Email            string `json:"email"`
		PhoneCountryCode string `json:"phoneCountryCode"`
		PhoneNumber      string `json:"phoneNumber"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	planCode := strings.TrimSpace(input.PlanCode)
	if planCode == "" {
		planCode = "plus"
	}
	email, err := normalizeStoreEmail(input.Email)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	countryCode, phoneNumber, phoneE164, err := normalizeStorePhone(input.PhoneCountryCode, input.PhoneNumber)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var plan models.Plan
	if err := s.DB.Where("code = ? AND active = ?", planCode, true).First(&plan).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			fail(c, http.StatusNotFound, "套餐不存在")
			return
		}
		publicFail(c, http.StatusInternalServerError, "读取套餐失败")
		return
	}
	if !planHasSaleCapacity(plan) {
		fail(c, http.StatusConflict, errStoreProductSoldOut.Error())
		return
	}
	order := models.StoreOrder{
		ID: db.NewID("order"), TraceID: requestTraceID(c), PlanID: plan.ID,
		OrderNo: newStoreOrderNo(), Email: email, PhoneCountryCode: countryCode,
		PhoneNumber: phoneNumber, PhoneE164: phoneE164, Status: storeOrderPending,
		Amount: plan.Price, Currency: models.PlatformStoreCurrency,
		Plan: plan,
	}
	if err := s.DB.Create(&order).Error; err != nil {
		publicFail(c, http.StatusInternalServerError, "创建订单失败")
		return
	}

	if s.storeDebugMode() {
		if err := s.fulfillStoreOrder(order.ID, "debug_payment"); err != nil {
			if markErr := s.DB.Model(&order).Updates(map[string]any{"status": storeOrderFailed, "failure_reason": err.Error()}).Error; markErr != nil {
				publicFail(c, http.StatusInternalServerError, "调试订单失败状态保存失败")
				return
			}
			if errors.Is(err, errStoreProductSoldOut) {
				fail(c, http.StatusConflict, errStoreProductSoldOut.Error())
				return
			}
			publicFail(c, http.StatusInternalServerError, "调试订单发放兑换码失败")
			return
		}
		if err := s.DB.Preload("Plan").First(&order, "id = ?", order.ID).Error; err != nil {
			publicFail(c, http.StatusInternalServerError, "读取已完成订单失败")
			return
		}
		s.queuePurchaseEmail(order.ID, order.TraceID)
		c.JSON(http.StatusCreated, gin.H{"order": publicStoreOrderResponse(order), "debug": true, "message": "调试模式：订单已模拟支付，兑换码已生成"})
		return
	}
	stripeSecretKey := s.storeStripeSecretKey()
	if stripeSecretKey == "" {
		if err := s.DB.Model(&order).Updates(map[string]any{"status": storeOrderFailed, "failure_reason": "未配置 STRIPE_SECRET_KEY"}).Error; err != nil {
			publicFail(c, http.StatusInternalServerError, "保存 Stripe 配置错误状态失败")
			return
		}
		publicFail(c, http.StatusServiceUnavailable, "平台 Stripe 尚未配置")
		return
	}
	if s.storeStripeWebhookSecret() == "" {
		if err := s.DB.Model(&order).Updates(map[string]any{"status": storeOrderFailed, "failure_reason": "未配置 STRIPE_WEBHOOK_SECRET"}).Error; err != nil {
			publicFail(c, http.StatusInternalServerError, "保存 Stripe Webhook 配置错误状态失败")
			return
		}
		publicFail(c, http.StatusServiceUnavailable, "平台 Stripe Webhook 尚未配置")
		return
	}
	checkoutURL, sessionID, paymentID, err := s.createStripeCheckout(c, order, plan, stripeSecretKey)
	if err != nil {
		if markErr := s.DB.Model(&order).Updates(map[string]any{"status": storeOrderFailed, "failure_reason": err.Error()}).Error; markErr != nil {
			publicFail(c, http.StatusInternalServerError, "保存 Stripe 创建错误状态失败")
			return
		}
		publicFail(c, http.StatusBadGateway, "创建 Stripe 收款订单失败")
		return
	}
	updates := map[string]any{"stripe_session_id": sessionID}
	if paymentID != "" {
		updates["stripe_payment_id"] = paymentID
	}
	if err := s.DB.Model(&order).Updates(updates).Error; err != nil {
		publicFail(c, http.StatusInternalServerError, "保存 Stripe 订单失败")
		return
	}
	order.StripeSessionID = sessionID
	order.StripePaymentID = paymentID
	c.JSON(http.StatusCreated, gin.H{"order": publicStoreOrderResponse(order), "checkoutUrl": checkoutURL, "debug": false})
}

func (s *Server) getStoreOrder(c *gin.Context) {
	var order models.StoreOrder
	query := s.DB.Preload("Plan").Where("id = ?", c.Param("id")).First(&order)
	if query.Error == gorm.ErrRecordNotFound {
		fail(c, http.StatusNotFound, "订单不存在")
		return
	}
	if query.Error != nil {
		publicFail(c, http.StatusInternalServerError, "读取订单失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": publicStoreOrderResponse(order)})
}

func (s *Server) queryStoreOrders(c *gin.Context) {
	var input struct {
		Query            string `json:"query"`
		OrderNo          string `json:"orderNo"`
		Email            string `json:"email"`
		PhoneCountryCode string `json:"phoneCountryCode"`
		PhoneNumber      string `json:"phoneNumber"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if strings.TrimSpace(input.Query) != "" {
		lookup, args, err := aggregateStoreOrderLookup(input.Query)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		cutoff := time.Now().AddDate(0, -3, 0)
		var orders []models.StoreOrder
		if err := s.DB.Preload("Plan").Where("created_at >= ?", cutoff).Where(lookup, args...).Order("created_at DESC").Limit(50).Find(&orders).Error; err != nil {
			publicFail(c, http.StatusInternalServerError, "查询订单失败")
			return
		}
		items := make([]gin.H, 0, len(orders))
		for _, order := range orders {
			item := publicStoreOrderResponse(order)
			if order.Status != storeOrderPaid {
				item["cdkCode"] = ""
			}
			items = append(items, item)
		}
		c.JSON(http.StatusOK, gin.H{"orders": items, "retention": "仅查询最近 3 个月订单"})
		return
	}
	orderNo := strings.TrimSpace(input.OrderNo)
	if orderNo == "" && strings.TrimSpace(input.Email) == "" && (strings.TrimSpace(input.PhoneCountryCode) == "" || strings.TrimSpace(input.PhoneNumber) == "") {
		fail(c, http.StatusBadRequest, "请输入订单号、邮箱或完整手机号")
		return
	}
	cutoff := time.Now().AddDate(0, -3, 0)
	query := s.DB.Preload("Plan").Where("created_at >= ?", cutoff)
	switch {
	case orderNo != "":
		query = query.Where("order_no = ?", orderNo)
	case strings.TrimSpace(input.Email) != "":
		email, err := normalizeStoreEmail(input.Email)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		query = query.Where("email = ?", email)
	default:
		phoneE164, err := normalizePhoneLookup(input.PhoneCountryCode, input.PhoneNumber)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		query = query.Where("phone_e164 = ?", phoneE164)
	}
	var orders []models.StoreOrder
	if err := query.Order("created_at DESC").Limit(50).Find(&orders).Error; err != nil {
		publicFail(c, http.StatusInternalServerError, "查询订单失败")
		return
	}
	items := make([]gin.H, 0, len(orders))
	for _, order := range orders {
		item := publicStoreOrderResponse(order)
		if order.Status != storeOrderPaid {
			item["cdkCode"] = ""
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"orders": items, "retention": "仅查询最近 3 个月订单"})
}

func (s *Server) createStripeCheckout(c *gin.Context, order models.StoreOrder, plan models.Plan, stripeSecretKey string) (string, string, string, error) {
	amount, err := storeOrderMinorAmount(order.Amount)
	if err != nil {
		return "", "", "", err
	}
	publicBaseURL := strings.TrimRight(strings.TrimSpace(s.storePublicBaseURL()), "/")
	successURL, err := buildStripeReturnURL(s.storeSuccessURL(), publicBaseURL+"/recharge", "success", order.ID)
	if err != nil {
		return "", "", "", fmt.Errorf("支付成功回跳 URL 无效: %w", err)
	}
	cancelURL, err := buildStripeReturnURL(s.storeCancelURL(), publicBaseURL+"/recharge", "cancel", order.ID)
	if err != nil {
		return "", "", "", fmt.Errorf("支付取消回跳 URL 无效: %w", err)
	}
	productName := strings.TrimSpace(plan.Name)
	if productName == "" {
		productName = strings.TrimSpace(plan.Code)
	}
	metadata := map[string]string{
		"orderId":         order.ID,
		"order_id":        order.ID,
		"orderNo":         order.OrderNo,
		"order_no":        order.OrderNo,
		"traceId":         order.TraceID,
		"trace_id":        order.TraceID,
		"productCode":     plan.Code,
		"product_code":    plan.Code,
		"paymentCurrency": models.PlatformStoreCurrency,
	}
	values := url.Values{}
	values.Set("mode", "payment")
	values.Set("success_url", successURL)
	values.Set("cancel_url", cancelURL)
	// Stripe receives the customer-visible order number as its reference. The
	// internal order ID is retained in metadata for backwards compatibility.
	values.Set("client_reference_id", order.OrderNo)
	values.Set("billing_address_collection", "required")
	values.Set("customer_creation", "always")
	values.Set("invoice_creation[enabled]", "true")
	values.Set("invoice_creation[invoice_data][description]", productName)
	values.Set("payment_intent_data[description]", productName)
	if email := strings.TrimSpace(order.Email); email != "" {
		values.Set("customer_email", email)
		values.Set("payment_intent_data[receipt_email]", email)
	}
	values.Set("line_items[0][price_data][currency]", strings.ToLower(models.PlatformStoreCurrency))
	values.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(amount, 10))
	values.Set("line_items[0][price_data][product_data][name]", productName)
	if description := strings.TrimSpace(plan.Description); description != "" {
		values.Set("line_items[0][price_data][product_data][description]", description)
	}
	values.Set("line_items[0][quantity]", "1")
	values.Set("expand[]", "invoice")
	values.Add("expand[]", "payment_intent")
	for key, value := range metadata {
		values.Set("metadata["+key+"]", value)
		values.Set("payment_intent_data[metadata]["+key+"]", value)
		values.Set("invoice_creation[invoice_data][metadata]["+key+"]", value)
	}
	values.Set("expires_at", strconv.FormatInt(time.Now().UTC().Add(23*time.Hour+59*time.Minute).Unix(), 10))
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, strings.TrimRight(s.storeStripeAPIBaseURL(), "/")+"/v1/checkout/sessions", strings.NewReader(values.Encode()))
	if err != nil {
		return "", "", "", err
	}
	request.SetBasicAuth(stripeSecretKey, "")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Idempotency-Key", "cs-"+order.ID)
	request.Header.Set("X-Trace-ID", firstNonEmpty(order.TraceID, requestTraceID(c)))
	response, err := stripeHTTPClient.Do(request)
	if err != nil {
		return "", "", "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("stripe http %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		ID            string          `json:"id"`
		URL           string          `json:"url"`
		PaymentIntent json.RawMessage `json:"payment_intent"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.ID == "" || payload.URL == "" {
		return "", "", "", fmt.Errorf("stripe 返回缺少 checkout session")
	}
	return payload.URL, payload.ID, stripeObjectID(payload.PaymentIntent), nil
}

func (s *Server) handleStripeWebhook(c *gin.Context) {
	if c.Request.Method != http.MethodPost {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxStripeWebhookBodySize+1))
	if err != nil {
		fail(c, http.StatusBadRequest, "读取 Stripe Webhook 失败")
		return
	}
	if len(body) > maxStripeWebhookBodySize {
		fail(c, http.StatusBadRequest, "Stripe Webhook 请求体过大")
		return
	}
	secret := strings.TrimSpace(s.storeStripeWebhookSecret())
	if secret == "" {
		fail(c, http.StatusBadRequest, "Stripe Webhook 尚未配置签名密钥")
		return
	}
	if !validStripeSignature(body, c.GetHeader("Stripe-Signature"), secret) {
		fail(c, http.StatusBadRequest, "Stripe Webhook 签名无效")
		return
	}
	event, err := parseStripeWebhookEvent(body)
	if err != nil {
		fail(c, http.StatusBadRequest, "Stripe Webhook 格式无效")
		return
	}
	if strings.TrimSpace(event.ID) == "" {
		fail(c, http.StatusBadRequest, "Stripe Webhook 缺少事件 ID")
		return
	}
	if err := s.processStripeWebhook(event, body, requestTraceID(c)); err != nil {
		var nonRetryable *stripeWebhookNonRetryableError
		if errors.As(err, &nonRetryable) {
			c.JSON(http.StatusOK, gin.H{"received": true, "processed": false, "traceId": requestTraceID(c), "trace_id": requestTraceID(c), "message": nonRetryable.Error()})
			return
		}
		fail(c, http.StatusInternalServerError, "Stripe Webhook 处理失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"received": true, "processed": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c)})
}

func (s *Server) failStoreOrder(orderID, reason string) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return s.failStoreOrderTx(tx, orderID, reason)
	})
}

func (s *Server) failStoreOrderTx(tx *gorm.DB, orderID, reason string) error {
	return s.failStoreOrderWithPaymentTx(tx, orderID, "", "", reason)
}

func (s *Server) failStripeStoreOrderTx(tx *gorm.DB, orderID, sessionID, paymentID, reason string) error {
	return s.failStoreOrderWithPaymentTx(tx, orderID, sessionID, paymentID, reason)
}

func (s *Server) failStoreOrderWithPaymentTx(tx *gorm.DB, orderID, sessionID, paymentID, reason string) error {
	var order models.StoreOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error; err != nil {
		return err
	}
	if err := validateStripePaymentIdentity(order, sessionID, paymentID); err != nil {
		return err
	}
	if order.Status == storeOrderPaid {
		return nil
	}
	if order.CDKID != "" {
		if err := tx.Model(&models.CDK{}).Where("id = ? AND status = ?", order.CDKID, models.CDKProcessing).Updates(map[string]any{"status": models.CDKAvailable, "used_by_task_id": ""}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&order).Updates(map[string]any{"status": storeOrderFailed, "failure_reason": strings.TrimSpace(reason)}).Error
}

type stripeWebhookEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

type stripeWebhookAction string

const (
	stripeWebhookActionIgnore  stripeWebhookAction = "ignore"
	stripeWebhookActionFulfill stripeWebhookAction = "fulfill"
	stripeWebhookActionFail    stripeWebhookAction = "fail"
)

type stripePaymentNotification struct {
	Action        stripeWebhookAction
	OrderID       string
	OrderNo       string
	ReferenceID   string
	TraceID       string
	CheckoutID    string
	PaymentID     string
	InvoiceID     string
	PaymentStatus string
	AmountMinor   int64
	Currency      string
	FailureReason string
}

type stripeCheckoutSessionWebhookObject struct {
	ID                string            `json:"id"`
	ClientReferenceID string            `json:"client_reference_id"`
	PaymentStatus     string            `json:"payment_status"`
	Currency          string            `json:"currency"`
	AmountTotal       int64             `json:"amount_total"`
	Metadata          map[string]string `json:"metadata"`
	PaymentIntent     json.RawMessage   `json:"payment_intent"`
	Invoice           json.RawMessage   `json:"invoice"`
}

type stripeInvoiceWebhookObject struct {
	ID            string            `json:"id"`
	Currency      string            `json:"currency"`
	Total         int64             `json:"total"`
	AmountPaid    int64             `json:"amount_paid"`
	Metadata      map[string]string `json:"metadata"`
	PaymentIntent json.RawMessage   `json:"payment_intent"`
}

type stripePaymentIntentWebhookObject struct {
	ID               string            `json:"id"`
	Amount           int64             `json:"amount"`
	AmountReceived   int64             `json:"amount_received"`
	Currency         string            `json:"currency"`
	Status           string            `json:"status"`
	Metadata         map[string]string `json:"metadata"`
	LastPaymentError struct {
		Message string `json:"message"`
	} `json:"last_payment_error"`
}

func parseStripeWebhookEvent(body []byte) (stripeWebhookEvent, error) {
	var event stripeWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return stripeWebhookEvent{}, err
	}
	if strings.TrimSpace(event.Type) == "" || len(event.Data.Object) == 0 || string(event.Data.Object) == "null" {
		return stripeWebhookEvent{}, fmt.Errorf("Stripe Webhook 缺少 type 或 data.object")
	}
	return event, nil
}

func parseStripePaymentNotification(event stripeWebhookEvent) (stripePaymentNotification, error) {
	notification := stripePaymentNotification{Action: stripeWebhookActionIgnore}
	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "checkout.session.expired":
		var object stripeCheckoutSessionWebhookObject
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return stripePaymentNotification{}, err
		}
		notification.CheckoutID = strings.TrimSpace(object.ID)
		notification.PaymentID = stripeObjectID(object.PaymentIntent)
		notification.InvoiceID = stripeObjectID(object.Invoice)
		notification.OrderID = firstStripeMetadata(object.Metadata, "orderId", "order_id")
		notification.OrderNo = firstStripeMetadata(object.Metadata, "orderNo", "order_no")
		notification.ReferenceID = strings.TrimSpace(object.ClientReferenceID)
		if notification.OrderID == "" && notification.OrderNo == "" {
			// Older Checkout Sessions used the internal order ID as the client
			// reference. Keep accepting that shape during migration.
			notification.OrderID = notification.ReferenceID
		} else if notification.OrderNo == "" {
			notification.OrderNo = notification.ReferenceID
		}
		notification.TraceID = firstStripeMetadata(object.Metadata, "traceId", "trace_id")
		notification.PaymentStatus = strings.ToLower(strings.TrimSpace(object.PaymentStatus))
		notification.AmountMinor = object.AmountTotal
		notification.Currency = strings.ToUpper(strings.TrimSpace(object.Currency))
		switch event.Type {
		case "checkout.session.async_payment_succeeded":
			notification.Action = stripeWebhookActionFulfill
		case "checkout.session.async_payment_failed":
			notification.Action = stripeWebhookActionFail
			notification.FailureReason = "Stripe 异步支付失败"
		case "checkout.session.expired":
			notification.Action = stripeWebhookActionFail
			notification.FailureReason = "Stripe Checkout 已过期"
		case "checkout.session.completed":
			if notification.PaymentStatus == "paid" {
				notification.Action = stripeWebhookActionFulfill
			}
		}
	case "invoice.paid", "invoice.payment_failed":
		var object stripeInvoiceWebhookObject
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return stripePaymentNotification{}, err
		}
		notification.InvoiceID = strings.TrimSpace(object.ID)
		notification.PaymentID = stripeObjectID(object.PaymentIntent)
		notification.OrderID = firstStripeMetadata(object.Metadata, "orderId", "order_id")
		notification.OrderNo = firstStripeMetadata(object.Metadata, "orderNo", "order_no")
		notification.TraceID = firstStripeMetadata(object.Metadata, "traceId", "trace_id")
		notification.PaymentStatus = strings.ToLower(strings.TrimSpace(event.Type))
		notification.AmountMinor = object.AmountPaid
		if notification.AmountMinor == 0 {
			notification.AmountMinor = object.Total
		}
		notification.Currency = strings.ToUpper(strings.TrimSpace(object.Currency))
		if event.Type == "invoice.paid" {
			notification.Action = stripeWebhookActionFulfill
		} else {
			notification.Action = stripeWebhookActionFail
			notification.FailureReason = "Stripe Invoice 支付失败"
		}
	case "payment_intent.succeeded", "payment_intent.payment_failed":
		var object stripePaymentIntentWebhookObject
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return stripePaymentNotification{}, err
		}
		notification.PaymentID = strings.TrimSpace(object.ID)
		notification.OrderID = firstStripeMetadata(object.Metadata, "orderId", "order_id")
		notification.OrderNo = firstStripeMetadata(object.Metadata, "orderNo", "order_no")
		notification.TraceID = firstStripeMetadata(object.Metadata, "traceId", "trace_id")
		notification.PaymentStatus = strings.ToLower(strings.TrimSpace(object.Status))
		notification.AmountMinor = object.AmountReceived
		if notification.AmountMinor == 0 {
			notification.AmountMinor = object.Amount
		}
		notification.Currency = strings.ToUpper(strings.TrimSpace(object.Currency))
		if event.Type == "payment_intent.succeeded" && (notification.PaymentStatus == "" || notification.PaymentStatus == "succeeded") {
			notification.Action = stripeWebhookActionFulfill
		} else if event.Type == "payment_intent.payment_failed" {
			notification.Action = stripeWebhookActionFail
			notification.FailureReason = strings.TrimSpace(object.LastPaymentError.Message)
			if notification.FailureReason == "" {
				notification.FailureReason = "Stripe PaymentIntent 支付失败"
			}
		}
	}
	return notification, nil
}

func (s *Server) processStripeWebhook(event stripeWebhookEvent, body []byte, traceID string) error {
	notification, err := parseStripePaymentNotification(event)
	if err != nil {
		return &stripeWebhookNonRetryableError{err: err}
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	notification.TraceID = firstNonEmpty(notification.TraceID, traceID)
	var nonRetryable error
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		eventRow := models.StorePaymentEvent{
			ID: db.NewID("stripe_event"), Provider: storePaymentProvider, ProviderEventID: event.ID,
			OrderID: notification.OrderID, OrderNo: notification.OrderNo, TraceID: notification.TraceID, EventType: event.Type,
			Status: storePaymentEventReceived, Payload: string(body),
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&eventRow)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// eventRow contains a newly generated ID. Query the existing ledger
			// row into a zero-valued model so GORM does not add that stale ID as
			// an implicit predicate.
			var existingEvent models.StorePaymentEvent
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider = ? AND provider_event_id = ?", storePaymentProvider, event.ID).First(&existingEvent).Error; err != nil {
				return err
			}
			eventRow = existingEvent
			if existingEvent.Status == storePaymentEventProcessed || existingEvent.Status == storePaymentEventIgnored {
				return nil
			}
		}
		if err := tx.Model(&models.StorePaymentEvent{}).
			Where("provider = ? AND provider_event_id = ?", storePaymentProvider, event.ID).
			Updates(map[string]any{"order_id": notification.OrderID, "order_no": notification.OrderNo, "trace_id": notification.TraceID, "event_type": event.Type, "status": storePaymentEventReceived, "error_message": "", "payload": string(body)}).Error; err != nil {
			return err
		}

		if notification.Action == stripeWebhookActionIgnore {
			return markStripePaymentEvent(tx, event.ID, storePaymentEventIgnored, "")
		}
		if strings.TrimSpace(notification.OrderID) == "" && strings.TrimSpace(notification.OrderNo) == "" && strings.TrimSpace(notification.ReferenceID) == "" {
			nonRetryable = &stripeWebhookNonRetryableError{err: fmt.Errorf("Stripe 事件缺少订单标识")}
			return markStripePaymentEvent(tx, event.ID, storePaymentEventIgnored, nonRetryable.Error())
		}
		resolvedOrder, resolveErr := resolveStripeStoreOrder(tx, notification)
		if resolveErr != nil {
			if errors.Is(resolveErr, gorm.ErrRecordNotFound) {
				nonRetryable = &stripeWebhookNonRetryableError{err: fmt.Errorf("Stripe 事件对应订单不存在")}
				return markStripePaymentEvent(tx, event.ID, storePaymentEventIgnored, nonRetryable.Error())
			}
			var validationErr *stripePaymentValidationError
			if errors.As(resolveErr, &validationErr) {
				nonRetryable = &stripeWebhookNonRetryableError{err: validationErr}
				return markStripePaymentEvent(tx, event.ID, storePaymentEventFailed, nonRetryable.Error())
			}
			return resolveErr
		}
		notification.OrderID = resolvedOrder.ID
		notification.OrderNo = resolvedOrder.OrderNo
		if err := tx.Model(&models.StorePaymentEvent{}).
			Where("provider = ? AND provider_event_id = ?", storePaymentProvider, event.ID).
			Updates(map[string]any{"order_id": notification.OrderID, "order_no": notification.OrderNo}).Error; err != nil {
			return err
		}

		if notification.Action == stripeWebhookActionFulfill {
			if err := s.fulfillStripeStoreOrderWithPaymentTx(tx, notification.OrderID, notification.CheckoutID, notification.PaymentID, notification.AmountMinor, notification.Currency); err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					nonRetryable = &stripeWebhookNonRetryableError{err: fmt.Errorf("Stripe 事件对应订单不存在")}
					return markStripePaymentEvent(tx, event.ID, storePaymentEventIgnored, nonRetryable.Error())
				}
				var validationErr *stripePaymentValidationError
				if errors.As(err, &validationErr) {
					nonRetryable = &stripeWebhookNonRetryableError{err: validationErr}
					return markStripePaymentEvent(tx, event.ID, storePaymentEventFailed, nonRetryable.Error())
				}
				if errors.Is(err, errStoreProductSoldOut) {
					if failErr := s.failStripeStoreOrderTx(tx, notification.OrderID, notification.CheckoutID, notification.PaymentID, err.Error()); failErr != nil {
						return failErr
					}
					nonRetryable = &stripeWebhookNonRetryableError{err: err}
					return markStripePaymentEvent(tx, event.ID, storePaymentEventFailed, nonRetryable.Error())
				}
				return err
			}
		} else if err := s.failStripeStoreOrderTx(tx, notification.OrderID, notification.CheckoutID, notification.PaymentID, firstNonEmpty(notification.FailureReason, "Stripe 支付失败")); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				nonRetryable = &stripeWebhookNonRetryableError{err: fmt.Errorf("Stripe 事件对应订单不存在")}
				return markStripePaymentEvent(tx, event.ID, storePaymentEventIgnored, nonRetryable.Error())
			}
			var validationErr *stripePaymentValidationError
			if errors.As(err, &validationErr) {
				nonRetryable = &stripeWebhookNonRetryableError{err: validationErr}
				return markStripePaymentEvent(tx, event.ID, storePaymentEventFailed, nonRetryable.Error())
			}
			return err
		}
		return markStripePaymentEvent(tx, event.ID, storePaymentEventProcessed, "")
	})
	if err != nil {
		return err
	}
	if nonRetryable == nil && notification.Action == stripeWebhookActionFulfill {
		s.queuePurchaseEmail(notification.OrderID, firstNonEmpty(notification.TraceID, traceID))
	}
	return nonRetryable
}

// resolveStripeStoreOrder accepts the public order number and the legacy
// internal order ID, but every supplied identifier must resolve to one row.
// This prevents a callback carrying mixed metadata from paying the wrong order.
func resolveStripeStoreOrder(tx *gorm.DB, notification stripePaymentNotification) (models.StoreOrder, error) {
	candidateIDs := make(map[string]struct{})
	addCandidates := func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		var matches []models.StoreOrder
		if err := tx.Where("id = ? OR order_no = ?", value, value).Find(&matches).Error; err != nil {
			return err
		}
		for _, match := range matches {
			candidateIDs[match.ID] = struct{}{}
		}
		return nil
	}
	for _, value := range []string{notification.OrderID, notification.OrderNo, notification.ReferenceID} {
		if err := addCandidates(value); err != nil {
			return models.StoreOrder{}, err
		}
	}
	if len(candidateIDs) == 0 {
		return models.StoreOrder{}, gorm.ErrRecordNotFound
	}
	if len(candidateIDs) != 1 {
		return models.StoreOrder{}, &stripePaymentValidationError{err: fmt.Errorf("Stripe 订单号与内部订单 ID 不匹配")}
	}
	var order models.StoreOrder
	for id := range candidateIDs {
		if err := tx.Where("id = ?", id).First(&order).Error; err != nil {
			return models.StoreOrder{}, err
		}
	}
	return order, nil
}

func markStripePaymentEvent(tx *gorm.DB, providerEventID, status, errorMessage string) error {
	updates := map[string]any{"status": status, "error_message": strings.TrimSpace(errorMessage)}
	if status == storePaymentEventProcessed || status == storePaymentEventIgnored {
		now := time.Now()
		updates["processed_at"] = now
	} else {
		updates["processed_at"] = nil
	}
	return tx.Model(&models.StorePaymentEvent{}).
		Where("provider = ? AND provider_event_id = ?", storePaymentProvider, providerEventID).
		Updates(updates).Error
}

func validateStripePayment(tx *gorm.DB, orderID string, amountMinor int64, currency string) error {
	var order models.StoreOrder
	if err := tx.Where("id = ?", orderID).First(&order).Error; err != nil {
		return err
	}
	return validateStripeOrderPayment(order, amountMinor, currency)
}

func validateStripeOrderPayment(order models.StoreOrder, amountMinor int64, currency string) error {
	expectedCurrency := strings.ToUpper(strings.TrimSpace(order.Currency))
	if expectedCurrency == "" {
		expectedCurrency = models.PlatformStoreCurrency
	}
	if expectedCurrency != models.PlatformStoreCurrency || strings.ToUpper(strings.TrimSpace(currency)) != expectedCurrency {
		return &stripePaymentValidationError{err: fmt.Errorf("Stripe 支付币种不匹配: expected=%s actual=%s", expectedCurrency, strings.ToUpper(strings.TrimSpace(currency)))}
	}
	expectedAmount, err := storeOrderMinorAmount(order.Amount)
	if err != nil {
		return err
	}
	if amountMinor != expectedAmount {
		return &stripePaymentValidationError{err: fmt.Errorf("Stripe 支付金额不匹配: expected=%d actual=%d", expectedAmount, amountMinor)}
	}
	return nil
}

func validateStripePaymentIdentity(order models.StoreOrder, sessionID, paymentID string) error {
	if expected := strings.TrimSpace(order.StripeSessionID); expected != "" && strings.TrimSpace(sessionID) != "" && expected != strings.TrimSpace(sessionID) {
		return &stripePaymentValidationError{err: fmt.Errorf("Stripe Checkout Session 与订单不匹配")}
	}
	if expected := strings.TrimSpace(order.StripePaymentID); expected != "" && strings.TrimSpace(paymentID) != "" && expected != strings.TrimSpace(paymentID) {
		return &stripePaymentValidationError{err: fmt.Errorf("Stripe PaymentIntent 与订单不匹配")}
	}
	return nil
}

func storeOrderMinorAmount(amount float64) (int64, error) {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, fmt.Errorf("套餐价格无效")
	}
	minor := math.Round(amount * 100)
	if minor < 1 || minor > float64(^uint64(0)>>1) {
		return 0, fmt.Errorf("套餐价格超出 Stripe 支持范围")
	}
	return int64(minor), nil
}

func firstStripeMetadata(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func stripeObjectID(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		return strings.TrimSpace(id)
	}
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return strings.TrimSpace(object.ID)
	}
	return ""
}

func buildStripeReturnURL(rawURL, fallbackURL, state, orderID string) (string, error) {
	value := checkoutReturnURL(firstNonEmpty(rawURL, fallbackURL), state, orderID)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("必须是绝对 http(s) URL")
	}
	return value, nil
}

// checkoutReturnURL mirrors the reference project's return-link contract. It
// keeps configured query parameters and lets Stripe replace its session token.
func checkoutReturnURL(rawURL, state, orderID string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = "/recharge"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	if strings.TrimSpace(state) != "" {
		query.Set("checkout", strings.TrimSpace(state))
	}
	if strings.TrimSpace(orderID) != "" {
		query.Set("order_id", strings.TrimSpace(orderID))
	}
	query.Set("session_id", "{CHECKOUT_SESSION_ID}")
	parsed.RawQuery = query.Encode()
	value := strings.ReplaceAll(parsed.String(), "%7BCHECKOUT_SESSION_ID%7D", "{CHECKOUT_SESSION_ID}")
	return strings.ReplaceAll(value, "{ORDER_ID}", url.PathEscape(strings.TrimSpace(orderID)))
}

func validStripeSignature(payload []byte, header, secret string) bool {
	var timestamp string
	var signatures []string
	for _, item := range strings.Split(header, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "t":
			timestamp = parts[1]
		case "v1":
			signatures = append(signatures, parts[1])
		}
	}
	parsed, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 || math.Abs(float64(time.Now().Unix()-parsed)) > stripeSignatureTolerance.Seconds() {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	actual := mac.Sum(nil)
	for _, signature := range signatures {
		expected, err := hex.DecodeString(signature)
		if err == nil && hmac.Equal(actual, expected) {
			return true
		}
	}
	return false
}

func (s *Server) fulfillStoreOrder(orderID, paymentID string) error {
	return s.fulfillStoreOrderWithPayment(orderID, "", paymentID)
}

func (s *Server) fulfillStoreOrderWithPayment(orderID, sessionID, paymentID string) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return s.fulfillStoreOrderWithPaymentTx(tx, orderID, sessionID, paymentID)
	})
}

func (s *Server) fulfillStoreOrderWithPaymentTx(tx *gorm.DB, orderID, sessionID, paymentID string) error {
	return s.fulfillStoreOrderWithPaymentDetailsTx(tx, orderID, sessionID, paymentID, 0, "", false)
}

func (s *Server) fulfillStripeStoreOrderWithPaymentTx(tx *gorm.DB, orderID, sessionID, paymentID string, amountMinor int64, currency string) error {
	return s.fulfillStoreOrderWithPaymentDetailsTx(tx, orderID, sessionID, paymentID, amountMinor, currency, true)
}

func (s *Server) fulfillStoreOrderWithPaymentDetailsTx(tx *gorm.DB, orderID, sessionID, paymentID string, amountMinor int64, currency string, validatePayment bool) error {
	var order models.StoreOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Plan").Where("id = ?", orderID).First(&order).Error; err != nil {
		return err
	}
	if validatePayment {
		if err := validateStripePaymentIdentity(order, sessionID, paymentID); err != nil {
			return err
		}
		if err := validateStripeOrderPayment(order, amountMinor, currency); err != nil {
			return err
		}
	}
	if order.Status == storeOrderPaid && order.CDKCode != "" {
		return nil
	}
	if err := consumeStoreProductInventory(tx, order.PlanID); err != nil {
		return err
	}
	code, err := newCDKCode("KC")
	if err != nil {
		return err
	}
	now := time.Now()
	cdk := buildStoreOrderCDK(order, db.NewID("cdk"), code, now)
	if err := tx.Create(&cdk).Error; err != nil {
		return err
	}
	updates := map[string]any{"status": storeOrderPaid, "cdk_id": cdk.ID, "cdk_code": cdk.Code, "paid_at": now}
	if paymentID != "" {
		updates["stripe_payment_id"] = paymentID
	}
	if sessionID != "" {
		updates["stripe_session_id"] = sessionID
	}
	return tx.Model(&order).Updates(updates).Error
}

// consumeStoreProductInventory performs the only persisted sale-count update.
// The conditional predicate makes concurrent fulfillments serialize at the
// database level and prevents sold_count from exceeding sale_limit. Unlimited
// products keep their counter at zero while still passing the same statement.
func consumeStoreProductInventory(tx *gorm.DB, planID string) error {
	if tx == nil || strings.TrimSpace(planID) == "" {
		return fmt.Errorf("售卡商品不存在")
	}
	result := tx.Model(&models.Plan{}).
		Where("id = ? AND (sale_limit <= 0 OR sold_count < sale_limit)", planID).
		UpdateColumn("sold_count", gorm.Expr("CASE WHEN sale_limit > 0 THEN sold_count + 1 ELSE sold_count END"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errStoreProductSoldOut
	}
	return nil
}

// buildStoreOrderCDK is the delivery contract for a paid platform order. A
// purchased code is a self-service code, immediately available for recharge,
// and already shipped because it is delivered in the order response.
func buildStoreOrderCDK(order models.StoreOrder, id, code string, shippedAt time.Time) models.CDK {
	return models.CDK{
		ID: id, Code: code, PlanID: order.PlanID, PlanType: db.PlanTypeForPlan(order.Plan),
		Type: models.CDKTypeSelf, Status: models.CDKAvailable, ShippedAt: &shippedAt,
	}
}

func publicStoreOrderResponse(order models.StoreOrder) gin.H {
	return gin.H{
		"id": order.ID, "orderNo": order.OrderNo, "planCode": order.Plan.Code, "planName": order.Plan.Name,
		"status": order.Status, "amount": order.Amount, "currency": order.Currency,
		"cdkCode":          order.CDKCode,
		"phoneCountryCode": order.PhoneCountryCode, "phoneNumber": maskStorePhone(order.PhoneCountryCode, order.PhoneNumber),
		"email":     maskStoreEmail(order.Email),
		"createdAt": order.CreatedAt, "paidAt": order.PaidAt,
	}
}

func (s *Server) storeDebugMode() bool {
	value := firstNonEmpty(s.configValue("store_debug_mode", ""), s.configValue("storeDebugMode", ""))
	return parseBoolConfig(firstNonEmpty(value, strconv.FormatBool(s.Cfg.StoreDebugMode)), s.Cfg.StoreDebugMode)
}

func parseBoolConfig(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func (s *Server) storeStripeSecretKey() string {
	return firstNonEmpty(s.configValue("stripe_secret_key", ""), s.Cfg.StripeSecretKey)
}

func (s *Server) storeStripeWebhookSecret() string {
	return firstNonEmpty(s.configValue("stripe_webhook_secret", ""), s.Cfg.StripeWebhookSecret)
}

func (s *Server) storeStripeAPIBaseURL() string {
	return firstNonEmpty(
		s.configValue("stripe_api_base_url", ""),
		s.configValue("stripeAPIBaseURL", ""),
		s.Cfg.StripeAPIBaseURL,
		"https://api.stripe.com",
	)
}

func (s *Server) storeSuccessURL() string {
	return firstNonEmpty(s.configValue("stripe_success_url", ""), s.configValue("stripeSuccessURL", ""), s.Cfg.StripeSuccessURL)
}

func (s *Server) storeCancelURL() string {
	return firstNonEmpty(s.configValue("stripe_cancel_url", ""), s.configValue("stripeCancelURL", ""), s.Cfg.StripeCancelURL)
}

func (s *Server) storePublicBaseURL() string {
	return firstNonEmpty(s.configValue("public_base_url", ""), s.configValue("publicBaseURL", ""), s.Cfg.PublicBaseURL)
}

func normalizeStoreEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || !strings.Contains(email, "@") {
		return "", fmt.Errorf("请输入有效联系邮箱")
	}
	return email, nil
}

func normalizeStorePhone(countryCode, number string) (string, string, string, error) {
	countryCode = strings.TrimSpace(countryCode)
	if countryCode == "" {
		return "", "", "", fmt.Errorf("请选择手机号国家/地区区号")
	}
	if !strings.HasPrefix(countryCode, "+") {
		countryCode = "+" + countryCode
	}
	countryDigits := digitsOnly(countryCode)
	phoneDigits := digitsOnly(number)
	if len(countryDigits) < 1 || len(countryDigits) > 3 || len(phoneDigits) < 4 || len(phoneDigits) > 14 || len(countryDigits)+len(phoneDigits) > 15 {
		return "", "", "", fmt.Errorf("手机号或国家/地区区号格式不正确")
	}
	return "+" + countryDigits, phoneDigits, "+" + countryDigits + phoneDigits, nil
}

func normalizePhoneLookup(countryCode, number string) (string, error) {
	_, _, phoneE164, err := normalizeStorePhone(countryCode, number)
	return phoneE164, err
}

func aggregateStoreOrderLookup(value string) (string, []any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, fmt.Errorf("请输入订单号、邮箱或手机号")
	}

	conditions := []string{"order_no = ?"}
	args := []any{value}
	if strings.Contains(value, "@") {
		email, err := normalizeStoreEmail(value)
		if err != nil {
			return "", nil, err
		}
		conditions = append(conditions, "email = ?")
		args = append(args, email)
	}

	phoneDigits := digitsOnly(value)
	if len(phoneDigits) >= 4 && len(phoneDigits) <= 15 {
		conditions = append(conditions, "phone_number = ?", "phone_e164 = ?")
		args = append(args, phoneDigits, "+"+phoneDigits)
	}
	return "(" + strings.Join(conditions, " OR ") + ")", args, nil
}

func maskStorePhone(countryCode, number string) string {
	if strings.TrimSpace(countryCode) == "" || strings.TrimSpace(number) == "" {
		return ""
	}
	if len(number) <= 4 {
		return countryCode + " ****"
	}
	return countryCode + " " + strings.Repeat("*", len(number)-4) + number[len(number)-4:]
}

func maskStoreEmail(email string) string {
	parts := strings.SplitN(strings.TrimSpace(email), "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return ""
	}
	local := parts[0]
	if len(local) == 1 {
		return "*@" + parts[1]
	}
	return local[:1] + strings.Repeat("*", maxInt(1, len(local)-2)) + local[len(local)-1:] + "@" + parts[1]
}

func newStoreOrderNo() string {
	code, err := newCDKCode("ORD")
	if err != nil {
		return "ORD-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return code
}
