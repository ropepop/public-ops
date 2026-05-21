package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
)

func TestSnapshotPublisherReusesVersionWhenPayloadIsUnchanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	publisher := NewSnapshotPublisher(dir, 1000)
	now := time.Date(2026, 3, 30, 1, 45, 5, 987654321, time.UTC)
	vehicles := []model.LiveVehicle{{
		ID:             "bus:22:17693",
		VehicleCode:    "17693",
		Mode:           "bus",
		RouteLabel:     "22",
		Direction:      "d1-a",
		Latitude:       56.947733,
		Longitude:      24.118448,
		UpdatedAt:      now,
		StopID:         "296",
		StopName:       "11. novembra krastmala",
		ArrivalSeconds: 3600,
		LiveRowID:      "17693",
	}}

	first, err := publisher.Publish(now, vehicles)
	if err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if first == nil || !first.Changed {
		t.Fatalf("first publish should create a version")
	}
	if filepath.Ext(first.Path) != ".js" {
		t.Fatalf("first path = %q, want cache-friendly .js suffix", first.Path)
	}

	second, err := publisher.Publish(now.Add(5*time.Second), vehicles)
	if err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if second == nil {
		t.Fatalf("second publish unexpectedly returned nil")
	}
	if second.Changed {
		t.Fatalf("second publish unexpectedly created a new version")
	}
	if second.Version != first.Version {
		t.Fatalf("second version = %q, want %q", second.Version, first.Version)
	}
}

func TestSnapshotPublisherRekeysPreNoStoreSnapshotVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 19, 45, 0, 0, time.UTC)
	vehicles := []model.LiveVehicle{{
		ID:          "tram:1:123",
		VehicleCode: "123",
		Mode:        "tram",
		RouteLabel:  "1",
		Latitude:    56.947733,
		Longitude:   24.118448,
	}}
	canonicalBody, err := json.Marshal(canonicalSnapshotVehicles(vehicles))
	if err != nil {
		t.Fatalf("Marshal(canonical) error = %v", err)
	}
	oldHashSum := sha256.Sum256(canonicalBody)
	oldHash := hex.EncodeToString(oldHashSum[:])
	oldActive := SnapshotActiveState{
		Version:      "20260508T222511Z-" + oldHash[:12],
		Path:         "transport/live/20260508T222511Z-" + oldHash[:12] + ".json.js",
		Hash:         oldHash,
		PublishedAt:  now.Add(-24 * time.Hour),
		VehicleCount: len(vehicles),
	}
	activeBody, err := json.Marshal(oldActive)
	if err != nil {
		t.Fatalf("Marshal(active) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, snapshotActiveName), append(activeBody, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(active) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(oldActive.Path)), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old snapshot) error = %v", err)
	}

	publisher := NewSnapshotPublisher(dir, 1)
	result, err := publisher.Publish(now, vehicles)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result == nil || !result.Changed {
		t.Fatalf("publish should create a fresh no-store snapshot version")
	}
	if result.Path == oldActive.Path {
		t.Fatalf("result path reused pre-no-store snapshot path %q", result.Path)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(oldActive.Path))); !os.IsNotExist(err) {
		t.Fatalf("old snapshot file still exists or stat failed unexpectedly: %v", err)
	}
}

func TestSnapshotPublisherWritesSnapshotPayload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	publisher := NewSnapshotPublisher(dir, 1000)
	now := time.Date(2026, 3, 30, 1, 45, 5, 987654321, time.UTC)
	result, err := publisher.Publish(now, []model.LiveVehicle{{
		ID:             "bus:22:17693",
		VehicleCode:    "17693",
		Mode:           "bus",
		RouteLabel:     "22",
		Direction:      "d1-a",
		Latitude:       56.947733,
		Longitude:      24.118448,
		UpdatedAt:      now.Add(-time.Minute),
		StopID:         "296",
		StopName:       "11. novembra krastmala",
		ArrivalSeconds: 3600,
		LiveRowID:      "17693",
	}})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Publish() unexpectedly returned nil")
	}

	body, err := os.ReadFile(filepath.Join(dir, filepath.Base(result.Path)))
	if err != nil {
		t.Fatalf("ReadFile(snapshot) error = %v", err)
	}
	var payload model.LiveTransportSnapshot
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal(snapshot) error = %v", err)
	}
	if payload.Version != result.Version {
		t.Fatalf("payload version = %q, want %q", payload.Version, result.Version)
	}
	if filepath.Ext(result.Path) != ".js" {
		t.Fatalf("result path = %q, want cache-friendly .js suffix", result.Path)
	}
	if len(payload.Vehicles) != 1 {
		t.Fatalf("len(payload.Vehicles) = %d, want 1", len(payload.Vehicles))
	}
	if !payload.GeneratedAt.Equal(now.Truncate(time.Second)) {
		t.Fatalf("payload generatedAt = %s, want %s", payload.GeneratedAt, now.Truncate(time.Second))
	}
	if !payload.Vehicles[0].UpdatedAt.Equal(now.Truncate(time.Second)) {
		t.Fatalf("payload vehicle updatedAt = %s, want %s", payload.Vehicles[0].UpdatedAt, now.Truncate(time.Second))
	}
	if got := payload.Vehicles[0].LiveRowID; got != "" {
		t.Fatalf("payload vehicle liveRowId = %q, want empty", got)
	}
	if got := payload.Vehicles[0].VehicleCode; got != "" {
		t.Fatalf("payload vehicle vehicleCode = %q, want empty", got)
	}
	if strings.Contains(payload.Vehicles[0].ID, "17693") {
		t.Fatalf("payload vehicle id exposes raw live row id: %q", payload.Vehicles[0].ID)
	}
	if payload.Vehicles[0].Latitude != 56.94773 || payload.Vehicles[0].Longitude != 24.11845 {
		t.Fatalf("payload vehicle coordinates = %.8f, %.8f; want rounded", payload.Vehicles[0].Latitude, payload.Vehicles[0].Longitude)
	}

	active, err := ReadSnapshotActiveState(dir)
	if err != nil {
		t.Fatalf("ReadSnapshotActiveState() error = %v", err)
	}
	if active == nil {
		t.Fatalf("active snapshot state unexpectedly nil")
	}
	if active.Path != result.Path {
		t.Fatalf("active path = %q, want %q", active.Path, result.Path)
	}
}

func TestSnapshotPublisherRetainsPreviousSnapshotWhenConfigured(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	publisher := NewSnapshotPublisher(dir, 2)
	first, err := publisher.Publish(time.Date(2026, 5, 10, 21, 56, 56, 0, time.UTC), []model.LiveVehicle{{
		ID:          "bus:22:17693",
		VehicleCode: "17693",
		Mode:        "bus",
		RouteLabel:  "22",
		Latitude:    56.947733,
		Longitude:   24.118448,
	}})
	if err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	second, err := publisher.Publish(time.Date(2026, 5, 10, 21, 57, 7, 0, time.UTC), []model.LiveVehicle{{
		ID:          "bus:22:17694",
		VehicleCode: "17694",
		Mode:        "bus",
		RouteLabel:  "22",
		Latitude:    56.9479,
		Longitude:   24.1189,
	}})
	if err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if first == nil || second == nil || !second.Changed {
		t.Fatalf("expected two published snapshot versions, got first=%+v second=%+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(first.Path))); err != nil {
		t.Fatalf("first snapshot should remain fetchable while public state catches up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(second.Path))); err != nil {
		t.Fatalf("second snapshot should exist: %v", err)
	}
}
