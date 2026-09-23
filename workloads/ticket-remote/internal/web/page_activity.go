package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"ticketremote/internal/state"
)

// Viewing samples are independent of the viewer subscription and phone state.
// Membership and deduplication are checked atomically by the service reducer.
func (s *Server) handlePageActivity(w http.ResponseWriter, r *http.Request) {
	writeNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.browserWebSocketOriginAllowed(r) {
		http.Error(w, "bad_origin", http.StatusForbidden)
		return
	}
	id, err := s.auth.IdentityFromRequest(r.Context(), r)
	if err != nil {
		http.Error(w, "authentication_required", http.StatusUnauthorized)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		http.Error(w, "invalid_activity_request", http.StatusBadRequest)
		return
	}
	var input struct {
		AccountScopeID string  `json:"accountScopeId"`
		Slots          []int64 `json:"slots"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF || input.Slots == nil || len(input.Slots) > 720 {
		http.Error(w, "invalid_activity_request", http.StatusBadRequest)
		return
	}
	scope := ticketAccountScopeID(id.Email)
	if input.AccountScopeID != scope {
		http.Error(w, "activity_account_changed", http.StatusForbidden)
		return
	}
	accepted, discarded := splitActivitySlots(input.Slots, time.Now())
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.store.RecordMemberActivitySlots(ctx, s.cfg.TicketID, id.Email, accepted); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, state.ErrNotMember) || errors.Is(err, state.ErrForbidden) {
			status = http.StatusForbidden
		}
		http.Error(w, "activity_unavailable", status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountScopeId": scope, "serverTime": time.Now().UTC().Format(time.RFC3339Nano),
		"serverVersion": serverVersion, "acknowledgedSlots": accepted, "discardedSlots": discarded,
	})
}

func splitActivitySlots(slots []int64, now time.Time) ([]int64, []int64) {
	// The binary embeds tzdata, as do the existing Riga schedule handlers.
	zone, _ := time.LoadLocation("Europe/Riga")
	local := now.In(zone)
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, -29).Unix() / 5
	current := now.Unix() / 5
	accepted, discarded := make([]int64, 0, len(slots)), make([]int64, 0)
	seen := make(map[int64]bool, len(slots))
	for _, slot := range slots {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		if slot < cutoff || slot > current {
			discarded = append(discarded, slot)
		} else {
			accepted = append(accepted, slot)
		}
	}
	return accepted, discarded
}
