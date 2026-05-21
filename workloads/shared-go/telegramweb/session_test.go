package telegramweb

import (
	"net/http"
	"testing"
	"time"
)

func TestIssueSessionCookieUsesLaxSameSite(t *testing.T) {
	t.Parallel()

	cookie, err := IssueSessionCookie([]byte("0123456789abcdef0123456789abcdef"), SessionConfig{
		CookieName: "test_session",
		SessionTTL: time.Hour,
	}, Auth{User: User{ID: 77}}, time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("IssueSessionCookie() error = %v", err)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie.SameSite = %v, want Lax", cookie.SameSite)
	}
}
