package web

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
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
	"satiksmebot/internal/config"
	"satiksmebot/internal/model"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/store"
)

func TestTelegramAuthLifecycle(t *testing.T) {
	serverNow := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	authNow := time.Now().UTC()
	server, baseURL := newTelegramAuthTestServer(t, serverNow)
	fixture := newTelegramLoginFixture(t, server.cfg.SatiksmeWebTelegramClientID)
	server.telegramLogin = fixture.verifier
	httpClient := noRedirectHTTPClient()

	anonymousMeResp, err := http.Get(baseURL + "/api/v1/me")
	if err != nil {
		t.Fatalf("GET anonymous /me error = %v", err)
	}
	defer anonymousMeResp.Body.Close()
	if anonymousMeResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /me status = %d, want 401", anonymousMeResp.StatusCode)
	}

	configReq, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/auth/telegram/config", nil)
	if err != nil {
		t.Fatalf("NewRequest(config) error = %v", err)
	}
	configResp, err := httpClient.Do(configReq)
	if err != nil {
		t.Fatalf("Do(config) error = %v", err)
	}
	defer configResp.Body.Close()
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d, want 200", configResp.StatusCode)
	}
	var configPayload map[string]any
	if err := json.NewDecoder(configResp.Body).Decode(&configPayload); err != nil {
		t.Fatalf("Decode(config) error = %v", err)
	}
	if configPayload["clientId"] != server.cfg.SatiksmeWebTelegramClientID {
		t.Fatalf("config clientId = %#v", configPayload["clientId"])
	}
	if configPayload["origin"] != "https://kontrole.info" {
		t.Fatalf("config origin = %#v, want https://kontrole.info", configPayload["origin"])
	}
	if configPayload["redirectUri"] != "https://kontrole.info/" {
		t.Fatalf("config redirectUri = %#v, want https://kontrole.info/", configPayload["redirectUri"])
	}
	nonceCookie := cookieByName(configResp.Cookies(), loginNonceCookieName)
	if nonceCookie == nil {
		t.Fatalf("missing %s cookie", loginNonceCookieName)
	}
	loginNonce, err := parseLoginNonce(server.sessionSecret, nonceCookie.Value, authNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("parseLoginNonce() error = %v", err)
	}
	if configPayload["nonce"] != loginNonce.Nonce {
		t.Fatalf("config nonce = %#v, want %q", configPayload["nonce"], loginNonce.Nonce)
	}
	if scopes, ok := configPayload["scopes"].([]any); !ok || len(scopes) != 1 || scopes[0] != "profile" {
		t.Fatalf("config scopes = %#v", configPayload["scopes"])
	}

	token := fixture.issue(t, map[string]any{
		"iss":                telegramweb.TelegramLoginIssuer,
		"aud":                server.cfg.SatiksmeWebTelegramClientID,
		"sub":                "telegram:777001",
		"iat":                authNow.Unix(),
		"exp":                authNow.Add(5 * time.Minute).Unix(),
		"auth_date":          authNow.Unix(),
		"nonce":              loginNonce.Nonce,
		"id":                 777001,
		"name":               "Kontrole Tester",
		"preferred_username": "kontroletester",
	})
	completeReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(`{
		"idToken":"`+token+`"
	}`))
	if err != nil {
		t.Fatalf("NewRequest(complete) error = %v", err)
	}
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.AddCookie(nonceCookie)
	completeResp, err := httpClient.Do(completeReq)
	if err != nil {
		t.Fatalf("Do(complete) error = %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, want 200", completeResp.StatusCode)
	}
	sessionCookie := cookieByName(completeResp.Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}
	clearedNonceCookie := cookieByName(completeResp.Cookies(), loginNonceCookieName)
	if clearedNonceCookie == nil || clearedNonceCookie.Value != "" {
		t.Fatalf("expected cleared login nonce cookie, got %#v", clearedNonceCookie)
	}
	var completePayload map[string]any
	if err := json.NewDecoder(completeResp.Body).Decode(&completePayload); err != nil {
		t.Fatalf("Decode(complete) error = %v", err)
	}
	if completePayload["authenticated"] != true {
		t.Fatalf("complete authenticated = %#v, want true", completePayload["authenticated"])
	}

	meReq, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("NewRequest(me) error = %v", err)
	}
	meReq.AddCookie(sessionCookie)
	meResp, err := httpClient.Do(meReq)
	if err != nil {
		t.Fatalf("Do(me) error = %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", meResp.StatusCode)
	}
	var mePayload map[string]any
	if err := json.NewDecoder(meResp.Body).Decode(&mePayload); err != nil {
		t.Fatalf("Decode(me) error = %v", err)
	}
	if mePayload["authenticated"] != true {
		t.Fatalf("me authenticated = %#v, want true", mePayload["authenticated"])
	}
	if mePayload["userId"] != float64(777001) {
		t.Fatalf("me userId = %#v, want 777001", mePayload["userId"])
	}
	if mePayload["stableUserId"] != "telegram:777001" {
		t.Fatalf("me stableUserId = %#v", mePayload["stableUserId"])
	}
	if _, ok := mePayload["spacetime"]; ok {
		t.Fatalf("me payload unexpectedly exposed spacetime browser auth: %#v", mePayload["spacetime"])
	}

	for _, path := range []string{"/api/v1/auth/telegram/start", "/api/v1/auth/telegram/callback"} {
		resp, err := httpClient.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("%s status = %d, want 410", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	logoutReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("NewRequest(logout) error = %v", err)
	}
	logoutReq.AddCookie(sessionCookie)
	logoutResp, err := httpClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("Do(logout) error = %v", err)
	}
	defer logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutResp.StatusCode)
	}
	if cookie := cookieByName(logoutResp.Cookies(), sessionCookieName); cookie == nil || cookie.Value != "" {
		t.Fatalf("logout session cookie = %#v, want cleared cookie", cookie)
	}
	if cookie := cookieByName(logoutResp.Cookies(), loginStateCookieName); cookie == nil || cookie.Value != "" {
		t.Fatalf("logout state cookie = %#v, want cleared cookie", cookie)
	}
	if cookie := cookieByName(logoutResp.Cookies(), loginNonceCookieName); cookie == nil || cookie.Value != "" {
		t.Fatalf("logout nonce cookie = %#v, want cleared cookie", cookie)
	}
}

