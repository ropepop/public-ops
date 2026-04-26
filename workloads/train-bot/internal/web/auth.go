package web

import (
	"net/http"
	"time"

	"pixelops/shared/telegramweb"
)

const (
	sessionCookieName = "train_app_session"
	sessionTTL        = 12 * time.Hour
)

type telegramUser = telegramweb.User

type telegramAuth = telegramweb.Auth

type sessionClaims = telegramweb.SessionClaims

func loadSessionSecret(path string) ([]byte, error) {
	return telegramweb.LoadSessionSecret(path, "train web session secret")
}

func validateTelegramInitData(initData string, botToken string, maxAge time.Duration, now time.Time) (telegramAuth, error) {
	return telegramweb.ValidateInitData(initData, botToken, maxAge, now)
}

func issueSessionCookie(secret []byte, auth telegramAuth, now time.Time) (*http.Cookie, error) {
	return telegramweb.IssueSessionCookie(secret, telegramweb.SessionConfig{
		CookieName: sessionCookieName,
		SessionTTL: sessionTTL,
	}, auth, now)
}

func parseSession(secret []byte, raw string, now time.Time) (sessionClaims, error) {
	return telegramweb.ParseSession(secret, raw, now)
}
