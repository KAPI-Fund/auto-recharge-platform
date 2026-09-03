package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func registerPoolAssetRoutes(admin *gin.RouterGroup, secondary *gin.RouterGroup, s *Server) {
	admin.GET("/pool-emails", s.listPoolEmails)
	admin.POST("/pool-emails/import", s.importPoolEmails)
	admin.GET("/pool-emails/:id/messages", s.poolEmailMessages)
	admin.DELETE("/pool-emails/:id", s.deletePoolEmail)

	admin.GET("/phones", s.listPhones)
	admin.POST("/phones/import", s.importPhones)
	admin.PUT("/phones/:id", s.updatePhone)
	admin.DELETE("/phones/:id", s.deletePhone)

	admin.GET("/products", s.listProducts)
	admin.POST("/products/import", s.importProducts)
	admin.PUT("/products/:id/status", s.updateProductStatus)
	admin.DELETE("/products/:id", s.deleteProduct)
	admin.GET("/products/:id/export", s.exportProduct)
	admin.POST("/products/export", s.exportProducts)
	secondary.POST("/products/claim", s.claimProduct)
}

func poolEmailResponse(row models.PoolEmail) gin.H {
	hasPassword := strings.TrimSpace(row.Password) != ""
	hasOAuth := strings.TrimSpace(row.RefreshToken) != ""
	return gin.H{
		"id": row.ID, "email": row.Email, "has_password": hasPassword,
		"has_oauth": hasOAuth, "registered": row.Registered,
		"registered_at": row.RegisteredAt, "in_use": row.InUse, "locked_at": row.LockedAt,
		"is_active": row.Active, "created_at": row.CreatedAt,
		"password_label":     map[bool]string{true: "已配置", false: "缺失"}[hasPassword],
		"password_tone":      map[bool]string{true: "success", false: "danger"}[hasPassword],
		"oauth_label":        map[bool]string{true: "已配置", false: "未配置"}[hasOAuth],
		"oauth_tone":         map[bool]string{true: "info", false: "neutral"}[hasOAuth],
		"registration_label": map[bool]string{true: "已注册", false: "待注册"}[row.Registered],
		"registration_tone":  map[bool]string{true: "success", false: "neutral"}[row.Registered],
		"usage_label":        map[bool]string{true: "使用中", false: "空闲"}[row.InUse],
		"usage_tone":         map[bool]string{true: "warning", false: "neutral"}[row.InUse],
	}
}

func (s *Server) listPoolEmails(c *gin.Context) {
	var rows []models.PoolEmail
	if err := s.DB.Where("active = ?", true).Order("sort_order ASC, created_at ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, poolEmailResponse(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "items": items})
}

type importedPoolEmail struct {
	Email        string
	Password     string
	ClientID     string
	RefreshToken string
}

type importedPhone struct {
	Phone string `json:"phone"`
	Key   string `json:"key"`
}

type importedProduct struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Token    string `json:"token"`
	IMAPKey  string `json:"imap_key"`
	FilePath string `json:"file_path"`
	Status   string `json:"status"`
}

func parsePoolEmailImport(text string) ([]importedPoolEmail, int) {
	rows := []importedPoolEmail{}
	skipped := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var email, password, clientID, refresh string
		if strings.Contains(line, "----") {
			parts := strings.Split(line, "----")
			if len(parts) >= 4 && strings.Contains(parts[0], "@") {
				email, password, clientID, refresh = strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), strings.TrimSpace(strings.Join(parts[3:], "----"))
			} else if len(parts) >= 2 && strings.Contains(parts[0], "@") {
				email, password = strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(strings.Join(parts[1:], "----"))
			} else {
				skipped++
				continue
			}
		} else {
			parts := strings.Fields(line)
			if strings.Contains(line, "\t") {
				parts = strings.FieldsFunc(line, func(r rune) bool { return r == '\t' })
			}
			if len(parts) < 2 || !strings.Contains(parts[0], "@") {
				skipped++
				continue
			}
			email, password = strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(strings.Join(parts[1:], " "))
		}
		if email == "" || (password == "" && refresh == "") {
			skipped++
			continue
		}
		rows = append(rows, importedPoolEmail{Email: email, Password: password, ClientID: clientID, RefreshToken: refresh})
	}
	return rows, skipped
}

