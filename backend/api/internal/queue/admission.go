package queue

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	admissionAcquireScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local ttl = math.max(1000, tonumber(ARGV[2]))
local limit = math.max(1, tonumber(ARGV[3]))
local token = ARGV[4]
local expires = now + ttl

redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if redis.call('ZSCORE', KEYS[1], token) then
  redis.call('ZADD', KEYS[1], expires, token)
  redis.call('EXPIRE', KEYS[1], math.ceil(ttl / 1000) + 1)
  return 1
end
if tonumber(redis.call('ZCARD', KEYS[1])) >= limit then
  return 0
end
redis.call('ZADD', KEYS[1], expires, token)
redis.call('EXPIRE', KEYS[1], math.ceil(ttl / 1000) + 1)
return 1
`)
	admissionRefreshScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local ttl = math.max(1000, tonumber(ARGV[2]))
local token = ARGV[3]
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if not redis.call('ZSCORE', KEYS[1], token) then
  return 0
end
redis.call('ZADD', KEYS[1], now + ttl, token)
redis.call('EXPIRE', KEYS[1], math.ceil(ttl / 1000) + 1)
return 1
`)
	admissionReleaseScript = redis.NewScript(`
local token = ARGV[1]
local removed = redis.call('ZREM', KEYS[1], token)
if redis.call('ZCARD', KEYS[1]) == 0 then
  redis.call('DEL', KEYS[1])
end
return removed
`)
)

var errAdmissionClientUnavailable = errors.New("redis admission client is unavailable")

func (c *Client) admissionKey() string {
	if c == nil || strings.TrimSpace(c.Name) == "" {
		return "recharge:admission"
	}
	return strings.TrimSpace(c.Name) + ":admission"
}

func admissionTTLMillis(ttl time.Duration) int64 {
	if ttl < time.Second {
		ttl = time.Second
	}
	return ttl.Milliseconds()
}

// TryAcquireAdmission reserves one distributed execution slot. The sorted set
// score is the reservation expiry, so abandoned reservations self-heal even if
// the API process dies before it can release the token.
func (c *Client) TryAcquireAdmission(ctx context.Context, token string, limit int, ttl time.Duration) (bool, error) {
	if c == nil || c.Redis == nil {
		return false, errAdmissionClientUnavailable
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, errors.New("redis admission token is required")
	}
	if limit < 1 {
		limit = 1
	}
	result, err := admissionAcquireScript.Run(ctx, c.Redis, []string{c.admissionKey()}, time.Now().UnixMilli(), admissionTTLMillis(ttl), limit, token).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// RefreshAdmission extends a reservation held by a running task. A missing
// token is deliberately reported as false rather than recreated: PostgreSQL
// remains the authoritative limiter after a Redis restart or key expiry.
func (c *Client) RefreshAdmission(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	if c == nil || c.Redis == nil {
		return false, errAdmissionClientUnavailable
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	result, err := admissionRefreshScript.Run(ctx, c.Redis, []string{c.admissionKey()}, time.Now().UnixMilli(), admissionTTLMillis(ttl), token).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *Client) ReleaseAdmission(ctx context.Context, token string) error {
	if c == nil || c.Redis == nil {
		return errAdmissionClientUnavailable
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return admissionReleaseScript.Run(ctx, c.Redis, []string{c.admissionKey()}, token).Err()
}
