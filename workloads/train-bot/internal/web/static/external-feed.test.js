"use strict";

var test = require("node:test");
var assert = require("node:assert/strict");

var externalFeed = require("./external-feed.js");

function isoAt(baseMs, offsetMs) {
  return new Date(baseMs + (offsetMs || 0)).toISOString();
}

function liveTrainSample(overrides) {
  return Object.assign({
    routeId: "route-6326",
    serviceDate: "2026-03-10",
    trainNumber: "6326",
    updatedAt: "2026-03-10T10:00:00.000Z",
    animatedPosition: null,
    rawPosition: null,
    position: null,
    isGpsActive: false,
    stops: [],
    polyline: [],
    origin: "Riga",
    destination: "Majori",
  }, overrides || {});
}

test("normalizeTrainGraphPayload strips source-only fields and keeps join data", function () {
  var payload = [
    {
      id: "route-6101",
      name: "Riga - Daugavpils",
      train: "6101",
      schDate: "2026-03-06",
      departure: "09:10",
      arrival: "12:45",
      fuelType: "electric",
      logo: "/assets/vivi.svg",
      stops: [
        {
          id: "stop-riga",
          gps_id: "gps-riga",
          title: "Rīga",
          departure: "09:10",
          coords: [56.946285, 24.105078],
          workingTime: "24/7",
          waitingRoom: true,
          wc: true,
          coffeMachine: true,
          stationNotes: "Source only",
          adress: "Stacijas laukums 2"
        },
        {
          id: "stop-dgp",
          gps_id: "gps-dgp",
          title: "Daugavpils",
          departure: "12:45",
          coords: [55.874736, 26.536179]
        }
      ]
    }
  ];

  var normalized = externalFeed.normalizeTrainGraphPayload(payload);
  assert.equal(normalized.routes.length, 1);

  var route = normalized.routes[0];
  assert.equal(route.routeId, "route-6101");
  assert.equal(route.serviceDate, "2026-03-06");
  assert.equal(route.trainNumber, "6101");
  assert.equal(route.origin, "Rīga");
  assert.equal(route.destination, "Daugavpils");
  assert.equal(route.stops.length, 2);
  assert.deepEqual(route.stops[0].position, { lat: 56.946285, lng: 24.105078 });
  assert.equal(route.stops[0].titleKey, "riga");
  assert.ok(!("workingTime" in route.stops[0]));
  assert.ok(!("wc" in route.stops[0]));
  assert.ok(!("coffeMachine" in route.stops[0]));
  assert.ok(!("stationNotes" in route.stops[0]));
  assert.ok(!("adress" in route.stops[0]));
});

test("normalizeBackEndFrame keeps live movement fields only", function () {
  var payload = {
    type: "back-end",
    name: "Riga - Daugavpils",
    stopCoordArray: [
      [56.946285, 24.105078],
      [55.874736, 26.536179]
    ],
    returnValue: {
      id: "748081",
      train: "804",
      animatedCoord: [56.95, 24.11],
      position: [56.94, 24.1],
      nextTime: "10:20",
      arrivingTime: "10:18",
      departureTime: "09:55",
      arrivalTime: "12:05",
      waitingTime: "2m",
      currentStopIndex: 0,
      stopped: false,
      finished: false,
      isGpsActive: true,
      updaterTimeStamp: 1710000000000,
      nextStopObj: {
        id: "stop-1",
        title: "Ogre",
        departure: "10:20",
        coords: [56.8167, 24.6],
        stationNotes: "discard me"
      },
      stopObjArray: [
        {
          id: "stop-0",
          title: "Rīga",
          departure: "09:55",
          coords: [56.946285, 24.105078],
          wc: true
        },
        {
          id: "stop-1",
          title: "Ogre",
          departure: "10:20",
          coords: [56.8167, 24.6]
        },
        {
          id: "stop-2",
          title: "Daugavpils",
          departure: "12:05",
          coords: [55.874736, 26.536179]
        }
      ]
    }
  };

  var normalized = externalFeed.normalizeBackEndFrame(payload);
  assert.equal(normalized.routeId, "748081");
  assert.equal(normalized.trainNumber, "804");
  assert.deepEqual(normalized.position, { lat: 56.94, lng: 24.1 });
  assert.equal(normalized.displaySource, "live");
  assert.equal(normalized.isGpsActive, true);
  assert.equal(normalized.currentStop.title, "Rīga");
  assert.equal(normalized.nextStop.title, "Ogre");
  assert.equal(normalized.origin, "Rīga");
  assert.equal(normalized.destination, "Daugavpils");
  assert.equal(normalized.polyline.length, 2);
  assert.ok(!("stationNotes" in normalized.nextStop));
});

