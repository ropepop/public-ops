package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicEdgeCachePersistsVersionsAcrossReloads(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "public-edge-cache.json")
	cache, err := newPublicEdgeCache(statePath, 2592000, "release-seed")
	if err != nil {
		t.Fatalf("newPublicEdgeCache: %v", err)
	}
	cache.syncLoadedServiceDate("2026-03-26")
	cache.noteReportAccepted("train-1", "incident-1")

	wantDashboard := cache.versionFor(publicEdgeCacheDashboardRoute(0))
	wantTrain := cache.versionFor(publicEdgeCacheTrainRoute("train-1"))
	wantIncident := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1"))

	reloaded, err := newPublicEdgeCache(statePath, 2592000, "release-seed")
	if err != nil {
		t.Fatalf("reload public edge cache: %v", err)
	}

	if got := reloaded.versionFor(publicEdgeCacheDashboardRoute(0)); got != wantDashboard {
		t.Fatalf("dashboard version mismatch after reload: got %q want %q", got, wantDashboard)
	}
	if got := reloaded.versionFor(publicEdgeCacheTrainRoute("train-1")); got != wantTrain {
		t.Fatalf("train version mismatch after reload: got %q want %q", got, wantTrain)
	}
	if got := reloaded.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1")); got != wantIncident {
		t.Fatalf("incident version mismatch after reload: got %q want %q", got, wantIncident)
	}
}

func TestPublicEdgeCacheReportBumpOnlyTouchesRelatedSlices(t *testing.T) {
	t.Parallel()

	cache, err := newPublicEdgeCache(filepath.Join(t.TempDir(), "public-edge-cache.json"), 2592000, "release-seed")
	if err != nil {
		t.Fatalf("newPublicEdgeCache: %v", err)
	}
	cache.syncLoadedServiceDate("2026-03-26")

	beforeDashboard := cache.versionFor(publicEdgeCacheDashboardRoute(0))
	beforeNetworkMap := cache.versionFor(publicEdgeCacheNetworkMapRoute())
	beforeStationSearch := cache.versionFor(publicEdgeCacheStationSearchRoute("ri"))
	beforeStationDepartures := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga"))
	beforeTrain := cache.versionFor(publicEdgeCacheTrainRoute("train-1"))
	beforeIncident := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1"))
	beforeIncidents := cache.versionFor(publicEdgeCacheIncidentsRoute(0))

	cache.noteReportAccepted("train-1", "incident-1")

	if got := cache.versionFor(publicEdgeCacheDashboardRoute(0)); got == beforeDashboard {
		t.Fatalf("dashboard version did not change after report bump")
	}
	if got := cache.versionFor(publicEdgeCacheTrainRoute("train-1")); got == beforeTrain {
		t.Fatalf("train version did not change after report bump")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1")); got == beforeIncident {
		t.Fatalf("incident detail version did not change after report bump")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentsRoute(0)); got == beforeIncidents {
		t.Fatalf("incidents list version did not change after report bump")
	}
	if got := cache.versionFor(publicEdgeCacheNetworkMapRoute()); got != beforeNetworkMap {
		t.Fatalf("network map version changed unexpectedly after report bump: got %q want %q", got, beforeNetworkMap)
	}
	if got := cache.versionFor(publicEdgeCacheStationSearchRoute("ri")); got != beforeStationSearch {
		t.Fatalf("station search version changed unexpectedly after report bump: got %q want %q", got, beforeStationSearch)
	}
	if got := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga")); got != beforeStationDepartures {
		t.Fatalf("station departures version changed unexpectedly after report bump: got %q want %q", got, beforeStationDepartures)
	}
}

func TestPublicEdgeCacheStationSightingBumpLeavesDashboardStable(t *testing.T) {
	t.Parallel()

	cache, err := newPublicEdgeCache(filepath.Join(t.TempDir(), "public-edge-cache.json"), 2592000, "release-seed")
	if err != nil {
		t.Fatalf("newPublicEdgeCache: %v", err)
	}
	cache.syncLoadedServiceDate("2026-03-26")

	beforeDashboard := cache.versionFor(publicEdgeCacheDashboardRoute(0))
	beforeNetworkMap := cache.versionFor(publicEdgeCacheNetworkMapRoute())
	beforeStationDepartures := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga"))
	beforeTrain := cache.versionFor(publicEdgeCacheTrainRoute("train-1"))
	beforeIncident := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-2"))
	beforeIncidents := cache.versionFor(publicEdgeCacheIncidentsRoute(0))

	cache.noteStationSightingAccepted("riga", "train-1", "incident-2")

	if got := cache.versionFor(publicEdgeCacheDashboardRoute(0)); got != beforeDashboard {
		t.Fatalf("dashboard version changed unexpectedly after station sighting: got %q want %q", got, beforeDashboard)
	}
	if got := cache.versionFor(publicEdgeCacheNetworkMapRoute()); got == beforeNetworkMap {
		t.Fatalf("network map version did not change after station sighting")
	}
	if got := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga")); got == beforeStationDepartures {
		t.Fatalf("station departures version did not change after station sighting")
	}
	if got := cache.versionFor(publicEdgeCacheTrainRoute("train-1")); got == beforeTrain {
		t.Fatalf("matched train version did not change after station sighting")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-2")); got == beforeIncident {
		t.Fatalf("incident detail version did not change after station sighting")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentsRoute(0)); got == beforeIncidents {
		t.Fatalf("incidents list version did not change after station sighting")
	}
}

