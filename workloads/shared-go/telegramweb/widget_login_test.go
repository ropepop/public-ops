package telegramweb

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateLoginWidget(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	botToken := "123456:telegram-widget-secret"
	values := url.Values{
		"id":         {"777001"},
		"first_name": {"Kontrole"},
		"last_name":  {"Tester"},
		"username":   {"kontroletester"},
		"photo_url":  {"https://t.me/i/userpic/320/test.jpg"},
		"auth_date":  {strconvFormat(now.Unix())},
	}
	values.Set("hash", widgetHash(t, values, botToken))

	auth, err := ValidateLoginWidget(values, botToken, 5*time.Minute, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("ValidateLoginWidget() error = %v", err)
	}
	if auth.User.ID != 777001 {
		t.Fatalf("auth.User.ID = %d, want 777001", auth.User.ID)
	}
	if auth.User.Username != "kontroletester" {
		t.Fatalf("auth.User.Username = %q", auth.User.Username)
	}
}

func TestValidateLoginWidgetRejectsBadHashAndExpiry(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	botToken := "123456:telegram-widget-secret"
	values := url.Values{
		"id":         {"777001"},
		"first_name": {"Kontrole"},
		"auth_date":  {strconvFormat(now.Unix())},
	}
	values.Set("hash", widgetHash(t, values, botToken))

	badHash := cloneValues(values)
	badHash.Set("hash", strings.Repeat("0", 64))
	if _, err := ValidateLoginWidget(badHash, botToken, 5*time.Minute, now); err == nil {
		t.Fatalf("ValidateLoginWidget(bad hash) error = nil")
	}

	if _, err := ValidateLoginWidget(values, botToken, 30*time.Second, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("ValidateLoginWidget(expired) error = nil")
	}
}

func widgetHash(t *testing.T, values url.Values, botToken string) string {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "hash" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secret := sha256.Sum256([]byte(botToken))
	return hex.EncodeToString(hmacSHA256(secret[:], []byte(strings.Join(lines, "\n"))))
}

func cloneValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, value := range values {
		clone[key] = append([]string{}, value...)
	}
	return clone
}

func strconvFormat(value int64) string {
	return strconv.FormatInt(value, 10)
}
