package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/paymentregion"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Server struct {
	DB        *gorm.DB
	Cfg       config.Config
	Q         *queue.Client
	CardPools *cardpool.Service
}

func NewRouter(server *Server) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), cors(server.Cfg.CORSOrigins), traceMiddleware)
	router.GET("/healthz", server.health)

	api := router.Group("/api/v1")
	api.GET("/plans", server.listPlans)
	api.GET("/runtime", server.runtime)
	store := api.Group("/store")
	store.POST("/orders", server.createStoreOrder)
	store.GET("/orders/:id", server.getStoreOrder)
	store.POST("/orders/query", server.queryStoreOrders)
	router.POST("/api/v1/store/webhooks/stripe", server.handleStripeWebhook)
	cardWebhooks := router.Group("/api/v1/webhooks/cards")
	cardWebhooks.POST("/airwallex", func(c *gin.Context) { server.handleCardProviderWebhook(c, "AIRWALLEX") })
	cardWebhooks.POST("/stripe", func(c *gin.Context) { server.handleCardProviderWebhook(c, "STRIPE_ISSUING") })
	cardWebhooks.POST("/photonpay", func(c *gin.Context) { server.handleCardProviderWebhook(c, "PHOTONPAY") })
	cardWebhooks.POST("/dogpay", func(c *gin.Context) { server.handleCardProviderWebhook(c, "DOGPAY") })
	cardWebhooks.POST("/kimoox", func(c *gin.Context) { server.handleCardProviderWebhook(c, "KIMOOX") })

	recharge := api.Group("/recharge")
	recharge.POST("/verify", server.verifyCDK)
	recharge.POST("/tasks", server.createTask)
	recharge.GET("/tasks/:id", server.getTask)

	admin := api.Group("/admin", server.requireAdmin)
	admin.GET("/overview", server.adminOverview)
	admin.GET("/plans", server.listPlans)
	admin.GET("/store/products", server.adminStoreProducts)
	admin.POST("/store/products", server.upsertStoreProduct)
	admin.PATCH("/store/products/:code", server.updateStoreProduct)
	admin.POST("/store/products/:code/toggle", server.toggleStoreProduct)
	admin.GET("/tasks", server.adminTasks)
	admin.GET("/cdks", server.adminCDKs)
	admin.POST("/cdks", server.generateCDKs)
	admin.POST("/cdks/import", server.importCDKs)
	admin.POST("/products/generate", server.generateProducts)
	admin.POST("/products/resume", server.resumeProducts)
	admin.POST("/products/generate-stop", server.stopProducts)
	admin.GET("/product-generations/:jobKey", server.getProductGeneration)
	admin.GET("/cards", server.adminCards)
	admin.GET("/cards/:id/activity", server.adminCardActivity)
	admin.POST("/cards/create", server.adminCreateProviderCard)
	admin.POST("/cards/import", server.importCards)
	admin.GET("/card-pools", server.adminCardPools)
	admin.POST("/card-pools", server.createCardPool)
	admin.PATCH("/card-pools/:id", server.updateCardPool)
	admin.GET("/billing", server.adminBilling)
	admin.GET("/config", server.getConfig)
	admin.GET("/card-providers/kimoox/bins", server.legacyKimooxCardBINs)
	admin.PUT("/config", server.saveConfig)
	admin.POST("/email/test", server.legacyTestEmail)

	internal := api.Group("/internal", server.requireWorker)
	internal.GET("/config", server.getWorkerConfig)
	internal.POST("/tasks/:id/claim", server.claimTask)
	internal.POST("/tasks/:id/heartbeat", server.heartbeatTask)
	internal.GET("/tasks/:id/runtime", server.taskRuntime)
	internal.POST("/tasks/:id/upstream", server.runUpstreamEndpoint)
	internal.GET("/tasks/:id/secret", server.taskSecret)
	internal.PATCH("/tasks/:id", server.updateTask)
	internal.GET("/product-generations/:id", server.productGenerationSecret)
	internal.PATCH("/product-generations/:id", server.updateProductGeneration)
	internal.POST("/runtime-logs", server.appendRuntimeLog)
	internal.POST("/store/:action", server.internalStore)

	registerLegacyRoutes(router, server)

	return router
}

func (s *Server) health(c *gin.Context) {
	if err := s.Q.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "service": "api", "redis": "down"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "service": "api", "redis": "up"})
}

func (s *Server) listPlans(c *gin.Context) {
	var plans []models.Plan
	if err := s.DB.Where("active = ?", true).Order("sort_order ASC, code ASC").Find(&plans).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取套餐失败")
		return
	}
	for index := range plans {
		plans[index].Currency = models.PlatformStoreCurrency
		decoratePlanInventory(&plans[index])
	}
	c.JSON(http.StatusOK, gin.H{"plans": plans})
}

func (s *Server) runtime(c *gin.Context) {
	var running int64
	if err := s.DB.Model(&models.RechargeTask{}).Where("status IN ?", []string{models.TaskQueued, models.TaskRunning}).Count(&running).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取运行中任务数量失败")
		return
	}
	var total int64
	if err := s.DB.Model(&models.RechargeTask{}).Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取任务总数失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"runningTasks": running, "totalTasks": total})
}

