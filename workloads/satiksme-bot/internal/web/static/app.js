(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory(root);
    return;
  }
  root.SatiksmeApp = factory(root);
})(typeof globalThis !== "undefined" ? globalThis : this, function (root) {
  "use strict";

  var defaultCenter = { lat: 56.9496, lng: 24.1052, zoom: 13 };
  var config = (root.window && root.window.SATIKSME_APP_CONFIG) || root.SATIKSME_APP_CONFIG || {};
  var incidentMobileBreakpointPx = 980;
  var incidentOverlayHistoryKey = "__satiksmeIncidentOverlay";
  function createInitialState() {
    return {
      catalog: null,
      stopIndex: null,
      stopActivityCounts: null,
      sightings: { stopSightings: [], vehicleSightings: [] },
      stopIncidents: [],
      vehicleIncidents: [],
      transportSnapshotVehicles: [],
      vehicles: [],
      selectedStop: null,
      selectedVehicleId: "",
      selectedVehicleMarkerKey: "",
      vehicleFollowPaused: false,
      focusedMapEntity: null,
      openMapDetailEntity: null,
      mapDetailDismissSuppressedUntil: 0,
      publicIncidents: [],
      publicIncidentsLoaded: false,
      publicIncidentsLoading: false,
      publicIncidentDetail: null,
      publicIncidentDetailLoading: false,
      publicIncidentDetailLoadingId: "",
      publicIncidentSelectedId: "",
      publicIncidentDetailOpen: false,
      publicIncidentHistoryOpen: false,
      publicIncidentHistoryNavigating: false,
      publicIncidentListScrollY: 0,
      publicIncidentMobileLayout: false,
      publicIncidentCommentDrafts: {},
      publicIncidentVoteSelections: {},
      authenticated: false,
      authState: "unknown",
      authFeedback: null,
      authInProgress: false,
      spacetimeAuth: null,
      publicMapLoaded: false,
      publicMapLoading: false,
      liveTransportClient: null,
      liveTransportSnapshotState: null,
      liveTransportVersion: "",
      liveTransportHeartbeatTimer: null,
      liveTransportHeartbeatInFlight: false,
      liveTransportHeartbeatSessionId: "",
      liveTransportRealtimeStarted: false,
      liveTransportRealtimeListenerBound: false,
      liveTransportVisibilityBound: false,
      incidentFeedRefreshTimer: null,
      map: null,
      markers: new Map(),
      vehicleMarkers: new Map(),
      userLocationMarker: null,
      userLocationControl: null,
      locationRequestInFlight: null,
      stopIconCache: new Map(),
      vehicleIconCache: new Map(),
      currentPosition: null,
      liveVehiclesRefreshTimer: null,
      liveRefreshInFlight: false,
      mapProgrammaticViewUntil: 0,
      mapGestureStartCenter: null,
      mapGestureStartZoom: null,
      mapGestureZoomPending: false,
      mapViewportWidth: 0,
      mapViewportHeight: 0,
      mapViewportSyncFrame: 0,
      mapViewportSyncForce: false,
      mapViewportResizeObserver: null,
      telegramViewportSyncBound: false,
      telegramViewportChangeHandler: null,
    };
  }

  function resetStateForTest(overrides) {
    if (state.liveTransportHeartbeatTimer) {
      clearInterval(state.liveTransportHeartbeatTimer);
    }
    if (state.incidentFeedRefreshTimer) {
      clearInterval(state.incidentFeedRefreshTimer);
    }
    var nextState = createInitialState();
    Object.keys(state).forEach(function (key) {
      delete state[key];
    });
    Object.assign(state, nextState, overrides || {});
    if (!(state.markers instanceof Map)) {
      state.markers = new Map();
    }
    if (!(state.vehicleMarkers instanceof Map)) {
      state.vehicleMarkers = new Map();
    }
    if (!(state.stopIconCache instanceof Map)) {
      state.stopIconCache = new Map();
    }
    if (!(state.vehicleIconCache instanceof Map)) {
      state.vehicleIconCache = new Map();
    }
    if (!state.sightings || typeof state.sightings !== "object") {
      state.sightings = { stopSightings: [], vehicleSightings: [] };
    }
    if (!Array.isArray(state.sightings.stopSightings)) {
      state.sightings.stopSightings = [];
    }
    if (!Array.isArray(state.sightings.vehicleSightings)) {
      state.sightings.vehicleSightings = [];
    }
    if (!Array.isArray(state.stopIncidents)) {
      state.stopIncidents = [];
    }
    if (!Array.isArray(state.vehicleIncidents)) {
      state.vehicleIncidents = [];
    }
    if (!Array.isArray(state.transportSnapshotVehicles)) {
      state.transportSnapshotVehicles = [];
    }
    if (!state.publicIncidentVoteSelections || typeof state.publicIncidentVoteSelections !== "object") {
      state.publicIncidentVoteSelections = {};
    }
    if (!state.stopIndex) {
      state.stopIndex = createStopIndex(state.catalog && state.catalog.stops);
    }
    syncStopActivityCounts();
  }

  var state = createInitialState();

  var sightingsFetchLimit = 24;
  var liveMapRefreshMs = 15000;
  var liveTransportHeartbeatMs = 30000;
  var activityOnlyStopVisibilityHeightMeters = 2000;
  var liveVehicleAnimationMinMs = 300;
  var liveVehicleCompactMarkerDefaultSizePx = 14;
  var liveVehicleCompactMarkerShrunkSizePx = 5;
  var liveVehicleCompactMarkerShrinkHeightMeters = 3000;
  var stopBadgeScaleMax = 3;
  var stopBadgeScaleMaxHeightMeters = 3000;
  var stopBadgeScaleMinHeightMeters = 100;
  var stopMarkerRadiusMin = 2.33;
  var stopMarkerRadiusMax = 15;
  var stopMarkerRadiusMinHeightMeters = 1000;
  var stopMarkerRadiusMaxHeightMeters = 50;
  var mapDetailDismissSuppressWindowMs = 450;
  var mapUserPanTolerancePx = 8;
  var mapDetailOverlayClampPaddingPx = 12;
  var actionsBound = false;
  function pathFor(path) {
    var base = String(config.basePath || "").replace(/\/$/, "");
    if (!base) {
      return path;
    }
    return base + path;
  }

  function windowHandle() {
    return root.window || root;
  }

  function telegramWebApp() {
    var win = windowHandle();
    return win && win.Telegram && win.Telegram.WebApp ? win.Telegram.WebApp : null;
  }

  function telegramMiniAppInitData() {
    var webApp = telegramWebApp();
    var initData = webApp && typeof webApp.initData === "string" ? webApp.initData : "";
    return String(initData || "").trim();
  }

  function notifyTelegramMiniAppReady() {
    var webApp = telegramWebApp();
    if (!webApp || typeof webApp.ready !== "function") {
      return;
    }
    try {
      webApp.ready();
    } catch (_error) {
      return;
    }
  }

  function currentWindowWidth() {
    var win = windowHandle();
    if (win && typeof win.innerWidth === "number" && win.innerWidth > 0) {
      return win.innerWidth;
    }
    if (root && typeof root.innerWidth === "number" && root.innerWidth > 0) {
      return root.innerWidth;
    }
    return incidentMobileBreakpointPx + 1;
  }

  function isIncidentMobileLayout() {
    var win = windowHandle();
    if (win && typeof win.matchMedia === "function") {
      return !!win.matchMedia("(max-width: " + incidentMobileBreakpointPx + "px)").matches;
    }
    if (typeof root.matchMedia === "function") {
      return !!root.matchMedia("(max-width: " + incidentMobileBreakpointPx + "px)").matches;
    }
    return currentWindowWidth() <= incidentMobileBreakpointPx;
  }

  function shouldOpenMapDetailImmediately() {
    return isIncidentMobileLayout();
  }

  function currentPageScrollY() {
    var win = windowHandle();
    if (win && typeof win.scrollY === "number") {
      return win.scrollY;
    }
    if (win && typeof win.pageYOffset === "number") {
      return win.pageYOffset;
    }
    if (typeof root.pageYOffset === "number") {
      return root.pageYOffset;
    }
    return 0;
  }

  function setPageScrollY(value) {
    var win = windowHandle();
    var nextValue = Math.max(0, Number(value) || 0);
    if (win && typeof win.scrollTo === "function") {
      win.scrollTo(0, nextValue);
      return;
    }
    if (win) {
      win.scrollY = nextValue;
      win.pageYOffset = nextValue;
    }
    root.pageYOffset = nextValue;
  }

  function incidentOverlayHistoryState() {
    var value = {};
    value[incidentOverlayHistoryKey] = true;
    return value;
  }

  function isIncidentOverlayHistoryState(value) {
    return !!(value && value[incidentOverlayHistoryKey]);
  }

  function locationHandle() {
    var win = windowHandle();
    return (win && win.location) || root.location || null;
  }

  function currentURL() {
    var location = locationHandle();
    if (!location) {
      return null;
    }
    try {
      return new URL(location.href || (location.pathname || "/"), location.origin || "https://satiksme.invalid");
    } catch (_error) {
      return null;
    }
  }

  function selectedIncidentIdFromURL() {
    var url = currentURL();
    if (!url) {
      return "";
    }
    return String(url.searchParams.get("incident") || "").trim();
  }

  function syncIncidentURL(incidentId) {
    var win = windowHandle();
    var url = currentURL();
    if (!win.history || typeof win.history.replaceState !== "function" || !url) {
      return;
    }
    if (incidentId) {
      url.searchParams.set("incident", String(incidentId).trim());
    } else {
      url.searchParams.delete("incident");
    }
    try {
      win.history.replaceState(win.history.state || null, "", url.pathname + url.search + url.hash);
    } catch (_error) {
      return;
    }
  }

  function fetchJSON(url, options) {
    var requestOptions = Object.assign({}, options || {});
    if (!requestOptions.credentials) {
      requestOptions.credentials = "include";
    }
    return fetch(url, requestOptions).then(function (response) {
      return response.text().then(function (raw) {
        var body = raw ? JSON.parse(raw) : {};
        if (!response.ok) {
          var error = new Error(body.error || ("HTTP " + response.status));
          error.status = response.status;
          throw error;
        }
        return body;
      });
    });
  }

  function spacetimeEnabled() {
    return Boolean(config.spacetimeEnabled && config.spacetimeHost && config.spacetimeDatabase);
  }

  function spacetimeConnectionConfigured() {
    return Boolean(config.spacetimeHost && config.spacetimeDatabase);
  }

  function spacetimeDirectOnlyEnabled() {
    return Boolean(config.spacetimeDirectOnly);
  }

  function liveTransportSnapshotLookupEnabled() {
    return Boolean(config.liveTransportSnapshotLookupEnabled && spacetimeConnectionConfigured());
  }

  function normalizeSpacetimeSession(value) {
    if (!value || typeof value !== "object") {
      return null;
    }
    if (typeof value.token !== "string" || !value.token) {
      return null;
    }
    if (typeof value.expiresAt === "string" && value.expiresAt) {
      var expiresAt = new Date(value.expiresAt);
      if (!Number.isFinite(expiresAt.getTime()) || expiresAt.getTime() <= Date.now()) {
        return null;
      }
    }
    return {
      token: String(value.token),
      expiresAt: String(value.expiresAt || ""),
    };
  }

  function persistSpacetimeSession(value) {
    state.spacetimeAuth = normalizeSpacetimeSession(value);
    return state.spacetimeAuth;
  }

  function currentSpacetimeSession() {
    state.spacetimeAuth = normalizeSpacetimeSession(state.spacetimeAuth);
    return state.spacetimeAuth;
  }

  function canonicalSpacetimeProcedureName(name) {
    var clean = String(name || "").trim();
    if (!clean) {
      return "";
    }
    return clean.indexOf("satiksmebot_") === 0 ? clean : "satiksmebot_" + clean;
  }

  function decodeSpacetimePayload(raw) {
    var payload = raw ? JSON.parse(raw) : null;
    if (typeof payload === "string" && payload) {
      return JSON.parse(payload);
    }
    return payload || {};
  }

  function displayStopName(stop) {
    var name = String((stop && stop.name) || "").trim();
    var id = String((stop && stop.id) || "").trim();
    if (!name || name === "0") {
      return id ? "Pietura " + id : "Pietura";
    }
    return name;
  }

  function bundleURLFor(path) {
    var value = String(path || "").trim();
    if (!value) {
      return "";
    }
    if (/^https?:\/\//i.test(value)) {
      return value;
    }
    if (value.charAt(0) === "/") {
      return value;
    }
    return pathFor("/" + value);
  }

  function activeBundleIdentity() {
    if (!state.catalog || typeof state.catalog !== "object") {
      return { version: "", generatedAt: "" };
    }
    return {
      version: String(state.catalog.bundleVersion || ""),
      generatedAt: String(state.catalog.bundleGeneratedAt || state.catalog.generatedAt || ""),
    };
  }

  function callSpacetimeProcedureWithMode(name, args, options, requireDirectData) {
    var settings = options || {};
    var candidate = canonicalSpacetimeProcedureName(name);
    var session = currentSpacetimeSession();
    if (requireDirectData ? !spacetimeEnabled() : !spacetimeConnectionConfigured()) {
      return Promise.reject(new Error("Spacetime nav pieejams"));
    }
    if (!settings.allowAnonymous && !state.authenticated) {
      return Promise.reject(new Error("Nepieciešama Telegram sesija"));
    }
    if (!candidate) {
      return Promise.reject(new Error("Spacetime procedūra nav pieejama"));
    }
    var headers = { "Content-Type": "application/json" };
    if (session && session.token) {
      headers.Authorization = "Bearer " + session.token;
    }
    return fetch(
      String(config.spacetimeHost).replace(/\/+$/, "") +
        "/v1/database/" +
        encodeURIComponent(String(config.spacetimeDatabase)) +
        "/call/" +
        encodeURIComponent(candidate),
      {
        method: "POST",
        headers: headers,
        keepalive: settings.keepalive === true,
        body: JSON.stringify(Array.isArray(args) ? args : []),
      }
    ).then(function (response) {
      return response.text().then(function (raw) {
        if (!response.ok) {
          if (response.status === 401 && !settings.retriedAuth) {
            return refreshSpacetimeSession().then(function (payload) {
              if (!payload && !settings.allowAnonymous) {
                throw new Error("Nepieciešama Telegram sesija");
              }
              return callSpacetimeProcedureWithMode(name, args, Object.assign({}, settings, { retriedAuth: true }), requireDirectData);
            });
          }
          var message = raw;
          try {
            message = JSON.parse(raw).error || raw;
          } catch (_error) {
            message = raw;
          }
          throw new Error(String(message || ("HTTP " + response.status)));
        }
        return decodeSpacetimePayload(raw);
      });
    });
  }

  function callSpacetimeProcedure(name, args, options) {
    return callSpacetimeProcedureWithMode(name, args, options, true);
  }

  function callSpacetimePublicProcedure(name, args, options) {
    return callSpacetimeProcedureWithMode(name, args, Object.assign({ allowAnonymous: true }, options || {}), false);
  }

  function liveTransportRealtimeEnabled() {
    return Boolean(config.liveTransportRealtimeEnabled && spacetimeEnabled());
  }

  function liveTransportClientAvailable() {
    return Boolean(
      (liveTransportRealtimeEnabled() || liveTransportSnapshotLookupEnabled()) &&
      root.SatiksmeLiveClient &&
      typeof root.SatiksmeLiveClient.create === "function"
    );
  }

  function liveTransportSpacetimeControlEnabled() {
    return Boolean((liveTransportRealtimeEnabled() || liveTransportSnapshotLookupEnabled()) && liveTransportPageEnabled());
  }

  function liveTransportPageEnabled() {
    return String(config.mode || "public") !== "public-incidents";
  }

  function documentVisible() {
    return !(root.document && root.document.hidden === true);
  }

  function generateLiveTransportViewerSessionId() {
    if (root.crypto && typeof root.crypto.randomUUID === "function") {
      return String(root.crypto.randomUUID());
    }
    return "viewer-" + Date.now() + "-" + Math.round(Math.random() * 1000000);
  }

  function normalizeVehicleDirection(value) {
    return String(value || "").trim().replace(/>/g, "-");
  }

  function secondsDistance(left, right) {
    var diff = Math.abs((Number(left) || 0) - (Number(right) || 0));
    var day = 24 * 3600;
    if (diff > day / 2) {
      diff = day - diff;
    }
    return diff;
  }

  function vehicleIncidentsForMap(items) {
    return (Array.isArray(items) ? items : []).filter(function (item) {
      return item && item.scope === "vehicle" && item.resolved !== true;
    });
  }

  function stopIncidentsForMap(items) {
    return (Array.isArray(items) ? items : []).filter(function (item) {
      return item && item.scope === "stop" && item.resolved !== true;
    });
  }

  function bestVehicleMatch(vehicles, sighting) {
    var bestIndex = -1;
    var bestScore = Number.MAX_SAFE_INTEGER;
    var mode = String(sighting && sighting.mode || "").trim().toLowerCase();
    var routeLabel = String(sighting && sighting.routeLabel || "").trim();
    var direction = normalizeVehicleDirection(sighting && sighting.direction);
    (vehicles || []).forEach(function (vehicle, index) {
      var score = 0;
      var vehicleDirection = "";
      var stopId = "";
      if (!vehicle || String(vehicle.mode || "").trim().toLowerCase() !== mode || String(vehicle.routeLabel || "").trim() !== routeLabel) {
        return;
      }
      stopId = String(sighting && sighting.stopId || "").trim();
      if (stopId) {
        if (normalizeStopKey(String(vehicle.stopId || "").trim()) === normalizeStopKey(stopId)) {
          score += 0;
        } else if (!String(vehicle.stopId || "").trim()) {
          score += 40;
        } else {
          return;
        }
      }
      vehicleDirection = normalizeVehicleDirection(vehicle.direction);
      if (!direction || !vehicleDirection) {
        score += 10;
      } else if (direction !== vehicleDirection) {
        return;
      }
      if ((Number(vehicle.arrivalSeconds) || 0) > 0 && (Number(sighting && sighting.departureSeconds) || 0) > 0) {
        score += secondsDistance(vehicle.arrivalSeconds, sighting.departureSeconds);
      } else {
        score += 300;
      }
      if (bestIndex === -1 || score < bestScore) {
        bestIndex = index;
        bestScore = score;
      }
    });
    return bestIndex;
  }

  function cloneLiveVehicle(vehicle) {
    return Object.assign({}, vehicle || {}, {
      incidents: Array.isArray(vehicle && vehicle.incidents) ? vehicle.incidents.slice() : [],
      sightingCount: 0,
    });
  }

  function mergeLiveVehiclesWithSharedState(vehicles, sightings, incidents) {
    var merged = (vehicles || []).map(cloneLiveVehicle);
    (sightings && sightings.vehicleSightings || []).forEach(function (item) {
      var index = bestVehicleMatch(merged, item || {});
      if (index >= 0) {
        merged[index].sightingCount = (Number(merged[index].sightingCount) || 0) + 1;
      }
    });
    merged.forEach(function (vehicle) {
      vehicle.incidents = [];
    });
    vehicleIncidentsForMap(incidents).forEach(function (item) {
      var match = bestVehicleMatch(merged, {
        stopId: item && item.vehicle ? item.vehicle.stopId : "",
        mode: item && item.vehicle ? item.vehicle.mode : "",
        routeLabel: item && item.vehicle ? item.vehicle.routeLabel : "",
        direction: item && item.vehicle ? item.vehicle.direction : "",
        destination: item && item.vehicle ? item.vehicle.destination : "",
        departureSeconds: item && item.vehicle ? item.vehicle.departureSeconds : 0,
        liveRowId: item && item.vehicle ? item.vehicle.liveRowId : "",
      });
      if (match >= 0) {
        merged[match].incidents = (merged[match].incidents || []).concat([item]);
      }
    });
    merged.forEach(function (vehicle) {
      vehicle.incidents = (vehicle.incidents || []).slice().sort(function (left, right) {
        return new Date(right && right.lastReportAt || 0).getTime() - new Date(left && left.lastReportAt || 0).getTime();
      });
    });
    return merged;
  }

  function rebuildMergedLiveVehicles() {
    state.vehicles = mergeLiveVehiclesWithSharedState(
      state.transportSnapshotVehicles,
      state.sightings,
      state.vehicleIncidents
    );
    renderLiveVehicles();
    applySelectedVehicleFollow();
    return state.vehicles;
  }

  function applySharedMapCollections(sightings, incidents) {
    state.sightings = sightings || { stopSightings: [], vehicleSightings: [] };
    state.stopIncidents = stopIncidentsForMap(incidents);
    state.vehicleIncidents = vehicleIncidentsForMap(incidents);
    markPublicMapLoaded();
    syncStopActivityCounts();
    renderVisibleStops();
    rebuildMergedLiveVehicles();
    renderSightings();
    renderSelectedStop();
  }

  function applyLiveTransportSnapshotPayload(payload) {
    state.transportSnapshotVehicles = Array.isArray(payload && payload.vehicles) ? payload.vehicles.slice() : [];
    rebuildMergedLiveVehicles();
    return payload;
  }

  function normalizeLiveTransportSnapshotState(value) {
    if (!value || typeof value !== "object") {
      return null;
    }
    return {
      feed: String(value.feed || "").trim(),
      version: String(value.version || "").trim(),
      path: String(value.path || "").trim(),
      hash: String(value.hash || "").trim(),
      publishedAt: String(value.publishedAt || "").trim(),
      lastSuccessAt: String(value.lastSuccessAt || "").trim(),
      lastAttemptAt: String(value.lastAttemptAt || "").trim(),
      status: String(value.status || "").trim(),
      consecutiveFailures: Number(value.consecutiveFailures) || 0,
      vehicleCount: Number(value.vehicleCount) || 0,
    };
  }

  function liveTransportClient() {
    if (!liveTransportClientAvailable()) {
      return null;
    }
    if (!state.liveTransportClient) {
      state.liveTransportClient = root.SatiksmeLiveClient.create({
        host: config.spacetimeHost,
        database: config.spacetimeDatabase,
        pageMode: currentLiveTransportClientPageMode(),
      });
    }
    syncLiveTransportClientScope();
    return state.liveTransportClient;
  }

  function currentLiveTransportClientPageMode() {
    return liveTransportPageEnabled() ? "map" : "incidents";
  }

  function currentIncidentSubscriptionTarget() {
    if (currentLiveTransportClientPageMode() !== "incidents") {
      return "";
    }
    return String(state.publicIncidentSelectedId || selectedIncidentIdFromURL() || "").trim();
  }

  function syncLiveTransportClientScope() {
    var client = state.liveTransportClient;
    if (!client) {
      return null;
    }
    if (typeof client.setPageMode === "function") {
      client.setPageMode(currentLiveTransportClientPageMode());
    }
    if (typeof client.setIncidentDetailTarget === "function") {
      client.setIncidentDetailTarget(currentIncidentSubscriptionTarget());
    }
    return client;
  }

  function loadLiveTransportSnapshotByState(snapshotState) {
    var normalized = normalizeLiveTransportSnapshotState(snapshotState);
    var needsFetch = false;
    if (!normalized || !normalized.path) {
      return Promise.resolve(null);
    }
    state.liveTransportSnapshotState = normalized;
    needsFetch = normalized.version !== String(state.liveTransportVersion || "") || !state.transportSnapshotVehicles.length;
    if (!needsFetch) {
      return Promise.resolve(normalized);
    }
    return fetchJSON(bundleURLFor(normalized.path), { credentials: "omit" }).then(function (payload) {
      state.liveTransportVersion = normalized.version;
      applyLiveTransportSnapshotPayload(payload);
      return normalized;
    });
  }

  function syncLiveTransportRealtimeState() {
    var client = syncLiveTransportClientScope() || liveTransportClient();
    if (!client || typeof client.currentSnapshotState !== "function") {
      return Promise.resolve(null);
    }
    return loadLiveTransportSnapshotByState(client.currentSnapshotState()).catch(function () {
      setStatus("Tiešraides transports nav pieejams");
      return null;
    });
  }

  function ensureLiveTransportRealtimeStarted() {
    var client = liveTransportClient();
    var connectionState = "";
    if (!client) {
      return Promise.resolve(false);
    }
    bindLiveTransportLifecycleEvents();
    if (!state.liveTransportRealtimeListenerBound && typeof client.onInvalidate === "function") {
      state.liveTransportRealtimeListenerBound = true;
      client.onInvalidate(function () {
        handleSpacetimeRealtimeInvalidate();
      });
    }
    if (typeof client.getConnectionState === "function") {
      connectionState = String(client.getConnectionState() || "");
    }
    if (state.liveTransportRealtimeStarted && connectionState && connectionState !== "idle" && connectionState !== "offline") {
      return Promise.resolve(true);
    }
    state.liveTransportRealtimeStarted = true;
    return client.connect(currentSpacetimeSession()).then(function () {
      return syncLiveTransportRealtimeState().then(function () {
        return true;
      });
    }).catch(function () {
      setStatus("Tiešraides transports nav pieejams");
      return false;
    });
  }

  function currentPublicIncidentVoteSelections() {
    if (!state.publicIncidentVoteSelections || typeof state.publicIncidentVoteSelections !== "object") {
      state.publicIncidentVoteSelections = {};
    }
    return state.publicIncidentVoteSelections;
  }

  function currentSpacetimeSharedMapSnapshot() {
    var client = syncLiveTransportClientScope() || liveTransportClient();
    var connectionState = "";
    if (!client || typeof client.currentSharedMapState !== "function") {
      return null;
    }
    if (typeof client.getConnectionState === "function") {
      connectionState = String(client.getConnectionState() || "").trim().toLowerCase();
      if (connectionState && connectionState !== "live") {
        return null;
      }
    }
    return client.currentSharedMapState(sightingsFetchLimit, currentPublicIncidentVoteSelections());
  }

  function currentSpacetimeIncidentList() {
    var client = syncLiveTransportClientScope() || liveTransportClient();
    var connectionState = "";
    if (!client || typeof client.currentPublicIncidents !== "function") {
      return null;
    }
    if (typeof client.getConnectionState === "function") {
      connectionState = String(client.getConnectionState() || "").trim().toLowerCase();
      if (connectionState && connectionState !== "live") {
        return null;
      }
    }
    return client.currentPublicIncidents(0, currentPublicIncidentVoteSelections());
  }

  function currentSpacetimeIncidentDetail(incidentId) {
    var client = syncLiveTransportClientScope() || liveTransportClient();
    var connectionState = "";
    if (!client || typeof client.currentIncidentDetail !== "function") {
      return null;
    }
    if (typeof client.getConnectionState === "function") {
      connectionState = String(client.getConnectionState() || "").trim().toLowerCase();
      if (connectionState && connectionState !== "live") {
        return null;
      }
    }
    return client.currentIncidentDetail(incidentId, currentPublicIncidentVoteSelections());
  }

  function currentLiveTransportViewerPage() {
    return String(config.mode || "public");
  }

  function stopLiveMapRefreshTimer() {
    if (state.liveVehiclesRefreshTimer) {
      clearInterval(state.liveVehiclesRefreshTimer);
      state.liveVehiclesRefreshTimer = null;
    }
  }

  function stopLiveTransportHeartbeat() {
    if (state.liveTransportHeartbeatTimer) {
      clearInterval(state.liveTransportHeartbeatTimer);
      state.liveTransportHeartbeatTimer = null;
    }
  }

  function stopIncidentFeedRefreshTimer() {
    if (state.incidentFeedRefreshTimer) {
      clearInterval(state.incidentFeedRefreshTimer);
      state.incidentFeedRefreshTimer = null;
    }
  }

  function setLiveTransportViewerVisibility(visible, options) {
    var settings = options || {};
    if (!liveTransportSpacetimeControlEnabled()) {
      return Promise.resolve(null);
    }
    if (!state.liveTransportHeartbeatSessionId) {
      state.liveTransportHeartbeatSessionId = generateLiveTransportViewerSessionId();
    }
    return callSpacetimePublicProcedure(
      "satiksmebot_set_live_viewer_state",
      [state.liveTransportHeartbeatSessionId, currentLiveTransportViewerPage(), Boolean(visible)],
      { keepalive: settings.keepalive === true }
    ).catch(function () {
      return null;
    });
  }

  function bindLiveTransportLifecycleEvents() {
    var win = windowHandle();
    if (state.liveTransportVisibilityBound || !root.document || typeof root.document.addEventListener !== "function") {
      return;
    }
    state.liveTransportVisibilityBound = true;
    root.document.addEventListener("visibilitychange", function () {
      if (documentVisible()) {
        void handleLiveTransportPageVisible();
        return;
      }
      void handleLiveTransportPageHidden({ keepalive: true });
    });
    if (win && typeof win.addEventListener === "function") {
      win.addEventListener("pagehide", function () {
        void handleLiveTransportPageHidden({ keepalive: true });
      });
      win.addEventListener("pageshow", function () {
        if (documentVisible()) {
          void handleLiveTransportPageVisible();
        }
      });
    }
  }

  function handleLiveTransportPageHidden(options) {
    var client = state.liveTransportClient;
    stopLiveTransportHeartbeat();
    stopLiveMapRefreshTimer();
    stopIncidentFeedRefreshTimer();
    if (client && state.liveTransportRealtimeStarted && typeof client.disconnect === "function") {
      client.disconnect();
    }
    state.liveTransportRealtimeStarted = false;
    return setLiveTransportViewerVisibility(false, options);
  }

  function handleLiveTransportPageVisible() {
    if (!documentVisible()) {
      return Promise.resolve(null);
    }
    if (!liveTransportRealtimeEnabled()) {
      if (liveTransportPageEnabled()) {
        startLiveMapPolling();
        if (liveTransportSnapshotLookupEnabled()) {
          return ensureLiveTransportRealtimeStarted().then(function () {
            return refreshLiveMap().catch(function () {
              return null;
            });
          });
        }
        return refreshLiveMap().catch(function () {
          return null;
        });
      }
      startIncidentFeedPolling();
      return refreshIncidentFeed().catch(function () {
        return null;
      });
    }
    return ensureLiveTransportRealtimeStarted().then(function () {
      if (liveTransportPageEnabled()) {
        startLiveTransportHeartbeat();
        return refreshLiveMap().catch(function () {
          return null;
        });
      }
      return loadIncidents()
        .then(function () {
          return ensureIncidentDetailForLayout();
        })
        .catch(function () {
          return null;
        });
    });
  }

  function handleSpacetimeRealtimeInvalidate() {
    if (String(config.mode || "public") === "public-incidents") {
      var detailChanged = false;
      loadIncidents()
        .then(function (listChanged) {
          return ensureIncidentDetailForLayout().then(function (changed) {
            detailChanged = changed;
            return listChanged;
          });
        })
        .then(function (listChanged) {
          if (listChanged || detailChanged) {
            renderIncidentFeed();
          }
        })
        .catch(function () {
          return null;
        });
      return;
    }
    refreshLiveMap().catch(function () {
      return null;
    });
  }

  function heartbeatLiveTransportViewer() {
    if (!liveTransportSpacetimeControlEnabled() || !documentVisible() || state.liveTransportHeartbeatInFlight) {
      return Promise.resolve(null);
    }
    if (!state.liveTransportHeartbeatSessionId) {
      state.liveTransportHeartbeatSessionId = generateLiveTransportViewerSessionId();
    }
    state.liveTransportHeartbeatInFlight = true;
    return callSpacetimePublicProcedure(
      "satiksmebot_heartbeat_live_viewer",
      [state.liveTransportHeartbeatSessionId, currentLiveTransportViewerPage()],
      {}
    ).finally(function () {
      state.liveTransportHeartbeatInFlight = false;
    });
  }

  function startLiveTransportHeartbeat() {
    if (!liveTransportSpacetimeControlEnabled() || !documentVisible()) {
      return;
    }
    stopLiveTransportHeartbeat();
    void setLiveTransportViewerVisibility(true).finally(function () {
      void heartbeatLiveTransportViewer();
    });
    state.liveTransportHeartbeatTimer = setInterval(function () {
      void heartbeatLiveTransportViewer();
    }, liveTransportHeartbeatMs);
  }

  function refreshIncidentFeed() {
    var detailChanged = false;
    return loadIncidents()
      .then(function (listChanged) {
        return ensureIncidentDetailForLayout().then(function (changed) {
          detailChanged = changed;
          return listChanged;
        });
      })
      .then(function (listChanged) {
        if (listChanged || detailChanged) {
          renderIncidentFeed();
        }
        return Boolean(listChanged || detailChanged);
      });
  }

  function startIncidentFeedPolling() {
    bindLiveTransportLifecycleEvents();
    stopIncidentFeedRefreshTimer();
    if (liveTransportRealtimeEnabled() || !documentVisible()) {
      return;
    }
    state.incidentFeedRefreshTimer = setInterval(function () {
      refreshIncidentFeed().catch(function (error) {
        setStatus((error && error.message) || "Neizdevās atjaunot incidentus");
      });
    }, 30000);
  }

  function pad(value) {
    return String(value).padStart(2, "0");
  }

  function localizedModeLabel(mode) {
    switch (String(mode || "").trim().toLowerCase()) {
      case "bus":
        return "Autobuss";
      case "tram":
        return "Tramvajs";
      case "trol":
        return "Trolejbuss";
      case "minibus":
        return "Mikroautobuss";
      case "seasonalbus":
        return "Sezonas autobuss";
      case "suburbanbus":
        return "Piepilsētas autobuss";
      default:
        return String(mode || "").trim() || "Transports";
    }
  }

  function modeAndRouteLabel(mode, routeLabel) {
    var modeLabel = localizedModeLabel(mode);
    var route = String(routeLabel || "").trim();
    return route ? modeLabel + " " + route : modeLabel;
  }

  function vehicleMovementTimestampMs(vehicle) {
    var timestampMs = vehicle && vehicle.updatedAt ? new Date(vehicle.updatedAt).getTime() : NaN;
    return Number.isFinite(timestampMs) ? timestampMs : 0;
  }

  function vehicleMovementDurationMs(previousTimestampMs, nextTimestampMs, fallbackElapsedMs) {
    var maxMs = Math.max(1000, Math.round(liveMapRefreshMs * 0.85));
    if (
      Number.isFinite(previousTimestampMs) &&
      previousTimestampMs > 0 &&
      Number.isFinite(nextTimestampMs) &&
      nextTimestampMs > previousTimestampMs
    ) {
      return Math.min(maxMs, Math.max(liveVehicleAnimationMinMs, Math.round((nextTimestampMs - previousTimestampMs) * 0.85)));
    }
    if (Number.isFinite(fallbackElapsedMs) && fallbackElapsedMs > 0) {
      return Math.min(maxMs, Math.max(liveVehicleAnimationMinMs, Math.round(fallbackElapsedMs * 0.85)));
    }
    return maxMs;
  }

  function vehicleMotionEase(progress) {
    if (progress <= 0) {
      return 0;
    }
    if (progress >= 1) {
      return 1;
    }
    if (progress < 0.5) {
      return 4 * progress * progress * progress;
    }
    return 1 - Math.pow(-2 * progress + 2, 3) / 2;
  }

  function vehicleLatLngEqual(left, right) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== 2 || right.length !== 2) {
      return false;
    }
    return Math.abs(left[0] - right[0]) <= 0.000001 && Math.abs(left[1] - right[1]) <= 0.000001;
  }

  function vehicleMarkerLatLng(marker) {
    if (!marker || typeof marker.getLatLng !== "function") {
      return null;
    }
    var latLng = marker.getLatLng();
    if (!latLng || !Number.isFinite(latLng.lat) || !Number.isFinite(latLng.lng)) {
      return null;
    }
    return [latLng.lat, latLng.lng];
  }

  function liveVehicleMarkerKey(vehicleId) {
    var value = String(vehicleId || "").trim();
    return value ? "live-vehicle:" + value : "";
  }

  function normalizeMapEntity(entity) {
    var type = "";
    var id = "";
    if (!entity || typeof entity !== "object") {
      return null;
    }
    type = String(entity.type || "").trim().toLowerCase();
    id = String(entity.id || "").trim();
    if ((type !== "stop" && type !== "vehicle") || !id) {
      return null;
    }
    return {
      type: type,
      id: id,
    };
  }

  function stopMapEntity(stopId) {
    var id = String(stopId || "").trim();
    return id ? { type: "stop", id: id } : null;
  }

  function vehicleMapEntity(vehicleId) {
    var id = String(vehicleId || "").trim();
    return id ? { type: "vehicle", id: id } : null;
  }

  function focusedMapEntity() {
    return normalizeMapEntity(state.focusedMapEntity);
  }

  function openMapDetailEntity() {
    return normalizeMapEntity(state.openMapDetailEntity);
  }

  function sameMapEntity(left, right) {
    var leftEntity = normalizeMapEntity(left);
    var rightEntity = normalizeMapEntity(right);
    if (!leftEntity || !rightEntity) {
      return false;
    }
    return leftEntity.type === rightEntity.type && leftEntity.id === rightEntity.id;
  }

  function isFocusedStop(stopId) {
    return sameMapEntity(focusedMapEntity(), stopMapEntity(stopId));
  }

  function isFocusedVehicle(vehicleId) {
    return sameMapEntity(focusedMapEntity(), vehicleMapEntity(vehicleId));
  }

  function isOpenStopDetail(stopId) {
    return sameMapEntity(openMapDetailEntity(), stopMapEntity(stopId));
  }

  function isOpenVehicleDetail(vehicleId) {
    return sameMapEntity(openMapDetailEntity(), vehicleMapEntity(vehicleId));
  }

  function mapDetailPresentation(entity) {
    var normalized = normalizeMapEntity(entity);
    if (!normalized) {
      return "none";
    }
    return "external-portal";
  }

  function suppressNextMapDetailDismiss() {
    state.mapDetailDismissSuppressedUntil = Date.now() + mapDetailDismissSuppressWindowMs;
  }

  function isMapDetailDismissSuppressed() {
    return Date.now() < Number(state.mapDetailDismissSuppressedUntil || 0);
  }

  function handlePotentialMapDetailDismiss(insideMapDetail) {
    if (!openMapDetailEntity() || insideMapDetail || isMapDetailDismissSuppressed()) {
      return false;
    }
    return closeMapDetail("outside-click");
  }

  function isInsideMapDetailTarget(target) {
    if (!target || !target.closest || typeof target.closest !== "function") {
      return false;
    }
    return !!target.closest("#map-detail-overlay");
  }

  function guardMapDetailOutsideClick(event) {
    if (!event || !event.target) {
      return false;
    }
    if (!handlePotentialMapDetailDismiss(isInsideMapDetailTarget(event.target))) {
      return false;
    }
    if (typeof event.preventDefault === "function") {
      event.preventDefault();
    }
    if (typeof event.stopImmediatePropagation === "function") {
      event.stopImmediatePropagation();
    }
    if (typeof event.stopPropagation === "function") {
      event.stopPropagation();
    }
    return true;
  }

  function selectedVehicleTrackingState() {
    return {
      vehicleId: String(state.selectedVehicleId || "").trim(),
      markerKey: String(state.selectedVehicleMarkerKey || "").trim(),
      paused: Boolean(state.vehicleFollowPaused),
    };
  }

  function hasSelectedVehicleTracking() {
    var tracking = selectedVehicleTrackingState();
    return Boolean(tracking.vehicleId || tracking.markerKey || tracking.paused);
  }

  function setSelectedVehicleTracking(markerKey, vehicleId, reason) {
    var nextVehicleId = String(vehicleId || "").trim();
    var nextMarkerKey = String(markerKey || "").trim() || liveVehicleMarkerKey(nextVehicleId);
    var tracking = selectedVehicleTrackingState();
    if (tracking.vehicleId === nextVehicleId && tracking.markerKey === nextMarkerKey && !tracking.paused) {
      return;
    }
    state.selectedVehicleId = nextVehicleId;
    state.selectedVehicleMarkerKey = nextMarkerKey;
    state.vehicleFollowPaused = false;
  }

  function clearSelectedVehicleTracking(reason) {
    if (!hasSelectedVehicleTracking()) {
      return;
    }
    state.selectedVehicleId = "";
    state.selectedVehicleMarkerKey = "";
    state.vehicleFollowPaused = false;
  }

  function pauseSelectedVehicleTracking(reason) {
    if (!hasSelectedVehicleTracking() || state.vehicleFollowPaused) {
      return;
    }
    state.vehicleFollowPaused = true;
  }

  function markProgrammaticMapView() {
    state.mapProgrammaticViewUntil = Date.now() + 300;
  }

  function isProgrammaticMapView() {
    return Date.now() < Number(state.mapProgrammaticViewUntil || 0);
  }

  function currentMapCenterLatLng() {
    var center = null;
    if (!state.map || typeof state.map.getCenter !== "function") {
      return null;
    }
    center = state.map.getCenter();
    if (!center || !Number.isFinite(Number(center.lat)) || !Number.isFinite(Number(center.lng))) {
      return null;
    }
    return [Number(center.lat), Number(center.lng)];
  }

  function mapViewportHostElement() {
    var mapNode = null;
    if (!document || typeof document.getElementById !== "function") {
      return null;
    }
    mapNode = document.getElementById("map");
    if (!mapNode) {
      return null;
    }
    if (typeof mapNode.closest === "function") {
      return mapNode.closest(".map-viewport") || mapNode.closest(".map-shell") || mapNode;
    }
    return mapNode.parentNode || mapNode;
  }

  function readElementPixelSize(node) {
    var rect = null;
    var width = 0;
    var height = 0;
    if (!node) {
      return { width: 0, height: 0 };
    }
    if (typeof node.getBoundingClientRect === "function") {
      rect = node.getBoundingClientRect();
      if (rect) {
        if (Number.isFinite(Number(rect.width))) {
          width = Number(rect.width);
        }
        if (Number.isFinite(Number(rect.height))) {
          height = Number(rect.height);
        }
      }
    }
    if (!width && Number.isFinite(Number(node.clientWidth))) {
      width = Number(node.clientWidth);
    }
    if (!height && Number.isFinite(Number(node.clientHeight))) {
      height = Number(node.clientHeight);
    }
    if (!width && Number.isFinite(Number(node.offsetWidth))) {
      width = Number(node.offsetWidth);
    }
    if (!height && Number.isFinite(Number(node.offsetHeight))) {
      height = Number(node.offsetHeight);
    }
    return {
      width: Math.max(0, Math.round(width)),
      height: Math.max(0, Math.round(height)),
    };
  }

  function currentMapViewportSize() {
    var host = mapViewportHostElement();
    var size = readElementPixelSize(host);
    return {
      node: host,
      width: size.width,
      height: size.height,
    };
  }

  function hasMapViewportSizeChanged(size) {
    return !!(
      size &&
      size.width > 0 &&
      size.height > 0 &&
      (size.width !== Number(state.mapViewportWidth || 0) || size.height !== Number(state.mapViewportHeight || 0))
    );
  }

  function rememberMapViewportSize(size) {
    if (!size) {
      return;
    }
    state.mapViewportWidth = Number(size.width || 0);
    state.mapViewportHeight = Number(size.height || 0);
  }

  function syncLeafletViewport(options) {
    var measurement = null;
    var force = !!(options && options.force);
    if (!state.map) {
      return false;
    }
    measurement = currentMapViewportSize();
    if (!measurement.node) {
      return false;
    }
    if ((!measurement.width || !measurement.height) && !force) {
      return false;
    }
    if (!force && !hasMapViewportSizeChanged(measurement)) {
      return false;
    }
    rememberMapViewportSize(measurement);
    markProgrammaticMapView();
    if (typeof state.map.invalidateSize === "function") {
      state.map.invalidateSize({ pan: false, animate: false });
    }
    renderVisibleStops();
    renderLiveVehicles();
    applySelectedVehicleFollow();
    renderMapDetailOverlay();
    return true;
  }

  function scheduleLeafletViewportSync(options) {
    var force = !!(options && options.force);
    var win = windowHandle();
    var requestFrame = null;
    if (!state.map) {
      return false;
    }
    state.mapViewportSyncForce = state.mapViewportSyncForce || force;
    if (state.mapViewportSyncFrame) {
      return true;
    }
    if (win && typeof win.requestAnimationFrame === "function") {
      requestFrame = win.requestAnimationFrame.bind(win);
    } else if (typeof root.requestAnimationFrame === "function") {
      requestFrame = root.requestAnimationFrame.bind(root);
    }
    if (!requestFrame) {
      force = !!state.mapViewportSyncForce;
      state.mapViewportSyncForce = false;
      return syncLeafletViewport({ force: force });
    }
    state.mapViewportSyncFrame = requestFrame(function () {
      var pendingForce = !!state.mapViewportSyncForce;
      state.mapViewportSyncFrame = 0;
      state.mapViewportSyncForce = false;
      syncLeafletViewport({ force: pendingForce });
    });
    return true;
  }

  function handleMapViewportResize(options) {
    var force = !!(options && options.force);
    var measurement = currentMapViewportSize();
    if (!measurement.node) {
      return false;
    }
    if (!force && !hasMapViewportSizeChanged(measurement)) {
      return false;
    }
    return scheduleLeafletViewportSync({ force: force });
  }

  function observeMapViewportResize() {
    var ResizeObserverCtor = null;
    var host = mapViewportHostElement();
    if (state.mapViewportResizeObserver && typeof state.mapViewportResizeObserver.disconnect === "function") {
      state.mapViewportResizeObserver.disconnect();
    }
    state.mapViewportResizeObserver = null;
    if (!host) {
      return false;
    }
    ResizeObserverCtor = root.ResizeObserver || (windowHandle() && windowHandle().ResizeObserver) || null;
    if (typeof ResizeObserverCtor !== "function") {
      return false;
    }
    state.mapViewportResizeObserver = new ResizeObserverCtor(function () {
      handleMapViewportResize();
    });
    if (typeof state.mapViewportResizeObserver.observe === "function") {
      state.mapViewportResizeObserver.observe(host);
    }
    return true;
  }

  function clearMapGestureStart() {
    state.mapGestureStartCenter = null;
    state.mapGestureStartZoom = null;
    state.mapGestureZoomPending = false;
  }

  function hasActiveVehicleMapFocus() {
    var focused = focusedMapEntity();
    var openDetail = openMapDetailEntity();
    return Boolean(
      (focused && focused.type === "vehicle") ||
      (openDetail && openDetail.type === "vehicle") ||
      hasSelectedVehicleTracking()
    );
  }

  function beginUserMapGesture(kind) {
    var center = null;
    var zoom = currentMapZoom();
    if (((kind || "move") !== "zoom" && isProgrammaticMapView()) || !hasActiveVehicleMapFocus() || !Number.isFinite(zoom)) {
      clearMapGestureStart();
      return false;
    }
    if (!Array.isArray(state.mapGestureStartCenter) || !Number.isFinite(Number(state.mapGestureStartZoom))) {
      center = currentMapCenterLatLng();
      if (!center) {
        clearMapGestureStart();
        return false;
      }
      state.mapGestureStartCenter = center.slice();
      state.mapGestureStartZoom = zoom;
    }
    if (kind === "zoom") {
      state.mapGestureZoomPending = true;
    }
    return true;
  }

  function mapPanDistancePx(startCenterLatLng) {
    var mapSize = null;
    var startPoint = null;
    var centerPoint = null;
    var currentCenter = null;
    if (!Array.isArray(startCenterLatLng) || startCenterLatLng.length !== 2) {
      return 0;
    }
    if (
      state.map &&
      typeof state.map.getSize === "function" &&
      typeof state.map.latLngToContainerPoint === "function"
    ) {
      mapSize = state.map.getSize();
      startPoint = state.map.latLngToContainerPoint(startCenterLatLng);
      if (
        mapSize &&
        startPoint &&
        Number.isFinite(Number(mapSize.x)) &&
        Number.isFinite(Number(mapSize.y)) &&
        Number.isFinite(Number(startPoint.x)) &&
        Number.isFinite(Number(startPoint.y))
      ) {
        centerPoint = {
          x: Number(mapSize.x) / 2,
          y: Number(mapSize.y) / 2,
        };
        return Math.sqrt(
          Math.pow(Number(startPoint.x) - Number(centerPoint.x), 2) +
          Math.pow(Number(startPoint.y) - Number(centerPoint.y), 2)
        );
      }
    }
    currentCenter = currentMapCenterLatLng();
    if (currentCenter && vehicleLatLngEqual(startCenterLatLng, currentCenter)) {
      return 0;
    }
    return mapUserPanTolerancePx + 1;
  }

  function clearVehicleMapFocus(reason) {
    var focused = focusedMapEntity();
    var openDetail = openMapDetailEntity();
    var shouldClearFocus = !!(focused && focused.type === "vehicle");
    var shouldClearDetail = !!(openDetail && openDetail.type === "vehicle");
    var shouldClearTracking = hasSelectedVehicleTracking();
    if (!shouldClearFocus && !shouldClearDetail && !shouldClearTracking) {
      return false;
    }
    if (shouldClearTracking) {
      clearSelectedVehicleTracking(reason || "vehicle-map-focus-cleared");
    }
    if (shouldClearFocus) {
      state.focusedMapEntity = null;
    }
    if (shouldClearDetail) {
      state.openMapDetailEntity = null;
    }
    renderMapDetailOverlay();
    return true;
  }

  function finishUserMapGesture(reason, options) {
    var startCenter = Array.isArray(state.mapGestureStartCenter) ? state.mapGestureStartCenter.slice() : null;
    var startZoom = Number(state.mapGestureStartZoom);
    var zoomPending = !!state.mapGestureZoomPending;
    if (!startCenter || !Number.isFinite(startZoom) || !hasActiveVehicleMapFocus()) {
      clearMapGestureStart();
      return false;
    }
    if (currentMapZoom() !== startZoom) {
      clearMapGestureStart();
      return clearVehicleMapFocus(reason || "user-zoomed-map");
    }
    if (options && options.deferIfZoomPending && zoomPending) {
      return false;
    }
    clearMapGestureStart();
    if (mapPanDistancePx(startCenter) > mapUserPanTolerancePx) {
      return clearVehicleMapFocus(reason || "user-moved-map");
    }
    return false;
  }

  function mapZoomTier(zoom) {
    var numericZoom = Number(zoom);
    if (!Number.isFinite(numericZoom)) {
      return "detail";
    }
    if (numericZoom <= 13) {
      return "far";
    }
    if (numericZoom <= 14) {
      return "compact";
    }
    return "detail";
  }

  function currentMapZoom() {
    if (!state.map || typeof state.map.getZoom !== "function") {
      return defaultCenter.zoom;
    }
    return Number(state.map.getZoom());
  }

  function coordinateDistanceMeters(latA, lngA, latB, lngB) {
    var earthRadiusMeters = 6371000;
    var latARad = Number(latA) * Math.PI / 180;
    var latBRad = Number(latB) * Math.PI / 180;
    var deltaLatRad = (Number(latB) - Number(latA)) * Math.PI / 180;
    var deltaLngRad = (Number(lngB) - Number(lngA)) * Math.PI / 180;
    var sinLat = Math.sin(deltaLatRad / 2);
    var sinLng = Math.sin(deltaLngRad / 2);
    var a = (sinLat * sinLat) + (Math.cos(latARad) * Math.cos(latBRad) * sinLng * sinLng);
    var c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
    return earthRadiusMeters * c;
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }

  function boundsHeightMeters(bounds) {
    var north = NaN;
    var south = NaN;
    var lng = NaN;
    if (!bounds) {
      return Infinity;
    }
    if (typeof bounds.getNorth === "function" && typeof bounds.getSouth === "function") {
      north = Number(bounds.getNorth());
      south = Number(bounds.getSouth());
    } else if (typeof bounds.getNorthWest === "function" && typeof bounds.getSouthWest === "function") {
      var northWest = bounds.getNorthWest();
      var southWest = bounds.getSouthWest();
      north = Number(northWest && northWest.lat);
      south = Number(southWest && southWest.lat);
      lng = Number(((northWest && northWest.lng) || 0) + ((southWest && southWest.lng) || 0)) / 2;
    }
    if (!Number.isFinite(lng) && typeof bounds.getCenter === "function") {
      var center = bounds.getCenter();
      lng = Number(center && center.lng);
    }
    if (!Number.isFinite(lng)) {
      lng = defaultCenter.lng;
    }
    if (!Number.isFinite(north) || !Number.isFinite(south)) {
      return Infinity;
    }
    return coordinateDistanceMeters(north, lng, south, lng);
  }

  function mapVisibleHeightMeters(map) {
    if (!map || typeof map.getBounds !== "function") {
      return NaN;
    }
    return boundsHeightMeters(map.getBounds());
  }

  function stopMarkerRadiusForHeight(visibleHeightMeters) {
    var heightMeters = Number(visibleHeightMeters);
    var progress = 0;
    if (!Number.isFinite(heightMeters) || heightMeters >= stopMarkerRadiusMinHeightMeters) {
      return stopMarkerRadiusMin;
    }
    if (heightMeters <= stopMarkerRadiusMaxHeightMeters) {
      return stopMarkerRadiusMax;
    }
    progress = (stopMarkerRadiusMinHeightMeters - heightMeters) /
      (stopMarkerRadiusMinHeightMeters - stopMarkerRadiusMaxHeightMeters);
    progress = clamp(progress, 0, 1);
    return stopMarkerRadiusMin + ((stopMarkerRadiusMax - stopMarkerRadiusMin) * progress);
  }

  function shouldRenderStopMarker(zoom, selected, sightingCount, visibleHeightMeters) {
    if (selected) {
      return true;
    }
    if (isActivityOnlyStopVisibilityMode(visibleHeightMeters)) {
      return Number(sightingCount) > 0;
    }
    return true;
  }

  function shouldShowStopBadge(zoom, sightingCount) {
    return Number(sightingCount) > 0;
  }

  function stopMarkerStyle(zoom, selected, sightingCount, visibleHeightMeters) {
    var tier = mapZoomTier(zoom);
    var hasSighting = Number(sightingCount) > 0;
    var radius = stopMarkerRadiusForHeight(visibleHeightMeters);
    if (tier === "far") {
      return {
        radius: radius,
        weight: selected ? 2 : 1,
        color: selected ? "#8a5a12" : (hasSighting ? "#a63d32" : "#21636d"),
        fillColor: selected ? "#ffd166" : (hasSighting ? "#f46d43" : "#4db8ab"),
        fillOpacity: selected ? 0.96 : (hasSighting ? 0.88 : 0.74),
      };
    }
    if (tier === "compact") {
      return {
        radius: radius,
        weight: selected ? 2 : 1,
        color: selected ? "#8a5a12" : (hasSighting ? "#b34a3c" : "#21636d"),
        fillColor: selected ? "#ffd166" : (hasSighting ? "#f46d43" : "#4db8ab"),
        fillOpacity: selected ? 0.94 : (hasSighting ? 0.88 : 0.74),
      };
    }
    return {
      radius: radius,
      weight: selected ? 2 : 1,
      color: selected ? "#8a5a12" : (hasSighting ? "#d94b3d" : "#0b5563"),
      fillColor: selected ? "#ffd166" : (hasSighting ? "#f46d43" : "#3bb7a5"),
      fillOpacity: 0.92,
    };
  }

  function stopBadgeOffsetForRadius(radius) {
    var effectiveRadius = Number(radius);
    if (!Number.isFinite(effectiveRadius)) {
      effectiveRadius = stopMarkerRadiusMin;
    }
    return [0, -Math.max(4, Math.round((effectiveRadius / 2) + 2))];
  }

  function stopBadgeScaleForHeight(visibleHeightMeters) {
    var heightMeters = Number(visibleHeightMeters);
    var progress = 0;
    if (!Number.isFinite(heightMeters) || heightMeters <= stopBadgeScaleMinHeightMeters) {
      return 1;
    }
    if (heightMeters >= stopBadgeScaleMaxHeightMeters) {
      return stopBadgeScaleMax;
    }
    progress = (heightMeters - stopBadgeScaleMinHeightMeters) /
      (stopBadgeScaleMaxHeightMeters - stopBadgeScaleMinHeightMeters);
    progress = clamp(progress, 0, 1);
    return 1 + ((stopBadgeScaleMax - 1) * progress);
  }

  function liveVehicleCompactMarkerSize(visibleHeightMeters) {
    var heightMeters = Number(visibleHeightMeters);
    if (Number.isFinite(heightMeters) && heightMeters >= liveVehicleCompactMarkerShrinkHeightMeters) {
      return liveVehicleCompactMarkerShrunkSizePx;
    }
    return liveVehicleCompactMarkerDefaultSizePx;
  }

  function vehicleMarkerProfile(zoom, visibleHeightMeters) {
    var compactSize = liveVehicleCompactMarkerSize(visibleHeightMeters);
    if (mapZoomTier(zoom) === "detail") {
      return {
        className: "map-icon-leaflet",
        iconSize: [34, 34],
        iconAnchor: [17, 17],
        showRoute: true,
        showBadge: true,
        compact: false,
        tier: "detail",
      };
    }
    return {
      className: "map-icon-leaflet",
      iconSize: [compactSize, compactSize],
      iconAnchor: [
        roundPixelValue(compactSize / 2, compactSize / 2),
        roundPixelValue(compactSize / 2, compactSize / 2),
      ],
      showRoute: false,
      showBadge: true,
      compact: true,
      tier: "compact",
    };
  }

  function colorWithAlpha(color, opacity) {
    var value = String(color || "").trim();
    var alpha = clamp(Number(opacity), 0, 1);
    var match = null;
    var red = 0;
    var green = 0;
    var blue = 0;
    if (!value) {
      return "rgba(0, 0, 0, " + alpha + ")";
    }
    match = /^#([a-f0-9]{6})$/i.exec(value);
    if (!match) {
      return value;
    }
    red = parseInt(match[1].slice(0, 2), 16);
    green = parseInt(match[1].slice(2, 4), 16);
    blue = parseInt(match[1].slice(4, 6), 16);
    return "rgba(" + red + ", " + green + ", " + blue + ", " + alpha + ")";
  }

  function roundPixelValue(value, fallback) {
    var numeric = Number(value);
    if (!Number.isFinite(numeric)) {
      return Math.max(0, Math.round(Number(fallback) || 0));
    }
    return Math.max(0, Math.round(numeric));
  }

  function stopMarkerDiameter(style) {
    var radius = Number(style && style.radius);
    var weight = Number(style && style.weight);
    if (!Number.isFinite(radius)) {
      radius = stopMarkerRadiusMin;
    }
    if (!Number.isFinite(weight)) {
      weight = 1;
    }
    return Math.max(6, roundPixelValue((radius * 2) + (weight * 2), (stopMarkerRadiusMin * 2) + 2));
  }

  function mapIconBadgeSize(size, compact) {
    var baseSize = Number(size);
    if (!Number.isFinite(baseSize) || baseSize <= 0) {
      baseSize = compact ? 14 : 18;
    }
    if (compact) {
      return Math.max(11, roundPixelValue(baseSize * 0.82, 11));
    }
    return Math.max(12, roundPixelValue(baseSize * 0.62, 12));
  }

  function mapIconBadgeFontSize(size) {
    return Math.max(9, roundPixelValue(Number(size) * 0.56, 9));
  }

  function stopBadgeSizeForHeight(baseSize, visibleHeightMeters) {
    var safeBaseSize = Number(baseSize);
    if (!Number.isFinite(safeBaseSize) || safeBaseSize <= 0) {
      safeBaseSize = mapIconBadgeSize(14, true);
    }
    return Math.max(safeBaseSize, roundPixelValue(safeBaseSize * stopBadgeScaleForHeight(visibleHeightMeters), safeBaseSize));
  }

  function stopBadgeConnectorMetrics(badgeSize) {
    var safeBadgeSize = Number(badgeSize);
    if (!Number.isFinite(safeBadgeSize) || safeBadgeSize <= 0) {
      return {
        stemHeight: 4,
        stemWidth: 3,
        pinSize: 6,
      };
    }
    return {
      stemHeight: Math.max(4, roundPixelValue(safeBadgeSize * 0.22, 4)),
      stemWidth: Math.max(3, roundPixelValue(safeBadgeSize * 0.12, 3)),
      pinSize: Math.max(6, roundPixelValue(safeBadgeSize * 0.24, 6)),
    };
  }

  function stopBadgeGeometry(bodyHeight, badgeSize, connectorMetrics) {
    var safeBodyHeight = Number(bodyHeight);
    var safeBadgeSize = Number(badgeSize);
    var connector = connectorMetrics || {};
    var stemHeight = Number(connector.stemHeight || connector.stopBadgeStemHeight);
    var pinSize = Number(connector.pinSize || connector.stopBadgePinSize);
    var pinCenterY = 0;
    var bodyOffsetY = 0;
    var iconHeight = 0;
    if (!Number.isFinite(safeBodyHeight) || safeBodyHeight <= 0) {
      safeBodyHeight = stopMarkerDiameter({
        radius: stopMarkerRadiusMin,
        weight: 1,
      });
    }
    if (!Number.isFinite(safeBadgeSize) || safeBadgeSize <= 0) {
      safeBadgeSize = mapIconBadgeSize(safeBodyHeight, true);
    }
    if (!Number.isFinite(stemHeight) || stemHeight <= 0 || !Number.isFinite(pinSize) || pinSize <= 0) {
      connector = stopBadgeConnectorMetrics(safeBadgeSize);
      stemHeight = connector.stemHeight;
      pinSize = connector.pinSize;
    }
    pinCenterY = roundPixelValue(safeBadgeSize + stemHeight + (pinSize / 2) - 2, safeBadgeSize + stemHeight);
    bodyOffsetY = Math.max(0, pinCenterY - 1);
    iconHeight = bodyOffsetY + safeBodyHeight;
    return {
      iconHeight: roundPixelValue(iconHeight, safeBodyHeight),
      bodyOffsetY: roundPixelValue(bodyOffsetY, 0),
      pinCenterY: pinCenterY,
      popupOffsetY: -1 * roundPixelValue(pinCenterY + 8, pinCenterY + 8),
    };
  }

  function vehicleModeClass(mode) {
    var token = escapeToken(mode);
    return token ? "map-icon-mode-" + token : "";
  }

  function vehicleMarkerCount(vehicle) {
    var incidentCount = incidentActivityTotal(vehicle && vehicle.incidents);
    if (incidentCount > 0) {
      return incidentCount;
    }
    return Number(vehicle && vehicle.sightingCount) || 0;
  }

  function vehicleMarkerWidth(profile, routeLabel) {
    var label = String(routeLabel || "").trim();
    if (!profile || !profile.showRoute || !label) {
      return roundPixelValue(profile && profile.iconSize ? profile.iconSize[0] : 14, 14);
    }
    return Math.max(
      roundPixelValue(profile.iconSize[0], 34),
      roundPixelValue(16 + (label.length * 9), 34)
    );
  }

  function buildStopMarkerSpec(stop, options) {
    var zoom = Number(options && options.zoom);
    var selected = !!(options && options.selected);
    var sightingCount = Number(options && options.sightingCount) || 0;
    var visibleHeightMeters = options && options.visibleHeightMeters;
    var style = stopMarkerStyle(zoom, selected, sightingCount, visibleHeightMeters);
    var diameter = stopMarkerDiameter(style);
    var baseBadgeSize = shouldShowStopBadge(zoom, sightingCount) ? mapIconBadgeSize(diameter, mapZoomTier(zoom) !== "detail") : 0;
    var badgeSize = shouldShowStopBadge(zoom, sightingCount) ? stopBadgeSizeForHeight(baseBadgeSize, visibleHeightMeters) : 0;
    var badgeConnector = stopBadgeConnectorMetrics(badgeSize);
    var geometry = stopBadgeGeometry(diameter, badgeSize, badgeConnector);
    var tier = mapZoomTier(zoom);
    var popupOffsetY = geometry.popupOffsetY;
    return {
      type: "stop",
      tier: tier,
      showBadge: shouldShowStopBadge(zoom, sightingCount),
      badgeText: "!",
      labelText: "",
      classNames: [
        "map-icon-stop",
        "map-icon-tier-" + tier,
        selected ? "map-icon-selected" : "",
      ].filter(Boolean),
      style: {
        width: diameter,
        height: diameter,
        paddingX: 0,
        borderWidth: Math.max(1, Number(style.weight) || 1),
        borderColor: style.color,
        fillColor: colorWithAlpha(style.fillColor, style.fillOpacity),
        textColor: style.color,
        fontSize: Math.max(10, roundPixelValue(diameter * 0.45, 10)),
        badgeSize: badgeSize,
        badgeFontSize: mapIconBadgeFontSize(badgeSize),
        stopBadgeStemHeight: badgeConnector.stemHeight,
        stopBadgeStemWidth: badgeConnector.stemWidth,
        stopBadgePinSize: badgeConnector.pinSize,
        stopBodyOffsetY: geometry.bodyOffsetY,
      },
      iconSize: [diameter, geometry.iconHeight],
      iconAnchor: [roundPixelValue(diameter / 2, diameter / 2), geometry.pinCenterY],
      popupOffsetY: popupOffsetY,
      metrics: {
        popupOffsetY: popupOffsetY,
        bodyWidth: diameter,
        bodyHeight: diameter,
        badgeSize: badgeSize,
        bodyOffsetY: geometry.bodyOffsetY,
        pinCenterY: geometry.pinCenterY,
        iconHeight: geometry.iconHeight,
      },
    };
  }

  function buildVehicleMarkerSpec(vehicle, options) {
    var zoom = Number(options && options.zoom);
    var visibleHeightMeters = options && options.visibleHeightMeters;
    var profile = options && options.profile ? options.profile : vehicleMarkerProfile(zoom, visibleHeightMeters);
    var routeLabel = String(vehicle && vehicle.routeLabel || "").trim();
    var bodyHeight = roundPixelValue(profile && profile.iconSize ? profile.iconSize[1] : 14, 14);
    var bodyWidth = vehicleMarkerWidth(profile, routeLabel);
    var count = vehicleMarkerCount(vehicle);
    var badgeSize = profile.showBadge && count > 0 ? mapIconBadgeSize(bodyHeight, profile.compact) : 0;
    var popupOffsetY = vehiclePopupOffsetY(zoom, visibleHeightMeters);
    return {
      type: "vehicle",
      tier: profile && profile.tier ? profile.tier : (profile && profile.compact ? "compact" : "detail"),
      showBadge: !!(profile && profile.showBadge && count > 0),
      badgeText: "!",
      labelText: profile && profile.showRoute ? routeLabel : "",
      classNames: [
        "map-icon-vehicle",
        "map-icon-tier-" + (profile && profile.tier ? profile.tier : (profile && profile.compact ? "compact" : "detail")),
        profile && profile.compact ? "map-icon-compact" : "",
        vehicleModeClass(vehicle && vehicle.mode),
        !profile.compact && vehicle && vehicle.lowFloor ? "map-icon-low-floor" : "",
      ].filter(Boolean),
      style: {
        width: bodyWidth,
        height: bodyHeight,
        paddingX: profile && profile.showRoute ? 8 : 0,
        borderWidth: profile && profile.compact ? 1.5 : 2,
        borderColor: "#12333c",
        textColor: "#0f2027",
        fontSize: profile && profile.compact ? 10 : 12,
        badgeSize: badgeSize,
        badgeFontSize: mapIconBadgeFontSize(badgeSize),
      },
      iconSize: [bodyWidth, bodyHeight],
      iconAnchor: [roundPixelValue(bodyWidth / 2, bodyWidth / 2), roundPixelValue(bodyHeight / 2, bodyHeight / 2)],
      popupOffsetY: popupOffsetY,
      metrics: {
        popupOffsetY: popupOffsetY,
        bodyWidth: bodyWidth,
        bodyHeight: bodyHeight,
      },
    };
  }

  function buildMapIconStyle(spec) {
    var style = spec && spec.style ? spec.style : {};
    var declarations = [];
    declarations.push("--map-icon-width:" + roundPixelValue(style.width, 14) + "px");
    declarations.push("--map-icon-height:" + roundPixelValue(style.height, 14) + "px");
    declarations.push("--map-icon-padding-x:" + roundPixelValue(style.paddingX, 0) + "px");
    declarations.push("--map-icon-border-width:" + String(Number(style.borderWidth || 1)) + "px");
    declarations.push("--map-icon-border:" + String(style.borderColor || "#12333c"));
    declarations.push("--map-icon-text:" + String(style.textColor || "#12333c"));
    declarations.push("--map-icon-font-size:" + roundPixelValue(style.fontSize, 12) + "px");
    declarations.push("--map-icon-badge-size:" + roundPixelValue(style.badgeSize, 12) + "px");
    declarations.push("--map-icon-badge-font-size:" + roundPixelValue(style.badgeFontSize, 9) + "px");
    declarations.push("--map-icon-stop-badge-stem-height:" + roundPixelValue(style.stopBadgeStemHeight, 4) + "px");
    declarations.push("--map-icon-stop-badge-stem-width:" + roundPixelValue(style.stopBadgeStemWidth, 3) + "px");
    declarations.push("--map-icon-stop-badge-pin-size:" + roundPixelValue(style.stopBadgePinSize, 6) + "px");
    declarations.push("--map-icon-stop-body-offset-y:" + roundPixelValue(style.stopBodyOffsetY, 0) + "px");
    if (style.fillColor) {
      declarations.push("--map-icon-fill:" + String(style.fillColor));
    }
    return declarations.join(";");
  }

  function buildMapIconHTML(spec) {
    var classNames = ["map-icon"].concat((spec && spec.classNames) || []);
    var badgeHTML = "";
    var labelHTML = "";
    if (spec && spec.labelText) {
      labelHTML = '<span class="map-icon-label">' + escapeHTML(spec.labelText) + "</span>";
    }
    if (spec && spec.showBadge) {
      if (spec.type === "stop") {
        badgeHTML =
          '<span class="map-icon-badge-anchor map-icon-stop-badge-anchor">' +
          '<span class="map-icon-badge map-icon-stop-badge">' + escapeHTML(spec.badgeText || "!") + "</span>" +
          '<span class="map-icon-stop-badge-connector" aria-hidden="true"></span>' +
          '<span class="map-icon-stop-badge-pin" aria-hidden="true"></span>' +
          "</span>";
      } else {
        badgeHTML = '<span class="map-icon-badge-anchor"><span class="map-icon-badge">' + escapeHTML(spec.badgeText || "!") + "</span></span>";
      }
    }
    return (
      '<div class="map-icon-host">' +
      '<div class="' + classNames.join(" ") + '" style="' + escapeAttr(buildMapIconStyle(spec)) + '">' +
      labelHTML +
      badgeHTML +
      "</div>" +
      "</div>"
    );
  }

  function buildMapMarkerIcon(spec) {
    if (!root.L || typeof root.L.divIcon !== "function") {
      return null;
    }
    return root.L.divIcon({
      className: "map-icon-leaflet",
      html: buildMapIconHTML(spec),
      iconSize: spec.iconSize,
      iconAnchor: spec.iconAnchor,
    });
  }

  function markerRenderKey(spec) {
    return JSON.stringify(spec || {});
  }

  function cachedMapMarkerIcon(spec, cache) {
    var iconCache = cache instanceof Map ? cache : null;
    var key = markerRenderKey(spec);
    if (!iconCache) {
      return {
        key: key,
        icon: buildMapMarkerIcon(spec),
      };
    }
    if (!iconCache.has(key)) {
      iconCache.set(key, buildMapMarkerIcon(spec));
    }
    return {
      key: key,
      icon: iconCache.get(key),
    };
  }

  function markerIconMetrics(marker) {
    if (marker && marker.__satiksmeIconMetrics && Number.isFinite(Number(marker.__satiksmeIconMetrics.popupOffsetY))) {
      return marker.__satiksmeIconMetrics;
    }
    if (marker && marker.options && marker.options.mapIconMetrics && Number.isFinite(Number(marker.options.mapIconMetrics.popupOffsetY))) {
      return marker.options.mapIconMetrics;
    }
    if (marker && marker.options && Number.isFinite(Number(marker.options.radius))) {
      return {
        popupOffsetY: stopPopupOffsetY(marker.options),
      };
    }
    return null;
  }

  function setMarkerIconSpec(marker, spec) {
    if (!marker || !spec) {
      return;
    }
    marker.__satiksmeIconSpec = spec;
    marker.__satiksmeRenderKey = markerRenderKey(spec);
    marker.__satiksmeIconMetrics = spec.metrics || null;
    if (!marker.options || typeof marker.options !== "object") {
      marker.options = {};
    }
    marker.options.mapIconMetrics = spec.metrics || null;
  }

  function markerLatLngMatches(marker, latLng) {
    var currentLatLng = vehicleMarkerLatLng(marker);
    return vehicleLatLngEqual(currentLatLng, latLng);
  }

  function visibleStopCandidates(visibleHeightMeters) {
    var stops = state.catalog && Array.isArray(state.catalog.stops) ? state.catalog.stops : [];
    var candidateIDs = Object.create(null);
    var out = [];

    function pushStop(stopId) {
      var stop = findStop(stopId);
      if (!stop || !stop.id || candidateIDs[stop.id]) {
        return;
      }
      candidateIDs[stop.id] = true;
      out.push(stop);
    }

    if (!isActivityOnlyStopVisibilityMode(visibleHeightMeters)) {
      return stops;
    }
    Object.keys(state.stopActivityCounts || {}).forEach(pushStop);
    pushStop(state.selectedStop && state.selectedStop.id);
    pushStop(focusedMapEntity() && focusedMapEntity().type === "stop" ? focusedMapEntity().id : "");
    pushStop(openMapDetailEntity() && openMapDetailEntity().type === "stop" ? openMapDetailEntity().id : "");
    return out;
  }

  function normalizeStopKey(value) {
    var trimmed = String(value || "").trim();
    if (!trimmed) {
      return "";
    }
    var normalized = trimmed.replace(/^0+/, "");
    return normalized || "0";
  }

  function createStopIndex(stops) {
    var index = Object.create(null);
    (stops || []).forEach(function (stop) {
      var stopId = String(stop && stop.id || "").trim();
      var normalizedStopId = normalizeStopKey(stopId);
      if (!stopId) {
        return;
      }
      index[stopId] = stop;
      if (normalizedStopId && !index[normalizedStopId]) {
        index[normalizedStopId] = stop;
      }
    });
    return index;
  }

  function setCatalogState(payload) {
    var catalog = payload || null;
    var stops = catalog && Array.isArray(catalog.stops) ? catalog.stops : [];
    state.catalog = catalog;
    state.stopIndex = createStopIndex(stops);
    syncStopActivityCounts();
  }

  function resolveIndexedStop(stopId, index) {
    var targetStopId = String(stopId || "").trim();
    var normalizedTargetStopId = normalizeStopKey(targetStopId);
    var stopIndex = index || state.stopIndex || Object.create(null);
    if (!targetStopId) {
      return null;
    }
    return stopIndex[targetStopId] || stopIndex[normalizedTargetStopId] || null;
  }

  function stopActivityCount(stopId) {
    if (!state.stopActivityCounts) {
      return 0;
    }
    return Number(state.stopActivityCounts[String(stopId || "").trim()]) || 0;
  }

  function syncStopActivityCounts() {
    var counts = Object.create(null);
    function mark(stopId, amount) {
      var stop = resolveIndexedStop(stopId, state.stopIndex);
      var increment = Number(amount);
      if (!stop || !stop.id) {
        return;
      }
      if (!Number.isFinite(increment) || increment < 1) {
        increment = 1;
      }
      counts[stop.id] = (counts[stop.id] || 0) + increment;
    }
    if (!state.catalog || !state.stopIndex) {
      state.stopActivityCounts = counts;
      return counts;
    }
    var incidentStopIds = Object.create(null);
    (state.stopIncidents || []).forEach(function (item) {
      var stopId = item && (item.stopId || item.subjectId);
      if (stopId) {
        incidentStopIds[String(stopId).trim()] = true;
        incidentStopIds[normalizeStopKey(stopId)] = true;
      }
      mark(stopId, incidentActivityCount(item));
    });
    (state.sightings && state.sightings.stopSightings || []).forEach(function (item) {
      var itemStopId = String(item && item.stopId || "").trim();
      if (incidentStopIds[itemStopId] || incidentStopIds[normalizeStopKey(itemStopId)]) {
        return;
      }
      mark(itemStopId, 1);
    });
    state.stopActivityCounts = counts;
    return counts;
  }

  function isActivityOnlyStopVisibilityMode(visibleHeightMeters) {
    var heightMeters = Number(visibleHeightMeters);
    return Number.isFinite(heightMeters) && heightMeters > activityOnlyStopVisibilityHeightMeters;
  }

  function isSelectedStop(stopId) {
    return !!(
      state.selectedStop &&
      String(state.selectedStop.id || "").trim() === String(stopId || "").trim()
    );
  }

  function sightingTimestampMs(value) {
    if (!value) {
      return 0;
    }
    var at = value instanceof Date ? value : new Date(value);
    var timestampMs = at.getTime();
    return Number.isFinite(timestampMs) ? timestampMs : 0;
  }

  function stopIncidentsForStop(stopId, stopIncidents) {
    var targetStopId = String(stopId || "").trim();
    var normalizedTargetStopId = normalizeStopKey(targetStopId);
    if (!targetStopId) {
      return [];
    }
    return (stopIncidents || []).filter(function (item) {
      var itemStopId = String(item && item.stopId || item && item.subjectId || "").trim();
      return itemStopId === targetStopId || normalizeStopKey(itemStopId) === normalizedTargetStopId;
    });
  }

  function latestReportTimestampForStop(stopId, sightings, stopIncidents) {
    var targetStopId = String(stopId || "").trim();
    var normalizedTargetStopId = normalizeStopKey(targetStopId);
    var latestMs = 0;
    if (!targetStopId || !sightings) {
      return 0;
    }
    (sightings.stopSightings || []).forEach(function (item) {
      var itemStopId = String(item.stopId || "").trim();
      if (itemStopId === targetStopId || normalizeStopKey(itemStopId) === normalizedTargetStopId) {
        latestMs = Math.max(latestMs, sightingTimestampMs(item.createdAt));
      }
    });
    stopIncidentsForStop(targetStopId, stopIncidents).forEach(function (item) {
      latestMs = Math.max(latestMs, sightingTimestampMs(item.lastReportAt));
    });
    return latestMs;
  }

  function formatRelativeReportAge(value, now) {
    var timestampMs = sightingTimestampMs(value);
    if (!timestampMs) {
      return "nav ziņojumu";
    }
    var nowMs = sightingTimestampMs(now || new Date());
    if (!nowMs || nowMs < timestampMs) {
      nowMs = timestampMs;
    }
    var diffSeconds = Math.floor((nowMs - timestampMs) / 1000);
    if (diffSeconds < 60) {
      return "tikko";
    }
    var diffMinutes = Math.floor(diffSeconds / 60);
    if (diffMinutes < 60) {
      return "pirms " + diffMinutes + " min";
    }
    var diffHours = Math.floor(diffMinutes / 60);
    if (diffHours < 24) {
      return "pirms " + diffHours + " h";
    }
    return "pirms " + Math.floor(diffHours / 24) + " d";
  }

  function latestReportAgeLabel(stopId, sightings, stopIncidents, now) {
    if (!Array.isArray(stopIncidents)) {
      now = stopIncidents;
      stopIncidents = state.stopIncidents;
    }
    var latestMs = latestReportTimestampForStop(stopId, sightings, stopIncidents);
    if (!latestMs) {
      return "Nav ziņojumu";
    }
    return "Pēdējais: " + formatRelativeReportAge(latestMs, now);
  }

  function resolveInitialView(position) {
    if (!position || !position.coords) {
      return defaultCenter;
    }
    return {
      lat: position.coords.latitude,
      lng: position.coords.longitude,
      zoom: 15,
    };
  }

  function userLocationLatLng(position) {
    var source = position && position.coords ? position.coords : position;
    var lat = NaN;
    var lng = NaN;
    if (!source) {
      return null;
    }
    lat = Number(source.latitude !== undefined ? source.latitude : source.lat);
    lng = Number(source.longitude !== undefined ? source.longitude : source.lng);
    if (!Number.isFinite(lat) || !Number.isFinite(lng)) {
      return null;
    }
    return [lat, lng];
  }

  function buildUserLocationIcon() {
    if (!root.L || typeof root.L.divIcon !== "function") {
      return null;
    }
    return root.L.divIcon({
      className: "map-user-location-leaflet",
      iconSize: [28, 28],
      iconAnchor: [14, 14],
      html: '<span class="map-user-location-marker" aria-hidden="true"><span class="map-user-location-dot"></span></span>',
    });
  }

  function renderUserLocationMarker() {
    var latLng = userLocationLatLng(state.currentPosition);
    var icon = null;
    if (!state.map || !root.L || !latLng) {
      return false;
    }
    if (state.userLocationMarker && typeof state.userLocationMarker.setLatLng === "function") {
      state.userLocationMarker.setLatLng(latLng);
      return true;
    }
    if (typeof root.L.marker !== "function") {
      return false;
    }
    icon = buildUserLocationIcon();
    state.userLocationMarker = root.L.marker(latLng, {
      icon: icon || undefined,
      interactive: false,
      keyboard: false,
      zIndexOffset: 650,
    });
    if (state.userLocationMarker && typeof state.userLocationMarker.addTo === "function") {
      state.userLocationMarker.addTo(state.map);
    }
    return true;
  }

  function centerMapOnUserLocation(options) {
    var latLng = userLocationLatLng(state.currentPosition);
    var animate = !(options && options.animate === false);
    if (!state.map || !latLng || typeof state.map.setView !== "function") {
      return false;
    }
    markProgrammaticMapView();
    state.map.setView(latLng, 15, { animate: animate });
    return true;
  }

  function setLocationControlPending(pending) {
    var button = state.userLocationControl && state.userLocationControl.button;
    if (!button || typeof button.setAttribute !== "function") {
      return;
    }
    if (pending) {
      button.setAttribute("aria-busy", "true");
      button.classList && button.classList.add("is-pending");
      return;
    }
    if (typeof button.removeAttribute === "function") {
      button.removeAttribute("aria-busy");
    }
    button.classList && button.classList.remove("is-pending");
  }

  function focusUserLocation() {
    if (centerMapOnUserLocation({ animate: true })) {
      return Promise.resolve(true);
    }
    return requestLocation({ centerOnSuccess: true, userInitiated: true });
  }

  function createElementForMapControl(tagName, className, container) {
    if (root.L && root.L.DomUtil && typeof root.L.DomUtil.create === "function") {
      return root.L.DomUtil.create(tagName, className, container);
    }
    if (!document || typeof document.createElement !== "function") {
      return null;
    }
    var node = document.createElement(tagName);
    if (className) {
      node.className = className;
    }
    if (container && typeof container.appendChild === "function") {
      container.appendChild(node);
    }
    return node;
  }

  function addUserLocationControl() {
    var control = null;
    if (!state.map || !root.L || typeof root.L.control !== "function" || state.userLocationControl) {
      return false;
    }
    control = root.L.control({ position: "topleft" });
    control.onAdd = function () {
      var container = createElementForMapControl("div", "leaflet-bar leaflet-control map-user-location-control", null);
      var button = createElementForMapControl("button", "map-user-location-button", container);
      var icon = createElementForMapControl("span", "map-user-location-control-icon", button);
      if (!container || !button) {
        return container || button;
      }
      button.type = "button";
      button.title = "Parādīt manu atrašanās vietu";
      button.setAttribute("aria-label", "Parādīt manu atrašanās vietu");
      if (icon) {
        icon.setAttribute("aria-hidden", "true");
      }
      if (root.L.DomEvent) {
        if (typeof root.L.DomEvent.disableClickPropagation === "function") {
          root.L.DomEvent.disableClickPropagation(container);
        }
        if (typeof root.L.DomEvent.disableScrollPropagation === "function") {
          root.L.DomEvent.disableScrollPropagation(container);
        }
        if (typeof root.L.DomEvent.on === "function") {
          root.L.DomEvent.on(button, "click", function (event) {
            if (event && typeof event.preventDefault === "function") {
              event.preventDefault();
            }
            void focusUserLocation();
          });
        } else if (typeof button.addEventListener === "function") {
          button.addEventListener("click", function (event) {
            event.preventDefault();
            void focusUserLocation();
          });
        }
      } else if (typeof button.addEventListener === "function") {
        button.addEventListener("click", function (event) {
          event.preventDefault();
          void focusUserLocation();
        });
      }
      state.userLocationControl.button = button;
      return container;
    };
    state.userLocationControl = { control: control, button: null };
    if (typeof control.addTo === "function") {
      control.addTo(state.map);
    }
    return true;
  }

  function canVoteIncidentOnMap(mode, authenticated) {
    return !!authenticated;
  }

  function incidentVoteLabel(value) {
    return value === "CLEARED" ? "Nav kontrole" : "Kontrole";
  }

  function incidentVoteTotal(votes) {
    var ongoing = votes && typeof votes.ongoing === "number" ? votes.ongoing : 0;
    var cleared = votes && typeof votes.cleared === "number" ? votes.cleared : 0;
    return Math.max(0, ongoing) + Math.max(0, cleared);
  }

  function incidentActivityCount(item) {
    var total = incidentVoteTotal(item && item.votes);
    return total > 0 ? total : 1;
  }

  function incidentVoteTotalForItems(items) {
    return (items || []).reduce(function (total, item) {
      return total + incidentVoteTotal(item && item.votes);
    }, 0);
  }

  function incidentActivityTotal(items) {
    return (items || []).reduce(function (total, item) {
      return total + incidentActivityCount(item);
    }, 0);
  }

  function renderIncidentStatusPill(item) {
    if (!item || !item.resolved) {
      return "";
    }
    return '<span class="station-selected-pill incident-status-pill incident-status-pill-resolved">Nav kontrole</span>';
  }

  function renderIncidentActionRow(item, options) {
    var incident = item || {};
    var mode = String((options && options.mode) || config.mode || "public");
    var authenticated = !!(options && options.authenticated);
    var allowVotes = canVoteIncidentOnMap(mode, authenticated);
    var voteValue = incident && incident.votes && incident.votes.userValue ? incident.votes.userValue : "";
    var title = incident.lastReportName || incident.subjectName || "Incidents";
    return (
      '<div class="incident-inline-row">' +
      '<div class="incident-inline-copy">' +
      '<strong>' + escapeHTML(title) + '</strong>' +
      '<span>' + escapeHTML(incidentVoteSummaryLabel(incident.votes)) + '</span>' +
      '</div>' +
      '<div class="button-row incident-inline-actions">' +
      (allowVotes
        ? '<button class="' + (voteValue === "ONGOING" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(incident.id) + '" data-value="ONGOING">' + escapeHTML(incidentVoteLabel("ONGOING")) + '</button>' +
          '<button class="' + (voteValue === "CLEARED" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(incident.id) + '" data-value="CLEARED">' + escapeHTML(incidentVoteLabel("CLEARED")) + "</button>"
        : "") +
      '<button class="action action-secondary action-compact" data-action="open-incident-page" data-incident-id="' + escapeAttr(incident.id) + '">Detaļas</button>' +
      "</div>" +
      "</div>"
    );
  }

  function renderIncidentActionRows(items, options) {
    var incidents = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!incidents.length) {
      return "";
    }
    return '<div class="incident-inline-list">' + incidents.map(function (item) {
      return renderIncidentActionRow(item, options);
    }).join("") + "</div>";
  }

  function renderStopSightingControl(mode, authenticated, selectedStop, sightings, stopIncidents, now) {
    if (!Array.isArray(stopIncidents)) {
      now = stopIncidents;
      stopIncidents = state.stopIncidents;
    }
    if (!authenticated || !selectedStop) {
      return "";
    }
    return (
      '<div class="report-stop-inline stop-detail-actions">' +
      '<button class="action action-danger action-compact" data-action="report-stop">Pieturas kontrole</button>' +
      "</div>"
    );
  }

  function groupSightingsByStop(sightings, stopIncidents) {
    var counts = Object.create(null);
    (sightings.stopSightings || []).forEach(function (item) {
      var stopId = String(item && item.stopId || "").trim();
      var normalizedStopId = normalizeStopKey(stopId);
      if (!stopId) {
        return;
      }
      counts[stopId] = (counts[stopId] || 0) + 1;
      if (normalizedStopId && normalizedStopId !== stopId) {
        counts[normalizedStopId] = (counts[normalizedStopId] || 0) + 1;
      }
    });
    (stopIncidents || []).forEach(function (item) {
      var stopId = String(item && item.stopId || item && item.subjectId || "").trim();
      var normalizedStopId = normalizeStopKey(stopId);
      if (!stopId) {
        return;
      }
      counts[stopId] = Math.max(counts[stopId] || 0, 1);
      if (normalizedStopId && normalizedStopId !== stopId) {
        counts[normalizedStopId] = Math.max(counts[normalizedStopId] || 0, 1);
      }
    });
    return counts;
  }

  function findStop(stopId) {
    return resolveIndexedStop(stopId, state.stopIndex);
  }

  function stopDistance(a, b) {
    var dx = (a.latitude - b.latitude) * 111000;
    var dy = (a.longitude - b.longitude) * 64000;
    return Math.sqrt(dx * dx + dy * dy);
  }

  function nearestStops(origin, limit, excludeStopId) {
    if (!state.catalog || !state.catalog.stops || !origin) {
      return [];
    }
    return state.catalog.stops
      .filter(function (stop) {
        return stop.id !== excludeStopId;
      })
      .map(function (stop) {
        return { stop: stop, distance: stopDistance(origin, stop) };
      })
      .sort(function (a, b) {
        return a.distance - b.distance;
      })
      .slice(0, limit)
      .map(function (item) {
        return item.stop;
      });
  }

  function mapRootPath() {
    return pathFor("/");
  }

  function renderHeroMeta(mode) {
    var links = [];
    if (mode === "public-incidents") {
      links.push('<a class="pill pill-muted" href="' + escapeAttr(mapRootPath()) + '">Mape</a>');
    } else {
      links.push('<a class="pill pill-muted" href="' + escapeAttr(pathFor("/incidents")) + '">Plūsma</a>');
    }
    return '<div class="hero-meta">' + links.join("") + '<span id="status-pill" class="pill pill-muted">Ielādē…</span><span id="auth-controls" class="button-row hero-auth-controls"></span></div>';
  }

  function quickLoadingBarHTML() {
    return '<span class="quick-loading-bar" aria-hidden="true"></span>';
  }

  function loadingStateHTML(label, className) {
    var classes = "loading-state" + (className ? " " + String(className) : "");
    return (
      '<div class="' + escapeAttr(classes) + '" role="status" aria-live="polite">' +
      quickLoadingBarHTML() +
      '<span class="loading-copy">' + escapeHTML(label || "Ielādē datus…") + "</span>" +
      "</div>"
    );
  }

  function publicMapNeedsLoadingUI() {
    return !!state.publicMapLoading && !state.publicMapLoaded;
  }

  function publicIncidentListNeedsLoadingUI() {
    return !!state.publicIncidentsLoading && !state.publicIncidentsLoaded && !state.publicIncidents.length;
  }

  function publicIncidentDetailNeedsLoadingUI(detail) {
    var loadingId = String(state.publicIncidentDetailLoadingId || state.publicIncidentSelectedId || "").trim();
    var detailId = detail && detail.summary ? String(detail.summary.id || "").trim() : "";
    return !!state.publicIncidentDetailLoading && !!loadingId && detailId !== loadingId;
  }

  function pageDataLoading() {
    if (String(config.mode || "public") === "public-incidents") {
      return publicIncidentListNeedsLoadingUI() || publicIncidentDetailNeedsLoadingUI(state.publicIncidentDetail);
    }
    return publicMapNeedsLoadingUI();
  }

  function syncLoadingIndicators() {
    var mapIndicator = document.getElementById("map-loading-indicator");
    var statusPill = document.getElementById("status-pill");
    var mapLoading = publicMapNeedsLoadingUI();
    var pageLoading = pageDataLoading();
    if (mapIndicator) {
      mapIndicator.hidden = !mapLoading;
      if (mapIndicator.setAttribute) {
        mapIndicator.setAttribute("aria-hidden", mapLoading ? "false" : "true");
      }
    }
    if (statusPill && statusPill.classList && typeof statusPill.classList.toggle === "function") {
      statusPill.classList.toggle("pill-loading", pageLoading);
    }
    if (statusPill && statusPill.setAttribute) {
      statusPill.setAttribute("aria-busy", pageLoading ? "true" : "false");
    }
  }

  function setPublicMapLoading(value) {
    state.publicMapLoading = !!value;
    syncLoadingIndicators();
    setStatus(readyStatusText());
  }

  function markPublicMapLoaded() {
    state.publicMapLoaded = true;
    syncLoadingIndicators();
  }

  function setPublicIncidentsLoading(value) {
    state.publicIncidentsLoading = !!value;
    syncLoadingIndicators();
    setStatus(readyStatusText());
  }

  function markPublicIncidentsLoaded() {
    state.publicIncidentsLoaded = true;
    syncLoadingIndicators();
  }

  function setPublicIncidentDetailLoading(incidentId, value) {
    state.publicIncidentDetailLoading = !!value;
    state.publicIncidentDetailLoadingId = value ? String(incidentId || "").trim() : "";
    syncLoadingIndicators();
    setStatus(readyStatusText());
  }

  function renderAuthControlsHTML() {
    if (state.authenticated) {
      return '<button class="action action-secondary action-compact" data-action="logout">Izrakstīties</button>';
    }
    return '<button class="action action-primary action-compact" data-action="telegram-login">Pieslēgties ar Telegram</button>';
  }

  function renderAuthControls() {
    var node = document.getElementById("auth-controls");
    if (!node) {
      return;
    }
    node.innerHTML = renderAuthControlsHTML();
  }

  function renderAuthDependentUI() {
    renderAuthControls();
    renderSelectedStop();
    renderIncidentFeed();
    renderMapDetailOverlay();
  }

  function pageTitleForMode(mode) {
    if (String(mode || "").trim() === "public-incidents") {
      return "Kontroles plūsma | Kontrole";
    }
    return "Kontrole | Satiksmes mape";
  }

  function syncPageTitle() {
    if (!root.document) {
      return;
    }
    root.document.title = pageTitleForMode(String(config.mode || "public"));
  }

  function authErrorStatusText() {
    return "Pieslēgties ar Telegram neizdevās. Mēģini vēlreiz.";
  }

  function authFeedbackText(kind, message) {
    if (kind === "cancelled") {
      return "Telegram pieslēgšanās tika atcelta";
    }
    if (kind === "popup_blocked") {
      return "Telegram popupu neizdevās atvērt";
    }
    if (kind === "error") {
      return String(message || authErrorStatusText());
    }
    return "";
  }

  function clearAuthFeedback() {
    state.authInProgress = false;
    state.authFeedback = null;
  }

  function startAuthFeedback() {
    state.authInProgress = true;
    state.authFeedback = null;
    renderAuthDependentUI();
    setStatus(readyStatusText());
  }

  function finishAuthFeedback(kind, message) {
    state.authInProgress = false;
    if (!kind) {
      state.authFeedback = null;
    } else {
      state.authFeedback = {
        kind: String(kind),
        message: authFeedbackText(kind, message),
      };
    }
    renderAuthDependentUI();
    setStatus(readyStatusText());
  }

  function logAuthFailure(stage, error) {
    if (root.console && typeof root.console.warn === "function") {
      root.console.warn("[satiksme-auth] " + String(stage || "auth"), error);
    }
  }

  function readyStatusText() {
    var mode = String(config.mode || "public");
    if (state.authInProgress) {
      return "Atveram Telegram pieslēgšanos…";
    }
    if (state.authFeedback && state.authFeedback.message) {
      return state.authFeedback.message;
    }
    if (pageDataLoading()) {
      return mode === "public-incidents" ? "Ielādē incidentus…" : "Ielādē kartes datus…";
    }
    if (state.authenticated || state.authState === "authenticated") {
      return "Telegram sesija aktīva";
    }
    if (mode === "public-incidents") {
      return "Incidentu plūsma ielādēta";
    }
    return "Mape gatava";
  }

  function incidentPageURL(incidentId) {
    return pathFor("/incidents?incident=" + encodeURIComponent(String(incidentId || "").trim()));
  }

  function navigateToIncidentPage(incidentId) {
    var nextIncidentId = String(incidentId || "").trim();
    var win = windowHandle();
    if (!nextIncidentId) {
      return;
    }
    if (win.location && typeof win.location.assign === "function") {
      win.location.assign(incidentPageURL(nextIncidentId));
      return;
    }
    if (win.location) {
      win.location.href = incidentPageURL(nextIncidentId);
    }
  }

  function syncIncidentLayoutState() {
    state.publicIncidentMobileLayout = isIncidentMobileLayout();
    return state.publicIncidentMobileLayout;
  }

  function isIncidentDetailVisible() {
    return !state.publicIncidentMobileLayout || state.publicIncidentDetailOpen;
  }

  function updateIncidentOverlayBodyState() {
    var classList = null;
    if (!document || !document.body || !document.body.classList) {
      return;
    }
    classList = document.body.classList;
    if (typeof classList.remove === "function") {
      classList.remove("incident-detail-overlay-open");
      return;
    }
    if (typeof classList.toggle === "function") {
      classList.toggle("incident-detail-overlay-open", false);
    }
  }

  function scrollIncidentDetailIntoView() {
    var win = windowHandle();
    var panel = document.getElementById("incident-detail-panel");
    var scrollToPanel = function () {
      var panelTop = 0;
      if (!panel) {
        return;
      }
      if (typeof panel.scrollIntoView === "function") {
        panel.scrollIntoView({ block: "start", behavior: "smooth" });
        return;
      }
      if (typeof panel.getBoundingClientRect === "function") {
        panelTop = currentPageScrollY() + panel.getBoundingClientRect().top - 16;
        setPageScrollY(panelTop);
      }
    };

    if (!panel) {
      return;
    }
    if (win && typeof win.requestAnimationFrame === "function") {
      win.requestAnimationFrame(scrollToPanel);
      return;
    }
    root.setTimeout(scrollToPanel, 0);
  }

  function pushIncidentOverlayHistory() {
    var win = windowHandle();
    if (!state.publicIncidentMobileLayout || state.publicIncidentHistoryOpen) {
      return;
    }
    if (!win.history || typeof win.history.pushState !== "function") {
      return;
    }
    try {
      win.history.pushState(incidentOverlayHistoryState(), "");
      state.publicIncidentHistoryOpen = true;
    } catch (_error) {
      state.publicIncidentHistoryOpen = false;
    }
  }

  function closeIncidentDetailOverlay(options) {
    var opts = options || {};
    var win = windowHandle();
    if (!state.publicIncidentMobileLayout && !opts.force) {
      return false;
    }
    if (!state.publicIncidentDetailOpen && !state.publicIncidentHistoryOpen) {
      return false;
    }
    state.publicIncidentDetailOpen = false;
    renderIncidentFeed();
    setPageScrollY(state.publicIncidentListScrollY);
    if (opts.skipHistory) {
      state.publicIncidentHistoryOpen = false;
      return true;
    }
    if (state.publicIncidentHistoryOpen && win.history && typeof win.history.back === "function") {
      state.publicIncidentHistoryOpen = false;
      state.publicIncidentHistoryNavigating = true;
      try {
        win.history.back();
      } catch (_error) {
        state.publicIncidentHistoryNavigating = false;
      }
      return true;
    }
    state.publicIncidentHistoryOpen = false;
    return true;
  }

  function openIncidentDetailView(incidentId) {
    var nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId) {
      return Promise.resolve(false);
    }
    syncIncidentLayoutState();
    if (state.publicIncidentMobileLayout && !state.publicIncidentDetailOpen) {
      state.publicIncidentListScrollY = currentPageScrollY();
    }
    return loadIncidentDetail(nextIncidentId)
      .then(function (changed) {
        if (state.publicIncidentMobileLayout) {
          state.publicIncidentDetailOpen = true;
          pushIncidentOverlayHistory();
        }
        renderIncidentFeed();
        if (state.publicIncidentMobileLayout) {
          scrollIncidentDetailIntoView();
        }
        return changed;
      })
      .catch(function (error) {
        setStatus((error && error.message) || "Neizdevās atvērt incidentu");
        throw error;
      });
  }

  function ensureIncidentDetailForLayout() {
    syncIncidentLayoutState();
    if (!state.publicIncidents.length) {
      state.publicIncidentSelectedId = "";
      state.publicIncidentDetail = null;
      state.publicIncidentDetailOpen = false;
      state.publicIncidentHistoryOpen = false;
      syncLiveTransportClientScope();
      return Promise.resolve(false);
    }
    if (state.publicIncidentMobileLayout) {
      if (state.publicIncidentDetailOpen && state.publicIncidentSelectedId) {
        return loadIncidentDetail(state.publicIncidentSelectedId);
      }
      return Promise.resolve(false);
    }
    return loadIncidentDetail(state.publicIncidentSelectedId || state.publicIncidents[0].id);
  }

  function handleIncidentViewportChange() {
    var wasMobile = !!state.publicIncidentMobileLayout;
    var isMobile = syncIncidentLayoutState();
    if (wasMobile === isMobile) {
      return;
    }
    renderIncidentFeed();
    if (!isMobile) {
      ensureIncidentDetailForLayout()
        .then(function () {
          renderIncidentFeed();
        })
        .catch(function (error) {
          setStatus((error && error.message) || "Neizdevās atjaunot incidentu detaļas");
        });
    }
  }

  function handleIncidentPopState(event) {
    if (String(config.mode || "public") !== "public-incidents") {
      return;
    }
    syncIncidentLayoutState();
    if (state.publicIncidentHistoryNavigating) {
      state.publicIncidentHistoryNavigating = false;
      return;
    }
    if (!state.publicIncidentMobileLayout) {
      state.publicIncidentHistoryOpen = isIncidentOverlayHistoryState(event && event.state);
      return;
    }
    if (isIncidentOverlayHistoryState(event && event.state)) {
      if (state.publicIncidentSelectedId && state.publicIncidentDetail) {
        state.publicIncidentDetailOpen = true;
        state.publicIncidentHistoryOpen = true;
        renderIncidentFeed();
      }
      return;
    }
    if (state.publicIncidentDetailOpen || state.publicIncidentHistoryOpen) {
      closeIncidentDetailOverlay({ skipHistory: true, force: true });
    }
  }

  function boot() {
    if (!root.document || !document.getElementById) {
      return;
    }
    notifyTelegramMiniAppReady();
    syncPageTitle();
    var app = document.getElementById("app");
    if (!app) {
      return;
    }
    if (String(config.mode || "public") === "public-incidents") {
      state.publicIncidentsLoading = true;
      state.publicIncidentsLoaded = false;
      state.publicIncidentDetailLoading = false;
      state.publicIncidentDetailLoadingId = "";
      app.innerHTML =
        '<div class="shell">' +
        '<header class="hero">' +
        '<div class="hero-copy"><p class="eyebrow">Pēdējās 24 stundas</p><h1>Kontroles plūsma</h1><p class="lede">Aktīvie kontroles ziņojumi, anonīmi balsojumi un komentāri vienuviet.</p></div>' +
        renderHeroMeta("public-incidents") +
        "</header>" +
        '<section class="layout">' +
        '<section class="card"><h2>Pēdējās 24 stundas</h2><div id="incident-list">' + loadingStateHTML("Ielādē notiekošo…") + "</div></section>" +
        '<aside id="incident-detail-panel" class="sidebar incident-detail-panel"><section class="card"><h2>Detalizēti</h2><div id="incident-detail">' + loadingStateHTML("Gatavojam detaļas…") + "</div></section></aside>" +
      "</section>" +
      "</div>";
      syncIncidentLayoutState();
      bindActions();
      restoreAuthFeedbackFromURL();
      renderAuthControls();
      setStatus(readyStatusText());
      syncLoadingIndicators();
      Promise.resolve()
        .then(function () {
          return completePendingTelegramAuthResult({ refresh: false });
        })
        .then(function (handledTelegramAuth) {
          return handledTelegramAuth ? true : completePendingTelegramMiniAppAuth({ refresh: false });
        })
        .then(function (handledTelegramAuth) {
          return handledTelegramAuth ? null : bootstrapSession();
        })
        .then(loadIncidents)
        .then(ensureIncidentDetailForLayout)
        .then(function () {
          renderIncidentFeed();
          startIncidentFeedPolling();
        })
        .catch(function (error) {
          setStatus(error.message || "Failed to load");
        });
      return;
    }
    state.publicMapLoading = true;
    state.publicMapLoaded = false;
    app.innerHTML =
      '<div class="shell">' +
      '<header class="hero">' +
      '<div class="hero-copy"><h1>Kontrole</h1><p class="lede">Satiksmes karte, aktīvais transports un kontroles ziņojumi vienuviet.</p></div>' +
      renderHeroMeta(String(config.mode || "public")) +
      "</header>" +
      '<section class="layout">' +
      '<div class="map-panel"><div class="map-shell"><div id="map-loading-indicator" class="map-loading-indicator">' + loadingStateHTML("Savienojam ar tiešraides datiem…", "loading-state-compact") + '</div><div class="map-viewport"><div id="map" class="map"></div></div><div id="map-detail-overlay" class="map-detail-overlay" hidden></div></div></div>' +
      '<aside class="sidebar">' +
      '<section class="card"><h2>Izvēlētā pietura</h2><div id="selected-stop">Izvēlies pieturu mapē.</div></section>' +
      '<section class="card"><h2>Jaunākie ziņojumi</h2><div id="recent-sightings">' + loadingStateHTML("Ielādē ziņojumus…") + "</div></section>" +
      "</aside>" +
      "</section>" +
      "</div>";

    initMap();
    bindActions();
    restoreAuthFeedbackFromURL();
    renderAuthControls();
    setStatus(readyStatusText());
    syncLoadingIndicators();
    Promise.resolve()
      .then(function () {
        return completePendingTelegramAuthResult({ refresh: false });
      })
      .then(function (handledTelegramAuth) {
        return handledTelegramAuth ? true : completePendingTelegramMiniAppAuth({ refresh: false });
      })
      .then(function (handledTelegramAuth) {
        return handledTelegramAuth ? null : bootstrapSession();
      })
      .then(loadBootstrap)
      .then(function () {
        startLiveMapPolling();
      })
      .then(requestLocation)
      .catch(function (error) {
        setStatus(error.message || "Failed to load");
      });
  }

  function initMap() {
    if (!root.L || !document.getElementById("map")) {
      return;
    }
    state.map = root.L.map("map", { zoomControl: true }).setView([defaultCenter.lat, defaultCenter.lng], defaultCenter.zoom);
    root.L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
      attribution: '&copy; OpenStreetMap contributors',
      maxZoom: 19,
    }).addTo(state.map);
    var syncVisibleStops = function () {
      finishUserMapGesture("user-moved-map", { deferIfZoomPending: true });
      renderVisibleStops();
    };
    var syncVisibleZoomLayers = function () {
      finishUserMapGesture("user-zoomed-map");
      renderVisibleStops();
      renderLiveVehicles();
    };
    state.map.on("movestart", function () {
      beginUserMapGesture("move");
    });
    state.map.on("zoomstart", function () {
      beginUserMapGesture("zoom");
    });
    state.map.on("move", function () {
      syncMapDetailOverlayPosition();
    });
    state.map.on("zoom", function () {
      syncMapDetailOverlayPosition();
    });
    state.map.on("resize", function () {
      syncMapDetailOverlayPosition();
    });
    state.map.on("moveend", syncVisibleStops);
    state.map.on("zoomend", syncVisibleZoomLayers);
    addUserLocationControl();
    observeMapViewportResize();
    scheduleLeafletViewportSync({ force: true });
  }

  function applyLiveMapPayload(payload) {
    state.sightings = payload.sightings || { stopSightings: [], vehicleSightings: [] };
    state.stopIncidents = Array.isArray(payload.stopIncidents) ? payload.stopIncidents : [];
    state.vehicles = payload.liveVehicles || [];
    markPublicMapLoaded();
    syncStopActivityCounts();
    renderVisibleStops();
    renderLiveVehicles();
    applySelectedVehicleFollow();
    renderSightings();
    renderSelectedStop();
    return payload;
  }

  function applyMapPayload(payload) {
    setCatalogState({
      generatedAt: payload.generatedAt,
      stops: payload.stops || [],
      routes: payload.routes || [],
    });
    return applyLiveMapPayload(payload);
  }

  function loadCatalog(options) {
    var silent = !!(options && options.silent);
    var deferRender = !!(options && options.deferRender);
    if (!config.bundleActiveURL) {
      return Promise.reject(new Error("Statiskais katalogs nav pieejams"));
    }
    return fetchJSON(bundleURLFor(config.bundleActiveURL))
      .then(function (active) {
        return fetchJSON(bundleURLFor(active && active.manifestPath)).then(function (manifest) {
          return Promise.all([
            fetchJSON(bundleURLFor((active && active.manifestPath ? active.manifestPath.replace(/manifest\.json$/, "") : "") + (manifest.slices ? manifest.slices.stops : "stops.json"))),
            fetchJSON(bundleURLFor((active && active.manifestPath ? active.manifestPath.replace(/manifest\.json$/, "") : "") + (manifest.slices ? manifest.slices.routes : "routes.json"))),
          ]).then(function (results) {
            var payload = {
              generatedAt: active.generatedAt || manifest.generatedAt,
              bundleVersion: active.version || manifest.version,
              bundleGeneratedAt: active.generatedAt || manifest.generatedAt,
              stops: results[0] || [],
              routes: results[1] || [],
            };
            setCatalogState(payload);
            if (!deferRender) {
              renderVisibleStops();
            }
            if (!silent) {
              setStatus("Katalogs ielādēts");
            }
            return payload;
          });
        });
      });
  }

  function loadSharedMapStateDirect() {
    return ensureLiveTransportRealtimeStarted().then(function () {
      var snapshot = currentSpacetimeSharedMapSnapshot();
      if (snapshot && snapshot.sightings) {
        applySharedMapCollections(
          snapshot.sightings || { stopSightings: [], vehicleSightings: [] },
          Array.isArray(snapshot.incidents) ? snapshot.incidents : []
        );
        return {
          sightings: state.sightings,
          stopIncidents: state.stopIncidents,
          vehicleIncidents: state.vehicleIncidents,
          liveVehicles: state.vehicles,
        };
      }
      return Promise.all([
        callSpacetimeProcedure("satiksmebot_list_public_sightings", ["", sightingsFetchLimit], { allowAnonymous: true }),
        callSpacetimeProcedure("satiksmebot_list_public_incidents", [0], { allowAnonymous: true }),
      ]).then(function (results) {
        applySharedMapCollections(
          results[0] || { stopSightings: [], vehicleSightings: [] },
          Array.isArray(results[1] && results[1].incidents) ? results[1].incidents : []
        );
        return {
          sightings: state.sightings,
          stopIncidents: state.stopIncidents,
          vehicleIncidents: state.vehicleIncidents,
          liveVehicles: state.vehicles,
        };
      });
    });
  }

  function loadBackendSharedMapState() {
    return Promise.all([
      fetchJSON(pathFor("/api/v1/public/sightings?limit=" + sightingsFetchLimit)),
      fetchJSON(pathFor("/api/v1/public/incidents")),
    ]).then(function (results) {
      applySharedMapCollections(
        results[0] || { stopSightings: [], vehicleSightings: [] },
        Array.isArray(results[1] && results[1].incidents) ? results[1].incidents : []
      );
      return {
        sightings: state.sightings,
        stopIncidents: state.stopIncidents,
        vehicleIncidents: state.vehicleIncidents,
        liveVehicles: state.vehicles,
      };
    });
  }

  function loadSnapshotBackedMapState() {
    return Promise.all([
      loadBackendSharedMapState(),
      ensureLiveTransportRealtimeStarted().then(function () {
        return syncLiveTransportRealtimeState();
      }),
    ]).then(function () {
      return {
        sightings: state.sightings,
        stopIncidents: state.stopIncidents,
        vehicleIncidents: state.vehicleIncidents,
        liveVehicles: state.vehicles,
      };
    });
  }

  function loadBootstrap() {
    if (!state.publicMapLoaded) {
      setPublicMapLoading(true);
      renderSightings();
      renderSelectedStop();
    }
    var liveStatePromise = spacetimeEnabled()
      ? loadSharedMapStateDirect()
      : (liveTransportSnapshotLookupEnabled() ? loadSnapshotBackedMapState() : fetchJSON(pathFor("/api/v1/public/map-live?limit=" + sightingsFetchLimit)));
    return Promise.all([
      loadCatalog({ silent: true, deferRender: true }),
      liveStatePromise,
      spacetimeEnabled() ? ensureLiveTransportRealtimeStarted() : Promise.resolve(false),
    ]).then(function (results) {
      if (!spacetimeEnabled() && !liveTransportSnapshotLookupEnabled()) {
        applyLiveMapPayload(results[1]);
      }
      setStatus(readyStatusText());
      return results[1];
    }).finally(function () {
      setPublicMapLoading(false);
      renderSightings();
      renderSelectedStop();
    });
  }

  function loadSightings() {
    if (spacetimeEnabled()) {
      return ensureLiveTransportRealtimeStarted().then(function () {
        var snapshot = currentSpacetimeSharedMapSnapshot();
        if (snapshot && snapshot.sightings) {
          state.sightings = snapshot.sightings;
          syncStopActivityCounts();
          renderVisibleStops();
          rebuildMergedLiveVehicles();
          renderSightings();
          renderSelectedStop();
          return snapshot.sightings;
        }
        return callSpacetimeProcedure("satiksmebot_list_public_sightings", ["", sightingsFetchLimit], { allowAnonymous: true }).then(function (payload) {
          state.sightings = payload;
          syncStopActivityCounts();
          renderVisibleStops();
          rebuildMergedLiveVehicles();
          renderSightings();
          renderSelectedStop();
          return payload;
        });
      });
    }
    return fetchJSON(pathFor("/api/v1/public/sightings?limit=" + sightingsFetchLimit)).then(function (payload) {
      state.sightings = payload;
      syncStopActivityCounts();
      renderVisibleStops();
      renderLiveVehicles();
      renderSightings();
      renderSelectedStop();
      return payload;
    });
  }

  function applyPublicIncidentList(nextItems) {
    var items = Array.isArray(nextItems) ? nextItems : [];
    var missingSelected = false;
    var requestedIncidentId = selectedIncidentIdFromURL();
    var changed = !sameMaterialValue(state.publicIncidents, items);
    state.stopIncidents = stopIncidentsForMap(items);
    state.vehicleIncidents = vehicleIncidentsForMap(items);
    rebuildMergedLiveVehicles();
    if (changed) {
      state.publicIncidents = items;
    }
    syncIncidentLayoutState();
    if (requestedIncidentId && items.some(function (item) { return item.id === requestedIncidentId; })) {
      state.publicIncidentSelectedId = requestedIncidentId;
      if (state.publicIncidentMobileLayout) {
        state.publicIncidentDetailOpen = true;
      }
    }
    if (state.publicIncidentSelectedId && !items.some(function (item) { return item.id === state.publicIncidentSelectedId; })) {
      missingSelected = true;
    }
    if (missingSelected) {
      state.publicIncidentSelectedId = state.publicIncidentMobileLayout ? "" : (items[0] ? items[0].id : "");
      state.publicIncidentDetail = null;
      if (state.publicIncidentMobileLayout && state.publicIncidentDetailOpen) {
        closeIncidentDetailOverlay({ force: true });
      }
    }
    if (!items.length) {
      syncIncidentURL("");
    }
    syncLiveTransportClientScope();
    markPublicIncidentsLoaded();
    setPublicIncidentsLoading(false);
    setStatus(readyStatusText());
    return changed;
  }

  function loadIncidents() {
    var showInitialLoading = !state.publicIncidentsLoaded && !state.publicIncidents.length;
    var request = null;
    if (showInitialLoading) {
      setPublicIncidentsLoading(true);
      renderIncidentList();
    }
    if (spacetimeEnabled()) {
      request = ensureLiveTransportRealtimeStarted().then(function () {
        var snapshotItems = currentSpacetimeIncidentList();
        if (snapshotItems) {
          return applyPublicIncidentList(snapshotItems);
        }
        return callSpacetimeProcedure("satiksmebot_list_public_incidents", [0], { allowAnonymous: true }).then(function (payload) {
          return applyPublicIncidentList(Array.isArray(payload.incidents) ? payload.incidents : []);
        });
      });
    } else {
      request = fetchJSON(pathFor("/api/v1/public/incidents")).then(function (payload) {
        return applyPublicIncidentList(Array.isArray(payload.incidents) ? payload.incidents : []);
      });
    }
    return request.finally(function () {
      setPublicIncidentsLoading(false);
      renderIncidentList();
    });
  }

  function loadIncidentDetail(incidentId) {
    var nextIncidentId = String(incidentId || "").trim();
    var currentDetailId = state.publicIncidentDetail && state.publicIncidentDetail.summary
      ? String(state.publicIncidentDetail.summary.id || "").trim()
      : "";
    var showInitialLoading = !!nextIncidentId && currentDetailId !== nextIncidentId;
    if (!nextIncidentId) {
      state.publicIncidentSelectedId = "";
      state.publicIncidentDetail = null;
      setPublicIncidentDetailLoading("", false);
      syncIncidentURL("");
      syncLiveTransportClientScope();
      return Promise.resolve(false);
    }
    state.publicIncidentSelectedId = nextIncidentId;
    syncIncidentURL(nextIncidentId);
    syncLiveTransportClientScope();
    if (showInitialLoading) {
      setPublicIncidentDetailLoading(nextIncidentId, true);
      renderIncidentDetail();
    }
    if (spacetimeEnabled()) {
      return ensureLiveTransportRealtimeStarted().then(function () {
        var snapshotDetail = currentSpacetimeIncidentDetail(nextIncidentId);
        if (snapshotDetail) {
          var changed = !sameMaterialValue(state.publicIncidentDetail, snapshotDetail);
          if (changed) {
            state.publicIncidentDetail = snapshotDetail;
          }
          return changed;
        }
        return callSpacetimeProcedure("satiksmebot_get_public_incident_detail", [nextIncidentId], { allowAnonymous: true }).then(function (payload) {
          state.publicIncidentSelectedId = nextIncidentId;
          syncIncidentURL(nextIncidentId);
          syncLiveTransportClientScope();
          var changed = !sameMaterialValue(state.publicIncidentDetail, payload);
          if (changed) {
            state.publicIncidentDetail = payload;
          }
          return changed;
        });
      }).finally(function () {
        setPublicIncidentDetailLoading(nextIncidentId, false);
        renderIncidentDetail();
      });
    }
    return fetchJSON(pathFor("/api/v1/public/incidents/" + encodeURIComponent(nextIncidentId))).then(function (payload) {
      state.publicIncidentSelectedId = nextIncidentId;
      syncIncidentURL(nextIncidentId);
      syncLiveTransportClientScope();
      var changed = !sameMaterialValue(state.publicIncidentDetail, payload);
      if (changed) {
        state.publicIncidentDetail = payload;
      }
      return changed;
    }).finally(function () {
      setPublicIncidentDetailLoading(nextIncidentId, false);
      renderIncidentDetail();
    });
  }

  function loadLiveVehicles() {
    if (spacetimeEnabled() || liveTransportSnapshotLookupEnabled()) {
      return ensureLiveTransportRealtimeStarted().then(function () {
        return syncLiveTransportRealtimeState();
      }).then(function () {
        return { liveVehicles: state.vehicles };
      });
    }
    return fetchJSON(pathFor("/api/v1/public/live-vehicles")).then(function (payload) {
      state.vehicles = payload.liveVehicles || [];
      renderLiveVehicles();
      applySelectedVehicleFollow();
      return payload;
    });
  }

  function loadLiveMapState() {
    if (spacetimeEnabled()) {
      return loadSharedMapStateDirect().then(function (payload) {
        return syncLiveTransportRealtimeState().then(function () {
          return payload;
        });
      });
    }
    if (liveTransportSnapshotLookupEnabled()) {
      return loadSnapshotBackedMapState();
    }
    return fetchJSON(pathFor("/api/v1/public/map-live?limit=" + sightingsFetchLimit)).then(function (payload) {
      var needsCatalogRefresh = !state.catalog || String(state.catalog.generatedAt || "") !== String(payload.generatedAt || "");
      var catalogPromise = needsCatalogRefresh ? loadCatalog({ silent: true, deferRender: true }) : Promise.resolve(state.catalog);
      return catalogPromise.then(function () {
        applyLiveMapPayload(payload);
        return {
          sightings: state.sightings,
          stopIncidents: state.stopIncidents,
          liveVehicles: state.vehicles,
        };
      });
    });
  }

  function refreshLiveMap() {
    if (state.liveRefreshInFlight) {
      return Promise.resolve(null);
    }
    state.liveRefreshInFlight = true;
    return loadLiveMapState().finally(function () {
      state.liveRefreshInFlight = false;
    });
  }

  function startLiveMapPolling() {
    bindLiveTransportLifecycleEvents();
    stopLiveMapRefreshTimer();
    if (liveTransportRealtimeEnabled() || liveTransportSnapshotLookupEnabled()) {
      if (documentVisible()) {
        startLiveTransportHeartbeat();
      } else {
        stopLiveTransportHeartbeat();
      }
    }
    if (!state.vehicles.length && !(state.stopIncidents && state.stopIncidents.length)) {
      refreshLiveMap().catch(function () {
        setStatus("Tiešraides transports nav pieejams");
      });
    }
    if (liveTransportRealtimeEnabled() || !documentVisible()) {
      return;
    }
    state.liveVehiclesRefreshTimer = setInterval(function () {
      refreshLiveMap().catch(function () {
        return null;
      });
    }, liveMapRefreshMs);
  }

  function applyAuthenticatedSession(payload) {
    clearAuthFeedback();
    persistSpacetimeSession(payload && payload.spacetime ? payload.spacetime : null);
    state.authenticated = true;
    state.authState = "authenticated";
    renderAuthDependentUI();
    setStatus(readyStatusText());
    if (state.liveTransportClient && typeof state.liveTransportClient.connect === "function") {
      void state.liveTransportClient.connect(currentSpacetimeSession());
    }
    return payload || null;
  }

  function applyAnonymousSession(authState) {
    var nextAuthState = String(authState || "anonymous");
    if (nextAuthState !== "authenticated" && nextAuthState !== "unknown") {
      nextAuthState = "anonymous";
    }
    persistSpacetimeSession(null);
    state.authenticated = false;
    state.authState = nextAuthState;
    renderAuthDependentUI();
    setStatus(readyStatusText());
    if (state.liveTransportClient && typeof state.liveTransportClient.connect === "function") {
      void state.liveTransportClient.connect(currentSpacetimeSession());
    }
    return null;
  }

  function bootstrapSession() {
    return fetchJSON(pathFor("/api/v1/me"), {
      method: "GET",
      credentials: "same-origin",
    })
      .then(function (payload) {
        return applyAuthenticatedSession(payload);
      })
      .catch(function (error) {
        if (error && error.status === 401) {
          return applyAnonymousSession("anonymous");
        }
        logAuthFailure("bootstrap-session", error);
        applyAnonymousSession("anonymous");
        return null;
      });
  }

  function refreshSpacetimeSession() {
    return fetchJSON(pathFor("/api/v1/me"), {
      method: "GET",
      credentials: "same-origin",
    })
      .then(function (payload) {
        return applyAuthenticatedSession(payload);
      })
      .catch(function (error) {
        if (error && error.status === 401) {
          applyAnonymousSession("anonymous");
          return null;
        }
        logAuthFailure("spacetime-session-refresh", error);
        throw error;
      });
  }

  function currentReturnTo() {
    var win = windowHandle();
    if (!win || !win.location) {
      return pathFor("/");
    }
    return String(win.location.pathname || "/") + String(win.location.search || "");
  }

  function telegramLoginConfigURL() {
    return pathFor("/api/v1/auth/telegram/config");
  }

  function telegramLoginPopupOrigin() {
    return "https://oauth.telegram.org";
  }

  function telegramLoginRedirectURI(loginConfig) {
    var redirectURI = String((loginConfig && loginConfig.redirectUri) || "").trim();
    if (redirectURI) {
      return redirectURI;
    }
    return String((currentURL() && currentURL().origin) || "").trim() || "https://kontrole.info/";
  }

  function telegramLoginOrigin(loginConfig) {
    var origin = String((loginConfig && loginConfig.origin) || "").trim();
    if (origin) {
      return origin;
    }
    return String((currentURL() && currentURL().origin) || "").trim() || "https://kontrole.info";
  }

  function telegramLoginScope(loginConfig) {
    var scopes = ["openid"];
    var rawScopes = loginConfig && Array.isArray(loginConfig.scopes) ? loginConfig.scopes : [];
    rawScopes.forEach(function (value) {
      var scope = String(value || "").trim();
      if (!scope) {
        return;
      }
      if (scopes.indexOf(scope) === -1) {
        scopes.push(scope);
      }
    });
    return scopes.join(" ");
  }

  function telegramLoginPopupURL(loginConfig) {
    var params = new root.URLSearchParams();
    params.set("response_type", "post_message");
    params.set("client_id", String((loginConfig && loginConfig.clientId) || "").trim());
    params.set("origin", telegramLoginOrigin(loginConfig));
    params.set("redirect_uri", telegramLoginRedirectURI(loginConfig));
    params.set("scope", telegramLoginScope(loginConfig));
    if (loginConfig && loginConfig.nonce) {
      params.set("nonce", String(loginConfig.nonce));
    }
    params.set("lang", "lv");
    return telegramLoginPopupOrigin() + "/auth?" + params.toString();
  }

  function telegramLoginPopupFeatures() {
    var win = windowHandle();
    var screenObject = (win && win.screen) || root.screen || {};
    var width = 550;
    var height = 650;
    var screenWidth = typeof screenObject.width === "number" && screenObject.width > 0 ? screenObject.width : 1280;
    var screenHeight = typeof screenObject.height === "number" && screenObject.height > 0 ? screenObject.height : 900;
    var availLeft = typeof screenObject.availLeft === "number" ? screenObject.availLeft : 0;
    var availTop = typeof screenObject.availTop === "number" ? screenObject.availTop : 0;
    var left = Math.max(0, (screenWidth - width) / 2) + availLeft;
    var top = Math.max(0, (screenHeight - height) / 2) + availTop;
    return [
      "width=" + width,
      "height=" + height,
      "left=" + left,
      "top=" + top,
      "status=0",
      "location=0",
      "menubar=0",
      "toolbar=0",
    ].join(",");
  }

  function parseTelegramLoginMessageData(value) {
    if (!value) {
      return null;
    }
    if (typeof value === "string") {
      try {
        return JSON.parse(value);
      } catch (_error) {
        return null;
      }
    }
    if (typeof value === "object") {
      return value;
    }
    return null;
  }

  function decodeTelegramAuthResult(value) {
    var encoded = String(value || "").trim();
    var normalized = encoded.replace(/-/g, "+").replace(/_/g, "/");
    var decoded = "";
    var binary = "";
    var bytes = null;
    var i = 0;
    while (normalized.length % 4) {
      normalized += "=";
    }
    if (!normalized) {
      throw new Error("missing Telegram auth result");
    }
    if (root.Buffer && typeof root.Buffer.from === "function") {
      decoded = root.Buffer.from(normalized, "base64").toString("utf8");
      return JSON.parse(decoded);
    }
    if (typeof root.atob !== "function") {
      throw new Error("base64 decoder unavailable");
    }
    binary = root.atob(normalized);
    if (typeof root.TextDecoder === "function" && typeof root.Uint8Array === "function") {
      bytes = new root.Uint8Array(binary.length);
      for (i = 0; i < binary.length; i += 1) {
        bytes[i] = binary.charCodeAt(i);
      }
      decoded = new root.TextDecoder("utf-8").decode(bytes);
    } else {
      try {
        decoded = decodeURIComponent(
          binary
            .split("")
            .map(function (char) {
              return "%" + ("00" + char.charCodeAt(0).toString(16)).slice(-2);
            })
            .join("")
        );
      } catch (_error) {
        decoded = binary;
      }
    }
    return JSON.parse(decoded);
  }

  function consumeTelegramAuthResultFromURL() {
    var win = windowHandle();
    var location = win && win.location ? win.location : null;
    var history = win && win.history ? win.history : null;
    var url = null;
    var hash = "";
    var params = null;
    var rawResult = "";
    if (!location || !history || typeof history.replaceState !== "function") {
      return null;
    }
    hash = String(location.hash || "");
    if (!hash || hash.indexOf("tgAuthResult=") === -1) {
      return null;
    }
    try {
      url = new URL(String(location.href || ""), String(location.origin || ""));
      params = new root.URLSearchParams(hash.replace(/^#/, ""));
    } catch (_error) {
      return null;
    }
    rawResult = String(params.get("tgAuthResult") || "");
    if (!rawResult) {
      return null;
    }
    params.delete("tgAuthResult");
    url.hash = params.toString() ? "#" + params.toString() : "";
    history.replaceState(history.state || null, "", url.pathname + url.search + url.hash);
    return decodeTelegramAuthResult(rawResult);
  }

  function ensureTelegramLoginLibrary() {
    return Promise.resolve(true);
  }

  function popupBlockedAuthError() {
    var error = new Error("popup blocked");
    error.code = "popup_blocked";
    return error;
  }

  function cancelledAuthError() {
    var error = new Error("popup closed");
    error.code = "cancelled";
    return error;
  }

  function runTelegramLoginPopup(loginConfig) {
    return new Promise(function (resolve, reject) {
      var win = windowHandle();
      var popup = null;
      var settled = false;
      var closeTimer = 0;
      var closeGraceTimer = 0;

      if (!win || typeof win.open !== "function" || typeof win.addEventListener !== "function") {
        reject(new Error("Telegram Login nav pieejams šajā pārlūkā"));
        return;
      }

      function resolveOnce(value) {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        resolve(value);
      }

      function rejectOnce(error) {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        reject(error);
      }

      function cleanup() {
        if (closeTimer) {
          clearTimeout(closeTimer);
          closeTimer = 0;
        }
        if (closeGraceTimer) {
          clearTimeout(closeGraceTimer);
          closeGraceTimer = 0;
        }
        if (typeof win.removeEventListener === "function") {
          win.removeEventListener("message", handleMessage);
        }
      }

      function handleMessage(event) {
        var data = null;
        var appOrigin = telegramLoginOrigin(loginConfig);
        if (!event) {
          return;
        }
        if (popup && event.source && event.source !== popup) {
          return;
        }
        if (event.origin === appOrigin) {
          data = parseTelegramLoginMessageData(event.data);
          if (!data || data.event !== "kontrole_telegram_auth_result") {
            return;
          }
          if (data.ok) {
            resolveOnce({
              sessionComplete: true,
              payload: data.payload || null,
            });
            return;
          }
          rejectOnce(new Error(String((data.error && data.error.message) || data.error || "Telegram login failed")));
          return;
        }
        if (event.origin !== telegramLoginPopupOrigin()) {
          return;
        }
        data = parseTelegramLoginMessageData(event.data);
        if (!data || data.event !== "auth_result") {
          return;
        }
        if (data.error) {
          if (String(data.error) === "popup_closed") {
            rejectOnce(cancelledAuthError());
            return;
          }
          rejectOnce(new Error(String(data.error)));
          return;
        }
        if (data.result && typeof data.result === "object") {
          resolveOnce({
            widgetAuth: data.result,
          });
          return;
        }
        if (typeof data.result !== "string" || !data.result) {
          rejectOnce(new Error("missing Telegram id_token"));
          return;
        }
        resolveOnce(String(data.result));
      }

      function checkClosed() {
        if (settled) {
          return;
        }
        if (!popup || popup.closed) {
          closeGraceTimer = setTimeout(function () {
            if (!settled) {
              rejectOnce(cancelledAuthError());
            }
          }, 300);
          return;
        }
        closeTimer = setTimeout(checkClosed, 200);
      }

      try {
        win.addEventListener("message", handleMessage);
        popup = win.open(telegramLoginPopupURL(loginConfig), "telegram_oidc_login", telegramLoginPopupFeatures());
      } catch (error) {
        rejectOnce(error);
        return;
      }

      if (!popup) {
        rejectOnce(popupBlockedAuthError());
        return;
      }
      if (typeof popup.focus === "function") {
        popup.focus();
      }
      checkClosed();
    });
  }

  function completeTelegramLogin(idToken) {
    return fetchJSON(pathFor("/api/v1/auth/telegram/complete"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ idToken: String(idToken || "") }),
    }).then(function (payload) {
      if (!payload || payload.authenticated !== true) {
        throw new Error("missing authenticated session");
      }
      return payload;
    });
  }

  function completeTelegramWidgetLogin(widgetAuth) {
    return fetchJSON(pathFor("/api/v1/auth/telegram/complete"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ widgetAuth: widgetAuth || {} }),
    }).then(function (payload) {
      if (!payload || payload.authenticated !== true) {
        throw new Error("missing authenticated session");
      }
      return payload;
    });
  }

  function completeTelegramMiniAppLogin(initData) {
    return fetchJSON(pathFor("/api/v1/auth/telegram/complete"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ initData: String(initData || "") }),
    }).then(function (payload) {
      if (!payload || payload.authenticated !== true) {
        throw new Error("missing authenticated session");
      }
      return payload;
    });
  }

  function completePendingTelegramMiniAppAuth(options) {
    var opts = options || {};
    var initData = telegramMiniAppInitData();
    if (!initData) {
      return Promise.resolve(false);
    }
    startAuthFeedback();
    return completeTelegramMiniAppLogin(initData)
      .then(function (payload) {
        applyAuthenticatedSession(payload);
        if (opts.refresh === false) {
          return true;
        }
        return refreshAfterAuthChange()
          .catch(function (error) {
            logAuthFailure("telegram-mini-app-success-refresh", error);
            return null;
          })
          .then(function () {
            return true;
          });
      })
      .catch(function (error) {
        logAuthFailure("telegram-mini-app-login", error);
        finishAuthFeedback("error", authErrorStatusText());
        return false;
      });
  }

  function postTelegramAuthResultToOpener(ok, payloadOrError) {
    var win = windowHandle();
    var origin = (currentURL() && currentURL().origin) || telegramLoginOrigin();
    if (!win || !win.opener || win.opener.closed || typeof win.opener.postMessage !== "function") {
      return false;
    }
    try {
      win.opener.postMessage(
        {
          event: "kontrole_telegram_auth_result",
          ok: !!ok,
          payload: ok ? payloadOrError || null : null,
          error: ok ? null : { message: String((payloadOrError && payloadOrError.message) || payloadOrError || authErrorStatusText()) },
        },
        origin
      );
      return true;
    } catch (_error) {
      return false;
    }
  }

  function closeTelegramAuthPopupSoon() {
    var win = windowHandle();
    if (!win || typeof win.close !== "function") {
      return;
    }
    root.setTimeout(function () {
      try {
        win.close();
      } catch (_error) {
        return;
      }
    }, 0);
  }

  function completePendingTelegramAuthResult(options) {
    var opts = options || {};
    var widgetAuth = null;
    try {
      widgetAuth = consumeTelegramAuthResultFromURL();
    } catch (error) {
      logAuthFailure("telegram-result-decode", error);
      finishAuthFeedback("error", authErrorStatusText());
      return Promise.resolve(true);
    }
    if (!widgetAuth) {
      return Promise.resolve(false);
    }
    startAuthFeedback();
    return completeTelegramWidgetLogin(widgetAuth)
      .then(function (payload) {
        if (postTelegramAuthResultToOpener(true, payload)) {
          closeTelegramAuthPopupSoon();
          return true;
        }
        applyAuthenticatedSession(payload);
        if (opts.refresh === false) {
          return true;
        }
        return refreshAfterAuthChange()
          .catch(function (error) {
            logAuthFailure("telegram-widget-success-refresh", error);
            return null;
          })
          .then(function () {
            return true;
          });
      })
      .catch(function (error) {
        logAuthFailure("telegram-widget-login", error);
        if (postTelegramAuthResultToOpener(false, error)) {
          closeTelegramAuthPopupSoon();
          return true;
        }
        finishAuthFeedback("error", authErrorStatusText());
        return true;
      });
  }

  function fetchTelegramLoginConfig() {
    return fetchJSON(telegramLoginConfigURL(), {
      method: "GET",
      credentials: "same-origin",
    }).then(function (payload) {
      if (
        !payload ||
        typeof payload.clientId !== "string" ||
        !payload.clientId ||
        typeof payload.nonce !== "string" ||
        !payload.nonce ||
        typeof payload.origin !== "string" ||
        !payload.origin ||
        typeof payload.redirectUri !== "string" ||
        !payload.redirectUri
      ) {
        throw new Error("invalid Telegram login config");
      }
      return payload;
    });
  }

  function handleTelegramLoginError(error) {
    if (error && error.code === "cancelled") {
      finishAuthFeedback("cancelled");
      return null;
    }
    if (error && error.code === "popup_blocked") {
      finishAuthFeedback("popup_blocked");
      return null;
    }
    logAuthFailure("telegram-login", error);
    finishAuthFeedback("error", authErrorStatusText());
    return null;
  }

  function consumeTelegramAuthStatusFromURL() {
    var win = windowHandle();
    var location = win && win.location ? win.location : null;
    var history = win && win.history ? win.history : null;
    var url = null;
    var status = "";
    if (!location || !history || typeof history.replaceState !== "function") {
      return "";
    }
    try {
      url = new URL(String(location.href || ""), String(location.origin || ""));
    } catch (_error) {
      return "";
    }
    status = String(url.searchParams.get("tgAuth") || "").trim().toLowerCase();
    if (!status) {
      return "";
    }
    url.searchParams.delete("tgAuth");
    history.replaceState(history.state || null, "", url.pathname + url.search + url.hash);
    return status;
  }

  function restoreAuthFeedbackFromURL() {
    var status = consumeTelegramAuthStatusFromURL();
    if (status === "cancelled") {
      state.authInProgress = false;
      state.authFeedback = {
        kind: "cancelled",
        message: authFeedbackText("cancelled"),
      };
      return;
    }
    if (status === "failed") {
      state.authInProgress = false;
      state.authFeedback = {
        kind: "error",
        message: authFeedbackText("error", authErrorStatusText()),
      };
    }
  }

  function refreshAfterAuthChange() {
    if (String(config.mode || "public") === "public-incidents") {
      return loadIncidents()
        .then(ensureIncidentDetailForLayout)
        .then(function () {
          renderIncidentFeed();
          return null;
        });
    }
    return loadLiveMapState().then(function () {
      renderSelectedStop();
      return null;
    });
  }

  function beginTelegramLogin() {
    startAuthFeedback();
    return fetchTelegramLoginConfig()
      .then(function (loginConfig) {
        return ensureTelegramLoginLibrary().then(function () {
          return runTelegramLoginPopup(loginConfig);
        });
      })
      .then(function (loginResult) {
        if (loginResult && loginResult.sessionComplete) {
          if (loginResult.payload && loginResult.payload.authenticated === true) {
            return loginResult.payload;
          }
          return bootstrapSession();
        }
        if (loginResult && loginResult.widgetAuth) {
          return completeTelegramWidgetLogin(loginResult.widgetAuth);
        }
        return completeTelegramLogin(loginResult);
      })
      .then(function (payload) {
        applyAuthenticatedSession(payload);
        return refreshAfterAuthChange().catch(function (error) {
          logAuthFailure("telegram-login-success-refresh", error);
          return null;
        });
      })
      .catch(handleTelegramLoginError);
  }

  function logout() {
    return fetchJSON(pathFor("/api/v1/auth/logout"), {
      method: "POST",
      credentials: "same-origin",
    })
      .then(function () {
        clearAuthFeedback();
        applyAnonymousSession("anonymous");
        return refreshAfterAuthChange();
      })
      .catch(function (error) {
        if (error && error.message) {
          setStatus(error.message);
        }
        return null;
      });
  }

  function requestLocation(options) {
    var settings = options || {};
    var win = windowHandle();
    var nav = (win && win.navigator) || root.navigator || (typeof navigator !== "undefined" ? navigator : null);
    if (!nav || !nav.geolocation) {
      if (settings.userInitiated) {
        setStatus("Atrašanās vieta nav pieejama");
      }
      selectInitialStop();
      return Promise.resolve(false);
    }
    if (state.locationRequestInFlight) {
      return state.locationRequestInFlight.then(function (resolved) {
        if (settings.centerOnSuccess && resolved) {
          centerMapOnUserLocation({ animate: true });
        }
        return resolved;
      });
    }
    setLocationControlPending(true);
    state.locationRequestInFlight = new Promise(function (resolve) {
      nav.geolocation.getCurrentPosition(
        function (position) {
          state.currentPosition = {
            latitude: position.coords.latitude,
            longitude: position.coords.longitude,
          };
          applyInitialView(position);
          if (settings.centerOnSuccess) {
            centerMapOnUserLocation({ animate: true });
          }
          resolve(true);
        },
        function () {
          applyInitialView(null);
          if (settings.userInitiated) {
            setStatus("Neizdevās noteikt atrašanās vietu");
          }
          resolve(false);
        },
        { enableHighAccuracy: true, timeout: 8000, maximumAge: 60000 }
      );
    }).finally(function () {
      state.locationRequestInFlight = null;
      setLocationControlPending(false);
    });
    return state.locationRequestInFlight;
  }

  function applyInitialView(position) {
    if (position && position.coords) {
      state.currentPosition = {
        latitude: position.coords.latitude,
        longitude: position.coords.longitude,
      };
    }
    renderUserLocationMarker();
    selectInitialStop();
  }

  function selectInitialStop() {
    if (state.selectedStop || !state.catalog || !state.catalog.stops || !state.catalog.stops.length) {
      return;
    }
    var origin = state.currentPosition || { latitude: defaultCenter.lat, longitude: defaultCenter.lng };
    var nearest = nearestStops(origin, 1, null);
    if (nearest.length > 0) {
      selectStop(nearest[0].id);
    }
  }

  function canReportStop(mode, authenticated, stop) {
    if (!authenticated || !stop) {
      return false;
    }
    return !!String(stop.id || "").trim();
  }

  function findVehicle(vehicleId) {
    var targetID = String(vehicleId || "").trim();
    if (!targetID) {
      return null;
    }
    for (var i = 0; i < state.vehicles.length; i += 1) {
      if (String(state.vehicles[i].id || "").trim() === targetID) {
        return state.vehicles[i];
      }
    }
    return null;
  }

  function canReportLiveVehicle(mode, authenticated, vehicle) {
    if (!authenticated || !vehicle) {
      return false;
    }
    return !!buildLiveVehicleFallbackReportPayload(vehicle);
  }

  function buildLiveVehicleFallbackReportPayload(vehicle) {
    if (!vehicle) {
      return null;
    }
    var mode = String(vehicle.mode || "").trim();
    var routeLabel = String(vehicle.routeLabel || "").trim();
    var destination = String(vehicle.destination || "").trim();
    if (!mode || !routeLabel) {
      return null;
    }
    return {
      mode: mode,
      routeLabel: routeLabel,
      direction: String(vehicle.direction || "").trim(),
      destination: destination,
      departureSeconds: Number(vehicle.arrivalSeconds) || 0,
      liveRowId: String(vehicle.liveRowId || "").trim(),
    };
  }

  function stopReportsCount(stopId, sightings) {
    var targetStopId = String(stopId || "").trim();
    var normalizedTargetStopId = normalizeStopKey(targetStopId);
    var count = 0;
    if (!targetStopId || !sightings) {
      return 0;
    }
    (sightings.stopSightings || []).forEach(function (item) {
      var itemStopId = String(item && item.stopId || "").trim();
      if (itemStopId === targetStopId || normalizeStopKey(itemStopId) === normalizedTargetStopId) {
        count += 1;
      }
    });
    return count;
  }

  function unifiedStopReportCount(stopId, sightings, incidents) {
    var stopIncidents = stopIncidentsForStop(stopId, incidents);
    var incidentTotal = incidentVoteTotalForItems(stopIncidents);
    if (incidentTotal > 0 || stopIncidents.length > 0) {
      return incidentTotal;
    }
    return stopReportsCount(stopId, sightings);
  }

  function reportCountLabel(count) {
    var total = Number(count) || 0;
    if (total === 0) {
      return "Nav nesenu ziņojumu";
    }
    return String(total) + (total === 1 ? " ziņojums" : " ziņojumi");
  }

  function buildStopPopupHTML(stop, options) {
    var popupMode = String((options && options.mode) || config.mode || "public");
    var popupAuthenticated = !!(options && options.authenticated);
    var dismissible = !!(options && options.dismissible);
    var now = options && options.now ? options.now : new Date();
    var sightings = options && options.sightings ? options.sightings : state.sightings;
    var stopIncidents = options && options.stopIncidents ? options.stopIncidents : state.stopIncidents;
    var routes = Array.isArray(stop && stop.routeLabels) ? stop.routeLabels.filter(Boolean) : [];
    var reportCount = unifiedStopReportCount(stop && stop.id, sightings, stopIncidents);
    var incidents = stopIncidentsForStop(stop && stop.id, stopIncidents);
    var meta = [];
    var actionsHtml = "";

    if (!stop) {
      return '<div class="stop-popup"><p class="stop-popup-note">Pietura nav pieejama.</p></div>';
    }

    meta.push(reportCountLabel(reportCount));
    meta.push(latestReportAgeLabel(stop.id, sightings, stopIncidents, now));

    if (canReportStop(popupMode, popupAuthenticated, stop)) {
      actionsHtml =
        '<div class="stop-popup-actions">' +
        '<button class="action action-danger action-compact stop-popup-action" data-action="report-stop" data-stop-id="' + escapeAttr(stop.id) + '">Pieturas kontrole</button>' +
        "</div>";
    }
    actionsHtml += renderIncidentActionRows(incidents, {
      mode: popupMode,
      authenticated: popupAuthenticated,
    });

    return (
      '<div class="stop-popup">' +
      (dismissible
        ? '<div class="map-popup-dismiss-row"><button type="button" class="map-detail-close map-popup-dismiss" data-action="close-map-detail" aria-label="Aizvērt pieturas detaļas">Aizvērt</button></div>'
        : "") +
      '<div class="stop-popup-heading">' +
      '<strong>' + escapeHTML(displayStopName(stop)) + "</strong>" +
      (routes.length ? '<span class="stop-popup-subtitle">' + escapeHTML(routes.join(", ")) + "</span>" : "") +
      "</div>" +
      '<div class="stop-popup-meta">' +
      meta.map(function (item) {
        return '<span class="stop-popup-pill">' + escapeHTML(item) + "</span>";
      }).join("") +
      "</div>" +
      actionsHtml +
      "</div>"
    );
  }

  function stopPopupOffsetY(style) {
    var bodyHeight = Number(style && (style.bodyHeight || style.height || style.bodyWidth || style.width));
    var badgeSize = Number(style && style.badgeSize);
    var radius = Number(style && style.radius);
    var geometry = null;
    if (Number.isFinite(bodyHeight) && bodyHeight > 0 && Number.isFinite(badgeSize) && badgeSize > 0) {
      geometry = stopBadgeGeometry(bodyHeight, badgeSize, {
        stemHeight: Number(style && style.stopBadgeStemHeight),
        pinSize: Number(style && style.stopBadgePinSize),
      });
      return geometry.popupOffsetY;
    }
    if (!Number.isFinite(radius)) {
      radius = stopMarkerRadiusMin;
    }
    return -1 * (Math.max(radius, stopMarkerRadiusMin) + 12);
  }

  function vehiclePopupOffsetY(zoom, visibleHeightMeters) {
    var profile = vehicleMarkerProfile(zoom, visibleHeightMeters);
    var bodyHeight = roundPixelValue(
      profile && profile.iconSize ? profile.iconSize[1] : liveVehicleCompactMarkerDefaultSizePx,
      liveVehicleCompactMarkerDefaultSizePx
    );
    if (!profile || !profile.compact) {
      return -18;
    }
    return -1 * (Math.round(bodyHeight / 2) + 5);
  }

  function clampNumber(value, min, max) {
    if (!Number.isFinite(Number(value))) {
      return Number.isFinite(Number(min)) ? Number(min) : 0;
    }
    if (!Number.isFinite(Number(min)) || !Number.isFinite(Number(max))) {
      return Number(value);
    }
    if (Number(max) < Number(min)) {
      return Number(min);
    }
    return Math.min(Math.max(Number(value), Number(min)), Number(max));
  }

  function measureNodeSize(node) {
    var rect = null;
    var width = 0;
    var height = 0;
    if (!node) {
      return null;
    }
    if (typeof node.getBoundingClientRect === "function") {
      rect = node.getBoundingClientRect();
      width = Number(rect && rect.width);
      height = Number(rect && rect.height);
    }
    if (!Number.isFinite(width) || width <= 0) {
      width = Number(node.offsetWidth || node.clientWidth || 0);
    }
    if (!Number.isFinite(height) || height <= 0) {
      height = Number(node.offsetHeight || node.clientHeight || 0);
    }
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
      return null;
    }
    return {
      width: width,
      height: height,
    };
  }

  function mapDetailAnchorPoint(entity) {
    var latLng = mapEntityLatLng(entity);
    var point = null;
    if (
      !state.map ||
      !latLng ||
      latLng.length !== 2 ||
      typeof state.map.latLngToContainerPoint !== "function"
    ) {
      return null;
    }
    point = state.map.latLngToContainerPoint(latLng);
    if (!point || !Number.isFinite(Number(point.x)) || !Number.isFinite(Number(point.y))) {
      return null;
    }
    return {
      x: Number(point.x),
      y: Number(point.y),
    };
  }

  function mapDetailOffsetY(entity) {
    var normalized = normalizeMapEntity(entity);
    var marker = null;
    var entry = null;
    var metrics = null;
    if (!normalized) {
      return 0;
    }
    if (normalized.type === "stop") {
      marker = state.markers.get(normalized.id);
      metrics = markerIconMetrics(marker);
      if (metrics && Number.isFinite(Number(metrics.popupOffsetY))) {
        return Number(metrics.popupOffsetY);
      }
      return stopPopupOffsetY(marker && marker.options ? marker.options : null);
    }
    entry = state.vehicleMarkers instanceof Map ? state.vehicleMarkers.get(normalized.id) : null;
    metrics = entry && entry.iconMetrics ? entry.iconMetrics : markerIconMetrics(entry && entry.marker);
    if (metrics && Number.isFinite(Number(metrics.popupOffsetY))) {
      return Number(metrics.popupOffsetY);
    }
    return vehiclePopupOffsetY(currentMapZoom());
  }

  function resolveMapDetailOverlayLayout(anchorPoint, offsetY, cardSize, overlaySize, padding) {
    var safePadding = Math.max(0, Number(padding) || 0);
    var cardWidth = Number(cardSize && cardSize.width);
    var cardHeight = Number(cardSize && cardSize.height);
    var overlayWidth = Number(overlaySize && overlaySize.width);
    var overlayHeight = Number(overlaySize && overlaySize.height);
    var unclampedLeft = 0;
    var unclampedTop = 0;
    var maxLeft = 0;
    var maxTop = 0;
    var left = 0;
    var top = 0;
    if (
      !anchorPoint ||
      !Number.isFinite(Number(anchorPoint.x)) ||
      !Number.isFinite(Number(anchorPoint.y)) ||
      !Number.isFinite(cardWidth) ||
      !Number.isFinite(cardHeight) ||
      !Number.isFinite(overlayWidth) ||
      !Number.isFinite(overlayHeight) ||
      cardWidth <= 0 ||
      cardHeight <= 0 ||
      overlayWidth <= 0 ||
      overlayHeight <= 0
    ) {
      return null;
    }
    unclampedLeft = Number(anchorPoint.x) - (cardWidth / 2);
    unclampedTop = Number(anchorPoint.y) + Number(offsetY || 0) - cardHeight;
    maxLeft = overlayWidth - safePadding - cardWidth;
    maxTop = overlayHeight - safePadding - cardHeight;
    if (maxLeft < safePadding) {
      left = Math.max(0, (overlayWidth - cardWidth) / 2);
    } else {
      left = clampNumber(unclampedLeft, safePadding, maxLeft);
    }
    if (maxTop < safePadding) {
      top = Math.max(0, (overlayHeight - cardHeight) / 2);
    } else {
      top = clampNumber(unclampedTop, safePadding, maxTop);
    }
    return {
      left: left + (cardWidth / 2),
      top: top + cardHeight,
    };
  }

  function syncMapDetailOverlayPosition() {
    var overlay = document.getElementById("map-detail-overlay");
    var card = null;
    var entity = openMapDetailEntity();
    var anchorPoint = null;
    var cardSize = null;
    var overlaySize = null;
    var layout = null;
    if (!overlay || overlay.hidden || !entity) {
      return false;
    }
    if (!state.map || !isMapEntityRendered(entity)) {
      overlay.innerHTML = "";
      overlay.hidden = true;
      return false;
    }
    card = overlay.querySelector && typeof overlay.querySelector === "function"
      ? overlay.querySelector("[data-map-detail-card]")
      : null;
    if (!card) {
      return false;
    }
    anchorPoint = mapDetailAnchorPoint(entity);
    cardSize = measureNodeSize(card);
    overlaySize = measureNodeSize(overlay);
    layout = resolveMapDetailOverlayLayout(
      anchorPoint,
      mapDetailOffsetY(entity),
      cardSize,
      overlaySize,
      mapDetailOverlayClampPaddingPx
    );
    if (!layout) {
      return false;
    }
    card.style.left = String(Math.round(layout.left)) + "px";
    card.style.top = String(Math.round(layout.top)) + "px";
    card.style.transform = "translate(-50%, -100%)";
    return true;
  }

  function renderVisibleStops() {
    if (!state.map || !state.catalog || !state.catalog.stops) {
      return;
    }
    if (!state.stopIndex) {
      state.stopIndex = createStopIndex(state.catalog.stops);
    }
    var focusedStopRemoved = false;
    var openStopRemoved = false;
    var selectedStopRemoved = false;
    var bounds = state.map.getBounds();
    var visibleHeightMeters = mapVisibleHeightMeters(state.map);
    var counts = state.stopActivityCounts || syncStopActivityCounts();
    var nextVisibleStopIDs = new Set();
    var zoom = currentMapZoom();
    visibleStopCandidates(visibleHeightMeters).forEach(function (stop) {
      var count = counts[stop.id] || counts[normalizeStopKey(stop.id)] || 0;
      var highlighted = isFocusedStop(stop.id);
      var pinned = highlighted || isOpenStopDetail(stop.id) || isSelectedStop(stop.id);
      var inBounds = !bounds || bounds.contains([stop.latitude, stop.longitude]);
      if (inBounds && shouldRenderStopMarker(zoom, pinned, count, visibleHeightMeters)) {
        var marker = state.markers.get(stop.id);
        var spec = buildStopMarkerSpec(stop, {
          zoom: zoom,
          selected: highlighted,
          sightingCount: count,
          visibleHeightMeters: visibleHeightMeters,
        });
        var iconState = cachedMapMarkerIcon(spec, state.stopIconCache);
        if (!marker) {
          if (!root.L || typeof root.L.marker !== "function") {
            return;
          }
          marker = root.L.marker([stop.latitude, stop.longitude], {
            icon: iconState.icon,
          });
          marker.on("click", function () {
            handleMapEntityClick(stopMapEntity(stop.id));
          });
          setMarkerIconSpec(marker, spec);
          marker.addTo(state.map);
          state.markers.set(stop.id, marker);
        } else {
          if (typeof marker.setLatLng === "function" && !markerLatLngMatches(marker, [stop.latitude, stop.longitude])) {
            marker.setLatLng([stop.latitude, stop.longitude]);
          }
          if (typeof marker.setIcon === "function" && iconState.icon && marker.__satiksmeRenderKey !== iconState.key) {
            marker.setIcon(iconState.icon);
          }
          setMarkerIconSpec(marker, spec);
        }
        nextVisibleStopIDs.add(stop.id);
      }
    });
    state.markers.forEach(function (marker, stopId) {
      if (nextVisibleStopIDs.has(stopId)) {
        return;
      }
      state.map.removeLayer(marker);
      state.markers.delete(stopId);
      focusedStopRemoved = focusedStopRemoved || isFocusedStop(stopId);
      openStopRemoved = openStopRemoved || isOpenStopDetail(stopId);
      selectedStopRemoved = selectedStopRemoved || isSelectedStop(stopId);
    });
    if (focusedStopRemoved) {
      state.focusedMapEntity = null;
    }
    if (openStopRemoved) {
      state.openMapDetailEntity = null;
    }
    if (selectedStopRemoved) {
      state.selectedStop = null;
    }
    renderMapDetailOverlay();
  }

  function buildVehicleMarkerIcon(vehicle, zoom, visibleHeightMeters) {
    return buildMapMarkerIcon(buildVehicleMarkerSpec(vehicle, {
      zoom: zoom,
      visibleHeightMeters: visibleHeightMeters,
    }));
  }

  function focusMapCenterLatLng(targetLatLng, options) {
    return targetLatLng;
  }

  function centerMapOnLatLng(latLng, options) {
    var targetLatLng = Array.isArray(latLng) ? latLng.slice(0, 2) : null;
    var zoom = currentMapZoom();
    var focusLatLng = null;
    var animate = !!(options && options.animate);
    if (
      !state.map ||
      !targetLatLng ||
      targetLatLng.length !== 2 ||
      !Number.isFinite(targetLatLng[0]) ||
      !Number.isFinite(targetLatLng[1])
    ) {
      return false;
    }
    focusLatLng = focusMapCenterLatLng(targetLatLng, options) || targetLatLng;
    markProgrammaticMapView();
    if (typeof state.map.setView === "function") {
      state.map.setView(focusLatLng, zoom, { animate: animate });
      return true;
    }
    if (typeof state.map.panTo === "function") {
      state.map.panTo(focusLatLng, { animate: animate });
      return true;
    }
    return false;
  }

  function selectedVehicleMarkerLatLng() {
    var tracking = selectedVehicleTrackingState();
    var entry = null;
    if (!state.map || !tracking.markerKey || !tracking.vehicleId || !state.vehicleMarkers.has(tracking.vehicleId)) {
      return null;
    }
    entry = state.vehicleMarkers.get(tracking.vehicleId);
    if (!entry || !entry.marker) {
      return null;
    }
    if (Array.isArray(entry.positionLatLng) && entry.positionLatLng.length === 2) {
      return entry.positionLatLng.slice();
    }
    return vehicleMarkerLatLng(entry.marker);
  }

  function centerSelectedVehicleMarker() {
    var markerLatLng = selectedVehicleMarkerLatLng();
    if (!Array.isArray(markerLatLng) || markerLatLng.length !== 2) {
      return false;
    }
    return centerMapOnLatLng(markerLatLng, { animate: false, anchor: "center" });
  }

  function applySelectedVehicleFollow() {
    var tracking = selectedVehicleTrackingState();
    if (!tracking.markerKey || tracking.paused) {
      return false;
    }
    if (!centerSelectedVehicleMarker()) {
      clearSelectedVehicleTracking("vehicle-follow-target-missing");
      return false;
    }
    return true;
  }

  function cancelVehicleMarkerAnimation(entry) {
    if (!entry) {
      return;
    }
    if (entry.animationFrame && typeof root.cancelAnimationFrame === "function") {
      root.cancelAnimationFrame(entry.animationFrame);
    }
    entry.animationFrame = 0;
  }

  function animateVehicleMarkerTo(entry, vehicle) {
    if (!entry || !entry.marker || !vehicle) {
      return;
    }
    var vehicleId = String(vehicle.id || "").trim();
    var nextLatLng = [vehicle.latitude, vehicle.longitude];
    var startLatLng = vehicleMarkerLatLng(entry.marker) || (Array.isArray(entry.positionLatLng) ? entry.positionLatLng.slice() : null);
    var observedAt = vehicleMovementTimestampMs(vehicle);
    var nowMs = Date.now();
    var durationMs = vehicleMovementDurationMs(
      entry.positionObservedAt,
      observedAt,
      entry.lastMovementSyncAt ? nowMs - entry.lastMovementSyncAt : 0
    );
    entry.targetLatLng = nextLatLng.slice();
    entry.positionObservedAt = observedAt;
    entry.lastMovementSyncAt = nowMs;
    if (!startLatLng || vehicleLatLngEqual(startLatLng, nextLatLng) || typeof root.requestAnimationFrame !== "function") {
      cancelVehicleMarkerAnimation(entry);
      if (!vehicleLatLngEqual(startLatLng, nextLatLng)) {
        entry.marker.setLatLng(nextLatLng);
      }
      entry.positionLatLng = nextLatLng.slice();
      entry.targetLatLng = nextLatLng.slice();
      if (sameMapEntity(openMapDetailEntity(), vehicleMapEntity(vehicleId))) {
        syncMapDetailOverlayPosition();
      }
      if (state.selectedVehicleId === vehicleId && !state.vehicleFollowPaused) {
        centerMapOnLatLng(entry.positionLatLng, { anchor: "center" });
      }
      return;
    }
    cancelVehicleMarkerAnimation(entry);
    var startTime = root.performance && typeof root.performance.now === "function"
      ? root.performance.now()
      : nowMs;
    var tick = function (frameNow) {
      var elapsed = frameNow - startTime;
      var progress = Math.min(1, elapsed / durationMs);
      var eased = vehicleMotionEase(progress);
      var lat = startLatLng[0] + ((nextLatLng[0] - startLatLng[0]) * eased);
      var lng = startLatLng[1] + ((nextLatLng[1] - startLatLng[1]) * eased);
      entry.marker.setLatLng([lat, lng]);
      entry.positionLatLng = [lat, lng];
      if (sameMapEntity(openMapDetailEntity(), vehicleMapEntity(vehicleId))) {
        syncMapDetailOverlayPosition();
      }
      if (state.selectedVehicleId === vehicleId && !state.vehicleFollowPaused) {
        centerMapOnLatLng(entry.positionLatLng, { anchor: "center" });
      }
      if (progress >= 1) {
        entry.animationFrame = 0;
        entry.positionLatLng = nextLatLng.slice();
        entry.targetLatLng = nextLatLng.slice();
        return;
      }
      entry.animationFrame = root.requestAnimationFrame(tick);
    };
    entry.animationFrame = root.requestAnimationFrame(tick);
  }

  function removeVehicleMarkerEntry(vehicleId) {
    if (!state.vehicleMarkers.has(vehicleId)) {
      return;
    }
    var entry = state.vehicleMarkers.get(vehicleId);
    cancelVehicleMarkerAnimation(entry);
    if (state.map && entry && entry.marker) {
      state.map.removeLayer(entry.marker);
    }
    state.vehicleMarkers.delete(vehicleId);
    if (state.selectedVehicleId === String(vehicleId || "").trim()) {
      clearSelectedVehicleTracking("vehicle-removed");
    }
    if (isFocusedVehicle(vehicleId)) {
      state.focusedMapEntity = null;
    }
    if (isOpenVehicleDetail(vehicleId)) {
      state.openMapDetailEntity = null;
    }
    renderMapDetailOverlay();
  }

  function renderLiveVehicles() {
    if (!state.map) {
      return;
    }
    var zoom = currentMapZoom();
    var visibleHeightMeters = mapVisibleHeightMeters(state.map);
    var nextIds = new Set();
    (state.vehicles || []).forEach(function (vehicle) {
      if (
        !vehicle ||
        !vehicle.id ||
        !Number.isFinite(Number(vehicle.latitude)) ||
        !Number.isFinite(Number(vehicle.longitude))
      ) {
        return;
      }
      nextIds.add(vehicle.id);
      if (!state.vehicleMarkers.has(vehicle.id)) {
        var createSpec = buildVehicleMarkerSpec(vehicle, {
          zoom: zoom,
          visibleHeightMeters: visibleHeightMeters,
        });
        var createIconState = cachedMapMarkerIcon(createSpec, state.vehicleIconCache);
        var marker = root.L.marker([vehicle.latitude, vehicle.longitude], {
          icon: createIconState.icon,
        });
        marker.on("click", function () {
          handleMapEntityClick(vehicleMapEntity(vehicle.id));
        });
        setMarkerIconSpec(marker, createSpec);
        marker.addTo(state.map);
        state.vehicleMarkers.set(vehicle.id, {
          markerKey: liveVehicleMarkerKey(vehicle.id),
          marker: marker,
          vehicle: vehicle,
          iconMetrics: createSpec.metrics,
          animationFrame: 0,
          positionLatLng: [vehicle.latitude, vehicle.longitude],
          targetLatLng: [vehicle.latitude, vehicle.longitude],
          positionObservedAt: vehicleMovementTimestampMs(vehicle),
          lastMovementSyncAt: Date.now(),
        });
        return;
      }
      var entry = state.vehicleMarkers.get(vehicle.id);
      var spec = buildVehicleMarkerSpec(vehicle, {
        zoom: zoom,
        visibleHeightMeters: visibleHeightMeters,
      });
      var iconState = cachedMapMarkerIcon(spec, state.vehicleIconCache);
      entry.markerKey = liveVehicleMarkerKey(vehicle.id);
      entry.vehicle = vehicle;
      entry.iconMetrics = spec.metrics;
      if (typeof entry.marker.setIcon === "function" && entry.marker.__satiksmeRenderKey !== iconState.key) {
        entry.marker.setIcon(iconState.icon);
      }
      setMarkerIconSpec(entry.marker, spec);
      animateVehicleMarkerTo(entry, vehicle);
    });
    state.vehicleMarkers.forEach(function (_, vehicleId) {
      if (!nextIds.has(vehicleId)) {
        removeVehicleMarkerEntry(vehicleId);
      }
    });
    renderMapDetailOverlay();
  }

  function selectStop(stopId, options) {
    state.selectedStop = findStop(stopId);
    if (!(options && options.skipMarkerRender)) {
      renderVisibleStops();
    }
    renderSelectedStop();
  }

  function mapEntityLatLng(entity) {
    var normalized = normalizeMapEntity(entity);
    var stop = null;
    var vehicle = null;
    var entry = null;
    if (!normalized) {
      return null;
    }
    if (normalized.type === "stop") {
      stop = findStop(normalized.id);
      if (stop && Number.isFinite(Number(stop.latitude)) && Number.isFinite(Number(stop.longitude))) {
        return [Number(stop.latitude), Number(stop.longitude)];
      }
      return null;
    }
    vehicle = findVehicle(normalized.id);
    if (state.vehicleMarkers.has(normalized.id)) {
      entry = state.vehicleMarkers.get(normalized.id);
      if (entry && Array.isArray(entry.positionLatLng) && entry.positionLatLng.length === 2) {
        return entry.positionLatLng.slice();
      }
      if (entry && entry.marker) {
        return vehicleMarkerLatLng(entry.marker);
      }
      if (entry && Array.isArray(entry.targetLatLng) && entry.targetLatLng.length === 2) {
        return entry.targetLatLng.slice();
      }
    }
    if (vehicle && Number.isFinite(Number(vehicle.latitude)) && Number.isFinite(Number(vehicle.longitude))) {
      return [Number(vehicle.latitude), Number(vehicle.longitude)];
    }
    return null;
  }

  function isMapEntityRendered(entity) {
    var normalized = normalizeMapEntity(entity);
    if (!normalized) {
      return false;
    }
    if (normalized.type === "stop") {
      return state.markers.has(normalized.id);
    }
    return state.vehicleMarkers.has(normalized.id);
  }

  function clearFocusedMapEntity(reason) {
    var focused = focusedMapEntity();
    if (!focused) {
      return false;
    }
    if (focused.type === "vehicle") {
      return clearVehicleMapFocus(reason || "map-focus-cleared");
    }
    state.focusedMapEntity = null;
    renderVisibleStops();
    return true;
  }

  function closeMapDetail(reason) {
    if (!openMapDetailEntity()) {
      return false;
    }
    state.openMapDetailEntity = null;
    renderMapDetailOverlay();
    return true;
  }

  function focusMapEntity(entity, options) {
    var normalized = normalizeMapEntity(entity);
    var focused = focusedMapEntity();
    var latLng = null;
    var openDetail = !!(options && options.openDetail);
    if (!normalized) {
      return false;
    }
    if (focused && focused.type === "vehicle" && focused.id !== normalized.id) {
      clearSelectedVehicleTracking("map-focus-changed");
    }
    state.focusedMapEntity = normalized;
    if (!openDetail || !sameMapEntity(openMapDetailEntity(), normalized)) {
      state.openMapDetailEntity = openDetail ? normalized : null;
    }
    if (normalized.type === "stop") {
      selectStop(normalized.id, { skipMarkerRender: true });
    } else {
      setSelectedVehicleTracking(liveVehicleMarkerKey(normalized.id), normalized.id, "map-focus");
    }
    latLng = mapEntityLatLng(normalized);
    if (latLng) {
      centerMapOnLatLng(latLng, {
        animate: !!(options && options.animate),
        anchor: "center",
      });
    }
    renderVisibleStops();
    renderMapDetailOverlay();
    return true;
  }

  function handleMapEntityClick(entity) {
    var normalized = normalizeMapEntity(entity);
    if (!normalized) {
      return false;
    }
    suppressNextMapDetailDismiss();
    return focusMapEntity(normalized, {
      animate: false,
      openDetail: true,
    });
  }

  function buildSelectedStopHTML(stop, options) {
    var mode = String((options && options.mode) || config.mode || "public");
    var authenticated = !!(options && options.authenticated);
    var authState = String((options && options.authState) || state.authState || "unknown");
    var sightings = options && options.sightings ? options.sightings : state.sightings;
    var stopIncidents = options && options.stopIncidents ? options.stopIncidents : state.stopIncidents;
    var now = options && options.now ? options.now : new Date();
    var routes = ((stop && stop.routeLabels) || []).join(", ");
    var reportCount = unifiedStopReportCount(stop && stop.id, sightings, stopIncidents);
    var incidents = stopIncidentsForStop(stop && stop.id, stopIncidents);
    var loadingReports = publicMapNeedsLoadingUI();

    if (!stop) {
      return "<p>Izvēlies pieturu, lai redzētu pieturas informāciju un ziņojumus.</p>";
    }

    return (
      '<div class="stack">' +
      '<div class="stop-heading"><h3>' + escapeHTML(displayStopName(stop)) + '</h3><p>' + escapeHTML("Pietura " + stop.id) + '</p><p>' + escapeHTML(routes || "Nav maršrutu") + "</p></div>" +
      (loadingReports
        ? loadingStateHTML("Ielādē pieturas ziņojumus…", "loading-state-inline")
        : '<div class="meta"><span>' + escapeHTML(reportCountLabel(reportCount)) + "</span><span>" + escapeHTML(latestReportAgeLabel(stop.id, sightings, stopIncidents, now)) + "</span></div>") +
      renderReportNote(mode, authState) +
      renderStopSightingControl(mode, authenticated, stop, sightings, stopIncidents, now) +
      renderIncidentActionRows(incidents, {
        mode: mode,
        authenticated: authenticated,
      }) +
      "</div>"
    );
  }

  function renderSelectedStop() {
    var rootNode = document.getElementById("selected-stop");
    if (!rootNode) {
      return;
    }
    rootNode.innerHTML = buildSelectedStopHTML(state.selectedStop, {
      mode: String(config.mode || "public"),
      authenticated: state.authenticated,
      authState: state.authState,
      sightings: state.sightings,
      stopIncidents: state.stopIncidents,
      now: new Date(),
    });
  }

  function buildVehicleMarkerHTML(vehicle, profile) {
    return buildMapIconHTML(buildVehicleMarkerSpec(vehicle, {
      profile: profile || vehicleMarkerProfile(15),
      zoom: profile && profile.tier === "compact" ? 14 : 15,
    }));
  }

  function buildVehiclePopupHTML(vehicle, options) {
    var routeLabel = String(vehicle.routeLabel || "").trim();
    var vehicleCode = String(vehicle.vehicleCode || "").trim();
    var popupMode = String((options && options.mode) || config.mode || "public");
    var popupAuthenticated = !!(options && options.authenticated);
    var dismissible = !!(options && options.dismissible);
    var incidents = Array.isArray(vehicle && vehicle.incidents) ? vehicle.incidents : [];
    var identityHtml = "";
    var actionsHtml = "";

    if (routeLabel) {
      identityHtml += '<span class="vehicle-popup-route">' + escapeHTML(routeLabel) + "</span>";
    }
    if (vehicleCode) {
      identityHtml += '<span class="vehicle-popup-id">' + escapeHTML(vehicleCode) + "</span>";
    }
    if (!identityHtml) {
      identityHtml = '<span class="vehicle-popup-empty">Transports tiešraidē</span>';
    }

    if (canReportLiveVehicle(popupMode, popupAuthenticated, vehicle)) {
      actionsHtml =
        '<div class="vehicle-popup-actions">' +
        '<button class="action action-secondary action-compact vehicle-popup-action" data-action="report-live-vehicle" data-vehicle-id="' +
        escapeAttr(vehicle.id) +
        '">Kontrole</button>' +
        "</div>";
    }
    actionsHtml += renderIncidentActionRows(incidents, {
      mode: popupMode,
      authenticated: popupAuthenticated,
    });

    return (
      '<div class="vehicle-popup vehicle-popup-mode-' + escapeToken(vehicle.mode) + '">' +
      (dismissible
        ? '<div class="map-popup-dismiss-row"><button type="button" class="map-detail-close map-popup-dismiss" data-action="close-map-detail" aria-label="Aizvērt transporta detaļas">Aizvērt</button></div>'
        : "") +
      '<div class="vehicle-popup-identity">' + identityHtml + "</div>" +
      actionsHtml +
      "</div>"
    );
  }

  function buildMapDetailBodyHTML(entity) {
    var normalized = normalizeMapEntity(entity);
    var stop = null;
    var vehicle = null;
    if (!normalized) {
      return "";
    }
    if (normalized.type === "stop") {
      stop = findStop(normalized.id);
      if (!stop) {
        return "";
      }
      return buildStopPopupHTML(stop, {
        mode: String(config.mode || "public"),
        authenticated: state.authenticated,
        dismissible: true,
        sightings: state.sightings,
        stopIncidents: state.stopIncidents,
        now: new Date(),
      });
    }
    vehicle = findVehicle(normalized.id);
    if (!vehicle) {
      return "";
    }
    return buildVehiclePopupHTML(vehicle, {
      mode: String(config.mode || "public"),
      authenticated: state.authenticated,
      dismissible: true,
    });
  }

  function buildMapDetailOverlayHTML(entity) {
    var normalized = normalizeMapEntity(entity);
    var body = buildMapDetailBodyHTML(entity);
    if (!body || !normalized) {
      return "";
    }
    return (
      '<div class="map-detail-card map-popup map-popup-' + escapeToken(normalized.type) + '" data-map-detail-card="true" data-map-detail-entity="' +
      escapeAttr(normalized.type + ":" + normalized.id) +
      '">' +
      '<div class="map-detail-content">' + body + "</div>" +
      "</div>"
    );
  }

  function renderMapDetailOverlay() {
    var overlay = document.getElementById("map-detail-overlay");
    var entity = openMapDetailEntity();
    if (entity && state.map && !isMapEntityRendered(entity)) {
      state.openMapDetailEntity = null;
      entity = null;
    }
    if (!overlay) {
      return;
    }
    if (!entity) {
      overlay.innerHTML = "";
      overlay.hidden = true;
      return;
    }
    overlay.hidden = false;
    overlay.innerHTML = buildMapDetailOverlayHTML(entity);
    if (!overlay.innerHTML) {
      overlay.hidden = true;
      return;
    }
    syncMapDetailOverlayPosition();
  }

  function renderSightings() {
    var node = document.getElementById("recent-sightings");
    if (!node) {
      return;
    }
    if (publicMapNeedsLoadingUI()) {
      node.innerHTML = loadingStateHTML("Ielādē ziņojumus…");
      return;
    }
    var stopItems = (state.sightings.stopSightings || []).slice(0, 6).map(function (item) {
      return '<li>Pieturas kontrole · ' + escapeHTML(item.stopName || item.stopId) + ' · ' + escapeHTML(formatEventTime(item.createdAt)) + "</li>";
    });
    var vehicleItems = (state.sightings.vehicleSightings || []).slice(0, 6).map(function (item) {
      var parts = [modeAndRouteLabel(item.mode, item.routeLabel)];
      if (item.destination) {
        parts.push(item.destination);
      }
      parts.push(formatEventTime(item.createdAt));
      return '<li>' + escapeHTML(parts.join(" · ")) + "</li>";
    });
    node.innerHTML = "<ul>" + (stopItems.concat(vehicleItems).join("") || "<li>Nav nesenu ziņojumu.</li>") + "</ul>";
  }

  function incidentCommentDraft(incidentId) {
    return String(state.publicIncidentCommentDrafts[incidentId] || "");
  }

  function setIncidentCommentDraft(incidentId, value) {
    if (!incidentId) {
      return;
    }
    state.publicIncidentCommentDrafts[incidentId] = String(value || "");
  }

  function clearIncidentCommentDraft(incidentId) {
    if (!incidentId) {
      return;
    }
    delete state.publicIncidentCommentDrafts[incidentId];
  }

  function updateIncidentVotesInState(incidentId, votes) {
    var resolved = votes && Number(votes.cleared) >= 2;
    if (!incidentId) {
      return;
    }
    currentPublicIncidentVoteSelections()[incidentId] = votes && votes.userValue ? String(votes.userValue) : "";
    if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId) {
      state.publicIncidentDetail.summary.votes = votes;
      state.publicIncidentDetail.summary.resolved = resolved;
    }
    state.publicIncidents = (state.publicIncidents || []).map(function (item) {
      if (!item || item.id !== incidentId) {
        return item;
      }
      return Object.assign({}, item, { votes: votes, resolved: resolved, active: !resolved });
    });
  }

  function appendIncidentCommentToState(incidentId, comment) {
    if (!incidentId || !comment) {
      return;
    }
    state.publicIncidents = (state.publicIncidents || []).map(function (item) {
      if (!item || item.id !== incidentId) {
        return item;
      }
      return Object.assign({}, item, {
        commentCount: Number(item.commentCount || 0) + 1,
      });
    });
    if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId) {
      state.publicIncidentDetail.summary.commentCount = Number(state.publicIncidentDetail.summary.commentCount || 0) + 1;
      state.publicIncidentDetail.comments = (state.publicIncidentDetail.comments || []).concat([comment]);
    }
  }

  function bindActions() {
    var win = windowHandle();
    if (!document || !document.addEventListener) {
      return;
    }
    if (actionsBound) {
      return;
    }
    actionsBound = true;
    document.addEventListener("click", function (event) {
      guardMapDetailOutsideClick(event);
    }, true);
    document.addEventListener("click", function (event) {
      var target = event.target;
      var button = null;
      var action = "";
      if (!target) {
        return;
      }
      button = target.closest && typeof target.closest === "function"
        ? target.closest("[data-action]")
        : target;
      if (!button || !button.getAttribute) {
        return;
      }
      action = button.getAttribute("data-action");
      if (action === "close-map-detail") {
        closeMapDetail("close-button");
        return;
      }
      if (action === "telegram-login") {
        beginTelegramLogin();
        return;
      }
      if (action === "logout") {
        logout();
        return;
      }
      if (action === "report-stop") {
        var stopId = String(button.getAttribute("data-stop-id") || (state.selectedStop && state.selectedStop.id) || "").trim();
        if (!stopId) {
          return;
        }
        submitStopReport(stopId);
        return;
      }
      if (action === "report-live-vehicle") {
        var vehicleId = String(button.getAttribute("data-vehicle-id") || "").trim();
        if (!vehicleId) {
          return;
        }
        submitLiveVehicleReport(vehicleId);
        return;
      }
      if (action === "open-incident") {
        openIncidentDetailView(button.getAttribute("data-incident-id")).catch(function () {
          return null;
        });
        return;
      }
      if (action === "open-incident-page") {
        navigateToIncidentPage(button.getAttribute("data-incident-id"));
        return;
      }
      if (action === "close-incident-detail") {
        closeIncidentDetailOverlay({ force: true });
        return;
      }
      if (action === "incident-vote") {
        submitIncidentVote(button.getAttribute("data-incident-id"), button.getAttribute("data-value"));
        return;
      }
      if (action === "submit-incident-comment") {
        submitIncidentComment(button.getAttribute("data-incident-id"));
      }
    });
    document.addEventListener("input", function (event) {
      var target = event && event.target;
      if (!target || target.id !== "incident-comment-body" || !target.getAttribute) {
        return;
      }
      setIncidentCommentDraft(target.getAttribute("data-incident-id"), target.value);
    });
    if (win && typeof win.addEventListener === "function") {
      win.addEventListener("popstate", handleIncidentPopState);
      win.addEventListener("resize", handleIncidentViewportChange);
    }
  }

  function incidentVoteSummaryLabel(votes) {
    var ongoing = votes && typeof votes.ongoing === "number" ? votes.ongoing : 0;
    var cleared = votes && typeof votes.cleared === "number" ? votes.cleared : 0;
    return "Kontrole: " + ongoing + " · Nav kontrole: " + cleared;
  }

  function renderIncidentQuickVoteButtons(item) {
    var voteValue = item && item.votes && item.votes.userValue ? item.votes.userValue : "";
    if (!state.publicIncidentMobileLayout || !state.authenticated) {
      return "";
    }
    return (
      '<div class="button-row incident-summary-actions">' +
      '<button class="' + (voteValue === "ONGOING" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(item.id) + '" data-value="ONGOING">' + escapeHTML(incidentVoteLabel("ONGOING")) + '</button>' +
      '<button class="' + (voteValue === "CLEARED" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(item.id) + '" data-value="CLEARED">' + escapeHTML(incidentVoteLabel("CLEARED")) + "</button>" +
      "</div>"
    );
  }

  function renderIncidentSummaryCard(item) {
    var active = state.publicIncidentSelectedId === item.id ? " selected-train-card" : "";
    var lastReportName = item.lastReportName || "Ziņojums";
    return (
      '<article class="detail-card incident-card' + active + '">' +
      '<button class="incident-summary-button" data-action="open-incident" data-incident-id="' + escapeAttr(item.id) + '">' +
      '<div class="station-card-header"><h3>' + escapeHTML(item.subjectName || "Incidents") + '</h3><div class="incident-summary-pills"><span class="station-selected-pill">' + escapeHTML(formatRelativeReportAge(item.lastReportAt, new Date())) + "</span>" + renderIncidentStatusPill(item) + "</div></div>" +
      '<div class="meta"><span>' + escapeHTML(lastReportName) + "</span><span>" + escapeHTML("Pēdējais: " + (item.lastReporter || "anonīmi")) + "</span></div>" +
      '<div class="meta"><span>' + escapeHTML(incidentVoteSummaryLabel(item.votes)) + "</span><span>" + escapeHTML(String(item.commentCount || 0) + " komentāri") + "</span></div>" +
      "</button>" +
      renderIncidentQuickVoteButtons(item) +
      "</article>"
    );
  }

  function renderIncidentEventCard(item) {
    return '<article class="favorite-card"><h3>' + escapeHTML(item.name || "") + '</h3><div class="meta"><span>' + escapeHTML(item.nickname || "") + "</span><span>" + escapeHTML(formatEventTime(item.createdAt)) + "</span></div></article>";
  }

  function renderIncidentCommentCard(item) {
    return '<article class="favorite-card"><h3>' + escapeHTML(item.nickname || "") + '</h3><div class="meta"><span>' + escapeHTML(formatEventTime(item.createdAt)) + "</span></div><p>" + escapeHTML(item.body || "") + "</p></article>";
  }

  function renderIncidentDetailHTML(detail) {
    var voteValue = "";
    var draft = "";
    var eventsHtml = "";
    var commentsHtml = "";
    var mobileClose = state.publicIncidentMobileLayout
      ? '<div class="incident-detail-mobile-nav"><button class="action action-secondary action-compact" data-action="close-incident-detail">Atpakaļ</button></div>'
      : "";
    if (publicIncidentDetailNeedsLoadingUI(detail)) {
      return mobileClose + loadingStateHTML("Ielādē incidenta detaļas…");
    }
    if (!detail || !detail.summary) {
      return mobileClose + '<div class="empty">Izvēlies incidentu, lai redzētu detaļas.</div>';
    }
    voteValue = detail.summary.votes && detail.summary.votes.userValue ? detail.summary.votes.userValue : "";
    draft = incidentCommentDraft(detail.summary.id);
    eventsHtml = (detail.events || []).length
      ? detail.events.map(renderIncidentEventCard).join("")
      : '<div class="empty">Vēl nav incidenta aktivitātes.</div>';
    commentsHtml = (detail.comments || []).length
      ? detail.comments.map(renderIncidentCommentCard).join("")
      : '<div class="empty">Komentāru vēl nav.</div>';

    return (
      '<div class="stack">' +
      mobileClose +
      '<div class="incident-detail-badges"><div class="badge">' + escapeHTML(detail.summary.subjectName || "") + "</div>" + renderIncidentStatusPill(detail.summary) + "</div>" +
      '<section class="detail-card">' +
      '<h3>' + escapeHTML(detail.summary.lastReportName || "") + '</h3>' +
      '<div class="meta"><span>' + escapeHTML("Pēdējais: " + (detail.summary.lastReporter || "anonīmi")) + "</span><span>" + escapeHTML(formatRelativeReportAge(detail.summary.lastReportAt, new Date())) + "</span></div>" +
      (state.authenticated
        ? '<div class="button-row">' +
          '<button class="' + (voteValue === "ONGOING" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(detail.summary.id) + '" data-value="ONGOING">' + escapeHTML(incidentVoteLabel("ONGOING")) + '</button>' +
          '<button class="' + (voteValue === "CLEARED" ? "action action-primary action-compact" : "action action-secondary action-compact") + '" data-action="incident-vote" data-incident-id="' + escapeAttr(detail.summary.id) + '" data-value="CLEARED">' + escapeHTML(incidentVoteLabel("CLEARED")) + "</button>" +
          "</div>"
        : "") +
      '<p class="report-note">' + escapeHTML(incidentVoteSummaryLabel(detail.summary.votes)) + "</p>" +
      (state.authenticated
        ? '<div class="field"><label for="incident-comment-body">Anonīms komentārs</label><textarea id="incident-comment-body" data-incident-id="' + escapeAttr(detail.summary.id) + '" rows="3" placeholder="Pievieno īsu komentāru">' + escapeHTML(draft) + '</textarea></div><div class="button-row"><button class="action action-primary action-compact" data-action="submit-incident-comment" data-incident-id="' + escapeAttr(detail.summary.id) + '">Publicēt komentāru</button></div>'
        : '<p class="report-note">Pieslēdzies ar Telegram, lai balsotu un komentētu anonīmi.</p>') +
      "</section>" +
      '<section class="detail-card"><h3>Aktivitāte</h3><div class="card-list">' + eventsHtml + "</div></section>" +
      '<section class="detail-card"><h3>Komentāri</h3><div class="card-list">' + commentsHtml + "</div></section>" +
      "</div>"
    );
  }

  function renderIncidentList() {
    var node = document.getElementById("incident-list");
    if (!node) {
      return;
    }
    if (publicIncidentListNeedsLoadingUI()) {
      node.innerHTML = loadingStateHTML("Ielādē notiekošo…");
      return;
    }
    if (!state.publicIncidents.length) {
      node.innerHTML = '<div class="empty">Pēdējo 24 stundu incidentu nav.</div>';
      return;
    }
    node.innerHTML = state.publicIncidents.map(renderIncidentSummaryCard).join("");
  }

  function renderIncidentDetail() {
    var node = document.getElementById("incident-detail");
    var panel = document.getElementById("incident-detail-panel");
    var detailVisible = isIncidentDetailVisible();
    updateIncidentOverlayBodyState();
    if (panel) {
      if (panel.classList && typeof panel.classList.toggle === "function") {
        panel.classList.toggle("incident-detail-panel-open", state.publicIncidentMobileLayout && state.publicIncidentDetailOpen);
      }
      if ("hidden" in panel) {
        panel.hidden = state.publicIncidentMobileLayout && !state.publicIncidentDetailOpen;
      }
      if (panel.setAttribute) {
        panel.setAttribute("aria-hidden", detailVisible ? "false" : "true");
      }
    }
    if (!node) {
      return;
    }
    node.innerHTML = renderIncidentDetailHTML(state.publicIncidentDetail);
  }

  function renderIncidentFeed() {
    syncIncidentLayoutState();
    renderIncidentList();
    renderIncidentDetail();
  }

  function submitStopReport(stopId) {
    var bundleIdentity = activeBundleIdentity();
    var request = spacetimeEnabled()
      ? callSpacetimeProcedure("satiksmebot_submit_stop_report", [
          stopId,
          bundleIdentity.version,
          bundleIdentity.generatedAt,
        ], {})
      : fetchJSON(pathFor("/api/v1/reports/stop"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ stopId: stopId }),
          credentials: "same-origin",
        });
    return request
      .then(function (result) {
        if (result.accepted) {
          prependStopSighting(stopId);
        }
        setStatus(reportMessage(result, "Kontroles ziņojums saglabāts"));
        return loadLiveMapState();
      })
      .catch(function () {
        setStatus("Neizdevās iesniegt kontroles ziņojumu");
      });
  }

  function submitLiveVehicleReport(vehicleId) {
    var vehicle = findVehicle(vehicleId);
    var payload = buildLiveVehicleFallbackReportPayload(vehicle);
    if (!vehicle) {
      setStatus("Transports vairs nav pieejams");
      return Promise.resolve(null);
    }
    if (!payload) {
      setStatus("Transportam nav pietiekamu datu, ziņojumu nevar iesniegt");
      return Promise.resolve(null);
    }
    return submitVehicleReport(payload);
  }

  function submitVehicleReport(payload) {
    var bundleIdentity = activeBundleIdentity();
    var request = spacetimeEnabled()
      ? callSpacetimeProcedure("satiksmebot_submit_vehicle_report", [
          String(payload.stopId || ""),
          String(payload.mode || ""),
          String(payload.routeLabel || ""),
          String(payload.direction || ""),
          String(payload.destination || ""),
          Number(payload.departureSeconds || 0),
          String(payload.liveRowId || ""),
          bundleIdentity.version,
          bundleIdentity.generatedAt,
        ], {})
      : fetchJSON(pathFor("/api/v1/reports/vehicle"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
          credentials: "same-origin",
        });
    return request
      .then(function (result) {
        if (result.accepted) {
          prependVehicleSighting(payload);
        }
        setStatus(reportMessage(result, "Transporta kontroles ziņojums saglabāts"));
        return loadLiveMapState();
      })
      .catch(function () {
        setStatus("Neizdevās iesniegt transporta kontroles ziņojumu");
      });
  }

  function submitIncidentVote(incidentId, value) {
    var request = spacetimeEnabled()
      ? callSpacetimeProcedure("satiksmebot_vote_incident", [incidentId, value], {})
      : fetchJSON(pathFor("/api/v1/incidents/" + encodeURIComponent(incidentId) + "/votes"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ value: value }),
          credentials: "same-origin",
        });
    return request
      .then(function (votes) {
        updateIncidentVotesInState(incidentId, votes);
        return votes;
      })
      .then(function () {
        setStatus("Balsojums saglabāts");
        if (String(config.mode || "public") === "public-incidents") {
          renderIncidentFeed();
          return null;
        }
        return loadLiveMapState();
      })
      .catch(function (error) {
        setStatus((error && error.message) || "Neizdevās saglabāt balsojumu");
      });
  }

  function submitIncidentComment(incidentId) {
    var input = document.getElementById("incident-comment-body");
    var body = input ? String(input.value || "") : "";
    var request = spacetimeEnabled()
      ? callSpacetimeProcedure("satiksmebot_comment_incident", [incidentId, body], {})
      : fetchJSON(pathFor("/api/v1/incidents/" + encodeURIComponent(incidentId) + "/comments"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ body: body }),
          credentials: "same-origin",
        });
    return request
      .then(function (comment) {
        clearIncidentCommentDraft(incidentId);
        appendIncidentCommentToState(incidentId, comment);
        return comment;
      })
      .then(function () {
        setStatus("Komentārs publicēts");
        renderIncidentFeed();
      })
      .catch(function (error) {
        setStatus((error && error.message) || "Neizdevās publicēt komentāru");
      });
  }

  function reportMessage(result, acceptedMessage) {
    if (result.accepted) {
      return acceptedMessage;
    }
    if (result.deduped) {
      return "Dublikāts ignorēts";
    }
    if (result.rateLimited) {
      if (result.reason === "map_report_limit") {
        return "Pārāk daudz ziņojumu. Jānogaida: " + (result.cooldownSeconds || 1) + " s";
      }
      return "Jānogaida: " + (result.cooldownSeconds || 1) + " s";
    }
    if (result.cooldownSeconds) {
      return "Jānogaida: " + result.cooldownSeconds + " s";
    }
    return "Ziņojums ignorēts";
  }

  function renderReportNote(mode, authState) {
    if (authState === "authenticated") {
      return '<p class="report-note report-note-ready">Telegram sesija aktīva. Šajā skatā vari ziņot par kontroli.</p>';
    }
    return '<p class="report-note">Pieslēdzies ar Telegram, lai ziņotu par kontroli.</p>';
  }

  function prependStopSighting(stopId) {
    var stop = findStop(stopId);
    state.sightings.stopSightings = [{
      id: "local-stop-" + Date.now(),
      stopId: stopId,
      stopName: stop ? displayStopName(stop) : stopId,
      createdAt: new Date().toISOString(),
    }].concat(state.sightings.stopSightings || []).slice(0, sightingsFetchLimit);
    renderVisibleStops();
    renderSightings();
  }

  function prependVehicleSighting(payload) {
    var stopId = String(payload && payload.stopId || "").trim();
    var stop = findStop(stopId);
    state.sightings.vehicleSightings = [{
      id: "local-vehicle-" + Date.now(),
      stopId: stopId,
      stopName: stop && stopId ? displayStopName(stop) : "",
      mode: payload.mode,
      routeLabel: payload.routeLabel,
      direction: payload.direction,
      destination: payload.destination,
      departureSeconds: payload.departureSeconds,
      liveRowId: payload.liveRowId || "",
      createdAt: new Date().toISOString(),
    }].concat(state.sightings.vehicleSightings || []).slice(0, sightingsFetchLimit);
    renderSightings();
  }

  function sameMaterialValue(left, right) {
    return JSON.stringify(left) === JSON.stringify(right);
  }

  function formatEventTime(value) {
    var at = new Date(value);
    if (!Number.isFinite(at.getTime())) {
      return "--:--";
    }
    return pad(at.getHours()) + ":" + pad(at.getMinutes());
  }

  function setStatus(text) {
    var pill = document.getElementById("status-pill");
    if (pill) {
      pill.textContent = text;
    }
    syncLoadingIndicators();
  }

  function escapeHTML(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function escapeAttr(value) {
    return escapeHTML(value).replace(/'/g, "&#39;");
  }

  function escapeToken(value) {
    return String(value || "").replace(/[^a-z0-9_-]/gi, "-");
  }

  if (root.document && root.document.addEventListener) {
    root.document.addEventListener("DOMContentLoaded", boot);
  }

  return {
    __test__: {
      defaultCenter: defaultCenter,
      normalizeStopKey: normalizeStopKey,
      vehicleMovementTimestampMs: vehicleMovementTimestampMs,
      vehicleMovementDurationMs: vehicleMovementDurationMs,
      boundsHeightMeters: boundsHeightMeters,
      mapVisibleHeightMeters: mapVisibleHeightMeters,
      stopMarkerRadiusForHeight: stopMarkerRadiusForHeight,
      liveVehicleCompactMarkerSize: liveVehicleCompactMarkerSize,
      mapZoomTier: mapZoomTier,
      shouldRenderStopMarker: shouldRenderStopMarker,
      shouldShowStopBadge: shouldShowStopBadge,
      stopMarkerStyle: stopMarkerStyle,
      stopBadgeOffsetForRadius: stopBadgeOffsetForRadius,
      stopBadgeScaleForHeight: stopBadgeScaleForHeight,
      stopBadgeSizeForHeight: stopBadgeSizeForHeight,
      stopBadgeGeometry: stopBadgeGeometry,
      buildStopMarkerSpec: buildStopMarkerSpec,
      vehicleMarkerProfile: vehicleMarkerProfile,
      buildVehicleMarkerSpec: buildVehicleMarkerSpec,
      buildMapIconHTML: buildMapIconHTML,
      markerIconMetrics: markerIconMetrics,
      resolveInitialView: resolveInitialView,
      userLocationLatLng: userLocationLatLng,
      renderUserLocationMarker: renderUserLocationMarker,
      centerMapOnUserLocation: centerMapOnUserLocation,
      focusUserLocation: focusUserLocation,
      addUserLocationControl: addUserLocationControl,
      requestLocation: requestLocation,
      applyInitialView: applyInitialView,
      buildVehicleMarkerHTML: buildVehicleMarkerHTML,
      buildStopPopupHTML: buildStopPopupHTML,
      buildSelectedStopHTML: buildSelectedStopHTML,
      buildVehiclePopupHTML: buildVehiclePopupHTML,
      stopPopupOffsetY: stopPopupOffsetY,
      vehiclePopupOffsetY: vehiclePopupOffsetY,
      mapDetailAnchorPoint: mapDetailAnchorPoint,
      resolveMapDetailOverlayLayout: resolveMapDetailOverlayLayout,
      syncMapDetailOverlayPosition: syncMapDetailOverlayPosition,
      canReportStop: canReportStop,
      canReportLiveVehicle: canReportLiveVehicle,
      buildLiveVehicleFallbackReportPayload: buildLiveVehicleFallbackReportPayload,
      stopActivityCount: stopActivityCount,
      syncStopActivityCounts: syncStopActivityCounts,
      stopReportsCount: stopReportsCount,
      unifiedStopReportCount: unifiedStopReportCount,
      incidentVoteTotal: incidentVoteTotal,
      vehicleMarkerCount: vehicleMarkerCount,
      reportCountLabel: reportCountLabel,
      renderSightings: renderSightings,
      renderStopSightingControl: renderStopSightingControl,
      renderReportNote: renderReportNote,
      renderHeroMeta: renderHeroMeta,
      renderIncidentActionRows: renderIncidentActionRows,
      renderIncidentSummaryCard: renderIncidentSummaryCard,
      loadingStateHTML: loadingStateHTML,
      renderIncidentList: renderIncidentList,
	      renderIncidentDetail: renderIncidentDetail,
	      spacetimeDirectOnlyEnabled: spacetimeDirectOnlyEnabled,
	      liveTransportSnapshotLookupEnabled: liveTransportSnapshotLookupEnabled,
	      liveTransportRealtimeEnabled: liveTransportRealtimeEnabled,
      mergeLiveVehiclesWithSharedState: mergeLiveVehiclesWithSharedState,
      applySharedMapCollections: applySharedMapCollections,
      applyLiveTransportSnapshotPayload: applyLiveTransportSnapshotPayload,
      normalizeLiveTransportSnapshotState: normalizeLiveTransportSnapshotState,
      bestVehicleMatch: bestVehicleMatch,
      renderIncidentDetailHTML: function (detail, authenticated) {
        state.authenticated = authenticated !== false;
        state.publicIncidentDetail = detail;
        syncIncidentLayoutState();
        return renderIncidentDetailHTML(detail);
      },
      renderAuthControlsHTML: renderAuthControlsHTML,
      resetState: function (overrides) {
        resetStateForTest(overrides);
      },
      liveVehicleMarkerKey: liveVehicleMarkerKey,
      normalizeMapEntity: normalizeMapEntity,
      stopMapEntity: stopMapEntity,
      vehicleMapEntity: vehicleMapEntity,
      sameMapEntity: sameMapEntity,
      suppressNextMapDetailDismiss: suppressNextMapDetailDismiss,
      isMapDetailDismissSuppressed: isMapDetailDismissSuppressed,
      isInsideMapDetailTarget: isInsideMapDetailTarget,
      guardMapDetailOutsideClick: guardMapDetailOutsideClick,
      handlePotentialMapDetailDismiss: handlePotentialMapDetailDismiss,
      centerMapOnLatLng: centerMapOnLatLng,
      focusMapCenterLatLng: focusMapCenterLatLng,
      beginUserMapGesture: beginUserMapGesture,
      finishUserMapGesture: finishUserMapGesture,
      mapPanDistancePx: mapPanDistancePx,
      setSelectedVehicleTracking: setSelectedVehicleTracking,
      clearSelectedVehicleTracking: clearSelectedVehicleTracking,
      pauseSelectedVehicleTracking: pauseSelectedVehicleTracking,
      applySelectedVehicleFollow: applySelectedVehicleFollow,
      animateVehicleMarkerTo: animateVehicleMarkerTo,
      renderLiveVehicles: renderLiveVehicles,
      clearVehicleMapFocus: clearVehicleMapFocus,
      renderVisibleStops: renderVisibleStops,
      focusMapEntity: focusMapEntity,
      handleMapEntityClick: handleMapEntityClick,
      closeMapDetail: closeMapDetail,
      buildMapDetailOverlayHTML: buildMapDetailOverlayHTML,
      renderMapDetailOverlay: renderMapDetailOverlay,
      mapDetailPresentation: mapDetailPresentation,
      mapViewportHostElement: mapViewportHostElement,
      currentMapViewportSize: currentMapViewportSize,
      hasMapViewportSizeChanged: hasMapViewportSizeChanged,
      syncLeafletViewport: syncLeafletViewport,
      scheduleLeafletViewportSync: scheduleLeafletViewportSync,
      handleMapViewportResize: handleMapViewportResize,
      observeMapViewportResize: observeMapViewportResize,
      selectStop: selectStop,
      setIncidentCommentDraft: setIncidentCommentDraft,
      clearIncidentCommentDraft: clearIncidentCommentDraft,
      updateIncidentVotesInState: updateIncidentVotesInState,
      sameMaterialValue: sameMaterialValue,
	      loadCatalog: loadCatalog,
	      loadSharedMapStateDirect: loadSharedMapStateDirect,
	      loadBackendSharedMapState: loadBackendSharedMapState,
	      loadSnapshotBackedMapState: loadSnapshotBackedMapState,
	      loadIncidents: loadIncidents,
	      loadIncidentDetail: loadIncidentDetail,
	      loadLiveVehicles: loadLiveVehicles,
	      loadLiveMapState: loadLiveMapState,
      submitIncidentVote: submitIncidentVote,
      openIncidentDetailView: openIncidentDetailView,
      closeIncidentDetailOverlay: closeIncidentDetailOverlay,
      navigateToIncidentPage: navigateToIncidentPage,
      incidentPageURL: incidentPageURL,
      handleIncidentPopState: handleIncidentPopState,
      syncIncidentLayoutState: syncIncidentLayoutState,
      isIncidentMobileLayout: isIncidentMobileLayout,
      setPageScrollY: setPageScrollY,
      currentPageScrollY: currentPageScrollY,
      getState: function () {
        return JSON.parse(JSON.stringify({
          sightings: state.sightings,
          authenticated: state.authenticated,
          authState: state.authState,
          authFeedback: state.authFeedback,
          authInProgress: state.authInProgress,
          spacetimeAuth: state.spacetimeAuth,
          selectedStop: state.selectedStop,
          selectedVehicleId: state.selectedVehicleId,
          selectedVehicleMarkerKey: state.selectedVehicleMarkerKey,
          vehicleFollowPaused: state.vehicleFollowPaused,
          focusedMapEntity: state.focusedMapEntity,
          openMapDetailEntity: state.openMapDetailEntity,
          mapDetailDismissSuppressedUntil: state.mapDetailDismissSuppressedUntil,
          publicMapLoaded: state.publicMapLoaded,
          publicMapLoading: state.publicMapLoading,
          stopIncidents: state.stopIncidents,
          vehicleIncidents: state.vehicleIncidents,
          liveTransportVersion: state.liveTransportVersion,
          currentPosition: state.currentPosition,
          publicIncidents: state.publicIncidents,
          publicIncidentsLoaded: state.publicIncidentsLoaded,
          publicIncidentsLoading: state.publicIncidentsLoading,
          publicIncidentDetail: state.publicIncidentDetail,
          publicIncidentDetailLoading: state.publicIncidentDetailLoading,
          publicIncidentDetailLoadingId: state.publicIncidentDetailLoadingId,
          publicIncidentSelectedId: state.publicIncidentSelectedId,
          publicIncidentDetailOpen: state.publicIncidentDetailOpen,
          publicIncidentHistoryOpen: state.publicIncidentHistoryOpen,
          publicIncidentListScrollY: state.publicIncidentListScrollY,
          publicIncidentMobileLayout: state.publicIncidentMobileLayout,
          publicIncidentCommentDrafts: state.publicIncidentCommentDrafts,
          publicIncidentVoteSelections: state.publicIncidentVoteSelections,
        }));
      },
      formatEventTime: formatEventTime,
      formatRelativeReportAge: formatRelativeReportAge,
      latestReportAgeLabel: latestReportAgeLabel,
      groupSightingsByStop: groupSightingsByStop,
      pageTitleForMode: pageTitleForMode,
      syncPageTitle: syncPageTitle,
      readyStatusText: readyStatusText,
      applyAuthenticatedSession: applyAuthenticatedSession,
      applyAnonymousSession: applyAnonymousSession,
      bootstrapSession: bootstrapSession,
      refreshSpacetimeSession: refreshSpacetimeSession,
      telegramLoginConfigURL: telegramLoginConfigURL,
      telegramLoginPopupOrigin: telegramLoginPopupOrigin,
      telegramLoginPopupURL: telegramLoginPopupURL,
      telegramMiniAppInitData: telegramMiniAppInitData,
      ensureTelegramLoginLibrary: ensureTelegramLoginLibrary,
      runTelegramLoginPopup: runTelegramLoginPopup,
      completeTelegramLogin: completeTelegramLogin,
      completeTelegramWidgetLogin: completeTelegramWidgetLogin,
      completeTelegramMiniAppLogin: completeTelegramMiniAppLogin,
      completePendingTelegramMiniAppAuth: completePendingTelegramMiniAppAuth,
      fetchTelegramLoginConfig: fetchTelegramLoginConfig,
      decodeTelegramAuthResult: decodeTelegramAuthResult,
      consumeTelegramAuthResultFromURL: consumeTelegramAuthResultFromURL,
      completePendingTelegramAuthResult: completePendingTelegramAuthResult,
      consumeTelegramAuthStatusFromURL: consumeTelegramAuthStatusFromURL,
      restoreAuthFeedbackFromURL: restoreAuthFeedbackFromURL,
      beginTelegramLogin: beginTelegramLogin,
      logout: logout,
      displayStopName: displayStopName,
    },
  };
});
