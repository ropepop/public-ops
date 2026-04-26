package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"
)

const (
	trainIncidentActiveWindow   = 15 * time.Minute
	stationIncidentActiveWindow = 30 * time.Minute
	maxIncidentComments         = 100
	maxIncidentVoteEvents       = 100
)

type incidentBundle struct {
	summary domain.IncidentSummary
	events  []domain.IncidentEvent
}

func (s *Service) ListActiveIncidents(ctx context.Context, now time.Time, viewerID int64, limit int) ([]domain.IncidentSummary, error) {
	trainBundles, err := s.collectTrainIncidentBundles(ctx, now, false)
	if err != nil {
		return nil, err
	}
	stationBundles, err := s.collectStationIncidentBundles(ctx, now, false)
	if err != nil {
		return nil, err
	}
	bundles := append(trainBundles, stationBundles...)
	dayStart := incidentDayStart(now)
	summaries := make([]domain.IncidentSummary, 0, len(bundles))
	for _, bundle := range bundles {
		summary, err := s.enrichIncidentSummary(ctx, bundle.summary, viewerID, dayStart)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].LastActivityAt.After(summaries[j].LastActivityAt)
	})
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (s *Service) IncidentDetail(ctx context.Context, incidentID string, now time.Time, viewerID int64) (*domain.IncidentDetail, error) {
	incidentID = strings.TrimSpace(incidentID)
	if incidentID == "" {
		return nil, fmt.Errorf("incident id is required")
	}
	bundle, err := s.findIncidentBundle(ctx, incidentID, now)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("incident not found")
	}
	dayStart := incidentDayStart(now)
	summary, err := s.enrichIncidentSummary(ctx, bundle.summary, viewerID, dayStart)
	if err != nil {
		return nil, err
	}
	comments, err := s.store.ListIncidentComments(ctx, incidentID, maxIncidentComments)
	if err != nil {
		return nil, err
	}
	voteEvents, err := s.store.ListIncidentVoteEvents(ctx, incidentID, dayStart, maxIncidentVoteEvents)
	if err != nil {
		return nil, err
	}
	events := append([]domain.IncidentEvent{}, bundle.events...)
	for _, comment := range comments {
		if comment.CreatedAt.Before(dayStart) {
			continue
		}
		events = append(events, domain.IncidentEvent{
			ID:        comment.ID,
			Kind:      "comment",
			Name:      incidentCommentActivityLabel(),
			Detail:    comment.Body,
			Nickname:  comment.Nickname,
			CreatedAt: comment.CreatedAt,
		})
	}
	for _, voteEvent := range voteEvents {
		if voteEvent.Value != domain.IncidentVoteOngoing {
			continue
		}
		events = append(events, domain.IncidentEvent{
			ID:        voteEvent.ID,
			Kind:      "vote",
			Name:      incidentVoteEventLabel(voteEvent.Value),
			Nickname:  voteEvent.Nickname,
			CreatedAt: voteEvent.CreatedAt,
		})
	}
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].CreatedAt.After(comments[j].CreatedAt)
	})
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})
	return &domain.IncidentDetail{
		Summary:  summary,
		Events:   events,
		Comments: comments,
	}, nil
}

func (s *Service) VoteIncident(ctx context.Context, incidentID string, userID int64, value domain.IncidentVoteValue, now time.Time) (domain.IncidentVoteSummary, error) {
	if _, err := s.IncidentDetail(ctx, incidentID, now, userID); err != nil {
		return domain.IncidentVoteSummary{}, err
	}
	vote := domain.IncidentVote{
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   domain.GenericNickname(userID),
		Value:      value,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}
	if err := s.store.UpsertIncidentVote(ctx, vote); err != nil {
		return domain.IncidentVoteSummary{}, err
	}
	if err := s.store.InsertIncidentVoteEvent(ctx, domain.IncidentVoteEvent{
		ID:         generateID(),
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   vote.Nickname,
		Value:      value,
		CreatedAt:  now.UTC(),
	}); err != nil {
		return domain.IncidentVoteSummary{}, err
	}
	return s.incidentVoteSummary(ctx, incidentID, userID)
}

