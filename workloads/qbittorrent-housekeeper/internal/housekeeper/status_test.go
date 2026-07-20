package housekeeper

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthRequiresRecentSuccessfulPoll(t *testing.T) {
	now := fixedNow
	store := NewStatusStore()
	handler := store.Handler(func() time.Time { return now }, time.Minute)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("initial health status = %d", response.Code)
	}

	store.RecordSuccess(Snapshot{LastAttempt: now, LastSuccess: now, Healthy: true})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("fresh health status = %d", response.Code)
	}

	now = now.Add(2 * time.Minute)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale health status = %d", response.Code)
	}
}
