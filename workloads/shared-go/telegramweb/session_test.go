package telegramweb

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

func TestValidateInitDataAcceptsOptionalThirdPartySignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	botToken := "123456:telegram-mini-secret"
	values := url.Values{
		"query_id":  {"AAEAAAE"},
		"auth_date": {strconv.FormatInt(now.Unix(), 10)},
		"user":      {`{"id":777001,"first_name":"Kontrole Tester","language_code":"lv"}`},
		"signature": {"third-party-ed25519-signature"},
	}
	values.Set("hash", initDataHashForTest(values, botToken, true))

	auth, err := ValidateInitData(values.Encode(), botToken, 5*time.Minute, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ValidateInitData() error = %v", err)
	}
	if auth.User.ID != 777001 {
		t.Fatalf("auth.User.ID = %d, want 777001", auth.User.ID)
	}
	if auth.User.LanguageCode != "lv" {
		t.Fatalf("auth.User.LanguageCode = %q, want lv", auth.User.LanguageCode)
	}
}

func initDataHashForTest(values url.Values, botToken string, skipSignature bool) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.EqualFold(key, "hash") || (skipSignature && strings.EqualFold(key, "signature")) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	return hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(lines, "\n"))))
}