test("normalizeBackEndFrame falls back to the raw point as projection when GPS is inactive", function () {
  var normalized = externalFeed.normalizeBackEndFrame({
    type: "back-end",
    returnValue: {
      id: "748082",
      train: "805",
      position: [56.941, 24.101],
      isGpsActive: false,
      updaterTimeStamp: 1710000001000,
    }
  });

  assert.equal(normalized.displaySource, "projection");
  assert.deepEqual(normalized.position, { lat: 56.941, lng: 24.101 });
});

test("stabilizeLiveTrainPositions keeps the majority source across the last 9 samples", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:00:00Z");
  var snapshot = null;

  for (var projectionIndex = 0; projectionIndex < 5; projectionIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, projectionIndex * 1000),
        animatedPosition: { lat: 56.95 + (projectionIndex * 0.001), lng: 24.10 + (projectionIndex * 0.001) },
      })
    ], memory, baseMs + (projectionIndex * 1000))[0];
  }

  for (var liveIndex = 0; liveIndex < 4; liveIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, (5 + liveIndex) * 1000),
        rawPosition: { lat: 56.80 + (liveIndex * 0.001), lng: 24.30 + (liveIndex * 0.001) },
        isGpsActive: true,
      })
    ], memory, baseMs + ((5 + liveIndex) * 1000))[0];
  }

  assert.equal(snapshot.displaySource, "projection");
  assert.equal(snapshot.position.lat, 56.954);
  assert.ok(Math.abs(snapshot.position.lng - 24.104) < 1e-9);

  snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 9000),
      rawPosition: { lat: 56.91, lng: 24.41 },
      isGpsActive: true,
    })
  ], memory, baseMs + 9000)[0];

  assert.equal(snapshot.displaySource, "live");
  assert.deepEqual(snapshot.position, { lat: 56.91, lng: 24.41 });
});

test("stabilizeLiveTrainPositions uses the observed GPS mode when both coordinate sets are present", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:02:00Z");
  var snapshot = null;

  for (var projectionIndex = 0; projectionIndex < 5; projectionIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, projectionIndex * 1000),
        animatedPosition: { lat: 56.90 + (projectionIndex * 0.001), lng: 24.50 + (projectionIndex * 0.001) },
        rawPosition: { lat: 56.70 + (projectionIndex * 0.001), lng: 24.10 + (projectionIndex * 0.001) },
        isGpsActive: false,
      })
    ], memory, baseMs + (projectionIndex * 1000))[0];
  }

  for (var liveIndex = 0; liveIndex < 4; liveIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, (5 + liveIndex) * 1000),
        animatedPosition: { lat: 56.91 + (liveIndex * 0.001), lng: 24.51 + (liveIndex * 0.001) },
        rawPosition: { lat: 56.80 + (liveIndex * 0.001), lng: 24.20 + (liveIndex * 0.001) },
        isGpsActive: true,
      })
    ], memory, baseMs + ((5 + liveIndex) * 1000))[0];
  }

  assert.equal(snapshot.displaySource, "projection");
  assert.deepEqual(snapshot.position, { lat: 56.913, lng: 24.513 });

  snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 9000),
      animatedPosition: { lat: 56.95, lng: 24.55 },
      rawPosition: { lat: 56.85, lng: 24.25 },
      isGpsActive: true,
    })
  ], memory, baseMs + 9000)[0];

  assert.equal(snapshot.displaySource, "live");
  assert.deepEqual(snapshot.position, { lat: 56.85, lng: 24.25 });
});

