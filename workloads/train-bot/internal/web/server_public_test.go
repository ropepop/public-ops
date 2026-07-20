package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
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
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type publicSnapshot struct {
	SourceVersion string                `json:"source_version"`
	Trains        []publicSnapshotTrain `json:"trains"`
}

type publicSnapshotTrain struct {
	ID          string               `json:"id"`
	ServiceDate string               `json:"service_date"`
	FromStation string               `json:"from_station"`
	ToStation   string               `json:"to_station"`
	DepartureAt string               `json:"departure_at"`
	ArrivalAt   string               `json:"arrival_at"`
	Stops       []publicSnapshotStop `json:"stops"`
}

type publicSnapshotStop struct {
	StationName string   `json:"station_name"`
	Seq         int      `json:"seq"`
	ArrivalAt   string   `json:"arrival_at,omitempty"`
	DepartureAt string   `json:"departure_at,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

func TestServeHTTPPublicIncidentsShell(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/incidents", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("unexpected public incidents status: got %d body=%s", res.Code, res.Body.String())
	}
	if body := res.Body.String(); !strings.Contains(body, `mode: "public-incidents"`) {
		t.Fatalf("public incidents shell missing public-incidents mode: %s", body)
	}
}

func TestServeHTTPPublicShellRoutes(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	cases := []struct {
		path string
		mode string
	}{
		{path: "/pixel-stack/train", mode: "public-network-map"},
		{path: "/pixel-stack/train/app", mode: "mini-app"},
		{path: "/pixel-stack/train/feed", mode: "public-dashboard"},
		{path: "/pixel-stack/train/departures", mode: "public-dashboard"},
		{path: "/pixel-stack/train/stations", mode: "public-stations"},
		{path: "/pixel-stack/train/map", mode: "public-network-map"},
		{path: "/pixel-stack/train/incidents", mode: "public-incidents"},
		{path: "/pixel-stack/train/events", mode: "public-incidents"},
		{path: "/pixel-stack/train/t/train-next-0", mode: "public-train"},
		{path: "/pixel-stack/train/t/train-next-0/map", mode: "public-map"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s unexpected status: got %d body=%s", tc.path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", tc.path, got)
		}
		if body := res.Body.String(); !strings.Contains(body, `mode: "`+tc.mode+`"`) {
			t.Fatalf("%s shell missing %s mode: %s", tc.path, tc.mode, body)
		} else if !strings.Contains(body, `<meta name="robots" content="noindex, noarchive">`) {
			t.Fatalf("%s shell missing robots meta tag: %s", tc.path, body)
		}
	}
}

func TestServeHTTPPublicShellScriptsBypassCloudflareRocketLoader(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test")
	req := httptest.NewRequest("GET", "/", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{
		`<script data-cfasync="false" nonce="`,
		`<script data-cfasync="false" defer src="/assets/vendor/leaflet.js`,
		`<script data-cfasync="false" defer src="/assets/external-feed.js`,
		`<script data-cfasync="false" defer src="/assets/arrow-core.js`,
		`<script data-cfasync="false" defer src="/assets/app.js`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing Rocket Loader opt-out marker %q: %s", want, body)
		}
	}
}

func TestServeHTTPUnknownPublicTrainShellRoutesReturnNotFound(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")

	for _, path := range []string{
		"/pixel-stack/train/t/not-real",
		"/pixel-stack/train/t/not-real/map",
		"/pixel-stack/train/t/811",
		"/pixel-stack/train/t/811/map",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}

		headReq := httptest.NewRequest(http.MethodHead, path, nil)
		headRes := httptest.NewRecorder()
		server.ServeHTTP(headRes, headReq)
		if headRes.Code != http.StatusNotFound {
			t.Fatalf("HEAD %s status = %d, want 404 body=%s", path, headRes.Code, headRes.Body.String())
		}
		if got := headRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("HEAD %s Cache-Control = %q, want no-store", path, got)
		}
		if got := headRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("HEAD %s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}
}

func TestServeHTTPMalformedPublicTrainShellRouteUsesStrictMethodAndCacheHeaders(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")

	optionsReq := httptest.NewRequest(http.MethodOptions, "/pixel-stack/train/t/example/extra", nil)
	optionsRes := httptest.NewRecorder()
	server.ServeHTTP(optionsRes, optionsReq)
	if optionsRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("OPTIONS malformed train route status = %d, want 405 body=%s", optionsRes.Code, optionsRes.Body.String())
	}
	if got := optionsRes.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("OPTIONS malformed train route Allow = %q, want GET, HEAD", got)
	}
	if got := optionsRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("OPTIONS malformed train route Cache-Control = %q", got)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/t/example/extra", nil)
	getRes := httptest.NewRecorder()
	server.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusNotFound {
		t.Fatalf("GET malformed train route status = %d, want 404 body=%s", getRes.Code, getRes.Body.String())
	}
	if got := getRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("GET malformed train route Cache-Control = %q", got)
	}
}

func TestServeHTTPUnsafePublicPathErrorsAreNoStoreAndNoIndex(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")

	for _, path := range []string{
		"/pixel-stack/train/api/v1/public/trains/train%2Fhack",
		"/pixel-stack/train/api%2fv1%2fpublic%2fdashboard",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 body=%s", path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}
}

func TestRedactPublicJSONPayloadRemovesRawScheduleAndReportFields(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"generatedAt": "2026-05-11T02:00:11.345561043Z",
		"schedule": map[string]any{
			"requestedServiceDate": "2026-05-12",
			"effectiveServiceDate": "2026-05-11",
			"loadedServiceDate":    "2026-05-10",
			"fallbackActive":       true,
			"cutoffHour":           float64(3),
			"available":            true,
			"sameDayFresh":         false,
		},
		"trains": []any{
			map[string]any{
				"train": map[string]any{
					"id":            "train-1",
					"sourceVersion": "agg-2026-04-10",
				},
				"timeline": []any{
					map[string]any{
						"at":     "2026-04-10T06:00:00Z",
						"signal": "INSPECTION_STARTED",
						"count":  float64(2),
					},
				},
			},
		},
		"stations": []any{
			map[string]any{
				"id":        "station-1",
				"latitude":  57.3332937309977,
				"longitude": 24.4630916121241,
			},
		},
	}

	redacted := redactPublicJSONPayload(payload)
	body, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted payload: %v", err)
	}
	text := string(body)
	for _, forbidden := range []string{"sourceVersion", "INSPECTION_STARTED", `"signal"`, "requestedServiceDate", "loadedServiceDate", "fallbackActive", "cutoffHour", "sameDayFresh", "345561043"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public payload still exposes %q: %s", forbidden, text)
		}
	}
	if strings.Contains(text, "57.3332937309977") || strings.Contains(text, "24.4630916121241") {
		t.Fatalf("public payload still exposes raw fields: %s", text)
	}
	if !strings.Contains(text, `"eventLabel":"Inspection started"`) {
		t.Fatalf("public payload missing event label: %s", text)
	}
	if !strings.Contains(text, `"generatedAt":"2026-05-11T02:00:11Z"`) {
		t.Fatalf("public payload did not round generatedAt to seconds: %s", text)
	}
	if !strings.Contains(text, `"schedule":{"available":true,"effectiveServiceDate":"2026-05-11"}`) {
		t.Fatalf("public payload did not reduce schedule fields: %s", text)
	}
	if !strings.Contains(text, `"latitude":57.33329`) || !strings.Contains(text, `"longitude":24.46309`) {
		t.Fatalf("public payload did not round coordinates: %s", text)
	}
}

