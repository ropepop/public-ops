package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"satiksmebot/internal/model"
)

type legacyAreaPrivacyGroup struct {
	storageIncidentID string
	publicIncidentID  string
	opaqueScopeKey    string
	logicalScopeKey   string
	reports           []model.AreaReport
}

type legacyVehiclePrivacyGroup struct {
	storageIncidentID string
	publicIncidentID  string
	logicalScopeKey   string
	sightingCount     int
}

// RepairLegacyVehiclePrivacy moves legacy incident activity onto opaque IDs
// while preserving every retained v1 sighting scope byte-for-byte. New rows
// use an opaque stored compatibility scope, but old raw scopes are private and
// remain the authoritative logical identity for alias and collision handling.
func (s *Service) RepairLegacyVehiclePrivacy(ctx context.Context, since time.Time) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("reports store unavailable")
	}
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	vehicleSightings, err := s.store.ListVehicleSightingsSince(ctx, since.UTC(), "", 0)
	if err != nil {
		return 0, fmt.Errorf("list vehicle sightings for privacy repair: %w", err)
	}

	groups := make(map[string]*legacyVehiclePrivacyGroup)
	logicalScopeByPublicID := make(map[string]string)
	for index := range vehicleSightings {
		item := vehicleSightings[index]
		logicalScopeKey := vehicleSightingScopeKey(&item)
		publicIncidentID := VehicleIncidentID(logicalScopeKey)
		if existing, ok := logicalScopeByPublicID[publicIncidentID]; ok && existing != logicalScopeKey {
			return 0, fmt.Errorf("opaque vehicle incident collision requires manual privacy repair")
		}
		logicalScopeByPublicID[publicIncidentID] = logicalScopeKey

		storedScopeKey := strings.TrimSpace(item.ScopeKey)
		if isOpaqueVehicleScopeKey(storedScopeKey) {
			if strings.ToLower(storedScopeKey) != opaqueVehicleScopeKey(logicalScopeKey) {
				return 0, fmt.Errorf("opaque vehicle scope does not match its sighting")
			}
			continue
		}
		if storedScopeKey == "" {
			storedScopeKey = logicalScopeKey
		}
		storageIncidentID := legacyVehicleIncidentID(storedScopeKey)
		group := groups[storageIncidentID]
		if group == nil {
			group = &legacyVehiclePrivacyGroup{
				storageIncidentID: storageIncidentID,
				publicIncidentID:  publicIncidentID,
				logicalScopeKey:   logicalScopeKey,
			}
			groups[storageIncidentID] = group
		} else if group.publicIncidentID != publicIncidentID || group.logicalScopeKey != logicalScopeKey {
			return 0, fmt.Errorf("legacy vehicle scope collision requires manual privacy repair")
		}
		group.sightingCount++
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	repaired := 0
	for _, key := range keys {
		group := groups[key]
		if err := s.copyLegacyPrivacyActivity(ctx, group.storageIncidentID, group.publicIncidentID); err != nil {
			return repaired, fmt.Errorf("copy legacy vehicle activity for privacy repair: %w", err)
		}
		repaired += group.sightingCount
	}
	return repaired, nil
}

