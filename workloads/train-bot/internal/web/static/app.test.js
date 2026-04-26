"use strict";

var test = require("node:test");
var assert = require("node:assert/strict");

global.window = {
  TRAIN_APP_CONFIG: {},
  location: {
    href: "https://example.test/pixel-stack/train/app",
    pathname: "/pixel-stack/train/app",
    search: "",
    hash: "",
  },
  history: {
    replaceState: function () {},
  },
  localStorage: {
    getItem: function () {
      return null;
    },
  },
};

global.document = {
  getElementById: function () {
    return null;
  },
  addEventListener: function () {},
};

var app = require("./app.js");
var missingConfigValue = {};

function withAppConfig(overrides, fn) {
  var previous = {};
  Object.keys(overrides).forEach(function (key) {
    previous[key] = Object.prototype.hasOwnProperty.call(global.window.TRAIN_APP_CONFIG, key)
      ? global.window.TRAIN_APP_CONFIG[key]
      : missingConfigValue;
  });
  Object.assign(global.window.TRAIN_APP_CONFIG, overrides);
  var result;
  try {
    result = fn();
  } catch (err) {
    Object.keys(overrides).forEach(function (key) {
      if (previous[key] === missingConfigValue) {
        delete global.window.TRAIN_APP_CONFIG[key];
        return;
      }
      global.window.TRAIN_APP_CONFIG[key] = previous[key];
    });
    throw err;
  }
  return Promise.resolve(result).finally(function () {
    Object.keys(overrides).forEach(function (key) {
      if (previous[key] === missingConfigValue) {
        delete global.window.TRAIN_APP_CONFIG[key];
        return;
      }
      global.window.TRAIN_APP_CONFIG[key] = previous[key];
    });
  });
}

function spacetimeCallURL(name) {
  return "https://stdb.example/v1/database/train-db/call/" + name;
}

function publicTrainStopsURL(basePath, trainId) {
  return basePath + "/api/v1/public/trains/" + encodeURIComponent(trainId) + "/stops";
}

function setWindowLocation(parts) {
  global.window.location = Object.assign({
    href: "https://example.test/pixel-stack/train/app",
    pathname: "/pixel-stack/train/app",
    search: "",
    hash: "",
  }, parts || {});
}

test("renderSettingsTab uses the reports channel URL in settings", function () {
  var html = app.__test__.renderSettingsTab(
    {
      alertsEnabled: true,
      alertStyle: "DETAILED",
      language: "EN",
    }
  );

  assert.match(
    html,
    /href="https:\/\/t\.me\/vivi_kontrole_reports"/
  );
  assert.equal(app.__test__.reportsChannelURL, "https://t.me/vivi_kontrole_reports");
});

test("resolveSignedInLanguage prefers saved settings over Telegram language", function () {
  assert.equal(
    app.__test__.resolveSignedInLanguage({ language: "LV" }, "en"),
    "LV"
  );
  assert.equal(
    app.__test__.resolveSignedInLanguage({ language: "EN" }, "lv"),
    "EN"
  );
  assert.equal(
    app.__test__.resolveSignedInLanguage(null, "lv"),
    "LV"
  );
  assert.equal(
    app.__test__.resolveSignedInLanguage(null, ""),
    "EN"
  );
});

test("stripTestTicketFromLocation removes only the one-time login parameter", function () {
  var replaceCall = null;
  var previousHistory = global.window.history;

  try {
    setWindowLocation({
      href: "https://example.test/pixel-stack/train/app?test_ticket=abc123&view=map#train-1",
      pathname: "/pixel-stack/train/app",
      search: "?test_ticket=abc123&view=map",
      hash: "#train-1",
    });
    global.window.history = {
      replaceState: function (_, __, url) {
        replaceCall = url;
      },
    };

    assert.equal(app.__test__.readTestTicketFromLocation(), "abc123");
    app.__test__.stripTestTicketFromLocation();
  } finally {
    global.window.history = previousHistory;
    setWindowLocation();
  }

  assert.equal(replaceCall, "/pixel-stack/train/app?view=map#train-1");
});

test("authenticateMiniApp consumes a test ticket before loading the signed-in server session", async function () {
  var previousFetch = global.fetch;
  var previousHistory = global.window.history;
  var fetchCalls = [];
  var replaceCall = null;

  try {
    setWindowLocation({
      href: "https://example.test/pixel-stack/train/app?test_ticket=one-time-ticket&view=feed",
      pathname: "/pixel-stack/train/app",
      search: "?test_ticket=one-time-ticket&view=feed",
      hash: "",
    });
    global.window.history = {
      replaceState: function (_, __, url) {
        replaceCall = url;
      },
    };

    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState();
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        if (url === "/pixel-stack/train/api/v1/auth/test") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({
                ok: true,
                lang: "EN",
                spacetime: {
                  enabled: true,
                  host: "https://stdb.example",
                  database: "train-db",
                  token: "test-token",
                  expiresAt: "2099-01-02T00:00:00.000Z",
                  issuer: "",
                  audience: "",
                },
              });
            },
          };
        }
        if (url === "/pixel-stack/train/api/v1/me") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({
                userId: 7001,
                nickname: "Browser agent",
                settings: {
                  alertsEnabled: true,
                  alertStyle: "DETAILED",
                  language: "EN",
                },
                schedule: {
                  available: true,
                },
              });
            },
          };
        }
        if (url === "/pixel-stack/train/api/v1/messages?lang=EN") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({ messages: {} });
            },
          };
        }
        throw new Error("unexpected fetch " + url);
      };

      await app.__test__.authenticateMiniApp();
    });
  } finally {
    global.fetch = previousFetch;
    global.window.history = previousHistory;
    app.__test__.resetLiveClient();
    setWindowLocation();
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/auth/test",
    "/pixel-stack/train/api/v1/me",
    "/pixel-stack/train/api/v1/messages?lang=EN",
  ]);
  assert.equal(replaceCall, "/pixel-stack/train/app?view=feed");
  assert.equal(app.__test__.getState().authenticated, true);
  assert.equal(app.__test__.getState().me.userId, 7001);
});

test("loadMiniAppInitialData runs primary loaders before background loaders", async function () {
  var calls = [];

  await app.__test__.loadMiniAppInitialData({
    awaitBackground: true,
    render: function (options) {
      calls.push(options && options.preserveDetail ? "render:preserve" : "render");
    },
    primaryLoaders: [
      async function () {
        calls.push("me");
      },
    ],
    backgroundLoaders: [
      async function () {
        calls.push("window-trains");
      },
      async function () {
        calls.push("favorites");
      },
      async function () {
        calls.push("dashboard");
      },
    ],
    rememberErrorStatus: function () {},
  });

  assert.deepEqual(calls, [
    "render",
    "me",
    "render:preserve",
    "window-trains",
    "favorites",
    "dashboard",
    "render:preserve",
  ]);
});

test("loadMiniAppInitialData still refreshes background data without primary loaders", async function () {
  var calls = [];

  await app.__test__.loadMiniAppInitialData({
    awaitBackground: true,
    render: function (options) {
      calls.push(options && options.preserveDetail ? "render:preserve" : "render");
    },
    backgroundLoaders: [
      async function () {
        calls.push("window-trains");
      },
    ],
    rememberErrorStatus: function () {},
  });

  assert.deepEqual(calls, [
    "render",
    "render:preserve",
    "window-trains",
    "render:preserve",
  ]);
});

test("mini app state defaults to the feed tab", function () {
  app.__test__.resetState();

  assert.equal(app.__test__.getState().tab, "feed");
});

test("mini app accepts the restored map tab", function () {
  app.__test__.resetState();

  app.__test__.setMiniAppTab("map", "test");

  assert.equal(app.__test__.getState().tab, "map");
});

test("renderFeedTab accepts trainCard-shaped window rows", function () {
  var html = app.__test__.renderFeedTab({
    windowTrains: [
      {
        trainCard: {
          train: {
            id: "demo-train",
            fromStation: "Riga",
            toStation: "Jelgava",
            departureAt: "2026-03-31T09:10:00Z",
            arrivalAt: "2026-03-31T10:05:00Z",
          },
          status: {
            state: "LAST_SIGHTING",
            confidence: "HIGH",
          },
          riders: 2,
        },
      },
    ],
  });

  assert.match(html, /Riga/);
  assert.match(html, /Jelgava/);
  assert.match(html, /btn_view_status/);
});

test("mini train map content renders an inline loader while the primary map payload is pending", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";

  var html = app.__test__.renderMiniTrainMapContent({
    mapTrainId: "train-42",
    mapData: null,
    mapLoadState: {
      active: true,
      mode: "train",
      progress: 68,
      label: "Loading selected train map",
    },
  });

  assert.match(html, /Loading map/);
  assert.match(html, /Loading selected train map/);
  assert.match(html, /progressbar/);
  assert.match(html, /68%/);
});

test("mini network map content renders an inline loader while the primary map payload is pending", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";

  var html = app.__test__.renderMiniNetworkMapContent({
    networkMapData: null,
    mapLoadState: {
      active: true,
      mode: "network",
      progress: 68,
      label: "Loading full network map",
    },
  });

  assert.match(html, /Loading map/);
  assert.match(html, /Loading full network map/);
  assert.match(html, /progressbar/);
  assert.match(html, /68%/);
});

test("markerMovementTimestampMs prefers explicit movement timestamps", function () {
  assert.equal(
    app.__test__.markerMovementTimestampMs({ movementObservedAt: "2026-03-10T18:55:15Z" }),
    Date.parse("2026-03-10T18:55:15Z")
  );
});

test("markerMovementDurationMs caps long gaps for the train live overlay", function () {
  assert.equal(
    app.__test__.markerMovementDurationMs(
      Date.parse("2026-03-10T18:55:00Z"),
      Date.parse("2026-03-10T18:55:10Z"),
      10000
    ),
    2500
  );
});

test("mapZoomTier switches between far, compact, and detail tiers", function () {
  assert.equal(app.__test__.mapZoomTier(12), "far");
  assert.equal(app.__test__.mapZoomTier(14), "compact");
  assert.equal(app.__test__.mapZoomTier(15), "detail");
});

test("station and live train marker profiles adapt to wide and detail scopes", function () {
  var farStation = app.__test__.stationMarkerProfile({ zoom: 12, visibleHeightMeters: 1000 });
  var compactStation = app.__test__.stationMarkerProfile({ zoom: 14, visibleHeightMeters: 525 });
  var detailStation = app.__test__.stationMarkerProfile({ zoom: 15, visibleHeightMeters: 50 });

  assert.equal(farStation.tier, "far");
  assert.equal(compactStation.tier, "compact");
  assert.equal(detailStation.tier, "detail");
  assert.ok(farStation.markerSize < compactStation.markerSize);
  assert.ok(compactStation.markerSize < detailStation.markerSize);

  var farTrain = app.__test__.liveTrainMarkerProfile({ zoom: 12 });
  var compactTrain = app.__test__.liveTrainMarkerProfile({ zoom: 14 });
  var detailTrain = app.__test__.liveTrainMarkerProfile({ zoom: 15 });

  assert.equal(farTrain.showLabel, true);
  assert.equal(compactTrain.showLabel, true);
  assert.equal(compactTrain.compact, true);
  assert.equal(detailTrain.compact, false);
  assert.deepEqual(detailTrain.iconSize, [52, 30]);
});

test("train marker HTML keeps the train number visible across zoom tiers", function () {
  var farHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "6321",
        updatedAt: "2026-03-10T18:55:15Z",
        isGpsActive: true,
      },
      status: {
        uniqueReporters: 2,
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 12 })
  );
  var compactHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "6321",
        updatedAt: "2026-03-10T18:55:15Z",
        isGpsActive: true,
      },
      status: {
        uniqueReporters: 2,
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 14 })
  );
  var detailHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "6321",
        updatedAt: "2026-03-10T18:55:15Z",
        isGpsActive: true,
      },
      status: {
        uniqueReporters: 2,
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 15 })
  );

  assert.match(farHtml, /map-train-marker-far/);
  assert.match(farHtml, /map-marker-label/);
  assert.match(farHtml, />6321</);
  assert.match(compactHtml, /map-train-marker-compact/);
  assert.match(compactHtml, /map-marker-label/);
  assert.match(detailHtml, /map-train-marker-detail/);
  assert.match(detailHtml, /map-marker-label/);
});

test("train marker HTML preserves contrast-driving state classes", function () {
  var freshHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "7101",
        updatedAt: new Date().toISOString(),
        isGpsActive: true,
      },
      status: {
        state: "MOVING",
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 15 })
  );
  var warmHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "7102",
        updatedAt: new Date(Date.now() - (5 * 60 * 1000)).toISOString(),
        isGpsActive: true,
      },
      status: {
        state: "NO_REPORTS",
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 14 })
  );
  var staleHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "7103",
        updatedAt: new Date(Date.now() - (10 * 60 * 1000)).toISOString(),
        isGpsActive: true,
      },
      status: {
        state: "NO_REPORTS",
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 15 })
  );
  var scheduledHtml = app.__test__.buildLiveTrainMarkerHTML(
    {
      external: {
        trainNumber: "7104",
        updatedAt: "",
        isGpsActive: false,
      },
      status: {
        state: "MOVING",
      },
    },
    app.__test__.liveTrainMarkerProfile({ zoom: 12 })
  );

  assert.match(freshHtml, /gps-fresh/);
  assert.match(freshHtml, /crew-active/);
  assert.match(warmHtml, /gps-warm/);
  assert.match(warmHtml, /crew-idle/);
  assert.match(staleHtml, /gps-stale/);
  assert.match(staleHtml, /map-marker-label/);
  assert.match(scheduledHtml, /gps-scheduled/);
  assert.match(scheduledHtml, /crew-active/);
});

