package recovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/domain"
)

type staticBundleBaseProvider interface {
	StaticBundleBase(ctx context.Context, now time.Time) (*trainapp.StaticBundleBase, error)
}

type spacetimeScheduleTarget interface {
	ScheduleCounts(ctx context.Context, serviceDate string) (int, int, int, error)
	ReplaceScheduleSnapshot(ctx context.Context, serviceDate string, sourceVersion string, stations []domain.Station, trains []domain.TrainInstance, stops []domain.TrainStop) (int, int, int, error)
}

type SyncResult struct {
	ServiceDate      string
	SourceVersion    string
	Stations         int
	Trains           int
	Stops            int
	ExistingStations int
	ExistingTrains   int
	ExistingStops    int
	Synced           bool
	Reason           string
}

func AnchorTimeForServiceDate(serviceDate string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(serviceDate), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse service date: %w", err)
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 12, 0, 0, 0, loc), nil
}

func SyncServiceDate(ctx context.Context, provider staticBundleBaseProvider, target spacetimeScheduleTarget, loc *time.Location, serviceDate string, dryRun bool) (SyncResult, error) {
	anchor, err := AnchorTimeForServiceDate(serviceDate, loc)
	if err != nil {
		return SyncResult{ServiceDate: strings.TrimSpace(serviceDate)}, err
	}
	return syncAt(ctx, provider, target, anchor, strings.TrimSpace(serviceDate), false, dryRun)
}

func MaybeSelfHeal(ctx context.Context, provider staticBundleBaseProvider, target spacetimeScheduleTarget, now time.Time) (SyncResult, error) {
	return syncAt(ctx, provider, target, now, "", true, false)
}

func syncAt(ctx context.Context, provider staticBundleBaseProvider, target spacetimeScheduleTarget, anchor time.Time, expectedServiceDate string, onlyWhenMissing bool, dryRun bool) (SyncResult, error) {
	base, err := provider.StaticBundleBase(ctx, anchor)
	if err != nil {
		return SyncResult{ServiceDate: strings.TrimSpace(expectedServiceDate)}, err
	}
	if base == nil {
		return SyncResult{ServiceDate: strings.TrimSpace(expectedServiceDate)}, fmt.Errorf("schedule snapshot unavailable")
	}

	result := SyncResult{
		ServiceDate:   strings.TrimSpace(base.ServiceDate),
		SourceVersion: strings.TrimSpace(base.SourceVersion),
		Stations:      len(base.Stations),
		Trains:        len(base.Trains),
		Stops:         len(base.Stops),
	}
	if expected := strings.TrimSpace(expectedServiceDate); expected != "" && result.ServiceDate != expected {
		return result, fmt.Errorf("requested service date %s resolved to %s", expected, result.ServiceDate)
	}
	if result.Trains == 0 {
		return result, fmt.Errorf("local schedule snapshot for %s is empty", result.ServiceDate)
	}

	existingStations, existingTrains, existingStops, err := target.ScheduleCounts(ctx, result.ServiceDate)
	if err != nil {
		return result, err
	}
	result.ExistingStations = existingStations
	result.ExistingTrains = existingTrains
	result.ExistingStops = existingStops

	if onlyWhenMissing && existingTrains > 0 {
		result.Reason = "schedule already present in Spacetime"
		return result, nil
	}
	if dryRun {
		result.Reason = "dry run"
		return result, nil
	}

	stationsWritten, trainsWritten, stopsWritten, err := target.ReplaceScheduleSnapshot(
		ctx,
		result.ServiceDate,
		result.SourceVersion,
		base.Stations,
		base.Trains,
		base.Stops,
	)
	if err != nil {
		return result, err
	}
	result.Stations = stationsWritten
	result.Trains = trainsWritten
	result.Stops = stopsWritten
	result.Synced = true
	return result, nil
}
