package chatanalyzer

import (
	"context"
	"time"

	"satiksmebot/internal/model"
)

const MinimumProcessInterval = 5 * time.Minute

const (
	ActionSighting     = "sighting"
	ActionNotice       = "notice"
	ActionCleared      = "cleared"
	ActionConfirmation = "confirmation"
	ActionDenial       = "denial"
	ActionIgnore       = "ignore"

	TargetStop     = "stop"
	TargetVehicle  = "vehicle"
	TargetArea     = "area"
	TargetIncident = "incident"
	TargetNone     = "none"
)

type CatalogProvider interface {
	Current() *model.Catalog
}

type CollectionResult struct {
	Messages             []model.ChatAnalyzerMessage
	CheckpointMessageIDs map[string]int64
	SkippedStale         int
}

type Collector interface {
	Collect(ctx context.Context) (CollectionResult, error)
}

type Analyzer interface {
	Analyze(ctx context.Context, item model.ChatAnalyzerMessage, candidates CandidateContext) (Decision, string, error)
}

type BatchAnalyzer interface {
	AnalyzeBatch(ctx context.Context, items []BatchItem, activeIncidents []model.IncidentSummary) (BatchDecision, string, string, error)
}

type LocationReasoningAnalyzer interface {
	DeduceLocations(ctx context.Context, items []BatchItem, activeIncidents []model.IncidentSummary, initial BatchDecision, recheckMessageIDs []int64) (BatchDecision, string, string, error)
}

type ReportDumper interface {
	EnqueueStop(stop model.Stop, sighting *model.StopSighting)
	EnqueueVehicle(sighting *model.VehicleSighting)
}

type LiveVehicleFetcher func(ctx context.Context, catalog *model.Catalog, now time.Time) ([]model.LiveVehicle, error)
type ActiveIncidentFetcher func(ctx context.Context, catalog *model.Catalog, now time.Time) ([]model.IncidentSummary, error)

type Decision struct {
	Action     string  `json:"action"`
	TargetType string  `json:"targetType"`
	TargetID   string  `json:"targetId"`
	Confidence float64 `json:"confidence"`
	Language   string  `json:"language,omitempty"`
	Reason     string  `json:"reason,omitempty"`
}

type StopCandidate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Modes       []string `json:"modes,omitempty"`
	RouteLabels []string `json:"routeLabels,omitempty"`
	Latitude    float64  `json:"latitude,omitempty"`
	Longitude   float64  `json:"longitude,omitempty"`
	Score       int      `json:"score,omitempty"`
}

type VehicleCandidate struct {
	ID               string  `json:"id"`
	Mode             string  `json:"mode"`
	RouteLabel       string  `json:"routeLabel"`
	Direction        string  `json:"direction,omitempty"`
	Destination      string  `json:"destination,omitempty"`
	StopID           string  `json:"stopId,omitempty"`
	StopName         string  `json:"stopName,omitempty"`
	DepartureSeconds int     `json:"departureSeconds,omitempty"`
	LiveRowID        string  `json:"liveRowId,omitempty"`
	Latitude         float64 `json:"latitude,omitempty"`
	Longitude        float64 `json:"longitude,omitempty"`
	MatchedStopID    string  `json:"matchedStopId,omitempty"`
	MatchedStopName  string  `json:"matchedStopName,omitempty"`
	DistanceMeters   int     `json:"distanceMeters,omitempty"`
	Score            int     `json:"score,omitempty"`
}

type IncidentCandidate struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	SubjectName string `json:"subjectName"`
	SubjectID   string `json:"subjectId,omitempty"`
	StopID      string `json:"stopId,omitempty"`
	Score       int    `json:"score,omitempty"`
}

type AreaCandidate struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Latitude     float64  `json:"latitude"`
	Longitude    float64  `json:"longitude"`
	RadiusMeters int      `json:"radiusMeters"`
	Description  string   `json:"description"`
	Anchors      []string `json:"anchors,omitempty"`
	Score        int      `json:"score,omitempty"`
}

type CandidateContext struct {
	Stops     []StopCandidate     `json:"stops"`
	Vehicles  []VehicleCandidate  `json:"vehicles"`
	Areas     []AreaCandidate     `json:"areas,omitempty"`
	Incidents []IncidentCandidate `json:"incidents"`
}