func (s *Server) importPoolEmails(c *gin.Context) {
	var input struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供邮箱池导入文本"})
		return
	}
	parsed, skipped := parsePoolEmailImport(input.Text)
	created := 0
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for index, item := range parsed {
			var existing models.PoolEmail
			result := tx.Where("email = ?", item.Email).First(&existing)
			if result.Error == nil {
				if err := tx.Model(&existing).Updates(map[string]any{"password": item.Password, "client_id": item.ClientID, "refresh_token": item.RefreshToken, "active": true, "sort_order": index}).Error; err != nil {
					return err
				}
				created++
				continue
			}
			if result.Error != gorm.ErrRecordNotFound {
				return result.Error
			}
			if err := tx.Create(&models.PoolEmail{ID: db.NewID("mail"), Email: item.Email, Password: item.Password, ClientID: item.ClientID, RefreshToken: item.RefreshToken, Active: true, SortOrder: index}).Error; err != nil {
				return err
			}
			created++
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "applied": created, "parsed": len(parsed), "skipped": skipped, "oauthCount": countOAuth(parsed)})
}

func countOAuth(rows []importedPoolEmail) int {
	count := 0
	for _, row := range rows {
		if row.RefreshToken != "" {
			count++
		}
	}
	return count
}

func (s *Server) deletePoolEmail(c *gin.Context) {
	result := s.DB.Delete(&models.PoolEmail{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "邮箱不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "邮箱已删除"})
}

func (s *Server) poolEmailMessages(c *gin.Context) {
	var row models.PoolEmail
	if err := s.DB.Where("id = ? AND active = ?", c.Param("id"), true).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "邮箱不存在"})
		return
	}
	limit := parseLimit(c.Query("limit"), 50)
	payload, err := s.browserPoolControl("POST", "/mailbox-preview", gin.H{
		"email": row.Email, "password": row.Password, "clientId": row.ClientID,
		"refreshToken": row.RefreshToken, "limit": limit,
		"imapHost":    s.configValue("pool_email_imap_host", "outlook.office365.com"),
		"imapPort":    parseConfigInt(s.configValue("pool_email_imap_port", "993"), 993),
		"includeJunk": s.configValue("pool_email_include_junk", "1") != "0",
	}, requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, payload)
}

func phoneResponse(row models.PhoneAsset) gin.H {
	statusLabel, statusTone := phoneStatusView(row.Status)
	return gin.H{
		"id": row.ID, "phone": row.Phone, "key": row.APIKey, "usage_count": row.UsageCount,
		"sort_order": row.SortOrder, "is_active": row.Active, "status": row.Status, "in_use": row.InUse, "locked_at": row.LockedAt,
		"status_label": statusLabel, "status_tone": statusTone,
		"active_label":   map[bool]string{true: "启用", false: "停用"}[row.Active],
		"active_tone":    map[bool]string{true: "success", false: "neutral"}[row.Active],
		"usage_text":     fmt.Sprintf("%d", row.UsageCount),
		"status_options": phoneStatusOptions(),
	}
}

func phoneStatusOptions() []gin.H {
	return []gin.H{
		{"value": "正常", "label": "正常", "tone": "success"},
		{"value": "封禁", "label": "封禁", "tone": "danger"},
		{"value": "disabled", "label": "disabled", "tone": "danger"},
	}
}

func phoneStatusView(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "normal", "正常", "active":
		return "正常", "success"
	case "封禁", "banned", "disabled":
		return status, "danger"
	default:
		return status, "warning"
	}
}