func TestTelegramCompleteRejectsInvalidIDTokenClaims(t *testing.T) {
	serverNow := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	authNow := time.Now().UTC()
	testCases := []struct {
		name          string
		mutateClaims  func(map[string]any)
		wantSubstring string
	}{
		{
			name: "bad issuer",
			mutateClaims: func(claims map[string]any) {
				claims["iss"] = "https://example.com"
			},
			wantSubstring: "issuer",
		},
		{
			name: "bad audience",
			mutateClaims: func(claims map[string]any) {
				claims["aud"] = "987654321"
			},
			wantSubstring: "audience",
		},
		{
			name: "expired",
			mutateClaims: func(claims map[string]any) {
				claims["exp"] = authNow.Add(-time.Minute).Unix()
			},
			wantSubstring: "expired",
		},
		{
			name: "missing id",
			mutateClaims: func(claims map[string]any) {
				delete(claims, "id")
			},
			wantSubstring: "id",
		},
		{
			name: "nonce mismatch",
			mutateClaims: func(claims map[string]any) {
				claims["nonce"] = "different-nonce"
			},
			wantSubstring: "nonce",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, baseURL := newTelegramAuthTestServer(t, serverNow)
			fixture := newTelegramLoginFixture(t, server.cfg.SatiksmeWebTelegramClientID)
			server.telegramLogin = fixture.verifier
			httpClient := noRedirectHTTPClient()

			configResp, err := httpClient.Get(baseURL + "/api/v1/auth/telegram/config")
			if err != nil {
				t.Fatalf("GET config error = %v", err)
			}
			defer configResp.Body.Close()
			if configResp.StatusCode != http.StatusOK {
				t.Fatalf("config status = %d, want 200", configResp.StatusCode)
			}
			nonceCookie := cookieByName(configResp.Cookies(), loginNonceCookieName)
			if nonceCookie == nil {
				t.Fatalf("missing %s cookie", loginNonceCookieName)
			}
			loginNonce, err := parseLoginNonce(server.sessionSecret, nonceCookie.Value, authNow.Add(time.Minute))
			if err != nil {
				t.Fatalf("parseLoginNonce() error = %v", err)
			}

			claims := map[string]any{
				"iss":       telegramweb.TelegramLoginIssuer,
				"aud":       server.cfg.SatiksmeWebTelegramClientID,
				"sub":       "telegram:777001",
				"iat":       authNow.Unix(),
				"exp":       authNow.Add(5 * time.Minute).Unix(),
				"auth_date": authNow.Unix(),
				"nonce":     loginNonce.Nonce,
				"id":        777001,
				"name":      "Kontrole Tester",
			}
			tc.mutateClaims(claims)
			token := fixture.issue(t, claims)

			req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(`{"idToken":"`+token+`"}`))
			if err != nil {
				t.Fatalf("NewRequest(complete) error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(nonceCookie)
			resp, err := httpClient.Do(req)
			if err != nil {
				t.Fatalf("Do(complete) error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("complete status = %d, want 401", resp.StatusCode)
			}
			body := mustReadBody(t, resp)
			if !strings.Contains(body, tc.wantSubstring) {
				t.Fatalf("complete error = %q, want substring %q", body, tc.wantSubstring)
			}
		})
	}
}

func TestTelegramCompleteAcceptsWidgetAuthResult(t *testing.T) {
	serverNow := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	authNow := time.Now().UTC()
	botToken := "123456:telegram-widget-secret"
	_, baseURL := newTelegramAuthTestServer(t, serverNow, func(cfg *config.Config) {
		cfg.BotToken = botToken
	})
	httpClient := noRedirectHTTPClient()

	values := url.Values{
		"id":         {"777001"},
		"first_name": {"Kontrole Tester"},
		"username":   {"kontroletester"},
		"photo_url":  {"https://t.me/i/userpic/320/test.jpg"},
		"auth_date":  {strconv.FormatInt(authNow.Unix(), 10)},
	}
	values.Set("hash", telegramWidgetHashForTest(t, values, botToken))
	body := map[string]any{
		"widgetAuth": map[string]any{
			"id":         777001,
			"first_name": values.Get("first_name"),
			"username":   values.Get("username"),
			"photo_url":  values.Get("photo_url"),
			"auth_date":  authNow.Unix(),
			"hash":       values.Get("hash"),
		},
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal(body) error = %v", err)
	}

	completeReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(string(bodyJSON)))
	if err != nil {
		t.Fatalf("NewRequest(complete) error = %v", err)
	}
	completeReq.Header.Set("Content-Type", "application/json")
	completeResp, err := httpClient.Do(completeReq)
	if err != nil {
		t.Fatalf("Do(complete) error = %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body = %s", completeResp.StatusCode, mustReadBody(t, completeResp))
	}
	sessionCookie := cookieByName(completeResp.Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}

	meReq, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("NewRequest(me) error = %v", err)
	}
	meReq.AddCookie(sessionCookie)
	meResp, err := httpClient.Do(meReq)
	if err != nil {
		t.Fatalf("Do(me) error = %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", meResp.StatusCode)
	}
	var mePayload map[string]any
	if err := json.NewDecoder(meResp.Body).Decode(&mePayload); err != nil {
		t.Fatalf("Decode(me) error = %v", err)
	}
	if mePayload["authenticated"] != true {
		t.Fatalf("me authenticated = %#v, want true", mePayload["authenticated"])
	}
	if mePayload["stableUserId"] != "telegram:777001" {
		t.Fatalf("me stableUserId = %#v", mePayload["stableUserId"])
	}
}

