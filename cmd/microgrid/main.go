package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"microgrid/internal/application"
	"microgrid/internal/background"
	"microgrid/internal/config"
	"microgrid/internal/httpapi"
	"microgrid/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfgPath := os.Getenv("MICROGRID_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("config load failed", "error", err)
		os.Exit(1)
	}

	repo := store.NewMemory()
	svc := application.NewService(repo, application.WithSettings(cfg.ToDomainSettings()))
	monitor := background.NewMonitor(svc, cfg.MonitorInterval)
	srv := httpapi.New(cfg.HTTPAddr, svc)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := monitor.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("deadline monitor stopped", "error", err)
		}
	}()

	log.Info("starting ejina microgrid black-start coordinator", "addr", cfg.HTTPAddr, "monitor_interval", cfg.MonitorInterval)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
	}
}