func (s *Server) listPhones(c *gin.Context) {
	var rows []models.PhoneAsset
	if err := s.DB.Order("sort_order ASC, created_at ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, phoneResponse(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "phones": items})
}

func (s *Server) importPhones(c *gin.Context) {
	var input struct {
		Text   string          `json:"text"`
		Phones []importedPhone `json:"phones"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	items := input.Phones
	if strings.TrimSpace(input.Text) != "" {
		items = parsePhoneImport(input.Text)
	}
	if len(items) == 0 || len(items) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供 1 到 1000 个手机号"})
		return
	}
	created := 0
	for index, item := range items {
		phone := strings.TrimSpace(item.Phone)
		if phone == "" {
			continue
		}
		var row models.PhoneAsset
		result := s.DB.Where("phone = ?", phone).First(&row)
		if result.Error == nil {
			_ = s.DB.Model(&row).Updates(map[string]any{"api_key": strings.TrimSpace(item.Key), "active": true, "status": "正常", "sort_order": index}).Error
			created++
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
			return
		}
		if err := s.DB.Create(&models.PhoneAsset{ID: db.NewID("phone"), Phone: phone, APIKey: strings.TrimSpace(item.Key), Active: true, Status: "正常", SortOrder: index}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		created++
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "created": created})
}

func parsePhoneImport(text string) []importedPhone {
	items := make([]importedPhone, 0)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if separator := strings.IndexByte(line, '-'); separator > 0 {
			phone := strings.TrimSpace(line[:separator])
			key := strings.TrimSpace(line[separator+1:])
			if phone != "" && key != "" && digitsOnly(phone) == phone {
				items = append(items, importedPhone{Phone: phone, Key: key})
			}
			continue
		}

		parts := strings.FieldsFunc(line, func(r rune) bool {
			return r == '\t' || r == ',' || r == '|' || r == ' '
		})
		if len(parts) > 0 && parts[0] != "" {
			key := ""
			if len(parts) > 1 {
				key = strings.Join(parts[1:], " ")
			}
			items = append(items, importedPhone{Phone: parts[0], Key: key})
		}
	}
	return items
}

func (s *Server) updatePhone(c *gin.Context) {
	var input struct {
		Phone  string `json:"phone"`
		Key    string `json:"key"`
		Active *bool  `json:"active"`
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	updates := map[string]any{"phone": strings.TrimSpace(input.Phone), "api_key": strings.TrimSpace(input.Key), "status": strings.TrimSpace(input.Status)}
	if input.Active != nil {
		updates["active"] = *input.Active
	}
	result := s.DB.Model(&models.PhoneAsset{}).Where("id = ?", c.Param("id")).Updates(updates)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "手机号不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (s *Server) deletePhone(c *gin.Context) {
	result := s.DB.Delete(&models.PhoneAsset{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "手机号不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func productResponse(row models.ProductAsset) gin.H {
	statusLabel, statusTone := productStatusView(row.Status)
	return gin.H{
		"id": row.ID, "email": row.Email, "imap_key": row.IMAPKey, "claimed_cdk": row.ClaimedCDK,
		"file_path": row.FilePath, "status": row.Status, "status_label": statusLabel, "status_tone": statusTone,
		"shipped": row.Shipped, "is_shipped": row.Shipped,
		"shipped_label": map[bool]string{true: "是", false: "否"}[row.Shipped],
		"shipped_tone":  map[bool]string{true: "success", false: "neutral"}[row.Shipped],
		"created_at":    row.CreatedAt,
	}
}

func productStatusView(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "normal", "正常", "active":
		return "正常", "success"
	case "封禁", "banned", "disabled":
		return "封禁", "danger"
	default:
		return status, "warning"
	}
}

func (s *Server) listProducts(c *gin.Context) {
	var rows []models.ProductAsset
	if err := s.DB.Order("created_at DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, productResponse(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "items": items})
}

func (s *Server) importProducts(c *gin.Context) {
	var input struct {
		Text     string            `json:"text"`
		Products []importedProduct `json:"products"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供 1 到 1000 个成品账号"})
		return
	}
	if strings.TrimSpace(input.Text) != "" {
		input.Products = parseProductImport(input.Text)
	}
	if len(input.Products) == 0 || len(input.Products) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请提供 1 到 1000 个成品账号"})
		return
	}
	created := 0
	for _, item := range input.Products {
		email := strings.ToLower(strings.TrimSpace(item.Email))
		if email == "" {
			continue
		}
		var row models.ProductAsset
		result := s.DB.Where("email = ?", email).First(&row)
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = "正常"
		}
		updates := map[string]any{"password": item.Password, "token": item.Token, "imap_key": item.IMAPKey, "file_path": item.FilePath, "status": status, "active": true}
		if result.Error == nil {
			if err := s.DB.Model(&row).Updates(updates).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
				return
			}
			created++
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
			return
		}
		if err := s.DB.Create(&models.ProductAsset{ID: db.NewID("product"), Email: email, Password: item.Password, Token: item.Token, IMAPKey: item.IMAPKey, FilePath: item.FilePath, Status: status, Active: true}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		created++
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "created": created})
}

