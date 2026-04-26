package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func insertTrain(t *testing.T, st *SQLiteStore, trainID string, dep time.Time, arr time.Time) {
	t.Helper()
	serviceDate := dep.In(time.UTC).Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            trainID,
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   dep,
		ArrivalAt:     arr,
		SourceVersion: "test",
	}
	if err := st.UpsertTrainInstances(context.Background(), serviceDate, "test", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("upsert train: %v", err)
	}
}

func insertTrainStops(t *testing.T, st *SQLiteStore, trainID string, dep time.Time) {
	t.Helper()
	arr := dep.Add(45 * time.Minute)
	serviceDate := dep.In(time.UTC).Format("2006-01-02")
	stops := map[string][]domain.TrainStop{
		trainID: {
			{TrainInstanceID: trainID, StationName: "Riga", Seq: 1, DepartureAt: &dep},
			{TrainInstanceID: trainID, StationName: "Jelgava", Seq: 2, ArrivalAt: &arr},
		},
	}
	if err := st.UpsertTrainStops(context.Background(), serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}
}

func floatPtr(v float64) *float64 {
	return &v
}

func countOrphanStops(t *testing.T, st *SQLiteStore) int {
	t.Helper()
	var count int
	err := st.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM train_stops ts
		LEFT JOIN train_instances t ON t.id = ts.train_instance_id
		WHERE t.id IS NULL
	`).Scan(&count)
	if err != nil {
		t.Fatalf("count orphan stops: %v", err)
	}
	return count
}

func TestCleanupExpiredRetentionBoundaries(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	dep := now.Add(-50 * time.Hour)
	arr := dep.Add(1 * time.Hour)
	insertTrain(t, st, "train-old", dep, arr)
	insertTrainStops(t, st, "train-old", dep)

	if err := st.CheckInUser(ctx, 10, "train-old", dep, arr.Add(10*time.Minute)); err != nil {
		t.Fatalf("checkin old: %v", err)
	}
	if err := st.UpsertSubscription(ctx, 11, "train-old", arr.Add(30*time.Minute)); err != nil {
		t.Fatalf("sub old: %v", err)
	}
	if err := st.InsertReportEvent(ctx, domain.ReportEvent{
		ID:              "evt-old",
		TrainInstanceID: "train-old",
		UserID:          10,
		Signal:          domain.SignalInspectionStarted,
		CreatedAt:       now.Add(-25 * time.Hour),
	}); err != nil {
		t.Fatalf("report old: %v", err)
	}

	dep2 := now.Add(-1 * time.Hour)
	arr2 := dep2.Add(1 * time.Hour)
	insertTrain(t, st, "train-keep", dep2, arr2)
	insertTrainStops(t, st, "train-keep", dep2)
	if err := st.CheckInUser(ctx, 20, "train-keep", dep2, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("checkin keep: %v", err)
	}
	if err := st.InsertReportEvent(ctx, domain.ReportEvent{
		ID:              "evt-keep",
		TrainInstanceID: "train-keep",
		UserID:          20,
		Signal:          domain.SignalInspectionInCar,
		CreatedAt:       now.Add(-23 * time.Hour),
	}); err != nil {
		t.Fatalf("report keep: %v", err)
	}

	res, err := st.CleanupExpired(ctx, now, 24*time.Hour, time.UTC)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.CheckinsDeleted < 1 || res.SubscriptionsDeleted < 1 || res.ReportsDeleted < 1 {
		t.Fatalf("expected old rows deleted, got %+v", res)
	}
	if res.TrainStopsDeleted != 2 {
		t.Fatalf("expected 2 orphaned train stops deleted, got %+v", res)
	}

	keepCheckin, err := st.GetActiveCheckIn(ctx, 20, now)
	if err != nil {
		t.Fatalf("active checkin keep query: %v", err)
	}
	if keepCheckin != nil {
		t.Fatalf("expected inactive checkin because auto checkout passed, got %+v", keepCheckin)
	}
	oldTrain, err := st.GetTrainInstanceByID(ctx, "train-old")
	if err != nil {
		t.Fatalf("get old train: %v", err)
	}
	if oldTrain != nil {
		t.Fatalf("expected old train deleted, got %+v", oldTrain)
	}
	keepTrain, err := st.GetTrainInstanceByID(ctx, "train-keep")
	if err != nil {
		t.Fatalf("get keep train: %v", err)
	}
	if keepTrain == nil {
		t.Fatalf("expected keep train to remain")
	}
	oldHasStops, err := st.TrainHasStops(ctx, "train-old")
	if err != nil {
		t.Fatalf("old train stops query: %v", err)
	}
	if oldHasStops {
		t.Fatalf("expected old train stops to be deleted")
	}
	keepHasStops, err := st.TrainHasStops(ctx, "train-keep")
	if err != nil {
		t.Fatalf("keep train stops query: %v", err)
	}
	if !keepHasStops {
		t.Fatalf("expected keep train stops to remain")
	}

	reports, err := st.ListRecentReports(ctx, "train-keep", 10)
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected keep report to remain, got %d", len(reports))
	}
	if got := countOrphanStops(t, st); got != 0 {
		t.Fatalf("expected no orphan stops after cleanup, got %d", got)
	}
}

func TestCleanupExpiredKeepsYesterdayTrainData(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 2, 25, 1, 30, 0, 0, loc)
	dep := time.Date(2026, 2, 24, 23, 50, 0, 0, loc)
	arr := dep.Add(45 * time.Minute)
	insertTrain(t, st, "train-yesterday", dep, arr)
	insertTrainStops(t, st, "train-yesterday", dep)

	res, err := st.CleanupExpired(ctx, now, 24*time.Hour, loc)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.TrainsDeleted != 0 || res.TrainStopsDeleted != 0 {
		t.Fatalf("expected yesterday train data to be preserved, got %+v", res)
	}

	train, err := st.GetTrainInstanceByID(ctx, "train-yesterday")
	if err != nil {
		t.Fatalf("get yesterday train: %v", err)
	}
	if train == nil {
		t.Fatalf("expected yesterday train to remain available")
	}
	hasStops, err := st.TrainHasStops(ctx, "train-yesterday")
	if err != nil {
		t.Fatalf("train has stops: %v", err)
	}
	if !hasStops {
		t.Fatalf("expected yesterday train stops to remain available")
	}
}

func TestCleanupExpiredRepairsPreexistingOrphanStops(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO stations(id, name, normalized_key)
		VALUES (?, ?, ?)
	`, "ghost-station", "Ghost Station", "ghost-station"); err != nil {
		t.Fatalf("insert station: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO train_stops(train_instance_id, station_id, seq, arrival_at, departure_at)
		VALUES (?, ?, ?, NULL, NULL)
	`, "missing-train", "ghost-station", 1); err != nil {
		t.Fatalf("insert orphan stop: %v", err)
	}
	if got := countOrphanStops(t, st); got != 1 {
		t.Fatalf("expected 1 orphan stop before cleanup, got %d", got)
	}

	res, err := st.CleanupExpired(ctx, now, 24*time.Hour, time.UTC)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.TrainStopsDeleted != 1 {
		t.Fatalf("expected 1 repaired orphan stop, got %+v", res)
	}
	if got := countOrphanStops(t, st); got != 0 {
		t.Fatalf("expected no orphan stops after cleanup, got %d", got)
	}
}

func TestTrainMuteAndRecipientQueries(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	dep := now.Add(-5 * time.Minute)
	arr := now.Add(40 * time.Minute)
	insertTrain(t, st, "train-1", dep, arr)

	if err := st.CheckInUser(ctx, 1, "train-1", now.Add(-2*time.Minute), arr.Add(10*time.Minute)); err != nil {
		t.Fatalf("checkin 1: %v", err)
	}
	if err := st.CheckInUser(ctx, 2, "train-1", now.Add(-2*time.Minute), arr.Add(10*time.Minute)); err != nil {
		t.Fatalf("checkin 2: %v", err)
	}
	if err := st.UpsertSubscription(ctx, 3, "train-1", arr.Add(30*time.Minute)); err != nil {
		t.Fatalf("sub 3: %v", err)
	}
	if err := st.SetTrainMute(ctx, 2, "train-1", now.Add(30*time.Minute)); err != nil {
		t.Fatalf("mute 2: %v", err)
	}
	if err := st.SetTrainMute(ctx, 3, "train-1", now.Add(30*time.Minute)); err != nil {
		t.Fatalf("mute 3: %v", err)
	}

	checkinUsers, err := st.ListActiveCheckinUsers(ctx, "train-1", now)
	if err != nil {
		t.Fatalf("list checkins: %v", err)
	}
	if len(checkinUsers) != 2 {
		t.Fatalf("expected both active checkins, got %+v", checkinUsers)
	}

	subUsers, err := st.ListActiveSubscriptionUsers(ctx, "train-1", now)
	if err != nil {
		t.Fatalf("list subs: %v", err)
	}
	if len(subUsers) != 1 || subUsers[0] != 3 {
		t.Fatalf("expected only sub user 3, got %+v", subUsers)
	}

	muted2, err := st.IsTrainMuted(ctx, 2, "train-1", now)
	if err != nil {
		t.Fatalf("is muted 2: %v", err)
	}
	if !muted2 {
		t.Fatalf("expected user 2 muted")
	}
	muted1, err := st.IsTrainMuted(ctx, 1, "train-1", now)
	if err != nil {
		t.Fatalf("is muted 1: %v", err)
	}
	if muted1 {
		t.Fatalf("expected user 1 not muted")
	}
}

func TestResetTestUserClearsInteractiveStateAndRestoresDefaults(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	dep := now.Add(-5 * time.Minute)
	arr := now.Add(40 * time.Minute)
	trainID := "train-reset"
	insertTrain(t, st, trainID, dep, arr)
	userID := int64(77)
	boardingStationID := "riga"
	destinationStationID := "jelgava"
	incidentID := "incident-reset"

	if err := st.SetAlertsEnabled(ctx, userID, false); err != nil {
		t.Fatalf("disable alerts: %v", err)
	}
	if err := st.SetAlertStyle(ctx, userID, domain.AlertStyleDiscreet); err != nil {
		t.Fatalf("set alert style: %v", err)
	}
	if err := st.SetLanguage(ctx, userID, domain.LanguageLV); err != nil {
		t.Fatalf("set language: %v", err)
	}
	if err := st.CheckInUserAtStation(ctx, userID, trainID, &boardingStationID, now.Add(-2*time.Minute), arr.Add(10*time.Minute)); err != nil {
		t.Fatalf("check in user: %v", err)
	}
	if err := st.UndoCheckoutUser(ctx, userID, trainID, &boardingStationID, now.Add(-2*time.Minute), arr.Add(10*time.Minute)); err != nil {
		t.Fatalf("undo checkout user: %v", err)
	}
	if err := st.SetTrainMute(ctx, userID, trainID, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("set train mute: %v", err)
	}
	if err := st.UpsertSubscription(ctx, userID, trainID, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}
	if err := st.UpsertFavoriteRoute(ctx, userID, "riga", "jelgava"); err != nil {
		t.Fatalf("upsert favorite route: %v", err)
	}
	if err := st.InsertReportEvent(ctx, domain.ReportEvent{
		ID:              "report-reset",
		TrainInstanceID: trainID,
		UserID:          userID,
		Signal:          domain.SignalInspectionStarted,
		CreatedAt:       now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("insert report event: %v", err)
	}
	if err := st.InsertStationSighting(ctx, domain.StationSighting{
		ID:                     "sighting-reset",
		StationID:              "riga",
		DestinationStationID:   &destinationStationID,
		MatchedTrainInstanceID: &trainID,
		UserID:                 userID,
		CreatedAt:              now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}
	if err := st.UpsertIncidentVote(ctx, domain.IncidentVote{
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   "Tester",
		Value:      domain.IncidentVoteOngoing,
		CreatedAt:  now.Add(-45 * time.Second),
		UpdatedAt:  now.Add(-15 * time.Second),
	}); err != nil {
		t.Fatalf("upsert incident vote: %v", err)
	}
	if err := st.InsertIncidentVoteEvent(ctx, domain.IncidentVoteEvent{
		ID:         "vote-event-reset",
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   "Tester",
		Value:      domain.IncidentVoteOngoing,
		CreatedAt:  now.Add(-15 * time.Second),
	}); err != nil {
		t.Fatalf("insert incident vote event: %v", err)
	}
	if err := st.InsertIncidentComment(ctx, domain.IncidentComment{
		ID:         "comment-reset",
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   "Tester",
		Body:       "Still there",
		CreatedAt:  now.Add(-10 * time.Second),
	}); err != nil {
		t.Fatalf("insert incident comment: %v", err)
	}

	if err := st.ResetTestUser(ctx, userID); err != nil {
		t.Fatalf("reset test user: %v", err)
	}

	settings, err := st.GetUserSettings(ctx, userID)
	if err != nil {
		t.Fatalf("get user settings: %v", err)
	}
	if !settings.AlertsEnabled || settings.AlertStyle != domain.AlertStyleDetailed || settings.Language != domain.LanguageEN {
		t.Fatalf("expected default settings after reset, got %+v", settings)
	}

	checkIn, err := st.GetActiveCheckIn(ctx, userID, now)
	if err != nil {
		t.Fatalf("get active checkin: %v", err)
	}
	if checkIn != nil {
		t.Fatalf("expected active checkin cleared, got %+v", checkIn)
	}

	var undoCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM undo_checkouts WHERE user_id = ?`, userID).Scan(&undoCount); err != nil {
		t.Fatalf("count undo checkouts: %v", err)
	}
	if undoCount != 0 {
		t.Fatalf("expected undo checkout cleared, got %d", undoCount)
	}

	favorites, err := st.ListFavoriteRoutes(ctx, userID)
	if err != nil {
		t.Fatalf("list favorite routes: %v", err)
	}
	if len(favorites) != 0 {
		t.Fatalf("expected favorite routes cleared, got %+v", favorites)
	}

	muted, err := st.IsTrainMuted(ctx, userID, trainID, now)
	if err != nil {
		t.Fatalf("check train mute: %v", err)
	}
	if muted {
		t.Fatalf("expected mute cleared")
	}

	subscribed, err := st.HasActiveSubscription(ctx, userID, trainID, now)
	if err != nil {
		t.Fatalf("check active subscription: %v", err)
	}
	if subscribed {
		t.Fatalf("expected subscription cleared")
	}

	reports, err := st.ListRecentReports(ctx, trainID, 10)
	if err != nil {
		t.Fatalf("list recent reports: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("expected reports cleared, got %+v", reports)
	}

	sightings, err := st.ListRecentStationSightings(ctx, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("list recent station sightings: %v", err)
	}
	if len(sightings) != 0 {
		t.Fatalf("expected station sightings cleared, got %+v", sightings)
	}

	votes, err := st.ListIncidentVotes(ctx, incidentID)
	if err != nil {
		t.Fatalf("list incident votes: %v", err)
	}
	if len(votes) != 0 {
		t.Fatalf("expected incident votes cleared, got %+v", votes)
	}

	voteEvents, err := st.ListIncidentVoteEvents(ctx, incidentID, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("list incident vote events: %v", err)
	}
	if len(voteEvents) != 0 {
		t.Fatalf("expected incident vote events cleared, got %+v", voteEvents)
	}

	comments, err := st.ListIncidentComments(ctx, incidentID, 10)
	if err != nil {
		t.Fatalf("list incident comments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected incident comments cleared, got %+v", comments)
	}
}

func TestConsumeTestLoginTicketIsDurableAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "train-bot.db")

	firstStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	if err := firstStore.Migrate(ctx); err != nil {
		t.Fatalf("migrate first store: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	accepted, err := firstStore.ConsumeTestLoginTicket(ctx, "nonce-hash-1", 7001, expiresAt)
	if err != nil {
		t.Fatalf("consume first ticket: %v", err)
	}
	if !accepted {
		t.Fatalf("expected first consume to succeed")
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	secondStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	defer secondStore.Close()
	if err := secondStore.Migrate(ctx); err != nil {
		t.Fatalf("migrate second store: %v", err)
	}

	accepted, err = secondStore.ConsumeTestLoginTicket(ctx, "nonce-hash-1", 7001, expiresAt)
	if err != nil {
		t.Fatalf("consume duplicate ticket: %v", err)
	}
	if accepted {
		t.Fatalf("expected duplicate consume to stay rejected after restart")
	}
}

func TestConsumeTestLoginTicketPrunesExpiredRowsBeforeAcceptingANewTicket(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	expiredAt := time.Now().UTC().Add(-time.Minute)
	accepted, err := st.ConsumeTestLoginTicket(ctx, "nonce-hash-expired", 7001, expiredAt)
	if err != nil {
		t.Fatalf("consume expired ticket row: %v", err)
	}
	if !accepted {
		t.Fatalf("expected first insert to succeed even for an already expired row")
	}

	accepted, err = st.ConsumeTestLoginTicket(ctx, "nonce-hash-expired", 7001, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("consume replacement ticket row: %v", err)
	}
	if !accepted {
		t.Fatalf("expected expired row to be pruned before the next consume")
	}
}

func TestStationQueriesUsePassTimeAndCheckinMetadata(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	serviceDate := now.Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-station-1",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   now.Add(-30 * time.Minute),
		ArrivalAt:     now.Add(90 * time.Minute),
		SourceVersion: "test",
	}
	train2 := domain.TrainInstance{
		ID:            "train-station-2",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   now.Add(-40 * time.Minute),
		ArrivalAt:     now.Add(120 * time.Minute),
		SourceVersion: "test",
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "test", []domain.TrainInstance{train, train2}); err != nil {
		t.Fatalf("upsert train: %v", err)
	}
	rigaStopAt := now.Add(-30 * time.Minute)
	jelgavaStopAt := now.Add(10 * time.Minute)
	rigaStop2At := now.Add(-40 * time.Minute)
	jelgavaStop2At := now.Add(30 * time.Minute)
	stops := map[string][]domain.TrainStop{
		train.ID: {
			{TrainInstanceID: train.ID, StationName: "Riga", Seq: 1, DepartureAt: &rigaStopAt},
			{TrainInstanceID: train.ID, StationName: "Jelgava", Seq: 2, ArrivalAt: &jelgavaStopAt},
		},
		train2.ID: {
			{TrainInstanceID: train2.ID, StationName: "Riga", Seq: 1, DepartureAt: &rigaStop2At},
			{TrainInstanceID: train2.ID, StationName: "Jelgava", Seq: 2, ArrivalAt: &jelgavaStop2At},
		},
	}
	if err := st.UpsertTrainStops(ctx, serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}
	stations, err := st.ListStationsByDate(ctx, serviceDate)
	if err != nil {
		t.Fatalf("list stations: %v", err)
	}
	if len(stations) < 2 {
		t.Fatalf("expected at least 2 stations, got %d", len(stations))
	}
	jelgavaID := "jelgava"
	trains, err := st.ListStationWindowTrains(ctx, serviceDate, jelgavaID, now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("list trains by station: %v", err)
	}
	if len(trains) != 2 {
		t.Fatalf("expected 2 trains by station, got %d", len(trains))
	}
	if trains[0].PassAt.UTC().Format(time.RFC3339) != jelgavaStopAt.UTC().Format(time.RFC3339) {
		t.Fatalf("expected pass-at %s, got %s", jelgavaStopAt.UTC().Format(time.RFC3339), trains[0].PassAt.UTC().Format(time.RFC3339))
	}
	if trains[1].PassAt.UTC().Format(time.RFC3339) != jelgavaStop2At.UTC().Format(time.RFC3339) {
		t.Fatalf("expected second pass-at %s, got %s", jelgavaStop2At.UTC().Format(time.RFC3339), trains[1].PassAt.UTC().Format(time.RFC3339))
	}

	if err := st.CheckInUserAtStation(ctx, 77, train.ID, &jelgavaID, now, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("checkin at station: %v", err)
	}
	checkin, err := st.GetActiveCheckIn(ctx, 77, now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("get active checkin: %v", err)
	}
	if checkin == nil || checkin.BoardingStationID == nil || *checkin.BoardingStationID != jelgavaID {
		t.Fatalf("expected boarding station id %q, got %+v", jelgavaID, checkin)
	}
}

func TestRouteQueriesAndFavorites(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	serviceDate := now.Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-route-1",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Cesis",
		DepartureAt:   now.Add(-20 * time.Minute),
		ArrivalAt:     now.Add(2 * time.Hour),
		SourceVersion: "test",
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "test", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("upsert train: %v", err)
	}

	rigaPass := now.Add(-20 * time.Minute)
	siguldaPass := now.Add(50 * time.Minute)
	cesisPass := now.Add(2 * time.Hour)
	stops := map[string][]domain.TrainStop{
		train.ID: {
			{TrainInstanceID: train.ID, StationName: "Riga", Seq: 1, DepartureAt: &rigaPass},
			{TrainInstanceID: train.ID, StationName: "Sigulda", Seq: 2, ArrivalAt: &siguldaPass},
			{TrainInstanceID: train.ID, StationName: "Cesis", Seq: 3, ArrivalAt: &cesisPass},
		},
	}
	if err := st.UpsertTrainStops(ctx, serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}

	destinations, err := st.ListReachableDestinations(ctx, serviceDate, "riga")
	if err != nil {
		t.Fatalf("list reachable destinations: %v", err)
	}
	if len(destinations) < 2 {
		t.Fatalf("expected >=2 destinations from Riga, got %d", len(destinations))
	}

	routes, err := st.ListRouteWindowTrains(ctx, serviceDate, "riga", "sigulda", now.Add(-30*time.Minute), now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("list route trains: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected one route train, got %d", len(routes))
	}
	if routes[0].Train.ID != train.ID {
		t.Fatalf("expected train %s, got %s", train.ID, routes[0].Train.ID)
	}
	if routes[0].FromStationID != "riga" || routes[0].ToStationID != "sigulda" {
		t.Fatalf("unexpected stations %+v", routes[0])
	}

	if err := st.UpsertFavoriteRoute(ctx, 99, "riga", "sigulda"); err != nil {
		t.Fatalf("upsert favorite: %v", err)
	}
	if err := st.UpsertFavoriteRoute(ctx, 99, "riga", "cesis"); err != nil {
		t.Fatalf("upsert second favorite: %v", err)
	}
	favorites, err := st.ListFavoriteRoutes(ctx, 99)
	if err != nil {
		t.Fatalf("list favorites: %v", err)
	}
	if len(favorites) != 2 {
		t.Fatalf("expected 2 favorites, got %d", len(favorites))
	}
	if err := st.DeleteFavoriteRoute(ctx, 99, "riga", "sigulda"); err != nil {
		t.Fatalf("delete favorite: %v", err)
	}
	favorites, err = st.ListFavoriteRoutes(ctx, 99)
	if err != nil {
		t.Fatalf("list favorites after delete: %v", err)
	}
	if len(favorites) != 1 {
		t.Fatalf("expected 1 favorite after delete, got %d", len(favorites))
	}
}

