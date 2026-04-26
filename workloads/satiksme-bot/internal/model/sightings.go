package model

import "time"

type StopSighting struct {
	ID        string    `json:"id"`
	StopID    string    `json:"stopId"`
	UserID    int64     `json:"-"`
	Hidden    bool      `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

type VehicleSighting struct {
	ID               string    `json:"id"`
	StopID           string    `json:"stopId,omitempty"`
	UserID           int64     `json:"-"`
	Mode             string    `json:"mode"`
	RouteLabel       string    `json:"routeLabel"`
	Direction        string    `json:"direction"`
	Destination      string    `json:"destination"`
	DepartureSeconds int       `json:"departureSeconds"`
	LiveRowID        string    `json:"liveRowId,omitempty"`
	ScopeKey         string    `json:"-"`
	Hidden           bool      `json:"-"`
	CreatedAt        time.Time `json:"createdAt"`
}

type VehicleReportInput struct {
	StopID           string `json:"stopId,omitempty"`
	Mode             string `json:"mode"`
	RouteLabel       string `json:"routeLabel"`
	Direction        string `json:"direction"`
	Destination      string `json:"destination"`
	DepartureSeconds int    `json:"departureSeconds"`
	LiveRowID        string `json:"liveRowId"`
}

type PublicStopSighting struct {
	ID        string    `json:"id"`
	StopID    string    `json:"stopId"`
	StopName  string    `json:"stopName"`
	CreatedAt time.Time `json:"createdAt"`
}

type PublicVehicleSighting struct {
	ID               string    `json:"id"`
	StopID           string    `json:"stopId,omitempty"`
	StopName         string    `json:"stopName,omitempty"`
	Mode             string    `json:"mode"`
	RouteLabel       string    `json:"routeLabel"`
	Direction        string    `json:"direction"`
	Destination      string    `json:"destination"`
	DepartureSeconds int       `json:"departureSeconds"`
	LiveRowID        string    `json:"liveRowId,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

type VisibleSightings struct {
	StopSightings    []PublicStopSighting    `json:"stopSightings"`
	VehicleSightings []PublicVehicleSighting `json:"vehicleSightings"`
}

type ReportResult struct {
	Accepted          bool          `json:"accepted"`
	Deduped           bool          `json:"deduped"`
	RateLimited       bool          `json:"rateLimited,omitempty"`
	Reason            string        `json:"reason,omitempty"`
	CooldownRemaining time.Duration `json:"-"`
	CooldownSeconds   int           `json:"cooldownSeconds,omitempty"`
	IncidentID        string        `json:"incidentId,omitempty"`
}
