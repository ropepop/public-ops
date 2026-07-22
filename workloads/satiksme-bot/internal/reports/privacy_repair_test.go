package reports

import (
	"context"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

func TestVehicleIncidentIDMatchesSpacetimeUTF16FNV(t *testing.T) {
	tests := []struct {
		scope string
		want  string
	}{
		{scope: "live:bus:22:outbound:private-row-42", want: "vehicle:pub-63b763c0"},
		{scope: "fallback:tram:1:A:centrālā stacija", want: "vehicle:pub-aa859ab4"},
		{scope: "  🚌:Ā  ", want: "vehicle:pub-1c201fc6"},
		{scope: "pub-63b763c0", want: "vehicle:pub-63b763c0"},
	}
	for _, test := range tests {
		if got := VehicleIncidentID(test.scope); got != test.want {
			t.Fatalf("VehicleIncidentID(%q) = %q, want %q", test.scope, got, test.want)
		}
	}
}

func TestRedactPublicIncidentCommentUsesCanonicalOpaqueID(t *testing.T) {
	createdAt := time.Date(2026, 7, 22, 12, 34, 56, 987000000, time.UTC)
	comment := model.IncidentComment{
		ID: "raw-comment-42", IncidentID: "vehicle:pub-63b763c0",
		UserID: 42, Nickname: "private", Body: "public body", CreatedAt: createdAt,
	}
	got := redactPublicIncidentComment(comment)
	if got.ID != "incident-comment:pub-04966f8a" {
		t.Fatalf("public comment ID = %q, want TS-compatible opaque ID", got.ID)
	}
	if !got.CreatedAt.Equal(createdAt.Truncate(time.Second)) || got.Nickname != publicIncidentActorLabel {
		t.Fatalf("public comment redaction = %+v", got)
	}
	second := redactPublicIncidentComment(got)
	if second.ID != got.ID {
		t.Fatalf("public comment ID changed on second redaction: first=%q second=%q", got.ID, second.ID)
	}
}

func TestRepairLegacyVehiclePrivacyPreservesScopeAndActivity(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC)
	legacyScope := "live:bus:22:V1-Direction:private-row-42"
	legacyID := legacyVehicleIncidentID(legacyScope)
	publicID := VehicleIncidentID(legacyScope)
	createdAt := now.Add(-40 * time.Minute)
	sighting := model.VehicleSighting{
		ID:               "legacy-vehicle-one",
		UserID:           71,
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "different-normalized-direction",
		Destination:      "Centrs",
		DepartureSeconds: 60,
		LiveRowID:        "private-row-42",
		ScopeKey:         legacyScope,
		CreatedAt:        now.Add(-45 * time.Minute),
	}
	if err := st.InsertVehicleSighting(ctx, sighting); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: legacyID, UserID: 101, Nickname: model.GenericNickname(101),
		Value: model.IncidentVoteOngoing, CreatedAt: createdAt, UpdatedAt: createdAt,
	}, model.IncidentVoteEvent{
		ID: "legacy-vehicle-event-one", IncidentID: legacyID, UserID: 101,
		Nickname: model.GenericNickname(101), Value: model.IncidentVoteOngoing,
		Source: model.IncidentVoteSourceMapReport, CreatedAt: createdAt,
	})
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: legacyID, UserID: 101, Nickname: model.GenericNickname(101),
		Value: model.IncidentVoteCleared, CreatedAt: createdAt, UpdatedAt: now.Add(-20 * time.Minute),
	}, model.IncidentVoteEvent{
		ID: "legacy-vehicle-event-two", IncidentID: legacyID, UserID: 101,
		Nickname: model.GenericNickname(101), Value: model.IncidentVoteCleared,
		Source: model.IncidentVoteSourceVote, CreatedAt: now.Add(-20 * time.Minute),
	})
	if err := st.UpsertIncidentVote(ctx, model.IncidentVote{
		IncidentID: legacyID, UserID: 102, Nickname: model.GenericNickname(102),
		Value: model.IncidentVoteOngoing, CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertIncidentVote(no-event) error = %v", err)
	}
	// Simulate a partial run that already moved one exact-ID event.
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: publicID, UserID: 101, Nickname: model.GenericNickname(101),
		Value: model.IncidentVoteOngoing, CreatedAt: createdAt, UpdatedAt: createdAt,
	}, model.IncidentVoteEvent{
		ID: "legacy-vehicle-event-one", IncidentID: publicID, UserID: 101,
		Nickname: model.GenericNickname(101), Value: model.IncidentVoteOngoing,
		Source: model.IncidentVoteSourceMapReport, CreatedAt: createdAt,
	})
	insertPrivacyComment(t, ctx, st, model.IncidentComment{
		ID: "legacy-vehicle-comment", IncidentID: legacyID, UserID: 201,
		Nickname: model.GenericNickname(201), Body: "saglabāts komentārs", CreatedAt: now.Add(-15 * time.Minute),
	})

	repaired, err := svc.RepairLegacyVehiclePrivacy(ctx, now.Add(-time.Hour))
	if err != nil || repaired != 1 {
		t.Fatalf("RepairLegacyVehiclePrivacy() repaired=%d err=%v, want one", repaired, err)
	}
	items, err := st.ListVehicleSightingsSince(ctx, now.Add(-time.Hour), "", 0)
	if err != nil || len(items) != 1 || items[0].ScopeKey != legacyScope {
		t.Fatalf("retained vehicle scope changed: items=%+v err=%v", items, err)
	}
	assertPrivacyEventIDs(t, ctx, st, publicID, []string{"legacy-vehicle-event-one", "legacy-vehicle-event-two"})
	assertPrivacyEventIDs(t, ctx, st, legacyID, nil)
	assertPrivacyCommentIDs(t, ctx, st, publicID, []string{"legacy-vehicle-comment"})
	assertPrivacyCommentIDs(t, ctx, st, legacyID, nil)
	votes, err := st.ListIncidentVotes(ctx, publicID)
	if err != nil {
		t.Fatalf("ListIncidentVotes() error = %v", err)
	}
	byUser := privacyVotesByUser(votes)
	if got := byUser[101]; got.Value != model.IncidentVoteCleared || !got.CreatedAt.Equal(createdAt) || !got.UpdatedAt.Equal(now.Add(-20*time.Minute)) {
		t.Fatalf("latest migrated vote = %+v", got)
	}
	if _, ok := byUser[102]; !ok {
		t.Fatal("current vote without an event was not preserved")
	}

	if repairedAgain, secondErr := svc.RepairLegacyVehiclePrivacy(ctx, now.Add(-time.Hour)); secondErr != nil || repairedAgain != 1 {
		t.Fatalf("idempotent RepairLegacyVehiclePrivacy() repaired=%d err=%v", repairedAgain, secondErr)
	}
	assertPrivacyEventIDs(t, ctx, st, publicID, []string{"legacy-vehicle-event-one", "legacy-vehicle-event-two"})
	assertPrivacyCommentIDs(t, ctx, st, publicID, []string{"legacy-vehicle-comment"})

	detail, err := svc.IncidentDetail(ctx, &model.Catalog{}, legacyID, now, 0)
	if err != nil || detail == nil || detail.Summary.ID != publicID {
		t.Fatalf("IncidentDetail(legacy vehicle link) = %+v err=%v", detail, err)
	}
	publicStore := &privacyPublicReadStore{SQLiteStore: st, scope: "vehicle"}
	publicSvc := NewService(publicStore, 3*time.Minute, 90*time.Second, 30*time.Minute)
	publicDetail, err := publicSvc.IncidentDetail(ctx, &model.Catalog{}, legacyID, now, 0)
	if err != nil || publicDetail.Summary.ID != publicID || publicStore.requestedIncidentID != publicID {
		t.Fatalf("public legacy vehicle alias requested=%q detail=%+v err=%v", publicStore.requestedIncidentID, publicDetail, err)
	}
	if _, err := svc.VoteIncident(ctx, &model.Catalog{}, legacyID, 401, model.IncidentVoteOngoing, now); err != nil {
		t.Fatalf("VoteIncident(legacy vehicle link) error = %v", err)
	}
	comment, err := svc.AddIncidentComment(ctx, &model.Catalog{}, legacyID, 402, "pārejas saite", now)
	if err != nil || comment.IncidentID != publicID {
		t.Fatalf("AddIncidentComment(legacy vehicle link) = %+v err=%v", comment, err)
	}
}

