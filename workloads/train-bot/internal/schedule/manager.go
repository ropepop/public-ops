package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/store"
)

var ErrUnavailable = errors.New("schedule unavailable")

type AccessContext struct {
	RequestedServiceDate string `json:"requestedServiceDate"`
	EffectiveServiceDate string `json:"effectiveServiceDate,omitempty"`
	LoadedServiceDate    string `json:"loadedServiceDate,omitempty"`
	FallbackActive       bool   `json:"fallbackActive"`
	CutoffHour           int    `json:"cutoffHour"`
	Available            bool   `json:"available"`
	SameDayFresh         bool   `json:"sameDayFresh"`
}

type Manager struct {
	store            store.Store
	dir              string
	loc              *time.Location
	scraperDailyHour int

	mu                sync.RWMutex
	available         bool
	lastErr           error
	lastLoaded        time.Time
	loadedServiceDate string
}

func NewManager(st store.Store, dir string, loc *time.Location, scraperDailyHour int) *Manager {
	if scraperDailyHour < 0 || scraperDailyHour > 23 {
		scraperDailyHour = 3
	}
	return &Manager{
		store:            st,
		dir:              dir,
		loc:              loc,
		scraperDailyHour: scraperDailyHour,
	}
}

func (m *Manager) LoadForAccess(ctx context.Context, now time.Time) error {
	localNow := now.In(m.loc)
	requestedServiceDate := localNow.Format("2006-01-02")
	loadErr := m.LoadServiceDate(ctx, requestedServiceDate)
	if loadErr == nil {
		return nil
	}
	if !localNow.Before(m.dailyCutoff(localNow)) {
		return loadErr
	}
	fallbackServiceDate := localNow.AddDate(0, 0, -1).Format("2006-01-02")
	if fallbackServiceDate == requestedServiceDate {
		return loadErr
	}
	if fallbackErr := m.LoadServiceDate(ctx, fallbackServiceDate); fallbackErr == nil {
		m.mu.Lock()
		if m.available && m.loadedServiceDate == fallbackServiceDate {
			m.lastErr = loadErr
		}
		m.mu.Unlock()
		return nil
	}
	return loadErr
}

func (m *Manager) LoadToday(ctx context.Context, now time.Time) error {
	return m.LoadServiceDate(ctx, now.In(m.loc).Format("2006-01-02"))
}

func (m *Manager) LoadServiceDate(ctx context.Context, serviceDate string) error {
	if existing, err := m.store.ListTrainInstancesByDate(ctx, serviceDate); err == nil && len(existing) > 0 {
		m.mu.Lock()
		m.available = true
		m.lastErr = nil
		m.lastLoaded = time.Now().UTC()
		m.loadedServiceDate = serviceDate
		m.mu.Unlock()
		return nil
	}

	path := filepath.Join(m.dir, serviceDate+".json")
	sourceVersion, trains, stopsByTrain, err := LoadSnapshotFile(path, serviceDate)
	if err != nil {
		if m.useExistingServiceDate(ctx, serviceDate, err) {
			return nil
		}
		m.mu.Lock()
		m.available = false
		m.lastErr = err
		m.loadedServiceDate = ""
		m.mu.Unlock()
		return fmt.Errorf("load schedule %s failed: %w", serviceDate, err)
	}
	if err := m.store.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains); err != nil {
		if m.useExistingServiceDate(ctx, serviceDate, err) {
			return nil
		}
		m.mu.Lock()
		m.available = false
		m.lastErr = err
		m.loadedServiceDate = ""
		m.mu.Unlock()
		return fmt.Errorf("persist schedule: %w", err)
	}
	if err := m.store.UpsertTrainStops(ctx, serviceDate, stopsByTrain); err != nil {
		if m.useExistingServiceDate(ctx, serviceDate, err) {
			return nil
		}
		m.mu.Lock()
		m.available = false
		m.lastErr = err
		m.loadedServiceDate = ""
		m.mu.Unlock()
		return fmt.Errorf("persist stops: %w", err)
	}
	m.mu.Lock()
	m.available = true
	m.lastErr = nil
	m.lastLoaded = time.Now().UTC()
	m.loadedServiceDate = serviceDate
	m.mu.Unlock()
	return nil
}

func (m *Manager) useExistingServiceDate(ctx context.Context, serviceDate string, cause error) bool {
	existing, listErr := m.store.ListTrainInstancesByDate(ctx, serviceDate)
	if listErr != nil || len(existing) == 0 {
		return false
	}
	m.mu.Lock()
	m.available = true
	m.lastErr = cause
	m.lastLoaded = time.Now().UTC()
	m.loadedServiceDate = serviceDate
	m.mu.Unlock()
	return true
}

func (m *Manager) DeleteSnapshot(serviceDate string) error {
	serviceDate = strings.TrimSpace(serviceDate)
	if serviceDate == "" {
		return nil
	}
	err := os.Remove(filepath.Join(m.dir, serviceDate+".json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) Availability() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available, m.lastErr
}

func (m *Manager) LastLoaded() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastLoaded
}

func (m *Manager) LoadedServiceDate() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadedServiceDate
}