func parseProductImport(text string) []importedProduct {
	items := make([]importedProduct, 0)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "----")
		if len(parts) < 2 {
			parts = strings.Split(line, "\t")
		}
		if len(parts) < 2 || !strings.Contains(parts[0], "@") {
			continue
		}
		item := importedProduct{}
		item.Email = strings.ToLower(strings.TrimSpace(parts[0]))
		item.Password = strings.TrimSpace(partAt(parts, 1))
		item.Token = strings.TrimSpace(partAt(parts, 2))
		item.IMAPKey = strings.TrimSpace(partAt(parts, 3))
		if len(parts) > 4 {
			item.FilePath = strings.TrimSpace(strings.Join(parts[4:], "----"))
		}
		item.Status = "正常"
		items = append(items, item)
	}
	return items
}

func partAt(parts []string, index int) string {
	if index < 0 || index >= len(parts) {
		return ""
	}
	return parts[index]
}

func (s *Server) updateProductStatus(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求格式不正确"})
		return
	}
	var row models.ProductAsset
	if err := s.DB.Where("id = ?", c.Param("id")).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "成品不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = toggleProductStatus(row.Status)
	}
	result := s.DB.Model(&row).Update("status", status)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "status": row.Status})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "status": status})
}

func toggleProductStatus(status string) string {
	if strings.EqualFold(strings.TrimSpace(status), "正常") {
		return "封禁"
	}
	return "正常"
}

func (s *Server) deleteProduct(c *gin.Context) {
	result := s.DB.Delete(&models.ProductAsset{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "成品不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type productExport struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Token    string `json:"token"`
	IMAPKey  string `json:"imap_key"`
	FilePath string `json:"file_path"`
	JobKey   string `json:"jobKey,omitempty"`
}

type productProtocolExport struct {
	ExportedAt string            `json:"exported_at"`
	Proxies    []json.RawMessage `json:"proxies"`
	Accounts   []json.RawMessage `json:"accounts"`
}

func (s *Server) exportProduct(c *gin.Context) {
	s.exportProductsByIDs(c, []string{c.Param("id")})
}

func (s *Server) exportProducts(c *gin.Context) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请选择成品"})
		return
	}
	s.exportProductsByIDs(c, input.IDs)
}