func (s *Server) verifyCDK(c *gin.Context) {
	var input struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Code) == "" {
		fail(c, http.StatusBadRequest, "请输入兑换码")
		return
	}

	var cdk models.CDK
	result := s.DB.Preload("Plan").Where("code = ?", strings.TrimSpace(input.Code)).First(&cdk)
	if result.Error == gorm.ErrRecordNotFound {
		fail(c, http.StatusNotFound, "兑换码无效")
		return
	}
	if result.Error != nil {
		publicFail(c, http.StatusInternalServerError, "查询兑换码失败")
		return
	}
	if cdk.Status == models.CDKDisabled {
		fail(c, http.StatusGone, "兑换码已停用")
		return
	}
	if cdk.Status == models.CDKUsed {
		fail(c, http.StatusConflict, "兑换码已使用")
		return
	}
	if cdk.Status == models.CDKProcessing {
		var task models.RechargeTask
		if err := s.DB.Where("cdk_id = ?", cdk.ID).Order("created_at DESC").First(&task).Error; err != nil && err != gorm.ErrRecordNotFound {
			publicFail(c, http.StatusInternalServerError, "读取兑换任务失败")
			return
		}
		response := gin.H{"valid": true, "status": cdk.Status}
		if task.ID != "" {
			response["task"] = publicTaskResponse(task)
		}
		c.JSON(http.StatusOK, response)
		return
	}
	if cdk.Type != "" && cdk.Type != models.CDKTypeSelf {
		fail(c, http.StatusForbidden, "CDK 不是自助激活码")
		return
	}
	if cdk.CooldownUntil != nil && cdk.CooldownUntil.After(time.Now()) {
		fail(c, http.StatusForbidden, "该卡密连续无资格尝试过多，请稍后再试")
		return
	}
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		var limit models.ActivationAttemptLimit
		if err := s.DB.Where("scope_type = ? AND scope_key = ?", "ip", ip).First(&limit).Error; err == nil && limit.CooldownUntil != nil && limit.CooldownUntil.After(time.Now()) {
			fail(c, http.StatusForbidden, "当前 IP 连续无资格尝试过多，请稍后再试")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"valid":  true,
		"status": cdk.Status,
		"cdk": gin.H{
			"code": cdk.Code,
			"plan": cdk.Plan,
		},
	})
}

