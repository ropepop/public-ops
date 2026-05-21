package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type publicEdgeCacheRouteKind string

const (
	publicEdgeCacheMessagesKind          publicEdgeCacheRouteKind = "messages"
	publicEdgeCacheDashboardKind         publicEdgeCacheRouteKind = "public_dashboard"
	publicEdgeCacheServiceDayTrainsKind  publicEdgeCacheRouteKind = "public_service_day_trains"
	publicEdgeCacheNetworkMapKind        publicEdgeCacheRouteKind = "public_network_map"
	publicEdgeCacheStationSearchKind     publicEdgeCacheRouteKind = "public_station_search"
	publicEdgeCacheStationDeparturesKind publicEdgeCacheRouteKind = "public_station_departures"
	publicEdgeCacheTrainKind             publicEdgeCacheRouteKind = "public_train"
	publicEdgeCacheTrainStopsKind        publicEdgeCacheRouteKind = "public_train_stops"
	publicEdgeCacheIncidentsKind         publicEdgeCacheRouteKind = "public_incidents"
	publicEdgeCacheIncidentDetailKind    publicEdgeCacheRouteKind = "public_incident_detail"
	maxPublicEdgeCacheHTTPMaxAgeSec                               = 60
)

type publicEdgeCacheRoute struct {
	kind                 publicEdgeCacheRouteKind
	lang                 string
	limit                int
	query                string
	trainID              string
	stationID            string
	incidentID           string
	disallowSessionShare bool
}

func publicEdgeCacheMessagesRoute(lang string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheMessagesKind, lang: strings.ToUpper(strings.TrimSpace(lang))}
}

func publicEdgeCacheDashboardRoute(limit int) publicEdgeCacheRoute {
	if limit < 0 {
		limit = 0
	}
	return publicEdgeCacheRoute{kind: publicEdgeCacheDashboardKind, limit: limit}
}

func publicEdgeCacheServiceDayTrainsRoute() publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheServiceDayTrainsKind}
}

func publicEdgeCacheNetworkMapRoute() publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheNetworkMapKind}
}

func publicEdgeCacheStationSearchRoute(query string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheStationSearchKind, query: normalizePublicEdgeCacheQuery(query)}
}

func publicEdgeCacheStationDeparturesRoute(stationID string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheStationDeparturesKind, stationID: strings.TrimSpace(stationID)}
}

func publicEdgeCacheTrainRoute(trainID string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheTrainKind, trainID: strings.TrimSpace(trainID)}
}

func publicEdgeCacheTrainStopsRoute(trainID string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{kind: publicEdgeCacheTrainStopsKind, trainID: strings.TrimSpace(trainID)}
}

func publicEdgeCacheIncidentsRoute(limit int) publicEdgeCacheRoute {
	if limit < 0 {
		limit = 0
	}
	return publicEdgeCacheRoute{
		kind:                 publicEdgeCacheIncidentsKind,
		limit:                limit,
		disallowSessionShare: true,
	}
}

func publicEdgeCacheIncidentDetailRoute(incidentID string) publicEdgeCacheRoute {
	return publicEdgeCacheRoute{
		kind:                 publicEdgeCacheIncidentDetailKind,
		incidentID:           strings.TrimSpace(incidentID),
		disallowSessionShare: true,
	}
}

func (r publicEdgeCacheRoute) sliceName() string {
	switch r.kind {
	case publicEdgeCacheMessagesKind:
		return "messages"
	case publicEdgeCacheDashboardKind:
		return "public-dashboard"
	case publicEdgeCacheServiceDayTrainsKind:
		return "public-service-day-trains"
	case publicEdgeCacheNetworkMapKind:
		return "public-network-map"
	case publicEdgeCacheStationSearchKind:
		return "public-stations"
	case publicEdgeCacheStationDeparturesKind:
		return "public-station-departures"
	case publicEdgeCacheTrainKind:
		return "public-train"
	case publicEdgeCacheTrainStopsKind:
		return "public-train-stops"
	case publicEdgeCacheIncidentsKind:
		return "public-incidents"
	case publicEdgeCacheIncidentDetailKind:
		return "public-incident-detail"
	default:
		return "public"
	}
}

