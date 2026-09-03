package httpapi

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	appemail "github.com/kc-catk/auto-recharge-platform/backend/api/internal/email"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	emailDeliveryPending = "pending"
	emailDeliverySending = "sending"
	emailDeliverySent    = "sent"
	emailDeliveryFailed  = "failed"

	emailPurchaseEvent = "purchase_cdk"
	emailRedeemEvent   = "redeem_success"
)

type emailNotificationConfig struct {
	Enabled        bool
	NotifyPurchase bool
	NotifyRedeem   bool
	SiteName       string
	PublicBaseURL  string
	SMTP           appemail.SMTPConfig
}

func (s *Server) emailNotificationConfig() emailNotificationConfig {
	defaultPort := s.Cfg.EmailSMTPPort
	if defaultPort <= 0 {
		defaultPort = 587
	}
	defaultTimeout := s.Cfg.EmailSMTPTimeoutSeconds
	if defaultTimeout <= 0 {
		defaultTimeout = 10
	}
	siteName := firstNonEmpty(s.configValue("email_site_name", ""), s.Cfg.EmailSiteName, "KC GPT自动充值系统")
	fromName := firstNonEmpty(s.configValue("email_smtp_from_name", ""), s.Cfg.EmailSMTPFromName, siteName)
	return emailNotificationConfig{
		Enabled:        parseBoolConfig(s.configValue("email_enabled", fmt.Sprintf("%t", s.Cfg.EmailEnabled)), s.Cfg.EmailEnabled),
		NotifyPurchase: parseBoolConfig(s.configValue("email_notify_purchase", fmt.Sprintf("%t", s.Cfg.EmailNotifyPurchase)), s.Cfg.EmailNotifyPurchase),
		NotifyRedeem:   parseBoolConfig(s.configValue("email_notify_redeem", fmt.Sprintf("%t", s.Cfg.EmailNotifyRedeem)), s.Cfg.EmailNotifyRedeem),
		SiteName:       siteName,
		PublicBaseURL:  strings.TrimRight(firstNonEmpty(s.configValue("public_base_url", ""), s.Cfg.PublicBaseURL), "/"),
		SMTP: appemail.SMTPConfig{
			Host:     strings.TrimSpace(s.configValue("email_smtp_host", s.Cfg.EmailSMTPHost)),
			Port:     parseConfigInt(s.configValue("email_smtp_port", fmt.Sprintf("%d", defaultPort)), defaultPort),
			Username: strings.TrimSpace(s.configValue("email_smtp_username", s.Cfg.EmailSMTPUsername)),
			Password: strings.TrimSpace(s.configValue("email_smtp_password", s.Cfg.EmailSMTPPassword)),
			From:     strings.TrimSpace(s.configValue("email_smtp_from", s.Cfg.EmailSMTPFrom)),
			FromName: fromName,
			UseTLS:   parseBoolConfig(s.configValue("email_smtp_use_tls", fmt.Sprintf("%t", s.Cfg.EmailSMTPUseTLS)), s.Cfg.EmailSMTPUseTLS),
			Timeout:  time.Duration(parseConfigInt(s.configValue("email_smtp_timeout_seconds", fmt.Sprintf("%d", defaultTimeout)), defaultTimeout)) * time.Second,
		},
	}
}

// emailConfigValues is the safe admin response. The SMTP password is reduced
// to a boolean and is never returned to the browser.
func (s *Server) emailConfigValues() map[string]string {
	settings := s.emailNotificationConfig()
	return map[string]string{
		"emailEnabled":                fmt.Sprintf("%t", settings.Enabled),
		"emailNotifyPurchase":         fmt.Sprintf("%t", settings.NotifyPurchase),
		"emailNotifyRedeem":           fmt.Sprintf("%t", settings.NotifyRedeem),
		"emailSiteName":               settings.SiteName,
		"emailSMTPHost":               settings.SMTP.Host,
		"emailSMTPPort":               fmt.Sprintf("%d", settings.SMTP.Port),
		"emailSMTPUsername":           settings.SMTP.Username,
		"emailSMTPFrom":               settings.SMTP.From,
		"emailSMTPFromName":           settings.SMTP.FromName,
		"emailSMTPUseTLS":             fmt.Sprintf("%t", settings.SMTP.UseTLS),
		"emailSMTPTimeoutSeconds":     fmt.Sprintf("%d", int(settings.SMTP.Timeout/time.Second)),
		"emailSMTPPasswordSavedValue": fmt.Sprintf("%t", strings.TrimSpace(settings.SMTP.Password) != ""),
		"email_smtp_password_saved":   fmt.Sprintf("%t", strings.TrimSpace(settings.SMTP.Password) != ""),
	}
}

