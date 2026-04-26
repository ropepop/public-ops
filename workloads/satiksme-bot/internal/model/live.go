package model

import "time"

type LiveVehicle struct {
	ID             string            `json:"id"`
	VehicleCode    string            `json:"vehicleCode,omitempty"`
	Mode           string            `json:"mode"`
	RouteLabel     string            `json:"routeLabel"`
	Direction      string            `json:"direction,omitempty"`
	Destination    string            `json:"destination,omitempty"`
	Latitude       float64           `json:"latitude"`
	Longitude      float64           `json:"longitude"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	Heading        int               `json:"heading,omitempty"`
	StopID         string            `json:"stopId,omitempty"`
	StopName       string            `json:"stopName,omitempty"`
	ArrivalSeconds int               `json:"arrivalSeconds,omitempty"`
	LowFloor       bool              `json:"lowFloor,omitempty"`
	LiveRowID      string            `json:"liveRowId,omitempty"`
	SightingCount  int               `json:"sightingCount,omitempty"`
	Incidents      []IncidentSummary `json:"incidents,omitempty"`
}

type LiveTransportSnapshot struct {
	Version     string        `json:"version"`
	GeneratedAt time.Time     `json:"generatedAt"`
	Vehicles    []LiveVehicle `json:"vehicles"`
}

type LiveTransportState struct {
	Feed                string    `json:"feed"`
	Version             string    `json:"version"`
	Path                string    `json:"path"`
	Hash                string    `json:"hash"`
	PublishedAt         time.Time `json:"publishedAt"`
	LastSuccessAt       time.Time `json:"lastSuccessAt"`
	LastAttemptAt       time.Time `json:"lastAttemptAt"`
	Status              string    `json:"status"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	VehicleCount        int       `json:"vehicleCount"`
}

type PublicMapPayload struct {
	GeneratedAt   time.Time         `json:"generatedAt"`
	Stops         []Stop            `json:"stops"`
	Sightings     VisibleSightings  `json:"sightings"`
	StopIncidents []IncidentSummary `json:"stopIncidents,omitempty"`
	LiveVehicles  []LiveVehicle     `json:"liveVehicles"`
}

type PublicLiveMapPayload struct {
	GeneratedAt   time.Time         `json:"generatedAt"`
	Sightings     VisibleSightings  `json:"sightings"`
	StopIncidents []IncidentSummary `json:"stopIncidents,omitempty"`
	LiveVehicles  []LiveVehicle     `json:"liveVehicles"`
}