test("stabilizeLiveTrainPositions switches between projection and live without one-sample flicker", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:05:00Z");
  var snapshot = null;

  for (var liveIndex = 0; liveIndex < 5; liveIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, liveIndex * 1000),
        rawPosition: { lat: 56.70 + (liveIndex * 0.001), lng: 24.10 + (liveIndex * 0.001) },
        isGpsActive: true,
      })
    ], memory, baseMs + (liveIndex * 1000))[0];
  }
  assert.equal(snapshot.displaySource, "live");

  for (var projectionIndex = 0; projectionIndex < 4; projectionIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, (5 + projectionIndex) * 1000),
        animatedPosition: { lat: 56.90 + (projectionIndex * 0.001), lng: 24.50 + (projectionIndex * 0.001) },
      })
    ], memory, baseMs + ((5 + projectionIndex) * 1000))[0];
  }
  assert.equal(snapshot.displaySource, "live");

  snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 9000),
      animatedPosition: { lat: 56.95, lng: 24.55 },
    })
  ], memory, baseMs + 9000)[0];
  assert.equal(snapshot.displaySource, "projection");

  snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 10000),
      rawPosition: { lat: 56.75, lng: 24.15 },
      isGpsActive: true,
    })
  ], memory, baseMs + 10000)[0];
  assert.equal(snapshot.displaySource, "projection");

  for (var returnIndex = 0; returnIndex < 4; returnIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, (11 + returnIndex) * 1000),
        rawPosition: { lat: 56.76 + (returnIndex * 0.001), lng: 24.16 + (returnIndex * 0.001) },
        isGpsActive: true,
      })
    ], memory, baseMs + ((11 + returnIndex) * 1000))[0];
  }

  assert.equal(snapshot.displaySource, "live");
});

test("stabilizeLiveTrainPositions uses the raw point as a projection fallback when GPS is inactive", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:07:00Z");
  var snapshot = null;

  for (var projectionIndex = 0; projectionIndex < 5; projectionIndex += 1) {
    snapshot = externalFeed.stabilizeLiveTrainPositions([
      liveTrainSample({
        updatedAt: isoAt(baseMs, projectionIndex * 1000),
        animatedPosition: { lat: 56.40 + (projectionIndex * 0.001), lng: 24.40 + (projectionIndex * 0.001) },
        rawPosition: { lat: 56.20 + (projectionIndex * 0.001), lng: 24.20 + (projectionIndex * 0.001) },
        isGpsActive: false,
      })
    ], memory, baseMs + (projectionIndex * 1000))[0];
  }

  snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 6000),
      rawPosition: { lat: 56.99, lng: 24.99 },
      isGpsActive: false,
    })
  ], memory, baseMs + 6000)[0];

  assert.equal(snapshot.displaySource, "projection");
  assert.deepEqual(snapshot.position, { lat: 56.99, lng: 24.99 });
  assert.equal(snapshot.displayUpdatedAt, isoAt(baseMs, 6000));
});

test("stabilizeLiveTrainPositions keeps the last location when the current sample has no usable point", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:10:00Z");

  externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 0),
      rawPosition: { lat: 56.88, lng: 24.22 },
      isGpsActive: true,
    })
  ], memory, baseMs);

  var snapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 60000),
    })
  ], memory, baseMs + 60000)[0];

  assert.equal(snapshot.displaySource, "live");
  assert.deepEqual(snapshot.position, { lat: 56.88, lng: 24.22 });
  assert.equal(snapshot.displayUpdatedAt, isoAt(baseMs, 0));
});

test("stabilizeLiveTrainPositions keeps the last location through a short full-frame gap", function () {
  var memory = new Map();
  var baseMs = Date.parse("2026-03-10T10:15:00Z");

  externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 0),
      animatedPosition: { lat: 56.99, lng: 24.33 },
    })
  ], memory, baseMs);

  var remembered = externalFeed.stabilizeLiveTrainPositions([], memory, baseMs + 120000);
  assert.equal(remembered.length, 1);
  assert.equal(remembered[0].displaySource, "projection");
  assert.deepEqual(remembered[0].position, { lat: 56.99, lng: 24.33 });

  var expired = externalFeed.stabilizeLiveTrainPositions([], memory, baseMs + (7 * 60 * 1000));
  assert.deepEqual(expired, []);
});

