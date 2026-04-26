package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"telegramtrainapp/internal/spacetime"
)

type scheduleTiming struct {
	ServiceDate      string  `json:"serviceDate"`
	DurationMs       float64 `json:"durationMs"`
	TripsReturned    int     `json:"tripsReturned"`
	StationsReturned int     `json:"stationsReturned"`
	WindowTrips      int     `json:"windowTrips"`
}

type dashboardFanoutTiming struct {
	Limit              int     `json:"limit"`
	TrainCount         int     `json:"trainCount"`
	TripCalls          int     `json:"tripCalls"`
	ActivityCalls      int     `json:"activityCalls"`
	TotalCalls         int     `json:"totalCalls"`
	ActivitiesReturned int     `json:"activitiesReturned"`
	DurationMs         float64 `json:"durationMs"`
	AvgCallMs          float64 `json:"avgCallMs"`
}

type dashboardProcedureTiming struct {
	Limit          int     `json:"limit"`
	TrainCount     int     `json:"trainCount"`
	TrainsReturned int     `json:"trainsReturned"`
	PayloadBytes   int     `json:"payloadBytes"`
	DurationMs     float64 `json:"durationMs"`
}

type profileResult struct {
	GeneratedAtUTC      string                     `json:"generatedAtUtc"`
	NowLocal            string                     `json:"nowLocal"`
	Timezone            string                     `json:"timezone"`
	Host                string                     `json:"host"`
	Database            string                     `json:"database"`
	Schedule            scheduleTiming             `json:"schedule"`
	DashboardFanout     []dashboardFanoutTiming    `json:"dashboardFanout"`
	DashboardProcedures []dashboardProcedureTiming `json:"dashboardProcedures"`
}