test("buildTrainPopupHTML shows direct report actions for the signed-in mini app", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    authenticated: true,
    currentRide: {
      checkIn: {
        trainInstanceId: "train-current",
      },
    },
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainPopupHTML({
    trainId: "train-current",
    localMatch: {
      train: {
        id: "train-current",
        arrivalAt: "2099-03-10T19:15:00Z",
      },
    },
    external: {
      trainNumber: "6321",
      origin: "Riga",
      destination: "Jelgava",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Torņakalns",
        departureTime: "2026-03-10T19:02:00Z",
      },
    },
    status: {
      uniqueReporters: 1,
      state: "MOVING",
    },
    timeline: [],
    sightings: [],
  });

  assert.match(html, /popup-report-train-signal/);
  assert.match(html, /data-train-id="train-current"/);
  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainPopupHTML keeps report actions available for matched trains without restoring check in", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    currentRide: {
      checkIn: {
        trainInstanceId: "train-other",
      },
    },
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainPopupHTML({
    trainId: "train-next",
    localMatch: {
      train: {
        id: "train-next",
        arrivalAt: "2099-03-10T19:15:00Z",
      },
      stationId: "riga",
    },
    external: {
      trainNumber: "6322",
      origin: "Riga",
      destination: "Tukums",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Zasulauks",
        departureTime: "2026-03-10T19:02:00Z",
      },
    },
    status: {
      uniqueReporters: 0,
      state: "NO_REPORTS",
    },
    timeline: [],
    sightings: [],
  });

  assert.match(html, /popup-report-train-signal/);
  assert.match(html, /data-train-id="train-next"/);
  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainPopupHTML keeps report actions when the map train is identified only by train id", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainPopupHTML({
    trainId: "train-next",
    localMatch: null,
    external: {
      trainNumber: "6322",
      origin: "Riga",
      destination: "Tukums",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Riga",
        stationId: "riga",
        departureTime: "2026-03-10T19:02:00Z",
      },
    },
    status: {
      uniqueReporters: 0,
      state: "NO_REPORTS",
    },
    timeline: [],
    sightings: [],
  });

  assert.match(html, /popup-report-train-signal/);
  assert.match(html, /data-train-id="train-next"/);
  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainPopupHTML keeps report actions when station preservation is disabled", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = false;
  app.__test__.resetState({
    authenticated: true,
    selectedStation: {
      id: "riga",
      name: "Riga",
    },
    stationDepartures: [
      {
        trainCard: {
          train: {
            id: "train-next",
          },
        },
      },
    ],
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainPopupHTML({
    trainId: "train-next",
    localMatch: {
      train: {
        id: "train-next",
        arrivalAt: "2099-03-10T19:15:00Z",
      },
    },
    external: {
      trainNumber: "6322",
      origin: "Riga",
      destination: "Tukums",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Zasulauks",
        departureTime: "2026-03-10T19:02:00Z",
      },
    },
    status: {
      uniqueReporters: 0,
      state: "NO_REPORTS",
    },
    timeline: [],
    sightings: [],
  });

  assert.match(html, /popup-report-train-signal/);
  assert.match(html, /data-train-id="train-next"/);
  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainPopupHTML stays informational for public or unmatched popups", function () {
  global.window.TRAIN_APP_CONFIG.mode = "public-map";
  app.__test__.resetState({
    authenticated: false,
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var publicHtml = app.__test__.buildTrainPopupHTML({
    trainId: "train-public",
    localMatch: {
      train: {
        id: "train-public",
        arrivalAt: "2099-03-10T19:15:00Z",
      },
    },
    external: {
      trainNumber: "7001",
      origin: "Riga",
      destination: "Jelgava",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Torņakalns",
      },
    },
    status: null,
    timeline: [],
    sightings: [],
  });

  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    authenticated: true,
    messages: {
      btn_report_inspection: "Report inspection",
      btn_checkin_confirm: "Check in",
    },
  });

  var unmatchedHtml = app.__test__.buildTrainPopupHTML({
    trainId: "",
    localMatch: null,
    external: {
      trainNumber: "7002",
      origin: "Riga",
      destination: "Sigulda",
      updatedAt: "2026-03-10T18:55:15Z",
      nextStop: {
        title: "Jugla",
      },
    },
    status: null,
    timeline: [],
    sightings: [],
  });

  assert.doesNotMatch(publicHtml, /popup-(report|checkin)-train/);
  assert.doesNotMatch(unmatchedHtml, /popup-(report|checkin)-train/);
});

test("handleMapPopupAction submits a direct train report from a popup button", async function () {
  var reportCall = null;
  var runUserActionCalled = false;

  var reportHandled = app.__test__.handleMapPopupAction({
    getAttribute: function (name) {
      if (name === "data-action") {
        return "popup-report-train-signal";
      }
      if (name === "data-train-id") {
        return "train-next";
      }
      if (name === "data-signal") {
        return "INSPECTION_STARTED";
      }
      return "";
    },
    closest: function (selector) {
      return selector === ".leaflet-popup" ? {} : null;
    },
  }, {
    runUserAction: async function (action) {
      runUserActionCalled = true;
      await action();
    },
    submitReport: async function (signal, trainId) {
      reportCall = { signal: signal, trainId: trainId };
      return "Reported.";
    },
  });

  assert.equal(reportHandled, true);
  assert.equal(runUserActionCalled, true);
  assert.deepEqual(reportCall, {
    signal: "INSPECTION_STARTED",
    trainId: "train-next",
  });
});

test("handleMapPopupAction ignores retired popup check in actions", async function () {
  var checkInCall = null;

  var handled = app.__test__.handleMapPopupAction({
    getAttribute: function (name) {
      if (name === "data-action") {
        return "popup-checkin-train";
      }
      if (name === "data-train-id") {
        return "train-next";
      }
      if (name === "data-station-id") {
        return null;
      }
      return "";
    },
    closest: function (selector) {
      return selector === ".leaflet-popup" ? {} : null;
    },
  }, {
    runUserAction: async function (action) {
      await action();
    },
    checkIn: async function (trainId, stationId, source) {
      checkInCall = { trainId: trainId, stationId: stationId, source: source };
      return "Checked in.";
    },
  });

  assert.equal(handled, false);
  assert.equal(checkInCall, null);
});

test("resolveTrainPopupAction returns a direct train report action without inferring a check in", async function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    mapData: {
      train: {
        id: "train-next",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.9496,
          longitude: 24.1052,
        },
        {
          stationId: "jelgava",
          stationName: "Jelgava",
          latitude: 56.6516,
          longitude: 23.7128,
        },
      ],
      stationSightings: [],
    },
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var action = app.__test__.resolveTrainPopupAction({
    trainId: "train-next",
    localMatch: null,
    external: {
      trainNumber: "6322",
      position: { lat: 56.6518, lng: 23.7131 },
      updatedAt: "2026-03-10T18:55:15Z",
    },
    status: null,
    timeline: [],
    sightings: [],
  });
  assert.ok(action);
  assert.equal(action.action, "popup-report-train-signal");
  assert.equal(action.trainId, "train-next");
  assert.equal(action.signal, "INSPECTION_STARTED");
  assert.equal(action.className, "primary small");
});

test("resolveTrainPopupAction ignores nearest-station hints and still returns a direct report action", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    networkMapData: {
      stations: [
        {
          id: "riga",
          name: "Riga",
          latitude: 56.9496,
          longitude: 24.1052,
        },
        {
          id: "jelgava",
          name: "Jelgava",
          latitude: 56.6516,
          longitude: 23.7128,
        },
      ],
      recentSightings: [],
    },
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var action = app.__test__.resolveTrainPopupAction({
    trainId: "train-next",
    localMatch: null,
    external: {
      trainNumber: "6322",
      position: { lat: 56.9497, lng: 24.1054 },
      updatedAt: "2026-03-10T18:55:15Z",
    },
    status: null,
    timeline: [],
    sightings: [],
  });

  assert.ok(action);
  assert.equal(action.action, "popup-report-train-signal");
  assert.equal(action.trainId, "train-next");
  assert.equal(action.signal, "INSPECTION_STARTED");
  assert.equal(action.className, "primary small");
});

test("resolveTrainPopupAction ignores live stop hints and still returns a direct report action", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    networkMapData: {
      stations: [],
      recentSightings: [],
    },
    externalFeed: {
      enabled: true,
      activeStops: [],
    },
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var action = app.__test__.resolveTrainPopupAction({
    trainId: "train-next",
    localMatch: null,
    external: {
      trainNumber: "6322",
      currentStop: {
        title: "Riga",
        stationId: "riga",
      },
      nextStop: {
        title: "Jelgava",
        stationId: "jelgava",
      },
      updatedAt: "2026-03-10T18:55:15Z",
    },
    status: null,
    timeline: [],
    sightings: [],
  });

  assert.ok(action);
  assert.equal(action.action, "popup-report-train-signal");
  assert.equal(action.trainId, "train-next");
  assert.equal(action.signal, "INSPECTION_STARTED");
  assert.equal(action.className, "primary small");
});

test("parked ride mutation routes are not mapped to the strict live client transport", function () {
  assert.equal(app.__test__.usesStrictSpacetimePath("/checkins/current", { method: "PUT" }), false);
  assert.equal(app.__test__.usesStrictSpacetimePath("/checkins/current", { method: "DELETE" }), false);
  assert.equal(app.__test__.usesStrictSpacetimePath("/checkins/current/undo", { method: "POST" }), false);
  assert.equal(app.__test__.usesStrictSpacetimePath("/trains/train-next/reports", { method: "POST" }), false);
  assert.equal(app.__test__.usesStrictSpacetimePath("/stations/riga/sightings", { method: "POST" }), false);
  assert.equal(app.__test__.usesStrictSpacetimePath("/incidents/incident-1/votes", { method: "POST" }), false);
});

test("fetchSpacetimePath hydrates live current ride reads with the public train stops payload", async function () {
  var previousLiveClient = global.window.TrainAppLiveClient;
  var previousFetch = global.fetch;
  var currentRideCalls = 0;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        spacetimeAuth: {
          enabled: true,
          host: "https://stdb.example",
          database: "train-db",
          token: "token",
          expiresAt: "2099-01-01T00:00:00.000Z",
          issuer: "",
          audience: "",
        },
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trainCard: {
                train: {
                  id: "train-next",
                  fromStation: "Riga",
                  toStation: "Jelgava",
                  departureAt: "2026-03-27T10:39:00Z",
                  arrivalAt: "2026-03-27T11:21:00Z",
                },
                status: {
                  state: "MOVING",
                },
                riders: 3,
              },
              timeline: [],
              stationSightings: [],
              stops: [],
            });
          },
        };
      };

      global.window.TrainAppLiveClient = {
        create: function () {
          return {
            connect: async function () {
              return true;
            },
            onInvalidate: function () {
              return function () {};
            },
            currentRide: async function () {
              currentRideCalls += 1;
              return {
                currentRide: {
                  checkIn: {
                    trainInstanceId: "train-next",
                    boardingStationId: "riga",
                  },
                  train: null,
                  boardingStationId: "riga",
                  boardingStationName: "",
                },
              };
            },
          };
        },
      };

      var payload = await app.__test__.fetchSpacetimePath("/checkins/current", { method: "GET" }, false);

      assert.equal(payload.currentRide.checkIn.trainInstanceId, "train-next");
      assert.equal(payload.currentRide.train.trainCard.train.id, "train-next");
      assert.equal(payload.currentRide.train.trainCard.riders, 3);
    });
  } finally {
    app.__test__.resetLiveClient();
    global.window.TrainAppLiveClient = previousLiveClient;
    global.fetch = previousFetch;
  }

  assert.equal(currentRideCalls, 1);
  assert.deepEqual(fetchCalls, [
    publicTrainStopsURL("/pixel-stack/train", "train-next"),
  ]);
});

test("fetchSpacetimePath hydrates live bootstrap reads with the public train stops payload", async function () {
  var previousLiveClient = global.window.TrainAppLiveClient;
  var previousFetch = global.fetch;
  var bootstrapCalls = 0;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        spacetimeAuth: {
          enabled: true,
          host: "https://stdb.example",
          database: "train-db",
          token: "token",
          expiresAt: "2099-01-01T00:00:00.000Z",
          issuer: "",
          audience: "",
        },
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trainCard: {
                train: {
                  id: "train-next",
                  fromStation: "Riga",
                  toStation: "Jelgava",
                  departureAt: "2026-03-27T10:39:00Z",
                  arrivalAt: "2026-03-27T11:21:00Z",
                },
                status: {
                  state: "MOVING",
                },
                riders: 3,
              },
              timeline: [],
              stationSightings: [],
              stops: [],
            });
          },
        };
      };

      global.window.TrainAppLiveClient = {
        create: function () {
          return {
            connect: async function () {
              return true;
            },
            onInvalidate: function () {
              return function () {};
            },
            bootstrapMe: async function () {
              bootstrapCalls += 1;
              return {
                userId: "telegram:77",
                stableUserId: "telegram:77",
                settings: {
                  language: "EN",
                },
                currentRide: {
                  checkIn: {
                    trainInstanceId: "train-next",
                    boardingStationId: "riga",
                  },
                  train: null,
                  boardingStationId: "riga",
                  boardingStationName: "",
                },
              };
            },
          };
        },
      };

      var payload = await app.__test__.fetchSpacetimePath("/me", { method: "GET" }, false);

      assert.equal(payload.userId, "telegram:77");
      assert.equal(payload.currentRide.checkIn.trainInstanceId, "train-next");
      assert.equal(payload.currentRide.train.trainCard.train.id, "train-next");
      assert.equal(payload.currentRide.train.trainCard.riders, 3);
    });
  } finally {
    app.__test__.resetLiveClient();
    global.window.TrainAppLiveClient = previousLiveClient;
    global.fetch = previousFetch;
  }

  assert.equal(bootstrapCalls, 1);
  assert.deepEqual(fetchCalls, [
    publicTrainStopsURL("/pixel-stack/train", "train-next"),
  ]);
});

