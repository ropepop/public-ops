package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/stationsearch"
)

const staticBundleTransformVersion = "static-v1"

type activeBundleSync interface {
	PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error
}

type staticBundleManifest struct {
	Version          string                `json:"version"`
	ServiceDate      string                `json:"serviceDate"`
	SourceVersion    string                `json:"sourceVersion"`
	TransformVersion string                `json:"transformVersion"`
	GeneratedAt      string                `json:"generatedAt"`
	Counts           staticBundleCounts    `json:"counts"`
	Slices           staticBundleSliceSet  `json:"slices"`
	Freshness        staticBundleFreshness `json:"freshness"`
}

type staticBundleCounts struct {
	Stations      int `json:"stations"`
	Trains        int `json:"trains"`
	Stops         int `json:"stops"`
	StationPasses int `json:"stationPasses"`
}

type staticBundleFreshness struct {
	ServiceDate string `json:"serviceDate"`
	GeneratedAt string `json:"generatedAt"`
	Source      string `json:"sourceVersion"`
}

type staticBundleSliceSet struct {
	Stations      string `json:"stations"`
	Trains        string `json:"trains"`
	Stops         string `json:"stops"`
	StationPasses string `json:"stationPasses"`
	TrainGraph    string `json:"trainGraph"`
}

type staticBundleActiveState struct {
	Version          string                `json:"version"`
	ServiceDate      string                `json:"serviceDate"`
	SourceVersion    string                `json:"sourceVersion"`
	TransformVersion string                `json:"transformVersion"`
	GeneratedAt      string                `json:"generatedAt"`
	ManifestPath     string                `json:"manifestPath"`
	Freshness        staticBundleFreshness `json:"freshness"`
}

type staticBundleStationPass struct {
	TrainID     string `json:"trainId"`
	StationID   string `json:"stationId"`
	StationName string `json:"stationName"`
	Seq         int    `json:"seq"`
	PassAt      string `json:"passAt"`
}

type staticBundleGraphPayload struct {
	Data []staticBundleGraphRoute `json:"data"`
}

type staticBundleGraphRoute struct {
	ID        string                  `json:"id"`
	Train     string                  `json:"train"`
	SchDate   string                  `json:"schDate"`
	Name      string                  `json:"name"`
	Departure string                  `json:"departure"`
	Arrival   string                  `json:"arrival"`
	Stops     []staticBundleGraphStop `json:"stops"`
}

type staticBundleGraphStop struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Coords    []float64 `json:"coords"`
	Departure string    `json:"departure,omitempty"`
	RoutesID  string    `json:"routes_id"`
	GPSID     string    `json:"gps_id,omitempty"`
	I         int       `json:"i"`
}

type staticBundlePublisher struct {
	dir    string
	app    *trainapp.Service
	loc    *time.Location
	syncer activeBundleSync
}

type staticBundleStore struct {
	dir string

	mu             sync.RWMutex
	cachedVersion  string
	cachedManifest *staticBundleManifest
	cachedData     *staticBundleData
}

type staticBundleData struct {
	manifest      *staticBundleManifest
	stations      []domain.Station
	trains        []domain.TrainInstance
	stops         []domain.TrainStop
	stationPasses []staticBundleStationPass

	stationByID     map[string]domain.Station
	trainsByID      map[string]domain.TrainInstance
	stopsByTrain    map[string][]domain.TrainStop
	passesByStation map[string][]staticBundleStationPass
}

func newStaticBundlePublisher(dir string, appSvc *trainapp.Service, loc *time.Location, syncer activeBundleSync) *staticBundlePublisher {
	return &staticBundlePublisher{
		dir:    strings.TrimSpace(dir),
		app:    appSvc,
		loc:    loc,
		syncer: syncer,
	}
}

func NewStaticBundlePublisher(dir string, appSvc *trainapp.Service, loc *time.Location, syncer activeBundleSync) *staticBundlePublisher {
	return newStaticBundlePublisher(dir, appSvc, loc, syncer)
}

func newStaticBundleStore(dir string) *staticBundleStore {
	return &staticBundleStore{dir: strings.TrimSpace(dir)}
}

func (p *staticBundlePublisher) Enabled() bool {
	return p != nil && p.app != nil && strings.TrimSpace(p.dir) != ""
}