func TestPublicEdgeCacheServiceDayChangeClearsEntityVersions(t *testing.T) {
	t.Parallel()

	cache, err := newPublicEdgeCache(filepath.Join(t.TempDir(), "public-edge-cache.json"), 2592000, "release-seed")
	if err != nil {
		t.Fatalf("newPublicEdgeCache: %v", err)
	}
	cache.syncLoadedServiceDate("2026-03-26")
	cache.noteReportAccepted("train-1", "incident-1")
	cache.noteStationSightingAccepted("riga", "train-1", "incident-2")

	beforeTrain := cache.versionFor(publicEdgeCacheTrainRoute("train-1"))
	beforeStation := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga"))
	beforeIncident := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1"))
	beforeIncidents := cache.versionFor(publicEdgeCacheIncidentsRoute(0))

	cache.syncLoadedServiceDate("2026-03-27")

	if got := cache.versionFor(publicEdgeCacheTrainRoute("train-1")); got == beforeTrain {
		t.Fatalf("train version did not change after service-day change")
	}
	if got := cache.versionFor(publicEdgeCacheStationDeparturesRoute("riga")); got == beforeStation {
		t.Fatalf("station version did not change after service-day change")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentDetailRoute("incident-1")); got == beforeIncident {
		t.Fatalf("incident detail version did not change after service-day change")
	}
	if got := cache.versionFor(publicEdgeCacheIncidentsRoute(0)); got == beforeIncidents {
		t.Fatalf("incidents list version did not change after service-day change")
	}
}

func TestServeHTTPPublicEdgeCacheRedirectsStableDashboardRequests(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	enablePublicEdgeCacheForTest(t, server)

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusTemporaryRedirect {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	location := res.Header().Get("Location")
	if !strings.Contains(location, "cv=") {
		t.Fatalf("expected cache version redirect, got %q", location)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Cache-Mode"); got != cacheModeRedirect {
		t.Fatalf("unexpected cache mode: %q", got)
	}
}

func TestServeHTTPPublicEdgeCacheServesVersionedDashboardWithImmutableHeaders(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	enablePublicEdgeCacheForTest(t, server)

	redirectReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	redirectRes := httptest.NewRecorder()
	server.ServeHTTP(redirectRes, redirectReq)
	location := redirectRes.Header().Get("Location")

	req := httptest.NewRequest(http.MethodGet, location, nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "public, max-age=2592000, immutable" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Cache-Mode"); got != cacheModeHit {
		t.Fatalf("unexpected cache mode: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Cache-Slice"); got != "public-dashboard" {
		t.Fatalf("unexpected cache slice: %q", got)
	}
}

func TestServeHTTPPublicIncidentsSignedInResponseSkipsEdgeCacheHeaders(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	enablePublicEdgeCacheForTest(t, server)
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           41,
			LanguageCode: "en",
		},
	}, now)
	if err != nil {
		t.Fatalf("issue session cookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/incidents?limit=0", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != "" {
		t.Fatalf("expected no redirect for signed-in public incidents, got %q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Cache-Mode"); got != cacheModeBypass {
		t.Fatalf("expected signed-in incidents to bypass public edge cache, got %q", got)
	}
}

func TestServeHTTPPublicEdgeCacheRedirectsStaleDashboardVersionAfterReportBump(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	enablePublicEdgeCacheForTest(t, server)

	initialReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	initialRes := httptest.NewRecorder()
	server.ServeHTTP(initialRes, initialReq)
	staleLocation := initialRes.Header().Get("Location")

	server.publicEdgeCache.noteReportAccepted("train-next-0", "incident-train-next-0")

	staleReq := httptest.NewRequest(http.MethodGet, staleLocation, nil)
	staleRes := httptest.NewRecorder()
	server.ServeHTTP(staleRes, staleReq)

	if staleRes.Code != http.StatusTemporaryRedirect {
		t.Fatalf("unexpected status: got %d body=%s", staleRes.Code, staleRes.Body.String())
	}
	if got := staleRes.Header().Get("Location"); got == staleLocation {
		t.Fatalf("expected stale cache version redirect to change, got %q", got)
	}
}

func enablePublicEdgeCacheForTest(t *testing.T, server *Server) {
	t.Helper()

	cache, err := newPublicEdgeCache(filepath.Join(t.TempDir(), "public-edge-cache.json"), 2592000, server.release.AppJSHash)
	if err != nil {
		t.Fatalf("newPublicEdgeCache: %v", err)
	}
	server.cfg.TrainWebPublicEdgeCacheEnabled = true
	server.cfg.TrainWebPublicEdgeCacheTTLSec = 2592000
	server.publicEdgeCache = cache
}