func (s *Server) newEmailSender(settings emailNotificationConfig) (*appemail.SMTPSender, error) {
	if strings.TrimSpace(settings.SMTP.Host) == "" || strings.TrimSpace(settings.SMTP.From) == "" {
		return nil, fmt.Errorf("邮件 SMTP 尚未配置主机和发件人邮箱")
	}
	if settings.SMTP.Port < 1 || settings.SMTP.Port > 65535 {
		return nil, fmt.Errorf("邮件 SMTP 端口必须是 1-65535 的整数")
	}
	return appemail.NewSMTPSender(settings.SMTP), nil
}

func (s *Server) legacyTestEmail(c *gin.Context) {
	var input struct {
		To    string `json:"to"`
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	to, err := normalizeStoreEmail(firstNonEmpty(input.To, input.Email))
	if err != nil {
		fail(c, http.StatusBadRequest, "测试收件人邮箱无效")
		return
	}
	settings := s.emailNotificationConfig()
	sender, err := s.newEmailSender(settings)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	traceID := requestTraceID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), settings.SMTP.Timeout+time.Second)
	defer cancel()
	err = sender.SendGeneric(ctx, to, fmt.Sprintf("[%s] 邮件配置测试", settings.SiteName),
		fmt.Sprintf("这是 %s 的邮件配置测试。\n\nTrace ID: %s\n\n如果你收到这封邮件，SMTP 配置已生效。", settings.SiteName, traceID),
		fmt.Sprintf("<p>这是 <strong>%s</strong> 的邮件配置测试。</p><p>如果你收到这封邮件，SMTP 配置已生效。</p>", html.EscapeString(settings.SiteName)))
	if err != nil {
		log.Printf("[email] trace_id=%s event=config_test recipient=%s status=failed error=%v", traceID, maskStoreEmail(to), err)
		fail(c, http.StatusBadGateway, "测试邮件发送失败")
		return
	}
	log.Printf("[email] trace_id=%s event=config_test recipient=%s status=sent", traceID, maskStoreEmail(to))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "测试邮件已发送", "to": to, "traceId": traceID, "trace_id": traceID})
}

func (s *Server) queuePurchaseEmail(orderID, traceID string) {
	if s == nil || s.DB == nil {
		return
	}
	settings := s.emailNotificationConfig()
	if !settings.Enabled || !settings.NotifyPurchase {
		return
	}
	var order models.StoreOrder
	if err := s.DB.Preload("Plan").Where("id = ?", strings.TrimSpace(orderID)).First(&order).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			log.Printf("[email] trace_id=%s event=%s status=queue_failed error=%v", traceID, emailPurchaseEvent, err)
		}
		return
	}
	if order.Status != storeOrderPaid || strings.TrimSpace(order.CDKCode) == "" || strings.TrimSpace(order.Email) == "" {
		return
	}
	s.queueEmailDelivery(models.EmailDelivery{
		ID:        db.NewID("email"),
		EventKey:  emailPurchaseEvent + ":" + order.ID,
		EventType: emailPurchaseEvent,
		OrderID:   order.ID,
		To:        order.Email,
		TraceID:   firstNonEmpty(traceID, order.TraceID, db.NewID("trace")),
		Status:    emailDeliveryPending,
	})
}

func (s *Server) queueRedeemEmail(taskID, traceID string) {
	if s == nil || s.DB == nil {
		return
	}
	settings := s.emailNotificationConfig()
	if !settings.Enabled || !settings.NotifyRedeem {
		return
	}
	var task models.RechargeTask
	if err := s.DB.Where("id = ?", strings.TrimSpace(taskID)).First(&task).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			log.Printf("[email] trace_id=%s event=%s status=queue_failed error=%v", traceID, emailRedeemEvent, err)
		}
		return
	}
	if task.Status != models.TaskSucceeded {
		return
	}
	query := s.DB.Where("status = ?", storeOrderPaid)
	if task.CDKID != nil && strings.TrimSpace(*task.CDKID) != "" {
		query = query.Where("cdk_id = ?", strings.TrimSpace(*task.CDKID))
	} else if strings.TrimSpace(task.CDKCode) != "" {
		query = query.Where("cdk_code = ?", strings.TrimSpace(task.CDKCode))
	} else {
		return
	}
	var order models.StoreOrder
	if err := query.First(&order).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			log.Printf("[email] trace_id=%s event=%s status=queue_failed error=%v", firstNonEmpty(traceID, task.TraceID), emailRedeemEvent, err)
		}
		return
	}
	if strings.TrimSpace(order.Email) == "" || strings.TrimSpace(order.CDKCode) == "" {
		return
	}
	s.queueEmailDelivery(models.EmailDelivery{
		ID:        db.NewID("email"),
		EventKey:  emailRedeemEvent + ":" + task.ID,
		EventType: emailRedeemEvent,
		OrderID:   order.ID,
		TaskID:    task.ID,
		To:        order.Email,
		TraceID:   firstNonEmpty(traceID, task.TraceID, db.NewID("trace")),
		Status:    emailDeliveryPending,
	})
}

