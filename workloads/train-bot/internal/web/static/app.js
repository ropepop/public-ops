(function () {
  const cfg = window.TRAIN_APP_CONFIG || {};
  const reportsChannelURL = "https://t.me/vivi_kontrole_reports";
  const classicControlSiteURL = "https://kontrole.info/";
  const languageStorageKey = "trainAppLanguage";
  const debugTrainStateTransitions = (() => {
    try {
      if (window.location && /\btrainDebugState=1\b/.test(window.location.search || "")) {
        return true;
      }
      if (window.localStorage && window.localStorage.getItem("trainAppDebugState") === "1") {
        return true;
      }
      if (window.localStorage && window.localStorage.getItem("trainAppDebugState") === "true") {
        return true;
      }
    } catch (_) {}
    return false;
  })();
  function emptyMapLoadState() {
    return {
      active: false,
      mode: "",
      progress: 0,
      label: "",
    };
  }
  function createInitialState() {
    return {
      lang: savedBrowserLanguage(),
      messages: {},
      tab: "feed",
      window: "now",
      authenticated: false,
      me: null,
      currentRide: null,
      routeCheckIn: null,
      routeCheckInRoutes: [],
      siteMenuOpen: false,
      routeCheckInMenuOpen: false,
      routeCheckInLoading: false,
      routeCheckInSelectedRouteId: "",
      routeCheckInDurationMinutes: 120,
      routeCheckInDefaultDurationMinutes: 120,
      routeCheckInMinDurationMinutes: 30,
      routeCheckInMaxDurationMinutes: 480,
      windowTrains: [],
      selectedTrain: null,
      pinnedDetailTrainId: "",
      pinnedDetailFromUser: false,
      publicDashboard: [],
      publicDashboardAll: [],
      publicServiceDayTrains: [],
      publicTrain: null,
      mapTrainDetail: null,
      publicStationMatches: [],
      publicStationDepartures: null,
      publicStationSearchLoading: false,
      publicStationDeparturesLoading: false,
      publicIncidents: [],
      publicIncidentsLoading: false,
      publicIncidentsLoaded: false,
      publicIncidentDetail: null,
      publicIncidentSelectedId: "",
      publicIncidentDetailLoading: false,
      publicIncidentDetailLoadingId: "",
      publicIncidentDetailOpen: false,
      publicIncidentHistoryOpen: false,
      publicIncidentHistoryNavigating: false,
      publicIncidentListScrollY: 0,
      publicIncidentMobileLayout: false,
	      publicIncidentCommentDrafts: {},
	      publicIncidentVoteSelections: {},
	      miniAppPublicMapFallback: false,
	      publicNetworkMapShowAllSightings: false,
	      miniNetworkMapShowAllSightings: false,
      publicStationSelected: null,
      mapData: null,
      networkMapData: null,
      mapLoadState: emptyMapLoadState(),
      mapTrainId: "",
      mapPinnedTrainId: "",
      mapFollowTrainId: "",
      mapFollowPaused: false,
      publicMapPopupKey: "",
      publicMapSelectedMarkerKey: "",
      publicMapFollowPaused: false,
      mapIncidentFocusAppliedId: "",
      stations: [],
      selectedStation: null,
      stationDepartures: [],
      stationRecentSightings: [],
      stationSightingDestinations: [],
      stationSightingDestinationId: "",
      selectedSightingTrainId: "",
      expandedStationContextTrainId: "",
      expandedStopContextKey: "",
      favorites: [],
      originResults: [],
      destinationResults: [],
      routeResults: [],
      chosenOrigin: null,
      chosenDestination: null,
      dashboardFilter: "",
      publicFilter: "",
      publicFilterDraft: "",
      publicStationQuery: "",
      stationQuery: "",
      originQuery: "",
      destinationQuery: "",
      statusText: "",
      authState: "unknown",
      authFeedback: null,
      authInProgress: false,
      strictModeLoadError: null,
      scheduleMeta: cfg.schedule || null,
      toast: null,
      spacetimeAuth: null,
      checkInDropdownOpen: false,
      selectedCheckInTrainId: "",
      debugTrainStateTransitions,
      externalFeed: {
        enabled: Boolean(cfg.externalTrainMapEnabled),
        connectionState: cfg.externalTrainMapEnabled ? "idle" : "disabled",
        graphState: externalGraphEnabledByConfig() ? "idle" : "disabled",
        routes: [],
        liveTrains: [],
        activeStops: [],
        lastGraphAt: "",
        lastMessageAt: "",
        connectionError: "",
        graphError: "",
        error: "",
      },
    };
  }

  function resetStateForTest(overrides) {
    const nextState = Object.assign(createInitialState(), overrides || {});
    Object.keys(state).forEach((key) => {
      delete state[key];
    });
    Object.assign(state, nextState);
  }

  const appEl = document.getElementById("app");
  const state = createInitialState();
  restoreSpacetimeSession();
  const mapController = createMapController();
  const MAP_MARKER_ANIMATION_DEFAULT_MS = 900;
  const MAP_MARKER_ANIMATION_MIN_MS = 250;
  const MAP_MARKER_ANIMATION_MAX_MS = 2500;
  const MAP_MARKER_COORD_EPSILON = 0.000001;
  const MAP_TOUCH_PROXY_MAX_MOVEMENT_PX = 18;
  const MAP_TOUCH_PROXY_MAX_DURATION_MS = 650;
  const MAP_TOUCH_PROXY_RADIUS_PX = 34;
  const MAP_DETAIL_DISMISS_SUPPRESS_WINDOW_MS = 450;
  const MAP_USER_PAN_TOLERANCE_PX = 8;
  const MAP_DEFAULT_VIEW_ZOOM = 13;
  const INCIDENT_MOBILE_BREAKPOINT_PX = 980;
  const INCIDENT_OVERLAY_HISTORY_KEY = "__trainIncidentOverlay";
  const CHECKIN_RIDE_SETTLE_RETRIES = 4;
  const CHECKIN_RIDE_SETTLE_DELAY_MS = 250;
  const PUBLIC_DASHBOARD_VISIBLE_LIMIT = 60;
  const MAP_STATION_MARKER_MIN_HEIGHT_METERS = 1000;
  const MAP_STATION_MARKER_MAX_HEIGHT_METERS = 50;
  const MAP_STATION_SIZE_RANGES = {
    far: { min: 12, max: 14 },
    compact: { min: 14, max: 18 },
    detail: { min: 18, max: 22 },
  };
  let toastTimer = null;
  let externalFeedClient = null;
  let externalFeedRenderTimer = null;
  let selectedDetailSnapshot = null;
  let releaseMapRelayoutListeners = null;
  let liveClient = null;
  let releaseLiveInvalidation = null;
  let liveRenderTimer = null;
  let publicMapLibraryRetryTimer = null;
  let publicMapLibraryRetryCount = 0;

  function waitMs(ms) {
    return new Promise((resolve) => {
      window.setTimeout(resolve, Math.max(0, Number(ms) || 0));
    });
  }
  const staticBundleState = {
    manifestURL: "",
    manifest: null,
    slices: Object.create(null),
    indexes: null,
    loadPromise: null,
  };

  function bundleManifestURL() {
    return typeof cfg.bundleManifestURL === "string" ? cfg.bundleManifestURL.trim() : "";
  }

  function bundleEnabled() {
    return Boolean(bundleManifestURL());
  }

  function resolveAbsoluteURL(path, baseURL) {
    const rawPath = typeof path === "string" ? path.trim() : "";
    if (!rawPath) {
      return "";
    }
    const rawBase = typeof baseURL === "string" ? baseURL.trim() : "";
    const fallbackBase = (typeof cfg.publicBaseURL === "string" && cfg.publicBaseURL.trim())
      || (window.location && typeof window.location.href === "string" && window.location.href.trim())
      || "https://train-bot.local/";
    try {
      return new URL(rawPath, rawBase || fallbackBase).toString();
    } catch (_) {
      return new URL(rawPath, fallbackBase).toString();
    }
  }

  function currentLocationURL() {
    if (!window.location) {
      return null;
    }
    const href = typeof window.location.href === "string" ? window.location.href.trim() : "";
    if (href) {
      try {
        return new URL(href);
      } catch (_) {}
    }
    const pathname = typeof window.location.pathname === "string" ? window.location.pathname : "/";
    const search = typeof window.location.search === "string" ? window.location.search : "";
    const hash = typeof window.location.hash === "string" ? window.location.hash : "";
    try {
      return new URL(`${pathname}${search}${hash}`, "https://train-bot.local/");
    } catch (_) {
      return null;
    }
  }

  function pathFor(path) {
    const base = String(cfg.basePath || "").replace(/\/$/, "");
    if (!base) {
      return path;
    }
    return base + path;
  }

  function currentURL() {
    return currentLocationURL();
  }

  function currentWindowWidth() {
    if (window && typeof window.innerWidth === "number" && window.innerWidth > 0) {
      return window.innerWidth;
    }
    return INCIDENT_MOBILE_BREAKPOINT_PX + 1;
  }

  function isIncidentMobileLayout() {
    if (window && typeof window.matchMedia === "function") {
      return Boolean(window.matchMedia(`(max-width: ${INCIDENT_MOBILE_BREAKPOINT_PX}px)`).matches);
    }
    return currentWindowWidth() <= INCIDENT_MOBILE_BREAKPOINT_PX;
  }

  function currentPageScrollY() {
    if (window && typeof window.scrollY === "number") {
      return window.scrollY;
    }
    if (window && typeof window.pageYOffset === "number") {
      return window.pageYOffset;
    }
    return 0;
  }

  function setPageScrollY(value) {
    const nextValue = Math.max(0, Number(value) || 0);
    if (window && typeof window.scrollTo === "function") {
      window.scrollTo(0, nextValue);
      return;
    }
    if (window) {
      window.scrollY = nextValue;
      window.pageYOffset = nextValue;
    }
  }

  function incidentOverlayHistoryState() {
    const value = {};
    value[INCIDENT_OVERLAY_HISTORY_KEY] = true;
    return value;
  }

  function isIncidentOverlayHistoryState(value) {
    return Boolean(value && value[INCIDENT_OVERLAY_HISTORY_KEY]);
  }

  function selectedIncidentIdFromURL() {
    const url = currentURL();
    if (!url) {
      return "";
    }
    return String(url.searchParams.get("incident") || "").trim();
  }

  function syncIncidentURL(incidentId) {
    const url = currentURL();
    if (!window.history || typeof window.history.replaceState !== "function" || !url) {
      return;
    }
    if (incidentId) {
      url.searchParams.set("incident", String(incidentId).trim());
    } else {
      url.searchParams.delete("incident");
    }
    try {
      window.history.replaceState(window.history.state || null, "", url.pathname + url.search + url.hash);
    } catch (_) {}
  }

  function incidentIdParts(incidentId) {
    const parts = String(incidentId || "").trim().split(":");
    return {
      scope: String(parts[0] || "").trim().toLowerCase(),
      subjectId: String(parts[1] || "").trim(),
    };
  }

  function trainIdFromIncidentId(incidentId) {
    const parts = incidentIdParts(incidentId);
    return parts.scope === "train" ? parts.subjectId : "";
  }

  function stationIdFromIncident(item) {
    const location = item && item.location ? item.location : null;
    const parts = incidentIdParts(item && item.id);
    return String(
      (item && item.subjectId) ||
      (location && location.stationId) ||
      (location && location.subjectId) ||
      (parts.scope === "station" ? parts.subjectId : "") ||
      ""
    ).trim();
  }

  function incidentMapTargetInfo(summaryOrId) {
    const summary = typeof summaryOrId === "object" && summaryOrId ? summaryOrId : null;
    const id = summary ? String(summary.id || "").trim() : String(summaryOrId || "").trim();
    const parts = incidentIdParts(id);
    const rawTarget = summary && summary.mapTarget ? summary.mapTarget : null;
    const explicitType = String(rawTarget && rawTarget.type || "").trim().toLowerCase();
    const scope = String((summary && summary.scope) || parts.scope || "").trim().toLowerCase();
    const subjectId = String((summary && summary.subjectId) || parts.subjectId || "").trim();
    const fallbackType = scope === "train" || scope === "station" || scope === "area" ? scope : "";
    const type = explicitType === "train" || explicitType === "station" || explicitType === "area"
      ? explicitType
      : fallbackType;
    if (type === "train") {
      const trainInstanceId = String(
        (rawTarget && rawTarget.trainInstanceId) ||
        trainIdFromIncidentId(id) ||
        (scope === "train" ? subjectId : "") ||
        ""
      ).trim();
      return trainInstanceId ? { type, trainInstanceId } : null;
    }
    if (type === "station") {
      const stationId = String(
        (rawTarget && rawTarget.stationId) ||
        (scope === "station" ? subjectId : "") ||
        ""
      ).trim();
      return stationId ? { type, stationId } : null;
    }
    if (type === "area") {
      const incidentId = String((rawTarget && rawTarget.incidentId) || id || "").trim();
      return incidentId ? { type, incidentId } : null;
    }
    return null;
  }

  function incidentIdsFromMarkerItem(item) {
    return Array.isArray(item && item.incidentIds)
      ? item.incidentIds.map((id) => String(id || "").trim()).filter(Boolean)
      : [];
  }

  function incidentMarkerKeyInSet(incidentId, markerKeys) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId || !markerKeys || typeof markerKeys[Symbol.iterator] !== "function" || !mapController.markerState) {
      return "";
    }
    for (const markerKey of markerKeys) {
      const entry = mapController.markerState.get(markerKey);
      if (incidentIdsFromMarkerItem(entry && entry.item).includes(nextIncidentId)) {
        return markerKey;
      }
    }
    return "";
  }

  function incidentMarkerKeyMatching(incidentId, predicate) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId || !mapController.markerState || typeof mapController.markerState.forEach !== "function") {
      return "";
    }
    let foundKey = "";
    mapController.markerState.forEach((entry, markerKey) => {
      if (foundKey || (typeof predicate === "function" && !predicate(markerKey, entry))) {
        return;
      }
      if (incidentIdsFromMarkerItem(entry && entry.item).includes(nextIncidentId)) {
        foundKey = markerKey;
      }
    });
    return foundKey;
  }

  function incidentTrainMarkerKey(incidentId) {
    return incidentMarkerKeyInSet(incidentId, mapController.trainMarkerKeys)
      || incidentMarkerKeyMatching(incidentId, (markerKey) => String(markerKey || "").startsWith("live-train:"));
  }

  function existingIncidentMarkerKey(incidentId) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId || !mapController.markerState || typeof mapController.markerState.forEach !== "function") {
      return "";
    }
    return incidentTrainMarkerKey(nextIncidentId)
      || incidentMarkerKeyInSet(nextIncidentId, mapController.baseMarkerKeys)
      || incidentMarkerKeyInSet(nextIncidentId, mapController.sightingMarkerKeys)
      || incidentMarkerKeyMatching(nextIncidentId);
  }

  function incidentSummaryForMap(incidentId) {
    const id = String(incidentId || "").trim();
    if (!id) {
      return null;
    }
    if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === id) {
      return state.publicIncidentDetail.summary;
    }
    return (Array.isArray(state.publicIncidents) ? state.publicIncidents : [])
      .find((item) => item && item.id === id) || null;
  }

  function incidentMapTarget(summaryOrId) {
    const summary = typeof summaryOrId === "object" && summaryOrId ? summaryOrId : null;
    const id = summary ? summary.id : String(summaryOrId || "").trim();
    const target = incidentMapTargetInfo(summary || id);
    if (target && target.type === "train") {
      return {
        href: publicNetworkMapRoot(),
        markerKey: "",
      };
    }
    if (target && target.type === "station" && target.stationId) {
      return {
        href: publicNetworkMapRoot(),
        markerKey: networkStationMarkerKey({ id: target.stationId }),
      };
    }
    if (target && target.type === "area" && target.incidentId) {
      return {
        href: publicNetworkMapRoot(),
        markerKey: `area-incident:${target.incidentId}`,
      };
    }
    return {
      href: publicNetworkMapRoot(),
      markerKey: "",
    };
  }

  function urlWithIncidentParam(href, incidentId) {
    const rawHref = String(href || "").trim() || publicNetworkMapRoot();
    const base = currentURL() || new URL("https://train-bot.local/");
    const url = new URL(rawHref, base);
    url.searchParams.set("incident", String(incidentId || "").trim());
    if (/^https?:\/\//i.test(rawHref)) {
      return url.toString();
    }
    return url.pathname + url.search + url.hash;
  }

  function incidentMapURL(incidentId) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId) {
      return publicNetworkMapRoot();
    }
    const target = incidentMapTarget(incidentSummaryForMap(nextIncidentId) || nextIncidentId);
    return urlWithIncidentParam(target.href, nextIncidentId);
  }

  function navigateToIncidentMap(incidentId) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId || !window.location) {
      return;
    }
    const href = incidentMapURL(nextIncidentId);
    if (typeof window.location.assign === "function") {
      window.location.assign(href);
      return;
    }
    window.location.href = href;
  }

  function incidentMapFocusMarkerKey(incidentId) {
    const nextIncidentId = String(incidentId || "").trim();
    const summary = incidentSummaryForMap(nextIncidentId);
    const target = incidentMapTarget(summary || nextIncidentId);
    const targetInfo = incidentMapTargetInfo(summary || nextIncidentId);
    if (targetInfo && targetInfo.type === "train") {
      const trainMarkerKey = incidentTrainMarkerKey(nextIncidentId);
      if (trainMarkerKey) {
        return trainMarkerKey;
      }
    }
    if (target.markerKey) {
      return target.markerKey;
    }
    const existingMarkerKey = existingIncidentMarkerKey(nextIncidentId);
    if (existingMarkerKey) {
      return existingMarkerKey;
    }
    const parts = incidentIdParts((summary && summary.id) || nextIncidentId);
    const subjectId = String((summary && summary.subjectId) || parts.subjectId || "").trim();
    if (parts.scope === "train" && usesPublicTrainMap() && subjectId && state.mapData && state.mapData.train && state.mapData.train.id === subjectId) {
      const trainMarker = Array.from(mapController.trainMarkerKeys || [])[0] || "";
      if (trainMarker) {
        return trainMarker;
      }
      return Array.from(mapController.baseMarkerKeys || [])[0] || "";
    }
    return "";
  }

  function focusIncidentOnMap(incidentId, options) {
    const markerKey = incidentMapFocusMarkerKey(incidentId);
    if (!markerKey || !mapController.markerState || !mapController.markerState.has(markerKey)) {
      return false;
    }
    return mapController.handleMarkerInteraction(markerKey, options || {});
  }

  function focusRequestedIncidentFromURL(options) {
    const incidentId = selectedIncidentIdFromURL();
    if (!incidentId) {
      state.mapIncidentFocusAppliedId = "";
      return false;
    }
    if (state.mapIncidentFocusAppliedId === incidentId) {
      return false;
    }
    if (!focusIncidentOnMap(incidentId, options)) {
      return false;
    }
    state.mapIncidentFocusAppliedId = incidentId;
    return true;
  }

  function readTestTicketFromLocation() {
    const currentURL = currentLocationURL();
    if (!currentURL) {
      return "";
    }
    return currentURL.searchParams.get("test_ticket") || "";
  }

  function stripTestTicketFromLocation() {
    if (!window.history || typeof window.history.replaceState !== "function") {
      return;
    }
    const currentURL = currentLocationURL();
    if (!currentURL || !currentURL.searchParams.has("test_ticket")) {
      return;
    }
    currentURL.searchParams.delete("test_ticket");
    const nextPath = `${currentURL.pathname}${currentURL.search}${currentURL.hash}`;
    try {
      window.history.replaceState({}, "", nextPath);
    } catch (_) {}
  }

  async function fetchBundleJSON(url) {
    const response = await fetch(url, { method: "GET", credentials: "include" });
    if (!response.ok) {
      const err = new Error(`bundle request failed (${response.status})`);
      err.status = response.status;
      throw err;
    }
    return response.json();
  }

  async function ensureBundleManifest() {
    const manifestURL = bundleManifestURL();
    if (!manifestURL) {
      return null;
    }
    if (staticBundleState.manifest && staticBundleState.manifestURL === manifestURL) {
      return staticBundleState.manifest;
    }
    if (staticBundleState.loadPromise && staticBundleState.manifestURL === manifestURL) {
      return staticBundleState.loadPromise;
    }
    staticBundleState.manifestURL = manifestURL;
    staticBundleState.loadPromise = fetchBundleJSON(manifestURL).then((manifest) => {
      staticBundleState.manifest = manifest || null;
      staticBundleState.slices = Object.create(null);
      staticBundleState.indexes = null;
      if (!state.scheduleMeta && cfg.schedule) {
        state.scheduleMeta = cfg.schedule;
      }
      return staticBundleState.manifest;
    }).finally(() => {
      staticBundleState.loadPromise = null;
    });
    return staticBundleState.loadPromise;
  }

  async function ensureBundleSlices(names) {
    const manifest = await ensureBundleManifest();
    if (!manifest || !manifest.slices) {
      return null;
    }
    const pending = [];
    (Array.isArray(names) ? names : []).forEach((name) => {
      if (!name || Object.prototype.hasOwnProperty.call(staticBundleState.slices, name)) {
        return;
      }
      const relativeURL = manifest.slices[name];
      if (!relativeURL) {
        staticBundleState.slices[name] = null;
        return;
      }
      const absoluteURL = resolveAbsoluteURL(relativeURL, resolveAbsoluteURL(staticBundleState.manifestURL));
      pending.push(fetchBundleJSON(absoluteURL).then((payload) => {
        staticBundleState.slices[name] = payload;
      }));
    });
    if (pending.length) {
      await Promise.all(pending);
      staticBundleState.indexes = null;
    }
    return manifest;
  }

  async function ensureBundleIndexes(requiredSlices) {
    const manifest = await ensureBundleSlices(requiredSlices);
    if (!manifest) {
      return null;
    }
    if (staticBundleState.indexes) {
      return staticBundleState.indexes;
    }
    const stations = Array.isArray(staticBundleState.slices.stations) ? staticBundleState.slices.stations : [];
    const trains = Array.isArray(staticBundleState.slices.trains) ? staticBundleState.slices.trains : [];
    const stops = Array.isArray(staticBundleState.slices.stops) ? staticBundleState.slices.stops : [];
    const stationPasses = Array.isArray(staticBundleState.slices.stationPasses) ? staticBundleState.slices.stationPasses : [];
    const stationById = new Map();
    const trainById = new Map();
    const stopsByTrain = new Map();
    const passesByStation = new Map();
    stations.forEach((station) => {
      stationById.set(String(station.id || "").trim(), station);
    });
    trains.forEach((train) => {
      trainById.set(String(train.id || "").trim(), train);
    });
    stops.forEach((stop) => {
      const trainId = String(stop.trainInstanceId || "").trim();
      if (!stopsByTrain.has(trainId)) {
        stopsByTrain.set(trainId, []);
      }
      stopsByTrain.get(trainId).push(stop);
    });
    Array.from(stopsByTrain.values()).forEach((items) => {
      items.sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0));
    });
    stationPasses.forEach((pass) => {
      const stationId = String(pass.stationId || "").trim();
      if (!passesByStation.has(stationId)) {
        passesByStation.set(stationId, []);
      }
      passesByStation.get(stationId).push(pass);
    });
    Array.from(passesByStation.values()).forEach((items) => {
      items.sort((left, right) => new Date(left.passAt || "").getTime() - new Date(right.passAt || "").getTime());
    });
    staticBundleState.indexes = {
      manifest,
      stations,
      trains,
      stops,
      stationPasses,
      stationById,
      trainById,
      stopsByTrain,
      passesByStation,
    };
    return staticBundleState.indexes;
  }

  function bundleFreshnessGeneratedAt() {
    if (cfg.bundleFreshness && cfg.bundleFreshness.generatedAt) {
      return cfg.bundleFreshness.generatedAt;
    }
    if (staticBundleState.manifest && staticBundleState.manifest.generatedAt) {
      return staticBundleState.manifest.generatedAt;
    }
    return "";
  }

  function currentBundleIdentity() {
    const manifest = staticBundleState.manifest;
    return {
      version: String((manifest && manifest.version) || cfg.bundleVersion || "").trim(),
      serviceDate: String((manifest && manifest.serviceDate) || cfg.bundleServiceDate || "").trim(),
    };
  }

  function withBundleSchedule(scheduleMeta) {
    const baseSchedule = scheduleMeta && typeof scheduleMeta === "object"
      ? Object.assign({}, scheduleMeta)
      : null;
    const requestedServiceDate = String((baseSchedule && baseSchedule.requestedServiceDate) || "").trim();
    const bundleServiceDate = currentBundleIdentity().serviceDate;
    if (!requestedServiceDate || !bundleServiceDate || bundleServiceDate !== requestedServiceDate) {
      return baseSchedule;
    }
    return Object.assign({}, baseSchedule || {}, {
      requestedServiceDate: requestedServiceDate,
      effectiveServiceDate: bundleServiceDate,
      loadedServiceDate: bundleServiceDate,
      fallbackActive: false,
      available: true,
      sameDayFresh: true,
    });
  }

  function resolvedScheduleMeta() {
    return withBundleSchedule(state.scheduleMeta || cfg.schedule || null);
  }

  function bundleDefaultStatus() {
    return {
      state: "NO_REPORTS",
      confidence: "LOW",
      uniqueReporters: 0,
    };
  }

  function bundleDefaultTrainCard(train) {
    return {
      train: train,
      status: bundleDefaultStatus(),
      riders: 0,
    };
  }

  function bundlePassAt(stop) {
    return stop && (stop.departureAt || stop.arrivalAt) ? new Date(stop.departureAt || stop.arrivalAt) : null;
  }

  function bundleTrainsByWindow(trains, windowId, nowDate) {
    const current = nowDate || new Date();
    let start = new Date(current.getTime());
    let end = new Date(current.getTime());
    if (windowId === "now") {
      start = new Date(current.getTime() - (15 * 60 * 1000));
      end = new Date(current.getTime() + (15 * 60 * 1000));
    } else if (windowId === "next_hour") {
      end = new Date(current.getTime() + (60 * 60 * 1000));
    } else {
      start = new Date(current.getTime() - (30 * 60 * 1000));
      end = new Date(current.getTime());
      end.setHours(23, 59, 59, 0);
    }
    return (Array.isArray(trains) ? trains : []).filter((train) => {
      const departureAt = new Date(train && train.departureAt || "");
      return !Number.isNaN(departureAt.getTime()) && departureAt >= start && departureAt <= end;
    }).slice().sort((left, right) => new Date(left.departureAt).getTime() - new Date(right.departureAt).getTime());
  }

  function normalizeStationQueryValue(value) {
    let normalized = String(value || "").trim().toLowerCase();
    if (!normalized) {
      return "";
    }
    const folds = [
      ["ā", "a"],
      ["č", "c"],
      ["ē", "e"],
      ["ģ", "g"],
      ["ī", "i"],
      ["ķ", "k"],
      ["ļ", "l"],
      ["ņ", "n"],
      ["š", "s"],
      ["ū", "u"],
      ["ž", "z"],
    ];
    folds.forEach((entry) => {
      normalized = normalized.replaceAll(entry[0], entry[1]);
    });
    normalized = normalized.replaceAll("-", " ");
    return normalized.split(/\s+/).filter(Boolean).join(" ");
  }

  function filterBundleStations(stations, query) {
    const normalizedQuery = normalizeStationQueryValue(query || "");
    if (!normalizedQuery) {
      return Array.isArray(stations) ? stations.slice() : [];
    }
    return (Array.isArray(stations) ? stations : []).filter((station) => {
      const normalizedKey = normalizeStationQueryValue(station && (station.normalizedKey || station.name) || "");
      const normalizedName = normalizeStationQueryValue(station && station.name || "");
      return normalizedKey.indexOf(normalizedQuery) === 0 || normalizedName.indexOf(normalizedQuery) === 0;
    });
  }

  function bundleStationTrainCard(indexes, pass) {
    const train = indexes.trainById.get(String(pass.trainId || "").trim()) || null;
    if (!train) {
      return null;
    }
    const passAt = new Date(pass.passAt || "");
    return {
      trainCard: bundleDefaultTrainCard(train),
      stationId: String(pass.stationId || "").trim(),
      stationName: pass.stationName || "",
      passAt: Number.isNaN(passAt.getTime()) ? "" : passAt.toISOString(),
      sightingCount: 0,
      sightingContext: [],
    };
  }

  function bundleRouteDestinations(indexes, originStationId, query) {
    const destinations = new Map();
    indexes.stopsByTrain.forEach((stops) => {
      let originSeq = null;
      stops.forEach((stop) => {
        if (originSeq === null && String(stop.stationId || "").trim() === originStationId) {
          originSeq = Number(stop.seq || 0);
        }
      });
      if (originSeq === null || !stops.length) {
        return;
      }
      const terminalStop = stops[stops.length - 1];
      if (Number(terminalStop.seq || 0) <= originSeq) {
        return;
      }
      const stationId = String(terminalStop.stationId || "").trim();
      const station = indexes.stationById.get(stationId) || {
        id: stationId,
        name: terminalStop.stationName || stationId,
        normalizedKey: terminalStop.stationName || stationId,
      };
      destinations.set(stationId, station);
    });
    return filterBundleStations(Array.from(destinations.values()), query);
  }

  function bundleRouteTrainCards(indexes, originStationId, destinationStationId, nowDate) {
    const current = nowDate || new Date();
    const startTime = current.getTime() - (30 * 60 * 1000);
    const endTime = current.getTime() + (18 * 60 * 60 * 1000);
    const items = [];
    indexes.stopsByTrain.forEach((stops, trainId) => {
      let fromStop = null;
      let toStop = null;
      stops.forEach((stop) => {
        const stationId = String(stop.stationId || "").trim();
        if (!fromStop && stationId === originStationId) {
          fromStop = stop;
          return;
        }
        if (fromStop && !toStop && stationId === destinationStationId && Number(stop.seq || 0) > Number(fromStop.seq || 0)) {
          toStop = stop;
        }
      });
      if (!fromStop || !toStop) {
        return;
      }
      const fromPassAt = bundlePassAt(fromStop);
      const toPassAt = bundlePassAt(toStop);
      if (!fromPassAt || Number.isNaN(fromPassAt.getTime()) || fromPassAt.getTime() < startTime || fromPassAt.getTime() > endTime) {
        return;
      }
      const train = indexes.trainById.get(trainId) || null;
      if (!train) {
        return;
      }
      items.push({
        trainCard: bundleDefaultTrainCard(train),
        fromStationId: String(fromStop.stationId || "").trim(),
        fromStationName: fromStop.stationName || "",
        toStationId: String(toStop.stationId || "").trim(),
        toStationName: toStop.stationName || "",
        fromPassAt: fromPassAt.toISOString(),
        toPassAt: toPassAt && !Number.isNaN(toPassAt.getTime()) ? toPassAt.toISOString() : "",
      });
    });
    items.sort((left, right) => new Date(left.fromPassAt).getTime() - new Date(right.fromPassAt).getTime());
    return items;
  }

  async function resolveBundlePath(path, options) {
    if (!bundleEnabled()) {
      return null;
    }
    const spec = spacetimeProcedureFor(path, options || {});
    if (!spec) {
      return null;
    }
    const nowDate = new Date();
    const schedule = resolvedScheduleMeta();
    if (spec.kind === "public_dashboard") {
      const indexes = await ensureBundleIndexes(["trains"]);
      if (!indexes) return null;
      const items = bundleTrainsByWindow(indexes.trains, "today", nowDate).map((train) => ({
        train: train,
        status: bundleDefaultStatus(),
        timeline: [],
        stationSightings: [],
      }));
      return {
        generatedAt: bundleFreshnessGeneratedAt(),
        trains: spec.limit > 0 ? items.slice(0, spec.limit) : items,
        schedule: schedule,
      };
    }
    if (spec.kind === "public_service_day_trains") {
      const indexes = await ensureBundleIndexes(["trains"]);
      if (!indexes) return null;
      const items = indexes.trains.slice().sort((left, right) => {
        return new Date(left.departureAt).getTime() - new Date(right.departureAt).getTime();
      }).map((train) => ({
        train: train,
        status: bundleDefaultStatus(),
        timeline: [],
        stationSightings: [],
      }));
      return {
        generatedAt: bundleFreshnessGeneratedAt(),
        trains: items,
        schedule: schedule,
      };
    }
    if (spec.kind === "public_network_map") {
      const indexes = await ensureBundleIndexes(["stations"]);
      if (!indexes) return null;
      return {
        stations: indexes.stations.filter((station) => typeof station.latitude === "number" && typeof station.longitude === "number"),
        recentSightings: [],
        sameDaySightings: [],
        schedule: schedule,
      };
    }
    if (spec.kind === "public_train" || spec.kind === "train_stops" || spec.kind === "public_train_stops") {
      const indexes = await ensureBundleIndexes(["trains", "stops"]);
      if (!indexes) return null;
      const train = indexes.trainById.get(String(spec.trainId || "").trim()) || null;
      if (!train) {
        return null;
      }
      if (spec.kind === "public_train") {
        return {
          train: train,
          status: bundleDefaultStatus(),
          timeline: [],
          stationSightings: [],
          schedule: schedule,
        };
      }
      return {
        trainCard: bundleDefaultTrainCard(train),
        train: train,
        stops: indexes.stopsByTrain.get(String(spec.trainId || "").trim()) || [],
        stationSightings: [],
        schedule: schedule,
      };
    }
    if (spec.kind === "public_station_search" || spec.kind === "station_search") {
      const indexes = await ensureBundleIndexes(["stations"]);
      if (!indexes) return null;
      return {
        stations: filterBundleStations(indexes.stations, spec.query),
        schedule: schedule,
      };
    }
    if (spec.kind === "public_station_departures" || spec.kind === "station_departures" || spec.kind === "station_sighting_destinations") {
      const indexes = await ensureBundleIndexes(["stations", "trains", "stops", "stationPasses"]);
      if (!indexes) return null;
      const stationId = String(spec.stationId || "").trim();
      const station = indexes.stationById.get(stationId) || null;
      if (!station) {
        return null;
      }
      if (spec.kind === "station_sighting_destinations") {
        return {
          stations: bundleRouteDestinations(indexes, stationId, ""),
          schedule: schedule,
        };
      }
      const passes = indexes.passesByStation.get(stationId) || [];
      if (spec.kind === "public_station_departures") {
        let lastDeparture = null;
        const upcoming = [];
        const startOfDay = new Date(nowDate.getTime());
        startOfDay.setHours(0, 0, 0, 0);
        const endOfDay = new Date(nowDate.getTime());
        endOfDay.setHours(23, 59, 59, 999);
        passes.forEach((pass) => {
          const passAt = new Date(pass.passAt || "");
          if (Number.isNaN(passAt.getTime()) || passAt < startOfDay || passAt > endOfDay) {
            return;
          }
          const card = bundleStationTrainCard(indexes, pass);
          if (!card) {
            return;
          }
          if (passAt < nowDate) {
            lastDeparture = card;
            return;
          }
          upcoming.push(card);
        });
        return {
          station: station,
          lastDeparture: lastDeparture,
          upcoming: upcoming.slice(0, 8),
          recentSightings: [],
          schedule: schedule,
        };
      }
      const start = nowDate.getTime() - (2 * 60 * 60 * 1000);
      const end = nowDate.getTime() + (2 * 60 * 60 * 1000);
      const trains = passes.map((pass) => {
        const passAt = new Date(pass.passAt || "");
        if (Number.isNaN(passAt.getTime()) || passAt.getTime() < start || passAt.getTime() > end) {
          return null;
        }
        return bundleStationTrainCard(indexes, pass);
      }).filter(Boolean);
      return {
        station: station,
        trains: trains,
        recentSightings: [],
        schedule: schedule,
      };
    }
    if (spec.kind === "window_trains") {
      const indexes = await ensureBundleIndexes(["trains"]);
      if (!indexes) return null;
      return {
        trains: bundleTrainsByWindow(indexes.trains, spec.windowId, nowDate).map((train) => bundleDefaultTrainCard(train)),
        schedule: schedule,
      };
    }
    if (spec.kind === "route_destinations") {
      const indexes = await ensureBundleIndexes(["stations", "stops"]);
      if (!indexes) return null;
      return {
        stations: bundleRouteDestinations(indexes, String(spec.originStationId || "").trim(), spec.query),
        schedule: schedule,
      };
    }
    if (spec.kind === "route_trains") {
      const indexes = await ensureBundleIndexes(["stations", "trains", "stops"]);
      if (!indexes) return null;
      return {
        trains: bundleRouteTrainCards(indexes, String(spec.originStationId || "").trim(), String(spec.destinationStationId || "").trim(), nowDate),
        schedule: schedule,
      };
    }
    return null;
  }

  function mapZoomTier(zoom) {
    const numericZoom = Number(zoom);
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

  function coordinateDistanceMeters(latA, lngA, latB, lngB) {
    const earthRadiusMeters = 6371000;
    const latARad = Number(latA) * Math.PI / 180;
    const latBRad = Number(latB) * Math.PI / 180;
    const deltaLatRad = (Number(latB) - Number(latA)) * Math.PI / 180;
    const deltaLngRad = (Number(lngB) - Number(lngA)) * Math.PI / 180;
    const sinLat = Math.sin(deltaLatRad / 2);
    const sinLng = Math.sin(deltaLngRad / 2);
    const value = (sinLat * sinLat) + (Math.cos(latARad) * Math.cos(latBRad) * sinLng * sinLng);
    return earthRadiusMeters * (2 * Math.atan2(Math.sqrt(value), Math.sqrt(1 - value)));
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }

  function boundsHeightMeters(bounds) {
    let north = NaN;
    let south = NaN;
    let lng = NaN;
    if (!bounds) {
      return Infinity;
    }
    if (typeof bounds.getNorth === "function" && typeof bounds.getSouth === "function") {
      north = Number(bounds.getNorth());
      south = Number(bounds.getSouth());
    } else if (typeof bounds.getNorthWest === "function" && typeof bounds.getSouthWest === "function") {
      const northWest = bounds.getNorthWest();
      const southWest = bounds.getSouthWest();
      north = Number(northWest && northWest.lat);
      south = Number(southWest && southWest.lat);
      lng = Number(((northWest && northWest.lng) || 0) + ((southWest && southWest.lng) || 0)) / 2;
    }
    if (!Number.isFinite(lng) && typeof bounds.getCenter === "function") {
      const center = bounds.getCenter();
      lng = Number(center && center.lng);
    }
    if (!Number.isFinite(north) || !Number.isFinite(south) || !Number.isFinite(lng)) {
      return Infinity;
    }
    return coordinateDistanceMeters(north, lng, south, lng);
  }

  function interpolateStationMarkerSize(visibleHeightMeters, range) {
    const heightMeters = Number(visibleHeightMeters);
    let size = range.min;
    if (!Number.isFinite(heightMeters) || heightMeters >= MAP_STATION_MARKER_MIN_HEIGHT_METERS) {
      size = range.min;
    } else if (heightMeters <= MAP_STATION_MARKER_MAX_HEIGHT_METERS) {
      size = range.max;
    } else {
      const progress = clamp(
        (MAP_STATION_MARKER_MIN_HEIGHT_METERS - heightMeters)
          / (MAP_STATION_MARKER_MIN_HEIGHT_METERS - MAP_STATION_MARKER_MAX_HEIGHT_METERS),
        0,
        1
      );
      size = range.min + ((range.max - range.min) * progress);
    }
    return Math.round(size * 100) / 100;
  }

  function stationMarkerProfile(viewport) {
    const tier = mapZoomTier(viewport && viewport.zoom);
    const range = MAP_STATION_SIZE_RANGES[tier] || MAP_STATION_SIZE_RANGES.detail;
    const markerSize = interpolateStationMarkerSize(viewport && viewport.visibleHeightMeters, range);
    const iconExtent = Math.ceil(markerSize + 12);
    return {
      tier,
      markerSize,
      coreSize: Math.max(4, Math.round(markerSize * 0.4)),
      iconSize: [iconExtent, iconExtent],
      iconAnchor: [Math.round(iconExtent / 2), Math.round(iconExtent / 2)],
      popupAnchor: [0, -Math.max(12, Math.round(markerSize * 0.9))],
    };
  }

  function liveTrainMarkerProfile(viewport) {
    const tier = mapZoomTier(viewport && viewport.zoom);
    if (tier === "far") {
      return {
        tier,
        iconSize: [38, 22],
        iconAnchor: [19, 11],
        popupAnchor: [0, -12],
        markerHeight: 18,
        markerMinWidth: 28,
        markerPaddingX: 6,
        showLabel: true,
        compact: true,
      };
    }
    if (tier === "compact") {
      return {
        tier,
        iconSize: [44, 26],
        iconAnchor: [22, 13],
        popupAnchor: [0, -15],
        markerHeight: 20,
        markerMinWidth: 34,
        markerPaddingX: 7,
        showLabel: true,
        compact: true,
      };
    }
    return {
      tier: "detail",
      iconSize: [52, 30],
      iconAnchor: [26, 15],
      popupAnchor: [0, -19],
      markerHeight: 22,
      markerMinWidth: 38,
      markerPaddingX: 10,
      showLabel: true,
      compact: false,
    };
  }

  function trainMarkerVisualState(gpsClass, crewActive) {
    const stateKey = String(gpsClass || "").trim();
    const stateMap = {
      "gps-fresh": {
        bgColor: "rgba(205, 244, 233, 0.98)",
        bgImage: "none",
        borderColor: "rgba(12, 126, 103, 0.72)",
        textColor: "rgba(5, 62, 53, 0.98)",
        labelShadow: "rgba(255, 255, 255, 0.82)",
        boxShadow: "0 10px 22px rgba(12, 126, 103, 0.24)",
        borderStyle: "solid",
      },
      "gps-warm": {
        bgColor: "rgba(228, 244, 241, 0.98)",
        bgImage: "none",
        borderColor: "rgba(15, 107, 98, 0.5)",
        textColor: "rgba(9, 71, 65, 0.98)",
        labelShadow: "rgba(255, 255, 255, 0.82)",
        boxShadow: "0 10px 22px rgba(15, 107, 98, 0.22)",
        borderStyle: "solid",
      },
      "gps-projection": {
        bgColor: "rgba(252, 242, 221, 0.98)",
        bgImage: "linear-gradient(135deg, rgba(252, 242, 221, 0.98), rgba(246, 227, 187, 0.98))",
        borderColor: "rgba(174, 115, 29, 0.54)",
        textColor: "rgba(111, 71, 18, 0.98)",
        labelShadow: "rgba(255, 255, 255, 0.85)",
        boxShadow: "0 10px 22px rgba(174, 115, 29, 0.2)",
        borderStyle: "dashed",
      },
      "gps-stale": {
        bgColor: "rgba(246, 242, 236, 0.98)",
        bgImage: "none",
        borderColor: "rgba(108, 95, 82, 0.36)",
        textColor: "rgba(73, 64, 56, 0.98)",
        labelShadow: "rgba(255, 255, 255, 0.78)",
        boxShadow: "0 8px 18px rgba(108, 95, 82, 0.18)",
        borderStyle: "dashed",
      },
      "gps-scheduled": {
        bgColor: "rgba(246, 242, 236, 0.98)",
        bgImage: "none",
        borderColor: "rgba(108, 95, 82, 0.36)",
        textColor: "rgba(73, 64, 56, 0.98)",
        labelShadow: "rgba(255, 255, 255, 0.78)",
        boxShadow: "0 8px 18px rgba(108, 95, 82, 0.18)",
        borderStyle: "dashed",
      },
      default: {
        bgColor: "rgba(255, 251, 244, 0.98)",
        bgImage: "none",
        borderColor: "rgba(31, 27, 22, 0.18)",
        textColor: "rgba(31, 27, 22, 0.96)",
        labelShadow: "rgba(255, 255, 255, 0.8)",
        boxShadow: "0 8px 18px rgba(31, 27, 22, 0.18)",
        borderStyle: "solid",
      },
    };
    const visual = Object.assign({}, stateMap[stateKey] || stateMap.default);
    if (crewActive) {
      visual.borderColor = "rgba(21, 86, 132, 0.72)";
      visual.boxShadow = "0 0 0 1px rgba(21, 86, 132, 0.12), 0 10px 22px rgba(21, 86, 132, 0.24)";
    }
    return visual;
  }

  function applyTrainMarkerStateTransition(marker, previousItem, nextItem) {
    if (!marker || !previousItem || !nextItem || previousItem.kind !== "html" || nextItem.kind !== "html") {
      return;
    }
    const previousGpsClass = String(previousItem.gpsClass || "").trim();
    const nextGpsClass = String(nextItem.gpsClass || "").trim();
    if (!previousGpsClass || !nextGpsClass || previousGpsClass === nextGpsClass) {
      return;
    }
    const markerRoot = typeof marker.getElement === "function" ? marker.getElement() : marker._icon;
    const trainEl = markerRoot && typeof markerRoot.querySelector === "function"
      ? markerRoot.querySelector(".map-train-marker")
      : null;
    if (!trainEl || !trainEl.style) {
      return;
    }
    const from = trainMarkerVisualState(previousGpsClass, Boolean(previousItem.crewActive));
    trainEl.style.setProperty("--map-train-bg-color", from.bgColor);
    trainEl.style.setProperty("--map-train-bg-image", from.bgImage);
    trainEl.style.setProperty("--map-train-border", from.borderColor);
    trainEl.style.setProperty("--map-train-text", from.textColor);
    trainEl.style.setProperty("--map-train-label-shadow", from.labelShadow);
    trainEl.style.boxShadow = from.boxShadow;
    trainEl.style.borderStyle = from.borderStyle;
    trainEl.getBoundingClientRect();
    const finish = () => {
      trainEl.style.removeProperty("--map-train-bg-color");
      trainEl.style.removeProperty("--map-train-bg-image");
      trainEl.style.removeProperty("--map-train-border");
      trainEl.style.removeProperty("--map-train-text");
      trainEl.style.removeProperty("--map-train-label-shadow");
      trainEl.style.removeProperty("box-shadow");
      trainEl.style.removeProperty("border-style");
    };
    if (window && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(finish);
    } else {
      setTimeout(finish, 0);
    }
  }

  function currentMapZoom(map, fallbackZoom) {
    if (!map || !map._loaded || typeof map.getZoom !== "function") {
      return Number.isFinite(Number(fallbackZoom)) ? Number(fallbackZoom) : MAP_DEFAULT_VIEW_ZOOM;
    }
    const numericZoom = Number(map.getZoom());
    return Number.isFinite(numericZoom)
      ? numericZoom
      : (Number.isFinite(Number(fallbackZoom)) ? Number(fallbackZoom) : MAP_DEFAULT_VIEW_ZOOM);
  }

  function currentMapBounds(map) {
    if (!map || !map._loaded || typeof map.getBounds !== "function") {
      return null;
    }
    try {
      return map.getBounds();
    } catch (_) {
      return null;
    }
  }

  function mapViewportContext(map, fallbackView) {
    const fallbackZoom = fallbackView && Number.isFinite(Number(fallbackView.zoom))
      ? Number(fallbackView.zoom)
      : MAP_DEFAULT_VIEW_ZOOM;
    const bounds = currentMapBounds(map);
    const zoom = currentMapZoom(map, fallbackZoom);
    return {
      zoom,
      zoomTier: mapZoomTier(zoom),
      visibleHeightMeters: boundsHeightMeters(bounds),
      bounds,
    };
  }

  function currentWindowWidth() {
    if (typeof window !== "undefined" && typeof window.innerWidth === "number" && window.innerWidth > 0) {
      return window.innerWidth;
    }
    if (typeof window !== "undefined" && typeof window.outerWidth === "number" && window.outerWidth > 0) {
      return window.outerWidth;
    }
    return 0;
  }

  function shouldOpenMapDetailImmediately() {
    if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
      try {
        if (window.matchMedia("(max-width: 920px)").matches) {
          return true;
        }
      } catch (_error) {}
    }
    return currentWindowWidth() > 0 && currentWindowWidth() <= 920;
  }

  function targetTagName(target) {
    return target && target.tagName ? String(target.tagName).toUpperCase() : "";
  }

  function isTouchLikeEvent(event) {
    if (!event || !event.type) {
      return false;
    }
    if (String(event.type).startsWith("touch")) {
      return true;
    }
    return String(event.pointerType || "").toLowerCase() === "touch";
  }

  function eventClientPoint(event, options) {
    const preferChangedTouches = Boolean(options && options.preferChangedTouches);
    if (
      event &&
      Number.isFinite(Number(event.clientX)) &&
      Number.isFinite(Number(event.clientY))
    ) {
      return {
        x: Number(event.clientX),
        y: Number(event.clientY),
      };
    }
    const touchList = preferChangedTouches
      ? (event && event.changedTouches && event.changedTouches.length ? event.changedTouches : event && event.touches)
      : (event && event.touches && event.touches.length ? event.touches : event && event.changedTouches);
    if (!touchList || !touchList.length) {
      return null;
    }
    return {
      x: Number(touchList[0].clientX),
      y: Number(touchList[0].clientY),
    };
  }

  function pointDistance(left, right) {
    if (!left || !right) {
      return Number.POSITIVE_INFINITY;
    }
    const dx = Number(left.x) - Number(right.x);
    const dy = Number(left.y) - Number(right.y);
    return Math.sqrt((dx * dx) + (dy * dy));
  }

  function pointInsideRect(point, rect, padding) {
    if (!point || !rect) {
      return false;
    }
    const inset = Number.isFinite(Number(padding)) ? Number(padding) : 0;
    return Number(point.x) >= Number(rect.left) - inset
      && Number(point.x) <= Number(rect.right) + inset
      && Number(point.y) >= Number(rect.top) - inset
      && Number(point.y) <= Number(rect.bottom) + inset;
  }

  function rectFromElement(element) {
    if (!element || typeof element.getBoundingClientRect !== "function") {
      return null;
    }
    const rect = element.getBoundingClientRect();
    if (
      !rect ||
      !Number.isFinite(Number(rect.left)) ||
      !Number.isFinite(Number(rect.top)) ||
      Number(rect.width) <= 0 ||
      Number(rect.height) <= 0
    ) {
      return null;
    }
    return {
      left: Number(rect.left),
      top: Number(rect.top),
      width: Number(rect.width),
      height: Number(rect.height),
      right: Number(rect.right),
      bottom: Number(rect.bottom),
    };
  }

  function shouldShowSightingTags(zoom) {
    return mapZoomTier(zoom) !== "detail";
  }

  function publicRoot() {
    return cfg.publicBaseURL || cfg.basePath || "/";
  }

  function spacetimeSessionStorageKey() {
    return `train-app-spacetime:${cfg.basePath || "/"}`;
  }

  function normalizeSpacetimeSession(raw) {
    if (!raw || !raw.enabled || !raw.host || !raw.database || !raw.token || !raw.expiresAt) {
      return null;
    }
    const expiresAt = new Date(raw.expiresAt);
    if (!(expiresAt instanceof Date) || Number.isNaN(expiresAt.getTime()) || expiresAt.getTime() <= Date.now()) {
      return null;
    }
    return {
      enabled: true,
      host: String(raw.host).replace(/\/+$/, ""),
      database: String(raw.database),
      token: String(raw.token),
      expiresAt: expiresAt.toISOString(),
      issuer: raw.issuer ? String(raw.issuer) : "",
      audience: raw.audience ? String(raw.audience) : "",
    };
  }

  function persistSpacetimeSession(raw) {
    const next = normalizeSpacetimeSession(raw);
    state.spacetimeAuth = next;
    if (next) {
      state.authenticated = true;
    }
    try {
      if (!window.localStorage || typeof window.localStorage.setItem !== "function") {
        return next;
      }
      if (!next) {
        window.localStorage.removeItem(spacetimeSessionStorageKey());
        return null;
      }
      window.localStorage.setItem(spacetimeSessionStorageKey(), JSON.stringify(next));
    } catch (_) {}
    return next;
  }

  function restoreSpacetimeSession() {
    if (state.spacetimeAuth) {
      return normalizeSpacetimeSession(state.spacetimeAuth);
    }
    try {
      if (!window.localStorage || typeof window.localStorage.getItem !== "function") {
        return null;
      }
      const raw = window.localStorage.getItem(spacetimeSessionStorageKey());
      if (!raw) {
        return null;
      }
      return persistSpacetimeSession(JSON.parse(raw));
    } catch (_) {
      return null;
    }
  }

  function clearSpacetimeSession() {
    state.spacetimeAuth = null;
    try {
      if (window.localStorage && typeof window.localStorage.removeItem === "function") {
        window.localStorage.removeItem(spacetimeSessionStorageKey());
      }
    } catch (_) {}
  }

  function hasSpacetimeSession() {
    return Boolean(normalizeSpacetimeSession(state.spacetimeAuth));
  }

  function savedBrowserLanguage() {
    try {
      if (window.localStorage && typeof window.localStorage.getItem === "function") {
        return normalizeLang(window.localStorage.getItem(languageStorageKey));
      }
    } catch (_) {}
    return "LV";
  }

  function persistBrowserLanguage(lang) {
    try {
      if (window.localStorage && typeof window.localStorage.setItem === "function") {
        window.localStorage.setItem(languageStorageKey, normalizeLang(lang));
      }
    } catch (_) {}
  }

  function publicStationRoot() {
    return pathFor("/stations");
  }

  function publicDashboardRoot() {
    return pathFor("/feed");
  }

  function publicNetworkMapRoot() {
    return pathFor("/map");
  }

  function publicIncidentsRoot() {
    return pathFor("/events");
  }

  function classicControlRoot() {
    return classicControlSiteURL;
  }

  function classicControlLink() {
    return publicStatusLink(classicControlRoot(), t("app_open_classic_control"));
  }

  function publicTrainMapRoot(trainId) {
    return pathFor(`/t/${encodeURIComponent(trainId)}/map`);
  }

  const fallbackMessages = {
    app_title: "vivi kontrole bot",
    app_loading: "Loading train app…",
    app_public_dashboard_eyebrow: "Public feed",
    app_public_train_eyebrow: "Public train",
    app_public_station_eyebrow: "Station search",
    app_public_dashboard_title: "Live departures feed",
    app_public_dashboard_note: "Read-only live status for active departures and current train activity.",
    app_public_train_title: "Public train status",
    app_public_train_note: "Read-only live status and recent reports for this departure.",
    app_public_map_eyebrow: "Live map",
    app_public_map_title: "Live train map",
    app_public_map_note: "Follow this departure's live location when GPS is available.",
    app_public_dashboard_empty: "No departures are currently visible in the live feed.",
    app_auth_required: "Open this page from Telegram to report, vote, comment, and manage alert settings.",
    app_auth_required_body: "The incidents feed, departures, map, and station search remain available without Telegram sign-in.",
    app_login_telegram: "Sign in with Telegram",
    app_logout: "Log out",
    app_signing_in: "Signing in…",
    app_login_cancelled: "Telegram sign-in was cancelled.",
    app_login_popup_blocked: "Telegram sign-in popup could not open.",
	    app_login_failed: "Telegram sign-in failed. Try again.",
	    app_signed_in_as: "Signed in as %s",
	    app_menu: "Menu",
	    app_menu_close: "Close menu",
	    app_language: "Language",
	    app_language_lv: "LV",
	    app_language_en: "EN",
	    app_route_checkin_none: "No route watch",
	    app_route_checkin_active: "%s until %s",
	    app_route_checkin_watch: "Watch route",
	    app_route_checkin_change: "Change route",
	    app_route_checkin_login: "Sign in for route alerts",
	    app_route_checkin_route: "Route",
	    app_route_checkin_duration: "Duration",
	    app_route_checkin_start: "Start",
	    app_route_checkin_stop: "Stop",
	    app_route_checkin_no_routes: "No routes available",
	    app_route_checkin_choose_route: "Choose a route first.",
	    app_route_checkin_started: "Route watch started.",
	    app_route_checkin_stopped: "Route watch stopped.",
	    app_route_checkin_hours: "%s h",
	    app_route_checkin_minutes: "%s min",
	    app_status_ready: "Live view connected.",
    app_status_public: "Public read-only view.",
    app_status_telegram: "Telegram session active.",
    app_status_error: "Request failed.",
    app_status_error_with_code: "Request failed (%s).",
    app_data_unavailable_title: "Live train data is unavailable right now.",
    app_data_unavailable_body: "The train server could not load live data right now. Retry in a moment.",
    app_retry_data_load: "Retry data load",
    app_section_feed: "Feed",
    app_section_ride: "Ride",
    app_section_incidents: "Incidents",
    app_section_profile: "Profile",
    app_section_dashboard: "Dashboard",
    app_section_checkin: "Check in",
    app_section_my_ride: "My ride",
    app_section_report: "Report",
    app_section_sightings: "Sightings",
    app_section_map: "Map",
    app_section_settings: "Settings",
    app_find_station: "Find station",
    app_feed_intro: "Watch live departures and open any train for a clean status view.",
    app_profile_saved_routes: "Saved routes",
    app_profile_saved_routes_empty: "Save a route from a departure card to keep it here.",
    app_report_sighting: "Report Sighting",
    app_find_origin: "Find origin",
    app_find_destination: "Find destination",
    app_find_route: "Find route",
    app_live_status: "Live status",
    app_public_page: "Public page",
    app_open_public: "Open public page",
    app_open_departures: "Live feed",
    app_open_station_search: "Station search",
    app_open_classic_control: "Classic control",
    app_public_incidents_show_on_map: "Show on map",
    app_refresh: "Refresh",
    app_search: "Search",
    app_search_placeholder: "Type a station prefix",
    app_station_results: "Station matches",
    app_route_results: "Route departures",
    app_dashboard_filter: "Filter feed",
    app_dashboard_intro: "Track active departures and open any train for a live status view.",
    app_status_hint: "Select a departure to inspect status and timeline.",
    app_status_empty: "No departure selected.",
    app_current_ride_none: "You are not checked into a ride.",
    app_saved_routes: "Saved routes",
	    app_report_success: "Report accepted.",
	    app_report_deduped: "Already captured. No duplicate report sent.",
	    app_report_cooldown: "You can report again in %s min.",
	    app_station_report_action: "Controle sighted",
	    app_station_report_success: "Controle sighting accepted.",
    app_location_report_title: "Report location",
    app_location_report_description_label: "What is the location?",
    app_location_report_description_placeholder: "Short description",
    app_location_report_radius_label: "Radius",
    app_location_report_submit: "Report inspection here",
    app_location_report_success: "Location report accepted.",
    app_location_report_deduped: "That location report was already captured.",
    app_location_report_cooldown: "You can report this location again in %s min.",
    app_location_report_description_required: "Add a short location description.",
    app_report_notice: "Only report what you personally observe on this train.",
    app_settings_saved: "Settings saved.",
    app_checked_in: "Checked in.",
    app_checked_out: "Checked out.",
    app_undo_restored: "Ride restored.",
    app_subscribed: "Subscription updated.",
    app_unsubscribed: "Subscription removed.",
    app_muted: "Alerts muted.",
    app_favorite_saved: "Route saved.",
    app_favorite_removed: "Saved route removed.",
    app_search_complete: "Search complete.",
    app_refresh_success: "Refreshed.",
    app_status_loaded: "Status loaded.",
    app_map_loaded: "Live map loaded.",
    app_route_loaded: "Route departures updated.",
    status_no_reports: "Status: No reports today",
    status_last: "Status: Last inspection sighting %s",
    status_mixed: "Status: Mixed reports",
    app_choose_origin: "Choose origin first.",
    app_choose_destination: "Choose destination.",
    app_from: "From",
    app_to: "To",
    app_passes: "Passes",
    app_public_station_title: "Search departures by station",
    app_public_station_note: "Find a station and see its most recent departure plus the next departures today.",
    app_public_incidents_eyebrow: "Ongoing incidents",
    app_public_incidents_title: "Live incident feed",
    app_public_incidents_note: "Read the latest reports, see anonymous votes, and follow today’s incidents across the network.",
    app_public_incidents_loading: "Loading current situations…",
    app_public_incidents_detail_loading: "Loading situation thread…",
    app_public_incidents_empty: "No incidents have been reported yet today.",
    app_public_incidents_detail_empty: "Choose an incident to see its full thread.",
    app_public_incidents_back: "Back to events",
    app_public_deferred_title: "Feature rebuilding in progress",
    app_public_deferred_note: "The simplified train app keeps incidents, departures, stations, and the live map available while older views are rebuilt.",
    app_public_deferred_map_message: "The train map is temporarily unavailable while the simplified train app release is being rebuilt.",
    app_public_deferred_incidents_message: "Live incident threads are temporarily unavailable while the simplified train app release is being rebuilt.",
    app_public_incidents_vote_ongoing: "Still there",
    app_public_incidents_vote_cleared: "Not present",
    app_public_incidents_comment_label: "Comment anonymously",
    app_public_incidents_comment_placeholder: "Add a short anonymous update",
    app_public_incidents_comment_submit: "Post comment",
    app_public_incidents_vote_saved: "Vote saved.",
    app_public_incidents_comment_saved: "Comment posted.",
    app_public_incidents_auth_hint: "Open from Telegram or an active app session to vote and comment.",
    app_public_incidents_activity: "Activity",
    app_public_incidents_comments: "Comments",
    app_public_incidents_activity_empty: "No activity yet for this incident.",
    app_public_incidents_comments_empty: "No comments yet for this incident.",
    app_public_incidents_last_reporter: "Last by %s",
    app_public_station_search_label: "Station",
    app_public_station_search_placeholder: "Type the start of a station name",
    app_public_station_matches: "Matching stations",
    app_public_station_no_matches: "No stations matched that search.",
    app_public_station_prompt: "Search for a station to load departures.",
    app_public_station_search_loading: "Loading station matches…",
    app_public_station_departures_loading: "Loading station departures…",
    app_public_station_selected: "Selected station",
    app_public_station_last: "Last departure",
    app_public_station_upcoming: "Upcoming departures",
    app_public_station_empty: "No departures found for this station today.",
    app_public_station_last_empty: "No earlier departures today.",
    app_public_station_upcoming_empty: "No upcoming departures today.",
    app_public_station_search_success: "Station results updated.",
    app_public_station_departures_loaded: "Station departures updated.",
    app_recent_platform_sightings: "Recent platform sightings",
    app_station_sighting_empty: "No recent platform sightings.",
    app_station_sighting_title: "Report platform sighting",
    app_station_sighting_note: "Choose the departure you saw. Destination filtering is optional.",
    app_station_sighting_destination_label: "Destination (optional)",
    app_station_sighting_destination_any: "No destination specified",
    app_station_sighting_submit: "Report platform sighting",
    app_station_sighting_candidates: "Candidate departures",
    app_station_sighting_selected_departure: "Selected departure",
    app_station_sighting_select_departure: "Use this departure",
    app_station_sighting_select_departure_toast: "Select a departure before reporting a platform sighting.",
    app_station_sighting_departures_empty: "No departures in this station window match the current filter.",
    app_station_sighting_success: "Platform sighting reported.",
    app_station_sighting_deduped: "That platform sighting was already captured.",
    app_station_sighting_cooldown: "You can report this platform again in %s min.",
    app_station_sighting_matched: "Matched to a departure.",
    app_station_sighting_unmatched: "Saved as a station-level sighting.",
    app_sighting_metric_zero: "0 sightings",
    app_sighting_metric_one: "1 sighting",
    app_sighting_metric_many: "%s sightings",
    app_sighting_context_open_full: "Open full sightings view",
    app_sighting_context_title: "Sighting context",
    app_sightings_empty: "Select a station from Dashboard before reporting a platform sighting.",
    app_sightings_choose_station: "Choose station",
    app_map_prompt: "Choose a departure to follow its live location.",
    app_map_empty: "No current live location is available for this departure right now.",
    app_map_loading_title: "Loading map",
    app_map_loading_train: "Fetching stops and recent sightings for this departure.",
    app_map_loading_network: "Fetching stations and recent sightings across the network.",
    app_map_missing_coords: "Map coordinates are unavailable for some stops. The full ordered stop list is still shown below.",
    app_network_map_title: "Network map",
    app_network_map_note: "No active ride is selected. Showing live GPS trains and recent projected positions when GPS drops out.",
    app_network_map_empty: "No trains are currently broadcasting live location.",
    app_network_map_activity_title: "All train activity",
    app_network_map_activity_empty: "No departures are available to show right now.",
    app_network_map_show_all: "Show all trains",
    app_network_map_toggle_label: "Show older and unrelated sightings",
    app_network_map_toggle_hint_default: "Default view stays focused on current matched sightings.",
    app_network_map_toggle_hint_all: "Showing all sightings reported today, including older and unmatched ones.",
    app_public_network_map_title: "Live network map",
    app_public_network_map_note: "See live GPS trains and recent projected positions when GPS drops out.",
    app_map_popup_destination: "Destination",
    app_map_popup_status: "Status",
    app_map_popup_age: "Age",
    app_map_popup_seen_at: "Seen at",
    app_map_popup_next_stop: "Next stop",
    app_map_popup_last_update: "Last update",
    app_map_popup_live_train: "Live train",
    app_map_popup_recent_reports: "Recent reports",
    app_map_popup_recent_sightings: "Recent sightings",
    app_map_popup_crew: "Crew",
    app_map_popup_schedule: "Schedule",
    app_map_popup_live_now: "Live now",
    app_map_tag_now: "now",
    app_detail_state_no_reports: "No reports today",
    app_detail_state_last_sighting: "Inspection sighting",
    app_detail_state_mixed_reports: "Mixed reports",
    app_detail_last_update_aria: "Last update %s",
    app_live_overlay_ready: "Live locations connected.",
    app_live_overlay_connecting: "Live locations reconnecting.",
    app_live_overlay_graph_unavailable: "Live locations connected.",
    app_live_overlay_offline: "Live locations are unavailable right now.",
    app_live_overlay_unavailable: "Live overlay disabled.",
    app_stop_list: "Ordered stops",
    app_stop_context_open_full: "Open station sightings",
    app_view_stops_map: "Live map",
    app_schedule_unavailable_detail: "Live train data is unavailable for the requested date right now.",
    settings_alerts_label: "Notifications",
    settings_alert_style_label: "Notification style",
    settings_language_label: "Language",
    settings_reports_channel_label: "Reports channel",
    settings_style_detailed_option: "Detailed",
    settings_style_discreet_option: "Discreet",
    btn_open_reports_channel: "Open vivi kontrole Reports",
    link_reports_channel: reportsChannelURL,
    app_relative_now: "just now",
    app_relative_one_min: "1 min ago",
    app_relative_many_mins: "%s min ago",
    app_schedule_unavailable: "Schedule unavailable right now.",
    schedule_fallback_notice: "Showing schedule for %s while new-day data is still loading.",
    btn_open_app: "Open app",
  };

  document.addEventListener("DOMContentLoaded", () => {
    boot().catch((err) => {
      if (!handleInitialLoadError(err)) {
        renderFatal(err);
      }
    });
  });

  function supportsLiveClient() {
    return Boolean(
      cfg.spacetimeHost &&
      cfg.spacetimeDatabase &&
      window.TrainAppLiveClient &&
      typeof window.TrainAppLiveClient.create === "function"
    );
  }

  function externalGraphEnabledByConfig() {
    if (!cfg.externalTrainMapEnabled) {
      return false;
    }
    if (cfg.externalTrainGraphURL) {
      return true;
    }
    return Boolean(cfg.externalTrainMapBaseURL);
  }

  function mappedSpacetimeProcedure(path, options) {
    return spacetimeProcedureFor(path, options || {});
  }

  function isParkedSpacetimeRoute(pathname, method) {
    if (pathname === "/public/incidents" && method === "GET") {
      return true;
    }
    if (/^\/public\/incidents\/[^/]+$/.test(pathname) && method === "GET") {
      return true;
    }
    if (pathname === "/checkins/current" && (method === "PUT" || method === "DELETE")) {
      return true;
    }
	    if (pathname === "/checkins/current/undo" && method === "POST") {
	      return true;
	    }
	    if (pathname === "/public/route-checkin-routes" && method === "GET") {
	      return true;
	    }
	    if (pathname === "/route-checkins/current" && (method === "GET" || method === "POST" || method === "DELETE")) {
	      return true;
	    }
    if (/^\/stations\/[^/]+\/sighting-destinations$/.test(pathname) && method === "GET") {
      return true;
    }
    if (/^\/stations\/[^/]+\/sightings$/.test(pathname) && method === "POST") {
      return true;
    }
    if (/^\/trains\/[^/]+\/reports$/.test(pathname) && method === "POST") {
      return true;
    }
    if (/^\/trains\/[^/]+\/mute$/.test(pathname) && method === "PUT") {
      return true;
    }
    if (/^\/incidents\/[^/]+\/votes$/.test(pathname) && method === "POST") {
      return true;
    }
    if (/^\/incidents\/[^/]+\/comments$/.test(pathname) && method === "POST") {
      return true;
    }
    return false;
  }

  function spacetimeTransportAvailable() {
    return Boolean(hasSpacetimeSession() || (cfg.spacetimeHost && cfg.spacetimeDatabase));
  }

  function usesStrictSpacetimePath(path, options) {
    return Boolean(mappedSpacetimeProcedure(path, options));
  }

  function clearStrictModeLoadError() {
    state.strictModeLoadError = null;
  }

  function rememberStrictModeLoadError(err, blocking) {
    rememberErrorStatus(err);
    state.strictModeLoadError = {
      blocking: Boolean(blocking),
      message: err && err.message ? err.message : String(err),
    };
    return true;
  }

  function handleInitialLoadError(err) {
    if (rememberStrictModeLoadError(err, true)) {
      renderDataUnavailable();
      return true;
    }
    rememberErrorStatus(err);
    return false;
  }

  function handleCurrentViewRefreshError(err) {
    if (rememberStrictModeLoadError(err, false)) {
      rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
      return true;
    }
    setStatusFromError(err);
    return false;
  }

  function handleCurrentViewLoadSuccess() {
    clearStrictModeLoadError();
  }

  async function ensureLiveClient(sessionOverride) {
    if (!supportsLiveClient()) {
      return null;
    }
    if (!liveClient) {
      liveClient = window.TrainAppLiveClient.create({
        host: cfg.spacetimeHost,
        database: cfg.spacetimeDatabase,
      });
    }
    const session = typeof sessionOverride === "undefined"
      ? normalizeSpacetimeSession(state.spacetimeAuth)
      : sessionOverride;
    const connected = await liveClient.connect(session || null);
    return connected ? liveClient : null;
  }

  function clearLiveInvalidation() {
    if (typeof releaseLiveInvalidation === "function") {
      releaseLiveInvalidation();
    }
    releaseLiveInvalidation = null;
  }

  function stopLiveRenderTimer() {
    if (liveRenderTimer) {
      clearInterval(liveRenderTimer);
      liveRenderTimer = null;
    }
  }

  function startLiveRenderTimer(renderFn) {
    stopLiveRenderTimer();
  }

  async function activateLiveRefresh(refreshFn, renderFn) {
    const client = await ensureLiveClient();
    clearLiveInvalidation();
    stopLiveRenderTimer();
    if (!client) {
      return false;
    }
    releaseLiveInvalidation = client.onInvalidate(() => {
      Promise.resolve()
        .then(() => refreshFn())
        .then(() => handleCurrentViewLoadSuccess())
        .catch((err) => handleCurrentViewRefreshError(err));
    });
    return true;
  }

  async function refreshMiniAppLiveState() {
    if (!state.authenticated) {
      return;
    }
    const previousSelectedTrainId = detailTargetTrainId();
    await refreshMe();
    if (state.tab === "feed") {
      await Promise.all([refreshWindowTrains(), refreshPublicIncidents()]);
    }
    if (state.tab === "stations" && state.selectedStation) {
      await fetchStationDepartures(state.selectedStation.id);
    }
    if (state.selectedTrain && state.selectedTrain.trainCard) {
      const activeSelectedTrainId = state.selectedTrain.trainCard.train.id;
      if (!currentRideTrainId() || activeSelectedTrainId !== currentRideTrainId()) {
        const next = await api(`/trains/${encodeURIComponent(activeSelectedTrainId)}/status`);
        if (!samePayloadIgnoringSchedule(state.selectedTrain, next)) {
          state.selectedTrain = next;
        }
      }
    }
    renderMiniApp({ preserveDetail: true, previousSelectedTrainId });
  }

  function isPublicMode() {
    return String(cfg.mode || "").indexOf("public-") === 0;
  }

  function isMiniAppPublicMapFallback() {
    return cfg.mode === "mini-app" && Boolean(state.miniAppPublicMapFallback);
  }

  async function bootPublicNetworkMapSurface(options = {}) {
    const render = typeof options.render === "function" ? options.render : renderPublicNetworkMap;
    const activate = typeof options.activateLiveRefresh === "function" ? options.activateLiveRefresh : activateLiveRefresh;
    const intervalFn = typeof options.setInterval === "function" ? options.setInterval : setInterval;

    try {
      await Promise.all([refreshPublicNetworkMap(), refreshPublicDashboardAll()]);
      handleCurrentViewLoadSuccess();
    } catch (err) {
      if (handleInitialLoadError(err)) {
        return true;
      }
      throw err;
	    }
	    render();
    void refreshPublicNetworkMapIncidentOverlay(render);
	    if (await activate(async () => {
	      const results = await Promise.all([refreshPublicNetworkMap(), refreshPublicDashboardAll()]);
	      if (results.some(Boolean)) {
	        render();
	      }
      void refreshPublicNetworkMapIncidentOverlay(render);
	    }, () => render())) {
	      return true;
	    }
    intervalFn(async () => {
      try {
        const results = await Promise.all([refreshPublicNetworkMap(), refreshPublicDashboardAll()]);
        handleCurrentViewLoadSuccess();
	        if (results.some(Boolean)) {
	          render();
	        }
        void refreshPublicNetworkMapIncidentOverlay(render);
	      } catch (err) {
	        handleCurrentViewRefreshError(err);
	      }
    }, cfg.publicRefreshMs || 30000);
	    return true;
	  }

  async function refreshPublicNetworkMapIncidentOverlay(render) {
    try {
      const changed = await refreshPublicIncidents();
      if (changed && typeof render === "function") {
        render();
      }
      focusRequestedIncidentFromURL({ animate: false });
      return changed;
    } catch (err) {
      handleCurrentViewRefreshError(err);
      return false;
    }
  }

		  async function bootMiniAppAnonymousFallback(options = {}) {
	    await ensurePublicSession();
	    state.miniAppPublicMapFallback = true;
	    return bootPublicNetworkMapSurface(options);
	  }

  async function boot() {
    bindGlobalDocumentEvents();
    bindMapRelayoutListeners();
    await loadMessages(state.lang);
    renderLoading();
    startExternalFeedIfNeeded();
	    if (isPublicMode()) {
	      const handledTelegramAuth = await completePendingTelegramAuthResult({ refresh: false });
	      if (!handledTelegramAuth) {
	        await ensurePublicSession();
	      }
	      void ensureRouteCheckInRoutes().then(() => rerenderCurrent({ preserveInputFocus: true, preserveDetail: true })).catch(() => {});
	    }

    if (cfg.mode === "public-dashboard") {
      try {
        await refreshPublicDashboard();
        handleCurrentViewLoadSuccess();
      } catch (err) {
        if (handleInitialLoadError(err)) {
          return;
        }
      }
      renderPublicDashboard();
      if (await activateLiveRefresh(async () => {
        if (await refreshPublicDashboard()) {
          renderPublicDashboard();
        }
      }, () => renderPublicDashboard())) {
        return;
      }
      setInterval(async () => {
        try {
          if (await refreshPublicDashboard()) {
            renderPublicDashboard();
          }
          handleCurrentViewLoadSuccess();
        } catch (err) {
          handleCurrentViewRefreshError(err);
        }
      }, cfg.publicRefreshMs || 30000);
      return;
    }

    if (cfg.mode === "public-train") {
      try {
        await refreshPublicTrain();
        handleCurrentViewLoadSuccess();
      } catch (err) {
        if (handleInitialLoadError(err)) {
          return;
        }
      }
      renderPublicTrain();
      if (await activateLiveRefresh(async () => {
        if (await refreshPublicTrain()) {
          renderPublicTrain();
        }
      }, () => renderPublicTrain())) {
        return;
      }
      setInterval(async () => {
        try {
          if (await refreshPublicTrain()) {
            renderPublicTrain();
          }
          handleCurrentViewLoadSuccess();
        } catch (err) {
          handleCurrentViewRefreshError(err);
        }
      }, cfg.publicRefreshMs || 30000);
      return;
    }

    if (cfg.mode === "public-deferred-map") {
      handleCurrentViewLoadSuccess();
      renderDeferredPublicPage("map");
      return;
    }

    if (cfg.mode === "public-deferred-incidents") {
      handleCurrentViewLoadSuccess();
      renderDeferredPublicPage("incidents");
      return;
    }

    if (cfg.mode === "public-map") {
      try {
        await refreshMapData(cfg.trainId, true);
        handleCurrentViewLoadSuccess();
      } catch (err) {
        if (handleInitialLoadError(err)) {
          return;
        }
      }
      renderPublicMap();
      if (await activateLiveRefresh(async () => {
        if (await refreshMapData(cfg.trainId, true)) {
          renderPublicMap();
        }
      }, () => renderPublicMap())) {
        return;
      }
      setInterval(async () => {
        try {
          if (await refreshMapData(cfg.trainId, true)) {
            handleCurrentViewLoadSuccess();
            renderPublicMap();
            return;
          }
          handleCurrentViewLoadSuccess();
        } catch (err) {
          handleCurrentViewRefreshError(err);
        }
      }, cfg.publicRefreshMs || 30000);
      return;
    }

    if (cfg.mode === "public-network-map") {
      await bootPublicNetworkMapSurface();
      return;
    }

    if (cfg.mode === "public-stations") {
      handleCurrentViewLoadSuccess();
      renderPublicStationSearch();
      if (await activateLiveRefresh(async () => {
        if (state.publicStationSelected && state.publicStationSelected.id) {
          if (await refreshPublicStationDepartures(state.publicStationSelected.id)) {
            renderPublicStationSearch();
          }
        }
      }, () => renderPublicStationSearch())) {
        return;
      }
      setInterval(async () => {
        try {
          if (state.publicStationSelected && state.publicStationSelected.id) {
            if (await refreshPublicStationDepartures(state.publicStationSelected.id)) {
              renderPublicStationSearch();
            }
          }
          handleCurrentViewLoadSuccess();
        } catch (err) {
          handleCurrentViewRefreshError(err);
        }
      }, cfg.publicRefreshMs || 30000);
      return;
    }

    if (cfg.mode === "public-incidents") {
      try {
        state.publicIncidentsLoading = true;
        renderPublicIncidents();
        await refreshPublicIncidents();
        if (state.publicIncidentSelectedId) {
          await refreshPublicIncidentDetail(state.publicIncidentSelectedId);
        } else if (state.publicIncidents[0] && !state.publicIncidentMobileLayout) {
          await refreshPublicIncidentDetail(state.publicIncidents[0].id);
        }
        handleCurrentViewLoadSuccess();
      } catch (err) {
        if (handleInitialLoadError(err)) {
          return;
        }
      }
      renderPublicIncidents();
      if (await activateLiveRefresh(async () => {
        let shouldRender = false;
        shouldRender = (await refreshPublicIncidents()) || shouldRender;
        if (state.publicIncidentSelectedId) {
          shouldRender = (await refreshPublicIncidentDetail(state.publicIncidentSelectedId)) || shouldRender;
        } else if (state.publicIncidents[0] && !state.publicIncidentMobileLayout) {
          shouldRender = (await refreshPublicIncidentDetail(state.publicIncidents[0].id)) || shouldRender;
        }
        if (shouldRender) {
          renderPublicIncidents();
        }
      }, () => renderPublicIncidents())) {
        return;
      }
      setInterval(async () => {
        try {
          const listChanged = await refreshPublicIncidents();
          let detailChanged = false;
          if (state.publicIncidentSelectedId) {
            detailChanged = await refreshPublicIncidentDetail(state.publicIncidentSelectedId);
          } else if (state.publicIncidents[0] && !state.publicIncidentMobileLayout) {
            detailChanged = await refreshPublicIncidentDetail(state.publicIncidents[0].id);
          }
          handleCurrentViewLoadSuccess();
          if (listChanged || detailChanged) {
            renderPublicIncidents();
          }
        } catch (err) {
          handleCurrentViewRefreshError(err);
        }
      }, cfg.publicRefreshMs || 30000);
      return;
    }

    try {
      await authenticateMiniApp();
      if (!state.authenticated) {
        await bootMiniAppAnonymousFallback();
        return;
      }
      await loadMiniAppInitialData({
        rethrowPrimaryError: true,
        rememberBackgroundErrorStatus: handleCurrentViewRefreshError,
      });
      handleCurrentViewLoadSuccess();
    } catch (err) {
      if (handleInitialLoadError(err)) {
        return;
      }
      throw err;
    }
    if (await ensureLiveClient(normalizeSpacetimeSession(state.spacetimeAuth))) {
      if (await activateLiveRefresh(async () => {
        await refreshMiniAppLiveState();
      }, () => {
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
      })) {
        return;
      }
    }
    setInterval(async () => {
      if (!state.authenticated) return;
      try {
        await refreshMiniAppLiveState();
        handleCurrentViewLoadSuccess();
      } catch (err) {
        handleCurrentViewRefreshError(err);
      }
    }, cfg.miniAppRefreshMs || 15000);
  }

  function telegramWebApp() {
    return window.Telegram && window.Telegram.WebApp ? window.Telegram.WebApp : null;
  }

  function telegramInitData() {
    const tg = telegramWebApp();
    if (tg && typeof tg.initData === "string" && tg.initData) {
      return tg.initData;
    }
    const hash = String(window.location && window.location.hash || "");
    if (!hash || hash.length <= 1) {
      return "";
    }
    const params = new URLSearchParams(hash.slice(1));
    return String(params.get("tgWebAppData") || "");
  }

  function requestMapRelayout(reason, controller) {
    const nextController = controller || mapController;
    if (!nextController || typeof nextController.requestRelayout !== "function") {
      return;
    }
    nextController.requestRelayout(reason || "map-relayout");
  }

  function bindMapRelayoutListeners() {
    if (releaseMapRelayoutListeners) {
      return releaseMapRelayoutListeners;
    }
    releaseMapRelayoutListeners = bindMapRelayoutListenersWithEnvironment(window, telegramWebApp(), mapController);
    return releaseMapRelayoutListeners;
  }

  function bindMapRelayoutListenersWithEnvironment(win, tg, controller) {
    const nextWindow = win || null;
    const nextController = controller || null;
    const detach = [];
    const bindWindowEvent = (eventName, reason) => {
      if (!nextWindow || typeof nextWindow.addEventListener !== "function") {
        return;
      }
      const handler = () => {
        requestMapRelayout(reason, nextController);
      };
      nextWindow.addEventListener(eventName, handler);
      detach.push(() => {
        if (typeof nextWindow.removeEventListener === "function") {
          nextWindow.removeEventListener(eventName, handler);
        }
      });
    };

    bindWindowEvent("resize", "window-resize");
    bindWindowEvent("orientationchange", "orientation-change");

    if (tg && typeof tg.onEvent === "function") {
      const handler = () => {
        requestMapRelayout("telegram-viewport-changed", nextController);
      };
      tg.onEvent("viewportChanged", handler);
      detach.push(() => {
        if (typeof tg.offEvent === "function") {
          tg.offEvent("viewportChanged", handler);
        }
      });
    }

    const cleanup = () => {
      while (detach.length) {
        const release = detach.pop();
        try {
          release();
        } catch (_) {
          // Ignore listener cleanup failures in test and browser environments.
        }
      }
      if (releaseMapRelayoutListeners === cleanup) {
        releaseMapRelayoutListeners = null;
      }
    };
    return cleanup;
  }

  function expandTelegramWebApp(tg, controller) {
    if (!tg || typeof tg.expand !== "function") {
      return;
    }
    tg.expand();
    requestMapRelayout("telegram-expand", controller);
  }

  async function loadMiniAppInitialData(options) {
    const settings = options || {};
    const render = typeof settings.render === "function" ? settings.render : renderMiniApp;
    const rememberPrimaryError = typeof settings.rememberPrimaryErrorStatus === "function"
      ? settings.rememberPrimaryErrorStatus
      : (typeof settings.rememberErrorStatus === "function" ? settings.rememberErrorStatus : rememberErrorStatus);
    const rememberBackgroundError = typeof settings.rememberBackgroundErrorStatus === "function"
      ? settings.rememberBackgroundErrorStatus
      : (typeof settings.rememberErrorStatus === "function" ? settings.rememberErrorStatus : rememberErrorStatus);
    const getPreviousSelectedTrainId = typeof settings.getPreviousSelectedTrainId === "function"
      ? settings.getPreviousSelectedTrainId
      : detailTargetTrainId;
    const backgroundLoaders = Array.isArray(settings.backgroundLoaders)
      ? settings.backgroundLoaders.filter((loader) => typeof loader === "function")
      : [refreshMe, refreshWindowTrains, refreshPublicIncidents];
    const primaryLoaders = Array.isArray(settings.primaryLoaders)
      ? settings.primaryLoaders.filter((loader) => typeof loader === "function")
      : [];
    const renderPreservingDetail = () => {
      render({
        preserveDetail: true,
        previousSelectedTrainId: getPreviousSelectedTrainId(),
      });
    };

    render();

    try {
      for (let i = 0; i < primaryLoaders.length; i += 1) {
        await primaryLoaders[i]();
      }
      renderPreservingDetail();
    } catch (err) {
      rememberPrimaryError(err);
      if (settings.rethrowPrimaryError) {
        throw err;
      }
      renderPreservingDetail();
    }

    const backgroundPromise = Promise.allSettled(backgroundLoaders.map((loader) => loader())).then((results) => {
      results.forEach((result) => {
        if (result.status === "rejected") {
          rememberBackgroundError(result.reason);
        }
      });
      if (backgroundLoaders.length) {
        renderPreservingDetail();
      }
      return results;
    });

    if (settings.awaitBackground) {
      await backgroundPromise;
    }

    return {
      initialTrainId: "",
      backgroundPromise,
    };
  }

  async function finalizeMiniAppAuthentication(payload, options) {
    if (options && options.stripTestTicket) {
      stripTestTicketFromLocation();
    }
    await applyAuthenticatedSession(payload, { focusActiveRide: true });
  }

  async function authenticateMiniApp() {
    const testTicket = readTestTicketFromLocation();
    if (testTicket) {
      const payload = await api("/auth/test", {
        method: "POST",
        body: JSON.stringify({ ticket: testTicket }),
      }, true);
      await finalizeMiniAppAuthentication(payload, { stripTestTicket: true });
      return;
    }

    const tg = telegramWebApp();
    const initData = telegramInitData();
    if (!initData) {
      state.authenticated = false;
      return;
    }
    if (tg) {
      tg.ready();
      expandTelegramWebApp(tg, mapController);
    }
    const payload = await completeTelegramMiniAppLogin(initData);
    await finalizeMiniAppAuthentication(payload, null);
  }

	  async function applyAuthenticatedSession(payload, options) {
	    const settings = options || {};
	    persistSpacetimeSession(payload && payload.spacetime ? payload.spacetime : null);
	    state.authenticated = Boolean(payload && (payload.ok || payload.authenticated));
	    state.authState = state.authenticated ? "authenticated" : "anonymous";
	    state.miniAppPublicMapFallback = false;
	    state.authFeedback = null;
	    state.authInProgress = false;
    if (!state.authenticated) {
      return false;
    }
    let me = null;
    if (payload && payload.authenticated === true && payload.settings && typeof payload.userId !== "undefined") {
      me = payload;
    } else {
      try {
        me = await api("/me", {}, true);
      } catch (_) {
        me = {
          authenticated: true,
          userId: payload && payload.userId,
          stableUserId: payload && payload.stableUserId,
          nickname: payload && (payload.nickname || payload.firstName),
          settings: { language: payload && (payload.lang || payload.language) },
          currentRide: null,
        };
      }
    }
    if (!me) {
      me = {
        authenticated: true,
        userId: payload && payload.userId,
        stableUserId: payload && payload.stableUserId,
        nickname: payload && (payload.nickname || payload.firstName),
        settings: { language: payload && (payload.lang || payload.language) },
        currentRide: null,
      };
    }
	    state.me = me;
	    state.currentRide = me.currentRide || null;
	    state.routeCheckIn = me.routeCheckIn || null;
	    state.siteMenuOpen = false;
	    state.routeCheckInMenuOpen = false;
	    if (state.routeCheckIn && state.routeCheckIn.routeId) {
	      state.routeCheckInSelectedRouteId = state.routeCheckIn.routeId;
	    }
	    syncSelectedTrainToCurrentRide({ focusActiveRide: Boolean(settings.focusActiveRide) });
    syncMapSelectionToCurrentRide();
    state.lang = resolveSignedInLanguage(me.settings, payload && (payload.lang || payload.language));
    await loadMessages(state.lang);
    return true;
  }

	  function applyAnonymousSession(reason) {
	    clearSpacetimeSession();
	    state.authenticated = false;
	    state.authState = reason || "anonymous";
	    state.miniAppPublicMapFallback = false;
		    state.me = null;
	    state.currentRide = null;
	    state.routeCheckIn = null;
	    closeSiteMenus();
  }

  function publicSessionStatusText() {
    return state.authenticated ? t("app_status_telegram") : t("app_status_public");
  }

  async function ensurePublicSession() {
    if (state.authenticated) {
      return true;
    }
    try {
      const session = await api("/session", {}, true);
      if (session && session.authenticated === true) {
        return await applyAuthenticatedSession(session);
      }
      clearSpacetimeSession();
    } catch (_) {
      clearSpacetimeSession();
    }

    const tg = telegramWebApp();
    const initData = telegramInitData();
    if (!initData) {
      applyAnonymousSession("anonymous");
      return false;
    }
    try {
      if (tg) {
        tg.ready();
        expandTelegramWebApp(tg, mapController);
      }
      const payload = await completeTelegramMiniAppLogin(initData);
      return await applyAuthenticatedSession(payload, null);
    } catch (_) {
      applyAnonymousSession("anonymous");
      return false;
    }
  }

  function telegramLoginConfigURL() {
    return pathFor("/api/v1/auth/telegram/config");
  }

  let telegramLoginLibraryPromise = null;

  function telegramLoginPopupOrigin() {
    return "https://oauth.telegram.org";
  }

  function telegramLoginLibraryURL() {
    return `${telegramLoginPopupOrigin()}/js/telegram-login.js?3`;
  }

  function telegramLoginSDK() {
    return window && window.Telegram && window.Telegram.Login ? window.Telegram.Login : null;
  }

  function telegramLoginClientID(loginConfig) {
    const raw = String((loginConfig && loginConfig.clientId) || "").trim();
    const parsed = Number(raw);
    if (!raw || !Number.isFinite(parsed) || parsed <= 0) {
      throw new Error("invalid Telegram Login client ID");
    }
    return parsed;
  }

  function telegramLoginRequestAccess(loginConfig) {
    const allowed = ["phone", "write"];
    const raw = loginConfig && Array.isArray(loginConfig.requestAccess)
      ? loginConfig.requestAccess
      : [];
    return raw
      .map((value) => String(value || "").trim())
      .filter((value, index, all) => allowed.includes(value) && all.indexOf(value) === index);
  }

  function telegramLoginScopes(loginConfig) {
    const scopes = ["openid", "profile"];
    telegramLoginRequestAccess(loginConfig).forEach((value) => {
      if (value === "phone") {
        scopes.push("phone");
      } else if (value === "write") {
        scopes.push("telegram:bot_access");
      }
    });
    return scopes;
  }

  function telegramLoginRedirectURI(loginConfig) {
    const url = currentURL();
    const fallback = String((loginConfig && loginConfig.redirectUri) || "").trim();
    if (url && url.origin && url.pathname) {
      return url.origin + url.pathname;
    }
    return fallback;
  }

  function telegramLoginOptions(loginConfig) {
    const options = {
      client_id: telegramLoginClientID(loginConfig),
      lang: normalizeLang(state.lang).toLowerCase(),
    };
    const requestAccess = telegramLoginRequestAccess(loginConfig);
    if (requestAccess.length) {
      options.request_access = requestAccess;
    }
    const nonce = String((loginConfig && loginConfig.nonce) || "").trim();
    if (nonce) {
      options.nonce = nonce;
    }
    return options;
  }

  function telegramLoginAuthURL(loginConfig) {
    const origin = String((loginConfig && loginConfig.origin) || "").trim();
    const query = [
      ["response_type", "post_message"],
      ["client_id", String(telegramLoginClientID(loginConfig))],
      ["redirect_uri", telegramLoginRedirectURI(loginConfig)],
      ["scope", telegramLoginScopes(loginConfig).join(" ")],
      ["origin", origin],
    ];
    const nonce = String((loginConfig && loginConfig.nonce) || "").trim();
    if (nonce) {
      query.push(["nonce", nonce]);
    }
    query.push(["lang", normalizeLang(state.lang).toLowerCase()]);
    return `${telegramLoginPopupOrigin()}/auth?${query
      .map((pair) => `${encodeURIComponent(pair[0])}=${encodeURIComponent(pair[1])}`)
      .join("&")}`;
  }

  function telegramLoginPopupFeatures() {
    const width = 550;
    const height = 650;
    const screenObject = (window && window.screen) || {};
    const screenWidth = typeof screenObject.width === "number" && screenObject.width > 0 ? screenObject.width : 1280;
    const screenHeight = typeof screenObject.height === "number" && screenObject.height > 0 ? screenObject.height : 900;
    const availLeft = typeof screenObject.availLeft === "number" ? screenObject.availLeft : 0;
    const availTop = typeof screenObject.availTop === "number" ? screenObject.availTop : 0;
    const left = Math.max(0, (screenWidth - width) / 2) + availLeft;
    const top = Math.max(0, (screenHeight - height) / 2) + availTop;
    return [
      `width=${width}`,
      `height=${height}`,
      `left=${left}`,
      `top=${top}`,
      "status=0",
      "location=0",
      "menubar=0",
      "toolbar=0",
    ].join(",");
  }

  function telegramLoginResultFromMessage(raw) {
    let data = raw;
    if (typeof data === "string") {
      try {
        data = JSON.parse(data);
      } catch (_) {
        data = { result: raw };
      }
    }
    if (!data || typeof data !== "object") {
      return { error: "missing id_token" };
    }
    if (data.event && data.event !== "auth_result") {
      return null;
    }
    if (data.error) {
      return { error: data.error };
    }
    const idToken = String(data.result || data.id_token || data.idToken || "").trim();
    if (!idToken) {
      return { error: "missing id_token" };
    }
    return { id_token: idToken };
  }

  function cleanLegacyTelegramAuthResultFromURL() {
    const url = currentURL();
    if (!url || !window.history || typeof window.history.replaceState !== "function") {
      return false;
    }
    const hash = String(window.location && window.location.hash || "");
    if (!hash || !hash.includes("tgAuthResult=")) {
      return false;
    }
    const params = new URLSearchParams(hash.replace(/^#/, ""));
    params.delete("tgAuthResult");
    url.hash = params.toString() ? `#${params.toString()}` : "";
    window.history.replaceState(window.history.state || null, "", url.pathname + url.search + url.hash);
    return true;
  }

  function consumeTelegramAuthResultFromURL() {
    cleanLegacyTelegramAuthResultFromURL();
    return null;
  }

  function ensureTelegramLoginLibrary() {
    return Promise.resolve(true);
  }

  function popupBlockedAuthError() {
    const error = new Error("popup blocked");
    error.code = "popup_blocked";
    return error;
  }

  function cancelledAuthError() {
    const error = new Error("popup closed");
    error.code = "cancelled";
    return error;
  }

  function telegramLoginCallbackError(raw) {
    const message = String(raw || "Telegram Login failed");
    if (message === "popup_closed" || message === "cancelled") {
      return cancelledAuthError();
    }
    if (message === "popup_blocked") {
      return popupBlockedAuthError();
    }
    return new Error(message);
  }

  function runTelegramLoginPopup(loginConfig) {
    return new Promise((resolve, reject) => {
      let settled = false;
      let popup = null;
      let closeTimer = 0;
      const authOrigin = telegramLoginPopupOrigin();
      if (!window || typeof window.open !== "function" || typeof window.addEventListener !== "function") {
        reject(new Error("Telegram Login is not available in this browser"));
        return;
      }
      const cleanup = () => {
        if (closeTimer) {
          clearTimeout(closeTimer);
          closeTimer = 0;
        }
        if (typeof window.removeEventListener === "function") {
          window.removeEventListener("message", handleMessage);
        }
      };
      const resolveOnce = (value) => {
        if (settled) return;
        settled = true;
        cleanup();
        resolve(value);
      };
      const rejectOnce = (error) => {
        if (settled) return;
        settled = true;
        cleanup();
        reject(error);
      };
      function handleMessage(event) {
        let result = null;
        if (!event || event.origin !== authOrigin) {
          return;
        }
        if (popup && event.source && event.source !== popup) {
          return;
        }
        result = telegramLoginResultFromMessage(event.data);
        if (!result) {
          return;
        }
        if (result.error) {
          rejectOnce(telegramLoginCallbackError(result.error));
          return;
        }
        resolveOnce(result.id_token);
      }
      const checkClosed = () => {
        if (!popup || popup.closed) {
          rejectOnce(cancelledAuthError());
          return;
        }
        closeTimer = setTimeout(checkClosed, 200);
      };

      try {
        window.addEventListener("message", handleMessage);
        popup = window.open(telegramLoginAuthURL(loginConfig), "telegram_oidc_login", telegramLoginPopupFeatures());
        if (!popup) {
          rejectOnce(popupBlockedAuthError());
          return;
        }
        if (typeof popup.focus === "function") {
          popup.focus();
        }
        checkClosed();
      } catch (error) {
        rejectOnce(error);
      }
    });
  }

  function completeTelegramLogin(idToken) {
    return fetchJSON(pathFor("/api/v1/auth/telegram/complete"), {
      method: "POST",
      credentials: "same-origin",
      body: JSON.stringify({ idToken: String(idToken || "") }),
    }).then((payload) => {
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
      body: JSON.stringify({ initData: String(initData || "") }),
    }).then((payload) => {
      if (!payload || payload.authenticated !== true) {
        throw new Error("missing authenticated session");
      }
      return payload;
    });
  }

  async function completePendingTelegramAuthResult() {
    consumeTelegramAuthResultFromURL();
    return false;
  }

  function fetchTelegramLoginConfig() {
    return fetchJSON(telegramLoginConfigURL(), {
      method: "GET",
      credentials: "same-origin",
    }).then((payload) => {
      if (!payload || !payload.clientId || !payload.nonce || !payload.origin || !payload.redirectUri) {
        throw new Error("invalid Telegram login config");
      }
      return payload;
    });
  }

  function authFeedbackText(kind, message) {
    if (kind === "cancelled") {
      return t("app_login_cancelled");
    }
    if (kind === "popup_blocked") {
      return t("app_login_popup_blocked");
    }
    if (kind === "error") {
      return String(message || t("app_login_failed"));
    }
    return "";
  }

  function startAuthFeedback() {
    state.authInProgress = true;
    state.authFeedback = null;
    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
  }

  function finishAuthFeedback(kind, message) {
    state.authInProgress = false;
    state.authFeedback = {
      kind,
      message: authFeedbackText(kind, message),
    };
    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
  }

  function clearAuthFeedback() {
    state.authInProgress = false;
    state.authFeedback = null;
  }

	  async function refreshAfterAuthChange() {
	    if (state.authenticated) {
	      try {
	        await refreshCurrentRouteCheckIn();
	      } catch (_) {}
	    }
	    if (cfg.mode === "public-incidents") {
      await refreshPublicIncidents();
      if (state.publicIncidentSelectedId) {
        await refreshPublicIncidentDetail(state.publicIncidentSelectedId);
      }
      renderPublicIncidents();
      return;
    }
    if (cfg.mode === "public-network-map") {
      renderPublicNetworkMap();
      return;
    }
    if (cfg.mode === "public-map") {
      renderPublicMap();
      return;
    }
    if (cfg.mode === "public-stations") {
      renderPublicStationSearch({ preserveInputFocus: true });
      return;
    }
    if (cfg.mode === "public-dashboard") {
      renderPublicDashboard({ preserveInputFocus: true });
      return;
    }
    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
  }

  async function beginTelegramLogin() {
    startAuthFeedback();
    let loginConfig = null;
    try {
      loginConfig = await fetchTelegramLoginConfig();
      await ensureTelegramLoginLibrary();
      const loginResult = await runTelegramLoginPopup(loginConfig);
      let payload = null;
      payload = await completeTelegramLogin(loginResult);
      await applyAuthenticatedSession(payload, null);
      await refreshAfterAuthChange();
      clearAuthFeedback();
      return payload;
    } catch (error) {
      if (error && error.code === "cancelled") {
        finishAuthFeedback("cancelled");
        return null;
      }
      if (error && error.code === "popup_blocked") {
        finishAuthFeedback("popup_blocked");
        return null;
      }
      finishAuthFeedback("error", t("app_login_failed"));
      return null;
    }
  }

  async function logout() {
    try {
      await fetchJSON(pathFor("/api/v1/auth/logout"), {
        method: "POST",
        credentials: "same-origin",
	      });
	      clearAuthFeedback();
	      applyAnonymousSession("anonymous");
	      closeSiteMenus();
	      state.lang = savedBrowserLanguage();
	      await loadMessages(state.lang);
	      await refreshAfterAuthChange();
    } catch (error) {
      state.statusText = error && error.message ? error.message : t("app_status_error");
      rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
    }
  }

	  async function loadMessages(lang) {
	    state.lang = normalizeLang(lang);
	    try {
	      const payload = await fetchJSON(`${cfg.basePath}/api/v1/messages?lang=${encodeURIComponent(state.lang)}`, { method: "GET" });
	      state.messages = Object.assign({}, fallbackMessages, payload.messages || {});
	    } catch (_) {
	      state.messages = Object.assign({}, fallbackMessages);
	    }
	    if (document && document.documentElement) {
	      document.documentElement.lang = state.lang.toLowerCase();
	    }
	  }

	  async function changeSiteLanguage(lang) {
	    const nextLang = normalizeLang(lang);
	    persistBrowserLanguage(nextLang);
	    state.lang = nextLang;
	    if (state.authenticated) {
	      try {
	        const payload = await api("/settings", {
	          method: "PATCH",
	          body: JSON.stringify({ language: nextLang }),
	        });
	        state.me = state.me || {};
	        state.me.settings = Object.assign({}, state.me.settings || {}, payload || {}, { language: nextLang });
	      } catch (_) {}
	    }
	    await loadMessages(nextLang);
	    closeSiteMenus();
	    state.statusText = t("app_settings_saved");
	    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	  }

  function startExternalFeedIfNeeded() {
    if (!cfg.externalTrainMapEnabled || externalFeedClient || !window.TrainExternalFeed || typeof window.TrainExternalFeed.createExternalTrainMapClient !== "function") {
      return;
    }
    if (
      cfg.mode !== "public-dashboard" &&
      cfg.mode !== "public-stations" &&
      cfg.mode !== "public-train" &&
      cfg.mode !== "public-network-map" &&
      cfg.mode !== "public-map" &&
      cfg.mode !== "mini-app"
    ) {
      return;
    }
    externalFeedClient = window.TrainExternalFeed.createExternalTrainMapClient({
      enabled: cfg.externalTrainMapEnabled,
      baseURL: cfg.externalTrainMapBaseURL,
      wsURL: cfg.externalTrainMapWsURL,
      graphURL: cfg.externalTrainGraphURL || "",
      onState(nextState) {
        if (sameExternalFeedMaterialState(state.externalFeed, nextState || null)) {
          return;
        }
        state.externalFeed = nextState || state.externalFeed;
        scheduleExternalFeedRender();
      },
    });
    externalFeedClient.start().catch((err) => {
      const message = err && err.message ? err.message : String(err);
      state.externalFeed = Object.assign({}, state.externalFeed, {
        connectionState: "offline",
        connectionError: message,
        error: message,
        graphState: state.externalFeed && state.externalFeed.graphState ? state.externalFeed.graphState : "idle",
      });
      scheduleExternalFeedRender();
    });
  }

  async function restartExternalFeedIfNeeded() {
    startExternalFeedIfNeeded();
    if (!externalFeedClient || typeof externalFeedClient.restart !== "function") {
      return false;
    }
    await externalFeedClient.restart();
    return true;
  }

  function scheduleExternalFeedRender() {
    if (externalFeedRenderTimer) {
      return;
    }
    externalFeedRenderTimer = window.setTimeout(() => {
      externalFeedRenderTimer = null;
      if (cfg.mode === "public-map") {
        if (!patchPublicMapMainPanel({ mapOnly: true })) {
          renderPublicMap();
        }
        return;
      }
      if (cfg.mode === "public-network-map") {
        if (!patchPublicNetworkMapPanel({ mapOnly: true })) {
          renderPublicNetworkMap();
        }
        return;
      }
      if (cfg.mode === "mini-app" && state.tab === "map") {
        const mainPanel = document.getElementById("mini-app-main-panel");
        if (mainPanel && patchMiniMapPanel(mainPanel)) {
          return;
        }
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
      }
    }, 250);
  }

  async function refreshPublicDashboard() {
    const payload = await publicApi("/public/dashboard?limit=0");
    return applyPublicDashboardPayload(payload);
  }

  async function refreshPublicDashboardAll() {
    const results = await Promise.all([
      refreshPublicDashboard(),
      refreshPublicServiceDayTrains(),
    ]);
    return results.some(Boolean);
  }

  async function refreshPublicServiceDayTrains() {
    const payload = await publicApi("/public/service-day-trains");
    return applyPublicServiceDayTrainsPayload(payload);
  }

  function applyPublicDashboardPayload(payload) {
    const previousSchedule = state.scheduleMeta;
    if (payload && payload.schedule) {
      state.scheduleMeta = payload.schedule;
    }
    const previousAllItems = state.publicDashboardAll;
    const previousVisibleItems = state.publicDashboard;
    const nextItems = Array.isArray(payload && payload.trains) ? payload.trains : [];
    const nextVisibleItems = nextItems.slice(0, PUBLIC_DASHBOARD_VISIBLE_LIMIT);
    const allChanged = !samePublicDashboardPayload(previousAllItems, nextItems);
    const visibleChanged = !samePublicDashboardPayload(previousVisibleItems, nextVisibleItems);
    const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
    if (allChanged) {
      state.publicDashboardAll = nextItems;
    }
    if (visibleChanged) {
      state.publicDashboard = nextVisibleItems;
    }
    state.statusText = publicSessionStatusText();
    return allChanged || visibleChanged || scheduleChanged;
  }

  function applyPublicServiceDayTrainsPayload(payload) {
    const previousSchedule = state.scheduleMeta;
    if (payload && payload.schedule) {
      state.scheduleMeta = payload.schedule;
    }
    const previousItems = state.publicServiceDayTrains;
    const nextItems = Array.isArray(payload && payload.trains) ? payload.trains : [];
    const itemsChanged = !samePublicDashboardPayload(previousItems, nextItems);
    const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
    if (itemsChanged) {
      state.publicServiceDayTrains = nextItems;
    }
    state.statusText = publicSessionStatusText();
    return itemsChanged || scheduleChanged;
  }

  function applyPublicFilter() {
    state.publicFilter = state.publicFilterDraft;
    renderPublicDashboard({ preserveInputFocus: true });
  }

  async function refreshPublicTrain() {
    const payload = await publicApi(`/public/trains/${encodeURIComponent(cfg.trainId)}`);
    state.publicTrain = payload;
    state.statusText = publicSessionStatusText();
  }

  function liveOnlyNetworkMapData() {
    return {
      liveOnly: true,
    };
  }

  async function loadPublicNetworkMapData() {
    const payload = await publicApi("/public/map");
    return Object.assign({ liveOnly: false }, payload || {});
  }

  async function refreshPublicNetworkMap() {
    const previousSchedule = state.scheduleMeta;
    const previousMapData = state.networkMapData;
    const nextMapData = await loadPublicNetworkMapData();
    const dataChanged = !sameNetworkMapPayload(previousMapData, nextMapData);
    const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
    if (dataChanged) {
      state.networkMapData = nextMapData;
    }
    state.statusText = publicSessionStatusText();
    return dataChanged || scheduleChanged;
  }

  async function searchPublicStations(query) {
    state.publicStationQuery = query || "";
    state.publicStationSearchLoading = true;
    renderPublicStationSearch({ preserveInputFocus: true });
    try {
      const payload = await publicApi(`/public/stations?q=${encodeURIComponent(state.publicStationQuery)}`);
      state.publicStationMatches = Array.isArray(payload.stations) ? payload.stations : [];
    } finally {
      state.publicStationSearchLoading = false;
      renderPublicStationSearch({ preserveInputFocus: true });
    }
  }

  async function refreshPublicStationDepartures(stationId) {
    const previousSchedule = state.scheduleMeta;
    const previousDepartures = state.publicStationDepartures;
    state.publicStationDeparturesLoading = true;
    renderPublicStationSearch({ preserveInputFocus: true });
    try {
      const payload = await publicApi(`/public/stations/${encodeURIComponent(stationId)}/departures`);
      const nextDepartures = payload || null;
      const dataChanged = !samePublicStationDeparturesPayload(previousDepartures, nextDepartures);
      const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
      if (dataChanged) {
        state.publicStationDepartures = nextDepartures;
        state.publicStationSelected = nextDepartures && nextDepartures.station ? nextDepartures.station : null;
      }
      state.statusText = publicSessionStatusText();
      return dataChanged || scheduleChanged;
    } finally {
      state.publicStationDeparturesLoading = false;
    }
  }

  function incidentActivityTimeValue(item) {
    const raw = item && (item.lastActivityAt || item.lastReportAt);
    const time = raw ? new Date(raw).getTime() : 0;
    return Number.isFinite(time) ? time : 0;
  }

  function sortIncidentSummaries(items) {
    return (Array.isArray(items) ? items : []).slice().sort((left, right) => {
      const delta = incidentActivityTimeValue(right) - incidentActivityTimeValue(left);
      if (delta !== 0) {
        return delta;
      }
      return String(right && right.id || "").localeCompare(String(left && left.id || ""));
    });
  }

  function syncIncidentSummary(summary) {
    if (!summary || !summary.id) {
      return;
    }
    const items = Array.isArray(state.publicIncidents) ? state.publicIncidents.slice() : [];
    const index = items.findIndex((item) => item.id === summary.id);
    if (index >= 0) {
      items[index] = Object.assign({}, items[index], summary);
    } else {
      items.push(summary);
    }
    state.publicIncidents = sortIncidentSummaries(items);
  }

  function defaultNetworkMapSightings(mapData) {
    if (mapData && mapData.liveOnly) {
      return [];
    }
    return Array.isArray(mapData && mapData.recentSightings) ? mapData.recentSightings : [];
  }

  function sameDayNetworkMapSightings(mapData) {
    if (mapData && mapData.liveOnly) {
      return [];
    }
    if (Array.isArray(mapData && mapData.sameDaySightings)) {
      return mapData.sameDaySightings;
    }
    return defaultNetworkMapSightings(mapData);
  }

  function activeNetworkMapSightings(mapData) {
    const showAll = cfg.mode === "public-network-map"
      ? state.publicNetworkMapShowAllSightings
      : state.miniNetworkMapShowAllSightings;
    return showAll ? sameDayNetworkMapSightings(mapData) : defaultNetworkMapSightings(mapData);
  }

  function incidentCommentDraft(incidentId) {
    return String(state.publicIncidentCommentDrafts[incidentId] || "");
  }

  function setIncidentCommentDraft(incidentId, value) {
    if (!incidentId) return;
    state.publicIncidentCommentDrafts[incidentId] = String(value || "");
  }

  function clearIncidentCommentDraft(incidentId) {
    if (!incidentId) return;
    delete state.publicIncidentCommentDrafts[incidentId];
  }

  function syncIncidentLayoutState() {
    state.publicIncidentMobileLayout = isIncidentMobileLayout();
    return state.publicIncidentMobileLayout;
  }

  function isIncidentDetailVisible() {
    return !state.publicIncidentMobileLayout || state.publicIncidentDetailOpen;
  }

  function pushIncidentOverlayHistory() {
    if (!state.publicIncidentMobileLayout || state.publicIncidentHistoryOpen) {
      return;
    }
    if (!window.history || typeof window.history.pushState !== "function") {
      return;
    }
    try {
      window.history.pushState(incidentOverlayHistoryState(), "");
      state.publicIncidentHistoryOpen = true;
    } catch (_) {
      state.publicIncidentHistoryOpen = false;
    }
  }

  function closeIncidentDetailOverlay(options) {
    const opts = options || {};
    if (!state.publicIncidentMobileLayout && !opts.force) {
      return false;
    }
    if (!state.publicIncidentDetailOpen && !state.publicIncidentHistoryOpen) {
      return false;
    }
    state.publicIncidentDetailOpen = false;
    renderPublicIncidents();
    setPageScrollY(state.publicIncidentListScrollY);
    if (opts.skipHistory) {
      state.publicIncidentHistoryOpen = false;
      return true;
    }
    if (state.publicIncidentHistoryOpen && window.history && typeof window.history.back === "function") {
      state.publicIncidentHistoryOpen = false;
      state.publicIncidentHistoryNavigating = true;
      try {
        window.history.back();
      } catch (_) {
        state.publicIncidentHistoryNavigating = false;
      }
    }
    return true;
  }

  function handleIncidentViewportChange() {
    if (cfg.mode !== "public-incidents") {
      return;
    }
    const wasMobile = Boolean(state.publicIncidentMobileLayout);
    const isMobile = syncIncidentLayoutState();
    if (wasMobile === isMobile) {
      return;
    }
    if (!isMobile && state.publicIncidents.length && !state.publicIncidentSelectedId) {
      state.publicIncidentSelectedId = state.publicIncidents[0].id;
    }
    renderPublicIncidents();
  }

  function handleIncidentPopState(event) {
    if (cfg.mode !== "public-incidents") {
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
        renderPublicIncidents();
      }
      return;
    }
    if (state.publicIncidentDetailOpen || state.publicIncidentHistoryOpen) {
      closeIncidentDetailOverlay({ skipHistory: true, force: true });
    }
  }

  function beginIncidentDetailLoading(incidentId, options) {
    const nextIncidentId = String(incidentId || "").trim();
    if (!nextIncidentId) {
      return false;
    }
    syncIncidentLayoutState();
    if (state.publicIncidentMobileLayout && !state.publicIncidentDetailOpen) {
      state.publicIncidentListScrollY = currentPageScrollY();
      state.publicIncidentDetailOpen = true;
      if (!options || options.pushHistory !== false) {
        pushIncidentOverlayHistory();
      }
    }
    state.publicIncidentSelectedId = nextIncidentId;
    state.publicIncidentDetailLoading = true;
    state.publicIncidentDetailLoadingId = nextIncidentId;
    return true;
  }

  async function openIncidentDetailView(incidentId) {
    if (!beginIncidentDetailLoading(incidentId)) {
      return false;
    }
    const nextIncidentId = String(incidentId || "").trim();
    const changed = await refreshPublicIncidentDetail(nextIncidentId);
    return changed;
  }

  async function refreshPublicIncidents() {
    const firstLoad = !state.publicIncidentsLoaded;
    state.publicIncidentsLoading = true;
    try {
      const payload = await publicApi("/public/incidents?limit=60");
      const nextIncidents = Array.isArray(payload.incidents) ? sortIncidentSummaries(payload.incidents) : [];
      const previousFirstId = state.publicIncidents[0] ? state.publicIncidents[0].id : "";
      const nextFirstId = nextIncidents[0] ? nextIncidents[0].id : "";
      const requestedIncidentId = selectedIncidentIdFromURL();
      const changed = !sameMaterialValue(state.publicIncidents, nextIncidents)
        || previousFirstId !== nextFirstId
        || firstLoad;
      state.publicIncidents = nextIncidents;
      state.publicIncidentsLoaded = true;
      syncIncidentLayoutState();
      if (requestedIncidentId && nextIncidents.some((item) => item.id === requestedIncidentId)) {
        state.publicIncidentSelectedId = requestedIncidentId;
        if (state.publicIncidentMobileLayout) {
          state.publicIncidentDetailOpen = true;
        }
      }
      if (state.publicIncidentSelectedId && !nextIncidents.some((item) => item.id === state.publicIncidentSelectedId)) {
        state.publicIncidentSelectedId = "";
        state.publicIncidentDetail = null;
        state.publicIncidentDetailOpen = false;
      }
      if (!state.publicIncidentSelectedId && nextFirstId && !state.publicIncidentMobileLayout) {
        await refreshPublicIncidentDetail(nextFirstId);
      }
      if (!nextIncidents.length) {
        syncIncidentURL("");
      }
      state.statusText = state.authenticated ? t("app_status_telegram") : t("app_status_public");
      return changed;
    } finally {
      state.publicIncidentsLoading = false;
    }
  }

  async function refreshPublicIncidentDetail(incidentId) {
    const nextSelectedId = incidentId ? String(incidentId) : "";
    if (!nextSelectedId) {
      const changed = state.publicIncidentSelectedId !== "" || Boolean(state.publicIncidentDetail);
      state.publicIncidentSelectedId = "";
      state.publicIncidentDetail = null;
      syncIncidentURL("");
      return changed;
    }
    const previousSelectedId = state.publicIncidentSelectedId;
    state.publicIncidentSelectedId = nextSelectedId;
    state.publicIncidentDetailLoading = true;
    state.publicIncidentDetailLoadingId = nextSelectedId;
    try {
      const payload = await publicApi(`/public/incidents/${encodeURIComponent(nextSelectedId)}`);
      const changed = previousSelectedId !== nextSelectedId || !samePayloadIgnoringSchedule(state.publicIncidentDetail, payload);
      state.publicIncidentSelectedId = nextSelectedId;
      state.publicIncidentDetail = payload || null;
      syncIncidentURL(nextSelectedId);
      return changed;
    } finally {
      if (state.publicIncidentDetailLoadingId === nextSelectedId) {
        state.publicIncidentDetailLoading = false;
        state.publicIncidentDetailLoadingId = "";
      }
    }
  }

  async function submitIncidentVote(incidentId, value) {
    const payload = await api(`/incidents/${encodeURIComponent(incidentId)}/votes`, {
      method: "POST",
      body: JSON.stringify({ value }),
    }, false);
    const selectedDetail = state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId;
    if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId) {
      state.publicIncidentDetail.summary.votes = payload;
    }
    const item = state.publicIncidents.find((entry) => entry.id === incidentId);
    if (item) {
      item.votes = payload;
    }
    if (value === "ONGOING") {
      const activityAt = new Date().toISOString();
      if (item) {
        item.lastActivityAt = activityAt;
        item.lastActivityName = t("app_public_incidents_vote_ongoing");
      }
      if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId) {
        state.publicIncidentDetail.summary.lastActivityAt = activityAt;
        state.publicIncidentDetail.summary.lastActivityName = t("app_public_incidents_vote_ongoing");
      }
      state.publicIncidents = sortIncidentSummaries(state.publicIncidents);
    }
    if (selectedDetail) {
      await refreshPublicIncidentDetail(incidentId);
      if (state.publicIncidentDetail && state.publicIncidentDetail.summary) {
        syncIncidentSummary(state.publicIncidentDetail.summary);
      }
    }
    showToast(t("app_public_incidents_vote_saved"));
    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
  }

  async function submitIncidentComment(incidentId, body) {
    const comment = await api(`/incidents/${encodeURIComponent(incidentId)}/comments`, {
      method: "POST",
      body: JSON.stringify({ body }),
    }, false);
    clearIncidentCommentDraft(incidentId);
    if (state.publicIncidentDetail && state.publicIncidentDetail.summary && state.publicIncidentDetail.summary.id === incidentId) {
      const activityAt = comment.createdAt || new Date().toISOString();
      state.publicIncidentDetail.summary.commentCount = Number(state.publicIncidentDetail.summary.commentCount || 0) + 1;
      state.publicIncidentDetail.summary.lastActivityAt = activityAt;
      state.publicIncidentDetail.summary.lastActivityName = t("app_public_incidents_comment_label");
      state.publicIncidentDetail.summary.lastActivityActor = comment.nickname || "";
      state.publicIncidentDetail.comments = [comment, ...(state.publicIncidentDetail.comments || [])];
      state.publicIncidentDetail.events = [{
        id: comment.id,
        kind: "comment",
        name: t("app_public_incidents_comment_label"),
        detail: comment.body || "",
        nickname: comment.nickname || "",
        createdAt: activityAt,
      }].concat(Array.isArray(state.publicIncidentDetail.events) ? state.publicIncidentDetail.events : []);
      state.publicIncidentDetail.events.sort((left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime());
      syncIncidentSummary(state.publicIncidentDetail.summary);
    }
    const item = state.publicIncidents.find((entry) => entry.id === incidentId);
    if (item) {
      item.commentCount = Number(item.commentCount || 0) + 1;
    }
    if (item) {
      item.lastActivityAt = comment.createdAt || new Date().toISOString();
      item.lastActivityName = t("app_public_incidents_comment_label");
      item.lastActivityActor = comment.nickname || "";
    }
    state.publicIncidents = sortIncidentSummaries(state.publicIncidents);
    showToast(t("app_public_incidents_comment_saved"));
    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
  }

  async function refreshWindowTrains() {
    const payload = await api(`/windows/${encodeURIComponent(state.window)}`);
    state.windowTrains = Array.isArray(payload.trains) ? payload.trains : [];
    state.statusText = t("app_status_ready");
  }

  function applyCurrentRidePayload(payload, options = {}) {
    const previousRideTrainId = currentRideTrainId();
    state.currentRide = payload && payload.currentRide ? payload.currentRide : null;
    if (state.me) {
      state.me.currentRide = state.currentRide;
    }
    syncSelectedTrainToCurrentRide({
      focusActiveRide: Boolean(options.focusActiveRide),
      previousRideTrainId,
    });
    syncMapSelectionToCurrentRide();
  }

	  async function refreshMe() {
	    if (!state.authenticated) {
	      return;
	    }
	    const me = await api("/me");
	    state.me = me;
	    applyCurrentRidePayload({ currentRide: me.currentRide || null });
	    applyRouteCheckInPayload({ routeCheckIn: me.routeCheckIn || null });
	  }

	  async function refreshCurrentRide(options = {}) {
	    try {
	      const payload = await api("/checkins/current");
      applyCurrentRidePayload(payload, options);
      return;
    } catch (err) {
      if (!err || err.status !== 410) {
        throw err;
      }
      const me = await api("/me");
      state.me = me;
	      applyCurrentRidePayload({ currentRide: me.currentRide || null }, options);
	    }
	  }

	  function applyRouteCheckInPayload(payload) {
	    if (payload) {
	      if (Number.isFinite(Number(payload.defaultDurationMinutes))) {
	        state.routeCheckInDefaultDurationMinutes = Number(payload.defaultDurationMinutes);
	      }
	      if (Number.isFinite(Number(payload.minDurationMinutes))) {
	        state.routeCheckInMinDurationMinutes = Number(payload.minDurationMinutes);
	      }
	      if (Number.isFinite(Number(payload.maxDurationMinutes))) {
	        state.routeCheckInMaxDurationMinutes = Number(payload.maxDurationMinutes);
	      }
	    }
	    state.routeCheckIn = payload && payload.routeCheckIn ? payload.routeCheckIn : null;
	    if (state.me) {
	      state.me.routeCheckIn = state.routeCheckIn;
	    }
	    if (state.routeCheckIn && state.routeCheckIn.routeId) {
	      state.routeCheckInSelectedRouteId = state.routeCheckIn.routeId;
	    }
	  }

	  async function refreshRouteCheckInRoutes() {
	    state.routeCheckInLoading = true;
	    try {
	      const payload = await publicApi("/public/route-checkin-routes");
	      state.routeCheckInRoutes = Array.isArray(payload.routes) ? payload.routes : [];
	      if (Number.isFinite(Number(payload.defaultDurationMinutes))) {
	        state.routeCheckInDefaultDurationMinutes = Number(payload.defaultDurationMinutes);
	      }
	      if (Number.isFinite(Number(payload.minDurationMinutes))) {
	        state.routeCheckInMinDurationMinutes = Number(payload.minDurationMinutes);
	      }
	      if (Number.isFinite(Number(payload.maxDurationMinutes))) {
	        state.routeCheckInMaxDurationMinutes = Number(payload.maxDurationMinutes);
	      }
	      if (!state.routeCheckInSelectedRouteId && state.routeCheckInRoutes[0]) {
	        state.routeCheckInSelectedRouteId = state.routeCheckInRoutes[0].id;
	      }
	      return true;
	    } finally {
	      state.routeCheckInLoading = false;
	    }
	  }

	  async function ensureRouteCheckInRoutes() {
	    if (state.routeCheckInRoutes.length || state.routeCheckInLoading) {
	      return;
	    }
	    await refreshRouteCheckInRoutes();
	  }

	  function closeSiteMenus() {
	    const changed = Boolean(state.siteMenuOpen || state.routeCheckInMenuOpen);
	    state.siteMenuOpen = false;
	    state.routeCheckInMenuOpen = false;
	    return changed;
	  }

	  async function refreshCurrentRouteCheckIn() {
	    if (!state.authenticated) {
	      applyRouteCheckInPayload(null);
	      return;
	    }
	    const payload = await api("/route-checkins/current");
	    applyRouteCheckInPayload(payload);
	  }

	  async function startRouteCheckIn(routeId, durationMinutes) {
	    const selectedRouteId = String(routeId || state.routeCheckInSelectedRouteId || "").trim();
	    if (!selectedRouteId) {
	      throw new Error(t("app_route_checkin_choose_route"));
	    }
	    const payload = await api("/route-checkins/current", {
	      method: "POST",
	      body: JSON.stringify({
	        routeId: selectedRouteId,
	        durationMinutes: Number(durationMinutes) || state.routeCheckInDefaultDurationMinutes,
	      }),
	    });
	    applyRouteCheckInPayload(payload);
	    closeSiteMenus();
	    state.statusText = t("app_route_checkin_started");
	    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	    return t("app_route_checkin_started");
	  }

	  async function checkoutRouteCheckIn() {
	    await api("/route-checkins/current", { method: "DELETE" });
	    applyRouteCheckInPayload(null);
	    closeSiteMenus();
	    state.statusText = t("app_route_checkin_stopped");
	    rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	    return t("app_route_checkin_stopped");
	  }

	  function rideTrainDetailPayload(payload) {
    if (!payload) {
      return null;
    }
    if (payload.trainCard && payload.trainCard.train) {
      return payload;
    }
    if (!payload.train) {
      return null;
    }
    return {
      trainCard: {
        train: payload.train,
        status: payload.status || null,
        riders: Number.isFinite(Number(payload.riders)) ? Number(payload.riders) : 0,
      },
      timeline: Array.isArray(payload.timeline) ? payload.timeline : [],
      stationSightings: Array.isArray(payload.stationSightings) ? payload.stationSightings : [],
    };
  }

  function currentRideNeedsTrainHydration(ride) {
    if (!ride || !ride.checkIn || !ride.checkIn.trainInstanceId) {
      return false;
    }
    const expectedTrainId = normalizeTrainId(ride.checkIn.trainInstanceId);
    const detail = rideTrainDetailPayload(ride.train);
    const actualTrainId = normalizeTrainId(detail && detail.trainCard && detail.trainCard.train ? detail.trainCard.train.id : "");
    const riders = detail && detail.trainCard ? Number(detail.trainCard.riders) : NaN;
    if (!detail || !expectedTrainId || !actualTrainId) {
      return true;
    }
    if (expectedTrainId !== actualTrainId) {
      return true;
    }
    return !Number.isFinite(riders) || riders < 1;
  }

  async function hydrateCurrentRideTrainFromPublic(trainId) {
    const normalizedTrainId = normalizeTrainId(trainId);
    if (!normalizedTrainId || !state.currentRide) {
      return false;
    }
    if (normalizeTrainId(currentRideTrainId()) !== normalizedTrainId) {
      return false;
    }
    if (!currentRideNeedsTrainHydration(state.currentRide)) {
      return false;
    }
    try {
      state.currentRide.train = rideTrainDetailPayload(
        await fetchCurrentRidePublicTrainStops(normalizedTrainId)
      );
      if (state.me) {
        state.me.currentRide = state.currentRide;
      }
      syncSelectedTrainToCurrentRide({
        focusActiveRide: true,
        previousRideTrainId: normalizedTrainId,
      });
      return Boolean(state.currentRide.train);
    } catch (_) {
      return false;
    }
  }

  async function settleCurrentRideAfterCheckIn(trainId) {
    const expectedTrainId = normalizeTrainId(trainId);
    if (!expectedTrainId) {
      return "";
    }
    for (let attempt = 0; attempt < CHECKIN_RIDE_SETTLE_RETRIES; attempt += 1) {
      if (currentRideTrainId() === expectedTrainId && state.currentRide && !currentRideNeedsTrainHydration(state.currentRide)) {
        return expectedTrainId;
      }
      if (currentRideTrainId() === expectedTrainId && await hydrateCurrentRideTrainFromPublic(expectedTrainId)) {
        return expectedTrainId;
      }
      try {
        await refreshCurrentRide({ focusActiveRide: true });
      } catch (_) {}
      if (attempt + 1 < CHECKIN_RIDE_SETTLE_RETRIES) {
        await waitMs(CHECKIN_RIDE_SETTLE_DELAY_MS);
      }
    }
    if (currentRideTrainId() === expectedTrainId && await hydrateCurrentRideTrainFromPublic(expectedTrainId)) {
      return expectedTrainId;
    }
    return currentRideTrainId();
  }

  async function refreshFavorites() {
    const payload = await api("/favorites");
    state.favorites = Array.isArray(payload.favorites) ? payload.favorites : [];
  }

  async function fetchStationMatches(query) {
    const payload = await api(`/stations?q=${encodeURIComponent(query || "")}`);
    state.stations = Array.isArray(payload.stations) ? payload.stations : [];
    state.selectedStation = null;
    state.stationDepartures = [];
    state.selectedCheckInTrainId = "";
    state.checkInDropdownOpen = false;
    state.stationRecentSightings = [];
    state.stationSightingDestinations = [];
    state.stationSightingDestinationId = "";
    state.selectedSightingTrainId = "";
    state.expandedStationContextTrainId = "";
    renderMiniApp();
  }

  async function fetchStationDepartures(stationId) {
    const previousStationId = state.selectedStation && state.selectedStation.id ? state.selectedStation.id : "";
    const payload = await api(`/stations/${encodeURIComponent(stationId)}/departures`);
    state.selectedStation = payload && payload.station ? payload.station : null;
    state.stationDepartures = Array.isArray(payload.trains) ? payload.trains : [];
    state.checkInDropdownOpen = false;
    state.stationRecentSightings = Array.isArray(payload.recentSightings) ? payload.recentSightings : [];
    state.stationSightingDestinations = [];
    const sameStation = Boolean(state.selectedStation && state.selectedStation.id === previousStationId);
    state.stationSightingDestinationId = "";
    state.selectedSightingTrainId = "";
    state.selectedCheckInTrainId = sameStation ? state.selectedCheckInTrainId : "";
    state.expandedStationContextTrainId = "";
    if (state.authenticated && state.selectedStation && state.selectedStation.id) {
      try {
        await fetchStationSightingDestinations(state.selectedStation.id);
      } catch (_) {}
    }
    renderMiniApp();
  }

  async function fetchStationSightingDestinations(stationId) {
    const payload = await api(`/stations/${encodeURIComponent(stationId)}/sighting-destinations`);
    state.stationSightingDestinations = Array.isArray(payload.stations) ? payload.stations : [];
  }

  async function fetchOriginMatches(query) {
    const payload = await api(`/stations?q=${encodeURIComponent(query || "")}`);
    state.originResults = Array.isArray(payload.stations) ? payload.stations : [];
    renderMiniApp();
  }

  async function fetchDestinationMatches(query) {
    if (!state.chosenOrigin) {
      throw new Error(t("app_choose_origin"));
    }
    const payload = await api(`/routes/destinations?originStationId=${encodeURIComponent(state.chosenOrigin.id)}&q=${encodeURIComponent(query || "")}`);
    state.destinationResults = Array.isArray(payload.stations) ? payload.stations : [];
    renderMiniApp();
  }

  async function fetchRouteResults() {
    if (!state.chosenOrigin) throw new Error(t("app_choose_origin"));
    if (!state.chosenDestination) throw new Error(t("app_choose_destination"));
    const payload = await api(`/routes/trains?originStationId=${encodeURIComponent(state.chosenOrigin.id)}&destinationStationId=${encodeURIComponent(state.chosenDestination.id)}`);
    state.routeResults = Array.isArray(payload.trains) ? payload.trains : [];
    renderMiniApp();
  }

  async function openStatus(trainId) {
    state.selectedTrain = await api(`/trains/${encodeURIComponent(trainId)}/status`);
    setPinnedDetailTrain(trainId, { fromUser: true, reason: "open-status" });
    renderMiniApp();
    return t("app_status_loaded");
  }

  async function refreshMapData(trainId, allowAnonymous, previousTrainIdOverride, options) {
    const nextOptions = options || {};
    const previousTrainId = previousTrainIdOverride || state.mapTrainId || "";
    const notifyLoadStateChange = () => {
      if (typeof nextOptions.onLoadStateChange === "function") {
        nextOptions.onLoadStateChange({
          mode: "train",
          mapLoadState: state.mapLoadState,
        });
      }
    };
    if (!trainId) {
      const changed = Boolean(state.mapData || state.mapTrainId || state.mapTrainDetail);
      state.mapData = null;
      state.mapTrainId = "";
      state.mapTrainDetail = null;
      state.expandedStopContextKey = "";
      clearMapLoadState("train");
      return changed;
    }
    const previousSchedule = state.scheduleMeta;
    const previousMapData = state.mapData;
    const previousMapDetail = state.mapTrainDetail;
    state.mapTrainId = trainId;
    if (previousTrainId && previousTrainId !== trainId) {
      state.expandedStopContextKey = "";
    }
    const path = allowAnonymous
      ? `/public/trains/${encodeURIComponent(trainId)}/stops`
      : `/trains/${encodeURIComponent(trainId)}/stops`;
    const loadMapData = allowAnonymous
      ? publicApi(path)
      : api(path);
    const loadMapDetail = (allowAnonymous
      ? publicApi(`/public/trains/${encodeURIComponent(trainId)}`)
      : api(`/trains/${encodeURIComponent(trainId)}/status`))
      .then((payload) => ({ ok: true, payload: payload || null }))
      .catch(() => ({ ok: false, payload: null }));
    const showMiniMapLoad = beginMiniMapLoad("train");
    if (showMiniMapLoad) {
      notifyLoadStateChange();
      advanceMiniMapLoad("train");
      notifyLoadStateChange();
    }

    try {
      const payload = await loadMapData;
      const nextMapData = payload || null;
      const mapChanged = !sameTrainStopsPayload(previousMapData, nextMapData);
      if (mapChanged) {
        state.mapData = nextMapData;
      }
      clearMapLoadState("train");
      if (showMiniMapLoad) {
        notifyLoadStateChange();
      }
      state.statusText = cfg.mode === "mini-app" ? t("app_status_ready") : publicSessionStatusText();
      if (typeof nextOptions.onPrimaryData === "function") {
        nextOptions.onPrimaryData({
          trainId,
          mapChanged,
          mapData: nextMapData,
        });
      }
      const mapDetailResult = await loadMapDetail;
      const nextMapDetail = mapDetailResult.ok ? mapDetailResult.payload : null;
      const detailChanged = !samePayloadIgnoringSchedule(previousMapDetail, nextMapDetail);
      const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
      if (detailChanged) {
        state.mapTrainDetail = nextMapDetail;
      }
      return mapChanged || detailChanged || scheduleChanged;
    } catch (err) {
      clearMapLoadState("train");
      if (showMiniMapLoad) {
        notifyLoadStateChange();
      }
      throw err;
    }
  }

  async function refreshNetworkMapData(allowAnonymous, options) {
    const nextOptions = options || {};
    const previousSchedule = state.scheduleMeta;
    const previousMapData = state.networkMapData;
    const notifyLoadStateChange = () => {
      if (typeof nextOptions.onLoadStateChange === "function") {
        nextOptions.onLoadStateChange({
          mode: "network",
          mapLoadState: state.mapLoadState,
        });
      }
    };
    const showMiniMapLoad = beginMiniMapLoad("network");
    if (showMiniMapLoad) {
      notifyLoadStateChange();
      advanceMiniMapLoad("network");
      notifyLoadStateChange();
    }
    try {
      const nextMapData = await loadPublicNetworkMapData();
      const dataChanged = !sameNetworkMapPayload(previousMapData, nextMapData);
      const scheduleChanged = !sameMaterialValue(previousSchedule, state.scheduleMeta);
      if (dataChanged) {
        state.networkMapData = nextMapData;
        state.expandedStopContextKey = "";
      }
      clearMapLoadState("network");
      if (showMiniMapLoad) {
        notifyLoadStateChange();
      }
      state.statusText = cfg.mode === "mini-app" ? t("app_status_ready") : publicSessionStatusText();
      if (typeof nextOptions.onPrimaryData === "function") {
        nextOptions.onPrimaryData({
          dataChanged,
          mapData: nextMapData,
        });
      }
      return dataChanged || scheduleChanged;
    } catch (err) {
      clearMapLoadState("network");
      if (showMiniMapLoad) {
        notifyLoadStateChange();
      }
      throw err;
    }
  }

  function selectedTrainId() {
    if (state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train) {
      return state.selectedTrain.trainCard.train.id || "";
    }
    return "";
  }

  function normalizeTrainId(trainId) {
    return String(trainId || "").trim();
  }

  function hasTrainMapPayload(mapData) {
    return Boolean(mapData && mapData.train);
  }

  function hasNetworkMapPayload(mapData) {
    return mapData != null;
  }

  function mapLoadLabel(mode) {
    return mode === "train" ? t("app_map_loading_train") : t("app_map_loading_network");
  }

  function beginMiniMapLoad(mode) {
    if (cfg.mode !== "mini-app") {
      return false;
    }
    const normalizedMode = mode === "train" ? "train" : "network";
    const hasVisibleData = normalizedMode === "train"
      ? hasTrainMapPayload(state.mapData)
      : hasNetworkMapPayload(state.networkMapData);
    if (hasVisibleData) {
      clearMapLoadState(normalizedMode);
      return false;
    }
    state.mapLoadState = {
      active: true,
      mode: normalizedMode,
      progress: 24,
      label: mapLoadLabel(normalizedMode),
    };
    return true;
  }

  function advanceMiniMapLoad(mode) {
    const normalizedMode = mode === "train" ? "train" : "network";
    if (!state.mapLoadState.active || state.mapLoadState.mode !== normalizedMode) {
      return false;
    }
    state.mapLoadState = {
      active: true,
      mode: normalizedMode,
      progress: 68,
      label: mapLoadLabel(normalizedMode),
    };
    return true;
  }

  function clearMapLoadState(mode) {
    const normalizedMode = mode ? (mode === "train" ? "train" : "network") : "";
    if (normalizedMode && state.mapLoadState.mode && state.mapLoadState.mode !== normalizedMode) {
      return false;
    }
    const hadState = Boolean(state.mapLoadState.active || state.mapLoadState.mode);
    state.mapLoadState = emptyMapLoadState();
    return hadState;
  }

  function resolveTrainIdFromPayload(payload) {
    if (!payload) {
      return "";
    }
    if (payload.trainCard && payload.trainCard.train && payload.trainCard.train.id) {
      return normalizeTrainId(payload.trainCard.train.id);
    }
    if (payload.train && payload.train.trainCard && payload.train.trainCard.train && payload.train.trainCard.train.id) {
      return normalizeTrainId(payload.train.trainCard.train.id);
    }
    if (payload.train && payload.train.id) {
      return normalizeTrainId(payload.train.id);
    }
    return "";
  }

  function detailTargetTrainId() {
    return state.pinnedDetailTrainId || selectedTrainId();
  }

  function emitTrainStateTransition(eventName, details) {
    if (!state.debugTrainStateTransitions) {
      return;
    }
    const payload = Object.assign({
      event: eventName,
      selectedTrainId: selectedTrainId(),
      pinnedDetailTrainId: state.pinnedDetailTrainId,
      pinnedDetailFromUser: state.pinnedDetailFromUser,
      mapTrainId: state.mapTrainId,
      mapPinnedTrainId: state.mapPinnedTrainId,
      movingMapMarkerKey: state.publicMapSelectedMarkerKey,
      mapFollowTrainId: state.mapFollowTrainId,
      mapFollowPaused: state.mapFollowPaused,
    }, details || {});
    console.debug("[train-app-state]", payload);
  }

  function movingMapSelectionState() {
    return {
      markerKey: String(state.publicMapSelectedMarkerKey || "").trim(),
      trainId: normalizeTrainId(state.mapFollowTrainId),
      paused: Boolean(state.mapFollowPaused || state.publicMapFollowPaused),
    };
  }

  function syncMovingMapFollowPaused(paused) {
    const nextPaused = Boolean(paused);
    state.mapFollowPaused = nextPaused;
    state.publicMapFollowPaused = nextPaused;
  }

  function setMovingMapSelection(options) {
    const nextOptions = options || {};
    const selection = movingMapSelectionState();
    const nextMarkerKey = isLiveTrainPopupKey(nextOptions.markerKey) ? String(nextOptions.markerKey || "").trim() : "";
    const nextTrainId = normalizeTrainId(nextOptions.trainId);
    const nextPaused = Boolean(nextOptions.paused);
    if (
      selection.markerKey === nextMarkerKey
      && selection.trainId === nextTrainId
      && selection.paused === nextPaused
    ) {
      return;
    }
    state.publicMapSelectedMarkerKey = nextMarkerKey;
    state.mapFollowTrainId = nextTrainId;
    syncMovingMapFollowPaused(nextPaused);
    emitTrainStateTransition("moving-map-selection", {
      reason: nextOptions.reason || "",
      markerKey: nextMarkerKey,
      mapFollowTrainId: nextTrainId,
      mapFollowPaused: nextPaused,
    });
  }

  function clearMovingMapSelection(reason) {
    const selection = movingMapSelectionState();
    if (!selection.markerKey && !selection.trainId && !selection.paused) {
      return;
    }
    state.publicMapSelectedMarkerKey = "";
    state.mapFollowTrainId = "";
    syncMovingMapFollowPaused(false);
    emitTrainStateTransition("moving-map-selection-clear", {
      reason: reason || "",
    });
  }

  function pauseMovingMapFollow(reason) {
    const selection = movingMapSelectionState();
    if ((!selection.markerKey && !selection.trainId && !normalizeTrainId(state.mapTrainId)) || selection.paused) {
      return;
    }
    syncMovingMapFollowPaused(true);
    emitTrainStateTransition("moving-map-selection-pause", {
      reason: reason || "user-moved-map",
      markerKey: selection.markerKey,
      mapFollowTrainId: selection.trainId,
    });
  }

  function setPinnedDetailTrain(trainId, options) {
    const nextTrainId = normalizeTrainId(trainId);
    const nextFromUser = Boolean(options && options.fromUser);
    if (state.pinnedDetailTrainId === nextTrainId && state.pinnedDetailFromUser === nextFromUser) {
      return;
    }
    state.pinnedDetailTrainId = nextTrainId;
    state.pinnedDetailFromUser = nextFromUser;
    emitTrainStateTransition("detail-pin", {
      toTrainId: nextTrainId,
      fromUser: nextFromUser,
      reason: options && options.reason ? options.reason : "",
    });
  }

  function clearPinnedDetailTrain(reason) {
    if (!state.pinnedDetailTrainId) {
      return;
    }
    state.pinnedDetailTrainId = "";
    state.pinnedDetailFromUser = false;
    emitTrainStateTransition("detail-unpin", {
      reason: reason || "manual",
      toTrainId: "",
    });
  }

  function setPinnedMapTrain(trainId, reason) {
    const nextTrainId = normalizeTrainId(trainId);
    if (state.mapPinnedTrainId === nextTrainId) {
      return;
    }
    state.mapPinnedTrainId = nextTrainId;
    emitTrainStateTransition("map-pin", {
      reason: reason || "",
      toTrainId: nextTrainId,
    });
  }

  function clearPinnedMapTrain(reason) {
    const selection = movingMapSelectionState();
    if (!state.mapPinnedTrainId && !selection.markerKey && !selection.trainId && !selection.paused) {
      return;
    }
    state.mapPinnedTrainId = "";
    clearMovingMapSelection(reason || "map-unpin");
    emitTrainStateTransition("map-unpin", {
      reason: reason || "",
      toTrainId: "",
    });
  }

  function isPinnedTrainKnown(trainId) {
    const target = normalizeTrainId(trainId);
    if (!target) {
      return false;
    }
    if (normalizeTrainId(state.mapTrainId) === target) {
      return true;
    }
    if (resolveTrainIdFromPayload(state.mapData) === target) {
      return true;
    }
    if (resolveTrainIdFromPayload(state.mapTrainDetail) === target) {
      return true;
    }
    if (resolveTrainIdFromPayload(state.currentRide) === target) {
      return true;
    }
    if (resolveTrainIdFromPayload(state.selectedTrain) === target) {
      return true;
    }
    if (Array.isArray(state.windowTrains) && state.windowTrains.some((item) => resolveTrainIdFromPayload(item) === target)) {
      return true;
    }
    if (Array.isArray(state.stationDepartures) && state.stationDepartures.some((item) => resolveTrainIdFromPayload(item) === target)) {
      return true;
    }
    return false;
  }

  function preferredMapTrainId() {
    return state.mapPinnedTrainId || currentRideTrainId() || "";
  }

  function setMapFollow(trainId) {
    const nextTrainId = normalizeTrainId(trainId);
    const selection = movingMapSelectionState();
    const changed = selection.trainId !== nextTrainId || selection.markerKey !== "";
    if (!changed && !selection.paused) {
      return;
    }
    setMovingMapSelection({
      markerKey: "",
      trainId: nextTrainId,
      paused: false,
      reason: "set-map-follow",
    });
    emitTrainStateTransition("map-follow-state", {
      action: changed ? "set" : "refresh",
      mapFollowTrainId: nextTrainId,
    });
  }

  function pauseMiniMapFollow(reason) {
    if (cfg.mode !== "mini-app" || state.tab !== "map") {
      return;
    }
    pauseMovingMapFollow(reason || "user-moved-map");
  }

  function resetMapFollow(trainId) {
    setMapFollow(trainId);
  }

  function alignMiniMapToSelectedTrain(trainId) {
    if (cfg.mode !== "mini-app") {
      return;
    }
    const nextTrainId = normalizeTrainId(trainId);
    setPinnedMapTrain(nextTrainId, "align-mini-map");
    state.mapTrainId = nextTrainId;
    state.expandedStopContextKey = "";
    resetMapFollow(state.mapTrainId);
  }

  function isPublicMapMode() {
    return usesPublicNetworkMap() || usesPublicTrainMap();
  }

  function usesPublicNetworkMap() {
    return cfg.mode === "public-network-map"
      || cfg.mode === "public-dashboard"
      || cfg.mode === "public-stations"
      || isMiniAppPublicMapFallback();
  }

  function usesPublicTrainMap() {
    return cfg.mode === "public-map" || cfg.mode === "public-train";
  }

  function isLiveTrainPopupKey(popupKey) {
    return String(popupKey || "").startsWith("live-train:");
  }

  function resetPublicMapSelection() {
    state.publicMapPopupKey = "";
    clearMovingMapSelection("public-map-selection-reset");
  }

  function setPublicMapPopupSelection(popupKey, popupOptions) {
    state.publicMapPopupKey = popupKey || "";
    if (popupOptions && popupOptions.movingMarkerTracking && isLiveTrainPopupKey(popupKey)) {
      setMovingMapSelection({
        markerKey: popupKey,
        trainId: popupOptions.movingTrainId || "",
        paused: false,
        reason: "popup-open",
      });
      return;
    }
  }

  function clearPublicMapPopupSelection(popupKey) {
    if (popupKey && state.publicMapPopupKey && state.publicMapPopupKey !== popupKey) {
      return;
    }
    state.publicMapPopupKey = "";
  }

  function syncActivePublicMap() {
    if (!isPublicMapMode()) {
      return;
    }
    if (!window.L) {
      schedulePublicMapLibraryRetry();
      return;
    }
    publicMapLibraryRetryCount = 0;
    if (publicMapLibraryRetryTimer && typeof window.clearTimeout === "function") {
      window.clearTimeout(publicMapLibraryRetryTimer);
      publicMapLibraryRetryTimer = null;
    }
    if (usesPublicTrainMap()) {
      syncMapFromDOM("public-train-map", state.mapData);
    } else {
      syncMapFromDOM("public-network-map", state.networkMapData);
    }
    applyPublicMapFollow();
  }

  function schedulePublicMapLibraryRetry() {
    if (publicMapLibraryRetryTimer || publicMapLibraryRetryCount >= 40) {
      return;
    }
    publicMapLibraryRetryCount += 1;
    const delayMs = Math.min(1000, 50 * publicMapLibraryRetryCount);
    const setTimer = window && typeof window.setTimeout === "function"
      ? window.setTimeout.bind(window)
      : setTimeout;
    publicMapLibraryRetryTimer = setTimer(() => {
      publicMapLibraryRetryTimer = null;
      syncActivePublicMap();
    }, delayMs);
  }

  function applyPublicMapFollow(controller) {
    if (!isPublicMapMode()) {
      return;
    }
    const mapModel = usesPublicTrainMap() ? state.mapData : state.networkMapData;
    applyMovingMapFollow(mapModel, {
      reason: "public-map-follow",
      missingReason: "public-map-follow-target-missing",
    }, controller);
  }

  function applyActiveMapFollow(controller) {
    if (cfg.mode === "mini-app") {
      applyMiniMapFollow(controller);
      return;
    }
    if (isPublicMapMode()) {
      applyPublicMapFollow(controller);
    }
  }

  function liveTrainItemForTrainId(mapModel, trainId) {
    const targetTrainId = normalizeTrainId(trainId);
    if (!mapModel || !targetTrainId) {
      return null;
    }
    if (mapModel.train) {
      const liveItem = buildSelectedTrainLiveItem(mapModel);
      return liveItem && normalizeTrainId(liveItem.trainId) === targetTrainId ? liveItem : null;
    }
    const liveItems = buildMapVisibleLiveItems(serviceDayTrainItemsForMapMatching(), activeNetworkMapSightings(mapModel));
    return liveItems.find((item) => normalizeTrainId(item.trainId) === targetTrainId) || null;
  }

  function movingMapFollowMarkerCandidates(mapModel, trainId) {
    const selection = movingMapSelectionState();
    const targetTrainId = normalizeTrainId(trainId || selection.trainId);
    const keys = [];
    const seen = new Set();
    const pushKey = (markerKey) => {
      const nextKey = String(markerKey || "").trim();
      if (!nextKey || seen.has(nextKey)) {
        return;
      }
      seen.add(nextKey);
      keys.push(nextKey);
    };
    pushKey(selection.markerKey);
    const liveItem = liveTrainItemForTrainId(mapModel, targetTrainId);
    if (liveItem && liveItem.markerKey) {
      pushKey(liveItem.markerKey);
    }
    return keys;
  }

  function handleMovingMapFollowMiss(selection, followOptions) {
    const nextSelection = selection || movingMapSelectionState();
    const nextOptions = followOptions || {};
    if (!nextSelection.markerKey && !nextSelection.trainId) {
      return false;
    }
    if (nextOptions.clearOnMissing) {
      clearMovingMapSelection(nextOptions.missingReason || "moving-map-follow-target-missing");
      return false;
    }
    emitTrainStateTransition("moving-map-follow-pending", {
      reason: nextOptions.reason || "",
      missingReason: nextOptions.missingReason || "moving-map-follow-target-missing",
      markerKey: nextSelection.markerKey,
      mapFollowTrainId: normalizeTrainId(nextOptions.trainId || nextSelection.trainId),
    });
    return false;
  }

  function applyMovingMapFollow(mapModel, options, controller) {
    const followOptions = options || {};
    const nextController = controller || mapController;
    const selection = movingMapSelectionState();
    if (selection.paused) {
      return false;
    }
    const followTrainId = normalizeTrainId(followOptions.trainId || selection.trainId);
    const markerKeys = movingMapFollowMarkerCandidates(mapModel, followTrainId);
    if (!markerKeys.length) {
      return handleMovingMapFollowMiss(selection, followOptions);
    }
    for (const markerKey of markerKeys) {
      if (!nextController || typeof nextController.panToMarker !== "function" || !nextController.panToMarker(markerKey)) {
        continue;
      }
      if (selection.markerKey !== markerKey || (followTrainId && selection.trainId !== followTrainId)) {
        setMovingMapSelection({
          markerKey: markerKey,
          trainId: followTrainId || selection.trainId,
          paused: false,
          reason: followOptions.reason || "moving-map-follow",
        });
      }
      return true;
    }
    return handleMovingMapFollowMiss(selection, followOptions);
  }

  function syncMapSelectionToCurrentRide() {
    const nextTrainId = preferredMapTrainId();
    if (!nextTrainId) {
      state.mapTrainId = "";
      state.mapData = null;
      state.mapTrainDetail = null;
      clearMapLoadState("train");
      return;
    }
    if (state.mapTrainId !== nextTrainId) {
      state.expandedStopContextKey = "";
    }
    state.mapTrainId = nextTrainId;
    emitTrainStateTransition("map-sync", {
      nextTrainId,
    });
  }

  function normalizeMiniAppTab(nextTab) {
    const clean = String(nextTab || "").trim();
    if (!clean || clean === "feed" || clean === "dashboard") {
      return "feed";
    }
    if (clean === "map") {
      return "map";
    }
    if (clean === "stations" || clean === "sightings") {
      return "stations";
    }
    if (clean === "profile" || clean === "settings") {
      return "profile";
    }
    return "feed";
  }

  function setMiniAppTab(nextTab, reason) {
    const normalizedTab = normalizeMiniAppTab(nextTab);
    if (state.tab === "map" && normalizedTab !== "map") {
      clearPinnedMapTrain(reason || `tab:${normalizedTab}`);
    }
    if (normalizedTab !== "map") {
      clearMapLoadState();
    }
    closeSiteMenus();
    state.tab = normalizedTab;
  }

  async function refreshActiveMapView(options) {
    const nextOptions = options || {};
    syncMapSelectionToCurrentRide();
    if (state.mapTrainId) {
      return refreshMapData(state.mapTrainId, false, "", nextOptions);
    }
    const results = await Promise.all([
      refreshNetworkMapData(true, nextOptions),
      refreshPublicDashboardAll(),
    ]);
    return results.some(Boolean);
  }

  async function openMap(trainId) {
    const previousMapTrainId = state.mapTrainId || "";
    alignMiniMapToSelectedTrain(trainId);
    setMiniAppTab("map", "open-map");
    await refreshMapData(trainId, false, previousMapTrainId, {
      onLoadStateChange() {
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
      },
    });
    renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
    return t("app_map_loaded");
  }

  async function showAllTrainsMap() {
    clearPinnedMapTrain("show-all-trains");
    state.expandedStopContextKey = "";
    syncMapSelectionToCurrentRide();
    if (!appEl) {
      return null;
    }
    if (!state.mapTrainId && !hasNetworkMapPayload(state.networkMapData)) {
      const results = await Promise.all([
        refreshNetworkMapData(true, {
          onLoadStateChange() {
            renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
          },
        }),
        refreshPublicDashboardAll(),
      ]);
      renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
      return results.some(Boolean) ? t("app_map_loaded") : null;
    }
    renderMiniApp();
    return null;
  }

  async function checkIn(trainId, boardingStationId, source) {
    const normalizedBoardingStationId = normalizeCheckInStationId(boardingStationId);
    const previousMapTrainId = state.mapTrainId || "";
    const payload = await api("/checkins/current", {
      method: "PUT",
      body: JSON.stringify({
        trainId,
        boardingStationId: normalizedBoardingStationId,
        source: source || undefined,
      }),
    });
    applyCurrentRidePayload(payload, { focusActiveRide: true });
    const settledTrainId = await settleCurrentRideAfterCheckIn(trainId);
    const nextTrainId = normalizeTrainId(settledTrainId || trainId);
    if (nextTrainId) {
      alignMiniMapToSelectedTrain(nextTrainId);
      try {
        await refreshMapData(nextTrainId, false, previousMapTrainId);
      } catch (_) {}
    }
    state.checkInDropdownOpen = false;
    setMiniAppTab("map", "check-in");
    state.statusText = t("app_checked_in");
    renderMiniApp();
    return t("app_checked_in");
  }

  async function checkoutRide() {
    await api("/checkins/current", { method: "DELETE" });
    await refreshCurrentRide();
    state.statusText = t("app_checked_out");
    renderMiniApp();
    return t("app_checked_out");
  }

  async function undoCheckout() {
    const payload = await api("/checkins/current/undo", { method: "POST" });
    if (payload.restored) {
      await refreshCurrentRide({ focusActiveRide: true });
      setMiniAppTab("map", "undo-checkout");
      state.statusText = t("app_undo_restored");
      renderMiniApp();
      return t("app_undo_restored");
    }
    return null;
  }

  async function submitReport(signal, explicitTrainId) {
    const trainId = explicitTrainId || currentRideTrainId() || selectedTrainId();
    if (!trainId) {
      throw new Error(t("app_status_empty"));
    }
    const payload = await api(`/trains/${encodeURIComponent(trainId)}/reports`, {
      method: "POST",
      body: JSON.stringify({ signal }),
    });
    let message = t("app_report_success");
    let kind = "success";
    if (payload.deduped) {
      message = t("app_report_deduped");
      kind = "info";
    } else if (payload.cooldownRemaining > 0) {
      const mins = Math.max(1, Math.ceil(Number(payload.cooldownRemaining) / 60000000000));
      message = t("app_report_cooldown", mins);
      kind = "info";
    }
    state.statusText = message;
    await refreshMe();
    if (state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train.id === trainId) {
      try {
        state.selectedTrain = await api(`/trains/${encodeURIComponent(trainId)}/status`);
      } catch (err) {
        if (!err || err.status !== 404) {
          throw err;
        }
      }
    }
    if (state.mapTrainId === trainId) {
      try {
        await refreshMapData(trainId);
      } catch (err) {
        if (!err || err.status !== 404) {
          throw err;
        }
      }
    }
    renderAfterReportMutation();
    return { message, kind };
  }

  async function submitStationSighting() {
    if (!state.selectedStation || !state.selectedStation.id) {
      throw new Error(t("app_public_station_prompt"));
    }
    if (!state.selectedSightingTrainId) {
      throw new Error(t("app_station_sighting_select_departure_toast"));
    }
    const payload = await api(`/stations/${encodeURIComponent(state.selectedStation.id)}/sightings`, {
      method: "POST",
      body: JSON.stringify({
        destinationStationId: state.stationSightingDestinationId || "",
        trainId: state.selectedSightingTrainId || "",
      }),
    });
    let message = t("app_station_sighting_success");
    let kind = "success";
    if (payload.deduped) {
      message = t("app_station_sighting_deduped");
      kind = "info";
    } else if (payload.cooldownRemaining > 0) {
      const mins = Math.max(1, Math.ceil(Number(payload.cooldownRemaining) / 60000000000));
      message = t("app_station_sighting_cooldown", mins);
      kind = "info";
    } else if (payload.event && payload.event.matchedTrainInstanceId) {
      message = `${t("app_station_sighting_success")} ${t("app_station_sighting_matched")}`;
    } else {
      message = `${t("app_station_sighting_success")} ${t("app_station_sighting_unmatched")}`;
    }

    await fetchStationDepartures(state.selectedStation.id);
    await refreshNetworkMapData(true);
    if (payload.event && payload.event.matchedTrainInstanceId) {
      const matchedTrainId = payload.event.matchedTrainInstanceId;
      if (state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train.id === matchedTrainId) {
        state.selectedTrain = await api(`/trains/${encodeURIComponent(matchedTrainId)}/status`);
      }
      if (state.mapTrainId === matchedTrainId) {
        await refreshMapData(matchedTrainId);
      }
    }
    state.statusText = message;
    renderMiniApp();
    return { message, kind };
  }

  function locationReportFeedback(payload, successKey) {
    let message = t(successKey || "app_location_report_success");
    let kind = "success";
    if (payload && payload.deduped) {
      message = t("app_location_report_deduped");
      kind = "info";
    } else if (payload && payload.cooldownRemaining > 0) {
      const mins = Math.max(1, Math.ceil(Number(payload.cooldownRemaining) / 60000000000));
      message = t("app_location_report_cooldown", mins);
      kind = "info";
    }
    return { message, kind };
  }

  async function refreshAfterLocationReport() {
    try {
      await refreshPublicIncidents();
    } catch (_) {}
    try {
      if (state.mapTrainId) {
        await refreshMapData(state.mapTrainId);
      } else {
        await refreshNetworkMapData(true);
      }
    } catch (_) {}
  }

  function reportMutationRenderTarget() {
    if (isMiniAppPublicMapFallback()) {
      return "public-network-map";
    }
    if (cfg.mode === "public-network-map") {
      return "public-network-map";
    }
    if (cfg.mode === "public-map") {
      return "public-map";
    }
    if (cfg.mode === "public-train") {
      return "public-train";
    }
    if (cfg.mode === "public-incidents") {
      return "public-incidents";
    }
    if (cfg.mode === "public-dashboard") {
      return "public-dashboard";
    }
    if (cfg.mode === "public-stations") {
      return "public-stations";
    }
    return "mini-app";
  }

  function renderAfterReportMutation() {
    const target = reportMutationRenderTarget();
    if (target === "public-network-map") {
      renderPublicNetworkMap();
      return;
    }
    if (target === "public-map") {
      renderPublicMap();
      return;
    }
    if (target === "public-train") {
      renderPublicTrain();
      return;
    }
    if (target === "public-incidents") {
      renderPublicIncidents();
      return;
    }
    if (target === "public-dashboard") {
      renderPublicDashboard();
      return;
    }
    if (target === "public-stations") {
      renderPublicStationSearch({ preserveInputFocus: true });
      return;
    }
    renderMiniApp();
  }

  async function submitStationReport(stationId) {
    const normalizedStationId = String(stationId || "").trim();
    if (!normalizedStationId) {
      throw new Error(t("app_public_station_prompt"));
    }
    const payload = await api(`/stations/${encodeURIComponent(normalizedStationId)}/reports`, {
      method: "POST",
    });
    const feedback = locationReportFeedback(payload, "app_station_report_success");
    state.statusText = feedback.message;
    await refreshAfterLocationReport();
    renderAfterReportMutation();
    return feedback;
  }

  async function submitLocationReport(input) {
    const data = input || {};
    const latitude = Number(data.latitude);
    const longitude = Number(data.longitude);
    const radiusMeters = Number(data.radiusMeters) || 100;
    const description = String(data.description || "").trim();
    if (!description) {
      throw new Error(t("app_location_report_description_required"));
    }
    const payload = await api("/location-reports", {
      method: "POST",
      body: JSON.stringify({
        latitude,
        longitude,
        radiusMeters,
        description,
      }),
    });
    const feedback = locationReportFeedback(payload, "app_location_report_success");
    state.statusText = feedback.message;
    await refreshAfterLocationReport();
    renderAfterReportMutation();
    return feedback;
  }

  async function muteTrain(trainId) {
    await api(`/trains/${encodeURIComponent(trainId)}/mute`, {
      method: "PUT",
      body: JSON.stringify({ durationMinutes: 30 }),
    });
    state.statusText = t("app_muted");
    renderMiniApp();
    return t("app_muted");
  }

  async function saveFavorite(fromStationId, toStationId, fromStationName, toStationName) {
    await api("/favorites", {
      method: "PUT",
      body: JSON.stringify({ fromStationId, toStationId, fromStationName, toStationName }),
    });
    await refreshFavorites();
    state.statusText = t("app_favorite_saved");
    renderMiniApp();
    return t("app_favorite_saved");
  }

  async function removeFavorite(fromStationId, toStationId) {
    await api("/favorites", {
      method: "DELETE",
      body: JSON.stringify({ fromStationId, toStationId }),
    });
    await refreshFavorites();
    state.statusText = t("app_favorite_removed");
    renderMiniApp();
    return t("app_favorite_removed");
  }

  async function saveSettings() {
    const alertsEnabled = document.getElementById("settings-alerts").checked;
    const alertStyle = document.getElementById("settings-style").value;
    const language = document.getElementById("settings-language").value;
    const payload = await api("/settings", {
      method: "PATCH",
      body: JSON.stringify({ alertsEnabled, alertStyle, language }),
    });
    state.me = state.me || {};
    state.me.settings = payload;
    state.lang = normalizeLang(payload.language);
    await loadMessages(state.lang);
    state.statusText = t("app_settings_saved");
    renderMiniApp();
    return t("app_settings_saved");
  }

  async function api(path, options = {}, allowAnonymous) {
    if (!state.authenticated && !allowAnonymous) {
      throw new Error(t("app_auth_required"));
    }
    const bundlePayload = await resolveBundlePath(path, options);
    if (bundlePayload) {
      return bundlePayload;
    }
    return fetchJSON(`${cfg.basePath}/api/v1${path}`, options);
  }

  async function publicApi(path, options = {}) {
    const requestOptions = Object.assign({ method: "GET" }, options || {});
    const bundlePayload = await resolveBundlePath(path, requestOptions);
    if (bundlePayload) {
      return bundlePayload;
    }
    return fetchJSON(`${cfg.basePath}/api/v1${path}`, requestOptions);
  }

  async function fetchSpacetimePath(path, options = {}, allowAnonymous) {
    const spec = spacetimeProcedureFor(path, options);
    if (!spec) {
      return null;
    }
    if (!spacetimeTransportAvailable() || !supportsLiveClient()) {
      throw new Error(t("app_data_unavailable_body"));
    }
    const client = await ensureLiveClient();
    if (!client) {
      throw new Error(t("app_data_unavailable_body"));
    }
    switch (spec.kind) {
      case "public_dashboard":
        return client.readPublicDashboard(spec.limit);
      case "public_service_day_trains":
        return client.readPublicServiceDayTrains();
      case "public_train":
        return client.readPublicTrain(spec.trainId);
      case "public_train_stops":
        return client.readPublicTrainStops(spec.trainId);
      case "public_network_map":
        return client.readPublicNetworkMap();
      case "public_station_search":
        return client.searchPublicStations(spec.query);
      case "public_station_departures":
        return client.readPublicStationDepartures(spec.stationId);
      case "public_incidents":
        return client.listPublicIncidents(spec.limit);
      case "public_incident_detail":
        return client.readPublicIncidentDetail(spec.incidentId);
      case "bootstrap_me":
        return enrichCurrentRidePayload(await client.bootstrapMe());
      case "window_trains":
        return client.listWindowTrains(spec.windowId);
      case "favorites_list":
        return client.favorites();
      case "current_ride":
        return enrichCurrentRidePayload(await client.currentRide());
      case "station_search":
        return client.searchStations(spec.query);
      case "station_departures":
        return client.stationDepartures(spec.stationId);
      case "station_sighting_destinations":
        return client.stationSightingDestinations(spec.stationId);
      case "route_destinations":
        return client.searchRouteDestinations(spec.originStationId, spec.query);
      case "route_trains":
        return client.listRouteTrains(spec.originStationId, spec.destinationStationId);
      case "train_status":
        return client.trainStatus(spec.trainId);
      case "train_stops":
        return client.trainStops(spec.trainId);
      case "settings_patch":
        return client.patchSettings(spec.body || {});
      case "favorite_save":
        return client.saveFavoriteRoute(spec.body || {});
      case "favorite_delete":
        return client.deleteFavoriteRoute(spec.body || {});
      case "checkin_put": {
        const bundleIdentity = currentBundleIdentity();
        let payload = await client.checkIn(
          spec.body.trainId,
          normalizeCheckInStationId(spec.body.boardingStationId),
          Boolean(spec.body && spec.body.source === "map"),
          bundleIdentity,
        );
        if (!payload || !payload.currentRide || !payload.currentRide.checkIn || payload.currentRide.checkIn.trainInstanceId !== spec.body.trainId) {
          try {
            payload = await client.currentRide();
          } catch (_) {}
        }
        return enrichCurrentRidePayload(payload);
      }
      case "checkin_delete": {
        return client.checkout();
      }
      case "checkin_undo": {
        return client.undoCheckout();
      }
      case "submit_report":
        return client.submitReport(spec.trainId, spec.body.signal, currentBundleIdentity());
      case "submit_station_sighting":
        return client.submitStationSighting(spec.stationId, spec.body || {}, currentBundleIdentity());
      case "vote_incident":
        return client.voteIncident(spec.incidentId, spec.body.value);
      case "comment_incident":
        return client.commentIncident(spec.incidentId, spec.body.body);
      case "train_mute":
        return client.setTrainMute(
          spec.trainId,
          Number(spec.body.durationMinutes) > 0 ? Number(spec.body.durationMinutes) : 30,
        );
      default:
        return null;
    }
  }

  function spacetimeProcedureFor(path, options = {}) {
    const method = String((options && options.method) || "GET").toUpperCase();
    const body = parseRequestBody(options && options.body);
    const parsed = parseProcedurePath(path);
    const pathname = parsed.pathname;
    const search = parsed.searchParams;
    if (isParkedSpacetimeRoute(pathname, method)) {
      return null;
    }
    if ((pathname === "/public/dashboard" || pathname === "/public/feed") && method === "GET") {
      return { kind: "public_dashboard", limit: parsePositiveInt(search.get("limit"), 60) };
    }
    if (pathname === "/public/service-day-trains" && method === "GET") {
      return { kind: "public_service_day_trains" };
    }
    if (pathname === "/public/map" && method === "GET") {
      return { kind: "public_network_map" };
    }
    if (pathname === "/public/stations" && method === "GET") {
      return { kind: "public_station_search", query: search.get("q") || "" };
    }
    if (pathname === "/me" && method === "GET") {
      return { kind: "bootstrap_me" };
    }
    if (path === "/checkins/current" && method === "GET") {
      return { kind: "current_ride" };
    }
    if (pathname.startsWith("/windows/") && method === "GET") {
      return { kind: "window_trains", windowId: decodeURIComponent(pathname.slice("/windows/".length)) };
    }
    if (path === "/favorites" && method === "GET") {
      return { kind: "favorites_list" };
    }
    if (pathname === "/stations" && method === "GET") {
      return { kind: "station_search", query: search.get("q") || "" };
    }
    if (path === "/settings" && method === "PATCH") {
      return { kind: "settings_patch", body };
    }
    if (path === "/favorites" && method === "PUT") {
      return { kind: "favorite_save", body };
    }
    if (path === "/favorites" && method === "DELETE") {
      return { kind: "favorite_delete", body };
    }
    let match = pathname.match(/^\/public\/trains\/([^/]+)\/stops$/);
    if (match && method === "GET") {
      return { kind: "public_train_stops", trainId: decodeURIComponent(match[1]) };
    }
    match = pathname.match(/^\/public\/trains\/([^/]+)$/);
    if (match && method === "GET") {
      return { kind: "public_train", trainId: decodeURIComponent(match[1]) };
    }
    match = pathname.match(/^\/public\/stations\/([^/]+)\/departures$/);
    if (match && method === "GET") {
      return { kind: "public_station_departures", stationId: decodeURIComponent(match[1]) };
    }
    match = pathname.match(/^\/stations\/([^/]+)\/departures$/);
    if (match && method === "GET") {
      return { kind: "station_departures", stationId: decodeURIComponent(match[1]) };
    }
    match = pathname.match(/^\/trains\/([^/]+)\/status$/);
    if (match && method === "GET") {
      return { kind: "train_status", trainId: decodeURIComponent(match[1]) };
    }
    match = pathname.match(/^\/trains\/([^/]+)\/stops$/);
    if (match && method === "GET") {
      return { kind: "train_stops", trainId: decodeURIComponent(match[1]) };
    }
    if (pathname === "/routes/destinations" && method === "GET") {
      return {
        kind: "route_destinations",
        originStationId: search.get("originStationId") || "",
        query: search.get("q") || "",
      };
    }
    if (pathname === "/routes/trains" && method === "GET") {
      return {
        kind: "route_trains",
        originStationId: search.get("originStationId") || "",
        destinationStationId: search.get("destinationStationId") || "",
      };
    }
    return null;
  }

  function parseProcedurePath(path) {
    try {
      return new URL(String(path || ""), "https://train-bot.local");
    } catch (_) {
      return new URL("/", "https://train-bot.local");
    }
  }

  function parsePositiveInt(raw, fallbackValue) {
    var parsed = Number.parseInt(String(raw || ""), 10);
    if (!Number.isFinite(parsed) || parsed < 0) {
      return fallbackValue;
    }
    return parsed;
  }

  function parseRequestBody(body) {
    if (!body) {
      return {};
    }
    if (typeof body === "string") {
      try {
        return JSON.parse(body);
      } catch (_) {
        return {};
      }
    }
    return body;
  }

  async function fetchCurrentRidePublicTrainStops(trainId) {
    return fetchJSON(`${cfg.basePath}/api/v1/public/trains/${encodeURIComponent(trainId)}/stops`);
  }

  async function enrichCurrentRidePayload(payload) {
    if (!payload || !payload.currentRide) {
      return payload || {};
    }
    if (payload.currentRide.train) {
      payload.currentRide.train = rideTrainDetailPayload(payload.currentRide.train);
      if (!currentRideNeedsTrainHydration(payload.currentRide)) {
        return payload;
      }
    }
    if (!payload.currentRide.checkIn || !payload.currentRide.checkIn.trainInstanceId) {
      return payload || {};
    }
    try {
      payload.currentRide.train = rideTrainDetailPayload(
        await fetchCurrentRidePublicTrainStops(payload.currentRide.checkIn.trainInstanceId)
      );
    } catch (_) {}
    return payload;
  }

  async function fetchJSON(url, options) {
    const response = await fetch(url, Object.assign({
      headers: { "Content-Type": "application/json" },
      credentials: "include",
    }, options || {}));
    const text = await response.text();
    let payload = {};
    try {
      payload = text ? JSON.parse(text) : {};
    } catch (_) {
      payload = {};
    }
    if (!response.ok) {
      const err = new Error(payload.error || t("app_status_error_with_code", response.status));
      err.status = response.status;
      throw err;
    }
    if (payload && payload.schedule) {
      state.scheduleMeta = payload.schedule;
    }
    return payload;
  }

  function normalizeCheckInStationId(value) {
    return String(value || "").trim();
  }

  function rememberErrorStatus(err) {
    if (err && err.status === 503) {
      state.scheduleMeta = null;
    }
    state.statusText = err && err.message ? err.message : String(err);
  }

  async function retryCurrentView() {
    const blocking = Boolean(state.strictModeLoadError && state.strictModeLoadError.blocking);
    clearStrictModeLoadError();
    try {
      if (isPublicMode()) {
        await ensurePublicSession();
      }
      if (isMiniAppPublicMapFallback()) {
        await Promise.all([refreshPublicNetworkMap(), refreshPublicDashboardAll()]);
        handleCurrentViewLoadSuccess();
        renderPublicNetworkMap();
        return true;
      }
      if (cfg.mode === "public-dashboard") {
        await refreshPublicDashboard();
        handleCurrentViewLoadSuccess();
        renderPublicDashboard();
        return true;
      }
      if (cfg.mode === "public-train") {
        await refreshPublicTrain();
        handleCurrentViewLoadSuccess();
        renderPublicTrain();
        return true;
      }
      if (cfg.mode === "public-deferred-map") {
        handleCurrentViewLoadSuccess();
        renderDeferredPublicPage("map");
        return true;
      }
      if (cfg.mode === "public-deferred-incidents") {
        handleCurrentViewLoadSuccess();
        renderDeferredPublicPage("incidents");
        return true;
      }
      if (cfg.mode === "public-map") {
        await refreshMapData(cfg.trainId, true);
        handleCurrentViewLoadSuccess();
        renderPublicMap();
        return true;
      }
      if (cfg.mode === "public-network-map") {
        await Promise.all([refreshPublicNetworkMap(), refreshPublicDashboardAll()]);
        handleCurrentViewLoadSuccess();
        renderPublicNetworkMap();
        return true;
      }
      if (cfg.mode === "public-stations") {
        if (state.publicStationSelected && state.publicStationSelected.id) {
          await refreshPublicStationDepartures(state.publicStationSelected.id);
        }
        handleCurrentViewLoadSuccess();
        renderPublicStationSearch({ preserveInputFocus: true });
        return true;
      }
      if (cfg.mode === "public-incidents") {
        await ensurePublicSession();
        await refreshPublicIncidents();
        if (state.publicIncidentSelectedId) {
          await refreshPublicIncidentDetail(state.publicIncidentSelectedId);
        } else if (state.publicIncidents[0] && !state.publicIncidentMobileLayout) {
          await refreshPublicIncidentDetail(state.publicIncidents[0].id);
        }
        handleCurrentViewLoadSuccess();
        renderPublicIncidents();
        return true;
      }
      if (!state.authenticated) {
        await authenticateMiniApp();
        if (!state.authenticated) {
          renderAuthRequired();
          return false;
        }
      }
      await loadMiniAppInitialData({
        rethrowPrimaryError: true,
        rememberBackgroundErrorStatus: handleCurrentViewRefreshError,
      });
      handleCurrentViewLoadSuccess();
      return true;
    } catch (err) {
      if (blocking && rememberStrictModeLoadError(err, true)) {
        renderDataUnavailable();
        return false;
      }
      handleCurrentViewRefreshError(err);
      return false;
    }
  }

  function rerenderCurrent(options) {
    if (isMiniAppPublicMapFallback()) {
      renderPublicNetworkMap();
      return;
    }
    if (cfg.mode === "mini-app") {
      if (options && options.preserveDetail) {
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
        return;
      }
      renderMiniApp();
      return;
    }
    if (cfg.mode === "public-dashboard") {
      renderPublicDashboard(options);
      return;
    }
    if (cfg.mode === "public-stations") {
      renderPublicStationSearch(options);
      return;
    }
    if (cfg.mode === "public-deferred-map") {
      renderDeferredPublicPage("map");
      return;
    }
    if (cfg.mode === "public-deferred-incidents") {
      renderDeferredPublicPage("incidents");
      return;
    }
    if (cfg.mode === "public-network-map") {
      renderPublicNetworkMap();
      return;
    }
    if (cfg.mode === "public-map") {
      renderPublicMap();
      return;
    }
    if (cfg.mode === "public-incidents") {
      renderPublicIncidents();
      return;
    }
    renderPublicTrain();
  }

  function currentRideTrainId() {
    if (state.currentRide && state.currentRide.train && state.currentRide.train.trainCard && state.currentRide.train.trainCard.train) {
      return state.currentRide.train.trainCard.train.id || "";
    }
    if (state.currentRide && state.currentRide.checkIn) {
      return state.currentRide.checkIn.trainInstanceId || "";
    }
    return "";
  }

  function syncSelectedTrainToCurrentRide(options = {}) {
    const activeSelectedTrainId = selectedTrainId();
    const nextRideTrainId = currentRideTrainId();
    const currentRideTrain = state.currentRide && state.currentRide.train ? state.currentRide.train : null;
    const pinnedTrainId = state.pinnedDetailTrainId;
    const previousRideTrainId = options.previousRideTrainId || "";
    const rideTransition = Boolean(previousRideTrainId) && previousRideTrainId !== nextRideTrainId;

    if (state.pinnedDetailFromUser && pinnedTrainId) {
      if (!isPinnedTrainKnown(pinnedTrainId)) {
        clearPinnedDetailTrain("pinned-train-not-known");
      } else {
        emitTrainStateTransition("detail-sync-preserve", {
          reason: "user-pinned",
          pinnedTrainId,
        });
        return;
      }
    }

    if (rideTransition && !state.pinnedDetailFromUser && nextRideTrainId) {
      setMapFollow(nextRideTrainId);
    }

    if (currentRideTrain && (options.focusActiveRide || !state.selectedTrain || activeSelectedTrainId === nextRideTrainId)) {
      state.selectedTrain = currentRideTrain;
      setPinnedDetailTrain(nextRideTrainId, { fromUser: false, reason: "current-ride-sync" });
      emitTrainStateTransition("detail-sync-ride", {
        toTrainId: nextRideTrainId,
        previousRideTrainId,
        focusActiveRide: Boolean(options.focusActiveRide),
      });
      return;
    }
    if (!nextRideTrainId && options.previousRideTrainId && activeSelectedTrainId === options.previousRideTrainId) {
      clearPinnedDetailTrain("ride-ended");
      state.selectedTrain = null;
      emitTrainStateTransition("detail-sync-ride-clear", {
        fromTrainId: activeSelectedTrainId,
      });
    }
  }

  function showToast(message, kind) {
    if (!message) return;
    state.toast = { message, kind: kind || "success" };
    if (toastTimer) {
      clearTimeout(toastTimer);
    }
    rerenderCurrent({ preserveInputFocus: true });
    toastTimer = setTimeout(() => {
      state.toast = null;
      rerenderCurrent({ preserveInputFocus: true });
    }, 3200);
    if (toastTimer && typeof toastTimer.unref === "function") {
      toastTimer.unref();
    }
  }

  function setActionButtonBusy(button, busy) {
    if (!button || typeof button.setAttribute !== "function") {
      return;
    }
    if (busy) {
      if (!button.dataset) {
        button.dataset = {};
      }
      button.dataset.busyWasDisabled = button.disabled ? "1" : "0";
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      if (button.classList && typeof button.classList.add === "function") {
        button.classList.add("is-busy");
      }
      return;
    }
    if (button.dataset && button.dataset.busyWasDisabled !== "1") {
      button.disabled = false;
    }
    if (button.dataset) {
      delete button.dataset.busyWasDisabled;
    }
    button.removeAttribute("aria-busy");
    if (button.classList && typeof button.classList.remove === "function") {
      button.classList.remove("is-busy");
    }
  }

  async function runUserAction(action, success, options) {
    const button = options && options.button ? options.button : null;
    setActionButtonBusy(button, true);
    try {
      const result = await action();
      const toast = typeof success === "function" ? success(result) : success;
      if (toast) {
        if (typeof toast === "string") {
          showToast(toast, "success");
        } else {
          showToast(toast.message, toast.kind || "success");
        }
      }
      return result;
    } catch (err) {
      setStatusFromError(err);
      showToast(err && err.message ? err.message : t("app_status_error"), "error");
      return null;
    } finally {
      setActionButtonBusy(button, false);
    }
  }

  function detachMapHost() {
    mapController.detach();
  }

  function setAppHTML(html) {
    detachMapHost();
    if (!appEl) {
      return;
    }
    appEl.innerHTML = html;
  }

  function syncMapFromDOM(containerId, mapModel) {
    mapController.sync(containerId, mapModel);
  }

  function materialCompare(methodName, left, right) {
    const api = externalFeedAPI();
    if (api && typeof api[methodName] === "function") {
      return api[methodName](left, right);
    }
    return JSON.stringify(left) === JSON.stringify(right);
  }

  function sameMaterialValue(left, right) {
    return materialCompare("sameMaterialValue", left, right);
  }

  function samePayloadIgnoringSchedule(left, right) {
    const api = externalFeedAPI();
    if (api && typeof api.sameMaterialValue === "function") {
      return api.sameMaterialValue(left, right, { schedule: true, generatedAt: true });
    }
    return JSON.stringify(left) === JSON.stringify(right);
  }

  function sameTrainStopsPayload(left, right) {
    return materialCompare("sameTrainStopsPayload", left, right);
  }

  function sameNetworkMapPayload(left, right) {
    return materialCompare("sameNetworkMapPayload", left, right);
  }

  function samePublicDashboardPayload(left, right) {
    return materialCompare("samePublicDashboard", left, right);
  }

  function samePublicStationDeparturesPayload(left, right) {
    return materialCompare("samePublicStationDepartures", left, right);
  }

  function sameExternalFeedMaterialState(left, right) {
    return materialCompare("sameExternalFeedState", left, right);
  }

  function materialMapConfigKey(config) {
    const api = externalFeedAPI();
    if (api && typeof api.mapConfigSignature === "function") {
      return api.mapConfigSignature(config);
    }
    return JSON.stringify(config);
  }

  function materialMarkerSignature(item) {
    const api = externalFeedAPI();
    if (api && typeof api.mapConfigMarkerSignature === "function") {
      return api.mapConfigMarkerSignature(item);
    }
    return JSON.stringify(item);
  }

  function markerReconcilePlan(previousItems, nextItems, openPopupKey) {
    const api = externalFeedAPI();
    if (api && typeof api.planMarkerReconcile === "function") {
      return api.planMarkerReconcile(previousItems, nextItems, openPopupKey);
    }
    return {
      addKeys: [],
      updateKeys: [],
      removeKeys: [],
      retainPopupKey: "",
      clearPopup: false,
      hasChanges: true,
    };
  }

  function markerMovementTimestampMs(item) {
    const candidates = [
      item && item.movementObservedAt,
      item && item.updatedAt,
      item && item.external && item.external.updatedAt,
    ];
    for (const value of candidates) {
      if (!value) {
        continue;
      }
      const timestampMs = new Date(value).getTime();
      if (Number.isFinite(timestampMs)) {
        return timestampMs;
      }
    }
    return 0;
  }

  function markerMovementDurationMs(previousTimestampMs, nextTimestampMs, fallbackElapsedMs) {
    if (
      Number.isFinite(previousTimestampMs) &&
      previousTimestampMs > 0 &&
      Number.isFinite(nextTimestampMs) &&
      nextTimestampMs > previousTimestampMs
    ) {
      return Math.min(
        MAP_MARKER_ANIMATION_MAX_MS,
        Math.max(MAP_MARKER_ANIMATION_MIN_MS, Math.round((nextTimestampMs - previousTimestampMs) * 0.85))
      );
    }
    if (Number.isFinite(fallbackElapsedMs) && fallbackElapsedMs > 0) {
      return Math.min(
        MAP_MARKER_ANIMATION_MAX_MS,
        Math.max(MAP_MARKER_ANIMATION_MIN_MS, Math.round(fallbackElapsedMs * 0.85))
      );
    }
    return MAP_MARKER_ANIMATION_DEFAULT_MS;
  }

  function markerLatLngFromLeaflet(marker) {
    if (!marker || typeof marker.getLatLng !== "function") {
      return null;
    }
    const latLng = marker.getLatLng();
    if (!latLng || !Number.isFinite(latLng.lat) || !Number.isFinite(latLng.lng)) {
      return null;
    }
    return [latLng.lat, latLng.lng];
  }

  function markerLatLngEqual(left, right) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== 2 || right.length !== 2) {
      return false;
    }
    return Math.abs(left[0] - right[0]) <= MAP_MARKER_COORD_EPSILON
      && Math.abs(left[1] - right[1]) <= MAP_MARKER_COORD_EPSILON;
  }

  function markerMotionEase(progress) {
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

  function createMapController() {
    return {
      map: null,
      mapModel: null,
      hostEl: null,
      containerEl: null,
      viewportEl: null,
      detailLayerEl: null,
      tileLayer: null,
      baseLayer: null,
      sightingLayer: null,
      trainLayer: null,
      routeLine: null,
      containerId: "",
      modelKey: "",
      viewKey: "",
      routeSignature: "",
      viewCache: new Map(),
      markerIndex: new Map(),
      markerState: new Map(),
      baseMarkerKeys: new Set(),
      sightingMarkerKeys: new Set(),
      trainMarkerKeys: new Set(),
      focusedEntityKey: "",
      openPopupKey: "",
      pendingPopupKey: "",
      syncingLayers: false,
      programmaticViewUntil: 0,
      layoutFrame: 0,
      layoutTimeouts: [],
      resizeObserver: null,
      observedContainer: null,
      lastLayoutOptions: null,
      lastContainerId: "",
      pendingRelayout: false,
      mapDetailDismissSuppressedUntil: 0,
      mapGestureStartCenter: null,
      mapGestureStartZoom: null,
      mapGestureZoomPending: false,
      pendingDocumentTap: null,
      lastTapProxyAt: 0,
      lastMarkerInteractionAt: 0,

      clearScheduledLayout() {
        if (this.layoutFrame && typeof window.cancelAnimationFrame === "function") {
          window.cancelAnimationFrame(this.layoutFrame);
        }
        this.layoutFrame = 0;
        const clearTimer = typeof window.clearTimeout === "function" ? window.clearTimeout.bind(window) : clearTimeout;
        while (this.layoutTimeouts.length) {
          clearTimer(this.layoutTimeouts.pop());
        }
      },

      disconnectResizeObserver() {
        if (this.resizeObserver && typeof this.resizeObserver.disconnect === "function") {
          this.resizeObserver.disconnect();
        }
        this.resizeObserver = null;
        this.observedContainer = null;
      },

      observeContainer(container) {
        if (!container || typeof window.ResizeObserver !== "function") {
          return;
        }
        if (this.observedContainer === container && this.resizeObserver) {
          return;
        }
        this.disconnectResizeObserver();
        const controller = this;
        this.observedContainer = container;
        this.resizeObserver = new window.ResizeObserver(() => {
          controller.requestRelayout("container-resize");
        });
        this.resizeObserver.observe(container);
      },

      layoutOptions(options) {
        return {
          shouldFit: Boolean(options && options.shouldFit),
          shouldRestore: Boolean(options && options.shouldRestore),
          savedView: options && options.savedView ? options.savedView : null,
          bounds: options && Array.isArray(options.bounds) ? options.bounds : [],
        };
      },

      ensureHost() {
        if (this.hostEl) {
          return this.hostEl;
        }
        const hostEl = document.createElement("div");
        hostEl.className = "train-map";
        this.hostEl = hostEl;
        return hostEl;
      },

      ensureContainerSlots(container) {
        let viewportEl = container && typeof container.querySelector === "function"
          ? container.querySelector(".train-map-viewport")
          : null;
        let detailLayerEl = container && typeof container.querySelector === "function"
          ? container.querySelector(".train-map-detail-layer")
          : null;
        if (!container) {
          return { viewportEl: null, detailLayerEl: null };
        }
        if (typeof container.querySelector !== "function") {
          this.detailLayerEl = null;
          return { viewportEl: container, detailLayerEl: null };
        }
        if (!viewportEl) {
          viewportEl = document.createElement("div");
          viewportEl.className = "train-map-viewport";
          while (container.firstChild) {
            viewportEl.appendChild(container.firstChild);
          }
          container.appendChild(viewportEl);
        }
        if (!detailLayerEl) {
          detailLayerEl = document.createElement("div");
          detailLayerEl.className = "train-map-detail-layer";
          detailLayerEl.hidden = true;
          container.appendChild(detailLayerEl);
        }
        this.containerEl = container;
        this.viewportEl = viewportEl;
        this.detailLayerEl = detailLayerEl;
        return { viewportEl, detailLayerEl };
      },

      currentShellRect() {
        return rectFromElement(this.containerEl || this.viewportEl || this.hostEl);
      },

      currentMapRect() {
        return rectFromElement(this.hostEl || this.viewportEl || this.containerEl);
      },

      currentDetailRect() {
        if (!this.detailLayerEl || this.detailLayerEl.hidden || typeof this.detailLayerEl.querySelector !== "function") {
          return null;
        }
        return rectFromElement(this.detailLayerEl.querySelector(".train-map-detail-card"));
      },

      suppressNextDetailDismiss() {
        this.mapDetailDismissSuppressedUntil = Date.now() + MAP_DETAIL_DISMISS_SUPPRESS_WINDOW_MS;
      },

      isDetailDismissSuppressed() {
        return Date.now() < Number(this.mapDetailDismissSuppressedUntil || 0);
      },

      mapProxyTargetElements() {
        if (!this.containerEl || typeof this.containerEl.querySelectorAll !== "function") {
          return [];
        }
        const selectors = [
          "button",
          "a[href]",
          "input",
          "label",
          "summary",
          ".leaflet-control a",
          ".leaflet-control button",
        ];
        const seen = new Set();
        return Array.from(this.containerEl.querySelectorAll(selectors.join(", "))).filter((element) => {
          if (!element || seen.has(element)) {
            return false;
          }
          seen.add(element);
          return Boolean(rectFromElement(element));
        });
      },

      actionableElementAtPoint(point) {
        const matches = this.mapProxyTargetElements()
          .map((element) => ({
            element,
            rect: rectFromElement(element),
          }))
          .filter((entry) => pointInsideRect(point, entry.rect));
        if (!matches.length) {
          return null;
        }
        matches.sort((left, right) => {
          const leftArea = left.rect.width * left.rect.height;
          const rightArea = right.rect.width * right.rect.height;
          return leftArea - rightArea;
        });
        return matches[0].element;
      },

      markerProxyRect(entry) {
        const marker = entry && entry.marker ? entry.marker : null;
        const markerElement = marker
          ? ((typeof marker.getElement === "function" && marker.getElement()) || marker._icon || marker._path || null)
          : null;
        const elementRect = rectFromElement(markerElement);
        if (elementRect) {
          return elementRect;
        }
        if (
          !this.map ||
          typeof this.map.latLngToContainerPoint !== "function" ||
          !entry ||
          !Array.isArray(entry.targetLatLng) ||
          entry.targetLatLng.length !== 2
        ) {
          return null;
        }
        const mapRect = this.currentMapRect();
        if (!mapRect) {
          return null;
        }
        const point = this.map.latLngToContainerPoint(entry.targetLatLng);
        if (!point || !Number.isFinite(Number(point.x)) || !Number.isFinite(Number(point.y))) {
          return null;
        }
        return {
          left: mapRect.left + Number(point.x) - MAP_TOUCH_PROXY_RADIUS_PX,
          top: mapRect.top + Number(point.y) - MAP_TOUCH_PROXY_RADIUS_PX,
          width: MAP_TOUCH_PROXY_RADIUS_PX * 2,
          height: MAP_TOUCH_PROXY_RADIUS_PX * 2,
          right: mapRect.left + Number(point.x) + MAP_TOUCH_PROXY_RADIUS_PX,
          bottom: mapRect.top + Number(point.y) + MAP_TOUCH_PROXY_RADIUS_PX,
        };
      },

      nearestMarkerKeyForPoint(point) {
        let best = null;
        this.markerState.forEach((entry, markerKey) => {
          const item = entry && entry.item ? entry.item : null;
          const interaction = item && item.interaction ? item.interaction : null;
          const rect = this.markerProxyRect(entry);
          if (!interaction || !interaction.entityKey || !rect) {
            return;
          }
          const center = {
            x: rect.left + (rect.width / 2),
            y: rect.top + (rect.height / 2),
          };
          const distance = pointDistance(point, center);
          const radius = (Math.max(rect.width, rect.height, MAP_TOUCH_PROXY_RADIUS_PX) / 2) + 10;
          if (distance > radius) {
            return;
          }
          if (!best || distance < best.distance) {
            best = {
              markerKey: item.markerKey || markerKey,
              distance,
            };
          }
        });
        return best ? best.markerKey : "";
      },

      shouldProxyDocumentTapTarget(target) {
        if (!target) {
          return true;
        }
        if (targetTagName(target) === "HTML" || targetTagName(target) === "BODY") {
          return true;
        }
        if (typeof target.closest !== "function") {
          return false;
        }
        if (target.closest("button, a[href], input, label, textarea, select, summary, [data-action], .leaflet-interactive, .leaflet-control, .train-map-detail-card")) {
          return false;
        }
        return Boolean(target.closest(".train-map-shell, .train-map-viewport, .leaflet-container, .leaflet-pane"));
      },

      triggerProxyElementAction(element) {
        if (!element) {
          return false;
        }
        if (typeof element.click === "function") {
          element.click();
          return true;
        }
        if (element.getAttribute && element.getAttribute("data-action") === "close-map-detail") {
          this.closePopup();
          return true;
        }
        return false;
      },

      recordDocumentTapStart(event) {
        if (!isTouchLikeEvent(event)) {
          return;
        }
        const point = eventClientPoint(event);
        if (!point) {
          this.pendingDocumentTap = null;
          return;
        }
        this.pendingDocumentTap = {
          point,
          time: Date.now(),
        };
      },

      clearDocumentTap() {
        this.pendingDocumentTap = null;
      },

      handleDocumentTapEnd(event) {
        const pendingTap = this.pendingDocumentTap;
        const point = eventClientPoint(event, { preferChangedTouches: true }) || (pendingTap ? pendingTap.point : null);
        const shellRect = this.currentShellRect();
        const detailRect = this.currentDetailRect();
        let actionableElement = null;
        let markerKey = "";
        this.pendingDocumentTap = null;
        if (!isTouchLikeEvent(event) || !pendingTap || !point || !shellRect) {
          return false;
        }
        if (!pointInsideRect(point, shellRect)) {
          return false;
        }
        if (pointDistance(pendingTap.point, point) > MAP_TOUCH_PROXY_MAX_MOVEMENT_PX) {
          return false;
        }
        if ((Date.now() - pendingTap.time) > MAP_TOUCH_PROXY_MAX_DURATION_MS) {
          return false;
        }
        if (this.lastTapProxyAt && Date.now() - this.lastTapProxyAt < 240) {
          return false;
        }
        if (this.lastMarkerInteractionAt && Date.now() - this.lastMarkerInteractionAt < 240) {
          return false;
        }
        if (!this.shouldProxyDocumentTapTarget(event && event.target ? event.target : null)) {
          return false;
        }
        actionableElement = this.actionableElementAtPoint(point);
        if (actionableElement) {
          this.lastTapProxyAt = Date.now();
          this.triggerProxyElementAction(actionableElement);
          return true;
        }
        if (detailRect && pointInsideRect(point, detailRect)) {
          return false;
        }
        markerKey = this.nearestMarkerKeyForPoint(point);
        if (!markerKey) {
          return false;
        }
        this.lastTapProxyAt = Date.now();
        this.handleMarkerInteraction(markerKey);
        return true;
      },

      markerEntryByEntityKey(entityKey) {
        if (!entityKey) {
          return null;
        }
        for (const entry of this.markerState.values()) {
          if (!entry || !entry.item || !entry.item.interaction) {
            continue;
          }
          if ((entry.item.interaction.entityKey || "") === entityKey) {
            return entry;
          }
        }
        return null;
      },

      selectionOptionsForEntityKey(entityKey) {
        const entry = this.markerEntryByEntityKey(entityKey);
        return entry && entry.item ? this.markerSelectionOptions(entry.item) : {};
      },

      isMovingEntityKey(entityKey) {
        const selectionOptions = this.selectionOptionsForEntityKey(entityKey);
        return Boolean(selectionOptions && selectionOptions.movingMarkerTracking);
      },

      hasActiveMovingMapFocus() {
        const selection = movingMapSelectionState();
        return Boolean(
          this.isMovingEntityKey(this.focusedEntityKey)
          || this.isMovingEntityKey(this.openPopupKey)
          || selection.markerKey
          || selection.trainId
          || selection.paused
        );
      },

      clearMovingMapFocus(reason) {
        const selection = movingMapSelectionState();
        const shouldClearFocus = this.isMovingEntityKey(this.focusedEntityKey);
        const shouldClearDetail = this.isMovingEntityKey(this.openPopupKey);
        const shouldClearFollow = Boolean(selection.markerKey || selection.trainId || selection.paused);
        if (!shouldClearFocus && !shouldClearDetail && !shouldClearFollow) {
          return false;
        }
        if (shouldClearFollow) {
          clearMovingMapSelection(reason || "moving-map-focus-cleared");
        }
        if (shouldClearFocus) {
          this.focusedEntityKey = "";
        }
        if (shouldClearDetail) {
          this.closePopup();
        }
        return true;
      },

      currentMapCenterLatLng() {
        const center = this.map && typeof this.map.getCenter === "function" ? this.map.getCenter() : null;
        if (!center || !Number.isFinite(Number(center.lat)) || !Number.isFinite(Number(center.lng))) {
          return null;
        }
        return [Number(center.lat), Number(center.lng)];
      },

      currentMapZoom() {
        if (!this.map || typeof this.map.getZoom !== "function") {
          return MAP_DEFAULT_VIEW_ZOOM;
        }
        return Number(this.map.getZoom());
      },

      mapViewportCenterPoint(mapSize) {
        const width = Number(mapSize && mapSize.x);
        const height = Number(mapSize && mapSize.y);
        if (!Number.isFinite(width) || !Number.isFinite(height)) {
          return null;
        }
        return window.L && typeof window.L.point === "function"
          ? window.L.point(width / 2, height / 2)
          : { x: width / 2, y: height / 2 };
      },

      mapFollowAnchorPoint(mapSize) {
        return this.mapViewportCenterPoint(mapSize);
      },

      clearMapGestureStart() {
        this.mapGestureStartCenter = null;
        this.mapGestureStartZoom = null;
        this.mapGestureZoomPending = false;
      },

      beginUserMapGesture(kind) {
        const gestureKind = kind || "move";
        const zoom = this.currentMapZoom();
        let center = null;
        if (
          ((gestureKind !== "zoom") && this.isProgrammaticViewChange())
          || !this.hasActiveMovingMapFocus()
          || !Number.isFinite(zoom)
        ) {
          this.clearMapGestureStart();
          return false;
        }
        if (!Array.isArray(this.mapGestureStartCenter) || !Number.isFinite(Number(this.mapGestureStartZoom))) {
          center = this.currentMapCenterLatLng();
          if (!center) {
            this.clearMapGestureStart();
            return false;
          }
          this.mapGestureStartCenter = center.slice();
          this.mapGestureStartZoom = zoom;
        }
        if (gestureKind === "zoom") {
          this.mapGestureZoomPending = true;
        }
        return true;
      },

      mapPanDistancePx(startCenterLatLng) {
        let mapSize = null;
        let startPoint = null;
        let centerPoint = null;
        const currentCenter = this.currentMapCenterLatLng();
        if (!Array.isArray(startCenterLatLng) || startCenterLatLng.length !== 2) {
          return 0;
        }
        if (
          this.map
          && typeof this.map.getSize === "function"
          && typeof this.map.latLngToContainerPoint === "function"
        ) {
          mapSize = this.map.getSize();
          startPoint = this.map.latLngToContainerPoint(startCenterLatLng);
          centerPoint = this.mapViewportCenterPoint(mapSize);
          if (
            mapSize
            && startPoint
            && centerPoint
            && Number.isFinite(Number(startPoint.x))
            && Number.isFinite(Number(startPoint.y))
          ) {
            return Math.sqrt(
              Math.pow(Number(startPoint.x) - Number(centerPoint.x), 2)
              + Math.pow(Number(startPoint.y) - Number(centerPoint.y), 2)
            );
          }
        }
        if (
          currentCenter
          && currentCenter.length === 2
          && markerLatLngEqual(startCenterLatLng, currentCenter)
        ) {
          return 0;
        }
        return MAP_USER_PAN_TOLERANCE_PX + 1;
      },

      finishUserMapGesture(reason, options) {
        const startCenter = Array.isArray(this.mapGestureStartCenter) ? this.mapGestureStartCenter.slice() : null;
        const startZoom = Number(this.mapGestureStartZoom);
        const zoomPending = Boolean(this.mapGestureZoomPending);
        if (!startCenter || !Number.isFinite(startZoom) || !this.hasActiveMovingMapFocus()) {
          this.clearMapGestureStart();
          return false;
        }
        if (this.currentMapZoom() !== startZoom) {
          this.clearMapGestureStart();
          return this.clearMovingMapFocus(reason || "user-zoomed-map");
        }
        if (options && options.deferIfZoomPending && zoomPending) {
          return false;
        }
        this.clearMapGestureStart();
        if (this.mapPanDistancePx(startCenter) > MAP_USER_PAN_TOLERANCE_PX) {
          return this.clearMovingMapFocus(reason || "user-moved-map");
        }
        return false;
      },

      focusCenterLatLng(targetLatLng) {
        let mapSize = null;
        let currentPoint = null;
        let anchorPoint = null;
        let centerPoint = null;
        if (
          !this.map ||
          !targetLatLng ||
          !Array.isArray(targetLatLng) ||
          targetLatLng.length !== 2 ||
          typeof this.map.getSize !== "function" ||
          typeof this.map.latLngToContainerPoint !== "function" ||
          typeof this.map.containerPointToLatLng !== "function"
        ) {
          return targetLatLng;
        }
        mapSize = this.map.getSize();
        if (!mapSize || !Number.isFinite(Number(mapSize.x)) || !Number.isFinite(Number(mapSize.y))) {
          return targetLatLng;
        }
        currentPoint = this.map.latLngToContainerPoint(targetLatLng);
        if (!currentPoint || !Number.isFinite(Number(currentPoint.x)) || !Number.isFinite(Number(currentPoint.y))) {
          return targetLatLng;
        }
        anchorPoint = this.mapFollowAnchorPoint(mapSize);
        centerPoint = this.mapViewportCenterPoint(mapSize);
        if (!anchorPoint || !centerPoint) {
          return targetLatLng;
        }
        return this.map.containerPointToLatLng({
          x: Number(centerPoint.x) + (Number(currentPoint.x) - Number(anchorPoint.x)),
          y: Number(centerPoint.y) + (Number(currentPoint.y) - Number(anchorPoint.y)),
        });
      },

      focusLatLng(targetLatLng, options) {
        const latLng = Array.isArray(targetLatLng)
          ? targetLatLng.slice(0, 2)
          : markerLatLngFromLeaflet(targetLatLng);
        const animate = Boolean(options && options.animate);
        const nextCenter = this.focusCenterLatLng(latLng) || latLng;
        if (!this.map || !nextCenter) {
          return false;
        }
        this.markProgrammaticView();
        if (typeof this.map.panTo === "function") {
          this.map.panTo(nextCenter, { animate });
          return true;
        }
        if (typeof this.map.setView === "function") {
          this.map.setView(nextCenter, this.map.getZoom(), { animate });
          return true;
        }
        return false;
      },

      ensureMap() {
        if (this.map || !window.L) {
          return this.map;
        }
        const map = window.L.map(this.ensureHost(), {
          zoomControl: true,
          closePopupOnClick: true,
        });
        this.tileLayer = window.L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
          attribution: "&copy; OpenStreetMap contributors",
          maxZoom: 19,
        }).addTo(map);
        this.baseLayer = window.L.layerGroup().addTo(map);
        this.sightingLayer = window.L.layerGroup().addTo(map);
        this.trainLayer = window.L.layerGroup().addTo(map);
        const persistView = () => {
          if (this.isProgrammaticViewChange()) {
            return;
          }
          this.saveCurrentView();
        };
        const syncVisibleMoveLayers = () => {
          this.finishUserMapGesture("user-moved-map", { deferIfZoomPending: true });
          this.refreshViewportLayers();
        };
        const syncVisibleZoomLayers = () => {
          this.finishUserMapGesture("user-zoomed-map");
          this.refreshViewportLayers();
        };
        map.on("movestart", () => {
          this.beginUserMapGesture("move");
        });
        map.on("zoomstart", () => {
          this.beginUserMapGesture("zoom");
        });
        map.on("moveend", persistView);
        map.on("zoomend", persistView);
        map.on("moveend", syncVisibleMoveLayers);
        map.on("zoomend", syncVisibleZoomLayers);
        map.on("click", (event) => {
          this.handleMapClick(event);
        });
        this.map = map;
        return this.map;
      },

      handleMapClick(event) {
        if (!mapReportsEnabled() || !event || !event.latlng) {
          return false;
        }
        if (Date.now() - this.lastMarkerInteractionAt < 250) {
          return false;
        }
        const latitude = Number(event.latlng.lat);
        const longitude = Number(event.latlng.lng);
        if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) {
          return false;
        }
        const html = buildLocationReportDraftHTML({ latitude, longitude });
        if (!window.L || typeof window.L.popup !== "function" || !this.map) {
          return false;
        }
        window.L.popup()
          .setLatLng(event.latlng)
          .setContent(html)
          .openOn(this.map);
        return true;
      },

      detach() {
        this.clearScheduledLayout();
        this.disconnectResizeObserver();
        this.containerEl = null;
        this.viewportEl = null;
        this.detailLayerEl = null;
        if (this.hostEl && this.hostEl.parentNode) {
          this.hostEl.parentNode.removeChild(this.hostEl);
        }
        this.containerId = "";
      },

      storageKey(viewKey) {
        return `pixel-train-map-view:${viewKey}`;
      },

      loadStoredView(viewKey) {
        if (!viewKey) {
          return null;
        }
        if (this.viewCache.has(viewKey)) {
          return this.viewCache.get(viewKey);
        }
        try {
          const raw = window.localStorage.getItem(this.storageKey(viewKey));
          if (!raw) {
            return null;
          }
          const parsed = JSON.parse(raw);
          if (!parsed || !Array.isArray(parsed.center) || parsed.center.length !== 2) {
            return null;
          }
          const lat = Number(parsed.center[0]);
          const lng = Number(parsed.center[1]);
          const zoom = Number(parsed.zoom);
          if (!Number.isFinite(lat) || !Number.isFinite(lng) || !Number.isFinite(zoom)) {
            return null;
          }
          const view = { center: [lat, lng], zoom: zoom };
          this.viewCache.set(viewKey, view);
          return view;
        } catch (_) {
          return null;
        }
      },

      saveCurrentView() {
        if (!this.map || !this.viewKey || !this.map._loaded) {
          return;
        }
        const center = this.map.getCenter();
        const view = {
          center: [roundMapCoord(center.lat), roundMapCoord(center.lng)],
          zoom: this.map.getZoom(),
        };
        this.viewCache.set(this.viewKey, view);
        try {
          window.localStorage.setItem(this.storageKey(this.viewKey), JSON.stringify(view));
        } catch (_) {
          // Ignore storage failures and keep the in-memory view only.
        }
      },

      isProgrammaticViewChange() {
        return Date.now() < this.programmaticViewUntil;
      },

      markProgrammaticView() {
        this.programmaticViewUntil = Date.now() + 300;
      },

      applySavedView(view) {
        if (!this.map || !view) {
          return;
        }
        this.markProgrammaticView();
        this.map.setView(view.center, view.zoom, { animate: false });
        this.saveCurrentView();
      },

      viewportContext(fallbackView) {
        return mapViewportContext(this.map, fallbackView);
      },

      buildConfig(mapModel, options) {
        const buildOptions = options || {};
        const viewport = this.viewportContext(buildOptions.view || null);
        return mapModel && mapModel.train
          ? buildTrainMapConfig(mapModel, viewport)
          : buildNetworkMapConfig(mapModel, viewport);
      },

      refreshViewportLayers() {
        if (!this.map || !this.mapModel) {
          return;
        }
        const config = this.buildConfig(this.mapModel);
        if (!config.bounds.length || this.modelKey === config.modelKey) {
          return;
        }
        this.modelKey = config.modelKey;
        this.updateLayers(config);
        this.restorePendingPopup();
      },

      panToMarker(markerKey) {
        if (!this.map || !markerKey || !this.markerIndex.has(markerKey)) {
          return false;
        }
        const marker = this.markerIndex.get(markerKey);
        const entry = this.markerState.get(markerKey) || null;
        const targetLatLng = entry && Array.isArray(entry.targetLatLng) && entry.targetLatLng.length === 2
          ? (window.L && typeof window.L.latLng === "function"
            ? window.L.latLng(entry.targetLatLng[0], entry.targetLatLng[1])
            : entry.targetLatLng.slice())
          : null;
        if (!targetLatLng && (!marker || typeof marker.getLatLng !== "function")) {
          return false;
        }
        return this.focusLatLng(targetLatLng || marker.getLatLng(), { animate: false });
      },

      currentPopupKey() {
        return this.openPopupKey || "";
      },

      reset() {
        this.clearScheduledLayout();
        this.disconnectResizeObserver();
        if (this.map && typeof this.map.remove === "function") {
          this.map.remove();
        }
        this.map = null;
        this.tileLayer = null;
        this.baseLayer = null;
        this.sightingLayer = null;
        this.trainLayer = null;
        this.routeLine = null;
        this.containerId = "";
        this.mapModel = null;
        this.modelKey = "";
        this.viewKey = "";
        this.routeSignature = "";
        this.markerState.forEach((entry) => {
          this.cancelMarkerAnimation(entry);
        });
        this.markerIndex.clear();
        this.markerState.clear();
        this.baseMarkerKeys.clear();
        this.sightingMarkerKeys.clear();
        this.trainMarkerKeys.clear();
        this.focusedEntityKey = "";
        this.openPopupKey = "";
        this.pendingPopupKey = "";
        this.syncingLayers = false;
        this.programmaticViewUntil = 0;
        this.lastLayoutOptions = null;
        this.lastContainerId = "";
        this.pendingRelayout = false;
        this.mapDetailDismissSuppressedUntil = 0;
        this.clearMapGestureStart();
        this.pendingDocumentTap = null;
        this.lastTapProxyAt = 0;
        this.lastMarkerInteractionAt = 0;
        if (this.openPopupKey) {
          clearPublicMapPopupSelection(this.openPopupKey);
        }
        if (isPublicMapMode()) {
          clearMovingMapSelection("map-controller-reset");
        }
        this.containerEl = null;
        this.viewportEl = null;
        this.detailLayerEl = null;
        if (this.hostEl && this.hostEl.parentNode) {
          this.hostEl.parentNode.removeChild(this.hostEl);
        }
        this.hostEl = null;
      },

      sync(containerId, mapModel) {
        const container = document.getElementById(containerId);
        let containerSlots = null;
        if (!container || !mapModel || !window.L) {
          return;
        }
        if (this.lastContainerId && this.lastContainerId !== containerId) {
          this.reset();
        }
        this.ensureMap();
        const nextViewKey = mapModel && mapModel.train
          ? trainMapViewKey(mapModel)
          : networkMapViewKey();
        const viewChanged = this.viewKey !== nextViewKey;
        const savedView = viewChanged ? this.loadStoredView(nextViewKey) : null;
        const config = this.buildConfig(mapModel, {
          view: viewChanged ? savedView : null,
        });
        if (!config.bounds.length) {
          return;
        }
        const hostEl = this.ensureHost();
        containerSlots = this.ensureContainerSlots(container);
        if (hostEl.parentNode !== containerSlots.viewportEl) {
          containerSlots.viewportEl.appendChild(hostEl);
        }
        this.observeContainer(container);
        if (viewChanged) {
          if (this.openPopupKey) {
            clearPublicMapPopupSelection(this.openPopupKey);
          }
          this.focusedEntityKey = "";
          this.openPopupKey = "";
          this.pendingPopupKey = "";
          this.mapDetailDismissSuppressedUntil = 0;
          this.clearMapGestureStart();
          this.renderDetailOverlay();
        }
        const nextModelKey = config.modelKey;
        const modelChanged = this.modelKey !== nextModelKey;
        const shouldRestore = viewChanged && Boolean(savedView);
        const shouldFit = viewChanged && !savedView;

        this.containerId = containerId;
        this.lastContainerId = containerId;
        this.mapModel = mapModel;
        this.modelKey = nextModelKey;
        this.viewKey = config.viewKey;
        if (!viewChanged && !modelChanged) {
          this.scheduleLayout({
            shouldFit: false,
            shouldRestore: false,
            savedView: null,
            bounds: config.bounds,
          });
          return;
        }
        try {
          this.updateLayers(config);
          this.scheduleLayout({
            shouldFit: shouldFit,
            shouldRestore: shouldRestore,
            savedView: savedView,
            bounds: config.bounds,
          });
        } catch (err) {
          this.reset();
          const retryContainer = document.getElementById(containerId);
          let retrySlots = null;
          if (!retryContainer) {
            throw err;
          }
          const retryHost = this.ensureHost();
          retrySlots = this.ensureContainerSlots(retryContainer);
          if (retryHost.parentNode !== retrySlots.viewportEl) {
            retrySlots.viewportEl.appendChild(retryHost);
          }
          this.ensureMap();
          this.containerId = containerId;
          this.lastContainerId = containerId;
          this.mapModel = mapModel;
          this.modelKey = nextModelKey;
          this.viewKey = config.viewKey;
          this.observeContainer(retryContainer);
          this.updateLayers(config);
          this.scheduleLayout({
            shouldFit: shouldFit,
            shouldRestore: shouldRestore,
            savedView: savedView,
            bounds: config.bounds,
          });
        }
      },

      performLayout(options, applyViewChanges) {
        if (!this.map) {
          return;
        }
        const layoutOptions = this.layoutOptions(options);
        this.map.invalidateSize(false);
        if (applyViewChanges) {
          if (layoutOptions.shouldRestore && layoutOptions.savedView) {
            this.applySavedView(layoutOptions.savedView);
          } else if (layoutOptions.shouldFit) {
            this.fitBounds(layoutOptions.bounds);
          }
        }
        this.refreshViewportLayers();
        this.restorePendingPopup();
        applyActiveMapFollow(this);
      },

      scheduleLayoutPasses(options, applyViewChanges) {
        const layoutOptions = this.layoutOptions(options);
        const setTimer = typeof window.setTimeout === "function" ? window.setTimeout.bind(window) : setTimeout;
        this.clearScheduledLayout();
        this.performLayout(layoutOptions, applyViewChanges);
        const followUp = () => {
          this.performLayout(layoutOptions, false);
        };
        if (typeof window.requestAnimationFrame === "function") {
          this.layoutFrame = window.requestAnimationFrame(() => {
            this.layoutFrame = 0;
            followUp();
          });
        } else {
          this.layoutTimeouts.push(setTimer(followUp, 16));
        }
        this.layoutTimeouts.push(setTimer(followUp, 120));
        this.layoutTimeouts.push(setTimer(followUp, 320));
      },

      scheduleLayout(options) {
        this.lastLayoutOptions = this.layoutOptions(options);
        this.pendingRelayout = false;
        this.scheduleLayoutPasses(this.lastLayoutOptions, true);
      },

      requestRelayout() {
        this.pendingRelayout = true;
        if (!this.map) {
          return;
        }
        this.scheduleLayoutPasses(this.lastLayoutOptions, false);
        this.pendingRelayout = false;
      },

      updateLayers(config) {
        if (!this.map) {
          return;
        }
        this.syncingLayers = true;
        this.pendingPopupKey = "";
        try {
          this.reconcileRouteLine(config.polyline);
          this.reconcileMarkerLayer("base", this.baseLayer, config.baseMarkers, "base-marker");
          this.reconcileMarkerLayer("sighting", this.sightingLayer, config.sightingMarkers, "sighting-marker");
          this.reconcileMarkerLayer("train", this.trainLayer, config.trainMarkers || [], "train-marker");
        } finally {
          this.syncingLayers = false;
        }
        if (this.focusedEntityKey && !this.markerEntryByEntityKey(this.focusedEntityKey)) {
          this.focusedEntityKey = "";
        }
        if (this.openPopupKey && !this.markerEntryByEntityKey(this.openPopupKey)) {
          clearPublicMapPopupSelection(this.openPopupKey);
          this.openPopupKey = "";
        }
        this.renderDetailOverlay();
      },

      markerKeysForLayer(layerKey) {
        if (layerKey === "base") {
          return this.baseMarkerKeys;
        }
        if (layerKey === "sighting") {
          return this.sightingMarkerKeys;
        }
        return this.trainMarkerKeys;
      },

      reconcileRouteLine(polyline) {
        const nextPolyline = Array.isArray(polyline) ? polyline : [];
        const nextSignature = materialMapConfigKey({ polyline: nextPolyline });
        if (this.routeSignature === nextSignature) {
          return;
        }
        if (this.routeLine) {
          this.map.removeLayer(this.routeLine);
          this.routeLine = null;
        }
        if (nextPolyline.length > 1) {
          try {
            this.routeLine = window.L.polyline(nextPolyline, { color: "#9f3d22", weight: 4, opacity: 0.9 }).addTo(this.map);
          } catch (err) {
            throw new Error(`route-line: ${err && err.message ? err.message : String(err)}`);
          }
        }
        this.routeSignature = nextSignature;
      },

      reconcileMarkerLayer(layerKey, layer, items, debugKey) {
        const nextItems = Array.isArray(items) ? items : [];
        const markerKeys = this.markerKeysForLayer(layerKey);
        const previousItems = Array.from(markerKeys)
          .map((key) => this.markerState.has(key) ? this.markerState.get(key).item : null)
          .filter(Boolean);
        const plan = markerReconcilePlan(previousItems, nextItems, this.openPopupKey);
        const addKeys = new Set(plan.addKeys);
        const updateKeys = new Set(plan.updateKeys);

        if (!plan.hasChanges) {
          return;
        }

        nextItems.forEach((item) => {
          const markerKey = item && item.markerKey ? item.markerKey : "";
          if (!markerKey) {
            return;
          }
          let entry = this.markerState.get(markerKey) || null;
          if (entry && entry.layerKey !== layerKey) {
            this.removeMarkerEntry(markerKey, { preservePopup: markerKey === this.openPopupKey });
            entry = null;
          }
          if (addKeys.has(markerKey) || !entry) {
            this.addMarker(layerKey, layer, item, debugKey);
            markerKeys.add(markerKey);
            return;
          }
          if (updateKeys.has(markerKey)) {
            this.updateMarkerEntry(entry, item, debugKey);
          }
          entry = this.markerState.get(markerKey);
          if (entry) {
            entry.layerKey = layerKey;
            entry.layer = layer;
            entry.item = item;
            this.markerIndex.set(markerKey, entry.marker);
            markerKeys.add(markerKey);
          }
        });

        plan.removeKeys.forEach((markerKey) => {
          this.removeMarkerEntry(markerKey, { preservePopup: markerKey === this.openPopupKey });
          markerKeys.delete(markerKey);
        });
      },

      addMarker(layerKey, layer, item, debugKey) {
        try {
          const marker = this.buildMarker(item);
          this.bindMarkerInteraction(marker, item);
          if (item.markerKey) {
            this.markerIndex.set(item.markerKey, marker);
            this.markerState.set(item.markerKey, {
              layerKey: layerKey,
              layer: layer,
              marker: marker,
              signature: materialMarkerSignature(item),
              item: item,
              animationFrame: 0,
              positionLatLng: Array.isArray(item.latLng) ? item.latLng.slice() : markerLatLngFromLeaflet(marker),
              targetLatLng: Array.isArray(item.latLng) ? item.latLng.slice() : markerLatLngFromLeaflet(marker),
              positionObservedAt: markerMovementTimestampMs(item),
              lastMovementSyncAt: Date.now(),
            });
          }
          marker.addTo(layer);
        } catch (err) {
          throw new Error(`${debugKey} ${item.latLng.join(",")}: ${err && err.message ? err.message : String(err)}`);
        }
      },

      removeMarkerEntry(markerKey, options) {
        if (!markerKey || !this.markerState.has(markerKey)) {
          return;
        }
        const entry = this.markerState.get(markerKey);
        this.cancelMarkerAnimation(entry);
        if (entry && entry.layer && entry.marker) {
          entry.layer.removeLayer(entry.marker);
        }
        this.markerState.delete(markerKey);
        this.markerIndex.delete(markerKey);
        this.baseMarkerKeys.delete(markerKey);
        this.sightingMarkerKeys.delete(markerKey);
        this.trainMarkerKeys.delete(markerKey);
        if (options && options.preservePopup) {
          this.pendingPopupKey = markerKey;
        }
      },

      updateMarkerEntry(entry, item, debugKey) {
        if (!entry || !entry.marker) {
          this.addMarker(entry ? entry.layerKey : "base", entry ? entry.layer : this.baseLayer, item, debugKey);
          return;
        }
        const signature = materialMarkerSignature(item);
        if (entry.signature === signature) {
          entry.item = item;
          return;
        }
        const marker = entry.marker;
        const previousItem = entry.item;
        const canReuse = entry.item && entry.item.kind === item.kind;
        if (!canReuse) {
          const markerKey = item && item.markerKey ? item.markerKey : "";
          const preservePopup = markerKey && markerKey === this.openPopupKey;
          this.removeMarkerEntry(markerKey, { preservePopup: preservePopup });
          this.addMarker(entry.layerKey, entry.layer, item, debugKey);
          return;
        }
        if (typeof marker.setLatLng === "function" && Array.isArray(item.latLng)) {
          if (item.animateMovement) {
            this.animateMarkerTo(entry, item.latLng, item);
          } else {
            this.cancelMarkerAnimation(entry);
            marker.setLatLng(item.latLng);
            entry.positionLatLng = item.latLng.slice();
            entry.targetLatLng = item.latLng.slice();
            entry.positionObservedAt = markerMovementTimestampMs(item);
            entry.lastMovementSyncAt = Date.now();
          }
        }
        if ((item.kind === "html" || item.kind === "tag") && typeof marker.setIcon === "function") {
          marker.setIcon(this.buildMarkerIcon(item));
          applyTrainMarkerStateTransition(marker, previousItem, item);
        }
        if (item.kind === "circle") {
          if (typeof marker.setRadius === "function") {
            marker.setRadius(item.options && typeof item.options.radius === "number" ? item.options.radius : 100);
          }
          if (typeof marker.setStyle === "function") {
            marker.setStyle(item.options || {});
          }
        }
        if (typeof marker.setZIndexOffset === "function") {
          marker.setZIndexOffset(item.zIndexOffset || 0);
        }
        entry.signature = signature;
        entry.item = item;
      },

      cancelMarkerAnimation(entry) {
        if (!entry || !entry.animationFrame || typeof window.cancelAnimationFrame !== "function") {
          if (entry) {
            entry.animationFrame = 0;
          }
          return;
        }
        window.cancelAnimationFrame(entry.animationFrame);
        entry.animationFrame = 0;
      },

      shouldKeepMarkerCentered(entry, item) {
        const selection = movingMapSelectionState();
        const nextItem = item || (entry && entry.item ? entry.item : null);
        const selectionOptions = nextItem ? this.markerSelectionOptions(nextItem) : {};
        const movingTrainId = normalizeTrainId(selectionOptions && selectionOptions.movingTrainId);
        const markerKey = nextItem && nextItem.markerKey ? String(nextItem.markerKey || "").trim() : "";
        if (selection.paused) {
          return false;
        }
        if (selection.markerKey && markerKey && selection.markerKey === markerKey) {
          return true;
        }
        return Boolean(selection.trainId && movingTrainId && selection.trainId === movingTrainId);
      },

      animateMarkerTo(entry, nextLatLng, item) {
        if (!entry || !entry.marker || !Array.isArray(nextLatLng) || nextLatLng.length !== 2) {
          return;
        }
        const marker = entry.marker;
        const startLatLng = markerLatLngFromLeaflet(marker) || (Array.isArray(entry.positionLatLng) ? entry.positionLatLng.slice() : null);
        const observedAt = markerMovementTimestampMs(item);
        const now = Date.now();
        const durationMs = markerMovementDurationMs(
          entry.positionObservedAt,
          observedAt,
          entry.lastMovementSyncAt ? now - entry.lastMovementSyncAt : 0
        );
        entry.targetLatLng = nextLatLng.slice();
        entry.positionObservedAt = observedAt;
        entry.lastMovementSyncAt = now;
        if (!startLatLng || markerLatLngEqual(startLatLng, nextLatLng) || typeof window.requestAnimationFrame !== "function") {
          this.cancelMarkerAnimation(entry);
          marker.setLatLng(nextLatLng);
          entry.positionLatLng = nextLatLng.slice();
          entry.targetLatLng = nextLatLng.slice();
          if (this.shouldKeepMarkerCentered(entry, item)) {
            this.focusLatLng(entry.positionLatLng, { animate: false });
          }
          return;
        }
        this.cancelMarkerAnimation(entry);
        const startTime = window.performance && typeof window.performance.now === "function"
          ? window.performance.now()
          : now;
        const tick = (frameNow) => {
          const elapsed = frameNow - startTime;
          const progress = Math.min(1, elapsed / durationMs);
          const eased = markerMotionEase(progress);
          const lat = startLatLng[0] + ((nextLatLng[0] - startLatLng[0]) * eased);
          const lng = startLatLng[1] + ((nextLatLng[1] - startLatLng[1]) * eased);
          marker.setLatLng([lat, lng]);
          entry.positionLatLng = [lat, lng];
          if (this.shouldKeepMarkerCentered(entry, item)) {
            this.focusLatLng(entry.positionLatLng, { animate: false });
          }
          if (progress >= 1) {
            entry.animationFrame = 0;
            entry.positionLatLng = nextLatLng.slice();
            entry.targetLatLng = nextLatLng.slice();
            return;
          }
          entry.animationFrame = window.requestAnimationFrame(tick);
        };
        entry.animationFrame = window.requestAnimationFrame(tick);
      },

      restorePendingPopup() {
        if (!this.pendingPopupKey) {
          this.renderDetailOverlay();
          return;
        }
        if (this.markerEntryByEntityKey(this.pendingPopupKey)) {
          this.openPopupKey = this.pendingPopupKey;
          state.publicMapPopupKey = this.pendingPopupKey;
        } else if (this.openPopupKey === this.pendingPopupKey) {
          this.openPopupKey = "";
          clearPublicMapPopupSelection(this.pendingPopupKey);
        }
        this.pendingPopupKey = "";
        if (this.focusedEntityKey && !this.markerEntryByEntityKey(this.focusedEntityKey)) {
          this.focusedEntityKey = "";
        }
        this.renderDetailOverlay();
      },

      markerSelectionOptions(item) {
        if (item && item.interaction && item.interaction.selectionOptions) {
          return item.interaction.selectionOptions;
        }
        return item && item.popupOptions ? item.popupOptions : {};
      },

      focusMarkerInteraction(item, options) {
        const interaction = item && item.interaction ? item.interaction : null;
        const entityKey = interaction && interaction.entityKey
          ? interaction.entityKey
          : item && item.markerKey
            ? item.markerKey
            : "";
        const selectionOptions = this.markerSelectionOptions(item);
        const openDetail = Boolean(options && options.openDetail);
        if (!entityKey) {
          return false;
        }
        this.focusedEntityKey = entityKey;
        if (selectionOptions && selectionOptions.movingMarkerTracking) {
          setMovingMapSelection({
            markerKey: item && item.markerKey ? item.markerKey : entityKey,
            trainId: selectionOptions.movingTrainId || "",
            paused: false,
            reason: openDetail ? "map-detail-open" : "map-focus",
          });
        } else {
          clearMovingMapSelection(openDetail ? "map-detail-open-non-moving" : "map-focus-non-moving");
        }
        this.focusLatLng(item && item.latLng ? item.latLng : null, { animate: Boolean(options && options.animate) });
        if (openDetail) {
          this.openPopupKey = entityKey;
          setPublicMapPopupSelection(entityKey, selectionOptions);
        } else if (this.openPopupKey) {
          clearPublicMapPopupSelection(this.openPopupKey);
          this.openPopupKey = "";
        }
        this.renderDetailOverlay();
        return true;
      },

      handleMarkerInteraction(markerKey) {
        const entry = markerKey ? this.markerState.get(markerKey) : null;
        const item = entry && entry.item ? entry.item : null;
        const interaction = item && item.interaction ? item.interaction : null;
        const entityKey = interaction && interaction.entityKey ? interaction.entityKey : "";
        if (!item || !entityKey) {
          return false;
        }
        this.lastMarkerInteractionAt = Date.now();
        this.suppressNextDetailDismiss();
        return this.focusMarkerInteraction(item, {
          animate: false,
          openDetail: true,
        });
      },

      buildDetailOverlayHTML(item) {
        const detailHTML = item && item.interaction && item.interaction.detailHTML
          ? item.interaction.detailHTML
          : item && item.popupHTML
            ? item.popupHTML
            : "";
        if (!detailHTML) {
          return "";
        }
        return `
          <div class="train-map-detail-card">
            <button type="button" class="train-map-detail-close" data-action="close-map-detail" aria-label="${escapeAttr(t("btn_back"))}">${escapeHtml(t("btn_back"))}</button>
            <div class="train-map-detail-content">${detailHTML}</div>
          </div>
        `;
      },

      renderDetailOverlay() {
        const detailLayerEl = this.detailLayerEl;
        const entry = this.openPopupKey ? this.markerEntryByEntityKey(this.openPopupKey) : null;
        const html = entry && entry.item ? this.buildDetailOverlayHTML(entry.item) : "";
        if (!detailLayerEl) {
          return;
        }
        detailLayerEl.innerHTML = html;
        detailLayerEl.hidden = !html;
      },

      bindMarkerInteraction(marker, item) {
        if (!marker || !item || !item.markerKey || !item.interaction || !item.interaction.entityKey) {
          return;
        }
        const triggerInteraction = () => {
          this.handleMarkerInteraction(item.markerKey);
        };
        marker.on("click", triggerInteraction);
        marker.on("touchend", triggerInteraction);
      },

      buildMarkerIcon(item) {
        if (item.kind === "html") {
          return window.L.divIcon({
            className: item.className || "map-html-marker",
            html: item.html || "",
            iconSize: item.iconSize || [120, 54],
            iconAnchor: item.iconAnchor || [60, 27],
            popupAnchor: item.popupAnchor || [0, -24],
          });
        }
        if (item.kind === "tag") {
          const tagOffset = item.pixelOffset || [0, 0];
          return window.L.divIcon({
            className: "map-tag-marker",
            html: `<span class="map-tag ${escapeAttr(item.bucketClass)}">${escapeHtml(item.tagText)}</span>`,
            iconSize: [56, 34],
            iconAnchor: [28 - tagOffset[0], 17 - tagOffset[1]],
            popupAnchor: [tagOffset[0], tagOffset[1] - 12],
          });
        }
        return null;
      },

      buildMarker(item) {
        if (item.kind === "html") {
          return window.L.marker(item.latLng, {
            zIndexOffset: item.zIndexOffset || 0,
            icon: this.buildMarkerIcon(item),
          });
        }
        if (item.kind === "tag") {
          return window.L.marker(item.latLng, {
            zIndexOffset: item.zIndexOffset || 0,
            icon: this.buildMarkerIcon(item),
          });
        }
        if (item.kind === "circle" && typeof window.L.circle === "function") {
          return window.L.circle(item.latLng, item.options || {});
        }
        return window.L.circleMarker(item.latLng, item.options);
      },

      fitBounds(bounds) {
        if (!this.map) {
          return;
        }
        this.markProgrammaticView();
        this.map.fitBounds(window.L.latLngBounds(bounds).pad(0.22), { animate: false });
        this.saveCurrentView();
      },

      handlePotentialDetailDismiss(insideDetail) {
        if (!this.hasOpenPopup() || insideDetail || this.isDetailDismissSuppressed()) {
          return false;
        }
        this.closePopup();
        return true;
      },

      handleDocumentClick(event) {
        const target = event && event.target && typeof event.target.closest === "function"
          ? event.target
          : null;
        if (!this.hasOpenPopup() || !target) {
          return false;
        }
        return this.handlePotentialDetailDismiss(Boolean(target.closest(".train-map-detail-card")));
      },

      hasOpenPopup() {
        return Boolean(this.openPopupKey);
      },

      closePopup() {
        if (this.openPopupKey) {
          clearPublicMapPopupSelection(this.openPopupKey);
        }
        this.openPopupKey = "";
        this.renderDetailOverlay();
      },
    };
  }

  function trainMapViewKey(mapData) {
    const trainId = mapData && mapData.train && mapData.train.id ? mapData.train.id : "unknown";
    return `${usesPublicTrainMap() ? "train-public" : "train"}:${trainId}`;
  }

  function networkMapViewKey() {
    return usesPublicNetworkMap() ? "network:public-network-map" : "network:mini-app";
  }

  function coordinateLatLng(item) {
    const latitude = typeof item === "object" && item ? Number(item.latitude) : NaN;
    const longitude = typeof item === "object" && item ? Number(item.longitude) : NaN;
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) {
      return null;
    }
    return [latitude, longitude];
  }

  function pointListLatLng(points) {
    return (Array.isArray(points) ? points : []).map(pointToLatLng).filter(Boolean);
  }

  function locatedTrainStops(mapData) {
    return (Array.isArray(mapData && mapData.stops) ? mapData.stops : [])
      .map((stop, index) => ({ stop, index, latLng: coordinateLatLng(stop) }))
      .filter((entry) => entry.latLng);
  }

  function locatedNetworkStations(mapData) {
    if (mapData && mapData.liveOnly) {
      return [];
    }
    return (Array.isArray(mapData && mapData.stations) ? mapData.stations : [])
      .map((station) => ({ station, latLng: coordinateLatLng(station) }))
      .filter((entry) => entry.latLng);
  }

  function trainStopMarkerKey(trainId, stop, index) {
    const stationId = String(stop && (stop.stationId || stop.stationName) || "stop").trim() || "stop";
    return `train-stop:${trainId || "unknown"}:${stationId}:${index}`;
  }

  function networkStationMarkerKey(station) {
    const stationId = String(station && (station.id || station.name) || "station").trim() || "station";
    return `network-station:${stationId}`;
  }

  function trainRoutePolyline(mapData, liveItem) {
    const externalPolyline = pointListLatLng(liveItem && liveItem.external && liveItem.external.polyline);
    if (externalPolyline.length > 1) {
      return externalPolyline;
    }
    return locatedTrainStops(mapData).map((entry) => entry.latLng);
  }

  function mapConfigBounds(baseMarkers, sightingMarkers, trainMarkers, polyline) {
    return []
      .concat((Array.isArray(baseMarkers) ? baseMarkers : []).map((item) => item.latLng))
      .concat((Array.isArray(sightingMarkers) ? sightingMarkers : []).map((item) => item.latLng))
      .concat((Array.isArray(trainMarkers) ? trainMarkers : []).map((item) => item.latLng))
      .concat(Array.isArray(polyline) ? polyline : [])
      .filter((latLng) => Array.isArray(latLng) && latLng.length === 2);
  }

  function buildTrainStopMarkerConfigs(mapData, liveItems, hasLiveTrainMarker, viewport) {
    const trainId = normalizeTrainId(mapData && mapData.train && mapData.train.id);
    const allLiveItems = Array.isArray(liveItems) ? liveItems : [];
    return locatedTrainStops(mapData).map((entry) => {
      const stop = entry.stop;
      const markerKey = trainStopMarkerKey(trainId, stop, entry.index);
      const stopLiveItems = allLiveItems.filter((item) => liveItemTouchesStation(item, stop.stationName || stop.stationId));
      const popupHTML = buildTrainStopPopupHTML(stop, entry.index, mapData, stopLiveItems, hasLiveTrainMarker);
      return buildStationMarkerConfig({
        markerKey,
        name: stop.stationName || stop.stationId || "",
        latLng: entry.latLng,
        sightings: stopSightings(stop, mapData),
        liveItems: stopLiveItems,
        popupHTML,
        popupOptions: {},
      }, viewport);
    });
  }

  function buildNetworkStationMarkerConfigs(mapData, liveItems, viewport) {
    const activity = buildStationActivityMap(mapData, liveItems);
    return locatedNetworkStations(mapData).map((entry) => {
      const station = entry.station;
      const key = stationKeyValue(station.name || station.id);
      const bucket = activity.get(key) || emptyStationActivity(station.name || station.id || "", station.id || "");
      bucket.name = bucket.name || station.name || station.id || "";
      bucket.stationId = bucket.stationId || station.id || "";
      const incidents = activeStationIncidentsFor(station, bucket);
      const popupHTML = buildStationPopupHTML(station, bucket, {
        allowReports: mapReportsEnabled(),
        incidents,
      });
      return buildStationMarkerConfig({
        markerKey: networkStationMarkerKey(station),
        name: station.name || station.id || "",
        latLng: entry.latLng,
        sightings: bucket.sightings,
        liveItems: bucket.liveItems,
        incidents,
        popupHTML,
        popupOptions: {},
      }, viewport);
    });
  }

  function buildTrainMapConfig(mapData, viewport) {
    const liveItem = buildSelectedTrainLiveItem(mapData);
    const trainMarkers = liveItem && liveItem.external && liveItem.external.position
      ? [buildLiveTrainMarkerConfig(liveItem, viewport)]
      : [];
    const liveItems = liveItem ? [liveItem] : [];
    const baseMarkers = buildTrainStopMarkerConfigs(mapData, liveItems, trainMarkers.length > 0, viewport);
    const polyline = trainRoutePolyline(mapData, liveItem);
    const sightingMarkers = shouldShowSightingTags(viewport && viewport.zoom)
      ? buildSightingMarkers(mapData && mapData.stationSightings, buildCoordinateLookup(Array.isArray(mapData && mapData.stops) ? mapData.stops : [], "stationId"))
      : [];
    const bounds = mapConfigBounds(baseMarkers, sightingMarkers, trainMarkers, polyline);
    const config = {
      viewKey: trainMapViewKey(mapData),
      bounds: bounds,
      polyline: polyline,
      baseMarkers: baseMarkers,
      sightingMarkers: sightingMarkers,
      trainMarkers: trainMarkers,
    };
    return {
      modelKey: materialMapConfigKey(config),
      viewKey: config.viewKey,
      bounds: config.bounds,
      polyline: config.polyline,
      baseMarkers: config.baseMarkers,
      sightingMarkers: config.sightingMarkers,
      trainMarkers: config.trainMarkers,
    };
  }

  function buildNetworkMapConfig(mapData, viewport) {
    const liveItems = buildMapVisibleLiveItems(serviceDayTrainItemsForMapMatching(), activeNetworkMapSightings(mapData));
    const trainMarkers = liveItems
      .filter((item) => item.external && item.external.position)
      .map((item) => buildLiveTrainMarkerConfig(item, viewport));
    const baseMarkers = buildNetworkStationMarkerConfigs(mapData, liveItems, viewport);
    const sightingMarkers = (shouldShowSightingTags(viewport && viewport.zoom)
      ? buildSightingMarkers(activeNetworkMapSightings(mapData), buildCoordinateLookup(Array.isArray(mapData && mapData.stations) ? mapData.stations : [], "id"))
      : []).concat(activeAreaIncidentMarkers());
    const bounds = mapConfigBounds(baseMarkers, sightingMarkers, trainMarkers, []);
    const config = {
      viewKey: networkMapViewKey(),
      bounds: bounds,
      polyline: [],
      baseMarkers: baseMarkers,
      sightingMarkers: sightingMarkers,
      trainMarkers: trainMarkers,
    };
    return {
      modelKey: materialMapConfigKey(config),
      viewKey: config.viewKey,
      bounds: config.bounds,
      polyline: config.polyline,
      baseMarkers: config.baseMarkers,
      sightingMarkers: config.sightingMarkers,
      trainMarkers: config.trainMarkers,
    };
  }

  function externalFeedAPI() {
    return window.TrainExternalFeed || null;
  }

  function pointToLatLng(point) {
    if (!point || typeof point.lat !== "number" || typeof point.lng !== "number") {
      return null;
    }
    return [point.lat, point.lng];
  }

  function stationKeyValue(value) {
    if (externalFeedAPI() && typeof externalFeedAPI().normalizeStationKey === "function") {
      return externalFeedAPI().normalizeStationKey(value);
    }
    return String(value || "")
      .normalize("NFKD")
      .replace(/[\u0300-\u036f]/g, "")
      .replace(/\*/g, "")
      .replace(/[^a-zA-Z0-9]+/g, " ")
      .trim()
      .toLowerCase();
  }

  function extractTrainNumberFromValue(value) {
    const match = String(value || "").match(/(\d{3,5})$/);
    return match ? match[1] : "";
  }

  function stableExternalTrainIdentity(raw) {
    const api = externalFeedAPI();
    if (!api || typeof api.stableExternalTrainIdentity !== "function") {
      return "";
    }
    return api.stableExternalTrainIdentity(raw);
  }

  function liveTrainMarkerKeyFromIdentity(identity) {
    return identity ? `live-train:${identity}` : "";
  }

  function liveTrainMarkerKeyForExternal(raw) {
    return liveTrainMarkerKeyFromIdentity(stableExternalTrainIdentity(raw));
  }

  function comparableLocalTrainData(raw) {
    const train = raw && raw.trainCard && raw.trainCard.train
      ? raw.trainCard.train
      : raw && raw.train
        ? raw.train
        : raw;
    if (!train) {
      return null;
    }
    const trainNumber = String(train.trainNumber || (raw && raw.trainNumber) || extractTrainNumberFromValue(train.id)).trim();
    return {
      routeId: train.routeId || (raw && raw.routeId) || "",
      serviceDate: train.serviceDate || (raw && raw.serviceDate) || "",
      trainNumber: trainNumber,
      origin: train.fromStation || (raw && raw.fromStation) || (raw && raw.origin) || (raw && raw.originName) || "",
      destination: train.toStation || (raw && raw.toStation) || (raw && raw.destination) || (raw && raw.destinationName) || "",
      departureTime: train.departureAt || train.departure || (raw && raw.departureTime) || (raw && raw.departureAt) || "",
    };
  }

  function liveTrainMarkerKeyForLocal(raw) {
    const comparable = comparableLocalTrainData(raw);
    if (!comparable) {
      return "";
    }
    return liveTrainMarkerKeyForExternal(comparable);
  }

  function buildSelectedTrainLiveItem(mapData) {
    if (!mapData || !mapData.train) {
      return null;
    }
    const locals = [];
    if (state.mapTrainDetail) {
      locals.push(state.mapTrainDetail);
    }
    locals.push(mapData.train);
    const items = buildMapVisibleLiveItems(locals, mapData.stationSightings);
    const exact = items.find((item) => item.trainId === mapData.train.id);
    if (exact) {
      return exact;
    }
    const fallbackMarkerKeys = [
      state.publicMapSelectedMarkerKey || "",
      liveTrainMarkerKeyForLocal(state.mapTrainDetail),
      liveTrainMarkerKeyForLocal(mapData.train),
    ].filter(Boolean);
    for (const markerKey of fallbackMarkerKeys) {
      const candidate = items.find((item) => item.markerKey === markerKey);
      if (candidate) {
        return candidate;
      }
    }
    return null;
  }

  function createPreparedLocalTrainMatcher(localItems, feedApi) {
    const api = feedApi || externalFeedAPI();
    if (!api) {
      return null;
    }
    if (typeof api.createLocalTrainMatcher === "function") {
      return api.createLocalTrainMatcher(localItems || []);
    }
    if (typeof api.matchLocalTrain === "function") {
      return (externalTrain) => api.matchLocalTrain(externalTrain, localItems || []);
    }
    return null;
  }

  function buildExternalRouteLookup(routes) {
    const lookup = {
      byRouteId: new Map(),
      byTrainAndDate: new Map(),
    };
    (Array.isArray(routes) ? routes : []).forEach((route) => {
      const routeId = route && route.routeId ? String(route.routeId) : "";
      if (routeId && !lookup.byRouteId.has(routeId)) {
        lookup.byRouteId.set(routeId, route);
      }
      const trainKey = `${String(route && route.trainNumber || "")}\n${String(route && route.serviceDate || "")}`;
      if (!lookup.byTrainAndDate.has(trainKey)) {
        lookup.byTrainAndDate.set(trainKey, route);
      }
    });
    return lookup;
  }

  function buildFallbackSightingIndex(fallbackSightings) {
    const index = new Map();
    (Array.isArray(fallbackSightings) ? fallbackSightings : []).forEach((entry) => {
      const trainId = entry && entry.matchedTrainInstanceId ? String(entry.matchedTrainInstanceId) : "";
      if (!trainId) {
        return;
      }
      if (!index.has(trainId)) {
        index.set(trainId, []);
      }
      index.get(trainId).push(entry);
    });
    return index;
  }

  function serviceDayTrainItemsForMapMatching() {
    if (Array.isArray(state.publicServiceDayTrains) && state.publicServiceDayTrains.length) {
      return state.publicServiceDayTrains;
    }
    return Array.isArray(state.publicDashboardAll) ? state.publicDashboardAll : [];
  }

  function buildMatchedLiveItems(localItems, fallbackSightings) {
    const feedApi = externalFeedAPI();
    const matchLocalTrain = createPreparedLocalTrainMatcher(localItems, feedApi);
    if (typeof matchLocalTrain !== "function") {
      return [];
    }
    const feedLiveTrains = Array.isArray(state.externalFeed.liveTrains) ? state.externalFeed.liveTrains : [];
    const routeLookup = buildExternalRouteLookup(state.externalFeed.routes);
    const fallbackSightingIndex = buildFallbackSightingIndex(fallbackSightings);
    return feedLiveTrains.map((external) => {
      const mergedExternal = mergeExternalTrain(external, findExternalRoute(external, routeLookup));
      const matchInfo = matchLocalTrain(mergedExternal);
      const localMatch = matchInfo && matchInfo.match ? matchInfo.match : null;
      const trainId = matchInfo && matchInfo.localTrainId ? matchInfo.localTrainId : localTrainId(localMatch);
      const markerKey = liveTrainMarkerKeyForExternal(mergedExternal);
      const item = {
        external: mergedExternal,
        localMatch: localMatch,
        matchInfo: matchInfo,
        markerKey: markerKey,
        trainId: trainId,
        status: localTrainStatus(localMatch),
        timeline: localTrainTimeline(localMatch),
        sightings: localTrainSightings(localMatch, trainId, fallbackSightings, fallbackSightingIndex),
      };
      item.incidents = activeTrainIncidentsForItem(item);
      return item;
    }).filter((item) => item.external && item.external.position);
  }

  function buildMapVisibleLiveItems(localItems, fallbackSightings) {
    return buildMatchedLiveItems(localItems, fallbackSightings)
      .filter(isMapVisibleLiveItem);
  }

  function isMapVisibleLiveItem(item) {
    return Boolean(item && item.external && item.external.position && isMapVisibleLiveTrain(item.external));
  }

  function isMapVisibleLiveTrain(external) {
    const gpsClass = liveTrainGpsClass(external);
    return gpsClass === "gps-fresh" || gpsClass === "gps-warm" || gpsClass === "gps-projection";
  }

  function findExternalRoute(external, routeLookup) {
    const lookup = routeLookup || buildExternalRouteLookup(state.externalFeed.routes);
    const routeId = external && external.routeId ? String(external.routeId) : "";
    if (routeId) {
      const exact = lookup.byRouteId.get(routeId) || null;
      if (exact) {
        return exact;
      }
    }
    const trainNumber = external && external.trainNumber ? String(external.trainNumber) : "";
    const serviceDate = external && external.serviceDate ? String(external.serviceDate) : "";
    return lookup.byTrainAndDate.get(`${trainNumber}\n${serviceDate}`) || null;
  }

  function mergeExternalTrain(external, route) {
    const merged = Object.assign({}, route || {}, external || {});
    merged.stops = Array.isArray(external && external.stops) && external.stops.length
      ? external.stops
      : Array.isArray(route && route.stops)
        ? route.stops
        : [];
    merged.polyline = Array.isArray(external && external.polyline) && external.polyline.length
      ? external.polyline
      : Array.isArray(route && route.polyline)
        ? route.polyline
        : [];
    merged.origin = merged.origin || (route && route.origin) || "";
    merged.destination = merged.destination || (route && route.destination) || "";
    merged.originKey = merged.originKey || (route && route.originKey) || stationKeyValue(merged.origin);
    merged.destinationKey = merged.destinationKey || (route && route.destinationKey) || stationKeyValue(merged.destination);
    return merged;
  }

  function localTrainId(item) {
    if (!item) {
      return "";
    }
    if (item.train && item.train.id) {
      return item.train.id;
    }
    if (item.trainCard && item.trainCard.train && item.trainCard.train.id) {
      return item.trainCard.train.id;
    }
    return item.id || "";
  }

  function externalTrainInstanceId(external) {
    const serviceDate = String(external && external.serviceDate || "").trim();
    const trainNumber = String(external && external.trainNumber || "").trim();
    if (!/^\d{4}-\d{2}-\d{2}$/.test(serviceDate) || !/^[0-9A-Za-z-]+$/.test(trainNumber)) {
      return "";
    }
    return `${serviceDate}-train-${trainNumber}`;
  }

  function localTrainStatus(item) {
    if (!item) {
      return null;
    }
    if (item.trainCard && item.trainCard.status) {
      return item.trainCard.status;
    }
    return item.status || null;
  }

  function localTrainTimeline(item) {
    return item && Array.isArray(item.timeline) ? item.timeline : [];
  }

  function localTrainSightings(item, trainId, fallbackSightings, fallbackSightingIndex) {
    if (item && Array.isArray(item.stationSightings) && item.stationSightings.length) {
      return item.stationSightings;
    }
    if (!trainId) {
      return [];
    }
    if (fallbackSightingIndex instanceof Map && fallbackSightingIndex.has(trainId)) {
      return fallbackSightingIndex.get(trainId).slice();
    }
    const sightings = Array.isArray(fallbackSightings) ? fallbackSightings : [];
    return sightings.filter((entry) => entry && entry.matchedTrainInstanceId === trainId);
  }

  function liveItemTouchesStation(item, stationName) {
    const stationKey = stationKeyValue(stationName);
    const external = item && item.external ? item.external : null;
    if (!external) {
      return false;
    }
    if (external.currentStop && stationKeyValue(external.currentStop.title) === stationKey) {
      return true;
    }
    if (external.nextStop && stationKeyValue(external.nextStop.title) === stationKey) {
      return true;
    }
    return false;
  }

  function buildStationActivityMap(mapData, liveItems) {
    const activity = new Map();
    const sightings = activeNetworkMapSightings(mapData);
    sightings.forEach((item) => {
      const key = stationKeyValue(item.stationName || item.stationId);
      const bucket = ensureStationActivity(activity, key, item.stationName || item.stationId, item.stationId);
      bucket.sightings.push(item);
    });

    const liveIndex = new Map();
    liveItems.forEach((item) => {
      if (item.external && item.external.routeId) {
        liveIndex.set(`route:${item.external.routeId}`, item);
      }
      if (item.external && item.external.trainNumber) {
        liveIndex.set(`train:${item.external.trainNumber}:${item.external.serviceDate || ""}`, item);
      }
    });

    const activeStops = Array.isArray(state.externalFeed.activeStops) ? state.externalFeed.activeStops : [];
    activeStops.forEach((entry) => {
      if (!entry || !entry.hasTrain) {
        return;
      }
      const key = stationKeyValue(entry.title || entry.stationId);
      const bucket = ensureStationActivity(activity, key, entry.title || entry.stationId, entry.stationId);
      const liveItem = liveIndex.get(`route:${entry.routeId}`) || liveIndex.get(`train:${entry.trainNumber}:${entry.serviceDate || ""}`) || null;
      if (liveItem) {
        pushStationLiveItem(bucket, liveItem);
      }
    });

    liveItems.forEach((item) => {
      [item.external && item.external.currentStop, item.external && item.external.nextStop].forEach((stop) => {
        if (!stop || !stop.title) {
          return;
        }
        const key = stationKeyValue(stop.title);
        const bucket = ensureStationActivity(activity, key, stop.title, stop.stationId);
        pushStationLiveItem(bucket, item);
      });
    });

    return activity;
  }

  function ensureStationActivity(activity, key, name, stationId) {
    if (!activity.has(key)) {
      activity.set(key, emptyStationActivity(name, stationId));
    }
    const bucket = activity.get(key);
    if (!bucket.name && name) {
      bucket.name = name;
    }
    if (!bucket.stationId && stationId) {
      bucket.stationId = stationId;
    }
    return bucket;
  }

  function emptyStationActivity(name, stationId) {
    return {
      name: name || "",
      stationId: stationId || "",
      sightings: [],
      liveItems: [],
      liveKeys: new Set(),
    };
  }

  function pushStationLiveItem(bucket, liveItem) {
    const key = liveItem.markerKey || liveItem.trainId || `${liveItem.external.routeId}:${liveItem.external.trainNumber}`;
    if (bucket.liveKeys.has(key)) {
      return;
    }
    bucket.liveKeys.add(key);
    bucket.liveItems.push(liveItem);
  }

  function buildStationMarkerConfig(options, viewport) {
    const sightings = Array.isArray(options.sightings) ? options.sightings : [];
    const liveItems = Array.isArray(options.liveItems) ? options.liveItems : [];
    const incidents = Array.isArray(options.incidents) ? options.incidents : [];
    const profile = stationMarkerProfile(viewport);
    const markerKey = options.markerKey || "";
    const popupHTML = options.popupHTML || "";
    return {
      kind: "html",
      className: "map-html-marker",
      markerKey: markerKey,
      latLng: options.latLng,
      html: buildStationMarkerHTML(options.name, sightings.length, liveItems, profile, incidents.length),
      incidentIds: incidents.map((item) => item.id).filter(Boolean),
      iconSize: profile.iconSize,
      iconAnchor: profile.iconAnchor,
      popupAnchor: profile.popupAnchor,
      popupHTML: popupHTML,
      popupOptions: options.popupOptions,
      interaction: {
        entityKey: markerKey,
        detailHTML: popupHTML,
        selectionOptions: options.popupOptions || {},
      },
    };
  }

  function buildLiveTrainMarkerConfig(item, viewport) {
    const profile = liveTrainMarkerProfile(viewport);
    const popupHTML = buildTrainPopupHTML(item);
    const gpsClass = liveTrainGpsClass(item.external);
    const incidents = Array.isArray(item && item.incidents) ? item.incidents : [];
    const crewActive = hasCrewActivity(item.status) || incidents.length > 0;
    return {
      kind: "html",
      className: "map-html-marker",
      markerKey: item.markerKey || "",
      gpsClass: gpsClass,
      crewActive: crewActive,
      latLng: pointToLatLng(item.external.position),
      animateMovement: false,
      movementObservedAt: liveTrainDisplayUpdatedAt(item.external),
      html: buildLiveTrainMarkerHTML(item, profile, gpsClass, crewActive),
      incidentIds: incidents.map((incident) => incident.id).filter(Boolean),
      iconSize: profile.iconSize,
      iconAnchor: profile.iconAnchor,
      popupAnchor: profile.popupAnchor,
      zIndexOffset: 1300,
      popupHTML: popupHTML,
      popupOptions: {
        movingMarkerTracking: true,
        movingTrainId: item.trainId || "",
      },
      interaction: {
        entityKey: item.markerKey || "",
        detailHTML: popupHTML,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: item.trainId || "",
        },
      },
    };
  }

  function buildStationMarkerHTML(name, sightingCount, liveItems, profile, incidentCount) {
    const markerProfile = profile || stationMarkerProfile({ zoom: MAP_DEFAULT_VIEW_ZOOM, visibleHeightMeters: Infinity });
    const activeIncidentCount = Number(incidentCount) || 0;
    const crewCount = liveItems.filter((item) => hasCrewActivity(item.status)).length;
    const liveCount = liveItems.length;
    const stateClass = crewCount > 0
      ? "crew-active"
      : activeIncidentCount > 0
        ? "crew-active"
        : liveCount > 0
          ? "live-active"
          : sightingCount > 0
            ? "sighting-active"
            : "idle";
    const markerLabel = [
      name || "Station",
      activeIncidentCount ? `${activeIncidentCount} ${t("app_section_incidents")}` : "",
      liveCount ? `${liveCount} ${t("app_map_popup_live_now")}` : "",
      sightingCount ? `${sightingCount} ${t("app_map_popup_recent_sightings")}` : "",
    ].filter(Boolean).join(" • ");
    return `
      <div
        class="map-station-marker map-station-marker-${escapeAttr(markerProfile.tier)} ${escapeAttr(stateClass)}"
        style="--map-station-size:${markerProfile.markerSize}px;--map-station-core-size:${markerProfile.coreSize}px"
        title="${escapeAttr(markerLabel)}"
        aria-label="${escapeAttr(markerLabel)}"
      >
        ${(activeIncidentCount || sightingCount) ? `<span class="map-marker-count">!</span>` : ""}
      </div>
    `;
  }

  function buildLiveTrainMarkerHTML(item, profile, gpsClassOverride, crewActiveOverride) {
    const markerProfile = profile || liveTrainMarkerProfile({ zoom: MAP_DEFAULT_VIEW_ZOOM });
    const number = item.external && item.external.trainNumber ? item.external.trainNumber : trainNumberLabel(item.trainId);
    const gpsClass = gpsClassOverride || liveTrainGpsClass(item.external);
    const crewActive = typeof crewActiveOverride === "boolean" ? crewActiveOverride : hasCrewActivity(item.status);
    const reporterCount = item.status && typeof item.status.uniqueReporters === "number" ? item.status.uniqueReporters : 0;
    const incidentCount = Array.isArray(item && item.incidents) ? item.incidents.length : 0;
    const markerLabel = [
      number,
      gpsClass.replace("gps-", ""),
      incidentCount ? `${incidentCount} ${t("app_section_incidents")}` : "",
      reporterCount ? `${reporterCount} crew` : "",
    ].filter(Boolean).join(" • ");
    return `
      <div
        class="map-train-marker map-train-marker-${escapeAttr(markerProfile.tier)} ${escapeAttr(gpsClass)} ${crewActive ? "crew-active" : "crew-idle"}"
        style="--map-train-height:${markerProfile.markerHeight}px;--map-train-min-width:${markerProfile.markerMinWidth}px;--map-train-padding-x:${markerProfile.markerPaddingX}px"
        title="${escapeAttr(markerLabel)}"
        aria-label="${escapeAttr(markerLabel)}"
      >
        <span class="map-marker-label">${escapeHtml(number)}</span>
        ${(incidentCount || reporterCount) ? `<span class="map-marker-count">!</span>` : ""}
      </div>
    `;
  }

  function popupActionTrainId(item) {
    if (!item) {
      return "";
    }
    return String(item.trainId || localTrainId(item.localMatch) || externalTrainInstanceId(item.external) || "").trim();
  }

  function popupActionTrain(item) {
    const localMatch = item && item.localMatch ? item.localMatch : null;
    if (!localMatch) {
      return null;
    }
    if (localMatch.trainCard && localMatch.trainCard.train) {
      return localMatch.trainCard.train;
    }
    if (localMatch.train) {
      return localMatch.train;
    }
    return localMatch;
  }

  function popupActionStationId(item) {
    const localMatch = item && item.localMatch ? item.localMatch : null;
    if (!localMatch) {
      return "";
    }
    if (localMatch.boardingStationId) {
      return localMatch.boardingStationId;
    }
    if (localMatch.stationId) {
      return localMatch.stationId;
    }
    return "";
  }

  function stationCheckinEnabled() {
    return cfg.stationCheckinEnabled !== false;
  }

  function selectedStationContextStationId(trainId) {
    if (!stationCheckinEnabled()) {
      return "";
    }
    const normalizedTrainId = normalizeTrainId(trainId);
    const station = state.selectedStation;
    if (!normalizedTrainId || !station || !station.id) {
      return "";
    }
    const departures = Array.isArray(state.stationDepartures) ? state.stationDepartures : [];
    const matchesSelectedStation = departures.some((item) => resolveTrainIdFromPayload(item) === normalizedTrainId);
    return matchesSelectedStation ? String(station.id || "").trim() : "";
  }

  function normalizeInferredStationId(value, nameFallback) {
    const explicit = String(value || "").trim();
    if (explicit) {
      return explicit;
    }
    const fallbackName = String(nameFallback || "").trim();
    return fallbackName ? stationKeyValue(fallbackName) : "";
  }

  function haversineDistanceMeters(left, right) {
    if (!Array.isArray(left) || left.length !== 2 || !Array.isArray(right) || right.length !== 2) {
      return Number.POSITIVE_INFINITY;
    }
    const earthRadiusMeters = 6371000;
    const lat1 = left[0] * Math.PI / 180;
    const lat2 = right[0] * Math.PI / 180;
    const deltaLat = (right[0] - left[0]) * Math.PI / 180;
    const deltaLng = (right[1] - left[1]) * Math.PI / 180;
    const sinLat = Math.sin(deltaLat / 2);
    const sinLng = Math.sin(deltaLng / 2);
    const a = sinLat * sinLat + Math.cos(lat1) * Math.cos(lat2) * sinLng * sinLng;
    const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
    return earthRadiusMeters * c;
  }

  function nearestCoordinateBearingStationId(latLng, candidates) {
    const items = Array.isArray(candidates) ? candidates : [];
    let bestStationId = "";
    let bestDistance = Number.POSITIVE_INFINITY;
    items.forEach((candidate) => {
      const latitude = typeof candidate.latitude === "number" ? candidate.latitude : null;
      const longitude = typeof candidate.longitude === "number" ? candidate.longitude : null;
      if (latitude === null || longitude === null) {
        return;
      }
      const stationId = normalizeInferredStationId(candidate.stationId || candidate.id, candidate.stationName || candidate.name);
      if (!stationId) {
        return;
      }
      const distance = haversineDistanceMeters(latLng, [latitude, longitude]);
      if (distance < bestDistance) {
        bestDistance = distance;
        bestStationId = stationId;
      }
    });
    return bestStationId;
  }

  function coordinateBearingTrainStopsForInference(trainId, item) {
    const normalizedTrainId = normalizeTrainId(trainId);
    const candidates = [];
    const seen = new Set();
    function appendStops(stops) {
      (Array.isArray(stops) ? stops : []).forEach((stop) => {
        const latitude = typeof stop.latitude === "number" ? stop.latitude : null;
        const longitude = typeof stop.longitude === "number" ? stop.longitude : null;
        if (latitude === null || longitude === null) {
          return;
        }
        const stationId = normalizeInferredStationId(stop.stationId, stop.stationName || stop.name);
        if (!stationId) {
          return;
        }
        const key = `${stationId}:${latitude}:${longitude}`;
        if (seen.has(key)) {
          return;
        }
        seen.add(key);
        candidates.push({
          stationId,
          stationName: stop.stationName || stop.name || "",
          latitude,
          longitude,
        });
      });
    }
    const localMatch = item && item.localMatch ? item.localMatch : null;
    appendStops(localMatch && localMatch.stops);
    if (state.mapData && resolveTrainIdFromPayload(state.mapData) === normalizedTrainId) {
      appendStops(state.mapData.stops);
    }
    return candidates;
  }

  function networkInferenceStations() {
    return Array.isArray(state.networkMapData && state.networkMapData.stations)
      ? state.networkMapData.stations
      : [];
  }

  function matchingActiveStopEntry(item) {
    const external = item && item.external ? item.external : null;
    if (!external) {
      return null;
    }
    const activeStops = Array.isArray(state.externalFeed && state.externalFeed.activeStops)
      ? state.externalFeed.activeStops
      : [];
    const routeId = String(external.routeId || "").trim();
    const trainNumber = String(external.trainNumber || "").trim();
    const serviceDate = String(external.serviceDate || "").trim();
    return activeStops.find((entry) => {
      if (!entry || !entry.hasTrain) {
        return false;
      }
      if (routeId && String(entry.routeId || "").trim() === routeId) {
        return true;
      }
      return Boolean(
        trainNumber &&
        String(entry.trainNumber || "").trim() === trainNumber &&
        String(entry.serviceDate || "").trim() === serviceDate
      );
    }) || null;
  }

  function inferredStationIdFromLiveHints(item) {
    const external = item && item.external ? item.external : null;
    const candidates = [
      external && external.currentStop,
      external && external.nextStop,
      matchingActiveStopEntry(item),
    ];
    for (const candidate of candidates) {
      const stationId = normalizeInferredStationId(candidate && candidate.stationId, candidate && (candidate.title || candidate.stationName || candidate.name));
      if (stationId) {
        return stationId;
      }
    }
    return "";
  }

  function inferPopupCheckInStationId(trainId, item) {
    const latLng = pointToLatLng(item && item.external && item.external.position);
    if (latLng) {
      const trainStopStationId = nearestCoordinateBearingStationId(latLng, coordinateBearingTrainStopsForInference(trainId, item));
      if (trainStopStationId) {
        return trainStopStationId;
      }
      const networkStationId = nearestCoordinateBearingStationId(latLng, networkInferenceStations());
      if (networkStationId) {
        return networkStationId;
      }
    }
    return inferredStationIdFromLiveHints(item);
  }

  function resolvedPopupCheckInStationId(trainId, explicitStationId, item) {
    if (!stationCheckinEnabled()) {
      return "";
    }
    const normalizedExplicitStationId = String(explicitStationId || "").trim();
    if (normalizedExplicitStationId) {
      return normalizedExplicitStationId;
    }
    return inferPopupCheckInStationId(trainId, item);
  }

  function resolvePopupCheckInAction(trainId, eligibleUntilAts, explicitStationId, item) {
    return null;
  }

  function renderPopupActionButton(action) {
    if (!action) {
      return "";
    }
    return `
      <button
        class="${escapeAttr(action.className)}"
        data-action="${escapeAttr(action.action)}"
        data-train-id="${escapeAttr(action.trainId)}"
        data-station-id="${escapeAttr(action.stationId || "")}"
        data-signal="${escapeAttr(action.signal || "")}"
      >${escapeHtml(action.label)}</button>
    `;
  }

  function renderPopupActionButtons(actions) {
    return (Array.isArray(actions) ? actions : []).map(renderPopupActionButton).join("");
  }

  function popupActionEligibleUntil(item) {
    const train = popupActionTrain(item);
    const external = item && item.external ? item.external : null;
    return (
      (train && (train.arrivalAt || train.departureAt)) ||
      (external && external.departureTime) ||
      (external && external.nextStop && external.nextStop.departureTime) ||
      ""
    );
  }

  function popupTrainReportActions(trainId) {
    if (!canReportFromCurrentMapMode() || !state.authenticated || !trainId) {
      return [];
    }
    return [
      {
        className: "primary small",
        action: "popup-report-train-signal",
        trainId: trainId,
        signal: "INSPECTION_STARTED",
        label: t("btn_report_started"),
      },
      {
        className: "secondary small",
        action: "popup-report-train-signal",
        trainId: trainId,
        signal: "INSPECTION_IN_MY_CAR",
        label: t("btn_report_in_car"),
      },
      {
        className: "warning small",
        action: "popup-report-train-signal",
        trainId: trainId,
        signal: "INSPECTION_ENDED",
        label: t("btn_report_ended"),
      },
    ];
  }

  function resolveTrainPopupActions(item) {
    const trainId = popupActionTrainId(item);
    if (!canReportFromCurrentMapMode() || !state.authenticated || !item || !trainId) {
      return [];
    }
    return popupTrainReportActions(trainId);
  }

  function canReportFromCurrentMapMode() {
    return cfg.mode === "mini-app" || cfg.mode === "public-map" || cfg.mode === "public-network-map";
  }

  function resolveTrainPopupAction(item) {
    const actions = resolveTrainPopupActions(item);
    return actions.length ? actions[0] : null;
  }

  function renderTrainPopupActions(item) {
    return renderPopupActionButtons(resolveTrainPopupActions(item));
  }

  function buildTrainPopupHTML(item) {
    const routeName = [item.external.origin, item.external.destination].filter(Boolean).join(" → ");
    const nextStop = item.external.nextStop && item.external.nextStop.title
      ? `${item.external.nextStop.title}${item.external.nextStop.departureTime ? ` • ${clockLabel(item.external.nextStop.departureTime)}` : ""}`
      : "";
    const crewSummary = item.status
      ? `${statusSummary(item.status)}${typeof item.status.uniqueReporters === "number" && item.status.uniqueReporters > 0 ? ` • ${item.status.uniqueReporters} crew` : ""}`
      : "";
    const recentReports = Array.isArray(item.timeline) && item.timeline.length
      ? item.timeline.slice(0, 3).map((entry) => `${clockLabel(entry.at)} ${signalLabel(entry.signal)}`)
      : [];
    const recentSightings = Array.isArray(item.sightings) && item.sightings.length
      ? item.sightings.slice(0, 3).map((entry) => `${entry.stationName || entry.stationId} • ${relativeAgo(entry.createdAt)}`)
      : [];
    const incidents = Array.isArray(item.incidents) ? item.incidents : [];
    return buildPopupCard({
      title: item.external.trainNumber || trainNumberLabel(item.trainId),
      subtitle: routeName,
      sections: [
        popupInfoRow(t("app_map_popup_next_stop"), nextStop),
        popupInfoRow(
          t("app_map_popup_last_update"),
          liveTrainDisplayUpdatedAt(item.external)
            ? relativeAgo(liveTrainDisplayUpdatedAt(item.external))
            : t("app_map_popup_schedule")
        ),
        popupInfoRow(t("app_map_popup_crew"), crewSummary),
        incidentPopupSection(incidents),
        popupListSection(t("app_map_popup_recent_reports"), recentReports),
        popupListSection(t("app_map_popup_recent_sightings"), recentSightings),
      ],
      actionsHTML: renderTrainPopupActions(item) + incidentPopupActionsHTML(incidents),
    });
  }

  function stationPopupQuickCheckInAction(bucket) {
    if (cfg.mode !== "mini-app" || !state.authenticated || !bucket || !bucket.stationId) {
      return null;
    }
    const liveItems = Array.isArray(bucket.liveItems) ? bucket.liveItems : [];
    for (const item of liveItems) {
      const trainId = popupActionTrainId(item);
      const action = resolvePopupCheckInAction(trainId, popupActionEligibleUntil(item), bucket.stationId, item);
      if (!action) {
        continue;
      }
      return Object.assign({}, action, {
        label: `${action.label} ${trainNumberLabel(trainId)}`.trim(),
      });
    }
    return null;
  }

  function stationPopupSightingAction(bucket) {
    return null;
  }

  function stationPopupReportAction(station, bucket, options) {
    const hasExplicitAllow = options && Object.prototype.hasOwnProperty.call(options, "allowReports");
    const allowReports = hasExplicitAllow ? Boolean(options.allowReports) : mapReportsEnabled();
    const stationId = String((station && station.id) || (bucket && bucket.stationId) || "").trim();
    if (!allowReports || !state.authenticated || !stationId) {
      return null;
    }
    return {
      className: "primary small",
      action: "popup-report-station",
      stationId: stationId,
      label: t("app_station_report_action"),
    };
  }

  function mapReportsEnabled() {
    return Boolean(state.authenticated);
  }

  function buildStationPopupHTML(station, bucket, options) {
    const liveNow = bucket.liveItems.length
      ? bucket.liveItems.slice(0, 3).map((item) => {
        const crewSummary = item.status ? ` • ${statusSummary(item.status)}` : "";
        const nextStop = item.external && item.external.nextStop && item.external.nextStop.title ? ` • ${item.external.nextStop.title}` : "";
        return `${item.external.trainNumber || trainNumberLabel(item.trainId)}${crewSummary}${nextStop}`;
      })
      : [];
    const recentSightings = bucket.sightings.length
      ? bucket.sightings.slice(0, 3).map((entry) => `${relativeAgo(entry.createdAt)}${entry.destinationStationName ? ` • ${entry.destinationStationName}` : ""}`)
      : [t("app_station_sighting_empty")];
    const incidents = Array.isArray(options && options.incidents) ? options.incidents : [];
    const actions = [];
    const checkInAction = stationPopupQuickCheckInAction(bucket);
    if (checkInAction) {
      actions.push(checkInAction);
    }
    const sightingAction = stationPopupSightingAction(bucket);
    if (sightingAction) {
      actions.push(sightingAction);
    }
    const reportAction = stationPopupReportAction(station, bucket, options);
    if (reportAction) {
      actions.push(reportAction);
    }
    return buildPopupCard({
      title: (station && (station.name || station.id)) || bucket.name || "Station",
      sections: [
        popupListSection(t("app_map_popup_live_now"), liveNow),
        incidentPopupSection(incidents),
        popupListSection(t("app_map_popup_recent_sightings"), recentSightings),
      ],
      actionsHTML: renderPopupActionButtons(actions) + incidentPopupActionsHTML(incidents),
    });
  }

  function buildLocationReportDraftHTML(location) {
    const latitude = roundMapCoord(location && (location.latitude != null ? location.latitude : location.lat));
    const longitude = roundMapCoord(location && (location.longitude != null ? location.longitude : location.lng));
    const radiusMeters = locationReportRadiusMeters(location && location.radiusMeters);
    const radiusOptionsHTML = locationReportRadiusChoices().map((choice) => {
      const selectedAttr = choice === radiusMeters ? " selected" : "";
      return `<option value="${escapeAttr(choice)}"${selectedAttr}>${escapeHtml(`${choice} m`)}</option>`;
    }).join("");
    return buildPopupCard({
      title: t("app_location_report_title"),
      sections: [`
        <div class="map-popup-row">
          <label class="map-popup-label" for="location-report-description">${escapeHtml(t("app_location_report_description_label"))}</label>
          <textarea
            id="location-report-description"
            data-role="location-report-description"
            rows="3"
            maxlength="160"
            placeholder="${escapeAttr(t("app_location_report_description_placeholder"))}"
          ></textarea>
        </div>
        <div class="map-popup-row">
          <label class="map-popup-label" for="location-report-radius">${escapeHtml(t("app_location_report_radius_label"))}</label>
          <select id="location-report-radius" data-role="location-report-radius">
            ${radiusOptionsHTML}
          </select>
        </div>
      `],
      actionsHTML: `
        <button
          class="primary small"
          data-action="popup-submit-location-report"
          data-latitude="${escapeAttr(latitude)}"
          data-longitude="${escapeAttr(longitude)}"
          data-radius-meters="${escapeAttr(radiusMeters)}"
        >${escapeHtml(t("app_location_report_submit"))}</button>
      `,
    });
  }

  function locationReportRadiusChoices() {
    return [100, 250, 500, 1000];
  }

  function locationReportRadiusMeters(value) {
    const radius = Math.round(Number(value) || 0);
    return locationReportRadiusChoices().includes(radius) ? radius : 100;
  }

  function stopEligibleUntilAt(stop) {
    if (!stop) {
      return "";
    }
    return stop.departureAt || stop.arrivalAt || "";
  }

  function resolveTrainStopPopupAction(stop, mapData, hasLiveTrainMarker) {
    if (hasLiveTrainMarker) {
      return null;
    }
    const train = mapData && mapData.train ? mapData.train : null;
    const trainId = normalizeTrainId(train && train.id);
    if (!trainId) {
      return null;
    }
    return resolvePopupCheckInAction(
      trainId,
      [stopEligibleUntilAt(stop), train && train.arrivalAt ? train.arrivalAt : ""],
      stop && stop.stationId ? stop.stationId : ""
    );
  }

  function buildTrainStopPopupHTML(stop, index, mapData, liveItems, hasLiveTrainMarker) {
    const liveTrainSummary = Array.isArray(liveItems) && liveItems.length
      ? `${liveItems[0].external.trainNumber || trainNumberLabel(liveItems[0].trainId)}`
      : "";
    const sightings = stopSightings(stop, mapData);
    const recentSightings = sightings.length
      ? sightings.slice(0, 3).map((entry) => `${relativeAgo(entry.createdAt)}${entry.destinationStationName ? ` • ${entry.destinationStationName}` : ""}`)
      : [t("app_station_sighting_empty")];
    const actions = [
      stationPopupReportAction({
        id: stop && stop.stationId,
        name: stop && stop.stationName,
      }, {
        stationId: stop && stop.stationId,
      }, {
        allowReports: mapReportsEnabled(),
      }),
      resolveTrainStopPopupAction(stop, mapData, hasLiveTrainMarker),
    ].filter(Boolean);
    return buildPopupCard({
      title: stop.stationName || stop.stationId || "",
      sections: [
        popupInfoRow(t("app_map_popup_schedule"), stopTimeLabel(stop, index)),
        popupInfoRow(t("app_map_popup_live_train"), liveTrainSummary),
        popupListSection(t("app_map_popup_recent_sightings"), recentSightings),
      ],
      actionsHTML: renderPopupActionButtons(actions),
    });
  }

  function liveTrainDisplaySource(external) {
    if (!external) {
      return "";
    }
    if (external.displaySource === "projection" || external.displaySource === "live") {
      return external.displaySource;
    }
    if (external.position && external.isGpsActive) {
      return "live";
    }
    if (external.position && !external.isGpsActive) {
      return "projection";
    }
    return "";
  }

  function liveTrainDisplayUpdatedAt(external) {
    if (!external) {
      return "";
    }
    return external.displayUpdatedAt || external.updatedAt || "";
  }

  function liveTrainGpsLabel(external) {
    const displaySource = liveTrainDisplaySource(external);
    const displayUpdatedAt = liveTrainDisplayUpdatedAt(external);
    if (displaySource === "projection") {
      return "proj";
    }
    if (displaySource === "live") {
      if (!displayUpdatedAt) {
        return "sched";
      }
      const ageMinutes = sightingAgeMinutes(displayUpdatedAt);
      if (ageMinutes <= 1) {
        return "gps";
      }
      return `${ageMinutes}m`;
    }
    if (!external || !displayUpdatedAt || !external.isGpsActive) {
      return "sched";
    }
    const fallbackAgeMinutes = sightingAgeMinutes(displayUpdatedAt);
    if (fallbackAgeMinutes <= 1) {
      return "gps";
    }
    return `${fallbackAgeMinutes}m`;
  }

  function liveTrainGpsClass(external) {
    const displaySource = liveTrainDisplaySource(external);
    const displayUpdatedAt = liveTrainDisplayUpdatedAt(external);
    if (displaySource === "projection") {
      return sightingAgeMinutes(displayUpdatedAt) <= 6
        ? "gps-projection"
        : "gps-stale";
    }
    if (displaySource === "live") {
      if (!displayUpdatedAt) {
        return "gps-scheduled";
      }
      const ageMinutes = sightingAgeMinutes(displayUpdatedAt);
      if (ageMinutes <= 2) {
        return "gps-fresh";
      }
      if (ageMinutes <= 6) {
        return "gps-warm";
      }
      return "gps-stale";
    }
    if (!external || !external.isGpsActive) {
      return "gps-scheduled";
    }
    const fallbackAgeMinutes = sightingAgeMinutes(displayUpdatedAt);
    if (fallbackAgeMinutes <= 2) {
      return "gps-fresh";
    }
    if (fallbackAgeMinutes <= 6) {
      return "gps-warm";
    }
    return "gps-stale";
  }

  function hasCrewActivity(status) {
    return Boolean(status && status.state && status.state !== "NO_REPORTS");
  }

  function clockLabel(value) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return formatClock(date);
    }
    const text = String(value || "");
    const match = text.match(/(\d{2}:\d{2})/);
    return match ? match[1] : text;
  }

  function externalFeedStatusText() {
    if (!cfg.externalTrainMapEnabled) {
      return t("app_live_overlay_unavailable");
    }
    if (!state.externalFeed || state.externalFeed.connectionState === "disabled") {
      return t("app_live_overlay_unavailable");
    }
    if (state.externalFeed.connectionState === "live") {
      return t("app_live_overlay_ready");
    }
    if (state.externalFeed.connectionState === "connecting" || state.externalFeed.connectionState === "idle") {
      return t("app_live_overlay_connecting");
    }
    return t("app_live_overlay_offline");
  }

  function buildCoordinateLookup(items, idKey) {
    const byId = new Map();
    const byName = new Map();
    items.forEach((item) => {
      const latitude = typeof item.latitude === "number" ? item.latitude : null;
      const longitude = typeof item.longitude === "number" ? item.longitude : null;
      if (latitude === null || longitude === null) {
        return;
      }
      const id = item[idKey];
      if (id) {
        byId.set(String(id), [latitude, longitude]);
      }
      if (item.stationName) {
        byName.set(String(item.stationName), [latitude, longitude]);
      }
      if (item.name) {
        byName.set(String(item.name), [latitude, longitude]);
      }
    });
    return { byId, byName };
  }

  function buildSightingMarkers(items, lookup) {
    const sightings = Array.isArray(items) ? items : [];
    const stationCounts = Object.create(null);
    return sightings.map((item) => {
      const baseLatLng = lookup.byId.get(String(item.stationId || "")) || lookup.byName.get(String(item.stationName || ""));
      if (!baseLatLng) {
        return null;
      }
      const stationKey = String(item.stationId || item.stationName || "unknown");
      const offsetIndex = stationCounts[stationKey] || 0;
      stationCounts[stationKey] = offsetIndex + 1;
      const markerKey = `sighting:${stationKey}:${item.createdAt || offsetIndex}:${item.destinationStationName || ""}`;
      const popupHTML = sightingPopupHTML(item);
      return {
        kind: "tag",
        latLng: baseLatLng,
        markerKey,
        pixelOffset: sightingPixelOffset(offsetIndex),
        zIndexOffset: 1000 - offsetIndex,
        tagText: sightingTagText(item.createdAt),
        bucketClass: `bucket-${sightingRecencyBucket(item.createdAt)}`,
        popupHTML,
        interaction: {
          entityKey: markerKey,
          detailHTML: popupHTML,
          selectionOptions: {},
        },
      };
    }).filter(Boolean);
  }

  function activeMapIncidents(scope) {
    return (Array.isArray(state.publicIncidents) ? state.publicIncidents : [])
      .filter((item) => item && item.active !== false && (!scope || item.scope === scope));
  }

  function incidentActivityLabel(item) {
    const title = String((item && item.subjectName) || (item && item.lastActivityName) || (item && item.lastReportName) || t("app_section_incidents")).trim();
    const at = (item && (item.lastActivityAt || item.lastReportAt)) || "";
    return at ? `${title} • ${relativeAgo(at)}` : title;
  }

  function incidentPopupActionsHTML(incidents) {
    const items = (Array.isArray(incidents) ? incidents : []).filter((item) => item && item.id).slice(0, 3);
    return items.map((item) => `
      <button
        class="ghost small"
        data-action="popup-open-incident"
        data-incident-id="${escapeAttr(item.id)}"
      >${escapeHtml(t("app_section_incidents"))}</button>
    `).join("");
  }

  function incidentPopupSection(incidents) {
    const items = (Array.isArray(incidents) ? incidents : []).filter(Boolean);
    return popupListSection(t("app_section_incidents"), items.slice(0, 3).map(incidentActivityLabel));
  }

  function activeStationIncidentsFor(station, bucket) {
    const keys = [
      station && station.id,
      station && station.name,
      bucket && bucket.stationId,
      bucket && bucket.name,
    ].map(stationKeyValue).filter(Boolean);
    if (!keys.length) {
      return [];
    }
    const seen = new Set();
    return activeMapIncidents("station").filter((item) => {
      const location = item && item.location ? item.location : null;
      const incidentKeys = [
        stationIdFromIncident(item),
        item && item.subjectName,
        location && location.name,
        location && location.description,
      ].map(stationKeyValue).filter(Boolean);
      const matched = incidentKeys.some((key) => keys.includes(key));
      if (!matched || seen.has(item.id)) {
        return false;
      }
      seen.add(item.id);
      return true;
    });
  }

  function activeTrainIncidentsForItem(item) {
    const keys = [
      item && item.trainId,
      item && localTrainId(item.localMatch),
      item && externalTrainInstanceId(item.external),
    ].map((value) => String(value || "").trim()).filter(Boolean);
    if (!keys.length) {
      return [];
    }
    const seen = new Set();
    return activeMapIncidents().filter((incident) => {
      const target = incidentMapTargetInfo(incident);
      const trainId = target && target.type === "train"
        ? target.trainInstanceId
        : trainIdFromIncidentId(incident && incident.id);
      if (!trainId || !keys.includes(trainId) || seen.has(incident.id)) {
        return false;
      }
      seen.add(incident.id);
      return true;
    });
  }

  function activeAreaIncidentMarkers() {
    const incidents = activeMapIncidents("area");
    const markers = [];
    incidents.forEach((item, index) => {
      const location = item && item.location ? item.location : null;
      const latitude = location && typeof location.latitude === "number" ? location.latitude : null;
      const longitude = location && typeof location.longitude === "number" ? location.longitude : null;
      if (!item || item.active === false || item.scope !== "area" || !location || location.kind !== "area" || latitude === null || longitude === null) {
        return;
      }
      const markerKey = `area-incident:${item.id}`;
      const circleKey = `area-circle:${item.id}`;
      const radius = Math.max(0, Math.round(Number(location.radiusMeters) || 0));
      const popupHTML = areaIncidentPopupHTML(item);
      const interaction = {
        entityKey: markerKey,
        detailHTML: popupHTML,
        selectionOptions: {
          incidentId: item.id,
        },
      };
      markers.push({
        kind: "circle",
        latLng: [latitude, longitude],
        markerKey: circleKey,
        incidentIds: [item.id],
        options: {
          radius: radius || 100,
          color: "#b45309",
          fillColor: "#f97316",
          fillOpacity: 0.14,
          opacity: 0.86,
          weight: 2,
        },
        popupHTML,
        interaction,
      });
      markers.push({
        kind: "tag",
        latLng: [latitude, longitude],
        markerKey,
        incidentIds: [item.id],
        pixelOffset: sightingPixelOffset(index),
        zIndexOffset: 900 - index,
        tagText: radius > 0 ? `${radius}m` : "!",
        bucketClass: "bucket-warm",
        popupHTML,
        interaction,
      });
    });
    return markers;
  }

  function sightingPixelOffset(index) {
    const offsets = [
      [0, 0],
      [34, 0],
      [0, 28],
      [-34, 0],
      [0, -28],
      [26, 22],
      [-26, 22],
      [26, -22],
      [-26, -22],
    ];
    return offsets[index % offsets.length];
  }

  function sightingRecencyBucket(raw) {
    const minutes = sightingAgeMinutes(raw);
    if (minutes <= 5) return "fresh";
    if (minutes <= 15) return "warm";
    return "stale";
  }

  function sightingAgeMinutes(raw) {
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) {
      return 999;
    }
    return Math.max(0, Math.round((Date.now() - date.getTime()) / 60000));
  }

  function sightingTagText(raw) {
    const minutes = sightingAgeMinutes(raw);
    if (minutes <= 0) return t("app_map_tag_now");
    return `${minutes}m`;
  }

  function sightingPopupHTML(item) {
    const details = [
      `<strong>${escapeHtml(item.stationName || item.stationId || "")}</strong>`,
    ];
    if (item.destinationStationName) {
      details.push(`${escapeHtml(t("app_map_popup_destination"))}: ${escapeHtml(item.destinationStationName)}`);
    }
    details.push(`${escapeHtml(t("app_map_popup_status"))}: ${escapeHtml(item.matchedTrainInstanceId ? t("app_station_sighting_matched") : t("app_station_sighting_unmatched"))}`);
    details.push(`${escapeHtml(t("app_map_popup_age"))}: ${escapeHtml(relativeAgo(item.createdAt))}`);
    details.push(`${escapeHtml(t("app_map_popup_seen_at"))}: ${escapeHtml(formatDateTime(item.createdAt))}`);
    return details.join("<br>");
  }

  function areaIncidentPopupHTML(item) {
    const location = item && item.location ? item.location : {};
    const radius = Math.max(0, Math.round(Number(location.radiusMeters) || 0));
    const details = [
      `<strong>${escapeHtml(item.subjectName || location.description || t("app_location_report_title"))}</strong>`,
    ];
    if (radius > 0) {
      details.push(`${escapeHtml(t("app_map_popup_status"))}: ${escapeHtml(radius + " m")}`);
    }
    if (item.lastActivityAt) {
      details.push(`${escapeHtml(t("app_map_popup_age"))}: ${escapeHtml(relativeAgo(item.lastActivityAt))}`);
    }
    return details.join("<br>");
  }

  function renderToast() {
    if (!state.toast || !state.toast.message) return "";
    const kind = state.toast.kind || "success";
    const role = kind === "error" ? "alert" : "status";
    return `
      <div class="toast-stack" aria-live="polite">
        <div class="toast ${escapeAttr(kind)}" role="${escapeAttr(role)}">${escapeHtml(state.toast.message)}</div>
      </div>
    `;
  }

  function renderLoading() {
    setAppHTML(`<div class="shell"><section class="hero"><h1>${escapeHtml(t("app_loading"))}</h1></section></div>`);
  }

  function renderDataUnavailableContent() {
    const detail = state.strictModeLoadError && state.strictModeLoadError.message
      ? state.strictModeLoadError.message
      : "";
    const showDetail = detail && detail !== t("app_data_unavailable_body");
    return `
      <div class="shell">
        <section class="hero">
          <h1>${escapeHtml(t("app_data_unavailable_title"))}</h1>
          <p>${escapeHtml(t("app_data_unavailable_body"))}</p>
          ${showDetail ? `<p>${escapeHtml(detail)}</p>` : ""}
          <div class="hero-actions">
            <button class="button primary" data-action="retry-current-view">${escapeHtml(t("app_retry_data_load"))}</button>
          </div>
        </section>
      </div>`;
  }

  function renderDataUnavailable() {
    setAppHTML(renderDataUnavailableContent());
  }

  function renderAuthRequired() {
    setAppHTML(`
      <div class="shell">
        <section class="hero">
          <h1>${escapeHtml(t("app_auth_required"))}</h1>
          <p>${escapeHtml(t("app_auth_required_body"))}</p>
          <div class="hero-actions">
            <a class="button primary" href="${escapeAttr(publicStationRoot())}">${escapeHtml(t("app_open_station_search"))}</a>
            <a class="button ghost" href="${escapeAttr(publicDashboardRoot())}">${escapeHtml(t("app_open_departures"))}</a>
          </div>
        </section>
      </div>`);
  }

  function renderFatal(err) {
    setAppHTML(`<div class="shell"><section class="hero"><h1>${escapeHtml(t("app_status_error"))}</h1><p>${escapeHtml(err && err.message ? err.message : String(err))}</p></section></div>`);
  }

  function bindGlobalDocumentEvents() {
    if (document.body && document.body.dataset.trainMapGlobalsBound === "1") {
      return;
    }
    if (document.body) {
      document.body.dataset.trainMapGlobalsBound = "1";
    }
    if (typeof window !== "undefined" && typeof window.PointerEvent === "function") {
      document.addEventListener("pointerdown", (event) => {
        mapController.recordDocumentTapStart(event);
      });
      document.addEventListener("pointerup", (event) => {
        if (mapController.handleDocumentTapEnd(event)) {
          if (event && typeof event.preventDefault === "function") {
            event.preventDefault();
          }
          if (event && typeof event.stopPropagation === "function") {
            event.stopPropagation();
          }
        }
      });
      document.addEventListener("pointercancel", () => {
        mapController.clearDocumentTap();
      });
    } else {
      document.addEventListener("touchstart", (event) => {
        mapController.recordDocumentTapStart(event);
      });
      document.addEventListener("touchend", (event) => {
        if (mapController.handleDocumentTapEnd(event)) {
          if (event && typeof event.preventDefault === "function") {
            event.preventDefault();
          }
          if (event && typeof event.stopPropagation === "function") {
            event.stopPropagation();
          }
        }
      });
      document.addEventListener("touchcancel", () => {
        mapController.clearDocumentTap();
      });
    }
    document.addEventListener("click", (event) => {
      const target = event && event.target && typeof event.target.closest === "function"
        ? event.target
        : null;
      const actionButton = target ? target.closest("[data-action]") : null;
      if (actionButton && actionButton.getAttribute("data-action") === "retry-current-view") {
        closeSiteMenus();
        void retryCurrentView();
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (actionButton && actionButton.getAttribute("data-action") === "refresh-current-view") {
        closeSiteMenus();
        void retryCurrentView();
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (actionButton && actionButton.getAttribute("data-action") === "site-menu-toggle") {
        const willOpen = !state.siteMenuOpen;
        state.siteMenuOpen = willOpen;
        if (!willOpen) {
          state.routeCheckInMenuOpen = false;
        }
        rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (actionButton && actionButton.getAttribute("data-action") === "telegram-login") {
        closeSiteMenus();
        void beginTelegramLogin();
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
	      if (actionButton && actionButton.getAttribute("data-action") === "telegram-logout") {
	        closeSiteMenus();
	        void logout();
	        if (event && typeof event.preventDefault === "function") {
	          event.preventDefault();
	        }
	        return;
	      }
	      if (actionButton && actionButton.getAttribute("data-action") === "route-checkin-login") {
	        closeSiteMenus();
	        void beginTelegramLogin();
	        if (event && typeof event.preventDefault === "function") {
	          event.preventDefault();
	        }
	        return;
	      }
	      if (actionButton && actionButton.getAttribute("data-action") === "route-checkin-toggle") {
	        if (!state.authenticated) {
	          void beginTelegramLogin();
	        } else {
	          state.siteMenuOpen = true;
	          state.routeCheckInMenuOpen = !state.routeCheckInMenuOpen;
	          if (state.routeCheckInMenuOpen) {
	            void ensureRouteCheckInRoutes().then(() => rerenderCurrent({ preserveInputFocus: true, preserveDetail: true })).catch(() => {});
	          }
	          rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	        }
	        if (event && typeof event.preventDefault === "function") {
	          event.preventDefault();
	        }
	        return;
	      }
	      if (actionButton && actionButton.getAttribute("data-action") === "route-checkin-start") {
	        const selectedRouteId = actionButton.getAttribute("data-route-id") || state.routeCheckInSelectedRouteId;
	        const durationMinutes = Number(actionButton.getAttribute("data-duration-minutes")) || state.routeCheckInDurationMinutes;
	        void runUserAction(() => startRouteCheckIn(selectedRouteId, durationMinutes), (result) => result, { button: actionButton });
	        if (event && typeof event.preventDefault === "function") {
	          event.preventDefault();
	        }
	        return;
	      }
	      if (actionButton && actionButton.getAttribute("data-action") === "route-checkin-checkout") {
	        void runUserAction(() => checkoutRouteCheckIn(), (result) => result, { button: actionButton });
	        if (event && typeof event.preventDefault === "function") {
	          event.preventDefault();
	        }
	        return;
	      }
      if (actionButton && actionButton.getAttribute("data-action") === "close-incident-detail") {
        closeIncidentDetailOverlay({ force: true });
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (actionButton && actionButton.getAttribute("data-action") === "close-map-detail") {
        mapController.closePopup();
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (state.siteMenuOpen && target && !target.closest(".status-bar")) {
        closeSiteMenus();
        rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
      }
      if (actionButton && handleMapPopupAction(actionButton)) {
        if (event && typeof event.preventDefault === "function") {
          event.preventDefault();
        }
        return;
      }
      if (state.tab === "map" && state.expandedStopContextKey && target
        && !target.closest(".stop-row")
        && !target.closest(".leaflet-popup")
        && !target.closest(".train-map-detail-card")) {
        state.expandedStopContextKey = "";
        collapseExpandedStopContextUI();
      }
      mapController.handleDocumentClick(event);
    });
	    if (typeof window.addEventListener === "function") {
	      window.addEventListener("popstate", handleIncidentPopState);
	      window.addEventListener("resize", handleIncidentViewportChange);
	    }
	    document.addEventListener("change", (event) => {
	      const target = event && event.target && typeof event.target.closest === "function"
	        ? event.target
	        : null;
	      if (!target) {
	        return;
	      }
	      const languageSelect = target.closest("[data-action='site-language']");
	      if (languageSelect) {
	        void changeSiteLanguage(languageSelect.value);
	        return;
	      }
	      const routeSelect = target.closest("[data-action='route-checkin-route']");
	      if (routeSelect) {
	        state.routeCheckInSelectedRouteId = String(routeSelect.value || "");
	        rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	        return;
	      }
	      const durationSelect = target.closest("[data-action='route-checkin-duration']");
	      if (durationSelect) {
	        state.routeCheckInDurationMinutes = Number(durationSelect.value) || state.routeCheckInDefaultDurationMinutes;
	        rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
	      }
	    });
	  }

  function collapseExpandedStopContextUI() {
    const expandedToggle = document.querySelector("[data-action='toggle-stop-context'][aria-expanded='true']");
    if (!expandedToggle) {
      return;
    }
    expandedToggle.setAttribute("aria-expanded", "false");
    const row = expandedToggle.closest(".stop-row");
    if (!row) {
      return;
    }
    row.classList.remove("expanded");
    const context = row.querySelector(".stop-context");
    if (context && context.parentNode) {
      context.parentNode.removeChild(context);
    }
  }

  function publicStatusLink(href, label, className) {
    return `<a class="${escapeAttr(className || "button ghost small")}" href="${escapeAttr(href)}">${escapeHtml(label)}</a>`;
  }

  function publicStatusButton(id, label, className, action) {
    const actionAttr = action ? ` data-action="${escapeAttr(action)}"` : "";
    return `<button class="${escapeAttr(className || "ghost small")}" id="${escapeAttr(id)}"${actionAttr}>${escapeHtml(label)}</button>`;
  }

	  function retryCurrentViewAction(className) {
	    if (!state.strictModeLoadError || state.strictModeLoadError.blocking) {
	      return "";
	    }
	    return `<button class="${escapeAttr(className || "ghost small")}" data-action="retry-current-view">${escapeHtml(t("app_retry_data_load"))}</button>`;
	  }

	  function renderSiteLanguageControlHTML() {
	    return `
	      <label class="site-language-control">
	        <span>${escapeHtml(t("app_language"))}</span>
	        <select class="site-language-select" data-action="site-language" aria-label="${escapeAttr(t("app_language"))}">
	          <option value="LV" ${state.lang === "LV" ? "selected" : ""}>${escapeHtml(t("app_language_lv"))}</option>
	          <option value="EN" ${state.lang === "EN" ? "selected" : ""}>${escapeHtml(t("app_language_en"))}</option>
	        </select>
	      </label>
	    `;
	  }

	  function routeCheckInChipHTML(options) {
	    const config = options || {};
	    const active = state.routeCheckIn;
	    const activeName = active && (active.routeName || active.routeId) ? (active.routeName || active.routeId) : "";
	    if (!activeName && config.hideEmpty) {
	      return "";
	    }
	    return activeName
	      ? `<span class="route-checkin-chip">${escapeHtml(t("app_route_checkin_active", activeName, formatClock(active.expiresAt)))}</span>`
	      : `<span class="route-checkin-chip muted">${escapeHtml(t("app_route_checkin_none"))}</span>`;
	  }

	  function renderRouteCheckInControlsHTML(options) {
	    const config = options || {};
	    const active = state.routeCheckIn;
	    const activeName = active && (active.routeName || active.routeId) ? (active.routeName || active.routeId) : "";
	    const activeChip = routeCheckInChipHTML();
	    const toggleLabel = activeName ? t("app_route_checkin_change") : t("app_route_checkin_watch");
	    const menu = state.routeCheckInMenuOpen ? renderRouteCheckInMenuHTML() : "";
	    const rootClass = config.menuContext ? "route-checkin-controls route-checkin-controls-menu" : "route-checkin-controls";
	    if (!state.authenticated) {
	      return `
	        <div class="${rootClass}">
	          ${activeChip}
	          <button class="ghost small" data-action="route-checkin-login">${escapeHtml(t("app_route_checkin_login"))}</button>
	        </div>
	      `;
	    }
	    return `
	      <div class="${rootClass}">
	        ${activeChip}
	        <button class="secondary small" data-action="route-checkin-toggle" aria-expanded="${state.routeCheckInMenuOpen ? "true" : "false"}">${escapeHtml(toggleLabel)}</button>
	        ${menu}
	      </div>
	    `;
	  }

	  function renderRouteCheckInMenuHTML() {
	    const routes = Array.isArray(state.routeCheckInRoutes) ? state.routeCheckInRoutes : [];
	    const selectedId = state.routeCheckInSelectedRouteId || (state.routeCheckIn && state.routeCheckIn.routeId) || (routes[0] && routes[0].id) || "";
	    const routeOptions = routes.length
	      ? routes.map((route) => `<option value="${escapeAttr(route.id)}" ${route.id === selectedId ? "selected" : ""}>${escapeHtml(route.name || route.id)}</option>`).join("")
	      : `<option value="">${escapeHtml(state.routeCheckInLoading ? t("app_loading") : t("app_route_checkin_no_routes"))}</option>`;
	    const durations = [30, 60, 120, 240, 480].filter((minutes) => (
	      minutes >= state.routeCheckInMinDurationMinutes && minutes <= state.routeCheckInMaxDurationMinutes
	    ));
	    if (!durations.includes(state.routeCheckInDurationMinutes)) {
	      durations.push(state.routeCheckInDefaultDurationMinutes || 120);
	      durations.sort((left, right) => left - right);
	    }
	    const durationOptions = durations.map((minutes) => (
	      `<option value="${escapeAttr(minutes)}" ${minutes === state.routeCheckInDurationMinutes ? "selected" : ""}>${escapeHtml(durationLabel(minutes))}</option>`
	    )).join("");
	    return `
	      <div class="route-checkin-menu">
	        ${state.routeCheckInLoading && !routes.length ? loadingStateHTML(t("app_loading"), "loading-state-inline") : ""}
	        <label>
	          <span>${escapeHtml(t("app_route_checkin_route"))}</span>
	          <select data-action="route-checkin-route">${routeOptions}</select>
	        </label>
	        <label>
	          <span>${escapeHtml(t("app_route_checkin_duration"))}</span>
	          <select data-action="route-checkin-duration">${durationOptions}</select>
	        </label>
	        <div class="button-row route-checkin-actions">
	          <button class="primary small" data-action="route-checkin-start" data-route-id="${escapeAttr(selectedId)}" data-duration-minutes="${escapeAttr(state.routeCheckInDurationMinutes)}" ${selectedId ? "" : "disabled"}>${escapeHtml(t("app_route_checkin_start"))}</button>
	          ${state.routeCheckIn ? `<button class="ghost small" data-action="route-checkin-checkout">${escapeHtml(t("app_route_checkin_stop"))}</button>` : ""}
	        </div>
	      </div>
	    `;
	  }

	  function durationLabel(minutes) {
	    const numeric = Number(minutes) || 0;
	    if (numeric >= 60 && numeric % 60 === 0) {
	      return t("app_route_checkin_hours", numeric / 60);
	    }
	    return t("app_route_checkin_minutes", numeric);
	  }

	  function renderPublicStatusBar(options) {
	    const config = options || {};
	    const actions = Array.isArray(config.actions) ? config.actions.filter(Boolean).join("") : "";
	    const retryAction = retryCurrentViewAction("ghost small");
	    const authControls = config.authControls === false ? "" : renderPublicAuthControlsHTML();
	    const languageControl = config.languageControl === false ? "" : renderSiteLanguageControlHTML();
	    const routeCheckInControls = config.routeCheckInControls === false ? "" : renderRouteCheckInControlsHTML();
	    const textId = config.textId ? ` id="${escapeAttr(config.textId)}"` : "";
	    return `
	      <span${textId}>${escapeHtml(config.statusText || state.statusText || t("app_status_public"))}</span>
	      <div class="button-row status-actions">${actions}${languageControl}${routeCheckInControls}${authControls}${retryAction}${config.trailingHTML || ""}</div>
	    `;
	  }

	  function renderCompactMapStatusBar(options) {
	    const config = options || {};
	    const menuOpen = Boolean(state.siteMenuOpen);
	    const textId = config.textId ? ` id="${escapeAttr(config.textId)}"` : "";
	    const statusText = config.statusText || state.statusText || t("app_status_public");
	    const quickActions = Array.isArray(config.quickActions) ? config.quickActions.filter(Boolean).join("") : "";
	    const menuActions = Array.isArray(config.menuActions) ? config.menuActions.filter(Boolean).join("") : "";
	    const retryAction = retryCurrentViewAction("ghost small");
	    const activeRouteChip = routeCheckInChipHTML({ hideEmpty: true });
	    return `
	      <span class="map-top-status"${textId}>${escapeHtml(statusText)}</span>
	      <div class="button-row map-top-actions">
	        ${activeRouteChip}
	        ${quickActions}
	        <button class="secondary small site-menu-toggle" data-action="site-menu-toggle" aria-expanded="${menuOpen ? "true" : "false"}">${escapeHtml(menuOpen ? t("app_menu_close") : t("app_menu"))}</button>
	      </div>
	      ${menuOpen ? `
	        <div class="site-menu-dropdown" data-role="site-menu">
	          <div class="button-row site-menu-links">${menuActions}</div>
	          <div class="site-menu-section">${renderSiteLanguageControlHTML()}</div>
	          <div class="site-menu-section">${renderRouteCheckInControlsHTML({ menuContext: true })}</div>
	          <div class="site-menu-section">${renderPublicAuthControlsHTML()}</div>
	          ${retryAction ? `<div class="site-menu-section">${retryAction}</div>` : ""}
	          ${config.trailingHTML ? `<div class="site-menu-section">${config.trailingHTML}</div>` : ""}
	        </div>
	      ` : ""}
	    `;
	  }

  function renderPublicAuthControlsHTML() {
    if (state.authInProgress) {
      return `<span class="status-pill auth-status">${escapeHtml(t("app_signing_in"))}</span>`;
    }
    if (state.authenticated) {
      const nickname = state.me && state.me.nickname ? state.me.nickname : "";
      const signedIn = nickname ? `<span class="status-pill auth-status">${escapeHtml(t("app_signed_in_as", nickname))}</span>` : "";
      return `${signedIn}<button class="ghost small" data-action="telegram-logout">${escapeHtml(t("app_logout"))}</button>`;
    }
    const feedback = state.authFeedback && state.authFeedback.message
      ? `<span class="status-pill auth-status warning">${escapeHtml(state.authFeedback.message)}</span>`
      : "";
    return `${feedback}<button class="primary small" data-action="telegram-login">${escapeHtml(t("app_login_telegram"))}</button>`;
  }

  function renderPublicDashboardStatusBar() {
    return renderPublicStatusBar({
      actions: [
        publicStatusLink(publicNetworkMapRoot(), t("app_section_map")),
        publicStatusLink(publicIncidentsRoot(), t("app_public_incidents_title")),
        publicStatusLink(publicStationRoot(), t("app_open_station_search")),
        classicControlLink(),
      ],
      trailingHTML: `<span class="status-pill">${escapeHtml(formatClock(new Date()))}</span>`,
    });
  }

  function renderPublicTrainStatusBar() {
    const mapHref = cfg.trainId ? publicTrainMapRoot(cfg.trainId) : publicNetworkMapRoot();
    return renderPublicStatusBar({
      actions: [
        publicStatusLink(mapHref, t("app_section_map")),
        publicStatusLink(publicIncidentsRoot(), t("app_public_incidents_title")),
        publicStatusLink(publicDashboardRoot(), t("app_open_departures")),
        publicStatusLink(publicStationRoot(), t("app_open_station_search")),
        classicControlLink(),
      ],
    });
  }

  function renderPublicDashboard(options) {
    const inputFocus = snapshotFocusedInput("public-filter");
    const filter = state.publicFilter.trim().toLowerCase();
    const items = state.publicDashboard.filter((item) => {
      if (!filter) return true;
      const route = `${item.train.fromStation} ${item.train.toStation}`.toLowerCase();
      return route.includes(filter) || String(item.train.departureAt).toLowerCase().includes(filter);
    });
    const emptyDashboardText = scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_public_dashboard_empty");
    setAppHTML(`
      <div class="shell">
        ${renderHero(t("app_public_dashboard_eyebrow"), t("app_public_dashboard_title"), t("app_public_dashboard_note"))}
        <section class="status-bar">${renderPublicDashboardStatusBar()}</section>
        <section class="panel" id="public-dashboard-list-panel">
          <div class="form-grid">
            <div class="field">
              <label>${escapeHtml(t("app_dashboard_filter"))}</label>
              <input id="public-filter" type="text" value="${escapeAttr(state.publicFilterDraft)}" placeholder="${escapeAttr(t("app_dashboard_filter"))}">
            </div>
            <div class="button-row">
              <button class="secondary" id="public-search">${escapeHtml(t("app_search"))}</button>
              <button class="primary" id="public-refresh">${escapeHtml(t("app_refresh"))}</button>
            </div>
          </div>
          <div class="divider"></div>
          <div class="card-list">${items.length ? items.map(renderPublicCard).join("") : `<div class="empty">${escapeHtml(emptyDashboardText)}</div>`}</div>
        </section>
      </div>
      ${renderToast()}`);
    bindPublicDashboardEvents(document.getElementById("public-dashboard-list-panel") || appEl);
    restoreFocusedInput(inputFocus);
  }

  function renderPublicTrainSidebarPanel(item) {
    return item ? renderPublicDetail(item) : `<div class="empty">${escapeHtml(scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_public_dashboard_empty"))}</div>`;
  }

  function renderPublicTrain() {
    const item = state.publicTrain;
    setAppHTML(`
      <div class="shell">
        ${renderHero(t("app_public_train_eyebrow"), t("app_public_train_title"), t("app_public_train_note"))}
        <section class="status-bar">${renderPublicTrainStatusBar()}</section>
        <section class="panel" id="public-train-status-panel">${renderPublicTrainSidebarPanel(item)}</section>
      </div>
      ${renderToast()}`);
  }

  function renderToastRoot(rootId) {
    return `<div id="${escapeAttr(rootId)}">${renderToast()}</div>`;
  }

  function snapshotFocusedInput(inputId) {
    const activeElement = document.activeElement;
    const wasFocused = Boolean(activeElement && activeElement.id === inputId);
    return {
      inputId,
      wasFocused,
      selectionStart: wasFocused && typeof activeElement.selectionStart === "number" ? activeElement.selectionStart : null,
      selectionEnd: wasFocused && typeof activeElement.selectionEnd === "number" ? activeElement.selectionEnd : null,
    };
  }

  function restoreFocusedInput(snapshot) {
    if (!snapshot || !snapshot.wasFocused) {
      return;
    }
    const input = document.getElementById(snapshot.inputId);
    if (!input) {
      return;
    }
    input.focus();
    if (snapshot.selectionStart !== null && snapshot.selectionEnd !== null && typeof input.setSelectionRange === "function") {
      input.setSelectionRange(snapshot.selectionStart, snapshot.selectionEnd);
    }
  }

  function trainMapShellState(containerId, mapData, includeSelectionPrompt) {
    if (!mapData || !mapData.train) {
      return {
        hasTrain: false,
        hasMap: false,
        html: `<div class="empty">${escapeHtml(scheduleUnavailable() ? scheduleUnavailableMessage() : includeSelectionPrompt ? t("app_map_prompt") : t("app_map_empty"))}</div>`,
        missingCoordsText: "",
        stopListHTML: "",
      };
    }
    const liveItem = buildSelectedTrainLiveItem(mapData);
    const stops = Array.isArray(mapData.stops) ? mapData.stops : [];
    const locatedStops = locatedTrainStops(mapData);
    const hasMap = Boolean((liveItem && liveItem.external && liveItem.external.position) || locatedStops.length);
    const stopListHTML = stops.length
      ? stops.map((stop, index) => renderStopRow(stop, index, mapData)).join("")
      : `<div class="empty">${escapeHtml(t("app_map_empty"))}</div>`;
    return {
      hasTrain: true,
      hasMap: hasMap,
      html: hasMap
        ? `
          <div id="${escapeAttr(containerId)}" class="train-map-shell" aria-label="${escapeAttr(t("app_section_map"))}">
            <div class="train-map-viewport"></div>
            <div class="train-map-detail-layer" hidden></div>
          </div>`
        : `<div class="empty">${escapeHtml(scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_map_empty"))}</div>`,
      missingCoordsText: stops.length && locatedStops.length !== stops.length ? t("app_map_missing_coords") : "",
      stopListHTML: stopListHTML,
    };
  }

  function networkMapShellState(containerId, mapData) {
    const liveItems = buildMapVisibleLiveItems(serviceDayTrainItemsForMapMatching(), []);
    const stations = locatedNetworkStations(mapData);
    return {
      hasMap: liveItems.length > 0 || stations.length > 0,
      html: liveItems.length || stations.length
        ? `
          <div id="${escapeAttr(containerId)}" class="train-map-shell" aria-label="${escapeAttr(t("app_network_map_title"))}">
            <div class="train-map-viewport"></div>
            <div class="train-map-detail-layer" hidden></div>
          </div>`
        : `<div class="empty">${escapeHtml(scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_network_map_empty"))}</div>`,
    };
  }

  function syncPublicMapShellSlot(slotEl, containerId, shellState) {
    if (!slotEl) {
      return false;
    }
    const currentMapEl = slotEl.querySelector(`#${containerId}`);
    if (shellState.hasMap) {
      if (!currentMapEl) {
        slotEl.innerHTML = shellState.html;
      }
      return true;
    }
    if (currentMapEl && mapController.containerId === containerId) {
      mapController.closePopup();
      mapController.detach();
      clearPublicMapPopupSelection();
    }
    if (!currentMapEl || slotEl.innerHTML !== shellState.html) {
      slotEl.innerHTML = shellState.html;
    }
    return false;
  }

  function publicTrainMapSummaryItem() {
    if (!state.mapData || !state.mapData.train) {
      return null;
    }
    const fallbackStatus = state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train
      && state.selectedTrain.trainCard.train.id === state.mapData.train.id
      ? state.selectedTrain.trainCard.status
      : null;
    const fallbackRiders = state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train
      && state.selectedTrain.trainCard.train.id === state.mapData.train.id
      ? state.selectedTrain.trainCard.riders
      : 0;
    return state.mapData.trainCard
      ? { trainCard: state.mapData.trainCard }
      : { trainCard: { train: state.mapData.train, status: fallbackStatus, riders: fallbackRiders } };
  }

  function renderPublicMapStatusBar() {
    return renderCompactMapStatusBar({
      textId: "public-map-status-text",
      quickActions: [
        publicStatusButton("public-map-refresh", t("app_refresh"), "ghost small", "refresh-current-view"),
      ],
      menuActions: [
        publicStatusLink(publicNetworkMapRoot(), t("app_network_map_title")),
        publicStatusLink(pathFor(`/t/${encodeURIComponent(cfg.trainId || "")}`), t("btn_view_status")),
        publicStatusLink(publicIncidentsRoot(), t("app_public_incidents_title")),
        classicControlLink(),
      ],
    });
  }

  function renderPublicMapSightingsCard() {
    return `
      <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
      ${renderStationSightings(state.mapData && state.mapData.stationSightings)}
    `;
  }

  function renderPublicMapStopListCard(shellState) {
    return `
      <h3>${escapeHtml(t("app_stop_list"))}</h3>
      <div class="stop-list">${shellState.stopListHTML}</div>
    `;
  }

  function renderPublicMapMainPanel() {
    const shellState = trainMapShellState("public-train-map", state.mapData, false);
    if (!shellState.hasTrain) {
      return shellState.html;
    }
    return `
      <div class="stack">
        <div id="public-map-shell-slot">${shellState.html}</div>
        <p class="panel-subtitle map-live-status" id="public-map-live-status">${escapeHtml(externalFeedStatusText())}</p>
      </div>
    `;
  }

  function renderPublicMapDetailsPanel() {
    const summaryItem = publicTrainMapSummaryItem();
    if (!summaryItem) {
      return `<div class="empty">${escapeHtml(scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_map_empty"))}</div>`;
    }
    const shellState = trainMapShellState("public-train-map", state.mapData, false);
    return `
      <div class="stack">
        <div id="public-map-summary">${renderRideSummary(summaryItem)}</div>
        <section class="detail-card" id="public-map-sightings-card">${renderPublicMapSightingsCard()}</section>
        ${shellState.missingCoordsText ? `<p class="panel-subtitle">${escapeHtml(shellState.missingCoordsText)}</p>` : ""}
        <section class="detail-card" id="public-map-stop-list-card">${renderPublicMapStopListCard(shellState)}</section>
      </div>
    `;
  }

  function patchPublicMapMainPanel(options) {
    const mainPanel = document.getElementById("public-map-main-panel");
    if (!mainPanel) {
      return false;
    }
    const renderOptions = options || {};
    const shellState = trainMapShellState("public-train-map", state.mapData, false);
    if (!shellState.hasTrain) {
      if (mapController.containerId === "public-train-map") {
        mapController.closePopup();
        mapController.detach();
        clearPublicMapPopupSelection();
      }
      if (!renderOptions.mapOnly) {
        mainPanel.innerHTML = renderPublicMapMainPanel();
      }
      return true;
    }
    const slotEl = document.getElementById("public-map-shell-slot");
    const liveStatusEl = document.getElementById("public-map-live-status");
    if (!slotEl || !liveStatusEl) {
      mainPanel.innerHTML = renderPublicMapMainPanel();
    } else {
      syncPublicMapShellSlot(slotEl, "public-train-map", shellState);
      liveStatusEl.textContent = externalFeedStatusText();
    }
    if (!renderOptions.mapOnly) {
      patchPublicMapDetailsPanel();
    }
    syncActivePublicMap();
    focusRequestedIncidentFromURL({ animate: false });
    return true;
  }

  function patchPublicMapDetailsPanel() {
    const detailsPanel = document.getElementById("public-map-details-panel");
    if (!detailsPanel) {
      return false;
    }
    detailsPanel.innerHTML = renderPublicMapDetailsPanel();
    return true;
  }

  function renderPublicMap() {
    const statusBar = document.getElementById("public-map-status-bar");
    const mainPanel = document.getElementById("public-map-main-panel");
    const detailsPanel = document.getElementById("public-map-details-panel");
    const toastRoot = document.getElementById("public-map-toast-root");
    if (statusBar && mainPanel && detailsPanel && toastRoot) {
      statusBar.innerHTML = renderPublicMapStatusBar();
      patchPublicMapMainPanel();
      toastRoot.innerHTML = renderToast();
      return;
    }
    setAppHTML(`
      <div class="shell map-first-shell">
        <section class="status-bar map-top-bar" id="public-map-status-bar">${renderPublicMapStatusBar()}</section>
        <section class="panel map-workspace" id="public-map-main-panel" aria-label="${escapeAttr(t("app_public_map_title"))}">${renderPublicMapMainPanel()}</section>
        <section class="panel" id="public-map-details-panel">${renderPublicMapDetailsPanel()}</section>
      </div>
      ${renderToastRoot("public-map-toast-root")}`);
    patchPublicMapMainPanel();
  }

  function renderPublicStationStatusBar() {
    return renderPublicStatusBar({
      textId: "public-station-status-text",
      actions: [
        publicStatusLink(publicNetworkMapRoot(), t("app_section_map")),
        publicStatusLink(publicIncidentsRoot(), t("app_public_incidents_title")),
        publicStatusLink(publicDashboardRoot(), t("app_open_departures")),
        classicControlLink(),
        publicStatusButton("public-station-refresh", t("app_refresh"), "ghost small", "refresh-current-view"),
      ],
    });
  }

  function renderPublicStationSearchPanel() {
    const emptyMatchesText = scheduleUnavailable()
      ? scheduleUnavailableMessage()
      : state.publicStationQuery
        ? t("app_public_station_no_matches")
        : t("app_public_station_prompt");
    const matches = state.publicStationSearchLoading && !state.publicStationMatches.length
      ? loadingStateHTML(t("app_public_station_search_loading"), "loading-state-inline")
      : state.publicStationMatches.length
      ? state.publicStationMatches.map(renderPublicStationMatch).join("")
      : `<div class="empty">${escapeHtml(emptyMatchesText)}</div>`;
    return `
      <div class="form-grid">
        <div class="field">
          <label>${escapeHtml(t("app_public_station_search_label"))}</label>
          <input id="public-station-query" type="text" value="${escapeAttr(state.publicStationQuery)}" placeholder="${escapeAttr(t("app_public_station_search_placeholder"))}">
        </div>
        <div class="button-row">
          <button class="primary" id="public-station-search">${escapeHtml(t("app_search"))}</button>
        </div>
      </div>
      <div class="divider"></div>
      <h2>${escapeHtml(t("app_public_station_matches"))}</h2>
      <div class="card-list">${matches}</div>
    `;
  }

  function renderPublicStationDeparturesPanel() {
    const departures = state.publicStationDepartures;
    const departureEmptyText = scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_public_station_last_empty");
    const upcomingEmptyText = scheduleUnavailable() ? scheduleUnavailableMessage() : t("app_public_station_upcoming_empty");
    const departuresLoading = state.publicStationDeparturesLoading && !departures;
    const lastDeparture = departuresLoading
      ? loadingStateHTML(t("app_public_station_departures_loading"), "loading-state-inline")
      : departures && departures.lastDeparture ? renderPublicStationDepartureCard(departures.lastDeparture) : `<div class="empty">${escapeHtml(departureEmptyText)}</div>`;
    const upcoming = departuresLoading
      ? loadingStateHTML(t("app_public_station_departures_loading"), "loading-state-inline")
      : departures && Array.isArray(departures.upcoming) && departures.upcoming.length
      ? departures.upcoming.map(renderPublicStationDepartureCard).join("")
      : `<div class="empty">${escapeHtml(upcomingEmptyText)}</div>`;
    return `
      <div class="stack">
        <div class="badge">${escapeHtml(state.publicStationSelected ? `${t("app_public_station_selected")}: ${state.publicStationSelected.name}` : t("app_public_station_prompt"))}</div>
        <div class="split">
          <section class="detail-card">
            <h3>${escapeHtml(t("app_public_station_last"))}</h3>
            <div class="card-list">${lastDeparture}</div>
          </section>
          <section class="detail-card">
            <h3>${escapeHtml(t("app_public_station_upcoming"))}</h3>
            <div class="card-list">${upcoming}</div>
          </section>
        </div>
      </div>
    `;
  }

  function renderPublicNetworkMapStatusBar() {
    return renderCompactMapStatusBar({
      textId: "public-network-map-status-text",
      quickActions: [
        publicStatusButton("public-network-map-refresh", t("app_refresh"), "ghost small", "refresh-current-view"),
      ],
      menuActions: [
        publicStatusLink(publicIncidentsRoot(), t("app_public_incidents_title")),
        classicControlLink(),
      ],
    });
  }

  function networkMapHasStationContext(mapData) {
    if (!mapData || mapData.liveOnly) {
      return false;
    }
    return Boolean(
      (Array.isArray(mapData.stations) && mapData.stations.length) ||
      (Array.isArray(mapData.recentSightings) && mapData.recentSightings.length) ||
      (Array.isArray(mapData.sameDaySightings) && mapData.sameDaySightings.length)
    );
  }

  function renderNetworkMapSightingsCard(mode) {
    const normalizedMode = mode === "mini" ? "mini" : "public";
    const showAll = normalizedMode === "public"
      ? state.publicNetworkMapShowAllSightings
      : state.miniNetworkMapShowAllSightings;
    const toggleId = `${normalizedMode}-network-map-history-toggle`;
    const sightings = activeNetworkMapSightings(state.networkMapData);
    return `
      <div class="map-history-toggle-row">
        <label class="map-history-toggle" for="${escapeAttr(toggleId)}">
          <input id="${escapeAttr(toggleId)}" type="checkbox" data-action="toggle-network-map-history" data-mode="${escapeAttr(normalizedMode)}" ${showAll ? "checked" : ""}>
          <span>${escapeHtml(t("app_network_map_toggle_label"))}</span>
        </label>
        <p class="panel-subtitle">${escapeHtml(showAll ? t("app_network_map_toggle_hint_all") : t("app_network_map_toggle_hint_default"))}</p>
      </div>
      <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
      ${renderStationSightings(sightings)}
    `;
  }

  function renderPublicNetworkMapSightingsCard() {
    return renderNetworkMapSightingsCard("public");
  }

  function renderMiniNetworkMapSightingsCard() {
    return renderNetworkMapSightingsCard("mini");
  }

  function renderPublicNetworkMapPanel() {
    const shellState = networkMapShellState("public-network-map", state.networkMapData);
    const sightingsCard = networkMapHasStationContext(state.networkMapData)
      ? `<section class="detail-card" id="public-network-map-sightings-card">${renderPublicNetworkMapSightingsCard()}</section>`
      : "";
    return `
      <div class="stack">
        <div id="public-network-map-shell-slot">${shellState.html}</div>
        <p class="panel-subtitle map-live-status" id="public-network-map-live-status">${escapeHtml(externalFeedStatusText())}</p>
        ${sightingsCard}
      </div>
    `;
  }

  function patchPublicNetworkMapPanel(options) {
    const mapPanel = document.getElementById("public-network-map-panel");
    if (!mapPanel) {
      return false;
    }
    const slotEl = document.getElementById("public-network-map-shell-slot");
    const liveStatusEl = document.getElementById("public-network-map-live-status");
    const sightingsCardEl = document.getElementById("public-network-map-sightings-card");
    const shellState = networkMapShellState("public-network-map", state.networkMapData);
    const nextSightingsCardHTML = networkMapHasStationContext(state.networkMapData)
      ? renderPublicNetworkMapSightingsCard()
      : "";
    if (!slotEl || !liveStatusEl) {
      mapPanel.innerHTML = renderPublicNetworkMapPanel();
    } else {
      syncPublicMapShellSlot(slotEl, "public-network-map", shellState);
      liveStatusEl.textContent = externalFeedStatusText();
      if (nextSightingsCardHTML && sightingsCardEl) {
        sightingsCardEl.innerHTML = nextSightingsCardHTML;
      } else if (Boolean(nextSightingsCardHTML) !== Boolean(sightingsCardEl)) {
        mapPanel.innerHTML = renderPublicNetworkMapPanel();
      }
    }
    syncActivePublicMap();
    focusRequestedIncidentFromURL({ animate: false });
    bindPublicNetworkMapEvents(mapPanel);
    return true;
  }

  function renderPublicNetworkMap() {
    const statusBar = document.getElementById("public-network-map-status-bar");
    const mapPanel = document.getElementById("public-network-map-panel");
    const toastRoot = document.getElementById("public-network-map-toast-root");
    if (statusBar && mapPanel && toastRoot) {
      statusBar.innerHTML = renderPublicNetworkMapStatusBar();
      patchPublicNetworkMapPanel();
      toastRoot.innerHTML = renderToast();
      return;
    }
    setAppHTML(`
      <div class="shell map-first-shell">
        <section class="status-bar map-top-bar" id="public-network-map-status-bar">${renderPublicNetworkMapStatusBar()}</section>
        <section class="panel map-workspace" id="public-network-map-panel" aria-label="${escapeAttr(t("app_public_network_map_title"))}">${renderPublicNetworkMapPanel()}</section>
      </div>
      ${renderToastRoot("public-network-map-toast-root")}`);
    patchPublicNetworkMapPanel();
    bindPublicNetworkMapEvents();
  }

  function loadingBarHTML() {
    return `<span class="quick-loading-bar" aria-hidden="true"></span>`;
  }

  function loadingStateHTML(message, className) {
    const classes = `loading-state ${className || ""}`.trim();
    return `
      <div class="${escapeAttr(classes)}" role="status" aria-live="polite">
        ${loadingBarHTML()}
        <span class="loading-copy">${escapeHtml(message || t("app_loading"))}</span>
      </div>
    `;
  }

  function publicIncidentListNeedsLoadingUI() {
    return Boolean(state.publicIncidentsLoading && !state.publicIncidentsLoaded && !state.publicIncidents.length);
  }

  function publicIncidentDetailNeedsLoadingUI() {
    if (!state.publicIncidentDetailLoading || !state.publicIncidentDetailLoadingId) {
      return false;
    }
    const detailId = state.publicIncidentDetail && state.publicIncidentDetail.summary
      ? state.publicIncidentDetail.summary.id
      : "";
    return detailId !== state.publicIncidentDetailLoadingId;
  }

  function incidentVoteSummaryLabel(votes) {
    const ongoing = votes && typeof votes.ongoing === "number" ? votes.ongoing : 0;
    const cleared = votes && typeof votes.cleared === "number" ? votes.cleared : 0;
    return `${t("app_public_incidents_vote_ongoing")}: ${ongoing} • ${t("app_public_incidents_vote_cleared")}: ${cleared}`;
  }

  function localizedIncidentActivityName(name) {
    switch (String(name || "").trim().toLowerCase()) {
      case "inspection started":
        return signalLabel("INSPECTION_STARTED");
      case "inspection in carriage":
        return signalLabel("INSPECTION_IN_MY_CAR");
      case "inspection ended":
        return signalLabel("INSPECTION_ENDED");
      default:
        return name || "";
    }
  }

  function renderIncidentQuickVoteButtons(item) {
    if (!state.authenticated || !item || !item.id) {
      return "";
    }
    const voteValue = item.votes && item.votes.userValue ? item.votes.userValue : "";
    return `
      <div class="button-row incident-summary-actions">
        <button class="${voteValue === "ONGOING" ? "secondary small" : "ghost small"}" data-action="incident-vote" data-incident-id="${escapeAttr(item.id)}" data-value="ONGOING">${escapeHtml(t("app_public_incidents_vote_ongoing"))}</button>
        <button class="${voteValue === "CLEARED" ? "secondary small" : "ghost small"}" data-action="incident-vote" data-incident-id="${escapeAttr(item.id)}" data-value="CLEARED">${escapeHtml(t("app_public_incidents_vote_cleared"))}</button>
      </div>
    `;
  }

  function renderIncidentSummaryCard(item) {
    const active = state.publicIncidentSelectedId === item.id;
    const activityAt = item.lastActivityAt || item.lastReportAt;
    const activityName = localizedIncidentActivityName(item.lastActivityName || item.lastReportName || "");
    const activityActor = item.lastActivityActor || item.lastReporter || "";
    return `
      <article class="detail-card incident-card ${active ? "selected-train-card" : ""}">
        <button class="incident-summary-button" data-action="open-incident" data-incident-id="${escapeAttr(item.id)}">
          <div class="station-card-header">
            <h3>${escapeHtml(item.subjectName || "Incident")}</h3>
            <span class="station-selected-pill">${escapeHtml(activityAt ? relativeAgo(activityAt) : "")}</span>
          </div>
          <div class="meta">
            <span>${escapeHtml(activityName)}</span>
            <span>${escapeHtml(t("app_public_incidents_last_reporter", activityActor))}</span>
          </div>
          <div class="meta">
            <span>${escapeHtml(incidentVoteSummaryLabel(item.votes))}</span>
            <span>${escapeHtml(`${item.commentCount || 0} ${t("app_public_incidents_comments").toLowerCase()}`)}</span>
          </div>
        </button>
        ${renderIncidentQuickVoteButtons(item)}
      </article>
    `;
  }

  function renderIncidentEvent(item) {
    return `
      <article class="favorite-card">
        <h3>${escapeHtml(localizedIncidentActivityName(item.name || ""))}</h3>
        <div class="meta">
          <span>${escapeHtml(item.nickname || "")}</span>
          <span>${escapeHtml(relativeAgo(item.createdAt))}</span>
        </div>
        ${item.detail ? `<p>${escapeHtml(item.detail)}</p>` : ""}
      </article>
    `;
  }

  function renderIncidentComment(item) {
    return `
      <article class="favorite-card">
        <h3>${escapeHtml(item.nickname || "")}</h3>
        <div class="meta">
          <span>${escapeHtml(relativeAgo(item.createdAt))}</span>
        </div>
        <p>${escapeHtml(item.body || "")}</p>
      </article>
    `;
  }

  function renderIncidentDetailPanel(detailOverride) {
    const detail = detailOverride || state.publicIncidentDetail;
    const mobileClose = state.publicIncidentMobileLayout
      ? `<div class="incident-detail-mobile-nav"><button class="ghost small" data-action="close-incident-detail">${escapeHtml(t("app_public_incidents_back"))}</button></div>`
      : "";
    if (publicIncidentDetailNeedsLoadingUI()) {
      return `${mobileClose}${loadingStateHTML(t("app_public_incidents_detail_loading"))}`;
    }
    if (!detail || !detail.summary) {
      return `${mobileClose}<div class="empty">${escapeHtml(t("app_public_incidents_detail_empty"))}</div>`;
    }
    const votes = detail.summary.votes || {};
    const comments = Array.isArray(detail.comments) ? detail.comments : [];
    const events = Array.isArray(detail.events) ? detail.events : [];
    const activityAt = detail.summary.lastActivityAt || detail.summary.lastReportAt;
    const activityName = localizedIncidentActivityName(detail.summary.lastActivityName || detail.summary.lastReportName || "");
    const activityActor = detail.summary.lastActivityActor || detail.summary.lastReporter || "";
    const voteValue = votes.userValue || "";
    const draft = incidentCommentDraft(detail.summary.id);
    return `
      <div class="stack">
        ${mobileClose}
        <div class="badge">${escapeHtml(detail.summary.subjectName || "")}</div>
        <section class="detail-card">
          <h3>${escapeHtml(activityName)}</h3>
          <div class="meta">
            <span>${escapeHtml(t("app_public_incidents_last_reporter", activityActor))}</span>
            <span>${escapeHtml(activityAt ? relativeAgo(activityAt) : "")}</span>
          </div>
          <div class="button-row incident-detail-actions">
            <button class="ghost small" data-action="open-incident-map" data-incident-id="${escapeAttr(detail.summary.id)}">${escapeHtml(t("app_public_incidents_show_on_map"))}</button>
          </div>
          ${state.authenticated ? `
            <div class="button-row">
              <button class="${voteValue === "ONGOING" ? "secondary small" : "ghost small"}" data-action="incident-vote" data-incident-id="${escapeAttr(detail.summary.id)}" data-value="ONGOING">${escapeHtml(t("app_public_incidents_vote_ongoing"))}</button>
              <button class="${voteValue === "CLEARED" ? "secondary small" : "ghost small"}" data-action="incident-vote" data-incident-id="${escapeAttr(detail.summary.id)}" data-value="CLEARED">${escapeHtml(t("app_public_incidents_vote_cleared"))}</button>
            </div>
          ` : ""}
          <p class="panel-subtitle">${escapeHtml(incidentVoteSummaryLabel(votes))}</p>
          ${state.authenticated ? `
            <div class="field">
              <label for="incident-comment-body">${escapeHtml(t("app_public_incidents_comment_label"))}</label>
              <textarea id="incident-comment-body" data-incident-id="${escapeAttr(detail.summary.id)}" rows="3" placeholder="${escapeAttr(t("app_public_incidents_comment_placeholder"))}">${escapeHtml(draft)}</textarea>
            </div>
            <div class="button-row">
              <button class="primary small" data-action="submit-incident-comment" data-incident-id="${escapeAttr(detail.summary.id)}">${escapeHtml(t("app_public_incidents_comment_submit"))}</button>
            </div>
          ` : `<p class="panel-subtitle">${escapeHtml(t("app_public_incidents_auth_hint"))}</p>`}
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_public_incidents_activity"))}</h3>
          <div class="card-list">${events.length ? events.map(renderIncidentEvent).join("") : `<div class="empty">${escapeHtml(t("app_public_incidents_activity_empty"))}</div>`}</div>
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_public_incidents_comments"))}</h3>
          <div class="card-list">${comments.length ? comments.map(renderIncidentComment).join("") : `<div class="empty">${escapeHtml(t("app_public_incidents_comments_empty"))}</div>`}</div>
        </section>
      </div>
    `;
  }

  function renderIncidentListHTML() {
    const incidents = Array.isArray(state.publicIncidents) ? state.publicIncidents : [];
    if (publicIncidentListNeedsLoadingUI()) {
      return loadingStateHTML(t("app_public_incidents_loading"));
    }
    return incidents.length
      ? incidents.map(renderIncidentSummaryCard).join("")
      : `<div class="empty">${escapeHtml(t("app_public_incidents_empty"))}</div>`;
  }

  function renderPublicIncidents() {
    syncIncidentLayoutState();
    const incidentListHTML = renderIncidentListHTML();
    const detailVisible = isIncidentDetailVisible();
    const detailPanelClasses = `panel incident-detail-panel ${state.publicIncidentMobileLayout && state.publicIncidentDetailOpen ? "incident-detail-panel-open" : ""}`;
    setAppHTML(`
      <div class="shell">
        ${renderHero(t("app_public_incidents_eyebrow"), t("app_public_incidents_title"), t("app_public_incidents_note"))}
        <section class="status-bar">${renderPublicIncidentsStatusBar()}</section>
        <div class="split incident-layout">
          <section class="panel" id="incident-list-panel">
            <div class="card-list">${incidentListHTML}</div>
          </section>
          <section class="${escapeAttr(detailPanelClasses)}" id="incident-detail-panel" ${detailVisible ? "" : "hidden"} aria-hidden="${detailVisible ? "false" : "true"}">
            ${renderIncidentDetailPanel()}
          </section>
        </div>
      </div>
      ${renderToast()}`);
    bindPublicIncidentEvents(appEl);
  }

  function renderPublicIncidentsStatusBar() {
    return renderPublicStatusBar({
      actions: [
        publicStatusLink(publicNetworkMapRoot(), t("app_section_map")),
        publicStatusLink(publicDashboardRoot(), t("app_open_departures")),
        publicStatusLink(publicStationRoot(), t("app_open_station_search")),
        classicControlLink(),
        publicStatusButton("public-incidents-refresh", t("app_refresh"), "ghost small", "refresh-current-view"),
      ],
    });
  }

  function renderDeferredPublicPage(kind) {
    const isMap = kind === "map";
    const message = isMap ? t("app_public_deferred_map_message") : t("app_public_deferred_incidents_message");
    setAppHTML(`
      <div class="shell">
        ${renderHero("", t("app_public_deferred_title"), t("app_public_deferred_note"))}
        <section class="panel">
          <div class="stack">
            <div class="empty">${escapeHtml(message)}</div>
            <div class="button-row">
              <a class="button ghost" href="${escapeAttr(publicNetworkMapRoot())}">${escapeHtml(t("app_section_map"))}</a>
              <a class="button primary" href="${escapeAttr(publicDashboardRoot())}">${escapeHtml(t("app_open_departures"))}</a>
              <a class="button ghost" href="${escapeAttr(publicStationRoot())}">${escapeHtml(t("app_open_station_search"))}</a>
            </div>
          </div>
        </section>
      </div>
      ${renderToast()}`);
  }

  function renderPublicStationSearch(options) {
    const inputFocus = snapshotFocusedInput("public-station-query");
    setAppHTML(`
      <div class="shell">
        ${renderHero(t("app_public_station_eyebrow"), t("app_public_station_title"), t("app_public_station_note"))}
        <section class="status-bar" id="public-stations-status-bar">${renderPublicStationStatusBar()}</section>
        <div class="stack">
          <section class="panel" id="public-stations-search-panel">${renderPublicStationSearchPanel()}</section>
          <section class="panel" id="public-stations-departures-panel">${renderPublicStationDeparturesPanel()}</section>
        </div>
      </div>
      ${renderToastRoot("public-stations-toast-root")}`);
    bindPublicStationEvents(appEl);
    restoreFocusedInput(inputFocus);
  }

	  function miniStatusTabButton(tab, label) {
	    return `<button class="ghost small" data-action="tab" data-tab="${escapeAttr(tab)}">${escapeHtml(label)}</button>`;
	  }

	  function renderMiniStatusBar() {
	    if (state.tab === "map") {
	      return renderCompactMapStatusBar({
	        textId: "mini-app-status-text",
	        statusText: state.statusText || t("app_status_telegram"),
	        quickActions: [
	          publicStatusButton("global-refresh", t("app_refresh"), "ghost small"),
	        ],
	        menuActions: [
	          miniStatusTabButton("feed", t("app_section_incidents")),
	          miniStatusTabButton("stations", t("app_open_station_search")),
	          miniStatusTabButton("profile", t("app_section_settings")),
	          publicStatusLink(publicRoot(), t("app_open_public")),
	        ],
	      });
	    }
	    return `
	      <span id="mini-app-status-text">${escapeHtml(state.statusText || t("app_status_telegram"))}</span>
	      <div class="button-row">
	        <a class="button ghost small" href="${escapeAttr(publicRoot())}" target="_blank" rel="noreferrer">${escapeHtml(t("app_open_public"))}</a>
	        ${renderSiteLanguageControlHTML()}
	        ${renderRouteCheckInControlsHTML()}
	        ${retryCurrentViewAction("ghost small")}
	        <button class="ghost small" id="global-refresh">${escapeHtml(t("app_refresh"))}</button>
	      </div>
    `;
  }

  function renderMiniNavTabs() {
    return `
      <div class="nav-tabs">
        ${renderTabButton("feed", t("app_section_incidents"))}
        ${renderTabButton("map", t("app_section_map"))}
        ${renderTabButton("stations", t("app_open_station_search"))}
        ${renderTabButton("profile", t("app_section_settings"))}
      </div>
    `;
  }

  function syncSelectedDetailSnapshot() {
    selectedDetailSnapshot = state.selectedTrain
      ? buildStatusDetailSnapshot(state.selectedTrain, true, false)
      : null;
  }

  function syncActiveMiniMap() {
    if (state.tab !== "map") {
      return;
    }
    if (state.mapTrainId) {
      syncMapFromDOM("mini-train-map", state.mapData);
    } else {
      syncMapFromDOM("mini-network-map", state.networkMapData);
    }
    applyMiniMapFollow();
  }

  function resolveMiniMapFollowTarget() {
    return state.mapFollowTrainId || state.mapPinnedTrainId || currentRideTrainId() || "";
  }

  function resolveMiniMapFollowMarkerKey() {
    const mapModel = state.mapTrainId ? state.mapData : state.networkMapData;
    const candidates = movingMapFollowMarkerCandidates(mapModel, resolveMiniMapFollowTarget());
    return candidates.length ? candidates[0] : "";
  }

  function applyMiniMapFollow(controller) {
    if (cfg.mode !== "mini-app" || state.tab !== "map" || movingMapSelectionState().paused) {
      return;
    }
    const mapModel = state.mapTrainId ? state.mapData : state.networkMapData;
    const followTrainId = resolveMiniMapFollowTarget();
    const selection = movingMapSelectionState();
    if (!followTrainId && !selection.markerKey) {
      emitTrainStateTransition("mini-map-follow-skip", {
        followTrainId,
        mapTrainId: state.mapTrainId,
        markerKey: selection.markerKey,
        reason: "no-follow-selection",
      });
      return;
    }
    if (state.mapTrainId && followTrainId && followTrainId !== state.mapTrainId && !selection.markerKey) {
      emitTrainStateTransition("mini-map-follow-skip", {
        followTrainId,
        mapTrainId: state.mapTrainId,
        reason: "map-train-mismatch",
      });
      return;
    }
    const markerKey = resolveMiniMapFollowMarkerKey();
    if (!markerKey) {
      emitTrainStateTransition("mini-map-follow-skip", {
        followTrainId,
        mapTrainId: state.mapTrainId,
        markerKey: "",
        reason: "live-marker-missing",
      });
      applyMovingMapFollow(mapModel, {
        trainId: followTrainId,
        reason: "mini-map-follow-missing",
        missingReason: "mini-map-follow-target-missing",
      }, controller);
      return;
    }
    if (applyMovingMapFollow(mapModel, {
      trainId: followTrainId,
      reason: "mini-map-follow",
      missingReason: "mini-map-follow-target-missing",
    }, controller)) {
      emitTrainStateTransition("mini-map-follow", {
        followTrainId,
        mapTrainId: state.mapTrainId,
        markerKey,
      });
    }
  }

  function renderMiniApp(options) {
    const renderOptions = options || {};
    const settings = state.me && state.me.settings ? state.me.settings : {};
    if (renderOptions.preserveDetail && renderMiniAppPreservingDetail(renderOptions, settings)) {
      return;
    }
    const miniStatusBarClass = state.tab === "map" ? "status-bar map-top-bar" : "status-bar";
    setAppHTML(`
      <div class="shell">
        ${renderHero("", t("app_title"), "")}
        <section class="${miniStatusBarClass}" id="mini-app-status-bar">${renderMiniStatusBar()}</section>
        <section class="panel" id="mini-app-nav-panel">${renderMiniNavTabs()}</section>
        <div class="layout">
          <section class="panel" id="mini-app-main-panel">${renderMiniMain(settings)}</section>
          <section class="panel" id="mini-app-sidebar-panel">${renderMiniSidebar()}</section>
        </div>
      </div>
      ${renderToast()}`);
    syncSelectedDetailSnapshot();
    syncActiveMiniMap();
    bindMiniAppEvents(appEl);
  }

  function renderMiniAppPreservingDetail(options, settings) {
    const statusBar = document.getElementById("mini-app-status-bar");
    const navPanel = document.getElementById("mini-app-nav-panel");
    const mainPanel = document.getElementById("mini-app-main-panel");
    const sidebarPanel = document.getElementById("mini-app-sidebar-panel");
    if (!statusBar || !navPanel || !mainPanel || !sidebarPanel) {
      return false;
    }

    statusBar.classList.toggle("map-top-bar", state.tab === "map");
    statusBar.innerHTML = renderMiniStatusBar();
    navPanel.innerHTML = renderMiniNavTabs();
    const mainPanelPatched = state.tab === "map" && patchMiniMapPanel(mainPanel);
    if (!mainPanelPatched) {
      mainPanel.innerHTML = renderMiniMain(settings);
    }

    const currentPinnedTrainId = detailTargetTrainId();
    if (
      state.selectedTrain
      && currentPinnedTrainId
      && options.previousSelectedTrainId === currentPinnedTrainId
      && selectedTrainId() === currentPinnedTrainId
    ) {
      patchMiniSidebarDetail(sidebarPanel);
    } else {
      sidebarPanel.innerHTML = renderMiniSidebar();
      syncSelectedDetailSnapshot();
      bindMiniAppEvents(sidebarPanel);
    }

    bindMiniAppEvents(statusBar);
    bindMiniAppEvents(navPanel);
    if (!mainPanelPatched) {
      bindMiniAppEvents(mainPanel);
    }
    syncActiveMiniMap();
    return true;
  }

  function renderMiniMain(settings) {
    if (state.tab === "feed" || state.tab === "dashboard") {
      return renderFeedTab();
    }
    if (state.tab === "map") {
      return renderMapTab();
    }
    if (state.tab === "stations") {
      return renderStationsTab();
    }
    return renderProfileTab(settings);
  }

  function selectedTrainSidebarIncludesActions() {
    return state.authenticated;
  }

  function renderMiniSidebar() {
    if (state.selectedTrain) {
      return `<h2>${escapeHtml(t("app_live_status"))}</h2>${renderStatusDetail(state.selectedTrain, selectedTrainSidebarIncludesActions())}`;
    }
    return `
      <h2>${escapeHtml(t("app_live_status"))}</h2>
      <p class="panel-subtitle">${escapeHtml(t("app_status_hint"))}</p>
      <div class="empty">${escapeHtml(t("app_status_empty"))}</div>
    `;
  }

  function renderMiniMapLoadCard(mode) {
    if (!state.mapLoadState || !state.mapLoadState.active || state.mapLoadState.mode !== mode) {
      return "";
    }
    const progress = Math.max(0, Math.min(100, Number(state.mapLoadState.progress) || 0));
    const label = state.mapLoadState.label || mapLoadLabel(mode);
    return `
      <section class="detail-card map-loading-card" aria-live="polite">
        <div class="map-loading-header">
          <h3>${escapeHtml(t("app_map_loading_title"))}</h3>
          <span class="map-loading-value">${escapeHtml(`${progress}%`)}</span>
        </div>
        <p class="panel-subtitle">${escapeHtml(label)}</p>
        <div class="map-loading-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${escapeAttr(progress)}" aria-label="${escapeAttr(label)}">
          <span class="map-loading-progress-bar" style="width:${escapeAttr(progress)}%"></span>
        </div>
      </section>
    `;
  }

  function renderMiniTrainMapContent() {
    const shellState = trainMapShellState("mini-train-map", state.mapData, true);
    const loadingCard = renderMiniMapLoadCard("train");
    if (!shellState.hasTrain) {
      return `
        <div id="mini-map-train-actions">${renderMiniTrainMapActions()}</div>
        ${loadingCard || shellState.html}
      `;
    }
    return `
      <div id="mini-map-summary">${renderRideSummary(publicTrainMapSummaryItem())}</div>
      <div id="mini-map-train-actions">${renderMiniTrainMapActions()}</div>
      <div id="mini-map-shell-slot">${shellState.html}</div>
      <p class="panel-subtitle map-live-status" id="mini-map-live-status">${escapeHtml(externalFeedStatusText())}</p>
    `;
  }

  function renderMiniTrainMapActions() {
    if (currentRideTrainId() || !state.mapPinnedTrainId) {
      return "";
    }
    return `
      <div class="button-row">
        <button class="ghost small" data-action="show-all-trains-map">${escapeHtml(t("app_network_map_show_all"))}</button>
      </div>
    `;
  }

  function renderMiniNetworkMapContent() {
    const shellState = networkMapShellState("mini-network-map", state.networkMapData);
    const loadingCard = renderMiniMapLoadCard("network");
    const sightingsCard = networkMapHasStationContext(state.networkMapData)
      ? `<section class="detail-card" id="mini-network-map-sightings-card">${renderMiniNetworkMapSightingsCard()}</section>`
      : "";
    if (loadingCard && !hasNetworkMapPayload(state.networkMapData)) {
      return `
        <p class="panel-subtitle" id="mini-map-network-note">${escapeHtml(t("app_network_map_note"))}</p>
        ${loadingCard}
      `;
    }
    return `
      <p class="panel-subtitle" id="mini-map-network-note">${escapeHtml(t("app_network_map_note"))}</p>
      <div id="mini-network-map-shell-slot">${shellState.html}</div>
      <p class="panel-subtitle map-live-status" id="mini-map-live-status">${escapeHtml(externalFeedStatusText())}</p>
      ${sightingsCard}
    `;
  }

  function patchMiniMapPanel(mainPanel) {
    if (!mainPanel || state.tab !== "map") {
      return false;
    }
    const tabRoot = mainPanel.querySelector("#mini-map-tab");
    if (!tabRoot) {
      return false;
    }
    const expectedMode = state.mapTrainId ? "train" : "network";
    if (tabRoot.getAttribute("data-map-mode") !== expectedMode) {
      mainPanel.innerHTML = renderMapTab();
      bindMiniAppEvents(mainPanel);
      syncActiveMiniMap();
      return true;
    }

    if (expectedMode === "train") {
      const shellState = trainMapShellState("mini-train-map", state.mapData, true);
      if (!shellState.hasTrain) {
        mainPanel.innerHTML = renderMapTab();
        bindMiniAppEvents(mainPanel);
        return true;
      }
      const summaryEl = mainPanel.querySelector("#mini-map-summary");
      const trainActionsEl = mainPanel.querySelector("#mini-map-train-actions");
      const slotEl = mainPanel.querySelector("#mini-map-shell-slot");
      const liveStatusEl = mainPanel.querySelector("#mini-map-live-status");
      if (!summaryEl || !trainActionsEl || !slotEl || !liveStatusEl) {
        mainPanel.innerHTML = renderMapTab();
        bindMiniAppEvents(mainPanel);
        syncActiveMiniMap();
        return true;
      }
      syncPublicMapShellSlot(slotEl, "mini-train-map", shellState);
      liveStatusEl.textContent = externalFeedStatusText();
      summaryEl.innerHTML = renderRideSummary(publicTrainMapSummaryItem());
      const nextTrainActionsHTML = renderMiniTrainMapActions();
      if (trainActionsEl.innerHTML !== nextTrainActionsHTML) {
        trainActionsEl.innerHTML = nextTrainActionsHTML;
        bindMiniAppEvents(trainActionsEl);
      }
      syncActiveMiniMap();
      return true;
    }

    const shellState = networkMapShellState("mini-network-map", state.networkMapData);
    const noteEl = mainPanel.querySelector("#mini-map-network-note");
    const slotEl = mainPanel.querySelector("#mini-network-map-shell-slot");
    const liveStatusEl = mainPanel.querySelector("#mini-map-live-status");
    const sightingsCardEl = mainPanel.querySelector("#mini-network-map-sightings-card");
    const nextSightingsCardHTML = networkMapHasStationContext(state.networkMapData)
      ? renderMiniNetworkMapSightingsCard()
      : "";
    if (!noteEl || !slotEl || !liveStatusEl) {
      mainPanel.innerHTML = renderMapTab();
      bindMiniAppEvents(mainPanel);
      syncActiveMiniMap();
      return true;
    }
    noteEl.textContent = t("app_network_map_note");
    syncPublicMapShellSlot(slotEl, "mini-network-map", shellState);
    liveStatusEl.textContent = externalFeedStatusText();
    if (nextSightingsCardHTML && sightingsCardEl) {
      sightingsCardEl.innerHTML = nextSightingsCardHTML;
      bindMiniAppEvents(sightingsCardEl);
    } else if (Boolean(nextSightingsCardHTML) !== Boolean(sightingsCardEl)) {
      mainPanel.innerHTML = renderMapTab();
      bindMiniAppEvents(mainPanel);
      syncActiveMiniMap();
      return true;
    }
    syncActiveMiniMap();
    return true;
  }

  function renderFeedTab() {
    const items = state.windowTrains || [];
    const incidents = Array.isArray(state.publicIncidents) ? state.publicIncidents.slice(0, 6) : [];
    const emptyWindowText = scheduleUnavailable() ? scheduleUnavailableMessage() : t("no_trains");
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_public_incidents_title"))}</h2>
        <p class="panel-subtitle">${escapeHtml(t("app_public_incidents_note"))}</p>
        <section class="detail-card">
          <div class="card-list">${incidents.length ? incidents.map(renderIncidentSummaryCard).join("") : `<div class="empty">${escapeHtml(t("app_public_incidents_empty"))}</div>`}</div>
        </section>
        <section class="detail-card">
          ${renderIncidentDetailPanel()}
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_open_departures"))}</h3>
          <p class="panel-subtitle">${escapeHtml(t("app_feed_intro"))}</p>
          <div class="toolbar">
            ${renderWindowButton("now", "window_now")}
            ${renderWindowButton("next_hour", "window_next_hour")}
            ${renderWindowButton("today", "window_today")}
          </div>
          <div class="card-list">${items.length ? items.map((item) => renderTrainCard(item, false)).join("") : `<div class="empty">${escapeHtml(emptyWindowText)}</div>`}</div>
        </section>
      </div>
    `;
  }

  function renderDashboardTab() {
    return renderFeedTab();
  }

  function renderStationsTab() {
    const emptyMatchesText = state.stationQuery.trim()
      ? t("app_public_station_no_matches")
      : t("app_public_station_prompt");
    const departures = Array.isArray(state.stationDepartures) ? state.stationDepartures : [];
    const selectedLabel = state.selectedStation
      ? `${t("app_public_station_selected")}: ${state.selectedStation.name || state.selectedStation.id}`
      : t("app_public_station_prompt");
    const departuresEmptyText = state.selectedStation
      ? t("app_public_station_empty")
      : t("app_public_station_prompt");
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_open_station_search"))}</h2>
        <p class="panel-subtitle">${escapeHtml(t("app_public_station_note"))}</p>
        <section class="detail-card">
          <div class="form-grid">
            <div class="field">
              <label>${escapeHtml(t("app_public_station_search_label"))}</label>
              <input id="station-query" value="${escapeAttr(state.stationQuery)}" placeholder="${escapeAttr(t("app_public_station_search_placeholder"))}">
            </div>
            <div class="button-row">
              <button class="primary" id="station-search">${escapeHtml(t("app_search"))}</button>
            </div>
          </div>
          <div class="divider"></div>
          <h3>${escapeHtml(t("app_public_station_matches"))}</h3>
          <div class="card-list">${state.stations.length ? state.stations.map(renderStationMatch).join("") : `<div class="empty">${escapeHtml(emptyMatchesText)}</div>`}</div>
        </section>
        <section class="detail-card">
          <div class="badge">${escapeHtml(selectedLabel)}</div>
          <div class="divider"></div>
          <h3>${escapeHtml(t("app_public_station_upcoming"))}</h3>
          <div class="card-list">${departures.length ? departures.map((item) => renderStationDepartureCard(item, "browse")).join("") : `<div class="empty">${escapeHtml(departuresEmptyText)}</div>`}</div>
        </section>
        ${renderStationSightingComposer()}
      </div>
    `;
  }

  function renderFeedIncidentPreview() {
    const incidents = Array.isArray(state.publicIncidents) ? state.publicIncidents.slice(0, 3) : [];
    return `
      <section class="detail-card">
        <div class="station-card-header">
          <h3>${escapeHtml(t("app_section_incidents"))}</h3>
          <button class="ghost small" data-action="tab" data-tab="incidents">${escapeHtml(t("app_open_public"))}</button>
        </div>
        <div class="card-list">${incidents.length ? incidents.map(renderIncidentSummaryCard).join("") : `<div class="empty">${escapeHtml(t("app_public_incidents_empty"))}</div>`}</div>
      </section>
    `;
  }

  function renderCurrentRideHighlight() {
    if (!state.currentRide || !state.currentRide.train) {
      return "";
    }
    const ride = state.currentRide;
    const boardingStation = ride.boardingStationName
      ? `<div class="badge">${escapeHtml(`${t("app_from")}: ${ride.boardingStationName}`)}</div>`
      : "";
    return `
      <section class="detail-card">
        <h3>${escapeHtml(t("app_section_my_ride"))}</h3>
        ${boardingStation}
        ${renderRideSummary(ride.train)}
        ${renderActiveRideActionRow(ride)}
      </section>
    `;
  }

  function renderDashboardCheckInTools() {
    const selectedDeparture = selectedCheckInDeparture();
    const selectedTrain = selectedDeparture && selectedDeparture.trainCard ? selectedDeparture.trainCard.train : null;
    const hasSelection = Boolean(selectedDeparture && selectedTrain);
    const trainNumber = hasSelection ? trainNumberLabel(selectedTrain.id) : "0000";
    const dropdown = state.checkInDropdownOpen && state.stationDepartures.length
      ? `
        <div class="checkin-dropdown" id="checkin-dropdown-menu">
          ${state.stationDepartures.map((item) => {
            const train = item.trainCard && item.trainCard.train ? item.trainCard.train : null;
            if (!train) {
              return "";
            }
            const selected = state.selectedCheckInTrainId === train.id;
            return `
              <button
                class="checkin-dropdown-option ${selected ? "selected" : ""}"
                data-action="choose-checkin-train"
                data-train-id="${escapeAttr(train.id)}"
                id="${selected ? "selected-checkin-option" : ""}"
              >
                ${escapeHtml(stationDepartureLabel(item))}
              </button>
            `;
          }).join("")}
        </div>
      `
      : "";
    return `
      <div class="stack">
        <h3>${escapeHtml(t("app_section_checkin"))}</h3>
        <section class="detail-card checkin-single-menu">
          <h3>${escapeHtml(t("btn_checkin_by_station"))}</h3>
          <p class="panel-subtitle">${escapeHtml(t("checkin_station_prompt"))}</p>
          <div class="form-grid">
            <div class="field">
              <input id="station-query" value="${escapeAttr(state.stationQuery)}" placeholder="${escapeAttr(t("app_search_placeholder"))}">
            </div>
            <div class="button-row checkin-search-actions">
              <button class="primary" id="station-search">${escapeHtml(t("app_find_station"))}</button>
              <button class="secondary" data-action="tab-sightings" ${state.selectedStation ? "" : "disabled"}>${escapeHtml(t("app_report_sighting"))}</button>
            </div>
          </div>
          <div class="divider"></div>
          <div class="card-list">${state.stations.length ? state.stations.map(renderStationMatch).join("") : `<div class="empty">${escapeHtml(t("app_station_results"))}</div>`}</div>
          ${state.selectedStation ? `<div class="badge">${escapeHtml(state.selectedStation.name || state.selectedStation.id)}</div>` : ""}
          ${state.selectedStation && !state.stationDepartures.length ? `<div class="empty">${escapeHtml(t("no_station_trains"))}</div>` : ""}
          ${state.stationDepartures.length ? `
            <div class="divider"></div>
            <div class="checkin-selector-stack">
              <button
                class="checkin-hero-button"
                data-action="toggle-checkin-dropdown"
                aria-expanded="${state.checkInDropdownOpen ? "true" : "false"}"
              >
                <span class="checkin-hero-time">${escapeHtml(formatClock(selectedDeparture.passAt))}</span>
                <span class="checkin-hero-arrow">→</span>
                <span class="checkin-hero-destination">${escapeHtml(selectedTrain.toStation)}</span>
              </button>
              ${dropdown}
              <button
                class="checkin-register-button"
                data-action="selected-checkin"
                data-train-id="${escapeAttr(selectedTrain.id)}"
                data-station-id="${escapeAttr(state.selectedStation.id)}"
              >
                <span class="checkin-register-metric">${escapeHtml(trainNumber)}</span>
                <span class="checkin-register-label">${escapeHtml(t("btn_checkin_confirm"))}</span>
                <span class="checkin-register-metric">${escapeHtml(trainNumber)}</span>
              </button>
              <button
                class="checkin-map-button"
                data-action="selected-checkin-map"
                data-train-id="${escapeAttr(selectedTrain.id)}"
              >${escapeHtml(t("app_view_stops_map"))}</button>
            </div>
          ` : ""}
        </section>
      </div>
    `;
  }

  function renderMyRideTab() {
    if (!state.currentRide || !state.currentRide.train) {
      return `<div class="stack"><h2>${escapeHtml(t("app_section_my_ride"))}</h2><div class="empty">${escapeHtml(t("my_ride_none"))}</div></div>`;
    }
    const ride = state.currentRide;
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_section_my_ride"))}</h2>
        ${renderStatusDetail(ride.train, false)}
        ${renderActiveRideActionRow(ride)}
      </div>
    `;
  }

  function renderReportTab() {
    if (!state.currentRide || !state.currentRide.checkIn) {
      return `
        <div class="stack">
          <h2>${escapeHtml(t("app_section_report"))}</h2>
          <div class="empty">${escapeHtml(t("report_requires_checkin"))}</div>
          <div class="button-row">
            <button class="primary" data-action="tab" data-tab="feed">${escapeHtml(t("btn_start_checkin"))}</button>
          </div>
        </div>
      `;
    }
    return `
      <div class="stack">
        <h2>${escapeHtml(t("report_prompt"))}</h2>
        <p class="panel-subtitle">${escapeHtml(t("app_report_notice"))}</p>
        ${renderRideSummary(state.currentRide.train)}
        <div class="split">
          <button class="primary" data-action="report" data-signal="INSPECTION_STARTED">${escapeHtml(t("btn_report_started"))}</button>
          <button class="secondary" data-action="report" data-signal="INSPECTION_IN_MY_CAR">${escapeHtml(t("btn_report_in_car"))}</button>
          <button class="warning" data-action="report" data-signal="INSPECTION_ENDED">${escapeHtml(t("btn_report_ended"))}</button>
        </div>
      </div>
    `;
  }

  function renderRideTab() {
    if (!state.currentRide || !state.currentRide.train) {
      return `
        <div class="stack">
          <h2>${escapeHtml(t("app_section_ride"))}</h2>
          <div class="empty">${escapeHtml(t("my_ride_none"))}</div>
          <div class="button-row">
            <button class="primary" data-action="tab" data-tab="feed">${escapeHtml(t("btn_start_checkin"))}</button>
          </div>
        </div>
      `;
    }
    const ride = state.currentRide;
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_section_ride"))}</h2>
        ${renderStatusDetail(ride.train, false)}
        ${renderActiveRideActionRow(ride)}
        <section class="detail-card">
          <h3>${escapeHtml(t("report_prompt"))}</h3>
          <p class="panel-subtitle">${escapeHtml(t("app_report_notice"))}</p>
          <div class="split">
            <button class="primary" data-action="report" data-signal="INSPECTION_STARTED">${escapeHtml(t("btn_report_started"))}</button>
            <button class="secondary" data-action="report" data-signal="INSPECTION_IN_MY_CAR">${escapeHtml(t("btn_report_in_car"))}</button>
            <button class="warning" data-action="report" data-signal="INSPECTION_ENDED">${escapeHtml(t("btn_report_ended"))}</button>
          </div>
        </section>
      </div>
    `;
  }

  function renderSightingsTab() {
    if (!state.selectedStation) {
      return `
        <div class="stack">
          <h2>${escapeHtml(t("app_section_sightings"))}</h2>
          <div class="empty">${escapeHtml(t("app_sightings_empty"))}</div>
          <div class="button-row">
            <button class="primary" data-action="tab" data-tab="feed">${escapeHtml(t("app_sightings_choose_station"))}</button>
          </div>
        </div>
      `;
    }
    const departures = filteredStationDepartures();
    const selectedDeparture = selectedSightingDeparture();
    const destinationOptions = [`<option value="">${escapeHtml(t("app_station_sighting_destination_any"))}</option>`]
      .concat(state.stationSightingDestinations.map((item) => `<option value="${escapeAttr(item.id)}" ${state.stationSightingDestinationId === item.id ? "selected" : ""}>${escapeHtml(item.name)}</option>`))
      .join("");
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_section_sightings"))}</h2>
        <p class="panel-subtitle">${escapeHtml(t("app_station_sighting_note"))}</p>
        <div class="badge">${escapeHtml(state.selectedStation.name || state.selectedStation.id)}</div>
        <section class="detail-card">
          <div class="sighting-toolbar">
            <div class="field">
              <label>${escapeHtml(t("app_station_sighting_destination_label"))}</label>
              <select id="station-sighting-destination">${destinationOptions}</select>
            </div>
            <div class="button-row">
              <button class="${selectedDeparture ? "secondary" : "washed-success"}" id="station-sighting-submit">${escapeHtml(t("app_station_sighting_submit"))}</button>
            </div>
          </div>
          ${selectedDeparture ? `<div class="badge">${escapeHtml(`${t("app_station_sighting_selected_departure")}: ${selectedDeparture.trainCard.train.fromStation} → ${selectedDeparture.trainCard.train.toStation}`)}</div>` : ""}
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_station_sighting_candidates"))}</h3>
          <div class="card-list">${departures.length ? departures.map((item) => renderStationDepartureCard(item, "sightings")).join("") : `<div class="empty">${escapeHtml(t("app_station_sighting_departures_empty"))}</div>`}</div>
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
          ${renderStationSightings(state.stationRecentSightings)}
        </section>
      </div>
    `;
  }

  function renderMapTab() {
    if (state.mapTrainId) {
      return `
        <div class="stack" id="mini-map-tab" data-map-mode="train">
          <h2>${escapeHtml(t("app_section_map"))}</h2>
          ${renderMiniTrainMapContent()}
        </div>
      `;
    }
    return `
      <div class="stack" id="mini-map-tab" data-map-mode="network">
        <h2>${escapeHtml(t("app_section_map"))}</h2>
        ${renderMiniNetworkMapContent()}
      </div>
    `;
  }

  function renderSettingsTab(settings) {
    return `
      <div class="stack">
        <h2>${escapeHtml(t("settings_title"))}</h2>
        <div class="form-grid">
          <div class="field">
            <label>${escapeHtml(t("settings_alerts_label"))}</label>
            <input id="settings-alerts" type="checkbox" ${settings.alertsEnabled ? "checked" : ""}>
          </div>
          <div class="field">
            <label>${escapeHtml(t("settings_reports_channel_label"))}</label>
            <div class="button-row">
              <a class="button secondary" href="${escapeAttr(t("link_reports_channel"))}" target="_blank" rel="noopener noreferrer">${escapeHtml(t("btn_open_reports_channel"))}</a>
            </div>
          </div>
          <div class="field">
            <label>${escapeHtml(t("settings_alert_style_label"))}</label>
            <select id="settings-style">
              <option value="DETAILED" ${settings.alertStyle === "DETAILED" ? "selected" : ""}>${escapeHtml(t("settings_style_detailed_option"))}</option>
              <option value="DISCREET" ${settings.alertStyle === "DISCREET" ? "selected" : ""}>${escapeHtml(t("settings_style_discreet_option"))}</option>
            </select>
          </div>
          <div class="field">
            <label>${escapeHtml(t("settings_language_label"))}</label>
            <select id="settings-language">
              <option value="EN" ${settings.language === "EN" ? "selected" : ""}>EN</option>
              <option value="LV" ${settings.language === "LV" ? "selected" : ""}>LV</option>
            </select>
          </div>
          <div class="button-row">
            <button class="primary" id="save-settings">${escapeHtml(t("btn_confirm"))}</button>
          </div>
        </div>
      </div>
    `;
  }

  function renderProfileTab(settings) {
    return `
      <div class="stack">
        <h2>${escapeHtml(t("settings_title"))}</h2>
        ${renderSettingsTab(settings)}
      </div>
    `;
  }

  function renderRouteSearchPanel() {
    const originEmptyText = state.originQuery.trim()
      ? t("checkin_route_origin_no_match", state.originQuery.trim())
      : t("route_origin_search_prompt");
    const destinationEmptyText = state.chosenOrigin
      ? (state.destinationQuery.trim()
        ? t("checkin_route_dest_no_match", state.destinationQuery.trim())
        : t("route_dest_search_prompt"))
      : t("app_choose_origin");
    const routeEmptyText = state.chosenDestination
      ? t("no_trains")
      : t("app_choose_destination");
    return `
      <section class="detail-card">
        <h3>${escapeHtml(t("app_find_route"))}</h3>
        <div class="form-grid">
          <div class="field">
            <label>${escapeHtml(t("app_find_origin"))}</label>
            <input id="origin-query" value="${escapeAttr(state.originQuery)}" placeholder="${escapeAttr(t("route_origin_search_prompt"))}">
          </div>
          <div class="button-row">
            <button class="secondary" id="origin-search">${escapeHtml(t("app_find_origin"))}</button>
          </div>
        </div>
        <div class="card-list">${state.originResults.length ? state.originResults.map(renderOriginMatch).join("") : `<div class="empty">${escapeHtml(originEmptyText)}</div>`}</div>
        ${state.chosenOrigin ? `<div class="badge">${escapeHtml(`${t("app_from")}: ${state.chosenOrigin.name || state.chosenOrigin.id}`)}</div>` : ""}
        <div class="divider"></div>
        <div class="form-grid">
          <div class="field">
            <label>${escapeHtml(t("app_find_destination"))}</label>
            <input id="destination-query" value="${escapeAttr(state.destinationQuery)}" placeholder="${escapeAttr(t("route_dest_search_prompt"))}" ${state.chosenOrigin ? "" : "disabled"}>
          </div>
          <div class="button-row">
            <button class="secondary" id="destination-search" ${state.chosenOrigin ? "" : "disabled"}>${escapeHtml(t("app_find_destination"))}</button>
          </div>
        </div>
        <div class="card-list">${state.destinationResults.length ? state.destinationResults.map(renderDestinationMatch).join("") : `<div class="empty">${escapeHtml(destinationEmptyText)}</div>`}</div>
        ${state.chosenDestination ? `<div class="badge">${escapeHtml(`${t("app_to")}: ${state.chosenDestination.name || state.chosenDestination.id}`)}</div>` : ""}
        <div class="button-row">
          <button class="primary" id="route-search" ${state.chosenOrigin && state.chosenDestination ? "" : "disabled"}>${escapeHtml(t("app_find_route"))}</button>
        </div>
        <div class="card-list">${state.routeResults.length ? state.routeResults.map(renderRouteCard).join("") : `<div class="empty">${escapeHtml(routeEmptyText)}</div>`}</div>
      </section>
    `;
  }

  function renderMiniIncidentsTab() {
    const incidents = Array.isArray(state.publicIncidents) ? state.publicIncidents : [];
    return `
      <div class="stack">
        <h2>${escapeHtml(t("app_section_incidents"))}</h2>
        <p class="panel-subtitle">${escapeHtml(t("app_public_incidents_note"))}</p>
        <div class="split">
          <section class="panel">
            <div class="card-list">${incidents.length ? incidents.map(renderIncidentSummaryCard).join("") : `<div class="empty">${escapeHtml(t("app_public_incidents_empty"))}</div>`}</div>
          </section>
          <section class="panel">
            ${renderIncidentDetailPanel()}
          </section>
        </div>
      </div>
    `;
  }

  function renderTrainMapPanel(containerId, mapData, includeSelectionPrompt) {
    if (!mapData || !mapData.train) {
      return `<div class="empty">${escapeHtml(includeSelectionPrompt ? t("app_map_prompt") : t("app_map_empty"))}</div>`;
    }
    const fallbackStatus = state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train
      && state.selectedTrain.trainCard.train.id === mapData.train.id
      ? state.selectedTrain.trainCard.status
      : null;
    const fallbackRiders = state.selectedTrain && state.selectedTrain.trainCard && state.selectedTrain.trainCard.train
      && state.selectedTrain.trainCard.train.id === mapData.train.id
      ? state.selectedTrain.trainCard.riders
      : 0;
    const summaryItem = mapData.trainCard
      ? { trainCard: mapData.trainCard }
      : { trainCard: { train: mapData.train, status: fallbackStatus, riders: fallbackRiders } };
    const stops = Array.isArray(mapData.stops) ? mapData.stops : [];
    const locatedStops = stops.filter((stop) => typeof stop.latitude === "number" && typeof stop.longitude === "number");
    const mapEmpty = locatedStops.length
      ? `
        <div id="${escapeAttr(containerId)}" class="train-map-shell" aria-label="${escapeAttr(t("app_section_map"))}">
          <div class="train-map-viewport"></div>
          <div class="train-map-detail-layer" hidden></div>
        </div>`
      : `<div class="empty">${escapeHtml(t("app_map_empty"))}</div>`;
    const stopList = stops.length
      ? stops.map((stop, index) => renderStopRow(stop, index, mapData)).join("")
      : `<div class="empty">${escapeHtml(t("app_map_empty"))}</div>`;
    return `
      <div class="stack">
        ${renderRideSummary(summaryItem)}
        ${mapEmpty}
        <p class="panel-subtitle map-live-status">${escapeHtml(externalFeedStatusText())}</p>
        ${stops.length && locatedStops.length !== stops.length ? `<p class="panel-subtitle">${escapeHtml(t("app_map_missing_coords"))}</p>` : ""}
        <section class="detail-card">
          <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
          ${renderStationSightings(mapData.stationSightings)}
        </section>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_stop_list"))}</h3>
          <div class="stop-list">${stopList}</div>
        </section>
      </div>
    `;
  }

  function renderNetworkMapPanel(containerId, mapData) {
    const stations = mapData && Array.isArray(mapData.stations) ? mapData.stations : [];
    const mapEmpty = stations.length
      ? `
        <div id="${escapeAttr(containerId)}" class="train-map-shell" aria-label="${escapeAttr(t("app_network_map_title"))}">
          <div class="train-map-viewport"></div>
          <div class="train-map-detail-layer" hidden></div>
        </div>`
      : `<div class="empty">${escapeHtml(t("app_network_map_empty"))}</div>`;
    return `
      <div class="stack">
        ${mapEmpty}
        <p class="panel-subtitle map-live-status">${escapeHtml(externalFeedStatusText())}</p>
        <section class="detail-card">
          <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
          ${renderStationSightings(mapData && mapData.recentSightings)}
        </section>
      </div>
    `;
  }

  function renderStationSightings(items) {
    const sightings = Array.isArray(items) ? items : [];
    if (!sightings.length) {
      return `<div class="empty">${escapeHtml(t("app_station_sighting_empty"))}</div>`;
    }
    return `
      <div class="card-list">
        ${sightings.map((item) => {
          const destination = item.destinationStationName ? ` • ${item.destinationStationName}` : "";
          const matched = item.matchedTrainInstanceId ? ` • ${t("app_station_sighting_matched")}` : "";
          return `
            <article class="sighting-card">
              <strong>${escapeHtml(item.stationName || item.stationId)}${escapeHtml(destination)}</strong>
              <div class="meta">
                <span>${escapeHtml(relativeAgo(item.createdAt))}</span>
                <span>${escapeHtml(item.matchedTrainInstanceId ? t("app_station_sighting_matched") : t("app_station_sighting_unmatched"))}</span>
              </div>
            </article>
          `;
        }).join("")}
      </div>
    `;
  }

  function renderStopRow(stop, index, mapData) {
    const sightings = stopSightings(stop, mapData);
    const key = stopContextKey(stop, index);
    const expanded = state.expandedStopContextKey === key;
    const latestSighting = latestStationSighting(sightings);
    const bucketClass = latestSighting ? `bucket-${sightingRecencyBucket(latestSighting.createdAt)}` : "";
    const content = `
      <div class="stop-index">${escapeHtml(String(index + 1))}</div>
      <div class="stop-copy">
        <strong class="stop-title">${escapeHtml(stop.stationName || stop.stationId || "")}</strong>
        <div class="stop-meta">
          <span class="stop-primary-time">${escapeHtml(stopTimeLabel(stop, index))}</span>
          ${latestSighting ? `<span class="stop-recency-pill ${escapeAttr(bucketClass)}">${escapeHtml(relativeAgo(latestSighting.createdAt))}</span>` : ""}
        </div>
      </div>
    `;
    return `
      <article class="stop-row ${sightings.length ? `with-sighting ${escapeAttr(bucketClass)}` : ""} ${expanded ? "expanded" : ""}">
        <button class="stop-row-button" data-action="toggle-stop-context" data-stop-key="${escapeAttr(key)}" aria-expanded="${expanded ? "true" : "false"}">${content}</button>
        ${expanded ? `
          <div class="stop-context">
            <div class="stop-detail-pills">
              ${renderStopDetailPills(stop, index)}
            </div>
            ${sightings.length ? `
              <h4>${escapeHtml(t("app_sighting_context_title"))}</h4>
              ${renderStationSightings(sightings)}
              <div class="button-row">
                <button class="ghost small" data-action="open-sightings-stop" data-station-id="${escapeAttr(stop.stationId || "")}" data-train-id="${escapeAttr(mapData && mapData.train ? mapData.train.id : "")}">${escapeHtml(t("app_stop_context_open_full"))}</button>
              </div>
            ` : ""}
          </div>
        ` : ""}
      </article>
    `;
  }

  function renderHero(eyebrow, title, body) {
    return `
      <section class="hero">
        ${eyebrow ? `<span class="eyebrow">${escapeHtml(eyebrow)}</span>` : ""}
        <h1>${escapeHtml(title)}</h1>
        ${body ? `<p>${escapeHtml(body)}</p>` : ""}
        ${renderScheduleBanner()}
      </section>
    `;
  }

  function scheduleUnavailable() {
    const schedule = resolvedScheduleMeta();
    return Boolean(schedule && schedule.available === false);
  }

  function scheduleUnavailableMessage() {
    return t("app_schedule_unavailable_detail");
  }

  function renderScheduleBanner() {
    const meta = resolvedScheduleMeta();
    if (scheduleUnavailable()) {
      return `<div class="schedule-banner">${escapeHtml(scheduleUnavailableMessage())}</div>`;
    }
    if (!meta || !meta.fallbackActive || !meta.effectiveServiceDate) {
      return "";
    }
    return `<div class="schedule-banner">${escapeHtml(t("schedule_fallback_notice", meta.effectiveServiceDate))}</div>`;
  }

  function renderTabButton(id, label) {
    return `<button class="tab-button ${state.tab === id ? "active" : ""}" data-action="tab" data-tab="${escapeAttr(id)}">${escapeHtml(label)}</button>`;
  }

  function renderWindowButton(id, labelKey) {
    return `<button class="${state.window === id ? "primary" : "ghost"}" data-action="window" data-window="${escapeAttr(id)}">${escapeHtml(t(labelKey))}</button>`;
  }

  function defaultCheckInTrainId(items) {
    const departures = Array.isArray(items) ? items : [];
    if (!departures.length) {
      return "";
    }
    const nowMs = Date.now();
    let best = departures[0];
    let bestDelta = Math.abs(Date.parse(departures[0].passAt || "") - nowMs);
    departures.slice(1).forEach((item) => {
      const delta = Math.abs(Date.parse(item.passAt || "") - nowMs);
      if (delta < bestDelta) {
        best = item;
        bestDelta = delta;
      }
    });
    return best && best.trainCard && best.trainCard.train ? best.trainCard.train.id || "" : "";
  }

  function selectedCheckInDeparture() {
    const items = Array.isArray(state.stationDepartures) ? state.stationDepartures : [];
    if (!items.length) {
      return null;
    }
    if (state.selectedCheckInTrainId) {
      const explicit = items.find((item) => item.trainCard && item.trainCard.train && item.trainCard.train.id === state.selectedCheckInTrainId);
      if (explicit) {
        return explicit;
      }
    }
    return items.find(Boolean) || null;
  }

  function stationDepartureLabel(item) {
    if (!item || !item.trainCard || !item.trainCard.train) {
      return "";
    }
    return `${formatClock(item.passAt)} → ${item.trainCard.train.toStation}`;
  }

  function trainNumberLabel(trainId) {
    const match = String(trainId || "").match(/(\d{3,5})$/);
    return match ? match[1] : "0000";
  }

  function renderPublicCard(item) {
    const train = item.train;
    return `
      <article class="train-card">
        <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
        <div class="meta">
          <span>${escapeHtml(train.departureAt ? formatDateTime(train.departureAt) : "")}</span>
          <span>${escapeHtml(statusSummary(item.status))}</span>
        </div>
        <div class="card-actions">
          <a class="button ghost small" href="${escapeAttr(`${cfg.basePath}/t/${train.id}`)}">${escapeHtml(t("btn_view_status"))}</a>
        </div>
      </article>
    `;
  }

  function renderPublicStationMatch(item) {
    return `<button class="ghost" data-action="public-station-departures" data-station-id="${escapeAttr(item.id)}">${escapeHtml(item.name)}</button>`;
  }

  function renderPublicStationDepartureCard(item) {
    const card = item.trainCard;
    const train = card.train;
    return `
      <article class="train-card">
        <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
        <div class="meta">
          <span>${escapeHtml(t("station_pass_line", item.stationName, formatClock(item.passAt)))}</span>
          <span>${escapeHtml(statusSummary(card.status))}</span>
        </div>
        <div class="card-actions">
          <a class="button ghost small" href="${escapeAttr(`${cfg.basePath}/t/${train.id}`)}">${escapeHtml(t("btn_view_status"))}</a>
        </div>
      </article>
    `;
  }

  function renderPublicDetail(item) {
    return renderStatusDetail({
      trainCard: {
        train: item.train,
        status: item.status,
        riders: 0,
      },
      timeline: item.timeline || [],
      stationSightings: item.stationSightings || [],
    }, false, true);
  }

  function isCurrentRideTrain(trainId) {
    return Boolean(state.currentRide && state.currentRide.checkIn && state.currentRide.checkIn.trainInstanceId === trainId);
  }

  function canDirectCheckIn(...eligibleUntilAts) {
    let sawFiniteDeadline = false;
    for (const rawValue of eligibleUntilAts) {
      const eligibleUntilMs = Date.parse(rawValue || "");
      if (!Number.isFinite(eligibleUntilMs)) {
        continue;
      }
      sawFiniteDeadline = true;
      if (eligibleUntilMs + (10 * 60 * 1000) <= Date.now()) {
        return false;
      }
    }
    return true;
  }

  function renderPrimaryRideAction(trainId, stationId, eligibleUntilAt, className) {
    return "";
  }

  function renderActiveRideActionRow(ride) {
    return "";
  }

  function renderRideSummary(item) {
    const card = item.trainCard;
    const train = card.train;
    return `
      <article class="detail-card">
        <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
        <div class="meta">
          <span>${escapeHtml(`${formatClock(train.departureAt)} • ${formatClock(train.arrivalAt)}`)}</span>
          <span>${escapeHtml(statusSummary(card.status))}</span>
          <span>${escapeHtml(t("ride_riders", card.riders || 0))}</span>
        </div>
      </article>
    `;
  }

  function filteredStationDepartures() {
    const items = Array.isArray(state.stationDepartures) ? state.stationDepartures : [];
    const destinationName = selectedSightingDestinationName();
    if (!destinationName) {
      return items;
    }
    return items.filter((item) => {
      const train = item && item.trainCard ? item.trainCard.train : null;
      return Boolean(train && String(train.toStation || "").toLowerCase() === destinationName.toLowerCase());
    });
  }

  function selectedSightingDestinationName() {
    if (!state.stationSightingDestinationId) {
      return "";
    }
    const match = (state.stationSightingDestinations || []).find((item) => item.id === state.stationSightingDestinationId);
    return match ? match.name || "" : "";
  }

  function selectedSightingDeparture() {
    if (!state.selectedSightingTrainId) {
      return null;
    }
    return filteredStationDepartures().find((item) => item.trainCard && item.trainCard.train && item.trainCard.train.id === state.selectedSightingTrainId) || null;
  }

  function renderTrainReportButtons(trainId, options) {
    const settings = options || {};
    if (!state.authenticated || !trainId) {
      return "";
    }
    const wrapperClass = settings.wrapperClass || "button-row report-action-row";
    const startedClass = settings.startedClass || "primary small";
    const inCarClass = settings.inCarClass || "secondary small";
    const endedClass = settings.endedClass || "warning small";
    return `
      <div class="${escapeAttr(wrapperClass)}">
        <button class="${escapeAttr(startedClass)}" data-action="report" data-train-id="${escapeAttr(trainId)}" data-signal="INSPECTION_STARTED">${escapeHtml(t("btn_report_started"))}</button>
        <button class="${escapeAttr(inCarClass)}" data-action="report" data-train-id="${escapeAttr(trainId)}" data-signal="INSPECTION_IN_MY_CAR">${escapeHtml(t("btn_report_in_car"))}</button>
        <button class="${escapeAttr(endedClass)}" data-action="report" data-train-id="${escapeAttr(trainId)}" data-signal="INSPECTION_ENDED">${escapeHtml(t("btn_report_ended"))}</button>
      </div>
    `;
  }

  function renderStationSightingComposer() {
    if (!state.authenticated || !state.selectedStation) {
      return "";
    }
    const selectedDeparture = selectedSightingDeparture();
    const destinationOptions = [`<option value="">${escapeHtml(t("app_station_sighting_destination_any"))}</option>`]
      .concat((state.stationSightingDestinations || []).map((item) => `<option value="${escapeAttr(item.id)}" ${state.stationSightingDestinationId === item.id ? "selected" : ""}>${escapeHtml(item.name)}</option>`))
      .join("");
    return `
      <section class="detail-card">
        <h3>${escapeHtml(t("app_station_sighting_title"))}</h3>
        <p class="panel-subtitle">${escapeHtml(t("app_station_sighting_note"))}</p>
        ${selectedDeparture ? `<div class="badge">${escapeHtml(`${t("app_station_sighting_selected_departure")}: ${selectedDeparture.trainCard.train.fromStation} → ${selectedDeparture.trainCard.train.toStation}`)}</div>` : `<div class="empty">${escapeHtml(t("app_station_sighting_select_departure_toast"))}</div>`}
        <div class="form-grid">
          <div class="field">
            <label>${escapeHtml(t("app_station_sighting_destination_label"))}</label>
            <select id="station-sighting-destination">${destinationOptions}</select>
          </div>
          <div class="button-row">
            <button class="secondary" id="station-sighting-submit" ${selectedDeparture ? "" : "disabled"}>${escapeHtml(t("app_station_sighting_submit"))}</button>
          </div>
        </div>
      </section>
    `;
  }

  function renderStationDepartureCard(item, mode) {
    const card = item.trainCard;
    const train = card.train;
    const trainId = train.id;
    const expanded = state.expandedStationContextTrainId === trainId;
    const selected = state.selectedSightingTrainId === trainId;
    const context = expanded ? renderStationDepartureContext(item, mode) : "";
    const primaryActions = mode === "sightings"
      ? ""
      : `<button class="ghost small" data-action="open-status" data-train-id="${escapeAttr(train.id)}">${escapeHtml(t("btn_view_status"))}</button>`;
    return `
      <article class="train-card station-departure-card ${selected ? "selected-train-card" : ""} ${expanded ? "expanded" : ""}">
        <div class="station-card-header">
          <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
          ${selected ? `<span class="station-selected-pill">${escapeHtml(t("app_station_sighting_selected_departure"))}</span>` : ""}
        </div>
        ${primaryActions ? `<div class="card-actions station-primary-actions">
          ${primaryActions}
        </div>` : ""}
        <button class="station-info-toggle ${item.sightingCount > 0 ? "highlighted" : ""}" data-action="toggle-station-context" data-train-id="${escapeAttr(trainId)}" aria-expanded="${expanded ? "true" : "false"}">
          <span class="station-info-main">${escapeHtml(statusSummary(card.status))}</span>
          <span class="station-info-pill">${escapeHtml(sightingMetricLabel(item.sightingCount || 0))}</span>
        </button>
        ${context}
      </article>
    `;
  }

  function renderStationDepartureContext(item, mode) {
    const card = item.trainCard;
    const showSightingSelect = state.authenticated && state.selectedStation;
    const primaryAction = `<button class="ghost small" data-action="open-status" data-train-id="${escapeAttr(card.train.id)}">${escapeHtml(t("btn_view_status"))}</button>`;
    const sightingAction = showSightingSelect
      ? `<button class="${state.selectedSightingTrainId === card.train.id ? "secondary small" : "ghost small"}" data-action="select-sighting-train" data-train-id="${escapeAttr(card.train.id)}">${escapeHtml(state.selectedSightingTrainId === card.train.id ? t("app_station_sighting_selected_departure") : t("app_station_sighting_select_departure"))}</button>`
      : "";
    return `
      <section class="station-context">
        <div class="meta">
          <span>${escapeHtml(t("station_pass_line", item.stationName, formatClock(item.passAt)))}</span>
          <span>${escapeHtml(`${formatClock(card.train.departureAt)} • ${formatClock(card.train.arrivalAt)}`)}</span>
          <span>${escapeHtml(t("ride_riders", card.riders))}</span>
        </div>
        <h4>${escapeHtml(statusSummary(card.status))}</h4>
        ${renderStationSightings(item.sightingContext)}
        <div class="button-row">${primaryAction}${sightingAction}</div>
        ${renderTrainReportButtons(card.train.id)}
      </section>
    `;
  }

  function sightingMetricLabel(count) {
    if (!count) return t("app_sighting_metric_zero");
    if (count === 1) return t("app_sighting_metric_one");
    return t("app_sighting_metric_many", count);
  }

  function resolveTrainCardPayload(item) {
    if (!item) {
      return null;
    }
    if (item.trainCard && item.trainCard.train) {
      return item.trainCard;
    }
    if (item.train) {
      return item;
    }
    return null;
  }

  function renderTrainCard(item, stationMode, stationId) {
    const card = resolveTrainCardPayload(item);
    const train = card && card.train ? card.train : null;
    if (!train) {
      return "";
    }
    return `
      <article class="train-card">
        <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
        <div class="meta">
          <span>${escapeHtml(`${formatClock(train.departureAt)} • ${formatClock(train.arrivalAt)}`)}</span>
          <span>${escapeHtml(statusSummary(card.status))}</span>
          <span>${escapeHtml(t("ride_riders", card.riders))}</span>
        </div>
        <div class="card-actions">
          <button class="ghost small" data-action="open-status" data-train-id="${escapeAttr(train.id)}">${escapeHtml(t("btn_view_status"))}</button>
        </div>
        ${renderTrainReportButtons(train.id)}
      </article>
    `;
  }

  function sortedNetworkMapActivityItems() {
    const items = Array.isArray(state.publicDashboardAll) ? state.publicDashboardAll.filter((item) => item && item.train) : [];
    const active = [];
    const inactive = [];
    items.forEach((item) => {
      if (item.status && item.status.state && item.status.state !== "NO_REPORTS") {
        active.push(item);
      } else {
        inactive.push(item);
      }
    });
    return active.concat(inactive);
  }

  function renderNetworkActivityCard(item) {
    const train = item.train;
    const riderMeta = typeof item.riders === "number"
      ? `<span>${escapeHtml(t("ride_riders", item.riders))}</span>`
      : "";
    return `
      <article class="train-card">
        <div class="badge">${escapeHtml(statusSummary(item.status))}</div>
        <h3>${escapeHtml(train.fromStation)} → ${escapeHtml(train.toStation)}</h3>
        <div class="meta">
          <span>${escapeHtml(`${formatClock(train.departureAt)} • ${formatClock(train.arrivalAt)}`)}</span>
          ${riderMeta}
        </div>
        <div class="card-actions">
          <button class="ghost small" data-action="open-status" data-train-id="${escapeAttr(train.id)}">${escapeHtml(t("btn_view_status"))}</button>
        </div>
      </article>
    `;
  }

  function renderRouteCard(item) {
    const routeKey = `${item.fromStationId}:${item.toStationId}`;
    const isFavorite = state.favorites.some((entry) => `${entry.fromStationId}:${entry.toStationId}` === routeKey);
    return `
      <article class="favorite-card">
        <h3>${escapeHtml(item.fromStationName)} → ${escapeHtml(item.toStationName)}</h3>
        <div class="meta">
          <span>${escapeHtml(`${formatClock(item.fromPassAt)} → ${formatClock(item.toPassAt)}`)}</span>
          <span>${escapeHtml(statusSummary(item.trainCard.status))}</span>
        </div>
        <div class="card-actions">
          <button class="ghost small" data-action="open-status" data-train-id="${escapeAttr(item.trainCard.train.id)}">${escapeHtml(t("btn_view_status"))}</button>
          <button class="ghost small" data-action="${isFavorite ? "remove-favorite" : "save-favorite"}" data-from-station-id="${escapeAttr(item.fromStationId)}" data-from-station-name="${escapeAttr(item.fromStationName)}" data-to-station-id="${escapeAttr(item.toStationId)}" data-to-station-name="${escapeAttr(item.toStationName)}">${escapeHtml(isFavorite ? t("btn_remove_favorite") : t("btn_save_route"))}</button>
        </div>
      </article>
    `;
  }

  function renderFavoriteRoute(item) {
    return `
      <article class="favorite-card">
        <h3>${escapeHtml(item.fromStationName)} → ${escapeHtml(item.toStationName)}</h3>
        <div class="card-actions">
          <button class="ghost small" data-action="favorite-open" data-from-station-id="${escapeAttr(item.fromStationId)}" data-from-station-name="${escapeAttr(item.fromStationName)}" data-to-station-id="${escapeAttr(item.toStationId)}" data-to-station-name="${escapeAttr(item.toStationName)}">${escapeHtml(t("btn_view_status"))}</button>
          <button class="warning small" data-action="remove-favorite" data-from-station-id="${escapeAttr(item.fromStationId)}" data-to-station-id="${escapeAttr(item.toStationId)}">${escapeHtml(t("btn_remove_favorite"))}</button>
        </div>
      </article>
    `;
  }

  function renderStationMatch(item) {
    return `<button class="ghost" data-action="station-departures" data-station-id="${escapeAttr(item.id)}" data-station-name="${escapeAttr(item.name)}">${escapeHtml(item.name)}</button>`;
  }

  function renderOriginMatch(item) {
    return `<button class="ghost" data-action="choose-origin" data-station-id="${escapeAttr(item.id)}" data-station-name="${escapeAttr(item.name)}">${escapeHtml(item.name)}</button>`;
  }

  function renderDestinationMatch(item) {
    return `<button class="ghost" data-action="choose-destination" data-station-id="${escapeAttr(item.id)}" data-station-name="${escapeAttr(item.name)}">${escapeHtml(item.name)}</button>`;
  }

  function statusStateLabel(status) {
    if (!status || status.state === "NO_REPORTS" || !status.lastReportAt) {
      return t("app_detail_state_no_reports");
    }
    if (status.state === "MIXED_REPORTS") {
      return t("app_detail_state_mixed_reports");
    }
    return t("app_detail_state_last_sighting");
  }

  function renderStatusDetailLastUpdate(lastUpdateText) {
    if (!lastUpdateText) {
      return "";
    }
    const ariaLabel = t("app_detail_last_update_aria", lastUpdateText);
    return `
      <span class="detail-last-update-chip" title="${escapeAttr(ariaLabel)}" aria-label="${escapeAttr(ariaLabel)}">
        <span class="detail-last-update-icon" aria-hidden="true">🕒</span>
        <span class="detail-last-update-value">${escapeHtml(lastUpdateText)}</span>
      </span>
    `;
  }

  function buildStatusDetailSnapshot(item, includeActions, publicView) {
    const card = item.trainCard;
    const train = card.train;
    const lastUpdateText = card.status && card.status.lastReportAt ? relativeAgo(card.status.lastReportAt) : "";
    const actionsHTML = includeActions && state.authenticated && !publicView
      ? renderTrainReportButtons(train.id, { wrapperClass: "button-row detail-report-actions" })
      : "";
    const timelineHTML = `
      <ul class="timeline">${(item.timeline || []).length ? item.timeline.map((entry) => `<li><span>${escapeHtml(formatClock(entry.at))}</span><span>${escapeHtml(signalLabel(entry.signal))} (${escapeHtml(String(entry.count))})</span></li>`).join("") : `<li><span>${escapeHtml(t("status_no_recent_events"))}</span></li>`}</ul>
    `;
    return {
      trainId: train.id || "",
      title: `${train.fromStation} → ${train.toStation}`,
      lastUpdateHTML: renderStatusDetailLastUpdate(lastUpdateText),
      metaHTML: `
        <span>${escapeHtml(`${formatClock(train.departureAt)} • ${formatClock(train.arrivalAt)}`)}</span>
        <span>${escapeHtml(statusStateLabel(card.status))}</span>
      `,
      ridersHTML: `<span>${escapeHtml(t("ride_riders", card.riders || 0))}</span>`,
      actionsHTML: actionsHTML,
      timelineHTML: timelineHTML,
      sightingsHTML: `
        <h3>${escapeHtml(t("app_recent_platform_sightings"))}</h3>
        ${renderStationSightings(item.stationSightings)}
      `,
    };
  }

  function renderStatusDetailFromSnapshot(snapshot) {
    return `
      <article class="detail-card detail-status-card" data-role="status-detail" data-train-id="${escapeAttr(snapshot.trainId)}">
        <div class="detail-card-header" data-detail-key="header">
          <h3 data-detail-key="title">${escapeHtml(snapshot.title)}</h3>
          <div class="detail-last-update-slot" data-detail-key="last-update">${snapshot.lastUpdateHTML}</div>
        </div>
        <div class="meta detail-status-meta" data-detail-key="meta">${snapshot.metaHTML}</div>
        <div class="detail-status-riders" data-detail-key="riders">${snapshot.ridersHTML}</div>
        ${snapshot.actionsHTML ? `<div class="card-actions detail-status-actions" data-detail-key="actions">${snapshot.actionsHTML}</div>` : ""}
        <div class="detail-status-section" data-detail-key="timeline">${snapshot.timelineHTML}</div>
        <div class="divider"></div>
        <div class="detail-status-section" data-detail-key="sightings">${snapshot.sightingsHTML}</div>
      </article>
    `;
  }

  function renderStatusDetail(item, includeActions, publicView) {
    return renderStatusDetailFromSnapshot(buildStatusDetailSnapshot(item, includeActions, publicView));
  }

  function flashDetailSection(sectionEl) {
    if (!sectionEl) {
      return;
    }
    sectionEl.classList.remove("detail-section-updated");
    void sectionEl.offsetWidth;
    sectionEl.classList.add("detail-section-updated");
    if (sectionEl._detailFlashTimer) {
      window.clearTimeout(sectionEl._detailFlashTimer);
    }
    sectionEl._detailFlashTimer = window.setTimeout(() => {
      sectionEl.classList.remove("detail-section-updated");
      sectionEl._detailFlashTimer = 0;
    }, 220);
  }

  function replaceDetailSection(cardEl, key, nextHTML, options) {
    const sectionEl = cardEl.querySelector(`[data-detail-key="${key}"]`);
    if (!sectionEl || sectionEl.innerHTML === nextHTML) {
      return false;
    }
    sectionEl.innerHTML = nextHTML;
    if (!options || options.flash !== false) {
      flashDetailSection(sectionEl);
    }
    return true;
  }

  function patchStatusDetailCard(cardEl, previousSnapshot, nextSnapshot) {
    const titleEl = cardEl.querySelector("[data-detail-key='title']");
    if (titleEl && titleEl.textContent !== nextSnapshot.title) {
      titleEl.textContent = nextSnapshot.title;
    }
    replaceDetailSection(cardEl, "last-update", nextSnapshot.lastUpdateHTML, { flash: false });
    replaceDetailSection(cardEl, "meta", nextSnapshot.metaHTML);
    replaceDetailSection(cardEl, "riders", nextSnapshot.ridersHTML);
    const actionsChanged = replaceDetailSection(cardEl, "actions", nextSnapshot.actionsHTML, { flash: false });
    replaceDetailSection(cardEl, "timeline", nextSnapshot.timelineHTML);
    replaceDetailSection(cardEl, "sightings", nextSnapshot.sightingsHTML);
    if (cardEl.getAttribute("data-train-id") !== nextSnapshot.trainId) {
      cardEl.setAttribute("data-train-id", nextSnapshot.trainId);
    }
    if (actionsChanged) {
      const actionsEl = cardEl.querySelector("[data-detail-key='actions']");
      if (actionsEl) {
        bindMiniAppEvents(actionsEl);
      }
    }
  }

  function patchMiniSidebarDetail(sidebarPanel) {
    if (!sidebarPanel || !state.selectedTrain) {
      return;
    }
    const titleEl = sidebarPanel.querySelector("h2");
    if (titleEl) {
      titleEl.textContent = t("app_live_status");
    }
    const cardEl = sidebarPanel.querySelector("[data-role='status-detail']");
    const nextSnapshot = buildStatusDetailSnapshot(state.selectedTrain, selectedTrainSidebarIncludesActions(), false);
    if (!cardEl || !selectedDetailSnapshot || selectedDetailSnapshot.trainId !== nextSnapshot.trainId) {
      sidebarPanel.innerHTML = renderMiniSidebar();
      syncSelectedDetailSnapshot();
      bindMiniAppEvents(sidebarPanel);
      return;
    }
    patchStatusDetailCard(cardEl, selectedDetailSnapshot, nextSnapshot);
    selectedDetailSnapshot = nextSnapshot;
  }

  function bindPublicDashboardEvents() {
    const input = document.getElementById("public-filter");
    if (input) {
      input.addEventListener("input", (event) => {
        state.publicFilterDraft = event.target.value;
      });
      input.addEventListener("keydown", (event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          applyPublicFilter();
        }
      });
    }
    const search = document.getElementById("public-search");
    if (search) {
      search.addEventListener("click", () => {
        applyPublicFilter();
      });
    }
    const refresh = document.getElementById("public-refresh");
    if (refresh) {
      refresh.addEventListener("click", () => {
        runUserAction(async () => {
          await Promise.all([refreshPublicDashboard(), restartExternalFeedIfNeeded()]);
          renderPublicDashboard({ preserveInputFocus: true });
        }, t("app_refresh_success"));
      });
    }
  }

  function bindPublicStationEvents(root) {
    const scope = root || document;
    const stationSearch = scope.querySelector("#public-station-search");
    const stationQueryInput = scope.querySelector("#public-station-query");
    if (stationQueryInput) {
      stationQueryInput.addEventListener("input", (event) => {
        state.publicStationQuery = event.target.value;
      });
    }
    const searchAction = () => runUserAction(async () => {
      await searchPublicStations(state.publicStationQuery);
    }, t("app_public_station_search_success"), { button: stationSearch });
    if (stationSearch) {
      stationSearch.addEventListener("click", searchAction);
    }
    bindEnterAction("public-station-query", searchAction, scope);
    scope.querySelectorAll("[data-action='public-station-departures']").forEach((el) => {
      el.addEventListener("click", () => {
        runUserAction(async () => {
          await refreshPublicStationDepartures(el.getAttribute("data-station-id"));
          renderPublicStationSearch({ preserveInputFocus: true });
        }, t("app_public_station_departures_loaded"), { button: el });
      });
    });
    const refresh = scope.querySelector("#public-station-refresh");
    if (refresh) {
      refresh.addEventListener("click", () => {
        runUserAction(async () => {
          if (state.publicStationSelected) {
            await refreshPublicStationDepartures(state.publicStationSelected.id);
            renderPublicStationSearch({ preserveInputFocus: true });
            return t("app_public_station_departures_loaded");
          }
          if (state.publicStationQuery) {
            await searchPublicStations(state.publicStationQuery);
            return t("app_public_station_search_success");
          }
          return null;
        }, (message) => message, { button: refresh });
      });
    }
  }

  function bindPublicIncidentEvents(root) {
    const scope = root || document;
    scope.querySelectorAll("[data-action='open-incident']").forEach((el) => {
      el.addEventListener("click", () => {
        runUserAction(async () => {
          const incidentId = el.getAttribute("data-incident-id");
          const selectedId = String(incidentId || "").trim();
          if (selectedId && beginIncidentDetailLoading(selectedId)) {
            rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
          }
          await openIncidentDetailView(incidentId);
          rerenderCurrent({ preserveInputFocus: true, preserveDetail: true });
        }, null, { button: el });
      });
    });
    scope.querySelectorAll("[data-action='open-incident-map']").forEach((el) => {
      el.addEventListener("click", () => {
        navigateToIncidentMap(el.getAttribute("data-incident-id"));
      });
    });
    scope.querySelectorAll("[data-action='incident-vote']").forEach((el) => {
      el.addEventListener("click", () => {
        if (!state.authenticated) {
          void beginTelegramLogin();
          return;
        }
        runUserAction(async () => {
          await submitIncidentVote(el.getAttribute("data-incident-id"), el.getAttribute("data-value"));
        }, null, { button: el });
      });
    });
    scope.querySelectorAll("[data-action='submit-incident-comment']").forEach((el) => {
      el.addEventListener("click", () => {
        if (!state.authenticated) {
          void beginTelegramLogin();
          return;
        }
        runUserAction(async () => {
          const input = document.getElementById("incident-comment-body");
          const body = input ? input.value : "";
          await submitIncidentComment(el.getAttribute("data-incident-id"), body);
        }, null, { button: el });
      });
    });
    const commentInput = scope.querySelector("#incident-comment-body");
    if (commentInput) {
      commentInput.addEventListener("input", (event) => {
        setIncidentCommentDraft(event.target.getAttribute("data-incident-id"), event.target.value);
      });
    }
  }

  function bindPublicNetworkMapEvents(root) {
    const scope = root || document;
    scope.querySelectorAll("[data-action='toggle-network-map-history'][data-mode='public']").forEach((el) => {
      el.onchange = (event) => {
        state.publicNetworkMapShowAllSightings = Boolean(event.target.checked);
        rerenderCurrent();
      };
    });
  }

  function bindEnterAction(inputId, action, root) {
    const scope = root || document;
    const input = scope.querySelector(`#${inputId}`);
    if (!input) return;
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      action();
    });
  }

  function bindMiniAppEvents(root) {
    const scope = root || document;
    if (!scope || typeof scope.querySelectorAll !== "function") {
      return;
    }
    scope.querySelectorAll("[data-action='tab']").forEach((el) => {
      el.addEventListener("click", async () => {
        const previousSelectedTrainId = detailTargetTrainId();
        setMiniAppTab(el.getAttribute("data-tab"), "nav-tab");
        if (state.tab === "stations" && state.selectedStation) {
          await fetchStationDepartures(state.selectedStation.id);
        }
        if (state.tab === "map") {
          renderMiniApp({ preserveDetail: true, previousSelectedTrainId });
          try {
            await refreshActiveMapView({
              onLoadStateChange() {
                renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
              },
            });
          } catch (err) {
            rememberErrorStatus(err);
          }
          renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
          return;
        }
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId });
      });
    });
    const globalRefresh = scope.querySelector("#global-refresh");
    if (globalRefresh) {
      globalRefresh.addEventListener("click", () => {
        runUserAction(async () => {
          await Promise.all([refreshMe(), refreshWindowTrains(), refreshPublicIncidents()]);
          if (state.tab === "stations" && state.selectedStation) {
            await fetchStationDepartures(state.selectedStation.id);
          }
          renderMiniApp();
        }, t("app_refresh_success"), { button: globalRefresh });
      });
    }
    scope.querySelectorAll("[data-action='window']").forEach((el) => {
      el.addEventListener("click", () => {
        state.window = el.getAttribute("data-window");
        runUserAction(async () => {
          await refreshWindowTrains();
          renderMiniApp();
        }, t("app_refresh_success"));
      });
    });
    scope.querySelectorAll("[data-action='open-status']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(() => openStatus(el.getAttribute("data-train-id")), t("app_status_loaded")));
    });
    scope.querySelectorAll("[data-action='open-map']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(() => openMap(el.getAttribute("data-train-id")), t("app_map_loaded")));
    });
    scope.querySelectorAll("[data-action='show-all-trains-map']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(() => showAllTrainsMap()));
    });
    scope.querySelectorAll("[data-action='toggle-network-map-history'][data-mode='mini']").forEach((el) => {
      el.addEventListener("change", (event) => {
        state.miniNetworkMapShowAllSightings = Boolean(event.target.checked);
        renderMiniApp({ preserveDetail: true, previousSelectedTrainId: detailTargetTrainId() });
      });
    });
    scope.querySelectorAll("[data-action='toggle-checkin-dropdown']").forEach((el) => {
      el.addEventListener("click", () => {
        if (!state.stationDepartures.length) {
          return;
        }
        state.checkInDropdownOpen = !state.checkInDropdownOpen;
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='choose-checkin-train']").forEach((el) => {
      el.addEventListener("click", () => {
        state.selectedCheckInTrainId = el.getAttribute("data-train-id") || "";
        state.checkInDropdownOpen = false;
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='selected-checkin']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(
        () => checkIn(el.getAttribute("data-train-id"), el.getAttribute("data-station-id")),
        (result) => result,
        { button: el }
      ));
    });
    scope.querySelectorAll("[data-action='selected-checkin-map']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(() => openMap(el.getAttribute("data-train-id")), t("app_map_loaded")));
    });
    scope.querySelectorAll("[data-action='checkin']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(
        () => checkIn(el.getAttribute("data-train-id"), el.getAttribute("data-station-id")),
        (result) => result,
        { button: el }
      ));
    });
    scope.querySelectorAll("[data-action='mute-train']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(
        () => muteTrain(el.getAttribute("data-train-id")),
        (result) => result,
        { button: el }
      ));
    });
    scope.querySelectorAll("[data-action='station-departures']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(async () => {
        await fetchStationDepartures(el.getAttribute("data-station-id"));
      }, t("app_search_complete")));
    });
    scope.querySelectorAll("[data-action='select-sighting-train']").forEach((el) => {
      el.addEventListener("click", () => {
        state.selectedSightingTrainId = el.getAttribute("data-train-id") || "";
        state.expandedStationContextTrainId = state.selectedSightingTrainId;
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='toggle-station-context']").forEach((el) => {
      el.addEventListener("click", () => {
        const trainId = el.getAttribute("data-train-id") || "";
        state.expandedStationContextTrainId = state.expandedStationContextTrainId === trainId ? "" : trainId;
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='open-sightings-train']").forEach((el) => {
      el.addEventListener("click", () => {
        state.stationSightingDestinationId = "";
        state.selectedSightingTrainId = el.getAttribute("data-train-id") || "";
        state.expandedStationContextTrainId = state.selectedSightingTrainId;
        setMiniAppTab("sightings", "open-sightings-train");
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='toggle-stop-context']").forEach((el) => {
      el.addEventListener("click", () => {
        const key = el.getAttribute("data-stop-key") || "";
        state.expandedStopContextKey = state.expandedStopContextKey === key ? "" : key;
        renderMiniApp();
      });
    });
    scope.querySelectorAll("[data-action='open-sightings-stop']").forEach((el) => {
      el.addEventListener("click", () => {
        const stationId = el.getAttribute("data-station-id");
        const trainId = el.getAttribute("data-train-id") || "";
        if (!stationId) {
          return;
        }
        runUserAction(async () => {
          await fetchStationDepartures(stationId);
          state.stationSightingDestinationId = "";
          state.selectedSightingTrainId = trainId;
          state.expandedStationContextTrainId = trainId;
          setMiniAppTab("sightings", "open-sightings-stop");
          renderMiniApp();
        });
      });
    });
    scope.querySelectorAll("[data-action='tab-sightings']").forEach((el) => {
      el.addEventListener("click", () => {
        if (!state.selectedStation) {
          return;
        }
        setMiniAppTab("sightings", "tab-sightings");
        renderMiniApp();
      });
    });
    const stationSightingDestination = scope.querySelector("#station-sighting-destination");
    if (stationSightingDestination) {
      stationSightingDestination.addEventListener("change", (event) => {
        state.stationSightingDestinationId = event.target.value;
        state.selectedSightingTrainId = "";
        state.expandedStationContextTrainId = "";
        renderMiniApp();
      });
    }
    const stationSightingSubmit = scope.querySelector("#station-sighting-submit");
    if (stationSightingSubmit) {
      stationSightingSubmit.addEventListener("click", () => {
        if (!state.selectedSightingTrainId) {
          showToast(t("app_station_sighting_select_departure_toast"), "info");
          return;
        }
        runUserAction(() => submitStationSighting(), (result) => result, { button: stationSightingSubmit });
      });
    }
    scope.querySelectorAll("[data-action='report']").forEach((el) => {
      el.addEventListener("click", () => runUserAction(
        () => submitReport(el.getAttribute("data-signal"), el.getAttribute("data-train-id")),
        (result) => result,
        { button: el }
      ));
    });
    const stationSearch = scope.querySelector("#station-search");
    const stationQueryInput = scope.querySelector("#station-query");
    if (stationQueryInput) {
      stationQueryInput.addEventListener("input", (event) => {
        state.stationQuery = event.target.value;
      });
    }
    const stationSearchAction = () => runUserAction(async () => {
      await fetchStationMatches(state.stationQuery);
    }, t("app_search_complete"), { button: stationSearch });
    if (stationSearch) {
      stationSearch.addEventListener("click", stationSearchAction);
    }
    bindEnterAction("station-query", stationSearchAction, scope);
    const saveSettingsButton = scope.querySelector("#save-settings");
    if (saveSettingsButton) {
      saveSettingsButton.addEventListener("click", () => runUserAction(
        () => saveSettings(),
        (result) => result,
        { button: saveSettingsButton }
      ));
    }
    bindPublicIncidentEvents(scope);
    if (state.checkInDropdownOpen) {
      const selectedOption = scope.querySelector("#selected-checkin-option");
      if (selectedOption && typeof selectedOption.scrollIntoView === "function") {
        window.requestAnimationFrame(() => {
          selectedOption.scrollIntoView({ block: "center", inline: "nearest" });
        });
      }
    }
  }

  function setStatusFromError(err) {
    rememberErrorStatus(err);
    rerenderCurrent({ preserveInputFocus: true });
  }

	  function normalizeLang(raw) {
	    return String(raw || "LV").trim().toUpperCase() === "EN" ? "EN" : "LV";
	  }

  function resolveSignedInLanguage(settings, fallbackLang) {
    if (settings && typeof settings.language === "string" && settings.language.trim()) {
      return normalizeLang(settings.language);
    }
    return normalizeLang(fallbackLang);
  }

  function t(key) {
    const args = Array.prototype.slice.call(arguments, 1);
    let value = state.messages[key] || fallbackMessages[key] || key;
    args.forEach((arg) => {
      value = value.replace(/%[sd]/, String(arg));
    });
    return value;
  }

  function statusSummary(status) {
    if (!status) return t("status_no_reports");
    if (status.state === "MIXED_REPORTS") return t("status_mixed");
    if (status.state === "NO_REPORTS" || !status.lastReportAt) return t("status_no_reports");
    return t("status_last", relativeAgo(status.lastReportAt));
  }

  function signalLabel(signal) {
    if (signal === "INSPECTION_STARTED") return t("event_inspection_started");
    if (signal === "INSPECTION_IN_MY_CAR") return t("event_inspection_in_car");
    if (signal === "INSPECTION_ENDED") return t("event_inspection_ended");
    return signal || t("event_unknown");
  }

  function latestStationSighting(items) {
    return (Array.isArray(items) ? items : []).reduce((latest, item) => {
      if (!latest) {
        return item;
      }
      return new Date(item.createdAt).getTime() > new Date(latest.createdAt).getTime() ? item : latest;
    }, null);
  }

  function sightingBucketColor(bucket) {
    if (bucket === "fresh") return "#d94d1f";
    if (bucket === "warm") return "#0f6b62";
    return "#6c5f52";
  }

  function stopHasSighting(stop, mapData) {
    return stopSightings(stop, mapData).length > 0;
  }

  function stopSightingColor(stop, mapData) {
    const latest = latestStationSighting(stopSightings(stop, mapData));
    if (!latest) {
      return "#9f3d22";
    }
    return sightingBucketColor(sightingRecencyBucket(latest.createdAt));
  }

  function stopContextKey(stop, index) {
    const base = stop && (stop.stationId || stop.stationName || "");
    if (typeof index === "number" && Number.isFinite(index)) {
      return `${base}#${index}`;
    }
    return base;
  }

  function compactMarkerCount(count) {
    const numeric = Number(count) || 0;
    if (numeric <= 0) {
      return "";
    }
    return numeric > 9 ? "9+" : String(numeric);
  }

  function buildPopupCard(options) {
    const sections = Array.isArray(options && options.sections)
      ? options.sections.filter(Boolean).join("")
      : "";
    const actionsHTML = options && options.actionsHTML ? String(options.actionsHTML) : "";
    return `
      <div class="map-popup-card">
        <div class="map-popup-heading">
          <strong>${escapeHtml(options && options.title ? options.title : "")}</strong>
          ${options && options.subtitle ? `<span class="map-popup-subtitle">${escapeHtml(options.subtitle)}</span>` : ""}
        </div>
        ${sections ? `<div class="map-popup-sections">${sections}</div>` : ""}
        ${actionsHTML ? `<div class="map-popup-actions">${actionsHTML}</div>` : ""}
      </div>
    `;
  }

  function handleMapPopupAction(button, options = {}) {
    if (!button || typeof button.getAttribute !== "function") {
      return false;
    }
    if (!options.ignorePopupScope) {
      const popupRoot = typeof button.closest === "function"
        ? (button.closest(".leaflet-popup") || button.closest(".train-map-detail-card"))
        : null;
      if (!popupRoot) {
        return false;
      }
    }
    const action = String(button.getAttribute("data-action") || "").trim();
    if (action === "popup-report-train-signal") {
      const trainId = String(button.getAttribute("data-train-id") || "").trim();
      const signal = String(button.getAttribute("data-signal") || "").trim();
      if (!trainId || !signal) {
        return false;
      }
      const runAction = typeof options.runUserAction === "function" ? options.runUserAction : runUserAction;
      const reportAction = typeof options.submitReport === "function" ? options.submitReport : submitReport;
      runAction(() => reportAction(signal, trainId), (result) => result, { button });
      return true;
    }
	    if (action === "popup-report-station") {
	      const stationId = String(button.getAttribute("data-station-id") || "").trim();
	      if (!stationId) {
	        return false;
	      }
      const runAction = typeof options.runUserAction === "function" ? options.runUserAction : runUserAction;
      const reportAction = typeof options.submitStationReport === "function" ? options.submitStationReport : submitStationReport;
	      runAction(() => reportAction(stationId), (result) => result, { button });
	      return true;
	    }
    if (action === "popup-open-incident") {
      const incidentId = String(button.getAttribute("data-incident-id") || "").trim();
      if (!incidentId || !window.location) {
        return false;
      }
      const href = urlWithIncidentParam(publicIncidentsRoot(), incidentId);
      if (typeof window.location.assign === "function") {
        window.location.assign(href);
      } else {
        window.location.href = href;
      }
      return true;
    }
	    if (action === "popup-submit-location-report") {
      const latitude = Number(button.getAttribute("data-latitude") || button.getAttribute("data-lat") || "");
      const longitude = Number(button.getAttribute("data-longitude") || button.getAttribute("data-lng") || "");
      const popupRoot = typeof button.closest === "function"
        ? (button.closest(".leaflet-popup") || button.closest(".train-map-detail-card"))
        : null;
      const input = popupRoot && typeof popupRoot.querySelector === "function"
        ? popupRoot.querySelector("[data-role='location-report-description']")
        : null;
      const radiusInput = popupRoot && typeof popupRoot.querySelector === "function"
        ? popupRoot.querySelector("[data-role='location-report-radius']")
        : null;
      const radiusMeters = locationReportRadiusMeters(radiusInput && "value" in radiusInput
        ? radiusInput.value
        : button.getAttribute("data-radius-meters"));
      const description = input && "value" in input ? input.value : "";
      if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) {
        return false;
      }
      const runAction = typeof options.runUserAction === "function" ? options.runUserAction : runUserAction;
      const reportAction = typeof options.submitLocationReport === "function" ? options.submitLocationReport : submitLocationReport;
      runAction(() => reportAction({ latitude, longitude, radiusMeters, description }), (result) => result, { button });
      return true;
    }
    if (action === "popup-report-train") {
      return false;
    }
    if (action === "popup-checkin-train") {
      return false;
    }
    if (action === "popup-open-station-sightings") {
      return false;
    }
    return false;
  }

  function popupInfoRow(label, value) {
    if (!value) {
      return "";
    }
    return `
      <div class="map-popup-row">
        <span class="map-popup-label">${escapeHtml(label)}</span>
        <span class="map-popup-value">${escapeHtml(value)}</span>
      </div>
    `;
  }

  function popupListSection(label, items) {
    const values = (Array.isArray(items) ? items : []).filter(Boolean);
    if (!values.length) {
      return "";
    }
    return `
      <div class="map-popup-row">
        <span class="map-popup-label">${escapeHtml(label)}</span>
        <ul class="map-popup-list">
          ${values.map((item) => `<li>${escapeHtml(item)}</li>`).join("")}
        </ul>
      </div>
    `;
  }

  function stopSightings(stop, mapData) {
    if (!mapData || !Array.isArray(mapData.stationSightings) || !stop) {
      return [];
    }
    return mapData.stationSightings.filter((item) => item.stationId === stop.stationId || item.stationName === stop.stationName);
  }

  function stopTimeLabel(stop, index) {
    const parts = [];
    if (stop.departureAt) {
      parts.push(formatClock(stop.departureAt));
    } else if (stop.arrivalAt) {
      parts.push(formatClock(stop.arrivalAt));
    }
    if (!parts.length) {
      parts.push(`#${index + 1}`);
    }
    return parts.join(" • ");
  }

  function renderStopDetailPills(stop, index) {
    const pills = [`<span>${escapeHtml(`#${index + 1}`)}</span>`];
    if (stop.arrivalAt && stop.departureAt) {
      pills.push(`<span>${escapeHtml(`↓ ${formatClock(stop.arrivalAt)}`)}</span>`);
      pills.push(`<span>${escapeHtml(`↑ ${formatClock(stop.departureAt)}`)}</span>`);
    }
    return pills.join("");
  }

  function relativeAgo(raw) {
    const date = new Date(raw);
    const deltaMin = Math.max(0, Math.round((Date.now() - date.getTime()) / 60000));
    if (deltaMin <= 0) return t("app_relative_now");
    if (deltaMin === 1) return t("app_relative_one_min");
    return t("app_relative_many_mins", deltaMin);
  }

  function formatDateTime(raw) {
    const date = new Date(raw);
    return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  }

  function formatClock(raw) {
    const date = typeof raw === "string" ? new Date(raw) : raw;
    if (!(date instanceof Date) || Number.isNaN(date.getTime())) return "";
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function escapeHtml(value) {
    return String(value == null ? "" : value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  function escapeAttr(value) {
    return escapeHtml(value).replaceAll("'", "&#39;");
  }

  function roundMapCoord(value) {
    return Math.round(Number(value) * 1000000) / 1000000;
  }

  if (typeof module === "object" && module.exports) {
    module.exports = {
      __test__: {
        reportsChannelURL,
        mapZoomTier,
        boundsHeightMeters,
        stationMarkerProfile,
        liveTrainMarkerProfile,
        trainMarkerVisualState,
        applyTrainMarkerStateTransition,
        markerMovementTimestampMs,
        markerMovementDurationMs,
        shouldShowSightingTags,
        buildStationMarkerHTML,
        buildLiveTrainMarkerHTML,
        buildTrainPopupHTML,
        liveTrainGpsClass,
        mapReportsEnabled,
        buildStationPopupHTML,
        buildLocationReportDraftHTML,
        reportMutationRenderTarget,
        buildTrainStopPopupHTML,
        buildTrainMapConfig,
        buildNetworkMapConfig,
        buildMatchedLiveItems,
        createMapController,
        mapController,
        bindMapRelayoutListenersWithEnvironment,
        loadMiniAppInitialData,
        bootPublicNetworkMapSurface,
        bootMiniAppAnonymousFallback,
        authenticateMiniApp,
        syncActivePublicMap,
        applyPublicMapFollow,
        applyMiniMapFollow,
        setMovingMapSelection,
        clearMovingMapSelection,
        pauseMovingMapFollow,
        setPublicMapPopupSelection,
        clearPublicMapPopupSelection,
        readTestTicketFromLocation,
        stripTestTicketFromLocation,
        telegramLoginConfigURL,
        telegramLoginLibraryURL,
        telegramLoginScopes,
        telegramLoginRedirectURI,
        telegramLoginOptions,
        telegramLoginAuthURL,
        telegramLoginResultFromMessage,
        ensureTelegramLoginLibrary,
        runTelegramLoginPopup,
        completeTelegramLogin,
        completeTelegramMiniAppLogin,
        consumeTelegramAuthResultFromURL,
        completePendingTelegramAuthResult,
        beginTelegramLogin,
        logout,
        ensurePublicSession,
        applyAuthenticatedSession,
        applyAnonymousSession,
        publicSessionStatusText,
        renderPublicAuthControlsHTML,
        resetState(overrides) {
          if (externalFeedClient && typeof externalFeedClient.stop === "function") {
            try {
              externalFeedClient.stop();
            } catch (_) {}
          }
          externalFeedClient = null;
          if (externalFeedRenderTimer) {
            clearTimeout(externalFeedRenderTimer);
            externalFeedRenderTimer = null;
          }
          if (publicMapLibraryRetryTimer) {
            clearTimeout(publicMapLibraryRetryTimer);
            publicMapLibraryRetryTimer = null;
          }
          publicMapLibraryRetryCount = 0;
          resetStateForTest(overrides);
          mapController.focusedEntityKey = "";
          mapController.openPopupKey = "";
          mapController.pendingPopupKey = "";
          mapController.mapDetailDismissSuppressedUntil = 0;
          mapController.clearMapGestureStart();
          mapController.pendingDocumentTap = null;
          mapController.lastTapProxyAt = 0;
          mapController.lastMarkerInteractionAt = 0;
          mapController.markerIndex.clear();
          mapController.markerState.clear();
          mapController.baseMarkerKeys.clear();
          mapController.sightingMarkerKeys.clear();
          mapController.trainMarkerKeys.clear();
        },
        getState() {
          return JSON.parse(JSON.stringify(Object.assign({}, state, {
            mapFocusedEntityKey: mapController.focusedEntityKey,
            mapOpenDetailKey: mapController.openPopupKey,
            mapDetailDismissSuppressedUntil: mapController.mapDetailDismissSuppressedUntil,
            mapGestureZoomPending: mapController.mapGestureZoomPending,
          })));
        },
        setPinnedDetailTrain,
        clearPinnedDetailTrain,
        clearPinnedMapTrain,
        alignMiniMapToSelectedTrain,
        pauseMiniMapFollow,
        resetLiveClient() {
          clearLiveInvalidation();
          stopLiveRenderTimer();
          liveClient = null;
        },
        applyCurrentRidePayload,
        rideTrainDetailPayload,
        hydrateCurrentRideTrainFromPublic,
        settleCurrentRideAfterCheckIn,
        preferredMapTrainId,
        resolveMiniMapFollowTarget,
        setMiniAppTab,
        showAllTrainsMap,
        sortedNetworkMapActivityTrainIds() {
          return sortedNetworkMapActivityItems().map((item) => item.train.id);
        },
        renderMiniTrainMapContent(overrides) {
          if (overrides) {
            Object.assign(state, overrides);
          }
          return renderMiniTrainMapContent();
        },
        renderFeedTab(overrides) {
          if (overrides) {
            Object.assign(state, overrides);
          }
          return renderFeedTab();
        },
        renderMiniNetworkMapContent(overrides) {
          if (overrides) {
            Object.assign(state, overrides);
          }
          return renderMiniNetworkMapContent();
        },
        renderSettingsTab(settings, messages) {
          state.messages = Object.assign({}, fallbackMessages, messages || {});
          return renderSettingsTab(settings || {});
        },
        resolveTrainPopupAction,
        handleMapPopupAction,
        showToast,
        renderToast,
        runUserAction,
        setActionButtonBusy,
        submitReport,
        submitStationReport,
        submitLocationReport,
        submitIncidentVote,
        submitIncidentComment,
        startRouteCheckIn,
        checkoutRouteCheckIn,
        renderRouteCheckInMenuHTML,
        api,
        publicApi,
        fetchSpacetimePath,
        usesStrictSpacetimePath,
        startExternalFeedIfNeeded,
        restartExternalFeedIfNeeded,
        externalFeedStatusText,
        normalizeCheckInStationId,
        resolveSignedInLanguage,
        renderMyRideTab,
        renderMiniSidebar,
        renderPublicStatusBar,
        renderPublicDashboardStatusBar,
        renderPublicTrainStatusBar,
        renderPublicMapStatusBar,
        renderPublicStationStatusBar,
        renderPublicNetworkMapStatusBar,
        renderPublicIncidentsStatusBar,
        renderPublicIncidents,
        applyPublicDashboardPayload,
        applyPublicServiceDayTrainsPayload,
        filterBundleStations,
        renderPublicNetworkMapPanel,
        renderIncidentSummaryCard,
        loadingStateHTML,
        renderIncidentListHTML,
        renderIncidentDetailPanel: function (detail, overrides) {
          if (overrides) {
            Object.assign(state, overrides);
          }
          state.publicIncidentDetail = detail;
          return renderIncidentDetailPanel();
        },
        selectedIncidentIdFromURL,
        incidentMapURL,
        navigateToIncidentMap,
        focusRequestedIncidentFromURL,
        localizedIncidentActivityName,
        syncIncidentURL,
        syncIncidentLayoutState,
        openIncidentDetailView,
        beginIncidentDetailLoading,
        closeIncidentDetailOverlay,
        handleIncidentPopState,
        sortIncidentSummaries,
        incidentCommentDraft,
        setIncidentCommentDraft,
        clearIncidentCommentDraft,
        renderDataUnavailableContent,
        retryCurrentViewAction,
        retryCurrentView,
        refreshPublicIncidents,
        refreshPublicIncidentDetail,
        refreshMapData,
        refreshNetworkMapData,
        resolvedScheduleMeta,
      },
    };
  }
})();
