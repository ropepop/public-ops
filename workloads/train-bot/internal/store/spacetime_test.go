package store

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/spacetime"
)

func TestSpacetimeSettingsPreserveSavedRiderAndReadErrors(t *testing.T) {
	rider := spacetime.TrainbotRiderRow{
		StableID: spacetime.StableIDForTelegramUser(42), TelegramUserID: "42", Nickname: "Saved rider",
		Settings:      spacetime.TrainbotSettings{AlertsEnabled: true, AlertStyle: "DETAILED", Language: "LV"},
		Favorites:     []spacetime.TrainbotFavorite{{FromStationID: "riga", ToStationID: "jelgava"}},
		CurrentRide:   &spacetime.TrainbotRideState{TrainInstanceID: "saved-train"},
		Mutes:         []spacetime.TrainbotMute{{TrainInstanceID: "muted-train", MutedUntil: "2099-01-01T00:00:00Z"}},
		Subscriptions: []spacetime.TrainbotSubscription{{TrainInstanceID: "followed-train", ExpiresAt: "2099-01-01T00:00:00Z", IsActive: true}},
	}
	var writes int
	var failRead bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "" {
			t.Error("missing service authentication")
		}
		switch r.URL.Path {
		case "/v1/database/train-db/call/trainbot_service_get_rider":
			if failRead {
				http.Error(w, "trainbot_rider may be marked private", http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"rider": rider})
		case "/v1/database/train-db/call/trainbot_service_list_riders":
			_ = json.NewEncoder(w).Encode(map[string]any{"riders": []spacetime.TrainbotRiderRow{rider}})
		case "/v1/database/train-db/call/trainbot_service_put_rider":
			var args []string
			if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args) != 1 {
				t.Fatalf("invalid rider write: %v", err)
			}
			if err := json.Unmarshal([]byte(args[0]), &rider); err != nil {
				t.Fatal(err)
			}
			writes++
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected read path %s", r.URL.Path)
			http.Error(w, "trainbot_rider may be marked private", http.StatusForbidden)
		}
	}))
	defer server.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "test-key.pem")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := spacetime.NewSyncer(spacetime.SyncConfig{Host: server.URL, Database: "train-db", JWTPrivateKeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	st := NewSpacetimeStore(client, time.UTC)
	ctx := context.Background()
	if err := st.SetAlertsEnabled(ctx, 42, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlertStyle(ctx, 42, domain.AlertStyleDiscreet); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLanguage(ctx, 42, domain.LanguageEN); err != nil {
		t.Fatal(err)
	}
	settings, err := st.GetUserSettings(ctx, 42)
	if err != nil || settings.AlertsEnabled || settings.AlertStyle != domain.AlertStyleDiscreet || settings.Language != domain.LanguageEN {
		t.Fatalf("settings reverted after sequential saves: %+v, %v", settings, err)
	}
	if len(rider.Favorites) != 1 || rider.CurrentRide == nil || rider.CurrentRide.TrainInstanceID != "saved-train" || rider.Nickname != "Saved rider" {
		t.Fatalf("settings save discarded existing rider data: %+v", rider)
	}
	if len(rider.Mutes) != 1 || rider.Mutes[0].TrainInstanceID != "muted-train" || rider.Mutes[0].MutedUntil != "2099-01-01T00:00:00Z" ||
		len(rider.Subscriptions) != 1 || rider.Subscriptions[0].TrainInstanceID != "followed-train" || !rider.Subscriptions[0].IsActive || rider.Subscriptions[0].ExpiresAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("settings save discarded notification preferences: %+v", rider)
	}
	riders, err := client.ServiceListRiders(ctx)
	if err != nil || len(riders) != 1 || riders[0].Settings.AlertsEnabled {
		t.Fatalf("rider listing lost saved preferences: %+v %v", riders, err)
	}
	failRead = true
	if _, err := st.GetUserSettings(ctx, 42); err == nil {
		t.Fatal("failed read must not create a default rider")
	}
	if writes != 3 {
		t.Fatalf("expected only the three requested setting writes, got %d", writes)
	}
}

func TestLocationReportSignalRoundTrip(t *testing.T) {
	t.Parallel()

	latitude := 56.95721
	longitude := 23.68939
	encoded := encodeLocationReportSignal(&latitude, &longitude, 250)
	if encoded != "LOC:56.95721,23.68939,250" {
		t.Fatalf("encodeLocationReportSignal() = %q", encoded)
	}

	decoded, ok := decodeLocationReportSignal(encoded)
	if !ok {
		t.Fatalf("decodeLocationReportSignal() ok = false")
	}
	if decoded.Latitude == nil || *decoded.Latitude != latitude {
		t.Fatalf("decoded latitude = %v, want %v", decoded.Latitude, latitude)
	}
	if decoded.Longitude == nil || *decoded.Longitude != longitude {
		t.Fatalf("decoded longitude = %v, want %v", decoded.Longitude, longitude)
	}
	if decoded.RadiusMeters != 250 {
		t.Fatalf("decoded radius = %d, want 250", decoded.RadiusMeters)
	}
}

func TestLocationReportSignalIgnoresOtherSignals(t *testing.T) {
	t.Parallel()

	if _, ok := decodeLocationReportSignal("INSPECTION_STARTED"); ok {
		t.Fatalf("decodeLocationReportSignal() ok = true for report signal")
	}
}

func TestMapSpacetimeMutationErrorTreatsStationDuplicateAsDedupe(t *testing.T) {
	t.Parallel()

	wantRemaining := 90 * time.Second
	err := mapSpacetimeMutationError(errors.New("reducer failed: duplicate station sighting ignored"), wantRemaining, time.Hour)
	var rejected *MutationRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("mapSpacetimeMutationError() = %v, want MutationRejectedError", err)
	}
	if rejected.Reason != MutationReportDuplicate {
		t.Fatalf("reason = %q, want %q", rejected.Reason, MutationReportDuplicate)
	}
	if rejected.Remaining != wantRemaining {
		t.Fatalf("remaining = %v, want %v", rejected.Remaining, wantRemaining)
	}
}