func main() {
	hostFlag := flag.String("spacetime-host", strings.TrimSpace(os.Getenv("TRAIN_RUNTIME_SPACETIME_HOST")), "SpacetimeDB host")
	databaseFlag := flag.String("spacetime-database", strings.TrimSpace(os.Getenv("TRAIN_RUNTIME_SPACETIME_DATABASE")), "SpacetimeDB database identity or name")
	issuerFlag := flag.String("spacetime-issuer", firstEnv("TRAIN_RUNTIME_SPACETIME_OIDC_ISSUER", "TRAIN_WEB_SPACETIME_OIDC_ISSUER"), "OIDC issuer used to sign runtime service tokens")
	audienceFlag := flag.String("spacetime-audience", firstEnv("TRAIN_RUNTIME_SPACETIME_OIDC_AUDIENCE", "TRAIN_WEB_SPACETIME_OIDC_AUDIENCE"), "OIDC audience for runtime service tokens")
	keyFileFlag := flag.String("spacetime-key-file", firstEnv("TRAIN_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", "TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE"), "RSA private key used to sign runtime service tokens")
	serviceSubjectFlag := flag.String("service-subject", firstEnv("TRAIN_RUNTIME_SPACETIME_SERVICE_SUBJECT"), "service token subject")
	serviceRolesFlag := flag.String("service-roles", firstEnv("TRAIN_RUNTIME_SPACETIME_SERVICE_ROLES"), "comma-separated service roles")
	tokenTTLFlag := flag.Int("token-ttl-sec", envOrInt("TRAIN_RUNTIME_SPACETIME_TOKEN_TTL_SEC", 900), "service token lifetime in seconds")
	httpTimeoutFlag := flag.Int("http-timeout-sec", envOrInt("HTTP_TIMEOUT_SEC", 45), "HTTP timeout in seconds")
	serviceDateFlag := flag.String("service-date", "", "service date to profile in YYYY-MM-DD (defaults to local today)")
	timezoneFlag := flag.String("timezone", envOr("TZ", "Europe/Riga"), "timezone used to match the dashboard window")
	limitsFlag := flag.String("limits", "1,5,10,20,60", "comma-separated dashboard limits to profile")
	flag.Parse()

	if strings.TrimSpace(*hostFlag) == "" {
		log.Fatal("spacetime host is required")
	}
	if strings.TrimSpace(*databaseFlag) == "" {
		log.Fatal("spacetime database is required")
	}
	if strings.TrimSpace(*keyFileFlag) == "" {
		log.Fatal("spacetime key file is required")
	}

	loc, err := time.LoadLocation(strings.TrimSpace(*timezoneFlag))
	if err != nil {
		log.Fatalf("invalid timezone: %v", err)
	}
	nowLocal := time.Now().In(loc)
	serviceDate := strings.TrimSpace(*serviceDateFlag)
	if serviceDate == "" {
		serviceDate = nowLocal.Format("2006-01-02")
	}

	limits, err := parseLimits(*limitsFlag)
	if err != nil {
		log.Fatalf("parse limits: %v", err)
	}

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              *hostFlag,
		Database:          *databaseFlag,
		Issuer:            *issuerFlag,
		Audience:          *audienceFlag,
		JWTPrivateKeyFile: *keyFileFlag,
		ServiceSubject:    firstNonEmpty(*serviceSubjectFlag, "service:train-bot"),
		ServiceRoles:      parseCSV(firstNonEmpty(*serviceRolesFlag, "train_service")),
		TokenTTL:          time.Duration(*tokenTTLFlag) * time.Second,
		HTTPTimeout:       time.Duration(*httpTimeoutFlag) * time.Second,
	})
	if err != nil {
		log.Fatalf("configure spacetime syncer: %v", err)
	}

	ctx := context.Background()
	profile := profileResult{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		NowLocal:       nowLocal.Format(time.RFC3339),
		Timezone:       loc.String(),
		Host:           strings.TrimSpace(*hostFlag),
		Database:       strings.TrimSpace(*databaseFlag),
	}

	scheduleStart := time.Now()
	serviceDay, trips, err := syncer.ServiceGetSchedule(ctx, serviceDate)
	if err != nil {
		log.Fatalf("service_get_schedule: %v", err)
	}
	windowTrips, err := dashboardWindowTrips(trips, nowLocal, loc)
	if err != nil {
		log.Fatalf("match dashboard window: %v", err)
	}
	profile.Schedule = scheduleTiming{
		ServiceDate:      serviceDate,
		DurationMs:       durationMs(time.Since(scheduleStart)),
		TripsReturned:    len(trips),
		StationsReturned: stationCount(serviceDay),
		WindowTrips:      len(windowTrips),
	}

	for _, limit := range limits {
		trainCount := limit
		if trainCount > len(windowTrips) {
			trainCount = len(windowTrips)
		}
		if trainCount <= 0 {
			continue
		}
		subset := windowTrips[:trainCount]

		procedureStart := time.Now()
		payload, err := syncer.CallProcedure(ctx, "get_public_dashboard", []any{trainCount})
		if err != nil {
			log.Fatalf("get_public_dashboard limit=%d: %v", trainCount, err)
		}
		trainsReturned, payloadBytes := dashboardPayloadStats(payload)
		profile.DashboardProcedures = append(profile.DashboardProcedures, dashboardProcedureTiming{
			Limit:          limit,
			TrainCount:     trainCount,
			TrainsReturned: trainsReturned,
			PayloadBytes:   payloadBytes,
			DurationMs:     durationMs(time.Since(procedureStart)),
		})

		fanoutStart := time.Now()
		tripCalls := 0
		activityCalls := 0
		activitiesReturned := 0
		for _, trip := range subset {
			for range 2 {
				tripRow, err := syncer.ServiceGetTrip(ctx, strings.TrimSpace(trip.ID))
				if err != nil {
					log.Fatalf("service_get_trip train=%s limit=%d: %v", trip.ID, trainCount, err)
				}
				tripCalls++
				filter := spacetime.ListActivitiesFilter{
					ScopeType: "train",
					SubjectID: strings.TrimSpace(trip.ID),
				}
				if tripRow != nil {
					filter.ServiceDate = strings.TrimSpace(tripRow.ServiceDate)
				}
				items, err := syncer.ServiceListActivities(ctx, filter)
				if err != nil {
					log.Fatalf("service_list_activities train=%s limit=%d: %v", trip.ID, trainCount, err)
				}
				activityCalls++
				activitiesReturned += len(items)
			}
		}
		totalDuration := time.Since(fanoutStart)
		totalCalls := tripCalls + activityCalls
		profile.DashboardFanout = append(profile.DashboardFanout, dashboardFanoutTiming{
			Limit:              limit,
			TrainCount:         trainCount,
			TripCalls:          tripCalls,
			ActivityCalls:      activityCalls,
			TotalCalls:         totalCalls,
			ActivitiesReturned: activitiesReturned,
			DurationMs:         durationMs(totalDuration),
			AvgCallMs:          durationMs(totalDuration) / float64(totalCalls),
		})
	}

	body, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		log.Fatalf("encode profile: %v", err)
	}
	fmt.Println(string(body))
}

func dashboardWindowTrips(trips []spacetime.TrainbotTripRow, nowLocal time.Time, loc *time.Location) ([]spacetime.TrainbotTripRow, error) {
	start := nowLocal.Add(-30 * time.Minute).UTC()
	end := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 23, 59, 59, 0, loc).UTC()
	out := make([]spacetime.TrainbotTripRow, 0, len(trips))
	for _, trip := range trips {
		departureAt, err := time.Parse(time.RFC3339, strings.TrimSpace(trip.DepartureAt))
		if err != nil {
			return nil, fmt.Errorf("parse departure %s: %w", trip.ID, err)
		}
		if departureAt.Before(start) || departureAt.After(end) {
			continue
		}
		out = append(out, trip)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DepartureAt < out[j].DepartureAt
	})
	return out, nil
}

func stationCount(day *spacetime.TrainbotServiceDayRow) int {
	if day == nil {
		return 0
	}
	return len(day.Stations)
}

func dashboardPayloadStats(payload any) (int, int) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, 0
	}
	decoded := struct {
		Trains []any `json:"trains"`
	}{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return 0, len(body)
	}
	return len(decoded.Trains), len(body)
}

func parseLimits(raw string) ([]int, error) {
	items := strings.Split(strings.TrimSpace(raw), ",")
	out := make([]int, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid limit %q", trimmed)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("limit must be positive, got %d", parsed)
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one limit is required")
	}
	sort.Ints(out)
	return out, nil
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func parseCSV(raw string) []string {
	items := strings.Split(raw, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envOr(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
