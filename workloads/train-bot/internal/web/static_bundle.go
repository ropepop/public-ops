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
	SourceVersion    string                `json:"-"`
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
	Source      string `json:"-"`
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
	SourceVersion    string                `json:"-"`
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
	manifest   *staticBundleManifest
	stations   []domain.Station
	trainsByID map[string]domain.TrainInstance
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
		"trains.json":         publicStaticBundleTrains(base.Trains),
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
	sliceNames := make([]string, 0, len(slices))
	for name := range slices {
		sliceNames = append(sliceNames, name)
	}
	sort.Strings(sliceNames)
	for _, name := range sliceNames {
		payload := slices[name]
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
	versionDir := filepath.Join(p.dir, version)
	generatedAt := now.UTC().Format(time.RFC3339)
	if existingGeneratedAt := existingStaticBundleGeneratedAt(versionDir, version); existingGeneratedAt != "" {
		generatedAt = existingGeneratedAt
	}
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
	if err := removeOldStaticBundleVersions(p.dir, manifest.Version); err != nil {
		return nil, err
	}
	if p.syncer != nil {
		if syncErr := p.syncer.PublishActiveBundle(ctx, manifest.Version, manifest.ServiceDate, now.UTC(), manifest.SourceVersion); syncErr != nil {
			return nil, syncErr
		}
	}
	return manifest, nil
}

func existingStaticBundleGeneratedAt(versionDir string, version string) string {
	body, err := os.ReadFile(filepath.Join(versionDir, "manifest.json"))
	if err != nil {
		return ""
	}
	var manifest staticBundleManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return ""
	}
	if strings.TrimSpace(manifest.Version) != strings.TrimSpace(version) {
		return ""
	}
	return strings.TrimSpace(manifest.GeneratedAt)
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
	if err := loadSlice(manifest.Slices.Stations, &stations); err != nil {
		return nil, err
	}
	if err := loadSlice(manifest.Slices.Trains, &trains); err != nil {
		return nil, err
	}
	data := newStaticBundleData(manifest, stations, trains)
	s.mu.Lock()
	s.cachedData = data
	s.mu.Unlock()
	return data, nil
}

func newStaticBundleData(manifest *staticBundleManifest, stations []domain.Station, trains []domain.TrainInstance) *staticBundleData {
	data := &staticBundleData{
		manifest:   manifest,
		stations:   append([]domain.Station(nil), stations...),
		trainsByID: make(map[string]domain.TrainInstance, len(trains)),
	}
	for _, train := range trains {
		data.trainsByID[strings.TrimSpace(train.ID)] = train
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

func publicStaticBundleTrains(trains []domain.TrainInstance) []trainapp.PublicTrainInstance {
	out := make([]trainapp.PublicTrainInstance, 0, len(trains))
	for _, train := range trains {
		out = append(out, trainapp.PublicTrainInstanceFor(train))
	}
	return out
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

func removeOldStaticBundleVersions(parentDir string, activeVersion string) error {
	activeVersion = strings.TrimSpace(activeVersion)
	if strings.TrimSpace(parentDir) == "" || activeVersion == "" {
		return nil
	}
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read static bundle versions: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == activeVersion {
			continue
		}
		if err := os.RemoveAll(filepath.Join(parentDir, entry.Name())); err != nil {
			return fmt.Errorf("remove old static bundle version %s: %w", entry.Name(), err)
		}
	}
	return nil
}