func TestListTerminalDestinationsDedupesAndSkipsIntermediateStops(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	serviceDate := now.Format("2006-01-02")
	trains := []domain.TrainInstance{
		{
			ID:            "train-terminal-a",
			ServiceDate:   serviceDate,
			FromStation:   "Riga",
			ToStation:     "Cesis",
			DepartureAt:   now,
			ArrivalAt:     now.Add(2 * time.Hour),
			SourceVersion: "test",
		},
		{
			ID:            "train-terminal-b",
			ServiceDate:   serviceDate,
			FromStation:   "Riga",
			ToStation:     "Cesis",
			DepartureAt:   now.Add(20 * time.Minute),
			ArrivalAt:     now.Add(140 * time.Minute),
			SourceVersion: "test",
		},
		{
			ID:            "train-terminal-loop",
			ServiceDate:   serviceDate,
			FromStation:   "Riga",
			ToStation:     "Riga",
			DepartureAt:   now.Add(40 * time.Minute),
			ArrivalAt:     now.Add(3 * time.Hour),
			SourceVersion: "test",
		},
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "test", trains); err != nil {
		t.Fatalf("upsert trains: %v", err)
	}

	stops := map[string][]domain.TrainStop{
		"train-terminal-a": {
			{TrainInstanceID: "train-terminal-a", StationName: "Riga", Seq: 1, DepartureAt: floatTimePtr(now)},
			{TrainInstanceID: "train-terminal-a", StationName: "Sigulda", Seq: 2, ArrivalAt: floatTimePtr(now.Add(50 * time.Minute))},
			{TrainInstanceID: "train-terminal-a", StationName: "Cesis", Seq: 3, ArrivalAt: floatTimePtr(now.Add(2 * time.Hour))},
		},
		"train-terminal-b": {
			{TrainInstanceID: "train-terminal-b", StationName: "Riga", Seq: 1, DepartureAt: floatTimePtr(now.Add(20 * time.Minute))},
			{TrainInstanceID: "train-terminal-b", StationName: "Cesis", Seq: 2, ArrivalAt: floatTimePtr(now.Add(140 * time.Minute))},
		},
		"train-terminal-loop": {
			{TrainInstanceID: "train-terminal-loop", StationName: "Riga", Seq: 1, DepartureAt: floatTimePtr(now.Add(40 * time.Minute))},
			{TrainInstanceID: "train-terminal-loop", StationName: "Sigulda", Seq: 2, ArrivalAt: floatTimePtr(now.Add(90 * time.Minute))},
			{TrainInstanceID: "train-terminal-loop", StationName: "Riga", Seq: 3, ArrivalAt: floatTimePtr(now.Add(3 * time.Hour))},
		},
	}
	if err := st.UpsertTrainStops(ctx, serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}

	destinations, err := st.ListTerminalDestinations(ctx, serviceDate, "riga")
	if err != nil {
		t.Fatalf("list terminal destinations: %v", err)
	}
	if len(destinations) != 2 {
		t.Fatalf("expected 2 terminal destinations, got %d: %+v", len(destinations), destinations)
	}

	got := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		got[destination.ID] = struct{}{}
	}
	if _, ok := got["cesis"]; !ok {
		t.Fatalf("expected cesis terminal destination, got %+v", destinations)
	}
	if _, ok := got["riga"]; !ok {
		t.Fatalf("expected repeated-stop final riga destination, got %+v", destinations)
	}
	if _, ok := got["sigulda"]; ok {
		t.Fatalf("expected intermediate sigulda to be excluded, got %+v", destinations)
	}
}

