package domain

import "satiksmebot/internal/model"

type IncidentVoteValue = model.IncidentVoteValue

const (
	IncidentVoteOngoing = model.IncidentVoteOngoing
	IncidentVoteCleared = model.IncidentVoteCleared
)

type IncidentVote = model.IncidentVote
type IncidentComment = model.IncidentComment
type IncidentVoteSummary = model.IncidentVoteSummary
type IncidentVehicleContext = model.IncidentVehicleContext
type IncidentSummary = model.IncidentSummary
type IncidentEvent = model.IncidentEvent
type IncidentDetail = model.IncidentDetail

var ParseIncidentVoteValue = model.ParseIncidentVoteValue
var GenericNickname = model.GenericNickname
