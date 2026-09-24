package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestErrorPageStylesRemainAuthorizedAndMessageEscaped(t *testing.T) {
	response := httptest.NewRecorder()
	writeErrorPage(response, http.StatusForbidden, `No ticket assigned to <script>alert(1)</script>.`)
	body := response.Body.String()
	nonce := regexp.MustCompile(`<style nonce="([^"]+)">`).FindStringSubmatch(body)
	if response.Code != http.StatusForbidden || len(nonce) != 2 ||
		!strings.Contains(response.Header().Get("Content-Security-Policy"), "style-src 'self' 'nonce-"+nonce[1]+"'") ||
		!strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("error response lost its status, cache protection, or authorized styling")
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("error message must remain plain escaped text")
	}
}

func TestErrorPageRecoveryDoesNotLoopDeniedMembers(t *testing.T) {
	for _, tc := range []struct {
		name, message, heading, action string
		status                         int
	}{
		{"sign-in", "Login callback did not match this browser.", "Sign-in needs to restart", `href="/api/v1/auth/start"`, http.StatusUnauthorized},
		{"admin", "Admin access is required.", "Admin access required", `href="/"`, http.StatusForbidden},
		{"membership", "No ticket has been assigned to your account.", "Ticket access required", "", http.StatusForbidden},
		{"outage", "Ticket state is unavailable.", "Ticket is temporarily unavailable", `href="/"`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeErrorPage(response, tc.status, tc.message)
			body := response.Body.String()
			if response.Code != tc.status || !strings.Contains(body, tc.heading) || !strings.Contains(body, tc.message) {
				t.Fatal("error page lost its status or explanation")
			}
			if tc.action == "" {
				if strings.Contains(body, "<a ") {
					t.Fatal("denied account must not be sent back into the denial")
				}
			} else if !strings.Contains(body, tc.action) {
				t.Fatalf("missing safe recovery action %q", tc.action)
			}
		})
	}
}