func TestRepairLegacyVehiclePrivacyFailsClosedOnCollision(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	scopes := []string{
		"fallback:bus:22:out:91mwkf1443bn0bcqzjp",
		"fallback:bus:22:out:1oousxzm915islowj8d",
	}
	if scopes[0] == scopes[1] || VehicleIncidentID(scopes[0]) != VehicleIncidentID(scopes[1]) {
		t.Fatal("test scopes no longer form the expected deterministic FNV collision")
	}
	for index, scopeKey := range scopes {
		if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
			ID: "vehicle-collision-" + string(rune('a'+index)), UserID: int64(501 + index),
			Mode: "bus", RouteLabel: "22", Direction: "out", Destination: "private",
			ScopeKey: scopeKey, CreatedAt: now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("InsertVehicleSighting(collision %d) error = %v", index, err)
		}
	}
	if repaired, err := svc.RepairLegacyVehiclePrivacy(ctx, now.Add(-time.Hour)); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("RepairLegacyVehiclePrivacy() repaired=%d err=%v, want collision refusal", repaired, err)
	}
}

func TestSubmitVehicleSightingFailsClosedOnOpaqueIDCollision(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC)
	firstScope := "fallback:bus:22:out:91mwkf1443bn0bcqzjp"
	secondDestination := "1oousxzm915islowj8d"
	secondInput := model.VehicleReportInput{Mode: "bus", RouteLabel: "22", Direction: "out", Destination: secondDestination}
	if VehicleIncidentID(firstScope) != VehicleIncidentID(VehicleScopeKey(secondInput)) {
		t.Fatal("test inputs no longer form the expected deterministic FNV collision")
	}
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID: "existing-vehicle-collision", UserID: 601, Mode: "bus", RouteLabel: "22",
		Direction: "out", Destination: "first", ScopeKey: firstScope, CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertVehicleSighting(existing) error = %v", err)
	}
	result, item, err := svc.SubmitVehicleSighting(ctx, 602, secondInput, now)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("SubmitVehicleSighting() result=%+v item=%+v err=%v, want collision refusal", result, item, err)
	}
	items, listErr := st.ListVehicleSightingsSince(ctx, now.Add(-time.Hour), "", 0)
	if listErr != nil || len(items) != 1 {
		t.Fatalf("vehicle sightings after collision = %+v err=%v", items, listErr)
	}
}

