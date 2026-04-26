package telegramweb

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

type User struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	PhotoURL     string `json:"photo_url,omitempty"`
	LanguageCode string `json:"language_code"`
}

type Auth struct {
	QueryID  string
	AuthDate time.Time
	User     User
}

type SessionClaims struct {
	UserID    int64  `json:"user_id"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Language  string `json:"language,omitempty"`
}

type SessionConfig struct {
	CookieName       string
	SessionTTL       time.Duration
	Path             string
	LanguageResolver func(string) string
}

func LoadSessionSecret(path string, label string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", secretLabel(label), err)
	}
	secret := strings.TrimSpace(string(raw))
	if len(secret) < 16 {
		return nil, fmt.Errorf("%s must be at least 16 characters", secretLabel(label))
	}
	return []byte(secret), nil
}

func ValidateInitData(initData string, botToken string, maxAge time.Duration, now time.Time) (Auth, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return Auth{}, fmt.Errorf("parse initData: %w", err)
	}
	hashHex := strings.TrimSpace(values.Get("hash"))
	if hashHex == "" {
		return Auth{}, errors.New("missing hash")
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
		return Auth{}, fmt.Errorf("decode hash: %w", err)
	}
	if len(actual) != len(expected) || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return Auth{}, errors.New("invalid Telegram initData signature")
	}

	authRaw := strings.TrimSpace(values.Get("auth_date"))
	if authRaw == "" {
		return Auth{}, errors.New("missing auth_date")
	}
	authUnix, err := strconv.ParseInt(authRaw, 10, 64)
	if err != nil {
		return Auth{}, fmt.Errorf("invalid auth_date: %w", err)
	}
	authAt := time.Unix(authUnix, 0).UTC()
	if now.UTC().Sub(authAt) > maxAge {
		return Auth{}, errors.New("Telegram initData expired")
	}

	userRaw := values.Get("user")
	if strings.TrimSpace(userRaw) == "" {
		return Auth{}, errors.New("missing Telegram user")
	}
	var user User
	if err := json.Unmarshal([]byte(userRaw), &user); err != nil {
		return Auth{}, fmt.Errorf("decode Telegram user: %w", err)
	}
	if user.ID <= 0 {
		return Auth{}, errors.New("invalid Telegram user id")
	}
	return Auth{
		QueryID:  values.Get("query_id"),
		AuthDate: authAt,
		User:     user,
	}, nil
}

func IssueSessionCookie(secret []byte, cfg SessionConfig, auth Auth, now time.Time) (*http.Cookie, error) {
	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 12 * time.Hour
	}
	cookieName := strings.TrimSpace(cfg.CookieName)
	if cookieName == "" {
		cookieName = "telegram_app_session"
	}
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = "/"
	}
	language := strings.TrimSpace(auth.User.LanguageCode)
	if cfg.LanguageResolver != nil {
		language = cfg.LanguageResolver(language)
	}
	claims := SessionClaims{
		UserID:    auth.User.ID,
		IssuedAt:  now.UTC().Unix(),
		ExpiresAt: now.UTC().Add(sessionTTL).Unix(),
		Language:  language,
	}
	token, err := signSessionClaims(secret, claims)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     path,
		HttpOnly: true,
		MaxAge:   int(sessionTTL.Seconds()),
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}, nil
}

func ParseSession(secret []byte, raw string, now time.Time) (SessionClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return SessionClaims{}, errors.New("missing session")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return SessionClaims{}, errors.New("invalid session format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SessionClaims{}, fmt.Errorf("decode session payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SessionClaims{}, fmt.Errorf("decode session signature: %w", err)
	}
	expected := hmacSHA256(secret, payload)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return SessionClaims{}, errors.New("invalid session signature")
	}
	var claims SessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return SessionClaims{}, fmt.Errorf("decode session claims: %w", err)
	}
	if claims.UserID <= 0 {
		return SessionClaims{}, errors.New("invalid session user")
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return SessionClaims{}, errors.New("session expired")
	}
	return claims, nil
}

func signSessionClaims(secret []byte, claims SessionClaims) (string, error) {
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

func secretLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "session secret"
	}
	return label
}
