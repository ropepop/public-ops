package catalog

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/runtime"
)

type Settings struct {
	StopsURL     string
	RoutesURL    string
	GTFSURL      string
	MirrorDir    string
	OutputPath   string
	RefreshAfter time.Duration
	HTTPClient   *http.Client
	RuntimeState *runtime.State
}

type Manager struct {
	settings Settings

	mu          sync.RWMutex
	current     *model.Catalog
	status      runtime.CatalogStatus
	stopIndex   map[string]model.Stop
	catalogJSON []byte
	catalogETag string
}

func NewManager(settings Settings) *Manager {
	client := settings.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if settings.RefreshAfter <= 0 {
		settings.RefreshAfter = 24 * time.Hour
	}
	settings.HTTPClient = client
	return &Manager{settings: settings}
}

func (m *Manager) Run(ctx context.Context) error {
	if _, err := m.LoadOrRefresh(ctx, false); err != nil {
		return err
	}
	ticker := time.NewTicker(m.settings.RefreshAfter)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, _ = m.Refresh(ctx, false)
		}
	}
}

func (m *Manager) LoadOrRefresh(ctx context.Context, force bool) (*model.Catalog, error) {
	catalog, err := m.Refresh(ctx, force)
	if err == nil {
		return catalog, nil
	}

	fallback, loadErr := LoadCatalog(m.settings.OutputPath)
	if loadErr != nil {
		return nil, err
	}
	status := runtime.CatalogStatus{
		Loaded:             true,
		LoadedFromFallback: true,
		GeneratedAt:        fallback.GeneratedAt.UTC(),
		LastRefreshAttempt: time.Now().UTC(),
		LastRefreshError:   err.Error(),
		StopCount:          len(fallback.Stops),
		RouteCount:         len(fallback.Routes),
	}
	if useErr := m.useCatalog(fallback, status); useErr != nil {
		return nil, useErr
	}
	return fallback, nil
}

func (m *Manager) Current() *model.Catalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

func (m *Manager) Status() runtime.CatalogStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) FindStop(stopID string) (model.Stop, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stop, ok := m.stopIndex[strings.TrimSpace(stopID)]
	return stop, ok
}

func (m *Manager) CatalogJSON() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalogJSON
}

func (m *Manager) CatalogETag() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalogETag
}