func (p *staticBundlePublisher) Publish(ctx context.Context, now time.Time) error {
	_, err := p.PublishManifest(ctx, now)
	return err
}

func (p *staticBundlePublisher) PublishManifest(ctx context.Context, now time.Time) (*staticBundleManifest, error) {
	if !p.Enabled() {
		return nil, nil
	}
	base, err := p.app.StaticBundleBase(ctx, now)
	if err != nil {
		return nil, err
	}
	stationPasses := buildStaticBundleStationPasses(base.Stops)
	graphPayload := buildStaticBundleGraphPayload(base.Trains, base.Stops)
	slices := map[string]any{
		"stations.json":       base.Stations,
		"trains.json":         base.Trains,
		"stops.json":          base.Stops,
		"station-passes.json": stationPasses,
		"train-graph.json":    graphPayload,
	}
	sliceBytes := make(map[string][]byte, len(slices))
	hasher := sha256.New()
	hasher.Write([]byte(staticBundleTransformVersion))
	hasher.Write([]byte{0})
	hasher.Write([]byte(strings.TrimSpace(base.ServiceDate)))
	hasher.Write([]byte{0})
	hasher.Write([]byte(strings.TrimSpace(base.SourceVersion)))
	for name, payload := range slices {
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal static bundle slice %s: %w", name, marshalErr)
		}
		sliceBytes[name] = append(body, '\n')
		hasher.Write([]byte{0})
		hasher.Write([]byte(name))
		hasher.Write([]byte{0})
		hasher.Write(body)
	}
	version := fmt.Sprintf("%s-%s", strings.TrimSpace(base.ServiceDate), hex.EncodeToString(hasher.Sum(nil))[:12])
	generatedAt := now.UTC().Format(time.RFC3339)
	manifest := &staticBundleManifest{
		Version:          version,
		ServiceDate:      strings.TrimSpace(base.ServiceDate),
		SourceVersion:    strings.TrimSpace(base.SourceVersion),
		TransformVersion: staticBundleTransformVersion,
		GeneratedAt:      generatedAt,
		Counts: staticBundleCounts{
			Stations:      len(base.Stations),
			Trains:        len(base.Trains),
			Stops:         len(base.Stops),
			StationPasses: len(stationPasses),
		},
		Slices: staticBundleSliceSet{
			Stations:      "stations.json",
			Trains:        "trains.json",
			Stops:         "stops.json",
			StationPasses: "station-passes.json",
			TrainGraph:    "train-graph.json",
		},
		Freshness: staticBundleFreshness{
			ServiceDate: strings.TrimSpace(base.ServiceDate),
			GeneratedAt: generatedAt,
			Source:      strings.TrimSpace(base.SourceVersion),
		},
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create static bundle dir: %w", err)
	}
	versionDir := filepath.Join(p.dir, version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create static bundle version dir: %w", err)
	}
	for name, body := range sliceBytes {
		if err := writeJSONFile(filepath.Join(versionDir, name), body); err != nil {
			return nil, err
		}
	}
	manifestBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal static bundle manifest: %w", err)
	}
	if err := writeJSONFile(filepath.Join(versionDir, "manifest.json"), append(manifestBody, '\n')); err != nil {
		return nil, err
	}
	active := staticBundleActiveState{
		Version:          manifest.Version,
		ServiceDate:      manifest.ServiceDate,
		SourceVersion:    manifest.SourceVersion,
		TransformVersion: manifest.TransformVersion,
		GeneratedAt:      manifest.GeneratedAt,
		ManifestPath:     filepath.ToSlash(filepath.Join("bundles", manifest.Version, "manifest.json")),
		Freshness:        manifest.Freshness,
	}
	activeBody, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal static bundle active state: %w", err)
	}
	if err := writeJSONFile(filepath.Join(p.dir, "active.json"), append(activeBody, '\n')); err != nil {
		return nil, err
	}
	if p.syncer != nil {
		if syncErr := p.syncer.PublishActiveBundle(ctx, manifest.Version, manifest.ServiceDate, now.UTC(), manifest.SourceVersion); syncErr != nil {
			return nil, syncErr
		}
	}
	return manifest, nil
}