test("hydrateCurrentRideTrainFromPublic normalizes the public train payload for signed-in ride rendering", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        currentRide: {
          checkIn: {
            trainInstanceId: "train-next",
          },
          train: null,
        },
      });
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trainCard: {
                train: {
                  id: "train-next",
                  fromStation: "Riga",
                  toStation: "Jelgava",
                  departureAt: "2026-03-27T10:39:00Z",
                  arrivalAt: "2026-03-27T11:21:00Z",
                },
                status: {
                  state: "MOVING",
                },
                riders: 3,
              },
              timeline: [],
              stationSightings: [],
              stops: [],
            });
          },
        };
      };

      var hydrated = await app.__test__.hydrateCurrentRideTrainFromPublic("train-next");
      var state = app.__test__.getState();

      assert.equal(hydrated, true);
      assert.equal(state.currentRide.train.trainCard.train.id, "train-next");
      assert.equal(state.currentRide.train.trainCard.riders, 3);
      assert.equal(state.selectedTrain.trainCard.train.id, "train-next");
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    publicTrainStopsURL("/pixel-stack/train", "train-next"),
  ]);
});

test("hydrateCurrentRideTrainFromPublic refreshes existing zero-rider ride detail from public train stops", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        currentRide: {
          checkIn: {
            trainInstanceId: "train-next",
          },
          train: {
            trainCard: {
              train: {
                id: "train-next",
              },
              riders: 0,
            },
          },
        },
      });
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trainCard: {
                train: {
                  id: "train-next",
                  fromStation: "Riga",
                  toStation: "Jelgava",
                  departureAt: "2026-03-27T10:39:00Z",
                  arrivalAt: "2026-03-27T11:21:00Z",
                },
                status: {
                  state: "MOVING",
                },
                riders: 3,
              },
              timeline: [],
              stationSightings: [],
              stops: [],
            });
          },
        };
      };

      var hydrated = await app.__test__.hydrateCurrentRideTrainFromPublic("train-next");
      var state = app.__test__.getState();

      assert.equal(hydrated, true);
      assert.equal(state.currentRide.train.trainCard.train.id, "train-next");
      assert.equal(state.currentRide.train.trainCard.riders, 3);
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    publicTrainStopsURL("/pixel-stack/train", "train-next"),
  ]);
});

test("publicApi uses the server API for public reads", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        messages: {
          app_data_unavailable_body: "Direct data unavailable",
        },
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trains: [],
              schedule: { available: true },
            });
          },
        };
      };

      var payload = await app.__test__.publicApi("/public/dashboard?limit=0");
      assert.equal(payload.schedule.available, true);
    });
  } finally {
    app.__test__.resetLiveClient();
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/dashboard?limit=0",
  ]);
});

test("publicApi still uses the server API when no live transport is configured", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "",
      spacetimeDatabase: "",
    }, async function () {
      app.__test__.resetState({
        messages: {
          app_data_unavailable_body: "Direct data unavailable",
        },
      });
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trainCard: {
                train: {
                  id: "demo-train",
                },
              },
              timeline: [],
              stationSightings: [],
              stops: [],
            });
          },
        };
      };

      var payload = await app.__test__.publicApi("/public/trains/demo-train/stops");
      assert.equal(payload.trainCard.train.id, "demo-train");
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/trains/demo-train/stops",
  ]);
});

test("publicApi keeps using the server API when edge cache is enabled", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      publicEdgeCacheEnabled: true,
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trains: [],
              schedule: { available: true },
            });
          },
        };
      };

      await app.__test__.publicApi("/public/dashboard?limit=0");
    });
  } finally {
    global.fetch = previousFetch;
    app.__test__.resetLiveClient();
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/dashboard?limit=0",
  ]);
});

test("publicApi resolves bundle slices from a relative manifest URL", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      publicBaseURL: "https://train-bot.jolkins.id.lv",
      bundleManifestURL: "/assets/bundles/bundle-2026-03-27/manifest.json",
    }, async function () {
      app.__test__.resetState();
      global.fetch = async function (url) {
        fetchCalls.push(String(url));
        if (url === "/assets/bundles/bundle-2026-03-27/manifest.json") {
          return {
            ok: true,
            json: async function () {
              return {
                slices: {
                  stations: "stations.json",
                },
              };
            },
          };
        }
        if (url === "https://train-bot.jolkins.id.lv/assets/bundles/bundle-2026-03-27/stations.json") {
          return {
            ok: true,
            json: async function () {
              return [
                { id: "riga", name: "Riga", latitude: 56.9496, longitude: 24.1052 },
              ];
            },
          };
        }
        throw new Error("unexpected fetch: " + url);
      };

      var payload = await app.__test__.publicApi("/public/map");
      assert.equal(payload.stations.length, 1);
      assert.equal(payload.stations[0].id, "riga");
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/assets/bundles/bundle-2026-03-27/manifest.json",
    "https://train-bot.jolkins.id.lv/assets/bundles/bundle-2026-03-27/stations.json",
  ]);
});

test("resolvedScheduleMeta treats a same-day bundle as available even when runtime schedule is unavailable", async function () {
  await withAppConfig({
    bundleServiceDate: "2026-03-30",
    schedule: {
      requestedServiceDate: "2026-03-30",
      fallbackActive: false,
      cutoffHour: 3,
      available: false,
      sameDayFresh: false,
    },
  }, async function () {
    app.__test__.resetState({
      scheduleMeta: null,
    });

    var schedule = app.__test__.resolvedScheduleMeta();
    assert.equal(schedule.available, true);
    assert.equal(schedule.sameDayFresh, true);
    assert.equal(schedule.effectiveServiceDate, "2026-03-30");
    assert.equal(schedule.loadedServiceDate, "2026-03-30");
  });
});

test("publicApi returns the bundle-backed schedule when the same-day bundle is present", async function () {
  var previousFetch = global.fetch;

  try {
    await withAppConfig({
      publicBaseURL: "https://train-bot.jolkins.id.lv",
      bundleManifestURL: "/assets/bundles/bundle-2026-03-30/manifest.json",
      bundleServiceDate: "2026-03-30",
      schedule: {
        requestedServiceDate: "2026-03-30",
        fallbackActive: false,
        cutoffHour: 3,
        available: false,
        sameDayFresh: false,
      },
    }, async function () {
      app.__test__.resetState();
      global.fetch = async function (url) {
        if (url === "/assets/bundles/bundle-2026-03-30/manifest.json") {
          return {
            ok: true,
            json: async function () {
              return {
                serviceDate: "2026-03-30",
                slices: {
                  trains: "trains.json",
                },
              };
            },
          };
        }
        if (url === "https://train-bot.jolkins.id.lv/assets/bundles/bundle-2026-03-30/trains.json") {
          return {
            ok: true,
            json: async function () {
              return [
                {
                  id: "train-1",
                  serviceDate: "2026-03-30",
                  departureAt: "2026-03-30T10:00:00Z",
                },
              ];
            },
          };
        }
        throw new Error("unexpected fetch: " + url);
      };

      var payload = await app.__test__.publicApi("/public/dashboard?limit=0");
      assert.equal(payload.schedule.available, true);
      assert.equal(payload.schedule.sameDayFresh, true);
      assert.equal(payload.schedule.effectiveServiceDate, "2026-03-30");
    });
  } finally {
    global.fetch = previousFetch;
  }
});

test("publicApi keeps signed-in public reads on the server API even when edge cache is enabled", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      publicEdgeCacheEnabled: true,
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              trains: [],
              schedule: { available: true },
            });
          },
        };
      };

      await app.__test__.publicApi("/public/dashboard?limit=0");
    });
  } finally {
    global.fetch = previousFetch;
    app.__test__.resetLiveClient();
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/dashboard?limit=0",
  ]);
});

test("api uses the server API for signed-in reads", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              userId: 44,
              settings: {
                alertsEnabled: true,
                alertStyle: "DETAILED",
                language: "EN",
              },
            });
          },
        };
      };

      var payload = await app.__test__.api("/me");
      assert.equal(payload.userId, 44);
    });
  } finally {
    global.fetch = previousFetch;
    app.__test__.resetLiveClient();
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/me",
  ]);
});

test("api still uses the server API when no live transport is configured", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "",
      spacetimeDatabase: "",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        messages: {
          app_data_unavailable_body: "Direct data unavailable",
        },
      });
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              userId: 45,
              settings: {
                alertsEnabled: false,
                alertStyle: "DISCREET",
                language: "LV",
              },
            });
          },
        };
      };

      var payload = await app.__test__.api("/me");
      assert.equal(payload.userId, 45);
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/me",
  ]);
});

test("api keeps using the server API for signed-in reads when edge cache is enabled", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      publicEdgeCacheEnabled: true,
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({
              userId: 46,
              settings: {
                alertsEnabled: true,
                alertStyle: "DETAILED",
                language: "EN",
              },
            });
          },
        };
      };

      await app.__test__.api("/me");
    });
  } finally {
    global.fetch = previousFetch;
    app.__test__.resetLiveClient();
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/me",
  ]);
});

test("api still uses the phone endpoint for local-only routes", async function () {
  var previousFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        return {
          ok: true,
          status: 200,
          text: async function () {
            return JSON.stringify({ ok: true });
          },
        };
      };

      await app.__test__.api("/auth/telegram", {
        method: "POST",
        body: JSON.stringify({ initData: "demo" }),
      }, true);
    });
  } finally {
    global.fetch = previousFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/auth/telegram",
  ]);
});

test("startExternalFeedIfNeeded keeps the configured graph URL available", async function () {
  var previousExternalFeed = global.window.TrainExternalFeed;
  var createdOptions = null;

  try {
    await withAppConfig({
      mode: "public-network-map",
      externalTrainMapEnabled: true,
      externalTrainMapBaseURL: "https://trainmap.vivi.lv",
      externalTrainMapWsURL: "wss://trainmap.pv.lv/ws",
      externalTrainGraphURL: "/pixel-stack/train/assets/bundles/bundle-2026-03-27/train-graph.json",
    }, async function () {
      app.__test__.resetState();
      global.window.TrainExternalFeed = {
        createExternalTrainMapClient: function (options) {
          createdOptions = options;
          return {
            start: function () {
              return Promise.resolve();
            },
            stop: function () {},
            restart: function () {
              return Promise.resolve();
            },
          };
        },
      };

      app.__test__.startExternalFeedIfNeeded();
      await Promise.resolve();
    });
  } finally {
    global.window.TrainExternalFeed = previousExternalFeed;
  }

  assert.ok(createdOptions);
  assert.equal(createdOptions.baseURL, "https://trainmap.vivi.lv");
  assert.equal(createdOptions.graphURL, "/pixel-stack/train/assets/bundles/bundle-2026-03-27/train-graph.json");
});

test("restartExternalFeedIfNeeded reuses the browser feed client for explicit retries", async function () {
  var previousExternalFeed = global.window.TrainExternalFeed;
  var restartCalls = 0;

  try {
    await withAppConfig({
      mode: "public-network-map",
      externalTrainMapEnabled: true,
      externalTrainMapBaseURL: "https://trainmap.vivi.lv",
      externalTrainMapWsURL: "wss://trainmap.pv.lv/ws",
    }, async function () {
      app.__test__.resetState();
      global.window.TrainExternalFeed = {
        createExternalTrainMapClient: function () {
          return {
            start: function () {
              return Promise.resolve();
            },
            stop: function () {},
            restart: function () {
              restartCalls += 1;
              return Promise.resolve();
            },
          };
        },
      };

      app.__test__.startExternalFeedIfNeeded();
      await app.__test__.restartExternalFeedIfNeeded();
    });
  } finally {
    global.window.TrainExternalFeed = previousExternalFeed;
  }

  assert.equal(restartCalls, 1);
});

test("externalFeedStatusText distinguishes graph degradation from a live feed outage", async function () {
  await withAppConfig({
    externalTrainMapEnabled: true,
  }, async function () {
    app.__test__.resetState({
      externalFeed: {
        enabled: true,
        connectionState: "live",
        graphState: "unavailable",
        routes: [],
        liveTrains: [],
        activeStops: [],
        lastGraphAt: "",
        lastMessageAt: "",
        connectionError: "",
        graphError: "blocked by cors",
        error: "",
      },
    });
    assert.equal(
      app.__test__.externalFeedStatusText(),
      "Live locations connected."
    );
  });
});

test("fetchSpacetimePath rejects mapped reads when the live client cannot connect", async function () {
  var previousLiveClient = global.window.TrainAppLiveClient;
  var previousFetch = global.fetch;
  var connectCalls = 0;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      app.__test__.resetState({
        authenticated: true,
        messages: {
          app_data_unavailable_body: "Direct data unavailable",
        },
        spacetimeAuth: {
          enabled: true,
          host: "https://stdb.example",
          database: "train-db",
          token: "token",
          expiresAt: "2099-01-01T00:00:00.000Z",
          issuer: "",
          audience: "",
        },
      });
      app.__test__.resetLiveClient();
      global.fetch = async function (url) {
        fetchCalls.push(url);
        throw new Error("server fallback should not be used");
      };
      global.window.TrainAppLiveClient = {
        create: function () {
          return {
            connect: async function () {
              connectCalls += 1;
              return false;
            },
            onInvalidate: function () {
              return function () {};
            },
          };
        },
      };

      await assert.rejects(
        app.__test__.fetchSpacetimePath("/me", { method: "GET" }, false),
        /Direct data unavailable/
      );
    });
  } finally {
    app.__test__.resetLiveClient();
    global.window.TrainAppLiveClient = previousLiveClient;
    global.fetch = previousFetch;
  }

  assert.equal(connectCalls, 1);
  assert.deepEqual(fetchCalls, []);
});

test("renderPublicStatusBar shows a retry action for non-blocking strict-mode load errors", function () {
  app.__test__.resetState({
    strictModeLoadError: {
      blocking: false,
      message: "spacetime down",
    },
    messages: {
      app_retry_data_load: "Retry",
    },
  });

  var html = app.__test__.renderPublicStatusBar({ actions: [] });

  assert.match(html, /data-action="retry-current-view"/);
  assert.match(html, />Retry</);
});

