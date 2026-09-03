package session

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

type Session struct {
	AccessToken string
	Email       string
	AccountID   string
	DeviceID    string
	Expired     bool
	Cookies     map[string]string
}

var jwtRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

func Parse(raw string) (*Session, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errInvalid("缺少 Session")
	}
	out := &Session{Cookies: map[string]string{}}
	if strings.HasPrefix(raw, "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			out.AccessToken = firstString(payload, "accessToken", "access_token", "token")
			out.DeviceID = firstString(payload, "deviceId", "device_id", "oai-did")
			if user, _ := payload["user"].(map[string]any); user != nil {
				out.Email = firstString(user, "email")
			}
			if cookies, ok := payload["cookies"].([]any); ok {
				for _, item := range cookies {
					row, _ := item.(map[string]any)
					name := strings.TrimSpace(asString(row["name"]))
					value := strings.TrimSpace(asString(row["value"]))
					domain := strings.ToLower(asString(row["domain"]))
					if name == "" || value == "" {
						continue
					}
					if domain != "" && !strings.Contains(domain, "chatgpt.com") && !strings.Contains(domain, "openai.com") {
						continue
					}
					if isBrowserBoundCookie(name) {
						continue
					}
					out.Cookies[name] = value
				}
			}
			if token := firstString(payload, "sessionToken", "session_token", "__Secure-next-auth.session-token"); token != "" {
				if _, exists := out.Cookies["__Secure-next-auth.session-token"]; !exists {
					out.Cookies["__Secure-next-auth.session-token"] = token
				}
			}
		}
	}
	if out.AccessToken == "" {
		out.AccessToken = jwtRe.FindString(raw)
	}
	if out.AccessToken == "" {
		return nil, errInvalid("Session 缺少 AccessToken")
	}
	profile := DecodeProfile(out.AccessToken)
	if out.Email == "" {
		out.Email = profile.Email
	}
	if out.DeviceID == "" {
		out.DeviceID = out.Cookies["oai-did"]
	}
	if out.DeviceID != "" {
		out.Cookies["oai-did"] = out.DeviceID
	}
	out.AccountID = profile.AccountID
	out.Expired = profile.Expired
	return out, nil
}

type Profile struct {
	Email     string
	AccountID string
	Expired   bool
}

func DecodeProfile(token string) Profile {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Profile{Expired: true}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Profile{Expired: true}
	}
	var data map[string]any
	if json.Unmarshal(payload, &data) != nil {
		return Profile{Expired: true}
	}
	auth, _ := data["https://api.openai.com/auth"].(map[string]any)
	profile, _ := data["https://api.openai.com/profile"].(map[string]any)
	exp, _ := data["exp"].(float64)
	return Profile{
		Email:     asString(profile["email"]),
		AccountID: asString(auth["chatgpt_account_id"]),
		Expired:   exp > 0 && time.Unix(int64(exp), 0).Before(time.Now()),
	}
}

type parseError string

func (e parseError) Error() string { return string(e) }

func errInvalid(message string) error { return parseError(message) }

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(asString(data[key])); value != "" {
			return value
		}
	}
	return ""
}

func isBrowserBoundCookie(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(lower, "cf_") || lower == "__cf_bm" || lower == "_cfuvid" || strings.Contains(lower, "clearance")
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