func (s *staticBundleStore) activeState() (*staticBundleActiveState, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	body, err := os.ReadFile(filepath.Join(s.dir, "active.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read static bundle active state: %w", err)
	}
	var state staticBundleActiveState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("decode static bundle active state: %w", err)
	}
	if strings.TrimSpace(state.Version) == "" || strings.TrimSpace(state.ManifestPath) == "" {
		return nil, nil
	}
	return &state, nil
}

func (s *staticBundleStore) activeManifest() (*staticBundleManifest, error) {
	state, err := s.activeState()
	if err != nil || state == nil {
		return nil, err
	}
	s.mu.RLock()
	if s.cachedManifest != nil && s.cachedVersion == state.Version {
		defer s.mu.RUnlock()
		return s.cachedManifest, nil
	}
	s.mu.RUnlock()
	manifestPath := filepath.Join(s.dir, state.Version, "manifest.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read static bundle manifest: %w", err)
	}
	var manifest staticBundleManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode static bundle manifest: %w", err)
	}
	s.mu.Lock()
	s.cachedVersion = state.Version
	s.cachedManifest = &manifest
	s.cachedData = nil
	s.mu.Unlock()
	return &manifest, nil
}

func (s *staticBundleStore) bundleAssetURL(basePath string) string {
	state, err := s.activeState()
	if err != nil || state == nil {
		return ""
	}
	return strings.TrimRight(basePath, "/") + "/assets/" + strings.TrimLeft(state.ManifestPath, "/")
}

func (s *staticBundleStore) bundleMetadata() (*staticBundleActiveState, error) {
	return s.activeState()
}

func (s *staticBundleStore) loadData() (*staticBundleData, error) {
	manifest, err := s.activeManifest()
	if err != nil || manifest == nil {
		return nil, err
	}
	s.mu.RLock()
	if s.cachedData != nil && s.cachedVersion == manifest.Version {
		defer s.mu.RUnlock()
		return s.cachedData, nil
	}
	s.mu.RUnlock()
	loadSlice := func(relativePath string, dest any) error {
		body, readErr := os.ReadFile(filepath.Join(s.dir, manifest.Version, relativePath))
		if readErr != nil {
			return fmt.Errorf("read static bundle slice %s: %w", relativePath, readErr)
		}
		if unmarshalErr := json.Unmarshal(body, dest); unmarshalErr != nil {
			return fmt.Errorf("decode static bundle slice %s: %w", relativePath, unmarshalErr)
		}
		return nil
	}
	var stations []domain.Station
	var trains []domain.TrainInstance
	var stops []domain.TrainStop
	var stationPasses []staticBundleStationPass
	if err := loadSlice(manifest.Slices.Stations, &stations); err != nil {
		return nil, err
	}
	if err := loadSlice(manifest.Slices.Trains, &trains); err != nil {
		return nil, err
	}
	if err := loadSlice(manifest.Slices.Stops, &stops); err != nil {
		return nil, err
	}
	if err := loadSlice(manifest.Slices.StationPasses, &stationPasses); err != nil {
		return nil, err
	}
	data := newStaticBundleData(manifest, stations, trains, stops, stationPasses)
	s.mu.Lock()
	s.cachedData = data
	s.mu.Unlock()
	return data, nil
}

func newStaticBundleData(manifest *staticBundleManifest, stations []domain.Station, trains []domain.TrainInstance, stops []domain.TrainStop, stationPasses []staticBundleStationPass) *staticBundleData {
	data := &staticBundleData{
		manifest:        manifest,
		stations:        append([]domain.Station(nil), stations...),
		trains:          append([]domain.TrainInstance(nil), trains...),
		stops:           append([]domain.TrainStop(nil), stops...),
		stationPasses:   append([]staticBundleStationPass(nil), stationPasses...),
		stationByID:     make(map[string]domain.Station, len(stations)),
		trainsByID:      make(map[string]domain.TrainInstance, len(trains)),
		stopsByTrain:    make(map[string][]domain.TrainStop, len(trains)),
		passesByStation: make(map[string][]staticBundleStationPass),
	}
	for _, station := range data.stations {
		data.stationByID[strings.TrimSpace(station.ID)] = station
	}
	for _, train := range data.trains {
		data.trainsByID[strings.TrimSpace(train.ID)] = train
	}
	for _, stop := range data.stops {
		trainID := strings.TrimSpace(stop.TrainInstanceID)
		data.stopsByTrain[trainID] = append(data.stopsByTrain[trainID], stop)
	}
	for trainID := range data.stopsByTrain {
		sort.SliceStable(data.stopsByTrain[trainID], func(i, j int) bool {
			return data.stopsByTrain[trainID][i].Seq < data.stopsByTrain[trainID][j].Seq
		})
	}
	for _, pass := range data.stationPasses {
		stationID := strings.TrimSpace(pass.StationID)
		data.passesByStation[stationID] = append(data.passesByStation[stationID], pass)
	}
	for stationID := range data.passesByStation {
		sort.SliceStable(data.passesByStation[stationID], func(i, j int) bool {
			left := parseBundleTime(data.passesByStation[stationID][i].PassAt)
			right := parseBundleTime(data.passesByStation[stationID][j].PassAt)
			return left.Before(right)
		})
	}
	return data
}