test("renderDataUnavailableContent shows the blocking retry panel", function () {
  app.__test__.resetState({
    strictModeLoadError: {
      blocking: true,
      message: "spacetime down",
    },
    messages: {
      app_data_unavailable_title: "Unavailable",
      app_data_unavailable_body: "Try again shortly.",
      app_retry_data_load: "Retry",
    },
  });

  var html = app.__test__.renderDataUnavailableContent();

  assert.match(html, /Unavailable/);
  assert.match(html, /Try again shortly\./);
  assert.match(html, /spacetime down/);
  assert.match(html, /data-action="retry-current-view"/);
});

test("renderMyRideTab omits deprecated ride action buttons", function () {
  app.__test__.resetState({
    currentRide: {
      checkIn: {
        trainInstanceId: "train-next",
      },
      boardingStationId: "riga",
      train: {
        trainCard: {
          train: {
            id: "train-next",
            fromStation: "Riga",
            toStation: "Jelgava",
            departureAt: "2099-03-10T19:02:00Z",
            arrivalAt: "2099-03-10T19:45:00Z",
          },
          riders: 3,
          status: {
            state: "NO_REPORTS",
            lastReportAt: "",
          },
        },
        timeline: [],
        stationSightings: [],
      },
    },
  });

  var html = app.__test__.renderMyRideTab();

  assert.doesNotMatch(html, /data-action="open-map"/);
  assert.doesNotMatch(html, /data-action="mute-train"/);
  assert.doesNotMatch(html, /data-action="checkout"/);
  assert.doesNotMatch(html, /data-action="undo-checkout"/);
  assert.doesNotMatch(html, /data-action="tab-report"/);
});

test("renderMiniSidebar hides duplicate active ride actions on my ride tab", function () {
  var rideTrain = {
    trainCard: {
      train: {
        id: "train-next",
        fromStation: "Riga",
        toStation: "Jelgava",
        departureAt: "2099-03-10T19:02:00Z",
        arrivalAt: "2099-03-10T19:45:00Z",
      },
      riders: 3,
      status: {
        state: "NO_REPORTS",
        lastReportAt: "",
      },
    },
    timeline: [],
    stationSightings: [],
  };

  app.__test__.resetState({
    tab: "my-ride",
    currentRide: {
      checkIn: {
        trainInstanceId: "train-next",
      },
      boardingStationId: "riga",
      train: rideTrain,
    },
    selectedTrain: rideTrain,
  });

  var html = app.__test__.renderMiniSidebar();

  assert.doesNotMatch(html, /data-action="open-map"/);
  assert.doesNotMatch(html, /data-action="mute-train"/);
  assert.doesNotMatch(html, /data-action="checkout"/);
});

test("buildStationPopupHTML stays informational in the simplified mini app", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    currentRide: null,
    messages: {
      btn_checkin_confirm: "Check in",
      app_report_sighting: "Report sighting",
    },
  });

  var html = app.__test__.buildStationPopupHTML({
    id: "riga",
    name: "Riga",
  }, {
    stationId: "riga",
    name: "Riga",
    sightings: [],
    liveItems: [{
      trainId: "train-next",
      localMatch: null,
      external: {
        trainNumber: "6321",
      },
      status: null,
    }],
  });

  assert.doesNotMatch(html, /popup-(checkin-train|open-station-sightings)/);
});

test("buildTrainStopPopupHTML stays informational when there is no live marker", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainStopPopupHTML({
    stationId: "riga",
    stationName: "Riga",
    departureAt: "2099-03-10T19:02:00Z",
  }, 0, {
    train: {
      id: "train-map",
      arrivalAt: "2099-03-10T19:15:00Z",
    },
    stationSightings: [],
  }, [], false);

  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainStopPopupHTML stays informational when a live marker is available", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainStopPopupHTML({
    stationId: "riga",
    stationName: "Riga",
    departureAt: "2099-03-10T19:02:00Z",
  }, 0, {
    train: {
      id: "train-map",
      arrivalAt: "2099-03-10T19:15:00Z",
    },
    stationSightings: [],
  }, [], true);

  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("buildTrainStopPopupHTML stays informational for expired stops on the map", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TRAIN_APP_CONFIG.stationCheckinEnabled = true;
  app.__test__.resetState({
    authenticated: true,
    messages: {
      btn_checkin_confirm: "Check in",
    },
  });

  var html = app.__test__.buildTrainStopPopupHTML({
    stationId: "riga",
    stationName: "Riga",
    departureAt: "2000-03-10T19:02:00Z",
  }, 0, {
    train: {
      id: "train-map",
      arrivalAt: "2000-03-10T19:15:00Z",
    },
    stationSightings: [],
  }, [], false);

  assert.doesNotMatch(html, /popup-checkin-train/);
});

test("alignMiniMapToSelectedTrain pins the map target and resumes follow", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    mapFollowTrainId: "train-old",
    mapFollowPaused: true,
  });

  app.__test__.alignMiniMapToSelectedTrain("train-new");

  var state = app.__test__.getState();
  assert.equal(state.mapTrainId, "train-new");
  assert.equal(state.mapPinnedTrainId, "train-new");
  assert.equal(state.mapFollowTrainId, "train-new");
  assert.equal(state.mapFollowPaused, false);
});

test("pauseMiniMapFollow only pauses while actively tracking a train map", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "train-map",
    mapFollowTrainId: "train-map",
  });

  app.__test__.pauseMiniMapFollow("test-manual-move");

  var pausedState = app.__test__.getState();
  assert.equal(pausedState.mapFollowPaused, true);

  app.__test__.resetState({
    tab: "dashboard",
    mapTrainId: "train-map",
    mapFollowTrainId: "train-map",
  });

  app.__test__.pauseMiniMapFollow("test-non-map-view");

  var idleState = app.__test__.getState();
  assert.equal(idleState.mapFollowPaused, false);
});

test("map tracking survives detail changes and app refreshes until leaving the map view", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    tab: "map",
    selectedTrain: {
      trainCard: {
        train: {
          id: "train-detail",
        },
      },
    },
  });

  app.__test__.setPinnedDetailTrain("train-detail", { fromUser: true, reason: "test" });
  app.__test__.alignMiniMapToSelectedTrain("train-map");
  app.__test__.clearPinnedDetailTrain("test-close-detail");
  app.__test__.applyCurrentRidePayload({
    currentRide: {
      checkIn: {
        trainInstanceId: "train-ride",
      },
      train: {
        trainCard: {
          train: {
            id: "train-ride",
          },
        },
      },
    },
  });

  var activeState = app.__test__.getState();
  assert.equal(app.__test__.preferredMapTrainId(), "train-map");
  assert.equal(app.__test__.resolveMiniMapFollowTarget(), "train-map");
  assert.equal(activeState.mapTrainId, "train-map");
  assert.equal(activeState.mapPinnedTrainId, "train-map");
  assert.equal(activeState.mapFollowTrainId, "train-map");

  app.__test__.setMiniAppTab("dashboard", "test-leave-map");

  var inactiveState = app.__test__.getState();
  assert.equal(inactiveState.tab, "feed");
  assert.equal(inactiveState.mapPinnedTrainId, "");
  assert.equal(inactiveState.mapFollowTrainId, "");
  assert.equal(inactiveState.mapFollowPaused, false);
});

test("no active ride plus selected detail keeps the map in network mode", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    selectedTrain: {
      trainCard: {
        train: {
          id: "train-detail",
        },
      },
    },
    mapTrainId: "train-detail",
  });
  app.__test__.setPinnedDetailTrain("train-detail", { fromUser: true, reason: "test" });

  app.__test__.applyCurrentRidePayload({ currentRide: null });

  var state = app.__test__.getState();
  assert.equal(app.__test__.preferredMapTrainId(), "");
  assert.equal(state.mapTrainId, "");
});

test("explicit map pin stays preferred without an active ride", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    selectedTrain: {
      trainCard: {
        train: {
          id: "train-detail",
        },
      },
    },
  });

  app.__test__.alignMiniMapToSelectedTrain("train-map");
  app.__test__.applyCurrentRidePayload({ currentRide: null });

  var state = app.__test__.getState();
  assert.equal(app.__test__.preferredMapTrainId(), "train-map");
  assert.equal(state.mapTrainId, "train-map");
  assert.equal(state.mapPinnedTrainId, "train-map");
});

test("showAllTrainsMap clears a non-checked-in pinned train map back to network mode", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "train-map",
    mapPinnedTrainId: "train-map",
    mapFollowTrainId: "train-map",
    selectedTrain: {
      trainCard: {
        train: {
          id: "train-detail",
        },
      },
    },
  });

  app.__test__.showAllTrainsMap();

  var state = app.__test__.getState();
  assert.equal(app.__test__.preferredMapTrainId(), "");
  assert.equal(state.mapTrainId, "");
  assert.equal(state.mapPinnedTrainId, "");
  assert.equal(state.mapFollowTrainId, "");
});