func (s *Server) createTask(c *gin.Context) {
	var input struct {
		Code              string `json:"code"`
		Session           string `json:"session"`
		Mode              string `json:"mode"`
		PoolID            string `json:"poolId"`
		BusinessAccountID string `json:"businessAccountId"`
		UsageType         string `json:"usageType"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	code := strings.TrimSpace(input.Code)
	sessionRaw, sessionToken, err := security.NormalizeSessionPayload(input.Session)
	if code == "" || err != nil {
		fail(c, http.StatusBadRequest, "请提供兑换码和有效 Session")
		return
	}
	if err := security.ValidateAccessToken(sessionToken); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	mode := s.resolveRechargeMode(input.Mode)
	if usage := strings.ToUpper(strings.TrimSpace(input.UsageType)); usage != "" && usage != string(cardpool.UsageOneTime) && usage != string(cardpool.UsageRecurring) && usage != string(cardpool.UsageMultiUse) {
		fail(c, http.StatusBadRequest, "usageType 不支持")
		return
	}
	if poolID := strings.TrimSpace(input.PoolID); poolID != "" {
		var pool models.CardPool
		if err := s.DB.Where("id = ? AND enabled = ?", poolID, true).First(&pool).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fail(c, http.StatusBadRequest, "银行卡池不存在或已停用")
				return
			}
			publicFail(c, http.StatusInternalServerError, "读取银行卡池失败")
			return
		}
	}
	if !s.guardOrAbortForPool(c, code, mode, c.ClientIP(), strings.TrimSpace(input.PoolID), subscriptionEmailFromRaw(sessionRaw, sessionToken)) {
		return
	}
	ciphertext, err := security.Encrypt(sessionRaw, s.Cfg.SessionEncryptionKey)
	if err != nil {
		publicFail(c, http.StatusInternalServerError, "Session 加密失败")
		return
	}
	admissionToken, admissionErr := s.acquireAdmission(c.Request.Context())
	if admissionErr != nil {
		if errors.Is(admissionErr, errActivationCapacity) {
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "当前任务过多，请稍后再试"})
			return
		}
		publicFail(c, http.StatusServiceUnavailable, "当前任务暂不可用，请稍后再试")
		return
	}

	var task models.RechargeTask
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.checkActivationCapacityTx(tx); err != nil {
			return err
		}
		var cdk models.CDK
		if queryErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Plan").Where("code = ?", code).First(&cdk).Error; queryErr != nil {
			return queryErr
		}
		if cdk.Status != models.CDKAvailable || (cdk.Type != "" && cdk.Type != models.CDKTypeSelf) {
			return gorm.ErrDuplicatedKey
		}
		now := time.Now()
		queueDeadline := now.Add(s.taskQueuedTimeout())
		cdkID := cdk.ID
		task = models.RechargeTask{
			ID:                db.NewID("task"),
			JobKey:            db.NewID("job"),
			TraceID:           requestTraceID(c),
			CDKID:             &cdkID,
			PlanID:            cdk.PlanID,
			PoolID:            strings.TrimSpace(input.PoolID),
			PaymentRegion:     paymentregion.Normalize(cdk.Plan.Country),
			BusinessAccountID: strings.TrimSpace(input.BusinessAccountID),
			UsageType:         strings.ToUpper(strings.TrimSpace(input.UsageType)),
			Mode:              mode,
			Status:            models.TaskQueued,
			Progress:          1,
			Message:           "任务已排队",
			SessionPreview:    security.SessionPreview(sessionToken),
			TokenPreview:      security.SessionPreview(sessionToken),
			SessionCiphertext: ciphertext,
			AdmissionToken:    admissionToken,
			CDKCode:           code,
			ClientIP:          c.ClientIP(),
			QueueDeadlineAt:   &queueDeadline,
			DisplayTime:       now.Format("2006-01-02 15:04:05"),
		}
		if createErr := tx.Create(&task).Error; createErr != nil {
			return createErr
		}
		if err := tx.Model(&cdk).Updates(map[string]any{
			"status":          models.CDKProcessing,
			"used_by_task_id": task.ID,
		}).Error; err != nil {
			return err
		}
		cdk.Status = models.CDKProcessing
		cdk.UsedByTaskID = task.ID
		task.CDK = cdk
		task.Plan = cdk.Plan
		return nil
	})
	if err != nil {
		s.releaseAdmission(admissionToken)
		if errors.Is(err, errActivationCapacity) {
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "当前任务过多，请稍后再试"})
			return
		}
		if err == gorm.ErrRecordNotFound {
			fail(c, http.StatusNotFound, "兑换码无效")
			return
		}
		fail(c, http.StatusConflict, "兑换码不可用或正在处理")
		return
	}

	if err := s.Q.Enqueue(c.Request.Context(), queue.TaskMessage{TaskID: task.ID, Mode: mode, TraceID: task.TraceID}); err != nil {
		s.releaseTask(task.ID, "queue_unavailable", "任务入队失败，请稍后重试")
		publicFail(c, http.StatusServiceUnavailable, "任务队列暂不可用")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"task": publicTaskResponse(task), "message": publicTaskMessage(models.TaskQueued)})
}

func (s *Server) getTask(c *gin.Context) {
	var task models.RechargeTask
	result := s.DB.Preload("CDK").Preload("Plan").Where("id = ?", c.Param("id")).First(&task)
	if result.Error == gorm.ErrRecordNotFound {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	if result.Error != nil {
		publicFail(c, http.StatusInternalServerError, "读取任务失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": publicTaskResponse(task)})
}

func (s *Server) taskSecret(c *gin.Context) {
	var task models.RechargeTask
	if err := s.DB.First(&task, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	if err := validateTaskLeaseHeaders(c, &task); err != nil {
		if errors.Is(err, errTaskLeaseInput) || errors.Is(err, errTaskLeaseLost) {
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
			return
		}
		publicFail(c, http.StatusInternalServerError, "读取任务租约失败")
		return
	}
	secret, err := s.decryptSessionValue(task.SessionCiphertext)
	if err != nil {
		fail(c, http.StatusInternalServerError, "无法解密任务 Session")
		return
	}
	var sessionData map[string]any
	_ = json.Unmarshal([]byte(secret), &sessionData)
	token := secret
	for _, key := range []string{"accessToken", "access_token", "token"} {
		if value, ok := sessionData[key].(string); ok && strings.TrimSpace(value) != "" {
			token = strings.TrimSpace(value)
			break
		}
	}
	var cdk models.CDK
	cdkID := ""
	if task.CDKID != nil {
		cdkID = strings.TrimSpace(*task.CDKID)
	}
	if cdkID != "" {
		if err := s.DB.Preload("Plan").First(&cdk, "id = ?", cdkID).Error; err != nil && err != gorm.ErrRecordNotFound {
			fail(c, http.StatusInternalServerError, "读取任务 CDK 失败")
			return
		}
	}
	var plan models.Plan
	if task.PlanID != "" {
		if err := s.DB.First(&plan, "id = ?", task.PlanID).Error; err != nil && err != gorm.ErrRecordNotFound {
			fail(c, http.StatusInternalServerError, "读取任务套餐失败")
			return
		}
	}
	planType := normalizePlanType(firstNonEmpty(cdk.PlanType, plan.Code, "plus"))
	planName := db.NormalizeProviderPlanName(planType, firstNonEmpty(task.PlanNameOverride, cdk.Plan.ProviderPlanName, plan.ProviderPlanName, plan.Code, planType))
	cdkCode := firstNonEmpty(cdk.Code, task.CDKCode)
	region := paymentregion.Normalize(task.PaymentRegion)
	if region == "" {
		region = paymentregion.Normalize(plan.Country)
	}
	if region == "" {
		region = paymentregion.Normalize(s.configValue("payment_region", paymentregion.Default().Code))
	}
	c.JSON(http.StatusOK, gin.H{"taskId": task.ID, "jobKey": task.JobKey, "traceId": task.TraceID, "trace_id": task.TraceID, "session": secret, "token": token, "mode": task.Mode, "planId": task.PlanID, "planType": planType, "cdkCode": cdkCode, "region": region, "paymentRegion": region, "currency": regionCurrency(region), "planName": planName})
}

func (s *Server) updateTask(c *gin.Context) {
	var input struct {
		Status             string  `json:"status"`
		Progress           *int    `json:"progress"`
		Message            *string `json:"message"`
		WorkerID           *string `json:"workerId"`
		LeaseToken         *string `json:"leaseToken"`
		UpstreamOrderID    *string `json:"upstreamOrderId"`
		ErrorCode          *string `json:"errorCode"`
		ErrorMessage       *string `json:"errorMessage"`
		RawOutput          *string `json:"rawOutput"`
		FailureScreenshots *string `json:"failureScreenshots"`
		CardLast4          *string `json:"cardLast4"`
		GPTAPIOrderID      *string `json:"gptApiOrderId"`
		GPTAPITaskID       *string `json:"gptApiTaskId"`
		GPTAPIRaw          *string `json:"gptApiRaw"`
		GPTAPITopupCode    *string `json:"gptApiTopupCode"`
		Attempt            *int    `json:"attempt"`
		ClientIP           *string `json:"clientIp"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if !validTaskStatus(input.Status) {
		fail(c, http.StatusBadRequest, "无效任务状态")
		return
	}
	update := taskUpdateInput{
		Status: input.Status, Progress: input.Progress, Message: input.Message, WorkerID: input.WorkerID, LeaseToken: input.LeaseToken,
		UpstreamOrderID: input.UpstreamOrderID, ErrorCode: input.ErrorCode, ErrorMessage: input.ErrorMessage,
		RawOutput: input.RawOutput, FailureScreenshots: input.FailureScreenshots, CardLast4: input.CardLast4,
		GPTAPIOrderID: input.GPTAPIOrderID, GPTAPITaskID: input.GPTAPITaskID, GPTAPIRaw: input.GPTAPIRaw,
		GPTAPITopupCode: input.GPTAPITopupCode, Attempt: input.Attempt, ClientIP: input.ClientIP,
	}
	notifyEvent, notifyPayload, err := s.applyTaskUpdate(c.Param("id"), update)
	if err == gorm.ErrRecordNotFound {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	if errors.Is(err, errTaskLeaseInput) {
		taskLeaseError(c, http.StatusBadRequest, "task_lease_required", err)
		return
	}
	if errors.Is(err, errTaskLeaseLost) {
		taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "更新任务失败")
		return
	}
	if notifyEvent != "" {
		go s.notifyTelegramEvent(notifyEvent, notifyPayload)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "traceId": requestTraceID(c), "trace_id": requestTraceID(c)})
}

