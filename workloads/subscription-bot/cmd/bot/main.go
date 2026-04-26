package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"subscriptionbot/internal/bot"
	"subscriptionbot/internal/config"
	"subscriptionbot/internal/payments"
	"subscriptionbot/internal/service"
	"subscriptionbot/internal/store"
	"subscriptionbot/internal/telegram"
	appversion "subscriptionbot/internal/version"
	"subscriptionbot/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.WebEnabled {
		if err := ensureSessionSecret(cfg.SessionSecretFile); err != nil {
			log.Fatalf("session secret: %v", err)
		}
	}

	lockPath := resolveSingleInstanceLockPath(cfg)
	lockFile, err := acquireSingleInstanceLock(lockPath)
	if err != nil {
		log.Fatalf("single instance lock: %v", err)
	}
	if lockFile != nil {
		defer releaseSingleInstanceLock(lockFile)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("load timezone: %v", err)
	}

	st, err := store.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	provider, err := newProvider(cfg, st)
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	app := service.New(st.DB(), cfg, provider, loc)
	if err := app.SeedCatalog(ctx); err != nil {
		log.Fatalf("seed catalog: %v", err)
	}

	tg := telegram.NewClient(cfg.BotToken, time.Duration(cfg.HTTPTimeoutSec)*time.Second)

	var webServer *web.Server
	if cfg.WebEnabled {
		webServer, err = web.NewServer(cfg, app, loc)
		if err != nil {
			log.Fatalf("web server: %v", err)
		}
	}

	botSvc := bot.NewService(
		tg,
		app,
		cfg,
	)
	if webServer != nil {
		webServer.SetNotifier(botSvc)
	}

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	type componentResult struct {
		name string
		err  error
	}
	results := make(chan componentResult, 4)
	components := 0
	start := func(name string, fn func(context.Context) error) {
		components++
		go func() {
			results <- componentResult{name: name, err: fn(runCtx)}
		}()
	}

	start("telegram-bot", botSvc.Start)
	start("scheduler", func(ctx context.Context) error {
		return schedulerLoop(ctx, app, botSvc)
	})
	if webServer != nil {
		start("web", webServer.Run)
	}

	log.Printf("subscription bot started (%s)", appversion.Display())

	var firstErr error
	for components > 0 {
		result := <-results
		components--
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", result.name, result.err)
			runCancel()
		}
	}
	if firstErr != nil {
		log.Fatalf("subscription bot stopped with error: %v", firstErr)
	}
}

func newProvider(cfg config.Config, st *store.SQLiteStore) (payments.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.PaymentProvider)) {
	case "", "sandbox":
		return payments.NewSandboxProvider(st.DB()), nil
	case "nowpayments":
		return payments.NewNowPaymentsProvider(
			st.DB(),
			cfg.NowPaymentsAPIBaseURL,
			cfg.NowPaymentsAPIKey,
			cfg.NowPaymentsIPNSecret,
			cfg.RequiredConfirmations,
			time.Duration(cfg.HTTPTimeoutSec)*time.Second,
		), nil
	default:
		return nil, fmt.Errorf("unsupported payment provider: %s", cfg.PaymentProvider)
	}
}

func schedulerLoop(ctx context.Context, app *service.App, botSvc *bot.Service) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	run := func(now time.Time) error {
		notifications, err := app.ProcessCycle(ctx, now)
		if err != nil {
			return err
		}
		if len(notifications) == 0 {
			return nil
		}
		return botSvc.DispatchNotifications(ctx, notifications)
	}

	if err := run(time.Now()); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := run(now); err != nil {
				return err
			}
		}
	}
}

func resolveSingleInstanceLockPath(cfg config.Config) string {
	if strings.TrimSpace(cfg.SingleInstanceLockPath) != "" {
		return cfg.SingleInstanceLockPath
	}
	return cfg.DBPath + ".lock"
}

func acquireSingleInstanceLock(lockPath string) (*os.File, error) {
	if strings.TrimSpace(lockPath) == "" {
		return nil, nil
	}
	dir := filepath.Dir(lockPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create lock dir: %w", err)
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("another subscription-bot instance is already running")
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return f, nil
}

func releaseSingleInstanceLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func ensureSessionSecret(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("session secret path is empty")
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session secret dir: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate session secret: %w", err)
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%x", buf)), 0o600)
}