func TestListTrainStopsPreservesCoordinatesAndRepeatedStations(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	serviceDate := now.Format("2006-01-02")
	trainA := domain.TrainInstance{
		ID:            "train-loop-a",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Olaine",
		DepartureAt:   now,
		ArrivalAt:     now.Add(90 * time.Minute),
		SourceVersion: "test",
	}
	trainB := domain.TrainInstance{
		ID:            "train-loop-b",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   now.Add(2 * time.Hour),
		ArrivalAt:     now.Add(3 * time.Hour),
		SourceVersion: "test",
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "test", []domain.TrainInstance{trainA, trainB}); err != nil {
		t.Fatalf("upsert train instances: %v", err)
	}

	rigaLat := 56.9496
	rigaLon := 24.1052
	olaineLat := 56.7947
	olaineLon := 23.9358
	stops := map[string][]domain.TrainStop{
		trainA.ID: {
			{TrainInstanceID: trainA.ID, StationName: "Riga", Seq: 1, DepartureAt: &now, Latitude: floatPtr(rigaLat), Longitude: floatPtr(rigaLon)},
			{TrainInstanceID: trainA.ID, StationName: "Olaine", Seq: 2, ArrivalAt: floatTimePtr(now.Add(25 * time.Minute)), DepartureAt: floatTimePtr(now.Add(27 * time.Minute)), Latitude: floatPtr(olaineLat), Longitude: floatPtr(olaineLon)},
			{TrainInstanceID: trainA.ID, StationName: "Riga", Seq: 3, ArrivalAt: floatTimePtr(now.Add(55 * time.Minute))},
		},
		trainB.ID: {
			{TrainInstanceID: trainB.ID, StationName: "Riga", Seq: 1, DepartureAt: floatTimePtr(now.Add(2 * time.Hour))},
			{TrainInstanceID: trainB.ID, StationName: "Jelgava", Seq: 2, ArrivalAt: floatTimePtr(now.Add(3 * time.Hour))},
		},
	}
	if err := st.UpsertTrainStops(ctx, serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}

	station, err := st.GetStationByID(ctx, "riga")
	if err != nil {
		t.Fatalf("get station: %v", err)
	}
	if station == nil || station.Latitude == nil || station.Longitude == nil {
		t.Fatalf("expected preserved station coordinates, got %+v", station)
	}
	if *station.Latitude != rigaLat || *station.Longitude != rigaLon {
		t.Fatalf("unexpected station coordinates: %+v", station)
	}

	loopStops, err := st.ListTrainStops(ctx, trainA.ID)
	if err != nil {
		t.Fatalf("list train loop stops: %v", err)
	}
	if len(loopStops) != 3 {
		t.Fatalf("expected 3 loop stops, got %d", len(loopStops))
	}
	if loopStops[0].Seq != 1 || loopStops[1].Seq != 2 || loopStops[2].Seq != 3 {
		t.Fatalf("expected seq ordering preserved, got %+v", loopStops)
	}
	if loopStops[0].StationID != "riga" || loopStops[2].StationID != "riga" {
		t.Fatalf("expected repeated station ids to be preserved, got %+v", loopStops)
	}
	if loopStops[2].Latitude == nil || loopStops[2].Longitude == nil {
		t.Fatalf("expected repeated stop to inherit preserved coordinates, got %+v", loopStops[2])
	}

	reusedStops, err := st.ListTrainStops(ctx, trainB.ID)
	if err != nil {
		t.Fatalf("list reused stops: %v", err)
	}
	if len(reusedStops) != 2 {
		t.Fatalf("expected 2 stops for trainB, got %d", len(reusedStops))
	}
	if reusedStops[0].Latitude == nil || reusedStops[0].Longitude == nil {
		t.Fatalf("expected coordinates preserved for reused station, got %+v", reusedStops[0])
	}
}

