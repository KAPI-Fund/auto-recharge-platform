package cardpool

import (
	"strings"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
)

// MapConfigReader is an immutable, in-memory configuration source. It is used
// for validation snapshots and tests so a provider can be evaluated against a
// consistent view without issuing one database query per setting.
type MapConfigReader struct {
	values map[string]string
}

// NewMapConfigReader creates a configuration reader backed by a defensive
// copy of values. Empty values are retained so callers can intentionally
// validate a cleared non-secret setting as missing.
func NewMapConfigReader(values map[string]string) ConfigReader {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[strings.TrimSpace(key)] = value
	}
	return MapConfigReader{values: copyValues}
}

func (r MapConfigReader) Value(key, fallback string) string {
	if value, ok := r.values[strings.TrimSpace(key)]; ok {
		if strings.TrimSpace(value) != "" {
			return value
		}
		return fallback
	}
	return fallback
}

func (r MapConfigReader) Secret(key, fallback string) string {
	return r.Value(key, fallback)
}

// OverlayConfigReader applies request-scoped values over a persisted
// configuration reader. It lets the API validate a complete post-save state
// before any value is written.
type OverlayConfigReader struct {
	Base   ConfigReader
	Values map[string]string
}

func NewOverlayConfigReader(base ConfigReader, values map[string]string) ConfigReader {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[strings.TrimSpace(key)] = value
	}
	return OverlayConfigReader{Base: base, Values: copyValues}
}

func (r OverlayConfigReader) Value(key, fallback string) string {
	key = strings.TrimSpace(key)
	if value, ok := r.Values[key]; ok {
		return value
	}
	if r.Base != nil {
		return r.Base.Value(key, fallback)
	}
	return fallback
}

func (r OverlayConfigReader) Secret(key, fallback string) string {
	key = strings.TrimSpace(key)
	if value, ok := r.Values[key]; ok {
		return value
	}
	if r.Base != nil {
		return r.Base.Secret(key, fallback)
	}
	return fallback
}

// DBConfigReader keeps provider settings in the existing System Settings
// store. Secret values are encrypted at rest and are never returned through
// the ordinary Value method.
type DBConfigReader struct {
	DB                     *gorm.DB
	EncryptionKey          string
	EncryptionKeyFallbacks []string
}

func (r DBConfigReader) Value(key, fallback string) string {
	if r.DB == nil {
		return fallback
	}
	var config models.AppConfig
	if err := r.DB.Where("key = ?", strings.TrimSpace(key)).First(&config).Error; err != nil || config.IsSecret {
		return fallback
	}
	if strings.TrimSpace(config.Value) == "" {
		return fallback
	}
	return config.Value
}

func (r DBConfigReader) Secret(key, fallback string) string {
	if r.DB == nil {
		return fallback
	}
	var config models.AppConfig
	if err := r.DB.Where("key = ?", strings.TrimSpace(key)).First(&config).Error; err != nil {
		return fallback
	}
	value := strings.TrimSpace(config.Value)
	if value == "" {
		return fallback
	}
	if !config.IsSecret {
		return value
	}
	keys := append([]string{r.EncryptionKey}, r.EncryptionKeyFallbacks...)
	decrypted, err := security.DecryptWithFallbacks(value, keys...)
	if err != nil || strings.TrimSpace(decrypted) == "" {
		return fallback
	}
	return decrypted
}
