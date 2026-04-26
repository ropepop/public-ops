package model

import "time"

type Catalog struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Stops       []Stop    `json:"stops"`
	Routes      []Route   `json:"routes"`
}

type Stop struct {
	ID            string   `json:"id"`
	LiveID        string   `json:"liveId"`
	Name          string   `json:"name"`
	Latitude      float64  `json:"latitude"`
	Longitude     float64  `json:"longitude"`
	Modes         []string `json:"modes"`
	RouteLabels   []string `json:"routeLabels"`
	NearbyStopIDs []string `json:"nearbyStopIds,omitempty"`
}

type Route struct {
	Label   string   `json:"label"`
	Mode    string   `json:"mode"`
	Name    string   `json:"name"`
	StopIDs []string `json:"stopIds,omitempty"`
}
