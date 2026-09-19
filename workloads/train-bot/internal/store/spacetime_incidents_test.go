package store_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
)

func TestSpacetimeIncidentPublicIDsRoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "test-key.pem")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	const day = "2026-09-17"
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 1, 0, 0, 0, loc).UTC()
	for _, scope := range []string{"area", "station", "train"} {
		t.Run(scope, func(t *testing.T) {
			subject := "rīga—pārbaude"
			publicID := reports.LocationIncidentID(domain.LocationReport{Scope: scope, SubjectID: subject}, day)
			if scope == "train" {
				publicID = reports.TrainIncidentID(subject, day, "inspection")
			}
			activity := spacetime.TrainbotActivityRow{ID: scope + ":" + subject + ":" + day, ScopeType: scope, SubjectID: subject, ServiceDate: day}
			if scope == "area" {
				// Overnight schedule fallback may retain yesterday's service date.
				activity.ServiceDate = "2026-09-16"
				activity.ID = scope + ":" + subject + ":" + activity.ServiceDate
				activity.Timeline = []spacetime.TrainbotActivityEvent{{Kind: "location_report", CreatedAt: now.Format(time.RFC3339)}}
			}
			writes := 0
			failRead := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var args []string
				if err := json.NewDecoder(r.Body).Decode(&args); err != nil || r.Header.Get("Authorization") == "" {
					t.Error("invalid or unauthenticated request")
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				switch strings.TrimPrefix(r.URL.Path, "/v1/database/train-db/call/") {
				case "trainbot_service_get_trip":
					_ = json.NewEncoder(w).Encode(map[string]any{"trip": spacetime.TrainbotTripRow{ID: subject, ServiceDate: day}})
				case "trainbot_service_list_activities":
					if failRead {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					items := []spacetime.TrainbotActivityRow{}
					if len(args) == 4 && args[1] == scope && (args[2] == "" || args[2] == subject) && (args[3] == "" || args[3] == day) {
						// An older area with the same subject must never receive today's feedback.
						if scope == "area" {
							items = append(items, spacetime.TrainbotActivityRow{ID: "area:older", ScopeType: scope, SubjectID: subject, ServiceDate: "2026-09-16", Timeline: []spacetime.TrainbotActivityEvent{{Kind: "location_report", CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}}})
						}
						items = append(items, activity)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"activities": items})
				case "trainbot_service_submit_incident_comment", "trainbot_service_submit_incident_vote":
					if len(args) != 5 || args[1] != activity.ID {
						http.Error(w, "not found", http.StatusNotFound)
						return
					}
					writes++
					if strings.HasSuffix(r.URL.Path, "comment") {
						activity.Comments = append(activity.Comments, spacetime.TrainbotActivityComment{ID: args[0], StableID: args[2], Nickname: args[3], Body: args[4], CreatedAt: now.Format(time.RFC3339)})
					} else {
						activity.Votes = []spacetime.TrainbotActivityVote{{StableID: args[2], Nickname: args[3], Value: args[4], CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}}
					}
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.Error(w, "unexpected request", http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := spacetime.NewSyncer(spacetime.SyncConfig{Host: server.URL, Database: "train-db", JWTPrivateKeyFile: keyFile})
			if err != nil {
				t.Fatal(err)
			}
			st := store.NewSpacetimeStore(client, loc)
			ctx := context.Background()
			comment := domain.IncidentComment{ID: "comment", IncidentID: publicID, UserID: 42, Body: "QA test comment"}
			if err := st.SubmitIncidentComment(ctx, comment, store.CommentMutationPolicy{}); err != nil {
				t.Fatalf("public comment failed: %v", err)
			}
			vote := domain.IncidentVote{IncidentID: publicID, UserID: 42, Value: domain.IncidentVoteOngoing}
			if err := st.SubmitIncidentVote(ctx, vote, domain.IncidentVoteEvent{ID: "vote"}, store.VoteMutationPolicy{}); err != nil {
				t.Fatalf("public vote failed: %v", err)
			}
			comments, err := st.ListIncidentComments(ctx, publicID, 100)
			if err != nil || len(comments) != 1 || comments[0].IncidentID != publicID || comments[0].Body != comment.Body {
				t.Fatalf("public comment readback failed: %+v, %v", comments, err)
			}
			votes, err := st.ListIncidentVotes(ctx, publicID)
			if err != nil || len(votes) != 1 || votes[0].IncidentID != publicID || votes[0].Value != vote.Value {
				t.Fatalf("public vote readback failed: %+v, %v", votes, err)
			}
			events, err := st.ListIncidentVoteEvents(ctx, publicID, now.Add(-time.Minute), 100)
			if err != nil || len(events) != 1 || events[0].IncidentID != publicID {
				t.Fatalf("public vote event readback failed: %+v, %v", events, err)
			}
			comment.IncidentID = "area:pub-missing"
			if err := st.SubmitIncidentComment(ctx, comment, store.CommentMutationPolicy{}); err == nil {
				t.Fatal("unknown incident accepted")
			}
			failRead = true
			if err := st.SubmitIncidentVote(ctx, vote, domain.IncidentVoteEvent{ID: "retry"}, store.VoteMutationPolicy{}); err == nil {
				t.Fatal("lookup failure accepted")
			}
			if writes != 2 {
				t.Fatalf("unexpected mutation count %d", writes)
			}
		})
	}
}
