package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ropepop/ops/workloads/shared-go/telegramweb"
)

const (
	sessionCookieName    = "train_app_session"
	loginNonceCookieName = "train_login_nonce"
	sessionTTL           = 30 * 24 * time.Hour
)

type telegramUser = telegramweb.User

type telegramAuth = telegramweb.Auth

type sessionClaims = telegramweb.SessionClaims

type loginNonceClaims struct {
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"exp"`
}

func loadSessionSecret(path string) ([]byte, error) {
	return telegramweb.LoadSessionSecret(path, "train web session secret")
}

func verifyTelegramAuthAge(auth telegramAuth, maxAge time.Duration, now time.Time) error {
	if maxAge <= 0 || auth.AuthDate.IsZero() {
		return nil
	}
	if now.UTC().Sub(auth.AuthDate.UTC()) > maxAge {
		return errors.New("Telegram login is too old")
	}
	return nil
}

func validateTelegramInitData(initData string, botToken string, maxAge time.Duration, now time.Time) (telegramAuth, error) {
	return telegramweb.ValidateInitData(initData, botToken, maxAge, now)
}

func issueSessionCookie(secret []byte, auth telegramAuth, now time.Time) (*http.Cookie, error) {
	return issueSessionCookieWithSameSite(secret, auth, now, http.SameSiteLaxMode)
}

func issueMiniAppSessionCookie(secret []byte, auth telegramAuth, now time.Time) (*http.Cookie, error) {
	return issueSessionCookieWithSameSite(secret, auth, now, http.SameSiteNoneMode)
}

func issueSessionCookieWithSameSite(secret []byte, auth telegramAuth, now time.Time, sameSite http.SameSite) (*http.Cookie, error) {
	return telegramweb.IssueSessionCookie(secret, telegramweb.SessionConfig{
		CookieName: sessionCookieName,
		SessionTTL: sessionTTL,
		SameSite:   sameSite,
	}, auth, now)
}

func parseSession(secret []byte, raw string, now time.Time) (sessionClaims, error) {
	return telegramweb.ParseSession(secret, raw, now)
}

func issueLoginNonceCookie(secret []byte, ttl time.Duration, now time.Time) (loginNonceClaims, *http.Cookie, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	nonce, err := randomHexToken(16)
	if err != nil {
		return loginNonceClaims{}, nil, err
	}
	claims := loginNonceClaims{
		Nonce:     nonce,
		ExpiresAt: now.UTC().Add(ttl).Unix(),
	}
	value, err := signLoginNonceClaims(secret, claims)
	if err != nil {
		return loginNonceClaims{}, nil, err
	}
	return claims, &http.Cookie{
		Name:     loginNonceCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(ttl.Seconds()),
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

func parseLoginNonce(secret []byte, raw string, now time.Time) (loginNonceClaims, error) {
	claims, err := parseLoginNonceClaims(secret, raw)
	if err != nil {
		return loginNonceClaims{}, err
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return loginNonceClaims{}, errors.New("login nonce expired")
	}
	if strings.TrimSpace(claims.Nonce) == "" {
		return loginNonceClaims{}, errors.New("invalid login nonce")
	}
	return claims, nil
}

func clearSessionCookie(path string) *http.Cookie {
	return expiredCookie(sessionCookieName, path)
}

func clearLoginNonceCookie(path string) *http.Cookie {
	return expiredCookie(loginNonceCookieName, path)
}

func signLoginNonceClaims(secret []byte, claims loginNonceClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal login nonce claims: %w", err)
	}
	signature := authHMACSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseLoginNonceClaims(secret []byte, raw string) (loginNonceClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return loginNonceClaims{}, errors.New("missing login nonce")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return loginNonceClaims{}, errors.New("invalid login nonce format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return loginNonceClaims{}, fmt.Errorf("decode login nonce payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return loginNonceClaims{}, fmt.Errorf("decode login nonce signature: %w", err)
	}
	expected := authHMACSHA256(secret, payload)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return loginNonceClaims{}, errors.New("invalid login nonce signature")
	}
	var claims loginNonceClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return loginNonceClaims{}, fmt.Errorf("decode login nonce claims: %w", err)
	}
	return claims, nil
}

func randomHexToken(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 16
	}
	raw := make([]byte, byteLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func authHMACSHA256(secret []byte, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func expiredCookie(name string, path string) *http.Cookie {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		cleanPath = "/"
	}
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     cleanPath,
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}
