package web

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/store"
)

func TestValidateTelegramInitDataAcceptsValidPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-2 * time.Minute),
		User: telegramUser{
			ID:           123456789,
			FirstName:    "Alex",
			LanguageCode: "lv",
		},
	}

	initData := signedInitData(t, "bot-token", auth)
	got, err := validateTelegramInitData(initData, "bot-token", 5*time.Minute, now)
	if err != nil {
		t.Fatalf("validateTelegramInitData: %v", err)
	}
	if got.User.ID != auth.User.ID {
		t.Fatalf("unexpected user id: got %d want %d", got.User.ID, auth.User.ID)
	}
	if got.User.LanguageCode != auth.User.LanguageCode {
		t.Fatalf("unexpected language: got %q want %q", got.User.LanguageCode, auth.User.LanguageCode)
	}
}

func TestValidateTelegramInitDataRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-1 * time.Minute),
		User:     telegramUser{ID: 42, FirstName: "Alex", LanguageCode: "en"},
	}

	initData := signedInitData(t, "bot-token", auth)
	if _, err := validateTelegramInitData(initData, "different-token", 5*time.Minute, now); err == nil {
		t.Fatalf("expected signature validation error")
	}
}

func TestValidateTelegramInitDataRejectsExpiredPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-10 * time.Minute),
		User:     telegramUser{ID: 42, FirstName: "Alex", LanguageCode: "en"},
	}

	initData := signedInitData(t, "bot-token", auth)
	if _, err := validateTelegramInitData(initData, "bot-token", 5*time.Minute, now); err == nil {
		t.Fatalf("expected expiry error")
	}
}

func TestIssueSessionCookieRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	cookie, err := issueSessionCookie([]byte("0123456789abcdef0123456789abcdef"), telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           77,
			LanguageCode: "lv",
		},
	}, now)
	if err != nil {
		t.Fatalf("issueSessionCookie: %v", err)
	}

	claims, err := parseSession([]byte("0123456789abcdef0123456789abcdef"), cookie.Value, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("parseSession: %v", err)
	}
	if claims.UserID != 77 {
		t.Fatalf("unexpected user id: got %d", claims.UserID)
	}
	if claims.Language != "lv" {
		t.Fatalf("unexpected language: got %q", claims.Language)
	}
}

func TestTestLoginBrokerRoundTripAndMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	broker := &testLoginBroker{
		secret: []byte("0123456789abcdef0123456789abcdef"),
		userID: 7001,
		ttl:    time.Minute,
	}

	ticket, err := broker.Mint(now)
	if err != nil {
		t.Fatalf("mint test login ticket: %v", err)
	}
	claims, meta, err := broker.Consume(ticket, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("consume test login ticket: %v", err)
	}
	if claims.Nonce == "" {
		t.Fatalf("expected nonce in claims")
	}
	if meta.NonceHash == "" {
		t.Fatalf("expected nonce hash in ticket metadata")
	}
	if !meta.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected expiry: got %s want %s", meta.ExpiresAt, now.Add(time.Minute))
	}
	if _, repeatedMeta, err := broker.Consume(ticket, now.Add(20*time.Second)); err != nil {
		t.Fatalf("expected repeated broker validation to stay stateless, got %v", err)
	} else if repeatedMeta.NonceHash != meta.NonceHash {
		t.Fatalf("expected repeated validation to preserve nonce hash, got %+v want %+v", repeatedMeta, meta)
	}
}

func TestTestLoginBrokerRejectsExpiredTicket(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	broker := &testLoginBroker{
		secret: []byte("0123456789abcdef0123456789abcdef"),
		userID: 7001,
		ttl:    15 * time.Second,
	}

	ticket, err := broker.Mint(now)
	if err != nil {
		t.Fatalf("mint test login ticket: %v", err)
	}
	if _, _, err := broker.Consume(ticket, now.Add(16*time.Second)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired ticket rejection, got %v", err)
	}
}