func (s *Server) adminOverview(c *gin.Context) {
	var cdkTotal, cdkAvailable, cdkUsed, taskTotal, taskRunning, taskSucceeded, cardActive int64
	queries := []struct {
		destination *int64
		query       *gorm.DB
	}{
		{&cdkTotal, s.DB.Model(&models.CDK{})},
		{&cdkAvailable, s.DB.Model(&models.CDK{}).Where("status = ?", models.CDKAvailable)},
		{&cdkUsed, s.DB.Model(&models.CDK{}).Where("status = ?", models.CDKUsed)},
		{&taskTotal, s.DB.Model(&models.RechargeTask{})},
		{&taskRunning, s.DB.Model(&models.RechargeTask{}).Where("status IN ?", []string{models.TaskQueued, models.TaskRunning})},
		{&taskSucceeded, s.DB.Model(&models.RechargeTask{}).Where("status = ?", models.TaskSucceeded)},
		{&cardActive, s.DB.Model(&models.CardAsset{}).Where("active = ?", true).
			Where("status NOT IN ?", []string{"报废", "已报废", "disabled"}).
			Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now())},
	}
	for _, item := range queries {
		if err := item.query.Count(item.destination).Error; err != nil {
			fail(c, http.StatusInternalServerError, "读取概览指标失败")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"overview": gin.H{
		"cdkTotal": cdkTotal, "cdkAvailable": cdkAvailable, "cdkUsed": cdkUsed,
		"taskTotal": taskTotal, "taskRunning": taskRunning, "taskSucceeded": taskSucceeded,
		"cardActive": cardActive,
	}})
}

func (s *Server) adminTasks(c *gin.Context) {
	limit := parseLimit(c.Query("limit"), 100)
	var tasks []models.RechargeTask
	if err := s.DB.Preload("CDK").Preload("Plan").Order("created_at DESC").Limit(limit).Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取任务失败")
		return
	}
	items := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskResponse(task))
	}
	c.JSON(http.StatusOK, gin.H{"tasks": items})
}

func (s *Server) adminCDKs(c *gin.Context) {
	limit := parseLimit(c.Query("limit"), 200)
	var cdks []models.CDK
	if err := s.DB.Preload("Plan").Order("created_at DESC").Limit(limit).Find(&cdks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取兑换码失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"cdks": cdks})
}

func (s *Server) generateCDKs(c *gin.Context) {
	var input struct {
		PlanCode string `json:"planCode"`
		Quantity int    `json:"quantity"`
		Prefix   string `json:"prefix"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	quantity := input.Quantity
	if quantity < 1 || quantity > 500 {
		fail(c, http.StatusBadRequest, "生成数量必须在 1 到 500 之间")
		return
	}
	var plan models.Plan
	if err := s.DB.Where("code = ? AND active = ?", strings.TrimSpace(input.PlanCode), true).First(&plan).Error; err != nil {
		fail(c, http.StatusBadRequest, "套餐不存在或已下架")
		return
	}
	prefix := strings.ToUpper(strings.TrimSpace(input.Prefix))
	if prefix == "" {
		prefix = "KC"
	}
	created := make([]models.CDK, 0, quantity)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < quantity; i++ {
			code, err := newCDKCode(prefix)
			if err != nil {
				return err
			}
			cdk := models.CDK{ID: db.NewID("cdk"), Code: code, PlanID: plan.ID, PlanType: db.PlanTypeForPlan(plan), Type: models.CDKTypeSelf, Status: models.CDKAvailable}
			if err := tx.Create(&cdk).Error; err != nil {
				return err
			}
			cdk.Plan = plan
			created = append(created, cdk)
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成兑换码失败")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"cdks": created})
}

func (s *Server) importCDKs(c *gin.Context) {
	var input struct {
		PlanCode string   `json:"planCode"`
		Codes    []string `json:"codes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Codes) == 0 || len(input.Codes) > 500 {
		fail(c, http.StatusBadRequest, "请提供 1 到 500 个兑换码")
		return
	}
	var plan models.Plan
	if err := s.DB.Where("code = ? AND active = ?", strings.TrimSpace(input.PlanCode), true).First(&plan).Error; err != nil {
		fail(c, http.StatusBadRequest, "套餐不存在或已下架")
		return
	}
	created, duplicate := 0, 0
	for _, raw := range input.Codes {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		result := s.DB.Create(&models.CDK{ID: db.NewID("cdk"), Code: code, PlanID: plan.ID, PlanType: db.PlanTypeForPlan(plan), Type: models.CDKTypeSelf, Status: models.CDKAvailable})
		if result.Error != nil {
			duplicate++
			continue
		}
		created++
	}
	c.JSON(http.StatusCreated, gin.H{"created": created, "insertedCount": created, "duplicateCount": duplicate, "totalCount": created + duplicate})
}

