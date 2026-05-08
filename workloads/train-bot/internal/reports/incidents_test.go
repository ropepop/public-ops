package reports

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
)

func TestIncidentSummariesExposeExplicitMapTargets(t *testing.T) {
	ctx := context.Background()
	st := setupStore(t)
	defer st.Close()

	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)
	dep := now.Add(-15 * time.Minute)
	arr := now.Add(45 * time.Minute)
	seedTrain(t, st, "train-target", dep, arr)
	seedTrainStops(t, st, "train-target", dep, arr)

	svc := NewService(st, 3*time.Minute, 90*time.Second)

	trainReport, err := svc.SubmitReport(ctx, 1, "train-target", domain.SignalInspectionStarted, now.Add(-4*time.Minute))
	if err != nil {
		t.Fatalf("submit train report: %v", err)
	}
	destinationID := "jelgava"
	matchedTrainID := "train-target"
	matchedStation, err := svc.SubmitStationSighting(ctx, 2, "riga", &destinationID, &matchedTrainID, now.Add(-3*time.Minute))
	if err != nil {
		t.Fatalf("submit matched station sighting: %v", err)
	}
	lat := 56.94672
	lng := 24.10589
	stationReport, err := svc.SubmitStationReport(ctx, 3, domain.Station{
		ID:        "riga",
		Name:      "Riga",
		Latitude:  &lat,
		Longitude: &lng,
	}, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("submit station report: %v", err)
	}
	areaReport, err := svc.SubmitAreaReport(ctx, 4, 56.94672, 24.10589, 100, "near the station tunnel", now.Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("submit area report: %v", err)
	}

	items, err := svc.ListActiveIncidents(ctx, now, 0, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}

	assertIncidentMapTarget(t, items, trainReport.IncidentID, "train", "train-target", "", "")
	assertIncidentMapTarget(t, items, matchedStation.IncidentID, "train", "train-target", "", "")
	assertIncidentMapTarget(t, items, stationReport.IncidentID, "station", "", "riga", "")
	assertIncidentMapTarget(t, items, areaReport.IncidentID, "area", "", "", areaReport.IncidentID)
}

func TestListActiveIncidentsUsesSameDayActivityOrdering(t *testing.T) {
	ctx := context.Background()
	st := setupStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, loc)

	depA := time.Date(2026, time.March, 18, 9, 0, 0, 0, loc)
	depB := time.Date(2026, time.March, 18, 11, 0, 0, 0, loc)
	depOld := time.Date(2026, time.March, 17, 10, 0, 0, 0, loc)
	seedTrain(t, st, "train-a", depA, depA.Add(45*time.Minute))
	seedTrainStops(t, st, "train-a", depA, depA.Add(45*time.Minute))
	seedTrain(t, st, "train-b", depB, depB.Add(45*time.Minute))
	seedTrainStops(t, st, "train-b", depB, depB.Add(45*time.Minute))
	seedTrain(t, st, "train-old", depOld, depOld.Add(45*time.Minute))
	seedTrainStops(t, st, "train-old", depOld, depOld.Add(45*time.Minute))

	svc := NewService(st, 3*time.Minute, 90*time.Second)

	firstA, err := svc.SubmitReport(ctx, 10, "train-a", domain.SignalInspectionStarted, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("submit report a: %v", err)
	}
	firstB, err := svc.SubmitReport(ctx, 20, "train-b", domain.SignalInspectionStarted, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("submit report b: %v", err)
	}
	if _, err := svc.SubmitReport(ctx, 30, "train-old", domain.SignalInspectionStarted, depOld.Add(15*time.Minute)); err != nil {
		t.Fatalf("submit previous-day report: %v", err)
	}

	items, err := svc.ListActiveIncidents(ctx, now, 0, 0)
	if err != nil {
		t.Fatalf("list same-day incidents: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 same-day incidents, got %+v", items)
	}
	if items[0].ID != firstB.IncidentID {
		t.Fatalf("expected newer report to sort first before later activity, got %+v", items)
	}

	if _, err := svc.AddIncidentComment(ctx, firstA.IncidentID, 11, "Still active here", now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("add incident comment: %v", err)
	}

	items, err = svc.ListActiveIncidents(ctx, now, 0, 0)
	if err != nil {
		t.Fatalf("list after comment: %v", err)
	}
	if items[0].ID != firstA.IncidentID || items[0].LastActivityName != "Comment" {
		t.Fatalf("expected commented incident to move first, got %+v", items)
	}

	if _, err := svc.VoteIncident(ctx, firstB.IncidentID, 21, domain.IncidentVoteCleared, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("cleared vote: %v", err)
	}

	items, err = svc.ListActiveIncidents(ctx, now, 0, 0)
	if err != nil {
		t.Fatalf("list after cleared vote: %v", err)
	}
	if items[0].ID != firstA.IncidentID {
		t.Fatalf("expected cleared vote not to bump ordering, got %+v", items)
	}

	if _, err := svc.VoteIncident(ctx, firstB.IncidentID, 22, domain.IncidentVoteOngoing, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("ongoing vote: %v", err)
	}

	items, err = svc.ListActiveIncidents(ctx, now, 0, 0)
	if err != nil {
		t.Fatalf("list after ongoing vote: %v", err)
	}
	if items[0].ID != firstB.IncidentID || items[0].LastActivityName != "Still there" {
		t.Fatalf("expected ongoing vote to bump ordering, got %+v", items)
	}
}