func (s *Service) AddIncidentComment(ctx context.Context, incidentID string, userID int64, body string, now time.Time) (*domain.IncidentComment, error) {
	if _, err := s.IncidentDetail(ctx, incidentID, now, userID); err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("comment is required")
	}
	if len([]rune(body)) > 280 {
		return nil, fmt.Errorf("comment is too long")
	}
	comment := domain.IncidentComment{
		ID:         generateID(),
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   domain.GenericNickname(userID),
		Body:       body,
		CreatedAt:  now.UTC(),
	}
	if err := s.store.InsertIncidentComment(ctx, comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

func TrainIncidentID(trainID string, dayKey string, contextKey string) string {
	return fmt.Sprintf("train:%s:%s", strings.TrimSpace(trainID), sanitizeIncidentKey(dayKey+"-"+contextKey))
}

func StationIncidentID(stationID string, dayKey string, contextKey string) string {
	return fmt.Sprintf("station:%s:%s", strings.TrimSpace(stationID), sanitizeIncidentKey(dayKey+"-"+contextKey))
}

func (s *Service) collectTrainIncidentBundles(ctx context.Context, now time.Time, activeOnly bool) ([]incidentBundle, error) {
	dayStart := incidentDayStart(now)
	reportEvents, err := s.store.ListRecentReportEvents(ctx, dayStart, 600)
	if err != nil {
		return nil, err
	}
	if len(reportEvents) == 0 {
		return nil, nil
	}
	bundlesByID := map[string]*incidentBundle{}
	trainCache := map[string]*domain.TrainInstance{}
	stopCache := map[string][]domain.TrainStop{}
	for _, reportEvent := range reportEvents {
		train, err := s.cachedTrain(ctx, trainCache, reportEvent.TrainInstanceID)
		if err != nil {
			return nil, err
		}
		stops, err := s.cachedTrainStops(ctx, stopCache, reportEvent.TrainInstanceID)
		if err != nil {
			return nil, err
		}
		contextKey, subjectName := resolveTrainIncidentContext(train, stops, reportEvent.CreatedAt)
		incidentID := TrainIncidentID(reportEvent.TrainInstanceID, incidentDayKey(reportEvent.CreatedAt.In(now.Location())), contextKey)
		event := domain.IncidentEvent{
			ID:        reportEvent.ID,
			Kind:      "report",
			Name:      trainSignalIncidentLabel(reportEvent.Signal),
			Nickname:  domain.GenericNickname(reportEvent.UserID),
			CreatedAt: reportEvent.CreatedAt,
		}
		existing, ok := bundlesByID[incidentID]
		if !ok {
			existing = &incidentBundle{
				summary: domain.IncidentSummary{
					ID:                incidentID,
					Scope:             "train",
					SubjectID:         contextKey,
					SubjectName:       subjectName,
					LastReportName:    event.Name,
					LastReportAt:      reportEvent.CreatedAt,
					LastActivityName:  event.Name,
					LastActivityAt:    reportEvent.CreatedAt,
					LastActivityActor: event.Nickname,
					LastReporter:      event.Nickname,
					Active:            reportEvent.CreatedAt.After(now.Add(-trainIncidentActiveWindow)),
				},
			}
			bundlesByID[incidentID] = existing
		}
		if reportEvent.CreatedAt.After(existing.summary.LastReportAt) {
			existing.summary.LastReportAt = reportEvent.CreatedAt
			existing.summary.LastReportName = event.Name
			existing.summary.LastReporter = event.Nickname
			existing.summary.Active = reportEvent.CreatedAt.After(now.Add(-trainIncidentActiveWindow))
		}
		if reportEvent.CreatedAt.After(existing.summary.LastActivityAt) {
			existing.summary.LastActivityAt = reportEvent.CreatedAt
			existing.summary.LastActivityName = event.Name
			existing.summary.LastActivityActor = event.Nickname
		}
		existing.events = append(existing.events, event)
	}
	return incidentBundleList(bundlesByID, activeOnly), nil
}

func (s *Service) collectStationIncidentBundles(ctx context.Context, now time.Time, activeOnly bool) ([]incidentBundle, error) {
	dayStart := incidentDayStart(now)
	stationSightings, err := s.store.ListRecentStationSightings(ctx, dayStart, 600)
	if err != nil {
		return nil, err
	}
	if len(stationSightings) == 0 {
		return nil, nil
	}
	bundlesByID := map[string]*incidentBundle{}
	for _, stationSighting := range stationSightings {
		contextKey, reportName := stationIncidentContext(stationSighting)
		incidentID := StationIncidentID(stationSighting.StationID, incidentDayKey(stationSighting.CreatedAt.In(now.Location())), contextKey)
		event := domain.IncidentEvent{
			ID:        stationSighting.ID,
			Kind:      "report",
			Name:      reportName,
			Nickname:  domain.GenericNickname(stationSighting.UserID),
			CreatedAt: stationSighting.CreatedAt,
		}
		existing, ok := bundlesByID[incidentID]
		if !ok {
			existing = &incidentBundle{
				summary: domain.IncidentSummary{
					ID:                incidentID,
					Scope:             "station",
					SubjectID:         stationSighting.StationID,
					SubjectName:       stationSighting.StationName,
					LastReportName:    event.Name,
					LastReportAt:      stationSighting.CreatedAt,
					LastActivityName:  event.Name,
					LastActivityAt:    stationSighting.CreatedAt,
					LastActivityActor: event.Nickname,
					LastReporter:      event.Nickname,
					Active:            stationSighting.CreatedAt.After(now.Add(-stationIncidentActiveWindow)),
				},
			}
			bundlesByID[incidentID] = existing
		}
		if stationSighting.CreatedAt.After(existing.summary.LastReportAt) {
			existing.summary.LastReportAt = stationSighting.CreatedAt
			existing.summary.LastReportName = event.Name
			existing.summary.LastReporter = event.Nickname
			existing.summary.Active = stationSighting.CreatedAt.After(now.Add(-stationIncidentActiveWindow))
		}
		if stationSighting.CreatedAt.After(existing.summary.LastActivityAt) {
			existing.summary.LastActivityAt = stationSighting.CreatedAt
			existing.summary.LastActivityName = event.Name
			existing.summary.LastActivityActor = event.Nickname
		}
		existing.events = append(existing.events, event)
	}
	return incidentBundleList(bundlesByID, activeOnly), nil
}

func incidentBundleList(items map[string]*incidentBundle, activeOnly bool) []incidentBundle {
	out := make([]incidentBundle, 0, len(items))
	for _, item := range items {
		if activeOnly && !item.summary.Active {
			continue
		}
		out = append(out, *item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].summary.LastActivityAt.After(out[j].summary.LastActivityAt)
	})
	return out
}