func TestStationSightingQueriesAndCleanup(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO stations(id, name, normalized_key, latitude, longitude)
		VALUES
			('riga', 'Riga', 'riga', 56.9496, 24.1052),
			('jelgava', 'Jelgava', 'jelgava', 56.6511, 23.7128)
	`); err != nil {
		t.Fatalf("insert stations: %v", err)
	}

	now := time.Date(2026, 2, 26, 9, 0, 0, 0, time.UTC)
	destinationID := "jelgava"
	matchedTrainID := "train-1"
	events := []domain.StationSighting{
		{
			ID:                     "sighting-dest",
			StationID:              "riga",
			DestinationStationID:   &destinationID,
			MatchedTrainInstanceID: &matchedTrainID,
			UserID:                 11,
			CreatedAt:              now.Add(-10 * time.Minute),
		},
		{
			ID:        "sighting-station",
			StationID: "riga",
			UserID:    11,
			CreatedAt: now.Add(-5 * time.Minute),
		},
		{
			ID:        "sighting-old",
			StationID: "riga",
			UserID:    22,
			CreatedAt: now.Add(-25 * time.Hour),
		},
	}
	for _, item := range events {
		if err := st.InsertStationSighting(ctx, item); err != nil {
			t.Fatalf("insert station sighting %s: %v", item.ID, err)
		}
	}

	lastDestination, err := st.GetLastStationSightingByUserScope(ctx, 11, "riga", &destinationID)
	if err != nil {
		t.Fatalf("get last destination sighting: %v", err)
	}
	if lastDestination == nil || lastDestination.ID != "sighting-dest" || lastDestination.DestinationStationName != "Jelgava" {
		t.Fatalf("unexpected destination-scoped sighting: %+v", lastDestination)
	}

	lastStation, err := st.GetLastStationSightingByUserScope(ctx, 11, "riga", nil)
	if err != nil {
		t.Fatalf("get last station sighting: %v", err)
	}
	if lastStation == nil || lastStation.ID != "sighting-station" || lastStation.StationName != "Riga" {
		t.Fatalf("unexpected station-scoped sighting: %+v", lastStation)
	}

	stationSightings, err := st.ListRecentStationSightingsByStation(ctx, "riga", now.Add(-30*time.Minute), 10)
	if err != nil {
		t.Fatalf("list recent station sightings: %v", err)
	}
	if len(stationSightings) != 2 {
		t.Fatalf("expected 2 recent station sightings, got %d", len(stationSightings))
	}

	allSightings, err := st.ListRecentStationSightings(ctx, now.Add(-30*time.Minute), 10)
	if err != nil {
		t.Fatalf("list recent station sightings: %v", err)
	}
	if len(allSightings) != 2 {
		t.Fatalf("expected 2 recent station sightings across all stations, got %d", len(allSightings))
	}

	trainSightings, err := st.ListRecentStationSightingsByTrain(ctx, "train-1", now.Add(-30*time.Minute), 10)
	if err != nil {
		t.Fatalf("list recent train sightings: %v", err)
	}
	if len(trainSightings) != 1 || trainSightings[0].ID != "sighting-dest" {
		t.Fatalf("unexpected train sightings: %+v", trainSightings)
	}

	res, err := st.CleanupExpired(ctx, now, 24*time.Hour, time.UTC)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.StationSightingsDeleted != 1 {
		t.Fatalf("expected 1 old station sighting deleted, got %+v", res)
	}
}

func TestIncidentVoteEventsAppendWhileLatestVoteStaysUpserted(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 9, 0, 0, 0, time.UTC)
	incidentID := "station:riga:2026-02-26-station"

	if err := st.UpsertIncidentVote(ctx, domain.IncidentVote{
		IncidentID: incidentID,
		UserID:     11,
		Nickname:   "Quiet Scout 111",
		Value:      domain.IncidentVoteOngoing,
		CreatedAt:  now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert first incident vote: %v", err)
	}
	if err := st.InsertIncidentVoteEvent(ctx, domain.IncidentVoteEvent{
		ID:         "vote-1",
		IncidentID: incidentID,
		UserID:     11,
		Nickname:   "Quiet Scout 111",
		Value:      domain.IncidentVoteOngoing,
		CreatedAt:  now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("insert first incident vote event: %v", err)
	}
	if err := st.UpsertIncidentVote(ctx, domain.IncidentVote{
		IncidentID: incidentID,
		UserID:     11,
		Nickname:   "Quiet Scout 111",
		Value:      domain.IncidentVoteCleared,
		CreatedAt:  now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert second incident vote: %v", err)
	}
	if err := st.InsertIncidentVoteEvent(ctx, domain.IncidentVoteEvent{
		ID:         "vote-2",
		IncidentID: incidentID,
		UserID:     11,
		Nickname:   "Quiet Scout 111",
		Value:      domain.IncidentVoteCleared,
		CreatedAt:  now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("insert second incident vote event: %v", err)
	}

	votes, err := st.ListIncidentVotes(ctx, incidentID)
	if err != nil {
		t.Fatalf("list incident votes: %v", err)
	}
	if len(votes) != 1 || votes[0].Value != domain.IncidentVoteCleared {
		t.Fatalf("expected a single latest vote summary, got %+v", votes)
	}

	events, err := st.ListIncidentVoteEvents(ctx, incidentID, now.Add(-10*time.Minute), 10)
	if err != nil {
		t.Fatalf("list incident vote events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected both vote events to be retained, got %+v", events)
	}
	if events[0].ID != "vote-2" || events[1].ID != "vote-1" {
		t.Fatalf("expected vote events in reverse chronological order, got %+v", events)
	}
}

func floatTimePtr(v time.Time) *time.Time {
	return &v
}

func TestTrainStopsPreserveRepeatedStationPasses(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	defer st.Close()

	now := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	serviceDate := now.Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-loop-1",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Riga",
		DepartureAt:   now,
		ArrivalAt:     now.Add(2 * time.Hour),
		SourceVersion: "test",
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "test", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("upsert train: %v", err)
	}

	pass1 := now.Add(10 * time.Minute)
	pass2 := now.Add(70 * time.Minute)
	stops := map[string][]domain.TrainStop{
		train.ID: {
			{TrainInstanceID: train.ID, StationName: "Riga", Seq: 1, DepartureAt: &pass1},
			{TrainInstanceID: train.ID, StationName: "Jelgava", Seq: 2},
			{TrainInstanceID: train.ID, StationName: "Riga", Seq: 3, ArrivalAt: &pass2},
		},
	}
	if err := st.UpsertTrainStops(ctx, serviceDate, stops); err != nil {
		t.Fatalf("upsert stops: %v", err)
	}

	routes, err := st.ListRouteWindowTrains(ctx, serviceDate, "riga", "riga", now, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("list route trains: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 loop route, got %d", len(routes))
	}
	if routes[0].FromPassAt.UTC().Format(time.RFC3339) != pass1.UTC().Format(time.RFC3339) {
		t.Fatalf("expected first riga pass-at %s, got %s", pass1.UTC().Format(time.RFC3339), routes[0].FromPassAt.UTC().Format(time.RFC3339))
	}
	if routes[0].ToPassAt.UTC().Format(time.RFC3339) != pass2.UTC().Format(time.RFC3339) {
		t.Fatalf("expected second riga pass-at %s, got %s", pass2.UTC().Format(time.RFC3339), routes[0].ToPassAt.UTC().Format(time.RFC3339))
	}
}
