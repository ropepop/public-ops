package domain

import "time"

type SignalType string

const (
	SignalInspectionStarted SignalType = "INSPECTION_STARTED"
	SignalInspectionInCar   SignalType = "INSPECTION_IN_MY_CAR"
	SignalInspectionEnded   SignalType = "INSPECTION_ENDED"
)

type Confidence string

const (
	ConfidenceLow    Confidence = "LOW"
	ConfidenceMedium Confidence = "MEDIUM"
	ConfidenceHigh   Confidence = "HIGH"
)

type AlertStyle string

const (
	AlertStyleDiscreet AlertStyle = "DISCREET"
	AlertStyleDetailed AlertStyle = "DETAILED"
)

type Language string

const (
	LanguageLV Language = "LV"
	LanguageEN Language = "EN"
)

const DefaultLanguage = LanguageLV

const (
	RouteCheckInDefaultMinutes = 120
	RouteCheckInMinMinutes     = 30
	RouteCheckInMaxMinutes     = 8 * 60
)

type TrainInstance struct {
	ID            string    `json:"id"`
	ServiceDate   string    `json:"serviceDate"`
	FromStation   string    `json:"fromStation"`
	ToStation     string    `json:"toStation"`
	DepartureAt   time.Time `json:"departureAt"`
	ArrivalAt     time.Time `json:"arrivalAt"`
	SourceVersion string    `json:"sourceVersion"`
}

type Station struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	NormalizedKey string   `json:"normalizedKey"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Longitude     *float64 `json:"longitude,omitempty"`
}

type TrainStop struct {
	TrainInstanceID string     `json:"trainInstanceId"`
	StationID       string     `json:"stationId"`
	StationName     string     `json:"stationName"`
	Seq             int        `json:"seq"`
	ArrivalAt       *time.Time `json:"arrivalAt,omitempty"`
	DepartureAt     *time.Time `json:"departureAt,omitempty"`
	Latitude        *float64   `json:"latitude,omitempty"`
	Longitude       *float64   `json:"longitude,omitempty"`
}

type StationWindowTrain struct {
	Train       TrainInstance `json:"train"`
	StationID   string        `json:"stationId"`
	StationName string        `json:"stationName"`
	PassAt      time.Time     `json:"passAt"`
}

type RouteWindowTrain struct {
	Train           TrainInstance `json:"train"`
	FromStationID   string        `json:"fromStationId"`
	FromStationName string        `json:"fromStationName"`
	ToStationID     string        `json:"toStationId"`
	ToStationName   string        `json:"toStationName"`
	FromPassAt      time.Time     `json:"fromPassAt"`
	ToPassAt        time.Time     `json:"toPassAt"`
}

type CheckIn struct {
	UserID            int64      `json:"userId"`
	TrainInstanceID   string     `json:"trainInstanceId"`
	BoardingStationID *string    `json:"boardingStationId,omitempty"`
	CheckedInAt       time.Time  `json:"checkedInAt"`
	AutoCheckoutAt    time.Time  `json:"autoCheckoutAt"`
	MutedUntil        *time.Time `json:"mutedUntil,omitempty"`
	IsActive          bool       `json:"isActive"`
}

type RouteCheckInRoute struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	StationIDs   []string `json:"stationIds"`
	StationNames []string `json:"stationNames"`
	StationCount int      `json:"stationCount"`
	Supplemental bool     `json:"supplemental,omitempty"`
}

type RouteCheckIn struct {
	UserID       int64     `json:"userId"`
	RouteID      string    `json:"routeId"`
	RouteName    string    `json:"routeName"`
	StationIDs   []string  `json:"stationIds"`
	StationNames []string  `json:"stationNames,omitempty"`
	CheckedInAt  time.Time `json:"checkedInAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	IsActive     bool      `json:"isActive"`
}

type Subscription struct {
	UserID          int64     `json:"userId"`
	TrainInstanceID string    `json:"trainInstanceId"`
	ExpiresAt       time.Time `json:"expiresAt"`
	IsActive        bool      `json:"isActive"`
}

type FavoriteRoute struct {
	UserID          int64     `json:"userId"`
	FromStationID   string    `json:"fromStationId"`
	FromStationName string    `json:"fromStationName"`
	ToStationID     string    `json:"toStationId"`
	ToStationName   string    `json:"toStationName"`
	CreatedAt       time.Time `json:"createdAt"`
}

type ReportEvent struct {
	ID              string     `json:"id"`
	TrainInstanceID string     `json:"trainInstanceId"`
	UserID          int64      `json:"userId"`
	Signal          SignalType `json:"signal"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type StationSighting struct {
	ID                     string    `json:"id"`
	StationID              string    `json:"stationId"`
	StationName            string    `json:"stationName,omitempty"`
	DestinationStationID   *string   `json:"destinationStationId,omitempty"`
	DestinationStationName string    `json:"destinationStationName,omitempty"`
	MatchedTrainInstanceID *string   `json:"matchedTrainInstanceId,omitempty"`
	UserID                 int64     `json:"-"`
	CreatedAt              time.Time `json:"createdAt"`
}

type LocationReport struct {
	ID           string    `json:"id"`
	Scope        string    `json:"scope"`
	SubjectID    string    `json:"subjectId"`
	SubjectName  string    `json:"subjectName"`
	Latitude     *float64  `json:"latitude,omitempty"`
	Longitude    *float64  `json:"longitude,omitempty"`
	RadiusMeters int       `json:"radiusMeters,omitempty"`
	Description  string    `json:"description,omitempty"`
	UserID       int64     `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type UserSettings struct {
	UserID        int64      `json:"userId"`
	AlertsEnabled bool       `json:"alertsEnabled"`
	AlertStyle    AlertStyle `json:"alertStyle"`
	Language      Language   `json:"language"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type TrainStatus struct {
	State           string     `json:"state"`
	LastReportAt    *time.Time `json:"lastReportAt,omitempty"`
	Confidence      Confidence `json:"confidence"`
	UniqueReporters int        `json:"uniqueReporters"`
}

const (
	StatusNoReports    = "NO_REPORTS"
	StatusLastSighting = "LAST_SIGHTING"
	StatusMixedReports = "MIXED_REPORTS"
)
