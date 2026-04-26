package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "subscription_app_session"
	sessionTTL        = 12 * time.Hour
)

type telegramUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code"`
}

type telegramAuth struct {
	QueryID  string
	AuthDate time.Time
	User     telegramUser
}

type sessionClaims struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username,omitempty"`
	Language  string `json:"language,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func loadSessionSecret(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read subscription web session secret: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if len(secret) < 16 {
		return nil, fmt.Errorf("subscription web session secret must be at least 16 characters")
	}
	return []byte(secret), nil
}

func validateTelegramInitData(initData string, botToken string, maxAge time.Duration, now time.Time) (telegramAuth, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return telegramAuth{}, fmt.Errorf("parse initData: %w", err)
	}
	hashHex := strings.TrimSpace(values.Get("hash"))
	if hashHex == "" {
		return telegramAuth{}, errors.New("missing hash")
	}
	values.Del("hash")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, values.Get(key)))
	}
	dataCheckString := strings.Join(lines, "\n")
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	expected := hmacSHA256(secret, []byte(dataCheckString))
	actual, err := hex.DecodeString(hashHex)
	if err != nil {
		return telegramAuth{}, fmt.Errorf("decode hash: %w", err)
	}
	if len(actual) != len(expected) || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return telegramAuth{}, errors.New("invalid Telegram initData signature")
	}
	authRaw := strings.TrimSpace(values.Get("auth_date"))
	if authRaw == "" {
		return telegramAuth{}, errors.New("missing auth_date")
	}
	authUnix, err := strconv.ParseInt(authRaw, 10, 64)
	if err != nil {
		return telegramAuth{}, fmt.Errorf("invalid auth_date: %w", err)
	}
	authAt := time.Unix(authUnix, 0).UTC()
	if now.UTC().Sub(authAt) > maxAge {
		return telegramAuth{}, errors.New("Telegram initData expired")
	}
	userRaw := values.Get("user")
	if strings.TrimSpace(userRaw) == "" {
		return telegramAuth{}, errors.New("missing Telegram user")
	}
	var user telegramUser
	if err := json.Unmarshal([]byte(userRaw), &user); err != nil {
		return telegramAuth{}, fmt.Errorf("decode Telegram user: %w", err)
	}
	if user.ID <= 0 {
		return telegramAuth{}, errors.New("invalid Telegram user id")
	}
	return telegramAuth{
		QueryID:  values.Get("query_id"),
		AuthDate: authAt,
		User:     user,
	}, nil
}

func issueSessionCookie(secret []byte, auth telegramAuth, path string, now time.Time) (*http.Cookie, error) {
	claims := sessionClaims{
		UserID:    auth.User.ID,
		Username:  strings.TrimSpace(auth.User.Username),
		Language:  strings.TrimSpace(auth.User.LanguageCode),
		IssuedAt:  now.UTC().Unix(),
		ExpiresAt: now.UTC().Add(sessionTTL).Unix(),
	}
	token, err := signSessionClaims(secret, claims)
	if err != nil {
		return nil, err
	}
	scope := strings.TrimSpace(path)
	if scope == "" {
		scope = "/"
	}
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     scope,
		HttpOnly: true,
		MaxAge:   int(sessionTTL.Seconds()),
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}, nil
}

func parseSession(secret []byte, raw string, now time.Time) (sessionClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return sessionClaims{}, errors.New("missing session")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return sessionClaims{}, errors.New("invalid session format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return sessionClaims{}, fmt.Errorf("decode session payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return sessionClaims{}, fmt.Errorf("decode session signature: %w", err)
	}
	expected := hmacSHA256(secret, payload)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return sessionClaims{}, errors.New("invalid session signature")
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return sessionClaims{}, fmt.Errorf("decode session claims: %w", err)
	}
	if claims.UserID <= 0 {
		return sessionClaims{}, errors.New("invalid session user")
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return sessionClaims{}, errors.New("session expired")
	}
	return claims, nil
}

func signSessionClaims(secret []byte, claims sessionClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal session claims: %w", err)
	}
	signature := hmacSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func hmacSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