func TestTelegramCompleteAcceptsMiniAppInitData(t *testing.T) {
	serverNow := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	authNow := time.Now().UTC()
	botToken := "123456:telegram-mini-secret"
	_, baseURL := newTelegramAuthTestServer(t, serverNow, func(cfg *config.Config) {
		cfg.BotToken = botToken
	})
	httpClient := noRedirectHTTPClient()

	userJSON, err := json.Marshal(map[string]any{
		"id":            777001,
		"first_name":    "Kontrole Tester",
		"username":      "kontroletester",
		"photo_url":     "https://t.me/i/userpic/320/test.jpg",
		"language_code": "lv",
	})
	if err != nil {
		t.Fatalf("Marshal(user) error = %v", err)
	}
	values := url.Values{
		"query_id":  {"AAEAAAE"},
		"auth_date": {strconv.FormatInt(authNow.Unix(), 10)},
		"user":      {string(userJSON)},
	}
	initData := telegramInitDataForTest(t, values, botToken)
	bodyJSON, err := json.Marshal(map[string]any{"initData": initData})
	if err != nil {
		t.Fatalf("Marshal(body) error = %v", err)
	}

	completeReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(string(bodyJSON)))
	if err != nil {
		t.Fatalf("NewRequest(complete) error = %v", err)
	}
	completeReq.Header.Set("Content-Type", "application/json")
	completeResp, err := httpClient.Do(completeReq)
	if err != nil {
		t.Fatalf("Do(complete) error = %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body = %s", completeResp.StatusCode, mustReadBody(t, completeResp))
	}
	sessionCookie := cookieByName(completeResp.Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}

	meReq, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("NewRequest(me) error = %v", err)
	}
	meReq.AddCookie(sessionCookie)
	meResp, err := httpClient.Do(meReq)
	if err != nil {
		t.Fatalf("Do(me) error = %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", meResp.StatusCode)
	}
	var mePayload map[string]any
	if err := json.NewDecoder(meResp.Body).Decode(&mePayload); err != nil {
		t.Fatalf("Decode(me) error = %v", err)
	}
	if mePayload["authenticated"] != true {
		t.Fatalf("me authenticated = %#v, want true", mePayload["authenticated"])
	}
	if mePayload["stableUserId"] != "telegram:777001" {
		t.Fatalf("me stableUserId = %#v", mePayload["stableUserId"])
	}
}