test("stabilizeLiveTrainPositions keeps ties stable and prefers live when there is no prior source", function () {
  var baseMs = Date.parse("2026-03-10T10:20:00Z");

  var firstMemory = new Map();
  var firstSnapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 0),
      animatedPosition: { lat: 56.60, lng: 24.60 },
      rawPosition: { lat: 56.61, lng: 24.61 },
      isGpsActive: true,
    })
  ], firstMemory, baseMs)[0];
  assert.equal(firstSnapshot.displaySource, "live");
  assert.deepEqual(firstSnapshot.position, { lat: 56.61, lng: 24.61 });

  var secondMemory = new Map();
  externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 0),
      animatedPosition: { lat: 56.70, lng: 24.70 },
    })
  ], secondMemory, baseMs);

  var tiedSnapshot = externalFeed.stabilizeLiveTrainPositions([
    liveTrainSample({
      updatedAt: isoAt(baseMs, 1000),
      rawPosition: { lat: 56.71, lng: 24.71 },
      isGpsActive: true,
    })
  ], secondMemory, baseMs + 1000)[0];

  assert.equal(tiedSnapshot.displaySource, "projection");
  assert.deepEqual(tiedSnapshot.position, { lat: 56.70, lng: 24.70 });
});

test("normalizeActiveStopsFrame strips source-only station metadata", function () {
  var payload = {
    data: [
      {
        id: "68",
        title: "Aizkraukle",
        coords: [56.62264915, 25.275455649999998],
        train: "702",
        routes_id: "748111",
        currentStopIndex: 6,
        stopIndex: 2,
        animatedCoord: [56.14481405, 26.3631758],
        directionList: [1, 2, 7],
        workingTime: ["discard"],
        waitingRoom: "1",
        wc: "1",
        coffeMachine: "0",
        stationNotes: "discard",
        adress: "discard"
      }
    ]
  };

  var normalized = externalFeed.normalizeActiveStopsFrame(payload);
  assert.equal(normalized.length, 1);
  assert.equal(normalized[0].stationId, "68");
  assert.equal(normalized[0].title, "Aizkraukle");
  assert.equal(normalized[0].trainNumber, "702");
  assert.deepEqual(normalized[0].position, { lat: 56.62264915, lng: 25.275455649999998 });
  assert.ok(!("workingTime" in normalized[0]));
  assert.ok(!("wc" in normalized[0]));
  assert.ok(!("coffeMachine" in normalized[0]));
  assert.ok(!("adress" in normalized[0]));
});

test("matchLocalTrain prefers exact service-date train id matches", function () {
  var localTrains = [
    {
      id: "2026-03-06-train-6101",
      serviceDate: "2026-03-06",
      trainNumber: "6101",
      origin: "Rīga",
      destination: "Daugavpils",
      departureTime: "09:10"
    },
    {
      id: "2026-03-06-train-6102",
      serviceDate: "2026-03-06",
      trainNumber: "6102",
      origin: "Rīga",
      destination: "Aizkraukle",
      departureTime: "09:20"
    }
  ];

  var result = externalFeed.matchLocalTrain(
    {
      serviceDate: "2026-03-06",
      trainNumber: "6101",
      origin: "Rīga",
      destination: "Daugavpils",
      departureTime: "09:10"
    },
    localTrains
  );

  assert.ok(result);
  assert.equal(result.matchType, "exact-id");
  assert.equal(result.localTrainId, "2026-03-06-train-6101");
  assert.equal(result.match, localTrains[0]);
});

test("matchLocalTrain falls back to same train number on the same service date", function () {
  var localTrains = [
    {
      id: "local-shadow-id",
      serviceDate: "2026-03-06",
      trainNumber: "804",
      origin: "Rīga",
      destination: "Jelgava",
      departureTime: "08:45"
    }
  ];

  var result = externalFeed.matchLocalTrain(
    {
      serviceDate: "2026-03-06",
      trainNumber: "804",
      origin: "Rīga",
      destination: "Jelgava",
      departureTime: "08:45"
    },
    localTrains
  );

  assert.ok(result);
  assert.equal(result.matchType, "train-number-same-day");
  assert.equal(result.match, localTrains[0]);
});