func (d *staticBundleData) schedulePayload(now time.Time) schedule.AccessContext {
	serviceDate := strings.TrimSpace(d.manifest.ServiceDate)
	localNow := now
	requestedServiceDate := localNow.Format("2006-01-02")
	return schedule.AccessContext{
		RequestedServiceDate: requestedServiceDate,
		EffectiveServiceDate: serviceDate,
		LoadedServiceDate:    serviceDate,
		FallbackActive:       serviceDate != "" && serviceDate != requestedServiceDate,
		CutoffHour:           3,
		Available:            serviceDate != "",
		SameDayFresh:         serviceDate != "" && serviceDate == requestedServiceDate,
	}
}

func (d *staticBundleData) withSchedule(payload map[string]any, now time.Time) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["schedule"] = d.schedulePayload(now)
	return payload
}

func (d *staticBundleData) defaultTrainStatus() domain.TrainStatus {
	return domain.TrainStatus{
		State:           domain.StatusNoReports,
		Confidence:      domain.ConfidenceLow,
		UniqueReporters: 0,
	}
}

func (d *staticBundleData) defaultTrainCard(train domain.TrainInstance) trainapp.TrainCard {
	return trainapp.TrainCard{
		Train:  train,
		Status: d.defaultTrainStatus(),
		Riders: 0,
	}
}

func (d *staticBundleData) trainsByWindow(now time.Time, windowID string) []domain.TrainInstance {
	localNow := now
	start := localNow
	end := localNow
	switch strings.TrimSpace(windowID) {
	case "now":
		start = localNow.Add(-15 * time.Minute)
		end = localNow.Add(15 * time.Minute)
	case "next_hour":
		start = localNow
		end = localNow.Add(1 * time.Hour)
	case "today":
		start = localNow.Add(-30 * time.Minute)
		end = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 59, 0, localNow.Location())
	default:
		return nil
	}
	items := make([]domain.TrainInstance, 0)
	for _, train := range d.trains {
		if train.DepartureAt.Before(start) || train.DepartureAt.After(end) {
			continue
		}
		items = append(items, train)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].DepartureAt.Before(items[j].DepartureAt)
	})
	return items
}

func (d *staticBundleData) publicDashboard(now time.Time, limit int) map[string]any {
	items := d.trainsByWindow(now, "today")
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]trainapp.PublicTrainView, 0, len(items))
	for _, train := range items {
		card := d.defaultTrainCard(train)
		out = append(out, trainapp.PublicTrainView{
			Train:            card.Train,
			Status:           card.Status,
			Riders:           card.Riders,
			Timeline:         nil,
			StationSightings: nil,
		})
	}
	return d.withSchedule(map[string]any{
		"generatedAt": now.UTC(),
		"trains":      out,
	}, now)
}

func (d *staticBundleData) publicServiceDayTrains(now time.Time) map[string]any {
	items := append([]domain.TrainInstance(nil), d.trains...)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].DepartureAt.Before(items[j].DepartureAt)
	})
	out := make([]trainapp.PublicTrainView, 0, len(items))
	for _, train := range items {
		card := d.defaultTrainCard(train)
		out = append(out, trainapp.PublicTrainView{
			Train:            card.Train,
			Status:           card.Status,
			Riders:           card.Riders,
			Timeline:         nil,
			StationSightings: nil,
		})
	}
	return d.withSchedule(map[string]any{
		"generatedAt": now.UTC(),
		"trains":      out,
	}, now)
}