func TestPublicShellDoesNotLoadTelegramScripts(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, needle := range []string{
		"https://telegram.org/js/telegram-web-app.js",
		"https://oauth.telegram.org/js/telegram-login.js",
	} {
		if strings.Contains(body, needle) {
			t.Fatalf("public shell loads Telegram script %q: %s", needle, body)
		}
	}
	csp := res.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "https://telegram.org") || strings.Contains(csp, "https://oauth.telegram.org") {
		t.Fatalf("public shell CSP allows Telegram scripts: %q", csp)
	}
}

func TestPublicShellUsesSpecificConnectSources(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	server.cfg.ExternalTrainMapEnabled = true
	server.cfg.ExternalTrainMapBaseURL = "https://trainmap.vivi.lv"
	server.cfg.ExternalTrainMapWsURL = "wss://trainmap.pv.lv/ws"
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	csp := res.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "connect-src 'self' https: wss:") {
		t.Fatalf("public shell CSP still allows all HTTPS/WSS connections: %q", csp)
	}
	if strings.Contains(csp, "img-src 'self' data: https:;") {
		t.Fatalf("public shell CSP still allows all HTTPS images: %q", csp)
	}
	want := "connect-src 'self' https://trainmap.vivi.lv wss://trainmap.pv.lv https://stdb.example.test"
	if !strings.Contains(csp, want) {
		t.Fatalf("public shell CSP connect-src = %q, want to contain %q", csp, want)
	}
	if !strings.Contains(csp, "img-src 'self' data: https://*.tile.openstreetmap.org") {
		t.Fatalf("public shell CSP image sources = %q, want OpenStreetMap tiles only", csp)
	}
}

func TestPublicShellDoesNotExposeSpacetimeConnectionConfig(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train",
		"/pixel-stack/train/app",
		"/pixel-stack/train/map",
		"/pixel-stack/train/incidents",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s unexpected status: got %d body=%s", path, res.Code, res.Body.String())
		}
		body := res.Body.String()
		for _, needle := range []string{
			"https://stdb.example.test",
			`spacetimeDatabase: "train-bot"`,
		} {
			if strings.Contains(body, needle) {
				t.Fatalf("%s public shell exposes Spacetime config %q: %s", path, needle, body)
			}
		}
		if !strings.Contains(body, `spacetimeHost: ""`) {
			t.Fatalf("%s public shell should leave spacetimeHost empty: %s", path, body)
		}
		if !strings.Contains(body, `spacetimeDatabase: ""`) {
			t.Fatalf("%s public shell should leave spacetimeDatabase empty: %s", path, body)
		}
	}
}

func TestMiniAppShellLoadsOnlyTelegramWebAppScript(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/app", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "https://telegram.org/js/telegram-web-app.js") {
		t.Fatalf("mini-app shell missing Telegram WebApp script: %s", body)
	}
	if strings.Contains(body, "https://oauth.telegram.org/js/telegram-login.js") {
		t.Fatalf("mini-app shell should not preload Telegram login script: %s", body)
	}
	csp := res.Header().Get("Content-Security-Policy")
	if got := res.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("mini-app shell X-Frame-Options = %q, want empty for Telegram Web embedding", got)
	}
	if !strings.Contains(csp, "frame-ancestors https://web.telegram.org") {
		t.Fatalf("mini-app shell CSP does not allow Telegram Web embedding: %q", csp)
	}
	if strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("mini-app shell CSP still denies Telegram Web embedding: %q", csp)
	}
	if !strings.Contains(csp, "https://telegram.org") {
		t.Fatalf("mini-app shell CSP missing Telegram WebApp script source: %q", csp)
	}
	if strings.Contains(csp, "https://oauth.telegram.org") {
		t.Fatalf("mini-app shell CSP should not allow Telegram login script source: %q", csp)
	}
}

func TestServeHTTPAppliesSecurityHeadersWithoutDebugHeaders(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train",
		"/pixel-stack/train/api/v1/health",
		"/pixel-stack/train/assets/app.js",
		"/pixel-stack/train/assets/app.css",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s unexpected status: got %d body=%s", path, res.Code, res.Body.String())
		}
		for _, header := range []string{
			"Strict-Transport-Security",
			"Content-Security-Policy",
			"X-Frame-Options",
			"X-Content-Type-Options",
			"Referrer-Policy",
			"Permissions-Policy",
		} {
			if res.Header().Get(header) == "" {
				t.Fatalf("%s missing security header %s", path, header)
			}
		}
		csp := res.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "'unsafe-inline'") {
			t.Fatalf("%s Content-Security-Policy still allows inline code: %q", path, csp)
		}
		if !strings.Contains(csp, "style-src 'self'") {
			t.Fatalf("%s Content-Security-Policy missing strict style-src: %q", path, csp)
		}
		if strings.Contains(path, "/assets/") {
			if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
				t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
			}
		}
		for name := range res.Header() {
			if strings.HasPrefix(strings.ToLower(name), "x-train-bot-") {
				t.Fatalf("%s exposed debug header %s=%q", path, name, res.Header().Get(name))
			}
		}
	}
}