test("network map config renders only live train markers in live-only mode", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    publicDashboardAll: [
      {
        train: {
          id: "train-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "2026-03-10T09:10:00Z",
        },
        stationSightings: [],
      },
    ],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          routeId: "route-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "2026-03-10T09:59:00Z",
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var config = app.__test__.buildNetworkMapConfig({
      liveOnly: true,
      stations: [
        {
          id: "riga",
          name: "Riga",
          normalizedKey: "riga",
          latitude: 56.95,
          longitude: 24.1,
        },
      ],
      recentSightings: [
        {
          stationId: "riga",
          stationName: "Riga",
          createdAt: "2026-03-10T18:55:15Z",
        },
      ],
    }, { zoom: 14, visibleHeightMeters: 525 });

    assert.equal(config.baseMarkers.length, 0);
    assert.equal(config.sightingMarkers.length, 0);
    assert.deepEqual(config.polyline, []);
    assert.equal(config.trainMarkers.length, 1);
    assert.equal(config.trainMarkers[0].markerKey, "live-train:route-fresh");
    assert.equal(config.trainMarkers[0].animateMovement, false);
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("train map config renders only the selected live train marker in live-only mode", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    mapTrainDetail: null,
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          routeId: "route-7104",
          serviceDate: "2026-03-10",
          trainNumber: "7104",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "2026-03-10T09:59:00Z",
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var config = app.__test__.buildTrainMapConfig({
      train: {
        id: "2026-03-10-train-7104",
        serviceDate: "2026-03-10",
        trainNumber: "7104",
        fromStation: "Riga",
        toStation: "Jelgava",
        departureAt: "2026-03-10T09:10:00Z",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.95,
          longitude: 24.1,
        },
      ],
      stationSightings: [
        {
          stationId: "riga",
          stationName: "Riga",
          createdAt: "2026-03-10T18:55:15Z",
        },
      ],
    }, { zoom: 14, visibleHeightMeters: 525 });

    assert.equal(config.baseMarkers.length, 0);
    assert.equal(config.sightingMarkers.length, 0);
    assert.deepEqual(config.polyline, []);
    assert.equal(config.trainMarkers.length, 1);
    assert.equal(config.trainMarkers[0].markerKey, "live-train:route-7104");
    assert.equal(config.trainMarkers[0].animateMovement, false);
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("live-only network map config still changes across zoom tiers without changing marker identity", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    publicDashboardAll: [
      {
        train: {
          id: "train-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "2026-03-10T09:10:00Z",
        },
        stationSightings: [],
      },
    ],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          routeId: "route-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "2026-03-10T09:59:00Z",
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var wideConfig = app.__test__.buildNetworkMapConfig({ liveOnly: true }, { zoom: 13, visibleHeightMeters: 1000 });
    var detailConfig = app.__test__.buildNetworkMapConfig({ liveOnly: true }, { zoom: 15, visibleHeightMeters: 50 });

    assert.notEqual(wideConfig.modelKey, detailConfig.modelKey);
    assert.equal(wideConfig.trainMarkers[0].markerKey, detailConfig.trainMarkers[0].markerKey);
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("buildMatchedLiveItems prepares local train matching once per live render", function () {
  var originalFeedApi = global.window.TrainExternalFeed;
  var prepareCalls = 0;
  var matchCalls = 0;
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      prepareCalls += 1;
      return function (externalTrain) {
        matchCalls += 1;
        if (externalTrain && externalTrain.trainNumber === "6321") {
          return {
            match: localItems[0],
            localTrainId: "2026-03-10-train-6321",
            matchType: "exact-id",
          };
        }
        return null;
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    publicDashboardAll: [
      {
        train: {
          id: "2026-03-10-train-6321",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "09:10"
        },
        stationSightings: [],
      },
    ],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [
        {
          routeId: "route-6321",
          trainNumber: "6321",
          serviceDate: "2026-03-10",
          origin: "Riga",
          destination: "Jelgava",
        },
      ],
      liveTrains: [
        {
          routeId: "route-6321",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "2026-03-10T18:55:15Z",
        },
        {
          routeId: "route-6322",
          serviceDate: "2026-03-10",
          trainNumber: "6322",
          position: { lat: 56.96, lng: 24.2 },
          updatedAt: "2026-03-10T18:56:15Z",
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var liveItems = app.__test__.buildMatchedLiveItems(
      app.__test__.getState().publicDashboardAll,
      [
        {
          matchedTrainInstanceId: "2026-03-10-train-6321",
          stationName: "Riga",
        },
      ]
    );

    assert.equal(prepareCalls, 1);
    assert.equal(matchCalls, 2);
    assert.equal(liveItems.length, 2);
    assert.equal(liveItems[0].external.destination, "Jelgava");
    assert.equal(liveItems[0].sightings.length, 1);
    assert.equal(liveItems[1].sightings.length, 0);
  } finally {
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("network map shows recent live and projection-backed trains", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    publicDashboardAll: [
      {
        train: {
          id: "train-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "2026-03-10T09:10:00Z",
        },
        stationSightings: [],
      },
      {
        train: {
          id: "train-warm",
          serviceDate: "2026-03-10",
          trainNumber: "6322",
          fromStation: "Riga",
          toStation: "Tukums",
          departureAt: "2026-03-10T09:20:00Z",
        },
        stationSightings: [],
      },
      {
        train: {
          id: "train-stale",
          serviceDate: "2026-03-10",
          trainNumber: "6323",
          fromStation: "Riga",
          toStation: "Ogre",
          departureAt: "2026-03-10T09:30:00Z",
        },
        stationSightings: [],
      },
      {
        train: {
          id: "train-scheduled",
          serviceDate: "2026-03-10",
          trainNumber: "6324",
          fromStation: "Riga",
          toStation: "Aizkraukle",
          departureAt: "2026-03-10T09:40:00Z",
        },
        stationSightings: [],
      },
      {
        train: {
          id: "train-projection",
          serviceDate: "2026-03-10",
          trainNumber: "6325",
          fromStation: "Riga",
          toStation: "Majori",
          departureAt: "2026-03-10T09:45:00Z",
        },
        stationSightings: [],
      },
    ],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          routeId: "route-fresh",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "2026-03-10T09:59:00Z",
          isGpsActive: true,
        },
        {
          routeId: "route-warm",
          serviceDate: "2026-03-10",
          trainNumber: "6322",
          position: { lat: 56.96, lng: 24.2 },
          updatedAt: "2026-03-10T09:56:00Z",
          isGpsActive: true,
        },
        {
          routeId: "route-live-memory",
          serviceDate: "2026-03-10",
          trainNumber: "6326",
          position: { lat: 56.965, lng: 24.25 },
          updatedAt: "2026-03-10T09:57:30Z",
          displaySource: "live",
          displayUpdatedAt: "2026-03-10T09:57:30Z",
          isGpsActive: false,
        },
        {
          routeId: "route-stale",
          serviceDate: "2026-03-10",
          trainNumber: "6323",
          position: { lat: 56.97, lng: 24.3 },
          updatedAt: "2026-03-10T09:45:00Z",
          isGpsActive: true,
        },
        {
          routeId: "route-scheduled",
          serviceDate: "2026-03-10",
          trainNumber: "6324",
          position: { lat: 56.98, lng: 24.4 },
          updatedAt: "",
          isGpsActive: false,
        },
        {
          routeId: "route-projection",
          serviceDate: "2026-03-10",
          trainNumber: "6325",
          position: { lat: 56.985, lng: 24.45 },
          updatedAt: "",
          displaySource: "projection",
          displayUpdatedAt: "2026-03-10T09:58:00Z",
          isGpsActive: false,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var config = app.__test__.buildNetworkMapConfig({
      stations: [],
      recentSightings: [],
    }, { zoom: 14, visibleHeightMeters: 400 });

    assert.deepEqual(config.trainMarkers.map(function (marker) {
      return marker.markerKey;
    }), [
      "live-train:route-fresh",
      "live-train:route-warm",
      "live-train:route-live-memory",
      "live-train:route-projection",
    ]);
    assert.match(config.trainMarkers[2].html, /gps-warm/);
    assert.match(config.trainMarkers[3].html, /gps-projection/);
    assert.equal(config.trainMarkers[3].movementObservedAt, "2026-03-10T09:58:00Z");
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("network map live train popups keep check-in when the train only exists in the service-day list", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalFeedApi = global.window.TrainExternalFeed;
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        if (externalTrain && externalTrain.trainNumber === "6321") {
          return {
            match: localItems[0],
            localTrainId: "2026-03-10-train-6321",
            matchType: "exact-id",
          };
        }
        return null;
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    authenticated: true,
    publicDashboardAll: [],
    publicServiceDayTrains: [
      {
        train: {
          id: "2026-03-10-train-6321",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "2026-03-10T09:10:00Z",
          arrivalAt: "2026-03-10T10:05:00Z",
        },
        status: {
          state: "NO_REPORTS",
          confidence: "LOW",
          uniqueReporters: 0,
        },
        timeline: [],
        stationSightings: [],
      },
    ],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [
        {
          routeId: "route-6321",
          trainNumber: "6321",
          serviceDate: "2026-03-10",
          origin: "Riga",
          destination: "Jelgava",
        },
      ],
      liveTrains: [
        {
          routeId: "route-6321",
          serviceDate: "2026-03-10",
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: new Date(Date.now() - (60 * 1000)).toISOString(),
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var config = app.__test__.buildNetworkMapConfig({
      stations: [],
      recentSightings: [],
    }, { zoom: 14, visibleHeightMeters: 400 });

    assert.equal(config.trainMarkers.length, 1);
    assert.doesNotMatch(config.trainMarkers[0].popupHTML, /popup-checkin-train/);
  } finally {
    global.window.TrainExternalFeed = originalFeedApi;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }
});

test("selected train map keeps projection-majority trains visible with projection styling", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };

  app.__test__.resetState({
    mapTrainDetail: null,
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          routeId: "route-scheduled",
          serviceDate: "2026-03-10",
          trainNumber: "7104",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: "",
          displaySource: "projection",
          displayUpdatedAt: "2026-03-10T09:58:00Z",
          isGpsActive: false,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  try {
    var config = app.__test__.buildTrainMapConfig({
      train: {
        id: "2026-03-10-train-7104",
        serviceDate: "2026-03-10",
        trainNumber: "7104",
        fromStation: "Riga",
        toStation: "Jelgava",
        departureAt: "2026-03-10T09:10:00Z",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.95,
          longitude: 24.1,
          departureAt: "2026-03-10T09:10:00Z",
        },
      ],
      stationSightings: [],
    }, { zoom: 14, visibleHeightMeters: 400 });

    assert.equal(config.baseMarkers.length, 0);
    assert.equal(config.trainMarkers.length, 1);
    assert.equal(config.trainMarkers[0].markerKey, "live-train:route-scheduled");
    assert.match(config.trainMarkers[0].html, /gps-projection/);
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("refreshViewportLayers reconciles markers after a zoom-only change", function () {
  var originalNow = Date.now;
  var originalFeedApi = global.window.TrainExternalFeed;
  Date.now = function () {
    return Date.parse("2026-03-10T10:00:00Z");
  };
  global.window.TrainExternalFeed = {
    createLocalTrainMatcher: function (localItems) {
      return function (externalTrain) {
        var match = localItems.find(function (item) {
          var train = item && item.train ? item.train : item;
          return train && train.trainNumber === externalTrain.trainNumber;
        }) || null;
        if (!match) {
          return null;
        }
        var train = match && match.train ? match.train : match;
        return {
          match: match,
          localTrainId: train && train.id ? train.id : "",
          matchType: "exact-id",
        };
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function (value) {
      return String((value && value.routeId) || (value && value.trainNumber) || "");
    },
  };
  try {
    app.__test__.resetState({
      publicDashboardAll: [
        {
          train: {
            id: "train-fresh",
            serviceDate: "2026-03-10",
            trainNumber: "6321",
            fromStation: "Riga",
            toStation: "Jelgava",
            departureAt: "2026-03-10T09:10:00Z",
          },
          stationSightings: [],
        },
      ],
      externalFeed: {
        enabled: true,
        connectionState: "live",
        routes: [],
        liveTrains: [
          {
            routeId: "route-fresh",
            serviceDate: "2026-03-10",
            trainNumber: "6321",
            position: { lat: 56.95, lng: 24.1 },
            updatedAt: "2026-03-10T09:59:00Z",
            isGpsActive: true,
          },
        ],
        activeStops: [],
        lastGraphAt: "",
        lastMessageAt: "",
        error: "",
      },
    });
    var zoom = 13;
    var bounds = {
      getNorth: function () {
        return 56.97;
      },
      getSouth: function () {
        return 56.96;
      },
      getCenter: function () {
        return { lng: 24.1 };
      },
    };
    var controller = app.__test__.createMapController();
    controller.map = {
      _loaded: true,
      getZoom: function () {
        return zoom;
      },
      getBounds: function () {
        return bounds;
      },
    };
    controller.mapModel = {
      liveOnly: true,
    };
    controller.modelKey = app.__test__.buildNetworkMapConfig(controller.mapModel, {
      zoom: 13,
      visibleHeightMeters: app.__test__.boundsHeightMeters(bounds),
    }).modelKey;
    var updateCalls = 0;
    var restoreCalls = 0;
    controller.updateLayers = function (config) {
      updateCalls += 1;
      this.modelKey = config.modelKey;
    };
    controller.restorePendingPopup = function () {
      restoreCalls += 1;
    };

    zoom = 15;
    controller.refreshViewportLayers();

    assert.equal(updateCalls, 1);
    assert.equal(restoreCalls, 1);
  } finally {
    Date.now = originalNow;
    global.window.TrainExternalFeed = originalFeedApi;
  }
});

test("map controller rebuilds only when the map shell changes", function () {
  var originalGetElementById = global.document.getElementById;
  var originalLeaflet = global.window.L;
  var containers = {};
  var host = { parentNode: null };

  function makeContainer(id) {
    return {
      id: id,
      appendChild: function (el) {
        this.child = el;
        el.parentNode = this;
      },
      removeChild: function (el) {
        if (this.child === el) {
          this.child = null;
        }
        if (el.parentNode === this) {
          el.parentNode = null;
        }
      },
    };
  }

  containers["mini-network-map"] = makeContainer("mini-network-map");
  containers["mini-train-map"] = makeContainer("mini-train-map");
  global.document.getElementById = function (id) {
    return containers[id] || null;
  };
  global.window.L = {};

  var controller = app.__test__.createMapController();
  var mapBuilds = 0;
  var resetCalls = 0;
  var updateCalls = 0;
  var scheduleCalls = 0;
  var originalReset = controller.reset;

  controller.ensureHost = function () {
    this.hostEl = host;
    return host;
  };
  controller.ensureMap = function () {
    if (this.map) {
      return this.map;
    }
    mapBuilds += 1;
    this.map = {
      closePopup: function () {},
      remove: function () {},
    };
    return this.map;
  };
  controller.buildConfig = function (mapModel) {
    var viewKey = mapModel.train ? "train:train-1" : "network:mini-app";
    return {
      bounds: [[56.95, 24.1]],
      modelKey: mapModel.key,
      viewKey: viewKey,
    };
  };
  controller.updateLayers = function () {
    updateCalls += 1;
  };
  controller.scheduleLayout = function () {
    scheduleCalls += 1;
  };
  controller.reset = function () {
    resetCalls += 1;
    return originalReset.call(this);
  };

  try {
    controller.sync("mini-network-map", { key: "network" });
    controller.detach();
    controller.sync("mini-train-map", { key: "train", train: { id: "train-1" } });
    controller.detach();
    controller.sync("mini-train-map", { key: "train", train: { id: "train-1" } });
  } finally {
    global.document.getElementById = originalGetElementById;
    global.window.L = originalLeaflet;
  }

  assert.equal(mapBuilds, 2);
  assert.equal(resetCalls, 1);
  assert.equal(updateCalls, 2);
  assert.equal(scheduleCalls, 3);
});

test("map controller pans focused markers to the map center", function () {
  var controller = app.__test__.createMapController();
  var calls = [];

  controller.map = {
    getSize: function () {
      return { x: 400, y: 200 };
    },
    getZoom: function () {
      return 15;
    },
    latLngToContainerPoint: function () {
      return { x: 100, y: 50 };
    },
    containerPointToLatLng: function (point) {
      return [point.y, point.x];
    },
    panTo: function (latLng, options) {
      calls.push({ latLng: latLng, options: options });
    },
  };
  controller.markerIndex.set("live-train:demo", {
    getLatLng: function () {
      return { lat: 56.95, lng: 24.1 };
    },
  });
  controller.markerState.set("live-train:demo", {
    targetLatLng: [56.95, 24.1],
  });

  assert.equal(controller.panToMarker("live-train:demo"), true);
  assert.deepEqual(calls, [
    {
      latLng: [50, 100],
      options: { animate: false },
    },
  ]);
});

test("map controller opens floating detail on the first click and transfers it to the next marker", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var stationKey = "network-station:riga";
  var nextKey = "network-station:jelgava";

  app.__test__.resetState();
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(stationKey, {
    item: {
      markerKey: stationKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: stationKey,
        detailHTML: "<div>Riga detail</div>",
        selectionOptions: {},
      },
    },
  });
  controller.markerState.set(nextKey, {
    item: {
      markerKey: nextKey,
      latLng: [56.64, 23.72],
      interaction: {
        entityKey: nextKey,
        detailHTML: "<div>Jelgava detail</div>",
        selectionOptions: {},
      },
    },
  });

  controller.handleMarkerInteraction(stationKey);
  var firstState = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, stationKey);
  assert.equal(controller.openPopupKey, stationKey);
  assert.equal(firstState.publicMapPopupKey, stationKey);
  assert.equal(detailLayer.hidden, false);
  assert.match(detailLayer.innerHTML, /train-map-detail-card/);
  assert.match(detailLayer.innerHTML, /Riga detail/);

  controller.handleMarkerInteraction(nextKey);
  var nextState = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, nextKey);
  assert.equal(controller.openPopupKey, nextKey);
  assert.equal(nextState.publicMapPopupKey, nextKey);
  assert.equal(detailLayer.hidden, false);
  assert.match(detailLayer.innerHTML, /Jelgava detail/);
});

test("compact map viewports keep the same first-click detail behavior", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var stationKey = "network-station:riga";
  var previousWidth = global.window.innerWidth;

  global.window.innerWidth = 480;
  app.__test__.resetState();
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(stationKey, {
    item: {
      markerKey: stationKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: stationKey,
        detailHTML: "<div>Riga detail</div>",
        selectionOptions: {},
      },
    },
  });

  try {
    controller.handleMarkerInteraction(stationKey);
  } finally {
    global.window.innerWidth = previousWidth;
  }

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, stationKey);
  assert.equal(controller.openPopupKey, stationKey);
  assert.equal(state.publicMapPopupKey, stationKey);
  assert.equal(detailLayer.hidden, false);
  assert.match(detailLayer.innerHTML, /Riga detail/);
});

test("map controller proxies missed touch taps inside the map to the nearest marker", function () {
  var controller = app.__test__.createMapController();
  var openedMarkerKey = null;

  controller.containerEl = {
    getBoundingClientRect: function () {
      return {
        left: 30,
        top: 200,
        width: 320,
        height: 320,
        right: 350,
        bottom: 520,
      };
    },
    querySelectorAll: function () {
      return [];
    },
  };
  controller.hostEl = {
    getBoundingClientRect: function () {
      return {
        left: 30,
        top: 200,
        width: 320,
        height: 320,
        right: 350,
        bottom: 520,
      };
    },
  };
  controller.map = {
    latLngToContainerPoint: function () {
      return { x: 120, y: 140 };
    },
  };
  controller.markerState.set("network-station:riga", {
    marker: {},
    targetLatLng: [56.95, 24.1],
    item: {
      markerKey: "network-station:riga",
      interaction: {
        entityKey: "network-station:riga",
      },
    },
  });
  controller.handleMarkerInteraction = function (markerKey) {
    openedMarkerKey = markerKey;
    return true;
  };

  controller.recordDocumentTapStart({
    type: "pointerdown",
    pointerType: "touch",
    clientX: 150,
    clientY: 340,
  });
  var handled = controller.handleDocumentTapEnd({
    type: "pointerup",
    pointerType: "touch",
    clientX: 150,
    clientY: 340,
    target: { tagName: "HTML" },
  });

  assert.equal(handled, true);
  assert.equal(openedMarkerKey, "network-station:riga");
});

test("map controller proxies missed touch taps inside popup chrome to the matching button", function () {
  var controller = app.__test__.createMapController();
  var clickCount = 0;
  var actionButton = {
    click: function () {
      clickCount += 1;
    },
    getBoundingClientRect: function () {
      return {
        left: 120,
        top: 280,
        width: 100,
        height: 32,
        right: 220,
        bottom: 312,
      };
    },
  };

  controller.containerEl = {
    getBoundingClientRect: function () {
      return {
        left: 30,
        top: 200,
        width: 320,
        height: 320,
        right: 350,
        bottom: 520,
      };
    },
    querySelectorAll: function () {
      return [actionButton];
    },
  };
  controller.hostEl = {
    getBoundingClientRect: function () {
      return {
        left: 30,
        top: 200,
        width: 320,
        height: 320,
        right: 350,
        bottom: 520,
      };
    },
  };

  controller.recordDocumentTapStart({
    type: "pointerdown",
    pointerType: "touch",
    clientX: 170,
    clientY: 296,
  });
  var handled = controller.handleDocumentTapEnd({
    type: "pointerup",
    pointerType: "touch",
    clientX: 170,
    clientY: 296,
    target: { tagName: "HTML" },
  });

  assert.equal(handled, true);
  assert.equal(clickCount, 1);
});

test("live train map clicks open detail and start follow on the first click", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var markerKey = "live-train:train-7";

  app.__test__.resetState({
    publicMapFollowPaused: true,
    mapFollowPaused: true,
  });
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: markerKey,
        detailHTML: "<div>Train 7 detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-7",
        },
      },
    },
  });

  controller.handleMarkerInteraction(markerKey);
  var focusedState = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, markerKey);
  assert.equal(controller.openPopupKey, markerKey);
  assert.equal(focusedState.publicMapSelectedMarkerKey, markerKey);
  assert.equal(focusedState.publicMapPopupKey, markerKey);
  assert.equal(focusedState.mapFollowTrainId, "train-7");
  assert.equal(detailLayer.hidden, false);
  assert.match(detailLayer.innerHTML, /Train 7 detail/);
});

