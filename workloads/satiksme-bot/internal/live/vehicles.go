package live

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"satiksmebot/internal/model"
)

const VehicleFeedURL = "https://www.saraksti.lv/gpsdata.ashx?gps"

func FetchVehicles(ctx context.Context, client *http.Client, sourceURL string, catalog *model.Catalog, now time.Time) ([]model.LiveVehicle, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(sourceURL) == "" {
		sourceURL = VehicleFeedURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build live vehicles request: %w", err)
	}
	client.CloseIdleConnections()
	req.Close = true
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	if strings.Contains(sourceURL, "saraksti.lv/gpsdata.ashx") {
		req.Header.Set("Origin-Custom", "saraksti.lv")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch live vehicles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch live vehicles: upstream status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read live vehicles: %w", err)
	}
	return ParseVehicles(string(body), catalog, now), nil
}

func ParseVehicles(raw string, catalog *model.Catalog, now time.Time) []model.LiveVehicle {
	stopNames := model.StopNameLookup(catalog)

	current := now
	if current.IsZero() {
		current = time.Now()
	}
	currentSeconds := current.Hour()*3600 + current.Minute()*60 + current.Second()

	lines := strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n")
	out := make([]model.LiveVehicle, 0, len(lines))
	seen := make(map[string]struct{})
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		for len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
			parts = parts[:len(parts)-1]
		}
		if len(parts) < 9 {
			continue
		}

		mode := vehicleMode(parts[0])
		routeLabel := strings.TrimSpace(parts[1])
		if routeLabel == "" {
			continue
		}
		longitude, okLng := parseVehicleCoordinate(parts[2])
		latitude, okLat := parseVehicleCoordinate(parts[3])
		if !okLng || !okLat {
			continue
		}

		rawCode := strings.TrimSpace(parts[7])
		if rawCode == "" {
			rawCode = strings.TrimSpace(parts[6])
		}
		if rawCode == "" {
			rawCode = strings.TrimSpace(parts[1])
		}
		liveRowID := rawCode
		id := fmt.Sprintf("%s:%s:%s", mode, routeLabel, rawCode)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}

		direction := strings.TrimSpace(parts[8])
		stopID := ""
		if len(parts) > 9 {
			stopID = strings.TrimSpace(parts[9])
		}
		arrivalSeconds := 0
		if len(parts) > 10 {
			if delta, ok := parseOptionalInt(parts[10]); ok {
				arrivalSeconds = normalizeSeconds(currentSeconds + delta)
			}
		}

		heading, _ := parseOptionalInt(parts[5])
		out = append(out, model.LiveVehicle{
			ID:             id,
			VehicleCode:    rawCode,
			Mode:           mode,
			RouteLabel:     routeLabel,
			Direction:      direction,
			Destination:    "",
			Latitude:       latitude,
			Longitude:      longitude,
			UpdatedAt:      current,
			Heading:        heading,
			StopID:         stopID,
			StopName:       stopNames[stopID],
			ArrivalSeconds: arrivalSeconds,
			LowFloor:       strings.Contains(strings.ToUpper(strings.TrimSpace(parts[6])), "L"),
			LiveRowID:      liveRowID,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Mode != out[j].Mode {
			return out[i].Mode < out[j].Mode
		}
		if out[i].RouteLabel != out[j].RouteLabel {
			return out[i].RouteLabel < out[j].RouteLabel
		}
		if out[i].VehicleCode != out[j].VehicleCode {
			return out[i].VehicleCode < out[j].VehicleCode
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func PublicLiveVehicles(vehicles []model.LiveVehicle) []model.LiveVehicle {
	out := make([]model.LiveVehicle, 0, len(vehicles))
	for index, vehicle := range vehicles {
		out = append(out, PublicLiveVehicle(vehicle, index))
	}
	return out
}

func PublicLiveVehicle(vehicle model.LiveVehicle, index int) model.LiveVehicle {
	next := vehicle
	next.ID = publicVehicleID(next, index)
	next.VehicleCode = ""
	next.LiveRowID = ""
	next.Latitude = publicLiveCoordinate(next.Latitude)
	next.Longitude = publicLiveCoordinate(next.Longitude)
	if !next.UpdatedAt.IsZero() {
		next.UpdatedAt = next.UpdatedAt.UTC().Truncate(time.Second)
	}
	next.Incidents = publicLiveVehicleIncidents(next.Incidents)
	return next
}

func publicLiveVehicleIncidents(items []model.IncidentSummary) []model.IncidentSummary {
	if len(items) == 0 {
		return nil
	}
	out := make([]model.IncidentSummary, 0, len(items))
	for _, item := range items {
		next := item
		if next.Scope == "vehicle" {
			next.SubjectID = ""
		}
		if next.Vehicle != nil {
			vehicle := *next.Vehicle
			vehicle.ScopeKey = ""
			vehicle.LiveRowID = ""
			next.Vehicle = &vehicle
		}
		out = append(out, next)
	}
	return out
}

func publicVehicleID(vehicle model.LiveVehicle, index int) string {
	seed := publicVehicleStableSeed(vehicle)
	if seed == "" {
		seed = fmt.Sprintf(
			"fallback\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d",
			strings.ToLower(strings.TrimSpace(vehicle.Mode)),
			strings.TrimSpace(vehicle.RouteLabel),
			normalizeDirection(vehicle.Direction),
			strings.ToLower(strings.TrimSpace(vehicle.Destination)),
			strings.TrimSpace(vehicle.StopID),
			vehicle.ArrivalSeconds,
			index,
		)
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(seed))
	return fmt.Sprintf("vehicle:pub-%08x", hash.Sum32())
}

func publicVehicleStableSeed(vehicle model.LiveVehicle) string {
	mode := strings.ToLower(strings.TrimSpace(vehicle.Mode))
	routeLabel := strings.TrimSpace(vehicle.RouteLabel)
	if id := strings.TrimSpace(vehicle.ID); id != "" {
		return fmt.Sprintf("id\x00%s\x00%s\x00%s", mode, routeLabel, strings.ToLower(id))
	}
	if liveRowID := strings.TrimSpace(vehicle.LiveRowID); liveRowID != "" {
		return fmt.Sprintf("live-row\x00%s\x00%s\x00%s", mode, routeLabel, liveRowID)
	}
	if code := strings.TrimSpace(vehicle.VehicleCode); code != "" {
		return fmt.Sprintf("vehicle-code\x00%s\x00%s\x00%s", mode, routeLabel, code)
	}
	return ""
}

func publicLiveCoordinate(value float64) float64 {
	return math.Round(value*100000) / 100000
}

func ApplyVehicleSightingCounts(vehicles []model.LiveVehicle, sightings []model.PublicVehicleSighting) {
	for _, sighting := range sightings {
		index := bestVehicleMatch(vehicles, sighting)
		if index >= 0 {
			vehicles[index].SightingCount += 1
		}
	}
}

func ApplyVehicleIncidents(vehicles []model.LiveVehicle, incidents []model.IncidentSummary) {
	for index := range vehicles {
		vehicles[index].Incidents = nil
	}
	for _, incident := range incidents {
		if incident.Scope != "vehicle" || incident.Resolved || incident.Vehicle == nil {
			continue
		}
		index := bestVehicleMatch(vehicles, model.PublicVehicleSighting{
			StopID:           incident.Vehicle.StopID,
			Mode:             incident.Vehicle.Mode,
			RouteLabel:       incident.Vehicle.RouteLabel,
			Direction:        incident.Vehicle.Direction,
			Destination:      incident.Vehicle.Destination,
			DepartureSeconds: incident.Vehicle.DepartureSeconds,
			LiveRowID:        incident.Vehicle.LiveRowID,
		})
		if index >= 0 {
			vehicles[index].Incidents = append(vehicles[index].Incidents, incident)
		}
	}
	for index := range vehicles {
		sort.SliceStable(vehicles[index].Incidents, func(left, right int) bool {
			return vehicles[index].Incidents[left].LastReportAt.After(vehicles[index].Incidents[right].LastReportAt)
		})
	}
}

func bestVehicleMatch(vehicles []model.LiveVehicle, sighting model.PublicVehicleSighting) int {
	bestIndex := -1
	bestScore := int(^uint(0) >> 1)

	mode := strings.ToLower(strings.TrimSpace(sighting.Mode))
	routeLabel := strings.TrimSpace(sighting.RouteLabel)
	direction := normalizeDirection(sighting.Direction)
	for index, vehicle := range vehicles {
		if strings.ToLower(strings.TrimSpace(vehicle.Mode)) != mode {
			continue
		}
		if strings.TrimSpace(vehicle.RouteLabel) != routeLabel {
			continue
		}

		score := 0
		if stopID := strings.TrimSpace(sighting.StopID); stopID != "" {
			switch {
			case model.StopAliasEqual(vehicle.StopID, stopID):
			case vehicle.StopID == "":
				score += 40
			default:
				continue
			}
		}

		vehicleDirection := normalizeDirection(vehicle.Direction)
		switch {
		case direction == "" || vehicleDirection == "":
			score += 10
		case direction != vehicleDirection:
			continue
		}

		if vehicle.ArrivalSeconds > 0 && sighting.DepartureSeconds > 0 {
			score += secondsDistance(vehicle.ArrivalSeconds, sighting.DepartureSeconds)
		} else {
			score += 300
		}

		if bestIndex == -1 || score < bestScore {
			bestIndex = index
			bestScore = score
		}
	}
	return bestIndex
}

func vehicleMode(code string) string {
	switch strings.TrimSpace(code) {
	case "1":
		return "trol"
	case "3":
		return "tram"
	case "4":
		return "minibus"
	case "5":
		return "seasonalbus"
	case "6":
		return "suburbanbus"
	default:
		return "bus"
	}
}

func parseVehicleCoordinate(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return parsed / 1e6, true
}

func parseOptionalInt(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func normalizeSeconds(value int) int {
	for value < 0 {
		value += 24 * 3600
	}
	return value % (24 * 3600)
}

func secondsDistance(a, b int) int {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	day := 24 * 3600
	if diff > day/2 {
		diff = day - diff
	}
	return diff
}

func normalizeDirection(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, ">", "-"))
}
