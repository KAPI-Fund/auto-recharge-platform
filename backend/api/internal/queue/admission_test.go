package queue

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRedisAdmissionLeaseSupportsLimitRefreshReleaseAndExpiry(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("RECHARGE_TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("set RECHARGE_TEST_REDIS_URL to run the real Redis admission test")
	}

	client, err := New(redisURL, "recharge:admission-test:"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatalf("open Redis client: %v", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	first, err := client.TryAcquireAdmission(ctx, "first", 1, 2*time.Second)
	if err != nil || !first {
		t.Fatalf("first admission = %t, err=%v", first, err)
	}
	second, err := client.TryAcquireAdmission(ctx, "second", 1, 2*time.Second)
	if err != nil || second {
		t.Fatalf("second admission = %t, err=%v, want rejected", second, err)
	}
	refreshed, err := client.RefreshAdmission(ctx, "first", 2*time.Second)
	if err != nil || !refreshed {
		t.Fatalf("refresh = %t, err=%v", refreshed, err)
	}
	if err := client.ReleaseAdmission(ctx, "first"); err != nil {
		t.Fatalf("release = %v", err)
	}
	third, err := client.TryAcquireAdmission(ctx, "third", 1, 2*time.Second)
	if err != nil || !third {
		t.Fatalf("admission after release = %t, err=%v", third, err)
	}
	if err := client.ReleaseAdmission(ctx, "third"); err != nil {
		t.Fatalf("second release = %v", err)
	}

	// A leaked reservation must stop blocking new work after its TTL even if
	// the API process never reaches its normal release path.
	leaked, err := client.TryAcquireAdmission(ctx, "leaked", 1, time.Second)
	if err != nil || !leaked {
		t.Fatalf("leaked admission = %t, err=%v", leaked, err)
	}
	time.Sleep(1200 * time.Millisecond)
	recovered, err := client.TryAcquireAdmission(ctx, "recovered", 1, time.Second)
	if err != nil || !recovered {
		t.Fatalf("admission after TTL = %t, err=%v", recovered, err)
	}
	if err := client.ReleaseAdmission(ctx, "recovered"); err != nil {
		t.Fatalf("final release = %v", err)
	}
}
