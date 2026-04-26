package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"satiksmebot/internal/model"
)

const (
	maxIncidentComments       = 100
	incidentLookbackWindow    = 24 * time.Hour
	incidentResolvedVoteCount = 2
	sameVoteWindow            = 30 * time.Minute
	mapReportWindow           = 30 * time.Minute
	mapReportLimit            = 5
	voteActionWindow          = time.Hour
	voteActionLimit           = 20
)

type RateLimitError struct {
	Reason    string
	Remaining time.Duration
}

func (e *RateLimitError) Error() string {
	switch e.Reason {
	case "same_vote":
		return "Šāds balsojums jau ir iesniegts. Jānogaida."
	case "map_report_limit":
		return "Pārāk daudz kartes ziņojumu. Jānogaida."
	case "vote_action_limit":
		return "Pārāk daudz balsojumu. Jānogaida."
	default:
		return "Jānogaida pirms nākamās darbības."
	}
}

type incidentBundle struct {
	summary model.IncidentSummary
	events  []model.IncidentEvent
}

func StopIncidentID(stopID string) string {
	return fmt.Sprintf("stop:%s", sanitizeIncidentKey(stopID))
}

func VehicleIncidentID(scopeKey string) string {
	return fmt.Sprintf("vehicle:%s", sanitizeIncidentKey(scopeKey))
}

func (s *Service) ListActiveIncidents(ctx context.Context, catalog *model.Catalog, now time.Time, viewerID int64, limit int) ([]model.IncidentSummary, error) {
	summaries, err := s.listRecentIncidents(ctx, catalog, now, viewerID)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (s *Service) ListMapVisibleIncidents(ctx context.Context, catalog *model.Catalog, now time.Time, viewerID int64) ([]model.IncidentSummary, error) {
	summaries, err := s.listRecentIncidents(ctx, catalog, now, viewerID)
	if err != nil {
		return nil, err
	}
	out := make([]model.IncidentSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Resolved {
			continue
		}
		out = append(out, summary)
	}
	return out, nil
}

func (s *Service) IncidentDetail(ctx context.Context, catalog *model.Catalog, incidentID string, now time.Time, viewerID int64) (*model.IncidentDetail, error) {
	incidentID = strings.TrimSpace(incidentID)
	if incidentID == "" {
		return nil, fmt.Errorf("incident id is required")
	}
	bundle, err := s.findIncidentBundle(ctx, catalog, incidentID, now)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("incident not found")
	}
	since := incidentSince(now)
	summary, err := s.enrichIncidentSummary(ctx, bundle.summary, viewerID, since)
	if err != nil {
		return nil, err
	}
	comments, err := s.store.ListIncidentComments(ctx, incidentID, maxIncidentComments)
	if err != nil {
		return nil, err
	}
	comments = recentIncidentComments(comments, since)
	events, err := s.unifiedIncidentEvents(ctx, incidentID, bundle.events, since)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(comments, func(left, right int) bool {
		return comments[left].CreatedAt.Before(comments[right].CreatedAt)
	})
	return &model.IncidentDetail{
		Summary:  summary,
		Events:   events,
		Comments: comments,
	}, nil
}

func (s *Service) VoteIncident(ctx context.Context, catalog *model.Catalog, incidentID string, userID int64, value model.IncidentVoteValue, now time.Time) (model.IncidentVoteSummary, error) {
	if _, err := s.IncidentDetail(ctx, catalog, incidentID, now, userID); err != nil {
		return model.IncidentVoteSummary{}, err
	}
	if err := s.enforceSameVoteWindow(ctx, incidentID, userID, value, now); err != nil {
		return model.IncidentVoteSummary{}, err
	}
	count, err := s.store.CountIncidentVoteEventsByUserSince(ctx, userID, model.IncidentVoteSourceVote, now.Add(-voteActionWindow))
	if err != nil {
		return model.IncidentVoteSummary{}, err
	}
	if count >= voteActionLimit {
		return model.IncidentVoteSummary{}, &RateLimitError{Reason: "vote_action_limit", Remaining: voteActionWindow}
	}
	if err := s.recordIncidentVote(ctx, incidentID, userID, value, model.IncidentVoteSourceVote, generateID(), now); err != nil {
		return model.IncidentVoteSummary{}, err
	}
	return s.incidentVoteSummary(ctx, incidentID, userID, incidentSince(now))
}