func TestTelegramCompleteRejectsInvalidMiniAppInitData(t *testing.T) {
	serverNow := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	authNow := time.Now().UTC()
	botToken := "123456:telegram-mini-secret"
	_, baseURL := newTelegramAuthTestServer(t, serverNow, func(cfg *config.Config) {
		cfg.BotToken = botToken
	})
	httpClient := noRedirectHTTPClient()

	testCases := []struct {
		name       string
		values     url.Values
		mutateData func(string) string
		want       string
	}{
		{
			name: "bad signature",
			values: url.Values{
				"auth_date": {strconv.FormatInt(authNow.Unix(), 10)},
				"user":      {`{"id":777001,"first_name":"Kontrole Tester"}`},
			},
			mutateData: func(initData string) string {
				values, err := url.ParseQuery(initData)
				if err != nil {
					return initData
				}
				values.Set("hash", strings.Repeat("0", 64))
				return values.Encode()
			},
			want: "signature",
		},
		{
			name: "expired",
			values: url.Values{
				"auth_date": {strconv.FormatInt(authNow.Add(-10*time.Minute).Unix(), 10)},
				"user":      {`{"id":777001,"first_name":"Kontrole Tester"}`},
			},
			want: "expired",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initData := telegramInitDataForTest(t, tc.values, botToken)
			if tc.mutateData != nil {
				initData = tc.mutateData(initData)
			}
			bodyJSON, err := json.Marshal(map[string]any{"initData": initData})
			if err != nil {
				t.Fatalf("Marshal(body) error = %v", err)
			}
			completeReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(string(bodyJSON)))
			if err != nil {
				t.Fatalf("NewRequest(complete) error = %v", err)
			}
			completeReq.Header.Set("Content-Type", "application/json")
			completeResp, err := httpClient.Do(completeReq)
			if err != nil {
				t.Fatalf("Do(complete) error = %v", err)
			}
			defer completeResp.Body.Close()
			if completeResp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("complete status = %d, want 401", completeResp.StatusCode)
			}
			body := mustReadBody(t, completeResp)
			if !strings.Contains(body, tc.want) {
				t.Fatalf("complete error = %q, want substring %q", body, tc.want)
			}
		})
	}
}

