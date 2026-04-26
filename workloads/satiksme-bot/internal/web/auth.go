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

	"pixelops/shared/telegramweb"
)

const (
	sessionCookieName    = "satiksme_app_session"
	loginStateCookieName = "satiksme_login_state"
	loginNonceCookieName = "satiksme_login_nonce"
	sessionTTL           = 12 * time.Hour
	defaultAppLanguage   = "lv"
)

type telegramUser = telegramweb.User

type telegramAuth = telegramweb.Auth

type sessionClaims = telegramweb.SessionClaims

type loginStateClaims struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ReturnTo     string `json:"return_to"`
	ExpiresAt    int64  `json:"exp"`
}

type loginNonceClaims struct {
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"exp"`
}

func loadSessionSecret(path string) ([]byte, error) {
	return telegramweb.LoadSessionSecret(path, "satiksme web session secret")
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

func normalizeTelegramBotUsername(raw string) string {
	return strings.TrimSpace(strings.TrimPrefix(raw, "@"))
}

func issueSessionCookie(secret []byte, auth telegramAuth, now time.Time) (*http.Cookie, error) {
	return telegramweb.IssueSessionCookie(secret, telegramweb.SessionConfig{
		CookieName:       sessionCookieName,
		SessionTTL:       sessionTTL,
		LanguageResolver: sessionLanguageCode,
	}, auth, now)
}

func parseSession(secret []byte, raw string, now time.Time) (sessionClaims, error) {
	return telegramweb.ParseSession(secret, raw, now)
}

func issueLoginStateCookie(secret []byte, returnTo string, ttl time.Duration, now time.Time) (loginStateClaims, *http.Cookie, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	state, err := randomHexToken(16)
	if err != nil {
		return loginStateClaims{}, nil, err
	}
	nonce, err := randomHexToken(16)
	if err != nil {
		return loginStateClaims{}, nil, err
	}
	codeVerifier, err := randomBase64URLToken(32)
	if err != nil {
		return loginStateClaims{}, nil, err
	}
	claims := loginStateClaims{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		ReturnTo:     strings.TrimSpace(returnTo),
		ExpiresAt:    now.UTC().Add(ttl).Unix(),
	}
	value, err := signLoginStateClaims(secret, claims)
	if err != nil {
		return loginStateClaims{}, nil, err
	}
	return claims, &http.Cookie{
		Name:     loginStateCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(ttl.Seconds()),
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

func parseLoginState(secret []byte, raw string, now time.Time) (loginStateClaims, error) {
	claims, err := parseLoginStateClaims(secret, raw)
	if err != nil {
		return loginStateClaims{}, err
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return loginStateClaims{}, errors.New("login state expired")
	}
	if strings.TrimSpace(claims.State) == "" || strings.TrimSpace(claims.Nonce) == "" || strings.TrimSpace(claims.CodeVerifier) == "" {
		return loginStateClaims{}, errors.New("invalid login state")
	}
	return claims, nil
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

func clearLoginStateCookie(path string) *http.Cookie {
	return expiredCookie(loginStateCookieName, path)
}

func clearLoginNonceCookie(path string) *http.Cookie {
	return expiredCookie(loginNonceCookieName, path)
}

func clearLegacyLoginNonceCookie(path string) *http.Cookie {
	return clearLoginNonceCookie(path)
}

func sessionLanguageCode(raw string) string {
	language := strings.ToLower(strings.TrimSpace(raw))
	if language == "" {
		return defaultAppLanguage
	}
	return language
}

func signLoginStateClaims(secret []byte, claims loginStateClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal login state claims: %w", err)
	}
	signature := authHMACSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseLoginStateClaims(secret []byte, raw string) (loginStateClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return loginStateClaims{}, errors.New("missing login state")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return loginStateClaims{}, errors.New("invalid login state format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return loginStateClaims{}, fmt.Errorf("decode login state payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return loginStateClaims{}, fmt.Errorf("decode login state signature: %w", err)
	}
	expected := authHMACSHA256(secret, payload)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return loginStateClaims{}, errors.New("invalid login state signature")
	}
	var claims loginStateClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return loginStateClaims{}, fmt.Errorf("decode login state claims: %w", err)
	}
	return claims, nil
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

func randomHexToken(size int) (string, error) {
	if size <= 0 {
		size = 16
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate login token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func randomBase64URLToken(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate PKCE token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func expiredCookie(name string, path string) *http.Cookie {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		MaxAge:   -1,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

func authHMACSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