func (s *Service) AddIncidentComment(ctx context.Context, catalog *model.Catalog, incidentID string, userID int64, body string, now time.Time) (*model.IncidentComment, error) {
	if _, err := s.IncidentDetail(ctx, catalog, incidentID, now, userID); err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("comment is required")
	}
	if len([]rune(body)) > 280 {
		return nil, fmt.Errorf("comment is too long")
	}
	comment := model.IncidentComment{
		ID:         generateID(),
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   model.GenericNickname(userID),
		Body:       body,
		CreatedAt:  now.UTC(),
	}
	if err := s.store.InsertIncidentComment(ctx, comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

func (s *Service) listRecentIncidents(ctx context.Context, catalog *model.Catalog, now time.Time, viewerID int64) ([]model.IncidentSummary, error) {
	bundles, err := s.collectIncidentBundles(ctx, catalog, now)
	if err != nil {
		return nil, err
	}
	since := incidentSince(now)
	summaries := make([]model.IncidentSummary, 0, len(bundles))
	for _, bundle := range bundles {
		summary, err := s.enrichIncidentSummary(ctx, bundle.summary, viewerID, since)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(left, right int) bool {
		return summaries[left].LastReportAt.After(summaries[right].LastReportAt)
	})
	return summaries, nil
}

func (s *Service) collectIncidentBundles(ctx context.Context, catalog *model.Catalog, now time.Time) ([]incidentBundle, error) {
	since := incidentSince(now)
	stopSightings, err := s.store.ListStopSightingsSince(ctx, since, "", 0)
	if err != nil {
		return nil, err
	}
	vehicleSightings, err := s.store.ListVehicleSightingsSince(ctx, since, "", 0)
	if err != nil {
		return nil, err
	}
	stopNames := model.StopNameLookup(catalog)
	bundlesByID := map[string]*incidentBundle{}
	for _, stopSighting := range stopSightings {
		if stopSighting.Hidden {
			continue
		}
		stopID := strings.TrimSpace(stopSighting.StopID)
		subjectName := stopNames[stopID]
		if subjectName == "" {
			subjectName = stopID
		}
		event := model.IncidentEvent{
			ID:        stopSighting.ID,
			Kind:      "report",
			Name:      incidentVoteEventLabel(model.IncidentVoteOngoing),
			Nickname:  model.GenericNickname(stopSighting.UserID),
			CreatedAt: stopSighting.CreatedAt,
		}
		upsertIncidentBundle(bundlesByID, StopIncidentID(stopID), model.IncidentSummary{
			ID:             StopIncidentID(stopID),
			Scope:          "stop",
			SubjectID:      stopID,
			SubjectName:    subjectName,
			StopID:         stopID,
			LastReportName: event.Name,
			LastReportAt:   stopSighting.CreatedAt,
			LastReporter:   event.Nickname,
		}, event)
	}
	for _, vehicleSighting := range vehicleSightings {
		if vehicleSighting.Hidden {
			continue
		}
		stopID := strings.TrimSpace(vehicleSighting.StopID)
		stopName := stopNames[stopID]
		if stopName == "" {
			stopName = stopID
		}
		event := model.IncidentEvent{
			ID:        vehicleSighting.ID,
			Kind:      "report",
			Name:      incidentVoteEventLabel(model.IncidentVoteOngoing),
			Nickname:  model.GenericNickname(vehicleSighting.UserID),
			CreatedAt: vehicleSighting.CreatedAt,
		}
		upsertIncidentBundle(bundlesByID, VehicleIncidentID(vehicleSighting.ScopeKey), model.IncidentSummary{
			ID:             VehicleIncidentID(vehicleSighting.ScopeKey),
			Scope:          "vehicle",
			SubjectID:      strings.TrimSpace(vehicleSighting.ScopeKey),
			SubjectName:    vehicleIncidentSubjectName(vehicleSighting, stopName),
			StopID:         stopID,
			LastReportName: event.Name,
			LastReportAt:   vehicleSighting.CreatedAt,
			LastReporter:   event.Nickname,
			Vehicle: &model.IncidentVehicleContext{
				ScopeKey:         strings.TrimSpace(vehicleSighting.ScopeKey),
				StopID:           stopID,
				StopName:         stopName,
				Mode:             strings.TrimSpace(vehicleSighting.Mode),
				RouteLabel:       strings.TrimSpace(vehicleSighting.RouteLabel),
				Direction:        strings.TrimSpace(vehicleSighting.Direction),
				Destination:      strings.TrimSpace(vehicleSighting.Destination),
				DepartureSeconds: vehicleSighting.DepartureSeconds,
				LiveRowID:        strings.TrimSpace(vehicleSighting.LiveRowID),
			},
		}, event)
	}
	out := make([]incidentBundle, 0, len(bundlesByID))
	for _, item := range bundlesByID {
		out = append(out, *item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].summary.LastReportAt.After(out[right].summary.LastReportAt)
	})
	return out, nil
}