test("map detail dismissal stays suppressed right after opening", function () {
  var originalDateNow = Date.now;
  var now = 1000;
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-8";

  Date.now = function () {
    return now;
  };

  app.__test__.resetState();
  controller.detailLayerEl = { innerHTML: "", hidden: true };
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: markerKey,
        detailHTML: "<div>Train 8 detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-8",
        },
      },
    },
  });

  try {
    controller.handleMarkerInteraction(markerKey);
    assert.equal(controller.handleDocumentClick({
      target: {
        closest: function () {
          return null;
        },
      },
    }), false);
    assert.equal(controller.openPopupKey, markerKey);

    now += 500;
    assert.equal(controller.handleDocumentClick({
      target: {
        closest: function () {
          return null;
        },
      },
    }), true);
    assert.equal(controller.openPopupKey, "");
  } finally {
    Date.now = originalDateNow;
  }
});

test("closing live train detail keeps follow active", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var markerKey = "live-train:train-9";

  app.__test__.resetState({
    publicMapFollowPaused: true,
    mapFollowPaused: true,
  });
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: markerKey,
        detailHTML: "<div>Train 9 detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-9",
        },
      },
    },
  });

  controller.handleMarkerInteraction(markerKey);
  controller.closePopup();

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, markerKey);
  assert.equal(controller.openPopupKey, "");
  assert.equal(state.publicMapPopupKey, "");
  assert.equal(state.publicMapSelectedMarkerKey, markerKey);
  assert.equal(state.mapFollowTrainId, "train-9");
  assert.equal(state.mapFollowPaused, false);
  assert.equal(detailLayer.hidden, true);
});

test("outside click closes live train detail without clearing follow", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var markerKey = "live-train:train-10";

  app.__test__.resetState();
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: markerKey,
        detailHTML: "<div>Train 10 detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-10",
        },
      },
    },
  });

  controller.handleMarkerInteraction(markerKey);
  controller.mapDetailDismissSuppressedUntil = 0;
  assert.equal(controller.handleDocumentClick({
    target: {
      closest: function () {
        return null;
      },
    },
  }), true);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, markerKey);
  assert.equal(controller.openPopupKey, "");
  assert.equal(state.publicMapPopupKey, "");
  assert.equal(state.publicMapSelectedMarkerKey, markerKey);
  assert.equal(state.mapFollowTrainId, "train-10");
});

test("switching to another live train transfers focus, detail, and follow", function () {
  var controller = app.__test__.createMapController();
  var detailLayer = { innerHTML: "", hidden: true };
  var firstKey = "live-train:train-10a";
  var secondKey = "live-train:train-10b";

  app.__test__.resetState();
  controller.detailLayerEl = detailLayer;
  controller.markerState.set(firstKey, {
    item: {
      markerKey: firstKey,
      latLng: [56.95, 24.1],
      interaction: {
        entityKey: firstKey,
        detailHTML: "<div>Train 10A detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-10a",
        },
      },
    },
  });
  controller.markerState.set(secondKey, {
    item: {
      markerKey: secondKey,
      latLng: [56.96, 24.11],
      interaction: {
        entityKey: secondKey,
        detailHTML: "<div>Train 10B detail</div>",
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-10b",
        },
      },
    },
  });

  controller.handleMarkerInteraction(firstKey);
  controller.handleMarkerInteraction(secondKey);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, secondKey);
  assert.equal(controller.openPopupKey, secondKey);
  assert.equal(state.publicMapPopupKey, secondKey);
  assert.equal(state.publicMapSelectedMarkerKey, secondKey);
  assert.equal(state.mapFollowTrainId, "train-10b");
  assert.match(detailLayer.innerHTML, /Train 10B detail/);
});

test("small map movement keeps live train focus detail and follow", function () {
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-11";

  app.__test__.resetState();
  controller.map = {
    getCenter: function () {
      return { lat: 56.95, lng: 24.1 };
    },
    getZoom: function () {
      return 15;
    },
    getSize: function () {
      return { x: 400, y: 200 };
    },
    latLngToContainerPoint: function () {
      return { x: 204, y: 102 };
    },
  };
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-11",
        },
      },
    },
  });
  controller.focusedEntityKey = markerKey;
  controller.openPopupKey = markerKey;
  app.__test__.setPublicMapPopupSelection(markerKey, {
    movingMarkerTracking: true,
    movingTrainId: "train-11",
  });

  assert.equal(controller.beginUserMapGesture("move"), true);
  assert.equal(controller.finishUserMapGesture("user-moved-map"), false);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, markerKey);
  assert.equal(controller.openPopupKey, markerKey);
  assert.equal(state.publicMapPopupKey, markerKey);
  assert.equal(state.publicMapSelectedMarkerKey, markerKey);
  assert.equal(state.mapFollowTrainId, "train-11");
});

test("larger map movement clears live train focus detail and follow", function () {
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-12";

  app.__test__.resetState();
  controller.map = {
    getCenter: function () {
      return { lat: 56.95, lng: 24.1 };
    },
    getZoom: function () {
      return 15;
    },
    getSize: function () {
      return { x: 400, y: 200 };
    },
    latLngToContainerPoint: function () {
      return { x: 214, y: 100 };
    },
  };
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-12",
        },
      },
    },
  });
  controller.focusedEntityKey = markerKey;
  controller.openPopupKey = markerKey;
  app.__test__.setPublicMapPopupSelection(markerKey, {
    movingMarkerTracking: true,
    movingTrainId: "train-12",
  });

  assert.equal(controller.beginUserMapGesture("move"), true);
  assert.equal(controller.finishUserMapGesture("user-moved-map"), true);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, "");
  assert.equal(controller.openPopupKey, "");
  assert.equal(state.publicMapPopupKey, "");
  assert.equal(state.publicMapSelectedMarkerKey, "");
  assert.equal(state.mapFollowTrainId, "");
});

test("zooming clears live train focus, detail, and follow", function () {
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-13";
  var zoom = 14;

  app.__test__.resetState();
  controller.map = {
    getCenter: function () {
      return { lat: 56.95, lng: 24.1 };
    },
    getZoom: function () {
      return zoom;
    },
  };
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-13",
        },
      },
    },
  });
  controller.focusedEntityKey = markerKey;
  controller.openPopupKey = markerKey;
  app.__test__.setPublicMapPopupSelection(markerKey, {
    movingMarkerTracking: true,
    movingTrainId: "train-13",
  });

  assert.equal(controller.beginUserMapGesture("zoom"), true);
  zoom = 15;
  assert.equal(controller.finishUserMapGesture("user-zoomed-map"), true);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, "");
  assert.equal(controller.openPopupKey, "");
  assert.equal(state.publicMapPopupKey, "");
  assert.equal(state.publicMapSelectedMarkerKey, "");
  assert.equal(state.mapFollowTrainId, "");
});

test("zooming still clears live train focus when moveend runs before zoomend", function () {
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-14";
  var zoom = 14;

  app.__test__.resetState();
  controller.map = {
    getCenter: function () {
      return { lat: 56.95, lng: 24.1 };
    },
    getZoom: function () {
      return zoom;
    },
    getSize: function () {
      return { x: 400, y: 200 };
    },
    latLngToContainerPoint: function () {
      return { x: 200, y: 100 };
    },
  };
  controller.markerState.set(markerKey, {
    item: {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-14",
        },
      },
    },
  });
  controller.focusedEntityKey = markerKey;
  controller.openPopupKey = markerKey;
  app.__test__.setPublicMapPopupSelection(markerKey, {
    movingMarkerTracking: true,
    movingTrainId: "train-14",
  });

  assert.equal(controller.beginUserMapGesture("move"), true);
  assert.equal(controller.beginUserMapGesture("zoom"), true);
  assert.equal(controller.finishUserMapGesture("user-moved-map", { deferIfZoomPending: true }), false);

  zoom = 15;
  assert.equal(controller.finishUserMapGesture("user-zoomed-map"), true);

  var state = app.__test__.getState();
  assert.equal(controller.focusedEntityKey, "");
  assert.equal(controller.openPopupKey, "");
  assert.equal(state.publicMapPopupKey, "");
  assert.equal(state.publicMapSelectedMarkerKey, "");
  assert.equal(state.mapFollowTrainId, "");
});

test("map relayout reapplies live follow after viewport changes", function () {
  var controller = app.__test__.createMapController();
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var invalidations = [];
  var followCalls = [];

  global.window.TRAIN_APP_CONFIG.mode = "public-network-map";
  app.__test__.resetState({
    publicMapSelectedMarkerKey: "live-train:train-relayout",
    mapFollowTrainId: "train-relayout",
    mapFollowPaused: false,
    publicMapFollowPaused: false,
    networkMapData: {
      stations: [],
      recentSightings: [],
    },
  });
  controller.map = {
    invalidateSize: function (value) {
      invalidations.push(value);
    },
  };
  controller.refreshViewportLayers = function () {};
  controller.restorePendingPopup = function () {};
  controller.panToMarker = function (markerKey) {
    followCalls.push(markerKey);
    return true;
  };

  try {
    controller.performLayout({
      shouldFit: false,
      shouldRestore: false,
      savedView: null,
      bounds: [],
    }, false);
  } finally {
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  assert.deepEqual(invalidations, [false]);
  assert.deepEqual(followCalls, ["live-train:train-relayout"]);
});

test("animateMarkerTo keeps a followed live train centered during marker animation", function () {
  var controller = app.__test__.createMapController();
  var markerKey = "live-train:train-15";
  var panCalls = [];
  var markerLatLng = { lat: 56.95, lng: 24.1 };
  var frameCallbacks = [];
  var originalRequestAnimationFrame = global.window.requestAnimationFrame;
  var originalCancelAnimationFrame = global.window.cancelAnimationFrame;
  var originalPerformance = global.window.performance;

  app.__test__.resetState();
  global.window.requestAnimationFrame = function (callback) {
    frameCallbacks.push(callback);
    return frameCallbacks.length;
  };
  global.window.cancelAnimationFrame = function () {};
  global.window.performance = {
    now: function () {
      return 0;
    },
  };
  controller.map = {
    getSize: function () {
      return { x: 400, y: 200 };
    },
    getZoom: function () {
      return 15;
    },
    latLngToContainerPoint: function () {
      return { x: 100, y: 50 };
    },
    containerPointToLatLng: function (point) {
      return [point.y, point.x];
    },
    panTo: function (latLng) {
      panCalls.push(latLng);
    },
  };

  var entry = {
    marker: {
      getLatLng: function () {
        return markerLatLng;
      },
      setLatLng: function (latLng) {
        markerLatLng = { lat: latLng[0], lng: latLng[1] };
      },
    },
    item: {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-15",
        },
      },
    },
    positionLatLng: [56.95, 24.1],
    targetLatLng: [56.95, 24.1],
    positionObservedAt: 0,
    lastMovementSyncAt: 0,
    animationFrame: 0,
  };

  controller.markerState.set(markerKey, entry);
  app.__test__.setMovingMapSelection({
    markerKey: markerKey,
    trainId: "train-15",
    paused: false,
  });

  try {
    controller.animateMarkerTo(entry, [56.96, 24.11], {
      markerKey: markerKey,
      interaction: {
        entityKey: markerKey,
        selectionOptions: {
          movingMarkerTracking: true,
          movingTrainId: "train-15",
        },
      },
      movementObservedAt: "2026-03-10T18:55:15Z",
    });

    assert.equal(frameCallbacks.length, 1);
    frameCallbacks.shift()(450);
    assert.equal(frameCallbacks.length, 1);
    frameCallbacks.shift()(900);
  } finally {
    global.window.requestAnimationFrame = originalRequestAnimationFrame;
    global.window.cancelAnimationFrame = originalCancelAnimationFrame;
    global.window.performance = originalPerformance;
  }

  assert.deepEqual(panCalls, [
    [50, 100],
    [50, 100],
  ]);
});