test("matchLocalTrain falls back to route and departure window when ids miss", function () {
  var localTrains = [
    {
      id: "2026-03-06-train-7001",
      serviceDate: "2026-03-06",
      trainNumber: "7001",
      origin: "Rīga",
      destination: "Valmiera",
      departureTime: "10:00"
    },
    {
      id: "shadow-train",
      serviceDate: "2026-03-06",
      trainNumber: "9999",
      origin: "Rīga",
      destination: "Daugavpils",
      departureTime: "09:11"
    }
  ];

  var result = externalFeed.matchLocalTrain(
    {
      serviceDate: "2026-03-06",
      trainNumber: "6101",
      origin: "Riga",
      destination: "Daugavpils",
      departureTime: "09:10"
    },
    localTrains
  );

  assert.ok(result);
  assert.equal(result.matchType, "route-time-window");
  assert.equal(result.match, localTrains[1]);
});

test("createLocalTrainMatcher reuses prepared local data without changing match results", function () {
  var localTrains = [
    {
      id: "2026-03-06-train-6101",
      serviceDate: "2026-03-06",
      trainNumber: "6101",
      origin: "Rīga",
      destination: "Daugavpils",
      departureTime: "09:10"
    },
    {
      id: "shadow-train",
      serviceDate: "2026-03-06",
      trainNumber: "804",
      origin: "Rīga",
      destination: "Jelgava",
      departureTime: "08:45"
    },
    {
      id: "route-window",
      serviceDate: "2026-03-06",
      trainNumber: "9999",
      origin: "Rīga",
      destination: "Valmiera",
      departureTime: "10:01"
    }
  ];
  var matcher = externalFeed.createLocalTrainMatcher(localTrains);
  var exactExternal = {
    serviceDate: "2026-03-06",
    trainNumber: "6101",
    origin: "Rīga",
    destination: "Daugavpils",
    departureTime: "09:10"
  };
  var sameNumberExternal = {
    serviceDate: "2026-03-06",
    trainNumber: "804",
    origin: "Rīga",
    destination: "Jelgava",
    departureTime: "08:46"
  };
  var routeWindowExternal = {
    serviceDate: "2026-03-06",
    trainNumber: "6102",
    origin: "Riga",
    destination: "Valmiera",
    departureTime: "10:00"
  };

  assert.deepEqual(matcher(exactExternal), externalFeed.matchLocalTrain(exactExternal, localTrains));
  assert.deepEqual(matcher(sameNumberExternal), externalFeed.matchLocalTrain(sameNumberExternal, localTrains));
  assert.deepEqual(matcher(routeWindowExternal), externalFeed.matchLocalTrain(routeWindowExternal, localTrains));
  assert.deepEqual(
    externalFeed.matchLocalTrain(localTrains, exactExternal),
    externalFeed.matchLocalTrain(exactExternal, localTrains)
  );
});

test("stableExternalTrainIdentity prefers route ids when available", function () {
  var identity = externalFeed.stableExternalTrainIdentity({
    routeId: "748111",
    serviceDate: "2026-03-06",
    trainNumber: "873",
    origin: "Valga",
    destination: "Rīga",
    departureTime: "13:15"
  });

  assert.equal(identity, "route:748111");
});

test("stableExternalTrainIdentity falls back to service date and train number", function () {
  var identity = externalFeed.stableExternalTrainIdentity({
    serviceDate: "2026-03-06",
    trainNumber: "873",
    origin: "Valga",
    destination: "Rīga",
    departureTime: "13:15"
  });

  assert.equal(identity, "train:2026-03-06:873");
});

test("stableExternalTrainIdentity falls back to route and departure window", function () {
  var identity = externalFeed.stableExternalTrainIdentity({
    serviceDate: "2026-03-06",
    origin: "Valga",
    destination: "Rīga",
    departureTime: "2026-03-06T13:15:00Z"
  });

  assert.equal(identity, "route-time:2026-03-06:valga:riga:795");
});