func upsertIncidentBundle(items map[string]*incidentBundle, incidentID string, summary model.IncidentSummary, event model.IncidentEvent) {
	if existing, ok := items[incidentID]; ok {
		if summary.LastReportAt.After(existing.summary.LastReportAt) {
			existing.summary.SubjectID = summary.SubjectID
			existing.summary.SubjectName = summary.SubjectName
			existing.summary.StopID = summary.StopID
			existing.summary.LastReportAt = summary.LastReportAt
			existing.summary.LastReportName = summary.LastReportName
			existing.summary.LastReporter = summary.LastReporter
			existing.summary.Vehicle = summary.Vehicle
		}
		existing.events = append(existing.events, event)
		return
	}
	items[incidentID] = &incidentBundle{
		summary: summary,
		events:  []model.IncidentEvent{event},
	}
}

func (s *Service) findIncidentBundle(ctx context.Context, catalog *model.Catalog, incidentID string, now time.Time) (*incidentBundle, error) {
	bundles, err := s.collectIncidentBundles(ctx, catalog, now)
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundles {
		if bundle.summary.ID == incidentID {
			return &bundle, nil
		}
	}
	return nil, nil
}

func (s *Service) enrichIncidentSummary(ctx context.Context, summary model.IncidentSummary, viewerID int64, since time.Time) (model.IncidentSummary, error) {
	votes, err := s.incidentVoteSummary(ctx, summary.ID, viewerID, since)
	if err != nil {
		return model.IncidentSummary{}, err
	}
	comments, err := s.store.ListIncidentComments(ctx, summary.ID, maxIncidentComments)
	if err != nil {
		return model.IncidentSummary{}, err
	}
	comments = recentIncidentComments(comments, since)
	summary.Votes = votes
	summary.CommentCount = len(comments)
	summary.Resolved = votes.Cleared >= incidentResolvedVoteCount
	summary.Active = !summary.Resolved
	events, err := s.store.ListIncidentVoteEvents(ctx, summary.ID, since, 1)
	if err != nil {
		return model.IncidentSummary{}, err
	}
	if len(events) > 0 {
		summary.LastReportName = incidentVoteEventLabel(events[0].Value)
		summary.LastReportAt = events[0].CreatedAt
		summary.LastReporter = events[0].Nickname
	}
	return summary, nil
}

func (s *Service) incidentVoteSummary(ctx context.Context, incidentID string, viewerID int64, since time.Time) (model.IncidentVoteSummary, error) {
	items, err := s.store.ListIncidentVotes(ctx, incidentID)
	if err != nil {
		return model.IncidentVoteSummary{}, err
	}
	summary := model.IncidentVoteSummary{}
	for _, item := range items {
		if item.UpdatedAt.Before(since) {
			continue
		}
		switch item.Value {
		case model.IncidentVoteOngoing:
			summary.Ongoing++
		case model.IncidentVoteCleared:
			summary.Cleared++
		}
		if viewerID > 0 && item.UserID == viewerID {
			summary.UserValue = item.Value
		}
	}
	return summary, nil
}

func (s *Service) recordIncidentVote(ctx context.Context, incidentID string, userID int64, value model.IncidentVoteValue, source model.IncidentVoteSource, eventID string, now time.Time) error {
	vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, value, source, eventID, now)
	if err != nil {
		return err
	}
	return s.store.RecordIncidentVote(ctx, vote, event)
}