test("bindMapRelayoutListenersWithEnvironment requests relayout on viewport changes", function () {
  var windowHandlers = {};
  var telegramHandlers = {};
  var calls = [];
  var cleanup = app.__test__.bindMapRelayoutListenersWithEnvironment(
    {
      addEventListener: function (eventName, handler) {
        windowHandlers[eventName] = handler;
      },
      removeEventListener: function (eventName) {
        delete windowHandlers[eventName];
      },
    },
    {
      onEvent: function (eventName, handler) {
        telegramHandlers[eventName] = handler;
      },
      offEvent: function (eventName) {
        delete telegramHandlers[eventName];
      },
    },
    {
      requestRelayout: function (reason) {
        calls.push(reason);
      },
    }
  );

  windowHandlers.resize();
  windowHandlers.orientationchange();
  telegramHandlers.viewportChanged();
  cleanup();

  assert.deepEqual(calls, [
    "window-resize",
    "orientation-change",
    "telegram-viewport-changed",
  ]);
  assert.equal(windowHandlers.resize, undefined);
  assert.equal(telegramHandlers.viewportChanged, undefined);
});

test("applyPublicMapFollow pans to the selected live marker", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "public-network-map";
  app.__test__.resetState({
    publicMapSelectedMarkerKey: "live-train:demo",
    publicMapFollowPaused: false,
  });
  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return true;
  };

  try {
    app.__test__.applyPublicMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  assert.deepEqual(calls, ["live-train:demo"]);
});

test("public live marker selection starts shared follow state", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  global.window.TRAIN_APP_CONFIG.mode = "public-network-map";
  app.__test__.resetState({
    mapFollowPaused: true,
    publicMapFollowPaused: true,
  });

  try {
    app.__test__.setPublicMapPopupSelection("live-train:train-public", {
      movingMarkerTracking: true,
      movingTrainId: "train-public",
    });
  } finally {
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  var state = app.__test__.getState();
  assert.equal(state.publicMapSelectedMarkerKey, "live-train:train-public");
  assert.equal(state.mapFollowTrainId, "train-public");
  assert.equal(state.mapFollowPaused, false);
  assert.equal(state.publicMapFollowPaused, false);
});

test("applyMiniMapFollow keeps panning the tracked train marker", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalFeedApi = global.window.TrainExternalFeed;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TrainExternalFeed = {
    matchLocalTrain: function () {
      return {
        match: {
          train: {
            id: "train-map",
          },
        },
        localTrainId: "train-map",
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function () {
      return "train-map";
    },
  };
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "train-map",
    mapFollowTrainId: "train-map",
    mapFollowPaused: false,
    mapData: {
      train: {
        id: "train-map",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.95,
          longitude: 24.1,
        },
      ],
      stationSightings: [],
    },
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: new Date().toISOString(),
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });
  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return true;
  };

  try {
    app.__test__.applyMiniMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TrainExternalFeed = originalFeedApi;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  assert.deepEqual(calls, ["live-train:train-map"]);
});

test("applyMiniMapFollow keeps tracking the same train when the display source flips to projection", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalFeedApi = global.window.TrainExternalFeed;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TrainExternalFeed = {
    matchLocalTrain: function () {
      return {
        match: {
          train: {
            id: "train-map",
          },
        },
        localTrainId: "train-map",
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function () {
      return "train-map";
    },
  };
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "train-map",
    mapFollowTrainId: "train-map",
    mapFollowPaused: false,
    mapData: {
      train: {
        id: "train-map",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.95,
          longitude: 24.1,
        },
      ],
      stationSightings: [],
    },
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [
        {
          trainNumber: "6321",
          position: { lat: 56.95, lng: 24.1 },
          updatedAt: new Date().toISOString(),
          displaySource: "live",
          displayUpdatedAt: new Date().toISOString(),
          isGpsActive: true,
        },
      ],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });
  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return true;
  };

  try {
    app.__test__.applyMiniMapFollow();
    var stateAfterLive = app.__test__.getState();
    app.__test__.resetState(Object.assign({}, stateAfterLive, {
      externalFeed: Object.assign({}, stateAfterLive.externalFeed, {
        liveTrains: [
          {
            trainNumber: "6321",
            position: { lat: 56.951, lng: 24.101 },
            updatedAt: "",
            displaySource: "projection",
            displayUpdatedAt: new Date().toISOString(),
            isGpsActive: false,
          },
        ],
      }),
    }));
    app.__test__.applyMiniMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TrainExternalFeed = originalFeedApi;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  var state = app.__test__.getState();
  assert.equal(state.mapFollowTrainId, "train-map");
  assert.equal(state.mapFollowPaused, false);
  assert.equal(state.publicMapSelectedMarkerKey, "live-train:train-map");
  assert.deepEqual(calls, ["live-train:train-map", "live-train:train-map"]);
});

test("mini-app network map click follows in place and resumes after manual pause", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "",
    networkMapData: {
      stations: [],
      recentSightings: [],
    },
  });

  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return true;
  };

  try {
    app.__test__.setPublicMapPopupSelection("live-train:train-network", {
      movingMarkerTracking: true,
      movingTrainId: "train-network",
    });
    app.__test__.pauseMovingMapFollow("test-manual-pan");
    var pausedState = app.__test__.getState();
    assert.equal(pausedState.mapTrainId, "");
    assert.equal(pausedState.publicMapSelectedMarkerKey, "live-train:train-network");
    assert.equal(pausedState.mapFollowTrainId, "train-network");
    assert.equal(pausedState.mapFollowPaused, true);

    app.__test__.setPublicMapPopupSelection("live-train:train-network", {
      movingMarkerTracking: true,
      movingTrainId: "train-network",
    });
    app.__test__.applyMiniMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  var state = app.__test__.getState();
  assert.equal(state.mapTrainId, "");
  assert.equal(state.mapFollowPaused, false);
  assert.deepEqual(calls, ["live-train:train-network"]);
});

test("mini-app follow stays armed across marker gaps and resumes on the next update", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalFeedApi = global.window.TrainExternalFeed;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  global.window.TrainExternalFeed = {
    matchLocalTrain: function () {
      return {
        match: {
          train: {
            id: "train-map",
          },
        },
        localTrainId: "train-map",
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function () {
      return "train-map";
    },
  };
  app.__test__.resetState({
    tab: "map",
    mapTrainId: "train-map",
    mapFollowTrainId: "train-map",
    mapFollowPaused: false,
    mapData: {
      train: {
        id: "train-map",
      },
      stops: [
        {
          stationId: "riga",
          stationName: "Riga",
          latitude: 56.95,
          longitude: 24.1,
        },
      ],
      stationSightings: [],
    },
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });
  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return markerKey === "live-train:train-map";
  };

  try {
    app.__test__.applyMiniMapFollow();
    var pendingState = app.__test__.getState();
    assert.equal(pendingState.mapFollowTrainId, "train-map");
    assert.equal(pendingState.mapFollowPaused, false);
    assert.equal(pendingState.publicMapSelectedMarkerKey, "");
    assert.deepEqual(calls, []);

    app.__test__.resetState(Object.assign({}, pendingState, {
      externalFeed: Object.assign({}, pendingState.externalFeed, {
        liveTrains: [
          {
            trainNumber: "6321",
            position: { lat: 56.95, lng: 24.1 },
            updatedAt: new Date().toISOString(),
            isGpsActive: true,
          },
        ],
      }),
    }));
    app.__test__.applyMiniMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TrainExternalFeed = originalFeedApi;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  var resumedState = app.__test__.getState();
  assert.equal(resumedState.mapFollowTrainId, "train-map");
  assert.equal(resumedState.mapFollowPaused, false);
  assert.equal(resumedState.publicMapSelectedMarkerKey, "live-train:train-map");
  assert.deepEqual(calls, ["live-train:train-map"]);
});

test("public map follow stays armed across marker gaps and resumes on the next update", function () {
  var originalMode = global.window.TRAIN_APP_CONFIG.mode;
  var originalFeedApi = global.window.TrainExternalFeed;
  var originalPanToMarker = app.__test__.mapController.panToMarker;
  global.window.TRAIN_APP_CONFIG.mode = "public-network-map";
  global.window.TrainExternalFeed = {
    matchLocalTrain: function () {
      return {
        match: {
          train: {
            id: "train-public",
          },
        },
        localTrainId: "train-public",
      };
    },
    normalizeStationKey: function (value) {
      return String(value || "").trim().toLowerCase();
    },
    stableExternalTrainIdentity: function () {
      return "train-public";
    },
  };
  app.__test__.resetState({
    publicMapSelectedMarkerKey: "live-train:missing",
    mapFollowTrainId: "train-public",
    mapFollowPaused: false,
    publicMapFollowPaused: false,
    publicDashboardAll: [
      {
        train: {
          id: "train-public",
        },
      },
    ],
    networkMapData: {
      stations: [],
      recentSightings: [],
    },
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });
  var calls = [];
  app.__test__.mapController.panToMarker = function (markerKey) {
    calls.push(markerKey);
    return markerKey === "live-train:train-public";
  };

  try {
    app.__test__.applyPublicMapFollow();
    var pendingState = app.__test__.getState();
    assert.equal(pendingState.publicMapSelectedMarkerKey, "live-train:missing");
    assert.equal(pendingState.mapFollowTrainId, "train-public");
    assert.equal(pendingState.mapFollowPaused, false);

    app.__test__.resetState(Object.assign({}, pendingState, {
      externalFeed: Object.assign({}, pendingState.externalFeed, {
        liveTrains: [
          {
            trainNumber: "6321",
            position: { lat: 56.95, lng: 24.1 },
            updatedAt: new Date().toISOString(),
            isGpsActive: true,
          },
        ],
      }),
    }));
    app.__test__.applyPublicMapFollow();
  } finally {
    app.__test__.mapController.panToMarker = originalPanToMarker;
    global.window.TrainExternalFeed = originalFeedApi;
    global.window.TRAIN_APP_CONFIG.mode = originalMode;
  }

  var resumedState = app.__test__.getState();
  assert.equal(resumedState.publicMapSelectedMarkerKey, "live-train:train-public");
  assert.equal(resumedState.mapFollowTrainId, "train-public");
  assert.equal(resumedState.mapFollowPaused, false);
  assert.equal(resumedState.publicMapFollowPaused, false);
  assert.deepEqual(calls, [
    "live-train:missing",
    "live-train:missing",
    "live-train:train-public",
  ]);
});

test("network activity trains are sorted with eventful departures first", function () {
  app.__test__.resetState({
    publicDashboardAll: [
      { train: { id: "train-a" }, status: { state: "NO_REPORTS" } },
      { train: { id: "train-b" }, status: { state: "LAST_SIGHTING" } },
      { train: { id: "train-c" }, status: { state: "MIXED_REPORTS" } },
      { train: { id: "train-d" }, status: { state: "NO_REPORTS" } },
    ],
  });

  assert.deepEqual(app.__test__.sortedNetworkMapActivityTrainIds(), ["train-b", "train-c", "train-a", "train-d"]);
});

test("mini network map content explains live and projected trains and omits the activity card", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";
  var html = app.__test__.renderMiniNetworkMapContent({
    messages: {
      ride_riders: "%s riders",
    },
    networkMapData: {
      stations: [],
      recentSightings: [],
    },
    publicDashboardAll: [
      {
        train: {
          id: "train-mixed",
          fromStation: "Riga",
          toStation: "Jelgava",
          departureAt: "2026-03-10T18:55:00Z",
          arrivalAt: "2026-03-10T19:40:00Z",
        },
        status: {
          state: "MIXED_REPORTS",
        },
        riders: 1,
      },
    ],
  });

  assert.match(html, /Showing live GPS trains and recent projected positions when GPS drops out/);
  assert.doesNotMatch(html, /All train activity/);
  assert.doesNotMatch(html, /Status: Mixed reports/);
  assert.doesNotMatch(html, /1 riders/);
});

test("renderPublicStatusBar preserves action order and trailing markup", function () {
  var html = app.__test__.renderPublicStatusBar({
    textId: "status-id",
    statusText: "Public read-only view.",
    actions: [
      '<a class="button ghost small" href="/feed">Live feed</a>',
      '<a class="button ghost small" href="/incidents">Incidents</a>',
    ],
    trailingHTML: '<span class="status-pill">12:00</span>',
  });

  assert.match(html, /id="status-id"/);
  assert.match(html, /Public read-only view\./);
  assert.ok(html.indexOf('href="/feed"') < html.indexOf('href="/incidents"'));
  assert.match(html, /status-pill/);
});