type publicEdgeCacheDecision struct {
	route   publicEdgeCacheRoute
	version string
}

type publicEdgeCache struct {
	mu          sync.Mutex
	ttlSec      int
	statePath   string
	catalogSeed string
	state       publicEdgeCacheState
}

type publicEdgeCacheState struct {
	CatalogVersion       uint64            `json:"catalogVersion"`
	LoadedServiceDate    string            `json:"loadedServiceDate,omitempty"`
	LoadedScheduleID     string            `json:"loadedScheduleId,omitempty"`
	ScheduleRoot         uint64            `json:"scheduleRoot"`
	DashboardReportsRoot uint64            `json:"dashboardReportsRoot"`
	NetworkSightingsRoot uint64            `json:"networkSightingsRoot"`
	IncidentsListRoot    uint64            `json:"incidentsListRoot"`
	TrainVersion         map[string]uint64 `json:"trainVersion,omitempty"`
	StationVersion       map[string]uint64 `json:"stationVersion,omitempty"`
	IncidentVersion      map[string]uint64 `json:"incidentVersion,omitempty"`
}

func newPublicEdgeCache(statePath string, ttlSec int, catalogSeed string) (*publicEdgeCache, error) {
	cache := &publicEdgeCache{
		ttlSec:      ttlSec,
		statePath:   strings.TrimSpace(statePath),
		catalogSeed: strings.TrimSpace(catalogSeed),
	}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *publicEdgeCache) load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state = publicEdgeCacheState{}
	if c.statePath != "" {
		body, err := os.ReadFile(c.statePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read public edge cache state: %w", err)
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &c.state); err != nil {
				return fmt.Errorf("decode public edge cache state: %w", err)
			}
		}
	}
	c.normalizeLocked()
	return nil
}

func (c *publicEdgeCache) normalizeLocked() {
	if c.state.CatalogVersion == 0 {
		c.state.CatalogVersion = 1
	}
	if c.state.ScheduleRoot == 0 {
		c.state.ScheduleRoot = 1
	}
	if c.state.DashboardReportsRoot == 0 {
		c.state.DashboardReportsRoot = 1
	}
	if c.state.NetworkSightingsRoot == 0 {
		c.state.NetworkSightingsRoot = 1
	}
	if c.state.IncidentsListRoot == 0 {
		c.state.IncidentsListRoot = 1
	}
	if c.state.TrainVersion == nil {
		c.state.TrainVersion = make(map[string]uint64)
	}
	if c.state.StationVersion == nil {
		c.state.StationVersion = make(map[string]uint64)
	}
	if c.state.IncidentVersion == nil {
		c.state.IncidentVersion = make(map[string]uint64)
	}
}

func (c *publicEdgeCache) saveLocked() error {
	if c.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o755); err != nil {
		return fmt.Errorf("create public edge cache state dir: %w", err)
	}
	body, err := json.Marshal(c.state)
	if err != nil {
		return fmt.Errorf("encode public edge cache state: %w", err)
	}
	tmpPath := c.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return fmt.Errorf("write public edge cache state: %w", err)
	}
	if err := os.Rename(tmpPath, c.statePath); err != nil {
		return fmt.Errorf("rename public edge cache state: %w", err)
	}
	return nil
}

func (c *publicEdgeCache) ttl() int {
	return c.ttlSec
}

func (c *publicEdgeCache) httpMaxAge() int {
	ttl := c.ttl()
	if ttl <= 0 || ttl > maxPublicEdgeCacheHTTPMaxAgeSec {
		return maxPublicEdgeCacheHTTPMaxAgeSec
	}
	return ttl
}

func (c *publicEdgeCache) syncLoadedServiceDate(serviceDate string) {
	c.syncLoadedSchedule(serviceDate, "")
}

