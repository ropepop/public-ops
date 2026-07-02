package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"phonebroker/internal/broker"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	b, err := broker.New(cfg)
	if err != nil {
		log.Fatalf("broker: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go b.Run(ctx)

	addr := net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port))
	server := &http.Server{Addr: addr, Handler: b.Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("phone broker listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
}

func loadConfig() (broker.Config, error) {
	port, err := envInt("PHONE_BROKER_PORT", 9398)
	if err != nil {
		return broker.Config{}, err
	}
	ticketGrace, err := envDuration("PHONE_BROKER_TICKET_GRACE", 10*time.Second)
	if err != nil {
		return broker.Config{}, err
	}
	cfg := broker.Config{
		BindAddr:        env("PHONE_BROKER_BIND_ADDR", "0.0.0.0"),
		Port:            port,
		UpstreamBaseURL: strings.TrimRight(env("PHONE_BROKER_UPSTREAM_BASE_URL", "http://ticket_phone_bridge:9388"), "/"),
		TicketGrace:     ticketGrace,
		EventSink: broker.EventSinkConfig{
			URL:   env("PHONE_BROKER_EVENT_SINK_URL", ""),
			Token: env("PHONE_BROKER_EVENT_SINK_TOKEN", ""),
		},
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return broker.Config{}, fmt.Errorf("PHONE_BROKER_PORT out of range: %d", cfg.Port)
	}
	return cfg, nil
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration", key)
	}
	return parsed, nil
}
