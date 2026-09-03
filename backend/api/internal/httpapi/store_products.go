package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

var storeProductCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

var storeProductProviderPlans = map[string]bool{
	"chatgptplusplan": true,
	"chatgptprolite":  true,
	"chatgptpro":      true,
}

var storeProductCountries = map[string]bool{
	"US": true,
	"PH": true,
	"SG": true,
	"MY": true,
}

// decoratePlanInventory adds computed storefront availability without
// persisting the projection fields. A sale limit of zero means unlimited.
func decoratePlanInventory(plan *models.Plan) {
	if plan == nil {
		return
	}
	plan.RemainingQuantity = nil
	plan.SoldOut = false
	if plan.SaleLimit > 0 {
		remaining := plan.SaleLimit - plan.SoldCount
		if remaining < 0 {
			remaining = 0
		}
		plan.RemainingQuantity = &remaining
		plan.SoldOut = remaining == 0
	}
	plan.PurchaseEnabled = plan.Active && !plan.SoldOut
	switch {
	case plan.SoldOut:
		plan.AvailabilityLabel = "已售罄，补货中"
	case !plan.Active:
		plan.AvailabilityLabel = "已下架"
	default:
		plan.AvailabilityLabel = "在售"
	}
}

func planHasSaleCapacity(plan models.Plan) bool {
	return plan.SaleLimit <= 0 || plan.SoldCount < plan.SaleLimit
}

func storeProductResponse(plan models.Plan) gin.H {
	decoratePlanInventory(&plan)
	publishedLabel := map[bool]string{true: "已发布", false: "已下架"}[plan.Active]
	publishedTone := map[bool]string{true: "success", false: "neutral"}[plan.Active]
	return gin.H{
		"id": plan.ID, "code": plan.Code, "name": plan.Name, "description": plan.Description, "active": plan.Active,
		"providerPlanName": db.NormalizeProviderPlanName(plan.Code, plan.ProviderPlanName), "country": plan.Country, "currency": models.PlatformStoreCurrency,
		"price": plan.Price, "priceText": fmt.Sprintf("CN¥%.2f", plan.Price), "published": plan.Active, "sortOrder": plan.SortOrder,
		"published_label": publishedLabel, "published_tone": publishedTone,
		"published_action_label": map[bool]string{true: "下架", false: "发布"}[plan.Active],
		"saleLimit":              plan.SaleLimit,
		"soldCount":              plan.SoldCount,
		"remainingQuantity":      plan.RemainingQuantity,
		"soldOut":                plan.SoldOut,
		"purchaseEnabled":        plan.PurchaseEnabled,
		"availabilityLabel":      plan.AvailabilityLabel,
		"deliveryMode":           "generated_cdk",
	}
}

func storeProductOptions() gin.H {
	return gin.H{
		"currency": models.PlatformStoreCurrency,
		"providerPlans": []gin.H{
			{"value": "chatgptplusplan", "code": "plus", "label": "ChatGPT Plus（plus）"},
			{"value": "chatgptprolite", "code": "pro_5x", "label": "Pro 5x（pro_5x）"},
			{"value": "chatgptpro", "code": "pro_20x", "label": "Pro 20x（pro_20x）"},
		},
		"countries": []gin.H{
			{"value": "US", "code": "US", "label": "美国（US）"},
			{"value": "PH", "code": "PH", "label": "菲律宾（PH）"},
			{"value": "SG", "code": "SG", "label": "新加坡（SG）"},
			{"value": "MY", "code": "MY", "label": "马来西亚（MY）"},
		},
		"defaults": gin.H{"providerPlanName": db.ProviderPlanNameForCode("plus"), "country": "US", "currency": models.PlatformStoreCurrency, "price": 20, "saleLimit": 0, "sortOrder": 10, "published": true},
	}
}

func (s *Server) adminStoreProducts(c *gin.Context) {
	var plans []models.Plan
	if err := s.DB.Order("sort_order ASC, code ASC").Find(&plans).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取售卡商品失败")
		return
	}
	items := make([]gin.H, 0, len(plans))
	for _, plan := range plans {
		items = append(items, storeProductResponse(plan))
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{"products": items, "options": storeProductOptions(), "traceId": traceID, "trace_id": traceID})
}