func (m *Manager) Refresh(ctx context.Context, force bool) (*model.Catalog, error) {
	attemptAt := time.Now().UTC()
	stopsPath := filepath.Join(m.settings.MirrorDir, "stops.txt")
	routesPath := filepath.Join(m.settings.MirrorDir, "routes.txt")
	gtfsPath := filepath.Join(m.settings.MirrorDir, "gtfs.zip")

	for _, dir := range []string{m.settings.MirrorDir, filepath.Dir(m.settings.OutputPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.recordRefreshError(attemptAt, err)
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	if err := mirrorFile(ctx, m.settings.HTTPClient, m.settings.StopsURL, stopsPath, m.settings.RefreshAfter, force); err != nil {
		m.recordRefreshError(attemptAt, err)
		return nil, fmt.Errorf("mirror stops: %w", err)
	}
	if err := mirrorFile(ctx, m.settings.HTTPClient, m.settings.RoutesURL, routesPath, m.settings.RefreshAfter, force); err != nil {
		m.recordRefreshError(attemptAt, err)
		return nil, fmt.Errorf("mirror routes: %w", err)
	}
	if strings.TrimSpace(m.settings.GTFSURL) != "" {
		if err := mirrorFile(ctx, m.settings.HTTPClient, m.settings.GTFSURL, gtfsPath, m.settings.RefreshAfter, force); err != nil {
			m.recordRefreshError(attemptAt, err)
			return nil, fmt.Errorf("mirror gtfs: %w", err)
		}
	}

	catalog, err := BuildCatalogFromPaths(stopsPath, routesPath, gtfsPath)
	if err != nil {
		m.recordRefreshError(attemptAt, err)
		return nil, err
	}
	if err := writeCatalog(m.settings.OutputPath, catalog); err != nil {
		m.recordRefreshError(attemptAt, err)
		return nil, err
	}

	if err := m.useCatalog(catalog, runtime.CatalogStatus{
		Loaded:             true,
		LoadedFromFallback: false,
		GeneratedAt:        catalog.GeneratedAt.UTC(),
		LastRefreshAttempt: attemptAt,
		LastRefreshSuccess: attemptAt,
		StopCount:          len(catalog.Stops),
		RouteCount:         len(catalog.Routes),
	}); err != nil {
		m.recordRefreshError(attemptAt, err)
		return nil, err
	}
	return catalog, nil
}

func (m *Manager) recordRefreshError(attemptAt time.Time, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.status
	status.LastRefreshAttempt = attemptAt
	status.LastRefreshError = err.Error()
	if m.current != nil {
		status.Loaded = true
		status.GeneratedAt = m.current.GeneratedAt.UTC()
		status.StopCount = len(m.current.Stops)
		status.RouteCount = len(m.current.Routes)
	}
	m.status = status
	if m.settings.RuntimeState != nil {
		m.settings.RuntimeState.UpdateCatalog(status)
	}
}

func LoadCatalog(path string) (*model.Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var catalog model.Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (m *Manager) useCatalog(catalog *model.Catalog, status runtime.CatalogStatus) error {
	stopIndex, catalogJSON, catalogETag, err := buildCatalogRuntimeSnapshot(catalog)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = catalog
	m.stopIndex = stopIndex
	m.catalogJSON = catalogJSON
	m.catalogETag = catalogETag
	m.status = status
	if m.settings.RuntimeState != nil {
		m.settings.RuntimeState.UpdateCatalog(status)
	}
	return nil
}

func buildCatalogRuntimeSnapshot(catalog *model.Catalog) (map[string]model.Stop, []byte, string, error) {
	if catalog == nil {
		return map[string]model.Stop{}, nil, "", nil
	}

	stopIndex := make(map[string]model.Stop, len(catalog.Stops))
	for _, stop := range catalog.Stops {
		stopIndex[stop.ID] = stop
	}

	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal catalog cache: %w", err)
	}
	sum := sha256.Sum256(catalogJSON)
	return stopIndex, catalogJSON, `"` + hex.EncodeToString(sum[:]) + `"`, nil
}

func BuildCatalogFromPaths(stopsPath, routesPath, gtfsPath string) (*model.Catalog, error) {
	stopsRaw, err := os.ReadFile(stopsPath)
	if err != nil {
		return nil, fmt.Errorf("read stops: %w", err)
	}
	routesRaw, err := os.ReadFile(routesPath)
	if err != nil {
		return nil, fmt.Errorf("read routes: %w", err)
	}
	var gtfsRaw []byte
	if strings.TrimSpace(gtfsPath) != "" {
		gtfsRaw, _ = os.ReadFile(gtfsPath)
	}
	return BuildCatalog(stopsRaw, routesRaw, gtfsRaw)
}

func BuildCatalog(stopsRaw, routesRaw, gtfsZip []byte) (*model.Catalog, error) {
	stopRows, err := parseStopsSource(bytes.NewReader(stopsRaw))
	if err != nil {
		return nil, err
	}
	routeRows, err := parseRoutesSource(bytes.NewReader(routesRaw))
	if err != nil {
		return nil, err
	}
	gtfsStops, err := parseGTFSStops(gtfsZip)
	if err != nil {
		return nil, err
	}
	gtfsStopRoutes, gtfsRoutes, err := parseGTFSRouteMappings(gtfsZip)
	if err != nil {
		return nil, err
	}

	stopsByID := make(map[string]*model.Stop, len(stopRows))
	for _, row := range stopRows {
		stop := model.Stop{
			ID:            row.ID,
			LiveID:        row.LiveID,
			Name:          row.Name,
			Latitude:      row.Latitude,
			Longitude:     row.Longitude,
			NearbyStopIDs: cloneStrings(row.NearbyStopIDs),
		}
		if gtfs, ok := gtfsStops[row.ID]; ok {
			if looksInvalidStopName(stop.Name) {
				stop.Name = firstNonEmpty(gtfs.Name, stop.Name)
			}
			if stop.Latitude == 0 {
				stop.Latitude = gtfs.Latitude
			}
			if stop.Longitude == 0 {
				stop.Longitude = gtfs.Longitude
			}
		}
		if strings.TrimSpace(stop.Name) == "" {
			stop.Name = "Stop " + row.ID
		}
		stopsByID[row.ID] = &stop
	}
	for id, gtfs := range gtfsStops {
		if _, ok := stopsByID[id]; ok {
			continue
		}
		stopsByID[id] = &model.Stop{
			ID:        id,
			LiveID:    "",
			Name:      gtfs.Name,
			Latitude:  gtfs.Latitude,
			Longitude: gtfs.Longitude,
		}
	}

	modeByStop := map[string]map[string]struct{}{}
	routesByStop := map[string]map[string]struct{}{}
	routesOut := make([]model.Route, 0, len(routeRows))
	routeRegistry := map[string]model.Route{}
	matchedGTFSStopRoutes := map[string][]sourceRoute{}
	for _, route := range routeRows {
		item := model.Route{
			Label:   route.Label,
			Mode:    route.Mode,
			Name:    route.Name,
			StopIDs: cloneStrings(route.StopIDs),
		}
		registerRoute(routeRegistry, item)
		for _, stopID := range route.StopIDs {
			if _, ok := stopsByID[stopID]; !ok {
				continue
			}
			if _, ok := modeByStop[stopID]; !ok {
				modeByStop[stopID] = map[string]struct{}{}
			}
			if _, ok := routesByStop[stopID]; !ok {
				routesByStop[stopID] = map[string]struct{}{}
			}
			modeByStop[stopID][route.Mode] = struct{}{}
			routesByStop[stopID][route.Label] = struct{}{}
		}
	}
	for stopID, stopRoutes := range gtfsStopRoutes {
		if _, ok := stopsByID[stopID]; ok {
			matchedGTFSStopRoutes[stopID] = stopRoutes
		}
	}
	for stopID, stop := range stopsByID {
		if len(matchedGTFSStopRoutes[stopID]) > 0 {
			continue
		}
		matched := nearestGTFSRoutes(*stop, gtfsStops, gtfsStopRoutes, 120)
		if len(matched) > 0 {
			matchedGTFSStopRoutes[stopID] = matched
		}
	}
	for stopID, stopRoutes := range matchedGTFSStopRoutes {
		for _, route := range stopRoutes {
			if _, ok := modeByStop[stopID]; !ok {
				modeByStop[stopID] = map[string]struct{}{}
			}
			if _, ok := routesByStop[stopID]; !ok {
				routesByStop[stopID] = map[string]struct{}{}
			}
			modeByStop[stopID][route.Mode] = struct{}{}
			routesByStop[stopID][route.Label] = struct{}{}
		}
	}
	for _, route := range gtfsRoutes {
		registerRoute(routeRegistry, model.Route{
			Label:   route.Label,
			Mode:    route.Mode,
			Name:    route.Name,
			StopIDs: cloneStrings(route.StopIDs),
		})
	}
	for _, route := range routeRegistry {
		routesOut = append(routesOut, route)
	}

	stopsOut := make([]model.Stop, 0, len(stopsByID))
	for _, stop := range stopsByID {
		stop.Modes = sortedKeys(modeByStop[stop.ID])
		stop.RouteLabels = sortedKeys(routesByStop[stop.ID])
		stopsOut = append(stopsOut, *stop)
	}
	sort.Slice(stopsOut, func(i, j int) bool {
		if stopsOut[i].Name == stopsOut[j].Name {
			return stopsOut[i].ID < stopsOut[j].ID
		}
		return stopsOut[i].Name < stopsOut[j].Name
	})
	sort.Slice(routesOut, func(i, j int) bool {
		if routesOut[i].Mode == routesOut[j].Mode {
			return routesOut[i].Label < routesOut[j].Label
		}
		return routesOut[i].Mode < routesOut[j].Mode
	})

	return &model.Catalog{
		GeneratedAt: time.Now().UTC(),
		Stops:       stopsOut,
		Routes:      routesOut,
	}, nil
}

type sourceStop struct {
	ID            string
	LiveID        string
	Name          string
	Latitude      float64
	Longitude     float64
	NearbyStopIDs []string
}

type sourceRoute struct {
	Label   string
	Mode    string
	Name    string
	StopIDs []string
}

type gtfsStop struct {
	Name      string
	Latitude  float64
	Longitude float64
}

func parseStopsSource(r io.Reader) ([]sourceStop, error) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse stops source: %w", err)
	}
	out := make([]sourceStop, 0, len(rows))
	for i, row := range rows {
		if i == 0 || len(row) < 1 {
			continue
		}
		id := strings.TrimSpace(rowValue(row, 0))
		if id == "" {
			continue
		}
		out = append(out, sourceStop{
			ID:            id,
			LiveID:        strings.TrimSpace(rowValue(row, 1)),
			Name:          strings.TrimSpace(rowValue(row, 6)),
			Latitude:      scaledStopCoordinate(rowValue(row, 3)),
			Longitude:     scaledStopCoordinate(rowValue(row, 4)),
			NearbyStopIDs: splitCSVish(rowValue(row, 5)),
		})
	}
	return out, nil
}