func (s *Server) exportProductsByIDs(c *gin.Context, ids []string) {
	var rows []models.ProductAsset
	var payload []byte
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND shipped = ?", ids, false).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return gorm.ErrRecordNotFound
		}
		var buildErr error
		payload, buildErr = s.buildProductProtocolExport(rows)
		if buildErr != nil {
			return buildErr
		}
		rowIDs := make([]string, 0, len(rows))
		for _, row := range rows {
			rowIDs = append(rowIDs, row.ID)
		}
		return tx.Model(&models.ProductAsset{}).Where("id IN ? AND shipped = ?", rowIDs, false).Update("shipped", true).Error
	}); err != nil {
		status := http.StatusInternalServerError
		message := err.Error()
		if err == gorm.ErrRecordNotFound {
			status = http.StatusConflict
			message = "成品不存在或已出库"
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="products.json"`)
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

func (s *Server) buildProductProtocolExport(rows []models.ProductAsset) ([]byte, error) {
	document := productProtocolExport{
		ExportedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Proxies:    make([]json.RawMessage, 0),
		Accounts:   make([]json.RawMessage, 0),
	}
	for _, row := range rows {
		filePath := strings.TrimSpace(row.FilePath)
		if filePath == "" {
			return nil, fmt.Errorf("成品 %s 缺少协议文件", row.Email)
		}
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(s.Cfg.RuntimeDir, filePath)
		}
		raw, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			return nil, fmt.Errorf("成品 %s 的协议文件读取失败: %w", row.Email, err)
		}
		if err := appendProductProtocol(raw, &document); err != nil {
			return nil, fmt.Errorf("成品 %s 的协议文件格式错误: %w", row.Email, err)
		}
	}
	return json.MarshalIndent(document, "", "  ")
}

func appendProductProtocol(raw []byte, document *productProtocolExport) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil && object != nil {
		if accounts, ok := object["accounts"]; ok {
			var values []json.RawMessage
			if err := json.Unmarshal(accounts, &values); err != nil {
				return fmt.Errorf("accounts 字段不是数组: %w", err)
			}
			document.Accounts = append(document.Accounts, values...)
			if proxies, ok := object["proxies"]; ok {
				var proxyValues []json.RawMessage
				if err := json.Unmarshal(proxies, &proxyValues); err != nil {
					return fmt.Errorf("proxies 字段不是数组: %w", err)
				}
				document.Proxies = append(document.Proxies, proxyValues...)
			}
			return nil
		}
	}

	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		document.Accounts = append(document.Accounts, values...)
		return nil
	}
	var value json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	document.Accounts = append(document.Accounts, value)
	return nil
}

func (s *Server) claimProduct(c *gin.Context) {
	var input struct {
		Code string `json:"cdk"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请输入成品 CDK"})
		return
	}
	result, err := s.claimProductCode(strings.TrimSpace(input.Code), requestTraceID(c))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "product": result})
}

func (s *Server) legacyProductDownload(c *gin.Context) {
	c.String(http.StatusGone, "成品号下载功能已移除，仅支持自助开通")
}

func (s *Server) claimProductCode(code, traceID string) (productExport, error) {
	var result productExport
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var cdk models.CDK
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", code).First(&cdk).Error; err != nil {
			return err
		}
		if cdk.Status != models.CDKAvailable || cdk.Type != models.CDKTypeProduct {
			return fmt.Errorf("CDK 无效、已使用或非成品激活码")
		}
		var product models.ProductAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("shipped = ? AND status = ?", false, "正常").Order("created_at ASC, id ASC").First(&product).Error; err != nil {
			return fmt.Errorf("当前成品号库暂时缺货")
		}
		now := time.Now()
		cdkID := cdk.ID
		if err := tx.Model(&cdk).Updates(map[string]any{"status": models.CDKUsed, "used_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&product).Updates(map[string]any{"shipped": true, "claimed_cdk": cdk.Code}).Error; err != nil {
			return err
		}
		jobKey := db.NewID("product-claim")
		logRow := models.RechargeTask{
			ID:           db.NewID("task"),
			JobKey:       jobKey,
			TraceID:      traceID,
			CDKID:        &cdkID,
			PlanID:       cdk.PlanID,
			Mode:         "product_claim",
			TokenPreview: "PRODUCT_CLAIM",
			CDKCode:      cdk.Code,
			Status:       models.TaskSucceeded,
			Progress:     100,
			Message:      fmt.Sprintf("成品号兑换成功: %s", product.Email),
			DisplayTime:  now.Format("2006-01-02 15:04:05"),
			CreatedAt:    now,
			UpdatedAt:    now,
			FinishedAt:   &now,
		}
		if err := tx.Create(&logRow).Error; err != nil {
			return err
		}
		result = productExport{ID: product.ID, Email: product.Email, Password: product.Password, Token: product.Token, IMAPKey: product.IMAPKey, FilePath: product.FilePath, JobKey: jobKey}
		return nil
	})
	return result, err
}
