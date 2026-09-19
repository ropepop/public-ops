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