func (s *Server) upsertStoreProduct(c *gin.Context) {
	var input struct {
		Code             string  `json:"code"`
		Name             string  `json:"name"`
		Description      string  `json:"description"`
		ProviderPlanName string  `json:"providerPlanName"`
		Country          string  `json:"country"`
		Currency         string  `json:"currency"`
		Price            float64 `json:"price"`
		SaleLimit        *int    `json:"saleLimit"`
		SortOrder        int     `json:"sortOrder"`
		Published        *bool   `json:"published"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.ProviderPlanName = strings.TrimSpace(input.ProviderPlanName)
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	input.Currency = models.PlatformStoreCurrency
	if input.ProviderPlanName == "" {
		input.ProviderPlanName = db.ProviderPlanNameForCode("plus")
	}
	input.ProviderPlanName = db.NormalizeProviderPlanName("plus", input.ProviderPlanName)
	saleLimit := 0
	if input.SaleLimit != nil {
		if *input.SaleLimit < 0 {
			fail(c, http.StatusBadRequest, "可售数量必须是大于等于 0 的整数")
			return
		}
		saleLimit = *input.SaleLimit
	}
	if input.Country == "" {
		input.Country = "US"
	}
	if !storeProductCodePattern.MatchString(input.Code) || input.Name == "" || input.Price <= 0 {
		fail(c, http.StatusBadRequest, "商品编码、名称或价格格式不正确")
		return
	}
	if !storeProductProviderPlans[input.ProviderPlanName] {
		fail(c, http.StatusBadRequest, "关联原版套餐不正确")
		return
	}
	if !storeProductCountries[input.Country] {
		fail(c, http.StatusBadRequest, "开通地区不正确")
		return
	}
	published := true
	if input.Published != nil {
		published = *input.Published
	}
	plan := models.Plan{ID: db.NewID("plan"), Code: input.Code, Name: input.Name, Description: strings.TrimSpace(input.Description), ProviderPlanName: input.ProviderPlanName, Country: input.Country, Currency: models.PlatformStoreCurrency, Price: input.Price, SaleLimit: saleLimit, SortOrder: input.SortOrder, Active: published}
	var existing models.Plan
	result := s.DB.Where("code = ?", input.Code).First(&existing)
	if result.Error == nil {
		updates := map[string]any{
			"name": plan.Name, "description": plan.Description, "provider_plan_name": plan.ProviderPlanName,
			"country": plan.Country, "currency": plan.Currency, "price": plan.Price, "sort_order": plan.SortOrder, "active": plan.Active,
		}
		if input.SaleLimit != nil {
			updates["sale_limit"] = saleLimit
		}
		if err := s.DB.Model(&existing).Updates(updates).Error; err != nil {
			fail(c, http.StatusInternalServerError, "更新售卡商品失败")
			return
		}
		plan = existing
		plan.Name, plan.Description, plan.ProviderPlanName, plan.Country, plan.Currency, plan.Price, plan.SortOrder, plan.Active = input.Name, strings.TrimSpace(input.Description), input.ProviderPlanName, input.Country, models.PlatformStoreCurrency, input.Price, input.SortOrder, published
		if input.SaleLimit != nil {
			plan.SaleLimit = saleLimit
		}
	} else if result.Error == gorm.ErrRecordNotFound {
		// Plan.Active has a database default of true. Insert a value map so an
		// explicit published=false is persisted instead of being treated as a
		// zero value by GORM's default-value callback.
		now := time.Now()
		if err := s.DB.Table("plans").Create(map[string]any{
			"id": plan.ID, "code": plan.Code, "name": plan.Name, "description": plan.Description,
			"provider_plan_name": plan.ProviderPlanName, "country": plan.Country, "currency": plan.Currency,
			"price": plan.Price, "active": plan.Active, "sale_limit": plan.SaleLimit, "sold_count": plan.SoldCount, "sort_order": plan.SortOrder,
			"created_at": now, "updated_at": now,
		}).Error; err != nil {
			fail(c, http.StatusInternalServerError, "发布售卡商品失败")
			return
		}
	} else {
		fail(c, http.StatusInternalServerError, "读取售卡商品失败")
		return
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{"product": storeProductResponse(plan), "message": fmt.Sprintf("商品 %s 已保存", plan.Code), "traceId": traceID, "trace_id": traceID})
}

func (s *Server) updateStoreProduct(c *gin.Context) {
	var input struct {
		Published *bool `json:"published"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Published == nil {
		fail(c, http.StatusBadRequest, "请提供 published")
		return
	}
	var plan models.Plan
	if err := s.DB.Where("code = ?", strings.ToLower(strings.TrimSpace(c.Param("code")))).First(&plan).Error; err != nil {
		fail(c, http.StatusNotFound, "商品不存在")
		return
	}
	if err := s.DB.Model(&plan).Update("active", *input.Published).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新商品状态失败")
		return
	}
	plan.Active = *input.Published
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{"product": storeProductResponse(plan), "traceId": traceID, "trace_id": traceID})
}

func (s *Server) toggleStoreProduct(c *gin.Context) {
	var plan models.Plan
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if err := s.DB.Where("code = ?", code).First(&plan).Error; err != nil {
		fail(c, http.StatusNotFound, "商品不存在")
		return
	}
	updatedPublished := !plan.Active
	if err := s.DB.Model(&plan).Update("active", updatedPublished).Error; err != nil {
		fail(c, http.StatusInternalServerError, "切换商品状态失败")
		return
	}
	plan.Active = updatedPublished
	action := "已下架"
	if updatedPublished {
		action = "已发布"
	}
	traceID := requestTraceID(c)
	c.JSON(http.StatusOK, gin.H{
		"product": storeProductResponse(plan),
		"message": fmt.Sprintf("商品 %s %s", plan.Code, action),
		"traceId": traceID, "trace_id": traceID,
	})
}
