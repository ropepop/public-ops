package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"pixelops/shared/telegramweb"
)

func TestIssueAndParseLoginStateCookie(t *testing.T) {
	secret := []byte("0123456789abcdef")
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)

	claims, cookie, err := issueLoginStateCookie(secret, "/incidents?incident=stop%3A3012", 5*time.Minute, now)
	if err != nil {
		t.Fatalf("issueLoginStateCookie() error = %v", err)
	}
	if claims.State == "" || claims.Nonce == "" || claims.CodeVerifier == "" {
		t.Fatalf("claims = %#v, want state, nonce, and code verifier", claims)
	}
	if cookie == nil {
		t.Fatalf("cookie = nil")
	}
	if cookie.Name != loginStateCookieName {
		t.Fatalf("cookie.Name = %q, want %q", cookie.Name, loginStateCookieName)
	}

	got, err := parseLoginState(secret, cookie.Value, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("parseLoginState() error = %v", err)
	}
	if got.State != claims.State {
		t.Fatalf("parseLoginState().State = %q, want %q", got.State, claims.State)
	}
	if got.ReturnTo != claims.ReturnTo {
		t.Fatalf("parseLoginState().ReturnTo = %q, want %q", got.ReturnTo, claims.ReturnTo)
	}
	if challenge := telegramweb.PKCEChallengeS256(got.CodeVerifier); strings.TrimSpace(challenge) == "" {
		t.Fatalf("PKCEChallengeS256() = empty")
	}
}

func TestParseLoginStateRejectsTamperingAndExpiry(t *testing.T) {
	secret := []byte("0123456789abcdef")
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)

	_, cookie, err := issueLoginStateCookie(secret, "/", 90*time.Second, now)
	if err != nil {
		t.Fatalf("issueLoginStateCookie() error = %v", err)
	}

	tampered := cookie.Value + "broken"
	if _, err := parseLoginState(secret, tampered, now.Add(time.Minute)); err == nil {
		t.Fatalf("parseLoginState(tampered) error = nil")
	} else if !strings.Contains(strings.ToLower(err.Error()), "format") && !strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("parseLoginState(tampered) error = %v", err)
	}

	if _, err := parseLoginState(secret, cookie.Value, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("parseLoginState(expired) error = nil")
	} else if !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("parseLoginState(expired) error = %v", err)
	}
}

func TestIssueSessionCookieLivesForThirtyDays(t *testing.T) {
	secret := []byte("0123456789abcdef")
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)

	cookie, err := issueSessionCookie(secret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           777001,
			FirstName:    "Kontrole Tester",
			LanguageCode: "lv",
		},
	}, now)
	if err != nil {
		t.Fatalf("issueSessionCookie() error = %v", err)
	}
	if cookie == nil {
		t.Fatalf("cookie = nil")
	}
	if got, want := cookie.MaxAge, int((30 * 24 * time.Hour).Seconds()); got != want {
		t.Fatalf("cookie.MaxAge = %d, want %d", got, want)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie.SameSite = %v, want Lax", cookie.SameSite)
	}
	claims, err := parseSession(secret, cookie.Value, now.Add(29*24*time.Hour))
	if err != nil {
		t.Fatalf("parseSession(before expiry) error = %v", err)
	}
	if got, want := time.Unix(claims.ExpiresAt, 0).UTC(), now.Add(30*24*time.Hour); !got.Equal(want) {
		t.Fatalf("claims expiry = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if _, err := parseSession(secret, cookie.Value, now.Add(31*24*time.Hour)); err == nil {
		t.Fatalf("parseSession(after expiry) error = nil")
	}
}

func TestSessionLanguageCodeDefaultsToLatvian(t *testing.T) {
	if got := sessionLanguageCode(""); got != "lv" {
		t.Fatalf("sessionLanguageCode(\"\") = %q, want lv", got)
	}
	if got := sessionLanguageCode("EN"); got != "en" {
		t.Fatalf("sessionLanguageCode(\"EN\") = %q, want en", got)
	}
}