func (d *staticBundleData) publicTrain(now time.Time, trainID string) map[string]any {
	train, ok := d.trainsByID[strings.TrimSpace(trainID)]
	if !ok {
		return nil
	}
	return d.withSchedule(map[string]any{
		"train":            train,
		"status":           d.defaultTrainStatus(),
		"riders":           d.defaultTrainCard(train).Riders,
		"timeline":         []any{},
		"stationSightings": []any{},
	}, now)
}

func (d *staticBundleData) trainStops(now time.Time, trainID string) map[string]any {
	train, ok := d.trainsByID[strings.TrimSpace(trainID)]
	if !ok {
		return nil
	}
	return d.withSchedule(map[string]any{
		"trainCard":        d.defaultTrainCard(train),
		"train":            train,
		"stops":            d.stopsByTrain[strings.TrimSpace(trainID)],
		"stationSightings": []any{},
	}, now)
}

func (d *staticBundleData) publicNetworkMap(now time.Time) map[string]any {
	stations := make([]domain.Station, 0, len(d.stations))
	for _, station := range d.stations {
		if station.Latitude == nil || station.Longitude == nil {
			continue
		}
		stations = append(stations, station)
	}
	return d.withSchedule(map[string]any{
		"stations":         stations,
		"recentSightings":  []any{},
		"sameDaySightings": []any{},
	}, now)
}

func (d *staticBundleData) searchStations(now time.Time, query string) map[string]any {
	return d.withSchedule(map[string]any{
		"stations": filterBundleStations(d.stations, query),
	}, now)
}

func (d *staticBundleData) publicStationDepartures(now time.Time, stationID string, limit int) map[string]any {
	station, ok := d.stationByID[strings.TrimSpace(stationID)]
	if !ok {
		return nil
	}
	passes := d.passesByStation[strings.TrimSpace(stationID)]
	localNow := now
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, localNow.Location())
	dayEnd := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), localNow.Location())
	var lastDeparture *trainapp.StationTrainCard
	upcoming := make([]trainapp.StationTrainCard, 0)
	for _, pass := range passes {
		passAt := parseBundleTime(pass.PassAt)
		if passAt.Before(dayStart) || passAt.After(dayEnd) {
			continue
		}
		card := d.stationTrainCard(pass, passAt)
		if passAt.Before(localNow) {
			copyCard := card
			lastDeparture = &copyCard
			continue
		}
		upcoming = append(upcoming, card)
	}
	if limit > 0 && len(upcoming) > limit {
		upcoming = upcoming[:limit]
	}
	return d.withSchedule(map[string]any{
		"station":         station,
		"lastDeparture":   lastDeparture,
		"upcoming":        upcoming,
		"recentSightings": []any{},
	}, now)
}

func (d *staticBundleData) windowTrains(now time.Time, windowID string) map[string]any {
	trains := d.trainsByWindow(now, windowID)
	items := make([]trainapp.TrainCard, 0, len(trains))
	for _, train := range trains {
		items = append(items, d.defaultTrainCard(train))
	}
	return d.withSchedule(map[string]any{"trains": items}, now)
}

func (d *staticBundleData) stationDepartures(now time.Time, stationID string, pastWindow, futureWindow time.Duration) map[string]any {
	station, ok := d.stationByID[strings.TrimSpace(stationID)]
	if !ok {
		return nil
	}
	if pastWindow <= 0 {
		pastWindow = 2 * time.Hour
	}
	if futureWindow <= 0 {
		futureWindow = 2 * time.Hour
	}
	start := now.Add(-pastWindow)
	end := now.Add(futureWindow)
	items := make([]trainapp.StationTrainCard, 0)
	for _, pass := range d.passesByStation[strings.TrimSpace(stationID)] {
		passAt := parseBundleTime(pass.PassAt)
		if passAt.Before(start) || passAt.After(end) {
			continue
		}
		items = append(items, d.stationTrainCard(pass, passAt))
	}
	return d.withSchedule(map[string]any{
		"station":         station,
		"trains":          items,
		"recentSightings": []any{},
	}, now)
}