func (m *Manager) AccessContext(now time.Time) AccessContext {
	localNow := now.In(m.loc)
	requestedServiceDate := localNow.Format("2006-01-02")
	cutoff := m.dailyCutoff(localNow)

	m.mu.RLock()
	available := m.available
	loadedServiceDate := m.loadedServiceDate
	m.mu.RUnlock()

	access := AccessContext{
		RequestedServiceDate: requestedServiceDate,
		LoadedServiceDate:    loadedServiceDate,
		CutoffHour:           m.scraperDailyHour,
	}
	if !available {
		return access
	}
	if loadedServiceDate == requestedServiceDate {
		access.Available = true
		access.SameDayFresh = true
		access.EffectiveServiceDate = requestedServiceDate
		return access
	}
	fallbackServiceDate := localNow.AddDate(0, 0, -1).Format("2006-01-02")
	if localNow.Before(cutoff) && loadedServiceDate == fallbackServiceDate {
		access.Available = true
		access.FallbackActive = true
		access.EffectiveServiceDate = fallbackServiceDate
	}
	return access
}

func (m *Manager) IsFreshFor(now time.Time) bool {
	return m.AccessContext(now).SameDayFresh
}

func (m *Manager) ListByWindow(ctx context.Context, now time.Time, windowID string) ([]domain.TrainInstance, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	localNow := now.In(m.loc)
	switch windowID {
	case "now":
		start := localNow.Add(-15 * time.Minute)
		end := localNow.Add(15 * time.Minute)
		return m.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	case "next_hour":
		start := localNow
		end := localNow.Add(1 * time.Hour)
		return m.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	case "today":
		start := localNow.Add(-30 * time.Minute)
		end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 59, 0, m.loc)
		return m.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	default:
		return nil, fmt.Errorf("unsupported window %s", windowID)
	}
}

func (m *Manager) ListAllTrains(ctx context.Context, now time.Time) ([]domain.TrainInstance, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	return m.store.ListTrainInstancesByDate(ctx, serviceDate)
}

func (m *Manager) ListAllStops(ctx context.Context, now time.Time) ([]domain.TrainStop, error) {
	trains, err := m.ListAllTrains(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrainStop, 0)
	for _, train := range trains {
		stops, stopErr := m.store.ListTrainStops(ctx, train.ID)
		if stopErr != nil {
			return nil, stopErr
		}
		out = append(out, stops...)
	}
	return out, nil
}

func (m *Manager) GetTrain(ctx context.Context, id string) (*domain.TrainInstance, error) {
	return m.store.GetTrainInstanceByID(ctx, id)
}

func (m *Manager) GetStation(ctx context.Context, now time.Time, stationID string) (*domain.Station, error) {
	if _, err := m.requireServiceDate(now); err != nil {
		return nil, err
	}
	return m.store.GetStationByID(ctx, stationID)
}

func (m *Manager) ListStations(ctx context.Context, now time.Time) ([]domain.Station, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	return m.store.ListStationsByDate(ctx, serviceDate)
}

func (m *Manager) ListByStationRange(ctx context.Context, now time.Time, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	if end.Before(start) {
		return []domain.StationWindowTrain{}, nil
	}
	return m.store.ListStationWindowTrains(ctx, serviceDate, stationID, start, end)
}

func (m *Manager) ListByStationWindow(ctx context.Context, now time.Time, stationID string, d time.Duration) ([]domain.StationWindowTrain, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	if d <= 0 {
		d = 3 * time.Hour
	}
	localNow := now.In(m.loc)
	return m.store.ListStationWindowTrains(ctx, serviceDate, stationID, localNow, localNow.Add(d))
}

func (m *Manager) ListReachableDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	return m.store.ListReachableDestinations(ctx, serviceDate, fromStationID)
}

func (m *Manager) ListTerminalDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	return m.store.ListTerminalDestinations(ctx, serviceDate, fromStationID)
}

func (m *Manager) ListRouteWindowTrains(ctx context.Context, now time.Time, fromStationID string, toStationID string, d time.Duration) ([]domain.RouteWindowTrain, error) {
	serviceDate, err := m.requireServiceDate(now)
	if err != nil {
		return nil, err
	}
	if d <= 0 {
		d = 18 * time.Hour
	}
	localNow := now.In(m.loc)
	start := localNow.Add(-30 * time.Minute)
	return m.store.ListRouteWindowTrains(ctx, serviceDate, fromStationID, toStationID, start, localNow.Add(d))
}

func (m *Manager) dailyCutoff(localNow time.Time) time.Time {
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), m.scraperDailyHour, 0, 0, 0, m.loc)
}

func (m *Manager) requireServiceDate(now time.Time) (string, error) {
	access := m.AccessContext(now)
	if !access.Available || strings.TrimSpace(access.EffectiveServiceDate) == "" {
		return "", ErrUnavailable
	}
	return access.EffectiveServiceDate, nil
}
