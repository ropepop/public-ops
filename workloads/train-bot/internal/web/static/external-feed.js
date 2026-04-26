(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory();
    return;
  }
  root.TrainExternalFeed = factory();
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var GRAPH_PATH = "/api/trainGraph";
  var LIVE_POSITION_MEMORY_LIMIT = 9;
  var LIVE_POSITION_FRESHNESS_MS = 6 * 60 * 1000;

  function stringValue(value) {
    if (value === null || value === undefined) {
      return "";
    }
    return String(value).trim();
  }

  function toNumber(value) {
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    if (typeof value === "string" && value.trim() !== "") {
      var parsed = Number(value);
      if (Number.isFinite(parsed)) {
        return parsed;
      }
    }
    return null;
  }

  function toInteger(value) {
    var parsed = toNumber(value);
    if (parsed === null) {
      return null;
    }
    return Math.round(parsed);
  }

  function normalizePoint(value) {
    if (!Array.isArray(value) || value.length < 2) {
      return null;
    }
    var lat = toNumber(value[0]);
    var lng = toNumber(value[1]);
    if (lat === null || lng === null) {
      return null;
    }
    return { lat: lat, lng: lng };
  }

  function normalizeStationKey(value) {
    return stringValue(value)
      .normalize("NFKD")
      .replace(/[\u0300-\u036f]/g, "")
      .replace(/\*/g, "")
      .replace(/[^a-zA-Z0-9]+/g, " ")
      .trim()
      .toLowerCase();
  }

  function extractServiceDate(value) {
    var text = stringValue(value);
    var match = text.match(/^(\d{4}-\d{2}-\d{2})/);
    return match ? match[1] : "";
  }

  function clockMinutes(value) {
    var text = stringValue(value);
    var match = text.match(/(?:\d{4}-\d{2}-\d{2}[ T])?(\d{2}):(\d{2})/);
    if (!match) {
      return null;
    }
    return (Number(match[1]) * 60) + Number(match[2]);
  }

  function pointList(points) {
    return (Array.isArray(points) ? points : []).map(normalizePoint).filter(Boolean);
  }

  function normalizeStop(stop, index) {
    return {
      stationId: stringValue(stop && (stop.id || stop.pvID || stop.gps_id)),
      title: stringValue(stop && (stop.title || stop.lockedTitle || stop.cargoTitle)),
      titleKey: normalizeStationKey(stop && (stop.title || stop.lockedTitle || stop.cargoTitle)),
      position: normalizePoint(stop && stop.coords),
      departureTime: stringValue(stop && stop.departure),
      departureMinutes: clockMinutes(stop && stop.departure),
      routeId: stringValue(stop && stop.routes_id),
      gpsId: stringValue(stop && stop.gps_id),
      stopIndex: toInteger(stop && stop.i) !== null ? toInteger(stop && stop.i) : index,
    };
  }

  function normalizeTrainGraphRoute(route) {
    var stops = Array.isArray(route && route.stops)
      ? route.stops.map(normalizeStop).filter(function (stop) {
        return Boolean(stop.title) && Boolean(stop.position);
      })
      : [];
    var origin = stops.length ? stops[0] : null;
    var destination = stops.length ? stops[stops.length - 1] : null;
    return {
      routeId: stringValue(route && route.id),
      trainNumber: stringValue(route && route.train),
      serviceDate: stringValue(route && route.schDate) || extractServiceDate(route && route.departure),
      name: stringValue(route && route.name),
      departureTime: stringValue(route && route.departure),
      arrivalTime: stringValue(route && route.arrival),
      departureMinutes: clockMinutes(route && route.departure),
      arrivalMinutes: clockMinutes(route && route.arrival),
      direction: stringValue(route && route.direction),
      railLine: stringValue(route && route.railLine),
      fuelType: stringValue(route && route.fuelType),
      origin: origin ? origin.title : "",
      destination: destination ? destination.title : "",
      originKey: origin ? origin.titleKey : "",
      destinationKey: destination ? destination.titleKey : "",
      stops: stops,
      polyline: stops.map(function (stop) {
        return stop.position;
      }),
    };
  }

  function normalizeTrainGraphPayload(payload) {
    var routes = Array.isArray(payload && payload.data)
      ? payload.data
      : Array.isArray(payload)
        ? payload
        : [];
    var normalized = routes.map(normalizeTrainGraphRoute).filter(function (route) {
      return route.routeId || route.trainNumber;
    });
    return {
      routes: normalized,
      routeCount: normalized.length,
    };
  }

  function normalizeBackEndEntry(entry) {
    var live = entry && entry.returnValue ? entry.returnValue : entry;
    var stops = Array.isArray(live && live.stopObjArray)
      ? live.stopObjArray.map(normalizeStop).filter(function (stop) {
        return Boolean(stop.title) && Boolean(stop.position);
      })
      : [];
    var currentStopIndex = toInteger(live && live.currentStopIndex);
    var currentStop = currentStopIndex !== null && currentStopIndex >= 0 && currentStopIndex < stops.length
      ? stops[currentStopIndex]
      : null;
    var nextStop = live && live.nextStopObj ? normalizeStop(live.nextStopObj, currentStopIndex !== null ? currentStopIndex + 1 : stops.length) : null;
    var animatedPosition = normalizePoint(live && live.animatedCoord);
    var rawPosition = normalizePoint(live && live.position);
    var observedSource = live && live.isGpsActive && rawPosition
      ? "live"
      : animatedPosition
        ? "projection"
        : rawPosition
          ? live && live.isGpsActive
            ? "live"
            : "projection"
          : "";
    return {
      routeId: stringValue(live && live.id),
      trainNumber: stringValue(live && live.train),
      name: stringValue(entry && entry.name),
      serviceDate: extractServiceDate(live && live.departureTime),
      departureTime: stringValue(live && live.departureTime),
      arrivalTime: stringValue(live && live.arrivalTime),
      departureMinutes: clockMinutes(live && live.departureTime),
      arrivalMinutes: clockMinutes(live && live.arrivalTime),
      position: observedSource === "live"
        ? rawPosition
        : observedSource === "projection"
          ? (animatedPosition || rawPosition)
          : null,
      animatedPosition: animatedPosition,
      rawPosition: rawPosition,
      displaySource: observedSource,
      displayUpdatedAt: stringValue(live && live.updaterTimeStamp),
      currentStopIndex: currentStopIndex,
      currentStop: currentStop,
      nextStop: nextStop && nextStop.title ? nextStop : null,
      updatedAt: stringValue(live && live.updaterTimeStamp),
      stopped: Boolean(live && live.stopped),
      finished: Boolean(live && live.finished),
      isGpsActive: Boolean(live && live.isGpsActive),
      nextTime: live && live.nextTime,
      waitingTime: live && live.waitingTime,
      arrivingTime: live && live.arrivingTime,
      stops: stops,
      polyline: Array.isArray(entry && entry.stopCoordArray)
        ? pointList(entry.stopCoordArray)
        : stops.map(function (stop) {
          return stop.position;
        }),
      origin: stops.length ? stops[0].title : "",
      destination: stops.length ? stops[stops.length - 1].title : "",
      originKey: stops.length ? stops[0].titleKey : "",
      destinationKey: stops.length ? stops[stops.length - 1].titleKey : "",
    };
  }

  function normalizeBackEndFrame(payload) {
    if (payload && Array.isArray(payload.data)) {
      return payload.data.map(normalizeBackEndEntry).filter(function (item) {
        return item.routeId || item.trainNumber || item.position;
      });
    }
    if (Array.isArray(payload)) {
      return payload.map(normalizeBackEndEntry).filter(function (item) {
        return item.routeId || item.trainNumber || item.position;
      });
    }
    return normalizeBackEndEntry(payload);
  }

  function clonePoint(point) {
    if (!point || typeof point !== "object") {
      return null;
    }
    return {
      lat: point.lat,
      lng: point.lng,
    };
  }

  function timestampMs(value) {
    var numeric = toNumber(value);
    if (numeric !== null) {
      return numeric;
    }
    var text = stringValue(value);
    if (!text) {
      return null;
    }
    var parsed = Date.parse(text);
    return Number.isFinite(parsed) ? parsed : null;
  }

  function isoTimestamp(ms) {
    return typeof ms === "number" && Number.isFinite(ms)
      ? new Date(ms).toISOString()
      : "";
  }

  function resolvedSampleTime(value, fallbackMs) {
    var parsedMs = timestampMs(value);
    var sampleMs = parsedMs !== null ? parsedMs : fallbackMs;
    return {
      ms: sampleMs,
      iso: isoTimestamp(sampleMs),
    };
  }

  function observedLiveTrainSource(train) {
    var hasProjection = Boolean(train && train.animatedPosition);
    var hasLive = Boolean(train && train.rawPosition);
    if (train && train.isGpsActive && hasLive) {
      return "live";
    }
    if (hasProjection) {
      return "projection";
    }
    if (hasLive) {
      return train && train.isGpsActive ? "live" : "projection";
    }
    return "";
  }

  function ensureLiveTrainMemory(store, train) {
    var key = stableExternalTrainIdentity(train);
    if (!key) {
      return null;
    }
    var memory = store.get(key);
    if (memory) {
      return memory;
    }
    memory = {
      key: key,
      history: [],
      lastProjectionPosition: null,
      lastProjectionUpdatedAt: "",
      lastProjectionUpdatedAtMs: null,
      lastLivePosition: null,
      lastLiveUpdatedAt: "",
      lastLiveUpdatedAtMs: null,
      lastDisplaySource: "",
      lastTrain: null,
    };
    store.set(key, memory);
    return memory;
  }

  function liveTrainSourceCounts(memory) {
    return (memory && Array.isArray(memory.history) ? memory.history : []).reduce(function (counts, sample) {
      if (sample && sample.observedSource === "projection") {
        counts.projection += 1;
      }
      if (sample && sample.observedSource === "live") {
        counts.live += 1;
      }
      return counts;
    }, { projection: 0, live: 0 });
  }

  function rememberedSourcePosition(memory, source) {
    if (source === "projection") {
      return clonePoint(memory && memory.lastProjectionPosition);
    }
    if (source === "live") {
      return clonePoint(memory && memory.lastLivePosition);
    }
    return null;
  }

  function rememberedSourceUpdatedAt(memory, source) {
    if (source === "projection") {
      return {
        value: stringValue(memory && memory.lastProjectionUpdatedAt),
        ms: memory && memory.lastProjectionUpdatedAtMs !== undefined
          ? memory.lastProjectionUpdatedAtMs
          : null,
      };
    }
    if (source === "live") {
      return {
        value: stringValue(memory && memory.lastLiveUpdatedAt),
        ms: memory && memory.lastLiveUpdatedAtMs !== undefined
          ? memory.lastLiveUpdatedAtMs
          : null,
      };
    }
    return { value: "", ms: null };
  }

  function chooseRememberedDisplaySource(memory, preferredSource) {
    if (!memory) {
      return "";
    }
    var counts = liveTrainSourceCounts(memory);
    if (counts.projection > counts.live && memory.lastProjectionPosition) {
      return "projection";
    }
    if (counts.live > counts.projection && memory.lastLivePosition) {
      return "live";
    }
    if (memory.lastDisplaySource === "projection" && memory.lastProjectionPosition) {
      return "projection";
    }
    if (memory.lastDisplaySource === "live" && memory.lastLivePosition) {
      return "live";
    }
    if (preferredSource === "live" && memory.lastLivePosition) {
      return "live";
    }
    if (preferredSource === "projection" && memory.lastProjectionPosition) {
      return "projection";
    }
    if (memory.lastLivePosition) {
      return "live";
    }
    if (memory.lastProjectionPosition) {
      return "projection";
    }
    return "";
  }

  function buildRememberedTrainSnapshot(memory, train) {
    var displaySource = chooseRememberedDisplaySource(
      memory,
      observedLiveTrainSource(train) || stringValue(train && train.displaySource)
    );
    var position = rememberedSourcePosition(memory, displaySource);
    if (!position) {
      return null;
    }
    var displayUpdated = rememberedSourceUpdatedAt(memory, displaySource);
    var base = Object.assign({}, memory && memory.lastTrain ? memory.lastTrain : {}, train || {});
    base.position = position;
    base.displaySource = displaySource;
    base.displayUpdatedAt = displayUpdated.value;
    return base;
  }

  function isRememberedTrainFresh(memory, nowMs) {
    var displaySource = chooseRememberedDisplaySource(memory, "");
    var displayUpdated = rememberedSourceUpdatedAt(memory, displaySource);
    if (displayUpdated.ms === null) {
      return false;
    }
    return Math.abs(nowMs - displayUpdated.ms) <= LIVE_POSITION_FRESHNESS_MS;
  }

  function buildLiveDisplaySnapshot(train, nowMs) {
    var sampleTime = resolvedSampleTime(train && train.updatedAt, nowMs);
    var observedSource = observedLiveTrainSource(train) || stringValue(train && train.displaySource);
    var projectionPosition = clonePoint(train && train.animatedPosition);
    var livePosition = clonePoint(train && train.rawPosition);
    var rememberedProjectionPosition = projectionPosition
      || (observedSource === "projection" && livePosition ? clonePoint(livePosition) : null);
    var memory = {
      history: observedSource
        ? [
          {
            observedSource: observedSource,
            shownSource: "",
            recordedAt: sampleTime.iso,
          }
        ]
        : [],
      lastProjectionPosition: rememberedProjectionPosition,
      lastProjectionUpdatedAt: rememberedProjectionPosition ? sampleTime.iso : "",
      lastProjectionUpdatedAtMs: rememberedProjectionPosition ? sampleTime.ms : null,
      lastLivePosition: livePosition,
      lastLiveUpdatedAt: livePosition ? sampleTime.iso : "",
      lastLiveUpdatedAtMs: livePosition ? sampleTime.ms : null,
      lastDisplaySource: observedSource || stringValue(train && train.displaySource),
      lastTrain: Object.assign({}, train || {}),
    };
    var snapshot = buildRememberedTrainSnapshot(memory, train);
    if (memory.history.length && snapshot && snapshot.displaySource) {
      memory.history[0].shownSource = snapshot.displaySource;
    }
    return snapshot;
  }

  function updateRememberedLiveTrain(memory, train, nowMs) {
    var sampleTime = resolvedSampleTime(train && train.updatedAt, nowMs);
    var observedSource = observedLiveTrainSource(train) || stringValue(train && train.displaySource);
    var projectionPosition = clonePoint(train && train.animatedPosition);
    var livePosition = clonePoint(train && train.rawPosition);
    var rememberedProjectionPosition = projectionPosition
      || (observedSource === "projection" && livePosition ? clonePoint(livePosition) : null);
    memory.lastTrain = Object.assign({}, memory.lastTrain || {}, train || {});
    if (rememberedProjectionPosition) {
      memory.lastProjectionPosition = rememberedProjectionPosition;
      memory.lastProjectionUpdatedAt = sampleTime.iso;
      memory.lastProjectionUpdatedAtMs = sampleTime.ms;
    }
    if (livePosition) {
      memory.lastLivePosition = livePosition;
      memory.lastLiveUpdatedAt = sampleTime.iso;
      memory.lastLiveUpdatedAtMs = sampleTime.ms;
    }
    if (observedSource) {
      memory.history.push({
        observedSource: observedSource,
        shownSource: "",
        recordedAt: sampleTime.iso,
      });
      while (memory.history.length > LIVE_POSITION_MEMORY_LIMIT) {
        memory.history.shift();
      }
    }
    var snapshot = buildRememberedTrainSnapshot(memory, train);
    memory.lastDisplaySource = snapshot && snapshot.displaySource
      ? snapshot.displaySource
      : memory.lastDisplaySource;
    if (observedSource && memory.history.length) {
      memory.history[memory.history.length - 1].shownSource = memory.lastDisplaySource;
    }
    return snapshot;
  }

  function stabilizeLiveTrainPositions(frame, memoryByTrain, nowMs) {
    var store = memoryByTrain instanceof Map ? memoryByTrain : new Map();
    var sampleMs = typeof nowMs === "number" && Number.isFinite(nowMs) ? nowMs : Date.now();
    var incoming = Array.isArray(frame)
      ? frame
      : frame
        ? [frame]
        : [];
    var seenKeys = new Set();
    var nextTrains = [];

    incoming.forEach(function (train) {
      if (!train) {
        return;
      }
      var memory = ensureLiveTrainMemory(store, train);
      if (!memory) {
        var transientSnapshot = buildLiveDisplaySnapshot(train, sampleMs);
        if (transientSnapshot && transientSnapshot.position) {
          nextTrains.push(transientSnapshot);
        }
        return;
      }
      seenKeys.add(memory.key);
      var snapshot = updateRememberedLiveTrain(memory, train, sampleMs);
      if (snapshot && snapshot.position) {
        nextTrains.push(snapshot);
      }
    });

    Array.from(store.keys()).forEach(function (key) {
      if (seenKeys.has(key)) {
        return;
      }
      var memory = store.get(key);
      if (!memory || !isRememberedTrainFresh(memory, sampleMs)) {
        store.delete(key);
        return;
      }
      var snapshot = buildRememberedTrainSnapshot(memory, null);
      if (snapshot && snapshot.position) {
        nextTrains.push(snapshot);
      }
    });

    return nextTrains;
  }

  function normalizeActiveStopEntry(entry) {
    return {
      stationId: stringValue(entry && (entry.id || entry.pvID || entry.gps_id)),
      title: stringValue(entry && (entry.title || entry.lockedTitle || entry.cargoTitle)),
      titleKey: normalizeStationKey(entry && (entry.title || entry.lockedTitle || entry.cargoTitle)),
      position: normalizePoint(entry && entry.coords),
      routeId: stringValue(entry && entry.routes_id),
      trainNumber: stringValue(entry && entry.train),
      currentStopIndex: toInteger(entry && entry.currentStopIndex),
      stopIndex: toInteger(entry && entry.stopIndex),
      animatedPosition: normalizePoint(entry && entry.animatedCoord),
      directionList: Array.isArray(entry && entry.directionList) ? entry.directionList.slice() : [],
      departureTime: stringValue(entry && entry.departure),
      serviceDate: extractServiceDate(entry && entry.departure),
      hasTrain: Boolean(stringValue(entry && entry.train)),
    };
  }

  function normalizeActiveStopsFrame(payload) {
    if (payload && Array.isArray(payload.data)) {
      return payload.data.map(normalizeActiveStopEntry).filter(function (item) {
        return item.title && item.position;
      });
    }
    if (Array.isArray(payload)) {
      return payload.map(normalizeActiveStopEntry).filter(function (item) {
        return item.title && item.position;
      });
    }
    return normalizeActiveStopEntry(payload);
  }

  function roundedNumber(value) {
    var parsed = toNumber(value);
    if (parsed === null) {
      return null;
    }
    return Math.round(parsed * 1000000) / 1000000;
  }

  function stableMaterialValue(value, ignoredFields) {
    if (value === undefined || value === null) {
      return null;
    }
    if (typeof value === "number") {
      return Number.isFinite(value) ? roundedNumber(value) : null;
    }
    if (typeof value === "string" || typeof value === "boolean") {
      return value;
    }
    if (Array.isArray(value)) {
      return value.map(function (item) {
        return stableMaterialValue(item, ignoredFields);
      });
    }
    if (typeof value === "object") {
      var out = {};
      Object.keys(value).sort().forEach(function (key) {
        if (ignoredFields && ignoredFields[key]) {
          return;
        }
        if (typeof value[key] === "function") {
          return;
        }
        out[key] = stableMaterialValue(value[key], ignoredFields);
      });
      return out;
    }
    return value;
  }

  function stableSerialize(value) {
    return JSON.stringify(value);
  }

  function materialSignature(value, ignoredFields) {
    return stableSerialize(stableMaterialValue(value, ignoredFields || {}));
  }

  function sameMaterialValue(left, right, ignoredFields) {
    return materialSignature(left, ignoredFields) === materialSignature(right, ignoredFields);
  }

  function sameTrainStopsPayload(left, right) {
    return sameMaterialValue(left, right, { schedule: true, generatedAt: true });
  }

  function sameNetworkMapPayload(left, right) {
    return sameMaterialValue(left, right, { schedule: true, generatedAt: true });
  }

  function samePublicDashboard(left, right) {
    return sameMaterialValue(left, right, { schedule: true, generatedAt: true });
  }

  function samePublicStationDepartures(left, right) {
    return sameMaterialValue(left, right, { schedule: true, generatedAt: true });
  }

  function sameExternalFeedState(left, right) {
    return sameMaterialValue(left, right, { lastMessageAt: true, lastGraphAt: true });
  }

  function mapConfigSignature(config) {
    return materialSignature(config, { modelKey: true, bounds: true });
  }

  function mapConfigMarkerSignature(marker) {
    return materialSignature(marker, {});
  }

  function planMarkerReconcile(previousItems, nextItems, openPopupKey) {
    var previousIndex = new Map();
    var nextIndex = new Map();
    var addKeys = [];
    var updateKeys = [];
    var removeKeys = [];
    var previousList = Array.isArray(previousItems) ? previousItems : [];
    var nextList = Array.isArray(nextItems) ? nextItems : [];

    previousList.forEach(function (item) {
      var key = stringValue(item && item.markerKey);
      if (!key) {
        return;
      }
      previousIndex.set(key, mapConfigMarkerSignature(item));
    });
    nextList.forEach(function (item) {
      var key = stringValue(item && item.markerKey);
      if (!key) {
        return;
      }
      var nextSignature = mapConfigMarkerSignature(item);
      nextIndex.set(key, nextSignature);
      if (!previousIndex.has(key)) {
        addKeys.push(key);
        return;
      }
      if (previousIndex.get(key) !== nextSignature) {
        updateKeys.push(key);
      }
    });
    previousIndex.forEach(function (_signature, key) {
      if (!nextIndex.has(key)) {
        removeKeys.push(key);
      }
    });

    var retainPopup = Boolean(openPopupKey) && nextIndex.has(openPopupKey);
    return {
      addKeys: addKeys,
      updateKeys: updateKeys,
      removeKeys: removeKeys,
      retainPopupKey: retainPopup ? openPopupKey : "",
      clearPopup: Boolean(openPopupKey) && !retainPopup,
      hasChanges: addKeys.length > 0 || updateKeys.length > 0 || removeKeys.length > 0,
    };
  }

  function extractLocalTrain(raw) {
    var train = raw && raw.train
      ? raw.train
      : raw && raw.trainCard && raw.trainCard.train
        ? raw.trainCard.train
        : raw;
    if (!train) {
      return null;
    }
    var id = stringValue(train.id || raw.localTrainId);
    var exactMatch = id.match(/(\d{3,5})$/);
    return {
      match: raw,
      localTrainId: id,
      serviceDate: stringValue(train.serviceDate || raw.serviceDate) || extractServiceDate(id),
      trainNumber: stringValue(train.trainNumber || raw.trainNumber || (exactMatch ? exactMatch[1] : "")),
      originKey: normalizeStationKey(train.fromStation || raw.fromStation || raw.origin || raw.originName),
      destinationKey: normalizeStationKey(train.toStation || raw.toStation || raw.destination || raw.destinationName),
      departureMinutes: clockMinutes(train.departureAt || train.departure || raw.departureTime || raw.departureAt),
    };
  }

  function extractExternalTrain(raw) {
    if (!raw) {
      return null;
    }
    return {
      routeId: stringValue(raw.routeId || raw.id),
      serviceDate: stringValue(raw.serviceDate) || extractServiceDate(raw.departureTime || raw.departureAt),
      trainNumber: stringValue(raw.trainNumber || raw.train),
      originKey: normalizeStationKey(raw.origin || raw.originName || (raw.stops && raw.stops[0] && raw.stops[0].title)),
      destinationKey: normalizeStationKey(raw.destination || raw.destinationName || (raw.stops && raw.stops[raw.stops.length - 1] && raw.stops[raw.stops.length - 1].title)),
      departureMinutes: clockMinutes(raw.departureTime || raw.departureAt || raw.departure),
    };
  }

  function stableExternalTrainIdentity(raw) {
    var external = extractExternalTrain(raw);
    if (!external) {
      return "";
    }
    if (external.routeId) {
      return "route:" + external.routeId;
    }
    if (external.serviceDate && external.trainNumber) {
      return "train:" + external.serviceDate + ":" + external.trainNumber;
    }
    if (
      external.serviceDate &&
      external.originKey &&
      external.destinationKey &&
      external.departureMinutes !== null
    ) {
      return "route-time:" + external.serviceDate + ":" + external.originKey + ":" + external.destinationKey + ":" + String(external.departureMinutes);
    }
    return "";
  }

  function bestLocalTrainMatch(candidates, targetMinutes, maxDeltaMinutes) {
    var compareMinutes = targetMinutes === null ? 0 : targetMinutes;
    var best = null;
    var bestDelta = Infinity;
    (Array.isArray(candidates) ? candidates : []).forEach(function (item) {
      if (!item) {
        return;
      }
      if (maxDeltaMinutes !== null && item.departureMinutes === null) {
        return;
      }
      var itemMinutes = item.departureMinutes === null ? 0 : item.departureMinutes;
      var delta = Math.abs(itemMinutes - compareMinutes);
      if (maxDeltaMinutes !== null && delta > maxDeltaMinutes) {
        return;
      }
      if (!best || delta < bestDelta) {
        best = item;
        bestDelta = delta;
      }
    });
    return best;
  }

  function createLocalTrainMatcher(localItems) {
    var normalizedLocal = (Array.isArray(localItems) ? localItems : []).map(extractLocalTrain).filter(Boolean);
    var exactIdIndex = new Map();
    var sameDayTrainIndex = new Map();
    var routeTimeIndex = new Map();

    normalizedLocal.forEach(function (item) {
      if (item.localTrainId && !exactIdIndex.has(item.localTrainId)) {
        exactIdIndex.set(item.localTrainId, item);
      }
      var sameDayKey = item.serviceDate + "\n" + item.trainNumber;
      if (!sameDayTrainIndex.has(sameDayKey)) {
        sameDayTrainIndex.set(sameDayKey, []);
      }
      sameDayTrainIndex.get(sameDayKey).push(item);
      var routeTimeKey = item.serviceDate + "\n" + item.originKey + "\n" + item.destinationKey;
      if (!routeTimeIndex.has(routeTimeKey)) {
        routeTimeIndex.set(routeTimeKey, []);
      }
      routeTimeIndex.get(routeTimeKey).push(item);
    });

    return function matchPreparedLocalTrain(externalTrain) {
      var external = extractExternalTrain(externalTrain);
      if (!external || !external.serviceDate) {
        return null;
      }

      if (external.trainNumber) {
        var exactId = external.serviceDate + "-train-" + external.trainNumber;
        var exact = exactIdIndex.get(exactId) || null;
        if (exact) {
          return {
            match: exact.match,
            matchType: "exact-id",
            localTrainId: exact.localTrainId,
          };
        }

        var sameNumber = bestLocalTrainMatch(
          sameDayTrainIndex.get(external.serviceDate + "\n" + external.trainNumber),
          external.departureMinutes,
          null
        );
        if (sameNumber) {
          return {
            match: sameNumber.match,
            matchType: "train-number-same-day",
            localTrainId: sameNumber.localTrainId,
          };
        }
      }

      if (!external.originKey || !external.destinationKey || external.departureMinutes === null) {
        return null;
      }

      var routeTime = bestLocalTrainMatch(
        routeTimeIndex.get(external.serviceDate + "\n" + external.originKey + "\n" + external.destinationKey),
        external.departureMinutes,
        2
      );
      if (!routeTime) {
        return null;
      }
      return {
        match: routeTime.match,
        matchType: "route-time-window",
        localTrainId: routeTime.localTrainId,
      };
    };
  }

  function matchLocalTrain(externalTrain, localItems) {
    var external = Array.isArray(externalTrain) ? localItems : externalTrain;
    var locals = Array.isArray(externalTrain) ? externalTrain : localItems;
    return createLocalTrainMatcher(locals)(external);
  }

  function trimTrailingSlash(value) {
    return stringValue(value).replace(/\/+$/, "");
  }

  function createExternalTrainMapClient(options) {
    var opts = options || {};
    var fetchImpl = typeof opts.fetchImpl === "function"
      ? opts.fetchImpl
      : typeof fetch === "function"
        ? fetch.bind(typeof globalThis !== "undefined" ? globalThis : null)
        : null;
    var WebSocketCtor = typeof opts.WebSocketCtor === "function"
      ? opts.WebSocketCtor
      : typeof WebSocket === "function"
        ? WebSocket
        : null;
    var setTimer = typeof opts.setTimeoutFn === "function" ? opts.setTimeoutFn : setTimeout;
    var clearTimer = typeof opts.clearTimeoutFn === "function" ? opts.clearTimeoutFn : clearTimeout;
    var onState = typeof opts.onState === "function" ? opts.onState : null;
    var initialGraphState = opts.enabled === false
      ? "disabled"
      : resolveGraphURL()
        ? "idle"
        : "disabled";
    var state = {
      enabled: opts.enabled !== false,
      connectionState: opts.enabled === false ? "disabled" : "idle",
      graphState: initialGraphState,
      routes: [],
      liveTrains: [],
      activeStops: [],
      lastGraphAt: "",
      lastMessageAt: "",
      connectionError: "",
      graphError: "",
      error: "",
    };

    var socket = null;
    var reconnectTimer = null;
    var reconnectDelayMs = 1000;
    var stopped = false;
    var liveTrainMemory = new Map();

    function resolveGraphURL() {
      if (!fetchImpl) {
        return "";
      }
      var explicit = stringValue(opts.graphURL);
      if (explicit) {
        return explicit;
      }
      var baseURL = trimTrailingSlash(opts.baseURL);
      return baseURL ? (baseURL + GRAPH_PATH) : "";
    }

    function snapshot() {
      return {
        enabled: state.enabled,
        connectionState: state.connectionState,
        graphState: state.graphState,
        routes: state.routes.slice(),
        liveTrains: state.liveTrains.slice(),
        activeStops: state.activeStops.slice(),
        lastGraphAt: state.lastGraphAt,
        lastMessageAt: state.lastMessageAt,
        connectionError: state.connectionError,
        graphError: state.graphError,
        error: state.error,
      };
    }

    function emit() {
      if (onState) {
        onState(snapshot());
      }
    }

    function clearReconnectTimer() {
      if (!reconnectTimer) {
        return;
      }
      clearTimer(reconnectTimer);
      reconnectTimer = null;
    }

    function disconnectSocket() {
      clearReconnectTimer();
      var currentSocket = socket;
      socket = null;
      if (currentSocket && currentSocket.readyState <= 1) {
        try {
          currentSocket.close();
        } catch (_) {}
      }
    }

    function scheduleReconnect() {
      if (stopped || !state.enabled || reconnectTimer) {
        return;
      }
      var delay = reconnectDelayMs;
      reconnectDelayMs = Math.min(reconnectDelayMs * 2, 30000);
      reconnectTimer = setTimer(function () {
        reconnectTimer = null;
        connect();
      }, delay);
    }

    function connect() {
      if (stopped || !state.enabled || !WebSocketCtor) {
        return;
      }
      if (socket && socket.readyState <= 1) {
        return;
      }
      state.connectionState = "connecting";
      state.connectionError = "";
      state.error = "";
      emit();
      var currentSocket = new WebSocketCtor(opts.wsURL);
      socket = currentSocket;
      currentSocket.addEventListener("open", function () {
        if (socket !== currentSocket) {
          return;
        }
        reconnectDelayMs = 1000;
        state.connectionState = "live";
        state.connectionError = "";
        state.error = "";
        emit();
      });
      currentSocket.addEventListener("message", function (event) {
        if (socket !== currentSocket) {
          return;
        }
        var payload;
        try {
          payload = JSON.parse(event.data);
        } catch (_) {
          return;
        }
        if (payload && payload.type === "back-end") {
          var nextLiveTrains = normalizeBackEndFrame(payload);
          state.liveTrains = stabilizeLiveTrainPositions(nextLiveTrains, liveTrainMemory, Date.now());
        }
        if (payload && payload.type === "active-stops") {
          var nextActiveStops = normalizeActiveStopsFrame(payload);
          state.activeStops = Array.isArray(nextActiveStops)
            ? nextActiveStops
            : nextActiveStops
              ? [nextActiveStops]
              : [];
        }
        if (payload && (payload.type === "back-end" || payload.type === "active-stops")) {
          state.lastMessageAt = new Date().toISOString();
          emit();
        }
      });
      currentSocket.addEventListener("error", function () {
        if (socket !== currentSocket) {
          return;
        }
        state.connectionError = "external websocket error";
        state.error = state.connectionError;
        emit();
      });
      currentSocket.addEventListener("close", function () {
        if (socket !== currentSocket) {
          return;
        }
        socket = null;
        if (stopped) {
          return;
        }
        state.connectionState = "offline";
        emit();
        scheduleReconnect();
      });
    }

    function loadGraph() {
      if (!state.enabled) {
        state.graphState = "disabled";
        state.graphError = "";
        emit();
        return Promise.resolve(snapshot());
      }
      var graphURL = resolveGraphURL();
      if (!graphURL) {
        state.graphState = "disabled";
        state.graphError = "";
        emit();
        return Promise.resolve(snapshot());
      }
      state.graphState = "loading";
      state.graphError = "";
      emit();
      return fetchImpl(graphURL, { method: "GET" })
        .then(function (response) {
          if (!response.ok) {
            throw new Error("external route graph request failed");
          }
          return response.json();
        })
        .then(function (payload) {
          state.routes = normalizeTrainGraphPayload(payload).routes;
          state.lastGraphAt = new Date().toISOString();
          state.graphState = "ready";
          state.graphError = "";
          emit();
          return snapshot();
        })
        .catch(function (error) {
          state.graphState = "unavailable";
          state.graphError = error && error.message ? error.message : String(error);
          emit();
          return snapshot();
        });
    }

    function start() {
      if (!state.enabled) {
        state.connectionState = "disabled";
        state.graphState = "disabled";
        emit();
        return Promise.resolve(snapshot());
      }
      stopped = false;
      connect();
      return loadGraph().then(function () {
        return snapshot();
      });
    }

    function restart() {
      if (!state.enabled) {
        state.connectionState = "disabled";
        state.graphState = "disabled";
        emit();
        return Promise.resolve(snapshot());
      }
      stopped = false;
      disconnectSocket();
      state.connectionState = "idle";
      state.connectionError = "";
      state.error = "";
      emit();
      connect();
      return loadGraph().then(function () {
        return snapshot();
      });
    }

    function stop() {
      stopped = true;
      disconnectSocket();
      state.connectionState = "disabled";
      state.graphState = "disabled";
      state.connectionError = "";
      state.error = "";
      emit();
    }

    return {
      start: start,
      restart: restart,
      stop: stop,
      snapshot: snapshot,
      loadGraph: loadGraph,
    };
  }

  return {
    createLocalTrainMatcher: createLocalTrainMatcher,
    createExternalTrainMapClient: createExternalTrainMapClient,
    mapConfigMarkerSignature: mapConfigMarkerSignature,
    mapConfigSignature: mapConfigSignature,
    matchLocalTrain: matchLocalTrain,
    normalizeActiveStopsFrame: normalizeActiveStopsFrame,
    normalizeBackEndFrame: normalizeBackEndFrame,
    normalizeStationKey: normalizeStationKey,
    normalizeTrainGraphPayload: normalizeTrainGraphPayload,
    stabilizeLiveTrainPositions: stabilizeLiveTrainPositions,
    planMarkerReconcile: planMarkerReconcile,
    sameExternalFeedState: sameExternalFeedState,
    sameMaterialValue: sameMaterialValue,
    sameNetworkMapPayload: sameNetworkMapPayload,
    samePublicDashboard: samePublicDashboard,
    samePublicStationDepartures: samePublicStationDepartures,
    stableExternalTrainIdentity: stableExternalTrainIdentity,
    sameTrainStopsPayload: sameTrainStopsPayload,
    stableMaterialValue: stableMaterialValue,
    stableSerialize: stableSerialize,
  };
});