// RepairLegacyAreaPrivacy rewrites area reports created by the older live
// Spacetime module so that its own public projections use the same opaque area
// IDs as current source. The operation is idempotent and runs before the web
// server or chat analyzer starts.
func (s *Service) RepairLegacyAreaPrivacy(ctx context.Context, since time.Time) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("reports store unavailable")
	}
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	areaReports, err := s.store.ListAreaReportsSince(ctx, since.UTC(), 0)
	if err != nil {
		return 0, fmt.Errorf("list area reports for privacy repair: %w", err)
	}

	groups := make(map[string]*legacyAreaPrivacyGroup)
	logicalScopeByPublicID := make(map[string]string)
	for _, item := range areaReports {
		scopeKey := areaReportScopeKey(&item)
		logicalScopeKey := AreaScopeKey(model.AreaReportInput{
			Latitude:     item.Latitude,
			Longitude:    item.Longitude,
			RadiusMeters: item.RadiusMeters,
			Description:  item.Description,
		})
		publicIncidentID := AreaIncidentID(logicalScopeKey)
		opaqueScopeKey := opaqueAreaScopeKey(logicalScopeKey)
		if existing, ok := logicalScopeByPublicID[publicIncidentID]; ok && existing != logicalScopeKey {
			return 0, fmt.Errorf("opaque area incident collision requires manual privacy repair")
		}
		logicalScopeByPublicID[publicIncidentID] = logicalScopeKey
		if isOpaqueAreaScopeKey(scopeKey) {
			if strings.ToLower(strings.TrimSpace(scopeKey)) != opaqueScopeKey {
				return 0, fmt.Errorf("opaque area scope does not match its report")
			}
			continue
		}
		storageIncidentID := legacyAreaIncidentID(scopeKey)
		group := groups[storageIncidentID]
		if group == nil {
			group = &legacyAreaPrivacyGroup{
				storageIncidentID: storageIncidentID,
				publicIncidentID:  publicIncidentID,
				opaqueScopeKey:    opaqueScopeKey,
				logicalScopeKey:   logicalScopeKey,
			}
			groups[storageIncidentID] = group
		} else if group.publicIncidentID != publicIncidentID || group.opaqueScopeKey != opaqueScopeKey || group.logicalScopeKey != logicalScopeKey {
			return 0, fmt.Errorf("legacy area scope collision requires manual privacy repair")
		}
		group.reports = append(group.reports, item)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	repaired := 0
	for _, key := range keys {
		group := groups[key]
		if err := s.copyLegacyPrivacyActivity(ctx, group.storageIncidentID, group.publicIncidentID); err != nil {
			return repaired, fmt.Errorf("copy legacy area activity for privacy repair: %w", err)
		}
		for _, item := range group.reports {
			item.ScopeKey = group.opaqueScopeKey
			if err := s.store.InsertAreaReport(ctx, item); err != nil {
				return repaired, fmt.Errorf("rewrite legacy area report for privacy repair: %w", err)
			}
			repaired++
		}
	}

	remaining, err := s.store.ListAreaReportsSince(ctx, since.UTC(), 0)
	if err != nil {
		return repaired, fmt.Errorf("verify area report privacy repair: %w", err)
	}
	for index := range remaining {
		if !isOpaqueAreaScopeKey(areaReportScopeKey(&remaining[index])) {
			return repaired, fmt.Errorf("area report privacy repair left a readable scope")
		}
	}
	return repaired, nil
}

