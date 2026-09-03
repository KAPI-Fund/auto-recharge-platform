package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const upstreamResponseLimit = 2 << 20

var cardExpiryPattern = regexp.MustCompile(`^(0?[1-9]|1[0-2])\s*/?\s*(\d{2}|\d{4})$`)

type upstreamRuntimeConfig struct {
	protocol          string
	baseURL           string
	apiKey            string
	createPath        string
	statusPath        string
	pollInterval      time.Duration
	pollAttempts      int
	planKey           string
	country           string
	currency          string
	idempotencyPrefix string
}

type upstreamCard struct {
	id           string
	allocationID string
	provider     string
	last4        string
	number       string
	expiry       string
	cvc          string
	holder       string
}

func (s *Server) upstreamConfig() upstreamRuntimeConfig {
	gpt := s.gptConfig()
	pollIntervalMS := s.Cfg.UpstreamPollIntervalMS
	if pollIntervalMS <= 0 {
		pollIntervalMS = 3000
	}
	pollAttempts := s.Cfg.UpstreamPollAttempts
	if pollAttempts <= 0 {
		pollAttempts = 40
	}
	config := upstreamRuntimeConfig{
		protocol:          "generic",
		baseURL:           strings.TrimRight(firstNonEmpty(s.configValue("upstreamBaseURL", ""), s.Cfg.UpstreamBaseURL), "/"),
		apiKey:            firstNonEmpty(s.configValue("upstream_api_key", ""), s.configValue("upstreamApiKey", ""), s.Cfg.UpstreamAPIKey),
		createPath:        firstNonEmpty(s.configValue("upstreamCreatePath", ""), s.Cfg.UpstreamCreatePath, "/pay"),
		statusPath:        firstNonEmpty(s.configValue("upstreamStatusPath", ""), s.Cfg.UpstreamStatusPath, "/tasks/:id"),
		pollInterval:      time.Duration(pollIntervalMS) * time.Millisecond,
		pollAttempts:      pollAttempts,
		planKey:           "plus",
		country:           "PH",
		currency:          "PHP",
		idempotencyPrefix: "task",
	}
	if config.pollInterval <= 0 {
		config.pollInterval = 3 * time.Second
	}
	if config.pollAttempts <= 0 {
		config.pollAttempts = 40
	}
	if gpt["enabled"] == "1" && strings.TrimSpace(gpt["api_key"]) != "" {
		config.protocol = "gpt"
		config.baseURL = strings.TrimRight(firstNonEmpty(gpt["base_url"], "https://kc.vpss.eu.cc/"), "/")
		config.apiKey = strings.TrimSpace(gpt["api_key"])
		config.planKey = firstNonEmpty(gpt["plan_key"], "plus")
		config.country = strings.ToUpper(firstNonEmpty(gpt["country"], "PH"))
		config.currency = strings.ToUpper(firstNonEmpty(gpt["currency"], "PHP"))
		gptPollIntervalMS := s.Cfg.GPTAPIPollIntervalMS
		if gptPollIntervalMS <= 0 {
			gptPollIntervalMS = 5000
		}
		gptPollAttempts := s.Cfg.GPTAPIMaxPolls
		if gptPollAttempts <= 0 {
			gptPollAttempts = 120
		}
		config.pollInterval = time.Duration(gptPollIntervalMS) * time.Millisecond
		config.pollAttempts = gptPollAttempts
		if config.pollInterval <= 0 {
			config.pollInterval = 5 * time.Second
		}
		if config.pollAttempts <= 0 {
			config.pollAttempts = 120
		}
		config.idempotencyPrefix = "cdk"
	}
	return config
}

