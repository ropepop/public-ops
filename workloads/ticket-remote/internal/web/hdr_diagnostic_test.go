package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHDRDiagnosticIsUnlinkedAndOwnerOnly(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)

	for _, path := range []string{"/", "/admin"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "/owner/hdr-diagnostic") {
			t.Fatalf("%s linked the owner diagnostic", path)
		}
	}

	for _, email := range []string{"admin@example.com", "member@example.com"} {
		for _, path := range []string{"/owner/hdr-diagnostic", "/owner/hdr-diagnostic/app.js"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-Ticket-Remote-Email", email)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d, want 404", email, path, rec.Code)
			}
		}
	}
}

func TestHDRDiagnosticPageAndScriptAreIsolated(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)

	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}
	normalPage := request("/")
	if strings.Contains(normalPage.Header().Get("Content-Security-Policy"), "ccameron-chromium.github.io") {
		t.Fatal("the public HDR image origin escaped the isolated diagnostic CSP")
	}

	page := request("/owner/hdr-diagnostic")
	if page.Code != http.StatusOK {
		t.Fatalf("diagnostic page status = %d body = %s", page.Code, page.Body.String())
	}
	for _, required := range []string{"Ticket HDR diagnostic", "/owner/hdr-diagnostic/app.js?v=", "hdrDiagnosticMount", "no-store"} {
		source := page.Body.String()
		if required == "no-store" {
			source = page.Header().Get("Cache-Control")
		}
		if !strings.Contains(source, required) {
			t.Fatalf("diagnostic page missing %q", required)
		}
	}
	for _, required := range []string{
		".reference-standard { dynamic-range-limit: standard; }",
		".reference-hdr { dynamic-range-limit: no-limit; }",
	} {
		if !strings.Contains(page.Body.String(), required) {
			t.Fatalf("diagnostic page lost its reference-image range contract %q", required)
		}
	}
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "https://ccameron-chromium.github.io") {
		t.Fatal("diagnostic page CSP does not allow its fixed public HDR reference")
	}
	for _, forbidden := range []string{"TICKET_REMOTE_CONFIG", "/api/v1/stream", "spacetime-client.js", "ticket@jolkins.id.lv"} {
		if strings.Contains(page.Body.String(), forbidden) {
			t.Fatalf("diagnostic page leaked or activated %q", forbidden)
		}
	}

	script := request("/owner/hdr-diagnostic/app.js")
	if script.Code != http.StatusOK {
		t.Fatalf("diagnostic script status = %d body = %s", script.Code, script.Body.String())
	}
	for _, required := range []string{
		"rgba16float", "no-limit", "intendedPeaks", "intendedColors", "magenta", "orange",
		"ticketHdrDiagnosticUi", "pescariello0-hlg.avif",
	} {
		if !strings.Contains(script.Body.String(), required) {
			t.Fatalf("diagnostic script missing %q; run make web-client-build", required)
		}
	}
	for _, forbidden := range []string{"WebSocket(", "localStorage", "sessionStorage", "experimental_hdr_diagnostic"} {
		if strings.Contains(script.Body.String(), forbidden) {
			t.Fatalf("diagnostic script retained side effect %q", forbidden)
		}
	}
}

func TestHDRDiagnosticRejectsMutatingMethods(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	for _, path := range []string{"/owner/hdr-diagnostic", "/owner/hdr-diagnostic/app.js"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("unused"))
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status = %d, want 405", path, rec.Code)
		}
	}
}
