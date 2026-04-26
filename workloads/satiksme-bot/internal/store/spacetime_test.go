package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
)

func TestSpacetimePayloadsExposeStableTelegramIdentity(t *testing.T) {
	createdAt := time.Date(2026, 4, 24, 10, 30, 0, 0, time.UTC)
	stop := spacetimeStopSightingPayload(model.StopSighting{
		ID:        "stop-1",
		StopID:    "1033a",
		UserID:    777001,
		CreatedAt: createdAt,
	})
	vote := spacetimeIncidentVotePayload(model.IncidentVote{
		IncidentID: "stop:1033a",
		UserID:     777001,
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})

	assertIdentityJSON(t, stop)
	assertIdentityJSON(t, vote)
}

func TestSpacetimeReportDumpPayloadUsesLowerCamelFields(t *testing.T) {
	createdAt := time.Date(2026, 4, 24, 11, 0, 0, 0, time.UTC)
	nextAttemptAt := createdAt.Add(30 * time.Second)
	payload := spacetimeReportDumpPayload(ReportDumpItem{
		ID:            "dump-1",
		Payload:       "Kontrole pie pieturas",
		Attempts:      2,
		CreatedAt:     createdAt,
		NextAttemptAt: nextAttemptAt,
	})

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	raw := string(body)
	for _, want := range []string{`"id"`, `"payload"`, `"attempts"`, `"createdAt"`, `"nextAttemptAt"`, `"lastAttemptAt"`, `"lastError"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("report dump payload JSON = %s, want key %s", raw, want)
		}
	}
	for _, unwanted := range []string{`"ID"`, `"Payload"`, `"CreatedAt"`, `"NextAttemptAt"`} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("report dump payload JSON = %s, did not want exported Go key %s", raw, unwanted)
		}
	}
	if !strings.Contains(raw, `"payload":"Kontrole pie pieturas"`) {
		t.Fatalf("report dump payload JSON = %s, want non-empty payload", raw)
	}
}

func TestDecodeSpacetimeReportDumpPayloadAcceptsBlankLastAttemptAt(t *testing.T) {
	createdAt := "2026-04-24T11:00:00Z"
	nextAttemptAt := "2026-04-24T11:01:00Z"
	item, err := decodeSpacetimeReportDumpPayload(map[string]any{
		"item": map[string]any{
			"id":            "dump-1",
			"payload":       "Kontrole pie pieturas",
			"attempts":      0,
			"createdAt":     createdAt,
			"nextAttemptAt": nextAttemptAt,
			"lastAttemptAt": "",
			"lastError":     "",
		},
	})
	if err != nil {
		t.Fatalf("decodeSpacetimeReportDumpPayload() error = %v", err)
	}
	if item == nil {
		t.Fatalf("decodeSpacetimeReportDumpPayload() = nil")
	}
	if item.LastAttemptAt.IsZero() != true {
		t.Fatalf("LastAttemptAt = %v, want zero time", item.LastAttemptAt)
	}
	if item.ID != "dump-1" || item.Payload != "Kontrole pie pieturas" {
		t.Fatalf("decoded item = %+v", item)
	}
}

func assertIdentityJSON(t *testing.T, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["stableId"] != "telegram:777001" {
		t.Fatalf("stableId = %#v, want telegram:777001 in %s", payload["stableId"], string(body))
	}
	if payload["userId"] != "777001" {
		t.Fatalf("userId = %#v, want 777001 in %s", payload["userId"], string(body))
	}
}