func (s *Service) findIncidentBundle(ctx context.Context, incidentID string, now time.Time) (*incidentBundle, error) {
	parts := strings.SplitN(strings.TrimSpace(incidentID), ":", 3)
	if len(parts) != 3 {
		return nil, nil
	}
	switch parts[0] {
	case "train":
		bundles, err := s.collectTrainIncidentBundles(ctx, now, false)
		if err != nil {
			return nil, err
		}
		for _, bundle := range bundles {
			if bundle.summary.ID == incidentID {
				return &bundle, nil
			}
		}
	case "station":
		bundles, err := s.collectStationIncidentBundles(ctx, now, false)
		if err != nil {
			return nil, err
		}
		for _, bundle := range bundles {
			if bundle.summary.ID == incidentID {
				return &bundle, nil
			}
		}
	}
	return nil, nil
}

func (s *Service) enrichIncidentSummary(ctx context.Context, summary domain.IncidentSummary, viewerID int64, since time.Time) (domain.IncidentSummary, error) {
	voteSummary, err := s.incidentVoteSummary(ctx, summary.ID, viewerID)
	if err != nil {
		return domain.IncidentSummary{}, err
	}
	comments, err := s.store.ListIncidentComments(ctx, summary.ID, maxIncidentComments)
	if err != nil {
		return domain.IncidentSummary{}, err
	}
	voteEvents, err := s.store.ListIncidentVoteEvents(ctx, summary.ID, since, maxIncidentVoteEvents)
	if err != nil {
		return domain.IncidentSummary{}, err
	}
	summary.Votes = voteSummary
	summary.CommentCount = len(comments)
	if summary.LastActivityAt.IsZero() {
		summary.LastActivityAt = summary.LastReportAt
	}
	if strings.TrimSpace(summary.LastActivityName) == "" {
		summary.LastActivityName = summary.LastReportName
	}
	if strings.TrimSpace(summary.LastActivityActor) == "" {
		summary.LastActivityActor = summary.LastReporter
	}
	for _, comment := range comments {
		if comment.CreatedAt.Before(since) {
			continue
		}
		if comment.CreatedAt.After(summary.LastActivityAt) {
			summary.LastActivityAt = comment.CreatedAt
			summary.LastActivityName = incidentCommentActivityLabel()
			summary.LastActivityActor = comment.Nickname
		}
	}
	for _, voteEvent := range voteEvents {
		if voteEvent.Value != domain.IncidentVoteOngoing {
			continue
		}
		if voteEvent.CreatedAt.After(summary.LastActivityAt) {
			summary.LastActivityAt = voteEvent.CreatedAt
			summary.LastActivityName = incidentVoteEventLabel(voteEvent.Value)
			summary.LastActivityActor = voteEvent.Nickname
		}
	}
	return summary, nil
}