func (s *Service) copyLegacyPrivacyActivity(ctx context.Context, storageIncidentID, publicIncidentID string) error {
	oldVotes, err := s.store.ListIncidentVotes(ctx, storageIncidentID)
	if err != nil {
		return err
	}
	newVotes, err := s.store.ListIncidentVotes(ctx, publicIncidentID)
	if err != nil {
		return err
	}
	oldEvents, err := s.store.ListIncidentVoteEvents(ctx, storageIncidentID, time.Unix(0, 0).UTC(), 0)
	if err != nil {
		return err
	}
	newEvents, err := s.store.ListIncidentVoteEvents(ctx, publicIncidentID, time.Unix(0, 0).UTC(), 0)
	if err != nil {
		return err
	}
	mergedVotes, err := mergeLegacyPrivacyVotes(storageIncidentID, publicIncidentID, oldVotes, newVotes, oldEvents, newEvents)
	if err != nil {
		return err
	}
	existingEvents := make(map[string]model.IncidentVoteEvent, len(newEvents))
	for _, event := range newEvents {
		if err := validatePrivacyEvent(event, publicIncidentID); err != nil {
			return err
		}
		if existing, ok := existingEvents[event.ID]; ok && !samePrivacyEvent(existing, event) {
			return fmt.Errorf("conflicting opaque vote event requires manual privacy repair")
		}
		existingEvents[event.ID] = event
	}
	sort.SliceStable(oldEvents, func(left, right int) bool {
		if oldEvents[left].CreatedAt.Equal(oldEvents[right].CreatedAt) {
			return oldEvents[left].ID < oldEvents[right].ID
		}
		return oldEvents[left].CreatedAt.Before(oldEvents[right].CreatedAt)
	})
	for _, event := range oldEvents {
		if err := validatePrivacyEvent(event, storageIncidentID); err != nil {
			return err
		}
		if existing, exists := existingEvents[event.ID]; exists {
			if !samePrivacyEvent(existing, event) {
				return fmt.Errorf("conflicting vote event requires manual privacy repair")
			}
			continue
		}
		movedEvent := event
		movedEvent.IncidentID = publicIncidentID
		vote, ok := mergedVotes[event.UserID]
		if !ok {
			return fmt.Errorf("vote event has no recoverable current vote")
		}
		if err := s.store.RecordIncidentVote(ctx, vote, movedEvent); err != nil {
			return err
		}
		existingEvents[event.ID] = movedEvent
	}
	userIDs := make([]int64, 0, len(mergedVotes))
	for userID := range mergedVotes {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(left, right int) bool { return userIDs[left] < userIDs[right] })
	for _, userID := range userIDs {
		if err := s.store.UpsertIncidentVote(ctx, mergedVotes[userID]); err != nil {
			return err
		}
	}

	oldComments, err := s.store.ListIncidentComments(ctx, storageIncidentID, 0)
	if err != nil {
		return err
	}
	newComments, err := s.store.ListIncidentComments(ctx, publicIncidentID, 0)
	if err != nil {
		return err
	}
	existingComments := make(map[string]model.IncidentComment, len(newComments))
	for _, comment := range newComments {
		if err := validatePrivacyComment(comment, publicIncidentID); err != nil {
			return err
		}
		if existing, ok := existingComments[comment.ID]; ok && !samePrivacyComment(existing, comment) {
			return fmt.Errorf("conflicting opaque comment requires manual privacy repair")
		}
		existingComments[comment.ID] = comment
	}
	for _, comment := range oldComments {
		if err := validatePrivacyComment(comment, storageIncidentID); err != nil {
			return err
		}
		if existing, exists := existingComments[comment.ID]; exists {
			if !samePrivacyComment(existing, comment) {
				return fmt.Errorf("conflicting comment requires manual privacy repair")
			}
			continue
		}
		comment.IncidentID = publicIncidentID
		if err := s.store.InsertIncidentComment(ctx, comment); err != nil {
			return err
		}
		existingComments[comment.ID] = comment
	}
	return nil
}

func mergeLegacyPrivacyVotes(storageIncidentID, publicIncidentID string, oldVotes, newVotes []model.IncidentVote, oldEvents, newEvents []model.IncidentVoteEvent) (map[int64]model.IncidentVote, error) {
	merged := make(map[int64]model.IncidentVote, len(oldVotes)+len(newVotes))
	for _, group := range []struct {
		incidentID string
		votes      []model.IncidentVote
	}{
		{incidentID: storageIncidentID, votes: oldVotes},
		{incidentID: publicIncidentID, votes: newVotes},
	} {
		for _, vote := range group.votes {
			if err := validatePrivacyVote(vote, group.incidentID); err != nil {
				return nil, err
			}
			vote.IncidentID = publicIncidentID
			if err := mergePrivacyVote(merged, vote); err != nil {
				return nil, err
			}
		}
	}
	for _, group := range []struct {
		incidentID string
		events     []model.IncidentVoteEvent
	}{
		{incidentID: storageIncidentID, events: oldEvents},
		{incidentID: publicIncidentID, events: newEvents},
	} {
		for _, event := range group.events {
			if err := validatePrivacyEvent(event, group.incidentID); err != nil {
				return nil, err
			}
			vote := model.IncidentVote{
				IncidentID: publicIncidentID,
				UserID:     event.UserID,
				Nickname:   event.Nickname,
				Value:      event.Value,
				CreatedAt:  event.CreatedAt,
				UpdatedAt:  event.CreatedAt,
			}
			if err := mergePrivacyVote(merged, vote); err != nil {
				return nil, err
			}
		}
	}
	return merged, nil
}

