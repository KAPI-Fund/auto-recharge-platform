package queue

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

type TaskMessage struct {
	TaskID  string `json:"taskId"`
	Mode    string `json:"mode"`
	Kind    string `json:"kind,omitempty"`
	TraceID string `json:"traceId,omitempty"`
}

type Client struct {
	Redis *redis.Client
	Name  string
}

func New(redisURL, name string) (*Client, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &Client{Redis: redis.NewClient(options), Name: name}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.Redis.Ping(ctx).Err()
}

func (c *Client) Enqueue(ctx context.Context, message TaskMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.Redis.RPush(ctx, c.Name, payload).Err()
}

func (c *Client) Close() error {
	return c.Redis.Close()
}