func TestServeHTTPAuthConfigErrorStillAppliesSecurityHeaders(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/auth/telegram/config", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	for _, header := range []string{
		"Strict-Transport-Security",
		"Content-Security-Policy",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Permissions-Policy",
	} {
		if res.Header().Get(header) == "" {
			t.Fatalf("auth config error missing security header %s", header)
		}
	}
	if got := res.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("auth config error HSTS = %q, want max-age=31536000", got)
	}
}

func TestServeHTTPPublicHealthAndReadyAreMinimal(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train/api/v1/health",
		"/pixel-stack/train/api/v1/ready",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s unexpected status: got %d body=%s", path, res.Code, res.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s decode payload: %v", path, err)
		}
		if payload["ok"] != true {
			t.Fatalf("%s ok = %#v, want true", path, payload["ok"])
		}
		for _, forbidden := range []string{
			"assets",
			"bundle",
			"loadedServiceDate",
			"now",
			"readinessReason",
			"runtime",
			"schedule",
			"scheduleAvailable",
			"scheduleError",
			"scheduleFallbackActive",
			"scheduleSameDayFresh",
			"staleLoadedServiceDate",
			"version",
		} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("%s public health exposed %q in payload %#v", path, forbidden, payload)
			}
		}
	}
}

func TestServeHTTPDoesNotServeProductionOnlyExcludedAssets(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train/assets/app.test.js",
		"/pixel-stack/train/assets/external-feed.test.js",
		"/pixel-stack/train/assets/live-client.test.js",
		"/pixel-stack/train/assets/live-client.js",
		"/pixel-stack/train/assets/app.js.map",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code == http.StatusOK {
			t.Fatalf("%s should not be publicly served, got 200", path)
		}
	}
}

func TestServeHTTPRejectsUnsafeStaticPathsAndUnsupportedMethods(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/pixel-stack/train/assets/%2e%2e/app.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/pixel-stack/train/assets//app.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/pixel-stack/train/assets%5capp.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/pixel-stack/train/assets/app.js/", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/pixel-stack/train/assets/bundles/2026-05-11/manifest.json/", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/pixel-stack/train/api%2fv1%2fpublic%2ffeed", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/pixel-stack/train/api%5cv1%5cpublic%5cfeed", want: http.StatusBadRequest},
		{method: http.MethodPost, path: "/pixel-stack/train", want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/pixel-stack/train/assets/app.js", want: http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("%s %s status = %d, want %d body=%s", tc.method, tc.path, res.Code, tc.want, res.Body.String())
		}
	}
}

func TestServeHTTPMessagesRejectUnsupportedLanguage(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, query := range []string{"lang=zz", "lang=..%2Flv"} {
		req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/messages?"+query, nil)
		res := httptest.NewRecorder()

		server.ServeHTTP(res, req)

		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 body=%s", query, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q, want no-store", query, got)
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", query, got)
		}
	}
}

func TestServeHTTPStaticAssetQueryMustOnlyContainExpectedVersion(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	appHash := server.release.AssetHash("app.js")
	cases := []struct {
		path              string
		want              int
		wantCacheContains string
	}{
		{path: "/pixel-stack/train/assets/app.js", want: http.StatusOK, wantCacheContains: "no-store"},
		{path: "/pixel-stack/train/assets/app.js?v=" + appHash, want: http.StatusOK, wantCacheContains: "immutable"},
		{path: "/pixel-stack/train/assets/app.js?v=wrong", want: http.StatusNotFound, wantCacheContains: "no-store"},
		{path: "/pixel-stack/train/assets/app.js?v=" + appHash + "&debug=1", want: http.StatusNotFound, wantCacheContains: "no-store"},
		{path: "/pixel-stack/train/assets/app.js?v=" + appHash + "&v=" + appHash, want: http.StatusNotFound, wantCacheContains: "no-store"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("%s status = %d, want %d body=%s", tc.path, res.Code, tc.want, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); !strings.Contains(got, tc.wantCacheContains) {
			t.Fatalf("%s Cache-Control = %q, want %q", tc.path, got, tc.wantCacheContains)
		}
	}
}

func TestServeHTTPMissingPublicPathsAreNoStore(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test")
	for _, path := range []string{
		"/service-worker.js",
		"/manifest.json",
		"/favicon.ico",
		"/site.webmanifest",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/.well-known/security.txt",
		"/sitemap.xml",
		"/spacetimedb/dist/bundle.js",
		"/deploy-validation-missing-path",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}
}

func TestServeHTTPRobotsTxtDeniesIndexing(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/robots.txt", nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s /robots.txt status = %d, want 200 body=%s", method, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s /robots.txt Cache-Control = %q, want no-store", method, got)
		}
		if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s /robots.txt X-Robots-Tag = %q, want noindex, noarchive", method, got)
		}
		if method == http.MethodGet {
			body := strings.ToLower(res.Body.String())
			if !strings.Contains(body, "user-agent: *") || !strings.Contains(body, "disallow: /") {
				t.Fatalf("robots.txt does not deny indexing: %q", res.Body.String())
			}
		}
	}
}

func TestProductionAppBundleDoesNotExposeTestLoginStrings(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(mustStaticSubFS(), "app.js")
	if err != nil {
		t.Fatalf("read app bundle: %v", err)
	}
	for _, forbidden := range []string{
		"__test__",
		`"__" + "test__"`,
		"test_ticket",
		"/auth/test",
		"stripTestTicketFromLocation",
	} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("production app bundle exposes test-login string %q", forbidden)
		}
	}
}