test("renderPublicDashboardStatusBar keeps the station-search CTA", function () {
  global.window.TRAIN_APP_CONFIG.basePath = "";

  var html = app.__test__.renderPublicDashboardStatusBar();

  assert.match(html, /href="\/stations"/);
  assert.match(html, /Station search/);
  assert.doesNotMatch(html, /href="\/incidents"/);
  assert.doesNotMatch(html, /href="\/map"/);
});

test("renderPublicMapStatusBar keeps status, departures, and incidents exits together", function () {
  global.window.TRAIN_APP_CONFIG.basePath = "";
  global.window.TRAIN_APP_CONFIG.trainId = "train-1";

  var html = app.__test__.renderPublicMapStatusBar();

  assert.match(html, /id="public-map-status-text"/);
  assert.match(html, /href="\/t\/train-1"/);
  assert.match(html, /href="\/feed"/);
  assert.match(html, /href="\/incidents"/);
});

test("renderPublicStationStatusBar keeps departures and refresh actions", function () {
  global.window.TRAIN_APP_CONFIG.basePath = "";

  var html = app.__test__.renderPublicStationStatusBar();

  assert.match(html, /id="public-station-refresh"/);
  assert.match(html, /href="\/feed"/);
  assert.doesNotMatch(html, /href="\/map"/);
  assert.doesNotMatch(html, /href="\/incidents"/);
});

test("renderPublicIncidentsStatusBar exposes exits back to main public views", function () {
  global.window.TRAIN_APP_CONFIG.basePath = "";

  var html = app.__test__.renderPublicIncidentsStatusBar();

  assert.match(html, /href="\/feed"/);
  assert.match(html, /href="\/stations"/);
  assert.match(html, /href="\/map"/);
  assert.doesNotMatch(html, /href="\/incidents"/);
});

test("renderIncidentDetailPanel keeps the draft comment and clearer empty copy", function () {
  app.__test__.resetState({
    authenticated: true,
    publicIncidentCommentDrafts: { "train:abc:ctx": "Still seeing checks" },
  });

  var html = app.__test__.renderIncidentDetailPanel({
    summary: {
      id: "train:abc:ctx",
      subjectName: "Riga -> Jelgava",
      lastReportName: "Inspection started",
      lastReportAt: "2026-03-10T18:55:00Z",
      lastReporter: "Amber Scout 101",
      votes: { ongoing: 2, cleared: 1, userValue: "ONGOING" },
    },
    events: [],
    comments: [],
  });

  assert.match(html, /Still seeing checks/);
  assert.match(html, /No activity yet for this incident\./);
  assert.match(html, /No comments yet for this incident\./);
  assert.match(html, /button class="secondary small" data-action="incident-vote" data-incident-id="train:abc:ctx" data-value="ONGOING"/);
});

test("sortIncidentSummaries prefers the newest related activity over the last report time", function () {
  var ordered = app.__test__.sortIncidentSummaries([
    {
      id: "train:older-report:newer-activity",
      lastReportAt: "2026-03-10T18:00:00Z",
      lastActivityAt: "2026-03-10T19:10:00Z",
    },
    {
      id: "train:newer-report:older-activity",
      lastReportAt: "2026-03-10T19:00:00Z",
      lastActivityAt: "2026-03-10T19:05:00Z",
    },
  ]);

  assert.deepEqual(ordered.map(function (item) { return item.id; }), [
    "train:older-report:newer-activity",
    "train:newer-report:older-activity",
  ]);
});

test("renderIncidentSummaryCard highlights the newest activity instead of the last report", function () {
  var html = app.__test__.renderIncidentSummaryCard({
    id: "train:abc:ctx",
    subjectName: "Riga -> Jelgava",
    lastReportName: "Inspection started",
    lastReportAt: "2026-03-10T18:55:00Z",
    lastReporter: "Amber Scout 101",
    lastActivityName: "Comment",
    lastActivityAt: "2026-03-10T19:10:00Z",
    lastActivityActor: "Amber Scout 202",
    commentCount: 2,
    votes: { ongoing: 2, cleared: 1 },
  });

  assert.match(html, /Comment/);
  assert.match(html, /Amber Scout 202/);
});

test("public network map panel omits sighting controls in live-only mode", function () {
  global.window.TRAIN_APP_CONFIG.mode = "public-network-map";

  app.__test__.resetState({
    networkMapData: {
      liveOnly: true,
    },
    publicDashboardAll: [],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });
  var html = app.__test__.renderPublicNetworkMapPanel();

  assert.doesNotMatch(html, /Show older and unrelated sightings/);
  assert.doesNotMatch(html, /Recent platform sightings/);
  assert.doesNotMatch(html, /public-network-map-sightings-card/);
});

test("mini network map content omits sighting controls in live-only mode", function () {
  global.window.TRAIN_APP_CONFIG.mode = "mini-app";

  var html = app.__test__.renderMiniNetworkMapContent({
    networkMapData: {
      liveOnly: true,
    },
    publicDashboardAll: [],
    externalFeed: {
      enabled: true,
      connectionState: "live",
      routes: [],
      liveTrains: [],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      error: "",
    },
  });

  assert.doesNotMatch(html, /Show older and unrelated sightings/);
  assert.doesNotMatch(html, /Recent platform sightings/);
  assert.doesNotMatch(html, /mini-network-map-sightings-card/);
});

test("refreshNetworkMapData no longer fetches the legacy public map payload", async function () {
  var originalFetch = global.fetch;
  global.fetch = function () {
    throw new Error("legacy public map should not be fetched");
  };

  try {
    app.__test__.resetState({
      networkMapData: null,
    });

    var changed = await app.__test__.refreshNetworkMapData(true);

    assert.equal(changed, true);
    assert.deepEqual(app.__test__.getState().networkMapData, {
      liveOnly: true,
    });
  } finally {
    global.fetch = originalFetch;
  }
});

test("refreshPublicIncidentDetail loads incident detail from the server and keeps stable state on repeats", async function () {
  var originalFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      global.fetch = async function (url) {
        fetchCalls.push(url);
        if (url === "/pixel-stack/train/api/v1/public/incidents/train%3Aabc%3Actx") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({
                summary: {
                  id: "train:abc:ctx",
                  title: "Inspection seen",
                  lastActivityAt: "2026-03-27T10:30:00Z",
                },
                timeline: [],
                comments: [],
              });
            },
          };
        }
        throw new Error("unexpected fetch " + url);
      };

      app.__test__.resetState({
        publicIncidentSelectedId: "",
        publicIncidentDetail: {
          summary: {
            id: "train:stale:ctx",
          },
        },
      });
      var first = await app.__test__.refreshPublicIncidentDetail("train:abc:ctx");
      var second = await app.__test__.refreshPublicIncidentDetail("train:abc:ctx");
      assert.equal(first, true);
      assert.equal(second, false);
      assert.equal(app.__test__.getState().publicIncidentSelectedId, "train:abc:ctx");
      assert.equal(app.__test__.getState().publicIncidentDetail.summary.id, "train:abc:ctx");
    });
  } finally {
    global.fetch = originalFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/incidents/train%3Aabc%3Actx",
    "/pixel-stack/train/api/v1/public/incidents/train%3Aabc%3Actx",
  ]);
});

test("refreshMapData starts the server stop and status requests in parallel", async function () {
  var originalFetch = global.fetch;
  var resolvers = {};
  var calls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      global.fetch = function (url) {
        if (url === "/pixel-stack/train/api/v1/trains/train-42/stops") {
          calls.push("trainStops");
          return new Promise(function (resolve) {
            resolvers.trainStops = resolve;
          });
        }
        if (url === "/pixel-stack/train/api/v1/trains/train-42/status") {
          calls.push("trainStatus");
          return new Promise(function (resolve) {
            resolvers.trainStatus = resolve;
          });
        }
        throw new Error("unexpected fetch " + url);
      };

      app.__test__.resetState({
        authenticated: true,
        mapTrainId: "",
        mapData: null,
        mapTrainDetail: null,
      });
      var primaryCalls = 0;
      var loading = app.__test__.refreshMapData("train-42", false, "", {
        onPrimaryData: function () {
          primaryCalls += 1;
        },
      });
      await new Promise(function (resolve) {
        setImmediate(resolve);
      });

      assert.deepEqual(calls, ["trainStops", "trainStatus"]);
      assert.equal(app.__test__.getState().mapLoadState.active, true);
      assert.equal(app.__test__.getState().mapLoadState.mode, "train");

      resolvers.trainStops({
        ok: true,
        status: 200,
        text: async function () {
          return JSON.stringify({
            train: { id: "train-42" },
            stops: [
              {
                stationId: "riga",
                latitude: 56.95,
                longitude: 24.1,
              },
            ],
          });
        },
      });
      await new Promise(function (resolve) {
        setImmediate(resolve);
      });

      assert.equal(primaryCalls, 1);
      assert.equal(app.__test__.getState().mapData.train.id, "train-42");
      assert.equal(app.__test__.getState().mapTrainDetail, null);
      assert.equal(app.__test__.getState().mapLoadState.active, false);

      resolvers.trainStatus({
        ok: true,
        status: 200,
        text: async function () {
          return JSON.stringify({
            trainCard: {
              train: { id: "train-42" },
            },
          });
        },
      });
      await loading;

      assert.equal(app.__test__.getState().mapTrainDetail.trainCard.train.id, "train-42");
    });
  } finally {
    global.fetch = originalFetch;
  }
});

test("refreshNetworkMapData clears the loader immediately after seeding live-only state", async function () {
  await withAppConfig({
    mode: "mini-app",
    spacetimeHost: "https://stdb.example",
    spacetimeDatabase: "train-db",
  }, async function () {
    app.__test__.resetState({
      networkMapData: null,
    });

    var changed = await app.__test__.refreshNetworkMapData(true);

    assert.equal(changed, true);
    assert.equal(app.__test__.getState().mapLoadState.active, false);
    assert.deepEqual(app.__test__.getState().networkMapData, {
      liveOnly: true,
    });
  });
});

test("refreshPublicIncidents loads incidents from the server and refreshes the selected detail", async function () {
  var originalFetch = global.fetch;
  var fetchCalls = [];

  try {
    await withAppConfig({
      basePath: "/pixel-stack/train",
      spacetimeHost: "https://stdb.example",
      spacetimeDatabase: "train-db",
    }, async function () {
      global.fetch = async function (url) {
        fetchCalls.push(url);
        if (url === "/pixel-stack/train/api/v1/public/incidents?limit=60") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({
                incidents: [
                  {
                    id: "train:abc:ctx",
                    title: "Inspection seen",
                    lastActivityAt: "2026-03-27T10:30:00Z",
                  },
                ],
              });
            },
          };
        }
        if (url === "/pixel-stack/train/api/v1/public/incidents/train%3Aabc%3Actx") {
          return {
            ok: true,
            status: 200,
            text: async function () {
              return JSON.stringify({
                summary: {
                  id: "train:abc:ctx",
                  title: "Inspection seen",
                  lastActivityAt: "2026-03-27T10:30:00Z",
                },
                timeline: [],
                comments: [],
              });
            },
          };
        }
        throw new Error("unexpected fetch " + url);
      };

      app.__test__.resetState({
        publicIncidents: [
          {
            id: "train:stale:ctx",
          },
        ],
        publicIncidentSelectedId: "train:stale:ctx",
        publicIncidentDetail: {
          summary: {
            id: "train:stale:ctx",
          },
        },
      });
      var first = await app.__test__.refreshPublicIncidents();
      var second = await app.__test__.refreshPublicIncidents();
      assert.equal(first, true);
      assert.equal(second, false);
      assert.deepEqual(app.__test__.getState().publicIncidents, [
        {
          id: "train:abc:ctx",
          title: "Inspection seen",
          lastActivityAt: "2026-03-27T10:30:00Z",
        },
      ]);
      assert.equal(app.__test__.getState().publicIncidentSelectedId, "train:abc:ctx");
      assert.equal(app.__test__.getState().publicIncidentDetail.summary.id, "train:abc:ctx");
    });
  } finally {
    global.fetch = originalFetch;
  }

  assert.deepEqual(fetchCalls, [
    "/pixel-stack/train/api/v1/public/incidents?limit=60",
    "/pixel-stack/train/api/v1/public/incidents/train%3Aabc%3Actx",
    "/pixel-stack/train/api/v1/public/incidents?limit=60",
  ]);
});

test("applyPublicDashboardPayload stores the full list and derives the visible dashboard slice", function () {
  var trains = [];
  for (var i = 0; i < 75; i++) {
    trains.push({
      train: {
        id: `train-${i}`,
      },
      status: {
        state: "NO_REPORTS",
      },
      timeline: [],
      stationSightings: [],
    });
  }

  app.__test__.resetState({
    publicDashboard: [],
    publicDashboardAll: [],
    scheduleMeta: null,
  });

  var changed = app.__test__.applyPublicDashboardPayload({
    trains: trains,
    schedule: {
      available: true,
      effectiveServiceDate: "2026-03-26",
    },
  });

  var state = app.__test__.getState();
  assert.equal(changed, true);
  assert.equal(state.publicDashboardAll.length, 75);
  assert.equal(state.publicDashboard.length, 60);
  assert.equal(state.publicDashboard[0].train.id, "train-0");
  assert.equal(state.publicDashboard[59].train.id, "train-59");
  assert.equal(state.publicDashboardAll[74].train.id, "train-74");
  assert.equal(state.scheduleMeta.effectiveServiceDate, "2026-03-26");
});

test("filterBundleStations matches plain ASCII queries against diacritics", function () {
  var matches = app.__test__.filterBundleStations([
    { id: "riga", name: "Rīga", normalizedKey: "rīga" },
    { id: "jelgava", name: "Jelgava", normalizedKey: "jelgava" },
  ], "riga");

  assert.equal(matches.length, 1);
  assert.equal(matches[0].name, "Rīga");
});
