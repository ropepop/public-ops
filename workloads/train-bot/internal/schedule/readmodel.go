package schedule

import (
	"context"
	"fmt"
	"sync"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/store"
)

const projectionProbeTTL = 15 * time.Second

type ReadModel interface {
	Availability() (bool, error)
	LoadedServiceDate() string
	AccessContext(now time.Time) AccessContext
	IsFreshFor(now time.Time) bool
	ListByWindow(ctx context.Context, now time.Time, windowID string) ([]domain.TrainInstance, error)
	ListAllTrains(ctx context.Context, now time.Time) ([]domain.TrainInstance, error)
	ListAllStops(ctx context.Context, now time.Time) ([]domain.TrainStop, error)
	GetTrain(ctx context.Context, id string) (*domain.TrainInstance, error)
	GetStation(ctx context.Context, now time.Time, stationID string) (*domain.Station, error)
	ListStations(ctx context.Context, now time.Time) ([]domain.Station, error)
	ListByStationRange(ctx context.Context, now time.Time, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error)
	ListByStationWindow(ctx context.Context, now time.Time, stationID string, d time.Duration) ([]domain.StationWindowTrain, error)
	ListReachableDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error)
	ListTerminalDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error)
	ListRouteWindowTrains(ctx context.Context, now time.Time, fromStationID string, toStationID string, d time.Duration) ([]domain.RouteWindowTrain, error)
}

type ProjectionReader struct {
	store            store.Store
	loc              *time.Location
	scraperDailyHour int

	mu             sync.RWMutex
	cachedAt       time.Time
	cachedDate     string
	cachedAccess   AccessContext
	cachedProbeErr error
}

func NewProjectionReader(st store.Store, loc *time.Location, scraperDailyHour int) *ProjectionReader {
	if scraperDailyHour < 0 || scraperDailyHour > 23 {
		scraperDailyHour = 3
	}
	if loc == nil {
		loc = time.UTC
	}
	return &ProjectionReader{
		store:            st,
		loc:              loc,
		scraperDailyHour: scraperDailyHour,
	}
}

func (r *ProjectionReader) Availability() (bool, error) {
	access, err := r.AccessContextWithContext(context.Background(), time.Now())
	return access.Available, err
}

func (r *ProjectionReader) LoadedServiceDate() string {
	return r.AccessContext(time.Now()).LoadedServiceDate
}

func (r *ProjectionReader) AccessContext(now time.Time) AccessContext {
	access, _ := r.AccessContextWithContext(context.Background(), now)
	return access
}

func (r *ProjectionReader) AccessContextWithContext(ctx context.Context, now time.Time) (AccessContext, error) {
	return r.probeAccessContext(ctx, now)
}

func (r *ProjectionReader) IsFreshFor(now time.Time) bool {
	return r.AccessContext(now).SameDayFresh
}

func (r *ProjectionReader) ListByWindow(ctx context.Context, now time.Time, windowID string) ([]domain.TrainInstance, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	localNow := now.In(r.loc)
	switch windowID {
	case "now":
		start := localNow.Add(-15 * time.Minute)
		end := localNow.Add(15 * time.Minute)
		return r.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	case "next_hour":
		start := localNow
		end := localNow.Add(1 * time.Hour)
		return r.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	case "today":
		start := localNow.Add(-30 * time.Minute)
		end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 59, 0, r.loc)
		return r.store.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
	default:
		return nil, fmt.Errorf("unsupported window %s", windowID)
	}
}

func (r *ProjectionReader) ListAllTrains(ctx context.Context, now time.Time) ([]domain.TrainInstance, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	return r.store.ListTrainInstancesByDate(ctx, serviceDate)
}

func (r *ProjectionReader) ListAllStops(ctx context.Context, now time.Time) ([]domain.TrainStop, error) {
	trains, err := r.ListAllTrains(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrainStop, 0)
	for _, train := range trains {
		stops, stopErr := r.store.ListTrainStops(ctx, train.ID)
		if stopErr != nil {
			return nil, stopErr
		}
		out = append(out, stops...)
	}
	return out, nil
}

func (r *ProjectionReader) GetTrain(ctx context.Context, id string) (*domain.TrainInstance, error) {
	return r.store.GetTrainInstanceByID(ctx, id)
}

func (r *ProjectionReader) GetStation(ctx context.Context, now time.Time, stationID string) (*domain.Station, error) {
	if _, err := r.requireServiceDate(ctx, now); err != nil {
		return nil, err
	}
	return r.store.GetStationByID(ctx, stationID)
}

func (r *ProjectionReader) ListStations(ctx context.Context, now time.Time) ([]domain.Station, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	return r.store.ListStationsByDate(ctx, serviceDate)
}