func TestAuthTestResetsFixedUserAndCreatesNormalSession(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.testLogin = &testLoginBroker{
		secret: []byte("0123456789abcdef0123456789abcdef"),
		userID: 7001,
		ttl:    time.Minute,
	}

	if err := st.SetAlertsEnabled(context.Background(), 7001, false); err != nil {
		t.Fatalf("disable alerts: %v", err)
	}
	if err := st.SetAlertStyle(context.Background(), 7001, domain.AlertStyleDiscreet); err != nil {
		t.Fatalf("set alert style: %v", err)
	}
	if err := st.SetLanguage(context.Background(), 7001, domain.LanguageLV); err != nil {
		t.Fatalf("set language: %v", err)
	}
	if err := st.UpsertFavoriteRoute(context.Background(), 7001, "riga", "jelgava"); err != nil {
		t.Fatalf("upsert favorite route: %v", err)
	}
	if err := st.InsertReportEvent(context.Background(), domain.ReportEvent{
		ID:              "report-reset",
		TrainInstanceID: "train-past",
		UserID:          7001,
		Signal:          domain.SignalInspectionStarted,
		CreatedAt:       now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("insert report event: %v", err)
	}

	ticket, err := server.testLogin.Mint(now)
	if err != nil {
		t.Fatalf("mint test login ticket: %v", err)
	}
	body, err := json.Marshal(map[string]string{"ticket": ticket})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/test", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTest(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		OK           bool   `json:"ok"`
		UserID       int64  `json:"userId"`
		StableUserID string `json:"stableUserId"`
		Lang         string `json:"lang"`
		BaseURL      string `json:"baseUrl"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth test payload: %v", err)
	}
	if !payload.OK || payload.UserID != 7001 || payload.StableUserID != "telegram:7001" {
		t.Fatalf("unexpected auth test payload: %+v", payload)
	}
	if payload.Lang != "EN" {
		t.Fatalf("expected reset language EN, got %q", payload.Lang)
	}
	if payload.BaseURL != "https://example.test/pixel-stack/train" {
		t.Fatalf("unexpected base url: %q", payload.BaseURL)
	}

	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}

	settings, err := st.GetUserSettings(context.Background(), 7001)
	if err != nil {
		t.Fatalf("get reset settings: %v", err)
	}
	if !settings.AlertsEnabled || settings.AlertStyle != domain.AlertStyleDetailed || settings.Language != domain.LanguageEN {
		t.Fatalf("expected reset defaults, got %+v", settings)
	}
	favorites, err := st.ListFavoriteRoutes(context.Background(), 7001)
	if err != nil {
		t.Fatalf("list favorites after reset: %v", err)
	}
	if len(favorites) != 0 {
		t.Fatalf("expected favorites cleared, got %+v", favorites)
	}
	reports, err := st.ListRecentReports(context.Background(), "train-past", 10)
	if err != nil {
		t.Fatalf("list reports after reset: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("expected reports cleared, got %+v", reports)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/me", nil)
	meReq.AddCookie(cookies[0])
	meRes := httptest.NewRecorder()
	server.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK {
		t.Fatalf("expected authenticated /me after test auth, got %d body=%s", meRes.Code, meRes.Body.String())
	}
}

func TestAuthTestRejectsReusedTicket(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.testLogin = &testLoginBroker{
		secret: []byte("0123456789abcdef0123456789abcdef"),
		userID: 7001,
		ttl:    time.Minute,
	}
	now := time.Now().UTC()

	ticket, err := server.testLogin.Mint(now)
	if err != nil {
		t.Fatalf("mint test login ticket: %v", err)
	}
	body, err := json.Marshal(map[string]string{"ticket": ticket})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	firstReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/test", bytes.NewReader(body))
	firstRes := httptest.NewRecorder()
	server.handleAuthTest(firstRes, firstReq, now)
	if firstRes.Code != http.StatusOK {
		t.Fatalf("expected first test auth to succeed, got %d body=%s", firstRes.Code, firstRes.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/test", bytes.NewReader(body))
	secondRes := httptest.NewRecorder()
	server.handleAuthTest(secondRes, secondReq, now.Add(5*time.Second))
	if secondRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected reused ticket rejection, got %d body=%s", secondRes.Code, secondRes.Body.String())
	}
}

func TestAuthTestRejectsReusedTicketAfterRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Now().UTC()

	firstServer, firstStore := newPersistentAuthTestServer(t, "https://example.test/pixel-stack/train", dir)
	ticket, err := firstServer.testLogin.Mint(now)
	if err != nil {
		t.Fatalf("mint test login ticket: %v", err)
	}
	body, err := json.Marshal(map[string]string{"ticket": ticket})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	firstReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/test", bytes.NewReader(body))
	firstRes := httptest.NewRecorder()
	firstServer.handleAuthTest(firstRes, firstReq, now)
	if firstRes.Code != http.StatusOK {
		t.Fatalf("expected first auth test to succeed, got %d body=%s", firstRes.Code, firstRes.Body.String())
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	secondServer, _ := newPersistentAuthTestServer(t, "https://example.test/pixel-stack/train", dir)
	secondReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/test", bytes.NewReader(body))
	secondRes := httptest.NewRecorder()
	secondServer.handleAuthTest(secondRes, secondReq, now.Add(5*time.Second))
	if secondRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected reused ticket rejection after restart, got %d body=%s", secondRes.Code, secondRes.Body.String())
	}
	if !strings.Contains(secondRes.Body.String(), "already used") {
		t.Fatalf("expected already-used error after restart, got %s", secondRes.Body.String())
	}
}

func TestAuthTelegramSetsScopedSessionCookie(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	server := newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           77,
			FirstName:    "Alex",
			LanguageCode: "en",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Path != "/pixel-stack/train" {
		t.Fatalf("unexpected cookie path: %q", cookies[0].Path)
	}
	if !cookies[0].Secure {
		t.Fatalf("expected secure cookie")
	}
	if cookies[0].SameSite != http.SameSiteNoneMode {
		t.Fatalf("unexpected SameSite: %v", cookies[0].SameSite)
	}
}

func TestAuthTelegramSetsRootScopedSessionCookieForHostRootDeployment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	server := newTestServerWithBaseURL(t, "https://train-bot.jolkins.id.lv")
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           77,
			FirstName:    "Alex",
			LanguageCode: "en",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Path != "/" {
		t.Fatalf("unexpected cookie path: %q", cookies[0].Path)
	}
	if !cookies[0].Secure {
		t.Fatalf("expected secure cookie")
	}
	if cookies[0].SameSite != http.SameSiteNoneMode {
		t.Fatalf("unexpected SameSite: %v", cookies[0].SameSite)
	}
}

func TestAuthTelegramPersistsTelegramLanguageForFirstTimeUser(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           707,
			FirstName:    "Alex",
			LanguageCode: "lv",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	settings, err := st.GetUserSettings(context.Background(), auth.User.ID)
	if err != nil {
		t.Fatalf("get user settings: %v", err)
	}
	if settings.Language != domain.LanguageLV {
		t.Fatalf("expected saved language LV, got %q", settings.Language)
	}
	var payload struct {
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth payload: %v", err)
	}
	if payload.Lang != "LV" {
		t.Fatalf("expected auth payload lang LV, got %q", payload.Lang)
	}
}

func TestAuthTelegramKeepsSavedLanguageForExistingUser(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	if err := st.SetLanguage(context.Background(), 808, domain.LanguageLV); err != nil {
		t.Fatalf("set existing language: %v", err)
	}
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           808,
			FirstName:    "Alex",
			LanguageCode: "en",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	settings, err := st.GetUserSettings(context.Background(), auth.User.ID)
	if err != nil {
		t.Fatalf("get user settings: %v", err)
	}
	if settings.Language != domain.LanguageLV {
		t.Fatalf("expected saved language to stay LV, got %q", settings.Language)
	}
	var payload struct {
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth payload: %v", err)
	}
	if payload.Lang != "LV" {
		t.Fatalf("expected auth payload lang LV, got %q", payload.Lang)
	}
}

func TestAuthTelegramFallsBackToTelegramLanguageWhenStoreUnavailable(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           909,
			FirstName:    "Alex",
			LanguageCode: "lv",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth payload: %v", err)
	}
	if payload.Lang != "LV" {
		t.Fatalf("expected auth payload lang LV fallback, got %q", payload.Lang)
	}
}

func TestAuthTelegramIncludesSpacetimeTokenWhenConfigured(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	server := newTestServerWithSpacetime(t, "https://example.test/pixel-stack/train")
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           77,
			FirstName:    "Alex",
			LanguageCode: "en",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, server.cfg.BotToken, auth)})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		StableUserID string `json:"stableUserId"`
		Spacetime    struct {
			Enabled   bool   `json:"enabled"`
			Host      string `json:"host"`
			Database  string `json:"database"`
			Token     string `json:"token"`
			ExpiresAt string `json:"expiresAt"`
			Issuer    string `json:"issuer"`
			Audience  string `json:"audience"`
		} `json:"spacetime"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if payload.StableUserID != "telegram:77" {
		t.Fatalf("unexpected stable user id: %q", payload.StableUserID)
	}
	if !payload.Spacetime.Enabled {
		t.Fatalf("expected spacetime payload to be enabled")
	}
	if payload.Spacetime.Host != "https://stdb.example.test" {
		t.Fatalf("unexpected spacetime host: %q", payload.Spacetime.Host)
	}
	if payload.Spacetime.Database != "train-bot" {
		t.Fatalf("unexpected spacetime database: %q", payload.Spacetime.Database)
	}
	if payload.Spacetime.Issuer != "https://example.test/pixel-stack/train/oidc" {
		t.Fatalf("unexpected issuer: %q", payload.Spacetime.Issuer)
	}
	if payload.Spacetime.Audience != "train-bot-web" {
		t.Fatalf("unexpected audience: %q", payload.Spacetime.Audience)
	}
	expiresAt, err := time.Parse(time.RFC3339, payload.Spacetime.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiresAt: %v", err)
	}
	if !expiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("unexpected expiresAt: got %s want %s", expiresAt, now.Add(24*time.Hour))
	}
	claims := decodeJWTClaims(t, payload.Spacetime.Token)
	if got := claims["iss"]; got != payload.Spacetime.Issuer {
		t.Fatalf("unexpected iss: %#v", got)
	}
	if got := claims["sub"]; got != "telegram:77" {
		t.Fatalf("unexpected sub: %#v", got)
	}
	aud, ok := claims["aud"].([]any)
	if !ok || len(aud) != 1 || aud[0] != "train-bot-web" {
		t.Fatalf("unexpected aud: %#v", claims["aud"])
	}
	if got := claims["telegram_user_id"]; got != "77" {
		t.Fatalf("unexpected telegram_user_id: %#v", got)
	}
	if got := claims["given_name"]; got != "Alex" {
		t.Fatalf("unexpected given_name: %#v", got)
	}
	if err := verifyJWTSignature(server.spacetime.publicKey, payload.Spacetime.Token); err != nil {
		t.Fatalf("verify spacetime token signature: %v", err)
	}
}