func mergePrivacyVote(merged map[int64]model.IncidentVote, candidate model.IncidentVote) error {
	candidate.Nickname = strings.TrimSpace(candidate.Nickname)
	candidate.CreatedAt, candidate.UpdatedAt = normalizedPrivacyVoteTimes(candidate.CreatedAt, candidate.UpdatedAt)
	existing, ok := merged[candidate.UserID]
	if !ok {
		merged[candidate.UserID] = candidate
		return nil
	}
	existing.CreatedAt, existing.UpdatedAt = normalizedPrivacyVoteTimes(existing.CreatedAt, existing.UpdatedAt)
	earliest := earlierPrivacyTime(existing.CreatedAt, candidate.CreatedAt)
	switch {
	case candidate.UpdatedAt.After(existing.UpdatedAt):
		candidate.CreatedAt = earliest
		merged[candidate.UserID] = candidate
	case candidate.UpdatedAt.Before(existing.UpdatedAt):
		existing.CreatedAt = earliest
		merged[candidate.UserID] = existing
	default:
		if existing.Value != candidate.Value {
			return fmt.Errorf("conflicting current vote requires manual privacy repair")
		}
		if existing.Nickname != "" && candidate.Nickname != "" && existing.Nickname != candidate.Nickname {
			return fmt.Errorf("conflicting current voter requires manual privacy repair")
		}
		if existing.Nickname == "" {
			existing.Nickname = candidate.Nickname
		}
		existing.CreatedAt = earliest
		merged[candidate.UserID] = existing
	}
	return nil
}

func normalizedPrivacyVoteTimes(createdAt, updatedAt time.Time) (time.Time, time.Time) {
	if createdAt.IsZero() {
		createdAt = updatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	return createdAt.UTC(), updatedAt.UTC()
}

func earlierPrivacyTime(left, right time.Time) time.Time {
	if left.IsZero() {
		return right
	}
	if right.IsZero() || left.Before(right) {
		return left
	}
	return right
}

func validatePrivacyVote(vote model.IncidentVote, incidentID string) error {
	if strings.TrimSpace(vote.IncidentID) != strings.TrimSpace(incidentID) || vote.UserID <= 0 || !validPrivacyVoteValue(vote.Value) {
		return fmt.Errorf("invalid current vote requires manual privacy repair")
	}
	createdAt, updatedAt := normalizedPrivacyVoteTimes(vote.CreatedAt, vote.UpdatedAt)
	if createdAt.IsZero() || updatedAt.IsZero() {
		return fmt.Errorf("undated current vote requires manual privacy repair")
	}
	return nil
}

func validatePrivacyEvent(event model.IncidentVoteEvent, incidentID string) error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.IncidentID) != strings.TrimSpace(incidentID) || event.UserID <= 0 || !validPrivacyVoteValue(event.Value) || strings.TrimSpace(string(event.Source)) == "" || event.CreatedAt.IsZero() {
		return fmt.Errorf("invalid vote event requires manual privacy repair")
	}
	return nil
}

func validatePrivacyComment(comment model.IncidentComment, incidentID string) error {
	if strings.TrimSpace(comment.ID) == "" || strings.TrimSpace(comment.IncidentID) != strings.TrimSpace(incidentID) || comment.UserID <= 0 || strings.TrimSpace(comment.Body) == "" || comment.CreatedAt.IsZero() {
		return fmt.Errorf("invalid comment requires manual privacy repair")
	}
	return nil
}

func validPrivacyVoteValue(value model.IncidentVoteValue) bool {
	return value == model.IncidentVoteOngoing || value == model.IncidentVoteCleared
}

func samePrivacyEvent(left, right model.IncidentVoteEvent) bool {
	return strings.TrimSpace(left.ID) == strings.TrimSpace(right.ID) &&
		left.UserID == right.UserID &&
		strings.TrimSpace(left.Nickname) == strings.TrimSpace(right.Nickname) &&
		left.Value == right.Value &&
		left.Source == right.Source &&
		left.CreatedAt.Equal(right.CreatedAt)
}

func samePrivacyComment(left, right model.IncidentComment) bool {
	return strings.TrimSpace(left.ID) == strings.TrimSpace(right.ID) &&
		left.UserID == right.UserID &&
		strings.TrimSpace(left.Nickname) == strings.TrimSpace(right.Nickname) &&
		strings.TrimSpace(left.Body) == strings.TrimSpace(right.Body) &&
		left.CreatedAt.Equal(right.CreatedAt)
}
