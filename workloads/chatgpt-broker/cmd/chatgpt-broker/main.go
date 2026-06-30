package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"chatgptbroker/internal/broker"
	"chatgptbroker/internal/config"
	"chatgptbroker/internal/ocr"
	"chatgptbroker/internal/spacetime"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	queue, err := spacetime.New(spacetime.Config{
		Host:        cfg.SpacetimeHost,
		Database:    cfg.SpacetimeDatabase,
		BearerToken: cfg.SpacetimeToken,
		KeyFile:     cfg.SpacetimeKeyFile,
		Issuer:      cfg.SpacetimeIssuer,
		Audience:    cfg.SpacetimeAudience,
		Subject:     cfg.SpacetimeSubject,
		Roles:       serviceRoles(cfg.SpacetimeRoles, "chatgptbroker_broker", "chatgptbroker_bot"),
		TokenTTL:    cfg.SpacetimeTokenTTL,
		HTTPTimeout: cfg.HTTPTimeout,
		ServiceName: "chatgpt-broker",
		Role:        "broker",
	})
	if err != nil {
		log.Fatalf("spacetime: %v", err)
	}
	if err := queue.Register(ctx); err != nil {
		log.Fatalf("spacetime register: %v", err)
	}
	runner := broker.NewRunner(queue, broker.RunnerConfig{
		Enabled:      cfg.OCREnabled,
		PollInterval: cfg.OCRPollInterval,
		OCR:          ocr.Extractor{TesseractPath: cfg.TesseractPath},
	})
	server := &http.Server{
		Addr:    net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port)),
		Handler: broker.NewServer(queue, cfg.DefaultProjectName, cfg.JobRetention).Handler(),
	}
	go runner.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("chatgpt broker listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
	fmt.Println("chatgpt broker stopped")
}

func serviceRoles(configured []string, fallback ...string) []string {
	if len(configured) > 0 {
		return configured
	}
	return fallback
}