test("planMarkerReconcile retains an open popup when a stable marker key survives updates", function () {
  var markerKey = "live-train:train:2026-03-06:873";
  var plan = externalFeed.planMarkerReconcile(
    [{ markerKey: markerKey, html: "<span>old</span>" }],
    [{ markerKey: markerKey, html: "<span>new</span>" }],
    markerKey
  );

  assert.equal(plan.retainPopupKey, markerKey);
  assert.equal(plan.clearPopup, false);
  assert.deepEqual(plan.addKeys, []);
  assert.deepEqual(plan.removeKeys, []);
  assert.deepEqual(plan.updateKeys, [markerKey]);
});

test("sameTrainStopsPayload treats cloned payloads with different schedule envelopes as equal", function () {
  var left = {
    train: { id: "2026-03-09-train-6326", fromStation: "Dubulti", toStation: "Rīga" },
    trainCard: {
      train: { id: "2026-03-09-train-6326" },
      status: { state: "NO_REPORTS", lastReportAt: "" },
      riders: 0
    },
    stops: [
      {
        stationId: "maj",
        stationName: "Majori",
        latitude: 56.97,
        longitude: 23.78,
        arrivalAt: "2026-03-09T15:55:00+02:00",
        departureAt: "2026-03-09T15:55:00+02:00"
      }
    ],
    stationSightings: [],
    schedule: { effectiveServiceDate: "2026-03-09", fallbackActive: false }
  };
  var right = {
    train: { id: "2026-03-09-train-6326", fromStation: "Dubulti", toStation: "Rīga" },
    trainCard: {
      train: { id: "2026-03-09-train-6326" },
      status: { state: "NO_REPORTS", lastReportAt: "" },
      riders: 0
    },
    stops: [
      {
        stationId: "maj",
        stationName: "Majori",
        latitude: 56.97,
        longitude: 23.78,
        arrivalAt: "2026-03-09T15:55:00+02:00",
        departureAt: "2026-03-09T15:55:00+02:00"
      }
    ],
    stationSightings: [],
    schedule: { effectiveServiceDate: "2026-03-10", fallbackActive: true },
    generatedAt: "2026-03-09T14:00:00Z"
  };

  assert.equal(externalFeed.sameTrainStopsPayload(left, right), true);
});

test("sameExternalFeedState ignores lastMessageAt-only updates", function () {
  var left = {
    enabled: true,
    connectionState: "live",
    routes: [{ routeId: "6326", trainNumber: "6326" }],
    liveTrains: [{ routeId: "6326", position: { lat: 56.97, lng: 23.78 }, updatedAt: "1710000000000" }],
    activeStops: [{ stationId: "maj", title: "Majori" }],
    lastGraphAt: "2026-03-09T14:00:00Z",
    lastMessageAt: "2026-03-09T14:00:00Z",
    error: ""
  };
  var right = {
    enabled: true,
    connectionState: "live",
    routes: [{ routeId: "6326", trainNumber: "6326" }],
    liveTrains: [{ routeId: "6326", position: { lat: 56.97, lng: 23.78 }, updatedAt: "1710000000000" }],
    activeStops: [{ stationId: "maj", title: "Majori" }],
    lastGraphAt: "2026-03-09T14:00:01Z",
    lastMessageAt: "2026-03-09T14:00:05Z",
    error: ""
  };

  assert.equal(externalFeed.sameExternalFeedState(left, right), true);
});

test("mapConfigSignature changes when popup content changes", function () {
  var left = {
    modelKey: "old",
    viewKey: "network:public",
    bounds: [[56.97, 23.78]],
    polyline: [],
    baseMarkers: [
      {
        markerKey: "network-station:majori",
        kind: "html",
        latLng: [56.97, 23.78],
        html: "<div>marker</div>",
        popupHTML: "<div>Majori</div>"
      }
    ],
    sightingMarkers: [],
    trainMarkers: []
  };
  var right = {
    modelKey: "new",
    viewKey: "network:public",
    bounds: [[56.97, 23.78], [56.98, 23.79]],
    polyline: [],
    baseMarkers: [
      {
        markerKey: "network-station:majori",
        kind: "html",
        latLng: [56.97, 23.78],
        html: "<div>marker</div>",
        popupHTML: "<div>Majori • 1 sighting</div>"
      }
    ],
    sightingMarkers: [],
    trainMarkers: []
  };

  assert.notEqual(externalFeed.mapConfigSignature(left), externalFeed.mapConfigSignature(right));
});

