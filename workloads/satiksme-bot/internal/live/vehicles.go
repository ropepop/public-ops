package live

import (
	"context"
	"fmt"
	"io"
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