func TestServeHTTPRootDeploymentRejectsPrefixedTrainRoutes(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test")
	cases := []struct {
		path string
		mode string
	}{
		{path: "/", mode: "public-network-map"},
		{path: "/map", mode: "public-network-map"},
		{path: "/events", mode: "public-incidents"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s unexpected status: got %d body=%s", tc.path, res.Code, res.Body.String())
		}
		if body := res.Body.String(); !strings.Contains(body, `mode: "`+tc.mode+`"`) {
			t.Fatalf("%s shell missing %s mode: %s", tc.path, tc.mode, body)
		}
	}

	for _, path := range []string{
		"/pixel-stack/train",
		"/pixel-stack/train/map",
		"/pixel-stack/train/events",
		"/pixel-stack/train/api/v1/health",
	} {
		req := httptest.NewRequest("GET", path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestServeHTTPPublicReadAPIRoutesAllowHeadAndRejectOptions(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train/api/v1/messages?lang=lv",
		"/pixel-stack/train/api/v1/public/dashboard?limit=1",
		"/pixel-stack/train/api/v1/public/service-day-trains",
		"/pixel-stack/train/api/v1/public/map",
		"/pixel-stack/train/api/v1/public/stations?q=riga",
		"/pixel-stack/train/api/v1/public/stations/riga/departures",
		"/pixel-stack/train/api/v1/public/trains/train-next-0",
		"/pixel-stack/train/api/v1/public/trains/train-next-0/stops",
		"/pixel-stack/train/api/v1/public/incidents?limit=1",
		"/pixel-stack/train/api/v1/public/route-checkin-routes",
	} {
		getReq := httptest.NewRequest(http.MethodGet, path, nil)
		getRes := httptest.NewRecorder()
		server.ServeHTTP(getRes, getReq)
		if getRes.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 body=%s", path, getRes.Code, getRes.Body.String())
		}
		if strings.HasPrefix(path, "/pixel-stack/train/api/v1/public/") || strings.Contains(path, "/api/v1/messages") {
			if got := getRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
				t.Fatalf("GET %s X-Robots-Tag = %q, want noindex, noarchive", path, got)
			}
		}

		headReq := httptest.NewRequest(http.MethodHead, path, nil)
		headRes := httptest.NewRecorder()
		server.ServeHTTP(headRes, headReq)
		if headRes.Code != http.StatusOK {
			t.Fatalf("HEAD %s status = %d, want 200 body=%s", path, headRes.Code, headRes.Body.String())
		}

		optionsReq := httptest.NewRequest(http.MethodOptions, path, nil)
		optionsRes := httptest.NewRecorder()
		server.ServeHTTP(optionsRes, optionsReq)
		if optionsRes.Code != http.StatusMethodNotAllowed {
			t.Fatalf("OPTIONS %s status = %d, want 405 body=%s", path, optionsRes.Code, optionsRes.Body.String())
		}
		if got := optionsRes.Header().Get("Allow"); got != "GET, HEAD" {
			t.Fatalf("OPTIONS %s Allow = %q, want GET, HEAD", path, got)
		}
	}

	for _, path := range []string{
		"/pixel-stack/train/api/v1/public/route-checkin-routes?debug=1",
		"/pixel-stack/train/api/v1/public/route-checkin-routes?limit=1&limit=2",
		"/pixel-stack/train/api/v1/public/route-checkin-routes?cv=bogus",
	} {
		getReq := httptest.NewRequest(http.MethodGet, path, nil)
		getRes := httptest.NewRecorder()
		server.ServeHTTP(getRes, getReq)
		if getRes.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400 body=%s", path, getRes.Code, getRes.Body.String())
		}
		if got := getRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("GET %s Cache-Control = %q", path, got)
		}
		if got := getRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("GET %s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}

		headReq := httptest.NewRequest(http.MethodHead, path, nil)
		headRes := httptest.NewRecorder()
		server.ServeHTTP(headRes, headReq)
		if headRes.Code != http.StatusBadRequest {
			t.Fatalf("HEAD %s status = %d, want 400 body=%s", path, headRes.Code, headRes.Body.String())
		}
	}
}

func TestServeHTTPPublicFeedsRejectInvalidLimits(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	for _, path := range []string{
		"/pixel-stack/train/api/v1/public/dashboard",
		"/pixel-stack/train/api/v1/public/incidents",
	} {
		for _, query := range []string{
			"limit=abc",
			"limit=-1",
			"limit=2001",
			"limit=1&limit=999",
		} {
			req := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
			res := httptest.NewRecorder()
			server.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("%s?%s status = %d, want 400 body=%s", path, query, res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
				t.Fatalf("%s?%s Cache-Control = %q", path, query, got)
			}
			if got := res.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
				t.Fatalf("%s?%s X-Robots-Tag = %q, want noindex, noarchive", path, query, got)
			}
		}
	}
}

func TestServeHTTPPublicIncidentsReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/incidents?limit=0", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public incidents status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Incidents []any `json:"incidents"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public incidents: %v", err)
	}
	if payload.Incidents == nil {
		t.Fatalf("expected incidents payload, got %+v", payload)
	}
}