test("planMarkerReconcile returns no mutations for an unchanged selected popup", function () {
  var previous = [
    {
      markerKey: "train-stop:6326:majori",
      kind: "html",
      latLng: [56.97, 23.78],
      html: "<div>marker</div>",
      popupHTML: "<div>Majori</div>"
    }
  ];
  var next = [
    {
      markerKey: "train-stop:6326:majori",
      kind: "html",
      latLng: [56.97, 23.78],
      html: "<div>marker</div>",
      popupHTML: "<div>Majori</div>"
    }
  ];

  var plan = externalFeed.planMarkerReconcile(previous, next, "train-stop:6326:majori");
  assert.deepEqual(plan.addKeys, []);
  assert.deepEqual(plan.updateKeys, []);
  assert.deepEqual(plan.removeKeys, []);
  assert.equal(plan.clearPopup, false);
  assert.equal(plan.retainPopupKey, "train-stop:6326:majori");
});

test("planMarkerReconcile updates an in-place selected popup and clears it when removed", function () {
  var previous = [
    {
      markerKey: "train-stop:6326:majori",
      kind: "html",
      latLng: [56.97, 23.78],
      html: "<div>marker</div>",
      popupHTML: "<div>Majori</div>"
    }
  ];
  var updated = [
    {
      markerKey: "train-stop:6326:majori",
      kind: "html",
      latLng: [56.97, 23.78],
      html: "<div>marker</div>",
      popupHTML: "<div>Majori • updated</div>"
    }
  ];

  var updatePlan = externalFeed.planMarkerReconcile(previous, updated, "train-stop:6326:majori");
  assert.deepEqual(updatePlan.addKeys, []);
  assert.deepEqual(updatePlan.updateKeys, ["train-stop:6326:majori"]);
  assert.deepEqual(updatePlan.removeKeys, []);
  assert.equal(updatePlan.clearPopup, false);
  assert.equal(updatePlan.retainPopupKey, "train-stop:6326:majori");

  var removePlan = externalFeed.planMarkerReconcile(updated, [], "train-stop:6326:majori");
  assert.deepEqual(removePlan.addKeys, []);
  assert.deepEqual(removePlan.updateKeys, []);
  assert.deepEqual(removePlan.removeKeys, ["train-stop:6326:majori"]);
  assert.equal(removePlan.clearPopup, true);
  assert.equal(removePlan.retainPopupKey, "");
});

test("createExternalTrainMapClient starts websocket even when the direct graph fetch fails", async function () {
  function FakeSocket(url) {
    this.url = url;
    this.readyState = 0;
    this.listeners = {};
    sockets.push(this);
  }
  FakeSocket.prototype.addEventListener = function (type, handler) {
    this.listeners[type] = this.listeners[type] || [];
    this.listeners[type].push(handler);
  };
  FakeSocket.prototype.close = function () {
    this.readyState = 3;
  };
  FakeSocket.prototype.emit = function (type, payload) {
    (this.listeners[type] || []).forEach(function (handler) {
      handler(payload || {});
    });
  };

  var sockets = [];
  var requestedURL = "";
  var client = externalFeed.createExternalTrainMapClient({
    enabled: true,
    baseURL: "https://trainmap.vivi.lv",
    wsURL: "wss://trainmap.pv.lv/ws",
    fetchImpl: function (url) {
      requestedURL = url;
      return Promise.reject(new Error("blocked by cors"));
    },
    WebSocketCtor: FakeSocket,
  });

  await client.start();

  assert.equal(requestedURL, "https://trainmap.vivi.lv/api/trainGraph");
  assert.equal(sockets.length, 1);
  assert.equal(client.snapshot().connectionState, "connecting");
  assert.equal(client.snapshot().graphState, "unavailable");

  sockets[0].emit("open");

  assert.equal(client.snapshot().connectionState, "live");
  assert.equal(client.snapshot().graphState, "unavailable");
});

