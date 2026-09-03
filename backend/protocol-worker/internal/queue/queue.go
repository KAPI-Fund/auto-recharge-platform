package queue

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Message struct {
	TaskID     string `json:"taskId"`
	Mode       string `json:"mode"`
	Kind       string `json:"kind"`
	TraceID    string `json:"traceId"`
	Raw        string `json:"-"`
}

type Client struct {
	redis      *redis.Client
	name       string
	processing string
}

func New(redisURL, name, workerID string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ':' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, workerID)
	return &Client{
		redis:      redis.NewClient(opts),
		name:       name,
		processing: name + ":processing:" + safe,
	}, nil
}

func (c *Client) Next(ctx context.Context) (*Message, error) {
	raw, err := c.redis.BRPopLPush(ctx, c.name, c.processing, 5*time.Second).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var message Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		_ = c.redis.LRem(ctx, c.processing, 1, raw).Err()
		return nil, nil
	}
	message.Raw = raw
	return &message, nil
}

func (c *Client) Ack(ctx context.Context, message *Message) {
	if message == nil || message.Raw == "" {
		return
	}
	_ = c.redis.LRem(ctx, c.processing, 1, message.Raw).Err()
}

func (c *Client) Requeue(ctx context.Context, message *Message) {
	if message == nil || message.Raw == "" {
		return
	}
	_ = c.redis.LRem(ctx, c.processing, 1, message.Raw).Err()
	_ = c.redis.RPush(ctx, c.name, message.Raw).Err()
}

func (c *Client) Recover(ctx context.Context) (int, error) {
	items, err := c.redis.LRange(ctx, c.processing, 0, -1).Result()
	if err != nil {
		return 0, err
	}
	for _, raw := range items {
		_ = c.redis.LRem(ctx, c.processing, 1, raw).Err()
		_ = c.redis.RPush(ctx, c.name, raw).Err()
	}
	return len(items), nil
}

func (c *Client) Close() error { return c.redis.Close() }
