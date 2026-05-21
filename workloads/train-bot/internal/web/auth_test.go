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
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"pixelops/shared/telegramweb"
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
	if got, want := cookie.MaxAge, int((30 * 24 * time.Hour).Seconds()); got != want {
		t.Fatalf("cookie MaxAge = %d, want %d", got, want)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie.SameSite = %v, want Lax", cookie.SameSite)
	}

	claims, err := parseSession([]byte("0123456789abcdef0123456789abcdef"), cookie.Value, now.Add(30*24*time.Hour-time.Second))
	if err != nil {
		t.Fatalf("parseSession: %v", err)
	}
	if claims.UserID != 77 {
		t.Fatalf("unexpected user id: got %d", claims.UserID)
	}
	if claims.Language != "lv" {
		t.Fatalf("unexpected language: got %q", claims.Language)
	}
	if got, want := time.Unix(claims.ExpiresAt, 0).UTC(), now.Add(30*24*time.Hour).UTC(); !got.Equal(want) {
		t.Fatalf("session expiry = %s, want %s", got, want)
	}
	if _, err := parseSession([]byte("0123456789abcdef0123456789abcdef"), cookie.Value, now.Add(30*24*time.Hour+time.Second)); err == nil {
		t.Fatalf("expected session to expire after 30 days")
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
	if payload.Lang != string(domain.DefaultLanguage) {
		t.Fatalf("expected reset language %s, got %q", domain.DefaultLanguage, payload.Lang)
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
	if !settings.AlertsEnabled || settings.AlertStyle != domain.AlertStyleDetailed || settings.Language != domain.DefaultLanguage {
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

func TestAuthTestRouteStaysDisabledOnProductionHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionSecretPath := filepath.Join(dir, "session.secret")
	if err := os.WriteFile(sessionSecretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write session secret: %v", err)
	}
	testSecretPath := filepath.Join(dir, "test-ticket.secret")
	if err := os.WriteFile(testSecretPath, []byte("abcdef0123456789abcdef0123456789"), 0o600); err != nil {
		t.Fatalf("write test ticket secret: %v", err)
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
		TrainWebPublicBaseURL:              "https://vilciens.kontrole.info",
		TrainWebSessionSecretFile:          sessionSecretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebTestLoginEnabled:           true,
		TrainWebTestUserID:                 7001,
		TrainWebTestTicketSecretFile:       testSecretPath,
		TrainWebTestTicketTTLSec:           60,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, trainapp.NewService(nil, nil, nil, nil, time.UTC, false), i18n.NewCatalog(), time.UTC)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/v1/auth/test", strings.NewReader(`{}`))
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s /auth/test status = %d, want 404 body=%s", method, res.Code, res.Body.String())
		}
		if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
			t.Fatalf("%s /auth/test unexpectedly set cookies: %v", method, got)
		}
	}
}

func TestSessionEndpointReturnsAnonymousWithoutCookie(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/session", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("session status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if payload["authenticated"] != false {
		t.Fatalf("authenticated = %#v, want false", payload["authenticated"])
	}
	if _, ok := payload["schedule"].(map[string]any); !ok {
		t.Fatalf("missing schedule payload: %#v", payload["schedule"])
	}
}