func TestRepairLegacyVehiclePrivacyFailsClosedOnConflictingSameIDActivity(t *testing.T) {
	ctx, st, _ := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	scopeKey := "live:bus:22:out:private-row"
	legacyID := legacyVehicleIncidentID(scopeKey)
	publicID := VehicleIncidentID(scopeKey)
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID: "conflict-vehicle", UserID: 701, Mode: "bus", RouteLabel: "22",
		Direction: "out", LiveRowID: "private-row", ScopeKey: scopeKey, CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}
	conflictingStore := &vehiclePrivacyConflictStore{
		SQLiteStore: st, legacyID: legacyID, publicID: publicID, createdAt: now.Add(-30 * time.Second),
	}
	svc := NewService(conflictingStore, 3*time.Minute, 90*time.Second, 30*time.Minute)
	if repaired, err := svc.RepairLegacyVehiclePrivacy(ctx, now.Add(-time.Hour)); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("RepairLegacyVehiclePrivacy() repaired=%d err=%v, want conflicting-ID refusal", repaired, err)
	}
}

func TestRepairLegacyAreaPrivacyPreservesActivityAndRecoversPartialRun(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

	firstInput := model.AreaReportInput{
		Latitude:     56.9501,
		Longitude:    24.1103,
		RadiusMeters: 500,
		Description:  "starp pieturām",
	}
	firstScope := AreaScopeKey(firstInput)
	firstLegacyID := legacyAreaIncidentID(firstScope)
	firstPublicID := AreaIncidentID(firstScope)
	firstReport := model.AreaReport{
		ID:           "legacy-area-one",
		UserID:       41,
		Latitude:     firstInput.Latitude,
		Longitude:    firstInput.Longitude,
		RadiusMeters: firstInput.RadiusMeters,
		Description:  firstInput.Description,
		ScopeKey:     firstScope,
		CreatedAt:    now.Add(-45 * time.Minute),
	}
	if err := st.InsertAreaReport(ctx, firstReport); err != nil {
		t.Fatalf("InsertAreaReport(first) error = %v", err)
	}
	firstCreatedAt := now.Add(-40 * time.Minute)
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: firstLegacyID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  firstCreatedAt,
		UpdatedAt:  firstCreatedAt,
	}, model.IncidentVoteEvent{
		ID:         "legacy-event-one",
		IncidentID: firstLegacyID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteOngoing,
		Source:     model.IncidentVoteSourceMapReport,
		CreatedAt:  firstCreatedAt,
	})
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: firstLegacyID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteCleared,
		CreatedAt:  firstCreatedAt,
		UpdatedAt:  now.Add(-20 * time.Minute),
	}, model.IncidentVoteEvent{
		ID:         "legacy-event-two",
		IncidentID: firstLegacyID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteCleared,
		Source:     model.IncidentVoteSourceVote,
		CreatedAt:  now.Add(-20 * time.Minute),
	})
	if err := st.UpsertIncidentVote(ctx, model.IncidentVote{
		IncidentID: firstLegacyID,
		UserID:     102,
		Nickname:   model.GenericNickname(102),
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  now.Add(-30 * time.Minute),
		UpdatedAt:  now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertIncidentVote(no-event) error = %v", err)
	}
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: firstPublicID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  firstCreatedAt,
		UpdatedAt:  now.Add(-5 * time.Minute),
	}, model.IncidentVoteEvent{
		ID:         "opaque-event-newer",
		IncidentID: firstPublicID,
		UserID:     101,
		Nickname:   model.GenericNickname(101),
		Value:      model.IncidentVoteOngoing,
		Source:     model.IncidentVoteSourceVote,
		CreatedAt:  now.Add(-5 * time.Minute),
	})
	if err := st.UpsertIncidentVote(ctx, model.IncidentVote{
		IncidentID: firstPublicID,
		UserID:     103,
		Nickname:   model.GenericNickname(103),
		Value:      model.IncidentVoteCleared,
		CreatedAt:  now.Add(-15 * time.Minute),
		UpdatedAt:  now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertIncidentVote(existing opaque) error = %v", err)
	}
	insertPrivacyComment(t, ctx, st, model.IncidentComment{
		ID:         "legacy-comment",
		IncidentID: firstLegacyID,
		UserID:     201,
		Nickname:   model.GenericNickname(201),
		Body:       "vecais komentārs",
		CreatedAt:  now.Add(-12 * time.Minute),
	})
	insertPrivacyComment(t, ctx, st, model.IncidentComment{
		ID:         "opaque-comment",
		IncidentID: firstPublicID,
		UserID:     202,
		Nickname:   model.GenericNickname(202),
		Body:       "jaunais komentārs",
		CreatedAt:  now.Add(-8 * time.Minute),
	})

	secondInput := model.AreaReportInput{
		Latitude:     56.9601,
		Longitude:    24.1203,
		RadiusMeters: 300,
		Description:  "daļēji pārvietots",
	}
	secondScope := AreaScopeKey(secondInput)
	secondLegacyID := legacyAreaIncidentID(secondScope)
	secondPublicID := AreaIncidentID(secondScope)
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "legacy-area-two",
		UserID:       42,
		Latitude:     secondInput.Latitude,
		Longitude:    secondInput.Longitude,
		RadiusMeters: secondInput.RadiusMeters,
		Description:  secondInput.Description,
		ScopeKey:     secondScope,
		CreatedAt:    now.Add(-35 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertAreaReport(second) error = %v", err)
	}
	secondCreatedAt := now.Add(-32 * time.Minute)
	firstPartialEvent := model.IncidentVoteEvent{
		ID:         "partial-event-one",
		IncidentID: secondLegacyID,
		UserID:     301,
		Nickname:   model.GenericNickname(301),
		Value:      model.IncidentVoteOngoing,
		Source:     model.IncidentVoteSourceMapReport,
		CreatedAt:  secondCreatedAt,
	}
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: secondLegacyID,
		UserID:     301,
		Nickname:   model.GenericNickname(301),
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  secondCreatedAt,
		UpdatedAt:  secondCreatedAt,
	}, firstPartialEvent)
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: secondLegacyID,
		UserID:     301,
		Nickname:   model.GenericNickname(301),
		Value:      model.IncidentVoteCleared,
		CreatedAt:  secondCreatedAt,
		UpdatedAt:  now.Add(-7 * time.Minute),
	}, model.IncidentVoteEvent{
		ID:         "partial-event-two",
		IncidentID: secondLegacyID,
		UserID:     301,
		Nickname:   model.GenericNickname(301),
		Value:      model.IncidentVoteCleared,
		Source:     model.IncidentVoteSourceVote,
		CreatedAt:  now.Add(-7 * time.Minute),
	})
	// Simulate a process stop after one event moved but before the report scope
	// and final current vote were repaired.
	firstPartialEvent.IncidentID = secondPublicID
	recordPrivacyVoteEvent(t, ctx, st, model.IncidentVote{
		IncidentID: secondPublicID,
		UserID:     301,
		Nickname:   model.GenericNickname(301),
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  secondCreatedAt,
		UpdatedAt:  secondCreatedAt,
	}, firstPartialEvent)

	repaired, err := svc.RepairLegacyAreaPrivacy(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("RepairLegacyAreaPrivacy() error = %v", err)
	}
	if repaired != 2 {
		t.Fatalf("repaired reports = %d, want 2", repaired)
	}

	areaReports, err := st.ListAreaReportsSince(ctx, now.Add(-time.Hour), 0)
	if err != nil || len(areaReports) != 2 {
		t.Fatalf("ListAreaReportsSince() = %+v err=%v, want two", areaReports, err)
	}
	for _, item := range areaReports {
		if !isOpaqueAreaScopeKey(item.ScopeKey) {
			t.Fatalf("repaired report scope is not opaque")
		}
	}
	assertPrivacyEventIDs(t, ctx, st, firstPublicID, []string{"legacy-event-one", "legacy-event-two", "opaque-event-newer"})
	assertPrivacyEventIDs(t, ctx, st, firstLegacyID, nil)
	assertPrivacyEventIDs(t, ctx, st, secondPublicID, []string{"partial-event-one", "partial-event-two"})
	assertPrivacyEventIDs(t, ctx, st, secondLegacyID, nil)

	firstVotes, err := st.ListIncidentVotes(ctx, firstPublicID)
	if err != nil {
		t.Fatalf("ListIncidentVotes(first) error = %v", err)
	}
	firstVotesByUser := privacyVotesByUser(firstVotes)
	if got := firstVotesByUser[101]; got.Value != model.IncidentVoteOngoing || !got.UpdatedAt.Equal(now.Add(-5*time.Minute)) || !got.CreatedAt.Equal(firstCreatedAt) {
		t.Fatalf("merged latest vote = %+v, want newer opaque state with original creation time", got)
	}
	if _, ok := firstVotesByUser[102]; !ok {
		t.Fatalf("current vote without an event was not preserved")
	}
	if _, ok := firstVotesByUser[103]; !ok {
		t.Fatalf("pre-existing opaque current vote was not preserved")
	}
	secondVotes, err := st.ListIncidentVotes(ctx, secondPublicID)
	if err != nil {
		t.Fatalf("ListIncidentVotes(second) error = %v", err)
	}
	if got := privacyVotesByUser(secondVotes)[301]; got.Value != model.IncidentVoteCleared || !got.UpdatedAt.Equal(now.Add(-7*time.Minute)) {
		t.Fatalf("partial-run current vote = %+v, want latest cleared state", got)
	}
	assertPrivacyCommentIDs(t, ctx, st, firstPublicID, []string{"legacy-comment", "opaque-comment"})
	assertPrivacyCommentIDs(t, ctx, st, firstLegacyID, nil)

	repairedAgain, err := svc.RepairLegacyAreaPrivacy(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("second RepairLegacyAreaPrivacy() error = %v", err)
	}
	if repairedAgain != 0 {
		t.Fatalf("second repaired reports = %d, want 0", repairedAgain)
	}
	assertPrivacyEventIDs(t, ctx, st, firstPublicID, []string{"legacy-event-one", "legacy-event-two", "opaque-event-newer"})
	assertPrivacyCommentIDs(t, ctx, st, firstPublicID, []string{"legacy-comment", "opaque-comment"})

	detail, err := svc.IncidentDetail(ctx, &model.Catalog{}, firstLegacyID, now, 0)
	if err != nil {
		t.Fatalf("IncidentDetail(legacy link) error = %v", err)
	}
	if detail == nil || detail.Summary.ID != firstPublicID {
		t.Fatalf("legacy detail summary = %+v, want opaque ID", detail)
	}
	publicStore := &privacyPublicReadStore{SQLiteStore: st}
	publicSvc := NewService(publicStore, 3*time.Minute, 90*time.Second, 30*time.Minute)
	publicDetail, err := publicSvc.IncidentDetail(ctx, &model.Catalog{}, firstLegacyID, now, 0)
	if err != nil {
		t.Fatalf("public IncidentDetail(legacy link) error = %v", err)
	}
	if publicStore.requestedIncidentID != firstPublicID || publicDetail.Summary.ID != firstPublicID {
		t.Fatalf("public legacy alias requested %q and returned %q, want opaque ID", publicStore.requestedIncidentID, publicDetail.Summary.ID)
	}
	if _, err := svc.VoteIncident(ctx, &model.Catalog{}, firstLegacyID, 401, model.IncidentVoteOngoing, now); err != nil {
		t.Fatalf("VoteIncident(legacy link) error = %v", err)
	}
	comment, err := svc.AddIncidentComment(ctx, &model.Catalog{}, firstLegacyID, 402, "pārejas saite", now)
	if err != nil {
		t.Fatalf("AddIncidentComment(legacy link) error = %v", err)
	}
	if comment.IncidentID != firstPublicID {
		t.Fatalf("legacy-link comment incident = %q, want opaque ID", comment.IncidentID)
	}
}