func TestIncidentDetailIncludesNewestActivityFirst(t *testing.T) {
	ctx := context.Background()
	st := setupStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, loc)
	dep := time.Date(2026, time.March, 18, 11, 0, 0, 0, loc)
	seedTrain(t, st, "train-detail", dep, dep.Add(45*time.Minute))
	seedTrainStops(t, st, "train-detail", dep, dep.Add(45*time.Minute))

	svc := NewService(st, 3*time.Minute, 90*time.Second)
	first, err := svc.SubmitReport(ctx, 41, "train-detail", domain.SignalInspectionStarted, now.Add(-25*time.Minute))
	if err != nil {
		t.Fatalf("submit report: %v", err)
	}
	if _, err := svc.AddIncidentComment(ctx, first.IncidentID, 42, "Still checking", now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if _, err := svc.VoteIncident(ctx, first.IncidentID, 43, domain.IncidentVoteOngoing, now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("ongoing vote: %v", err)
	}

	detail, err := svc.IncidentDetail(ctx, first.IncidentID, now, 0)
	if err != nil {
		t.Fatalf("incident detail: %v", err)
	}
	if detail.Summary.LastActivityName != "Still there" {
		t.Fatalf("expected latest activity label to reflect confirming vote, got %+v", detail.Summary)
	}
	if len(detail.Events) < 3 {
		t.Fatalf("expected report, comment, and ongoing vote in activity, got %+v", detail.Events)
	}
	if detail.Events[0].Kind != "vote" || detail.Events[1].Kind != "comment" || detail.Events[len(detail.Events)-1].Kind != "report" {
		t.Fatalf("expected newest-first activity ordering, got %+v", detail.Events)
	}
	if len(detail.Comments) != 1 || detail.Comments[0].Body != "Still checking" {
		t.Fatalf("expected comments to remain available in their own section, got %+v", detail.Comments)
	}
}

type incidentMapTargetPayload struct {
	Type            string `json:"type"`
	TrainInstanceID string `json:"trainInstanceId"`
	StationID       string `json:"stationId"`
	IncidentID      string `json:"incidentId"`
}

func assertIncidentMapTarget(t *testing.T, items []domain.IncidentSummary, incidentID, targetType, trainID, stationID, areaIncidentID string) {
	t.Helper()
	for _, item := range items {
		if item.ID != incidentID {
			continue
		}
		var payload struct {
			MapTarget incidentMapTargetPayload `json:"mapTarget"`
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal incident %q: %v", incidentID, err)
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode incident %q map target: %v", incidentID, err)
		}
		if payload.MapTarget.Type != targetType ||
			payload.MapTarget.TrainInstanceID != trainID ||
			payload.MapTarget.StationID != stationID ||
			payload.MapTarget.IncidentID != areaIncidentID {
			t.Fatalf("unexpected map target for %q: got %+v want type=%q train=%q station=%q incident=%q", incidentID, payload.MapTarget, targetType, trainID, stationID, areaIncidentID)
		}
		return
	}
	t.Fatalf("incident %q not found in %+v", incidentID, items)
}