func (s *Server) queueEmailDelivery(delivery models.EmailDelivery) {
	if s == nil || s.DB == nil || strings.TrimSpace(delivery.EventKey) == "" {
		return
	}
	if delivery.Status == "" {
		delivery.Status = emailDeliveryPending
	}
	result := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery)
	if result.Error != nil {
		log.Printf("[email] trace_id=%s event=%s status=queue_failed error=%v", delivery.TraceID, delivery.EventType, result.Error)
		return
	}
	if result.RowsAffected == 1 {
		go s.deliverEmailDelivery(delivery.ID)
	}
}

func (s *Server) deliverEmailDelivery(deliveryID string) {
	var delivery models.EmailDelivery
	claimed := false
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", deliveryID).First(&delivery).Error; err != nil {
			return err
		}
		if delivery.Status != emailDeliveryPending {
			return nil
		}
		if err := tx.Model(&delivery).Updates(map[string]any{
			"status":        emailDeliverySending,
			"attempt_count": delivery.AttemptCount + 1,
		}).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return
	}
	settings := s.emailNotificationConfig()
	sender, err := s.newEmailSender(settings)
	if err == nil {
		var to, subject, plainBody, htmlBody string
		to, subject, plainBody, htmlBody, err = s.emailDeliveryMessage(delivery, settings)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), settings.SMTP.Timeout+time.Second)
			err = sender.SendGeneric(ctx, to, subject, plainBody, htmlBody)
			cancel()
		}
	}
	if err != nil {
		_ = s.DB.Model(&models.EmailDelivery{}).Where("id = ?", delivery.ID).Updates(map[string]any{"status": emailDeliveryFailed, "error_message": truncate(err.Error(), 1000)}).Error
		log.Printf("[email] trace_id=%s event=%s recipient=%s status=failed error=%v", delivery.TraceID, delivery.EventType, maskStoreEmail(delivery.To), err)
		return
	}
	now := time.Now()
	_ = s.DB.Model(&models.EmailDelivery{}).Where("id = ?", delivery.ID).Updates(map[string]any{"status": emailDeliverySent, "error_message": "", "sent_at": now}).Error
	log.Printf("[email] trace_id=%s event=%s recipient=%s status=sent", delivery.TraceID, delivery.EventType, maskStoreEmail(delivery.To))
}

func (s *Server) emailDeliveryMessage(delivery models.EmailDelivery, settings emailNotificationConfig) (string, string, string, string, error) {
	switch delivery.EventType {
	case emailPurchaseEvent:
		var order models.StoreOrder
		if err := s.DB.Preload("Plan").Where("id = ?", delivery.OrderID).First(&order).Error; err != nil {
			return "", "", "", "", err
		}
		if order.Status != storeOrderPaid || strings.TrimSpace(order.CDKCode) == "" {
			return "", "", "", "", fmt.Errorf("订单尚未完成发卡")
		}
		return buildPurchaseEmail(order, settings)
	case emailRedeemEvent:
		var task models.RechargeTask
		if err := s.DB.Preload("Plan").Where("id = ?", delivery.TaskID).First(&task).Error; err != nil {
			return "", "", "", "", err
		}
		var order models.StoreOrder
		if err := s.DB.Preload("Plan").Where("id = ?", delivery.OrderID).First(&order).Error; err != nil {
			return "", "", "", "", err
		}
		if task.Status != models.TaskSucceeded || order.Status != storeOrderPaid {
			return "", "", "", "", fmt.Errorf("兑换任务尚未成功")
		}
		return buildRedeemEmail(order, task, settings)
	default:
		return "", "", "", "", fmt.Errorf("未知邮件事件类型: %s", delivery.EventType)
	}
}