func TestServeHTTPExposesSpacetimeOIDCMetadata(t *testing.T) {
	t.Parallel()

	server := newTestServerWithSpacetime(t, "https://example.test/pixel-stack/train")

	discoveryReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/oidc/.well-known/openid-configuration", nil)
	discoveryRes := httptest.NewRecorder()
	server.ServeHTTP(discoveryRes, discoveryReq)
	if discoveryRes.Code != http.StatusOK {
		t.Fatalf("unexpected discovery status: got %d body=%s", discoveryRes.Code, discoveryRes.Body.String())
	}
	var discovery map[string]any
	if err := json.Unmarshal(discoveryRes.Body.Bytes(), &discovery); err != nil {
		t.Fatalf("decode discovery payload: %v", err)
	}
	if got := discovery["issuer"]; got != "https://example.test/pixel-stack/train/oidc" {
		t.Fatalf("unexpected discovery issuer: %#v", got)
	}
	if got := discovery["jwks_uri"]; got != "https://example.test/pixel-stack/train/oidc/jwks.json" {
		t.Fatalf("unexpected discovery jwks_uri: %#v", got)
	}

	jwksReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/oidc/jwks.json", nil)
	jwksRes := httptest.NewRecorder()
	server.ServeHTTP(jwksRes, jwksReq)
	if jwksRes.Code != http.StatusOK {
		t.Fatalf("unexpected jwks status: got %d body=%s", jwksRes.Code, jwksRes.Body.String())
	}
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(jwksRes.Body.Bytes(), &jwks); err != nil {
		t.Fatalf("decode jwks: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected one jwks key, got %+v", jwks.Keys)
	}
	if got := jwks.Keys[0]["kid"]; got != server.spacetime.keyID {
		t.Fatalf("unexpected jwks kid: %#v", got)
	}
}