func TestServeHTTPPublicStationsAndDepartures(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "jelgava"
	matchedTrainID := "train-past"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-public", "riga", &destinationID, &matchedTrainID, 77, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}

	stationsReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/stations?q=ri", nil)
	stationsRes := httptest.NewRecorder()
	server.ServeHTTP(stationsRes, stationsReq)
	if stationsRes.Code != 200 {
		t.Fatalf("unexpected public stations status: got %d body=%s", stationsRes.Code, stationsRes.Body.String())
	}
	var stationsPayload struct {
		Stations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stations"`
	}
	if err := json.Unmarshal(stationsRes.Body.Bytes(), &stationsPayload); err != nil {
		t.Fatalf("decode public stations: %v", err)
	}
	if len(stationsPayload.Stations) == 0 || stationsPayload.Stations[0].ID != "riga" {
		t.Fatalf("unexpected public stations payload: %+v", stationsPayload.Stations)
	}

	departuresReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/stations/riga/departures", nil)
	departuresRes := httptest.NewRecorder()
	server.ServeHTTP(departuresRes, departuresReq)
	if departuresRes.Code != 200 {
		t.Fatalf("unexpected public departures status: got %d body=%s", departuresRes.Code, departuresRes.Body.String())
	}
	var departuresPayload struct {
		Station struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"station"`
		LastDeparture *struct {
			TrainCard struct {
				Train struct {
					ID string `json:"id"`
				} `json:"train"`
			} `json:"trainCard"`
		} `json:"lastDeparture"`
		Upcoming []struct {
			TrainCard struct {
				Train struct {
					ID string `json:"id"`
				} `json:"train"`
			} `json:"trainCard"`
		} `json:"upcoming"`
		RecentSightings []struct {
			StationID string `json:"stationId"`
		} `json:"recentSightings"`
	}
	if err := json.Unmarshal(departuresRes.Body.Bytes(), &departuresPayload); err != nil {
		t.Fatalf("decode public departures: %v", err)
	}
	if departuresPayload.Station.ID != "riga" {
		t.Fatalf("unexpected station payload: %+v", departuresPayload.Station)
	}
	if departuresPayload.LastDeparture == nil {
		t.Fatalf("expected lastDeparture in public response")
	}
	dayEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), now.Location())
	expectedUpcoming := 0
	for i := 0; i < 10 && expectedUpcoming < 8; i++ {
		if !now.Add(time.Duration(i+1) * 15 * time.Minute).After(dayEnd) {
			expectedUpcoming++
		}
	}
	if len(departuresPayload.Upcoming) != expectedUpcoming {
		t.Fatalf("expected %d upcoming departures for the remainder of the service day, got %d", expectedUpcoming, len(departuresPayload.Upcoming))
	}
	if len(departuresPayload.RecentSightings) != 1 || departuresPayload.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent station sighting in departures payload, got %+v", departuresPayload.RecentSightings)
	}

	privateReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/stations?q=ri", nil)
	privateRes := httptest.NewRecorder()
	server.ServeHTTP(privateRes, privateReq)
	if privateRes.Code != 401 {
		t.Fatalf("expected private stations endpoint to require auth, got %d body=%s", privateRes.Code, privateRes.Body.String())
	}
}

func TestServeHTTPPublicMapReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "jelgava"
	matchedTrainID := "train-past"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-public-map", "riga", &destinationID, &matchedTrainID, 77, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public map status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Stations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stations"`
		RecentSightings []struct {
			StationID              string  `json:"stationId"`
			MatchedTrainInstanceID *string `json:"matchedTrainInstanceId"`
		} `json:"recentSightings"`
		SameDaySightings []struct {
			StationID string `json:"stationId"`
		} `json:"sameDaySightings"`
		Schedule struct {
			Available            bool   `json:"available"`
			EffectiveServiceDate string `json:"effectiveServiceDate"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public map: %v", err)
	}
	hasRiga := false
	for _, station := range payload.Stations {
		if station.ID == "riga" {
			hasRiga = true
			break
		}
	}
	if !hasRiga {
		t.Fatalf("unexpected station payload: %+v", payload.Stations)
	}
	if !payload.Schedule.Available || payload.Schedule.EffectiveServiceDate == "" {
		t.Fatalf("unexpected schedule payload: %+v", payload.Schedule)
	}
	if len(payload.RecentSightings) != 1 || payload.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent station sighting in network map payload, got %+v", payload.RecentSightings)
	}
	if payload.RecentSightings[0].MatchedTrainInstanceID == nil || *payload.RecentSightings[0].MatchedTrainInstanceID != "train-past" {
		t.Fatalf("expected matched train in recent sightings, got %+v", payload.RecentSightings[0])
	}
	if len(payload.SameDaySightings) != 1 || payload.SameDaySightings[0].StationID != "riga" {
		t.Fatalf("expected same-day station sighting in network map payload, got %+v", payload.SameDaySightings)
	}
}

func TestServeHTTPStationSightingDestinationsReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/stations/riga/sighting-destinations", nil)
	req.AddCookie(testSessionCookie(t, server, 77, "en", now))
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected sighting destinations status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Stations []struct {
			ID string `json:"id"`
		} `json:"stations"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sighting destinations: %v", err)
	}
	if len(payload.Stations) == 0 {
		t.Fatalf("expected station sighting destinations, got %+v", payload)
	}
}

func newPublicDataServer(t *testing.T, publicBaseURL string) *Server {
	server, _, _ := newPublicDataServerWithStore(t, publicBaseURL)
	return server
}

func newPublicDataServerWithStore(t *testing.T, publicBaseURL string) (*Server, *store.SQLiteStore, time.Time) {
	return newPublicDataServerWithStoreAndTrainCount(t, publicBaseURL, 10)
}

func newPublicDataServerWithStoreAndTrainCount(t *testing.T, publicBaseURL string, futureTrainCount int) (*Server, *store.SQLiteStore, time.Time) {
	t.Helper()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")

	dir := t.TempDir()
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
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotPath := filepath.Join(dir, serviceDate+".json")
	trains := []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-past", serviceDate, "Riga", "Jelgava", now.Add(-20*time.Minute)),
	}
	for i := 0; i < futureTrainCount; i++ {
		trains = append(trains, buildPublicSnapshotTrain(
			"train-next-"+strconv.Itoa(i),
			serviceDate,
			"Riga",
			"Stop "+strconv.Itoa(i),
			now.Add(time.Duration(i+1)*15*time.Minute),
		))
	}
	payload, err := json.Marshal(publicSnapshot{
		SourceVersion: "server-public-test",
		Trains:        trains,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, dir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	appSvc := trainapp.NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)
	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              publicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, appSvc, i18n.NewCatalog(), loc)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.now = func() time.Time { return now }
	return server, st, now
}

func newPublicDataServerWithLoadedSnapshot(t *testing.T, publicBaseURL string, now time.Time, loadAt time.Time, trains []publicSnapshotTrain) (*Server, *store.SQLiteStore) {
	t.Helper()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	dir := t.TempDir()
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
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	serviceDate := loadAt.In(loc).Format("2006-01-02")
	if len(trains) == 0 {
		trains = []publicSnapshotTrain{
			buildPublicSnapshotTrain("train-fallback", serviceDate, "Riga", "Jelgava", loadAt.Add(time.Hour)),
		}
	}
	snapshotPath := filepath.Join(dir, serviceDate+".json")
	payload, err := json.Marshal(publicSnapshot{
		SourceVersion: "server-public-fallback-test",
		Trains:        trains,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, dir, loc, 3)
	if err := manager.LoadToday(ctx, loadAt.In(loc)); err != nil {
		t.Fatalf("load schedule: %v", err)
	}

	appSvc := trainapp.NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)
	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              publicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, appSvc, i18n.NewCatalog(), loc)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.now = func() time.Time { return now }
	return server, st
}