test("createExternalTrainMapClient uses direct graph data as optional enrichment", async function () {
  function FakeSocket() {
    this.readyState = 0;
    this.listeners = {};
  }
  FakeSocket.prototype.addEventListener = function (type, handler) {
    this.listeners[type] = this.listeners[type] || [];
    this.listeners[type].push(handler);
  };
  FakeSocket.prototype.close = function () {
    this.readyState = 3;
  };

  var client = externalFeed.createExternalTrainMapClient({
    enabled: true,
    baseURL: "https://trainmap.vivi.lv",
    wsURL: "wss://trainmap.pv.lv/ws",
    fetchImpl: function () {
      return Promise.resolve({
        ok: true,
        json: function () {
          return Promise.resolve({
            data: [
              {
                id: "route-6326",
                train: "6326",
                schDate: "2026-03-10",
                departure: "18:55",
                stops: [
                  { id: "riga", title: "Rīga", departure: "18:55", coords: [56.946285, 24.105078] },
                  { id: "majori", title: "Majori", departure: "19:23", coords: [56.970354, 23.770267] }
                ]
              }
            ]
          });
        }
      });
    },
    WebSocketCtor: FakeSocket,
  });

  await client.start();

  var snapshot = client.snapshot();
  assert.equal(snapshot.graphState, "ready");
  assert.equal(snapshot.routes.length, 1);
  assert.equal(snapshot.routes[0].routeId, "route-6326");
  assert.equal(snapshot.routes[0].polyline.length, 2);
});

test("createExternalTrainMapClient keeps websocket-only train geometry usable without graph routes", async function () {
  function FakeSocket() {
    this.readyState = 0;
    this.listeners = {};
    sockets.push(this);
  }
  FakeSocket.prototype.addEventListener = function (type, handler) {
    this.listeners[type] = this.listeners[type] || [];
    this.listeners[type].push(handler);
  };
  FakeSocket.prototype.close = function () {
    this.readyState = 3;
  };
  FakeSocket.prototype.emit = function (type, payload) {
    (this.listeners[type] || []).forEach(function (handler) {
      handler(payload || {});
    });
  };

  var sockets = [];
  var client = externalFeed.createExternalTrainMapClient({
    enabled: true,
    baseURL: "https://trainmap.vivi.lv",
    wsURL: "wss://trainmap.pv.lv/ws",
    fetchImpl: function () {
      return Promise.reject(new Error("blocked by cors"));
    },
    WebSocketCtor: FakeSocket,
  });

  await client.start();
  sockets[0].emit("open");
  sockets[0].emit("message", {
    data: JSON.stringify({
      type: "back-end",
      name: "Riga - Majori",
      stopCoordArray: [
        [56.946285, 24.105078],
        [56.970354, 23.770267]
      ],
      returnValue: {
        id: "748081",
        train: "6326",
        animatedCoord: [56.95, 24.11],
        departureTime: "2026-03-10T18:55:00Z",
        arrivalTime: "2026-03-10T19:23:00Z",
        currentStopIndex: 0,
        nextStopObj: {
          id: "majori",
          title: "Majori",
          departure: "2026-03-10T19:23:00Z",
          coords: [56.970354, 23.770267]
        },
        stopObjArray: [
          { id: "riga", title: "Rīga", departure: "2026-03-10T18:55:00Z", coords: [56.946285, 24.105078] },
          { id: "majori", title: "Majori", departure: "2026-03-10T19:23:00Z", coords: [56.970354, 23.770267] }
        ]
      }
    })
  });
  sockets[0].emit("message", {
    data: JSON.stringify({
      type: "active-stops",
      data: [
        {
          id: "majori",
          title: "Majori",
          coords: [56.970354, 23.770267],
          train: "6326",
          routes_id: "748081",
          departure: "2026-03-10T19:23:00Z"
        }
      ]
    })
  });

  var snapshot = client.snapshot();
  assert.equal(snapshot.connectionState, "live");
  assert.equal(snapshot.routes.length, 0);
  assert.equal(snapshot.liveTrains.length, 1);
  assert.equal(snapshot.liveTrains[0].stops.length, 2);
  assert.equal(snapshot.liveTrains[0].nextStop.title, "Majori");
  assert.equal(snapshot.liveTrains[0].polyline.length, 2);
  assert.equal(snapshot.activeStops.length, 1);
});