func (d *staticBundleData) stationSightingDestinations(now time.Time, stationID string) map[string]any {
	return d.withSchedule(map[string]any{
		"stations": d.reachableDestinations(strings.TrimSpace(stationID), ""),
	}, now)
}

func (d *staticBundleData) routeDestinations(now time.Time, stationID string, query string) map[string]any {
	return d.withSchedule(map[string]any{
		"stations": d.reachableDestinations(strings.TrimSpace(stationID), query),
	}, now)
}

func (d *staticBundleData) routeTrains(now time.Time, originStationID string, destinationStationID string, window time.Duration) map[string]any {
	if window <= 0 {
		window = 18 * time.Hour
	}
	items := make([]trainapp.RouteTrainCard, 0)
	start := now.Add(-30 * time.Minute)
	end := now.Add(window)
	for _, train := range d.trains {
		stops := d.stopsByTrain[strings.TrimSpace(train.ID)]
		if len(stops) == 0 {
			continue
		}
		var fromStop *domain.TrainStop
		var toStop *domain.TrainStop
		for idx := range stops {
			stop := stops[idx]
			if strings.TrimSpace(stop.StationID) == strings.TrimSpace(originStationID) {
				copyStop := stop
				fromStop = &copyStop
			}
			if fromStop != nil && strings.TrimSpace(stop.StationID) == strings.TrimSpace(destinationStationID) && stop.Seq > fromStop.Seq {
				copyStop := stop
				toStop = &copyStop
				break
			}
		}
		if fromStop == nil || toStop == nil {
			continue
		}
		fromPassAt := bundlePassAt(*fromStop)
		toPassAt := bundlePassAt(*toStop)
		if fromPassAt.Before(start) || fromPassAt.After(end) {
			continue
		}
		items = append(items, trainapp.RouteTrainCard{
			TrainCard:       d.defaultTrainCard(train),
			FromStationID:   strings.TrimSpace(fromStop.StationID),
			FromStationName: fromStop.StationName,
			ToStationID:     strings.TrimSpace(toStop.StationID),
			ToStationName:   toStop.StationName,
			FromPassAt:      fromPassAt,
			ToPassAt:        toPassAt,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].FromPassAt.Before(items[j].FromPassAt)
	})
	return d.withSchedule(map[string]any{"trains": items}, now)
}

