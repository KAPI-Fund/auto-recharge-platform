package httpapi

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

const maxCardProviderWebhookBodySize = 1 << 20

func (s *Server) handleCardProviderWebhook(c *gin.Context, provider string) {
	provider = strings.ToUpper(strings.TrimSpace(provider))
	if s.CardPools == nil {
		fail(c, http.StatusServiceUnavailable, "银行卡 Provider 服务未初始化")
		return
	}
	if c.Request.ContentLength > maxCardProviderWebhookBodySize {
		fail(c, http.StatusBadRequest, "Provider Webhook 请求体过大")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxCardProviderWebhookBodySize+1))
	if err != nil {
		fail(c, http.StatusBadRequest, "读取 Provider Webhook 失败")
		return
	}
	if len(body) > maxCardProviderWebhookBodySize {
		fail(c, http.StatusBadRequest, "Provider Webhook 请求体过大")
		return
	}
	traceID := requestTraceID(c)
	result, err := s.CardPools.ProcessWebhook(c.Request.Context(), provider, c.Request.Header, body, traceID)
	if err != nil {
		if cardpool.IsWebhookValidationError(err) {
			fail(c, http.StatusBadRequest, "Provider Webhook 校验失败")
			return
		}
		if errors.Is(err, cardpool.ErrProviderNotFound) || errors.Is(err, cardpool.ErrUnsupportedCapability) {
			fail(c, http.StatusNotFound, "Provider Webhook 不受支持")
			return
		}
		log.Printf("[card-webhook] trace_id=%s provider=%s status=failed error=%v", traceID, provider, err)
		fail(c, http.StatusInternalServerError, "Provider Webhook 处理失败")
		return
	}
	log.Printf("[card-webhook] trace_id=%s provider=%s event_type=%s duplicate=%t ignored=%t card_updated=%t transaction_saved=%t", traceID, result.Provider, result.EventType, result.Duplicate, result.Ignored, result.CardUpdated, result.TransactionSaved)
	if provider == "KIMOOX" {
		// Kimoox only treats 2xx with a trimmed body of "ok" as delivered.
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte("ok"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"received": true, "processed": !result.Ignored, "duplicate": result.Duplicate,
		"ignored": result.Ignored, "provider": result.Provider, "eventType": result.EventType,
		"eventId": result.ProviderEventID, "traceId": traceID, "trace_id": traceID,
	})
}
