package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"chatgptbroker/internal/broker"
	"chatgptbroker/internal/config"
	"chatgptbroker/internal/spacetime"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		os.Exit(1)
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
		os.Exit(1)
	}
	if err := queue.Register(ctx); err != nil {
		recordEvent(context.Background(), queue, "broker", "error", "spacetime_register_failed", "Broker could not register with SpacetimeDB", map[string]string{"error": err.Error()})
		os.Exit(1)
	}
	server := &http.Server{
		Addr:    net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port)),
		Handler: broker.NewServer(queue, cfg.DefaultProjectName, cfg.JobRetention).Handler(),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	recordEvent(context.Background(), queue, "broker", "info", "broker_listening", "Broker listening", map[string]string{"addr": server.Addr})
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		recordEvent(context.Background(), queue, "broker", "error", "http_listen_failed", "Broker HTTP server failed", map[string]string{"error": err.Error()})
		os.Exit(1)
	}
	recordEvent(context.Background(), queue, "broker", "info", "broker_stopped", "Broker stopped", nil)
}

func serviceRoles(configured []string, fallback ...string) []string {
	if len(configured) > 0 {
		return configured
	}
	return fallback
}

type eventRecorder interface {
	RecordEvent(ctx context.Context, input spacetime.EventInput) error
}

func recordEvent(ctx context.Context, recorder eventRecorder, component, level, kind, publicText string, details map[string]string) {
	if recorder == nil {
		return
	}
	detailJSON := "{}"
	if len(details) > 0 {
		body, err := json.Marshal(details)
		if err == nil {
			detailJSON = string(body)
		}
	}
	eventCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = recorder.RecordEvent(eventCtx, spacetime.EventInput{
		Component:       component,
		Level:           level,
		Kind:            kind,
		PublicText:      publicText,
		SafeDetailsJSON: detailJSON,
	})
}