func (c *publicEdgeCache) syncLoadedSchedule(serviceDate string, scheduleID string) {
	trimmed := strings.TrimSpace(serviceDate)
	if trimmed == "" {
		return
	}
	trimmedScheduleID := strings.TrimSpace(scheduleID)
	if trimmedScheduleID == "" {
		trimmedScheduleID = trimmed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalizeLocked()
	if c.state.LoadedServiceDate == trimmed && c.state.LoadedScheduleID == trimmedScheduleID {
		return
	}
	c.state.LoadedServiceDate = trimmed
	c.state.LoadedScheduleID = trimmedScheduleID
	c.bumpCounterLocked(&c.state.ScheduleRoot)
	c.bumpCounterLocked(&c.state.IncidentsListRoot)
	clear(c.state.TrainVersion)
	clear(c.state.StationVersion)
	clear(c.state.IncidentVersion)
	_ = c.saveLocked()
}

func (c *publicEdgeCache) noteReportAccepted(trainID string, incidentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalizeLocked()
	c.bumpCounterLocked(&c.state.DashboardReportsRoot)
	c.bumpMapCounterLocked(c.state.TrainVersion, strings.TrimSpace(trainID))
	c.bumpMapCounterLocked(c.state.IncidentVersion, strings.TrimSpace(incidentID))
	c.bumpCounterLocked(&c.state.IncidentsListRoot)
	_ = c.saveLocked()
}

func (c *publicEdgeCache) noteStationSightingAccepted(stationID string, matchedTrainID string, incidentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalizeLocked()
	c.bumpCounterLocked(&c.state.NetworkSightingsRoot)
	c.bumpMapCounterLocked(c.state.StationVersion, strings.TrimSpace(stationID))
	c.bumpMapCounterLocked(c.state.TrainVersion, strings.TrimSpace(matchedTrainID))
	c.bumpMapCounterLocked(c.state.IncidentVersion, strings.TrimSpace(incidentID))
	c.bumpCounterLocked(&c.state.IncidentsListRoot)
	_ = c.saveLocked()
}

func (c *publicEdgeCache) noteIncidentUpdated(incidentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalizeLocked()
	c.bumpMapCounterLocked(c.state.IncidentVersion, strings.TrimSpace(incidentID))
	c.bumpCounterLocked(&c.state.IncidentsListRoot)
	_ = c.saveLocked()
}

func (c *publicEdgeCache) versionFor(route publicEdgeCacheRoute) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalizeLocked()
	switch route.kind {
	case publicEdgeCacheMessagesKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.CatalogVersion, 10),
			c.catalogSeed,
			route.lang,
		)
	case publicEdgeCacheDashboardKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			strconv.FormatUint(c.state.DashboardReportsRoot, 10),
			strconv.Itoa(route.limit),
		)
	case publicEdgeCacheServiceDayTrainsKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			strconv.FormatUint(c.state.DashboardReportsRoot, 10),
		)
	case publicEdgeCacheNetworkMapKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			strconv.FormatUint(c.state.NetworkSightingsRoot, 10),
		)
	case publicEdgeCacheStationSearchKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			route.query,
		)
	case publicEdgeCacheStationDeparturesKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			route.stationID,
			strconv.FormatUint(c.state.StationVersion[route.stationID], 10),
		)
	case publicEdgeCacheTrainKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			route.trainID,
			strconv.FormatUint(c.state.TrainVersion[route.trainID], 10),
		)
	case publicEdgeCacheTrainStopsKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			route.trainID,
			strconv.FormatUint(c.state.TrainVersion[route.trainID], 10),
		)
	case publicEdgeCacheIncidentsKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.IncidentsListRoot, 10),
			strconv.Itoa(route.limit),
		)
	case publicEdgeCacheIncidentDetailKind:
		return publicEdgeCacheHashVersion(
			string(route.kind),
			strconv.FormatUint(c.state.ScheduleRoot, 10),
			route.incidentID,
			strconv.FormatUint(c.state.IncidentVersion[route.incidentID], 10),
		)
	default:
		return publicEdgeCacheHashVersion("public")
	}
}