func TestServeHTTPRejectsAnonymousUserRoute(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/me", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
}

func TestServeHTTPServesRootHostDeploymentRoutes(t *testing.T) {
	t.Parallel()

	server := newTestServerWithBaseURL(t, "https://train-bot.jolkins.id.lv")
	paths := map[string]string{
		"/":                 "public-incidents",
		"/app":              "mini-app",
		"/map":              "public-network-map",
		"/stations":         "public-stations",
		"/departures":       "public-dashboard",
		"/t/demo-train":     "public-train",
		"/t/demo-train/map": "public-map",
	}
	for path, mode := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("unexpected status for %s: got %d body=%s", path, res.Code, res.Body.String())
		}
		assertShellMode(t, path, res.Body.String(), mode)
	}
}

func TestServeHTTPLegacyPathDeploymentRoutesStillWork(t *testing.T) {
	t.Parallel()

	server := newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")
	paths := map[string]string{
		"/pixel-stack/train":                  "public-incidents",
		"/pixel-stack/train/app":              "mini-app",
		"/pixel-stack/train/map":              "public-network-map",
		"/pixel-stack/train/stations":         "public-stations",
		"/pixel-stack/train/departures":       "public-dashboard",
		"/pixel-stack/train/t/demo-train":     "public-train",
		"/pixel-stack/train/t/demo-train/map": "public-map",
	}
	for path, mode := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("unexpected status for %s: got %d body=%s", path, res.Code, res.Body.String())
		}
		assertShellMode(t, path, res.Body.String(), mode)
	}
}