func TestRepairLegacyAreaPrivacyFailsClosedOnOpaqueIDCollision(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	inputs := []model.AreaReportInput{
		{Latitude: 56.95, Longitude: 24.11, RadiusMeters: 500, Description: "test-1nxxkqx"},
		{Latitude: 56.95, Longitude: 24.11, RadiusMeters: 500, Description: "test-1sffz56"},
	}
	if AreaScopeKey(inputs[0]) == AreaScopeKey(inputs[1]) || AreaIncidentID(AreaScopeKey(inputs[0])) != AreaIncidentID(AreaScopeKey(inputs[1])) {
		t.Fatal("test inputs no longer form the expected deterministic FNV collision")
	}
	for index, input := range inputs {
		if err := st.InsertAreaReport(ctx, model.AreaReport{
			ID:           []string{"collision-one", "collision-two"}[index],
			UserID:       int64(501 + index),
			Latitude:     input.Latitude,
			Longitude:    input.Longitude,
			RadiusMeters: input.RadiusMeters,
			Description:  input.Description,
			ScopeKey:     AreaScopeKey(input),
			CreatedAt:    now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("InsertAreaReport(collision %d) error = %v", index, err)
		}
	}
	repaired, err := svc.RepairLegacyAreaPrivacy(ctx, now.Add(-time.Hour))
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("RepairLegacyAreaPrivacy() repaired=%d err=%v, want collision refusal", repaired, err)
	}
	areaReports, listErr := st.ListAreaReportsSince(ctx, now.Add(-time.Hour), 0)
	if listErr != nil || len(areaReports) != 2 {
		t.Fatalf("ListAreaReportsSince() = %+v err=%v", areaReports, listErr)
	}
	for _, item := range areaReports {
		if isOpaqueAreaScopeKey(item.ScopeKey) {
			t.Fatalf("collision refusal rewrote a report")
		}
	}
}