func TestSessionEndpointReturnsAuthenticatedSession(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:        7001,
			FirstName: "Session",
			Username:  "session_user",
		},
	}, now)
	if err != nil {
		t.Fatalf("issue session cookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/session", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("session status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if payload["authenticated"] != true {
		t.Fatalf("authenticated = %#v, want true", payload["authenticated"])
	}
	if payload["userId"] != float64(7001) {
		t.Fatalf("userId = %#v, want 7001", payload["userId"])
	}
}

func TestAuthSpacetimeRefreshReturnsFreshTokenForSession(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           7001,
			FirstName:    "Session",
			LanguageCode: "lv",
		},
	}, now)
	if err != nil {
		t.Fatalf("issue session cookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/spacetime", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("spacetime refresh status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("X-Robots-Tag = %q, want noindex, noarchive", got)
	}
	var payload struct {
		OK        bool `json:"ok"`
		Spacetime struct {
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
		t.Fatalf("decode spacetime refresh payload: %v", err)
	}
	if !payload.OK || !payload.Spacetime.Enabled {
		t.Fatalf("spacetime refresh payload not enabled: %+v", payload)
	}
	if payload.Spacetime.Host != "https://stdb.example.test" || payload.Spacetime.Database != "train-bot" {
		t.Fatalf("unexpected spacetime target: %+v", payload.Spacetime)
	}
	claims := decodeJWTClaims(t, payload.Spacetime.Token)
	if got := claims["sub"]; got != "telegram:7001" {
		t.Fatalf("unexpected sub: %#v", got)
	}
	if got := claims["language"]; got != "lv" {
		t.Fatalf("unexpected language: %#v", got)
	}
	if err := verifyJWTSignature(server.spacetime.publicKey, payload.Spacetime.Token); err != nil {
		t.Fatalf("verify spacetime token signature: %v", err)
	}
}

func TestAuthSpacetimeRefreshHidesTokenIssueDetails(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.spacetimeIssueToken = func(telegramAuth, time.Time) (spacetimeIssuedToken, error) {
		return spacetimeIssuedToken{}, fmt.Errorf("sign OIDC token: crypto failure")
	}
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           7001,
			FirstName:    "Session",
			LanguageCode: "lv",
		},
	}, now)
	if err != nil {
		t.Fatalf("issue session cookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/spacetime", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("spacetime refresh status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "failed to issue token") {
		t.Fatalf("body = %q, want generic token issue error", body)
	}
	for _, leaked := range []string{"sign OIDC token", "crypto failure"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("body leaked token issue detail %q: %s", leaked, body)
		}
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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	sessionCookie := cookieByName(res.Result().Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}
	if sessionCookie.Path != "/pixel-stack/train" {
		t.Fatalf("unexpected cookie path: %q", sessionCookie.Path)
	}
	if !sessionCookie.Secure {
		t.Fatalf("expected secure cookie")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected SameSite: %v", sessionCookie.SameSite)
	}
}

func TestAuthTelegramSetsRootScopedSessionCookieForHostRootDeployment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	server := newTestServerWithBaseURL(t, "https://vilciens.kontrole.info")
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	sessionCookie := cookieByName(res.Result().Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}
	if sessionCookie.Path != "/" {
		t.Fatalf("unexpected cookie path: %q", sessionCookie.Path)
	}
	if !sessionCookie.Secure {
		t.Fatalf("expected secure cookie")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected SameSite: %v", sessionCookie.SameSite)
	}
}

func TestLegacyAuthTelegramReturnsGone(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 10, 30, 0, 0, time.UTC)
	server := newTestServerWithBaseURL(t, "https://vilciens.kontrole.info")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram", strings.NewReader(`{"initData":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegram(res, req, now)

	if res.Code != http.StatusGone {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "/api/v1/auth/telegram/complete") {
		t.Fatalf("body missing replacement route: %s", body)
	}
	for _, forbidden := range []string{"invalid Telegram login", "missing hash", "signature", "expired", "initData", "parse"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body should not reach legacy validation detail %q: %s", forbidden, body)
		}
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("X-Robots-Tag = %q, want noindex, noarchive", got)
	}
}