func (s *Service) incidentVoteAction(ctx context.Context, incidentID string, userID int64, value model.IncidentVoteValue, source model.IncidentVoteSource, eventID string, now time.Time) (model.IncidentVote, model.IncidentVoteEvent, error) {
	nickname := model.GenericNickname(userID)
	createdAt := now.UTC()
	if existing, err := s.currentIncidentVote(ctx, incidentID, userID); err != nil {
		return model.IncidentVote{}, model.IncidentVoteEvent{}, err
	} else if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	vote := model.IncidentVote{
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   nickname,
		Value:      value,
		CreatedAt:  createdAt,
		UpdatedAt:  now.UTC(),
	}
	event := model.IncidentVoteEvent{
		ID:         eventID,
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   nickname,
		Value:      value,
		Source:     source,
		CreatedAt:  now.UTC(),
	}
	return vote, event, nil
}

func (s *Service) enforceSameVoteWindow(ctx context.Context, incidentID string, userID int64, value model.IncidentVoteValue, now time.Time) error {
	current, err := s.currentIncidentVote(ctx, incidentID, userID)
	if err != nil {
		return err
	}
	if current == nil || current.Value != value {
		return nil
	}
	delta := now.Sub(current.UpdatedAt)
	if delta < sameVoteWindow {
		return &RateLimitError{Reason: "same_vote", Remaining: sameVoteWindow - delta}
	}
	return nil
}

func (s *Service) currentIncidentVote(ctx context.Context, incidentID string, userID int64) (*model.IncidentVote, error) {
	items, err := s.store.ListIncidentVotes(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.UserID == userID {
			return &item, nil
		}
	}
	return nil, nil
}

func (s *Service) unifiedIncidentEvents(ctx context.Context, incidentID string, fallback []model.IncidentEvent, since time.Time) ([]model.IncidentEvent, error) {
	voteEvents, err := s.store.ListIncidentVoteEvents(ctx, incidentID, since, 0)
	if err != nil {
		return nil, err
	}
	out := make([]model.IncidentEvent, 0, len(voteEvents)+len(fallback))
	seen := make(map[string]bool, len(voteEvents))
	for _, item := range voteEvents {
		if item.CreatedAt.Before(since) {
			continue
		}
		seen[item.ID] = true
		out = append(out, model.IncidentEvent{
			ID:        item.ID,
			Kind:      string(item.Source),
			Name:      incidentVoteEventLabel(item.Value),
			Nickname:  item.Nickname,
			CreatedAt: item.CreatedAt,
		})
	}
	for _, item := range fallback {
		if item.CreatedAt.Before(since) || seen[item.ID] {
			continue
		}
		item.Name = incidentVoteEventLabel(model.IncidentVoteOngoing)
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.Before(out[right].CreatedAt)
	})
	return out, nil
}

func recentIncidentComments(items []model.IncidentComment, since time.Time) []model.IncidentComment {
	out := make([]model.IncidentComment, 0, len(items))
	for _, item := range items {
		if item.CreatedAt.Before(since) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func incidentSince(now time.Time) time.Time {
	return now.Add(-incidentLookbackWindow)
}

func stopIncidentLabel() string {
	return incidentVoteEventLabel(model.IncidentVoteOngoing)
}

func vehicleIncidentSubjectName(item model.VehicleSighting, stopName string) string {
	label := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(item.Mode), strings.TrimSpace(item.RouteLabel)}, " "))
	switch {
	case label != "":
		return label
	case stopName != "":
		return stopName
	case strings.TrimSpace(item.Destination) != "":
		return strings.TrimSpace(item.Destination)
	default:
		return strings.TrimSpace(item.ScopeKey)
	}
}

func vehicleIncidentLabel(item model.VehicleSighting) string {
	mode := strings.TrimSpace(item.Mode)
	route := strings.TrimSpace(item.RouteLabel)
	destination := strings.TrimSpace(item.Destination)
	label := strings.TrimSpace(strings.Join([]string{mode, route}, " "))
	switch {
	case label == "" && destination == "":
		return "Transporta kontrole"
	case label == "":
		return "Kontrole uz " + destination
	case destination == "":
		return "Kontrole " + label
	default:
		return "Kontrole " + label + " uz " + destination
	}
}

func incidentVoteEventLabel(value model.IncidentVoteValue) string {
	switch value {
	case model.IncidentVoteCleared:
		return "Nav kontrole"
	default:
		return "Kontrole"
	}
}

func sanitizeIncidentKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", "|", "-", ">", "-", "<", "-")
	value = replacer.Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "unknown"
	}
	return value
}