func TestSubmitAreaReportFailsClosedOnOpaqueIDCollision(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 7, 22, 10, 30, 0, 0, time.UTC)
	first := model.AreaReportInput{Latitude: 56.95, Longitude: 24.11, RadiusMeters: 500, Description: "test-1nxxkqx"}
	second := model.AreaReportInput{Latitude: 56.95, Longitude: 24.11, RadiusMeters: 500, Description: "test-1sffz56"}
	if AreaIncidentID(AreaScopeKey(first)) != AreaIncidentID(AreaScopeKey(second)) {
		t.Fatal("test inputs no longer form the expected deterministic FNV collision")
	}
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "existing-collision",
		UserID:       601,
		Latitude:     first.Latitude,
		Longitude:    first.Longitude,
		RadiusMeters: first.RadiusMeters,
		Description:  first.Description,
		ScopeKey:     AreaScopeKey(first),
		CreatedAt:    now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertAreaReport(existing) error = %v", err)
	}
	result, item, err := svc.SubmitAreaReport(ctx, 602, second, now)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("SubmitAreaReport() result=%+v item=%+v err=%v, want collision refusal", result, item, err)
	}
	areaReports, listErr := st.ListAreaReportsSince(ctx, now.Add(-time.Hour), 0)
	if listErr != nil || len(areaReports) != 1 {
		t.Fatalf("area reports after collision = %+v err=%v, want original only", areaReports, listErr)
	}
}