func TestServeHTTPPublicDashboardLimitZeroReturnsAllTodayTrains(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")
	trains := make([]publicSnapshotTrain, 0, 75)
	for i := 0; i < 75; i++ {
		trains = append(trains, buildPublicSnapshotTrain(
			"train-bulk-"+strconv.Itoa(i),
			serviceDate,
			"Riga",
			"Stop "+strconv.Itoa(i),
			now.Add(time.Duration(i+1)*time.Second),
		))
	}
	server, st := newAuthenticatedDataServerWithTrains(t, "https://example.test/pixel-stack/train", now, trains)
	if err := st.CheckInUser(context.Background(), 44, "train-bulk-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	defaultReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard", nil)
	defaultRes := httptest.NewRecorder()
	server.ServeHTTP(defaultRes, defaultReq)
	if defaultRes.Code != 200 {
		t.Fatalf("unexpected default dashboard status: got %d body=%s", defaultRes.Code, defaultRes.Body.String())
	}
	var defaultPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(defaultRes.Body.Bytes(), &defaultPayload); err != nil {
		t.Fatalf("decode default dashboard payload: %v", err)
	}
	if len(defaultPayload.Trains) != 60 {
		t.Fatalf("expected default dashboard limit of 60, got %d", len(defaultPayload.Trains))
	}
	if defaultPayload.Trains[0].Train.ID != "train-bulk-0" || defaultPayload.Trains[0].Riders != 0 {
		t.Fatalf("expected single rider hidden for first dashboard train, got %+v", defaultPayload.Trains[0])
	}

	allReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	allRes := httptest.NewRecorder()
	server.ServeHTTP(allRes, allReq)
	if allRes.Code != 200 {
		t.Fatalf("unexpected all dashboard status: got %d body=%s", allRes.Code, allRes.Body.String())
	}
	var allPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(allRes.Body.Bytes(), &allPayload); err != nil {
		t.Fatalf("decode limit=0 dashboard payload: %v", err)
	}
	if len(allPayload.Trains) != 75 {
		t.Fatalf("expected limit=0 dashboard to return all 75 trains, got %d", len(allPayload.Trains))
	}
	if allPayload.Trains[0].Train.ID != "train-bulk-0" || allPayload.Trains[0].Riders != 0 {
		t.Fatalf("expected single rider hidden for full dashboard payload, got %+v", allPayload.Trains[0])
	}
}

func TestServeHTTPPublicServiceDayTrainsIncludesDepartedTrainsOutsideDashboardWindow(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")
	server, st := newAuthenticatedDataServerWithTrains(t, "https://example.test/pixel-stack/train", now, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-past", serviceDate, "Riga", "Jelgava", now.Add(-45*time.Minute)),
		buildPublicSnapshotTrain("train-next", serviceDate, "Riga", "Tukums", now.Add(15*time.Minute)),
	})
	if err := st.CheckInUser(context.Background(), 44, "train-next", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	dashboardReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	dashboardRes := httptest.NewRecorder()
	server.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != 200 {
		t.Fatalf("unexpected dashboard status: got %d body=%s", dashboardRes.Code, dashboardRes.Body.String())
	}
	var dashboardPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(dashboardRes.Body.Bytes(), &dashboardPayload); err != nil {
		t.Fatalf("decode dashboard payload: %v", err)
	}
	if len(dashboardPayload.Trains) != 1 || dashboardPayload.Trains[0].Train.ID != "train-next" {
		t.Fatalf("expected dashboard to keep only the future train, got %+v", dashboardPayload.Trains)
	}
	if dashboardPayload.Trains[0].Riders != 0 {
		t.Fatalf("expected single rider hidden on dashboard payload, got %+v", dashboardPayload.Trains[0])
	}

	serviceDayReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/service-day-trains", nil)
	serviceDayRes := httptest.NewRecorder()
	server.ServeHTTP(serviceDayRes, serviceDayReq)
	if serviceDayRes.Code != 200 {
		t.Fatalf("unexpected service day trains status: got %d body=%s", serviceDayRes.Code, serviceDayRes.Body.String())
	}
	var serviceDayPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(serviceDayRes.Body.Bytes(), &serviceDayPayload); err != nil {
		t.Fatalf("decode service day payload: %v", err)
	}
	if len(serviceDayPayload.Trains) != 2 {
		t.Fatalf("expected service day trains to include both departures, got %d", len(serviceDayPayload.Trains))
	}
	if serviceDayPayload.Trains[0].Train.ID != "train-past" || serviceDayPayload.Trains[1].Train.ID != "train-next" {
		t.Fatalf("unexpected service day train order: %+v", serviceDayPayload.Trains)
	}
	if serviceDayPayload.Trains[1].Riders != 0 {
		t.Fatalf("expected single rider hidden on service day payload, got %+v", serviceDayPayload.Trains[1])
	}
}

