package model

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

type IncidentVoteValue string

const (
	IncidentVoteOngoing IncidentVoteValue = "ONGOING"
	IncidentVoteCleared IncidentVoteValue = "CLEARED"
)

type IncidentVoteSource string

const (
	IncidentVoteSourceMapReport IncidentVoteSource = "map_report"
	IncidentVoteSourceVote      IncidentVoteSource = "vote"
)

type IncidentVote struct {
	IncidentID string            `json:"incidentId"`
	UserID     int64             `json:"-"`
	Nickname   string            `json:"nickname"`
	Value      IncidentVoteValue `json:"value"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type IncidentVoteEvent struct {
	ID         string             `json:"id"`
	IncidentID string             `json:"incidentId"`
	UserID     int64              `json:"-"`
	Nickname   string             `json:"nickname"`
	Value      IncidentVoteValue  `json:"value"`
	Source     IncidentVoteSource `json:"source"`
	CreatedAt  time.Time          `json:"createdAt"`
}

type IncidentComment struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incidentId"`
	UserID     int64     `json:"-"`
	Nickname   string    `json:"nickname"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type IncidentVoteSummary struct {
	Ongoing   int               `json:"ongoing"`
	Cleared   int               `json:"cleared"`
	UserValue IncidentVoteValue `json:"userValue,omitempty"`
}

type IncidentVehicleContext struct {
	ScopeKey         string `json:"scopeKey,omitempty"`
	StopID           string `json:"stopId,omitempty"`
	StopName         string `json:"stopName,omitempty"`
	Mode             string `json:"mode,omitempty"`
	RouteLabel       string `json:"routeLabel,omitempty"`
	Direction        string `json:"direction,omitempty"`
	Destination      string `json:"destination,omitempty"`
	DepartureSeconds int    `json:"departureSeconds,omitempty"`
	LiveRowID        string `json:"liveRowId,omitempty"`
}

type IncidentSummary struct {
	ID             string                  `json:"id"`
	Scope          string                  `json:"scope"`
	SubjectID      string                  `json:"subjectId,omitempty"`
	SubjectName    string                  `json:"subjectName"`
	StopID         string                  `json:"stopId,omitempty"`
	LastReportName string                  `json:"lastReportName"`
	LastReportAt   time.Time               `json:"lastReportAt"`
	LastReporter   string                  `json:"lastReporter"`
	CommentCount   int                     `json:"commentCount"`
	Votes          IncidentVoteSummary     `json:"votes"`
	Active         bool                    `json:"active"`
	Resolved       bool                    `json:"resolved"`
	Vehicle        *IncidentVehicleContext `json:"vehicle,omitempty"`
}

type IncidentEvent struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Nickname  string    `json:"nickname"`
	CreatedAt time.Time `json:"createdAt"`
}

type IncidentDetail struct {
	Summary  IncidentSummary   `json:"summary"`
	Events   []IncidentEvent   `json:"events"`
	Comments []IncidentComment `json:"comments"`
}

func ParseIncidentVoteValue(raw string) (IncidentVoteValue, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(IncidentVoteOngoing):
		return IncidentVoteOngoing, true
	case string(IncidentVoteCleared):
		return IncidentVoteCleared, true
	default:
		return "", false
	}
}

func GenericNickname(userID int64) string {
	return GenericNicknameForStableID(TelegramStableID(userID))
}

func TelegramStableID(userID int64) string {
	return fmt.Sprintf("telegram:%d", userID)
}

func GenericNicknameForStableID(stableID string) string {
	adjectives := []string{
		"Amber", "Cedar", "Silver", "North", "Swift", "Mellow", "Harbor", "Forest",
		"Granite", "Quiet", "Bright", "Saffron", "Willow", "Copper", "River", "Cloud",
	}
	nouns := []string{
		"Scout", "Rider", "Signal", "Beacon", "Traveler", "Watcher", "Harbor", "Comet",
		"Falcon", "Lantern", "Pioneer", "Courier", "Voyager", "Pilot", "Atlas", "Drifter",
	}
	cleanStableID := strings.TrimSpace(stableID)
	if cleanStableID == "" {
		cleanStableID = "telegram:0"
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte("satiksme:" + cleanStableID))
	sum := hasher.Sum32()
	adjective := adjectives[int(sum)%len(adjectives)]
	noun := nouns[int((sum>>8))%len(nouns)]
	suffix := int(sum%900) + 100
	return fmt.Sprintf("%s %s %03d", adjective, noun, suffix)
}