func TestServeHTTPShellAddsReleaseHeadersAndFingerprintedAssets(t *testing.T) {
	t.Parallel()

	server := newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/app", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Commit"); got != server.release.Commit {
		t.Fatalf("unexpected commit header: got %q want %q", got, server.release.Commit)
	}
	if got := res.Header().Get("X-Train-Bot-Build-Time"); got != server.release.BuildTime {
		t.Fatalf("unexpected build time header: got %q want %q", got, server.release.BuildTime)
	}
	if got := res.Header().Get("X-Train-Bot-Instance"); got != server.release.Instance {
		t.Fatalf("unexpected instance header: got %q want %q", got, server.release.Instance)
	}
	if got := res.Header().Get("X-Train-Bot-App-Js"); got != server.release.AppJSHash {
		t.Fatalf("unexpected app.js header: got %q want %q", got, server.release.AppJSHash)
	}
	body := res.Body.String()
	if !strings.Contains(body, "/pixel-stack/train/assets/app.css?v="+server.release.AppCSSHash) {
		t.Fatalf("expected fingerprinted app.css URL, body=%s", body)
	}
	if !strings.Contains(body, "/pixel-stack/train/assets/app.js?v="+server.release.AppJSHash) {
		t.Fatalf("expected fingerprinted app.js URL, body=%s", body)
	}
	if !strings.Contains(body, "/pixel-stack/train/assets/vendor/leaflet.css?v="+server.release.AssetHash("vendor/leaflet.css")) {
		t.Fatalf("expected fingerprinted leaflet.css URL, body=%s", body)
	}
	if !strings.Contains(body, "/pixel-stack/train/assets/vendor/leaflet.js?v="+server.release.AssetHash("vendor/leaflet.js")) {
		t.Fatalf("expected fingerprinted leaflet.js URL, body=%s", body)
	}
}

func TestServeHTTPShellOmitsLegacyModeBootstrapFlags(t *testing.T) {
	t.Parallel()

	server := newTestServerWithSpacetime(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/app", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if strings.Contains(body, "spacetimeDirectOnly") {
		t.Fatalf("did not expect spacetimeDirectOnly bootstrap flag, body=%s", body)
	}
	if strings.Contains(body, "externalTrainMapDirectOnly") {
		t.Fatalf("did not expect externalTrainMapDirectOnly bootstrap flag, body=%s", body)
	}
	if strings.Contains(body, "legacyMirror") {
		t.Fatalf("did not expect legacyMirror bootstrap flag, body=%s", body)
	}
}

func TestServeHTTPShellIncludesPublicEdgeCacheBootstrapFlag(t *testing.T) {
	t.Parallel()

	server := newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")
	server.cfg.TrainWebPublicEdgeCacheEnabled = true
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/departures", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "publicEdgeCacheEnabled: true") {
		t.Fatalf("expected public edge cache bootstrap flag in shell, body=%s", body)
	}
}