func (s *Service) incidentVoteSummary(ctx context.Context, incidentID string, viewerID int64) (domain.IncidentVoteSummary, error) {
	votes, err := s.store.ListIncidentVotes(ctx, incidentID)
	if err != nil {
		return domain.IncidentVoteSummary{}, err
	}
	summary := domain.IncidentVoteSummary{}
	for _, vote := range votes {
		switch vote.Value {
		case domain.IncidentVoteOngoing:
			summary.Ongoing++
		case domain.IncidentVoteCleared:
			summary.Cleared++
		}
		if viewerID > 0 && vote.UserID == viewerID {
			summary.UserValue = vote.Value
		}
	}
	return summary, nil
}

func (s *Service) cachedTrain(ctx context.Context, cache map[string]*domain.TrainInstance, trainID string) (*domain.TrainInstance, error) {
	if item, ok := cache[trainID]; ok {
		return item, nil
	}
	item, err := s.store.GetTrainInstanceByID(ctx, trainID)
	if err != nil {
		return nil, err
	}
	cache[trainID] = item
	return item, nil
}

func (s *Service) cachedTrainStops(ctx context.Context, cache map[string][]domain.TrainStop, trainID string) ([]domain.TrainStop, error) {
	if item, ok := cache[trainID]; ok {
		return item, nil
	}
	items, err := s.store.ListTrainStops(ctx, trainID)
	if err != nil {
		return nil, err
	}
	cache[trainID] = items
	return items, nil
}

func resolveTrainIncidentContext(train *domain.TrainInstance, stops []domain.TrainStop, at time.Time) (string, string) {
	const maxDistance = 20 * time.Minute
	bestID := ""
	bestName := ""
	bestDistance := maxDistance + time.Second
	bestUpcoming := false
	for _, stop := range stops {
		passAt := trainStopPassAt(stop)
		if passAt == nil {
			continue
		}
		distance := at.Sub(*passAt)
		upcoming := distance <= 0
		if distance < 0 {
			distance = -distance
		}
		if distance > maxDistance {
			continue
		}
		if distance < bestDistance || (distance == bestDistance && upcoming && !bestUpcoming) {
			bestID = stop.StationID
			bestName = stop.StationName
			bestDistance = distance
			bestUpcoming = upcoming
		}
	}
	if bestID != "" {
		return bestID, bestName
	}
	if train != nil {
		fallbackName := strings.TrimSpace(train.FromStation + " -> " + train.ToStation)
		return "route:" + sanitizeIncidentKey(fallbackName), fallbackName
	}
	return "route:unknown", "Route"
}

func trainStopPassAt(stop domain.TrainStop) *time.Time {
	if stop.DepartureAt != nil {
		return stop.DepartureAt
	}
	return stop.ArrivalAt
}

func stationIncidentContext(item domain.StationSighting) (string, string) {
	if item.MatchedTrainInstanceID != nil && strings.TrimSpace(*item.MatchedTrainInstanceID) != "" {
		if item.DestinationStationName != "" {
			return "train:" + strings.TrimSpace(*item.MatchedTrainInstanceID), "Platform sighting to " + item.DestinationStationName
		}
		return "train:" + strings.TrimSpace(*item.MatchedTrainInstanceID), "Platform sighting"
	}
	if item.DestinationStationID != nil && strings.TrimSpace(*item.DestinationStationID) != "" {
		if item.DestinationStationName != "" {
			return "destination:" + strings.TrimSpace(*item.DestinationStationID), "Platform sighting to " + item.DestinationStationName
		}
		return "destination:" + strings.TrimSpace(*item.DestinationStationID), "Platform sighting"
	}
	return "station", "Platform sighting"
}

func trainSignalIncidentLabel(signal domain.SignalType) string {
	switch signal {
	case domain.SignalInspectionStarted:
		return "Inspection started"
	case domain.SignalInspectionInCar:
		return "Inspection in carriage"
	case domain.SignalInspectionEnded:
		return "Inspection ended"
	default:
		return strings.TrimSpace(string(signal))
	}
}

func incidentCommentActivityLabel() string {
	return "Comment"
}

func incidentVoteEventLabel(value domain.IncidentVoteValue) string {
	switch value {
	case domain.IncidentVoteOngoing:
		return "Still there"
	case domain.IncidentVoteCleared:
		return "Cleared"
	default:
		return "Vote"
	}
}

func incidentDayKey(at time.Time) string {
	return at.Format("2006-01-02")
}

func incidentDayStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
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
