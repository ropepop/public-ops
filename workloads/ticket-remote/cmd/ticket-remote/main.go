package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
	"ticketremote/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := state.NewStore(cfg.State)
	if err != nil {
		log.Fatalf("configure state store: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Bootstrap(ctx, state.BootstrapInput{
		TicketID:        cfg.TicketID,
		DisplayName:     cfg.TicketDisplayName,
		AdminEmail:      cfg.BootstrapAdminEmail,
		PhoneBackendID:  cfg.Phone.BackendID,
		PhoneBaseURL:    cfg.Phone.BaseURL,
		PhoneAttachName: cfg.Phone.AttachName,
		AuthIssuer:      cfg.Access.OIDCIssuer,
		AuthAudience:    cfg.Access.OIDCClientID,
	}); err != nil {
		log.Fatalf("bootstrap state: %v", err)
	}
	if err := configureDevPerfMetrics(ctx, store, cfg); err != nil {
		log.Fatalf("configure dev perf metrics: %v", err)
	}

	relay := phone.NewRelay(cfg.Phone.RelayConfig())
	defer relay.Close()

	server, err := web.NewServer(cfg, store, relay)
	if err != nil {
		log.Fatalf("configure server: %v", err)
	}
	defer server.Close()

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port)),
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("ticket-remote listening on %s", httpServer.Addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}
}

func configureDevPerfMetrics(ctx context.Context, store state.Store, cfg config.Config) error {
	now := time.Now()
	expiresAt := cfg.DevPerfMetrics.ExpiresAt
	if expiresAt.IsZero() && cfg.DevPerfMetrics.Enabled {
		ttl := cfg.DevPerfMetrics.TTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		expiresAt = now.Add(ttl)
	}
	enabled := cfg.DevPerfMetrics.Enabled && expiresAt.After(now)
	if expiresAt.IsZero() {
		expiresAt = now
	}
	return store.SetDevPerfMetrics(ctx, state.DevPerfMetricsConfigInput{
		TicketID:  cfg.TicketID,
		Enabled:   enabled,
		Reason:    "ticket_remote_startup",
		ExpiresAt: expiresAt,
		Now:       now,
	})
}