func TestServeHTTPPublicRiderCountsAreBucketed(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	seedPublicActiveCheckins(t, st, "train-next-0", now, 1)
	seedPublicActiveCheckins(t, st, "train-next-1", now, 3)
	seedPublicReporters(t, st, "train-next-0", now, 1)
	seedPublicReporters(t, st, "train-next-1", now, 3)

	dashboardReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	dashboardRes := httptest.NewRecorder()
	server.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: got %d body=%s", dashboardRes.Code, dashboardRes.Body.String())
	}
	var dashboardPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Status struct {
				UniqueReporters int `json:"uniqueReporters"`
			} `json:"status"`
			Train struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(dashboardRes.Body.Bytes(), &dashboardPayload); err != nil {
		t.Fatalf("decode dashboard payload: %v", err)
	}
	if len(dashboardPayload.Trains) < 3 {
		t.Fatalf("expected at least three dashboard trains, got %+v", dashboardPayload.Trains)
	}
	ridersByTrainID := func(trainID string) int {
		for _, item := range dashboardPayload.Trains {
			if item.Train.ID == trainID {
				return item.Riders
			}
		}
		return -1
	}
	if got := ridersByTrainID("train-next-0"); got != 0 {
		t.Fatalf("expected single rider to be hidden on dashboard, got %d", got)
	}
	if got := ridersByTrainID("train-next-1"); got != 2 {
		t.Fatalf("expected three riders to be bucketed to 2 on dashboard, got %d", got)
	}
	reportersByTrainID := func(trainID string) int {
		for _, item := range dashboardPayload.Trains {
			if item.Train.ID == trainID {
				return item.Status.UniqueReporters
			}
		}
		return -1
	}
	if got := reportersByTrainID("train-next-0"); got != 0 {
		t.Fatalf("expected single reporter to be hidden on dashboard, got %d", got)
	}
	if got := reportersByTrainID("train-next-1"); got != 2 {
		t.Fatalf("expected three reporters to be bucketed to 2 on dashboard, got %d", got)
	}

	trainReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-1", nil)
	trainRes := httptest.NewRecorder()
	server.ServeHTTP(trainRes, trainReq)
	if trainRes.Code != http.StatusOK {
		t.Fatalf("unexpected public train status: got %d body=%s", trainRes.Code, trainRes.Body.String())
	}
	var trainPayload struct {
		Riders int `json:"riders"`
		Status struct {
			UniqueReporters int `json:"uniqueReporters"`
		} `json:"status"`
	}
	if err := json.Unmarshal(trainRes.Body.Bytes(), &trainPayload); err != nil {
		t.Fatalf("decode public train payload: %v", err)
	}
	if trainPayload.Riders != 2 {
		t.Fatalf("expected public train riders bucketed to 2, got %+v", trainPayload)
	}
	if trainPayload.Status.UniqueReporters != 2 {
		t.Fatalf("expected public train reporters bucketed to 2, got %+v", trainPayload.Status)
	}

	stopsReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-1/stops", nil)
	stopsRes := httptest.NewRecorder()
	server.ServeHTTP(stopsRes, stopsReq)
	if stopsRes.Code != http.StatusOK {
		t.Fatalf("unexpected public train stops status: got %d body=%s", stopsRes.Code, stopsRes.Body.String())
	}
	var stopsPayload struct {
		TrainCard struct {
			Riders int `json:"riders"`
			Status struct {
				UniqueReporters int `json:"uniqueReporters"`
			} `json:"status"`
		} `json:"trainCard"`
	}
	if err := json.Unmarshal(stopsRes.Body.Bytes(), &stopsPayload); err != nil {
		t.Fatalf("decode public train stops payload: %v", err)
	}
	if stopsPayload.TrainCard.Riders != 2 {
		t.Fatalf("expected public train stops riders bucketed to 2, got %+v", stopsPayload.TrainCard)
	}
	if stopsPayload.TrainCard.Status.UniqueReporters != 2 {
		t.Fatalf("expected public train stops reporters bucketed to 2, got %+v", stopsPayload.TrainCard.Status)
	}

	stationReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/stations/riga/departures", nil)
	stationRes := httptest.NewRecorder()
	server.ServeHTTP(stationRes, stationReq)
	if stationRes.Code != http.StatusOK {
		t.Fatalf("unexpected public station departures status: got %d body=%s", stationRes.Code, stationRes.Body.String())
	}
	var stationPayload struct {
		Upcoming []struct {
			TrainCard struct {
				Riders int `json:"riders"`
				Status struct {
					UniqueReporters int `json:"uniqueReporters"`
				} `json:"status"`
				Train struct {
					ID string `json:"id"`
				} `json:"train"`
			} `json:"trainCard"`
		} `json:"upcoming"`
	}
	if err := json.Unmarshal(stationRes.Body.Bytes(), &stationPayload); err != nil {
		t.Fatalf("decode public station departures payload: %v", err)
	}
	if len(stationPayload.Upcoming) < 2 {
		t.Fatalf("expected at least two upcoming departures, got %+v", stationPayload.Upcoming)
	}
	if stationPayload.Upcoming[0].TrainCard.Train.ID != "train-next-0" || stationPayload.Upcoming[0].TrainCard.Riders != 0 || stationPayload.Upcoming[0].TrainCard.Status.UniqueReporters != 0 {
		t.Fatalf("expected single rider hidden in public station departures, got %+v", stationPayload.Upcoming[0].TrainCard)
	}
	if stationPayload.Upcoming[1].TrainCard.Train.ID != "train-next-1" || stationPayload.Upcoming[1].TrainCard.Riders != 2 || stationPayload.Upcoming[1].TrainCard.Status.UniqueReporters != 2 {
		t.Fatalf("expected three riders bucketed to 2 in public station departures, got %+v", stationPayload.Upcoming[1].TrainCard)
	}
}

func buildPublicSnapshotTrain(id string, serviceDate string, fromStation string, toStation string, departureAt time.Time) publicSnapshotTrain {
	arrivalAt := departureAt.Add(45 * time.Minute)
	return publicSnapshotTrain{
		ID:          id,
		ServiceDate: serviceDate,
		FromStation: fromStation,
		ToStation:   toStation,
		DepartureAt: departureAt.Format(time.RFC3339),
		ArrivalAt:   arrivalAt.Format(time.RFC3339),
		Stops: []publicSnapshotStop{
			{
				StationName: fromStation,
				Seq:         1,
				DepartureAt: departureAt.Format(time.RFC3339),
				Latitude:    publicFloatPtr(56.9496),
				Longitude:   publicFloatPtr(24.1052),
			},
			{
				StationName: toStation,
				Seq:         2,
				ArrivalAt:   arrivalAt.Format(time.RFC3339),
				Latitude:    publicFloatPtr(56.6511),
				Longitude:   publicFloatPtr(23.7128),
			},
		},
	}
}

func seedPublicActiveCheckins(t *testing.T, st *store.SQLiteStore, trainID string, now time.Time, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		if err := st.CheckInUser(context.Background(), int64(1000+index), trainID, now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
			t.Fatalf("seed active checkin %d for %s: %v", index+1, trainID, err)
		}
	}
}

func seedPublicReporters(t *testing.T, st *store.SQLiteStore, trainID string, now time.Time, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		if err := st.InsertReportEvent(context.Background(), domain.ReportEvent{
			ID:              "public-report-" + trainID + "-" + strconv.Itoa(index),
			TrainInstanceID: trainID,
			UserID:          int64(2000 + index),
			Signal:          domain.SignalInspectionStarted,
			CreatedAt:       now.Add(-2 * time.Minute),
		}); err != nil {
			t.Fatalf("seed public reporter %d for %s: %v", index+1, trainID, err)
		}
	}
}