func parseRoutesSource(r io.Reader) ([]sourceRoute, error) {
	scanner := bufio.NewScanner(r)
	out := make([]sourceRoute, 0, 512)
	lastLabel := ""
	lastMode := ""
	lastName := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\uFEFF"))
		if line == "" {
			continue
		}
		row := strings.Split(line, ";")
		if lineNo == 1 || len(row) < 15 {
			continue
		}
		label := strings.TrimSpace(rowValue(row, 0))
		if label == "" {
			label = lastLabel
		} else {
			lastLabel = label
		}
		mode := strings.TrimSpace(rowValue(row, 3))
		if mode == "" {
			mode = lastMode
		} else {
			lastMode = mode
		}
		name := strings.TrimSpace(rowValue(row, 10))
		if name == "" {
			name = lastName
		} else {
			lastName = name
		}
		stopIDs := splitCSVish(rowValue(row, 14))
		if len(stopIDs) == 0 {
			stopIDs = splitCSVish(rowValue(row, 13))
		}
		if label == "" || mode == "" || name == "" || len(stopIDs) == 0 {
			continue
		}
		out = append(out, sourceRoute{
			Label:   label,
			Mode:    mode,
			Name:    name,
			StopIDs: stopIDs,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse routes source: %w", err)
	}
	return out, nil
}

func parseGTFSStops(zipBytes []byte) (map[string]gtfsStop, error) {
	if len(zipBytes) == 0 {
		return map[string]gtfsStop{}, nil
	}
	readerAt := bytes.NewReader(zipBytes)
	zipReader, err := zip.NewReader(readerAt, int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open gtfs zip: %w", err)
	}
	var stopsFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "stops.txt" {
			stopsFile = file
			break
		}
	}
	if stopsFile == nil {
		return map[string]gtfsStop{}, nil
	}
	rc, err := stopsFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open gtfs stops.txt: %w", err)
	}
	defer rc.Close()
	reader := csv.NewReader(rc)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse gtfs stops: %w", err)
	}
	if len(rows) == 0 {
		return map[string]gtfsStop{}, nil
	}
	header := indexHeader(rows[0])
	out := make(map[string]gtfsStop, len(rows)-1)
	for _, row := range rows[1:] {
		id := strings.TrimSpace(rowValueByHeader(row, header, "stop_id"))
		if id == "" {
			continue
		}
		lat, _ := strconv.ParseFloat(strings.TrimSpace(rowValueByHeader(row, header, "stop_lat")), 64)
		lng, _ := strconv.ParseFloat(strings.TrimSpace(rowValueByHeader(row, header, "stop_lon")), 64)
		out[id] = gtfsStop{
			Name:      strings.TrimSpace(rowValueByHeader(row, header, "stop_name")),
			Latitude:  lat,
			Longitude: lng,
		}
	}
	return out, nil
}