func buildPurchaseEmail(order models.StoreOrder, settings emailNotificationConfig) (string, string, string, string, error) {
	code := strings.TrimSpace(order.CDKCode)
	planName := firstNonEmpty(order.Plan.Name, order.Plan.Code, "订阅套餐")
	amount := fmt.Sprintf("%.2f %s", order.Amount, firstNonEmpty(order.Currency, models.PlatformStoreCurrency))
	url := publicRechargeURL(settings, "redeem")
	subject := fmt.Sprintf("[%s] 购买成功，您的兑换码", settings.SiteName)
	plain := fmt.Sprintf("您好，\n\n您的商品购买已成功。\n\n商品：%s\n订单号：%s\n兑换码：%s\n金额：%s\n\n请打开以下地址输入兑换码完成开通：\n%s\n\n这是系统自动发送的邮件，请勿直接回复。", planName, order.OrderNo, code, amount, url)
	htmlBody := emailHTML(settings.SiteName, "购买成功", "您的商品购买已成功。", []emailLine{
		{Label: "商品", Value: planName}, {Label: "订单号", Value: order.OrderNo}, {Label: "兑换码", Value: code}, {Label: "金额", Value: amount},
	}, url, "打开兑换页面")
	return order.Email, subject, plain, htmlBody, nil
}

func buildRedeemEmail(order models.StoreOrder, task models.RechargeTask, settings emailNotificationConfig) (string, string, string, string, error) {
	code := firstNonEmpty(order.CDKCode, task.CDKCode)
	planName := firstNonEmpty(order.Plan.Name, task.Plan.Name, order.Plan.Code, task.Plan.Code, "订阅套餐")
	url := publicRechargeURL(settings, "redeem")
	subject := fmt.Sprintf("[%s] CDK 兑换成功，服务已开通", settings.SiteName)
	plain := fmt.Sprintf("您好，\n\n您的 CDK 已兑换成功，服务已开通。\n\n商品：%s\n订单号：%s\n兑换码：%s\n完成时间：%s\n\n如需查询订单或再次查看兑换入口，请打开：\n%s\n\n这是系统自动发送的邮件，请勿直接回复。", planName, order.OrderNo, code, finishedTime(task), url)
	htmlBody := emailHTML(settings.SiteName, "兑换成功", "您的 CDK 已兑换成功，服务已开通。", []emailLine{
		{Label: "商品", Value: planName}, {Label: "订单号", Value: order.OrderNo}, {Label: "兑换码", Value: code}, {Label: "完成时间", Value: finishedTime(task)},
	}, url, "打开订单查询页面")
	return order.Email, subject, plain, htmlBody, nil
}

type emailLine struct {
	Label string
	Value string
}

func emailHTML(siteName, title, intro string, lines []emailLine, link, linkLabel string) string {
	var builder strings.Builder
	builder.WriteString(`<div style="font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Arial,sans-serif;max-width:640px;color:#0f172a;line-height:1.6">`)
	fmt.Fprintf(&builder, `<h2 style="margin:0 0 12px;color:#0f172a">%s</h2><p>%s</p><table style="border-collapse:collapse;width:100%%;margin:18px 0">`, html.EscapeString(title), html.EscapeString(intro))
	for _, line := range lines {
		fmt.Fprintf(&builder, `<tr><td style="padding:9px 12px;border-bottom:1px solid #e2e8f0;color:#64748b;width:100px">%s</td><td style="padding:9px 12px;border-bottom:1px solid #e2e8f0;font-weight:600;word-break:break-all">%s</td></tr>`, html.EscapeString(line.Label), html.EscapeString(line.Value))
	}
	fmt.Fprintf(&builder, `</table><p><a href="%s" style="display:inline-block;padding:10px 16px;background:#2563eb;color:#fff;text-decoration:none;border-radius:6px">%s</a></p><p style="color:#64748b;font-size:12px">%s 自动发送，请勿直接回复。</p></div>`, html.EscapeString(link), html.EscapeString(linkLabel), html.EscapeString(siteName))
	return builder.String()
}

func publicRechargeURL(settings emailNotificationConfig, tab string) string {
	if settings.PublicBaseURL == "" {
		return "/recharge?tab=" + tab
	}
	return settings.PublicBaseURL + "/recharge?tab=" + tab
}

func finishedTime(task models.RechargeTask) string {
	if task.FinishedAt != nil {
		return task.FinishedAt.Format("2006-01-02 15:04:05")
	}
	return task.UpdatedAt.Format("2006-01-02 15:04:05")
}
