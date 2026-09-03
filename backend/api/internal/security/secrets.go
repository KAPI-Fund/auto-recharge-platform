package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

func Encrypt(plaintext, secret string) (string, error) {
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Decrypt(encoded, secret string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(decoded) < gcm.NonceSize() {
		return "", errors.New("encrypted value is too short")
	}
	nonce, ciphertext := decoded[:gcm.NonceSize()], decoded[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DecryptWithFallbacks attempts the primary key and then each explicitly
// configured fallback key. Fallbacks are intended for controlled key rotation
// or migration of data written by an older process; callers still encrypt new
// values with the primary key.
func DecryptWithFallbacks(encoded string, secrets ...string) (string, error) {
	var lastErr error
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		plaintext, err := Decrypt(encoded, secret)
		if err == nil {
			return plaintext, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return "", errors.New("no encryption key configured")
	}
	return "", lastErr
}

func SessionPreview(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 18 {
		return trimmed[:min(len(trimmed), 6)] + "..."
	}
	return trimmed[:8] + "..." + trimmed[len(trimmed)-6:]
}

// CreatePasswordHash matches the scrypt$<salt>$<hex> format used by the
// original Node service so credentials can be migrated without a reset.
func CreatePasswordHash(password string) (string, error) {
	saltBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		return "", err
	}
	salt := base64.RawURLEncoding.EncodeToString(saltBytes)
	hash, err := scrypt.Key([]byte(password), []byte(salt), 16384, 8, 1, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$%s$%x", salt, hash), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "scrypt" || parts[1] == "" || parts[2] == "" {
		return false
	}
	expected, err := hexDecode(parts[2])
	if err != nil {
		return false
	}
	actual, err := scrypt.Key([]byte(password), []byte(parts[1]), 16384, 8, 1, len(expected))
	if err != nil {
		return false
	}
	return hmac.Equal(actual, expected)
}

func hexDecode(value string) ([]byte, error) {
	if len(value)%2 != 0 {
		return nil, fmt.Errorf("invalid hex length")
	}
	result := make([]byte, len(value)/2)
	for i := range result {
		left, err := strconv.ParseUint(value[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		result[i] = byte(left)
	}
	return result, nil
}

func SignToken(payload map[string]any, secret string, ttl time.Duration) (string, map[string]any) {
	now := time.Now().UnixMilli()
	copyPayload := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		copyPayload[key] = value
	}
	copyPayload["iat"] = now
	copyPayload["exp"] = now + ttl.Milliseconds()
	raw, _ := json.Marshal(copyPayload)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
	return encoded + "." + signature, copyPayload
}

func VerifyToken(token, secret, subject string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, false
	}
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	if subject != "" && fmt.Sprint(payload["sub"]) != subject {
		return nil, false
	}
	exp, ok := payload["exp"].(float64)
	if !ok || time.Now().UnixMilli() >= int64(exp) {
		return nil, false
	}
	return payload, true
}

func GenerateTOTP(secret string, counter int64) string {
	key := decodeBase32(secret)
	buffer := make([]byte, 8)
	binary.BigEndian.PutUint64(buffer, uint64(counter))
	digest := hmac.New(sha1.New, key)
	_, _ = digest.Write(buffer)
	sum := digest.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1000000)
}

func VerifyTOTP(secret, code string) bool {
	secret = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	code = strings.TrimSpace(code)
	if secret == "" || len(code) != 6 {
		return false
	}
	counter := time.Now().Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		if hmac.Equal([]byte(GenerateTOTP(secret, counter+offset)), []byte(code)) {
			return true
		}
	}
	return false
}

func decodeBase32(value string) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	value = strings.TrimRight(strings.ToUpper(strings.TrimSpace(value)), "=")
	var result []byte
	bits, buffer := 0, 0
	for _, char := range value {
		index := strings.IndexRune(alphabet, char)
		if index < 0 {
			continue
		}
		buffer = (buffer << 5) | index
		bits += 5
		if bits >= 8 {
			result = append(result, byte(buffer>>(bits-8)))
			bits -= 8
		}
	}
	return result
}

func NormalizeSession(value string) (string, error) {
	raw, token, err := NormalizeSessionPayload(value)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", errors.New("session is required")
	}
	return token, nil
}

func NormalizeSessionPayload(value string) (string, string, error) {
	trimmed := strings.Trim(strings.TrimSpace(value), "\"'")
	if trimmed == "" {
		return "", "", errors.New("session is required")
	}
	if len(trimmed) > 128*1024 {
		return "", "", errors.New("session is too large")
	}
	var payload map[string]any
	if json.Unmarshal([]byte(trimmed), &payload) == nil {
		for _, key := range []string{"accessToken", "access_token", "token"} {
			if token, ok := payload[key].(string); ok && strings.TrimSpace(token) != "" {
				return trimmed, strings.TrimSpace(token), nil
			}
		}
	}
	return trimmed, trimmed, nil
}

// ValidateAccessToken mirrors the legacy server's structural checks. The
// original service intentionally did not verify the OpenAI signature here;
// it validated the JWT contract before the browser/upstream flow performed
// the actual account check.
func ValidateAccessToken(token string) error {
	value := strings.TrimSpace(token)
	if value == "" {
		return errors.New("缺少 AccessToken")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return errors.New("该 Token 不合法：格式错误")
	}
	header, err := decodeJWTPart(parts[0])
	if err != nil {
		return errors.New("该 Token 不合法：无法解析")
	}
	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return errors.New("该 Token 不合法：无法解析")
	}
	if fmt.Sprint(header["typ"]) != "JWT" {
		return errors.New("该 Token 不合法：类型错误")
	}
	if fmt.Sprint(header["alg"]) != "RS256" {
		return errors.New("该 Token 不合法：算法错误")
	}
	if fmt.Sprint(payload["iss"]) != "https://auth.openai.com" {
		return errors.New("该 Token 不合法：签发方错误")
	}
	if !containsStringClaim(payload["aud"], "https://api.openai.com/v1") {
		return errors.New("该 Token 不合法：aud 不匹配")
	}
	auth, ok := payload["https://api.openai.com/auth"].(map[string]any)
	if !ok || auth["chatgpt_account_id"] == nil || auth["chatgpt_user_id"] == nil || strings.TrimSpace(fmt.Sprint(auth["chatgpt_account_id"])) == "" || strings.TrimSpace(fmt.Sprint(auth["chatgpt_user_id"])) == "" {
		return errors.New("该 Token 不合法：缺少账户信息")
	}
	if !containsStringArrayClaim(payload["scp"], "model.request") {
		return errors.New("该 Token 不合法：缺少 model.request 权限")
	}
	exp, ok := payload["exp"].(float64)
	if !ok || exp <= 0 || exp != exp {
		return errors.New("该 Token 不合法：缺少过期时间")
	}
	if exp <= float64(time.Now().Unix()) {
		return errors.New("该 Token 已过期")
	}
	return nil
}

func decodeJWTPart(part string) (map[string]any, error) {
	value := strings.NewReplacer("-", "+", "_", "/").Replace(part)
	value += strings.Repeat("=", (4-len(value)%4)%4)
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func containsStringClaim(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return typed == expected
	case []any:
		for _, item := range typed {
			if fmt.Sprint(item) == expected {
				return true
			}
		}
	}
	return false
}

func containsStringArrayClaim(value any, expected string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if fmt.Sprint(item) == expected {
			return true
		}
	}
	return false
}

func deriveKey(secret string) []byte {
	digest := sha256.Sum256([]byte(secret))
	return digest[:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