func parseGTFSRouteMappings(zipBytes []byte) (map[string][]sourceRoute, []sourceRoute, error) {
	if len(zipBytes) == 0 {
		return map[string][]sourceRoute{}, nil, nil
	}
	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, nil, fmt.Errorf("open gtfs zip for route mappings: %w", err)
	}
	routesFile := findZipFile(zipReader, "routes.txt")
	tripsFile := findZipFile(zipReader, "trips.txt")
	stopTimesFile := findZipFile(zipReader, "stop_times.txt")
	if routesFile == nil || tripsFile == nil || stopTimesFile == nil {
		return map[string][]sourceRoute{}, nil, nil
	}

	routeInfoByID, err := parseGTFSRoutes(routesFile)
	if err != nil {
		return nil, nil, err
	}
	tripRouteByID, err := parseGTFSTrips(tripsFile, routeInfoByID)
	if err != nil {
		return nil, nil, err
	}

	rc, err := stopTimesFile.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("open gtfs stop_times.txt: %w", err)
	}
	defer rc.Close()

	reader := csv.NewReader(rc)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("parse gtfs stop_times: %w", err)
	}
	if len(rows) == 0 {
		return map[string][]sourceRoute{}, nil, nil
	}
	header := indexHeader(rows[0])
	stopRoutes := map[string]map[string]sourceRoute{}
	routeStops := map[string]map[string]struct{}{}
	for _, row := range rows[1:] {
		stopID := strings.TrimSpace(rowValueByHeader(row, header, "stop_id"))
		tripID := strings.TrimSpace(rowValueByHeader(row, header, "trip_id"))
		route, ok := tripRouteByID[tripID]
		if !ok || stopID == "" {
			continue
		}
		key := routeKey(route.Label, route.Mode, route.Name)
		if _, ok := stopRoutes[stopID]; !ok {
			stopRoutes[stopID] = map[string]sourceRoute{}
		}
		stopRoutes[stopID][key] = sourceRoute{
			Label: route.Label,
			Mode:  route.Mode,
			Name:  route.Name,
		}
		if _, ok := routeStops[key]; !ok {
			routeStops[key] = map[string]struct{}{}
		}
		routeStops[key][stopID] = struct{}{}
	}

	stopRouteList := make(map[string][]sourceRoute, len(stopRoutes))
	for stopID, items := range stopRoutes {
		keys := make([]string, 0, len(items))
		for key := range items {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		list := make([]sourceRoute, 0, len(keys))
		for _, key := range keys {
			list = append(list, items[key])
		}
		stopRouteList[stopID] = list
	}
	routes := make([]sourceRoute, 0, len(routeStops))
	for key, stops := range routeStops {
		base := sourceRoute{}
		for _, list := range stopRouteList {
			found := false
			for _, item := range list {
				if routeKey(item.Label, item.Mode, item.Name) == key {
					base = item
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		base.StopIDs = sortedKeys(stops)
		routes = append(routes, base)
	}
	return stopRouteList, routes, nil
}

func mirrorFile(ctx context.Context, client *http.Client, sourceURL, dst string, maxAge time.Duration, force bool) error {
	if !force {
		if info, err := os.Stat(dst); err == nil && time.Since(info.ModTime()) < maxAge {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: status %d", sourceURL, resp.StatusCode)
	}

	tmpPath := dst + ".tmp"
	fh, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fh, resp.Body); err != nil {
		_ = fh.Close()
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}

func writeCatalog(path string, catalog *model.Catalog) error {
	raw, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	return nil
}

func rowValue(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

func rowValueByHeader(row []string, header map[string]int, key string) string {
	index, ok := header[key]
	if !ok || index >= len(row) {
		return ""
	}
	return row[index]
}

func indexHeader(header []string) map[string]int {
	out := make(map[string]int, len(header))
	for i, cell := range header {
		normalized := strings.TrimSpace(strings.TrimPrefix(cell, "\uFEFF"))
		out[normalized] = i
	}
	return out
}

func splitCSVish(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func scaledStopCoordinate(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	if value > 1000 {
		return value / 100000.0
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func looksInvalidStopName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "0" {
		return true
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sortedKeys(items map[string]struct{}) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for item := range items {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func findZipFile(reader *zip.Reader, name string) *zip.File {
	for _, file := range reader.File {
		if file.Name == name {
			return file
		}
	}
	return nil
}

func parseGTFSRoutes(file *zip.File) (map[string]sourceRoute, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open gtfs routes.txt: %w", err)
	}
	defer rc.Close()

	reader := csv.NewReader(rc)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse gtfs routes: %w", err)
	}
	if len(rows) == 0 {
		return map[string]sourceRoute{}, nil
	}
	header := indexHeader(rows[0])
	out := make(map[string]sourceRoute, len(rows)-1)
	for _, row := range rows[1:] {
		id := strings.TrimSpace(rowValueByHeader(row, header, "route_id"))
		if id == "" {
			continue
		}
		label := firstNonEmpty(rowValueByHeader(row, header, "route_short_name"), rowValueByHeader(row, header, "route_long_name"), id)
		name := firstNonEmpty(rowValueByHeader(row, header, "route_long_name"), label)
		out[id] = sourceRoute{
			Label: label,
			Mode:  normalizeGTFSMode(rowValueByHeader(row, header, "route_type")),
			Name:  name,
		}
	}
	return out, nil
}

func parseGTFSTrips(file *zip.File, routeInfoByID map[string]sourceRoute) (map[string]sourceRoute, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open gtfs trips.txt: %w", err)
	}
	defer rc.Close()

	reader := csv.NewReader(rc)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse gtfs trips: %w", err)
	}
	if len(rows) == 0 {
		return map[string]sourceRoute{}, nil
	}
	header := indexHeader(rows[0])
	out := make(map[string]sourceRoute, len(rows)-1)
	for _, row := range rows[1:] {
		tripID := strings.TrimSpace(rowValueByHeader(row, header, "trip_id"))
		routeID := strings.TrimSpace(rowValueByHeader(row, header, "route_id"))
		if tripID == "" || routeID == "" {
			continue
		}
		route, ok := routeInfoByID[routeID]
		if !ok {
			continue
		}
		out[tripID] = route
	}
	return out, nil
}

func normalizeGTFSMode(raw string) string {
	switch strings.TrimSpace(raw) {
	case "0":
		return "tram"
	case "3":
		return "bus"
	case "11":
		return "trol"
	case "800":
		return "trol"
	case "900":
		return "tram"
	default:
		return "unknown"
	}
}

func registerRoute(registry map[string]model.Route, route model.Route) {
	key := routeKey(route.Label, route.Mode, route.Name)
	if existing, ok := registry[key]; ok {
		if len(existing.StopIDs) == 0 && len(route.StopIDs) > 0 {
			existing.StopIDs = cloneStrings(route.StopIDs)
			registry[key] = existing
		}
		return
	}
	registry[key] = route
}

func routeKey(label, mode, name string) string {
	return strings.TrimSpace(mode) + "|" + strings.TrimSpace(label) + "|" + strings.TrimSpace(name)
}

func nearestGTFSRoutes(stop model.Stop, gtfsStops map[string]gtfsStop, gtfsStopRoutes map[string][]sourceRoute, maxDistanceMeters float64) []sourceRoute {
	if stop.Latitude == 0 || stop.Longitude == 0 {
		return nil
	}
	bestDistance := maxDistanceMeters
	bestID := ""
	for gtfsID, gtfsStop := range gtfsStops {
		routes := gtfsStopRoutes[gtfsID]
		if len(routes) == 0 || gtfsStop.Latitude == 0 || gtfsStop.Longitude == 0 {
			continue
		}
		distance := approximateDistanceMeters(stop.Latitude, stop.Longitude, gtfsStop.Latitude, gtfsStop.Longitude)
		if distance < bestDistance {
			bestDistance = distance
			bestID = gtfsID
		}
	}
	if bestID == "" {
		return nil
	}
	return gtfsStopRoutes[bestID]
}

func approximateDistanceMeters(lat1, lng1, lat2, lng2 float64) float64 {
	latMeters := (lat1 - lat2) * 111000
	lngMeters := (lng1 - lng2) * 64000
	if latMeters < 0 {
		latMeters = -latMeters
	}
	if lngMeters < 0 {
		lngMeters = -lngMeters
	}
	return latMeters + lngMeters
}