type BatchItem struct {
	Message       model.ChatAnalyzerMessage
	Candidates    CandidateContext
	StopDirectory []StopCandidate
}

type BatchDecision struct {
	Reports   []BatchReportDecision  `json:"reports"`
	Votes     []BatchVoteDecision    `json:"votes"`
	Ignored   []BatchIgnoredDecision `json:"ignored"`
	ModelMeta BatchModelMeta         `json:"modelMeta"`
}

type BatchReportDecision struct {
	ID               string  `json:"id"`
	Action           string  `json:"action"`
	TargetType       string  `json:"targetType"`
	TargetID         string  `json:"targetId"`
	Confidence       float64 `json:"confidence"`
	Reason           string  `json:"reason"`
	Language         string  `json:"language,omitempty"`
	SourceMessageIDs []int64 `json:"sourceMessageIds"`
}

type BatchVoteDecision struct {
	Action           string  `json:"action"`
	TargetType       string  `json:"targetType"`
	TargetID         string  `json:"targetId"`
	Confidence       float64 `json:"confidence"`
	Reason           string  `json:"reason"`
	Language         string  `json:"language,omitempty"`
	SourceMessageIDs []int64 `json:"sourceMessageIds"`
}

type BatchIgnoredDecision struct {
	MessageID int64  `json:"messageId"`
	Reason    string `json:"reason"`
}

type BatchModelMeta struct {
	SelectedModel string `json:"selectedModel,omitempty"`
	Provider      string `json:"provider,omitempty"`
}

type Settings struct {
	Enabled                 bool
	DryRun                  bool
	PollInterval            time.Duration
	BatchLimit              int
	MaxMessageAge           time.Duration
	MinConfidence           float64
	ProcessStartMinute      int
	ProcessEndMinute        int
	ProcessInterval         time.Duration
	Location                *time.Location
	ModelName               string
	SenderWindow            time.Duration
	SenderMessageLimit      int
	TargetDedupeWindow      time.Duration
	LiveVehicleFetchTimeout time.Duration
	ModelCallDelay          time.Duration
	RetryBaseDelay          time.Duration
	RetryMaxDelay           time.Duration
	ModelFailureLimit       int
	ModelCircuitOpen        time.Duration
	BatchStaleAfter         time.Duration
}

func (s Settings) withDefaults() Settings {
	if s.PollInterval <= 0 {
		s.PollInterval = 30 * time.Second
	}
	if s.BatchLimit <= 0 {
		s.BatchLimit = 250
	}
	if s.ProcessStartMinute < 0 || s.ProcessStartMinute >= 24*60 {
		s.ProcessStartMinute = 8 * 60
	}
	if s.ProcessEndMinute < 0 || s.ProcessEndMinute >= 24*60 || s.ProcessEndMinute == s.ProcessStartMinute {
		s.ProcessEndMinute = 20 * 60
	}
	if s.ProcessInterval <= 0 {
		s.ProcessInterval = MinimumProcessInterval
	}
	if s.ProcessInterval < MinimumProcessInterval {
		s.ProcessInterval = MinimumProcessInterval
	}
	if s.Location == nil {
		s.Location = time.UTC
	}
	if s.MinConfidence <= 0 {
		s.MinConfidence = 0.82
	}
	if s.SenderWindow <= 0 {
		s.SenderWindow = 30 * time.Minute
	}
	if s.SenderMessageLimit <= 0 {
		s.SenderMessageLimit = 20
	}
	if s.TargetDedupeWindow <= 0 {
		s.TargetDedupeWindow = 5 * time.Minute
	}
	if s.LiveVehicleFetchTimeout <= 0 {
		s.LiveVehicleFetchTimeout = 8 * time.Second
	}
	if s.ModelCallDelay <= 0 {
		s.ModelCallDelay = 5 * time.Second
	}
	if s.RetryBaseDelay <= 0 {
		s.RetryBaseDelay = time.Minute
	}
	if s.RetryMaxDelay <= 0 {
		s.RetryMaxDelay = 30 * time.Minute
	}
	if s.ModelFailureLimit <= 0 {
		s.ModelFailureLimit = 3
	}
	if s.ModelCircuitOpen <= 0 {
		s.ModelCircuitOpen = 10 * time.Minute
	}
	if s.BatchStaleAfter <= 0 {
		s.BatchStaleAfter = 15 * time.Minute
	}
	return s
}