func (d *staticBundleData) reachableDestinations(originStationID string, query string) []domain.Station {
	type destinationInfo struct {
		station domain.Station
		order   time.Time
	}
	destinations := make(map[string]destinationInfo)
	for trainID, stops := range d.stopsByTrain {
		_ = trainID
		var originSeq int
		var sawOrigin bool
		for _, stop := range stops {
			if strings.TrimSpace(stop.StationID) == strings.TrimSpace(originStationID) {
				originSeq = stop.Seq
				sawOrigin = true
				break
			}
		}
		if !sawOrigin {
			continue
		}
		lastStop := stops[len(stops)-1]
		if lastStop.Seq <= originSeq {
			continue
		}
		station, ok := d.stationByID[strings.TrimSpace(lastStop.StationID)]
		if !ok {
			station = domain.Station{
				ID:            strings.TrimSpace(lastStop.StationID),
				Name:          lastStop.StationName,
				NormalizedKey: stationsearch.Normalize(lastStop.StationName),
			}
		}
		passAt := bundlePassAt(lastStop)
		existing, exists := destinations[station.ID]
		if !exists || passAt.Before(existing.order) {
			destinations[station.ID] = destinationInfo{station: station, order: passAt}
		}
	}
	items := make([]domain.Station, 0, len(destinations))
	for _, item := range destinations {
		items = append(items, item.station)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return filterBundleStations(items, query)
}

func (d *staticBundleData) stationTrainCard(pass staticBundleStationPass, passAt time.Time) trainapp.StationTrainCard {
	train := d.trainsByID[strings.TrimSpace(pass.TrainID)]
	return trainapp.StationTrainCard{
		TrainCard:       d.defaultTrainCard(train),
		StationID:       strings.TrimSpace(pass.StationID),
		StationName:     pass.StationName,
		PassAt:          passAt,
		SightingCount:   0,
		SightingContext: nil,
	}
}

func filterBundleStations(stations []domain.Station, query string) []domain.Station {
	normalizedQuery := stationsearch.Normalize(query)
	if normalizedQuery == "" {
		return append([]domain.Station(nil), stations...)
	}
	out := make([]domain.Station, 0, len(stations))
	for _, station := range stations {
		normalizedKey := stationsearch.Normalize(station.NormalizedKey)
		normalizedName := stationsearch.Normalize(station.Name)
		if strings.HasPrefix(normalizedKey, normalizedQuery) || strings.HasPrefix(normalizedName, normalizedQuery) {
			out = append(out, station)
		}
	}
	return out
}

func buildStaticBundleStationPasses(stops []domain.TrainStop) []staticBundleStationPass {
	out := make([]staticBundleStationPass, 0, len(stops))
	for _, stop := range stops {
		passAt := stop.DepartureAt
		if passAt == nil {
			passAt = stop.ArrivalAt
		}
		if passAt == nil {
			continue
		}
		out = append(out, staticBundleStationPass{
			TrainID:     strings.TrimSpace(stop.TrainInstanceID),
			StationID:   strings.TrimSpace(stop.StationID),
			StationName: stop.StationName,
			Seq:         stop.Seq,
			PassAt:      passAt.UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := parseBundleTime(out[i].PassAt)
		right := parseBundleTime(out[j].PassAt)
		if left.Equal(right) {
			if out[i].StationID == out[j].StationID {
				return out[i].TrainID < out[j].TrainID
			}
			return out[i].StationID < out[j].StationID
		}
		return left.Before(right)
	})
	return out
}

func buildStaticBundleGraphPayload(trains []domain.TrainInstance, stops []domain.TrainStop) staticBundleGraphPayload {
	stopsByTrain := make(map[string][]domain.TrainStop, len(trains))
	for _, stop := range stops {
		trainID := strings.TrimSpace(stop.TrainInstanceID)
		stopsByTrain[trainID] = append(stopsByTrain[trainID], stop)
	}
	routes := make([]staticBundleGraphRoute, 0, len(trains))
	for _, train := range trains {
		trainStops := append([]domain.TrainStop(nil), stopsByTrain[strings.TrimSpace(train.ID)]...)
		sort.SliceStable(trainStops, func(i, j int) bool {
			return trainStops[i].Seq < trainStops[j].Seq
		})
		routeStops := make([]staticBundleGraphStop, 0, len(trainStops))
		for _, stop := range trainStops {
			if stop.Latitude == nil || stop.Longitude == nil {
				continue
			}
			routeStops = append(routeStops, staticBundleGraphStop{
				ID:        strings.TrimSpace(stop.StationID),
				Title:     stop.StationName,
				Coords:    []float64{*stop.Latitude, *stop.Longitude},
				Departure: optionalBundleTime(stop.DepartureAt),
				RoutesID:  strings.TrimSpace(train.ID),
				GPSID:     strings.TrimSpace(stop.StationID),
				I:         stop.Seq,
			})
		}
		routes = append(routes, staticBundleGraphRoute{
			ID:        strings.TrimSpace(train.ID),
			Train:     bundleTrainNumber(train.ID),
			SchDate:   strings.TrimSpace(train.ServiceDate),
			Name:      strings.TrimSpace(train.FromStation) + " → " + strings.TrimSpace(train.ToStation),
			Departure: train.DepartureAt.UTC().Format(time.RFC3339),
			Arrival:   train.ArrivalAt.UTC().Format(time.RFC3339),
			Stops:     routeStops,
		})
	}
	return staticBundleGraphPayload{Data: routes}
}

func bundlePassAt(stop domain.TrainStop) time.Time {
	if stop.DepartureAt != nil {
		return stop.DepartureAt.UTC()
	}
	if stop.ArrivalAt != nil {
		return stop.ArrivalAt.UTC()
	}
	return time.Time{}
}

func parseBundleTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func optionalBundleTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func bundleTrainNumber(trainID string) string {
	clean := strings.TrimSpace(trainID)
	for idx := len(clean) - 1; idx >= 0; idx-- {
		if clean[idx] < '0' || clean[idx] > '9' {
			if idx == len(clean)-1 {
				return clean
			}
			return clean[idx+1:]
		}
	}
	return clean
}

func writeJSONFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create static bundle parent dir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return fmt.Errorf("write static bundle file %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename static bundle file %s: %w", path, err)
	}
	return nil
}