func TestAppRouteUsesPublicWebsiteShell(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	server, _ := newTelegramAuthTestServer(t, now)

	for _, path := range []string{"/", "/app"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<title>Kontrole</title>") {
			t.Fatalf("%s body missing updated title: %s", path, body)
		}
		if !strings.Contains(body, "https://telegram.org/js/telegram-web-app.js") {
			t.Fatalf("%s body missing Telegram Web App script: %s", path, body)
		}
		if !strings.Contains(body, `"mode":"public"`) {
			t.Fatalf("%s body missing public mode: %s", path, body)
		}
		if strings.Contains(body, `"spacetimeEnabled"`) || strings.Contains(body, `"spacetimeDirectOnly"`) {
			t.Fatalf("%s body unexpectedly exposes browser-direct Spacetime config", path)
		}
	}
}

func TestSpacetimeOIDCRoutesServeMetadata(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	server, _ := newTelegramAuthTestServer(t, now, func(cfg *config.Config) {
		cfg.SatiksmeWebSpacetimeEnabled = true
	})

	configRec := httptest.NewRecorder()
	server.ServeHTTP(configRec, httptest.NewRequest(http.MethodGet, "/oidc/.well-known/openid-configuration", nil))
	if configRec.Code != http.StatusOK {
		t.Fatalf("openid configuration status = %d, want 200", configRec.Code)
	}
	var openIDConfiguration map[string]any
	if err := json.Unmarshal(configRec.Body.Bytes(), &openIDConfiguration); err != nil {
		t.Fatalf("Unmarshal(openid configuration) error = %v", err)
	}
	if openIDConfiguration["issuer"] != "https://kontrole.info/oidc" {
		t.Fatalf("issuer = %#v, want https://kontrole.info/oidc", openIDConfiguration["issuer"])
	}
	if openIDConfiguration["jwks_uri"] != "https://kontrole.info/oidc/jwks.json" {
		t.Fatalf("jwks_uri = %#v, want https://kontrole.info/oidc/jwks.json", openIDConfiguration["jwks_uri"])
	}

	jwksRec := httptest.NewRecorder()
	server.ServeHTTP(jwksRec, httptest.NewRequest(http.MethodGet, "/oidc/jwks.json", nil))
	if jwksRec.Code != http.StatusOK {
		t.Fatalf("jwks status = %d, want 200", jwksRec.Code)
	}
	var jwks map[string]any
	if err := json.Unmarshal(jwksRec.Body.Bytes(), &jwks); err != nil {
		t.Fatalf("Unmarshal(jwks) error = %v", err)
	}
	keys, ok := jwks["keys"].([]any)
	if !ok || len(keys) == 0 {
		t.Fatalf("jwks keys = %#v, want non-empty array", jwks["keys"])
	}
}

