package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string
	WorkerToken string
	TraceID     string
	http        *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		WorkerToken: token,
		http:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) SetTrace(id string) { c.TraceID = strings.TrimSpace(id) }

func (c *Client) request(method, path string, headers map[string]string, payload any) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Worker-Token", c.WorkerToken)
	if c.TraceID != "" {
		req.Header.Set("X-Trace-ID", c.TraceID)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &data)
	}
	if resp.StatusCode >= 400 {
		message := "API request failed"
		if data != nil {
			if text, _ := data["message"].(string); text != "" {
				message = text
			}
		}
		return data, fmt.Errorf("%s (%d)", message, resp.StatusCode)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}

func (c *Client) Claim(taskID, workerID string) (map[string]any, error) {
	return c.request("POST", "/internal/tasks/"+urlPath(taskID)+"/claim", nil, map[string]any{"workerId": workerID})
}

func (c *Client) Heartbeat(taskID, workerID, lease string) error {
	_, err := c.request("POST", "/internal/tasks/"+urlPath(taskID)+"/heartbeat", nil, map[string]any{"workerId": workerID, "leaseToken": lease})
	return err
}

func (c *Client) Secret(taskID, workerID, lease string) (map[string]any, error) {
	return c.request("GET", "/internal/tasks/"+urlPath(taskID)+"/secret", map[string]string{"X-Worker-ID": workerID, "X-Worker-Lease-Token": lease}, nil)
}

func (c *Client) Runtime(taskID, workerID, lease string) (map[string]any, error) {
	return c.request("GET", "/internal/tasks/"+urlPath(taskID)+"/runtime", map[string]string{"X-Worker-ID": workerID, "X-Worker-Lease-Token": lease}, nil)
}

func (c *Client) Update(taskID string, body map[string]any) error {
	_, err := c.request("PATCH", "/internal/tasks/"+urlPath(taskID), nil, body)
	return err
}

func (c *Client) Log(body map[string]any) {
	_, _ = c.request("POST", "/internal/runtime-logs", nil, body)
}

func (c *Client) Config() (map[string]any, error) {
	return c.request("GET", "/internal/config", nil, nil)
}

func (c *Client) Action(action string, body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	return c.request("POST", "/internal/store/"+urlPath(action), nil, body)
}

func urlPath(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), " ", "")
}

func Str(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func Map(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}