func (s *Server) adminCards(c *gin.Context) {
	limit := parseLimit(c.Query("limit"), 200)
	var cards []models.CardAsset
	if err := s.DB.Order("created_at DESC").Limit(limit).Find(&cards).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取卡池失败")
		return
	}
	paymentCards := []cardpool.PaymentCard{}
	combined := make([]any, 0, len(cards))
	for _, card := range cards {
		combined = append(combined, card)
	}
	if s.CardPools != nil {
		if values, err := s.CardPools.ListCards(c.Query("poolId")); err == nil {
			paymentCards = values
			now := time.Now()
			for _, card := range paymentCards {
				// LOCAL_TEXT is already returned by the CardAsset query above.
				// Provider-issued cards have no CardAsset row and must be part of
				// the primary cards collection so all admin clients see one pool.
				if card.LocalCardAssetID != "" {
					continue
				}
				combined = append(combined, paymentCardAdminProjection(card, now))
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"cards": combined, "paymentCards": paymentCards})
}

func (s *Server) importCards(c *gin.Context) {
	var input struct {
		PoolID string `json:"poolId"`
		Cards  []struct {
			Number string `json:"number"`
			Expiry string `json:"expiry"`
			CVC    string `json:"cvc"`
			Holder string `json:"holder"`
		} `json:"cards"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Cards) == 0 || len(input.Cards) > 500 {
		fail(c, http.StatusBadRequest, "请提供 1 到 500 张卡片")
		return
	}
	created := 0
	poolID := firstNonEmpty(input.PoolID, s.configValue("card_pool_default_id", "pool_legacy"))
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range input.Cards {
			number := digitsOnly(item.Number)
			if len(number) < 12 || strings.TrimSpace(item.CVC) == "" {
				continue
			}
			numberCipher, err := security.Encrypt(number, s.Cfg.SessionEncryptionKey)
			if err != nil {
				return err
			}
			expiryCipher, err := security.Encrypt(strings.TrimSpace(item.Expiry), s.Cfg.SessionEncryptionKey)
			if err != nil {
				return err
			}
			cvcCipher, err := security.Encrypt(strings.TrimSpace(item.CVC), s.Cfg.SessionEncryptionKey)
			if err != nil {
				return err
			}
			asset := models.CardAsset{ID: db.NewID("card"), PoolID: poolID, Last4: number[len(number)-4:], CardNumberCiphertext: numberCipher, ExpiryCiphertext: expiryCipher, CVVCiphertext: cvcCipher, Holder: strings.TrimSpace(item.Holder), Status: "正常", Active: true}
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			created++
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "导入卡池失败")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"created": created})
}

func (s *Server) adminBilling(c *gin.Context) {
	limit := parseLimit(c.Query("limit"), 100)
	var records []models.BillingRecord
	if err := s.DB.Order("created_at DESC").Limit(limit).Find(&records).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取账单失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"billing": records})
}

func (s *Server) getConfig(c *gin.Context) {
	var values []models.AppConfig
	if err := s.DB.Find(&values).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取配置失败")
		return
	}
	result := map[string]string{"mode": s.Cfg.DefaultRechargeMode, "upstreamBaseURL": s.Cfg.UpstreamBaseURL, "upstreamCreatePath": s.Cfg.UpstreamCreatePath, "upstreamStatusPath": s.Cfg.UpstreamStatusPath}
	for key, value := range publicConfigValues(values) {
		result[key] = value
	}
	result["storeDebugMode"] = strconv.FormatBool(s.storeDebugMode())
	result["stripeSecretKeySaved"] = strconv.FormatBool(s.storeStripeSecretKey() != "")
	result["stripeWebhookSecretSaved"] = strconv.FormatBool(s.storeStripeWebhookSecret() != "")
	result["stripeSuccessURL"] = s.storeSuccessURL()
	result["stripeCancelURL"] = s.storeCancelURL()
	result["publicBaseURL"] = firstNonEmpty(s.configValue("public_base_url", ""), s.Cfg.PublicBaseURL)
	result["rechargeQueuedTimeoutSeconds"] = strconv.Itoa(int(s.taskQueuedTimeout() / time.Second))
	result["rechargeTaskLeaseTimeoutSeconds"] = strconv.Itoa(int(s.taskLeaseTimeout() / time.Second))
	if strings.TrimSpace(result["checkout_mode"]) == "" {
		result["checkout_mode"] = "api"
	}
	if strings.TrimSpace(result["worker_log_level"]) == "" {
		result["worker_log_level"] = "info"
	}
	for key, value := range s.emailConfigValues() {
		result[key] = value
	}
	result["airwallexApiKeySaved"] = strconv.FormatBool(s.configValue("airwallex_api_key", "") != "")
	result["airwallexClientIDSaved"] = strconv.FormatBool(s.configValue("airwallex_client_id", "") != "")
	result["airwallexCardholderIDSaved"] = strconv.FormatBool(s.configValue("airwallex_cardholder_id", "") != "")
	result["airwallexWebhookSecretSaved"] = strconv.FormatBool(s.configValue("airwallex_webhook_secret", "") != "")
	result["stripeIssuingSecretKeySaved"] = strconv.FormatBool(firstNonEmpty(s.configValue("stripe_issuing_secret_key", ""), s.configValue("stripe_secret_key", "")) != "")
	result["stripeIssuingCardholderIDSaved"] = strconv.FormatBool(s.configValue("stripe_issuing_cardholder_id", "") != "")
	result["stripeIssuingWebhookSecretSaved"] = strconv.FormatBool(firstNonEmpty(s.configValue("stripe_issuing_webhook_secret", ""), s.configValue("stripe_webhook_secret", "")) != "")
	result["photonpayAppIDSaved"] = strconv.FormatBool(s.configValue("photonpay_app_id", "") != "")
	result["photonpayAppSecretSaved"] = strconv.FormatBool(s.configValue("photonpay_app_secret", "") != "")
	result["photonpayCardholderIDSaved"] = strconv.FormatBool(s.configValue("photonpay_cardholder_id", "") != "")
	result["photonpayPrivateKeySaved"] = strconv.FormatBool(s.configValue("photonpay_private_key", "") != "")
	result["photonpayWebhookPublicKeySaved"] = strconv.FormatBool(s.configValue("photonpay_webhook_public_key", "") != "")
	result["dogpayAppIDSaved"] = strconv.FormatBool(s.configValue("dogpay_appid", "") != "")
	result["dogpaySecretSaved"] = strconv.FormatBool(s.configValue("dogpay_secret", "") != "")
	result["dogpayCardholderIDSaved"] = strconv.FormatBool(s.configValue("dogpay_cardholder_id", "") != "")
	result["dogpayPrivateKeySaved"] = strconv.FormatBool(s.configValue("dogpay_private_key", "") != "")
	result["dogpayWebhookSecretSaved"] = strconv.FormatBool(s.configValue("dogpay_webhook_secret", "") != "")
	result["kimooxAPIKeySaved"] = strconv.FormatBool(s.configValue("kimoox_api_key", "") != "")
	result["kimooxAPISecretSaved"] = strconv.FormatBool(s.configValue("kimoox_api_secret", "") != "")
	result["kimooxWebhookSecretSaved"] = strconv.FormatBool(s.configValue("kimoox_webhook_secret", "") != "")
	c.JSON(http.StatusOK, gin.H{"config": adminConfigView(result, s.storeDebugMode(), s.storeStripeSecretKey() != "", s.storeStripeWebhookSecret() != "")})
}

// adminConfigView keeps the legacy string contract for writes and adds typed
// read fields for the React admin view. Business state is normalized once here
// instead of being interpreted independently by each browser control.
func adminConfigView(result map[string]string, storeDebugMode, stripeSecretSaved, stripeWebhookSecretSaved bool) gin.H {
	view := gin.H{}
	for key, value := range result {
		view[key] = value
	}
	view["maintenanceModeEnabled"] = boolConfigValue(result["maintenance_mode"]) == "1"
	view["maintenanceModeDrainEnabled"] = boolConfigValue(result["maintenance_mode_drain"]) == "1"
	view["browserPoolEnabled"] = boolConfigValue(result["browser_pool_enabled"]) == "1"
	view["poolEmailEnabled"] = boolConfigValue(result["pool_email_enabled"]) == "1"
	view["poolEmailIncludeJunk"] = boolConfigValue(result["pool_email_include_junk"]) == "1"
	view["storeDebugModeEnabled"] = storeDebugMode
	view["stripeSecretKeySavedValue"] = stripeSecretSaved
	view["stripeWebhookSecretSavedValue"] = stripeWebhookSecretSaved
	view["emailEnabled"] = parseBoolConfig(result["emailEnabled"], boolConfigValue(result["email_enabled"]) == "1")
	view["emailNotifyPurchase"] = parseBoolConfig(result["emailNotifyPurchase"], boolConfigValue(result["email_notify_purchase"]) == "1")
	view["emailNotifyRedeem"] = parseBoolConfig(result["emailNotifyRedeem"], boolConfigValue(result["email_notify_redeem"]) == "1")
	view["emailSMTPUseTLS"] = parseBoolConfig(result["emailSMTPUseTLS"], boolConfigValue(result["email_smtp_use_tls"]) == "1")
	view["emailSMTPPasswordSavedValue"] = parseBoolConfig(result["emailSMTPPasswordSavedValue"], boolConfigValue(result["email_smtp_password_saved"]) == "1")
	view["airwallexWebhookSecretSavedValue"] = parseBoolConfig(result["airwallexWebhookSecretSaved"], false)
	view["airwallexCardholderIDSavedValue"] = parseBoolConfig(result["airwallexCardholderIDSaved"], false)
	view["stripeIssuingWebhookSecretSavedValue"] = parseBoolConfig(result["stripeIssuingWebhookSecretSaved"], false)
	view["stripeIssuingCardholderIDSavedValue"] = parseBoolConfig(result["stripeIssuingCardholderIDSaved"], false)
	view["photonpayAppIDSavedValue"] = parseBoolConfig(result["photonpayAppIDSaved"], false)
	view["photonpayAppSecretSavedValue"] = parseBoolConfig(result["photonpayAppSecretSaved"], false)
	view["photonpayCardholderIDSavedValue"] = parseBoolConfig(result["photonpayCardholderIDSaved"], false)
	view["photonpayPrivateKeySavedValue"] = parseBoolConfig(result["photonpayPrivateKeySaved"], false)
	view["photonpayWebhookPublicKeySavedValue"] = parseBoolConfig(result["photonpayWebhookPublicKeySaved"], false)
	view["dogpayAppIDSavedValue"] = parseBoolConfig(result["dogpayAppIDSaved"], false)
	view["dogpaySecretSavedValue"] = parseBoolConfig(result["dogpaySecretSaved"], false)
	view["dogpayCardholderIDSavedValue"] = parseBoolConfig(result["dogpayCardholderIDSaved"], false)
	view["dogpayPrivateKeySavedValue"] = parseBoolConfig(result["dogpayPrivateKeySaved"], false)
	view["dogpayWebhookSecretSavedValue"] = parseBoolConfig(result["dogpayWebhookSecretSaved"], false)
	view["kimooxAPIKeySavedValue"] = parseBoolConfig(result["kimooxAPIKeySaved"], false)
	view["kimooxAPISecretSavedValue"] = parseBoolConfig(result["kimooxAPISecretSaved"], false)
	view["kimooxWebhookSecretSavedValue"] = parseBoolConfig(result["kimooxWebhookSecretSaved"], false)
	return view
}

func publicConfigValues(values []models.AppConfig) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		if !isSensitiveConfigKey(value.Key) {
			result[value.Key] = value.Value
		}
	}
	return result
}

func (s *Server) getWorkerConfig(c *gin.Context) {
	hcaptcha := s.hcaptchaConfig()
	checkoutMode := strings.ToLower(strings.TrimSpace(s.configValue("checkout_mode", "api")))
	switch checkoutMode {
	case "api", "ui", "api_then_ui":
	default:
		checkoutMode = "api"
	}
	workerLogLevel := normalizeWorkerLogLevel(s.configValue("worker_log_level", "info"))
	config := map[string]string{
		"mode":                           s.configValue("mode", s.Cfg.DefaultRechargeMode),
		"upstreamBaseURL":                s.configValue("upstreamBaseURL", s.Cfg.UpstreamBaseURL),
		"upstreamCreatePath":             s.configValue("upstreamCreatePath", s.Cfg.UpstreamCreatePath),
		"upstreamStatusPath":             s.configValue("upstreamStatusPath", s.Cfg.UpstreamStatusPath),
		"browserCheckoutURL":             s.configValue("browserCheckoutURL", s.Cfg.BrowserCheckoutURL),
		"browserHeadless":                s.configValue("browserHeadless", strconv.FormatBool(s.Cfg.BrowserHeadless)),
		"runtimeDir":                     s.configValue("runtimeDir", s.Cfg.RuntimeDir),
		"paymentRegion":                  s.configValue("payment_region", "PH"),
		"checkoutMode":                   checkoutMode,
		"workerLogLevel":                 workerLogLevel,
		"hcaptchaSolverEnabled":          hcaptcha["hcaptcha_solver_enabled"],
		"hcaptchaVlmApiKey":              hcaptcha["hcaptcha_vlm_api_key"],
		"hcaptchaVlmBaseUrl":             hcaptcha["hcaptcha_vlm_base_url"],
		"hcaptchaVlmModel":               hcaptcha["hcaptcha_vlm_model"],
		"hcaptchaVlmTimeout":             hcaptcha["hcaptcha_vlm_timeout"],
		"hcaptchaSolverTimeout":          hcaptcha["hcaptcha_solver_timeout"],
		"hcaptchaSolverNoVlm":            hcaptcha["hcaptcha_solver_no_vlm"],
		"hcaptchaCdpPort":                hcaptcha["hcaptcha_cdp_port"],
		"hcaptchaCaptchaPlatformApiKey":  hcaptcha["hcaptcha_captcha_platform_api_key"],
		"hcaptchaCaptchaPlatformApiUrl":  hcaptcha["hcaptcha_captcha_platform_api_url"],
		"hcaptchaCaptchaPlatformTimeout": hcaptcha["hcaptcha_captcha_platform_timeout"],
		"gptApiEnabled":                  s.configValue("gpt_api_enabled", "0"),
		"gptApiBaseUrl":                  s.configValue("gpt_api_base_url", ""),
		"gptApiKey":                      s.configValue("gpt_api_key", ""),
		"gptApiPlanKey":                  s.configValue("gpt_api_plan_key", "plus"),
		"gptApiCountry":                  s.configValue("gpt_api_country", "PH"),
		"gptApiCurrency":                 s.configValue("gpt_api_currency", "PHP"),
	}
	c.JSON(http.StatusOK, gin.H{"config": config})
}

func isSensitiveConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"password", "secret", "token", "api_key", "bot_token", "client_id", "app_id", "appid", "cardholder_id", "private_key", "public_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func (s *Server) saveConfig(c *gin.Context) {
	var input map[string]string
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	values, err := normalizeModernConfigValues(input)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateCardProviderSettings(cardProviderOverridesFromStrings(values)); err != nil {
		writeCardProviderValidationError(c, err)
		return
	}
	if err := s.upsertConfigBatch(values); err != nil {
		fail(c, http.StatusInternalServerError, "保存配置失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) resolveRechargeMode(value string) string {
	if mode := strings.TrimSpace(value); mode != "" {
		return normalizeMode(mode, s.Cfg.DefaultRechargeMode)
	}
	if s.configValue("gpt_api_enabled", "0") == "1" && strings.TrimSpace(s.configValue("gpt_api_key", "")) != "" {
		return "upstream"
	}
	return s.workerExecutionMode()
}

func (s *Server) workerExecutionMode() string {
	return "browser"
}

func checkoutDebugMode(requested, fallback string) string {
	_ = requested
	_ = fallback
	return "browser"
}

func (s *Server) releaseTask(taskID, errorCode, message string) {
	now := time.Now()
	admissionToken := ""
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var task models.RechargeTask
		if err := tx.First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		admissionToken = strings.TrimSpace(task.AdmissionToken)
		if isTerminal(task.Status) {
			return nil
		}
		if err := tx.Model(&task).Updates(map[string]any{
			"status": models.TaskFailed, "progress": 0, "message": message,
			"error_code": errorCode, "error_message": message, "finished_at": now,
			"worker_lease_token": "", "admission_token": "", "heartbeat_at": nil, "lease_expires_at": nil,
		}).Error; err != nil {
			return err
		}
		if task.CDKID != nil && strings.TrimSpace(*task.CDKID) != "" {
			if err := tx.Model(&models.CDK{}).Where("id = ? AND used_by_task_id = ?", strings.TrimSpace(*task.CDKID), task.ID).Updates(map[string]any{"status": models.CDKAvailable, "used_by_task_id": "", "used_at": nil}).Error; err != nil {
				return err
			}
		}
		if strings.TrimSpace(task.JobKey) != "" {
			if err := tx.Create(&models.RuntimeLog{ID: db.NewID("runtime"), JobKey: task.JobKey, TraceID: firstNonEmpty(task.TraceID, db.NewID("trace")), Level: "error", Source: "task-release", Text: message}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.releaseAdmission(admissionToken)
	}
}

func (s *Server) requireAdmin(c *gin.Context) {
	if !s.hasAdminSession(c) {
		fail(c, http.StatusUnauthorized, "需要管理员授权")
		return
	}
	c.Next()
}

func (s *Server) requireWorker(c *gin.Context) {
	if !authorized(c, s.Cfg.WorkerAPIToken) {
		fail(c, http.StatusUnauthorized, "需要 Worker 授权")
		return
	}
	c.Next()
}

func authorized(c *gin.Context, expected string) bool {
	provided := strings.TrimSpace(c.GetHeader("X-Worker-Token"))
	if provided == "" {
		provided = strings.TrimPrefix(strings.TrimSpace(c.GetHeader("Authorization")), "Bearer ")
	}
	return expected != "" && provided == expected
}

func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowed[origin] || allowed["*"] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Trace-ID, X-Worker-Token, X-Worker-ID, X-Worker-Lease-Token, X-Admin-Token, X-Secondary-Token, X-Admin-Secondary-Token, X-Client-Fingerprint")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func taskResponse(task models.RechargeTask) gin.H {
	cdkCode := firstNonEmpty(task.CDK.Code, task.CDKCode)
	cdkStatus := task.CDK.Status
	planCode := firstNonEmpty(task.Plan.Code, task.CDK.PlanType)
	planName := firstNonEmpty(task.Plan.Name, db.NormalizeProviderPlanName(planCode, task.Plan.ProviderPlanName), planCode)
	return gin.H{
		"id": task.ID, "jobKey": task.JobKey, "traceId": task.TraceID, "status": task.Status, "progress": task.Progress, "message": task.Message,
		"mode": task.Mode, "sessionPreview": task.SessionPreview, "upstreamOrderId": task.UpstreamOrderID,
		"errorCode": task.ErrorCode, "errorMessage": task.ErrorMessage, "attempt": task.Attempt,
		"workerId": task.WorkerID, "heartbeatAt": task.HeartbeatAt, "leaseExpiresAt": task.LeaseExpiresAt, "queueDeadlineAt": task.QueueDeadlineAt,
		"cdkCode": task.CDKCode, "cardLast4": task.CardLast4, "rawOutput": task.RawOutput,
		"poolId": task.PoolID, "businessAccountId": task.BusinessAccountID, "usageType": task.UsageType,
		"paymentCardId": task.PaymentCardID, "cardAllocationId": task.CardAllocationID,
		"cardProvider": task.CardProvider, "cardProviderCardId": task.CardProviderCardID,
		"cardFailureCode": task.CardFailureCode, "cardFailureMessage": task.CardFailureMessage, "cardFailureAt": task.CardFailureAt,
		"failureScreenshots": task.FailureScreenshots, "gptApiOrderId": task.GPTAPIOrderID,
		"gptApiTaskId": task.GPTAPITaskID, "gptApiTopupCode": task.GPTAPITopupCode, "clientIp": task.ClientIP,
		"createdAt": task.CreatedAt, "updatedAt": task.UpdatedAt, "startedAt": task.StartedAt, "finishedAt": task.FinishedAt,
		"cdk":  gin.H{"code": cdkCode, "status": cdkStatus},
		"plan": gin.H{"code": planCode, "name": planName, "price": task.Plan.Price, "currency": task.Plan.Currency},
	}
}

// publicTaskResponse is the deliberately small task contract exposed to
// buyers. Worker messages, card fingerprints, raw output, error codes and
// trace identifiers remain available only through administrator endpoints.
func publicTaskResponse(task models.RechargeTask) gin.H {
	return gin.H{
		"id":       task.ID,
		"status":   task.Status,
		"progress": clampProgress(task.Progress),
		"message":  publicTaskMessage(task.Status),
	}
}

func publicTaskMessage(status string) string {
	switch status {
	case models.TaskQueued:
		return "任务已提交，请耐心等待"
	case models.TaskRunning:
		return "正在处理中，请耐心等待"
	case models.TaskSucceeded:
		return "开通成功"
	case models.TaskFailed, models.TaskManual:
		return "内部错误，请联系客服"
	default:
		return "正在处理中，请耐心等待"
	}
}

func traceMiddleware(c *gin.Context) {
	started := time.Now()
	traceID := strings.TrimSpace(c.GetHeader("X-Trace-ID"))
	if traceID == "" || len(traceID) > 96 {
		traceID = db.NewID("trace")
	}
	c.Set("trace_id", traceID)
	c.Header("X-Trace-ID", traceID)
	c.Next()
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	log.Printf("[request] trace_id=%s method=%s path=%s status=%d latency_ms=%d", traceID, c.Request.Method, path, c.Writer.Status(), time.Since(started).Milliseconds())
}

func requestTraceID(c *gin.Context) string {
	if value, ok := c.Get("trace_id"); ok {
		if traceID, ok := value.(string); ok && strings.TrimSpace(traceID) != "" {
			return strings.TrimSpace(traceID)
		}
	}
	traceID := db.NewID("trace")
	c.Set("trace_id", traceID)
	c.Header("X-Trace-ID", traceID)
	return traceID
}

func fail(c *gin.Context, status int, message string) {
	traceID := requestTraceID(c)
	c.Abort()
	c.JSON(status, gin.H{"success": false, "message": message, "traceId": traceID, "trace_id": traceID})
}

func publicFail(c *gin.Context, status int, _ string) {
	requestTraceID(c)
	c.Abort()
	c.JSON(status, gin.H{"success": false, "message": "内部错误，请联系客服"})
}

func normalizeMode(value, fallback string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode != "upstream" && mode != "browser" && mode != "dry_run" && mode != "protocol" {
		mode = fallback
	}
	return mode
}

func validTaskStatus(status string) bool {
	return status == models.TaskRunning || status == models.TaskSucceeded || status == models.TaskFailed || status == models.TaskManual
}

func isTerminal(status string) bool {
	return status == models.TaskSucceeded || status == models.TaskFailed || status == models.TaskManual
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func parseLimit(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > 500 {
		return 500
	}
	return parsed
}

func newCDKCode(prefix string) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	for index, value := range buffer {
		buffer[index] = alphabet[int(value)%len(alphabet)]
	}
	return prefix + "-" + string(buffer[:4]) + "-" + string(buffer[4:8]) + "-" + string(buffer[8:]), nil
}

func digitsOnly(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if char >= '0' && char <= '9' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