func (s *Server) runUpstreamEndpoint(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("id"))
	var input struct {
		WorkerID   string `json:"workerId"`
		LeaseToken string `json:"leaseToken"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, "请求格式不正确")
			return
		}
	}
	traceID := requestTraceID(c)
	result, err := s.executeUpstreamTask(c.Request.Context(), taskID, strings.TrimSpace(input.WorkerID), strings.TrimSpace(input.LeaseToken), traceID)
	if err != nil {
		if errors.Is(err, errTaskLeaseLost) || errors.Is(err, errTaskLeaseInput) {
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
			return
		}
		message := err.Error()
		_ = s.appendTaskRuntimeLog(taskID, traceID, "error", "upstream", message, input.WorkerID, input.LeaseToken)
		failureCode := classifyUpstreamError(message)
		failure := taskUpdateInput{
			Status:       models.TaskFailed,
			Progress:     intPointer(0),
			Message:      stringPointer(message),
			WorkerID:     stringPointer(input.WorkerID),
			LeaseToken:   stringPointer(input.LeaseToken),
			ErrorCode:    stringPointer(failureCode),
			ErrorMessage: stringPointer(message),
		}
		if _, _, updateErr := s.applyTaskUpdate(taskID, failure); updateErr != nil && updateErr != gorm.ErrRecordNotFound {
			fail(c, http.StatusInternalServerError, "更新 upstream 任务失败: "+updateErr.Error())
			return
		}
		result = map[string]any{
			"status":    models.TaskFailed,
			"progress":  0,
			"message":   message,
			"errorCode": failureCode,
			"traceId":   traceID,
			"trace_id":  traceID,
		}
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) executeUpstreamTask(ctx context.Context, taskID, workerID, leaseToken, traceID string) (map[string]any, error) {
	var task models.RechargeTask
	if err := s.DB.Preload("Plan").First(&task, "id = ?", taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("任务不存在")
		}
		return nil, fmt.Errorf("读取 upstream 任务失败: %w", err)
	}
	traceID = firstNonEmpty(traceID, task.TraceID, db.NewID("trace"))
	if task.Status == models.TaskSucceeded || task.Status == models.TaskFailed || task.Status == models.TaskManual {
		return map[string]any{"status": task.Status, "progress": task.Progress, "message": task.Message, "traceId": traceID, "trace_id": traceID}, nil
	}
	secret, err := s.decryptSessionValue(task.SessionCiphertext)
	if err != nil {
		return nil, fmt.Errorf("无法解密任务 Session")
	}
	config := s.upstreamConfig()
	if strings.TrimSpace(config.baseURL) == "" {
		return nil, fmt.Errorf("未配置第三方代充 API 地址")
	}
	if config.protocol == "gpt" && strings.TrimSpace(config.apiKey) == "" {
		return nil, fmt.Errorf("未配置第三方代充 API Key")
	}
	if workerID == "" {
		workerID = "go-upstream"
	}
	if _, _, err := s.applyTaskUpdate(task.ID, taskUpdateInput{Status: models.TaskRunning, Progress: intPointer(maxInt(5, task.Progress)), Message: stringPointer("Go upstream 执行器已接管任务"), WorkerID: stringPointer(workerID), LeaseToken: stringPointer(leaseToken)}); err != nil {
		return nil, fmt.Errorf("更新 upstream 开始状态失败: %w", err)
	}

	lastRaw := map[string]any{}
	orderID := ""
	providerTaskID := ""
	topupCode := ""
	var reservedCard *upstreamCard
	proxy := ""
	cardSettled := false
	client := &http.Client{Timeout: 60 * time.Second}
	settleCard := func(failureCode, failureMessage string) error {
		if reservedCard == nil || strings.TrimSpace(reservedCard.id) == "" || cardSettled {
			return nil
		}
		// Mark the local guard after the Go boundary returns. The endpoint is
		// idempotent, and a network-level error is still safe to retry from the
		// deferred exception path because the allocation/usage rows are locked
		// and the Provider cancellation has its own pending-retry marker.
		if s.CardPools != nil {
			_, err := s.CardPools.SettleCardForAllocation(ctx, reservedCard.id, reservedCard.allocationID, task.ID, failureCode, failureMessage)
			if err == nil {
				cardSettled = true
			}
			return err
		}
		if strings.TrimSpace(failureCode) != "" || strings.TrimSpace(failureMessage) != "" {
			result := s.DB.Model(&models.CardAsset{}).Where("id = ?", reservedCard.id).Updates(map[string]any{
				"status": "已报废", "active": false, "in_use": false, "locked_at": nil, "locked_by": "",
			})
			cardSettled = true
			return result.Error
		}
		if _, err := s.internalRecordCardUsage(reservedCard.id); err != nil {
			return err
		}
		cardSettled = true
		return s.releaseCardAsset(reservedCard.id)
	}

	update := func(progress int, message string, raw map[string]any, status string, errorCode string) error {
		if raw == nil {
			raw = lastRaw
		}
		rawOutput := jsonText(raw)
		lastRaw = raw
		if code := extractUpstreamTopupCode(raw); code != "" {
			topupCode = code
		}
		input := taskUpdateInput{
			Status:          status,
			Progress:        intPointer(progress),
			Message:         stringPointer(message),
			WorkerID:        stringPointer(workerID),
			LeaseToken:      stringPointer(leaseToken),
			UpstreamOrderID: stringPointer(orderID),
			GPTAPIOrderID:   stringPointer(orderID),
			GPTAPITaskID:    stringPointer(providerTaskID),
			GPTAPIRaw:       stringPointer(rawOutput),
			GPTAPITopupCode: stringPointer(topupCode),
			CardLast4:       stringPointer(cardLast4(reservedCard)),
			ErrorCode:       stringPointer(errorCode),
			ErrorMessage:    stringPointer(errorMessageForStatus(status, message)),
		}
		if _, _, err := s.applyTaskUpdate(task.ID, input); err != nil {
			return err
		}
		if strings.TrimSpace(message) != "" {
			return s.appendTaskRuntimeLog(task.ID, traceID, "stdout", "upstream/progress", message, workerID, leaseToken)
		}
		return nil
	}

	defer func() {
		if reservedCard != nil && !cardSettled {
			if err := settleCard("upstream_exception", "上游执行异常，已结束银行卡生命周期"); err != nil {
				_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "释放银行卡失败: "+err.Error(), workerID, leaseToken)
			}
		}
	}()

	sessionPayload := sessionBodyObject(secret)
	if config.protocol == "gpt" {
		if err := update(10, "正在检查 Session 格式", lastRaw, models.TaskRunning, ""); err != nil {
			return nil, err
		}
		inspect, err := doUpstreamJSON(ctx, client, http.MethodPost, joinUpstreamURL(config.baseURL, "/pay/inspect"), map[string]string{"Authorization": "Bearer " + config.apiKey}, map[string]any{"plan_key": firstNonEmpty(config.planKey, task.Plan.Code, "plus"), "session": sessionPayload})
		if err != nil {
			return nil, err
		}
		if !boolValue(inspect, "ok") || !boolValue(inspect, "verified") {
			return nil, fmt.Errorf("Session 格式或有效期检查失败: %s", upstreamDetail(inspect, "session_invalid"))
		}
		lastRaw = inspect
		proxy, err = s.activeUpstreamProxy()
		if err != nil {
			return nil, fmt.Errorf("读取代理池失败: %w", err)
		}
		reservedCard, err = s.reserveUpstreamCard(ctx, task, workerID, leaseToken, "gptapi_"+task.ID)
		if err != nil {
			return nil, err
		}
		if reservedCard == nil {
			return nil, fmt.Errorf("银行卡池暂无可用卡片，请在后台「银行卡池」导入银行卡后再试")
		}
	}

	if err := update(protocolProgress(config.protocol, 20, 12), "连接第三方代充 API", lastRaw, models.TaskRunning, ""); err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + config.apiKey}
	body := map[string]any{}
	if config.protocol == "gpt" {
		body = map[string]any{
			"plan_key": firstNonEmpty(config.planKey, task.Plan.Code, "plus"),
			"country":  config.country,
			"currency": config.currency,
			"session":  sessionPayload,
			"new_card": map[string]any{
				"number":    reservedCard.number,
				"exp_month": expiryMonth(reservedCard.expiry),
				"exp_year":  expiryYear(reservedCard.expiry),
				"cvc":       reservedCard.cvc,
				"name":      firstNonEmpty(reservedCard.holder, "API User"),
				"country":   config.country,
			},
			"client_ref": fmt.Sprintf("kc-cdk-%s-%s", firstNonEmpty(task.CDKCode, task.ID), task.ID),
		}
		if expiryMonth(reservedCard.expiry) == 0 || expiryYear(reservedCard.expiry) == 0 {
			return nil, fmt.Errorf("银行卡有效期格式错误，应为 MMYY、MM/YY 或 MM/YYYY")
		}
		if proxy != "" {
			body["proxy"] = proxy
		}
		headers["X-API-Key"] = config.apiKey
		headers["Idempotency-Key"] = fmt.Sprintf("cdk-%s", firstNonEmpty(task.CDKCode, task.ID))
	} else {
		body = map[string]any{"task_id": task.ID, "plan_code": firstNonEmpty(task.Plan.Code, "plus"), "session": secret, "idempotency_key": task.ID}
	}
	created, err := doUpstreamJSON(ctx, client, http.MethodPost, joinUpstreamURL(config.baseURL, chooseCreatePath(config)), headers, body)
	if err != nil {
		return nil, fmt.Errorf("代充提交失败: %w", err)
	}
	lastRaw = created
	orderID = extractUpstreamOrderID(created)
	providerTaskID = extractUpstreamTaskID(created)
	topupCode = extractUpstreamTopupCode(created)
	if orderID == "" {
		return nil, fmt.Errorf("代充提交成功但未返回订单号: %s", truncate(jsonText(created), 300))
	}
	if terminal := upstreamTerminalStatus(extractUpstreamStatus(created)); terminal != "" {
		if terminal == models.TaskSucceeded {
			if err := settleCard("", ""); err != nil {
				_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "银行卡已结束，但 Provider 销卡待重试: "+err.Error(), workerID, leaseToken)
			}
			message := upstreamSuccessMessage(config.protocol)
			if err := update(100, message, created, models.TaskSucceeded, ""); err != nil {
				return nil, err
			}
			return upstreamResult(orderID, providerTaskID, topupCode, 100, message, models.TaskSucceeded, traceID), nil
		}
		message := upstreamFailureMessage(config.protocol, created, extractUpstreamStatus(created))
		if err := settleCard("upstream_failed", message); err != nil {
			_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "银行卡已结束，但 Provider 销卡待重试: "+err.Error(), workerID, leaseToken)
		}
		if err := update(finalProgress(config.protocol, false), message, created, models.TaskFailed, "upstream_failed"); err != nil {
			return nil, err
		}
		return upstreamResult(orderID, providerTaskID, topupCode, finalProgress(config.protocol, false), message, models.TaskFailed, traceID), nil
	}
	if err := update(protocolProgress(config.protocol, 35, 25), fmt.Sprintf("代充订单已创建 %s，正在等待上游处理...", orderID), created, models.TaskRunning, ""); err != nil {
		return nil, err
	}

	for poll := 1; poll <= config.pollAttempts; poll++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(config.pollInterval):
		}
		progress := pollProgress(config.protocol, poll)
		queried, queryErr := s.queryUpstream(ctx, client, config, headers, orderID, providerTaskID)
		if queryErr != nil {
			if err := update(progress, fmt.Sprintf("状态轮询第 %d 次失败: %s", poll, queryErr), lastRaw, models.TaskRunning, ""); err != nil {
				return nil, err
			}
			continue
		}
		lastRaw = queried
		if code := extractUpstreamTopupCode(queried); code != "" {
			topupCode = code
		}
		rawStatus := extractUpstreamStatus(queried)
		terminal := upstreamTerminalStatus(rawStatus)
		if terminal != "" {
			if terminal == models.TaskSucceeded && boolValueIfPresent(nestedObject(queried, "result"), "ok", true) {
				if err := settleCard("", ""); err != nil {
					_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "银行卡已结束，但 Provider 销卡待重试: "+err.Error(), workerID, leaseToken)
				}
				message := upstreamSuccessMessage(config.protocol)
				if config.protocol == "generic" {
					message = firstNonEmpty(stringValueMap(queried, "message"), "上游代充完成")
				}
				if err := update(100, message, queried, models.TaskSucceeded, ""); err != nil {
					return nil, err
				}
				return upstreamResult(orderID, providerTaskID, topupCode, 100, message, models.TaskSucceeded, traceID), nil
			}
			message := upstreamFailureMessage(config.protocol, queried, rawStatus)
			if err := settleCard("upstream_failed", message); err != nil {
				_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "银行卡已结束，但 Provider 销卡待重试: "+err.Error(), workerID, leaseToken)
			}
			if err := update(finalProgress(config.protocol, false), message, queried, models.TaskFailed, "upstream_failed"); err != nil {
				return nil, err
			}
			return upstreamResult(orderID, providerTaskID, topupCode, finalProgress(config.protocol, false), message, models.TaskFailed, traceID), nil
		}
		if err := update(progress, fmt.Sprintf("%s (%s)", upstreamProcessingMessage(config.protocol), firstNonEmpty(rawStatus, "pending")), queried, models.TaskRunning, ""); err != nil {
			return nil, err
		}
	}

	message := "第三方代充超时未完成，请稍后在第三方平台查询订单状态"
	status := models.TaskFailed
	progress := 99
	if config.protocol == "generic" {
		message = "上游订单轮询超时，请人工确认"
		status = models.TaskManual
		progress = 92
	}
	if err := settleCard("upstream_timeout", message); err != nil {
		_ = s.appendTaskRuntimeLog(task.ID, traceID, "warn", "upstream", "银行卡已结束，但 Provider 销卡待重试: "+err.Error(), workerID, leaseToken)
	}
	if err := update(progress, message, lastRaw, status, "upstream_timeout"); err != nil {
		return nil, err
	}
	return upstreamResult(orderID, providerTaskID, topupCode, progress, message, status, traceID), nil
}

func (s *Server) queryUpstream(ctx context.Context, client *http.Client, config upstreamRuntimeConfig, headers map[string]string, orderID, taskID string) (map[string]any, error) {
	if config.protocol == "gpt" && taskID != "" {
		queried, err := doUpstreamJSON(ctx, client, http.MethodGet, joinUpstreamURL(config.baseURL, "/tasks/"+url.PathEscape(taskID)), headers, nil)
		if err == nil && extractUpstreamStatus(queried) != "" {
			return queried, nil
		}
	}
	path := config.statusPath
	if config.protocol == "gpt" {
		path = "/pay/orders/" + url.PathEscape(orderID)
	} else {
		path = strings.Replace(path, ":id", url.PathEscape(orderID), 1)
	}
	return doUpstreamJSON(ctx, client, http.MethodGet, joinUpstreamURL(config.baseURL, path), headers, nil)
}

func (s *Server) activeUpstreamProxy() (string, error) {
	var proxy models.ProxyAsset
	query := s.DB.Where("active = ?", true).Order("RANDOM()").First(&proxy)
	if query.Error == gorm.ErrRecordNotFound {
		value := firstNonEmpty(s.configValue("proxy", ""), s.Cfg.OutboundProxy)
		return strings.ReplaceAll(value, "{session}", strings.TrimPrefix(db.NewID("session"), "session_")), nil
	}
	if query.Error != nil {
		return "", query.Error
	}
	value := strings.ReplaceAll(proxy.ProxyURL, "{session}", strings.TrimPrefix(db.NewID("session"), "session_"))
	if err := s.DB.Model(&proxy).UpdateColumn("usage_count", gorm.Expr("usage_count + 1")).Error; err != nil {
		return "", err
	}
	return value, nil
}

func (s *Server) reserveUpstreamCard(ctx context.Context, task models.RechargeTask, workerID, leaseToken, owner string) (*upstreamCard, error) {
	payload, err := s.internalReserveCardContext(ctx, cardReservationInput{
		OwnerKey: owner, TaskID: task.ID, JobKey: task.JobKey, PoolID: task.PoolID,
		BusinessAccountID: task.BusinessAccountID, UsageType: task.UsageType,
		IdempotencyKey: "task:" + task.ID, WorkerID: workerID, LeaseToken: leaseToken,
	})
	if err != nil {
		return nil, err
	}
	value, ok := payload["card"]
	if !ok || value == nil {
		return nil, nil
	}
	card, ok := value.(map[string]any)
	if !ok {
		if typed, typedOK := value.(gin.H); typedOK {
			card = map[string]any(typed)
		} else {
			return nil, fmt.Errorf("银行卡池返回格式不正确")
		}
	}
	return &upstreamCard{
		id: mapString(card, "id"), allocationID: firstNonEmpty(mapString(card, "allocationId"), mapString(card, "allocation_id"), mapString(payload, "allocationId"), mapString(payload, "allocation_id")),
		provider: mapString(payload, "provider"), last4: mapString(card, "last4"), number: mapString(card, "card_number"),
		expiry: mapString(card, "card_expiry"), cvc: mapString(card, "card_cvc"), holder: mapString(card, "card_holder"),
	}, nil
}

func (s *Server) releaseUpstreamCard(ctx context.Context, card *upstreamCard) error {
	if card == nil || strings.TrimSpace(card.id) == "" {
		return nil
	}
	if s.CardPools != nil {
		return s.CardPools.ReleaseCardForAllocation(ctx, card.id, card.allocationID)
	}
	return s.releaseCardAsset(card.id)
}

func (s *Server) releaseCardAsset(cardID string) error {
	if strings.TrimSpace(cardID) == "" {
		return nil
	}
	return s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error
}

func (s *Server) consumeUpstreamCard(ctx context.Context, card *upstreamCard) error {
	if card == nil {
		return nil
	}
	if s.CardPools != nil {
		if _, err := s.CardPools.RecordUsageForAllocation(ctx, card.id, card.allocationID); err != nil {
			return err
		}
		return s.CardPools.ReleaseCardForAllocation(ctx, card.id, card.allocationID)
	}
	_, err := s.internalRecordCardUsage(card.id)
	return err
}

func (s *Server) appendTaskRuntimeLog(taskID, traceID, level, source, message string, leaseCredentials ...string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var task models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "job_key", "trace_id", "worker_id", "worker_lease_token", "lease_expires_at", "status").First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		if len(leaseCredentials) > 0 {
			workerID, leaseToken := "", ""
			if len(leaseCredentials) > 0 {
				workerID = leaseCredentials[0]
			}
			if len(leaseCredentials) > 1 {
				leaseToken = leaseCredentials[1]
			}
			if err := validateTaskLease(&task, workerID, leaseToken, time.Now()); err != nil {
				return err
			}
		}
		return tx.Create(&models.RuntimeLog{
			ID: db.NewID("runtime"), JobKey: task.JobKey,
			TraceID: firstNonEmpty(task.TraceID, traceID), Level: level,
			Source: source, Text: truncate(message, 32768),
		}).Error
	})
}

func doUpstreamJSON(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, body map[string]any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, upstreamResponseLimit))
	if err != nil {
		return nil, err
	}
	var payload any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			payload = string(raw)
		}
	}
	data, ok := payload.(map[string]any)
	if !ok {
		data = map[string]any{}
		if text, textOK := payload.(string); textOK && strings.TrimSpace(text) != "" {
			data["_raw"] = text
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("上游 API 请求失败: %s", upstreamDetail(data, fmt.Sprintf("HTTP %d", response.StatusCode)))
	}
	return data, nil
}

func joinUpstreamURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func chooseCreatePath(config upstreamRuntimeConfig) string {
	if config.protocol == "gpt" {
		return "/pay"
	}
	return config.createPath
}

func sessionBodyObject(value string) map[string]any {
	var result map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(value)), &result) == nil && result != nil {
		return result
	}
	return map[string]any{"access_token": value}
}

func extractUpstreamOrderID(data map[string]any) string {
	return firstNonEmpty(mapString(data, "order_id"), nestedString(data, "order", "id"), mapString(data, "orderId"), mapString(data, "pay_order_id"), mapString(data, "id"))
}

func extractUpstreamTaskID(data map[string]any) string {
	return firstNonEmpty(mapString(data, "task_id"), nestedString(data, "task", "id"), mapString(data, "taskId"))
}

func extractUpstreamTopupCode(data map[string]any) string {
	return firstNonEmpty(mapString(data, "topup_code"), nestedString(data, "order", "topup_code"))
}

func extractUpstreamStatus(data map[string]any) string {
	result := nestedObject(data, "result")
	if value := mapString(result, "status"); value != "" {
		return value
	}
	outer := firstNonEmpty(mapString(data, "status"), mapString(data, "state"), nestedString(data, "order", "status"), nestedString(data, "task", "status"))
	if strings.EqualFold(outer, "done") && result != nil && !boolValueIfPresent(result, "ok", true) {
		return "failed"
	}
	return outer
}

func upstreamTerminalStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range []string{"success", "succeeded", "completed", "complete", "done", "paid", "active", "开通成功", "成功"} {
		if strings.Contains(value, strings.ToLower(item)) {
			return models.TaskSucceeded
		}
	}
	for _, item := range []string{"failed", "failure", "declined", "canceled", "cancelled", "error", "expired", "rejected", "失败", "取消"} {
		if strings.Contains(value, strings.ToLower(item)) {
			return models.TaskFailed
		}
	}
	return ""
}

func upstreamSuccessMessage(protocol string) string {
	if protocol == "gpt" {
		return "第三方代充开通成功"
	}
	return "上游代充完成"
}

func upstreamProcessingMessage(protocol string) string {
	if protocol == "gpt" {
		return "上游处理中"
	}
	return "等待上游订单确认"
}

func upstreamFailureMessage(protocol string, data map[string]any, status string) string {
	result := nestedObject(data, "result")
	detail := firstNonEmpty(mapString(result, "error"), mapString(data, "error"), mapString(result, "status"), status, "unknown")
	if protocol == "gpt" {
		return "第三方代充失败: " + detail
	}
	return "上游代充失败: " + detail
}

func finalProgress(protocol string, success bool) int {
	if success {
		return 100
	}
	if protocol == "gpt" {
		return 99
	}
	return 92
}

func protocolProgress(protocol string, gptValue, genericValue int) int {
	if protocol == "gpt" {
		return gptValue
	}
	return genericValue
}

func pollProgress(protocol string, poll int) int {
	if protocol == "gpt" {
		return minInt(95, 40+poll)
	}
	return minInt(92, 25+poll)
}

func upstreamResult(orderID, taskID, topupCode string, progress int, message, status, traceID string) map[string]any {
	return map[string]any{"status": status, "progress": progress, "message": message, "orderId": orderID, "upstreamOrderId": orderID, "gptApiOrderId": orderID, "gptApiTaskId": taskID, "gptApiTopupCode": topupCode, "traceId": traceID, "trace_id": traceID}
}

func classifyUpstreamError(message string) string {
	value := strings.ToLower(message)
	switch {
	case strings.Contains(value, "银行卡池") || strings.Contains(value, "卡池"):
		return "card_pool_exhausted"
	case strings.Contains(value, "session"):
		return "session_invalid"
	case strings.Contains(value, "超时") || strings.Contains(value, "timeout"):
		return "upstream_timeout"
	case strings.Contains(value, "订单") || strings.Contains(value, "api"):
		return "upstream_request_failed"
	default:
		return "upstream_failed"
	}
}

func errorMessageForStatus(status, message string) string {
	if status == models.TaskSucceeded {
		return ""
	}
	return strings.TrimSpace(message)
}

func expiryMonth(value string) int {
	match := cardExpiryPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0
	}
	month, _ := strconv.Atoi(match[1])
	return month
}

func expiryYear(value string) int {
	match := cardExpiryPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0
	}
	year, _ := strconv.Atoi(match[2])
	if len(match[2]) == 2 {
		year += 2000
	}
	return year
}

func cardLast4(card *upstreamCard) string {
	if card == nil {
		return ""
	}
	return firstNonEmpty(card.last4, suffix(card.number, 4))
}

func suffix(value string, count int) string {
	value = strings.TrimSpace(value)
	if len(value) <= count {
		return value
	}
	return value[len(value)-count:]
}

func mapString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func nestedObject(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}

func nestedString(data map[string]any, parent, key string) string {
	return mapString(nestedObject(data, parent), key)
}

func stringValueMap(data map[string]any, key string) string {
	return mapString(data, key)
}

func boolValue(data map[string]any, key string) bool {
	return boolValueIfPresent(data, key, false)
}

func boolValueIfPresent(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	value, ok := data[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return strings.EqualFold(fmt.Sprint(value), "true")
	}
}

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func upstreamDetail(data map[string]any, fallback string) string {
	if data == nil {
		return fallback
	}
	for _, key := range []string{"detail", "message", "error", "msg"} {
		if value, ok := data[key]; ok && value != nil {
			if text, textOK := value.(string); textOK && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
			raw := jsonText(value)
			if raw != "" && raw != "null" && raw != "{}" {
				return truncate(raw, 300)
			}
		}
	}
	return fallback
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func intPointer(value int) *int { return &value }

func stringPointer(value string) *string { return &value }