func TestServeHTTPPublicTrainStopsIncludesCoordinatesAndSightings(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "stop-0"
	matchedTrainID := "train-next-0"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-map", "riga", &destinationID, &matchedTrainID, 91, now.Add(-1*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}
	if err := st.CheckInUser(context.Background(), 44, "train-next-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-0/stops", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("unexpected public train stops status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		TrainCard struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trainCard"`
		Train struct {
			ID string `json:"id"`
		} `json:"train"`
		Stops []struct {
			StationID string   `json:"stationId"`
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
		} `json:"stops"`
		StationSightings []struct {
			MatchedTrainInstanceID *string `json:"matchedTrainInstanceId"`
		} `json:"stationSightings"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public train stops: %v", err)
	}
	if payload.Train.ID != "train-next-0" {
		t.Fatalf("unexpected train id: %q", payload.Train.ID)
	}
	if payload.TrainCard.Train.ID != "train-next-0" || payload.TrainCard.Riders != 0 {
		t.Fatalf("expected single rider hidden in stops payload, got %+v", payload.TrainCard)
	}
	if len(payload.Stops) != 2 {
		t.Fatalf("expected 2 stops, got %d", len(payload.Stops))
	}
	if payload.Stops[0].StationID != "riga" || payload.Stops[0].Latitude == nil || payload.Stops[0].Longitude == nil {
		t.Fatalf("expected coordinates on first stop, got %+v", payload.Stops[0])
	}
	if len(payload.StationSightings) != 1 || payload.StationSightings[0].MatchedTrainInstanceID == nil || *payload.StationSightings[0].MatchedTrainInstanceID != "train-next-0" {
		t.Fatalf("expected matched station sighting in stops payload, got %+v", payload.StationSightings)
	}
}

func TestServeHTTPPublicTrainIncludesRiderCount(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	if err := st.CheckInUser(context.Background(), 44, "train-next-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-0", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public train status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Riders int `json:"riders"`
		Train  struct {
			ID string `json:"id"`
		} `json:"train"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public train payload: %v", err)
	}
	if payload.Train.ID != "train-next-0" || payload.Riders != 0 {
		t.Fatalf("expected single rider hidden in public train payload, got %+v", payload)
	}
}

func TestServeHTTPHealthIsMinimal(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/health", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != 200 {
		t.Fatalf("unexpected health status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	for _, header := range []string{"X-Train-Bot-Commit", "X-Train-Bot-Build-Time", "X-Train-Bot-Instance", "X-Train-Bot-App-Js"} {
		if got := res.Header().Get(header); got != "" {
			t.Fatalf("public health exposed debug header %s=%q", header, got)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if len(payload) != 1 || payload["ok"] != true {
		t.Fatalf("expected minimal public health payload, got %+v", payload)
	}
}

func TestServeHTTPReadyReturnsOKDuringAllowedFallback(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	loadAt := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	now := time.Date(2026, 2, 28, 2, 59, 0, 0, loc)
	serviceDate := loadAt.Format("2006-01-02")
	server, _ := newPublicDataServerWithLoadedSnapshot(t, "https://example.test/pixel-stack/train", now, loadAt, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-fallback", serviceDate, "Riga", "Jelgava", time.Date(2026, 2, 28, 1, 30, 0, 0, loc)),
	})

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/ready", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected fallback readiness to succeed, got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		OK    bool `json:"ok"`
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode ready payload: %v", err)
	}
	if !payload.OK || !payload.Ready {
		t.Fatalf("unexpected fallback readiness payload: %+v", payload)
	}
}

func TestServeHTTPHealthAndReadyStayMinimalForStaleScheduleAfterCutoff(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	loadAt := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	now := time.Date(2026, 2, 28, 4, 0, 0, 0, loc)
	server, _ := newPublicDataServerWithLoadedSnapshot(t, "https://example.test/pixel-stack/train", now, loadAt, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-stale", loadAt.Format("2006-01-02"), "Riga", "Jelgava", time.Date(2026, 2, 28, 1, 30, 0, 0, loc)),
	})

	healthReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/health", nil)
	healthRes := httptest.NewRecorder()
	server.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("expected liveness endpoint to stay up, got %d body=%s", healthRes.Code, healthRes.Body.String())
	}

	var healthPayload map[string]any
	if err := json.Unmarshal(healthRes.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if len(healthPayload) != 1 || healthPayload["ok"] != true {
		t.Fatalf("expected minimal health payload, got %+v", healthPayload)
	}

	readyReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/ready", nil)
	readyRes := httptest.NewRecorder()
	server.ServeHTTP(readyRes, readyReq)
	if readyRes.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected stale after-cutoff readiness to fail, got %d body=%s", readyRes.Code, readyRes.Body.String())
	}
	var readyPayload struct {
		OK    bool `json:"ok"`
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(readyRes.Body.Bytes(), &readyPayload); err != nil {
		t.Fatalf("decode ready payload: %v", err)
	}
	if readyPayload.OK || readyPayload.Ready {
		t.Fatalf("expected minimal failed readiness payload, got %+v", readyPayload)
	}
}

func TestServeHTTPInternalHealthExposesReleaseIdentityOnlyLocally(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, loc)
	server, _ := newPublicDataServerWithLoadedSnapshot(t, "https://example.test/pixel-stack/train", now, now.Add(-time.Hour), nil)
	server.release.Commit = "fedcba654321"
	server.release.Dirty = "clean"
	server.release.ReleaseID = "release-20260511T120000Z"
	server.release.SourceSHA256 = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"

	publicReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/internal/health", nil)
	publicReq.RemoteAddr = "203.0.113.10:48123"
	publicRes := httptest.NewRecorder()
	server.ServeHTTP(publicRes, publicReq)
	if publicRes.Code != http.StatusNotFound {
		t.Fatalf("public internal health status = %d, want 404", publicRes.Code)
	}

	localReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/internal/health", nil)
	localReq.RemoteAddr = "127.0.0.1:48123"
	localRes := httptest.NewRecorder()
	server.ServeHTTP(localRes, localReq)
	if localRes.Code != http.StatusOK {
		t.Fatalf("local internal health status = %d, want 200 body=%s", localRes.Code, localRes.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(localRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode local internal health payload: %v", err)
	}
	versionPayload, ok := payload["version"].(map[string]any)
	if !ok {
		t.Fatalf("local internal health missing version payload: %#v", payload)
	}
	if versionPayload["commit"] != "fedcba654321" {
		t.Fatalf("version.commit = %#v, want fedcba654321", versionPayload["commit"])
	}
	if versionPayload["dirty"] != "clean" {
		t.Fatalf("version.dirty = %#v, want clean", versionPayload["dirty"])
	}
	if versionPayload["releaseId"] != "release-20260511T120000Z" {
		t.Fatalf("version.releaseId = %#v, want release-20260511T120000Z", versionPayload["releaseId"])
	}
	if versionPayload["sourceSha256"] != "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b" {
		t.Fatalf("version.sourceSha256 = %#v, want release source hash", versionPayload["sourceSha256"])
	}
}

func publicFloatPtr(v float64) *float64 {
	return &v
}

func stableRigaMidday(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
}

func storeStationSighting(id string, stationID string, destinationStationID *string, matchedTrainID *string, userID int64, createdAt time.Time) domain.StationSighting {
	return domain.StationSighting{
		ID:                     id,
		StationID:              stationID,
		DestinationStationID:   destinationStationID,
		MatchedTrainInstanceID: matchedTrainID,
		UserID:                 userID,
		CreatedAt:              createdAt,
	}
}
