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

	"qbittorrenthousekeeper/internal/config"
	"qbittorrenthousekeeper/internal/housekeeper"
	"qbittorrenthousekeeper/internal/qbit"
	"qbittorrenthousekeeper/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("housekeeper stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	password, err := cfg.ReadPassword()
	if err != nil {
		return err
	}
	client, err := qbit.NewClient(cfg.QBitURL, cfg.Username, password, cfg.RequestTimeout)
	if err != nil {
		return err
	}
	status := housekeeper.NewStatusStore()
	policy := housekeeper.DefaultPolicy(cfg.SoftCapBytes, cfg.MinAge, cfg.MinRatio)
	policy.DownloadRoot = cfg.DownloadPath
	engine, err := housekeeper.New(
		client,
		storage.Filesystem{Path: cfg.DownloadPath},
		policy,
		time.Now,
		status,
	)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.HealthAddr,
		Handler:           status.Handler(time.Now, cfg.HealthMaxAge),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("health listener started", "address", cfg.HealthAddr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reconcile := func() {
		pollCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout*8)
		defer cancel()
		if err := engine.Reconcile(pollCtx); err != nil {
			logger.Error("reconciliation failed closed", "error", err)
			return
		}
		snapshot := status.Snapshot()
		logger.Info("reconciliation complete",
			"torrents", snapshot.TorrentCount,
			"admitted", snapshot.Admitted,
			"waiting", snapshot.Waiting,
			"rejected", snapshot.Rejected,
			"deletions_requested", snapshot.DeletionsRequested,
			"used_bytes", snapshot.UsedBytes,
			"reserved_bytes", snapshot.ReservedBytes,
			"committed_bytes", snapshot.CommittedBytes,
		)
	}

	reconcile()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		case err := <-serverErrors:
			return err
		case <-ticker.C:
			reconcile()
		}
	}
}