func (r *ProjectionReader) ListByStationRange(ctx context.Context, now time.Time, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	if end.Before(start) {
		return []domain.StationWindowTrain{}, nil
	}
	return r.store.ListStationWindowTrains(ctx, serviceDate, stationID, start, end)
}

func (r *ProjectionReader) ListByStationWindow(ctx context.Context, now time.Time, stationID string, d time.Duration) ([]domain.StationWindowTrain, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	if d <= 0 {
		d = 3 * time.Hour
	}
	localNow := now.In(r.loc)
	return r.store.ListStationWindowTrains(ctx, serviceDate, stationID, localNow, localNow.Add(d))
}

func (r *ProjectionReader) ListReachableDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	return r.store.ListReachableDestinations(ctx, serviceDate, fromStationID)
}

func (r *ProjectionReader) ListTerminalDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	return r.store.ListTerminalDestinations(ctx, serviceDate, fromStationID)
}

func (r *ProjectionReader) ListRouteWindowTrains(ctx context.Context, now time.Time, fromStationID string, toStationID string, d time.Duration) ([]domain.RouteWindowTrain, error) {
	serviceDate, err := r.requireServiceDate(ctx, now)
	if err != nil {
		return nil, err
	}
	if d <= 0 {
		d = 18 * time.Hour
	}
	localNow := now.In(r.loc)
	start := localNow.Add(-30 * time.Minute)
	return r.store.ListRouteWindowTrains(ctx, serviceDate, fromStationID, toStationID, start, localNow.Add(d))
}

func (r *ProjectionReader) probeAccessContext(ctx context.Context, now time.Time) (AccessContext, error) {
	localNow := now.In(r.loc)
	requestedServiceDate := localNow.Format("2006-01-02")

	r.mu.RLock()
	if requestedServiceDate == r.cachedDate && !r.cachedAt.IsZero() && time.Since(r.cachedAt) < projectionProbeTTL {
		access := r.cachedAccess
		err := r.cachedProbeErr
		r.mu.RUnlock()
		return access, err
	}
	r.mu.RUnlock()

	access := AccessContext{
		RequestedServiceDate: requestedServiceDate,
		CutoffHour:           r.scraperDailyHour,
	}

	todayCount, todayErr := r.trainCountForDate(ctx, requestedServiceDate)
	if todayErr == nil && todayCount > 0 {
		access.Available = true
		access.SameDayFresh = true
		access.EffectiveServiceDate = requestedServiceDate
		access.LoadedServiceDate = requestedServiceDate
		r.storeProbeResult(requestedServiceDate, access, nil)
		return access, nil
	}

	var probeErr error
	if todayErr != nil {
		probeErr = todayErr
	}

	if localNow.Before(r.dailyCutoff(localNow)) {
		fallbackServiceDate := localNow.AddDate(0, 0, -1).Format("2006-01-02")
		if fallbackServiceDate != requestedServiceDate {
			fallbackCount, fallbackErr := r.trainCountForDate(ctx, fallbackServiceDate)
			if fallbackErr == nil && fallbackCount > 0 {
				access.Available = true
				access.FallbackActive = true
				access.EffectiveServiceDate = fallbackServiceDate
				access.LoadedServiceDate = fallbackServiceDate
				r.storeProbeResult(requestedServiceDate, access, probeErr)
				return access, probeErr
			}
			if probeErr == nil {
				probeErr = fallbackErr
			}
		}
	}

	r.storeProbeResult(requestedServiceDate, access, probeErr)
	return access, probeErr
}

func (r *ProjectionReader) trainCountForDate(ctx context.Context, serviceDate string) (int, error) {
	items, err := r.store.ListTrainInstancesByDate(ctx, serviceDate)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func (r *ProjectionReader) requireServiceDate(ctx context.Context, now time.Time) (string, error) {
	access, err := r.probeAccessContext(ctx, now)
	if !access.Available || access.EffectiveServiceDate == "" {
		if err != nil {
			return "", err
		}
		return "", ErrUnavailable
	}
	return access.EffectiveServiceDate, nil
}

func (r *ProjectionReader) dailyCutoff(localNow time.Time) time.Time {
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), r.scraperDailyHour, 0, 0, 0, r.loc)
}

func (r *ProjectionReader) storeProbeResult(serviceDate string, access AccessContext, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !access.Available {
		r.cachedAt = time.Time{}
		r.cachedDate = ""
		r.cachedAccess = AccessContext{}
		r.cachedProbeErr = err
		return
	}
	r.cachedAt = time.Now()
	r.cachedDate = serviceDate
	r.cachedAccess = access
	r.cachedProbeErr = err
}
