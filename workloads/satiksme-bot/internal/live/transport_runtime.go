package live

import (
	"context"
	"log"
	"net/http"
	"time"

	"satiksmebot/internal/model"
)

type transportCatalogReader interface {
	Current() *model.Catalog
}

type transportStateStore interface {
	CountActiveLiveViewers(ctx context.Context, activeSince time.Time) (int, error)
	UpsertLiveTransportState(ctx context.Context, state model.LiveTransportState) error
	CleanupLiveViewers(ctx context.Context, cutoff time.Time) error
}

type TransportRuntimeSettings struct {
	SourceURL          string
	HTTPClient         *http.Client
	Catalog            transportCatalogReader
	StateStore         transportStateStore
	Publisher          *SnapshotPublisher
	ViewerWindow       time.Duration
	ViewerGracePeriod  time.Duration
	PollInterval       time.Duration
	MaxPollInterval    time.Duration
	IdleCheckInterval  time.Duration
	CleanupInterval    time.Duration
	CleanupRetention   time.Duration
	FailureBackoffStep time.Duration
	FailureBackoffMax  time.Duration
}

func RunTransportSnapshotLoop(ctx context.Context, settings TransportRuntimeSettings) error {
	if settings.Catalog == nil || settings.StateStore == nil || settings.Publisher == nil || !settings.Publisher.Enabled() {
		return nil
	}
	if settings.ViewerWindow <= 0 {
		settings.ViewerWindow = 45 * time.Second
	}
	if settings.ViewerGracePeriod <= 0 {
		settings.ViewerGracePeriod = 60 * time.Second
	}
	if settings.PollInterval <= 0 {
		settings.PollInterval = 5 * time.Second
	}
	if settings.MaxPollInterval <= 0 {
		settings.MaxPollInterval = settings.PollInterval
	}
	if settings.MaxPollInterval < settings.PollInterval {
		settings.MaxPollInterval = settings.PollInterval
	}
	if settings.IdleCheckInterval <= 0 {
		settings.IdleCheckInterval = settings.PollInterval
	}
	if settings.FailureBackoffStep <= 0 {
		settings.FailureBackoffStep = 10 * time.Second
	}
	if settings.FailureBackoffMax <= 0 {
		settings.FailureBackoffMax = 20 * time.Second
	}

	var (
		graceUntil          time.Time
		lastSuccessAt       time.Time
		lastPublishedAt     time.Time
		currentVersion      string
		currentPath         string
		currentHash         string
		currentVehicleCount int
		consecutiveFailures int
		nextPollDelay       = settings.PollInterval
		lastStoredState     model.LiveTransportState
		storedStateLoaded   bool
		hadActiveViewers    bool
	)

	if active, err := settings.Publisher.ActiveState(); err != nil {
		log.Printf("read live transport active snapshot: %v", err)
	} else if active != nil {
		currentVersion = active.Version
		currentPath = active.Path
		currentHash = active.Hash
		lastPublishedAt = active.PublishedAt
		currentVehicleCount = active.VehicleCount
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	resetTimer := func(delay time.Duration) {
		if delay <= 0 {
			delay = settings.PollInterval
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	persistState := func(state model.LiveTransportState) error {
		if storedStateLoaded && sameLiveTransportStateMaterial(lastStoredState, state) {
			return nil
		}
		if err := settings.StateStore.UpsertLiveTransportState(ctx, state); err != nil {
			return err
		}
		lastStoredState = state
		storedStateLoaded = true
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		now := time.Now().UTC()
		activeViewers, err := settings.StateStore.CountActiveLiveViewers(ctx, now.Add(-settings.ViewerWindow))
		if err != nil {
			log.Printf("count live transport viewers: %v", err)
			resetTimer(backoffDelay(consecutiveFailures+1, settings.FailureBackoffStep, settings.FailureBackoffMax))
			continue
		}
		if activeViewers > 0 {
			if !hadActiveViewers {
				nextPollDelay = settings.PollInterval
			}
			graceUntil = now.Add(settings.ViewerGracePeriod)
		}
		hadActiveViewers = activeViewers > 0
		shouldPoll := activeViewers > 0 || (!graceUntil.IsZero() && now.Before(graceUntil))
		if !shouldPoll {
			resetTimer(settings.IdleCheckInterval)
			continue
		}

		state := model.LiveTransportState{
			Feed:                "transport",
			Version:             currentVersion,
			Path:                currentPath,
			Hash:                currentHash,
			PublishedAt:         lastPublishedAt,
			LastSuccessAt:       lastSuccessAt,
			LastAttemptAt:       now,
			Status:              "idle",
			ConsecutiveFailures: consecutiveFailures,
			VehicleCount:        currentVehicleCount,
		}

		catalog := settings.Catalog.Current()
		if catalog == nil {
			consecutiveFailures += 1
			state.Status = "error"
			state.ConsecutiveFailures = consecutiveFailures
			if err := persistState(state); err != nil {
				log.Printf("update live transport error state: %v", err)
			}
			resetTimer(backoffDelay(consecutiveFailures, settings.FailureBackoffStep, settings.FailureBackoffMax))
			continue
		}

		vehicles, err := FetchVehicles(ctx, settings.HTTPClient, settings.SourceURL, catalog, now)
		if err != nil {
			consecutiveFailures += 1
			state.Status = "error"
			state.ConsecutiveFailures = consecutiveFailures
			if err := persistState(state); err != nil {
				log.Printf("update live transport error state: %v", err)
			}
			log.Printf("fetch live transport snapshot: %v", err)
			resetTimer(backoffDelay(consecutiveFailures, settings.FailureBackoffStep, settings.FailureBackoffMax))
			continue
		}

		result, err := settings.Publisher.Publish(now, vehicles)
		if err != nil {
			consecutiveFailures += 1
			state.Status = "error"
			state.ConsecutiveFailures = consecutiveFailures
			if err := persistState(state); err != nil {
				log.Printf("update live transport error state: %v", err)
			}
			log.Printf("publish live transport snapshot: %v", err)
			resetTimer(backoffDelay(consecutiveFailures, settings.FailureBackoffStep, settings.FailureBackoffMax))
			continue
		}

		recovered := consecutiveFailures > 0
		consecutiveFailures = 0
		lastSuccessAt = now
		if result != nil {
			currentVersion = result.Version
			currentPath = result.Path
			currentHash = result.Hash
			currentVehicleCount = result.VehicleCount
			if result.PublishedAt.After(lastPublishedAt) || lastPublishedAt.IsZero() {
				lastPublishedAt = result.PublishedAt
			}
		}
		state = model.LiveTransportState{
			Feed:                "transport",
			Version:             currentVersion,
			Path:                currentPath,
			Hash:                currentHash,
			PublishedAt:         lastPublishedAt,
			LastSuccessAt:       lastSuccessAt,
			LastAttemptAt:       now,
			Status:              "live",
			ConsecutiveFailures: 0,
			VehicleCount:        currentVehicleCount,
		}
		if err := persistState(state); err != nil {
			log.Printf("update live transport state: %v", err)
			resetTimer(backoffDelay(1, settings.FailureBackoffStep, settings.FailureBackoffMax))
			continue
		}
		nextPollDelay = nextTransportPollDelay(nextPollDelay, settings.PollInterval, settings.MaxPollInterval, recovered || (result != nil && result.Changed))
		resetTimer(nextPollDelay)
	}
}

func RunLiveViewerCleanupLoop(ctx context.Context, settings TransportRuntimeSettings) error {
	if settings.StateStore == nil {
		return nil
	}
	if settings.CleanupInterval <= 0 {
		settings.CleanupInterval = 5 * time.Minute
	}
	if settings.CleanupRetention <= 0 {
		settings.CleanupRetention = time.Hour
	}
	ticker := time.NewTicker(settings.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-settings.CleanupRetention)
			if err := settings.StateStore.CleanupLiveViewers(ctx, cutoff); err != nil {
				log.Printf("cleanup live transport viewers: %v", err)
			}
		}
	}
}

func backoffDelay(consecutiveFailures int, step, max time.Duration) time.Duration {
	if consecutiveFailures <= 0 {
		return 0
	}
	delay := time.Duration(consecutiveFailures) * step
	if delay > max {
		return max
	}
	return delay
}

func nextTransportPollDelay(current, base, max time.Duration, changed bool) time.Duration {
	if base <= 0 {
		base = 5 * time.Second
	}
	if max < base {
		max = base
	}
	if changed {
		return base
	}
	if current < base {
		current = base
	}
	next := current * 2
	if next < base {
		next = base
	}
	if next > max {
		next = max
	}
	return next
}

func sameLiveTransportStateMaterial(left, right model.LiveTransportState) bool {
	return left.Feed == right.Feed &&
		left.Version == right.Version &&
		left.Path == right.Path &&
		left.Hash == right.Hash &&
		left.PublishedAt.Equal(right.PublishedAt) &&
		left.Status == right.Status &&
		left.ConsecutiveFailures == right.ConsecutiveFailures &&
		left.VehicleCount == right.VehicleCount
}
