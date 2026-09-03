package httpapi

import "github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"

// decryptSessionValue keeps legacy encrypted Session/card/config data readable
// during an explicit key rotation. New values continue to use
// s.Cfg.SessionEncryptionKey at the write sites.
func (s *Server) decryptSessionValue(value string) (string, error) {
	if s == nil {
		return security.DecryptWithFallbacks(value)
	}
	return security.DecryptWithFallbacks(value, s.Cfg.SessionEncryptionKeys()...)
}