func TestTelegramBrowserAuthLifecycle(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.cfg.BotToken = "123456:telegram-login-secret"
	server.cfg.TrainWebTelegramClientID = ""
	server.cfg.TrainWebTelegramAuthStateTTLSec = 600
	fixture := newTelegramLoginFixture(t, "123456")
	server.telegramLogin = fixture.verifier
	authNow := now.Add(-time.Minute)

	configReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/auth/telegram/config", nil)
	configRes := httptest.NewRecorder()
	server.ServeHTTP(configRes, configReq)
	if configRes.Code != http.StatusOK {
		t.Fatalf("config status: got %d body=%s", configRes.Code, configRes.Body.String())
	}
	var configPayload map[string]any
	if err := json.Unmarshal(configRes.Body.Bytes(), &configPayload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if configPayload["clientId"] != "123456" {
		t.Fatalf("clientId = %#v, want derived bot id", configPayload["clientId"])
	}
	if configPayload["origin"] != "https://example.test" {
		t.Fatalf("origin = %#v", configPayload["origin"])
	}
	if configPayload["redirectUri"] != "https://example.test/pixel-stack/train/" {
		t.Fatalf("redirectUri = %#v", configPayload["redirectUri"])
	}
	nonceCookie := cookieByName(configRes.Result().Cookies(), loginNonceCookieName)
	if nonceCookie == nil {
		t.Fatalf("missing %s cookie", loginNonceCookieName)
	}
	if nonceCookie.Path != "/pixel-stack/train" {
		t.Fatalf("nonce cookie path = %q", nonceCookie.Path)
	}
	loginNonce, err := parseLoginNonce(server.sessionSecret, nonceCookie.Value, now)
	if err != nil {
		t.Fatalf("parse nonce: %v", err)
	}
	if configPayload["nonce"] != loginNonce.Nonce {
		t.Fatalf("nonce = %#v, want %q", configPayload["nonce"], loginNonce.Nonce)
	}

	idToken := fixture.issue(t, map[string]any{
		"iss":                telegramweb.TelegramLoginIssuer,
		"aud":                "123456",
		"sub":                "telegram:777001",
		"iat":                authNow.Unix(),
		"exp":                authNow.Add(5 * time.Minute).Unix(),
		"auth_date":          authNow.Unix(),
		"nonce":              loginNonce.Nonce,
		"id":                 777001,
		"name":               "ViVi Tester",
		"preferred_username": "vivitester",
	})
	body, err := json.Marshal(map[string]string{"idToken": idToken})
	if err != nil {
		t.Fatalf("marshal complete body: %v", err)
	}
	completeReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.AddCookie(nonceCookie)
	completeRes := httptest.NewRecorder()
	server.ServeHTTP(completeRes, completeReq)
	if completeRes.Code != http.StatusOK {
		t.Fatalf("complete status: got %d body=%s", completeRes.Code, completeRes.Body.String())
	}
	sessionCookie := cookieByName(completeRes.Result().Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}
	if cleared := cookieByName(completeRes.Result().Cookies(), loginNonceCookieName); cleared == nil || cleared.Value != "" {
		t.Fatalf("expected cleared nonce cookie, got %#v", cleared)
	}
	var completePayload map[string]any
	if err := json.Unmarshal(completeRes.Body.Bytes(), &completePayload); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if completePayload["authenticated"] != true {
		t.Fatalf("authenticated = %#v", completePayload["authenticated"])
	}

	meReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	meRes := httptest.NewRecorder()
	server.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK {
		t.Fatalf("me status: got %d body=%s", meRes.Code, meRes.Body.String())
	}
	var mePayload map[string]any
	if err := json.Unmarshal(meRes.Body.Bytes(), &mePayload); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if mePayload["authenticated"] != true {
		t.Fatalf("me authenticated = %#v", mePayload["authenticated"])
	}
	if mePayload["stableUserId"] != "telegram:777001" {
		t.Fatalf("stableUserId = %#v", mePayload["stableUserId"])
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRes := httptest.NewRecorder()
	server.ServeHTTP(logoutRes, logoutReq)
	if logoutRes.Code != http.StatusOK {
		t.Fatalf("logout status: got %d body=%s", logoutRes.Code, logoutRes.Body.String())
	}
	if cookie := cookieByName(logoutRes.Result().Cookies(), sessionCookieName); cookie == nil || cookie.Value != "" {
		t.Fatalf("logout session cookie = %#v, want cleared cookie", cookie)
	}
	if cookie := cookieByName(logoutRes.Result().Cookies(), loginNonceCookieName); cookie == nil || cookie.Value != "" {
		t.Fatalf("logout nonce cookie = %#v, want cleared cookie", cookie)
	}
}

func TestTelegramConfigRateLimitsNonceMinting(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.cfg.BotToken = "123456:telegram-login-secret"
	server.cfg.TrainWebTelegramClientID = ""
	server.telegramLogin = newTelegramLoginFixture(t, "123456").verifier

	for index := 0; index < 30; index++ {
		req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/auth/telegram/config", nil)
		req.RemoteAddr = "203.0.113.7:48123"
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("config request %d status = %d, want 200 body=%s", index+1, rec.Code, rec.Body.String())
		}
		if cookie := cookieByName(rec.Result().Cookies(), loginNonceCookieName); cookie == nil {
			t.Fatalf("config request %d did not mint nonce cookie", index+1)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/auth/telegram/config", nil)
	req.RemoteAddr = "203.0.113.7:48123"
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited config status = %d, want 429 body=%s", rec.Code, rec.Body.String())
	}
	if cookie := cookieByName(rec.Result().Cookies(), loginNonceCookieName); cookie != nil {
		t.Fatalf("limited config minted nonce cookie: %#v", cookie)
	}
}

func TestTelegramCompleteRateLimitsInvalidAttempts(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.cfg.BotToken = "123456:telegram-login-secret"
	server.cfg.TrainWebTelegramClientID = ""
	server.telegramLogin = newTelegramLoginFixture(t, "123456").verifier

	for index := 0; index < 15; index++ {
		req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.8:48123"
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("complete request %d status = %d, want 401 body=%s", index+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.8:48123"
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited complete status = %d, want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func TestTelegramCompleteRateLimitIgnoresSpoofedForwardedHeaders(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.cfg.BotToken = "123456:telegram-login-secret"
	server.cfg.TrainWebTelegramClientID = ""
	server.telegramLogin = newTelegramLoginFixture(t, "123456").verifier

	for index := 0; index < 15; index++ {
		req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.9:48123"
		req.Header.Set("CF-Connecting-IP", "198.51.100."+strconv.Itoa(index+1))
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("complete request %d status = %d, want 401 body=%s", index+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.9:48123"
	req.Header.Set("CF-Connecting-IP", "198.51.100.200")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited complete status = %d, want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func TestTelegramCompleteRateLimitIgnoresSpoofedForwardedHeadersFromPrivatePeer(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	server.cfg.BotToken = "123456:telegram-login-secret"
	server.cfg.TrainWebTelegramClientID = ""
	server.telegramLogin = newTelegramLoginFixture(t, "123456").verifier

	for index := 0; index < 15; index++ {
		req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.5:48123"
		req.Header.Set("X-Real-IP", "198.51.100."+strconv.Itoa(index+1))
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(index+101))
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("complete request %d status = %d, want 401 body=%s", index+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.5:48123"
	req.Header.Set("X-Real-IP", "198.51.100.200")
	req.Header.Set("X-Forwarded-For", "198.51.100.201")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited complete status = %d, want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func TestLegacyAuthTelegramDoesNotReachValidationOrRateLimit(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	for index := 0; index < 3; index++ {
		req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram", strings.NewReader(`{"initData":"invalid"}`))
		req.RemoteAddr = "203.0.113.10:48123"
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusGone {
			t.Fatalf("legacy auth request %d status = %d, want 410 body=%s", index+1, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "invalid Telegram login") {
			t.Fatalf("legacy auth request %d reached auth validation: %s", index+1, rec.Body.String())
		}
	}
}

func TestUnsafeAPIRejectsCrossSiteBrowserRequests(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "foreign origin",
			headers: map[string]string{
				"Origin": "https://evil.example",
			},
		},
		{
			name: "cross-site fetch metadata",
			headers: map[string]string{
				"Sec-Fetch-Site": "cross-site",
			},
		},
		{
			name: "sibling origin",
			headers: map[string]string{
				"Origin": "https://kontrole.info",
			},
		},
		{
			name: "same-site fetch metadata",
			headers: map[string]string{
				"Sec-Fetch-Site": "same-site",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/logout", nil)
			for key, value := range tc.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/logout", nil)
	req.Header.Set("Origin", "https://example.test")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthUnsupportedMethodsReturnAllowHeader(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	for _, tc := range []struct {
		method string
		path   string
		allow  string
	}{
		{method: http.MethodOptions, path: "/pixel-stack/train/api/v1/auth/telegram/config", allow: "GET"},
		{method: http.MethodGet, path: "/pixel-stack/train/api/v1/auth/telegram/complete", allow: "POST"},
		{method: http.MethodOptions, path: "/pixel-stack/train/api/v1/auth/telegram/complete", allow: "POST"},
		{method: http.MethodGet, path: "/pixel-stack/train/api/v1/auth/telegram", allow: "POST"},
		{method: http.MethodOptions, path: "/pixel-stack/train/api/v1/auth/telegram", allow: "POST"},
		{method: http.MethodGet, path: "/pixel-stack/train/api/v1/auth/spacetime", allow: "POST"},
		{method: http.MethodOptions, path: "/pixel-stack/train/api/v1/auth/spacetime", allow: "POST"},
		{method: http.MethodGet, path: "/pixel-stack/train/api/v1/auth/logout", allow: "POST"},
		{method: http.MethodOptions, path: "/pixel-stack/train/api/v1/auth/logout", allow: "POST"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status = %d, want 405 body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Allow"); got != tc.allow {
			t.Fatalf("%s %s Allow = %q, want %q", tc.method, tc.path, got, tc.allow)
		}
	}
}

func TestProtectedAPIUnsupportedMethodsReturnAllowBeforeAuth(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodOptions, "/pixel-stack/train/api/v1/me", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow = %q, want GET", got)
	}
	if strings.Contains(rec.Body.String(), "session") {
		t.Fatalf("unsupported method reached session handling: %s", rec.Body.String())
	}
}

func TestTelegramCompleteRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	body := `{"idToken":"` + strings.Repeat("a", 70*1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestTelegramCompleteRejectsNonJSONContentType(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"invalid"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("X-Robots-Tag = %q, want noindex, noarchive", got)
	}
}

func TestProtectedAPIReturnsGenericInvalidSession(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/stations", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "header.%%%%.signature"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid session") {
		t.Fatalf("body = %q, want generic invalid session", body)
	}
	if strings.Contains(body, "base64") || strings.Contains(body, "decode") {
		t.Fatalf("body leaked parser detail: %q", body)
	}
}

func TestTelegramCompleteRejectsLegacyWidgetAuthResult(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	botToken := "123456:telegram-widget-secret"
	server.cfg.BotToken = botToken
	authNow := now.Add(-time.Minute)
	values := url.Values{
		"id":         {"777002"},
		"first_name": {"ViVi Tester"},
		"username":   {"vivitester"},
		"photo_url":  {"https://t.me/i/userpic/320/test.jpg"},
		"auth_date":  {strconv.FormatInt(authNow.Unix(), 10)},
	}
	values.Set("hash", telegramWidgetHashForTest(t, values, botToken))
	body, err := json.Marshal(map[string]any{
		"widgetAuth": map[string]any{
			"id":         777002,
			"first_name": values.Get("first_name"),
			"username":   values.Get("username"),
			"photo_url":  values.Get("photo_url"),
			"auth_date":  authNow.Unix(),
			"hash":       values.Get("hash"),
		},
	})
	if err != nil {
		t.Fatalf("marshal widget body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("complete status: got %d body=%s", res.Code, res.Body.String())
	}
	if cookieByName(res.Result().Cookies(), sessionCookieName) != nil {
		t.Fatalf("legacy widget payload unexpectedly created a session")
	}
}

func TestTelegramCompleteAcceptsMiniAppInitData(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	botToken := "123456:telegram-mini-secret"
	server.cfg.BotToken = botToken
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-time.Minute),
		User: telegramUser{
			ID:           777003,
			FirstName:    "ViVi Tester",
			Username:     "vivitester",
			PhotoURL:     "https://t.me/i/userpic/320/test.jpg",
			LanguageCode: "lv",
		},
	}
	body, err := json.Marshal(map[string]string{"initData": signedInitData(t, botToken, auth)})
	if err != nil {
		t.Fatalf("marshal initData body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("complete status: got %d body=%s", res.Code, res.Body.String())
	}
	if cookieByName(res.Result().Cookies(), sessionCookieName) == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/train/api/v1/auth/telegram/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.handleAuthTelegramComplete(res, req, now)

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
	discoveryHeadReq := httptest.NewRequest(http.MethodHead, "/pixel-stack/train/oidc/.well-known/openid-configuration", nil)
	discoveryHeadRes := httptest.NewRecorder()
	server.ServeHTTP(discoveryHeadRes, discoveryHeadReq)
	if discoveryHeadRes.Code != http.StatusOK {
		t.Fatalf("unexpected discovery HEAD status: got %d body=%s", discoveryHeadRes.Code, discoveryHeadRes.Body.String())
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
	jwksHeadReq := httptest.NewRequest(http.MethodHead, "/pixel-stack/train/oidc/jwks.json", nil)
	jwksHeadRes := httptest.NewRecorder()
	server.ServeHTTP(jwksHeadRes, jwksHeadReq)
	if jwksHeadRes.Code != http.StatusOK {
		t.Fatalf("unexpected jwks HEAD status: got %d body=%s", jwksHeadRes.Code, jwksHeadRes.Body.String())
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

	server := newTestServerWithBaseURL(t, "https://vilciens.kontrole.info")
	paths := map[string]string{
		"/":                 "public-network-map",
		"/app":              "mini-app",
		"/map":              "public-network-map",
		"/events":           "public-incidents",
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
		"/pixel-stack/train":                  "public-network-map",
		"/pixel-stack/train/app":              "mini-app",
		"/pixel-stack/train/map":              "public-network-map",
		"/pixel-stack/train/events":           "public-incidents",
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

func TestServeHTTPShellAddsSecurityHeadersAndFingerprintedAssets(t *testing.T) {
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
	for _, header := range []string{
		"Strict-Transport-Security",
		"Content-Security-Policy",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Permissions-Policy",
	} {
		if got := res.Header().Get(header); got == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
	csp := res.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("Content-Security-Policy still allows inline scripts: %q", csp)
	}
	if strings.Contains(csp, "'unsafe-inline'") {
		t.Fatalf("Content-Security-Policy still allows inline code: %q", csp)
	}
	if !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("Content-Security-Policy missing strict style-src: %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Fatalf("Content-Security-Policy missing script nonce: %q", csp)
	}
	for _, header := range []string{"X-Train-Bot-Commit", "X-Train-Bot-Build-Time", "X-Train-Bot-Instance", "X-Train-Bot-App-Js"} {
		if got := res.Header().Get(header); got != "" {
			t.Fatalf("unexpected public debug header %s=%q", header, got)
		}
	}
	body := res.Body.String()
	if !strings.Contains(body, "/pixel-stack/train/assets/app.css?v="+server.release.AppCSSHash) {
		t.Fatalf("expected fingerprinted app.css URL, body=%s", body)
	}
	if !strings.Contains(body, `nonce="`) {
		t.Fatalf("shell missing script nonce: %s", body)
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
	if got := versionedRes.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("unexpected immutable asset Vary: %q", got)
	}
	if got := versionedRes.Header().Get("X-Train-Bot-App-Js"); got != "" {
		t.Fatalf("unexpected app.js hash header: %q", got)
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
	if got := unversionedRes.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("unexpected unversioned asset Vary: %q", got)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/assets/vendor/leaflet.js", nil)
	rangeReq.Header.Set("Range", "bytes=0-63")
	rangeRes := httptest.NewRecorder()
	server.ServeHTTP(rangeRes, rangeReq)
	if rangeRes.Code != http.StatusOK {
		t.Fatalf("unexpected unversioned range asset status: got %d body=%s", rangeRes.Code, rangeRes.Body.String())
	}
	if got := rangeRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected unversioned range cache-control: %q", got)
	}
	if got := rangeRes.Header().Get("Content-Range"); got != "" {
		t.Fatalf("unexpected unversioned range Content-Range: %q", got)
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

func telegramWidgetHashForTest(t *testing.T, values url.Values, botToken string) string {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.EqualFold(key, "hash") {
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
	return hex.EncodeToString(authHMACSHA256(secret[:], []byte(strings.Join(lines, "\n"))))
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie
		}
	}
	return nil
}

type telegramLoginFixture struct {
	verifier *telegramweb.LoginVerifier
	key      *rsa.PrivateKey
	keyID    string
}

func newTelegramLoginFixture(t *testing.T, clientID string) telegramLoginFixture {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID := "telegram-login-test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"use": "sig",
					"alg": "RS256",
					"kid": keyID,
					"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
				},
			},
		})
	}))
	t.Cleanup(jwksServer.Close)
	verifier, err := telegramweb.NewLoginVerifier(telegramweb.LoginVerifierConfig{
		ClientID: clientID,
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewLoginVerifier: %v", err)
	}
	return telegramLoginFixture{
		verifier: verifier,
		key:      privateKey,
		keyID:    keyID,
	}
}

func (f telegramLoginFixture) issue(t *testing.T, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{
		"typ": "JWT",
		"alg": "RS256",
		"kid": f.keyID,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
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