func (c *publicEdgeCache) bumpCounterLocked(counter *uint64) {
	if *counter == 0 {
		*counter = 1
		return
	}
	*counter++
}

func (c *publicEdgeCache) bumpMapCounterLocked(items map[string]uint64, key string) {
	if key == "" {
		return
	}
	current := items[key]
	if current == 0 {
		items[key] = 1
		return
	}
	items[key] = current + 1
}

func publicEdgeCacheHashVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func normalizePublicEdgeCacheQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

func (s *Server) beginPublicEdgeCache(w http.ResponseWriter, r *http.Request, now time.Time, route publicEdgeCacheRoute) (*publicEdgeCacheDecision, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil, false
	}
	query := r.URL.Query()
	if !publicEdgeCacheQueryAllowed(query, route) {
		s.writeError(w, http.StatusBadRequest, "invalid query")
		return nil, true
	}
	cvValues, hasCV := query["cv"]
	if hasCV && len(cvValues) != 1 {
		s.writeError(w, http.StatusBadRequest, "invalid cache version")
		return nil, true
	}
	if s.publicEdgeCache == nil {
		return nil, false
	}
	serviceDate, scheduleID := s.publicEdgeCacheScheduleState()
	s.publicEdgeCache.syncLoadedSchedule(serviceDate, scheduleID)
	if route.disallowSessionShare {
		if _, ok := s.optionalSession(r, now); ok {
			return nil, false
		}
	}

	version := s.publicEdgeCache.versionFor(route)
	if !hasCV || strings.TrimSpace(cvValues[0]) != version {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		redirectURL := cloneURLWithCacheVersion(r.URL, version)
		w.Header().Set("Location", redirectURL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		return nil, true
	}

	return &publicEdgeCacheDecision{route: route, version: version}, false
}

func publicEdgeCacheQueryAllowed(query url.Values, route publicEdgeCacheRoute) bool {
	allowed := map[string]bool{"cv": true}
	switch route.kind {
	case publicEdgeCacheMessagesKind:
		allowed["lang"] = true
	case publicEdgeCacheDashboardKind, publicEdgeCacheIncidentsKind:
		allowed["limit"] = true
	case publicEdgeCacheStationSearchKind:
		allowed["q"] = true
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return false
		}
	}
	return true
}

func (s *Server) publicEdgeCacheScheduleState() (string, string) {
	serviceDate := strings.TrimSpace(s.appLoadedServiceDate())
	scheduleID := publicEdgeCacheHashVersion("app", serviceDate)
	if s.bundleStore == nil {
		return serviceDate, scheduleID
	}
	metadata, err := s.bundleStore.bundleMetadata()
	if err != nil || metadata == nil {
		return serviceDate, scheduleID
	}
	bundleServiceDate := strings.TrimSpace(metadata.ServiceDate)
	if bundleServiceDate != "" {
		serviceDate = bundleServiceDate
	}
	return serviceDate, publicEdgeCacheHashVersion(
		"bundle",
		strings.TrimSpace(metadata.Version),
		bundleServiceDate,
		strings.TrimSpace(metadata.SourceVersion),
		strings.TrimSpace(metadata.TransformVersion),
	)
}

func (s *Server) writePublicJSON(w http.ResponseWriter, status int, payload any, decision *publicEdgeCacheDecision) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")
	s.setNoIndexHeaders(w)
	if decision == nil || s.publicEdgeCache == nil {
		s.setNoStoreHeaders(w)
	} else {
		s.setPublicCacheHeadersWithTTL(w, s.publicEdgeCache.httpMaxAge())
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(redactPublicJSONPayload(payload))
}

func cloneURLWithCacheVersion(rawURL *url.URL, version string) string {
	if rawURL == nil {
		return ""
	}
	nextURL := *rawURL
	query := nextURL.Query()
	query.Set("cv", version)
	nextURL.RawQuery = query.Encode()
	return nextURL.String()
}
