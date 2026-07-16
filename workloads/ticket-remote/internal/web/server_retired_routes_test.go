package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ticketremote/internal/phone"
)

func TestRetiredTicketRoutesReturnOneCompactGoneContract(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://phone.test"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")
	t.Cleanup(server.Close)

	for _, path := range []string{
		"/api/v1/session",
		"/api/v1/me",
		"/api/v1/state",
		"/api/v1/client-log",
		"/api/v1/control-code/request",
		"/api/v1/control-code/prepare",
		"/api/v1/control-code/capture",
		"/api/v1/control-code/close",
		"/api/v1/control/claim",
		"/api/v1/control/extend",
		"/api/v1/control/release",
		"/api/v1/admin/control/revoke",
	} {
		t.Run(strings.TrimPrefix(path, "/api/v1/"), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusGone {
				t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusGone)
			}
			if body := rec.Body.String(); !strings.Contains(body, `"error":"route_retired"`) || !strings.Contains(body, "direct Spacetime flow") {
				t.Fatalf("%s body = %q", path, body)
			}
		})
	}
}