func recordPrivacyVoteEvent(t *testing.T, ctx context.Context, st *store.SQLiteStore, vote model.IncidentVote, event model.IncidentVoteEvent) {
	t.Helper()
	if err := st.RecordIncidentVote(ctx, vote, event); err != nil {
		t.Fatalf("RecordIncidentVote(%s) error = %v", event.ID, err)
	}
}

func insertPrivacyComment(t *testing.T, ctx context.Context, st *store.SQLiteStore, comment model.IncidentComment) {
	t.Helper()
	if err := st.InsertIncidentComment(ctx, comment); err != nil {
		t.Fatalf("InsertIncidentComment(%s) error = %v", comment.ID, err)
	}
}

func assertPrivacyEventIDs(t *testing.T, ctx context.Context, st *store.SQLiteStore, incidentID string, want []string) {
	t.Helper()
	items, err := st.ListIncidentVoteEvents(ctx, incidentID, time.Unix(0, 0).UTC(), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	got := make(map[string]struct{}, len(items))
	for _, item := range items {
		got[item.ID] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d", len(got), len(want))
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing expected event %q", id)
		}
	}
}

func assertPrivacyCommentIDs(t *testing.T, ctx context.Context, st *store.SQLiteStore, incidentID string, want []string) {
	t.Helper()
	items, err := st.ListIncidentComments(ctx, incidentID, 0)
	if err != nil {
		t.Fatalf("ListIncidentComments() error = %v", err)
	}
	got := make(map[string]struct{}, len(items))
	for _, item := range items {
		got[item.ID] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("comment count = %d, want %d", len(got), len(want))
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing expected comment %q", id)
		}
	}
}

