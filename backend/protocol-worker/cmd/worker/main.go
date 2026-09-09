package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/flow"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/queue"
	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/store"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	q, err := queue.New(cfg.RedisURL, cfg.QueueName, cfg.WorkerID)
	if err != nil {
		log.Fatal(err)
	}
	defer q.Close()
	if n, err := q.Recover(ctx); err == nil && n > 0 {
		log.Printf("[protocol] recovered %d in-flight task(s)", n)
	}
	api := store.New(cfg.APIBaseURL, cfg.WorkerToken)
	log.Printf("[protocol] %s listening on %s (Go HTTP, no page payment)", cfg.WorkerID, cfg.QueueName)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		message, err := q.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[protocol] queue error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if message == nil {
			continue
		}
		if err := process(ctx, cfg, api, message); err != nil {
			if errors.Is(err, errSkipForeignTask) {
				q.Requeue(ctx, message)
				time.Sleep(750 * time.Millisecond)
				continue
			}
			log.Printf("[protocol] task error: %v", err)
			q.Requeue(ctx, message)
			continue
		}
		q.Ack(ctx, message)
	}
}

var errSkipForeignTask = errors.New("skip_foreign_task")

func process(ctx context.Context, cfg config.Config, api *store.Client, message *queue.Message) error {
	if strings.TrimSpace(message.Kind) == "product_generation" {
		log.Printf("[protocol] skip product_generation %s", message.TaskID)
		return nil
	}
	if strings.TrimSpace(message.Mode) != "protocol" {
		return errSkipForeignTask
	}
	taskID := strings.TrimSpace(message.TaskID)
	if taskID == "" {
		return nil
	}
	api.SetTrace(message.TraceID)
	workerID := cfg.WorkerID + "-" + uuid.NewString()[:8]
	claim, err := api.Claim(taskID, workerID)
	if err != nil {
		return err
	}
	if claimed, _ := claim["claimed"].(bool); claimed == false {
		return nil
	}
	if terminal, _ := claim["terminal"].(bool); terminal {
		return nil
	}
	lease := store.Str(claim, "leaseToken")
	if lease == "" {
		return fmt.Errorf("claim 未返回租约")
	}
	traceID := firstNonEmpty(store.Str(claim, "traceId"), store.Str(claim, "trace_id"), message.TraceID)
	api.SetTrace(traceID)

	stopBeat := startHeartbeat(api, taskID, workerID, lease)
	defer stopBeat()

	runtime, err := api.Runtime(taskID, workerID, lease)
	if err != nil {
		return err
	}
	secret, err := api.Secret(taskID, workerID, lease)
	if err != nil {
		return err
	}
	jobKey := firstNonEmpty(store.Str(secret, "jobKey"), store.Str(runtime, "jobKey"), taskID)
	logTask := func(text string) {
		api.Log(map[string]any{"taskId": taskID, "jobKey": jobKey, "traceId": traceID, "level": "stdout", "source": "protocol", "text": text, "workerId": workerID, "leaseToken": lease})
	}
	_ = api.Update(taskID, map[string]any{"status": "running", "progress": 5, "message": "协议 Worker 已接管任务", "workerId": workerID, "leaseToken": lease})
	logTask("协议 Worker 已接管任务 job=" + jobKey)

	result := flow.Result{Status: "failed"}
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		_ = api.Update(taskID, map[string]any{"status": "running", "progress": 6, "message": fmt.Sprintf("协议支付第 %d/%d 次", attempt, cfg.MaxAttempts), "workerId": workerID, "leaseToken": lease, "attempt": attempt})
		if store.Str(secret, "region") == "" {
			secret["region"] = cfg.PaymentRegion
		}
		proxy, err := api.Action("getActiveProxy", map[string]any{"region": store.Str(secret, "region")})
		if err != nil {
			result = flow.Result{Status: "retry", Message: err.Error(), ErrorCode: "proxy_unavailable"}
			if attempt == cfg.MaxAttempts {
				break
			}
			continue
		}
		proxyURL := strings.TrimSpace(store.Str(proxy, "proxy"))
		proxyID := strings.TrimSpace(firstNonEmpty(store.Str(proxy, "id"), store.Str(proxy, "proxyId")))
		if proxyURL == "" && proxyID != "" {
			_, _ = api.Action("releaseProxy", map[string]any{"id": proxyID})
			proxyID = ""
		}
		secret["proxy"] = proxyURL
		result = flow.Run(flow.Options{
			Config: cfg,
			Store:  api,
			Secret: secret,
			OnLog: logTask,
			OnProg: func(progress int, message string) {
				_ = api.Update(taskID, map[string]any{"status": "running", "progress": progress, "message": message, "workerId": workerID, "leaseToken": lease})
			},
		})
		if proxyID != "" {
			_, _ = api.Action("releaseProxy", map[string]any{"id": proxyID})
		}
		if result.Status != "retry" || attempt == cfg.MaxAttempts {
			break
		}
	}

	status := result.Status
	if status == "retry" {
		status = "failed"
	}
	progress := 99
	if status == "succeeded" {
		progress = 100
	} else if status == "manual" {
		progress = 92
	}
	return api.Update(taskID, map[string]any{
		"status": status, "progress": progress, "message": result.Message,
		"rawOutput": strings.Join(result.Output, "\n"), "cardLast4": result.CardLast4,
		"errorCode": result.ErrorCode, "errorMessage": emptyIf(result.Status == "succeeded", result.Message),
		"workerId": workerID, "leaseToken": lease,
	})
}

func startHeartbeat(api *store.Client, taskID, workerID, lease string) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = api.Heartbeat(taskID, workerID, lease)
			}
		}
	}()
	return func() { close(done) }
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func emptyIf(ok bool, value string) string {
	if ok {
		return ""
	}
	return value
}