func TestServeHTTPAssetCacheHeadersDependOnFingerprint(t *testing.T) {
	t.Parallel()

	server := newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")

	versionedReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/assets/vendor/leaflet.js?v="+server.release.AssetHash("vendor/leaflet.js"), nil)
	versionedRes := httptest.NewRecorder()
	server.ServeHTTP(versionedRes, versionedReq)
	if versionedRes.Code != http.StatusOK {
		t.Fatalf("unexpected versioned asset status: got %d body=%s", versionedRes.Code, versionedRes.Body.String())
	}
	if got := versionedRes.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected immutable cache-control: %q", got)
	}
	if got := versionedRes.Header().Get("X-Train-Bot-App-Js"); got != server.release.AppJSHash {
		t.Fatalf("unexpected app.js hash header: got %q want %q", got, server.release.AppJSHash)
	}

	unversionedReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/assets/vendor/leaflet.js", nil)
	unversionedRes := httptest.NewRecorder()
	server.ServeHTTP(unversionedRes, unversionedReq)
	if unversionedRes.Code != http.StatusOK {
		t.Fatalf("unexpected unversioned asset status: got %d body=%s", unversionedRes.Code, unversionedRes.Body.String())
	}
	if got := unversionedRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected unversioned cache-control: %q", got)
	}
}

func assertShellMode(t *testing.T, path string, body string, want string) {
	t.Helper()

	if !strings.Contains(body, want) {
		t.Fatalf("expected shell for %s to contain mode %q, body=%s", path, want, body)
	}
}

func TestServeHTTPRejectsAnonymousStationSightingSubmission(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/stations/riga/sightings", bytes.NewReader([]byte(`{"destinationStationId":"jelgava"}`)))
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
}

func newTestServer(t *testing.T) *Server {
	return newTestServerWithBaseURL(t, "https://example.test/pixel-stack/train")
}

func newTestServerWithBaseURL(t *testing.T, trainWebPublicBaseURL string) *Server {
	t.Helper()

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}

	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              trainWebPublicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, trainapp.NewService(nil, nil, nil, nil, time.UTC, false), i18n.NewCatalog(), time.UTC)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func newPersistentAuthTestServer(t *testing.T, trainWebPublicBaseURL string, dir string) (*Server, *store.SQLiteStore) {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}
	dbPath := filepath.Join(dir, "train-bot.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              trainWebPublicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, trainapp.NewService(st, nil, nil, nil, loc, false), i18n.NewCatalog(), loc)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.testLogin = &testLoginBroker{
		secret: []byte("0123456789abcdef0123456789abcdef"),
		userID: 7001,
		ttl:    time.Minute,
	}
	return server, st
}

func newTestServerWithSpacetime(t *testing.T, trainWebPublicBaseURL string) *Server {
	t.Helper()

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write session secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}

	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              trainWebPublicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, trainapp.NewService(nil, nil, nil, nil, time.UTC, false), i18n.NewCatalog(), time.UTC)
	if err != nil {
		t.Fatalf("NewServer with Spacetime: %v", err)
	}
	return server
}

func signedInitData(t *testing.T, botToken string, auth telegramAuth) string {
	t.Helper()

	userJSON, err := json.Marshal(auth.User)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}

	values := url.Values{}
	values.Set("auth_date", strconv.FormatInt(auth.AuthDate.Unix(), 10))
	values.Set("query_id", auth.QueryID)
	values.Set("user", string(userJSON))

	dataCheckString := "auth_date=" + values.Get("auth_date") + "\n" +
		"query_id=" + values.Get("query_id") + "\n" +
		"user=" + values.Get("user")
	secretMac := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMac.Write([]byte(botToken))
	secret := secretMac.Sum(nil)
	hash := hmac.New(sha256.New, secret)
	_, _ = hash.Write([]byte(dataCheckString))
	values.Set("hash", hex.EncodeToString(hash.Sum(nil)))
	return values.Encode()
}

func pemEncodePKCS1PrivateKey(t *testing.T) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT format: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal JWT payload: %v", err)
	}
	return claims
}

func verifyJWTSignature(publicKey *rsa.PublicKey, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("unexpected JWT format")
	}
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	return nil
}
