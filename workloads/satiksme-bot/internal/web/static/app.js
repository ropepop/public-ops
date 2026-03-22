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
  var state = {
    catalog: null,
    sightings: { stopSightings: [], vehicleSightings: [] },
    vehicles: [],
    selectedStop: null,
    selectedDepartures: [],
    selectedDeparturesError: "",
    selectedDeparturesLoading: false,
    nearbyDepartures: [],
    nearbyDeparturesDegraded: false,
    authenticated: false,
    authState: "unknown",
    map: null,
    markers: new Map(),
    vehicleMarkers: new Map(),
    currentPosition: null,
    liveVehiclesRefreshTimer: null,
    liveRefreshInFlight: false,
  };

  var liveDeparturesUnavailableMessage = "Tiešraides atiešanas laiki no oficiālā Rīgas Satiksmes avota pašlaik nav pieejami.";
  var liveDeparturesLoadingMessage = "Ielādē atiešanas laikus...";
  var liveDeparturesTimeZone = "Europe/Riga";
  var staleDepartureGraceSeconds = 90;
  var departureRolloverThresholdSeconds = 2 * 3600;
  var sightingsFetchLimit = 24;
  var liveMapRefreshMs = 15000;
  var liveVehicleAnimationMinMs = 300;
  var stopMarkerRadiusMin = 2.33;
  var stopMarkerRadiusMax = 15;
  var stopMarkerRadiusMinHeightMeters = 1000;
  var stopMarkerRadiusMaxHeightMeters = 50;
  var reportConfirmWindowMs = 2500;
  var reportConfirmTimers = typeof root.WeakMap === "function" ? new root.WeakMap() : null;
  var armedReportButton = null;

  function pathFor(path) {
    var base = String(config.basePath || "").replace(/\/$/, "");
    if (!base) {
      return path;
    }
    return base + path;
  }

  function fetchJSON(url, options) {
    return fetch(url, options).then(function (response) {
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

  function departureClock(seconds) {
    var total = Number(seconds) || 0;
    var hours = Math.floor(total / 3600) % 24;
    var minutes = Math.floor((total % 3600) / 60);
    return pad(hours) + ":" + pad(minutes);
  }

  function timeZoneClockParts(value, timeZone) {
    var at = value instanceof Date ? value : new Date(value);
    if (!Number.isFinite(at.getTime())) {
      return null;
    }
    var formatter = new Intl.DateTimeFormat("en-GB", {
      timeZone: timeZone,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    });
    var hours = 0;
    var minutes = 0;
    var seconds = 0;
    formatter.formatToParts(at).forEach(function (part) {
      var numeric = parseInt(part.value, 10);
      if (!Number.isFinite(numeric)) {
        return;
      }
      if (part.type === "hour") {
        hours = numeric;
      } else if (part.type === "minute") {
        minutes = numeric;
      } else if (part.type === "second") {
        seconds = numeric;
      }
    });
    return { hours: hours, minutes: minutes, seconds: seconds };
  }

  function secondsOfDayInTimeZone(value, timeZone) {
    var parts = timeZoneClockParts(value, timeZone);
    if (!parts) {
      return 0;
    }
    return parts.hours * 3600 + parts.minutes * 60 + parts.seconds;
  }

  function departureTiming(seconds, now, timeZone) {
    var departureSeconds = Number(seconds);
    if (!Number.isFinite(departureSeconds)) {
      return null;
    }
    var currentSeconds = secondsOfDayInTimeZone(now || new Date(), timeZone || liveDeparturesTimeZone);
    var normalizedDepartureSeconds = departureSeconds;
    if (normalizedDepartureSeconds < currentSeconds - departureRolloverThresholdSeconds) {
      normalizedDepartureSeconds += 24 * 3600;
    }
    var deltaSeconds = normalizedDepartureSeconds - currentSeconds;
    if (deltaSeconds < -staleDepartureGraceSeconds) {
      return null;
    }
    return {
      normalizedDepartureSeconds: normalizedDepartureSeconds,
      minutesAway: Math.max(0, Math.trunc(deltaSeconds / 60)),
    };
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

  function shouldRenderStopMarker(zoom, selected, sightingCount) {
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

  function vehicleMarkerProfile(zoom) {
    if (mapZoomTier(zoom) === "detail") {
      return {
        className: "vehicle-marker-host",
        iconSize: [34, 34],
        iconAnchor: [17, 17],
        showRoute: true,
        showBadge: true,
        compact: false,
      };
    }
    return {
      className: "vehicle-marker-host vehicle-marker-host-compact",
      iconSize: [14, 14],
      iconAnchor: [7, 7],
      showRoute: false,
      showBadge: false,
      compact: true,
    };
  }

  function parseDepartures(raw, now) {
    var lines = String(raw || "")
      .split(/\r?\n/)
      .map(function (line) {
        return line.trim();
      })
      .filter(Boolean);
    var stopId = "";
    var departures = [];
    lines.forEach(function (line, index) {
      var parts = line.split(",");
      if (index === 0 && parts[0] === "stop") {
        stopId = parts[1] || "";
        return;
      }
      if (parts.length < 6) {
        return;
      }
      var departureSeconds = parseInt(parts[3], 10);
      if (!Number.isFinite(departureSeconds)) {
        return;
      }
      var timing = departureTiming(departureSeconds, now || new Date(), liveDeparturesTimeZone);
      if (!timing) {
        return;
      }
      departures.push({
        mode: parts[0],
        routeLabel: parts[1],
        direction: parts[2],
        departureSeconds: departureSeconds,
        liveRowId: parts[4],
        destination: parts.slice(5).join(","),
        departureClock: departureClock(departureSeconds),
        minutesAway: timing.minutesAway,
        normalizedDepartureSeconds: timing.normalizedDepartureSeconds,
      });
    });
    departures.sort(function (a, b) {
      return a.normalizedDepartureSeconds - b.normalizedDepartureSeconds;
    });
    return { stopId: stopId, departures: departures };
  }

  function renderDepartureMeta(item) {
    var clock = String(item && item.departureClock || "").trim();
    var minutes = Number(item && item.minutesAway);
    var hasMinutes = Number.isFinite(minutes);
    var metaClock = clock
      ? '<span class="departure-meta-clock">' + escapeHTML(clock) + "</span>"
      : "";
    var metaCountdown = hasMinutes
      ? '<span class="departure-meta-countdown">pēc ' + escapeHTML(String(Math.max(0, Math.trunc(minutes)))) + " min</span>"
      : "";
    if (!metaClock && !metaCountdown) {
      return "";
    }
    return '<div class="departure-meta">' + metaClock + metaCountdown + "</div>";
  }

  function renderDepartureRow(item, index, options) {
    var destination = String(item && item.destination || "").trim();
    var reportable = canReportDeparture(item);
    var mode = String(options && options.mode || config.mode || "public");
    var authenticated = !!(options && options.authenticated);
    return (
      '<li class="departure-row">' +
      '<span class="route-chip mode-' + escapeAttr(item.mode) + '">' + escapeHTML(modeAndRouteLabel(item.mode, item.routeLabel)) + "</span>" +
      '<div class="departure-copy">' +
      '<span class="departure-main' + (reportable ? "" : " departure-main-muted") + '">' + escapeHTML(destination || "Galamērķis nav norādīts") + "</span>" +
      (reportable ? "" : '<span class="departure-detail">Ziņošana nav pieejama bez galamērķa.</span>') +
      renderDepartureMeta(item) +
      "</div>" +
      (mode === "mini-app" && authenticated && reportable
        ? '<button class="tiny-button" data-action="report-vehicle" data-index="' + index + '">Kontrole</button>'
        : "") +
      "</li>"
    );
  }

  function buildVehicleReportPayload(stopId, item) {
    return {
      stopId: stopId,
      mode: item.mode,
      routeLabel: item.routeLabel,
      direction: item.direction,
      destination: item.destination,
      departureSeconds: item.departureSeconds,
      liveRowId: item.liveRowId || "",
    };
  }

  function canReportDeparture(item) {
    if (!item) {
      return false;
    }
    return !!(
      String(item.mode || "").trim() &&
      String(item.routeLabel || "").trim() &&
      String(item.destination || "").trim()
    );
  }

  function normalizeStopKey(value) {
    var trimmed = String(value || "").trim();
    if (!trimmed) {
      return "";
    }
    var normalized = trimmed.replace(/^0+/, "");
    return normalized || "0";
  }

  function normalizeDirection(value) {
    return String(value || "").trim().replace(/>/g, "-");
  }

  function secondsDistance(a, b) {
    var diff = Math.abs((Number(a) || 0) - (Number(b) || 0));
    var day = 24 * 3600;
    if (diff > day / 2) {
      diff = day - diff;
    }
    return diff;
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
    if (mode !== "mini-app" || !authenticated || !vehicle) {
      return false;
    }
    return !!String(vehicle.stopId || "").trim();
  }

  function buildLiveVehicleFallbackReportPayload(vehicle) {
    if (!vehicle) {
      return null;
    }
    var stopId = String(vehicle.stopId || "").trim();
    var mode = String(vehicle.mode || "").trim();
    var routeLabel = String(vehicle.routeLabel || "").trim();
    var destination = String(vehicle.stopName || vehicle.stopId || "").trim();
    if (!stopId || !mode || !routeLabel || !destination) {
      return null;
    }
    return {
      stopId: stopId,
      mode: mode,
      routeLabel: routeLabel,
      direction: String(vehicle.direction || "").trim(),
      destination: destination,
      departureSeconds: Number(vehicle.arrivalSeconds) || 0,
      liveRowId: "",
    };
  }

  function matchDepartureToVehicle(vehicle, departures) {
    if (!vehicle || !Array.isArray(departures) || !departures.length) {
      return null;
    }
    var targetMode = String(vehicle.mode || "").trim().toLowerCase();
    var targetRoute = String(vehicle.routeLabel || "").trim();
    var targetDirection = normalizeDirection(vehicle.direction);
    var targetArrivalSeconds = Number(vehicle.arrivalSeconds) || 0;
    var best = null;
    var bestScore = Infinity;
    departures.forEach(function (item) {
      if (!item) {
        return;
      }
      if (String(item.mode || "").trim().toLowerCase() !== targetMode) {
        return;
      }
      if (String(item.routeLabel || "").trim() !== targetRoute) {
        return;
      }
      var score = 0;
      var departureDirection = normalizeDirection(item.direction);
      if (targetDirection && departureDirection) {
        if (targetDirection !== departureDirection) {
          return;
        }
      } else {
        score += 10;
      }
      if (targetArrivalSeconds > 0 && (Number(item.departureSeconds) || 0) > 0) {
        score += secondsDistance(targetArrivalSeconds, item.departureSeconds);
      } else {
        score += 300;
      }
      if (!best || score < bestScore) {
        best = item;
        bestScore = score;
      }
    });
    return best;
  }

  function resolveLiveVehicleReportPayload(vehicle) {
    if (!vehicle) {
      return Promise.reject(new Error("Transports vairs nav pieejams"));
    }
    var stopId = String(vehicle.stopId || "").trim();
    if (!stopId) {
      return Promise.reject(new Error("Transportam nav pieturas datu, ziņojumu nevar iesniegt"));
    }
    if (
      state.selectedStop &&
      String(state.selectedStop.id || "").trim() === stopId &&
      Array.isArray(state.selectedDepartures) &&
      state.selectedDepartures.length
    ) {
      var selectedMatch = matchDepartureToVehicle(vehicle, state.selectedDepartures);
      if (selectedMatch) {
        return Promise.resolve(buildVehicleReportPayload(stopId, selectedMatch));
      }
    }
    return fetchLiveDepartures(stopId)
      .then(function (parsed) {
        var match = matchDepartureToVehicle(vehicle, parsed && parsed.departures);
        return match ? buildVehicleReportPayload(stopId, match) : buildLiveVehicleFallbackReportPayload(vehicle);
      })
      .catch(function () {
        return buildLiveVehicleFallbackReportPayload(vehicle);
      })
      .then(function (payload) {
        if (!payload) {
          throw new Error("Neizdevās noteikt transporta maršrutu ziņojumam");
        }
        return payload;
      });
  }

  function sightingTimestampMs(value) {
    if (!value) {
      return 0;
    }
    var at = value instanceof Date ? value : new Date(value);
    var timestampMs = at.getTime();
    return Number.isFinite(timestampMs) ? timestampMs : 0;
  }

  function latestReportTimestampForStop(stopId, sightings) {
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
    (sightings.vehicleSightings || []).forEach(function (item) {
      var itemStopId = String(item.stopId || "").trim();
      if (itemStopId === targetStopId || normalizeStopKey(itemStopId) === normalizedTargetStopId) {
        latestMs = Math.max(latestMs, sightingTimestampMs(item.createdAt));
      }
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

  function latestReportAgeLabel(stopId, sightings, now) {
    var latestMs = latestReportTimestampForStop(stopId, sightings);
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

  function renderReportControls(mode, authenticated, selectedStop, departures, sightings, now) {
    if (mode !== "mini-app" || !authenticated || !selectedStop) {
      return "";
    }
    var vehicleButtons = (departures || [])
      .map(function (item, index) {
        if (!canReportDeparture(item)) {
          return "";
        }
        return (
          '<button class="action action-secondary action-compact" data-action="report-vehicle" data-index="' +
          index +
          '">Kontrole ' +
          escapeHTML(modeAndRouteLabel(item.mode, item.routeLabel)) +
          "</button>"
        );
      })
      .join("");
    return vehicleButtons ? '<div class="report-actions report-actions-minimal">' + vehicleButtons + "</div>" : "";
  }

  function renderStopSightingControl(mode, authenticated, selectedStop, sightings, now) {
    if (mode !== "mini-app" || !authenticated || !selectedStop) {
      return "";
    }
    return (
      '<div class="report-stop-inline stop-detail-actions">' +
      '<button class="action action-danger action-compact" data-action="report-stop">Pieturas kontrole</button>' +
      '<span class="report-age">' + escapeHTML(latestReportAgeLabel(selectedStop.id, sightings, now)) + "</span>" +
      "</div>"
    );
  }

  function groupSightingsByStop(sightings) {
    var counts = Object.create(null);
    (sightings.stopSightings || []).forEach(function (item) {
      counts[item.stopId] = (counts[item.stopId] || 0) + 1;
    });
    return counts;
  }

  function findStop(stopId) {
    if (!state.catalog || !state.catalog.stops) {
      return null;
    }
    for (var i = 0; i < state.catalog.stops.length; i += 1) {
      if (state.catalog.stops[i].id === stopId) {
        return state.catalog.stops[i];
      }
    }
    return null;
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

  function boot() {
    if (!root.document || !document.getElementById) {
      return;
    }
    var app = document.getElementById("app");
    if (!app) {
      return;
    }
    app.innerHTML =
      '<div class="shell">' +
      '<header class="hero">' +
      '<div class="hero-copy"><h1>Kontroles mape</h1><p class="lede">Tiešraides atiešanas laiki, pieturu novērojumi un kontroles ziņojumi vienuviet.</p></div>' +
      '<div class="hero-meta"><span id="status-pill" class="pill pill-muted">Ielādē…</span></div>' +
      "</header>" +
      '<section class="layout">' +
      '<div class="map-panel"><div id="map" class="map"></div></div>' +
      '<aside class="sidebar">' +
      '<section class="card"><h2>Izvēlētā pietura</h2><div id="selected-stop">Izvēlies pieturu mapē.</div></section>' +
      '<section class="card"><h2>Tuvākie atiešanas laiki</h2><div id="nearby-departures">Gaida atrašanās vietu…</div></section>' +
      '<section class="card"><h2>Jaunākie ziņojumi</h2><div id="recent-sightings">Ielādē…</div></section>' +
      "</aside>" +
      "</section>" +
      "</div>";

    initMap();
    bindActions();
    Promise.resolve()
      .then(function () {
        return Promise.all([loadBootstrap(), maybeAuthenticate()]);
      })
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
    var syncVisibleMapLayers = function () {
      renderVisibleStops();
      renderLiveVehicles();
    };
    state.map.on("moveend", syncVisibleMapLayers);
    state.map.on("zoomend", syncVisibleMapLayers);
  }

  function loadCatalog() {
    return fetchJSON(pathFor("/api/v1/public/catalog")).then(function (payload) {
      state.catalog = payload;
      renderVisibleStops();
      setStatus("Katalogs ielādēts");
      return payload;
    });
  }

  function loadBootstrap() {
    return fetchJSON(pathFor("/api/v1/public/map?limit=" + sightingsFetchLimit))
      .then(function (payload) {
        state.catalog = {
          generatedAt: payload.generatedAt,
          stops: payload.stops || [],
        };
        state.sightings = payload.sightings || { stopSightings: [], vehicleSightings: [] };
        state.vehicles = payload.liveVehicles || [];
        renderVisibleStops();
        renderLiveVehicles();
        renderSightings();
        setStatus("Mape gatava");
        return payload;
      })
      .catch(function () {
        return Promise.all([loadCatalog(), loadSightings()]);
      });
  }

  function loadSightings() {
    return fetchJSON(pathFor("/api/v1/public/sightings?limit=" + sightingsFetchLimit)).then(function (payload) {
      state.sightings = payload;
      renderVisibleStops();
      renderLiveVehicles();
      renderSightings();
      return payload;
    });
  }

  function loadLiveVehicles() {
    return fetchJSON(pathFor("/api/v1/public/live-vehicles")).then(function (payload) {
      state.vehicles = payload.liveVehicles || [];
      renderLiveVehicles();
      return payload;
    });
  }

  function loadLiveMapState() {
    return Promise.all([
      fetchJSON(pathFor("/api/v1/public/sightings?limit=" + sightingsFetchLimit)),
      fetchJSON(pathFor("/api/v1/public/live-vehicles")),
    ]).then(function (result) {
      state.sightings = result[0] || { stopSightings: [], vehicleSightings: [] };
      state.vehicles = (result[1] && result[1].liveVehicles) || [];
      renderVisibleStops();
      renderLiveVehicles();
      renderSightings();
      return {
        sightings: state.sightings,
        liveVehicles: state.vehicles,
      };
    });
  }

  function refreshSelectedDepartures(silent) {
    if (!state.selectedStop) {
      return Promise.resolve(null);
    }
    if (!silent) {
      state.selectedDeparturesLoading = true;
      renderSelectedStop();
    }
    return fetchLiveDepartures(state.selectedStop.id)
      .then(function (parsed) {
        state.selectedDepartures = parsed.departures;
        state.selectedDeparturesError = "";
        state.selectedDeparturesLoading = false;
        renderSelectedStop();
        return parsed;
      })
      .catch(function (error) {
        state.selectedDeparturesError = liveDeparturesUnavailableMessage;
        state.selectedDeparturesLoading = false;
        if (!silent) {
          setStatus(error.message || liveDeparturesUnavailableMessage);
        }
        renderSelectedStop();
        return null;
      });
  }

  function refreshLiveMap() {
    if (state.liveRefreshInFlight) {
      return Promise.resolve(null);
    }
    state.liveRefreshInFlight = true;
    return Promise.all([loadLiveMapState(), refreshSelectedDepartures(true)]).then(function (result) {
      return result[0];
    }).finally(function () {
      state.liveRefreshInFlight = false;
    });
  }

  function startLiveMapPolling() {
    if (state.liveVehiclesRefreshTimer) {
      clearInterval(state.liveVehiclesRefreshTimer);
    }
    refreshLiveMap().catch(function (error) {
      setStatus("Tiešraides transports nav pieejams");
    });
    state.liveVehiclesRefreshTimer = setInterval(function () {
      refreshLiveMap().catch(function () {
        return null;
      });
    }, liveMapRefreshMs);
  }

  function maybeAuthenticate() {
    var mode = String(config.mode || "public");
    state.authenticated = false;
    if (mode !== "mini-app") {
      state.authState = "public";
      return Promise.resolve(null);
    }
    var tg = root.Telegram && root.Telegram.WebApp;
    if (!tg || !tg.initData) {
      state.authState = "telegram_required";
      setStatus("Mape gatava. Atver Telegram, lai ziņotu par kontroli.");
      renderSelectedStop();
      return Promise.resolve(null);
    }
    return fetchJSON(pathFor("/api/v1/auth/telegram"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ initData: tg.initData }),
      credentials: "same-origin",
    })
      .then(function () {
        state.authenticated = true;
        state.authState = "authenticated";
        setStatus("Telegram sesija aktīva");
        renderSelectedStop();
      })
      .catch(function () {
        state.authState = "auth_failed";
        setStatus("Neizdevās autorizēties Telegram");
        renderSelectedStop();
      });
  }

  function requestLocation() {
    if (!navigator.geolocation) {
      applyInitialView(null);
      return Promise.resolve();
    }
    return new Promise(function (resolve) {
      navigator.geolocation.getCurrentPosition(
        function (position) {
          state.currentPosition = {
            latitude: position.coords.latitude,
            longitude: position.coords.longitude,
          };
          applyInitialView(position);
          resolve();
        },
        function () {
          applyInitialView(null);
          resolve();
        },
        { enableHighAccuracy: true, timeout: 8000, maximumAge: 60000 }
      );
    });
  }

  function applyInitialView(position) {
    var view = resolveInitialView(position);
    if (state.map) {
      state.map.setView([view.lat, view.lng], view.zoom);
    }
    selectInitialStop();
    renderNearbyDepartures();
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

  function renderVisibleStops() {
    if (!state.map || !state.catalog || !state.catalog.stops) {
      return;
    }
    var bounds = state.map.getBounds();
    var visibleHeightMeters = boundsHeightMeters(bounds);
    var counts = groupSightingsByStop(state.sightings);
    var zoom = currentMapZoom();
    state.catalog.stops.forEach(function (stop) {
      var count = counts[stop.id] || 0;
      var selected = !!(state.selectedStop && state.selectedStop.id === stop.id);
      var inBounds = !bounds || bounds.contains([stop.latitude, stop.longitude]);
      if (inBounds && shouldRenderStopMarker(zoom, selected, count)) {
        var marker = state.markers.get(stop.id);
        var style = stopMarkerStyle(zoom, selected, count, visibleHeightMeters);
        if (!marker) {
          marker = root.L.circleMarker([stop.latitude, stop.longitude], style);
          marker.on("click", function () {
            selectStop(stop.id);
          });
          marker.addTo(state.map);
          state.markers.set(stop.id, marker);
        }
        marker.setStyle(style);
        if (shouldShowStopBadge(zoom, count)) {
          var badgeHTML = '<span class="map-badge-stop__value">' + escapeHTML(String(count)) + "</span>";
          if (!marker.getTooltip()) {
            marker.bindTooltip(badgeHTML, {
              permanent: true,
              direction: "top",
              className: "map-badge-stop",
              offset: [10, -6],
            });
          } else {
            marker.setTooltipContent(badgeHTML);
          }
          marker.openTooltip();
        } else if (marker.getTooltip()) {
          marker.unbindTooltip();
        }
      } else if (state.markers.has(stop.id)) {
        state.map.removeLayer(state.markers.get(stop.id));
        state.markers.delete(stop.id);
      }
    });
  }

  function buildVehicleMarkerIcon(vehicle, zoom) {
    var profile = vehicleMarkerProfile(zoom);
    return root.L.divIcon({
      className: profile.className,
      html: buildVehicleMarkerHTML(vehicle, profile),
      iconSize: profile.iconSize,
      iconAnchor: profile.iconAnchor,
    });
  }

  function syncVehicleMarkerPopup(marker, vehicle) {
    if (!marker) {
      return;
    }
    var popup = typeof marker.getPopup === "function" ? marker.getPopup() : null;
    if (!popup) {
      marker.bindPopup(buildVehiclePopupHTML(vehicle, {
        mode: String(config.mode || "public"),
        authenticated: state.authenticated,
      }));
      return;
    }
    if (typeof popup.setContent === "function") {
      popup.setContent(buildVehiclePopupHTML(vehicle, {
        mode: String(config.mode || "public"),
        authenticated: state.authenticated,
      }));
    }
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
      entry.marker.setLatLng(nextLatLng);
      entry.positionLatLng = nextLatLng.slice();
      entry.targetLatLng = nextLatLng.slice();
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
  }

  function renderLiveVehicles() {
    if (!state.map) {
      return;
    }
    var zoom = currentMapZoom();
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
        var marker = root.L.marker([vehicle.latitude, vehicle.longitude], {
          icon: buildVehicleMarkerIcon(vehicle, zoom),
        });
        syncVehicleMarkerPopup(marker, vehicle);
        marker.addTo(state.map);
        state.vehicleMarkers.set(vehicle.id, {
          marker: marker,
          vehicle: vehicle,
          animationFrame: 0,
          positionLatLng: [vehicle.latitude, vehicle.longitude],
          targetLatLng: [vehicle.latitude, vehicle.longitude],
          positionObservedAt: vehicleMovementTimestampMs(vehicle),
          lastMovementSyncAt: Date.now(),
        });
        return;
      }
      var entry = state.vehicleMarkers.get(vehicle.id);
      entry.vehicle = vehicle;
      if (typeof entry.marker.setIcon === "function") {
        entry.marker.setIcon(buildVehicleMarkerIcon(vehicle, zoom));
      }
      syncVehicleMarkerPopup(entry.marker, vehicle);
      animateVehicleMarkerTo(entry, vehicle);
    });
    state.vehicleMarkers.forEach(function (_, vehicleId) {
      if (!nextIds.has(vehicleId)) {
        removeVehicleMarkerEntry(vehicleId);
      }
    });
  }

  function selectStop(stopId) {
    state.selectedStop = findStop(stopId);
    state.selectedDepartures = [];
    state.selectedDeparturesError = "";
    state.selectedDeparturesLoading = !!state.selectedStop;
    renderVisibleStops();
    renderSelectedStop();
    if (!state.selectedStop) {
      return;
    }
    refreshSelectedDepartures(false);
  }

  function fetchLiveDepartures(stopId) {
    var mode = String(config.liveDeparturesMode || "proxy");
    var proxyEndpoint = String(config.liveDeparturesProxyEndpoint || "/api/v1/live/departures");
    if (mode === "proxy") {
      return fetchJSON(pathFor(proxyEndpoint) + "?stopId=" + encodeURIComponent(stopId))
        .then(function (payload) {
          return parseLiveDeparturesPayload(payload);
        })
        .catch(function () {
          return fetchDirectLiveDepartures(stopId);
        });
    }
    return fetchDirectLiveDepartures(stopId);
  }

  function parseLiveDeparturesPayload(payload) {
    if (!payload || typeof payload !== "object" || !Array.isArray(payload.departures)) {
      throw new Error("invalid live departures response");
    }
    var departures = payload.departures || [];
    return { stopId: String(payload.stopId || ""), departures: departures };
  }

  function fetchDirectLiveDepartures(stopId) {
    var base = String(config.liveDeparturesURL || "https://saraksti.rigassatiksme.lv/departures2.php").replace(/\/$/, "");
    var separator = base.indexOf("?") === -1 ? "?" : "&";
    return fetch(base + separator + "stopid=" + encodeURIComponent(stopId))
      .then(function (response) {
        return response.text();
      })
      .then(function (raw) {
        return parseDepartures(raw, new Date());
      });
  }

  function renderSelectedStop() {
    var rootNode = document.getElementById("selected-stop");
    if (!rootNode) {
      return;
    }
    if (!state.selectedStop) {
      rootNode.innerHTML = "<p>Izvēlies pieturu, lai redzētu atiešanas laikus un ziņojumus.</p>";
      return;
    }
    var departuresHtml = (state.selectedDepartures || [])
      .slice(0, 8)
      .map(function (item, index) {
        return renderDepartureRow(item, index, {
          mode: String(config.mode || "public"),
          authenticated: state.authenticated,
        });
      })
      .join("");
    var departuresFallback = state.selectedDeparturesLoading
      ? '<li class="departure-row departure-row-loading">' + escapeHTML(liveDeparturesLoadingMessage) + "</li>"
      : state.selectedDeparturesError
      ? '<li class="departure-row departure-row-error">' + escapeHTML(state.selectedDeparturesError) + "</li>"
      : "<li>Šobrīd nav tiešraides atiešanu.</li>";
    rootNode.innerHTML =
      '<div class="stop-heading"><h3>' + escapeHTML(state.selectedStop.name) + '</h3><p>' + escapeHTML(((state.selectedStop.routeLabels || []).join(", ")) || "Nav maršrutu") + "</p></div>" +
      renderReportNote(String(config.mode || "public"), state.authState) +
      renderStopSightingControl(String(config.mode || "public"), state.authenticated, state.selectedStop, state.sightings, new Date()) +
      renderReportControls(String(config.mode || "public"), state.authenticated, state.selectedStop, state.selectedDepartures, state.sightings, new Date()) +
      '<ul class="departure-list">' + (departuresHtml || departuresFallback) + "</ul>";
  }

  function buildVehicleMarkerHTML(vehicle, profile) {
    var count = Number(vehicle.sightingCount) || 0;
    var markerProfile = profile || vehicleMarkerProfile(15);
    var lowFloorClass = markerProfile.compact || !vehicle.lowFloor ? "" : " vehicle-marker-low-floor";
    return (
      '<div class="vehicle-marker vehicle-mode-' + escapeAttr(vehicle.mode) + (markerProfile.compact ? " vehicle-marker-compact" : "") + lowFloorClass + '">' +
      (markerProfile.showRoute ? '<span class="vehicle-marker-route">' + escapeHTML(vehicle.routeLabel) + "</span>" : "") +
      (markerProfile.showBadge && count > 0 ? '<span class="vehicle-marker-badge">' + escapeHTML(String(count)) + "</span>" : "") +
      "</div>"
    );
  }

  function buildVehiclePopupHTML(vehicle, options) {
    var routeLabel = String(vehicle.routeLabel || "").trim();
    var vehicleCode = String(vehicle.vehicleCode || "").trim();
    var popupMode = String((options && options.mode) || config.mode || "public");
    var popupAuthenticated = !!(options && options.authenticated);
    var identityHtml = "";

    if (routeLabel) {
      identityHtml += '<span class="vehicle-popup-route">' + escapeHTML(routeLabel) + "</span>";
    }
    if (vehicleCode) {
      identityHtml += '<span class="vehicle-popup-id">' + escapeHTML(vehicleCode) + "</span>";
    }
    if (!identityHtml) {
      identityHtml = '<span class="vehicle-popup-empty">Transports tiešraidē</span>';
    }

    var actionsHtml = "";
    if (canReportLiveVehicle(popupMode, popupAuthenticated, vehicle)) {
      actionsHtml =
        '<div class="vehicle-popup-actions">' +
        '<button class="action action-secondary action-compact vehicle-popup-action" data-action="report-live-vehicle" data-vehicle-id="' +
        escapeHTML(vehicle.id) +
        '">Kontrole</button>' +
        "</div>";
    }

    return (
      '<div class="vehicle-popup vehicle-popup-mode-' + escapeAttr(vehicle.mode) + '">' +
      '<div class="vehicle-popup-identity">' + identityHtml + "</div>" +
      actionsHtml +
      "</div>"
    );
  }

  function renderNearbyDepartures() {
    var node = document.getElementById("nearby-departures");
    if (!node) {
      return;
    }
    var origin = state.currentPosition;
    if (!origin && state.selectedStop) {
      origin = { latitude: state.selectedStop.latitude, longitude: state.selectedStop.longitude };
    }
    if (!origin) {
      node.innerHTML = "<p>Atļauj atrašanās vietu, lai ielādētu tuvākās pieturas.</p>";
      return;
    }
    var candidates = nearestStops(origin, 3, state.selectedStop && state.selectedStop.id);
    Promise.all(
      candidates.map(function (stop) {
        return fetchLiveDepartures(stop.id)
          .then(function (parsed) {
            return { stop: stop, departures: parsed.departures.slice(0, 2), degraded: false };
          })
          .catch(function () {
            return { stop: stop, departures: [], degraded: true };
          });
      })
    ).then(function (items) {
      state.nearbyDeparturesDegraded = items.some(function (item) { return item.degraded; });
      node.innerHTML = items
        .map(function (item) {
          if (item.degraded) {
            return '<div class="nearby-stop"><h4>' + escapeHTML(item.stop.name) + '</h4><ul><li>' + escapeHTML(liveDeparturesUnavailableMessage) + "</li></ul></div>";
          }
          var departures = item.departures
            .map(function (dep) {
              return '<li>' + escapeHTML(modeAndRouteLabel(dep.mode, dep.routeLabel) + " · " + dep.destination + " · " + dep.departureClock) + "</li>";
            })
            .join("");
          return '<div class="nearby-stop"><h4>' + escapeHTML(item.stop.name) + '</h4><ul>' + (departures || "<li>Nav tiešraides atiešanu.</li>") + "</ul></div>";
        })
        .join("");
    });
  }

  function renderSightings() {
    var node = document.getElementById("recent-sightings");
    if (!node) {
      return;
    }
    var stopItems = (state.sightings.stopSightings || []).slice(0, 6).map(function (item) {
      return '<li>Pieturas kontrole · ' + escapeHTML(item.stopName || item.stopId) + ' · ' + escapeHTML(formatEventTime(item.createdAt)) + "</li>";
    });
    var vehicleItems = (state.sightings.vehicleSightings || []).slice(0, 6).map(function (item) {
      return '<li>' + escapeHTML(modeAndRouteLabel(item.mode, item.routeLabel) + " · " + item.destination + " · " + (item.stopName || item.stopId) + " · " + formatEventTime(item.createdAt)) + "</li>";
    });
    node.innerHTML = "<ul>" + (stopItems.concat(vehicleItems).join("") || "<li>Nav nesenu ziņojumu.</li>") + "</ul>";
  }

  function reportConfirmationPrompt(action) {
    if (action === "report-stop") {
      return "Nospied vēlreiz, lai apstiprinātu pieturas ziņojumu";
    }
    return "Nospied vēlreiz, lai apstiprinātu transporta ziņojumu";
  }

  function clearReportConfirmation(button) {
    var originalText = "";
    var timeoutId = 0;
    if (!button || !button.getAttribute) {
      return;
    }
    if (reportConfirmTimers && typeof reportConfirmTimers.get === "function") {
      timeoutId = reportConfirmTimers.get(button);
      if (timeoutId) {
        root.clearTimeout(timeoutId);
        reportConfirmTimers.delete(button);
      }
    }
    originalText = button.getAttribute("data-confirm-original");
    if (originalText) {
      button.textContent = originalText;
    }
    button.removeAttribute("data-confirm-original");
    button.removeAttribute("data-confirm-state");
    if (button.classList && typeof button.classList.remove === "function") {
      button.classList.remove("action-confirm-pending");
    }
    if (armedReportButton === button) {
      armedReportButton = null;
    }
  }

  function armReportConfirmation(button, action) {
    if (!button || !button.getAttribute) {
      return;
    }
    if (armedReportButton && armedReportButton !== button) {
      clearReportConfirmation(armedReportButton);
    }
    clearReportConfirmation(button);
    button.setAttribute("data-confirm-original", String(button.textContent || "").trim() || "Kontrole");
    button.setAttribute("data-confirm-state", "armed");
    button.textContent = "Apstiprini";
    if (button.classList && typeof button.classList.add === "function") {
      button.classList.add("action-confirm-pending");
    }
    if (reportConfirmTimers && typeof reportConfirmTimers.set === "function") {
      reportConfirmTimers.set(button, root.setTimeout(function () {
        clearReportConfirmation(button);
      }, reportConfirmWindowMs));
    }
    armedReportButton = button;
    setStatus(reportConfirmationPrompt(action));
  }

  function confirmReportAction(button, action) {
    if (!button || !button.getAttribute) {
      return false;
    }
    if (button.getAttribute("data-confirm-state") === "armed") {
      clearReportConfirmation(button);
      return true;
    }
    armReportConfirmation(button, action);
    return false;
  }

  function bindActions() {
    if (!document || !document.addEventListener) {
      return;
    }
    document.addEventListener("click", function (event) {
      var target = event.target;
      var button = null;
      var action = "";
      var index = -1;
      var departure = null;
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
      if (action === "report-stop" && state.selectedStop) {
        if (!confirmReportAction(button, action)) {
          return;
        }
        submitStopReport(state.selectedStop.id);
        return;
      }
      if (action === "report-live-vehicle") {
        if (!confirmReportAction(button, action)) {
          return;
        }
        submitLiveVehicleReport(button.getAttribute("data-vehicle-id"));
        return;
      }
      if (action === "report-vehicle" && state.selectedStop) {
        index = parseInt(button.getAttribute("data-index") || "-1", 10);
        if (index >= 0 && state.selectedDepartures[index]) {
          departure = state.selectedDepartures[index];
          if (!canReportDeparture(departure)) {
            setStatus("Transporta ziņojumam vajag galamērķi");
            return;
          }
          if (!confirmReportAction(button, action)) {
            return;
          }
          submitVehicleReport(buildVehicleReportPayload(state.selectedStop.id, departure));
        }
      }
    });
  }

  function submitStopReport(stopId) {
    return fetchJSON(pathFor("/api/v1/reports/stop"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ stopId: stopId }),
      credentials: "same-origin",
    })
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
    if (!vehicle) {
      setStatus("Transports vairs nav pieejams");
      return Promise.resolve(null);
    }
    setStatus("Ielādē transporta detaļas...");
    return resolveLiveVehicleReportPayload(vehicle)
      .then(function (payload) {
        return submitVehicleReport(payload);
      })
      .catch(function (error) {
        setStatus((error && error.message) || "Neizdevās sagatavot transporta kontroles ziņojumu");
        return null;
      });
  }

  function submitVehicleReport(payload) {
    return fetchJSON(pathFor("/api/v1/reports/vehicle"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      credentials: "same-origin",
    })
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

  function reportMessage(result, acceptedMessage) {
    if (result.accepted) {
      return acceptedMessage;
    }
    if (result.deduped) {
      return "Dublikāts ignorēts";
    }
    if (result.cooldownSeconds) {
      return "Jānogaida: " + result.cooldownSeconds + " s";
    }
    return "Ziņojums ignorēts";
  }

  function renderReportNote(mode, authState) {
    if (mode === "public") {
      return '<p class="report-note">Ziņošana pieejama tikai Telegram mini lietotnē.</p>';
    }
    if (authState === "authenticated") {
      return '<p class="report-note report-note-ready">Telegram sesija aktīva. Šajā skatā vari ziņot par kontroli.</p>';
    }
    if (authState === "auth_failed") {
      return '<p class="report-note report-note-warning">Telegram autorizācija neizdevās. Atver mini lietotni no Telegram vēlreiz.</p>';
    }
    return '<p class="report-note">Atver šo lapu no Telegram, lai ziņotu par kontroli.</p>';
  }

  function prependStopSighting(stopId) {
    var stop = findStop(stopId);
    state.sightings.stopSightings = [{
      id: "local-stop-" + Date.now(),
      stopId: stopId,
      stopName: stop ? stop.name : stopId,
      createdAt: new Date().toISOString(),
    }].concat(state.sightings.stopSightings || []).slice(0, sightingsFetchLimit);
    renderVisibleStops();
    renderSightings();
  }

  function prependVehicleSighting(payload) {
    var stop = findStop(payload.stopId);
    state.sightings.vehicleSightings = [{
      id: "local-vehicle-" + Date.now(),
      stopId: payload.stopId,
      stopName: stop ? stop.name : payload.stopId,
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
  }

  function escapeHTML(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function escapeAttr(value) {
    return String(value || "").replace(/[^a-z0-9_-]/gi, "-");
  }

  if (root.document && root.document.addEventListener) {
    root.document.addEventListener("DOMContentLoaded", boot);
  }

  return {
    __test__: {
      defaultCenter: defaultCenter,
      parseDepartures: parseDepartures,
      departureTiming: departureTiming,
      renderDepartureMeta: renderDepartureMeta,
      renderDepartureRow: renderDepartureRow,
      buildVehicleReportPayload: buildVehicleReportPayload,
      normalizeStopKey: normalizeStopKey,
      normalizeDirection: normalizeDirection,
      secondsDistance: secondsDistance,
      vehicleMovementTimestampMs: vehicleMovementTimestampMs,
      vehicleMovementDurationMs: vehicleMovementDurationMs,
      boundsHeightMeters: boundsHeightMeters,
      stopMarkerRadiusForHeight: stopMarkerRadiusForHeight,
      mapZoomTier: mapZoomTier,
      shouldRenderStopMarker: shouldRenderStopMarker,
      shouldShowStopBadge: shouldShowStopBadge,
      stopMarkerStyle: stopMarkerStyle,
      vehicleMarkerProfile: vehicleMarkerProfile,
      resolveInitialView: resolveInitialView,
      buildVehicleMarkerHTML: buildVehicleMarkerHTML,
      buildVehiclePopupHTML: buildVehiclePopupHTML,
      buildLiveVehicleFallbackReportPayload: buildLiveVehicleFallbackReportPayload,
      canReportLiveVehicle: canReportLiveVehicle,
      matchDepartureToVehicle: matchDepartureToVehicle,
      renderReportControls: renderReportControls,
      renderStopSightingControl: renderStopSightingControl,
      renderReportNote: renderReportNote,
      formatEventTime: formatEventTime,
      formatRelativeReportAge: formatRelativeReportAge,
      latestReportAgeLabel: latestReportAgeLabel,
      groupSightingsByStop: groupSightingsByStop,
      canReportDeparture: canReportDeparture,
    },
  };
});
