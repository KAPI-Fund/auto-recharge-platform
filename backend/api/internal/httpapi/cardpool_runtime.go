package httpapi

import (
	"net/http"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/airwallex"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/dogpay"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/kimoox"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/localtext"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/photonpay"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/stripeissuing"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"gorm.io/gorm"
)

// NewCardPoolService is the single composition root for card providers. The
// business layer receives only cardpool.Service and never imports provider DTOs.
func NewCardPoolService(database *gorm.DB, cfg config.Config) *cardpool.Service {
	reader := cardpool.DBConfigReader{DB: database, EncryptionKey: cfg.SessionEncryptionKey, EncryptionKeyFallbacks: cfg.SessionEncryptionKeyFallbacks}
	return newCardPoolServiceWithReader(database, cfg, reader)
}

func newCardPoolServiceWithReader(database *gorm.DB, cfg config.Config, reader cardpool.ConfigReader) *cardpool.Service {
	registry := cardpool.NewProviderRegistry()
	registry.Register(localtext.NewWithKeys(database, cfg.SessionEncryptionKey, cfg.SessionEncryptionKeyFallbacks, reader))
	registry.Register(airwallex.New(reader, &http.Client{Timeout: 30 * time.Second}))
	registry.Register(stripeissuing.New(reader, &http.Client{Timeout: 30 * time.Second}))
	registry.Register(photonpay.New(reader, &http.Client{Timeout: 30 * time.Second}))
	registry.Register(dogpay.New(reader, &http.Client{Timeout: 30 * time.Second}))
	registry.Register(kimoox.New(reader, &http.Client{Timeout: 30 * time.Second}))
	return cardpool.NewService(database, registry, reader)
}