func newTelegramAuthTestServer(t *testing.T, now time.Time, mutators ...func(*config.Config)) (*Server, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "spacetime.key")
	if err := writeTestRSAKey(keyPath); err != nil {
		t.Fatalf("WriteFile(spacetime.key) error = %v", err)
	}

	catalog := &model.Catalog{
		GeneratedAt: now.Add(-10 * time.Minute),
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12}},
	}
	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("Marshal(catalog) error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog:     catalog,
		status:      runtime.CatalogStatus{Loaded: true, GeneratedAt: catalog.GeneratedAt, StopCount: len(catalog.Stops)},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now.Add(-time.Hour), true, "127.0.0.1:9318")
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                              "bot-token",
		SatiksmeWebEnabled:                    true,
		SatiksmeWebBindAddr:                   "127.0.0.1",
		SatiksmeWebPort:                       9318,
		SatiksmeWebPublicBaseURL:              "https://kontrole.info",
		SatiksmeWebSessionSecretFile:          secretPath,
		SatiksmeWebTelegramClientID:           "123456789",
		SatiksmeWebTelegramBotUsername:        "kontrolebot",
		SatiksmeWebTelegramAuthMaxAgeSec:      300,
		SatiksmeWebTelegramAuthStateTTLSec:    600,
		SatiksmeWebSpacetimeHost:              "https://maincloud.spacetimedb.com",
		SatiksmeWebSpacetimeDatabase:          "db123",
		SatiksmeWebSpacetimeOIDCIssuer:        "https://kontrole.info/oidc",
		SatiksmeWebSpacetimeOIDCAudience:      "satiksme-bot-web",
		SatiksmeWebSpacetimeJWTPrivateKeyFile: keyPath,
		SatiksmeWebSpacetimeTokenTTLSec:       86400,
	}
	for _, mutate := range mutators {
		mutate(&cfg)
	}

	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return server, ts.URL
}

func authenticateTestSession(t *testing.T, server *Server, baseURL string, userID int64, now time.Time) *http.Cookie {
	t.Helper()
	httpClient := noRedirectHTTPClient()
	fixture := newTelegramLoginFixture(t, server.cfg.SatiksmeWebTelegramClientID)
	server.telegramLogin = fixture.verifier

	configResp, err := httpClient.Get(baseURL + "/api/v1/auth/telegram/config")
	if err != nil {
		t.Fatalf("GET(config) error = %v", err)
	}
	defer configResp.Body.Close()
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d, want 200", configResp.StatusCode)
	}
	nonceCookie := cookieByName(configResp.Cookies(), loginNonceCookieName)
	if nonceCookie == nil {
		t.Fatalf("missing %s cookie", loginNonceCookieName)
	}
	loginNonce, err := parseLoginNonce(server.sessionSecret, nonceCookie.Value, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("parseLoginNonce() error = %v", err)
	}
	token := fixture.issue(t, map[string]any{
		"iss":       telegramweb.TelegramLoginIssuer,
		"aud":       server.cfg.SatiksmeWebTelegramClientID,
		"sub":       "telegram:" + strconv.FormatInt(userID, 10),
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     loginNonce.Nonce,
		"id":        userID,
		"name":      "Kontrole",
	})
	completeReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/telegram/complete", strings.NewReader(`{
		"idToken":"`+token+`"
	}`))
	if err != nil {
		t.Fatalf("NewRequest(complete) error = %v", err)
	}
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.AddCookie(nonceCookie)
	completeResp, err := httpClient.Do(completeReq)
	if err != nil {
		t.Fatalf("Do(complete) error = %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, want 200", completeResp.StatusCode)
	}
	sessionCookie := cookieByName(completeResp.Cookies(), sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("missing %s cookie", sessionCookieName)
	}
	return sessionCookie
}

func strconvFormat(value int64) string {
	return strconv.FormatInt(value, 10)
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

func telegramInitDataForTest(t *testing.T, values url.Values, botToken string) string {
	t.Helper()
	copied := url.Values{}
	for key, entries := range values {
		for _, entry := range entries {
			copied.Add(key, entry)
		}
	}
	keys := make([]string, 0, len(copied))
	for key := range copied {
		if strings.EqualFold(key, "hash") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+copied.Get(key))
	}
	secret := authHMACSHA256([]byte("WebAppData"), []byte(botToken))
	copied.Set("hash", hex.EncodeToString(authHMACSHA256(secret, []byte(strings.Join(lines, "\n")))))
	return copied.Encode()
}

func mustReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(data)
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func writeTestRSAKey(path string) error {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
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
		t.Fatalf("GenerateKey() error = %v", err)
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
		t.Fatalf("NewLoginVerifier() error = %v", err)
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
		t.Fatalf("Marshal(header) error = %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