func privacyVotesByUser(items []model.IncidentVote) map[int64]model.IncidentVote {
	out := make(map[int64]model.IncidentVote, len(items))
	for _, item := range items {
		out[item.UserID] = item
	}
	return out
}

type privacyPublicReadStore struct {
	*store.SQLiteStore
	requestedIncidentID string
	scope               string
}

type vehiclePrivacyConflictStore struct {
	*store.SQLiteStore
	legacyID  string
	publicID  string
	createdAt time.Time
}

func (s *vehiclePrivacyConflictStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]model.IncidentVoteEvent, error) {
	base, err := s.SQLiteStore.ListIncidentVoteEvents(ctx, incidentID, since, limit)
	if err != nil {
		return nil, err
	}
	switch incidentID {
	case s.legacyID:
		return append(base, model.IncidentVoteEvent{
			ID: "shared-conflicting-event", IncidentID: s.legacyID, UserID: 801,
			Nickname: model.GenericNickname(801), Value: model.IncidentVoteOngoing,
			Source: model.IncidentVoteSourceMapReport, CreatedAt: s.createdAt,
		}), nil
	case s.publicID:
		return append(base, model.IncidentVoteEvent{
			ID: "shared-conflicting-event", IncidentID: s.publicID, UserID: 802,
			Nickname: model.GenericNickname(802), Value: model.IncidentVoteOngoing,
			Source: model.IncidentVoteSourceMapReport, CreatedAt: s.createdAt,
		}), nil
	default:
		return base, nil
	}
}

func (s *privacyPublicReadStore) ListPublicIncidents(context.Context, int64, int) ([]model.IncidentSummary, error) {
	return nil, nil
}

func (s *privacyPublicReadStore) GetPublicIncidentDetail(_ context.Context, incidentID string, _ int64) (*model.IncidentDetail, error) {
	s.requestedIncidentID = incidentID
	scope := s.scope
	if scope == "" {
		scope = IncidentScopeArea
	}
	return &model.IncidentDetail{Summary: model.IncidentSummary{ID: incidentID, Scope: scope}}, nil
}
