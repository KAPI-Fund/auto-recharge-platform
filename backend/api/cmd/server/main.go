package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/httpapi"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
)

func main() {
	cfg := config.Load()
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	queueClient, err := queue.New(cfg.RedisURL, cfg.QueueName)
	if err != nil {
		log.Fatalf("create redis client: %v", err)
	}
	defer queueClient.Close()
	if err := queueClient.Ping(context.Background()); err != nil {
		log.Fatalf("redis is unavailable: %v", err)
	}

	server := &httpapi.Server{DB: database, Cfg: cfg, Q: queueClient, CardPools: httpapi.NewCardPoolService(database, cfg)}
	if err := server.CardPools.ValidateConfiguration(); err != nil {
		log.Fatalf("validate card provider configuration: %v", err)
	}
	if report, err := server.ResetAssetLocks(); err != nil {
		log.Printf("asset lock startup reset failed: %v", err)
	} else {
		log.Printf("asset locks reset at startup: phone=%d card=%d pool_emails=%d proxies=%d", report.PhoneReleased, report.CardReleased, report.PoolReleased, report.ProxyReleased)
	}
	if cleaned, err := server.CleanupStaleProductGenerationTasks(); err != nil {
		log.Printf("stale product task cleanup failed: %v", err)
	} else if cleaned > 0 {
		log.Printf("stale product tasks cleaned: %d", cleaned)
	}
	if report, err := server.RecoverStaleRechargeTasks(); err != nil {
		log.Printf("stale recharge task recovery failed: %v", err)
	} else if report.QueuedExpired > 0 || report.RunningExpired > 0 {
		log.Printf("stale recharge tasks recovered: queued=%d running=%d", report.QueuedExpired, report.RunningExpired)
	}

	maintenanceContext, stopMaintenance := context.WithCancel(context.Background())
	defer stopMaintenance()
	go runMaintenanceLoop(maintenanceContext, server)

	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.NewRouter(server), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Printf("api listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("api server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		log.Printf("api shutdown: %v", err)
	}
}

func runMaintenanceLoop(ctx context.Context, server *httpapi.Server) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			taskReport, taskErr := server.RecoverStaleRechargeTasks()
			if taskErr != nil {
				log.Printf("stale recharge task recovery failed: %v", taskErr)
			} else if taskReport.QueuedExpired > 0 || taskReport.RunningExpired > 0 {
				log.Printf("stale recharge tasks recovered: queued=%d running=%d", taskReport.QueuedExpired, taskReport.RunningExpired)
			}
			report, err := server.ReleaseStaleAssetLocks()
			if err != nil {
				log.Printf("stale asset lock cleanup failed: %v", err)
				continue
			}
			if report.PhoneReleased > 0 || report.CardReleased > 0 || report.PoolReleased > 0 || report.ProxyReleased > 0 {
				log.Printf("stale asset locks released: phone=%d card=%d pool_emails=%d proxies=%d", report.PhoneReleased, report.CardReleased, report.PoolReleased, report.ProxyReleased)
			}
		}
	}
}
